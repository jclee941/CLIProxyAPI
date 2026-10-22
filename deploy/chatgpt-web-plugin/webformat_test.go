package main

import (
	"strings"
	"testing"
)

const structuredSchema = `{"type":"object","additionalProperties":false,"required":["score"],"properties":{"score":{"type":"integer"}}}`

func structuredPayload(format string) []byte {
	return []byte(`{"messages":[{"role":"user","content":"grade it"}],"temperature":0.2,"response_format":` + format + `}`)
}

func structuredRelayFor(t *testing.T, recorder *hostRecorder) *webRelay {
	t.Helper()
	plan, err := mediaPlan(t, structuredPayload(`{"type":"json_object"}`))
	if err != nil {
		t.Fatal(err)
	}
	relay := relayFor(recorder, false)
	plan.stream = relay.plan.stream
	relay.plan = plan
	return relay
}

// A caller that asks for a JSON schema has to get that schema in front of the
// model, after the conversation so it is the last thing the model reads; the web
// product has no structured-output mode of its own.
func TestWebChatResponseFormatStatesTheSchemaAfterTheConversation(t *testing.T) {
	prompt, request, err := webChatPrompt(structuredPayload(`{"type":"json_schema","json_schema":{"name":"grade","strict":true,"schema":` + structuredSchema + `}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !request.structured() {
		t.Fatal("json_schema was not treated as structured output")
	}
	if user, schema := strings.Index(prompt, "grade it"), strings.Index(prompt, structuredSchema); user < 0 || schema < user {
		t.Fatalf("schema is not stated after the conversation: user=%d schema=%d in %q", user, schema, prompt)
	}

	objectPrompt, objectRequest, err := webChatPrompt(structuredPayload(`{"type":"json_object"}`))
	if err != nil || !objectRequest.structured() || objectPrompt == "grade it" {
		t.Fatalf("json_object: structured=%v prompt=%q err=%v", objectRequest.structured(), objectPrompt, err)
	}

	for _, format := range []string{`{"type":"text"}`, `null`} {
		plain, plainRequest, err := webChatPrompt(structuredPayload(format))
		if err != nil || plainRequest.structured() || plain != "grade it" {
			t.Fatalf("%s changed the prompt: structured=%v prompt=%q err=%v", format, plainRequest.structured(), plain, err)
		}
	}
}

func TestWebChatRefusesAResponseFormatItCannotHonor(t *testing.T) {
	cases := map[string][]byte{
		"unknown type":     structuredPayload(`{"type":"xml"}`),
		"schema and tools": []byte(`{"messages":[{"role":"user","content":"x"}],"tools":[{"type":"function","function":{"name":"f","parameters":{}}}],"response_format":{"type":"json_object"}}`),
	}
	for name, payload := range cases {
		_, _, err := webChatPrompt(payload)
		if err == nil || publicMessage(err) != "unsupported_response_format" {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}

func TestWebChatPlanMarksStructuredTurns(t *testing.T) {
	plan, err := mediaPlan(t, structuredPayload(`{"type":"json_object"}`))
	if err != nil {
		t.Fatal(err)
	}
	if plan.schema == nil {
		t.Fatal("the plan lost the structured flag")
	}
	plain, err := mediaPlan(t, structuredPayload(`{"type":"text"}`))
	if err != nil || plain.schema != nil {
		t.Fatalf("text format: structured=%v err=%v", plain.schema != nil, err)
	}
}

// The web models wrap a document in a code fence or in prose, and the app adds its
// own markup around it; the document itself is what the caller asked for.
func TestWebStructuredJSONRecoversTheDocument(t *testing.T) {
	cases := []struct {
		name, reply, want string
	}{
		{"bare", `{"score":7}`, `{"score":7}`},
		{"padded", "\n  {\"score\":7}\n", `{"score":7}`},
		{"fenced", "```json\n{\"score\":7}\n```", `{"score":7}`},
		{"prose around", "Here is the grade:\n{\"score\":7}\nHope that helps.", `{"score":7}`},
		{"fenced in prose", "Sure.\n```json\n{\"score\":7,\n\"notes\":\"a\"}\n```\nDone.", "{\"score\":7,\n\"notes\":\"a\"}"},
		{"app markup", "<escape>{\n  \"score\": 7\n}</escape>", "{\n  \"score\": 7\n}"},
		{"app prefix", "<escape>{\"score\":7}", `{"score":7}`},
		{"literal marker", `{"notes":"<escape> belongs to the value"}`, `{"notes":"<escape> belongs to the value"}`},
		{"array", `[{"score":7}]`, `[{"score":7}]`},
	}
	for _, c := range cases {
		got, err := webStructuredJSON(c.reply)
		if err != nil || got != c.want {
			t.Fatalf("%s: got %q, err %v, want %q", c.name, got, err, c.want)
		}
	}
	for _, reply := range []string{"", "no document here", `{"score":`, "```json\n{broken\n```"} {
		if _, err := webStructuredJSON(reply); err == nil || publicMessage(err) != "web_structured_output_invalid" {
			t.Fatalf("%q was accepted: %v", reply, err)
		}
	}
}

// A structured reply is streamed only once it is whole: deltas of a fenced
// document would put the fence in the caller's content.
func TestRelayHoldsAStructuredReplyUntilItIsADocument(t *testing.T) {
	recorder := &hostRecorder{}
	relay := structuredRelayFor(t, recorder)
	pieces := []string{"Here you go:\n```json\n{\"score\":", "7}\n```"}

	for _, piece := range pieces {
		if err := relay.deliver(piece); err != nil {
			t.Fatal(err)
		}
	}
	if sent := recorder.chunks(t); len(sent.contents) != 0 {
		t.Fatalf("content leaked before the document was whole: %q", sent.contents)
	}
	if err := relay.finish(webReply{Text: strings.Join(pieces, "")}); err != nil {
		t.Fatal(err)
	}
	sent := recorder.chunks(t)
	if strings.Join(sent.contents, "") != `{"score":7}` || strings.Join(sent.reasons, "|") != "stop" {
		t.Fatalf("sent = %+v", sent)
	}
}

func TestRelayFailsAStructuredReplyThatIsNotADocument(t *testing.T) {
	recorder := &hostRecorder{}
	relay := structuredRelayFor(t, recorder)

	if err := relay.deliver("I cannot grade this."); err != nil {
		t.Fatal(err)
	}
	err := relay.finish(webReply{Text: "I cannot grade this."})
	if err == nil || publicMessage(err) != "web_structured_output_invalid" {
		t.Fatalf("err = %v", err)
	}
	if sent := recorder.chunks(t); len(sent.contents) != 0 || len(sent.reasons) != 0 {
		t.Fatalf("an invalid document reached the caller: %+v", sent)
	}
}

func TestStructuredRelayRejectsJSONOutsideTheRequestedSchema(t *testing.T) {
	for _, document := range []string{`{}`, `{"score":"seven"}`, `{"score":7,"extra":true}`, `[{"score":7}]`} {
		t.Run(document, func(t *testing.T) {
			plan, err := mediaPlan(t, structuredPayload(`{"type":"json_schema","json_schema":{"name":"grade","strict":true,"schema":`+structuredSchema+`}}`))
			if err != nil {
				t.Fatal(err)
			}
			recorder := &hostRecorder{}
			relay := relayFor(recorder, false)
			plan.stream = relay.plan.stream
			relay.plan = plan

			err = relay.finish(webReply{Text: document})

			if err == nil || publicMessage(err) != "web_structured_output_invalid" {
				t.Fatalf("nonconforming JSON was accepted: %v", err)
			}
			if sent := recorder.chunks(t); len(sent.contents) != 0 || len(sent.reasons) != 0 {
				t.Fatalf("nonconforming JSON escaped: %+v", sent)
			}
		})
	}
}

func TestWebChatRejectsAnInvalidResponseSchemaBeforeExecution(t *testing.T) {
	for _, schema := range []string{`null`, `{"type":"not-a-type"}`, `{"$ref":"file:///unavailable-schema.json"}`} {
		_, err := mediaPlan(t, structuredPayload(`{"type":"json_schema","json_schema":{"name":"grade","strict":true,"schema":`+schema+`}}`))
		if err == nil || publicMessage(err) != "invalid_json_schema" {
			t.Fatalf("invalid schema %s was accepted: %v", schema, err)
		}
	}
}
