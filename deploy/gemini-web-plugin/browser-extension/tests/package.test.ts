import { expect, test } from "bun:test";
import { buildExtension } from "../build.ts";

test("packages an installable MV3 root with license and no test or token artifacts", async () => {
  const artifact = await buildExtension();

  const archive = Bun.spawn(["unzip", "-Z1", artifact.zip], { stdout: "pipe", stderr: "pipe" });
  const entries = (await new Response(archive.stdout).text()).trim().split("\n").sort();

  expect(await archive.exited).toBe(0);
  expect(entries).toEqual(["DESIGN.md", "LICENSE.vendor", "LICENSE.zod", "PROVENANCE.md", "README.md", "manifest.json", "popup.html", "popup.js", "service-worker.js", "styles.css", "tokens.css"]);
  const manifest: unknown = await Bun.file(`${artifact.directory}/manifest.json`).json();
  expect(manifest).toMatchObject({ background: { service_worker: "service-worker.js", type: "module" } });
}, 30000);
