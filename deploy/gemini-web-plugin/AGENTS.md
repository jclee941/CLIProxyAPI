# GEMINI WEB PLUGIN

## OVERVIEW
Native Gemini session, media, and durable interaction engine; score 15 from module size, directory breadth, configuration, symbols, and measured LSP references.

## STRUCTURE
- Root Go files: one `package main` implementing the plugin and its state machines.
- `web/`: Gemini and Flow portal sources; follow `web/AGENTS.md`.
- `browser-extension/`: consented session handoff; follow `browser-extension/AGENTS.md`.
- `smoke/`: C loader and interceptor ABI checks.
- `ops/`: operator-started maintenance runner (`maintain.py`, unscheduled systemd oneshot) and its stdlib tests.

## WHERE TO LOOK
| Task | Location | Detail |
| --- | --- | --- |
| ABI/RPC entry | `abi.go`, `plugin.go`, `wire.go` | C ABI 1, registration schema 6 |
| Local persistence | `session_store.go`, `session_runtime.go` | Encrypted records, ownership, revisions |
| Login and reconciliation | `login.go`, `login_commit.go` | Consent flow and host projection |
| Credential concurrency | `credential_leases.go`, `session_lifecycle.go` | Maintenance fences, draining, pending-writer slot waits |
| Flow session broker | `session_exchange.go`, `session_exchange_cancel.go` | Cookie ownership, scoped uploads, explicit cancellation |
| Account selection | `session_slots.go`, `session_quota_routing.go` | Slot rotation and quota admission |
| Native upstream protocol | `webaccount.go`, `webgenerate.go`, `webvideo.go` | WIZ frames, JSPB slots, Veo replies |
| Video wire experiments | `video_wire.go`, `video_diag.go` | Per-turn variant selection and redacted wire diagnostics |
| Continuation lifecycle | `continuation*.go` | Durable intents, pinned turns, recovery |
| Interaction API and replay | `interactions*.go` | Caller-scoped receipts and SSE cursors |
| File upload/download | `files*.go`, `webdrive.go`, `webupload.go` | Drive capabilities and encrypted metadata |
| Public contract | `openapi.json`, `INTERACTIONS*.md` | Routes and recovery semantics |

## CONVENTIONS
- This is a separate Go module: run `make check` or `go test -race -shuffle=on -count=1 ./...` here.
- Deployment authority and browser ownership procedures live in `../AGENTS.md`.
- `make smoke` builds the real shared library and loads it through the C harness.
- `GEMINI_WEB_SESSION_KEY` supplies canonical base64 for a 32-byte key; configuration does not carry the key.
- Store references use `session://gemini-web/<32 lowercase hex>`; `sessionFile` validates before resolving a record.
- Store writes bind identity, revision, and state through AES-GCM authenticated data and atomic persistence.
- The store is single-owner for the plugin lifetime; reconfiguration drains active work before replacing runtime state, and a hot reload's `plugin.quiesce` drains running calls (at most 90 s) and closes the store so the replacement takes the owner lock; a drain timeout keeps the old instance serving.
- Interaction ownership comes from trusted host caller metadata, not a caller-supplied routing header.
- Detached interaction execution can survive downstream disconnect; retrieval and cursor replay read the durable outcome.
- Native account discovery, quota snapshots, and scheduler admission are distinct responsibilities.
- Flow borrows requests through the broker; this plugin alone attaches cookies and persists their rotations.
- A turn waiting for its account waits as a pending writer: new shared Flow holds are refused until the turn owns the slot.
- Broker targets and forwarded upload headers are explicit allowlists; cancellation identifies a specific exchange.
- The native web transport is the only path; `native_generation` is a legacy key accepted only as `true`. `native_continuation` gates continuation registration and dispatch; preserve the configured format/capability pairing.
- `video_wire_mode` accepts `legacy`, `web`, or `alternate`; continuation inherits its turn's variant unless an explicit mode overrides it.
- Changing only video wire policy does not drain sessions; verify the applied variant in diagnostics, not only the saved config.
- Plugin `video_mode` is the default capability mode when a request names none; an account without it keeps its standard mode, request modes stay strict, and changing it does not drain sessions.
- Plugin `video_thinking_level` (1-4; zero keeps STANDARD) is the slot 80 level a video turn sends when its request names none; changing it does not drain sessions.
- Tuning keys (`tuning.go`) are flat non-negative integers overriding named defaults (0 or absent keeps them); they are read at use from an atomic snapshot, so a PATCH does not drain sessions, and a negative value, `route_room_ceiling_percent` over 100 or `text_thinking_slot` over 4 rejects the config.
- A turn the default mode answers in prose (slot 9 names a language) is read back once and, if it ended without a video and without a named limit, resent on the standard mode as the web edit of the refused attempt (`video_fallback.go`); a requested mode is never resent.

## ANTI-PATTERNS
- Do not delete owner, intent, or recovery records to clear an uncertain operation.
- Do not replace the store key independently of the encrypted session directory.
- Do not resubmit a generation through GET/recovery or repeat POST to reconnect a stream.
- Do not treat a prepared receipt or unknown submission result as authorization to send again.
- Do not derive account identity from labels or email addresses.
- Do not let upload headers or caller-provided URLs redirect private Drive capabilities.
- Do not translate Omni failures out of the stop-policy prefix/status contract the host matches.
- Do not weaken host schema checks to load a compatibility build.
- Do not reintroduce TryLock polling for slot waits; overlapping shared Flow holds starve the waiting turn.
- Do not add retries, redirects, environment proxies, or request timeouts to the `ops/` maintenance request.
- Do not return cookie jars to Flow or accept caller-supplied broker authorization/account-selector headers.
