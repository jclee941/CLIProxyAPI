# NATIVE PLUGIN EXAMPLES

## OVERVIEW
Capability-focused native examples across Go, C, and Rust; score 13 from file/directory breadth, Makefile configuration, symbol/export density, and measured router references within this subtree.

## STRUCTURE
- `simple/`: mixed-capability ABI introduction.
- `auth/`, `frontend-auth/`, `frontend-auth-exclusive/`: credential and frontend access hooks.
- `executor/`, `protocol-format/`, `model/`, `thinking/`: provider execution contracts.
- `request-*/`, `response-*/`: translation, normalization, and lifecycle hooks.
- `management-api/`, `host-callback*/`, `host-model-callback/`: resource routes and host callbacks.
- `scheduler/`, `usage/`, `cli/`: scheduler, accounting, and command extensions.
- `claude-web-search-router/`: multi-backend Claude search delegation; own guide in `claude-web-search-router/AGENTS.md`.
- `codex-service-tier/`, `structured-output/`: focused request and reply policies.
- `scripts/`: shared polyglot scaffolding generator.

## WHERE TO LOOK
| Task | Location | Detail |
| --- | --- | --- |
| ABI contract and error mapping | `README.md`, `simple/README.md` | Buffers, envelopes, lifecycle |
| Build target inventory | `Makefile` | Explicit `EXAMPLES` x `LANGUAGES` (go c rust) matrix, not every directory |
| Scaffold consistency | `scripts/generate_examples.py` | Shared C/Go/Rust templates |
| Lifecycle admission/release | `request-lifecycle/go/main.go` | Request-ID ownership and completion |
| Scoped host model execution | `host-model-callback/go/main.go` | Nested execution and stream cleanup |
| Structured response repair | `structured-output/go/` | Schema/tool enforcement and SSE replay |

## CONVENTIONS
- Standard examples have `go/`, `c/`, and `rust/` peers; specialized examples may be Go-only.
- Every Go implementation except `structured-output/go/` has its own `go.mod` (module `.../CLIProxyAPI/v8/examples/plugin/<name>/go`); run its tests from that directory.
- SDK-consuming modules require `github.com/router-for-me/CLIProxyAPI/v8 v8.0.0` with `replace ... => ../../../..`; keep module paths under `.../v8/` (the generator writes them) so imports of root `internal/` packages stay legal.
- `structured-output/go/` has no `go.mod`: root tests cover it; targeted check is `go test ./examples/plugin/structured-output/go`.
- Use `make -C examples/plugin list` to inspect configured targets before choosing a build.
- The Makefile emits `bin/<example>-<lang>.<so|dylib|dll>`; outside `EXAMPLES` are `codex-service-tier`, `frontend-auth-exclusive`, `request-lifecycle`, `scheduler`, `structured-output` (use their README direct builds).
- `BIN_DIR` defaults to an absolute path; set `BIN_DIR=bin` when requesting a relative `bin/` target.
- `host-callback-auth-files`, `host-model-callback`, `claude-web-search-router` are Go-only yet listed in `EXAMPLES`: the aggregate build requests missing c/rust peers; select only their Go target.
- Build structured output from the root with `go build -buildmode=c-shared -o structured-output.so ./examples/plugin/structured-output/go`.
- Go builds use `-buildmode=c-shared`, C uses CMake, Rust uses locked `cdylib` builds.
- ABI buffers carry JSON envelopes; lifecycle `config_yaml` is encoded bytes containing raw YAML.
- Use each example's declared registration name for its `plugins.configs` key; build-target suffixes are not configuration keys.
- Management browser resources and authenticated management APIs use separate host route surfaces.

## ANTI-PATTERNS
- Do not use Go `-buildmode=plugin` or pass Go runtime objects across this C ABI.
- Do not omit `http_status` from execution error envelopes, including streaming failures.
- Do not leave host streams open or abandon an opened host HTTP operation without cancellation.
- Do not omit scoped `host_callback_id` on nested model execution; it prevents interceptor recursion.
- Do not decrement lifecycle counters for rejected requests or duplicate terminal notifications.
- Do not rewrite existing native tool calls or force optional tool use in structured-output enforcement.
- Do not regenerate all examples merely to change a specialized implementation; inspect generator ownership first.
