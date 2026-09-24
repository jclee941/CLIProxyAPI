package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/http/httpguts"
)

type frontendHTTPRouteRecord struct {
	owner      capabilityRecord
	route      pluginapi.FrontendHTTPRoute
	segments   []frontendHTTPSegment
	generation uint64
}

// RegisterFrontendHTTPRoutes atomically rebuilds the authenticated frontend route
// table. Reserved keys use "METHOD /path" and may contain Gin parameters/catch-alls.
// This shares the lifecycle lock with ApplyConfig and unload, so a stale refresh
// cannot republish routes after a plugin has stopped or reconfigured.
func (h *Host) RegisterFrontendHTTPRoutes(ctx context.Context, reserved map[string]struct{}) {
	if h == nil || !h.lockApply(ctx) {
		return
	}
	defer h.unlockApply()
	h.mu.Lock()
	generation := h.frontendHTTPGeneration
	h.mu.Unlock()
	var next []frontendHTTPRouteRecord
	var standard []frontendHTTPRouteRecord
	for key := range reserved {
		method, path, _ := strings.Cut(key, " ")
		if segments, ok := parseFrontendHTTPPattern(path, true); ok {
			standard = append(standard, frontendHTTPRouteRecord{route: pluginapi.FrontendHTTPRoute{Method: method}, segments: segments})
		}
	}
	for _, record := range h.activeRecords() {
		if record.plugin.Capabilities.FrontendHTTP == nil || h.isPluginFused(record.id) {
			continue
		}
		resp, err := h.callFrontendHTTPRegistrar(ctx, record)
		if err != nil || len(resp.Routes) > 256 {
			log.Warnf("pluginhost: frontend HTTP registration failed for %s", record.id)
			continue
		}
		for _, route := range resp.Routes {
			route.Method = strings.ToUpper(strings.TrimSpace(route.Method))
			segments, ok := parseFrontendHTTPPattern(route.Path, false)
			if !ok || route.Handler == nil || !frontendHTTPMethodAllowed(route.Method) || !validFrontendHTTPAuthMode(route) {
				log.Warnf("pluginhost: invalid frontend HTTP route from %s", record.id)
				continue
			}
			candidate := frontendHTTPRouteRecord{owner: record, route: route, segments: segments, generation: generation}
			if frontendHTTPRouteConflicts(candidate, standard) || frontendHTTPRouteConflicts(candidate, next) {
				log.Warnf("pluginhost: conflicting frontend HTTP route from %s", record.id)
				continue
			}
			next = append(next, candidate)
		}
	}
	h.mu.Lock()
	h.frontendHTTPRoutes = next
	h.mu.Unlock()
}

func frontendHTTPMethodAllowed(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func validFrontendHTTPAuthMode(route pluginapi.FrontendHTTPRoute) bool {
	switch route.AuthMode {
	case pluginapi.FrontendHTTPAuthCore:
		return true
	case pluginapi.FrontendHTTPAuthScoped:
		return route.Authenticator != nil
	default:
		return false
	}
}

func frontendHTTPRouteConflicts(route frontendHTTPRouteRecord, existing []frontendHTTPRouteRecord) bool {
	for _, other := range existing {
		if route.route.Method == other.route.Method && frontendHTTPPatternsOverlap(route.segments, other.segments) {
			return true
		}
	}
	return false
}

func (h *Host) callFrontendHTTPRegistrar(ctx context.Context, record capabilityRecord) (resp pluginapi.FrontendHTTPRegistrationResponse, err error) {
	defer func() {
		if recover() != nil {
			h.fusePlugin(record.id, "FrontendHTTP.RegisterFrontendHTTP", "plugin panic")
			resp = pluginapi.FrontendHTTPRegistrationResponse{}
			err = fmt.Errorf("plugin frontend HTTP registrar panicked")
		}
	}()
	return record.plugin.Capabilities.FrontendHTTP.RegisterFrontendHTTP(ctx, pluginapi.FrontendHTTPRegistrationRequest{Plugin: record.meta})
}

func (h *Host) frontendHTTPRoute(method, path string) (frontendHTTPRouteRecord, map[string]string, bool) {
	if h == nil {
		return frontendHTTPRouteRecord{}, nil, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, record := range h.frontendHTTPRoutes {
		if record.route.Method != method {
			continue
		}
		if params, ok := matchFrontendHTTPPath(record.segments, path); ok {
			return record, params, true
		}
	}
	return frontendHTTPRouteRecord{}, nil, false
}

// HasFrontendHTTPRoute checks the table without calling plugin code or reading a body.
func (h *Host) HasFrontendHTTPRoute(method, path string) bool {
	record, _, ok := h.frontendHTTPRoute(method, path)
	return ok && h.frontendHTTPRouteCurrent(record)
}

// ServeFrontendHTTP pins a route before selecting core or route-local authentication.
// coreAuth runs only for default routes: on success it returns a trusted, derived
// caller scope; on failure it must write the rejection and return false. A nil
// callback or empty scope rejects default routes. Scoped routes never invoke it.
// Neither route selection nor scoped authentication reads the request body.
func (h *Host) ServeFrontendHTTP(w http.ResponseWriter, r *http.Request, coreAuth func() (string, bool)) bool {
	if h == nil || w == nil || r == nil || r.URL == nil {
		return false
	}
	record, params, ok := h.frontendHTTPRoute(r.Method, r.URL.Path)
	if !ok || !h.frontendHTTPRouteCurrent(record) {
		return false
	}
	var callerScope string
	if record.route.AuthMode == pluginapi.FrontendHTTPAuthScoped {
		decision, err := h.callFrontendHTTPAuthenticator(r.Context(), record, pluginapi.FrontendAuthRequest{
			Method: r.Method, Path: r.URL.Path, Headers: cloneHeader(r.Header), Query: cloneValues(r.URL.Query()),
			RawPath: r.URL.RawPath, RawQuery: r.URL.RawQuery,
		})
		if err != nil {
			status := http.StatusUnauthorized
			if errors.Is(err, errFrontendHTTPUnavailable) {
				status = http.StatusServiceUnavailable
			}
			http.Error(w, "plugin frontend authentication failed", status)
			return true
		}
		if !h.frontendHTTPRouteCurrent(record) {
			http.Error(w, "plugin frontend HTTP route unavailable", http.StatusServiceUnavailable)
			return true
		}
		if !decision.Authenticated || !validFrontendHTTPCallerScope(decision.CallerScope) || decision.Rejection != nil {
			if rejection := decision.Rejection; rejection != nil && rejection.StatusCode >= 400 && validFrontendHTTPResponse(*rejection) {
				writeFrontendHTTPResponse(w, r, *rejection, record.owner.id)
			} else {
				http.Error(w, "authentication required", http.StatusUnauthorized)
			}
			return true
		}
		callerScope = decision.CallerScope
	} else if coreAuth != nil {
		var authenticated bool
		callerScope, authenticated = coreAuth()
		if !authenticated {
			return true
		}
	}
	if callerScope == "" {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return true
	}
	if !h.frontendHTTPRouteCurrent(record) {
		http.Error(w, "plugin frontend HTTP route unavailable", http.StatusServiceUnavailable)
		return true
	}
	if r.ContentLength > pluginapi.FrontendHTTPMaxBodyBytes {
		http.Error(w, "plugin request body too large", http.StatusRequestEntityTooLarge)
		return true
	}
	var body []byte
	if r.Body != nil {
		var err error
		body, err = io.ReadAll(http.MaxBytesReader(w, r.Body, pluginapi.FrontendHTTPMaxBodyBytes))
		if err != nil {
			status := http.StatusBadRequest
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				status = http.StatusRequestEntityTooLarge
			}
			http.Error(w, "failed to read plugin request body", status)
			return true
		}
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	resp, err := h.callFrontendHTTPHandler(r.Context(), record, pluginapi.FrontendHTTPRequest{
		Method: r.Method, Path: r.URL.Path, RawPath: r.URL.RawPath, RawQuery: r.URL.RawQuery,
		RequestURI: r.RequestURI, Scheme: scheme, Host: r.Host, ContentLength: r.ContentLength,
		Headers: cloneHeader(r.Header), Query: cloneValues(r.URL.Query()), Body: body,
		Params: params, CallerScope: callerScope,
	})
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, errFrontendHTTPUnavailable) {
			status = http.StatusServiceUnavailable
		} else if errors.Is(err, errFrontendHTTPRequestTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, "plugin frontend HTTP handler failed", status)
		return true
	}
	if resp.StatusCode == 0 {
		resp.StatusCode = http.StatusOK
	}
	if !validFrontendHTTPResponse(resp) {
		http.Error(w, "invalid plugin frontend HTTP response", http.StatusBadGateway)
		return true
	}
	writeFrontendHTTPResponse(w, r, resp, record.owner.id)
	return true
}

func writeFrontendHTTPResponse(w http.ResponseWriter, r *http.Request, resp pluginapi.FrontendHTTPResponse, pluginID string) {
	for key, values := range resp.Headers {
		w.Header()[http.CanonicalHeaderKey(key)] = append([]string(nil), values...)
	}
	w.WriteHeader(resp.StatusCode)
	if r.Method != http.MethodHead {
		if _, errWrite := w.Write(resp.Body); errWrite != nil {
			log.Warnf("pluginhost: failed to write frontend HTTP response for %s", pluginID)
		}
	}
}

func (h *Host) frontendHTTPRouteCurrent(record frontendHTTPRouteRecord) bool {
	h.mu.Lock()
	current := record.generation == h.frontendHTTPGeneration
	h.mu.Unlock()
	return current && h.recordCurrent(record.owner) && !h.isPluginFused(record.owner.id)
}

func validFrontendHTTPCallerScope(scope string) bool {
	if len(scope) != 64 {
		return false
	}
	for _, ch := range scope {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return true
}

func (h *Host) callFrontendHTTPAuthenticator(ctx context.Context, record frontendHTTPRouteRecord, req pluginapi.FrontendAuthRequest) (resp pluginapi.FrontendAuthResponse, err error) {
	if !h.frontendHTTPRouteCurrent(record) {
		return pluginapi.FrontendAuthResponse{}, errFrontendHTTPUnavailable
	}
	defer func() {
		if recover() != nil {
			h.fusePlugin(record.owner.id, "FrontendHTTP.Authenticate", "plugin panic")
			resp = pluginapi.FrontendAuthResponse{}
			err = fmt.Errorf("plugin frontend HTTP authenticator panicked")
		}
	}()
	return record.route.Authenticator.Authenticate(ctx, req)
}

func (h *Host) callFrontendHTTPHandler(ctx context.Context, record frontendHTTPRouteRecord, req pluginapi.FrontendHTTPRequest) (resp pluginapi.FrontendHTTPResponse, err error) {
	if !h.frontendHTTPRouteCurrent(record) {
		return pluginapi.FrontendHTTPResponse{}, errFrontendHTTPUnavailable
	}
	defer func() {
		if recover() != nil {
			h.fusePlugin(record.owner.id, "FrontendHTTP.HandleFrontendHTTP", "plugin panic")
			resp = pluginapi.FrontendHTTPResponse{}
			err = fmt.Errorf("plugin frontend HTTP handler panicked")
		}
	}()
	return record.route.Handler.HandleFrontendHTTP(ctx, req)
}

func validFrontendHTTPResponse(resp pluginapi.FrontendHTTPResponse) bool {
	if resp.StatusCode < 200 || resp.StatusCode > 599 || len(resp.Body) > pluginapi.FrontendHTTPMaxBodyBytes {
		return false
	}
	for key, values := range resp.Headers {
		if !httpguts.ValidHeaderFieldName(key) {
			return false
		}
		for _, value := range values {
			if !httpguts.ValidHeaderFieldValue(value) {
				return false
			}
		}
	}
	return true
}
