#ifndef CHATGPT2API_BRIDGE_H
#define CHATGPT2API_BRIDGE_H
#include <stdint.h>
#include <stdlib.h>

typedef struct { void *ptr; size_t len; } cliproxy_buffer;
typedef int (*cliproxy_host_call_fn)(void *, const char *, const uint8_t *, size_t, cliproxy_buffer *);
typedef void (*cliproxy_host_free_fn)(void *, size_t);
typedef struct {
    uint32_t abi_version;
    void *host_ctx;
    cliproxy_host_call_fn call;
    cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;
typedef int (*cliproxy_plugin_call_fn)(char *, uint8_t *, size_t, cliproxy_buffer *);
typedef void (*cliproxy_plugin_free_fn)(void *, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);
typedef struct {
    uint32_t abi_version;
    cliproxy_plugin_call_fn call;
    cliproxy_plugin_free_fn free_buffer;
    cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxyPluginCall(char *, uint8_t *, size_t, cliproxy_buffer *);
extern void cliproxyPluginFree(void *, size_t);
extern void cliproxyPluginShutdown(void);
int valid_host_api(const cliproxy_host_api *);
static inline int invoke_host_call(const cliproxy_host_api *host, const char *method, const uint8_t *request, size_t length, cliproxy_buffer *response) {
    return host->call(host->host_ctx, method, request, length, response);
}
static inline void release_host_buffer(const cliproxy_host_api *host, void *pointer, size_t length) {
    host->free_buffer(pointer, length);
}
#endif
