# IN-MEMORY USAGE QUEUES

## OVERVIEW
Usage/error publication and usage-plugin serialization; score 8, a distinct event-delivery domain.

## WHERE TO LOOK
| Task | File |
|------|------|
| Retention, backlog, subscriptions | `queue.go` |
| SDK usage plugin and payload fields | `plugin.go` |
| Usage-statistics switch | `usage_toggle.go` |
| Queue delivery semantics | `queue_test.go` |
| Usage/error/session serialization | `plugin_test.go` |

## CONVENTIONS
- The implementation is in-memory; the Redis-compatible wire endpoint lives in `../api/`.
- Usage plugin registration happens in `init()` through the SDK usage manager.
- Queue enablement and usage-statistics enablement are separate switches.
- `Enqueue` publishes directly when subscribers accept the payload; otherwise it retains usage backlog.
- `PopOldest` consumes retained usage entries in order.
- `EnqueueError` broadcasts only; error events have no retained backlog.
- Subscriber delivery clones byte payloads.
- Slow subscribers are removed and their channels closed on saturation.
- Usage subscriptions receive the support/refresh protocol payload.
- Session hierarchy serialization clears self-referential parent IDs.

## ANTI-PATTERNS
- Do not add a Redis network dependency merely because of the package name.
- Do not promise replay of error events published without subscribers.
- Do not block producers while waiting for a slow subscriber.
- Do not retain usage events twice when the live-subscriber path has handled them.
- Do not conflate statistics collection with protocol queue availability.

Run `go test ./internal/redisqueue` for queue and serialization tests.
Wire behavior is additionally covered by `internal/api/redis_queue_protocol_integration_test.go`.
Home and management usage consumers depend on the same queue semantics.
