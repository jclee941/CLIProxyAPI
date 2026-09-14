# Gemini Web Login Companion

## 1. Intent And Reference

This scope extends [the parent design contract](../DESIGN.md), not the Manager
or portal. Preserve its compact Korean operational UI, quiet blue, neutral
layered surfaces and native controls. The memorable element is the explicit
destination followed by a single account-index selection, never a secret field.
Inspected the parent contract and the existing button, badge, metric, account
card, state panel and token dialog primitives in `web/src/` (read-only).
No new brand, fonts, artwork, React runtime or decorative animation.

## 2. Color

`tokens.css` owns all raw colors. Use the parent light/dark values for
`--bg-primary`, `--bg-secondary`, `--glass-bg`, `--app-surface-strong`,
`--surface-subtle`, `--text-primary`, `--text-secondary`, `--border-color`,
`--primary-solid`, `--primary-solid-hover`, `--primary-color` and
`--data-badge-danger-{text,bg,border}`. Button contrast is white in light mode
and the dark canvas in dark mode. Native controls follow `prefers-color-scheme`.
The stronger control border is parent secondary text, distinct from card edges.

## 3. Typography

Use the parent's local Inter / Apple / BlinkMacSystemFont / Segoe UI / sans-serif
stack, without downloading fonts. Title 24px/700, heading 18px/700, body and h3/h4
14px (400/600), small 13px, caption 12px. Line heights 1.5 and headings 1.3.
All are `--gw-*` tokens. Korean uses `word-break: keep-all`; identifiers may wrap
anywhere. Display only a fixed destination, numeric tab ID and account index.
Never display tab titles, conversation paths, query strings, Gaia or digests.

## 4. Spacing And Layout

Use the parent 4/8/12/16/20/24/32px `--gw-space-*` scale. Control minimum height
44px at every width. Other dimensions: 14px button padding, 16px radio size,
1px border, 2px focus outline/offset, 520px content/dialog maximum, 720px initial
window height (window chrome included). Body has 16px gutters; card padding 24px.
The document owns the only scroll. No fixed footer or nested scrolling list.
The named [clamped-card pattern](https://github.com/changeroa/StyleGallery/blob/main/patterns/containment/clamped-card.md)
constrains width without reordering focus; stack and wrapping cluster handle
content. Check 375/768/1280px and the actual extension window.

## 5. Primitives And States

- Card: bordered strong surface, vertical stack; all states use the same shell.
- Destination: small label + origin; visible before permission or consent.
- Choice: labelled native radio in a bordered row, tab ID + account index;
  checked, focus and disabled states; initially none selected, even one choice.
  Keep the account-index label and number together; wrap that whole group to a
  second line when the window is narrow, never orphan the account number.
- Consent: independent, initially unchecked native checkbox; explicit disclosure
  that the selected Google session will go only to this portal once.
- Button: primary approve; secondary reload/open Gemini/cancel; native disabled,
  keyboard focus, hover and pressed feedback. Approval requires both selections.
- Notice: polite status or alert, fixed safe text; waiting, no tabs, permission
  required, capture in progress, delivered/awaiting portal, finished, failure.

## 6. Interaction

Opening a Gemini tab never selects it or approves capture. Google handles login
and 2FA. The user returns and refreshes the list. Refresh resets selection and
consent. Closing the companion cancels the request; Escape cancels explicitly.
The isolated extension window stays open when focus moves to Google. It binds
to exactly one originating portal port; another window cannot approve it.
Primary control feedback uses the parent's 150ms ease transform/opacity, disabled
opacity .62, one border-width lift; reduced motion disables transitions.
No spinner, background animation, automatic capture, popup token or clipboard.

## 7. Surface

Parent 8/12/20px radius tokens for choices/buttons/card. Canvas and strong card
surface provide depth with thin borders, no shadows, gradients or background
images. No remote assets. CSP denies network connections and framed embedding.

## 8. Accessibility And Verification Boundaries

Native radios, checkbox, buttons, fieldset/legend and heading order support
keyboard and screen readers. Focus is visible, status is textual, no color-only
meaning. Test an operator switching among multiple accounts, a keyboard-only
operator cancelling, and a narrow-window Korean reader. Secrets never enter UI,
console, URL, storage or traces. Tests and screenshots use synthetic fixtures.
The state harness is the actual companion driven through its runtime port,
covering empty/choice/consented/pending/error/finished in light and dark modes.
Chrome extension pages are not crawlable HTTP documents: no Lighthouse/SEO score
is claimed. Human Google login/2FA and production portal completion belong to
the integration owner; no production browser is accessed here.
