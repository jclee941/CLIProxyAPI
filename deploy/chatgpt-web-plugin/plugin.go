package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"time"
)

type hostCall func(string, []byte) ([]byte, error)

type service struct {
	host      hostCall
	client    *http.Client
	now       func() time.Time
	dashboard string
}

func newService(host hostCall) *service {
	return &service{host: host, client: newUpstreamClient(), now: time.Now, dashboard: defaultDashboard}
}

func newUpstreamClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext:         (&net.Dialer{}).DialContext,
		ForceAttemptHTTP2:   true,
		MaxIdleConnsPerHost: 4,
	}}
}

func (service *service) stop() {
	service.client.CloseIdleConnections()
}

func (service *service) handle(ctx context.Context, method string, raw []byte) []byte {
	var result interface{}
	var err error
	if method == "plugin.register" || method == "plugin.reconfigure" {
		result, err = service.register()
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
		encoded, encodeErr := json.Marshal(result)
		if encodeErr != nil {
			response.OK = false
			response.Error = failure(500, "response_encoding_failed")
		} else {
			response.Result = encoded
		}
	}
	raw, err = json.Marshal(response)
	if err != nil {
		return []byte(`{"ok":false,"error":{"http_status":500,"code":"response_encoding_failed"}}`)
	}
	return raw
}

func (service *service) register() (interface{}, error) {
	return json.RawMessage(`{"schema_version":6,"metadata":{"Name":"chatgpt-web","Version":"0.1.0","Author":"jclee941","GitHubRepository":"https://github.com/jclee941/CLIProxyAPI","ConfigFields":[]},"capabilities":{"management_api":true,"quota_provider":true,"model_provider":true,"model_router":true,"executor":true,"executor_model_scope":"static","executor_input_formats":["openai-image","openai"],"executor_output_formats":["openai-image","openai"]}}`), nil
}

func (service *service) dispatch(ctx context.Context, method string, raw []byte) (interface{}, error) {
	switch method {
	case "quota.identifier":
		return struct {
			Identifier string `json:"identifier"`
		}{provider}, nil
	case "quota.describe":
		return quotaDescription{SupportedProviders: []string{authProvider}, DisplayName: "ChatGPT Web", SupportsReset: false}, nil
	case "quota.fetch":
		return service.quotaFetch(ctx, raw)
	case "quota.reset":
		return nil, failure(400, "quota_reset_unsupported")
	case "executor.identifier":
		return struct {
			Identifier string `json:"identifier"`
		}{provider}, nil
	case "model.static":
		return modelResponse{Provider: provider, Models: append(webImageModels(), webChatModels()...)}, nil
	case "model.route":
		return routeImages(raw)
	case "executor.execute":
		var routed executorRequest
		if json.Unmarshal(raw, &routed) == nil && claimsWebChatModel(routed.Model) {
			return service.executeChat(ctx, raw)
		}
		return service.executeImages(ctx, raw)
	case "executor.execute_stream":
		var routed executorRequest
		if json.Unmarshal(raw, &routed) == nil && claimsWebChatModel(routed.Model) {
			return service.executeChatStream(ctx, raw)
		}
		return nil, failure(400, "image_streaming_unsupported")
	case "executor.count_tokens":
		return nil, failure(400, "count_tokens_not_supported")
	case "management.register":
		return json.RawMessage(`{"routes":[{"Method":"GET","Path":"/plugins/chatgpt-web/accounts"}],"resources":[{"Path":"/index","Menu":"ChatGPT Web","Description":"ChatGPT web and Codex quota per credential"}]}`), nil
	case "management.handle":
		return service.management(ctx, raw)
	default:
		return nil, failure(400, "unknown_method")
	}
}

// report writes one warning through the host, which is where an operator reads
// and where the request id is attached; a loaded plugin's own output reaches no
// log. The core renders only its known field names. A line that cannot be
// delivered is not worth failing the request it describes.
func (service *service) report(message string, fields map[string]any) {
	if service.host == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{"level": "warn", "message": message, "fields": fields})
	if err != nil {
		return
	}
	_, _ = service.host("host.log", payload)
}
