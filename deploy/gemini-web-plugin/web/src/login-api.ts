import { PluginApiError, readJson, request, userMessage } from './api';
import { HostAuthError } from './auth';
import { assertLoginLocation, LoginError, loginStartSchema, loginViewSchema,
  type LoginAction, type LoginComplete, type LoginInput, type LoginState, type LoginView } from './login-contract';

export async function startLogin(input: LoginInput) {
  assertLoginLocation(new URL(window.location.href));
  const parsed = loginStartSchema.safeParse(await readJson(await request('login/start', input)));
  if (!parsed.success) throw new LoginError('configuration');
  assertLoginLocation(new URL(window.location.href), parsed.data.manager_origin);
  return parsed.data;
}

async function readView(response: Promise<Response>, state: LoginState): Promise<LoginView> {
  const parsed = loginViewSchema.safeParse(await readJson(await response));
  if (!parsed.success || parsed.data.state !== state) throw new LoginError('uncertain');
  return parsed.data;
}

export function completeLogin(payload: LoginComplete): Promise<LoginView> {
  return readView(request('login/complete', payload), payload.state).finally(() => { payload.token = ''; });
}

export function loginAction(action: LoginAction, state: LoginState): Promise<LoginView> {
  return readView(request(`login/${action}`, { state }), state);
}

export function loginErrorMessage(error: unknown, transferred: boolean): string {
  if (error instanceof LoginError) return error.message;
  if (error instanceof HostAuthError || error instanceof PluginApiError && error.requiresHostLogin) return userMessage(error);
  if (error instanceof PluginApiError && error.code === 'secret_store_unavailable') return error.message;
  if (error instanceof PluginApiError && error.status === 429) return '로그인 요청 한도에 도달했습니다. 진행 중인 요청을 취소하거나 만료 후 직접 다시 시작하세요.';
  return new LoginError(transferred ? 'uncertain' : 'configuration').message;
}
