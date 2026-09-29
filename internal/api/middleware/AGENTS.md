# REQUEST LOGGING KNOWLEDGE BASE

## OVERVIEW
Request/response capture without blocking client streaming; score 9, a distinct logging lifecycle domain.

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Gin middleware assembly | `request_logging.go` | Capture setup and finalization |
| Deferred request capture | `request_logging.go` | `deferredRequestBodyCapture` and file spooling |
| Response interception | `response_writer.go` | `ResponseWriterWrapper` |
| Stream log dispatch | `response_writer.go` | Chunk channel and stream completion |
| Override/context extraction | `response_writer.go` | Body, API exchange, websocket timeline |
| Upstream error summary | `response_writer_error_logging.go` | Structured request-error fields |
| Capture regressions | `request_logging_test.go` | Multipart, zstd bounds, error-only mode |
| Response regressions | `response_writer_test.go` | Status, headers, cancellation, finalization |

## CONVENTIONS
- Write response bytes to the client before buffering or dispatching log chunks.
- Streaming log publication is nonblocking; full channels drop logging chunks.
- Clone headers including each value slice before asynchronous processing.
- NoRoute requests defer body capture until the authenticated handler consumes bytes.
- Error-only capture may spool to file-backed sources; successful requests discard those sources.
- Error-only spooling is capped at 32 MiB; decompressed capture is bounded separately.
- Finalize after `c.Next()` to collect handler-provided context overrides.
- Preserve request/response overrides and websocket timelines when composing log sources.
- Management path prefixes are excluded from request logging.
- Run `go test -race ./internal/api/middleware` for capture/concurrency checks.

## ANTI-PATTERNS
- Do not eagerly consume a native plugin request body during route discovery.
- Do not spool multipart form uploads into request logs.
- Do not wait for logging backpressure before returning client bytes.
- Do not share mutable header slices with logging goroutines.
- Do not turn client cancellation or status 499 into forced actionable-error logs.
- Do not leave temporary body sources behind after finalization.
- Do not decompress captured zstd bodies without the existing size bound.
