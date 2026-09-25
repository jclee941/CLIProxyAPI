import { z } from "zod";

export const PORTAL_ORIGIN = "https://cliproxy.jclee.me";
export const PORTAL_PATH = "/v0/resource/plugins/gemini-web/index";
export const PORT_NAME = "gemini-web-login";
export const CONSENT_PORT = "gemini-web-consent";
export const TTL_MS = 10 * 60 * 1000;
export const MAX_ACTIVE = 32;
export const GOOGLE_HOSTS = ["https://*.google.com/*"];
export const safeIndex = z.number().int().min(0).max(Number.MAX_SAFE_INTEGER);
export const stateSchema = z.string().length(64).regex(/^[0-9a-f]+$/).brand("LoginState");
export const expectedSchema = z.strictObject({
  account_sha256: z.string().length(64).regex(/^[0-9a-f]+$/), auth_user: safeIndex,
});
export const portalMessage = z.discriminatedUnion("type", [
  z.strictObject({ type: z.literal("begin"), state: stateSchema, expected_identity: expectedSchema.optional() }),
  z.strictObject({ type: z.literal("ack"), state: stateSchema }),
  z.strictObject({ type: z.literal("cancel"), state: stateSchema }),
]);
export const consentMessage = z.discriminatedUnion("type", [
  z.strictObject({ type: z.literal("select"), tab_id: safeIndex }),
  z.strictObject({ type: z.literal("approve"), consent: z.literal(true) }),
  z.strictObject({ type: z.literal("refresh") }),
  z.strictObject({ type: z.literal("cancel") }),
  z.strictObject({ type: z.literal("open_gemini") }),
  z.strictObject({ type: z.literal("keepalive") }),
]);
export const closeReason = z.enum(["cancelled", "expired", "invalid_message", "unavailable", "capture_failed", "acknowledged"]);
export type CloseReason = z.output<typeof closeReason>;
export const viewMessage = z.discriminatedUnion("type", [
  z.strictObject({
    type: z.literal("view"), destination: z.string(),
    phase: z.enum(["choosing", "loading", "capturing", "delivered"]),
    permissions: z.boolean(), replacement: z.boolean(),
    choices: z.array(z.strictObject({ tab_id: safeIndex, auth_user: safeIndex })),
    selected: safeIndex.nullable(),
  }),
  z.strictObject({ type: z.literal("closed"), reason: closeReason }),
]);
export type ViewMessage = z.output<typeof viewMessage>;
export type Begin = Extract<z.output<typeof portalMessage>, { type: "begin" }>;

const senderSchema = z.object({
  id: z.undefined().optional(), url: z.string(), origin: z.string(),
  documentId: z.string().min(1), frameId: safeIndex, tab: z.object({ id: safeIndex }),
  documentLifecycle: z.literal("active").optional(),
});
export type PortalSender = z.output<typeof senderSchema>;
export function authorizeSender(raw: unknown, origin: string): PortalSender | null {
  const parsed = senderSchema.safeParse(raw);
  if (!parsed.success) return null;
  const sender = parsed.data;
  const url = URL.parse(sender.url);
  return url && url.href === sender.url && url.origin === origin && sender.origin === origin
    && url.pathname === PORTAL_PATH && !url.username && !url.password ? sender : null;
}

export type Port = {
  readonly name: string;
  readonly sender?: unknown;
  readonly postMessage: (message: unknown) => void;
  readonly disconnect: () => void;
  readonly onMessage: {
    readonly addListener: (listener: (message: unknown) => void) => void;
    readonly removeListener: (listener: (message: unknown) => void) => void;
  };
  readonly onDisconnect: {
    readonly addListener: (listener: () => void) => void;
    readonly removeListener: (listener: () => void) => void;
  };
};

export function postToPort(port: Port, message: unknown): boolean {
  try { port.postMessage(message); return true; }
  catch { return false; }
}

export function assertNever(value: never): never {
  throw new TypeError(`Unexpected variant type: ${typeof value}`);
}
