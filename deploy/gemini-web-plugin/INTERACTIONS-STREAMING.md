# CPA Omni streaming and receipt retrieval

This document describes the local streaming/retrieval implementation, extending
`INTERACTIONS.md`'s synchronous POST subset. It is not a deployment claim.

## Host compatibility

The primary feature base declared RPC schema 2 while the deployed plugin uses
schema 6. The initial real-host test rejected registration. Source comparison
used the already-present upstream v7.3.1 commit
`44e62bc8acc2f224bff9c62d222717d3f6723dea`, not a cloned or switched checkout.
The host now backports the actual versioned contracts:

- `ba5ab795`: schema 3 request-body omission on payload interceptor chunks.
- `4b5f1eab`: schema 4 upstream WebSocket event observation, including executor
  and handler delivery. The older primary has one Codex stream reader rather
  than the newer two-reader implementation; its existing reader is instrumented.
- `649a8bdb`: schema 5 per-payload history omission.
- `c6327a86`: schema 6 raw management JSON preservation.

Registration retains each plugin's declared schema; omitted versions mean 1.
Legacy plugins retain their previous request-body, history and escaping behavior.
Future versions are still rejected. This is a bounded contract backport, not a
merge of the newer host, quota endpoints, affinity subsystem or hot-reload
quiesce implementation. Existing plugin shutdown/drain remains in force.

Credential-owned `action:"stop"` error rules are projected onto the primary's
existing request-scoped error interface at the plugin executor adapter. This
prevents refresh, account failover, retries and cooldown after an ambiguous paid
submission. Nonmatching errors and legacy credentials keep prior behavior;
this does not claim the newer host's configuration-wide four-action policy API.

The real local HTTP test uses the unchanged schema-6 Gemini Web registration,
actual host auth selection, caller context, RPC adapter and stream bridge, actual
plugin service/encrypted store, and a faithful upstream HTTP fixture. It covers
initial streaming, disconnect/reconnect, cursor replay, caller isolation, missing
receipts, completed GET and an explicit synchronous same-chat follow-up. No real
Google generation is used.

## Public contract

Use CPA Bearer authentication and model `gemini-omni-1.1-flash`. No Google API key
or direct Google API client is involved. Both native feature flags and the
existing continuation scheduler remain required.

POST `/v1beta/interactions` accepts the existing text input and video response
format, plus `stream:true`. `store:true` (the default) retains the receipt and
result. `previous_interaction_id` names a completed caller-bound receipt and
creates exactly one new turn carrying that receipt's video as an uploaded
reference, on whichever eligible account the scheduler selects. A POST
retry is still a new generation request: never reconnect by repeating POST.
Synchronous POST remains available. Background execution is not exposed.

Each SSE event has an `event:` name and an `id:` cursor. JSON data includes
`event_type` and `event_id`. The receipt is durably prepared before the first
event; video events are emitted only after actual completion and persistence.
There are no synthetic progress, thought, or partial-video events.

| Cursor | Event | JSON data (apart from event fields) |
| --- | --- | --- |
| `<receipt>:1` | `interaction.created` | `interaction:{id,object:"interaction",model,status:"in_progress",steps:[]}` |
| `<receipt>:2` | `step.start` | `index:0,step:{type:"model_output"}` |
| `<receipt>:3` | `step.delta` | `index:0,delta:{type:"video",mime_type:"video/mp4",data:"BASE64"}` |
| `<receipt>:4` | `step.stop` | `index:0` |
| `<receipt>:5` | `interaction.completed` | `interaction:` the full completed JSON resource, including `steps` |
| `<receipt>:6` | `done` | Literal `[DONE]`, not JSON |

GET `/v1beta/interactions/{receipt}` returns the existing interaction snapshot.
While a locally owned generation is running it returns `status:"in_progress"`
without waiting on its exclusive account lease. After a restart, a pending
receipt uses only the existing read-only continuation recovery operation. A
prepared-but-unsubmitted receipt is never submitted by GET. Unknown upstream
submission outcomes must not be interpreted as authorization to submit again.

GET with `?stream=true&last_event_id=<receipt>:N` emits only events after N. An
absent cursor replays from event 1. `Last-Event-ID` is accepted when the query
cursor is absent. Foreign, noncanonical, or impossible cursors return 400;
missing or other-caller receipts return 404. A retained completed receipt can be
replayed without upstream access. Cursor 6 yields no events, only an SSE comment
confirming completion to the host's stream bootstrap.
Pending observations or upstream errors may end a stream with an error; retain
the receipt and use GET rather than replacing the generation with another POST.

## Ownership and persistence

Generation runs in the existing plugin session lifecycle, detached from the
subscriber's context and canceled by plugin shutdown. Subscribers can disconnect
without canceling the owned submission. Per-receipt in-memory operations let GET
join existing work; they are not a new generation queue. After process loss, the
encrypted receipt's submission intent and exact c/r/rc metadata remain the
source of truth. GET only reads/recoveries that operation.

Completed native response bodies are stored in separate encrypted
`interaction-<receipt-hash>.bin` records under the existing private session
directory. The existing AES-GCM key, single-process store ownership and
fsync/rename transaction are reused. AAD binds purpose, account, credential
reference, receipt hash and caller scope. Receipt state is committed before
completion events are sent. Fixed version-1 event ordinals plus the stored result
produce stable replay without enlarging the credential JSON record limit.
Receipt eviction and `store:false` remove their result artifacts. The existing
16-receipt retention policy still applies. A process interruption between result
write and receipt commit can leave an encrypted orphan; it grants no retrieval
access and cannot cause submission replay.

## Documentation reference

Event semantics were checked against Google's streaming documentation on
2026-09-15: https://ai.google.dev/gemini-api/docs/streaming?hl=en . This is an
explicit CPA contract using those event names, not a claim of full official
Interactions API parity. There are no invented duration or resolution fields,
Files routes, URI delivery, media input, or alternate public models.
