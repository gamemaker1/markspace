import { test } from "node:test";
import assert from "node:assert/strict";
import { register } from "node:module";

// Browser modules import each other as /static/js/...; map that to the files.
register("data:text/javascript," + encodeURIComponent(`
export async function resolve(specifier, context, next) {
  if (specifier.startsWith("/static/")) {
    return { url: new URL("../../../internal/web/static/" + specifier.slice(8), ${JSON.stringify(import.meta.url)}).href, shortCircuit: true };
  }
  return next(specifier, context);
}`));

const {
  parseSSE, relativeTime, threadComments, accessRank, formatBytes,
} = await import("../../../internal/web/static/js/panels.js");

test("editor.js, panels.js and ui.js link together", async () => {
  // A missing named export between the modules fails this import.
  const editor = await import("../../../internal/web/static/js/editor.js");
  const panels = await import("../../../internal/web/static/js/panels.js");
  assert.equal(typeof editor.mountEditor, "function");
  for (const name of ["openComments", "openShare", "openHistory", "openAI"]) {
    assert.equal(typeof panels[name], "function", name);
  }
});

test("parseSSE splits frames and keeps the tail", () => {
  const { events, rest } = parseSSE('event: delta\ndata: {"delta":"Hel"}\n\nevent: delta\ndata: {"delta":"lo"}\n\nevent: do');
  assert.deepEqual(events, [
    { event: "delta", data: '{"delta":"Hel"}' },
    { event: "delta", data: '{"delta":"lo"}' },
  ]);
  assert.equal(rest, "event: do");
});

test("parseSSE handles comments, CRLF, multi-line data and flush", () => {
  const { events } = parseSSE(": ping\r\n\r\ndata: a\r\ndata: b\r\n\r\n");
  assert.deepEqual(events, [{ event: "message", data: "a\nb" }]);
  const flushed = parseSSE('event: done\ndata: {"done":true}', true);
  assert.deepEqual(flushed.events, [{ event: "done", data: '{"done":true}' }]);
  assert.equal(flushed.rest, "");
  assert.deepEqual(parseSSE("data:x\n\n").events, [{ event: "message", data: "x" }]);
});

test("relativeTime", () => {
  const now = Date.parse("2026-09-13T12:00:00Z");
  assert.equal(relativeTime("2026-09-13T11:59:40Z", now), "just now");
  assert.equal(relativeTime("2026-09-13T11:55:00Z", now), "5 min ago");
  assert.equal(relativeTime("2026-09-13T09:00:00Z", now), "3 h ago");
  assert.equal(relativeTime("2026-09-12T10:00:00Z", now), "yesterday");
  assert.equal(relativeTime("2026-09-09T12:00:00Z", now), "4 days ago");
  assert.ok(relativeTime("2025-01-01T00:00:00Z", now).includes("2025"));
  assert.equal(relativeTime("garbage", now), "");
});

test("threadComments nests flat lists and keeps nested ones", () => {
  const flat = [
    { id: "r2", createdAt: "2026-01-02" },
    { id: "c1", parentId: "r1", createdAt: "2026-01-03" },
    { id: "r1", createdAt: "2026-01-01" },
    { id: "orphan", parentId: "gone", createdAt: "2026-01-04" },
  ];
  const out = threadComments(flat);
  assert.deepEqual(out.map((c) => c.id), ["r1", "r2", "orphan"]);
  assert.deepEqual(out[0].children.map((c) => c.id), ["c1"]);
  const nested = [{ id: "a", children: [{ id: "b" }] }];
  assert.equal(threadComments(nested), nested);
  assert.deepEqual(threadComments(null), []);
});

test("accessRank and formatBytes", () => {
  assert.ok(accessRank("manage") > accessRank("edit"));
  assert.ok(accessRank("edit") > accessRank("comment"));
  assert.equal(accessRank("bogus"), 0);
  assert.equal(formatBytes(512), "512 B");
  assert.equal(formatBytes(2048), "2.0 KB");
  assert.equal(formatBytes(3 * 1048576), "3.0 MB");
});

