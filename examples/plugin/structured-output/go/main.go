// Structured output plugin.
//
// Some providers are bridges onto a chat product rather than the platform API.
// They accept the client's response_format and then answer with prose anyway.
// This plugin restates the contract to the model before the request is sent,
// reduces the reply to its JSON value, and checks it against the requested
// schema. A reply that still breaks the contract is regenerated through the host
// with the specific violations until it conforms or the budget is spent.
//
// Providers with native structured output are unaffected: their first reply
// already validates, so nothing is rewritten and nothing is re-executed.
package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	cliproxy_host_call_fn call;
	cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
	uint32_t abi_version;
	cliproxy_plugin_call_fn call;
	cliproxy_plugin_free_fn free_buffer;
	cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);

static const cliproxy_host_api* stored_host;

static void store_host_api(const cliproxy_host_api* host) {
	stored_host = host;
}

static int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	if (stored_host == NULL || stored_host->call == NULL) {
		return 1;
	}
	return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}

static void free_host_buffer(void* ptr, size_t len) {
	if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) {
		stored_host->free_buffer(ptr, len);
	}
}
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"gopkg.in/yaml.v3"
)

const pluginID = "structured-output"

var state = struct {
	mu     sync.Mutex
	config pluginConfig
}{config: defaultConfig()}

type pluginConfig struct {
	// Instruct restates the contract to the model before the request is sent.
	Instruct bool `yaml:"instruct"`
	// Clean strips fences and prose from the reply so only the JSON value remains.
	Clean bool `yaml:"clean"`
	// StripAgentTags removes dangling <Image .../> cards whose src is an internal
	// agent placeholder rather than a resolvable image, which some web bridges emit.
	// Off by default because it rewrites ordinary replies, not just structured ones.
	StripAgentTags bool `yaml:"strip_agent_tags"`
	// Validate checks the reply against the requested schema rather than trusting
	// an upstream that already ignored the contract once.
	Validate bool `yaml:"validate"`
	// MaxAttempts bounds regeneration after a violating reply. Zero delivers the
	// cleaned reply unchanged.
	MaxAttempts int `yaml:"max_attempts"`
	// BufferStreaming answers a streaming strict request from one complete reply,
	// because a contract cannot be judged from a partial stream.
	BufferStreaming bool `yaml:"buffer_streaming"`
	// InstructTools selects which models are told about a demanded function call
	// up front, which saves a round trip on a bridge that cannot call functions.
	// Leave it unset for providers that call functions natively, because the
	// instruction talks them out of a real call. Optional tool use is never
	// instructed either way.
	InstructTools toolInstructionScope `yaml:"instruct_tools"`
}

// toolInstructionScope accepts either the original boolean or a list of model
// substrings, so a deployment that already set instruct_tools: false keeps
// parsing while a mixed core can name only the bridges that need the help.
type toolInstructionScope struct {
	All      bool
	Patterns []string
}

func (scope *toolInstructionScope) UnmarshalYAML(value *yaml.Node) error {
	var all bool
	if value.Decode(&all) == nil {
		scope.All, scope.Patterns = all, nil
		return nil
	}
	var patterns []string
	if err := value.Decode(&patterns); err != nil {
		return err
	}
	scope.All, scope.Patterns = false, patterns
	return nil
}

// covers reports whether a model should be told the tool contract up front.
func (scope toolInstructionScope) covers(model string) bool {
	if scope.All {
		return true
	}
	base := strings.ToLower(strings.TrimSpace(model))
	if base == "" {
		return false
	}
	for _, pattern := range scope.Patterns {
		trimmed := strings.ToLower(strings.TrimSpace(pattern))
		if trimmed != "" && strings.Contains(base, trimmed) {
			return true
		}
	}
	return false
}

// defaultConfig keeps the defaults in one place so a reconfigure cannot drift
// from the values the plugin starts with.
func defaultConfig() pluginConfig {
	return pluginConfig{Instruct: true, Clean: true, Validate: true, MaxAttempts: defaultMaxAttempts, BufferStreaming: true}
}

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type lifecycleRequest struct {
	ConfigYAML    []byte `json:"config_yaml"`
	SchemaVersion uint32 `json:"schema_version"`
}

type registration struct {
	SchemaVersion uint32                 `json:"schema_version"`
	Metadata      pluginapi.Metadata     `json:"metadata"`
	Capabilities  registrationCapability `json:"capabilities"`
}

type registrationCapability struct {
	RequestInterceptor  bool `json:"request_interceptor"`
	ResponseInterceptor bool `json:"response_interceptor"`
}

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	C.store_host_api(host)
	plugin.abi_version = C.uint32_t(pluginabi.ABIVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, errorEnvelope("invalid_method", "method is required"))
		return 1
	}
	var requestBytes []byte
	if request != nil && requestLen > 0 {
		requestBytes = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	raw, err := handleMethod(C.GoString(method), requestBytes)
	if err != nil {
		writeResponse(response, errorEnvelope("plugin_error", err.Error()))
		return 1
	}
	writeResponse(response, raw)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, length C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
	_ = length
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {}

func handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		if err := configure(request); err != nil {
			return nil, err
		}
		return okEnvelope(pluginRegistration())
	case pluginabi.MethodRequestInterceptBefore:
		return interceptRequest(request)
	case pluginabi.MethodRequestInterceptAfter:
		return passThroughRequest(request)
	case pluginabi.MethodResponseInterceptAfter:
		return interceptResponse(request)
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

func configure(raw []byte) error {
	var req lifecycleRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return err
		}
	}
	cfg := defaultConfig()
	if len(req.ConfigYAML) > 0 {
		if err := yaml.Unmarshal(req.ConfigYAML, &cfg); err != nil {
			return err
		}
	}
	state.mu.Lock()
	state.config = cfg
	state.mu.Unlock()
	return nil
}

func currentConfig() pluginConfig {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.config
}

func pluginRegistration() registration {
	return registration{
		SchemaVersion: 1,
		Metadata: pluginapi.Metadata{
			Name:             pluginID,
			Version:          "0.1.0",
			Author:           "router-for-me",
			GitHubRepository: "https://github.com/router-for-me/CLIProxyAPI",
		},
		Capabilities: registrationCapability{RequestInterceptor: true, ResponseInterceptor: true},
	}
}

// interceptRequest restates the output contract as a system message so the model
// is told what response_format asked for, since the upstream will not enforce it.
func interceptRequest(raw []byte) ([]byte, error) {
	var req pluginapi.RequestInterceptRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	cfg := currentConfig()
	body := req.Body
	if cfg.Instruct {
		body = withContract(body, cfg, req.Model)
	}
	if req.Stream && cfg.Validate && cfg.BufferStreaming && requestCarriesContract(body) {
		if terminated, ok := bufferStrictStream(req, cfg, body); ok {
			return okEnvelope(terminated)
		}
	}
	return okEnvelope(pluginapi.RequestInterceptResponse{Headers: req.Headers, Body: body})
}

func passThroughRequest(raw []byte) ([]byte, error) {
	var req pluginapi.RequestInterceptRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	return okEnvelope(pluginapi.RequestInterceptResponse{Headers: req.Headers, Body: req.Body})
}

// interceptResponse holds the output contract on the caller's behalf: the reply
// is cleaned, checked against the schema, and regenerated when it does not
// conform. A reply that needs none of that is left untouched.
func interceptResponse(raw []byte) ([]byte, error) {
	var req pluginapi.ResponseInterceptRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	body, changed := enforceResponse(req, currentConfig())
	if !changed {
		return okEnvelope(pluginapi.ResponseInterceptResponse{})
	}
	return okEnvelope(pluginapi.ResponseInterceptResponse{Body: body})
}

func okEnvelope(v any) ([]byte, error) {
	encoded, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope{OK: true, Result: encoded})
}

func errorEnvelope(code, message string) []byte {
	encoded, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message}})
	return encoded
}

// callHost invokes a host callback and returns the result payload it carried.
func callHost(method string, payload any) (json.RawMessage, error) {
	rawPayload, errMarshal := json.Marshal(payload)
	if errMarshal != nil {
		return nil, fmt.Errorf("marshal host callback payload %s: %w", method, errMarshal)
	}
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))
	cPayload := C.CBytes(rawPayload)
	defer C.free(cPayload)

	var response C.cliproxy_buffer
	code := C.call_host_api(cMethod, (*C.uint8_t)(cPayload), C.size_t(len(rawPayload)), &response)
	var rawResponse []byte
	if response.ptr != nil {
		if response.len > 0 {
			rawResponse = C.GoBytes(response.ptr, C.int(response.len))
		}
		C.free_host_buffer(response.ptr, response.len)
	}
	if len(rawResponse) == 0 {
		return nil, fmt.Errorf("host callback %s returned no response, code=%d", method, int(code))
	}
	var env envelope
	if errUnmarshal := json.Unmarshal(rawResponse, &env); errUnmarshal != nil {
		return nil, fmt.Errorf("decode host callback envelope %s: %w", method, errUnmarshal)
	}
	if !env.OK {
		if env.Error != nil {
			return nil, fmt.Errorf("%s: %s", env.Error.Code, env.Error.Message)
		}
		return nil, fmt.Errorf("host callback %s failed", method)
	}
	return env.Result, nil
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	buffer := C.CBytes(raw)
	response.ptr = buffer
	response.len = C.size_t(len(raw))
}
