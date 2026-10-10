# Google Flow Management Resource Design

## 1. Shared Design System & Token Reuse
This plugin resource shares the visual identity and DOM primitive components established by the `gemini-web` management portal. It reuses the exact CPA Manager Plus (CPAMP) injected theme tokens without introducing new colors, fonts, or competing CSS systems.
- **Colors:** Fallbacks and live injections use `--cpamp-plugin-host` and `--glass-bg`, `--app-surface-strong`, `--primary-solid`, etc. No hardcoded hex values are used for new components.
- **Typography:** Uses `--cpamp-plugin-font-family`.
- **Primitives:** Button, badge, and icon components from `deploy/gemini-web-plugin/web/src/dom.ts` are reused entirely.

## 2. Google Flow Frontend Components
The frontend code for this resource lives in `deploy/gemini-web-plugin/web/src/flow/` and is bundled into `deploy/flow2api-plugin/web/index.html` by the shared `build.ts` infrastructure.

### Data Flow
- One authenticated GET to `/v0/management/plugins/flow2api/accounts` loads or refreshes all configured accounts.
- There is no separate refresh mutation or per-account refresh endpoint.

### UI State and Display
- **Summary Header:** Shows account count, successful current observations, their credit subtotal, and the number not currently confirmed. An unknown subtotal is not zero.
- **Account Cards:**
  - Status pills describe lookup state, not a promise of generation availability. Raw Google tier codes are labelled as codes, not inferred billing subscriptions.
  - The optional default Flow project links only to `https://flow.google.com/project/{uuid}`. Its absence does not mean the account has no projects.
  - Prominent display of `credits` remaining.
  - The supported model catalogue is shown once, separately from account eligibility and affordability.
  - No speculative or fabricated reset times or monthly bounds; values rely strictly on `observed_at` and known limits.

### Interaction
- One manual refresh action rechecks the complete snapshot. Duplicate requests are disabled while loading.
- Failed account checks retain the last observed balance and time with an explicit stale label. Operator authentication failure hides account data.
- No automated UI polling.

## 3. Build & Architecture
- Reuses `host-request.ts` (parameterized for `flow2api`) to bridge CPAMP token persistence.
- Reuses generic HTTP handlers in `api.ts`.
- `build.ts` dynamically packages the `/flow/` entrypoint into an IIFE and injects it into a single `index.html` alongside the shared `styles.css` and `tokens.css`.

## 4. Layout and Accessibility
The existing Manager owns application navigation. This one-view resource has no second sidebar or application shell. The iframe document owns scrolling. Existing page, card, cluster, heading, and status primitives wrap at narrow widths.

The credit amount reuses `--gw-text-title` at weight 700 with tabular figures. All other spacing, typography, colors, focus indicators, and reduced-motion behavior inherit the Gemini resource contract. Labels preserve Korean word boundaries and long IDs may wrap. The manual authentication field has a visible label and remains masked.

Validate fresh, zero, unknown, busy, expired, stale, empty, network-error, and missing-host-authentication states. Inspect phone and desktop widths in both light and dark themes. No real credentials are stored in fixture screenshots.
