# Local Application Sessions

The independently built backend supports authenticated portal handoff at
`POST /v0/management/plugins/gemini-web/login/{start,complete,status,cancel,reconcile}`.
The static portal resource remains credential-free. Native `auth.login.start` and
`auth.login.poll` remain unsupported; the Manager button navigates to the portal
before attempting native login.

This is an implementation contract, not a production rollout receipt. The exact
target core and its disabled-state persistence must pass integration checks before
activation. The repository's current RPC schema 2 is not interchangeable with the
production core's supported schema 6; do not weaken version checks to load a module.

## Bootstrap

Configure `session_dir` (a dedicated absolute directory with mode `0700`),
`manager_origin` (the exact HTTPS origin, without a path), and
`browser_extension_id` (the registered 32-character extension ID). Inject
`GEMINI_WEB_SESSION_KEY` into the host process at bootstrap from the protected
local `/etc/cliproxy/gemini-web/session.key` file. Its value must be canonical base64 encoding of
32 random bytes. The plugin never generates this key, accepts it in configuration,
or returns it. Preserve the existing key and its protected backup: changing it
would make existing encrypted sessions unreadable. No external secret CLI is required.
HTTP origins are accepted only for IP-loopback fixtures.

## Identity And Persistence

Login requires explicit consent and checks the token's account index and the
observed account digest against the private HTTP sidecar's identity response.
The extension ID is an allowlist check, not cryptographic browser attestation.
Existing-account handoff requires a verified local identity or an existing
`maintenance_sources` binding with an explicit account index. Labels and email
addresses are never identity evidence. Existing account IDs and effective disabled
flags must be preserved; enabling an account remains a separate operator action.

New host auth files contain only opaque `session://gemini-web/<32hex>` references
and canonical non-secret metadata. Application-session records use AES-256-GCM,
fresh nonces, authenticated identity/revision/state, `0600` files, and a separate
process-lifetime owner lock. Keep the entire directory, including recovery records,
on durable local storage. Do not edit files or delete an intent/owner file to clear
a failure. Corrupt records, wrong keys, uncertain writes, and ambiguous cookie
rotation or Omni submission fail closed and require operator investigation.

The token and one fixed target host revision are persisted before host saving.
Explicit reconcile reads the host first and never repeats an already-applied save
or increments its revision on retry. `saved` means storage was verified;
`models_ready` additionally requires fresh canonical host and supported model
evidence, and remains false for disabled accounts. It is not an assertion that an
unobserved global host registry or PostgreSQL store has durably converged. The
portal separately checks the authenticated per-account host model registry.

Local sessions use direct HTTP execution and scheduled HTTP maintenance without
CDP recovery or Vault reads/writes. Legacy external-secret references are rejected;
use the authenticated login handoff rather than fabricating replacement records.
Drain active calls before changing the store/key/origin/extension configuration
and restarting. Browser installation, human Google login/2FA, bootstrap injection,
and target-host persistence/model-registration smoke tests remain deployment gates.
