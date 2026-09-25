#include "../bridge.h"
#include <dlfcn.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static int reject_callback(void* context, const char* method, const uint8_t* request, size_t length, cliproxy_buffer* response) {
    (void)context; (void)method; (void)request; (void)length; (void)response;
    return 1;
}

static void free_callback(void* pointer, size_t length) {
    (void)length;
    free(pointer);
}

static int contains_ok(const cliproxy_buffer* response) {
    char* copy = malloc(response->len + 1);
    if (copy == NULL) return 0;
    memcpy(copy, response->ptr, response->len);
    copy[response->len] = '\0';
    int found = strstr(copy, "\"ok\":true") != NULL;
    free(copy);
    return found;
}

int main(int count, char** arguments) {
    if (count != 2) { fputs("usage: abi-smoke plugin.so\n", stderr); return 2; }
    void* library = dlopen(arguments[1], RTLD_NOW | RTLD_LOCAL);
    if (library == NULL) { fputs(dlerror(), stderr); return 1; }
    int (*initialize)(const cliproxy_host_api*, cliproxy_plugin_api*) = dlsym(library, "cliproxy_plugin_init");
    if (initialize == NULL) { fputs("missing ABI entrypoint\n", stderr); return 1; }
    cliproxy_host_api host = {1, NULL, reject_callback, free_callback};
    cliproxy_plugin_api plugin = {0};
    if (initialize(&host, &plugin) != 0 || plugin.abi_version != 1) return 1;

    char* methods[] = {"plugin.register", "management.register", "quota.identifier", "quota.describe"};
    char empty[] = "{}";
    for (size_t index = 0; index < sizeof(methods) / sizeof(methods[0]); index++) {
        cliproxy_buffer response = {0};
        if (plugin.call(methods[index], (uint8_t*)empty, strlen(empty), &response) != 0 || response.ptr == NULL) {
            fprintf(stderr, "call failed: %s\n", methods[index]);
            return 1;
        }
        if (!contains_ok(&response)) {
            fprintf(stderr, "method not ok: %s\n", methods[index]);
            plugin.free_buffer(response.ptr, response.len);
            return 1;
        }
        printf("%s: ok\n", methods[index]);
        plugin.free_buffer(response.ptr, response.len);
    }

    cliproxy_buffer denied = {0};
    char fetch_method[] = "quota.fetch";
    char fetch_body[] = "{\"auth_id\":\"x\",\"host_callback_id\":\"c\"}";
    if (plugin.call(fetch_method, (uint8_t*)fetch_body, strlen(fetch_body), &denied) != 0 || denied.ptr == NULL) {
        fputs("quota.fetch call failed\n", stderr);
        return 1;
    }
    if (contains_ok(&denied)) {
        fputs("quota.fetch succeeded without a host callback\n", stderr);
        plugin.free_buffer(denied.ptr, denied.len);
        return 1;
    }
    printf("quota.fetch: refused without host auth\n");
    plugin.free_buffer(denied.ptr, denied.len);

    plugin.shutdown();
    puts("ABI1 load/call/free/shutdown smoke passed; no secret or network callbacks");
    return 0;
}
