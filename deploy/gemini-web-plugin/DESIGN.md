# Gemini Web Resource Design Contract

## 1. Atmosphere & Identity

An account-management resource inside the existing CPA-Manager-Plus dashboard,
not a second dashboard. Preserve CPAMP's quiet blue accents, translucent neutral
surfaces, thin borders, rounded controls, and compact operational typography.
No application header, sidebar, chat, video playground, or decorative motion.
The primary task is identifying an expired account and replacing its web token.
All user-facing labels are concise Korean; provider/model identifiers stay exact.

Source of truth: `seakee/CPA-Manager-Plus` v1.12.11,
commit `e1a8788ab796f4d001c5d1e9851c418989b05424`.
Inspected `apps/web/src/styles/{themes,variables,components}.scss`,
`components/ui/{Button,Card,Input,Modal,EmptyState}.tsx`,
`features/accounts/components/{QuotaWindowCard,QuotaProgressBar}.tsx`, and
`features/plugins/{PluginResourcePage,pluginHostStyle}.tsx` (bridge is `.ts`).
The upstream `img/dashboard.png` was viewed as a visual reference for density,
surface hierarchy, type, and control anatomy, not copied as page content.

## 2. Color

The host's injected `#cpamp-plugin-host-style` and
`html[data-cpamp-plugin-host='true']` supply the live CSS custom properties.
Plugin styles must not redefine these properties when the bridge is present.
Standalone fallback tokens reproduce the source theme, not a new palette.

| Role | Host token | Source light fallback | Source dark fallback |
| --- | --- | --- | --- |
| Background | `--bg-primary` | `rgba(255,255,255,.94)` | `rgba(24,28,40,.9)` |
| Canvas | `--bg-secondary` | `#eff2f7` | `#0a0a0a` |
| Card | `--glass-bg` | `#ffffff` | `rgba(24,28,40,.72)` |
| Readable dialog surface | `--app-surface-strong` | `#ffffff` | `#1b1f2a` |
| Subtle surface | `--surface-subtle` | `#f6faff` | `rgba(255,255,255,.06)` |
| Text | `--text-primary` | `#2c3e50` | `#e5e5e5` |
| Secondary text | `--text-secondary` | `#5f6c7b` | `#a3a3a3` |
| Border | `--border-color` | `rgba(15,23,42,.08)` | `rgba(255,255,255,.08)` |
| Primary button | `--primary-solid` | `#2563eb` | `#60a5fa` |
| Primary hover | `--primary-solid-hover` | `#3b82f6` | `#93c5fd` |
| Accent | `--primary-color` | `#3b82f6` | `#60a5fa` |
| Quota track | `--data-track-bg` | `#e2e8f0` | `rgba(255,255,255,.08)` |
| Healthy quota | `--data-green-base` | `#22c55e` | `#4ade80` |
| Warning quota | `--data-amber-base` | `#f59e0b` | `#fbbf24` |
| Exhausted quota | `--data-red-base` | `#ef4444` | `#f87171` |

Status pills use the host's `--data-badge-{success,warning,danger,info,neutral}-`
`{text,bg,border}` families. Text remains readable independently of status color.
Small light-theme warning/success text is mixed toward `--text-primary` for AA
contrast; no unrelated accent color is introduced. Dark primary button text uses
the dark canvas token for contrast against CPAMP's light-blue button fill.

## 3. Typography

Use `--cpamp-plugin-font-family` from the bridge. Its upstream stack is
`Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif`; no font is
downloaded. Inherit the available local font, including local Korean fallback.
No external stylesheets, web fonts, icon fonts, or images.

| Plugin token | Size | Weight | Use |
| --- | --- | --- | --- |
| `--gw-text-title` | 24px | 700 | Resource h1 |
| `--gw-text-heading` | 18px | 700 | Account h2, dialog heading |
| `--gw-text-body` | 14px | 400 / 600 | Body, h3/h4 labels, buttons |
| `--gw-text-small` | 13px | 400 / 600 | Model badges, field hints |
| `--gw-text-caption` | 12px | 400 / 600 | Observation metadata |

Line-height 1.5; headings 1.3. Preserve Korean words with `word-break: keep-all`;
allow long server labels/identifiers to wrap with `overflow-wrap: anywhere`.
Numbers use tabular figures. API-returned strings render only as text nodes.

## 4. Spacing & Layout

Existing spacing grid: 4, 8, 12, 16, 20, 24, 32, 48px, named
`--gw-space-{1,2,3,4,5,6,8,12}`. Host `--app-gap` is 20px and
`--app-card-padding` is 24px. Control height is 40px (44px at <=768px for touch).
Other named dimensions: 14px button horizontal padding, 8px quota track,
16px icons, 520px dialog width, 360px minimum card column, 2px focus ring.

The iframe document owns vertical scrolling. The host owns application chrome
and the iframe's dimensions. No fixed viewport dashboard or nested account-card
scroll areas. Use a wrapping toolbar, summary cluster, and intrinsic grid
`repeat(auto-fit, minmax(min(360px, 100%), 1fr))`. No hardcoded account count.
The dialog owns its bounded overflow; narrow viewport gutters are 16px.
Verify 375, 768, and 1280px widths, long labels, empty data, and errors.

## 5. Components

| Primitive | Anatomy and states | Source |
| --- | --- | --- |
| Button | Native button, local SVG, primary/secondary; hover, active, focus, disabled, loading | CPAMP `Button` / `.btn` |
| Account card | Heading + status/tier cluster, model badges, quota stack, observation metadata, update/refresh actions | CPAMP `Card`, quota components |
| Status pill | Text plus semantic tone; ready, expired, error, unknown; separate disabled pill | CPAMP `.status-badge` |
| Metric | Named window, used percentage, remaining compute units, 8px bar, local reset time | CPAMP `QuotaProgressBar` |
| State panel | Loading, empty, request error, missing host authentication; one actionable recovery control | CPAMP `EmptyState` |
| Token dialog | Native modal dialog, labelled name input, required masked textarea, hints, error region, submit/cancel | CPAMP `Input`, `Modal` |

Only returned models are shown, and only for enabled, ready accounts. Never
invent available models, a Pro tier, quotas, or five account cards. Usage bars
derive from `usage_fraction`, not optional `usage_percent`. Numeric remaining
values are provider compute units, never token counts. Unknown/absent windows
are not presented as unlimited or zero usage. Failed refresh preserves the last
observation with an explicit stale warning. `observed_at` and reset fields are
Unix seconds; timestamps show browser-local timezone. `usage.source` must be
`GoogleWeb` and `estimated` must be false. Refresh does not perform browser login.

## 6. Motion & Interaction

Use CPAMP's 150ms ease control feedback, restricted to transform/opacity.
A loading indicator rotates only during a request. Reduced motion disables it;
loading text remains. No timed API polling, account renewal, or automatic mutation.
Manual refresh-all uses two workers, disables conflicting actions, reports
per-account progress/failure, and continues after individual failures.
The inspected backend returns one account view from refresh; consume that view
directly and verify its ID. Do not follow refresh with a list GET, because the
current backend list operation itself rechecks every account against Google.
Native modal focus containment, Escape, cancel, focus restoration, and keyboard
form submission are required. The token is masked, never revealed or persisted,
cleared immediately after submission and on every close; failed submission asks
for re-entry. Closing during a request does not imply cancelling a server mutation.

## 7. Depth & Surface

Use the host's existing mixed translucent-surface and border treatment.
Cards: `--glass-bg`, `--glass-border`, `--app-radius-lg` (20px), no new shadows.
Inputs: `--app-input-bg`, `--border-color`, `--app-radius-sm` (8px).
Buttons/dialog: `--app-radius-md` (12px). Pills/tracks: 999px. The dialog uses
`--app-surface-strong` so background account text cannot bleed through its form.
Dialog backdrop derives from host text/canvas color with transparency.
No gradients, brand artwork, animated atmosphere, or host stylesheet edits.

## 8. Accessibility Constraints & Accepted Boundaries

WCAG 2.2 AA intent: visible focus, named controls, status text without color
dependence, touch targets, semantic headings, live errors, modal keyboard trap,
reduced motion, and no horizontal overflow. Do not show token/key values in DOM
text, console, notifications, URLs, screenshots, or error messages.

Verified host auth: `useAuthStore.ts` persists Zustand `{state,version}` under
`cli-proxy-auth` via `secureStorage.ts`. `utils/encryption.ts` decodes
`enc::v1::` base64 XOR using UTF-8 bytes of the public obfuscation salt
`cli-proxy-api-webui::secure-storage|${location.host}|${navigator.userAgent}`.
Only `state.managementKey` and same-origin `state.apiBase` are consumed. This is
reversible obfuscation, not a security boundary. No new storage entries or writes.
When `rememberPassword` is false the key is deliberately absent: explain how to
enable saved login in Manager, then offer explicit reconnection. Do not inspect
React internals, guess global aliases, or request a second management key.
Official CPAMC `main` was also inspected: same persistence name/envelope,
obfuscation, and Bearer transport. Only that shared contract is supported.
Cross-origin embedding/configuration is rejected without sending credentials.
HTTP redirects are rejected, preventing credential/token forwarding.
Known backend error codes distinguish an expired Google token from an expired
Manager login. Unrecognized error response text is never reflected. A failed
Manager authentication clears account display and offers explicit reconnection.
The concurrently inspected backend's Go DTO allows null metric numbers/tier and
null metrics, and does not yet emit `metric_type` or `overage_enabled`. Accept
those absent metadata fields without inventing values; support the coordinator's
full frozen shape when present. Required source, unit, identity and status fields
remain validated.

Full Mode Manager authenticates its own admin key and proxies management calls
with its server-side saved CPA key (`internal/http/middleware/auth.go`,
`internal/service/proxy/service.go`). Plugin requests remain same-origin at
`/v0/management/plugins/gemini-web/{accounts,refresh}`. The static HTML resource
at `/v0/resource/plugins/gemini-web/index` contains no accounts or credentials.

| Boundary | Evidence / Owner |
| --- | --- |
| Production Manager/CPA/Google integration | Coordinator; outside this frontend-only task |
| Real secrets and Google profiles | Never accessed; all local test data is marked MOCK |
| Independent agent reviews | Explicitly prohibited by task; local browser QA performed directly |
| Host visual system | Source and upstream screenshot inspected; no production browser opened |
| Lighthouse / unrelated dev tooling | Not installed for this bounded plugin task; no score claimed |
