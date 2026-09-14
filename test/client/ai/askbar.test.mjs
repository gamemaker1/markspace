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

const A = await import("../../../internal/web/static/js/ai/askbar.js");
const { EditorState, markdownSupport } = await import("../../../internal/web/static/vendor/codemirror.js");

const labels = (ctx) => A.suggestedActions(ctx).map((a) => a.label);

test("the suggested action table matches docs/assistant.md for every context", () => {
  assert.deepEqual(labels({ kind: "prose" }), ["Improve", "Shorten", "Fix spelling and grammar", "Change tone", "Translate", "Explain"]);
  assert.deepEqual(labels({ kind: "heading" }), ["Draft this section", "Outline", "Continue"]);
  assert.deepEqual(labels({ kind: "paragraph" }), ["Continue", "Summarize above"]);
  assert.deepEqual(labels({ kind: "list" }), ["Sort", "Group", "Turn into table"]);
  assert.deepEqual(labels({ kind: "table" }), ["Add column", "Fill empty cells", "Summarize table", "Sort by column"]);
  assert.deepEqual(labels({ kind: "code" }), ["Explain code", "Add comments", "Find bugs"]);
  assert.deepEqual(labels({ kind: "link" }), ["Summarize page", "Quote key points"]);
  assert.deepEqual(labels({ kind: "wikilink" }), ["Summarize linked note", "Compare with this note"]);
  assert.deepEqual(labels({ kind: "image" }), []);
  assert.deepEqual(labels({ kind: "unknown" }), []);
  for (const [id, a] of Object.entries(A.ACTIONS)) {
    assert.ok(["replace", "insert", "answer"].includes(a.output), id);
    assert.ok(a.task, id);
  }
  assert.equal(A.ACTIONS.tone.sub, "tone");
  assert.equal(A.ACTIONS.translate.sub, "language");
  assert.equal(A.TONES.length, 5);
});

test("classifyContext reads the node chain and the line", () => {
  assert.equal(A.classifyContext({ selected: true, selectionText: "Some prose here." }).kind, "prose");
  assert.equal(A.classifyContext({ selected: true, selectionText: "- one\n- two\n\n3. three" }).kind, "list");
  assert.equal(A.classifyContext({ selected: true, selectionText: "- one\nplain" }).kind, "prose");
  assert.deepEqual(A.classifyContext({ chain: [{ name: "CodeText", from: 5, to: 9 }, { name: "FencedCode", from: 0, to: 20 }] }), { kind: "code", block: { from: 0, to: 20 } });
  assert.deepEqual(A.classifyContext({ selected: true, chain: [{ name: "TableCell" }, { name: "Table", from: 3, to: 40 }] }), { kind: "table", block: { from: 3, to: 40 } });
  assert.equal(A.classifyContext({ lineText: "## Findings", prevText: "" }).kind, "heading");
  assert.equal(A.classifyContext({ lineText: "", prevText: "# Title\n\n## Findings\n\n" }).kind, "heading");
  assert.equal(A.classifyContext({ lineText: "", prevText: "## Findings\n\nA paragraph.\n\n" }).kind, "paragraph");
  assert.equal(A.classifyContext({ lineText: "The estimate looks high." }).kind, "paragraph");
  assert.deepEqual(A.classifyContext({ lineText: "See [[Methods#Tide|how]] here", pos: 8, lineFrom: 0 }), { kind: "wikilink", target: "Methods" });
  assert.deepEqual(A.classifyContext({ lineText: "Read [the spec](https://example.com/spec) now", pos: 10, lineFrom: 0 }), { kind: "link", url: "https://example.com/spec" });
  assert.deepEqual(A.classifyContext({ lineText: "Go to https://example.com/a now", pos: 12, lineFrom: 0 }), { kind: "link", url: "https://example.com/a" });
  assert.equal(A.classifyContext({ lineText: "Read [notes](Methods.md) now", pos: 10, lineFrom: 0 }).kind, "paragraph", "a relative link is no web page");
  assert.deepEqual(A.classifyContext({ lineText: "![Depth chart](/files/s/01A/c.png)", pos: 134, lineFrom: 100 }).image,
    { from: 100, to: 134, alt: "Depth chart", url: "/files/s/01A/c.png" });
});

test("detectContext classifies real markdown through the syntax tree", () => {
  const doc = [
    "# Harbour survey", // 0
    "",
    "The survey covers the north quay.",
    "",
    "## Findings",
    "",
    "- Silt rose 18 cm",
    "- The crane rail is sound",
    "",
    "| Month | Depth |",
    "| --- | --- |",
    "| March | 12 |",
    "",
    "```js",
    "const depth = 18;",
    "```",
    "",
    "See [the spec](https://example.com/spec) and [[Methods]].",
    "",
    "![Depth chart](/files/harbour/01ABC/chart.png)",
  ].join("\n");
  const state = EditorState.create({ doc, extensions: [markdownSupport([])] });
  const at = (needle, offset = 0) => doc.indexOf(needle) + offset;
  const kind = (from, to = from) => A.detectContext(state, from, to).kind;
  assert.equal(kind(at("The survey"), at("quay.") + 5), "prose");
  assert.equal(kind(at("- Silt"), at("sound") + 5), "list");
  assert.equal(kind(at("north") + 2), "paragraph");
  assert.equal(kind(at("## Findings") + 5), "heading");
  assert.equal(kind(at("## Findings") + 12), "heading", "the empty line under a heading");
  const table = A.detectContext(state, at("March") + 2);
  assert.equal(table.kind, "table");
  assert.equal(doc.slice(table.block.from, table.block.to).split("\n")[0], "| Month | Depth |");
  const code = A.detectContext(state, at("const") + 3);
  assert.equal(code.kind, "code");
  assert.ok(doc.slice(code.block.from, code.block.to).startsWith("```js"));
  assert.deepEqual(A.detectContext(state, at("the spec") + 2), { kind: "link", url: "https://example.com/spec" });
  assert.deepEqual(A.detectContext(state, at("Methods") + 2), { kind: "wikilink", target: "Methods" });
  const img = A.detectContext(state, doc.length);
  assert.equal(img.kind, "image");
  assert.equal(img.image.url, "/files/harbour/01ABC/chart.png");
});

test("textBlockAt finds fenced code and tables before the parser reaches them", () => {
  const doc = "Intro\n\n```js\nconst a = 1;\n```\n\n| A | B |\n| --- | --- |\n| 1 | 2 |\n\nEnd";
  const code = A.textBlockAt(doc, doc.indexOf("const") + 2);
  assert.deepEqual([code.name, doc.slice(code.from, code.to)], ["FencedCode", "```js\nconst a = 1;\n```"]);
  const table = A.textBlockAt(doc, doc.indexOf("| 1") + 2);
  assert.deepEqual([table.name, doc.slice(table.from, table.to)], ["Table", "| A | B |\n| --- | --- |\n| 1 | 2 |"]);
  assert.equal(A.textBlockAt(doc, 2), null);
  assert.equal(A.textBlockAt(doc, doc.length - 1), null);
  assert.equal(A.textBlockAt("| just a pipe |\ntext", 3), null, "a table needs a separator row");
  assert.equal(A.textBlockAt("```\nopen", 6).name, "FencedCode", "an unclosed fence runs to the end");
});

test("defaultOutput follows the action, a question mark and the selection", () => {
  assert.equal(A.defaultOutput({ action: A.ACTIONS.improve, hasTarget: true }), "replace");
  assert.equal(A.defaultOutput({ action: A.ACTIONS.improve, hasTarget: false }), "insert");
  assert.equal(A.defaultOutput({ action: A.ACTIONS.continue, hasTarget: true }), "insert");
  assert.equal(A.defaultOutput({ action: A.ACTIONS.explain, hasTarget: true }), "answer");
  assert.equal(A.defaultOutput({ action: A.ACTIONS.findBugs }), "answer");
  assert.equal(A.defaultOutput({ instruction: "What does this mean? ", hasTarget: true }), "answer");
  assert.equal(A.defaultOutput({ instruction: "Make it a haiku", hasTarget: true }), "replace");
  assert.equal(A.defaultOutput({ instruction: "Write a closing line" }), "insert");
});

test("buildRequest maps actions, choices, questions, images and refinement", () => {
  const note = { id: "n1", spaceId: "s1" };
  const chips = [{ kind: "selection", ref: { kind: "selection", text: "abc" } }, { kind: "search", ref: { kind: "search", query: "" } }];
  const improve = A.buildRequest({ action: A.ACTIONS.improve, output: "replace", chips, note, text: "abc" });
  assert.deepEqual(improve, {
    noteId: "n1", spaceId: "s1", output: "replace", task: "improve", text: "abc", feature: "rewrite",
    context: [{ kind: "selection", text: "abc" }, { kind: "search", query: "Improve" }],
  });
  const tone = A.buildRequest({ action: A.ACTIONS.tone, output: "replace", note, text: "abc", choice: "Friendly" });
  assert.equal(tone.task, "custom");
  assert.match(tone.instruction, /friendly tone/);
  const translate = A.buildRequest({ action: A.ACTIONS.translate, output: "replace", note, text: "abc", choice: "French" });
  assert.deepEqual([translate.task, translate.language], ["translate", "French"]);
  const ask = A.buildRequest({ instruction: "Why is silt rising?", output: "answer", note, model: { providerId: "or", model: "gpt" } });
  assert.deepEqual([ask.task, ask.question, ask.feature, ask.providerId, ask.model], ["ask", "Why is silt rising?", "ask", "or", "gpt"]);
  const free = A.buildRequest({ instruction: "Write a closing line", output: "insert", note, text: "before" });
  assert.deepEqual([free.task, free.instruction, free.text], ["custom", "Write a closing line", "before"]);
  const image = A.buildRequest({ instruction: "What is shown?", output: "answer", note, chips: [{ kind: "file", image: true, ref: { kind: "file", fileId: "f1" } }] });
  assert.equal(image.route, "vision");
  const refine = A.buildRequest({
    action: A.ACTIONS.addComments, instruction: "Shorter comments", output: "replace", note, text: "code",
    history: [{ role: "user", content: "Add comments" }, { role: "assistant", content: "old" }],
  });
  assert.ok(refine.instruction.startsWith(A.ACTIONS.addComments.instruction));
  assert.ok(refine.instruction.endsWith("Shorter comments"));
  assert.equal(refine.history.length, 2);
  const shorten = A.buildRequest({ action: A.ACTIONS.shorten, instruction: "keep numbers", output: "replace", note, text: "t" });
  assert.equal(shorten.instruction, "keep numbers", "a canned task sends the extra instruction as is");
});

test("cleanResult strips a wrapping fence and keeps the line ending of the old text", () => {
  assert.equal(A.cleanResult("```markdown\nNew text\n```", "Old text"), "New text");
  assert.equal(A.cleanResult("```js\nx()\n```", "```js\ny()\n```"), "```js\nx()\n```");
  assert.equal(A.cleanResult("New text\n\n", "Old"), "New text");
  assert.equal(A.cleanResult("New text", "Old\n"), "New text\n");
  assert.equal(A.headingAbove("# A\ntext\n## B *bold*\nmore", 22), "B bold");
  assert.equal(A.headingAbove("no heading", 5), "");
  assert.deepEqual(A.parseImageSyntax('![a b](<x y.png> "t")', 7), { from: 7, to: 28, alt: "a b", url: "x y.png" });
  assert.deepEqual(A.parseImageSyntax("![](chart.png)"), { from: 0, to: 14, alt: "", url: "chart.png" });
  assert.equal(A.parseImageSyntax("[not](an image)"), null);
});
