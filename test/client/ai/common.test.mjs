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

const C = await import("../../../internal/web/static/js/ai/common.js");

test("parseMention finds an @ mention that ends at the caret", () => {
  assert.deepEqual(C.parseMention("Ask @Harb", 9), { start: 4, end: 9, query: "Harb", note: "Harb", heading: null });
  assert.deepEqual(C.parseMention("@", 1), { start: 0, end: 1, query: "", note: "", heading: null });
  assert.deepEqual(C.parseMention("see (@Harbour survey#Find", 25), { start: 5, end: 25, query: "Harbour survey#Find", note: "Harbour survey", heading: "Find" });
  assert.deepEqual(C.parseMention("x @Methods#", 11).heading, "");
  assert.equal(C.parseMention("mail ada@example.com", 20), null, "an @ inside a word is no mention");
  assert.equal(C.parseMention("@ space", 7), null, "a space right after @ ends it");
  assert.equal(C.parseMention("@line\nnext", 10), null, "a line break ends it");
  assert.equal(C.parseMention("@a @b", 5).query, "b");
  assert.equal(C.parseMention("Ask @Harb more", 9).query, "Harb", "the caret decides the end");
  assert.equal(C.parseMention("@" + "x".repeat(90), 91), null, "a very long run is no mention");
});

test("mentionMatches ranks specials, notes and files and returns their chips", () => {
  const notes = [
    { id: "n1", title: "Harbour survey", path: "Harbour survey.md", content: "# Harbour survey\n\n## Findings\n\nx\n\n```\n## not a heading\n```\n\n## Estimate\n" },
    { id: "n2", title: "Methods", path: "Survey/Methods.md", content: "# Methods" },
  ];
  const files = [{ id: "f1", name: "depth-chart.png", mime: "image/png", size: 900 }, { id: "f2", name: "report.pdf", size: 5000 }];
  const q = (text) => C.mentionMatches(C.parseMention(text, text.length), { notes, files });

  const all = q("@");
  assert.deepEqual(all.slice(0, 3).map((m) => m.type), ["special", "special", "special"]);
  assert.equal(all.length, 6, "the list stops at 6");

  const harb = q("@harb");
  assert.equal(harb[0].label, "Harbour survey");
  assert.deepEqual(harb[0].chip.ref, { kind: "note", noteId: "n1" });

  const surv = q("@surv");
  assert.deepEqual(surv.map((m) => m.label), ["Harbour survey"], "a word prefix matches");

  const img = q("@depth");
  assert.equal(img[0].type, "file");
  assert.equal(img[0].sub, "Image");
  assert.deepEqual(img[0].chip.ref, { kind: "file", fileId: "f1" });
  assert.equal(img[0].chip.badge, "Image");
  assert.equal(q("@report")[0].chip.badge, "File");

  assert.equal(q("@link")[0].chip.kind, "linked", "a prefix match ranks before a substring match");
  assert.deepEqual(q("@back").map((m) => m.chip.kind), ["backlinks"]);
  assert.deepEqual(q("@space").map((m) => m.chip.kind), ["space"]);

  const sections = q("@Harbour survey#");
  assert.deepEqual(sections.map((m) => m.label), ["Harbour survey", "Findings", "Estimate"], "fenced lines are no headings");
  const est = q("@Harbour survey#est");
  assert.deepEqual(est[0].chip.ref, { kind: "section", noteId: "n1", heading: "Estimate" });
  assert.deepEqual(q("@Nothing#x"), []);
  assert.equal(C.specialChip("linked", "n9").ref.noteId, "n9");
  assert.deepEqual(C.specialChip("space", "n9").ref, { kind: "search", query: "" });
});

test("chips estimate the prompt at 4 characters per token and warn past 75 percent", () => {
  const chips = [C.selectionChip("x".repeat(400)), { key: "a", chars: 1600, ref: {} }];
  assert.deepEqual(C.promptEstimate(chips, "abcd", 48000), { chars: 2004, tokens: 501, warn: false });
  assert.equal(C.promptEstimate([{ chars: 36001, ref: {} }], "", 48000).warn, true);
  assert.equal(C.promptEstimate([{ chars: 36000, ref: {} }], "", 48000).warn, false);
  assert.equal(C.formatTokens(950), "950");
  assert.equal(C.formatTokens(1234), "1.2k");
  assert.equal(C.formatTokens(61100), "61k");
  assert.deepEqual(C.refsFromChips([{ ref: { kind: "search", query: "" } }, { ref: { kind: "note", noteId: "n" } }], "rollback plan"),
    [{ kind: "search", query: "rollback plan" }, { kind: "note", noteId: "n" }]);
  assert.deepEqual(C.urlsInText("see https://example.com/a, and https://example.com/b. http://no.example"), ["https://example.com/a", "https://example.com/b"]);
  assert.equal(C.sectionText("# A\n\n## B\nb text\n### C\nc\n## D\nd", "b"), "## B\nb text\n### C\nc");
});

test("modelChoices filters capable enabled models and orders them by price", () => {
  const price = (i, o) => ({ inputPerMTok: i, outputPerMTok: o });
  const models = [
    { providerId: "or", model: "openai/gpt-4o", name: "GPT-4o", inputs: ["text", "image"], outputs: ["text"], effectivePrice: price(2.5, 10), enabled: true, available: true },
    { providerId: "or", model: "openai/gpt-4o-mini", name: "GPT-4o mini", inputs: ["text", "image"], outputs: ["text"], effectivePrice: price(0.15, 0.6), enabled: true, available: true },
    { providerId: "local", model: "llama3", name: "Llama 3", providerName: "Ollama", inputs: ["text"], outputs: ["text"], local: true, enabled: true, available: true },
    { providerId: "or", model: "mystery", inputs: ["text"], outputs: ["text"], enabled: true, available: true },
    { providerId: "or", model: "openrouter/auto", inputs: ["text"], outputs: ["text"], effectivePrice: price(-1, -1), enabled: true, available: true },
    { providerId: "or", model: "off", inputs: ["text"], outputs: ["text"], effectivePrice: price(0, 0), enabled: false, available: true },
    { providerId: "or", model: "gone", inputs: ["text"], outputs: ["text"], effectivePrice: price(0, 0), enabled: true, available: false },
    { providerId: "pics", model: "sdxl", inputs: ["text"], outputs: ["image"], effectivePrice: { perImageOut: 0.04 }, enabled: true, available: true },
    { providerId: "or", model: "embed", inputs: ["text"], outputs: ["embedding"], enabled: true, available: true },
  ];
  assert.deepEqual(C.modelChoices(models, { capability: "text", limit: 0 }).map((m) => m.model),
    ["llama3", "openai/gpt-4o-mini", "openai/gpt-4o", "mystery", "openrouter/auto"]);
  assert.deepEqual(C.modelChoices(models, { capability: "vision" }).map((m) => m.model), ["openai/gpt-4o-mini", "openai/gpt-4o"]);
  assert.deepEqual(C.modelChoices(models, { capability: "image.generate" }).map((m) => [m.model, m.price]), [["sdxl", "$0.04 per image"]]);
  assert.deepEqual(C.modelChoices(models, { capability: "text", query: "4o" }).map((m) => m.model), ["openai/gpt-4o-mini", "openai/gpt-4o"]);
  assert.deepEqual(C.modelChoices(models, { capability: "text", query: "ollama" }).map((m) => m.model), ["llama3"]);
  assert.equal(C.modelChoices(models, { capability: "text", limit: 2 }).length, 2);
  const first = C.modelChoices(models, { capability: "text" })[0];
  assert.deepEqual([first.providerId, first.price, first.provider], ["local", "Local", "Ollama"]);
  assert.equal(C.modelChoices(models, { capability: "vision" })[0].price, "$0.15 / $0.60 per M");
  assert.equal(C.modelChipLabel(null), "Auto");
  assert.equal(C.modelChipLabel({ label: "A very long model name that keeps going" }).length, 22);
});

test("fileIdFromUrl reads file URLs and resolves relative paths", () => {
  assert.equal(C.fileIdFromUrl("/files/harbour/01ABC/depth%20chart.png"), "01ABC");
  assert.equal(C.fileIdFromUrl("https://ms.example/files/harbour/01ABC/x.png"), "01ABC");
  assert.equal(C.fileIdFromUrl("/api/files/01DEF/content"), "01DEF");
  assert.equal(C.fileIdFromUrl("images/chart.png"), "");
  assert.equal(C.fileIdFromUrl("images/chart.png", { resolve: (u) => (u === "images/chart.png" ? "01GHI" : "") }), "01GHI");
  assert.equal(C.fileIdFromUrl("https://example.com/chart.png", { resolve: () => "no" }), "");
  assert.equal(C.fileIdFromUrl("data:image/png;base64,AAA", { resolve: () => "no" }), "");
  assert.equal(C.fileIdFromUrl("x.png", { resolve: () => { throw new Error("boom"); } }), "");
});

function streamFetch(chunks, { status = 200, json } = {}) {
  return async () => {
    if (status !== 200) return { ok: false, status, statusText: "Bad", json: async () => json || {} };
    const enc = new TextEncoder();
    let i = 0;
    return {
      ok: true, status, body: {
        getReader: () => ({
          read: async () => (i < chunks.length ? { value: enc.encode(chunks[i++]), done: false } : { value: undefined, done: true }),
          cancel: async () => {},
        }),
      },
    };
  };
}

test("streamEvents parses frames across chunk boundaries", async () => {
  const seen = [];
  const done = await C.streamEvents("/api/ai/image/stream", {}, {
    fetch: streamFetch(["event: progress\ndata: {\"done\":0,", "\"total\":2}\n\nevent: progress\r\ndata: {\"done\":1,\"total\":2}\r\n\r\n", "event: done\ndata: {\"done\":true,\"images\":[]}\n\n"]),
    onEvent: (name, data) => seen.push([name, data.done]),
  });
  assert.deepEqual(seen, [["progress", 0], ["progress", 1], ["done", true]]);
  assert.deepEqual(done, { done: true, images: [] });
  await assert.rejects(C.streamEvents("/x", {}, { fetch: streamFetch(["event: error\ndata: {\"error\":\"model down\"}\n\n"]) }), /model down/);
  await assert.rejects(C.streamEvents("/x", {}, { fetch: streamFetch([], { status: 404, json: { error: "not found" } }) }), (err) => err.status === 404 && /not found/.test(err.message));
  assert.deepEqual(await C.streamEvents("/x", {}, { fetch: streamFetch(["event: delta\ndata: {\"delta\":\"a\"}\n\n"]) }), { done: false, incomplete: true });
  assert.deepEqual(C.splitSSE("event: a\ndata: 1\n\nevent: b\ndata: 2", false), { events: [{ event: "a", data: "1" }], rest: "event: b\ndata: 2" });
});
