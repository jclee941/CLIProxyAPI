package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCodexSource_whenPhysicalOrProviderMetadataInvalid_neverReadsCredentials(t *testing.T) {
	for _, replacement := range []struct{ old, next string }{
		{`"provider":"codex"`, `"provider":"other"`},
		{`"type":"codex"`, `"type":"other"`},
		{`"source":"file"`, `"source":"memory"`},
		{`"source":"file"`, `"source":"file","runtime_only":true`},
		{`"source":"file"`, `"source":"file","unavailable":true`},
		{`"auth_index":"physical-index"`, `"auth_index":""`},
	} {
		t.Run(replacement.next, func(t *testing.T) {
			plugin := configured(t)
			entry := strings.Replace(codexPhysicalFixture, replacement.old, replacement.next, 1)
			calls := codexBindFixture(t, plugin, entry, codexStorageFixture)

			response := codexManagement(t, plugin, "GET", "codex-sources", "", "fresh-scope")

			if response.StatusCode != 200 || string(response.Body) != `{"sources":[]}` || calls.Load() != 1 {
				t.Fatalf("ineligible source read: %s", response.Body)
			}
		})
	}
}

func TestCodexSource_whenRuntimeChangesOrHostErrors_failClosed(t *testing.T) {
	for _, problem := range []string{"provider", "index", "runtime-only", "unavailable", "malformed", "utf8", "host-error"} {
		t.Run(problem, func(t *testing.T) {
			plugin := configured(t)
			var gets atomic.Int64
			plugin.bindHost(func(ctx context.Context, method string, raw []byte) ([]byte, error) {
				if method == "host.auth.list" {
					return []byte(`{"ok":true,"result":{"files":[` + codexPhysicalFixture + `]}}`), nil
				}
				if method == "host.auth.get" {
					gets.Add(1)
					return nil, fmt.Errorf("forbidden")
				}
				runtime := codexPhysicalFixture
				switch problem {
				case "provider":
					runtime = strings.Replace(runtime, `"provider":"codex"`, `"provider":"other"`, 1)
				case "index":
					runtime = strings.Replace(runtime, "physical-index", "other-index", 1)
				case "runtime-only":
					runtime = strings.Replace(runtime, `"source":"file"`, `"source":"file","runtime_only":true`, 1)
				case "unavailable":
					runtime = strings.Replace(runtime, `"source":"file"`, `"source":"file","unavailable":true`, 1)
				case "malformed":
					return []byte(`{"ok":true,"result":"private-metadata-marker"}`), nil
				case "utf8":
					return []byte{0xff}, nil
				case "host-error":
					return nil, fmt.Errorf("synthetic-access-marker")
				}
				return []byte(`{"ok":true,"result":{"auth":` + runtime + `}}`), nil
			})

			response := codexManagement(t, plugin, "GET", "codex-sources", "", "fresh-scope")

			if response.StatusCode < 400 || gets.Load() != 0 {
				t.Fatal("unsafe runtime metadata accepted")
			}
		})
	}
}

func TestCodexImport_whenReadOnlyPhysicalSource_preservesBytesModeAndModTime(t *testing.T) {
	plugin := codexVendorFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			codexWriteFixture(t, writer, `{"items":[],"capabilities":{"preserve_disabled_accounts":1}}`)
			return
		}
		codexWriteFixture(t, writer, `{"added":1,"items":[{"access_token":"synthetic-access-marker","status":"禁用","source_type":"codex"}]}`)
	})
	path := filepath.Join(t.TempDir(), "physical.json")
	if err := os.WriteFile(path, []byte(codexStorageFixture), 0400); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	codexBindFixture(t, plugin, codexPhysicalFixture, codexStorageFixture)
	plugin.mu.RLock()
	delegate := plugin.host
	plugin.mu.RUnlock()
	plugin.bindHost(func(ctx context.Context, method string, raw []byte) ([]byte, error) {
		if method != "host.auth.get" {
			return delegate(ctx, method, raw)
		}
		storage, errRead := os.ReadFile(path)
		if errRead != nil {
			return nil, errRead
		}
		return []byte(`{"ok":true,"result":{"auth_index":"physical-index","name":"physical.json","json":` + string(storage) + `}}`), nil
	})
	id := codexListedID(t, plugin)

	response := codexManagement(t, plugin, "POST", "import-codex", `{"id":"`+id+`","consent":true,"allow_disabled_source":true}`, "fresh-scope")

	if response.StatusCode != 200 || !strings.Contains(string(response.Body), `"status":"imported_disabled"`) {
		t.Fatal("protected vendor import failed")
	}
	after, errStat := os.Stat(path)
	if errStat != nil {
		t.Fatal(errStat)
	}
	content, errRead := os.ReadFile(path)
	if errRead != nil {
		t.Fatal(errRead)
	}
	if string(content) != codexStorageFixture || before.Mode() != after.Mode() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("source file mutated")
	}
}

func TestWebMetrics_whenUnknownOrInvalid_neverInventsValuesOrLeaksMetadata(t *testing.T) {
	for _, raw := range []string{
		`{"access_token":"synthetic-access-marker","status":"synthetic-access-marker","source_type":"private-metadata-marker","type":"synthetic-id-marker"}`,
		`{"access_token":"synthetic-access-marker","quota":-1,"limits_progress":[{"feature_name":"image_gen","remaining":false,"reset_after":120}]}`,
		`{"access_token":"synthetic-access-marker","quota":1.5,"limits_progress":[{"feature_name":"image_gen","remaining":2},{"feature_name":"image_gen","remaining":3}]}`,
		`{"access_token":"synthetic-access-marker","quota":"0","limits_progress":[{"feature_name":"other","remaining":3}]}`,
	} {
		var account vendorAccount
		if err := json.Unmarshal([]byte(raw), &account); err != nil {
			t.Fatal(err)
		}

		view := account.view(time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC))

		if view.TrackedImageRemaining != nil || view.ObservedImageRemaining != nil || view.ResetAfterSeconds != nil || view.ObservedAt != nil {
			t.Fatal("invented metrics")
		}
		encoded, err := json.Marshal(view)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "marker") {
			t.Fatal("unsafe enum reflected")
		}
	}
}
