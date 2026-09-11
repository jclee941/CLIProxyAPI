#include "../bridge.h"
#include <dlfcn.h>
#include <stdio.h>
#include <string.h>

int test_interceptor_pipeline(cliproxy_plugin_api* plugin);

static int reject_callback(void* context, const char* method, const uint8_t* request, size_t length, cliproxy_buffer* response) {
    (void)context; (void)method; (void)request; (void)length; (void)response;
    return 1;
}

static void free_callback(void* pointer, size_t length) {
    (void)length;
    free(pointer);
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
    char* methods[] = {"plugin.register", "management.register", "auth.identifier", "model.static", "executor.count_tokens"};
    char* payloads[] = {"{}", "{}", "{}", "{}", "{\"Model\":\"gemini-web-omni\"}"};
    for (size_t index = 0; index < sizeof(methods)/sizeof(methods[0]); index++) {
        cliproxy_buffer response = {0};
        if (plugin.call(methods[index], (uint8_t*)payloads[index], strlen(payloads[index]), &response) != 0 || response.ptr == NULL) return 1;
        char* text = malloc(response.len + 1);
        if (text == NULL) return 1;
        memcpy(text, response.ptr, response.len);
        text[response.len] = '\0';
        const char* expected = index == 4 ? "\"ok\":false" : "\"ok\":true";
        if (strstr(text, expected) == NULL || strstr(text, "op://") != NULL || strstr(text, "gemini-web:v1:") != NULL) return 1;
        if (index == 0 && strstr(text, "\"request_interceptor\":true") == NULL) return 1;
        printf("%s: %s\n", methods[index], expected);
        free(text);
        plugin.free_buffer(response.ptr, response.len);
    }
    if (test_interceptor_pipeline(&plugin) != 0) return 1;
    plugin.shutdown();
    puts("ABI1 load/call/free/shutdown smoke passed; no secret or network callbacks");
    return 0;
}
