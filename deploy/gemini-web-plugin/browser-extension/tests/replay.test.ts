import { expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { ReplayLedger } from "../replay.ts";
import { STATE } from "./fake-companion.ts";

class MemoryArea {
  value: unknown = {};
  get = async (_key: string): Promise<unknown> => structuredClone(this.value);
  set = async (value: unknown): Promise<void> => { this.value = structuredClone(value); };
}

test("rejects replay across worker instances without storing the original state", async () => {
  const area = new MemoryArea();
  const first = new ReplayLedger(area, () => 1000);
  await first.claim(STATE, 2000);

  const accepted = await new ReplayLedger(area, () => 1000).claim(STATE, 2000);

  expect(accepted).toBe(false);
  expect(area.value).toEqual({ usedStates: [{ digest: createHash("sha256").update(STATE).digest("hex"), expires: 2000 }] });
});

test("serializes concurrent claims so only one port owns the state", async () => {
  const ledger = new ReplayLedger(new MemoryArea(), () => 1000);

  const results = await Promise.all([ledger.claim(STATE, 2000), ledger.claim(STATE, 2000)]);

  expect(results).toEqual([true, false]);
});

test("removes expired replay tombstones rather than retaining session metadata", async () => {
  const area = new MemoryArea();
  await new ReplayLedger(area, () => 1000).claim(STATE, 2000);

  await new ReplayLedger(area, () => 2000).claim("c".repeat(64), 3000);

  expect(area.value).toEqual({ usedStates: [{ digest: createHash("sha256").update("c".repeat(64)).digest("hex"), expires: 3000 }] });
});
