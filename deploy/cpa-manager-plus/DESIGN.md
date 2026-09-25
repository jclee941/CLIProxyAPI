# Manager GeminiWeb Patch Design Contract

## 1. Identity
Preserve CPA Manager Plus v1.12.11's existing operational UI. This is an
integration, not a redesign. The existing OAuth card and native account Quota
tab remain the entry points; Google login belongs to the plugin portal.

## 2. Color
Reuse `styles/themes.scss` and the account page's semantic aliases:
`--bg-primary`, `--text-primary`, `--text-secondary`, `--border-color`,
`--accounts-surface`, `--accounts-text`, and `--accounts-muted`. No new palette.

## 3. Typography
Preserve the upstream font stack and component scales: Card title 18px,
section title 15px, Button 14px (small 13px), account field value 12px.
No new fonts or typography overrides.

## 4. Spacing And Layout
Reuse SCSS spacing xs/sm/md/lg/xl = 4/8/16/24/32px and the existing
`quotaSection`, `quotaCardList`, `quotaTabActions`, and `overviewFieldGrid`.
The native detail drawer retains scroll ownership and responsive behavior.

## 5. Primitives
Use upstream `Card`, `Button`, the OAuth provider card, and definition-list
field grid. Preserve Button primary/secondary, focus, hover, disabled and
loading states. Render dynamic metrics as text fields, not normalized quota
bars. Empty/unknown values are explicit; failure uses the existing error box.

## 6. Interaction
Login means "Continue in GeminiWeb plugin", never native OAuth success.
Validate support, enabled plugin/menu and resource availability before routing.
Quota load and refresh are explicit clicks only. No timers, auto-enable,
credential changes, second key prompt, or extra key storage.

## 7. Surfaces
Reuse upstream card borders, radii, theme surfaces and existing motion.
No CSS or animation additions are needed.

## 8. Accessibility And Boundaries
Keep semantic buttons and definition lists, visible keyboard focus, and
actionable error/status text. Long IDs, opaque tiers and model names wrap in
the existing grid. Check 375/768/1280px using synthetic local fixtures.
Existing upstream typography and layout are explicitly preserved by the task;
no unrelated accessibility, performance, or developer-tooling redesign is included.

## Request Bridge And Registration State

Keep all existing surfaces and tokens. A connected Manager with Remember off
can load its registered plugin resources without a second key or persistence.
The invisible in-memory request capability is independent of the style bridge.
The Gemini portal's existing warning/ready states now require authenticated host
model-registry evidence. Saved but empty/failed registration uses the existing
warning badge and explicit Check Status action, never a premature usable badge.
No new layout, animation, theme or primitive is introduced.
