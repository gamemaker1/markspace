/**
 * AI actions on images. Selecting an image in live preview shows a slim row
 * of actions under it, in the flow of the note. On a phone the keyboard bar
 * shows the same actions instead. Generate image and Variations stream into a
 * placeholder widget with progress, and Edit image opens a Dialog with a
 * brush mask, a before and after slider and one Save. On narrow viewports the
 * Dialog fills the screen and keeps its Close button.
 *
 * The availability rules, the mask PNG and the save rule are pure and have
 * node tests.
 */

import { StateField, StateEffect, EditorView, Decoration, WidgetType } from "/static/vendor/codemirror.js";
import { h, icon, button, announce, editInPlace, openDialog, progressBar, inlineMessage, setBusy, spinner, toggleSwitch, isNarrow, uid } from "/static/js/ui.js";
import { blockedOffline, streamEvents, fileIdFromUrl, aiCapabilities, copyToClipboard, AI_OFFLINE_MESSAGE } from "/static/js/ai/common.js";
import { openAskBar, parseImageSyntax } from "/static/js/ai/askbar.js";
import { startChange } from "/static/js/ai/tracked.js";

/* ---------- Availability ---------- */

/** The image actions in display order. ai marks actions that need a connection and a model. */
export const IMAGE_ACTIONS = [
  { id: "describe", label: "Describe", icon: "eye", ai: true },
  { id: "alt", label: "Alt text", icon: "tag", ai: true },
  { id: "extract", label: "Extract text", icon: "note", ai: true },
  { id: "notes", label: "Turn into notes", icon: "list-unordered", ai: true },
  { id: "ask", label: "Ask", icon: "sparkle", ai: true },
  { id: "edit", label: "Edit image", icon: "pencil", ai: true },
  { id: "variations", label: "Variations", icon: "duplicate", ai: true },
  { id: "open", label: "Open", icon: "image" },
  { id: "rename", label: "Rename file", icon: "pencil" },
  { id: "copy", label: "Copy link", icon: "link" },
];

/**
 * Decides which image actions show. Describe and Ask need a vision model and
 * a file. Alt text, Extract text and Turn into notes also need edit access,
 * because they make tracked changes. Edit image needs an editing model, and
 * Variations a generation model plus a prompt from the alt text or a vision
 * model. Offline, AI actions stay visible and disabled. Without a vision
 * model, addVision asks for the single "Add a vision model" link.
 * @param {{online?: boolean, canEdit?: boolean, fileId?: string, vision?: boolean, generate?: boolean, edit?: boolean, alt?: string}} input
 * @returns {{actions: {id: string, label: string, icon: string, ai?: boolean, disabled: boolean}[], addVision: boolean}}
 */
export function imageActionAvailability({ online = true, canEdit = false, fileId = "", vision = false, generate = false, edit = false, alt = "" } = {}) {
  const actions = [];
  for (const a of IMAGE_ACTIONS) {
    let visible = true;
    let disabled = false;
    switch (a.id) {
      case "describe":
      case "ask":
        visible = vision && !!fileId;
        break;
      case "alt":
      case "extract":
      case "notes":
        visible = vision && !!fileId && canEdit;
        break;
      case "edit":
        visible = edit && !!fileId && canEdit;
        break;
      case "variations":
        visible = generate && canEdit && (!!String(alt || "").trim() || (vision && !!fileId));
        break;
      case "rename":
        visible = canEdit && !!fileId;
        disabled = !online;
        break;
      default:
        break;
    }
    if (a.ai && !online) disabled = true;
    if (visible) actions.push({ ...a, disabled });
  }
  return { actions, addVision: !vision && !!fileId };
}

/* ---------- Mask and save rule ---------- */

/**
 * Turns painted pixels into a mask: transparent where the brush painted, so
 * the model may change them, and opaque black elsewhere.
 * @param {Uint8ClampedArray} src RGBA of the painted layer
 * @param {Uint8ClampedArray} dst RGBA of the mask, the same length
 * @returns {number} the painted pixel count
 */
export function invertMask(src, dst) {
  let painted = 0;
  for (let i = 0; i + 3 < src.length; i += 4) {
    const on = src[i + 3] > 0;
    dst[i] = 0;
    dst[i + 1] = 0;
    dst[i + 2] = 0;
    dst[i + 3] = on ? 0 : 255;
    if (on) painted++;
  }
  return painted;
}

/**
 * Renders the painted layer as a mask PNG at the natural size of the image
 * and returns the base64 data without the data URL prefix.
 * @param {HTMLCanvasElement} paint the canvas the brush painted on
 * @param {number} width natural width of the image
 * @param {number} height natural height of the image
 * @param {{createCanvas?: (w: number, h: number) => HTMLCanvasElement}} [opts]
 * @returns {{base64: string, painted: number}}
 */
export function maskPNG(paint, width, height, { createCanvas } = {}) {
  const make = createCanvas || ((w, hgt) => {
    const c = document.createElement("canvas");
    c.width = w;
    c.height = hgt;
    return c;
  });
  const scaled = make(width, height);
  const sctx = scaled.getContext("2d");
  sctx.drawImage(paint, 0, 0, width, height);
  const src = sctx.getImageData(0, 0, width, height);
  const out = make(width, height);
  const octx = out.getContext("2d");
  const dst = octx.createImageData(width, height);
  const painted = invertMask(src.data, dst.data);
  octx.putImageData(dst, 0, 0);
  const url = out.toDataURL("image/png");
  return { base64: url.slice(url.indexOf(",") + 1), painted };
}

/**
 * Chooses how Save stores an edited image: a new version when no other note
 * embeds the file, otherwise a copy.
 * @param {{fromNote?: {id: string, title?: string}}[]} backlinks
 * @param {string} noteId the note being edited
 * @returns {{mode: "version"|"copy", others: {id: string, title: string}[]}}
 */
export function saveChoice(backlinks, noteId) {
  const others = [];
  for (const b of backlinks || []) {
    const n = b && (b.fromNote || b.note || b);
    if (!n || !n.id || n.id === noteId || others.some((x) => x.id === n.id)) continue;
    others.push({ id: n.id, title: n.title || "Untitled" });
  }
  return { mode: others.length ? "copy" : "version", others };
}

/** The line above Save that states what it will do. */
export function saveLineText({ mode, others }, name) {
  const stem = String(name || "image").replace(/\.[^.]+$/, "");
  const ext = (/\.([^.]+)$/.exec(String(name || "")) || [, "png"])[1];
  const names = others.map((o) => o.title);
  const list = names.length <= 2 ? names.join(" and ") : names.slice(0, 2).join(", ") + " and " + (names.length - 2) + " more";
  if (mode === "copy") {
    return others.length
      ? `Saves a copy, ${stem} edited.${ext}, because ${list} ${others.length === 1 ? "embeds" : "embed"} this image. This note links to the copy.`
      : `Saves a copy, ${stem} edited.${ext}, and this note links to it.`;
  }
  return others.length
    ? `Saves a new version of ${name}. ${list} ${others.length === 1 ? "shows" : "show"} the new version too.`
    : `Saves a new version of ${name}. No other note embeds it.`;
}

/** Decodes a data URL into a Blob. */
export function dataUrlToBlob(dataUrl) {
  const [head, data] = String(dataUrl).split(",");
  const mime = (/data:([^;]+)/.exec(head) || [, "image/png"])[1];
  const bin = atob(data || "");
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return new Blob([bytes], { type: mime });
}

/* ---------- Configuration ---------- */

let cfg = {};
let caps = null;

/**
 * Reports a result or a failure. The editor passes notify, which shows
 * failures in a Banner. Without it the message is announced.
 * @param {string} message
 * @param {{variant?: "info"|"success"|"warning"|"critical"}} [opts]
 */
function notify(message, { variant = "info" } = {}) {
  if (typeof cfg.notify === "function") cfg.notify(message, { variant });
  else announce(message, { politeness: variant === "critical" ? "assertive" : "polite" });
}

/** Loads and caches the AI capabilities, then redraws. */
async function loadCaps(view) {
  try {
    caps = await aiCapabilities(cfg.api);
  } catch {
    caps = { text: false, vision: false, generate: false, edit: false, sizes: [] };
  }
  if (view && !view.destroyed) view.dispatch({ effects: refreshImages.of(null) });
  if (cfg.onChange) cfg.onChange();
  return caps;
}

/** The capabilities last loaded, or null. */
export function imageCaps() {
  return caps;
}

/* ---------- Selected image ---------- */

const refreshImages = StateEffect.define();
const refreshCounter = StateField.define({
  create: () => 0,
  update: (v, tr) => (tr.effects.some((e) => e.is(refreshImages)) ? v + 1 : v),
});

const pickImage = StateEffect.define();

/**
 * The image picked with a tap on its widget: {from, to} or null. Picking
 * leaves the cursor where it is, so live preview keeps the markup hidden. A
 * change of selection drops the pick.
 */
const pickedImageField = StateField.define({
  create: () => null,
  update(value, tr) {
    let picked = false;
    for (const e of tr.effects) {
      if (e.is(pickImage)) {
        value = e.value;
        picked = true;
      }
    }
    if (!picked && tr.selection) return null;
    if (value && tr.docChanged) value = { from: tr.changes.mapPos(value.from, 1), to: tr.changes.mapPos(value.to, -1) };
    return value;
  },
});

/** Picks the image whose markdown spans [from, to], which shows its actions. */
export function selectImageAt(view, from, to) {
  view.dispatch({ effects: pickImage.of({ from, to }) });
}

/**
 * Returns the picked image, or the image whose markdown holds the cursor:
 * {from, to, alt, url}, or null. Uploads still in progress have no actions.
 */
export function selectedImage(state) {
  const picked = state.field(pickedImageField, false);
  if (picked) {
    const found = parseImageSyntax(state.doc.sliceString(picked.from, Math.min(state.doc.length, picked.from + 4000)), picked.from);
    if (found && found.url && !found.url.startsWith("markspace-pending:")) return found;
  }
  const head = state.selection.main.head;
  if (!state.selection.main.empty) return null;
  const line = state.doc.lineAt(head);
  const re = /!\[[^\]\n]*\]\([^)\n]*\)/g;
  let m;
  while ((m = re.exec(line.text))) {
    const from = line.from + m.index;
    const to = from + m[0].length;
    if (head < from || head > to) continue;
    const img = parseImageSyntax(m[0], from);
    if (img && img.url && !img.url.startsWith("markspace-pending:")) return img;
  }
  return null;
}

function fileNameOf(url) {
  const last = String(url || "").split(/[?#]/)[0].split("/").pop() || "image";
  try { return decodeURIComponent(last); } catch { return last; }
}

function imageInfo(img) {
  const fileId = fileIdFromUrl(img.url, { resolve: cfg.resolveFileId });
  return { ...img, fileId, name: fileNameOf(img.url) };
}

/* ---------- Action row ---------- */

class ImageRowWidget extends WidgetType {
  constructor(img, key) { super(); this.img = img; this.key = key; }
  eq(o) { return o.key === this.key; }
  get estimatedHeight() { return 44; }
  toDOM(view) { return h("div", { class: "cm-ms-block image-actions-block" }, buildRow(view, this.img)); }
  ignoreEvent() { return true; }
}

function availability(img) {
  const online = cfg.isOnline ? cfg.isOnline() : true;
  const c = caps || { vision: false, generate: false, edit: false };
  return imageActionAvailability({ online, canEdit: !!cfg.canEdit, fileId: img.fileId, vision: c.vision, generate: c.generate, edit: c.edit, alt: img.alt });
}

function buildRow(view, raw) {
  const img = imageInfo(raw);
  const row = h("div", { class: "image-actions", role: "toolbar", "aria-label": "Image actions" });
  const draw = () => {
    row.replaceChildren();
    const nameEl = h("span", { class: "image-actions-name", text: img.name });
    row.appendChild(nameEl);
    const { actions, addVision } = availability(img);
    for (const a of actions) {
      const btn = button({ label: a.label, variant: "invisible", size: "small", leadingVisual: a.icon, inactive: a.disabled, className: "image-action" + (a.ai ? " is-ai" : ""), attrs: { "data-action": a.id } });
      btn.addEventListener("mousedown", (ev) => ev.preventDefault());
      btn.addEventListener("click", (ev) => {
        ev.preventDefault();
        runImageAction(view, a.id, raw, { nameEl, button: btn });
      });
      row.appendChild(btn);
    }
    if (addVision && caps && cfg.canEdit !== undefined) {
      row.appendChild(h("a", { class: "image-actions-link", href: cfg.settingsHref || "/settings/ai", "data-full-nav": "", text: "Add a vision model" }));
    }
    // Arrow keys move between the buttons of the toolbar.
    const items = [...row.querySelectorAll("button, a")];
    items.forEach((el, i) => {
      el.tabIndex = i === 0 ? 0 : -1;
      el.addEventListener("keydown", (ev) => {
        const next = ev.key === "ArrowRight" ? i + 1 : ev.key === "ArrowLeft" ? i - 1 : ev.key === "Home" ? 0 : ev.key === "End" ? items.length - 1 : null;
        if (next === null) return;
        ev.preventDefault();
        const target = items[(next + items.length) % items.length];
        for (const x of items) x.tabIndex = -1;
        target.tabIndex = 0;
        target.focus();
      });
    });
  };
  draw();
  if (!caps) loadCaps(view);
  return row;
}

/**
 * The image actions extension.
 * @param {{api: Function, note: () => object, isOnline: () => boolean, isPhone: () => boolean, canEdit: boolean,
 *   enabled: () => boolean, imageSrc?: (url: string) => Promise<string>, resolveFileId?: (url: string) => string,
 *   imageSave?: () => string, settingsHref?: string, fetch?: Function, onChange?: () => void}} config
 */
export function imageActions(config) {
  cfg = config;
  return [
    refreshCounter,
    pickedImageField,
    genField,
    EditorView.decorations.compute(["selection", "doc", refreshCounter, pickedImageField], (state) => {
      if (cfg.enabled && !cfg.enabled()) return Decoration.none;
      if (cfg.isPhone && cfg.isPhone()) return Decoration.none;
      const img = selectedImage(state);
      if (!img) return Decoration.none;
      const line = state.doc.lineAt(img.to);
      const key = [img.from, img.url, img.alt, state.field(refreshCounter), cfg.isOnline ? cfg.isOnline() : true].join("|");
      return Decoration.set([Decoration.widget({ widget: new ImageRowWidget(img, key), block: true, side: 2 }).range(line.to)]);
    }),
    EditorView.decorations.compute([genField], (state) => {
      const list = state.field(genField);
      if (!list.length) return Decoration.none;
      return Decoration.set(list.map((g) => Decoration.widget({ widget: new GenWidget(g), block: true, side: 4 }).range(Math.min(g.pos, state.doc.length))), true);
    }),
  ];
}

/** Asks the image row to redraw, for example after the connection changes. */
export function refreshImageActions(view) {
  view.dispatch({ effects: refreshImages.of(null) });
}

/**
 * The image actions of the phone keyboard bar for the selected image.
 * @returns {{id: string, label: string, icon: string, disabled: boolean, run: () => void}[]}
 */
export function imageKbarActions(view) {
  const raw = selectedImage(view.state);
  if (!raw) return [];
  if (!caps) loadCaps(view);
  const img = imageInfo(raw);
  const { actions } = availability(img);
  return actions.map((a) => ({ ...a, run: () => runImageAction(view, a.id, raw, {}) }));
}

/** Runs one image action. */
export async function runImageAction(view, id, raw, { nameEl = null, button: trigger = null } = {}) {
  const action = IMAGE_ACTIONS.find((a) => a.id === id);
  if (action && action.ai && blockedOffline(cfg.isOnline, trigger)) return;
  const img = imageInfo(raw);
  const src = cfg.imageSrc ? await cfg.imageSrc(img.url) : img.url;
  const withSrc = { ...img, src };
  const lineEnd = () => view.state.doc.lineAt(Math.min(img.to, view.state.doc.length)).to;
  switch (id) {
    case "describe":
      openAskBar(view, {
        from: img.to, to: img.to, image: withSrc, run: true, text: img.alt || img.name, title: "Describe",
        action: { id: "describe", label: "Describe", task: "describe", output: "answer" },
        extraActions: cfg.canEdit ? [
          { label: "Use as alt text", primary: true, run: (text) => proposeAlt(view, img, firstSentence(text)) },
          { label: "Insert as caption", run: (text, bar) => { insertCaption(view, img, text); bar.close({ keepResult: true }); } },
        ] : [],
      });
      return;
    case "alt": {
      const range = altRange(view, img);
      if (!range) return;
      openAskBar(view, {
        from: img.to, to: img.to, image: withSrc, run: true, title: "Alt text", target: range, text: img.alt || img.name,
        action: { id: "alt", label: "Alt text", task: "describe", output: "replace", instruction: "Write alt text for the image: one plain sentence of at most 125 characters, with no leading phrase such as Image of, and no quotes or brackets." },
      });
      return;
    }
    case "extract":
      openAskBar(view, {
        from: img.to, to: img.to, image: withSrc, run: true, title: "Extract text", target: { from: lineEnd(), to: lineEnd() }, insertPrefix: "\n\n", text: img.name,
        action: { id: "extract", label: "Extract text", task: "extract", output: "insert" },
      });
      return;
    case "notes":
      openAskBar(view, {
        from: img.to, to: img.to, image: withSrc, run: true, title: "Turn into notes", target: { from: lineEnd(), to: lineEnd() }, insertPrefix: "\n\n", text: img.name,
        action: { id: "notes", label: "Turn into notes", task: "custom", route: "vision", output: "insert", instruction: "Turn the whiteboard, slide, chart or diagram in the image into notes: markdown headings and lists that keep its structure. Return only the notes." },
      });
      return;
    case "ask":
      openAskBar(view, { from: img.to, to: img.to, image: withSrc });
      return;
    case "variations": {
      let prompt = String(img.alt || "").trim();
      if (!prompt) {
        if (trigger) setBusy(trigger, true, { message: "Describing the image for variations" });
        else announce("Describing the image for variations");
        try {
          const note = cfg.note();
          const res = await cfg.api("POST", "/api/ai/run", { task: "describe", noteId: note.id, spaceId: note.spaceId, text: img.name, output: "answer", feature: "image", context: [{ kind: "file", fileId: img.fileId }] });
          prompt = String((res && res.text) || "").trim();
        } catch (err) {
          notify("Variations failed: " + ((err && err.message) || "the image could not be described"), { variant: "critical" });
          return;
        } finally {
          if (trigger && trigger.isConnected) setBusy(trigger, false);
        }
      }
      if (prompt) startImageGeneration(view, { pos: lineEnd(), prompt, n: 4, title: "Variations" });
      return;
    }
    case "edit":
      openImageEditor({
        src, fileId: img.fileId, name: img.name, note: cfg.note(), api: cfg.api, fetch: cfg.fetch, isOnline: cfg.isOnline,
        onSaved: ({ mode, file }) => {
          if (mode === "copy" && file) replaceImageUrl(view, img, fileHref(file));
        },
      });
      return;
    case "open":
      if (src) window.open(src, "_blank", "noopener");
      return;
    case "copy":
      copyToClipboard(new URL(img.url, location.href).href, trigger);
      return;
    case "rename":
      if (!cfg.isOnline || cfg.isOnline()) {
        if (!nameEl) {
          notify("Select the image on a larger screen to rename its file");
          return;
        }
        editInPlace(nameEl, {
          value: img.name, label: "File name",
          onSave: async (name) => {
            await cfg.api("PATCH", "/api/files/" + encodeURIComponent(img.fileId), { name });
            announce("File renamed to " + name);
          },
        });
      } else {
        notify("Renaming a file needs a connection", { variant: "warning" });
      }
      return;
    default:
  }
}

function firstSentence(text) {
  const t = String(text || "").replace(/\s+/g, " ").trim();
  const m = /^(.{20,160}?[.!?])(\s|$)/.exec(t);
  return (m ? m[1] : t.slice(0, 160)).replace(/[[\]]/g, "");
}

function altRange(view, img) {
  const doc = view.state.doc;
  const found = parseImageSyntax(doc.sliceString(img.from, Math.min(doc.length, img.from + 4000)), img.from);
  if (!found) return null;
  return { from: img.from + 2, to: img.from + 2 + found.alt.length };
}

function proposeAlt(view, img, alt) {
  const range = altRange(view, img);
  if (!range || !alt) return;
  startChange(view, { title: "Alt text", author: "AI", from: range.from, to: range.to, text: alt.replace(/[[\]\n]/g, " ").trim(), status: "open" });
}

function insertCaption(view, img, text) {
  const line = view.state.doc.lineAt(Math.min(img.to, view.state.doc.length));
  const caption = firstSentence(text);
  startChange(view, { title: "Caption", author: "AI", from: line.to, to: line.to, text: "\n\n*" + caption + "*", status: "open" });
}

function fileHref(file) {
  return String(file.url || file.path || file.name || "").replace(/ /g, "%20").replace(/\(/g, "%28").replace(/\)/g, "%29");
}

function replaceImageUrl(view, img, url) {
  const doc = view.state.doc;
  const found = parseImageSyntax(doc.sliceString(img.from, Math.min(doc.length, img.from + 4000)), img.from);
  if (!found || !url) return;
  const at = doc.sliceString(img.from, found.to).lastIndexOf("(" + found.url) + 1;
  if (at <= 0) return;
  view.dispatch({ changes: { from: img.from + at, to: img.from + at + found.url.length, insert: url }, userEvent: "input.ai" });
}

/* ---------- Generation placeholder ---------- */

const genEffect = StateEffect.define();
let genSeq = 0;
const genAborts = new Map();

/** Image generations in progress or waiting for a choice, each at a position. */
const genField = StateField.define({
  create: () => [],
  update(list, tr) {
    if (tr.docChanged && list.length) list = list.map((g) => ({ ...g, pos: tr.changes.mapPos(g.pos, 1) }));
    for (const e of tr.effects) {
      if (!e.is(genEffect)) continue;
      const { op, entry } = e.value;
      if (op === "add") list = [...list, entry];
      else if (op === "update") list = list.map((g) => (g.id === entry.id ? { ...g, ...entry } : g));
      else if (op === "remove") list = list.filter((g) => g.id !== entry.id);
    }
    return list;
  },
});

/** Lists the generation placeholders of a state. */
export function listGenerations(state) {
  return state.field(genField, false) || [];
}

class GenWidget extends WidgetType {
  constructor(entry) {
    super();
    this.entry = entry;
    this.key = [entry.id, entry.status, entry.done, entry.total, entry.queue, entry.results.length, entry.error].join("|");
  }
  eq(o) { return o.key === this.key; }
  toDOM(view) { return h("div", { class: "cm-ms-block image-gen-block" }, buildGen(view, this.entry)); }
  ignoreEvent() { return true; }
}

function genButton(label, variant, run) {
  const b = button({ label, variant, size: "small", className: "image-gen-action" });
  b.addEventListener("mousedown", (ev) => ev.preventDefault());
  b.addEventListener("click", (ev) => { ev.preventDefault(); run(); });
  return b;
}

function buildGen(view, g) {
  const head = h("div", { class: "change-header image-gen-header" },
    h("span", { class: "change-title" }, h("span", { class: "change-author", text: "AI" }), h("span", { text: " · " + (g.title || "Generate image") })),
    h("span", { class: "change-state", text: "“" + truncate(g.prompt, 60) + "”" }));
  const card = h("div", { class: "image-gen", role: "group", "aria-label": g.title || "Generate image", dataset: { status: g.status } }, head);
  if (g.status === "running") {
    const pct = g.total ? Math.round((100 * g.done) / g.total) : 0;
    const text = g.queue > 0 ? `Waiting in queue, position ${g.queue}` : g.total > 1 ? `Generating ${Math.min(g.done + 1, g.total)} of ${g.total}` : "Generating";
    card.append(
      h("div", { class: "image-gen-progress" },
        h("span", { class: "image-gen-text", text, "aria-live": "polite" }),
        progressBar({ value: Math.max(4, pct), label: "Generation progress", valueText: text })),
      h("div", { class: "image-gen-actions" }, genButton("Stop", "default", () => stopGeneration(view, g.id))));
  } else if (g.status === "error") {
    card.append(inlineMessage({ variant: "critical", text: g.error || "The images could not be generated" }),
      h("div", { class: "image-gen-actions" }, genButton("Try again", "primary", () => { removeGen(view, g.id); startImageGeneration(view, g.spec); }), genButton("Dismiss", "default", () => removeGen(view, g.id))));
  } else {
    const grid = h("div", { class: "image-gen-grid" + (g.results.length > 1 ? " is-multi" : "") });
    g.results.forEach((r, i) => {
      grid.appendChild(h("figure", { class: "image-gen-result" },
        h("img", { src: r.src, alt: "Generated image " + (i + 1) + ": " + g.prompt }),
        h("figcaption", {}, genButton(g.results.length > 1 ? "Accept " + (i + 1) : "Accept", "default", () => acceptGenerated(view, g, i)))));
    });
    card.append(grid, h("div", { class: "image-gen-actions" }, genButton(g.results.length > 1 ? "Discard all" : "Discard", "default", () => discardGenerated(view, g))));
  }
  return card;
}

function updateGen(view, entry) {
  if (!view.destroyed) view.dispatch({ effects: genEffect.of({ op: "update", entry }) });
}

function removeGen(view, id) {
  if (!view.destroyed) view.dispatch({ effects: genEffect.of({ op: "remove", entry: { id } }) });
}

function stopGeneration(view, id) {
  const abort = genAborts.get(id);
  if (abort) abort.abort();
  removeGen(view, id);
}

function altFromPrompt(prompt) {
  return truncate(String(prompt || "").replace(/[[\]\n]/g, " "), 120);
}

function acceptGenerated(view, g, index) {
  const current = listGenerations(view.state).find((x) => x.id === g.id) || g;
  const r = current.results[index];
  if (!r) return;
  const doc = view.state.doc;
  const pos = Math.min(current.pos, doc.length);
  const line = doc.lineAt(pos);
  const lead = line.text.trim() ? "\n\n" : "";
  const insert = lead + "![" + altFromPrompt(current.prompt) + "](" + r.href + ")";
  view.dispatch({
    changes: { from: pos, insert },
    effects: genEffect.of({ op: "remove", entry: { id: g.id } }),
    selection: { anchor: pos + insert.length },
    userEvent: "input.ai",
  });
  trashFiles(current.results.filter((_x, i) => i !== index));
  announce("Image inserted");
}

function discardGenerated(view, g) {
  removeGen(view, g.id);
  trashFiles(g.results);
}

function trashFiles(results) {
  for (const r of results) {
    if (r.file && r.file.id) cfg.api("POST", "/api/items/" + encodeURIComponent(r.file.id) + "/trash").catch(() => {});
  }
}

/**
 * Generates images into a placeholder at pos. The results are saved as new
 * files, or come back as data URLs when the server cannot save them.
 * @param {EditorView} view
 * @param {{pos: number, prompt: string, size?: string, n?: number, title?: string, model?: object|null}} spec
 */
export async function startImageGeneration(view, spec) {
  if (blockedOffline(cfg.isOnline)) return null;
  const id = "gen" + ++genSeq;
  const n = Math.min(4, Math.max(1, spec.n || 1));
  const entry = { id, pos: spec.pos, prompt: spec.prompt, title: spec.title || "Generate image", status: "running", done: 0, total: n, queue: 0, results: [], error: "", spec };
  view.dispatch({ effects: genEffect.of({ op: "add", entry }) });
  const abort = new AbortController();
  genAborts.set(id, abort);
  const note = cfg.note();
  const save = cfg.imageSave ? cfg.imageSave() : "new";
  const body = { spaceId: note.spaceId, noteId: note.id, prompt: spec.prompt, n, save };
  if (spec.size) body.size = spec.size;
  if (spec.model && spec.model.providerId) {
    body.providerId = spec.model.providerId;
    body.model = spec.model.model;
  }
  const onEvent = (name, data) => {
    if (name !== "progress") return;
    updateGen(view, { id, done: data.done || 0, total: data.total || n, queue: data.queuePosition || 0 });
  };
  const call = () => streamEvents("/api/ai/image/stream", body, { fetch: cfg.fetch, signal: abort.signal, onEvent });
  try {
    let done;
    try {
      done = await call();
    } catch (err) {
      if (body.save !== "none" && err && [404, 415, 501].includes(err.status)) {
        body.save = "none";
        done = await call();
      } else {
        throw err;
      }
    }
    const results = body.save === "none"
      ? ((done && done.images) || []).map((img) => ({ src: img.dataUrl, href: img.dataUrl }))
      : ((done && done.files) || []).map((f) => ({ src: f.thumb || f.url || "", href: fileHref(f), file: f }));
    if (!results.length) throw new Error("The model returned no images");
    updateGen(view, { id, status: "done", results });
  } catch (err) {
    if (err && err.name === "AbortError") return null;
    updateGen(view, { id, status: "error", error: (err && err.message) || "The images could not be generated" });
  } finally {
    genAborts.delete(id);
  }
  return id;
}

/** Stops every generation, for unmount. */
export function stopAllGenerations() {
  for (const abort of genAborts.values()) abort.abort();
  genAborts.clear();
}

function truncate(s, n) {
  const t = String(s || "").replace(/\s+/g, " ").trim();
  return t.length > n ? t.slice(0, n - 1) + "…" : t;
}

/* ---------- Image editor ---------- */

/**
 * Opens the image editor, a Dialog that fills the screen on narrow viewports,
 * where its close button becomes Back. The brush marks the area to change,
 * Keep unmasked areas sends the mask, and the result shows in a before and
 * after comparison with one Save.
 * @param {{src: string, fileId: string, name: string, note: object, api: Function, fetch?: Function, isOnline?: () => boolean,
 *   onSaved?: (r: {mode: string, file: object}) => void, onClose?: () => void}} opts
 * @returns {{close: () => void, element: HTMLElement}}
 */
export function openImageEditor(opts) {
  const { src, fileId, name, note, api } = opts;
  const fetchImpl = opts.fetch || globalThis.fetch.bind(globalThis);
  const prevFocus = document.activeElement;
  let resultUrl = "";
  let painted = false;
  let keepUnmasked = true;
  let choice = { mode: "version", others: [] };
  let abort = null;
  let closed = false;

  const before = h("img", { class: "image-editor-before", alt: "Original image", src, crossOrigin: "use-credentials" });
  const after = h("img", { class: "image-editor-after", alt: "Edited image", hidden: true });
  const mask = h("canvas", { class: "image-editor-mask", role: "img", "aria-label": "Mask. Paint over the area to change." });
  const frame = h("div", { class: "image-editor-frame" }, before, after, mask);
  const compareId = uid("compare");
  const compare = h("input", { type: "range", id: compareId, class: "image-editor-compare", min: "0", max: "100", value: "50" });
  const compareRow = h("div", { class: "image-editor-compare-row", hidden: true }, h("label", { class: "image-editor-label", for: compareId, text: "Before and after" }), compare);
  const stage = h("div", { class: "image-editor-stage" }, frame);
  const brushId = uid("brush");
  const brush = h("input", { type: "range", id: brushId, class: "image-editor-brush", min: "8", max: "120", value: "40" });
  const clear = button({ label: "Clear mask", size: "small", leadingVisual: "undo" });
  const keepLabelId = uid("keep");
  const keep = toggleSwitch({ checked: true, size: "small", labelledBy: keepLabelId, onChange: (on) => { keepUnmasked = on; } });
  const tools = h("div", { class: "image-editor-tools" },
    h("div", { class: "image-editor-field" }, h("label", { class: "image-editor-label", for: brushId, text: "Brush size" }), brush),
    clear,
    h("div", { class: "image-editor-field" }, h("span", { class: "image-editor-label", id: keepLabelId, text: "Keep unmasked areas" }), keep));
  const promptId = uid("prompt");
  const prompt = h("textarea", { id: promptId, class: "text-input-control image-editor-prompt", rows: "2" });
  const generate = button({ label: "Generate", variant: "primary", className: "image-editor-generate" });
  const promptRow = h("div", { class: "image-editor-prompt-row" },
    h("div", { class: "image-editor-prompt-field" }, h("label", { class: "image-editor-label", for: promptId, text: "Describe the change" }), h("span", { class: "text-input text-input-block" }, prompt)),
    generate);
  const progress = h("p", { class: "image-editor-progress", hidden: true }, spinner({ size: "small", label: "" }), h("span", { class: "image-editor-progress-text" }));
  const progressText = progress.querySelector(".image-editor-progress-text");
  const saveText = h("span", { class: "image-editor-save-text" });
  const switchMode = button({ label: "Save a copy instead", variant: "link", className: "image-editor-link" });
  const save = button({ label: "Save", variant: "primary", className: "image-editor-save" });
  const saveRow = h("div", { class: "image-editor-save-row", hidden: true }, h("p", { class: "image-editor-save-line" }, saveText, " ", switchMode), save);
  const content = h("div", { class: "image-editor-body" }, stage, compareRow, tools, promptRow, progress, saveRow);

  const setProgress = (text, busy = true) => {
    progress.hidden = !text;
    progressText.textContent = text || "";
    const spin = progress.querySelector(".spinner");
    if (spin) spin.hidden = !busy;
  };

  /* ----- Mask painting ----- */

  const ctx2d = () => mask.getContext("2d");
  function sizeMask() {
    const r = before.getBoundingClientRect();
    const dpr = window.devicePixelRatio || 1;
    if (!r.width || !r.height) return;
    const old = painted ? mask.toDataURL() : "";
    mask.width = Math.round(r.width * dpr);
    mask.height = Math.round(r.height * dpr);
    mask.style.width = r.width + "px";
    mask.style.height = r.height + "px";
    if (old) {
      const img = new Image();
      img.onload = () => ctx2d().drawImage(img, 0, 0, mask.width, mask.height);
      img.src = old;
    }
  }
  let drawing = false;
  let last = null;
  const point = (ev) => {
    const r = mask.getBoundingClientRect();
    const sx = mask.width / r.width;
    return { x: (ev.clientX - r.left) * sx, y: (ev.clientY - r.top) * sx };
  };
  const stroke = (p) => {
    const c = ctx2d();
    const w = Number(brush.value) * (mask.width / mask.getBoundingClientRect().width || 1);
    // The brush colour is the fuchsia accent at 55 percent, readable over any image.
    c.strokeStyle = "rgba(192, 38, 211, 0.55)";
    c.fillStyle = "rgba(192, 38, 211, 0.55)";
    c.lineCap = "round";
    c.lineJoin = "round";
    c.lineWidth = w;
    c.globalCompositeOperation = "source-over";
    if (last) {
      c.beginPath();
      c.moveTo(last.x, last.y);
      c.lineTo(p.x, p.y);
      c.stroke();
    } else {
      c.beginPath();
      c.arc(p.x, p.y, w / 2, 0, Math.PI * 2);
      c.fill();
    }
    last = p;
    painted = true;
  };
  mask.addEventListener("pointerdown", (ev) => {
    if (!mask.width) sizeMask();
    drawing = true;
    last = null;
    mask.setPointerCapture(ev.pointerId);
    stroke(point(ev));
    ev.preventDefault();
  });
  mask.addEventListener("pointermove", (ev) => { if (drawing) stroke(point(ev)); });
  const end = () => { drawing = false; last = null; };
  mask.addEventListener("pointerup", end);
  mask.addEventListener("pointercancel", end);
  clear.addEventListener("click", () => {
    ctx2d().clearRect(0, 0, mask.width, mask.height);
    painted = false;
  });
  before.addEventListener("load", sizeMask);
  const onResize = () => sizeMask();
  window.addEventListener("resize", onResize);

  /* ----- Compare ----- */

  compare.addEventListener("input", () => {
    after.style.clipPath = `inset(0 0 0 ${compare.value}%)`;
  });

  function setPrimary(el, on) {
    el.classList.toggle("btn-primary", on);
  }

  function showResult(url) {
    resultUrl = url;
    after.src = url;
    after.hidden = false;
    mask.hidden = true;
    compareRow.hidden = false;
    compare.value = "50";
    after.style.clipPath = "inset(0 0 0 50%)";
    tools.hidden = true;
    saveRow.hidden = false;
    generate.querySelector(".btn-label").textContent = "Try again";
    // Save is the one primary action once a result is shown.
    setPrimary(generate, false);
    drawSave();
  }

  function editMask() {
    after.hidden = true;
    mask.hidden = false;
    compareRow.hidden = true;
    tools.hidden = false;
    saveRow.hidden = true;
    generate.querySelector(".btn-label").textContent = "Generate";
  }

  function drawSave() {
    saveText.textContent = saveLineText(choice, name);
    switchMode.querySelector(".btn-label").textContent = choice.mode === "copy" ? "Save a new version instead" : "Save a copy instead";
  }
  switchMode.addEventListener("click", () => {
    choice = { ...choice, mode: choice.mode === "copy" ? "version" : "copy" };
    drawSave();
  });

  (async () => {
    try {
      const data = await api("GET", "/api/files/" + encodeURIComponent(fileId) + "/backlinks");
      choice = saveChoice((data && data.items) || [], note.id);
      drawSave();
    } catch { /* keep the version default */ }
  })();

  /* ----- Dialog ----- */

  const dialog = openDialog({
    title: "Edit image",
    subtitle: name,
    width: "xlarge",
    height: "large",
    content,
    className: "image-editor",
    returnFocus: prevFocus && prevFocus.isConnected ? prevFocus : null,
    onClose: () => {
      closed = true;
      if (abort) abort.abort();
      window.removeEventListener("resize", onResize);
      if (window.visualViewport) window.visualViewport.removeEventListener("resize", fitViewport);
      if (opts.onClose) opts.onClose();
    },
  });
  const close = () => dialog.close();
  // The on-screen keyboard shrinks only the visual viewport, so the dialog follows it and its body scrolls.
  const fitViewport = () => {
    const vv = window.visualViewport;
    if (!vv || closed) return;
    const open = vv.height < window.innerHeight - 1;
    const limit = Math.round(vv.height - (isNarrow() ? 0 : 16));
    const style = dialog.element.style;
    const next = open ? limit + "px" : "";
    if (style.maxHeight !== next) style.maxHeight = next;
    // A centred dialog would stay centred in the layout viewport, so it moves to the top of the visual viewport.
    const top = open ? (isNarrow() ? "0px" : "8px") : "";
    if (style.marginTop !== top) {
      style.marginTop = top;
      style.marginBottom = open ? "auto" : "";
    }
    if (isNarrow()) {
      const height = open ? limit + "px" : "";
      if (style.height !== height) style.height = height;
    }
  };
  if (window.visualViewport) window.visualViewport.addEventListener("resize", fitViewport);
  fitViewport();

  /* ----- Generate and save ----- */

  generate.addEventListener("click", async () => {
    if (blockedOffline(opts.isOnline, generate)) return;
    if (abort) {
      abort.abort();
      return;
    }
    const text = prompt.value.trim();
    if (!text) {
      prompt.focus();
      return;
    }
    const label = generate.querySelector(".btn-label");
    if (!resultUrl || label.textContent === "Generate") editMask();
    setPrimary(generate, true);
    const body = { spaceId: note.spaceId, noteId: note.id, prompt: text, fileId, n: 1, save: "none" };
    if (keepUnmasked && painted && before.naturalWidth) {
      body.mask = maskPNG(mask, before.naturalWidth, before.naturalHeight).base64;
    }
    abort = new AbortController();
    label.textContent = "Stop";
    setProgress("Editing the image");
    try {
      const done = await streamEvents("/api/ai/image/stream", body, {
        fetch: opts.fetch, signal: abort.signal,
        onEvent: (event, data) => {
          if (event === "progress") setProgress(data.queuePosition > 0 ? `Waiting in queue, position ${data.queuePosition}` : "Editing the image");
        },
      });
      const url = done && done.images && done.images[0] && done.images[0].dataUrl;
      if (!url) throw new Error("The model returned no image");
      setProgress("");
      announce("The edited image is ready");
      showResult(url);
    } catch (err) {
      setProgress("");
      if (err && err.name === "AbortError") announce("Stopped");
      else if (!closed) dialog.showError((err && err.message) || "The image could not be edited");
      label.textContent = resultUrl ? "Try again" : "Generate";
    } finally {
      abort = null;
    }
  });

  save.addEventListener("click", async () => {
    if (!resultUrl || save.getAttribute("data-busy") === "true" || blockedOffline(opts.isOnline, save)) return;
    setBusy(save, true, { message: "Saving the image" });
    try {
      const blob = dataUrlToBlob(resultUrl);
      const ext = blob.type === "image/jpeg" ? "jpg" : blob.type === "image/webp" ? "webp" : "png";
      const meta = await api("GET", "/api/files/" + encodeURIComponent(fileId));
      const form = new FormData();
      let res;
      if (choice.mode === "version") {
        form.append("baseSha256", (meta && meta.sha256) || "");
        form.append("file", blob, String(meta.name || name).replace(/\.[^.]+$/, "") + "." + ext);
        res = await fetchImpl("/api/files/" + encodeURIComponent(fileId) + "/content", { method: "PUT", body: form, credentials: "same-origin" });
      } else {
        form.append("spaceId", meta.spaceId || note.spaceId);
        form.append("parentId", meta.parentId || "");
        form.append("onConflict", "rename");
        form.append("pasteFor", note.id);
        form.append("file", blob, String(meta.name || name).replace(/\.[^.]+$/, "") + " edited." + ext);
        res = await fetchImpl("/api/files", { method: "POST", body: form, credentials: "same-origin" });
      }
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error || "The image could not be saved");
      if (opts.onSaved) opts.onSaved({ mode: choice.mode, file: data });
      setBusy(save, false);
      close();
      announce(choice.mode === "version" ? "Saved a new version" : "Saved a copy");
    } catch (err) {
      setBusy(save, false);
      dialog.showError((err && err.message) || "The image could not be saved");
    }
  });

  requestAnimationFrame(() => sizeMask());
  return { close, element: dialog.element };
}

/** The message used when an image action is tapped offline. */
export const IMAGE_OFFLINE_MESSAGE = AI_OFFLINE_MESSAGE;
