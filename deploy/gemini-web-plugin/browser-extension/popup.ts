import { assertNever, CONSENT_PORT, GOOGLE_HOSTS, viewMessage, type CloseReason, type ViewMessage } from "./protocol.ts";

declare const __PORTAL_ORIGIN__: string;
function node<Kind extends keyof HTMLElementTagNameMap>(selector: string, tag: Kind): HTMLElementTagNameMap[Kind] {
  const typed = document.querySelector<HTMLElementTagNameMap[Kind]>(tag + selector);
  if (!typed) throw new TypeError("invalid_popup_markup");
  return typed;
}
const status = node("#status", "p");
const error = node("#error", "p");
const request = node("#request", "section");
const choices = node("#choices", "div");
const fieldset = node("#tab-fieldset", "fieldset");
const consent = node("#consent", "input");
const approve = node("#approve", "button");
const cancel = node("#cancel", "button");
const refresh = node("#refresh", "button");
const openGemini = node("#open-gemini", "button");
const grant = node("#grant", "button");
const permissionPanel = node("#permission-panel", "div");
node("#destination", "strong").textContent = __PORTAL_ORIGIN__;
node("#extension-id", "code").textContent = chrome.runtime.id;

const port = chrome.runtime.connect({ name: CONSENT_PORT });
let view: Extract<ViewMessage, { type: "view" }> | null = null;
let ended = false;
let choiceKey = "";
const keepalive = setInterval(() => { if (!ended) send({ type: "keepalive" }); }, 20000);
const closeCopy: Record<CloseReason, string> = {
  acknowledged: "포털이 세션 수신을 확인했습니다. 최종 계정 상태는 포털에서 확인하세요.",
  cancelled: "연결이 취소되었습니다. 다시 연결하려면 포털에서 새 요청을 시작하세요.",
  expired: "10분이 지나 연결 요청이 만료되었습니다. 포털에서 다시 시작하세요.",
  invalid_message: "연결 순서나 선택 정보가 맞지 않아 중단했습니다. 포털에서 다시 시작하세요.",
  capture_failed: "선택한 탭의 계정 또는 쿠키 범위를 안전하게 확인하지 못했습니다. Google 로그인과 계정 선택을 확인한 뒤 포털에서 다시 시작하세요.",
  unavailable: "연결을 계속할 수 없습니다. 사이트 접근 권한과 포털 페이지를 확인한 뒤 다시 시작하세요.",
};

function finish(reason: CloseReason | "idle"): void {
  ended = true;
  view = null;
  clearInterval(keepalive);
  consent.checked = false;
  choices.replaceChildren();
  request.hidden = true;
  approve.hidden = true;
  cancel.textContent = "닫기";
  if (reason === "idle") status.textContent = "포털에서 로그인 연결을 시작하면 별도 확인 창이 열립니다.";
  else if (["acknowledged", "cancelled"].includes(reason)) status.textContent = closeCopy[reason];
  else { status.textContent = "세션을 추가로 전송하지 않습니다."; error.textContent = closeCopy[reason]; error.hidden = false; }
}

function send(message: unknown): void {
  if (ended) return;
  try { port.postMessage(message); }
  catch {
    finish("unavailable");
  }
}

function render(next: Extract<ViewMessage, { type: "view" }>): void {
  if (next.destination !== __PORTAL_ORIGIN__) { finish("unavailable"); port.disconnect(); return; }
  if (next.selected !== view?.selected || next.phase === "loading") consent.checked = false;
  view = next;
  request.hidden = next.phase === "delivered";
  const busy = next.phase !== "choosing";
  fieldset.disabled = busy;
  refresh.disabled = busy;
  openGemini.disabled = busy;
  consent.disabled = busy || next.selected === null;
  approve.disabled = busy || !consent.checked || next.selected === null || !next.permissions;
  approve.hidden = next.phase === "delivered";
  permissionPanel.hidden = next.permissions || next.phase !== "choosing";
  node("#empty", "p").hidden = next.choices.length > 0 || busy || !next.permissions;
  const key = JSON.stringify(next.choices);
  if (key !== choiceKey) {
    choiceKey = key;
    choices.replaceChildren();
    for (const choice of next.choices) {
      const label = document.createElement("label");
      label.className = "choice";
      const radio = document.createElement("input");
      radio.type = "radio";
      radio.name = "gemini-tab";
      radio.value = String(choice.tab_id);
      radio.addEventListener("change", () => { consent.checked = false; send({ type: "select", tab_id: choice.tab_id }); });
      const text = document.createElement("span");
      const title = document.createElement("strong");
      title.textContent = `Gemini · 탭 ${choice.tab_id}`;
      const index = document.createElement("span");
      index.textContent = `계정 인덱스 ${choice.auth_user}`;
      text.append(title, index);
      label.append(radio, text);
      choices.append(label);
    }
  }
  for (const radio of choices.querySelectorAll<HTMLInputElement>("input")) radio.checked = Number(radio.value) === next.selected;
  switch (next.phase) {
    case "choosing": status.textContent = next.replacement ? "기존 계정과 동일한지 검증한 후 교체합니다." : "탭을 고르고 아래 전송 동의를 확인하세요."; break;
    case "loading": status.textContent = "선택 가능한 Gemini 탭을 확인하고 있습니다."; break;
    case "capturing": status.textContent = "선택한 계정과 쿠키 범위를 확인하고 있습니다."; break;
    case "delivered": status.textContent = "원래 포털에 전달했습니다. 포털의 수신 확인을 기다립니다."; break;
    default: assertNever(next.phase);
  }
}

port.onMessage.addListener((raw: unknown) => {
  const message = viewMessage.safeParse(raw);
  if (!message.success) { finish("unavailable"); port.disconnect(); return; }
  switch (message.data.type) {
    case "view": render(message.data); return;
    case "closed": finish(message.data.reason); return;
    default: assertNever(message.data);
  }
});
port.onDisconnect.addListener(() => {
  void chrome.runtime.lastError;
  if (!ended) finish(view ? "cancelled" : "idle");
});
consent.addEventListener("change", () => {
  approve.disabled = !consent.checked || view?.phase !== "choosing" || view.selected === null || !view.permissions;
});
approve.addEventListener("click", () => {
  if (approve.disabled) return;
  approve.disabled = true;
  send({ type: "approve", consent: true });
});
refresh.addEventListener("click", () => { consent.checked = false; send({ type: "refresh" }); });
openGemini.addEventListener("click", () => { consent.checked = false; send({ type: "open_gemini" }); });
grant.addEventListener("click", async () => {
  grant.disabled = true;
  try {
    const allowed = await chrome.permissions.request({ origins: [...GOOGLE_HOSTS, `${__PORTAL_ORIGIN__}/*`] });
    if (allowed) send({ type: "refresh" });
  } catch {
    finish("unavailable");
  } finally { grant.disabled = false; }
});
cancel.addEventListener("click", () => { if (ended) window.close(); else send({ type: "cancel" }); });
document.addEventListener("keydown", (event) => {
  if (event.key === "Escape") { event.preventDefault(); if (ended) window.close(); else send({ type: "cancel" }); }
});
window.addEventListener("pagehide", () => { clearInterval(keepalive); port.disconnect(); });
