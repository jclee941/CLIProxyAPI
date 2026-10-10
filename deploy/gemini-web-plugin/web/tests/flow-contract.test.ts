import { expect, test } from 'bun:test';
import { accountSchema, accountsResponseSchema } from '../src/flow/contract';
import { accountIsCurrent, reconcileAccounts } from '../src/flow/state';

test('Flow balances distinguish zero, unknown, and invalid numbers', () => {
  const zero = accountSchema.parse({ id: 'a', status: 'ready', credits: 0 });
  expect(accountIsCurrent(zero)).toBe(true);
  const unknown = accountSchema.parse({ id: 'a' });
  expect(unknown.credits).toBeUndefined();
  expect(unknown.status).toBe('unknown');
  for (const credits of [-1, 1.5, '5', null]) {
    expect(accountSchema.safeParse({ id: 'a', credits }).success).toBe(false);
  }
});

test('Flow catalogue preserves the native model wire fields', () => {
  const response = accountsResponseSchema.parse({
    provider: 'flow2api', accounts: [],
    models: [{ ID: 'flow-omni-1.1-flash', DisplayName: 'Omni' }],
  });
  expect(response.models[0]?.ID).toBe('flow-omni-1.1-flash');
  expect(accountsResponseSchema.safeParse({ provider: 'gemini-web', accounts: [] }).success).toBe(false);
});

test('failed lookups preserve stale observations until fresh success', () => {
  const previous = accountSchema.parse({ id: 'a', status: 'ready', credits: 17, tier: 3, observed_at: 10 });
  const busy = accountSchema.parse({ id: 'a', status: 'busy', error: 'flow_session_exchange_busy' });
  const failed = reconcileAccounts([previous], [busy]);
  expect(failed.accounts[0]?.credits).toBe(17);
  expect(failed.accounts[0]?.observed_at).toBe(10);
  expect(failed.stale.has(previous.id)).toBe(true);
  expect(accountIsCurrent(failed.accounts[0]!)).toBe(false);
  const zero = accountSchema.parse({ id: 'a', status: 'ready', credits: 0, observed_at: 20 });
  const recovered = reconcileAccounts(failed.accounts, [zero]);
  expect(recovered.accounts[0]?.credits).toBe(0);
  expect(recovered.stale.size).toBe(0);
  expect(reconcileAccounts(recovered.accounts, []).accounts).toEqual([]);
});
