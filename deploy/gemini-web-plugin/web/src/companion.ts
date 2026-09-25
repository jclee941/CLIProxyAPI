import { assertNever, companionMessageSchema, LoginError, type LoginStart, type LoginState, type Session } from './login-contract';

export type PortalMessage =
  | { readonly type: 'begin'; readonly state: LoginState; readonly expected_identity?: LoginStart['expected_identity'] }
  | { readonly type: 'ack' | 'cancel'; readonly state: LoginState };
type PortEvent<Listener> = { readonly addListener: (listener: Listener) => void; readonly removeListener: (listener: Listener) => void };
export type ExternalPort = {
  readonly name: string;
  readonly onMessage: PortEvent<(message: unknown, port: ExternalPort) => void>;
  readonly onDisconnect: PortEvent<(port: ExternalPort) => void>;
  readonly postMessage: (message: PortalMessage) => void;
  readonly disconnect: () => void;
};
export type ExternalRuntime = {
  readonly connect: (extensionId: string, options: { readonly name: 'gemini-web-login' }) => ExternalPort;
  readonly lastError?: { readonly message?: string };
};
declare global {
  interface Window { readonly chrome?: { readonly runtime?: ExternalRuntime } }
}
type CompanionEvents = {
  readonly ready: () => void;
  readonly session: (session: Session) => void;
  readonly error: (error: LoginError) => void;
};
export type CompanionConnection = { readonly ack: () => void; readonly cancel: () => void; readonly close: () => void };

export function connectCompanion(start: LoginStart, events: CompanionEvents, runtime: ExternalRuntime | undefined): CompanionConnection {
  if (!runtime?.connect) throw new LoginError('companion_unavailable');
  let port: ExternalPort;
  try { port = runtime.connect(start.extension_id, { name: 'gemini-web-login' }); }
  catch (error) {
    if (error instanceof Error) throw new LoginError('companion_unavailable');
    throw error;
  }
  let ready = false;
  let delivered = false;
  let closed = false;
  const close = () => {
    if (closed) return;
    closed = true;
    port.onMessage.removeListener(receive);
    port.onDisconnect.removeListener(disconnected);
    port.disconnect();
  };
  const fail = (error: LoginError) => { close(); events.error(error); };
  const send = (message: PortalMessage) => {
    if (closed) return;
    try { port.postMessage(message); }
    catch (error) {
      if (!(error instanceof Error)) throw error;
      fail(new LoginError('companion_unavailable'));
    }
  };
  const receive = (message: unknown, source: ExternalPort) => {
    if (closed || delivered || source !== port) return;
    const parsed = companionMessageSchema.safeParse(message);
    if (!parsed.success) { fail(new LoginError('companion_protocol')); return; }
    const reply = parsed.data;
    if (reply.state !== start.state) return;
    switch (reply.type) {
      case 'ready':
        if (!ready) { ready = true; events.ready(); }
        return;
      case 'error': fail(new LoginError('companion_protocol')); return;
      case 'session':
        if (!ready) { fail(new LoginError('companion_protocol')); return; }
        if (start.expected_identity && (reply.account_sha256 !== start.expected_identity.account_sha256
          || reply.auth_user !== start.expected_identity.auth_user)) {
          reply.token = '';
          fail(new LoginError('identity'));
          return;
        }
        delivered = true;
        try { events.session(reply); }
        finally { reply.token = ''; }
        return;
      default: assertNever(reply);
    }
  };
  const disconnected = (source: ExternalPort) => {
    const connectionFailed = Boolean(runtime.lastError);
    if (source !== port || closed) return;
    close();
    if (!delivered) events.error(new LoginError(connectionFailed ? 'companion_unavailable' : 'companion_protocol'));
  };
  port.onMessage.addListener(receive);
  port.onDisconnect.addListener(disconnected);
  if (port.name !== 'gemini-web-login') fail(new LoginError('companion_protocol'));
  else send({ type: 'begin', state: start.state, ...(start.expected_identity ? { expected_identity: start.expected_identity } : {}) });
  return {
    close,
    ack: () => { send({ type: 'ack', state: start.state }); close(); },
    cancel: () => { send({ type: 'cancel', state: start.state }); close(); },
  };
}
