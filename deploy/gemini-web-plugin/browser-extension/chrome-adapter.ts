export type CookieQuery = {
  readonly url: string;
  readonly storeId: string;
  readonly partitionKey?: Readonly<chrome.cookies.CookiePartitionKey>;
};

export type ChromeCaptureApi = {
  readonly tabs: {
    readonly get: (tabId: number) => Promise<Readonly<Pick<chrome.tabs.Tab,
      "id" | "url" | "pendingUrl" | "status" | "discarded" | "frozen"
    >>>;
  };
  readonly cookies: {
    readonly getAllCookieStores: () => Promise<readonly chrome.cookies.CookieStore[]>;
    readonly getPartitionKey: (target: chrome.cookies.FrameDetails) => Promise<{
      readonly partitionKey: Readonly<chrome.cookies.CookiePartitionKey>;
    }>;
    readonly getAll: (query: CookieQuery) => Promise<readonly Readonly<chrome.cookies.Cookie>[]>;
  };
  readonly scripting: {
    readonly executeScript: (injection: chrome.scripting.ScriptInjection<[string], unknown>) =>
      Promise<readonly chrome.scripting.InjectionResult<unknown>[]>;
  };
};

export function readDocumentIdentity(browser: ChromeCaptureApi, selected: {
  readonly tabId: number;
  readonly url: string;
}): Promise<readonly chrome.scripting.InjectionResult<unknown>[]> {
  return browser.scripting.executeScript({
    target: { tabId: selected.tabId, frameIds: [0] },
    world: "MAIN",
    injectImmediately: true,
    args: [selected.url],
    func: (expectedUrl: string) => {
      const url = location.href;
      if (location.origin !== "https://gemini.google.com" || url !== expectedUrl) return null;
      const page: object = globalThis;
      const wiz: unknown = "WIZ_global_data" in page ? page.WIZ_global_data : undefined;
      if (typeof wiz !== "object" || wiz === null) return null;
      const primary: unknown = "S06Grb" in wiz ? wiz.S06Grb : undefined;
      const secondary: unknown = "W3Yyqf" in wiz ? wiz.W3Yyqf : undefined;
      const tertiary: unknown = "qDCSke" in wiz ? wiz.qDCSke : undefined;
      return {
        url,
        S06Grb: typeof primary === "string" ? primary : null,
        W3Yyqf: typeof secondary === "string" ? secondary : null,
        qDCSke: typeof tertiary === "string" ? tertiary : null,
      };
    },
  });
}
