// What the inbox shows for a thread and in what order. Runs under plain Node
// via type stripping:
//
//   node --test tests/inbox.mjs
//
// from web/.
import { describe, it } from "node:test";
import assert from "node:assert/strict";

const { agentsDiffer, busyFor, byAttentionThenActivity, heldOrder, inboxViewPath, inView, isWorking, needsUser, orderHoldMs, parseInboxView, relayBlocked, rowState, sameOrder, threadSummary, threadTitle } = await import("../src/lib/inbox.ts");

const now = Date.parse("2026-11-23T12:00:00Z");
const iso = (ms) => new Date(ms).toISOString();
const thread = (over = {}) => ({
  id: "t1",
  title: "Checkout race fix",
  agent: "eagent",
  description: "",
  summary: "",
  preview: "",
  archived_at: null,
  unread_count: 0,
  pending_questions: 0,
  last_activity_at: iso(now - 60000),
  activity: null,
  ...over,
});
const busy = (kind = "working", expiresIn = 60000) => ({ text: "Running tests", kind, at: iso(now - 5000), since: iso(now - 12 * 60000), expires_at: iso(now + expiresIn) });

describe("a relayed agent stopped on its screen", () => {
  it("is told apart from one that is only ready, by the relay's state", () => {
    assert.equal(relayBlocked(thread({ meta: { relay: { name: "claude", state: "blocked" } } })), true);
    assert.equal(relayBlocked(thread({ meta: { relay: { name: "claude", state: "idle" } } })), false);
    assert.equal(relayBlocked(thread({ meta: { relay: null } })), false);
    assert.equal(relayBlocked(thread({ meta: {} })), false);
    assert.equal(relayBlocked(undefined), false);
  });
  it("is still not working", () => {
    assert.equal(isWorking(thread({ meta: { relay: { state: "blocked" } }, activity: busy("waiting") }), now), false);
  });
});

describe("a thread that needs you", () => {
  const blocked = (over = {}) => thread({ meta: { relay: { name: "claude", state: "blocked" } }, activity: busy("waiting"), ...over });
  const live = { t1: { thread_id: "t1" } };

  it("is one with a question waiting, or a running agent stopped on its screen", () => {
    assert.equal(needsUser(thread({ pending_questions: 1 }), now), true);
    assert.equal(needsUser(blocked(), now, live), true);
    assert.equal(rowState(blocked(), now, live), "needs-you");
  });

  it("is not one whose agent is only ready, or whose blocked state is left over", () => {
    assert.equal(needsUser(thread({ meta: { relay: { state: "idle" } }, activity: busy("waiting") }), now, live), false);
    // No session running: the relay state is what the last session left.
    assert.equal(needsUser(blocked(), now, {}), false);
    // The waiting status lapsed.
    assert.equal(needsUser(blocked({ activity: busy("waiting", -1) }), now, live), false);
    assert.equal(needsUser(blocked({ archived_at: iso(now) }), now, live), false);
    assert.equal(rowState(blocked(), now, {}), "idle");
  });

  it("comes first in the order, like a question", () => {
    const a = thread({ id: "a", last_activity_at: iso(now - 1000) });
    const b = blocked({ id: "b", last_activity_at: iso(now - 5000) });
    const lives = { b: { thread_id: "b" } };
    assert.deepEqual([a, b].sort((x, y) => byAttentionThenActivity(x, y, now, lives)).map((t) => t.id), ["b", "a"]);
    assert.deepEqual([a, b].sort((x, y) => byAttentionThenActivity(x, y, now, {})).map((t) => t.id), ["a", "b"]);
  });
});

describe("row state", () => {
  it("is working only while the agent says so and the status is fresh", () => {
    assert.equal(isWorking(thread({ activity: busy() }), now), true);
    assert.equal(isWorking(thread({ activity: busy("working", -1) }), now), false);
    assert.equal(isWorking(thread({ activity: busy("waiting") }), now), false);
    assert.equal(isWorking(thread(), now), false);
  });

  it("puts a waiting question above work, and work above unread", () => {
    assert.equal(rowState(thread({ pending_questions: 1, activity: busy(), unread_count: 3 }), now), "needs-you");
    assert.equal(rowState(thread({ activity: busy(), unread_count: 3 }), now), "working");
    assert.equal(rowState(thread({ unread_count: 3 }), now), "unread");
    assert.equal(rowState(thread(), now), "idle");
  });

  it("keeps archived rows idle even when trailing updates arrive", () => {
    assert.equal(rowState(thread({ archived_at: iso(now), pending_questions: 1, activity: busy(), unread_count: 3 }), now), "idle");
    assert.equal(rowState(thread({ archived_at: iso(now), activity: busy() }), now), "idle");
    assert.equal(rowState(thread({ archived_at: iso(now), unread_count: 2 }), now), "idle");
  });

  it("reads the summary from either name and never shows an empty title", () => {
    assert.equal(threadSummary(thread({ description: " Fix is in. " })), "Fix is in.");
    assert.equal(threadSummary(thread({ summary: "From the alias." })), "From the alias.");
    assert.equal(threadSummary(thread()), "");
    assert.equal(threadTitle(thread({ title: "" })), "eagent");
    assert.equal(threadTitle(thread({ title: "", agent: "" })), "Untitled thread");
  });
});

describe("views", () => {
  const archived = thread({ archived_at: iso(now) });
  it("keeps archived threads to their own view", () => {
    assert.equal(inView(archived, "archived", now), true);
    for (const v of ["all", "unread", "working"]) assert.equal(inView(archived, v, now), false);
    assert.equal(inView(thread(), "archived", now), false);
  });

  it("counts a waiting question as unread", () => {
    assert.equal(inView(thread({ unread_count: 2 }), "unread", now), true);
    assert.equal(inView(thread({ pending_questions: 1 }), "unread", now), true);
    assert.equal(inView(thread(), "unread", now), false);
  });

  it("lists only fresh work under Working", () => {
    assert.equal(inView(thread({ activity: busy() }), "working", now), true);
    assert.equal(inView(thread({ activity: busy("working", -1) }), "working", now), false);
  });

  it("lists threads whose session is running under Active, archived ones aside", () => {
    const t = thread();
    assert.equal(inView(t, "live", now, { [t.id]: {} }), true);
    assert.equal(inView(t, "live", now, {}), false);
    assert.equal(inView(t, "live", now), false);
    assert.equal(inView(archived, "live", now, { [archived.id]: {} }), false);
  });

  it("parses and formats inbox view query values", () => {
    for (const v of ["all", "unread", "working", "live", "archived"]) {
      assert.equal(parseInboxView(v), v);
    }
    assert.equal(parseInboxView(null), "all");
    assert.equal(parseInboxView(""), "all");
    assert.equal(parseInboxView("bogus"), "all");
    assert.equal(inboxViewPath("all"), "/");
    assert.equal(inboxViewPath("archived"), "/?view=archived");
    assert.equal(inboxViewPath("unread"), "/?view=unread");
  });
});

describe("order", () => {
  it("is questions first, then newest activity, with a stable tiebreak", () => {
    const a = thread({ id: "a", last_activity_at: iso(now - 1000) });
    const b = thread({ id: "b", last_activity_at: iso(now - 5000), pending_questions: 1 });
    const c = thread({ id: "c", last_activity_at: iso(now - 9000) });
    const d = thread({ id: "d", last_activity_at: iso(now - 9000) });
    const e = thread({ id: "e", last_activity_at: iso(now - 10000), pending_questions: 1, archived_at: iso(now - 2000) });
    assert.deepEqual([e, c, a, d, b].sort(byAttentionThenActivity).map((t) => t.id), ["b", "a", "d", "c", "e"]);
  });

  it("follows the latest order until the reader touches the list", () => {
    let hold = heldOrder(null, "all", ["a", "b", "c"], now);
    assert.deepEqual(hold.ids, ["a", "b", "c"]);
    hold = heldOrder(hold, "all", ["c", "a", "b"], now + 1000);
    assert.deepEqual(hold.ids, ["c", "a", "b"]);
  });

  it("holds the order the reader saw, with new threads on top", () => {
    let hold = { ...heldOrder(null, "all", ["a", "b", "c"], now), touchedAt: now };
    // b posts and would jump to the top; d is new; c was archived.
    hold = heldOrder(hold, "all", ["b", "d", "a"], now + 5000);
    assert.deepEqual(hold.ids, ["d", "a", "b"]);
    assert.equal(hold.touchedAt, now);
  });

  it("catches up once the list has been left alone", () => {
    const held = { ...heldOrder(null, "all", ["a", "b", "c"], now), touchedAt: now };
    assert.deepEqual(heldOrder(held, "all", ["b", "a", "c"], now + orderHoldMs - 1).ids, ["a", "b", "c"]);
    assert.deepEqual(heldOrder(held, "all", ["b", "a", "c"], now + orderHoldMs).ids, ["b", "a", "c"]);
  });

  it("starts another view, filter or search from the latest order, still held", () => {
    const held = { ...heldOrder(null, "all", ["a", "b", "c"], now), touchedAt: now };
    const next = heldOrder(held, "unread", ["c", "a"], now + 1000);
    assert.deepEqual(next.ids, ["c", "a"]);
    assert.equal(next.touchedAt, now);
    // Back on the first list it is the latest order again, and it holds.
    const back = heldOrder(next, "all", ["b", "a", "c"], now + 2000);
    assert.deepEqual(back.ids, ["b", "a", "c"]);
    assert.deepEqual(heldOrder(back, "all", ["c", "b", "a", "d"], now + 3000).ids, ["d", "b", "a", "c"]);
  });

  it("compares orders", () => {
    assert.equal(sameOrder(["a", "b"], ["a", "b"]), true);
    assert.equal(sameOrder(["a", "b"], ["b", "a"]), false);
    assert.equal(sameOrder(["a"], ["a", "b"]), false);
  });
});

describe("row details", () => {
  it("shows the agent only when the list has more than one", () => {
    assert.equal(agentsDiffer([thread(), thread({ id: "t2" })]), false);
    assert.equal(agentsDiffer([thread(), thread({ id: "t2", agent: "" })]), false);
    assert.equal(agentsDiffer([thread(), thread({ id: "t2", agent: "Codex" })]), true);
    assert.equal(agentsDiffer([]), false);
  });

  it("says how long an agent has been busy", () => {
    const since = (ms) => iso(now - ms);
    assert.equal(busyFor(since(20 * 1000), now), "now");
    assert.equal(busyFor(since(12 * 60000), now), "12m");
    assert.equal(busyFor(since(125 * 60000), now), "2h 05m");
    assert.equal(busyFor(since(27 * 3600000), now), "1d 3h");
    assert.equal(busyFor(since(-5000), now), "now");
  });
});
