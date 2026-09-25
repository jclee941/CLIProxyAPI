import { expect, test } from "bun:test";
import { captureWithSignal, listCandidates, verifyPortalDocument } from "../browser-runtime.ts";
import { FakeChrome, SELECTED_TAB } from "./fake-chrome.ts";

test("stops every later Chrome read when cancellation occurs during capture", async () => {
  const browser = new FakeChrome();
  const abort = new AbortController();
  browser.afterCall = (call) => { if (call.method === "stores") abort.abort(); };

  const outcome = captureWithSignal(browser, { tabId: SELECTED_TAB }, abort.signal);

  await expect(outcome).rejects.toMatchObject({ code: "capture_unavailable" });
  expect(browser.calls.map((call) => call.method)).toEqual(["tab", "stores"]);
});

test("lists only selectable Gemini tabs with a unique store and no page access", async () => {
  const browser = new FakeChrome();
  const queries: chrome.tabs.QueryInfo[] = [];
  const source = { cookies: browser.cookies, tabs: { query: async (query: chrome.tabs.QueryInfo) => {
    queries.push(query);
    return [{ ...browser.selected, title: "SYNTHETIC-secret-title" }, { ...browser.other, frozen: true }];
  } } };

  const choices = await listCandidates(source);

  expect(choices).toEqual([{ tabId: SELECTED_TAB, storeId: "1", url: browser.selected.url, authUser: 2 }]);
  expect(queries).toEqual([{ url: "https://gemini.google.com/*" }]);
  expect(browser.calls.map((call) => call.method)).toEqual(["stores"]);
});

test("rejects portal history navigation at its exact document ID", async () => {
  const sender = { url: "https://cliproxy.jclee.me/v0/resource/plugins/gemini-web/index",
    origin: "https://cliproxy.jclee.me", tab: { id: 10 }, frameId: 3, documentId: "SYNTHETIC-portal" };
  const requests: chrome.scripting.ScriptInjection<[], string>[] = [];
  const scripting = { executeScript: async (injection: chrome.scripting.ScriptInjection<[], string>) => {
    requests.push(injection);
    return [{ documentId: sender.documentId, frameId: 3, result: sender.origin + "/other" }];
  } };

  const authorized = await verifyPortalDocument(scripting, sender);

  expect(authorized).toBe(false);
  expect(requests[0]?.target).toEqual({ tabId: 10, documentIds: [sender.documentId] });
});
