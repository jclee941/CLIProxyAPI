//go:build abismoke

package main

/*
#include "bridge.h"
*/
import "C"

import "unsafe"

func nativeFixtureCall(method string, raw []byte) ([]byte, *publicError) {
	methodPointer := C.CString(method)
	defer C.free(unsafe.Pointer(methodPointer))
	request := C.CBytes(raw)
	defer C.free(request)
	var response C.cliproxy_buffer
	code := C.cliproxyPluginCall(methodPointer, (*C.uint8_t)(request), C.size_t(len(raw)), &response)
	if response.ptr != nil {
		defer C.cliproxyPluginFree(response.ptr, response.len)
	}
	if code != 0 || response.ptr == nil || response.len > 192*1024*1024 {
		return nil, failure(500, "native_fixture_failed")
	}
	return C.GoBytes(response.ptr, C.int(response.len)), nil
}
