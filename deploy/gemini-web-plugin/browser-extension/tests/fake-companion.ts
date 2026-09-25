import type { CapturedSession, CaptureSelection } from "../capture.ts";
import type { Candidate, CompanionHost, LoginCompanion, LoginRequest } from "../companion.ts";
import { PORTAL_ORIGIN, PORTAL_PATH, PORT_NAME, type Port } from "../protocol.ts";

export const STATE = "a".repeat(64);
export const VALID_SENDER = { url: PORTAL_ORIGIN + PORTAL_PATH, origin: PORTAL_ORIGIN,
  tab: { id: 10 }, frameId: 0, documentId: "SYNTHETIC-portal" };
export const settle = () => new Promise<void>((resolve) => setImmediate(resolve));

export class FakePort implements Port {
  name = PORT_NAME;
  sender: unknown = structuredClone(VALID_SENDER);
  readonly sent: unknown[] = [];
  disconnected = false;
  private readonly messages = new Set<(message: unknown) => void>();
  private readonly disconnects = new Set<() => void>();
  readonly onMessage = {
    addListener: (listener: (message: unknown) => void) => { this.messages.add(listener); },
    removeListener: (listener: (message: unknown) => void) => { this.messages.delete(listener); },
  };
  readonly onDisconnect = {
    addListener: (listener: () => void) => { this.disconnects.add(listener); },
    removeListener: (listener: () => void) => { this.disconnects.delete(listener); },
  };
  postMessage = (message: unknown) => {
    if (this.disconnected) throw new Error("SYNTHETIC-disconnected-port");
    this.sent.push(structuredClone(message));
  };
  receive(message: unknown): void { for (const listener of this.messages) listener(message); }
  disconnect = () => { this.disconnected = true; };
  remoteDisconnect(): void { this.disconnected = true; for (const listener of this.disconnects) listener(); }
}

export class FakeCompanion implements CompanionHost {
  readonly origin = PORTAL_ORIGIN;
  time = 1000;
  permissions = true;
  authorized = true;
  readonly timers = new Set<() => void>();
  readonly clock = {
    now: () => this.time,
    schedule: (callback: () => void, _delay: number) => {
      this.timers.add(callback);
      return () => { this.timers.delete(callback); };
    },
  };
  readonly claimed = new Set<string>();
  readonly dialogs: LoginRequest[] = [];
  readonly captures: CaptureSelection[] = [];
  readonly session: CapturedSession = { token: "gemini-web:v1:SYNTHETIC", account_sha256: "b".repeat(64), auth_user: 2 };
  choices: readonly Candidate[] = [
    { tabId: 42, storeId: "1", url: "https://gemini.google.com/u/2/app?hl=ko", authUser: 2 },
    { tabId: 73, storeId: "0", url: "https://gemini.google.com/u/7/app", authUser: 7 },
  ];
  captureGate: Promise<void> = Promise.resolve();
  opened = 0;
  claimState = async (state: string, _expires: number) => {
    if (this.claimed.has(state)) return false;
    this.claimed.add(state);
    return true;
  };
  openConsent = async (request: LoginRequest) => { this.dialogs.push(request); };
  listTabs = async () => this.choices;
  hasPermissions = async () => this.permissions;
  verifyPortal = async () => this.authorized;
  capture = async (selection: CaptureSelection, _signal: AbortSignal) => {
    this.captures.push(selection);
    await this.captureGate;
    return this.session;
  };
  openGemini = async () => { this.opened += 1; };

  async begin(companion: LoginCompanion, message: unknown = { type: "begin", state: STATE }): Promise<FakePort> {
    const port = new FakePort();
    companion.connect(port);
    port.receive(message);
    await settle();
    return port;
  }

  consent(index = 0): FakePort {
    const request = this.dialogs[index];
    if (!request) throw new Error("SYNTHETIC-missing-request");
    const port = new FakePort();
    request.attachConsent(port);
    return port;
  }
}
