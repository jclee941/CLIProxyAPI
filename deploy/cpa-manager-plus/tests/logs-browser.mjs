import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFile, mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const [vendor, evidence, playwrightModule] = process.argv.slice(2);
assert(vendor && evidence && playwrightModule);
const { chromium } = await import(pathToFileURL(resolve(playwrightModule)).href);
const artifact = await readFile(resolve(vendor, 'apps/web/dist/index.html'));
await mkdir(evidence, { recursive: true });
const fullLog = `{"integer":9007199254740993,"payload":"${'a'.repeat(150000)}","tail":"END_OF_FULL_LOG"}`;
const errorRequest = fullLog + '\n\n\n';
const errorResponse = '{"error":{"code":"no_video_generated","message":"fixture failure"}}\n';
const errorLog = '=== REQUEST INFO ===\nMethod: POST\n\n=== REQUEST BODY ===\n' +
  errorRequest + '=== RESPONSE ===\nStatus: 422\nContent-Type: application/json\n\n' + errorResponse;
const firstLine = '[2026-09-22 15:00:00] [abcdef12] [info] [gin_logger.go:97] 200 | 1s | 127.0.0.1 | POST "/v1beta/interactions"';
const nextLine = '[2026-09-22 15:00:01] [abcdef13] [info] [gin_logger.go:97] 200 | 2s | 127.0.0.1 | POST "/v1/chat/completions"';
const managementTail = Array.from({ length: 150 }, (_, index) =>
  `[2026-09-22 15:00:00] [00000000] [info] [gin_logger.go:97] 200 | 1ms | 127.0.0.1 | GET "/v0/management/config?fixture=${index}"`);
const calls = [];
const server = createServer((request, response) => {
  const url = new URL(request.url, 'http://localhost');
  calls.push(url);
  if (url.pathname === '/' || url.pathname === '/management.html') {
    response.writeHead(200, { 'content-type': 'text/html' });
    response.end(artifact);
    return;
  }
  if (url.pathname === '/v0/management/request-log-by-id/abcdef12' ||
      url.pathname === '/v0/management/request-error-logs/error-fixture.log') {
    response.writeHead(200, { 'content-type': 'text/plain' });
    response.end(url.pathname.includes('/request-error-logs/') ? errorLog : fullLog);
    return;
  }
  let body = {};
  switch (url.pathname) {
    case '/usage-service/info':
      body = { service: 'cpa-manager-plus', configured: true, projectInitialized: true, setupRequired: false };
      break;
    case '/v0/management/config':
      body = { 'logging-to-file': true, 'request-log': true };
      break;
    case '/v0/management/request-error-logs':
      body = { files: [{ name: 'error-fixture.log', size: errorLog.length, modified: 1790056801 }] };
      break;
    case '/v0/management/logs':
      body = { lines: url.searchParams.has('cursor') ? [nextLine] : [firstLine, ...managementTail], 'line-count': 151, 'latest-timestamp': 1790056801, 'next-cursor': 'fixture-cursor' };
      break;
  }
  response.writeHead(200, { 'content-type': 'application/json' });
  response.end(JSON.stringify(body));
});
await new Promise(resolveListen => server.listen(0, '127.0.0.1', resolveListen));
const address = server.address();
assert(address && typeof address === 'object');
const base = `http://127.0.0.1:${address.port}`;
const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || '/usr/bin/google-chrome' });
async function dismissNotices(page) {
  const dismissed = page.locator('.notification-container').waitFor({ state: 'detached' });
  await page.locator('.notification-container .close-btn').evaluateAll(buttons =>
    buttons.forEach(button => button.click()));
  await dismissed;
}
try {
  for (const width of [1280, 375]) {
    const context = await browser.newContext({ viewport: { width, height: 900 }, locale: 'en-US' });
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    await context.addInitScript(() => localStorage.setItem('cli-proxy-language', 'en'));
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    await page.clock.install();
    await page.clock.pauseAt(new Date(Date.now() + 1000));
    await page.goto(`${base}/management.html#/logs`);
    await page.locator('input[type=password]').fill('synthetic-manager-fixture');
    const initialLogRead = page.waitForResponse(response =>
      response.url().includes('/logs?') && !response.url().includes('cursor='));
    await page.getByRole('button', { name: 'Login', exact: true }).click();
    await (await initialLogRead).finished();
    await page.locator('button:not([disabled])').filter({ hasText: 'Refresh Logs' }).waitFor();
    const first = page.getByText('abcdef12', { exact: true });
    assert.equal(await first.count(), 1, 'Hidden management rows made an existing API log unreachable');
    assert(await page.getByRole('checkbox', { name: 'Auto Refresh' }).isChecked(), 'New logs do not arrive: automatic refresh defaults off');
    const incremental = page.waitForResponse(response => response.url().includes('/logs?') && response.url().includes('cursor='));
    await page.clock.runFor(8000);
    await incremental;
    await page.getByText('abcdef13', { exact: true }).waitFor();
    assert(calls.some(url => url.searchParams.get('cursor') === 'fixture-cursor'));
    await page.getByRole('checkbox', { name: 'Auto Refresh' }).locator('..').click();
    assert.equal(await page.getByRole('checkbox', { name: 'Auto Refresh' }).isChecked(), false);
    await first.scrollIntoViewIfNeeded();
    await first.dispatchEvent('pointerdown', { button: 0, clientX: 1, clientY: 1 });
    await page.clock.runFor(650);
    await first.dispatchEvent('pointerup', { button: 0 });
    const full = page.locator('[role=dialog] pre');
    await full.waitFor();
    await page.clock.resume();
    assert.equal(await full.textContent(), fullLog, 'Full log text was truncated or changed');
    await page.getByRole('button', { name: 'Copy', exact: true }).click();
    assert.equal(await page.evaluate(() => navigator.clipboard.readText()), fullLog);
    await full.evaluate(node => { node.scrollTop = node.scrollHeight; });
    await page.evaluate(() => {
      for (const animation of document.getAnimations()) {
        if (animation.effect?.getTiming().iterations !== Infinity) animation.finish();
      }
    });
    await page.screenshot({ path: resolve(evidence, `logs-full-${width}.png`), fullPage: true });
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'Page overflows horizontally');
    await dismissNotices(page);
    const dialogClosed = page.getByRole('dialog').waitFor({ state: 'detached' });
    await page.getByRole('button', { name: 'Cancel', exact: true }).click();
    await dialogClosed;
    await page.locator('.content').evaluate(node => { node.scrollTop = 0; });
    await page.getByRole('button', { name: 'Error Request Logs', exact: true }).click();
    await page.getByText('error-fixture.log', { exact: true }).waitFor();
    const viewError = page.getByRole('button', { name: 'View', exact: true });
    assert.equal(await viewError.count(), 1, 'Error tab has no full-text viewer');
    await viewError.click();
    await full.waitFor();
    const requestTab = page.getByRole('tab', { name: 'Request payload', exact: true });
    assert.equal(await requestTab.count(), 1, 'Request payload is not separately visible');
    await requestTab.click();
    assert.equal(await full.textContent(), errorRequest);
    await page.evaluate(() => {
      for (const animation of document.getAnimations()) {
        if (animation.effect?.getTiming().iterations !== Infinity) animation.finish();
      }
    });
    await page.screenshot({ path: resolve(evidence, `logs-error-request-${width}.png`), fullPage: true });
    await page.getByRole('button', { name: 'Copy', exact: true }).click();
    assert.equal(await page.evaluate(() => navigator.clipboard.readText()), errorRequest);
    await dismissNotices(page);
    await page.getByRole('tab', { name: 'Response payload', exact: true }).click();
    assert.equal(await full.textContent(), errorResponse);
    await page.evaluate(() => {
      for (const animation of document.getAnimations()) {
        if (animation.effect?.getTiming().iterations !== Infinity) animation.finish();
      }
    });
    await page.screenshot({ path: resolve(evidence, `logs-error-response-${width}.png`), fullPage: true });
    await page.getByRole('tab', { name: 'Original log', exact: true }).click();
    assert.equal(await full.textContent(), errorLog, 'Original error log was truncated or changed');
    await full.evaluate(node => { node.scrollTop = node.scrollHeight; });
    await page.evaluate(() => {
      for (const animation of document.getAnimations()) {
        if (animation.effect?.getTiming().iterations !== Infinity) animation.finish();
      }
    });
    await page.screenshot({ path: resolve(evidence, `logs-error-full-${width}.png`), fullPage: true });
    assert.deepEqual(errors, []);
    await context.close();
    console.log(`LOGS_BROWSER_PASS width=${width} bytes=${fullLog.length} cursor=true clipboard=true`);
  }
} finally {
  await browser.close();
  await new Promise((resolveClose, reject) => server.close(error => error ? reject(error) : resolveClose()));
}
