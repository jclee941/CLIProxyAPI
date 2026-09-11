import { accountsResponseSchema, type Account } from '../src/contract';

export const MOCK_KEY = 'MOCK_MANAGER_CONTEXT';
export const MOCK_TOKEN = 'gemini-web:v1:TU9DSw';
export const observedAt = 1_789_084_800;

export function mockAccounts(): readonly Account[] {
  const usage = {
    tier: 'PRO', tier_code: 2, overage_enabled: false,
    source: 'GoogleWeb', estimated: false, observed_at: observedAt,
    metrics: [
      { remaining_units: 750, usage_fraction: .25, usage_percent: 98,
        reset_unix_seconds: observedAt + 18_000, window_kind: '5h', metric_type: 1, unit: 'provider_compute_unit' },
      { remaining_units: 4200, usage_fraction: .58,
        reset_unix_seconds: observedAt + 604_800, window_kind: 'weekly', metric_type: 2, unit: 'provider_compute_unit' },
    ],
  };
  const models = [{ id: 'gemini-web-flash-3.8', name: 'Gemini Web Flash 3.8' },
    { id: 'gemini-web-omni', name: 'Gemini Web Omni' }];
  return accountsResponseSchema.parse({ provider: 'gemini-web', accounts: [
    { id: 'mock-default', label: 'MOCK · Default', enabled: true, status: 'ready', models, usage, observed_at: observedAt },
    { id: 'mock-profile-1', label: 'MOCK · Profile 1', enabled: true, status: 'ready', models: models.slice(0, 1),
      usage: { ...usage, metrics: [{ ...usage.metrics[0], usage_fraction: .82, remaining_units: 180 },
        { ...usage.metrics[1], usage_fraction: .34, remaining_units: 6600 }] }, observed_at: observedAt },
    { id: 'mock-profile-2', label: 'MOCK · Profile 2', enabled: true, status: 'expired', models, usage: null,
      error: 'MOCK: Google 웹 세션이 만료되었습니다.', observed_at: observedAt },
    { id: 'mock-profile-4', label: 'MOCK · Profile 4', enabled: true, status: 'error', models: [], usage: null,
      error: 'MOCK: Google 응답을 확인하지 못했습니다. 직접 다시 확인해 주세요.', observed_at: observedAt },
    { id: 'mock-profile-5', label: 'MOCK · Profile 5', enabled: false, status: 'unknown', models: [], usage: null,
      observed_at: 0 },
  ] }).accounts;
}
