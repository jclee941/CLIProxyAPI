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
| Video input | `[{type:video,data}]` or `{type:video,uri}` | live | inline bytes or a Drive file; the SDK takes video only through Files |

An attached video also has to be declared. The video tool treats an image as a
starting frame without being asked, but a video is ambiguous between an edit
source, an extension target and a reference, and an undeclared one is uploaded,
accepted and then ignored - the generation answers `no_video_generated`. The
plugin states `[# References <VIDEO_REF_0>@Video1]` for a video attachment,
which is the syntax the omni prompt guide documents, and leaves a caller who
wrote their own declaration alone.
| Audio input | - | not in the SDK | the docs list uploading audio references as unsupported |

The SDK accepts video input only as `{"type":"video","uri":...}`, resolved through
the Files API; no inline video input form is documented. The web session has no
Files API, but it does take a video as an ordinary attachment and read it: a
three second clip attached to a text turn was described correctly. So video input
works here by a different route than the SDK's, and only for callers who send the
bytes inline.
| Chaining | `previous_interaction_id` | live | prior video attached; follow-up may use another account |
| Storage opt-out | `store:false` | live | |
| Background | `background:true` | unexpressible | web has no detached job handle |
| Streaming | `stream:true` | partial | retrieval streams; submit is synchronous |

Accepted attachment types are listed in `webUploadKinds`. Anything else is
refused before the upload, because upstream answers an unsupported file with a
reference error that names neither the file nor the reason.

### Attachments named by uri

A part may name a Google Drive file instead of carrying bytes, through
`fileData.fileUri` on the Gemini path or `{"type":"image","uri":...}` on
interactions. The share link, the `open`/`uc` query links and a bare
`drive:<id>` all resolve. The plugin fetches the file and uploads it like any
other attachment, so the outbound host is the Drive API alone rather than
whatever a caller pastes.

An API key reaches only files shared with anyone holding the link. With an OAuth
client configured the fetch carries a bearer token instead and reaches the
operator's own private files; credentials come from the plugin configuration
first and the environment second, because the configuration is hot reloaded and
the environment is not.

Drive types its files by extension, so its metadata is checked against the same
allowlist as an inline attachment: a `.mts` config file it reports as
`video/mp2t` is refused rather than uploaded as a video.

A fetched file streams from Drive straight into the upload: metadata supplies
the type and the length the resumable protocol must declare, and the bytes are
opened only once the upload is ready for them, so memory does not track file
size. An attachment the caller inlined is decoded up front instead, because
those bytes arrived with the request anyway and a malformed encoding should be
the caller's error rather than a transfer that dies halfway.

The bound on one attachment is therefore a bound on one transfer, not on the
process. A file Drive reports no length for, such as a folder or a Google Doc,
is refused because the upload has to declare a length before it may send
anything.

### Inline media spellings

A request reaches the plugin in whichever spelling the host bridge produced, and
all of them are valid Gemini REST:

| Caller | Spelling |
| --- | --- |
| Gemini | `inlineData` / `mimeType` |
| OpenAI chat completions | `inlineData` / `mime_type`, plus a sibling `thoughtSignature` |
| OpenAI responses | `inline_data` / `mime_type` |
| Claude messages | `inline_data` / `mime_type` |

All four are read. Accepting only the first meant an OpenAI image was refused as
malformed and a Claude image was dropped in silence, so the caller paid for a
turn in which the model never saw the file.

## Generation options

| Capability | SDK field | Status | Notes |
| --- | --- | --- | --- |
| Aspect ratio | `response_format.aspect_ratio` | live | `16:9` and `9:16`; the wire holds an orientation, not a free ratio |
| Negative prompt | `negativePrompt` | live, extra | the SDK has no such field and tells you to write negatives into the prompt, which is exactly what this does |
| Candidate count | `candidateCount` | unexpressible | the web product returns one candidate |
| Resolution | `response_format.resolution` | gap | the SDK offers 360p/720p/1080p; the web product exposes no slot, so it is refused |
| Duration | `response_format.duration` | unexpressible | a chip exists in the bundle but the product never sets it |
| Seed, temperature, top_p, stop | - | not in the SDK | the docs list these, and system instructions, as unsupported for this model |

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
