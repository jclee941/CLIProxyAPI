# Pinned Plus Compatibility Build

Deployment artifacts only for `jc01rho/CLIProxyAPIPlus` at
`7f75ba94917647164807028fdd6d988d5aebd744` (`v7.2.157-4`).
These artifacts reproduce the `v7.2.157-4-webplugins.2` image-response-wait fix
and deployment target. Production remains on `.1` until a separately authorized
cutover. They do not modify the main source tree or automatically replace production. `compose.yml`
is included by the existing operator wrapper alongside its original service and
Gemini plugin overrides; it changes only the Core image.

## Contents and Scope

- `SOURCE`: exact source, toolchain, build metadata, runtime identity, and patch hashes.
- `antigravity.patch`: restore project lookup, request-token accounting, outcome
  recording, session recovery, and stream-error handling; includes the new
  `internal/runtime/executor/helps/antigravity_stream.go` and empty-stream test update.
- `nonantigravity-fixes.patch`: Claude legacy header identity handling, unnamed
  Antigravity tool-call translation, Kiro role/merge ordering, and reviewed private
  buffer writes; includes the new
  `internal/runtime/executor/claude_executor_legacy_override_test.go`.
- `image-response-wait.patch`: image-only response waiting in the OpenAI-compatible
  executor, with a separate HTTP client/connection pool and three regression test files.
- `Dockerfile`: replace only `/CLIProxyAPI/CLIProxyAPI` in the original runtime.
- `compose.yml`: the `.2` deployment-target image override, without replacing mounts,
  provider settings, account storage, or adjacent services.
- `SHA256SUMS`: integrity checks for the packaging files and all three patches.

The three patches reproduce the isolated candidate's fifteen source/test files
(nine modified, six new). The first two patches are unchanged. The third modifies
only the two image client call sites in `openai_compat_executor.go` and adds
`helps/proxy_image_helpers.go`, `helps/proxy_image_helpers_test.go`,
`helps/proxy_image_http_test.go`, and `openai_compat_images_timeout_test.go` under
`internal/runtime/executor/`. Inspection probes, temporary evidence Go files, binaries,
credentials, and ABI shim changes are excluded. Native C ABI **1** and RPC schema
**6** remain unchanged. `CGO_ENABLED=1` is required for native plugin loading.

The unpatched candidate had 12 suite failures; the combined isolated candidate
was reported passing `go test -count=1 ./...` before packaging. Packaging checks
reapply these exact patches to the pinned clean source; they do not replace the
full tests or runtime preflight below.

## Image-Only Response-Wait Contract

Only image generation/edit execution, both streaming and non-streaming, uses
`NewProxyAwareImageHTTPClient`. Its separate cached transports add no active
response-header or body-read timeout and no total client timeout. Requests still
honor caller cancellation. Default, direct, proxy, and context-provided transport
selection, connection setup settings, and TCP pool reuse are preserved; caller
transports and the ordinary client cache are not mutated. Unknown middleware is
retained rather than bypassed.

The generic `proxy_helpers.go` and its tests remain byte-for-byte identical to
the pinned source. Ordinary HTTP behavior retains its **45-second** response-header
timeout and **five-minute** body-read idle timeout. No global timeout increase,
retry configuration change, plugin change, or provider/configuration change is
part of this patch.

The final isolated source was reported passing Go 1.26.6 `go test -count=1 ./...`
and a CGO-enabled build. The operator's local HTTP fixture comparison recorded
the old image request failing at 45.029 seconds with HTTP 504, the fixed image
request returning PNG/HTTP 200 at 50.012 seconds, ordinary chat still failing at
45.013 seconds with HTTP 504, and a fast image returning HTTP 200 at 0.003 seconds.
Each case reached the fixture upstream once; no real Google image generation was
used. These are isolated candidate results, not a `.2` production deployment claim.

## Rebuild from Clean Source

Run in Bash from this directory. Use an isolated Linux builder with **Go 1.26.6**,
a C compiler, and libc development headers compatible with the original runtime
and the existing native plugins. Match the target architecture; do not substitute
a CGO-disabled release binary. A different compiler/libc can change the resulting
binary, so these are pinned source/build instructions, not a bit-for-bit binary
reproducibility claim. The initial clone and dependency population need network
access unless a local mirror and module/toolchain caches are already available.

```bash
set -euo pipefail
ARTIFACTS="$PWD"
sha256sum -c SHA256SUMS
. "$ARTIFACTS/SOURCE"
WORK="$(mktemp -d)"
export GIT_MASTER=1
git clone --no-checkout "$SOURCE_REPOSITORY" "$WORK/source"
git -C "$WORK/source" checkout --detach "$SOURCE_COMMIT"
test "$(git -C "$WORK/source" rev-parse HEAD)" = "$SOURCE_COMMIT"
test "$(git -C "$WORK/source" rev-list -n 1 "$SOURCE_TAG")" = "$SOURCE_COMMIT"
for PATCH in "$PATCH_1" "$PATCH_2" "$PATCH_3"; do
  git -C "$WORK/source" apply -p1 --check "$ARTIFACTS/$PATCH"
  git -C "$WORK/source" apply -p1 "$ARTIFACTS/$PATCH"
done

export GOTOOLCHAIN=go1.26.6 CGO_ENABLED GOOS
test "$(go env GOVERSION)" = "go$GO_VERSION"
go -C "$WORK/source" mod download
go -C "$WORK/source" test -count=1 ./...
mkdir "$WORK/image"
go -C "$WORK/source" build -buildvcs=false \
  -ldflags="-s -w -X main.Version=$VERSION -X main.Commit=$COMMIT -X main.BuildDate=$BUILD_DATE" \
  -o "$WORK/image/CLIProxyAPIPlus" ./cmd/server
go version -m "$WORK/image/CLIProxyAPIPlus"
sha256sum "$WORK/image/CLIProxyAPIPlus"
```

All patch prefixes (`a/b`, `i/w`, and `1/2`) intentionally remain as captured;
`git apply -p1` strips exactly one component. Do not add inspection programs to
the source tree before running `./...`. Keep the binary and build logs outside
this repository; record the binary hash with the operator's release evidence.

## Original-Runtime Overlay

The already-created local tag `cliproxyapi:web-base-09a29bd` must resolve to
`sha256:7ad14e95aa5347325a0f727cb9be30673f619369624b9fc89673da60736f7cb5`.
This is the original local image ID, not an invented registry manifest digest.
Keep that image available for rollback. Continue in the same shell, on the
builder that holds the original image:

```bash
test "$(docker image inspect --format '{{.Id}}' "$BASE_IMAGE")" = "$ORIGINAL_IMAGE_ID"
test "$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$BASE_IMAGE")" = \
  "$GOOS/$(go env GOARCH)"
docker build --pull=false --build-arg BASE_IMAGE="$BASE_IMAGE" \
  -f "$ARTIFACTS/Dockerfile" -t "$IMAGE" "$WORK/image"
```

The build context contains only the new executable. The overlay retains the
original runtime's libc, utilities, entrypoint, command, and other image settings.
Existing plugins remain supplied by the operator's existing Compose bind mount;
do not bake them, configuration, auth files, or secrets into the image. Do not
switch to an unknown Plus release image that may lack native plugin support.

## Manual Preflight and Rollback

Full tests do not prove compatibility with the real plugins or configuration.
Before a separately authorized deployment, use the existing Compose wrapper and
an isolated preflight to verify plugin loading (native ABI 1 / RPC schema 6),
configuration parsing, model routes, and streaming/non-streaming requests with
the intended plugin/configuration set. Preserve current behavior and stop on a
regression rather than adding ABI shims or changing production configuration.

The Plus fork already allows HTTP **400** to activate configured model fallback
chains, unlike upstream behavior. This is an inherited fork behavior difference,
not a new feature introduced by these patches. Manually validate the intended
400 fallback and request-error behavior before cutover.

For rollback, select the retained original image identified above through the
**existing Compose wrapper** and recreate only the affected Core service using
the same Compose project and mounts. Preserve plugins, auths, configuration,
volumes, and databases. Do not run `down -v`, prune rollback images, delete a
database, or reset data. No production deployment or rollback is executed by
this package.

The earlier `.1` deployed core passed the combined full suite after the first two compatibility patches.
Real preflight loaded both the original `structured-output.so` and the independent
`gemini-web.so`. The immediate pre-cutover runtime config was backed up encrypted
in 1Password; all its existing settings and 11 auth records survived. Plus adds
default alias entries for previously unused Copilot and Kiro providers. Existing
ChatGPT2API model mappings were also reconciled into the source YAML so restarts
do not replace a runtime-only 27-model list with the older three-model source.
