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
	for _, model := range []string{imagesModel, "gpt-image-2", "gpt-image-2.5-sunburst", "GPT-Image-1.5"} {
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

func TestRouteDeclinesOtherModels(t *testing.T) {
	for _, model := range []string{"gemini-web-omni", "grok-imagine-image-2.0", "gpt-5.5", ""} {
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

func TestUpstreamBodyMatchesTheImageGenerationContract(t *testing.T) {
	raw, err := imagesUpstreamBody(imagesRequest{Prompt: "a red apple"}, "gpt-image-2")
	if err != nil {
		t.Fatalf("build upstream body: %v", err)
	}
	var body struct {
		Model      string `json:"model"`
		Store      bool   `json:"store"`
		Stream     bool   `json:"stream"`
		ToolChoice struct {
			Type string `json:"type"`
		} `json:"tool_choice"`
		Tools []struct {
			Type         string `json:"type"`
			Model        string `json:"model"`
			Action       string `json:"action"`
			Size         string `json:"size"`
			Quality      string `json:"quality"`
			OutputFormat string `json:"output_format"`
		} `json:"tools"`
		Input []struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"input"`
	}
	if unmarshalErr := json.Unmarshal(raw, &body); unmarshalErr != nil {
		t.Fatalf("decode upstream body: %v", unmarshalErr)
	}
	if body.Model != responsesModel || body.Store || !body.Stream {
		t.Fatalf("unexpected envelope: model=%q store=%v stream=%v", body.Model, body.Store, body.Stream)
	}
	if body.ToolChoice.Type != "image_generation" || len(body.Tools) != 1 {
		t.Fatalf("unexpected tool choice %q with %d tools", body.ToolChoice.Type, len(body.Tools))
	}
	tool := body.Tools[0]
	if tool.Type != "image_generation" || tool.Model != "gpt-image-2" || tool.Action != "generate" {
		t.Fatalf("unexpected tool %+v", tool)
	}
	if tool.Size != imagesDefaultSize || tool.Quality != imagesDefaultQuality || tool.OutputFormat != imagesDefaultFormat {
		t.Fatalf("defaults not applied: %+v", tool)
	}
	if len(body.Input) != 1 || len(body.Input[0].Content) != 1 || body.Input[0].Content[0].Text != "a red apple" {
		t.Fatalf("prompt not carried: %+v", body.Input)
	}
}

func TestUpstreamBodyHonoursExplicitOptions(t *testing.T) {
	raw, err := imagesUpstreamBody(imagesRequest{
		Prompt:       "a blue square",
		Size:         "1792x1024",
		Quality:      "high",
		OutputFormat: "webp",
		Background:   "transparent",
	}, "gpt-image-1.5")
	if err != nil {
		t.Fatalf("build upstream body: %v", err)
	}
	var body struct {
		Tools []struct {
			Model        string `json:"model"`
			Size         string `json:"size"`
			Quality      string `json:"quality"`
			OutputFormat string `json:"output_format"`
			Background   string `json:"background"`
		} `json:"tools"`
	}
	if unmarshalErr := json.Unmarshal(raw, &body); unmarshalErr != nil {
		t.Fatalf("decode upstream body: %v", unmarshalErr)
	}
	tool := body.Tools[0]
	if tool.Model != "gpt-image-1.5" || tool.Size != "1792x1024" || tool.Quality != "high" {
		t.Fatalf("options dropped: %+v", tool)
	}
	if tool.OutputFormat != "webp" || tool.Background != "transparent" {
		t.Fatalf("options dropped: %+v", tool)
	}
}

func TestReadImageResultExtractsTheGeneratedImage(t *testing.T) {
	image := strings.Repeat("A", imagesMinResultBytes+64)
	stream := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1"}}`,
		`data: {"type":"response.output_item.added","item":{"type":"image_generation_call","result":"short"}}`,
		`data: {"type":"response.image_generation_call.completed","item":{"type":"image_generation_call","result":"` + image + `"}}`,
		"data: [DONE]",
		"",
	}, "\n")
	result, err := readImageResult(strings.NewReader(stream))
	if err != nil {
		t.Fatalf("read image result: %v", err)
	}
	if result != image {
		t.Fatalf("expected the long result, got %d bytes", len(result))
	}
}

func TestReadImageResultFailsWithoutAnImage(t *testing.T) {
	stream := "data: {\"type\":\"response.created\"}\ndata: [DONE]\n"
	if _, err := readImageResult(strings.NewReader(stream)); err == nil {
		t.Fatal("expected an error when the stream carries no image")
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
		"unsupported_model":         {Model: "gemini-web-omni", Payload: []byte(`{"prompt":"x"}`)},
		"prompt_required":           {Model: imagesModel, Payload: []byte(`{"prompt":"   "}`)},
		"single_image_per_request":  {Model: imagesModel, Payload: []byte(`{"prompt":"x","n":2}`)},
		"authenticated_execution_c": {Model: imagesModel, Payload: []byte(`{"prompt":"x"}`)},
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
