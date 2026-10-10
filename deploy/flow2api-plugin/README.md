# Google Flow native plugin

`flow2api.so` serves Google Flow image and video generation through the existing
Gemini `generateContent` API surface, and returns native output for OpenAI Chat,
OpenAI Responses, and Claude callers. It uses an existing Gemini account binding;
it does not keep a second copy of that account's Google cookie jar.

## Models

| Model ID | Output | Notes |
| --- | --- | --- |
| `flow-nano-banana-2` | image | Nano Banana 2.1 |
| `flow-nano-banana-pro` | image | Nano Banana Pro |
| `flow-veo-3.1-fast` | video | Veo 3.1 Fast |
| `flow-veo-3.1-quality` | video | Veo 3.1 Quality |
| `flow-veo-3.1-lite` | video | Veo 3.1 Lite, the only model that accepts `priority: "low"` |
| `flow-omni-1.1-flash` | video | Omni 1.1 Flash, the only model with 10 second clips and 360p |

Each option is checked against the live Flow model catalog and the account's tier
before anything is submitted. An unavailable combination returns
`400 flow_model_options_unsupported` and nothing is charged.

## Generate media

Send an authenticated `POST /v1beta/models/{model}:generateContent`:

```json
{
  "contents": [{
    "role": "user",
    "parts": [{"text": "A yellow paper boat floating on calm blue water."}]
  }],
  "generationConfig": {
    "aspectRatio": "9:16",
    "durationSeconds": 4
  }
}
```

For an image, use an image model and put the ratio in
`{"imageConfig":{"aspectRatio":"9:16"}}`. The combined consumer specification is
available at `/openapi.json`.

Each request is one submission. Never replay a request after an uncertain
submission or connection loss, and never retry `409 flow_session_exchange_busy`
automatically if the first attempt may have been accepted.

## Parameters

Parameters go in `generationConfig`. Unknown keys return
`400 flow_unsupported_generation_option: <key>`. Sampling knobs that chat clients
attach to every request (`temperature`, `topP`, `topK`, `maxOutputTokens`,
`responseModalities`, `thinkingConfig`, `stopSequences`, `presencePenalty`,
`frequencyPenalty`, `responseMimeType`) are accepted and ignored. Flow has no
such controls.

| Parameter | Applies to | Constraint |
| --- | --- | --- |
| `candidateCount` | all | 1 to 4, default 1. All outputs come from one Flow batch. Must be 1 for `upscale`. |
| `seed` | image generation, video upscale | 0 to 2147483647. Rejected for video generation and image upscale (`flow_seed_unsupported`). |
| `aspectRatio` | all | Images: `1:1` (default), `9:16`, `16:9`, `3:4`, `4:3`. Video: `9:16` (default) or `16:9`. |
| `imageConfig.aspectRatio` | images | Same values as `aspectRatio`. A different value in both returns `flow_conflicting_aspect_ratio`. |
| `imageConfig.imageSize` | images | `1K` (default), `2K`, `4K`. `2K` and `4K` generate first, then upscale. Required for image `upscale`. |
| `durationSeconds` | video | 4, 6, 8 (default), or 10 for Omni only. Rejected for video `edit` and `upscale`. |
| `resolution` | video | `360p` (Omni only), `720p` (default), `1080p`, `4K`. `1080p` and `4K` generate at 720p, then upscale. `4K` needs an account tier that allows it. Required for video `upscale`, which accepts `720p`, `1080p`, and `4K`. |
| `flow` | all | Typed object below. Unknown fields are rejected. |

Set `aspectRatio` on a video `upscale` to match the source clip. It defaults to
`9:16`.

### `generationConfig.flow`

| Field | Meaning |
| --- | --- |
| `mode` | `auto` (default), `text`, `references`, `frames`, `edit`, `extend`, `upscale`. |
| `priority` | `normal` (default) or `low`. `low` is for `flow-veo-3.1-lite` only (`flow_priority_unsupported` otherwise). Not valid for images or `upscale`. |
| `projectId` | Existing Flow project of the linked account. Defaults to the account's current project. |
| `modelKey` | Exact Flow model usage key from the live catalog. It must stay inside the requested model's family. |
| `firstFrame`, `lastFrame` | Video only. Media references. |
| `baseImage` | Image only. Media reference. |
| `referenceImages` | Array of media references. |
| `sourceVideo` | Video only. `{mediaId, startFrame?, endFrame?}`, frame numbers are non-negative and `startFrame` must be below `endFrame`. |
| `referenceAudio` | Video only. Array of existing Flow audio media IDs. |
| `referenceLikenesses`, `referenceEntities` | Arrays of Flow likeness or entity IDs. |
| `audioFailurePreference` | Video only. `allow_silent` or `require_audio`. |
| `destination` | `{workflowId?, collectionId?, sceneId?, position?}` with at least one field. `position` is non-negative and requires `sceneId`. Images reject `sceneId`; image upscale rejects the whole destination. Wire-tested; destination placement is not yet live-verified. |
| `structuredPrompt` | `{parts: [...]}`. Each part is `{text}` or `{reference: {...}}` with exactly one of `mediaId`, `likenessId`, `entityId`, or `audioId` (video only). Use it instead of the text prompt: sending both returns `flow_prompt_conflict`. |

A media reference is `{mediaId}` or `{inlineData: {mimeType, data}}`, plus an
optional `cropCoordinates: {top, left, bottom, right}`. Crop edges are normalized
0 to 1, with `top < bottom` and `left < right`. Inline data must be a base64
`image/*`. Images in `contents` parts (`inlineData`) are added as references.
`fileData` is rejected.

Image models accept at most 10 references in total, counting `contents` images,
`referenceImages`, and `baseImage` (`flow_too_many_references`). Video reference
limits come from the live catalog for the model.

### Modes

With `auto`, frames select `frames`, a `baseImage` selects `edit`, any other
reference selects `references`, and no input selects `text`. A `sourceVideo`
never infers a mode: set `edit`, `extend`, or `upscale` yourself
(`flow_mode_required`). A prompt is required except for `upscale`.

| Mode | Requires | Notes |
| --- | --- | --- |
| `text` | prompt | No references. |
| `references` | at least one reference | Images, audio, likenesses, or entities. |
| `frames` | `firstFrame` | Video only. `lastFrame` optional. No other references. |
| `edit` | image: `baseImage`; video: `sourceVideo` | Video edit length is the selected clip, not `durationSeconds`. |
| `extend` | `sourceVideo` | Video only. Extension length is not guaranteed. |
| `upscale` | image: `baseImage` with a `mediaId`, no crop; video: `sourceVideo` without frame range | No prompt text (send an empty text part), references, or `priority`. Image upscale needs `imageSize` 2K or 4K. |

## Media IDs and output references

Every output carries `mediaId` and `projectId`. A `mediaId` is the Flow media UUID
of that output, not a workflow or operation ID and not a Gemini file or
interaction ID. Pass it back as `mediaId` in `baseImage`, `referenceImages`,
`firstFrame`, `lastFrame`, `sourceVideo.mediaId`, or a structured prompt
reference, with the returned `projectId` as `flow.projectId`, to edit, extend,
or upscale a result. Upscales return a new media ID. Treat IDs as opaque strings
(up to 256 characters, no whitespace). They only work on the Flow account that
owns them. A video source that is missing or not ready returns
`400 flow_source_video_unavailable` before any submission.

Uploading video or audio bytes is not supported. Use existing Flow media IDs for
video and audio inputs. Only images can be sent inline.

## Output by protocol

The plugin accepts Gemini-shaped input. Chat, Responses, and Claude callers send
their usual request plus the same `generationConfig` object at the top level;
`n` and `seed` map to `candidateCount` and `seed`. A different value for the same
option returns `flow_conflicting_generation_option`. Usage is never reported
because Flow does not meter tokens.

| Protocol | Image | Video | Output references |
| --- | --- | --- | --- |
| Gemini | `candidates[].content.parts[0].inlineData` | same, `video/mp4` | `parts[0].flow` as `{mediaId, projectId}` |
| OpenAI Chat | `message.images[].image_url.url` data URL | data URL in `message.content` | top-level `flow[]` |
| OpenAI Responses | `image_generation_call` item, base64 in `result` | `output_text` data URL | top-level `flow[]` |
| Claude | text block holding a data URL | text block holding a data URL | top-level `flow[]` |

The top-level `flow[]` entries are `{mediaId, projectId, candidateIndex, partIndex}`.

Streams deliver the finished media, not partial frames:

- Gemini: `:streamGenerateContent?alt=sse` sends one JSON chunk per `data:` line.
- Chat: `chat.completion.chunk` events per choice with the media in `delta`, a
  final chunk with `finish_reason`, then `[DONE]`.
- Responses: the usual `response.created` through `response.completed` events.
- Claude: `message_start`, one text block per media item, `message_delta`,
  `message_stop`.

## Verified live

These cases returned real media from the deployed plugin: images with
`candidateCount` 2, `seed` 42, and `3:4`; image edit with `2K`; image crop and a
structured prompt; a 4K image upscale; video from first and last frames at 4
seconds and 360p; an inline JPEG first frame at 4 seconds and 360p; a video edit
of clip frames 0 to 48, which returned 2 seconds; 720p, 1080p, and 4K video
upscales, including IDs returned by earlier upscales; Veo Lite with
`priority: "low"` and references at 8 seconds and 720p; and a 720p extension,
which returned 7 seconds. All six models have returned media.

## Account configuration

Keep the existing `gemini-web` configuration and add the source account ID to
its `session_exchange_accounts`. Configure this plugin separately:

```yaml
plugins:
  configs:
    gemini-web:
      session_exchange_accounts:
        - gemini-web-ACCOUNT_ID.json
    flow2api:
      enabled: true
      accounts:
        - gemini-web-ACCOUNT_ID.json
      session_broker_url: http://127.0.0.1:8317
      captcha_provider: native
```

Register a `flow2api` auth file through the existing management auth-file API:

```json
{
  "type": "flow2api",
  "id": "flow2api-account.json",
  "label": "Flow account",
  "source_auth_id": "gemini-web-ACCOUNT_ID.json"
}
```

The reference file contains no cookies. Sign into Flow in the source browser
profile before capturing its Gemini session with the updated companion. The
capture includes Flow's host-only session cookies. `MANAGEMENT_PASSWORD` in the
core environment authenticates the local session broker.

## Account status

Both routes are read-only and need management authentication.

`GET /v0/management/plugins/flow2api/accounts` returns the captcha provider, the
Flow models, and one entry per configured account:

| Field | Meaning |
| --- | --- |
| `id`, `label` | Source account ID and its label. |
| `status` | `ready`, `busy`, `expired`, `error`, or `unknown`. |
| `credits` | Current credit balance. |
| `tier` | Account tier, when Flow reports one. |
| `observed_at` | Unix seconds when `credits` and `tier` were read. |
| `project` | Current project ID, when one is known. |
| `error` | Named error for a non-`ready` account. |

Each call reads Flow live. There is no refresh route.

`GET /v0/resource/plugins/flow2api/index` serves the read-only "Google Flow"
dashboard page that shows the same data. On the current Manager, enable
"Remember credential" when logging in, or use the page's masked Manager Admin Key
field. The Manager Admin Key is different from the core management key.

External captcha services can be selected with `captcha_provider: yescaptcha` or
`capsolver` and a protected `captcha_key`; neither is required by the native
provider.

## Build

Run `make check` in this module. Build deployment artifacts on Debian bookworm
and update `flow2api.so.sha256` and `build-manifest.json`. The deployment manifest
loads this plugin using the normal restart convergence procedure.
