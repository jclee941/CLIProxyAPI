#include "bridge.h"
static cliproxy_host_api stored_host;
int store_host_api(const cliproxy_host_api* host) {
    if (host == NULL || host->abi_version != 1 || host->call == NULL || host->free_buffer == NULL) return 1;
    stored_host = *host;
    return 0;
}
int call_host_api(const char* method, const uint8_t* request, size_t length, cliproxy_buffer* response) {
    if (stored_host.call == NULL) return 1;
    return stored_host.call(stored_host.host_ctx, method, request, length, response);
}
void free_host_buffer(void* pointer, size_t length) {
    if (pointer != NULL && stored_host.free_buffer != NULL) stored_host.free_buffer(pointer, length);
}
