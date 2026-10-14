# CONFIGURATION CONTRACT

## OVERVIEW
Shared configuration schema, validation, runtime copies, and YAML persistence; score 14 (69 Go files, 172 exports, 476 importing files), the central configuration boundary.

## WHERE TO LOOK
| Change | Location |
|--------|----------|
| Root schema | `config.go` |
| Provider/routing/plugin structures | `config_types.go` |
| SDK-facing behavior | `sdk_config.go` |
| File loading | `config_load.go` |
| Default constants (auth dir, pprof, panel repo) | `config_defaults.go` |
| v8 layout and historical YAML aliases | `config_v8.go` |
| Management alias projection/normalization | `config_v8_api.go` |
| API-key view without OAuth-only overrides | `oauth_scope.go` |
| Claude fingerprint profile values | `claude_fingerprint_profile.go` |
| Vertex-compatible API keys | `vertex_compat.go` |
| Model catalog source alias | `model_catalogs.go` (`registry.CatalogSources`) |
| In-memory parsing | `parse.go` |
| Comment-preserving saves | `config_yaml.go` |
| Runtime deep copy | `clone.go` |
| Provider/key normalization | `config_normalization.go` |
| Weight syntax and bounds | `weight.go` |
| Concurrency presence/lifecycle fields | `credential_concurrency.go` |
| Observation windows | `credential_in_flight.go` |
| Realtime relay settings | `codex_live.go` |
| Polymorphic image-generation mode | `disable_image_generation_mode.go` |
| Home listener and control-plane settings | `home.go` |
| Telegram/environment settings | `telegram.go` |
| Plugin directory resolution | `plugin_path.go` |
| Payload rule validation | `config_validation.go` |
| Trusted reverse proxies | `trusted_proxies.go` |

## CONVENTIONS
- Keep `LoadConfigOptional` and `ParseConfigBytes` defaults aligned.
- File loading may hash and persist the management key; byte parsing hashes without disk writes.
- YAML saves merge `yaml.Node` trees to preserve comments, ordering, and operator input.
- Explicit zero weights and pointer-backed false booleans carry meaning during merges.
- Presence-aware decoding distinguishes absent lifecycle fields from explicit zero values.
- `CloneForRuntime` deep-copies graphs, including plugin YAML nodes.
- OAuth-wide alias/error rules are separate from per-API-key rules.
- Deprecated relay IP settings are translated to the replacement setting with inverse polarity.
- v8 layout translation stays at YAML boundaries; `Config` remains the runtime/wire shape.
- Canonical paths win by presence (explicit false/null included), then older v8 OAuth paths, then globals.
- `ForAPIKey` returns a request-local view; the shared config is never modified.
- `NormalizeClaudeFingerprintProfile` is the single source of truth for runtime, sanitizer, and management.
- Optional file loading tolerates missing, empty, or invalid configuration for cloud standby.

## ANTI-PATTERNS
- Do not replace preserving saves with an unconditional `yaml.Marshal` rewrite.
- Do not normalize away intentional zero/false values or discard raw plugin configuration.
- Do not make payload parsing mutate the operator's configuration file.
- Do not share mutable slices, maps, or YAML nodes between runtime snapshots.
- Do not merge legacy entries because endpoints match; routing, headers, models, or policies may differ.
- Do not bypass pre-unmarshal weight checks; scalar syntax is part of the input contract.

Run `go test ./internal/config`; schema changes also touch watcher diff/synthesis and SDK configuration consumers.
