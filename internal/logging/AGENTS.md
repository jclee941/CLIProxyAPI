# LOGGING AND REQUEST TRANSCRIPTS

## OVERVIEW
Application output, request/body capture, and Home forwarding; score 14 (32 Go files, 108 exports, 61 importing files), a distinct observability lifecycle domain.

## WHERE TO LOOK
| Concern | File |
|---------|------|
| Base logger and output destinations | `global_logger.go` |
| ELK JSON output format | `elk_formatter.go` |
| Gin request/recovery middleware | `gin_logger.go` |
| Request logger interfaces/context keys | `request_logger.go` |
| Capture files and collisions | `request_logger_writer.go` |
| Stream transcript lifecycle | `request_logger_streaming.go` |
| Spool-backed body ownership | `request_logger_body_source.go` |
| Formatting/decompression | `request_logger_format.go` |
| Error-log classification of capture files | `request_logger_error_detect.go` |
| Request transcript forwarding | `request_logger_home.go` |
| Application log forwarding | `home_app_log_forwarder.go` |
| Trace IDs | `cpa_trace.go` and `requestid.go` |
| Response/request context holders | `requestmeta.go` |
| Diagnostic allowlisting | `diagnostic.go` |
| Retention and Telegram | `log_dir_cleaner.go`, `telegram_hook.go` |

## CONVENTIONS
- Application logs and full HTTP transcripts are separate output systems.
- Middleware probes optional richer `RequestLogger` interfaces before the base interface.
- `FileBodySource` allows large bodies and WebSocket timelines to stay spool-backed.
- Use a fresh response-header holder for each retry attempt.
- A process-wide mux hook selects the active Home application forwarder across reconnects.
- Unbound forwarders drop entries rather than selecting a global fallback client.
- `IsRequestErrorLogFile` trusts `error-*` names, else scans at most a 64 KB tail; status 499 is a cancellation, not a failure.
- Diagnostic helpers keep allowlisted failure signals, not arbitrary error prose.

## ANTI-PATTERNS
- Do not reuse response-header state from an earlier credential/model attempt.
- Do not swallow `http.ErrAbortHandler` in Gin recovery; it must reach `net/http`.
- Do not register an accumulating Home log hook on every reconnect.
- Do not bypass body-source cleanup when request logging fails or is canceled.
- Follow the root secret-logging rule through the existing header masking and diagnostic helpers.

Run `go test ./internal/logging`; forwarding/body ownership changes also affect API middleware.
