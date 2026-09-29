# REASONING SIGNATURE COMPATIBILITY

## OVERVIEW
Signature shape inspection, provider compatibility, and history sanitization; score 11, a distinct protocol-integrity domain.

## WHERE TO LOOK
| Concern | File |
|---------|------|
| Provider detection and action decisions | `provider_compatibility.go` |
| Claude envelope structure/normalization | `claude_validation.go` |
| Claude history filtering | `claude.go`, `claude_messages_sanitize.go` |
| Gemini envelopes and tool pairing | `gemini_validation.go` |
| Gemini canonical fields and bypass | `gemini_sanitize.go` |
| GPT reasoning transport shape | `gpt_validation.go` |
| Grok opaque ciphertext replay checks | `grok_validation.go` |
| Kimi exact-length/entropy checks | `kimi_validation.go` |

## CONVENTIONS
- JSON mutations operate on raw payload bytes; envelope inspection uses `protowire`.
- Self-describing GPT/Claude/Gemini probes precede Kimi's size-based probe.
- Grok is a target-only family: generic detection never identifies it.
- Native Claude expects single-layer E form; Antigravity Claude replay expects double-layer R form.
- Optional provider cache prefixes are parsed separately from payload shape.
- Gemini bypass belongs only on the first synthetic parallel `functionCall`; siblings remain unsigned.
- Function-call pairing validates history order, names, IDs, and response counts.

## ANTI-PATTERNS
- Similar base64 prefixes are not proof that Gemini and Claude signatures share provenance.
- `InspectGrokEncryptedContent` validates transport shape, not provider identity or decryptability.
- Do not send Claude CAIS signatures to Antigravity's R-form replay path.
- Do not attach thought signatures to user `functionResponse` parts.
- Do not replay bare Gemini ASCII UUID envelopes as proven conversation state.
- Do not pin rotating Gemini Tink key IDs in envelope validation.
- Do not interleave parallel calls and responses inside the same content turn.
- Apply the root secret-logging rule to signature diagnostics using paths and lengths.

Run `go test ./internal/signature`; caller regressions span cache, executors, and translators.
