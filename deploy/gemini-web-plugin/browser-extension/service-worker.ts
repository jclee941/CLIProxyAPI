import { z } from "zod";
import { captureWithSignal, listCandidates, verifyPortalDocument } from "./browser-runtime.ts";
import { CaptureError } from "./capture.ts";
import { LoginCompanion, type CompanionHost, type LoginRequest } from "./companion.ts";
import { CONSENT_PORT, GOOGLE_HOSTS, safeIndex } from "./protocol.ts";
import { ReplayLedger } from "./replay.ts";

declare const __PORTAL_ORIGIN__: string;
const origin = __PORTAL_ORIGIN__;
const ledger = new ReplayLedger(chrome.storage.local, Date.now);
const bindings = new Map<number, LoginRequest>();
const opening = new Set<Promise<void>>();
const popupUrl = chrome.runtime.getURL("popup.html");
const consentSender = z.object({ id: z.literal(chrome.runtime.id), url: z.literal(popupUrl),
  origin: z.literal(`chrome-extension://${chrome.runtime.id}`), tab: z.object({ id: safeIndex }), frameId: z.literal(0) });
const hasPermissions = () => chrome.permissions.contains({
  permissions: ["cookies", "tabs", "scripting", "storage"], origins: [...GOOGLE_HOSTS, `${origin}/*`],
});

async function openConsent(request: LoginRequest): Promise<void> {
  const created = await chrome.windows.create({ type: "popup", url: popupUrl, width: 520, height: 720, focused: true });
  if (!created) { request.close("unavailable"); return; }
  const tabId = created.tabs?.[0]?.id;
  if (tabId === undefined) { request.close("unavailable"); return; }
  if (!request.isOpen) {
    if (created.id !== undefined) await chrome.windows.remove(created.id);
    return;
  }
  bindings.set(tabId, request);
  const onClose = request.onClose;
  request.onClose = () => { bindings.delete(tabId); onClose(); };
}

const host: CompanionHost = {
  origin,
  clock: { now: Date.now, schedule: (callback, delay) => {
    const timer = setTimeout(callback, delay);
    return () => clearTimeout(timer);
  } },
  claimState: (state, expires) => ledger.claim(state, expires),
  openConsent: (request) => {
    const operation = openConsent(request);
    opening.add(operation);
    void operation.then(() => opening.delete(operation), () => opening.delete(operation));
    return operation;
  },
  listTabs: () => listCandidates(chrome),
  hasPermissions,
  verifyPortal: (sender) => verifyPortalDocument(chrome.scripting, sender),
  capture: async (selection, signal) => {
    const session = await captureWithSignal(chrome, selection, signal);
    if (!await hasPermissions()) throw new CaptureError("capture_unavailable");
    return session;
  },
  openGemini: async () => { await chrome.tabs.create({ url: "https://gemini.google.com/app", active: true }); },
};
const companion = new LoginCompanion(host);
chrome.runtime.onConnectExternal.addListener((port) => companion.connect(port));
chrome.runtime.onConnect.addListener((port) => {
  const sender = consentSender.safeParse(port.sender);
  if (port.name !== CONSENT_PORT || !sender.success) { port.disconnect(); return; }
  let connected = true;
  const disconnected = () => { connected = false; };
  port.onDisconnect.addListener(disconnected);
  void Promise.allSettled([...opening]).then(() => {
    port.onDisconnect.removeListener(disconnected);
    if (!connected) return;
    const request = bindings.get(sender.data.tab.id);
    if (!request) { port.disconnect(); return; }
    request.attachConsent(port);
  });
});
chrome.tabs.onRemoved.addListener((tabId) => bindings.get(tabId)?.close("cancelled"));
