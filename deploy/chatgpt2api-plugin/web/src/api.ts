import ky from 'ky';
import { parseStatus, StatusFormatError, type PluginStatus } from './contract.ts';
import { HostAuthError, readHostAuth } from './host-auth.ts';

export class StatusRequestError extends Error {
  readonly requiresHostLogin: boolean;

  constructor(status: number) {
    const requiresHostLogin = status === 401 || status === 403;
    super(requiresHostLogin
      ? 'Manager 인증이 만료되었거나 권한이 없습니다. 부모 Manager에서 다시 로그인한 뒤 새로고침하세요.'
      : status === 0
        ? '상태를 가져오지 못했습니다. 네트워크와 플러그인 연결을 확인한 뒤 새로고침하세요.'
        : `상태를 가져오지 못했습니다 (HTTP ${status}). 플러그인 연결을 확인한 뒤 새로고침하세요.`);
    this.name = 'StatusRequestError';
    this.requiresHostLogin = requiresHostLogin;
  }
}

const http = ky.create({
  retry: 0, timeout: false, redirect: 'error', mode: 'same-origin',
  credentials: 'same-origin', cache: 'no-store', throwHttpErrors: false,
  referrerPolicy: 'no-referrer',
});

export async function loadStatus(signal: AbortSignal): Promise<PluginStatus> {
  const auth = readHostAuth();
  try {
    const response = await http.get('/v0/management/plugins/chatgpt2api/status', {
      signal, headers: { Authorization: `Bearer ${auth.managementKey}`, Accept: 'application/json' },
    });
    if (!response.ok) throw new StatusRequestError(response.status);
    const payload: unknown = await response.json();
    return parseStatus(payload);
  } catch (error) {
    if (signal.aborted) throw error;
    if (error instanceof StatusRequestError || error instanceof StatusFormatError) throw error;
    if (error instanceof SyntaxError) throw new StatusFormatError();
    if (error instanceof Error) throw new StatusRequestError(0);
    throw error;
  }
}

export function statusFailure(error: unknown) {
  if (error instanceof HostAuthError || error instanceof StatusRequestError && error.requiresHostLogin) {
    return { kind: 'auth-error', label: 'Manager 로그인 필요', message: error.message, tone: 'warning' } as const;
  }
  if (error instanceof StatusFormatError) {
    return { kind: 'bad-response', label: '응답 확인 필요', message: error.message, tone: 'danger' } as const;
  }
  return { kind: 'request-error', label: '조회 실패', tone: 'danger',
    message: error instanceof StatusRequestError ? error.message : '상태 확인 중 오류가 발생했습니다. 새로고침으로 다시 확인하세요.' } as const;
}
