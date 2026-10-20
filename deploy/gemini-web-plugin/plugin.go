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
	"strconv"
	"sync"
	"sync/atomic"
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
	// LegacyNativeGeneration is only decoded so configurations written while
	// the sidecar path existed still load; false is rejected because that path
	// was removed. It is cleared after validation and never read afterwards.
	LegacyNativeGeneration *bool         `yaml:"native_generation"`
	NativeContinuation     bool          `yaml:"native_continuation"`
	VideoWireMode          videoWireMode `yaml:"video_wire_mode"`
	// VideoMode is the capability mode a video turn runs on when its request
	// names none; zero keeps the account's standard mode, and so does an
	// account that does not advertise the configured one.
	VideoMode int `yaml:"video_mode,omitempty"`
	// VideoThinkingLevel is the slot 80 thinking level a video turn is sent with
	// when its request names none, numbered as omniThinkingLevels; zero keeps
	// webVideoThinkingLevel.
	VideoThinkingLevel int    `yaml:"video_thinking_level,omitempty"`
	Tuning             tuning `yaml:",inline"`
	// DeleteChats deletes the web conversation a text, image or music turn opens
	// once it has answered, keeping generated chats out of the account history.
	DeleteChats bool `yaml:"delete_chats,omitempty"`
	// ExtendDeclaration prefixes a task=extend turn that continues its own
	// conversation with the web app's [# Sources <PREVIOUS_VIDEO>@Video1]
	// declaration. Off by default: the turn is sent as the caller wrote it.
	ExtendDeclaration bool `yaml:"extend_declaration,omitempty"`
	// ExtendSourceDeclaration prefixes a task=extend turn whose video is attached
	// - uploaded by the caller, or a stored one carried to another account - with
	// [# Sources <VIDEO_0>@Video1]. Off by default: the turn is sent as the
	// caller wrote it, with the video attached as its source.
	ExtendSourceDeclaration bool     `yaml:"extend_source_declaration,omitempty"`
	SessionExchangeAccounts []string `yaml:"session_exchange_accounts,omitempty"`
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
	mu                 sync.RWMutex
	config             pluginConfig
	tuned              atomic.Pointer[tuning]
	host               hostCall
	client             *http.Client
	now                func() time.Time
	continuationWait   func(context.Context) error
	interactionsMu     sync.Mutex
	interactions       map[string]*interactionOperation
	sessionExchangesMu sync.Mutex
	sessionExchanges   map[sessionExchangeKey]context.CancelFunc
	fileLeases         fileLeases
	leases             credentialLeases
	sessions           *sessionStore
	sessionKeyHash     [32]byte
	loginMu            sync.Mutex
	logins             map[string]loginFlow
	modelPending       map[string]uint64
	lifecycle          sessionLifecycle
	accountsMu         sync.Mutex
	accountsCache      map[string]cachedAccountList
	accountsEpoch      uint64
	accountsRefreshing map[string]*accountsRefresh
	// background starts a listing refresh; nil runs it on its own goroutine.
	background     func(func())
	accountNamesMu sync.RWMutex
	accountNames   map[string]string
	// webOriginOverride redirects the native web calls; it is set only by tests,
	// which cannot reach the real product.
	webOriginOverride string
	// webRotateOverride redirects cookie rotation, which lives on a different
	// Google origin than the rest of the native calls.
	webRotateOverride             string
	webUploadOverride             string
	sessionExchangeOriginOverride string
	// driveOverride redirects the Drive fetch, which lives on a Google API origin
	// none of the other native calls use, and driveTokenOverride does the same for
	// the OAuth exchange, which lives on a third.
	driveOverride      string
	driveTokenOverride string
	driveAccess        driveToken
	startedAt          int64
	quota              quotaCache
	videoDiagMu        sync.Mutex
	videoDiags         map[string]*videoTurnDiag
	// slotReservations keep the next video pick off an account until a room just
	// picked onto it has submitted; see reserveSlot.
	slotReservationsMu sync.Mutex
	slotReservations   map[string]time.Time
}

func newService(host hostCall) *service {
	return &service{config: pluginConfig{DashboardPath: "/CLIProxyAPI/plugins/gemini-web/index.html", VideoWireMode: videoWireLegacy}, startedAt: time.Now().Unix(), host: host, client: newHTTPClient(), now: time.Now}
}

func (service *service) handle(ctx context.Context, method string, raw []byte) []byte {
	var result interface{}
	var err error
	registering := method == "plugin.register" || method == "plugin.reconfigure"
	if method == "plugin.quiesce" {
		// The quiesce is not a running call, or the drain it waits for could
		// never reach zero.
		result, err = struct{}{}, service.quiesce()
	} else {
		if registering {
			service.resumeQuiesced()
		}
		if err = service.lifecycle.enter(); err == nil {
			defer service.lifecycle.leave()
			if registering {
				service.mu.Lock()
				result, err = service.register(raw)
				service.mu.Unlock()
			} else {
				result, err = service.dispatch(ctx, method, raw)
			}
		} else if answer, declined := closingRoutingAnswer(method); declined {
			result, err = answer, nil
		} else if service.lifecycle.isQuiesced() && isSessionExchange(method, raw) {
			// Nothing was sent upstream, so the exchange is refused as busy,
			// like one that meets a held credential lease: Flow asks again
			// and reaches the replacement once the host has loaded it.
			busy := sessionExchangeError(409, "busy")
			result, err = managementJSON(busy.HTTPStatus, struct {
				Error string `json:"error"`
			}{busy.Code})
		}
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

// closingRoutingAnswer is what a closing instance answers the routing hooks the
// host runs for every request, whatever its provider: a refusal there failed
// other providers' requests while a hot reload drained this instance. The pick
// defers to the host's own scheduler, and the interceptor still strips the
// internal routing headers a caller must not supply.
func isSessionExchange(method string, raw []byte) bool {
	if method != "management.handle" {
		return false
	}
	var request managementRequest
	return json.Unmarshal(raw, &request) == nil && request.Method == "POST" && request.Path == sessionExchangePath
}

func closingRoutingAnswer(method string) (interface{}, bool) {
	switch method {
	case "scheduler.pick":
		return continuationPick{}, true
	case "request.intercept_before", "request.intercept_after":
		return requestInterceptResponse{ClearHeaders: []string{continuationHeader, interactionRetrieveHeader, roomVideosHeader}}, true
	}
	return nil, false
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
	if config.LegacyNativeGeneration != nil && !*config.LegacyNativeGeneration {
		return nil, &publicError{Code: "invalid_plugin_config", Message: "invalid_plugin_config: native_generation: false is no longer supported; the sidecar path was removed", HTTPStatus: 400}
	}
	config.LegacyNativeGeneration = nil
	if !filepath.IsAbs(config.DashboardPath) || filepath.Ext(config.DashboardPath) != ".html" {
		return nil, failure(400, "invalid_plugin_config")
	}
	switch config.VideoWireMode {
	case "":
		config.VideoWireMode = videoWireLegacy
	case videoWireLegacy, videoWireWeb, videoWireAlternate:
	default:
		return nil, failure(400, "invalid_plugin_config")
	}
	if config.VideoMode < 0 || config.VideoThinkingLevel < 0 || config.VideoThinkingLevel > 4 || !config.Tuning.valid() {
		return nil, failure(400, "invalid_plugin_config")
	}
	if len(config.SessionExchangeAccounts) == 0 {
		config.SessionExchangeAccounts = nil
	}
	current := service.config
	current.HostEnabled, current.HostPriority = config.HostEnabled, config.HostPriority
	wireChanged := current.VideoWireMode != config.VideoWireMode
	current.VideoWireMode = config.VideoWireMode
	modeChanged := current.VideoMode != config.VideoMode
	current.VideoMode = config.VideoMode
	current.VideoThinkingLevel = config.VideoThinkingLevel
	tuningChanged := current.Tuning != config.Tuning
	current.Tuning = config.Tuning
	deleteChanged := current.DeleteChats != config.DeleteChats
	current.DeleteChats = config.DeleteChats
	current.ExtendDeclaration = config.ExtendDeclaration
	current.ExtendSourceDeclaration = config.ExtendSourceDeclaration
	current.SessionExchangeAccounts = config.SessionExchangeAccounts
	// Drive credentials are read where they are used, and the cached access
	// token is their only state, so rotating one never waits for a drain: a
	// reconfigure refused while any call is in flight takes the plugin off the
	// host.
	driveChanged := current.DriveAPIKey != config.DriveAPIKey || current.DriveClientID != config.DriveClientID || current.DriveClientSecret != config.DriveClientSecret || current.DriveRefreshToken != config.DriveRefreshToken
	current.DriveAPIKey, current.DriveClientID = config.DriveAPIKey, config.DriveClientID
	current.DriveClientSecret, current.DriveRefreshToken = config.DriveClientSecret, config.DriveRefreshToken
	unchanged := reflect.DeepEqual(current, config) && (config.SessionDir == "" || service.sessions != nil && service.sessionKeyHash == sha256.Sum256([]byte(os.Getenv("GEMINI_WEB_SESSION_KEY"))))
	if unchanged {
		// These policies are read at submission; they do not replace session
		// state or alter a request whose wire variant or capability has already
		// been selected.
		service.config.VideoWireMode = config.VideoWireMode
		service.config.VideoMode = config.VideoMode
		service.config.VideoThinkingLevel = config.VideoThinkingLevel
		service.config.Tuning = config.Tuning
		service.config.DeleteChats = config.DeleteChats
		service.config.ExtendDeclaration = config.ExtendDeclaration
		service.config.ExtendSourceDeclaration = config.ExtendSourceDeclaration
		service.config.SessionExchangeAccounts = config.SessionExchangeAccounts
		service.config.DriveAPIKey, service.config.DriveClientID = config.DriveAPIKey, config.DriveClientID
		service.config.DriveClientSecret, service.config.DriveRefreshToken = config.DriveClientSecret, config.DriveRefreshToken
	} else {
		if err := service.lifecycle.reconfigure(func() error {
			if err := service.reconfigureCredentials(config); err != nil {
				return err
			}
			if err := service.configureSessions(config); err != nil {
				return err
			}
			service.config = config
			return nil
		}); err != nil {
			return nil, err
		}
	}
	if driveChanged {
		service.driveAccess.mu.Lock()
		service.driveAccess.value, service.driveAccess.scopes = "", ""
		service.driveAccess.expires = time.Time{}
		service.driveAccess.mu.Unlock()
	}
	tuned := service.config.Tuning
	service.tuned.Store(&tuned)
	if tuningChanged {
		service.report(map[string]any{
			"provider": provider,
			"state":    "tuning",
			"reason":   "keys changed",
		}, "gemini-web: tuning applied")
	}
	if wireChanged {
		service.report(map[string]any{
			"provider": provider,
			"state":    "video_wire_mode",
			"reason":   "mode=" + string(config.VideoWireMode),
		}, "gemini-web: video wire mode applied")
	}
	if modeChanged {
		service.report(map[string]any{
			"provider": provider,
			"state":    "video_mode",
			"reason":   "mode=" + strconv.Itoa(config.VideoMode),
		}, "gemini-web: default video mode applied")
	}
	if deleteChanged {
		service.report(map[string]any{
			"provider": provider,
			"state":    "delete_chats",
			"reason":   "enabled=" + strconv.FormatBool(config.DeleteChats),
		}, "gemini-web: generated chat deletion applied")
	}
	registration := json.RawMessage(`{"schema_version":6,"metadata":{"Name":"gemini-web","Version":"0.1.0","Author":"jclee941","GitHubRepository":"https://github.com/jclee941/CLIProxyAPI","Logo":"","ConfigFields":[{"Name":"dashboard_path","Type":"string","Description":"Absolute path to the separately built static dashboard HTML"},{"Name":"session_dir","Type":"string","Description":"Dedicated 0700 encrypted application-session directory; single process owner"},{"Name":"manager_origin","Type":"string","Description":"Exact HTTPS management portal origin"},{"Name":"browser_extension_id","Type":"string","Description":"Registered 32-character browser companion extension ID"}]},"capabilities":{"auth_provider":true,"model_provider":true,"executor":true,"executor_model_scope":"oauth","executor_input_formats":["gemini","openai-image"],"executor_output_formats":["gemini","openai-image"],"management_api":true,"frontend_http":true,"request_interceptor":true,"quota_provider":true}}`)
	registration = bytes.Replace(registration, []byte(`"ConfigFields":[`), []byte(`"ConfigFields":[{"Name":"video_wire_mode","Type":"enum","EnumValues":["legacy","web","alternate"],"Description":"Omni account-tier wire profile; legacy by default, alternate stays fixed per conversation"},{"Name":"video_mode","Type":"integer","Description":"Default video capability mode when a request names none; 0 keeps each account's standard mode, which is also used where the mode is not advertised"},{"Name":"delete_chats","Type":"boolean","Description":"Delete the web conversation a text, image or music turn opens once it has answered"},{"Name":"extend_declaration","Type":"boolean","Description":"Prefix a task=extend turn that continues its own conversation with the web app's [# Sources <PREVIOUS_VIDEO>@Video1] declaration; off by default"},{"Name":"extend_source_declaration","Type":"boolean","Description":"Prefix a task=extend turn whose video is attached (uploaded by the caller, or carried to another account) with [# Sources <VIDEO_0>@Video1]; off by default"},{"Name":"video_thinking_level","Type":"integer","Description":"Default slot 80 thinking level of a video turn whose request names none (1-4); 0 keeps STANDARD"},{"Name":"route_room_ceiling_percent","Type":"integer","Description":"5-hour usage percent at which a new room skips an account (1-100); 0 keeps 80"},{"Name":"video_limit_hold_seconds","Type":"integer","Description":"Seconds an account reported out of video sits out; 0 keeps 1800"},{"Name":"room_turns","Type":"integer","Description":"Videos a room makes on the account that opens it, and the largest room_videos; 0 keeps 3"},{"Name":"default_generation_units","Type":"integer","Description":"Units a generation is assumed to cost before samples exist; 0 keeps 5000"},{"Name":"generation_samples","Type":"integer","Description":"Observed generation costs kept; 0 keeps 8"},{"Name":"same_window_slack_seconds","Type":"integer","Description":"Seconds within which two usage readings count as one window; 0 keeps 600"},{"Name":"accounts_cache_seconds","Type":"integer","Description":"Seconds an account listing is served from cache; 0 keeps 60"},{"Name":"credential_wait_seconds","Type":"integer","Description":"Seconds a call waits for its account slot; 0 keeps 90"},{"Name":"quiesce_drain_seconds","Type":"integer","Description":"Seconds a hot reload waits for running calls; 0 keeps 90"},{"Name":"trapped_release_seconds","Type":"integer","Description":"Interval of the trapped-session release loop, applied when it next starts; 0 keeps 300"},{"Name":"turn_rotation_seconds","Type":"integer","Description":"Seconds after a cookie rotation during which a turn skips renewal; 0 keeps 60"},{"Name":"slot_reservation_seconds","Type":"integer","Description":"Seconds a picked account is reserved for its call; 0 keeps 60"},{"Name":"continuation_request_bytes","Type":"integer","Description":"Largest continuation request in bytes; 0 keeps 5 MiB"},{"Name":"continuation_retention","Type":"integer","Description":"Turns a continuation keeps; 0 keeps 16"},{"Name":"interaction_input_bytes","Type":"integer","Description":"Largest interaction or research request in bytes; 0 keeps 4 MiB"},{"Name":"video_diag_stash_limit","Type":"integer","Description":"Video turn diagnostics kept; 0 keeps 256"},{"Name":"text_thinking_slot","Type":"integer","Description":"Slot 17 of a text turn (0-4); 0 is what the web app sends"},{"Name":"maintenance_auth_cooldown_seconds","Type":"integer","Description":"Seconds an account waits after an authentication failure before maintenance retries; 0 keeps 1800"},{"Name":"maintenance_ready_interval_seconds","Type":"integer","Description":"Seconds between maintenance checks of a ready account; 0 keeps 1800"},{"Name":"credential_fence_seconds","Type":"integer","Description":"Seconds a credential renewal or inspection may take and an uncertain one fences the account; 0 keeps 70"},{"Name":"omni_request_bytes","Type":"integer","Description":"Largest native Omni request in bytes, at most 5 MiB; 0 keeps 5 MiB"},{"Name":"research_limit_seconds","Type":"integer","Description":"Seconds a Deep Research report is awaited; 0 keeps 5400"},{"Name":"upload_limit_bytes","Type":"integer","Description":"Largest new upload in bytes, at most 100 MiB; 0 keeps 100 MiB"},{"Name":"video_budget_seconds","Type":"integer","Description":"Seconds a video turn is awaited; 0 keeps 600"},{"Name":"video_poll_seconds","Type":"integer","Description":"Seconds between video status polls; 0 keeps 10"},{"Name":"keep_alive_interval_seconds","Type":"integer","Description":"Seconds between keep-alive sweeps when GEMINI_WEB_KEEPALIVE=on, from the next sweep; 0 keeps 480"},{"Name":"keep_alive_workers","Type":"integer","Description":"Sessions one keep-alive sweep rotates at once; 0 keeps 4"},{"Name":"keep_alive_age_seconds","Type":"integer","Description":"Cookie age below which a keep-alive sweep leaves a session alone; 0 keeps 1200"},`), 1)
	if config.NativeContinuation {
		registration = bytes.Replace(registration, []byte(`"request_interceptor":true`), []byte(`"request_interceptor":true,"scheduler":true`), 1)
		registration = bytes.ReplaceAll(registration, []byte(`["gemini","openai-image"]`), []byte(`["gemini","openai-image","interactions"]`))
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
		return json.RawMessage(`{"routes":[{"Method":"POST","Path":"/plugins/gemini-web/session/exchange"},{"Method":"POST","Path":"/plugins/gemini-web/session/exchange/cancel"},{"Method":"GET","Path":"/plugins/gemini-web/accounts"},{"Method":"POST","Path":"/plugins/gemini-web/refresh"},{"Method":"POST","Path":"/plugins/gemini-web/maintain"},{"Method":"POST","Path":"/plugins/gemini-web/resolve"},{"Method":"POST","Path":"/plugins/gemini-web/recover"},{"Method":"POST","Path":"/plugins/gemini-web/label"},{"Method":"POST","Path":"/plugins/gemini-web/detach"},{"Method":"POST","Path":"/plugins/gemini-web/login/start"},{"Method":"POST","Path":"/plugins/gemini-web/login/complete"},{"Method":"POST","Path":"/plugins/gemini-web/login/status"},{"Method":"POST","Path":"/plugins/gemini-web/login/cancel"},{"Method":"POST","Path":"/plugins/gemini-web/login/reconcile"}],"resources":[{"Path":"/extension"},{"Path":"/openapi.json"},{"Path":"/index","Menu":"Gemini Web","Description":"Account models and measured usage dashboard"}]}`), nil
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
		return nil, failure(400, "executor_http_route_denied")
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
