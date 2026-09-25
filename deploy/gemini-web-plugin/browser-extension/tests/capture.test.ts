import { expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { CaptureError, captureGeminiSession } from "../capture.ts";
import { FakeChrome, GAIA, OTHER_GAIA, OTHER_STORE, OTHER_TAB, SELECTED_STORE, SELECTED_TAB, identity } from "./fake-chrome.ts";

export const chromeTypeCompatibility = (browser: typeof chrome, tabId: number) =>
  captureGeminiSession(browser, { tabId });

test("captures the complete selected session when another profile's Gemini tab is active", async () => {
  const browser = new FakeChrome();
  const expectedHeader = "EMPTY=; HOST_COOKIE=SYNTHETIC-gemini-host; NEW_SESSION_COOKIE=SYNTHETIC-not-in-vendor-list; SAPISID=SYNTHETIC-selected-sapi; SID=SYNTHETIC-selected-session";

  const captured = await captureGeminiSession(browser, { tabId: SELECTED_TAB });

  expect(captured).toEqual({
    token: "gemini-web:v1:" + Buffer.from(JSON.stringify({ cookie: expectedHeader, auth_user: 2 }), "utf8").toString("base64url"),
    account_sha256: createHash("sha256").update(GAIA, "utf8").digest("hex"),
    auth_user: 2,
  });
});

test("touches only the selected tab and store when other tabs and partitions exist", async () => {
  const browser = new FakeChrome();

  await captureGeminiSession(browser, { tabId: SELECTED_TAB });

  expect(browser.calls.filter((call) => call.method === "tab" || call.method === "script")
    .every((call) => call.tabId === SELECTED_TAB)).toBe(true);
  const queries = browser.calls.filter((call) => call.method === "cookies").map((call) => call.query);
  expect(queries.length).toBeGreaterThan(0);
  expect(queries.every((query) => query.storeId === SELECTED_STORE
    && query.url?.startsWith("https://gemini.google.com/u/2/")
    && !query.name && !query.domain && !query.path && query.session === undefined)).toBe(true);
  expect(browser.forbiddenReads).toEqual([]);
});

test("uses the explicit auth user when the same store has a different active account", async () => {
  const browser = new FakeChrome();
  browser.stores = [{ id: SELECTED_STORE, tabIds: [OTHER_TAB, SELECTED_TAB] }];

  const captured = await captureGeminiSession(browser, { tabId: SELECTED_TAB });

  expect(captured.auth_user).toBe(2);
  expect(captured.account_sha256).toBe(createHash("sha256").update(GAIA).digest("hex"));
  expect(browser.calls.filter((call) => call.method === "script").every((call) => call.tabId === SELECTED_TAB)).toBe(true);
});

test.each([
  ["/", 0], ["/app", 0], ["/u/0/app", 0], ["/u/2", 2], ["/u/31/app", 31],
  ["/u/9007199254740991/app", Number.MAX_SAFE_INTEGER],
  ["/app?authuser=2", 2], ["/u/2/app?authuser=2", 2], ["/app?hl=en", 0], ["/app?", 0],
] as const)("selects canonical index from %s when the document agrees", async (path, authUser) => {
  const browser = new FakeChrome();
  browser.selected.url = `https://gemini.google.com${path}`;
  browser.selectedDocument.url = browser.selected.url;

  const captured = await captureGeminiSession(browser, { tabId: SELECTED_TAB });

  expect(captured.auth_user).toBe(authUser);
});

test.each([
  "http://gemini.google.com/app", "https://gemini.google.com.evil.test/app",
  "https://accounts.google.com/app", "https://gemini.google.com:8443/app",
  "https://gemini.google.com:443/app", "https://user@gemini.google.com/app",
  "https://gemini.google.com./app", "https://GEMINI.google.com/app",
  "https://gemini.google.com/u/2/app?authuser=7", "https://gemini.google.com/app?authuser=0&authuser=2",
  "https://gemini.google.com/app?%61uthuser=2", "https://gemini.google.com/app#authuser=2",
  "https://gemini.google.com/u", "https://gemini.google.com/u/", "https://gemini.google.com/u/02/app",
  "https://gemini.google.com/u/-1/app", "https://gemini.google.com/u/+2/app",
  "https://gemini.google.com/u/2.0/app", "https://gemini.google.com/u/2e0/app",
  "https://gemini.google.com/u/9007199254740992/app", "https://gemini.google.com/u/%32/app",
  "https://gemini.google.com/u/2/u/7/app", "https://gemini.google.com/app/u/7",
  "https://gemini.google.com/u/2/../7/app", "https://gemini.google.com//u/2/app",
  "https://gemini.google.com/U/2/app", "not-a-url",
])("stops before store or page access when the selected URL is ineligible: %s", async (url) => {
  const browser = new FakeChrome();
  browser.selected.url = url;

  const outcome = captureGeminiSession(browser, { tabId: SELECTED_TAB });

  await expect(outcome).rejects.toMatchObject({ code: "invalid_tab" });
  expect(browser.calls).toEqual([{ method: "tab", tabId: SELECTED_TAB }]);
});

test.each([-1, 1.5, Number.NaN, Number.POSITIVE_INFINITY, Number.MAX_SAFE_INTEGER + 1])(
  "makes no Chrome calls when tab ID is invalid: %s", async (tabId) => {
    const browser = new FakeChrome();

    const outcome = captureGeminiSession(browser, { tabId });

    await expect(outcome).rejects.toMatchObject({ code: "invalid_selection" });
    expect(browser.calls).toEqual([]);
  },
);

test.each([
  { status: "loading" as const }, { pendingUrl: "https://gemini.google.com/u/7/app" },
  { discarded: true }, { frozen: true }, { id: OTHER_TAB }, { url: "" },
])("stops before store access when the tab is unstable: %j", async (patch) => {
  const browser = new FakeChrome();
  Object.assign(browser.selected, patch);

  const outcome = captureGeminiSession(browser, { tabId: SELECTED_TAB });

  await expect(outcome).rejects.toMatchObject({ code: "invalid_tab" });
  expect(browser.calls).toHaveLength(1);
});

test.each([
  [], [{ id: SELECTED_STORE, tabIds: [OTHER_TAB] }],
  [{ id: "", tabIds: [SELECTED_TAB] }],
  [{ id: SELECTED_STORE, tabIds: [SELECTED_TAB] }, { id: OTHER_STORE, tabIds: [SELECTED_TAB] }],
].map((stores) => ({ stores })))("stops before page or cookie reads when store membership is ambiguous: %j", async ({ stores }) => {
  const browser = new FakeChrome();
  browser.stores = structuredClone(stores);

  const outcome = captureGeminiSession(browser, { tabId: SELECTED_TAB });

  await expect(outcome).rejects.toMatchObject({ code: "ambiguous_store" });
  expect(browser.calls.map((call) => call.method)).toEqual(["tab", "stores"]);
});

test.each([undefined, null, {}, { S06Grb: GAIA }, { S06Grb: GAIA, W3Yyqf: GAIA },
  identity("1".repeat(20)), identity("1".repeat(22)), identity("a".repeat(21)),
  identity(GAIA + "\n"), identity(GAIA + "\r\n"),
  { S06Grb: Number(GAIA), W3Yyqf: GAIA, qDCSke: GAIA },
  { S06Grb: GAIA, W3Yyqf: OTHER_GAIA, qDCSke: GAIA },
  { S06Grb: GAIA, W3Yyqf: GAIA, qDCSke: OTHER_GAIA },
])("rejects before cookies when Gaia evidence is missing or inconsistent: %#", async (wiz) => {
  const browser = new FakeChrome();
  browser.selectedDocument.wiz = wiz;

  const outcome = captureGeminiSession(browser, { tabId: SELECTED_TAB });

  await expect(outcome).rejects.toMatchObject({ code: "identity_unavailable" });
  expect(browser.calls.some((call) => call.method === "cookies")).toBe(false);
});

test("does not inspect any other WIZ fields when the identity fields exist", async () => {
  const browser = new FakeChrome();
  const reads: string[] = [];
  browser.selectedDocument.wiz = new Proxy(identity(GAIA), {
    get: (target, property: string) => {
      reads.push(property);
      if (!["S06Grb", "W3Yyqf", "qDCSke"].includes(property)) throw new Error("SYNTHETIC-forbidden-field");
      return target[property];
    },
  });

  await captureGeminiSession(browser, { tabId: SELECTED_TAB });

  expect(new Set(reads)).toEqual(new Set(["S06Grb", "W3Yyqf", "qDCSke"]));
  expect(browser.forbiddenReads).toEqual([]);
});

test.each(["account", "auth_user", "malformed_digest", "malformed_index"] as const)(
  "rejects a mismatched expected identity before cookies: %s", async (kind) => {
    const browser = new FakeChrome();
    const expectedIdentity = {
      account_sha256: createHash("sha256").update(kind === "account" ? OTHER_GAIA : GAIA).digest("hex"),
      auth_user: kind === "auth_user" ? 7 : 2,
    };
    if (kind === "malformed_digest") expectedIdentity.account_sha256 = "invalid";
    if (kind === "malformed_index") expectedIdentity.auth_user = -1;

    const outcome = captureGeminiSession(browser, { tabId: SELECTED_TAB, expectedIdentity });

    await expect(outcome).rejects.toBeInstanceOf(CaptureError);
    expect(browser.calls.some((call) => call.method === "cookies")).toBe(false);
    if (kind.startsWith("malformed")) expect(browser.calls).toEqual([]);
    if (kind === "auth_user") expect(browser.calls).toHaveLength(1);
  },
);

test("accepts a matching expected identity when capture stays stable", async () => {
  const browser = new FakeChrome();
  const expectedIdentity = { account_sha256: createHash("sha256").update(GAIA).digest("hex"), auth_user: 2 };

  const captured = await captureGeminiSession(browser, { tabId: SELECTED_TAB, expectedIdentity });

  expect({ account_sha256: captured.account_sha256, auth_user: captured.auth_user }).toEqual(expectedIdentity);
});

test("rejects a digest ending in a newline without calling Chrome", async () => {
  const browser = new FakeChrome();
  const expectedIdentity = { account_sha256: createHash("sha256").update(GAIA).digest("hex") + "\n", auth_user: 2 };

  const outcome = captureGeminiSession(browser, { tabId: SELECTED_TAB, expectedIdentity });

  await expect(outcome).rejects.toMatchObject({ code: "invalid_selection" });
  expect(browser.calls).toEqual([]);
});

test.each([
  [], [{ documentId: "", frameId: 0, result: { url: "https://gemini.google.com/u/2/app/synthetic-conversation", ...identity(GAIA) } }],
  [{ documentId: "SYNTHETIC-document", frameId: 1, result: { url: "https://gemini.google.com/u/2/app/synthetic-conversation", ...identity(GAIA) } }],
  [{ documentId: "SYNTHETIC-document", frameId: 0 }],
  [{ documentId: "SYNTHETIC-document", frameId: 0, result: { url: "https://gemini.google.com/u/7/app", ...identity(GAIA) } }],
  [{ documentId: "SYNTHETIC-document", frameId: 0, result: null }, { documentId: "SYNTHETIC-other", frameId: 0, result: null }],
].map((results) => ({ results })))("rejects incomplete or ambiguous script results before cookies: %#", async ({ results }) => {
  const browser = new FakeChrome();
  browser.scriptResults = results;

  const outcome = captureGeminiSession(browser, { tabId: SELECTED_TAB });

  await expect(outcome).rejects.toMatchObject({ code: "identity_unavailable" });
  expect(browser.calls.some((call) => call.method === "cookies")).toBe(false);
});

test("does not read WIZ or cookies when the document navigates before injection", async () => {
  const browser = new FakeChrome();
  let reads = 0;
  browser.selectedDocument.wiz = new Proxy(identity(OTHER_GAIA), { get: () => { reads += 1; return OTHER_GAIA; } });
  browser.afterCall = (call) => {
    if (call.method === "stores") browser.selectedDocument.url = "https://accounts.google.com/";
  };

  const outcome = captureGeminiSession(browser, { tabId: SELECTED_TAB });

  await expect(outcome).rejects.toMatchObject({ code: "identity_unavailable" });
  expect(reads).toBe(0);
  expect(browser.calls.some((call) => call.method === "cookies")).toBe(false);
});

test("keeps the original selection when the caller mutates its request during capture", async () => {
  const browser = new FakeChrome();
  const selection = { tabId: SELECTED_TAB, expectedIdentity: {
    account_sha256: createHash("sha256").update(GAIA).digest("hex"), auth_user: 2,
  } };
  browser.afterCall = (call) => {
    if (call.method === "tab") {
      selection.tabId = OTHER_TAB;
      selection.expectedIdentity.auth_user = 7;
      selection.expectedIdentity.account_sha256 = createHash("sha256").update(OTHER_GAIA).digest("hex");
    }
  };

  const captured = await captureGeminiSession(browser, selection);

  expect(captured.auth_user).toBe(2);
  expect(captured.account_sha256).toBe(createHash("sha256").update(GAIA).digest("hex"));
  expect(browser.calls.filter((call) => call.method === "tab").every((call) => call.tabId === SELECTED_TAB)).toBe(true);
});

test("does not coerce a rejected API value into a secret-bearing message", async () => {
  const browser = new FakeChrome();
  let coercions = 0;
  browser.failure = { method: "cookies", error: { toString: () => { coercions += 1; return "SYNTHETIC-secret"; } } };

  const outcome = captureGeminiSession(browser, { tabId: SELECTED_TAB });

  await expect(outcome).rejects.toMatchObject({ code: "capture_unavailable" });
  expect(coercions).toBe(0);
});

const identityChanges: ReadonlyArray<readonly [string, (browser: FakeChrome) => void]> = [
  ["Gaia", (browser) => { browser.selectedDocument.wiz = identity(OTHER_GAIA); }],
  ["partial Gaia", (browser) => { browser.selectedDocument.wiz = { S06Grb: GAIA }; }],
  ["document ID at the same URL", (browser) => { browser.selectedDocument.documentId = "33333333-3333-4333-8333-333333333333"; }],
  ["document URL only", (browser) => { browser.selectedDocument.url = "https://gemini.google.com/u/7/app"; }],
  ["tab URL only", (browser) => { browser.selected.url = "https://gemini.google.com/u/7/app"; }],
  ["same-account path", (browser) => { browser.selected.url = browser.selectedDocument.url = "https://gemini.google.com/u/2/app/changed"; }],
  ["auth user", (browser) => { browser.selected.url = browser.selectedDocument.url = "https://gemini.google.com/u/7/app"; }],
  ["store ID", (browser) => { browser.stores = [{ id: OTHER_STORE, tabIds: [SELECTED_TAB] }]; }],
  ["ambiguous store", (browser) => { browser.stores.push({ id: "new", tabIds: [SELECTED_TAB] }); }],
  ["missing membership", (browser) => { browser.stores = []; }],
  ["pending navigation", (browser) => { browser.selected.pendingUrl = "https://accounts.google.com/"; }],
  ["closed tab", (browser) => { browser.tabsById.delete(SELECTED_TAB); }],
  ["non-main frame", (browser) => { browser.selectedDocument.frameId = 1; }],
  ["partition site", (browser) => { browser.selectedDocument.partitionKey = { topLevelSite: "https://example.test", hasCrossSiteAncestor: true }; }],
];

test.each(identityChanges)("never returns a token when %s changes after cookie reading", async (_name, change) => {
  const browser = new FakeChrome();
  let changed = false;
  browser.afterCall = (call) => {
    if (!changed && call.method === "cookies" && call.query.partitionKey === undefined) {
      changed = true;
      change(browser);
    }
  };

  const outcome = captureGeminiSession(browser, { tabId: SELECTED_TAB });

  await expect(outcome).rejects.toBeInstanceOf(CaptureError);
  expect(changed).toBe(true);
});

test.each(["tab", "stores", "script", "partition", "cookies"] as const)(
  "discards untrusted API errors when %s fails", async (method) => {
    const browser = new FakeChrome();
    browser.failure = { method, error: new Error("SYNTHETIC-secret-cookie-error") };

    const failure: unknown = await captureGeminiSession(browser, { tabId: SELECTED_TAB }).catch((error: unknown) => error);

    expect(failure).toBeInstanceOf(CaptureError);
    if (!(failure instanceof CaptureError)) throw new Error("Expected a safe capture error");
    expect(failure.code).toBe("capture_unavailable");
    expect(String(failure)).toBe("CaptureError: capture_unavailable");
    expect(JSON.stringify(failure)).not.toContain("SYNTHETIC");
    expect(failure.cause).toBeUndefined();
  },
);
