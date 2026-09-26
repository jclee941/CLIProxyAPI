# Gap analysis against the official Omni documentation

Historical gap analysis (2026-09-16). The current supported API and its
prerequisites are defined in [openapi.json](openapi.json), published at
https://cliproxy.jclee.me/openapi.json. Keep this document as the earlier
investigation record.

Source: the published Gemini Omni guide (local copy taken 2026-09-16, 69KB).
Compared against this plugin as deployed, and against OMNI-SDK-COVERAGE.md,
which already records what the web session can and cannot express.

## The finding that decides most of the list

The documentation's media roles are **prompt syntax, not API fields**. First
frame, last frame, subject reference, edit source and previous-turn extension
are all expressed by writing a tag into the prompt:

    <FIRST_FRAME> <LAST_FRAME> <IMAGE_REF_N> <VIDEO_REF_N>
    [# Sources <FIRST_FRAME>@Image1 <LAST_FRAME>@Image2]
    [# Sources <VIDEO_0>@Video1]        edit this uploaded video
    [# Sources <PREVIOUS_VIDEO>@Video1] extend the previous turn
    [# References <IMAGE_REF_0>@Image1 <VIDEO_REF_0>@Video1]

This plugin already injects one of them - `[# References <VIDEO_REF_0>@Video1]`
for a video attachment - and leaves a caller who wrote their own declaration
alone. For image attachments it injects nothing at all, so a prompt a caller
writes reaches the model verbatim.

**Consequence: first frame, last frame, looping, image references and multi-role
prompts are reachable on this surface today, by a caller who writes the tag.**
They are not refused, not stripped, and not mapped - they are simply undocumented
here. That is the largest gap, and it is a documentation gap rather than a wiring
one.

## Feature by feature

| Documented feature | On this surface | Evidence |
| --- | --- | --- |
| Text to video | live | measured repeatedly, 60-99s, 1.8-6.5MB |
| Aspect ratio | live for both | `9:16` returned 720x1280 in 72.4s on 2026-09-21, where 2026-09-15 saw no video; an unnamed ratio is now framed `9:16` |
| Output resolution `360p/720p/1080p` | refused | no slot in the web request; the product returned 1280x720 unasked |
| Image to video | live | an image attachment is treated as the starting frame without being asked |
| First and last frame interpolation | reachable, undocumented | `<FIRST_FRAME>`/`<LAST_FRAME>` in the prompt; nothing in the plugin blocks it. NOT yet measured here |
| Subject reference | reachable, undocumented | `<IMAGE_REF_N>` in the prompt; images are uploaded in the order sent, which the tags index from 0. NOT yet measured here |
| Tasks parameter (`generation_config.video_config.task`) | live for `extend` | the wire has no task slot, so `extend` is carried as the product's own `[# Sources <PREVIOUS_VIDEO>@Video1]` declaration; other tasks stay refused |
| Stateful editing across turns | live | `previous_interaction_id` stays on the original account and conversation while it can serve; a blocked owner hands the stored video to another account, which extends it (10.005s -> 20.010s, measured 2026-09-27) |
| Editing an uploaded video | tried, failed | `[# Sources <VIDEO_0>@Video1]` measured to spend the full budget and answer no video |
| Extending an uploaded video | live | `[# Sources <VIDEO_0>@Video1]` with `task=extend`: 10.005s -> 20.000s cumulative, measured 2026-09-27; `[# References <VIDEO_REF_0>@Video1]` instead makes a new 10s clip |
| Video extension | live | declared in its own conversation: 10.005s -> 20.010s -> 30.016s -> 40.000s, four for four, measured 2026-09-21 |
| Cross-account attachment continuation | removed | prior clips are no longer uploaded to another account as a fallback |
| URI delivery (`delivery:"uri"`) | refused | the download URL opens only with the session cookie, and there is no route here to serve it from |
| Retrieval by id | live | 200 completed with the stored mp4, replayed after a restart |
| Background execution | refused | the web product has no detached job handle |
| Streaming | partial | retrieval streams; the submit is synchronous |

## Where the documentation and this surface disagree on limits

- **Extension duration.** The guide caps an *uploaded* input video at 10 seconds
  and explicitly exempts multi-turn: "unless using multi-turn
  (`previous_interaction_id`)". It names no ceiling for the multi-turn case, so
  the real limit is empirical. Measured here with the declaration written into
  the prompt: 10.005s -> 20.010s -> 30.016s -> 40.000s, ten seconds a turn, four
  turns out of four. Testing ended at 40s; no longer extension was attempted,
  so this does not establish a web-session ceiling or guaranteed success.
- **Payload size.** The guide recommends `delivery="uri"` above 4MB to avoid
  payload limits. This surface returns inline base64 only and has produced an
  8.5MB video, well past that line. The recommendation cannot be followed here.
- **Video references.** Historical cross-account attachment experiments did
  not establish reliable continuation. That fallback has been removed; normal
  continuation uses the original account and conversation.

## What is worth doing, in order

1. **Document the tag vocabulary** on the dashboard. It is reachable now and
   nobody using this surface can know it. Costs nothing upstream.
2. **Measure first/last frame and image references** before documenting them as
   working. Reachable is not the same as verified.
3. **Probe the task parameter** only if a caller needs it; there is no evidence
   the web wire carries one.
4. Leave resolution, URI delivery, background and candidate count refused. Each
   is refused because the product has no slot, not because of a missing wire.

