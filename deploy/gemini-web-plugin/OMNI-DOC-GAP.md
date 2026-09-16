# Gap analysis against the official Omni documentation

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
| Aspect ratio | live for `16:9` | `9:16` measured 2026-09-15 to produce no video upstream |
| Output resolution `360p/720p/1080p` | refused | no slot in the web request; the product returned 1280x720 unasked |
| Image to video | live | an image attachment is treated as the starting frame without being asked |
| First and last frame interpolation | reachable, undocumented | `<FIRST_FRAME>`/`<LAST_FRAME>` in the prompt; nothing in the plugin blocks it. NOT yet measured here |
| Subject reference | reachable, undocumented | `<IMAGE_REF_N>` in the prompt; images are uploaded in the order sent, which the tags index from 0. NOT yet measured here |
| Tasks parameter (`generation_config.video_config.task`) | refused | the interactions surface takes no `generation_config`; whether the web wire has a task slot is untested |
| Stateful editing across turns | live | `previous_interaction_id`, and the turn runs on whatever account is free |
| Editing an uploaded video | tried, failed | `[# Sources <VIDEO_0>@Video1]` measured to spend the full budget and answer no video |
| Video extension | live | base 10.01s then 20.01s on a different account, measured 2026-09-17 |
| Extending with reference media | partially | the prior video is carried as `<VIDEO_REF_0>`, which extended correctly in the measurement above |
| URI delivery (`delivery:"uri"`) | refused | the download URL opens only with the session cookie, and there is no route here to serve it from |
| Retrieval by id | live | 200 completed with the stored mp4, replayed after a restart |
| Background execution | refused | the web product has no detached job handle |
| Streaming | partial | retrieval streams; the submit is synchronous |

## Where the documentation and this surface disagree on limits

- **Extension duration.** The guide caps an *uploaded* input video at 10 seconds
  and explicitly exempts multi-turn: "unless using multi-turn
  (`previous_interaction_id`)". It names no ceiling for the multi-turn case, so
  the real limit is empirical. Measured here: 10.01s then 20.01s.
- **Payload size.** The guide recommends `delivery="uri"` above 4MB to avoid
  payload limits. This surface returns inline base64 only and has produced an
  8.5MB video, well past that line. The recommendation cannot be followed here.
- **Video references.** The guide limits references to 3 clips of up to 3s each.
  Chaining here attaches one full prior clip, which is longer than that and
  worked; the documented limit describes reference behaviour rather than
  extension.

## What is worth doing, in order

1. **Document the tag vocabulary** on the dashboard. It is reachable now and
   nobody using this surface can know it. Costs nothing upstream.
2. **Measure first/last frame and image references** before documenting them as
   working. Reachable is not the same as verified.
3. **Probe the task parameter** only if a caller needs it; there is no evidence
   the web wire carries one.
4. Leave resolution, URI delivery, background and candidate count refused. Each
   is refused because the product has no slot, not because of a missing wire.

