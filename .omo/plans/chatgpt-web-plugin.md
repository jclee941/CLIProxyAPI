# chatgpt-web plugin — absorb the chatgpt2api vendor into CPA

## Goal

Attach ChatGPT to CPA the way gemini-web is attached: CPA owns the credential and
the upstream calls, accounts appear with their own quota, and the external
`chatgpt2api` vendor container is retired.

## Decisive discovery (verified 2026-09-14, not assumed)

ChatGPT's backend is reachable with a plain OAuth bearer token — the same token
type CPA already stores and refreshes for Codex. No cookie scraping is needed,
which is the key difference from gemini-web.

- Vendor calls `https://chatgpt.com/backend-api/...` with
  `Authorization: Bearer {access_token}` (`/app/services/openai_backend_api.py:272,657`).
- Vendor refreshes via `https://auth.openai.com/oauth/token` — identical to CPA's codex refresh.
- CPA already holds 7 healthy codex credentials on `.114`
  (`access_token`/`refresh_token`/`id_token`, access expiry 09-20..09-24), including
  `jclee@jclee.me` — the very account the vendor has parked as `异常`.
- The vendor's account records are `source_type: oauth_login` with exactly those three
  token fields, i.e. the vendor is holding a stale copy of what CPA keeps fresh.

### Endpoints the plugin needs

| Purpose | Path | Notes |
|---|---|---|
| Identity | `/backend-api/me` | `openai_backend_api.py:346` |
| **Web quota** | `/backend-api/conversation/init` | returns `limits_progress`; `image_gen` -> quota + `restore_at` (`:334-336`, `:409-417`) |
| Anti-bot | `/backend-api/sentinel/chat-requirements` | gates chat and image paths |
| Chat | `/backend-api/f/conversation{,/prepare}` | |
| Codex responses | `/backend-api/codex/responses` | the real path behind `codex-gpt-image-2` |
| Files | `/backend-api/files`, `/backend-api/files/{id}/uploaded`, `/backend-api/files/download/{id}` | image upload/download |
| Models | `/backend-api/models`, `/backend-api/accounts/check/v4-2023-04-27` | |

## Principal risk — REVISED 2026-09-14 after live probing

The original "sentinel gates everything" assumption was **wrong twice**. Measured
boundary, all through CPA's `api-call` passthrough with a codex bearer:

| Endpoint | Headers needed | Result |
|---|---|---|
| `/backend-api/wham/usage` | bearer only | 200 |
| `/backend-api/me` | bearer + browser fingerprint | 200 (403 without fingerprint) |
| `/backend-api/conversation/init` | bearer + browser fingerprint | 200, returns `limits_progress` |
| `/backend-api/models`, `/backend-api/accounts/check/v4-2023-04-27` | bearer + fingerprint | 200 |
| `/backend-api/sentinel/chat-requirements` | needs a real proof-of-work `p` | 500 with a dummy token |
| **`/backend-api/codex/responses`** | **bearer + Content-Type ONLY** | auth passes (400 was model validation) |

So sentinel only gates the **browser conversation** path. The vendor's own image
generation does **not** use it: `_codex_responses_headers()` returns exactly
`{Authorization: Bearer …, Content-Type: application/json}` and the image request is a
`codex/responses` call with an `image_generation` tool
(`openai_backend_api.py:844-860`). `_image_headers` with the sentinel tokens belongs to
the other, browser-based path.

### Codex image-generation contract (verbatim from the vendor)

```json
{"model":"gpt-5.5",
 "instructions":"Use the image_generation tool to create exactly one image for the user's request. Return the generated image result.",
 "store":false,
 "input":[{"role":"user","content":[{"type":"input_text","text":"<prompt>"}]}],
 "tools":[{"type":"image_generation","model":"gpt-image-2","action":"generate","size":"1024x1024","quality":"auto","output_format":"png"}],
 "tool_choice":{"type":"image_generation"},
 "stream":true}
```

`CODEX_RESPONSES_MODEL = "gpt-5.5"` (`gpt-5.6` is rejected: "not supported when using
Codex with a ChatGPT account"). Edits add `{"type":"input_image","image_url":"data:image/png;base64,…"}`
to the content and set `action:"edit"`. The SSE stream carries
`type:"image_generation_call"` events whose `result` is the base64 image.

Consequence: image generation needs **no sentinel and no browser**, so slices 2-4 are
far cheaper than planned and the vendor can genuinely be retired.

## Slices

### Slice 1 — quota visibility without sentinel (no generation)
- New plugin `deploy/chatgpt-web-plugin/` modelled on `deploy/gemini-web-plugin/`.
- Credential source: host auth manager, codex provider entries (no new secret store).
- Implement `/backend-api/me` + `/backend-api/conversation/init` reads only.
- Implement the `quota.*` ABI (`quota.identifier|describe|fetch|reset`) and declare
  `capabilities.quota_provider`, mapping `limits_progress` -> `QuotaFetchResponse`
  groups/buckets. Reuse the mapping proven for gemini-web in `session_quota.go`.
- Dashboard section listing ChatGPT accounts with **web quota separated from codex usage**
  (the user's "쿼타 조회도 웹이랑 나눠서").
- Vendor untouched. Nothing can regress.

### Slice 2 — chat through CPA
- `/backend-api/sentinel/chat-requirements` handling, then `/backend-api/codex/responses`.
- Prove parity against the vendor for one model before routing anything to it.

### Slice 3 — image generation
- File upload + `_start_image_generation` equivalent + result download.
- Prove a real image end-to-end.

### Slice 4 — retire the vendor
- Move `gpt-image-*` off the `openai-compatible-chatgpt2api` provider onto the plugin.
- Stop and remove the `chatgpt2api` container and its compose entry; drop
  `CHATGPT2API_AUTH_KEY` from the core env.

## Current workaround while this is built

`gpt-image-*` is served by provider `openai-compatible-chatgpt2api` and cannot avoid the
dead vendor pool. Verified working alternative today: `grok-imagine-image-2.0`
(200, ~7s, real image bytes) and `grok-imagine-image` (200, ~5s). The user chose to wait
for chatgpt-web rather than switch.

## Non-goals

- No cookie/browser session store for ChatGPT (OAuth makes it unnecessary).
- No change to gemini-web behaviour.
- Do not weaken the vendor's account pool or force its status fields; forcing
  `status: 正常` was tried and the vendor re-parks the account by itself.
