import { z } from "zod";

const MAX_TOMBSTONES = 4096;
const ledgerSchema = z.object({ usedStates: z.array(z.strictObject({
  digest: z.string().length(64).regex(/^[0-9a-f]+$/), expires: z.number().int().min(0),
})).max(MAX_TOMBSTONES).optional() });

export type ReplayArea = {
  readonly get: (key: string) => Promise<unknown>;
  readonly set: (value: { readonly usedStates: readonly { readonly digest: string; readonly expires: number }[] }) => Promise<void>;
};

export class ReplayLedger {
  private queue: Promise<void> = Promise.resolve();
  constructor(private readonly area: ReplayArea, private readonly now: () => number) {}

  claim(state: string, expires: number): Promise<boolean> {
    const operation = this.queue.then(async () => {
      const snapshot = ledgerSchema.safeParse(await this.area.get("usedStates"));
      if (!snapshot.success || expires <= this.now()) return false;
      const bytes = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(state));
      const digest = Array.from(new Uint8Array(bytes), (byte) => byte.toString(16).padStart(2, "0")).join("");
      const retained = (snapshot.data.usedStates ?? []).filter((entry) => entry.expires > this.now());
      if (retained.length >= MAX_TOMBSTONES || retained.some((entry) => entry.digest === digest)) return false;
      await this.area.set({ usedStates: [...retained, { digest, expires }] });
      return true;
    });
    this.queue = operation.then(() => undefined, () => undefined);
    return operation;
  }
}
