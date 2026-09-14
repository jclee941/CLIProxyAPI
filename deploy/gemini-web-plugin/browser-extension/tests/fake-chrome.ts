import { runInNewContext } from "node:vm";

export const SELECTED_TAB = 42;
export const OTHER_TAB = 73;
export const SELECTED_STORE = "1";
export const OTHER_STORE = "0";
export const GAIA = "111111111111111111111";
export const OTHER_GAIA = "222222222222222222222";
export const FIRST_PARTY = {
  topLevelSite: "https://google.com",
  hasCrossSiteAncestor: false,
} as const;

type FakeTab = {
  id: number;
  url: string;
  active: boolean;
  status: "complete" | "loading";
  discarded: boolean;
  frozen: boolean;
  pendingUrl?: string;
};

type FakeDocument = {
  documentId: string;
  frameId: number;
  url: string;
  wiz: unknown;
  partitionKey: chrome.cookies.CookiePartitionKey;
};

export type FakeCall =
  | { readonly method: "tab"; readonly tabId: number }
  | { readonly method: "script"; readonly tabId: number }
  | { readonly method: "stores" }
  | { readonly method: "partition"; readonly target: chrome.cookies.FrameDetails }
  | { readonly method: "cookies"; readonly query: chrome.cookies.GetAllDetails };

export function identity(gaia: string): Record<string, unknown> {
  return { S06Grb: gaia, W3Yyqf: gaia, qDCSke: gaia };
}

export function cookie(overrides: Partial<chrome.cookies.Cookie> = {}): chrome.cookies.Cookie {
  return {
    name: "SID",
    value: "SYNTHETIC-selected-session",
    storeId: SELECTED_STORE,
    domain: ".google.com",
    hostOnly: false,
    path: "/",
    httpOnly: true,
    secure: true,
    session: true,
    sameSite: "lax",
    ...overrides,
  };
}

export class FakeChrome {
  readonly selected: FakeTab = {
    id: SELECTED_TAB,
    url: "https://gemini.google.com/u/2/app/synthetic-conversation",
    active: false,
    status: "complete",
    discarded: false,
    frozen: false,
  };
  readonly other: FakeTab = {
    id: OTHER_TAB,
    url: "https://gemini.google.com/u/7/app",
    active: true,
    status: "complete",
    discarded: false,
    frozen: false,
  };
  readonly selectedDocument: FakeDocument = {
    documentId: "11111111-1111-4111-8111-111111111111",
    frameId: 0,
    url: this.selected.url,
    wiz: identity(GAIA),
    partitionKey: { ...FIRST_PARTY },
  };
  readonly tabsById = new Map([[SELECTED_TAB, this.selected], [OTHER_TAB, this.other]]);
  readonly documents = new Map([
    [SELECTED_TAB, this.selectedDocument],
    [OTHER_TAB, {
      documentId: "22222222-2222-4222-8222-222222222222",
      frameId: 0,
      url: this.other.url,
      wiz: identity(OTHER_GAIA),
      partitionKey: { ...FIRST_PARTY },
    }],
  ]);
  stores: chrome.cookies.CookieStore[] = [
    { id: OTHER_STORE, tabIds: [OTHER_TAB] },
    { id: SELECTED_STORE, tabIds: [SELECTED_TAB] },
  ];
  jar: chrome.cookies.Cookie[] = [
    cookie({ storeId: OTHER_STORE, value: "SYNTHETIC-other-profile" }),
    cookie(),
    cookie({ name: "SAPISID", value: "SYNTHETIC-selected-sapi", httpOnly: false }),
    cookie({ name: "NEW_SESSION_COOKIE", value: "SYNTHETIC-not-in-vendor-list" }),
    cookie({ name: "HOST_COOKIE", value: "SYNTHETIC-gemini-host", domain: "gemini.google.com", hostOnly: true,
      session: false, expirationDate: 4102444800, secure: false, sameSite: "no_restriction" }),
    cookie({ name: "EMPTY", value: "" }),
    cookie({ name: "ACCOUNT_ONLY", value: "SYNTHETIC-accounts-only", domain: "accounts.google.com", hostOnly: true }),
    cookie({ name: "FOREIGN", value: "SYNTHETIC-foreign", domain: "example.test", hostOnly: true }),
    cookie({ name: "OTHER_PATH", value: "SYNTHETIC-other-user-path", path: "/u/7" }),
    cookie({ name: "OTHER_PARTITION", value: "SYNTHETIC-other-site", partitionKey: { topLevelSite: "https://example.test", hasCrossSiteAncestor: true } }),
  ];
  readonly calls: FakeCall[] = [];
  afterCall?: (call: FakeCall) => void;
  failure?: { readonly method: FakeCall["method"]; readonly error: unknown };
  scriptResults?: readonly chrome.scripting.InjectionResult<unknown>[];
  cookieResults?: readonly chrome.cookies.Cookie[];
  readonly forbiddenReads: string[] = [];

  private complete<Value>(call: FakeCall, result: Value): Value {
    this.calls.push(call);
    if (this.failure?.method === call.method) throw this.failure.error;
    this.afterCall?.(call);
    return result;
  }

  readonly tabs = {
    get: async (tabId: number): Promise<Readonly<FakeTab>> => {
      const tab = this.tabsById.get(tabId);
      if (!tab) throw new Error("SYNTHETIC-tab-unavailable");
      return this.complete({ method: "tab", tabId }, { ...tab });
    },
  };

  readonly cookies = {
    getAllCookieStores: async (): Promise<chrome.cookies.CookieStore[]> =>
      this.complete({ method: "stores" }, structuredClone(this.stores)),
    getPartitionKey: async (target: chrome.cookies.FrameDetails) => {
      const page = this.documents.get(target.tabId ?? -1);
      if (!page || page.documentId !== target.documentId || target.frameId !== 0) {
        throw new Error("SYNTHETIC-document-unavailable");
      }
      return this.complete({ method: "partition", target }, { partitionKey: { ...page.partitionKey } });
    },
    getAll: async (query: chrome.cookies.GetAllDetails): Promise<readonly chrome.cookies.Cookie[]> => {
      if (!query.url || query.storeId === undefined) throw new Error("SYNTHETIC-unscoped-query");
      const target = new URL(query.url);
      const matches = this.jar.filter((entry) => {
        const domain = entry.domain.replace(/^\./, "");
        const hostMatches = target.hostname === domain || (!entry.hostOnly && target.hostname.endsWith(`.${domain}`));
        const pathMatches = target.pathname === entry.path || (target.pathname.startsWith(entry.path) && (entry.path.endsWith("/") || target.pathname[entry.path.length] === "/"));
        const partitionMatches = query.partitionKey === undefined
          ? entry.partitionKey === undefined
          : entry.partitionKey !== undefined
            && entry.partitionKey.topLevelSite === query.partitionKey.topLevelSite
            && entry.partitionKey.hasCrossSiteAncestor === query.partitionKey.hasCrossSiteAncestor;
        return entry.storeId === query.storeId && hostMatches && pathMatches
          && (!entry.secure || target.protocol === "https:") && partitionMatches;
      });
      return this.complete({ method: "cookies", query }, structuredClone(this.cookieResults ?? matches));
    },
  };

  readonly scripting = {
    executeScript: async (injection: chrome.scripting.ScriptInjection<[string], unknown>) => {
      const { target } = injection;
      const page = this.documents.get(target.tabId);
      if (!page || !injection.func || !("args" in injection) || injection.world !== "MAIN"
          || target.allFrames || target.frameIds?.length !== 1 || target.frameIds[0] !== 0) {
        throw new Error("SYNTHETIC-invalid-injection");
      }
      const sandbox = {
        location: { href: page.url, origin: new URL(page.url).origin },
        WIZ_global_data: page.wiz,
        captureArgs: injection.args,
      };
      for (const field of ["document", "performance", "fetch", "localStorage", "sessionStorage", "navigator", "console"]) {
        Object.defineProperty(sandbox, field, { get: () => {
          this.forbiddenReads.push(field);
          throw new Error("SYNTHETIC-forbidden-page-read");
        } });
      }
      const result: unknown = runInNewContext(`(${injection.func.toString()})(...captureArgs)`, sandbox);
      return this.complete({ method: "script", tabId: target.tabId }, this.scriptResults ?? [
        { frameId: page.frameId, documentId: page.documentId, result },
      ]);
    },
  };
}
