import assert from 'node:assert/strict';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { fixture, managerKey } from '../../../cpa-manager-plus/tests/plugin-request-fixture.mjs';
import { chatgptPrefix } from './accounts-fixture.mjs';
import { sourceID, disabledSourceID, webID, secondWebID } from './account-fixtures.ts';

const [vendor, evidence, playwrightModule, filter] = process.argv.slice(2);
assert(vendor && evidence && playwrightModule, 'Usage: node accounts-browser.mjs VENDOR NEW_EVIDENCE PLAYWRIGHT_MODULE [FILTER]');
const { chromium, expect } = await import(pathToFileURL(resolve(playwrightModule)).href);
await mkdir(evidence, { recursive: false });
const browser = await chromium.launch({ channel: 'chrome' });
const results = [];
const captures = [];

async function scenario(name, verify, options = {}) {
  if (filter && !name.includes(filter)) return;
  const host = await fixture(vendor);
  const context = await browser.newContext({ viewport: { width: options.width ?? 1280, height: 1000 }, colorScheme: options.theme ?? 'light', locale: 'en-US', serviceWorkers: 'block' });
  const page = await context.newPage();
  const errors = [];
  const logs = [];
  const external = [];
  const childNetwork = [];
  page.setDefaultTimeout(6000);
  await context.route('**/*', (route) => {
    if (new URL(route.request().url()).origin === host.base) return route.continue();
    external.push(new URL(route.request().url()).origin); return route.abort();
  });
  await context.addInitScript(() => { if (window === window.top) localStorage.setItem('cli-proxy-language', 'en'); });
  page.on('pageerror', (error) => errors.push(error.message));
  page.on('console', (message) => logs.push(message.text()));
  page.on('request', (request) => {
    if (request.url().includes(chatgptPrefix) && request.frame().parentFrame()) childNetwork.push(request.url());
  });
  const frame = page.frameLocator('iframe');
  const capture = async (suffix) => {
    await page.evaluate(async () => { await Promise.all(document.getAnimations().filter((animation) => animation.effect?.getTiming().iterations !== Infinity).map((animation) => animation.finished.catch(() => undefined))); });
    const path = resolve(evidence, `${name}-${suffix}.png`);
    await page.screenshot({ path });
    captures.push({ name: `${name}-${suffix}`, sha256: createHash('sha256').update(await readFile(path)).digest('hex') });
  };
  const open = async () => {
    await page.goto(`${host.base}/management.html#/plugin-pages/chatgpt2api/0`);
    assert.equal(await page.locator('input[type="checkbox"]').isChecked(), false);
    await page.locator('input[type="password"]').fill(managerKey);
    await page.getByRole('button', { name: 'Login', exact: true }).click();
    await page.locator('.notification-container .close-btn').click();
    await expect(frame.locator('main')).toHaveAttribute('data-state', 'healthy');
    await expect(frame.locator('#account-area')).toHaveAttribute('aria-busy', 'false');
    await expect(page.locator('.notification')).toHaveCount(0);
    await page.evaluate(() => {
      const label = document.createElement('div'); label.textContent = 'MOCK ONLY - synthetic account data';
      label.style.cssText = 'position:fixed;bottom:0;right:0;padding:4px 12px;background:#14243b;color:white;z-index:99999;font:12px sans-serif;pointer-events:none'; document.body.append(label);
    });
  };
  try {
    await verify({ host, page, frame, open, capture });
    assert.deepEqual(errors, []); assert.deepEqual(external, []); assert.deepEqual(childNetwork, []);
    assert(!logs.some((line) => /SYNTHETIC_MANAGER_|SYNTHETIC_PRIVATE/.test(line)));
    const privacy = await frame.locator('html').evaluate((root) => {
      let stored = localStorage.getItem('cli-proxy-auth') ?? '{}';
      if (stored.startsWith('enc::v1::')) {
        const salt = new TextEncoder().encode(`cli-proxy-api-webui::secure-storage|${location.host}|${navigator.userAgent}`);
        stored = new TextDecoder().decode(Uint8Array.from(atob(stored.slice(9)), (character, index) => character.charCodeAt(0) ^ salt[index % salt.length]));
      }
      return { html: root.outerHTML, local: stored, session: JSON.stringify(sessionStorage),
        globals: Object.values(Object.getOwnPropertyDescriptors(window)).filter((entry) => typeof entry.value === 'string').map((entry) => entry.value).join(''),
        fields: Object.keys(window.__CPAMP_PLUGIN_HOST__), persisted: JSON.parse(stored).state?.managementKey };
    });
    assert(!/SYNTHETIC_MANAGER_|SYNTHETIC_PRIVATE/.test(JSON.stringify(privacy)));
    assert.deepEqual(privacy.fields.sort(), ['pluginID', 'request', 'resourceURL', 'version']);
    assert(!privacy.persisted);
    const posts = host.calls.filter((entry) => entry.path.startsWith(chatgptPrefix) && entry.method === 'POST');
    for (const post of posts) {
      assert(post.authorized); assert.equal(post.origin, host.base);
      assert(Object.keys(JSON.parse(post.body)).every((key) => ['id', 'consent', 'allow_disabled_source', 'enabled'].includes(key)));
    }
    results.push({ name, passed: true, calls: host.calls.filter((entry) => entry.path.startsWith(chatgptPrefix)), mutations: host.state.chatgpt.mutations });
    console.log(`PASS ${name}`);
  } catch (error) {
    await capture('failure');
    results.push({ name, passed: false });
    throw error;
  } finally { host.release(); await context.close(); await host.close(); }
}

try {
  for (const width of [375, 768, 1280]) for (const theme of ['light', 'dark']) {
    await scenario(`accounts-${theme}-${width}`, async ({ host, page, frame, open, capture }) => {
      await open();
      await expect(frame.locator('[data-source-id]')).toHaveCount(2);
      await expect(frame.locator('[data-web-id]')).toHaveCount(2);
      assert.deepEqual(host.state.chatgpt.mutations, []);
      await frame.locator('#account-area-title').evaluate((element) => element.scrollIntoView({ block: 'start' }));
      await capture('sources');
      const source = frame.locator(`[data-source-id="${sourceID}"]`);
      await source.getByRole('button').click();
      await expect(frame.locator('#account-consent')).not.toBeChecked();
      await expect(frame.locator('#confirm-account')).toBeDisabled();
      await capture('import-consent');
      await frame.locator('#account-consent').check();
      await frame.locator('#confirm-account').click();
      await expect(frame.locator('[data-web-id]')).toHaveCount(3);
      const imported = frame.locator(`[data-web-id="web_${'e'.repeat(64)}"]`);
      await expect(imported).toHaveAttribute('data-disabled', 'true');
      assert.deepEqual(host.state.chatgpt.mutations, [{ action: 'import-codex', id: sourceID, consent: true }]);
      await imported.getByRole('button', { name: /Web 활성화/ }).click();
      await expect(frame.locator('#confirm-account')).toBeDisabled();
      await capture('enable-consent');
      await frame.locator('#account-consent').check();
      await frame.locator('#confirm-account').click();
      await expect(imported).toHaveAttribute('data-disabled', 'false');
      await imported.getByRole('button', { name: /Web 사용량 갱신/ }).click();
      await expect(imported.locator('[data-metric="observed"]')).toContainText('11');
      await expect(imported.locator('[data-metric="tracked"]')).toContainText('5');
      await expect(frame.locator(`[data-web-id="${secondWebID}"] [data-metric="observed"]`)).toContainText('미확인');
      await imported.scrollIntoViewIfNeeded(); await capture('quota');
      await imported.getByRole('button', { name: /Web 비활성화/ }).click();
      await frame.locator('#account-consent').check(); await frame.locator('#confirm-account').click();
      await expect(imported).toHaveAttribute('data-disabled', 'true');
      assert.equal(host.state.chatgpt.sources[0].disabled, false); assert.equal(host.state.chatgpt.sources[1].disabled, true);
      assert.equal(host.state.chatgpt.mutations.length, 4);
      await page.clock.install(); await page.clock.fastForward(600_000);
      assert.equal(host.state.chatgpt.mutations.length, 4);
      assert.equal(await frame.locator('[role="meter"], [role="progressbar"], input:not([type="checkbox"]), textarea').count(), 0);
      assert.equal(await frame.locator('html').evaluate((root) => root.scrollWidth > innerWidth), false);
    }, { width, theme });
  }
  await scenario('disabled-source-separate-consent-and-keyboard', async ({ host, page, frame, open, capture }) => {
    await open();
    const trigger = frame.locator(`[data-source-id="${disabledSourceID}"] button`);
    await trigger.focus(); await page.keyboard.press('Enter');
    await expect(frame.locator('#cancel-account')).toBeFocused();
    await page.keyboard.press('Tab');
    await expect(frame.locator('#disabled-source-consent')).toBeFocused();
    await frame.locator('#account-consent').check();
    await expect(frame.locator('#confirm-account')).toBeDisabled();
    await capture('override-required');
    await page.keyboard.press('Escape');
    await expect(frame.locator('dialog')).toHaveCount(0); await expect(trigger).toBeFocused();
    assert.deepEqual(host.state.chatgpt.mutations, []);
    await trigger.click();
    await expect(frame.locator('#account-consent')).not.toBeChecked();
    await frame.locator('#disabled-source-consent').check();
    await expect(frame.locator('#confirm-account')).toBeDisabled();
    await frame.locator('#account-consent').check(); await frame.locator('#confirm-account').focus();
    await page.keyboard.press('Tab'); await expect(frame.locator('#disabled-source-consent')).toBeFocused();
    await frame.locator('#confirm-account').click();
    await expect(frame.locator('[data-web-id]')).toHaveCount(3);
    assert.deepEqual(host.state.chatgpt.mutations, [{ action: 'import-codex', id: disabledSourceID, consent: true, allow_disabled_source: true }]);
    assert.equal(host.state.chatgpt.sources[1].disabled, true);
  }, { width: 375 });
  await scenario('duplicate-target-does-not-change-enabled-state', async ({ host, frame, open, capture }) => {
    host.state.chatgpt.imports.set(sourceID, secondWebID);
    await open();
    await frame.locator(`[data-source-id="${sourceID}"] button`).click();
    await frame.locator('#account-consent').check(); await frame.locator('#confirm-account').click();
    await expect(frame.locator('#account-area')).toHaveAttribute('aria-busy', 'false');
    await expect(frame.locator('#account-feedback')).toContainText('이미 존재');
    await expect(frame.locator(`[data-web-id="${secondWebID}"]`)).toHaveAttribute('data-disabled', 'false');
    assert.deepEqual(host.state.chatgpt.mutations, []);
    await frame.locator('#account-feedback').scrollIntoViewIfNeeded(); await capture('unchanged');
  });
  for (const mode of ['unsupported', 'unknown', 'unconfigured', 'malformed', 'unknown-state']) {
    await scenario(`blocked-${mode}`, async ({ host, frame, open, capture }) => {
      if (mode === 'unsupported') host.state.chatgpt.capability = false;
      if (mode === 'unknown') host.state.chatgpt.omitCapability = true;
      if (mode === 'unconfigured') Object.assign(host.state.chatgpt, { errorPath: 'webaccounts', errorCode: 'web_api_unconfigured_set_CHATGPT2API_AUTH_KEY' });
      if (mode === 'malformed') host.state.chatgpt.malformed = true;
      if (mode === 'unknown-state') host.state.chatgpt.accounts = [{ ...host.state.chatgpt.accounts[1], status: 'unknown' }];
      await open();
      const actions = mode === 'unknown-state' ? frame.locator('[data-web-id] button') : frame.locator('[data-source-id] button, [data-web-id] button');
      for (const action of await actions.all()) await expect(action).toBeDisabled();
      if (mode === 'unconfigured') await expect(frame.locator('#account-feedback')).toContainText('CHATGPT2API_AUTH_KEY');
      assert.deepEqual(host.state.chatgpt.mutations, []);
      await frame.locator('#account-area-title').evaluate((element) => element.scrollIntoView({ block: 'start' })); await capture('blocked');
    });
  }
  for (const mode of ['body-error', 'transport', 'rotation', 'wrong-id']) {
    await scenario(`refresh-${mode}`, async ({ host, frame, open, capture }) => {
      await open();
      if (mode === 'transport') Object.assign(host.state.chatgpt, { errorPath: 'refresh-web', errorCode: 'web_api_unavailable' });
      else host.state.chatgpt.refreshMode = mode;
      const account = frame.locator(`[data-web-id="${webID}"]`);
      await account.getByRole('button', { name: /Web 사용량 갱신/ }).click();
      await expect(account).toHaveAttribute('data-stale', 'true');
      await expect(account.locator('[data-metric="observed"]')).toContainText('3');
      await expect(frame.locator(`[data-web-id="${secondWebID}"]`)).toHaveAttribute('data-stale', 'false');
      assert.equal(host.calls.filter((entry) => entry.path === `${chatgptPrefix}refresh-web`).length, 1);
      assert.equal(host.calls.filter((entry) => entry.path === `${chatgptPrefix}webaccounts`).length, 1);
      await account.scrollIntoViewIfNeeded(); await capture('stale');
      if (mode !== 'body-error') for (const control of await account.locator('button').all()) await expect(control).toBeDisabled();
      host.state.chatgpt.errorPath = ''; host.state.chatgpt.refreshMode = 'success';
      await frame.locator('#reload-accounts').click(); await expect(frame.locator('#account-area')).toHaveAttribute('aria-busy', 'false');
      if (mode === 'rotation') { await expect(account).toHaveCount(0); await expect(frame.locator('[data-web-id]')).toHaveCount(2); }
      else { await account.getByRole('button', { name: /Web 사용량 갱신/ }).click(); await expect(account).toHaveAttribute('data-stale', 'false'); }
    });
  }
  await scenario('pending-toggle-cancel-and-explicit-disable', async ({ host, page, frame, open, capture }) => {
    await open(); const account = frame.locator(`[data-web-id="${secondWebID}"]`);
    await account.getByRole('button', { name: /Web 비활성화/ }).click();
    await frame.locator('#cancel-account').click(); assert.deepEqual(host.state.chatgpt.mutations, []);
    await account.getByRole('button', { name: /Web 비활성화/ }).click();
    await frame.locator('#account-consent').check();
    host.state.hold = `${chatgptPrefix}set-web-enabled`; await frame.locator('#confirm-account').click();
    await expect(account).toHaveAttribute('aria-busy', 'true');
    await expect(account).toHaveAttribute('data-disabled', 'false');
    await expect(frame.locator('#reload-accounts')).toBeDisabled();
    await account.scrollIntoViewIfNeeded(); await capture('pending');
    host.release(); await expect(account).toHaveAttribute('data-disabled', 'true');
    assert.deepEqual(host.state.chatgpt.mutations, [{ action: 'set-web-enabled', id: secondWebID, enabled: false, consent: true }]);
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await expect(account.locator('button').first()).toHaveCSS('transition-duration', '0s');
  });
  await scenario('chatgpt-account-401-is-not-manager-logout', async ({ host, page, frame, open, capture }) => {
    await open();
    Object.assign(host.state.chatgpt, { errorPath: 'refresh-web', errorStatus: 401, errorCode: 'web_api_http_error' });
    await frame.locator(`[data-web-id="${webID}"] button`).first().click();
    await expect(frame.locator(`[data-web-id="${webID}"]`)).toHaveAttribute('data-stale', 'true');
    assert.equal(await page.locator('input[type="password"]').count(), 0);
    await frame.locator('#account-feedback').scrollIntoViewIfNeeded(); await capture('account-error');
  });
  await scenario('chatgpt-exact-post-denials-and-body-errors', async ({ host, frame, open }) => {
    await open();
    const before = host.calls.length;
    const rejected = [
      { path: `${chatgptPrefix}arbitrary`, body: '{}' },
      { path: '/v0/management/config', body: '{}' },
      { path: '/v0/management/plugins/gemini-web/refresh', body: '{}' },
      { path: `${chatgptPrefix}refresh-web`, body: JSON.stringify({ id: webID, access_token: 'SYNTHETIC_NOT_SENT' }) },
      { path: `${chatgptPrefix}refresh-web`, body: ' '.repeat(4096) + JSON.stringify({ id: webID }) },
      { path: `${chatgptPrefix}set-web-enabled`, body: JSON.stringify({ id: webID, enabled: true }) },
      { path: `${chatgptPrefix}import-codex`, body: JSON.stringify({ id: sourceID, consent: false }) },
    ];
    for (const input of rejected) {
      const result = await frame.locator('html').evaluate((_root, input) => window.__CPAMP_PLUGIN_HOST__.request({ ...input, method: 'POST' }), input);
      assert.deepEqual(result, { status: 403, body: '{"error":"host_request_failed"}' });
    }
    assert.equal(host.calls.length, before);
    Object.assign(host.state.chatgpt, { errorPath: 'webaccounts', errorStatus: 200, errorCode: 'web_api_unconfigured_set_CHATGPT2API_AUTH_KEY' });
    const result = await frame.locator('html').evaluate((_root, path) => window.__CPAMP_PLUGIN_HOST__.request({ path, method: 'GET' }), `${chatgptPrefix}webaccounts`);
    assert.deepEqual(result, { status: 200, body: '{"error":"web_api_unconfigured_set_CHATGPT2API_AUTH_KEY"}' });
  });
} finally {
  await browser.close();
  await writeFile(resolve(evidence, 'browser.json'), JSON.stringify({ mockOnly: true, results, captures,
    cleanup: { browserClosed: true, contextsClosed: true, serversClosed: true } }, null, 2));
}
console.log(`${results.length} ChatGPT nested scenarios passed`);
