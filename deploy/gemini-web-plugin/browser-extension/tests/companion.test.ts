import { expect, test } from "bun:test";
import { LoginCompanion } from "../companion.ts";
import { FakeCompanion, FakePort, STATE, VALID_SENDER, settle } from "./fake-companion.ts";

test("replies ready without capture when an authorized portal begins", async () => {
  const host = new FakeCompanion();
  const port = new FakePort();
  new LoginCompanion(host).connect(port);

  port.receive({ type: "begin", state: STATE });
  await settle();

  expect(port.sent).toEqual([{ type: "ready", state: STATE }]);
  expect(host.captures).toEqual([]);
  expect(host.dialogs).toHaveLength(1);
});

test.each([
  { name: "wrong" }, { sender: undefined },
  { sender: { ...VALID_SENDER, url: "https://evil.test/v0/resource/plugins/gemini-web/index", origin: "https://evil.test" } },
  { sender: { ...VALID_SENDER, url: "https://cliproxy.jclee.me/management.html" } },
  { sender: { ...VALID_SENDER, url: "https://cliproxy.jclee.me/v0/resource/plugins/gemini-web/index/" } },
  { sender: { ...VALID_SENDER, origin: "null" } },
  { sender: { ...VALID_SENDER, id: "another-extension" } },
  { sender: { ...VALID_SENDER, documentLifecycle: "prerender" } },
  { sender: { ...VALID_SENDER, documentId: undefined } },
  { sender: { ...VALID_SENDER, frameId: undefined } },
])("refuses authority before opening consent or capture: %#", async (patch) => {
  const host = new FakeCompanion();
  const port = new FakePort();
  Object.assign(port, patch);

  new LoginCompanion(host).connect(port);
  port.receive({ type: "begin", state: STATE });
  await settle();

  expect(port.disconnected).toBe(true);
  expect(host.dialogs).toEqual([]);
  expect(host.captures).toEqual([]);
});

test("sends a session only to its original port after explicit selection and consent", async () => {
  const host = new FakeCompanion();
  const port = await host.begin(new LoginCompanion(host));
  const ui = host.consent();
  await settle();

  ui.receive({ type: "select", tab_id: 42 });
  ui.receive({ type: "approve", consent: true });
  await settle();

  expect(host.captures).toEqual([{ tabId: 42, storeId: "1", url: "https://gemini.google.com/u/2/app?hl=ko" }]);
  expect(port.sent).toEqual([{ type: "ready", state: STATE }, { type: "session", state: STATE, ...host.session }]);
  expect(JSON.stringify(ui.sent)).not.toContain(host.session.token);
  expect(port.disconnected).toBe(false);
});

test.each([{ type: "approve", consent: true }, { type: "approve", consent: false }, { type: "approve" }])(
  "never selects the first tab as a fallback when approval is incomplete: %#", async (message) => {
    const host = new FakeCompanion();
    const port = await host.begin(new LoginCompanion(host));
    const ui = host.consent();
    await settle();

    ui.receive(message);
    await settle();

    expect(host.captures).toEqual([]);
    expect(port.disconnected).toBe(true);
  },
);
