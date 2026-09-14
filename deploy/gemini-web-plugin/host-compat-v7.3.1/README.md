# CPA v7.3.1 Host Auth Save Compatibility Patch

Local-only, same-release source overlay. No deployment, restart, production
configuration access, Vault access, staging, commit, or push was performed.
This package supersedes neither the running binary nor any existing overlay.
The older `../host-compat/` package was read as a reference and left untouched;
its v7.2.159 binary must not be deployed for this task.

## Pin And Scope

[source.json](source.json) pins upstream `router-for-me/CLIProxyAPI` tag
`v7.3.1` to `44e62bc8acc2f224bff9c62d222717d3f6723dea`, tree
`0b9513a6c0a0c3682064a43a8d3ec62bfde22e3e`. The GitHub tag API and three
fresh clones resolved to this commit. The source archive SHA-256 is verified
on every rebuild. The dirty, newer-schema workspace is never a core build input.

[maintained.patch](maintained.patch) modifies only
`internal/pluginhost/auth_callbacks.go` (42 additions, one deletion) and adds
`internal/pluginhost/auth_callbacks_disabled_test.go` (218 lines). The callback
implementation at this tag is identical to the older reference's base, so the
same narrow semantics and regression tests apply without an ABI shim. The new
patch was generated from the inspected exact-release checkout. License,
dependencies, Manager, token store, watcher, native status endpoint, plugin,
native ABI 1, and RPC schema 6 source are unchanged.

Acceptance: a new `disabled:true` or already-disabled canonical runtime account
survives `host.auth.save` in runtime flag/status and persisted JSON; malformed
disabled types fail before writing; false and omission cannot enable an existing
disabled account; a stale prepared Auth cannot erase a disabled state observed
at upsert. Stop at the verified local artifact and handoff, not deployment.

## Root Cause And Fix

The current path is `callHostAuthSave -> saveAuthFile -> buildAuthFromFileData
-> os.WriteFile -> upsertAuthRecord -> Manager.Register/Update -> persist
-> FileTokenStore.Save`.

The builder creates `StatusActive` without projecting `Auth.Disabled`. The
initial file write retains incoming JSON, but FileTokenStore subsequently sets
`auth.Metadata["disabled"] = auth.Disabled` and can overwrite true with false.
The real deployed binary reproduced exactly this failure, not a plugin ABI
failure: its unchanged fixture passed 70/71 before this patch.

The fix strictly parses the case-sensitive boolean, ORs it with the existing
canonical-ID runtime flag, and projects `StatusDisabled` plus metadata true.
It normalizes the initial physical write to true using `json.RawMessage`,
retaining unrelated nested fields and large integer values. Existing credential
normalization, weight validation, custom headers, canonical ID/FileName, and
lifecycle-field handling are untouched. Non-disabled status behavior remains
upstream behavior. The existing second lookup at upsert also preserves a
disabled state that appeared after the builder lookup. Explicit enablement is
still the unchanged native `PATCH /v0/management/auth-files/status` operation.

## Reproduce

Prerequisites: Linux amd64, Go 1.26.8, GCC 13.3.0, GNU ld 2.42 (Ubuntu), glibc
2.39, Bash, Git, jq, rg, and populated Go module caches for the pinned source.
The script can acquire the pinned Go toolchain through Go's normal verified
toolchain resolution. Clone/toolchain acquisition needs network access; tests
and compilation use a sanitized environment with module network access disabled.
If dependencies are absent, populate them from the pinned isolated checkout,
not by changing the root workspace or the pinned go.mod/go.sum.

```bash
bash deploy/gemini-web-plugin/host-compat-v7.3.1/build.sh
```

Every invocation creates a fresh `/tmp/opencode/cpa-host-v731-build-*` clone,
verifies tag/commit/tree/archive and clean status, applies only the regression
test first, and requires all three test groups to fail. It then applies the
implementation, checks formatting, runs the targeted tests, nine related
package race suites and vet, and builds with:

```bash
go build -trimpath -buildvcs=false \
  -ldflags='-s -w -X main.Version=v7.3.1-compatible-host-auth-save.1 -X main.Commit=44e62bc8acc2f224bff9c62d222717d3f6723dea-local-patched -X main.BuildDate=2026-09-14T00:00:00Z' \
  -o "$work/CLIProxyAPI-host-compat" ./cmd/server
```

The date is a fixed reproducibility label, not a compilation timestamp. Version
and commit labels explicitly disclose the local patch. Two separate maintained
rebuilds produced byte-identical binaries (`cmp`):

```text
/tmp/opencode/cpa-host-v731-build-oHlR9M/CLIProxyAPI-host-compat
/tmp/opencode/cpa-host-v731-build-LewNWZ/CLIProxyAPI-host-compat
SHA256 1cd125f9e1c969c157b77f010e391a17f269bc8fa7a30decf4c9f3336b401a1e
```

Both candidate and captured deployed binary use Go 1.26.8, CGO, Linux amd64/v1,
the same 98 linked dependency version/checksum entries, the same glibc loader,
and a highest required GLIBC symbol version of 2.34. The original compiler/libc
build environment is not recorded; this is not a claim of bit-for-bit upstream
release reproduction. Candidate `-trimpath`, explicit patch labels, and local
linker output intentionally differ. No upgrade or downgrade is embedded.

## Unchanged Real-Core Fixture

The original evidence and input files under
`/tmp/opencode/gemini-actual114-qa-HlUR0IxU` were left intact. The fixture's Go
driver, shell runner, synthetic provider, fake `op`, and C probe were copied
byte-for-byte to `/tmp/opencode/gemini-v731-patched-qa-pVFvWYFA`.

```bash
CPA_QA_CORE_BINARY=/tmp/opencode/cpa-host-v731-build-oHlR9M/CLIProxyAPI-host-compat \
  bash /tmp/opencode/gemini-v731-patched-qa-pVFvWYFA/run.sh
```

Both independently built candidates passed **71/71** without assertion edits.
The current plugin rebuilt to the same SHA-256 as the original failing run:
`c6111b408c34a00b246108d33f69e92bff58d35c9144fcd95d438e29a38470aa`.
Disabled relogin retained ID/FileName and revision 3, persisted `disabled:true`,
returned `saved` with `models_ready:false`, and exposed no account models.
Flash, alias, Omni, usage, renewal, persistent restart, real host-save
failure/reconcile, and zero local-session `op` subprocess assertions passed.

The fixture uses Docker `--network none`, loopback synthetic upstreams, dropped
capabilities, no-new-privileges, and a read-only root filesystem. No real Google,
browser, .220, Vault, or production requests occur. Both core processes in each
run were terminated and waited, the synthetic provider drained, and both owned
containers were removed. [evidence.json](evidence.json) records hashes and logs.
LSP diagnostics were clean for both patched Go files and the maintained JSON
and build script; build-script help/bad arguments and candidate CLI help/bad
flags were exercised non-interactively. No full repository suite is claimed.

## Rollout Boundary And Remaining Limits

The coordinator owns any rollout decision. This package includes no image or
Compose activation. A separately authorized cutover must retain the exact
current runtime image, mounts, data, plugin set, and rollback binary, and verify
the candidate in that runtime before replacing only the core executable.
The local glibc fixture is not a production deployment test. This patch does not
rename accounts, remove production legacy dependencies, or activate local login.

This is not a CAS transaction across host save, native status changes, and the
watcher. A change after the final lookup can still race Manager replace-mode
Update; its generation persistence guard does not serialize the host's initial
direct write. Avoid simultaneous explicit status changes during auth save.
Upstream Manager persistence-error handling and non-file store behavior remain
unchanged; non-file deployments were not exercised. The raw-field preservation
fix is scoped to disabled normalization, not general numeric metadata handling.
