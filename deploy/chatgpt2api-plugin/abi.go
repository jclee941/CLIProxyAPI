package main

/*
#include "bridge.h"
*/
import "C"

import (
	"context"
	"unsafe"
)

var pluginService = newService()

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil || C.valid_host_api(host) == 0 {
		return 1
	}
	api := *host
	pluginService.bindHost(func(ctx context.Context, method string, raw []byte) ([]byte, error) {
		return callHostABI(ctx, api, method, raw)
	})
	plugin.abi_version = 1
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, length C.size_t, response *C.cliproxy_buffer) C.int {
	if response == nil {
		return 1
	}
	response.ptr = nil
	response.len = 0
	if method == nil || length > 192*1024*1024 || request == nil && length != 0 {
		return 1
	}
	var raw []byte
	if length > 0 {
		raw = C.GoBytes(unsafe.Pointer(request), C.int(length))
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := pluginService.handle(ctx, C.GoString(method), raw)
	response.ptr = C.CBytes(result)
	if response.ptr == nil {
		return 1
	}
	response.len = C.size_t(len(result))
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(pointer unsafe.Pointer, length C.size_t) { C.free(pointer) }

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {
	pluginService.client.CloseIdleConnections()
	pluginService.accountClient.CloseIdleConnections()
}

func callHostABI(ctx context.Context, host C.cliproxy_host_api, method string, raw []byte) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, failure(503, "request_cancelled")
	}
	methodPointer := C.CString(method)
	defer C.free(unsafe.Pointer(methodPointer))
	request := C.CBytes(raw)
	defer C.free(request)
	var response C.cliproxy_buffer
	code := C.invoke_host_call(&host, methodPointer, (*C.uint8_t)(request), C.size_t(len(raw)), &response)
	if response.ptr != nil {
		defer C.release_host_buffer(&host, response.ptr, response.len)
	}
	if code != 0 || response.ptr == nil || response.len > accountBodyLimit {
		return nil, failure(503, "host_auth_unavailable")
	}
	return C.GoBytes(response.ptr, C.int(response.len)), nil
}
