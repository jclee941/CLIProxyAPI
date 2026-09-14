import type { ExternalPort, ExternalRuntime, PortalMessage } from '../src/companion';

export type CompanionMode = 'normal' | 'absent' | 'unavailable' | 'wrong-state' | 'untrusted-port' | 'error' | 'early-session' | 'malformed' | 'identity-mismatch' | 'disconnect' | 'throw-connect';
type Evidence = { readonly messages: readonly string[]; readonly fields: readonly string[][]; readonly disconnects: number; readonly connects: number; readonly writes: number; readonly lastErrorReads: number };
declare global { interface Window { readonly mockCompanionEvidence?: () => Evidence } }

export function installFakeCompanion(mode: CompanionMode): void {
  if (location.pathname !== '/v0/resource/plugins/gemini-web/index') return;
  const messages: string[] = [];
  const fields: string[][] = [];
  let disconnects = 0;
  let connects = 0;
  let writes = 0;
  let lastErrorReads = 0;
  const originalSet = Storage.prototype.setItem;
  Storage.prototype.setItem = function (key: string, value: string): void {
    writes++;
    originalSet.call(this, key, value);
  };
  Object.defineProperty(window, 'mockCompanionEvidence', { value: () => ({ messages, fields, disconnects, connects, writes, lastErrorReads }) });
  if (mode === 'absent') { Object.defineProperty(window, 'chrome', { value: {} }); return; }
  const runtime: ExternalRuntime = {
    get lastError() { lastErrorReads++; return { message: 'MOCK_PRIVATE_EXTENSION_ERROR' }; },
    connect(_extensionId, options) {
      connects++;
      if (mode === 'throw-connect') throw new Error('MOCK_PRIVATE_EXTENSION_ERROR');
      const listeners = new Set<(message: unknown, source: ExternalPort) => void>();
      const disconnected = new Set<(source: ExternalPort) => void>();
      let state = '';
      let closed = false;
      const dispatch = (message: unknown, source: ExternalPort = port) => {
        for (const listener of listeners) listener(message, source);
      };
      const approve = () => {
        const session = { type: 'session', state: mode === 'wrong-state' ? 'MOCK_UNTRUSTED_STATE' : state,
          token: 'gemini-web:v1:TU9DSw', account_sha256: (mode === 'identity-mismatch' ? 'c' : 'b').repeat(64), auth_user: 1 };
        dispatch(session, mode === 'untrusted-port' ? { ...port } : port);
        dispatch(session, mode === 'untrusted-port' ? { ...port } : port);
      };
      const port: ExternalPort = {
        name: options.name,
        onMessage: { addListener: (listener) => listeners.add(listener), removeListener: (listener) => listeners.delete(listener) },
        onDisconnect: { addListener: (listener) => disconnected.add(listener), removeListener: (listener) => disconnected.delete(listener) },
        postMessage(message: PortalMessage) {
          messages.push(message.type);
          fields.push(Object.keys(message));
          if (message.type !== 'begin') return;
          state = message.state;
          queueMicrotask(() => {
            if (closed) return;
            if (mode === 'unavailable' || mode === 'disconnect') {
              for (const listener of disconnected) listener(port);
              return;
            }
            if (mode === 'error') { dispatch({ type: 'error', state, code: 'MOCK_PRIVATE_EXTENSION_ERROR' }); return; }
            if (mode === 'malformed') { dispatch({ type: 'session', state, token: 'MOCK_PRIVATE_ERROR' }); return; }
            if (mode === 'early-session') { approve(); return; }
            dispatch({ type: 'ready', state });
          });
        },
        disconnect() {
          if (closed) return;
          closed = true;
          disconnects++;
          window.removeEventListener('MOCK_COMPANION_APPROVE', approve);
        },
      };
      window.addEventListener('MOCK_COMPANION_APPROVE', approve);
      return port;
    },
  };
  Object.defineProperty(window, 'chrome', { value: { runtime } });
}
