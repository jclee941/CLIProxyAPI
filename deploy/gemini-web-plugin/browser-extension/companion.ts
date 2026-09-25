import { CaptureError, type CapturedSession, type CaptureSelection } from "./capture.ts";
import {
  authorizeSender, assertNever, consentMessage, MAX_ACTIVE, PORT_NAME, portalMessage, postToPort, TTL_MS,
  type Begin, type CloseReason, type Port, type PortalSender, type ViewMessage,
} from "./protocol.ts";

export type Candidate = { readonly tabId: number; readonly storeId: string; readonly url: string; readonly authUser: number };
export type Clock = { readonly now: () => number; readonly schedule: (callback: () => void, delay: number) => () => void };
export type CompanionHost = {
  readonly origin: string;
  readonly clock: Clock;
  readonly claimState: (state: string, expires: number) => Promise<boolean>;
  readonly openConsent: (request: LoginRequest) => Promise<void>;
  readonly listTabs: () => Promise<readonly Candidate[]>;
  readonly hasPermissions: () => Promise<boolean>;
  readonly verifyPortal: (sender: PortalSender) => Promise<boolean>;
  readonly capture: (selection: CaptureSelection, signal: AbortSignal) => Promise<CapturedSession>;
  readonly openGemini: () => Promise<void>;
};

export class LoginCompanion {
  private readonly requests = new Set<LoginRequest>();
  constructor(private readonly host: CompanionHost) {}

  connect(port: Port): void {
    const sender = authorizeSender(port.sender, this.host.origin);
    if (port.name !== PORT_NAME || !sender || this.requests.size >= MAX_ACTIVE) {
      port.disconnect();
      return;
    }
    const request = new LoginRequest(port, sender, this.host);
    this.requests.add(request);
    request.onClose = () => this.requests.delete(request);
  }
}

export class LoginRequest {
  private begin: Begin | null = null;
  private phase: "waiting" | "starting" | "choosing" | "loading" | "capturing" | "delivered" | "closed" = "waiting";
  private choices: readonly Candidate[] = [];
  private selected: Candidate | null = null;
  private ui: Port | null = null;
  private permissions = false;
  private readonly abort = new AbortController();
  private readonly expires: number;
  private readonly cancelTimer: () => void;
  onClose: () => void = () => undefined;

  get isOpen(): boolean { return this.live(); }

  constructor(private readonly portal: Port, private readonly sender: PortalSender, private readonly host: CompanionHost) {
    this.expires = host.clock.now() + TTL_MS;
    this.cancelTimer = host.clock.schedule(() => this.close("expired"), TTL_MS);
    portal.onMessage.addListener(this.receivePortal);
    portal.onDisconnect.addListener(this.disconnected);
  }

  private live(): boolean {
    if (this.phase === "closed") return false;
    if (this.host.clock.now() >= this.expires) { this.close("expired"); return false; }
    return true;
  }

  private readonly disconnected = () => this.close("cancelled");
  private readonly consentDisconnected = () => {
    this.ui?.onMessage.removeListener(this.receiveConsent);
    this.ui?.onDisconnect.removeListener(this.consentDisconnected);
    this.ui = null;
    this.close("cancelled");
  };
  private readonly receivePortal = (raw: unknown): void => {
    if (!this.live()) return;
    const message = portalMessage.safeParse(raw);
    if (!message.success || !authorizeSender(this.portal.sender, this.host.origin)) { this.close("invalid_message"); return; }
    const value = message.data;
    switch (value.type) {
      case "begin":
        if (this.phase !== "waiting") { this.close("invalid_message"); return; }
        this.phase = "starting";
        this.begin = value;
        void this.start(value).catch(() => this.close("unavailable"));
        return;
      case "ack":
        this.close(this.begin?.state === value.state && this.phase === "delivered" ? "acknowledged" : "invalid_message");
        return;
      case "cancel":
        this.close(this.begin?.state === value.state ? "cancelled" : "invalid_message");
        return;
      default: assertNever(value);
    }
  };

  private async start(begin: Begin): Promise<void> {
    if (!await this.host.claimState(begin.state, this.expires)) { this.close("invalid_message"); return; }
    if (!this.live()) return;
    this.phase = "choosing";
    if (!postToPort(this.portal, { type: "ready", state: begin.state })) { this.close("unavailable"); return; }
    await this.host.openConsent(this);
  }

  attachConsent(port: Port): void {
    if (!this.live() || this.phase !== "choosing" || this.ui) { port.disconnect(); return; }
    this.ui = port;
    port.onMessage.addListener(this.receiveConsent);
    port.onDisconnect.addListener(this.consentDisconnected);
    void this.refresh().catch(() => this.close("unavailable"));
  }

  private readonly receiveConsent = (raw: unknown): void => {
    if (!this.live()) return;
    const message = consentMessage.safeParse(raw);
    if (!message.success) { this.close("invalid_message"); return; }
    const value = message.data;
    switch (value.type) {
      case "cancel": this.close("cancelled"); return;
      case "keepalive": return;
      case "select":
        if (this.phase !== "choosing") { this.close("invalid_message"); return; }
        this.selected = this.choices.find((choice) => choice.tabId === value.tab_id) ?? null;
        if (!this.selected) { this.close("invalid_message"); return; }
        this.publish();
        return;
      case "approve":
        if (this.phase !== "choosing" || !this.selected || !this.begin || !this.permissions) { this.close("invalid_message"); return; }
        this.phase = "capturing";
        this.publish();
        if (!this.live()) return;
        void this.deliver(this.selected, this.begin).catch((error: unknown) => {
          this.close(error instanceof CaptureError ? "capture_failed" : "unavailable");
        });
        return;
      case "refresh":
        if (this.phase !== "choosing") { this.close("invalid_message"); return; }
        void this.refresh().catch(() => this.close("unavailable"));
        return;
      case "open_gemini":
        if (this.phase !== "choosing") { this.close("invalid_message"); return; }
        this.selected = null;
        this.publish();
        if (!this.live()) return;
        void this.host.openGemini().catch(() => this.close("unavailable"));
        return;
      default: assertNever(value);
    }
  };

  private async refresh(): Promise<void> {
    this.phase = "loading";
    this.selected = null;
    this.choices = [];
    this.publish();
    if (!this.live()) return;
    this.permissions = await this.host.hasPermissions();
    if (!this.live()) return;
    const choices = this.permissions ? await this.host.listTabs() : [];
    if (!this.live()) return;
    this.choices = choices;
    this.phase = "choosing";
    this.publish();
  }

  private async deliver(selected: Candidate, begin: Begin): Promise<void> {
    const authorized = await this.host.verifyPortal(this.sender);
    if (!this.live()) return;
    if (!authorized || !await this.host.hasPermissions()) { this.close("unavailable"); return; }
    if (!this.live()) return;
    const session = await this.host.capture({
      tabId: selected.tabId, storeId: selected.storeId, url: selected.url,
      ...(begin.expected_identity ? { expectedIdentity: begin.expected_identity } : {}),
    }, this.abort.signal);
    if (!this.live()) return;
    if (!await this.host.verifyPortal(this.sender)) { this.close("unavailable"); return; }
    if (!this.live()) return;
    this.phase = "delivered";
    if (!postToPort(this.portal, { type: "session", state: begin.state, token: session.token,
      account_sha256: session.account_sha256, auth_user: session.auth_user })) { this.close("unavailable"); return; }
    this.choices = [];
    this.selected = null;
    this.publish();
  }

  private publish(): void {
    if (this.phase === "waiting" || this.phase === "starting" || this.phase === "closed") return;
    const message: ViewMessage = { type: "view", destination: this.host.origin, phase: this.phase,
      permissions: this.permissions, replacement: Boolean(this.begin?.expected_identity),
      choices: this.choices.map((choice) => ({ tab_id: choice.tabId, auth_user: choice.authUser })),
      selected: this.selected?.tabId ?? null };
    if (this.ui && !postToPort(this.ui, message)) this.close("unavailable");
  }

  close(reason: CloseReason): void {
    if (this.phase === "closed") return;
    this.phase = "closed";
    this.abort.abort();
    this.cancelTimer();
    this.begin = null;
    this.choices = [];
    this.selected = null;
    this.portal.onMessage.removeListener(this.receivePortal);
    this.portal.onDisconnect.removeListener(this.disconnected);
    const ui = this.ui;
    this.ui = null;
    this.onClose();
    if (ui) postToPort(ui, { type: "closed", reason });
    ui?.onMessage.removeListener(this.receiveConsent);
    ui?.onDisconnect.removeListener(this.consentDisconnected);
    ui?.disconnect();
    this.portal.disconnect();
  }
}
