# CONFIGURATION CONTRACT

## OVERVIEW
Shared configuration schema, validation, runtime copies, and YAML persistence; score 12, a central configuration boundary.

## WHERE TO LOOK
| Change | Location |
|--------|----------|
| Root schema | `config.go` |
| Provider/routing/plugin structures | `config_types.go` |
| SDK-facing behavior | `sdk_config.go` |
| File loading and defaults | `config_load.go` |
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
- Optional file loading tolerates missing, empty, or invalid configuration for cloud standby.

## ANTI-PATTERNS
- Do not replace preserving saves with an unconditional `yaml.Marshal` rewrite.
- Do not normalize away intentional zero/false values or discard raw plugin configuration.
- Do not make payload parsing mutate the operator's configuration file.
- Do not share mutable slices, maps, or YAML nodes between runtime snapshots.
- Do not bypass pre-unmarshal weight checks; scalar syntax is part of the input contract.

Run `go test ./internal/config`; schema changes also touch watcher diff/synthesis and SDK configuration consumers.
