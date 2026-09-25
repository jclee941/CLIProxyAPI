# CPA v7.2.159 Host Auth Save Compatibility Patch

Local-only compatibility package for the production CPA source, not the newer
SDK-2 workspace core. No deployment, Compose change, credential access, staging,
commit, or push is part of this package.

## Source And Scope

[source.json](source.json) pins upstream `router-for-me/CLIProxyAPI` tag
`v7.2.159` to commit `ac02da6c05e18f465aa7e3ed5b0a65a2f060917d`.
GitHub's tag API and a fresh clone both resolved to this exact commit.
The source tree is `d3bc409ffe0b7183d65a12ad673ba3cb573e5d12`.

[maintained.patch](maintained.patch) changes only
`internal/pluginhost/auth_callbacks.go` (42 additions, one deletion) and adds
`internal/pluginhost/auth_callbacks_disabled_test.go`. Upstream source and MIT
license remain intact. The workspace's separate uncommitted SDK-2 fix is not
included or edited. Plugin, Manager, watcher, token-store, ABI, schemas, and
native management-enable implementation files are unchanged.

Acceptance: incoming `disabled:true` and an already-disabled canonical runtime
record survive `host.auth.save` in runtime state, status, and persisted JSON;
non-boolean disabled values fail before writing; the unchanged real-core fixture
passes all 71 assertions. Work stops at this local artifact and evidence.

## Root Cause And Fix

At this pin, the actual chain is `callHostAuthSave` -> `saveAuthFile` ->
`buildAuthFromFileData` -> initial file write -> `upsertAuthRecord` ->
`Manager.Register/Update` -> `Manager.persist` -> `FileTokenStore.Save`.

The old builder omitted `Auth.Disabled` and selected active status. The initial
`os.WriteFile` **does preserve an incoming JSON true**. The subsequent
`sdk/auth/filestore.go:126` assignment of `auth.Metadata["disabled"] =
auth.Disabled` changes it to false; the following JSON comparison and write
persist that false value. The fail-first test reproduces this with the real
Manager and FileTokenStore, without replacing either callback or persistence.

The patch parses the canonical, case-sensitive `disabled` boolean at the host
boundary. It projects `Auth.Disabled` and `StatusDisabled`, ORs the incoming flag
with the existing runtime flag found by the existing canonical-ID lookup, and
normalizes persisted JSON to true before the initial write. Raw JSON values are
used for that normalization so unrelated nested metadata and large integer
values are not re-decoded through float64. Existing credential normalization,
weight validation, headers, ID/FileName, and lifecycle fields remain intact.

The existing second lookup in `upsertAuthRecord` also preserves a disabled state
that became visible after the builder's lookup. A deterministic test covers that
interleaving. This is **not CAS or an atomic save/disable transaction**: an
independent update after the final lookup can still race the native Manager's
replace-mode update. No manager lock, rollback, polling, or watcher rewrite is
introduced. Native `PATCH /v0/management/auth-files/status` with
`disabled:false` remains the explicit enable path, unchanged.

## Reproduce The Pinned Build

Prerequisites: Linux amd64, Go 1.26.5, GCC 13.3.0, GNU ld 2.42 (Ubuntu),
glibc 2.39, Bash, Git, jq, rg, and cached dependencies matching the pinned
`go.mod`/`go.sum`. These are the tested CGO toolchain versions, not a claim of
musl compatibility. Source cloning requires GitHub access; Go proxy and checksum
network access are disabled during tests/build. The exact original production
binary is only a read-only provenance reference, not an input to compilation.

From the workspace root:

```bash
bash deploy/gemini-web-plugin/host-compat/build.sh
```

The script creates its own `/tmp/opencode/cpa-host-compat-build-*` checkout,
verifies the commit, tree, and source-archive hash, applies only the test portion
first, and requires all three regression test groups to fail. It then applies
the code patch, checks formatting, runs targeted tests, related race tests and
vet, and builds the binary. The tested packages are pluginhost, SDK auth,
core auth Manager, pluginabi, pluginapi, watcher and its subpackages, and native
management handlers. No full-repository test-suite result is claimed.

The build runs in a sanitized environment with `GOENV=off`, `GOTOOLCHAIN=local`,
`GOFLAGS=-mod=readonly`, `CGO_ENABLED=1`, `GOOS=linux`, `GOARCH=amd64`,
`GOAMD64=v1`, and `CC=gcc`. Its underlying command is:

```bash
go build -trimpath -buildvcs=false \
  -ldflags='-s -w -X main.Version=v7.2.159-compatible-host-auth-save.1 -X main.Commit=ac02da6c05e18f465aa7e3ed5b0a65a2f060917d-local-patched -X main.BuildDate=2026-09-13T00:00:00Z' \
  -o "$work/CLIProxyAPI-host-compat" ./cmd/server
```

The build date is a fixed reproducibility label, not a claimed compilation
timestamp. The version and commit labels explicitly identify the local patch.
Two separate pinned checkouts produced byte-identical binaries (`cmp`, exit 0).
ABI/schema source files are untouched and upstream schema-6 tests passed.

## Real-Core Fixture

The unchanged fixture driver, assertions, and synthetic provider were run twice:

```bash
CPA_QA_CORE_BINARY=/tmp/opencode/cpa-host-compat-build-gIooqp/CLIProxyAPI-host-compat \
  bash /tmp/opencode/gemini-native-session-qa-kJtYJqlA/run.sh
```

Both runs passed **71/71**, including
`disabled_handoff_preserves_ID_and_disabled`. The replacement file retained its
canonical ID/FileName, revision 3, and `disabled:true`. Flash, its alias, Omni,
renewal, persistent restart, real host-save failure/reconcile, and zero local
session 1Password subprocess assertions also passed. No assertion was removed,
relaxed, replaced, or forced true.

Each run used `--network none`, loopback-only synthetic provider/key material,
all capabilities dropped, no-new-privileges, and a read-only container root.
The core was restarted against its own persistent temporary directories; both
core processes were waited and both owned containers removed. No real Google,
1Password, SSH, production configuration, or deployment was used.

[evidence.json](evidence.json) records both original report paths and SHA-256s,
binary/source/patch hashes, and reproducible RED/GREEN logs. The reports exclude
observation-only `data` events when counting the 71 assertions.

## Limits

This patch does not redesign native persistence. Upstream Manager persistence
errors are still ignored at its existing call sites, and unrelated active-record
serialization can still round large numeric metadata when the token store adds
an omitted disabled field. The patch's disabled-state JSON normalization retains
raw unrelated values; it is not a general metadata-serialization rewrite.
Production rollout and non-file storage backends were not exercised.
