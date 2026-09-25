# ChatGPT2API Manager Resource

## 1. Intent And Reference
Preserve the existing CPA-Manager-Plus v1.12.11 resource surface, not a new
dashboard. The reference is `../gemini-web-plugin/DESIGN.md` and its actual
`web/src/{auth,dom,main,account-card,token-dialog}.ts`, `tokens.css`, and
`styles.css`. There are no separate host-auth or host-style source files there.
Keep the quiet blue accent, compact Korean copy, neutral translucent cards,
and existing local font stack. No new branding, navigation, credential editor,
or generation controls. Preserve status inspection and parent provider settings;
extend this resource with safe Codex selection, consented access-only import,
independent Web quota inspection and explicitly confirmed Web-only state control.

## 2. Color And Host Bridge
Bundle the existing Gemini `tokens.css` unchanged as the shared token source.
Its lower-priority `fallback` layer cannot override the live
`#cpamp-plugin-host-style` injection on `html[data-cpamp-plugin-host='true']`.
Use `--bg-primary`, `--glass-bg`, `--glass-border`, `--surface-subtle`,
`--text-primary`, `--text-secondary`, `--border-color`, `--primary-color`,
`--primary-solid`, and `--data-badge-{neutral,success,warning,danger}-*`.
Light/dark fallbacks, readable badge text mixes, and button contrast are the
shared reference values. Do not create a competing theme or change host CSS.

## 3. Typography
Use `--gw-font`, backed by `--cpamp-plugin-font-family` and the existing local
Inter/system/Korean fallback stack. No downloads. Shared sizes are title 24,
heading 18, body 14, small 13, caption 12px; line heights 1.3 and 1.5.
Use semantic headings, tabular numbers, `word-break: keep-all`, and
`overflow-wrap: anywhere` for returned model names. All API values are text nodes.

## 4. Spacing And Layout
Use shared `--gw-space-{1,2,3,4,5,6,8}` (4/8/12/16/20/24/32px), 24px page
gutters, 16px at <=768px, `--app-gap` 20px and `--app-card-padding` 24px.
The iframe document owns vertical scrolling; Manager owns its bounds and
outer chrome. A wrapping heading/action row precedes status feedback and one
resource card, then source and Web account sections. Definition rows and model
chips wrap naturally at 375px. Account rows remain a vertical list, not a metric grid.
No fixed height, nested scroll area, sidebar, chart, or dashboard metric grid.

## 5. Primitives And States
Reuse the reference's typed DOM creation and local refresh SVG. Native button
and anchor use the reference `.btn` anatomy, 40px height (44px on mobile),
14px horizontal padding, 12px radius, hover/active/disabled states, and 2px
focus outline. Card: 20px radius, host translucent fill and thin border, no
new shadow. Text status pill and model chip: host badges, 999px/8px radii.
One live status region distinguishes loading, healthy, upstream abnormal,
missing/expired Manager login, malformed response, and request failure.
No cached healthy snapshot remains visible after a failed refresh.
Account cards reuse the card, heading, definition-row, badge and button anatomy.
Native checkbox labels and a native confirmation dialog reuse Gemini's
`--gw-dialog-width`, `--gw-backdrop`, `--app-input-bg`, spacing and focus tokens.
The dialog has no animation, keeps Tab inside, restores trigger focus on close,
and starts every consent/disabled-source override unchecked. The existing native
dialog pattern takes precedence over beui's center-morph-modal motion; only its
focus containment, Escape and trigger-restoration mechanisms are relevant here.

## 6. Data And Interaction
One initial status GET and one safe source/inventory GET each. List reload is
separate from per-account upstream Web refresh. No intervals, automatic quota
polling, retries, redirects, generation, bulk import, or automatic enablement.
Re-read the host transport on each action and abort in-flight work on pagehide.
The static resource contains
no runtime health data or credentials. Parse required identity/routing fields,
boolean health, string model arrays, and nonnegative safe-integer counters.
Absent/null optional account numbers are never zero. Count only returned model
names; route_count means routing selections, not successful requests.
Unlisted models retain existing routing; the auto/shared-model reminder is
qualified by absence from the returned list. Unknown fields and raw error
bodies are discarded. Upstream failure uses fixed local copy, not error text.
The only link is `/management.html#/ai-providers`, `target="_top"`, no query.
The scoped account API supplies only opaque hashed IDs and safe labels. Import
copies one access snapshot server-side, never ID/refresh tokens, and always
creates a disabled Web target; a duplicate preserves its current target state.
Disabled Codex sources require a second explicit override and a risk notice.
Codex source state and files are never changed. Enable/disable is a separate
consented action on one Web target, never the second half of import or refresh.
Unknown/missing vendor capability blocks actions with upgrade guidance. Unknown
target state is not enabled. Errors require explicit list reload/reselection;
uncertain mutations are not replayed. Fixed error codes select local safe copy.
Show stored image tracking separately from conversation/init image observations,
with counts in images, nullable unknowns, relative reset seconds, actual source
and observation timestamp. Inventory retrieval time is not quota observation time.
No percentages, Codex wham windows, weekly totals or invented reset dates. Failed
refresh retains prior quota only as stale evidence; a failed listing never looks
like an empty account inventory. Toggle does not claim a fresh quota measurement.

## 7. Authentication And Motion
Prefer the existing keyless `readHostRequest` capability: the current Manager
key stays only in the parent. Retain the tested `web/src/host-auth.ts` adapter
only as the existing Remember-on/standalone fallback. Its `cli-proxy-auth`
secureStorage envelope is Zustand `{state,version}`, plaintext or `enc::v1::` base64/XOR with the
host/UA-derived public salt. Only `state.managementKey` and same-origin apiBase
are consumed. No global aliases, React internals, URL credentials, storage
writes, or second login form. Full Manager mode supplies the Manager admin key;
core mode uses the same contract. Missing saved credentials and HTTP 401/403
tell the user to log in through Manager and then manually refresh.
Use only existing 150ms control opacity feedback; no decorative animation.
Reduced motion removes transitions. No polling or spinner loop.

## 8. Accessibility And Accepted Boundaries
Korean semantic labels, visible keyboard focus, 44px mobile actions, readable
status text independent of color, polite live announcements, and no horizontal
overflow are required. Loading disables repeat requests without replacing the
focused button. Failure clears data and explains recovery without reflecting
headers, response bodies, or credentials. No secrets in DOM/logs/screenshots.
Local QA uses isolated loopback-only MOCK host/API data at 375, 768 and 1280px,
including light/dark, success, refresh, 401, down, malformed data, and keyboard.
The operator persona must distinguish source from target, consent separately,
and recover from unknown/stale results without unintended writes. Keyboard-only
operators must complete or cancel every confirmation and retain visible focus.
New built-Manager/built-resource QA covers Remember off with synthetic data only.
Every screenshot is explicitly labeled MOCK, never production evidence.
The coordinator owns production Manager/CPA integration. No live browser
profiles, 1Password, SSH, deployment, independent review panels, or unrelated
Lighthouse/dev tooling are used in this narrowly scoped implementation.
