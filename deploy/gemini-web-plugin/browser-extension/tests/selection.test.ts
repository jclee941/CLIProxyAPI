import { expect, test } from "bun:test";
import { captureGeminiSession } from "../capture.ts";
import { FakeChrome, SELECTED_TAB } from "./fake-chrome.ts";

test.each([
  { storeId: "other-store" }, { url: "https://gemini.google.com/u/2/app?hl=ko" },
])("rejects a tab that changed since the user selected it: %#", async (binding) => {
  const browser = new FakeChrome();

  const outcome = captureGeminiSession(browser, { tabId: SELECTED_TAB, ...binding });

  await expect(outcome).rejects.toMatchObject({ code: "identity_changed" });
  expect(browser.calls.some((call) => call.method === "cookies")).toBe(false);
});
