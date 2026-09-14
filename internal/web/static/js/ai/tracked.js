/**
 * Tracked changes for the note editor. One CodeMirror extension draws live AI
 * results and stored suggestions the same way: removed words struck through,
 * inserted words underlined next to them, a header above the first hunk with
 * the author and title, and Accept and Discard controls. The note text keeps
 * the old words until the user accepts a hunk, so autosave never stores an
 * unreviewed result.
 *
 * The diff and hunk functions at the top are pure and have node tests.
 */

import { StateField, StateEffect, EditorView, Decoration, WidgetType, keymap, Prec } from "/static/vendor/codemirror.js";
import { h, button, iconButton, inlineMessage, setBusy } from "/static/js/ui.js";

/* ---------- Pure diff ---------- */

const TOKEN_RE = /[\p{L}\p{N}\p{M}_]+(?:['’][\p{L}\p{N}\p{M}_]+)*|\s+|[^\s\p{L}\p{N}\p{M}_]/gu;
const WORD_RE = /[\p{L}\p{N}]/u;

/** The largest edit distance the token diff searches before it gives up. */
export const MAX_DIFF_DISTANCE = 1500;

/**
 * Splits text into word, whitespace and punctuation tokens. Joining the
 * tokens gives the text back.
 * @param {string} text
 * @returns {string[]}
 */
export function tokenize(text) {
  return String(text || "").match(TOKEN_RE) || [];
}

/** Reports whether a token counts as a word for hunk splitting. */
export function isWordToken(token) {
  return WORD_RE.test(token);
}

/**
 * Myers' diff over two token arrays. It returns a list of [op, token] pairs,
 * or null once the edit distance passes maxD.
 */
function myers(a, b, maxD) {
  const n = a.length;
  const m = b.length;
  const max = n + m;
  const off = max + 1;
  const v = new Int32Array(2 * max + 3);
  const trace = [];
  for (let d = 0; d <= Math.min(max, maxD); d++) {
    trace.push(v.slice(off - d - 1, off + d + 2));
    for (let k = -d; k <= d; k += 2) {
      let x = k === -d || (k !== d && v[off + k - 1] < v[off + k + 1]) ? v[off + k + 1] : v[off + k - 1] + 1;
      let y = x - k;
      while (x < n && y < m && a[x] === b[y]) { x++; y++; }
      v[off + k] = x;
      if (x >= n && y >= m) return backtrack(trace, a, b);
    }
  }
  return null;
}

function backtrack(trace, a, b) {
  const out = [];
  let x = a.length;
  let y = b.length;
  for (let d = trace.length - 1; d >= 0; d--) {
    const row = trace[d];
    const at = (k) => row[k + d + 1];
    const k = x - y;
    const prevK = k === -d || (k !== d && at(k - 1) < at(k + 1)) ? k + 1 : k - 1;
    const prevX = d === 0 ? 0 : at(prevK);
    const prevY = prevX - prevK;
    while (x > prevX && y > prevY) { out.push(["eq", a[x - 1]]); x--; y--; }
    if (d > 0) {
      if (x === prevX) out.push(["ins", b[y - 1]]);
      else out.push(["del", a[x - 1]]);
    }
    x = prevX;
    y = prevY;
  }
  return out.reverse();
}

/**
 * Computes a word-level diff. Adjacent operations of one kind are merged, and
 * a deletion always comes before the insertion that replaces it.
 * @param {string} oldText
 * @param {string} newText
 * @returns {{op: "eq"|"del"|"ins", text: string}[]}
 */
export function diffWords(oldText, newText) {
  const a = tokenize(oldText);
  const b = tokenize(newText);
  let pre = 0;
  while (pre < a.length && pre < b.length && a[pre] === b[pre]) pre++;
  let post = 0;
  while (post < a.length - pre && post < b.length - pre && a[a.length - 1 - post] === b[b.length - 1 - post]) post++;
  const midA = a.slice(pre, a.length - post);
  const midB = b.slice(pre, b.length - post);
  let middle = myers(midA, midB, MAX_DIFF_DISTANCE);
  if (!middle) middle = [...midA.map((t) => ["del", t]), ...midB.map((t) => ["ins", t])];
  const pairs = [...a.slice(0, pre).map((t) => ["eq", t]), ...middle, ...a.slice(a.length - post).map((t) => ["eq", t])];
  const ops = [];
  let pending = { del: "", ins: "" };
  const flush = () => {
    if (pending.del) ops.push({ op: "del", text: pending.del });
    if (pending.ins) ops.push({ op: "ins", text: pending.ins });
    pending = { del: "", ins: "" };
  };
  for (const [op, tok] of pairs) {
    if (op === "eq") {
      flush();
      const last = ops[ops.length - 1];
      if (last && last.op === "eq") last.text += tok;
      else ops.push({ op: "eq", text: tok });
    } else {
      pending[op] += tok;
    }
  }
  flush();
  return ops;
}

/**
 * Splits the diff of oldText and newText into hunks. A hunk is a run of
 * changed words. Two runs separated by fewer than gap unchanged words form one
 * hunk, whose removed and inserted text both include the words between.
 * Offsets are UTF-16 offsets into oldText.
 * @param {string} oldText
 * @param {string} newText
 * @param {{gap?: number}} [opts]
 * @returns {{from: number, to: number, insert: string}[]}
 */
export function computeHunks(oldText, newText, { gap = 3 } = {}) {
  const hunks = [];
  let pos = 0;
  let current = null;
  let between = null;
  for (const { op, text } of diffWords(oldText, newText)) {
    if (op === "eq") {
      if (current) between = { text, from: pos, words: tokenize(text).filter(isWordToken).length };
      pos += text.length;
      continue;
    }
    if (current && between) {
      if (between.words < gap) {
        current.to = between.from + between.text.length;
        current.insert += between.text;
      } else {
        hunks.push(current);
        current = null;
      }
      between = null;
    }
    if (!current) current = { from: pos, to: pos, insert: "" };
    if (op === "del") {
      pos += text.length;
      current.to = pos;
    } else {
      current.insert += text;
    }
  }
  if (current) hunks.push(current);
  return hunks;
}

/**
 * Applies hunks to the text they were computed from. accepted chooses the
 * hunks to apply: a predicate over (hunk, index), a Set of indexes, or null
 * for every hunk.
 * @param {string} oldText
 * @param {{from: number, to: number, insert: string}[]} hunks
 * @param {null|Set<number>|((hunk: object, index: number) => boolean)} [accepted]
 * @returns {string}
 */
export function applyHunks(oldText, hunks, accepted = null) {
  const take = accepted === null ? () => true : accepted instanceof Set ? (_h, i) => accepted.has(i) : accepted;
  const order = hunks.map((hk, i) => [hk, i]).sort((x, y) => x[0].from - y[0].from);
  let out = "";
  let pos = 0;
  for (const [hk, i] of order) {
    if (!take(hk, i)) continue;
    out += oldText.slice(pos, hk.from) + hk.insert;
    pos = hk.to;
  }
  return out + oldText.slice(pos);
}

/* ---------- State ---------- */

let changeSeq = 0;

const addChange = StateEffect.define();
const updateChange = StateEffect.define();
const resolveHunks = StateEffect.define();
const removeChange = StateEffect.define();
const setCurrent = StateEffect.define();
const replaceSuggestions = StateEffect.define();

/** Forces the tracked change decorations to rebuild, for example after a switch between phone and desktop layout. */
export const refreshTracked = StateEffect.define();

/**
 * Builds the hunks of a change over the document range [from, to].
 * @returns {{id: string, from: number, to: number, insert: string}[]}
 */
function hunksFor(doc, from, to, text, seq) {
  return computeHunks(doc.sliceString(from, to), text).map((hk, i) => ({
    id: "h" + seq + "-" + i, from: from + hk.from, to: from + hk.to, insert: hk.insert,
  }));
}

/** Reports whether a changed range [fromA, toA] touches the inside of a hunk. */
function touchesInside(hk, fromA, toA) {
  if (hk.from === hk.to) return fromA < hk.from && toA > hk.from;
  if (fromA === toA) return fromA > hk.from && fromA < hk.to;
  return fromA < hk.to && toA > hk.from;
}

/**
 * The tracked changes of a document: {changes, current, seq}. A change is
 * {id, kind: "ai"|"suggestion", title, author, status: "streaming"|"open"|"error",
 * error, from, to, text, hunks, suggestion}. Hunk positions map through every
 * transaction. An edit inside a hunk ends the review of that hunk and keeps
 * the text as edited.
 */
export const trackedField = StateField.define({
  create: () => ({ changes: [], current: null, seq: 0 }),
  update(value, tr) {
    let { changes, current, seq } = value;
    let dirty = false;
    const resolved = new Set();
    for (const e of tr.effects) if (e.is(resolveHunks)) for (const id of e.value.hunkIds) resolved.add(e.value.changeId + "/" + id);

    if (tr.docChanged && changes.length) {
      dirty = true;
      changes = changes.map((c) => {
        const hunks = [];
        for (const hk of c.hunks) {
          if (resolved.has(c.id + "/" + hk.id)) continue;
          let touched = false;
          tr.changes.iterChangedRanges((fromA, toA) => { if (touchesInside(hk, fromA, toA)) touched = true; });
          if (touched) continue;
          const from = tr.changes.mapPos(hk.from, hk.from === hk.to ? -1 : 1);
          const to = hk.from === hk.to ? from : Math.max(from, tr.changes.mapPos(hk.to, -1));
          hunks.push({ ...hk, from, to });
        }
        const from = tr.changes.mapPos(c.from, 1);
        const to = Math.max(from, tr.changes.mapPos(c.to, -1));
        return { ...c, from, to, hunks };
      });
    }

    for (const e of tr.effects) {
      if (e.is(addChange)) {
        seq++;
        const c = { kind: "ai", author: "AI", title: "", status: "streaming", error: "", text: "", ...e.value, seq, hunks: [] };
        if (c.text) c.hunks = hunksFor(tr.state.doc, c.from, c.to, c.text, seq);
        changes = [...changes.filter((x) => x.id !== c.id), c];
        dirty = true;
      } else if (e.is(updateChange)) {
        changes = changes.map((c) => {
          if (c.id !== e.value.id) return c;
          seq++;
          const next = { ...c, ...e.value, seq };
          if (e.value.text !== undefined) next.hunks = hunksFor(tr.state.doc, next.from, next.to, next.text, seq);
          return next;
        });
        dirty = true;
      } else if (e.is(resolveHunks)) {
        changes = changes.map((c) => (c.id === e.value.changeId ? { ...c, hunks: c.hunks.filter((hk) => !e.value.hunkIds.includes(hk.id)) } : c));
        dirty = true;
      } else if (e.is(removeChange)) {
        changes = changes.filter((c) => c.id !== e.value);
        dirty = true;
      } else if (e.is(replaceSuggestions)) {
        changes = [...changes.filter((c) => c.kind !== "suggestion"), ...e.value];
        dirty = true;
      } else if (e.is(setCurrent)) {
        current = e.value;
        dirty = true;
      } else if (e.is(refreshTracked)) {
        dirty = true;
      }
    }
    if (!dirty) return value;
    // A reviewed change with no hunks left is finished.
    changes = changes.filter((c) => c.status === "streaming" || c.status === "error" || c.hunks.length > 0 || c.keep);
    if (current && !changes.some((c) => c.id === current.changeId && c.hunks.some((hk) => hk.id === current.hunkId))) current = null;
    return { changes, current, seq };
  },
});

/* ---------- Queries ---------- */

/** Lists the tracked changes of a state. */
export function listChanges(state) {
  const v = state.field(trackedField, false);
  return v ? v.changes : [];
}

/** Returns one change by id. */
export function getChange(state, id) {
  return listChanges(state).find((c) => c.id === id) || null;
}

/** Reports whether any change is on screen. */
export function hasChanges(state) {
  return listChanges(state).length > 0;
}

/** Lists every open hunk in document order with its change. */
export function allHunks(state) {
  const out = [];
  for (const c of listChanges(state)) for (const hk of c.hunks) out.push({ change: c, hunk: hk });
  return out.sort((x, y) => x.hunk.from - y.hunk.from || x.hunk.to - y.hunk.to);
}

/**
 * Returns the hunk at pos, or the current hunk chosen with Next or Previous
 * when pos is in none.
 */
export function hunkAt(state, pos) {
  const list = allHunks(state);
  const hit = list.find(({ hunk }) => pos >= hunk.from && pos <= hunk.to);
  if (hit) return hit;
  const cur = state.field(trackedField, false)?.current;
  return cur ? list.find(({ change, hunk }) => change.id === cur.changeId && hunk.id === cur.hunkId) || null : null;
}

/** The change under the cursor, or the only change. */
export function changeAt(state, pos) {
  const hit = hunkAt(state, pos);
  if (hit) return hit.change;
  const list = listChanges(state);
  const inRange = list.find((c) => pos >= c.from && pos <= c.to);
  if (inRange) return inRange;
  return list.length === 1 ? list[0] : null;
}

/**
 * Describes the review position for the phone keyboard bar.
 * @returns {{total: number, index: number, change: object|null, hunk: object|null}}
 */
export function reviewState(state) {
  const list = allHunks(state);
  const hit = hunkAt(state, state.selection.main.head);
  const index = hit ? list.findIndex((x) => x.hunk.id === hit.hunk.id && x.change.id === hit.change.id) : -1;
  return { total: list.length, index, change: hit ? hit.change : null, hunk: hit ? hit.hunk : null };
}

/* ---------- Commands ---------- */

/**
 * Opens a live change over [from, to]. text may be empty while a stream
 * starts. Returns the change id.
 * @param {EditorView} view
 * @param {{id?: string, title: string, author?: string, from: number, to: number, text?: string, status?: string, keep?: boolean}} spec
 */
export function startChange(view, spec) {
  const id = spec.id || "c" + ++changeSeq;
  view.dispatch({ effects: addChange.of({ ...spec, id }) });
  return id;
}

/** Updates the text, status or error of a live change and recomputes its hunks. */
export function streamChange(view, id, patch) {
  if (!getChange(view.state, id)) return false;
  view.dispatch({ effects: updateChange.of({ ...patch, id }) });
  return true;
}

/** Removes a change from the screen without touching the note. */
export function closeChange(view, id) {
  if (!getChange(view.state, id)) return false;
  view.dispatch({ effects: removeChange.of(id) });
  return true;
}

let trackedConfig = {};

/**
 * Accepts hunks of a change as one transaction with userEvent "input.ai", so
 * one undo step reverts them. Without hunkIds every hunk is accepted. A stored
 * suggestion is handed to onSuggestion and the note is left to sync.
 */
export function acceptHunks(view, changeId, hunkIds = null) {
  const c = getChange(view.state, changeId);
  if (!c || c.status === "streaming") return false;
  const hunks = c.hunks.filter((hk) => !hunkIds || hunkIds.includes(hk.id));
  if (!hunks.length) return false;
  const ids = hunks.map((hk) => hk.id);
  if (c.kind === "suggestion") {
    if (typeof trackedConfig.onSuggestion === "function") trackedConfig.onSuggestion({ suggestion: c.suggestion, editIds: hunks.map((hk) => hk.editId), action: "accept" });
    view.dispatch({ effects: resolveHunks.of({ changeId, hunkIds: ids, accepted: true }) });
    return true;
  }
  const last = hunks[hunks.length - 1];
  view.dispatch({
    changes: hunks.map((hk) => ({ from: hk.from, to: hk.to, insert: hk.insert })),
    effects: resolveHunks.of({ changeId, hunkIds: ids, accepted: true }),
    selection: { anchor: view.state.changes(hunks.map((hk) => ({ from: hk.from, to: hk.to, insert: hk.insert }))).mapPos(last.to, 1) },
    userEvent: "input.ai",
    scrollIntoView: true,
  });
  return true;
}

/** Discards hunks of a change. The note does not change. */
export function discardHunks(view, changeId, hunkIds = null) {
  const c = getChange(view.state, changeId);
  if (!c) return false;
  if (c.status === "streaming" && typeof trackedConfig.onStop === "function") trackedConfig.onStop(c);
  const hunks = c.hunks.filter((hk) => !hunkIds || hunkIds.includes(hk.id));
  if (c.kind === "suggestion" && hunks.length && typeof trackedConfig.onSuggestion === "function") {
    trackedConfig.onSuggestion({ suggestion: c.suggestion, editIds: hunks.map((hk) => hk.editId), action: "discard" });
  }
  if (!hunkIds) {
    view.dispatch({ effects: removeChange.of(changeId) });
    if (typeof trackedConfig.onDiscard === "function") trackedConfig.onDiscard(c);
    return true;
  }
  if (!hunks.length) return false;
  view.dispatch({ effects: resolveHunks.of({ changeId, hunkIds: hunks.map((hk) => hk.id), accepted: false }) });
  return true;
}

/** Accepts every hunk of the change under the cursor, or of the only change. */
export function acceptAll(view, changeId = null) {
  const c = changeId ? getChange(view.state, changeId) : changeAt(view.state, view.state.selection.main.head);
  return c ? acceptHunks(view, c.id) : false;
}

/** Discards every hunk of the change under the cursor, or of the only change. */
export function discardAll(view, changeId = null) {
  const c = changeId ? getChange(view.state, changeId) : changeAt(view.state, view.state.selection.main.head);
  return c ? discardHunks(view, c.id) : false;
}

/** Accepts or discards the hunk at the cursor. */
export function resolveHunkAt(view, accept) {
  const hit = hunkAt(view.state, view.state.selection.main.head);
  if (!hit) return false;
  return accept ? acceptHunks(view, hit.change.id, [hit.hunk.id]) : discardHunks(view, hit.change.id, [hit.hunk.id]);
}

/** Moves the cursor to the next (dir 1) or previous (dir -1) hunk, wrapping around. */
export function moveToHunk(view, dir) {
  const list = allHunks(view.state);
  if (!list.length) return false;
  const { index } = reviewState(view.state);
  const head = view.state.selection.main.head;
  let next;
  if (index >= 0) next = (index + dir + list.length) % list.length;
  else if (dir > 0) next = Math.max(0, list.findIndex((x) => x.hunk.from > head));
  else {
    const before = list.map((x, i) => [x, i]).filter(([x]) => x.hunk.to < head);
    next = before.length ? before[before.length - 1][1] : list.length - 1;
  }
  const target = list[next];
  view.dispatch({
    selection: { anchor: target.hunk.from },
    effects: [setCurrent.of({ changeId: target.change.id, hunkId: target.hunk.id }), EditorView.scrollIntoView(target.hunk.from, { y: "center" })],
  });
  return true;
}

/**
 * Moves the cursor to one hunk, marks it current and scrolls it to the
 * middle of the editor. Returns false when the hunk is gone.
 * @param {EditorView} view
 * @param {string} changeId
 * @param {string} [hunkId] the first hunk when left out
 * @returns {boolean}
 */
export function revealHunk(view, changeId, hunkId = "") {
  const c = getChange(view.state, changeId);
  if (!c || !c.hunks.length) return false;
  const hk = (hunkId && c.hunks.find((x) => x.id === hunkId)) || [...c.hunks].sort((a, b) => a.from - b.from)[0];
  view.dispatch({
    selection: { anchor: hk.from },
    effects: [setCurrent.of({ changeId: c.id, hunkId: hk.id }), EditorView.scrollIntoView(hk.from, { y: "center" })],
  });
  return true;
}

/**
 * Draws stored suggestions from docs/plugins.md as tracked changes. resolve maps an
 * anchor to {start, end} in the current text, or null when it no longer
 * matches. Open edits only are drawn, and each edit is one hunk.
 * @param {EditorView} view
 * @param {object[]} suggestions
 * @param {{resolve: (anchor: object, text: string) => ({start: number, end: number}|null), authorName?: (s: object) => string}} opts
 */
export function setSuggestions(view, suggestions, { resolve, authorName } = {}) {
  const text = view.state.doc.toString();
  const out = [];
  for (const s of suggestions || []) {
    if (s.status && s.status !== "open" && s.status !== "partial") continue;
    const hunks = [];
    for (const edit of s.edits || []) {
      if (edit.status && edit.status !== "open") continue;
      const at = resolve ? resolve(edit.anchor, text) : null;
      if (!at || at.start < 0 || at.end > text.length || at.end < at.start) continue;
      hunks.push({ id: "e-" + edit.id, editId: edit.id, from: at.start, to: at.end, insert: String(edit.text || "") });
    }
    if (!hunks.length) continue;
    hunks.sort((a, b) => a.from - b.from);
    const author = authorName ? authorName(s) : s.authorKind === "ai" ? "AI" : s.authorName || s.authorId || "Suggestion";
    out.push({
      id: "s-" + s.id, kind: "suggestion", suggestion: s, title: s.title || "Suggestion", author, status: "open", error: "",
      from: hunks[0].from, to: hunks[hunks.length - 1].to, text: "", hunks, seq: 0,
    });
  }
  view.dispatch({ effects: replaceSuggestions.of(out) });
  return out.length;
}

/* ---------- Widgets ---------- */

const IS_MAC = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent || "");
const HINT_ACCEPT = IS_MAC ? "⌘⇧Enter" : "Ctrl+Shift+Enter";
const HINT_DISCARD = IS_MAC ? "⌘⇧⌫" : "Ctrl+Shift+Backspace";

/** Keeps the editor selection when a control inside a widget is pressed. */
function keepSelection(el, run) {
  el.addEventListener("mousedown", (ev) => ev.preventDefault());
  el.addEventListener("click", (ev) => {
    ev.preventDefault();
    ev.stopPropagation();
    run(el);
  });
  return el;
}

/** Shortens changed text for an accessible name. */
function excerpt(text) {
  const t = String(text || "").replace(/\s+/g, " ").trim();
  return t.length > 40 ? t.slice(0, 39) + "…" : t;
}

class InsertWidget extends WidgetType {
  constructor(text, current) { super(); this.text = text; this.current = current; }
  eq(o) { return o.text === this.text && o.current === this.current; }
  toDOM() {
    return h("ins", { class: "hunk hunk-ins" + (this.current ? " is-current" : ""), text: this.text });
  }
  ignoreEvent() { return false; }
}

/**
 * The Accept and Discard IconButtons of the hunks that start on one line.
 * Each hunk has its own pair, and pairs stack when two hunks share the line.
 */
class MarginWidget extends WidgetType {
  constructor(changeId, hunks) {
    super();
    this.changeId = changeId;
    this.hunks = hunks;
    this.key = changeId + ":" + hunks.map((hk) => hk.id).join();
  }
  eq(o) { return o.key === this.key; }
  toDOM(view) {
    const n = this.hunks.length;
    const box = h("span", { class: "hunk-actions" + (n > 1 ? " is-stacked" : "") });
    this.hunks.forEach((hk, i) => {
      const where = n > 1 ? " " + (i + 1) + " of " + n + " on this line" : "";
      const what = [hk.removed ? "removes “" + excerpt(hk.removed) + "”" : "", hk.insert ? "adds “" + excerpt(hk.insert) + "”" : ""].filter(Boolean).join(", ");
      const accept = keepSelection(iconButton({ icon: "check", label: "Accept change" + where, description: what ? "Accept: " + what : "Accept change", keybindingHint: HINT_ACCEPT, size: "small", className: "hunk-action is-accept", attrs: { "data-action": "accept" } }), () => acceptHunks(view, this.changeId, [hk.id]));
      const discard = keepSelection(iconButton({ icon: "x", label: "Discard change" + where, description: what ? "Discard: " + what : "Discard change", keybindingHint: HINT_DISCARD, size: "small", className: "hunk-action is-discard", attrs: { "data-action": "discard" } }), () => discardHunks(view, this.changeId, [hk.id]));
      box.appendChild(h("span", { class: "hunk-pair", role: "group", "aria-label": "Change" + where, dataset: { changeId: this.changeId, hunkId: hk.id } }, accept, discard));
    });
    return box;
  }
  ignoreEvent() { return true; }
}

class HeaderWidget extends WidgetType {
  constructor(change, phone, canSave) {
    super();
    this.change = change; this.phone = phone; this.canSave = canSave;
    this.key = [change.id, change.status, change.error, change.title, change.author, change.hunks.length, phone, canSave].join("|");
  }
  eq(o) { return o.key === this.key; }
  get estimatedHeight() { return 44; }
  toDOM(view) {
    const c = this.change;
    const label = h("span", { class: "change-title" }, h("span", { class: "change-author", text: c.author || "AI" }), h("span", { text: " · " + (c.title || "Change") }));
    const state = h("span", { class: "change-state", "aria-live": "polite" });
    if (c.status === "streaming") state.textContent = "Writing";
    else if (c.status === "open") state.textContent = c.hunks.length === 1 ? "1 change" : c.hunks.length + " changes";
    const actions = h("span", { class: "change-actions" });
    if (c.status === "open" && !this.phone) {
      actions.append(
        keepSelection(button({ label: "Accept all", variant: "primary", size: "small", className: "is-accept" }), () => acceptHunks(view, c.id)),
        keepSelection(button({ label: "Discard all", size: "small", className: "is-discard" }), () => discardHunks(view, c.id)));
    }
    if (c.status === "error") actions.append(keepSelection(button({ label: "Dismiss", size: "small", className: "is-discard" }), () => discardHunks(view, c.id)));
    if (c.kind === "ai" && c.status === "open" && this.canSave) {
      actions.append(keepSelection(button({ label: "Save as suggestion", variant: "invisible", size: "small", className: "is-save" }), async (btn) => {
        if (typeof trackedConfig.onSaveSuggestion !== "function" || btn.getAttribute("data-busy") === "true") return;
        setBusy(btn, true, { message: "Saving as a suggestion" });
        let saved = false;
        try {
          saved = await trackedConfig.onSaveSuggestion(c);
        } finally {
          setBusy(btn, false);
        }
        if (saved) closeChange(view, c.id);
      }));
    }
    const header = h("div", { class: "change-header", dataset: { status: c.status, kind: c.kind } }, label, state, actions);
    if (c.status === "error") header.appendChild(inlineMessage({ variant: "critical", size: "small", text: c.error || "The request failed" }));
    return h("div", { class: "cm-ms-block change-block" }, header);
  }
  ignoreEvent() { return true; }
}

function buildDecorations(state, config) {
  const v = state.field(trackedField, false);
  if (!v || !v.changes.length) return Decoration.none;
  const phone = typeof config.isPhone === "function" && config.isPhone();
  const canSave = typeof config.canSaveSuggestion === "function" ? !!config.canSaveSuggestion() : typeof config.onSaveSuggestion === "function";
  const doc = state.doc;
  const head = state.selection.main.head;
  const cur = hunkAt(state, head);
  const out = [];
  for (const c of v.changes) {
    const anchorPos = c.hunks.length ? c.hunks[0].from : c.from;
    const anchorLine = doc.lineAt(Math.min(anchorPos, doc.length));
    out.push(Decoration.widget({ widget: new HeaderWidget(c, phone, canSave), block: true, side: -1 }).range(anchorLine.from));
    const byLine = new Map();
    for (const hk of c.hunks) {
      const isCur = !!cur && cur.change.id === c.id && cur.hunk.id === hk.id;
      if (hk.to > hk.from) out.push(Decoration.mark({ class: "hunk hunk-del" + (isCur ? " is-current" : "") }).range(hk.from, hk.to));
      if (hk.insert) out.push(Decoration.widget({ widget: new InsertWidget(hk.insert, isCur), side: 1 }).range(hk.to));
      const line = doc.lineAt(hk.from);
      if (!byLine.has(line.from)) byLine.set(line.from, []);
      byLine.get(line.from).push({ id: hk.id, insert: hk.insert, removed: doc.sliceString(hk.from, hk.to) });
    }
    if (!phone && c.status === "open") {
      for (const [from, hunks] of byLine) {
        out.push(Decoration.line({ class: "hunk-line" }).range(from));
        out.push(Decoration.widget({ widget: new MarginWidget(c.id, hunks), side: -1 }).range(from));
      }
    }
  }
  return Decoration.set(out, true);
}

/**
 * The tracked changes extension.
 * @param {{isPhone?: () => boolean, onSaveSuggestion?: (change: object) => Promise<boolean>, canSaveSuggestion?: () => boolean,
 *   onSuggestion?: (e: {suggestion: object, editIds: string[], action: "accept"|"discard"}) => void,
 *   onStop?: (change: object) => void, onDiscard?: (change: object) => void}} [config]
 */
export function trackedChanges(config = {}) {
  trackedConfig = config;
  const visible = (view) => hasChanges(view.state);
  const onlyWhenOpen = (view) => {
    const c = changeAt(view.state, view.state.selection.main.head);
    return !!c && c.status === "open";
  };
  return [
    trackedField,
    EditorView.decorations.compute([trackedField, "selection"], (state) => buildDecorations(state, config)),
    Prec.highest(keymap.of([
      { key: "Tab", run: (view) => onlyWhenOpen(view) && acceptAll(view) },
      { key: "Escape", run: (view) => visible(view) && discardAll(view) },
      { key: "Alt-]", run: (view) => visible(view) && moveToHunk(view, 1) },
      { key: "Alt-[", run: (view) => visible(view) && moveToHunk(view, -1) },
      { key: "Mod-Shift-Enter", run: (view) => visible(view) && resolveHunkAt(view, true) },
      { key: "Mod-Shift-Backspace", run: (view) => visible(view) && resolveHunkAt(view, false) },
    ])),
  ];
}

/**
 * The review actions of the phone keyboard bar: Previous, Next, Accept,
 * Discard, Accept all and Discard all.
 * @param {EditorView} view
 * @returns {{id: string, label: string, run: () => boolean, disabled: boolean}[]}
 */
export function reviewActions(view) {
  const st = reviewState(view.state);
  const streaming = listChanges(view.state).some((c) => c.status === "streaming");
  const target = st.change || changeAt(view.state, view.state.selection.main.head);
  return [
    { id: "prev", label: "Previous change", run: () => moveToHunk(view, -1), disabled: st.total === 0 },
    { id: "next", label: "Next change", run: () => moveToHunk(view, 1), disabled: st.total === 0 },
    { id: "accept", label: "Accept change", run: () => resolveHunkAt(view, true), disabled: !st.hunk || streaming },
    { id: "discard", label: "Discard change", run: () => resolveHunkAt(view, false), disabled: !st.hunk },
    { id: "accept-all", label: "Accept all", run: () => acceptAll(view, target ? target.id : null), disabled: !target || target.status !== "open" },
    { id: "discard-all", label: "Discard all", run: () => discardAll(view, target ? target.id : null), disabled: !target },
  ];
}
