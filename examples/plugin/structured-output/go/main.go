// Structured output plugin.
//
// Some providers are bridges onto a chat product rather than the platform API.
// They accept the client's response_format and then answer with prose anyway.
// This plugin restates the contract to the model before the request is sent and
// strips the reply back down to its JSON value afterwards.
//
// Providers with native structured output are unaffected: their reply already is
// a bare JSON value, so the response pass finds nothing to change.
package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	void* call;
	void* free_buffer;
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
*/
import "C"

import (
	"encoding/json"
	"sync"
	"unsafe"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"gopkg.in/yaml.v3"
)

const pluginID = "structured-output"

var state = struct {
	mu     sync.Mutex
	config pluginConfig
}{config: pluginConfig{Instruct: true, Clean: true}}

type pluginConfig struct {
	// Instruct restates the contract to the model before the request is sent.
	Instruct bool `yaml:"instruct"`
	// Clean strips fences and prose from the reply so only the JSON value remains.
	Clean bool `yaml:"clean"`
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
func cliproxy_plugin_init(_ *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
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
	cfg := pluginConfig{Instruct: true, Clean: true}
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
	body := req.Body
	if currentConfig().Instruct {
		if spec := parseResponseFormat(body); spec != nil && gjson.GetBytes(body, "messages").IsArray() {
			body = withSystemInstruction(body, instructionText(spec))
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

// interceptResponse reduces the reply to its JSON value when the caller asked for
// structured output. A reply that already is a bare JSON value is left untouched.
func interceptResponse(raw []byte) ([]byte, error) {
	var req pluginapi.ResponseInterceptRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if !currentConfig().Clean {
		return okEnvelope(pluginapi.ResponseInterceptResponse{})
	}
	if spec := parseResponseFormat(req.OriginalRequest); spec == nil {
		return okEnvelope(pluginapi.ResponseInterceptResponse{})
	}
	content := gjson.GetBytes(req.Body, "choices.0.message.content")
	if !content.Exists() {
		return okEnvelope(pluginapi.ResponseInterceptResponse{})
	}
	cleaned, ok := extractJSON(content.String())
	if !ok || cleaned == content.String() {
		return okEnvelope(pluginapi.ResponseInterceptResponse{})
	}
	updated, err := sjson.SetBytes(req.Body, "choices.0.message.content", cleaned)
	if err != nil {
		return okEnvelope(pluginapi.ResponseInterceptResponse{})
	}
	return okEnvelope(pluginapi.ResponseInterceptResponse{Body: updated})
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

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	buffer := C.CBytes(raw)
	response.ptr = buffer
	response.len = C.size_t(len(raw))
}
