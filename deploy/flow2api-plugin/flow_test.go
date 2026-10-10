package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"
)

const (
	flowTestProject = "11111111-2222-4333-8444-555555555555"
	flowTestMedia   = "66666666-7777-4888-9999-aaaaaaaaaaaa"
	flowTestOp      = "bbbbbbbb-cccc-4ddd-8eee-ffffffffffff"
)

var (
	flowTestPNG = append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), make([]byte, 64)...)
	flowTestMP4 = append([]byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom"), make([]byte, 64)...)
)

// flowFixture stands in for flow.google.com, the captcha solver, the reCAPTCHA
// endpoints and the signed media host, all on one TLS server so the signed
// links it hands out are https like the real ones.
type flowFixture struct {
	t             *testing.T
	server        *httptest.Server
	mu            sync.Mutex
	calls         map[string][]url.Values
	agents        map[string][]string
	replies       map[string][]string
	tasks         []map[string]any
	polls         map[string]int
	reload        url.Values
	cookies       []string
	dropRPC       string
	busyRPC       string
	busyRemaining int
	upload        *flowUploadFixtureState
	streams       map[string]string
	dropStream    bool
}

func newFlowFixture(t *testing.T) *flowFixture {
	t.Helper()
	fixture := &flowFixture{t: t, calls: map[string][]url.Values{}, agents: map[string][]string{}, replies: map[string][]string{}, polls: map[string]int{}}
	fixture.server = httptest.NewTLSServer(http.HandlerFunc(fixture.serve))
	t.Cleanup(fixture.server.Close)
	fixture.reply("nzlxg", rpcEnvelope(t, "nzlxg", []any{142, 2, 3, 3}))
	fixture.reply("HTrJv", rpcEnvelope(t, "HTrJv", flowModelFixture(t)))
	return fixture
}

// reply queues the answers an RPC gives in order; the last one repeats.
func (fixture *flowFixture) reply(rpcID string, bodies ...string) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	fixture.replies[rpcID] = append(fixture.replies[rpcID], bodies...)
}

func (fixture *flowFixture) link(kind string) string {
	return fixture.server.URL + "/" + kind + "/" + flowTestMedia + "?sig=fixture"
}

func (fixture *flowFixture) serve(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/v0/management/plugins/gemini-web/session/exchange" {
		if request.Header.Get("Authorization") != "Bearer fixture-management" {
			fixture.t.Error("broker credential missing")
			writer.WriteHeader(401)
			return
		}
		var exchange struct {
			AuthID    string      `json:"auth_id"`
			Method    string      `json:"method"`
			URL       string      `json:"url"`
			Headers   http.Header `json:"headers"`
			Body      []byte      `json:"body"`
			RequestID string      `json:"request_id"`
		}
		if err := json.NewDecoder(request.Body).Decode(&exchange); err != nil {
			fixture.t.Error(err)
			writer.WriteHeader(400)
			return
		}
		if exchange.AuthID != "gemini-web-f.json" {
			fixture.t.Errorf("wrong source account: %s", exchange.AuthID)
		}
		upstream := httptest.NewRequest(exchange.Method, exchange.URL, strings.NewReader(string(exchange.Body)))
		fixture.mu.Lock()
		if exchange.RequestID != "" {
			fixture.calls["request:"+exchange.RequestID] = append(fixture.calls["request:"+exchange.RequestID], url.Values{})
		}
		if fixture.busyRPC == upstream.URL.Query().Get("rpcids") && fixture.busyRemaining > 0 {
			fixture.busyRemaining--
			fixture.mu.Unlock()
			writer.WriteHeader(http.StatusConflict)
			writeFixture(fixture.t, writer, `{"error":"session_exchange_busy"}`)
			return
		}
		drop := fixture.dropRPC != "" && upstream.URL.Query().Get("rpcids") == fixture.dropRPC
		if fixture.dropStream && (upstream.URL.Path == flowCreationStreamPath || upstream.URL.Path == flowAppletStreamPath) {
			drop = true
			fixture.calls[upstream.URL.Path] = append(fixture.calls[upstream.URL.Path], url.Values{})
		}
		if drop {
			fixture.calls[fixture.dropRPC] = append(fixture.calls[fixture.dropRPC], url.Values{})
		}
		fixture.mu.Unlock()
		if drop {
			connection, _, err := writer.(http.Hijacker).Hijack()
			if err != nil {
				fixture.t.Error(err)
				return
			}
			if err := connection.Close(); err != nil {
				fixture.t.Error(err)
			}
			return
		}
		upstream.Header = exchange.Headers.Clone()
		upstream.Header.Set("Cookie", "SID=flow; SAPISID=secret")
		recorder := httptest.NewRecorder()
		fixture.serve(recorder, upstream)
		writeFixture(fixture.t, writer, string(jsonFixture(fixture.t, exchangeResult{StatusCode: recorder.Code, Headers: recorder.Header(), Body: recorder.Body.Bytes()})))
		return
	}

	t := fixture.t
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	fixture.cookies = append(fixture.cookies, request.Header.Get("Cookie"))
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Error(err)
	}
	path := strings.TrimPrefix(request.URL.Path, "/u/2")
	switch {
	case path == flowCreationStreamPath || path == flowAppletStreamPath:
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Error(err)
		}
		form.Set("deadline", request.Header.Get("X-Client-Deadline-Ms"))
		fixture.calls[path] = append(fixture.calls[path], form)
		reply, found := fixture.streams[path]
		if !found {
			t.Errorf("unexpected stream %s", path)
			writer.WriteHeader(500)
			return
		}
		writeFixture(t, writer, reply)
	case strings.HasPrefix(path, "/upload/v1/flow/upload/video/"):
		fixture.serveUpload(writer, request, body)
	case path == "/createTask":
		var task struct {
			ClientKey string         `json:"clientKey"`
			Task      map[string]any `json:"task"`
		}
		if err := json.Unmarshal(body, &task); err != nil || task.ClientKey != "fixture-key" {
			t.Errorf("createTask = %s", body)
		}
		fixture.tasks = append(fixture.tasks, task.Task)
		writeFixture(t, writer, fmt.Sprintf(`{"errorId":0,"taskId":"task-%d"}`, len(fixture.tasks)))
	case path == "/getTaskResult":
		var poll struct {
			TaskID string `json:"taskId"`
		}
		if err := json.Unmarshal(body, &poll); err != nil {
			t.Error(err)
		}
		fixture.polls[poll.TaskID]++
		if fixture.polls[poll.TaskID] == 1 {
			writeFixture(t, writer, `{"errorId":0,"status":"processing"}`)
			return
		}
		writeFixture(t, writer, `{"errorId":0,"status":"ready","solution":{"gRecaptchaResponse":"token-`+strings.TrimPrefix(poll.TaskID, "task-")+`","userAgent":"Solver Browser/1.0"}}`)
	case path == "/recaptcha/enterprise.js":
		writeFixture(t, writer, `po.src='https://www.gstatic.com/recaptcha/releases/fixture-version/recaptcha__en.js'`)
	case path == "/recaptcha/enterprise/anchor":
		writeFixture(t, writer, `<input type="hidden" id="recaptcha-token" value="anchor-challenge">`)
	case path == "/recaptcha/enterprise/reload":
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Error(err)
		}
		fixture.reload = form
		writeFixture(t, writer, `)]}'`+"\n"+`["rresp","0cAFcWnative",null,120]`)
	case path == flowBatchPath:
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Error(err)
		}
		rpcID := request.URL.Query().Get("rpcids")
		form.Set("bl", request.URL.Query().Get("bl"))
		form.Set("f.sid", request.URL.Query().Get("f.sid"))
		fixture.calls[rpcID] = append(fixture.calls[rpcID], form)
		fixture.agents[rpcID] = append(fixture.agents[rpcID], request.Header.Get("User-Agent"))
		queue := fixture.replies[rpcID]
		if len(queue) == 0 {
			t.Errorf("unexpected rpc %s", rpcID)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		answer := queue[0]
		if len(queue) > 1 {
			fixture.replies[rpcID] = queue[1:]
		}
		if _, err := io.WriteString(writer, answer); err != nil {
			t.Error(err)
		}
	case strings.HasPrefix(path, "/image/"):
		if request.Header.Get("Cookie") != "" {
			t.Error("a signed media link was fetched with the account cookie")
		}
		if _, err := writer.Write(flowTestPNG); err != nil {
			t.Error(err)
		}
	case strings.HasPrefix(path, "/video/"):
		if _, err := writer.Write(flowTestMP4); err != nil {
			t.Error(err)
		}
	default:
		if request.Method != http.MethodGet {
			t.Errorf("unexpected Flow request: %s %s", request.Method, path)
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		writeFixture(t, writer, `<script>window.WIZ_global_data = {"FdrFJe":"-42","SNlM0e":"flow-xsrf"};</script>`+
			`<script src="/_/mss/boq-labs-ai-sandbox/_/js/k=boq_labs-ai-sandbox-frontend_20261008.01_p0/am=AAA"></script>`)
	}
}

// args decodes the arguments the nth call of an RPC carried.
func (fixture *flowFixture) args(rpcID string, call int) []any {
	fixture.t.Helper()
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	calls := fixture.calls[rpcID]
	if call >= len(calls) {
		fixture.t.Fatalf("%s was called %d times, want call %d", rpcID, len(calls), call+1)
	}
	var envelope [][][]any
	if err := json.Unmarshal([]byte(calls[call].Get("f.req")), &envelope); err != nil {
		fixture.t.Fatal(err)
	}
	var args []any
	if err := json.Unmarshal([]byte(envelope[0][0][1].(string)), &args); err != nil {
		fixture.t.Fatal(err)
	}
	return args
}

func (fixture *flowFixture) count(rpcID string) int {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return len(fixture.calls[rpcID])
}

// flowErrorEnvelope renders a refused RPC: the payload slot is empty and the
// error detail names the refusal.
func flowErrorEnvelope(t *testing.T, frame []any) string {
	t.Helper()
	encoded, err := json.Marshal([]any{frame})
	if err != nil {
		t.Fatal(err)
	}
	return ")]}'\n\n" + fmt.Sprintf("%d\n%s\n", len(utf16.Encode([]rune(string(encoded)))), encoded)
}

func flowService(t *testing.T, fixture *flowFixture) (*service, storageRecord) {
	t.Helper()
	t.Setenv("MANAGEMENT_PASSWORD", "fixture-management")
	service := newService(nil)
	record := storageRecord{Type: provider, ID: "flow2api-f.json", Label: "Flow fixture", SourceAuthID: "gemini-web-f.json"}
	service.client = fixture.server.Client()
	service.flowOriginOverride = fixture.server.URL
	service.recaptchaOriginOverride = fixture.server.URL
	service.config.SessionBrokerURL = fixture.server.URL
	service.config.FlowAccounts = []string{record.SourceAuthID}
	service.config.FlowCaptchaKey = "fixture-key"
	service.config.FlowCaptchaProvider = "yescaptcha"
	service.config.FlowCaptchaBaseURL = fixture.server.URL
	service.flowWait = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	return service, record
}

func flowExecute(t *testing.T, service *service, record storageRecord, model, payload string) envelope {
	t.Helper()
	auth, err := authFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	return invoke(t, service, "executor.execute", executorRequest{
		AuthID: record.ID, AuthProvider: provider, Model: model, Format: "gemini", SourceFormat: "gemini",
		StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata, Payload: []byte(payload),
	})
}

func flowInlineMedia(t *testing.T, result envelope) (string, []byte) {
	t.Helper()
	if !result.OK {
		t.Fatalf("flow generation failed: %+v", result.Error)
	}
	var response struct{ Payload []byte }
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					InlineData struct {
						MIMEType string `json:"mimeType"`
						Data     string `json:"data"`
					} `json:"inlineData"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(response.Payload, &body); err != nil || len(body.Candidates) != 1 || len(body.Candidates[0].Content.Parts) != 1 {
		t.Fatalf("response = %s", response.Payload)
	}
	part := body.Candidates[0].Content.Parts[0].InlineData
	data, err := base64.StdEncoding.DecodeString(part.Data)
	if err != nil {
		t.Fatal(err)
	}
	return part.MIMEType, data
}

func TestFlowRequestReadsTheLastUserTurnAndDefaults(t *testing.T) {
	video, _ := flowModelFor("flow-veo-3.1-fast")
	input, err := parseFlowRequest(video, []byte(`{"systemInstruction":{"parts":[{"text":"cinematic"}]},"contents":[`+
		`{"role":"user","parts":[{"text":"old turn"}]},{"role":"model","parts":[{"text":"reply"}]},`+
		`{"role":"user","parts":[{"text":"a fox"},{"inlineData":{"mimeType":"image/png","data":"AAAA"}}]}],`+
		`"generationConfig":{"temperature":1,"candidateCount":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	if input.prompt != "cinematic\na fox" || len(input.references) != 1 || input.aspect != "9:16" || input.seconds != 8 {
		t.Fatalf("input = %+v", input)
	}
	image, _ := flowModelFor("flow-nano-banana-2")
	input, err = parseFlowRequest(image, []byte(`{"contents":[{"parts":[{"text":"an apple"}]}],"generationConfig":{"imageConfig":{"aspectRatio":"16:9","imageSize":"2k"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if input.aspect != "16:9" || input.imageSize != "2K" {
		t.Fatalf("image input = %+v", input)
	}
}

func TestFlowRequestRefusesWhatFlowCannotHonour(t *testing.T) {
	video, _ := flowModelFor("flow-veo-3.1-fast")
	omni, _ := flowModelFor("flow-omni-1.1-flash")
	image, _ := flowModelFor("flow-nano-banana-2")
	reference := `{"inlineData":{"mimeType":"image/png","data":"AAAA"}}`
	for name, test := range map[string]struct {
		model flowModel
		body  string
		code  string
	}{
		"empty prompt":       {video, `{"contents":[{"parts":[{"text":"  "}]}]}`, "flow_prompt_required"},
		"square video":       {video, `{"contents":[{"parts":[{"text":"x"}]}],"generationConfig":{"aspectRatio":"1:1"}}`, "flow_invalid_aspect_ratio"},
		"ten second veo":     {video, `{"contents":[{"parts":[{"text":"x"}]}],"generationConfig":{"durationSeconds":10}}`, "flow_invalid_duration"},
		"unknown option":     {video, `{"contents":[{"parts":[{"text":"x"}]}],"generationConfig":{"negativePrompt":"no"}}`, "flow_unsupported_generation_option"},
		"image duration":     {image, `{"contents":[{"parts":[{"text":"x"}]}],"generationConfig":{"durationSeconds":4}}`, "flow_unsupported_generation_option"},
		"five candidates":    {image, `{"contents":[{"parts":[{"text":"x"}]}],"generationConfig":{"candidateCount":5}}`, "flow_invalid_candidate_count"},
		"file reference":     {video, `{"contents":[{"parts":[{"text":"x"},{"fileData":{"fileUri":"files/a"}}]}]}`, "flow_file_reference_unsupported"},
		"pdf reference":      {video, `{"contents":[{"parts":[{"text":"x"},{"inlineData":{"mimeType":"application/pdf","data":"AAAA"}}]}]}`, "flow_reference_type_unsupported"},
		"eleven references":  {image, `{"contents":[{"parts":[{"text":"x"},` + strings.Repeat(reference+",", 10) + reference + `]}]}`, "flow_too_many_references"},
		"undecodable image":  {omni, `{"contents":[{"parts":[{"text":"x"},{"inlineData":{"mimeType":"image/png","data":"!!"}}]}]}`, "flow_reference_invalid"},
		"unknown image size": {image, `{"contents":[{"parts":[{"text":"x"}]}],"generationConfig":{"imageConfig":{"imageSize":"8K"}}}`, "flow_invalid_image_size"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseFlowRequest(test.model, []byte(test.body))
			if code := safeCredentialCode(err); code != test.code {
				t.Fatalf("code = %s, want %s", code, test.code)
			}
		})
	}
	if _, err := parseFlowRequest(omni, []byte(`{"contents":[{"parts":[{"text":"x"}]}],"generationConfig":{"durationSeconds":10}}`)); err != nil {
		t.Fatalf("Omni refused ten seconds: %v", err)
	}
}

func TestFlowVideoKeysMatchTheWebApp(t *testing.T) {
	usages, err := decodeFlowModels(flowModelFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		family     string
		seconds    int
		portrait   bool
		references bool
		key        string
	}{
		{"fast", 8, false, false, "veo_3_1_t2v_fast_ultra"},
		{"fast", 8, true, false, "veo_3_1_t2v_fast_portrait_ultra"},
		{"fast", 4, true, false, "veo_3_1_t2v_fast_4s"},
		{"fast", 6, false, false, "veo_3_1_t2v_fast_6s"},
		{"quality", 8, true, false, "veo_3_1_t2v_portrait"},
		{"quality", 6, false, false, "veo_3_1_t2v_quality_6s"},
		{"lite", 8, true, false, "veo_3_1_t2v_lite"},
		{"omni", 10, true, false, "abra_t2v_10s"},
		{"omni", 6, false, true, "abra_r2v_6s"},
		{"fast", 8, true, true, "veo_3_1_r2v_fast_portrait_ultra"},
		{"lite", 8, false, true, "veo_3_1_r2v_lite"},
	} {
		family := map[string]string{"fast": "veo_3_1_fast", "quality": "veo_3_1_quality", "lite": "veo_3_1_lite", "omni": "abra"}[test.family]
		selection := flowModelSelection{family: family, video: true, tier: 3, aspect: 2, duration: test.seconds, resolution: 1, inputs: []int{1}}
		if test.portrait {
			selection.aspect = 1
		}
		if test.references {
			selection.inputs = append(selection.inputs, 6)
		}
		usage, err := selectFlowModel(usages, selection)
		if err != nil || usage.key != test.key {
			t.Errorf("%+v = %q, %v", test, usage.key, err)
		}
	}
	if _, err := selectFlowModel(usages, flowModelSelection{family: "veo_3_1_quality", video: true, tier: 3, aspect: 1, duration: 8, resolution: 1, inputs: []int{1, 6}}); safeCredentialCode(err) != "flow_model_options_unsupported" {
		t.Fatalf("quality references = %v", err)
	}
	if _, err := selectFlowModel(usages, flowModelSelection{family: "veo_3_1_fast", video: true, tier: 3, aspect: 1, duration: 4, resolution: 1, inputs: []int{1, 6}}); safeCredentialCode(err) != "flow_model_options_unsupported" {
		t.Fatalf("short fast references = %v", err)
	}
}

// The slots are positional: a shifted index still compiles and only the server
// sees the difference.
func TestFlowArgumentSlots(t *testing.T) {
	context := flowContext(flowTestProject, "tok")
	if context[1] != 22 || context[5] != flowTestProject || !slices.Equal(context[10].([]any), []any{"tok", 1}) || len(context) != 11 {
		t.Fatalf("context = %v", context)
	}
	_, args := flowBatchArgs(flowTestProject, "tok", flowInput{prompt: "an apple", count: 1, references: []flowReference{{MediaID: flowTestMedia}}}, flowSelected{aspect: 3, usage: flowModelUsage{key: "BELUGA"}})
	request := args[1].([]any)[0].([]any)
	if len(request) != 14 || request[4] != 3 || request[5] != "BELUGA" {
		t.Fatalf("image request = %v", request)
	}
	for _, index := range []int{12, 13} {
		id, ok := request[index].(string)
		if !ok || !flowUUIDPattern.MatchString(id) || id == args[4].([]any)[0] {
			t.Fatalf("image request ID slot %d = %v", index, request[index])
		}
	}
	if reference := request[2].([]any)[0].([]any); reference[0] != flowTestMedia || reference[4] != 1 {
		t.Fatalf("image reference = %v", reference)
	}
	rpcID, video := flowBatchArgs(flowTestProject, "tok", flowInput{prompt: "a fox", count: 1}, flowSelected{mode: "text", aspect: 1, resolution: 1, usage: flowModelUsage{key: "veo_3_1_t2v_fast_portrait", video: true}})
	text := video[0].([]any)[0].([]any)
	if rpcID != "YhhmEf" || text[1] != "veo_3_1_t2v_fast_portrait" || text[2] != 1 || len(text) != 8 {
		t.Fatalf("video request = %s %v", rpcID, text)
	}
	rpcID, video = flowBatchArgs(flowTestProject, "tok", flowInput{prompt: "a fox", count: 1, references: []flowReference{{MediaID: flowTestMedia}}}, flowSelected{mode: "references", aspect: 2, resolution: 1, usage: flowModelUsage{key: "abra_r2v_8s", video: true}})
	references := video[0].([]any)[0].([]any)
	if rpcID != "MZZa6b" || references[2] != "abra_r2v_8s" || references[3] != 2 || references[1].([]any)[0].([]any)[1] != flowTestMedia || len(references) != 12 {
		t.Fatalf("reference request = %s %v", rpcID, references)
	}
}

func TestDecodeFlowRPCNamesTheRefusal(t *testing.T) {
	refused := func(detail any) string {
		return flowErrorEnvelope(t, []any{"wrb.fr", "YhhmEf", nil, nil, nil, detail, "generic"})
	}
	for name, test := range map[string]struct {
		body, code string
		status     int
	}{
		"unusual activity": {refused([]any{3, nil, []any{[]any{"type.googleapis.com/google.rpc.ErrorInfo", []any{"PUBLIC_ERROR_UNUSUAL_ACTIVITY"}}}}), "flow_captcha_rejected", 503},
		"plan":             {refused([]any{7, nil, []any{"MODEL_ACCESS_DENIED"}}), "flow_model_access_denied", 403},
		"unsafe prompt":    {refused([]any{3, nil, []any{"PUBLIC_ERROR_UNSAFE_GENERATION"}}), "flow_request_rejected", 400},
		"exhausted":        {refused([]any{8}), "flow_quota_exhausted", 429},
		"signed out":       {flowErrorEnvelope(t, []any{"er", nil, nil, nil, nil, 401, "generic"}), "flow_unauthenticated", 401},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decodeFlowRPC([]byte(test.body), "YhhmEf")
			var public *publicError
			if !errors.As(err, &public) || public.Code != test.code || public.HTTPStatus != test.status {
				t.Fatalf("error = %+v, want %s/%d", err, test.code, test.status)
			}
		})
	}
	payload, err := decodeFlowRPC([]byte(rpcEnvelope(t, "YhhmEf", []any{"ok"})), "YhhmEf")
	if err != nil || jsonField(payload, 0) != "ok" {
		t.Fatalf("payload = %v, %v", payload, err)
	}
}

func TestFlowImageGenerationEndToEnd(t *testing.T) {
	fixture := newFlowFixture(t)
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{[]any{flowTestProject}}))
	fixture.reply("ogiZ0b", rpcEnvelope(t, "ogiZ0b", []any{[]any{[]any{flowTestMedia, nil, fixture.link("image")}}}))
	service, record := flowService(t, fixture)

	for range 2 {
		mimeType, data := flowInlineMedia(t, flowExecute(t, service, record, "flow-nano-banana-2", `{"contents":[{"role":"user","parts":[{"text":"a red apple"}]}]}`))
		if mimeType != "image/png" || string(data) != string(flowTestPNG) {
			t.Fatalf("image = %s %d bytes", mimeType, len(data))
		}
	}
	if count := fixture.count("jHPbke"); count != 1 {
		t.Fatalf("the project was created %d times, want once", count)
	}
	args := fixture.args("ogiZ0b", 0)
	if model := jsonField(args, 1, 0, 5); model != "BELUGA" {
		t.Fatalf("image model = %v, want the current Nano Banana model", model)
	}
	if token := jsonField(args, 3, 10, 0); token != "token-1" {
		t.Fatalf("the generation carried token %v", token)
	}
	if prompt := jsonField(args, 1, 0, 8, 0, 0, 0); prompt != "a red apple" {
		t.Fatalf("prompt slot = %v", prompt)
	}
	fixture.mu.Lock()
	agent, form, task := fixture.agents["ogiZ0b"][0], fixture.calls["ogiZ0b"][0], fixture.tasks[0]
	fixture.mu.Unlock()
	if agent != "Solver Browser/1.0" {
		t.Fatalf("the generation presented %q, not the solver's browser", agent)
	}
	if form.Get("at") != "flow-xsrf" || form.Get("f.sid") != "-42" || form.Get("bl") != "boq_labs-ai-sandbox-frontend_20261008.01_p0" {
		t.Fatalf("page globals = %v", form)
	}
	if task["type"] != "RecaptchaV3TaskProxylessM1S9" || task["websiteKey"] != flowRecaptchaKey || task["pageAction"] != "IMAGE_GENERATION" ||
		task["websiteURL"] != flowOrigin+"/project/"+flowTestProject || task["minScore"] != 0.9 {
		t.Fatalf("captcha task = %v", task)
	}
}

func TestFlowVideoGenerationPollsUntilTheVideoExists(t *testing.T) {
	fixture := newFlowFixture(t)
	record := []any{flowTestOp, flowTestProject, flowTestMedia}
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{flowTestProject}))
	fixture.reply("YhhmEf", rpcEnvelope(t, "YhhmEf", []any{[]any{record}}))
	fixture.reply("jwpduf",
		rpcEnvelope(t, "jwpduf", []any{[]any{append(slices.Clone(record), "MEDIA_GENERATION_STATUS_ACTIVE")}}),
		rpcEnvelope(t, "jwpduf", []any{[]any{append(slices.Clone(record), []any{fixture.link("video")})}}))
	fixture.reply("as29s", rpcEnvelope(t, "as29s", []any{nil}))
	service, account := flowService(t, fixture)

	result := flowExecute(t, service, account, "flow-veo-3.1-fast", `{"contents":[{"parts":[{"text":"a fox"}]}],"generationConfig":{"aspectRatio":"16:9","durationSeconds":4}}`)
	mimeType, data := flowInlineMedia(t, result)
	if mimeType != "video/mp4" || string(data) != string(flowTestMP4) {
		t.Fatalf("video = %s %d bytes", mimeType, len(data))
	}
	var response struct{ Payload []byte }
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	parsed, err := flowOutParse(response.Payload)
	if err != nil || len(parsed.media) != 1 || parsed.media[0].MediaID != flowTestOp {
		t.Fatalf("video media reference must use the polled asset ID, not its workflow ID: %+v, %v", parsed.media, err)
	}
	args := fixture.args("YhhmEf", 0)
	if key, orientation := jsonField(args, 0, 0, 1), jsonField(args, 0, 0, 2); key != "veo_3_1_t2v_fast_4s" || orientation != float64(2) {
		t.Fatalf("video request = %v %v", key, orientation)
	}
	if status := fixture.args("jwpduf", 1); jsonField(status, 2, 0, 0) != flowTestOp {
		t.Fatalf("status poll = %v", status)
	}
	fixture.mu.Lock()
	action := fixture.tasks[0]["pageAction"]
	fixture.mu.Unlock()
	if action != "VIDEO_GENERATION" {
		t.Fatalf("video token action = %v", action)
	}
}

func TestFlowVideoReportsTheRefusalFlowRecorded(t *testing.T) {
	fixture := newFlowFixture(t)
	record := []any{flowTestOp, flowTestProject, flowTestMedia}
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{flowTestProject}))
	fixture.reply("YhhmEf", rpcEnvelope(t, "YhhmEf", []any{[]any{record}}))
	fixture.reply("jwpduf", rpcEnvelope(t, "jwpduf", []any{[]any{append(slices.Clone(record), []any{"PUBLIC_ERROR_PROMINENT_PEOPLE_FILTER_FAILED"})}}))
	service, account := flowService(t, fixture)

	result := flowExecute(t, service, account, "flow-omni-1.1-flash", `{"contents":[{"parts":[{"text":"a celebrity"}]}]}`)
	if result.OK || result.Error.Code != "flow_request_rejected" || result.Error.HTTPStatus != 400 || !strings.Contains(result.Error.Message, "PROMINENT_PEOPLE") {
		t.Fatalf("refusal = %+v", result.Error)
	}
}

func TestFlowExecutionFailureReachesTheHostAsAnErrorObject(t *testing.T) {
	// Given: Flow refusing the generation because the session is signed out.
	fixture := newFlowFixture(t)
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{[]any{flowTestProject}}))
	fixture.reply("ogiZ0b", flowErrorEnvelope(t, []any{"er", nil, nil, nil, nil, 401, "generic"}))
	service, record := flowService(t, fixture)

	// When: the image is requested.
	result := flowExecute(t, service, record, "flow-nano-banana-2", `{"contents":[{"role":"user","parts":[{"text":"a red apple"}]}]}`)

	// Then: the message is the error object the host passes to the caller as it
	// stands, so the 401 does not read as the caller's API key.
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if result.OK || result.Error.Code != "flow_unauthenticated" || result.Error.HTTPStatus != 401 ||
		json.Unmarshal([]byte(result.Error.Message), &body) != nil || body.Error.Code != "flow_unauthenticated" || body.Error.Message != "flow_unauthenticated" {
		t.Fatalf("execution failure = %+v", result.Error)
	}
}

// A refused token never started a generation, so it is replaced; a lost answer
// may have, so it is not.
func TestFlowResendsOnlyARefusedToken(t *testing.T) {
	fixture := newFlowFixture(t)
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{flowTestProject}))
	fixture.reply("ogiZ0b",
		flowErrorEnvelope(t, []any{"wrb.fr", "ogiZ0b", nil, nil, nil, []any{3, nil, []any{"PUBLIC_ERROR_UNUSUAL_ACTIVITY"}}, "generic"}),
		rpcEnvelope(t, "ogiZ0b", []any{[]any{[]any{flowTestMedia, fixture.link("image")}}}))
	service, record := flowService(t, fixture)

	flowInlineMedia(t, flowExecute(t, service, record, "flow-nano-banana-2", `{"contents":[{"parts":[{"text":"an apple"}]}]}`))
	if token := jsonField(fixture.args("ogiZ0b", 1), 3, 10, 0); token != "token-2" {
		t.Fatalf("the resend carried %v, want a fresh token", token)
	}

	lost := newFlowFixture(t)
	lost.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{flowTestProject}))
	service, record = flowService(t, lost)
	lost.mu.Lock()
	lost.dropRPC = "ogiZ0b"
	lost.mu.Unlock()
	result := flowExecute(t, service, record, "flow-nano-banana-2", `{"contents":[{"parts":[{"text":"an apple"}]}]}`)
	if result.OK || result.Error.Code != "flow_submission_outcome_unknown" || lost.count("ogiZ0b") != 1 {
		t.Fatalf("a lost answer was resent or misnamed: %+v after %d calls", result.Error, lost.count("ogiZ0b"))
	}
}

func TestFlowNativeTokenAsksAnchorThenReloadOnTheAccountSession(t *testing.T) {
	fixture := newFlowFixture(t)
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{flowTestProject}))
	fixture.reply("ogiZ0b", rpcEnvelope(t, "ogiZ0b", []any{[]any{[]any{flowTestMedia, fixture.link("image")}}}))
	service, record := flowService(t, fixture)
	service.config.FlowCaptchaProvider, service.config.FlowCaptchaKey = "native", ""

	flowInlineMedia(t, flowExecute(t, service, record, "flow-nano-banana-2", `{"contents":[{"parts":[{"text":"an apple"}]}]}`))
	fixture.mu.Lock()
	reload, tasks, cookies := fixture.reload, len(fixture.tasks), slices.Clone(fixture.cookies)
	fixture.mu.Unlock()
	if tasks != 0 {
		t.Fatalf("the native provider called the solver %d times", tasks)
	}
	if reload.Get("c") != "anchor-challenge" || reload.Get("sa") != "IMAGE_GENERATION" || reload.Get("v") != "fixture-version" || reload.Get("co") != "aHR0cHM6Ly9mbG93Lmdvb2dsZS5jb206NDQz" {
		t.Fatalf("reload form = %v", reload)
	}
	if token := jsonField(fixture.args("ogiZ0b", 0), 3, 10, 0); token != "0cAFcWnative" {
		t.Fatalf("the generation carried %v", token)
	}
	if !slices.ContainsFunc(cookies, func(cookie string) bool { return strings.Contains(cookie, "SID=flow") }) {
		t.Fatal("the account session never reached the reCAPTCHA calls")
	}
}

func TestFlowModelsPublishOnlyForFlowAccounts(t *testing.T) {
	service := newService(nil)
	record := storageRecord{Type: provider, ID: "flow2api-f.json", Label: "Fixture", SourceAuthID: "gemini-web-f.json"}
	for _, enabled := range []bool{false, true} {
		if enabled {
			service.config.FlowAccounts = []string{record.SourceAuthID}
		}
		result := invoke(t, service, "model.for_auth", struct {
			AuthID, AuthProvider string
			StorageJSON          []byte
		}{record.ID, provider, jsonFixture(t, record)})
		if !result.OK {
			t.Fatalf("models failed: %+v", result.Error)
		}
		var response struct {
			Provider string
			Models   []modelInfo
		}
		if err := json.Unmarshal(result.Result, &response); err != nil {
			t.Fatal(err)
		}
		if response.Provider != provider || enabled && len(response.Models) != len(flowCatalog) || !enabled && len(response.Models) != 0 {
			t.Fatalf("models = %+v", response)
		}
	}
}

func TestFlowRegistrationDoesNotClaimGeminiScheduling(t *testing.T) {
	result := invoke(t, newService(nil), "plugin.register", nil)
	var registration struct {
		Capabilities struct {
			Scheduler bool   `json:"scheduler"`
			Scope     string `json:"executor_model_scope"`
		} `json:"capabilities"`
	}
	if !result.OK || json.Unmarshal(result.Result, &registration) != nil || registration.Capabilities.Scheduler || registration.Capabilities.Scope != "oauth" {
		t.Fatal("incorrect independent plugin scope")
	}
}

func TestFlowCaptchaFailureNamesTheSolverCode(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeFixture(t, writer, `{"errorId":1,"errorCode":"ERROR_ZERO_BALANCE","errorDescription":"fixture-key is empty"}`)
	}))
	t.Cleanup(server.Close)
	service := newService(nil)
	service.client = server.Client()
	service.config.FlowCaptchaKey, service.config.FlowCaptchaBaseURL = "fixture-key", server.URL
	_, err := service.flowCaptcha(context.Background(), "IMAGE_GENERATION", flowOrigin+"/project/x")
	if safeCredentialMessage(err) != "flow_captcha_failed: ERROR_ZERO_BALANCE" {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(safeCredentialMessage(err), "fixture-key") {
		t.Fatal("the solver key leaked into the error")
	}
	service.config.FlowCaptchaKey = ""
	if _, err := service.flowCaptcha(context.Background(), "IMAGE_GENERATION", ""); safeCredentialCode(err) != "flow_captcha_unconfigured" {
		t.Fatalf("missing key = %v", err)
	}
}

func TestFlowSettingsApplyWithoutReconfiguringSessions(t *testing.T) {
	service := newService(nil)
	register := func(config string) error {
		_, err := service.register(jsonFixture(t, struct {
			ConfigYAML []byte `json:"config_yaml"`
		}{[]byte(config)}))
		return err
	}
	if err := register("accounts: [gemini-web-a.json]\ncaptcha_provider: native\n"); err != nil {
		t.Fatal(err)
	}
	if settings := service.settings(); !slices.Equal(settings.FlowAccounts, []string{"gemini-web-a.json"}) || settings.FlowCaptchaProvider != "native" {
		t.Fatalf("settings = %+v", settings)
	}
	for _, config := range []string{"captcha_provider: browser\n", "captcha_base_url: http://solver.example\n"} {
		if err := register(config); safeCredentialCode(err) != "invalid_plugin_config" {
			t.Fatalf("%q accepted: %v", config, err)
		}
	}
}

func invoke(t *testing.T, service *service, method string, value any) envelope {
	t.Helper()
	var result envelope
	raw := service.handle(t.Context(), method, jsonFixture(t, value))
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func jsonFixture(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func writeFixture(t *testing.T, writer http.ResponseWriter, body string) {
	t.Helper()
	if _, err := io.WriteString(writer, body); err != nil {
		t.Error(err)
	}
}
func rpcEnvelope(t *testing.T, rpcID string, payload any) string {
	t.Helper()
	encoded := jsonFixture(t, payload)
	frame := jsonFixture(t, []any{[]any{"wrb.fr", rpcID, string(encoded), nil, nil, nil, "generic"}})
	return ")]}'\n\n" + fmt.Sprintf("%d\n%s\n", len(utf16.Encode([]rune(string(frame)))), frame)
}
