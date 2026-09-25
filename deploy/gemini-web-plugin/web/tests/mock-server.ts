import { z } from 'zod';
import { accountIdSchema, accountSchema, type Account } from '../src/contract';
import { MOCK_KEY, MOCK_TOKEN, mockAccounts, observedAt } from './fixtures';
import { createMockLogin } from './mock-login';

const registration = z.object({ label: z.string().min(1), token: z.string(), existing_id: accountIdSchema.optional() });

export function startMockServer() {
  const login = createMockLogin();
  const submissions: { readonly label: string; readonly existingId: string | undefined; readonly validToken: boolean }[] = [];
  const state = {
    accounts: mockAccounts(),
    listStatus: 200,
    modelsStatus: 200,
    modelsEmpty: false,
    modelsCount: 0,
    saveStatus: 200,
    saveError: 'MOCK private response must not be rendered',
    holdSave: false,
    holdRefresh: false,
    holdList: false,
    failRefreshId: '',
    activeRefreshes: 0,
    peakRefreshes: 0,
    refreshCount: 0,
    listCount: 0,
    authorizedCount: 0,
    submissions,
  };
  const refreshWaiters: (() => void)[] = [];
  const listWaiters: (() => void)[] = [];
  const saveWaiters: (() => void)[] = [];
  const server = Bun.serve({
    hostname: '127.0.0.1',
    port: 0,
    idleTimeout: 0,
    async fetch(request) {
      const url = new URL(request.url);
      if (url.pathname === '/mock') return new Response(Bun.file(`${import.meta.dir}/mock-host.html`));
      if (url.pathname === '/mock-tokens.css') return new Response(Bun.file(`${import.meta.dir}/../src/tokens.css`), { headers: { 'Content-Type': 'text/css' } });
      if (url.pathname === '/v0/resource/plugins/gemini-web/index') return new Response(Bun.file(`${import.meta.dir}/../index.html`));
      if (!url.pathname.startsWith('/v0/management/plugins/gemini-web/') && url.pathname !== '/v0/management/auth-files/models') return new Response('MOCK not found', { status: 404 });
      if (request.headers.get('Authorization') !== `Bearer ${MOCK_KEY}`) return Response.json({ error: 'MOCK unauthorized' }, { status: 401 });
      state.authorizedCount++;
      if (url.pathname === '/v0/management/auth-files/models') {
        state.modelsCount++;
        return Response.json({ models: state.modelsEmpty ? [] : state.accounts.find((account) => account.id === url.searchParams.get('name'))?.models ?? [] }, { status: state.modelsStatus });
      }
      if (url.pathname.startsWith('/v0/management/plugins/gemini-web/login/')) {
        const response = await login.handle(request);
        if (login.state.input && ['saved', 'ready'].includes(login.state.status)) {
          const id = login.state.input.existing_id ?? 'mock-login-added';
          const previous = state.accounts.find((account) => account.id === id);
          const account = accountSchema.parse({ id, label: login.state.input.label, enabled: previous?.enabled ?? true,
            status: login.state.status === 'ready' ? 'ready' : 'unknown', usage: previous?.usage ?? null,
            models: login.state.status === 'ready' ? [{ id: 'gemini-omni-1.1-flash', name: 'MOCK returned model' }] : [], observed_at: observedAt + 180 });
          state.accounts = previous ? state.accounts.map((entry) => entry.id === id ? account : entry) : [...state.accounts, account];
        }
        return response;
      }
      if (url.pathname.endsWith('/accounts') && request.method === 'GET') {
        state.listCount++;
        if (state.holdList) await new Promise<void>((resolve) => listWaiters.push(resolve));
        return state.listStatus === 200 ? Response.json({ provider: 'gemini-web', accounts: state.accounts })
          : Response.json({ error: 'MOCK raw error must not be reflected' }, { status: state.listStatus });
      }
      if (url.pathname.endsWith('/accounts') && request.method === 'POST') {
        const body: unknown = await request.json();
        const input = registration.parse(body);
        state.submissions.push({ label: input.label, existingId: input.existing_id, validToken: input.token === MOCK_TOKEN });
        if (state.holdSave) await new Promise<void>((resolve) => saveWaiters.push(resolve));
        if (state.saveStatus !== 200) return Response.json({ error: state.saveError }, { status: state.saveStatus });
        const id = input.existing_id ?? `mock-added-${state.submissions.length}`;
        const account: Account = accountSchema.parse({ id, label: input.label, enabled: true, status: 'ready',
          models: [{ id: 'gemini-omni-1.1-flash', name: 'MOCK returned model' }], usage: null, observed_at: observedAt + 60 });
        state.accounts = input.existing_id
          ? state.accounts.map((existing) => existing.id === id ? account : existing)
          : [...state.accounts, account];
        return Response.json({ id, status: 'ready' });
      }
      if (url.pathname.endsWith('/refresh') && request.method === 'POST') {
        const input = z.object({ id: accountIdSchema }).parse(await request.json());
        state.refreshCount++;
        state.activeRefreshes++;
        state.peakRefreshes = Math.max(state.peakRefreshes, state.activeRefreshes);
        try {
          if (state.holdRefresh) await new Promise<void>((resolve) => refreshWaiters.push(resolve));
          if (input.id === state.failRefreshId) return Response.json({ error: 'MOCK upstream failure' }, { status: 502 });
          state.accounts = state.accounts.map((account) => account.id === input.id
            ? { ...account, observed_at: observedAt + 120 } : account);
          return Response.json(state.accounts.find((account) => account.id === input.id));
        } finally {
          state.activeRefreshes--;
        }
      }
      return new Response('MOCK method not allowed', { status: 405 });
    },
  });
  return {
    server, state, login,
    url: new URL('/mock', server.url).href,
    releaseRefresh: () => { refreshWaiters.splice(0).forEach((resolve) => resolve()); },
    releaseList: () => { listWaiters.splice(0).forEach((resolve) => resolve()); },
    releaseSave: () => { saveWaiters.splice(0).forEach((resolve) => resolve()); },
  };
}
