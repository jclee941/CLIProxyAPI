import { expect, test } from "bun:test";
import { captureGeminiSession } from "../capture.ts";
import { FakeChrome, SELECTED_TAB } from "./fake-chrome.ts";

test.each([
  ["/app?hl=ko", 0], ["/app?", 0], ["/app#", 0],
  ["/u/2/app?hl=ko#settings", 2], ["/app?hl=ko&authuser=2", 2],
  ["/u/2/app?authuser=2&hl=ko", 2], ["/app?authuser=0", 0],
  ["/app?continue=https%3A%2F%2Fgemini.google.com%2Fapp#login-return", 0],
] as const)("captures without rewriting a benign login-return URL: %s", async (path, authUser) => {
  const browser = new FakeChrome();
  const url = `https://gemini.google.com${path}`;
  browser.selected.url = browser.selectedDocument.url = url;

  const result = await captureGeminiSession(browser, { tabId: SELECTED_TAB });

  expect(result.auth_user).toBe(authUser);
  expect(browser.selected.url).toBe(url);
  expect(browser.calls.find((call) => call.method === "cookies")?.query.url).toBe(url);
});

test.each([
  "?authuser=2&authuser=2", "?authuser=2&authuser=7", "?authuser=7",
  "?%61uthuser=2", "?AuthUser=2", "?authuser=%32", "?authuser=02",
  "?authuser=", "?authuser=user%40example.test", "?authuser=2;authuser=7",
  "#authuser=2", "#?authuser=2", "#%61uthuser=2", "#/u/2/app",
  "?hl=ko#authuser=7",
])("rejects ambiguous account selection before inspecting a store: %s", async (suffix) => {
  const browser = new FakeChrome();
  browser.selected.url = browser.selectedDocument.url = `https://gemini.google.com/u/2/app${suffix}`;

  const outcome = captureGeminiSession(browser, { tabId: SELECTED_TAB });

  await expect(outcome).rejects.toMatchObject({ code: "invalid_tab" });
  expect(browser.calls).toEqual([{ method: "tab", tabId: SELECTED_TAB }]);
});

test.each(["?hl=en#settings", "?hl=ko#changed"])("rejects an exact URL change during capture: %s", async (suffix) => {
  const browser = new FakeChrome();
  browser.selected.url = browser.selectedDocument.url = "https://gemini.google.com/u/2/app?hl=ko#settings";
  browser.afterCall = (call) => {
    if (call.method === "cookies") {
      browser.selected.url = browser.selectedDocument.url = `https://gemini.google.com/u/2/app${suffix}`;
    }
  };

  const outcome = captureGeminiSession(browser, { tabId: SELECTED_TAB });

  await expect(outcome).rejects.toMatchObject({ code: "identity_changed" });
});
