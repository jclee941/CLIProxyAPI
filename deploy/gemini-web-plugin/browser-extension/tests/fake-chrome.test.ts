import { expect, test } from "bun:test";
import { FIRST_PARTY, FakeChrome, OTHER_STORE, SELECTED_STORE, cookie } from "./fake-chrome.ts";

test("the cookie fake filters store, domain, host-only, path and partition independently", async () => {
  const browser = new FakeChrome();
  browser.jar.push(cookie({ name: "PARTITIONED", partitionKey: { ...FIRST_PARTY } }));

  const result = await browser.cookies.getAll({ storeId: SELECTED_STORE, url: "https://gemini.google.com/u/2/app" });

  expect(result.map((entry) => entry.name).sort()).toEqual(["EMPTY", "HOST_COOKIE", "NEW_SESSION_COOKIE", "SAPISID", "SID"]);
  expect(result.find((entry) => entry.name === "SID")?.httpOnly).toBe(true);
});

test("the cookie fake returns the other store's fixture when it is explicitly requested", async () => {
  const browser = new FakeChrome();

  const result = await browser.cookies.getAll({ storeId: OTHER_STORE, url: "https://gemini.google.com/app" });

  expect(result.map((entry) => entry.value)).toEqual(["SYNTHETIC-other-profile"]);
});

test("the cookie fake separates matching partitions from unpartitioned cookies", async () => {
  const browser = new FakeChrome();
  browser.jar.push(cookie({ name: "PARTITIONED", partitionKey: { ...FIRST_PARTY } }));

  const result = await browser.cookies.getAll({ storeId: SELECTED_STORE, url: "https://gemini.google.com/app", partitionKey: FIRST_PARTY });

  expect(result.map((entry) => entry.name)).toEqual(["PARTITIONED"]);
});
