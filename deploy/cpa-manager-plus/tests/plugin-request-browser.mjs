import assert from 'node:assert/strict';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { fixture, geminiPath, managerKey, measured, accountId } from './plugin-request-fixture.mjs';
import { installFakeCompanion } from '../../gemini-web-plugin/web/tests/fake-companion.ts';
import { runSecurityCases } from './plugin-request-security.mjs';

const [vendor, evidence, playwrightModule, filter] = process.argv.slice(2);
assert(vendor && evidence && playwrightModule, 'Usage: node plugin-request-browser.mjs VENDOR EVIDENCE PLAYWRIGHT_MODULE');
const { chromium, expect } = await import(pathToFileURL(resolve(playwrightModule)).href);
assert(expect, 'Use @playwright/test/index.mjs for assertion support');
await mkdir(evidence, { recursive: true });
const browser = await chromium.launch({ channel: 'chrome' });
const results = [];
const captures = [];

async function scenario(name, verify, options = {}) {
  if (filter && !name.includes(filter)) return;
  const host = await fixture(vendor);
  const context = await browser.newContext({ viewport: { width: options.width ?? 1280, height: 900 }, colorScheme: options.theme ?? 'light', locale: 'en-US', serviceWorkers: 'block' });
  const errors = [];
  const leaks = [];
  await context.route('**/*', (route) => new URL(route.request().url()).origin === host.base ? route.continue() : route.abort());
  await context.addInitScript(() => { if (window === window.top) localStorage.setItem('cli-proxy-language', 'en'); });
  const page = await context.newPage();
  page.setDefaultTimeout(8000);
  page.on('pageerror', (error) => errors.push(error.message));
  page.on('console', (message) => { if (/SYNTHETIC_MANAGER_|MOCK_LOGIN_NONCE/.test(message.text())) leaks.push(true); });
  const frame = page.frameLocator('iframe');
  const settle = () => page.evaluate(async () => { await Promise.all(document.getAnimations().filter((animation) => animation.effect?.getTiming().iterations !== Infinity).map((animation) => animation.finished.catch(() => undefined))); });
  const login = async (key = managerKey, remember = false) => {
    await page.locator('input[type="password"]').fill(key);
    const checkbox = page.locator('input[type="checkbox"]');
    if (await checkbox.isChecked() !== remember) { await checkbox.focus(); await page.keyboard.press('Space'); }
    await page.getByRole('button', { name: 'Login', exact: true }).click();
    await page.locator('.notification-container .close-btn').click();
    await expect(page.locator('.notification')).toHaveCount(0);
  };
  const open = async () => {
    await page.goto(`${host.base}/management.html#/oauth`);
    assert.equal(await page.locator('input[type="checkbox"]').isChecked(), false);
    await login(managerKey, options.remember ?? false);
    await page.locator('#oauth-provider-gemini-web').getByRole('button').click();
    await page.waitForURL('**#/plugin-pages/gemini-web/0');
    await page.locator('iframe').waitFor();
    await settle();
  };
  const capture = async (suffix) => {
    await settle();
    const path = resolve(evidence, `${name}-${suffix}.png`);
    await page.screenshot({ path });
    captures.push({ name: `${name}-${suffix}`, sha256: createHash('sha256').update(await readFile(path)).digest('hex') });
  };
  try {
    await verify({ host, page, frame, open, login, capture, settle, expect });
    assert.deepEqual(errors, []);
    assert.deepEqual(leaks, []);
    results.push({ name, passed: true, calls: host.calls });
    console.log(`PASS ${name}`);
  } catch (error) {
    await capture('failure');
    await writeFile(resolve(evidence, `${name}-failure.txt`), (await Promise.all(page.frames().map((entry) => entry.locator('body').innerText().catch(() => 'Unavailable frame')))).join('\n\n'));
    results.push({ name, passed: false, calls: host.calls, errors });
    throw error;
  } finally {
    host.release();
    await context.close();
    await host.close();
  }
}

try {
  for (const width of [1280, 768, 375]) for (const theme of ['light', 'dark']) {
    await scenario(`remember-off-${theme}-${width}`, async ({ host, page, frame, open, capture, settle, expect }) => {
      await open();
      await expect(frame.locator('.account-card[data-account-id]')).toHaveCount(1);
      await expect(frame.locator('html')).toHaveAttribute('data-theme', theme === 'dark' ? 'dark' : 'white');
      assert(host.calls.some((call) => call.path === `${geminiPath}accounts` && call.authorized));
      assert.equal(await frame.locator('.metric').count(), 2);
      await expect(frame.getByRole('meter')).toHaveAttribute('aria-valuenow', '25');
      assert.equal(await frame.locator('.badge-model').count(), 1);
      await capture('gemini');
      const privacy = await frame.locator('html').evaluate((root) => {
        const capability = window.__CPAMP_PLUGIN_HOST__;
        const descriptor = Object.getOwnPropertyDescriptor(window, '__CPAMP_PLUGIN_HOST__');
        const values = Object.values(Object.getOwnPropertyDescriptors(window)).filter((entry) => typeof entry.value === 'string').map((entry) => entry.value);
        const stored = localStorage.getItem('cli-proxy-auth');
        let plain = stored ?? '';
        if (plain.startsWith('enc::v1::')) {
          const salt = new TextEncoder().encode(`cli-proxy-api-webui::secure-storage|${location.host}|${navigator.userAgent}`);
          plain = new TextDecoder().decode(Uint8Array.from(atob(plain.slice(9)), (character, index) => character.charCodeAt(0) ^ salt[index % salt.length]));
        }
        return { fields: Object.keys(capability), enumerable: descriptor.enumerable, frozen: Object.isFrozen(capability),
          leaked: [...values, plain, root.outerHTML, JSON.stringify(capability), String(capability.request)].some((value) => /SYNTHETIC_MANAGER_/.test(value)),
          persistedKey: JSON.parse(plain).state.managementKey, sessionKeys: Object.keys(sessionStorage) };
      });
      assert.deepEqual(privacy.fields.sort(), ['pluginID', 'request', 'resourceURL', 'version']);
      assert.equal(privacy.enumerable, false); assert.equal(privacy.frozen, true); assert.equal(privacy.leaked, false);
      assert(!privacy.persistedKey); assert.deepEqual(privacy.sessionKeys, []);
      await page.evaluate(() => { location.hash = '#/plugin-pages/chatgpt2api/0'; });
      await expect(frame.locator('main')).toHaveAttribute('data-state', 'healthy');
      await settle();
      assert.equal(await frame.locator('#route-count').innerText(), '17');
      assert(host.calls.some((call) => call.path === '/v0/management/plugins/chatgpt2api/status' && call.authorized));
      await frame.locator('#refresh').click();
      await expect(frame.locator('main')).toHaveAttribute('data-state', 'healthy');
      await capture('chatgpt');
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
      assert.equal(await frame.locator('html').evaluate((root) => root.scrollWidth > innerWidth), false);
    }, { width, theme });
  }

  for (const unavailable of ['empty', 'failed']) {
    await scenario(`registry-${unavailable}-explicit-recheck`, async ({ host, page, frame, open, capture, expect }) => {
      await open();
      await expect(frame.locator('.badge-model')).toHaveCount(1);
      const resource = page.frames().find((entry) => entry.url().endsWith('/v0/resource/plugins/gemini-web/index'));
      assert(resource);
      await resource.evaluate(installFakeCompanion, 'normal');
      if (unavailable === 'empty') host.state.models = []; else host.state.modelStatus = 503;
      await frame.locator('#google-login').click();
      await frame.locator('#login-label').fill('MOCK registry gate');
      await frame.locator('#login-consent').check();
      await frame.locator('#login-start').click();
      await frame.locator('#login-connect').click();
      await expect(frame.locator('.login-panel')).toHaveAttribute('data-phase', 'approval');
      await resource.evaluate(() => window.dispatchEvent(new Event('MOCK_COMPANION_APPROVE')));
      await expect.poll(() => host.calls.filter((call) => call.path.startsWith('/v0/management/auth-files/models?')).length).toBe(2);
      await expect(frame.locator('.badge-model')).toHaveCount(0);
      assert.equal(await frame.locator('#login-status .badge-success').count(), 0);
      await capture('pending');
      host.state.models = measured.models; host.state.modelStatus = 200;
      await frame.locator('#login-check').click();
      await expect(frame.locator('#login-status .badge-success')).toHaveCount(1);
      assert.equal(host.state.loginCompletes, 1);
      assert.equal(host.calls.filter((call) => call.path.startsWith('/v0/management/auth-files/models?')).length, 3);
      assert(host.calls.filter((call) => call.method === 'POST').every((call) => call.origin === host.base));
      const port = await resource.evaluate(() => window.mockCompanionEvidence());
      assert.deepEqual(port.messages, ['begin', 'ack']); assert.deepEqual(port.fields, [['type', 'state'], ['type', 'state']]);
      assert.equal(port.writes, 0);
      await capture('ready');
    });
  }

  await scenario('quota-http-200-error-is-stale', async ({ host, frame, open, capture, expect }) => {
    await open();
    await expect(frame.locator('.badge-model')).toHaveCount(1);
    host.state.account = { ...measured, status: 'error', models: [], usage: null, error: 'secret_store_unavailable' };
    await frame.locator(`#refresh-all`).click();
    await expect(frame.locator('#refresh-all')).toBeEnabled();
    assert.equal(await frame.locator('.badge-model').count(), 0);
    await expect(frame.getByRole('meter')).toHaveAttribute('aria-valuenow', '25');
    await expect(frame.locator('.feedback')).toContainText('0개 사용 가능 확인');
    assert.equal(host.calls.filter((call) => call.path === `${geminiPath}refresh` && call.body === JSON.stringify({ id: accountId })).length, 1);
    await capture('stale');
  });
  await runSecurityCases(scenario);
} finally {
  await browser.close();
  await writeFile(resolve(evidence, 'browser.json'), JSON.stringify({ results, captures,
    cleanup: { browserClosed: true, fixtureServersClosed: true, isolatedContextsClosed: true } }, null, 2));
}
console.log(`${results.length} real nested build scenarios passed`);
