// Client-capability capsule tests. Runs under plain Node against the real
// module via type stripping (no build step, no extra dependencies):
//
//   node --test tests/client-caps.mjs
//
// from web/. Covers the capsule shape eagent's capsFromMeta reads.
import { describe, it, beforeEach } from "node:test";
import assert from "node:assert/strict";
import { register } from "node:module";

// Lets plain Node resolve the web sources' extensionless relative imports.
register("./caps-resolve-hooks.mjs", import.meta.url);

const mod = await import("../src/lib/clientCaps.ts");

const ALLOWED_KEYS = new Set(["timezone", "locale", "device", "app", "screen", "supplies"]);

function stubBrowser({ language, mobile, coarse, width, height, timezone, appVersion } = {}) {
  delete globalThis.navigator;
  delete globalThis.window;
  if (language !== undefined || mobile !== undefined) {
    globalThis.navigator = {};
    if (language !== undefined) {
      Object.defineProperty(globalThis.navigator, "language", { value: language, configurable: true });
    }
    if (mobile !== undefined) {
      globalThis.navigator.userAgentData = { mobile };
    }
  }
  if (width !== undefined || coarse !== undefined) {
    globalThis.window = {
      ...(width !== undefined ? { screen: { width, height } } : {}),
      matchMedia: () => ({ matches: coarse === true }),
    };
  }
  if (timezone !== undefined) {
    const RealDTF = Intl.DateTimeFormat;
    Intl.DateTimeFormat = function (...args) {
      const fmt = new RealDTF(...args);
      const orig = fmt.resolvedOptions.bind(fmt);
      fmt.resolvedOptions = () => ({ ...orig(), timeZone: timezone });
      return fmt;
    };
    return () => {
      Intl.DateTimeFormat = RealDTF;
    };
  }
  return () => {};
}

function resetEnv(restoreIntl) {
  restoreIntl?.();
  delete globalThis.navigator;
  delete globalThis.window;
  delete globalThis.__APP_VERSION__;
  mod.resetClientCapsuleForTests();
}

describe("client capsule", () => {
  let restore = null;
  beforeEach(() => {
    resetEnv(restore);
    restore = null;
  });

  it("declares the full phone capsule eagent reads", () => {
    globalThis.__APP_VERSION__ = "9.9.9";
    restore = stubBrowser({ language: "en-US", mobile: true, coarse: true, width: 390, height: 844, timezone: "America/Los_Angeles" });
    const capsule = mod.getClientCapsule();
    assert.deepEqual(capsule, {
      timezone: "America/Los_Angeles",
      locale: "en-US",
      device: "phone",
      app: "finalechat-web/9.9.9",
      screen: "390x844",
      supplies: ["tz", "locale", "screen"],
    });
    const meta = mod.clientCapsMeta();
    assert.deepEqual(meta, { "eagent.client": capsule });
    for (const key of Object.keys(capsule)) assert.ok(ALLOWED_KEYS.has(key), `unexpected key ${key}`);
  });

  it("reports tablet for large coarse screens", () => {
    restore = stubBrowser({ language: "de-DE", mobile: true, coarse: true, width: 1024, height: 768, timezone: "Europe/Berlin" });
    const capsule = mod.getClientCapsule();
    assert.equal(capsule.device, "tablet");
    assert.equal(capsule.locale, "de-DE");
  });

  it("reports desktop without mobile hardware", () => {
    restore = stubBrowser({ language: "en-US", mobile: false, coarse: false, width: 1512, height: 982, timezone: "UTC" });
    const capsule = mod.getClientCapsule();
    assert.equal(capsule.device, "desktop");
    assert.equal(capsule.screen, "1512x982");
  });

  it("omits fields the browser denies, keeping supplies honest", () => {
    // navigator.language throws (private mode); screen unavailable.
    globalThis.navigator = {};
    Object.defineProperty(globalThis.navigator, "language", {
      get() {
        throw new Error("denied");
      },
      configurable: true,
    });
    globalThis.window = { matchMedia: () => ({ matches: false }) };
    const capsule = mod.getClientCapsule();
    assert.ok(!("locale" in capsule), `locale should be absent: ${JSON.stringify(capsule)}`);
    assert.ok(!("screen" in capsule), `screen should be absent: ${JSON.stringify(capsule)}`);
    assert.deepEqual(capsule.supplies, ["tz"]);
    assert.ok(!capsule.supplies.includes("locale") && !capsule.supplies.includes("screen"));
  });

  it("yields no meta when every probe fails instead of throwing", () => {
    const RealDTF = Intl.DateTimeFormat;
    Intl.DateTimeFormat = function () {
      throw new Error("denied");
    };
    try {
      // No navigator, no window, broken Intl: timezone/locale/device/screen
      // are all unreadable, so there is nothing worth declaring.
      assert.equal(mod.getClientCapsule(), null);
      assert.equal(mod.clientCapsMeta(), undefined);
    } finally {
      Intl.DateTimeFormat = RealDTF;
    }
  });

  it("never sends anything beyond the documented keys", () => {
    globalThis.__APP_VERSION__ = "1.2.3";
    restore = stubBrowser({ language: "en-US", mobile: false, coarse: false, width: 800, height: 600, timezone: "UTC" });
    const raw = JSON.stringify(mod.clientCapsMeta());
    assert.ok(!raw.includes("userAgent") && !raw.includes("canvas") && !raw.includes("battery"));
    for (const key of Object.keys(mod.getClientCapsule())) assert.ok(ALLOWED_KEYS.has(key));
  });

  it("gathers once per page load", () => {
    restore = stubBrowser({ language: "en-US", mobile: false, coarse: false, width: 800, height: 600, timezone: "UTC" });
    assert.equal(mod.getClientCapsule(), mod.getClientCapsule());
  });
});

const api = await import("../src/lib/api.ts");

describe("api request bodies", () => {
  let restore = null;
  let lastRequest = null;
  beforeEach(() => {
    resetEnv(restore);
    restore = null;
    lastRequest = null;
    globalThis.fetch = async (url, init) => {
      lastRequest = { url, body: JSON.parse(init.body) };
      const payload =
        String(url).includes("/answer") ? { question: {}, message: {}, thread: {} } : { message: {}, thread: {} };
      return { status: 200, ok: true, text: async () => JSON.stringify(payload) };
    };
  });

  it("sendMessage posts the capsule in meta", async () => {
    globalThis.__APP_VERSION__ = "9.9.9";
    restore = stubBrowser({ language: "en-US", mobile: true, coarse: true, width: 390, height: 844, timezone: "America/Los_Angeles" });
    await api.api.sendMessage("thread-1", "hi");
    assert.ok(lastRequest.url.endsWith("/threads/thread-1/messages"));
    assert.deepEqual(lastRequest.body.meta, { "eagent.client": mod.getClientCapsule() });
    assert.equal(lastRequest.body.meta["eagent.client"].timezone, "America/Los_Angeles");
  });

  it("answerQuestion posts the capsule in meta", async () => {
    globalThis.__APP_VERSION__ = "9.9.9";
    restore = stubBrowser({ language: "en-US", mobile: false, coarse: false, width: 1512, height: 982, timezone: "UTC" });
    await api.api.answerQuestion("q-1", { selected: ["Yes"] });
    assert.ok(lastRequest.url.endsWith("/questions/q-1/answer"));
    assert.deepEqual(lastRequest.body.selected, ["Yes"]);
    assert.deepEqual(lastRequest.body.meta, { "eagent.client": mod.getClientCapsule() });
  });

  it("both routes omit meta when the capsule is empty", async () => {
    const RealDTF = Intl.DateTimeFormat;
    Intl.DateTimeFormat = function () {
      throw new Error("denied");
    };
    try {
      await api.api.sendMessage("thread-1", "hi");
      assert.ok(!("meta" in lastRequest.body), `meta should be absent: ${JSON.stringify(lastRequest.body)}`);
      await api.api.answerQuestion("q-1", { selected: ["Yes"] });
      assert.ok(!("meta" in lastRequest.body), `meta should be absent: ${JSON.stringify(lastRequest.body)}`);
    } finally {
      Intl.DateTimeFormat = RealDTF;
    }
  });
});
