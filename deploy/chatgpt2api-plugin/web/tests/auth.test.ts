import assert from 'node:assert/strict';
import { test } from 'node:test';
import { decodeStoredAuth, HostAuthError } from '../src/host-auth.ts';
import { MOCK_KEY } from './fixtures.ts';

const environment = { origin: 'http://localhost:8123', host: 'localhost:8123', userAgent: 'MOCK browser' };
const state = { apiBase: environment.origin, managementKey: MOCK_KEY, rememberPassword: true };
const plaintext = JSON.stringify({ state, version: 0 });
const salt = new TextEncoder().encode(`cli-proxy-api-webui::secure-storage|${environment.host}|${environment.userAgent}`);
const saltView = new DataView(salt.buffer);
const obfuscated = `enc::v1::${btoa(Array.from(new TextEncoder().encode(plaintext), (byte, index) =>
  String.fromCharCode(byte ^ saltView.getUint8(index % salt.length))).join(''))}`;

for (const raw of [plaintext, obfuscated]) {
  test(`reads the exact host envelope when ${raw.startsWith('enc::') ? 'obfuscated' : 'plaintext'}`, () => {
    const result = decodeStoredAuth(raw, environment);
    assert.deepEqual(result, { managementKey: MOCK_KEY });
  });
}

for (const raw of [null, '{}', 'broken', 'enc::v1::invalid',
  JSON.stringify({ state: { apiBase: environment.origin } }),
  JSON.stringify({ state: { ...state, apiBase: 'https://untrusted.invalid' } }),
  JSON.stringify({ state: { ...state, apiBase: 'http://user@localhost:8123' } }),
  JSON.stringify({ state: { ...state, managementKey: 'header\r\ninjection' } }),
]) {
  test(`fails closed on invalid host storage case ${String(raw)}`, () => {
    const read = () => decodeStoredAuth(raw, environment);
    assert.throws(read, HostAuthError);
  });
}

test('fails closed when host or browser identity differs', () => {
  const read = () => decodeStoredAuth(obfuscated, { ...environment, userAgent: 'Other MOCK browser' });
  assert.throws(read, HostAuthError);
});
