# PROVIDER LOGIN AND TOKEN STORAGE

## OVERVIEW
Provider login orchestration, refresh-lead registration, and filesystem credential persistence; score 11, distinct authentication lifecycle domain.

## WHERE TO LOOK
| Task | Location | Detail |
|------|----------|--------|
| Add an authenticator | `interfaces.go`, provider-named files | Provider, Login, RefreshLead contract and LoginOptions. |
| Orchestrate login/persistence | `manager.go` | Registration, metadata merge, explicit creation intent, legacy migration. |
| Browser/device Codex login | `codex.go`, `codex_device.go` | Separate browser and device-code paths. |
| Headless/project discovery | `antigravity.go`, `devin.go` | Provider-specific flows and injected prompts. |
| Credential save/list/delete | `filestore.go` | FileTokenStore and plugin parser adapters. |
| Replace process-wide store | `store_registry.go` | RegisterTokenStore/GetTokenStore. |
| Register refresh timing | `refresh_registry.go` | init-time lead callbacks for provider identifiers. |
| Check persistence invariants | `filestore*_test.go`, `manager_test.go` | Disabled files, metadata, proxy persistence, migration. |

## CONVENTIONS
- Login returns a coreauth.Auth record; Manager returns record, persisted path, and error separately.
- A Manager without a store returns the login record without persisting it.
- Manager sets stores supporting SetBaseDir from cfg.AuthDir before persistence.
- Existing credential metadata is merged before saving a replacement login.
- Login saves with coreauth.WithAuthCreationIntent; routine runtime updates do not.
- Legacy Claude credentials are removed only after a nonempty canonical save path succeeds.
- FileTokenStore normalizes credential metadata and validates auth weight before writing.
- Storage-backed records use their TokenStorage writer; metadata-only records use JSON persistence.
- Metadata-only writes avoid rewriting semantically unchanged JSON.
- Successful saves stamp path, source, and file-backend attributes onto the record.
- Directory configuration and file writes use separate locks; snapshot baseDir rather than reading it unlocked.
- PluginMultiAuthParser may expand one file into several records.
- handled=true with an empty plugin result intentionally suppresses built-in parsing.
- GetTokenStore lazily installs a FileTokenStore when no explicit store is registered.
- Refresh registration includes the distinct kimi, kimi-ai, and kimi.ai identifiers.
- Focused checks: `go test ./sdk/auth`; provider-specific flows have adjacent headless/login tests.

## ANTI-PATTERNS
- Do not recreate a removed disabled credential during routine Save; deliberate login/migration creation must carry intent.
- Do not discard preserved metadata when refreshing an existing login.
- Do not remove legacy Claude credentials before canonical persistence is confirmed.
- Do not interpret a handled empty plugin parse as permission to run the built-in parser.
- Do not confuse refresh-lead registration with implementation of token refresh itself.
