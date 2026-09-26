# Gemini Web .114 Local Cutover

## Current Status: Production Local, Omni Renewal Blocked

Production CPA now uses all five original Gemini IDs with reference-only local
auth and the same-version patched core/plugin. All five original disabled flags
were `false` and were restored through the canonical status API. Original labels
were replaced by the actual authenticated account names:

| Auth ID | Account Name | Latest State |
| --- | --- | --- |
| `gemini-web-1c2f50176f190c7cecc246cb6cc86e33.json` | 김민 | `needs_operator` after the Omni renewal failure |
| `gemini-web-76a618ccab54cf1923743e283accb389.json` | sj xndm | ready |
| `gemini-web-83fa747683343148070a7386bbe73e11.json` | Sksjcjvv | ready after explicit new-intent reauthentication |
| `gemini-web-a7a8abe7b64ba5d2256eb799e9902882.json` | djjd jddjdj | ready |
| `gemini-web-profile-1.json` | 이재철 | ready |

All five were PRO/ready with two per-account models after cutover and after a
fresh production process restart. All five explicit local HTTP maintenance calls
returned ready. The one real native Flash request returned HTTP 200 and exactly
`OK` from the local-only Gemini pool.

The **single** native Omni request returned HTTP 409 with
`gemini_web_omni:session_renewal_outcome_unknown_requires_operator`. It failed in
the pre-generation renewal path; no video artifact was returned. The selected
first account is now fenced by the encrypted session's uncertain renewal state.
Its existing login `status` and `reconcile` both return `error/needs_operator`.
The current plugin also rejects replacement of a local session that is still
renewing; ordinary completed-login reauthentication cannot clear this state.
No repeat video request, cookie-renewal retry, state-file edit, or stale-store
restoration was performed. **Five accounts are registered locally; only four
remain ready. This is not a claim of five working accounts or successful video.**

### Core-only startup

Install this directory's `cliproxy-compose` at `/usr/local/sbin/cliproxy-compose`.
It uses only `/opt/dashboard/docker-compose.dashboard-only.yml`, matching the
running CPA container. That Compose project contains CPA and Postgres, not
ChatGPT2API or Gemini sidecars. Configuration, environment, and the session key
are already supplied by its protected local mounts. The wrapper does not fetch
secrets or prepare sidecar files.

```sh
python3 -B deploy/gemini-web/test_startup.py -v
/usr/local/sbin/cliproxy-compose config --services
```

### Applied Production Layout

- Compose: `/opt/dashboard/docker-compose.dashboard-only.yml`, project
  `dashboard`, service `cliproxyapi`; image pinned to the exact original image ID.
- Persistent core/config/store: `/opt/dashboard/gemini-web/`.
- Encrypted store: `/opt/dashboard/gemini-web/sessions`, `0700`; files `0600`.
- Key: `/etc/cliproxy/gemini-web/session.key`, `0600` in a `0700` directory.
- Existing non-external-secret runtime environment:
  `/etc/cliproxy/gemini-web/core.env`, `0600`.
- `start-production.sh` reads the mounted key at every process startup. Production
  retains its normal PATH. The `op` bind mount and `OP_SERVICE_ACCOUNT_TOKEN`
  were removed; other runtime environment values are byte-for-byte preserved.
- Only the five Gemini auth files and Gemini plugin were replaced. Other auth
  file hashes, both unrelated plugin hashes, core ports/networks/image, and all
  other container PIDs were verified unchanged. VNC/browser and CPA are healthy;
  both Cloudflare service PIDs are unchanged.
- No maintenance or reauth timer runs. The plugin rotates each session's cookies
  itself; the timers and their scripts were removed from the host on 2026-09-20
  and from this directory on 2026-09-27.
- `gemini-web-importer` remains stopped. Do not resume it: its copied sessions and
  auths are now historical and production owns the current session lineage.

The preserved old `.114` Profile 9 still needs manual Google login, but it is a
duplicate of a migrated identity, not a missing sixth unique CPA account.

### Private Evidence And Recovery

Fresh production rollback snapshot:
`/root/.local/state/gemini-web-production-backup-296ca68a091f` (`0700`). It contains
the exact original core, Gemini plugin, five auth files, Compose/live/supplemental
configs, runtime/container snapshots, maintenance state and other-auth hashes.

Sanitized receipts under `/root/.local/state/gemini-web-importer/`:
`precutover-ready.json`, `production-gemini-web-flash.json`,
`production-gemini-web-omni.json`, `production-after-omni.json`,
`production-omni-reconciliation.json`, and `production-preservation.json`.
The new Default intent is
`receipts/reauth-c7877768edb8a96a8cf6839fa7b74174.json`; the earlier completed
receipt was retained byte-for-byte. No signed URLs or session tokens are included
in the public receipts.

Default expired again during the later QA window. Its completed operation was
reauthenticated through a second explicit production intent, without changing
the uncertain first account:
`receipts/reauth-production-790577eec6f9b02566ffe2fe3c783a54.json`.
That operation returned `ready/models_ready:true`. This recurrence demonstrates
why the coordinator's upcoming browser-cookie synchronization work is necessary;
HTTP renewal alone is not claimed to track every live browser rotation.

Normal core recreation uses only the applied Compose file and local bootstrap:

```sh
docker compose -p dashboard -f /opt/dashboard/docker-compose.dashboard-only.yml \
  up -d --no-deps --pull never cliproxyapi
```

Do not restart while any credential/generation operation is unresolved merely
to clear a fence. The persistence restart recorded above happened before Omni.

Rollback is an explicit legacy restoration, **not** recovery of the uncertain
local credential. It would reintroduce the old external dependencies and was not
executed. Preserve the current encrypted store/key and every receipt first. Stop
the core, then restore only these snapshot paths:

```sh
backup=/root/.local/state/gemini-web-production-backup-296ca68a091f
docker stop cliproxyapi
cp "$backup/gemini-web.so" /opt/dashboard/plugins/gemini-web.so
cp "$backup/runtime-config.yaml" /run/cliproxy/config.yaml
cp "$backup/config.local.yaml" /opt/dashboard/config.local.yaml
cp "$backup/rollback-compose.yml" /opt/dashboard/docker-compose.dashboard-only.yml
cp "$backup"/auths/gemini-web-*.json /opt/dashboard/auths/
docker compose -p dashboard -f /opt/dashboard/docker-compose.dashboard-only.yml \
  up -d --no-deps --pull never cliproxyapi
```

The private `rollback-compose.yml` already pins the snapshot image and loads the
captured environment from the root-only `rollback-core.env`, without resolving
new secrets. The unmodified original Compose is also retained as `compose.yml`.
Restore the five original flags exactly. Never overwrite other providers' auth files, delete
uncertain session records, or start both cores against the current local store.

## Historical Staging Preparation

An independently bootstrapped `gemini-web-importer` container is intentionally
running on `.114`, published only at `127.0.0.1:18319`. It has no public route.
It uses the exact image and candidate hashes below, the real sidecar through
`cliproxyapi_backend`, and no production auth/config/plugin mounts. The existing
production core, browser, other services, and maintenance timer are unchanged.

The stage contains disabled copies of all five original Gemini auth files. Their
original bytes, flags, reference/identity/profile bindings, source configs, core
snapshot, and Docker runtime snapshot are in its private `originals/` directory.
That directory is **not mounted** into the staged container.

### Private Paths On .114

| Purpose | Path |
| --- | --- |
| Stage root, mode `0700` | `/root/.local/state/gemini-web-importer` |
| Key directory, mode `0700` | `/etc/cliproxy/gemini-web-importer` |
| Canonical-base64 32-byte key, mode `0600` | key directory + `/session.key` |
| Independent management credential, mode `0600` | key directory + `/management.key` |
| Independent generation credential, mode `0600` | key directory + `/api.key` |
| Encrypted persistent store, mode `0700` | stage root + `/sessions` |
| Original identity/state mappings, mode `0600` | stage root + `/originals/mappings.json` |
| Fresh capture intake directory, mode `0700` | stage root + `/intake` |
| Durable operation receipts, mode `0600` | stage root + `/receipts/*.json` |

`start-stage.sh` reads the two bootstrap credentials into the core process
environment at every start. Keys never appear in Docker arguments or inspect
environment values. Docker restart policy is `unless-stopped`; the secret-free
`recreate-command.json` in the stage root records the exact recreation arguments.
Recreation requires removing only the stopped stage container first. Do not rerun
`prepare-stage.py` over an existing stage: it deliberately refuses that operation.

The image has no `op` executable, the actual core has no external-service-account
environment variable, and its `PATH` points to the mounted empty directory
`/stage/empty-bin`. No helper, external token, original snapshot, or browser
profile is mounted. Full plugin account listing does attempt legacy credential
resolution even for disabled accounts; it fails closed at executable lookup.
It cannot launch the external helper, read old tokens, or contact Vault. This
behavior is explicitly not represented as a ready/linked-account result.

### Readiness And Secure Operator Channel

Use SSH and run the helper on the host. It supplies the management credential
through curl configuration on stdin, never argv. It sends the exact Origin
`https://cliproxy.jclee.me` and the real extension ID
`jkomnpgdokmnojgbapacnnkakgnbekjn`. No browser capture or extension attestation is
claimed. The previous stale handoff is not an input.

```sh
ssh -o BatchMode=yes -o ConnectTimeout=5 192.168.50.114 \
  'python3 /root/.local/state/gemini-web-importer/import-stage.py probe'
```

The live probe verifies management authentication, full account listing, five
disabled staging records, an empty Gemini model registry, and synthetic
start/cancel with the exact Origin/extension. It does not complete a fake login.
`readiness.json` in the private stage root records the observed checks: missing
management authentication returned 401, wrong Origin returned 403, missing
consent returned 400, and a malformed bundle was rejected before login without
creating a receipt. The invalid test file was removed. A stage-only restart
retained the bootstrap key and passed the readiness probe again. No session
records exist yet. The legacy listing's expected error is
`secret_store_unavailable`, not a linked-account success.

**Important discovered boundary:** all five current source bindings omit
`auth_user`. Existing-ID login therefore correctly returns 409 until a verified
fresh bundle supplies that index. No zero/default index was invented. The probe
checks this failure and, after binding, checks existing-ID start/cancel instead.

### Fresh Bundle Contract

The capture worker writes one root-owned `0600` JSON file directly into `intake/`.
Never send its contents through chat, shared logs, a URL, or command arguments.
The object has `version: 1`, an independently generated 32-lowercase-hex
`bundle_id`, `consent: true`, and an `accounts` array. Each entry contains:

- `existing_id`: exact old auth filename selected by the private verified digest
  mapping, or an empty string only for a genuinely new identity.
- `label`: actual authenticated account label, at most 200 characters.
- `token`: freshly captured opaque session token, never the previous handoff.
- `account_sha256` and nonnegative integer `auth_user`: verified identity/index.
- `validated: true` and `consent: true`: the worker's verified capture receipt.

Use one stable bundle ID and account order for an intake attempt. Existing
receipts prevent resubmission; do not change bundle IDs to bypass them. A new
identity cannot reuse an old ID. An old digest cannot be imported with an empty
ID. The plugin independently checks token, index, and sidecar identity/models
before saving. A changed account index must be investigated, not silently rebound.

With no other stage callers active, run these separately, checking each result:

```sh
python3 /root/.local/state/gemini-web-importer/import-stage.py bind-indices \
  /root/.local/state/gemini-web-importer/intake/fresh-bundle.json
python3 /root/.local/state/gemini-web-importer/import-stage.py probe
python3 /root/.local/state/gemini-web-importer/import-stage.py import \
  /root/.local/state/gemini-web-importer/intake/fresh-bundle.json
```

`bind-indices` preserves the original mappings and binds only previously missing
stage indices whose digests match. It stops/starts **only the stage** to avoid
configuration-watcher ambiguity. It refuses uncertain prior receipt states.
The importer serializes its operations using `operator.lock`. Do not bypass it
with concurrent status/save calls. A transport failure is an unknown outcome;
inspect the private receipt and use existing `status`/`reconcile` operations,
never resubmit `complete` blindly. Import leaves old-ID accounts disabled in
staging, while a truly new account retains the native new-account default.
The `probe` command is specifically a pre-import readiness check, not a general
post-import account readiness check; use per-receipt status after importing.

```sh
python3 /root/.local/state/gemini-web-importer/import-stage.py status \
  /root/.local/state/gemini-web-importer/receipts/ACCOUNT_RECEIPT.json
python3 /root/.local/state/gemini-web-importer/import-stage.py reconcile \
  /root/.local/state/gemini-web-importer/receipts/ACCOUNT_RECEIPT.json
```

No fresh bundle was consumed during preparation. Real import completion and
cookie/session persistence remain intentionally untested with real credentials.

### Production Transfer And Rollback: Not Applied

1. Rebaseline production immediately before a separately authorized cutover;
   compare original IDs, flags, binary/image and unrelated auths with `originals/`.
   Preserve current source files, exact binary and runtime configuration privately.
2. Complete each account serially. Restore temporary staging disabled flags only
   through native `PATCH /v0/management/auth-files/status`, with `{name, disabled}`
   taken exactly from the private original mapping. Never enable legacy-reference
   records. Only restore a record after its reference is local and login is saved.
3. Keep identity bindings as inert migration metadata: migrated encrypted records
   require these for integrity checks. Local-reference maintenance does not read
   Vault or invoke browser recovery. Removing those bindings breaks the existing
   plugin contract; removing the executable/token dependency is the relevant gate.
4. Drain imports/status/maintenance/generation, stop the legacy maintenance owner
   at cutover, then stop the stage. Never mount its session directory into two live
   cores. Preserve the full encrypted store, including recovery/lock files, and
   the separate key. Never remove owner/recovery files to clear uncertainty.
5. Prepare a production-only Compose override retaining the current image, ports,
   networks, unrelated runtime values and plugins. Mount the supplied core,
   persistent encrypted store and separate key, and inject the key using the
   existing environment contract at every core start. Do not invoke the external
   secret launcher. Preserve normalized production configuration except the local
   Gemini settings. Replace only migrated Gemini auth files, with a private
   archive of their exact original bytes and disabled states.
6. Start only production core and verify local account registry, real Flash,
   exactly one eligible Omni request, local renewal, and other-provider preservation.
   An unknown submission/write outcome prohibits a restart/resubmission workaround.
7. If a safe rollback is required, stop only the new core, restore the exact
   original core/plugin/config/runtime setup and five auth files, and restore the
   original maintenance-unit state. Do not overwrite unrelated provider auths.
   Keep the encrypted stage/key and receipts for recovery, with no live second
   owner. Do not start the stage on the transferred store until production stops.

To suspend preparation now, run `docker stop gemini-web-importer`. To resume,
run `docker start gemini-web-importer`. To remove only the stage container, stop
it then run `docker rm gemini-web-importer`; retain the private directories and
keys until the coordinator explicitly disposes of them. No production rollback
is needed for staging preparation because production was not changed.

## Earlier Cutover Attempt: Blocked Before Production Mutation

The September 14, 2026 cutover did **not** activate a new core, plugin, local
session key, account, maintenance job, or configuration. The existing browser
and services were not restarted. No legacy auth records were moved or changed.
No 1Password command or `.220` connection was made.

The private handoff, previously validated by the login operator, failed fresh
revalidation against the existing `.114` sidecar:

- `POST /v1/session/inspect`: HTTP 401.
- `GET /v1/account-models`: HTTP 401.

Both calls used the handed-off token in the `x-goog-api-key` header, constructed
only in remote subprocess memory. Neither response body, the token, email,
identity digest, nor secret reference was printed. No renewal or generation was
submitted. A fresh authorized capture and successful identity/model validation
are required before resuming. These responses establish rejection of this
handoff, not that the user's still-running browser has logged out.

## Verified Compatibility

The supplied same-release candidate and plugin were tested inside the exact
production image, not a substitute glibc image:

- Image: `sha256:8e03771cd4846b3f3547ef8b2d60a58f64e80754fccd0f18850da5c649fba323`.
- Candidate version: `v7.3.1-compatible-host-auth-save.1`, based on `44e62bc`.
- Core SHA-256: `1cd125f9e1c969c157b77f010e391a17f269bc8fa7a30decf4c9f3336b401a1e`.
- Plugin SHA-256: `c6111b408c34a00b246108d33f69e92bff58d35c9144fcd95d438e29a38470aa`.
- The existing fixture exited zero, with zero failures across 116 report events
  (assertions plus evidence records).

The container had no network, no real credentials, a read-only root filesystem,
all capabilities dropped, and `no-new-privileges`. Its only upstream was the
fixture's synthetic loopback provider. The first launch could not execute the
copied driver because its original ownership conflicted with dropped DAC
capabilities; changing ownership of the copied QA files to root fixed setup
without relaxing isolation. The successful container was automatically removed.

Private synthetic QA evidence is retained on `.114` at
`/root/.local/state/gemini-web-local-cutover-20260914/qa/run/report.json`.
Its SHA-256 is
`d12ef5c28435a45f618cb5c93d85a1985765f6f6cc8963453df820b555d1b973`.
This directory is QA evidence, **not a production rollback backup**.

## Preserved Runtime

The final check at `2026-09-14T00:32:30Z` (remote host clock) confirmed:

- Original v7.3.1 core PID `2799931`, original image, and healthy status.
- Core SHA-256: `163fe79fdd84e7b071520212382d798a2163edfe3b85183312a1d17bd7dcc4a7`.
- Unchanged live config, supplemental config, Compose file, and all three plugin
  binary hashes.
- Unrelated auth-file aggregate SHA-256 unchanged:
  `6b3bd261e3cc590993effc0cbb7d2a0fd0bb2b295270ca460580321575fdb3dd`.
- All five original Gemini records still reference the external store. All five
  currently have `disabled:false`, including `gemini-web-profile-1.json`.
  This observed baseline differs from older documentation; it was not corrected.
- The existing maintenance timer was active/waiting at baseline; its service was
  already failed with no running main process. Neither unit was changed.

Consequently the active legacy dependencies remain. There is no new named local
CPA account and no real CPA Flash or Omni result from this attempt.

## Resume And Rollback Boundary

After a fresh authorized handoff is available, repeat the live baseline and
sidecar identity/model gates before any mutation. Keep the current v7.3.1 source
pin and exact-image compatibility requirement; never build the core from the
dirty root workspace or use the older compatibility overlay.

The authorized deployment still requires a private exact rollback snapshot,
draining/stopping the old maintenance owner, archiving only the five old records
with their original states, and preserving unrelated provider runtime values.
Generate the 32-byte key locally into a separate root-only `0600` file outside
the dedicated `0700` session directory. Persistent core startup must inject
`GEMINI_WEB_SESSION_KEY` from that file without invoking the old external-secret
launcher. Bind-mount the supplied core, persistent session directory, and exact
local config while retaining the image and unrelated plugins. Use the exact
Manager origin `https://cliproxy.jclee.me` and installed extension ID
`jkomnpgdokmnojgbapacnnkakgnbekjn` with the existing consent/Origin-checked login
API. The private verified label, not an invented label, must be used.

Those bootstrap and rollback mechanisms were **not installed** in this blocked
attempt. No production rollback is necessary because no production mutations
occurred. A resumed cutover must document its actual private backup location and
tested restoration commands rather than treating this receipt as such a backup.
