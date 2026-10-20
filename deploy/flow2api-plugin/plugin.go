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
	"strings"
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
	// HealthCheckProject is the Flow project health checks generate in. Empty
	// uses the account's project titled flowHealthTitle, created when missing.
	HealthCheckProject string `yaml:"health_check_project"`
	// UnusualActivityHoldSeconds and UnusualActivityHoldCapSeconds replace the
	// first unusual-activity hold and its cap; 0 keeps the defaults.
	UnusualActivityHoldSeconds    int `yaml:"unusual_activity_hold_seconds"`
	UnusualActivityHoldCapSeconds int `yaml:"unusual_activity_hold_cap_seconds"`
	// HealthCheckIntervalSeconds replaces how often an idle account is checked;
	// 0 keeps flowHealthEvery and a negative value turns periodic checks off.
	HealthCheckIntervalSeconds int `yaml:"health_check_interval_seconds"`
	// HealthCheckRetrySeconds replaces flowHealthRetry, the wait before a held
	// account's check that could not tell is run again; 0 keeps it.
	HealthCheckRetrySeconds int `yaml:"health_check_retry_seconds"`
	// TelegramBotToken and TelegramChatID receive health cards; empty uses the
	// core's TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID.
	TelegramBotToken string `yaml:"telegram_bot_token"`
	TelegramChatID   string `yaml:"telegram_chat_id"`
}

type hostCall func(string, []byte) ([]byte, error)
type service struct {
	mu                      sync.RWMutex
	config                  pluginConfig
	host                    hostCall
	client                  *http.Client
	now                     func() time.Time
	dashboard               string
	flowMu                  sync.Mutex
	flowPages               map[string]flowPage
	flowProjects            map[string]string
	lowPriorityPausedUntil  map[string]time.Time
	holds                   map[string]*flowHold
	quiesced                bool
	dailyQuotaHeldUntil     map[string]time.Time
	flowReadings            map[string]flowReading
	flowSpending            int
	flowSpendSequence       uint64
	captchaBrowser          flowBrowser
	flowOriginOverride      string
	recaptchaOriginOverride string
	flowWait                func(context.Context, time.Duration) error
	healthProjects          map[string]string
	healthChecked           map[string]time.Time
	healthStates            map[string]flowHealthState
	flowAccepted            map[string]time.Time
	accountLabels           map[string]string
	periodicChecks          sync.Once
	telegramOrigin          string
	afterFunc               func(time.Duration, func()) *time.Timer
	// lifetime ends when the host shuts the plugin down, which cancels a
	// running health check.
	lifetime    context.Context
	endLifetime context.CancelFunc
}

func newService(host hostCall) *service {
	lifetime, endLifetime := context.WithCancel(context.Background())
	return &service{
		host:        host,
		now:         time.Now,
		afterFunc:   time.AfterFunc,
		lifetime:    lifetime,
		endLifetime: endLifetime,
		dashboard:   "/CLIProxyAPI/plugins/flow2api/index.html",
		config:      pluginConfig{SessionBrokerURL: "http://127.0.0.1:8317", FlowCaptchaProvider: "native"},
		client:      &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

func (service *service) stop() {
	service.endLifetime()
	service.flowMu.Lock()
	for _, hold := range service.holds {
		if hold.timer != nil {
			hold.timer.Stop()
		}
	}
	service.flowMu.Unlock()
	service.captchaBrowser.close(service.client)
	service.client.CloseIdleConnections()
}

// quiesce readies this instance for a replacement loaded into the same host,
// which calls it before loading the new build. The host keeps a replaced
// instance to finish its running calls but never shuts it down, so the work it
// does on its own stops here: the periodic health checks, a held account's
// scheduled check, health cards, and its captcha browser page. Calls are still
// served. A register resumes the work, which happens when the host keeps this
// instance because its replacement failed to load.
func (service *service) quiesce() {
	service.flowMu.Lock()
	service.quiesced = true
	for _, hold := range service.holds {
		if hold.timer != nil {
			hold.timer.Stop()
			hold.timer = nil
		}
	}
	service.flowMu.Unlock()
	service.captchaBrowser.close(service.client)
}

func (service *service) resumeQuiesced() {
	service.flowMu.Lock()
	defer service.flowMu.Unlock()
	if !service.quiesced {
		return
	}
	service.quiesced = false
	for _, hold := range service.holds {
		if hold.timer == nil {
			service.scheduleCheckLocked(hold.record, hold, max(hold.until.Sub(service.now()), 0))
		}
	}
}
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
		if method == "executor.execute" || method == "executor.execute_stream" {
			response.Error = executionFailure(response.Error)
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

// executionFailure answers a model request with an error object. The host hands
// a JSON message to the caller as it stands, while a bare code is filed under a
// generic one for its status, which showed an expired Flow session (401) as the
// caller's own API key being invalid. The flow_ stop rules still match inside it.
func executionFailure(public *publicError) *publicError {
	body, err := json.Marshal(map[string]map[string]string{"error": {"code": public.Code, "message": public.Message}})
	if err != nil {
		return public
	}
	return &publicError{Code: public.Code, Message: string(body), HTTPStatus: public.HTTPStatus}
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
		// A solver's key travels in the request body, so its address must be https.
		if config.FlowCaptchaBaseURL != "" {
			solver, err := url.Parse(config.FlowCaptchaBaseURL)
			if err != nil || solver.Scheme != "https" || solver.Host == "" || solver.User != nil {
				return nil, failure(400, "invalid_plugin_config")
			}
		}
	case "browser":
		// The DevTools address of a signed-out Chrome on the core's private
		// network. DevTools speaks plain http and carries no credential.
		browser, err := url.Parse(config.FlowCaptchaBaseURL)
		if err != nil || browser.Scheme != "http" || browser.Host == "" || browser.User != nil || browser.Path != "" && browser.Path != "/" {
			return nil, failure(400, "invalid_plugin_config")
		}
	default:
		return nil, failure(400, "invalid_plugin_config")
	}
	if config.HealthCheckProject != "" && !flowUUIDPattern.MatchString(config.HealthCheckProject) || config.UnusualActivityHoldSeconds < 0 || config.UnusualActivityHoldCapSeconds < 0 ||
		config.HealthCheckIntervalSeconds > 0 && config.HealthCheckIntervalSeconds < 60 || config.HealthCheckRetrySeconds < 0 || config.HealthCheckRetrySeconds > 0 && config.HealthCheckRetrySeconds < 30 || strings.ContainsAny(config.TelegramBotToken+config.TelegramChatID, " \r\n/") {
		return nil, failure(400, "invalid_plugin_config")
	}
	broker, err := url.Parse(config.SessionBrokerURL)
	if err != nil || broker.Host == "" || broker.User != nil || broker.Scheme != "http" && broker.Scheme != "https" {
		return nil, failure(400, "invalid_plugin_config")
	}
	for _, id := range config.FlowAccounts {
		if !sourceAccountPattern.MatchString(id) {
			return nil, failure(400, "invalid_plugin_config")
		}
	}
	service.mu.Lock()
	service.config = config
	service.mu.Unlock()
	service.periodicChecks.Do(func() { go service.runPeriodicHealthChecks() })
	return json.RawMessage(`{"schema_version":6,"metadata":{"Name":"flow2api","Version":"0.1.0","Author":"jclee941","GitHubRepository":"https://github.com/jclee941/CLIProxyAPI","ConfigFields":[{"Name":"accounts","Type":"array","Description":"Existing Gemini account IDs allowed for Flow"},{"Name":"session_broker_url","Type":"string","Description":"Local core URL serving the Gemini session exchange"},{"Name":"captcha_provider","Type":"enum","EnumValues":["native","browser","yescaptcha","capsolver"],"Description":"Flow token provider"},{"Name":"captcha_key","Type":"string","Description":"External token provider credential"},{"Name":"health_check_project","Type":"string","Description":"Flow project the health check of a held account generates in; empty uses the account's project titled CLIProxyAPI health check, created when missing"},{"Name":"unusual_activity_hold_seconds","Type":"integer","Description":"First hold after Flow refuses the account for unusual activity; 0 keeps 1800"},{"Name":"unusual_activity_hold_cap_seconds","Type":"integer","Description":"Longest hold after repeated refusals; 0 keeps 14400"},{"Name":"health_check_interval_seconds","Type":"integer","Description":"How often an account with no accepted Flow request in that time is health checked; 0 keeps 3600, a negative value turns it off"},{"Name":"health_check_retry_seconds","Type":"integer","Description":"Wait before a held account's health check that could not tell, for example a failed project lookup, runs again; 0 keeps 300"},{"Name":"telegram_bot_token","Type":"string","Description":"Telegram bot that receives Flow health cards; empty uses the core's TELEGRAM_BOT_TOKEN"},{"Name":"telegram_chat_id","Type":"string","Description":"Telegram chat for Flow health cards; empty uses the core's TELEGRAM_CHAT_ID"}]},"capabilities":{"auth_provider":true,"model_provider":true,"executor":true,"executor_model_scope":"oauth","executor_input_formats":["gemini"],"executor_output_formats":["gemini","openai","openai-response","claude"],"management_api":true,"frontend_http":true,"quota_provider":true}}`), nil
}

func (service *service) dispatch(ctx context.Context, method string, raw []byte) (interface{}, error) {
	switch method {
	case "plugin.register", "plugin.reconfigure":
		service.resumeQuiesced()
		return service.register(raw)
	case "plugin.quiesce":
		service.quiesce()
		return struct{}{}, nil
	case "frontend_http.register":
		return flowHTTPRegistration(), nil
	case "frontend_http.handle":
		return service.flowHTTP(ctx, raw)
	case "auth.identifier", "executor.identifier", "quota.identifier":
		return map[string]string{"identifier": provider}, nil
	case "quota.describe":
		return quotaDescription{SupportedProviders: []string{provider}, DisplayName: "Google Flow"}, nil
	case "quota.fetch":
		return service.quotaFetch(ctx, raw)
	case "quota.reset":
		return nil, failure(400, "flow_quota_reset_unsupported")
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
		defer service.beginFlowSpend()()
		return service.executeFlow(ctx, method, request, model)
	case "executor.count_tokens", "executor.http_request":
		return nil, failure(400, "flow_operation_unsupported")
	case "management.register":
		return json.RawMessage(`{"routes":[{"Method":"GET","Path":"/plugins/flow2api/accounts"},{"Method":"POST","Path":"/plugins/flow2api/telegram/test"}],"resources":[{"Path":"/index","Menu":"Google Flow","Description":"Flow credits, account connection status, and supported media models"}]}`), nil
	case "management.handle":
		var request managementRequest
		if json.Unmarshal(raw, &request) != nil {
			return nil, failure(400, "flow_invalid_request")
		}
		if request.Method == http.MethodGet && request.Path == flowResourcePath {
			return service.flowDashboard()
		}
		if request.Method == http.MethodPost && request.Path == flowTelegramTestPath {
			return service.telegramTest(ctx)
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
		// The host reads unknown_method as an optional method this plugin
		// does not offer.
		return nil, failure(400, "unknown_method")
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
