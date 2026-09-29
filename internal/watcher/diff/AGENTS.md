# CONFIGURATION CHANGE SUMMARIES

## OVERVIEW
Deterministic config/auth change descriptions and model signatures; score 8, a distinct reload-reporting domain.

## WHERE TO LOOK
| Task | File |
|------|------|
| Top-level configuration changes | `config_diff.go` |
| Small auth-field changes | `auth_diff.go` |
| Model hash adapters | `model_hash.go` |
| Alias/display/thinking summaries | `models_summary.go` |
| OAuth exclusion changes | `oauth_excluded.go` |
| OAuth alias changes | `oauth_model_alias.go` |
| OAuth scoped-error changes | `oauth_request_scoped_errors.go` |
| Duplicate OpenAI-compat providers | `openai_compat.go` |

## CONVENTIONS
- Change messages report structure and non-sensitive values; apply the root secret-logging rule.
- Shared model hashes delegate to `internal/modelconfig` where the model shape matches.
- Provider/channel summaries normalize their inputs before comparison.
- Model metadata such as display names, compat flags, and thinking support participates in relevant hashes.
- OpenAI-compatible lists account for duplicate provider names rather than assuming name uniqueness.
- OAuth-wide changes are grouped by channel for targeted reload decisions.

## ANTI-PATTERNS
- Do not describe two semantically distinct model lists with the same incomplete hash.
- Do not drop display-name or compat-only changes from reload summaries.
- Do not key duplicate OpenAI-compatible entries solely by provider name.
- Do not replace deterministic summaries with unstable map iteration output.
- Do not move source-to-auth synthesis into a reporting helper.

Run `go test ./internal/watcher/diff` for this package.
`config_diff_test.go` covers field reporting; `model_hash_test.go` covers hash semantics.
When adding configuration fields, decide whether they affect reporting, model identity, or both.
