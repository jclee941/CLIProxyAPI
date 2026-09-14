import assert from 'node:assert/strict';
import { test } from 'node:test';
import { importRequest, toggleRequest, AccountActionError } from '../src/account-actions.ts';
import { parseSources, parseWebAccounts } from '../src/account-contract.ts';
import { disabledSource, source, webInventory, webID, sourceID, disabledSourceID } from './account-fixtures.ts';

const [enabled, disabled] = parseSources({ sources: [source, disabledSource] });
const account = parseWebAccounts(webInventory).accounts[0];
assert(enabled && disabled && account);

test('blocks import when consent or disabled-source override is absent', () => {
  assert.throws(() => importRequest(enabled, { consent: false, allowDisabled: false, capability: true }), AccountActionError);
  assert.throws(() => importRequest(disabled, { consent: true, allowDisabled: false, capability: true }), AccountActionError);
  assert.throws(() => importRequest(disabled, { consent: false, allowDisabled: true, capability: true }), AccountActionError);
});
test('copies only opaque source selector and explicit consent bits', () => {
  assert.deepEqual(importRequest(enabled, { consent: true, allowDisabled: false, capability: true }), { id: sourceID, consent: true });
  assert.deepEqual(importRequest(disabled, { consent: true, allowDisabled: true, capability: true }), { id: disabledSourceID, consent: true, allow_disabled_source: true });
  assert.equal(disabled.disabled, true);
});
for (const capability of [undefined, false]) {
  test(`blocks actions on unknown or unsupported capability ${capability}`, () => {
    assert.throws(() => importRequest(enabled, { consent: true, allowDisabled: false, capability }), AccountActionError);
    assert.throws(() => toggleRequest(account, { consent: true, enabled: true, capability }), AccountActionError);
  });
}
test('requires explicit consent for both Web-only enable and disable', () => {
  for (const enabled of [true, false]) {
    assert.throws(() => toggleRequest(account, { consent: false, enabled, capability: true }), AccountActionError);
    assert.deepEqual(toggleRequest(account, { consent: true, enabled, capability: true }), { id: webID, enabled, consent: true });
  }
  assert.equal(account.disabled, true);
});
test('does not interpret an unknown target as enabled', () => {
  assert.throws(() => toggleRequest({ ...account, disabled: false, status: 'unknown' }, { consent: true, enabled: true, capability: true }), AccountActionError);
});
