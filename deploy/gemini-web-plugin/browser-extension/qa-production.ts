import assert from "node:assert/strict";
import { mkdir, mkdtemp, rm } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium, type BrowserContext } from "playwright-core";
import { buildExtension } from "./build.ts";

const root = fileURLToPath(new URL(".", import.meta.url));
const artifact = await buildExtension();
const profile = await mkdtemp(join(root, ".qa-production-profile-"));
let context: BrowserContext | undefined;
try {
  context = await chromium.launchPersistentContext(profile, {
    channel: "chromium", executablePath: Bun.argv[2] ?? chromium.executablePath(), headless: true,
    args: [`--disable-extensions-except=${artifact.directory}`, `--load-extension=${artifact.directory}`,
      "--host-resolver-rules=MAP * ~NOTFOUND"],
  });
  const worker = context.serviceWorkers()[0] ?? await context.waitForEvent("serviceworker");
  const extensionId = new URL(worker.url()).hostname;
  await context.route("**/*", async (route) => {
    const url = new URL(route.request().url());
    if (url.protocol === "chrome-extension:" && url.hostname === extensionId) await route.continue();
    else await route.abort();
  });
  const manifest = await worker.evaluate(() => chrome.runtime.getManifest());
  const permissions = await worker.evaluate(() => chrome.permissions.getAll());
  assert.deepEqual(manifest.externally_connectable?.matches, ["https://cliproxy.jclee.me/v0/resource/plugins/gemini-web/index"]);
  assert.equal(await worker.evaluate(() => chrome.permissions.contains({ origins: ["http://localhost:43123/*"] })), false);
  assert.equal(await worker.evaluate(() => chrome.permissions.contains({ origins: ["https://*.google.com/*", "https://cliproxy.jclee.me/*"] })), true);
  assert.match(extensionId, /^[a-p]{32}$/);
  const popup = await context.newPage();
  await popup.goto(`chrome-extension://${extensionId}/popup.html`);
  await popup.locator("#approve").waitFor({ state: "hidden" });
  await popup.setViewportSize({ width: 375, height: 720 });
  await mkdir(join(root, "evidence"), { recursive: true });
  await popup.screenshot({ path: join(root, "evidence", "production-idle.png"), fullPage: true });
  await Bun.write(join(root, "evidence", "production-install.json"), JSON.stringify({
    extensionId, version: context.browser()?.version(), manifest, permissions, artifact,
    network: "blocked; no portal or Google navigation", tests: ["production-manifest", "google-host-grant", "localhost-denied", "unbound-popup"],
  }, null, 2) + "\n");
  console.info(JSON.stringify({ extensionId, artifact }));
} finally {
  await context?.close();
  await rm(profile, { recursive: true, force: true });
}
