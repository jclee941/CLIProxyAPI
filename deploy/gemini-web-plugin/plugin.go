package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type pluginConfig struct {
	Vault              string                       `yaml:"vault"`
	DashboardPath      string                       `yaml:"dashboard_path"`
	MaintenanceSources map[string]maintenanceSource `yaml:"maintenance_sources"`
}
type hostCall func(string, []byte) ([]byte, error)
type service struct {
	mu      sync.RWMutex
	config  pluginConfig
	host    hostCall
	secrets secretStore
	client  *http.Client
	now     func() time.Time
	source  credentialSource
	leases  credentialLeases
}

func newService(host hostCall) *service {
	return &service{config: pluginConfig{Vault: "homelab", DashboardPath: "/CLIProxyAPI/plugins/gemini-web/index.html"}, host: host, secrets: opStore{vault: "homelab", run: runOP}, client: newSidecarClient(), now: time.Now, source: newCDPCredentialSource()}
}

func (service *service) handle(ctx context.Context, method string, raw []byte) []byte {
	var result interface{}
	var err error
	if method == "plugin.register" || method == "plugin.reconfigure" {
		service.mu.Lock()
		result, err = service.register(raw)
		service.mu.Unlock()
	} else {
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
	config := pluginConfig{Vault: "homelab", DashboardPath: "/CLIProxyAPI/plugins/gemini-web/index.html"}
	if len(request.ConfigYAML) > 0 && yaml.Unmarshal(request.ConfigYAML, &config) != nil {
		return nil, failure(400, "invalid_plugin_config")
	}
	if config.Vault != "homelab" || !filepath.IsAbs(config.DashboardPath) || filepath.Ext(config.DashboardPath) != ".html" {
		return nil, failure(400, "invalid_plugin_config")
	}
	if err := validateMaintenanceSources(config.MaintenanceSources, config.Vault); err != nil {
		return nil, err
	}
	if err := service.reconfigureCredentials(config); err != nil {
		return nil, err
	}
	service.config = config
	return json.RawMessage(`{"schema_version":6,"metadata":{"Name":"gemini-web","Version":"0.1.0","Author":"jclee941","GitHubRepository":"https://github.com/jclee941/CLIProxyAPI","Logo":"","ConfigFields":[{"Name":"vault","Type":"enum","EnumValues":["homelab"],"Description":"1Password vault; web-session field references only"},{"Name":"dashboard_path","Type":"string","Description":"Absolute path to the separately built static dashboard HTML"},{"Name":"maintenance_sources","Type":"object","Description":"Non-secret account-ID keyed credential source bindings"}]},"capabilities":{"auth_provider":true,"model_provider":true,"executor":true,"executor_model_scope":"oauth","executor_input_formats":["gemini"],"executor_output_formats":["gemini"],"management_api":true,"request_interceptor":true}}`), nil
}

func (service *service) dispatch(ctx context.Context, method string, raw []byte) (interface{}, error) {
	switch method {
	case "auth.identifier", "executor.identifier":
		return struct {
			Identifier string `json:"identifier"`
		}{provider}, nil
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
		return json.RawMessage(`{"routes":[{"Method":"GET","Path":"/plugins/gemini-web/accounts"},{"Method":"POST","Path":"/plugins/gemini-web/accounts"},{"Method":"POST","Path":"/plugins/gemini-web/refresh"},{"Method":"POST","Path":"/plugins/gemini-web/maintain"}],"resources":[{"Path":"/index","Menu":"Gemini Web","Description":"Five-account models and measured usage dashboard"}]}`), nil
	case "management.handle":
		return service.management(ctx, raw)
	case "request.intercept_before", "request.intercept_after":
		return interceptRequest(raw), nil
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
	reference, err := parseReference(record.TokenRef, service.settings().Vault)
	if err != nil {
		return record, sessionToken{}, err
	}
	token, err := service.secrets.Resolve(ctx, reference)
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
	record, token, err := service.resolve(ctx, request.StorageJSON, request.AuthID)
	if err != nil {
		return nil, err
	}
	account, err := service.accountModels(ctx, token)
	if err != nil {
		return nil, err
	}
	switch method {
	case "model.for_auth":
		return struct {
			Provider string
			Models   []modelInfo
		}{provider, verifiedModels(account)}, nil
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
