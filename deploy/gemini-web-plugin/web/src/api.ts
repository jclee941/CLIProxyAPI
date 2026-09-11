import ky from 'ky';
import { HostAuthError, readHostAuth } from './auth';
import {
  accountSchema, accountsResponseSchema, errorResponseSchema, savedAccountSchema,
  type Account, type AccountId, type ErrorCode, type SaveAccount,
} from './contract';

export class PluginApiError extends Error {
  readonly requiresHostLogin: boolean;

  constructor(readonly status: number, readonly code?: ErrorCode) {
    const authenticationFailure = status === 401 || status === 403;
    const expiredToken = authenticationFailure && (code === 'account_unavailable' || code === 'sidecar_request_failed');
    super(expiredToken
      ? '웹 토큰이 만료되었거나 사용할 수 없습니다. 새 웹 토큰을 등록해 주세요.'
      : code === 'disabled_account_update_requires_host_enable'
        ? '비활성 계정입니다. Manager에서 계정을 활성화한 뒤 토큰을 업데이트해 주세요.'
        : authenticationFailure
      ? 'Manager 인증이 만료되었거나 권한이 없습니다. Manager에서 다시 로그인한 뒤 연결을 확인하세요.'
      : status === 400 || status === 422
        ? '등록 정보를 확인하세요. 유효한 웹 토큰을 다시 붙여넣어 주세요.'
        : status === 429
          ? '조회 요청이 많습니다. 잠시 후 직접 다시 확인해 주세요.'
          : status === 404
            ? 'Gemini Web 관리 API를 찾을 수 없습니다. Manager의 플러그인 연결 설정을 확인하세요.'
            : status === 0
              ? '응답 형식이나 네트워크 연결을 확인할 수 없습니다. 연결을 확인한 뒤 다시 시도하세요.'
              : `요청을 완료하지 못했습니다 (HTTP ${status}). 잠시 후 다시 시도하세요.`);
    this.name = 'PluginApiError';
    this.requiresHostLogin = authenticationFailure && !expiredToken;
  }
}

export function userMessage(error: unknown): string {
  if (error instanceof HostAuthError || error instanceof PluginApiError) return error.message;
  return '작업을 완료하지 못했습니다. 연결을 확인한 뒤 다시 시도하세요.';
}

const http = ky.create({
  retry: 0,
  timeout: false,
  redirect: 'error',
  mode: 'same-origin',
  credentials: 'same-origin',
  cache: 'no-store',
  throwHttpErrors: false,
  referrerPolicy: 'no-referrer',
});

async function request(path: 'accounts' | 'refresh', body?: SaveAccount | { readonly id: AccountId }): Promise<Response> {
  const auth = readHostAuth();
  try {
    const response = await http(`/v0/management/plugins/gemini-web/${path}`, {
      method: body ? 'POST' : 'GET',
      headers: { Authorization: `Bearer ${auth.managementKey}`, Accept: 'application/json' },
      ...(body ? { json: body } : {}),
    });
    if (!response.ok) {
      let code: ErrorCode | undefined;
      try {
        const parsed = errorResponseSchema.safeParse(await response.json());
        if (parsed.success) code = parsed.data.error;
      } catch (error) {
        if (!(error instanceof SyntaxError)) throw error;
      }
      throw new PluginApiError(response.status, code);
    }
    return response;
  } catch (error) {
    if (error instanceof PluginApiError) throw error;
    if (error instanceof Error) throw new PluginApiError(0);
    throw error;
  }
}

async function readJson(response: Response): Promise<unknown> {
  try {
    return await response.json();
  } catch (error) {
    if (error instanceof Error) throw new PluginApiError(0);
    throw error;
  }
}

export async function listAccounts(): Promise<readonly Account[]> {
  const parsed = accountsResponseSchema.safeParse(await readJson(await request('accounts')));
  if (!parsed.success) throw new PluginApiError(0);
  return parsed.data.accounts;
}

export async function saveAccount(payload: SaveAccount) {
  const parsed = savedAccountSchema.safeParse(await readJson(await request('accounts', payload)));
  if (!parsed.success) throw new PluginApiError(0);
  return parsed.data;
}

export async function refreshAccount(id: AccountId): Promise<Account> {
  const parsed = accountSchema.safeParse(await readJson(await request('refresh', { id })));
  if (!parsed.success || parsed.data.id !== id) throw new PluginApiError(0);
  return parsed.data;
}
