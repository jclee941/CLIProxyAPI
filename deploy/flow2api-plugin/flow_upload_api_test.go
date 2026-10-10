package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestFlowGenerationReferenceUploadLeavesCropForTheTransformStep(t *testing.T) {
	// Given an inline generation reference that will be transformed after upload.
	fixture := newFlowFixture(t)
	service, record := flowService(t, fixture)
	fixture.reply("maseQ", rpcEnvelope(t, "maseQ", []any{[]any{flowTestMedia, fixture.link("image")}}))
	reference := flowReference{mimeType: "image/png", data: flowTestPNG, CropCoordinates: &flowCrop{Top: .1, Left: .2, Bottom: .8, Right: .9}}
	// When the existing generation path uses the shared uploader.
	ids, err := service.flowUploads(context.Background(), record, flowTestProject, []flowReference{reference})
	// Then upload does not apply the crop a second time or erase its original input.
	if err != nil || len(ids) != 1 || ids[0] != flowTestMedia ||
		jsonField(fixture.args("maseQ", 0), 6) != nil || reference.CropCoordinates.Top != .1 {
		t.Fatalf("ids=%v err=%v args=%v", ids, err, fixture.args("maseQ", 0))
	}
}

type flowUploadFixtureState struct {
	body      []byte
	size      int64
	final     bool
	cancelled bool
	loseAck   bool
}

// Called by serve with the fixture lock held.
func (fixture *flowFixture) serveUpload(writer http.ResponseWriter, request *http.Request, body []byte) {
	command := request.Header.Get("X-Goog-Upload-Command")
	fixture.calls["upload:"+command] = append(fixture.calls["upload:"+command], url.Values{"offset": {request.Header.Get("X-Goog-Upload-Offset")}})
	writer.Header().Set("Content-Type", "application/json")
	if command == "start" {
		if request.Header.Get("X-Framework-Xsrf-Token") != "flow-xsrf" || request.Header.Get("Slug") != "fixture.mp4" {
			fixture.t.Error("upload start omitted the captured metadata headers")
		}
		size, err := strconv.ParseInt(request.Header.Get("X-Goog-Upload-Header-Content-Length"), 10, 64)
		if err != nil {
			fixture.t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		fixture.upload = &flowUploadFixtureState{size: size}
		writer.Header().Set("X-Goog-Upload-URL", fixture.server.URL+request.URL.Path+"?upload_id=fixture")
		writer.Header().Set("X-Goog-Upload-Chunk-Granularity", "4")
		writer.Header().Set("X-Goog-Upload-Status", "active")
		return
	}
	state := fixture.upload
	if state == nil || state.cancelled {
		writer.WriteHeader(404)
		return
	}
	switch command {
	case "cancel":
		state.cancelled = true
		writer.Header().Set("X-Goog-Upload-Status", "cancelled")
		return
	case "upload", "upload, finalize", "finalize":
		offset, err := strconv.ParseInt(request.Header.Get("X-Goog-Upload-Offset"), 10, 64)
		if err != nil || offset != int64(len(state.body)) || state.final {
			writer.WriteHeader(409)
			return
		}
		state.body = append(state.body, body...)
		if strings.Contains(command, "finalize") {
			if int64(len(state.body)) != state.size {
				writer.WriteHeader(400)
				return
			}
			state.final = true
		}
		if state.loseAck {
			state.loseAck = false
			writer.WriteHeader(503)
			return
		}
	case "query":
	default:
		fixture.t.Errorf("unexpected upload command %q", command)
		writer.WriteHeader(400)
		return
	}
	writer.Header().Set("X-Goog-Upload-Size-Received", strconv.Itoa(len(state.body)))
	writer.Header().Set("X-Goog-Upload-Status", "active")
	if state.final {
		writer.Header().Set("X-Goog-Upload-Status", "final")
		writeFixture(fixture.t, writer, string(jsonFixture(fixture.t, map[string]any{
			"mediaId":  flowTestMedia,
			"media":    map[string]any{"name": flowTestMedia, "projectId": flowTestProject, "workflowId": flowTestOp},
			"workflow": map[string]any{"name": flowTestOp, "metadata": map[string]any{"displayName": "Fixture%20clip"}},
		})))
	}
}

func startFlowUploadTest(t *testing.T, service *service, fixture *flowFixture) flowUploadView {
	t.Helper()
	fixture.reply("ngNC2", rpcEnvelope(t, "ngNC2", []any{flowTestProject, []any{"fixture"}}))
	response := flowHTTPTest(t, service, flowHTTPRequest{
		Method: "POST", Path: "/v1/flow/projects/" + flowTestProject + "/uploads", CallerScope: strings.Repeat("a", 64),
		Body: []byte(`{"name":"fixture.mp4","mimeType":"video/mp4","sizeBytes":8}`),
	})
	if response.StatusCode != 201 {
		t.Fatalf("start status=%d body=%s", response.StatusCode, response.Body)
	}
	var view flowUploadView
	if err := json.Unmarshal(response.Body, &view); err != nil {
		t.Fatal(err)
	}
	return view
}

func TestFlowVideoUploadResumesAfterLostAcknowledgementAndRestart(t *testing.T) {
	// Given an upload whose first chunk is accepted but its acknowledgement fails.
	fixture := newFlowFixture(t)
	service, _ := flowService(t, fixture)
	upload := startFlowUploadTest(t, service, fixture)
	fixture.mu.Lock()
	fixture.upload.loseAck = true
	fixture.mu.Unlock()
	path := "/v1/flow/uploads/" + upload.ID
	first := flowHTTPTest(t, service, flowHTTPRequest{
		Method: "POST", Path: path, CallerScope: strings.Repeat("a", 64),
		Query: url.Values{"offset": {"0"}}, Body: []byte{1, 2, 3, 4},
	})
	if first.StatusCode != 502 || fixture.count("upload:upload") != 1 {
		t.Fatalf("uncertain append was hidden or repeated: status=%d", first.StatusCode)
	}
	service, _ = flowService(t, fixture)

	// When a new plugin instance queries and resumes the same encrypted capability.
	query := flowHTTPTest(t, service, flowHTTPRequest{Method: "GET", Path: path, CallerScope: strings.Repeat("a", 64)})
	var progress flowUploadView
	if err := json.Unmarshal(query.Body, &progress); err != nil {
		t.Fatal(err)
	}
	if query.StatusCode != 200 || progress.Offset != 4 {
		t.Fatalf("query=%s", query.Body)
	}
	final := flowHTTPTest(t, service, flowHTTPRequest{
		Method: "POST", Path: path, CallerScope: strings.Repeat("a", 64),
		Query: url.Values{"offset": {"4"}, "finalize": {"true"}}, Body: []byte{5, 6, 7, 8},
	})
	var complete flowUploadView
	if err := json.Unmarshal(final.Body, &complete); err != nil {
		t.Fatal(err)
	}
	recovered := flowHTTPTest(t, service, flowHTTPRequest{Method: "GET", Path: path, CallerScope: strings.Repeat("a", 64)})

	// Then the result is recoverable with no replay of the accepted bytes.
	if final.StatusCode != 200 || complete.Status != "complete" || complete.Offset != 8 ||
		complete.Media == nil || complete.Media.ID != flowTestMedia || complete.Media.WorkflowID != flowTestOp || complete.Media.Title != "Fixture clip" ||
		recovered.StatusCode != 200 || fixture.count("upload:upload") != 1 || fixture.count("upload:upload, finalize") != 1 {
		t.Fatalf("final=%s recovered=%s", final.Body, recovered.Body)
	}
}

func TestFlowUploadCapabilityCannotBeForgedOrSentToAnotherAccount(t *testing.T) {
	// Given a valid upload capability whose upstream URL must remain private.
	fixture := newFlowFixture(t)
	service, _ := flowService(t, fixture)
	upload := startFlowUploadTest(t, service, fixture)
	raw, err := base64.RawURLEncoding.DecodeString(upload.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), fixture.server.URL) {
		t.Fatal("upstream upload credential was not encrypted")
	}
	raw[len(raw)-1] ^= 1
	// When its ciphertext is modified or it is opened for another account.
	response := flowHTTPTest(t, service, flowHTTPRequest{
		Method: "GET", Path: "/v1/flow/uploads/" + base64.RawURLEncoding.EncodeToString(raw), CallerScope: strings.Repeat("a", 64),
	})
	_, accountErr := service.openFlowUpload(upload.ID, "gemini-web-other.json")
	// Then neither operation reaches the upstream.
	if response.StatusCode != 404 || safeCredentialCode(accountErr) != "flow_upload_not_found" || fixture.count("upload:query") != 0 {
		t.Fatalf("status=%d accountError=%v", response.StatusCode, accountErr)
	}
}

func TestFlowVideoUploadCanBeCancelled(t *testing.T) {
	// Given an active upload.
	fixture := newFlowFixture(t)
	service, _ := flowService(t, fixture)
	upload := startFlowUploadTest(t, service, fixture)
	// When the API caller cancels it.
	response := flowHTTPTest(t, service, flowHTTPRequest{
		Method: "DELETE", Path: "/v1/flow/uploads/" + upload.ID, CallerScope: strings.Repeat("a", 64),
	})
	// Then the upstream session receives exactly one cancel command.
	if response.StatusCode != 200 || fixture.count("upload:cancel") != 1 {
		t.Fatalf("response=%s", response.Body)
	}
}

func TestFlowImageUploadUsesTheSharedGenerationTransport(t *testing.T) {
	// Given the captured image upload response shape.
	fixture := newFlowFixture(t)
	service, _ := flowService(t, fixture)
	fixture.reply("ngNC2", rpcEnvelope(t, "ngNC2", []any{flowTestProject, []any{"fixture"}}))
	media := []any{flowTestMedia, flowTestProject, flowTestOp, nil, nil, nil, []any{fixture.link("image")}}
	fixture.reply("maseQ", rpcEnvelope(t, "maseQ", []any{media}))
	body := jsonFixture(t, map[string]any{"name": "fixture.png", "image": map[string]any{
		"inlineData": map[string]any{"mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(flowTestPNG)},
	}})
	// When the caller uploads an image through the media API.
	response := flowHTTPTest(t, service, flowHTTPRequest{
		Method: "POST", Path: "/v1/flow/projects/" + flowTestProject + "/media", CallerScope: strings.Repeat("a", 64), Body: body,
	})
	// Then one upload carries its filename and original bytes.
	args := fixture.args("maseQ", 0)
	if response.StatusCode != 201 || fixture.count("maseQ") != 1 || jsonField(args, 8) != "fixture.png" ||
		jsonField(args, 1) != base64.StdEncoding.EncodeToString(flowTestPNG) {
		t.Fatalf("status=%d args=%v", response.StatusCode, args)
	}
}
