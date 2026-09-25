import { z } from "zod";
import { authUserFromUrl, CaptureError, captureGeminiSession, type CaptureSelection } from "./capture.ts";
import type { ChromeCaptureApi } from "./chrome-adapter.ts";
import type { Candidate } from "./companion.ts";
import { safeIndex, type PortalSender } from "./protocol.ts";

const selectable = z.object({ id: safeIndex, url: z.string(), status: z.literal("complete"),
  discarded: z.literal(false), frozen: z.literal(false), pendingUrl: z.undefined().optional(),
  incognito: z.literal(false).optional() });
type TabSource = {
  readonly tabs: { readonly query: (query: chrome.tabs.QueryInfo) => Promise<readonly unknown[]> };
  readonly cookies: Pick<ChromeCaptureApi["cookies"], "getAllCookieStores">;
};

export async function listCandidates(browser: TabSource): Promise<readonly Candidate[]> {
  const tabs = await browser.tabs.query({ url: "https://gemini.google.com/*" });
  const stores = await browser.cookies.getAllCookieStores();
  const candidates: Candidate[] = [];
  for (const raw of tabs) {
    const tab = selectable.safeParse(raw);
    if (!tab.success) continue;
    const memberships = stores.filter((store) => store.tabIds.includes(tab.data.id));
    const store = memberships[0];
    if (memberships.length !== 1 || !store) continue;
    try {
      candidates.push({ tabId: tab.data.id, url: tab.data.url, storeId: store.id, authUser: authUserFromUrl(tab.data.url) });
    } catch (error: unknown) {
      if (!(error instanceof CaptureError)) throw error;
    }
  }
  return candidates;
}

export function captureWithSignal(browser: ChromeCaptureApi, selection: CaptureSelection, signal: AbortSignal) {
  async function guarded<Value>(operation: () => Promise<Value>): Promise<Value> {
    if (signal.aborted) throw new CaptureError("capture_unavailable");
    const value = await operation();
    if (signal.aborted) throw new CaptureError("capture_unavailable");
    return value;
  }
  return captureGeminiSession({
    tabs: { get: (tabId) => guarded(() => browser.tabs.get(tabId)) },
    cookies: {
      getAllCookieStores: () => guarded(() => browser.cookies.getAllCookieStores()),
      getPartitionKey: (target) => guarded(() => browser.cookies.getPartitionKey(target)),
      getAll: (query) => guarded(() => browser.cookies.getAll(query)),
    },
    scripting: { executeScript: (injection) => guarded(() => browser.scripting.executeScript(injection)) },
  }, selection);
}

export async function verifyPortalDocument(scripting: {
  readonly executeScript: (injection: chrome.scripting.ScriptInjection<[], string>) => Promise<readonly chrome.scripting.InjectionResult<string>[]>;
}, sender: PortalSender): Promise<boolean> {
  const results = await scripting.executeScript({
    target: { tabId: sender.tab.id, documentIds: [sender.documentId] },
    world: "ISOLATED", func: () => location.href,
  });
  const result = results[0];
  return results.length === 1 && result?.documentId === sender.documentId
    && result.frameId === sender.frameId && result.result === sender.url;
}
