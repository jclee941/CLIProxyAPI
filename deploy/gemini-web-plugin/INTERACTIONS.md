# Official Omni Interactions: isolated POST implementation

Historical implementation snapshot (2026-09-15). For the current consumer
contract, use [openapi.json](openapi.json), published at
https://cliproxy.jclee.me/openapi.json. The limitations below describe the
earlier implementation, not the current API.

Status: locally verified POST subset, **not a complete resumable public API**.
No deployment or host/core change is included. Existing stateless Gemini and
OpenAI-translated requests retain their existing behavior.

The public contract follows [Gemini Omni](https://ai.google.dev/gemini-api/docs/omni?hl=en)
and its Korean equivalent, retrieved on 2026-09-15. This is the Gemini web video
transport behind an Interactions-shaped CPA endpoint, not the paid Google API
transport or a claim that a web capability has the official model's entitlements.

## CPA configuration and SDK

Alongside the existing encrypted local-session configuration, enable:

```yaml
native_generation: true
native_continuation: true
```

Both default off. Enabling continuation explicitly declares `interactions` in
addition to `gemini` input/output formats and registers the existing schema-6
`scheduler.pick` capability. The plugin must be the selected, highest-priority
host scheduler. Another scheduler can cause a safe account-binding rejection;
it must not be assumed to preserve this plugin's routing decision.
CPA selects only one plugin scheduler globally. When this scheduler leaves a
request unhandled, CPA uses its builtin account selection; it does not call the
next plugin scheduler. Enabling this feature can therefore replace another
plugin's scheduling behavior even for requests without continuation.

The model is `gemini-omni-1.1-flash`. It is advertised only when this feature is
enabled and the selected account's existing model discovery reports availability.
Execution requires an encrypted `session://gemini-web/...` credential; legacy
`op://` credentials are not accepted for stored interactions. The SDK never
receives or resubmits the cookie. The existing local-session renewal and host
projection synchronization are retained.

Verified with official Python `google-genai==2.16.0` through an isolated real
CPA v7.3.1 public endpoint, not a stand-in HTTP API:

```python
import base64
from google import genai
from google.genai import types

with genai.Client(
    api_key="CPA_API_KEY",
    http_options=types.HttpOptions(
        base_url="https://CPA_BASE_URL",
        api_version="v1beta",
        retry_options=types.HttpRetryOptions(attempts=1),
    ),
) as client:
    first = client.interactions.create(
        model="gemini-omni-1.1-flash",
        input="A violinist outdoors.",
        response_format={"type": "video", "aspect_ratio": "16:9"},
        stream=False,
    )
    assert first.status == "completed"
    second = client.interactions.create(
        model="gemini-omni-1.1-flash",
        previous_interaction_id=first.id,
        input="Make the violin invisible. Keep everything else the same.",
        stream=False,
    )
    assert second.status == "completed"
    video = base64.b64decode(second.output_video.data)
```

`output_video` is the SDK convenience accessor. The wire response uses:

```json
{
  "id": "opaque-server-generated-interaction-id",
  "object": "interaction",
  "model": "gemini-omni-1.1-flash",
  "status": "completed",
  "steps": [{
    "type": "model_output",
    "content": [{"type": "video", "mime_type": "video/mp4", "data": "BASE64"}]
  }]
}
```

No `extra_body`, custom SDK, management generation route, Veo operation, or
`generateContent` continuation extension is required. Public attempts to use
`geminiWebContinuation` are rejected; that object is internal bookkeeping only.

## Supported route and option matrix

| Surface | Result |
| --- | --- |
| POST `/v1beta/interactions` | Supported subset below |
| `input` string or list of `{type:"text", text:...}` | Supported; one new prompt, up to the existing 8000-character limit |
| `previous_interaction_id` | Completed stored interaction only; the original account and conversation are required. If unavailable, the request fails without switching accounts or uploading the prior clip elsewhere |
| `generation_config.video_config.task` | `extend` only; continues the named interaction's video. Measured on one account and one conversation: 10.005s -> 20.010s -> 30.016s -> 40.000s, ten seconds per turn |
| `store:true` or omitted | Keep the bounded local interaction receipt history |
| `store:false` | Remove the completed receipt; later stateful editing with its ID fails before submission |
| `response_format.type` | `video` or omitted |
| `response_format.aspect_ratio` | `16:9` or `9:16`; omitted is framed `9:16`. Both measured 2026-09-21: portrait 720x1280 in 72.4s, landscape 1280x720 |
| `response_format.delivery` | `inline` or omitted |
| `stream:false`, `background:false` | Supported; true is rejected before generation |
| Resolution, duration, other video tasks, unknown fields | Explicit 400; not silently dropped or mapped to unsupported web options |
| GET `/v1beta/interactions/{id}` | Caller-bound retrieval; measured HTTP 200 with completed status for the 40-second interaction |
| Files inspect/download/upload | Host routes absent; URI delivery is rejected |
| OpenAI Chat/Responses for this official model | Rejected; no compatibility claim |
| Existing Flash/Omni stateless routes | Unchanged |

## Persistence, account binding, and interruption

IDs are opaque random 256-bit receipts, indexed by hash within the existing
AES-GCM encrypted application-session record. The record already binds account
ID, credential reference, identity digest, auth-user index, and revision through
authenticated storage. Conversation, reply, candidate, context metadata, and
submission state are stored there, not accepted as client identifiers.

Only a create without `previous_interaction_id` selects among eligible accounts
using round-robin. Accounts with a known exhausted five-hour or weekly quota are
excluded until their reset or a fresh usage observation restores availability.
Continuations and retrieval stay on the original account and conversation.
An unavailable continuation owner returns an error; there is no cross-account
attachment fallback. Retrieval of an existing result remains allowed when that
account's generation quota is exhausted.
The executor independently rejects wrong selected accounts, unknown/tampered
IDs, stale projections, disabled accounts, and identity changes.

Each stored receipt also binds the authenticated CPA `Metadata.caller_scope`.
This is not a client header: host `internal/api/server_middleware.go:169` obtains
`userApiKey` from the authenticated access provider's `Principal`;
`sdk/api/handlers/handlers.go:290-298` and
`sdk/cliproxy/session/identity.go:211-218` derive a domain-separated SHA-256 scope.
The scope survives the published scheduler/interceptor/executor metadata ABI.
The plugin stores only that irreversible scope, never the API key, and checks
it at both scheduling and execution. Missing trusted scope fails closed; old
unscoped receipts cannot be consumed through the public Interactions route.
A separate valid CPA API key presenting the first caller's interaction ID was
rejected by the actual SDK/CPA test, including spoofed caller headers, with zero
additional submissions. Identity follows the authenticated principal: API-key
rotation to a distinct principal does not inherit the old caller's receipts.

An exclusive account lease covers generation and durable state transitions.
Submission intent is synced before StreamGenerate. Each complete upstream receipt
frame is persisted before reading the next frame, so a later truncated response
cannot erase the known handle. Recovery observes the matching reply AND candidate
through the upstream conversation-read RPC; it never calls StreamGenerate.
Normal synchronous POST waiting also uses that read-only path. A cookie rotation
advancing the revision is carried into subsequent observations.

Unknown submission outcomes remain fenced. Credential-only account-list recovery
cannot clear their generation intent. If the transport failed before sufficient
upstream IDs arrived, the implementation reports a locally failed interaction;
that is **not proof that upstream generation failed**, and it must not be submitted
again automatically. A known but incomplete handle can return `in_progress` after
a transport error or wait budget. Public GET is still unavailable, so this state
currently needs operator/host integration rather than another create call.
Expired authentication before submission remains a 401 `auth_error`, distinct
from ambiguous post-submission outcomes.

Keep SDK retries disabled as shown. **POST is a new turn, not an idempotent retry
or a recovery endpoint.** A lost downstream success response cannot be safely
recreated with another POST. Full public recovery and idempotency are not claimed.

The newest 16 receipts per local account are retained; eviction bounds disk growth
without bounding conversation length. Evicted or `store:false` IDs fail closed.
Storage writes cannot exceed the existing 128 KiB encrypted-record read bound.
Restart preserves retained metadata and pending intent; model discovery keeps a
pending account discoverable while stateless generation remains fenced.

## Exact host blocker and minimum integration

Inspected host: upstream v7.3.1 `44e62bc8acc2f224bff9c62d222717d3f6723dea`, with
its separately maintained auth-save compatibility patch. The actual binary used
has SHA-256 `1cd125f9e1c969c157b77f010e391a17f269bc8fa7a30decf4c9f3336b401a1e`.

- `internal/api/server_routes.go:125` registers POST `/v1beta/interactions`.
  Lines 124-127 have no Interactions GET or Files route.
- `internal/api/server_management.go:246-261` handles unmatched routes;
  line 258 rejects paths outside `/v0/management` before plugin dispatch.
- `internal/pluginhost/management.go:146` prefixes registered plugin management
  paths with `/v0/management`; line 150 also rejects wildcard/parameter paths.
- The official SDK's `interactions.get` sends GET
  `/{api_version}/interactions/{id}`. Its actual CPA result was HTTP 404 with an
  empty body. It added zero upstream generation submissions.

Minimum host change for public recovery: register authenticated GET
`/v1beta/interactions/:id` and provide a supported plugin dispatch carrying the
opaque interaction ID and caller context, without invoking auth selection that
can move the request to another account or calling the generation POST path.
The plugin can then expose its durable, read-only receipt recovery there. Files
support separately needs authenticated inspect/download (and upload if offered)
routes plus corresponding ownership-bound plugin dispatch. No such host change
is included or worked around with management endpoints.

## Local evidence

Branch: `chaining-continuation`, based on `63883cdb1a1f52c668de12a6f6302916d46cb815`.
All changes are under this plugin. Diagnostic-only Go and Python fixture sources,
separate copied build tree, binaries, and logs are in ignored `.qa/chaining/`;
they are not compiled into the normal production plugin. Prior evidence remains
untouched in its earlier artifact directory.

- Go diagnostics: no diagnostics on every changed Go source/test file.
- `GOMAXPROCS=4 go test -race -shuffle=on -count=1 -p 2 ./...`: passed.
- `go vet ./...`: passed.
- Normal `go build -trimpath -buildmode=c-shared`: passed.
- Existing C ABI load/call/free/shutdown and interceptor smoke: passed.
- Official SDK POST: parsed video, distinct interaction IDs, same-account and
  exact same-conversation follow-up, cross-API-key isolation, `store:false`
  rejection, three synthetic
  submissions total and zero real Google submissions.
- Official SDK GET: 404, empty body, zero additional submissions.
- Deterministic tests cover truncated responses, unknown-outcome replay,
  wrong-account/identifier/reply rejection, exclusive leases, failed durable
  writes, credential rotation during polling, and restart recovery. No existing
  test was removed or weakened; no new test uses sleeps or timing-based readiness.

Exact logs, current source hashes, build hashes, and a complete unstaged patch
are in `.qa/chaining/artifacts/`. Production Google behavior, portrait success,
public GET/Files recovery, and full official Omni feature parity are unverified
or explicitly unsupported. This is a lead-review candidate, not a rollout gate.
