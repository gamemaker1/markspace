// Tests for the phone and scroll helpers of editor.js: the widget height
// cache, widget equality, block decoration gating, keyboard bar change
// detection, tooltip space and the title heading match.
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
  hashString, estimateTableHeight, WidgetHeights, ImageWidget, TableWidget, WikiPillWidget,
  blockUpdateKind, kbarStyle, kbarStyleChanges, tooltipSpaceRect, leadingTitleRange, findImageRange, editorScrollMargins,
} = await import("../../../internal/web/static/js/editor.js");
const { EditorState, markdownSupport } = await import("../../../internal/web/static/vendor/codemirror.js");

const IMG = "/files/ada/06abc/chart-1.png";

test("hashString is stable and separates sources", () => {
  assert.equal(hashString("| a |\n|---|\n| 1 |"), hashString("| a |\n|---|\n| 1 |"));
  assert.notEqual(hashString("| a |"), hashString("| b |"));
  assert.match(hashString(""), /^[0-9a-z]+$/);
});

test("estimateTableHeight counts rows and skips the delimiter row", () => {
  const src = "| Station | Depth |\n|---|---:|\n| NQ-1 | 4.0 |\n| NQ-2 | 4.3 |";
  assert.equal(estimateTableHeight(src), 3 * 34);
  assert.equal(estimateTableHeight("a | b\n:-- | --:\n1 | 2\n"), 2 * 34);
});

test("WidgetHeights records heights and ignores empty or repeated values", () => {
  const c = new WidgetHeights();
  assert.equal(c.get(IMG), -1);
  assert.equal(c.record(IMG, 0), false);
  assert.equal(c.get(IMG), -1);
  assert.equal(c.record(IMG, 211.6), true);
  assert.equal(c.get(IMG), 212);
  assert.equal(c.record(IMG, 212.2), false);
  assert.equal(c.record(IMG, 300), true);
  assert.equal(c.get(IMG), 300);
});

test("WidgetHeights image estimates: recorded height, then file size scaled to the column, then unknown", () => {
  const c = new WidgetHeights();
  c.setDims("id:06abc", 1600, 900);
  assert.deepEqual(c.dimsFor(IMG, "06abc"), { width: 1600, height: 900 });
  assert.equal(c.dimsFor(IMG), null, "the file id comes from the resolver, not from the URL");
  assert.equal(c.dimsFor("https://example.com/x.png"), null);
  // Without a column width there is nothing to scale to.
  assert.equal(c.imageEstimate(IMG, "06abc"), -1);
  c.setWidth(376);
  assert.equal(c.imageEstimate(IMG, "06abc"), 212 + 20, "the image height plus the block padding");
  // A tall image is capped at the height editor.css allows.
  c.setDims("id:06tall", 1000, 5000);
  assert.equal(c.imageEstimate("/files/ada/06tall/long.png", "06tall"), 640 + 20);
  // A small image keeps its own height.
  c.setDims("/files/ada/06small/icon.png", 64, 48);
  assert.equal(c.imageEstimate("/files/ada/06small/icon.png"), 48 + 20);
  c.record(IMG, 230);
  assert.equal(c.imageEstimate(IMG, "06abc"), 230);
  assert.equal(c.imageEstimate("/files/ada/other/none.png"), -1);
  // A URL match wins over the id match.
  c.setDims("/files/ada/06abc/chart-1.png", 800, 800);
  assert.deepEqual(c.dimsFor(IMG, "06abc"), { width: 800, height: 800 });
});

test("WidgetHeights table estimates fall back to the row count", () => {
  const c = new WidgetHeights();
  const src = "| a |\n|---|\n| 1 |";
  const key = "table:" + hashString(src);
  assert.equal(c.tableEstimate(key, src), 2 * 34 + 20);
  c.record(key, 97);
  assert.equal(c.tableEstimate(key, src), 97);
});

test("WidgetHeights.onResize records border-box heights, honours ready and tracks the column width", () => {
  const c = new WidgetHeights();
  const img = {}, table = {}, content = {};
  let loaded = false;
  c.targets.set(img, { key: IMG, ready: () => loaded });
  c.targets.set(table, { key: "table:x", ready: null });
  c.widthTarget = content;
  c.onResize([
    { target: img, borderBoxSize: [{ blockSize: 22 }], contentRect: { height: 22 } },
    { target: table, borderBoxSize: [{ blockSize: 180.4 }], contentRect: { height: 170 } },
    { target: content, contentBoxSize: [{ inlineSize: 376.2 }], contentRect: { width: 376 } },
  ]);
  assert.equal(c.get(IMG), -1, "an image that has not loaded is not recorded");
  assert.equal(c.get("table:x"), 180);
  assert.equal(c.width, 376);
  loaded = true;
  c.onResize([{ target: img, contentRect: { height: 212 } }]);
  assert.equal(c.get(IMG), 212, "contentRect is the fallback without borderBoxSize");
  c.onResize([{ target: {}, contentRect: { height: 99 } }]);
  assert.equal(c.heights.size, 2, "unknown targets are ignored");
});

test("widget eq ignores document positions", () => {
  const c = new WidgetHeights();
  const a = new ImageWidget(IMG, "Depth chart 1", c);
  const b = new ImageWidget(IMG, "Depth chart 1", c);
  assert.equal(a.eq(b), true);
  assert.equal(a.eq(new ImageWidget(IMG, "Other alt", c)), false);
  assert.equal(a.eq(new ImageWidget("/files/ada/06def/chart-2.png", "Depth chart 1", c)), false);
  assert.equal(a.eq(new ImageWidget(IMG, "Depth chart 1", null)), true, "the cache object is not compared");

  const src = "| a |\n|---|\n| 1 |";
  assert.equal(new TableWidget(src, c).eq(new TableWidget(src, null)), true);
  assert.equal(new TableWidget(src, c).eq(new TableWidget(src + "\n| 2 |", c)), false);

  const pill = new WikiPillWidget("Methods", "Methods", true);
  assert.equal(pill.eq(new WikiPillWidget("Methods", "Methods", true)), true);
  assert.equal(pill.eq(new WikiPillWidget("Methods", "How", true)), false);
  assert.equal(pill.eq(new WikiPillWidget("Methods", "Methods", false)), false);
  assert.equal(pill.eq(new WikiPillWidget("Other", "Methods", true)), false);
});

test("an image widget built before its file size is known is replaced once the size arrives", () => {
  const c = new WidgetHeights();
  const before = new ImageWidget(IMG, "x", c);
  c.setDims(IMG, 1600, 900);
  const after = new ImageWidget(IMG, "x", c);
  assert.equal(before.eq(after), false);
  assert.equal(after.eq(new ImageWidget(IMG, "x", c)), true);
  // A recorded height does not change equality, so drawing an image never replaces it.
  c.record(IMG, 212);
  assert.equal(after.eq(new ImageWidget(IMG, "x", c)), true);
});

test("widget estimatedHeight reads the cache", () => {
  const c = new WidgetHeights();
  const img = new ImageWidget(IMG, "x", c);
  assert.equal(img.estimatedHeight, -1);
  c.record(IMG, 250);
  assert.equal(img.estimatedHeight, 250);
  assert.equal(new ImageWidget(IMG, "x", null).estimatedHeight, -1);
  const src = "| a |\n|---|\n| 1 |\n| 2 |";
  const table = new TableWidget(src, c);
  assert.equal(table.estimatedHeight, 3 * 34 + 20);
  c.record(table.key, 140);
  assert.equal(table.estimatedHeight, 140);
});

test("blockUpdateKind rebuilds on document work and skips plain cursor moves", () => {
  assert.equal(blockUpdateKind({}), "none");
  assert.equal(blockUpdateKind({ docChanged: true }), "full");
  assert.equal(blockUpdateKind({ treeChanged: true }), "full");
  assert.equal(blockUpdateKind({ forced: true, cursorMoved: true }), "full");
  assert.equal(blockUpdateKind({ cursorMoved: true }), "none");
  assert.equal(blockUpdateKind({ cursorMoved: true, withTables: false }), "none");
  assert.equal(blockUpdateKind({ cursorMoved: true, withTables: true }), "tables");
  assert.equal(blockUpdateKind({ withTables: true }), "none");
  assert.equal(blockUpdateKind({ docChanged: true, cursorMoved: true, withTables: true }), "full");
});

test("kbarStyle lifts the bar above the keyboard and reports the covered space", () => {
  assert.deepEqual(kbarStyle({ hidden: true, offset: 0, height: 0 }), { transform: "", space: "0px" });
  assert.deepEqual(kbarStyle({ hidden: false, offset: 0, height: 51 }), { transform: "", space: "51px" });
  assert.deepEqual(kbarStyle({ hidden: false, offset: 336.4, height: 51 }), { transform: "translateY(-336px)", space: "387px" });
  assert.deepEqual(kbarStyle({ hidden: true, offset: 336, height: 51 }), { transform: "translateY(-336px)", space: "0px" });
  assert.deepEqual(kbarStyle({ hidden: false, offset: -4, height: 51 }), { transform: "", space: "51px" });
});

test("kbarStyleChanges returns only what differs", () => {
  const a = kbarStyle({ hidden: false, offset: 0, height: 51 });
  assert.deepEqual(kbarStyleChanges(null, a), { transform: "", space: "51px" });
  assert.equal(kbarStyleChanges(a, kbarStyle({ hidden: false, offset: 0, height: 51 })), null);
  assert.deepEqual(kbarStyleChanges(a, kbarStyle({ hidden: false, offset: 336, height: 51 })), { transform: "translateY(-336px)", space: "387px" });
  assert.deepEqual(kbarStyleChanges(a, kbarStyle({ hidden: true, offset: 0, height: 51 })), { space: "0px" });
});

test("tooltipSpaceRect is the visual viewport less the keyboard bar", () => {
  // Pixel 7 with the emulated 336 px keyboard and the 51 px bar from the diagnosis.
  assert.deepEqual(tooltipSpaceRect({ width: 412, height: 915, vvTop: 0, vvHeight: 579 }, 51), { top: 0, left: 0, right: 412, bottom: 528 });
  assert.deepEqual(tooltipSpaceRect({ width: 412, height: 915 }, 0), { top: 0, left: 0, right: 412, bottom: 915 });
  assert.deepEqual(tooltipSpaceRect({ width: 915, height: 412, vvTop: 0, vvHeight: 412 }, 51), { top: 0, left: 0, right: 915, bottom: 361 });
  // A scrolled visual viewport moves the top, and the bottom never passes above it.
  assert.deepEqual(tooltipSpaceRect({ width: 412, height: 915, vvTop: 120, vvHeight: 579 }, 51), { top: 120, left: 0, right: 412, bottom: 648 });
  assert.deepEqual(tooltipSpaceRect({ width: 412, height: 915, vvTop: 0, vvHeight: 30 }, 51), { top: 0, left: 0, right: 412, bottom: 0 });
});

test("editorScrollMargins leaves the keyboard to the visual viewport", () => {
  // Landscape 844 x 390 with a 200 px keyboard and the 49 px keyboard bar. CodeMirror's window ends at the visual
  // viewport bottom (190), so a bottom margin of 49 keeps the caret above the bar at 141. The old margin of 249 put the
  // visible bottom at -59 and scrolled the caret 86 px above the screen after Enter.
  assert.deepEqual(editorScrollMargins({ barHeight: 0, scrollerTop: -900, kbarHeight: 49 }), { top: 0, bottom: 49 });
  // The top margin covers only the part of the sticky bar that overlaps the note.
  assert.deepEqual(editorScrollMargins({ barHeight: 48, scrollerTop: 112, kbarHeight: 0 }), { top: 0, bottom: 0 });
  assert.deepEqual(editorScrollMargins({ barHeight: 48, scrollerTop: 20, kbarHeight: 0 }), { top: 28, bottom: 0 });
  assert.deepEqual(editorScrollMargins({ barHeight: 48, scrollerTop: -400, kbarHeight: 153 }), { top: 48, bottom: 153 });
});

test("leadingTitleRange matches a first-line H1 equal to the title", () => {
  assert.deepEqual(leadingTitleRange("# Harbour survey\n\nBody", "Harbour survey"), { from: 0, to: 17, lineEnd: 16 });
  assert.deepEqual(leadingTitleRange("# harbour Survey  ##\nBody", " Harbour survey "), { from: 0, to: 20, lineEnd: 20 });
  assert.deepEqual(leadingTitleRange("# T", "T"), { from: 0, to: 3, lineEnd: 3 });
  assert.deepEqual(leadingTitleRange("# T\n", "T"), { from: 0, to: 4, lineEnd: 3 });
  assert.deepEqual(leadingTitleRange("# T\n  \n\nx", "T"), { from: 0, to: 7, lineEnd: 3 });
  assert.equal(leadingTitleRange("# Harbour survey 2\n", "Harbour survey"), null);
  assert.equal(leadingTitleRange("## Harbour survey\n", "Harbour survey"), null);
  assert.equal(leadingTitleRange("Intro\n# Harbour survey", "Harbour survey"), null);
  assert.equal(leadingTitleRange("#Harbour survey", "Harbour survey"), null);
  assert.equal(leadingTitleRange("# Untitled", ""), null);
});

test("findImageRange finds the image whose widget sits on a line", () => {
  const doc = "Intro\n\n![Depth chart 1](/files/a/1/c1.png) and ![Other](/files/a/2/c2.png)\n\n![Depth chart 1](/files/a/1/c1.png)\n";
  const state = EditorState.create({ doc, extensions: [markdownSupport()] });
  const line3 = state.doc.line(3);
  const first = findImageRange(state, line3.to, "/files/a/1/c1.png", "Depth chart 1");
  assert.deepEqual(first, { from: line3.from, to: line3.from + "![Depth chart 1](/files/a/1/c1.png)".length });
  const second = findImageRange(state, line3.to, "/files/a/2/c2.png", "Other");
  assert.equal(state.doc.sliceString(second.from, second.to), "![Other](/files/a/2/c2.png)");
  // Typing above moves the image. The lookup works from the new position.
  const moved = state.update({ changes: { from: 0, insert: "More text\n" } }).state;
  const line6 = moved.doc.line(6);
  const again = findImageRange(moved, line6.to, "/files/a/1/c1.png", "Depth chart 1");
  assert.deepEqual(again, { from: line6.from, to: line6.to });
  assert.equal(findImageRange(state, state.doc.line(1).to, "/files/a/1/c1.png", "Depth chart 1"), null);
});
