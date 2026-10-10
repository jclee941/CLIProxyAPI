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
| `projectId` | Existing Flow project of the linked account. When omitted, the plugin creates and reuses a `CLIProxyAPI` project for its lifetime. Pass an explicit ID to keep using the same project across restarts. |
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
| `extend` | `sourceVideo` | Veo only in the current Flow catalog. A compatible Omni result can be extended using Veo. Extension length is not guaranteed. |
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

Only images can be sent inline in generation requests. Upload video bytes through
the resumable API below, then pass its returned media ID. The web does not expose
arbitrary audio-file upload; voice previews can be generated through the voice API.

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

The project and media routes below have also run live against the deployed
plugin. A project was created (201), read (200), renamed (200, confirmed by a
follow-up read), found in a paginated list (200), and deleted (204). An existing
web media item was read (200). Its public download answered 200 `video/mp4` with
1,212,262 bytes that began with an `ftyp` box. Trashing it answered 204, the
`archived=true` list showed it (200), and restoring it answered 204.

## Projects and media

These routes manage the Flow account's projects and the media inside them. They
live at the origin root, `/v1/flow/...`, not under `/v1beta`, so their base URL
differs from the generate route. Authenticate with a consumer API key as a
Bearer token, like every other route. The combined specification at
`/openapi.json` carries the same routes with a server override for the root.

One Flow account, the single entry in `accounts`, backs every request. All API
keys see the same projects and media, and nothing is split per key. If the
plugin doesn't have exactly one configured account, these routes answer
`409 flow_single_account_required`. Responses never include cookies or account
credentials.

| Method and path | Result |
| --- | --- |
| `GET /v1/flow/projects` | `200` with `{projects: [{id, title}], nextPageToken?}`. |
| `POST /v1/flow/projects` | `201` with `{id, title}`. |
| `GET /v1/flow/projects/{project}` | `200` with `{id, title}`. |
| `PATCH /v1/flow/projects/{project}` | `200` with `{id, title}`. |
| `DELETE /v1/flow/projects/{project}` | `204`. |
| `GET /v1/flow/projects/{project}/media` | `200` with `{media: [...]}`. |
| `GET /v1/flow/projects/{project}/media/{media}` | `200` with one media item. |
| `DELETE /v1/flow/projects/{project}/media/{media}` | `204`. Moves the item to the trash. |
| `GET /v1/flow/projects/{project}/media/{media}:download` | `200` with the media bytes. |
| `POST /v1/flow/projects/{project}/media/{media}:restore` | `204`. Takes it out of the trash. |

`{project}` is the project UUID, which is the `projectId` that generation
returns. `{media}` is the `mediaId`. A malformed ID answers
`404 flow_route_not_found`.

```bash
curl -sS -X POST https://cliproxy.jclee.me/v1/flow/projects \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"title": "Launch clips"}'
```

### Projects

`POST` and `PATCH` take `{"title": "..."}` and nothing else. The title is
trimmed, must be 1 to 256 characters, and can't hold control characters. An
unknown field or a bad title returns `400 flow_project_request_invalid` or
`400 flow_project_title_invalid`.

The list takes `pageSize` (1 to 100, default 20) and `pageToken`. While more
projects remain, the response carries `nextPageToken`. Send it back as
`pageToken` for the next page. A bad `pageSize` returns
`400 flow_page_size_invalid`, and a `pageToken` over 8192 characters returns
`400 flow_page_token_invalid`.

### Media

A media item is `{id, projectId, workflowId?, title?, type, mimeType?, url?, archived?}`.
`type` is `image`, `video`, `audio`, or `unknown`. `url` is a signed link that
fetches the file without credentials, so treat it as a secret and keep it out of
logs. It's left out while the media isn't ready.

The media list returns every match in one response, with no pagination. These
filters combine:

| Query | Meaning |
| --- | --- |
| `archived` | `false` (default) lists media outside the trash, `true` lists trashed media. Anything else returns `400 flow_archived_filter_invalid`. |
| `type` | `image`, `video`, or `audio`. Anything else returns `400 flow_media_type_invalid`. |
| `search` | Case-insensitive text the title must contain. |
| `collectionId` | Only media in that Flow collection. |

The list fills in `title` and `archived`. Reading a single item omits both
because it does not read the workflow metadata. To find a trashed item,
list with `archived=true`.

`DELETE` is a trash, not an erase. It archives the item's workflow in Flow, and
`:restore` reverses that. An item with no workflow answers
`409 flow_media_workflow_unavailable`.

`:download` returns the bytes with the item's `Content-Type`, or
`application/octet-stream` when none is known, plus `Cache-Control: private,
no-store` and `X-Content-Type-Options: nosniff`. The body is capped at 128 MiB.
While the item has no `url`, it answers `409 flow_media_not_ready`. If you only
need to hand the file to something else, use the signed `url` from a media read
instead of proxying the bytes.

`POST .../media/{media}:purge` permanently deletes only that media version.
It does not delete the entire workflow. This is separate from moving a workflow
to the trash.

### Uploads

- `POST /v1/flow/projects/{project}/media` uploads an image (up to 20 MiB).
  Send `{name, image: {inlineData: {mimeType, data}, cropCoordinates?},
  collectionId?, workflowId?, hidden?}`. `data` is base64.
- `POST /v1/flow/projects/{project}/uploads` starts video upload (up to 1 GiB).
  Send `{name, mimeType, sizeBytes, trimStartMilliseconds?, trimEndMilliseconds?}`.
  Both trim bounds must be supplied together.
- `POST /v1/flow/uploads/{upload}?offset=N&finalize=true` sends raw bytes as
  `application/octet-stream`. Use the returned `chunkSize` for intermediate
  chunks and set `finalize=true` on the last chunk.
- `GET /v1/flow/uploads/{upload}` reads the accepted offset or the final media
  receipt. Query after a lost response instead of resending a chunk.
- `DELETE /v1/flow/uploads/{upload}` cancels the upstream upload session.

The upload ID is opaque and requires the same account configuration and a valid
CPA API key. It survives plugin restarts, but not a broker-credential change.
Upload completion does not promise that Google's subsequent media processing
has finished.

### Collections, scenes and characters

All paths in this table start with `/v1/flow/projects/{project}`. The complete
request and response fields are in `/openapi.json`.

| Resource | Operations |
| --- | --- |
| `/collections` | List/create; get/update/trash by ID; `:restore`, `:purge`, `:addItems`, `:removeItems`. |
| `/workflows` | List/get/update/trash; `:restore`, `:purge`, `:copy`, `:trim`; `workflows:batchArchive`. |
| `/scenes` | List/create/get/update/trash; `:restore`, `:purge`, `:copy`. |
| `/scenes/{scene}/clips` | List/add; update/delete by position; `clips:reorder`. |
| `/entities` | List/create/get/update/trash characters; `:restore`, `:purge`, `:copy`. |
| `/entities/{entity}/images` | Copy an existing image to a reference slot; delete a slot by index. |
| `/voices` | List presets and saved voices; `voices:preview` generates audio; `{voice}:save` saves an existing preview. |

Collection and workflow updates expose names, favorite/trash flags, membership
and primary-media selection where the web supports them. Character updates
expose personality notes and voice references. Preset voice references use
`voices/<name>`; generated audio uses its returned media ID.

Voice previews use native 24 kHz mono PCM. Request
`GET .../media/{media}:download?format=wav` for a WAV playback container.

### Agent sessions and tools

- `/v1/flow/projects/{project}/sessions`: create/list sessions, get history,
  rename or delete a session.
- `POST .../sessions/{session}:chat`: send `prompt` or `structuredPrompt`,
  plus an optional `collectionId`. Provider tool calls are already executed
  at Google; returned events must not be executed again by the consumer.
- `POST .../sessions/{session}:cancel` requests cancellation of the current
  session turn.
- `/v1/flow/tools`: list account tools or run the builder with
  `{projectId, prompt|structuredPrompt, requestId?}`.
- `/v1/flow/tools/{tool}`: get/update/delete, `:edit`, `:copy`, favorites,
  sharing, version history, source files and version restoration.
- `/v1/flow/shared-tools`: saved shared links, source retrieval, forking and
  favorites. Deleting an owned tool invalidates its sharing links. The current
  web transport rejects standalone link revocation, so it is not advertised.
- `POST /v1/flow/requests/{request}:cancel`: best-effort cancellation of an
  active broker-owned request. Supply a unique `requestId` when starting a
  tool build if you need to cancel before it returns.
- `POST /v1/flow/projects/{project}/text:generate`: Flow SDK text generation,
  with system instruction, thinking level and inline image/video/audio parts.

Agent and builder HTTP responses are buffered until the upstream turn ends.
History and version reads provide recovery after an uncertain response; do not
repeat the mutation to retrieve its result. Cancellation stops the active
upstream connection when found, not an already committed resource change.

The tool builder returns the real source files. The generated React UI runs in
a browser; the API does not claim to execute arbitrary UI interactions on the
server.

### Personal likeness

`/v1/flow/likenesses` lists the account's registered likenesses and supports
retrieval/deletion by ID. `likenesses:eligibility` checks eligibility.
`POST /v1/flow/likenesses/registrations` returns Google's verification URL and
token; `GET .../registrations/{token}` reads its state. Registration still
requires the account holder to complete Google's verification. An API response
that issues the link does not mean registration is complete.

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
