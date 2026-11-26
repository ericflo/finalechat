// How a relayed agent's report that arrives as several posts reads. Runs
// under plain Node via type stripping:
//
//   node --test tests/reports.mjs
//
// from web/.
import { describe, it } from "node:test";
import assert from "node:assert/strict";

const { reportPart, reportText } = await import("../src/lib/reports.ts");

describe("a report part", () => {
  it("is read from meta part and parts", () => {
    assert.deepEqual(reportPart({ kind: "relay_report", part: 2, parts: 3 }), { part: 2, parts: 3 });
    assert.equal(reportPart({ kind: "relay_report" }), null);
  });

  it("is not claimed from numbers that do not describe one", () => {
    assert.equal(reportPart({ part: 1, parts: 1 }), null);
    assert.equal(reportPart({ part: 0, parts: 3 }), null);
    assert.equal(reportPart({ part: 4, parts: 3 }), null);
    assert.equal(reportPart({ part: 1.5, parts: 3 }), null);
    assert.equal(reportPart({ part: "1", parts: "3" }), null);
    assert.equal(reportPart({ parts: 3 }), null);
  });
});

describe("a report part's text", () => {
  const p = { part: 2, parts: 3 };

  it("drops the closing marker for its own numbers", () => {
    assert.equal(reportText("Shipped lava; sky next.\n\nMore here.\n\n_(part 2 of 3)_", p), "Shipped lava; sky next.\n\nMore here.");
    assert.equal(reportText("Text\n\n_(part 2 of 3)_\n", p), "Text");
    assert.equal(reportText("```go\nx := 1\n```\n\n_(part 2 of 3)_", p), "```go\nx := 1\n```");
  });

  it("keeps a last line that is not exactly that marker", () => {
    assert.equal(reportText("Text\n\n_(part 1 of 3)_", p), "Text\n\n_(part 1 of 3)_");
    assert.equal(reportText("Text\n\nsee _(part 2 of 3)_", p), "Text\n\nsee _(part 2 of 3)_");
    // The marker on the first line (an older form) is left as it is.
    assert.equal(reportText("_(part 2 of 3)_\n\nText", p), "_(part 2 of 3)_\n\nText");
  });

  it("is the body unchanged for a report in one post", () => {
    assert.equal(reportText("Done.\n\n_(part 2 of 3)_", null), "Done.\n\n_(part 2 of 3)_");
    assert.equal(reportText("Done.\n", null), "Done.\n");
  });
});
