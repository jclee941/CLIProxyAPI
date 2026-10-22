# ChatGPT Web API

## Models

`gpt-6-pro` is the public Pro model ID. It uses the ChatGPT web session, not
the Codex API allowance. The former `gpt-app-pro` name remains accepted for
existing requests but is not advertised in `/v1/models`.

`gpt-web-chat` selects the web chat presets through `reasoning_effort`.
`gpt-web-image` serves image generation.

## Media input

Verified through the deployed `/v1/chat/completions` endpoint on 2026-10-22:

| Input | Request content part | Verified behavior |
| --- | --- | --- |
| MP3 | `{"type":"input_audio","input_audio":{"format":"mp3","data":"<base64 bytes>"}}` | Two original 60-second narration files reached Pro and produced acoustic measurements. |
| WAV | `{"type":"input_audio","input_audio":{"format":"wav","data":"<base64 bytes>"}}` | The same two 60-second files, with order reversed, reached Pro and produced a structured comparison. |
| MP4 | `{"type":"video_url","video_url":{"url":"data:video/mp4;base64,<base64 bytes>"}}` | A real 40-second 720p video produced correct framing details and an exact Korean title, checked against its original first frame. |
| Named file | `{"type":"file","file":{"filename":"sample.mp3","file_data":"<base64 bytes>"}}` | WAV, MP3 and MP4 use the same upload path. A matching data URL can replace bare base64. |

Place media in a user message's `content` array. The plugin accepts up to
10 files, each at most 100 MiB decoded, and checks the declared file signature.
Remote `video_url` URLs, OGG, images and documents are not supported by this
chat attachment path; fetch an MP4 in the caller and provide a data URL.

Audio access does not establish reliable direct listening. The MP3 probe
explicitly used RMS and spectral measurements rather than listening. A WAV
reply claimed listening as well as measurements, which was not independently
verified. Earlier reversed-order WAV comparisons contradicted the measured
timbre difference twice. Treat these replies as assisted acoustic analysis,
not a dependable standalone judge of narration quality.

The production MP3, WAV and MP4 probes returned HTTP 200, valid requested JSON,
`finish_reason: stop`, and `system_fingerprint: gpt-6-pro`.

## Structured output

Use `response_format.type` of `json_object` or `json_schema`. For example:

```json
{
  "model": "gpt-6-pro",
  "messages": [{"role": "user", "content": "Describe the attached media briefly."}],
  "response_format": {
    "type": "json_schema",
    "json_schema": {
      "name": "review",
      "strict": true,
      "schema": {
        "type": "object",
        "properties": {
          "observations": {"type": "string"},
          "analysis_basis": {"type": "string"}
        },
        "required": ["observations", "analysis_basis"],
        "additionalProperties": false
      }
    }
  }
}
```

The schema is included in the web request's instructions. Presentation wrappers,
including code fences and `<escape>`, are removed before validating the actual
JSON document against the requested schema. Local `$defs` references work;
external schema URL and filesystem loading are disabled.

Structured streaming buffers content until the complete document validates.
It never emits a partial JSON document as successful content. Combining
`response_format` with tools is currently rejected.

The web product can still exhaust its output budget. A reproduced large
planner request ended upstream with `max_tokens` and
`finished_partial_completion`; transport completion did not mean the JSON was
finished. Ordinary responses preserve this as `finish_reason: length`.
Incomplete structured replies return `web_structured_output_truncated`.
The plugin does not silently regenerate or increase the upstream limit.

| Error | Meaning |
| --- | --- |
| `400 invalid_json_schema` | Missing, invalid or externally dependent schema; rejected before generation. |
| `400 unsupported_response_format` | Unknown format or a format combined with tools. |
| `400 unsupported_chat_attachment` | Unsupported attachment type or input shape. |
| `400 invalid_chat_attachment` | Invalid base64 or a mismatched file signature. |
| `400 too_many_chat_attachments` | More than 10 files. |
| `413 chat_attachment_too_large` | A decoded file exceeds 100 MiB. |
| `422 web_structured_output_invalid` | No valid document or a schema violation. |
| `422 web_structured_output_truncated` | The upstream output limit left the requested JSON incomplete. |

If the core has already sent non-stream keepalive headers, an error body can
arrive with HTTP 200. Check the top-level `error` before reading `choices`.

## Manager access

The registered ChatGPT page uses the Manager's authenticated in-memory request
capability. It needs no second key entry and does not copy the key into the
iframe. Unauthenticated management requests still return 401.
