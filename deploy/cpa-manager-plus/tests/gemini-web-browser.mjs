import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFile, mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { createHash } from 'node:crypto';

const [vendor, evidence, playwrightModule] = process.argv.slice(2);
assert(vendor && evidence && playwrightModule, 'Usage: node gemini-web-browser.mjs VENDOR EVIDENCE PLAYWRIGHT_MODULE');
const { chromium } = await import(pathToFileURL(resolve(playwrightModule)).href);
const artifact = await readFile(resolve(vendor, 'apps/web/dist/index.html'));
await mkdir(evidence, { recursive: true });
const calls = [];
const plugin = {
  id: 'gemini-web', oauth_provider: 'gemini-web', configured: true, registered: true,
  enabled: true, effective_enabled: true, supports_oauth: true,
  metadata: { name: 'Gemini Web' },
  menus: [{ menu: 'Gemini Web', description: 'Fixture portal', path: '/v0/resource/plugins/gemini-web/index' }],
};
let portalAvailable = true;
const accountId = 'gemini-web-fixture-1.json';
const nativeFiles = [
  { id: accountId, name: accountId, provider: 'gemini-web', type: 'gemini-web', label: 'GeminiWeb fixture', auth_index: 'fixture-1', disabled: true, status: 'ready' },
  { id: 'codex-fixture.json', name: 'codex-fixture.json', provider: 'codex', type: 'codex', label: 'Codex fixture', auth_index: 'fixture-2', status: 'ready' },
];
const measured = {
  id: accountId, label: 'GeminiWeb fixture', enabled: false, status: 'ready', observed_at: 1800000001,
  models: [{ id: 'fixture-model', name: 'Provider supplied model' }],
  usage: { tier: null, tier_code: 'opaque-42', source: 'GoogleWeb', estimated: false, observed_at: 1800000000,
    metrics: [
      { remaining_units: 71.25, usage_fraction: null, reset_unix_seconds: null, window_kind: 'ai_credit', unit: 'provider_compute_unit' },
      { remaining_units: null, usage_fraction: 0.125, reset_unix_seconds: 1800000500, window_kind: 'unknown', unit: 'provider_compute_unit', metric_type: 9 },
    ],
  },
};
let quotaResponse = measured;
let quotaStatus = 200;
const server = createServer(async (request, response) => {
  const path = new URL(request.url, 'http://localhost').pathname;
  let body = '';
  for await (const chunk of request) body += String(chunk);
  calls.push({ method: request.method, path, body });
  const headers = { 'content-type': 'application/json', 'x-cpa-support-plugin': 'true', 'x-cpa-version': 'v7.2.157' };
  if (path.startsWith('/v0/management/') && request.headers.authorization !== 'Bearer synthetic-manager-fixture') {
    response.writeHead(401, headers);
    response.end(JSON.stringify({ error: 'Fixture Manager authorization required' }));
    return;
  }
  if (path === '/' || path === '/management.html') {
    response.writeHead(200, { 'content-type': 'text/html' });
    response.end(artifact);
    return;
  }
  if (path === '/v0/resource/plugins/gemini-web/index') {
    response.writeHead(portalAvailable ? 200 : 503, { 'content-type': 'text/html' });
    response.end('<!doctype html><title>Local fixture portal</title><h1>GeminiWeb plugin portal fixture</h1>');
    return;
  }
  let payload = {};
  switch (path) {
    case '/usage-service/info': payload = { service: 'cpa-manager-plus', configured: true, projectInitialized: true, setupRequired: false }; break;
    case '/v0/management/plugins': payload = { plugins_enabled: true, plugins: [plugin] }; break;
    case '/v0/management/config': payload = { 'api-keys': [], 'plugins': { enabled: true } }; break;
    case '/v0/management/auth-files': payload = { files: nativeFiles }; break;
    case '/v0/management/plugins/gemini-web/accounts': payload = { provider: 'gemini-web', accounts: [quotaResponse] }; break;
    case '/v0/management/plugins/gemini-web/refresh': payload = quotaResponse; break;
    case '/v0/management/codex-auth-url': payload = { url: 'https://example.invalid/authorize', state: 'synthetic-state' }; break;
    case '/v0/management/get-auth-status': payload = { status: 'wait' }; break;
  }
  response.writeHead(path.endsWith('/gemini-web/refresh') ? quotaStatus : 200, headers);
  response.end(JSON.stringify(payload));
});
await new Promise((resolveListen) => server.listen(0, '127.0.0.1', resolveListen));
const address = server.address();
assert(address && typeof address === 'object');
const base = `http://127.0.0.1:${address.port}`;
const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || undefined });
const results = [];
const captures = [];
let activePage;
async function capture(page, name) {
  await page.evaluate(async () => {
    await Promise.all(document.getAnimations().filter((animation) => animation.effect?.getTiming().iterations !== Infinity).map((animation) => animation.finished.catch(() => {})));
  });
  await page.screenshot({ path: resolve(evidence, `${name}.png`) });
  const image = await readFile(resolve(evidence, `${name}.png`));
  assert.equal(image.subarray(0, 8).toString('hex'), '89504e470d0a1a0a');
  assert.equal(image.readUInt32BE(16), page.viewportSize().width);
  assert.equal(image.readUInt32BE(20), page.viewportSize().height);
  captures.push({ name, ...page.viewportSize(), sha256: createHash('sha256').update(image).digest('hex') });
}
try {
  for (const width of [1280, 375, 768]) {
    const context = await browser.newContext({ viewport: { width, height: 900 }, locale: 'en-US' });
    await context.route('**/*', (route) => new URL(route.request().url()).origin === base ? route.continue() : route.abort());
    await context.addInitScript(() => localStorage.setItem('cli-proxy-language', 'en'));
    const page = await context.newPage();
    activePage = page;
    const errors = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.goto(`${base}/management.html#/oauth`);
    await page.locator('input[type="password"]').fill('synthetic-manager-fixture');
    await page.getByRole('button', { name: 'Login', exact: true }).click();
    const card = page.locator('#oauth-provider-gemini-web');
    await card.scrollIntoViewIfNeeded();
    await card.getByRole('button').focus();
    await capture(page, `oauth-${width}`);
    const start = calls.length;
    await card.getByRole('button').click();
    await page.waitForURL('**#/plugin-pages/gemini-web/0');
    await page.frameLocator('iframe').getByRole('heading').waitFor();
    assert(!calls.slice(start).some(({ path }) => /auth-url|get-auth-status/.test(path)));
    await capture(page, `portal-${width}`);
    await page.goto(`${base}/management.html#/oauth`);
    portalAvailable = false;
    await page.locator('#oauth-provider-gemini-web').getByRole('button').click();
    await page.locator('#oauth-provider-gemini-web [role="alert"]').waitFor();
    await capture(page, `portal-error-${width}`);
    assert(page.url().endsWith('#/oauth'));
    portalAvailable = true;
    await page.locator('#oauth-provider-codex').getByRole('button').first().click();
    await page.waitForRequest('**/get-auth-status?**');
    const quotaStart = calls.length;
    const accountsLink = page.locator('a[href="#/accounts"]');
    const sidebarToggle = page.getByRole('button', { name: 'Expand sidebar', exact: true });
    if (await sidebarToggle.isVisible()) await sidebarToggle.click();
    await accountsLink.click();
    const accountCard = page.locator('[data-account-card]').filter({ hasText: accountId });
    await accountCard.getByRole('button', { name: /^(Refresh quota|GeminiWeb quota details)$/ }).click();
    const panel = page.locator('[data-gemini-web-quota]');
    await panel.waitFor();
    await panel.scrollIntoViewIfNeeded();
    assert.equal(calls.slice(quotaStart).filter(({ path }) => /plugins\/gemini-web\/(accounts|refresh)/.test(path)).length, 0);
    await capture(page, `quota-idle-${width}`);
    quotaResponse = measured;
    quotaStatus = 200;
    await panel.getByRole('button', { name: 'Load GeminiWeb quota', exact: true }).click();
    await page.locator('[data-gemini-web-quota="success"]').waitFor();
    assert.equal(await panel.locator('[data-gemini-web-field="tier"] dd').textContent(), 'Unknown');
    assert.equal(await panel.locator('[data-gemini-web-field="tier-code"] dd').textContent(), 'opaque-42');
    assert.equal(await panel.locator('[data-gemini-web-field="enabled"] dd').textContent(), 'false');
    assert.equal(await panel.locator('[data-gemini-web-metric]').count(), 2);
    assert.equal(await panel.getByText('71.25', { exact: true }).count(), 1);
    assert.equal(await panel.getByText('0.125', { exact: true }).count(), 1);
    assert.equal(await panel.getByText('12.5%', { exact: true }).count(), 0);
    assert.equal(await panel.getByRole('progressbar').count(), 0);
    await capture(page, `quota-measured-${width}`);
    await panel.locator('[data-gemini-web-metric]').last().scrollIntoViewIfNeeded();
    await capture(page, `quota-metrics-${width}`);
    quotaResponse = { ...measured, status: 'expired', usage: null, error: 'session_expired' };
    await panel.getByRole('button', { name: 'Refresh GeminiWeb quota', exact: true }).click();
    await panel.locator('[data-gemini-web-stale]').waitFor();
    assert.equal(await panel.getByText('71.25', { exact: true }).count(), 1);
    assert.match(await panel.getByRole('alert').textContent(), /expired/);
    await panel.getByRole('alert').scrollIntoViewIfNeeded();
    await capture(page, `quota-body-error-${width}`);
    quotaStatus = 503;
    await panel.getByRole('button', { name: 'Refresh GeminiWeb quota', exact: true }).click();
    await panel.getByText('GeminiWeb request failed.', { exact: false }).waitFor();
    await panel.getByRole('alert').scrollIntoViewIfNeeded();
    await capture(page, `quota-http-error-${width}`);
    quotaStatus = 200;
    quotaResponse = { ...measured, usage: null };
    await panel.getByRole('button', { name: 'Refresh GeminiWeb quota', exact: true }).click();
    await page.locator('[data-gemini-web-quota="unknown"]').waitFor();
    assert.equal(await panel.locator('[data-gemini-web-metric]').count(), 0);
    assert.equal(await panel.locator('[data-gemini-web-stale]').count(), 0);
    await panel.getByText('No provider quota metrics observed.', { exact: false }).scrollIntoViewIfNeeded();
    await capture(page, `quota-null-${width}`);
    const quotaCalls = calls.slice(quotaStart).filter(({ path }) => /plugins\/gemini-web\/(accounts|refresh)/.test(path));
    assert.equal(quotaCalls.length, 4);
    assert(quotaCalls.filter(({ path }) => path.endsWith('/refresh')).every(({ body }) => body === JSON.stringify({ id: accountId })));
    await panel.getByRole('button', { name: 'Open GeminiWeb plugin panel', exact: true }).click();
    await page.waitForURL('**#/plugin-pages/gemini-web/0');
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth);
    assert(!overflow, `Document overflow at ${width}`);
    results.push({ width, oauthPortal: true, nativeGeminiRequests: 0, codexStartAndPoll: true, missingResourceError: true, quotaManualOnly: true, nullableMetrics: true, staleOnFailure: true, quotaCalls: quotaCalls.length, overflow, errors });
    assert.deepEqual(errors, []);
    await context.close();
  }
} catch (error) {
  if (activePage && !activePage.isClosed()) {
    await activePage.screenshot({ path: resolve(evidence, 'failure.png') });
    await writeFile(resolve(evidence, 'failure.txt'), await activePage.locator('body').innerText());
  }
  throw error;
} finally {
  await browser.close();
  await new Promise((resolveClose) => server.close(resolveClose));
  await writeFile(resolve(evidence, 'browser.json'), JSON.stringify({ results, captures, calls, cleanup: { browserClosed: true, fixtureServerClosed: true } }, null, 2));
}
console.log(JSON.stringify(results, null, 2));
