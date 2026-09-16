import ky from 'ky';
import { z } from 'zod';
import { HostAuthError, readHostAuth } from './auth';
import { hostResponse, readHostRequest } from './host-request';
import {
  accountSchema, accountsResponseSchema, errorResponseSchema, savedAccountSchema,
  type Account, type AccountId, type ErrorCode, type SaveAccount,
} from './contract';
import type { LoginAction, LoginComplete, LoginInput, LoginState } from './login-contract';

export class PluginApiError extends Error {
  readonly requiresHostLogin: boolean;

  constructor(readonly status: number, readonly code?: ErrorCode) {
    const authenticationFailure = status === 401 || status === 403;
    const expiredToken = authenticationFailure && (code === 'account_unavailable' || code === 'sidecar_request_failed');
    super(code === 'secret_store_unavailable'
      ? '서버의 세션 저장소를 사용할 수 없습니다. 관리자에게 저장소 키와 서버 저장소 설정을 확인해 달라고 요청하세요.'
      : status === 200
        ? code === 'account_unavailable' ? 'Google 세션이 만료되었거나 사용할 수 없습니다. Google 로그인을 다시 연결하세요.'
          : '계정의 사용 가능 상태를 확인하지 못했습니다. Google 세션과 서버 설정을 확인하세요.'
      : expiredToken
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

export async function request(path: 'accounts' | 'refresh' | 'recover' | `login/${'start' | 'complete' | LoginAction}` | { readonly modelsFor: AccountId },
  body?: SaveAccount | { readonly id: AccountId } | { readonly id: AccountId; readonly consent: true } | LoginInput | LoginComplete | { readonly state: LoginState },
  transport: ReturnType<typeof readHostRequest> | null = readHostRequest() ?? null): Promise<Response> {
  try {
    const url = typeof path === 'string' ? `/v0/management/plugins/gemini-web/${path}`
      : `/v0/management/auth-files/models?name=${encodeURIComponent(path.modelsFor)}`;
    const response = transport ? await hostResponse(transport, { path: url, method: body ? 'POST' : 'GET', ...(body ? { body: JSON.stringify(body) } : {}) }) : await http(url, {
      method: body ? 'POST' : 'GET',
      headers: { Authorization: `Bearer ${readHostAuth().managementKey}`, Accept: 'application/json' },
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
    if (error instanceof HostAuthError || error instanceof PluginApiError) throw error;
    if (error instanceof Error) throw new PluginApiError(0);
    throw error;
  }
}

export async function readJson(response: Response): Promise<unknown> {
  try {
    return await response.json();
  } catch (error) {
    if (error instanceof Error) throw new PluginApiError(0);
    throw error;
  }
}

export async function listAccounts(): Promise<readonly Account[]> {
  const transport = readHostRequest() ?? null;
  const parsed = accountsResponseSchema.safeParse(await readJson(await request('accounts', undefined, transport)));
  if (!parsed.success) throw new PluginApiError(0);
  return Promise.all(parsed.data.accounts.map((account) => confirmRegisteredModels(account, transport)));
}

const registeredModelsSchema = z.object({ models: z.array(z.object({ id: z.string().trim().min(1), display_name: z.string().nullish() })) });

async function confirmRegisteredModels(account: Account, transport: ReturnType<typeof readHostRequest> | null): Promise<Account> {
  if (!account.enabled || account.status !== 'ready' && account.status !== 'generating') return account;
  try {
    const parsed = registeredModelsSchema.safeParse(await readJson(await request({ modelsFor: account.id }, undefined, transport)));
    if (parsed.success && parsed.data.models.length) {
      return { ...account, models: parsed.data.models.map((model) => ({ id: model.id, name: model.display_name || model.id })) };
    }
  } catch (error) {
    if (error instanceof HostAuthError || error instanceof PluginApiError && error.requiresHostLogin) throw error;
    if (!(error instanceof Error)) throw error;
  }
  return { ...account, status: 'unknown', models: [] };
}

export async function saveAccount(payload: SaveAccount) {
  const parsed = savedAccountSchema.safeParse(await readJson(await request('accounts', payload)));
  if (!parsed.success) throw new PluginApiError(0);
  return parsed.data;
}

export async function refreshAccount(id: AccountId): Promise<Account> {
  const transport = readHostRequest() ?? null;
  const parsed = accountSchema.safeParse(await readJson(await request('refresh', { id }, transport)));
  if (!parsed.success || parsed.data.id !== id) throw new PluginApiError(0);
  if (parsed.data.status !== 'ready' && parsed.data.status !== 'generating') {
    const error = errorResponseSchema.safeParse({ error: parsed.data.error });
    throw new PluginApiError(200, parsed.data.status === 'expired' ? 'account_unavailable' : error.success ? error.data.error : undefined);
  }
  const confirmed = await confirmRegisteredModels(parsed.data, transport);
  if (confirmed.status !== 'ready' && confirmed.status !== 'generating') throw new PluginApiError(200);
  return confirmed;
}

export function accountErrorMessage(code: string): string {
  const parsed = errorResponseSchema.safeParse({ error: code });
  return new PluginApiError(200, parsed.success ? parsed.data.error : undefined).message;
}

const recoverySchema = z.object({ id: z.string(), state: z.string(), released: z.boolean(), error: z.string().optional() });

export async function recoverAccount(id: AccountId) {
  const parsed = recoverySchema.safeParse(await readJson(await request('recover', { id, consent: true })));
  if (!parsed.success) throw new PluginApiError(0);
  if (!parsed.data.released) throw new PluginApiError(200, undefined);
  return parsed.data;
}
