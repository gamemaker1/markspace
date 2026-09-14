import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

import {
  CONTEXT,
  makeAnchor,
  resolveAnchor,
  alignRendered,
  renderedToSource,
  coveredUnits,
} from "../../internal/web/static/js/anchors.js";
import { decodeEntityAt } from "../../internal/web/static/js/markdown.js";

const cases = JSON.parse(readFileSync(new URL("../vectors/anchors.json", import.meta.url), "utf8"));

test("context length matches the reference", () => {
  assert.equal(CONTEXT, cases.context);
});

test("makeAnchor reproduces every reference case", () => {
  assert.ok(cases.make.length >= 10);
  for (const c of cases.make) {
    const got = makeAnchor(c.body, c.start, c.end);
    if (c.want === null) {
      assert.equal(got, null, c.name);
      continue;
    }
    assert.ok(got, c.name);
    assert.deepEqual(Object.keys(got).sort(), Object.keys(c.want).sort(), c.name);
    for (const key of Object.keys(c.want)) assert.equal(got[key], c.want[key], `${c.name}: ${key}`);
  }
});

test("resolveAnchor reproduces every reference case", () => {
  assert.ok(cases.resolve.length >= 14);
  for (const c of cases.resolve) {
    const got = resolveAnchor(c.body, c.anchor);
    assert.deepEqual(Object.keys(got).sort(), ["end", "start", "state"], c.name);
    for (const key of ["start", "end", "state"]) assert.equal(got[key], c.want[key], `${c.name}: ${key}`);
  }
});

test("makeAnchor and resolveAnchor round trip, and odd inputs do not throw", () => {
  const body = "a 😀 b sync, then sync again 你好";
  for (let s = 0; s <= body.length; s++) {
    for (let e = s + 1; e <= body.length; e++) {
      const anchor = makeAnchor(body, s, e);
      const got = resolveAnchor(body, anchor);
      assert.equal(body.slice(got.start, got.end), anchor.exact);
      assert.equal(got.state, "exact", `${s}:${e}`);
    }
  }
  assert.equal(makeAnchor(body, NaN, NaN), null);
  assert.equal(makeAnchor(null, 0, 4), null);
  assert.deepEqual(resolveAnchor(body, null), { start: -1, end: -1, state: "orphaned" });
  assert.deepEqual(resolveAnchor(body, { exact: "sync" }), { start: 7, end: 11, state: "moved" });
});

/**
 * Checks that every mapped rendered unit equals its source unit, or comes
 * from an entity there, and that mapped offsets never go backwards.
 */
function assertConsistent(source, rendered, map) {
  assert.equal(map.length, rendered.length);
  let last = -1;
  for (let j = 0; j < map.length; j++) {
    const k = map[j];
    if (k < 0) continue;
    if (source[k] === "&" && decodeEntityAt(source, k)) {
      assert.ok(decodeEntityAt(source, k).text.includes(rendered[j]), `unit ${j}`);
      assert.ok(k >= last, `unit ${j} goes backwards`);
    } else {
      assert.equal(source[k], rendered[j], `unit ${j} of ${JSON.stringify(rendered)}`);
      assert.ok(k > last, `unit ${j} goes backwards`);
    }
    last = k;
  }
}

/**
 * Returns the source text that the nth occurrence of needle in the rendered
 * text maps to, with the start snapped forward and the end snapped backward.
 */
function sourceOf(source, rendered, needle, nth = 0) {
  let i = -1;
  for (let k = 0; k <= nth; k++) i = rendered.indexOf(needle, i + 1);
  assert.ok(i >= 0, `${needle} in ${rendered}`);
  const map = alignRendered(source, rendered);
  assertConsistent(source, rendered, map);
  const s = renderedToSource(source, map, i, true);
  const e = renderedToSource(source, map, i + needle.length, false);
  return source.slice(s, e);
}

/** Counts the rendered units without a source unit. */
function unmapped(source, rendered) {
  return alignRendered(source, rendered).filter((k) => k < 0).length;
}

test("alignment skips bold and italic markup", () => {
  const src = "Ship the **sync** engine, an _odd_ and *even* __word__.";
  const out = "Ship the sync engine, an odd and even word.";
  assert.equal(unmapped(src, out), 0);
  assert.equal(sourceOf(src, out, "sync"), "sync");
  assert.equal(sourceOf(src, out, "sync engine"), "sync** engine");
  assert.equal(sourceOf(src, out, "odd"), "odd");
  assert.equal(sourceOf(src, out, "even"), "even");
  assert.equal(sourceOf(src, out, "word"), "word");
});

test("alignment skips code backticks", () => {
  const src = "run `a < b` now and ``x ` y`` too";
  const out = "run a < b now and x ` y too";
  assert.equal(unmapped(src, out), 0);
  assert.equal(sourceOf(src, out, "a < b"), "a < b");
  assert.equal(sourceOf(src, out, "x ` y"), "x ` y");
});

test("alignment skips link URLs, even when the URL repeats the text", () => {
  const src = "read [the docs](https://example.com/sync \"sync docs\") and sync";
  const out = "read the docs and sync";
  assert.equal(unmapped(src, out), 0);
  const map = alignRendered(src, out);
  assert.equal(map[out.lastIndexOf("sync")], src.lastIndexOf("sync"));
  assert.equal(sourceOf(src, out, "docs"), "docs");
  assert.equal(sourceOf(src, out, "docs and"), "docs](https://example.com/sync \"sync docs\") and");
  assert.equal(sourceOf("[x](https://a.io/x) x", "x x", "x", 1), "x");
  assert.equal(alignRendered("[x](https://a.io/x) x", "x x")[2], 20);
});

test("alignment maps a wikilink label to the label, not the target", () => {
  const src = "See [[notes/sync guide|sync guide]] now, and [[Plain]].";
  const out = "See sync guide now, and Plain.";
  assert.equal(unmapped(src, out), 0);
  const map = alignRendered(src, out);
  const labelAt = src.indexOf("|") + 1;
  assert.equal(map[out.indexOf("sync")], labelAt);
  for (const k of map) assert.ok(k < src.indexOf("[[") + 2 || k >= labelAt, `unit mapped into the target at ${k}`);
  assert.equal(sourceOf(src, out, "sync guide"), "sync guide");
  assert.equal(sourceOf(src, out, "Plain"), "Plain");
  assert.equal(sourceOf("[[target|tar]]", "tar", "tar"), "tar");
  assert.equal(alignRendered("[[target|tar]]", "tar")[0], 9);
});

test("alignment skips backslash escapes and image markup", () => {
  const src = "Escaped \\*stars\\* and \\[brackets\\] ![alt text](/x.png) after";
  const out = "Escaped *stars* and [brackets]  after";
  assert.equal(unmapped(src, out), 0);
  assert.equal(sourceOf(src, out, "*stars*"), "*stars\\*");
  assert.equal(sourceOf(src, out, "stars"), "stars");
  assert.equal(sourceOf(src, out, "brackets"), "brackets");
  assert.equal(sourceOf(src, out, "after"), "after");
});

test("alignment decodes entities", () => {
  const src = "a &amp; b &lt;i&gt; &copy; &#x1F600; end";
  const out = "a & b <i> © 😀 end";
  assert.equal(unmapped(src, out), 0);
  assert.equal(sourceOf(src, out, "&"), "&amp;");
  assert.equal(sourceOf(src, out, "<i>"), "&lt;i&gt;");
  assert.equal(sourceOf(src, out, "©"), "&copy;");
  assert.equal(sourceOf(src, out, "😀"), "&#x1F600;");
  assert.equal(sourceOf(src, out, "end"), "end");
  assert.equal(sourceOf("AT&T and &bogus;", "AT&T and &bogus;", "&T"), "&T");
});

test("alignment handles emoji and CJK as UTF-16 units", () => {
  const src = "x😀 **你好** café 🚀 world";
  const out = "x😀 你好 café 🚀 world";
  assert.equal(unmapped(src, out), 0);
  const map = alignRendered(src, out);
  assert.equal(map[1], 1);
  assert.equal(map[2], 2);
  assert.equal(sourceOf(src, out, "你好"), "你好");
  assert.equal(sourceOf(src, out, "🚀"), "🚀");
  assert.equal(sourceOf(src, out, "café 🚀"), "café 🚀");
});

test("alignment of table cells and task list items", () => {
  assert.equal(sourceOf("b \\| c", "b | c", "| c"), "| c");
  assert.equal(unmapped("b \\| c", "b | c"), 0);
  assert.equal(sourceOf("*alpha* `x|y`", "alpha x|y", "alpha x"), "alpha* `x");
  const item = " write the **tests** &amp; docs";
  assert.equal(unmapped(item, " write the tests & docs"), 0);
  assert.equal(sourceOf(item, " write the tests & docs", "tests & docs"), "tests** &amp; docs");
  const nested = "two\n  - nested item";
  assert.equal(sourceOf(nested, "two\nnested item\n\n", "two\nnested"), "two\n  - nested");
});

test("unmatched rendered units map to -1 and matching resumes", () => {
  assert.deepEqual([...alignRendered("abc", "xyz")], [-1, -1, -1]);
  const src = "hello world";
  const map = alignRendered(src, "hello ☃ world");
  assert.deepEqual([...map.slice(0, 6)], [0, 1, 2, 3, 4, 5]);
  assert.equal(map[6], -1, "the snowman has no source");
  assert.equal(map[7], -1, "the second space has no source left after the first");
  assert.deepEqual([...map.slice(8)], [6, 7, 8, 9, 10], "matching resumes after the gap");
  const far = "a" + "-".repeat(300) + "b";
  assert.deepEqual([...alignRendered(far, "ab")], [0, -1], "lookahead is bounded");
  assert.deepEqual([...alignRendered("", "ab")], [-1, -1]);
  assert.equal(alignRendered("abc", "").length, 0);
});

test("renderedToSource snaps out of markup", () => {
  const src = "a **b** c";
  const out = "a b c";
  const map = alignRendered(src, out);
  assert.equal(renderedToSource(src, map, 2, true), 4);
  assert.equal(renderedToSource(src, map, 3, false), 5);
  const gap = Int32Array.from([0, -1, -1, 5]);
  assert.equal(renderedToSource("a....b", gap, 1, true), 5, "a start snaps forward");
  assert.equal(renderedToSource("a....b", gap, 3, false), 1, "an end snaps backward");
  assert.equal(renderedToSource("ab", Int32Array.from([0, 1]), 2, true), 2, "a start at the end uses the last unit");
  assert.equal(renderedToSource("ab", Int32Array.from([-1, -1]), 1, true), null);
  assert.equal(renderedToSource("&amp;", Int32Array.from([0]), 1, false), 5, "an end after an entity includes it");
});

test("coveredUnits covers mapped units in range and gaps inside them", () => {
  const map = Int32Array.from([0, 1, -1, 5, 6, -1, 9]);
  assert.deepEqual([...coveredUnits(map, 1, 7)], [0, 1, 1, 1, 1, 0, 0]);
  assert.deepEqual([...coveredUnits(map, 0, 10)], [1, 1, 1, 1, 1, 1, 1]);
  assert.deepEqual([...coveredUnits(map, 7, 9)], [0, 0, 0, 0, 0, 0, 0]);
  assert.deepEqual([...coveredUnits(Int32Array.from([-1, 2, -1]), 0, 5)], [0, 1, 0]);
});

test("alignment stays fast on a large block", () => {
  const src = "word **bold** [link](https://x.io/y) ".repeat(2000);
  const out = "word bold link ".repeat(2000);
  const t = Date.now();
  const map = alignRendered(src, out);
  assert.ok(Date.now() - t < 2000);
  assert.equal(map.filter((k) => k < 0).length, 0);
  assert.equal(alignRendered("x".repeat(20000), "y".repeat(2000)).filter((k) => k < 0).length, 2000);
});
