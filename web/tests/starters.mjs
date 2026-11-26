// How multiple connected hosts are named and chosen on /new. Runs under plain
// Node via type stripping:
//
//   node --test tests/starters.mjs
//
// from web/.
import { describe, it } from "node:test";
import assert from "node:assert/strict";

const { starterQualifier, starterDisplayName, pickDefaultStarter, pickableStarters } = await import("../src/lib/starters.ts");

const mk = (id, label, name, online = true, provider = "eagent") => ({
  resource: { id, label },
  connector: { id: `c-${id}`, name, provider },
  online,
});

describe("starter display names", () => {
  it("extracts the host qualifier from standard eagent connector names", () => {
    assert.equal(starterQualifier("eagent · llmbox · Development", "eagent", "Development"), "llmbox");
    assert.equal(starterQualifier("eagent · macbook · eagent", "eagent", "eagent"), "macbook");
    assert.equal(starterDisplayName(mk("1", "Development", "eagent · llmbox · Development")), "llmbox · Development");
    assert.equal(starterDisplayName(mk("2", "eagent", "eagent · macbook · eagent")), "macbook · eagent");
  });

  it("avoids redundancy when the connector name adds nothing beyond provider and label", () => {
    assert.equal(starterQualifier("eagent · relay commands", "eagent", "Relay commands"), "");
    assert.equal(starterDisplayName(mk("1", "Relay commands", "eagent · relay commands")), "Relay commands");
    assert.equal(starterDisplayName(mk("2", "Development", "eagent")), "Development");
  });

  it("handles custom connector names and non-eagent providers", () => {
    assert.equal(starterQualifier("Claude Code · studio · app", "claude-code", "app"), "studio");
    assert.equal(starterDisplayName(mk("1", "app", "Claude Code · studio · app", true, "claude-code")), "studio · app");
    assert.equal(starterDisplayName(mk("2", "eagent", "MacBook Pro")), "MacBook Pro · eagent");
  });
});

describe("default starter selection", () => {
  const a = mk("a", "Development", "eagent · llmbox · Development", true);
  const b = mk("b", "Development", "eagent · macbook · Development", true);
  const off = mk("off", "Development", "eagent · oldbox · Development", false);

  it("prefers the remembered starter when it is online", () => {
    assert.equal(pickDefaultStarter([a, b], "b"), b);
  });

  it("falls back to an online starter when the remembered one is offline", () => {
    assert.equal(pickDefaultStarter([off, b], "off"), b);
  });

  it("falls back to the remembered or first starter when all are offline", () => {
    const off2 = mk("off2", "Development", "eagent · other · Development", false);
    assert.equal(pickDefaultStarter([off, off2], "off2"), off2);
    assert.equal(pickDefaultStarter([off, off2], null), off);
  });
});

describe("pickable starters", () => {
  const a = mk("a", "Development", "eagent · llmbox · Development", true);
  const b = mk("b", "Development", "eagent · macbook · Development", true);
  const stale = mk("stale", "Old", "eagent · oldbox · Old", false);

  it("hides unrelated offline connectors and puts online starters first", () => {
    assert.deepEqual(pickableStarters([stale, a, b], "a"), [a, b]);
    assert.deepEqual(pickableStarters([stale, a], "a"), [a]);
  });

  it("keeps the currently viewed starter even if it went offline", () => {
    assert.deepEqual(pickableStarters([stale, a], "stale"), [a, stale]);
  });
});
