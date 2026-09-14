// Tests of static/js/find.js: the query builder, the Reading text matcher and
// marks, the match count, CodeMirror matching and replace all, and the find bar.
import { test } from "node:test";
import assert from "node:assert/strict";
import { register } from "node:module";
import { installDOM, FakeEvent } from "./fakedom.mjs";

register("data:text/javascript," + encodeURIComponent(`
export async function resolve(specifier, context, next) {
  if (specifier.startsWith("/static/")) {
    return { url: new URL("../../internal/web/static/" + specifier.slice(8), ${JSON.stringify(import.meta.url)}).href, shortCircuit: true };
  }
  return next(specifier, context);
}`));

const {
  buildMatcher, findInText, isWholeWord, escapeRegExp, countLabel, countShortLabel, mapIndex, collectText, markMatches, clearMarks,
  toSearchQuery, cmMatches, matchIndexAt, nextIndexFrom, replaceAllMatches, createFindBar, HIT_CLASS, MAX_MATCHES,
} = await import("../../internal/web/static/js/find.js");
const { EditorState, history, undo, search, setSearchQuery, replaceNext, EditorSelection } = await import("../../internal/web/static/vendor/codemirror.js");

const texts = (text, s) => findInText(text, buildMatcher(s)).map((m) => text.slice(m.from, m.to));

test("buildMatcher: literal text, case and an empty query", () => {
  assert.deepEqual(texts("Harbour harbour HARBOUR", { query: "harbour" }), ["Harbour", "harbour", "HARBOUR"]);
  assert.deepEqual(texts("Harbour harbour HARBOUR", { query: "harbour", caseSensitive: true }), ["harbour"]);
  // Without Regular expression, special characters are literal.
  assert.deepEqual(texts("a.b axb (a.b)", { query: "a.b" }), ["a.b", "a.b"]);
  assert.deepEqual(texts("cost $5 (net)", { query: "$5 (net)" }), ["$5 (net)"]);
  assert.equal(buildMatcher({ query: "" }).regex, null);
  assert.equal(buildMatcher({ query: "" }).error, "");
  assert.deepEqual(findInText("abc", buildMatcher({ query: "" })), []);
});

test("buildMatcher: whole word", () => {
  assert.deepEqual(texts("cat catalog cat-food concat cat_x cat", { query: "cat", wholeWord: true }), ["cat", "cat", "cat"]);
  assert.ok(isWholeWord("a cat.", 2, 5));
  assert.ok(!isWholeWord("scat", 1, 4));
  // Unicode letters are word characters.
  assert.deepEqual(texts("café caféine", { query: "café", wholeWord: true }), ["café"]);
  // An edge that is not a word character needs no boundary.
  assert.deepEqual(texts("x-y", { query: "-", wholeWord: true }), ["-"]);
});

test("buildMatcher: regular expressions", () => {
  assert.deepEqual(texts("cat cot cut", { query: "c[ao]t", regexp: true }), ["cat", "cot"]);
  assert.deepEqual(texts("Line one\nline two", { query: "^line", regexp: true }), ["Line", "line"]);
  assert.deepEqual(texts("Line one\nline two", { query: "^line", regexp: true, caseSensitive: true }), ["line"]);
  // Empty matches are skipped and do not loop.
  assert.deepEqual(findInText("abc", buildMatcher({ query: "x*", regexp: true })), []);
  assert.deepEqual(texts("a😀b", { query: ".", regexp: true }), ["a", "😀", "b"]);
});

test("buildMatcher: an invalid regular expression gives an inline error and no throw", () => {
  let m;
  assert.doesNotThrow(() => { m = buildMatcher({ query: "harb(", regexp: true }); });
  assert.equal(m.regex, null);
  assert.equal(m.error, "Invalid regular expression");
  assert.deepEqual(findInText("harb(", m), []);
  // The same text without Regular expression is valid.
  assert.equal(buildMatcher({ query: "harb(" }).error, "");
  assert.equal(escapeRegExp("a.b*(c)"), "a\\.b\\*\\(c\\)");
});

test("findInText stops at the limit", () => {
  const text = "a ".repeat(MAX_MATCHES + 50);
  assert.equal(findInText(text, buildMatcher({ query: "a" })).length, MAX_MATCHES + 1);
  assert.equal(findInText(text, buildMatcher({ query: "a" }), 3).length, 3);
});

test("countLabel and mapIndex", () => {
  assert.equal(countLabel({ index: 2, total: 12 }), "3 of 12");
  assert.equal(countLabel({ index: -1, total: 12 }), "12 matches");
  assert.equal(countLabel({ index: -1, total: 1 }), "1 match");
  assert.equal(countLabel({ total: 0 }), "No results");
  assert.equal(countLabel({ total: 0, query: "" }), "");
  assert.equal(countLabel({ index: 4, total: 1000, capped: true }), "5 of 1000+");
  assert.equal(countShortLabel({ index: 2, total: 12 }), "3/12");
  assert.equal(countShortLabel({ index: -1, total: 6 }), "6");
  assert.equal(countShortLabel({ total: 0 }), "0");
  assert.equal(countShortLabel({ total: 0, query: "" }), "");
  assert.equal(countShortLabel({ total: 1000, capped: true }), "1000+");
  assert.equal(mapIndex(3, 10, 10), 3);
  assert.equal(mapIndex(9, 10, 5), 4);
  assert.equal(mapIndex(0, 10, 5), 0);
  assert.equal(mapIndex(-1, 10, 5), 0);
  assert.equal(mapIndex(2, 10, 0), -1);
  assert.equal(mapIndex(12, 12, 12), 11);
});

/** Builds <div><h1 class=title>Harbour</h1><p>A <strong>bold</strong> word about the harbour.</p><p>Next harbour</p></div>. */
function readingTree(document) {
  const el = (tag, attrs, ...children) => {
    const node = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) node.setAttribute(k, v);
    for (const c of children) node.appendChild(typeof c === "string" ? document.createTextNode(c) : c);
    return node;
  };
  const root = el("div", {},
    el("h1", { class: "ms-preview-title" }, "Harbour"),
    el("p", {}, "A ", el("strong", {}, "bold"), " word about the harbour."),
    el("p", {}, "Next harbour", el("button", {}, "harbour")),
    el("pre", {}, el("code", {}, "harbour = 1")));
  document.body.appendChild(root);
  return root;
}

test("collectText joins inline text and breaks between blocks", () => {
  const { document } = installDOM();
  const root = readingTree(document);
  const { text, segments } = collectText(root, { skip: ".ms-preview-title" });
  assert.equal(text, "A bold word about the harbour.\nNext harbour\nharbour = 1");
  assert.equal(segments.length, 5);
  assert.equal(segments[1].node.data, "bold");
  assert.deepEqual([segments[1].start, segments[1].end], [2, 6]);
  // Without the skip selector the title is text too.
  assert.ok(collectText(root).text.startsWith("Harbour\nA bold"));
});

test("Reading matches across inline elements and not across blocks", () => {
  const { document } = installDOM();
  const root = readingTree(document);
  const collected = collectText(root, { skip: ".ms-preview-title" });
  const across = findInText(collected.text, buildMatcher({ query: "a bold w" }));
  assert.equal(across.length, 1);
  assert.equal(findInText(collected.text, buildMatcher({ query: "harbour. Next" })).length, 0);
  assert.equal(findInText(collected.text, buildMatcher({ query: "harbour.\\nNext", regexp: true })).length, 1);

  const marks = markMatches(across, collected);
  assert.equal(marks.length, 1);
  // "A " in the text node, "bold" in strong, " w" after it: three marks for one match.
  assert.deepEqual(marks[0].map((m) => m.textContent), ["A ", "bold", " w"]);
  assert.ok(marks[0].every((m) => m.className === HIT_CLASS && m.getAttribute("data-find-index") === "0"));
  assert.equal(marks[0][1].parentNode.tagName, "STRONG");
  const p = root.children[1];
  assert.equal(p.textContent, "A bold word about the harbour.");

  clearMarks(root);
  assert.equal(root.querySelectorAll("mark").length, 0);
  assert.equal(p.textContent, "A bold word about the harbour.");
  // The text nodes are joined again: "A ", strong, " word about the harbour.".
  assert.equal(p.childNodes.length, 3);
  assert.equal(p.childNodes[2].data, " word about the harbour.");
});

test("Reading marks every match, skips buttons and the title, and clears", () => {
  const { document } = installDOM();
  const root = readingTree(document);
  const before = root.textContent;
  const collected = collectText(root, { skip: ".ms-preview-title" });
  const matches = findInText(collected.text, buildMatcher({ query: "harbour" }));
  assert.equal(matches.length, 3);
  const marks = markMatches(matches, collected);
  assert.deepEqual(marks.map((parts) => parts.length), [1, 1, 1]);
  assert.equal(root.querySelectorAll("mark." + HIT_CLASS).length, 3);
  assert.equal(root.querySelector("code").textContent, "harbour = 1");
  assert.equal(root.querySelector("button").querySelectorAll("mark").length, 0);
  assert.equal(root.textContent, before);
  clearMarks(root);
  assert.equal(root.textContent, before);
  assert.equal(root.querySelectorAll("mark").length, 0);
  // Two matches in one text node.
  const again = collectText(root, { skip: ".ms-preview-title" });
  const two = findInText(again.text, buildMatcher({ query: "o" })).slice(0, 2);
  markMatches(two, again);
  assert.equal(root.children[1].querySelectorAll("mark").length, 2);
  clearMarks(root);
  assert.equal(root.textContent, before);
});

const DOC = "# Harbour\n\nThe **harbour** wall. harbours and Harbour.\n\nharbour";

test("CodeMirror matches, whole word and the current index", () => {
  const state = EditorState.create({ doc: DOC });
  assert.equal(cmMatches(state, toSearchQuery({ query: "harbour" })).length, 5);
  assert.equal(cmMatches(state, toSearchQuery({ query: "harbour", caseSensitive: true })).length, 3);
  assert.equal(cmMatches(state, toSearchQuery({ query: "harbour", wholeWord: true })).length, 4);
  assert.equal(cmMatches(state, toSearchQuery({ query: "harbours?", regexp: true, caseSensitive: true })).length, 3);
  // Hidden live preview syntax is searchable because the search runs on the source.
  assert.equal(cmMatches(state, toSearchQuery({ query: "**harbour**" })).length, 1);
  assert.equal(cmMatches(state, toSearchQuery({ query: "harb(", regexp: true })).length, 0);
  assert.equal(toSearchQuery({ query: "harb(", regexp: true }).valid, false);
  // Text queries are literal, so \n is two characters.
  assert.equal(cmMatches(EditorState.create({ doc: "a\\nb" }), toSearchQuery({ query: "\\n" })).length, 1);

  const list = cmMatches(state, toSearchQuery({ query: "harbour" }));
  assert.equal(matchIndexAt(list, list[2].from, list[2].to), 2);
  assert.equal(matchIndexAt(list, list[2].from, list[2].to + 1), -1);
  assert.equal(matchIndexAt(list, 0, 1), -1);
  assert.equal(nextIndexFrom(list, list[1].from + 1), 2);
  assert.equal(nextIndexFrom(list, DOC.length), 0);
  assert.equal(nextIndexFrom([], 0), -1);
});

/** A view with state and dispatch, enough for the CodeMirror search commands. */
function fakeView(doc) {
  const view = {
    state: EditorState.create({ doc, extensions: [history(), search({ literal: true })] }),
    dispatched: 0,
    // Commands pass specs. undo passes a Transaction.
    dispatch(...specs) {
      const tr = specs[0] && specs[0].startState ? specs[0] : view.state.update(...specs);
      view.state = tr.state;
      view.dispatched++;
    },
  };
  return view;
}

test("replace all is one transaction and one undo step", () => {
  const view = fakeView(DOC);
  const before = view.dispatched;
  assert.equal(replaceAllMatches(view, { query: "harbour", replace: "port" }), true);
  // One transaction sets the query, one replaces every match.
  assert.equal(view.dispatched - before, 2);
  assert.equal(view.state.doc.toString(), "# port\n\nThe **port** wall. ports and port.\n\nport");
  assert.equal(undo(view), true);
  assert.equal(view.state.doc.toString(), DOC);
  assert.equal(undo(view), false);
  // Nothing to replace changes nothing.
  assert.equal(replaceAllMatches(view, { query: "absent", replace: "x" }), false);
  assert.equal(view.state.doc.toString(), DOC);
});

test("replace all with a regular expression, whole word and case", () => {
  const view = fakeView(DOC);
  replaceAllMatches(view, { query: "harbour", replace: "port", wholeWord: true, caseSensitive: true });
  assert.equal(view.state.doc.toString(), "# Harbour\n\nThe **port** wall. harbours and Harbour.\n\nport");
  undo(view);
  replaceAllMatches(view, { query: "(h)arbour(s?)", replace: "$1aven$2", regexp: true, caseSensitive: true });
  assert.equal(view.state.doc.toString(), "# Harbour\n\nThe **haven** wall. havens and Harbour.\n\nhaven");
});

test("replace next replaces the selected match and selects the next", () => {
  const view = fakeView("a cat and a cat");
  view.dispatch({ effects: setSearchQuery.of(toSearchQuery({ query: "cat", replace: "dog" })), selection: EditorSelection.single(2, 5) });
  replaceNext(view);
  assert.equal(view.state.doc.toString(), "a dog and a dog".slice(0, 10) + "a cat");
  const sel = view.state.selection.main;
  assert.equal(view.state.sliceDoc(sel.from, sel.to), "cat");
  assert.equal(undo(view), true);
  assert.equal(view.state.doc.toString(), "a cat and a cat");
});

test("find bar: toggles, count, keys, replace row and Reading", () => {
  const { document } = installDOM();
  const calls = [];
  const bar = createFindBar({
    onQuery: (s, why) => calls.push(["query", why, s]),
    onStep: (dir) => calls.push(["step", dir]),
    onReplace: () => calls.push(["replace"]),
    onReplaceAll: () => calls.push(["replaceAll"]),
    onClose: () => calls.push(["close"]),
    onSwitchToSource: () => calls.push(["source"]),
  });
  document.body.appendChild(bar.element);
  assert.equal(bar.element.getAttribute("role"), "search");
  assert.equal(bar.isOpen, false);
  bar.show();
  assert.equal(bar.isOpen, true);

  bar.findInput.value = "harbour";
  bar.findInput.dispatchEvent(new FakeEvent("input", { bubbles: true }));
  assert.deepEqual(calls.pop(), ["query", "query", { query: "harbour", replace: "", caseSensitive: false, wholeWord: false, regexp: false }]);

  const caseBtn = bar.element.querySelector("[data-option=caseSensitive]");
  assert.equal(caseBtn.getAttribute("aria-pressed"), "false");
  assert.equal(caseBtn.getAttribute("aria-label"), "Match case");
  caseBtn.dispatchEvent(new FakeEvent("click", { bubbles: true }));
  assert.equal(caseBtn.getAttribute("aria-pressed"), "true");
  assert.equal(calls.pop()[2].caseSensitive, true);
  bar.findInput.dispatchEvent(new FakeEvent("keydown", { bubbles: true, key: "r", code: "KeyR", altKey: true }));
  assert.equal(bar.element.querySelector("[data-option=regexp]").getAttribute("aria-pressed"), "true");
  assert.equal(calls.pop()[1], "regexp");

  bar.findInput.dispatchEvent(new FakeEvent("keydown", { bubbles: true, key: "Enter" }));
  bar.findInput.dispatchEvent(new FakeEvent("keydown", { bubbles: true, key: "Enter", shiftKey: true }));
  assert.deepEqual(calls.splice(-2), [["step", 1], ["step", -1]]);

  bar.setCount({ index: 2, total: 12 });
  const count = bar.element.querySelector(".ms-find-count");
  assert.equal(count.textContent, "3 of 12");
  const short = bar.element.querySelector(".ms-find-count-short");
  assert.equal(short.textContent, "3/12");
  assert.equal(short.getAttribute("title"), "3 of 12");
  assert.equal(short.getAttribute("aria-hidden"), "true");
  assert.equal(count.getAttribute("aria-live"), "polite");
  assert.equal(bar.findInput.getAttribute("aria-describedby"), count.id);
  const next = bar.element.querySelector(".ms-find-next");
  assert.equal(next.getAttribute("aria-disabled"), null);
  bar.setCount({ total: 0 });
  assert.equal(count.textContent, "No results");
  assert.equal(next.getAttribute("aria-disabled"), "true");
  bar.setCount({ error: "Invalid regular expression" });
  assert.equal(bar.findInput.getAttribute("aria-invalid"), "true");
  assert.match(count.textContent, /Invalid regular expression/);
  bar.setCount({ index: 0, total: 1 });
  assert.equal(bar.findInput.getAttribute("aria-invalid"), null);

  // Replace row behind the disclosure.
  const disclosure = bar.element.querySelector(".ms-find-disclosure");
  const row = bar.element.querySelector(".ms-find-replace");
  assert.equal(row.hidden, true);
  assert.equal(disclosure.getAttribute("aria-controls"), row.id);
  disclosure.dispatchEvent(new FakeEvent("click", { bubbles: true }));
  assert.equal(row.hidden, false);
  assert.equal(disclosure.getAttribute("aria-expanded"), "true");
  bar.replaceInput.dispatchEvent(new FakeEvent("keydown", { bubbles: true, key: "Enter" }));
  bar.replaceInput.dispatchEvent(new FakeEvent("keydown", { bubbles: true, key: "Enter", ctrlKey: true }));
  assert.deepEqual(calls.splice(-2), [["replace"], ["replaceAll"]]);
  const labels = [...bar.element.querySelectorAll("button")].map((b) => b.getAttribute("aria-label") || b.textContent.trim());
  assert.ok(labels.includes("Replace") && labels.includes("Replace all") && labels.includes("Close find"));

  // Reading hides Replace and shows the link to Source.
  const link = bar.element.querySelector(".ms-find-source");
  bar.setMode({ reading: true, canEdit: true });
  assert.equal(disclosure.hidden, true);
  assert.equal(row.hidden, true);
  assert.equal(link.hidden, false);
  assert.equal(link.textContent.trim(), "Switch to Source to replace");
  link.dispatchEvent(new FakeEvent("click", { bubbles: true }));
  assert.deepEqual(calls.pop(), ["source"]);
  bar.setMode({ reading: false, canEdit: true });
  assert.equal(row.hidden, false);
  assert.equal(link.hidden, true);
  // Without edit access there is no Replace and no link.
  bar.setMode({ reading: true, canEdit: false });
  assert.equal(link.hidden, true);
  bar.setMode({ reading: false, canEdit: false });
  assert.equal(disclosure.hidden, true);
  assert.equal(row.hidden, true);

  // Escape closes and does not reach the editor's handler.
  let reached = false;
  document.body.addEventListener("keydown", () => { reached = true; });
  const esc = new FakeEvent("keydown", { bubbles: true, key: "Escape" });
  bar.findInput.dispatchEvent(esc);
  assert.deepEqual(calls.pop(), ["close"]);
  assert.equal(esc.defaultPrevented, true);
  assert.equal(reached, false);

  // setState fills the fields without calling onQuery.
  const n = calls.length;
  bar.setState({ query: "tide", replace: "flow", wholeWord: true });
  assert.equal(bar.findInput.value, "tide");
  assert.equal(bar.replaceInput.value, "flow");
  assert.equal(bar.element.querySelector("[data-option=wholeWord]").getAttribute("aria-pressed"), "true");
  assert.equal(calls.length, n);
});
