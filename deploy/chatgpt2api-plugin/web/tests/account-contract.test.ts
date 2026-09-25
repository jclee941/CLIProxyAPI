import assert from 'node:assert/strict';
import { test } from 'node:test';
import { parseSources, parseWebAccounts, parseImport, parseWebResult, AccountFormatError } from '../src/account-contract.ts';
import { source, webAccount, webInventory, webID, secondWebID, observedAt } from './account-fixtures.ts';

test('projects only safe source and account fields when credential-rich extras arrive', () => {
  const sources = parseSources({ sources: [{ ...source, access_token: 'SYNTHETIC_PRIVATE', filename: 'private' }], auth: {} });
  const inventory = parseWebAccounts({ ...webInventory, accounts: [{ ...webAccount, access_token: 'SYNTHETIC_PRIVATE', refresh_token: 'private', Quota: { weekly: 999 } }] });
  assert.deepEqual(sources, [source]);
  assert.deepEqual(inventory, webInventory);
});
for (const value of [-1, '9', 1.25, Number.MAX_SAFE_INTEGER + 1, Infinity]) {
  test(`rejects invalid image counter ${value} instead of guessing units`, () => {
    assert.throws(() => parseWebAccounts({ ...webInventory, accounts: [{ ...webAccount, tracked_image_remaining: value }] }), AccountFormatError);
  });
}
test('keeps absent metrics unknown and does not fabricate capability on an older server', () => {
  const { tracked_image_remaining, observed_image_remaining, reset_after_seconds, ...account } = webAccount;
  void tracked_image_remaining; void observed_image_remaining; void reset_after_seconds;
  const { capabilities, ...inventory } = webInventory;
  void capabilities;
  const parsed = parseWebAccounts({ ...inventory, accounts: [account] });
  assert.equal(parsed.capabilities, undefined);
  assert.equal(parsed.accounts[0]?.tracked_image_remaining, null);
  assert.equal(parsed.accounts[0]?.observed_image_remaining, null);
  assert.equal(parsed.accounts[0]?.reset_after_seconds, null);
});
test('distinguishes unsupported boolean capability from malformed wire capability', () => {
  assert.equal(parseWebAccounts({ ...webInventory, capabilities: { preserve_disabled_accounts: false } }).capabilities?.preserve_disabled_accounts, false);
  assert.throws(() => parseWebAccounts({ ...webInventory, capabilities: { preserve_disabled_accounts: 1 } }), AccountFormatError);
});
test('rejects credential IDs, wrong providers, duplicate selectors and contradictory disabled state', () => {
  assert.throws(() => parseSources({ sources: [{ ...source, id: 'email@example.invalid' }] }), AccountFormatError);
  assert.throws(() => parseSources({ sources: [{ ...source, provider: 'claude' }] }), AccountFormatError);
  assert.throws(() => parseSources({ sources: [source, source] }), AccountFormatError);
  assert.throws(() => parseWebAccounts({ ...webInventory, source: 'Codex' }), AccountFormatError);
  assert.throws(() => parseWebAccounts({ ...webInventory, accounts: [{ ...webAccount, disabled: false }] }), AccountFormatError);
});
test('accepts imported disabled only when the target is disabled, while duplicates preserve current state', () => {
  assert.equal(parseImport({ status: 'imported_disabled', account: webAccount }).status, 'imported_disabled');
  const existing = { ...webAccount, disabled: false, status: 'normal' };
  assert.deepEqual(parseImport({ status: 'already_present', account: existing }).account, existing);
  assert.throws(() => parseImport({ status: 'imported_disabled', account: existing }), AccountFormatError);
});
test('preserves HTTP200 refresh failure and rejects a different snapshot identity', () => {
  const payload = { account: { ...webAccount, refresh_error: 'vendor_refresh_failed' }, observed_at: observedAt, source: 'ChatGPTWeb' };
  assert.equal(parseWebResult(payload, webID).account.refresh_error, 'vendor_refresh_failed');
  assert.throws(() => parseWebResult({ ...payload, account: { ...webAccount, id: secondWebID } }, webID), AccountFormatError);
});
