# GEMINI MANAGEMENT RESOURCE

## OVERVIEW
Self-contained iframe account/login portal; score 10 for its independent frontend build, symbol density, and exported contracts.

## WHERE TO LOOK
| Task | Location | Detail |
| --- | --- | --- |
| Portal startup | `src/main.ts` | Account loading and view coordination |
| Management transport | `src/api.ts`, `src/host-request.ts` | Host bridge or same-origin fallback |
| Fallback authentication | `src/auth.ts` | Existing host auth storage format |
| Account DTO validation | `src/contract.ts` | Zod boundary schemas |
| Login orchestration | `src/login-flow.ts`, `src/login-api.ts` | Complete/status/cancel/reconcile |
| Companion channel | `src/companion.ts` | Port identity and one-session handshake |
| Login UI and schemas | `src/login-panel.ts`, `src/login-contract.ts` | Consent and backend state rendering |
| Quota cards | `src/account-card.ts` | Measured account state and usage |
| Host-compatible styling | `src/tokens.css`, `src/styles.css` | Shared design tokens |
| Deployment artifact | `build.ts`, `src/index.html` | Generate root `index.html` |
| Browser fixtures | `tests/browser.ts`, `tests/login-browser.ts` | Mock host and login flows |

## CONVENTIONS
- Source lives under `src/`; root `index.html` is the bundled deployment artifact.
- Bun inlines the IIFE, styles, and assets into one HTML file; no external runtime resource loading.
- Prefer the parent's `__CPAMP_PLUGIN_HOST__` transport when available.
- Transport rejects redirects and off-origin requests; mutations have no automatic retries.
- Render the backend's explicit login state, not inferred success from a completed fetch.
- Missing metrics remain visibly unknown; compute units are not token counts.
- Saved-but-disabled accounts stay disabled until an explicit host action changes them.
- Run `bun --no-env-file run typecheck`, `run test`, and `run build` from this directory.
- `bun --no-env-file run qa` exercises the browser suite against synthetic management fixtures.

## ANTI-PATTERNS
- Do not hand-edit generated `index.html` instead of rebuilding its sources.
- Do not override host theme properties when the host bridge provides them.
- Do not turn a login request deadline into a displayed credential expiry.
- Do not resend a token after uncertain completion; expose status/reconcile instead.
- Do not promise cancellation reversed a server-side mutation already in progress.
- Do not reflect unknown raw response bodies into user-facing error text.
- Do not introduce polling timers or automatic disable/delete/renewal actions.
