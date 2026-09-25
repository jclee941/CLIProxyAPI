import assert from 'node:assert/strict';
import { test } from 'node:test';
import { hostResponse } from '../src/host-request.ts';

test('returns only a local Response when the host provides status data', async () => {
  const response = await hostResponse(async () => ({ status: 200, body: '{"healthy":true}', config: { key: 'MOCK_PRIVATE' } }), { path: '/v0/management/plugins/chatgpt2api/status', method: 'GET' });
  assert.deepEqual(await response.json(), { healthy: true });
  assert.deepEqual([...response.headers], [['content-type', 'application/json']]);
  assert(!Object.keys(response).includes('config'));
});

test('drops parent exception headers and message when a request rejects', async () => {
  const response = await hostResponse(async () => { throw { config: { headers: { Authorization: 'MOCK_PRIVATE' } }, message: 'MOCK_PRIVATE' }; }, { path: '/v0/management/plugins/chatgpt2api/status', method: 'GET' });
  assert.equal(response.status, 502);
  assert.deepEqual(await response.json(), { error: 'host_request_failed' });
});
