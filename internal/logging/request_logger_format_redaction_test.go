package logging

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

func TestResponseSectionMasksUploadBearerHeaders(t *testing.T) {
	headers := http.Header{
		"X-Goog-Upload-URL": {"https://example.test/upload/v1beta/files/resumable?upload_id=opaque-session", "x"},
		"X-Api-Key":         {"private-api-key"},
		"Content-Type":      {"application/json"},
	}
	var output bytes.Buffer
	if err := writeResponseSection(&output, 200, true, headers, strings.NewReader(`{"ok":true}`), nil, true); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if strings.Count(text, "X-Goog-Upload-URL: [REDACTED]\n") != 2 || strings.Contains(text, "opaque-session") || strings.Contains(text, "example.test") || strings.Contains(text, "private-api-key") {
		t.Fatalf("response leaked bearer credentials: %s", text)
	}
	if !strings.Contains(text, "Content-Type: application/json\n") || !strings.Contains(text, `{"ok":true}`) {
		t.Fatalf("non-secret response metadata or body changed: %s", text)
	}
	if headers["X-Goog-Upload-URL"][1] != "x" || headers.Get("X-Api-Key") != "private-api-key" {
		t.Fatal("logging mutated the actual response headers")
	}
}
