#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <pthread.h>

typedef struct { void *ptr; size_t len; } buffer;
typedef struct {
    uint32_t abi_version;
    void *host_ctx;
    int (*call)(void *, const char *, const uint8_t *, size_t, buffer *);
    void (*free_buffer)(void *, size_t);
} host_api;
typedef struct {
    uint32_t abi_version;
    int (*call)(const char *, const uint8_t *, size_t, buffer *);
    void (*free_buffer)(void *, size_t);
    void (*shutdown)(void);
} plugin_api;

static pthread_mutex_t lock = PTHREAD_MUTEX_INITIALIZER;
static unsigned calls;
static char owner[65];
static int reconfigured;

static int result(buffer *out, const char *text) {
    out->len = strlen(text);
    out->ptr = malloc(out->len);
    if (!out->ptr) return 1;
    memcpy(out->ptr, text, out->len);
    return 0;
}

static char *base64(const uint8_t *data, size_t n) {
    static const char alphabet[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    char *out = malloc(4 * ((n + 2) / 3) + 1);
    if (!out) return NULL;
    size_t j = 0;
    for (size_t i = 0; i < n; i += 3) {
        unsigned v = (unsigned)data[i] << 16;
        if (i + 1 < n) v |= (unsigned)data[i + 1] << 8;
        if (i + 2 < n) v |= data[i + 2];
        out[j++] = alphabet[(v >> 18) & 63];
        out[j++] = alphabet[(v >> 12) & 63];
        out[j++] = i + 1 < n ? alphabet[(v >> 6) & 63] : '=';
        out[j++] = i + 2 < n ? alphabet[v & 63] : '=';
    }
    out[j] = 0;
    return out;
}

static int call(const char *method, const uint8_t *raw, size_t n, buffer *out) {
    if (!strcmp(method, "plugin.register") || !strcmp(method, "plugin.reconfigure")) {
        reconfigured = !strcmp(method, "plugin.reconfigure");
        return result(out, "{\"ok\":true,\"result\":{\"schema_version\":6,\"metadata\":{\"Name\":\"frontend-fixture\",\"Version\":\"1.0.0\",\"Author\":\"test\",\"GitHubRepository\":\"https://example.invalid/plugin\"},\"capabilities\":{\"frontend_http\":true}}}");
    }
    if (!strcmp(method, "frontend_http.register")) {
        if (reconfigured) return result(out, "{\"ok\":true,\"result\":{\"Routes\":[{\"Method\":\"GET\",\"Path\":\"/native/reconfigured\"}]}}");
        return result(out, "{\"ok\":true,\"result\":{\"Routes\":["
            "{\"Method\":\"POST\",\"Path\":\"/upload/v1beta/files\"},"
            "{\"Method\":\"POST\",\"Path\":\"/upload/v1beta/files/{session}\"},"
            "{\"Method\":\"GET\",\"Path\":\"/v1beta/files/{id}\"},"
            "{\"Method\":\"GET\",\"Path\":\"/v1beta/files/{id}:download\"},"
            "{\"Method\":\"GET\",\"Path\":\"/v1beta/files\"},"
            "{\"Method\":\"DELETE\",\"Path\":\"/v1beta/files/{id}\"},"
            "{\"Method\":\"GET\",\"Path\":\"/healthz\"},"
            "{\"Method\":\"GET\",\"Path\":\"/v1beta/models/{id}\"},"
            "{\"Method\":\"GET\",\"Path\":\"/v1beta/interactions/{id}\"},"
            "{\"Method\":\"GET\",\"Path\":\"/v0/management/native\"},"
            "{\"Method\":\"GET\",\"Path\":\"/{id}\"},"
            "{\"Method\":\"GET\",\"Path\":\"/v1beta/../unsafe\"}]}}");
    }
    if (strcmp(method, "frontend_http.handle")) return 1;
    char *req = malloc(n + 1);
    if (!req) return 1;
    memcpy(req, raw, n);
    req[n] = 0;
    if (strstr(req, "bad_native_size=1")) {
        free(req);
        out->ptr = malloc(1);
        out->len = 192 * 1024 * 1024 + 1;
        return out->ptr ? 0 : 1;
    }
    const char *scope = strstr(req, "\"caller_scope\":\"");
    if (!scope) { free(req); return 1; }
    scope += strlen("\"caller_scope\":\"");
    if (strlen(scope) < 65 || scope[64] != '"') { free(req); return 1; }

    pthread_mutex_lock(&lock);
    unsigned count = ++calls;
    int post = strstr(req, "\"Method\":\"POST\"") != NULL;
    if (!owner[0] && post) memcpy(owner, scope, 64);
    int allowed = !owner[0] || !strncmp(owner, scope, 64);
    pthread_mutex_unlock(&lock);

    const uint8_t download[] = {0, 255, '<', '&'};
    int downloading = strstr(req, ":download\"") != NULL;
    char *body = base64(downloading ? download : raw, downloading ? sizeof(download) : n);
    char *response = malloc(n * 2 + 1024);
    if (!body || !response) { free(body); free(response); free(req); return 1; }
    snprintf(response, n * 2 + 1024,
        "{\"ok\":true,\"result\":{\"StatusCode\":%d,\"Headers\":{\"Content-Type\":[\"application/octet-stream\"],\"X-Native-Calls\":[\"%u\"],\"X-Multi\":[\"one\",\"two\"]},\"Body\":\"%s\"}}",
        allowed ? (post ? 201 : (downloading ? 206 : 200)) : 404, count, allowed ? body : "");
    int code = result(out, response);
    free(body);
    free(response);
    free(req);
    return code;
}

static void free_buffer(void *ptr, size_t len) { (void)len; free(ptr); }
static void shutdown_plugin(void) { calls = 0; owner[0] = 0; reconfigured = 0; }

int cliproxy_plugin_init(const host_api *host, plugin_api *plugin) {
    if (!host || host->abi_version != 1 || !plugin) return 1;
    plugin->abi_version = 1;
    plugin->call = call;
    plugin->free_buffer = free_buffer;
    plugin->shutdown = shutdown_plugin;
    return 0;
}
