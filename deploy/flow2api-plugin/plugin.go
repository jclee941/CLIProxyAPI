package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

const provider = "flow2api"
const flowPath = "/v0/management/plugins/flow2api/accounts"

type pluginConfig struct {
	Enabled             bool     `yaml:"enabled"`
	Priority            int      `yaml:"priority"`
	FlowAccounts        []string `yaml:"accounts"`
	SessionBrokerURL    string   `yaml:"session_broker_url"`
	FlowCaptchaProvider string   `yaml:"captcha_provider"`
	FlowCaptchaKey      string   `yaml:"captcha_key"`
	FlowCaptchaBaseURL  string   `yaml:"captcha_base_url"`
	FlowCaptchaTask     string   `yaml:"captcha_task"`
}

type hostCall func(string, []byte) ([]byte, error)
type service struct {
	mu                      sync.RWMutex
	config                  pluginConfig
	host                    hostCall
	client                  *http.Client
	flowMu                  sync.Mutex
	flowPages               map[string]flowPage
	flowProjects            map[string]string
	flowOriginOverride      string
	recaptchaOriginOverride string
	flowWait                func(context.Context, time.Duration) error
}

func newService(host hostCall) *service {
	return &service{
		host:   host,
		config: pluginConfig{SessionBrokerURL: "http://127.0.0.1:8317", FlowCaptchaProvider: "native"},
		client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

func (service *service) stop() { service.client.CloseIdleConnections() }
func (service *service) settings() pluginConfig {
	service.mu.RLock()
	defer service.mu.RUnlock()
	return service.config
}

func (service *service) handle(ctx context.Context, method string, raw []byte) []byte {
	result, err := service.dispatch(ctx, method, raw)
	response := envelope{OK: err == nil}
	if err != nil {
		if !errors.As(err, &response.Error) {
			response.Error = failure(500, "flow_plugin_operation_failed")
		}
	} else {
		response.Result, err = json.Marshal(result)
		if err != nil {
			response.OK, response.Error = false, failure(500, "flow_response_encoding_failed")
		}
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return []byte(`{"ok":false,"error":{"code":"flow_response_encoding_failed","message":"flow_response_encoding_failed","http_status":500}}`)
	}
	return encoded
}

func (service *service) register(raw []byte) (interface{}, error) {
	var request struct {
		ConfigYAML []byte `json:"config_yaml"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &request) != nil {
		return nil, failure(400, "invalid_plugin_config")
	}
	config := pluginConfig{SessionBrokerURL: "http://127.0.0.1:8317", FlowCaptchaProvider: "native"}
	if len(request.ConfigYAML) > 0 {
		decoder := yaml.NewDecoder(bytes.NewReader(request.ConfigYAML))
		decoder.KnownFields(true)
		if decoder.Decode(&config) != nil {
			return nil, failure(400, "invalid_plugin_config")
		}
	}
	switch config.FlowCaptchaProvider {
	case "", "native", "yescaptcha", "capsolver":
	default:
		return nil, failure(400, "invalid_plugin_config")
	}
	broker, err := url.Parse(config.SessionBrokerURL)
	if err != nil || broker.Host == "" || broker.User != nil || broker.Scheme != "http" && broker.Scheme != "https" {
		return nil, failure(400, "invalid_plugin_config")
	}
	if config.FlowCaptchaBaseURL != "" {
		solver, err := url.Parse(config.FlowCaptchaBaseURL)
		if err != nil || solver.Scheme != "https" || solver.Host == "" || solver.User != nil {
			return nil, failure(400, "invalid_plugin_config")
		}
	}
	for _, id := range config.FlowAccounts {
		if !sourceAccountPattern.MatchString(id) {
			return nil, failure(400, "invalid_plugin_config")
		}
	}
	service.mu.Lock()
	service.config = config
	service.mu.Unlock()
	return json.RawMessage(`{"schema_version":6,"metadata":{"Name":"flow2api","Version":"0.1.0","Author":"jclee941","GitHubRepository":"https://github.com/jclee941/CLIProxyAPI","ConfigFields":[{"Name":"accounts","Type":"array","Description":"Existing Gemini account IDs allowed for Flow"},{"Name":"session_broker_url","Type":"string","Description":"Local core URL serving the Gemini session exchange"},{"Name":"captcha_provider","Type":"enum","EnumValues":["native","yescaptcha","capsolver"],"Description":"Flow token provider"},{"Name":"captcha_key","Type":"string","Description":"External token provider credential"}]},"capabilities":{"auth_provider":true,"model_provider":true,"executor":true,"executor_model_scope":"oauth","executor_input_formats":["gemini"],"executor_output_formats":["gemini","openai","openai-response","claude"],"management_api":true}}`), nil
}

func (service *service) dispatch(ctx context.Context, method string, raw []byte) (interface{}, error) {
	switch method {
	case "plugin.register", "plugin.reconfigure":
		return service.register(raw)
	case "auth.identifier", "executor.identifier":
		return map[string]string{"identifier": provider}, nil
	case "model.static", "model.register":
		return struct {
			Provider string
			Models   []modelInfo
		}{provider, []modelInfo{}}, nil
	case "auth.parse":
		var request struct{ RawJSON []byte }
		if json.Unmarshal(raw, &request) != nil {
			return nil, failure(400, "flow_invalid_auth_request")
		}
		var kind struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(request.RawJSON, &kind) != nil {
			return nil, failure(400, "flow_invalid_auth_request")
		}
		if kind.Type != provider {
			return map[string]bool{"Handled": false}, nil
		}
		record, err := service.parseStorage(request.RawJSON, true)
		if err != nil {
			return nil, err
		}
		auth, err := authFromRecord(record)
		return struct {
			Handled bool
			Auth    authData
		}{true, auth}, err
	case "auth.refresh", "model.for_auth":
		var request struct {
			AuthID, AuthProvider string
			StorageJSON          []byte
		}
		if json.Unmarshal(raw, &request) != nil || request.AuthProvider != provider {
			return nil, failure(400, "flow_invalid_auth_request")
		}
		record, err := service.parseStorage(request.StorageJSON, false)
		if err != nil {
			return nil, err
		}
		if record.ID != request.AuthID {
			return nil, failure(400, "flow_auth_identity_mismatch")
		}
		if method == "model.for_auth" {
			models := []modelInfo{}
			if !record.Disabled && service.flowAccount(record.SourceAuthID) {
				models = flowModelInfos()
			}
			return struct {
				Provider string
				Models   []modelInfo
			}{provider, models}, nil
		}
		auth, err := authFromRecord(record)
		return struct{ Auth authData }{auth}, err
	case "auth.login.start", "auth.login.poll":
		return nil, failure(400, "flow_use_existing_gemini_session")
	case "executor.execute", "executor.execute_stream":
		var request executorRequest
		if json.Unmarshal(raw, &request) != nil {
			return nil, failure(400, "flow_invalid_execution_request")
		}
		model, ok := flowModelFor(request.Model)
		if !ok {
			return nil, failure(400, "flow_unsupported_model")
		}
		return service.executeFlow(ctx, method, request, model)
	case "executor.count_tokens", "executor.http_request":
		return nil, failure(400, "flow_operation_unsupported")
	case "management.register":
		return json.RawMessage(`{"routes":[{"Method":"GET","Path":"/plugins/flow2api/accounts"}]}`), nil
	case "management.handle":
		var request managementRequest
		if json.Unmarshal(raw, &request) != nil {
			return nil, failure(400, "flow_invalid_request")
		}
		if request.Method != "GET" || request.Path != flowPath {
			return nil, failure(404, "flow_route_not_found")
		}
		result, err := service.flowStatus(ctx, request)
		if err != nil {
			return nil, err
		}
		body, err := json.Marshal(result)
		return httpResponse{StatusCode: 200, Headers: http.Header{"Content-Type": {"application/json"}, "Cache-Control": {"no-store"}}, Body: body}, err
	default:
		return nil, failure(400, "flow_unknown_method")
	}
}

var sourceAccountPattern = regexp.MustCompile(`^gemini-web-[A-Za-z0-9_-]+\.json$`)
var flowAccountPattern = regexp.MustCompile(`^flow2api-[A-Za-z0-9_-]+\.json$`)

func (service *service) parseStorage(raw []byte, _ bool) (storageRecord, error) {
	var record storageRecord
	if json.Unmarshal(raw, &record) != nil || record.Type != provider || !flowAccountPattern.MatchString(record.ID) || !sourceAccountPattern.MatchString(record.SourceAuthID) || record.Label == "" {
		return record, failure(400, "flow_invalid_auth_storage")
	}
	return record, nil
}

func authFromRecord(record storageRecord) (authData, error) {
	rules := make([]stopRule, 0, 300)
	for status := 300; status < 600; status++ {
		rules = append(rules, stopRule{Status: status, Match: []string{"flow_"}, Action: "stop"})
	}
	raw, err := json.Marshal(struct {
		storageRecord
		RequestScopedErrors []stopRule `json:"request_scoped_errors"`
	}{record, rules})
	if err != nil {
		return authData{}, failure(500, "flow_auth_encoding_failed")
	}
	return authData{Provider: provider, ID: record.ID, FileName: record.ID, Label: record.Label, ProxyURL: "direct", Disabled: record.Disabled, StorageJSON: raw, Metadata: authMetadata{Type: provider, SourceAuthID: record.SourceAuthID, RequestScopedErrors: rules}}, nil
}

func hasRequestStopRules(rules []stopRule, prefix string) bool {
	for status := 300; status < 600; status++ {
		found := false
		for _, rule := range rules {
			if rule.Status == status && rule.Action == "stop" && slices.Contains(rule.Match, prefix) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (service *service) report(fields map[string]any, message string) {
	if service.host == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{"level": "warn", "message": message, "fields": fields})
	if err == nil {
		_, _ = service.host("host.log", payload)
	}
}

func brokerCredential() string { return os.Getenv("MANAGEMENT_PASSWORD") }
