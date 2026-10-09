package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
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
	{id: "flow-nano-banana-2", display: "Nano Banana 2 (Google Flow)", imageModel: "NARWHAL"},
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
			output, description = "video", "Google Flow video generation; aspectRatio 16:9 or 9:16, durationSeconds, up to three reference images"
		}
		models = append(models, modelInfo{ID: model.id, Object: "model", OwnedBy: provider, Type: provider, Name: model.id, DisplayName: model.display, Description: description, SupportedGenerationMethods: []string{"generateContent"}, SupportedInputModalities: []string{"text", "image"}, SupportedOutputModalities: []string{output}})
	}
	return models
}

type flowReference struct {
	mimeType string
	data     []byte
}

type flowInput struct {
	prompt     string
	references []flowReference
	aspect     string
	seconds    int
	imageSize  string
}

// flowSamplingOptions are settings chat bridges attach to every request. A
// generation has no use for them, and refusing them would refuse every caller
// that comes through the OpenAI format.
var flowSamplingOptions = map[string]bool{
	"temperature": true, "topP": true, "topK": true, "maxOutputTokens": true, "responseModalities": true,
	"thinkingConfig": true, "stopSequences": true, "presencePenalty": true, "frequencyPenalty": true,
	"seed": true, "responseMimeType": true,
}

// parseFlowRequest reads the prompt from the last user turn, since a Flow
// generation keeps no conversation, together with the options Flow can honour.
func parseFlowRequest(model flowModel, raw []byte) (flowInput, error) {
	var body struct {
		Contents []struct {
			Role  string `json:"role"`
			Parts []struct {
				Text            *string         `json:"text"`
				InlineData      *webInlinePart  `json:"inlineData"`
				InlineDataSnake *webInlinePart  `json:"inline_data"`
				FileData        json.RawMessage `json:"fileData"`
				FileDataSnake   json.RawMessage `json:"file_data"`
			} `json:"parts"`
		} `json:"contents"`
		SystemInstruction *struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"systemInstruction"`
		GenerationConfig map[string]json.RawMessage `json:"generationConfig"`
	}
	if len(raw) > 64*1024*1024 || json.Unmarshal(raw, &body) != nil {
		return flowInput{}, failure(400, "flow_request_invalid")
	}
	turn := -1
	for index := len(body.Contents) - 1; index >= 0; index-- {
		if role := body.Contents[index].Role; role == "" || role == "user" {
			turn = index
			break
		}
	}
	if turn < 0 {
		return flowInput{}, failure(400, "flow_prompt_required")
	}
	input := flowInput{}
	var texts []string
	if body.SystemInstruction != nil {
		for _, part := range body.SystemInstruction.Parts {
			if strings.TrimSpace(part.Text) != "" {
				texts = append(texts, part.Text)
			}
		}
	}
	for _, part := range body.Contents[turn].Parts {
		inline := part.InlineData
		if inline == nil {
			inline = part.InlineDataSnake
		}
		switch {
		case part.Text != nil:
			texts = append(texts, *part.Text)
		case inline != nil:
			if !strings.HasPrefix(inline.mimeType(), "image/") {
				return flowInput{}, failure(400, "flow_reference_type_unsupported")
			}
			data, err := base64.StdEncoding.DecodeString(inline.Data)
			if err != nil || len(data) == 0 {
				return flowInput{}, failure(400, "flow_reference_invalid")
			}
			input.references = append(input.references, flowReference{mimeType: inline.mimeType(), data: data})
		case len(part.FileData) > 0 || len(part.FileDataSnake) > 0:
			return flowInput{}, failure(400, "flow_file_reference_unsupported")
		}
	}
	input.prompt = strings.TrimSpace(strings.Join(texts, "\n"))
	if input.prompt == "" {
		return flowInput{}, failure(400, "flow_prompt_required")
	}
	if len(input.references) > 3 {
		return flowInput{}, failure(400, "flow_too_many_references")
	}
	for key, value := range body.GenerationConfig {
		var err error
		switch key {
		case "aspectRatio":
			err = json.Unmarshal(value, &input.aspect)
		case "durationSeconds":
			if !model.video {
				return flowInput{}, flowOptionRejection(key)
			}
			err = json.Unmarshal(value, &input.seconds)
		case "imageConfig":
			if model.video {
				return flowInput{}, flowOptionRejection(key)
			}
			var config struct {
				AspectRatio string `json:"aspectRatio"`
				ImageSize   string `json:"imageSize"`
			}
			if err = json.Unmarshal(value, &config); err == nil {
				if config.AspectRatio != "" {
					input.aspect = config.AspectRatio
				}
				input.imageSize = strings.ToUpper(config.ImageSize)
			}
		case "candidateCount":
			var count int
			if json.Unmarshal(value, &count) != nil || count != 1 {
				return flowInput{}, failure(400, "flow_single_candidate_only")
			}
		default:
			if !flowSamplingOptions[key] {
				return flowInput{}, flowOptionRejection(key)
			}
		}
		if err != nil {
			return flowInput{}, flowOptionRejection(key)
		}
	}
	return input, flowDefaults(model, &input)
}

func flowOptionRejection(key string) error {
	return &publicError{Code: "flow_unsupported_generation_option", Message: "flow_unsupported_generation_option: " + key, HTTPStatus: 400}
}

func flowDefaults(model flowModel, input *flowInput) error {
	if model.video {
		// Video framing defaults to portrait, matching the Gemini video path.
		if input.aspect == "" {
			input.aspect = "9:16"
		}
		if input.aspect != "9:16" && input.aspect != "16:9" {
			return failure(400, "flow_invalid_aspect_ratio")
		}
		if input.seconds == 0 {
			input.seconds = 8
		}
		allowed := []int{4, 6, 8}
		if model.family == "omni" {
			allowed = append(allowed, 10)
		}
		if !slices.Contains(allowed, input.seconds) {
			return failure(400, "flow_invalid_duration")
		}
		return nil
	}
	if input.aspect == "" {
		input.aspect = "1:1"
	}
	if flowImageAspect(input.aspect) == 0 {
		return failure(400, "flow_invalid_aspect_ratio")
	}
	switch input.imageSize {
	case "", "1K", "2K", "4K":
		return nil
	default:
		return failure(400, "flow_invalid_image_size")
	}
}

func flowImageAspect(aspect string) int {
	return map[string]int{"1:1": 1, "9:16": 2, "16:9": 3, "4:3": 4, "3:4": 5}[aspect]
}

// flowVideoKey is the Flow model key a family, duration, framing and input mode
// run on, as the live Flow web app names them.
func flowVideoKey(family string, seconds int, portrait, references bool) (string, error) {
	if references {
		switch {
		case family == "omni":
			return fmt.Sprintf("abra_r2v_%ds", seconds), nil
		case family == "fast" && seconds == 8 && portrait:
			return "veo_3_1_r2v_fast_portrait", nil
		case family == "fast" && seconds == 8:
			return "veo_3_1_r2v_fast_landscape", nil
		case family == "lite" && seconds == 8:
			return "veo_3_1_r2v_lite", nil
		case family == "quality":
			return "", failure(400, "flow_reference_images_unsupported")
		default:
			return "", failure(400, "flow_reference_duration_unsupported")
		}
	}
	keys := map[string]map[int]string{
		"fast":    {4: "veo_3_1_t2v_fast_4s_relaxed", 6: "veo_3_1_t2v_fast_6s_relaxed", 8: "veo_3_1_t2v_fast"},
		"quality": {4: "veo_3_1_t2v_quality_4s", 6: "veo_3_1_t2v_quality_6s", 8: "veo_3_1_t2v"},
		"lite":    {4: "veo_3_1_t2v_lite_4s", 6: "veo_3_1_t2v_lite_6s", 8: "veo_3_1_t2v_lite"},
	}
	if family == "omni" {
		return fmt.Sprintf("abra_t2v_%ds", seconds), nil
	}
	key := keys[family][seconds]
	if seconds == 8 && portrait && (family == "fast" || family == "quality") {
		key += "_portrait"
	}
	return key, nil
}

// flowContext is the client context every generation RPC carries, with the
// captcha token in its last slot.
func flowContext(project, token string) []any {
	return []any{nil, 22, nil, nil, nil, project, nil, nil, nil, nil, []any{token, 1}}
}

func flowImageArgs(project, token, imageModel, aspect, prompt string, references []string) []any {
	session := flowID()
	context := flowContext(project, token)
	var images any
	if len(references) > 0 {
		list := make([]any, 0, len(references))
		for _, id := range references {
			list = append(list, []any{id, nil, nil, nil, 1})
		}
		images = list
	}
	request := []any{nil, nil, images, flowSeed(), flowImageAspect(aspect), imageModel, nil, context, []any{[]any{[]any{prompt}}}, nil, nil, nil, nil, session, flowID()}
	return []any{nil, []any{request}, 1, context, []any{session}}
}

func flowVideoArgs(project, token, key, aspect, prompt string, references []string) (string, []any) {
	message := []any{nil, nil, []any{[]any{[]any{prompt}}}}
	orientation := 2
	if aspect == "9:16" {
		orientation = 1
	}
	metadata := []any{nil, nil, nil, nil, flowID(), flowID()}
	rpcID, request := "YhhmEf", []any{message, key, orientation, nil, metadata, nil, nil, nil}
	if len(references) > 0 {
		images := make([]any, 0, len(references))
		for _, id := range references {
			images = append(images, []any{nil, id})
		}
		rpcID, request = "MZZa6b", []any{message, images, key, orientation, nil, metadata, nil, nil, nil, nil, nil, nil}
	}
	return rpcID, []any{[]any{request}, flowContext(project, token), []any{flowID(), 2}}
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
	if request.Format != "gemini" || request.Alt != "" {
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
	input, err := parseFlowRequest(model, request.Payload)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	var body []byte
	if model.video {
		body, err = service.flowVideo(ctx, record, model, input)
	} else {
		body, err = service.flowImage(ctx, record, model, input)
	}
	outcome := "flow_generation_delivered"
	if err != nil {
		outcome = safeCredentialMessage(err)
	}
	service.report(map[string]any{"provider": provider, "state": outcome, "reason": model.id, "budget": time.Since(started).Round(time.Second).String()}, "flow2api: generation")
	if err != nil {
		return nil, err
	}
	return webExecutionResult(body, request.Stream || method == "executor.execute_stream"), nil
}

func (service *service) flowImage(ctx context.Context, record storageRecord, model flowModel, input flowInput) ([]byte, error) {
	project, err := service.flowProject(ctx, record)
	if err != nil {
		return nil, err
	}
	references, err := service.flowUploads(ctx, record, project, input.references)
	if err != nil {
		return nil, err
	}
	var mediaID, link string
	var encoded []byte
	err = service.flowSubmit(ctx, record, project, "IMAGE_GENERATION", func(token string) (string, any) {
		return "ogiZ0b", flowImageArgs(project, token, model.imageModel, input.aspect, input.prompt, references)
	}, func(payload any) error {
		mediaID, link = flowGeneratedImage(payload)
		if link == "" {
			return failure(502, "flow_image_missing")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if input.imageSize == "2K" || input.imageSize == "4K" {
		resolution := 1
		if input.imageSize == "4K" {
			resolution = 2
		}
		err = service.flowSubmit(ctx, record, project, "IMAGE_GENERATION", func(token string) (string, any) {
			return "SPrCad", []any{mediaID, resolution, flowContext(project, token)}
		}, func(payload any) error {
			if upscaled := flowFindURL(payload, "image"); upscaled != "" {
				link = upscaled
				return nil
			}
			if data := flowEncodedMedia(payload); data != nil {
				encoded = data
				return nil
			}
			return failure(502, "flow_upscale_missing")
		})
		if err != nil {
			return nil, err
		}
	}
	data := encoded
	if data == nil {
		if data, err = service.flowDownload(ctx, link, 64*1024*1024); err != nil {
			return nil, err
		}
	}
	mimeType := http.DetectContentType(data)
	if !strings.HasPrefix(mimeType, "image/") {
		return nil, failure(502, "flow_image_invalid")
	}
	return flowMediaResponse(model.id, mimeType, data)
}

func (service *service) flowVideo(ctx context.Context, record storageRecord, model flowModel, input flowInput) ([]byte, error) {
	project, err := service.flowProject(ctx, record)
	if err != nil {
		return nil, err
	}
	key, err := flowVideoKey(model.family, input.seconds, input.aspect == "9:16", len(input.references) > 0)
	if err != nil {
		return nil, err
	}
	references, err := service.flowUploads(ctx, record, project, input.references)
	if err != nil {
		return nil, err
	}
	var operation flowOperation
	var link string
	err = service.flowSubmit(ctx, record, project, "VIDEO_GENERATION", func(token string) (string, any) {
		return flowVideoArgs(project, token, key, input.aspect, input.prompt, references)
	}, func(payload any) error {
		if link = flowFindURL(payload, "video"); link != "" {
			return nil
		}
		if operation = flowVideoOperation(payload, project); operation.id == "" {
			return failure(502, "flow_operation_missing")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if link == "" {
		if link, err = service.flowAwaitVideo(ctx, record, project, operation); err != nil {
			return nil, err
		}
	}
	data, err := service.flowDownload(ctx, link, 512*1024*1024)
	if err != nil {
		return nil, err
	}
	if http.DetectContentType(data) != "video/mp4" {
		return nil, failure(502, "flow_video_invalid")
	}
	return flowMediaResponse(model.id, "video/mp4", data)
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
		extension := "jpg"
		if strings.Contains(reference.mimeType, "png") {
			extension = "png"
		}
		var id string
		err := service.flowSubmit(ctx, record, project, "UPLOAD_IMAGE", func(token string) (string, any) {
			return "maseQ", []any{flowContext(project, token), base64.StdEncoding.EncodeToString(reference.data), reference.mimeType, 1, nil, nil, nil, nil, fmt.Sprintf("reference-%d.%s", index+1, extension), nil, flowID(), flowID()}
		}, func(payload any) error {
			if id = flowUploadedMedia(payload, project); id == "" {
				return failure(502, "flow_upload_missing")
			}
			return nil
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
	service.rememberFlowPage(record.SourceAuthID, session.page)
	return err
}

type flowOperation struct {
	id, media string
}

func flowGeneratedImage(payload any) (string, string) {
	items, _ := jsonField(payload, 0).([]any)
	for _, item := range items {
		if link := flowFindURL(item, "image"); link != "" {
			id, _ := jsonField(item, 0).(string)
			return id, link
		}
	}
	return "", ""
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

// flowVideoRecords finds the operation records in a reply: lists that open with
// the operation id, the project and the media id.
func flowVideoRecords(value any, visit func([]any)) {
	list, ok := value.([]any)
	if !ok {
		return
	}
	if len(list) >= 3 {
		operation, okOperation := list[0].(string)
		media, okMedia := list[2].(string)
		if okOperation && okMedia && flowUUIDPattern.MatchString(operation) && flowUUIDPattern.MatchString(media) {
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
		if found.id == "" && (project == "" || jsonField(record, 1) == project) {
			found = flowOperation{id: record[0].(string), media: record[2].(string)}
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
			payload, err := session.rpc(ctx, "jwpduf", []any{nil, nil, []any{[]any{operation.id}}}, sourcePath, "")
			if err != nil {
				return err
			}
			if link, code = flowVideoState(payload, operation.id); link != "" || code != "" {
				return nil
			}
			// The status reply does not always carry the finished link; the
			// media lookup does once the video exists.
			if media, mediaErr := session.rpc(ctx, "as29s", []any{operation.id}, sourcePath+"/edit/"+operation.media, ""); mediaErr == nil {
				link = flowFindURL(media, "video")
			}
			return nil
		})
		if err != nil {
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

func flowMediaResponse(model, mimeType string, data []byte) ([]byte, error) {
	body, err := json.Marshal(map[string]any{
		"modelVersion": model,
		"candidates": []any{map[string]any{
			"index":        0,
			"finishReason": "STOP",
			"content": map[string]any{"role": "model", "parts": []any{
				map[string]any{"inlineData": map[string]any{"mimeType": mimeType, "data": base64.StdEncoding.EncodeToString(data)}},
			}},
		}},
	})
	if err != nil {
		return nil, failure(500, "flow_response_encoding_failed")
	}
	return body, nil
}

func flowCredits(payload any) (int, bool) {
	values, _ := payload.([]any)
	for _, value := range values {
		if credits, ok := jsonInteger(value); ok {
			return credits, true
		}
	}
	return 0, false
}

type flowAccountView struct {
	ID      string `json:"id"`
	Label   string `json:"label,omitempty"`
	Credits *int   `json:"credits,omitempty"`
	Project string `json:"project,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (service *service) flowStatus(ctx context.Context, _ managementRequest) (interface{}, error) {
	accounts := []flowAccountView{}
	for _, id := range service.settings().FlowAccounts {
		record := storageRecord{ID: id, SourceAuthID: id}
		view := flowAccountView{ID: id}
		err := service.withFlowSession(ctx, record, func(session *flowSession) error {
			payload, err := session.rpc(ctx, "nzlxg", []any{}, flowProjectsPath, "")
			if err != nil {
				return err
			}
			credits, ok := flowCredits(payload)
			if !ok {
				return failure(502, "flow_credits_missing")
			}
			view.Credits = &credits
			return nil
		})
		if err != nil {
			view.Error = safeCredentialMessage(err)
		}
		service.flowMu.Lock()
		view.Project = service.flowProjects[id]
		service.flowMu.Unlock()
		accounts = append(accounts, view)
	}
	return map[string]any{"provider": provider, "captcha": service.settings().FlowCaptchaProvider, "models": flowModelInfos(), "accounts": accounts}, nil
}
