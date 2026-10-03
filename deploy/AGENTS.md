# DEPLOYMENT KNOWLEDGE BASE

## OVERVIEW
Deployment convergence and native plugin artifacts; score 14, with operator policy overriding historical sidecar instructions.

## STRUCTURE
- `cpa-plugins.json`: active core paths (docs, tests, testdata excluded), plugin artifacts, dashboard targets.
- `cpa-converge.py`: SHA-256 comparison, staging, swaps, host verification.
- `pull-deploy.sh`: dedicated-clone update and convergence launcher.
- `gemini-web-plugin/`: active Gemini native plugin and its browser resources.
- `chatgpt-web-plugin/`: active ChatGPT native plugin and dashboard.
- `gemini-web/`: retained core-launch and operator-import tooling; inspect before reuse.
- `chatgpt2api/`, `chatgpt2api-plugin/`: historical sidecar and adapter artifacts.
- `gemini-web-native/`, `gemini-web2api/`: historical Python bridges.
- `cliproxy-plus/`, `cpa-manager-plus/`: historical overlays and patch snapshots.
- `elk/`, `telegram-*/`, `gemini-login-desktop/`: retained ancillary deployment packages.

## WHERE TO LOOK
| Task | Location | Detail |
| --- | --- | --- |
| Active artifact selection | `cpa-plugins.json` | Gemini restart reload; ChatGPT hot reload |
| Core release selection | `cpa-converge.py` | Published binary and checksum, not a local rebuild |
| Idle restart gate | `cpa-converge.py` | Account activity, unanswered Omni picks, and in-flight API requests |
| Convergence contracts | `cpa_converge_test.py` | Local management server and Docker fixture |
| Deployment procedure | `README.md` | Explicit run after core release publication |
| Core publishing | `../.github/workflows/core-build.yml` | Tests master on every push the core tests read; builds only unpublished core commits |

## CONVENTIONS
- CPA runs only core, PostgreSQL, and native plugins. Legacy sidecars/overlays are not deployed or maintained.
- Treat old Compose files, patches, and setup instructions as historical evidence, not activation instructions.
- The fork's `master` branch selects deployed artifacts; upstream is merged explicitly.
- The dedicated deploy clone may contain untracked operator files; tracked modifications abort `pull-deploy.sh`.
- Plugin `.so` artifacts are deployed from the clone; core binaries come from the `cpa-core` release with verified checksums.
- Dashboards replace atomically; restart plugins and core swaps share an idle-gated stop/start window.
- Health failure allows one container restart, then fails without automatic rollback.
- `pull-deploy.sh` still has legacy Compose defaults; do not mistake those defaults for the operator's supported topology.
- Offline converger checks: `cd deploy && python3 -m unittest cpa_converge_test`.
- Host plan inspection: `CPA_DRY_RUN=1 python3 deploy/cpa-converge.py deploy/cpa-plugins.json` from the repository root.

## ANTI-PATTERNS
- Do not recreate retired services, including `chatgpt2api`, from retained launchers or Compose snapshots.
- Do not use Compose to manage the active CPA core container; the converger uses Docker stop/start directly.
- Do not run `pull-deploy.sh` against a developer working tree: updates hard-reset the dedicated clone.
- Do not bypass the idle gate or install an unpublished/unverified core artifact.
- Do not infer an automatic schedule from script comments; the deployment README specifies explicit runs.
- Do not treat a failed health check as permission to restore stale plugin or core state.
