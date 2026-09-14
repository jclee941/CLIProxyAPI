import { source, disabledSource, webAccount, webID, secondWebID, observedAt } from './account-fixtures.ts';

export const chatgptPrefix = '/v0/management/plugins/chatgpt2api/';
export function chatgptFixture() {
  const state = {
    capability: true, omitCapability: false, sources: [source, disabledSource],
    accounts: [webAccount, { ...webAccount, id: secondWebID, label: 'Web dddddddddddd', disabled: false, status: 'normal', source_type: 'web',
      tracked_image_remaining: null, observed_image_remaining: null, reset_after_seconds: null }],
    errorPath: '', errorCode: '', errorStatus: 503, refreshMode: 'success', malformed: false,
    imports: new Map(), mutations: [],
  };
  const handle = (path, method, text) => {
    const action = path.slice(chatgptPrefix.length);
    if (state.errorPath === action) return { status: state.errorStatus, payload: { error: state.errorCode, config: { key: 'SYNTHETIC_PRIVATE_RESPONSE' } } };
    if (action === 'codex-sources' && method === 'GET') return { status: 200, payload: { sources: state.sources } };
    if (action === 'webaccounts' && method === 'GET') return { status: 200, payload: state.malformed ? { accounts: [] } : {
      accounts: state.accounts, observed_at: observedAt, source: 'ChatGPTWeb',
      ...(state.omitCapability ? {} : { capabilities: { preserve_disabled_accounts: state.capability } }),
    } };
    let input;
    try { input = JSON.parse(text); } catch { return { status: 400, payload: { error: 'invalid_account_request' } }; }
    if (method !== 'POST' || !input || typeof input !== 'object') return { status: 400, payload: { error: 'invalid_account_request' } };
    if (action === 'import-codex') {
      const selected = state.sources.find((entry) => entry.id === input.id);
      if (!selected || input.consent !== true || (selected.disabled && input.allow_disabled_source !== true)) return { status: 409, payload: { error: 'disabled_source_requires_override' } };
      if (!state.capability) return { status: 409, payload: { error: 'vendor_disabled_import_unsupported' } };
      const present = state.imports.get(input.id);
      if (present) return { status: 200, payload: { status: 'already_present', account: state.accounts.find((entry) => entry.id === present) } };
      const account = { ...webAccount, id: `web_${(selected.disabled ? 'f' : 'e').repeat(64)}`, label: `Web ${(selected.disabled ? 'f' : 'e').repeat(12)}` };
      state.accounts.push(account); state.imports.set(input.id, account.id); state.mutations.push({ action, ...input });
      return { status: 200, payload: { status: 'imported_disabled', account } };
    }
    const index = state.accounts.findIndex((entry) => entry.id === input.id);
    if (index < 0) return { status: 409, payload: { error: 'web_account_stale_reload_needed' } };
    const selected = state.accounts[index];
    if (action === 'refresh-web') {
      state.mutations.push({ action, ...input });
      if (state.refreshMode === 'rotation') {
        state.accounts[index] = { ...selected, id: `web_${'9'.repeat(64)}`, label: 'Web 999999999999' };
        return { status: 409, payload: { error: 'web_account_stale_reload_needed' } };
      }
      const account = state.refreshMode === 'body-error' ? { ...selected, refresh_error: 'vendor_refresh_failed' }
        : { ...selected, observed_image_remaining: 11, tracked_image_remaining: 5, observation_source: 'conversation/init', observed_at: observedAt, refresh_error: null };
      state.accounts[index] = account;
      return { status: 200, payload: { account: state.refreshMode === 'wrong-id' ? { ...account, id: secondWebID } : account, observed_at: observedAt, source: 'ChatGPTWeb' } };
    }
    if (action === 'set-web-enabled') {
      if (input.consent !== true || typeof input.enabled !== 'boolean') return { status: 400, payload: { error: 'invalid_web_enabled_request_require_consent' } };
      state.mutations.push({ action, ...input });
      const account = { ...selected, disabled: !input.enabled, status: input.enabled ? 'normal' : 'disabled', observation_source: 'stored_snapshot', observed_at: null };
      state.accounts[index] = account;
      return { status: 200, payload: { account, observed_at: observedAt, source: 'ChatGPTWeb' } };
    }
    return { status: 404, payload: { error: 'not_found' } };
  };
  return { state, handle, webID };
}
