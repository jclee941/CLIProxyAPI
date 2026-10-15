package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// webResearchModel is the web app's Deep Research agent. The host sends an
// Interactions `agent` to the Gemini API, so this provider publishes the agent
// as a model; agent_config keeps its official meaning.
const webResearchModel = "gemini-deep-research"

// webResearchStart is what the web app's Start research button sends when a
// plan names no action of its own.
const webResearchStart = "Start research"

// webResearchLimit bounds the wait for a report: the agent researches for at
// most an hour, so a report that has not arrived well after that will not.
const webResearchLimit = 90 * time.Minute

func webResearchModelInfos(account accountModels) []modelInfo {
	if _, ok := webFamilyCapability(account.Models, "Flash"); !ok {
		return nil
	}
	return []modelInfo{{ID: webResearchModel, Object: "model", OwnedBy: provider, Type: provider, Name: webResearchModel, DisplayName: "Deep Research (Gemini)", Description: "The Gemini web app's Deep Research agent on the Interactions surface: background runs, collaborative planning and follow-up questions", SupportedGenerationMethods: []string{"interactions"}, SupportedInputModalities: []string{"text", "image", "document"}, SupportedOutputModalities: []string{"text"}}}
}

type researchRequest struct {
	Model       string          `json:"model"`
	Input       json.RawMessage `json:"input"`
	Previous    string          `json:"previous_interaction_id,omitempty"`
	Store       *bool           `json:"store,omitempty"`
	Background  bool            `json:"background,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
	AgentConfig struct {
		Type                  string `json:"type,omitempty"`
		ThinkingSummaries     string `json:"thinking_summaries,omitempty"`
		Visualization         string `json:"visualization,omitempty"`
		CollaborativePlanning bool   `json:"collaborative_planning,omitempty"`
	} `json:"agent_config,omitempty"`
}

// parseResearch holds a request to the official agent contract: background
// execution of a stored interaction, with an agent_config of type
// deep-research. Thinking summaries only shape a stream and this path draws no
// charts, so those two settings are accepted as given.
func parseResearch(raw []byte) (researchRequest, string, []webMedia, error) {
	var request researchRequest
	if len(raw) > interactionInputLimit || strictJSON(raw, &request) != nil || request.Model != webResearchModel {
		return request, "", nil, failure(400, "unsupported_research_request")
	}
	config := request.AgentConfig
	switch {
	case !request.Background:
		return request, "", nil, failure(400, "research_requires_background")
	case request.Store != nil && !*request.Store:
		return request, "", nil, failure(400, "research_requires_store")
	case request.Stream:
		return request, "", nil, failure(400, "research_stream_unsupported")
	case config.Type != "" && config.Type != "deep-research",
		config.ThinkingSummaries != "" && config.ThinkingSummaries != "none" && config.ThinkingSummaries != "auto",
		config.Visualization != "" && config.Visualization != "auto" && config.Visualization != "off":
		return request, "", nil, failure(400, "research_agent_config_invalid")
	}
	prompt, media, err := researchInput(request.Input)
	return request, prompt, media, err
}

func researchInput(raw json.RawMessage) (string, []webMedia, error) {
	var prompt string
	if json.Unmarshal(raw, &prompt) == nil {
		return prompt, nil, nil
	}
	var parts []struct {
		Type     string          `json:"type"`
		Text     string          `json:"text"`
		Data     string          `json:"data"`
		MIMEType string          `json:"mime_type"`
		Content  json.RawMessage `json:"content,omitempty"`
	}
	if strictJSON(raw, &parts) != nil || len(parts) == 0 {
		return "", nil, failure(400, "research_input_invalid")
	}
	if len(parts) == 1 && parts[0].Type == "user_input" {
		if strictJSON(parts[0].Content, &parts) != nil || len(parts) == 0 {
			return "", nil, failure(400, "research_input_invalid")
		}
	}
	texts := make([]string, 0, len(parts))
	var media []webMedia
	for _, part := range parts {
		switch part.Type {
		case "text":
			texts = append(texts, part.Text)
		case "image", "document":
			if part.Data == "" || part.MIMEType == "" {
				return "", nil, failure(400, "research_input_invalid")
			}
			media = append(media, webMedia{MIMEType: part.MIMEType, Data: part.Data})
		default:
			return "", nil, failure(400, "research_input_invalid")
		}
	}
	return strings.Join(texts, "\n"), media, nil
}

// webResearchFields marks a turn as Deep Research, as the web app does when the
// tool is picked: a client token in slot 3, a request id in slot 4, and the
// research tool in slots 49, 54 and 55, where a video turn names its own tool.
func webResearchFields(fields []any) error {
	token := make([]byte, 2600)
	id := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return failure(500, "web_request_invalid")
	}
	if _, err := rand.Read(id); err != nil {
		return failure(500, "web_request_invalid")
	}
	fields[3] = "!" + base64.RawURLEncoding.EncodeToString(token)
	fields[4] = hex.EncodeToString(id)
	fields[49] = 1
	fields[54] = []any{[]any{[]any{[]any{[]any{1}}}}}
	fields[55] = []any{[]any{1}}
	return nil
}

// sendResearchTurn submits one turn of a Deep Research conversation. metadata
// continues the conversation an earlier turn left and nil opens one; research
// sends the turn to the agent rather than for an ordinary answer.
func (session *webSession) sendResearchTurn(ctx context.Context, prompt string, account webAccount, model capability, attachments []webAttachment, metadata []any, research bool) ([]byte, error) {
	if session.xsrf == "" {
		if err := session.bootstrap(ctx); err != nil {
			return nil, err
		}
	}
	turn, err := webConversationID()
	if err != nil {
		return nil, err
	}
	fields := webGenerationFields(prompt, model.Mode, webThinkingDefault, turn, attachments)
	if metadata != nil {
		fields[2] = metadata
	}
	if research {
		if err := webResearchFields(fields); err != nil {
			return nil, err
		}
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil, failure(400, "web_request_invalid")
	}
	return session.postGeneration(ctx, string(encoded), account, model, webLanguageDefault, turn, 0)
}

type webResearchPlan struct {
	Title string
	Steps []string
	ETA   string
	Start string
}

// webParseResearchReply reads a research turn's answer: its text, and the plan
// it proposes in field 55 of the rich content (56 in some replies).
func webParseResearchReply(raw []byte) (string, *webResearchPlan, error) {
	bodies, err := webResponseFrames(raw)
	if err != nil {
		return "", nil, err
	}
	text := ""
	var plan *webResearchPlan
	for _, body := range bodies {
		candidate := jsonField(body, 4, 0)
		if candidate == nil {
			continue
		}
		if value, ok := jsonField(candidate, 1, 0).(string); ok && value != "" {
			text = value
		}
		for _, field := range []int{55, 56} {
			if payload, ok := webJSPBField(jsonField(candidate, 12), field).([]any); ok {
				if found := webResearchPlanOf(payload); found != nil {
					plan = found
				}
				break
			}
		}
	}
	return text, plan, nil
}

// webResearchPlanOf reads a plan: its title, numbered steps of a label and a
// description, the time it expects to take, and the action that starts it.
func webResearchPlanOf(payload []any) *webResearchPlan {
	var plan webResearchPlan
	plan.Title, _ = jsonField(payload, 0).(string)
	steps, _ := jsonField(payload, 1).([]any)
	for _, step := range steps {
		label, _ := jsonField(step, 1).(string)
		detail, _ := jsonField(step, 2).(string)
		switch {
		case label != "" && detail != "":
			plan.Steps = append(plan.Steps, label+": "+detail)
		case detail != "":
			plan.Steps = append(plan.Steps, detail)
		case label != "":
			plan.Steps = append(plan.Steps, label)
		}
	}
	plan.ETA, _ = jsonField(payload, 2).(string)
	plan.Start, _ = jsonField(payload, 3, 0).(string)
	if plan.Title == "" && len(plan.Steps) == 0 && plan.ETA == "" && plan.Start == "" {
		return nil
	}
	if plan.Start == "" {
		plan.Start = webResearchStart
	}
	return &plan
}

// researchAnswer is what a planning turn answers with: the reply, then the plan
// the web app draws as a card beside it.
func researchAnswer(text string, plan *webResearchPlan) string {
	var answer strings.Builder
	answer.WriteString(strings.TrimSpace(webImageText(text)))
	if plan != nil {
		answer.WriteString("\n\n")
		if plan.Title != "" {
			answer.WriteString("## " + plan.Title + "\n\n")
		}
		for index, step := range plan.Steps {
			fmt.Fprintf(&answer, "%d. %s\n", index+1, step)
		}
		if plan.ETA != "" {
			answer.WriteString("\n" + plan.ETA + "\n")
		}
	}
	return strings.TrimSpace(answer.String())
}

type webResearchSource struct {
	Number int
	Title  string
	URL    string
}

type webResearchReport struct {
	Title   string
	Body    string
	Sources []webResearchSource
}

// webResearchDocument reads the report a research turn attaches at [30][0]. A
// task id at [3] tells it from other attachments, and the body at [4] (mirrored
// at [17][0]) stays empty until the research ends. The sources behind its
// [cite: N] markers are field 43 of [17][1], or of [5].
func webResearchDocument(candidate any) (webResearchReport, bool) {
	document := jsonField(candidate, 30, 0)
	if task := jsonField(document, 3); task == nil || task == "" {
		return webResearchReport{}, false
	}
	body, _ := jsonField(document, 4).(string)
	if body == "" {
		body, _ = jsonField(document, 17, 0).(string)
	}
	if strings.TrimSpace(body) == "" {
		return webResearchReport{}, false
	}
	report := webResearchReport{Body: body}
	report.Title, _ = jsonField(document, 2).(string)
	groups := webJSPBField(jsonField(document, 17, 1), 43)
	if groups == nil {
		groups = webJSPBField(jsonField(document, 5), 43)
	}
	report.Sources = webCitations(groups)
	return report, true
}

var (
	webCiteMarker = regexp.MustCompile(`\[cite:\s*([\d,\s]+)\]`)
	webCiteNumber = regexp.MustCompile(`\d+`)
)

// webCitations pairs each citation marker's numbers with the sources listed
// beside it, in order; a number keeps the first source it was given.
func webCitations(groups any) []webResearchSource {
	list, _ := groups.([]any)
	resolved := map[int]webResearchSource{}
	for _, group := range list {
		marker, _ := jsonField(group, 0, 0).(string)
		entries, _ := jsonField(group, 1).([]any)
		numbers := webCiteNumber.FindAllString(marker, -1)
		if bracket := webCiteMarker.FindStringSubmatch(marker); bracket != nil {
			numbers = webCiteNumber.FindAllString(bracket[1], -1)
		}
		for index, number := range numbers {
			if index >= len(entries) {
				break
			}
			id, err := strconv.Atoi(number)
			if err != nil {
				continue
			}
			if _, seen := resolved[id]; seen {
				continue
			}
			url, _ := jsonField(entries[index], 3, 0, 1).(string)
			if url == "" {
				continue
			}
			title, _ := jsonField(entries[index], 3, 0, 2).(string)
			resolved[id] = webResearchSource{Number: id, Title: title, URL: url}
		}
	}
	sources := make([]webResearchSource, 0, len(resolved))
	for _, source := range resolved {
		sources = append(sources, source)
	}
	slices.SortFunc(sources, func(a, b webResearchSource) int { return a.Number - b.Number })
	return sources
}

// markdown is the report as the web app shows it, with the sources its
// citation markers point to listed under it.
func (report webResearchReport) markdown() string {
	text := strings.TrimSpace(report.Body)
	if report.Title != "" && !strings.HasPrefix(text, "#") {
		text = "# " + report.Title + "\n\n" + text
	}
	if len(report.Sources) > 0 {
		text += "\n\n## Sources\n"
		for _, source := range report.Sources {
			title := source.Title
			if title == "" {
				title = source.URL
			}
			text += fmt.Sprintf("\n%d. [%s](%s)", source.Number, title, source.URL)
		}
	}
	return text
}

// researchReport looks for the finished report from the conversation's newest
// turn back to the turn that started the research, since the report can land
// on a later turn than that one.
func (session *webSession) researchReport(ctx context.Context, turn continuationTurn) (webResearchReport, bool, error) {
	body, err := session.rpc(ctx, webVideoTurnsRPC, []any{turn.Conversation, 10, nil, 1, []any{1}, []any{4}, nil, 1})
	if err != nil {
		return webResearchReport{}, false, err
	}
	entries, _ := jsonField(body, 0).([]any)
	for _, entry := range entries {
		candidates, _ := jsonField(entry, 3, 0).([]any)
		for _, candidate := range candidates {
			if report, ok := webResearchDocument(candidate); ok {
				return report, true, nil
			}
		}
		if jsonField(entry, 0, 1) == turn.Reply {
			break
		}
	}
	return webResearchReport{}, false, nil
}

// researchHandles names the conversation, reply and candidate a turn's answer
// carries, and the metadata the conversation's next turn sends.
func researchHandles(turn continuationTurn, raw []byte) (continuationTurn, error) {
	for _, line := range bytes.Split(raw, []byte("\n")) {
		next, err := continuationFrame(turn, line)
		if err != nil {
			return turn, err
		}
		turn = next
	}
	if turn.Conversation == "" || turn.Reply == "" || turn.Metadata == "" {
		return turn, failure(502, "research_turn_unnamed")
	}
	return turn, nil
}

func researchMetadata(encoded string) ([]any, error) {
	if encoded == "" {
		return nil, nil
	}
	var metadata []any
	if json.Unmarshal([]byte(encoded), &metadata) != nil {
		return nil, failure(503, "continuation_store_corrupt")
	}
	return metadata, nil
}

// researchResult is what a research interaction answered with, kept beside the
// account like a video, and the action that starts the plan it proposed.
type researchResult struct {
	Text  string `json:"text"`
	Start string `json:"start,omitempty"`
}

type researchRun struct {
	service *service
	session *webSession
	account webAccount
	model   capability
	lease   *credentialLease
	record  storageRecord
	caller  string
}

func (service *service) executeResearch(ctx context.Context, request executorRequest) (interface{}, error) {
	body, prompt, media, err := parseResearch(request.Payload)
	if err != nil {
		return nil, err
	}
	if body.Previous == "" && strings.TrimSpace(prompt) == "" {
		return nil, failure(400, "research_prompt_required")
	}
	if request.AuthProvider != provider || request.Metadata.PinnedAuthID != "" && request.Metadata.PinnedAuthID != request.AuthID {
		return nil, failure(400, "auth_identity_mismatch")
	}
	record, err := service.parseStorage(request.StorageJSON, false)
	if err != nil {
		return nil, err
	}
	if record.ID != request.AuthID {
		return nil, failure(400, "auth_identity_mismatch")
	}
	if record.Disabled {
		return nil, failure(409, "account_disabled")
	}
	if _, err := service.accountLease(record); err != nil {
		return nil, err
	}
	// A shared hold, as a text turn takes: research leaves the account to its
	// videos and Flow, and its record is written under the record lock.
	lease, err := service.acquireCredential(record.TokenRef, false)
	if err != nil {
		return nil, err
	}
	defer lease.guard.RUnlock()
	_, token, err := service.resolve(ctx, request.StorageJSON, request.AuthID)
	if err != nil {
		return nil, err
	}
	credential, err := decodeWebCredential(token)
	if err != nil {
		return nil, err
	}
	session := service.newSession(credential)
	service.trackJar(record.TokenRef, session)
	defer service.persistJar(record.TokenRef, session)
	run := researchRun{service: service, session: session, lease: lease, record: record, caller: request.Metadata.CallerScope}
	var parent continuationTurn
	var answered researchResult
	if body.Previous != "" {
		if parent, answered, err = run.parent(body.Previous); err != nil {
			return nil, err
		}
		if parent.State == "researching" {
			return nil, failure(409, "research_in_progress")
		}
		if parent.State != "planned" && parent.State != "complete" {
			return nil, failure(409, "research_not_continuable")
		}
	}
	if run.account, err = session.webCapabilities(ctx); err != nil {
		return nil, err
	}
	var ok bool
	if run.model, ok = webFamilyCapability(run.account.Capabilities, "Flash"); !ok {
		return nil, failure(404, "account_model_unavailable")
	}
	collaborative := body.AgentConfig.CollaborativePlanning
	// Approving a plan is the web app's Start research button: the plan's own
	// action is sent, not the words the approval came with.
	if parent.State == "planned" && answered.Start != "" && !collaborative {
		return run.start(ctx, parent, answered.Start)
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, failure(400, "research_prompt_required")
	}
	sources, err := service.mediaSources(ctx, media, "")
	if err != nil {
		return nil, err
	}
	attachments, err := session.uploadSources(ctx, sources)
	if err != nil {
		return nil, err
	}
	if parent.State == "complete" {
		return run.followUp(ctx, prompt, attachments, parent)
	}
	return run.ask(ctx, prompt, attachments, parent, collaborative)
}

func (run researchRun) parent(token string) (continuationTurn, researchResult, error) {
	store := run.service.localStore()
	local, err := store.read(run.record.TokenRef)
	if err != nil {
		return continuationTurn{}, researchResult{}, err
	}
	turns, err := continuationTurns(local)
	if err != nil {
		return continuationTurn{}, researchResult{}, err
	}
	key := continuationKey(token)
	turn, found := turns[key]
	if local.Target.ID != run.record.ID || !found || turn.CallerScope != run.caller || turn.Model != webResearchModel {
		return continuationTurn{}, researchResult{}, failure(404, "interaction_not_found")
	}
	var result researchResult
	if turn.ResultStored {
		raw, err := store.readInteractionResult(local, key, turn.CallerScope)
		if err != nil {
			return continuationTurn{}, researchResult{}, err
		}
		if json.Unmarshal(raw, &result) != nil {
			return continuationTurn{}, researchResult{}, failure(503, "continuation_store_corrupt")
		}
	}
	return turn, result, nil
}

// ask sends the agent a question, or a reply to its plan. With collaborative
// planning the plan comes back for review; otherwise it is started at once. An
// answer without a plan, such as a question back, is returned to be replied to.
func (run researchRun) ask(ctx context.Context, prompt string, attachments []webAttachment, parent continuationTurn, collaborative bool) (interface{}, error) {
	metadata, err := researchMetadata(parent.Metadata)
	if err != nil {
		return nil, err
	}
	raw, err := run.session.sendResearchTurn(ctx, prompt, run.account, run.model, attachments, metadata, true)
	if err != nil {
		return nil, err
	}
	turn, err := researchHandles(run.turn(parent), raw)
	if err != nil {
		return nil, err
	}
	text, plan, err := webParseResearchReply(raw)
	if err != nil {
		return nil, err
	}
	if plan != nil && !collaborative {
		return run.start(ctx, turn, plan.Start)
	}
	answer := researchAnswer(text, plan)
	if answer == "" {
		return nil, failure(502, "web_response_invalid")
	}
	result := researchResult{Text: answer}
	if plan != nil {
		result.Start = plan.Start
	}
	turn.State = "planned"
	return run.save(turn, &result, "completed")
}

func (run researchRun) start(ctx context.Context, plan continuationTurn, action string) (interface{}, error) {
	metadata, err := researchMetadata(plan.Metadata)
	if err != nil {
		return nil, err
	}
	raw, err := run.session.sendResearchTurn(ctx, action, run.account, run.model, nil, metadata, true)
	if err != nil {
		return nil, err
	}
	turn, err := researchHandles(run.turn(plan), raw)
	if err != nil {
		return nil, err
	}
	turn.State, turn.StartedAt = "researching", run.service.now().Unix()
	return run.save(turn, nil, "in_progress")
}

func (run researchRun) followUp(ctx context.Context, prompt string, attachments []webAttachment, parent continuationTurn) (interface{}, error) {
	metadata, err := researchMetadata(parent.Metadata)
	if err != nil {
		return nil, err
	}
	raw, err := run.session.sendResearchTurn(ctx, prompt, run.account, run.model, attachments, metadata, false)
	if err != nil {
		return nil, err
	}
	turn, err := researchHandles(run.turn(parent), raw)
	if err != nil {
		return nil, err
	}
	text, err := webReplyText(raw)
	if err != nil {
		return nil, err
	}
	turn.State = "complete"
	return run.save(turn, &researchResult{Text: text}, "completed")
}

func (run researchRun) turn(parent continuationTurn) continuationTurn {
	return continuationTurn{CallerScope: run.caller, Model: webResearchModel, Parent: parent.Metadata, Background: true}
}

func (run researchRun) save(turn continuationTurn, result *researchResult, status string) (interface{}, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, failure(500, "continuation_token_failed")
	}
	token := hex.EncodeToString(raw)
	if err := run.service.saveResearch(run.lease, run.record.TokenRef, continuationKey(token), turn, result, true); err != nil {
		return nil, err
	}
	text := ""
	if result != nil {
		text = result.Text
	}
	return run.service.renderResearch(run.record.ID, token, status, text, "", "")
}

// saveResearch records a research turn, and its answer when it has one, on the
// account. Text turns and Flow share the account meanwhile, so the record is
// read fresh and written under the record lock, as a cookie rotation is. A new
// turn joins the bounded receipt history the way a video turn does.
func (service *service) saveResearch(lease *credentialLease, reference, key string, turn continuationTurn, result *researchResult, fresh bool) error {
	lease.jar.Lock()
	defer lease.jar.Unlock()
	store := service.localStore()
	local, err := store.read(reference)
	if err != nil {
		return err
	}
	turns, err := continuationTurns(local)
	if err != nil {
		return err
	}
	if fresh {
		turn.Sequence = 1
		oldestKey, oldest := "", ^uint64(0)
		for other, previous := range turns {
			if previous.Sequence >= turn.Sequence {
				turn.Sequence = previous.Sequence + 1
			}
			if previous.State != "researching" && previous.Sequence < oldest {
				oldestKey, oldest = other, previous.Sequence
			}
		}
		if len(turns) >= continuationRetention && oldestKey != "" {
			delete(turns, oldestKey)
		}
	}
	if result != nil {
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		if err := store.writeInteractionResult(local, key, turn.CallerScope, encoded); err != nil {
			return err
		}
		turn.ResultStored = true
	}
	turns[key] = turn
	return service.saveContinuations(local, turns)
}

func (service *service) renderResearch(account, token, status, text, code, message string) (interface{}, error) {
	steps := []any{}
	if text != "" {
		steps = append(steps, map[string]any{"type": "model_output", "content": []any{map[string]string{"type": "text", "text": text}}})
	}
	body := map[string]any{"id": token, "object": "interaction", "model": webResearchModel, "status": status, "steps": steps}
	service.accountDisplay(account).put(body)
	if code != "" {
		if message == "" {
			message = code
		}
		body["error"] = map[string]string{"code": code, "message": message}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return continuationResult{Payload: payload, Headers: http.Header{"Content-Type": {"application/json"}}}, nil
}

// retrieveResearch answers a poll. A running research is looked at once per
// poll, as the web app re-reads the chat; an account busy with a video only
// delays the look.
func (service *service) retrieveResearch(ctx context.Context, request executorRequest, record storageRecord, local localSession, token string, turn continuationTurn) (interface{}, error) {
	if request.Stream {
		return nil, failure(400, "research_stream_unsupported")
	}
	key := continuationKey(token)
	switch turn.State {
	case "planned", "complete":
		raw, err := service.localStore().readInteractionResult(local, key, turn.CallerScope)
		if err != nil {
			return nil, err
		}
		var result researchResult
		if json.Unmarshal(raw, &result) != nil {
			return nil, failure(503, "continuation_store_corrupt")
		}
		return service.renderResearch(record.ID, token, "completed", result.Text, "", "")
	case "failed":
		return service.renderResearch(record.ID, token, "failed", "", turn.Error, turn.ErrorMessage)
	}
	lease, err := service.acquireCredential(record.TokenRef, false)
	if err != nil {
		return service.renderResearch(record.ID, token, "in_progress", "", "", "")
	}
	defer lease.guard.RUnlock()
	credential, err := decodeWebCredential(sessionToken{local.Token})
	if err != nil {
		return nil, err
	}
	session := service.newSession(credential)
	service.trackJar(record.TokenRef, session)
	defer service.persistJar(record.TokenRef, session)
	report, ready, err := session.researchReport(ctx, turn)
	if err != nil {
		service.report(map[string]any{"provider": provider, "state": "research_read_failed", "reason": safeCredentialCode(err)}, "gemini-web: a research conversation could not be read")
	}
	if !ready {
		if service.now().Sub(time.Unix(turn.StartedAt, 0)) < webResearchLimit {
			return service.renderResearch(record.ID, token, "in_progress", "", "", "")
		}
		turn.State, turn.Error, turn.ErrorMessage = "failed", "research_timed_out", "the report did not arrive within 90 minutes of the start"
		if err := service.saveResearch(lease, record.TokenRef, key, turn, nil, false); err != nil {
			return nil, err
		}
		return service.renderResearch(record.ID, token, "failed", "", turn.Error, turn.ErrorMessage)
	}
	text := report.markdown()
	turn.State = "complete"
	if err := service.saveResearch(lease, record.TokenRef, key, turn, &researchResult{Text: text}, false); err != nil {
		return nil, err
	}
	return service.renderResearch(record.ID, token, "completed", text, "", "")
}
