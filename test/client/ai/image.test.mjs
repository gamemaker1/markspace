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

const I = await import("../../../internal/web/static/js/ai/imageactions.js");
const T = await import("../../../internal/web/static/js/ai/aitab.js");

const ids = (input) => I.imageActionAvailability(input).actions.map((a) => a.id);

test("image actions follow the capabilities, the access and the connection", () => {
  const full = { online: true, canEdit: true, fileId: "f1", vision: true, generate: true, edit: true, alt: "" };
  assert.deepEqual(ids(full), ["describe", "alt", "extract", "notes", "ask", "edit", "variations", "open", "rename", "copy"]);

  // Without a vision model the AI actions hide and one link asks for a model.
  const noVision = I.imageActionAvailability({ ...full, vision: false });
  assert.deepEqual(noVision.actions.map((a) => a.id), ["edit", "open", "rename", "copy"], "variations needs alt text or a vision model");
  assert.equal(noVision.addVision, true);
  assert.deepEqual(ids({ ...full, vision: false, alt: "Depth chart" }), ["edit", "variations", "open", "rename", "copy"]);
  assert.equal(I.imageActionAvailability(full).addVision, false);

  // A reader keeps the answers and loses every change.
  assert.deepEqual(ids({ ...full, canEdit: false }), ["describe", "ask", "open", "copy"]);

  // An image that is not a file of the space has no AI context.
  assert.deepEqual(ids({ ...full, fileId: "" }), ["open", "copy"]);
  assert.deepEqual(ids({ ...full, fileId: "", alt: "A lighthouse" }), ["variations", "open", "copy"]);
  assert.equal(I.imageActionAvailability({ ...full, fileId: "" }).addVision, false);

  // Offline, AI actions stay and are disabled, and rename waits for the connection.
  const offline = I.imageActionAvailability({ ...full, online: false }).actions;
  assert.deepEqual(offline.filter((a) => a.disabled).map((a) => a.id), ["describe", "alt", "extract", "notes", "ask", "edit", "variations", "rename"]);
  assert.deepEqual(offline.filter((a) => !a.disabled).map((a) => a.id), ["open", "copy"]);

  // No image capability at all.
  assert.deepEqual(ids({ online: true, canEdit: true, fileId: "f1" }), ["open", "rename", "copy"]);
});

test("invertMask makes painted pixels transparent and the rest opaque black", () => {
  const src = new Uint8ClampedArray([255, 0, 0, 0, 1, 2, 3, 128, 9, 9, 9, 255, 0, 0, 0, 0]);
  const dst = new Uint8ClampedArray(src.length);
  assert.equal(I.invertMask(src, dst), 2);
  assert.deepEqual([...dst], [0, 0, 0, 255, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 255]);
});

/** A 2d canvas stub that keeps RGBA pixels and scales with nearest neighbour. */
function stubCanvas(width, height, fill = null) {
  const data = new Uint8ClampedArray(width * height * 4);
  if (fill) fill(data, width, height);
  const canvas = {
    width, height, data, drawCalls: [],
    getContext: () => ctx,
    toDataURL: (type) => "data:" + type + ";base64," + Buffer.from(JSON.stringify({ w: canvas.width, h: canvas.height, alpha: [...canvas.data].filter((_v, i) => i % 4 === 3) })).toString("base64"),
  };
  const ctx = {
    drawImage(src, x, y, w, h) {
      canvas.drawCalls.push([x, y, w, h]);
      for (let py = 0; py < h; py++) {
        for (let px = 0; px < w; px++) {
          const sx = Math.floor((px * src.width) / w);
          const sy = Math.floor((py * src.height) / h);
          const si = (sy * src.width + sx) * 4;
          const di = (py * canvas.width + px) * 4;
          for (let k = 0; k < 4; k++) canvas.data[di + k] = src.data[si + k];
        }
      }
    },
    getImageData: (x, y, w, h) => ({ width: w, height: h, data: canvas.data.slice(0, w * h * 4) }),
    createImageData: (w, h) => ({ width: w, height: h, data: new Uint8ClampedArray(w * h * 4) }),
    putImageData: (img) => { canvas.data.set(img.data); },
  };
  return canvas;
}

test("maskPNG scales the painted layer to the image and encodes the mask", () => {
  // A 2 by 2 paint layer at display size with the left column painted.
  const paint = stubCanvas(2, 2, (d) => { d[3] = 200; d[11] = 90; });
  const made = [];
  const { base64, painted } = I.maskPNG(paint, 4, 2, { createCanvas: (w, h) => { const c = stubCanvas(w, h); made.push(c); return c; } });
  assert.equal(made.length, 2, "one scaled layer and one mask");
  assert.deepEqual(made[0].drawCalls, [[0, 0, 4, 2]]);
  assert.equal(painted, 4, "the painted column covers two pixels of each row");
  const decoded = JSON.parse(Buffer.from(base64, "base64").toString());
  assert.deepEqual(decoded, { w: 4, h: 2, alpha: [0, 0, 255, 255, 0, 0, 255, 255] });
  assert.ok(!base64.startsWith("data:"), "the data URL prefix is stripped");
});

test("saveChoice picks a version unless another note embeds the image", () => {
  assert.deepEqual(I.saveChoice([], "n1"), { mode: "version", others: [] });
  assert.deepEqual(I.saveChoice([{ fromNote: { id: "n1", title: "Survey" } }], "n1"), { mode: "version", others: [] });
  const shared = I.saveChoice([{ fromNote: { id: "n1" } }, { fromNote: { id: "n2", title: "Budget" } }, { fromNote: { id: "n2", title: "Budget" } }, { fromNote: { id: "n3" } }], "n1");
  assert.deepEqual(shared, { mode: "copy", others: [{ id: "n2", title: "Budget" }, { id: "n3", title: "Untitled" }] });
  assert.equal(I.saveLineText(shared, "chart.png"), "Saves a copy, chart edited.png, because Budget and Untitled embed this image. This note links to the copy.");
  assert.equal(I.saveLineText({ mode: "version", others: [] }, "chart.png"), "Saves a new version of chart.png. No other note embeds it.");
  assert.equal(I.saveLineText({ mode: "version", others: [{ id: "n2", title: "Budget" }] }, "chart.png"), "Saves a new version of chart.png. Budget shows the new version too.");
  assert.equal(I.saveLineText({ mode: "copy", others: [] }, "a.b.webp"), "Saves a copy, a.b edited.webp, and this note links to it.");
  const blob = I.dataUrlToBlob("data:image/png;base64," + Buffer.from("png!").toString("base64"));
  assert.equal(blob.type, "image/png");
  assert.equal(blob.size, 4);
});

test("usageLine formats tokens and cost and warns at 80 percent", () => {
  const base = { today: { tokens: 1500 }, month: { tokens: 61100, cost: 0.19 }, currency: "USD" };
  assert.deepEqual(T.usageLine({ ...base, budget: { tokenLimit: 0, costLimit: 0, usedPct: 0 } }),
    { text: "Today 1.5k tokens · This month 61k tokens · $0.19", pct: null, warn: false, exceeded: false });
  assert.deepEqual(T.usageLine({ ...base, budget: { tokenLimit: 100000, usedPct: 61.1 } }).pct, 61.1);
  assert.equal(T.usageLine({ ...base, budget: { tokenLimit: 100000, usedPct: 61.1 } }).warn, false);
  assert.equal(T.usageLine({ ...base, budget: { tokenLimit: 100000, usedPct: 80 } }).warn, true);
  assert.equal(T.usageLine({ ...base, budget: { costLimit: 5, usedPct: 140, exceeded: true } }).pct, 100);
  assert.equal(T.usageLine({ today: { inputTokens: 10, outputTokens: 5 }, month: {} }).text, "Today 15 tokens · This month 0 tokens");
  assert.equal(T.usageLine(null), null);
  assert.equal(T.formatCost(0.004), "<$0.01");
  assert.equal(T.formatCost(2, "EUR"), "EUR 2.00");
});

test("noteFromAnswer keeps citation numbers in the source list", () => {
  const out = T.noteFromAnswer({
    question: "What changed in the survey?\nsecond line",
    answer: "Silt rose [1] and the page says so [3].\n",
    sources: [
      { kind: "note", noteId: "n1", title: "Harbour survey" },
      { kind: "file", fileId: "f1", title: "chart.png", skipped: "model does not accept images" },
      { kind: "url", url: "https://example.com/tides", title: "Tide [tables]" },
    ],
  });
  assert.equal(out.title, "What changed in the survey?");
  assert.equal(out.content, "Silt rose [1] and the page says so [3].\n\n## Sources\n\n1. [[Harbour survey]]\n3. [Tide tables](https://example.com/tides)\n");
  assert.equal(T.noteFromAnswer({ answer: "x" }).title, "AI answer");
  assert.equal(T.noteFromAnswer({ question: "q".repeat(80), answer: "x" }).title.length, 60);
});
