# DEPLOYMENT KNOWLEDGE BASE

## OVERVIEW
Deployment convergence and native plugin artifacts; score 10, a distinct operational domain governed by `README.md` ("Current host") and `cpa-plugins.json`.

## STRUCTURE
- `cpa-plugins.json`: active core paths (docs, tests, testdata excluded), plugin artifacts, dashboard targets.
- `cpa-converge.py`: SHA-256 comparison, staging, swaps, host verification.
- `pull-deploy.sh`: dedicated-clone update and convergence launcher.
- `gemini-web-plugin/`: active Gemini native plugin and its browser resources.
- `chatgpt-web-plugin/`: active ChatGPT native plugin and dashboard.
- `flow2api-plugin/`: active Flow native plugin borrowing Gemini-owned sessions.
- `shared-browser/`: profile capture tooling and the signed-out Flow captcha browser.
- `gemini-web/`: historical .114 cutover and operator-import tooling.
- `chatgpt2api/`, `chatgpt2api-plugin/`: historical sidecar and adapter artifacts.
- `gemini-web-native/`, `gemini-web2api/`: historical Python bridges.
- `cliproxy-plus/`, `cpa-manager-plus/`: historical overlays and patch snapshots.
- `elk/`, `telegram-*/`, `gemini-login-desktop/`: retained ancillary deployment packages.

## WHERE TO LOOK
| Task | Location | Detail |
| --- | --- | --- |
| Active artifact selection | `cpa-plugins.json` | Gemini restart reload; ChatGPT and Flow hot reload |
| Core release selection | `cpa-converge.py` | Published binary and checksum, not a local rebuild |
| Idle restart gate | `cpa-converge.py` | Account activity, unanswered Omni picks, and in-flight API requests |
| Convergence contracts | `cpa_converge_test.py` | Local management server and Docker fixture |
| Deployment procedure | `README.md` | Current NAS paths and explicit convergence command |
| Account/browser ownership | `shared-browser/README.md` | Captured profiles stopped; signed-out captcha browser stays running |
| Provider implementation | `gemini-web-plugin/AGENTS.md`, `chatgpt-web-plugin/AGENTS.md`, `flow2api-plugin/AGENTS.md` | Module-local contracts and checks |

## CONVENTIONS
- Follow `../AGENTS.md` for the supported CPA topology and retired-package policy.
- Current host: Synology NAS `192.168.50.215`, base `/volume1/docker/cliproxy`; `.114` runbooks are historical.
- Run convergence as `jclee`; use the NAS environment block in `README.md`, not the script's old-host defaults.
- The fork's `master` branch selects deployed artifacts; upstream is merged explicitly.
- The dedicated deploy clone may contain untracked operator files; tracked modifications abort `pull-deploy.sh`.
- Plugin `.so` artifacts are deployed from the clone; core binaries come from the `cpa-core` release with verified checksums.
- Dashboards replace atomically; restart plugins and core swaps share an idle-gated stop/start window.
- Health failure allows one container restart, then fails without automatic rollback.
- `pull-deploy.sh` still has legacy Compose defaults; do not mistake those defaults for the operator's supported topology.
- On the NAS, `GITOPS_COMPOSE_FILES=" "` disables those defaults; an empty string does not.
- Offline converger checks: `cd deploy && python3 -m unittest cpa_converge_test`.
- Host plan inspection: `CPA_DRY_RUN=1 python3 deploy/cpa-converge.py deploy/cpa-plugins.json` from the repository root.

## ANTI-PATTERNS
- Do not use Compose to manage the active CPA core container; the converger uses Docker stop/start directly.
- Do not run `pull-deploy.sh` against a developer working tree: updates hard-reset the dedicated clone.
- Do not bypass the idle gate or install an unpublished/unverified core artifact.
- Do not infer an automatic schedule from script comments; the deployment README specifies explicit runs.
- Do not treat a failed health check as permission to restore stale plugin or core state.
- Do not leave a captured account's Chrome profile running beside CPA's rotating cookie store.
- Do not sign in to `shared-browser-flowcaptcha`; it mints tokens without holding an account session.
