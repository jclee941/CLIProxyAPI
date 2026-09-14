import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { chatgptFixture, chatgptPrefix } from '../../chatgpt2api-plugin/web/tests/accounts-fixture.mjs';

export const accountId = 'gemini-web-mock-bridge.json';
export const managerKey = 'SYNTHETIC_MANAGER_MEMORY_ONLY';
export const rotatedKey = 'SYNTHETIC_MANAGER_ROTATED';
export const geminiPath = '/v0/management/plugins/gemini-web/';
export const resourcePath = '/v0/resource/plugins/gemini-web/index';
export const measured = {
  id: accountId, label: 'MOCK Gemini account', enabled: true, status: 'ready', observed_at: 1800000000,
  models: [{ id: 'gemini-web-omni', name: 'MOCK registered model' }],
  usage: { tier: 'MOCK opaque tier', tier_code: 'opaque-42', source: 'GoogleWeb', estimated: false, observed_at: 1800000000,
    metrics: [
      { remaining_units: 71.25, usage_fraction: 0.25, reset_unix_seconds: null, window_kind: 'ai_credit', unit: 'provider_compute_unit' },
      { remaining_units: null, usage_fraction: null, reset_unix_seconds: null, window_kind: 'unknown', unit: 'provider_compute_unit' },
    ],
  },
};

export async function fixture(vendor) {
  const manager = await readFile(resolve(vendor, 'apps/web/dist/index.html'));
  const gemini = await readFile(new URL('../../gemini-web-plugin/web/index.html', import.meta.url));
  const chatgpt = await readFile(new URL('../../chatgpt2api-plugin/web/index.html', import.meta.url));
  const calls = [];
  const waiting = new Set();
  const chatgptAccounts = chatgptFixture();
  const state = { key: managerKey, account: measured, models: measured.models, modelStatus: 200, resource: resourcePath,
    pluginEnabled: true, hold: '', aborted: 0, loginStatus: 'pending', loginStarts: 0, loginCompletes: 0,
    registrationStatus: 200, chatgpt: chatgptAccounts.state };
  const server = createServer(async (request, response) => {
    const url = new URL(request.url, 'http://localhost');
    let body = '';
    for await (const chunk of request) body += String(chunk);
    const path = url.pathname;
    if (path === '/' || path === '/management.html') {
      response.writeHead(200, { 'content-type': 'text/html' }); response.end(manager); return;
    }
    if (path.startsWith('/v0/resource/')) {
      response.writeHead(200, { 'content-type': 'text/html' });
      response.end(path === resourcePath ? gemini : path === '/v0/resource/plugins/chatgpt2api/index' ? chatgpt : '<!doctype html><title>MOCK unregistered document</title>');
      return;
    }
    const headers = { 'content-type': 'application/json', 'x-cpa-support-plugin': 'true', 'x-cpa-version': 'v7.2.157' };
    const authorized = request.headers.authorization === `Bearer ${state.key}`;
    calls.push({ method: request.method, path: `${path}${url.search}`, authorized,
      credential: request.headers.authorization === `Bearer ${rotatedKey}` ? 'rotated' : authorized ? 'initial' : 'none',
      origin: request.headers.origin ?? null, body: path.endsWith('/complete') ? '[synthetic session omitted]' : body });
    if (path.startsWith('/v0/management/') && !authorized) {
      response.writeHead(401, headers); response.end(JSON.stringify({ error: 'MOCK authorization required' })); return;
    }
    if (state.hold && path === state.hold) {
      await new Promise((release) => {
        waiting.add(release);
        response.on('close', () => { state.aborted++; waiting.delete(release); release(); });
      });
    }
    if (path === `${geminiPath}redirect`) {
      response.writeHead(302, { location: '/v0/management/config' }); response.end(); return;
    }
    if (path.startsWith(chatgptPrefix) && path !== `${chatgptPrefix}status`) {
      const result = chatgptAccounts.handle(path, request.method, body);
      response.writeHead(result.status, headers); response.end(JSON.stringify(result.payload)); return;
    }
    const plugin = (id) => ({ id, oauth_provider: id, configured: true, registered: true, enabled: state.pluginEnabled,
      effective_enabled: state.pluginEnabled, supports_oauth: id === 'gemini-web', metadata: { name: id },
      menus: [{ menu: id, path: id === 'gemini-web' ? state.resource : '/v0/resource/plugins/chatgpt2api/index' }] });
    let payload = {};
    let status = 200;
    switch (path) {
      case '/usage-service/info': payload = { service: 'cpa-manager-plus', configured: true, projectInitialized: true, setupRequired: false }; break;
      case '/v0/management/config': payload = { 'api-keys': [], plugins: { enabled: true } }; break;
      case '/v0/management/plugins': payload = { plugins_enabled: true, plugins: [plugin('gemini-web'), plugin('chatgpt2api')] }; break;
      case '/v0/management/auth-files': payload = { files: [{ id: accountId, name: accountId, type: 'gemini-web', provider: 'gemini-web', label: measured.label, status: 'ready' }] }; break;
      case `${geminiPath}accounts`:
        if (request.method === 'POST') { status = state.registrationStatus; payload = { error: 'account_unavailable' }; }
        else payload = { provider: 'gemini-web', accounts: [state.account] }; break;
      case `${geminiPath}refresh`: payload = state.account; break;
      case '/v0/management/auth-files/models': payload = { models: state.models }; status = state.modelStatus; break;
      case `${geminiPath}login/start`:
        state.loginStarts++; state.loginStatus = 'pending';
        payload = { state: 'MOCK_LOGIN_NONCE', status: 'pending', manager_origin: `http://${request.headers.host}`,
          expires_at: Math.floor(Date.now() / 1000) + 600, extension_id: 'a'.repeat(32) }; break;
      case `${geminiPath}login/complete`:
        state.loginCompletes++; state.loginStatus = 'ready';
        payload = { state: 'MOCK_LOGIN_NONCE', status: 'ready', account_id: accountId, models_ready: true }; break;
      case `${geminiPath}login/status`:
        payload = { state: 'MOCK_LOGIN_NONCE', status: state.loginStatus, account_id: accountId, models_ready: true }; break;
      case `${geminiPath}failure`:
        status = 503; payload = { error: state.key, config: { headers: { Authorization: state.key } } }; break;
      case '/v0/management/plugins/chatgpt2api/status':
        payload = { plugin: { id: 'chatgpt2api', version: '1.0.1', route_count: 17 },
          routing: { provider: 'chatgpt2api', mode: 'native-provider', model_names: ['MOCK-model'], credential_source: 'host-auth-manager' },
          upstream: { healthy: true, accounts: { active: 4, total: 5 } } }; break;
    }
    response.writeHead(status, headers); response.end(JSON.stringify(payload));
  });
  await new Promise((resolveListen) => server.listen(0, '127.0.0.1', resolveListen));
  const address = server.address();
  assert(address && typeof address === 'object');
  return { state, calls, base: `http://127.0.0.1:${address.port}`,
    release: () => { state.hold = ''; for (const release of waiting) release(); waiting.clear(); },
    close: async () => { for (const release of waiting) release(); server.closeAllConnections(); await new Promise((resolveClose) => server.close(resolveClose)); },
  };
}
