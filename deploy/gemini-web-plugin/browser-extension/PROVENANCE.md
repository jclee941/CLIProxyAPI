# Vendor Provenance

Source: [Sophomoresty/gemini-web2api, gemini-cookie-sync-extension](https://github.com/Sophomoresty/gemini-web2api/tree/2bb988bfcbb82a7fab5d2c99aa5560ff40d64f7e/gemini-cookie-sync-extension).
Pinned revision: `2bb988bfcbb82a7fab5d2c99aa5560ff40d64f7e`.
Inspected source: `gemini-cookie-sync-extension/popup.js`.

[LICENSE.vendor](LICENSE.vendor) reproduces the pinned repository's root
[MIT license](https://github.com/Sophomoresty/gemini-web2api/blob/2bb988bfcbb82a7fab5d2c99aa5560ff40d64f7e/LICENSE)
verbatim, including `Copyright (c) 2026`.

This typed adaptation reuses the companion concept and Chrome cookies, tabs,
and MAIN-world scripting API patterns. It deliberately does not carry over
the vendor's all-store cookie queries, cross-store scoring, fixed cookie list,
active-tab fallback, HTML/performance scraping, XSRF/build extraction, raw error
display, or plaintext JSON download. Identity comes only from the selected
document's three Gaia fields. The added MV3 worker, strict port protocol and
Korean consent popup are project-specific adaptations, not verbatim vendor UI.
Every build ZIP contains this provenance file and the unmodified MIT license.

API semantics were checked against the official Chrome
[cookies](https://developer.chrome.com/docs/extensions/reference/api/cookies),
[scripting](https://developer.chrome.com/docs/extensions/reference/api/scripting),
and [tabs](https://developer.chrome.com/docs/extensions/reference/api/tabs)
references. In particular, omitted `partitionKey` selects unpartitioned
cookies, not every partition. The selected document's partition must be checked
explicitly; an empty partition filter must never be used to enumerate other
sites' partitions. Chromium's `cookies_helpers.cc` confirms these filters.

The complete companion additionally verified Chrome for Testing 149.0.7827.55
in a new synthetic profile: `tabs.get().frozen === false` and
`cookies.getPartitionKey(...)` returned exactly the first-party Google site and
`hasCrossSiteAncestor: false`. The adapter's frozen and partition schemas were
not relaxed. Chromium's
[extension_tab_util.cc](https://github.com/chromium/chromium/blob/main/chrome/browser/extensions/extension_tab_util.cc)
sets the frozen boolean from the lifecycle unit; its
[cookies_api.cc](https://github.com/chromium/chromium/blob/main/chrome/browser/extensions/api/cookies/cookies_api.cc)
serializes the document partition and checks both site and frame host grants.

Chrome's [storage lifecycle](https://developer.chrome.com/docs/extensions/reference/api/storage)
and an actual owned-profile browser-restart probe showed that `storage.session`
does not retain replay history. Only non-secret nonce digests and expiry times
use `storage.local`; no token, cookie, original nonce or identity is stored.
Context7 was attempted but quota-exhausted; direct official Chrome documentation,
Chromium source and isolated runtime observations supplied the API evidence.
