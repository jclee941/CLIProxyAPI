# Manager UI Patch

`0001-gemini-web-manager.patch` applies only to CPA Manager Plus v1.12.11,
commit `e1a8788ab796f4d001c5d1e9851c418989b05424` (MIT).

The patch contains Manager frontend source and regression tests. It does not
patch CPA core, the GeminiWeb plugin, its resource HTML, or any OAuth protocol.
The vendor `LICENSE` is unchanged and accompanies the built HTML artifact.

Use [../build-patched.sh](../build-patched.sh) and the commands in
[../PATCH-BUILD.md](../PATCH-BUILD.md). Do not apply it to a shared vendor
checkout or build unrelated dirty root sources into an image.
