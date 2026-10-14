# REMOTE CREDENTIAL STORES

## OVERVIEW
Git, object-store, and PostgreSQL persistence with local file mirrors; score 8 (219 symbols, 41 exports; sole importer `cmd/server/main.go`), a distinct consistency/recovery domain.

## WHERE TO LOOK
| Task | File |
|------|------|
| Git worktree, branch, and push behavior | `gitstore.go` |
| Git corruption recovery and rollback | `gitstore.go` recovery helpers |
| S3/MinIO token/config synchronization | `objectstore.go` |
| PostgreSQL schema and spool | `postgresstore.go` |
| Concurrent cooldown persistence | `postgres_cooldown_store.go` |
| Git divergence/recovery integration cases | `gitstore_test.go` |
| Intentional disabled-credential creation | `disabled_login_save_test.go` |
| Multi-instance cooldown merge | `postgres_cooldown_store_test.go` |

## CONVENTIONS
- Remote stores expose local `AuthDir` and `ConfigPath` mirrors to filesystem consumers.
- Explicit login/migration saves carry `HasAuthCreationIntent` when creation is intentional.
- Runtime saves do not recreate removed disabled credentials without that intent.
- Git commits disable inherited signing to support unattended persistence.
- Git push precedes maintenance GC.
- Git recovery preserves backup material when rollback cannot safely complete.
- PostgreSQL cooldown persistence merges concurrent instances rather than replacing unrelated state.
- `PostgresStore` alone also provides the cooldown state store (`CooldownStateStoreProvider`).
- Startup selection priority: `PGSTORE_DSN`, then `OBJECTSTORE_ENDPOINT`, then `GITSTORE_GIT_URL`.
- Object-store sync overwrites incrementally; PostgreSQL bootstrap resets the local auth spool before repopulating it.

## ANTI-PATTERNS
- Do not wipe watched object-store auth directories during synchronization; watcher deletions can propagate remotely.
- Do not accept watcher-originated removal of tracked Git auth as an explicit deletion request.
- Do not overwrite overlapping dirty local paths during remote pull or recovery; fail closed.
- Do not bypass remote-lease checks to force a stale push.
- Do not stage unrelated worktree changes as part of configuration persistence.
- Do not discard recovery backups after an unsuccessful rollback.

Run `go test ./internal/store` for local Git and storage contract regressions.
Store lifecycle selection is wired by `cmd/server/main.go`; watcher persistence calls use these local mirrors.
