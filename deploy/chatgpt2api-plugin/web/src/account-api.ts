import ky from 'ky';
import { z } from 'zod';
import { HostAuthError, readHostAuth } from './host-auth.ts';
import { hostResponse, readHostRequest } from './host-request.ts';
import { AccountFormatError, parseImport, parseSources, parseWebAccounts, parseWebResult, type WebAccountID } from './account-contract.ts';
import { AccountActionError, capabilityMessage, type importRequest, type toggleRequest } from './account-actions.ts';

const codeMessages = {
  invalid_management_request: '계정 API 요청을 확인할 수 없습니다. 플러그인과 Manager 버전을 확인하세요.',
  invalid_account_request: '계정 API 요청을 확인할 수 없습니다. 플러그인과 Manager 버전을 확인하세요.',
  invalid_import_request_require_consent: '선택한 소스와 가져오기 동의를 다시 확인하세요.',
  invalid_web_account_id: '선택한 Web 스냅샷을 확인할 수 없습니다. 목록을 다시 조회하세요.',
  invalid_web_enabled_request_require_consent: 'Web 대상과 활성·비활성 변경 동의를 다시 확인하세요.',
  source_not_found: '선택한 Codex 소스가 없습니다. 목록을 다시 조회하고 직접 선택하세요.',
  not_found: '계정 API가 없습니다. 서버 플러그인 버전을 확인하세요.',
  method_not_allowed: '계정 작업을 지원하지 않습니다. Manager와 서버 플러그인 버전을 확인하세요.',
  source_metadata_unavailable: 'Codex 물리 소스와 런타임 연결을 확인할 수 없습니다. 관리자에게 소스 상태를 확인하세요.',
  source_credential_invalid: 'Codex access 스냅샷을 사용할 수 없습니다. 소스 인증은 기존 Codex 관리 경로에서 확인하세요.',
  disabled_source_requires_override: '비활성 Codex 소스입니다. 위험 확인과 별도 허용이 필요하며 Codex 상태는 바뀌지 않습니다.',
  vendor_disabled_import_unsupported: capabilityMessage,
  vendor_disabled_refresh_unsupported: capabilityMessage,
  vendor_web_enabled_unsupported: capabilityMessage,
  web_account_status_unknown: 'Web 대상 상태가 미확인입니다. 목록을 다시 조회하고 관리자에게 확인하세요.',
  web_account_stale_reload_needed: '선택한 Web 스냅샷이 없어졌거나 교체되었습니다. 목록을 다시 조회하고 직접 선택하세요. 다른 계정으로 대체하지 않습니다.',
  web_api_unavailable: '기존 Web 서비스에 연결하지 못했습니다. 서비스 연결을 확인하고 목록을 다시 조회하세요.',
  web_api_http_error: '기존 Web 서비스 요청이 거절되었습니다. 서버 측 API 인증과 서비스 상태를 확인하세요.',
  web_api_redirect_blocked: 'Web API 리디렉션을 차단했습니다. 관리자가 서버 목적지 설정을 확인해야 합니다.',
  web_api_invalid_response: 'Web 서비스 응답 형식이 올바르지 않습니다. vendor와 플러그인 버전을 확인하세요.',
  web_api_response_too_large: 'Web 서비스 응답이 허용 범위를 초과했습니다. 관리자에게 확인하세요.',
  vendor_refresh_failed: 'Web 사용량 갱신이 실패했습니다. 이전 관측값은 최신이 아닙니다. 상태를 확인한 뒤 직접 다시 갱신하세요.',
  vendor_disabled_contract_violated: 'vendor의 비활성 보존 계약 위반입니다. 결과 상태를 추정하지 않습니다. 관리자 확인 후 목록을 다시 조회하세요.',
  vendor_web_enabled_contract_violated: 'vendor의 Web 상태 변경 계약 위반입니다. 관리자 확인 후 목록을 다시 조회하세요.',
  host_callback_required: '호스트의 요청 범위가 없습니다. scoped auth callback을 지원하는 CPA 호스트 구성을 확인하세요.',
  host_auth_unavailable: '호스트에서 안전한 소스 목록을 제공하지 못했습니다. CPA auth callback 지원과 상태를 확인하세요.',
  web_api_unconfigured_set_CHATGPT2API_AUTH_KEY: '서버의 CHATGPT2API_AUTH_KEY가 구성되지 않았습니다. 관리자가 호스트 시작 환경에 주입해야 합니다. 브라우저에 키를 입력하지 마세요.',
  request_cancelled: '작업 결과를 확인하지 못했습니다. 이미 접수된 작업은 취소되지 않을 수 있습니다. 목록을 다시 조회하세요.',
  host_request_encoding_failed: '호스트 계정 요청을 구성하지 못했습니다. 관리자에게 플러그인 연결을 확인하세요.',
  request_encoding_failed: '계정 요청을 구성하지 못했습니다. 관리자에게 플러그인 연결을 확인하세요.',
  response_encoding_failed: '계정 응답을 확인하지 못했습니다. 목록을 다시 조회하세요.',
  host_request_failed: 'Manager의 계정 작업 권한 또는 연결을 확인하세요. ChatGPT POST 허용 패치 적용 후 Manager에서 페이지를 다시 여세요. 결과가 불확실하면 목록을 먼저 조회하세요.',
} as const;
const errorSchema = z.object({ error: z.string() });
type ErrorCode = keyof typeof codeMessages;
function knownCode(code: string): code is ErrorCode { return Object.hasOwn(codeMessages, code); }

export class AccountRequestError extends Error {
  readonly code: ErrorCode | undefined;
  readonly requiresHostLogin: boolean;
  constructor(status: number, code?: ErrorCode) {
    const login = status === 401 && (code === undefined || code === 'host_request_failed');
    super(login ? 'Manager 인증이 만료되었습니다. 부모 Manager에서 다시 로그인하세요.'
      : code ? codeMessages[code] : '계정 작업 결과를 확인하지 못했습니다. 연결과 서버 버전을 확인한 뒤 목록을 다시 조회하세요. 자동 재시도하지 않습니다.');
    this.name = 'AccountRequestError'; this.code = code; this.requiresHostLogin = login;
  }
}
const http = ky.create({ retry: 0, timeout: false, redirect: 'error', mode: 'same-origin', credentials: 'same-origin',
  cache: 'no-store', throwHttpErrors: false, referrerPolicy: 'no-referrer' });
type AccountRequest = { readonly path: 'codex-sources' | 'webaccounts' | 'import-codex' | 'refresh-web' | 'set-web-enabled'; readonly body?: object };
export async function accountRequest(input: AccountRequest, signal: AbortSignal): Promise<unknown> {
  signal.throwIfAborted();
  try {
    const path = `/v0/management/plugins/chatgpt2api/${input.path}`;
    const transport = readHostRequest();
    const method = input.body ? 'POST' : 'GET';
    const response = transport
      ? await hostResponse(transport, { path, method, ...(input.body ? { body: JSON.stringify(input.body) } : {}) })
      : await http(path, { method, signal, headers: { Authorization: `Bearer ${readHostAuth().managementKey}`, Accept: 'application/json' },
        ...(input.body ? { json: input.body } : {}) });
    signal.throwIfAborted();
    const payload: unknown = await response.json();
    const error = errorSchema.safeParse(payload);
    if (error.success) throw new AccountRequestError(response.status, knownCode(error.data.error) ? error.data.error : undefined);
    if (!response.ok) throw new AccountRequestError(response.status);
    return payload;
  } catch (error) {
    if (signal.aborted) throw error;
    if (error instanceof AccountRequestError || error instanceof HostAuthError) throw error;
    if (error instanceof SyntaxError) throw new AccountFormatError();
    if (error instanceof Error) throw new AccountRequestError(0);
    throw error;
  }
}
export async function loadSources(signal: AbortSignal) { return parseSources(await accountRequest({ path: 'codex-sources' }, signal)); }
export async function loadWebAccounts(signal: AbortSignal) { return parseWebAccounts(await accountRequest({ path: 'webaccounts' }, signal)); }
export async function importCodex(body: ReturnType<typeof importRequest>, signal: AbortSignal) {
  return parseImport(await accountRequest({ path: 'import-codex', body }, signal));
}
export async function refreshWeb(id: WebAccountID, signal: AbortSignal) {
  return parseWebResult(await accountRequest({ path: 'refresh-web', body: { id } }, signal), id);
}
export async function setWebEnabled(body: ReturnType<typeof toggleRequest>, signal: AbortSignal) {
  const result = parseWebResult(await accountRequest({ path: 'set-web-enabled', body }, signal), body.id);
  if (result.account.disabled === body.enabled || result.account.status === 'unknown') throw new AccountFormatError();
  return result;
}
export function accountMessage(error: unknown): string {
  if (error instanceof AccountRequestError || error instanceof AccountFormatError || error instanceof AccountActionError || error instanceof HostAuthError) return error.message;
  return '작업 결과를 확인하지 못했습니다. 이미 접수된 작업은 취소되지 않을 수 있습니다. 목록을 다시 조회하고 상태를 확인하세요.';
}
