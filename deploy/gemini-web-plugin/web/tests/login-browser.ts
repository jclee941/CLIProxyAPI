import { expect, type FrameLocator, type Page } from '@playwright/test';
import type { Surface } from './browser';
import { installFakeCompanion, type CompanionMode } from './fake-companion';
import { MOCK_KEY, MOCK_TOKEN } from './fixtures';

type Scenario = (name: string, verify: (surface: Surface) => Promise<void>) => Promise<void>;
type Capture = (page: Page, name: string, full?: boolean) => Promise<void>;

async function prepare(surface: Surface, mode: CompanionMode = 'normal', existingId?: string): Promise<void> {
  const { page, frame, mock } = surface;
  await page.addInitScript(installFakeCompanion, mode);
  await page.goto(mock.url);
  await frame.locator(existingId ? `#login-${existingId}` : '#google-login').click();
  if (!existingId) await frame.locator('#login-label').fill('MOCK · Google 연결');
  await frame.locator('#login-consent').check();
  await frame.locator('#login-start').click();
}

async function approve(frame: FrameLocator): Promise<void> {
  await frame.locator('html').evaluate(() => window.dispatchEvent(new Event('MOCK_COMPANION_APPROVE')));
}

async function portEvidence(frame: FrameLocator) {
  return frame.locator('html').evaluate(() => window.mockCompanionEvidence?.());
}

async function showPanel(page: Page): Promise<void> {
  await page.evaluate(() => {
    const frame = document.querySelector('iframe');
    const panel = frame?.contentDocument?.querySelector<HTMLElement>('.login-panel');
    if (panel && frame?.contentWindow) frame.contentWindow.scrollTo(0, panel.offsetTop - 16);
  });
}

async function assertPrivate(surface: Surface): Promise<void> {
  const html = await surface.frame.locator('html').evaluate((root) => root.outerHTML);
  const storage = await surface.page.evaluate(() => ({ local: Object.keys(localStorage), session: Object.keys(sessionStorage) }));
  for (const secret of [MOCK_TOKEN, MOCK_KEY, surface.mock.login.state.nonce, 'MOCK_PRIVATE_ERROR', 'MOCK_PRIVATE_EXTENSION_ERROR']) {
    expect(html.includes(secret)).toBe(false);
    expect(surface.page.url().includes(secret)).toBe(false);
  }
  expect(storage).toEqual({ local: ['cli-proxy-auth'], session: [] });
  expect((await portEvidence(surface.frame))?.writes).toBe(0);
  expect(surface.mock.login.state.originsValid).toBe(true);
  expect(surface.mock.login.state.methodsValid).toBe(true);
}

export async function runLoginScenarios(scenario: Scenario, capture: Capture): Promise<void> {
  await scenario('login-explicit-consent-and-start', async ({ page, frame, mock }) => {
    await page.addInitScript(installFakeCompanion, 'normal' as const);
    await page.goto(mock.url);
    await frame.locator('#google-login').click();
    await expect(frame.locator('#login-label')).toBeFocused();
    await expect(frame.locator('#login-start')).toBeDisabled();
    await frame.locator('#login-label').fill('MOCK · explicit consent');
    await expect(frame.locator('#login-start')).toBeDisabled();
    expect(mock.login.state.starts).toBe(0);
    await frame.locator('#login-consent').focus();
    await page.keyboard.press('Space');
    await expect(frame.locator('#login-start')).toBeEnabled();
    await page.keyboard.press('Tab');
    await page.keyboard.press('Enter');
    await expect(frame.locator('#login-connect')).toBeVisible();
    expect(mock.login.state.input).toEqual({ label: 'MOCK · explicit consent', consent: true });
    await assertPrivate({ page, frame, mock });
  });

  for (const theme of ['white', 'dark']) for (const width of [375, 768, 1280]) {
    await scenario(`login-responsive-${theme}-${width}`, async (surface) => {
      const { page, frame, mock } = surface;
      await page.setViewportSize({ width, height: 1000 });
      await page.addInitScript(installFakeCompanion, 'normal' as const);
      await page.goto(`${mock.url}?theme=${theme}`);
      await frame.locator('#google-login').click();
      await frame.locator('#login-label').fill('MOCK · 명확한 Google 계정 선택과 승인');
      await showPanel(page);
      await capture(page, `login-consent-${theme}-${width}`);
      await frame.locator('#login-consent').check();
      await frame.locator('#login-start').click();
      await frame.locator('#login-connect').click();
      await expect(frame.locator('.login-panel')).toHaveAttribute('data-phase', 'approval');
      const geometry = await frame.locator('html').evaluate((root) => ({ width: root.clientWidth, content: root.scrollWidth }));
      expect(geometry.content).toBeLessThanOrEqual(geometry.width);
      await showPanel(page);
      await capture(page, `login-approval-${theme}-${width}`);
      await frame.locator('#login-cancel').click();
      await expect(frame.locator('.login-panel')).toHaveAttribute('data-phase', 'cancelled');
      await assertPrivate(surface);
    });
  }

  for (const mode of ['absent', 'unavailable', 'throw-connect', 'disconnect', 'error', 'early-session', 'malformed'] as const) {
    await scenario(`companion-${mode}-is-actionable`, async (surface) => {
      await prepare(surface, mode);
      await surface.frame.locator('#login-connect').click();
      await expect(surface.frame.locator('#login-error')).toBeVisible();
      expect(surface.mock.login.state.completes).toBe(0);
      await expect(surface.frame.locator('#account-token')).toHaveCount(0);
      if (mode === 'absent') await capture(surface.page, 'login-companion-install');
      if (mode === 'unavailable') expect((await portEvidence(surface.frame))?.lastErrorReads).toBeGreaterThan(0);
      await assertPrivate(surface);
    });
  }

  for (const mode of ['wrong-state', 'untrusted-port'] as const) {
    await scenario(`companion-${mode}-never-hands-off`, async (surface) => {
      await prepare(surface, mode);
      await surface.frame.locator('#login-connect').click();
      await expect(surface.frame.locator('.login-panel')).toHaveAttribute('data-phase', 'approval');
      await approve(surface.frame);
      expect(surface.mock.login.state.completes).toBe(0);
      await surface.frame.locator('#login-cancel').click();
      await expect(surface.frame.locator('.login-panel')).toHaveAttribute('data-phase', 'cancelled');
      expect((await portEvidence(surface.frame))?.messages).toEqual(['begin', 'cancel']);
      await assertPrivate(surface);
    });
  }

  await scenario('session-once-ready-ack-no-leaks', async (surface) => {
    const consoleLeaks: boolean[] = [];
    surface.page.on('console', (message) => consoleLeaks.push([MOCK_TOKEN, MOCK_KEY, surface.mock.login.state.nonce, 'MOCK_PRIVATE'].some((secret) => message.text().includes(secret))));
    surface.mock.login.state.holdComplete = true;
    await prepare(surface);
    await surface.frame.locator('#login-connect').click();
    await approve(surface.frame);
    await expect(surface.frame.locator('.login-panel')).toHaveAttribute('data-phase', 'processing');
    await expect.poll(() => surface.mock.login.state.completes).toBe(1);
    await approve(surface.frame);
    await capture(surface.page, 'login-processing');
    expect(surface.mock.login.state.completes).toBe(1);
    surface.mock.login.release();
    await expect(surface.frame.locator('.login-panel')).toHaveAttribute('data-phase', 'ready');
    await expect(surface.frame.locator('article')).toHaveCount(6);
    expect(surface.mock.login.state.validTransfers).toBe(1);
    expect((await portEvidence(surface.frame))?.messages).toEqual(['begin', 'ack']);
    expect((await portEvidence(surface.frame))?.disconnects).toBe(1);
    expect((await portEvidence(surface.frame))?.fields).toEqual([['type', 'state'], ['type', 'state']]);
    expect(consoleLeaks.some(Boolean)).toBe(false);
    await assertPrivate(surface);
    await capture(surface.page, 'login-ready', true);
  });

  for (const failedCheck of [false, true]) {
    await scenario(`approval-survives-status-check-${failedCheck ? 'failure' : 'pending'}`, async (surface) => {
      await prepare(surface);
      await surface.frame.locator('#login-connect').click();
      await expect(surface.frame.locator('.login-panel')).toHaveAttribute('data-phase', 'approval');
      if (failedCheck) surface.mock.login.state.actionStatus = 503;
      await surface.frame.locator('#login-check').click();
      await expect.poll(() => surface.mock.login.state.statuses).toBe(1);
      await expect(surface.frame.locator('#login-check')).toBeVisible();
      await approve(surface.frame);
      await expect(surface.frame.locator('.login-panel')).toHaveAttribute('data-phase', 'ready');
      expect(surface.mock.login.state.completes).toBe(1);
      expect((await portEvidence(surface.frame))?.messages).toEqual(['begin', 'ack']);
      await assertPrivate(surface);
    });
  }

  await scenario('existing-account-bound-identity-and-disabled-save', async (surface) => {
    surface.mock.login.state.expectedIdentity = { account_sha256: 'b'.repeat(64), auth_user: 1 };
    surface.mock.login.state.finalStatus = 'saved';
    await prepare(surface, 'normal', 'mock-profile-5');
    await surface.frame.locator('#login-connect').click();
    await approve(surface.frame);
    await expect(surface.frame.locator('#login-status')).toContainText('Saved, disabled');
    expect(surface.mock.login.state.input?.existing_id).toBe('mock-profile-5');
    await expect(surface.frame.locator('[data-account-id="mock-profile-5"] .badge-model')).toHaveCount(0);
    expect((await portEvidence(surface.frame))?.fields[0]).toEqual(['type', 'state', 'expected_identity']);
    await capture(surface.page, 'login-saved-disabled');
    await assertPrivate(surface);
  });

  await scenario('companion-account-mismatch-rejected', async (surface) => {
    surface.mock.login.state.expectedIdentity = { account_sha256: 'b'.repeat(64), auth_user: 1 };
    await prepare(surface, 'identity-mismatch', 'mock-profile-1');
    await surface.frame.locator('#login-connect').click();
    await approve(surface.frame);
    await expect(surface.frame.locator('#login-error')).toBeVisible();
    expect(surface.mock.login.state.completes).toBe(0);
    await assertPrivate(surface);
  });

  await scenario('host-sync-reconciles-only-on-explicit-action', async (surface) => {
    surface.mock.login.state.finalStatus = 'host_sync_pending';
    await prepare(surface);
    await surface.frame.locator('#login-connect').click();
    await approve(surface.frame);
    await expect(surface.frame.locator('#login-reconcile')).toBeVisible();
    await surface.page.clock.install();
    await surface.page.clock.fastForward(600_000);
    expect(surface.mock.login.state.reconciles).toBe(0);
    expect(surface.mock.login.state.statuses).toBe(0);
    await capture(surface.page, 'login-host-sync-pending');
    await surface.frame.locator('#login-reconcile').click();
    await expect(surface.frame.locator('.login-panel')).toHaveAttribute('data-phase', 'ready');
    expect(surface.mock.login.state.reconciles).toBe(1);
    expect(surface.mock.login.state.completes).toBe(1);
    await assertPrivate(surface);
  });

  for (const status of ['error', 'expired', 'saved', 'ready'] as const) {
    await scenario(`login-${status}-without-models-is-not-ready`, async (surface) => {
      surface.mock.login.state.finalStatus = status;
      surface.mock.login.state.modelsReady = false;
      await prepare(surface);
      await surface.frame.locator('#login-connect').click();
      await approve(surface.frame);
      await expect(surface.frame.locator('.login-panel')).toHaveAttribute('data-phase', status);
      await expect(surface.frame.locator('#login-status .badge-success')).toHaveCount(0);
      await assertPrivate(surface);
    });
  }

  for (const code of [400, 401, 429, 503]) {
    await scenario(`login-start-http-${code}-safe-error`, async (surface) => {
      surface.mock.login.state.startStatus = code;
      await prepare(surface);
      await expect(surface.frame.locator('#login-error')).toBeVisible();
      expect(surface.mock.login.state.completes).toBe(0);
      expect((await portEvidence(surface.frame))?.connects).toBe(0);
      await assertPrivate(surface);
    });
  }

  await scenario('login-origin-configuration-fails-closed', async (surface) => {
    surface.mock.login.state.originOverride = 'https://untrusted.invalid';
    await prepare(surface);
    await expect(surface.frame.locator('#login-error')).toBeVisible();
    expect((await portEvidence(surface.frame))?.connects).toBe(0);
    await assertPrivate(surface);
  });

  await scenario('complete-response-loss-uses-status-not-replay', async (surface) => {
    surface.mock.login.state.completeStatus = 503;
    await prepare(surface);
    await surface.frame.locator('#login-connect').click();
    await approve(surface.frame);
    await expect(surface.frame.locator('#login-error')).toBeVisible();
    await expect(surface.frame.locator('#login-connect')).toBeHidden();
    await surface.frame.locator('#login-check').click();
    await expect(surface.frame.locator('.login-panel')).toHaveAttribute('data-phase', 'ready');
    expect(surface.mock.login.state.completes).toBe(1);
    expect(surface.mock.login.state.statuses).toBe(1);
    await assertPrivate(surface);
  });

  await scenario('wrong-api-state-never-reports-success', async (surface) => {
    surface.mock.login.state.wrongReplyState = true;
    await prepare(surface);
    await surface.frame.locator('#login-connect').click();
    await approve(surface.frame);
    await expect(surface.frame.locator('#login-error')).toBeVisible();
    await expect(surface.frame.locator('#login-status .badge-success')).toHaveCount(0);
    expect(surface.mock.login.state.completes).toBe(1);
    await assertPrivate(surface);
  });

  await scenario('cancel-before-session-disconnects-and-blocks-handoff', async (surface) => {
    await prepare(surface);
    await surface.frame.locator('#login-connect').click();
    await surface.frame.locator('#login-cancel').click();
    await expect(surface.frame.locator('.login-panel')).toHaveAttribute('data-phase', 'cancelled');
    await approve(surface.frame);
    expect(surface.mock.login.state.cancels).toBe(1);
    expect(surface.mock.login.state.completes).toBe(0);
    expect((await portEvidence(surface.frame))?.disconnects).toBe(1);
    await assertPrivate(surface);
  });

  await scenario('cancel-processing-does-not-delete-a-committed-session', async (surface) => {
    surface.mock.login.state.holdComplete = true;
    await prepare(surface);
    await surface.frame.locator('#login-connect').click();
    await approve(surface.frame);
    await expect.poll(() => surface.mock.login.state.completes).toBe(1);
    await surface.frame.locator('#login-cancel').click();
    await expect(surface.frame.locator('#login-check')).toBeVisible();
    surface.mock.login.release();
    await expect.poll(() => surface.mock.login.state.status).toBe('ready');
    await surface.frame.locator('#login-check').click();
    await expect(surface.frame.locator('.login-panel')).toHaveAttribute('data-phase', 'ready');
    expect((await portEvidence(surface.frame))?.messages).toEqual(['begin', 'cancel']);
    expect(surface.mock.login.state.completes).toBe(1);
    await assertPrivate(surface);
  });

  await scenario('login-local-expiry-never-polls-or-captures', async (surface) => {
    await surface.page.clock.install();
    await prepare(surface);
    await surface.frame.locator('#login-connect').click();
    await expect(surface.frame.locator('.login-panel')).toHaveAttribute('data-phase', 'approval');
    await surface.page.clock.fastForward(600_001);
    await expect(surface.frame.locator('.login-panel')).toHaveAttribute('data-phase', 'expired');
    await approve(surface.frame);
    expect(surface.mock.login.state.completes).toBe(0);
    expect(surface.mock.login.state.statuses).toBe(0);
    expect((await portEvidence(surface.frame))?.messages).toEqual(['begin', 'cancel']);
    await capture(surface.page, 'login-expired');
    await assertPrivate(surface);
  });

  await scenario('refresh-200-error-is-failed-and-stale', async ({ page, frame, mock }) => {
    await page.goto(mock.url);
    await expect(frame.locator('article')).toHaveCount(5);
    mock.state.accounts = mock.state.accounts.map((account) => account.id === 'mock-default'
      ? { ...account, status: 'error', usage: null, error: 'secret_store_unavailable' } : account);
    await frame.locator('#refresh-all').click();
    await expect(frame.locator('#refresh-all')).toBeEnabled();
    const card = frame.locator('[data-account-id="mock-default"]');
    await expect(card).toContainText('이전 관측값');
    await expect(card.getByRole('meter', { name: '5시간 사용률' })).toHaveAttribute('aria-valuenow', '25');
    await expect(card.locator('.badge-model')).toHaveCount(0);
    await expect(frame.locator('.feedback')).toContainText('조회 실패');
    await expect(frame.locator('.feedback')).toContainText('1개 사용 가능 확인 · 4개 조회 실패');
    expect(mock.state.listCount).toBe(1);
  });

  await scenario('refresh-200-expired-preserves-observation-until-ready', async ({ page, frame, mock }) => {
    await page.goto(mock.url);
    await expect(frame.locator('article')).toHaveCount(5);
    const original = mock.state.accounts.find((account) => account.id === 'mock-default');
    if (!original) throw new Error('MOCK account missing');
    mock.state.accounts = mock.state.accounts.map((account) => account.id === original.id
      ? { ...account, status: 'expired', usage: null } : account);
    await frame.locator('#refresh-mock-default').click();
    const card = frame.locator('[data-account-id="mock-default"]');
    await expect(card).toContainText('이전 관측값');
    await expect(card.locator('.account-footer')).toContainText('09:00');
    await expect(card.getByRole('meter', { name: '5시간 사용률' })).toHaveAttribute('aria-valuenow', '25');
    mock.state.accounts = mock.state.accounts.map((account) => account.id === original.id ? original : account);
    mock.state.holdRefresh = true;
    await frame.locator('#refresh-mock-default').click();
    await expect(card).toContainText('이전 관측값');
    mock.releaseRefresh();
    await expect(frame.locator('#refresh-mock-default')).toBeEnabled();
    await expect(card).not.toContainText('이전 관측값');
    await expect(card.locator('.account-footer')).toContainText('09:02');
    await expect(card.locator('.badge-model')).toHaveCount(2);
  });

  await scenario('failed-cancel-requires-status-not-session-recapture', async (surface) => {
    await prepare(surface);
    await surface.frame.locator('#login-connect').click();
    surface.mock.login.state.actionStatus = 503;
    await surface.frame.locator('#login-cancel').click();
    await expect(surface.frame.locator('#login-error')).toBeVisible();
    await expect(surface.frame.locator('#login-connect')).toBeHidden();
    await approve(surface.frame);
    expect(surface.mock.login.state.completes).toBe(0);
    surface.mock.login.state.actionStatus = 200;
    await surface.frame.locator('#login-check').click();
    await expect(surface.frame.locator('.login-panel')).toHaveAttribute('data-phase', 'cancelled');
    await assertPrivate(surface);
  });

  await scenario('failed-reconcile-is-not-success-or-auto-retried', async (surface) => {
    surface.mock.login.state.finalStatus = 'host_sync_pending';
    await prepare(surface);
    await surface.frame.locator('#login-connect').click();
    await approve(surface.frame);
    surface.mock.login.state.actionStatus = 503;
    await surface.frame.locator('#login-reconcile').click();
    await expect(surface.frame.locator('#login-error')).toBeVisible();
    await expect(surface.frame.locator('#login-status .badge-success')).toHaveCount(0);
    await surface.page.clock.install();
    await surface.page.clock.fastForward(600_000);
    expect(surface.mock.login.state.reconciles).toBe(1);
    await assertPrivate(surface);
  });
}
