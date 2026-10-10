package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

type flowModel struct {
	id, display string
	video       bool
	// imageModel is Flow's name for an image model; family selects the video
	// model keys.
	imageModel string
	family     string
}

var flowCatalog = []flowModel{
	{id: "flow-nano-banana-2", display: "Nano Banana 2.1 (Google Flow)", imageModel: "BELUGA"},
	{id: "flow-nano-banana-pro", display: "Nano Banana Pro (Google Flow)", imageModel: "GEM_PIX_2"},
	{id: "flow-veo-3.1-fast", display: "Veo 3.1 Fast (Google Flow)", video: true, family: "fast"},
	{id: "flow-veo-3.1-quality", display: "Veo 3.1 Quality (Google Flow)", video: true, family: "quality"},
	{id: "flow-veo-3.1-lite", display: "Veo 3.1 Lite (Google Flow)", video: true, family: "lite"},
	{id: "flow-omni-1.1-flash", display: "Omni 1.1 Flash (Google Flow)", video: true, family: "omni"},
}

var (
	flowVideoPoll     = 5 * time.Second
	flowTokenAttempts = 3
)

func flowModelFor(id string) (flowModel, bool) {
	for _, model := range flowCatalog {
		if model.id == id {
			return model, true
		}
	}
	return flowModel{}, false
}

func (service *service) flowAccount(id string) bool {
	return slices.Contains(service.settings().FlowAccounts, id)
}

func flowModelInfos() []modelInfo {
	models := make([]modelInfo, 0, len(flowCatalog))
	for _, model := range flowCatalog {
		output, description := "image", "Google Flow image generation; inline reference images are edited or combined"
		if model.video {
			output, description = "video", "Google Flow video generation, frames, ingredients, editing, extension and upscaling; model-specific options"
		}
		models = append(models, modelInfo{ID: model.id, Object: "model", OwnedBy: provider, Type: provider, Name: model.id, DisplayName: model.display, Description: description, SupportedGenerationMethods: []string{"generateContent"}, SupportedInputModalities: []string{"text", "image"}, SupportedOutputModalities: []string{output}})
	}
	return models
}

// flowContext is the client context every generation RPC carries, with the
// captcha token in its last slot.
func flowContext(project, token string) []any {
	return []any{nil, 22, nil, nil, nil, project, nil, nil, nil, nil, []any{token, 1}}
}

func flowID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "00000000-0000-4000-8000-000000000000"
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	encoded := hex.EncodeToString(raw[:])
	return strings.ToUpper(encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:])
}

func flowSeed() int {
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 1
	}
	return int(binary.BigEndian.Uint32(raw[:])&0x7fffffff) | 1
}

func (service *service) executeFlow(ctx context.Context, method string, request executorRequest, model flowModel) (interface{}, error) {
	if !hasRequestStopRules(request.AuthMetadata.RequestScopedErrors, "flow_") {
		return nil, failure(400, "flow_requires_host_request_stop_policy")
	}
	stream := request.Stream || method == "executor.execute_stream"
	if !slices.Contains([]string{"gemini", "openai", "openai-response", "claude"}, request.Format) ||
		request.Alt != "" && !(request.Alt == "sse" && request.Format == "gemini" && stream) {
		return nil, failure(400, "unsupported_execution_format")
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
	if !service.flowAccount(record.SourceAuthID) {
		return nil, failure(403, "flow_account_not_enabled")
	}
	payload, err := mergeFlowGenerationConfig(request.Payload, request.OriginalRequest)
	if err != nil {
		return nil, err
	}
	input, err := parseFlowRequest(model, payload)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	body, err := service.flowGenerate(ctx, record, model, input)
	outcome := "flow_generation_delivered"
	if err != nil {
		outcome = safeCredentialMessage(err)
	}
	service.report(map[string]any{"provider": provider, "state": outcome, "reason": model.id, "budget": time.Since(started).Round(time.Second).String()}, "flow2api: generation")
	if err != nil {
		return nil, err
	}
	return flowExecutionResult(body, request.Format, stream)
}

// flowProject is the Flow project the account's generations are filed under,
// created once per plugin lifetime.
func (service *service) flowProject(ctx context.Context, record storageRecord) (string, error) {
	service.flowMu.Lock()
	project := service.flowProjects[record.SourceAuthID]
	service.flowMu.Unlock()
	if project != "" {
		return project, nil
	}
	err := service.withFlowSession(ctx, record, func(session *flowSession) error {
		payload, err := session.rpc(ctx, "jHPbke", []any{"projects/*", []any{nil, []any{"CLIProxyAPI"}}, []any{nil, 22}}, flowProjectsPath, "")
		if err != nil {
			return err
		}
		if project = flowFindUUID(payload, ""); project == "" {
			return failure(502, "flow_project_missing")
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	service.flowMu.Lock()
	defer service.flowMu.Unlock()
	if service.flowProjects == nil {
		service.flowProjects = map[string]string{}
	}
	service.flowProjects[record.SourceAuthID] = project
	return project, nil
}

func (service *service) flowUploads(ctx context.Context, record storageRecord, project string, references []flowReference) ([]string, error) {
	ids := make([]string, 0, len(references))
	for index, reference := range references {
		// Generation applies its crop in prepareFlowReferences after uploading.
		reference.CropCoordinates = nil
		extension := "jpg"
		if strings.Contains(reference.mimeType, "png") {
			extension = "png"
		}
		id, _, err := service.uploadFlowImage(ctx, record, project, reference, flowImageUploadOptions{
			Name: fmt.Sprintf("reference-%d.%s", index+1, extension),
		})
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// flowSubmit spends one captcha token on one RPC. A token Flow judged unusual is
// replaced and the call resent, since a refused token never starts the work; a
// call whose answer was lost is never resent, because it may already have.
func (service *service) flowSubmit(ctx context.Context, record storageRecord, project, action string, build func(token string) (string, any), accept func(any) error) error {
	pageURL := flowOrigin + "/project/" + project
	for attempt := 1; ; attempt++ {
		token, err := service.flowToken(ctx, record, action, pageURL)
		if err != nil {
			return err
		}
		rpcID, args := build(token.value)
		err = service.withFlowSession(ctx, record, func(session *flowSession) error {
			payload, err := session.rpc(ctx, rpcID, args, "/project/"+project, token.userAgent)
			if err != nil {
				return err
			}
			return accept(payload)
		})
		if err == nil {
			return nil
		}
		switch safeCredentialCode(err) {
		case "flow_captcha_rejected":
			if attempt < flowTokenAttempts {
				continue
			}
		case "flow_transport_failed", "flow_response_failed", "flow_session_exchange_transport_failed", "flow_session_exchange_response_failed", "flow_session_exchange_response_too_large":
			return failure(502, "flow_submission_outcome_unknown")
		}
		return err
	}
}

func (service *service) flowToken(ctx context.Context, record storageRecord, action, pageURL string) (flowCaptchaToken, error) {
	if service.settings().FlowCaptchaProvider != "native" {
		return service.flowCaptcha(ctx, action, pageURL)
	}
	var token flowCaptchaToken
	err := service.withFlowSession(ctx, record, func(session *flowSession) error {
		var err error
		token, err = session.nativeToken(ctx, service.recaptchaOrigin(), action)
		return err
	})
	return token, err
}

func (service *service) recaptchaOrigin() string {
	if service.recaptchaOriginOverride != "" {
		return service.recaptchaOriginOverride
	}
	return "https://www.google.com"
}

func (service *service) withFlowSession(ctx context.Context, record storageRecord, exchange func(*flowSession) error) error {
	session := service.newFlowSession(record.SourceAuthID)
	err := exchange(session)
	code := safeCredentialCode(err)
	if code == "flow_unauthenticated" || code == "flow_upstream_status" {
		// A refreshed credential can invalidate cached page tokens. Forget the
		// rejected page for the next call, without replaying this operation.
		service.flowMu.Lock()
		delete(service.flowPages, record.SourceAuthID)
		service.flowMu.Unlock()
		return err
	}
	service.rememberFlowPage(record.SourceAuthID, session.page)
	return err
}

type flowOperation struct {
	mediaID, workflowID string
}

func flowUploadedMedia(payload any, project string) string {
	if link := flowFindURL(payload, "image"); link != "" {
		for _, segment := range strings.Split(strings.SplitN(link, "?", 2)[0], "/") {
			if flowUUIDPattern.MatchString(segment) {
				return segment
			}
		}
	}
	return flowFindUUID(payload, project)
}

// flowVideoRecords finds media records in a reply: lists that open with
// the media ID, project ID and workflow ID. Status polling uses the media ID.
func flowVideoRecords(value any, visit func([]any)) {
	list, ok := value.([]any)
	if !ok {
		return
	}
	if len(list) >= 3 {
		mediaID, okMedia := list[0].(string)
		projectID, okProject := list[1].(string)
		workflowID, okWorkflow := list[2].(string)
		baseID, _, _ := strings.Cut(mediaID, "_")
		if okMedia && okProject && okWorkflow && flowIdentifier(mediaID) &&
			flowUUIDPattern.MatchString(baseID) && flowUUIDPattern.MatchString(projectID) && flowUUIDPattern.MatchString(workflowID) {
			visit(list)
			return
		}
	}
	for _, item := range list {
		flowVideoRecords(item, visit)
	}
}

func flowVideoOperation(payload any, project string) flowOperation {
	var found flowOperation
	flowVideoRecords(payload, func(record []any) {
		if found.mediaID == "" && (project == "" || jsonField(record, 1) == project) {
			found = flowOperation{mediaID: record[0].(string), workflowID: record[2].(string)}
		}
	})
	return found
}

// flowVideoState reports the operation's finished video link or the code Flow
// refused it with; both empty means it is still generating.
func flowVideoState(payload any, operation string) (string, string) {
	var link, code string
	seen := false
	flowVideoRecords(payload, func(record []any) {
		if record[0] != operation || seen {
			return
		}
		seen = true
		link, code = flowFindURL(record, "video"), flowErrorCode(record)
	})
	if !seen {
		code = flowErrorCode(payload)
	}
	if link != "" {
		code = ""
	}
	return link, code
}

func (service *service) flowAwaitVideo(ctx context.Context, record storageRecord, project string, operation flowOperation) (string, error) {
	sourcePath := "/project/" + project
	misses := 0
	for {
		if err := service.waitFlow(ctx, flowVideoPoll); err != nil {
			return "", err
		}
		var link, code string
		err := service.withFlowSession(ctx, record, func(session *flowSession) error {
			payload, err := session.rpc(ctx, "jwpduf", []any{nil, nil, []any{[]any{operation.mediaID}}}, sourcePath, "")
			if err != nil {
				return err
			}
			if link, code = flowVideoState(payload, operation.mediaID); link != "" || code != "" {
				return nil
			}
			// The status reply does not always carry the finished link; the
			// media lookup does once the video exists.
			if media, mediaErr := session.rpc(ctx, "as29s", []any{operation.mediaID}, sourcePath+"/edit/"+operation.workflowID, ""); mediaErr == nil {
				link = flowFindURL(media, "video")
			}
			return nil
		})
		if err != nil {
			if safeCredentialCode(err) == "flow_session_exchange_busy" {
				// The generation was already accepted. A local credential
				// lease refusal must not discard its result or replay it.
				misses = 0
				continue
			}
			if misses++; misses >= 5 || safeCredentialCode(err) == "flow_unauthenticated" {
				return "", err
			}
			continue
		}
		misses = 0
		if code != "" {
			return "", flowRejection(code)
		}
		if link != "" {
			return link, nil
		}
	}
}

// flowDownload fetches a signed media link. The link is the credential, so no
// cookie goes with it, and its redirects are followed by hand because the
// shared client never follows one on its own.
func (service *service) flowDownload(ctx context.Context, link string, limit int64) ([]byte, error) {
	for hop := 0; hop < 4; hop++ {
		parsed, err := url.Parse(link)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return nil, failure(502, "flow_media_link_invalid")
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
		if err != nil {
			return nil, failure(502, "flow_media_link_invalid")
		}
		request.Header.Set("Accept", "*/*")
		request.Header.Set("Referer", flowOrigin+"/")
		request.Header.Set("User-Agent", flowUserAgent)
		response, err := service.client.Do(request)
		if err != nil {
			return nil, failure(502, "flow_media_download_failed")
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
		if closeErr := response.Body.Close(); closeErr != nil {
			_ = closeErr
		}
		if response.StatusCode >= 300 && response.StatusCode < 400 {
			location, locationErr := parsed.Parse(response.Header.Get("Location"))
			if locationErr != nil {
				return nil, failure(502, "flow_media_link_invalid")
			}
			link = location.String()
			continue
		}
		if response.StatusCode != http.StatusOK || readErr != nil || len(data) == 0 {
			return nil, failure(502, "flow_media_download_failed")
		}
		if int64(len(data)) > limit {
			return nil, failure(502, "flow_media_too_large")
		}
		return data, nil
	}
	return nil, failure(502, "flow_media_redirect_loop")
}

func flowEncodedMedia(value any) []byte {
	switch typed := value.(type) {
	case string:
		if len(typed) >= 512 && !strings.HasPrefix(typed, "https://") {
			if data, err := base64.StdEncoding.DecodeString(typed); err == nil {
				return data
			}
			if data, err := base64.URLEncoding.DecodeString(typed); err == nil {
				return data
			}
		}
	case []any:
		for _, item := range typed {
			if data := flowEncodedMedia(item); data != nil {
				return data
			}
		}
	}
	return nil
}
