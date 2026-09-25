import { describe, expect, test } from 'bun:test';
import { connectCompanion, type ExternalPort, type ExternalRuntime, type PortalMessage } from '../src/companion';
import { loginStartSchema } from '../src/login-contract';
import { MOCK_TOKEN } from './fixtures';

class FakeEvent<Listener> {
  readonly listeners = new Set<Listener>();
  addListener(listener: Listener): void { this.listeners.add(listener); }
  removeListener(listener: Listener): void { this.listeners.delete(listener); }
}

class FakePort implements ExternalPort {
  readonly name = 'gemini-web-login';
  readonly onMessage = new FakeEvent<(message: unknown, port: ExternalPort) => void>();
  readonly onDisconnect = new FakeEvent<(port: ExternalPort) => void>();
  readonly sent: PortalMessage[] = [];
  disconnected = false;
  postMessage(message: PortalMessage): void { this.sent.push(message); }
  disconnect(): void { this.disconnected = true; }
  emit(message: unknown, source: ExternalPort = this): void {
    for (const listener of this.onMessage.listeners) listener(message, source);
  }
}

function fixture() {
  const start = loginStartSchema.parse({ state: 'MOCK_LOGIN_STATE', status: 'pending', expires_at: 1_900_000_000,
    extension_id: 'a'.repeat(32), manager_origin: 'https://manager.example' });
  const port = new FakePort();
  const connections: { extensionId: string; name: string }[] = [];
  const runtime: ExternalRuntime = { connect(extensionId, options) {
    connections.push({ extensionId, name: options.name });
    return port;
  } };
  const received: string[] = [];
  const errors: string[] = [];
  const connection = connectCompanion(start, {
    ready: () => received.push('ready'),
    session: (session) => received.push(session.token === MOCK_TOKEN ? 'session' : 'invalid'),
    error: (error) => errors.push(error.message),
  }, runtime);
  const session = { type: 'session', state: start.state, token: MOCK_TOKEN, account_sha256: 'a'.repeat(64), auth_user: 0 };
  return { start, port, connections, received, errors, connection, session };
}

describe('External port binding', () => {
  test('connects only the configured extension and sends no Manager credential', () => {
    const subject = fixture();
    expect(subject.connections).toEqual([{ extensionId: 'a'.repeat(32), name: 'gemini-web-login' }]);
    expect(subject.port.sent).toEqual([{ type: 'begin', state: subject.start.state }]);
    subject.connection.close();
  });

  test('ignores a message from a different port', () => {
    const subject = fixture();
    subject.port.emit({ type: 'ready', state: subject.start.state }, new FakePort());
    subject.port.emit(subject.session, new FakePort());
    expect(subject.received).toEqual([]);
    subject.connection.close();
  });

  test('ignores a session with the wrong state', () => {
    const subject = fixture();
    subject.port.emit({ type: 'ready', state: subject.start.state });
    subject.port.emit({ ...subject.session, state: 'MOCK_WRONG_STATE' });
    expect(subject.received).toEqual(['ready']);
    subject.connection.close();
  });

  test('delivers an approved session at most once', () => {
    const subject = fixture();
    subject.port.emit({ type: 'ready', state: subject.start.state });
    subject.port.emit(subject.session);
    subject.port.emit(subject.session);
    expect(subject.received).toEqual(['ready', 'session']);
    subject.connection.ack();
    expect(subject.port.sent.at(-1)).toEqual({ type: 'ack', state: subject.start.state });
    expect(subject.port.disconnected).toBe(true);
    expect(subject.port.onMessage.listeners.size).toBe(0);
  });

  test('does not accept a session before the ready handshake', () => {
    const subject = fixture();
    subject.port.emit(subject.session);
    expect(subject.received).toEqual([]);
    expect(subject.errors).toHaveLength(1);
    expect(subject.port.disconnected).toBe(true);
  });

  test('does not hand off after cancellation', () => {
    const subject = fixture();
    subject.connection.cancel();
    subject.port.emit(subject.session);
    expect(subject.port.sent.at(-1)).toEqual({ type: 'cancel', state: subject.start.state });
    expect(subject.received).toEqual([]);
  });

  test('never reflects an arbitrary companion error', () => {
    const subject = fixture();
    subject.port.emit({ type: 'error', state: subject.start.state, code: MOCK_TOKEN });
    expect(subject.errors).toHaveLength(1);
    expect(subject.errors.join('')).not.toContain(MOCK_TOKEN);
  });

  test('fails actionably when Chrome runtime is absent', () => {
    const subject = fixture();
    expect(() => connectCompanion(subject.start, { ready() {}, session() {}, error() {} }, undefined)).toThrow();
    subject.connection.close();
  });
});
