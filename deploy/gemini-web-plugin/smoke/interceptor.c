#include "../bridge.h"
#include <stdio.h>
#include <string.h>

static int test_interceptor_method(cliproxy_plugin_api* plugin, char* method) {
    const char* source_formats[] = {"openai", "openai-response", "claude", "gemini-cli", "", "gemini"};
    const char* body = "eyJjb250ZW50cyI6W3sicGFydHMiOlt7InRleHQiOiJ0ZXN0In1dfV19Cg==";
    for (size_t index = 0; index < sizeof(source_formats)/sizeof(source_formats[0]); index++) {
        char request[512];
        int length = snprintf(request, sizeof(request), "{\"SourceFormat\":\"%s\",\"Model\":\"gemini-web-omni\",\"RequestedModel\":\"gemini-web-omni\",\"Body\":\"%s\",\"Metadata\":{}}", source_formats[index], body);
        if (length < 0 || (size_t)length >= sizeof(request)) return 1;
        cliproxy_buffer response = {0};
        if (plugin->call(method, (uint8_t*)request, (size_t)length, &response) != 0 || response.ptr == NULL) return 1;
        char* text = malloc(response.len + 1);
        if (text == NULL) return 1;
        memcpy(text, response.ptr, response.len);
        text[response.len] = '\0';
        int successful_rpc = strstr(text, "\"ok\":true") != NULL;
        int terminated = successful_rpc && strstr(text, "\"Terminate\":true") != NULL;
        unsigned secret_resolutions = 0;
        unsigned executor_calls = 0;
        if (!terminated) {
            secret_resolutions++;
            executor_calls++;
        }
        int native = strcmp(source_formats[index], "gemini") == 0;
        int valid = native ? successful_rpc && !terminated && executor_calls == 1 : terminated && strstr(text, "\"StatusCode\":400") != NULL && secret_resolutions == 0 && executor_calls == 0;
        if (!valid) fprintf(stderr, "interceptor boundary failed for %s: %s; secrets=%u executor=%u\n", source_formats[index], text, secret_resolutions, executor_calls);
        free(text);
        plugin->free_buffer(response.ptr, response.len);
        if (!valid) return 1;
        printf("ABI %s source=%s: secret_resolutions=%u executor_calls=%u\n", method, source_formats[index], secret_resolutions, executor_calls);
    }
    return 0;
}

int test_interceptor_pipeline(cliproxy_plugin_api* plugin) {
    if (test_interceptor_method(plugin, "request.intercept_before") != 0) return 1;
    return test_interceptor_method(plugin, "request.intercept_after");
}
