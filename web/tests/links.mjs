// Bare-URL linking for plain text (question prompts, answers, summaries,
// plain-text messages). Runs under plain Node via type stripping:
//
//   node --test tests/links.mjs
//
// from web/.
import { describe, it } from "node:test";
import assert from "node:assert/strict";

const { linkSegments } = await import("../src/lib/links.ts");
const urls = (text) => linkSegments(text).filter((s) => typeof s !== "string").map((s) => s.url);

describe("linkSegments", () => {
  it("keeps text around a URL in order", () => {
    assert.deepEqual(linkSegments("Report at https://a.neuralcolumn.com/ — have a look"), [
      "Report at ",
      { url: "https://a.neuralcolumn.com/" },
      " — have a look",
    ]);
  });

  it("leaves text without URLs alone", () => {
    assert.deepEqual(linkSegments("No links here."), ["No links here."]);
    assert.deepEqual(linkSegments(""), []);
  });

  it("drops sentence punctuation and emphasis after a URL", () => {
    assert.deepEqual(urls("See https://gpufinder.dev/."), ["https://gpufinder.dev/"]);
    assert.deepEqual(urls("Is it https://x.dev/a?, or not"), ["https://x.dev/a"]);
    assert.deepEqual(urls("**https://x.dev/report**"), ["https://x.dev/report"]);
    assert.deepEqual(urls("`https://x.dev/cmd`"), ["https://x.dev/cmd"]);
    assert.deepEqual(urls("“https://x.dev/q”"), ["https://x.dev/q"]);
    assert.deepEqual(urls("https://x.dev/a—and more"), ["https://x.dev/a"]);
  });

  it("keeps brackets the URL opened and drops ones it did not", () => {
    assert.deepEqual(urls("(see https://x.dev/page)"), ["https://x.dev/page"]);
    assert.deepEqual(urls("https://en.wikipedia.org/wiki/Fish_(food)"), ["https://en.wikipedia.org/wiki/Fish_(food)"]);
    assert.deepEqual(urls("[https://x.dev/a]"), ["https://x.dev/a"]);
  });

  it("keeps queries and fragments", () => {
    assert.deepEqual(urls("go https://x.dev/s?q=1&r=2#top now"), ["https://x.dev/s?q=1&r=2#top"]);
  });

  it("finds several URLs and only http(s) ones", () => {
    assert.deepEqual(urls("read https://a.dev/ then http://b.dev/x, not javascript:alert(1) or ftp://c.dev"), ["https://a.dev/", "http://b.dev/x"]);
  });

  it("does not link a scheme with no host", () => {
    assert.deepEqual(urls("type https:// then the host"), []);
  });

  it("covers the text exactly", () => {
    const text = "A (https://x.dev/a). B: https://y.dev/b?c=d — end";
    assert.equal(linkSegments(text).map((s) => (typeof s === "string" ? s : s.url)).join(""), text);
  });
});
