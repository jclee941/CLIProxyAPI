import assert from 'node:assert/strict';
import { test } from 'node:test';
import { parseStatus } from '../src/contract.ts';
import { mockStatus, PRIVATE_SENTINEL } from './fixtures.ts';

test('preserves the frozen status when the server returns valid data', () => {
  const input = structuredClone(mockStatus);
  const result = parseStatus(input);
  assert.deepEqual(result, input);
});

for (const upstream of [
  { healthy: false },
  { healthy: true, version: null, accounts: null },
  { healthy: true, accounts: { active: null, total: 0, limited: null, abnormal: 1 } },
  { healthy: true, accounts: {} },
]) {
  test(`preserves absent or nullable observations: ${JSON.stringify(upstream)}`, () => {
    const input = { ...mockStatus, upstream };
    const result = parseStatus(input);
    assert.deepEqual(result, input);
  });
}

test('discards unknown fields and raw upstream errors when parsing health', () => {
  const input = { ...mockStatus, secret: PRIVATE_SENTINEL,
    plugin: { ...mockStatus.plugin, token: PRIVATE_SENTINEL },
    upstream: { ...mockStatus.upstream, error: PRIVATE_SENTINEL } };
  const result = parseStatus(input);
  assert.deepEqual(result, mockStatus);
});

for (const route_count of [-1, 1.5, '12', null, Number.NaN, Infinity, Number.MAX_SAFE_INTEGER + 1]) {
  test(`rejects invalid route counter ${String(route_count)}`, () => {
    const input = { ...mockStatus, plugin: { ...mockStatus.plugin, route_count } };
    const parse = () => parseStatus(input);
    assert.throws(parse);
  });
}

for (const model_names of [null, {}, 'mock-model', [1], [null], [{ id: 'mock-model' }]]) {
  test(`rejects invalid model array ${JSON.stringify(model_names)}`, () => {
    const input = { ...mockStatus, routing: { ...mockStatus.routing, model_names } };
    const parse = () => parseStatus(input);
    assert.throws(parse);
  });
}

for (const upstream of [
  null, {}, { healthy: 'true' }, { healthy: true, version: 1 },
  { healthy: true, accounts: [] }, { healthy: true, accounts: { active: -1 } },
  { healthy: true, accounts: { total: '4' } }, { healthy: false, accounts: { limited: 1.2 } },
]) {
  test(`rejects invalid health observations ${JSON.stringify(upstream)}`, () => {
    const input = { ...mockStatus, upstream };
    const parse = () => parseStatus(input);
    assert.throws(parse);
  });
}

for (const input of [
  null, [], {}, { ...mockStatus, plugin: null },
  { ...mockStatus, plugin: { ...mockStatus.plugin, id: 'other' } },
  { ...mockStatus, routing: { ...mockStatus.routing, provider: 'other' } },
  { ...mockStatus, routing: { ...mockStatus.routing, mode: 'other' } },
  { ...mockStatus, routing: { ...mockStatus.routing, credential_source: 'other' } },
]) {
  test(`rejects missing or mismatched identity ${JSON.stringify(input)}`, () => {
    const parse = () => parseStatus(input);
    assert.throws(parse);
  });
}

test('keeps zero selections and an empty routing list when supplied', () => {
  const input = { ...mockStatus, plugin: { ...mockStatus.plugin, route_count: 0 },
    routing: { ...mockStatus.routing, model_names: [] } };
  const result = parseStatus(input);
  assert.deepEqual(result, input);
});
