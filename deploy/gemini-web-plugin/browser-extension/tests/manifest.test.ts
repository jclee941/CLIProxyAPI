import { expect, test } from "bun:test";
import { makeManifest } from "../manifest.ts";

test("ships only the exact production portal and declared capture permissions", () => {
  const manifest = makeManifest();

  expect(manifest).toMatchObject({ manifest_version: 3, minimum_chrome_version: "132",
    permissions: ["cookies", "tabs", "scripting", "storage"],
    host_permissions: ["https://*.google.com/*", "https://cliproxy.jclee.me/*"],
    externally_connectable: { matches: ["https://cliproxy.jclee.me/v0/resource/plugins/gemini-web/index"] },
    incognito: "not_allowed" });
  expect(manifest).not.toHaveProperty("content_scripts");
  expect(manifest).not.toHaveProperty("web_accessible_resources");
  expect(manifest).not.toHaveProperty("key");
});

test.each(["https://evil.test", "http://127.0.0.1", "http://localhost:43123/path", "http://localhost.evil.test:43123", "*"])(
  "rejects an arbitrary QA origin: %s", (origin) => {
    expect(() => makeManifest(origin)).toThrow();
  },
);

test("permits only the explicitly configured localhost QA build origin", () => {
  const manifest = makeManifest("http://localhost:43123");

  expect(manifest.externally_connectable.matches).toEqual(["http://localhost:43123/v0/resource/plugins/gemini-web/index"]);
});
