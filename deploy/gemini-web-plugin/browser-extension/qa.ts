import assert from "node:assert/strict";
import { mkdir, mkdtemp, rm } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium, type BrowserContext, type Page, type Route } from "playwright-core";
import { buildExtension } from "./build.ts";
import { PORTAL_PATH } from "./protocol.ts";
import { connectPortal, googleHtml, portalHtml } from "./qa-fixtures.ts";

const root = fileURLToPath(new URL(".", import.meta.url));
const profile = await mkdtemp(join(root, ".qa-profile-"));
const evidence = await mkdtemp(join(root, ".qa-evidence-"));
const shots: string[] = [];
const checks: string[] = [];
const measurements: unknown[] = [];
let context: BrowserContext | undefined;
let extensionId = "";
let version = "";
let permissions: unknown;
let chromeShapes: unknown;
let internalSenders: unknown;
let errorCount = 0;
const consoleErrors: unknown[] = [];
let failureEvidence: unknown;
const server = Bun.serve({ hostname: "127.0.0.1", port: 0, fetch: () => new Response(portalHtml, { headers: { "Content-Type": "text/html" } }) });
const origin = `http://localhost:${server.port}`;
let artifact: Awaited<ReturnType<typeof buildExtension>> | undefined;
async function routeOwnRequests(route: Route): Promise<void> {
  const url = new URL(route.request().url());
  if (url.origin === origin || url.origin === `http://127.0.0.1:${server.port}`
    || (url.protocol === "chrome-extension:" && url.hostname === extensionId
      && ["/popup.html", "/popup.js", "/styles.css", "/tokens.css"].includes(url.pathname))) await route.continue();
  else if (url.origin === "https://gemini.google.com" && route.request().isNavigationRequest()) {
    await route.fulfill({ contentType: "text/html", body: googleHtml(url.href) });
  } else await route.abort();
}

async function screenshot(page: Page, name: string): Promise<void> {
  const measured = await page.evaluate(() => ({ width: innerWidth, height: innerHeight,
    scrollWidth: document.documentElement.scrollWidth,
    secretVisible: /gemini-web:v1:|SYNTHETIC-session|SYNTHETIC-extra/.test(document.body.innerText),
    focus: document.activeElement?.id ?? "", checkedRadios: document.querySelectorAll("input[type=radio]:checked").length,
  }));
  assert.equal(measured.scrollWidth <= measured.width, true);
  assert.equal(measured.secretVisible, false);
  const path = join(evidence, `${name}.png`);
  await page.screenshot({ path, fullPage: true, animations: "disabled" });
  const image = new DataView(await Bun.file(path).arrayBuffer());
  assert.equal(image.getUint32(0), 0x89504e47);
  assert.equal(image.getUint32(16), measured.width);
  measurements.push({ name, ...measured, imageWidth: image.getUint32(16), imageHeight: image.getUint32(20) });
  shots.push(path);
}

try {
  artifact = await buildExtension(origin);
  const launchOptions: Parameters<typeof chromium.launchPersistentContext>[1] = {
    channel: "chromium", executablePath: Bun.argv[2] ?? chromium.executablePath(), headless: true,
    args: [`--disable-extensions-except=${artifact.directory}`, `--load-extension=${artifact.directory}`,
      "--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE localhost, EXCLUDE 127.0.0.1"],
  };
  context = await chromium.launchPersistentContext(profile, launchOptions);
  context.on("page", (page) => page.on("pageerror", () => { errorCount += 1; }));
  context.on("console", (message) => { if (message.type() === "error") consoleErrors.push(message.location()); });
  await context.route("**/*", routeOwnRequests);
  const worker = context.serviceWorkers()[0] ?? await context.waitForEvent("serviceworker");
  const observedSenders = await worker.evaluateHandle(() => {
    const senders: unknown[] = [];
    chrome.runtime.onConnect.addListener((port) => {
      senders.push({ name: port.name, id: port.sender?.id, url: port.sender?.url, origin: port.sender?.origin,
        tabId: port.sender?.tab?.id, frameId: port.sender?.frameId, documentId: port.sender?.documentId });
    });
    return senders;
  });
  extensionId = new URL(worker.url()).hostname;
  assert.match(extensionId, /^[a-p]{32}$/);
  version = context.browser()?.version() ?? "unreported";
  permissions = await worker.evaluate(() => chrome.permissions.getAll());
  assert.equal(await worker.evaluate(() => chrome.permissions.contains({ permissions: ["cookies", "tabs", "scripting", "storage"], origins: ["https://*.google.com/*"] })), true);
  const installed = await worker.evaluate(() => chrome.runtime.getManifest());
  assert.equal(installed.manifest_version, 3);
  assert.deepEqual(installed.externally_connectable?.matches, [origin + PORTAL_PATH]);
  checks.push("installed-id-manifest-permissions");

  const portal = await context.newPage();
  await portal.goto(origin + PORTAL_PATH);
  const emptyPopup = context.waitForEvent("page");
  const emptyFlow = await connectPortal(portal, { extensionId, state: "1".repeat(64) });
  const empty = await emptyPopup;
  await empty.waitForFunction(() => document.getElementById("approve")?.hidden || document.getElementById("request")?.hidden === false);
  internalSenders = await observedSenders.jsonValue();
  await screenshot(empty, "initial-binding");
  await empty.locator("#empty").waitFor({ state: "visible" });
  await screenshot(empty, "empty");
  assert.equal(await empty.locator("#approve").isDisabled(), true);
  const openedPage = context.waitForEvent("page");
  await empty.locator("#open-gemini").click();
  const opened = await openedPage;
  await opened.waitForURL("https://gemini.google.com/app");
  assert.deepEqual(await emptyFlow.evaluate((flow) => flow.events), ["ready"]);
  await empty.locator("#cancel").click();
  await portal.waitForFunction((flow) => flow.disconnected, emptyFlow);
  checks.push("empty-open-google-without-capture-cancel");

  const other = await context.newPage();
  await other.goto("https://gemini.google.com/u/7/app");
  const selected = await context.newPage();
  await selected.goto("https://gemini.google.com/u/2/app?hl=ko#login-return");
  await context.addCookies([
    { name: "SID", value: "SYNTHETIC-session", domain: ".google.com", path: "/", httpOnly: true, secure: true, sameSite: "Lax" },
    { name: "NEW_COOKIE", value: "SYNTHETIC-extra", domain: ".google.com", path: "/", httpOnly: false, secure: true, sameSite: "Lax" },
  ]);
  const selectedId = await worker.evaluate(async () => (await chrome.tabs.query({ url: "https://gemini.google.com/u/2/*" }))[0]?.id);
  assert.equal(typeof selectedId, "number");
  if (selectedId === undefined) throw new Error("qa_selected_tab_missing");
  chromeShapes = await worker.evaluate(async (tabId) => {
    const tab = await chrome.tabs.get(tabId);
    const documents = await chrome.scripting.executeScript({ target: { tabId, frameIds: [0] }, func: () => location.href });
    const documentId = documents[0]?.documentId;
    if (!documentId) throw new Error("qa_document_id_missing");
    const partition = await chrome.cookies.getPartitionKey({ tabId, frameId: 0, documentId });
    return { frozen: tab.frozen, discarded: tab.discarded, status: tab.status, partition };
  }, selectedId);
  assert.deepEqual(chromeShapes, { frozen: false, discarded: false, status: "complete",
    partition: { partitionKey: { topLevelSite: "https://google.com", hasCrossSiteAncestor: false } } });
  checks.push("real-chrome-frozen-and-partition-shapes");

  const popupPromise = context.waitForEvent("page");
  const flow = await connectPortal(portal, { extensionId, state: "2".repeat(64) });
  const popup = await popupPromise;
  await popup.locator(`input[value="${selectedId}"]`).waitFor();
  assert.equal(await popup.locator("input[type=radio]:checked").count(), 0);
  assert.equal(await popup.locator("#approve").isDisabled(), true);
  for (const width of [375, 768, 1280]) {
    await popup.setViewportSize({ width, height: 900 });
    for (const colorScheme of ["light", "dark"] as const) {
      await popup.emulateMedia({ colorScheme });
      await screenshot(popup, `choices-${width}-${colorScheme}`);
    }
  }
  await popup.setViewportSize({ width: 520, height: 720 });
  await popup.emulateMedia({ colorScheme: "light" });
  await popup.locator(`input[value="${selectedId}"]`).check();
  assert.equal(await popup.locator("#approve").isDisabled(), true);
  await popup.locator("#consent").focus();
  await popup.keyboard.press("Space");
  assert.equal(await popup.locator("#approve").isEnabled(), true);
  await screenshot(popup, "consented-keyboard-focus");
  await popup.locator("#approve").hover();
  await screenshot(popup, "approve-hover");
  await popup.emulateMedia({ reducedMotion: "reduce" });
  assert.equal(await popup.locator("#approve").evaluate((element) => getComputedStyle(element).transitionDuration), "0s");
  await popup.locator("#approve").click();
  await portal.waitForFunction((current) => current.events.includes("session") || current.disconnected, flow);
  assert.equal(await flow.evaluate((current) => current.valid), true);
  assert.deepEqual(await flow.evaluate((current) => current.events), ["ready", "session"]);
  await popup.locator("#request").waitFor({ state: "hidden" });
  await screenshot(popup, "delivered-awaiting-ack");
  await flow.evaluate((current) => current.send("ack"));
  await portal.waitForFunction((current) => current.disconnected, flow);
  await screenshot(popup, "acknowledged");
  checks.push("explicit-tab-consent-full-session-original-port-ack", "responsive-light-dark-keyboard-reduced-motion");

  const replay = await connectPortal(portal, { extensionId, state: "2".repeat(64) });
  await portal.waitForFunction((current) => current.disconnected, replay);
  assert.deepEqual(await replay.evaluate((current) => current.events), []);
  checks.push("replay-rejected");

  for (const variant of ["wrong-state", "wrong-identity", "portal-history", "disconnect", "tab-navigation"] as const) {
    await portal.goto(origin + PORTAL_PATH);
    const nextPopup = context.waitForEvent("page");
    const nextFlow = await connectPortal(portal, { extensionId,
      state: Buffer.from(variant).toString("hex").padEnd(64, "0"),
      ...(variant === "wrong-identity" ? { expected: { account_sha256: "f".repeat(64), auth_user: 2 } } : {}),
    });
    const next = await nextPopup;
    await next.locator(`input[value="${selectedId}"]`).check();
    await next.locator("#consent").check();
    switch (variant) {
      case "wrong-state": await nextFlow.evaluate((current) => current.send("cancel", "0".repeat(64))); break;
      case "disconnect": await nextFlow.evaluate((current) => current.disconnect()); break;
      case "portal-history": await portal.evaluate(() => history.pushState(null, "", "/wrong-path")); await next.locator("#approve").click(); break;
      case "tab-navigation": await selected.goto("https://gemini.google.com/u/2/app?hl=en"); await next.locator("#approve").click(); break;
      case "wrong-identity": await next.locator("#approve").click(); break;
    }
    await next.locator("#request").waitFor({ state: "hidden" });
    assert.deepEqual(await nextFlow.evaluate((current) => current.events), ["ready"]);
    await screenshot(next, variant);
    await next.close();
    checks.push(variant);
  }

  for (const url of [origin + "/wrong-path", `http://127.0.0.1:${server.port}${PORTAL_PATH}`]) {
    const rejectedPage = await context.newPage();
    await rejectedPage.goto(url);
    const available = await rejectedPage.evaluate(() => typeof chrome !== "undefined" && typeof chrome.runtime?.connect === "function");
    if (available) {
      const rejected = await connectPortal(rejectedPage, { extensionId, state: "9".repeat(64) });
      await rejectedPage.waitForFunction((current) => current.disconnected, rejected);
      assert.deepEqual(await rejected.evaluate((current) => current.events), []);
    }
    await rejectedPage.close();
  }
  await portal.goto(origin + PORTAL_PATH);
  const wrongName = await connectPortal(portal, { extensionId, state: "8".repeat(64), name: "wrong-name" });
  await portal.waitForFunction((current) => current.disconnected, wrongName);
  assert.deepEqual(await wrongName.evaluate((current) => current.events), []);
  const idle = await context.newPage();
  await idle.goto(`chrome-extension://${extensionId}/popup.html`);
  await idle.locator("#approve").waitFor({ state: "hidden" });
  await screenshot(idle, "unbound-popup");
  checks.push("wrong-origin-path-name-rejected", "unbound-popup-cannot-approve");
  for (const scope of ["partition", "path"] as const) {
    const partitionKey = { topLevelSite: "https://google.com", hasCrossSiteAncestor: false };
    await worker.evaluate(async ({ scope, partitionKey }) => { await chrome.cookies.set({
      url: "https://gemini.google.com/u/2/app", domain: ".google.com", name: "QA_SCOPE",
      value: "SYNTHETIC-unsupported-scope", secure: true, sameSite: "no_restriction",
      path: scope === "path" ? "/u/2" : "/", ...(scope === "partition" ? { partitionKey } : {}),
    }); }, { scope, partitionKey });
    const scopedPopup = context.waitForEvent("page");
    const scopedFlow = await connectPortal(portal, { extensionId, state: scope === "path" ? "a".repeat(64) : "b".repeat(64) });
    const scoped = await scopedPopup;
    await scoped.locator(`input[value="${selectedId}"]`).check();
    await scoped.locator("#consent").check();
    await scoped.locator("#approve").click();
    await scoped.locator("#error").waitFor({ state: "visible" });
    assert.deepEqual(await scopedFlow.evaluate((current) => current.events), ["ready"]);
    await screenshot(scoped, `unsupported-${scope}-cookie`);
    await scoped.close();
    await worker.evaluate(async ({ scope, partitionKey }) => { await chrome.cookies.remove({
      url: "https://gemini.google.com/u/2/app", name: "QA_SCOPE", ...(scope === "partition" ? { partitionKey } : {}),
    }); }, { scope, partitionKey });
    checks.push(`real-${scope}-cookie-rejected`);
  }
  const embeddedHost = await context.newPage();
  await embeddedHost.goto(origin + "/synthetic-host");
  await embeddedHost.setContent(`<iframe src="${origin}${PORTAL_PATH}"></iframe>`);
  const frame = await embeddedHost.locator("iframe").elementHandle().then((element) => element?.contentFrame());
  if (!frame) throw new Error("qa_frame_missing");
  await frame.waitForURL(origin + PORTAL_PATH);
  const framePopup = context.waitForEvent("page");
  const frameFlow = await connectPortal(frame, { extensionId, state: "c".repeat(64) });
  const frameConsent = await framePopup;
  await frameConsent.locator(`input[value="${selectedId}"]`).check();
  await frameConsent.locator("#consent").check();
  await frameConsent.locator("#approve").click();
  await frame.waitForFunction((current) => current.events.includes("session") || current.disconnected, frameFlow);
  assert.equal(await frameFlow.evaluate((current) => current.valid), true);
  await frameFlow.evaluate((current) => current.send("ack"));
  await frame.waitForFunction((current) => current.disconnected, frameFlow);
  checks.push("resource-iframe-document-authority");
  assert.deepEqual(consoleErrors, []);
  await context.close();
  context = await chromium.launchPersistentContext(profile, launchOptions);
  await context.route("**/*", routeOwnRequests);
  const restartedPortal = await context.newPage();
  await restartedPortal.goto(origin + PORTAL_PATH);
  const afterReload = await connectPortal(restartedPortal, { extensionId, state: "2".repeat(64) });
  await restartedPortal.waitForFunction((current) => current.disconnected || current.events.includes("ready"), afterReload);
  assert.deepEqual(await afterReload.evaluate((current) => current.events), []);
  checks.push("replay-rejected-after-browser-restart");
  assert.equal(errorCount, 0);
} catch (failure: unknown) {
  const pages = context?.pages() ?? [];
  const popups = pages.filter((page) => page.url().startsWith(`chrome-extension://${extensionId}/`));
  for (const [index, page] of popups.entries()) await screenshot(page, `failure-${index}`);
  failureEvidence = {
    popups: await Promise.all(popups.map((page) => page.evaluate(() => ({ status: document.getElementById("status")?.textContent,
      error: document.getElementById("error")?.textContent, choices: document.querySelectorAll("input[type=radio]").length })))),
    tabs: await context?.serviceWorkers()[0]?.evaluate(async () => (await chrome.tabs.query({ url: "https://gemini.google.com/*" }))
      .map((tab) => ({ id: tab.id, url: tab.url, pendingUrl: tab.pendingUrl, status: tab.status, discarded: tab.discarded, frozen: tab.frozen, incognito: tab.incognito }))),
  };
  throw failure;
} finally {
  await context?.close();
  server.stop(true);
  await rm(profile, { recursive: true, force: true });
  await mkdir(join(root, "evidence"), { recursive: true });
  await Bun.write(join(root, "evidence", "qa-report.json"), JSON.stringify({ extensionId, version, permissions, chromeShapes, internalSenders,
    checks, shots, measurements, errorCount, consoleErrors, artifact, failureEvidence, profileRemoved: true, serverStopped: true, browserClosed: true }, null, 2) + "\n");
}
console.info(JSON.stringify({ extensionId, version, checks: checks.length, screenshots: shots.length, evidence }));
