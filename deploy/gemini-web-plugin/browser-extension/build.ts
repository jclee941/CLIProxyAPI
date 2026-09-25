import { mkdir, copyFile, rm } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { makeManifest, resolveOrigin } from "./manifest.ts";
import { PORTAL_ORIGIN } from "./protocol.ts";

const root = fileURLToPath(new URL(".", import.meta.url));
const assets = ["popup.html", "styles.css", "tokens.css", "LICENSE.vendor", "LICENSE.zod", "PROVENANCE.md", "README.md", "DESIGN.md"] as const;

export async function buildExtension(origin = PORTAL_ORIGIN) {
  const allowed = resolveOrigin(origin);
  const variant = allowed === PORTAL_ORIGIN ? "production" : "qa";
  const directory = join(root, "dist", variant);
  const zip = join(root, "artifacts", `gemini-web-login-companion-0.2.0${variant === "qa" ? "-LOCAL-QA" : ""}.zip`);
  await mkdir(directory, { recursive: true });
  await mkdir(join(root, "artifacts"), { recursive: true });
  const built = await Bun.build({ entrypoints: [join(root, "service-worker.ts"), join(root, "popup.ts")],
    outdir: directory, target: "browser", format: "esm", minify: true,
    define: { __PORTAL_ORIGIN__: JSON.stringify(allowed) } });
  if (!built.success) throw new AggregateError(built.logs, "extension_build_failed");
  await Bun.write(join(directory, "manifest.json"), JSON.stringify(makeManifest(allowed), null, 2) + "\n");
  await Promise.all(assets.map((asset) => copyFile(join(root, asset), join(directory, asset))));
  await rm(zip, { force: true });
  const archive = Bun.spawn(["zip", "-q", "-X", zip, "manifest.json", "service-worker.js", "popup.js", ...assets],
    { cwd: directory, stdout: "pipe", stderr: "pipe" });
  if (await archive.exited !== 0) throw new Error("extension_package_failed");
  return { directory, zip };
}

if (import.meta.main) {
  const args = Bun.argv.slice(2);
  if (args.length !== 0 && (args.length !== 2 || args[0] !== "--qa-origin")) throw new TypeError("invalid_build_arguments");
  const artifact = await buildExtension(args[1] ?? PORTAL_ORIGIN);
  console.info(JSON.stringify(artifact));
}
