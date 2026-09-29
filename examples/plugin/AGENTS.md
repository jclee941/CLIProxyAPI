# NATIVE PLUGIN EXAMPLES

## OVERVIEW
Capability-focused C ABI examples across Go, C, and Rust; score 8 from directory breadth, file count, build configuration, and LSP symbols.

## STRUCTURE
- `simple/`: mixed-capability ABI introduction.
- `auth/`, `frontend-auth/`, `frontend-auth-exclusive/`: credential and frontend access hooks.
- `executor/`, `protocol-format/`, `model/`, `thinking/`: provider execution contracts.
- `request-*/`, `response-*/`: translation, normalization, and lifecycle hooks.
- `management-api/`, `host-callback*/`, `host-model-callback/`: resource routes and host callbacks.
- `scheduler/`, `usage/`, `cli/`: scheduler, accounting, and command extensions.
- `claude-web-search-router/`: multi-backend Claude search delegation.
- `codex-service-tier/`, `structured-output/`: focused request and reply policies.
- `scripts/`: shared polyglot scaffolding generator.

## WHERE TO LOOK
| Task | Location | Detail |
| --- | --- | --- |
| ABI contract and error mapping | `README.md`, `simple/README.md` | Buffers, envelopes, lifecycle |
| Build target inventory | `Makefile` | Explicit `EXAMPLES` list, not every directory |
| Scaffold consistency | `scripts/generate_examples.py` | Shared C/Go/Rust templates |
| Lifecycle admission/release | `request-lifecycle/go/main.go` | Request-ID ownership and completion |
| Scoped host model execution | `host-model-callback/go/main.go` | Nested execution and stream cleanup |
| Backend model selection | `claude-web-search-router/go/model_resolve.go` | Provider-specific target names |
| Structured response repair | `structured-output/go/` | Schema/tool enforcement and SSE replay |

## CONVENTIONS
- Standard examples have `go/`, `c/`, and `rust/` peers; specialized examples may be Go-only.
- Each Go implementation has its own module; run its tests from that implementation directory.
- Use `make -C examples/plugin list` to inspect configured targets before choosing a build.
- The Makefile emits libraries into `bin/`; some newer specialized examples require direct module builds.
- Go builds use `-buildmode=c-shared`, C uses CMake, Rust uses locked `cdylib` builds.
- ABI buffers carry JSON envelopes; lifecycle `config_yaml` is encoded bytes containing raw YAML.
- Library basename determines plugin ID and the corresponding `plugins.configs` key.
- Management browser resources and authenticated management APIs use separate host route surfaces.

## ANTI-PATTERNS
- Do not use Go `-buildmode=plugin` or pass Go runtime objects across this C ABI.
- Do not omit `http_status` from execution error envelopes, including streaming failures.
- Do not leave host streams open or abandon an opened host HTTP operation without cancellation.
- Do not omit scoped `host_callback_id` on nested model execution; it prevents interceptor recursion.
- Do not decrement lifecycle counters for rejected requests or duplicate terminal notifications.
- Do not forward a client Claude model name to Codex/xAI search backends.
- Do not rewrite existing native tool calls or force optional tool use in structured-output enforcement.
- Do not regenerate all examples merely to change a specialized implementation; inspect generator ownership first.
