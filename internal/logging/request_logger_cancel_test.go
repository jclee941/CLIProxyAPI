package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/clienterror"
)

// A client that closed its request cancelled it; nothing failed, so full request
// logging keeps its ordinary name and the error view leaves it out.
func TestClientClosedRequestIsNotAFailure(t *testing.T) {
	dir := t.TempDir()
	logger := NewFileRequestLogger(true, dir, "", 10)
	now := time.Now()

	if err := logger.LogRequestWithOptions("/v1/responses", "POST", nil, []byte(`{"model":"gpt-4"}`), clienterror.StatusClientClosedRequest, nil, nil, nil, nil, nil, nil, nil, false, "req-499", now, now); err != nil {
		t.Fatalf("log request: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read logs dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one request log, got %d", len(entries))
	}
	name := entries[0].Name()
	if strings.HasPrefix(name, "error-") {
		t.Fatalf("a cancelled request was logged as an error: %s", name)
	}
	if IsRequestErrorLogFile(filepath.Join(dir, name)) {
		t.Fatalf("the error view listed a cancelled request: %s", name)
	}
}
