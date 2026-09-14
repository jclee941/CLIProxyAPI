import { describe, expect, test } from 'bun:test';
import { accountSchema } from '../src/contract';
import { assertLoginLocation, loginOutcome, loginStartSchema, loginViewSchema, sessionSchema } from '../src/login-contract';
import { mockAccounts, MOCK_TOKEN } from './fixtures';

const state = 'MOCK_LOGIN_STATE';
const pending = { state, status: 'pending', expires_at: 1_900_000_000,
  extension_id: 'a'.repeat(32), manager_origin: 'https://manager.example' };

describe('Companion login boundary', () => {
  test('accepts only the fixed start response with an approved extension', () => {
    const result = loginStartSchema.safeParse(pending);
    expect(result.success).toBe(true);
  });

  test.each(['', 'unconfigured', 'z'.repeat(32)])('rejects an invalid extension ID', (extension_id) => {
    const result = loginStartSchema.safeParse({ ...pending, extension_id });
    expect(result.success).toBe(false);
  });

  test.each(['processing', 'host_sync_pending', 'saved', 'error', 'cancelled', 'expired'])('accepts the server lifecycle status %s', (status) => {
    const result = loginViewSchema.safeParse({ state, status, models_ready: false });
    expect(result.success).toBe(true);
  });

  test('requires both readiness conditions before reporting success', () => {
    const view = loginViewSchema.parse({ state, status: 'ready', models_ready: false });
    expect(loginOutcome(view, false)).toBe('pending');
  });

  test('reports a disabled save only when the account is known to be disabled', () => {
    const view = loginViewSchema.parse({ state, status: 'saved', models_ready: false });
    expect(loginOutcome(view, true)).toBe('saved_disabled');
    expect(loginOutcome(view, false)).toBe('pending');
  });

  test('accepts a single explicit session identity without decoding its token', () => {
    const result = sessionSchema.safeParse({ type: 'session', state, token: MOCK_TOKEN,
      account_sha256: 'a'.repeat(64), auth_user: 0 });
    expect(result.success).toBe(true);
  });

  test.each([-1, 0.5, '0'])('rejects an invalid account index', (auth_user) => {
    const result = sessionSchema.safeParse({ type: 'session', state, token: MOCK_TOKEN,
      account_sha256: 'a'.repeat(64), auth_user });
    expect(result.success).toBe(false);
  });

  test.each(['https://manager.example', 'http://127.0.0.1:8123', 'http://localhost:8123', 'http://[::1]:8123'])('permits HTTPS or loopback at the exact resource path', (origin) => {
    expect(() => assertLoginLocation(new URL(`${origin}/v0/resource/plugins/gemini-web/index`), origin)).not.toThrow();
  });

  test.each([
    'http://manager.example/v0/resource/plugins/gemini-web/index',
    'https://manager.example/management.html',
    'https://manager.example/v0/resource/plugins/gemini-web/index?state=private',
  ])('rejects unsafe portal locations before login', (url) => {
    expect(() => assertLoginLocation(new URL(url), 'https://manager.example')).toThrow();
  });

  test('rejects a different approved Manager origin', () => {
    expect(() => assertLoginLocation(new URL('https://manager.example/v0/resource/plugins/gemini-web/index'), 'https://other.example')).toThrow();
  });
});

describe('Account observation compatibility', () => {
  test('preserves an opaque provider tier and nullable metrics', () => {
    const source = mockAccounts()[0];
    const account = accountSchema.parse({ ...source, usage: { ...source?.usage, tier: 'Google AI Custom', metrics: null } });
    expect(account.usage?.tier).toBe('Google AI Custom');
    expect(account.usage?.metrics).toBeNull();
  });
});
