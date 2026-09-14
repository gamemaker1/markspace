/**
 * Markspace note editor, built on CodeMirror 6. The document is the exact
 * markdown text. Live preview hides markup away from the cursor and renders
 * checkboxes, bullets, rules, images, wikilink pills and code block headers
 * as widgets. Source mode shows plain highlighted markdown, and reading mode
 * uses markdown.js. Writing saves itself through the sync engine.
 *
 * The text transforms at the top of the file are pure functions over
 * (text, selectionStart, selectionEnd) and return
 * {text, selectionStart, selectionEnd}, or null when the default key
 * handling should run. The editor applies them to CodeMirror as one change,
 * and node tests import them directly.
 */

import {
  EditorState, StateField, StateEffect, Compartment, Prec, Transaction, Annotation,
  EditorView, Decoration, ViewPlugin, WidgetType, keymap, placeholder, drawSelection, dropCursor, showTooltip, tooltips,
  highlightActiveLine, history, historyKeymap, defaultKeymap, undo, redo, syntaxTree, syntaxHighlighting, HighlightStyle,
  autocompletion, startCompletion, tags, markdownSupport,
} from "/static/vendor/codemirror.js";
import { createFind, FIND_HINT } from "/static/js/find.js";
import {
  openComments, openShare, openHistory, openAI, accessRank,
  createCommentSource, loadAnchors, groupThreads,
} from "/static/js/panels.js";
import {
  h, icon, button, iconButton, segmentedControl, announce, banner, inlineMessage, anchoredOverlay, openMenu, undoable,
  progressBar, copyFeedback, spinner, fileIconName, formatBytes, NARROW_QUERY,
} from "/static/js/ui.js";
import {
  trackedChanges, trackedField, hasChanges, startChange, setSuggestions as drawStoredSuggestions, reviewActions, reviewState,
  refreshTracked, listChanges, revealHunk,
} from "/static/js/ai/tracked.js";
import { askBar, askBarField, openAskBar, closeAskBar } from "/static/js/ai/askbar.js";
import { imageActions, imageKbarActions, selectedImage, selectImageAt, refreshImageActions, stopAllGenerations, startImageGeneration } from "/static/js/ai/imageactions.js";
import { aiCapabilities, showAiOffline, fileIdFromUrl } from "/static/js/ai/common.js";

/* ---------- Pure text helpers ---------- */

/**
 * Returns the start and end offsets of the line containing pos.
 * @param {string} text
 * @param {number} pos
 * @returns {{start: number, end: number}}
 */
export function lineBounds(text, pos) {
  const start = pos > 0 ? text.lastIndexOf("\n", pos - 1) + 1 : 0;
  let end = text.indexOf("\n", pos);
  if (end < 0) end = text.length;
  return { start, end };
}

/**
 * Describes the line holding the caret: zero-based index, bounds and column.
 * @param {string} text
 * @param {number} pos
 * @returns {{index: number, start: number, end: number, column: number}}
 */
export function caretLine(text, pos) {
  const { start, end } = lineBounds(text, pos);
  let index = 0;
  for (let i = text.indexOf("\n"); i >= 0 && i < start; i = text.indexOf("\n", i + 1)) index++;
  return { index, start, end, column: pos - start };
}

/**
 * Computes the scrollTop that puts a caret line at the vertical centre of a
 * scroll viewport, clamped to the scrollable range.
 * @param {{caretTop: number, lineHeight: number, viewportHeight: number, maxScroll: number}} m
 * @returns {number}
 */
export function typewriterScrollTop({ caretTop, lineHeight, viewportHeight, maxScroll }) {
  const target = Math.round(caretTop + lineHeight / 2 - viewportHeight / 2);
  return Math.max(0, Math.min(Math.max(0, maxScroll), target));
}

/**
 * Reports whether lineStart sits inside an open fenced code block.
 * @param {string} text
 * @param {number} lineStart
 * @returns {boolean}
 */
export function insideFence(text, lineStart) {
  const head = text.slice(0, lineStart);
  const re = /^ {0,3}(```|~~~)/gm;
  let open = false;
  while (re.exec(head)) open = !open;
  return open;
}

const LIST_LINE = /^([ \t]*)(?:([-*+])|(\d{1,9})([.)]))([ \t]+)(\[[ xX]\](?:[ \t]+|$))?/;
const QUOTE_LINE = /^([ \t]*>(?:[ \t]?>)*[ \t]?)/;

/**
 * Parses a list or quote prefix. Returns null for other lines.
 * @param {string} line
 * @returns {null|{kind: "bullet"|"number"|"task"|"quote", indent: string, bullet: string, number: number|null, digits: string, delim: string, space: string, checkbox: string, prefix: string, prefixLen: number, content: string}}
 */
export function parseListLine(line) {
  const m = LIST_LINE.exec(line);
  if (m) {
    const number = m[3] !== undefined ? parseInt(m[3], 10) : null;
    return {
      kind: m[6] ? "task" : number !== null ? "number" : "bullet",
      indent: m[1], bullet: m[2] || "", number, digits: m[3] || "", delim: m[4] || "",
      space: m[5], checkbox: m[6] || "", prefix: m[0], prefixLen: m[0].length, content: line.slice(m[0].length),
    };
  }
  const q = QUOTE_LINE.exec(line);
  if (q) {
    return {
      kind: "quote", indent: "", bullet: "", number: null, digits: "", delim: "", space: "", checkbox: "",
      prefix: q[1], prefixLen: q[1].length, content: line.slice(q[1].length),
    };
  }
  return null;
}

function leadingWidth(line) {
  return /^[ \t]*/.exec(line)[0].length;
}

/** Renumbers ordered items at one indent, from the line starting at pos. */
function renumberFrom(text, pos, indent, next) {
  let out = text;
  while (pos < out.length) {
    const { end } = lineBounds(out, pos);
    const line = out.slice(pos, end);
    if (line.trim() === "") break;
    const info = parseListLine(line);
    if (info && info.number !== null && info.indent === indent) {
      const num = String(next++);
      out = out.slice(0, pos + indent.length) + num + out.slice(pos + indent.length + info.digits.length);
    } else if (leadingWidth(line) <= indent.length) {
      break;
    }
    const nl = out.indexOf("\n", pos);
    if (nl < 0) break;
    pos = nl + 1;
  }
  return out;
}

/**
 * Handles Enter on a list, task or quote line. Continues the list with the
 * next marker and renumbers ordered lists. Enter on an empty item ends the
 * list, or moves a nested item up to its parent level. Returns null when the
 * browser should insert a plain newline.
 * @param {string} text
 * @param {number} s
 * @param {number} e
 * @returns {null|{text: string, selectionStart: number, selectionEnd: number}}
 */
export function continueList(text, s, e) {
  const { start, end } = lineBounds(text, s);
  if (e > end) return null;
  if (insideFence(text, start)) return null;
  const line = text.slice(start, end);
  const info = parseListLine(line);
  if (!info || s - start < info.prefixLen) return null;
  const rest = text.slice(start + info.prefixLen, s) + text.slice(e, end);

  if (rest.trim() === "") {
    if (info.kind !== "quote" && info.indent.length > 0) {
      // Move the empty nested item to the indent of its parent item.
      let parentIndent = "";
      let pos = start;
      while (pos > 0) {
        const lb = lineBounds(text, pos - 1);
        const prev = text.slice(lb.start, lb.end);
        const pi = parseListLine(prev);
        if (pi && pi.kind !== "quote" && pi.indent.length < info.indent.length) { parentIndent = pi.indent; break; }
        if (prev.trim() === "") break;
        pos = lb.start;
      }
      const newLine = parentIndent + info.prefix.slice(info.indent.length);
      const out = text.slice(0, start) + newLine + text.slice(end);
      const caret = start + newLine.length;
      return { text: out, selectionStart: caret, selectionEnd: caret };
    }
    const out = text.slice(0, start) + text.slice(end);
    return { text: out, selectionStart: start, selectionEnd: start };
  }

  let prefix;
  let renumber = false;
  if (info.kind === "quote") {
    prefix = info.prefix;
  } else {
    let marker = info.bullet;
    if (info.number !== null) {
      const nextLine = end < text.length ? text.slice(end + 1, lineBounds(text, end + 1).end) : "";
      const ni = parseListLine(nextLine);
      const lazy = ni && ni.number !== null && ni.indent === info.indent && ni.number === info.number;
      const n = lazy ? info.number : info.number + 1;
      marker = String(n) + info.delim;
      renumber = !lazy;
    }
    prefix = info.indent + marker + (info.number !== null ? " " : info.space) + (info.checkbox ? "[ ] " : "");
  }
  let out = text.slice(0, s) + "\n" + prefix + text.slice(e);
  const caret = s + 1 + prefix.length;
  if (renumber) {
    const nl = out.indexOf("\n", caret);
    if (nl >= 0) out = renumberFrom(out, nl + 1, info.indent, info.number + 2);
  }
  return { text: out, selectionStart: caret, selectionEnd: caret };
}

/**
 * Indents or outdents the lines touched by the selection. A collapsed caret
 * on a line that is not a list item returns null so Tab keeps moving focus.
 * Bullets move by two spaces, ordered items by their marker width.
 * @param {string} text
 * @param {number} s
 * @param {number} e
 * @param {boolean} [outdent]
 * @returns {null|{text: string, selectionStart: number, selectionEnd: number}}
 */
export function indentLines(text, s, e, outdent = false) {
  const first = lineBounds(text, s).start;
  const endAnchor = e > s && text[e - 1] === "\n" ? e - 1 : e;
  const last = lineBounds(text, Math.max(endAnchor, s)).end;
  const lines = text.slice(first, last).split("\n");
  if (lines.length === 1 && s === e) {
    const info = parseListLine(lines[0]);
    if (!info || info.kind === "quote") return null;
  }
  let delta0 = 0;
  let total = 0;
  const out = lines.map((line, i) => {
    if (line.trim() === "" && lines.length > 1) return line;
    const info = parseListLine(line);
    const unit = info && info.number !== null ? info.digits.length + info.delim.length + 1 : 2;
    let next;
    if (!outdent) next = " ".repeat(unit) + line;
    else if (line.startsWith("\t")) next = line.slice(1);
    else next = line.slice(Math.min(/^ */.exec(line)[0].length, unit));
    const d = next.length - line.length;
    if (i === 0) delta0 = d;
    total += d;
    return next;
  });
  if (total === 0) return { text, selectionStart: s, selectionEnd: e };
  const joined = out.join("\n");
  const newText = text.slice(0, first) + joined + text.slice(last);
  const ns = Math.max(first, s + delta0);
  const ne = s === e ? ns : Math.max(ns, e + total);
  return { text: newText, selectionStart: ns, selectionEnd: ne };
}

const WORD_CHAR = /[\p{L}\p{N}]/u;

function isWordChar(c) {
  return c !== undefined && WORD_CHAR.test(c);
}

/**
 * Toggles an inline marker (**, _ or `) around the selection. Removes the
 * marker when the selection is already wrapped. A caret inside a word wraps
 * the word; elsewhere it inserts an empty pair.
 * @param {string} text
 * @param {number} s
 * @param {number} e
 * @param {string} marker
 * @returns {{text: string, selectionStart: number, selectionEnd: number}}
 */
export function toggleInline(text, s, e, marker) {
  while (s < e && /\s/.test(text[s])) s++;
  while (e > s && /\s/.test(text[e - 1])) e--;
  const alts = marker === "_" ? ["_", "*"] : marker === "*" ? ["*", "_"] : [marker];
  const exact = (mk, from, to) => mk.length > 1 || (text[from - 1] !== mk && text[to] !== mk);
  for (const mk of alts) {
    const l = mk.length;
    if (s >= l && text.slice(s - l, s) === mk && text.slice(e, e + l) === mk && exact(mk, s - l, e + l)) {
      return { text: text.slice(0, s - l) + text.slice(s, e) + text.slice(e + l), selectionStart: s - l, selectionEnd: e - l };
    }
  }
  const sel = text.slice(s, e);
  for (const mk of alts) {
    const l = mk.length;
    if (sel.length >= 2 * l + 1 && sel.startsWith(mk) && sel.endsWith(mk) && exact(mk, s, e)) {
      const inner = sel.slice(l, sel.length - l);
      return { text: text.slice(0, s) + inner + text.slice(e), selectionStart: s, selectionEnd: s + inner.length };
    }
  }
  if (s === e) {
    if (isWordChar(text[s - 1]) && isWordChar(text[s])) {
      while (s > 0 && isWordChar(text[s - 1])) s--;
      while (e < text.length && isWordChar(text[e])) e++;
    } else {
      const out = text.slice(0, s) + marker + marker + text.slice(e);
      return { text: out, selectionStart: s + marker.length, selectionEnd: s + marker.length };
    }
  }
  const m = marker.length;
  const out = text.slice(0, s) + marker + text.slice(s, e) + marker + text.slice(e);
  return { text: out, selectionStart: s + m, selectionEnd: e + m };
}

/**
 * Reports whether a string is a single http, https or mailto URL.
 * @param {string} s
 * @returns {boolean}
 */
export function isUrl(s) {
  return /^(https?:\/\/[^\s<>]+|mailto:[^\s<>]+)$/i.test(s);
}

/**
 * Toggles a markdown link. Inside an existing link it unwraps to the label.
 * A selected URL becomes the link target; other selected text becomes the
 * label with a placeholder URL selected.
 * @param {string} text
 * @param {number} s
 * @param {number} e
 * @returns {{text: string, selectionStart: number, selectionEnd: number}}
 */
export function toggleLink(text, s, e) {
  const { start, end } = lineBounds(text, s);
  if (e <= end) {
    const re = /(!?)\[([^\]\n]*)\]\(([^)\s]*)(?:\s+"[^"\n]*")?\)/g;
    const line = text.slice(start, end);
    let m;
    while ((m = re.exec(line))) {
      const ms = start + m.index;
      const me = ms + m[0].length;
      if (!m[1] && s >= ms && e <= me) {
        const label = m[2];
        return { text: text.slice(0, ms) + label + text.slice(me), selectionStart: ms, selectionEnd: ms + label.length };
      }
    }
  }
  const sel = text.slice(s, e);
  if (sel && isUrl(sel.trim())) {
    const out = text.slice(0, s) + "[](" + sel.trim() + ")" + text.slice(e);
    return { text: out, selectionStart: s + 1, selectionEnd: s + 1 };
  }
  const label = sel || "link text";
  const insert = "[" + label + "](https://)";
  const out = text.slice(0, s) + insert + text.slice(e);
  if (!sel) return { text: out, selectionStart: s + 1, selectionEnd: s + 1 + label.length };
  const urlStart = s + label.length + 3;
  return { text: out, selectionStart: urlStart, selectionEnd: urlStart + 8 };
}

/**
 * Toggles code: a fenced block for a multi-line selection, inline backticks
 * otherwise.
 * @param {string} text
 * @param {number} s
 * @param {number} e
 * @returns {{text: string, selectionStart: number, selectionEnd: number}}
 */
export function toggleCode(text, s, e) {
  const sel = text.slice(s, e);
  if (!sel.includes("\n")) return toggleInline(text, s, e, "`");
  const first = lineBounds(text, s).start;
  const last = lineBounds(text, e > s && text[e - 1] === "\n" ? e - 1 : e).end;
  const block = text.slice(first, last);
  const lines = block.split("\n");
  if (lines.length >= 2 && /^```/.test(lines[0]) && /^```\s*$/.test(lines[lines.length - 1])) {
    const inner = lines.slice(1, -1).join("\n");
    return { text: text.slice(0, first) + inner + text.slice(last), selectionStart: first, selectionEnd: first + inner.length };
  }
  const wrapped = "```\n" + block + "\n```";
  return { text: text.slice(0, first) + wrapped + text.slice(last), selectionStart: first + 4, selectionEnd: first + 4 + block.length };
}

const PAIRS = { "*": "*", _: "_", "`": "`", "(": ")", "[": "]", "\"": "\"" };

/**
 * Wraps a non-empty selection in a pairing character. Returns null for a
 * collapsed selection or a character that does not pair.
 * @param {string} text
 * @param {number} s
 * @param {number} e
 * @param {string} ch
 * @returns {null|{text: string, selectionStart: number, selectionEnd: number}}
 */
export function wrapPair(text, s, e, ch) {
  if (s === e || !(ch in PAIRS)) return null;
  const out = text.slice(0, s) + ch + text.slice(s, e) + PAIRS[ch] + text.slice(e);
  return { text: out, selectionStart: s + 1, selectionEnd: e + 1 };
}

/**
 * Turns a URL pasted over a selection into a link. Returns null when the
 * paste should run as usual.
 * @param {string} text
 * @param {number} s
 * @param {number} e
 * @param {string} pasted
 * @returns {null|{text: string, selectionStart: number, selectionEnd: number}}
 */
export function linkFromPaste(text, s, e, pasted) {
  const url = (pasted || "").trim();
  const sel = text.slice(s, e);
  if (s === e || !isUrl(url) || sel.includes("\n") || isUrl(sel.trim())) return null;
  const insert = "[" + sel + "](" + url + ")";
  const out = text.slice(0, s) + insert + text.slice(e);
  const caret = s + insert.length;
  return { text: out, selectionStart: caret, selectionEnd: caret };
}

const BLOCK_PREFIX = /^([ \t]*)(#{1,6}[ \t]+|(?:[-*+]|\d{1,9}[.)])[ \t]+\[[ xX]\](?:[ \t]+|$)|[-*+][ \t]+|\d{1,9}[.)][ \t]+|>[ \t]?)?/;

function blockKind(prefix) {
  if (!prefix) return "paragraph";
  const hm = /^(#{1,6})[ \t]/.exec(prefix);
  if (hm) return "h" + hm[1].length;
  if (/\[[ xX]\]/.test(prefix)) return "task";
  if (/^[-*+]/.test(prefix)) return "bullet";
  if (/^\d/.test(prefix)) return "number";
  if (prefix.startsWith(">")) return "quote";
  return "paragraph";
}

function blockPrefix(kind, n) {
  switch (kind) {
    case "h1": return "# ";
    case "h2": return "## ";
    case "h3": return "### ";
    case "bullet": return "- ";
    case "number": return n + ". ";
    case "task": return "- [ ] ";
    case "quote": return "> ";
    default: return "";
  }
}

/**
 * Sets the block type of the selected lines: h1, h2, h3, bullet, number,
 * task, quote or paragraph. Applying the type every line already has turns
 * the lines back into paragraphs.
 * @param {string} text
 * @param {number} s
 * @param {number} e
 * @param {string} kind
 * @param {{toggle?: boolean}} [opts]
 * @returns {{text: string, selectionStart: number, selectionEnd: number}}
 */
export function setBlock(text, s, e, kind, { toggle = true } = {}) {
  const first = lineBounds(text, s).start;
  const endAnchor = e > s && text[e - 1] === "\n" ? e - 1 : e;
  const last = lineBounds(text, Math.max(endAnchor, s)).end;
  const lines = text.slice(first, last).split("\n");
  const parsed = lines.map((l) => {
    const m = BLOCK_PREFIX.exec(l);
    const prefix = m[2] || "";
    return { indent: m[1], prefix, kind: blockKind(prefix), body: l.slice(m[0].length) };
  });
  const counted = lines.length > 1 ? parsed.filter((p) => p.body.trim() !== "" || p.prefix) : parsed;
  const all = counted.length > 0 && counted.every((p) => p.kind === kind);
  const target = toggle && all ? "paragraph" : kind;
  let n = 0;
  const out = parsed.map((p, i) => {
    if (lines.length > 1 && p.body.trim() === "" && !p.prefix) return lines[i];
    const pre = blockPrefix(target, ++n);
    const keepIndent = target === "bullet" || target === "number" || target === "task" || target === "paragraph";
    return (keepIndent ? p.indent : "") + pre + p.body;
  });
  const joined = out.join("\n");
  const newText = text.slice(0, first) + joined + text.slice(last);
  if (lines.length === 1) {
    const delta = joined.length - lines[0].length;
    const bodyStart = first + joined.length - parsed[0].body.length;
    const ns = Math.min(first + joined.length, Math.max(bodyStart, s + delta));
    const ne = s === e ? ns : Math.max(ns, e + delta);
    return { text: newText, selectionStart: ns, selectionEnd: ne };
  }
  return { text: newText, selectionStart: first, selectionEnd: first + joined.length };
}

/**
 * Cycles the caret line through heading 1, 2, 3 and back to a paragraph.
 * @param {string} text
 * @param {number} s
 * @param {number} e
 * @returns {{text: string, selectionStart: number, selectionEnd: number}}
 */
export function cycleHeading(text, s, e) {
  const { start, end } = lineBounds(text, s);
  const m = BLOCK_PREFIX.exec(text.slice(start, end));
  const kind = blockKind(m[2] || "");
  const next = kind === "h1" ? "h2" : kind === "h2" ? "h3" : kind === "h3" ? "paragraph" : "h1";
  return setBlock(text, s, e, next, { toggle: false });
}

/**
 * Inserts a block on its own lines at the caret, separated from surrounding
 * text by a blank line. selStart and selEnd are offsets inside block.
 * @param {string} text
 * @param {number} s
 * @param {number} e
 * @param {string} block
 * @param {number} [selStart]
 * @param {number} [selEnd]
 * @returns {{text: string, selectionStart: number, selectionEnd: number}}
 */
export function insertBlock(text, s, e, block, selStart = block.length, selEnd = selStart) {
  const ls = lineBounds(text, s).start;
  let before = text.slice(0, s);
  if (text.slice(ls, s).trim() === "") before = text.slice(0, ls);
  let after = text.slice(e);
  const le = lineBounds(text, e).end;
  if (text.slice(e, le).trim() === "") after = text.slice(le);
  let lead = "";
  if (before !== "") {
    if (!before.endsWith("\n")) lead = "\n\n";
    else if (!before.endsWith("\n\n") && before.trim() !== "") lead = "\n";
  }
  let trail = "";
  if (after !== "") {
    if (!after.startsWith("\n")) trail = "\n\n";
    else if (!after.startsWith("\n\n")) trail = "\n";
  }
  const out = before + lead + block + trail + after;
  const base = before.length + lead.length;
  return { text: out, selectionStart: base + selStart, selectionEnd: base + selEnd };
}

/**
 * Finds a slash command being typed at the caret: a "/" at the start of a
 * line or after whitespace, followed by letters. Returns null otherwise.
 * @param {string} text
 * @param {number} caret
 * @returns {null|{start: number, query: string}}
 */
export function slashTrigger(text, caret) {
  const { start } = lineBounds(text, caret);
  const before = text.slice(start, caret);
  const m = /(^|\s)\/([\p{L}\p{N}-]{0,24})$/u.exec(before);
  if (!m) return null;
  if (insideFence(text, start)) return null;
  return { start: start + m.index + m[1].length, query: m[2] };
}

/**
 * Finds a wikilink being typed at the caret, after "[[".
 * @param {string} text
 * @param {number} caret
 * @returns {null|{start: number, query: string}}
 */
export function wikiTrigger(text, caret) {
  const { start } = lineBounds(text, caret);
  const before = text.slice(start, caret);
  const m = /\[\[([^[\]\n|#]{0,80})$/.exec(before);
  if (!m) return null;
  return { start: start + m.index, query: m[1] };
}

/**
 * Replaces the typed "[[query" (and an auto-typed "]]") with [[title]].
 * @param {string} text
 * @param {number} start offset of "[["
 * @param {number} caret
 * @param {string} title
 * @returns {{text: string, selectionStart: number, selectionEnd: number}}
 */
export function completeWikilink(text, start, caret, title) {
  const tail = text.slice(caret, caret + 2) === "]]" ? 2 : 0;
  const insert = "[[" + title + "]]";
  const out = text.slice(0, start) + insert + text.slice(caret + tail);
  const pos = start + insert.length;
  return { text: out, selectionStart: pos, selectionEnd: pos };
}

/**
 * Ranks wikilink candidates: exact title, title prefix, word prefix, title
 * substring, then the rest in their given order. Drops deleted notes, the
 * excluded id and duplicates.
 * @param {object[]} notes
 * @param {string} query
 * @param {{excludeId?: string, limit?: number}} [opts]
 * @returns {object[]}
 */
export function rankWikiMatches(notes, query, { excludeId = "", limit = 8 } = {}) {
  const q = query.trim().toLowerCase();
  const seen = new Set();
  const scored = [];
  notes.forEach((n, i) => {
    if (!n || n.deleted || n.id === excludeId || seen.has(n.id)) return;
    seen.add(n.id);
    const title = String(n.title || "").toLowerCase();
    let score = 0;
    if (q) {
      if (title === q) score = 4;
      else if (title.startsWith(q)) score = 3;
      else if (title.split(/[^\p{L}\p{N}]+/u).some((w) => w && w.startsWith(q))) score = 2;
      else if (title.includes(q)) score = 1;
    }
    scored.push({ n, score, i });
  });
  scored.sort((a, b) => b.score - a.score || a.i - b.i);
  return scored.slice(0, limit).map((x) => x.n);
}

/** Slash menu entries. online entries need a connection. */
export const SLASH_ITEMS = [
  { id: "h1", icon: "heading", label: "Heading 1", hint: "#", keywords: "title h1 big" },
  { id: "h2", icon: "heading", label: "Heading 2", hint: "##", keywords: "subtitle h2" },
  { id: "h3", icon: "heading", label: "Heading 3", hint: "###", keywords: "h3 small" },
  { id: "bullet", icon: "list-unordered", label: "Bullet list", hint: "-", keywords: "unordered ul list" },
  { id: "number", icon: "list-ordered", label: "Numbered list", hint: "1.", keywords: "ordered ol list" },
  { id: "task", icon: "tasklist", label: "Task", hint: "[ ]", keywords: "todo checkbox check list" },
  { id: "quote", icon: "quote", label: "Quote", hint: ">", keywords: "blockquote citation" },
  { id: "code", icon: "code", label: "Code block", hint: "```", keywords: "fence pre snippet" },
  { id: "table", icon: "table", label: "Table", hint: "|", keywords: "grid columns rows" },
  { id: "divider", icon: "horizontal-rule", label: "Divider", hint: "---", keywords: "rule hr separator line" },
  { id: "wikilink", icon: "link", label: "Link to note or file", hint: "[[ ]]", keywords: "wiki wikilink reference page file" },
  { id: "image", icon: "image", label: "Image", hint: "Upload", keywords: "picture photo upload" },
  { id: "file", icon: "paperclip", label: "File", hint: "Upload", keywords: "attachment upload document" },
  { id: "ai", icon: "sparkle", label: "Ask AI", hint: "AI", keywords: "assistant ai ask write continue summarize improve translate", online: true },
  { id: "image-gen", icon: "sparkle", label: "Generate image", hint: "AI", keywords: "ai image picture draw generate", online: true },
];

/**
 * Filters and ranks slash menu entries: label prefix, word prefix, then
 * substring. Offline, entries that need the network stay in the list marked
 * disabled, and choosing one shows "AI needs a connection".
 * @param {object[]} items
 * @param {string} query
 * @param {{online?: boolean}} [opts]
 * @returns {object[]}
 */
export function filterSlashItems(items, query, { online = true } = {}) {
  const q = query.trim().toLowerCase();
  const scored = [];
  items.forEach((it0, i) => {
    const it = it0.online && !online ? { ...it0, disabled: true } : it0;
    if (!q) { scored.push({ it, score: 0, i }); return; }
    const label = it.label.toLowerCase();
    const keywords = (it.keywords || "").toLowerCase();
    const words = (label + " " + keywords).split(/[^\p{L}\p{N}]+/u).filter(Boolean);
    let score = -1;
    if (label.startsWith(q)) score = 3;
    else if (words.some((w) => w.startsWith(q))) score = 2;
    else if (label.includes(q) || keywords.includes(q)) score = 1;
    if (score >= 0) scored.push({ it, score, i });
  });
  scored.sort((a, b) => b.score - a.score || a.i - b.i);
  return scored.map((x) => x.it);
}

/**
 * Maps ranked slash items to completion options. Sections group the AI
 * entries only while the query is empty. CodeMirror draws options without a
 * section before sectioned ones, so a section during typing would move a
 * better match such as "Ask AI" below a weaker one such as "Task".
 * @param {object[]} items ranked output of filterSlashItems
 * @param {string} query
 * @returns {{item: object, label: string, detail: string, section: string|undefined}[]}
 */
export function slashCompletionOptions(items, query) {
  const browsing = !String(query || "").trim();
  return items.map((item) => ({
    item,
    label: item.label,
    detail: item.disabled ? "Needs a connection" : item.hint,
    section: browsing && item.online ? "AI" : undefined,
  }));
}

/**
 * Counts words and estimates reading time at wpm words per minute. Link
 * targets and markdown punctuation are ignored; each CJK character counts as
 * a word.
 * @param {string} text
 * @param {number} [wpm]
 * @returns {{words: number, minutes: number}}
 */
export function wordStats(text, wpm = 230) {
  const clean = String(text || "")
    .replace(/\]\([^)]*\)/g, "]")
    .replace(/<[^>]+>/g, " ");
  const cjk = clean.match(/[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}]/gu);
  const rest = clean.replace(/[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}]/gu, " ");
  const words = (rest.match(/[\p{L}\p{N}][\p{L}\p{N}'’_-]*/gu) || []).length + (cjk ? cjk.length : 0);
  return { words, minutes: words ? Math.max(1, Math.round(words / wpm)) : 0 };
}

/**
 * Finds the smallest single replacement that turns a into b.
 * @param {string} a
 * @param {string} b
 * @returns {{start: number, end: number, insert: string}}
 */
export function diffRange(a, b) {
  let p = 0;
  const max = Math.min(a.length, b.length);
  while (p < max && a.charCodeAt(p) === b.charCodeAt(p)) p++;
  let ea = a.length;
  let eb = b.length;
  while (ea > p && eb > p && a.charCodeAt(ea - 1) === b.charCodeAt(eb - 1)) { ea--; eb--; }
  // Keep surrogate pairs whole.
  if (p > 0 && p < a.length && isLowSurrogate(a.charCodeAt(p)) && isHighSurrogate(a.charCodeAt(p - 1))) p--;
  if (ea < a.length && isLowSurrogate(a.charCodeAt(ea)) && ea > p) { ea++; eb++; }
  return { start: p, end: ea, insert: b.slice(p, eb) };
}

function isHighSurrogate(c) { return c >= 0xd800 && c <= 0xdbff; }
function isLowSurrogate(c) { return c >= 0xdc00 && c <= 0xdfff; }

/**
 * Moves a caret through a text replacement so it stays on the same content.
 * @param {number} pos
 * @param {{start: number, end: number, insert: string}} d
 * @returns {number}
 */
export function mapCaret(pos, d) {
  if (pos <= d.start) return pos;
  if (pos >= d.end) return pos + d.insert.length - (d.end - d.start);
  return d.start + Math.min(d.insert.length, pos - d.start);
}

/**
 * Builds the markdown for an uploaded file: an image embed for images, a
 * link otherwise.
 * @param {string} name
 * @param {string} url
 * @param {boolean} image
 * @returns {string}
 */
export function attachmentMarkdown(name, url, image) {
  const label = String(name || "file").replace(/[[\]\n]/g, "");
  const safe = String(url).replace(/ /g, "%20").replace(/\(/g, "%28").replace(/\)/g, "%29");
  return (image ? "!" : "") + "[" + label + "](" + safe + ")";
}

/* ---------- Preferences ---------- */

const PREF = "markspace.editor.";

function readPref(key, fallback) {
  try {
    const v = localStorage.getItem(PREF + key);
    return v === null ? fallback : v;
  } catch {
    return fallback;
  }
}

function writePref(key, value) {
  try { localStorage.setItem(PREF + key, String(value)); } catch { /* storage unavailable */ }
}

/**
 * Finds where a stored insertion goes. An insertion anchor has no exact text,
 * so its place is where the stored prefix and suffix meet in the text,
 * nearest the stored start. Returns null when they no longer meet.
 * @param {string} text
 * @param {{exact?: string, prefix?: string, suffix?: string, start?: number}} anchor
 * @returns {{start: number, end: number}|null}
 */
export function insertionPoint(text, anchor) {
  const a = anchor || {};
  if (a.exact) return null;
  const prefix = String(a.prefix || "");
  const suffix = String(a.suffix || "");
  if (!prefix && !suffix) return null;
  const joined = prefix + suffix;
  let best = -1;
  for (let i = text.indexOf(joined); i >= 0; i = text.indexOf(joined, i + 1)) {
    const at = i + prefix.length;
    if (best < 0 || Math.abs(at - (a.start || 0)) < Math.abs(best - (a.start || 0))) best = at;
  }
  return best < 0 ? null : { start: best, end: best };
}

/**
 * Returns the heading and text of the section around pos: from the nearest
 * heading above down to the next heading of the same or a higher level.
 * @param {string} text
 * @param {number} pos
 * @returns {{heading: string, level: number, from: number, to: number, text: string}}
 */
export function sectionAt(text, pos) {
  const re = /^(#{1,6})[ \t]+(.*)$/gm;
  const heads = [];
  let m;
  while ((m = re.exec(text))) {
    if (!insideFence(text, m.index)) heads.push({ level: m[1].length, heading: m[2].trim(), from: m.index });
  }
  let current = null;
  for (const hd of heads) if (hd.from <= pos) current = hd;
  if (!current) {
    const to = heads.length ? heads[0].from : text.length;
    return { heading: "", level: 0, from: 0, to, text: text.slice(0, to) };
  }
  const next = heads.find((hd) => hd.from > current.from && hd.level <= current.level);
  const to = next ? next.from : text.length;
  return { heading: current.heading, level: current.level, from: current.from, to, text: text.slice(current.from, to) };
}

/**
 * Finds the markdown link, image or wikilink whose source span contains pos.
 * @param {string} text
 * @param {number} pos
 * @returns {null|{kind: "link"|"image"|"wikilink", from: number, to: number, url: string, label: string}}
 */
export function linkAt(text, pos) {
  const { start, end } = lineBounds(text, pos);
  const line = text.slice(start, end);
  const re = /(!?)\[([^\]\n]*)\]\(([^)\s]*)(?:\s+"[^"\n]*")?\)|\[\[([^[\]\n|]+)(?:\|([^[\]\n]+))?\]\]/g;
  let m;
  while ((m = re.exec(line))) {
    const from = start + m.index;
    const to = from + m[0].length;
    if (pos < from || pos > to) continue;
    if (m[4] !== undefined) return { kind: "wikilink", from, to, url: m[4].trim(), label: (m[5] || m[4]).trim() };
    return { kind: m[1] ? "image" : "link", from, to, url: m[3], label: m[2] };
  }
  return null;
}

/**
 * Replaces the alt text of the image whose source starts at from.
 * @param {string} text
 * @param {number} from offset of "!["
 * @param {string} alt
 * @returns {null|{from: number, to: number, insert: string}} a single change.
 */
export function altTextChange(text, from, alt) {
  const m = /^!\[([^\]\n]*)\]/.exec(text.slice(from));
  if (!m) return null;
  const clean = String(alt).replace(/[[\]\n]/g, " ").replace(/\s+/g, " ").trim();
  return { from: from + 2, to: from + 2 + m[1].length, insert: clean };
}

/* ---------- Markdown parser extension: wikilinks ---------- */

/** @lezer/markdown extension that parses [[target]] and [[target|label]]. */
const WikiLinkExtension = {
  defineNodes: [{ name: "WikiLink" }, { name: "WikiLinkMark" }],
  parseInline: [{
    // An embed, ![[target]]. The Image parser would take "![" first, so this runs before it. The WikiLink node
    // starts after the "!", and live preview reads the "!" in front of it.
    name: "WikiEmbed",
    before: "Image",
    parse(cx, next, pos) {
      if (next !== 33 || cx.char(pos + 1) !== 91 || cx.char(pos + 2) !== 91) return -1;
      const rest = cx.slice(pos + 3, cx.end);
      const close = rest.indexOf("]]");
      if (close <= 0) return -1;
      const inner = rest.slice(0, close);
      if (/[[\]\n]/.test(inner)) return -1;
      const end = pos + 3 + close + 2;
      return cx.addElement(cx.elt("WikiLink", pos + 1, end, [
        cx.elt("WikiLinkMark", pos + 1, pos + 3),
        cx.elt("WikiLinkMark", end - 2, end),
      ]));
    },
  }, {
    name: "WikiLink",
    before: "Link",
    parse(cx, next, pos) {
      if (next !== 91 || cx.char(pos + 1) !== 91) return -1;
      const rest = cx.slice(pos + 2, cx.end);
      const close = rest.indexOf("]]");
      if (close <= 0) return -1;
      const inner = rest.slice(0, close);
      if (/[[\]\n]/.test(inner)) return -1;
      const end = pos + 2 + close + 2;
      return cx.addElement(cx.elt("WikiLink", pos, end, [
        cx.elt("WikiLinkMark", pos, pos + 2),
        cx.elt("WikiLinkMark", end - 2, end),
      ]));
    },
  }],
};

/* ---------- Highlighting ---------- */

const highlightStyle = HighlightStyle.define([
  { tag: tags.heading1, class: "cm-ms-t-h1" },
  { tag: tags.heading2, class: "cm-ms-t-h2" },
  { tag: [tags.heading3, tags.heading4, tags.heading5, tags.heading6], class: "cm-ms-t-h3" },
  { tag: tags.strong, fontWeight: "700" },
  { tag: tags.emphasis, fontStyle: "italic" },
  { tag: tags.strikethrough, textDecoration: "line-through" },
  { tag: [tags.link, tags.url], class: "cm-ms-t-link" },
  { tag: tags.monospace, class: "cm-ms-t-mono" },
  { tag: [tags.processingInstruction, tags.meta, tags.contentSeparator], class: "cm-ms-t-mark" },
  { tag: tags.quote, class: "cm-ms-t-quote" },
  { tag: [tags.keyword, tags.controlKeyword, tags.moduleKeyword, tags.operatorKeyword], class: "cm-ms-t-keyword" },
  { tag: [tags.string, tags.special(tags.string), tags.regexp], class: "cm-ms-t-string" },
  { tag: [tags.comment, tags.lineComment, tags.blockComment], class: "cm-ms-t-comment" },
  { tag: [tags.number, tags.bool, tags.null, tags.atom], class: "cm-ms-t-number" },
  { tag: [tags.function(tags.variableName), tags.function(tags.propertyName)], class: "cm-ms-t-function" },
  { tag: [tags.typeName, tags.className, tags.namespace], class: "cm-ms-t-type" },
  { tag: [tags.propertyName, tags.attributeName], class: "cm-ms-t-property" },
  { tag: [tags.tagName, tags.angleBracket], class: "cm-ms-t-tag" },
]);

/* ---------- Editor state fields shared by widgets ---------- */

const setFocused = StateEffect.define();

/** Tracks whether the editor has focus, so markup is revealed only while editing. */
const focusField = StateField.define({
  create: () => false,
  update(value, tr) {
    for (const e of tr.effects) if (e.is(setFocused)) value = e.value;
    return value;
  },
});

const setUploads = StateEffect.define();

/** Asks the live preview to rebuild, for example after the link index loads. */
const refreshLive = StateEffect.define();

const setLinks = StateEffect.define();

/** Link resolution for block widgets: {resolve(target, form, embed)} over buildItemResolver, or null before it loads. */
const linksField = StateField.define({
  create: () => null,
  update(value, tr) {
    for (const e of tr.effects) if (e.is(setLinks)) value = e.value;
    return value;
  },
});

/** Upload items from sync.uploads(), by file id. */
const uploadsField = StateField.define({
  create: () => new Map(),
  update(value, tr) {
    for (const e of tr.effects) if (e.is(setUploads)) value = e.value;
    return value;
  },
});

function touches(state, from, to) {
  if (!state.field(focusField, false)) return false;
  for (const r of state.selection.ranges) if (r.from <= to && r.to >= from) return true;
  return false;
}

function lineTouches(state, pos) {
  const line = state.doc.lineAt(pos);
  return touches(state, line.from, line.to);
}

/** Returns the editor context object mountEditor attaches to the view. */
function ctxOf(view) {
  return view && view.msCtx ? view.msCtx : null;
}

/* ---------- Widget heights and phone layout helpers ---------- */

// The largest height of an image in live preview. editor.css caps .cm-ms-media img at the same value.
export const IMAGE_MAX_HEIGHT = 640;

// Vertical padding of every block widget, from .cm-ms-block in editor.css. Estimates include it, because the
// ResizeObserver records the padded box and CodeMirror measures that box once the widget is drawn.
export const BLOCK_PADDING = 20;

// Fallback height of one table row in pixels, used until a table has been drawn once.
const TABLE_ROW_ESTIMATE = 34;

// Syntax nodes the block widget scan descends into.
const BLOCK_CONTAINERS = new Set(["Document", "Paragraph", "BulletList", "OrderedList", "ListItem", "Blockquote"]);

/**
 * Hashes a string with 32-bit FNV-1a. Table widgets use it as their height cache key.
 * @param {string} s
 * @returns {string}
 */
export function hashString(s) {
  const str = String(s);
  let hash = 0x811c9dc5;
  for (let i = 0; i < str.length; i++) {
    hash ^= str.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return (hash >>> 0).toString(36);
}

/**
 * Estimates the drawn height of a markdown table from its row count.
 * @param {string} source
 * @returns {number}
 */
export function estimateTableHeight(source) {
  const delimiter = /^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$/;
  const rows = String(source).split("\n").filter((line) => line.trim() !== "" && !delimiter.test(line)).length;
  return rows * TABLE_ROW_ESTIMATE;
}

/**
 * Remembers the drawn heights of block widgets for one editor, so CodeMirror
 * can reserve their space before it draws them again. Keys are image URLs and
 * "table:" plus a hash of the table source. It also holds image file sizes
 * from the local store and the width of the content column.
 */
export class WidgetHeights {
  constructor() {
    this.heights = new Map();
    this.dims = new Map();
    this.width = 0;
    this.targets = new Map();
    this.widthTarget = null;
    this.observer = null;
  }

  /** Returns the recorded height for key, or -1. */
  get(key) {
    const v = this.heights.get(key);
    return v === undefined ? -1 : v;
  }

  /** Records a drawn height. Returns true when the stored value changed. */
  record(key, height) {
    if (!(height > 0)) return false;
    const v = Math.round(height);
    if (this.heights.get(key) === v) return false;
    this.heights.set(key, v);
    return true;
  }

  setWidth(width) {
    if (width > 0) this.width = Math.round(width);
  }

  /** Stores the pixel size of an image file under its URL, or under "id:" plus its file id. */
  setDims(key, width, height) {
    if (key && width > 0 && height > 0) this.dims.set(key, { width, height });
  }

  /** Returns {width, height} for an image, matched by URL or by file id, or null. */
  dimsFor(url, fileId = "") {
    const hit = this.dims.get(url);
    if (hit) return hit;
    return (fileId && this.dims.get("id:" + fileId)) || null;
  }

  /**
   * Returns the height to reserve for an image: the recorded one, one scaled
   * from the file size to the column and capped at IMAGE_MAX_HEIGHT, or -1.
   */
  imageEstimate(url, fileId = "") {
    const known = this.get(url);
    if (known >= 0) return known;
    const d = this.dimsFor(url, fileId);
    if (!d || !(this.width > 0)) return -1;
    return Math.round(Math.min(d.height, (this.width * d.height) / d.width, IMAGE_MAX_HEIGHT)) + BLOCK_PADDING;
  }

  /** Returns the height to reserve for a table: the recorded one, or a row-count estimate plus the block padding. */
  tableEstimate(key, source) {
    const known = this.get(key);
    return known >= 0 ? known : estimateTableHeight(source) + BLOCK_PADDING;
  }

  /** Records the height of el under key after each resize. ready() can veto a record, for example before an image loads. */
  observe(el, key, ready = null) {
    if (!this.ensureObserver()) return;
    this.targets.set(el, { key, ready });
    this.observer.observe(el);
  }

  unobserve(el) {
    if (this.observer && this.targets.delete(el)) this.observer.unobserve(el);
  }

  /** Follows the content width of el, which scales the image estimates. */
  trackWidth(el) {
    if (!this.ensureObserver()) return;
    this.widthTarget = el;
    this.observer.observe(el);
  }

  ensureObserver() {
    if (this.observer) return true;
    if (typeof ResizeObserver !== "function") return false;
    this.observer = new ResizeObserver((entries) => this.onResize(entries));
    return true;
  }

  /** Handles ResizeObserver entries. The sizes come from the entries, so nothing forces a layout. */
  onResize(entries) {
    for (const entry of entries) {
      if (entry.target === this.widthTarget) {
        const box = entry.contentBoxSize && entry.contentBoxSize[0];
        this.setWidth(box ? box.inlineSize : entry.contentRect.width);
        continue;
      }
      const meta = this.targets.get(entry.target);
      if (!meta || (meta.ready && !meta.ready())) continue;
      const box = entry.borderBoxSize && entry.borderBoxSize[0];
      this.record(meta.key, box ? box.blockSize : entry.contentRect.height);
    }
  }

  disconnect() {
    if (this.observer) this.observer.disconnect();
    this.observer = null;
    this.targets.clear();
    this.widthTarget = null;
  }
}

/** Holds the widget height cache of an editor. The value lives as long as the editor state. */
const widgetHeightsField = StateField.define({
  create: () => new WidgetHeights(),
  update: (value) => value,
});

/**
 * Decides how the block widget field updates for a transaction. "full"
 * rebuilds from the whole tree. "tables" revisits only the tables on the old
 * and new cursor lines. "none" keeps the decorations. Only tables depend on
 * the cursor, so without them a cursor move changes nothing.
 * @param {{docChanged?: boolean, treeChanged?: boolean, forced?: boolean, cursorMoved?: boolean, withTables?: boolean}} t
 * @returns {"full"|"tables"|"none"}
 */
export function blockUpdateKind({ docChanged = false, treeChanged = false, forced = false, cursorMoved = false, withTables = false } = {}) {
  if (docChanged || treeChanged || forced) return "full";
  if (cursorMoved && withTables) return "tables";
  return "none";
}

/**
 * Returns the styles that place the phone keyboard bar: the transform that
 * lifts it above the on-screen keyboard, and the space it covers at the bottom.
 * @param {{hidden: boolean, offset?: number, height?: number}} s
 * @returns {{transform: string, space: string}}
 */
export function kbarStyle({ hidden, offset = 0, height = 0 }) {
  const lift = Math.max(0, offset);
  return {
    transform: Math.round(lift) ? "translateY(" + -Math.round(lift) + "px)" : "",
    space: (hidden ? 0 : Math.round(lift + height)) + "px",
  };
}

/**
 * Compares a new kbarStyle result with the last written one. Returns the
 * entries to write, or null when nothing changed.
 * @param {{transform: string, space: string}|null} prev
 * @param {{transform: string, space: string}} next
 * @returns {{transform?: string, space?: string}|null}
 */
export function kbarStyleChanges(prev, next) {
  const out = {};
  let changed = false;
  for (const key of ["transform", "space"]) {
    if (!prev || prev[key] !== next[key]) {
      out[key] = next[key];
      changed = true;
    }
  }
  return changed ? out : null;
}

/**
 * Returns the scroll margins of the note: the part of the sticky editor bar
 * that overlaps the note on top, and the keyboard bar below. CodeMirror
 * measures the window through visualViewport, which already ends above the
 * on-screen keyboard, so the keyboard height is not added. Adding it scrolled
 * the caret off screen after Enter in landscape.
 * @param {{barHeight: number, scrollerTop: number, kbarHeight: number}} m
 * @returns {{top: number, bottom: number}}
 */
export function editorScrollMargins({ barHeight, scrollerTop, kbarHeight }) {
  return { top: Math.max(0, barHeight - Math.max(0, scrollerTop)), bottom: Math.max(0, kbarHeight) };
}

/**
 * Returns the rectangle CodeMirror may place tooltips in. It is the visual
 * viewport, which shrinks when the on-screen keyboard opens, less the keyboard
 * bar at its bottom.
 * @param {{width: number, height: number, vvTop?: number, vvHeight?: number}} box
 * @param {number} kbarHeight
 * @returns {{top: number, left: number, right: number, bottom: number}}
 */
export function tooltipSpaceRect({ width, height, vvTop = 0, vvHeight = height }, kbarHeight = 0) {
  const top = Math.max(0, vvTop);
  const bottom = Math.max(top, Math.min(height, vvTop + vvHeight) - Math.max(0, kbarHeight));
  return { top, left: 0, right: width, bottom };
}

/**
 * Finds a first line "# Title" that repeats the note title, compared without
 * case. Returns {from, to, lineEnd}, where to also covers the blank lines after
 * the heading, or null.
 * @param {string} text
 * @param {string} title
 * @returns {{from: number, to: number, lineEnd: number}|null}
 */
export function leadingTitleRange(text, title) {
  const want = String(title || "").trim().toLowerCase();
  if (!want) return null;
  const src = String(text || "");
  const nl = src.indexOf("\n");
  const lineEnd = nl < 0 ? src.length : nl;
  const m = /^#[ \t]+(.*?)(?:[ \t]+#+)?[ \t]*$/.exec(src.slice(0, lineEnd));
  if (!m || m[1].trim().toLowerCase() !== want) return null;
  let to = lineEnd;
  while (to < src.length) {
    const next = src.indexOf("\n", to + 1);
    const end = next < 0 ? src.length : next;
    if (src.slice(to + 1, end).trim() !== "") break;
    to = end;
  }
  return { from: 0, to, lineEnd };
}

/**
 * Finds the Image node for an image widget. The widget sits at the end of the
 * line where the image ends, so pos is any position on that line.
 * @returns {{from: number, to: number}|null}
 */
export function findImageRange(state, pos, url, alt) {
  const doc = state.doc;
  const line = doc.lineAt(Math.max(0, Math.min(pos, doc.length)));
  let found = null;
  syntaxTree(state).iterate({
    from: line.from,
    to: line.to,
    enter(ref) {
      if (ref.name !== "Image") return undefined;
      if (ref.to >= line.from && ref.to <= line.to) {
        const node = ref.node;
        const urlNode = node.getChild("URL");
        const marks = node.getChildren("LinkMark");
        const nodeUrl = urlNode ? doc.sliceString(urlNode.from, urlNode.to) : "";
        const label = marks.length >= 2 ? doc.sliceString(marks[0].to, marks[1].from) : "";
        if (nodeUrl === url && (!found || label === alt)) found = { from: ref.from, to: ref.to };
      }
      return false;
    },
  });
  return found;
}

/* ---------- Widgets ---------- */

const IS_MAC = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent || "");
const MOD = IS_MAC ? "⌘" : "Ctrl+";

/** Callout kinds with their Octicon and title, as docs/primer.md 5.10 lists them. */
const CALLOUTS = {
  note: { icon: "info", title: "Note" },
  info: { icon: "info", title: "Info" },
  tip: { icon: "light-bulb", title: "Tip" },
  success: { icon: "light-bulb", title: "Success" },
  important: { icon: "report", title: "Important" },
  warning: { icon: "alert", title: "Warning" },
  caution: { icon: "stop", title: "Caution" },
  danger: { icon: "stop", title: "Danger" },
  error: { icon: "stop", title: "Error" },
};

/**
 * Reports whether a link destination names an item of the space: a relative
 * path, not a URL with a scheme, a protocol-relative URL, an anchor or an
 * absolute path.
 * @param {string} url
 * @returns {boolean}
 */
export function isRelativeDestination(url) {
  const u = String(url || "").trim();
  return !!u && !/^[a-z][a-z0-9+.-]*:/i.test(u) && !u.startsWith("//") && !u.startsWith("#") && !u.startsWith("/");
}

/**
 * Reports whether a resolved item is an image file, from its media type or
 * its name.
 * @param {{kind?: string, mime?: string, name?: string}|null} hit
 * @returns {boolean}
 */
export function isImageItem(hit) {
  if (!hit || hit.kind !== "file") return false;
  const mime = String(hit.mime || "").toLowerCase();
  return mime.startsWith("image/") || (!mime && /\.(png|jpe?g|gif|webp|svg|avif|bmp)$/i.test(String(hit.name || "")));
}

/** Keeps the editor selection when a control inside a widget is pressed. */
function pressKeepsSelection(el) {
  el.addEventListener("mousedown", (ev) => ev.preventDefault());
  return el;
}

class BulletWidget extends WidgetType {
  eq() { return true; }
  toDOM() { return h("span", { class: "cm-ms-bullet", "aria-hidden": "true" }); }
  ignoreEvent() { return false; }
}

class CheckboxWidget extends WidgetType {
  constructor(checked, pos) { super(); this.checked = checked; this.pos = pos; }
  eq(other) { return other.checked === this.checked && other.pos === this.pos; }
  toDOM(view) {
    const box = h("input", { type: "checkbox", class: "cm-ms-check", "aria-label": this.checked ? "Completed task" : "Open task" });
    box.checked = this.checked;
    box.addEventListener("mousedown", (ev) => ev.preventDefault());
    box.addEventListener("click", (ev) => {
      ev.preventDefault();
      if (view.state.readOnly) return;
      // The marker is "[ ]" or "[x]"; the character at pos + 1 holds the state.
      const at = this.pos + 1;
      const cur = view.state.doc.sliceString(at, at + 1);
      view.dispatch({ changes: { from: at, to: at + 1, insert: cur === " " ? "x" : " " }, userEvent: "input.toggle" });
    });
    return box;
  }
  ignoreEvent() { return true; }
}

class HrWidget extends WidgetType {
  eq() { return true; }
  toDOM() { return h("span", { class: "cm-ms-hr", role: "separator" }); }
}

class CalloutWidget extends WidgetType {
  constructor(kind) { super(); this.kind = kind; }
  eq(other) { return other.kind === this.kind; }
  toDOM() {
    const spec = CALLOUTS[this.kind] || { icon: "info", title: this.kind.charAt(0).toUpperCase() + this.kind.slice(1) };
    return h("span", { class: "cm-ms-callout-label" }, h("span", { class: "cm-ms-callout-icon", "aria-hidden": "true" }, icon(spec.icon, 16)), h("span", { text: spec.title }));
  }
}

class CodeHeaderWidget extends WidgetType {
  constructor(lang, code) { super(); this.lang = lang; this.code = code; }
  eq(other) { return other.lang === this.lang && other.code === this.code; }
  toDOM() {
    const copy = pressKeepsSelection(iconButton({ icon: "copy", label: "Copy code", size: "small", className: "cm-ms-code-copy" }));
    copy.addEventListener("click", async (ev) => {
      ev.preventDefault();
      ev.stopPropagation();
      try {
        await navigator.clipboard.writeText(this.code);
        copyFeedback(copy);
      } catch {
        announce("Copy failed", { politeness: "assertive" });
      }
    });
    return h("span", { class: "cm-ms-code-head" }, h("span", { class: "cm-ms-code-lang", text: this.lang || "text" }), copy);
  }
  ignoreEvent(ev) { return ev.type !== "mousedown"; }
}

/** A wikilink in live preview. It renders as a link, muted with a dashed underline when nothing matches. */
export class WikiPillWidget extends WidgetType {
  constructor(target, label, exists) {
    super();
    this.target = target; this.label = label; this.exists = exists;
  }
  // Positions are not compared, so typing above the link keeps the element. Handlers read the position with posAtDOM.
  eq(other) { return other.target === this.target && other.label === this.label && other.exists === this.exists; }
  toDOM(view) {
    const ctx = ctxOf(view);
    const hit = ctx ? ctx.resolveLink(this.target) : null;
    const link = h("a", {
      class: "cm-ms-wikilink" + (this.exists === false ? " is-missing" : ""),
      href: hit && hit.exists ? hit.href : "#",
      text: this.label,
      "data-target": this.target,
    });
    if (ctx) ctx.bindWikiPill(link, this.target, () => view.posAtDOM(link));
    return link;
  }
  ignoreEvent() { return true; }
}

export class ImageWidget extends WidgetType {
  constructor(url, alt, heights = null, fileId = "") {
    super();
    this.url = url; this.alt = alt; this.heights = heights; this.fileId = fileId;
    // A widget built before the file sizes load is replaced once they arrive, so CodeMirror reads the new estimate.
    this.sized = !!(heights && heights.dimsFor(url, fileId));
  }
  // Positions are not compared, so typing above the image keeps its element and does not reload it.
  eq(other) { return other.url === this.url && other.alt === this.alt && other.sized === this.sized; }
  get estimatedHeight() { return this.heights ? this.heights.imageEstimate(this.url, this.fileId) : -1; }
  toDOM(view) {
    const ctx = ctxOf(view);
    // The file size reaches CodeMirror through estimatedHeight, not through width and height attributes.
    // Measured on the seeded note: the attributes raised wheel scroll CLS from 0.09 to 0.98 in 6.43.11.
    const img = h("img", { alt: this.alt, decoding: "async" });
    const media = h("div", { class: "cm-ms-media" }, img);
    const wrap = h("div", { class: "cm-ms-block cm-ms-media-block" }, media);
    if (ctx) {
      const src = ctx.imageSrcNow(this.url);
      if (src) img.src = src;
      else media.classList.add("is-broken");
      // Selecting the image shows its actions under it, or in the keyboard bar on a phone.
      img.addEventListener("click", () => {
        const range = findImageRange(view.state, view.posAtDOM(wrap), this.url, this.alt);
        if (range) ctx.selectImage(range.from, range.to);
      });
    } else {
      img.src = this.url;
    }
    img.addEventListener("error", () => media.classList.add("is-broken"));
    if (this.heights) this.heights.observe(wrap, this.url, () => img.naturalWidth > 0 || media.classList.contains("is-broken"));
    return wrap;
  }
  destroy(dom) { if (this.heights) this.heights.unobserve(dom); }
  ignoreEvent() { return true; }
}

// Height of a file card block, padding included. The CSS gives the card a fixed height to match.
const FILE_CARD_HEIGHT = 72;

/** An embedded file that is not an image, or an embedded note, drawn as a card that opens it. */
export class FileCardWidget extends WidgetType {
  constructor(kind, name, mime, href) {
    super();
    this.kind = kind; this.name = name; this.mime = mime; this.href = href;
  }
  eq(other) { return other.kind === this.kind && other.name === this.name && other.mime === this.mime && other.href === this.href; }
  get estimatedHeight() { return FILE_CARD_HEIGHT; }
  toDOM(view) {
    const ctx = ctxOf(view);
    const note = this.kind === "note";
    const ext = (/\.([^.]+)$/.exec(this.name) || [, ""])[1].toUpperCase();
    const meta = note ? "Note" : [ext, this.mime && !this.mime.startsWith("application/octet") ? this.mime : ""].filter(Boolean).join(" · ") || "File";
    const card = h("a", { class: "cm-ms-file-card", href: this.href || "#" },
      h("span", { class: "cm-ms-file-card-icon", "aria-hidden": "true" }, icon(note ? "note" : fileIconName(this.mime, this.name), 16)),
      h("span", { class: "cm-ms-file-card-body" }, h("span", { class: "cm-ms-file-card-name", text: this.name }), h("span", { class: "cm-ms-file-card-meta", text: meta })));
    card.addEventListener("mousedown", (ev) => ev.preventDefault());
    card.addEventListener("click", (ev) => {
      if (ev.button !== 0 || ev.metaKey || ev.ctrlKey || ev.shiftKey) return;
      ev.preventDefault();
      if (ctx && this.href) ctx.openHref(this.href);
    });
    return h("div", { class: "cm-ms-block cm-ms-file-block" }, card);
  }
  ignoreEvent() { return true; }
}

/** Upload progress in place of a file that is still uploading. */
class UploadWidget extends WidgetType {
  constructor(fileId, name, image, item) {
    super();
    this.fileId = fileId; this.name = name; this.image = image;
    this.state = item ? item.state : "queued";
    this.percent = item && item.size ? Math.round((100 * (item.loaded || 0)) / item.size) : 0;
    this.error = item && item.error ? String(item.error) : "";
    this.size = item ? item.size || 0 : 0;
  }
  eq(other) { return other.fileId === this.fileId && other.state === this.state && other.percent === this.percent && other.error === this.error; }
  get estimatedHeight() { return 72; }
  toDOM(view) {
    const ctx = ctxOf(view);
    const thumb = h("span", { class: "cm-ms-upload-thumb", "aria-hidden": "true" }, icon(this.image ? "image" : fileIconName("", this.name), 16));
    const dom = h("div", { class: "cm-ms-upload" }, thumb,
      h("span", { class: "cm-ms-upload-body" }, h("span", { class: "cm-ms-upload-name", text: this.name }), h("span", { class: "cm-ms-upload-meta" }), h("span", { class: "cm-ms-upload-status" })),
      h("span", { class: "cm-ms-upload-actions" }));
    if (ctx && this.image) {
      const src = ctx.imageSrcNow(ctx.fileUrlOf(this.fileId));
      if (src) thumb.replaceChildren(h("img", { src, alt: "" }));
    }
    const wrap = h("div", { class: "cm-ms-block cm-ms-upload-block" }, dom);
    this.fill(wrap, view);
    return wrap;
  }
  updateDOM(dom, view) {
    this.fill(dom, view);
    return true;
  }
  fill(wrap, view) {
    const ctx = ctxOf(view);
    const dom = wrap.querySelector(".cm-ms-upload");
    const online = ctx ? ctx.isOnline() : true;
    dom.dataset.state = this.state;
    const meta = dom.querySelector(".cm-ms-upload-meta");
    const size = this.size ? formatBytes(this.size) : "";
    const text = this.state === "failed" ? "Upload failed" : this.state === "queued" ? (online ? "Waiting to upload" : "Saved on this device, uploads when online") : this.state === "done" ? "Uploaded" : "Uploading " + this.percent + "%";
    meta.textContent = [size, text].filter(Boolean).join(" · ");
    const statusEl = dom.querySelector(".cm-ms-upload-status");
    if (this.state === "failed") {
      statusEl.replaceChildren(inlineMessage({ variant: "critical", size: "small", text: this.error || "The file could not be uploaded" }));
    } else if (this.state === "uploading" || this.state === "done") {
      let bar = statusEl.querySelector(".progress-bar");
      if (!bar) {
        bar = progressBar({ size: "small", label: "Upload progress" });
        statusEl.replaceChildren(bar);
      }
      bar.set(this.state === "done" ? 100 : this.percent);
    } else {
      statusEl.replaceChildren();
    }
    const actions = dom.querySelector(".cm-ms-upload-actions");
    actions.replaceChildren();
    if (ctx && this.state === "failed") {
      actions.appendChild(button({ label: "Retry", variant: "invisible", size: "small", onClick: () => ctx.retryUpload(this.fileId) }));
    }
  }
  ignoreEvent() { return true; }
}

export class TableWidget extends WidgetType {
  constructor(source, heights = null) {
    super();
    this.source = source; this.heights = heights;
    this.key = "table:" + hashString(source);
  }
  // Positions are not compared, so typing above the table keeps its element.
  eq(other) { return other.source === this.source; }
  get estimatedHeight() { return this.heights ? this.heights.tableEstimate(this.key, this.source) : -1; }
  toDOM(view) {
    const ctx = ctxOf(view);
    const inner = h("div", { class: "cm-ms-table md-body" });
    // markdown.render builds its HTML from escaped text and a fixed tag set.
    inner.innerHTML = ctx ? ctx.renderHtml(this.source) : "";
    const dom = h("div", { class: "cm-ms-block cm-ms-table-block" }, inner);
    dom.addEventListener("mousedown", (ev) => {
      ev.preventDefault();
      view.dispatch({ selection: { anchor: view.posAtDOM(dom) } });
      view.focus();
    });
    if (this.heights) this.heights.observe(dom, this.key);
    return dom;
  }
  destroy(dom) { if (this.heights) this.heights.unobserve(dom); }
  ignoreEvent() { return false; }
}

/* ---------- Live preview decorations ---------- */

const HIDE = Decoration.replace({});
const lineClass = (cls) => Decoration.line({ class: cls });
const markClass = (cls, attributes) => Decoration.mark({ class: cls, attributes });

function isBlank(doc, from, to) {
  return doc.sliceString(from, to).trim() === "";
}

/** Reports whether the wikilink at from is an embed, written with a leading "!". */
function isEmbedAt(doc, from) {
  return from > 0 && doc.sliceString(from - 1, from) === "!";
}

/**
 * Builds the inline and line decorations for the drawn part of the document.
 * It covers the whole viewport, not only the visible ranges: a heading or code
 * block line that is drawn above the screen but outside the visible ranges
 * would otherwise lose its class and height, and the lines below would jump.
 * Measured on the seeded note, the H1 fell from 58 to 24 px at 780 px of scroll.
 */
function buildLiveDecorations(view) {
  const { state } = view;
  const doc = state.doc;
  const out = [];
  const seen = new Set();
  const add = (from, to, deco) => { if (from <= to) out.push(deco.range(from, to)); };
  const tree = syntaxTree(state);

  // Lines that CodeMirror draws outside the viewport because they hold a selection are left undecorated. Decorating
  // them as a second range raised scroll CLS on the seeded note from 0.03 to 0.75, so a caret on the first line
  // still lets the H1 lose its class once the note scrolls about 780 px away.
  for (const { from, to } of [view.viewport]) {
    tree.iterate({
      from, to,
      enter(ref) {
        const name = ref.name;
        const key = name + ":" + ref.from;
        if (seen.has(key)) return name === "FencedCode" || name === "Table" ? false : undefined;
        seen.add(key);
        const node = ref.node;

        if (/^ATXHeading[1-6]$/.test(name)) {
          add(doc.lineAt(ref.from).from, doc.lineAt(ref.from).from, lineClass("cm-ms-h" + name.slice(-1)));
          return;
        }
        if (/^SetextHeading[12]$/.test(name)) {
          add(doc.lineAt(ref.from).from, doc.lineAt(ref.from).from, lineClass("cm-ms-h" + name.slice(-1)));
          return;
        }
        switch (name) {
          case "HeaderMark": {
            if (node.parent && /^ATXHeading/.test(node.parent.name) && !lineTouches(state, ref.from)) {
              const end = doc.sliceString(ref.to, ref.to + 1) === " " ? ref.to + 1 : ref.to;
              if (ref.from === doc.lineAt(ref.from).from || !isBlank(doc, ref.from, ref.to)) add(ref.from, end, HIDE);
            }
            return;
          }
          case "Emphasis": add(ref.from, ref.to, markClass("cm-ms-em")); return;
          case "StrongEmphasis": add(ref.from, ref.to, markClass("cm-ms-strong")); return;
          case "Strikethrough": add(ref.from, ref.to, markClass("cm-ms-strike")); return;
          case "InlineCode": add(ref.from, ref.to, markClass("cm-ms-code-inline")); return;
          case "EmphasisMark":
          case "StrikethroughMark": {
            const p = node.parent;
            if (p && !touches(state, p.from, p.to)) add(ref.from, ref.to, HIDE);
            return;
          }
          case "CodeMark": {
            const p = node.parent;
            if (p && p.name === "InlineCode" && !touches(state, p.from, p.to)) add(ref.from, ref.to, HIDE);
            return;
          }
          case "Link": {
            const marks = node.getChildren("LinkMark");
            const urlNode = node.getChild("URL");
            const url = urlNode ? doc.sliceString(urlNode.from, urlNode.to) : "";
            if (marks.length >= 2 && marks[0].to <= marks[1].from) {
              add(marks[0].to, marks[1].from, markClass("cm-ms-link", { "data-url": url }));
              if (!touches(state, ref.from, ref.to) && url) {
                add(ref.from, marks[0].to, HIDE);
                add(marks[1].from, ref.to, HIDE);
              }
            }
            return false;
          }
          case "URL": {
            if (!node.parent || node.parent.name !== "Link") {
              const url = doc.sliceString(ref.from, ref.to);
              add(ref.from, ref.to, markClass("cm-ms-link", { "data-url": url }));
            }
            return;
          }
          case "Image": {
            if (!touches(state, ref.from, ref.to)) add(ref.from, ref.to, markClass("cm-ms-image-src"));
            return false;
          }
          case "WikiLink": {
            const inner = doc.sliceString(ref.from + 2, ref.to - 2);
            const bar = inner.indexOf("|");
            const target = (bar >= 0 ? inner.slice(0, bar) : inner).trim();
            const label = (bar >= 0 ? inner.slice(bar + 1) : inner).trim() || target;
            const embed = isEmbedAt(doc, ref.from);
            if (touches(state, ref.from - (embed ? 1 : 0), ref.to)) add(ref.from, ref.to, markClass("cm-ms-wikilink-src"));
            else if (embed) add(ref.from - 1, ref.to, markClass("cm-ms-image-src"));
            else {
              const ctx = ctxOf(view);
              const hit = ctx ? ctx.resolveLink(target) : null;
              add(ref.from, ref.to, Decoration.replace({ widget: new WikiPillWidget(target, label, hit ? hit.exists : null) }));
            }
            return false;
          }
          case "ListMark": {
            const list = node.parent && node.parent.parent;
            if (list && list.name === "BulletList") {
              const end = Math.min(ref.to + 1, doc.lineAt(ref.from).to);
              const task = node.parent.getChild("Task");
              if (task) {
                if (!touches(state, ref.from, end)) add(ref.from, end, HIDE);
              } else if (!touches(state, ref.from, end)) {
                add(ref.from, end, Decoration.replace({ widget: new BulletWidget() }));
              }
            } else if (list && list.name === "OrderedList") {
              add(ref.from, ref.to, markClass("cm-ms-olmark"));
            }
            return;
          }
          case "TaskMarker": {
            const checked = /x/i.test(doc.sliceString(ref.from, ref.to));
            if (!touches(state, ref.from, ref.to)) {
              add(ref.from, ref.to, Decoration.replace({ widget: new CheckboxWidget(checked, ref.from) }));
            }
            if (checked && node.parent) add(ref.to, node.parent.to, markClass("cm-ms-task-done"));
            return;
          }
          case "Blockquote": {
            const first = doc.lineAt(ref.from);
            const callout = /^\s*>\s*\[!([A-Za-z]+)\][+-]?/.exec(first.text);
            const cls = callout ? "cm-ms-quote cm-ms-callout cm-ms-callout-" + callout[1].toLowerCase() : "cm-ms-quote";
            for (let pos = ref.from; pos <= ref.to;) {
              const line = doc.lineAt(pos);
              add(line.from, line.from, lineClass(cls + (line.from === first.from ? " is-first" : "")));
              if (line.to >= ref.to) break;
              pos = line.to + 1;
            }
            if (callout && !lineTouches(state, first.from)) {
              const start = first.from + first.text.indexOf("[!");
              const end = first.from + callout[0].length;
              add(start, end, Decoration.replace({ widget: new CalloutWidget(callout[1].toLowerCase()) }));
            }
            return;
          }
          case "QuoteMark": {
            if (!lineTouches(state, ref.from)) {
              const end = doc.sliceString(ref.to, ref.to + 1) === " " ? ref.to + 1 : ref.to;
              add(ref.from, end, HIDE);
            }
            return;
          }
          case "HorizontalRule": {
            const line = doc.lineAt(ref.from);
            if (!lineTouches(state, ref.from)) add(ref.from, ref.to, Decoration.replace({ widget: new HrWidget() }));
            add(line.from, line.from, lineClass("cm-ms-hr-line"));
            return;
          }
          case "FencedCode": {
            const open = doc.lineAt(ref.from);
            const close = doc.lineAt(ref.to);
            const editing = touches(state, ref.from, ref.to);
            for (let pos = ref.from; pos <= ref.to;) {
              const line = doc.lineAt(pos);
              let cls = "cm-ms-codeblock";
              if (line.from === open.from) cls += " is-first";
              if (line.from === close.from) cls += " is-last";
              add(line.from, line.from, lineClass(cls));
              if (line.to >= ref.to) break;
              pos = line.to + 1;
            }
            if (!editing && open.from !== close.from) {
              const info = node.getChild("CodeInfo");
              const lang = info ? doc.sliceString(info.from, info.to) : "";
              const textNode = node.getChild("CodeText");
              const code = textNode ? doc.sliceString(textNode.from, textNode.to) : "";
              add(open.from, open.to, Decoration.replace({ widget: new CodeHeaderWidget(lang, code) }));
              if (/^\s*(```|~~~)/.test(close.text)) {
                add(close.from, close.to, HIDE);
                add(close.from, close.from, lineClass("cm-ms-codeblock-fold"));
              }
            }
            return false;
          }
          case "Table": {
            for (let pos = ref.from; pos <= ref.to;) {
              const line = doc.lineAt(pos);
              add(line.from, line.from, lineClass("cm-ms-table-line"));
              if (line.to >= ref.to) break;
              pos = line.to + 1;
            }
            return false;
          }
          default:
            return;
        }
      },
    });
  }
  return Decoration.set(out, true);
}

const livePlugin = ViewPlugin.fromClass(class {
  constructor(view) { this.decorations = buildLiveDecorations(view); }
  update(u) {
    if (u.docChanged || u.viewportChanged || u.selectionSet || u.focusChanged || syntaxTree(u.startState) !== syntaxTree(u.state)
      || u.transactions.some((tr) => tr.effects.some((e) => e.is(setFocused) || e.is(refreshLive)))) {
      this.decorations = buildLiveDecorations(u.view);
    }
  }
}, { decorations: (v) => v.decorations });

/**
 * Chooses the block widget for an image, an embed or a link to an uploading
 * file. links resolves relative targets through buildItemResolver, and
 * uploads maps file ids to upload items. Returns null when no widget shows.
 * @param {{name: "Image"|"Link"|"Embed", url: string, label: string}} node
 * @param {{links: {resolve: Function}|null, uploads: Map, heights: WidgetHeights|null}} env
 * @returns {WidgetType|null}
 */
export function blockWidgetFor({ name, url, label }, { links = null, uploads = new Map(), heights = null } = {}) {
  let hit = null;
  if (name === "Embed") {
    hit = links ? links.resolve(url, url.includes("/") ? "pathWikilink" : "wikilink", true) : null;
    if (!hit) return null;
  } else if (isRelativeDestination(url)) {
    hit = links ? links.resolve(url, name === "Image" ? "markdownImage" : "markdownLink", name === "Image") : null;
    if (!hit) return null;
  } else if (name !== "Image" || !url) {
    return null;
  }
  const fileUrl = hit ? hit.fileUrl || "" : url;
  const fileId = fileIdFromUrl(fileUrl);
  const image = hit ? isImageItem(hit) : true;
  const upload = fileId ? uploads.get(fileId) : null;
  if (upload && upload.state !== "done") return new UploadWidget(fileId, (hit && hit.name) || upload.filename || label || "file", image, upload);
  if (hit && hit.kind === "note") return name === "Embed" ? new FileCardWidget("note", label || hit.name || url, "", hit.href) : null;
  if (name === "Link") return null;
  if (image && fileUrl) return new ImageWidget(fileUrl, label, heights, fileId);
  if (name === "Embed" && hit && hit.kind === "file") return new FileCardWidget("file", hit.name || label, hit.mime || "", hit.href);
  return null;
}

/** Builds block widgets over the whole document: images, embeds, uploads and tables. */
function buildBlockDecorations(state, withTables) {
  const doc = state.doc;
  const out = [];
  const env = {
    links: state.field(linksField, false) || null,
    uploads: state.field(uploadsField, false) || new Map(),
    heights: state.field(widgetHeightsField, false) || null,
  };
  syntaxTree(state).iterate({
    enter(ref) {
      const name = ref.name;
      if (BLOCK_CONTAINERS.has(name)) return true;
      if (name === "Image" || name === "Link") {
        const node = ref.node;
        const urlNode = node.getChild("URL");
        const url = urlNode ? doc.sliceString(urlNode.from, urlNode.to) : "";
        const marks = node.getChildren("LinkMark");
        const label = marks.length >= 2 ? doc.sliceString(marks[0].to, marks[1].from) : "";
        const widget = blockWidgetFor({ name, url, label }, env);
        if (widget) out.push(Decoration.widget({ widget, block: true, side: 1 }).range(doc.lineAt(ref.to).to));
        return false;
      }
      if (name === "WikiLink") {
        if (isEmbedAt(doc, ref.from)) {
          const inner = doc.sliceString(ref.from + 2, ref.to - 2);
          const bar = inner.indexOf("|");
          const target = (bar >= 0 ? inner.slice(0, bar) : inner).trim();
          const label = (bar >= 0 ? inner.slice(bar + 1) : inner).trim() || target;
          const widget = target ? blockWidgetFor({ name: "Embed", url: target.split("#")[0], label }, env) : null;
          if (widget) out.push(Decoration.widget({ widget, block: true, side: 1 }).range(doc.lineAt(ref.to).to));
        }
        return false;
      }
      if (name === "Table" && withTables && !touches(state, ref.from, ref.to)) {
        const from = doc.lineAt(ref.from).from;
        const to = doc.lineAt(ref.to).to;
        out.push(Decoration.replace({ widget: new TableWidget(doc.sliceString(from, to), env.heights), block: true }).range(from, to));
        return false;
      }
      return false;
    },
  });
  return Decoration.set(out, true);
}

/** Lists the tables, as whole-line ranges, that share a line with one of the ranges. */
function tablesOnLines(state, ranges) {
  const doc = state.doc;
  const tree = syntaxTree(state);
  const found = new Map();
  for (const r of ranges) {
    tree.iterate({
      from: doc.lineAt(r.from).from,
      to: doc.lineAt(r.to).to,
      enter(ref) {
        if (ref.name === "Table") {
          const from = doc.lineAt(ref.from).from;
          found.set(from, { from, to: doc.lineAt(ref.to).to });
          return false;
        }
        return BLOCK_CONTAINERS.has(ref.name) ? undefined : false;
      },
    });
  }
  return [...found.values()];
}

/** Swaps a table widget for its source, or back, where the cursor left or entered the table. */
function updateTablesAtCursor(value, tr) {
  const { state, startState } = tr;
  const heights = state.field(widgetHeightsField, false) || null;
  let set = value;
  for (const t of tablesOnLines(state, [...startState.selection.ranges, ...state.selection.ranges])) {
    const shown = !touches(state, t.from, t.to);
    if (shown === !touches(startState, t.from, t.to)) continue;
    const add = shown ? [Decoration.replace({ widget: new TableWidget(state.doc.sliceString(t.from, t.to), heights), block: true }).range(t.from, t.to)] : [];
    set = set.update({ filterFrom: t.from, filterTo: t.to, filter: (_from, _to, deco) => !(deco.spec.widget instanceof TableWidget), add });
  }
  return set;
}

function blockField(withTables) {
  return StateField.define({
    create: (state) => buildBlockDecorations(state, withTables),
    update(value, tr) {
      const kind = blockUpdateKind({
        docChanged: tr.docChanged,
        treeChanged: syntaxTree(tr.startState) !== syntaxTree(tr.state),
        forced: tr.effects.some((e) => e.is(setUploads) || e.is(refreshLive) || e.is(setLinks)),
        cursorMoved: !!tr.selection || tr.effects.some((e) => e.is(setFocused)),
        withTables,
      });
      if (kind === "full") return buildBlockDecorations(tr.state, withTables);
      if (kind === "tables") return updateTablesAtCursor(value, tr);
      return value;
    },
    provide: (f) => EditorView.decorations.from(f),
  });
}

/* ---------- Selection toolbar ---------- */

function createBubble(view) {
  const ctx = ctxOf(view);
  return { dom: ctx ? ctx.buildBubble() : h("div"), above: true };
}

const bubbleField = StateField.define({
  create: () => null,
  update(value, tr) {
    const state = tr.state;
    const sel = state.selection.main;
    // Phones get the selection tools in the keyboard bar, so nothing floats over the selection handles.
    if (sel.empty || !state.field(focusField, false) || state.field(askBarField, false) || state.readOnly || window.matchMedia(PHONE_QUERY).matches) return null;
    if (value && value.pos === sel.from && value.end === sel.to) return value;
    return { pos: sel.from, end: sel.to, above: true, create: createBubble };
  },
  provide: (f) => showTooltip.from(f),
});

/**
 * Moves focus between the buttons of a toolbar with the arrow keys, Home and
 * End, behind one tab stop.
 * @param {HTMLElement} bar
 */
function rovingToolbar(bar) {
  const items = () => [...bar.querySelectorAll("button")].filter((b) => !b.hidden && b.offsetParent !== null);
  const reset = () => {
    const list = [...bar.querySelectorAll("button")];
    list.forEach((b, i) => { b.tabIndex = i === 0 ? 0 : -1; });
  };
  bar.addEventListener("keydown", (ev) => {
    if (!["ArrowRight", "ArrowLeft", "Home", "End"].includes(ev.key)) return;
    const list = items();
    const i = list.indexOf(document.activeElement);
    if (i < 0) return;
    ev.preventDefault();
    const next = ev.key === "Home" ? 0 : ev.key === "End" ? list.length - 1 : (i + (ev.key === "ArrowRight" ? 1 : -1) + list.length) % list.length;
    for (const b of list) b.tabIndex = -1;
    list[next].tabIndex = 0;
    list[next].focus();
  });
  reset();
  return reset;
}

/* ---------- Comment passages ---------- */

const setCommentRanges = StateEffect.define();
const setActiveCommentId = StateEffect.define();

/** Builds mark decorations for comment passages, with the active one emphasised. */
function commentDecorations(ranges, active) {
  const marks = ranges
    .filter((r) => r.to > r.from)
    .sort((a, b) => a.from - b.from || a.to - b.to)
    .map((r) => Decoration.mark({
      class: "cm-ms-comment" + (r.id === active ? " is-active" : ""),
      attributes: { "data-comment-id": r.id },
      commentId: r.id,
    }).range(r.from, r.to));
  return Decoration.set(marks, true);
}

/** Lists the comment ranges held in a decoration set. */
function commentRangesOf(decos) {
  const out = [];
  for (let it = decos.iter(); it.value; it.next()) out.push({ id: it.value.spec.commentId, from: it.from, to: it.to });
  return out;
}

/**
 * Comment passages as mark decorations. They map through every change, so a
 * passage follows its text while the user types. Text typed at either edge
 * stays outside the passage, as on the server.
 */
const commentField = StateField.define({
  create: () => ({ decos: Decoration.none, active: null }),
  update(value, tr) {
    let decos = tr.docChanged ? value.decos.map(tr.changes) : value.decos;
    let active = value.active;
    let ranges = null;
    for (const e of tr.effects) {
      if (e.is(setCommentRanges)) ranges = e.value;
      else if (e.is(setActiveCommentId)) active = e.value;
    }
    if (ranges || active !== value.active) decos = commentDecorations(ranges || commentRangesOf(decos), active);
    return decos === value.decos && active === value.active ? value : { decos, active };
  },
  provide: (f) => EditorView.decorations.from(f, (v) => v.decos),
});

/* ---------- Editor ---------- */

// Primer's narrow range, plus landscape phones, which are wider than 768 px but short.
const PHONE_QUERY = NARROW_QUERY + ", (pointer: coarse) and (max-height: 500px)";

/**
 * Focuses the editor that contains el without scrolling, and restores its
 * selection. Returns false when el is not inside an editor.
 * @param {Element} el
 * @returns {boolean}
 */
export function focusEditorIn(el) {
  const host = el && el.closest ? el.closest(".cm-editor") : null;
  const found = host ? EditorView.findFromDOM(host) : null;
  if (!found) return false;
  found.focus();
  return true;
}

/**
 * Places the open tooltips of a view again, for example when the on-screen
 * keyboard opens. The tooltip plugin only listens to window resize, and a
 * keyboard resizes only the visual viewport. The vendored bundle does not
 * export repositionTooltips, so this calls the plugin method that function
 * calls. It reads the internal names view.plugins, plugin.value,
 * value.manager and value.maybeMeasure, which test/client/editor/bundle.test.mjs
 * checks against the bundle. Returns the number of plugins measured.
 * @param {EditorView} view
 * @returns {number}
 */
export function repositionTooltips(view) {
  let n = 0;
  for (const p of (view && view.plugins) || []) {
    const v = p && p.value;
    if (v && v.manager && typeof v.maybeMeasure === "function") {
      v.maybeMeasure();
      n++;
    }
  }
  return n;
}

const FADE_DELAY = 1500;
const SAVE_DELAY = 500;
const MODES = ["live", "source", "reading"];
const remoteAnnotation = Annotation.define();

// The octicon of each save state in the editor bar. Saving shows a Spinner instead.
const STATUS_ICONS = { synced: "check", offline: "cloud-offline", local: "alert", warn: "alert", error: "alert", readonly: "eye" };

/** Encodes an entry path segment by segment. */
function encodePath(path) {
  return String(path || "").split("/").filter(Boolean).map(encodeURIComponent).join("/");
}

/**
 * Mounts the note editor into container.
 *
 * options: {note, access, store, sync, render, navigate, isOnline, user,
 * onDone, setChrome, api?, app?, openComments?, commentSource?, openPanel?}.
 * api is optional; without it the editor loads /static/js/api.js when a panel
 * first needs it. app is the shell application; the editor uses its store,
 * hrefFor and fileCache to resolve links and embeds, and builds the same
 * pieces itself without it. openComments({focusId, draftAnchor}) shows the
 * shell's comments panel, and commentSource is the shell's comment list for
 * the note. openPanel(kind, extra) shows the shell's rail tab for "history",
 * "share", "files" or "ai"; extra carries a seed for the AI tab and the
 * editor callbacks insertText, insertTracked, replaceSelection and
 * getSelection. Without these options the editor keeps its own comment list
 * and opens panels in its drawer.
 *
 * The handle's insertText, replaceSelection and getSelection let the shell's
 * AI and Files panels write into the note. insertText(text, {tracked: true})
 * and insertTracked(text) put the text in as a tracked insertion for review.
 * openAsk(opts) opens the ask bar, setSuggestions(list) draws stored
 * suggestions as tracked changes, revealSuggestion(id) scrolls to the first
 * hunk of a stored suggestion and focuses its controls, and goTo(pos, index)
 * moves to a heading for the outline.
 *
 * @param {HTMLElement} container
 * @param {object} options
 * @returns {{flush(): Promise<void>, isDirty(): boolean, unmount(): void, openPanel(name: string, extra?: object): void, setActiveComment(id: string|null): void, revealComment(id: string): void, insertText(text: string, opts?: {tracked?: boolean, title?: string}): void, insertTracked(text: string, opts?: {title?: string}): void, replaceSelection(text: string): void, getSelection(): object, openAsk(opts?: object): object|null, setSuggestions(list: object[], opts?: object): number, revealSuggestion(id: string): boolean, changes(): object[], goTo(pos: number, index?: number): void, view: EditorView}}
 */
export function mountEditor(container, options) {
  const { store, sync, user } = options;
  let note = { ...options.note };
  const access = options.access || "view";
  const canEdit = accessRank(access) >= 3;
  const isOnline = options.isOnline || (() => navigator.onLine);
  const setChrome = options.setChrome || (() => {});
  const navigate = options.navigate || ((url) => { location.href = url; });
  const onDone = options.onDone || (() => {});
  let render = options.render || null;
  const ac = new AbortController();
  const on = (target, type, fn, opts = {}) => target.addEventListener(type, fn, { ...opts, signal: ac.signal });
  const unsubs = [];
  const timers = new Set();
  const later = (fn, ms) => { const t = setTimeout(() => { timers.delete(t); fn(); }, ms); timers.add(t); return t; };
  const cancel = (t) => { clearTimeout(t); timers.delete(t); };
  const phoneMq = window.matchMedia(PHONE_QUERY);

  let saveTimer = 0;
  let fadeTimer = 0;
  let statsTimer = 0;
  let indexTimer = 0;
  let saving = Promise.resolve();
  let saveError = null;
  let editedLocally = false;
  let lastSaved = { title: note.title || "", content: note.content || "" };
  let changeSeq = 0;
  let savedSeq = 0;
  let focusMode = false;
  let typewriter = readPref("typewriter", "0") === "1";
  let mode = readPref("mode", canEdit ? "live" : "reading");
  let panelHandle = null;
  let spaceSlug = note.spaceSlug || "";
  let unmounted = false;
  // Whether the note's suggestions route exists: null until probed.
  let suggestionsRoute = null;
  // A tap on an image shows its actions in the keyboard bar on a phone.
  let imageTapped = false;
  // Whether the first line is an H1 equal to the title, which then stands in for the title field.
  let titleLinked = false;
  // The link resolver from fileviews.js, notes by their href, and files by id.
  let resolver = null;
  let resolverSeq = 0;
  let notesByHref = new Map();
  let filesById = new Map();
  let saveBanner = null;

  /* ----- DOM ----- */

  const seg = segmentedControl({
    label: "Editor mode",
    iconsOnlyNarrow: true,
    items: [
      { label: "Live", value: "live", icon: "pencil" },
      { label: "Source", value: "source", icon: "code" },
      { label: "Reading", value: "reading", icon: "eye" },
    ],
    onChange: (value) => setMode(value, true),
  });
  seg.classList.add("ms-seg");
  const statusIcon = h("span", { class: "ms-status-icon", "aria-hidden": "true" });
  const statusText = h("span", { class: "ms-status-text" });
  const status = h("span", { class: "ms-status" }, statusIcon, statusText);
  const statusCount = h("span", { class: "ms-words" });
  const focusBtn = iconButton({ icon: "screen-full", label: "Focus mode", keybindingHint: MOD + "Shift+.", pressed: false, className: "ms-focus-btn" });
  const typeBtn = iconButton({ icon: "typography", label: "Typewriter scrolling", pressed: typewriter, className: "ms-type-btn" });
  const doneBtn = button({ label: "Done", variant: "primary", className: "ms-done" });
  // Find opens the find bar in every mode. It is the entry point on phones, where there is no keyboard shortcut.
  const findBtn = iconButton({ icon: "search", label: "Find", keybindingHint: FIND_HINT, className: "ms-find-btn", onClick: () => find.toggle() });
  // Below 382px Find, typewriter scrolling and focus mode leave the bar for this ActionMenu, so the mode control, the
  // save status, More and Done fit 320px without overlap. editor.css shows it only at that width.
  const barMoreBtn = iconButton({
    icon: "kebab-horizontal", label: "More editor actions", className: "ms-bar-more", attrs: { "aria-haspopup": "true" },
    onClick: (event) => contextMenu(event.currentTarget, [
      { label: "Find", icon: "search", onClick: () => requestAnimationFrame(() => { if (!unmounted) find.open(); }) },
      canEdit ? { label: "Typewriter scrolling", icon: "typography", checked: typewriter, selectionType: "multiple", onClick: () => setTypewriter(!typewriter) } : null,
      { label: "Focus mode", icon: focusMode ? "screen-normal" : "screen-full", checked: focusMode, selectionType: "multiple", onClick: () => setFocus(!focusMode) },
    ], "bottom-end"),
  });
  const bar = h("div", { class: "ms-editor-bar ms-chrome" }, seg, status, statusCount, h("span", { class: "ms-bar-spacer" }),
    findBtn, canEdit ? typeBtn : null, focusBtn, barMoreBtn, doneBtn);

  const bannerArea = h("div", { class: "ms-editor-banners" });
  const titleEl = h("input", { type: "text", class: "ms-title", placeholder: "Untitled", "aria-label": "Title", autocomplete: "off", spellcheck: true });
  titleEl.value = note.title || "";
  const cmHost = h("div", { class: "ms-cm" });
  const page = h("div", { class: "ms-page" }, titleEl, cmHost);
  const writePane = h("div", { class: "ms-pane ms-write" }, page);
  const readInner = h("div", { class: "ms-preview-page md-body" });
  const readPane = h("div", { class: "ms-pane ms-read", "aria-label": "Reading view" }, readInner);
  const panes = h("div", { class: "ms-panes" }, writePane, readPane);
  const drawerBody = h("div", { class: "ms-drawer-body" });
  const drawer = h("aside", { class: "ms-drawer", hidden: true }, drawerBody);
  const fileInput = h("input", { type: "file", class: "ms-file", multiple: true, tabindex: "-1", "aria-hidden": "true" });
  // The toolbar holds only the buttons. The docked ask bar sits beside it in the same fixed group.
  const kbarTools = h("div", { class: "ms-kbar-tools", role: "toolbar", "aria-label": "Formatting" });
  // The ask bar docks here on a phone, above the keyboard.
  const kbarDock = h("div", { class: "ms-kbar-dock" });
  const kbar = h("div", { class: "ms-kbar", role: "group", "aria-label": "Formatting", hidden: true }, kbarDock, kbarTools);
  // Find and replace (find.js). The bar sticks under the editor bar and serves Live, Source and Reading.
  const find = createFind({
    getMode: () => mode,
    setMode: (next) => setMode(next, true),
    canEdit,
    readRoot: readInner,
    readPane,
    readSkip: ".ms-preview-title",
    trigger: findBtn,
    insets: () => ({ top: barHeightNow(), bottom: kbar.hidden ? 0 : kbarHeightNow() }),
    onLayout: () => {
      if (unmounted) return;
      placeKbar();
      view.requestMeasure();
    },
  });
  const root = h("div", { class: "ms-editor" + (canEdit ? "" : " is-readonly") }, bar, find.element, bannerArea, panes, drawer, kbar, fileInput);

  if (!canEdit) {
    titleEl.readOnly = true;
    banner({
      variant: "info",
      title: access === "comment" ? "Comment only" : "View only",
      description: access === "comment" ? "You can comment on this note but not edit it." : "You can read this note but not edit it.",
      into: bannerArea,
      announce: false,
    });
  }
  container.appendChild(root);
  // Scroll padding in editor.css keeps focused lines clear of the sticky bar and the keyboard bar.
  document.documentElement.classList.add("ms-editing");

  /* ----- CodeMirror ----- */

  const modeCompartment = new Compartment();
  const focusLineCompartment = new Compartment();
  const editorKeymap = [
    { key: "Enter", run: () => runText(continueList) },
    { key: "Tab", run: () => runText(indentLines, false) },
    { key: "Shift-Tab", run: () => runText(indentLines, true) },
    { key: "Mod-b", run: () => runText(toggleInline, "**") },
    { key: "Mod-i", run: () => runText(toggleInline, "_") },
    { key: "Mod-k", run: () => runText(toggleLink) },
    { key: "Mod-e", run: () => runText(toggleCode) },
    { key: "Mod-Shift-x", run: () => runText(toggleInline, "~~") },
    { key: "Mod-s", run: () => { flush(); return true; } },
    { key: "Mod-Shift-.", run: () => { setFocus(!focusMode); return true; } },
  ];

  const view = new EditorView({
    parent: cmHost,
    state: EditorState.create({
      doc: note.content || "",
      extensions: [
        markdownSupport([WikiLinkExtension]),
        syntaxHighlighting(highlightStyle),
        history(),
        drawSelection(),
        dropCursor(),
        EditorView.lineWrapping,
        EditorState.readOnly.of(!canEdit),
        EditorView.editable.of(canEdit),
        EditorView.contentAttributes.of({ spellcheck: "true", autocorrect: "on", autocapitalize: "sentences", "aria-label": "Note text", "aria-multiline": "true" }),
        placeholder(canEdit ? "Start writing. Type / for blocks, [[ to link a note or file." : ""),
        focusField, uploadsField, linksField, bubbleField, commentField, widgetHeightsField,
        EditorView.focusChangeEffect.of((_state, focusing) => setFocused.of(focusing)),
        // The sticky editor bar on top and the keyboard bar below. CodeMirror also subtracts these margins from the
        // .cm-scroller box to decide where tooltips may show. The document scrolls, so that box starts below the bar until
        // the note scrolls under it, and the top margin covers only the overlap. The bottom margin stays whole, and the
        // space under the note is .cm-content padding, so the box reaches past it. The scroll padding in editor.css also
        // holds the keyboard, because the browser scrolls against the layout viewport.
        // The find bar adds to the top margin, or to the bottom while it sits above the keyboard bar.
        EditorView.scrollMargins.of((v) => editorScrollMargins({
          barHeight: barHeightNow() + find.topHeight(),
          scrollerTop: v.scrollDOM.getBoundingClientRect().top,
          kbarHeight: (kbar.hidden ? 0 : kbarHeightNow()) + find.dockHeight(),
        })),
        // Completion menus and other tooltips stay above the on-screen keyboard and the keyboard bar.
        tooltips({ tooltipSpace: () => tooltipSpaceRect(viewportBox(), (kbar.hidden ? 0 : kbarHeightNow()) + find.dockHeight()) }),
        modeCompartment.of([]),
        focusLineCompartment.of([]),
        autocompletion({
          override: [slashSource, wikiSource],
          icons: false,
          activateOnTyping: true,
          tooltipClass: () => "ms-completion",
          addToOptions: [{ render: (completion) => h("span", { class: "ms-completion-icon", "aria-hidden": "true" }, completion.msIcon ? icon(completion.msIcon, 16) : null), position: 20 }],
        }),
        askBar(askConfig()),
        trackedChanges(trackedConfig()),
        imageActions(imageConfig()),
        Prec.high(keymap.of(editorKeymap)),
        find.extension,
        keymap.of([...historyKeymap, ...defaultKeymap]),
        EditorView.inputHandler.of((v, from, to, text) => {
          const sel = v.state.selection.main;
          if (!canEdit || text.length !== 1 || !(text in PAIRS) || sel.empty || from !== sel.from || to !== sel.to) return false;
          return runText(wrapPair, text);
        }),
        EditorView.domEventHandlers({ paste: onPaste, drop: onDrop, dragover: onDragOver, click: onClick }),
        EditorView.updateListener.of(onUpdate),
      ],
    }),
  });

  find.bind(view);

  /** Context used by the widgets in the live preview. */
  view.msCtx = {
    isOnline,
    resolveLink: (target) => resolveLink(target),
    bindWikiPill: (link, target, posOf) => bindWikiPill(link, target, posOf),
    imageSrcNow: (url) => imageSrcNow(url),
    fileUrlOf: (id) => {
      const f = filesById.get(id);
      return f ? f.url || "" : "";
    },
    selectImage: (from, to) => selectImage(from, to),
    openHref: (href) => flush().then(() => navigate(href)),
    retryUpload: (key) => {
      if (sync.retryUpload) Promise.resolve(sync.retryUpload(key)).catch((err) => notify("The upload could not restart: " + errMessage(err), { variant: "critical" }));
    },
    renderHtml: (md) => {
      if (!render) return "";
      try { return render(md, renderOptions()).html || ""; } catch { return ""; }
    },
    buildBubble: () => buildBubble(),
  };

  function errMessage(err) {
    return (err && err.message) || "error";
  }

  /**
   * Shows a message. Failures and warnings go to a Banner above the note, as
   * docs/primer.md 5.7 places editor messages, and the rest is announced.
   * @param {string} message
   * @param {{variant?: "info"|"success"|"warning"|"critical", actions?: object[]}} [opts]
   */
  function notify(message, { variant = "info", actions = [] } = {}) {
    if (unmounted) return;
    if (variant !== "warning" && variant !== "critical") {
      announce(message);
      return;
    }
    let el = null;
    const dismiss = variant === "critical" ? [{ label: "Dismiss", onClick: () => { if (el) el.remove(); } }] : [];
    el = banner({ variant, title: message, dismissible: variant !== "critical", actions: [...actions, ...dismiss].slice(0, 2), into: bannerArea });
  }

  function keyboardOffset() {
    const vv = window.visualViewport;
    return vv ? Math.max(0, window.innerHeight - vv.height - vv.offsetTop) : 0;
  }

  /** Reads the window and visual viewport sizes for tooltipSpaceRect. */
  function viewportBox() {
    const vv = window.visualViewport;
    return { width: document.documentElement.clientWidth, height: window.innerHeight, vvTop: vv ? vv.offsetTop : 0, vvHeight: vv ? vv.height : window.innerHeight };
  }

  // Image file sizes from the local store let image widgets reserve their space before they load.
  view.state.field(widgetHeightsField).trackWidth(view.contentDOM);

  function onUpdate(u) {
    if (u.docChanged) {
      const remote = u.transactions.some((tr) => tr.annotation(remoteAnnotation));
      if (!remote) {
        changeSeq++;
        scheduleSave();
        fadeSoon();
      }
      scheduleStats();
      if (titleLinked && !remote && u.startState.doc.line(1).text !== u.state.doc.line(1).text) followTitleHeading();
    }
    if (u.selectionSet && typewriter && u.view.hasFocus) {
      requestAnimationFrame(() => {
        if (unmounted) return;
        view.dispatch({ effects: EditorView.scrollIntoView(view.state.selection.main.head, { y: "center" }) });
      });
    }
    // Keyboard focus that lands in the note shows the caret. A caret outside the rendered lines or off screen draws
    // nothing, so the caret line scrolls into view. A click sets the selection where it lands and scrolls nothing.
    if (u.focusChanged && u.view.hasFocus) requestAnimationFrame(revealCaretOnFocus);
    if (u.selectionSet && imageTapped && !selectedImage(u.state)) imageTapped = false;
    if (u.focusChanged || u.selectionSet || u.startState.field(trackedField, false) !== u.state.field(trackedField, false)
      || u.startState.field(askBarField, false) !== u.state.field(askBarField, false)) updateKbar();
  }

  /** Scrolls the caret into view when the note has focus and the caret is not on screen. */
  function revealCaretOnFocus() {
    if (unmounted || !view.hasFocus) return;
    const head = view.state.selection.main.head;
    const c = view.coordsAtPos(head);
    const bottom = window.visualViewport ? window.visualViewport.height : window.innerHeight;
    if (c && c.top >= barHeightNow() + find.topHeight() && c.bottom <= bottom - (kbar.hidden ? 0 : kbarHeightNow()) - find.dockHeight()) return;
    view.dispatch({ effects: EditorView.scrollIntoView(head, { y: "nearest" }) });
  }

  /* ----- Title and the first heading ----- */

  /**
   * Links the title field to a first-line H1 that repeats the title in live
   * preview. The field then hides and the heading is the title. Hiding the
   * field leaves the CodeMirror height map alone, unlike a decoration that
   * collapses the heading.
   */
  function updateTitleLink() {
    const linked = mode === "live" && !!leadingTitleRange(view.state.doc.sliceString(0, Math.min(view.state.doc.length, 2000)), titleEl.value);
    if (linked === titleLinked) return;
    titleLinked = linked;
    titleEl.hidden = linked;
    root.classList.toggle("has-title-heading", linked);
  }

  /** Copies an edited first-line heading into the title, which saves and renames the note as a title edit does. */
  function followTitleHeading() {
    const m = /^#[ \t]+(.*?)(?:[ \t]+#+)?[ \t]*$/.exec(view.state.doc.line(1).text);
    if (!m) {
      titleLinked = false;
      titleEl.hidden = false;
      root.classList.remove("has-title-heading");
      return;
    }
    const next = m[1].trim();
    if (next && next !== titleEl.value) {
      titleEl.value = next;
      scheduleSave();
    }
  }

  /* ----- Text commands ----- */

  /** Applies a {text, selectionStart, selectionEnd} result as one change. */
  function applyResult(res, userEvent = "input") {
    if (!res) return false;
    const d = diffRange(view.state.doc.toString(), res.text);
    const spec = { selection: { anchor: res.selectionStart, head: res.selectionEnd }, scrollIntoView: true, userEvent };
    if (d.start !== d.end || d.insert) spec.changes = { from: d.start, to: d.end, insert: d.insert };
    view.dispatch(spec);
    return true;
  }

  /** Runs a pure text helper on the document and the main selection. */
  function runText(fn, ...args) {
    if (!canEdit) return false;
    const s = view.state.selection.main;
    return applyResult(fn(view.state.doc.toString(), s.from, s.to, ...args));
  }

  /* ----- Autosave and status ----- */

  function isDirty() {
    return saveTimer !== 0 || changeSeq !== savedSeq || (titleEl.value.trim() !== "" && titleEl.value !== lastSaved.title);
  }

  function scheduleSave() {
    if (!canEdit) return;
    cancel(saveTimer);
    saveTimer = later(() => { saveTimer = 0; save(); }, SAVE_DELAY);
    setStatusView("saving", "Saving");
  }

  function save() {
    saving = saving.then(async () => {
      const seq = changeSeq;
      const title = titleEl.value;
      const content = view.state.doc.toString();
      const patch = {};
      if (title.trim() !== "" && title !== lastSaved.title) patch.title = title;
      if (content !== lastSaved.content) patch.content = content;
      if (Object.keys(patch).length === 0) {
        savedSeq = seq;
        if (!unmounted) updateStatus();
        return;
      }
      try {
        const saved = await sync.updateNote(note.id, patch);
        lastSaved = { title: patch.title !== undefined ? title : lastSaved.title, content };
        if (saved && typeof saved === "object") note = { ...note, ...saved };
        savedSeq = seq;
        editedLocally = true;
        saveError = null;
      } catch (err) {
        saveError = err || new Error("save failed");
      }
      if (!unmounted) updateStatus();
    });
    return saving;
  }

  function flush() {
    if (saveTimer) { cancel(saveTimer); saveTimer = 0; }
    if (!canEdit) return Promise.resolve();
    return save();
  }

  function syncStatus() {
    let st = null;
    try { st = sync && sync.status ? sync.status() : null; } catch { st = null; }
    return st || { online: isOnline(), pending: 0, syncing: false };
  }

  let statusState = "";
  /** Draws the save state: an Octicon or a Spinner, and text that is visually hidden on narrow viewports. */
  function setStatusView(state, text) {
    if (state !== statusState) {
      statusState = state;
      root.dataset.save = state;
      statusIcon.replaceChildren(state === "saving" ? spinner({ size: "small", label: "", delay: 1000 }) : icon(STATUS_ICONS[state] || "check", 16));
    }
    if (statusText.textContent !== text) statusText.textContent = text;
  }

  function updateStatus() {
    if (unmounted) return;
    if (!canEdit) {
      setStatusView("readonly", access === "comment" ? "Comment access" : "View only");
      return;
    }
    const st = syncStatus();
    if (saveError) {
      setStatusView("error", "Not saved");
      if (!saveBanner || !saveBanner.isConnected) {
        saveBanner = banner({
          variant: "critical",
          title: "Your changes are not saved",
          description: "The note could not be saved on this device: " + errMessage(saveError) + ".",
          actions: [{ label: "Retry", primary: true, onClick: () => { changeSeq++; return flush(); } }],
          into: bannerArea,
        });
      }
      return;
    }
    if (saveBanner) {
      saveBanner.remove();
      saveBanner = null;
    }
    if (isDirty() || st.syncing) setStatusView("saving", st.syncing ? "Syncing" : "Saving");
    else if (st.authRequired) setStatusView("warn", "Saved on this device, sign in to sync");
    else if (!st.online) setStatusView("offline", st.pending > 0 ? "Offline, saved on this device" : "Offline");
    else if (st.pending > 0) setStatusView("local", "Saved on this device only");
    else setStatusView("synced", "Saved");
  }

  function updateStats() {
    statsTimer = 0;
    const { words, minutes } = wordStats(view.state.doc.toString());
    statusCount.textContent = words.toLocaleString() + (words === 1 ? " word" : " words") + (minutes ? " · " + minutes + " min read" : "");
  }

  function scheduleStats() {
    if (!statsTimer) statsTimer = later(updateStats, 300);
  }

  /* ----- Modes and reading view ----- */

  function isPhone() {
    return phoneMq.matches;
  }

  function setMode(next, remember) {
    if (!MODES.includes(next)) next = "live";
    const prev = mode;
    mode = next;
    if (remember) writePref("mode", next);
    root.dataset.mode = next;
    seg.setSelected(next);
    view.dispatch({ effects: modeCompartment.reconfigure(next === "source" ? [] : [livePlugin, blockField(!!render)]) });
    updateTitleLink();
    if (next === "reading") renderReading();
    else requestAnimationFrame(() => { if (!unmounted) view.requestMeasure(); });
    // The find bar keeps its query and moves the current match to the new mode.
    find.modeChanged(prev, next);
  }

  async function ensureRender() {
    if (render) return render;
    try {
      render = (await import("/static/js/markdown.js")).render || null;
    } catch {
      render = null;
    }
    return render;
  }

  /** The options markdown.render takes to resolve links, embeds and images. */
  function renderOptions() {
    return resolver ? { resolveItem: resolver.resolveItem, resolveImage: (url) => resolver.resolveImage(url) } : { resolveImage: (url) => url };
  }

  function renderReading() {
    if (mode !== "reading" || unmounted) return;
    readInner.textContent = "";
    // A note that opens with its title as an H1 shows only that heading.
    if (!leadingTitleRange(view.state.doc.sliceString(0, 2000), titleEl.value)) {
      readInner.appendChild(h("h1", { class: "ms-preview-title", text: titleEl.value || "Untitled" }));
    }
    if (!render) {
      readInner.appendChild(h("pre", { class: "ms-reading-fallback", text: view.state.doc.toString() }));
      find.readingRendered();
      return;
    }
    try {
      const out = render(view.state.doc.toString(), renderOptions());
      const body = h("div");
      // markdown.render builds its HTML from escaped text and a fixed tag set.
      body.innerHTML = out && typeof out.html === "string" ? out.html : "";
      if (resolver) resolver.attach(body);
      readInner.append(...body.childNodes);
    } catch (err) {
      readInner.appendChild(inlineMessage({ variant: "critical", text: "The reading view could not be drawn: " + errMessage(err) }));
    }
    // A new render drops the find marks, so an open find bar marks the matches again.
    find.readingRendered();
  }

  function noteHref(n) {
    const slug = n.spaceSlug || spaceSlug;
    const path = String(n.path || "").replace(/\.md$/i, "").split("/").map(encodeURIComponent).join("/");
    return slug && path ? "/s/" + encodeURIComponent(slug) + "/n/" + path : "/n/" + encodeURIComponent(n.id);
  }

  /** The URL of an item, as app.hrefFor builds it in the shell. */
  function itemHref(kind, rec) {
    if (kind === "note") return noteHref(rec);
    const slug = rec.spaceSlug || spaceSlug;
    if (!slug) return "/i/" + encodeURIComponent(rec.id);
    return "/s/" + encodeURIComponent(slug) + (kind === "folder" ? "/d/" : "/f/") + encodePath(rec.path);
  }

  // The shell's app when it gives one, and otherwise the parts of it that buildItemResolver reads.
  const app = options.app || { store, isOnline, fileCache: options.fileCache || null, hrefFor: (kind, rec) => itemHref(kind, rec) };

  /** Resolves a link target through buildItemResolver, or null before it loads. */
  function resolveTarget(target, form, embed = false) {
    if (!resolver || !target) return null;
    try { return resolver.resolveItem(target, { form, embed }) || null; } catch { return null; }
  }

  /** Resolves a wikilink target. exists is null until the resolver loads. */
  function resolveLink(target) {
    const raw = String(target || "");
    const hash = raw.indexOf("#");
    const name = (hash >= 0 ? raw.slice(0, hash) : raw).trim();
    const anchor = hash >= 0 ? "#" + raw.slice(hash + 1) : "";
    if (!resolver) return { href: "#", exists: null, note: null, kind: "" };
    const hit = name ? resolveTarget(name, name.includes("/") ? "pathWikilink" : "wikilink") : null;
    if (!hit) return { href: "#", exists: false, note: null, kind: "" };
    const n = hit.kind === "note" ? notesByHref.get(hit.href) || null : null;
    return { href: hit.href + (hit.kind === "note" ? anchor : ""), exists: true, kind: hit.kind, note: n, fileUrl: hit.fileUrl || "", mime: hit.mime || "", name: hit.name || "" };
  }

  /** Maps a file URL to what an img may load, a blob URL for a file still on this device. */
  function imageSrcNow(url) {
    const u = String(url || "");
    return resolver && u ? resolver.resolveImage(u) : u;
  }

  async function imageSrc(url) {
    const u = String(url || "");
    if (isRelativeDestination(u)) {
      const hit = resolveTarget(u, "markdownImage", true);
      return hit && hit.fileUrl ? imageSrcNow(hit.fileUrl) : "";
    }
    return imageSrcNow(u);
  }

  /** Finds the file id of an image reference for AI context, through the resolver. */
  function fileIdForUrl(url) {
    const u = String(url || "");
    if (isRelativeDestination(u)) {
      const hit = resolveTarget(u, "markdownImage", true);
      return hit && hit.kind === "file" ? fileIdFromUrl(hit.fileUrl) : "";
    }
    return fileIdFromUrl(u);
  }

  /**
   * Builds the link resolver from the local store, with the notes by href and
   * the image sizes, then redraws the preview. Later builds replace earlier
   * ones, and the replaced resolver frees its blob URLs after the widgets had
   * time to load them.
   */
  async function rebuildResolver() {
    indexTimer = 0;
    if (!store || typeof store.listItems !== "function" || !note.spaceId) return;
    const seq = ++resolverSeq;
    try {
      if (!spaceSlug && store.getSpace) {
        const space = await store.getSpace(note.spaceId);
        if (space) spaceSlug = space.slug || "";
      }
      const [mod, items] = await Promise.all([import("/static/js/fileviews.js"), store.listItems(note.spaceId)]);
      const next = typeof store.snapshot === "function" ? await mod.buildItemResolver(app, { ...note, content: "" }) : null;
      if (unmounted || seq !== resolverSeq) {
        if (next) next.destroy();
        return;
      }
      const old = resolver;
      resolver = next;
      notesByHref = new Map((items.notes || []).filter((n) => !n.deleted).map((n) => [app.hrefFor("note", n), n]));
      filesById = new Map((items.files || []).map((f) => [f.id, f]));
      const heights = view.state.field(widgetHeightsField, false);
      if (heights) for (const f of items.files || []) if (f.id && f.width > 0 && f.height > 0) heights.setDims("id:" + f.id, f.width, f.height);
      view.dispatch({ effects: [setLinks.of(resolver ? { resolve: (target, form, embed) => resolveTarget(target, form, embed) } : null), refreshLive.of(null)] });
      if (mode === "reading") renderReading();
      if (old) later(() => old.destroy(), 30000);
    } catch (err) {
      console.error("[markspace] the link resolver could not be built", err);
    }
  }

  /* ----- Completion: slash menu and wikilinks ----- */

  function inCode(state, pos) {
    for (let n = syntaxTree(state).resolveInner(pos, -1); n; n = n.parent) {
      if (n.name === "FencedCode" || n.name === "InlineCode" || n.name === "CodeBlock") return true;
    }
    return false;
  }

  function slashSource(context) {
    if (!canEdit) return null;
    const line = context.state.doc.lineAt(context.pos);
    const trig = slashTrigger(line.text, context.pos - line.from);
    if (!trig || inCode(context.state, context.pos)) return null;
    const items = filterSlashItems(SLASH_ITEMS, trig.query, { online: isOnline() });
    if (!items.length) return null;
    return {
      from: line.from + trig.start,
      filter: false,
      options: slashCompletionOptions(items, trig.query).map(({ item, label, detail, section }) => ({
        type: item.disabled ? "disabled" : undefined,
        label,
        detail,
        section,
        msIcon: item.icon,
        apply: (v, _c, from, to) => runSlash(item, from, to),
      })),
    };
  }

  /**
   * Lists the notes and files a [[ link can name, from the local store. A
   * file appears by its name, or by its path when the name would resolve to
   * another item.
   */
  async function wikiSource(context) {
    const line = context.state.doc.lineAt(context.pos);
    const trig = wikiTrigger(line.text, context.pos - line.from);
    if (!trig || !store) return null;
    const q = trig.query.trim();
    let candidates = [];
    try {
      const items = store.listItems ? await store.listItems(note.spaceId) : { notes: store.listNotes ? await store.listNotes(note.spaceId) : [], files: [] };
      const notes = (items.notes || []).filter((n) => !n.deleted).map((n) => ({ id: n.id, title: n.title || "Untitled", kind: "note", rec: n, updatedAt: n.updatedAt }));
      const files = (items.files || []).filter((f) => !f.deleted).map((f) => ({ id: f.id, title: f.name, kind: "file", rec: f, updatedAt: f.updatedAt }));
      const all = [...notes, ...files];
      if (q) {
        const lower = q.toLowerCase();
        candidates = all.filter((x) => x.title.toLowerCase().includes(lower));
        if (store.searchNotes) {
          const hits = await store.searchNotes(q, { spaceId: note.spaceId, limit: 30 });
          const byId = new Map(notes.map((x) => [x.id, x]));
          for (const hit of hits || []) if (hit.note && byId.has(hit.note.id)) candidates.push(byId.get(hit.note.id));
        }
      } else {
        candidates = all.sort((a, b) => String(b.updatedAt).localeCompare(String(a.updatedAt)));
      }
    } catch { candidates = []; }
    const ranked = rankWikiMatches(candidates, q, { excludeId: note.id, limit: 12 });
    return {
      from: line.from + trig.start,
      filter: false,
      options: ranked.map((x) => ({
        label: x.title,
        detail: String(x.rec.path || "").replace(/\.md$/, ""),
        msIcon: x.kind === "note" ? "note" : fileIconName(x.rec.mime, x.rec.name),
        apply: (v, _c, from, to) => {
          const res = completeWikilink(v.state.doc.sliceString(0, v.state.doc.length), from, to, wikiText(x));
          applyResult(res, "input.complete");
        },
      })),
    };
  }

  /** The text of a wikilink to a note or file: its title or name, or its path when that is ambiguous. */
  function wikiText(x) {
    const byName = resolveTarget(x.title, x.title.includes("/") ? "pathWikilink" : "wikilink");
    const path = x.kind === "note" ? String(x.rec.path || "").replace(/\.md$/, "") : String(x.rec.path || x.title);
    const byPath = path ? resolveTarget(path, "pathWikilink") : null;
    if (!byName || !byPath || byName.href === byPath.href || !path) return x.title;
    return path;
  }

  function runSlash(item, from, to) {
    view.dispatch({ changes: { from, to, insert: "" }, selection: { anchor: from }, userEvent: "input.complete" });
    const text = view.state.doc.toString();
    const s = from;
    switch (item.id) {
      case "h1": case "h2": case "h3": case "bullet": case "number": case "task": case "quote":
        applyResult(setBlock(text, s, s, item.id, { toggle: false }));
        break;
      case "code":
        applyResult(insertBlock(text, s, s, "```\n\n```", 4));
        break;
      case "table":
        applyResult(insertBlock(text, s, s, "| Column | Column |\n| --- | --- |\n| Cell | Cell |", 2, 8));
        break;
      case "divider":
        applyResult(insertBlock(text, s, s, "---\n", 4));
        break;
      case "wikilink":
        view.dispatch({ changes: { from: s, insert: "[[" }, selection: { anchor: s + 2 } });
        startCompletion(view);
        break;
      case "image":
        pickFiles("image/*");
        break;
      case "file":
        pickFiles("");
        break;
      case "ai":
        askAt(s, s);
        break;
      case "image-gen":
        generateImageAt(s);
        break;
      default:
    }
  }

  function pickFiles(accept) {
    if (accept) fileInput.accept = accept;
    else fileInput.removeAttribute("accept");
    fileInput.click();
  }

  /* ----- Selection toolbar and inline AI ----- */

  function buildBubble() {
    const tool = (label, glyph, hint, run) => {
      const b = pressKeepsSelection(iconButton({ icon: glyph, label, keybindingHint: hint, size: "small" }));
      b.addEventListener("click", (ev) => { ev.preventDefault(); run(b); });
      return b;
    };
    const labelled = (label, glyph, run, cls) => {
      const b = pressKeepsSelection(button({ label, leadingVisual: glyph, variant: "invisible", size: "small", className: cls }));
      b.addEventListener("click", (ev) => { ev.preventDefault(); run(b); });
      return b;
    };
    const divider = () => h("span", { class: "cm-ms-bubble-divider", "aria-hidden": "true" });
    const barEl = h("div", { class: "cm-ms-bubble", role: "toolbar", "aria-label": "Selection" },
      tool("Bold", "bold", MOD + "B", () => runText(toggleInline, "**")),
      tool("Italic", "italic", MOD + "I", () => runText(toggleInline, "_")),
      tool("Strikethrough", "strikethrough", MOD + "Shift+X", () => runText(toggleInline, "~~")),
      tool("Code", "code", MOD + "E", () => runText(toggleCode)),
      tool("Link", "link", MOD + "K", () => runText(toggleLink)),
      isOnline() || canEdit ? divider() : null,
      isOnline() ? labelled("Comment", "comment", () => commentOnSelection(), "is-comment") : null,
      canEdit ? labelled("Ask", "sparkle", (b) => { const sel = view.state.selection.main; askAt(sel.from, sel.to, { anchor: b }); }, "is-ai") : null);
    rovingToolbar(barEl);
    return barEl;
  }

  /** Opens an ActionMenu for the editor. On narrow viewports it fills the screen. */
  function contextMenu(anchor, items, placement = "bottom-start", { refocus = false } = {}) {
    return openMenu(anchor, items.filter(Boolean), { placement, onClose: refocus ? () => { if (!unmounted && !document.querySelector("[data-overlay]:popover-open, dialog[open]")) view.focus(); } : undefined });
  }

  /** Opens the ask bar over [from, to], or explains that AI needs a connection. */
  function askAt(from, to, extra = {}) {
    if (!isOnline()) {
      showAiOffline(extra.anchor || null);
      return null;
    }
    if (focusMode) setFocus(false);
    return openAskBar(view, { from, to, ...extra });
  }

  /** Opens the ask bar in image mode at pos, when a model can generate images. */
  async function generateImageAt(pos) {
    if (!isOnline()) {
      showAiOffline(null);
      return;
    }
    const caps = await aiCapabilities(callApi);
    if (unmounted) return;
    if (!caps.generate) {
      notify("No model can generate images", { variant: "warning", actions: [{ label: "AI settings", href: user && user.isAdmin ? "/admin/ai" : "/settings/ai" }] });
      return;
    }
    askAt(pos, pos, { mode: "image" });
  }

  /** Selects the image at [from, to] after a tap on its widget. */
  function selectImage(from, to) {
    imageTapped = true;
    selectImageAt(view, from, to);
    if (isPhone()) updateKbar();
  }

  /** Opens a cited source: a note in the app, a web page or a file in a new tab. */
  async function openSource(src) {
    if (!src) return;
    if (src.noteId) {
      await flush();
      navigate("/n/" + encodeURIComponent(src.noteId));
    } else if (src.url) {
      window.open(src.url, "_blank", "noopener,noreferrer");
    } else if (src.path) {
      window.open(src.path, "_blank", "noopener");
    }
  }

  /** Puts text into the note as a tracked insertion at the cursor. */
  function insertTracked(str, { title = "Insert" } = {}) {
    if (!canEdit || !str) return;
    const pos = view.state.selection.main.to;
    const before = view.state.doc.sliceString(Math.max(0, pos - 1), pos);
    const lead = before && before !== "\n" ? "\n\n" : "";
    startChange(view, { title, author: "AI", from: pos, to: pos, text: lead + str, status: "open" });
    view.dispatch({ effects: EditorView.scrollIntoView(pos, { y: "center" }) });
  }

  /** Stores a live AI result as a suggestion for later review. */
  async function saveAsSuggestion(change) {
    const mod = anchorsMod || (await loadAnchors());
    const body = view.state.doc.toString();
    const edits = change.hunks.map((hk) => {
      const anchor = hk.to > hk.from && mod ? mod.makeAnchor(body, hk.from, hk.to) : null;
      return {
        anchor: anchor || { exact: "", prefix: body.slice(Math.max(0, hk.from - 32), hk.from), suffix: body.slice(hk.from, hk.from + 32), start: hk.from, end: hk.from },
        text: hk.insert,
      };
    });
    // The anchors describe this device's text, so the server gets that text first.
    const pushed = async () => {
      await flush();
      if (sync && typeof sync.syncNow === "function") {
        try { await sync.syncNow(); } catch { /* the request below reports the result */ }
      }
    };
    const post = () => callApi("POST", "/api/notes/" + encodeURIComponent(note.id) + "/suggestions", { title: change.title || "AI change", edits });
    try {
      await pushed();
      try {
        await post();
      } catch (err) {
        // A push that was still running when the request left can miss the latest text, so a conflict retries once.
        if (!err || err.status !== 409) throw err;
        await pushed();
        await post();
      }
      announce("Saved as a suggestion");
      return true;
    } catch (err) {
      if (err && err.status === 404) {
        suggestionsRoute = false;
        view.dispatch({ effects: refreshTracked.of(null) });
        notify("Suggestions are not available on this server", { variant: "warning" });
      } else {
        notify("The suggestion could not be saved: " + errMessage(err), { variant: "critical" });
      }
      return false;
    }
  }

  /** Accepts or discards edits of a stored suggestion on the server. */
  async function resolveStoredSuggestion({ suggestion, editIds, action }) {
    if (!suggestion) return;
    try {
      await callApi("POST", "/api/suggestions/" + encodeURIComponent(suggestion.id) + "/" + action, { editIds });
      if (sync && typeof sync.syncNow === "function") sync.syncNow().catch(() => {});
    } catch (err) {
      notify("The suggestion could not be updated: " + errMessage(err), { variant: "critical" });
    }
  }

  /** Checks once whether the server has the suggestions route, which shows Save as suggestion. */
  async function probeSuggestions() {
    if (!canEdit || !isOnline()) return;
    try {
      await callApi("GET", "/api/notes/" + encodeURIComponent(note.id) + "/suggestions?status=open");
      suggestionsRoute = true;
    } catch (err) {
      suggestionsRoute = err && err.status === 404 ? false : null;
    }
    if (!unmounted) view.dispatch({ effects: refreshTracked.of(null) });
  }

  function askConfig() {
    return {
      note: () => ({ ...note, title: titleEl.value, content: view.state.doc.toString() }),
      api: (...args) => callApi(...args),
      store,
      render: (md, extra = {}) => (render ? render(md, { ...renderOptions(), ...extra }) : { html: "" }),
      isOnline, isPhone: () => isPhone(), canEdit,
      enabled: () => mode !== "reading",
      dock: (el) => {
        kbarDock.replaceChildren(...(el ? [el] : []));
        updateKbar();
        if (el) requestAnimationFrame(revealAskTarget);
      },
      // contextRefs makes the shell mount the tab again with the seed.
      openAITab: (seed) => openPanel("ai", { seed, contextRefs: [{ kind: "selection", text: seed.answer }] }),
      generateImage: (v, spec) => startImageGeneration(v, spec),
      resolveWikilink: async (target) => {
        const hit = resolveLink(target);
        if (!hit.note) return null;
        return store && store.getNote ? (await store.getNote(hit.note.id)) || hit.note : hit.note;
      },
      openSource: (src) => openSource(src),
      onChange: () => updateKbar(),
    };
  }

  function trackedConfig() {
    return {
      isPhone: () => isPhone(),
      canSaveSuggestion: () => suggestionsRoute === true && isOnline(),
      onSaveSuggestion: (change) => saveAsSuggestion(change),
      onSuggestion: (e) => resolveStoredSuggestion(e),
    };
  }

  function imageConfig() {
    return {
      api: (...args) => callApi(...args),
      note: () => ({ ...note, content: view.state.doc.toString() }),
      isOnline, isPhone: () => isPhone(), canEdit,
      enabled: () => mode === "live",
      imageSrc: (url) => imageSrc(url),
      resolveFileId: (url) => fileIdForUrl(url),
      imageSave: () => readPref("aiImageSave", "new"),
      settingsHref: user && user.isAdmin ? "/admin/ai" : "/settings/ai",
      notify: (message, opts) => notify(message, opts),
      onChange: () => updateKbar(),
    };
  }

  /* ----- Links, wikilinks and images ----- */

  async function openWiki(target, anchor = null) {
    const hit = resolveLink(target);
    if (hit.exists) {
      await flush();
      navigate(hit.href);
      return;
    }
    if (!canEdit || !sync.createNote) {
      if (anchor) anchoredOverlay(anchor, inlineMessage({ variant: "unavailable", text: "No note or file is named " + target }), { label: "Link target missing", focus: false });
      announce("No note or file is named " + target);
      return;
    }
    // Creating acts at once. Undo removes the new note and goes back.
    const back = location.pathname + location.search;
    let created = null;
    await undoable({
      label: "Created “" + target + "”",
      failLabel: "The note could not be created.",
      run: async () => {
        await flush();
        created = await sync.createNote({ spaceId: note.spaceId, parentId: note.parentId || "", title: target, content: "", tags: [], icon: "" });
        await navigate(noteHref({ ...created, spaceSlug: created.spaceSlug || spaceSlug }));
      },
      undo: async () => {
        if (!created) return;
        if (sync.trashNote) await sync.trashNote(created.id);
        if (sync.deleteNote) await sync.deleteNote(created.id);
        await navigate(back);
      },
    });
  }

  /** Binds the link's handlers. posOf() returns the link's current start, read from the DOM when a handler runs. */
  function bindWikiPill(link, target, posOf) {
    let hoverTimer = 0;
    let pressTimer = 0;
    let pressed = false;
    link.addEventListener("mousedown", (ev) => ev.preventDefault());
    link.addEventListener("click", (ev) => {
      ev.preventDefault();
      if (pressed) { pressed = false; return; }
      openWiki(target, link);
    });
    // The hover preview is off unless the profile setting turns it on.
    link.addEventListener("pointerenter", (ev) => {
      if (ev.pointerType !== "mouse" || readPref("linkPreview", "0") !== "1") return;
      hoverTimer = setTimeout(() => wikiPreview(link, target, posOf()), 450);
    });
    link.addEventListener("pointerleave", () => clearTimeout(hoverTimer));
    link.addEventListener("pointerdown", (ev) => {
      if (ev.pointerType === "mouse") return;
      pressTimer = setTimeout(() => { pressed = true; wikiActions(link, target, posOf()); }, 500);
    });
    for (const type of ["pointerup", "pointercancel", "pointermove"]) link.addEventListener(type, () => clearTimeout(pressTimer));
    link.addEventListener("contextmenu", (ev) => { ev.preventDefault(); clearTimeout(hoverTimer); wikiActions(link, target, posOf()); });
  }

  /** Lists the actions of a wikilink: open or create, edit the link, summarize the linked note. */
  function wikiActions(anchor, target, from) {
    const hit = resolveLink(target);
    contextMenu(anchor, [
      { label: hit.exists ? "Open" : "Create note", icon: hit.exists ? "link" : "plus", onClick: () => openWiki(target, anchor) },
      canEdit ? { label: "Edit link", icon: "pencil", onClick: () => { view.dispatch({ selection: { anchor: from + 2 } }); view.focus(); } } : null,
      hit.note ? { label: "Summarize linked note", icon: "sparkle", onClick: () => askAt(from + 2, from + 2, { action: "summarizeLinked", run: true }) } : null,
    ], "bottom-start");
  }

  async function wikiPreview(anchor, target, from) {
    const hit = resolveLink(target);
    const fresh = hit.note && store && store.getNote ? await store.getNote(hit.note.id) : hit.note;
    if (unmounted || !anchor.isConnected) return;
    const excerptEl = h("div", { class: "ms-wiki-excerpt md-body" });
    if (fresh) {
      // Drop a leading heading that repeats the title shown above the excerpt.
      const source = String(fresh.content || "").replace(/^\s*#\s+(.*)\n+/, (m, t) => (t.trim().toLowerCase() === String(fresh.title || "").trim().toLowerCase() ? "" : m)).slice(0, 900);
      const html = render ? view.msCtx.renderHtml(source) : "";
      if (html) excerptEl.innerHTML = html; // markdown.render output only.
      else excerptEl.textContent = source || "This note is empty.";
    } else {
      excerptEl.textContent = hit.exists ? hit.name || target : "No note has this name yet.";
    }
    let pop = null;
    const actions = h("div", { class: "ms-wiki-actions" },
      button({ label: hit.exists ? "Open" : "Create note", variant: "primary", size: "small", onClick: () => { pop.close(); openWiki(target); } }),
      canEdit ? button({ label: "Edit link", size: "small", onClick: () => { pop.close(); view.dispatch({ selection: { anchor: from + 2 } }); view.focus(); } }) : null,
      fresh ? button({ label: "Summarize", size: "small", onClick: () => { pop.close(); askAt(from + 2, from + 2, { action: "summarizeLinked", run: true }); } }) : null);
    const title = fresh ? fresh.title || target : target;
    const content = h("div", { class: "ms-wiki-preview" }, h("h2", { class: "ms-wiki-title", text: title }), excerptEl, actions);
    pop = anchoredOverlay(anchor, content, { placement: "bottom-start", width: "small", label: title, className: "ms-wiki-popover" });
  }

  function linkMenu(anchor, url, from) {
    const internal = isRelativeDestination(url) ? resolveTarget(url, "markdownLink") : null;
    const href = internal ? internal.href : url;
    contextMenu(anchor, [
      { label: internal ? "Open" : "Open link", icon: "link", onClick: () => openUrl(href) },
      { label: "Copy link", icon: "copy", onClick: () => copyText(new URL(href, location.href).href) },
      canEdit ? { label: "Edit link", icon: "pencil", onClick: () => { view.dispatch({ selection: { anchor: from + 1 } }); view.focus(); } } : null,
      /^https?:\/\//.test(url) ? { label: "Summarize page", icon: "sparkle", onClick: () => askAt(from + 1, from + 1, { action: "summarizePage", run: true }) } : null,
    ], "bottom-start");
  }

  function openUrl(href) {
    if (href.startsWith("/")) flush().then(() => navigate(href));
    else window.open(href, "_blank", "noopener,noreferrer");
  }

  async function copyText(text) {
    try { await navigator.clipboard.writeText(text); announce("Copied"); } catch { announce("Copy failed", { politeness: "assertive" }); }
  }

  function onClick(ev, v) {
    const link = ev.target.closest && ev.target.closest(".cm-ms-link");
    const passage = ev.target.closest && ev.target.closest(".cm-ms-comment");
    // A plain click on a commented passage focuses its thread and keeps the caret there.
    if (passage && !link && v.state.selection.main.empty) {
      const id = passage.getAttribute("data-comment-id");
      if (id) later(() => showComments({ focusId: id }), 0);
    }
    if (!link || mode === "source") return false;
    const url = link.getAttribute("data-url") || "";
    if (!url) return false;
    if (ev.ctrlKey || ev.metaKey) {
      ev.preventDefault();
      const internal = isRelativeDestination(url) ? resolveTarget(url, "markdownLink") : null;
      openUrl(internal ? internal.href : url);
      return true;
    }
    if (ev.pointerType === "touch" || ev.pointerType === "pen" || isPhone() || !canEdit) {
      ev.preventDefault();
      linkMenu(link, url, v.posAtDOM(link));
      return true;
    }
    return false;
  }

  /* ----- Uploads ----- */

  function onPaste(ev) {
    if (!canEdit || !ev.clipboardData) return false;
    const files = ev.clipboardData.files;
    if (files && files.length) {
      ev.preventDefault();
      uploadFiles([...files]);
      return true;
    }
    const s = view.state.selection.main;
    const res = linkFromPaste(view.state.doc.sliceString(0, view.state.doc.length), s.from, s.to, ev.clipboardData.getData("text/plain"));
    if (res) { ev.preventDefault(); applyResult(res, "input.paste"); return true; }
    return false;
  }

  function onDragOver(ev) {
    if (canEdit && ev.dataTransfer && [...ev.dataTransfer.types].includes("Files")) ev.preventDefault();
    return false;
  }

  function onDrop(ev, v) {
    if (!canEdit || !ev.dataTransfer || !ev.dataTransfer.files || !ev.dataTransfer.files.length) return false;
    ev.preventDefault();
    const pos = v.posAtCoords({ x: ev.clientX, y: ev.clientY });
    if (pos !== null) v.dispatch({ selection: { anchor: pos } });
    uploadFiles([...ev.dataTransfer.files]);
    return true;
  }

  /** Saves each file into the note's folder, or the paste folder, and inserts a relative link as sync.uploadAttachment returns it. */
  async function uploadFiles(files) {
    if (!canEdit) return;
    for (const file of files) {
      const name = file.name || "file";
      const image = /^image\//.test(file.type || "");
      let res;
      try {
        res = await sync.uploadAttachment(note.id, file);
      } catch (err) {
        notify("“" + name + "” could not be added: " + errMessage(err), { variant: "critical" });
        continue;
      }
      if (unmounted) return;
      await Promise.all([rebuildResolver(), refreshUploads()]);
      if (unmounted) return;
      const md = attachmentMarkdown(res.name || name, res.url, image);
      const s = view.state.selection.main;
      const doc = view.state.doc;
      const line = doc.lineAt(s.from);
      const before = image && s.from > line.from ? "\n" : "";
      const after = image && s.to < doc.lineAt(s.to).to ? "\n" : "";
      const insert = before + md + after;
      view.dispatch({ changes: { from: s.from, to: s.to, insert }, selection: { anchor: s.from + insert.length }, scrollIntoView: true, userEvent: "input.upload" });
    }
  }

  let uploadsKey = "";
  /** Reads the upload queue of the note's files. It dispatches only when an upload changed. */
  async function refreshUploads() {
    if (!sync.uploads || unmounted) return;
    let items = [];
    try { items = (await sync.uploads()) || []; } catch { items = []; }
    if (unmounted) return;
    const map = new Map(items.filter((u) => u.fileId && (u.noteId === note.id || !u.noteId)).map((u) => [u.fileId, u]));
    const key = [...map.values()].map((u) => u.fileId + ":" + u.state + ":" + (u.loaded || 0) + ":" + (u.error || "")).join("|");
    if (key === uploadsKey) return;
    const finished = [...map.values()].some((u) => u.state === "done") && uploadsKey !== "";
    uploadsKey = key;
    view.dispatch({ effects: setUploads.of(map) });
    // A finished upload changes the file record, so the resolver reads the store again.
    if (finished && !indexTimer) indexTimer = later(rebuildResolver, 200);
  }

  /* ----- Remote changes ----- */

  let remoteBanner = null;
  function clearRemoteBanner() {
    if (remoteBanner) remoteBanner.remove();
    remoteBanner = null;
  }

  /** Replaces the document with fresh store content as one minimal change. */
  function loadText(fresh) {
    const next = fresh.content || "";
    const d = diffRange(view.state.doc.toString(), next);
    if (d.start !== d.end || d.insert) {
      view.dispatch({
        changes: { from: d.start, to: d.end, insert: d.insert },
        annotations: [remoteAnnotation.of(true), Transaction.addToHistory.of(false)],
      });
    }
    if (document.activeElement !== titleEl || titleEl.value === lastSaved.title) titleEl.value = fresh.title || "";
    lastSaved = { title: fresh.title || "", content: next };
    cancel(saveTimer);
    saveTimer = 0;
    savedSeq = changeSeq;
    note = { ...note, ...fresh };
    titleLinked = false;
    titleEl.hidden = false;
    updateTitleLink();
    if (mode === "reading") renderReading();
    updateStatus();
  }

  function showRemoteBanner(message, copyId) {
    clearRemoteBanner();
    const loadTheirs = {
      label: "Load their version",
      onClick: async () => {
        clearRemoteBanner();
        const fresh = store && store.getNote ? await store.getNote(note.id) : null;
        if (fresh && !unmounted) loadText(fresh);
      },
    };
    const actions = copyId
      ? [loadTheirs, { label: "Open my copy", onClick: async () => { await flush(); navigate("/n/" + encodeURIComponent(copyId)); } }]
      : [{ label: "Keep mine", primary: true, onClick: () => { clearRemoteBanner(); changeSeq++; flush(); } }, loadTheirs];
    remoteBanner = banner({ variant: "warning", title: message, actions, into: bannerArea, dismissible: !!copyId });
  }

  async function onRemoteChange() {
    if (!store || !store.getNote) return;
    const fresh = await store.getNote(note.id);
    if (!fresh || unmounted) return;
    if (fresh.deleted) {
      clearRemoteBanner();
      remoteBanner = banner({ variant: "warning", title: "This note was moved to the trash on another device", into: bannerArea });
      return;
    }
    const docText = view.state.doc.toString();
    const content = fresh.content || "";
    const title = fresh.title || "";
    if (content === docText && title === titleEl.value) {
      lastSaved = { title, content };
      note = { ...note, ...fresh };
      updateStatus();
      return;
    }
    if (content === lastSaved.content && title === lastSaved.title) return;
    const localEdits = isDirty() || (editedLocally && syncStatus().pending > 0);
    if (!canEdit || !localEdits) loadText(fresh);
    else showRemoteBanner("This note changed on another device");
  }

  /* ----- Focus mode, fading chrome, keyboard bar ----- */

  function setFocus(onOff, remember = true) {
    focusMode = onOff;
    root.classList.toggle("is-focus", onOff);
    focusBtn.setAttribute("aria-pressed", String(onOff));
    const slot = focusBtn.querySelector(".btn-visual");
    if (slot) slot.replaceChildren(icon(onOff ? "screen-normal" : "screen-full", 16));
    if (remember) writePref("focus", onOff ? "1" : "0");
    try { setChrome({ immersive: onOff }); } catch { /* shell may be gone */ }
    if (!onOff) unfade();
    view.dispatch({ effects: focusLineCompartment.reconfigure(onOff ? highlightActiveLine() : []) });
  }

  function unfade() {
    cancel(fadeTimer);
    fadeTimer = 0;
    root.classList.remove("is-faded");
  }

  function fadeSoon() {
    if (!focusMode || fadeTimer || root.classList.contains("is-faded")) return;
    fadeTimer = later(() => { fadeTimer = 0; if (focusMode) root.classList.add("is-faded"); }, FADE_DELAY);
  }

  function setTypewriter(onOff) {
    typewriter = onOff;
    writePref("typewriter", onOff ? "1" : "0");
    typeBtn.setAttribute("aria-pressed", String(onOff));
    root.classList.toggle("is-typewriter", onOff);
    if (onOff) view.dispatch({ effects: EditorView.scrollIntoView(view.state.selection.main.head, { y: "center" }) });
  }

  /** Runs a keyboard bar action with the editor focused, so the phone keyboard stays open. */
  const withFocus = (fn) => () => {
    view.focus();
    fn();
  };

  /** Opens the More formatting ActionMenu of the keyboard bar. */
  /**
   * Scrolls the end of the line that holds the selection above the keyboard bar, so the text the docked ask bar
   * works on stays in view. The scroll margins already include the bar.
   */
  function revealAskTarget() {
    if (unmounted || !kbarDock.firstChild) return;
    // A tracked change shows its header above the first hunk. The header scrolls into view, which the document
    // scroll padding keeps clear of the editor bar and the keyboard bar.
    const header = hasChanges(view.state) && cmHost.querySelector(".change-block .change-header");
    if (header) {
      // Outside the band between the editor bar and the keyboard bar, the header's top aligns under the editor bar,
      // so its buttons show even when the band is shorter than the header and its margins.
      const r = header.getBoundingClientRect();
      const bottom = (window.visualViewport ? window.visualViewport.height : window.innerHeight) - (kbar.hidden ? 0 : kbarHeightNow());
      if (r.top < barHeightNow() + 8 || r.bottom > bottom - 8) header.scrollIntoView({ block: "start" });
      return;
    }
    // A link pill on the last line is taller than the caret, so the line keeps 16px above the bar.
    const line = view.state.doc.lineAt(view.state.selection.main.to);
    view.dispatch({ effects: EditorView.scrollIntoView(line.to, { y: "nearest", yMargin: 16 }) });
  }

  function kbarMore(anchor) {
    const overflow = kbarOverflow.map((a) => ({ label: a.label, icon: a.icon, onClick: withFocus(() => a.run()) }));
    contextMenu(anchor, [
      ...overflow,
      ...(overflow.length ? [{ separator: true }] : []),
      { label: "Strikethrough", icon: "strikethrough", onClick: withFocus(() => runText(toggleInline, "~~")) },
      { label: "Code", icon: "code", onClick: withFocus(() => runText(toggleCode)) },
      { label: "Quote", icon: "quote", onClick: withFocus(() => runText(setBlock, "quote")) },
      { label: "Numbered list", icon: "list-ordered", onClick: withFocus(() => runText(setBlock, "number")) },
      { separator: true },
      { label: "Image", icon: "image", onClick: withFocus(() => pickFiles("image/*")) },
      { label: "File", icon: "paperclip", onClick: withFocus(() => pickFiles("")) },
      { label: "Insert block", icon: "plus", onClick: withFocus(() => {
        const { from, to } = view.state.selection.main;
        const line = view.state.doc.lineAt(from);
        const lead = from > line.from && !/\s/.test(view.state.doc.sliceString(from - 1, from)) ? " " : "";
        view.dispatch({ changes: { from, to, insert: lead + "/" }, selection: { anchor: from + lead.length + 1 } });
        startCompletion(view);
      }) },
      { separator: true },
      // Below 382px the editor bar has no Find button, so find opens from here. The menu refocuses the note when it
      // closes, so the bar opens on the next frame and keeps focus in the Find field.
      findBtn.getClientRects().length ? null : { label: "Find and replace", icon: "search", onClick: () => requestAnimationFrame(() => { if (!unmounted) find.open({ replace: canEdit }); }) },
      { label: "Undo", icon: "undo", onClick: withFocus(() => undo(view)) },
    ], "top-end", { refocus: true });
  }

  const askFromKbar = (btn) => {
    const sel = view.state.selection.main;
    askAt(sel.from, sel.to, { anchor: btn });
  };
  // Eight 40 px IconButtons with 44 px hit areas fit a 360 px screen. On a narrower column the actions named in
  // KBAR_COLLAPSE move into More, in that order, until the rest fit.
  const KBAR_COLLAPSE = ["link", "task", "bullet", "heading"];
  let kbarOverflow = [];
  const kbarActions = [
    { id: "bold", label: "Bold", icon: "bold", run: () => runText(toggleInline, "**") },
    { id: "italic", label: "Italic", icon: "italic", run: () => runText(toggleInline, "_") },
    { id: "heading", label: "Heading", icon: "heading", run: () => runText(cycleHeading) },
    { id: "bullet", label: "Bullet list", icon: "list-unordered", run: () => runText(setBlock, "bullet") },
    { id: "task", label: "Task", icon: "tasklist", run: () => runText(setBlock, "task") },
    { id: "link", label: "Link", icon: "link", run: () => runText(toggleLink) },
    { id: "ai", label: "Ask AI", icon: "sparkle", keepFocus: false, ai: true, run: (btn) => askFromKbar(btn) },
    { id: "more", label: "More formatting", icon: "kebab-horizontal", keepFocus: false, run: (btn) => kbarMore(btn) },
  ];
  // With text selected on a phone the bar holds the selection actions instead.
  const selectionActions = () => [
    { id: "bold", label: "Bold", icon: "bold", run: () => runText(toggleInline, "**") },
    { id: "italic", label: "Italic", icon: "italic", run: () => runText(toggleInline, "_") },
    { id: "link", label: "Link", icon: "link", run: () => runText(toggleLink) },
    { id: "code", label: "Code", icon: "code", run: () => runText(toggleCode) },
    isOnline() ? { id: "comment", label: "Comment", icon: "comment", keepFocus: false, run: () => commentOnSelection() } : null,
    canEdit ? { id: "ai", label: "Ask AI", icon: "sparkle", keepFocus: false, ai: true, run: (btn) => askFromKbar(btn) } : null,
    { id: "more", label: "More selection actions", icon: "kebab-horizontal", keepFocus: false, run: (btn) => selectionMore(btn) },
  ].filter(Boolean);
  /** Opens the More ActionMenu of the selection tools. Find and replace starts with the selected text. */
  function selectionMore(anchor) {
    contextMenu(anchor, [
      { label: "Strikethrough", icon: "strikethrough", onClick: withFocus(() => runText(toggleInline, "~~")) },
      // The editor bar has no Find button below 382px. The menu refocuses the note when it closes, so the bar opens on
      // the next frame and keeps focus in the Find field.
      { label: "Find and replace", icon: "search", onClick: () => requestAnimationFrame(() => { if (!unmounted) find.open({ replace: canEdit }); }) },
    ], "top-end", { refocus: true });
  }

  // While tracked changes are open on a phone, the bar reviews them. Accept all and Discard all sit in More.
  const reviewKbarActions = () => {
    const byId = Object.fromEntries(reviewActions(view).map((a) => [a.id, a]));
    const item = (id, glyph) => ({ id, label: byId[id].label, icon: glyph, disabled: byId[id].disabled, keepFocus: false, run: () => { byId[id].run(); updateKbar(); } });
    return [
      item("prev", "chevron-up"), item("next", "chevron-down"), item("accept", "check"), item("discard", "x"),
      { id: "more", label: "More review actions", icon: "kebab-horizontal", keepFocus: false, run: (btn) => contextMenu(btn, [
        { label: "Accept all", icon: "check", disabled: byId["accept-all"].disabled, onClick: () => { byId["accept-all"].run(); updateKbar(); } },
        { label: "Discard all", icon: "x", disabled: byId["discard-all"].disabled, onClick: () => { byId["discard-all"].run(); updateKbar(); } },
      ], "top-end", { refocus: true }) },
    ];
  };
  // A tapped image puts its actions in the bar: six buttons and More for the rest.
  const imageBarActions = () => {
    const list = imageKbarActions(view);
    const shown = list.length > 7 ? list.slice(0, 6) : list;
    const rest = list.length > 7 ? list.slice(6) : [];
    const out = shown.map((a) => ({ id: "image-" + a.id, label: a.label, icon: a.icon, disabled: a.disabled && !a.ai, ai: a.ai, keepFocus: false, run: () => a.run() }));
    if (rest.length) out.push({ id: "more", label: "More image actions", icon: "kebab-horizontal", keepFocus: false, run: (btn) => contextMenu(btn, rest.map((a) => ({ label: a.label, icon: a.icon, disabled: a.disabled, onClick: () => a.run() }))) });
    return out;
  };

  let kbarMode = "";
  let kbarKind = "format";
  // Width of the column the keyboard bar spans, written by placeKbarColumn.
  let kbarWidth = 0;
  // The last written left and right edges of the keyboard bar, as "left/right".
  let kbarColumn = "";
  // Height of the bar, or -1 until read again. The layout key and the last written styles let updates skip unchanged writes.
  let kbarHeight = -1;
  let kbarKey = "";
  let kbarPlaced = null;
  const resetKbarFocus = rovingToolbar(kbarTools);

  /** Draws the tools of the keyboard bar for "format", "selection", "review" or "image". */
  function renderKbarTools(next) {
    kbarKind = next;
    let key = next + ":" + isOnline();
    if (next === "review") {
      const st = reviewState(view.state);
      key += ":" + st.index + "/" + st.total + ":" + listChanges(view.state).map((c) => c.status).join();
    } else if (next === "image") {
      const img = selectedImage(view.state);
      key += ":" + (img ? img.from + img.url + img.alt : "") + ":" + imageKbarActions(view).map((a) => a.id + a.disabled).join();
    }
    let actions = next === "selection" ? selectionActions() : next === "review" ? reviewKbarActions() : next === "image" ? imageBarActions() : kbarActions;
    kbarOverflow = [];
    if (next === "format") {
      const slots = kbarSlots();
      for (const id of KBAR_COLLAPSE) {
        if (actions.length - kbarOverflow.length <= slots) break;
        kbarOverflow.push(actions.find((a) => a.id === id));
      }
      actions = actions.filter((a) => !kbarOverflow.includes(a));
      key += ":" + actions.length;
    }
    if (key === kbarMode) return;
    kbarMode = key;
    const name = { selection: "Selection", review: "Review changes", image: "Image actions" }[next] || "Formatting";
    kbar.setAttribute("aria-label", name);
    kbarTools.setAttribute("aria-label", name);
    kbarTools.replaceChildren();
    if (next === "review") {
      const st = reviewState(view.state);
      kbarTools.appendChild(h("span", { class: "ms-kbar-count", "aria-live": "polite", text: st.total ? (st.index >= 0 ? st.index + 1 : "–") + " of " + st.total : "" }));
    }
    const online = isOnline();
    for (const a of actions) {
      const btn = iconButton({ icon: a.icon, label: a.label, size: "large", iconSize: 24, tooltip: false, className: "ms-kbar-btn", attrs: { "data-kbar": a.id } });
      pressKeepsSelection(btn);
      const inactive = a.ai && !online;
      if (a.disabled) btn.setAttribute("aria-disabled", "true");
      if (inactive) {
        btn.setAttribute("aria-disabled", "true");
        btn.setAttribute("data-inactive", "true");
      }
      btn.addEventListener("click", () => {
        if (a.ai && !isOnline()) {
          showAiOffline(btn);
          return;
        }
        if (a.disabled) return;
        if (a.keepFocus !== false && !view.hasFocus) view.focus();
        a.run(btn);
      });
      kbarTools.appendChild(btn);
    }
    resetKbarFocus();
  }
  /** How many 40 px buttons with 4 px gaps fit the tools row, which is at most 480 px wide inside 8 px padding. */
  function kbarSlots() {
    const width = Math.min(480, (kbarWidth || document.documentElement.clientWidth || 360) - 16);
    return Math.max(4, Math.floor((width + 4) / 44));
  }
  renderKbarTools("format");

  function updateKbar() {
    if (unmounted) return;
    const phone = isPhone();
    const editing = canEdit && phone && mode !== "reading" && (view.hasFocus || document.activeElement === titleEl || kbar.contains(document.activeElement));
    const docked = phone && !!kbarDock.firstChild;
    const review = phone && mode !== "reading" && hasChanges(view.state);
    const image = phone && mode === "live" && (imageTapped || view.hasFocus) && !!selectedImage(view.state);
    let next = "format";
    if (review) next = "review";
    else if (image) next = "image";
    else if (editing && view.hasFocus && !view.state.selection.main.empty) next = "selection";
    renderKbarTools(next);
    const toolsHidden = docked && !review;
    const hidden = !(editing || docked || review || image);
    // Each write happens only on a change, so a caret move that keeps the bar as it is costs no style or layout work.
    if (kbarTools.hidden !== toolsHidden) kbarTools.hidden = toolsHidden;
    if (kbar.hidden !== hidden) kbar.hidden = hidden;
    const key = [hidden, toolsHidden, kbarMode, kbarDock.childElementCount].join("|");
    if (key !== kbarKey) {
      kbarKey = key;
      kbarHeight = -1;
      root.classList.toggle("has-kbar", !hidden);
      // The editor can move without resizing, for example when the sidebar closes and the rail opens.
      if (!hidden) placeKbarColumn();
    }
    placeKbar();
  }

  /** Returns the bar height, read from the DOM only after the bar changed. */
  function kbarHeightNow() {
    if (kbarHeight < 0) kbarHeight = kbar.hidden ? 0 : kbar.offsetHeight;
    return kbarHeight;
  }

  // Below this viewport height on a phone, which in practice means the keyboard is open, the editor bar scrolls away
  // instead of sticking, so a landscape phone keeps room for the text and its completion menu.
  const SHORT_VIEWPORT = 320;
  let isShort = false;
  let vvhWritten = "";
  function placeKbar() {
    const vv = window.visualViewport;
    const vvh = Math.round(vv ? vv.height : window.innerHeight) + "px";
    if (vvh !== vvhWritten) {
      vvhWritten = vvh;
      document.documentElement.style.setProperty("--ms-vvh", vvh);
    }
    // The viewport meta sets interactive-widget=resizes-content. Android then shrinks the layout viewport for the
    // keyboard and leaves the visual viewport without an offset, so the height alone decides on a phone.
    const short = isPhone() && (vv ? vv.height : window.innerHeight) < SHORT_VIEWPORT;
    if (short !== isShort) {
      isShort = short;
      root.classList.toggle("is-short", short);
      document.documentElement.style.setProperty("--ms-bar-h", short ? "0px" : "48px");
    }
    // While the keyboard is open on a phone, or the phone viewport is short, an open find bar sits above the keyboard
    // bar and adds to the space below. Browser toolbars and zoom move the visual viewport by less than 120px, which a
    // keyboard always exceeds.
    const offset = keyboardOffset();
    const kbarH = kbar.hidden ? 0 : kbarHeightNow();
    const findDock = find.placeDock({ docked: isPhone() && (offset > 120 || short), bottom: offset + kbarH, left: kbar.style.left, right: kbar.style.right });
    const next = kbarStyle({ hidden: kbar.hidden && !findDock, offset, height: kbarH + findDock });
    const changes = kbarStyleChanges(kbarPlaced, next);
    if (!changes) return;
    kbarPlaced = next;
    if ("transform" in changes) kbar.style.transform = changes.transform;
    // The document scroll padding and the space under the note read this property.
    if ("space" in changes) {
      document.documentElement.style.setProperty("--ms-kbar-space", changes.space);
      // An open completion menu was placed against the old space, so it measures again.
      repositionTooltips(view);
      // A docked ask bar that grows keeps its target line in view.
      if (kbarDock.firstChild) requestAnimationFrame(revealAskTarget);
    }
  }

  // The keyboard bar spans the editor column only, so a rail pane beside the editor stays uncovered in landscape.
  // The column edges are read when the editor resizes, never while scrolling.
  function placeKbarColumn() {
    if (unmounted) return;
    const r = root.getBoundingClientRect();
    const left = Math.max(0, Math.round(r.left));
    const right = Math.max(0, Math.round(document.documentElement.clientWidth - r.right));
    const key = left + "/" + right;
    if (key === kbarColumn) return;
    kbarColumn = key;
    kbar.style.left = left + "px";
    kbar.style.right = right + "px";
    const width = Math.max(0, document.documentElement.clientWidth - left - right);
    if (width !== kbarWidth) {
      kbarWidth = width;
      // The number of buttons that fit can change with the width.
      if (kbarKind === "format") renderKbarTools("format");
    }
  }
  const rootResize = typeof ResizeObserver === "function" ? new ResizeObserver(() => placeKbarColumn()) : null;
  if (rootResize) rootResize.observe(root);
  on(window, "resize", placeKbarColumn);

  // The bar height changes with its content and the screen width. The observer reports it without forcing a layout.
  const kbarResize = typeof ResizeObserver === "function" ? new ResizeObserver((entries) => {
    const entry = entries[entries.length - 1];
    const box = entry.borderBoxSize && entry.borderBoxSize[0];
    const height = kbar.hidden ? 0 : Math.round(box ? box.blockSize : kbar.offsetHeight);
    if (height === kbarHeight) return;
    kbarHeight = height;
    placeKbar();
  }) : null;
  if (kbarResize) kbarResize.observe(kbar);

  // The editor bar is 48 px. The observer keeps the scroll margin in step without reading layout while scrolling.
  let barHeight = 48;
  const barResize = typeof ResizeObserver === "function" ? new ResizeObserver((entries) => {
    const box = entries[entries.length - 1].borderBoxSize;
    if (box && box[0] && box[0].blockSize > 0) barHeight = Math.round(box[0].blockSize);
  }) : null;
  if (barResize) barResize.observe(bar);
  function barHeightNow() {
    return isShort || (focusMode && root.classList.contains("is-faded")) ? 0 : barHeight;
  }

  /* ----- Panels ----- */

  let apiImpl = options.api || null;
  async function apiFn() {
    if (!apiImpl) apiImpl = (await import("/static/js/api.js")).api;
    return apiImpl;
  }
  const callApi = async (...args) => (await apiFn())(...args);

  function insertText(str, { tracked = false, title = "Insert" } = {}) {
    if (!canEdit) return;
    if (tracked) {
      insertTracked(str, { title });
      return;
    }
    const pos = view.state.selection.main.to;
    view.dispatch({ changes: { from: pos, insert: str }, selection: { anchor: pos + str.length }, scrollIntoView: true, userEvent: "input.ai" });
  }

  function replaceSelection(str) {
    if (!canEdit) return;
    const { from, to } = view.state.selection.main;
    view.dispatch({ changes: { from, to, insert: str }, selection: { anchor: from, head: from + str.length }, userEvent: "input.ai" });
  }

  /** Describes the selection, the note and the section at the cursor for the AI panel. */
  function getSelection(override) {
    const s = override || view.state.selection.main;
    const noteText = view.state.doc.toString();
    const section = sectionAt(noteText, s.from);
    return { text: noteText.slice(s.from, s.to), start: s.from, end: s.to, noteText, heading: section.heading, sectionText: section.text };
  }

  function closePanel() {
    if (panelHandle) { try { panelHandle.unmount(); } catch { /* already gone */ } }
    panelHandle = null;
    drawer.hidden = true;
    root.classList.remove("has-drawer");
  }

  /**
   * Opens a note panel: "comments", "share", "history", "ai" or "files". With
   * the shell's openPanel option it opens the rail tab, and otherwise the
   * editor drawer. extra is merged into the panel options.
   */
  async function openPanel(name, extra = {}) {
    const fns = { comments: openComments, share: openShare, history: openHistory, ai: openAI, files: null };
    if (!(name in fns)) return;
    const { selectionOverride, ...rest } = extra;
    if (typeof options.openPanel === "function" && name !== "comments") {
      if (focusMode) setFocus(false);
      const hooks = { ...rest, getSelection: () => getSelection(selectionOverride) };
      if (canEdit) Object.assign(hooks, { insertText, replaceSelection, insertTracked });
      options.openPanel(name, hooks);
      return;
    }
    if (!isOnline() && name !== "files" && name !== "comments") {
      notify("This panel needs a connection", { variant: "warning" });
      return;
    }
    let fn = fns[name];
    if (name === "files") {
      try { fn = (await import("/static/js/filepanels.js")).openNoteFiles; } catch { fn = null; }
      if (typeof fn !== "function" || unmounted) return;
    }
    closePanel();
    drawer.hidden = false;
    root.classList.add("has-drawer");
    const base = {
      note: { ...note, title: titleEl.value, content: view.state.doc.toString() },
      access, store, sync, user, isOnline, api: callApi, navigate, render,
      onClose: () => { closePanel(); view.focus(); },
      getSelection: () => getSelection(selectionOverride),
    };
    if (canEdit && (name === "ai" || name === "files")) Object.assign(base, { insertText, replaceSelection, insertTracked });
    panelHandle = fn(drawerBody, { ...base, ...rest });
  }

  /* ----- Comments ----- */

  const ownCommentSource = !options.commentSource;
  const commentSource = options.commentSource || createCommentSource({ noteId: note.id, api: callApi, store, isOnline });
  let anchorsMod = null;

  /** Draws the passages of open anchored threads, found again in the current text. */
  function applyCommentThreads() {
    if (unmounted) return;
    const doc = view.state.doc.toString();
    const groups = groupThreads(commentSource.threads, { body: doc, resolve: anchorsMod ? anchorsMod.resolveAnchor : null });
    const ranges = groups.anchored
      .filter((e) => e.start >= 0 && e.end > e.start && e.end <= doc.length && (anchorsMod || doc.slice(e.start, e.end) === e.thread.anchor.exact))
      .map((e) => ({ id: e.thread.id, from: e.start, to: e.end }));
    view.dispatch({ effects: setCommentRanges.of(ranges) });
  }

  /** Emphasises one passage, or clears the emphasis. It does not scroll, so hovering a thread card never moves the page. */
  function setActiveComment(id) {
    if (unmounted) return;
    view.dispatch({ effects: [setActiveCommentId.of(id || null)] });
  }

  let revealTimer = 0;
  /** Scrolls a thread's passage to the middle of the editor, emphasises it and flashes it once. */
  function revealComment(id) {
    if (unmounted || !id) return;
    const range = commentRangesOf(view.state.field(commentField).decos).find((r) => r.id === id);
    if (!range) return;
    view.dispatch({ effects: [setActiveCommentId.of(id), EditorView.scrollIntoView(range.from, { y: "center" })] });
    clearTimeout(revealTimer);
    requestAnimationFrame(() => {
      if (unmounted) return;
      const marks = view.dom.querySelectorAll(`.cm-ms-comment[data-comment-id="${CSS.escape(id)}"]`);
      for (const m of marks) {
        m.classList.remove("is-flash");
        void m.offsetWidth;
        m.classList.add("is-flash");
      }
      revealTimer = setTimeout(() => { for (const m of marks) m.classList.remove("is-flash"); }, 1300);
    });
  }

  /** Opens the comments panel: the shell's rail when it gave a callback, the drawer otherwise. */
  function showComments(extra = {}) {
    if (focusMode) setFocus(false);
    if (typeof options.openComments === "function") options.openComments(extra);
    else openPanel("comments", { ...extra, source: commentSource, getBody: () => view.state.doc.toString(), onActivate: (id) => setActiveComment(id), onReveal: (id) => revealComment(id) });
  }

  /** Starts a comment on the selected text. */
  async function commentOnSelection() {
    const sel = view.state.selection.main;
    if (sel.empty) return;
    const mod = anchorsMod || (await loadAnchors());
    if (!mod) {
      notify("Comments on a selection could not load", { variant: "warning" });
      return;
    }
    const anchor = mod.makeAnchor(view.state.doc.toString(), sel.from, sel.to);
    if (!anchor) return;
    flush();
    showComments({ draftAnchor: anchor });
  }

  unsubs.push(commentSource.subscribe(() => applyCommentThreads()));
  loadAnchors().then((mod) => {
    anchorsMod = mod;
    applyCommentThreads();
  });
  if (commentSource.state.loaded) applyCommentThreads();
  if (ownCommentSource) {
    commentSource.load();
    if (sync && sync.on) {
      // Reload once the outbox drains, so passages reflect anchors the server mapped.
      let busy = false;
      unsubs.push(sync.on("status", (st) => {
        const now = !!(st && ((st.pending || 0) > 0 || st.syncing));
        if (busy && !now) commentSource.load();
        busy = now;
      }));
    }
  }

  /* ----- Suggestions ----- */

  /** Draws stored suggestions as tracked changes. Returns the number drawn. */
  function setSuggestions(list, opts = {}) {
    return drawStoredSuggestions(view, list, {
      resolve: (anchor, text) => {
        // Insertions have no exact text to find, so their prefix and suffix place them.
        if (anchor && !anchor.exact) return insertionPoint(text, anchor);
        if (!anchorsMod) return null;
        const at = anchorsMod.resolveAnchor(text, anchor);
        return at && at.state !== "orphaned" ? at : null;
      },
      ...opts,
    });
  }

  /**
   * Scrolls to the first hunk of a stored suggestion and moves focus to its
   * Accept button: in the start margin on desktop, or in the keyboard bar's
   * review mode on a phone. Returns false when the suggestion is not drawn.
   * @param {string} id the suggestion id
   * @returns {boolean}
   */
  function revealSuggestion(id) {
    if (unmounted || !id) return false;
    const changeId = String(id).startsWith("s-") ? String(id) : "s-" + id;
    if (!listChanges(view.state).some((c) => c.id === changeId)) return false;
    if (mode === "reading") setMode("live", false);
    if (!revealHunk(view, changeId)) return false;
    requestAnimationFrame(() => {
      if (unmounted) return;
      if (isPhone()) {
        view.focus();
        updateKbar();
        const accept = kbarTools.querySelector('[data-kbar="accept"]');
        if (accept) accept.focus({ preventScroll: true });
        return;
      }
      const pair = view.dom.querySelector(`.hunk-pair[data-change-id="${CSS.escape(changeId)}"]`);
      const accept = pair && pair.querySelector(".is-accept");
      if (accept) accept.focus({ preventScroll: true });
    });
    return true;
  }

  /**
   * Moves the cursor to a heading at pos and scrolls it near the top. In
   * reading mode it scrolls to the heading at index in the rendered note.
   */
  function goTo(pos, index = -1) {
    if (unmounted) return;
    if (mode === "reading") {
      const headings = [...readInner.querySelectorAll("h1, h2, h3, h4, h5, h6")].filter((el) => !el.classList.contains("ms-preview-title"));
      if (headings[index]) headings[index].scrollIntoView({ block: "start" });
      return;
    }
    const at = Math.max(0, Math.min(Number(pos) || 0, view.state.doc.length));
    view.dispatch({ selection: { anchor: at }, effects: EditorView.scrollIntoView(at, { y: "start", yMargin: 16 }) });
    view.focus();
  }

  /* ----- Done ----- */

  let closing = false;
  async function done() {
    if (closing) return;
    closing = true;
    try {
      closeAskBar(view);
      await flush();
      if (saveError) { updateStatus(); return; }
      onDone();
    } finally {
      closing = false;
    }
  }

  /* ----- Events ----- */

  on(doneBtn, "click", () => done());
  on(focusBtn, "click", () => setFocus(!focusMode));
  on(typeBtn, "click", () => setTypewriter(!typewriter));
  on(titleEl, "input", () => { scheduleSave(); fadeSoon(); updateTitleLink(); });
  on(titleEl, "focus", updateKbar);
  on(titleEl, "blur", () => later(updateKbar, 150));
  on(titleEl, "keydown", (ev) => {
    if (ev.isComposing || ev.keyCode === 229) return;
    if (ev.key === "Enter" || (ev.key === "ArrowDown" && titleEl.selectionStart === titleEl.value.length)) {
      ev.preventDefault();
      view.focus();
      view.dispatch({ selection: { anchor: 0 }, scrollIntoView: true });
    }
  });
  on(fileInput, "change", () => {
    const files = [...(fileInput.files || [])];
    fileInput.value = "";
    if (files.length) { view.focus(); uploadFiles(files); }
  });
  on(readPane, "click", (ev) => {
    const a = ev.target.closest && ev.target.closest("a[href]");
    if (!a || ev.button !== 0 || ev.metaKey || ev.ctrlKey || ev.shiftKey || a.target === "_blank") return;
    const href = a.getAttribute("href") || "";
    if (href.startsWith("#")) {
      ev.preventDefault();
      const target = href.length > 1 && readInner.querySelector("[id=\"" + CSS.escape(decodeURIComponent(href.slice(1))) + "\"]");
      if (target) target.scrollIntoView({ block: "start" });
    } else if (href.startsWith("/")) {
      ev.preventDefault();
      flush().then(() => navigate(href));
    }
  });
  // A click in the space under the note puts the caret at the end.
  on(page, "mousedown", (ev) => {
    if (ev.target !== page || mode === "reading" || !canEdit) return;
    const rect = cmHost.getBoundingClientRect();
    if (ev.clientY < rect.bottom) return;
    ev.preventDefault();
    view.focus();
    view.dispatch({ selection: { anchor: view.state.doc.length }, scrollIntoView: true });
  });

  let lastPointer = null;
  on(document, "pointermove", (ev) => {
    if (!root.classList.contains("is-faded") && !fadeTimer) return;
    if (lastPointer && Math.abs(ev.clientX - lastPointer.x) + Math.abs(ev.clientY - lastPointer.y) < 6) return;
    lastPointer = { x: ev.clientX, y: ev.clientY };
    unfade();
  }, { passive: true });
  // A tap sends no pointermove on a touch screen, so a pointerdown also brings the chrome back.
  on(document, "pointerdown", () => {
    if (root.classList.contains("is-faded") || fadeTimer) unfade();
  }, { passive: true, capture: true });
  on(root, "focusin", (ev) => { if (bar.contains(ev.target)) unfade(); });

  on(document, "keydown", (ev) => {
    if (ev.defaultPrevented || ev.isComposing) return;
    const inside = root.contains(ev.target) || ev.target === document.body || ev.target === document.documentElement;
    if (!inside) return;
    // Find keys in Reading and outside the note text. CodeMirror handles them inside the note. Only the editor binds them.
    if (find.handleKey(ev)) return;
    const mod = ev.ctrlKey || ev.metaKey;
    const key = ev.key.toLowerCase();
    if (mod && ev.shiftKey && !ev.altKey && (ev.code === "Period" || key === "." || key === ">")) { ev.preventDefault(); setFocus(!focusMode); return; }
    if (mod && !ev.shiftKey && !ev.altKey && key === "s") { ev.preventDefault(); flush(); return; }
    if (ev.key !== "Escape") return;
    if (!drawer.hidden && drawer.contains(ev.target)) {
      ev.preventDefault();
      closePanel();
      view.focus();
      return;
    }
    ev.preventDefault();
    if (focusMode) setFocus(false);
    else done();
  });

  on(phoneMq, "change", () => {
    root.classList.toggle("is-phone", isPhone());
    closeAskBar(view);
    view.dispatch({ effects: refreshTracked.of(null) });
    refreshImageActions(view);
    updateKbar();
  });
  if (window.visualViewport) {
    on(window.visualViewport, "resize", () => {
      placeKbar();
      repositionTooltips(view);
      // The keyboard opening under a docked ask bar moves the bar up without changing its height. The line the bar
      // works on holds the caret, so this reveal replaces the caret scroll below. Both in one frame kept only the last.
      if (kbarDock.firstChild) requestAnimationFrame(revealAskTarget);
      // The caret, or a tapped image whose actions show in the keyboard bar, stays above the bar. A selected image
      // aligns its top under the editor bar, because "nearest" put the start of a tall image at the bar's top edge.
      else if (view.hasFocus || imageTapped) requestAnimationFrame(() => {
        if (unmounted) return;
        const y = typewriter ? "center" : selectedImage(view.state) ? "start" : "nearest";
        view.dispatch({ effects: EditorView.scrollIntoView(view.state.selection.main.head, { y, yMargin: 8 }) });
      });
    });
    on(window.visualViewport, "scroll", placeKbar);
  }
  on(window, "online", () => {
    updateStatus();
    kbarMode = "";
    updateKbar();
    view.dispatch({ effects: [refreshLive.of(null), refreshTracked.of(null)] });
    refreshImageActions(view);
    if (suggestionsRoute === null) probeSuggestions();
  });
  on(window, "offline", () => {
    updateStatus();
    kbarMode = "";
    updateKbar();
    view.dispatch({ effects: refreshTracked.of(null) });
    refreshImageActions(view);
  });
  on(document, "visibilitychange", () => { if (document.visibilityState === "hidden" && isDirty()) flush(); });
  on(window, "pagehide", () => { if (isDirty()) flush(); });

  if (sync && sync.on) {
    unsubs.push(sync.on("status", () => updateStatus()));
    unsubs.push(sync.on("notes-changed", (ev) => {
      if (!ev || !Array.isArray(ev.ids)) return;
      if (ev.ids.includes(note.id) && ev.source === "remote") onRemoteChange();
      if (!indexTimer) indexTimer = later(rebuildResolver, 400);
    }));
    unsubs.push(sync.on("conflict", async (ev) => {
      if (!ev || ev.noteId !== note.id) return;
      const fresh = store && store.getNote ? await store.getNote(note.id) : null;
      if (unmounted || (fresh && (fresh.content || "") === view.state.doc.toString())) return;
      showRemoteBanner("This note changed on another device. Your version was saved as a separate copy.", ev.copyId);
    }));
    unsubs.push(sync.on("uploads-changed", () => refreshUploads()));
  }

  /* ----- Initial state ----- */

  root.classList.toggle("is-phone", isPhone());
  root.classList.toggle("is-typewriter", typewriter);
  setMode(MODES.includes(mode) ? mode : "live", false);
  if (!render) ensureRender().then((fn) => { if (fn && !unmounted) setMode(mode, false); });
  if (canEdit && readPref("focus", "0") === "1") setFocus(true, false);
  updateStatus();
  updateStats();
  rebuildResolver();
  refreshUploads();
  probeSuggestions();
  requestAnimationFrame(() => {
    if (unmounted || !canEdit || mode === "reading" || isPhone()) return;
    if (!titleEl.value && !titleEl.hidden) titleEl.focus({ preventScroll: true });
    else view.focus();
  });

  const handle = {
    view,
    flush,
    isDirty,
    openPanel,
    setActiveComment,
    revealComment,
    insertText,
    insertTracked,
    replaceSelection,
    getSelection: () => getSelection(),
    openAsk: (opts = {}) => {
      const sel = view.state.selection.main;
      return askAt(opts.from ?? sel.from, opts.to ?? sel.to, opts);
    },
    setSuggestions,
    revealSuggestion,
    changes: () => listChanges(view.state),
    goTo,
    // Opens the find bar. opts.state sets the query first, such as the query of the note view's Reading find bar.
    openFind: ({ state = null, ...opts } = {}) => {
      if (state) find.bar.setState(state);
      return find.open(opts);
    },
    unmount() {
      if (unmounted) return;
      if (canEdit && isDirty()) {
        cancel(saveTimer);
        saveTimer = 0;
        save();
      }
      closeAskBar(view);
      stopAllGenerations();
      unmounted = true;
      ac.abort();
      for (const u of unsubs) { try { if (typeof u === "function") u(); } catch { /* ignore */ } }
      for (const t of timers) clearTimeout(t);
      timers.clear();
      closePanel();
      if (kbarResize) kbarResize.disconnect();
      if (barResize) barResize.disconnect();
      if (rootResize) rootResize.disconnect();
      find.destroy();
      const heights = view.state.field(widgetHeightsField, false);
      if (heights) heights.disconnect();
      document.documentElement.style.removeProperty("--ms-kbar-space");
      document.documentElement.style.removeProperty("--ms-vvh");
      document.documentElement.style.removeProperty("--ms-bar-h");
      document.documentElement.classList.remove("ms-editing");
      if (resolver) {
        const old = resolver;
        resolver = null;
        setTimeout(() => old.destroy(), 30000);
      }
      if (focusMode) { try { setChrome({ immersive: false }); } catch { /* ignore */ } }
      view.destroy();
      root.remove();
    },
  };
  // The shell and browser tests reach the handle through the editor element.
  Object.defineProperty(root, "msHandle", { value: handle, configurable: true });
  return handle;
}
