import assert from 'node:assert/strict';
import { accountId, geminiPath, managerKey, resourcePath, rotatedKey } from './plugin-request-fixture.mjs';

const call = (frame, input) => frame.locator('html').evaluate((_root, input) => window.__CPAMP_PLUGIN_HOST__.request(input), input);
const waitReady = (frame, expect) => expect(frame.locator('.account-card[data-account-id]')).toHaveCount(1);

export async function runSecurityCases(scenario) {
  await scenario('google-401-does-not-log-out-the-manager', async ({ host, page, frame, open, expect }) => {
    await open(); await waitReady(frame, expect);
    host.state.registrationStatus = 401;
    await frame.locator('#add-account').click();
    await frame.locator('#account-label').fill('MOCK expired session');
    await frame.locator('#account-token').fill('gemini-web:v1:TU9DSw');
    await frame.locator('dialog button[type="submit"]').click();
    await expect(frame.locator('#token-error')).toContainText('웹 토큰이 만료');
    assert.equal(await page.locator('input[type="password"]').count(), 0);
    assert.equal((await call(frame, { path: `${geminiPath}accounts`, method: 'GET' })).status, 200);
  });
  await scenario('scope-denials-and-safe-error-envelope', async ({ host, frame, open, expect }) => {
    await open(); await waitReady(frame, expect);
    const before = host.calls.length;
    const denied = [
      { path: `${host.base}${geminiPath}accounts`, method: 'GET' },
      { path: `//example.invalid${geminiPath}accounts`, method: 'GET' },
      { path: '/v0/management/plugins/chatgpt2api/status', method: 'GET' },
      { path: `${geminiPath}../config`, method: 'GET' },
      { path: `${geminiPath}%2e%2e/config`, method: 'GET' },
      { path: `${geminiPath}%252e%252e/config`, method: 'GET' },
      { path: `${geminiPath}%2faccounts`, method: 'GET' },
      { path: `${geminiPath}..\\config`, method: 'GET' },
      { path: `${geminiPath}accounts?url=https://example.invalid`, method: 'GET' },
      { path: `${geminiPath}accounts`, method: 'DELETE' },
      { path: `${geminiPath}accounts`, method: 'PUT' },
      { path: `${geminiPath}accounts`, method: 'GET', headers: { 'X-Extra': 'untrusted' } },
      { path: `${geminiPath}accounts`, method: 'GET', body: '{}' },
      { path: '/v0/management/auth-files/models?name=codex.json', method: 'GET' },
      { path: '/v0/management/auth-files/models?name=gemini-web-unobserved.json', method: 'GET' },
      { path: `/v0/management/auth-files/models?name=${accountId}&extra=1`, method: 'GET' },
      { path: `/v0/management/auth-files/models?name=${accountId}`, method: 'POST' },
    ];
    for (const input of denied) assert.equal((await call(frame, input)).status, 403, JSON.stringify(input));
    assert.equal(host.calls.length, before);
    const failure = await call(frame, { path: `${geminiPath}failure`, method: 'GET' });
    assert.deepEqual(failure, { status: 503, body: '{"error":"host_request_failed"}' });
    const configCalls = host.calls.filter((entry) => entry.path === '/v0/management/config').length;
    assert.equal((await call(frame, { path: `${geminiPath}redirect`, method: 'GET' })).status, 502);
    assert.equal(host.calls.filter((entry) => entry.path === '/v0/management/config').length, configCalls);
  });

  await scenario('repeated-ready-style-and-unknown-messages-do-not-requery', async ({ host, page, frame, open, expect }) => {
    await open(); await waitReady(frame, expect);
    const before = host.calls.filter((entry) => entry.path.startsWith(geminiPath)).length;
    await frame.locator('html').evaluate(() => {
      for (let index = 0; index < 12; index++) {
        window.dispatchEvent(new Event('cpamp-plugin-request-ready'));
        window.postMessage({ type: 'unknown', path: '/v0/management/config' }, location.origin);
        document.head.append(document.createElement('style'));
      }
    });
    await page.clock.install(); await page.clock.fastForward(60_000);
    assert.equal(host.calls.filter((entry) => entry.path.startsWith(geminiPath)).length, before);
  });

  await scenario('unregistered-sibling-frame-has-no-capability', async ({ page, frame, open, expect }) => {
    await open(); await waitReady(frame, expect);
    const available = await page.evaluate(async (path) => {
      const sibling = document.createElement('iframe'); sibling.src = path;
      const loaded = new Promise((resolveLoad) => sibling.addEventListener('load', resolveLoad, { once: true }));
      document.body.append(sibling); await loaded;
      const granted = Boolean(sibling.contentWindow.__CPAMP_PLUGIN_HOST__);
      sibling.remove(); return granted;
    }, resourcePath);
    assert.equal(available, false);
  });

  for (const target of ['reload', 'document', 'pushState', 'replaceState', 'unmount', 'logout']) {
    await scenario(`revokes-and-aborts-on-${target}`, async ({ host, page, frame, open, expect }) => {
      await open(); await waitReady(frame, expect);
      host.state.hold = `${geminiPath}slow`;
      await page.evaluate((path) => {
        window.__qaRetained = document.querySelector('iframe').contentWindow.__CPAMP_PLUGIN_HOST__;
        window.__qaPending = window.__qaRetained.request({ path, method: 'GET' });
      }, host.state.hold);
      await expect.poll(() => host.calls.some((entry) => entry.path === host.state.hold)).toBe(true);
      if (target === 'logout') await page.getByRole('button', { name: 'Logout', exact: true }).click();
      else if (target === 'unmount') await page.evaluate(() => { location.hash = '#/oauth'; });
      else await page.evaluate((target) => {
        const iframe = document.querySelector('iframe');
        if (target === 'reload') iframe.contentWindow.location.reload();
        else if (target === 'pushState' || target === 'replaceState') iframe.contentWindow.history[target](null, '', '/v0/resource/plugins/gemini-web/other');
        else iframe.src = '/v0/resource/plugins/gemini-web/other';
      }, target);
      await expect.poll(() => host.state.aborted).toBe(1);
      assert.equal((await page.evaluate(() => window.__qaPending)).status, 401);
      host.release();
      const after = host.calls.filter((entry) => entry.path.startsWith(geminiPath)).length;
      const result = await page.evaluate((path) => window.__qaRetained.request({ path, method: 'GET' }), `${geminiPath}accounts`);
      assert.equal(result.status, 401);
      if (target !== 'reload') assert.equal(host.calls.filter((entry) => entry.path.startsWith(geminiPath)).length, after);
      else { await waitReady(frame, expect); assert.equal((await call(frame, { path: `${geminiPath}accounts`, method: 'GET' })).status, 200); }
    });
  }

  await scenario('parent-key-rotation-uses-new-auth-and-revokes-old-document', async ({ host, page, frame, open, login, expect }) => {
    await open(); await waitReady(frame, expect);
    await page.evaluate(() => { window.__qaRetained = document.querySelector('iframe').contentWindow.__CPAMP_PLUGIN_HOST__; });
    await page.getByRole('button', { name: 'Logout', exact: true }).click();
    host.state.key = rotatedKey;
    await login(rotatedKey);
    await page.evaluate(() => { location.hash = '#/plugin-pages/gemini-web/0'; });
    await waitReady(frame, expect);
    assert(host.calls.some((entry) => entry.path === `${geminiPath}accounts` && entry.credential === 'rotated'));
    assert.equal((await page.evaluate((path) => window.__qaRetained.request({ path, method: 'GET' }), `${geminiPath}accounts`)).status, 401);
  });

  for (const resource of ['/v0/resource/plugins/chatgpt2api/index', 'https://wrong-origin.invalid/resource']) {
    await scenario(`registered-resource-rejects-${resource.startsWith('https:') ? 'foreign-origin' : 'wrong-plugin'}`, async ({ host, page, frame, expect }) => {
      host.state.resource = resource;
      await page.goto(`${host.base}/management.html#/plugin-pages/gemini-web/0`);
      await page.locator('input[type="password"]').fill(managerKey);
      await page.getByRole('button', { name: 'Login', exact: true }).click();
      await page.locator('iframe').waitFor();
      if (!resource.startsWith('https:')) await expect(frame.locator('main')).toHaveAttribute('data-state', 'auth-error');
      const granted = await page.evaluate(() => {
        try { return Boolean(document.querySelector('iframe').contentWindow.__CPAMP_PLUGIN_HOST__); } catch { return false; }
      });
      assert.equal(granted, false);
      assert.equal(host.calls.filter((entry) => entry.path.startsWith(geminiPath)).length, 0);
    });
  }

  await scenario('remember-on-bridge-and-legacy-standalone-preserved', async ({ host, page, frame, open, expect }) => {
    await open(); await waitReady(frame, expect);
    await frame.locator('#refresh-all').click();
    await expect(frame.locator('#refresh-all')).toBeEnabled();
    await expect(frame.locator('.badge-model')).toHaveCount(1);
    await page.goto(`${host.base}${resourcePath}`);
    await expect(page.locator('.account-card[data-account-id]')).toHaveCount(1);
    assert.equal(await page.evaluate(() => window.__CPAMP_PLUGIN_HOST__), undefined);
    await page.goto(`${host.base}/v0/resource/plugins/chatgpt2api/index`);
    await expect(page.locator('main')).toHaveAttribute('data-state', 'healthy');
  }, { remember: true });
}
