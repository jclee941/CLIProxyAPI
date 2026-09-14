import { z } from 'zod';
import { accountIdSchema, isWebToken } from './contract';

export const loginStateSchema = z.string().min(1).brand('LoginState');
const identityFields = {
  account_sha256: z.string().regex(/^[a-f0-9]{64}$/),
  auth_user: z.number().int().nonnegative(),
};
export const identitySchema = z.object(identityFields).readonly();
export const loginStartSchema = z.object({
  state: loginStateSchema,
  expires_at: z.number().int().positive(),
  status: z.literal('pending'),
  extension_id: z.string().regex(/^[a-p]{32}$/),
  manager_origin: z.url(),
  expected_identity: identitySchema.optional(),
}).readonly();
export const loginViewSchema = z.object({
  state: loginStateSchema,
  status: z.enum(['pending', 'processing', 'host_sync_pending', 'saved', 'ready', 'error', 'cancelled', 'expired']),
  account_id: accountIdSchema.optional(),
  error: z.string().optional(),
  models_ready: z.boolean(),
  expires_at: z.number().int().positive().optional(),
}).readonly();
export const sessionSchema = z.object({
  type: z.literal('session'), state: loginStateSchema,
  token: z.string().refine(isWebToken),
  ...identityFields,
});
export const companionMessageSchema = z.discriminatedUnion('type', [
  z.object({ type: z.literal('ready'), state: loginStateSchema }),
  z.object({ type: z.literal('error'), state: loginStateSchema, code: z.string() }),
  sessionSchema,
]);
export type LoginStart = z.infer<typeof loginStartSchema>;
export type LoginView = z.infer<typeof loginViewSchema>;
export type LoginState = z.infer<typeof loginStateSchema>;
export type Session = z.infer<typeof sessionSchema>;
export type LoginInput = { readonly label: string; readonly existing_id?: z.infer<typeof accountIdSchema>; readonly consent: true };
export type LoginComplete = Omit<Session, 'type'> & { readonly extension_id: string; readonly consent: true };
export type LoginAction = 'status' | 'cancel' | 'reconcile';

const problems = {
  location: '이 로그인은 승인된 HTTPS Manager 주소의 Gemini Web 페이지에서만 시작할 수 있습니다. 서버의 Manager origin 설정을 확인하세요.',
  configuration: '로그인 설정을 확인할 수 없습니다. 관리자에게 저장소 키, 정확한 Manager origin과 승인된 확장 프로그램 ID 설정을 확인해 달라고 요청하세요.',
  companion_unavailable: 'Chrome에서 관리자가 제공한 승인된 Gemini Web companion을 설치·활성화하세요. 확장 프로그램의 허용 Manager 주소와 서버의 확장 ID를 확인한 뒤 다시 연결하세요. 모바일에서는 지원되는 데스크톱 Chrome을 사용하세요.',
  companion_protocol: '확장 프로그램의 응답을 확인하지 못했습니다. 승인된 companion 버전과 선택한 Gemini 계정을 확인한 뒤 다시 연결하세요.',
  identity: '선택한 Google 계정이 업데이트 대상과 다릅니다. 대상 계정의 Gemini 탭을 선택해 새 로그인을 시작하세요.',
  expired: '로그인 요청이 만료되었습니다. 새 로그인을 시작하고 10분 안에 세션 선택과 승인을 완료하세요.',
  uncertain: '전송 결과를 확인하지 못했습니다. 토큰을 다시 보내지 않습니다. 상태 확인으로 저장 여부를 확인하세요. 저장된 세션은 취소해도 삭제되지 않습니다.',
} as const;
export class LoginError extends Error {
  constructor(readonly code: keyof typeof problems) { super(problems[code]); this.name = 'LoginError'; }
}

export function assertLoginLocation(location: URL, expectedOrigin = location.origin): void {
  const loopback = ['localhost', '127.0.0.1', '[::1]'].includes(location.hostname);
  if (location.origin !== expectedOrigin || location.protocol !== 'https:' && !(location.protocol === 'http:' && loopback)
    || location.pathname !== '/v0/resource/plugins/gemini-web/index' || location.search || location.hash) {
    throw new LoginError('location');
  }
}

export function loginOutcome(view: Pick<LoginView, 'status' | 'models_ready'>, disabled: boolean): 'ready' | 'saved_disabled' | 'pending' | 'failed' {
  switch (view.status) {
    case 'ready': return view.models_ready ? 'ready' : 'pending';
    case 'saved': return disabled ? 'saved_disabled' : 'pending';
    case 'pending': case 'processing': case 'host_sync_pending': return 'pending';
    case 'error': case 'cancelled': case 'expired': return 'failed';
    default: return assertNever(view.status);
  }
}

export function assertNever(_value: never): never { throw new LoginError('companion_protocol'); }
