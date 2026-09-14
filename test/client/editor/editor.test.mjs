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
  lineBounds, caretLine, typewriterScrollTop, insideFence, parseListLine, continueList, indentLines,
  toggleInline, toggleLink, toggleCode, wrapPair, linkFromPaste, setBlock, cycleHeading, insertBlock,
  slashTrigger, wikiTrigger, completeWikilink, rankWikiMatches, filterSlashItems, SLASH_ITEMS,
  slashCompletionOptions,
  wordStats, diffRange, mapCaret, attachmentMarkdown, isUrl, sectionAt, linkAt, altTextChange,
  isRelativeDestination, isImageItem, blockWidgetFor, IMAGE_MAX_HEIGHT, WidgetHeights, insertionPoint,
} = await import("../../../internal/web/static/js/editor.js");

const r = (text, s, e = s) => ({ text, selectionStart: s, selectionEnd: e });

test("lineBounds and caretLine", () => {
  assert.deepEqual(lineBounds("a\nbc\nd", 3), { start: 2, end: 4 });
  assert.deepEqual(lineBounds("\nx", 0), { start: 0, end: 0 });
  assert.deepEqual(caretLine("a\nbc\nd", 3), { index: 1, start: 2, end: 4, column: 1 });
  assert.equal(caretLine("a\nbc\nd", 6).index, 2);
});

test("typewriterScrollTop centres and clamps", () => {
  assert.equal(typewriterScrollTop({ caretTop: 1000, lineHeight: 20, viewportHeight: 600, maxScroll: 5000 }), 710);
  assert.equal(typewriterScrollTop({ caretTop: 100, lineHeight: 20, viewportHeight: 600, maxScroll: 5000 }), 0);
  assert.equal(typewriterScrollTop({ caretTop: 9000, lineHeight: 20, viewportHeight: 600, maxScroll: 5000 }), 5000);
  assert.equal(typewriterScrollTop({ caretTop: 900, lineHeight: 20, viewportHeight: 600, maxScroll: -5 }), 0);
});

test("parseListLine", () => {
  assert.equal(parseListLine("- a").kind, "bullet");
  assert.equal(parseListLine("  * a").indent, "  ");
  assert.equal(parseListLine("12. a").number, 12);
  assert.equal(parseListLine("- [x] done").kind, "task");
  assert.equal(parseListLine("> > q").prefix, "> > ");
  assert.equal(parseListLine("plain"), null);
  assert.equal(parseListLine("-no space"), null);
  assert.equal(parseListLine("---"), null);
});

test("continueList continues bullets, tasks and quotes", () => {
  assert.deepEqual(continueList("- a", 3, 3), r("- a\n- ", 6));
  assert.deepEqual(continueList("* a", 3, 3), r("* a\n* ", 6));
  assert.deepEqual(continueList("- [x] done", 10, 10), r("- [x] done\n- [ ] ", 17));
  assert.deepEqual(continueList("> hi", 4, 4), r("> hi\n> ", 7));
  assert.deepEqual(continueList("1) x", 4, 4), r("1) x\n2) ", 8));
});

test("continueList splits the item at the caret", () => {
  assert.deepEqual(continueList("- ab", 3, 3), r("- a\n- b", 6));
  assert.deepEqual(continueList("- abcd", 3, 5), r("- a\n- d", 6));
});

test("continueList renumbers ordered lists", () => {
  assert.deepEqual(continueList("1. a\n2. b\n3. c", 4, 4), r("1. a\n2. \n3. b\n4. c", 8));
  assert.deepEqual(continueList("1. a\n   - child\n2. b", 4, 4), r("1. a\n2. \n   - child\n3. b", 8));
  assert.deepEqual(continueList("1. a\n1. b", 4, 4), r("1. a\n1. \n1. b", 8), "lazy numbering stays lazy");
  assert.deepEqual(continueList("9. a\n\n1. other", 4, 4), r("9. a\n10. \n\n1. other", 9));
});

test("continueList ends or outdents empty items", () => {
  assert.deepEqual(continueList("- a\n- ", 6, 6), r("- a\n", 4));
  assert.deepEqual(continueList("> hi\n> ", 7, 7), r("> hi\n", 5));
  assert.deepEqual(continueList("- a\n  - ", 8, 8), r("- a\n- ", 6));
  assert.deepEqual(continueList("- a\n  - b\n    - ", 16, 16), r("- a\n  - b\n  - ", 14));
});

test("continueList leaves other lines to the browser", () => {
  assert.equal(continueList("plain", 5, 5), null);
  assert.equal(continueList("- a", 1, 1), null, "caret inside the marker");
  assert.equal(continueList("```\n- a", 7, 7), null, "inside a code fence");
  assert.equal(continueList("- a\nb", 1, 5), null, "multi-line selection");
});

test("insideFence", () => {
  assert.equal(insideFence("```\ncode\n", 9), true);
  assert.equal(insideFence("```\ncode\n```\n", 13), false);
});

test("indentLines indents and outdents", () => {
  assert.deepEqual(indentLines("- a", 3, 3), r("  - a", 5));
  assert.deepEqual(indentLines("  - a", 5, 5, true), r("- a", 3));
  assert.deepEqual(indentLines("1. a", 4, 4), r("   1. a", 7));
  assert.deepEqual(indentLines("a\nb", 0, 3), r("  a\n  b", 2, 7));
  assert.deepEqual(indentLines("  a\n  b", 2, 7, true), r("a\nb", 0, 3));
  assert.deepEqual(indentLines("a\n\nb", 0, 4), r("  a\n\n  b", 2, 8), "blank lines stay blank");
  assert.deepEqual(indentLines("\tx\ny", 0, 4, true), r("x\ny", 0, 3));
});

test("indentLines returns null for a caret on a plain line", () => {
  assert.equal(indentLines("plain", 2, 2), null);
  assert.deepEqual(indentLines("- a", 3, 3, true), r("- a", 3), "outdent at the root is a no-op");
});

test("toggleInline wraps and unwraps", () => {
  assert.deepEqual(toggleInline("hello", 0, 5, "**"), r("**hello**", 2, 7));
  assert.deepEqual(toggleInline("**hello**", 2, 7, "**"), r("hello", 0, 5));
  assert.deepEqual(toggleInline("**hello**", 0, 9, "**"), r("hello", 0, 5));
  assert.deepEqual(toggleInline("hi there", 0, 3, "**"), r("**hi** there", 2, 4), "trims whitespace");
  assert.deepEqual(toggleInline("a", 0, 1, "`"), r("`a`", 1, 2));
});

test("toggleInline italic handles both markers without eating bold", () => {
  assert.deepEqual(toggleInline("*x*", 1, 2, "_"), r("x", 0, 1));
  assert.deepEqual(toggleInline("_x_", 1, 2, "_"), r("x", 0, 1));
  assert.deepEqual(toggleInline("**x**", 2, 3, "_"), r("**_x_**", 3, 4));
});

test("toggleInline with a caret", () => {
  assert.deepEqual(toggleInline("hello world", 2, 2, "_"), r("_hello_ world", 1, 6));
  assert.deepEqual(toggleInline("a ", 2, 2, "**"), r("a ****", 4));
  assert.deepEqual(toggleInline("a ****", 4, 4, "**"), r("a ", 2));
});

test("toggleLink", () => {
  const out = toggleLink("see docs", 4, 8);
  assert.equal(out.text, "see [docs](https://)");
  assert.equal(out.text.slice(out.selectionStart, out.selectionEnd), "https://");
  assert.deepEqual(toggleLink("[docs](http://x)", 2, 2), r("docs", 0, 4));
  assert.deepEqual(toggleLink("go http://x.y", 3, 13), r("go [](http://x.y)", 4));
  assert.deepEqual(toggleLink("", 0, 0), r("[link text](https://)", 1, 10));
  assert.ok(toggleLink("![img](a.png)", 2, 2).text.includes("](a.png)"), "images are not unwrapped");
});

test("toggleCode", () => {
  assert.deepEqual(toggleCode("run x", 4, 5), r("run `x`", 5, 6));
  const wrapped = toggleCode("a\nb", 0, 3);
  assert.equal(wrapped.text, "```\na\nb\n```");
  assert.equal(toggleCode(wrapped.text, 0, wrapped.text.length).text, "a\nb");
});

test("wrapPair", () => {
  assert.deepEqual(wrapPair("abc", 0, 3, "("), r("(abc)", 1, 4));
  assert.deepEqual(wrapPair("abc", 1, 2, "\""), r("a\"b\"c", 2, 3));
  assert.deepEqual(wrapPair("abc", 0, 3, "`"), r("`abc`", 1, 4));
  assert.equal(wrapPair("abc", 1, 1, "("), null);
  assert.equal(wrapPair("abc", 0, 3, "x"), null);
});

test("linkFromPaste", () => {
  assert.deepEqual(linkFromPaste("docs", 0, 4, "https://x.y/z"), r("[docs](https://x.y/z)", 21));
  assert.equal(linkFromPaste("docs", 2, 2, "https://x.y"), null);
  assert.equal(linkFromPaste("docs", 0, 4, "not a url"), null);
  assert.equal(linkFromPaste("http://a.b", 0, 10, "https://x.y"), null);
  assert.equal(isUrl("mailto:a@b.c"), true);
  assert.equal(isUrl("javascript:alert(1)"), false);
});

test("setBlock applies and toggles block types", () => {
  assert.deepEqual(setBlock("Title", 5, 5, "h1"), r("# Title", 7));
  assert.deepEqual(setBlock("# Title", 7, 7, "h1"), r("Title", 5));
  assert.deepEqual(setBlock("## Title", 8, 8, "h1"), r("# Title", 7));
  assert.deepEqual(setBlock("- a", 3, 3, "task"), r("- [ ] a", 7));
  assert.deepEqual(setBlock("- a\nb", 0, 5, "number"), r("1. a\n2. b", 0, 9));
  assert.deepEqual(setBlock("", 0, 0, "quote"), r("> ", 2));
  assert.deepEqual(setBlock("- a", 3, 3, "bullet", { toggle: false }), r("- a", 3));
});

test("cycleHeading cycles 1, 2, 3, off", () => {
  let t = r("x", 1);
  const seen = [];
  for (let i = 0; i < 4; i++) {
    t = cycleHeading(t.text, t.selectionStart, t.selectionEnd);
    seen.push(t.text);
  }
  assert.deepEqual(seen, ["# x", "## x", "### x", "x"]);
});

test("insertBlock separates blocks with blank lines", () => {
  assert.deepEqual(insertBlock("", 0, 0, "```\n\n```", 4), r("```\n\n```", 4));
  assert.deepEqual(insertBlock("para\n", 5, 5, "```\n\n```", 4), r("para\n\n```\n\n```", 10));
  assert.deepEqual(insertBlock("para", 4, 4, "---\n", 4), r("para\n\n---\n", 10));
  const mid = insertBlock("a\n\nb", 2, 2, "| x |");
  assert.equal(mid.text, "a\n\n| x |\n\nb");
});

test("slashTrigger", () => {
  assert.deepEqual(slashTrigger("/he", 3), { start: 0, query: "he" });
  assert.deepEqual(slashTrigger("hi /ta", 6), { start: 3, query: "ta" });
  assert.deepEqual(slashTrigger("x\n/", 3), { start: 2, query: "" });
  assert.equal(slashTrigger("and/or", 6), null);
  assert.equal(slashTrigger("/a b", 4), null);
  assert.equal(slashTrigger("```\n/x", 6), null);
});

test("wikiTrigger and completeWikilink", () => {
  assert.deepEqual(wikiTrigger("see [[Pro", 9), { start: 4, query: "Pro" });
  assert.equal(wikiTrigger("[[a]] b", 7), null);
  assert.deepEqual(completeWikilink("see [[Pro]]", 4, 9, "Project"), r("see [[Project]]", 15));
  assert.deepEqual(completeWikilink("[[Pro", 0, 5, "Project"), r("[[Project]]", 11));
});

test("rankWikiMatches ranks by title prefix and filters", () => {
  const notes = [
    { id: "1", title: "Old project" },
    { id: "2", title: "Project plan" },
    { id: "3", title: "Projection", deleted: true },
    { id: "4", title: "Proj" },
    { id: "5", title: "Body match only" },
    { id: "2", title: "Project plan" },
  ];
  assert.deepEqual(rankWikiMatches(notes, "proj", { excludeId: "4" }).map((n) => n.id), ["2", "1", "5"]);
  assert.deepEqual(rankWikiMatches(notes, "proj").map((n) => n.id).slice(0, 2), ["4", "2"]);
  assert.equal(rankWikiMatches(notes, "", { limit: 2 }).length, 2);
});

test("filterSlashItems", () => {
  assert.equal(filterSlashItems(SLASH_ITEMS, "").length, SLASH_ITEMS.length);
  const offline = filterSlashItems(SLASH_ITEMS, "", { online: false });
  assert.equal(offline.length, SLASH_ITEMS.length, "AI entries stay listed offline");
  assert.ok(offline.filter((i) => i.online).every((i) => i.disabled === true), "offline AI entries are disabled");
  assert.ok(offline.filter((i) => !i.online).every((i) => !i.disabled));
  assert.deepEqual(filterSlashItems(SLASH_ITEMS, "head").map((i) => i.id), ["h1", "h2", "h3"]);
  assert.equal(filterSlashItems(SLASH_ITEMS, "todo")[0].id, "task");
  assert.equal(filterSlashItems(SLASH_ITEMS, "h2")[0].id, "h2");
  assert.equal(filterSlashItems(SLASH_ITEMS, "TAB")[0].id, "table");
  assert.deepEqual(filterSlashItems(SLASH_ITEMS, "zzz"), []);
  assert.deepEqual(filterSlashItems(SLASH_ITEMS, "summ", { online: false }).map((i) => [i.id, i.disabled]), [["ai", true]]);
  assert.deepEqual(filterSlashItems(SLASH_ITEMS, "image").map((i) => i.id), ["image", "image-gen"]);
});

test("slashCompletionOptions keeps the ranked order while typing", () => {
  const typed = slashCompletionOptions(filterSlashItems(SLASH_ITEMS, "ask"), "ask");
  assert.equal(typed[0].item.id, "ai", "Ask AI must be the first option for /ask");
  assert.ok(typed.every((o) => o.section === undefined), "no sections while typing");
  const offline = slashCompletionOptions(filterSlashItems(SLASH_ITEMS, "ask", { online: false }), "ask");
  assert.equal(offline[0].detail, "Needs a connection");
  const browsing = slashCompletionOptions(filterSlashItems(SLASH_ITEMS, ""), "");
  assert.ok(browsing.filter((o) => o.item.online).every((o) => o.section === "AI"));
  assert.ok(browsing.filter((o) => !o.item.online).every((o) => o.section === undefined));
});

test("wordStats", () => {
  assert.deepEqual(wordStats(""), { words: 0, minutes: 0 });
  assert.deepEqual(wordStats("Hello world"), { words: 2, minutes: 1 });
  assert.equal(wordStats("# Title\n\n- [ ] do it's thing").words, 4);
  assert.equal(wordStats("[link](http://example.com/a-b)").words, 1);
  assert.equal(wordStats("你好世界").words, 4);
  assert.equal(wordStats("word ".repeat(690)).minutes, 3);
});

test("diffRange and mapCaret", () => {
  assert.deepEqual(diffRange("abc", "abXc"), { start: 2, end: 2, insert: "X" });
  assert.deepEqual(diffRange("abc", "abc"), { start: 3, end: 3, insert: "" });
  assert.deepEqual(diffRange("hello", "help"), { start: 3, end: 5, insert: "p" });
  const emoji = diffRange("a\u{1F600}", "a\u{1F601}");
  assert.equal(emoji.start, 1);
  assert.equal(emoji.insert, "\u{1F601}");
  const d = diffRange("one two", "one big two");
  assert.equal(mapCaret(7, d), 11);
  assert.equal(mapCaret(2, d), 2);
});

test("insertionPoint places a stored insertion where its prefix and suffix meet", () => {
  const text = "one two\n- The crane rail is sound\n\n## Estimate\n- The crane rail is sound\n\n## Estimate";
  const first = text.indexOf("sound") + 5;
  assert.deepEqual(insertionPoint(text, { exact: "", prefix: "rail is sound", suffix: "\n\n## Estimate", start: first }), { start: first, end: first });
  const second = text.lastIndexOf("sound") + 5;
  assert.deepEqual(insertionPoint(text, { exact: "", prefix: "rail is sound", suffix: "\n\n## Estimate", start: second + 3 }), { start: second, end: second }, "the occurrence nearest the stored start wins");
  assert.equal(insertionPoint(text, { exact: "", prefix: "missing", suffix: "text", start: 0 }), null);
  assert.equal(insertionPoint(text, { exact: "two", prefix: "one ", suffix: "" }), null, "anchors with exact text go to resolveAnchor");
  assert.equal(insertionPoint(text, { exact: "", prefix: "", suffix: "" }), null);
});

test("isRelativeDestination names items of the space only", () => {
  assert.equal(isRelativeDestination("east%20wall.jpg"), true);
  assert.equal(isRelativeDestination("../report.pdf"), true);
  assert.equal(isRelativeDestination("/files/docs/01/a.png"), false);
  assert.equal(isRelativeDestination("https://example.com/a.png"), false);
  assert.equal(isRelativeDestination("//cdn.example/a.png"), false);
  assert.equal(isRelativeDestination("#heading"), false);
  assert.equal(isRelativeDestination("data:image/png;base64,AA"), false);
  assert.equal(isRelativeDestination(""), false);
});

test("isImageItem reads the media type, then the name", () => {
  assert.equal(isImageItem({ kind: "file", mime: "image/png", name: "x" }), true);
  assert.equal(isImageItem({ kind: "file", mime: "", name: "wall.JPG" }), true);
  assert.equal(isImageItem({ kind: "file", mime: "application/pdf", name: "a.png" }), false);
  assert.equal(isImageItem({ kind: "note", name: "a.png" }), false);
  assert.equal(isImageItem(null), false);
});

test("blockWidgetFor resolves embeds and relative images, and shows uploads", () => {
  const items = {
    "east wall.jpg": { kind: "file", href: "/s/d/f/east%20wall.jpg", fileUrl: "/files/d/01img/east%20wall.jpg", mime: "image/jpeg", name: "east wall.jpg" },
    "report.pdf": { kind: "file", href: "/s/d/f/report.pdf", fileUrl: "/files/d/01pdf/report.pdf", mime: "application/pdf", name: "report.pdf" },
    "Methods": { kind: "note", href: "/s/d/n/methods", fileUrl: "", mime: "", name: "Methods" },
  };
  const seen = [];
  const links = { resolve: (target, form, embed) => { seen.push([target, form, embed]); return items[decodeURIComponent(target)] || null; } };
  const env = { links, uploads: new Map(), heights: new WidgetHeights() };
  const img = blockWidgetFor({ name: "Embed", url: "east wall.jpg", label: "east wall.jpg" }, env);
  assert.equal(img.constructor.name, "ImageWidget");
  assert.equal(img.url, "/files/d/01img/east%20wall.jpg");
  assert.equal(img.fileId, "01img");
  const md = blockWidgetFor({ name: "Image", url: "east%20wall.jpg", label: "Wall" }, env);
  assert.equal(md.constructor.name, "ImageWidget");
  assert.deepEqual(seen[1], ["east%20wall.jpg", "markdownImage", true]);
  const card = blockWidgetFor({ name: "Embed", url: "report.pdf", label: "report.pdf" }, env);
  assert.equal(card.constructor.name, "FileCardWidget");
  assert.equal(card.href, "/s/d/f/report.pdf");
  const noteCard = blockWidgetFor({ name: "Embed", url: "Methods", label: "Methods" }, env);
  assert.equal(noteCard.kind, "note");
  // A plain link to a file stays a link, and an unknown target draws nothing.
  assert.equal(blockWidgetFor({ name: "Link", url: "report.pdf", label: "report" }, env), null);
  assert.equal(blockWidgetFor({ name: "Embed", url: "missing.png", label: "" }, env), null);
  // Before the resolver loads, relative targets wait, and absolute image URLs draw at once.
  assert.equal(blockWidgetFor({ name: "Image", url: "east%20wall.jpg", label: "" }, { ...env, links: null }), null);
  assert.equal(blockWidgetFor({ name: "Image", url: "/files/d/01x/a.png", label: "" }, { ...env, links: null }).fileId, "01x");
  assert.equal(blockWidgetFor({ name: "Image", url: "https://example.com/a.png", label: "" }, env).fileId, "");
  // A file still uploading shows progress, for images and for linked files.
  const uploads = new Map([["01pdf", { fileId: "01pdf", state: "uploading", loaded: 5, size: 10 }], ["01img", { fileId: "01img", state: "done" }]]);
  const up = blockWidgetFor({ name: "Link", url: "report.pdf", label: "report" }, { ...env, uploads });
  assert.equal(up.constructor.name, "UploadWidget");
  assert.equal(up.percent, 50);
  assert.equal(blockWidgetFor({ name: "Embed", url: "east wall.jpg", label: "" }, { ...env, uploads }).constructor.name, "ImageWidget");
  assert.equal(IMAGE_MAX_HEIGHT, 640);
});

test("sectionAt", () => {
  const text = "intro\n# A\none\n## B\ntwo\n# C\nthree";
  assert.deepEqual(sectionAt(text, 2), { heading: "", level: 0, from: 0, to: 6, text: "intro\n" });
  const b = sectionAt(text, text.indexOf("two"));
  assert.equal(b.heading, "B");
  assert.equal(b.text, "## B\ntwo\n");
  const a = sectionAt(text, text.indexOf("one"));
  assert.equal(a.text, "# A\none\n## B\ntwo\n");
  assert.equal(sectionAt("```\n# not\n```\nx", 12).heading, "");
});

test("linkAt", () => {
  const text = "see [docs](http://a.b) and [[Plan|the plan]] ![alt](/i.png)";
  assert.deepEqual(linkAt(text, 6), { kind: "link", from: 4, to: 22, url: "http://a.b", label: "docs" });
  assert.equal(linkAt(text, 30).kind, "wikilink");
  assert.equal(linkAt(text, 30).url, "Plan");
  assert.equal(linkAt(text, text.indexOf("alt")).kind, "image");
  assert.equal(linkAt(text, 1), null);
});

test("altTextChange", () => {
  const text = "x ![old](/files/s/a/p.png)";
  const c = altTextChange(text, 2, "A red [bike]\nat dusk");
  assert.deepEqual(c, { from: 4, to: 7, insert: "A red bike at dusk" });
  assert.equal(altTextChange(text, 0, "x"), null);
});

test("attachmentMarkdown", () => {
  assert.equal(attachmentMarkdown("a b.png", "/files/x/a b.png", true), "![a b.png](/files/x/a%20b.png)");
  assert.equal(attachmentMarkdown("doc [1].pdf", "markspace-pending:k1", false), "[doc 1.pdf](markspace-pending:k1)");
});
