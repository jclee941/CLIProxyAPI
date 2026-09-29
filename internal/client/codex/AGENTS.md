# CODEX CLIENT INTEGRATION

## OVERVIEW
Codex-specific catalogs, realtime sessions, and collaboration adaptation; score 9, a distinct client protocol domain.

## WHERE TO LOOK
| Task | Location |
|------|----------|
| Client model catalog and capability intersection | `models/models.go` |
| WebRTC bootstrap and configuration reload | `live/live.go` |
| Pion audio/data-channel bridge | `live/media.go` |
| Ephemeral client secrets | `live/client_secret.go` |
| Call sidebands and session lifetime | `live/sideband.go` |
| Direct Realtime WebSocket relay | `live/websocket.go` |
| ICE TCP proxy and STUN verification | `live/tcp_proxy.go` |
| Unsupported capabilities and hangup | `live/capabilities.go` |
| Tool/namespace preparation and restoration | `optimize-multi-agent-v2/optimize_multi_agent_v2.go` |
| Orphan delegation output repair | `optimize-multi-agent-v2/orphan_delegation.go` |

## CONVENTIONS
- Model templates come from revisioned registry snapshots; `gpt-5.5` supplies the fallback template.
- Catalog output uses `MarshalCompact`: single-line JSON without HTML escaping.
- Mixed-provider capabilities are intersections, not unions of template features.
- Required non-Codex option keys remain present with JSON null values.
- Compact fallback instructions keep explicit catalogs within the client body's 1 MiB cap.
- Extended reasoning levels are client-version gated.
- CPA-only capability fields are emitted only for the dedicated `cpa` client version.
- Realtime client secrets bind principal and session/model scope, with bounded in-memory storage.
- WebRTC calls pin the selected credential for later sideband and hangup operations.
- Collaboration namespace optimization is reversible on responses.
- Boundary tool preparation is recorded in the Gin context to avoid preparing tools twice.

## ANTI-PATTERNS
- Do not remove required null catalog fields or either supported instruction field representation.
- Do not let template claims widen capabilities for mixed/non-Codex provider routes.
- Do not rename a client-defined reserved `collaboration-optimize` namespace.
- Do not leave collaboration message encryption fields in tool schemas the proxy must interpret.
- Do not skip principal/model scope checks on ephemeral-secret requests.
- Do not allow private/non-routable TCP proxy targets or candidates outside port 443.
- Do not end a relay session from inside the resource closer that it waits on.
- Do not forward `OpenAI-Alpha` on direct Realtime WebSocket handshakes.

Run `go test ./internal/client/codex/...` for all three packages.
Catalog consumer regressions also live in `internal/api` and `sdk/api/handlers/openai`.
Realtime lifecycle changes need the Pion, TCP proxy, and client-secret regression suites together.
