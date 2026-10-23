package main

import (
	"encoding/json"
	"testing"
)

func textTurn(t *testing.T, reply string) envelope {
	t.Helper()
	server := nativeWebServer(t, reply)
	service, record := nativeService(t, server)
	return invoke(t, service, "executor.execute", executorRequest{
		AuthID: record.ID, AuthProvider: provider, Model: "gemini-app-3.8-flash",
		Format: "gemini", SourceFormat: "gemini", StorageJSON: jsonFixture(t, record),
		Payload: []byte(`{"contents":[{"role":"user","parts":[{"text":"x"}]}]}`),
	})
}

// The web app's stock answer to a turn it failed is an upstream failure, not
// the model's reply: the caller gets 502 web_generic_error, which the host
// rotates on, instead of the sentence as text.
func TestTextTurnFailsOnTheWebAppsGenericError(t *testing.T) {
	for _, reply := range []string{
		"Sorry, something went wrong. Please try your request again.",
		"I\u2019m having a hard time fulfilling your request. Can I help you with something else instead?",
		"\n I encountered an error doing what you asked. Could you try again? ",
		"I seem to be encountering an error. Can I try something else for you?",
	} {
		result := textTurn(t, reply)
		if result.OK || result.Error == nil || result.Error.HTTPStatus != 502 || result.Error.Code != "web_generic_error" {
			t.Fatalf("%q: ok=%v error=%+v", reply, result.OK, result.Error)
		}
	}
}

// An answer that only quotes one of those sentences is still the model's answer.
func TestTextTurnKeepsAnAnswerQuotingTheGenericError(t *testing.T) {
	reply := `The app showed "Sorry, something went wrong. Please try your request again." after the upload.`
	result := textTurn(t, reply)
	if !result.OK {
		t.Fatalf("a quoted sentence failed the turn: %+v", result.Error)
	}
	var response struct{ Payload []byte }
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatalf("executor result: %v", err)
	}
	var body struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(response.Payload, &body); err != nil || len(body.Candidates) != 1 || len(body.Candidates[0].Content.Parts) != 1 || body.Candidates[0].Content.Parts[0].Text != reply {
		t.Fatalf("rendered answer = %s (%v)", response.Payload, err)
	}
}
