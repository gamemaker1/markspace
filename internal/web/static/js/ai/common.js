/**
 * Shared pieces of the AI editor modules: the @ mention parser and match
 * list, context chips with a token estimate, the inline model list, the
 * streaming client, image file ids, capability checks and the answer card
 * with citation chips. The pure functions at the top have node tests.
 */

import { h, icon, ICON_NAMES, announce, anchoredOverlay, inlineMessage, copyFeedback, selectPanel, token } from "/static/js/ui.js";

// Names of the older AI icons mapped to Octicons.
const AI_ICON_ALIASES = {
  send: "arrow-right",
  stop: "x",
  model: "ai-model",
  prev: "chevron-up",
  next: "chevron-down",
  checkAll: "check",
  closeAll: "x",
  wand: "sparkle",
  eye: "eye",
  tag: "tag",
  list: "list-unordered",
  grid: "apps",
};

/**
 * Returns an Octicon by its ui.js name or by an older AI icon name. Unknown
 * names fall back to sparkle.
 * @param {string} name
 * @param {number} [size]
 * @returns {SVGElement}
 */
export function aiIcon(name, size = 16) {
  const alias = AI_ICON_ALIASES[name];
  if (alias && ICON_NAMES.includes(alias)) return icon(alias, size);
  if (ICON_NAMES.includes(name)) return icon(name, size);
  return icon("sparkle", size);
}

/** The message shown when an AI trigger is used while offline. */
export const AI_OFFLINE_MESSAGE = "AI needs a connection";

/** The default context budget in characters, from docs/ai.md. */
export const DEFAULT_CONTEXT_CHARS = 48000;

/* ---------- Tokens ---------- */

/** Estimates tokens at 4 characters per token. */
export function estimateTokens(chars) {
  return Math.ceil(Math.max(0, Number(chars) || 0) / 4);
}

/** Formats a token count as 950, 1.2k or 61k. */
export function formatTokens(n) {
  const v = Math.max(0, Math.round(Number(n) || 0));
  if (v < 1000) return String(v);
  if (v < 10000) return (Math.round(v / 100) / 10).toFixed(1).replace(/\.0$/, "") + "k";
  if (v < 1e6) return Math.round(v / 1000) + "k";
  return (Math.round(v / 1e5) / 10).toFixed(1).replace(/\.0$/, "") + "M";
}

/**
 * Estimates the prompt size of chips plus an instruction.
 * @param {{chars?: number}[]} chips
 * @param {string} instruction
 * @param {number} [budget] context budget in characters
 * @returns {{chars: number, tokens: number, warn: boolean}}
 */
export function promptEstimate(chips, instruction = "", budget = DEFAULT_CONTEXT_CHARS) {
  const chars = (chips || []).reduce((sum, c) => sum + Math.max(0, Number(c.chars) || 0), 0) + String(instruction || "").length;
  return { chars, tokens: estimateTokens(chars), warn: chars > budget * 0.75 };
}

/* ---------- Mentions ---------- */

const MAX_MENTION = 80;

/**
 * Finds an @ mention that ends at the caret. The @ starts the input or
 * follows whitespace or an opening bracket. The query may hold spaces, and a
 * # splits a note title from a heading.
 * @param {string} text
 * @param {number} caret
 * @returns {null|{start: number, end: number, query: string, note: string, heading: string|null}}
 */
export function parseMention(text, caret) {
  const s = String(text || "");
  const end = Math.max(0, Math.min(caret, s.length));
  const from = Math.max(0, end - MAX_MENTION - 1);
  for (let i = end - 1; i >= from; i--) {
    const c = s[i];
    if (c === "\n") return null;
    if (c !== "@") continue;
    const before = i === 0 ? " " : s[i - 1];
    if (!/[\s([{]/.test(before)) return null;
    const query = s.slice(i + 1, end);
    if (/^\s/.test(query) || query.includes("@")) return null;
    const hash = query.indexOf("#");
    return {
      start: i, end, query,
      note: (hash >= 0 ? query.slice(0, hash) : query).trim(),
      heading: hash >= 0 ? query.slice(hash + 1).trim() : null,
    };
  }
  return null;
}

/** The mentions that are not a note or a file. */
export const MENTION_SPECIALS = [
  { id: "linked", label: "Linked notes", hint: "@linked" },
  { id: "backlinks", label: "Backlinks", hint: "@backlinks" },
  { id: "space", label: "Search this space", hint: "@space" },
];

/**
 * Lists the headings of a markdown body, outside fenced code.
 * @param {string} md
 * @returns {string[]}
 */
export function headingsOf(md) {
  const out = [];
  let fence = "";
  for (const line of String(md || "").split("\n")) {
    const f = /^\s{0,3}(`{3,}|~{3,})/.exec(line);
    if (f) {
      if (!fence) fence = f[1][0];
      else if (f[1][0] === fence) fence = "";
      continue;
    }
    if (fence) continue;
    const m = /^\s{0,3}#{1,6}\s+(.*?)\s*#*\s*$/.exec(line);
    if (m && m[1]) out.push(m[1].replace(/[*_`]/g, "").trim());
  }
  return out;
}

function rank(label, q) {
  const l = String(label || "").toLowerCase();
  if (!q) return 1;
  if (l === q) return 4;
  if (l.startsWith(q)) return 3;
  if (l.split(/[^\p{L}\p{N}]+/u).some((w) => w.startsWith(q))) return 2;
  return l.includes(q) ? 1 : 0;
}

/** Reports whether a file name or MIME type is an image. */
export function isImageName(name, mime = "") {
  return /^image\//.test(mime) || /\.(png|jpe?g|gif|webp|svg|avif)$/i.test(String(name || ""));
}

/**
 * Lists the matches of a parsed mention: sections of a note after #, else
 * special mentions, notes and files ranked by label. Each match carries the
 * chip it adds.
 * @param {ReturnType<typeof parseMention>} mention
 * @param {{notes?: object[], files?: object[], limit?: number, excludeNoteId?: string}} sources
 * @returns {{type: string, label: string, sub: string, chip: object}[]}
 */
export function mentionMatches(mention, { notes = [], files = [], limit = 6 } = {}) {
  if (!mention) return [];
  if (mention.heading !== null) {
    const q = mention.note.toLowerCase();
    const note = notes.map((n) => [n, rank(n.title, q)]).filter(([, r]) => r > 0).sort((a, b) => b[1] - a[1])[0];
    if (!note) return [];
    const n = note[0];
    const hq = mention.heading.toLowerCase();
    return headingsOf(n.content)
      .map((heading, i) => ({ heading, i, r: rank(heading, hq) }))
      .filter((x) => x.r > 0)
      .sort((a, b) => b.r - a.r || a.i - b.i)
      .slice(0, limit)
      .map(({ heading }) => ({ type: "section", label: heading, sub: n.title || "Untitled", chip: sectionChip(n, heading) }));
  }
  const q = mention.note.toLowerCase();
  const scored = [];
  MENTION_SPECIALS.forEach((sp, i) => {
    const r = Math.max(rank(sp.id, q), rank(sp.label, q));
    if (r > 0) scored.push({ r: r + 0.5, i, item: { type: "special", label: sp.label, sub: sp.hint, chip: { key: "special:" + sp.id, kind: sp.id, label: sp.label } } });
  });
  notes.forEach((n, i) => {
    const r = rank(n.title || "Untitled", q);
    if (r > 0) scored.push({ r, i: 100 + i, item: { type: "note", label: n.title || "Untitled", sub: String(n.path || "").replace(/\.md$/, ""), chip: noteChip(n) } });
  });
  files.forEach((f, i) => {
    const r = rank(f.name, q);
    if (r > 0) scored.push({ r, i: 1000 + i, item: { type: "file", label: f.name, sub: isImageName(f.name, f.mime) ? "Image" : "File", chip: fileChip(f) } });
  });
  scored.sort((a, b) => b.r - a.r || a.i - b.i);
  return scored.slice(0, limit).map((x) => x.item);
}

/* ---------- Chips ---------- */

/** A chip for a whole note. */
export function noteChip(n, label = "Note") {
  return { key: "note:" + n.id, kind: "note", badge: label, label: n.title || "Untitled", chars: String(n.content || "").length, ref: { kind: "note", noteId: n.id } };
}

/** A chip for the section of a note under a heading. */
export function sectionChip(n, heading, text = null) {
  const body = text !== null ? text : sectionText(n.content, heading);
  return { key: "section:" + n.id + "#" + heading, kind: "section", badge: "Section", label: heading, title: (n.title || "") + " › " + heading, chars: body.length, ref: { kind: "section", noteId: n.id, heading } };
}

/** A chip for a file, shown as Image for images. */
export function fileChip(f) {
  const image = isImageName(f.name, f.mime);
  return { key: "file:" + f.id, kind: "file", badge: image ? "Image" : "File", label: f.name || "file", image, chars: image ? 3200 : Math.min(Number(f.size) || 0, 8000), ref: { kind: "file", fileId: f.id }, src: f.src || "" };
}

/** A chip for the selected text. */
export function selectionChip(text) {
  return { key: "selection", kind: "selection", badge: "Selection", label: excerptLabel(text), chars: text.length, ref: { kind: "selection", text } };
}

/** A chip for a web page. */
export function urlChip(url) {
  return { key: "url:" + url, kind: "url", badge: "Page", label: shortUrl(url), title: url, chars: 8000, ref: { kind: "url", url } };
}

/** A chip for a special mention, bound to the note. */
export function specialChip(id, noteId) {
  const sp = MENTION_SPECIALS.find((x) => x.id === id);
  const label = sp ? sp.label : id;
  if (id === "space") return { key: "special:space", kind: "search", badge: "Search", label, chars: 6000, ref: { kind: "search", query: "" } };
  return { key: "special:" + id, kind: id, badge: id === "linked" ? "Linked" : "Backlinks", label, chars: 8000, ref: { kind: id, noteId } };
}

/** Returns the text under a heading, down to the next heading of the same or a higher level. */
export function sectionText(md, heading) {
  const lines = String(md || "").split("\n");
  const want = String(heading || "").trim().toLowerCase();
  let start = -1;
  let level = 0;
  for (let i = 0; i < lines.length; i++) {
    const m = /^\s{0,3}(#{1,6})\s+(.*?)\s*#*\s*$/.exec(lines[i]);
    if (!m) continue;
    if (start >= 0 && m[1].length <= level) return lines.slice(start, i).join("\n");
    if (start < 0 && m[2].replace(/[*_`]/g, "").trim().toLowerCase() === want) {
      start = i;
      level = m[1].length;
    }
  }
  return start >= 0 ? lines.slice(start).join("\n") : "";
}

function excerptLabel(text) {
  const t = String(text || "").replace(/\s+/g, " ").trim();
  return t.length > 32 ? t.slice(0, 31) + "…" : t;
}

/** Shortens a URL to host and path. */
export function shortUrl(url) {
  try {
    const u = new URL(url);
    const label = u.hostname.replace(/^www\./, "") + (u.pathname === "/" ? "" : u.pathname);
    return label.length > 36 ? label.slice(0, 35) + "…" : label;
  } catch {
    return String(url).slice(0, 36);
  }
}

/** Lists the https URLs in pasted text, once each. */
export function urlsInText(text) {
  const out = [];
  for (const m of String(text || "").matchAll(/https:\/\/[^\s<>()"'\]]+[^\s<>()"'\].,;:!?]/g)) {
    if (!out.includes(m[0])) out.push(m[0]);
  }
  return out;
}

/**
 * Turns chips into context references. A search chip takes its query from
 * the instruction.
 */
export function refsFromChips(chips, instruction = "") {
  return (chips || []).map((c) => (c.ref.kind === "search" ? { ...c.ref, query: String(instruction || "").slice(0, 300) } : c.ref));
}

/* ---------- Models ---------- */

function priceOf(m) {
  const p = m.effectivePrice || m.priceOverride || m.price || null;
  if (m.local && !p) return 0;
  if (!p) return Infinity;
  const images = Array.isArray(m.outputs) && m.outputs.includes("image") && !m.outputs.includes("text");
  if (images) return Number(p.perImageOut) || 0;
  const v = (Number(p.inputPerMTok) || 0) + (Number(p.outputPerMTok) || 0);
  return v < 0 ? Infinity : v;
}

/** Reports whether a catalog row can serve a capability. */
export function modelCapable(m, capability) {
  const ins = Array.isArray(m.inputs) ? m.inputs : [];
  const outs = Array.isArray(m.outputs) ? m.outputs : [];
  switch (capability) {
    case "vision": return outs.includes("text") && ins.includes("image");
    case "image.generate": return outs.includes("image");
    case "image.edit": return outs.includes("image");
    case "text": case "": case undefined: return outs.includes("text") || outs.length === 0;
    default: return true;
  }
}

/** Formats the price of a catalog row. */
export function priceLabel(m) {
  const p = priceOf(m);
  if (p === 0) return m.local ? "Local" : "Free";
  if (p === Infinity) return "";
  const price = m.effectivePrice || m.priceOverride || m.price;
  if (Array.isArray(m.outputs) && m.outputs.includes("image") && !m.outputs.includes("text")) return "$" + p.toFixed(2) + " per image";
  const fmt = (v) => "$" + (v >= 10 ? v.toFixed(0) : v >= 1 ? v.toFixed(2).replace(/0$/, "") : v.toFixed(2));
  return fmt(Number(price.inputPerMTok) || 0) + " / " + fmt(Number(price.outputPerMTok) || 0) + " per M";
}

/**
 * Filters catalog rows to enabled, available models with the capability,
 * matching query, ordered by price with unpriced models last.
 * @param {object[]} models rows of GET /api/ai/models
 * @param {{capability?: string, query?: string, limit?: number}} [opts]
 * @returns {{providerId: string, model: string, label: string, provider: string, price: string, cost: number}[]}
 */
export function modelChoices(models, { capability = "text", query = "", limit = 6 } = {}) {
  const q = String(query || "").trim().toLowerCase();
  const seen = new Set();
  const rows = [];
  for (const m of models || []) {
    if (!m || m.enabled === false || m.available === false || !modelCapable(m, capability)) continue;
    const key = m.providerId + " " + m.model;
    if (seen.has(key)) continue;
    seen.add(key);
    const label = m.name || m.model;
    if (q && !(label.toLowerCase().includes(q) || String(m.model).toLowerCase().includes(q) || String(m.providerName || m.providerId).toLowerCase().includes(q))) continue;
    rows.push({ providerId: m.providerId, model: m.model, label, provider: m.providerName || m.providerId, price: priceLabel(m), cost: priceOf(m) });
  }
  rows.sort((a, b) => a.cost - b.cost || a.label.localeCompare(b.label) || a.provider.localeCompare(b.provider));
  return limit > 0 ? rows.slice(0, limit) : rows;
}

/* ---------- Images ---------- */

/**
 * Finds the file id of an image reference. /files/<space>/<id>/<name> and
 * /api/files/<id>/... URLs carry the id. A relative path goes to resolve,
 * which answers from links.js and the local store once files sync. Without an
 * answer the id is empty.
 * @param {string} url
 * @param {{resolve?: (url: string) => string}} [opts]
 * @returns {string}
 */
export function fileIdFromUrl(url, { resolve } = {}) {
  const u = String(url || "").trim();
  let m = /^(?:https?:\/\/[^/]+)?\/files\/[^/]+\/([^/?#]+)\//.exec(u);
  if (m) return decodeURIComponent(m[1]);
  m = /^(?:https?:\/\/[^/]+)?\/api\/files\/([^/?#]+)/.exec(u);
  if (m) return decodeURIComponent(m[1]);
  if (!u || /^[a-z][a-z0-9+.-]*:/i.test(u) || u.startsWith("//")) return "";
  if (typeof resolve === "function") {
    try { return resolve(u) || ""; } catch { return ""; }
  }
  return "";
}

/* ---------- Network ---------- */

/**
 * Splits a text/event-stream buffer into complete events.
 * @returns {{events: {event: string, data: string}[], rest: string}}
 */
export function splitSSE(buffer, flush = false) {
  const events = [];
  const parts = buffer.replace(/\r\n/g, "\n").split("\n\n");
  const rest = flush ? "" : parts.pop();
  for (const block of parts) {
    let event = "message";
    const data = [];
    for (const line of block.split("\n")) {
      if (line.startsWith("event:")) event = line.slice(6).trim();
      else if (line.startsWith("data:")) data.push(line.slice(5).replace(/^ /, ""));
    }
    if (data.length) events.push({ event, data: data.join("\n") });
  }
  return { events, rest };
}

/**
 * Posts JSON and reads a text/event-stream. onEvent receives each event with
 * parsed data. The promise resolves with the done data, and rejects on an
 * error event or an HTTP error, whose status is on the error.
 * @param {string} url
 * @param {object} body
 * @param {{fetch?: Function, signal?: AbortSignal, onEvent?: (name: string, data: object) => void}} [opts]
 */
export async function streamEvents(url, body, { fetch: fetchImpl = globalThis.fetch, signal, onEvent } = {}) {
  const res = await fetchImpl(url, {
    method: "POST", credentials: "same-origin", signal,
    headers: { "Content-Type": "application/json", Accept: "text/event-stream" },
    body: JSON.stringify(body),
  });
  if (!res.ok || !res.body) {
    let message = res.statusText || "Request failed";
    try {
      const data = await res.json();
      if (data && data.error) message = data.error;
    } catch { /* keep the status text */ }
    const err = new Error(message);
    err.status = res.status;
    throw err;
  }
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  for (;;) {
    const { value, done } = await reader.read();
    buffer += done ? decoder.decode() : decoder.decode(value, { stream: true });
    const parsed = splitSSE(buffer, done);
    buffer = parsed.rest;
    for (const ev of parsed.events) {
      let data;
      try { data = JSON.parse(ev.data); } catch { data = { delta: ev.data }; }
      if (ev.event === "error") {
        reader.cancel().catch(() => {});
        const err = new Error(data.error || "The model returned an error");
        err.status = 502;
        throw err;
      }
      if (onEvent) onEvent(ev.event, data);
      if (ev.event === "done") {
        reader.cancel().catch(() => {});
        return data;
      }
    }
    if (done) return { done: false, incomplete: true };
  }
}

const capabilityCache = new Map();

/**
 * Reports which AI capabilities the actor can use: text, vision, image
 * generation and image editing. Answers are kept for 60 s.
 * @param {(method: string, path: string) => Promise<object>} api
 * @returns {Promise<{text: boolean, vision: boolean, generate: boolean, edit: boolean, sizes: string[]}>}
 */
export async function aiCapabilities(api) {
  const key = "caps";
  const hit = capabilityCache.get(key);
  if (hit && Date.now() - hit.at < 60000) return hit.value;
  const probe = async (capability) => {
    try {
      const data = await api("GET", "/api/ai/models?capability=" + encodeURIComponent(capability));
      return (data && data.items) || [];
    } catch {
      return [];
    }
  };
  const [text, vision, generate, edit] = await Promise.all([probe("text"), probe("vision"), probe("image.generate"), probe("image.edit")]);
  const value = { text: text.length > 0, vision: vision.length > 0, generate: generate.length > 0, edit: edit.length > 0, sizes: [] };
  if (value.generate || value.edit) {
    try {
      const providers = await api("GET", "/api/ai/providers");
      for (const p of (providers && providers.items) || []) for (const s of p.imageSizes || []) if (!value.sizes.includes(s)) value.sizes.push(s);
    } catch { /* sizes stay empty */ }
  }
  capabilityCache.set(key, { at: Date.now(), value });
  return value;
}

/** Forgets cached capabilities, for tests and after settings change. */
export function resetCapabilities() {
  capabilityCache.clear();
}

/* ---------- UI pieces ---------- */

/**
 * Tells the user that AI needs a connection. With an anchor that is on the
 * page, an AnchoredOverlay under the anchor holds a warning InlineMessage, as
 * docs/primer.md 6.9 prescribes for inactive AI triggers. The message is always
 * announced.
 * @param {Element|null} [anchor]
 * @returns {{close: Function}|null} the overlay, or null without an anchor
 */
export function showAiOffline(anchor = null) {
  announce(AI_OFFLINE_MESSAGE, { politeness: "assertive" });
  if (!anchor || !anchor.isConnected || typeof document === "undefined") return null;
  return anchoredOverlay(anchor, inlineMessage({ variant: "warning", text: AI_OFFLINE_MESSAGE }), { label: AI_OFFLINE_MESSAGE, width: "auto", focus: false, className: "ai-offline-overlay" });
}

/**
 * Reports AI as unavailable when the network is off. Returns true when the
 * caller should stop.
 * @param {() => boolean} isOnline
 * @param {Element|null} [anchor] the trigger the message is shown under
 * @returns {boolean}
 */
export function blockedOffline(isOnline, anchor = null) {
  if (typeof isOnline === "function" && !isOnline()) {
    showAiOffline(anchor);
    return true;
  }
  return false;
}

const CHIP_ICONS = { note: "file", section: "hash", selection: "quote", url: "link", linked: "link", backlinks: "link", search: "search", file: "file" };

/**
 * Builds one context chip as a large Primer Token. The leading visual is the
 * image thumbnail or a kind icon, and the accessible name holds the kind.
 * @param {object} chip
 * @param {{onRemove?: (chip: object) => void, removable?: boolean}} [opts]
 * @returns {HTMLElement}
 */
export function chipElement(chip, { onRemove, removable = true } = {}) {
  const kindIcon = chip.image ? "image" : CHIP_ICONS[chip.kind] || "file";
  const lead = chip.src ? h("img", { class: "ask-chip-thumb", src: chip.src, alt: "" }) : ICON_NAMES.includes(kindIcon) ? kindIcon : "file";
  const tok = token(chip.label, { size: "large", leadingVisual: lead, onRemove: removable && onRemove ? () => onRemove(chip) : undefined });
  tok.classList.add("ask-chip");
  tok.dataset.kind = chip.kind;
  const kind = chip.badge || chip.kind;
  if (tok.classList.contains("is-removable")) {
    tok.setAttribute("aria-label", kind + ": " + chip.label);
    const x = tok.querySelector(".token-remove");
    if (x) {
      x.setAttribute("aria-label", "Remove " + kind + " " + chip.label);
      x.addEventListener("mousedown", (ev) => ev.preventDefault());
    }
  } else {
    tok.prepend(h("span", { class: "sr-only", text: kind + ": " }));
  }
  return tok;
}

/**
 * Renders a markdown answer with citation chips. A [n] citation becomes a
 * button that opens sources[n-1] through onCite.
 * @param {HTMLElement} target
 * @param {string} md
 * @param {{render?: Function, sources?: object[], onCite?: (source: object) => void}} [opts]
 */
export function renderAnswer(target, md, { render, sources = [], onCite } = {}) {
  target.textContent = "";
  let html = "";
  if (typeof render === "function") {
    try { html = (render(md) || {}).html || ""; } catch { html = ""; }
  }
  if (html) target.innerHTML = html; // markdown.render builds its HTML from escaped text and a fixed tag set.
  else target.textContent = md;
  const usable = (sources || []).filter(Boolean);
  if (!usable.length) return;
  const walker = document.createTreeWalker ? document.createTreeWalker(target, 4) : null;
  if (!walker) return;
  const nodes = [];
  while (walker.nextNode()) if (/\[\d+\]/.test(walker.currentNode.nodeValue)) nodes.push(walker.currentNode);
  for (const node of nodes) {
    const parts = node.nodeValue.split(/(\[\d+\])/);
    const frag = document.createDocumentFragment();
    for (const part of parts) {
      const m = /^\[(\d+)\]$/.exec(part);
      const src = m ? sources[Number(m[1]) - 1] : null;
      if (!src || src.skipped) {
        frag.appendChild(document.createTextNode(part));
        continue;
      }
      // Without a handler, citations are plain text and the source chips below take the taps.
      if (!onCite) {
        frag.appendChild(h("sup", { class: "citation", text: m[1] }));
        continue;
      }
      const btn = h("button", { type: "button", class: "citation", text: m[1], "aria-label": "Source " + m[1] + ": " + (src.title || src.url || "") });
      btn.addEventListener("click", () => onCite(src));
      frag.appendChild(btn);
    }
    node.parentNode.replaceChild(frag, node);
  }
}

/**
 * Builds the source chips under an answer: medium Tokens that act as links to
 * the cited note, file or page.
 * @param {object[]} sources
 * @param {(source: object) => void} onCite
 * @returns {HTMLElement|null}
 */
export function sourceChips(sources, onCite) {
  const list = (sources || []).map((s, i) => [s, i]).filter(([s]) => s && !s.skipped);
  if (!list.length) return null;
  return h("ul", { class: "citation-list", "aria-label": "Sources" },
    ...list.map(([s, i]) => {
      const label = s.title || shortUrl(s.url || "") || "Source";
      const btn = h("button", { type: "button", class: "token token-medium citation-chip" },
        h("span", { class: "token-visual citation", "aria-hidden": "true", text: String(i + 1) }),
        h("span", { class: "token-text", text: label }));
      btn.setAttribute("aria-label", "Source " + (i + 1) + ": " + label);
      btn.addEventListener("click", () => onCite && onCite(s));
      return h("li", { class: "citation-item" }, btn);
    }));
}

/**
 * Copies text. With a button, the button swaps its icon for a check for 2 s,
 * and the result is announced in both cases.
 * @param {string} text
 * @param {HTMLElement|null} [button]
 */
export async function copyToClipboard(text, button = null) {
  try {
    await navigator.clipboard.writeText(text);
    if (button && typeof button.querySelector === "function") copyFeedback(button);
    else announce("Copied");
  } catch {
    announce("Copy failed", { politeness: "assertive" });
  }
}

/** The id of a model choice in the model SelectPanel. */
export function modelChoiceId(choice) {
  return choice ? choice.providerId + " " + choice.model : "auto";
}

/**
 * Opens the model picker, a single select SelectPanel anchored to the model
 * chip. It lists Automatic and the capable models ordered by price. Picking
 * applies at once and closes the panel.
 * @param {Element} anchor
 * @param {{api: Function, capability: string, current: {providerId?: string, model?: string}|null, onPick: (choice: object|null) => void, onClose?: Function}} opts
 * @returns {{close: Function}}
 */
export function openModelPanel(anchor, { api, capability, current, onPick, onClose }) {
  let rows = [];
  const itemsFor = (query = "") => [
    { id: "auto", label: "Automatic", description: "Task route" },
    ...modelChoices(rows, { capability, query, limit: 0 }).map((c) => ({ id: modelChoiceId(c), label: c.label, description: [c.provider, c.price].filter(Boolean).join(" · "), choice: c })),
  ];
  // A chip in the lower half of the screen opens the panel upward, so the list stays on screen.
  const rect = anchor.getBoundingClientRect();
  const placement = rect.top > window.innerHeight / 2 ? "top-start" : "bottom-start";
  const panel = selectPanel(anchor, {
    placement,
    title: "Model",
    subtitle: capability === "vision" ? "Models that read images" : capability === "image.generate" ? "Models that make images" : "",
    items: [],
    selected: modelChoiceId(current),
    placeholder: "Filter models",
    filterLabel: "Filter models",
    emptyTitle: "No capable model",
    emptyBody: "An administrator can add one in the AI settings.",
    onSelect: (id) => {
      if (id === null || id === undefined) return;
      const hit = itemsFor().find((it) => it.id === id);
      onPick(hit && hit.choice ? hit.choice : null);
    },
    onFilter: (query) => itemsFor(query),
    onClose,
  });
  if (!panel.element) return panel;
  panel.setLoading(true, "Loading models");
  (async () => {
    try {
      const data = await api("GET", "/api/ai/models?capability=" + encodeURIComponent(capability));
      rows = (data && data.items) || [];
    } catch {
      rows = [];
    }
    panel.setLoading(false);
    panel.setItems(itemsFor());
  })();
  return panel;
}

/** The short name of a chosen model for the model chip. */
export function modelChipLabel(choice) {
  if (!choice) return "Auto";
  const name = choice.label || choice.model || "";
  return name.length > 22 ? name.slice(0, 21) + "…" : name;
}
