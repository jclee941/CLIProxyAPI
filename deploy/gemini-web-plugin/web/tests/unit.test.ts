import { describe, expect, test } from 'bun:test';
import { decodeStoredAuth, HostAuthError } from '../src/auth';
import { accountsResponseSchema, isWebToken } from '../src/contract';

const environment = {
  origin: 'http://localhost:8123',
  host: 'localhost:8123',
  userAgent: 'MOCK browser',
};
const state = {
  apiBase: environment.origin,
  managementKey: 'MOCK_MANAGER_CONTEXT',
  rememberPassword: true,
  sessionMode: 'manager_embedded',
};

function hostEnvelope(value: unknown): string {
  const bytes = new TextEncoder().encode(JSON.stringify({ state: value, version: 0 }));
  const key = new TextEncoder().encode(
    `cli-proxy-api-webui::secure-storage|${environment.host}|${environment.userAgent}`,
  );
  return `enc::v1::${btoa(Array.from(bytes, (byte, index) =>
    String.fromCharCode(byte ^ (key[index % key.length] ?? 0)),
  ).join(''))}`;
}

describe('Manager persisted authentication', () => {
  test('reads the Manager key when the exact host envelope is obfuscated', () => {
    const stored = hostEnvelope(state);
    const auth = decodeStoredAuth(stored, environment);
    expect(auth.managementKey).toBe('MOCK_MANAGER_CONTEXT');
  });

  test('accepts plaintext envelopes supported by host secureStorage', () => {
    const stored = JSON.stringify({ state, version: 0 });
    const auth = decodeStoredAuth(stored, environment);
    expect(auth.managementKey).toBe('MOCK_MANAGER_CONTEXT');
  });

  test.each([
    null,
    'broken',
    hostEnvelope({ ...state, managementKey: '' }),
    hostEnvelope({ apiBase: environment.origin, rememberPassword: false }),
    hostEnvelope({ ...state, apiBase: 'https://untrusted.invalid' }),
  ])('fails closed when stored authentication is absent, invalid or cross-origin', (stored) => {
    const read = () => decodeStoredAuth(stored, environment);
    expect(read).toThrow(HostAuthError);
  });

  test('rejects the wrong host/UA without exposing stored data', () => {
    const stored = hostEnvelope(state);
    const read = () => decodeStoredAuth(stored, { ...environment, userAgent: 'Other MOCK' });
    expect(read).toThrow(HostAuthError);
  });
});

describe('Opaque token boundary', () => {
  test('accepts a v1 base64url envelope without reading cookie contents', () => {
    const valid = isWebToken('gemini-web:v1:TU9DSw');
    expect(valid).toBe(true);
  });

  test.each(['', '  ', 'MOCK', 'gemini-web:v2:TU9DSw', 'gemini-web:v1:', 'gemini-web:v1:A', 'gemini-web:v1:a+b/'])('rejects invalid token format', (token) => {
    const valid = isWebToken(token);
    expect(valid).toBe(false);
  });
});

describe('Google observation boundary', () => {
  const observation = {
    provider: 'gemini-web',
    accounts: [{
      id: 'mock-id', label: 'MOCK account', enabled: true, status: 'ready', models: [],
      usage: {
        tier: null, tier_code: null, overage_enabled: false,
        metrics: [{ remaining_units: null, usage_fraction: null, reset_unix_seconds: null,
          window_kind: 'unknown', metric_type: 0, unit: 'provider_compute_unit' }],
        source: 'GoogleWeb', estimated: false, observed_at: 1_789_084_800,
      },
      observed_at: 1_789_084_800,
    }],
  };

  test('preserves unavailable provider numbers as null, not zero', () => {
    const parsed = accountsResponseSchema.safeParse(observation);
    expect(parsed.success).toBe(true);
    if (parsed.success) expect(parsed.data.accounts[0]?.usage?.metrics?.[0]?.usage_fraction).toBeNull();
  });

  test('rejects estimated usage rather than labelling it Google observation', () => {
    const account = observation.accounts[0];
    const parsed = accountsResponseSchema.safeParse({ ...observation,
      accounts: [{ ...account, usage: { ...account?.usage, estimated: true } }],
    });
    expect(parsed.success).toBe(false);
  });

  test('does not substitute accounts when the server returns an empty list', () => {
    const parsed = accountsResponseSchema.parse({ provider: 'gemini-web', accounts: [] });
    expect(parsed.accounts).toEqual([]);
  });
});

test('built resource has executable inline JavaScript, with no external assets or mock data', async () => {
  const html = await Bun.file(`${import.meta.dir}/../index.html`).text();
  const scripts = [...html.matchAll(/<script>([\s\S]*?)<\/script>/g)];
  expect(scripts).toHaveLength(1);
  const syntax = Bun.spawn(['node', '--check'], {
    stdin: new Blob([scripts[0]?.[1] ?? '']), stdout: 'pipe', stderr: 'pipe',
  });
  expect(await syntax.exited).toBe(0);
  expect(html).not.toMatch(/<script[^>]+src=|<link[^>]+rel="stylesheet"|MOCK_MANAGER_CONTEXT|MOCK ·/);
});
