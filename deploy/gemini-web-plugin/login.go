package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const loginPath = "/v0/management/plugins/gemini-web/login/"
const loginConfigurationError = "login_requires_session_dir_manager_origin_extension_id_and_session_key"

var extensionIDPattern = regexp.MustCompile(`^[a-p]{32}$`)
var loginStatePattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type loginStatus string

const (
	loginPending     loginStatus = "pending"
	loginProcessing  loginStatus = "processing"
	loginHostPending loginStatus = "host_sync_pending"
	loginSaved       loginStatus = "saved"
	loginReady       loginStatus = "ready"
	loginError       loginStatus = "error"
	loginCancelled   loginStatus = "cancelled"
	loginExpired     loginStatus = "expired"
)

type loginView struct {
	State            string                `json:"state"`
	ExpiresAt        int64                 `json:"expires_at,omitempty"`
	Status           loginStatus           `json:"status"`
	ExtensionID      string                `json:"extension_id,omitempty"`
	ManagerOrigin    string                `json:"manager_origin,omitempty"`
	ExpectedIdentity *credentialInspection `json:"expected_identity,omitempty"`
	AccountID        string                `json:"account_id,omitempty"`
	Error            string                `json:"error,omitempty"`
	ModelsReady      bool                  `json:"models_ready"`
}

type loginFlow struct {
	View      loginView
	Label     string
	Previous  storageRecord
	Reference string
}

type loginCompletion struct {
	State         string  `json:"state"`
	Token         string  `json:"token"`
	AccountSHA256 string  `json:"account_sha256"`
	AuthUser      *uint64 `json:"auth_user"`
	ExtensionID   string  `json:"extension_id"`
	Consent       bool    `json:"consent"`
}

func (service *service) configureSessions(config pluginConfig) error {
	if config.SessionDir == "" && config.ManagerOrigin == "" && config.BrowserExtensionID == "" {
		if service.sessions != nil {
			return failure(409, "session_store_reconfiguration_requires_drained_restart")
		}
		return nil
	}
	key := os.Getenv("GEMINI_WEB_SESSION_KEY")
	if config.SessionDir == "" || config.ManagerOrigin == "" || config.BrowserExtensionID == "" || key == "" {
		return failure(503, loginConfigurationError)
	}
	origin, err := url.Parse(config.ManagerOrigin)
	if err != nil || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" || origin.Path != "" || origin.RawPath != "" || origin.Opaque != "" || origin.String() != config.ManagerOrigin || !extensionIDPattern.MatchString(config.BrowserExtensionID) {
		return failure(400, "invalid_login_configuration")
	}
	loopback := net.ParseIP(origin.Hostname())
	if origin.Scheme != "https" && !(origin.Scheme == "http" && loopback != nil && loopback.IsLoopback()) {
		return failure(400, "manager_origin_requires_https")
	}
	if service.sessions != nil {
		if service.config.SessionDir != config.SessionDir || service.sessionKeyHash != sha256.Sum256([]byte(key)) || service.config.ManagerOrigin != config.ManagerOrigin || service.config.BrowserExtensionID != config.BrowserExtensionID {
			return failure(409, "session_store_reconfiguration_requires_drained_restart")
		}
		return nil
	}
	store, err := openSessionStore(config.SessionDir, key)
	if err != nil {
		return err
	}
	records, err := store.records()
	if err != nil {
		return errors.Join(err, store.close())
	}
	for _, local := range records {
		previous := local.Target
		if local.LegacyRef != "" {
			previous.TokenRef = local.LegacyRef
		}
		lease, err := service.accountLease(previous)
		if err != nil {
			return errors.Join(err, store.close())
		}
		if err := service.aliasAccountLease(local.Target, lease); err != nil {
			return errors.Join(err, store.close())
		}
		lease.mu.Lock()
		if local.LegacyRef != "" {
			lease.retired = map[string]bool{local.LegacyRef: true}
		}
		switch local.State {
		case localHostPending:
			lease.state = credentialState{state: maintenanceHostPending}
		case localRenewing, localSubmitting:
			lease.state = credentialState{state: maintenanceOperator}
		case localReady:
			lease.state = credentialState{state: maintenanceReady}
		}
		lease.mu.Unlock()
	}
	service.loginMu.Lock()
	if service.logins == nil {
		service.logins = make(map[string]loginFlow)
	}
	for _, local := range records {
		if local.LoginState != "" {
			service.logins[local.LoginState] = recoveredLogin(local)
		}
	}
	service.loginMu.Unlock()
	service.sessions, service.sessionKeyHash = store, sha256.Sum256([]byte(key))
	service.startKeepAlive()
	return nil
}

func (service *service) localStore() *sessionStore {
	service.mu.RLock()
	defer service.mu.RUnlock()
	return service.sessions
}

func (service *service) loginOperation(ctx context.Context, request managementRequest) (loginView, error) {
	operation := strings.TrimPrefix(request.Path, loginPath)
	switch operation {
	case "start", "complete", "status", "cancel", "reconcile":
	default:
		return loginView{}, failure(404, "management_route_not_found")
	}
	if service.localStore() == nil {
		return loginView{}, failure(503, loginConfigurationError)
	}
	config := service.settings()
	if len(request.Headers.Values("Origin")) != 1 || request.Headers.Get("Origin") != config.ManagerOrigin {
		return loginView{}, failure(403, "login_origin_mismatch")
	}
	if request.HostCallbackID == "" {
		return loginView{}, failure(401, "authenticated_management_callback_required")
	}
	if len(request.Query) != 0 || len(request.Body) > 40000 {
		return loginView{}, failure(400, "invalid_login_request")
	}
	switch operation {
	case "start":
		return service.startLogin(request)
	case "complete":
		var body loginCompletion
		if strictJSON(request.Body, &body) != nil || !body.Consent || body.AuthUser == nil || !accountDigestPattern.MatchString(body.AccountSHA256) || body.ExtensionID != config.BrowserExtensionID {
			return loginView{}, failure(400, "invalid_login_handoff")
		}
		return service.completeLogin(ctx, request.HostCallbackID, body)
	case "status", "cancel", "reconcile":
		var body struct {
			State string `json:"state"`
		}
		if strictJSON(request.Body, &body) != nil {
			return loginView{}, failure(400, "invalid_login_request")
		}
		flow, err := service.getLogin(body.State)
		if err != nil {
			return loginView{}, err
		}
		if operation == "cancel" {
			service.loginMu.Lock()
			current := service.logins[body.State]
			if current.Reference == "" && (current.View.Status == loginPending || current.View.Status == loginProcessing) {
				current.View.Status = loginCancelled
				service.logins[body.State] = current
			}
			service.loginMu.Unlock()
			return current.View, nil
		}
		if operation == "reconcile" && flow.Reference != "" {
			return service.reconcileLogin(ctx, request.HostCallbackID, flow)
		}
		if operation == "status" && flow.Reference != "" {
			return service.loginStatus(ctx, request.HostCallbackID, flow)
		}
		return flow.View, nil
	default:
		return loginView{}, failure(404, "management_route_not_found")
	}
}

func (service *service) loginStatus(ctx context.Context, callbackID string, flow loginFlow) (loginView, error) {
	view := flow.View
	view.ModelsReady, view.Error = false, ""
	local, err := service.localStore().read(flow.Reference)
	if err != nil {
		return loginView{}, err
	}
	if local.LoginState != flow.View.State {
		view.Status, view.Error = loginError, "login_superseded"
		return view, nil
	}
	switch local.State {
	case localRenewing, localSubmitting:
		view.Status, view.Error = loginError, "needs_operator"
		return view, nil
	case localHostPending:
		view.Status = loginHostPending
		return view, nil
	case localReady:
	default:
		return loginView{}, failure(503, "session_store_corrupt")
	}
	current, enabled, err := service.canonicalHostRecord(callbackID, local.Target.ID)
	if err != nil || !sameHostProjection(current, local.Target) {
		view.Status = loginHostPending
		return view, nil
	}
	view.Status = loginSaved
	models, err := service.localAuthModels(ctx, current)
	if err != nil {
		view.Error = safeCredentialCode(err)
		return view, nil
	}
	// Readiness is that the account publishes something a caller may name, not
	// that it publishes one particular model.
	if enabled && len(models) > 0 {
		view.Status, view.ModelsReady = loginReady, true
	}
	return view, nil
}

func (service *service) startLogin(request managementRequest) (loginView, error) {
	var body struct {
		Label      string `json:"label"`
		ExistingID string `json:"existing_id"`
		Consent    bool   `json:"consent"`
	}
	if strictJSON(request.Body, &body) != nil || !body.Consent || strings.TrimSpace(body.Label) == "" || len(body.Label) > 200 || strings.ContainsAny(body.Label, "\r\n\x00") {
		return loginView{}, failure(400, "invalid_login_request")
	}
	flow := loginFlow{Label: body.Label}
	if body.ExistingID != "" {
		previous, _, err := service.findRecord(request.HostCallbackID, body.ExistingID)
		if err != nil {
			return loginView{}, err
		}
		identity, err := service.loginIdentity(previous)
		if err != nil {
			return loginView{}, err
		}
		flow.Previous, flow.View.ExpectedIdentity = previous, &identity
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return loginView{}, failure(500, "login_state_failed")
	}
	config := service.settings()
	flow.View.State, flow.View.ExpiresAt, flow.View.Status = hex.EncodeToString(random[:]), service.now().Add(10*time.Minute).Unix(), loginPending
	flow.View.ExtensionID, flow.View.ManagerOrigin = config.BrowserExtensionID, config.ManagerOrigin
	service.loginMu.Lock()
	defer service.loginMu.Unlock()
	if service.logins == nil {
		service.logins = make(map[string]loginFlow)
	}
	active := 0
	for state, previous := range service.logins {
		if previous.View.ExpiresAt <= service.now().Unix() && previous.View.Status != loginProcessing && previous.Reference == "" {
			delete(service.logins, state)
			continue
		}
		switch previous.View.Status {
		case loginPending, loginProcessing, loginHostPending:
			active++
		}
	}
	if active >= 32 {
		return loginView{}, failure(429, "login_capacity_reached")
	}
	service.logins[flow.View.State] = flow
	return flow.View, nil
}

func (service *service) loginIdentity(record storageRecord) (credentialInspection, error) {
	if !localReferencePattern.MatchString(record.TokenRef) {
		return credentialInspection{}, failure(400, "invalid_token_reference")
	}
	local, err := service.localStore().read(record.TokenRef)
	if err != nil {
		return credentialInspection{}, err
	}
	if err := service.checkLocalBinding(record, local); err != nil {
		return credentialInspection{}, err
	}
	return local.Identity, nil
}

func (service *service) getLogin(state string) (loginFlow, error) {
	if !loginStatePattern.MatchString(state) {
		return loginFlow{}, failure(400, "invalid_login_state")
	}
	service.loginMu.Lock()
	defer service.loginMu.Unlock()
	flow, exists := service.logins[state]
	if !exists {
		records, err := service.localStore().records()
		if err != nil {
			return loginFlow{}, err
		}
		for _, record := range records {
			if record.LoginState == state {
				flow = recoveredLogin(record)
				exists = true
			}
		}
		if !exists {
			return loginFlow{}, failure(404, "login_state_not_found")
		}
		if service.logins == nil {
			service.logins = make(map[string]loginFlow)
		}
	}
	if flow.View.Status == loginPending && flow.View.ExpiresAt <= service.now().Unix() {
		flow.View.Status = loginExpired
	}
	service.logins[state] = flow
	return flow, nil
}

func recoveredLogin(local localSession) loginFlow {
	view := loginView{State: local.LoginState, AccountID: local.Target.ID, ExpiresAt: local.LoginExpires}
	switch local.State {
	case localHostPending:
		view.Status = loginHostPending
	case localReady:
		view.Status = loginSaved
	case localRenewing, localSubmitting:
		view.Status, view.Error = loginError, "needs_operator"
	default:
		view.Status, view.Error = loginError, "session_store_corrupt"
	}
	return loginFlow{Reference: local.Target.TokenRef, View: view}
}
