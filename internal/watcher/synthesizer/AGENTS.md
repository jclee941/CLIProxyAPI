# CREDENTIAL SYNTHESIS

## OVERVIEW
Translate config keys and OAuth files into runtime auth records; score 8, a distinct credential-boundary domain.

## WHERE TO LOOK
| Task | File |
|------|------|
| Strategy interface | `interface.go` |
| Config API-key conversion | `config.go` |
| File traversal and OAuth parsing | `file.go` |
| Context and plugin parser contracts | `context.go` |
| Stable IDs and attribute helpers | `helpers.go` |
| Environment reference expansion | `environment.go` |
| Provider key matrix | `config_test.go` |
| File/plugin auth matrix | `file_test.go` |

## CONVENTIONS
- `ConfigSynthesizer` and `FileSynthesizer` share the `AuthSynthesizer` contract.
- `SynthesisContext` carries configuration, auth directory, identity generation, and plugin parsing.
- `StableIDGenerator` derives kind-prefixed short hashes and collision suffixes.
- Custom header attributes use the `header:` prefix consumed by request helpers.
- Excluded models combine provider-wide and per-key rules through one helper.
- File parsing can expand one plugin file into multiple auth records.
- OpenAI-compatible key values support `${ENV_VAR}` references.
- Kimi domain and Codex plan information survive file-to-auth conversion.

## ANTI-PATTERNS
- Do not replace stable identity generation with random IDs on each synthesis.
- Do not silently collapse multi-auth plugin results into one record.
- Do not omit configured weight, priority, cooling, or fingerprint attributes during provider adaptation.
- Do not strip the `header:` attribute namespace before runtime request construction.
- Do not assume all credential files share one provider's token fields.

Run `go test ./internal/watcher/synthesizer` for provider and file conversion coverage.
Management auth-file edits and SDK reloads call these same constructors; changes affect both paths.
