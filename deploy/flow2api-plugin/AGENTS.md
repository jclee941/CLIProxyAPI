# FLOW NATIVE PLUGIN

## OVERVIEW
Google Flow generation and resource APIs over Gemini-owned sessions; score 13 for module size, configuration, code density, symbols, and measured LSP references.

## WHERE TO LOOK
| Task | Location | Detail |
| --- | --- | --- |
| Native entry and dispatch | `abi.go`, `bridge.c`, `bridge.h`, `plugin.go` | C ABI 1, registration schema 6 |
| Public HTTP surface | `flow_http.go`, `protocol.go` | Resource routes and protocol admission |
| Generation submission | `flow_generate.go`, `flow_generation.go`, `flow_batch.go` | Token rejection, batch settlement, output count |
| Options and credit policy | `flow_input_types.go`, `flow_model_options.go`, `flow_selection.go` | Explicit modes, live capabilities, low-priority admission |
| Gemini session borrowing | `session_exchange.go`, `flow_session.go` | Broker requests, lease wait, RPC framing |
| Captcha providers | `flow_captcha.go`, `flow_captcha_browser.go` | Native, signed-out browser, external solver |
| Resource operations | `flow_projects_api.go`, `flow_scenes_api.go`, `flow_collections_api.go`, `flow_entities_api.go` | Validated Google resource shapes |
| Tools and workflows | `flow_tools_api.go`, `flow_tool_versions.go`, `flow_workflows_api.go` | Builder state, version history, recovery reads |
| Agent sessions and messages | `flow_agents_api.go`, `flow_agent_messages.go` | Project-scoped conversations, typed events, structured prompts |
| Uploads and media | `flow_upload_image.go`, `flow_upload_video.go`, `flow_media_api.go` | Scoped resumable transfers and references |
| Protocol output | `flow_response_formats.go`, `flow_response_chat.go`, `flow_response_claude.go`, `flow_response_responses.go` | Plain and streaming responses |
| Status and dashboard | `flow_status.go`, `flow_resource.go`, `DESIGN.md` | Account credits, lease state, bundled resource |
| Host quota provider | `flow_quota.go` | Monthly credit window, busy-lease answers from the latest reading |
| Wire fixtures and ABI smoke | `testdata/flow_models.json`, `smoke/load.c` | Captured catalog shapes and real loader checks |

## CONVENTIONS
- Deployment authority and artifact reload policy live in `../AGENTS.md`.
- Flat `package main`, separate `go.mod`; run `make check` here for race/shuffle tests, vet, and C loader smoke.
- `make build` emits `flow2api.so` with `CGO_ENABLED=1` and `-buildmode=c-shared`.
- Keep `flow2api.so.sha256` and `build-manifest.json` paired with the built artifact.
- Gemini owns credentials and rotation; Flow receives broker response bodies and scoped headers, never its cookie jar.
- A refused credential lease can be awaited before submission; `withoutLeaseWait` lets account listings and quota lookups answer at once from the latest reading (`flowReadings`, also fed by generation balance reads and used for the credit floor); only a lookup with no reading waits up to `flowQuotaLeaseWait`.
- Rejected captcha tokens can be replaced because no generation started; a lost response is an unknown outcome.
- Each browser token page opens after closing leftover Flow token pages and with an emptied cookie jar; a `flow_rate_limited` refusal naming `UNUSUAL_ACTIVITY` (the free queue's `UNUSUAL_ACTIVITY_TOO_MUCH_TRAFFIC`) judges the token's client, so a browser token is replaced from a fresh page within `flowTokenAttempts`. Native and solver tokens, and other traffic throttles, are returned as they are.
- Low-priority refusal may fall back to paid normal priority only with at least the code's 5000-credit floor.
- `web/index.html` is generated from `../gemini-web-plugin/web/src/flow/`; follow that web directory's guide.
- Public consumer behavior is documented in `README.md`; do not infer supported model options from names alone.

## ANTI-PATTERNS
- Do not replay uncertain submissions or resubmit a batch to repair partial results.
- Do not confuse a pre-submit broker lease refusal with a generation that may already have run.
- Do not substitute a different operation when a model lacks the requested mode.
- Do not turn malformed resource replies or missing text into an empty success.
- Do not invent token usage where Flow supplies none.
- Do not echo solver responses or cookie/account secrets in errors.
- Do not sign in to the captcha browser or create a second holder of Gemini's session.
- Do not edit the generated dashboard in this directory.
