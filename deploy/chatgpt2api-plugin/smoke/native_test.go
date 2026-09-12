package smoke

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  struct {
		Code       string
		HTTPStatus int `json:"http_status"`
	}
}

func TestNativeABI_whenLoaded_routesRegistersAndServesStaticResource(t *testing.T) {
	dashboard := filepath.Join(t.TempDir(), "index.html")
	html := []byte("<!doctype html><title>fixture</title>")
	if err := os.WriteFile(dashboard, html, 0600); err != nil {
		t.Fatal(err)
	}
	config, err := json.Marshal(struct {
		ConfigYAML []byte `json:"config_yaml"`
	}{[]byte("model_names: [gpt-test]\ndashboard_path: " + dashboard)})
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "../abi-smoke", "../chatgpt2api.so",
		"plugin.register", string(config),
		"management.register", `{}`,
		"model.route", `{"RequestedModel":"gpt-test","AvailableProviders":["openai-compatible-chatgpt2api"],"Headers":{"Authorization":["Bearer test-sentinel"]},"Body":{"ignored":"test-sentinel"}}`,
		"model.route", `{"RequestedModel":"removed-model","AvailableProviders":["openai-compatible-chatgpt2api"]}`,
		"management.handle", `{"Method":"GET","Path":"/v0/resource/plugins/chatgpt2api/index","Query":{"path":["/etc/passwd"]}}`,
		"management.handle", `{"Method":"POST","Path":"/v0/management/plugins/chatgpt2api/status"}`,
		"auth.parse", `{"secret":"test-sentinel"}`,
		"model.route", `{"RequestedModel":"gpt-test","AvailableProviders":[]}`)

	output, errRun := command.CombinedOutput()

	if errRun != nil {
		t.Fatalf("C ABI smoke: %v: %s", errRun, output)
	}
	if bytes.Contains(output, []byte("test-sentinel")) {
		t.Fatal("C ABI leaked request data")
	}
	lines := bytes.Split(bytes.TrimSpace(output), []byte("\n"))
	if len(lines) != 8 {
		t.Fatalf("expected 8 responses, got %d", len(lines))
	}
	responses := make([]envelope, len(lines))
	for index, line := range lines {
		if err := json.Unmarshal(line, &responses[index]); err != nil {
			t.Fatal(err)
		}
		if responses[index].OK != (index != 6) {
			t.Fatalf("unexpected envelope %d: %s", index, line)
		}
	}
	var registration struct {
		SchemaVersion int `json:"schema_version"`
		Capabilities  map[string]bool
	}
	if err := json.Unmarshal(responses[0].Result, &registration); err != nil {
		t.Fatal(err)
	}
	if registration.SchemaVersion != 6 || len(registration.Capabilities) != 2 || !registration.Capabilities["model_router"] || !registration.Capabilities["management_api"] {
		t.Fatal("wrong native registration")
	}
	var route struct {
		Handled                         bool
		TargetKind, Target, TargetModel string
	}
	if err := json.Unmarshal(responses[2].Result, &route); err != nil {
		t.Fatal(err)
	}
	if !route.Handled || route.TargetKind != "provider" || route.Target != "openai-compatible-chatgpt2api" || route.TargetModel != "" {
		t.Fatalf("wrong native route: %+v", route)
	}
	for _, index := range []int{3, 7} {
		var declined struct {
			Handled                         bool
			TargetKind, Target, TargetModel string
		}
		if err := json.Unmarshal(responses[index].Result, &declined); err != nil {
			t.Fatal(err)
		}
		if declined.Handled || declined.TargetKind != "" || declined.Target != "" || declined.TargetModel != "" {
			t.Fatal("native decline changed route")
		}
	}
	var resource struct {
		StatusCode int
		Body       []byte
		Headers    map[string][]string
	}
	if err := json.Unmarshal(responses[4].Result, &resource); err != nil {
		t.Fatal(err)
	}
	if resource.StatusCode != 200 || !bytes.Equal(resource.Body, html) || resource.Headers["Content-Type"][0] != "text/html; charset=utf-8" {
		t.Fatal("native resource mismatch")
	}
	var status struct {
		StatusCode int
		Body       []byte
	}
	if err := json.Unmarshal(responses[5].Result, &status); err != nil {
		t.Fatal(err)
	}
	if status.StatusCode != 405 || string(status.Body) != `{"error":"method_not_allowed"}` {
		t.Fatal("native management method guard failed")
	}
	if responses[6].Error.Code != "unknown_method" {
		t.Fatal("native executor/auth surface present")
	}
}
