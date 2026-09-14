/**
 * Markspace rail panels: Comments, Suggestions, History, Share, AI and plugin
 * panels. The Files tab of a note and the folder and file panels live in
 * filepanels.js and use the pieces here. Each panel renders into a container
 * it is given and returns {unmount()}. The shell shows the panels as tabs of
 * the rail pane. The editor shows them in its drawer when it runs without the
 * shell.
 *
 * The panels follow docs/primer.md 5.10 and 6. panelShell builds the frame: an h2
 * title with at most one small Button, an offline Banner slot, a message slot
 * for Banners, the body and a foot. createPanel adds the online gate, loading
 * rows and cleanup. listSection, listRow and rowMenu build Box rows,
 * emptyState builds a small Blankslate, and inlineError builds an
 * InlineMessage with Retry.
 *
 * Options shared by every panel:
 *   {note | item, access, store, sync, user, isOnline, api, route?, kind?,
 *    navigate?, onClose?, onCount?}
 * route is the item route prefix, "/api/notes/{id}" or "/api/items/{id}".
 * Without it a panel uses /api/items/{id} when item is set and
 * /api/notes/{id} otherwise. kind is "note", "folder" or "file". onClose adds
 * a close button to the header. onCount(n) reports the number the rail shows
 * on the tab.
 *
 * The pure helpers are exported for tests, for views.js and for editor.js.
 */

import {
  icon, button, iconButton, actionMenu, confirmDialog, banner, inlineMessage, announce, undoable, undoRow, setBusy,
  counterLabel, label as uiLabel, avatar as uiAvatar, skeleton, blankslate, relativeTime as timeElement,
  formControl, setFieldError, showFieldErrors, details, autocomplete, copyFeedback, prefersReducedMotion,
  openDialog, segmentedControl, tooltip, bannerHost,
} from "/static/js/ui.js";
import { openAITab } from "/static/js/ai/aitab.js";

/** The Files tab of a note lives in filepanels.js. The shell opens it through this module. */
export { openNoteFiles } from "/static/js/filepanels.js";

/* ---------- Pure helpers ---------- */

/* ---------- Pure helpers ---------- */

const ACCESS_RANK = { none: 0, view: 1, comment: 2, edit: 3, manage: 4 };

/**
 * Returns the numeric rank of an access name, 0 for unknown names.
 * @param {string} access
 * @returns {number}
 */
export function accessRank(access) {
  return ACCESS_RANK[access] || 0;
}

/**
 * Parses server-sent event frames out of a text buffer. Frames end with a
 * blank line. The unterminated tail is returned as rest unless flush is true,
 * in which case the tail is parsed as a final frame.
 * @param {string} buffer
 * @param {boolean} [flush]
 * @returns {{events: {event: string, data: string}[], rest: string}}
 */
export function parseSSE(buffer, flush = false) {
  const norm = buffer.replace(/\r\n/g, "\n");
  const blocks = norm.split("\n\n");
  const rest = flush ? "" : blocks.pop();
  const events = [];
  for (const block of blocks) {
    let event = "message";
    const data = [];
    for (const line of block.split("\n")) {
      if (line === "" || line.startsWith(":")) continue;
      const i = line.indexOf(":");
      const field = i < 0 ? line : line.slice(0, i);
      let value = i < 0 ? "" : line.slice(i + 1);
      if (value.startsWith(" ")) value = value.slice(1);
      if (field === "event") event = value;
      else if (field === "data") data.push(value);
    }
    if (data.length) events.push({ event, data: data.join("\n") });
  }
  return { events, rest };
}

/**
 * Formats a timestamp relative to now: "just now", "5 min ago", "3 h ago",
 * "yesterday", "4 days ago", then a short date.
 * @param {string|number|Date} when
 * @param {number} [now]
 * @returns {string}
 */
export function relativeTime(when, now = Date.now()) {
  const t = new Date(when).getTime();
  if (!Number.isFinite(t)) return "";
  const sec = Math.round((now - t) / 1000);
  if (sec < 45) return "just now";
  const min = Math.round(sec / 60);
  if (min < 60) return min + " min ago";
  const hours = Math.round(min / 60);
  if (hours < 24) return hours + " h ago";
  const days = Math.round(hours / 24);
  if (days === 1) return "yesterday";
  if (days < 7) return days + " days ago";
  const d = new Date(t);
  const sameYear = d.getFullYear() === new Date(now).getFullYear();
  return d.toLocaleDateString(undefined, sameYear
    ? { month: "short", day: "numeric" }
    : { year: "numeric", month: "short", day: "numeric" });
}

/**
 * Nests a flat comment list by parentId. Lists that already carry children
 * are returned as they are. Each level is ordered by createdAt.
 * @param {object[]} comments
 * @returns {object[]}
 */
export function threadComments(comments) {
  const list = Array.isArray(comments) ? comments : [];
  if (list.some((c) => Array.isArray(c.children) && c.children.length)) return list;
  const byId = new Map(list.map((c) => [c.id, { ...c, children: [] }]));
  const roots = [];
  for (const c of byId.values()) {
    const parent = c.parentId && byId.get(c.parentId);
    if (parent && parent !== c) parent.children.push(c);
    else roots.push(c);
  }
  const order = (a, b) => String(a.createdAt).localeCompare(String(b.createdAt));
  const sortDeep = (arr) => { arr.sort(order); arr.forEach((c) => sortDeep(c.children)); return arr; };
  return sortDeep(roots);
}

/**
 * Formats a byte count as B, KB or MB.
 * @param {number} n
 * @returns {string}
 */
export function formatBytes(n) {
  if (!Number.isFinite(n)) return "";
  if (n < 1024) return n + " B";
  if (n < 1024 * 1024) return (n / 1024).toFixed(n < 10240 ? 1 : 0) + " KB";
  return (n / 1048576).toFixed(1) + " MB";
}

/* ---------- Rail tabs, counts, versions and access ---------- */

/**
 * The rail tabs in display order. label is the accessible name of the tab and
 * the title in the panel header; icon names an icon from ui.js.
 */
export const RAIL_TABS = [
  { id: "outline", label: "Outline", icon: "list" },
  { id: "comments", label: "Comments", icon: "comment" },
  { id: "suggestions", label: "Suggestions", icon: "light-bulb" },
  { id: "history", label: "History", icon: "history" },
  { id: "share", label: "Share", icon: "users" },
  { id: "files", label: "Files", icon: "files" },
  { id: "ai", label: "AI", icon: "sparkle" },
  { id: "info", label: "Info", icon: "info" },
];

/**
 * Lists the rail tab ids a note shows, in order. Share needs manage access.
 * Suggestions shows unless suggestions is false. AI needs the server to have
 * AI switched on and a connection. While offline the AI tab stays only when
 * it is the tab in view, given as keep.
 * @param {{access?: string, comments?: boolean, suggestions?: boolean, ai?: boolean, online?: boolean, keep?: string}} [state]
 * @returns {string[]}
 */
export function railTabIds({ access = "view", comments = true, suggestions = true, ai = false, online = true, keep = "" } = {}) {
  return RAIL_TABS.map((tab) => tab.id).filter((id) => {
    if (id === "comments") return !!comments;
    if (id === "suggestions") return !!suggestions;
    if (id === "share") return accessRank(access) >= ACCESS_RANK.manage;
    if (id === "ai") return !!ai && (online || keep === "ai");
    return true;
  });
}

/**
 * Returns the tab a key press moves to in a horizontal tab list: arrows wrap
 * around, Home and End go to the ends. Returns "" for any other key.
 * @param {string[]} ids tab ids in order
 * @param {string} current the focused tab id
 * @param {string} key KeyboardEvent.key
 * @returns {string}
 */
export function tabKeyTarget(ids, current, key) {
  const n = Array.isArray(ids) ? ids.length : 0;
  if (!n) return "";
  const i = ids.indexOf(current);
  switch (key) {
    case "ArrowRight":
    case "ArrowDown":
      return ids[(i + 1) % n];
    case "ArrowLeft":
    case "ArrowUp":
      return ids[i <= 0 ? n - 1 : i - 1];
    case "Home":
      return ids[0];
    case "End":
      return ids[n - 1];
    default:
      return "";
  }
}

/**
 * Formats a count for a tab badge: "" for zero or less, "99+" above 99.
 * @param {number} n
 * @returns {string}
 */
export function badgeText(n) {
  const value = Math.floor(Number(n) || 0);
  if (value <= 0) return "";
  return value > 99 ? "99+" : String(value);
}

/**
 * Sorts versions newest first and pairs each with its size change against
 * the next older version. The oldest version has delta null.
 * @param {{id: string, size?: number, createdAt: string}[]} versions
 * @returns {{version: object, delta: number|null}[]}
 */
export function versionDeltas(versions) {
  const list = (Array.isArray(versions) ? versions : []).slice().sort((a, b) =>
    String(b.createdAt || "").localeCompare(String(a.createdAt || "")) || String(b.id || "").localeCompare(String(a.id || "")));
  return list.map((version, i) => {
    const older = list[i + 1];
    const delta = older && Number.isFinite(version.size) && Number.isFinite(older.size) ? version.size - older.size : null;
    return { version, delta };
  });
}

/**
 * Formats a size change: "+120 B", "-1.5 KB", "No size change", or "" when
 * the change is unknown.
 * @param {number|null|undefined} delta bytes
 * @returns {string}
 */
export function formatDelta(delta) {
  if (delta === null || delta === undefined || !Number.isFinite(delta)) return "";
  if (delta === 0) return "No size change";
  return (delta > 0 ? "+" : "-") + formatBytes(Math.abs(delta));
}

/**
 * Counts the UTF-8 bytes of a string, the unit of NoteVersion.size.
 * @param {string} text
 * @returns {number}
 */
export function byteLength(text) {
  return new TextEncoder().encode(String(text || "")).length;
}

const ACCESS_LABELS = { view: "Can view", comment: "Can comment", edit: "Can edit", manage: "Can manage" };

/**
 * Labels an access level: "Can view", "Can comment", "Can edit", "Can manage",
 * or "No access" for anything else.
 * @param {string} access
 * @returns {string}
 */
export function accessLabel(access) {
  return ACCESS_LABELS[access] || "No access";
}

/**
 * Lists the access levels a person share or a public link may grant.
 * @param {"share"|"link"} kind
 * @returns {string[]}
 */
export function accessChoices(kind) {
  return kind === "link" ? ["view", "comment"] : ["view", "comment", "edit", "manage"];
}

/**
 * Describes a public link for its card: access label, password flag, expiry
 * state and view count.
 * @param {{access?: string, hasPassword?: boolean, expiresAt?: string, views?: number}} link
 * @param {number} [now]
 * @returns {{access: string, password: boolean, expired: boolean, expiry: string, views: string}}
 */
export function linkSummary(link, now = Date.now()) {
  const l = link || {};
  const expires = l.expiresAt ? new Date(l.expiresAt).getTime() : NaN;
  const expired = Number.isFinite(expires) && expires < now;
  let expiry = "No expiry";
  if (Number.isFinite(expires)) {
    const d = new Date(expires);
    const sameYear = d.getFullYear() === new Date(now).getFullYear();
    const date = d.toLocaleDateString(undefined, sameYear ? { month: "short", day: "numeric" } : { year: "numeric", month: "short", day: "numeric" });
    expiry = (expired ? "Expired " : "Expires ") + date;
  }
  const views = Number(l.views) || 0;
  return {
    access: accessLabel(l.access === "comment" ? "comment" : "view"),
    password: !!l.hasPassword,
    expired,
    expiry,
    views: views === 1 ? "1 view" : views + " views",
  };
}

/**
 * Lists the ATX headings of a markdown text outside fenced code, with the
 * offset of each heading line, for the outline of the editor.
 * @param {string} md
 * @returns {{level: number, text: string, pos: number}[]}
 */
export function outlineOf(md) {
  const out = [];
  let fence = "";
  let pos = 0;
  for (const line of String(md || "").split("\n")) {
    const marker = /^\s{0,3}(`{3,}|~{3,})/.exec(line);
    if (fence) {
      if (marker && marker[1][0] === fence[0] && marker[1].length >= fence.length) fence = "";
    } else if (marker) {
      fence = marker[1];
    } else {
      const m = /^\s{0,3}(#{1,6})[ \t]+(.*?)[ \t]*#*[ \t]*$/.exec(line);
      if (m && m[2].trim()) out.push({ level: m[1].length, text: quoteText(m[2]) || m[2].trim(), pos });
    }
    pos += line.length + 1;
  }
  return out;
}

/* ---------- DOM helpers ---------- */

/* ---------- DOM helpers ---------- */

/**
 * Creates an element. attrs keys: class, text, on<Event> handlers, boolean
 * properties (hidden, disabled, checked, readOnly), anything else becomes an
 * attribute. Children may be nodes, strings or null.
 * @param {string} tag
 * @param {object} [attrs]
 * @param {...(Node|string|null|undefined)} children
 * @returns {HTMLElement}
 */
export function h(tag, attrs = {}, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs || {})) {
    if (value === undefined || value === null || value === false) continue;
    if (key === "class") node.className = value;
    else if (key === "text") node.textContent = String(value);
    else if (key.startsWith("on") && typeof value === "function") node.addEventListener(key.slice(2).toLowerCase(), value);
    else if (key === "hidden" || key === "disabled" || key === "checked" || key === "readOnly" || key === "value" || key === "selected") node[key] = value;
    else node.setAttribute(key, value === true ? "" : String(value));
  }
  for (const child of children.flat()) {
    if (child === null || child === undefined || child === false) continue;
    node.appendChild(typeof child === "string" ? document.createTextNode(child) : child);
  }
  return node;
}

const SVG_NS = "http://www.w3.org/2000/svg";

/**
 * Builds a 20 px stroke icon from an SVG path string.
 * @param {string} d
 * @returns {SVGElement}
 */
export function svgIcon(d) {
  const svg = document.createElementNS(SVG_NS, "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("width", "20");
  svg.setAttribute("height", "20");
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "1.8");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.setAttribute("aria-hidden", "true");
  const path = document.createElementNS(SVG_NS, "path");
  path.setAttribute("d", d);
  svg.appendChild(path);
  return svg;
}

/** Icon path data shared by the editor and the panels. */
export const ICONS = {
  close: "M6 6l12 12M18 6L6 18",
  copy: "M9 9h10v11H9zM5 15V4h10",
};

/* ---------- Shared pieces ---------- */

/** Returns the display name of a user record. */
function nameOf(user, fallback = "Someone") {
  return (user && (user.name || user.email)) || fallback;
}

/** Builds a decorative Avatar for a user whose name shows beside it. */
function avatarFor(user, size = 24, square = false) {
  return uiAvatar({ name: nameOf(user, "?"), color: user && user.avatarColor, size, square, alt: "" });
}

/** Builds a RelativeTime element. precise shows the full date and time. */
function timeEl(iso, precise = false) {
  return timeElement(iso, { precise });
}

function errorText(err) {
  if (!err) return "Something went wrong";
  if (err.status === 0) return "The server could not be reached";
  return err.message || "Something went wrong";
}

function sentence(text) {
  const s = String(text || "").trim();
  if (!s) return s;
  const first = s.charAt(0).toUpperCase() + s.slice(1);
  return /[.!?]$/.test(first) ? first : first + ".";
}

/**
 * Copies text to the clipboard. The trigger shows the Primer copy feedback,
 * and "Copied" is announced. Returns false when the clipboard refused.
 */
async function copyText(text, trigger = null) {
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    announce("The link could not be copied. Select it and copy it by hand.");
    return false;
  }
  if (trigger) copyFeedback(trigger);
  else announce("Copied");
  return true;
}

/** Returns the list in an API answer that is an array or {items}. */
function items(data) {
  if (Array.isArray(data)) return data;
  return (data && Array.isArray(data.items)) ? data.items : [];
}

/** Returns the note or item a panel is about. */
function subjectOf(opts) {
  return (opts && (opts.item || opts.note)) || {};
}

/** Names the subject of a panel in running text: note, folder or file. */
function nounOf(opts) {
  return opts && (opts.kind === "folder" || opts.kind === "file") ? opts.kind : "note";
}

/**
 * Returns the route prefix of the subject of a panel. An explicit route wins.
 * A folder or file passed as item uses /api/items/{id}, and a note uses
 * /api/notes/{id}.
 * @param {{route?: string, item?: {id: string}, note?: {id: string}}} opts
 * @returns {string}
 */
export function itemRoute(opts) {
  const o = opts || {};
  if (o.route) return String(o.route).replace(/\/+$/, "");
  if (o.item && o.item.id) return "/api/items/" + encodeURIComponent(o.item.id);
  return "/api/notes/" + encodeURIComponent((o.note && o.note.id) || "");
}

/** Loads the names of the members of a space, keyed by user id. */
async function memberNames(api, spaceId, signal) {
  const names = new Map();
  if (!spaceId || typeof api !== "function") return names;
  try {
    const data = await api("GET", `/api/spaces/${encodeURIComponent(spaceId)}/members`, undefined, { signal });
    for (const m of items(data)) {
      const u = m.user || m;
      const id = m.userId || u.id;
      if (id) names.set(id, u.name || u.email || "");
    }
  } catch {
    /* A note shared with the reader may not list the members of its space. */
  }
  return names;
}

/**
 * Builds the offline notice of a panel: a compact warning Banner with a
 * hidden title.
 * @param {string} text
 * @returns {HTMLElement}
 */
export function offlineNote(text) {
  const el = banner({ variant: "warning", title: "Offline", hideTitle: true, description: text, layout: "compact" });
  el.classList.add("ms-offline-note");
  return el;
}

/**
 * Builds a critical InlineMessage with an optional Try again button.
 * @param {string} text
 * @param {Function|null} [retry]
 * @returns {HTMLElement}
 */
export function inlineError(text, retry = null) {
  return h("div", { class: "ms-inline-error" },
    inlineMessage({ variant: "critical", text: sentence(text) }),
    typeof retry === "function" ? button({ label: "Try again", variant: "invisible", size: "small", onClick: retry }) : null);
}

/**
 * Builds the empty state of a panel or a list: a small Blankslate with a
 * border, an Octicon, a title, a description and at most one action. Without
 * title the text is the title.
 * @param {{icon?: string, title?: string, text?: string, action?: {label: string, onClick: Function}|Node|null, compact?: boolean, border?: boolean}} options
 * @returns {HTMLElement}
 */
export function emptyState({ icon: name = "info", title = "", text = "", action = null, compact = false, border = true } = {}) {
  const el = blankslate({
    icon: name,
    title: title || text,
    description: title ? text : "",
    size: "small",
    border,
    headingLevel: 3,
    primaryAction: action && typeof action.onClick === "function" ? { label: action.label, onClick: action.onClick } : null,
  });
  el.classList.add("ms-empty");
  if (compact) el.classList.add("is-compact");
  if (action && action.nodeType) (el.querySelector(".blankslate-inner") || el).appendChild(h("div", { class: "blankslate-action" }, action));
  return el;
}

/**
 * Builds a list for Box cards.
 * @param {string} [className]
 * @param {string} [tag] "ol" or "ul"
 * @returns {HTMLElement}
 */
export function cardList(className = "", tag = "ol") {
  return h(tag, { class: ("ms-cards " + className).trim() });
}

/**
 * Builds a Box card for an object in its own right, such as a comment thread.
 * With onSelect the card is focusable, and a click on a free part of it, or
 * Enter or Space on the card, calls onSelect.
 * @param {{tag?: string, className?: string, attrs?: object, onSelect?: Function|null}} [options]
 * @param {...(Node|string|null)} children
 * @returns {HTMLElement}
 */
export function itemCard({ tag = "li", className = "", attrs = {}, onSelect = null } = {}, ...children) {
  const card = h(tag, { ...attrs, class: ("box ms-card " + className).trim() }, ...children);
  if (typeof onSelect === "function") {
    if (!card.hasAttribute("tabindex")) card.tabIndex = 0;
    card.addEventListener("click", (ev) => {
      if (ev.target.closest("button, a, input, select, textarea, label")) return;
      onSelect(ev);
    });
    card.addEventListener("keydown", (ev) => {
      if (ev.target === card && (ev.key === "Enter" || ev.key === " ")) {
        ev.preventDefault();
        onSelect(ev);
      }
    });
  }
  return card;
}

/**
 * Highlights a card once to show where something landed. The highlight
 * fades only when the reader allows motion.
 * @param {HTMLElement} card
 */
export function flashCard(card) {
  if (!card) return;
  card.classList.remove("is-flash");
  void card.offsetWidth;
  card.classList.add("is-flash");
  setTimeout(() => card.classList.remove("is-flash"), 1200);
}

/**
 * Builds the loading rows of a panel: SkeletonBox squares and SkeletonText
 * lines that appear after 1 s, as docs/primer.md 6.5 asks.
 * @param {number} [n]
 * @returns {HTMLElement}
 */
export function skeletonCards(n = 3) {
  const el = skeleton({ variant: "rows", rows: n, label: "Loading" });
  el.classList.add("ms-loading");
  return el;
}

/**
 * Builds a section heading with an optional action at the end.
 * @param {string} title
 * @param {Node|null} [action]
 * @returns {HTMLElement}
 */
export function sectionHead(title, action = null) {
  return h("div", { class: "section-heading ms-section-head" },
    h("h3", { class: "section-heading-title", text: title }),
    action ? h("div", { class: "section-heading-actions" }, action) : null);
}

let blockSeq = 0;

/**
 * Builds a form with a section heading, stacked FormControls, an optional
 * More options Details, the primary Button at the bottom start and an
 * optional Cancel beside it (docs/primer.md 6.1). Escape cancels.
 * @param {{title: string, description?: string, className?: string, fields?: (Node|null)[], more?: Node|null, submitLabel?: string, onCancel?: Function|null, note?: Node|string|null}} options
 * @returns {{form: HTMLFormElement, submit: HTMLButtonElement, cancel: HTMLButtonElement|null}}
 */
export function formBlock({ title, description = "", className = "", fields = [], more = null, submitLabel = "Save", onCancel = null, note = null }) {
  const titleId = "ms-form-title-" + ++blockSeq;
  const submit = button({ label: submitLabel, variant: "primary", type: "submit" });
  const cancel = typeof onCancel === "function" ? button({ label: "Cancel", onClick: () => onCancel() }) : null;
  const form = h("form", { class: ["ms-form", className].filter(Boolean).join(" "), novalidate: true, "aria-labelledby": titleId },
    h("div", { class: "section-heading" },
      h("h3", { class: "section-heading-title", id: titleId, text: title }),
      description ? h("p", { class: "section-heading-description", text: description }) : null),
    ...fields,
    more,
    h("div", { class: "ms-form-actions" }, submit, cancel, note ? h("span", { class: "ms-form-note" }, note) : null));
  if (cancel) {
    form.addEventListener("keydown", (ev) => {
      if (ev.key !== "Escape" || ev.defaultPrevented) return;
      if (ev.target.closest && ev.target.closest('[aria-expanded="true"]')) return;
      ev.preventDefault();
      ev.stopPropagation();
      onCancel();
    });
  }
  return { form, submit, cancel };
}

/**
 * Builds a Details disclosure for secondary fields, closed by default.
 * @param {{label?: string, open?: boolean, className?: string}} [options]
 * @param {...(Node|null)} children
 * @returns {HTMLDetailsElement}
 */
export function disclosure({ label = "More options", open = false, className = "" } = {}, ...children) {
  const el = details({ summary: label, content: children.filter(Boolean), open });
  for (const cls of String(className).split(/\s+/).filter(Boolean)) el.classList.add(cls);
  return el;
}

/**
 * Builds a list section: a heading with a CounterLabel and an optional
 * action, then Box rows, or the empty node when there are no rows.
 * @param {{title: string, count?: number|null, action?: Node|null, description?: string, className?: string, rows?: Node[], empty?: Node|null, tag?: string}} options
 * @returns {{root: HTMLElement, head: HTMLElement, list: HTMLElement}}
 */
export function listSection({ title, count = null, action = null, description = "", className = "", rows = [], empty = null, tag = "ul" }) {
  const list = h(tag, { class: "list-rows" }, ...rows);
  const head = h("div", { class: "section-heading list-head" },
    h("h3", { class: "section-heading-title list-title" }, h("span", { text: title }), count === null || count === undefined ? null : counterLabel(count)),
    action ? h("div", { class: "section-heading-actions list-head-actions" }, action) : null);
  const root = h("section", { class: ("list-section " + className).trim() },
    head,
    description ? h("p", { class: "section-heading-description list-desc", text: description }) : null,
    rows.length || !empty ? list : empty);
  return { root, head, list };
}

/**
 * Builds a Box row: an identity column (lead, title and secondary lines) and
 * trailing controls. sub may list several secondary lines. detail spans the
 * row below both columns.
 * @param {{lead?: Node|null, title: string|Node, sub?: string|Node|null|(string|Node|null)[], trail?: Node|Node[], className?: string, attrs?: object, tag?: string, detail?: Node|null, wrapSub?: boolean}} options
 * @returns {HTMLElement}
 */
export function listRow({ lead = null, title, sub = null, trail = [], className = "", attrs = {}, tag = "li", detail = null, wrapSub = false }) {
  const titleNode = title && title.nodeType ? title : h("span", { text: title === null || title === undefined ? "" : String(title) });
  titleNode.classList.add("list-row-title");
  const subNodes = (Array.isArray(sub) ? sub : [sub]).filter((x) => x !== null && x !== undefined && x !== "").map((x) => {
    const node = x.nodeType ? x : h("span", { text: String(x) });
    node.classList.add("list-row-sub");
    if (wrapSub) node.classList.add("is-wrap");
    return node;
  });
  const trailNodes = (Array.isArray(trail) ? trail : [trail]).filter(Boolean);
  return h(tag, { ...attrs, class: ["list-row", lead ? "" : "no-lead", className].filter(Boolean).join(" ") },
    lead ? h("span", { class: "list-row-lead" }, lead) : null,
    h("span", { class: "list-row-main" }, titleNode, ...subNodes),
    trailNodes.length ? h("span", { class: "list-row-trail" }, ...trailNodes) : null,
    detail ? h("div", { class: "list-row-detail" }, detail) : null);
}

/**
 * Builds the menu button of a row: an IconButton invisible small with
 * kebab-horizontal that opens an ActionMenu. items is a menu item list for
 * openMenu, or a function that returns one when the menu opens. Empty
 * entries are skipped.
 * @param {string} label accessible name, such as "Actions for Grace Hopper"
 * @param {object[]|(() => object[])} entries
 * @returns {HTMLButtonElement}
 */
export function rowMenu(label, entries) {
  const trigger = iconButton({ icon: "kebab-horizontal", label, size: "small", className: "row-menu-btn" });
  actionMenu(trigger, () => (typeof entries === "function" ? entries() : entries).filter(Boolean), { placement: "bottom-end" });
  return trigger;
}

/**
 * Builds a quoted passage. With reveal the quote is a button that shows the
 * passage in the note. A missing passage gets a caption and no button.
 * @param {string} text raw markdown of the passage
 * @param {{missing?: boolean, reveal?: Function|null, caption?: string}} [options]
 * @returns {HTMLElement}
 */
export function quoteBlock(text, { missing = false, reveal = null, caption = "Removed from the note" } = {}) {
  const shown = quoteText(text) || String(text || "").trim();
  const words = h("span", { class: "ms-quote-text", text: shown });
  if (missing) {
    return h("blockquote", { class: "ms-quote is-missing" }, h("span", { class: "ms-quote-caption", text: caption }), words);
  }
  if (typeof reveal !== "function") return h("blockquote", { class: "ms-quote" }, words);
  return h("button", { type: "button", class: "ms-quote", onClick: reveal }, words, h("span", { class: "sr-only", text: ". Show in note" }));
}

/**
 * Builds the frame every panel shares (docs/primer.md 5.1 Rail pane): a header
 * with an h2 title and at most one small Button, a hidden toolbar row, the
 * offline Banner slot, the message slot for Banners, an inline error, the
 * body and a foot that keeps its content when the body reloads. With onClose
 * the header gets a close IconButton, for the editor drawer.
 * @param {{title: string, className?: string, action?: Node|null, onClose?: Function|null}} options
 * @returns {{root: HTMLElement, head: HTMLElement, toolbar: HTMLElement, messages: HTMLElement, body: HTMLElement, foot: HTMLElement, setAction(node: Node|null): void, setOffline(on: boolean, text?: string): void, setError(text: string, retry?: Function|null): void, banner(options: object): HTMLElement}}
 */
export function panelShell({ title, className = "", action = null, onClose = null }) {
  const titleId = "ms-panel-title-" + ++blockSeq;
  const actionSlot = h("div", { class: "ms-panel-action" }, action);
  const close = typeof onClose === "function"
    ? iconButton({ icon: "x", label: "Close " + title, className: "ms-panel-close", onClick: () => onClose() })
    : null;
  const head = h("header", { class: "ms-panel-head" }, h("h2", { class: "ms-panel-title", id: titleId, text: title }), actionSlot, close);
  const toolbar = h("div", { class: "ms-panel-toolbar", hidden: true });
  const offlineSlot = h("div", { class: "ms-panel-offline" });
  const messages = h("div", { class: "ms-panel-messages" });
  const error = h("div", { class: "ms-panel-error", hidden: true });
  const body = h("div", { class: "ms-panel-body" });
  const foot = h("div", { class: "ms-panel-foot" });
  const classes = ["ms-panel", className, typeof onClose === "function" ? "is-drawer" : ""].filter(Boolean).join(" ");
  const root = h("section", { class: classes, "aria-labelledby": titleId }, head, toolbar, offlineSlot, messages, error, body, foot);
  let offlineText = "";
  return {
    root, head, toolbar, messages, body, foot,
    setAction(node) {
      actionSlot.replaceChildren(...(node ? [node] : []));
    },
    setOffline(on, text) {
      const next = on ? text || "You are offline." : "";
      if (next !== offlineText) {
        offlineText = next;
        offlineSlot.replaceChildren(...(next ? [offlineNote(next)] : []));
      }
      root.classList.toggle("is-offline", !!on);
    },
    setError(text, retry = null) {
      error.replaceChildren(...(text ? [inlineError(text, retry)] : []));
      error.hidden = !text;
    },
    banner(options) {
      return banner({ headingLevel: 3, ...options, into: messages });
    },
  };
}

/**
 * Builds a panel on panelShell with the online gate, loading rows and
 * cleanup. load(body, ctx, current) runs while online, or always with
 * offlineOk. The body shows loading rows on the first load only, so a reload
 * keeps the rows in place. current() turns false once a newer load started or
 * the panel unmounted. While offline, a panel without offlineOk keeps its
 * content inert under the offline Banner.
 * @param {HTMLElement} container
 * @param {object} opts panel options
 * @param {{title: string, className?: string, action?: Node|null, offlineOk?: boolean, offlineText?: string}} frame
 * @param {(body: HTMLElement, ctx: object, current: () => boolean) => Promise<void>} load
 * @returns {{ctx: object, shell: object, unmount(): void}}
 */
export function createPanel(container, opts, { title, className, action = null, offlineOk = false, offlineText = "" }, load) {
  const ac = new AbortController();
  const isOnline = opts.isOnline || (() => navigator.onLine);
  const shell = panelShell({ title, className, action, onClose: opts.onClose });
  const { root, body } = shell;
  container.appendChild(root);

  let loadedOnline = false;
  let seq = 0;
  const ctx = {
    shell, root, body, signal: ac.signal, isOnline,
    /** Shows a critical Banner for a failed call, or the offline Banner when the network is gone. */
    fail(err) {
      if (ctx.offlineError(err)) return;
      shell.banner({ variant: "critical", title: sentence(errorText(err)) });
    },
    /** Returns true when a failed call means the network is gone. */
    offlineError(err) {
      if (err && err.status === 0) {
        setOffline(true);
        return true;
      }
      return false;
    },
    banner: (options) => shell.banner(options),
    reload: () => run(),
  };

  function setOffline(off) {
    shell.setOffline(off, offlineText || "You are offline. This panel comes back when the connection returns.");
    if (off && !offlineOk) body.setAttribute("inert", "");
    else body.removeAttribute("inert");
  }

  async function run() {
    const mine = ++seq;
    if (!isOnline() && !offlineOk) {
      setOffline(true);
      return;
    }
    setOffline(false);
    loadedOnline = true;
    shell.setError("");
    if (!body.firstChild) body.replaceChildren(skeletonCards(3));
    body.setAttribute("aria-busy", "true");
    try {
      await load(body, ctx, () => mine === seq && !ac.signal.aborted);
    } catch (err) {
      if (mine !== seq || ac.signal.aborted) return;
      if (ctx.offlineError(err)) {
        loadedOnline = false;
        return;
      }
      body.replaceChildren();
      shell.setError(errorText(err), () => run());
    } finally {
      if (mine === seq) body.removeAttribute("aria-busy");
    }
  }

  window.addEventListener("online", () => {
    if (offlineOk || !loadedOnline) run();
    else setOffline(false);
  }, { signal: ac.signal });
  window.addEventListener("offline", () => {
    if (offlineOk) run();
    else setOffline(true);
  }, { signal: ac.signal });
  run();

  return {
    ctx,
    shell,
    unmount() {
      ac.abort();
      seq++;
      root.remove();
    },
  };
}

/**
 * Finds the changed middle of two versions of a line, widened to whole words.
 * The text before start is the same in both lines, and so is the text after
 * endA in a and after endB in b.
 * @param {string} a the old line
 * @param {string} b the new line
 * @returns {{start: number, endA: number, endB: number}|null} null when the lines are equal or differ from end to end
 */
export function wordChange(a, b) {
  const x = String(a || "");
  const y = String(b || "");
  if (x === y) return null;
  const max = Math.min(x.length, y.length);
  let start = 0;
  while (start < max && x[start] === y[start]) start++;
  let endA = x.length;
  let endB = y.length;
  while (endA > start && endB > start && x[endA - 1] === y[endB - 1]) {
    endA--;
    endB--;
  }
  const word = (c) => /[\p{L}\p{N}_]/u.test(c || "");
  while (start > 0 && word(x[start - 1]) && (word(x[start]) || word(y[start]))) start--;
  while (endA < x.length && word(x[endA]) && (word(x[endA - 1]) || word(y[endB - 1]))) {
    endA++;
    endB++;
  }
  if (start === 0 && endA === x.length && endB === y.length) return null;
  return { start, endA, endB };
}

/** Returns the text node of a diff line after its gutter mark, or null when the line holds other markup. */
function diffLineText(line) {
  const nodes = [...line.childNodes];
  if (nodes[0] && nodes[0].nodeType === 1 && nodes[0].classList.contains("diff-mark")) nodes.shift();
  return nodes.length === 1 && nodes[0].nodeType === 3 ? nodes[0] : null;
}

/** Wraps [start, end) of a text node in a diff-word span. */
function wrapWords(node, start, end) {
  if (end <= start) return;
  const text = node.nodeValue;
  const frag = document.createDocumentFragment();
  if (start > 0) frag.append(text.slice(0, start));
  frag.append(h("span", { class: "diff-word", text: text.slice(start, end) }));
  if (end < text.length) frag.append(text.slice(end));
  node.replaceWith(frag);
}

/**
 * Marks the changed words of each removed line that a same-sized run of added
 * lines replaces, so a small edit reads at a glance.
 * @param {HTMLElement} root the diff element
 */
export function markChangedWords(root) {
  const lines = [...root.querySelectorAll(".diff-line")];
  for (let i = 0; i < lines.length; i++) {
    if (!lines[i].classList.contains("diff-del")) continue;
    let j = i;
    while (j < lines.length && lines[j].classList.contains("diff-del")) j++;
    let k = j;
    while (k < lines.length && lines[k].classList.contains("diff-add")) k++;
    if (k - j === j - i) {
      for (let n = 0; n < j - i; n++) {
        const oldText = diffLineText(lines[i + n]);
        const newText = diffLineText(lines[j + n]);
        const change = oldText && newText ? wordChange(oldText.nodeValue, newText.nodeValue) : null;
        if (!change) continue;
        wrapWords(oldText, change.start, change.endA);
        wrapWords(newText, change.start, change.endB);
      }
    }
    i = Math.max(i, k - 1);
  }
}

/* ---------- Comments ---------- */

const ANCHOR_STATES = new Set(["exact", "moved", "orphaned"]);

let anchorsPromise = null;

/**
 * Loads /static/js/anchors.js once. Resolves to the module, or to null when
 * the file is missing or lacks makeAnchor and resolveAnchor. A failed load is
 * tried again on the next call.
 * @returns {Promise<object|null>}
 */
export function loadAnchors() {
  if (!anchorsPromise) {
    anchorsPromise = import("/static/js/anchors.js").then(
      (mod) => (mod && typeof mod.makeAnchor === "function" && typeof mod.resolveAnchor === "function" ? mod : null),
      () => {
        anchorsPromise = null;
        return null;
      });
  }
  return anchorsPromise;
}

/**
 * Works out where a root thread sits in a body. A thread without an anchor is
 * general. When body is given and the stored offsets no longer hold the quoted
 * text, resolve(body, anchor) finds the passage again, which covers edits that
 * have not reached the server yet. Without a resolver the server offsets and
 * state stand.
 * @param {object} thread root comment with anchor and anchorState
 * @param {string|null} [body]
 * @param {Function|null} [resolve] (body, anchor) => {start, end, state}
 * @returns {{state: "general"|"exact"|"moved"|"orphaned", start: number, end: number}}
 */
export function threadPosition(thread, body = null, resolve = null) {
  const anchor = thread && thread.anchor;
  if (!anchor || !anchor.exact) return { state: "general", start: -1, end: -1 };
  const server = ANCHOR_STATES.has(thread.anchorState) ? thread.anchorState : "exact";
  const start = Number.isInteger(anchor.start) ? anchor.start : -1;
  const end = Number.isInteger(anchor.end) ? anchor.end : -1;
  if (typeof body === "string") {
    if (server !== "orphaned" && start >= 0 && body.slice(start, end) === anchor.exact) return { state: server, start, end };
    const found = typeof resolve === "function" ? resolve(body, anchor) : null;
    if (found && found.state) {
      return found.state === "orphaned" ? { state: "orphaned", start: -1, end: -1 } : { state: found.state, start: found.start, end: found.end };
    }
  }
  if (server === "orphaned" || start < 0 || end <= start) return { state: "orphaned", start: -1, end: -1 };
  return { state: server, start, end };
}

/**
 * Turns the quoted text of an anchor into readable text for a card. An anchor
 * holds raw markdown and can start or end inside markup, so link syntax, line
 * markers and emphasis markers are removed. Underscores inside words stay.
 * @param {string} exact
 * @returns {string}
 */
export function quoteText(exact) {
  return String(exact || "")
    .replace(/\[\[([^\]|\n]*)\|([^\]\n]*)\]\]/g, "$2")
    .replace(/\[\[([^\]\n]*)\]\]/g, "$1")
    .replace(/!?\[([^\]\n]*)\]\([^)\s]*(?:\s+"[^"\n]*")?\)/g, "$1")
    .replace(/\]\([^)\s]*\)?/g, "")
    .replace(/^[ \t]{0,3}(?:#{1,6}[ \t]+|>[ \t]?|(?:[-*+]|\d{1,9}[.)])[ \t]+(?:\[[ xX]\][ \t]+)?)/gm, "")
    .replace(/\*\*|__|~~|`+/g, "")
    .replace(/(^|[^\p{L}\p{N}])[*_]+|[*_]+(?=[^\p{L}\p{N}]|$)/gu, "$1")
    .trim();
}

/** Reports whether a root thread still has something to show. */
function threadVisible(thread) {
  return !thread.deletedAt || (Array.isArray(thread.children) && thread.children.some((c) => !c.deletedAt));
}

/**
 * Splits root threads into the groups the comments panel shows: open anchored
 * threads in document order, open general threads, open threads whose text is
 * gone, and resolved threads. A deleted root without remaining replies is left
 * out. Each entry is {thread, state, start, end}.
 * @param {object[]} threads root comments with children
 * @param {{body?: string|null, resolve?: Function|null}} [opts]
 * @returns {{anchored: object[], general: object[], orphaned: object[], resolved: object[], open: number}}
 */
export function groupThreads(threads, { body = null, resolve = null } = {}) {
  const groups = { anchored: [], general: [], orphaned: [], resolved: [], open: 0 };
  for (const thread of Array.isArray(threads) ? threads : []) {
    if (!thread || thread.parentId || !threadVisible(thread)) continue;
    const pos = threadPosition(thread, body, resolve);
    const stored = thread.anchor && Number.isInteger(thread.anchor.start) ? thread.anchor.start : 0;
    const entry = { thread, ...pos, order: pos.start >= 0 ? pos.start : stored };
    if (thread.resolved) groups.resolved.push(entry);
    else if (pos.state === "general") groups.general.push(entry);
    else if (pos.state === "orphaned") groups.orphaned.push(entry);
    else groups.anchored.push(entry);
  }
  const byTime = (a, b) => String(a.thread.createdAt || "").localeCompare(String(b.thread.createdAt || ""));
  const byDoc = (a, b) => a.order - b.order || a.end - b.end || byTime(a, b);
  const rank = (e) => (e.state === "general" ? 1 : e.state === "orphaned" ? 2 : 0);
  groups.anchored.sort(byDoc);
  groups.general.sort(byTime);
  groups.orphaned.sort(byTime);
  groups.resolved.sort((a, b) => rank(a) - rank(b) || (rank(a) === 0 ? byDoc(a, b) : byTime(a, b)));
  groups.open = groups.anchored.length + groups.general.length + groups.orphaned.length;
  return groups;
}

/**
 * Counts the threads of a note: open ones, resolved ones and both.
 * @param {object[]} threads root comments with children
 * @returns {{open: number, resolved: number, total: number}}
 */
export function commentCounts(threads) {
  const groups = groupThreads(threads);
  return { open: groups.open, resolved: groups.resolved.length, total: groups.open + groups.resolved.length };
}

/**
 * Places the reading view Comment button, in viewport pixels. On desktop it
 * sits centred above the selection, or below it when the top inset is in the
 * way. On touch screens it sits below the end of the selection, past the
 * selection handle, because Android and iOS draw their selection menu above
 * the selection. When that runs into the bottom inset it moves above the
 * selection and clear of the menu.
 * @param {{top: number, bottom: number, left: number, right: number}} selection bounding box of the selection
 * @param {{top: number, bottom: number, left: number, right: number}} end box of the last selected line
 * @param {{width: number, height: number}} button
 * @param {{width: number, height: number, top?: number, bottom?: number}} viewport top and bottom are the insets bars cover
 * @param {{touch?: boolean}} [opts]
 * @returns {{left: number, top: number, placement: "above"|"below"}}
 */
export function commentButtonPosition(selection, end, button, viewport, { touch = false } = {}) {
  const GAP = 8;
  const HANDLE = 28;
  const MENU = 56;
  const minTop = (viewport.top || 0) + GAP;
  const maxTop = Math.max(minTop, viewport.height - (viewport.bottom || 0) - button.height - GAP);
  let left;
  let top;
  let placement;
  if (touch) {
    left = end.right - button.width / 2;
    top = end.bottom + HANDLE;
    placement = "below";
    if (top > maxTop) {
      top = selection.top - MENU - button.height;
      placement = "above";
      if (top < minTop) {
        top = maxTop;
        placement = "below";
      }
    }
  } else {
    left = (selection.left + selection.right) / 2 - button.width / 2;
    top = selection.top - button.height - GAP;
    placement = "above";
    if (top < minTop) {
      top = selection.bottom + GAP;
      placement = "below";
    }
  }
  left = Math.max(GAP, Math.min(left, viewport.width - button.width - GAP));
  top = Math.max(minTop, Math.min(top, maxTop));
  return { left: Math.round(left), top: Math.round(top), placement };
}

/**
 * Creates the comment list of one note, shared by the panel, the reading view
 * and the editor. load() shows the threads cached in the store first, then
 * fetches GET {route}/comments when online and caches the result with
 * store.putComments. route defaults to /api/notes/{noteId}, and a folder or
 * file passes /api/items/{id}. Calls made while a load runs coalesce into one
 * more load.
 * @param {{noteId: string, route?: string, api?: Function, store?: object, isOnline?: () => boolean}} options
 * @returns {{readonly threads: object[], readonly state: {loaded: boolean, offline: boolean, cached: boolean, error: any}, load(): Promise<void>, subscribe(fn: Function): () => void}}
 */
export function createCommentSource({ noteId, route = "", api, store, isOnline = () => navigator.onLine }) {
  const base = route ? String(route).replace(/\/+$/, "") : `/api/notes/${encodeURIComponent(noteId)}`;
  let threads = [];
  let state = { loaded: false, offline: false, cached: false, error: null };
  let triedCache = false;
  let running = null;
  let again = false;
  const subscribers = new Set();

  const emit = () => {
    for (const fn of [...subscribers]) {
      try {
        fn(threads, state);
      } catch (err) {
        console.error("[markspace] comment listener failed", err);
      }
    }
  };

  async function fromCache() {
    triedCache = true;
    if (!store || typeof store.listComments !== "function") return;
    try {
      const cached = await store.listComments(noteId);
      // An empty cache is not an answer; the server may still have threads.
      if (Array.isArray(cached) && cached.length && !(state.loaded && !state.cached)) {
        threads = threadComments(cached);
        state = { ...state, loaded: true, cached: true };
        emit();
      }
    } catch { /* no cache */ }
  }

  async function once() {
    if (!triedCache) await fromCache();
    if (typeof api !== "function" || !isOnline()) {
      state = { ...state, loaded: true, offline: true };
      emit();
      return;
    }
    try {
      const data = await api("GET", `${base}/comments`);
      const list = items(data);
      threads = threadComments(list);
      state = { loaded: true, offline: false, cached: false, error: null };
      if (store && typeof store.putComments === "function") {
        Promise.resolve().then(() => store.putComments(noteId, list)).catch(() => {});
      }
    } catch (err) {
      state = err && err.status === 0 ? { ...state, loaded: true, offline: true } : { ...state, loaded: true, error: err };
    }
    emit();
  }

  return {
    get threads() { return threads; },
    get state() { return state; },
    load() {
      if (running) {
        again = true;
        return running;
      }
      running = (async () => {
        do {
          again = false;
          await once();
        } while (again);
        running = null;
      })();
      return running;
    },
    subscribe(fn) {
      subscribers.add(fn);
      return () => subscribers.delete(fn);
    },
  };
}

/** Finds the root thread holding a comment id. */
function rootOf(threads, id) {
  for (const t of threads) {
    if (t.id === id || (Array.isArray(t.children) && t.children.some((c) => c.id === id))) return t;
  }
  return null;
}

/**
 * Opens the Comments panel (docs/primer.md 5.10). Each thread holds its quoted
 * passage as plain muted text and a condensed Timeline of comments, then a
 * footer with Reply. Threads have no box. A hairline divider separates them.
 * Open anchored threads come in document order, then general threads and
 * threads whose text is gone. Resolved threads sit in a Details
 * "Resolved (n)" with their passage, who resolved them and a Reopen Button.
 * Threads come from a comment source, so cached threads show offline.
 * Posting, resolving and deleting need a connection, and their controls are
 * removed while offline.
 *
 * Resolving replaces the thread with an undo row. Deleting a comment asks in
 * a ConfirmationDialog, because the server cannot bring it back.
 *
 * Extra options: {source, focusId, draftAnchor, getBody, resolve, onActivate,
 * onReveal, onCount}. getBody() returns the body the reader sees,
 * resolve(body, anchor) finds a passage in it, onActivate(id) fires with the
 * emphasised thread id or null and must not scroll, onReveal(id) scrolls the
 * host to a thread's passage when its quote is chosen, and onCount(n)
 * reports the number of open threads. A folder or file passes item, kind and
 * the item route.
 * @param {HTMLElement} container
 * @param {object} opts
 * @returns {{element: HTMLElement, focus(id: string, options?: {moveFocus?: boolean}): void, draft(anchor: object|null): void, reload(): Promise<void>, unmount(): void}}
 */
export function openComments(container, opts) {
  const api = opts.api;
  const me = opts.user && opts.user.id;
  const subject = subjectOf(opts);
  const noteKey = subject.id;
  const route = itemRoute(opts);
  const noun = nounOf(opts);
  const canComment = accessRank(opts.access) >= ACCESS_RANK.comment && !subject.deleted;
  const canManage = accessRank(opts.access) >= ACCESS_RANK.manage;
  const isOnline = opts.isOnline || (() => navigator.onLine);
  const source = opts.source || createCommentSource({ noteId: noteKey, route, api, store: opts.store, isOnline });
  const ac = new AbortController();
  let anchors = null;
  let lastActive = null;
  let flashTimer = 0;

  const ui = {
    resolvedOpen: false,
    focusedId: null,
    hoverId: null,
    draft: opts.draftAnchor ? { anchor: opts.draftAnchor } : null,
    drafts: new Map(),
    editing: new Set(),
    // Threads whose reply box has been opened. A box stays open once shown,
    // because collapsing it when focus moves would shift the threads below
    // between press and release and swallow the click.
    replying: new Set(),
    pendingFocus: opts.focusId ? { id: opts.focusId, moveFocus: true } : null,
  };

  const newButton = canComment
    ? button({ label: "Comment", size: "small", leadingVisual: "comment", className: "ms-comments-new", onClick: () => startDraft(null) })
    : null;
  const shell = panelShell({ title: "Comments", className: "ms-comments", action: newButton, onClose: opts.onClose });
  const draftBox = h("div", { class: "ms-comments-draft" });
  const list = h("div", { class: "ms-comments-list" });
  shell.body.append(draftBox, list);
  const root = shell.root;
  container.appendChild(root);

  const enc = encodeURIComponent;
  const bodyText = () => (typeof opts.getBody === "function" ? opts.getBody() : typeof subject.content === "string" ? subject.content : null);
  const resolver = () => (typeof opts.resolve === "function" ? opts.resolve : anchors ? anchors.resolveAnchor : null);

  /** Reports a failed call in a critical Banner, or redraws the offline state. */
  function fail(err) {
    if (err && err.status === 0) {
      render();
      return;
    }
    shell.banner({ variant: "critical", title: sentence(errorText(err)) });
  }

  /** Tells the host which thread to emphasise: the hovered one, else the focused one. */
  function activate() {
    const id = ui.hoverId || ui.focusedId || null;
    if (id === lastActive) return;
    lastActive = id;
    if (typeof opts.onActivate === "function") opts.onActivate(id);
  }

  function setFocused(id) {
    ui.focusedId = id;
    for (const card of list.querySelectorAll(".ms-thread")) {
      card.classList.toggle("is-focused", card.getAttribute("data-comment-id") === id);
    }
    activate();
  }

  /** Remembers the text of every open composer so a re-render keeps it. */
  function saveDrafts() {
    for (const ta of root.querySelectorAll("textarea[data-draft-key]")) {
      if (ta.value) ui.drafts.set(ta.getAttribute("data-draft-key"), ta.value);
      else ui.drafts.delete(ta.getAttribute("data-draft-key"));
    }
  }

  /** Names the author of a comment or the person who resolved a thread. */
  function personLabel(userId, author = null) {
    if (userId && userId === me) return "You";
    if (author) return nameOf(author);
    for (const t of source.threads) {
      for (const c of [t, ...(Array.isArray(t.children) ? t.children : [])]) {
        if (c.userId === userId && c.author) return nameOf(c.author);
      }
    }
    return "Someone";
  }

  /**
   * Builds a comment box: a Textarea with a hidden label, then Cancel and the
   * submit Button at the end (docs/primer.md 6.1). A refused comment stays in the
   * box with the reason as its validation message.
   */
  function composer({ key, label, submitLabel, rows = 3, onSubmit, onDone, onCancel, className = "" }) {
    const control = formControl({ label, name: "body", type: "textarea", rows, hideLabel: true });
    const ta = control.querySelector("textarea");
    ta.setAttribute("data-draft-key", key);
    ta.value = ui.drafts.get(key) || "";
    const send = button({ label: submitLabel, variant: "primary", size: "small", type: "submit" });
    const cancel = typeof onCancel === "function" ? button({ label: "Cancel", size: "small", onClick: () => onCancel() }) : null;
    const form = h("form", { class: ("ms-composer " + className).trim(), novalidate: true }, control, h("div", { class: "ms-composer-actions" }, cancel, send));
    const submit = async () => {
      const text = ta.value.trim();
      if (!text) {
        setFieldError(control, "Enter a comment");
        ta.focus();
        return;
      }
      if (!isOnline()) {
        render();
        return;
      }
      setBusy(send, true);
      let result;
      try {
        result = await onSubmit(text);
      } catch (err) {
        setBusy(send, false);
        if (err && (err.status === 400 || err.status === 413 || err.field)) {
          setFieldError(control, err.field === "anchor"
            ? "The selected text is no longer in the note. Select it again to comment on it."
            : sentence(errorText(err)));
          ta.focus();
          return;
        }
        fail(err);
        return;
      }
      ta.value = "";
      ui.drafts.delete(key);
      if (onDone) await onDone(result);
    };
    form.addEventListener("submit", (ev) => {
      ev.preventDefault();
      submit();
    });
    ta.addEventListener("input", () => {
      ui.drafts.set(key, ta.value);
      setFieldError(control, "");
    });
    ta.addEventListener("keydown", (ev) => {
      if (ev.key === "Enter" && (ev.ctrlKey || ev.metaKey)) {
        ev.preventDefault();
        submit();
      } else if (ev.key === "Escape" && onCancel) {
        ev.preventDefault();
        ev.stopPropagation();
        onCancel();
      }
    });
    return form;
  }

  function draftCard() {
    const anchor = ui.draft.anchor;
    const form = composer({
      key: "new",
      label: anchor ? "Comment on this passage" : `Comment on this ${noun}`,
      submitLabel: "Comment",
      rows: 3,
      onSubmit: (text) => api("POST", `${route}/comments`, anchor ? { noteId: noteKey, body: text, anchor } : { noteId: noteKey, body: text }),
      onDone: async (created) => {
        ui.draft = null;
        if (created && created.id) ui.pendingFocus = { id: created.id, moveFocus: false };
        announce("Comment posted");
        await source.load();
      },
      onCancel: () => {
        ui.draft = null;
        ui.drafts.delete("new");
        render();
        if (newButton) newButton.focus();
      },
    });
    return h("div", { class: "ms-thread ms-draft is-focused" },
      anchor ? quoteBlock(anchor.exact) : h("p", { class: "ms-draft-label", text: `General comment on this ${noun}` }),
      h("div", { class: "ms-thread-foot" }, form));
  }

  /**
   * Builds one comment. The metadata is one small muted line: a 16px Avatar,
   * the author and the time. Actions sit in the overflow menu at the end of
   * that line, so the body is the most prominent part.
   */
  function commentItem(c, thread, offline, card) {
    const mine = !!(c.userId && c.userId === me);
    const deleted = !!c.deletedAt;
    const author = c.author || (mine ? opts.user : null);
    const name = mine ? "You" : nameOf(author);
    // editedAt is set only by a body edit. Resolving moves updatedAt, so it is not used.
    const edited = !deleted && !!c.editedAt;
    const head = h("div", { class: "ms-comment-head" },
      avatarFor(author || { name }, 16),
      h("span", { class: "ms-comment-author", text: name }),
      h("span", { class: "ms-comment-time" }, timeEl(c.createdAt)),
      edited ? h("span", { class: "ms-comment-edited", text: "edited" }) : null);
    let body;
    if (ui.editing.has(c.id) && !deleted && !offline) {
      if (!ui.drafts.has("edit:" + c.id)) ui.drafts.set("edit:" + c.id, c.body);
      body = composer({
        key: "edit:" + c.id,
        label: "Edit comment",
        submitLabel: "Save",
        onSubmit: (text) => api("PATCH", `/api/comments/${enc(c.id)}`, { body: text }),
        onDone: async () => {
          ui.editing.delete(c.id);
          await source.load();
        },
        onCancel: () => {
          ui.editing.delete(c.id);
          ui.drafts.delete("edit:" + c.id);
          render();
        },
      });
    } else {
      body = h("div", { class: "ms-comment-body" + (deleted ? " is-deleted" : ""), text: deleted ? "Comment deleted" : c.body });
    }
    const root = c === thread;
    // A resolved thread shows Reopen as a Button in its foot, so the menu offers only Resolve.
    const canResolve = root && !thread.resolved && !deleted && !offline && canComment;
    const canChange = !deleted && !offline && (mine || canManage);
    if (canResolve || canChange) {
      const menu = rowMenu("Actions for the comment by " + name, () => {
        const entries = [];
        if (canResolve) {
          entries.push({ label: "Resolve thread", icon: "check", onClick: () => resolveThread(thread, card) });
        }
        if (canChange && mine) {
          entries.push({ label: "Edit", icon: "pencil", onClick: () => { ui.editing.add(c.id); setFocused(thread.id); render(); focusDraft("edit:" + c.id); } });
        }
        if (canChange) {
          if (entries.length) entries.push({ separator: true });
          entries.push({ label: "Delete", icon: "trash", danger: true, onClick: () => removeComment(c, menu) });
        }
        return entries;
      });
      head.appendChild(h("span", { class: "ms-comment-actions" }, menu));
    }
    return h("li", { class: "ms-comment", "data-comment-id": c.id }, head, body);
  }

  /**
   * Builds the small muted line that says who resolved a thread and when:
   * "Resolved by Ana, 2 hours ago". Resolving sets updatedAt, so that is the
   * time. When a body edit came later, editedAt is not before updatedAt and
   * the line leaves the time out.
   */
  function resolvedItem(t) {
    const who = t.resolvedBy ? personLabel(t.resolvedBy) : "";
    const editedLater = t.editedAt && t.updatedAt && Date.parse(t.editedAt) >= Date.parse(t.updatedAt);
    const when = t.updatedAt && !editedLater ? timeEl(t.updatedAt) : null;
    const text = who ? "Resolved by " + (who === "You" ? "you" : who) : "Resolved";
    return h("li", { class: "ms-resolved-event" },
      h("span", { class: "ms-marker-icon", "aria-hidden": "true" }, icon("check", 16)),
      h("span", {}, when ? text + ", " : text, when));
  }

  function threadCard(entry, offline) {
    const t = entry.thread;
    // A thread has no box. A hairline divider separates it from the thread above (docs/primer.md 5.10).
    const card = h("li", { class: "ms-thread" + (t.resolved ? " is-resolved" : ""), tabindex: "0", "data-comment-id": t.id });
    if (entry.state !== "general" && t.anchor && t.anchor.exact) {
      const reveal = typeof opts.onReveal === "function" && entry.state !== "orphaned"
        ? () => {
          setFocused(t.id);
          opts.onReveal(t.id);
        }
        : null;
      const missing = entry.state === "orphaned";
      card.appendChild(quoteBlock(t.anchor.exact, missing && t.resolved ? { missing, reveal, caption: "Text no longer in the note" } : { missing, reveal }));
    }
    const timeline = h("ol", { class: "ms-thread-comments", role: "list" }, commentItem(t, t, offline, card));
    for (const c of Array.isArray(t.children) ? t.children : []) {
      if (!c.deletedAt) timeline.appendChild(commentItem(c, t, offline, card));
    }
    if (t.resolved) timeline.appendChild(resolvedItem(t));
    card.appendChild(timeline);

    // Resolve and Reopen live in the menu of the first comment. The foot holds only Reply.
    if (canComment && !offline && !t.resolved) {
      const foot = h("div", { class: "ms-thread-foot" });
      {
        const key = "reply:" + t.id;
        const replying = ui.replying.has(t.id) || ui.focusedId === t.id || ui.drafts.has(key);
        if (replying) {
          foot.appendChild(composer({
            key,
            label: "Reply",
            submitLabel: "Reply",
            rows: 3,
            onSubmit: (text) => api("POST", `${route}/comments`, { noteId: noteKey, parentId: t.id, body: text }),
            onDone: () => {
              announce("Reply posted");
              return source.load();
            },
            onCancel: () => {
              ui.replying.delete(t.id);
              ui.drafts.delete(key);
              if (ui.focusedId === t.id) ui.focusedId = null;
              render();
            },
          }));
        }
        if (!replying) foot.appendChild(h("div", { class: "ms-thread-actions" },
          button({
            label: "Reply", size: "small",
            onClick: () => {
              ui.replying.add(t.id);
              setFocused(t.id);
              render();
              focusDraft(key);
            },
          })));
      }
      card.appendChild(foot);
    }
    // A resolved thread offers Reopen, never Undo.
    if (canComment && !offline && t.resolved) {
      card.appendChild(h("div", { class: "ms-thread-foot" }, h("div", { class: "ms-thread-actions" },
        button({ label: "Reopen", size: "small", leadingVisual: "issue-reopened", onClick: () => reopen(t) }))));
    }
    card.classList.toggle("is-focused", ui.focusedId === t.id);
    card.addEventListener("pointerenter", (ev) => {
      if (ev.pointerType !== "mouse") return;
      ui.hoverId = t.id;
      activate();
    });
    card.addEventListener("pointerleave", () => {
      if (ui.hoverId !== t.id) return;
      ui.hoverId = null;
      activate();
    });
    card.addEventListener("focusin", () => {
      if (ui.focusedId !== t.id) setFocused(t.id);
    });
    card.addEventListener("click", (ev) => {
      if (ev.target.closest("button, a, textarea, input")) return;
      setFocused(t.id);
    });
    return card;
  }

  function group(title, entries, offline, className = "") {
    const ol = cardList("ms-threads " + className);
    for (const e of entries) ol.appendChild(threadCard(e, offline));
    return title ? h("section", { class: "ms-group" }, h("h3", { class: "ms-group-title", text: title }), ol) : ol;
  }

  function render() {
    // The source also reports offline when a fetch failed for lack of a connection.
    const offline = !isOnline() || !!source.state.offline;
    const active = document.activeElement;
    const activeKey = active && root.contains(active) && active.getAttribute ? active.getAttribute("data-draft-key") : null;
    const caret = activeKey ? active.selectionStart : 0;
    saveDrafts();

    shell.setOffline(offline, canComment
      ? "You are offline. These comments are from the last sync. Posting needs a connection."
      : "You are offline. These comments are from the last sync.");
    if (newButton) newButton.hidden = offline;

    draftBox.replaceChildren();
    if (ui.draft && canComment && !offline) draftBox.appendChild(draftCard());

    const st = source.state;
    const groups = groupThreads(source.threads, { body: bodyText(), resolve: resolver() });
    const resolvedCount = groups.resolved.length;

    list.replaceChildren();
    if (!st.loaded) {
      list.appendChild(skeletonCards(2));
    } else if (st.error && !source.threads.length) {
      list.appendChild(inlineError("Comments could not be loaded. " + sentence(errorText(st.error)), () => source.load()));
    } else if (!groups.open && !resolvedCount) {
      if (!ui.draft && !(offline && !st.cached)) {
        list.appendChild(emptyState({
          icon: "comment",
          title: "No comments yet",
          text: !canComment ? "" : noun === "note" ? "Select text in the note and choose Comment." : `Choose Comment to start a thread on this ${noun}.`,
        }));
      }
    } else {
      if (groups.anchored.length) list.appendChild(group("", groups.anchored, offline));
      if (groups.general.length) list.appendChild(group(noun === "note" ? "General" : "", groups.general, offline));
      if (groups.orphaned.length) list.appendChild(group("Text no longer found", groups.orphaned, offline, "is-orphaned"));
      if (!groups.open) list.appendChild(emptyState({ icon: "check", title: "No open comments", text: "Every thread is resolved." }));
      if (resolvedCount) {
        list.appendChild(details({
          summary: `Resolved (${resolvedCount})`,
          open: ui.resolvedOpen,
          content: group("", groups.resolved, offline, "is-resolved"),
          onToggle: (open) => {
            ui.resolvedOpen = open;
          },
        }));
      }
    }

    if (activeKey) {
      const again = root.querySelector(`textarea[data-draft-key="${CSS.escape(activeKey)}"]`);
      if (again) {
        again.focus({ preventScroll: true });
        try { again.setSelectionRange(caret, caret); } catch { /* not a text field */ }
      }
    }
    if (typeof opts.onCount === "function") opts.onCount(groups.open);
    if (ui.pendingFocus && st.loaded) {
      const pending = ui.pendingFocus;
      ui.pendingFocus = null;
      focus(pending.id, pending);
    }
  }

  function focusDraft(key) {
    const ta = root.querySelector(`textarea[data-draft-key="${CSS.escape(key)}"]`);
    if (ta) {
      ta.focus({ preventScroll: true });
      try { ta.setSelectionRange(ta.value.length, ta.value.length); } catch { /* not a text field */ }
    }
  }

  function startDraft(anchor) {
    if (!canComment) return;
    ui.draft = { anchor: anchor || null };
    render();
    draftBox.scrollIntoView({ block: "nearest" });
    focusDraft("new");
  }

  function focus(id, { moveFocus = true } = {}) {
    if (!id) return;
    if (!source.state.loaded) {
      ui.pendingFocus = { id, moveFocus };
      return;
    }
    const thread = rootOf(source.threads, id);
    if (!thread) return;
    if (thread.resolved && !ui.resolvedOpen) {
      ui.resolvedOpen = true;
      render();
    }
    const card = list.querySelector(`.ms-thread[data-comment-id="${CSS.escape(thread.id)}"]`);
    if (!card) return;
    setFocused(thread.id);
    card.scrollIntoView({ block: "nearest", behavior: prefersReducedMotion() ? "auto" : "smooth" });
    if (moveFocus) card.focus({ preventScroll: true });
    clearTimeout(flashTimer);
    for (const other of list.querySelectorAll(".ms-thread.is-flash")) other.classList.remove("is-flash");
    void card.offsetWidth;
    card.classList.add("is-flash");
    flashTimer = setTimeout(() => card.classList.remove("is-flash"), 1200);
  }

  const postResolved = (thread, resolved) => api("POST", `/api/comments/${enc(thread.id)}/resolve`, { resolved });

  /**
   * Resolves a thread at once and replaces it with an undo row, as Primer
   * prescribes for a non-destructive action. The list reloads when the
   * person undoes or acts elsewhere in the list. A render before that moves
   * the thread into the resolved list, where its action is Reopen, so the
   * Undo never moves into a Banner.
   */
  async function resolveThread(thread, card) {
    if (ui.focusedId === thread.id) {
      ui.focusedId = null;
      activate();
    }
    ui.replying.delete(thread.id);
    try {
      await postResolved(thread, true);
    } catch (err) {
      fail(err);
      return;
    }
    const row = undoRow(card, { message: "Thread resolved", onUndo: () => postResolved(thread, false) });
    await row.done;
    await source.load();
  }

  /** Reopens a resolved thread at once. It moves back to the open threads and takes focus. */
  async function reopen(thread) {
    try {
      await postResolved(thread, false);
    } catch (err) {
      fail(err);
      return;
    }
    announce("Thread reopened");
    ui.pendingFocus = { id: thread.id, moveFocus: true };
    await source.load();
  }

  /** Deletes a comment after a ConfirmationDialog, since the server cannot bring it back. */
  async function removeComment(c, trigger) {
    const ok = await confirmDialog({
      title: "Delete comment?",
      body: "The comment is removed for everyone. This cannot be undone.",
      confirmLabel: "Delete",
      returnFocus: trigger,
      onConfirm: () => api("DELETE", `/api/comments/${enc(c.id)}`),
    });
    if (!ok) return;
    announce("Comment deleted");
    await source.load();
  }

  const unsubscribe = source.subscribe(() => render());
  window.addEventListener("online", () => {
    render();
    source.load();
  }, { signal: ac.signal });
  window.addEventListener("offline", () => render(), { signal: ac.signal });
  loadAnchors().then((mod) => {
    anchors = mod;
    if (mod && !opts.resolve && !ac.signal.aborted) render();
  });
  render();
  if (!opts.source) source.load();

  return {
    element: root,
    focus,
    draft: startDraft,
    reload: () => source.load(),
    unmount() {
      ac.abort();
      clearTimeout(flashTimer);
      unsubscribe();
      root.remove();
    },
  };
}

/* ---------- Share ---------- */

const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

/** Formats a date input value (YYYY-MM-DD) from an ISO timestamp in local time. */
function dateInputValue(iso) {
  const d = new Date(iso);
  if (!Number.isFinite(d.getTime())) return "";
  const pad = (n) => String(n).padStart(2, "0");
  return d.getFullYear() + "-" + pad(d.getMonth() + 1) + "-" + pad(d.getDate());
}

/**
 * Opens the Share panel (docs/primer.md 5.10). People with access come first as
 * Box rows, each with an access ActionMenu and a row menu that ends with
 * Remove access. Removing access acts at once with an undo row. The Add a
 * person form follows. Public links come next, each with its address in a
 * read-only TextInput with Copy, Labels for the access and the password, and
 * a row menu with Revoke link behind a ConfirmationDialog. Add public link is
 * a Details that holds the form for a new link.
 *
 * The routes come from itemRoute, so a folder or file shares through
 * /api/items/{id}.
 * @param {HTMLElement} container
 * @param {object} opts
 * @returns {{unmount(): void}}
 */
export function openShare(container, opts) {
  const api = opts.api;
  const subject = subjectOf(opts);
  const route = itemRoute(opts);
  const noun = nounOf(opts);
  const childrenLabel = noun === "folder" ? "Include everything inside" : noun === "note" ? "Include child pages" : "";
  const childrenNote = noun === "folder" ? "Includes everything inside" : "Includes child pages";
  const state = { shares: [], links: [], editing: null, addOpen: false };

  const panel = createPanel(container, opts, {
    title: "Share",
    className: "ms-share",
    offlineText: "You are offline. Sharing changes need a connection.",
  }, async (body, ctx, current) => {
    if (accessRank(opts.access) < ACCESS_RANK.manage) {
      body.replaceChildren(emptyState({ icon: "lock", title: "You cannot change sharing", text: `Only people who manage this ${noun} can change who has access.` }));
      return;
    }
    const [shares, links] = await Promise.all([
      api("GET", `${route}/shares`, undefined, { signal: ctx.signal }),
      api("GET", `${route}/links`, undefined, { signal: ctx.signal }),
    ]);
    if (!current()) return;
    state.shares = items(shares);
    state.links = items(links);
    if (typeof opts.onCount === "function") opts.onCount(state.shares.length);
    draw(ctx);
  });

  /** Draws both sections from the last loaded data, without a request. */
  function draw(ctx) {
    ctx.body.replaceChildren(peopleSection(ctx), linksSection(ctx));
  }

  function peopleSection(ctx) {
    const { root } = listSection({
      title: "People",
      count: state.shares.length,
      className: "ms-person-list",
      rows: state.shares.map((s) => personRow(ctx, s)),
      empty: emptyState({ icon: "people", title: "Not shared with anyone", text: `Members of the space can open this ${noun}.` }),
    });
    return h("div", { class: "ms-section ms-people" }, root, addPersonForm(ctx));
  }

  function addPersonForm(ctx) {
    let chosen = null;
    const personField = formControl({ label: "Email or name", name: "person", type: "text", autocomplete: "off", spellcheck: "false" });
    const input = personField.querySelector("input");
    const accessField = formControl({
      label: "Access", name: "access", type: "select", value: "view",
      options: accessChoices("share").map((a) => ({ value: a, label: accessLabel(a) })),
    });
    const childrenField = childrenLabel ? formControl({ label: childrenLabel, name: "includeChildren", type: "checkbox", value: noun === "folder" }) : null;
    const { form, submit } = formBlock({
      title: "Add a person",
      className: "ms-share-add",
      fields: [personField, accessField],
      more: childrenField ? disclosure({}, childrenField) : null,
      submitLabel: "Share",
    });
    const shared = () => new Set(state.shares.map((s) => s.userId));
    autocomplete(input, {
      minLength: 2,
      filter: false,
      emptyText: "No matching people",
      source: async (q) => {
        const data = await api("GET", `/api/users/lookup?q=${encodeURIComponent(q.trim())}&limit=8`, undefined, { signal: ctx.signal });
        const skip = shared();
        return items(data)
          .filter((u) => (!opts.user || u.id !== opts.user.id) && !skip.has(u.id))
          .map((u) => ({ id: u.id, label: u.name || u.email, description: u.name ? u.email : "", icon: "person" }));
      },
      allowAdd: (value) => (EMAIL_PATTERN.test(String(value).trim()) ? { id: "email:" + String(value).trim(), label: "Invite " + String(value).trim(), email: String(value).trim() } : null),
      onSelect: (item) => {
        chosen = item.email ? { email: item.email, label: item.email } : { userId: item.id, label: item.label };
        input.value = chosen.label;
        setFieldError(personField, "");
      },
    });
    input.addEventListener("input", () => {
      chosen = null;
      setFieldError(personField, "");
    });
    form.addEventListener("submit", async (ev) => {
      ev.preventDefault();
      const typed = input.value.trim();
      if (!chosen || chosen.label !== typed) {
        chosen = EMAIL_PATTERN.test(typed) ? { email: typed, label: typed } : null;
      }
      if (!chosen) {
        setFieldError(personField, typed ? "Choose a person from the list or enter an email address" : "Enter an email address or a name");
        input.focus();
        return;
      }
      setBusy(submit, true);
      try {
        await api("POST", `${route}/shares`, {
          noteId: subject.id, userId: chosen.userId, email: chosen.email,
          access: accessField.querySelector("select").value,
          includeChildren: childrenField ? childrenField.querySelector("input").checked : false,
        });
      } catch (err) {
        setBusy(submit, false);
        if (!ctx.offlineError(err)) showFieldErrors(form, err);
        return;
      }
      announce("Shared with " + chosen.label);
      ctx.reload();
    });
    return form;
  }

  function personRow(ctx, s) {
    const u = s.user || {};
    const name = u.name || u.email || s.userId;
    const refocus = () => {
      const again = ctx.body.querySelector(`[data-share-id="${CSS.escape(s.id)}"] .ms-access-btn`);
      if (again) again.focus();
    };
    const patch = async (change) => {
      const next = { access: s.access, includeChildren: !!s.includeChildren, ...change };
      try {
        await api("PATCH", `/api/shares/${encodeURIComponent(s.id)}`, next);
      } catch (err) {
        ctx.fail(err);
        return;
      }
      s.access = next.access;
      s.includeChildren = next.includeChildren;
      draw(ctx);
      refocus();
      announce(accessLabel(s.access) + " for " + name);
    };
    const accessBtn = button({
      label: accessLabel(s.access), size: "small", trailingVisual: "triangle-down", className: "ms-access-btn",
      attrs: { "aria-label": `${accessLabel(s.access)}, access for ${name}` },
    });
    actionMenu(accessBtn, () => accessChoices("share").map((a) => ({
      label: accessLabel(a),
      checked: a === s.access,
      selectionType: "single",
      onClick: () => {
        if (a !== s.access) patch({ access: a });
      },
    })), { placement: "bottom-end", label: "Access for " + name });
    let row = null;
    const remove = async () => {
      const result = await undoable({
        label: "Removed access for " + name,
        where: row,
        into: panel.shell.messages,
        run: () => api("DELETE", `/api/shares/${encodeURIComponent(s.id)}`),
        undo: () => api("POST", `${route}/shares`, {
          noteId: subject.id, userId: s.userId, access: s.access, includeChildren: !!s.includeChildren,
        }),
      });
      if (result !== "failed") ctx.reload();
    };
    const menu = rowMenu("Actions for " + name, () => [
      childrenLabel ? { label: childrenLabel, checked: !!s.includeChildren, selectionType: "multiple", onClick: () => patch({ includeChildren: !s.includeChildren }) } : null,
      childrenLabel ? { separator: true } : null,
      { label: "Remove access", icon: "trash", danger: true, onClick: remove },
    ]);
    row = listRow({
      lead: avatarFor(u.name || u.email ? u : { name }, 32),
      title: name,
      sub: [u.name && u.email ? u.email : null, s.includeChildren && childrenLabel ? childrenNote : null],
      trail: [accessBtn, menu],
      className: "ms-person",
      attrs: { "data-share-id": s.id },
    });
    return row;
  }

  function linksSection(ctx) {
    const editing = state.editing ? state.links.find((l) => l.id === state.editing) : null;
    const { root } = listSection({
      title: "Public links",
      count: state.links.length,
      className: "ms-link-list",
      rows: state.links.map((link) => linkRow(ctx, link)),
      empty: emptyState({ icon: "globe", title: "No public links", text: `Anyone with a public link can read the ${noun} without an account.` }),
    });
    const wrap = h("div", { class: "ms-section ms-links" }, root);
    if (editing) {
      wrap.appendChild(h("div", { class: "box ms-link-edit" }, linkForm(ctx, editing)));
    } else {
      wrap.appendChild(details({
        summary: "Add public link",
        kind: "add",
        open: state.addOpen,
        content: linkForm(ctx, null),
        onToggle: (open) => {
          state.addOpen = open;
        },
      }));
    }
    return wrap;
  }

  function linkRow(ctx, link) {
    const url = location.origin + "/p/" + link.token;
    const sum = linkSummary(link);
    const urlField = formControl({
      label: "Public link", hideLabel: true, name: "url", value: url, readOnly: true, monospace: true, size: "small",
      trailingAction: { icon: "copy", label: "Copy link", onClick: (ev) => copyText(url, ev && ev.currentTarget) },
    });
    const meta = h("div", { class: "ms-link-meta" },
      h("ul", { class: "ms-labels", "aria-label": "Link details" },
        h("li", {}, uiLabel(sum.access, { variant: "secondary" })),
        sum.password ? h("li", {}, uiLabel("Password", { variant: "secondary" })) : null,
        sum.expired ? h("li", {}, uiLabel("Expired", { variant: "attention" })) : null),
      link.expiresAt && !sum.expired ? h("span", { text: sum.expiry }) : null,
      h("span", { text: sum.views }));
    const revoke = async (trigger) => {
      const ok = await confirmDialog({
        title: "Revoke public link?",
        body: "People who have the link can no longer open it. This cannot be undone.",
        confirmLabel: "Revoke link",
        returnFocus: trigger,
        onConfirm: () => api("DELETE", `/api/links/${encodeURIComponent(link.id)}`),
      });
      if (!ok) return;
      announce("Link revoked");
      ctx.reload();
    };
    const menu = rowMenu("Actions for this public link", () => [
      { label: "Copy link", icon: "copy", onClick: () => copyText(url) },
      { label: "Edit link", icon: "pencil", onClick: () => { state.editing = link.id; draw(ctx); focusLinkForm(ctx); } },
      { separator: true },
      { label: "Revoke link", icon: "trash", danger: true, onClick: () => revoke(menu) },
    ]);
    return h("li", { class: "list-row no-lead ms-link" + (sum.expired ? " is-expired" : ""), "data-link-id": link.id },
      h("div", { class: "list-row-main" }, urlField, meta),
      h("span", { class: "list-row-trail" }, menu));
  }

  function focusLinkForm(ctx) {
    const select = ctx.body.querySelector(".ms-link-form select");
    if (select) select.focus();
  }

  /** Builds the create or edit form of a public link. */
  function linkForm(ctx, link) {
    const today = dateInputValue(new Date().toISOString());
    const accessField = formControl({
      label: "Access", name: "access", type: "select", value: link ? link.access : "view",
      options: accessChoices("link").map((a) => ({ value: a, label: accessLabel(a) })),
    });
    const passwordField = formControl({
      label: link && link.hasPassword ? "New password" : "Password", name: "password", type: "password", autocomplete: "new-password",
      caption: link && link.hasPassword ? "Leave empty to keep the current password." : "Optional. People need the password to open the link.",
    });
    const removeField = link && link.hasPassword ? formControl({ label: "Remove the password", name: "removePassword", type: "checkbox" }) : null;
    const expiryField = formControl({ label: "Expires", name: "expiresAt", type: "date", value: link && link.expiresAt ? dateInputValue(link.expiresAt) : "", min: today, caption: "Leave empty for no expiry." });
    const childrenField = childrenLabel
      ? formControl({ label: childrenLabel, name: "includeChildren", type: "checkbox", value: link ? !!link.includeChildren : noun === "folder" })
      : null;
    const close = () => {
      state.addOpen = false;
      state.editing = null;
      draw(ctx);
    };
    const { form, submit } = formBlock({
      title: link ? "Edit link" : "New public link",
      className: "ms-link-form",
      fields: [accessField, passwordField, removeField, expiryField, childrenField].filter(Boolean),
      submitLabel: link ? "Save link" : "Create link",
      onCancel: close,
    });
    form.addEventListener("submit", async (ev) => {
      ev.preventDefault();
      const body = {
        noteId: subject.id,
        access: accessField.querySelector("select").value,
        includeChildren: childrenField ? childrenField.querySelector("input").checked : false,
      };
      const password = passwordField.querySelector("input").value;
      if (removeField && removeField.querySelector("input").checked) body.password = "";
      else if (password) body.password = password;
      const expiry = expiryField.querySelector("input").value;
      if (expiry) body.expiresAt = new Date(expiry + "T23:59:59").toISOString();
      setBusy(submit, true);
      try {
        if (link) await api("PATCH", `/api/links/${encodeURIComponent(link.id)}`, body);
        else await api("POST", `${route}/links`, body);
      } catch (err) {
        setBusy(submit, false);
        if (!ctx.offlineError(err)) showFieldErrors(form, err);
        return;
      }
      state.addOpen = false;
      state.editing = null;
      announce(link ? "Link saved" : "Link created");
      ctx.reload();
    });
    return form;
  }

  return { unmount: panel.unmount, reload: () => panel.ctx.reload() };
}

/* ---------- History ---------- */

/**
 * Groups versions by the local calendar day of createdAt, newest first.
 * @param {{version: object, delta: number|null}[]} rows from versionDeltas
 * @returns {{day: string, rows: object[]}[]}
 */
export function versionDays(rows) {
  const out = [];
  for (const row of Array.isArray(rows) ? rows : []) {
    const d = new Date(row.version && row.version.createdAt);
    const day = Number.isFinite(d.getTime()) ? d.getFullYear() + "-" + (d.getMonth() + 1) + "-" + d.getDate() : "";
    const last = out[out.length - 1];
    if (last && last.day === day) last.rows.push(row);
    else out.push({ day, rows: [row] });
  }
  return out;
}

/** Formats the heading of a day group, such as "Sep 13, 2026". */
function dayHeading(iso) {
  const d = new Date(iso);
  if (!Number.isFinite(d.getTime())) return "Earlier";
  return d.toLocaleDateString("en-US", { month: "short", day: "numeric", year: "numeric" });
}

const DAY_MS = 86400e3;
const WEEKDAYS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];
const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** The number of timeline entries the rail History tab shows, the current version included. */
export const HISTORY_RAIL_ROWS = 5;

/** The number of versions the history Dialog adds to its list at a time. */
export const HISTORY_PAGE = 20;

function startOfDay(ms) {
  const d = new Date(ms);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
}

function timeOf(when) {
  const t = new Date(when).getTime();
  return Number.isFinite(t) ? t : NaN;
}

/**
 * Names the day of a version in the history timeline: "Today", "Yesterday",
 * the weekday for the rest of the last seven days, and the date for older
 * days. The date carries the year only when it is not the current year.
 * @param {string|number|Date} when
 * @param {number} [now] unix milliseconds
 * @returns {string}
 */
export function historyDayLabel(when, now = Date.now()) {
  const t = timeOf(when);
  if (!Number.isFinite(t)) return "Earlier";
  const days = Math.round((startOfDay(now) - startOfDay(t)) / DAY_MS);
  if (days <= 0) return "Today";
  if (days === 1) return "Yesterday";
  const d = new Date(t);
  if (days < 7) return WEEKDAYS[d.getDay()];
  const date = MONTHS[d.getMonth()] + " " + d.getDate();
  return d.getFullYear() === new Date(now).getFullYear() ? date : date + ", " + d.getFullYear();
}

/**
 * Groups timeline entries by local day and keeps their order, newest first.
 * @param {{createdAt: any}[]} entries
 * @param {number} [now] unix milliseconds
 * @returns {{key: string, label: string, entries: object[]}[]}
 */
export function groupVersionsByDay(entries, now = Date.now()) {
  const out = [];
  for (const entry of Array.isArray(entries) ? entries : []) {
    const t = timeOf(entry && entry.createdAt);
    const key = Number.isFinite(t) ? String(startOfDay(t)) : "unknown";
    const last = out[out.length - 1];
    if (last && last.key === key) last.entries.push(entry);
    else out.push({ key, label: historyDayLabel(t, now), entries: [entry] });
  }
  return out;
}

/**
 * Formats the local clock time of a version in 24-hour form, such as "14:32".
 * @param {string|number|Date} when
 * @returns {string}
 */
export function clockTime(when) {
  const t = timeOf(when);
  if (!Number.isFinite(t)) return "";
  const d = new Date(t);
  return String(d.getHours()).padStart(2, "0") + ":" + String(d.getMinutes()).padStart(2, "0");
}

/**
 * Formats the full local date and time of a version, such as
 * "Monday, Sep 14, 2026, 14:32".
 * @param {string|number|Date} when
 * @returns {string}
 */
export function versionWhen(when) {
  const t = timeOf(when);
  if (!Number.isFinite(t)) return "Unknown time";
  const d = new Date(t);
  return `${WEEKDAYS[d.getDay()]}, ${MONTHS[d.getMonth()]} ${d.getDate()}, ${d.getFullYear()}, ${clockTime(t)}`;
}

/**
 * Says how long ago a version was written, such as "3 hours ago".
 * @param {string|number|Date} when
 * @param {number} [now] unix milliseconds
 * @returns {string}
 */
export function timeSince(when, now = Date.now()) {
  const t = timeOf(when);
  if (!Number.isFinite(t)) return "";
  const s = Math.max(0, Math.round((now - t) / 1000));
  const ago = (n, unit) => `${n} ${unit}${n === 1 ? "" : "s"} ago`;
  if (s < 60) return "Just now";
  if (s < 3600) return ago(Math.floor(s / 60), "minute");
  if (s < DAY_MS / 1000) return ago(Math.floor(s / 3600), "hour");
  if (s < 30 * 86400) return ago(Math.floor(s / 86400), "day");
  if (s < 365 * 86400) return ago(Math.floor(s / (30 * 86400)), "month");
  return ago(Math.floor(s / (365 * 86400)), "year");
}

/**
 * Counts the added and removed lines between two texts. Lines match as a
 * multiset, so a moved line counts as unchanged. A version's change summary
 * needs no more than that.
 * @param {string} before
 * @param {string} after
 * @returns {{added: number, removed: number}}
 */
export function lineChanges(before, after) {
  const split = (text) => {
    const s = String(text || "").replace(/\r\n/g, "\n").replace(/\n$/, "");
    return s === "" ? [] : s.split("\n");
  };
  const counts = new Map();
  for (const line of split(before)) counts.set(line, (counts.get(line) || 0) + 1);
  let added = 0;
  for (const line of split(after)) {
    const n = counts.get(line) || 0;
    if (n > 0) counts.set(line, n - 1);
    else added++;
  }
  let removed = 0;
  for (const n of counts.values()) removed += n;
  return { added, removed };
}

/**
 * Formats a line change summary, such as "+12 −3 lines".
 * @param {{added?: number, removed?: number}} changes
 * @returns {string}
 */
export function formatLineChanges({ added = 0, removed = 0 } = {}) {
  if (!added && !removed) return "No line changes";
  return `+${added} −${removed} ${added + removed === 1 ? "line" : "lines"}`;
}

let historyIds = 0;
function uidFor(prefix) {
  historyIds += 1;
  return `${prefix}-${historyIds}`;
}

/**
 * Opens the History panel (docs/primer.md 5.10).
 *
 * For a note the panel is a short timeline: the current version and the
 * latest versions as points in time under day headings, and Button "Show
 * diff" with the version count. Each point and the Button open the version
 * history Dialog, which lists every version and shows the diff of the
 * selected one. For a file the panel lists the versions with Download and
 * Restore, since files have no text diff.
 *
 * kind is "note" (the default) or "file". route defaults to /api/notes/{id}
 * or /api/files/{id}. A file view passes {kind: "file", item}.
 * @param {HTMLElement} container
 * @param {object} opts
 * @returns {{unmount(): void, reload(): void, openDialog(trigger?: Element|null, versionId?: string|null): Promise<object|null>}}
 */
export function openHistory(container, opts) {
  const api = opts.api;
  const me = opts.user && opts.user.id;
  const kind = opts.kind === "file" ? "file" : "note";
  const subject = subjectOf(opts);
  const route = opts.route ? String(opts.route).replace(/\/+$/, "") : `/api/${kind === "file" ? "files" : "notes"}/${encodeURIComponent(subject.id)}`;
  const canEdit = () => accessRank(opts.access) >= ACCESS_RANK.edit && !subject.deleted;
  const diffs = new Map();
  const openDiffs = new Set();
  let names = null;
  let lastIds = [];

  let replace = null;
  if (kind === "file" && canEdit() && opts.sync && typeof opts.sync.replaceFile === "function") {
    replace = button({ label: "Replace", size: "small", leadingVisual: "upload", onClick: () => chooseReplacement() });
  }

  const panel = createPanel(container, opts, {
    title: "History",
    className: "ms-history",
    action: replace,
    offlineText: "You are offline. Version history needs a connection.",
  }, async (body, ctx, current) => {
    const [data, known] = await Promise.all([
      api("GET", `${route}/versions`, undefined, { signal: ctx.signal }),
      names ? Promise.resolve(names) : memberNames(api, subject.spaceId, ctx.signal),
    ]);
    if (!current()) return;
    names = known;
    const rows = versionDeltas(items(data));
    lastIds = rows.map((r) => r.version.id);
    if (typeof opts.onCount === "function") opts.onCount(rows.length);
    if (kind === "note") {
      if (!rows.length) {
        body.replaceChildren(emptyState({ icon: "history", title: "No earlier versions", text: "Earlier versions appear here as the note changes." }));
        return;
      }
      const entries = [currentEntry(subject), ...rows.map(versionEntry)].slice(0, HISTORY_RAIL_ROWS);
      const timeline = railTimeline(entries);
      const count = rows.length;
      const show = button({
        label: "Show diff",
        size: "small",
        leadingVisual: "history",
        count,
        attrs: { "aria-label": `Show diff, ${count === 1 ? "1 version" : count + " versions"}` },
        onClick: (event) => openVersionDialog(event.currentTarget),
      });
      body.replaceChildren(timeline, h("div", { class: "ms-history-open" }, show));
      return;
    }
    const fresh = kind === "file" && opts.store && typeof opts.store.getFile === "function" ? (await opts.store.getFile(subject.id).catch(() => null)) || subject : subject;
    if (!current()) return;
    const timeline = h("ol", { class: "timeline ms-versions", role: "list", "aria-label": "Versions" }, currentItem(fresh));
    versionDays(rows).forEach((group, i) => {
      if (i > 0) timeline.appendChild(h("li", { class: "timeline-break", "aria-hidden": "true" }));
      timeline.appendChild(h("li", { class: "ms-history-day" }, h("h3", { class: "ms-history-day-title", text: dayHeading(group.rows[0].version.createdAt) })));
      for (const row of group.rows) timeline.appendChild(versionItem(row, fresh, ctx));
    });
    const parts = [timeline];
    if (!rows.length) {
      parts.push(emptyState({ icon: "history", title: "No earlier versions", text: "Earlier versions appear here when the file is replaced." }));
    }
    body.replaceChildren(...parts);
  });

  /** Reports whether the panel is on screen, so a Banner there can be seen. */
  const panelVisible = () => !!(container && container.isConnected && container.getClientRects().length);

  function authorName(id) {
    if (!id) return "Someone";
    if (id === me) return "You";
    return (names && names.get(id)) || "Someone";
  }

  function authorAvatar(id) {
    if (id && id === me && opts.user) return avatarFor(opts.user, 16);
    return avatarFor({ name: authorName(id) }, 16);
  }

  function versionEntry(row) {
    const v = row.version;
    return { id: v.id, createdAt: v.createdAt, authorId: v.authorId || "", via: v.via || "", delta: row.delta, current: false, version: v };
  }

  function currentEntry(rec) {
    return { id: "current", createdAt: rec.updatedAt || rec.modTime || rec.createdAt, authorId: rec.updatedBy || rec.createdBy || "", via: rec.updatedVia || "", delta: null, current: true };
  }

  /**
   * Builds the parts of one point in time: the dot on the timeline line, the
   * clock time (with "Current" for the current text), the author with Avatar
   * and plugin, and the size change.
   */
  function pointParts(entry) {
    const t = timeOf(entry.createdAt);
    return [
      h("span", { class: "history-dot", "aria-hidden": "true" }),
      h("span", { class: "history-point-main" },
        h("span", { class: "history-point-head" },
          h("time", { class: "history-point-clock", datetime: Number.isFinite(t) ? new Date(t).toISOString() : null, text: clockTime(t) }),
          entry.current ? uiLabel("Current", { variant: "accent" }) : null),
        h("span", { class: "history-point-by" },
          authorAvatar(entry.authorId),
          h("span", { class: "history-point-author", text: authorName(entry.authorId) }),
          entry.via ? h("span", { class: "history-point-via", text: "via " + entry.via }) : null)),
      entry.current ? null : deltaEl(entry.delta),
    ];
  }

  /** Builds the rail timeline. Each point is a Button that opens the Dialog on that version. */
  function railTimeline(entries) {
    const root = h("div", { class: "history-timeline" });
    for (const group of groupVersionsByDay(entries)) {
      const headingId = uidFor("history-day");
      root.appendChild(h("section", { class: "history-day-group", "aria-labelledby": headingId },
        h("h3", { class: "history-day-label", id: headingId, text: group.label }),
        h("ol", { class: "history-points", role: "list" }, ...group.entries.map((entry) => {
          const point = h("button", {
            type: "button",
            class: "history-point" + (entry.current ? " is-current" : ""),
            "data-version-id": entry.id,
            onClick: (event) => openVersionDialog(event.currentTarget, entry.id),
          }, ...pointParts(entry));
          tooltip(point, timeSince(entry.createdAt), { type: "description" });
          return h("li", {}, point);
        }))));
    }
    return root;
  }

  /**
   * Opens the version history Dialog (docs/primer.md 5.10, Version history). It
   * loads the versions itself. The start side lists the current version and
   * every earlier version as points in time under day headings, adding
   * HISTORY_PAGE entries at a time as the list scrolls. Up, Down, Home and End
   * move the selection. The end side names the selected version and shows its
   * diff: the changes in this version against the version before it, or the
   * version against the current text. Download and Restore this version act
   * on the selection. Below 768px the list and the diff are two screens, and
   * Back returns to the list.
   * @param {Element|null} [trigger] focus returns here
   * @param {string|null} [startId] a version id or "current"
   * @returns {Promise<object|null>} the Dialog handle, or null with a Banner when nothing can be shown
   */
  async function openVersionDialog(trigger = null, startId = null) {
    if (kind !== "note") return null;
    let rows;
    let note = subject;
    try {
      const [data, known, fresh] = await Promise.all([
        api("GET", `${route}/versions`),
        names ? Promise.resolve(names) : memberNames(api, subject.spaceId),
        api("GET", route).catch(() => null),
      ]);
      names = known;
      rows = versionDeltas(items(data));
      if (fresh && typeof fresh === "object" && !Array.isArray(fresh)) note = { ...subject, ...fresh };
    } catch (err) {
      banner({ variant: "critical", title: "Version history could not be loaded", description: errorText(err), into: bannerHost() });
      return null;
    }
    if (!rows.length) {
      banner({ variant: "info", title: "No earlier versions", description: "Earlier versions appear here as the note changes.", dismissible: true, into: bannerHost() });
      return null;
    }
    lastIds = rows.map((r) => r.version.id);
    const entries = [currentEntry(note), ...rows.map(versionEntry)];
    const texts = new Map();
    if (typeof note.content === "string") texts.set("current", Promise.resolve(note.content));
    let selected = entries.find((e) => e.id === startId) || entries[0];
    let mode = "changes";
    let view = null;
    let token = 0;
    let rendered = 0;
    let lastGroup = null;
    let drawTimer = 0;
    const options = new Map();
    const narrowQuery = typeof window !== "undefined" && window.matchMedia ? window.matchMedia("(max-width: calc(48rem - 0.02px))") : null;
    const isNarrow = () => !!(narrowQuery && narrowQuery.matches);

    const listbox = h("div", { class: "history-listbox", role: "listbox", "aria-label": "Versions", onKeydown: onListKey });
    const more = h("div", { class: "history-more" }, button({ label: "Show older versions", size: "small", onClick: () => renderMore(true) }));
    const listPane = h("div", { class: "history-dialog-list" }, listbox, more);

    const back = button({ label: "Back", leadingVisual: "arrow-left", size: "small", className: "history-back", onClick: () => showScreen("list") });
    const title = h("h2", { class: "history-diff-title", id: uidFor("history-diff-title") });
    const meta = h("p", { class: "history-diff-meta" });
    const caption = h("p", { class: "history-diff-caption", id: uidFor("history-caption") });
    const seg = segmentedControl({
      label: "Diff",
      size: "small",
      items: [{ label: "Changes in this version", value: "changes", selected: true }, { label: "Compare with current", value: "current" }],
      onChange: (value) => {
        mode = value;
        draw();
      },
    });
    const diffHost = h("div", { class: "history-diff", tabindex: "0", role: "region", "aria-labelledby": caption.id });
    const diffPane = h("section", { class: "history-dialog-diff", "aria-labelledby": title.id },
      h("div", { class: "history-diff-head" }, back, h("div", { class: "history-diff-heading" }, title, meta), seg, caption),
      diffHost);
    const layout = h("div", { class: "history-dialog-layout", "data-screen": isNarrow() ? "list" : "both" }, listPane, diffPane);

    const footer = [{ label: "Download", onClick: async () => { await downloadEntry(selected); return false; } }];
    if (canEdit()) {
      footer.push({
        label: "Restore this version",
        variant: "primary",
        onClick: () => {
          if (selected.current) return false;
          const v = selected.version;
          // The Dialog closes first, so the Undo Banner is not under it.
          setTimeout(() => restore(v, null, panel.ctx), 0);
          return undefined;
        },
      });
    }

    const destroyView = () => {
      if (view) {
        view.destroy();
        view = null;
      }
    };
    const onRange = () => {
      layout.dataset.screen = isNarrow() ? "list" : "both";
      draw();
    };
    let observer = null;
    const dialog = openDialog({
      title: "Version history",
      subtitle: note.title || subject.title || "",
      narrow: "fullscreen",
      className: "history-dialog",
      content: layout,
      footer,
      returnFocus: trigger,
      onClose: () => {
        token++;
        clearTimeout(drawTimer);
        destroyView();
        if (observer) observer.disconnect();
        if (narrowQuery) narrowQuery.removeEventListener("change", onRange);
      },
    });
    if (narrowQuery) narrowQuery.addEventListener("change", onRange);

    function renderMore(focusNew = false) {
      // Focus on Show older versions moves to the first new version, since the Button hides after the last page.
      const moreHadFocus = more.contains(document.activeElement);
      const next = entries.slice(rendered, rendered + HISTORY_PAGE);
      let first = null;
      for (const entry of next) {
        const t = timeOf(entry.createdAt);
        const key = Number.isFinite(t) ? String(startOfDay(t)) : "unknown";
        if (!lastGroup || lastGroup.key !== key) {
          const headingId = uidFor("history-day");
          const list = h("ul", { class: "history-points", role: "none" });
          listbox.appendChild(h("div", { class: "history-day-group", role: "group", "aria-labelledby": headingId },
            h("div", { class: "history-day-label", id: headingId, text: historyDayLabel(t) }),
            list));
          lastGroup = { key, list };
        }
        const option = h("li", {
          class: "history-point" + (entry.current ? " is-current" : ""),
          role: "option",
          id: uidFor("history-option"),
          tabindex: "-1",
          "aria-selected": "false",
          "data-version-id": entry.id,
          onClick: () => choose(entry, { open: true, focus: true }),
        }, ...pointParts(entry));
        tooltip(option, timeSince(entry.createdAt), { type: "description" });
        options.set(entry.id, option);
        lastGroup.list.appendChild(option);
        if (!first) first = option;
      }
      rendered += next.length;
      more.hidden = rendered >= entries.length;
      mark();
      if ((focusNew || (moreHadFocus && more.hidden)) && first) first.focus();
    }

    function mark() {
      for (const [id, el] of options) {
        const on = id === selected.id;
        el.setAttribute("aria-selected", on ? "true" : "false");
        el.tabIndex = on ? 0 : -1;
      }
      const restoreButton = dialog && dialog.buttons ? dialog.buttons[1] : null;
      if (restoreButton) restoreButton.setAttribute("aria-disabled", selected.current ? "true" : "false");
    }

    function choose(entry, { open = false, focus = false } = {}) {
      selected = entry;
      while (!options.has(entry.id) && rendered < entries.length) renderMore();
      mark();
      const el = options.get(entry.id);
      if (el) {
        if (focus) el.focus();
        el.scrollIntoView({ block: "nearest" });
      }
      if (open && isNarrow()) showScreen("diff");
      clearTimeout(drawTimer);
      drawTimer = setTimeout(draw, 120);
    }

    function onListKey(event) {
      const index = entries.indexOf(selected);
      let next = -1;
      if (event.key === "ArrowDown") next = Math.min(entries.length - 1, index + 1);
      else if (event.key === "ArrowUp") next = Math.max(0, index - 1);
      else if (event.key === "Home") next = 0;
      else if (event.key === "End") next = entries.length - 1;
      else if ((event.key === "Enter" || event.key === " ") && isNarrow()) {
        event.preventDefault();
        showScreen("diff");
        return;
      }
      if (next < 0) return;
      event.preventDefault();
      choose(entries[next], { focus: true });
    }

    function showScreen(screen) {
      layout.dataset.screen = isNarrow() ? screen : "both";
      if (!isNarrow()) return;
      if (screen === "diff") {
        back.focus();
      } else {
        const el = options.get(selected.id);
        if (el) el.focus();
      }
    }

    function textOf(key, url) {
      if (!texts.has(key)) {
        const load = api("GET", url).then((data) => String((data && data.content) || ""));
        load.catch(() => texts.delete(key));
        texts.set(key, load);
      }
      return texts.get(key);
    }
    const textFor = (entry) => (entry.current ? textOf("current", route) : textOf(entry.id, `${route}/versions/${encodeURIComponent(entry.id)}`));

    async function draw() {
      clearTimeout(drawTimer);
      const mine = ++token;
      const index = entries.indexOf(selected);
      const older = entries[index + 1] || null;
      const toCurrent = mode === "current";
      title.textContent = (selected.current ? "Current version, " : "") + versionWhen(selected.createdAt);
      const summary = h("span", { class: "history-diff-summary" });
      // replaceChildren turns null into text, so empty parts are left out.
      meta.replaceChildren(...[authorAvatar(selected.authorId), h("span", { text: authorName(selected.authorId) }),
        selected.via ? h("span", { text: "via " + selected.via }) : null, summary].filter(Boolean));
      caption.textContent = toCurrent
        ? selected.current ? "This is the current version" : "Changes from this version to the current version"
        : older ? "Changes made in this version, compared with the version before it" : "The first version, compared with an empty note";
      destroyView();
      diffHost.replaceChildren(skeleton({ lines: 4, label: "Loading changes" }));
      try {
        const [cm, before, after] = await Promise.all([
          import("/static/vendor/codemirror.js"),
          toCurrent ? textFor(selected) : older ? textFor(older) : Promise.resolve(""),
          toCurrent ? textFor(entries[0]) : textFor(selected),
        ]);
        if (mine !== token || !dialog.element.isConnected) return;
        summary.textContent = formatLineChanges(lineChanges(before, after));
        diffHost.replaceChildren();
        if (before === after) {
          const same = toCurrent ? (selected.current ? "This is the current version." : "This version matches the current version.") : "This version has no text changes.";
          diffHost.appendChild(h("p", { class: "ms-diff-same", text: same }));
          return;
        }
        const readOnly = [cm.EditorView.editable.of(false), cm.EditorState.readOnly.of(true), cm.EditorView.lineWrapping];
        const collapseUnchanged = { margin: 3, minSize: 4 };
        if (isNarrow()) {
          view = new cm.EditorView({
            doc: after,
            parent: diffHost,
            extensions: [...readOnly, cm.unifiedMergeView({ original: before, mergeControls: false, highlightChanges: true, gutter: true, syntaxHighlightDeletions: false, collapseUnchanged })],
          });
        } else {
          view = new cm.MergeView({ a: { doc: before, extensions: readOnly }, b: { doc: after, extensions: readOnly }, parent: diffHost, highlightChanges: true, gutter: true, collapseUnchanged });
        }
      } catch (err) {
        if (mine !== token) return;
        diffHost.replaceChildren(inlineError(errorText(err), () => draw()));
      }
    }

    async function downloadEntry(entry) {
      const text = await textFor(entry);
      const t = timeOf(entry.createdAt);
      const stamp = Number.isFinite(t) ? new Date(t).toISOString().slice(0, 16).replace(/[T:]/g, "-") : "version";
      const base = String(note.title || subject.title || "note").replace(/[\\/:*?"<>|]+/g, "-").trim() || "note";
      const url = URL.createObjectURL(new Blob([text], { type: "text/markdown" }));
      const link = h("a", { href: url, download: `${base} ${stamp}.md`, hidden: true, "data-full-nav": "" });
      document.body.appendChild(link);
      link.click();
      link.remove();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
      announce("Version downloaded");
    }

    renderMore();
    while (!options.has(selected.id) && rendered < entries.length) renderMore();
    mark();
    if (typeof IntersectionObserver === "function") {
      observer = new IntersectionObserver((seen) => {
        if (seen.some((e) => e.isIntersecting) && rendered < entries.length) renderMore();
      }, { root: listPane, rootMargin: "240px" });
      observer.observe(more);
    }
    const start = options.get(selected.id);
    if (start) {
      start.focus();
      start.scrollIntoView({ block: "nearest" });
    }
    if (startId && isNarrow()) showScreen("diff");
    draw();
    return dialog;
  }

  function deltaEl(delta) {
    if (delta === null || delta === undefined || !Number.isFinite(delta)) return null;
    const sign = delta > 0 ? "up" : delta < 0 ? "down" : "none";
    const spoken = delta > 0 ? formatBytes(delta) + " added" : delta < 0 ? formatBytes(-delta) + " removed" : "No size change";
    return h("span", { class: "ms-delta", "data-sign": sign },
      h("span", { "aria-hidden": "true", text: formatDelta(delta) }),
      h("span", { class: "sr-only", text: spoken }));
  }

  function currentItem(rec) {
    const by = rec.updatedBy ? authorName(rec.updatedBy) : "";
    return h("li", { class: "timeline-item ms-version is-current" },
      h("span", { class: "timeline-badge", "data-variant": "accent" }, icon("history", 16)),
      h("div", { class: "timeline-body" },
        h("div", { class: "ms-version-title", text: "Current version" }),
        h("div", { class: "ms-version-meta" },
          h("span", {}, "Updated ", timeEl(rec.updatedAt || rec.modTime, true), by ? " by " + (by === "You" ? "you" : by) : ""),
          kind === "file" && Number.isFinite(rec.size) ? h("span", { text: formatBytes(rec.size) }) : null)));
  }

  function versionItem(row, rec, ctx) {
    const v = row.version;
    const who = authorName(v.authorId);
    const title = h("div", { class: "ms-version-title" }, who, v.via ? h("span", { class: "ms-version-via", text: " via " + v.via }) : null);
    const meta = h("div", { class: "ms-version-meta" },
      timeEl(v.createdAt, true),
      deltaEl(row.delta),
      kind === "file" && Number.isFinite(v.size) ? h("span", { text: formatBytes(v.size) }) : null,
      v.label ? h("span", { text: sentence(v.label).replace(/\.$/, "") }) : null);
    const bodyParts = [title, meta];
    if (kind === "note") {
      const box = h("div", { class: "ms-diff-box" });
      bodyParts.push(details({
        summary: "Show changes",
        open: openDiffs.has(v.id),
        content: box,
        onToggle: (open) => {
          if (open) {
            openDiffs.add(v.id);
            showDiff(v, box, ctx);
          } else {
            openDiffs.delete(v.id);
          }
        },
      }));
      if (openDiffs.has(v.id)) showDiff(v, box, ctx);
    }
    const actions = [];
    if (kind === "file" && rec.url) {
      actions.push(button({
        label: "Download", size: "small", leadingVisual: "download", href: `${rec.url}?v=${encodeURIComponent(v.id)}&download=1`,
        attrs: { download: v.name || rec.name || "", "data-full-nav": "" },
      }));
    }
    if (canEdit()) {
      const restoreBtn = button({ label: "Restore this version", size: "small" });
      restoreBtn.addEventListener("click", () => restore(v, restoreBtn, ctx));
      actions.push(restoreBtn);
    }
    if (actions.length) bodyParts.push(h("div", { class: "ms-version-actions" }, ...actions));
    return h("li", { class: "timeline-item ms-version", "data-version-id": v.id },
      h("span", { class: "timeline-badge" }, icon("history", 16)),
      h("div", { class: "timeline-body" }, ...bodyParts));
  }

  async function showDiff(v, box, ctx) {
    if (diffs.has(v.id)) {
      drawDiff(box, diffs.get(v.id));
      return;
    }
    box.replaceChildren(skeleton({ lines: 3, label: "Loading changes" }));
    try {
      const data = await api("GET", `${route}/versions/${encodeURIComponent(v.id)}/diff`, undefined, { signal: ctx.signal });
      const html = data && typeof data.diff === "string" ? data.diff : "";
      diffs.set(v.id, html);
      if (box.isConnected) drawDiff(box, html);
    } catch (err) {
      if (ctx.signal.aborted || ctx.offlineError(err)) return;
      box.replaceChildren(inlineError(errorText(err), () => showDiff(v, box, ctx)));
    }
  }

  function drawDiff(box, html) {
    const diff = h("div", { class: "ms-diff", tabindex: "0", role: "region", "aria-label": "Changes from this version to the current note" });
    // The diff endpoint returns HTML that the server already escaped.
    diff.innerHTML = html;
    markChangedWords(diff);
    const changed = diff.querySelector(".diff-add, .diff-del");
    box.replaceChildren(changed ? diff : h("p", { class: "ms-diff-same", text: "This version matches the current note." }));
  }

  async function postRestore(id) {
    await api("POST", `${route}/versions/${encodeURIComponent(id)}/restore`);
    diffs.clear();
    if (opts.sync && typeof opts.sync.syncNow === "function") Promise.resolve(opts.sync.syncNow()).catch(() => {});
  }

  /** Restores a version at once and offers Undo in a Banner, since the list reloads. */
  async function restore(v, btn, ctx) {
    const before = new Set(lastIds);
    if (btn) setBusy(btn, true);
    const result = await undoable({
      label: "Version restored",
      // A hidden panel cannot show its Banner, so the page shows it.
      into: panelVisible() ? panel.shell.messages : null,
      run: async () => {
        await postRestore(v.id);
        openDiffs.clear();
        if (typeof opts.onRestored === "function") opts.onRestored(v);
        ctx.reload();
      },
      undo: () => undoRestore(before, ctx),
    });
    if (result === "failed" && btn && btn.isConnected) setBusy(btn, false);
  }

  /** Restores the snapshot the server saved just before a restore. Throws when it cannot. */
  async function undoRestore(before, ctx) {
    const data = await api("GET", `${route}/versions`);
    const fresh = versionDeltas(items(data)).map((r) => r.version).find((x) => !before.has(x.id));
    if (!fresh) throw new Error("The version from before the restore was not found.");
    await postRestore(fresh.id);
    ctx.reload();
  }

  function chooseReplacement() {
    const input = h("input", { type: "file", hidden: true });
    input.addEventListener("change", async () => {
      const f = input.files && input.files[0];
      input.remove();
      if (!f) return;
      try {
        await opts.sync.replaceFile(subject.id, f);
      } catch (err) {
        panel.ctx.fail(err);
        return;
      }
      announce("Replacing " + (subject.name || "the file"));
    });
    document.body.appendChild(input);
    input.click();
  }

  return {
    unmount: panel.unmount,
    reload: () => panel.ctx.reload(),
    /** Opens the version history Dialog, on versionId when given. */
    openDialog: (trigger = null, versionId = null) => openVersionDialog(trigger, versionId),
  };
}

/* ---------- AI ---------- */

/**
 * Opens the AI tab. The tab body lives in ai/aitab.js, which builds its frame
 * with panelShell.
 * @param {HTMLElement} container
 * @param {object} opts
 * @returns {{unmount(): void}}
 */
export function openAI(container, opts) {
  return openAITab(container, opts);
}

/* ---------- Suggestions ---------- */

const SUGGESTION_GROUPS = [
  { kind: "user", title: "People" },
  { kind: "plugin", title: "Plugins" },
  { kind: "ai", title: "AI" },
];

/** The event types that change the open suggestions of a note. */
export const SUGGESTION_EVENTS = ["suggestion.created", "suggestion.accepted", "suggestion.discarded"];

/**
 * Lists the ids of the open edits of a suggestion.
 * @param {{edits?: {id: string, status?: string}[]}} suggestion
 * @returns {string[]}
 */
export function openEditIds(suggestion) {
  return ((suggestion && suggestion.edits) || []).filter((e) => e && (!e.status || e.status === "open")).map((e) => e.id);
}

/**
 * Groups open suggestions by author kind: people, plugins, then AI. Each
 * group is ordered oldest first. Suggestions that are no longer open or have
 * no open edit are left out. An unknown author kind counts as a person.
 * @param {object[]} list
 * @returns {{kind: string, title: string, items: object[]}[]}
 */
export function groupSuggestions(list) {
  const open = (Array.isArray(list) ? list : []).filter((s) => s && (!s.status || s.status === "open" || s.status === "partial") && openEditIds(s).length);
  const kindOf = (s) => (s.authorKind === "plugin" || s.authorKind === "ai" ? s.authorKind : "user");
  const byTime = (a, b) => String(a.createdAt || "").localeCompare(String(b.createdAt || ""));
  return SUGGESTION_GROUPS
    .map((g) => ({ kind: g.kind, title: g.title, items: open.filter((s) => kindOf(s) === g.kind).sort(byTime) }))
    .filter((g) => g.items.length);
}

/**
 * Reports whether a server event changes the suggestions of a note. An event
 * that names no note counts, because a suggestion event carries the id of
 * the suggestion only.
 * @param {{type?: string, noteId?: string, itemId?: string}} event
 * @param {string} noteId
 * @returns {boolean}
 */
export function suggestionEventMatches(event, noteId) {
  if (!event || !SUGGESTION_EVENTS.includes(event.type)) return false;
  const id = event.noteId || event.itemId || "";
  return !id || id === noteId;
}

/**
 * Subscribes to server events. With events ({on(type, fn) => unsubscribe}),
 * the shell's stream is used. Without it, and when the browser has
 * EventSource, the panel opens GET /api/events itself.
 * @returns {() => void} unsubscribe
 */
function subscribeEvents(events, types, handler) {
  if (events && typeof events.on === "function") {
    const offs = types.map((type) => events.on(type, handler));
    return () => offs.forEach((off) => typeof off === "function" && off());
  }
  if (typeof EventSource !== "function") return () => {};
  const stream = new EventSource("/api/events");
  for (const type of types) {
    stream.addEventListener(type, (message) => {
      try {
        handler(JSON.parse(message.data));
      } catch {
        /* A frame that is not JSON carries nothing for this panel. */
      }
    });
  }
  return () => stream.close();
}

/**
 * Opens the Suggestions tab of a note. It lists the open suggestions from
 * GET /api/notes/{id}/suggestions?status=open, grouped by author: people,
 * plugins and AI. Each suggestion shows its title, author, edit count and
 * rationale, with Accept all and Discard for people who can edit. Discard
 * asks in a ConfirmationDialog, because a discarded suggestion cannot come
 * back.
 *
 * The editor draws stored suggestions as tracked changes. The panel calls
 * setSuggestions(list) whenever the list loads or changes, including after a
 * suggestion.* event, and revealSuggestion(id) when a suggestion is chosen.
 * Both come from opts.setSuggestions and opts.revealSuggestion, or from the
 * editor handle that opts.editor returns.
 *
 * Extra options: {editor, setSuggestions, revealSuggestion, events, onCount}.
 * @param {HTMLElement} container
 * @param {object} opts
 * @returns {{unmount(): void, reload(): Promise<void>, publish(): void}}
 */
export function openSuggestions(container, opts) {
  const api = opts.api;
  const me = opts.user && opts.user.id;
  const note = () => opts.note;
  const canEdit = () => accessRank(opts.access) >= ACCESS_RANK.edit && !(note() && note().deleted);
  let list = [];
  let names = null;
  let selected = null;
  let reloadTimer = 0;
  let retryTimer = 0;

  const editorHandle = () => (typeof opts.editor === "function" ? opts.editor() : opts.editor) || null;
  const editorFn = (name) => {
    if (typeof opts[name] === "function") return opts[name];
    const handle = editorHandle();
    return handle && typeof handle[name] === "function" ? handle[name].bind(handle) : null;
  };

  const panel = createPanel(container, opts, {
    title: "Suggestions",
    className: "ms-suggestions",
    offlineText: "You are offline. Suggestions need a connection.",
  }, async (body, ctx, current) => {
    const [data, known] = await Promise.all([
      api("GET", `/api/notes/${encodeURIComponent(note().id)}/suggestions?status=open`, undefined, { signal: ctx.signal }),
      names ? Promise.resolve(names) : memberNames(api, note().spaceId, ctx.signal),
    ]);
    if (!current()) return;
    names = known;
    list = groupSuggestions(items(data)).flatMap((g) => g.items);
    publish();
    draw(ctx);
  });

  /** Returns the full name of an author for its Avatar, where "You" would give the wrong initials. */
  function avatarName(s) {
    if (s.authorId && s.authorId === me && opts.user) return nameOf(opts.user, "You");
    return authorName(s);
  }

  function authorName(s) {
    if (s.authorKind === "ai") return "AI";
    if (s.authorKind === "plugin") return s.authorName || s.authorId || "Plugin";
    if (s.authorId && s.authorId === me) return "You";
    return s.authorName || (names && names.get(s.authorId)) || "Someone";
  }

  /** Reports the count to the tab and hands the list to the editor. */
  function publish() {
    if (typeof opts.onCount === "function") opts.onCount(list.length);
    clearTimeout(retryTimer);
    const set = editorFn("setSuggestions");
    if (set) {
      try {
        set(list, { authorName });
      } catch (err) {
        console.error("[markspace] the editor could not draw the suggestions", err);
      }
    } else if (opts.editor) {
      // The editor may mount after the panel. Try once more shortly after.
      retryTimer = setTimeout(() => {
        const later = editorFn("setSuggestions");
        if (later) later(list, { authorName });
      }, 600);
    }
  }

  function draw(ctx) {
    if (!list.length) {
      ctx.body.replaceChildren(emptyState({ icon: "light-bulb", title: "No open suggestions", text: "Suggested edits from people, plugins and AI appear here." }));
      return;
    }
    ctx.body.replaceChildren(...groupSuggestions(list).map((g) => listSection({
      title: g.title,
      count: g.items.length,
      className: "ms-suggestion-group",
      rows: g.items.map((s) => suggestionRow(ctx, s)),
    }).root));
  }

  function suggestionRow(ctx, s) {
    const n = openEditIds(s).length;
    const who = authorName(s);
    const titleText = s.title || "Suggestion";
    const reveal = editorFn("revealSuggestion");
    const title = reveal
      ? h("button", { type: "button", class: "ms-suggestion-title", "aria-current": selected === s.id ? "true" : null, onClick: () => select(s, ctx) }, titleText)
      : h("span", { class: "ms-suggestion-title", text: titleText });
    const lead = s.authorKind === "ai"
      ? h("span", { class: "list-row-glyph" }, icon("sparkle", 16))
      : avatarFor({ name: avatarName(s), avatarColor: s.authorId === me && opts.user ? opts.user.avatarColor : "" }, 32, s.authorKind === "plugin");
    const meta = h("span", { class: "ms-suggestion-meta" }, `${who} · ${n === 1 ? "1 edit" : n + " edits"} · `, timeEl(s.createdAt));
    let detail = null;
    if (canEdit()) {
      const accept = button({ label: "Accept all", variant: "primary", size: "small", attrs: { "aria-label": `Accept all, ${titleText}` } });
      accept.addEventListener("click", () => acceptSuggestion(s, accept, ctx));
      const discard = button({ label: "Discard", size: "small", attrs: { "aria-label": `Discard, ${titleText}` } });
      discard.addEventListener("click", () => discardSuggestion(s, discard, ctx));
      detail = h("div", { class: "ms-suggestion-actions" }, accept, discard);
    }
    return listRow({
      lead,
      title,
      sub: [meta, s.rationale ? h("span", { class: "ms-suggestion-rationale", text: s.rationale }) : null],
      wrapSub: false,
      detail,
      className: "ms-suggestion" + (selected === s.id ? " is-current" : ""),
      attrs: { "data-suggestion-id": s.id },
    });
  }

  function select(s, ctx) {
    selected = s.id;
    for (const row of ctx.body.querySelectorAll(".ms-suggestion")) {
      const on = row.getAttribute("data-suggestion-id") === s.id;
      row.classList.toggle("is-current", on);
      const btn = row.querySelector("button.ms-suggestion-title");
      if (btn) {
        if (on) btn.setAttribute("aria-current", "true");
        else btn.removeAttribute("aria-current");
      }
    }
    const reveal = editorFn("revealSuggestion");
    if (reveal) reveal(s.id);
  }

  /** Removes a suggestion from the list, then redraws and republishes. */
  function drop(id, ctx) {
    list = list.filter((x) => x.id !== id);
    if (selected === id) selected = null;
    publish();
    draw(ctx);
  }

  async function acceptSuggestion(s, btn, ctx) {
    setBusy(btn, true);
    try {
      await api("POST", `/api/suggestions/${encodeURIComponent(s.id)}/accept`, { editIds: openEditIds(s) });
    } catch (err) {
      setBusy(btn, false);
      ctx.fail(err);
      return;
    }
    announce("Accepted " + (s.title || "the suggestion"));
    if (opts.sync && typeof opts.sync.syncNow === "function") Promise.resolve(opts.sync.syncNow()).catch(() => {});
    drop(s.id, ctx);
  }

  async function discardSuggestion(s, btn, ctx) {
    const ok = await confirmDialog({
      title: "Discard suggestion?",
      body: `"${s.title || "Suggestion"}" is discarded for everyone. This cannot be undone.`,
      confirmLabel: "Discard",
      returnFocus: btn,
      onConfirm: () => api("POST", `/api/suggestions/${encodeURIComponent(s.id)}/discard`, { editIds: openEditIds(s) }),
    });
    if (!ok) return;
    announce("Discarded " + (s.title || "the suggestion"));
    drop(s.id, ctx);
  }

  const unsubscribe = subscribeEvents(opts.events, SUGGESTION_EVENTS, (event) => {
    if (!suggestionEventMatches(event, note().id)) return;
    clearTimeout(reloadTimer);
    reloadTimer = setTimeout(() => panel.ctx.reload(), 150);
  });

  return {
    reload: () => panel.ctx.reload(),
    publish,
    unmount() {
      clearTimeout(reloadTimer);
      clearTimeout(retryTimer);
      unsubscribe();
      panel.unmount();
    },
  };
}

/* ---------- Plugin panels ---------- */

/**
 * Returns the module id of a qualified panel id, such as "bookmark-wiki" for
 * "bookmark-wiki.wiki".
 * @param {string} panelId
 * @returns {string}
 */
export function pluginModuleOf(panelId) {
  const s = String(panelId || "");
  const i = s.indexOf(".");
  return i > 0 ? s.slice(0, i) : "";
}

const TOAST_VARIANTS = { success: "success", warn: "warning", error: "critical" };

/**
 * Opens a plugin rail panel. It posts POST /api/panels/{id} with the context,
 * renders the returned tree with renderPluginUI from plugins.js, and posts
 * each action to POST /api/modules/{module}/actions/{id}. A command response
 * is handled by its one field: toast shows a Banner, openPanel calls
 * opts.openPanel or opens the panel in place, navigate calls opts.navigate,
 * job shows a Banner with a link to the job list, and ui updates the tree.
 * When plugins.js cannot load, the panel shows a Blankslate.
 *
 * Extra options: {panelId, itemId, kind, spaceId, title, openPanel,
 * loadRenderer}. loadRenderer replaces the dynamic import in tests.
 * @param {HTMLElement} container
 * @param {object} opts
 * @returns {{unmount(): void, reload(): Promise<void>}}
 */
export function openPluginPanel(container, opts) {
  const api = opts.api;
  let panelId = opts.panelId;
  const itemId = opts.itemId || (opts.item && opts.item.id) || (opts.note && opts.note.id) || "";
  const context = {};
  if (itemId) context.itemId = itemId;
  if (opts.kind) context.kind = opts.kind;
  if (!itemId && opts.spaceId) context.spaceId = opts.spaceId;
  const loadRenderer = typeof opts.loadRenderer === "function" ? opts.loadRenderer : () => import("/static/js/plugins.js");
  let view = null;

  const destroyView = () => {
    if (view && typeof view.destroy === "function") {
      try {
        view.destroy();
      } catch (err) {
        console.error("[markspace] plugin panel cleanup failed", err);
      }
    }
    view = null;
  };

  const panel = createPanel(container, opts, {
    title: opts.title || "Panel",
    className: "ms-plugin-panel",
    offlineText: "You are offline. This panel needs a connection.",
  }, async (body, ctx, current) => {
    let render = null;
    try {
      const mod = await loadRenderer();
      render = mod && typeof mod.renderPluginUI === "function" ? mod.renderPluginUI : null;
    } catch (err) {
      console.error("[markspace] plugins.js could not load", err);
    }
    if (!current()) return;
    if (!render) {
      destroyView();
      body.replaceChildren(emptyState({ icon: "alert", title: "This panel could not load", text: "The plugin interface is not available. Reload the page to try again." }));
      return;
    }
    const data = await api("POST", `/api/panels/${encodeURIComponent(panelId)}`, { context }, { signal: ctx.signal });
    if (!current()) return;
    destroyView();
    const host = h("div", { class: "ms-plugin-body" });
    body.replaceChildren(host);
    view = render(host, data && data.ui, {
      context,
      onAction: (actionId, payload, values) => runAction(ctx, actionId, payload, values),
    });
  });

  async function runAction(ctx, actionId, payload, values) {
    const module = pluginModuleOf(panelId);
    let response;
    try {
      response = await api("POST", `/api/modules/${encodeURIComponent(module)}/actions/${encodeURIComponent(actionId)}`, {
        context, values: values || {}, payload: payload || {},
      });
    } catch (err) {
      ctx.fail(err);
      throw err;
    }
    handleResponse(ctx, response || {});
    return response;
  }

  function handleResponse(ctx, response) {
    // A newer response replaces the message of an older one.
    if (response.toast || response.job) ctx.shell.messages.replaceChildren();
    if (response.toast) {
      // renderPluginUI shows the toast as a Banner in the plugin view, so the panel adds none.
    } else if (response.openPanel) {
      const target = response.openPanel;
      if (typeof opts.openPanel === "function") {
        opts.openPanel(target.id, { itemId: target.itemId || itemId });
      } else if (!target.itemId || target.itemId === itemId) {
        panelId = target.id;
        ctx.reload();
      }
    } else if (response.navigate) {
      if (typeof opts.navigate === "function") opts.navigate(response.navigate);
      else window.location.assign(response.navigate);
    } else if (response.job) {
      ctx.banner({
        variant: "info",
        title: "The work continues in the background",
        description: "You can leave this page. The job list shows its progress.",
        dismissible: true,
        actions: [{ label: "View progress", href: "/jobs" }],
      });
    } else if (response.ui && view && typeof view.update === "function") {
      view.update(response.ui);
    }
  }

  return {
    reload: () => panel.ctx.reload(),
    unmount() {
      destroyView();
      panel.unmount();
    },
  };
}
