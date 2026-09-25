import assert from 'node:assert/strict';
import { afterEach, test } from 'node:test';
import { accountRequest, AccountRequestError } from '../src/account-api.ts';

afterEach(() => Reflect.deleteProperty(globalThis, 'window'));

function hostResponse(status: number, body: unknown) {
  const calls: unknown[] = [];
  const origin = 'http://localhost:9123';
  Object.defineProperty(globalThis, 'window', { configurable: true, value: {
    parent: { location: { origin } },
    location: { origin, href: `${origin}/v0/resource/plugins/chatgpt2api/index`, pathname: '/v0/resource/plugins/chatgpt2api/index', search: '', hash: '' },
    __CPAMP_PLUGIN_HOST__: { version: 1, pluginID: 'chatgpt2api', resourceURL: `${origin}/v0/resource/plugins/chatgpt2api/index`,
      request: async (input: unknown) => { calls.push(input); return { status, body: JSON.stringify(body), config: { key: 'SYNTHETIC_PRIVATE' } }; } },
  } });
  return calls;
}

test('uses the current keyless bridge for exactly one selected POST', async () => {
  const calls = hostResponse(200, { safe: true });
  const request = { path: 'refresh-web', body: { id: `web_${'a'.repeat(64)}` } } as const;
  assert.deepEqual(await accountRequest(request, new AbortController().signal), { safe: true });
  assert.deepEqual(calls, [{ path: '/v0/management/plugins/chatgpt2api/refresh-web', method: 'POST', body: JSON.stringify(request.body) }]);
});
for (const status of [200, 409, 503, 401]) {
  test(`uses fixed account errors instead of host login or raw body on HTTP ${status}`, async () => {
    const calls = hostResponse(status, { error: 'web_account_stale_reload_needed', config: { key: 'SYNTHETIC_PRIVATE' } });
    await assert.rejects(accountRequest({ path: 'refresh-web', body: { id: 'opaque' } }, new AbortController().signal), (error: unknown) => {
      assert(error instanceof AccountRequestError);
      assert.equal(error.code, 'web_account_stale_reload_needed');
      assert.equal(error.requiresHostLogin, false);
      assert(!JSON.stringify(error).includes('SYNTHETIC_PRIVATE'));
      return true;
    });
    assert.equal(calls.length, 1);
  });
}
test('retains Manager authentication semantics for an actual host 401', async () => {
  hostResponse(401, { error: 'host_request_failed' });
  await assert.rejects(accountRequest({ path: 'webaccounts' }, new AbortController().signal), (error: unknown) => error instanceof AccountRequestError && error.requiresHostLogin);
});
test('blocks old broker denial with actionable integration text instead of a second login', async () => {
  hostResponse(403, { error: 'host_request_failed' });
  await assert.rejects(accountRequest({ path: 'import-codex', body: {} }, new AbortController().signal), (error: unknown) => error instanceof AccountRequestError && !error.requiresHostLogin && error.code === 'host_request_failed');
});
test('does not display unknown HTTP200 body errors as success or leak response payloads', async () => {
  hostResponse(200, { error: 'SYNTHETIC_PRIVATE', safe: true });
  await assert.rejects(accountRequest({ path: 'webaccounts' }, new AbortController().signal), (error: unknown) => error instanceof AccountRequestError && !error.message.includes('SYNTHETIC_PRIVATE'));
});
