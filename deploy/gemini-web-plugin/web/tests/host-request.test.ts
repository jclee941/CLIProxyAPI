import { expect, test } from 'bun:test';
import { hostResponse } from '../src/host-request';

test('returns a local Response with only the response envelope when host succeeds', async () => {
  const response = await hostResponse(async () => ({ status: 200, body: '{"accounts":[]}', config: { key: 'MOCK_PRIVATE' } }), { path: '/v0/management/plugins/gemini-web/accounts', method: 'GET' });
  expect(await response.json()).toEqual({ accounts: [] });
  expect([...response.headers]).toEqual([['content-type', 'application/json']]);
  expect(Object.keys(response)).not.toContain('config');
});

test('returns a fixed safe error when an Axios-shaped parent exception is thrown', async () => {
  const response = await hostResponse(async () => { throw { message: 'MOCK_PRIVATE', config: { headers: { Authorization: 'MOCK_PRIVATE' } } }; }, { path: '/v0/management/plugins/gemini-web/accounts', method: 'GET' });
  expect(response.status).toBe(502);
  expect(await response.json()).toEqual({ error: 'host_request_failed' });
});

test.each([null, { status: 0, body: 'MOCK_PRIVATE' }, { status: 200, body: {} }])('rejects a malformed parent response %j', async (payload) => {
  const response = await hostResponse(async () => payload, { path: '/v0/management/plugins/gemini-web/accounts', method: 'GET' });
  expect(response.status).toBe(502);
  expect(await response.text()).not.toContain('MOCK_PRIVATE');
});
