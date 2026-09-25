//go:build abismoke

package main

/*
#include "bridge.h"
#include <string.h>

static int codex_callback_count;
static int codex_free_count;
static int codex_fixture_callback(void *context, const char *method, const uint8_t *request, size_t length, cliproxy_buffer *response) {
    (void)context;
    char *input = malloc(length + 1);
    if (!input) return 1;
    memcpy(input, request, length);
    input[length] = 0;
    int scoped = strstr(input, "\"host_callback_id\":\"fresh-scope\"") != NULL;
    int indexed = strstr(input, "\"auth_index\":\"physical-index\"") != NULL;
    free(input);
    if (!scoped) return 1;
    const char *output = NULL;
    if (strcmp(method, "host.auth.list") == 0) {
        output = "{\"ok\":true,\"result\":{\"files\":[{\"id\":\"physical.json\",\"auth_index\":\"physical-index\",\"name\":\"physical.json\",\"provider\":\"codex\",\"type\":\"codex\",\"source\":\"file\",\"disabled\":true}]}}";
    } else if (indexed && strcmp(method, "host.auth.get_runtime") == 0) {
        output = "{\"ok\":true,\"result\":{\"auth\":{\"id\":\"physical.json\",\"auth_index\":\"physical-index\",\"name\":\"physical.json\",\"provider\":\"codex\",\"type\":\"codex\",\"source\":\"file\",\"disabled\":true}}}";
    } else if (indexed && strcmp(method, "host.auth.get") == 0) {
        output = "{\"ok\":true,\"result\":{\"auth_index\":\"physical-index\",\"name\":\"physical.json\",\"json\":{\"type\":\"codex\",\"access_token\":\"synthetic-access-marker\",\"id_token\":\"synthetic-id-marker\",\"refresh_token\":\"synthetic-refresh-marker\",\"disabled\":true}}}";
    } else return 1;
    response->len = strlen(output);
    response->ptr = malloc(response->len);
    if (!response->ptr) return 1;
    memcpy(response->ptr, output, response->len);
    codex_callback_count++;
    return 0;
}
static void codex_fixture_free(void *pointer, size_t length) {
    (void)length;
    codex_free_count++;
    free(pointer);
}
static cliproxy_host_api codex_fixture_host(void) {
    codex_callback_count = 0;
    codex_free_count = 0;
    cliproxy_host_api host = {1, NULL, codex_fixture_callback, codex_fixture_free};
    return host;
}
static int codex_fixture_calls(void) { return codex_callback_count; }
static int codex_fixture_frees(void) { return codex_free_count; }
*/
import "C"

func initializeCodexCallbackFixture() bool {
	host := C.codex_fixture_host()
	var plugin C.cliproxy_plugin_api
	return cliproxy_plugin_init(&host, &plugin) == 0 && plugin.abi_version == 1
}

func codexCallbackFixtureCounts() (int, int) {
	return int(C.codex_fixture_calls()), int(C.codex_fixture_frees())
}
