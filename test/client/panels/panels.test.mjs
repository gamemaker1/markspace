// Tests for the pure helpers of the rail panels: tab state, badge counts,
// version size changes, access labels, link state, outlines and word marks.
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
  RAIL_TABS, railTabIds, tabKeyTarget, badgeText, versionDeltas, formatDelta, byteLength,
  accessLabel, accessChoices, linkSummary, outlineOf, wordChange,
} = await import("../../../internal/web/static/js/panels.js");

test("the rail has eight tabs with the names other scripts select", () => {
  assert.deepEqual(RAIL_TABS.map((t) => t.label), ["Outline", "Comments", "Suggestions", "History", "Share", "Files", "AI", "Info"]);
  for (const tab of RAIL_TABS) {
    assert.match(tab.id, /^[a-z]+$/);
    assert.ok(tab.icon, tab.id + " has an icon");
  }
});

test("railTabIds hides Share below manage and AI when off or offline", () => {
  assert.deepEqual(railTabIds({ access: "manage", ai: true }), ["outline", "comments", "suggestions", "history", "share", "files", "ai", "info"]);
  assert.deepEqual(railTabIds({ access: "edit", ai: true }), ["outline", "comments", "suggestions", "history", "files", "ai", "info"]);
  assert.deepEqual(railTabIds({ access: "view", ai: false }), ["outline", "comments", "suggestions", "history", "files", "info"]);
  assert.deepEqual(railTabIds({ access: "manage", ai: true, online: false }), ["outline", "comments", "suggestions", "history", "share", "files", "info"]);
  assert.ok(!railTabIds({ suggestions: false }).includes("suggestions"));
  // The AI tab in view stays while the connection is gone.
  assert.ok(railTabIds({ access: "edit", ai: true, online: false, keep: "ai" }).includes("ai"));
  assert.ok(!railTabIds({ access: "edit", ai: false, online: true, keep: "ai" }).includes("ai"));
  assert.ok(!railTabIds({ comments: false }).includes("comments"));
  assert.deepEqual(railTabIds(), ["outline", "comments", "suggestions", "history", "files", "info"]);
});

test("tabKeyTarget wraps arrows and jumps with Home and End", () => {
  const ids = ["outline", "comments", "history", "info"];
  assert.equal(tabKeyTarget(ids, "outline", "ArrowRight"), "comments");
  assert.equal(tabKeyTarget(ids, "info", "ArrowRight"), "outline");
  assert.equal(tabKeyTarget(ids, "outline", "ArrowLeft"), "info");
  assert.equal(tabKeyTarget(ids, "history", "ArrowUp"), "comments");
  assert.equal(tabKeyTarget(ids, "history", "ArrowDown"), "info");
  assert.equal(tabKeyTarget(ids, "history", "Home"), "outline");
  assert.equal(tabKeyTarget(ids, "outline", "End"), "info");
  assert.equal(tabKeyTarget(ids, "outline", "Enter"), "");
  assert.equal(tabKeyTarget(ids, "gone", "ArrowRight"), "outline");
  assert.equal(tabKeyTarget([], "outline", "ArrowRight"), "");
});

test("badgeText shows counts that mean something", () => {
  assert.equal(badgeText(0), "");
  assert.equal(badgeText(-2), "");
  assert.equal(badgeText(undefined), "");
  assert.equal(badgeText(3), "3");
  assert.equal(badgeText(99), "99");
  assert.equal(badgeText(100), "99+");
});

test("versionDeltas sorts newest first and compares with the next older version", () => {
  const rows = versionDeltas([
    { id: "a", size: 324, createdAt: "2026-09-13T05:41:39.480Z" },
    { id: "c", size: 448, createdAt: "2026-09-13T05:41:39.490Z" },
    { id: "b", size: 404, createdAt: "2026-09-13T05:41:39.485Z" },
  ]);
  assert.deepEqual(rows.map((r) => r.version.id), ["c", "b", "a"]);
  assert.deepEqual(rows.map((r) => r.delta), [44, 80, null]);
  assert.deepEqual(versionDeltas([{ id: "x", createdAt: "2026-01-01" }, { id: "y", size: 5, createdAt: "2025-01-01" }]).map((r) => r.delta), [null, null]);
  assert.deepEqual(versionDeltas(null), []);
});

test("formatDelta signs the size change", () => {
  assert.equal(formatDelta(120), "+120 B");
  assert.equal(formatDelta(-2048), "-2.0 KB");
  assert.equal(formatDelta(0), "No size change");
  assert.equal(formatDelta(null), "");
  assert.equal(formatDelta(NaN), "");
  assert.equal(byteLength("héllo"), 6);
  assert.equal(byteLength(null), 0);
});

test("accessLabel and accessChoices", () => {
  assert.equal(accessLabel("view"), "Can view");
  assert.equal(accessLabel("comment"), "Can comment");
  assert.equal(accessLabel("edit"), "Can edit");
  assert.equal(accessLabel("manage"), "Can manage");
  assert.equal(accessLabel("owner"), "No access");
  assert.deepEqual(accessChoices("share"), ["view", "comment", "edit", "manage"]);
  assert.deepEqual(accessChoices("link"), ["view", "comment"]);
});

test("linkSummary describes access, password and expiry", () => {
  const now = Date.parse("2026-09-13T12:00:00Z");
  const open = linkSummary({ access: "view", views: 1 }, now);
  assert.deepEqual({ ...open }, { access: "Can view", password: false, expired: false, expiry: "No expiry", views: "1 view" });
  const later = linkSummary({ access: "comment", hasPassword: true, expiresAt: "2026-09-20T12:00:00Z", views: 3 }, now);
  assert.equal(later.access, "Can comment");
  assert.equal(later.password, true);
  assert.equal(later.expired, false);
  assert.match(later.expiry, /^Expires /);
  assert.equal(later.views, "3 views");
  const gone = linkSummary({ access: "edit", expiresAt: "2026-09-01T00:00:00Z" }, now);
  assert.equal(gone.access, "Can view", "a link never grants more than comment");
  assert.equal(gone.expired, true);
  assert.match(gone.expiry, /^Expired /);
});

test("outlineOf lists headings outside code with their offsets", () => {
  const md = "# Title\n\nText\n\n```\n# not a heading\n```\n## Scope *now*\n###    Risks ##\n#nope\n";
  const items = outlineOf(md);
  assert.deepEqual(items.map((i) => [i.level, i.text]), [[1, "Title"], [2, "Scope now"], [3, "Risks"]]);
  assert.equal(md.slice(items[1].pos, items[1].pos + 2), "##");
  assert.equal(md.slice(items[2].pos, items[2].pos + 3), "###");
  assert.deepEqual(outlineOf(""), []);
});

test("wordChange widens the changed middle to whole words", () => {
  const a = "We ship the offline client in October.";
  const b = "We ship the offline client in November, after the public beta.";
  const c = wordChange(a, b);
  assert.equal(a.slice(c.start, c.endA), "October");
  assert.equal(b.slice(c.start, c.endB), "November, after the public beta");
  const d = wordChange("the quick brown fox", "the quack brown fox");
  assert.equal("the quick brown fox".slice(d.start, d.endA), "quick");
  assert.equal("the quack brown fox".slice(d.start, d.endB), "quack");
  assert.equal(wordChange("same", "same"), null);
  assert.equal(wordChange("abc", "xyz"), null);
});
