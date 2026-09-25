import { z } from "zod";
import { type ChromeCaptureApi, readDocumentIdentity } from "./chrome-adapter.ts";

export type { ChromeCaptureApi } from "./chrome-adapter.ts";

export type ExpectedIdentity = {
  readonly account_sha256: string;
  readonly auth_user: number;
};

export type CaptureSelection = {
  readonly tabId: number;
  readonly storeId?: string;
  readonly url?: string;
  readonly expectedIdentity?: ExpectedIdentity;
};

export type CapturedSession = ExpectedIdentity & { readonly token: string };

export type CaptureErrorCode =
  | "invalid_selection" | "invalid_tab" | "ambiguous_store"
  | "identity_unavailable" | "identity_mismatch" | "identity_changed"
  | "cookie_scope_unsupported" | "cookie_header_invalid" | "cookies_changed"
  | "token_too_large" | "capture_unavailable";

export class CaptureError extends Error {
  override readonly name = "CaptureError";
  constructor(readonly code: CaptureErrorCode) {
    super(code);
  }
}

const ORIGIN = "https://gemini.google.com";
const TOKEN_PREFIX = "gemini-web:v1:";
const MAX_TOKEN_BYTES = 32768;
const safeIndex = z.number().int().min(0).max(Number.MAX_SAFE_INTEGER);
const authUserSchema = safeIndex.brand("AuthUser");
const digestSchema = z.string().regex(/^[0-9a-f]{64}$/).brand("AccountSHA256");
const selectionSchema = z.object({
  tabId: safeIndex.brand("TabId"),
  storeId: z.string().min(1).optional(),
  url: z.string().optional(),
  expectedIdentity: z.object({ account_sha256: digestSchema, auth_user: authUserSchema }).optional(),
});
const tabSchema = z.object({
  id: safeIndex,
  url: z.string(),
  pendingUrl: z.undefined().optional(),
  status: z.literal("complete"),
  discarded: z.literal(false),
  frozen: z.literal(false),
});
const storesSchema = z.array(z.object({
  id: z.string().min(1).brand("CookieStoreId"),
  tabIds: z.array(safeIndex),
}));
const gaiaSchema = z.string().regex(/^[0-9]{21}$/);
const documentSchema = z.array(z.object({
  documentId: z.string().min(1).brand("DocumentId"),
  frameId: z.literal(0),
  result: z.object({
    url: z.string(),
    S06Grb: gaiaSchema,
    W3Yyqf: gaiaSchema,
    qDCSke: gaiaSchema,
  }).refine((value) => value.S06Grb === value.W3Yyqf && value.S06Grb === value.qDCSke),
})).length(1);
const partitionSchema = z.object({ partitionKey: z.strictObject({
  topLevelSite: z.literal("https://google.com"),
  hasCrossSiteAncestor: z.literal(false),
}) });
const cookiesSchema = z.array(z.object({
  name: z.string().regex(/^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/),
  value: z.string().regex(/^[\x21\x23-\x2B\x2D-\x3A\x3C-\x5B\x5D-\x7E]*$/),
  domain: z.string(),
  hostOnly: z.boolean(),
  path: z.string(),
  storeId: z.string(),
  partitionKey: z.unknown().optional(),
})).min(1);

export function authUserFromUrl(raw: string): z.output<typeof authUserSchema> {
  const url = URL.parse(raw);
  if (!url || url.href !== raw || url.origin !== ORIGIN || url.username || url.password
    || url.pathname.includes("%") || url.pathname.includes("//")) {
    throw new CaptureError("invalid_tab");
  }
  const segments = url.pathname.split("/").slice(1);
  const selectors = segments.filter((segment) => segment.toLowerCase() === "u");
  let index = 0;
  if (selectors.length !== 0) {
    const value = segments[1];
    if (selectors.length !== 1 || segments[0] !== "u" || value === undefined || !/^(0|[1-9][0-9]*)$/.test(value)) {
      throw new CaptureError("invalid_tab");
    }
    index = Number(value);
  }
  const querySelectors = [...url.searchParams.keys()].filter((key) => key.toLowerCase() === "authuser");
  if (querySelectors.length) {
    const value = url.searchParams.get("authuser");
    if (querySelectors.length !== 1 || value === null || !/^(0|[1-9][0-9]*)$/.test(value)
      || !url.search.slice(1).split("&").includes(`authuser=${value}`)
      || (selectors.length !== 0 && Number(value) !== index)) {
      throw new CaptureError("invalid_tab");
    }
    index = Number(value);
  }
  let fragment: string;
  try {
    fragment = decodeURIComponent(url.hash.slice(1));
  } catch (error: unknown) {
    if (error instanceof URIError) throw new CaptureError("invalid_tab");
    throw error;
  }
  if (/authuser|(?:^|\/)u(?:\/|$)/i.test(fragment)) throw new CaptureError("invalid_tab");
  const parsed = authUserSchema.safeParse(index);
  if (!parsed.success) throw new CaptureError("invalid_tab");
  return parsed.data;
}

async function snapshot(browser: ChromeCaptureApi, selection: z.output<typeof selectionSchema>) {
  const tab = tabSchema.safeParse(await browser.tabs.get(selection.tabId));
  if (!tab.success || tab.data.id !== selection.tabId) throw new CaptureError("invalid_tab");
  if (selection.url !== undefined && selection.url !== tab.data.url) throw new CaptureError("identity_changed");
  const authUser = authUserFromUrl(tab.data.url);
  if (selection.expectedIdentity && authUser !== selection.expectedIdentity.auth_user) {
    throw new CaptureError("identity_mismatch");
  }
  const stores = storesSchema.safeParse(await browser.cookies.getAllCookieStores());
  if (!stores.success) throw new CaptureError("ambiguous_store");
  const candidates = stores.data.filter((store) => store.tabIds.includes(selection.tabId));
  const store = candidates[0];
  if (candidates.length !== 1 || !store) throw new CaptureError("ambiguous_store");
  if (selection.storeId !== undefined && selection.storeId !== store.id) throw new CaptureError("identity_changed");
  const documents = documentSchema.safeParse(await readDocumentIdentity(browser, {
    tabId: selection.tabId, url: tab.data.url,
  }));
  if (!documents.success) throw new CaptureError("identity_unavailable");
  const document = documents.data[0];
  if (!document || document.result.url !== tab.data.url) throw new CaptureError("identity_unavailable");
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(document.result.S06Grb));
  const accountSHA256 = Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
  if (selection.expectedIdentity && accountSHA256 !== selection.expectedIdentity.account_sha256) {
    throw new CaptureError("identity_mismatch");
  }
  const partition = partitionSchema.safeParse(await browser.cookies.getPartitionKey({
    tabId: selection.tabId, frameId: 0, documentId: document.documentId,
  }));
  if (!partition.success) throw new CaptureError("cookie_scope_unsupported");
  return {
    url: tab.data.url,
    authUser,
    storeId: store.id,
    documentId: document.documentId,
    accountSHA256,
    partitionKey: partition.data.partitionKey,
  };
}

async function cookieHeader(browser: ChromeCaptureApi, scope: {
  readonly url: string;
  readonly storeId: string;
  readonly partitionKey: Readonly<chrome.cookies.CookiePartitionKey>;
}): Promise<string> {
  const { url, storeId, partitionKey } = scope;
  const partitioned = await browser.cookies.getAll({ url, storeId, partitionKey });
  if (partitioned.length !== 0) throw new CaptureError("cookie_scope_unsupported");
  const parsed = cookiesSchema.safeParse(await browser.cookies.getAll({ url, storeId }));
  if (!parsed.success) throw new CaptureError("cookie_header_invalid");
  const names = new Set<string>();
  for (const cookie of parsed.data) {
    const domain = cookie.domain.replace(/^\./, "");
    const applicable = domain === "gemini.google.com" || (!cookie.hostOnly && domain === "google.com");
    if (!applicable || cookie.storeId !== storeId || cookie.path !== "/"
      || cookie.partitionKey !== undefined || names.has(cookie.name)) {
      throw new CaptureError("cookie_scope_unsupported");
    }
    names.add(cookie.name);
  }
  return parsed.data.sort((first, second) => first.name < second.name ? -1 : 1)
    .map((cookie) => `${cookie.name}=${cookie.value}`).join("; ");
}

export async function captureGeminiSession(browser: ChromeCaptureApi, selection: CaptureSelection): Promise<CapturedSession> {
  try {
    const request = selectionSchema.safeParse(selection);
    if (!request.success) throw new CaptureError("invalid_selection");
    const before = await snapshot(browser, request.data);
    const header = await cookieHeader(browser, before);
    const payload = new TextEncoder().encode(JSON.stringify({ cookie: header, auth_user: before.authUser }));
    const encoded = btoa(Array.from(payload, (byte) => String.fromCharCode(byte)).join(""))
      .replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/, "");
    const token = TOKEN_PREFIX + encoded;
    if (new TextEncoder().encode(token).byteLength > MAX_TOKEN_BYTES) throw new CaptureError("token_too_large");
    const prefix = before.authUser === 0 ? "" : `/u/${before.authUser}`;
    const urls = new Set([
      `${ORIGIN}${prefix}/app`,
      `${ORIGIN}${prefix}/_/BardChatUi/data/batchexecute`,
      `${ORIGIN}${prefix}/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate`,
    ]);
    for (const url of urls) {
      if (url === before.url) continue;
      if (await cookieHeader(browser, { ...before, url }) !== header) throw new CaptureError("cookies_changed");
    }
    const after = await snapshot(browser, { tabId: request.data.tabId });
    if (before.url !== after.url || before.authUser !== after.authUser || before.storeId !== after.storeId
      || before.documentId !== after.documentId || before.accountSHA256 !== after.accountSHA256) {
      throw new CaptureError("identity_changed");
    }
    return { token, account_sha256: before.accountSHA256, auth_user: before.authUser };
  } catch (error: unknown) {
    if (error instanceof CaptureError) throw error;
    throw new CaptureError("capture_unavailable");
  }
}
