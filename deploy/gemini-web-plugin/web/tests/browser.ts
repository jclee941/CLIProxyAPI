import { chromium, expect, type FrameLocator, type Page } from '@playwright/test';
import { mkdir } from 'node:fs/promises';
import { accountSchema } from '../src/contract';
import { MOCK_KEY, MOCK_TOKEN, mockAccounts, observedAt } from './fixtures';
import { startMockServer } from './mock-server';

const evidence = `${import.meta.dir}/../evidence`;
await mkdir(evidence, { recursive: true });
const browser = await chromium.launch({ channel: 'chrome', headless: true });
const results: { readonly name: string; readonly passed: boolean; readonly error?: string }[] = [];
const screenshots: { readonly name: string; readonly width: number; readonly height: number }[] = [];
type Surface = { readonly page: Page; readonly frame: FrameLocator; readonly mock: ReturnType<typeof startMockServer> };

async function capture(page: Page, name: string, full = false): Promise<void> {
  if (full) {
    await page.evaluate(() => {
      const frame = document.querySelector('iframe');
      if (frame?.contentDocument) {
        document.body.style.height = 'auto';
        frame.style.height = `${frame.contentDocument.documentElement.scrollHeight}px`;
      }
    });
  }
  const image = await page.screenshot({ path: `${evidence}/${name}.png`, fullPage: full });
  expect(image.subarray(0, 8).toString('hex')).toBe('89504e470d0a1a0a');
  screenshots.push({ name, width: image.readUInt32BE(16), height: image.readUInt32BE(20) });
  if (full) await page.evaluate(() => {
    document.body.style.removeProperty('height');
    document.querySelector('iframe')?.style.removeProperty('height');
  });
}

async function scenario(name: string, verify: (surface: Surface) => Promise<void>): Promise<void> {
  const mock = startMockServer();
  const context = await browser.newContext({ viewport: { width: 1280, height: 1000 }, timezoneId: 'Asia/Seoul' });
  const page = await context.newPage();
  const pageErrors: string[] = [];
  const externalRequests: string[] = [];
  await page.route('**/*', (route) => {
    if (new URL(route.request().url()).origin === mock.server.url.origin) return route.continue();
    externalRequests.push(new URL(route.request().url()).origin);
    return route.abort();
  });
  page.on('pageerror', (error) => pageErrors.push(error.message));
  try {
    await verify({ page, frame: page.frameLocator('iframe'), mock });
    await expect(page.frameLocator('iframe').locator('body')).not.toContainText(MOCK_KEY);
    await expect(page.frameLocator('iframe').locator('body')).not.toContainText(MOCK_TOKEN);
    expect(pageErrors).toEqual([]);
    expect(externalRequests).toEqual([]);
    results.push({ name, passed: true });
    console.log(`PASS ${name}`);
  } catch (error) {
    if (!(error instanceof Error)) throw error;
    results.push({ name, passed: false, error: error.message });
    console.log(`FAIL ${name}: ${error.message}`);
  } finally {
    mock.releaseRefresh(); mock.releaseList(); mock.releaseSave();
    await context.close();
    await mock.server.stop(true);
  }
}

try {
  for (const theme of ['white', 'dark']) {
    for (const width of [375, 768, 1280]) {
      await scenario(`responsive-${theme}-${width}`, async ({ page, frame, mock }) => {
        await page.setViewportSize({ width, height: 1000 });
        await page.goto(`${mock.url}?theme=${theme}`);
        await expect(frame.locator('article')).toHaveCount(5);
        await expect(frame.locator('html')).toHaveAttribute('data-cpamp-plugin-host', 'true');
        const geometry = await frame.locator('html').evaluate((root) => ({ viewport: root.clientWidth, content: root.scrollWidth }));
        expect(geometry.content).toBeLessThanOrEqual(geometry.viewport);
        await capture(page, `accounts-${theme}-${width}`, true);
        await frame.locator('#add-account').click();
        await expect(frame.locator('#account-label')).toBeFocused();
        await frame.locator('dialog button[type="submit"]').click();
        await expect(frame.locator('#account-label')).toHaveAttribute('aria-invalid', 'true');
        await capture(page, `dialog-error-${theme}-${width}`);
        await page.keyboard.press('Escape');
        await expect(frame.locator('dialog')).toHaveCount(0);
        await expect(frame.locator('#add-account')).toBeFocused();
        expect(mock.state.refreshCount).toBe(0);
      });
    }
  }

  await scenario('model-and-quota-truth', async ({ page, frame, mock }) => {
    await page.goto(mock.url);
    const ready = frame.locator('[data-account-id="mock-default"]');
    await expect(ready.getByRole('meter', { name: '5시간 사용률' })).toHaveAttribute('aria-valuenow', '25');
    await expect(ready).toContainText('남은 연산 단위 750');
    await expect(ready).toContainText('GMT+9');
    await expect(frame.locator('[data-account-id="mock-profile-2"] .badge-model')).toHaveCount(0);
    await expect(frame.locator('[data-account-id="mock-profile-1"] .badge-model')).toHaveCount(1);
    await expect(frame.locator('[data-account-id="mock-profile-5"] .badge-info')).toHaveCount(0);
    const initialCalls = mock.state.authorizedCount;
    await page.clock.install();
    await page.clock.fastForward(600_000);
    expect(mock.state.authorizedCount).toBe(initialCalls);
  });

  await scenario('bounded-refresh-with-partial-failure', async ({ page, frame, mock }) => {
    await page.goto(mock.url);
    await expect(frame.locator('article')).toHaveCount(5);
    mock.state.holdRefresh = true;
    mock.state.failRefreshId = 'mock-profile-1';
    await frame.locator('#refresh-all').click();
    await expect.poll(() => mock.state.activeRefreshes).toBe(2);
    await expect(frame.locator('#add-account')).toBeDisabled();
    await capture(page, 'refresh-pending', true);
    mock.releaseRefresh();
    await expect.poll(() => mock.state.refreshCount).toBe(4);
    mock.releaseRefresh();
    await expect.poll(() => mock.state.refreshCount).toBe(5);
    mock.state.holdRefresh = false;
    mock.releaseRefresh();
    await expect(frame.locator('#refresh-all')).toBeEnabled();
    await expect(frame.locator('[data-account-id="mock-profile-1"]')).toContainText('이전 관측값');
    await capture(page, 'refresh-partial-error', true);
    expect(mock.state.peakRefreshes).toBe(2);
    expect(mock.state.refreshCount).toBe(5);
    expect(mock.state.listCount).toBe(1);
    await expect(frame.locator('[data-account-id="mock-default"] .account-footer')).toContainText('09:02');
  });

  await scenario('registration-validation-masking-failure-and-success', async ({ page, frame, mock }) => {
    await page.goto(mock.url);
    await expect(frame.locator('article')).toHaveCount(5);
    await frame.locator('#add-account').click();
    await frame.locator('#account-label').fill('MOCK · 새 프로필');
    await frame.locator('#account-token').fill('invalid MOCK');
    await frame.locator('dialog button[type="submit"]').click();
    expect(mock.state.submissions).toHaveLength(0);
    await expect(frame.locator('#account-token')).toBeFocused();
    await frame.locator('#account-token').fill(MOCK_TOKEN);
    await expect(frame.locator('#account-token')).toHaveCSS('-webkit-text-security', 'disc');
    await capture(page, 'dialog-masked');
    mock.state.saveStatus = 422;
    await frame.locator('dialog button[type="submit"]').click();
    await expect(frame.locator('#token-error')).toBeVisible();
    await expect(frame.locator('#account-token')).toHaveValue('');
    await expect(frame.locator('dialog')).not.toContainText('private response');
    await capture(page, 'dialog-server-error');
    mock.state.saveStatus = 200;
    mock.state.holdSave = true;
    await frame.locator('#account-token').fill(MOCK_TOKEN);
    await frame.locator('dialog button[type="submit"]').click();
    await expect.poll(() => mock.state.submissions.length).toBe(2);
    await expect(frame.locator('#account-token')).toHaveValue('');
    await expect(frame.locator('#account-token')).toBeDisabled();
    await capture(page, 'dialog-submitting');
    mock.releaseSave();
    await expect(frame.locator('dialog')).toHaveCount(0);
    await expect(frame.locator('article')).toHaveCount(6);
    expect(mock.state.submissions.every((entry) => entry.validToken)).toBe(true);
    expect(await page.evaluate(() => Object.keys(localStorage))).toEqual(['cli-proxy-auth']);
    await capture(page, 'registration-success', true);
  });

  await scenario('expired-token-replacement-and-dialog-keyboard', async ({ page, frame, mock }) => {
    await page.goto(mock.url);
    const target = frame.locator('[data-account-id="mock-profile-2"]');
    await target.getByRole('button', { name: /토큰 업데이트/ }).click();
    await expect(frame.locator('#account-token')).toBeFocused();
    await expect(frame.locator('#account-label')).toHaveValue('MOCK · Profile 2');
    await frame.locator('dialog button[type="submit"]').focus();
    await page.keyboard.press('Tab');
    await expect(frame.getByRole('button', { name: '닫기', exact: true })).toBeFocused();
    await page.keyboard.press('Shift+Tab');
    await expect(frame.locator('dialog button[type="submit"]')).toBeFocused();
    await frame.locator('#account-token').fill(MOCK_TOKEN);
    await frame.locator('dialog button[type="submit"]').click();
    await expect(frame.locator('dialog')).toHaveCount(0);
    expect(mock.state.submissions[0]?.existingId).toBe('mock-profile-2');
    await expect(target).toContainText('사용 가능');
    await expect(target.locator('.badge-model')).toHaveCount(1);
    await expect(target).not.toContainText('토큰 만료');
  });

  await scenario('expired-google-token-is-not-manager-logout', async ({ page, frame, mock }) => {
    mock.state.saveStatus = 401;
    mock.state.saveError = 'account_unavailable';
    await page.goto(mock.url);
    await frame.locator('#add-account').click();
    await frame.locator('#account-label').fill('MOCK expired token');
    await frame.locator('#account-token').fill(MOCK_TOKEN);
    await frame.locator('dialog button[type="submit"]').click();
    await expect(frame.locator('#token-error')).toContainText('웹 토큰');
    await expect(frame.locator('#token-error')).not.toContainText('Manager 인증');
    await expect(frame.locator('#account-token')).toHaveValue('');
    await capture(page, 'expired-registration');
  });

  await scenario('manager-unauthorized-is-actionable', async ({ page, frame, mock }) => {
    mock.state.listStatus = 401;
    await page.goto(mock.url);
    await expect(frame.locator('.state-panel')).toContainText('Manager 인증');
    await expect(frame.getByRole('button', { name: '연결 다시 확인' })).toBeVisible();
    await expect(frame.locator('#add-account')).toBeDisabled();
    await capture(page, 'manager-unauthorized');
  });

  for (const mode of ['missing', 'cross-origin', 'blocked']) {
    await scenario(`authentication-${mode}`, async ({ page, frame, mock }) => {
      await page.goto(`${mock.url}?auth=${mode}`);
      await expect(frame.getByRole('button', { name: '연결 다시 확인' })).toBeVisible();
      await expect(frame.locator('#add-account')).toBeDisabled();
      await expect(frame.locator('article')).toHaveCount(0);
      expect(mock.state.authorizedCount).toBe(0);
      await capture(page, `auth-${mode}`);
    });
  }

  await scenario('loading-empty-and-api-recovery', async ({ page, frame, mock }) => {
    mock.state.holdList = true;
    await page.goto(mock.url);
    await expect(frame.locator('.state-panel')).toContainText('불러오는 중');
    await capture(page, 'loading');
    mock.state.listStatus = 500;
    mock.state.holdList = false;
    mock.releaseList();
    await expect(frame.getByRole('button', { name: '연결 다시 확인' })).toBeVisible();
    await capture(page, 'api-error');
    mock.state.listStatus = 200;
    mock.state.accounts = [];
    await frame.getByRole('button', { name: '연결 다시 확인' }).click();
    await expect(frame.locator('.state-panel')).toContainText('등록된 계정이 없습니다');
    await expect(frame.locator('article')).toHaveCount(0);
    await expect(frame.locator('#add-account')).toBeEnabled();
    await capture(page, 'empty');
  });

  await scenario('nullable-unknown-credit-quotas-and-long-label', async ({ page, frame, mock }) => {
    mock.state.accounts = [accountSchema.parse({ ...mockAccounts()[0],
      label: 'MOCK · 긴 계정 이름과 <img src=x onerror=alert(1)> 안전한 문자열 표시 확인',
      usage: { tier: null, tier_code: null, overage_enabled: true, source: 'GoogleWeb', estimated: false, observed_at: observedAt,
        metrics: [
          { remaining_units: null, usage_fraction: null, reset_unix_seconds: null, window_kind: 'unknown', metric_type: 0, unit: 'provider_compute_unit' },
          { remaining_units: 0, usage_fraction: 1, reset_unix_seconds: null, window_kind: 'ai_credit', metric_type: 3, unit: 'provider_compute_unit' },
        ] },
    })];
    await page.setViewportSize({ width: 375, height: 1000 });
    await page.goto(mock.url);
    await expect(frame.locator('article')).toHaveCount(1);
    await expect(frame.getByRole('meter')).toHaveCount(1);
    await expect(frame.getByRole('meter')).toHaveAttribute('aria-valuenow', '100');
    await expect(frame.locator('article')).toContainText('남은 연산 단위 미확인');
    await expect(frame.locator('img')).toHaveCount(0);
    await capture(page, 'unknown-credit-long-label', true);
  });

  await scenario('live-host-theme-and-reduced-motion', async ({ page, frame, mock }) => {
    await page.goto(mock.url);
    await expect(frame.locator('article')).toHaveCount(5);
    await page.evaluate(() => document.documentElement.dataset['theme'] = 'dark');
    await expect(frame.locator('html')).toHaveAttribute('data-theme', 'dark');
    await page.evaluate(() => document.documentElement.style.setProperty('--text-primary', '#eeddcc'));
    await expect(frame.locator('h1')).toHaveCSS('color', 'rgb(238, 221, 204)');
    await page.emulateMedia({ reducedMotion: 'reduce' });
    mock.state.holdRefresh = true;
    await frame.locator('#refresh-all').click();
    await expect(frame.locator('.is-loading svg').first()).toHaveCSS('animation-name', 'none');
    await capture(page, 'reduced-motion-dark');
  });
} finally {
  await browser.close();
  await Bun.write(`${evidence}/qa-report.json`, JSON.stringify({ mockOnly: true, results, screenshots, cleanup: 'All isolated browser contexts, mock servers, and browser closed.' }, null, 2));
}
if (results.some((result) => !result.passed)) process.exitCode = 1;
console.log(`${results.filter((result) => result.passed).length}/${results.length} MOCK browser scenarios passed; ${screenshots.length} screenshots.`);
