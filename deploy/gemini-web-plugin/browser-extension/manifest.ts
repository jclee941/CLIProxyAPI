import { GOOGLE_HOSTS, PORTAL_ORIGIN, PORTAL_PATH } from "./protocol.ts";

export function resolveOrigin(origin: string): string {
  if (origin === PORTAL_ORIGIN) return origin;
  const url = URL.parse(origin);
  if (!url || url.origin !== origin || url.protocol !== "http:" || url.hostname !== "localhost"
    || !url.port || url.username || url.password) throw new TypeError("invalid_qa_origin");
  return origin;
}

export function makeManifest(origin = PORTAL_ORIGIN) {
  const allowed = resolveOrigin(origin);
  return {
    manifest_version: 3,
    name: allowed === PORTAL_ORIGIN ? "Gemini Web Login Companion" : "Gemini Web Login Companion (LOCAL QA ONLY)",
    version: "0.2.0",
    description: "Explicit, single-tab Gemini Web login transfer to the configured CLIProxy portal.",
    minimum_chrome_version: "132",
    permissions: ["cookies", "tabs", "scripting", "storage"],
    host_permissions: [...GOOGLE_HOSTS, `${allowed}/*`],
    externally_connectable: { matches: [`${allowed}${PORTAL_PATH}`] },
    background: { service_worker: "service-worker.js", type: "module" },
    action: { default_popup: "popup.html", default_title: "Gemini Web" },
    incognito: "not_allowed",
    content_security_policy: {
      extension_pages: "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'",
    },
  };
}
