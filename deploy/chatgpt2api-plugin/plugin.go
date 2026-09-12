package main

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

type service struct {
	mu         sync.RWMutex
	config     pluginConfig
	routeCount atomic.Uint64
	client     *http.Client
}

func newService() *service {
	return &service{config: pluginConfig{ModelNames: modelNames{}, DashboardPath: defaultDashboardPath}, client: newHealthClient()}
}

func (plugin *service) settings() pluginConfig {
	plugin.mu.RLock()
	defer plugin.mu.RUnlock()
	return plugin.config
}

func (plugin *service) handle(ctx context.Context, method string, raw []byte) []byte {
	result, err := plugin.dispatch(ctx, method, raw)
	response, errEncode := encode(envelope{OK: err == nil, Result: result, Error: err})
	if errEncode != nil {
		return []byte(`{"ok":false,"error":{"code":"response_encoding_failed","message":"response_encoding_failed","http_status":500}}`)
	}
	return response
}

func (plugin *service) dispatch(ctx context.Context, method string, raw []byte) (json.RawMessage, *publicError) {
	switch method {
	case "plugin.register", "plugin.reconfigure":
		return plugin.register(raw)
	case "model.route":
		var request routeRequest
		if json.Unmarshal(raw, &request) != nil {
			return nil, failure(400, "invalid_route_request")
		}
		return encode(plugin.route(request))
	case "management.register":
		return json.RawMessage(`{"routes":[{"Method":"GET","Path":"/plugins/chatgpt2api/status"}],"resources":[{"Path":"/index","Menu":"ChatGPT2API","Description":"Native-provider routing and safe sidecar health"}]}`), nil
	case "management.handle":
		return plugin.management(ctx, raw)
	default:
		return nil, failure(400, "unknown_method")
	}
}

func (plugin *service) route(request routeRequest) routeResponse {
	if !slices.Contains(plugin.settings().ModelNames, strings.TrimSpace(request.RequestedModel)) {
		return routeResponse{}
	}
	for _, available := range request.AvailableProviders {
		if strings.ToLower(strings.TrimSpace(available)) == nativeProvider {
			plugin.routeCount.Add(1)
			return routeResponse{Handled: true, TargetKind: "provider", Target: nativeProvider, Reason: "chatgpt2api integration"}
		}
	}
	return routeResponse{}
}
