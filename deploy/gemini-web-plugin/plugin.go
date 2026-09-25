package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type pluginConfig struct {
	HostEnabled        bool   `yaml:"enabled"`
	HostPriority       int    `yaml:"priority"`
	SessionDir         string `yaml:"session_dir"`
	ManagerOrigin      string `yaml:"manager_origin"`
	BrowserExtensionID string `yaml:"browser_extension_id"`
	DashboardPath      string `yaml:"dashboard_path"`
	// NativeGeneration serves text turns by speaking to the web product directly
	// instead of delegating to the sidecar. Off until the native path has been
	// compared against the sidecar on a real account.
	NativeGeneration   bool `yaml:"native_generation"`
	NativeContinuation bool `yaml:"native_continuation"`
	// Drive credentials live here as well as in the environment because the
	// configuration is mounted and hot reloaded, while the environment can only
	// change by recreating the container.
	DriveAPIKey       string `yaml:"drive_api_key"`
	DriveClientID     string `yaml:"drive_client_id"`
	DriveClientSecret string `yaml:"drive_client_secret"`
	DriveRefreshToken string `yaml:"drive_refresh_token"`
}
type hostCall func(string, []byte) ([]byte, error)
type service struct {
	mu               sync.RWMutex
	config           pluginConfig
	host             hostCall
	client           *http.Client
	now              func() time.Time
	continuationWait func(context.Context) error
	interactionsMu   sync.Mutex
	interactions     map[string]*interactionOperation
	fileLeases       fileLeases
	leases           credentialLeases
	sessions         *sessionStore
	sessionKeyHash   [32]byte
	loginMu          sync.Mutex
	logins           map[string]loginFlow
	modelPending     map[string]uint64
	lifecycle        sessionLifecycle
	accountsMu       sync.Mutex
	accountsCache    map[string]cachedAccountList
	// webOriginOverride redirects the native web calls; it is set only by tests,
	// which cannot reach the real product.
	webOriginOverride string
	// webRotateOverride redirects cookie rotation, which lives on a different
	// Google origin than the rest of the native calls.
	webRotateOverride string
	webUploadOverride string
	// driveOverride redirects the Drive fetch, which lives on a Google API origin
	// none of the other native calls use, and driveTokenOverride does the same for
	// the OAuth exchange, which lives on a third.
	driveOverride      string
	driveTokenOverride string
	driveAccess        driveToken
	startedAt          int64
	quota              quotaCache
}

func newService(host hostCall) *service {
	return &service{config: pluginConfig{DashboardPath: "/CLIProxyAPI/plugins/gemini-web/index.html"}, startedAt: time.Now().Unix(), host: host, client: newSidecarClient(), now: time.Now}
}

func (service *service) handle(ctx context.Context, method string, raw []byte) []byte {
	var result interface{}
	err := service.lifecycle.enter()
	if err == nil {
		defer service.lifecycle.leave()
	}
	if err == nil && (method == "plugin.register" || method == "plugin.reconfigure") {
		service.mu.Lock()
		result, err = service.register(raw)
		service.mu.Unlock()
	} else if err == nil {
		result, err = service.dispatch(ctx, method, raw)
	}
	response := envelope{OK: err == nil}
	if err != nil {
		var public *publicError
		if errors.As(err, &public) {
			response.Error = public
		} else {
			response.Error = failure(500, "plugin_operation_failed")
		}
	} else {
		encoded, err := json.Marshal(result)
		if err != nil {
			response.OK = false
			response.Error = failure(500, "response_encoding_failed")
		} else {
			response.Result = encoded
		}
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return []byte(`{"ok":false,"error":{"code":"response_encoding_failed","message":"response_encoding_failed","http_status":500}}`)
	}
	return encoded
}

func (service *service) settings() pluginConfig {
	service.mu.RLock()
	defer service.mu.RUnlock()
	return service.config
}

func (service *service) register(raw []byte) (interface{}, error) {
	var request struct {
		ConfigYAML []byte `json:"config_yaml"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &request) != nil {
		return nil, failure(400, "invalid_registration")
	}
	config := pluginConfig{DashboardPath: "/CLIProxyAPI/plugins/gemini-web/index.html"}
	if len(request.ConfigYAML) > 0 {
		decoder := yaml.NewDecoder(bytes.NewReader(request.ConfigYAML))
		decoder.KnownFields(true)
		if err := decoder.Decode(&config); err != nil {
			return nil, failure(400, "invalid_plugin_config")
		}
	}
	if !filepath.IsAbs(config.DashboardPath) || filepath.Ext(config.DashboardPath) != ".html" {
		return nil, failure(400, "invalid_plugin_config")
	}
	current := service.config
	current.HostEnabled, current.HostPriority = config.HostEnabled, config.HostPriority
	unchanged := reflect.DeepEqual(current, config) && (config.SessionDir == "" || service.sessions != nil && service.sessionKeyHash == sha256.Sum256([]byte(os.Getenv("GEMINI_WEB_SESSION_KEY"))))
	if !unchanged {
		if err := service.lifecycle.reconfigure(func() error {
			if err := service.reconfigureCredentials(config); err != nil {
				return err
			}
			if err := service.configureSessions(config); err != nil {
				return err
			}
			if current.DriveClientID != config.DriveClientID || current.DriveClientSecret != config.DriveClientSecret || current.DriveRefreshToken != config.DriveRefreshToken {
				service.driveAccess.mu.Lock()
				service.driveAccess.value, service.driveAccess.scopes = "", ""
				service.driveAccess.expires = time.Time{}
				service.driveAccess.mu.Unlock()
			}
			service.config = config
			return nil
		}); err != nil {
			return nil, err
		}
	}
	registration := json.RawMessage(`{"schema_version":6,"metadata":{"Name":"gemini-web","Version":"0.1.0","Author":"jclee941","GitHubRepository":"https://github.com/jclee941/CLIProxyAPI","Logo":"","ConfigFields":[{"Name":"dashboard_path","Type":"string","Description":"Absolute path to the separately built static dashboard HTML"},{"Name":"session_dir","Type":"string","Description":"Dedicated 0700 encrypted application-session directory; single process owner"},{"Name":"manager_origin","Type":"string","Description":"Exact HTTPS management portal origin"},{"Name":"browser_extension_id","Type":"string","Description":"Registered 32-character browser companion extension ID"}]},"capabilities":{"auth_provider":true,"model_provider":true,"executor":true,"executor_model_scope":"oauth","executor_input_formats":["gemini"],"executor_output_formats":["gemini"],"management_api":true,"frontend_http":true,"request_interceptor":true,"quota_provider":true}}`)
	if config.NativeContinuation {
		registration = bytes.Replace(registration, []byte(`"request_interceptor":true`), []byte(`"request_interceptor":true,"scheduler":true`), 1)
		registration = bytes.ReplaceAll(registration, []byte(`["gemini"]`), []byte(`["gemini","interactions"]`))
	}
	return registration, nil
}

func (service *service) dispatch(ctx context.Context, method string, raw []byte) (interface{}, error) {
	switch method {
	case "auth.identifier", "executor.identifier", "quota.identifier":
		return struct {
			Identifier string `json:"identifier"`
		}{provider}, nil
	case "quota.describe":
		return quotaDescription{SupportedProviders: []string{provider}, DisplayName: "Gemini Web", SupportsReset: false}, nil
	case "quota.fetch":
		return service.quotaFetch(ctx, raw)
	case "quota.reset":
		return nil, failure(400, "quota_reset_unsupported")
	case "auth.parse":
		var request struct{ RawJSON []byte }
		if json.Unmarshal(raw, &request) != nil {
			return nil, failure(400, "invalid_auth_request")
		}
		var kind struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(request.RawJSON, &kind) != nil {
			return nil, failure(400, "invalid_auth_request")
		}
		if kind.Type != provider {
			return struct{ Handled bool }{false}, nil
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
	case "auth.login.start", "auth.login.poll":
		return nil, failure(400, "use_authenticated_accounts_management_route")
	case "auth.refresh", "model.for_auth":
		return service.authOperation(ctx, method, raw)
	case "model.static", "model.register":
		return struct {
			Provider string
			Models   []modelInfo
		}{provider, []modelInfo{}}, nil
	case "management.register":
		return json.RawMessage(`{"routes":[{"Method":"GET","Path":"/plugins/gemini-web/accounts"},{"Method":"POST","Path":"/plugins/gemini-web/refresh"},{"Method":"POST","Path":"/plugins/gemini-web/maintain"},{"Method":"POST","Path":"/plugins/gemini-web/resolve"},{"Method":"POST","Path":"/plugins/gemini-web/recover"},{"Method":"POST","Path":"/plugins/gemini-web/label"},{"Method":"POST","Path":"/plugins/gemini-web/detach"},{"Method":"POST","Path":"/plugins/gemini-web/login/start"},{"Method":"POST","Path":"/plugins/gemini-web/login/complete"},{"Method":"POST","Path":"/plugins/gemini-web/login/status"},{"Method":"POST","Path":"/plugins/gemini-web/login/cancel"},{"Method":"POST","Path":"/plugins/gemini-web/login/reconcile"}],"resources":[{"Path":"/extension"},{"Path":"/openapi.json"},{"Path":"/index","Menu":"Gemini Web","Description":"Account models and measured usage dashboard"}]}`), nil
	case "management.handle":
		return service.management(ctx, raw)
	case "frontend_http.register":
		return filesRegistration(), nil
	case "frontend_auth.authenticate":
		return service.filesAuthenticateUpload(raw), nil
	case "frontend_http.handle":
		return service.filesHTTP(ctx, raw)
	case "request.intercept_before", "request.intercept_after":
		return service.interceptContinuation(raw), nil
	case "scheduler.pick":
		return service.pickContinuation(raw)
	case "executor.execute", "executor.execute_stream", "executor.count_tokens":
		return service.execute(ctx, method, raw)
	case "executor.http_request":
		return service.executorHTTP(ctx, raw)
	default:
		return nil, failure(400, "unknown_method")
	}
}

func (service *service) resolve(ctx context.Context, raw []byte, identity string) (storageRecord, sessionToken, error) {
	record, err := service.parseStorage(raw, false)
	if err != nil {
		return record, sessionToken{}, err
	}
	if identity != record.ID {
		return record, sessionToken{}, failure(400, "auth_identity_mismatch")
	}
	if !localReferencePattern.MatchString(record.TokenRef) {
		return record, sessionToken{}, failure(400, "invalid_token_reference")
	}
	token, err := service.resolveLocal(record)
	return record, token, err
}

func (service *service) authOperation(ctx context.Context, method string, raw []byte) (interface{}, error) {
	var request struct {
		AuthID, AuthProvider string
		StorageJSON          []byte
	}
	if json.Unmarshal(raw, &request) != nil || request.AuthProvider != provider {
		return nil, failure(400, "invalid_auth_request")
	}
	if method == "model.for_auth" {
		record, err := service.parseStorage(request.StorageJSON, false)
		if err != nil {
			return nil, err
		}
		if record.ID != request.AuthID {
			return nil, failure(400, "auth_identity_mismatch")
		}
		if localReferencePattern.MatchString(record.TokenRef) {
			models, err := service.localAuthModels(ctx, record)
			return struct {
				Provider string
				Models   []modelInfo
			}{provider, models}, err
		}
	}
	record, token, err := service.resolve(ctx, request.StorageJSON, request.AuthID)
	if err != nil {
		return nil, err
	}
	account, err := service.accountModels(ctx, record.TokenRef, token)
	if err != nil {
		return nil, err
	}
	if !localReferencePattern.MatchString(record.TokenRef) {
		if err := service.rejectMigratedRecord(record); err != nil {
			return nil, err
		}
	}
	switch method {
	case "model.for_auth":
		return struct {
			Provider string
			Models   []modelInfo
		}{provider, service.interactionModels(account)}, nil
	case "auth.refresh":
		auth, err := authFromRecord(record)
		return struct {
			Auth             authData
			NextRefreshAfter time.Time
		}{auth, service.now().Add(15 * time.Minute)}, err
	default:
		return nil, failure(400, "unknown_method")
	}
}
