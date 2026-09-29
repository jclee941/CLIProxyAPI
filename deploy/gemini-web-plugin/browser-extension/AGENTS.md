# GEMINI LOGIN COMPANION

## OVERVIEW
Manifest V3 session-capture companion with explicit user consent; score 10 for its independent TypeScript build and exported protocol surface.

## WHERE TO LOOK
| Task | Location | Detail |
| --- | --- | --- |
| Background entry | `service-worker.ts` | Port wiring and companion lifecycle |
| Consent state machine | `companion.ts` | Selected tab, approval, capture, cancellation |
| Capture boundary | `capture.ts` | Tab/store/document snapshots and cookie checks |
| Chrome API adapter | `chrome-adapter.ts` | Identity extraction from selected document |
| Wire authorization | `protocol.ts` | Origins, port names, schemas, sender checks |
| Restart replay protection | `replay.ts` | Bounded hashed tombstones |
| Popup surface | `popup.ts`, `popup.html` | Selection and explicit approval |
| Origin/packaging policy | `manifest.ts`, `build.ts` | Production versus local-QA artifacts |
| Deterministic fixtures | `tests/fake-chrome.ts`, `tests/fake-companion.ts` | Browser and portal simulation |
| Browser exercise | `qa.ts`, `qa-production.ts` | Synthetic consent flow and package QA |

## CONVENTIONS
- Install dependencies with `bun --no-env-file install --frozen-lockfile --ignore-scripts`.
- Validation commands: `bun --no-env-file run check` and `bun --no-env-file test`.
- Package with `bun --no-env-file run build`; exercise browser flows with `run qa` and `run qa:production`.
- Session capture checks the selected tab's cookie store and document before and after reading cookies.
- Account identity requires agreement between the three expected Google page globals.
- A selected partition is checked explicitly; partitioned cookies are unsupported.
- Captured cookie headers must match across the account's supported endpoint paths.
- The encoded token has a 32768-byte ceiling, including its prefix.
- Persistent replay protection stores nonce hashes and expiry, not raw portal state.
- The popup's account selection and consent are separate actions; opening a tab does not authorize capture.

## ANTI-PATTERNS
- Do not choose an active-tab fallback when the selected tab disappears or changes identity.
- Do not enumerate unrelated cookie partitions with an empty partition filter.
- Do not add broad origin permissions to work around a wrong extension ID or portal configuration.
- Do not install a `LOCAL-QA` package for a real login.
- Do not expose tab titles, conversation paths, GAIA identifiers, or identity digests in the popup.
- Do not add Manager HTTP calls, clipboard export, token storage, or model generation to this companion.
