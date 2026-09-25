package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestContinuationSDKControlAcceptsEmptySDKContents(t *testing.T) {
	service, local := continuationFixture(t)
	// When google-genai encodes an empty contents list and config for prepare.
	result := continuationCall(t, service, local, `{"contents":[],"generationConfig":{},"geminiWebContinuation":{"action":"prepare"}}`)
	// Then the normal SDK response fields expose the receipt and machine state.
	view := continuationReceipt(t, result)
	var response struct {
		Payload []byte
		Headers http.Header
	}
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	var native struct {
		ResponseID string `json:"responseId"`
	}
	if err := json.Unmarshal(response.Payload, &native); err != nil {
		t.Fatal(err)
	}
	if native.ResponseID != view.Token || response.Headers.Get("X-Gemini-Web-Continuation-State") != "prepared" {
		t.Fatalf("SDK receipt fields missing: %+v", response.Headers)
	}
}
