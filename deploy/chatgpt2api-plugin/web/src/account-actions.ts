import type { CodexSource, WebAccount } from './account-contract.ts';

export const capabilityMessage = '안전한 Web 계정 작업을 지원하는 vendor 계약이 확인되지 않았습니다. 관리자에게 chatgpt2api 0005 패치와 플러그인 버전을 확인하도록 요청한 뒤 목록을 다시 조회하세요.';
export class AccountActionError extends Error {
  constructor(message: string) { super(message); this.name = 'AccountActionError'; }
}
type Consent = { readonly consent: boolean; readonly capability: boolean | undefined };
export function importRequest(source: CodexSource, approval: Consent & { readonly allowDisabled: boolean }) {
  if (approval.capability !== true) throw new AccountActionError(capabilityMessage);
  if (!approval.consent) throw new AccountActionError('선택한 access 스냅샷의 Web 대상 저장에 명시적으로 동의해야 합니다.');
  if (source.disabled && !approval.allowDisabled) throw new AccountActionError('비활성 Codex 소스의 위험을 확인하고 별도로 허용해야 합니다. Codex 상태는 바뀌지 않습니다.');
  return { id: source.id, consent: true, ...(source.disabled ? { allow_disabled_source: true as const } : {}) } as const;
}
export function toggleRequest(account: WebAccount, approval: Consent & { readonly enabled: boolean }) {
  if (approval.capability !== true) throw new AccountActionError(capabilityMessage);
  if (!approval.consent) throw new AccountActionError('선택한 Web 대상의 상태 변경에 명시적으로 동의해야 합니다.');
  if (account.status === 'unknown') throw new AccountActionError('Web 대상 상태가 미확인입니다. 목록을 다시 조회하고 관리자에게 상태를 확인하세요.');
  return { id: account.id, enabled: approval.enabled, consent: true } as const;
}
