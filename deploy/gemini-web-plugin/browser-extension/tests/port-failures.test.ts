import { expect, test } from "bun:test";
import { LoginCompanion } from "../companion.ts";
import { FakeCompanion, FakePort, STATE, settle } from "./fake-companion.ts";

test("discards a failed consent-channel write and still closes both ports", async () => {
  const host = new FakeCompanion();
  const portal = await host.begin(new LoginCompanion(host));
  const ui = host.consent();
  await settle();
  ui.postMessage = () => { throw new Error("SYNTHETIC-private-port-error"); };

  const select = () => ui.receive({ type: "select", tab_id: 42 });

  expect(select).not.toThrow();
  expect(portal.disconnected && ui.disconnected).toBe(true);
  expect(host.captures).toEqual([]);
  expect(host.timers.size).toBe(0);
});

test("never opens consent if ready cannot be delivered to the original port", async () => {
  const host = new FakeCompanion();
  const portal = new FakePort();
  portal.postMessage = () => { throw new Error("SYNTHETIC-private-port-error"); };
  new LoginCompanion(host).connect(portal);

  portal.receive({ type: "begin", state: STATE });
  await settle();

  expect(host.dialogs).toEqual([]);
  expect(host.captures).toEqual([]);
  expect(portal.disconnected).toBe(true);
});
