# structured-output

Holds a caller's strict contract on providers that do not enforce it themselves.

Some providers are bridges onto a chat product rather than the platform API. They
accept `response_format`, or a tool definition, and then answer with prose anyway.
This plugin states the contract to the model, reduces the reply to its JSON value,
checks it against the schema, and asks again with the specific violations when the
reply does not conform.

Providers with native structured output and native function calling are
unaffected: their first reply already validates, so nothing is rewritten and
nothing is re-executed. The extra upstream call happens only on a real violation.

## Contracts

### Response schema

Read from either dialect:

| Dialect | Source |
|---|---|
| OpenAI chat completions | `response_format.type` = `json_object` \| `json_schema`, schema at `response_format.json_schema.schema` |
| Gemini generateContent | `generationConfig.responseJsonSchema`, `generationConfig.responseSchema`, or `generationConfig.responseMimeType` = `application/json` |

The reply is reduced to its JSON value — code fences, prose and double encoding
are stripped — then validated. Violations are reported by path, for example
`items[0].id must be integer but is string`, and fed back into the retry.

### Strict function call

A call is enforced only when the caller **demanded** one:

| Dialect | Demanded by |
|---|---|
| OpenAI | `tool_choice: "required"`, or `tool_choice: {"type":"function","function":{"name":...}}` |
| Gemini | `toolConfig.functionCallingConfig.mode: "ANY"` |

Optional tool use (`auto`, or no `tool_choice`) is left to the provider, because a
missing call cannot be judged wrong. When a call is demanded, the model is asked
for a call envelope, each call's `arguments` is validated against that function's
`parameters` schema, and the result is written back as a native call: `tool_calls`
with string `arguments` and `finish_reason: "tool_calls"` for OpenAI, a
`functionCall` part for Gemini.

For a model named in `instruct_tools` the envelope is requested up front; for any
other model the same repair still happens, one round trip later, after the
upstream answers with prose.

Every repair attempt is logged at warn with `state=` carrying the outcome
(`regenerated`, `regeneration_failed`, `budget_exhausted`) and `reason=` carrying
the first violation, so the rate at which a provider breaks its contract is
visible rather than hidden behind a repaired reply.

A reply that already carries a native tool call is never rewritten.

## Streaming

A schema cannot be judged from a partial stream. When a streaming request carries
a contract, the plugin calls the model once without streaming, enforces the
contract on the complete reply, and returns the result as the event stream the
caller expects: a two-chunk delta sequence plus `data: [DONE]` for OpenAI, one
whole `GenerateContentResponse` event for Gemini.

If the model cannot be reached, the request falls back to the ordinary streaming
path rather than failing.

## Configuration

```yaml
plugins:
  configs:
    structured-output:
      enabled: true
      instruct: true
      clean: true
      validate: true
      max_attempts: 2
      buffer_streaming: true
      instruct_tools: ["gemini-web"]
      strip_agent_tags: false
```

| Key | Default | Effect |
|---|---|---|
| `instruct` | `true` | States the response schema to the model before the request is sent. |
| `clean` | `true` | Reduces the reply to its JSON value. |
| `validate` | `true` | Checks the reply against the requested schema. |
| `max_attempts` | `2` | Regeneration budget after a violating reply. `0` delivers the cleaned reply unchanged. |
| `buffer_streaming` | `true` | Answers a streaming strict request from one complete, validated reply. |
| `instruct_tools` | unset | Models that should be told about a demanded function call up front, given as a list of substrings matched against the model id. This saves a round trip on a bridge that cannot call functions. Leave it unset for providers that call functions natively, because the instruction talks them out of a real call; on a mixed core, name only the bridges. `true` still means every model and `false` still means none, so an existing boolean keeps working. Optional tool use is never instructed either way. |
| `strip_agent_tags` | `false` | Removes dangling `<Image .../>` cards whose `src` is an internal agent placeholder. Rewrites ordinary replies, not just structured ones. |

## Guarantees and limits

- Enforcement never turns a usable answer into a failed request. If regeneration
  cannot run, the best reply obtained is delivered.
- A schema this plugin cannot parse never fails a reply.
- Regeneration is bounded by `max_attempts` and cannot recurse: the host marks the
  nested execution with this plugin's identity, so it skips this plugin's own
  interceptors.
- Supported dialects are OpenAI chat completions and Gemini `generateContent`. The
  OpenAI Responses `text.format` surface is not covered.
- Only the first text part of a reply is treated as the answer.

## Build

```bash
go build -buildmode=c-shared -o structured-output.so ./examples/plugin/structured-output/go
```

Deploy the resulting `.so` with the rest of the plugin set and restart the core.
