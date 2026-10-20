import { chromium, expect } from '@playwright/test';

const browser = await chromium.launch({ channel: 'chrome', headless: true });
const origin = 'http://localhost';
const resource = '/v0/resource/plugins/flow2api/index';
const endpoint = '/v0/management/plugins/flow2api/accounts';
const html = await Bun.file(`${import.meta.dir}/../../../flow2api-plugin/web/index.html`).text();
try {
  for (const width of [390, 1440]) {
    for (const colorScheme of ['light', 'dark'] as const) {
      const context = await browser.newContext({ viewport: { width, height: 1000 }, colorScheme });
      const page = await context.newPage();
      let busy = false;
      const methods: string[] = [];
      const alerts: string[] = [];
      await context.route(`${origin}/wrapper`, route => route.fulfill({
        contentType: 'text/html',
        body: `<body style="margin:0"><iframe src="${resource}" style="width:100%;height:100vh;border:0"></iframe></body>`,
      }));
      await context.route(`${origin}${resource}`, route => route.fulfill({
        contentType: 'text/html',
        body: html.replace('</head>', `<script>
          document.documentElement.dataset.theme = '${colorScheme}';
          window.__CPAMP_PLUGIN_HOST__ = {
            version: 1, pluginID: 'flow2api', resourceURL: location.href,
            request: async input => {
              const response = await fetch(input.path, {method: input.method});
              return {status: response.status, body: await response.text()};
            }
          };
        </script></head>`),
      }));
      await context.route(`${origin}${endpoint}`, route => {
        methods.push(route.request().method());
        return route.fulfill({ json: {
          provider: 'flow2api',
          accounts: busy
            ? [{ id: 'a', status: 'busy', error: 'flow_session_exchange_busy', health: {
              status: 'blocked', held_until: 1700001800, checked_at: 1700000000, check_kind: 'hold',
              check_result: 'refused', check_error: 'PUBLIC_ERROR_UNUSUAL_ACTIVITY', next_check: 1700001800,
            } }]
            : [{ id: 'a', label: 'Fixture', status: 'ready', credits: 0, observed_at: 1700000000, health: {
              status: 'blocked', held_until: 1700001800, checked_at: 1700000000, check_kind: 'hold',
              check_result: 'refused', check_error: 'PUBLIC_ERROR_UNUSUAL_ACTIVITY', next_check: 1700001800,
            } }],
          models: [{ ID: 'flow-omni-1.1-flash' }],
          health_checks: { interval_seconds: 3600, telegram: true },
        } });
      });
      await context.route(`${origin}/v0/management/plugins/flow2api/telegram/test`, route => {
        alerts.push(route.request().method());
        return route.fulfill({ json: { status: 'sent' } });
      });
      await page.goto(`${origin}/wrapper`);
      const frame = page.frameLocator('iframe');
      await expect(frame.locator('[data-credits="0"]')).toBeVisible();
      await expect(frame.locator('html')).toHaveAttribute('data-theme', colorScheme);
      await expect(frame.locator('[data-health="blocked"]')).toBeVisible();
      await expect(frame.locator('.health-policy')).toContainText('1시간마다');
      await frame.locator('#test-alert').click();
      await expect(frame.locator('.feedback')).toContainText('테스트 알림을 보냈습니다');
      expect(alerts).toEqual(['POST']);
      busy = true;
      await frame.locator('#refresh-all').click();
      await expect(frame.locator('.notice-warning')).toBeVisible();
      await expect(frame.locator('[data-credits="0"]')).toBeVisible();
      expect(methods).toEqual(['GET', 'GET']);
      expect(await frame.locator('body').evaluate(() =>
        document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      await page.screenshot({ path: `/tmp/flow-fixture-${width}-${colorScheme}.png`, fullPage: true });
      await context.close();
      console.log(`PASS Flow bridge, zero, stale, health, test alert, GET-only listing ${width} ${colorScheme}`);
    }
  }
} finally {
  await browser.close();
}
