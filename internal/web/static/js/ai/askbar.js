/**
 * The ask bar: a block widget in the flow of the note that opens under the
 * selection, the cursor line or an image. It holds a text field with @
 * mentions, suggested actions read from the syntax tree, context chips with a
 * token estimate, an output mode switch and a model chip. Replace and insert
 * results stream into tracked changes, and answers stream into a card under
 * the bar. On a phone the bar docks above the keyboard.
 *
 * The action table, the context classifier and the request builder at the top
 * are pure and have node tests.
 */

import { StateField, StateEffect, EditorView, Decoration, WidgetType, ViewPlugin, keymap, Prec, syntaxTree } from "/static/vendor/codemirror.js";
import { h, icon, button, iconButton, segmentedControl, inlineMessage } from "/static/js/ui.js";
import {
  selectionChip, noteChip, sectionChip, sectionText, urlChip, refsFromChips, streamEvents, renderAnswer, sourceChips,
  copyToClipboard, blockedOffline, aiIcon, aiCapabilities,
} from "/static/js/ai/common.js";
import { createComposer } from "/static/js/ai/composer.js";
import { startChange, streamChange, getChange, discardHunks, acceptHunks, allHunks } from "/static/js/ai/tracked.js";

/* ---------- Actions ---------- */

/** Every suggested action. output is the default output mode. */
export const ACTIONS = {
  improve: { label: "Improve", task: "improve", output: "replace" },
  shorten: { label: "Shorten", task: "shorten", output: "replace" },
  grammar: { label: "Fix spelling and grammar", task: "grammar", output: "replace" },
  tone: { label: "Change tone", task: "custom", sub: "tone", output: "replace" },
  translate: { label: "Translate", task: "translate", sub: "language", output: "replace" },
  explain: { label: "Explain", task: "explain", output: "answer" },
  draft: { label: "Draft this section", task: "custom", output: "insert", instruction: "Draft the body of the section under the heading, in the style and language of the note. Do not repeat the heading." },
  outline: { label: "Outline", task: "outline", output: "insert" },
  continue: { label: "Continue", task: "continue", output: "insert" },
  summarizeAbove: { label: "Summarize above", task: "summarize", output: "answer" },
  sort: { label: "Sort", task: "custom", output: "replace", instruction: "Sort the list items in a sensible order. Keep every item and the list syntax." },
  group: { label: "Group", task: "custom", output: "replace", instruction: "Group the list items under short labels as a nested list. Keep every item." },
  toTable: { label: "Turn into table", task: "custom", output: "replace", instruction: "Turn the list into a markdown table with suitable columns. Keep every item." },
  addColumn: { label: "Add column", task: "custom", output: "replace", scope: "block", instruction: "Add one useful column to the markdown table and fill it. Return the whole table." },
  fillCells: { label: "Fill empty cells", task: "custom", output: "replace", scope: "block", instruction: "Fill the empty cells of the markdown table from the context. Return the whole table." },
  summarizeTable: { label: "Summarize table", task: "summarize", output: "answer", scope: "block" },
  sortColumn: { label: "Sort by column", task: "custom", output: "replace", scope: "block", instruction: "Sort the rows of the markdown table by the first column, or by the column the request names. Return the whole table." },
  explainCode: { label: "Explain code", task: "explain", output: "answer", scope: "block" },
  addComments: { label: "Add comments", task: "custom", output: "replace", scope: "block", instruction: "Add brief comments to the code. Return the whole code block with its fence." },
  findBugs: { label: "Find bugs", task: "custom", output: "answer", scope: "block", instruction: "Find bugs in the code. For each, say what is wrong and how to fix it." },
  summarizePage: { label: "Summarize page", task: "summarize", output: "answer", ref: "url" },
  quotePoints: { label: "Quote key points", task: "custom", output: "insert", ref: "url", instruction: "Quote the key points of the web page as a markdown list of short quotations, each with the page title." },
  summarizeLinked: { label: "Summarize linked note", task: "summarize", output: "answer", ref: "wikilink" },
  compare: { label: "Compare with this note", task: "custom", output: "answer", ref: "wikilink", instruction: "Compare the linked note with this note. List what they share and where they differ." },
};

/** The suggested actions for each syntax context. Images use the image actions. */
export const SUGGESTIONS = {
  prose: ["improve", "shorten", "grammar", "tone", "translate", "explain"],
  heading: ["draft", "outline", "continue"],
  paragraph: ["continue", "summarizeAbove"],
  list: ["sort", "group", "toTable"],
  table: ["addColumn", "fillCells", "summarizeTable", "sortColumn"],
  code: ["explainCode", "addComments", "findBugs"],
  link: ["summarizePage", "quotePoints"],
  wikilink: ["summarizeLinked", "compare"],
  image: [],
};

/** The choices of Change tone. */
export const TONES = ["Formal", "Friendly", "Concise", "Confident", "Casual"];

/** The languages offered before the user has translated anything. */
export const DEFAULT_LANGUAGES = ["English", "German", "French", "Spanish", "Japanese"];

const LIST_LINE = /^\s*(?:[-*+]|\d{1,9}[.)])\s+/;
const HEADING_LINE = /^\s{0,3}#{1,6}\s+\S/;

/** Lists the suggested actions of a context, each with its id. */
export function suggestedActions(ctx) {
  return (SUGGESTIONS[ctx && ctx.kind] || []).map((id) => ({ id, ...ACTIONS[id] }));
}

function lineMatch(lineText, col, re, lineFrom) {
  re.lastIndex = 0;
  let m;
  while ((m = re.exec(lineText))) {
    if (col >= m.index && col <= m.index + m[0].length) return { from: lineFrom + m.index, to: lineFrom + m.index + m[0].length, text: m[0] };
  }
  return null;
}

/** Parses ![alt](url) at the start of text. */
export function parseImageSyntax(text, from = 0) {
  const m = /^!\[([^\]\n]*)\]\(\s*(?:<([^>\n]*)>|([^)\s]*))(?:\s+"[^"\n]*")?\s*\)/.exec(String(text || ""));
  return m ? { from, to: from + m[0].length, alt: m[1], url: m[2] !== undefined ? m[2] : m[3] } : null;
}

/**
 * Classifies the cursor or selection from the syntax node chain around it,
 * innermost first, and the text of the line.
 * @param {{chain?: {name: string, from: number, to: number, text?: string}[], selected?: boolean, selectionText?: string,
 *   lineText?: string, prevText?: string, pos?: number, lineFrom?: number}} input
 * @returns {{kind: string, block?: {from: number, to: number}, url?: string, target?: string, image?: object}}
 */
export function classifyContext({ chain = [], selected = false, selectionText = "", lineText = "", prevText = "", pos = 0, lineFrom = 0 } = {}) {
  const find = (...names) => chain.find((n) => names.includes(n.name));
  const code = find("FencedCode", "CodeBlock");
  if (code) return { kind: "code", block: { from: code.from, to: code.to } };
  const table = find("Table");
  if (table) return { kind: "table", block: { from: table.from, to: table.to } };
  if (!selected) {
    const col = pos - lineFrom;
    const imgNode = find("Image") || lineMatch(lineText, col, /!\[[^\]\n]*\]\([^)\n]*\)/g, lineFrom);
    if (imgNode) {
      const image = parseImageSyntax(imgNode.text || "", imgNode.from);
      if (image) return { kind: "image", image };
    }
    const wiki = find("WikiLink") || lineMatch(lineText, col, /\[\[[^[\]\n]+\]\]/g, lineFrom);
    if (wiki && wiki.text) {
      const inner = wiki.text.replace(/^\[\[|\]\]$/g, "");
      const target = inner.split("|")[0].split("#")[0].trim();
      if (target) return { kind: "wikilink", target };
    }
    const link = find("Link", "URL") || lineMatch(lineText, col, /\[[^\]\n]*\]\(\s*<?https?:\/\/[^)\n]*\)/g, lineFrom) || lineMatch(lineText, col, /https?:\/\/[^\s<>()]+/g, lineFrom);
    if (link && link.text) {
      const m = /\]\(\s*<?(https?:\/\/[^)\s>]+)/.exec(link.text) || /^(https?:\/\/\S+)$/.exec(link.text.trim());
      if (m) return { kind: "link", url: m[1] };
    }
  }
  if (selected) {
    const lines = String(selectionText).split("\n").filter((l) => l.trim());
    if (lines.length && lines.every((l) => LIST_LINE.test(l))) return { kind: "list" };
    return { kind: "prose" };
  }
  if (HEADING_LINE.test(lineText)) return { kind: "heading" };
  if (!String(lineText).trim()) {
    const prev = String(prevText).split("\n").reverse().find((l) => l.trim()) || "";
    if (HEADING_LINE.test(prev)) return { kind: "heading" };
  }
  return { kind: "paragraph" };
}

/**
 * Reads the syntax tree around [from, to] and classifies it.
 * @param {import("@codemirror/state").EditorState} state
 */
export function detectContext(state, from, to = from) {
  const doc = state.doc;
  const selected = to > from;
  const tree = syntaxTree(state);
  const chain = [];
  const seen = new Set();
  for (const side of selected ? [1] : [-1, 1]) {
    for (let n = tree.resolveInner(from, side); n; n = n.parent) {
      const key = n.name + ":" + n.from + ":" + n.to;
      if (seen.has(key)) continue;
      seen.add(key);
      const entry = { name: n.name, from: n.from, to: n.to };
      if (n.name === "Image" || n.name === "WikiLink" || n.name === "Link" || n.name === "URL") entry.text = doc.sliceString(n.from, n.to);
      chain.push(entry);
    }
  }
  // The parser works in time slices, so a long note may not be parsed as far
  // as the cursor yet. Fenced code and tables are then found in the text.
  if (!chain.some((n) => n.name === "FencedCode" || n.name === "CodeBlock" || n.name === "Table")) {
    const block = textBlockAt(doc.toString(), from);
    if (block) chain.push(block);
  }
  const line = doc.lineAt(from);
  return classifyContext({
    chain, selected, selectionText: doc.sliceString(from, to), lineText: line.text,
    prevText: doc.sliceString(Math.max(0, line.from - 2000), line.from), pos: from, lineFrom: line.from,
  });
}

/**
 * Finds the fenced code block or the pipe table around pos in plain text.
 * @returns {{name: "FencedCode"|"Table", from: number, to: number}|null}
 */
export function textBlockAt(text, pos) {
  const lines = String(text).split("\n");
  let offset = 0;
  let fence = null;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const start = offset;
    const end = offset + line.length;
    const mark = /^\s{0,3}(`{3,}|~{3,})/.exec(line);
    if (fence) {
      if (mark && mark[1][0] === fence.char && mark[1].length >= fence.len) {
        if (pos >= fence.from && pos <= end) return { name: "FencedCode", from: fence.from, to: end };
        fence = null;
      }
    } else if (mark) {
      fence = { char: mark[1][0], len: mark[1].length, from: start };
    } else if (/^\s*\|/.test(line) && pos >= start && pos <= end) {
      let a = i;
      while (a > 0 && /^\s*\|/.test(lines[a - 1])) a--;
      let b = i;
      while (b + 1 < lines.length && /^\s*\|/.test(lines[b + 1])) b++;
      const block = lines.slice(a, b + 1);
      if (block.length >= 2 && /^\s*\|?\s*:?-{3,}/.test(block[1])) {
        const from = lines.slice(0, a).reduce((n, l) => n + l.length + 1, 0);
        return { name: "Table", from, to: from + block.join("\n").length };
      }
    }
    offset = end + 1;
    if (offset > pos && !fence) return null;
  }
  return fence && pos >= fence.from ? { name: "FencedCode", from: fence.from, to: text.length } : null;
}

/**
 * Chooses the output mode: the action's mode, else answer for a question
 * ending in ?, else replace with a target and insert without one. A replace
 * action without a target inserts.
 */
export function defaultOutput({ action = null, instruction = "", hasTarget = false } = {}) {
  if (action) return action.output === "replace" && !hasTarget ? "insert" : action.output;
  if (String(instruction).trim().endsWith("?")) return "answer";
  return hasTarget ? "replace" : "insert";
}

/**
 * Builds the AIRequest of a run.
 * @param {{action?: object|null, instruction?: string, output: string, chips?: object[], note: {id: string, spaceId: string},
 *   text?: string, history?: object[], model?: object|null, choice?: string}} input
 */
export function buildRequest({ action = null, instruction = "", output, chips = [], note, text = "", history = [], model = null, choice = "" }) {
  const req = { noteId: note.id, spaceId: note.spaceId, output };
  const query = instruction || (action ? action.label : "");
  const refs = refsFromChips(chips, query);
  if (refs.length) req.context = refs;
  const hasImage = chips.some((c) => c.kind === "file" && c.image);
  if (action) {
    req.task = action.task;
    if (action.sub === "tone") req.instruction = `Rewrite the text in a ${String(choice || "neutral").toLowerCase()} tone. Keep the meaning and the markdown.`;
    else if (action.sub === "language") req.language = choice || "English";
    else if (action.instruction) req.instruction = action.instruction;
    if (instruction) req.instruction = req.task === "custom" && req.instruction ? req.instruction + "\n\n" + instruction : instruction;
  } else if (output === "answer") {
    req.task = "ask";
    req.question = instruction;
  } else {
    req.task = "custom";
    req.instruction = instruction;
  }
  if (text) req.text = text;
  if (history && history.length) req.history = history;
  if (model && model.providerId) {
    req.providerId = model.providerId;
    req.model = model.model;
  }
  req.feature = output === "answer" ? "ask" : "rewrite";
  if (action && action.route) req.route = action.route;
  else if (hasImage) req.route = "vision";
  return req;
}

/**
 * Cleans a result for the note: drops a code fence around the whole answer
 * unless the old text was fenced, and keeps the trailing line break of the
 * old text.
 */
export function cleanResult(result, oldText = "") {
  let out = String(result || "");
  const fenced = /^\s*```[^\n]*\n([\s\S]*?)\n```\s*$/.exec(out);
  if (fenced && !/^\s*```/.test(oldText)) out = fenced[1];
  if (!/\s$/.test(oldText)) out = out.replace(/\s+$/, "");
  else if (/\n$/.test(oldText) && !/\n$/.test(out)) out += "\n";
  return out;
}

/** The heading above pos, or the empty string. */
export function headingAbove(text, pos) {
  const lines = String(text).slice(0, pos).split("\n").reverse();
  for (const line of lines) {
    const m = /^\s{0,3}#{1,6}\s+(.*?)\s*#*\s*$/.exec(line);
    if (m) return m[1].replace(/[*_`]/g, "").trim();
  }
  return "";
}

function recentLanguages() {
  try {
    const list = JSON.parse(localStorage.getItem("markspace.ai.languages") || "[]");
    if (Array.isArray(list) && list.length) return [...new Set([...list, ...DEFAULT_LANGUAGES])].slice(0, 5);
  } catch { /* storage unavailable */ }
  return DEFAULT_LANGUAGES;
}

function rememberLanguage(lang) {
  try {
    const list = [lang, ...recentLanguages().filter((x) => x !== lang)].slice(0, 5);
    localStorage.setItem("markspace.ai.languages", JSON.stringify(list));
  } catch { /* storage unavailable */ }
}

/* ---------- Editor state ---------- */

const setBar = StateEffect.define();

/** The open bar: {id, pos, docked} or null. pos maps through changes. */
export const askBarField = StateField.define({
  create: () => null,
  update(value, tr) {
    if (value && tr.docChanged) value = { ...value, pos: tr.changes.mapPos(value.pos, 1) };
    for (const e of tr.effects) if (e.is(setBar)) value = e.value;
    return value;
  },
});

const IS_MAC = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent || "");
const controllers = new Map();
let barSeq = 0;
let config = {};

class BarWidget extends WidgetType {
  constructor(id) { super(); this.id = id; }
  eq(o) { return o.id === this.id; }
  // Block widgets keep their vertical space as padding, which CodeMirror measures.
  toDOM() {
    const c = controllers.get(this.id);
    return h("div", { class: "cm-ms-block ask-block" }, c ? c.root : null);
  }
  ignoreEvent() { return true; }
  get estimatedHeight() { return 132; }
}

/** The spark button in the left margin of the cursor line. */
class SparkWidget extends WidgetType {
  constructor(offline) { super(); this.offline = offline; }
  eq(o) { return o.offline === this.offline; }
  toDOM(view) {
    const btn = iconButton({ icon: "sparkle", label: "Ask AI", keybindingHint: IS_MAC ? "⌘J" : "Ctrl+J", size: "small", className: "ask-spark" });
    // Offline the spark keeps the inactive style, and a tap explains why.
    if (this.offline) {
      btn.setAttribute("aria-disabled", "true");
      btn.setAttribute("data-inactive", "true");
    }
    btn.addEventListener("mousedown", (ev) => ev.preventDefault());
    btn.addEventListener("click", (ev) => {
      ev.preventDefault();
      const sel = view.state.selection.main;
      openAskBar(view, { from: sel.from, to: sel.to, anchor: btn });
    });
    return btn;
  }
  ignoreEvent() { return true; }
}

function sparkDecorations(view) {
  const state = view.state;
  if (!config.canEdit || !view.hasFocus || (config.isPhone && config.isPhone()) || (config.enabled && !config.enabled())) return Decoration.none;
  if (state.field(askBarField, false)) return Decoration.none;
  const line = state.doc.lineAt(state.selection.main.head);
  if (allHunks(state).some(({ hunk }) => state.doc.lineAt(hunk.from).from === line.from)) return Decoration.none;
  const offline = !!(config.isOnline && !config.isOnline());
  return Decoration.set([
    Decoration.line({ class: "ask-spark-line" }).range(line.from),
    Decoration.widget({ widget: new SparkWidget(offline), side: -1 }).range(line.from),
  ]);
}

const sparkPlugin = ViewPlugin.fromClass(class {
  constructor(view) { this.decorations = sparkDecorations(view); }
  update(u) {
    if (u.selectionSet || u.focusChanged || u.docChanged || u.startState.field(askBarField, false) !== u.state.field(askBarField, false) || u.transactions.length) {
      this.decorations = sparkDecorations(u.view);
    }
  }
}, { decorations: (v) => v.decorations });

function currentBar(view) {
  const v = view.state.field(askBarField, false);
  return v ? controllers.get(v.id) || null : null;
}

/** Reports whether a bar is open in the editor. */
export function isAskBarOpen(state) {
  return !!state.field(askBarField, false);
}

/** Returns the open bar controller, for the editor wiring and tests. */
export function askBarOf(view) {
  return currentBar(view);
}

/**
 * The ask bar extension.
 * @param {{
 *   note: () => object, api: Function, store?: object, render?: Function, isOnline: () => boolean, isPhone: () => boolean,
 *   canEdit: boolean, dock?: (el: HTMLElement|null) => void, openAITab?: (seed: object) => void,
 *   generateImage?: (view: EditorView, spec: object) => void, resolveWikilink?: (target: string) => Promise<object|null>,
 *   openSource?: (source: object) => void, onChange?: () => void
 * }} cfg
 */
export function askBar(cfg) {
  config = cfg;
  return [
    askBarField,
    EditorView.decorations.compute([askBarField], (state) => {
      const v = state.field(askBarField);
      if (!v || v.docked) return Decoration.none;
      const line = state.doc.lineAt(Math.min(v.pos, state.doc.length));
      return Decoration.set([Decoration.widget({ widget: new BarWidget(v.id), block: true, side: 3 }).range(line.to)]);
    }),
    sparkPlugin,
    Prec.highest(keymap.of([
      { key: "Mod-j", run: (view) => { openAskBarAtSelection(view); return true; } },
      { key: "Escape", run: (view) => { const c = currentBar(view); return c ? c.escapeFromDocument() : false; } },
    ])),
    EditorView.updateListener.of((u) => {
      const c = currentBar(u.view);
      if (c) c.onViewUpdate(u);
    }),
  ];
}

/** Opens the bar under the selection, or under the cursor line. */
export function openAskBarAtSelection(view, extra = {}) {
  const sel = view.state.selection.main;
  return openAskBar(view, { from: sel.from, to: sel.to, ...extra });
}

/**
 * Opens the ask bar. One bar is open at a time, so an open bar closes first.
 * @param {EditorView} view
 * @param {{from: number, to: number, image?: object, action?: string|object, run?: boolean, mode?: "text"|"image",
 *   target?: {from: number, to: number}, title?: string, insertPrefix?: string, extraActions?: object[], text?: string}} opts
 * @returns {object|null} the controller
 */
export function openAskBar(view, opts) {
  if (blockedOffline(config.isOnline, opts && opts.anchor)) return null;
  // A reader without edit access can ask for answers only.
  if (!config.canEdit) opts = { ...opts, answerOnly: true, mode: "text" };
  const old = currentBar(view);
  if (old) old.close({ keepResult: true });
  const c = new AskBarController(view, opts);
  controllers.set(c.id, c);
  const docked = !!(config.isPhone && config.isPhone());
  view.dispatch({ effects: setBar.of({ id: c.id, pos: c.anchor, docked }) });
  if (docked && config.dock) config.dock(c.root);
  c.mounted(docked);
  return c;
}

/** Closes the open bar. */
export function closeAskBar(view) {
  const c = currentBar(view);
  if (!c) return false;
  c.close();
  return true;
}

/* ---------- Controller ---------- */

class AskBarController {
  constructor(view, opts) {
    this.view = view;
    this.opts = opts;
    this.id = "ask" + ++barSeq;
    const doc = view.state.doc;
    this.sel = { from: Math.max(0, Math.min(opts.from, doc.length)), to: Math.max(0, Math.min(opts.to, doc.length)) };
    this.image = opts.image || null;
    this.ctx = this.image ? { kind: "image", image: this.image } : detectContext(view.state, this.sel.from, this.sel.to);
    this.anchor = this.image ? this.image.to : opts.target ? opts.target.to : this.sel.to;
    this.state = "compose";
    this.mode = opts.mode === "image" ? "image" : "text";
    this.output = defaultOutput({ hasTarget: this.hasTarget() });
    this.manualOutput = false;
    this.changeId = null;
    this.abort = null;
    this.lastResult = "";
    this.lastPrompt = "";
    this.lastRun = null;
    this.size = "";
    this.count = 1;
    this.caps = null;
    this.docked = false;
    this.build();
  }

  hasTarget() {
    return !!this.opts.target || this.sel.to > this.sel.from;
  }

  capability() {
    if (this.mode === "image") return "image.generate";
    return this.composer && this.composer.chips().some((c) => c.image) ? "vision" : "text";
  }

  defaultChips() {
    const state = this.view.state;
    const text = state.doc.toString();
    const note = config.note();
    const self = { id: note.id, title: note.title, content: text };
    const chips = [];
    if (this.image && this.image.fileId) {
      chips.push({
        key: "file:" + this.image.fileId, kind: "file", badge: "Image", label: this.image.alt || this.image.name || "Image", image: true,
        chars: 3200, ref: { kind: "file", fileId: this.image.fileId }, src: this.image.src || "",
      });
    }
    if (this.sel.to > this.sel.from) chips.push(selectionChip(state.doc.sliceString(this.sel.from, this.sel.to)));
    const heading = headingAbove(text, this.sel.from);
    if (text.length < 4000 || !heading) chips.push(noteChip(self, "Note"));
    else chips.push(sectionChip(self, heading, sectionText(text, heading)));
    if (this.ctx.kind === "link" && this.ctx.url) chips.push(urlChip(this.ctx.url));
    return chips;
  }

  build() {
    const phone = !!(config.isPhone && config.isPhone());
    this.composer = createComposer({
      api: config.api, store: config.store, note: config.note,
      placeholder: this.placeholder(), label: "Ask AI",
      chips: this.defaultChips(), capability: () => this.capability(),
      onSubmit: (text) => this.submit(text),
      onEscape: () => this.escapeInBar(),
      onTab: () => this.acceptFromBar(),
      onInput: (text) => this.onTyping(text),
      onChipsChange: () => this.drawOutputs(),
    });
    this.suggest = h("div", { class: "ask-actions", role: "group", "aria-label": "Suggested actions" });
    this.outputs = h("div", { class: "ask-outputs" });
    this.genControls = h("div", { class: "image-gen-options", hidden: true });
    this.result = h("div", { class: "answer-card", hidden: true, "aria-live": "polite" });
    this.note = h("div", { class: "ask-bar-note", hidden: true });
    const close = iconButton({ icon: "x", label: "Close", description: "Close the ask bar", keybindingHint: "Esc", size: "small", className: "ask-bar-close" });
    close.addEventListener("mousedown", (ev) => ev.preventDefault());
    close.addEventListener("click", () => this.close());
    this.composer.inputRow.appendChild(close);
    const footer = h("div", { class: "ask-footer" }, this.outputs, this.genControls, h("span", { class: "ask-spacer" }), this.composer.modelChip);
    this.root = h("div", { class: "ask-bar" + (phone ? " is-docked" : ""), role: "group", "aria-label": "Ask AI", dataset: { state: this.state } },
      this.composer.inputRow, this.composer.mentions, this.suggest, this.composer.chipsRow, footer, this.note, this.result);
    this.root.addEventListener("keydown", (ev) => {
      if (ev.key === "Escape" && !ev.defaultPrevented) {
        ev.preventDefault();
        ev.stopPropagation();
        this.escapeInBar();
      }
    });
    this.drawSuggestions();
    this.drawOutputs();
    this.loadCaps();
  }

  async loadCaps() {
    try {
      this.caps = await aiCapabilities(config.api);
    } catch {
      this.caps = null;
    }
    if (!this.closed) this.drawOutputs();
  }

  mounted(docked) {
    this.docked = docked;
    requestAnimationFrame(() => {
      if (this.closed) return;
      this.composer.focus();
      if (!docked) this.root.scrollIntoView({ block: "nearest" });
    });
    const action = typeof this.opts.action === "string" ? { id: this.opts.action, ...ACTIONS[this.opts.action] } : this.opts.action;
    if (this.opts.run && action) this.run(action);
  }

  placeholder() {
    if (this.mode === "image") return "Describe the image to generate";
    if (this.state === "result") return "Refine the result";
    if (this.image) return "Ask about this image";
    return this.hasTarget() ? "Ask AI to change the selection" : "Ask AI to write or answer";
  }

  setState(next) {
    this.state = next;
    this.root.dataset.state = next;
    this.composer.setBusy(next === "running");
    this.composer.setPlaceholder(this.placeholder());
    this.drawSuggestions();
    if (config.onChange) config.onChange();
  }

  /* ----- Suggestions ----- */

  drawSuggestions(sub = null) {
    const typing = this.composer && this.composer.value().trim() !== "";
    this.suggest.replaceChildren();
    const show = this.state === "compose" && !typing && this.mode === "text";
    this.suggest.hidden = !show;
    if (!show) return;
    if (sub) {
      const back = iconButton({ icon: "arrow-left", label: "Back to actions", size: "small", className: "ask-action is-back" });
      back.addEventListener("mousedown", (ev) => ev.preventDefault());
      back.addEventListener("click", () => this.drawSuggestions());
      this.suggest.appendChild(back);
      const choices = sub.sub === "tone" ? TONES : recentLanguages();
      for (const choice of choices) this.suggest.appendChild(this.actionButton(choice, () => this.run(sub, choice)));
      if (sub.sub === "language") {
        const other = h("input", { type: "text", class: "text-input-control", "aria-label": "Other language", enterkeyhint: "go" });
        other.addEventListener("keydown", (ev) => {
          ev.stopPropagation();
          if (ev.key === "Enter" && other.value.trim()) {
            ev.preventDefault();
            this.run(sub, other.value.trim());
          }
          if (ev.key === "Escape") {
            ev.preventDefault();
            this.drawSuggestions();
            this.composer.focus();
          }
        });
        this.suggest.appendChild(h("span", { class: "text-input text-input-sm ask-other" }, other));
      }
      return;
    }
    let actions = suggestedActions(this.ctx);
    if (this.opts.answerOnly) actions = actions.filter((a) => a.output === "answer");
    for (const a of actions) this.suggest.appendChild(this.actionButton(a.label, () => (a.sub ? this.drawSuggestions(a) : this.run(a)), a.id));
    this.suggest.hidden = !actions.length;
  }

  actionButton(label, run, id = "") {
    const btn = button({ label, size: "small", className: "ask-action", attrs: { "data-action": id } });
    btn.addEventListener("mousedown", (ev) => ev.preventDefault());
    btn.addEventListener("click", run);
    return btn;
  }

  /* ----- Output switch and image controls ----- */

  /** The output modes the bar can use now, as [id, label]. */
  outputModes() {
    if (this.opts.answerOnly) return [["answer", "Answer"]];
    const modes = [];
    if (this.hasTarget()) modes.push(["replace", "Replace"]);
    modes.push(["insert", "Insert"], ["answer", "Answer"]);
    // Image mode writes a new image at the cursor, so it is offered without a selection only.
    if (this.caps && this.caps.generate && config.canEdit && !this.image && !this.hasTarget()) modes.push(["image", "Image"]);
    return modes;
  }

  drawOutputs() {
    const modes = this.outputModes();
    if (this.opts.answerOnly) this.output = "answer";
    const current = this.mode === "image" ? "image" : this.output;
    this.outputs.replaceChildren();
    // One mode needs no switch.
    if (modes.length > 1) {
      const control = segmentedControl({
        label: "Output", size: "small",
        items: modes.map(([id, label]) => ({ label, value: id, selected: current === id })),
        onChange: (id) => {
          this.manualOutput = true;
          if (id === "image") this.mode = "image";
          else {
            this.mode = "text";
            this.output = id;
          }
          this.composer.setPlaceholder(this.placeholder());
          this.drawOutputs();
          this.drawSuggestions();
          this.composer.focus();
        },
      });
      for (const b of control.querySelectorAll("button")) b.addEventListener("mousedown", (ev) => ev.preventDefault());
      this.outputs.appendChild(control);
    }
    this.drawGenControls();
  }

  drawGenControls() {
    const on = this.mode === "image";
    this.genControls.hidden = !on;
    if (!on) return;
    const sizes = this.caps && this.caps.sizes && this.caps.sizes.length ? this.caps.sizes : ["1024x1024", "1536x1024", "1024x1536"];
    if (!this.size || !sizes.includes(this.size)) this.size = sizes[0];
    const select = h("select", { class: "text-input-control", "aria-label": "Size" }, ...sizes.map((s) => h("option", { value: s, text: s.replace("x", " × "), selected: s === this.size })));
    select.addEventListener("change", () => { this.size = select.value; });
    const sizeBox = h("span", { class: "text-input text-input-sm select-box image-size" }, select, h("span", { class: "select-caret", "aria-hidden": "true" }, icon("triangle-down", 16)));
    const value = h("span", { class: "image-count-value", text: String(this.count), "aria-live": "polite" });
    const step = (d) => {
      this.count = Math.min(4, Math.max(1, this.count + d));
      value.textContent = String(this.count);
    };
    const minus = iconButton({ icon: "dash", label: "Fewer images", size: "small", onClick: () => step(-1) });
    const plus = iconButton({ icon: "plus", label: "More images", size: "small", onClick: () => step(1) });
    const count = h("span", { class: "image-count", role: "group", "aria-label": "Number of images" }, minus, value, plus);
    this.genControls.replaceChildren(sizeBox, count);
  }

  onTyping(text) {
    if (!this.manualOutput && this.state === "compose" && this.mode === "text") {
      const next = defaultOutput({ instruction: text, hasTarget: this.hasTarget() });
      if (next !== this.output) {
        this.output = this.opts.answerOnly ? "answer" : next;
        this.drawOutputs();
      }
    }
    this.drawSuggestions();
  }

  /* ----- Running ----- */

  submit(text) {
    if (this.state === "running") return;
    if (this.mode === "image") {
      if (!text) { this.composer.focus(); return; }
      if (config.generateImage) config.generateImage(this.view, { pos: this.view.state.doc.lineAt(Math.min(this.anchor, this.view.state.doc.length)).to, prompt: text, size: this.size, n: this.count, model: this.composer.model() });
      this.close();
      return;
    }
    if (this.state === "result" && this.lastRun) {
      if (!text) return;
      this.run(this.lastRun.action, this.lastRun.choice, { refine: text });
      return;
    }
    if (!text) { this.composer.focus(); return; }
    this.run(null, "", { instruction: text });
  }

  /** The range a run replaces, and the text sent with it. */
  targetFor(action) {
    if (this.opts.target) return { ...this.opts.target };
    if (action && action.scope === "block" && this.ctx.block) return { ...this.ctx.block };
    if (this.sel.to > this.sel.from && this.composer.chips().some((c) => c.kind === "selection")) return { ...this.sel };
    return { from: this.sel.to, to: this.sel.to };
  }

  textFor(action, target, output) {
    const doc = this.view.state.doc;
    if (this.opts.text !== undefined) return this.opts.text;
    if (target.to > target.from) return doc.sliceString(target.from, target.to);
    // Without a selection, the text before the cursor carries what Continue,
    // Draft and Summarize above work on. The chips add the rest of the note.
    if (output === "insert" || (action && action.task === "summarize")) return doc.sliceString(Math.max(0, target.from - 6000), target.from);
    return "";
  }

  async run(action, choice = "", { instruction = "", refine = "" } = {}) {
    if (blockedOffline(config.isOnline)) return;
    if (action && action.sub && !choice) {
      this.drawSuggestions(action);
      return;
    }
    if (action && action.sub === "language" && choice) rememberLanguage(choice);
    const output = this.opts.answerOnly ? "answer" : refine && this.lastRun ? this.lastRun.output : this.manualOutput ? this.output : defaultOutput({ action, instruction, hasTarget: this.hasTarget() || !!(action && action.scope === "block" && this.ctx.block) });
    // A refinement works on the range the change covers now: accepted hunks and
    // edits around it have moved the positions of the first run.
    const live = refine && this.changeId ? getChange(this.view.state, this.changeId) : null;
    const target = live ? { from: live.from, to: live.to } : refine && this.lastRun ? this.lastRun.target : this.targetFor(action);
    const chips = this.composer.chips();
    if (action && action.ref === "wikilink" && this.ctx.target && config.resolveWikilink) {
      const linked = await config.resolveWikilink(this.ctx.target);
      if (linked && !chips.some((c) => c.key === "note:" + linked.id)) chips.push(noteChip(linked, "Linked"));
    }
    const history = refine && this.lastRun ? [
      { role: "user", content: this.lastRun.prompt },
      { role: "assistant", content: this.lastResult },
    ] : [];
    const req = buildRequest({
      action, instruction: refine || instruction, output, chips, note: config.note(),
      text: this.textFor(action, target, output), history, model: this.composer.model(), choice,
    });
    const title = this.opts.title || (action ? (choice ? action.label + " · " + choice : action.label) : truncate(instruction || this.lastPrompt, 40));
    this.lastRun = { action, choice, output, target, prompt: refine ? refine : action ? title : instruction, title };
    if (!refine) this.lastPrompt = this.lastRun.prompt;
    this.composer.clear();
    this.manualOutput = this.manualOutput && !refine;
    // The switch shows the mode of the run, such as Answer for Describe.
    if (this.mode === "text" && output !== this.output) {
      this.output = output;
      this.drawOutputs();
    }
    this.note.hidden = true;
    if (output === "answer") return this.runAnswer(req, title);
    return this.runTracked(req, target, title, output);
  }

  async runTracked(req, target, title, output) {
    const view = this.view;
    if (!config.canEdit) return;
    const oldText = view.state.doc.sliceString(target.from, target.to);
    if (!this.changeId || !getChange(view.state, this.changeId)) {
      this.changeId = startChange(view, { title, author: "AI", from: target.from, to: target.to });
      // The selection highlight would cover the hunks, so it collapses to the end of the change.
      if (!view.state.selection.main.empty) view.dispatch({ selection: { anchor: target.to } });
    } else {
      streamChange(view, this.changeId, { text: "", status: "streaming", error: "", title });
    }
    const id = this.changeId;
    this.result.hidden = true;
    this.setState("running");
    const abort = new AbortController();
    this.abort = abort;
    let acc = "";
    let frame = 0;
    const prefix = this.insertPrefix(target, output);
    const push = () => {
      frame = 0;
      if (!this.closed && getChange(view.state, id)) streamChange(view, id, { text: prefix + cleanResult(acc, oldText) });
    };
    try {
      await streamEvents("/api/ai/stream", req, {
        fetch: config.fetch, signal: abort.signal,
        onEvent: (name, data) => {
          if (name === "delta" && data.delta) {
            acc += data.delta;
            if (!frame) frame = requestAnimationFrame(push);
          }
        },
      });
      if (frame) cancelAnimationFrame(frame);
      const text = prefix + cleanResult(acc, oldText);
      this.lastResult = cleanResult(acc, oldText);
      if (getChange(view.state, id)) streamChange(view, id, acc.trim() ? { text, status: "open" } : { status: "error", error: "The model returned nothing" });
    } catch (err) {
      if (frame) cancelAnimationFrame(frame);
      if (err && err.name === "AbortError") {
        this.lastResult = cleanResult(acc, oldText);
        if (getChange(view.state, id)) streamChange(view, id, acc.trim() ? { text: prefix + this.lastResult, status: "open" } : { status: "error", error: "Stopped" });
      } else if (getChange(view.state, id)) {
        streamChange(view, id, { status: "error", error: errorText(err) });
      }
    } finally {
      if (this.abort === abort) this.abort = null;
    }
    if (!this.closed) this.setState("result");
  }

  insertPrefix(target, output) {
    if (output !== "insert" || target.to > target.from) return this.opts.insertPrefix || "";
    if (this.opts.insertPrefix !== undefined) return this.opts.insertPrefix;
    const doc = this.view.state.doc;
    const before = doc.sliceString(Math.max(0, target.from - 1), target.from);
    return before && !/\s/.test(before) ? " " : "";
  }

  async runAnswer(req, title) {
    const view = this.view;
    this.setState("running");
    this.result.hidden = false;
    const body = h("div", { class: "answer-body md-body is-streaming" });
    const meta = h("div", { class: "answer-foot" });
    this.result.replaceChildren(h("div", { class: "answer-title", text: title }), body, meta);
    const abort = new AbortController();
    this.abort = abort;
    let acc = "";
    let frame = 0;
    const phone = !!(config.isPhone && config.isPhone());
    const draw = (sources) => renderAnswer(body, acc, { render: config.render ? (md) => config.render(md, {}) : null, sources, onCite: phone ? null : (s) => config.openSource && config.openSource(s) });
    let done = null;
    try {
      done = await streamEvents("/api/ai/stream", req, {
        fetch: config.fetch, signal: abort.signal,
        onEvent: (name, data) => {
          if (name === "delta" && data.delta) {
            acc += data.delta;
            if (!frame) frame = requestAnimationFrame(() => { frame = 0; draw([]); });
          }
        },
      });
    } catch (err) {
      if (!(err && err.name === "AbortError")) {
        meta.replaceChildren(inlineMessage({ variant: "critical", text: errorText(err) }));
      }
    } finally {
      if (frame) cancelAnimationFrame(frame);
      if (this.abort === abort) this.abort = null;
    }
    if (this.closed) return;
    body.classList.remove("is-streaming");
    const sources = (done && done.sources) || [];
    draw(sources);
    this.lastResult = acc;
    if (acc.trim()) {
      const chipsEl = sourceChips(sources, (s) => config.openSource && config.openSource(s));
      const actions = h("div", { class: "answer-actions" });
      const add = (label, run, variant = "default") => {
        const btn = button({ label, size: "small", variant, className: "answer-action" });
        btn.addEventListener("mousedown", (ev) => ev.preventDefault());
        btn.addEventListener("click", () => run(btn));
        actions.appendChild(btn);
      };
      for (const extra of this.opts.extraActions || []) add(extra.label, () => { extra.run(acc.trim(), this); }, extra.primary ? "primary" : "default");
      if (config.canEdit) {
        add("Insert below", () => this.insertAnswer(acc));
        if (this.sel.to > this.sel.from) add("Replace selection", () => this.replaceWithAnswer(acc));
      }
      add("Copy", (btn) => copyToClipboard(acc.trim(), btn));
      if (config.openAITab) add("Continue in AI tab", () => { config.openAITab({ question: this.lastPrompt, answer: acc.trim(), sources }); this.close({ keepResult: true }); });
      meta.replaceChildren(...[chipsEl, actions].filter(Boolean));
    } else if (!meta.childNodes.length) {
      meta.replaceChildren(inlineMessage({ variant: "critical", text: "The model returned nothing" }));
    }
    this.setState("result");
    void view;
  }

  insertAnswer(text) {
    const view = this.view;
    const line = view.state.doc.lineAt(Math.min(this.anchor, view.state.doc.length));
    startChange(view, { title: "Answer", author: "AI", from: line.to, to: line.to, text: "\n\n" + text.trim(), status: "open" });
    this.close({ keepResult: true });
  }

  replaceWithAnswer(text) {
    startChange(this.view, { title: "Answer", author: "AI", from: this.sel.from, to: this.sel.to, text: text.trim(), status: "open" });
    this.close({ keepResult: true });
  }

  /** Tab in the empty bar field accepts every hunk of the result and returns to the note. */
  acceptFromBar() {
    if (this.state !== "result" || !this.changeId) return false;
    const c = getChange(this.view.state, this.changeId);
    if (!c || c.status !== "open") return false;
    const view = this.view;
    acceptHunks(view, this.changeId);
    view.focus();
    return true;
  }

  /* ----- Escape and closing ----- */

  /** Esc inside the bar: stop a stream, then discard the result and close. */
  escapeInBar() {
    if (this.state === "running") {
      this.stop();
      return true;
    }
    this.close();
    this.view.focus();
    return true;
  }

  /** Esc in the note: stop a stream. A tracked result is left to the tracked changes keys. */
  escapeFromDocument() {
    if (this.state === "running") {
      this.stop();
      return true;
    }
    if (this.changeId && getChange(this.view.state, this.changeId)) return false;
    this.close();
    return true;
  }

  stop() {
    if (this.abort) this.abort.abort();
  }

  onViewUpdate(u) {
    if (this.closed) return;
    // Accepting or discarding every hunk finishes the result, so the bar goes too.
    if (this.state === "result" && this.changeId && !getChange(u.state, this.changeId) && getChange(u.startState, this.changeId)) {
      this.close({ keepResult: true });
    }
  }

  close({ keepResult = false } = {}) {
    if (this.closed) return;
    this.closed = true;
    this.stop();
    if (!keepResult && this.changeId && getChange(this.view.state, this.changeId)) discardHunks(this.view, this.changeId);
    controllers.delete(this.id);
    this.composer.destroy();
    const v = this.view.state.field(askBarField, false);
    if (v && v.id === this.id) this.view.dispatch({ effects: setBar.of(null) });
    if (this.docked && config.dock) config.dock(null);
    this.root.remove();
    if (config.onChange) config.onChange();
  }
}

function truncate(s, n) {
  const t = String(s || "").replace(/\s+/g, " ").trim();
  return t.length > n ? t.slice(0, n - 1) + "…" : t;
}

function errorText(err) {
  if (!err) return "The request failed";
  if (err.status === 429) return "The monthly AI budget is used up";
  if (err.status === 0 || err.name === "TypeError") return "AI needs a connection";
  return err.message || "The request failed";
}
