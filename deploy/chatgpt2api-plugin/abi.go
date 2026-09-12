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
	result := pluginService.handle(context.Background(), C.GoString(method), raw)
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
func cliproxyPluginShutdown() { pluginService.client.CloseIdleConnections() }
