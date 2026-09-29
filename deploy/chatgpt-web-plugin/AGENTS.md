# CHATGPT WEB PLUGIN

## OVERVIEW
ChatGPT web chat/image execution and Codex/Web quota reporting; score 10 for a dense, separately configured Go module.

## WHERE TO LOOK
| Task | Location | Detail |
| --- | --- | --- |
| Native entry and host calls | `abi.go`, `bridge.c`, `bridge.h` | C ABI v1 buffers and callbacks |
| Capability/RPC dispatch | `plugin.go` | Registration schema 6 and method routing |
| Wire contracts | `wire.go` | Strict JSON, envelopes, typed public errors |
| Model and credential routing | `images.go`, `webmodes.go` | Image/chat claims and requested effort |
| Chat prompt and tools | `webchat.go` | Agent action blocks and tool-call extraction |
| Image lifecycle | `webimage.go` | Sentinel, SSE, conversation inspection, downloads |
| Proof of work | `pow.go` | SHA3-512 target search and token formatting |
| Incremental replies | `webreply.go`, `webstream.go` | Patch folding and host stream bridge |
| Quota projections | `quota.go`, `usage.go` | Separate Codex and ChatGPT Web windows |
| Dashboard | `management.go`, `web/index.html` | Static resource plus protected accounts API |
| Native loader check | `smoke/load.c` | Actual `dlopen`/ABI exercise |

## CONVENTIONS
- This directory owns `go.mod`; repository-root `go test ./...` does not cover it.
- `make check` runs race/shuffle tests, vet, and the C loader smoke check.
- `make build` emits `chatgpt-web.so` with `CGO_ENABLED=1` and `-buildmode=c-shared`.
- Go response bytes cross the ABI as C-owned allocations; release plugin and host buffers through their corresponding callbacks.
- Public errors carry HTTP status, code, and message inside the JSON envelope.
- Host auth callbacks supply credentials; the plugin does not own a duplicate OAuth store.
- Codex quota-disabled credentials may still have usable Web allowance; preserve that distinction in candidate selection.
- Streaming emits through `host.stream.emit` and terminates through `host.stream.close`.
- A possible tool-call block is held back until it can be classified instead of leaking partial markup as content.

## ANTI-PATTERNS
- Do not retry a transport failure once a request may have been sent; repeated image requests spend quota again.
- Do not walk the entire credential pool on a systematic chat failure; preserve the bounded candidate policy.
- Do not rotate accounts to retry a content-policy refusal.
- Do not omit conversation cleanup when a caller disconnects.
- Do not discard requested image size/quality hints when converting an API request into a web prompt.
- Do not re-read every completed chat before returning its accumulated stream.
- Do not conflate a Codex usage window with ChatGPT Web model limits.
