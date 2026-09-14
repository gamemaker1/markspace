// Tests for the pure comment helpers of the side panel: thread grouping and
// order, counts, the reading view button placement and the comment source.
import { test } from "node:test";
import assert from "node:assert/strict";
import { register } from "node:module";

// Browser modules import each other as /static/js/...; map that to the files.
register("data:text/javascript," + encodeURIComponent(`
export async function resolve(specifier, context, next) {
  if (specifier.startsWith("/static/")) {
    return { url: new URL("../../internal/web/static/" + specifier.slice(8), ${JSON.stringify(import.meta.url)}).href, shortCircuit: true };
  }
  return next(specifier, context);
}`));

const { groupThreads, threadPosition, commentCounts, commentButtonPosition, createCommentSource, quoteText } = await import("../../internal/web/static/js/panels.js");
const { pickRailTab } = await import("../../internal/web/static/js/views.js");

const body = "Alpha beta gamma. Beta again here.";
const anchored = (id, exact, start, extra = {}) => ({
  id, body: id, createdAt: "2026-09-13T09:00:0" + id.slice(-1) + "Z",
  anchor: { exact, prefix: "", suffix: "", start, end: start + exact.length }, anchorState: "exact", ...extra,
});

test("groupThreads orders anchored threads by position, then general, orphaned and resolved", () => {
  const threads = [
    { id: "g1", body: "general", createdAt: "2026-09-13T09:00:05Z" },
    anchored("a2", "again", 23),
    anchored("a1", "beta", 6),
    anchored("o1", "vanished", 0, { anchorState: "orphaned" }),
    anchored("r1", "gamma", 11, { resolved: true }),
    { id: "r2", body: "done", createdAt: "2026-09-13T09:00:01Z", resolved: true },
  ];
  const groups = groupThreads(threads);
  assert.deepEqual(groups.anchored.map((e) => e.thread.id), ["a1", "a2"]);
  assert.deepEqual(groups.general.map((e) => e.thread.id), ["g1"]);
  assert.deepEqual(groups.orphaned.map((e) => e.thread.id), ["o1"]);
  assert.deepEqual(groups.resolved.map((e) => e.thread.id), ["r1", "r2"]);
  assert.equal(groups.open, 4);
  assert.deepEqual(commentCounts(threads), { open: 4, resolved: 2, total: 6 });
});

test("groupThreads breaks position ties by end, then by creation time", () => {
  const threads = [anchored("b2", "beta gamma", 6), anchored("b3", "beta", 6), anchored("b1", "beta", 6)];
  assert.deepEqual(groupThreads(threads).anchored.map((e) => e.thread.id), ["b1", "b3", "b2"]);
});

test("groupThreads drops deleted roots without replies and keeps ones with replies", () => {
  const threads = [
    { id: "d1", deletedAt: "2026-09-13T10:00:00Z", createdAt: "x" },
    { id: "d2", deletedAt: "2026-09-13T10:00:00Z", createdAt: "y", children: [{ id: "r", body: "still here" }] },
    { id: "d3", deletedAt: "2026-09-13T10:00:00Z", createdAt: "z", children: [{ id: "r2", deletedAt: "2026-09-13T10:00:00Z" }] },
  ];
  assert.deepEqual(groupThreads(threads).general.map((e) => e.thread.id), ["d2"]);
  assert.equal(commentCounts(threads).open, 1);
});

test("threadPosition keeps server offsets that still hold the text", () => {
  const calls = [];
  const resolve = (...args) => { calls.push(args); return { start: 0, end: 0, state: "orphaned" }; };
  assert.deepEqual(threadPosition(anchored("a", "beta", 6), body, resolve), { state: "exact", start: 6, end: 10 });
  assert.equal(calls.length, 0);
});

test("threadPosition resolves locally when the body changed since the server listed it", () => {
  const edited = "New intro. " + body;
  const resolve = (text, anchor) => {
    const at = text.indexOf(anchor.exact);
    return at < 0 ? { start: -1, end: -1, state: "orphaned" } : { start: at, end: at + anchor.exact.length, state: "moved" };
  };
  assert.deepEqual(threadPosition(anchored("a", "beta", 6), edited, resolve), { state: "moved", start: 17, end: 21 });
  assert.deepEqual(threadPosition(anchored("a", "delta", 6), edited, resolve), { state: "orphaned", start: -1, end: -1 });
  // A passage the server lost can come back when the local text still has it.
  assert.equal(threadPosition(anchored("a", "intro", 0, { anchorState: "orphaned" }), edited, resolve).state, "moved");
  const groups = groupThreads([anchored("x", "delta", 6), anchored("y", "Alpha", 0)], { body: edited, resolve });
  assert.deepEqual(groups.orphaned.map((e) => e.thread.id), ["x"]);
  assert.deepEqual(groups.anchored.map((e) => [e.thread.id, e.start]), [["y", 11]]);
});

test("threadPosition treats a missing or empty anchor as general", () => {
  assert.equal(threadPosition({ id: "g" }).state, "general");
  assert.equal(threadPosition({ id: "g", anchor: { exact: "" } }).state, "general");
});

const viewport = { width: 1200, height: 800, top: 52, bottom: 0 };
const button = { width: 100, height: 38 };

test("commentButtonPosition centres the desktop toolbar above the selection", () => {
  const sel = { top: 300, bottom: 350, left: 400, right: 600 };
  assert.deepEqual(commentButtonPosition(sel, sel, button, viewport), { left: 450, top: 254, placement: "above" });
});

test("commentButtonPosition flips below when the top bar is in the way", () => {
  const sel = { top: 70, bottom: 92, left: 400, right: 600 };
  assert.deepEqual(commentButtonPosition(sel, sel, button, viewport), { left: 450, top: 100, placement: "below" });
});

test("commentButtonPosition keeps the button inside the viewport horizontally", () => {
  const left = { top: 300, bottom: 320, left: 0, right: 40 };
  assert.equal(commentButtonPosition(left, left, button, viewport).left, 8);
  const right = { top: 300, bottom: 320, left: 1180, right: 1200 };
  assert.equal(commentButtonPosition(right, right, button, viewport).left, 1092);
});

const phone = { width: 390, height: 844, top: 54, bottom: 60 };
const fab = { width: 120, height: 44 };

test("commentButtonPosition puts the touch button below the selection end, past the handle", () => {
  const sel = { top: 200, bottom: 260, left: 20, right: 360 };
  const end = { top: 238, bottom: 260, left: 20, right: 180 };
  assert.deepEqual(commentButtonPosition(sel, end, fab, phone, { touch: true }), { left: 120, top: 288, placement: "below" });
});

test("commentButtonPosition moves the touch button above the selection menu near the bottom bar", () => {
  const sel = { top: 640, bottom: 740, left: 20, right: 360 };
  const end = { top: 718, bottom: 740, left: 20, right: 380 };
  assert.deepEqual(commentButtonPosition(sel, end, fab, phone, { touch: true }), { left: 262, top: 540, placement: "above" });
});

test("commentButtonPosition clamps a touch selection that fills the screen", () => {
  const sel = { top: 60, bottom: 780, left: 20, right: 360 };
  const pos = commentButtonPosition(sel, { top: 760, bottom: 780, left: 20, right: 200 }, fab, phone, { touch: true });
  assert.equal(pos.top, 844 - 60 - 44 - 8);
  assert.equal(pos.placement, "below");
});

test("pickRailTab keeps a saved tab the rail has and falls back to the first", () => {
  assert.equal(pickRailTab("comments", ["outline", "info", "comments"]), "comments");
  assert.equal(pickRailTab("outline", ["info", "comments"]), "info");
  assert.equal(pickRailTab(null, []), "");
});

/** Returns a promise and its resolver, for a fake api call held open. */
function deferred() {
  let resolve;
  const promise = new Promise((r) => { resolve = r; });
  return { promise, resolve };
}

test("createCommentSource shows the cache first, then the server list, and caches it", async () => {
  const cached = [{ id: "old", body: "cached", createdAt: "1" }];
  const fresh = [{ id: "c1", body: "root", createdAt: "1" }, { id: "c2", parentId: "c1", body: "reply", createdAt: "2" }];
  const put = [];
  const store = { listComments: async () => cached, putComments: async (id, list) => { put.push([id, list]); } };
  const calls = [];
  const api = async (method, path) => { calls.push(method + " " + path); return { items: fresh }; };
  const source = createCommentSource({ noteId: "n 1", api, store, isOnline: () => true });
  const seen = [];
  source.subscribe((threads, state) => seen.push([threads.map((t) => t.id), state.cached]));
  await source.load();
  await new Promise((r) => setTimeout(r, 0));
  assert.deepEqual(calls, ["GET /api/notes/n%201/comments"]);
  assert.deepEqual(seen, [[["old"], true], [["c1"], false]]);
  assert.equal(source.threads[0].children[0].id, "c2");
  assert.deepEqual(put, [["n 1", fresh]]);
});

test("quoteText strips markdown markup from quoted passages", () => {
  assert.equal(quoteText("sync** engine"), "sync engine");
  assert.equal(quoteText("The **bold claim** needs"), "The bold claim needs");
  assert.equal(quoteText("see [the docs](https://example.com/a) first"), "see the docs first");
  assert.equal(quoteText("k](https://example.com) and more"), "k and more");
  assert.equal(quoteText("[[Target|Label]] and [[Other]]"), "Label and Other");
  assert.equal(quoteText("## Heading\n- [ ] task item\n> quoted"), "Heading\ntask item\nquoted");
  assert.equal(quoteText("_em_ and `code` and ~~gone~~"), "em and code and gone");
  assert.equal(quoteText("snake_case_name stays"), "snake_case_name stays");
});

test("createCommentSource does not treat an empty cache as loaded", async () => {
  const gate = deferred();
  const store = { listComments: async () => [] };
  const source = createCommentSource({ noteId: "n", api: () => gate.promise, store, isOnline: () => true });
  const states = [];
  source.subscribe((threads, state) => states.push(state.loaded));
  const done = source.load();
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(source.state.loaded, false);
  gate.resolve({ items: [{ id: "s", createdAt: "1" }] });
  await done;
  assert.deepEqual(states, [true]);
  assert.deepEqual(source.threads.map((t) => t.id), ["s"]);
});

test("createCommentSource keeps cached threads offline and on a network error", async () => {
  const store = { listComments: async () => [{ id: "k", body: "kept", createdAt: "1" }] };
  let online = false;
  let fail = false;
  const api = async () => {
    if (fail) { const err = new Error("down"); err.status = 0; throw err; }
    return { items: [] };
  };
  const source = createCommentSource({ noteId: "n", api, store, isOnline: () => online });
  await source.load();
  assert.deepEqual(source.threads.map((t) => t.id), ["k"]);
  assert.equal(source.state.offline, true);
  online = true;
  fail = true;
  await source.load();
  assert.deepEqual(source.threads.map((t) => t.id), ["k"]);
  assert.equal(source.state.offline, true);
});

test("createCommentSource coalesces loads requested while one runs", async () => {
  const gates = [];
  let calls = 0;
  const api = () => { calls++; const d = deferred(); gates.push(d); return d.promise; };
  const source = createCommentSource({ noteId: "n", api, isOnline: () => true });
  const first = source.load();
  source.load();
  source.load();
  await new Promise((r) => setTimeout(r, 0));
  gates[0].resolve({ items: [] });
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(calls, 2);
  gates[1].resolve({ items: [{ id: "late", createdAt: "1" }] });
  await first;
  assert.deepEqual(source.threads.map((t) => t.id), ["late"]);
  assert.equal(source.state.loaded, true);
});
