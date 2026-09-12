#include "../../bridge.h"
#include <assert.h>
#include <dlfcn.h>
#include <stdio.h>
#include <string.h>

static int callbacks;

static int reject_host_call(void *context, const char *method, const uint8_t *request,
                            size_t length, cliproxy_buffer *response) {
    (void)context; (void)method; (void)request; (void)length; (void)response;
    callbacks++;
    return 1;
}

static void host_free(void *pointer, size_t length) {
    (void)length;
    free(pointer);
}

int main(int argc, char **argv) {
    if (argc < 4 || argc % 2 != 0) {
        fputs("usage: abi-smoke plugin.so method json [method json ...]\n", stderr);
        return 2;
    }
    void *library = dlopen(argv[1], RTLD_NOW | RTLD_LOCAL);
    if (library == NULL) { fputs(dlerror(), stderr); return 1; }
    int (*initialize)(cliproxy_host_api *, cliproxy_plugin_api *) = dlsym(library, "cliproxy_plugin_init");
    assert(initialize != NULL);
    cliproxy_host_api host = {1, NULL, reject_host_call, host_free};
    cliproxy_plugin_api plugin = {0};
    assert(initialize(NULL, &plugin) != 0);
    assert(initialize(&host, NULL) != 0);
    host.abi_version = 6;
    assert(initialize(&host, &plugin) != 0);
    host.abi_version = 1;
    assert(initialize(&host, &plugin) == 0);
    assert(plugin.abi_version == 1 && plugin.call && plugin.free_buffer && plugin.shutdown);
    cliproxy_buffer response = {0};
    assert(plugin.call(NULL, NULL, 0, &response) != 0);
    assert(plugin.call("model.route", NULL, 1, &response) != 0);
    assert(plugin.call("model.route", NULL, 0, NULL) != 0);
    assert(plugin.call("model.route", (uint8_t *)"{}", 192U*1024U*1024U+1U, &response) != 0);
    plugin.free_buffer(NULL, 0);
    for (int index = 2; index < argc; index += 2) {
        response.ptr = NULL;
        response.len = 0;
        assert(plugin.call(argv[index], (uint8_t *)argv[index+1], strlen(argv[index+1]), &response) == 0);
        assert(response.ptr != NULL && response.len > 0);
        assert(fwrite(response.ptr, 1, response.len, stdout) == response.len);
        assert(fputc('\n', stdout) != EOF);
        plugin.free_buffer(response.ptr, response.len);
    }
    plugin.shutdown();
    plugin.shutdown();
    assert(callbacks == 0);
    return 0;
}
