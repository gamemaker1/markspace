/**
 * Find and replace for the note editor. One find bar serves the Live, Source
 * and Reading modes and keeps one query across them.
 *
 * - Live and Source drive the CodeMirror search state. The CodeMirror panel is
 *   an empty element, and this bar replaces its DOM. Replace works on the
 *   markdown source, so syntax that Live hides is still found.
 * - Reading marks matches in the rendered HTML with mark elements.
 *
 * editor.js mounts it with createFind. The pure helpers at the top have no DOM
 * or CodeMirror view and are tested in test/client/find.test.mjs.
 */

import {
  EditorView, EditorSelection, keymap, search, searchKeymap, SearchQuery, setSearchQuery, getSearchQuery, searchPanelOpen,
  findNext, findPrevious, replaceNext, replaceAll, openSearchPanel, closeSearchPanel,
} from "/static/vendor/codemirror.js";
import { h, icon, button, iconButton, inlineMessage, uid } from "/static/js/ui.js";

/** Matches past this number are counted as "1000+". */
export const MAX_MATCHES = 1000;
/** The class of a match mark in Reading. The current match also has CURRENT_CLASS. */
export const HIT_CLASS = "ms-find-hit";
export const CURRENT_CLASS = "is-current";

const IS_MAC = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent || "");
const MOD = IS_MAC ? "⌘" : "Ctrl+";
const ALT = IS_MAC ? "⌥" : "Alt+";
/** The keybinding hint of the Replace disclosure. macOS keeps Cmd+H for hiding the app. */
export const REPLACE_HINT = IS_MAC ? "⌘⌥F" : "Ctrl+H";
export const FIND_HINT = MOD + "F";

/* ---------- Query and text matching ---------- */

// The same word characters CodeMirror's default categorizer uses.
const WORD = /[\p{Alphabetic}\p{Number}_]/u;

/**
 * Returns the query state with every field present and of the right type.
 * @param {object} [s]
 * @returns {{query: string, replace: string, caseSensitive: boolean, wholeWord: boolean, regexp: boolean}}
 */
export function normalizeQuery(s = {}) {
  return {
    query: String(s.query || ""),
    replace: String(s.replace || ""),
    caseSensitive: !!s.caseSensitive,
    wholeWord: !!s.wholeWord,
    regexp: !!s.regexp,
  };
}

/** Escapes text for use as a literal inside a regular expression. */
export function escapeRegExp(text) {
  return String(text).replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/**
 * Builds the matcher for a query. It never throws. An invalid regular
 * expression returns a null regex and an error message for the bar. The flags
 * follow CodeMirror's RegExpCursor, "gmu" plus "i" without Match case.
 * @param {{query?: string, caseSensitive?: boolean, wholeWord?: boolean, regexp?: boolean}} s
 * @returns {{regex: RegExp|null, wholeWord: boolean, error: string}}
 */
export function buildMatcher(s) {
  const q = normalizeQuery(s);
  if (!q.query) return { regex: null, wholeWord: q.wholeWord, error: "" };
  const source = q.regexp ? q.query : escapeRegExp(q.query);
  try {
    return { regex: new RegExp(source, "gmu" + (q.caseSensitive ? "" : "i")), wholeWord: q.wholeWord, error: "" };
  } catch {
    return { regex: null, wholeWord: q.wholeWord, error: "Invalid regular expression" };
  }
}

function charBefore(text, pos) {
  return pos > 0 ? Array.from(text.slice(Math.max(0, pos - 2), pos)).pop() : "";
}

function charAfter(text, pos) {
  return pos < text.length ? String.fromCodePoint(text.codePointAt(pos)) : "";
}

const isWord = (ch) => !!ch && WORD.test(ch);

/**
 * Whether from..to is a whole word. Each edge needs a non-word character on
 * one side, as in CodeMirror's whole word test.
 */
export function isWholeWord(text, from, to) {
  return (!isWord(charBefore(text, from)) || !isWord(charAfter(text, from)))
    && (!isWord(charAfter(text, to)) || !isWord(charBefore(text, to)));
}

/**
 * Finds the matches of a matcher in text. Empty matches are skipped.
 * @param {string} text
 * @param {{regex: RegExp|null, wholeWord?: boolean}} matcher
 * @param {number} [limit]
 * @returns {{from: number, to: number}[]}
 */
export function findInText(text, matcher, limit = MAX_MATCHES + 1) {
  const out = [];
  if (!matcher || !matcher.regex) return out;
  const re = new RegExp(matcher.regex.source, matcher.regex.flags);
  let m;
  while (out.length < limit && (m = re.exec(text))) {
    const from = m.index;
    const to = from + m[0].length;
    if (to === from) {
      re.lastIndex = from + (charAfter(text, from).length || 1);
      if (re.lastIndex > text.length) break;
      continue;
    }
    if (!matcher.wholeWord || isWholeWord(text, from, to)) out.push({ from, to });
  }
  return out;
}

/**
 * The count text of the bar: "3 of 12", "12 matches" without a current match,
 * "No results", or empty without a query.
 * @param {{index?: number, total?: number, capped?: boolean, query?: string}} c
 */
export function countLabel({ index = -1, total = 0, capped = false, query = "x" } = {}) {
  if (!query) return "";
  if (!total) return "No results";
  const all = total + (capped ? "+" : "");
  if (index >= 0) return (index + 1) + " of " + all;
  return all + (total === 1 && !capped ? " match" : " matches");
}

/**
 * The short count text for narrow bars: "3/12", "12" without a current match,
 * "0" for no results, or empty without a query. countLabel gives the full
 * text for the accessible name and the tooltip.
 * @param {{index?: number, total?: number, capped?: boolean, query?: string}} c
 */
export function countShortLabel({ index = -1, total = 0, capped = false, query = "x" } = {}) {
  if (!query) return "";
  if (!total) return "0";
  const all = total + (capped ? "+" : "");
  return index >= 0 ? (index + 1) + "/" + all : all;
}

/**
 * Maps a match index to the equivalent index in another list of matches. The
 * same count keeps the index. A different count keeps the relative position.
 * @returns {number} -1 when the other list is empty
 */
export function mapIndex(index, fromTotal, toTotal) {
  if (toTotal <= 0) return -1;
  if (index < 0) return 0;
  if (fromTotal === toTotal) return Math.min(index, toTotal - 1);
  if (fromTotal <= 1) return 0;
  return Math.min(toTotal - 1, Math.round((index * (toTotal - 1)) / (fromTotal - 1)));
}

/* ---------- Reading: text nodes and marks ---------- */

const BLOCK_TAGS = new Set([
  "ADDRESS", "ARTICLE", "ASIDE", "BLOCKQUOTE", "BR", "DD", "DETAILS", "DIV", "DL", "DT", "FIGCAPTION", "FIGURE", "FOOTER",
  "H1", "H2", "H3", "H4", "H5", "H6", "HEADER", "HR", "IMG", "LI", "OL", "P", "PRE", "SECTION", "SUMMARY", "TABLE", "TBODY",
  "TD", "TFOOT", "TH", "THEAD", "TR", "UL", "VIDEO", "AUDIO", "IFRAME",
]);
const SKIP_TAGS = new Set(["SCRIPT", "STYLE", "TEMPLATE", "NOSCRIPT", "BUTTON", "SVG", "CANVAS", "INPUT", "SELECT", "TEXTAREA"]);

/**
 * Collects the text nodes under root in document order. Text in different
 * block elements is joined with a newline, so a literal query never matches
 * across paragraphs. Inline elements such as strong join without a break.
 * @param {Node} root
 * @param {{skip?: string}} [options] a selector for subtrees to leave out
 * @returns {{text: string, segments: {node: Text, start: number, end: number}[]}}
 */
export function collectText(root, { skip = "" } = {}) {
  const segments = [];
  let text = "";
  let pendingBreak = false;
  const walk = (node) => {
    for (const child of Array.from(node.childNodes)) {
      if (child.nodeType === 3) {
        const data = child.data;
        if (!data) continue;
        if (pendingBreak && text) text += "\n";
        pendingBreak = false;
        segments.push({ node: child, start: text.length, end: text.length + data.length });
        text += data;
      } else if (child.nodeType === 1) {
        const tag = String(child.tagName).toUpperCase();
        if (SKIP_TAGS.has(tag) || child.hidden || child.getAttribute("aria-hidden") === "true") continue;
        if (skip && child.matches(skip)) continue;
        const block = BLOCK_TAGS.has(tag);
        if (block) pendingBreak = true;
        walk(child);
        if (block) pendingBreak = true;
      }
    }
  };
  walk(root);
  return { text, segments };
}

/**
 * Wraps matches in mark elements. A match that spans inline elements gets one
 * mark per text node. Returns the marks of each match in document order.
 * @param {{from: number, to: number}[]} matches sorted by position
 * @param {{segments: {node: Text, start: number, end: number}[]}} collected from collectText
 * @returns {HTMLElement[][]}
 */
export function markMatches(matches, collected) {
  const { segments } = collected;
  const marks = matches.map(() => []);
  const perNode = new Map();
  let first = 0;
  matches.forEach((m, index) => {
    while (first < segments.length && segments[first].end <= m.from) first++;
    for (let k = first; k < segments.length && segments[k].start < m.to; k++) {
      const seg = segments[k];
      const a = Math.max(m.from, seg.start) - seg.start;
      const b = Math.min(m.to, seg.end) - seg.start;
      if (b <= a) continue;
      if (!perNode.has(seg)) perNode.set(seg, []);
      perNode.get(seg).push([a, b, index]);
    }
  });
  for (const [seg, ranges] of perNode) {
    const node = seg.node;
    const parent = node.parentNode;
    const doc = node.ownerDocument;
    const data = node.data;
    let pos = 0;
    for (const [a, b, index] of ranges) {
      if (a > pos) parent.insertBefore(doc.createTextNode(data.slice(pos, a)), node);
      const mark = doc.createElement("mark");
      mark.className = HIT_CLASS;
      mark.setAttribute("data-find-index", String(index));
      mark.appendChild(doc.createTextNode(data.slice(a, b)));
      parent.insertBefore(mark, node);
      marks[index].push(mark);
      pos = b;
    }
    if (pos < data.length) node.data = data.slice(pos);
    else node.remove();
  }
  return marks;
}

/** Joins adjacent text nodes of parent and drops empty ones. */
function mergeText(parent) {
  let prev = null;
  for (const child of Array.from(parent.childNodes)) {
    if (child.nodeType !== 3) {
      prev = null;
    } else if (!child.data) {
      child.remove();
    } else if (prev) {
      prev.data += child.data;
      child.remove();
    } else {
      prev = child;
    }
  }
}

/** Removes the match marks under root and restores the original text nodes. */
export function clearMarks(root) {
  const parents = new Set();
  for (const mark of Array.from(root.querySelectorAll("mark." + HIT_CLASS))) {
    const parent = mark.parentNode;
    if (!parent) continue;
    parent.insertBefore(mark.ownerDocument.createTextNode(mark.textContent), mark);
    mark.remove();
    parents.add(parent);
  }
  for (const parent of parents) mergeText(parent);
}

/* ---------- CodeMirror ---------- */

/** Builds the CodeMirror SearchQuery of a query state. Text queries are literal. */
export function toSearchQuery(s) {
  const q = normalizeQuery(s);
  return new SearchQuery({ search: q.query, replace: q.replace, caseSensitive: q.caseSensitive, wholeWord: q.wholeWord, regexp: q.regexp, literal: true });
}

/**
 * Lists the matches of a SearchQuery in an EditorState, up to limit.
 * @returns {{from: number, to: number}[]}
 */
export function cmMatches(state, query, limit = MAX_MATCHES + 1) {
  const out = [];
  if (!query || !query.valid) return out;
  const cursor = query.getCursor(state);
  while (out.length < limit && !cursor.next().done) {
    const { from, to } = cursor.value;
    if (to > from) out.push({ from, to });
  }
  return out;
}

/** The index of the match equal to from..to, or -1. */
export function matchIndexAt(matches, from, to) {
  let lo = 0;
  let hi = matches.length - 1;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    const m = matches[mid];
    if (m.from < from) lo = mid + 1;
    else if (m.from > from) hi = mid - 1;
    else return m.to === to ? mid : -1;
  }
  return -1;
}

/** The index of the first match that starts at or after pos, wrapping to 0. */
export function nextIndexFrom(matches, pos) {
  if (!matches.length) return -1;
  const i = matches.findIndex((m) => m.from >= pos);
  return i >= 0 ? i : 0;
}

/**
 * Sets the query and replaces every match in one transaction, so one undo
 * restores the text. view needs state and dispatch only.
 * @returns {boolean} whether anything was replaced
 */
export function replaceAllMatches(view, s) {
  const query = toSearchQuery(s);
  if (!getSearchQuery(view.state).eq(query)) view.dispatch({ effects: setSearchQuery.of(query) });
  return replaceAll(view);
}

/* ---------- Find bar ---------- */

function glyph(key, text) {
  return h("span", { class: "ms-find-glyph ms-find-glyph-" + key, text });
}

/**
 * Builds the find bar: a Find TextInput with the match count, the Match case,
 * Whole word and Regular expression toggles, previous and next, a Replace
 * disclosure with its row, the Reading link to Source, and Close.
 * Enter and Shift+Enter in Find step through matches. Enter in Replace
 * replaces, Ctrl+Enter replaces all. Alt+C, Alt+W and Alt+R flip the toggles.
 * Escape anywhere in the bar closes it.
 * @param {{onQuery?: Function, onStep?: Function, onReplace?: Function, onReplaceAll?: Function, onClose?: Function, onSwitchToSource?: Function}} handlers
 */
export function createFindBar({ onQuery = () => {}, onStep = () => {}, onReplace = () => {}, onReplaceAll = () => {}, onClose = () => {}, onSwitchToSource = () => {} } = {}) {
  const id = uid("ms-find");
  const countId = id + "-count";
  const replaceId = id + "-replace";
  const state = normalizeQuery();
  let replaceOpen = false;
  let reading = false;
  let canEdit = true;
  let lastCount = null;

  const findInput = h("input", { type: "text", class: "text-input-control ms-find-input", "aria-label": "Find", placeholder: "Find", autocomplete: "off", autocapitalize: "off", enterkeyhint: "search", "aria-describedby": countId });
  findInput.setAttribute("spellcheck", "false");
  const findBox = h("span", { class: "text-input text-input-sm has-leading-visual ms-find-box" },
    h("span", { class: "text-input-visual", "aria-hidden": "true" }, icon("search", 16)), findInput);
  const count = h("span", { id: countId, class: "ms-find-count", "aria-live": "polite", "aria-atomic": "true" });
  // Below 382px the short count replaces the full one on screen. Screen readers read the full count.
  const countShort = h("span", { class: "ms-find-count-short", "aria-hidden": "true" });
  const status = h("span", { class: "ms-find-status" }, count, countShort);

  const disclosure = iconButton({
    icon: "chevron-right", label: "Toggle replace", keybindingHint: REPLACE_HINT, size: "small", className: "ms-find-disclosure",
    attrs: { "aria-expanded": "false", "aria-controls": replaceId },
    onClick: () => setReplaceOpen(!replaceOpen),
  });
  const optionButtons = {};
  const option = (key, label, text, hint) => {
    optionButtons[key] = iconButton({ icon: glyph(key, text), label, keybindingHint: hint, size: "small", pressed: false, className: "ms-find-option", attrs: { "data-option": key }, onClick: () => flip(key) });
    return optionButtons[key];
  };
  const options = h("span", { class: "ms-find-options", role: "group", "aria-label": "Search options" },
    option("caseSensitive", "Match case", "Aa", ALT + "C"),
    option("wholeWord", "Whole word", "ab", ALT + "W"),
    option("regexp", "Regular expression", ".*", ALT + "R"));
  const prevBtn = iconButton({ icon: "chevron-up", label: "Previous match", keybindingHint: "Shift+Enter", size: "small", className: "ms-find-prev", onClick: () => onStep(-1) });
  const nextBtn = iconButton({ icon: "chevron-down", label: "Next match", keybindingHint: "Enter", size: "small", className: "ms-find-next", onClick: () => onStep(1) });
  const sourceLink = button({ label: "Switch to Source to replace", variant: "link", size: "small", className: "ms-find-source", onClick: () => onSwitchToSource() });
  sourceLink.hidden = true;
  const closeBtn = iconButton({ icon: "x", label: "Close find", keybindingHint: "Escape", size: "small", className: "ms-find-close", onClick: () => onClose() });
  const main = h("div", { class: "ms-find-row ms-find-main" }, disclosure, findBox, status, options, prevBtn, nextBtn, sourceLink, closeBtn);

  const replaceInput = h("input", { type: "text", class: "text-input-control ms-replace-input", "aria-label": "Replace", placeholder: "Replace", autocomplete: "off", autocapitalize: "off", enterkeyhint: "done" });
  replaceInput.setAttribute("spellcheck", "false");
  const replaceBox = h("span", { class: "text-input text-input-sm ms-find-box ms-replace-box" }, replaceInput);
  const replaceBtn = button({ label: "Replace", size: "small", className: "ms-replace-one", onClick: () => onReplace() });
  const replaceAllBtn = button({ label: "Replace all", size: "small", className: "ms-replace-all", onClick: () => onReplaceAll() });
  const replaceRow = h("div", { id: replaceId, class: "ms-find-row ms-find-replace", hidden: true }, replaceBox, replaceBtn, replaceAllBtn);

  const element = h("div", { id, class: "ms-find", role: "search", "aria-label": "Find in note", hidden: true },
    h("div", { class: "ms-find-inner" }, main, replaceRow));

  function flip(key) {
    state[key] = !state[key];
    optionButtons[key].setAttribute("aria-pressed", String(state[key]));
    onQuery({ ...state }, key);
  }

  // aria-disabled keeps the button focusable. The click handler of ui.js ignores it, and the disabled style has no border.
  function setInactive(btn, inactive) {
    if (inactive) btn.setAttribute("aria-disabled", "true");
    else btn.removeAttribute("aria-disabled");
  }

  function syncReplace() {
    const allowed = canEdit && !reading;
    disclosure.hidden = !allowed;
    replaceRow.hidden = !(allowed && replaceOpen);
    sourceLink.hidden = !(canEdit && reading);
    disclosure.setAttribute("aria-expanded", String(allowed && replaceOpen));
    const slot = disclosure.querySelector(".btn-visual");
    if (slot) slot.replaceChildren(icon(replaceOpen ? "chevron-down" : "chevron-right", 16));
    element.classList.toggle("is-reading", reading);
    element.classList.toggle("has-replace", allowed && replaceOpen);
  }

  function setReplaceOpen(on) {
    replaceOpen = !!on;
    syncReplace();
  }

  findInput.addEventListener("input", () => {
    state.query = findInput.value;
    onQuery({ ...state }, "query");
  });
  replaceInput.addEventListener("input", () => {
    state.replace = replaceInput.value;
    onQuery({ ...state }, "replace");
  });
  element.addEventListener("keydown", (ev) => {
    if (ev.isComposing) return;
    if (ev.key === "Escape") {
      ev.preventDefault();
      ev.stopPropagation();
      onClose();
      return;
    }
    if (ev.key === "Enter" && ev.target === findInput) {
      ev.preventDefault();
      onStep(ev.shiftKey ? -1 : 1);
      return;
    }
    if (ev.key === "Enter" && ev.target === replaceInput) {
      ev.preventDefault();
      if (ev.ctrlKey || ev.metaKey) onReplaceAll();
      else onReplace();
      return;
    }
    if (ev.altKey && !ev.ctrlKey && !ev.metaKey && !ev.shiftKey) {
      const key = { KeyC: "caseSensitive", KeyW: "wholeWord", KeyR: "regexp" }[ev.code];
      if (key) {
        ev.preventDefault();
        flip(key);
      }
    }
  });

  return {
    element,
    id,
    findInput,
    replaceInput,
    get state() { return { ...state }; },
    get isOpen() { return !element.hidden; },
    get replaceOpen() { return replaceOpen && canEdit && !reading; },
    /** Sets the query state without calling onQuery. */
    setState(next) {
      Object.assign(state, normalizeQuery({ ...state, ...next }));
      if (findInput.value !== state.query) findInput.value = state.query;
      if (replaceInput.value !== state.replace) replaceInput.value = state.replace;
      for (const [key, btn] of Object.entries(optionButtons)) btn.setAttribute("aria-pressed", String(state[key]));
    },
    show() { element.hidden = false; },
    hide() { element.hidden = true; },
    setReplaceOpen,
    /** Reading hides Replace and shows the Source link. Without edit access neither shows. */
    setMode({ reading: isReading = false, canEdit: editable = true } = {}) {
      reading = !!isReading;
      canEdit = !!editable;
      syncReplace();
    },
    /** Shows the count, or the inline error of an invalid regular expression. */
    setCount({ index = -1, total = 0, capped = false, error = "" } = {}) {
      const key = [index, total, capped, error, state.query ? 1 : 0].join("|");
      if (key === lastCount) return;
      lastCount = key;
      if (error) {
        findInput.setAttribute("aria-invalid", "true");
        count.classList.add("is-error");
        count.replaceChildren(inlineMessage({ variant: "critical", size: "small", text: error }));
        countShort.textContent = "";
        countShort.removeAttribute("title");
      } else {
        findInput.removeAttribute("aria-invalid");
        count.classList.remove("is-error");
        const full = countLabel({ index, total, capped, query: state.query });
        count.textContent = full;
        countShort.textContent = countShortLabel({ index, total, capped, query: state.query });
        if (full) countShort.setAttribute("title", full);
        else countShort.removeAttribute("title");
      }
      const none = !!error || !total;
      setInactive(prevBtn, none);
      setInactive(nextBtn, none);
      setInactive(replaceBtn, none);
      setInactive(replaceAllBtn, none);
    },
    focusFind() {
      findInput.focus({ preventScroll: true });
      findInput.select();
    },
    focusReplace() {
      replaceInput.focus({ preventScroll: true });
      replaceInput.select();
    },
  };
}

/* ---------- Controller ---------- */

/**
 * Creates find and replace for one mounted editor.
 * @param {object} options
 * @param {() => "live"|"source"|"reading"} options.getMode
 * @param {(mode: string) => void} options.setMode switches the editor mode and remembers it
 * @param {boolean} options.canEdit
 * @param {HTMLElement} options.readRoot the rendered note in Reading
 * @param {HTMLElement} options.readPane the Reading pane, which takes focus when the bar closes in Reading
 * @param {string} [options.readSkip] a selector for rendered parts that are not note text
 * @param {HTMLElement} [options.trigger] the editor bar button, which gets aria-expanded
 * @param {() => {top: number, bottom: number}} [options.insets] the editor bar and keyboard bar heights
 * @param {() => void} [options.onLayout] runs when the bar height or placement changes
 */
export function createFind({ getMode, setMode, canEdit, readRoot, readPane, readSkip = "", trigger = null, insets = () => ({ top: 0, bottom: 0 }), onLayout = () => {} }) {
  let view = null;
  let open = false;
  let closing = false;
  let docked = false;
  let height = 0;
  let cmCache = { doc: null, query: null, list: [] };
  let reading = { marks: [], total: 0, capped: false, current: -1, currentFrom: -1 };
  let readingTimer = 0;

  const bar = createFindBar({
    onQuery: (next, why) => {
      if (why === "replace") {
        syncCmQuery();
        return;
      }
      if (getMode() === "reading") scheduleReading();
      else applyCm({ incremental: why === "query" });
    },
    onStep: (dir) => step(dir),
    onReplace: () => replaceOne(),
    onReplaceAll: () => replaceEvery(),
    onClose: () => close({ focus: true }),
    onSwitchToSource: () => {
      setMode("source");
      bar.setReplaceOpen(true);
      bar.focusReplace();
    },
  });
  if (trigger) {
    trigger.setAttribute("aria-expanded", "false");
    trigger.setAttribute("aria-controls", bar.id);
  }

  const resize = typeof ResizeObserver === "function" ? new ResizeObserver((entries) => {
    const box = entries[entries.length - 1].borderBoxSize;
    const next = bar.element.hidden ? 0 : Math.round(box && box[0] ? box[0].blockSize : bar.element.offsetHeight);
    if (next === height) return;
    height = next;
    writeLayout();
  }) : null;
  if (resize) resize.observe(bar.element);

  /** Writes the scroll padding the sticky bar needs and tells the editor. */
  function writeLayout() {
    const top = open && !docked ? height : 0;
    const html = document.documentElement;
    html.classList.toggle("ms-finding", top > 0);
    if (top > 0) html.style.setProperty("--ms-find-h", top + "px");
    else html.style.removeProperty("--ms-find-h");
    onLayout();
  }

  /* ----- CodeMirror ----- */

  // The panel keeps CodeMirror's search state open, which draws the match highlights. Its element stays empty and
  // hidden. The bar is outside CodeMirror, so it also shows in Reading.
  const createPanel = () => ({
    dom: h("div", { class: "ms-find-cm-panel", "aria-hidden": "true" }),
    top: true,
    update: (u) => {
      if (!open || getMode() === "reading") return;
      if (u.docChanged || u.selectionSet || getSearchQuery(u.state) !== getSearchQuery(u.startState)) refreshCount();
    },
    destroy: () => {
      if (open && !closing) close({ focus: false });
    },
  });

  const keys = [
    { key: "Mod-f", run: () => { openFind(); return true; } },
    { key: "Mod-Alt-f", run: () => openReplace() },
    IS_MAC ? null : { key: "Ctrl-h", run: () => openReplace() },
    { key: "F3", run: () => step(1), shift: () => step(-1), preventDefault: true },
    { key: "Mod-g", run: () => step(1), shift: () => step(-1), preventDefault: true },
    { key: "Escape", run: () => { if (!open) return false; close({ focus: true }); return true; } },
  ].filter(Boolean);

  const extension = [
    search({ literal: true, top: true, createPanel, scrollToMatch: (range) => EditorView.scrollIntoView(range, { y: "center" }) }),
    keymap.of([...keys, ...searchKeymap.filter((b) => b.key === "Mod-d" || b.key === "Mod-Shift-l")]),
  ];

  function syncCmQuery() {
    if (!view) return false;
    const query = toSearchQuery(bar.state);
    if (getSearchQuery(view.state).eq(query)) return false;
    view.dispatch({ effects: setSearchQuery.of(query) });
    return true;
  }

  function cmList() {
    const state = view.state;
    const query = getSearchQuery(state);
    if (cmCache.doc !== state.doc || cmCache.query !== query) cmCache = { doc: state.doc, query, list: cmMatches(state, query) };
    return cmCache.list;
  }

  /** The current CodeMirror match: the one the selection covers, or -1. */
  function cmCurrent() {
    const list = cmList();
    const sel = view.state.selection.main;
    return { list, index: matchIndexAt(list, sel.from, sel.to) };
  }

  function selectCm(m) {
    view.dispatch({ selection: EditorSelection.single(m.from, m.to), effects: EditorView.scrollIntoView(m.from, { y: "center" }), userEvent: "select.search" });
  }

  /** Sends the query to CodeMirror. While typing, the selection moves to the first match from the current one. */
  function applyCm({ incremental = false } = {}) {
    if (!view) return;
    syncCmQuery();
    if (incremental) {
      const list = cmList();
      const i = nextIndexFrom(list, view.state.selection.main.from);
      if (i >= 0) selectCm(list[i]);
    }
    refreshCount();
  }

  /* ----- Reading ----- */

  function scheduleReading() {
    clearTimeout(readingTimer);
    readingTimer = setTimeout(() => applyReading({ near: true }), 60);
  }

  /**
   * Marks the matches in the rendered note. keep keeps a match index, pick maps the new count to an index, and near
   * starts at the previous current match or the first match on screen.
   */
  function applyReading({ keep = -1, pick = null, near = false, scroll = true } = {}) {
    clearTimeout(readingTimer);
    const previousFrom = reading.currentFrom;
    clearMarks(readRoot);
    reading = { marks: [], total: 0, capped: false, current: -1, currentFrom: -1 };
    const matcher = buildMatcher(bar.state);
    if (!open || !matcher.regex) {
      refreshCount();
      return;
    }
    const collected = collectText(readRoot, { skip: readSkip });
    let matches = findInText(collected.text, matcher);
    const capped = matches.length > MAX_MATCHES;
    if (capped) matches = matches.slice(0, MAX_MATCHES);
    reading.marks = markMatches(matches, collected);
    reading.froms = matches.map((m) => m.from);
    reading.total = matches.length;
    reading.capped = capped;
    if (!matches.length) {
      refreshCount();
      return;
    }
    let index = 0;
    if (pick) index = pick(matches.length);
    else if (keep >= 0) index = Math.min(keep, matches.length - 1);
    else if (near) index = previousFrom >= 0 ? nextIndexFrom(matches, previousFrom) : firstVisibleMark();
    setReadingCurrent(index, { scroll });
  }

  function firstVisibleMark() {
    const top = insets().top + (docked ? 0 : height);
    const i = reading.marks.findIndex((parts) => parts[0] && parts[0].getBoundingClientRect().bottom > top);
    return i >= 0 ? i : 0;
  }

  function setReadingCurrent(i, { scroll = true } = {}) {
    const total = reading.total;
    for (const el of reading.marks[reading.current] || []) el.classList.remove(CURRENT_CLASS);
    reading.current = total ? ((i % total) + total) % total : -1;
    reading.currentFrom = reading.current >= 0 ? reading.froms[reading.current] : -1;
    const parts = reading.marks[reading.current] || [];
    for (const el of parts) el.classList.add(CURRENT_CLASS);
    if (scroll && parts[0]) reveal(parts[0]);
    refreshCount();
  }

  /** Scrolls a mark to the centre when it is under the sticky bars or off screen. */
  function reveal(el) {
    const r = el.getBoundingClientRect();
    const { top, bottom } = insets();
    const vv = window.visualViewport;
    const viewTop = top + (docked ? 0 : height) + 8;
    const viewBottom = (vv ? vv.height : window.innerHeight) - bottom - (docked ? height : 0) - 8;
    if (r.top < viewTop || r.bottom > viewBottom) el.scrollIntoView({ block: "center" });
  }

  /** Selects the current Reading match as a DOM range after its marks are gone. */
  function selectReadingMatch(index) {
    if (index < 0 || typeof document.createRange !== "function") return;
    const collected = collectText(readRoot, { skip: readSkip });
    const m = findInText(collected.text, buildMatcher(bar.state))[index];
    if (!m) return;
    const start = collected.segments.find((s) => s.end > m.from);
    const end = collected.segments.find((s) => s.end >= m.to);
    if (!start || !end) return;
    const range = document.createRange();
    range.setStart(start.node, m.from - start.start);
    range.setEnd(end.node, m.to - end.start);
    const sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(range);
  }

  /* ----- Count ----- */

  function refreshCount() {
    const s = bar.state;
    const error = buildMatcher(s).error;
    if (!open) return;
    if (getMode() === "reading") {
      bar.setCount({ index: reading.current, total: reading.total, capped: reading.capped, error });
      return;
    }
    if (!view) return;
    const { list, index } = cmCurrent();
    bar.setCount({ index: index < MAX_MATCHES ? index : -1, total: Math.min(list.length, MAX_MATCHES), capped: list.length > MAX_MATCHES, error });
  }

  /* ----- Actions ----- */

  /** The selected text to prefill Find with: one line of at most 100 characters. */
  function selectedText() {
    let text = "";
    if (getMode() === "reading") {
      const sel = window.getSelection ? window.getSelection() : null;
      if (sel && sel.rangeCount && readRoot.contains(sel.anchorNode)) text = sel.toString();
    } else if (view) {
      const s = view.state.selection.main;
      if (!s.empty && s.to - s.from <= 100) text = view.state.sliceDoc(s.from, s.to);
    }
    if (!text || text.length > 100 || /[\r\n]/.test(text)) return "";
    return bar.state.regexp ? escapeRegExp(text) : text;
  }

  function openFind({ replace = false, focus = true } = {}) {
    const mode = getMode();
    const inBar = bar.element.contains(document.activeElement);
    const prefill = inBar ? "" : selectedText();
    if (prefill) bar.setState({ query: prefill });
    const wasOpen = open;
    open = true;
    bar.setMode({ reading: mode === "reading", canEdit });
    if (replace) bar.setReplaceOpen(true);
    else if (!wasOpen) bar.setReplaceOpen(false);
    bar.show();
    if (trigger) trigger.setAttribute("aria-expanded", "true");
    if (view && !searchPanelOpen(view.state)) openSearchPanel(view);
    if (mode === "reading") {
      if (!wasOpen || prefill) applyReading({ near: !wasOpen });
      else refreshCount();
    } else {
      syncCmQuery();
      refreshCount();
    }
    writeLayout();
    if (focus) {
      if (replace && bar.replaceOpen && bar.state.query) bar.focusReplace();
      else bar.focusFind();
    }
    return true;
  }

  function openReplace() {
    if (!canEdit || getMode() === "reading") return false;
    return openFind({ replace: true });
  }

  function step(dir) {
    if (!open) openFind({ focus: false });
    if (getMode() === "reading") {
      if (!reading.total) return true;
      setReadingCurrent(reading.current < 0 ? (dir > 0 ? 0 : reading.total - 1) : reading.current + dir);
      return true;
    }
    if (!view) return false;
    syncCmQuery();
    if (toSearchQuery(bar.state).valid) (dir > 0 ? findNext : findPrevious)(view);
    refreshCount();
    return true;
  }

  function replaceOne() {
    if (!view || !canEdit || getMode() === "reading") return;
    syncCmQuery();
    if (!toSearchQuery(bar.state).valid) return;
    // CodeMirror replaces only a selected match. Without one, the next match is selected first.
    if (cmCurrent().index < 0) findNext(view);
    replaceNext(view);
    refreshCount();
  }

  function replaceEvery() {
    if (!view || !canEdit || getMode() === "reading") return;
    replaceAllMatches(view, bar.state);
    refreshCount();
  }

  /** Closes the bar. With focus, focus returns to the note at the current match. */
  function close({ focus = true } = {}) {
    if (!open) return;
    const mode = getMode();
    const current = reading.current;
    open = false;
    closing = true;
    clearTimeout(readingTimer);
    clearMarks(readRoot);
    reading = { marks: [], total: 0, capped: false, current: -1, currentFrom: -1 };
    if (view && searchPanelOpen(view.state)) closeSearchPanel(view);
    closing = false;
    const hadFocus = bar.element.contains(document.activeElement);
    bar.hide();
    if (trigger) trigger.setAttribute("aria-expanded", "false");
    writeLayout();
    if (!focus && !hadFocus) return;
    if (mode === "reading") {
      if (readPane) {
        if (!readPane.hasAttribute("tabindex")) readPane.setAttribute("tabindex", "-1");
        readPane.focus({ preventScroll: true });
      }
      selectReadingMatch(current);
    } else if (view) {
      view.focus();
      view.dispatch({ effects: EditorView.scrollIntoView(view.state.selection.main.head, { y: "nearest" }) });
    }
  }

  return {
    id: bar.id,
    element: bar.element,
    bar,
    /** The CodeMirror extensions: the search state with this panel, and the find keys. */
    extension,
    /** Connects the EditorView after it is created. */
    bind(v) {
      view = v;
    },
    get isOpen() { return open; },
    open: openFind,
    openReplace,
    close,
    toggle() {
      if (open) close({ focus: true });
      else openFind();
    },
    step,
    /** The bar height above the note, for the scroll margins. */
    topHeight: () => (open && !docked ? height : 0),
    /** The bar height above the keyboard bar while docked, for the scroll margins. */
    dockHeight: () => (open && docked ? height : 0),
    /**
     * Places the bar above the keyboard bar while the on-screen keyboard is open on a phone. Returns the height it
     * covers at the bottom.
     */
    placeDock({ docked: dock = false, bottom = 0, left = "", right = "" } = {}) {
      const next = dock && open;
      if (next !== docked) {
        docked = next;
        bar.element.classList.toggle("is-docked", docked);
        writeLayout();
      }
      const el = bar.element;
      const transform = docked && bottom ? "translateY(" + -Math.round(bottom) + "px)" : "";
      if (el.style.transform !== transform) el.style.transform = transform;
      if (el.style.left !== (docked ? left : "")) el.style.left = docked ? left : "";
      if (el.style.right !== (docked ? right : "")) el.style.right = docked ? right : "";
      return docked ? height : 0;
    },
    /** Moves the find state to a new mode. editor.js calls it after the mode switch and the Reading render. */
    modeChanged(prev, next) {
      bar.setMode({ reading: next === "reading", canEdit });
      if (!open || prev === next) return;
      const fromReading = prev === "reading";
      const toReading = next === "reading";
      if (fromReading === toReading || !view) {
        refreshCount();
        return;
      }
      if (toReading) {
        const { list, index } = cmCurrent();
        const at = index >= 0 ? index : nextIndexFrom(list, view.state.selection.main.from);
        applyReading({ pick: (total) => mapIndex(at, list.length, total) });
      } else {
        const at = reading.current;
        const total = reading.total;
        clearMarks(readRoot);
        reading = { marks: [], total: 0, capped: false, current: -1, currentFrom: -1 };
        syncCmQuery();
        const list = cmList();
        const k = at >= 0 ? mapIndex(at, total, list.length) : -1;
        if (k >= 0) selectCm(list[k]);
        refreshCount();
      }
    },
    /** Marks the matches again after Reading renders the note again. */
    readingRendered() {
      if (open && getMode() === "reading") applyReading({ keep: reading.current, scroll: false });
    },
    /**
     * Handles the find keys outside CodeMirror, such as in Reading. Returns true when the key was used.
     * @param {KeyboardEvent} ev
     */
    handleKey(ev) {
      if (ev.defaultPrevented || ev.isComposing) return false;
      const mod = IS_MAC ? ev.metaKey && !ev.ctrlKey : ev.ctrlKey && !ev.metaKey;
      const key = String(ev.key || "").toLowerCase();
      if (mod && !ev.altKey && !ev.shiftKey && (ev.code === "KeyF" || key === "f")) {
        ev.preventDefault();
        openFind();
        return true;
      }
      const replaceKey = (mod && ev.altKey && !ev.shiftKey && ev.code === "KeyF")
        || (!IS_MAC && ev.ctrlKey && !ev.metaKey && !ev.altKey && !ev.shiftKey && key === "h");
      if (replaceKey && canEdit && getMode() !== "reading") {
        ev.preventDefault();
        openReplace();
        return true;
      }
      if (ev.key === "F3" || (mod && !ev.altKey && (ev.code === "KeyG" || key === "g"))) {
        ev.preventDefault();
        step(ev.shiftKey ? -1 : 1);
        return true;
      }
      return false;
    },
    destroy() {
      clearTimeout(readingTimer);
      if (resize) resize.disconnect();
      document.documentElement.classList.remove("ms-finding");
      document.documentElement.style.removeProperty("--ms-find-h");
    },
  };
}
