import { createHash } from "node:crypto";
import type { Frame, Page } from "playwright-core";

export const SYNTHETIC_GAIA = "111111111111111111111";
export const SYNTHETIC_OTHER_GAIA = "222222222222222222222";
export const SYNTHETIC_DIGEST = createHash("sha256").update(SYNTHETIC_GAIA).digest("hex");
export const SYNTHETIC_TOKEN = "gemini-web:v1:" + Buffer.from(JSON.stringify({
  cookie: "NEW_COOKIE=SYNTHETIC-extra; SID=SYNTHETIC-session", auth_user: 2,
})).toString("base64url");
export const portalHtml = '<!doctype html><html lang="en"><title>SYNTHETIC portal</title><body><h1>SYNTHETIC portal</h1></body></html>';
export function googleHtml(url: string): string {
  const gaia = new URL(url).pathname.startsWith("/u/7") ? SYNTHETIC_OTHER_GAIA : SYNTHETIC_GAIA;
  return `<!doctype html><html lang="en"><title>SYNTHETIC Gemini</title><body><h1>SYNTHETIC Gemini</h1><script>globalThis.WIZ_global_data=${JSON.stringify({ S06Grb: gaia, W3Yyqf: gaia, qDCSke: gaia })}</script></body></html>`;
}

export function connectPortal(page: Page | Frame, options: {
  readonly extensionId: string; readonly state: string; readonly name?: string;
  readonly expected?: { readonly account_sha256: string; readonly auth_user: number };
}) {
  return page.evaluateHandle(({ options, token, digest }) => {
    const port = chrome.runtime.connect(options.extensionId, { name: options.name ?? "gemini-web-login" });
    const events: string[] = [];
    const result = {
      events, disconnected: false, valid: false,
      send: (type: string, state = options.state) => port.postMessage({ type, state }),
      disconnect: () => port.disconnect(),
    };
    port.onMessage.addListener((raw: unknown) => {
      if (typeof raw !== "object" || raw === null || !("type" in raw) || !("state" in raw)) return;
      if (raw.state !== options.state) { result.events.push("invalid_state"); return; }
      if (raw.type === "ready" && Object.keys(raw).length === 2) result.events.push("ready");
      if (raw.type === "session") {
        result.valid = "token" in raw && raw.token === token && "account_sha256" in raw && raw.account_sha256 === digest
          && "auth_user" in raw && raw.auth_user === 2
          && Object.keys(raw).sort().join(",") === "account_sha256,auth_user,state,token,type";
        result.events.push("session");
      }
    });
    port.onDisconnect.addListener(() => { void chrome.runtime.lastError; result.disconnected = true; });
    port.postMessage({ type: "begin", state: options.state, ...(options.expected ? { expected_identity: options.expected } : {}) });
    return result;
  }, { options, token: SYNTHETIC_TOKEN, digest: SYNTHETIC_DIGEST });
}
