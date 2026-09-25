import { connectCompanion, type CompanionConnection } from './companion';
import { completeLogin, loginAction, loginErrorMessage, startLogin } from './login-api';
import { accountErrorMessage } from './api';
import { assertLoginLocation, LoginError, type LoginAction, type LoginInput, type LoginStart, type LoginView, type Session } from './login-contract';

export type LoginSnapshot = {
  readonly phase: 'idle' | 'starting' | 'connecting' | 'approval' | LoginView['status'];
  readonly busy: boolean;
  readonly active: boolean;
  readonly canConnect: boolean;
  readonly canCheck: boolean;
  readonly canCancel: boolean;
  readonly canReconcile: boolean;
  readonly canRestart: boolean;
  readonly expiresAt: number | undefined;
  readonly extensionId: string | undefined;
  readonly result: Omit<LoginView, 'state' | 'error'> | undefined;
  readonly message: string;
};
type FlowEvents = { readonly changed: (snapshot: LoginSnapshot) => void; readonly failure: (error: unknown) => void };

export class LoginFlow {
  #start: LoginStart | undefined;
  #connection: CompanionConnection | undefined;
  #companionReady = false;
  #timer: ReturnType<typeof setTimeout> | undefined;
  #expiresAt: number | undefined;
  #phase: LoginSnapshot['phase'] = 'idle';
  #busy = false;
  #sent = false;
  #settled = false;
  #cancelRequested = false;
  #cancelling = false;
  #sequence = 0;
  #result: LoginSnapshot['result'];
  #message = '';

  constructor(private readonly events: FlowEvents) {}

  get snapshot(): LoginSnapshot {
    const canRestart = !this.#busy && (!this.#start || this.#settled);
    return {
      phase: this.#phase, busy: this.#busy, active: !canRestart,
      canConnect: Boolean(this.#start) && !this.#busy && !this.#sent && !this.#settled && !this.#connection
        && !this.#cancelRequested && (this.#phase === 'pending' || this.#phase === 'error'),
      canCheck: Boolean(this.#start) && !this.#busy,
      canCancel: Boolean(this.#start) && !this.#settled && !this.#cancelling,
      canReconcile: this.#phase === 'host_sync_pending' && !this.#busy,
      canRestart, expiresAt: this.#expiresAt, extensionId: this.#start?.extension_id,
      result: this.#result, message: this.#message,
    };
  }

  #publish(): void { this.events.changed(this.snapshot); }

  reset(): void {
    if (!this.snapshot.canRestart) return;
    this.dispose();
    this.#start = undefined;
    this.#result = undefined;
    this.#phase = 'idle';
    this.#sent = false;
    this.#settled = false;
    this.#cancelRequested = false;
    this.#expiresAt = undefined;
    this.#message = '';
    this.#publish();
  }

  async start(input: LoginInput): Promise<void> {
    if (!this.snapshot.canRestart) return;
    this.reset();
    this.#busy = true;
    this.#phase = 'starting';
    this.#publish();
    const sequence = ++this.#sequence;
    try {
      const started = await startLogin(input);
      if (sequence !== this.#sequence) return;
      this.#start = started;
      this.#expiresAt = Math.min(started.expires_at, Date.now() / 1000 + 600);
      this.#phase = 'pending';
      this.#timer = setTimeout(() => this.#expire(), Math.max(0, this.#expiresAt * 1000 - Date.now()));
      this.#expire();
    } catch (error) {
      if (sequence === this.#sequence) this.#fail(error);
    } finally {
      if (sequence === this.#sequence) { this.#busy = false; this.#publish(); }
    }
  }

  connect(): void {
    this.#expire();
    if (!this.#start || !this.snapshot.canConnect) return;
    this.#message = '';
    this.#phase = 'connecting';
    this.#companionReady = false;
    this.#publish();
    try {
      assertLoginLocation(new URL(window.location.href), this.#start.manager_origin);
      let rejected = false;
      const connection = connectCompanion(this.#start, {
        ready: () => { this.#companionReady = true; this.#phase = 'approval'; this.#publish(); },
        session: (session) => this.#handoff(session),
        error: (error) => { rejected = true; this.#connection = undefined; this.#fail(error); this.#publish(); },
      }, window.chrome?.runtime);
      if (!rejected) this.#connection = connection;
      this.#publish();
    } catch (error) { this.#fail(error); this.#publish(); }
  }

  #handoff(session: Session): void {
    this.#expire();
    if (!this.#start || this.#sent || this.#settled || this.#cancelRequested) return;
    this.#sent = true;
    this.#busy = true;
    this.#phase = 'processing';
    this.#message = '';
    clearTimeout(this.#timer);
    this.#publish();
    const sequence = ++this.#sequence;
    const connection = this.#connection;
    const operation = completeLogin({ state: this.#start.state, token: session.token,
      account_sha256: session.account_sha256, auth_user: session.auth_user,
      extension_id: this.#start.extension_id, consent: true });
    const acknowledged = operation.finally(() => {
      if (this.#connection === connection) {
        connection?.ack();
        this.#connection = undefined;
      }
    });
    void this.#observe(acknowledged, sequence);
  }

  async action(action: LoginAction): Promise<void> {
    if (!this.#start || this.#busy && action !== 'cancel') return;
    if (action === 'reconcile' && !this.snapshot.canReconcile) return;
    if (action === 'cancel') {
      if (!this.snapshot.canCancel) return;
      this.#cancelRequested = true;
      this.#cancelling = true;
      this.#connection?.cancel();
      this.#connection = undefined;
      clearTimeout(this.#timer);
    }
    this.#busy = true;
    this.#message = '';
    this.#publish();
    await this.#observe(loginAction(action, this.#start.state), ++this.#sequence);
  }

  async #observe(operation: Promise<LoginView>, sequence: number): Promise<void> {
    try {
      const view = await operation;
      if (sequence !== this.#sequence) return;
      this.#result = { status: view.status, models_ready: view.models_ready,
        ...(view.account_id ? { account_id: view.account_id } : {}),
        ...(view.expires_at ? { expires_at: view.expires_at } : {}) };
      this.#phase = view.status === 'pending' && this.#connection
        ? this.#companionReady ? 'approval' : 'connecting' : view.status;
      this.#settled = ['ready', 'saved', 'error', 'expired', 'cancelled'].includes(view.status);
      this.#message = view.status === 'error' ? accountErrorMessage(view.error ?? '') : '';
      if (this.#settled) { clearTimeout(this.#timer); this.#connection?.close(); this.#connection = undefined; }
    } catch (error) {
      if (sequence === this.#sequence) this.#fail(error);
    } finally {
      if (sequence === this.#sequence) { this.#busy = false; this.#cancelling = false; this.#publish(); }
    }
  }

  #fail(error: unknown): void {
    this.#phase = 'error';
    this.#message = loginErrorMessage(error, this.#sent);
    this.events.failure(error);
  }

  #expire(): void {
    if (this.#sent || this.#settled || !this.#expiresAt || Date.now() < this.#expiresAt * 1000) return;
    this.#connection?.cancel();
    this.#connection = undefined;
    this.#settled = true;
    this.#phase = 'expired';
    this.#message = new LoginError('expired').message;
    clearTimeout(this.#timer);
    this.#publish();
  }

  dispose(): void {
    this.#sequence++;
    clearTimeout(this.#timer);
    this.#connection?.cancel();
    this.#connection = undefined;
  }
}
