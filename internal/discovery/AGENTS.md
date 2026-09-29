# LAN GATEWAY DISCOVERY

## OVERVIEW
DNS-SD service construction, mDNS advertisement, and browsing; score 8, a distinct network discovery domain.

## WHERE TO LOOK
| Task | File |
|------|------|
| Advertiser/browser contracts | `types.go` |
| Persistent instance identity | `id.go` |
| Config-to-service conversion | `service.go` |
| Interface selection | `interfaces.go` |
| TXT encoding/decoding | `spec.go` |
| Zeroconf lifecycle | `zeroconf.go` |
| Identity, filtering, lifecycle regressions | `discovery_test.go` |

## CONVENTIONS
- Default service type is `_ai-gateway._tcp` in the `local.` domain.
- Protocol support is advertised through DNS-SD subtypes.
- Instance identity persists in a state directory instead of changing on every startup.
- Instance names retain the generated identifier when the human-readable prefix is shortened.
- Interface filtering excludes loopback, point-to-point, and known virtual-interface prefixes.
- Multi-homed results may contain multiple addresses for one gateway.
- TXT encoding enforces both per-record and total payload bounds.
- The CLI's presentation/filter precedence is implemented separately in `../cmd/discover.go`.

## ANTI-PATTERNS
- Do not advertise invalid service names or ports; validate before starting zeroconf.
- Do not truncate a TXT record beyond the DNS-SD byte limit.
- Do not regenerate stable instance IDs for every scan or advertisement refresh.
- Do not assume a discovered service has exactly one usable IP address.
- Do not equate every UP interface with a physical LAN interface.

Run `go test ./internal/discovery` for this package.
CLI scan output changes also need `go test ./internal/cmd`.
