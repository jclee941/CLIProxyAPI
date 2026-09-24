package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func decodeRouteResponse(t *testing.T, result interface{}) modelRouteResponse {
	t.Helper()
	response, ok := result.(modelRouteResponse)
	if !ok {
		t.Fatalf("expected a route response, got %T", result)
	}
	return response
}

func TestRouteClaimsTheImageModels(t *testing.T) {
	for _, model := range []string{webImageModel, "GPT-Web-Image"} {
		raw, err := json.Marshal(modelRouteRequest{SourceFormat: "openai-image", RequestedModel: model})
		if err != nil {
			t.Fatalf("marshal route request: %v", err)
		}
		result, routeErr := routeImages(raw)
		if routeErr != nil {
			t.Fatalf("route %s: %v", model, routeErr)
		}
		response := decodeRouteResponse(t, result)
		if !response.Handled || response.TargetKind != "self" {
			t.Fatalf("model %s: expected a self route, got handled=%v kind=%q", model, response.Handled, response.TargetKind)
		}
	}
}

func TestRouteClaimsGpt6Pro(t *testing.T) {
	raw, err := json.Marshal(modelRouteRequest{SourceFormat: "openai", RequestedModel: webProModel})
	if err != nil {
		t.Fatalf("marshal route request: %v", err)
	}
	result, routeErr := routeImages(raw)
	if routeErr != nil {
		t.Fatalf("route gpt-6-pro: %v", routeErr)
	}
	response := decodeRouteResponse(t, result)
	if !response.Handled || response.TargetKind != "self" || response.Reason != "chatgpt_web_chat" {
		t.Fatalf("gpt-6-pro: %+v", response)
	}
}

func TestRouteDeclinesOtherModels(t *testing.T) {
	for _, model := range []string{"chatgpt-web-image", "gpt-image-2", "gpt-image-2.5-sunburst", "GPT-Image-1.5", "gemini-web-omni", "grok-imagine-image-2.0", "gpt-5.5", ""} {
		raw, err := json.Marshal(modelRouteRequest{SourceFormat: "openai-image", RequestedModel: model})
		if err != nil {
			t.Fatalf("marshal route request: %v", err)
		}
		result, routeErr := routeImages(raw)
		if routeErr != nil {
			t.Fatalf("route %s: %v", model, routeErr)
		}
		if decodeRouteResponse(t, result).Handled {
			t.Fatalf("model %s must not be claimed", model)
		}
	}
}

func TestImagesPayloadRendersTheHostContract(t *testing.T) {
	raw := imagesPayload("QUJD", imagesRequest{OutputFormat: "webp"})
	var body struct {
		Created int64 `json:"created"`
		Data    []struct {
			B64JSON      string `json:"b64_json"`
			OutputFormat string `json:"output_format"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if body.Created <= 0 || len(body.Data) != 1 {
		t.Fatalf("unexpected payload %s", raw)
	}
	if body.Data[0].B64JSON != "QUJD" || body.Data[0].OutputFormat != "webp" {
		t.Fatalf("unexpected payload %s", raw)
	}
}

func executeImagesWith(t *testing.T, request executorRequest) error {
	t.Helper()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal executor request: %v", err)
	}
	_, executeErr := newService(nil).executeImages(context.Background(), raw)
	return executeErr
}

func TestExecuteRejectsInvalidImageRequests(t *testing.T) {
	cases := map[string]executorRequest{
		"unsupported_model":         {Model: "chatgpt-web-image", Payload: []byte(`{"prompt":"x"}`)},
		"prompt_required":           {Model: webImageModel, Payload: []byte(`{"prompt":"   "}`)},
		"single_image_per_request":  {Model: webImageModel, Payload: []byte(`{"prompt":"x","n":2}`)},
		"authenticated_execution_c": {Model: webImageModel, Payload: []byte(`{"prompt":"x"}`)},
	}
	for expected, request := range cases {
		err := executeImagesWith(t, request)
		if err == nil {
			t.Fatalf("%s: expected a rejection", expected)
		}
		public, ok := err.(*publicError)
		if !ok {
			t.Fatalf("%s: expected a public error, got %T", expected, err)
		}
		if !strings.HasPrefix(public.Code, expected) {
			t.Fatalf("expected code %s, got %s", expected, public.Code)
		}
	}
}

func hostListing(files string) hostCall {
	return func(method string, _ []byte) ([]byte, error) {
		if method != "host.auth.list" {
			return nil, failure(500, "unexpected_host_call")
		}
		return []byte(`{"ok":true,"result":{"files":` + files + `}}`), nil
	}
}

func candidateIDs(entries []hostEntry) []string {
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	return ids
}

func TestImageCandidatesPreferActiveCredentials(t *testing.T) {
	service := newService(hostListing(`[
		{"id":"active","auth_index":"1","provider":"codex","disabled":false},
		{"id":"spent","auth_index":"2","provider":"codex","disabled":true},
		{"id":"other","auth_index":"3","provider":"gemini-web","disabled":false}]`))
	candidates, err := service.imageCandidates("callback")
	if err != nil {
		t.Fatalf("image candidates: %v", err)
	}
	ids := candidateIDs(candidates)
	if len(ids) != 1 || ids[0] != "active" {
		t.Fatalf("expected only the active codex credential, got %v", ids)
	}
}

func TestImageCandidatesFallBackWhenQuotaDisabledEveryCredential(t *testing.T) {
	service := newService(hostListing(`[
		{"id":"spent-a","auth_index":"1","provider":"codex","disabled":true},
		{"id":"spent-b","auth_index":"2","provider":"codex","disabled":true}]`))
	candidates, err := service.imageCandidates("callback")
	if err != nil {
		t.Fatalf("image candidates must survive a fully disabled pool: %v", err)
	}
	if len(candidateIDs(candidates)) != 2 {
		t.Fatalf("expected both reserve credentials, got %v", candidateIDs(candidates))
	}
}

func TestImageCandidatesRejectWhenNoChatGPTCredentialExists(t *testing.T) {
	service := newService(hostListing(`[{"id":"other","auth_index":"1","provider":"gemini-web","disabled":false}]`))
	_, err := service.imageCandidates("callback")
	public, ok := err.(*publicError)
	if !ok || public.Code != "no_chatgpt_credential_available" {
		t.Fatalf("expected no_chatgpt_credential_available, got %v", err)
	}
}

func TestFailureCarriesTheMessageTheHostSurfaces(t *testing.T) {
	err := failure(503, "no_chatgpt_credential_available")
	if err.Message != "no_chatgpt_credential_available" {
		t.Fatalf("expected the code as the host-visible message, got %q", err.Message)
	}
}
