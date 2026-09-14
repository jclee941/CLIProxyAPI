import { z } from 'zod';
import { accountIdSchema } from '../src/contract';
import { MOCK_TOKEN } from './fixtures';

const startSchema = z.object({ label: z.string().min(1), consent: z.literal(true), existing_id: accountIdSchema.optional() }).strict();
const completeSchema = z.object({ state: z.string(), token: z.string(), account_sha256: z.string(),
  auth_user: z.number().int(), extension_id: z.string(), consent: z.literal(true) }).strict();
const stateSchema = z.object({ state: z.string() }).strict();
type Status = 'pending' | 'processing' | 'host_sync_pending' | 'saved' | 'ready' | 'error' | 'cancelled' | 'expired';
type MockLoginState = {
  nonce: string; extensionId: string; originOverride: string;
  expectedIdentity: { readonly account_sha256: string; readonly auth_user: number } | undefined;
  startStatus: number; actionStatus: number; completeStatus: number; rawError: string;
  finalStatus: Status; status: Status; modelsReady: boolean; holdComplete: boolean; ttl: number; wrongReplyState: boolean;
  starts: number; completes: number; statuses: number; cancels: number; reconciles: number; validTransfers: number;
  input: z.infer<typeof startSchema> | undefined; originsValid: boolean; methodsValid: boolean;
};

export function createMockLogin() {
  const state: MockLoginState = {
    nonce: 'MOCK_LOGIN_STATE_PRIVATE', extensionId: 'a'.repeat(32), originOverride: '',
    expectedIdentity: undefined,
    startStatus: 200, actionStatus: 200, completeStatus: 200, rawError: 'MOCK_PRIVATE_ERROR',
    finalStatus: 'ready', status: 'pending', modelsReady: true,
    holdComplete: false, ttl: 600, wrongReplyState: false,
    starts: 0, completes: 0, statuses: 0, cancels: 0, reconciles: 0, validTransfers: 0,
    input: undefined,
    originsValid: true, methodsValid: true,
  };
  const waiters: (() => void)[] = [];
  const view = () => ({ state: state.wrongReplyState ? 'MOCK_WRONG_REPLY' : state.nonce, status: state.status,
    account_id: state.input?.existing_id ?? 'mock-login-added', models_ready: state.status === 'ready' && state.modelsReady,
    ...(state.status === 'error' ? { error: state.rawError } : {}) });
  return {
    state,
    release: () => waiters.splice(0).forEach((resolve) => resolve()),
    async handle(request: Request): Promise<Response> {
      const url = new URL(request.url);
      state.originsValid &&= request.headers.get('Origin') === url.origin;
      state.methodsValid &&= request.method === 'POST' && url.search === '';
      const input: unknown = await request.json();
      if (url.pathname.endsWith('/start')) {
        state.starts++;
        state.input = startSchema.parse(input);
        if (state.startStatus !== 200) return Response.json({ error: state.rawError }, { status: state.startStatus });
        state.status = 'pending';
        return Response.json({ state: state.nonce, expires_at: Math.floor(Date.now() / 1000) + state.ttl,
          status: 'pending', extension_id: state.extensionId, manager_origin: state.originOverride || url.origin,
          ...(state.expectedIdentity ? { expected_identity: state.expectedIdentity } : {}) });
      }
      if (url.pathname.endsWith('/complete')) {
        const payload = completeSchema.parse(input);
        state.completes++;
        if (payload.token === MOCK_TOKEN && payload.state === state.nonce && payload.extension_id === state.extensionId
          && payload.account_sha256 === 'b'.repeat(64) && payload.auth_user === 1) state.validTransfers++;
        payload.token = '';
        state.status = 'processing';
        if (state.holdComplete) await new Promise<void>((resolve) => waiters.push(resolve));
        state.status = state.finalStatus;
        if (state.completeStatus !== 200) return Response.json({ error: state.rawError }, { status: state.completeStatus });
        return Response.json(view());
      }
      if (stateSchema.parse(input).state !== state.nonce) return Response.json({ error: 'MOCK invalid state' }, { status: 400 });
      if (url.pathname.endsWith('/status')) state.statuses++;
      if (url.pathname.endsWith('/cancel')) {
        state.cancels++;
        if (state.status === 'pending') state.status = 'cancelled';
      }
      if (url.pathname.endsWith('/reconcile')) {
        state.reconciles++;
        state.status = 'ready';
      }
      if (state.actionStatus !== 200) return Response.json({ error: state.rawError }, { status: state.actionStatus });
      return Response.json(view());
    },
  };
}
