// Folder view, file view, the /i/{id} permalink and the flat Files page. The
// views render from the local store first. File content comes from the
// network or the file cache, and a viewer module loads on demand. Headers
// are Primer PageHeaders, listings are DataTables or file card grids, and
// empty or failed states are Blankslates (docs/primer.md 5.2, 5.9 and 5.10).

import {
  h,
  icon,
  button,
  iconButton,
  openMenu,
  segmentedControl,
  blankslate,
  banner,
  announce,
  anchoredOverlay,
  spinner,
  formatBytes,
  isNarrow,
  editInPlace,
  uploadList,
  relativeTime as relativeTimeElement,
} from "./ui.js";
import { resolveWith, isFolderNote, dirOf } from "./store.js";
import {
  accessAtLeast,
  chooseFiles,
  downloadFile,
  itemIcon,
  itemLabel,
  itemMenuItems,
  newFolderIn,
  trashItemsWithUndo,
  uploadInto,
} from "./filetree.js";

/* ---------- Pure helpers ---------- */

/** File type filters of the Files page, as [value, label]. */
export const FILE_TYPES = [
  ["all", "All types"],
  ["image", "Images"],
  ["pdf", "PDFs"],
  ["document", "Documents"],
  ["video", "Video"],
  ["audio", "Audio"],
  ["archive", "Archives"],
  ["other", "Other"],
];

/** Sort orders of listings, as [value, label]. */
export const FILE_SORTS = [
  ["name", "Name"],
  ["kind", "Kind"],
  ["size", "Size"],
  ["updated", "Last updated"],
];

/** The first direction of each sort key. Size and update time start with the largest and newest. */
export const DEFAULT_DIRECTION = { name: "asc", kind: "asc", size: "desc", updated: "desc" };

/** Rows per page. Primer's DataTable paginates from 20 rows. */
export const PAGE_SIZE = 20;

const DOC_EXT = new Set(["txt", "md", "markdown", "csv", "tsv", "json", "yaml", "yml", "xml", "html", "htm", "css", "js", "ts", "go", "py", "rs", "java", "c", "h", "sh", "sql", "doc", "docx", "odt", "rtf", "xls", "xlsx", "ods", "ppt", "pptx", "odp", "epub"]);
const ARCHIVE_EXT = new Set(["zip", "tar", "gz", "tgz", "7z", "rar", "bz2", "xz", "zst"]);

/** Returns the lowercased extension of a name, without the dot. */
function extOf(name) {
  const s = String(name || "");
  const i = s.lastIndexOf(".");
  return i > 0 ? s.slice(i + 1).toLowerCase() : "";
}

/**
 * Classifies a file for the type filter.
 * @param {{mime?: string, name?: string}} file
 * @returns {"image"|"pdf"|"video"|"audio"|"archive"|"document"|"other"}
 */
export function fileTypeOf(file) {
  const mime = String((file && file.mime) || "").toLowerCase();
  const ext = extOf(file && file.name);
  if (mime.startsWith("image/")) return "image";
  if (mime === "application/pdf" || ext === "pdf") return "pdf";
  if (mime.startsWith("video/")) return "video";
  if (mime.startsWith("audio/")) return "audio";
  if (/zip|x-tar|gzip|compressed|x-7z|x-rar|x-bzip|x-xz|zstd/.test(mime) || ARCHIVE_EXT.has(ext)) return "archive";
  if (mime.startsWith("text/") || /json|xml|yaml|msword|officedocument|opendocument|rtf|epub/.test(mime) || DOC_EXT.has(ext)) return "document";
  return "other";
}

/**
 * Names the kind of an item for a row, such as "Folder", "Note" or "PDF".
 * @param {string} kind
 * @param {object} rec
 * @returns {string}
 */
export function kindLabel(kind, rec) {
  if (kind === "folder") return "Folder";
  if (kind === "note") return "Note";
  const type = fileTypeOf(rec);
  const ext = extOf(rec && rec.name);
  switch (type) {
    case "image":
      return ext ? `${ext.toUpperCase()} image` : "Image";
    case "pdf":
      return "PDF";
    case "video":
      return "Video";
    case "audio":
      return "Audio";
    case "archive":
      return ext ? `${ext.toUpperCase()} archive` : "Archive";
    default:
      return ext ? ext.toUpperCase() : "File";
  }
}

/** Returns the update time of a record in milliseconds. */
function updatedMs(rec) {
  const t = Date.parse(rec.updatedAt || rec.modTime || rec.createdAt || "");
  return Number.isNaN(t) ? 0 : t;
}

/**
 * Sorts items for a listing. Name order keeps folders, then notes, then
 * files, and reverses the names inside each group for "desc". Kind, size and
 * update order break ties by name. Without a direction, size and update order
 * put the largest and newest first.
 * @param {{kind: string, rec: object}[]} items
 * @param {"name"|"kind"|"size"|"updated"} [sort]
 * @param {"asc"|"desc"|""} [direction]
 * @returns {{kind: string, rec: object}[]} a new array
 */
export function sortItems(items, sort = "name", direction = "") {
  const dir = direction || DEFAULT_DIRECTION[sort] || "asc";
  const sign = dir === "desc" ? -1 : 1;
  const order = { folder: 0, note: 1, file: 2 };
  const names = (a, b) => itemLabel(a.kind, a.rec).localeCompare(itemLabel(b.kind, b.rec), undefined, { sensitivity: "base", numeric: true });
  const byName = (a, b) => (order[a.kind] - order[b.kind]) || names(a, b);
  const list = [...items];
  if (sort === "size") list.sort((a, b) => sign * ((a.rec.size || 0) - (b.rec.size || 0)) || byName(a, b));
  else if (sort === "updated") list.sort((a, b) => sign * (updatedMs(a.rec) - updatedMs(b.rec)) || byName(a, b));
  else if (sort === "kind") list.sort((a, b) => sign * kindLabel(a.kind, a.rec).localeCompare(kindLabel(b.kind, b.rec)) || byName(a, b));
  else list.sort((a, b) => (order[a.kind] - order[b.kind]) || sign * names(a, b));
  return list;
}

/**
 * Filters files by type and by a name query that must match every word.
 * @param {object[]} files
 * @param {{type?: string, query?: string}} [options]
 * @returns {object[]}
 */
export function filterFiles(files, { type = "all", query = "" } = {}) {
  const words = String(query || "").toLowerCase().split(/\s+/).filter(Boolean);
  return files.filter((f) => {
    if (f.deleted) return false;
    if (type && type !== "all" && fileTypeOf(f) !== type) return false;
    const name = String(f.name || "").toLowerCase();
    return words.every((w) => name.includes(w));
  });
}

/**
 * Lists the direct children of a folder, or of the space root for null. A
 * folder note is the page of its folder and is left out.
 * @param {{folders: object[], notes: object[], files: object[]}} items live items of the space
 * @param {object|null} folder
 * @returns {{kind: string, rec: object}[]}
 */
export function childrenOf(items, folder) {
  const dir = folder ? folder.path : "";
  const inside = (r) => !r.deleted && (folder ? r.parentId === folder.id || (!r.parentId && dirOf(r.path) === dir) : !r.parentId || dirOf(r.path) === "");
  const out = [];
  for (const f of items.folders) if (f.id !== (folder && folder.id) && inside(f) && dirOf(f.path) === dir) out.push({ kind: "folder", rec: f });
  for (const n of items.notes) if (inside(n) && dirOf(n.path) === dir && !(folder && isFolderNote(n, folder))) out.push({ kind: "note", rec: n });
  for (const f of items.files) if (inside(f) && dirOf(f.path) === dir) out.push({ kind: "file", rec: f });
  return out;
}

/**
 * Summarises a listing, such as "2 folders, 3 notes, 1 file, 4.2 MB".
 * @param {{kind: string, rec: object}[]} children
 * @returns {string}
 */
export function summarize(children) {
  const count = { folder: 0, note: 0, file: 0 };
  let bytes = 0;
  for (const t of children) {
    count[t.kind]++;
    if (t.kind === "file") bytes += t.rec.size || 0;
  }
  const parts = [];
  const add = (n, one, many) => n && parts.push(n === 1 ? `1 ${one}` : `${n} ${many}`);
  add(count.folder, "folder", "folders");
  add(count.note, "note", "notes");
  add(count.file, "file", "files");
  if (!parts.length) return "Empty";
  if (count.file) parts.push(formatBytes(bytes));
  return parts.join(", ");
}

/**
 * Formats a duration in seconds as m:ss or h:mm:ss.
 * @param {number} seconds
 * @returns {string}
 */
export function formatDuration(seconds) {
  const s = Math.max(0, Math.round(Number(seconds) || 0));
  const hh = Math.floor(s / 3600);
  const mm = Math.floor((s % 3600) / 60);
  const ss = String(s % 60).padStart(2, "0");
  return hh ? `${hh}:${String(mm).padStart(2, "0")}:${ss}` : `${mm}:${ss}`;
}

/**
 * Computes the rows of one page. The page is clamped into range.
 * @param {number} total
 * @param {number} page 1-based
 * @param {number} [size]
 * @returns {{page: number, count: number, start: number, end: number}} start inclusive, end exclusive
 */
export function pageSlice(total, page, size = PAGE_SIZE) {
  const count = Math.max(1, Math.ceil(Math.max(0, total) / size));
  const current = Math.min(count, Math.max(1, Math.round(Number(page) || 1)));
  const start = (current - 1) * size;
  return { page: current, count, start, end: Math.min(total, start + size) };
}

/**
 * Lists the page buttons of Primer's Pagination: the first page, the last
 * page and 2 pages on each side of the current page. "gap" marks an
 * ellipsis. A gap of a single page shows that page instead.
 * @param {number} page
 * @param {number} count
 * @returns {Array<number|"gap">}
 */
export function paginationItems(page, count) {
  if (count <= 1) return [1];
  const wanted = new Set([1, count]);
  for (let i = page - 2; i <= page + 2; i++) if (i >= 1 && i <= count) wanted.add(i);
  const sorted = [...wanted].sort((a, b) => a - b);
  const out = [];
  let prev = 0;
  for (const n of sorted) {
    if (prev && n - prev === 2) out.push(prev + 1);
    else if (prev && n - prev > 2) out.push("gap");
    out.push(n);
    prev = n;
  }
  return out;
}

/**
 * Formats the range of a page, such as "1 through 20 of 84".
 * @param {number} start inclusive, 0-based
 * @param {number} end exclusive
 * @param {number} total
 * @returns {string}
 */
export function rangeText(start, end, total) {
  if (!total) return "0 items";
  return `${start + 1} through ${end} of ${total}`;
}

/**
 * Formats a time relative to now, such as "5 min ago" or "Mar 3".
 * @param {Date|string|number} when
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
  return d.toLocaleDateString(undefined, sameYear ? { month: "short", day: "numeric" } : { year: "numeric", month: "short", day: "numeric" });
}

/**
 * Builds the breadcrumbs of an item: the space, each folder above it, then
 * the item.
 * @param {object} app
 * @param {object} space
 * @param {object[]} folders live folders of the space
 * @param {string} path entry path of the item
 * @param {string} label
 * @returns {{label: string, href: string|null}[]}
 */
export function crumbsFor(app, space, folders, path, label) {
  const crumbs = [{ label: space.name, href: spaceHref(space) }];
  const parts = String(path || "").split("/");
  for (let i = 1; i < parts.length; i++) {
    const p = parts.slice(0, i).join("/");
    const f = folders.find((x) => x.path === p);
    crumbs.push({ label: f ? f.name : parts[i - 1], href: f ? app.hrefFor("folder", f) : null });
  }
  crumbs.push({ label, href: null });
  return crumbs;
}

/* ---------- Preferences ---------- */

const PREFS_KEY = "markspace.folderView";
const LAYOUT_KEY = "markspace.folderLayout";

/** Reads the listing layout and sort order that folder views share. */
function readPrefs() {
  const out = { layout: "list", sort: "name", direction: "asc" };
  try {
    if (localStorage.getItem(LAYOUT_KEY) === "grid") out.layout = "grid";
    const saved = JSON.parse(localStorage.getItem(PREFS_KEY) || "{}") || {};
    if (FILE_SORTS.some(([v]) => v === saved.sort)) out.sort = saved.sort;
    out.direction = saved.direction === "desc" || saved.direction === "asc" ? saved.direction : DEFAULT_DIRECTION[out.sort];
  } catch {
    /* storage is disabled */
  }
  return out;
}

function writePrefs(prefs) {
  try {
    localStorage.setItem(LAYOUT_KEY, prefs.layout);
    localStorage.setItem(PREFS_KEY, JSON.stringify({ sort: prefs.sort, direction: prefs.direction }));
  } catch {
    /* storage is full or disabled */
  }
}

/* ---------- Shared pieces ---------- */

/** Returns the URL of a space home. */
function spaceHref(space) {
  return "/s/" + encodeURIComponent(space.slug);
}

/** Builds a RelativeTime element, or null for a missing date. */
function timeEl(value) {
  const t = value ? Date.parse(value) : NaN;
  return Number.isFinite(t) ? relativeTimeElement(new Date(t).toISOString()) : null;
}

/**
 * Wires an ActionMenu to a trigger. Click, Enter and Space open it at the
 * first item, ArrowDown at the first and ArrowUp at the last. Items are read
 * on each open, so checked states stay current.
 */
function wireMenu(app, trigger, items, { placement = "bottom-end", label = "" } = {}) {
  trigger.setAttribute("aria-haspopup", "true");
  trigger.setAttribute("aria-expanded", "false");
  const open = (focus = "first") => {
    app.menu = openMenu(trigger, typeof items === "function" ? items() : items, { placement, focus, label });
  };
  trigger.addEventListener("click", (event) => {
    event.preventDefault();
    open("first");
  });
  trigger.addEventListener("keydown", (event) => {
    if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
    event.preventDefault();
    if (trigger.getAttribute("aria-expanded") !== "true") open(event.key === "ArrowUp" ? "last" : "first");
  });
  return trigger;
}

/**
 * Builds the kebab IconButton of an item with its ActionMenu.
 * @param {object} app
 * @param {object} space
 * @param {{kind: string, rec: object}} item
 * @param {{label?: string, skip?: string[], variant?: string, size?: string, className?: string, before?: object[]}} [options]
 */
function itemMenuButton(app, space, item, { label = "", skip = [], variant = "invisible", size = "small", className = "", before = [] } = {}) {
  const name = label || "Actions: " + itemLabel(item.kind, item.rec);
  const trigger = iconButton({ icon: "kebab-horizontal", label: name, variant, size, className: ("item-menu-btn " + className).trim() });
  return wireMenu(app, trigger, () => {
    const rest = itemMenuItems(app, { space, item, where: "view", skip });
    return before.length ? [...before, { separator: true }, ...rest] : rest;
  });
}

/** Thumbnail widths the server renders. Any other w answers 400. */
export const THUMB_WIDTHS = [128, 256, 512, 1024];

/**
 * Returns the thumbnail URL of a file record. A table row asks for 128 px,
 * which covers its 24 px visual at high density. A card asks for 256 px.
 * @param {{thumb: string, sha256?: string}} rec
 * @param {boolean} [large]
 * @returns {string}
 */
export function thumbSrc(rec, large = false) {
  const w = large ? 256 : 128;
  return `${rec.thumb}?w=${w}${rec.sha256 ? `&h=${rec.sha256.slice(0, 12)}` : ""}`;
}

/** Builds the thumbnail or icon of an item. */
function itemThumb(kind, rec, large = false) {
  if (kind === "file" && rec.thumb && !rec.pendingKey) {
    const src = thumbSrc(rec, large);
    const img = h("img", { src, alt: "", loading: "lazy", decoding: "async", draggable: "false" });
    img.addEventListener("error", () => img.replaceWith(icon(itemIcon(kind, rec), large ? 24 : 16)), { once: true });
    return img;
  }
  if (kind === "note" && rec.icon) return h("span", { class: "glyph", text: rec.icon });
  return icon(itemIcon(kind, rec), large ? 24 : 16);
}

/** Formats the short facts of an item: the kind, and the size for a file. */
function itemMeta(kind, rec) {
  return kind === "file" ? `${kindLabel(kind, rec)} · ${formatBytes(rec.size || 0)}` : kindLabel(kind, rec);
}

/**
 * Builds a Primer PageHeader. The parent link shows on narrow viewports in
 * the context area. The title wraps.
 * @param {{title: string, leadingIcon?: Node|null, parent?: {label: string, href: string}|null, description?: Array<Node|string|null>, actions?: Array<Node|null>, border?: boolean, className?: string}} options
 * @returns {HTMLElement}
 */
function pageHeader({ title, leadingIcon = null, parent = null, description = [], actions = [], border = false, className = "" }) {
  const shownActions = actions.filter(Boolean);
  const shownDescription = description.filter((d) => d !== null && d !== "");
  return h(
    "div",
    { class: ("page-header " + className).trim(), "data-border": border ? "true" : null },
    parent ? h("div", { class: "page-header-context" }, h("a", { class: "page-header-parent", href: parent.href }, icon("arrow-left", 16), h("span", { text: parent.label }))) : null,
    leadingIcon ? h("div", { class: "page-header-leading-visual", "aria-hidden": "true" }, leadingIcon) : null,
    h("div", { class: "page-header-title-area" }, h("h1", { class: "page-header-title", tabindex: "-1" }, h("span", { class: "page-header-title-text", text: title }))),
    shownActions.length ? h("div", { class: "page-header-actions" }, shownActions) : null,
    shownDescription.length ? h("div", { class: "page-header-description" }, shownDescription) : null
  );
}

/**
 * Returns the parent link of an item: its folder. An item at the space root
 * has none, because the app header already links to the space home.
 */
function parentOf(app, space, folders, rec) {
  const parent = rec.parentId ? folders.find((f) => f.id === rec.parentId) : folders.find((f) => f.path === dirOf(rec.path));
  return parent ? { label: parent.name, href: app.hrefFor("folder", parent) } : null;
}

/** Appends a Button to a Blankslate built by ui.js. */
function addSlateAction(slate, node) {
  if (!node) return slate;
  (slate.querySelector(".blankslate-inner") || slate).appendChild(h("div", { class: "blankslate-action" }, node));
  return slate;
}

/**
 * Builds a Blankslate for a whole page, with an h1 that takes focus after a
 * soft navigation.
 */
function pageBlankslate({ icon: glyph, title, description = "", action = null, size = "spacious" }) {
  const slate = blankslate({ icon: glyph, title, description, size, headingLevel: 2 });
  const heading = slate.querySelector(".blankslate-heading");
  if (heading) heading.replaceWith(h("h1", { class: "blankslate-heading", tabindex: "-1", text: title }));
  return addSlateAction(slate, action);
}

/* ---------- Listings ---------- */

const listingPages = new Map();

const COLUMNS = [
  { key: "name", label: "Name" },
  { key: "kind", label: "Kind", className: "col-kind" },
  { key: "size", label: "Size", className: "col-num col-size" },
  { key: "updated", label: "Updated", className: "col-updated" },
];

/** Builds the header cell of a sortable column. */
function sortHeader(column, state, onSort) {
  const active = state.sort === column.key;
  const dir = active ? state.direction : DEFAULT_DIRECTION[column.key];
  const trigger = h(
    "button",
    { type: "button", class: "data-table-sort" },
    h("span", { text: column.label }),
    icon(dir === "desc" ? "sort-desc" : "sort-asc", 16)
  );
  trigger.addEventListener("click", () => onSort(column.key));
  return h("th", { scope: "col", class: column.className || null, "aria-sort": active ? (state.direction === "desc" ? "descending" : "ascending") : "none", dataset: { sortKey: column.key } }, trigger);
}

/** Builds one DataTable row of an item. */
function tableRow(app, space, item, { showPath }) {
  const { kind, rec } = item;
  const label = itemLabel(kind, rec);
  return h(
    "tr",
    { dataset: { itemId: rec.id, kind } },
    h(
      "th",
      { scope: "row", class: "file-table-name-cell" },
      h(
        "div",
        { class: "cell-ident" },
        h("span", { class: "file-table-visual", "aria-hidden": "true" }, itemThumb(kind, rec)),
        h(
          "div",
          { class: "cell-main" },
          h("a", { class: "file-table-name", href: app.hrefFor(kind, rec), dataset: kind === "note" ? { noteId: rec.id } : {}, text: label }),
          showPath ? h("span", { class: "cell-sub file-table-path", text: dirOf(rec.path) || space.name }) : null,
          h("span", { class: "cell-sub file-table-meta", text: itemMeta(kind, rec) })
        )
      )
    ),
    h("td", { class: "col-kind", text: kindLabel(kind, rec) }),
    h("td", { class: "col-num col-size", text: kind === "file" ? formatBytes(rec.size || 0) : "" }),
    h("td", { class: "col-updated" }, timeEl(rec.updatedAt || rec.modTime)),
    h("td", { class: "col-actions" }, itemMenuButton(app, space, item))
  );
}

/** Builds one file card of a grid listing. */
function itemCard(app, space, item) {
  const { kind, rec } = item;
  return h(
    "li",
    { class: "item-card", dataset: { itemId: rec.id, kind } },
    h(
      "a",
      { class: "item-card-link", href: app.hrefFor(kind, rec), dataset: kind === "note" ? { noteId: rec.id } : {} },
      h("span", { class: "item-card-thumb", "aria-hidden": "true" }, itemThumb(kind, rec, true)),
      h("span", { class: "item-card-name", text: itemLabel(kind, rec) })
    ),
    h("span", { class: "item-card-meta", text: itemMeta(kind, rec) }),
    itemMenuButton(app, space, item, { variant: "default", className: "item-card-menu" })
  );
}

/**
 * Builds Primer's Pagination for a client-side listing. The file listings and
 * the CSV viewer use it.
 * @param {{page: number, count: number, start: number, end: number}} slice from pageSlice
 * @param {number} total
 * @param {(page: number) => void} goTo
 * @param {{table?: boolean}} [options] table draws the footer under a DataTable
 * @returns {HTMLElement}
 */
export function paginationNav(slice, total, goTo, { table = false } = {}) {
  const direction = (label, glyph, target, disabled, trailing) => {
    const el = h(
      "button",
      { type: "button", class: "pagination-item pagination-direction", "aria-label": label + " Page", "aria-disabled": disabled ? "true" : null },
      trailing ? null : icon(glyph, 16),
      h("span", { text: label }),
      trailing ? icon(glyph, 16) : null
    );
    el.addEventListener("click", () => {
      if (!disabled) goTo(target);
    });
    return el;
  };
  const pages = paginationItems(slice.page, slice.count).map((n) => {
    if (n === "gap") return h("span", { class: "pagination-gap", "aria-hidden": "true", text: "…" });
    const el = h("button", { type: "button", class: "pagination-item", "aria-label": "Page " + n, "aria-current": n === slice.page ? "true" : null, text: String(n) });
    el.addEventListener("click", () => goTo(n));
    return el;
  });
  return h(
    "nav",
    { class: "item-pagination pagination" + (table ? " table-pagination" : ""), "aria-label": "Pagination" },
    h("p", { class: "table-pagination-range", text: rangeText(slice.start, slice.end, total) }),
    h(
      "div",
      { class: "item-pagination-pages" },
      direction("Previous", "chevron-left", slice.page - 1, slice.page <= 1, false),
      pages,
      direction("Next", "chevron-right", slice.page + 1, slice.page >= slice.count, true)
    )
  );
}

/**
 * Builds a listing of items: a DataTable with sortable Name, Kind, Size and
 * Updated columns and a row ActionMenu, or a grid of file cards. Both page at
 * PAGE_SIZE items. The element exposes setSort(sort, direction) for the grid
 * Sort menu, reveal(id) to show the page that holds an item, and state.
 * @param {object} app
 * @param {object} space
 * @param {{kind: string, rec: object}[]} items
 * @param {{layout?: "list"|"grid", showPath?: boolean, label?: string, sort?: string, direction?: string, onSort?: Function, memoryKey?: string}} [options]
 * @returns {HTMLElement}
 */
export function itemListing(app, space, items, { layout = "list", showPath = false, label = "Contents", sort = "name", direction = "", onSort = null, memoryKey = "" } = {}) {
  const key = memoryKey || label;
  const state = { sort, direction: direction || DEFAULT_DIRECTION[sort] || "asc", page: listingPages.get(key) || 1 };
  const root = h("div", { class: "item-listing", dataset: { layout } });
  let sorted = [];
  const draw = (focusSelector = "") => {
    sorted = sortItems(items, state.sort, state.direction);
    const slice = pageSlice(sorted.length, state.page);
    state.page = slice.page;
    listingPages.set(key, state.page);
    const shown = sorted.slice(slice.start, slice.end);
    let body;
    if (layout === "grid") {
      body = h("ul", { class: "item-grid", "aria-label": label }, shown.map((t) => itemCard(app, space, t)));
    } else {
      const table = h(
        "table",
        { class: "data-table file-table", "aria-label": label },
        h("thead", {}, h("tr", {}, COLUMNS.map((c) => sortHeader(c, state, sortBy)), h("th", { scope: "col", class: "col-actions" }, h("span", { class: "sr-only", text: "Actions" })))),
        h("tbody", {}, shown.map((t) => tableRow(app, space, t, { showPath })))
      );
      body = h("div", { class: "table-scroll file-table-scroll", role: "region", tabindex: "0", "aria-label": label + " table" }, table);
    }
    root.classList.toggle("is-paged", slice.count > 1);
    root.replaceChildren(...[body, slice.count > 1 ? paginationNav(slice, sorted.length, goTo, { table: layout !== "grid" }) : null].filter(Boolean));
    if (focusSelector) {
      const target = root.querySelector(focusSelector);
      if (target) target.focus();
    }
  };
  const sortBy = (column) => {
    if (state.sort === column) state.direction = state.direction === "asc" ? "desc" : "asc";
    else {
      state.sort = column;
      state.direction = DEFAULT_DIRECTION[column] || "asc";
    }
    state.page = 1;
    if (onSort) onSort(state.sort, state.direction);
    draw(`th[data-sort-key="${column}"] .data-table-sort`);
    const name = (FILE_SORTS.find(([v]) => v === state.sort) || [, state.sort])[1];
    announce(`Sorted by ${name}, ${state.direction === "desc" ? "descending" : "ascending"}`);
  };
  const goTo = (page) => {
    state.page = page;
    draw(".pagination-item[aria-current]");
    const slice = pageSlice(sorted.length, state.page);
    announce(rangeText(slice.start, slice.end, sorted.length));
  };
  root.state = state;
  root.setSort = (nextSort, nextDirection) => {
    state.sort = nextSort;
    state.direction = nextDirection || DEFAULT_DIRECTION[nextSort] || "asc";
    state.page = 1;
    draw();
  };
  root.reveal = (id) => {
    const index = sorted.findIndex((t) => t.rec.id === id);
    if (index < 0) return;
    const page = Math.floor(index / PAGE_SIZE) + 1;
    if (page !== state.page) {
      state.page = page;
      draw();
    }
  };
  draw();
  return root;
}

/** Builds the icon-only SegmentedControl that switches rows and cards. */
function layoutControl(layout, onChange) {
  const control = segmentedControl({
    label: "Layout",
    items: [
      { label: "List", value: "list", icon: "list-unordered", iconOnly: true, selected: layout !== "grid" },
      { label: "Grid", value: "grid", icon: "apps", iconOnly: true, selected: layout === "grid" },
    ],
    onChange,
  });
  control.classList.add("files-layout");
  return control;
}

/** Builds the Sort ActionMenu that the card grid uses in place of column headers. */
function sortMenu(app, state, onPick) {
  const current = (FILE_SORTS.find(([v]) => v === state.sort) || FILE_SORTS[0])[1];
  const trigger = button({ label: "Sort: " + current, variant: "invisible", trailingVisual: "triangle-down", className: "files-sort" });
  return wireMenu(
    app,
    trigger,
    () => [
      { group: "Sort by" },
      ...FILE_SORTS.map(([value, text]) => ({ label: text, checked: state.sort === value, selectionType: "single", onClick: () => onPick(value, DEFAULT_DIRECTION[value]) })),
      { separator: true },
      { group: "Order" },
      { label: "Ascending", checked: state.direction === "asc", selectionType: "single", onClick: () => onPick(state.sort, "asc") },
      { label: "Descending", checked: state.direction === "desc", selectionType: "single", onClick: () => onPick(state.sort, "desc") },
    ],
    { placement: "bottom-end", label: "Sort" }
  );
}

/**
 * Starts rename in place on an item of a listing.
 * @param {object} app
 * @param {HTMLElement} root the listing host
 * @param {string} id
 */
function renameRow(app, root, id) {
  const listing = root.querySelector(".item-listing");
  if (listing && typeof listing.reveal === "function") listing.reveal(id);
  const holder = Array.from(root.querySelectorAll("[data-item-id]")).find((x) => x.dataset.itemId === id);
  const link = holder && holder.querySelector(".file-table-name, .item-card-link");
  if (!link) return null;
  return app.store.getItem(id).then((item) => {
    if (!item) return null;
    const rec = item.folder || item.note || item.file;
    return editInPlace(link, {
      value: item.kind === "note" ? rec.title || "" : rec.name || "",
      label: item.kind === "note" ? "Title" : "Name",
      onSave: (v) => app.sync.renameItem(id, String(v).trim()),
    });
  });
}

/**
 * Makes an element accept dropped desktop files as uploads into a folder.
 * @param {object} app
 * @param {HTMLElement} el
 * @param {object} space
 * @param {string} folderId
 */
function acceptDrops(app, el, space, folderId) {
  el.addEventListener("dragover", (event) => {
    const types = Array.from((event.dataTransfer && event.dataTransfer.types) || []);
    if (!types.includes("Files")) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "copy";
    el.classList.add("is-drop-target");
  });
  el.addEventListener("dragleave", (event) => {
    if (!event.relatedTarget || !el.contains(event.relatedTarget)) el.classList.remove("is-drop-target");
  });
  el.addEventListener("drop", (event) => {
    el.classList.remove("is-drop-target");
    const files = Array.from((event.dataTransfer && event.dataTransfer.files) || []);
    if (!files.length) return;
    event.preventDefault();
    uploadInto(app, space, folderId, files);
  });
}

/** Finds the item of a folder or file route: the id in history first, then the path. */
async function resolveRouteItem(app, space, params, state) {
  if (state && state.itemId) {
    const byId = await app.store.getItem(state.itemId).catch(() => null);
    const rec = byId && (byId.folder || byId.file || byId.note);
    if (rec && rec.spaceId === space.id) return byId;
  }
  return app.store.getItemByPath(space.slug, params.path).catch(() => null);
}

/** Replaces the address with the canonical URL of an item and remembers its id. */
function keepCanonical(href, itemId) {
  const state = Object.assign({}, history.state || {}, { itemId });
  if (location.pathname !== href || !(history.state && history.state.itemId === itemId)) {
    history.replaceState(state, "", href + location.search + location.hash);
  }
}

/** Renders the not-found state of a folder or file route. */
function notFound(app, el, helpers, space, what) {
  if (helpers.neverSynced(app) && app.isOnline()) {
    el.appendChild(h("div", { class: "page" }, helpers.loadingState("Getting your files")));
    return { title: "Loading", live: true };
  }
  el.appendChild(
    h(
      "div",
      { class: "page" },
      pageBlankslate({
        icon: what === "folder" ? "folder" : "file",
        title: what === "folder" ? "This folder is not here" : "This file is not here",
        description: "It may have been deleted, moved, or not synced to this device yet.",
        action: button({ label: "Go to " + space.name, variant: "primary", href: spaceHref(space) }),
      })
    )
  );
  return { title: "Not found", crumbs: [{ label: space.name, href: spaceHref(space) }, { label: "Not found", href: null }], space, live: true };
}

/**
 * Builds the rail of an item. The panels module loads on first use, so the
 * views stay free of the panel code until a folder or file opens.
 */
async function railFor(app, helpers, { kind, rec, space, access, offline = false }) {
  try {
    const { buildItemRail } = await import("./filepanels.js");
    return buildItemRail(app, { kind, rec, space, access, railTabs: helpers.railTabs, offline });
  } catch (err) {
    // The view stays usable without its rail when the panel modules fail to load.
    console.error("[markspace] the item panels could not load", err);
    return { element: null, select: () => false, setCount() {}, destroy() {} };
  }
}

/** Builds the IconButton that opens the rail sheet on narrow viewports. */
function detailsButton(app, label) {
  return iconButton({ icon: "info", label, variant: "invisible", className: "only-sheet header-details", attrs: { "aria-controls": "rail", "aria-expanded": "false", "data-rail-toggle": "" }, onClick: () => app.showRail() });
}

/**
 * Builds the Banner of a trashed item. Restore acts at once. A failure
 * replaces the Banner with a critical one that offers Try again.
 */
function trashBanner(app, item, noun) {
  const canEdit = accessAtLeast(app.itemAccess(item.kind, item.rec), "edit");
  const name = itemLabel(item.kind, item.rec);
  const build = (failed, message = "") => {
    const restore = async () => {
      try {
        await app.sync.restoreItem(item.rec.id);
        announce(`Restored “${name}”`);
      } catch (err) {
        const next = build(true, (err && err.message) || "");
        el.replaceWith(next);
      }
    };
    const el = banner({
      variant: failed ? "critical" : "warning",
      title: failed ? `This ${noun} could not be restored` : `This ${noun} is in the trash`,
      description: failed ? message || "Check the connection and try again." : canEdit ? "Restore it to put it back where it was." : "",
      actions: canEdit ? [{ label: failed ? "Try again" : "Restore", onClick: restore }] : [],
    });
    el.classList.add("item-trash-banner");
    return el;
  };
  return build(false);
}

/* ---------- Folder view ---------- */

/**
 * Renders /s/{space}/d/{path}. A folder with a folder note shows that note
 * under a slim folder header. Any other folder lists its children.
 * @param {object} app
 * @param {{space: string, path: string}} params
 * @param {HTMLElement} el
 * @param {{state?: object, refresh?: boolean, helpers: object}} options
 */
export async function folderView(app, params, el, { state = {}, refresh = false, helpers }) {
  const space = app.spaceBy(params.space);
  if (!space) return helpers.missingSpace(app, el, params.space);
  const item = await resolveRouteItem(app, space, params, state);
  if (!item) return notFound(app, el, helpers, space, "folder");
  if (item.kind !== "folder") {
    const rec = item.note || item.file;
    queueMicrotask(() => app.navigate(app.hrefFor(item.kind, rec), { replace: true, state: { itemId: rec.id, noteId: item.kind === "note" ? rec.id : undefined } }));
    return { title: "Opening", live: false };
  }
  const folder = item.folder;
  const href = app.hrefFor("folder", folder);
  keepCanonical(href, folder.id);
  const items = await app.store.listItems(space.id);
  const access = app.itemAccess("folder", folder);
  const canEdit = accessAtLeast(access, "edit") && !folder.deleted;
  const crumbs = crumbsFor(app, space, items.folders, folder.path, folder.name);
  const children = childrenOf(items, folder);
  const page = items.notes.find((n) => !n.deleted && isFolderNote(n, folder));
  const folderItem = { kind: "folder", rec: folder };
  const prefs = readPrefs();

  if (page && !state.contents) {
    // The folder note is the page of the folder. The header above it keeps
    // the folder name, its item count and its menu in reach.
    const contentsId = "folder-contents-" + folder.id;
    const contents = h("div", { class: "folder-contents", id: contentsId, hidden: true });
    const toggle = button({ label: summarize(children), variant: "invisible", size: "small", leadingVisual: "list", trailingVisual: "chevron-down", className: "folder-contents-toggle", attrs: { "aria-expanded": "false", "aria-controls": contentsId } });
    toggle.addEventListener("click", () => {
      const open = contents.hidden;
      contents.hidden = !open;
      toggle.setAttribute("aria-expanded", open ? "true" : "false");
      const trailing = toggle.querySelector(".btn-trailing");
      if (trailing) trailing.replaceChildren(icon(open ? "chevron-up" : "chevron-down", 16));
      if (open && !contents.firstChild) {
        contents.appendChild(
          children.length
            ? itemListing(app, space, children, { label: "Contents of " + folder.name, sort: prefs.sort, direction: prefs.direction, memoryKey: "folder:" + folder.id })
            : blankslate({ icon: "folder", title: "Nothing else is in this folder", size: "small", border: true, headingLevel: 2 })
        );
      }
    });
    const header = h(
      "div",
      { class: "folder-header is-slim", role: "group", "aria-label": "Folder " + folder.name },
      h("span", { class: "folder-header-icon", "aria-hidden": "true" }, icon("folder", 16)),
      h("a", { class: "folder-header-name", href, text: folder.name, dataset: { folderId: folder.id } }),
      toggle,
      itemMenuButton(app, space, folderItem, { label: "Folder actions" })
    );
    const view = await helpers.noteView(app, { id: page.id, space: space.slug }, el, { state, refresh, keepUrl: true, header, extraBelowHeader: contents });
    return Object.assign({}, view, { crumbs, itemId: folder.id, title: view.title || folder.name });
  }

  const listingHost = h("div", { class: "folder-listing" });
  const toolbarHost = h("div", { class: "files-toolbar" });
  const newNote = () => app.createNote({ space, parentId: folder.id });
  const upload = async () => uploadInto(app, space, folder.id, await chooseFiles());
  const drawToolbar = (listing) => {
    toolbarHost.replaceChildren(
      ...[
      h("span", { class: "files-spacer" }),
      prefs.layout === "grid"
        ? sortMenu(app, prefs, (sort, direction) => {
            prefs.sort = sort;
            prefs.direction = direction;
            writePrefs(prefs);
            listing.setSort(sort, direction);
            drawToolbar(listing);
          })
        : null,
      layoutControl(prefs.layout, (value) => {
        prefs.layout = value;
        writePrefs(prefs);
        draw();
        const current = toolbarHost.querySelector('.segmented-control-button[aria-current="true"]');
        if (current) current.focus();
      }),
      ].filter(Boolean)
    );
  };
  const draw = () => {
    if (!children.length) {
      toolbarHost.hidden = true;
      const slate = blankslate({
        icon: "folder",
        title: "This folder is empty",
        description: canEdit ? "Drop files here, upload them, or write a note." : "Nothing is in this folder yet.",
        primaryAction: canEdit ? { label: "Upload", onClick: upload } : null,
        border: true,
        headingLevel: 2,
      });
      if (canEdit) addSlateAction(slate, button({ label: "New note", variant: "link", onClick: newNote }));
      listingHost.replaceChildren(slate);
      return;
    }
    toolbarHost.hidden = false;
    const listing = itemListing(app, space, children, {
      layout: prefs.layout,
      label: "Contents of " + folder.name,
      sort: prefs.sort,
      direction: prefs.direction,
      memoryKey: "folder:" + folder.id,
      onSort: (sort, direction) => {
        prefs.sort = sort;
        prefs.direction = direction;
        writePrefs(prefs);
      },
    });
    listingHost.replaceChildren(listing);
    drawToolbar(listing);
  };
  draw();

  const header = pageHeader({
    title: folder.name,
    leadingIcon: icon("folder", 16),
    parent: parentOf(app, space, items.folders, folder),
    description: [h("span", { class: "folder-summary", text: summarize(children) })],
    actions: [
      canEdit ? button({ label: "New note", variant: "primary", leadingVisual: "plus", className: "header-primary", onClick: newNote }) : null,
      detailsButton(app, "Folder details"),
      itemMenuButton(app, space, folderItem, { label: "Folder actions", variant: "default", size: "medium", className: "header-menu" }),
    ],
    border: true,
    className: "folder-page-header",
  });
  const root = h("div", { class: "page folder-page" }, folder.deleted ? trashBanner(app, folderItem, "folder") : null, header, toolbarHost, listingHost);
  if (canEdit) acceptDrops(app, root, space, folder.id);
  el.appendChild(root);

  const rail = await railFor(app, helpers, { kind: "folder", rec: folder, space, access });
  const view = {
    title: folder.name,
    crumbs,
    space,
    itemId: folder.id,
    rail: rail.element,
    railTitle: "Folder details",
    showPanel(kind) {
      if (rail.select(kind)) app.showRail({ focus: false });
    },
    renameItem(id) {
      if (id === folder.id) {
        const title = root.querySelector(".page-header-title-text");
        return editInPlace(title, { value: folder.name, label: "Name", onSave: (v) => app.sync.renameItem(folder.id, String(v).trim()) });
      }
      return renameRow(app, listingHost, id);
    },
    mounted() {
      const pending = app.pendingRename;
      if (pending && pending.where === "view" && children.some((c) => c.rec.id === pending.id)) {
        app.pendingRename = null;
        renameRow(app, listingHost, pending.id);
      }
      if (state.panel) view.showPanel(state.panel);
    },
    refresh() {
      if (root.querySelector(".edit-in-place-form, .edit-in-place-wrap")) return undefined;
      return app.refreshView();
    },
    destroy() {
      rail.destroy();
    },
  };
  return view;
}

/* ---------- File view ---------- */

/**
 * Reports whether a URL answers at all, within four seconds. The service
 * worker leaves HEAD requests to the network, so a failure means no network.
 * @param {string} url
 * @returns {Promise<boolean>}
 */
async function reachable(url) {
  const ac = new AbortController();
  const timer = setTimeout(() => ac.abort(), 4000);
  try {
    await fetch(url, { method: "HEAD", credentials: "same-origin", cache: "no-store", signal: ac.signal });
    return true;
  } catch {
    return false;
  } finally {
    clearTimeout(timer);
  }
}

/** The viewer module that shows a viewer kind. */
function viewerModule(kind) {
  return kind === "video" || kind === "audio" ? "media" : kind;
}

/**
 * Marks a Download control inactive while the file is not on this device. A
 * click explains why in an AnchoredOverlay, as docs/primer.md 6.9 describes.
 */
function markDownloadInactive(el) {
  el.setAttribute("aria-disabled", "true");
  el.setAttribute("data-inactive", "true");
  el.addEventListener("click", (event) => {
    event.preventDefault();
    anchoredOverlay(el, h("p", { class: "file-inactive-note", text: "Download needs a connection. This file is not saved on this device." }), { label: "Download needs a connection", placement: "bottom-end" });
  });
}

/**
 * Renders /s/{space}/f/{path}: a full-bleed viewer under a PageHeader with the
 * file type Octicon, the name, Download and a More ActionMenu. Offline, a file
 * that is not on this device shows the "Available when online" Blankslate.
 * @param {object} app
 * @param {{space: string, path: string}} params
 * @param {HTMLElement} el
 * @param {{state?: object, refresh?: boolean, helpers: object}} options
 */
export async function fileView(app, params, el, { state = {}, helpers }) {
  const space = app.spaceBy(params.space);
  if (!space) return helpers.missingSpace(app, el, params.space);
  const item = await resolveRouteItem(app, space, params, state);
  if (!item) return notFound(app, el, helpers, space, "file");
  if (item.kind !== "file") {
    const rec = item.note || item.folder;
    queueMicrotask(() => app.navigate(app.hrefFor(item.kind, rec), { replace: true, state: { itemId: rec.id, noteId: item.kind === "note" ? rec.id : undefined } }));
    return { title: "Opening", live: false };
  }
  const file = item.file;
  const fileItem = { kind: "file", rec: file };
  const href = app.hrefFor("file", file);
  keepCanonical(href, file.id);
  const items = await app.store.listItems(space.id);
  const access = app.itemAccess("file", file);
  const crumbs = crumbsFor(app, space, items.folders, file.path, file.name);
  const downloadUrl = `${file.url}?download=1`;
  let online = app.isOnline();
  let available = false;
  if (file.pendingKey) available = true;
  else if (app.fileCache) available = await app.fileCache.isAvailable(file).catch(() => false);
  // navigator.onLine stays true on a network that drops every request, so a
  // file that is not on this device is checked with one HEAD request first.
  if (online && !available) online = await reachable(file.url);
  const offlineMissing = !online && !available && !file.pendingKey;

  el.classList.add("file-host");
  const download = () => downloadFile(file);
  const downloadWide = button({ label: "Download", variant: "primary", leadingVisual: "download", className: "file-download is-wide-only", onClick: download });
  const downloadNarrow = iconButton({ icon: "download", label: "Download", variant: "default", size: "large", className: "file-download is-narrow-only", onClick: download });
  if (offlineMissing) {
    markDownloadInactive(downloadWide);
    markDownloadInactive(downloadNarrow);
  }
  const skip = ["Open", "Download", "New note", "New folder", "Upload"];
  const openInTab = { label: "Open in new tab", icon: itemIcon("file", file), onClick: () => window.open(file.url, "_blank", "noopener") };
  const before = offlineMissing ? [] : [openInTab];
  const updated = timeEl(file.updatedAt || file.modTime);
  const header = pageHeader({
    title: file.name,
    leadingIcon: icon(itemIcon("file", file), 16),
    parent: parentOf(app, space, items.folders, file),
    description: [
      h("span", { text: kindLabel("file", file) }),
      h("span", { "aria-hidden": "true", text: "·" }),
      h("span", { text: formatBytes(file.size || 0) }),
      updated ? h("span", { "aria-hidden": "true", text: "·" }) : null,
      updated ? h("span", {}, "Updated ", updated) : null,
    ],
    actions: [
      downloadWide,
      downloadNarrow,
      detailsButton(app, "File details"),
      itemMenuButton(app, space, fileItem, { label: "More file actions", skip, before, variant: "default", size: "medium", className: "header-menu is-wide-only" }),
      itemMenuButton(app, space, fileItem, { label: "More file actions", skip, before, variant: "invisible", size: "large", className: "header-menu is-narrow-only" }),
    ],
    className: "file-view-header",
  });
  const stage = h("div", { class: "file-stage", dataset: { fileId: file.id } });
  el.append(header);
  if (file.deleted) el.append(trashBanner(app, fileItem, "file"));
  el.append(stage);

  if (offlineMissing) {
    const slate = blankslate({
      icon: "cloudOff",
      title: "Available when online",
      description: `${file.name} is not saved on this device. It opens when the connection returns.`,
      size: "medium",
      headingLevel: 2,
    });
    stage.appendChild(h("div", { class: "file-state file-offline", role: "status" }, slate));
  }

  const rail = await railFor(app, helpers, { kind: "file", rec: file, space, access, offline: offlineMissing });
  const ac = new AbortController();
  let handle = null;
  let blobUrl = null;
  let mountedSha = file.sha256 || "";
  const view = {
    title: file.name,
    crumbs,
    space,
    itemId: file.id,
    live: true,
    rail: rail.element,
    railTitle: "File details",
    ownsFocus: false,
    showPanel(kind) {
      if (rail.select(kind)) app.showRail({ focus: false });
    },
    renameItem(id) {
      if (id !== file.id) return null;
      const title = header.querySelector(".page-header-title-text");
      return editInPlace(title, { value: file.name, label: "Name", onSave: (v) => app.sync.renameItem(file.id, String(v).trim()) });
    },
    async mounted() {
      if (state.panel) view.showPanel(state.panel);
      if (offlineMissing) {
        // The Info tab is selected without opening the rail, so no sheet opens unasked on a phone.
        if (!state.panel) rail.select("info");
        window.addEventListener("online", () => app.refreshView(), { signal: ac.signal, once: true });
        return;
      }
      let url = file.url;
      if (file.pendingKey) {
        const blob = await app.store.getBlob(file.pendingKey).catch(() => null);
        if (blob) url = blobUrl = URL.createObjectURL(blob);
      } else if (online && app.fileCache) {
        app.fileCache.open(file).catch(() => {});
      } else if (app.fileCache) {
        app.fileCache.touch(file.id).catch(() => {});
      }
      if (ac.signal.aborted) return;
      const loading = h("div", { class: "file-state file-loading" }, spinner({ size: "medium", label: "Loading " + file.name, delay: 1000 }));
      stage.appendChild(loading);
      try {
        const kinds = await import("./viewers/kind.js");
        const mod = await import(`./viewers/${viewerModule(kinds.viewerKind(file))}.js`);
        loading.remove();
        if (ac.signal.aborted) return;
        handle = await mod.mountViewer(stage, { file, url, downloadUrl, isNarrow, signal: ac.signal });
        if (ac.signal.aborted && handle) handle.unmount();
      } catch (err) {
        loading.remove();
        if (ac.signal.aborted) return;
        console.error("[markspace] the file viewer failed", err);
        const slate = blankslate({ icon: "alert", title: "This file could not be shown", description: "The viewer did not load. Download the file to open it in another app.", size: "medium", headingLevel: 2 });
        addSlateAction(slate, button({ label: "Download", leadingVisual: "download", onClick: download }));
        stage.replaceChildren(h("div", { class: "file-state", role: "alert" }, slate));
      }
    },
    async refresh({ ids = [] } = {}) {
      if (!ids.includes(file.id)) return undefined;
      const next = await app.store.getFile(file.id);
      if (!next || next.deleted !== file.deleted || next.path !== file.path || (next.sha256 || "") !== mountedSha || next.pendingKey !== file.pendingKey) {
        return app.refreshView();
      }
      return undefined;
    },
    destroy() {
      ac.abort();
      if (handle && typeof handle.unmount === "function") {
        try {
          handle.unmount();
        } catch (err) {
          console.error("[markspace] viewer cleanup failed", err);
        }
      }
      if (blobUrl) URL.revokeObjectURL(blobUrl);
      rail.destroy();
      mountedSha = "";
    },
  };
  return view;
}

/* ---------- Permalink ---------- */

/**
 * Renders /i/{id}: finds the item locally, or online through the API, and
 * replaces the address with its view.
 * @param {object} app
 * @param {{id: string}} params
 * @param {HTMLElement} el
 * @param {{helpers: object}} options
 */
export async function itemRedirectView(app, params, el, { helpers }) {
  let item = await app.store.getItem(params.id).catch(() => null);
  if (!item && app.isOnline() && app.api) {
    try {
      const res = await app.api("GET", `/api/items/${encodeURIComponent(params.id)}`);
      if (res && res.kind) item = res;
    } catch {
      item = null;
    }
  }
  const rec = item && (item.folder || item.note || item.file);
  if (rec) {
    const target = app.hrefFor(item.kind, rec);
    queueMicrotask(() => app.navigate(target, { replace: true, state: item.kind === "note" ? { noteId: rec.id } : { itemId: rec.id } }));
    el.appendChild(h("div", { class: "page" }, helpers.loadingState("Opening")));
    return { title: "Opening", live: false };
  }
  if (helpers.neverSynced(app) && app.isOnline()) {
    el.appendChild(h("div", { class: "page" }, helpers.loadingState("Getting your notes")));
    return { title: "Loading", live: true };
  }
  el.appendChild(
    h(
      "div",
      { class: "page" },
      pageBlankslate({
        icon: app.isOnline() ? "file" : "cloudOff",
        title: "This item is not here",
        description: app.isOnline() ? "It may have been deleted or you may not have access." : "It is not on this device. Try again when you are online.",
        action: button({ label: "Go home", variant: "primary", href: "/" }),
      })
    )
  );
  return { title: "Not found", live: true };
}

/* ---------- Space contents ---------- */

/**
 * Builds the root listing of a space for the space home.
 * @param {object} app
 * @param {object} space
 * @returns {Promise<{element: HTMLElement, count: number, summary: string}>}
 */
export async function spaceContents(app, space) {
  const items = await app.store.listItems(space.id);
  const children = childrenOf(items, null);
  const prefs = readPrefs();
  const element = h(
    "div",
    { class: "folder-listing space-listing" },
    children.length
      ? itemListing(app, space, children, {
          label: "Contents of " + space.name,
          sort: prefs.sort,
          direction: prefs.direction,
          memoryKey: "space:" + space.id,
          onSort: (sort, direction) => writePrefs(Object.assign(prefs, { sort, direction })),
        })
      : null
  );
  if (accessAtLeast(app.itemAccess("space", space), "edit")) acceptDrops(app, element, space, "");
  return { element, count: children.length, summary: summarize(children) };
}

/**
 * Builds the secondary New menu of a space or folder: New folder and Upload.
 * @param {object} app
 * @param {object} space
 * @param {string} folderId
 * @returns {HTMLButtonElement}
 */
export function newMenuButton(app, space, folderId = "") {
  const trigger = iconButton({ icon: "triangle-down", label: "More ways to add", variant: "default", className: "new-menu-btn" });
  return wireMenu(
    app,
    trigger,
    [
      { label: "New folder", icon: "folder", onClick: () => newFolderIn(app, space, folderId, "view") },
      { label: "Upload files", icon: "upload", onClick: async () => uploadInto(app, space, folderId, await chooseFiles()) },
    ],
    { placement: "bottom-end", label: "More ways to add" }
  );
}

/* ---------- Files page ---------- */

/**
 * Renders /s/{space}/files: every file of the space in one listing, with a
 * name filter, a type ActionMenu, the list and grid SegmentedControl, and
 * sorting. It reads the local store, so it works offline.
 * @param {object} app
 * @param {{space: string}} params
 * @param {HTMLElement} el
 * @param {{helpers: object}} options
 */
export async function filesPageView(app, params, el, { helpers }) {
  const space = app.spaceBy(params.space);
  if (!space) return helpers.missingSpace(app, el, params.space);
  const crumbs = [{ label: space.name, href: spaceHref(space) }, { label: "Files", href: null }];
  const files = (await app.store.listFiles(space.id)).filter((f) => !f.deleted);
  const canEdit = accessAtLeast(app.itemAccess("space", space), "edit");
  const prefs = readPrefs();
  const saved = (() => {
    try {
      return JSON.parse(localStorage.getItem("markspace.filesPage") || "{}") || {};
    } catch {
      return {};
    }
  })();
  const state = { type: FILE_TYPES.some(([v]) => v === saved.type) ? saved.type : "all", query: "" };
  const memoryKey = "files:" + space.id;
  const remember = () => {
    try {
      localStorage.setItem("markspace.filesPage", JSON.stringify({ type: state.type }));
    } catch {
      /* storage is full or disabled */
    }
  };
  const upload = async () => uploadInto(app, space, "", await chooseFiles());

  const page = h("div", { class: "page files-page" });
  page.appendChild(
    pageHeader({
      title: "Files",
      leadingIcon: icon("files", 16),
      description: [h("span", { text: `Every file in ${space.name}.` })],
      actions: [canEdit ? button({ label: "Upload", variant: "primary", leadingVisual: "upload", className: "header-primary", onClick: upload }) : null],
      border: true,
    })
  );
  const active = typeof app.sync.uploads === "function" ? app.sync.uploads().filter((u) => u.state !== "done") : [];
  if (active.length) {
    page.appendChild(h("section", { class: "files-uploads", "aria-labelledby": "files-uploads-title" }, h("h2", { class: "files-section-title", id: "files-uploads-title", text: "Uploading" }), uploadList({ sync: app.sync })));
  }

  const search = h("input", { type: "search", class: "text-input-control", "aria-label": "Filter files by name", autocomplete: "off", spellcheck: "false" });
  const searchBox = h("span", { class: "text-input has-leading-visual files-filter" }, h("span", { class: "text-input-visual", "aria-hidden": "true" }, icon("search", 16)), search);
  const typeText = () => (FILE_TYPES.find(([v]) => v === state.type) || FILE_TYPES[0])[1];
  const typeButton = button({ label: "Type: " + typeText(), leadingVisual: "filter", trailingVisual: "triangle-down", className: "files-type" });
  wireMenu(
    app,
    typeButton,
    () => [
      { group: "File type" },
      ...FILE_TYPES.map(([value, text]) => ({
        label: text,
        checked: state.type === value,
        selectionType: "single",
        onClick: () => {
          state.type = value;
          remember();
          const labelEl = typeButton.querySelector(".btn-label");
          if (labelEl) labelEl.textContent = "Type: " + typeText();
          listingPages.delete(memoryKey);
          draw({ speak: true });
        },
      })),
    ],
    { placement: "bottom-start", label: "File type" }
  );
  const count = h("span", { class: "files-count" });
  const extras = h("span", { class: "files-toolbar-end" });
  const toolbarRow = h("div", { class: "files-toolbar" }, searchBox, typeButton, count, h("span", { class: "files-spacer" }), extras);
  const body = h("div", { class: "files-body" });
  page.append(toolbarRow, body);

  let speakTimer = 0;
  const draw = ({ speak = false } = {}) => {
    const shown = filterFiles(files, state).map((rec) => ({ kind: "file", rec }));
    const total = files.reduce((sum, f) => sum + (f.size || 0), 0);
    count.textContent = (shown.length === files.length ? `${files.length} ${files.length === 1 ? "file" : "files"}` : `${shown.length} of ${files.length} files`) + " · " + formatBytes(total);
    if (speak) {
      clearTimeout(speakTimer);
      speakTimer = setTimeout(() => announce(count.textContent), 400);
    }
    if (!files.length) {
      toolbarRow.hidden = true;
      body.replaceChildren(
        blankslate({
          icon: "files",
          title: "No files yet",
          description: canEdit ? "Upload files, or drop them onto a folder in the sidebar." : "Files in this space appear here.",
          primaryAction: canEdit ? { label: "Upload", onClick: upload } : null,
          border: true,
          headingLevel: 2,
        })
      );
      return;
    }
    toolbarRow.hidden = false;
    if (!shown.length) {
      extras.replaceChildren(layoutControl(prefs.layout, setLayout));
      const slate = blankslate({ icon: "filter", title: "No files match", description: "Change the name filter or the type.", border: true, headingLevel: 2 });
      addSlateAction(
        slate,
        button({
          label: "Clear filters",
          variant: "link",
          onClick: () => {
            state.query = "";
            state.type = "all";
            search.value = "";
            remember();
            const labelEl = typeButton.querySelector(".btn-label");
            if (labelEl) labelEl.textContent = "Type: " + typeText();
            draw({ speak: true });
            search.focus();
          },
        })
      );
      body.replaceChildren(slate);
      return;
    }
    const listing = itemListing(app, space, shown, {
      layout: prefs.layout,
      showPath: true,
      label: "Files",
      sort: prefs.sort,
      direction: prefs.direction,
      memoryKey,
      onSort: (sort, direction) => {
        prefs.sort = sort;
        prefs.direction = direction;
        writePrefs(prefs);
      },
    });
    const sortPick = (sort, direction) => {
      prefs.sort = sort;
      prefs.direction = direction;
      writePrefs(prefs);
      listing.setSort(sort, direction);
      extras.replaceChildren(sortMenu(app, prefs, sortPick), layoutControl(prefs.layout, setLayout));
    };
    extras.replaceChildren(...[prefs.layout === "grid" ? sortMenu(app, prefs, sortPick) : null, layoutControl(prefs.layout, setLayout)].filter(Boolean));
    body.replaceChildren(listing);
  };
  const setLayout = (value) => {
    prefs.layout = value;
    writePrefs(prefs);
    draw();
    const current = extras.querySelector('.segmented-control-button[aria-current="true"]');
    if (current) current.focus();
  };
  search.addEventListener("input", () => {
    state.query = search.value;
    listingPages.delete(memoryKey);
    draw({ speak: true });
  });
  draw();
  if (canEdit) acceptDrops(app, page, space, "");
  el.appendChild(page);
  return {
    title: "Files",
    crumbs,
    space,
    refresh() {
      if (document.activeElement === search && search.value) return undefined;
      return app.refreshView();
    },
  };
}

/* ---------- Links in rendered notes ---------- */

/**
 * Prepares the link resolution of a note for markdown.js render: a
 * synchronous resolveItem over the space snapshot, and resolveImage that maps
 * the URL of a file waiting to upload to its local blob. After render,
 * attach(body) opens embedded image files in the file cache and replaces
 * images that cannot load with a placeholder that names them.
 * @param {object} app
 * @param {object} note
 * @returns {Promise<{resolveItem: Function, resolveImage: Function, attach: (body: HTMLElement) => void, destroy: () => void}>}
 */
export async function buildItemResolver(app, note) {
  const snap = await app.store.snapshot(note.spaceId);
  const items = await app.store.listItems(note.spaceId);
  const byId = new Map();
  for (const f of items.folders) byId.set(f.id, { kind: "folder", rec: f });
  for (const n of items.notes) byId.set(n.id, { kind: "note", rec: n });
  for (const f of items.files) byId.set(f.id, { kind: "file", rec: f });
  const objectUrls = new Map();
  for (const f of items.files) {
    if (!f.pendingKey) continue;
    const blob = await app.store.getBlob(f.pendingKey).catch(() => null);
    if (blob) objectUrls.set(f.url, URL.createObjectURL(blob));
  }
  const images = new Map();
  const resolveItem = (target, { form, embed } = {}) => {
    const hit = resolveWith(snap, note.path || "", target, form);
    const t = hit && byId.get(hit.id);
    if (!t) return null;
    const out = { id: t.rec.id, kind: t.kind, href: app.hrefFor(t.kind, t.rec), fileUrl: t.kind === "file" ? t.rec.url : "", mime: t.rec.mime || "", name: t.rec.name || "" };
    if (t.kind === "file" && (embed || form === "markdownImage")) images.set(t.rec.url, t.rec);
    return out;
  };
  const resolveImage = (url) => objectUrls.get(url) || url;
  return {
    resolveItem,
    resolveImage,
    attach(body) {
      for (const img of body.querySelectorAll("img")) {
        const src = img.getAttribute("src") || "";
        const rec = images.get(src) || Array.from(images.values()).find((r) => objectUrls.get(r.url) === src);
        if (rec && app.fileCache && app.isOnline() && !rec.pendingKey) app.fileCache.open(rec).catch(() => {});
        img.addEventListener(
          "error",
          () => {
            const name = (rec && rec.name) || img.getAttribute("alt") || decodeURIComponent(src.split("/").pop() || "image");
            img.replaceWith(
              h("span", { class: "file-embed-missing", role: "img", "aria-label": `${name}, available when online` }, icon(app.isOnline() ? "image" : "cloudOff", 16), h("span", { text: name }))
            );
          },
          { once: true }
        );
      }
    },
    destroy() {
      for (const url of objectUrls.values()) URL.revokeObjectURL(url);
    },
  };
}

export { trashItemsWithUndo };
