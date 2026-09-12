import { chromium, expect, type FrameLocator, type Page } from '@playwright/test';
import { mkdir } from 'node:fs/promises';
import { MOCK_KEY, mockStatus, PRIVATE_SENTINEL } from './fixtures.ts';
import { startMockServer } from './mock-server.ts';

const evidence = `${import.meta.dir}/../evidence`;
await mkdir(evidence, { recursive: true });
const browser = await chromium.launch({ channel: 'chrome', headless: true });
const screenshots: string[] = [];
const results: { readonly name: string; readonly passed: boolean; readonly error?: string }[] = [];
type Surface = { readonly page: Page; readonly frame: FrameLocator; readonly mock: ReturnType<typeof startMockServer> };

async function capture(page: Page, name: string): Promise<void> {
  await page.evaluate(() => {
    const frame = document.querySelector('iframe');
    if (frame?.contentDocument) {
      const height = Math.max(frame.clientHeight, frame.contentDocument.documentElement.scrollHeight);
      document.body.style.height = 'auto';
      frame.style.height = `${height}px`;
      frame.contentWindow?.scrollTo(0, 0);
      window.scrollTo(0, 0);
    }
  });
  const image = await page.screenshot({ path: `${evidence}/mock-${name}.png`, fullPage: true });
  expect(image.subarray(0, 8).toString('hex')).toBe('89504e470d0a1a0a');
  expect(image.readUInt32BE(16)).toBe(page.viewportSize()?.width);
  screenshots.push(`mock-${name}.png`);
  await page.evaluate(() => {
    document.body.style.removeProperty('height');
    document.querySelector('iframe')?.style.removeProperty('height');
  });
}

async function scenario(name: string, verify: (surface: Surface) => Promise<void>): Promise<void> {
  const mock = startMockServer();
  const context = await browser.newContext({ viewport: { width: 1280, height: 1000 } });
  const page = await context.newPage();
  const errors: string[] = [];
  const external: string[] = [];
  await page.route('**/*', (route) => {
    if (new URL(route.request().url()).origin === mock.server.url.origin) return route.continue();
    external.push(new URL(route.request().url()).origin);
    return route.abort();
  });
  page.on('pageerror', (error) => errors.push(error.message));
  try {
    await verify({ page, frame: page.frameLocator('iframe'), mock });
    expect(errors).toEqual([]);
    expect(external).toEqual([]);
    expect(mock.state.mutations).toBe(0);
    results.push({ name, passed: true });
    console.log(`PASS ${name}`);
  } catch (error) {
    if (!(error instanceof Error)) throw error;
    results.push({ name, passed: false, error: error.message });
    console.error(`FAIL ${name}: ${error.message}`);
  } finally {
    mock.release();
    await context.close();
    await mock.server.stop(true);
  }
}

try {
  for (const theme of ['white', 'dark']) {
    for (const width of [375, 1280]) {
      for (const state of ['healthy', 'auth-error', 'upstream-down']) {
        await scenario(`${state}-${theme}-${width}`, async ({ page, frame, mock }) => {
          if (state === 'auth-error') mock.state.status = 401;
          if (state === 'upstream-down') mock.state.payload = { ...mockStatus,
            upstream: { healthy: false, error: PRIVATE_SENTINEL } };
          await page.setViewportSize({ width, height: 1100 });
          await page.goto(`${mock.url}?theme=${theme}`);
          await expect(frame.locator('main')).toHaveAttribute('data-state', state);
          await expect(frame.locator('html')).toHaveAttribute('data-cpamp-plugin-host', 'true');
          await expect(frame.locator('body')).not.toContainText(MOCK_KEY);
          await expect(frame.locator('body')).not.toContainText(PRIVATE_SENTINEL);
          await expect(frame.locator('input, textarea')).toHaveCount(0);
          const geometry = await frame.locator('html').evaluate((root) => ({ width: root.clientWidth, content: root.scrollWidth }));
          expect(geometry.content).toBeLessThanOrEqual(geometry.width);
          const link = frame.getByRole('link', { name: '기존 공급자 설정 열기' });
          await expect(link).toHaveAttribute('href', '/management.html#/ai-providers');
          await expect(link).toHaveAttribute('target', '_top');
          await capture(page, `${state}-${theme}-${width}`);
          if (state === 'healthy') {
            await expect(frame.locator('#route-count')).toHaveText('12');
            await expect(frame.locator('#model-list li')).toHaveCount(2);
            await expect(frame.locator('#active-accounts')).toHaveText('3');
            await frame.locator('#refresh').focus();
            await page.keyboard.press('Tab');
            await expect(link).toBeFocused();
            await expect(link).toHaveCSS('outline-style', 'solid');
            await capture(page, `focus-${theme}-${width}`);
          } else if (state === 'auth-error') {
            await expect(frame.getByRole('status')).toContainText('Manager');
            await expect(frame.getByRole('status')).toContainText('다시 로그인');
            await expect(frame.locator('#route-count')).toHaveCount(0);
          } else {
            await expect(frame.getByRole('status')).toContainText('서비스 이상');
            await expect(frame.locator('#active-accounts')).toHaveCount(0);
          }
          expect(mock.state.calls).toBe(1);
        });
      }
    }
  }

  await scenario('refresh-updates-counter-and-clears-on-auth-failure', async ({ page, frame, mock }) => {
    await page.goto(mock.url);
    await expect(frame.locator('#route-count')).toHaveText('12');
    mock.state.payload = { ...mockStatus, plugin: { ...mockStatus.plugin, route_count: 19 } };
    await frame.locator('#refresh').click();
    await expect(frame.locator('#route-count')).toHaveText('19');
    await expect(frame.locator('#refresh')).toBeFocused();
    mock.state.status = 401;
    await frame.locator('#refresh').click();
    await expect(frame.locator('main')).toHaveAttribute('data-state', 'auth-error');
    await expect(frame.locator('#route-count')).toHaveCount(0);
    expect(mock.state.calls).toBe(3);
    await capture(page, 'refresh-auth-expired');
  });

  await scenario('initial-loading-manual-only-and-no-overlap', async ({ page, frame, mock }) => {
    mock.state.hold = true;
    await page.goto(mock.url);
    await expect(frame.locator('main')).toHaveAttribute('data-state', 'loading');
    await expect(frame.locator('#refresh')).toBeDisabled();
    await capture(page, 'loading');
    mock.release();
    await expect(frame.locator('main')).toHaveAttribute('data-state', 'healthy');
    await page.clock.install();
    await page.clock.fastForward(600_000);
    expect(mock.state.calls).toBe(1);
  });

  await scenario('pagehide-cancels-without-disabling-future-refresh', async ({ page, frame, mock }) => {
    mock.state.hold = true;
    await page.goto(mock.url);
    await expect(frame.locator('main')).toHaveAttribute('data-state', 'loading');
    await frame.locator('html').evaluate(() => window.dispatchEvent(new PageTransitionEvent('pagehide', { persisted: true })));
    await expect(frame.locator('main')).toHaveAttribute('data-state', 'request-error');
    mock.release();
    await frame.locator('#refresh').click();
    await expect(frame.locator('main')).toHaveAttribute('data-state', 'healthy');
    await expect(frame.locator('#route-count')).toHaveText('12');
  });

  for (const auth of ['missing', 'cross-origin', 'blocked']) {
    await scenario(`host-auth-${auth}`, async ({ page, frame, mock }) => {
      await page.goto(`${mock.url}?auth=${auth}`);
      await expect(frame.locator('main')).toHaveAttribute('data-state', 'auth-error');
      await expect(frame.locator('input')).toHaveCount(0);
      expect(mock.state.calls).toBe(0);
      await capture(page, `host-auth-${auth}`);
    });
  }

  await scenario('core-auth-and-parent-provider-navigation', async ({ page, frame, mock }) => {
    await page.goto(`${mock.url}?auth=core`);
    await expect(frame.locator('main')).toHaveAttribute('data-state', 'healthy');
    const keys = await page.evaluate(() => ({ local: Object.keys(localStorage), session: Object.keys(sessionStorage) }));
    expect(keys).toEqual({ local: ['cli-proxy-auth'], session: [] });
    await frame.getByRole('link', { name: '기존 공급자 설정 열기' }).click();
    await expect(page).toHaveURL(`${mock.server.url.origin}/management.html#/ai-providers`);
    expect(mock.state.authorized).toBe(1);
  });

  await scenario('bad-response-and-nullable-observations', async ({ page, frame, mock }) => {
    mock.state.payload = { ...mockStatus, plugin: { ...mockStatus.plugin, route_count: -1 } };
    await page.goto(mock.url);
    await expect(frame.locator('main')).toHaveAttribute('data-state', 'bad-response');
    await expect(frame.locator('#route-count')).toHaveCount(0);
    await capture(page, 'bad-response');
    mock.state.payload = { ...mockStatus, upstream: { healthy: true, version: null, accounts: { active: null, total: 0 } },
      routing: { ...mockStatus.routing, model_names: [] } };
    await frame.locator('#refresh').click();
    await expect(frame.locator('#active-accounts')).toHaveCount(0);
    await expect(frame.locator('#total-accounts')).toHaveText('0');
    await expect(frame.locator('#model-list li')).toHaveCount(0);
    await capture(page, 'nullable-empty');
  });

  for (const status of [403, 500, 302]) {
    await scenario(`http-${status}-safe-error`, async ({ page, frame, mock }) => {
      mock.state.status = status;
      await page.goto(mock.url);
      await expect(frame.locator('main')).toHaveAttribute('data-state', status === 403 ? 'auth-error' : 'request-error');
      await expect(frame.locator('body')).not.toContainText(PRIVATE_SENTINEL);
      expect(mock.state.redirects).toBe(0);
      expect(mock.state.calls).toBe(1);
    });
  }

  await scenario('host-theme-update-long-text-and-reduced-motion', async ({ page, frame, mock }) => {
    mock.state.payload = { ...mockStatus, routing: { ...mockStatus.routing,
      model_names: ['MOCK-' + 'long'.repeat(60), '<img src=x onerror=alert(1)>'] } };
    await page.setViewportSize({ width: 375, height: 1100 });
    await page.goto(mock.url);
    await expect(frame.locator('main')).toHaveAttribute('data-state', 'healthy');
    await page.evaluate(() => { document.documentElement.dataset['theme'] = 'dark';
      document.documentElement.style.setProperty('--text-primary', '#eeddcc'); });
    await expect(frame.locator('h1')).toHaveCSS('color', 'rgb(238, 221, 204)');
    await expect(frame.locator('img')).toHaveCount(0);
    expect(await frame.locator('html').evaluate((root) => root.scrollWidth <= root.clientWidth)).toBe(true);
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await expect(frame.locator('#refresh')).toHaveCSS('transition-duration', '0s');
    await capture(page, 'long-text-dark');
  });
} finally {
  await browser.close();
  await Bun.write(`${evidence}/qa-report.json`, JSON.stringify({ mockOnly: true, results, screenshots,
    cleanup: 'All isolated contexts, loopback servers, and local browser closed.' }, null, 2));
}
if (results.some((result) => !result.passed)) process.exitCode = 1;
console.log(`${results.filter((result) => result.passed).length}/${results.length} MOCK scenarios; ${screenshots.length} screenshots`);
