# Terminal Pro STOP Policy (Local Candidate)

Outcome: a Chat Completions request ending in the vendor's HTTP 502
`error.code: incomplete_upstream_response` stops without CPA credential rotation
or a second upstream submission. A partial SSE answer remains HTTP 200 with a
terminal error, not a successful final answer. Other errors retain their retry
policy. This package is a deployment prerequisite, **not deployment approval**.

Only this new directory is maintained here. The existing host disabled-state
patch/source pin, vendor patches 0001-0005, root SDK-2 core, plugins, UI, live
configuration, accounts, credentials, and production containers are unchanged.

## Merge The Policy, Do Not Replace Configuration

`provider-fragment.yaml` is deliberately **not** a standalone config. Prepend its
single rule to `request-scoped-errors` in the **existing** `openai-compatibility`
entry whose `name` is `chatgpt2api`. Preserve that entry's API keys, key weights,
base URL, models, aliases, headers, disabled state, retry override, and all other
rules and fields. Do not create a duplicate provider or replace the whole list.

At pinned RPC6, the policy belongs to the provider entry, not inside an
`api-key-entries` element. The synthesizer copies it to each native auth's
`request_scoped_errors` metadata; native provider ID is
`openai-compatible-chatgpt2api`. Do not edit OAuth files or
`oauth-request-scoped-errors`. Nonempty auth metadata rules take precedence over
configuration fallback, so activation must verify the selected runtime records
actually received the merged rules. The fixture exercises configuration loading
and the actual synthesizer rather than injecting a mock auth manager.

Exact pinned schema: `status`, `match`, `match-regexr`, `action`. Status must match;
substring/regex alternatives are ORed; the first matching valid rule wins.
The supplied regex matches the code member of the vendor's known **flat** error
object, allowing JSON whitespace. Merely mentioning the terminal code in
`error.message` does not match. This is not a general JSON-path matcher: a future
vendor error schema containing nested objects needs a fresh review/test. The
known vendor error has fixed flat fields and no braces in its fixed message.
No model-name heuristic, invented YAML field, broad 502 rule, or global retry
disable is used. Its scope is the code on this provider, including any model
that legitimately emits that code, not a name-based guess about Pro models.

## Why A Core Header Patch Is Necessary

Base: upstream `v7.2.159`, commit
`ac02da6c05e18f465aa7e3ed5b0a65a2f060917d`, RPC schema 6. Build after the existing
`../../gemini-web-plugin/host-compat/maintained.patch`; its `source.json` remains
the authoritative source/toolchain pin.

The existing auth manager matches the rule and stops its current credential loop
and retry rounds. Before any payload, its stream bootstrap path does the same.
But `unwrapExecutionBoundaryError` removes the typed STOP marker before the HTTP
handler, and no nonretryable client header is generated. Actual default Node and
Python clients therefore turned one logical terminal request into **three** CPA
requests and upstream submissions, even with the STOP config installed.

`maintained.patch` changes three production files and adds two regression files:

- `sdk/cliproxy/auth/conductor_execution.go`: retain the marker **only** when an
  existing policy already chose STOP, status is 502, and parsed `error.code` is
  exactly `incomplete_upstream_response`. Non-STOP errors and other STOP errors
  keep their existing boundary/type behavior, including original error identity.
- `sdk/cliproxy/auth/conductor_request_scoped_errors.go`: expose cloned original
  headers plus `X-Should-Retry: false` on that typed wrapper. Do not mutate the
  upstream header map or lose the underlying cause/status.
- `sdk/api/handlers/handlers_errors.go`: emit that trusted STOP signal after
  optional upstream header forwarding, even with `passthrough-headers: false`.
  An upstream `true` cannot override it. HTTP 502 and the JSON error stay honest.

No executor, translator, model router, parser, RPC ABI/schema, cooldown algorithm,
generation deadline, or retry setting changes. Existing upstream tests are not
modified. An early broader marker-preservation experiment broke original-error
identity tests; it was narrowed rather than relaxing those tests.

For already-emitted SSE bytes, HTTP headers/status cannot be replaced. The real
compatible executor recognizes the structured vendor error and forwards a
terminal stream error; the existing manager/handler does not bootstrap again
after emitted bytes. No stream source patch was necessary for the tested native
Chat Completions path. The partial fixture waits until the downstream consumer
acknowledges reading `PARTIAL` before sending the error, proving this ordering
over actual HTTP, not just assuming it from source. A role-only chunk similarly
commits HTTP 200 even though it contains no text. Neither emits a success stop.

## Tested Client Contracts And Limits

All SDK calls leave the SDK's **default retry count** intact. OpenAI Node SDK
tests run on Bun 1.3.14, Python tests on CPython 3.13.13. These are exact tested
versions, not claims about every historical or future release.

| Caller | Terminal HTTP 502 / first SSE error before payload | Role-only or partial SSE error after HTTP 200 |
| --- | --- | --- |
| Direct HTTP (no retry wrapper) | 1 submission | 1 submission |
| `openai` JS 6.32.0, default retries 2 | 1, honors `x-should-retry: false` | 1, iterator raises APIError |
| `openai` Python 2.26.0, default retries 2 | 1, honors `x-should-retry: false` | 1, iterator raises APIError |
| `ai` 6.0.116 + `@ai-sdk/openai-compatible` 2.0.35, unmodified defaults | **3, NOT SAFE** despite the header | 1, error stream part |
| Same Vercel SDK with `vercel-policy.ts`, default retries 2 | 1 | 1, error stream part |

Official source contracts inspected, in addition to executing the actual pinned
packages: [Node shouldRetry](https://github.com/openai/openai-node/blob/v6.32.0/src/client.ts),
[Python should_retry](https://github.com/openai/openai-python/blob/v2.26.0/src/openai/_base_client.py),
[Python defaults](https://github.com/openai/openai-python/blob/v2.26.0/src/openai/_constants.py),
[Vercel retry loop](https://github.com/vercel/ai/blob/ai%406.0.116/packages/ai/src/util/retry-with-exponential-backoff.ts).
Context7 was attempted but returned a monthly quota error; official source and
installed pinned source were used instead. `x-should-retry` is an SDK convention,
not a universal HTTP guarantee. Retry-After delays do not mean STOP.

### Vercel Configuration Artifact

Use the exported model constructor to install `vercel-policy.ts`:

```ts
import { OpenAICompatibleChatLanguageModel } from "@ai-sdk/openai-compatible";
import { proTerminalErrorStructure } from "./vercel-policy";

const model = new OpenAICompatibleChatLanguageModel(existingModelID, {
  provider: "chatgpt2api.chat",
  url: ({ path }) => `${existingBaseURLWithoutTrailingSlash}${path}`,
  headers: existingAuthenticatedHeaders,
  errorStructure: proTerminalErrorStructure,
});
```

No key is embedded in this template. Keep the caller's existing runtime secret
injection. The factory's typed `languageModel(id, config)` second argument is
**ignored at runtime in 2.0.35**, so passing the policy there is not sufficient;
the regression discovered this and uses the actual exported constructor.
The policy changes retryability only for this exact 502/code and preserves the
SDK's usual 408/409/429/5xx classification otherwise. The SSE provider reduces
the error to its message, losing its code; treat its error stream part as a
terminal failed call, never a trigger for automatic regeneration.

For clients without a pre-retry terminal-code policy hook, require a route/call
configuration such as `maxRetries: 0` (Python `max_retries=0`) **before** submission,
or a reviewed stable logical-request resubmission barrier. Catching the terminal
error only after an SDK exhausted its retries is too late. Such a no-retry
exception must be an explicit caller configuration artifact, not secret edits.
It sacrifices that caller's transient retries; the provided Vercel policy does
not. Do not claim generic callers can preserve both without a suitable hook.

No prompt-body deduplication is implemented. New intentional identical requests
remain new requests. No guarantee is made for outer agent/job retry wrappers,
user-triggered repeat calls, browser SDKs/CORS, other API protocols/translators,
Home dispatch, proxies stripping headers, or connection loss/timeouts before a
terminal response is observed. Those can require an independently designed
logical-request identity barrier, not a global prompt cache. A partial answer
is not a completed answer, even if useful text was already delivered.

## Reproduce And Inspect Evidence

```sh
bash deploy/chatgpt2api-plugin/stop-policy/build.sh
bash deploy/chatgpt2api-plugin/stop-policy/run-fixture.sh \
  /tmp/opencode/cpa-stop-build-XXXXXX/CLIProxyAPI-host-stop
```

The first command makes a unique pinned clone, checks source identity, applies
the existing disabled patch, requires the new STOP tests to fail before source
changes, then runs related race tests/vet and builds a truthfully labelled
`v7.2.159-compatible-host-auth-save-stop.1` binary. Its fixed build date is a
reproducibility label, not a compilation timestamp. It needs the original pinned
Go/CGO toolchain and cached Go modules; Go dependency fetching is disabled.

The second command installs locked synthetic SDK test dependencies in a unique
directory, typechecks, and launches one isolated Docker container. It needs Bun,
uv, a uv-managed Python 3.13 runtime, and the locally cached pinned postgres
image used only for libc. No PostgreSQL service starts. Docker uses `--network
none`, loopback DNS, a read-only root, dropped capabilities, no-new-privileges,
no host ports, and no production mounts. Only the fixture and read-only managed
Python runtime are mounted. Dependencies are acquired before network isolation.

Every case starts a fresh real CPA binary from a synthetic config, with **two
eligible native auth keys**, request-retry 3 and bootstrap-retries 2. Only the
fake `/v1/chat/completions` endpoint increments per-key submission counters.
Successful final answers, pre-content HTTP/SSE errors, role-only/partial errors,
ordinary 503, ordinary 502, and a terminal-code mention in a different error's
message are checked through direct HTTP and all listed SDK configurations.
Each ordinary transient control requires **two different keys** and a final
answer. The unmodified Vercel HTTP limit is an explicit expected negative
control, not silently counted as a supported at-most-once client.

Each run retains config, process logs, response/counter reports, dependency and
binary hashes, isolation metadata, and a container cleanup receipt under its
unique directory. All CPA children are terminated/waited; the container is
removed. `evidence.json` gives this turn's exact paths and hashes.

Verified: 50 cases, 307 assertions, 0 failures; total 71 synthetic submissions
(including intentional ordinary failovers and unsafe Vercel negative controls).
Unchanged Gemini driver/plugin artifacts with the combined core passed 71/71
assertions, including disabled-state handoff and persistent restart, in a
separate owned offline container. No previous proof directory was overwritten.

The broader executor suite has two pre-existing failures, reproduced on the
disabled-only baseline: the Claude shared-credential metadata race and OpenAI
compatible tool-result modality expectations. They are not suppressed or fixed
here. Related core packages and focused compatible retry tests pass. No complete
repository-wide green claim is made.

## Activation Gate

Before enabling vendor 0004's new 502 behavior, the integrating owner must review
this delta, drain in-flight work as appropriate, deploy the combined compatible
binary and correctly merged policy to **every** CPA replica on the route, and
apply the tested caller exception wherever the header is not honored. Verify
effective runtime auth policy and the request-count tests against that routing
surface before changing the vendor behavior. No mixed old/new CPA or caller
retry policy is safe. Preserve the vendor's disabled-account patch as already
required by its own gate. Rollback must remove the new vendor behavior before
removing STOP protection, after handling in-flight requests.

Live final-answer correctness, production config merge, traffic cutover, real
account behavior, and production deployment remain the coordinator's separately
approved gates. This fixture does not generate with ChatGPT, access 1Password,
import accounts, change browser profiles, or establish that live Pro has a final
answer available to recover.
