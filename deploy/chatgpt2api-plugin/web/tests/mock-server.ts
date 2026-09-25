import { MOCK_KEY, mockStatus, PRIVATE_SENTINEL } from './fixtures.ts';

export function startMockServer() {
  const payload: unknown = structuredClone(mockStatus);
  const state = { payload, status: 200, hold: false,
    calls: 0, authorized: 0, mutations: 0, redirects: 0 };
  const waiters: (() => void)[] = [];
  const server = Bun.serve({
    hostname: '127.0.0.1', port: 0, idleTimeout: 0,
    async fetch(request) {
      const url = new URL(request.url);
      if (request.method !== 'GET') { state.mutations++; return new Response(null, { status: 405 }); }
      if (url.pathname === '/mock') return new Response(Bun.file(`${import.meta.dir}/mock-host.html`));
      if (url.pathname === '/mock-tokens.css') return new Response(
        Bun.file(`${import.meta.dir}/../../../gemini-web-plugin/web/src/tokens.css`),
        { headers: { 'Content-Type': 'text/css' } },
      );
      if (url.pathname === '/v0/resource/plugins/chatgpt2api/index') {
        return new Response(Bun.file(`${import.meta.dir}/../index.html`));
      }
      if (url.pathname === '/management.html') return new Response('MOCK parent provider settings destination');
      if (url.pathname === '/redirect-target') { state.redirects++; return Response.json(mockStatus); }
      if (url.pathname !== '/v0/management/plugins/chatgpt2api/status') return new Response(null, { status: 404 });
      state.calls++;
      if (request.headers.get('Authorization') !== `Bearer ${MOCK_KEY}`) {
        return Response.json({ error: PRIVATE_SENTINEL }, { status: 401 });
      }
      state.authorized++;
      if (state.hold) await new Promise<void>((resolve) => waiters.push(resolve));
      if (state.status === 302) return new Response(null, { status: 302, headers: { Location: '/redirect-target' } });
      if (state.status !== 200) return Response.json({ error: PRIVATE_SENTINEL }, { status: state.status });
      return Response.json(state.payload);
    },
  });
  return { server, state, url: new URL('/mock', server.url).href,
    release: () => { state.hold = false; for (const resolve of waiters.splice(0)) resolve(); } };
}
