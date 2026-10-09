# Google Flow native plugin

`flow2api.so` serves Google Flow image and video generation through the existing
Gemini `generateContent` API surface. It uses an existing Gemini account binding;
it does not keep a second copy of that account's Google cookie jar.

## Generate media

Send an authenticated `POST /v1beta/models/{model}:generateContent`:

```json
{
  "contents": [{
    "role": "user",
    "parts": [{"text": "A yellow paper boat floating on calm blue water."}]
  }],
  "generationConfig": {
    "aspectRatio": "9:16",
    "durationSeconds": 4
  }
}
```

Use `flow-omni-1.1-flash` or `flow-veo-3.1-fast` for video. For an image, use
`flow-nano-banana-2` (currently Nano Banana 2.1) and replace `generationConfig`
with `{"imageConfig":{"aspectRatio":"9:16"}}`.

The response contains base64 media at
`candidates[0].content.parts[0].inlineData`, including its `mimeType`.
Each request is independent. Do not replay a request after an uncertain
submission or connection loss. The combined consumer specification is available
at `/openapi.json`.

## Account configuration

Keep the existing `gemini-web` configuration and add the source account ID to
its `session_exchange_accounts`. Configure this plugin separately:

```yaml
plugins:
  configs:
    gemini-web:
      session_exchange_accounts:
        - gemini-web-ACCOUNT_ID.json
    flow2api:
      enabled: true
      accounts:
        - gemini-web-ACCOUNT_ID.json
      session_broker_url: http://127.0.0.1:8317
      captcha_provider: native
```

Register a `flow2api` auth file through the existing management auth-file API:

```json
{
  "type": "flow2api",
  "id": "flow2api-account.json",
  "label": "Flow account",
  "source_auth_id": "gemini-web-ACCOUNT_ID.json"
}
```

The reference file contains no cookies. Sign into Flow in the source browser
profile before capturing its Gemini session with the updated companion. The
capture includes Flow's host-only session cookies. `MANAGEMENT_PASSWORD` in the
core environment authenticates the local session broker.

`GET /v0/management/plugins/flow2api/accounts` reports credits, the current
project, and named account errors. External captcha services can be selected
with `captcha_provider: yescaptcha` or `capsolver` and a protected `captcha_key`;
neither is required by the native provider.

## Build

Run `make check` in this module. Build deployment artifacts on Debian bookworm
and update `flow2api.so.sha256` and `build-manifest.json`. The deployment manifest
loads this plugin using the normal restart convergence procedure.
