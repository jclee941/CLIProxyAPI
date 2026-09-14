package smoke

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const codexNativeDriver = `
#include "bridge.h"
#include <assert.h>
#include <dlfcn.h>
#include <stdio.h>
#include <string.h>
static int calls;
static int frees;
static int callback(void *ctx, const char *method, const uint8_t *request, size_t length, cliproxy_buffer *out) {
    (void)ctx;
    char *raw = malloc(length + 1);
    assert(raw);
    memcpy(raw, request, length);
    raw[length] = 0;
    assert(strstr(raw, "\"host_callback_id\":\"native-fresh-scope\""));
    if (strcmp(method, "host.auth.list") != 0) assert(strstr(raw, "\"auth_index\":\"native-physical-index\""));
    free(raw);
    const char *result;
    if (strcmp(method, "host.auth.list") == 0)
        result = "{\"ok\":true,\"result\":{\"files\":[{\"id\":\"native.json\",\"auth_index\":\"native-physical-index\",\"name\":\"native.json\",\"provider\":\"codex\",\"type\":\"codex\",\"source\":\"file\",\"disabled\":true}]}}";
    else if (strcmp(method, "host.auth.get_runtime") == 0)
        result = "{\"ok\":true,\"result\":{\"auth\":{\"id\":\"native.json\",\"auth_index\":\"native-physical-index\",\"name\":\"native.json\",\"provider\":\"codex\",\"type\":\"codex\",\"source\":\"file\",\"disabled\":true}}}";
    else if (strcmp(method, "host.auth.get") == 0)
        result = "{\"ok\":true,\"result\":{\"auth_index\":\"native-physical-index\",\"name\":\"native.json\",\"json\":{\"type\":\"codex\",\"disabled\":true,\"access_token\":\"synthetic-native-access-marker\",\"refresh_token\":\"synthetic-native-refresh-marker\"}}}";
    else return 1;
    out->len = strlen(result);
    out->ptr = malloc(out->len);
    assert(out->ptr);
    memcpy(out->ptr, result, out->len);
    calls++;
    return 0;
}
static void host_free(void *ptr, size_t length) { (void)length; free(ptr); frees++; }
int main(int argc, char **argv) {
    assert(argc == 6);
    void *library = dlopen(argv[1], RTLD_NOW | RTLD_LOCAL);
    assert(library);
    int (*initialize)(cliproxy_host_api *, cliproxy_plugin_api *) = dlsym(library, "cliproxy_plugin_init");
    assert(initialize);
    cliproxy_host_api host = {1, NULL, callback, host_free};
    cliproxy_plugin_api plugin = {0};
    assert(initialize(&host, &plugin) == 0 && plugin.abi_version == 1);
    const char *methods[] = {"plugin.register", "management.handle", "management.handle", "management.handle"};
    for (int index = 0; index < 4; index++) {
        cliproxy_buffer out = {0};
        char *request = argv[index+2];
        assert(plugin.call((char *)methods[index], (uint8_t *)request, strlen(request), &out) == 0);
        assert(fwrite(out.ptr, 1, out.len, stdout) == out.len);
        assert(fputc('\n', stdout) != EOF);
        plugin.free_buffer(out.ptr, out.len);
    }
    plugin.shutdown();
    plugin.shutdown();
    assert(calls == 4 && frees == 4);
    return 0;
}
`

func TestNativeCodexAccountABI_whenFreshProcess_usesOnlyReadCallbacksAndPrivateInventory(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method != "GET" || request.URL.Path != "/api/accounts" || request.Header.Get("Authorization") != "Bearer synthetic-native-admin-marker" {
			t.Error("wrong native private request")
		}
		if _, err := writer.Write([]byte(`{"items":[{"access_token":"synthetic-native-access-marker","refresh_token":"synthetic-native-refresh-marker","status":"禁用","quota":3,"source_type":"web","email":"synthetic-private-marker"}]}`)); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	source := filepath.Join(directory, "native-codex.c")
	driver := filepath.Join(directory, "native-codex")
	if err := os.WriteFile(source, []byte(codexNativeDriver), 0600); err != nil {
		t.Fatal(err)
	}
	compile := exec.CommandContext(t.Context(), "cc", "-Wall", "-Wextra", "-Werror", "-I", root, "-o", driver, source, "-ldl")
	if output, errCompile := compile.CombinedOutput(); errCompile != nil {
		t.Fatalf("native fixture compile: %v: %s", errCompile, output)
	}
	encode := func(value any) string {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	config := encode(struct {
		Config []byte `json:"config_yaml"`
	}{[]byte("model_names: []\nenabled: true\npriority: 1\napi_base_url: " + server.URL)})
	request := func(method, path, body, scope string) string {
		return encode(struct {
			Method, Path string
			Body         []byte
			Scope        string `json:"host_callback_id"`
		}{method, "/v0/management/plugins/chatgpt2api/" + path, []byte(body), scope})
	}
	digest := sha256.Sum256([]byte("codex\x00native.json\x00native-physical-index"))
	id := "codex_" + hex.EncodeToString(digest[:])
	command := exec.CommandContext(t.Context(), driver, filepath.Join(root, "chatgpt2api.so"), config,
		request("POST", "import-codex", `{"id":"`+id+`","consent":true,"allow_disabled_source":true}`, "native-fresh-scope"),
		request("GET", "webaccounts", "", "native-fresh-scope"),
		request("GET", "codex-sources", "", ""))
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, "CHATGPT2API_AUTH_KEY=") {
			command.Env = append(command.Env, variable)
		}
	}
	command.Env = append(command.Env, "CHATGPT2API_AUTH_KEY=synthetic-native-admin-marker")

	output, errRun := command.CombinedOutput()

	if errRun != nil {
		t.Fatalf("native fixture failed: %v: %s", errRun, output)
	}
	lines := bytes.Split(bytes.TrimSpace(output), []byte("\n"))
	if len(lines) != 4 || requests.Load() != 2 {
		t.Fatal("wrong native fixture call count")
	}
	for index, line := range lines {
		var response envelope
		if err := json.Unmarshal(line, &response); err != nil {
			t.Fatal(err)
		}
		if !response.OK {
			t.Fatal("native envelope failed")
		}
		if index == 0 {
			continue
		}
		var result struct {
			StatusCode int
			Body       []byte
		}
		if err := json.Unmarshal(response.Result, &result); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(result.Body), "marker") {
			t.Fatal("native DTO credential leak")
		}
		if index == 1 && (result.StatusCode != 200 || !strings.Contains(string(result.Body), `"already_present"`)) {
			t.Fatal("native duplicate detection failed")
		}
		if index == 2 && (result.StatusCode != 200 || !strings.Contains(string(result.Body), `"tracked_image_remaining":3`)) {
			t.Fatal("native inventory failed")
		}
		if index == 3 && result.StatusCode != 503 {
			t.Fatal("native missing context accepted")
		}
	}
	t.Log("Fresh .so: duplicate and inventory safe; missing scope rejected; 4 C host calls/frees; 2 private GETs; zero POSTs")
}
