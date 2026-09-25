package main

import (
	"crypto/sha3"
	"encoding/base64"
	"encoding/json"
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

func TestWebImageGenerationRequiresACallback(t *testing.T) {
	_, err := newService(nil).generateWebImage(t.Context(), "", imagesRequest{Prompt: "x"})
	public, ok := err.(*publicError)
	if !ok || public.Code != "authenticated_execution_callback_required" {
		t.Fatalf("expected the callback requirement, got %v", err)
	}
}
