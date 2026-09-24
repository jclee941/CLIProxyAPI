package pluginhost

import (
	"context"
	"errors"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const (
	methodFrontendHTTPRegister = "frontend_http.register"
	methodFrontendHTTPHandle   = "frontend_http.handle"
)

var (
	errFrontendHTTPRequestTooLarge  = errors.New("plugin frontend HTTP request too large")
	errFrontendHTTPResponseTooLarge = errors.New("plugin frontend HTTP response too large")
	errFrontendHTTPUnavailable      = errors.New("plugin frontend HTTP route unavailable")
)

func isFrontendHTTPMethod(method string) bool {
	return method == methodFrontendHTTPRegister || method == methodFrontendHTTPHandle
}

func (a *rpcPluginAdapter) RegisterFrontendHTTP(ctx context.Context, req pluginapi.FrontendHTTPRegistrationRequest) (pluginapi.FrontendHTTPRegistrationResponse, error) {
	resp, err := callPlugin[pluginapi.FrontendHTTPRegistrationResponse](ctx, a.client, methodFrontendHTTPRegister, req)
	if err != nil {
		return pluginapi.FrontendHTTPRegistrationResponse{}, err
	}
	for i := range resp.Routes {
		resp.Routes[i].Handler = a
		if resp.Routes[i].AuthMode == pluginapi.FrontendHTTPAuthScoped {
			resp.Routes[i].Authenticator = rpcFrontendAuthProvider{rpcPluginAdapter: a}
		}
	}
	return resp, nil
}

func (a *rpcPluginAdapter) HandleFrontendHTTP(ctx context.Context, req pluginapi.FrontendHTTPRequest) (pluginapi.FrontendHTTPResponse, error) {
	callbackID, closeCallback := a.openHostCallbackContext(ctx)
	defer closeCallback()
	return callPlugin[pluginapi.FrontendHTTPResponse](ctx, a.client, methodFrontendHTTPHandle, struct {
		pluginapi.FrontendHTTPRequest
		HostCallbackID string `json:"host_callback_id,omitempty"`
	}{req, callbackID})
}
