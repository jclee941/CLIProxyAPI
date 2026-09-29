# MANAGEMENT HANDLER KNOWLEDGE BASE

## OVERVIEW
Management control surface under `management/`; score 12, distinct persistence/OAuth domain covered here at depth three.

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Handler state and authorization | `management/handler.go` | Locks, reload generations, management-key verification |
| Scalar/YAML configuration | `management/config_basic.go` | Validate uploaded YAML before committing |
| Credential list edits | `management/config_lists.go` | Tuple identity, normalization, comment preservation |
| Config/runtime credential joins | `management/config_auth_index.go` | Stable synthesized auth IDs |
| Disable static API keys | `management/config_apikey_disable.go` | Wildcard excluded-model marker |
| Auth listing and cooldowns | `management/auth_files.go` | Runtime state reconciliation and pagination |
| Auth mutation and persistence | `management/auth_files_crud.go`, `management/auth_files_fields.go` | Batch operations and post-persist hooks |
| OAuth state machine | `management/oauth_sessions.go`, `management/oauth_callback.go` | Pending guards and atomic callback handoff |
| Provider login flows | `management/auth_files_provider_oauth.go`, `management/auth_files_devin_oauth.go` | Asynchronous exchanges |
| Generic upstream call | `management/api_tools.go` | Token substitution and explicit proxy precedence |
| Plugin config/catalog | `management/plugins.go`, `management/plugin_store.go` | YAML nodes, source provenance, runtime locks |
| Release lookup coordination | `management/plugin_store_release.go` | Singleflight cache and two-slot concurrency |
| Quota probing | `management/plugin_quota.go`, `management/quota.go` | Declarative probes and auth-index reset |
| Log pagination | `management/logs.go` | Rotation-aware, fingerprinted cursors |
| Queue consumption | `management/usage.go` | GET destructively pops pending records |

## CONVENTIONS
- Handler receiver methods are grouped by operation; shared state stays in `handler.go`.
- Config saves preserve YAML comments; plugin raw config is edited as YAML nodes.
- Save and clone under `h.mu`; invoke reload callbacks after releasing it.
- Reload snapshots carry generations so older asynchronous work cannot replace newer state.
- Credential identity includes API key, base URL, and prefix; ambiguous matches require disambiguation.
- APICall proxy order: request, credential, global, then direct; environment proxies are bypassed.
- OAuth callback files use temporary siblings plus rename; token saves require a still-pending session.
- Run `go test ./internal/api/handlers/management` for package checks.

## ANTI-PATTERNS
- Do not call post-persist hooks while holding the handler mutex.
- Do not pass the live config pointer to asynchronous reload hooks.
- Do not reactivate authentication/token failures merely because a cooldown elapsed.
- Do not copy credential-specific settings onto new or ambiguously matched credentials.
- Do not forward unresolved `$TOKEN$` values or send quota probes with missing required tokens.
- Do not route quota resets by raw auth ID or filename; use `auth_index`.
- Do not overwrite a loaded plugin or silently switch its unverified installation source.
- Do not spend release-query quota on uninstalled catalog entries while browsing.
- Do not let cursor filenames or auth filenames select paths outside their managed directory.
- Do not include successful requests in the error-log listing.
