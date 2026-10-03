package main

import (
	"crypto/sha3"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestPowSolveProducesADigestUnderTheTarget(t *testing.T) {
	seed := "0.6284919484842104"
	difficulty := "0fffff"
	configuration := powConfiguration(webBrowserAgent, []string{powDefaultScript}, "c/abc/_")

	answer, err := powSolve(seed, difficulty, configuration)
	if err != nil {
		t.Fatalf("solve proof of work: %v", err)
	}
	decoded, decodeErr := base64.StdEncoding.DecodeString(answer)
	if decodeErr != nil {
		t.Fatalf("answer is not base64: %v", decodeErr)
	}
	var parsed []interface{}
	if unmarshalErr := json.Unmarshal(decoded, &parsed); unmarshalErr != nil {
		t.Fatalf("answer is not the configuration array: %v", unmarshalErr)
	}
	if len(parsed) != len(configuration) {
		t.Fatalf("expected %d configuration entries, got %d", len(configuration), len(parsed))
	}
	digest := sha3.Sum512([]byte(seed + answer))
	if digest[0] > 0x0f {
		t.Fatalf("digest does not satisfy the difficulty: %x", digest[:3])
	}
}

func TestPowSolveRejectsAnUnusableDifficulty(t *testing.T) {
	configuration := powConfiguration(webBrowserAgent, nil, "")
	if _, err := powSolve("seed", "zz", configuration); err == nil {
		t.Fatal("expected a rejection for a non-hex difficulty")
	}
}

func TestPowTokensCarryTheUpstreamPrefixes(t *testing.T) {
	legacy, err := powLegacyToken(webBrowserAgent, []string{powDefaultScript}, "c/abc/_")
	if err != nil {
		t.Fatalf("build legacy token: %v", err)
	}
	if !strings.HasPrefix(legacy, powLegacyPrefix) {
		t.Fatalf("legacy token prefix missing: %s", legacy[:12])
	}
	proof, err := powProofToken("seed", "ff", webBrowserAgent, nil, "")
	if err != nil {
		t.Fatalf("build proof token: %v", err)
	}
	if !strings.HasPrefix(proof, powProofPrefix) {
		t.Fatalf("proof token prefix missing: %s", proof[:12])
	}
}

func TestRouteClaimsTheWebImageModel(t *testing.T) {
	raw, err := json.Marshal(modelRouteRequest{SourceFormat: "openai-image", RequestedModel: webImageModel})
	if err != nil {
		t.Fatalf("marshal route request: %v", err)
	}
	result, routeErr := routeImages(raw)
	if routeErr != nil {
		t.Fatalf("route %s: %v", webImageModel, routeErr)
	}
	response := decodeRouteResponse(t, result)
	if !response.Handled || response.TargetKind != "self" {
		t.Fatalf("expected a self route, got handled=%v kind=%q", response.Handled, response.TargetKind)
	}
}

func TestWebImageModelIsRegisteredForTheImagesEndpoint(t *testing.T) {
	models := webImageModels()
	if len(models) != 1 {
		t.Fatalf("expected exactly one web image model, got %d", len(models))
	}
	if models[0].ID != webImageModel || models[0].Type != imagesModelType {
		t.Fatalf("unexpected registration %+v", models[0])
	}
}

func TestParseResourcesExtractsScriptsAndBuild(t *testing.T) {
	html := `<html data-build="fallback-build"><script src="https://cdn.oaistatic.com/assets/c/xyz123/_next.js"></script><script src="/other.js"></script></html>`
	sources, build := webParseResources(html)
	if len(sources) != 2 || sources[0] != "https://cdn.oaistatic.com/assets/c/xyz123/_next.js" {
		t.Fatalf("unexpected sources %v", sources)
	}
	if build != "c/xyz123/_" {
		t.Fatalf("expected the script-derived build, got %q", build)
	}
}

func TestParseResourcesFallsBackToTheHTMLBuild(t *testing.T) {
	html := `<html data-build="prod-9f2"><script src="/no-build-here.js"></script></html>`
	sources, build := webParseResources(html)
	if len(sources) != 1 || build != "prod-9f2" {
		t.Fatalf("unexpected sources=%v build=%q", sources, build)
	}
}

func TestParseResourcesDefaultsWhenNoScriptsExist(t *testing.T) {
	sources, build := webParseResources("<html></html>")
	if len(sources) != 1 || sources[0] != powDefaultScript || build != "" {
		t.Fatalf("unexpected sources=%v build=%q", sources, build)
	}
}

func TestScanReferencesCollectsConversationAndImageIDs(t *testing.T) {
	payload := `{"conversation_id":"abc-123","parts":[{"asset_pointer":"file-service://file_00000000111122223333444455556666"},` +
		`{"asset_pointer":"sediment://sed_9988"},{"id":"file_00000000aaaabbbbccccddddeeeeffff"}]}`
	references := webImageReferences{}
	webScanReferences(payload, &references)
	if references.conversationID != "abc-123" {
		t.Fatalf("conversation id not captured: %q", references.conversationID)
	}
	if len(references.fileIDs) != 2 {
		t.Fatalf("expected two file ids, got %v", references.fileIDs)
	}
	if len(references.sedimentIDs) != 1 || references.sedimentIDs[0] != "sed_9988" {
		t.Fatalf("expected one sediment id, got %v", references.sedimentIDs)
	}
}

func TestScanReferencesKeepsIDsUnique(t *testing.T) {
	payload := `file-service://file_00000000111122223333444455556666 file_00000000111122223333444455556666`
	references := webImageReferences{}
	webScanReferences(payload, &references)
	webScanReferences(payload, &references)
	if len(references.fileIDs) != 1 {
		t.Fatalf("expected deduplicated ids, got %v", references.fileIDs)
	}
}

// A live turn for a prompt that hung every caller attempt: the model called the
// image tool with a null prompt, the tool never answered, and the turn closed on
// an empty answer while the conversation stayed unchanged for 200 seconds.
const unansweredImageTurn = `event: delta_encoding
data: "v1"
data: {"type":"resume_conversation_token","kind":"topic","token":"tok","conversation_id":"conv-i"}
event: delta
data: {"p":"","o":"add","v":{"message":{"author":{"role":"system"},"content":{"content_type":"text","parts":[""]},"status":"finished_successfully","end_turn":null,"recipient":"all","channel":null},"conversation_id":"conv-i"},"c":0}
data: {"v":{"message":{"author":{"role":"system"},"content":{"content_type":"text","parts":[""]},"status":"finished_successfully","end_turn":true,"recipient":"all","channel":null},"conversation_id":"conv-i"},"c":1}
data: {"v":{"message":{"author":{"role":"assistant"},"content":{"content_type":"code","language":"json","text":"{\"size\":\"1024x1536\",\"n\":1,\"prompt\":null"},"status":"in_progress","end_turn":null,"recipient":"t2uay3k.sj1i4kz","channel":"commentary"},"conversation_id":"conv-i"},"c":2}
data: {"o":"patch","v":[{"p":"/message/content/text","o":"append","v":"}"},{"p":"/message/status","o":"replace","v":"finished_successfully"},{"p":"/message/end_turn","o":"replace","v":false}]}
data: {"o":"add","v":{"message":{"author":{"role":"assistant"},"content":{"content_type":"reasoning_recap","content":"Worked for a couple of seconds"},"status":"finished_successfully","end_turn":false,"recipient":"all","channel":null},"conversation_id":"conv-i"},"c":3}
data: {"v":{"message":{"author":{"role":"assistant"},"content":{"content_type":"text","parts":[""]},"status":"in_progress","end_turn":null,"recipient":"all","channel":"final"},"conversation_id":"conv-i"},"c":4}
data: {"o":"patch","v":[{"p":"/message/status","o":"replace","v":"finished_successfully"},{"p":"/message/end_turn","o":"replace","v":true}]}
data: {"type":"message_stream_complete","conversation_id":"conv-i"}
data: [DONE]
`

// A live turn whose image tool answered inside the stream.
const answeredImageTurn = `event: delta_encoding
data: "v1"
data: {"type":"resume_conversation_token","kind":"topic","token":"tok","conversation_id":"conv-a"}
event: delta
data: {"p":"","o":"add","v":{"message":{"author":{"role":"assistant"},"content":{"content_type":"code","language":"python3","text":"{\"skipped_mainline\":true}"},"status":"in_progress","end_turn":false,"recipient":"t2uay3k.sj1i4kz","channel":null},"conversation_id":"conv-a"},"c":0}
data: {"p":"/message/status","o":"replace","v":"finished_successfully"}
data: {"p":"","o":"add","v":{"message":{"author":{"role":"tool","name":"t2uay3k.sj1i4kz"},"content":{"content_type":"multimodal_text","parts":[{"content_type":"image_asset_pointer","asset_pointer":"sediment://file_00000000dbdc820994da75af06b30947"}]},"status":"finished_successfully","end_turn":null,"recipient":"all","channel":null},"conversation_id":"conv-a"},"c":1}
data: {"type":"message_stream_complete","conversation_id":"conv-a"}
data: [DONE]
`

func TestImageTurnClosedBeforeItsToolAnsweredIsDeclinedAtOnce(t *testing.T) {
	references, reply := webReadStream(sseResponse(unansweredImageTurn))
	if references.conversationID != "conv-i" || len(references.fileIDs) != 0 || len(references.sedimentIDs) != 0 {
		t.Fatalf("references = %+v", references)
	}
	if !webTurnEndedWithoutImage(reply) {
		t.Fatalf("a closed turn without a tool answer was left to the poll: %+v", reply)
	}
	declined := webImageDeclined()
	if !webRefusedByPolicy(declined) {
		t.Fatalf("the decline would walk the other accounts: %v", declined)
	}
}

func TestImageTurnWithItsToolAnswerIsNotDeclined(t *testing.T) {
	references, reply := webReadStream(sseResponse(answeredImageTurn))
	if len(references.sedimentIDs) != 1 || references.sedimentIDs[0] != "file_00000000dbdc820994da75af06b30947" {
		t.Fatalf("references = %+v", references)
	}
	if !reply.ToolAnswered || webTurnEndedWithoutImage(reply) {
		t.Fatalf("an answered turn was read as declined: %+v", reply)
	}
}

func TestImageTurnStillOpenIsLeftToThePoll(t *testing.T) {
	cut := unansweredImageTurn[:strings.Index(unansweredImageTurn, `data: {"o":"patch","v":[{"p":"/message/status","o":"replace","v":"finished_successfully"},{"p":"/message/end_turn","o":"replace","v":true}]}`)]
	_, reply := webReadStream(sseResponse(cut + "data: [DONE]\n"))
	if reply.TurnEnded || webTurnEndedWithoutImage(reply) {
		t.Fatalf("a system message or an open answer ended the turn: %+v", reply)
	}
	_, unfinished := webReadStream(sseResponse(unansweredImageTurn[:strings.Index(unansweredImageTurn, `data: {"type":"message_stream_complete"`)]))
	if webTurnEndedWithoutImage(unfinished) {
		t.Fatalf("a stream cut before its end marker was taken as conclusive: %+v", unfinished)
	}
}

// The host drops `code` from the envelope it writes, so a caller can only tell a
// refusal from any other failure by the message.
func TestDeclinedImageTurnNamesTheRefusalInItsMessage(t *testing.T) {
	var public *publicError
	if !errors.As(webImageDeclined(), &public) || public.HTTPStatus != 400 {
		t.Fatalf("not a public 400: %v", public)
	}
	if public.Code != webPolicyCode || public.Message != webPolicyCode {
		t.Fatalf("code = %q, message = %q", public.Code, public.Message)
	}
}

func TestWebImageGenerationRequiresACallback(t *testing.T) {
	_, err := newService(nil).generateWebImage(t.Context(), "", imagesRequest{Prompt: "x"})
	public, ok := err.(*publicError)
	if !ok || public.Code != "authenticated_execution_callback_required" {
		t.Fatalf("expected the callback requirement, got %v", err)
	}
}
