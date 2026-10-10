import type { AccountId } from '../contract';
import type { Account } from './contract';

export function accountIsCurrent(account: Account): boolean {
  return account.status === 'ready' && !account.error && account.credits !== undefined;
}

export function reconcileAccounts(previous: readonly Account[], incoming: readonly Account[]): {
  readonly accounts: readonly Account[]; readonly stale: ReadonlySet<AccountId>;
} {
  const last = new Map(previous.map(account => [account.id, account]));
  const stale = new Set<AccountId>();
  const accounts = incoming.map(account => {
    if (accountIsCurrent(account)) return account;
    const known = last.get(account.id);
    if (known?.credits !== undefined) {
      stale.add(account.id);
      return { ...account, credits: known.credits,
        ...(known.observed_at === undefined ? {} : { observed_at: known.observed_at }),
        ...(known.tier === undefined ? {} : { tier: known.tier }) };
    }
    if (account.credits !== undefined) stale.add(account.id);
    return account;
  });
  return { accounts, stale };
}

export function accountMessage(account: Account): string {
  if (accountIsCurrent(account)) return '';
  if (account.status === 'busy' || account.error?.endsWith('busy')) {
    return '다른 요청이 계정을 사용 중입니다. 잠시 후 새로고침하세요.';
  }
  if (account.status === 'expired' || account.error?.includes('unauthenticated')) {
    return 'Google 연결을 확인할 수 없습니다. Gemini Web 관리 화면에서 계정을 다시 연결하세요.';
  }
  return '크레딧 조회를 완료하지 못했습니다. 계정 연결 상태를 확인한 뒤 새로고침하세요.';
}
