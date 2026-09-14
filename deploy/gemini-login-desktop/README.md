# Gemini Login Desktop

The `.114` protected VNC desktop runs official **Google Chrome Stable
153.0.8010.36**. The package URL and SHA-256 are pinned in `Dockerfile`.
It can open the migrated Chrome 150, Chromium 151, and Chromium 152 data
without downgrading any profile. CPA production, Manager, the sidecar,
Cloudflare, and the original `.220` browser were not restarted.

## Migration Status, 2026-09-14

Seven persistent profiles are visible in Chrome's profile picker:

| Source | Target Directory | Native Chrome Association | Latest Gemini Web Check |
| --- | --- | --- | --- |
| `.220` Default, local name 5 | `Default` | Signed-in menu verified | Valid |
| `.220` Profile 1, local name 1 | `Profile 1` | Signed-in menu verified | Valid |
| `.220` Profile 2, local name 4 | `Profile 2` | Signed-in menu verified | Valid |
| `.220` Profile 4, local name 2 | `Profile 4` | Signed-in menu verified | Valid |
| `.220` Profile 5, local name 3 | `Profile 5` | Signed-in menu verified | Valid |
| `.220` Work Chromium | `Profile 8` | None at source or target | Valid; duplicate of Profile 1 |
| Original `.114` Chromium | `Profile 9` | None at source or target | Manual Google login required |

Native association is not Sync consent. No Sync enablement is claimed. The
five original menus offered **Sign out of Chrome**, with no observed native
reauthentication warning. Work and the original `.114` profile had no native
OAuth association to migrate; their metadata was not fabricated.

All seven initially passed target-host identity and account-model validation.
The `.114` copy was initially another duplicate of Profile 1, but its Google
authentication cookies disappeared after interactive use in official Chrome.
The final check could no longer establish its authenticated Gemini identity.
Do not treat that copy as signed in: the user must log in manually. The original
`home/chromium` directory and its complete rollback copy remain unchanged.
No automatic Google login, account switching within a web session, explicit
logout, cookie clearing, or credential renewal was performed.

Five unique accounts were saved by the existing isolated importer, retaining
their original auth IDs and verified account labels. All five have encrypted
local session references, `saved` receipts, and `disabled:true` in staging.
The final live account listing returned **four ready and one expired**: the
first saved Default session expired after browser QA, although a fresh check of
that browser profile still succeeded. Its receipt remains `saved`; persistence
success is not current credential validity. Replacing that credential needs a
new authorized login lifecycle with proper receipt handling. The existing
helper refuses another receipt for the same ID; no receipt was removed, no
bundle-ID bypass or blind completion was attempted, and renewal was not called.
The original production five records remain byte-identical. This is **staging,
not production linkage or cutover**. The browser and staged local records do
not require an ongoing `.220` connection or external secret-store lookup.

The first intake saved two accounts. A second, disjoint intake saved the three
previously unsubmitted accounts after cookie-scope filtering was corrected.
No completed login was resubmitted and no receipt was bypassed. The initial
readiness probe's zero-model assertion is no longer applicable after successful
imports; use the importer's receipt `status` operation instead.

## Access and Security

- Public viewer: `https://vnc2.jclee.me/vnc.html?autoconnect=1&resize=scale&reconnect=1`.
- Host origin: `http://127.0.0.1:16080`; only this loopback address is published.
- The original VNC password is retained. Never put it in a URL or command line.
- Raw VNC listens on container loopback. No CDP listener or browser bridge exists.
- UID/GID `1000:1000`, private home, basic password store, all capabilities
  dropped, `no-new-privileges`, and the existing CPU/RAM/PID limits are retained.
- The existing nested-container `--no-sandbox` constraint remains. This is a
  trusted-user-only desktop, not a general shared browser.

Authenticated noVNC mouse input opened the Chrome profile menu and selected
Work from the seven-profile picker. Keyboard input navigated to
`chrome://version`, showing the official build and persistent Profile 9 path.
An incorrect VNC credential was rejected; the retained credential connected.
The service is healthy, without a restart loop. Sequential profile checks kept
runtime memory below the existing 1.5 GiB limit.

## Persistence and Companion

Active user data is `/var/lib/gemini-login-desktop/home/chrome-migrated`.
The earlier `home/chromium` directory is rollback data and must not be opened
with an older browser after modification by a newer browser.

The companion remains at `/home/desktop/companion`. In Profile 9,
`chrome://extensions` displayed the enabled **Gemini Web Login Companion 0.2.0**
with actual installed ID `jkomnpgdokmnojgbapacnnkakgnbekjn`. Its portal authority
is `https://cliproxy.jclee.me`. Other profiles do not automatically inherit an
extension installation. If needed, use normal **Load unpacked** UI with this
same path and verify the displayed ID; do not guess IDs or bypass Chrome policy.

Only the desktop service may be restarted under this deployment. Browser logs
are written inside its private home rather than shared Docker logs.

## Deployment and Rollback

The active Compose project directory is `/opt/gemini-login-desktop-chrome153`.
Build and verify the image before a coordinated desktop-only interruption:

```sh
scp Dockerfile compose.yaml start-desktop.sh 192.168.50.114:/opt/gemini-login-desktop-chrome153/
ssh 192.168.50.114 'docker compose --project-directory /opt/gemini-login-desktop-chrome153 config --quiet'
ssh 192.168.50.114 'docker compose --project-directory /opt/gemini-login-desktop-chrome153 build desktop'
ssh 192.168.50.114 'docker run --rm --network none --entrypoint google-chrome-stable gemini-login-desktop:chrome153 --version'
ssh 192.168.50.114 'docker compose --project-directory /opt/gemini-login-desktop-chrome153 up -d --no-build --no-deps desktop'
```

Private rollback and evidence root on `.114`:
`/root/.local/state/gemini-profile-migration` (mode `0700`). It contains the
complete stopped-desktop `home-rollback`, `launch-rollback`, runtime inspection,
source snapshots, profile mapping, native-menu evidence, and final account status.
`final-account-status.json` records six current browser validations and the
Profile 9 login blocker (`inspect:401`, `account_models:401`).
`final-stage-accounts.json` records the four-ready/one-expired staging boundary.
The old image is retained as `gemini-login-desktop:rollback-20260914`.
For an authorized rollback, stop only the desktop, use `launch-rollback`'s
original Compose configuration and retained original image, and keep using the
untouched `home/chromium`. Do not overwrite the migrated Chrome data or importer.

Importer intake files are root-owned `0600` under
`/root/.local/state/gemini-web-importer/intake/`: `fresh-bundle.json` and
`fresh-bundle-remaining.json`. Keep them and `receipts/` private for reconciliation.
Never print their contents, recreate bundles to bypass receipts, restore staging
flags, or cut over production as part of desktop maintenance.

## One-Time Snapshot Helper

`snapshot-profile.py SOURCE_PROFILE NEW_PRIVATE_DESTINATION` copies a bounded,
stable set of Preferences, Local State, and authentication databases. It checks
file identity, size, modification/change times and a second byte read across
the set. Changed sources fail closed. Source databases are not opened or locked;
SQLite normally recovers the private DB/WAL/journal copy and checks integrity.
No `immutable` URI or source browser freeze/restart is used.

Exported SQLite files retain only Google cookies, native token-service rows
(including binding metadata), and schema metadata. History, saved-password
databases, caches, downloads, and unrelated autofill rows are not transferred.
Directories are `0700`, files `0600`. Live-WAL and exclusive-source-lock
synthetic probes verified recovery and unrelated-data exclusion.

When merging separately created profiles, preserve their genuine account
metadata and avoid source deletion tombstones. The source Local State reserved
`Profile 6` and `Profile 7` for deletion; the additional profiles therefore use
`Profile 8` and `Profile 9`. Never clear deletion history or synthesize Google
association merely to make a profile card appear signed in.
