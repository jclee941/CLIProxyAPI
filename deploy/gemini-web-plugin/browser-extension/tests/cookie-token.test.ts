import { expect, test } from "bun:test";
import { CaptureError, captureGeminiSession } from "../capture.ts";
import { FIRST_PARTY, FakeChrome, SELECTED_STORE, SELECTED_TAB, cookie } from "./fake-chrome.ts";

test.each([
  { name: "" }, { name: "bad name" }, { name: "bad=name" }, { name: "bad;name" },
  { name: "bad\r\nname" }, { value: "bad\r\nInjected: value" }, { value: "bad;SID=injected" },
  { value: "bad\u0000value" }, { value: "bad\u007fvalue" }, { value: "non-ascii-\u00e9" },
  { value: "surrogate-\ud800" }, { value: "bad\\escape" }, { value: '"quoted"' },
  { value: "unquoted space" }, { value: "comma,value" },
  { name: "SID\n" }, { value: "SYNTHETIC-trailing-newline\n" },
  { name: "SID\r" }, { value: "SYNTHETIC-trailing-crlf\r\n" },
])("rejects malformed header components rather than sanitizing them: %#", async (patch) => {
  const browser = new FakeChrome();
  browser.jar = [cookie(patch)];

  const outcome = captureGeminiSession(browser, { tabId: SELECTED_TAB });

  await expect(outcome).rejects.toMatchObject({ code: "cookie_header_invalid" });
  expect(browser.calls.filter((call) => call.method === "cookies")).toHaveLength(2);
});

test("rejects unsupported opaque partition metadata instead of stripping it", async () => {
  const browser = new FakeChrome();
  Object.assign(browser.selectedDocument.partitionKey, { nonce: "SYNTHETIC-opaque" });

  const outcome = captureGeminiSession(browser, { tabId: SELECTED_TAB });

  await expect(outcome).rejects.toMatchObject({ code: "cookie_scope_unsupported" });
  expect(browser.calls.some((call) => call.method === "cookies")).toBe(false);
});

test.each([
  cookie({ value: "SYNTHETIC-conflict", domain: "gemini.google.com", hostOnly: true }),
  cookie({ domain: "gemini.google.com", hostOnly: true }),
  cookie(),
])("rejects duplicate names instead of scoring or deduplicating their scopes: %#", async (duplicate) => {
  const browser = new FakeChrome();
  browser.jar = [cookie(), duplicate];

  const outcome = captureGeminiSession(browser, { tabId: SELECTED_TAB });

  await expect(outcome).rejects.toMatchObject({ code: "cookie_scope_unsupported" });
});

test.each([
  "/u/2", "/u/2/app", "/u/2/app/synthetic-conversation",
  "/u/2/_/BardChatUi/data/batchexecute",
  "/u/2/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate",
])("rejects a path-scoped cookie applicable only to part of the v1 surface: %s", async (path) => {
  const browser = new FakeChrome();
  browser.jar.push(cookie({ name: "PATH_SCOPED", path }));

  const outcome = captureGeminiSession(browser, { tabId: SELECTED_TAB });

  await expect(outcome).rejects.toMatchObject({ code: "cookie_scope_unsupported" });
});

test("rejects an applicable partitioned cookie instead of omitting it", async () => {
  const browser = new FakeChrome();
  browser.jar.push(cookie({ name: "PARTITIONED", partitionKey: { ...FIRST_PARTY } }));

  const outcome = captureGeminiSession(browser, { tabId: SELECTED_TAB });

  await expect(outcome).rejects.toMatchObject({ code: "cookie_scope_unsupported" });
  expect(browser.calls.filter((call) => call.method === "cookies")).toHaveLength(1);
});

test.each([{}, { topLevelSite: "" }, { topLevelSite: "null" },
  { topLevelSite: "https://google.com" }, { topLevelSite: "https://google.com", hasCrossSiteAncestor: true },
])("rejects an opaque or incomplete document partition before cookies: %#", async (partitionKey) => {
  const browser = new FakeChrome();
  browser.selectedDocument.partitionKey = partitionKey;

  const outcome = captureGeminiSession(browser, { tabId: SELECTED_TAB });

  await expect(outcome).rejects.toMatchObject({ code: "cookie_scope_unsupported" });
  expect(browser.calls.some((call) => call.method === "cookies")).toBe(false);
});

test.each([
  cookie({ storeId: "other-store" }), cookie({ domain: "example.test", hostOnly: true }),
  cookie({ domain: "google.com", hostOnly: true }), cookie({ partitionKey: {} }),
])("rejects out-of-scope cookie results at the adapter boundary: %#", async (entry) => {
  const browser = new FakeChrome();
  browser.afterCall = (call) => {
    if (call.method === "cookies" && call.query.partitionKey !== undefined) browser.cookieResults = [entry];
  };

  const outcome = captureGeminiSession(browser, { tabId: SELECTED_TAB });

  await expect(outcome).rejects.toBeInstanceOf(CaptureError);
});

test("rejects empty cookie results when the page still shows an identity", async () => {
  const browser = new FakeChrome();
  browser.jar = [];

  const outcome = captureGeminiSession(browser, { tabId: SELECTED_TAB });

  await expect(outcome).rejects.toMatchObject({ code: "cookie_header_invalid" });
});

test("fails closed when cookies rotate between required URL reads", async () => {
  const browser = new FakeChrome();
  browser.afterCall = (call) => {
    if (call.method === "cookies" && call.query.partitionKey === undefined) {
      browser.jar = [cookie({ value: "SYNTHETIC-rotated" })];
    }
  };

  const outcome = captureGeminiSession(browser, { tabId: SELECTED_TAB });

  await expect(outcome).rejects.toMatchObject({ code: "cookies_changed" });
});

test("matches the Go RawURLEncoding JSON example without extra fields or padding", async () => {
  const browser = new FakeChrome();
  browser.jar = [cookie({ value: "synthetic-original" })];

  const captured = await captureGeminiSession(browser, { tabId: SELECTED_TAB });

  expect(captured.token).toBe("gemini-web:v1:eyJjb29raWUiOiJTSUQ9c3ludGhldGljLW9yaWdpbmFsIiwiYXV0aF91c2VyIjoyfQ");
  expect(Object.keys(captured).sort()).toEqual(["account_sha256", "auth_user", "token"]);
});

test("preserves valid ASCII bytes and embedded equals signs without URL-encoding cookies", async () => {
  const browser = new FakeChrome();
  browser.jar = [cookie({ value: "SYNTHETIC=+/&<>%~!#$'()*-.:?@[]^_`{|}" })];

  const captured = await captureGeminiSession(browser, { tabId: SELECTED_TAB });

  expect(JSON.parse(Buffer.from(captured.token.slice(14), "base64url").toString("utf8"))).toEqual({
    cookie: "SID=SYNTHETIC=+/&<>%~!#$'()*-.:?@[]^_`{|}", auth_user: 2,
  });
});

test.each([[24534, true], [24535, false], [32768, false]] as const)(
  "enforces the total token byte cap when the cookie value has %i bytes", async (valueLength, accepted) => {
    const browser = new FakeChrome();
    browser.jar = [cookie({ value: "x".repeat(valueLength) })];

    const outcome = captureGeminiSession(browser, { tabId: SELECTED_TAB });

    if (accepted) {
      const captured = await outcome;
      expect(new TextEncoder().encode(captured.token)).toHaveLength(32768);
    } else {
      await expect(outcome).rejects.toMatchObject({ code: "token_too_large" });
      expect(browser.calls.filter((call) => call.method === "cookies")).toHaveLength(2);
    }
  },
);

test("queries only the resolved store when the default execution store differs", async () => {
  const browser = new FakeChrome();

  await captureGeminiSession(browser, { tabId: SELECTED_TAB });

  expect(browser.calls.filter((call) => call.method === "cookies")
    .every((call) => call.query.storeId === SELECTED_STORE)).toBe(true);
});
