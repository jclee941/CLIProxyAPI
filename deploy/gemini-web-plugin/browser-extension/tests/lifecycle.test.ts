import { expect, test } from "bun:test";
import { CaptureError } from "../capture.ts";
import { LoginCompanion } from "../companion.ts";
import { TTL_MS } from "../protocol.ts";
import { FakeCompanion, FakePort, STATE, settle } from "./fake-companion.ts";

test.each([
  null, [], {}, "begin", { type: "begin", state: "a".repeat(63) },
  { type: "begin", state: "A".repeat(64) }, { type: "begin", state: STATE + "\n" },
  { type: "begin", state: STATE, token: "SYNTHETIC" },
  { type: "begin", state: STATE, expected_identity: { account_sha256: "b".repeat(64) } },
  { type: "begin", state: STATE, expected_identity: { account_sha256: "b".repeat(64), auth_user: -1 } },
  { type: "ack", state: STATE }, { type: "cancel", state: STATE }, { type: "session", state: STATE },
].map((message) => ({ message })))("rejects malformed or out-of-order portal input before capture: %#", async ({ message }) => {
  const host = new FakeCompanion();

  const port = await host.begin(new LoginCompanion(host), message);

  expect(port.disconnected).toBe(true);
  expect(host.captures).toEqual([]);
  expect(host.dialogs).toEqual([]);
});

test.each(["cancel", "ack", "begin"])("stops before capture on wrong-state %s", async (type) => {
  const host = new FakeCompanion();
  const port = await host.begin(new LoginCompanion(host));
  const ui = host.consent();
  await settle();

  port.receive({ type, state: "c".repeat(64) });
  ui.receive({ type: "select", tab_id: 42 });
  ui.receive({ type: "approve", consent: true });
  await settle();

  expect(host.captures).toEqual([]);
  expect(port.disconnected).toBe(true);
});

test.each(["portal", "consent"])("cleans up when the %s disconnects before approval", async (which) => {
  const host = new FakeCompanion();
  const port = await host.begin(new LoginCompanion(host));
  const ui = host.consent();
  await settle();

  (which === "portal" ? port : ui).remoteDisconnect();
  ui.receive({ type: "select", tab_id: 42 });
  ui.receive({ type: "approve", consent: true });
  await settle();

  expect(host.captures).toEqual([]);
  expect(host.timers.size).toBe(0);
  expect(port.disconnected && ui.disconnected).toBe(true);
});

test.each(["ack", "cancel"])("clears and disconnects both channels on portal %s after delivery", async (type) => {
  const host = new FakeCompanion();
  const port = await host.begin(new LoginCompanion(host));
  const ui = host.consent();
  await settle();
  ui.receive({ type: "select", tab_id: 42 });
  ui.receive({ type: "approve", consent: true });
  await settle();

  port.receive({ type, state: STATE });
  await settle();

  expect(port.disconnected && ui.disconnected).toBe(true);
  expect(host.timers.size).toBe(0);
  expect(port.sent).toHaveLength(2);
});

test("rejects a replay on a new port after cancellation", async () => {
  const host = new FakeCompanion();
  const companion = new LoginCompanion(host);
  const original = await host.begin(companion);
  original.receive({ type: "cancel", state: STATE });

  const replay = await host.begin(companion);

  expect(replay.disconnected).toBe(true);
  expect(host.dialogs).toHaveLength(1);
  expect(host.captures).toEqual([]);
});

test("limits all outstanding connections to 32 including ports without begin", () => {
  const host = new FakeCompanion();
  const companion = new LoginCompanion(host);
  const ports = Array.from({ length: 33 }, () => new FakePort());

  for (const port of ports) companion.connect(port);

  expect(ports.filter((port) => port.disconnected)).toHaveLength(1);
  expect(host.timers.size).toBe(32);
});

test("rejects approval at the 10-minute boundary even before a suspended timer fires", async () => {
  const host = new FakeCompanion();
  const port = await host.begin(new LoginCompanion(host));
  const ui = host.consent();
  await settle();
  ui.receive({ type: "select", tab_id: 42 });
  host.time += TTL_MS;

  ui.receive({ type: "approve", consent: true });
  await settle();

  expect(host.captures).toEqual([]);
  expect(port.disconnected).toBe(true);
});

test.each(["disconnect", "cancel", "expire"])("drops an in-flight result when %s occurs", async (action) => {
  const host = new FakeCompanion();
  const gate = Promise.withResolvers<void>();
  host.captureGate = gate.promise;
  const port = await host.begin(new LoginCompanion(host));
  const ui = host.consent();
  await settle();
  ui.receive({ type: "select", tab_id: 42 });
  ui.receive({ type: "approve", consent: true });
  await settle();

  switch (action) {
    case "disconnect": port.remoteDisconnect(); break;
    case "cancel": port.receive({ type: "cancel", state: STATE }); break;
    case "expire": host.time += TTL_MS; break;
  }
  gate.resolve();
  await settle();

  expect(port.sent).toEqual([{ type: "ready", state: STATE }]);
  expect(port.disconnected).toBe(true);
});

test("rechecks portal document authority before invoking capture", async () => {
  const host = new FakeCompanion();
  const port = await host.begin(new LoginCompanion(host));
  const ui = host.consent();
  await settle();
  ui.receive({ type: "select", tab_id: 42 });
  host.authorized = false;

  ui.receive({ type: "approve", consent: true });
  await settle();

  expect(host.captures).toEqual([]);
  expect(port.disconnected).toBe(true);
});

test.each([false, true])("requires complete Google permissions at approval, initially %s", async (initial) => {
  const host = new FakeCompanion();
  host.permissions = initial;
  const port = await host.begin(new LoginCompanion(host));
  const ui = host.consent();
  await settle();
  if (initial) ui.receive({ type: "select", tab_id: 42 });
  host.permissions = false;

  ui.receive({ type: "approve", consent: true });
  await settle();

  expect(host.captures).toEqual([]);
  expect(port.disconnected).toBe(true);
});

test.each([new CaptureError("identity_mismatch"), new Error("SYNTHETIC-private-error")])(
  "exposes only a closed status when capture fails: %#", async (failure) => {
    const host = new FakeCompanion();
    host.capture = async () => { throw failure; };
    const port = await host.begin(new LoginCompanion(host));
    const ui = host.consent();
    await settle();
    ui.receive({ type: "select", tab_id: 42 });

    ui.receive({ type: "approve", consent: true });
    await settle();

    expect(port.sent).toEqual([{ type: "ready", state: STATE }]);
    expect(ui.sent.at(-1)).toEqual({ type: "closed", reason: failure instanceof CaptureError ? "capture_failed" : "unavailable" });
    expect(port.disconnected).toBe(true);
  },
);

test("passes the validated expected identity unchanged into selected capture", async () => {
  const host = new FakeCompanion();
  const expected = { account_sha256: "c".repeat(64), auth_user: 2 };
  await host.begin(new LoginCompanion(host), { type: "begin", state: STATE, expected_identity: expected });
  const ui = host.consent();
  await settle();
  ui.receive({ type: "select", tab_id: 42 });

  ui.receive({ type: "approve", consent: true });
  await settle();

  expect(host.captures[0]?.expectedIdentity).toEqual(expected);
});

test("opens Gemini without selecting or capturing a tab", async () => {
  const host = new FakeCompanion();
  await host.begin(new LoginCompanion(host));
  const ui = host.consent();
  await settle();

  ui.receive({ type: "open_gemini" });
  await settle();

  expect(host.opened).toBe(1);
  expect(host.captures).toEqual([]);
  expect(ui.sent.at(-1)).toMatchObject({ selected: null });
});
