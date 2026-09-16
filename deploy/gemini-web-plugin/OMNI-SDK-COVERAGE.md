# Omni SDK coverage over the web session

What the documented interactions surface offers, what the Gemini web session can
express, and where each item stands. "Live" marks a capability exercised against
the deployed plugin and the real product. "Unexpressible" means the web product
has no slot for it, so the plugin refuses the parameter rather than accepting a
request it cannot honour.

## Input

| Capability | SDK field | Status | Notes |
| --- | --- | --- | --- |
| Text prompt | `input` string / `[{type:text}]` | live | |
| Reference image | `[{type:image,data,mime_type}]` | live | uploaded and referenced from the prompt slot |
| Several references | repeated image blocks | live | two attachments returned in the order sent |
| Document | `inlineData` pdf / txt / csv / md | live | read by the model, not just carried |
| Uploaded reference | `[{type:image,uri}]` | unexpressible | names a Files entry; no Files API on this path |
| Video input | `[{type:video}]` | unexpressible | refused, upload is not a video-edit source |
| Chaining | `previous_interaction_id` | live | account stays pinned across the chain |
| Storage opt-out | `store:false` | live | |
| Background | `background:true` | unexpressible | web has no detached job handle |
| Streaming | `stream:true` | partial | retrieval streams; submit is synchronous |

Accepted attachment types are listed in `webUploadKinds`. Anything else is
refused before the upload, because upstream answers an unsupported file with a
reference error that names neither the file nor the reason.

## Generation options

| Capability | SDK field | Status | Notes |
| --- | --- | --- | --- |
| Aspect ratio | `response_format.aspect_ratio` | live | `16:9` and `9:16`; the wire holds an orientation, not a free ratio |
| Negative prompt | `negativePrompt` | live | folded into the prompt |
| Candidate count | `candidateCount` | unexpressible | the web product returns one candidate |
| Resolution | `response_format.resolution` | unexpressible | no slot; refused |
| Duration | `response_format.duration` | unexpressible | a chip exists in the bundle but the product never sets it |
| Person generation | `person_generation` | unexpressible | server-side policy |
| Seed | `seed` | unexpressible | |

## Output

| Capability | Status | Notes |
| --- | --- | --- |
| Inline MP4 | live | `video/mp4` in `steps[].content[]` |
| URI delivery | unexpressible | `delivery:"inline"` only, no Files API to serve from |
| Serving account | live | `account` on the response |
| Upstream chat id | withheld | deliberate: it is a handle into the live chat |

## Retrieval

| Capability | Status | Notes |
| --- | --- | --- |
| `interactions.get(id)` | live | recovers a pending turn by receipt |
| Operator recovery | live | `POST /v0/management/plugins/gemini-web/recover` |
| Event cursor resume | live | `last_event_id` |
| Cancel | absent | a submitted turn runs to completion |

## Verified

- Two images in one turn: the reply named both colours in the order sent.
- A PDF: the model read the word inside it rather than the file name.
- An unsupported type: refused in 0.7s, before any upload.
- Reference image to video through `POST /v1beta/interactions`: 2.8MB MP4 whose
  first frame carries the reference image's colour field.
