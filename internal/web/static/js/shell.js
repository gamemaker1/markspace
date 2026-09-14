/**
 * Markspace client shell. It loads the session, opens the local store,
 * starts the sync engine, draws the layout and routes the shell routes from
 * docs/offline.md with the History API.
 *
 * The first half of the file holds pure functions, which node tests import.
 * Nothing at the top level touches the DOM, and the browser modules load
 * with dynamic imports, so a missing export logs an error instead of
 * breaking the page.
 */

import {
  h,
  icon,
  ICON_NAMES,
  NARROW_QUERY,
  anchoredOverlay,
  announce,
  avatar as uiAvatar,
  banner,
  blankslate,
  button,
  clearFieldErrors,
  confirmDialog,
  focusables,
  formControl,
  iconButton,
  openDialog,
  openMenu,
  prefersReducedMotion,
  relativeTime,
  selectPanel,
  setCount,
  showFieldErrors,
  spinner,
  uid,
  watchTableScroll,
  undoable,
  uploadList,
  counterLabel,
  segmentedControl,
} from "./ui.js";

/* ---------- Routes and URLs ---------- */

const LIST_ROUTES = new Set(["search", "starred", "recent", "shared"]);

/** Decodes one path segment, returning null when it is malformed. */
function decodeSegment(segment) {
  try {
    return decodeURIComponent(segment);
  } catch (err) {
    return null;
  }
}

/**
 * Matches a path against the shell routes.
 * @param {string} pathname URL path, such as "/s/docs/n/a/b".
 * @param {string} [search] query string, with or without the leading "?".
 * @returns {{name: string, params: object} | null} null for a server-only path.
 */
export function parseRoute(pathname, search = "") {
  let path = pathname || "/";
  if (path.length > 1) {
    path = path.replace(/\/+$/, "") || "/";
  }
  const query = new URLSearchParams(search || "");
  if (path === "/") {
    return { name: "home", params: {} };
  }
  const segments = path.split("/").slice(1);
  if (segments.length === 1 && LIST_ROUTES.has(segments[0])) {
    if (segments[0] === "search") {
      return {
        name: "search",
        params: {
          q: query.get("q") || "",
          space: query.get("space") || "",
          mode: query.get("mode") || "",
        },
      };
    }
    return { name: segments[0], params: {} };
  }
  if (segments[0] === "n" && segments.length === 2 && segments[1]) {
    const id = decodeSegment(segments[1]);
    return id ? { name: "permalink", params: { id, edit: (query.get("edit") || "") !== "" } } : null;
  }
  if (segments[0] === "i" && segments.length === 2 && segments[1]) {
    const id = decodeSegment(segments[1]);
    return id ? { name: "item", params: { id } } : null;
  }
  if (segments[0] !== "s" || !segments[1]) {
    return null;
  }
  const space = decodeSegment(segments[1]);
  if (!space) {
    return null;
  }
  if (segments.length === 2) {
    return { name: "space", params: { space } };
  }
  if (segments[2] === "trash" && segments.length === 3) {
    return { name: "trash", params: { space } };
  }
  if (segments[2] === "files" && segments.length === 3) {
    return { name: "files", params: { space } };
  }
  if (segments[2] === "t" && segments.length === 4 && segments[3]) {
    const tag = decodeSegment(segments[3]);
    return tag ? { name: "tag", params: { space, tag } } : null;
  }
  if ((segments[2] === "d" || segments[2] === "f") && segments.length >= 4) {
    const parts = segments.slice(3).map(decodeSegment);
    if (parts.some((part) => !part)) {
      return null;
    }
    return { name: segments[2] === "d" ? "folder" : "file", params: { space, path: parts.join("/") } };
  }
  if (segments[2] === "n" && segments.length >= 4) {
    const parts = segments.slice(3).map(decodeSegment);
    if (parts.some((part) => !part)) {
      return null;
    }
    return {
      name: "note",
      params: { space, path: stripMd(parts.join("/")), edit: (query.get("edit") || "") !== "" },
    };
  }
  return null;
}

/**
 * Reports whether the service worker and the shell own a path.
 * @param {string} pathname
 * @returns {boolean}
 */
export function isShellPath(pathname) {
  return parseRoute(pathname) !== null;
}

/**
 * Removes a trailing ".md" from a note path.
 * @param {string} path
 * @returns {string}
 */
export function stripMd(path) {
  return String(path || "").replace(/\.md$/i, "");
}

/**
 * Builds a space home URL.
 * @param {string} slug
 * @returns {string}
 */
export function spaceUrl(slug) {
  return "/s/" + encodeURIComponent(slug);
}

/**
 * Builds the canonical note URL, "/s/{space}/n/{path}".
 * @param {string} slug space slug.
 * @param {string} path note path, with or without ".md".
 * @param {{edit?: boolean}} [options]
 * @returns {string}
 */
export function noteUrl(slug, path, { edit = false } = {}) {
  const clean = stripMd(path).split("/").filter(Boolean).map(encodeURIComponent).join("/");
  return spaceUrl(slug) + "/n/" + clean + (edit ? "?edit=1" : "");
}

/**
 * Builds the best URL for a note: the canonical path when the space slug and
 * path are known, and the permalink otherwise.
 * @param {object} note local note.
 * @param {string} [slug] space slug, used when the note lacks spaceSlug.
 * @param {{edit?: boolean}} [options]
 * @returns {string}
 */
export function noteHref(note, slug, { edit = false } = {}) {
  const spaceSlug = (note && note.spaceSlug) || slug;
  if (note && spaceSlug && note.path) {
    return noteUrl(spaceSlug, note.path, { edit });
  }
  return permalinkUrl(note ? note.id : "", { edit });
}

/**
 * Builds the permalink of a note.
 * @param {string} id
 * @param {{edit?: boolean}} [options]
 * @returns {string}
 */
export function permalinkUrl(id, { edit = false } = {}) {
  return "/n/" + encodeURIComponent(id) + (edit ? "?edit=1" : "");
}

/**
 * Builds a tag listing URL.
 * @param {string} slug
 * @param {string} tag
 * @returns {string}
 */
export function tagUrl(slug, tag) {
  return spaceUrl(slug) + "/t/" + encodeURIComponent(tag);
}

/**
 * Builds the trash URL of a space.
 * @param {string} slug
 * @returns {string}
 */
export function trashUrl(slug) {
  return spaceUrl(slug) + "/trash";
}

/**
 * Builds the files URL of a space.
 * @param {string} slug
 * @returns {string}
 */
export function filesUrl(slug) {
  return spaceUrl(slug) + "/files";
}

/**
 * Builds a search URL, leaving out empty parameters.
 * @param {{q?: string, space?: string, mode?: string}} [params]
 * @returns {string}
 */
export function searchUrl({ q = "", space = "", mode = "" } = {}) {
  const query = new URLSearchParams();
  if (q) {
    query.set("q", q);
  }
  if (space) {
    query.set("space", space);
  }
  if (mode) {
    query.set("mode", mode);
  }
  const text = query.toString();
  return "/search" + (text ? "?" + text : "");
}

/** Encodes an entry path segment by segment. */
function encodePath(path) {
  return String(path || "").split("/").filter(Boolean).map(encodeURIComponent).join("/");
}

/**
 * Returns the URL of a folder view.
 * @param {string} slug space slug.
 * @param {string} path entry path of the folder.
 * @returns {string}
 */
export function folderUrl(slug, path) {
  return spaceUrl(slug) + "/d/" + encodePath(path);
}

/**
 * Returns the URL of a file view.
 * @param {string} slug space slug.
 * @param {string} path entry path of the file.
 * @returns {string}
 */
export function fileViewUrl(slug, path) {
  return spaceUrl(slug) + "/f/" + encodePath(path);
}

/**
 * Returns the permalink of any item.
 * @param {string} id
 * @returns {string}
 */
export function itemPermalinkUrl(id) {
  return "/i/" + encodeURIComponent(id);
}

/**
 * Decides whether the shell handles a click on a link. It returns the local
 * URL to route to, or null to let the browser navigate.
 * @param {{button?: number, metaKey?: boolean, ctrlKey?: boolean, shiftKey?: boolean, altKey?: boolean, defaultPrevented?: boolean}} event
 * @param {{href: string, target?: string, download?: boolean, fullNav?: boolean}} link
 * @param {string} origin location.origin of the page.
 * @returns {string | null}
 */
export function interceptTarget(event, link, origin) {
  if (!link || !link.href || event.defaultPrevented) {
    return null;
  }
  if ((event.button || 0) !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) {
    return null;
  }
  if ((link.target && link.target !== "_self") || link.download || link.fullNav) {
    return null;
  }
  let url;
  try {
    url = new URL(link.href, origin);
  } catch (err) {
    return null;
  }
  if (url.origin !== origin || !parseRoute(url.pathname, url.search)) {
    return null;
  }
  return url.pathname + url.search + url.hash;
}

/* ---------- Notes, access and trees ---------- */

const ACCESS_RANK = { none: 0, view: 1, comment: 2, edit: 3, manage: 4 };

/**
 * Reports whether an access level permits at least another.
 * @param {string} have
 * @param {string} want
 * @returns {boolean}
 */
export function accessAtLeast(have, want) {
  return (ACCESS_RANK[have] || 0) >= (ACCESS_RANK[want] || 0);
}

/**
 * Returns the access a user has to a space, from the role or the admin flag.
 * @param {{myRole?: string} | null} space
 * @param {{isAdmin?: boolean} | null} user
 * @returns {string} "none", "view", "comment", "edit" or "manage".
 */
export function spaceAccess(space, user) {
  if (user && user.isAdmin) {
    return "manage";
  }
  switch (space && space.myRole) {
    case "owner":
    case "admin":
      return "manage";
    case "editor":
      return "edit";
    case "commenter":
      return "comment";
    case "viewer":
      return "view";
  }
  return "none";
}

/**
 * Returns the access a user has to one note. The sync feed fills note.access,
 * and a note created on this device falls back to the space access.
 * @param {{access?: string} | null} note
 * @param {object | null} space
 * @param {object | null} user
 * @returns {string}
 */
export function noteAccess(note, space, user) {
  if (note && note.access) {
    return note.access;
  }
  return spaceAccess(space, user);
}

/**
 * Indexes a list of notes by id.
 * @param {object[]} notes
 * @returns {Map<string, object>}
 */
export function indexById(notes) {
  const map = new Map();
  for (const note of notes || []) {
    if (note && note.id) {
      map.set(note.id, note);
    }
  }
  return map;
}

/**
 * Lists the ancestor ids of a note, root first. A cycle or a missing parent
 * ends the walk.
 * @param {string} noteId
 * @param {Map<string, object>} byId
 * @returns {string[]}
 */
export function ancestorIds(noteId, byId) {
  const out = [];
  const seen = new Set([noteId]);
  let current = byId.get(noteId);
  while (current && current.parentId && !seen.has(current.parentId)) {
    seen.add(current.parentId);
    out.unshift(current.parentId);
    current = byId.get(current.parentId);
  }
  return out;
}

/**
 * Builds the breadcrumb trail of a note: the space, every ancestor by title,
 * then the note itself without a link.
 * @param {{space: object | null, note: object | null, byId: Map<string, object>}} input
 * @returns {{label: string, href: string | null}[]}
 */
export function buildBreadcrumbs({ space, note, byId }) {
  const crumbs = [];
  const slug = space ? space.slug : note && note.spaceSlug;
  if (space) {
    crumbs.push({ label: space.name || space.slug, href: spaceUrl(space.slug) });
  }
  if (!note) {
    return crumbs;
  }
  for (const id of ancestorIds(note.id, byId || new Map())) {
    const parent = byId.get(id);
    crumbs.push({ label: parent.title || "Untitled", href: noteHref(parent, slug) });
  }
  crumbs.push({ label: note.title || "Untitled", href: null });
  return crumbs;
}

/**
 * Lists the ids of a note and all its descendants.
 * @param {string} noteId
 * @param {object[]} notes every note in the space.
 * @returns {Set<string>}
 */
export function descendantIds(noteId, notes) {
  const children = new Map();
  for (const note of notes || []) {
    if (!note.parentId) {
      continue;
    }
    if (!children.has(note.parentId)) {
      children.set(note.parentId, []);
    }
    children.get(note.parentId).push(note.id);
  }
  const out = new Set([noteId]);
  const queue = [noteId];
  while (queue.length) {
    for (const child of children.get(queue.shift()) || []) {
      if (!out.has(child)) {
        out.add(child);
        queue.push(child);
      }
    }
  }
  return out;
}

/**
 * Returns a position after every sibling, in steps of 100 like the server.
 * @param {{position?: number}[]} siblings
 * @returns {number}
 */
export function nextPosition(siblings) {
  let max = 0;
  for (const sibling of siblings || []) {
    if (typeof sibling.position === "number" && sibling.position > max) {
      max = sibling.position;
    }
  }
  return max + 100;
}

/* ---------- Tree expand state ---------- */

/** Key of the expanded-tree record, shared with the server pages. */
export const TREE_KEY_PREFIX = "markspace.tree.";

/**
 * Reads the expanded note ids of one space from storage.
 * @param {{getItem(key: string): string | null}} storage
 * @param {string} spaceId
 * @returns {Set<string>}
 */
export function readExpanded(storage, spaceId) {
  try {
    const raw = storage.getItem(TREE_KEY_PREFIX + spaceId);
    const parsed = raw ? JSON.parse(raw) : {};
    return new Set(Object.keys(parsed || {}).filter((id) => parsed[id] === true));
  } catch (err) {
    return new Set();
  }
}

/**
 * Writes the expanded note ids of one space to storage, in the object form
 * app.js reads.
 * @param {{setItem(key: string, value: string): void}} storage
 * @param {string} spaceId
 * @param {Set<string>} expanded
 */
export function writeExpanded(storage, spaceId, expanded) {
  const record = {};
  for (const id of expanded) {
    record[id] = true;
  }
  try {
    storage.setItem(TREE_KEY_PREFIX + spaceId, JSON.stringify(record));
  } catch (err) {
    /* storage is full or disabled */
  }
}

/**
 * Returns a copy of the expanded set with one id opened or closed.
 * @param {Set<string>} expanded
 * @param {string} id
 * @param {boolean} [open] toggles when omitted.
 * @returns {Set<string>}
 */
export function toggleExpanded(expanded, id, open) {
  const next = new Set(expanded);
  const shouldOpen = open === undefined ? !next.has(id) : open;
  if (shouldOpen) {
    next.add(id);
  } else {
    next.delete(id);
  }
  return next;
}

/**
 * Returns a copy of the expanded set with every ancestor of a note opened,
 * so the current note is visible in the tree.
 * @param {Set<string>} expanded
 * @param {string} noteId
 * @param {Map<string, object>} byId
 * @returns {Set<string>}
 */
export function expandToNote(expanded, noteId, byId) {
  const next = new Set(expanded);
  for (const id of ancestorIds(noteId, byId)) {
    next.add(id);
  }
  return next;
}

/* ---------- Sync status, time and text ---------- */

/**
 * Formats a time as a coarse relative phrase, such as "3 minutes ago".
 * @param {string | number | Date} value
 * @param {number} [now] milliseconds since the epoch.
 * @returns {string}
 */
export function timeAgo(value, now = Date.now()) {
  if (value === null || value === undefined || value === "") {
    return "";
  }
  const time = value instanceof Date ? value.getTime() : typeof value === "number" ? value : Date.parse(value);
  if (!Number.isFinite(time)) {
    return "";
  }
  let seconds = Math.round((now - time) / 1000);
  const future = seconds < 0;
  seconds = Math.abs(seconds);
  if (seconds < 45) {
    return "just now";
  }
  const unit = (n, one, many) => (n === 1 ? "1 " + one : n + " " + many);
  let text;
  if (seconds < 90) {
    text = "1 minute";
  } else if (seconds < 3600) {
    text = unit(Math.round(seconds / 60), "minute", "minutes");
  } else if (seconds < 5400) {
    text = "1 hour";
  } else if (seconds < 86400) {
    text = unit(Math.round(seconds / 3600), "hour", "hours");
  } else if (seconds < 172800) {
    text = "1 day";
  } else if (seconds < 30 * 86400) {
    text = unit(Math.floor(seconds / 86400), "day", "days");
  } else if (seconds < 365 * 86400) {
    text = unit(Math.floor(seconds / (30 * 86400)), "month", "months");
  } else {
    text = unit(Math.floor(seconds / (365 * 86400)), "year", "years");
  }
  return future ? "in " + text : text + " ago";
}

/**
 * Describes the sync status for the top bar pill.
 * @param {{online?: boolean, syncing?: boolean, pending?: number, lastSyncAt?: any, lastError?: any, authRequired?: boolean} | null} status
 * @param {number} [now]
 * @returns {{state: string, text: string, title: string}} state is synced,
 *   syncing, pending, offline, auth or error.
 */
export function syncLabel(status, now = Date.now()) {
  if (!status) {
    return { state: "offline", text: "Offline", title: "Sync is not running" };
  }
  const pending = status.pending || 0;
  const waiting = pending === 1 ? "1 change waiting" : pending + " changes waiting";
  let state;
  let text;
  if (status.authRequired) {
    state = "auth";
    text = "Sign in to sync";
  } else if (status.online === false) {
    state = "offline";
    text = pending ? "Offline, " + waiting : "Offline";
  } else if (status.syncing) {
    state = "syncing";
    text = "Syncing";
  } else if (pending) {
    state = status.lastError ? "error" : "pending";
    text = waiting.charAt(0).toUpperCase() + waiting.slice(1);
  } else if (status.lastError && !status.lastSyncAt) {
    state = "error";
    text = "Sync failed";
  } else {
    state = "synced";
    text = "Synced";
  }
  let title = status.lastSyncAt ? "Last synced " + timeAgo(status.lastSyncAt, now) : "Not synced yet";
  if (status.lastError) {
    const message = typeof status.lastError === "string" ? status.lastError : status.lastError.message;
    if (message) {
      title += ". Last error: " + message;
    }
  }
  return { state, text, title };
}

/**
 * Counts the words of a markdown text.
 * @param {string} text
 * @returns {number}
 */
export function wordCount(text) {
  const words = String(text || "").replace(/[#>*_`~\[\]()|-]+/g, " ").match(/\S+/g);
  return words ? words.length : 0;
}

/**
 * Moves a list selection by delta, wrapping at both ends.
 * @param {number} index current index, -1 for none.
 * @param {number} delta
 * @param {number} length
 * @returns {number} -1 when the list is empty.
 */
export function moveIndex(index, delta, length) {
  if (length <= 0) {
    return -1;
  }
  if (index < 0) {
    return delta > 0 ? 0 : length - 1;
  }
  return (((index + delta) % length) + length) % length;
}

/* ---------- Markdown helpers ---------- */

const TASK_PATTERN = /^(\s*(?:>\s*)*(?:[-*+]|\d+[.)])\s+\[)([ xX])(\])/;
const FENCE_PATTERN = /^\s*(```|~~~)/;

/**
 * Sets the checked state of the n-th task list item in markdown source,
 * counting only items outside fenced code blocks.
 * @param {string} md
 * @param {number} index zero-based task index, in document order.
 * @param {boolean} checked
 * @returns {string} the new source, unchanged when no such task exists.
 */
export function toggleTask(md, index, checked) {
  const lines = String(md || "").split("\n");
  let fence = null;
  let count = 0;
  for (let i = 0; i < lines.length; i++) {
    const fenceMatch = FENCE_PATTERN.exec(lines[i]);
    if (fenceMatch) {
      if (fence === null) {
        fence = fenceMatch[1];
      } else if (fenceMatch[1] === fence) {
        fence = null;
      }
      continue;
    }
    if (fence !== null) {
      continue;
    }
    const match = TASK_PATTERN.exec(lines[i]);
    if (!match) {
      continue;
    }
    if (count === index) {
      lines[i] = match[1] + (checked ? "x" : " ") + match[3] + lines[i].slice(match[0].length);
      return lines.join("\n");
    }
    count++;
  }
  return String(md || "");
}

/**
 * Builds a synchronous wikilink resolver for the markdown renderer. A target
 * matches by title, then slug, then path, case-insensitively, and may carry a
 * "#heading" suffix.
 * @param {object[]} notes live notes of the space.
 * @param {string} slug space slug.
 * @returns {(target: string) => {href: string, exists: boolean}}
 */
export function makeLinkResolver(notes, slug) {
  const byTitle = new Map();
  const bySlug = new Map();
  const byPath = new Map();
  for (const note of notes || []) {
    if (!note || note.deleted) {
      continue;
    }
    const title = String(note.title || "").trim().toLowerCase();
    if (title && !byTitle.has(title)) {
      byTitle.set(title, note);
    }
    if (note.slug && !bySlug.has(note.slug.toLowerCase())) {
      bySlug.set(note.slug.toLowerCase(), note);
    }
    if (note.path) {
      byPath.set(stripMd(note.path).toLowerCase(), note);
    }
  }
  return (target) => {
    const raw = String(target || "");
    const hashAt = raw.indexOf("#");
    const name = (hashAt >= 0 ? raw.slice(0, hashAt) : raw).trim().toLowerCase();
    const hash = hashAt >= 0 ? raw.slice(hashAt) : "";
    const note = byTitle.get(name) || bySlug.get(name) || byPath.get(stripMd(name).replace(/^\/+/, ""));
    if (!note) {
      return { href: "#", exists: false };
    }
    return { href: noteHref(note, slug) + hash, exists: true };
  };
}

/**
 * Lists the attachment links a note body references, both uploaded files
 * under /files/ and uploads still waiting on this device.
 * @param {string} md
 * @returns {{url: string, name: string, pending: boolean}[]}
 */
export function attachmentLinks(md) {
  const out = [];
  const seen = new Set();
  const pattern = /\[([^\]]*)\]\(((?:\/files\/|markspace-pending:)[^)\s]+)\)/g;
  let match;
  while ((match = pattern.exec(String(md || "")))) {
    const url = match[2];
    if (seen.has(url)) {
      continue;
    }
    seen.add(url);
    const pending = url.startsWith("markspace-pending:");
    let name = match[1];
    if (!name) {
      const last = url.split("/").pop();
      name = decodeSegment(last) || last;
    }
    out.push({ url, name, pending });
  }
  return out;
}

/* ---------- DOM helpers shared with views.js ---------- */

/* h and icon come from ui.js. views.js imports them from here too. */
export { h, icon };

/**
 * Builds a Primer Avatar with the user's initials and colour.
 * @param {{name?: string, email?: string, avatarColor?: string} | null} user
 * @param {number} [size] pixels.
 * @returns {HTMLElement}
 */
export function avatar(user, size = 20) {
  return uiAvatar({ name: (user && (user.name || user.email)) || "", color: (user && user.avatarColor) || "", size, alt: "" });
}

/**
 * Returns up to two uppercase initials for a user, matching core.User.
 * @param {{name?: string, email?: string} | null} user
 * @returns {string}
 */
export function initialsOf(user) {
  const source = String((user && (user.name || "").trim()) || (user && user.email) || "");
  const parts = source.split(/[ .\-_@]+/).filter(Boolean);
  const letters = parts.slice(0, 2).map((part) => part[0].toUpperCase());
  return letters.join("") || "?";
}

/** Reports whether an event target is a text field or editable region. */
export function isEditableTarget(target) {
  if (!target || !target.closest) {
    return false;
  }
  return !!target.closest("input, textarea, select, [contenteditable=''], [contenteditable='true']");
}

/** The Octicon of each sync state on the Avatar badge. Syncing and pending show a dot. */
export const SYNC_BADGE_ICONS = { synced: "check", offline: "alert", error: "alert", auth: "alert", recovered: "alert" };

/**
 * Returns the Avatar badge state (docs/primer.md 5.7). Recovered changes show the
 * warning state in place of synced, syncing and pending. Offline, error and
 * signed out keep their own state.
 * @param {string} state from syncLabel
 * @param {number} recovered the number of recovered changes
 * @returns {string}
 */
export function badgeState(state, recovered) {
  return recovered > 0 && (state === "synced" || state === "syncing" || state === "pending") ? "recovered" : state;
}

/**
 * Names the Avatar button, such as "Account, All changes synced, 2 recovered
 * changes".
 * @returns {string}
 */
export function accountLabel(label, status, recovered) {
  const parts = ["Account", syncDescription(label, status)];
  if (recovered > 0) {
    parts.push(recovered === 1 ? "1 recovered change" : recovered + " recovered changes");
  }
  return parts.join(", ");
}

/**
 * Returns the account menu item for recovered changes, with its count, or
 * null while there are none.
 * @param {number} count
 * @param {Function} onClick
 * @returns {object | null}
 */
export function recoveredMenuItem(count, onClick) {
  if (!(count > 0)) {
    return null;
  }
  return { label: "Recovered changes", icon: "alert", count, onClick };
}

/** The theme choices of the account menu, in order (docs/primer.md 2.3). */
export const THEME_CHOICES = Object.freeze([
  { value: "light", label: "Light theme", icon: "sun" },
  { value: "dark", label: "Dark theme", icon: "moon" },
  { value: "system", label: "Use system theme", icon: "device-desktop" },
]);

/**
 * Returns the stored theme preference: "light", "dark" or "system".
 * @param {{getItem(key: string): string | null}} storage
 * @returns {"light" | "dark" | "system"}
 */
export function themePreference(storage) {
  let stored = null;
  try {
    stored = storage.getItem(THEME_KEY);
  } catch (err) {
    stored = null;
  }
  return stored === "light" || stored === "dark" ? stored : "system";
}

/**
 * Stores a theme preference and applies it to the root element. The system
 * theme removes the key and sets data-color-mode "auto", which follows the
 * system through CSS.
 * @returns {"light" | "dark" | "system"}
 */
export function saveThemePreference(pref, storage, root) {
  const value = pref === "light" || pref === "dark" ? pref : "system";
  try {
    if (value === "system") {
      storage.removeItem(THEME_KEY);
    } else {
      storage.setItem(THEME_KEY, value);
    }
  } catch (err) {
    /* storage is full or disabled */
  }
  root.setAttribute("data-color-mode", value === "system" ? "auto" : value);
  return value;
}

/**
 * Builds the theme SegmentedControl of the account menu. The segments are
 * icon-only with Tooltips, and each is a menuitemradio item. A choice applies
 * at once and the menu stays open.
 * @param {"light" | "dark" | "system"} pref
 * @param {(value: string) => void} onChange
 * @returns {{element: HTMLElement, control: HTMLElement, buttons: HTMLElement[]}}
 */
export function themeSwitch(pref, onChange) {
  const control = segmentedControl({
    label: "Theme",
    itemRole: "menuitemradio",
    items: THEME_CHOICES.map((choice) => ({ label: choice.label, value: choice.value, icon: choice.icon, iconOnly: true, selected: choice.value === pref })),
    onChange,
  });
  const element = h("div", { class: "theme-switch" }, h("span", { class: "theme-switch-label", "aria-hidden": "true", text: "Theme" }), control);
  return { element, control, buttons: Array.from(control.querySelectorAll("button")) };
}

/**
 * Describes the sync state for the Sync now item and the Avatar button name
 * (docs/primer.md 5.7), such as "All changes synced" or "3 not synced".
 * @param {{state: string, text: string}} label from syncLabel
 * @param {{pending?: number} | null} status
 * @returns {string}
 */
export function syncDescription(label, status) {
  const pending = (status && status.pending) || 0;
  const changes = pending === 1 ? "1 change" : pending + " changes";
  switch (label.state) {
    case "synced":
      return "All changes synced";
    case "syncing":
      return pending ? "Syncing " + changes : "Syncing";
    case "pending":
      return pending + " not synced";
    case "offline":
      return pending ? "Offline, " + pending + " not synced" : "Offline";
    case "auth":
      return "Signed out";
    default:
      return label.text;
  }
}

/**
 * Measures the part of the layout viewport that is on screen. The on-screen
 * keyboard shrinks the visual viewport. A pinch zoom also shrinks it, and
 * then the layout viewport height is kept, so zooming does not resize panes.
 * @param {{innerHeight: number, visualViewport?: {height: number, offsetTop: number, scale?: number} | null}} win
 * @returns {{height: number, top: number}} CSS pixels, rounded
 */
export function visibleViewport(win) {
  const vv = win.visualViewport;
  if (!vv || !(vv.height > 0) || (vv.scale && vv.scale > 1.01)) {
    return { height: Math.round(win.innerHeight), top: 0 };
  }
  return { height: Math.round(Math.min(vv.height, win.innerHeight)), top: Math.max(0, Math.round(vv.offsetTop || 0)) };
}

/**
 * Returns the part of a sticky pane's layout that is off screen: the space
 * above the layout top and below its bottom edge. A sticky pane is that much
 * shorter than the viewport, so it can scroll to its last control.
 * @param {{top: number, bottom: number}} rect the layout box
 * @param {number} viewportHeight
 * @returns {number} whole pixels
 */
export function paneOffset(rect, viewportHeight) {
  return Math.round(Math.max(0, rect.top) + Math.max(0, viewportHeight - rect.bottom));
}

/**
 * Maps a command toast kind to a Banner variant (docs/primer.md 6.4).
 * @param {string} kind info, success, warn or error.
 * @returns {"info" | "success" | "warning" | "critical"}
 */
export function toastVariant(kind) {
  return { error: "critical", warn: "warning", warning: "warning", success: "success" }[kind] || "info";
}

/**
 * Filters and orders commands for the quick switcher. A query that starts
 * with ">" is command mode, which shows every command that matches the rest.
 * Without ">" a command shows only when its title matches.
 * @param {{id: string, title: string}[]} commands
 * @param {string} query
 * @returns {{commandMode: boolean, text: string, commands: object[]}}
 */
export function matchCommands(commands, query) {
  const raw = String(query || "").trimStart();
  const commandMode = raw.startsWith(">");
  const text = (commandMode ? raw.slice(1) : raw).trim().toLowerCase();
  const list = (commands || []).filter((cmd) => cmd && cmd.id && cmd.title);
  if (!commandMode && !text) {
    return { commandMode, text, commands: [] };
  }
  const words = text.split(/\s+/).filter(Boolean);
  const hits = list.filter((cmd) => {
    const title = cmd.title.toLowerCase();
    return words.every((word) => title.includes(word));
  });
  hits.sort((a, b) => Number(!a.title.toLowerCase().startsWith(text)) - Number(!b.title.toLowerCase().startsWith(text)) || a.title.localeCompare(b.title));
  return { commandMode, text, commands: hits };
}

/**
 * Picks the rail tab ids to show from contributions. Built-in panels keep
 * the order the server gives. A built-in panel the server leaves out is
 * hidden. Plugin panels, which have no builtin name, come last in order.
 * @param {{id: string, where?: string, builtin?: string, order?: number}[] | null} panels
 * @param {string[]} builtinIds the ids the client can render.
 * @returns {{builtin: string[], plugin: object[]} | null} null keeps the client defaults.
 */
export function railFromContributions(panels, builtinIds) {
  if (!Array.isArray(panels)) {
    return null;
  }
  const rail = panels.filter((p) => p && p.where === "rail").slice().sort((a, b) => (a.order || 0) - (b.order || 0));
  if (!rail.length) {
    return null;
  }
  const builtin = rail.filter((p) => p.builtin && builtinIds.includes(p.builtin)).map((p) => p.builtin);
  const plugin = rail.filter((p) => !p.builtin);
  return { builtin, plugin };
}

/* ---------- Browser application ---------- */

const SIDEBAR_KEY = "markspace.sidebar";
const RAIL_KEY = "markspace.rail";
const RAIL_REGULAR_KEY = "markspace.railRegular";
const RAIL_TAB_KEY = "markspace.railTab";
const AI_KEY = "markspace.ai";
const WIDE_QUERY = "(min-width: 87.5rem)";
const CONTRIBUTIONS_TTL = 60000;

/** The panels.js export that mounts each lazy rail panel. */
const RAIL_PANEL_EXPORTS = { history: "openHistory", share: "openShare", files: "openNoteFiles", ai: "openAI", suggestions: "openSuggestions" };
const THEME_KEY = "markspace.theme";
const LAST_SPACE_KEY = "markspace.lastSpace";

/** Reads localStorage without throwing. */
function storageGet(key) {
  try {
    return window.localStorage.getItem(key);
  } catch (err) {
    return null;
  }
}

/** Writes or removes a localStorage key without throwing. */
function storageSet(key, value) {
  try {
    if (value === null) {
      window.localStorage.removeItem(key);
    } else {
      window.localStorage.setItem(key, value);
    }
  } catch (err) {
    /* storage is full or disabled */
  }
}

/** Returns a local storage adapter that never throws. */
function safeStorage() {
  return {
    getItem: storageGet,
    setItem: (key, value) => storageSet(key, value),
    removeItem: (key) => storageSet(key, null),
  };
}

/** Looks up a function export and logs a clear error when it is missing. */
function need(mod, name, file) {
  const fn = mod && mod[name];
  if (typeof fn !== "function") {
    console.error(`[markspace] /static/js/${file} does not export ${name}() yet. See docs/offline.md.`);
    return null;
  }
  return fn;
}

/** Loads every browser module, keeping going when one fails. */
async function loadModules() {
  const names = ["session", "store", "sync", "api", "markdown", "editor", "panels", "views", "anchors", "filetree", "filecache", "filepanels"];
  // Without anchors.js comments still list, only without passage highlights.
  const optional = new Set(["anchors", "filecache", "filepanels"]);
  const results = await Promise.allSettled(names.map((name) => import(`./${name}.js`)));
  const mods = {};
  results.forEach((result, i) => {
    if (result.status === "fulfilled") {
      mods[names[i]] = result.value;
    } else if (optional.has(names[i])) {
      console.warn(`[markspace] /static/js/${names[i]}.js is not available`, result.reason);
      mods[names[i]] = {};
    } else {
      console.error(`[markspace] /static/js/${names[i]}.js failed to load`, result.reason);
      mods[names[i]] = {};
    }
  });
  return mods;
}

/**
 * Loads the plugin UI renderer. Package 10 adds the module, so a missing
 * module returns null and the caller shows a Blankslate.
 * @returns {Promise<{renderPluginUI: Function} | null>}
 */
let pluginRenderer = null;
async function loadPluginRenderer() {
  if (!pluginRenderer) {
    pluginRenderer = import("/static/js/plugins.js").catch((err) => {
      console.warn("[markspace] /static/js/plugins.js is not available", err);
      pluginRenderer = null;
      return null;
    });
  }
  const mod = await pluginRenderer;
  return mod && typeof mod.renderPluginUI === "function" ? mod : null;
}

/** Returns a short random key for a history entry. */
function newKey() {
  return Math.random().toString(36).slice(2, 10);
}

/** Runs fn once, wait milliseconds after the last call. */
function debounce(fn, wait) {
  let timer = null;
  return (...args) => {
    clearTimeout(timer);
    timer = setTimeout(() => fn(...args), wait);
  };
}

/** Returns the platform name of the command modifier key. */
function modifierLabel() {
  return typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform || "") ? "⌘" : "Ctrl";
}

/** Builds a Blankslate that is the only content of a page, with an h1 for focus. */
export function pageBlankslate(options) {
  const el = blankslate(Object.assign({ size: "spacious" }, options));
  const heading = el.querySelector(".blankslate-heading");
  if (heading) {
    const h1 = h("h1", { class: heading.className, tabindex: "-1", text: heading.textContent });
    heading.replaceWith(h1);
  }
  return el;
}

/**
 * The running application: layout, router, app header, sidebar, rail and the
 * shared overlays. Views in views.js receive it as their context.
 */
class App {
  constructor(root, { user, store, sync, mods, offline, fileCache = null }) {
    this.root = root;
    this.user = user;
    this.store = store;
    this.sync = sync;
    this.mods = mods;
    this.views = mods.views;
    this.startedOffline = !!offline;
    this.spaces = [];
    this.currentSpace = null;
    this.currentNoteId = null;
    this.view = null;
    this.route = null;
    this.routeToken = 0;
    this.scrollByKey = new Map();
    this.installable = false;
    this.menu = null;
    this.drawer = null;
    this.railDialog = null;
    this.railNode = null;
    this.railTitleText = "Page details";
    this.railPanels = new Map();
    this.railCounts = new Map();
    this.fileCache = fileCache;
    this.fileTree = null;
    this.clipboard = null;
    this.pendingRename = null;
    this.pendingPanel = null;
    this.focusAfterRefresh = null;
    this.bannerEls = new Map();
    this.contribCache = new Map();
    this.syncKey = "";
    this.reconcileFiles = debounce(() => {
      if (this.fileCache && this.isOnline()) {
        this.fileCache.reconcile().catch((err) => console.error("[markspace] offline files could not update", err));
      }
    }, 2000);
    const savedAI = storageGet(AI_KEY);
    this.aiState = savedAI === "on" || savedAI === "off" ? savedAI : "unknown";
    this.narrowQuery = window.matchMedia(NARROW_QUERY);
    this.wideQuery = window.matchMedia(WIDE_QUERY);
    this.api = typeof mods.api.api === "function" ? mods.api.api : null;
    this.upload = typeof mods.api.upload === "function" ? mods.api.upload : null;
    this.pendingIds = new Set();
    this.flushChanges = debounce(() => this.applyChanges(), 120);
    this.comments = null;
    this.syncWasBusy = false;
    this.reloadComments = debounce(() => {
      if (this.comments) {
        this.comments.source.load();
      }
    }, 300);
  }

  /* ----- Startup ----- */

  /** Draws the layout, loads the spaces and renders the first route. */
  async mount() {
    this.buildLayout();
    this.bindGlobalEvents();
    this.subscribeSync();
    this.refreshRecovered();
    this.startEvents();
    this.applyLayoutState();
    await this.loadSpaces();
    this.probeAI();
    this.renderSyncStatus(this.syncStatus());
    if (typeof this.sync.uploads === "function") {
      this.renderUploads(this.sync.uploads());
    }
    const state = Object.assign({}, history.state || {}, { key: (history.state && history.state.key) || newKey() });
    history.replaceState(state, "", location.pathname + location.search + location.hash);
    await this.renderRoute({ initial: true });
  }

  /**
   * Builds the frame: the static app header, then a split page layout with
   * the sidebar pane, the content and the rail pane (docs/primer.md 5.1). The
   * document scrolls. From 768 px the panes are sticky and scroll inside
   * themselves.
   */
  buildLayout() {
    const mod = modifierLabel();
    const instance = document.body.dataset.instanceName || "Markspace";

    this.sidebarToggle = iconButton({
      icon: "sidebar-collapse",
      label: "Toggle sidebar",
      keybindingHint: mod + " \\",
      className: "app-header-sidebar-toggle ms-regular-only",
      attrs: { "aria-controls": "sidebar", "aria-expanded": "true", "aria-keyshortcuts": "Control+\\ Meta+\\" },
      onClick: () => this.toggleSidebar(),
    });
    this.navButton = iconButton({
      icon: "three-bars",
      label: "Open navigation",
      size: "large",
      className: "ms-narrow-only",
      attrs: { "aria-haspopup": "dialog", "aria-expanded": "false" },
      onClick: () => this.openDrawer(),
    });
    this.contextInstance = h("a", { class: "app-header-instance", href: "/" }, h("span", { class: "app-header-context-text", text: instance }));
    this.contextSep = h("span", { class: "app-header-separator", "aria-hidden": "true" });
    this.contextSpaceText = h("span", { class: "app-header-context-text" });
    this.contextSpace = h("a", { class: "app-header-space", href: "/", hidden: true }, this.contextSpaceText);
    const context = h("div", { class: "app-header-context" }, this.contextInstance, this.contextSep, this.contextSpace);

    this.searchButton = button({
      label: "Search",
      leadingVisual: "search",
      keybindingHint: mod + " K",
      className: "app-header-search ms-regular-only",
      attrs: { "aria-haspopup": "dialog", "aria-keyshortcuts": "Control+K Meta+K" },
      onClick: () => this.openSwitcher(),
    });
    this.searchIcon = iconButton({
      icon: "search",
      label: "Search",
      size: "large",
      tooltip: false,
      className: "ms-narrow-only",
      attrs: { "aria-haspopup": "dialog" },
      onClick: () => this.openSwitcher(),
    });
    this.uploadButton = button({ label: "Uploads", variant: "invisible", size: "small", leadingVisual: "upload", count: 0, className: "app-header-uploads", attrs: { "aria-haspopup": "dialog" }, onClick: () => this.openUploads() });
    this.uploadButton.hidden = true;
    this.newButton = button({ label: "New page", variant: "primary", leadingVisual: "plus", className: "ms-regular-only", onClick: () => this.newNoteFromTopbar() });
    this.newIcon = iconButton({ icon: "plus", label: "New page", variant: "primary", size: "large", tooltip: false, className: "ms-narrow-only", onClick: () => this.newNoteFromTopbar() });
    this.railToggle = iconButton({
      icon: "sidebar-expand",
      label: "Page details",
      className: "app-header-rail-toggle ms-regular-only is-mirrored",
      attrs: { "aria-controls": "rail", "aria-expanded": "false" },
      onClick: () => this.toggleRail(),
    });
    this.railToggle.hidden = true;
    this.userButton = h(
      "button",
      { class: "btn btn-invisible app-header-avatar", type: "button", "aria-haspopup": "true", "aria-expanded": "false", "aria-label": "Account menu", on: { click: () => this.openUserMenu() } },
      uiAvatar({ name: this.user.name || this.user.email || "", color: this.user.avatarColor || "", size: 32, alt: "" })
    );
    // The sync state shows as a badge on the Avatar: a tick when synced, a dot
    // while changes wait or sync, and an alert offline or after an error.
    this.syncBadge = h("span", { class: "app-header-sync-badge", "aria-hidden": "true" });
    this.userButton.appendChild(this.syncBadge);
    this.topbar = h(
      "header",
      { class: "app-header", id: "app-header" },
      h("div", { class: "app-header-start" }, this.sidebarToggle, this.navButton, context),
      h("div", { class: "app-header-end" }, this.searchButton, this.searchIcon, this.uploadButton, this.newButton, this.newIcon, this.railToggle, this.userButton)
    );

    this.sidebarInner = h("div", { class: "sidebar-inner", role: "region", "aria-label": "Sidebar" });
    this.sidebarEl = h("div", { class: "sidebar", id: "sidebar" }, this.sidebarInner);
    this.banners = h("div", { class: "banners", dataset: { bannerHost: "" } });
    this.main = h("main", { class: "main", id: "main", tabindex: "-1" });
    this.railInner = h("div", { class: "rail-inner" });
    this.railEl = h("aside", { class: "rail", id: "rail", "aria-label": "Page details", hidden: true }, this.railInner);
    const layout = h("div", { class: "app-layout" }, this.sidebarEl, h("div", { class: "app-content" }, this.banners, this.main), this.railEl);
    this.root.replaceChildren(h("div", { class: "app-frame" }, this.topbar, layout));
    this.watchOverflow(this.sidebarInner);
    this.watchOverflow(this.railInner);
    this.watchPaneHeight(layout);
    watchTableScroll(document);
  }

  /**
   * Sizes the sticky panes to the viewport minus the part of the layout that
   * is not on screen yet, as Primer's PageLayout does. A pane that starts
   * under the static app header can then scroll to its last control.
   * @param {HTMLElement} layout
   */
  watchPaneHeight(layout) {
    let frame = 0;
    const update = () => {
      frame = 0;
      const view = visibleViewport(window);
      const root = document.documentElement;
      root.style.setProperty("--visual-viewport-height", view.height + "px");
      root.style.setProperty("--visual-viewport-top", view.top + "px");
      const rect = layout.getBoundingClientRect();
      layout.style.setProperty("--sticky-pane-offset", paneOffset(rect, view.height) + "px");
    };
    const schedule = () => {
      if (!frame) {
        frame = requestAnimationFrame(update);
      }
    };
    window.addEventListener("scroll", schedule, { passive: true });
    window.addEventListener("resize", schedule);
    // The on-screen keyboard shrinks the visual viewport without a window
    // resize in browsers that ignore interactive-widget, such as Safari.
    if (window.visualViewport) {
      window.visualViewport.addEventListener("resize", schedule);
      window.visualViewport.addEventListener("scroll", schedule);
    }
    if (typeof ResizeObserver === "function") {
      new ResizeObserver(schedule).observe(layout);
    }
    update();
  }

  /** Gives a pane that overflows a tab stop, so keyboard users can scroll it (docs/primer.md 5.1). */
  watchOverflow(pane) {
    if (typeof ResizeObserver !== "function") {
      return;
    }
    const check = () => {
      const scrolls = pane.scrollHeight > pane.clientHeight + 1 && getComputedStyle(pane).overflowY !== "visible";
      if (scrolls) {
        pane.setAttribute("tabindex", "0");
      } else {
        pane.removeAttribute("tabindex");
      }
    };
    const observer = new ResizeObserver(check);
    observer.observe(pane);
    new MutationObserver(check).observe(pane, { childList: true, subtree: true, attributes: true, attributeFilter: ["hidden", "aria-expanded"] });
  }

  /** Wires document-wide events: links, history, keys and lifecycle. */
  bindGlobalEvents() {
    document.addEventListener("click", (event) => this.onDocumentClick(event));
    window.addEventListener("popstate", () => this.renderRoute({ popstate: true }));
    document.addEventListener("keydown", (event) => this.onKeydown(event));
    window.addEventListener("pagehide", () => this.flushView());
    document.addEventListener("visibilitychange", () => {
      if (document.visibilityState === "hidden") {
        this.flushView();
      }
    });
    const onRange = () => {
      this.closeDrawer({ restore: false });
      this.closeRailDialog({ restore: false });
      this.applyLayoutState();
    };
    this.narrowQuery.addEventListener("change", onRange);
    this.wideQuery.addEventListener("change", onRange);
    window.addEventListener("online", () => {
      this.contribCache.clear();
      this.renderSyncStatus(this.syncStatus());
      this.probeAI();
    });
    window.addEventListener("offline", () => this.renderSyncStatus(this.syncStatus()));
    document.addEventListener("markspace:installable", () => {
      this.installable = true;
    });
    document.addEventListener("markspace:update-available", () => this.showUpdateBanner());
    if (window.markspace && window.markspace.updateAvailable) {
      this.showUpdateBanner();
    }
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    media.addEventListener("change", () => {
      if (!storageGet(THEME_KEY)) {
        this.applyTheme("system");
      }
    });
    // A theme chosen in another tab applies here too.
    window.addEventListener("storage", (event) => {
      if (event.key === THEME_KEY || event.key === null) {
        const pref = themePreference(safeStorage());
        document.documentElement.setAttribute("data-color-mode", pref === "system" ? "auto" : pref);
      }
    });
    setInterval(() => this.renderSyncStatus(this.syncStatus()), 30000);
  }

  /** Subscribes to the sync engine events. */
  subscribeSync() {
    if (!this.sync || typeof this.sync.on !== "function") {
      console.error("[markspace] the sync engine has no on() method. See docs/offline.md.");
      return;
    }
    this.sync.on("status", (status) => {
      this.renderSyncStatus(status);
      this.reloadCommentsAfterSync(status);
    });
    this.sync.on("notes-changed", (event) => {
      if (this.comments && event && event.source === "remote" && (event.ids || []).includes(this.comments.noteId)) {
        this.reloadComments();
      }
      for (const id of (event && event.ids) || []) {
        this.pendingIds.add(id);
      }
      this.pendingSource = event && event.source;
      this.flushChanges();
    });
    this.sync.on("notes-changed", (event) => {
      if (event && event.source === "remote") {
        this.reconcileFiles();
      }
    });
    this.sync.on("file-stored", () => this.reconcileFiles());
    this.sync.on("spaces-changed", () => this.onSpacesChanged());
    this.sync.on("conflict", (event) => this.showConflict(event));
    this.sync.on("rejected", (event) => this.showRejected(event));
    this.sync.on("recovered", (event) => this.showRecovered(event));
    this.sync.on("auth-required", () => this.showAuthRequired());
    this.sync.on("uploads-changed", (event) => this.renderUploads(event && event.uploads ? event.uploads : typeof this.sync.uploads === "function" ? this.sync.uploads() : []));
  }

  /**
   * Listens to the server event stream for plugin notifications. Each one is
   * announced and shown as a dismissible Banner (docs/primer.md 6.4).
   */
  startEvents() {
    const handlers = new Map();
    let source = null;
    const listen = (type) => {
      if (source && !source.markspaceTypes.has(type)) {
        source.markspaceTypes.add(type);
        source.addEventListener(type, (message) => {
          let data = message.data;
          try {
            data = JSON.parse(message.data);
          } catch (err) {
            /* pass the raw text */
          }
          for (const fn of Array.from(handlers.get(type) || [])) {
            try {
              fn(data, message);
            } catch (err) {
              console.error("[markspace] an event handler failed", type, err);
            }
          }
        });
      }
    };
    /** Subscribes to one event type of the stream. Returns the unsubscribe function. */
    this.events = {
      on: (type, fn) => {
        if (!handlers.has(type)) {
          handlers.set(type, new Set());
        }
        handlers.get(type).add(fn);
        listen(type);
        return () => handlers.get(type).delete(fn);
      },
    };
    this.events.on("plugin.notify", (data) => this.onPluginNotify(data));
    if (typeof EventSource !== "function") {
      return;
    }
    let delay = 2000;
    let timer = 0;
    const connect = () => {
      timer = 0;
      if (!navigator.onLine) {
        window.addEventListener("online", connect, { once: true });
        return;
      }
      source = new EventSource("/api/events", { withCredentials: true });
      source.markspaceTypes = new Set();
      for (const type of handlers.keys()) {
        listen(type);
      }
      source.addEventListener("open", () => {
        delay = 2000;
      });
      source.addEventListener("error", () => {
        source.close();
        source = null;
        if (!timer) {
          timer = setTimeout(connect, delay);
          delay = Math.min(delay * 2, 60000);
        }
      });
    };
    connect();
    window.addEventListener("pagehide", () => {
      if (source) {
        source.close();
      }
    });
  }

  /** Shows one plugin.notify event: the parsed event or its JSON text. */
  onPluginNotify(raw) {
    let ev = raw;
    if (typeof raw === "string") {
      try {
        ev = JSON.parse(raw);
      } catch (err) {
        return;
      }
    }
    const data = (ev && ev.data) || {};
    const message = String(data.message || "").trim();
    if (!message) {
      return;
    }
    const link = typeof data.link === "string" && data.link ? data.link : "";
    // A Banner of higher severity keeps its place, so the message is also announced.
    announce(String(data.pluginName || "Plugin") + ": " + message);
    this.showBanner("plugin-" + (ev.id || Date.now()), {
      variant: "info",
      title: String(data.pluginName || data.pluginId || "Plugin"),
      description: message,
      dismissible: true,
      actions: link ? [{ label: "Open", href: link }] : [],
    });
  }

  /* ----- Data access ----- */

  /** Returns the sync status, or null when the engine cannot report one. */
  syncStatus() {
    try {
      return this.sync && typeof this.sync.status === "function" ? this.sync.status() : null;
    } catch (err) {
      return null;
    }
  }

  /** Reports whether the browser and the sync engine see a connection. */
  isOnline() {
    const status = this.syncStatus();
    return navigator.onLine && !(status && status.online === false);
  }

  /** Reloads the space list from the store. */
  async loadSpaces() {
    try {
      this.spaces = (await this.store.listSpaces()) || [];
    } catch (err) {
      console.error("[markspace] could not list spaces", err);
      this.spaces = [];
    }
  }

  /** Finds a space by slug or id in the loaded list. */
  spaceBy(idOrSlug) {
    return this.spaces.find((space) => space.slug === idOrSlug || space.id === idOrSlug) || null;
  }

  /** Returns the space new notes go into: the current one, or the last used. */
  defaultSpace() {
    const editable = (space) => space && accessAtLeast(spaceAccess(space, this.user), "edit");
    if (editable(this.currentSpace)) {
      return this.currentSpace;
    }
    const last = this.spaceBy(storageGet(LAST_SPACE_KEY) || "");
    if (editable(last)) {
      return last;
    }
    return this.spaces.find(editable) || null;
  }

  /** Access to a note for the signed-in user. */
  accessFor(note) {
    return noteAccess(note, note ? this.spaceBy(note.spaceId) : null, this.user);
  }

  /**
   * Renders markdown into an element. It uses markdown.js and falls back to
   * plain paragraphs when the renderer is missing.
   * @returns {{toc: object[], tasks: number}}
   */
  renderMarkdown(el, md, options) {
    const render = this.mods.markdown && this.mods.markdown.render;
    if (typeof render === "function") {
      try {
        const out = render(md || "", options) || {};
        el.innerHTML = out.html || "";
        return out;
      } catch (err) {
        console.error("[markspace] markdown render failed", err);
      }
    } else if (!this.warnedRender) {
      this.warnedRender = true;
      console.error("[markspace] /static/js/markdown.js does not export render() yet. See docs/offline.md.");
    }
    el.replaceChildren(...String(md || "").split(/\n{2,}/).map((block) => h("p", { text: block })));
    return { toc: [], tasks: 0, links: [] };
  }

  /**
   * Returns the contributions for a context from GET /api/ui/contributions.
   * Answers are kept for a minute. Offline, or on a failure, it returns null.
   * @param {{kind?: string, itemId?: string, spaceId?: string}} [context]
   * @returns {Promise<{commands: object[], panels: object[]} | null>}
   */
  contributions({ kind = "", itemId = "", spaceId = "" } = {}) {
    if (!this.api || !this.isOnline()) {
      return Promise.resolve(null);
    }
    const key = [kind, itemId, spaceId].join("|");
    const cached = this.contribCache.get(key);
    if (cached && Date.now() - cached.at < CONTRIBUTIONS_TTL) {
      return cached.promise;
    }
    const query = new URLSearchParams();
    if (itemId) {
      query.set("itemId", itemId);
      if (kind) {
        query.set("kind", kind);
      }
    }
    if (spaceId) {
      query.set("spaceId", spaceId);
    }
    const promise = this.api("GET", "/api/ui/contributions" + (query.toString() ? "?" + query.toString() : "")).catch((err) => {
      console.warn("[markspace] contributions could not load", err);
      this.contribCache.delete(key);
      return null;
    });
    this.contribCache.set(key, { at: Date.now(), promise });
    return promise;
  }

  /** Returns the command context of the current view: {spaceId, itemId, kind}. */
  commandContext() {
    const view = this.view || {};
    const itemId = view.noteId || view.itemId || "";
    const context = { spaceId: (this.currentSpace && this.currentSpace.id) || "" };
    if (itemId) {
      context.itemId = itemId;
      context.kind = view.kind || (view.noteId ? "note" : "");
    }
    return context;
  }

  /* ----- Router ----- */

  /**
   * Routes to a local URL. A URL outside the shell routes navigates fully.
   * @param {string} url
   * @param {{replace?: boolean, state?: object}} [options]
   */
  async navigate(url, { replace = false, state = {} } = {}) {
    const target = new URL(url, location.origin);
    if (target.origin !== location.origin || !parseRoute(target.pathname, target.search)) {
      await this.flushView();
      location.assign(target.href);
      return;
    }
    this.saveScroll();
    const next = Object.assign({}, state, { key: newKey() });
    const path = target.pathname + target.search + target.hash;
    if (replace) {
      history.replaceState(next, "", path);
    } else {
      history.pushState(next, "", path);
    }
    await this.renderRoute({});
    if (target.hash) {
      this.scrollToId(decodeSegment(target.hash.slice(1)) || "");
    }
  }

  /** Remembers the document scroll position of the current history entry. */
  saveScroll() {
    const key = history.state && history.state.key;
    if (key) {
      this.scrollByKey.set(key, window.scrollY);
    }
  }

  /** Writes pending editor keystrokes to the store. */
  async flushView() {
    if (this.view && typeof this.view.flush === "function") {
      try {
        await this.view.flush();
      } catch (err) {
        console.error("[markspace] flush before leaving failed", err);
      }
    }
  }

  /**
   * Renders the route in the address bar. The new view is built off screen
   * and swapped in, so the old one stays visible until the new one is ready.
   * @param {{initial?: boolean, popstate?: boolean, refresh?: boolean}} options
   */
  async renderRoute({ initial = false, popstate = false, refresh = false } = {}) {
    const token = ++this.routeToken;
    const route = parseRoute(location.pathname, location.search);
    if (!route) {
      location.reload();
      return;
    }
    if (!refresh) {
      this.closeOverlays();
      await this.flushView();
    }
    const container = h("div", { class: "view" });
    let view;
    try {
      if (!this.views || typeof this.views.renderView !== "function") {
        throw new Error("/static/js/views.js does not export renderView()");
      }
      view = (await this.views.renderView(this, route, container, { state: history.state || {}, refresh })) || {};
    } catch (err) {
      console.error("[markspace] view failed", err);
      container.replaceChildren(
        h(
          "div",
          { class: "page" },
          pageBlankslate({
            icon: "alert",
            title: "This page could not be shown",
            description: String((err && err.message) || err),
            primaryAction: { label: "Go home", href: "/" },
          })
        )
      );
      view = { title: "Error" };
    }
    if (token !== this.routeToken) {
      if (typeof view.destroy === "function") {
        view.destroy();
      }
      return;
    }
    const previous = this.view;
    const keepScroll = window.scrollY;
    if (previous && typeof previous.destroy === "function") {
      try {
        previous.destroy();
      } catch (err) {
        console.error("[markspace] view cleanup failed", err);
      }
    }
    if (!view.kind) {
      view.kind = { note: "note", permalink: "note", folder: "folder", file: "file" }[route.name] || "";
    }
    this.main.replaceChildren(container);
    this.view = view;
    this.route = route;
    // Phones drop the header context in landscape while the editor owns the screen.
    if (view.editing) {
      document.documentElement.dataset.editing = "true";
    } else {
      delete document.documentElement.dataset.editing;
    }
    if (typeof view.mounted === "function") {
      try {
        view.mounted();
      } catch (err) {
        console.error("[markspace] view mount failed", err);
      }
    }
    const instance = document.body.dataset.instanceName || "Markspace";
    document.title = view.title ? view.title + " · " + instance : instance;
    await this.setContext(view.space || null, view.itemId || view.noteId || null);
    this.setRail(view.rail || null, { title: view.railTitle, closed: !!view.railClosed });
    this.railToggle.hidden = !(view.editing && view.rail);
    if (this.comments && this.comments.noteId !== (view.noteId || null)) {
      this.disposeComments();
    }
    this.disposeRailPanels(view.noteId || null);
    this.applyRailContributions(view);
    this.updateNewButton();
    if (refresh) {
      window.scrollTo(0, keepScroll);
      const focusTarget = typeof this.focusAfterRefresh === "function" ? this.focusAfterRefresh(container) : null;
      this.focusAfterRefresh = null;
      if (focusTarget && typeof focusTarget.focus === "function") {
        focusTarget.focus({ preventScroll: false });
      }
    } else if (popstate) {
      window.scrollTo(0, this.scrollByKey.get(history.state && history.state.key) || 0);
    } else if (!initial) {
      window.scrollTo(0, 0);
    }
    if (!initial && !refresh && !view.ownsFocus) {
      const heading = container.querySelector("h1");
      const target = view.focus || heading || this.main;
      if (target === heading && !heading.hasAttribute("tabindex")) {
        heading.setAttribute("tabindex", "-1");
      }
      target.focus({ preventScroll: true });
    }
  }

  /** Re-renders the current route in place after a data change. */
  refreshView() {
    if (!this.view || this.view.live === false) {
      return;
    }
    return this.renderRoute({ refresh: true });
  }

  /** Scrolls the document to an element id, such as a heading. */
  scrollToId(id) {
    if (!id) {
      return;
    }
    const target = document.getElementById(id);
    if (target && this.main.contains(target)) {
      target.scrollIntoView({ block: "start", behavior: prefersReducedMotion() ? "auto" : "smooth" });
    }
  }

  /* ----- Global events ----- */

  /** Routes same-origin shell links inside the page. */
  onDocumentClick(event) {
    const anchor = event.target.closest && event.target.closest("a[href]");
    if (!anchor) {
      return;
    }
    const raw = anchor.getAttribute("href") || "";
    if (raw.startsWith("#") && this.main.contains(anchor)) {
      event.preventDefault();
      this.scrollToId(decodeSegment(raw.slice(1)) || "");
      return;
    }
    const url = interceptTarget(
      event,
      {
        href: anchor.href,
        target: anchor.getAttribute("target") || "",
        download: anchor.hasAttribute("download"),
        fullNav: anchor.hasAttribute("data-full-nav"),
      },
      location.origin
    );
    if (!url) {
      return;
    }
    event.preventDefault();
    const state = anchor.dataset.noteId ? { noteId: anchor.dataset.noteId } : {};
    this.navigate(url, { state });
  }

  /** Keyboard shortcuts: Ctrl+K, Ctrl+Shift+K, Ctrl+\ and E. */
  onKeydown(event) {
    const mod = event.ctrlKey || event.metaKey;
    if (mod && !event.altKey && (event.key === "k" || event.key === "K")) {
      if (event.defaultPrevented) {
        return;
      }
      event.preventDefault();
      this.openSwitcher({ commandMode: event.shiftKey });
      return;
    }
    if (mod && event.key === "\\") {
      event.preventDefault();
      this.toggleSidebar();
      return;
    }
    if (
      (event.key === "e" || event.key === "E") &&
      !mod &&
      !event.altKey &&
      !event.shiftKey &&
      !event.defaultPrevented &&
      !isEditableTarget(event.target) &&
      !document.querySelector("dialog[open]") &&
      this.view &&
      typeof this.view.onEdit === "function"
    ) {
      event.preventDefault();
      this.view.onEdit();
    }
  }

  /* ----- Sidebar and rail ----- */

  /** Reports whether the rail pane is open on regular and wide viewports. */
  railPaneOpen() {
    const root = document.documentElement;
    if (!this.railNode || this.narrowQuery.matches || root.dataset.immersive === "true") {
      return false;
    }
    if (root.dataset.railTransient) {
      return root.dataset.railTransient === "open";
    }
    return this.wideQuery.matches ? storageGet(RAIL_KEY) !== "collapsed" : storageGet(RAIL_REGULAR_KEY) === "open";
  }

  /**
   * Applies the pane rules of docs/primer.md 5.1. Wide viewports show both panes.
   * Regular viewports show at most two columns, so an open rail hides the
   * sidebar pane. Narrow viewports show no panes.
   */
  applyLayoutState() {
    const root = document.documentElement;
    const narrow = this.narrowQuery.matches;
    const immersive = root.dataset.immersive === "true";
    const railOpen = this.railPaneOpen();
    const sidebarPane = !narrow && !immersive && root.dataset.sidebar !== "collapsed" && (this.wideQuery.matches || !railOpen);
    root.dataset.railOpen = railOpen ? "true" : "false";
    root.dataset.sidebarPane = sidebarPane ? "shown" : "hidden";
    this.sidebarEl.inert = !sidebarPane;
    const expanded = sidebarPane || !!this.drawer;
    this.sidebarToggle.setAttribute("aria-expanded", expanded ? "true" : "false");
    const glyph = this.sidebarToggle.querySelector(".btn-visual");
    if (glyph) {
      glyph.replaceChildren(icon(expanded ? "sidebar-collapse" : "sidebar-expand", 16));
    }
    this.navButton.setAttribute("aria-expanded", this.drawer ? "true" : "false");
    this.railEl.inert = !railOpen;
    const railShown = railOpen || !!this.railDialog;
    this.railToggle.setAttribute("aria-expanded", railShown ? "true" : "false");
    for (const toggle of document.querySelectorAll("[data-rail-toggle]")) {
      toggle.setAttribute("aria-expanded", railShown ? "true" : "false");
    }
  }

  /**
   * Toggles the sidebar. On narrow viewports, and on regular viewports while
   * the rail hides the pane, it opens the navigation Dialog instead.
   */
  toggleSidebar() {
    const root = document.documentElement;
    if (this.narrowQuery.matches || (!this.wideQuery.matches && this.railPaneOpen())) {
      if (this.drawer) {
        this.closeDrawer();
      } else {
        this.openDrawer();
      }
      return;
    }
    const collapse = root.dataset.sidebar !== "collapsed";
    if (collapse) {
      root.dataset.sidebar = "collapsed";
    } else {
      delete root.dataset.sidebar;
    }
    storageSet(SIDEBAR_KEY, collapse ? "collapsed" : null);
    this.applyLayoutState();
  }

  /**
   * Opens the sidebar as a left side sheet Dialog titled "Navigation"
   * (docs/primer.md 5.1). The sidebar content moves into the Dialog and back.
   */
  openDrawer() {
    if (this.drawer) {
      return this.drawer;
    }
    const current = this.sidebarInner.querySelector("a[aria-current='page']") || this.sidebarInner.querySelector("[role=treeitem][aria-current='true']");
    this.drawer = openDialog({
      title: "Navigation",
      position: "left",
      className: "nav-drawer",
      content: this.sidebarInner,
      initialFocus: current || null,
      onClose: () => {
        this.drawer = null;
        this.sidebarEl.appendChild(this.sidebarInner);
        this.applyLayoutState();
      },
    });
    this.applyLayoutState();
    return this.drawer;
  }

  /**
   * Closes the navigation Dialog.
   * @param {{restore?: boolean}} [options] restore false is for callers that move focus themselves.
   */
  closeDrawer({ restore = true } = {}) {
    if (!this.drawer) {
      return;
    }
    const handle = this.drawer;
    handle.close(restore ? "close" : "navigate");
  }

  /** Toggles the rail pane, or the rail Dialog on narrow viewports. */
  toggleRail() {
    if (this.narrowQuery.matches) {
      if (this.railDialog) {
        this.closeRailDialog();
      } else {
        this.openRailDialog();
      }
      return;
    }
    this.setRailPane(!this.railPaneOpen());
  }

  /** Opens or closes the rail pane and stores the choice per device. */
  setRailPane(open) {
    const root = document.documentElement;
    if (root.dataset.railTransient) {
      root.dataset.railTransient = open ? "open" : "closed";
    } else if (this.wideQuery.matches) {
      storageSet(RAIL_KEY, open ? null : "collapsed");
    } else {
      storageSet(RAIL_REGULAR_KEY, open ? "open" : null);
    }
    this.applyLayoutState();
  }

  /**
   * Opens the rail: the pane, or a full-screen Dialog on narrow viewports.
   * With focus false the keyboard focus stays where it is, for callers that
   * focus something in the rail.
   * @param {{focus?: boolean}} [options]
   */
  showRail({ focus = true } = {}) {
    if (!this.railNode) {
      return;
    }
    if (this.narrowQuery.matches) {
      this.openRailDialog({ focus });
      return;
    }
    if (!this.railPaneOpen()) {
      this.setRailPane(true);
    }
    if (focus) {
      const tab = this.railInner.querySelector("[role=tab][aria-selected='true']") || focusables(this.railInner)[0];
      if (tab) {
        tab.focus();
      }
    }
  }

  /**
   * Opens the rail as a full-screen Dialog titled "Page details". The tab
   * strip sits directly under the Dialog header and names the selected tab,
   * so the title does not repeat it.
   * @param {{focus?: boolean}} [options]
   */
  openRailDialog({ focus = true } = {}) {
    if (this.railDialog || !this.railNode) {
      return;
    }
    const tab = this.railInner.querySelector("[role=tab][aria-selected='true']");
    this.railDialog = openDialog({
      title: "Page details",
      width: "xlarge",
      narrow: "fullscreen",
      className: "rail-dialog",
      content: this.railInner,
      initialFocus: focus ? tab : null,
      onClose: () => {
        this.railDialog = null;
        this.railEl.appendChild(this.railInner);
        this.applyLayoutState();
      },
    });
    this.applyLayoutState();
  }

  /** Closes the rail Dialog. */
  closeRailDialog({ restore = true } = {}) {
    if (this.railDialog) {
      this.railDialog.close(restore ? "close" : "navigate");
    }
  }

  /**
   * Names the selected rail tab. The rail Dialog title follows it.
   * @param {string} label
   */
  setRailTitle(label) {
    this.railTitleText = label || "Page details";
  }

  /** Closes the navigation and rail Dialogs and any open menu. */
  closeOverlays() {
    this.closeMenu();
    this.closeDrawer({ restore: false });
    this.closeRailDialog({ restore: false });
    if (this.switcher) {
      this.switcher.handle.close("navigate");
    }
  }

  /**
   * Replaces the rail content, or hides the rail when node is null. With
   * closed the rail starts closed and its open state is not remembered, for
   * the editor, which opens it when a panel is asked for.
   * @param {Node | null} node
   * @param {{title?: string, closed?: boolean}} [options]
   */
  setRail(node, { title = "", closed = false } = {}) {
    const root = document.documentElement;
    this.railNode = node;
    this.railEl.hidden = !node;
    this.railInner.replaceChildren(...(node ? [node] : []));
    this.railEl.setAttribute("aria-label", title || "Page details");
    const selected = node && node.querySelector ? node.querySelector("[role=tab][aria-selected='true']") : null;
    this.setRailTitle(selected ? selected.dataset.label || selected.textContent.trim() : title);
    if (node && closed) {
      root.dataset.railTransient = "closed";
    } else {
      delete root.dataset.railTransient;
    }
    if (!node) {
      this.closeRailDialog({ restore: false });
    }
    this.applyLayoutState();
  }

  /**
   * Hides or shows both panes, for the editor's focus mode. The app header
   * stays and scrolls away with the page (docs/primer.md 6.9).
   * @param {{immersive: boolean}} options
   */
  setChrome({ immersive }) {
    const root = document.documentElement;
    if (immersive) {
      root.dataset.immersive = "true";
      this.closeMenu();
      this.closeDrawer({ restore: false });
    } else {
      delete root.dataset.immersive;
    }
    this.applyLayoutState();
  }

  /** Shows the instance name and the space name in the header context region. */
  setHeaderContext(space) {
    this.contextSpace.hidden = !space;
    this.contextSep.hidden = !space;
    if (space) {
      this.contextSpace.href = spaceUrl(space.slug);
      this.contextSpaceText.textContent = space.name;
    }
    this.topbar.classList.toggle("has-space", !!space);
  }

  /** Updates the current space and note, redrawing the sidebar when needed. */
  async setContext(space, noteId) {
    const spaceChanged = (space && space.id) !== (this.currentSpace && this.currentSpace.id);
    if (space) {
      this.currentSpace = space;
      storageSet(LAST_SPACE_KEY, space.slug);
    } else if (!this.currentSpace) {
      this.currentSpace = this.defaultSpace() || this.spaces[0] || null;
    }
    this.setHeaderContext(space || null);
    const noteChanged = noteId !== this.currentNoteId;
    this.currentNoteId = noteId;
    if (spaceChanged || !this.sidebarBuilt) {
      await this.renderSidebar();
    } else {
      this.markSidebarCurrent();
      if (noteChanged) {
        await this.renderTree();
      }
    }
  }

  /**
   * Shows the header New page only when a space accepts new pages and the
   * PageHeader of the view has no New page action of its own, so the page
   * never shows two.
   */
  updateNewButton() {
    const space = this.defaultSpace();
    const own = this.main ? Array.from(this.main.querySelectorAll(".page-header-actions [data-new-page], .page-header-actions a, .page-header-actions button")).some((el) => el.hasAttribute("data-new-page") || /^New (page|note)$/.test(el.textContent.trim())) : false;
    this.newButton.hidden = !space || own;
    this.newIcon.hidden = !space || own;
  }

  /* ----- Sidebar ----- */

  /** Builds a NavList: a nav named by its h2, and a list of links (docs/primer.md 5.1). */
  navList(id, heading, links, { hideHeading = true, className = "", navId = null } = {}) {
    return h(
      "nav",
      { class: ("nav-list " + className).trim(), "aria-labelledby": id, id: navId, tabindex: navId ? "-1" : null },
      h("h2", { class: hideHeading ? "sr-only" : "nav-list-heading", id, text: heading }),
      h(
        "ul",
        { class: "nav-list-items" },
        links.map((link) =>
          h(
            "li",
            {},
            h(
              "a",
              { class: "nav-list-item", href: link.href, dataset: link.route ? { route: link.route } : {}, "data-full-nav": link.fullNav ? "" : null },
              link.icon ? h("span", { class: "nav-list-visual", "aria-hidden": "true" }, typeof link.icon === "string" ? icon(link.icon, 16) : link.icon) : null,
              h("span", { class: "nav-list-label", text: link.label })
            )
          )
        )
      )
    );
  }

  /** Builds the whole sidebar for the current space. */
  async renderSidebar() {
    this.sidebarBuilt = true;
    const space = this.currentSpace;
    const scroll = this.sidebarInner.scrollTop;
    const parts = [];

    if (space) {
      const glyph = space.icon
        ? h("span", { class: "avatar avatar-square space-emoji", style: { "--avatar-size": "20px" }, "aria-hidden": "true", text: space.icon })
        : uiAvatar({ name: space.name, size: 20, square: true, alt: "" });
      const switcher = button({
        label: space.name,
        variant: "invisible",
        block: true,
        leadingVisual: glyph,
        trailingAction: "triangle-down",
        className: "space-switcher",
        attrs: { "aria-haspopup": "dialog", "aria-label": "Space: " + space.name },
        onClick: () => this.openSpaceMenu(switcher),
      });
      parts.push(h("div", { class: "sidebar-switcher" }, switcher));
    }

    parts.push(
      this.navList("nav-main-heading", "Main", [
        { href: "/starred", label: "Starred", icon: "star", route: "starred" },
        { href: "/recent", label: "Recent", icon: "clock", route: "recent" },
        { href: "/shared", label: "Shared with me", icon: "people", route: "shared" },
      ])
    );

    this.treeEl = h("div", { class: "tree-host", id: "page-tree" });
    if (this.fileTree) {
      this.fileTree.destroy();
      this.fileTree = null;
    }
    if (space) {
      const canEdit = accessAtLeast(spaceAccess(space, this.user), "edit");
      const tree = this.mods.filetree || {};
      let selectButton = null;
      if (typeof tree.createFileTree === "function") {
        this.fileTree = tree.createFileTree({
          app: this,
          host: this.treeEl,
          space,
          labelledBy: "pages-heading",
          onExpandedChange: (expanded) => {
            writeExpanded(safeStorage(), space.id, expanded);
            this.expanded = expanded;
          },
          onSelectingChange: (on) => {
            if (selectButton) {
              selectButton.hidden = on;
            }
          },
        });
        selectButton = button({ label: "Select", variant: "invisible", size: "small", className: "ms-narrow-only tree-select-toggle", onClick: () => this.fileTree && this.fileTree.setSelecting(true) });
      }
      const addButton = canEdit
        ? iconButton({
            icon: "plus",
            label: "Add to " + space.name,
            size: "small",
            attrs: { "aria-haspopup": "true" },
            onClick: (event) => {
              this.menu = openMenu(event.currentTarget, [
                { label: "New page", icon: "note", onClick: () => this.createNote({ space }) },
                { label: "New folder", icon: "file-directory-fill", onClick: () => tree.newFolderIn(this, space, "", "tree") },
                { label: "Upload files", icon: "upload", onClick: async () => tree.uploadInto(this, space, "", await tree.chooseFiles()) },
              ]);
            },
          })
        : null;
      parts.push(
        h(
          "section",
          { class: "sidebar-tree", "aria-labelledby": "pages-heading" },
          h("div", { class: "sidebar-section-head" }, h("h2", { class: "sidebar-section-title", id: "pages-heading", text: "Pages" }), h("div", { class: "sidebar-section-actions" }, selectButton, addButton)),
          this.treeEl
        )
      );
      parts.push(
        this.navList(
          "nav-space-heading",
          space.name,
          [
            { href: filesUrl(space.slug), label: "All files", icon: "file", route: "files" },
            { href: trashUrl(space.slug), label: "Trash", icon: "trash", route: "trash" },
            { href: spaceUrl(space.slug) + "/settings", label: "Space settings", icon: "gear" },
          ],
          { className: "sidebar-footer", navId: "sidebar-footer" }
        )
      );
    } else {
      parts.push(
        this.navList(
          "nav-spaces-heading",
          "Spaces",
          [...this.spaces.map((s) => ({ href: spaceUrl(s.slug), label: s.name, icon: "file-directory-fill" })), { href: "/spaces", label: "Manage spaces", icon: "gear" }],
          { hideHeading: false }
        )
      );
    }
    this.sidebarInner.replaceChildren(...parts);
    this.sidebarInner.id = "sidebar-content";
    this.markSidebarCurrent();
    await this.renderTree();
    this.sidebarInner.scrollTop = scroll;
    this.applyLayoutState();
  }

  /** Marks the sidebar link of the current route. */
  markSidebarCurrent() {
    const name = this.route ? this.route.name : "";
    const spaceSlug = this.currentSpace && this.currentSpace.slug;
    for (const anchor of this.sidebarInner.querySelectorAll("a.nav-list-item")) {
      const on = anchor.dataset.route ? anchor.dataset.route === name : name === "space" && anchor.getAttribute("href") === spaceUrl(spaceSlug || "");
      if (on) {
        anchor.setAttribute("aria-current", "page");
      } else {
        anchor.removeAttribute("aria-current");
      }
    }
  }

  /** Draws the tree of folders, notes and files of the current space. */
  async renderTree() {
    const space = this.currentSpace;
    if (!space || !this.treeEl || !this.fileTree) {
      return;
    }
    this.treeStale = false;
    let nodes = [];
    try {
      nodes = (await this.store.tree(space.id)) || [];
    } catch (err) {
      console.error("[markspace] could not load the tree", err);
    }
    if (!this.currentSpace || this.currentSpace.id !== space.id || !this.fileTree) {
      return;
    }
    const byId = new Map();
    const walk = (list) => {
      for (const node of list) {
        const rec = node && (node.folder || node.note || node.file);
        if (!rec) {
          continue;
        }
        byId.set(rec.id, rec);
        if (node.page) {
          byId.set(node.page.id, node.page);
        }
        walk(node.children || []);
      }
    };
    walk(nodes);
    this.treeById = byId;
    this.treeNodes = nodes;
    let expanded = readExpanded(safeStorage(), space.id);
    if (this.currentNoteId && byId.has(this.currentNoteId)) {
      expanded = expandToNote(expanded, this.currentNoteId, byId);
    }
    this.expanded = expanded;
    const scroll = this.sidebarInner.scrollTop;
    this.fileTree.render(nodes, { expanded, currentId: this.currentNoteId });
    this.sidebarInner.scrollTop = scroll;
  }

  /** Opens a folder in the tree and remembers it. */
  expandFolder(id) {
    if (!this.currentSpace) {
      return;
    }
    const stored = toggleExpanded(readExpanded(safeStorage(), this.currentSpace.id), id, true);
    writeExpanded(safeStorage(), this.currentSpace.id, stored);
    if (this.fileTree) {
      this.fileTree.expand(id);
    }
  }

  /* ----- Items ----- */

  /**
   * Returns the view URL of an item.
   * @param {"folder" | "note" | "file" | "space"} kind
   * @param {object} rec
   * @returns {string}
   */
  hrefFor(kind, rec) {
    if (!rec) {
      return "/";
    }
    if (kind === "space") {
      return spaceUrl(rec.slug);
    }
    const space = rec.spaceId ? this.spaceBy(rec.spaceId) : null;
    const slug = (space && space.slug) || rec.spaceSlug || (this.currentSpace && this.currentSpace.slug) || "";
    if (kind === "note") {
      return noteHref(rec, slug);
    }
    if (kind === "folder") {
      return folderUrl(slug, rec.path);
    }
    return fileViewUrl(slug, rec.path);
  }

  /**
   * Returns the access the user holds on an item, or on a space for "space".
   * @param {string} kind
   * @param {object} rec
   * @returns {string}
   */
  itemAccess(kind, rec) {
    if (!rec) {
      return "view";
    }
    if (kind === "space") {
      return spaceAccess(rec, this.user);
    }
    if (kind === "note") {
      return this.accessFor(rec);
    }
    if (this.user && this.user.isAdmin) {
      return "manage";
    }
    if (rec.access) {
      return rec.access;
    }
    const space = this.spaceBy(rec.spaceId);
    return space ? spaceAccess(space, this.user) : "view";
  }

  /**
   * Opens the Move to or Copy to Dialog with a TreeView folder picker.
   * @param {"move" | "copy"} mode
   * @param {{kind: string, rec: object}[]} targets
   */
  async openMoveDialog(mode, targets) {
    const list = (targets || []).filter(Boolean);
    const tree = this.mods.filetree || {};
    if (!list.length || typeof tree.openMoveDialog !== "function") {
      return null;
    }
    this.closeMenu();
    const space = this.spaceBy(list[0].rec.spaceId) || this.currentSpace;
    if (!space) {
      return null;
    }
    let nodes = this.currentSpace && this.currentSpace.id === space.id ? this.treeNodes : null;
    if (!nodes) {
      nodes = (await this.store.tree(space.id).catch(() => [])) || [];
    }
    return tree.openMoveDialog(this, { mode, targets: list, space, nodes });
  }

  /** Reports whether the page tree is on screen: in the pane or in the navigation Dialog. */
  treeShown() {
    return !!this.drawer || document.documentElement.dataset.sidebarPane === "shown";
  }

  /**
   * Renames an item in place: in the tree when it shows, else in the view.
   * @param {string} kind
   * @param {object} rec
   * @param {"tree" | "view"} [where] where the request came from
   */
  renameItem(kind, rec, where = "tree") {
    if (where === "view" && this.view && typeof this.view.renameItem === "function") {
      const edit = this.view.renameItem(rec.id);
      if (edit) {
        return edit;
      }
    }
    if (this.treeShown() && this.fileTree) {
      this.fileTree.revealItem(rec.id);
      const edit = this.fileTree.rename(rec.id);
      if (edit) {
        return edit;
      }
    }
    if (this.view && typeof this.view.renameItem === "function") {
      const edit = this.view.renameItem(rec.id);
      if (edit) {
        return edit;
      }
    }
    if (this.fileTree) {
      if (this.narrowQuery.matches || (!this.wideQuery.matches && this.railPaneOpen())) {
        this.openDrawer();
      } else if (document.documentElement.dataset.sidebar === "collapsed") {
        this.toggleSidebar();
      }
      requestAnimationFrame(() => {
        this.fileTree.revealItem(rec.id);
        this.fileTree.rename(rec.id);
      });
      return null;
    }
    if (kind === "note") {
      this.navigate(this.hrefFor("note", rec) + "?edit=1", { state: { noteId: rec.id } });
    }
    return null;
  }

  /**
   * Opens an item and one of its rail tabs, such as Share.
   * @param {string} kind
   * @param {object} rec
   * @param {string} tab
   */
  async openItemPanel(kind, rec, tab) {
    const same = this.view && (this.view.itemId === rec.id || this.view.noteId === rec.id);
    this.closeDrawer({ restore: false });
    if (same && typeof this.view.showPanel === "function") {
      this.view.showPanel(tab);
      return;
    }
    await this.navigate(this.hrefFor(kind, rec), { state: kind === "note" ? { noteId: rec.id, panel: tab } : { itemId: rec.id, panel: tab } });
  }

  /**
   * Returns the function that opens a rail panel of a note.
   * @param {string} kind
   * @returns {Function | null}
   */
  railOpener(kind) {
    if (kind === "files") {
      const fp = this.mods.filepanels;
      return fp && typeof fp.openNoteFiles === "function" ? fp.openNoteFiles : null;
    }
    const panels = this.mods.panels || {};
    const name = RAIL_PANEL_EXPORTS[kind];
    return name && typeof panels[name] === "function" ? panels[name] : null;
  }

  /* ----- Note actions ----- */

  /**
   * Creates a note locally and opens it in the editor.
   * @param {{space?: object, parentId?: string, title?: string, content?: string}} options
   */
  async createNote({ space = this.defaultSpace(), parentId = "", title = "", content = "" } = {}) {
    if (!space) {
      this.showBanner("create", { variant: "warning", title: "There is no space you can write to", dismissible: true });
      return null;
    }
    if (typeof this.sync.createNote !== "function") {
      console.error("[markspace] the sync engine has no createNote(). See docs/offline.md.");
      this.showBanner("create", { variant: "critical", title: "Pages cannot be created yet" });
      return null;
    }
    try {
      const note = await this.sync.createNote({ spaceId: space.id, parentId, title: title || "Untitled", content, tags: [], icon: "" });
      if (parentId && this.currentSpace && this.currentSpace.id === space.id) {
        const stored = toggleExpanded(readExpanded(safeStorage(), space.id), parentId, true);
        writeExpanded(safeStorage(), space.id, stored);
      }
      await this.navigate(noteHref(note, space.slug, { edit: true }), { state: { noteId: note.id } });
      return note;
    } catch (err) {
      console.error("[markspace] create note failed", err);
      this.showBanner("create", { variant: "critical", title: "The page could not be created", description: (err && err.message) || "" });
      return null;
    }
  }

  /** Header New page: a top-level page in the current space. */
  newNoteFromTopbar() {
    this.createNote({ space: this.defaultSpace() });
  }

  /** Stars or unstars a note. The star button shows the result, so it is only announced. */
  async setStar(note, starred) {
    try {
      await this.sync.setStar(note.id, starred);
      announce(starred ? "Starred" : "Star removed");
    } catch (err) {
      this.showBanner("star", { variant: "critical", title: "The star could not be changed", description: (err && err.message) || "" });
    }
  }

  /** Copies the permalink of a note. */
  async copyLink(note) {
    const url = location.origin + permalinkUrl(note.id);
    try {
      await navigator.clipboard.writeText(url);
      announce("Link copied");
    } catch (err) {
      this.showBanner("copy", { variant: "warning", title: "The link could not be copied", description: url, dismissible: true });
    }
  }

  /** Creates a copy of a note next to it and opens it. */
  async duplicateNote(note) {
    const space = this.spaceBy(note.spaceId);
    try {
      const copy = await this.sync.createNote({
        spaceId: note.spaceId,
        parentId: note.parentId || "",
        title: (note.title || "Untitled") + " (copy)",
        content: note.content || "",
        tags: note.tags || [],
        icon: note.icon || "",
      });
      await this.navigate(noteHref(copy, space && space.slug), { state: { noteId: copy.id } });
    } catch (err) {
      this.showBanner("duplicate", { variant: "critical", title: "The page could not be duplicated", description: (err && err.message) || "" });
    }
  }

  /**
   * Moves a note to the trash at once and offers Undo in a Banner. Undo
   * restores the note and returns to it when the trash moved away.
   */
  async trashNote(note) {
    const space = this.spaceBy(note.spaceId);
    const title = note.title || "Untitled";
    const leaves = this.currentNoteId === note.id || !!(this.view && this.view.noteId && this.treeById && ancestorIds(this.view.noteId, this.treeById).includes(note.id));
    const back = leaves ? location.pathname + location.search : null;
    const backState = history.state && history.state.noteId ? { noteId: history.state.noteId } : {};
    return undoable({
      label: "Moved “" + title + "” to the trash",
      failLabel: "The page could not be moved to the trash.",
      run: async () => {
        await this.sync.trashNote(note.id);
        if (leaves) {
          await this.navigate(space ? spaceUrl(space.slug) : "/", { replace: true });
        }
      },
      undo: async () => {
        await this.sync.restoreNote(note.id);
        if (back) {
          await this.navigate(back, { state: backState });
        }
      },
    });
  }

  /** Renames a note in place, in the tree or in its PageHeader. */
  renameNote(note) {
    return this.renameItem("note", note, "tree");
  }

  /** Moves a note through the Move to Dialog. */
  startMove(note) {
    return this.openMoveDialog("move", [{ kind: "note", rec: note }]);
  }

  /**
   * Creates a space in a Dialog with a name field. The space is created on
   * the server, syncs and opens.
   */
  newSpace() {
    if (!this.isOnline() || !this.api) {
      this.showBanner("space", { variant: "warning", title: "Creating a space needs a connection", dismissible: true });
      return null;
    }
    const form = h("form", { novalidate: true, on: { submit: (event) => event.preventDefault() } }, formControl({ label: "Name", name: "name", required: true, autocomplete: "off" }));
    let created = null;
    const handle = openDialog({
      title: "New space",
      width: "small",
      content: form,
      footer: [
        { label: "Cancel" },
        {
          label: "Create space",
          variant: "primary",
          onClick: async () => {
            clearFieldErrors(form);
            const name = form.elements.name.value.trim();
            if (!name) {
              showFieldErrors(form, { name: "Enter a name" });
              return false;
            }
            const res = await this.api("POST", "/api/spaces", { name });
            created = (res && res.space) || res;
            return undefined;
          },
        },
      ],
      onClose: async () => {
        if (!created || !created.slug) {
          return;
        }
        try {
          if (typeof this.store.putSpaces === "function") {
            await this.store.putSpaces([Object.assign({ myRole: "owner" }, created)]);
          }
          await this.loadSpaces();
          if (typeof this.sync.syncNow === "function") {
            this.sync.syncNow().catch(() => {});
          }
        } catch (err) {
          console.error("[markspace] could not store the new space", err);
        }
        await this.navigate(spaceUrl(created.slug));
      },
    });
    form.addEventListener("submit", () => handle.buttons[1] && handle.buttons[1].click());
    return handle;
  }

  /* ----- Space and account menus ----- */

  /** Opens the space switcher SelectPanel. */
  openSpaceMenu(anchor) {
    this.menu = selectPanel(anchor, {
      title: "Switch space",
      items: this.spaces.map((space) => ({ id: space.id, label: space.name, description: space.description || "" })),
      selected: this.currentSpace ? this.currentSpace.id : null,
      filterLabel: "Filter spaces",
      placeholder: "Filter spaces",
      emptyTitle: "No spaces match",
      onSelect: (id) => {
        const space = this.spaceBy(id);
        if (space && (!this.currentSpace || space.id !== this.currentSpace.id || (this.route && this.route.name !== "space"))) {
          this.closeDrawer({ restore: false });
          this.navigate(spaceUrl(space.slug));
        }
      },
    });
  }

  /** Opens the account ActionMenu. */
  openUserMenu() {
    const pref = themePreference(safeStorage());
    const status = this.syncStatus();
    const label = syncLabel(status);
    const auth = label.state === "auth";
    const offline = label.state === "offline";
    const items = [this.user.name && this.user.email ? { group: this.user.name, description: this.user.email } : { group: this.user.name || this.user.email || "Account" }];
    // docs/primer.md 5.7: Sync now comes first, with the state as its description.
    items.push({
      label: auth ? "Sign in to sync" : "Sync now",
      icon: label.state === "syncing" ? spinner({ size: "small", label: "" }) : auth ? "person" : "sync",
      description: syncDescription(label, status),
      inactive: offline,
      inactiveText: offline ? "Syncing needs a connection" : "",
      onClick: offline ? undefined : () => this.syncNow(),
    });
    // Recovered changes join the sync group while any exist.
    const recovered = recoveredMenuItem(this.recoveredCount || 0, () => this.openRecovered());
    if (recovered) {
      items.push(recovered);
    }
    items.push({ separator: true });
    const theme = themeSwitch(pref, (value) => this.applyTheme(value));
    items.push({ custom: theme.element, focusables: theme.buttons });
    items.push({ separator: true });
    // Every item has a leading visual, so all labels share one inset (docs/primer.md 5.5).
    items.push({ label: "Settings", icon: "gear", href: "/settings/profile" });
    if (this.user.isAdmin) {
      items.push({ label: "Administration", icon: "key", href: "/admin" });
    }
    const markspace = window.markspace;
    if (markspace && typeof markspace.install === "function" && (this.installable || markspace.installable)) {
      items.push({ label: "Install app", icon: "download", onClick: () => markspace.install() });
    }
    items.push({ separator: true });
    items.push({ label: "Sign out", icon: "sign-out", danger: true, onClick: () => this.signOut() });
    this.menu = openMenu(this.userButton, items, { placement: "bottom-end", label: "Account" });
  }

  /**
   * Applies and stores a theme preference.
   * @param {"light" | "dark" | "system"} pref
   */
  applyTheme(pref) {
    // Primer themes follow data-color-mode. "auto" follows the system
    // through CSS, so system mode needs no media listener.
    saveThemePreference(pref, safeStorage(), document.documentElement);
  }

  /**
   * Signs out. When changes have not synced, a ConfirmationDialog says so
   * first, because signing out deletes them from this device.
   * @param {{force?: boolean}} [options]
   */
  async signOut({ force = false } = {}) {
    const signOut = need(this.mods.session, "signOut", "session.js");
    if (!signOut) {
      return;
    }
    let pending = 0;
    try {
      pending = typeof this.store.countOps === "function" ? await this.store.countOps() : (this.syncStatus() || {}).pending || 0;
    } catch (err) {
      pending = (this.syncStatus() || {}).pending || 0;
    }
    if (pending > 0 && force !== true) {
      const ok = await confirmDialog({
        title: "Sign out anyway?",
        body: (pending === 1 ? "1 change has" : pending + " changes have") + " not synced. Signing out deletes them from this device.",
        confirmLabel: "Sign out",
        returnFocus: this.userButton,
      });
      if (ok) {
        await this.signOut({ force: true });
      }
      return;
    }
    await this.flushView();
    try {
      if (typeof this.sync.stop === "function") {
        this.sync.stop();
      }
      if (this.fileCache && typeof this.fileCache.destroy === "function") {
        this.fileCache.destroy();
      }
      const fc = this.mods.filecache;
      if (fc && typeof fc.deleteFileCache === "function") {
        await fc.deleteFileCache(this.user.id).catch((err) => console.error("[markspace] offline files could not be removed", err));
      }
      await signOut();
    } catch (err) {
      console.error("[markspace] sign out failed", err);
      this.showBanner("signout", { variant: "critical", title: "Sign out failed", description: (err && err.message) || "" });
    }
  }

  /* ----- Sync status and notices ----- */

  /**
   * Shows the sync state as the badge on the Avatar and in the name of the
   * account button (docs/primer.md 5.7). The account menu shows the details.
   */
  renderSyncStatus(status) {
    const label = syncLabel(status);
    if (status && !status.authRequired) {
      this.removeBanner("auth");
    }
    this.syncDetails = label;
    const recovered = this.recoveredCount || 0;
    const key = label.state + "|" + label.text + "|" + ((status && status.pending) || 0) + "|" + recovered;
    if (key === this.syncKey) {
      return;
    }
    this.syncKey = key;
    const state = badgeState(label.state, recovered);
    const glyph = SYNC_BADGE_ICONS[state] || "";
    this.syncBadge.dataset.state = state;
    this.syncBadge.replaceChildren(...(glyph ? [icon(glyph, 16)] : []));
    this.userButton.setAttribute("aria-label", accountLabel(label, status, recovered));
  }

  /**
   * Counts the recovered changes on this device. The count drives the
   * account menu item and the Avatar badge.
   * @returns {Promise<number>}
   */
  async refreshRecovered() {
    let count = this.recoveredCount || 0;
    if (this.store && typeof this.store.listRecovered === "function") {
      try {
        count = ((await this.store.listRecovered()) || []).length;
      } catch (err) {
        console.error("[markspace] recovered changes could not be counted", err);
      }
    }
    if (count !== (this.recoveredCount || 0)) {
      this.recoveredCount = count;
      this.renderSyncStatus(this.syncStatus());
    }
    return count;
  }

  /** Syncs now from the account menu, or goes to sign in when the session ended. */
  syncNow() {
    const label = syncLabel(this.syncStatus());
    if (label.state === "auth") {
      this.gotoLogin();
    } else if (this.sync && typeof this.sync.syncNow === "function") {
      announce("Syncing");
      this.sync.syncNow().catch((err) => console.error("[markspace] sync failed", err));
    }
  }

  /** Sends the user to the login form, returning here afterwards. */
  async gotoLogin() {
    await this.flushView();
    location.assign("/login?next=" + encodeURIComponent(location.pathname + location.search));
  }

  /** Applies a batch of note changes to the tree and the current view. */
  async applyChanges() {
    const ids = Array.from(this.pendingIds);
    const source = this.pendingSource;
    this.pendingIds.clear();
    await this.renderTree();
    if (!this.view) {
      return;
    }
    if (typeof this.view.refresh === "function") {
      await this.view.refresh({ ids, source });
    } else if (this.view.live !== false) {
      await this.refreshView();
    }
  }

  /** Reloads spaces and redraws the sidebar and view. */
  async onSpacesChanged() {
    await this.loadSpaces();
    this.contribCache.clear();
    if (this.currentSpace) {
      this.currentSpace = this.spaceBy(this.currentSpace.id);
    }
    // The first sync on a new device brings the spaces after the first render.
    if (!this.currentSpace) {
      this.currentSpace = this.defaultSpace() || this.spaces[0] || null;
    }
    await this.renderSidebar();
    this.updateNewButton();
    if (this.view && this.view.live !== false && typeof this.view.refresh !== "function") {
      await this.refreshView();
    } else if (this.view && typeof this.view.refresh === "function") {
      await this.view.refresh({ ids: [], source: "remote", spaces: true });
    }
  }

  /**
   * Shows a Banner at the top of the content. ui.js keeps one Banner on the
   * page and a newer Banner replaces an older one of the same or lower
   * severity.
   * @param {string} id
   * @param {{variant?: string, title: string, description?: string, actions?: object[], dismissible?: boolean}} options
   */
  showBanner(id, { variant = "info", title, description = "", actions = [], dismissible = false }) {
    this.removeBanner(id);
    const el = banner({
      variant,
      title,
      description,
      dismissible,
      into: this.banners,
      actions: actions.map((action) => ({
        label: action.label,
        href: action.href,
        primary: action.primary,
        onClick: action.onClick,
      })),
      onDismiss: () => this.bannerEls.delete(id),
    });
    this.bannerEls.set(id, el);
    return el;
  }

  /** Removes a Banner by id. */
  removeBanner(id) {
    const el = this.bannerEls.get(id);
    if (el) {
      el.remove();
      this.bannerEls.delete(id);
    }
  }

  /** Conflict notice: the local text was saved as a copy. */
  async showConflict({ noteId, copyId, kind = "note" } = {}) {
    if (kind === "file") {
      let original = null;
      let copy = null;
      try {
        original = await this.store.getFile(noteId);
        copy = await this.store.getFile(copyId);
      } catch (err) {
        /* show the notice without names */
      }
      const name = (original && original.name) || "A file";
      this.showBanner("conflict-" + noteId, {
        variant: "warning",
        title: "“" + name + "” changed on the server",
        description: "Your replacement is kept as “" + ((copy && copy.name) || "a conflicted copy") + "”.",
        dismissible: true,
        actions: copy ? [{ label: "Open copy", href: this.hrefFor("file", copy) }] : [],
      });
      return;
    }
    let original = null;
    let copy = null;
    try {
      original = await this.store.getNote(noteId);
      copy = await this.store.getNote(copyId);
    } catch (err) {
      /* show the notice without titles */
    }
    const title = (original && original.title) || "A page";
    const space = copy && this.spaceBy(copy.spaceId);
    this.showBanner("conflict-" + noteId, {
      variant: "warning",
      title: "“" + title + "” changed on the server while you were editing",
      description: "Your version is kept as a conflicted copy.",
      dismissible: true,
      actions: copy ? [{ label: "Open copy", href: noteHref(copy, space && space.slug) }] : [],
    });
  }

  /** Rejected notice: the server refused a change. */
  showRejected({ op, status, message } = {}) {
    const what = status === 403 ? "You no longer have permission for that change" : message || "The server did not accept a change";
    this.showBanner("rejected-" + ((op && op.id) || Date.now()), {
      variant: "warning",
      title: what,
      description: "The server version shows again.",
      dismissible: true,
    });
  }

  /**
   * Recovered notice: text kept on the device after a failed sync. One
   * Banner covers every new item, and the account menu keeps the entry point.
   */
  showRecovered() {
    this.refreshRecovered();
    this.showBanner("recovered", {
      variant: "warning",
      title: "Some changes could not be synced and were saved as recovered changes",
      dismissible: true,
      actions: [{ label: "Review", onClick: () => this.openRecovered() }],
    });
  }

  /** Session ended notice. The person must sign in, so it has no dismiss button. */
  showAuthRequired() {
    this.showBanner("auth", {
      variant: "critical",
      title: "Your session ended",
      description: "Changes stay on this device until you sign in again.",
      actions: [{ label: "Sign in", primary: true, onClick: () => this.gotoLogin() }],
    });
    this.renderSyncStatus(this.syncStatus());
  }

  /** New version notice from boot.js (docs/primer.md 6.4). */
  showUpdateBanner() {
    this.showBanner("update", {
      variant: "warning",
      title: "A newer version of " + (document.body.dataset.instanceName || "Markspace") + " is available",
      dismissible: true,
      actions: [
        {
          label: "Reload",
          onClick: async () => {
            await this.flushView();
            if (window.markspace && typeof window.markspace.update === "function") {
              window.markspace.update();
            } else {
              location.reload();
            }
          },
        },
      ],
    });
  }

  /** Lists recovered texts in a Dialog with actions to save, copy or discard them. */
  async openRecovered() {
    let items = [];
    try {
      items = (await this.store.listRecovered()) || [];
    } catch (err) {
      items = [];
    }
    const list = h("ul", { class: "box recovered-list" });
    let handle = null;
    const drawEmpty = () => list.replaceWith(blankslate({ icon: "check-circle", title: "Nothing to recover", description: "Changes that could not sync show here.", size: "small" }));
    for (const item of items) {
      const text = item.content !== undefined ? item.content : item.text || "";
      const title = item.title || "Untitled";
      const row = h("li", { class: "box-row recovered-row" });
      row.append(
        h(
          "div",
          { class: "list-row-main" },
          h("span", { class: "list-row-title", text: title }),
          h("pre", { class: "recovered-text", text: text.slice(0, 2000) })
        ),
        h(
          "div",
          { class: "list-row-trail recovered-actions" },
          button({
            label: "Save as page",
            size: "small",
            onClick: async () => {
              const space = this.spaceBy(item.spaceId) || this.defaultSpace();
              const note = await this.createNote({ space, title: item.title || "Recovered page", content: text });
              if (note) {
                await this.store.deleteRecovered(item.id);
                this.refreshRecovered();
                if (handle) {
                  handle.close();
                }
              }
            },
          }),
          button({
            label: "Copy text",
            size: "small",
            onClick: () =>
              navigator.clipboard.writeText(text).then(
                () => announce("Copied"),
                () => announce("The text could not be copied", { politeness: "assertive" })
              ),
          }),
          button({
            label: "Discard",
            size: "small",
            variant: "danger",
            onClick: async (event) => {
              const ok = await confirmDialog({
                title: "Discard these changes?",
                body: "The recovered text of “" + title + "” is deleted from this device.",
                confirmLabel: "Discard",
                returnFocus: event.currentTarget,
                onConfirm: async () => {
                  await this.store.deleteRecovered(item.id);
                  this.refreshRecovered();
                },
              });
              if (ok) {
                const next = row.nextElementSibling || row.previousElementSibling;
                row.remove();
                if (next) {
                  const target = focusables(next)[0];
                  if (target) {
                    target.focus();
                  }
                } else {
                  drawEmpty();
                }
              }
            },
          })
        )
      );
      list.appendChild(row);
    }
    const body = h("div", {}, list);
    if (!items.length) {
      drawEmpty();
    }
    handle = openDialog({ title: "Recovered changes", width: "xlarge", content: body, onClose: () => this.refreshRecovered() });
  }

  /* ----- Overlays: menus and panels ----- */

  /**
   * Opens an ActionMenu anchored to an element. Items may use onSelect or
   * onClick.
   * @param {Element} anchor
   * @param {object[]} items
   * @param {{align?: "start" | "end", point?: {x: number, y: number}, label?: string}} [options]
   */
  openMenu(anchor, items, { align = "end", point = null, label = "" } = {}) {
    const mapped = items.map((item) => (item.onSelect && !item.onClick ? Object.assign({}, item, { onClick: item.onSelect }) : item));
    this.menu = openMenu(anchor, mapped, { placement: align === "start" ? "bottom-start" : "bottom-end", point, label });
    return this.menu;
  }

  /** Closes the open menu or overlay. */
  closeMenu() {
    if (this.menu && typeof this.menu.close === "function") {
      this.menu.close({ restore: false });
    }
    this.menu = null;
  }

  /**
   * Opens a panel of the current view in its rail tab: comments, history,
   * share, files, ai or a plugin panel id. extra passes options such as an
   * AI task.
   * @param {string} kind
   * @param {object | null} [note] ignored unless it is the current note.
   * @param {object | null} [extra]
   */
  openPanel(kind, note = null, extra = null) {
    if (!this.view || typeof this.view.showPanel !== "function") {
      return;
    }
    if (note && this.view.noteId && note.id !== this.view.noteId) {
      return;
    }
    this.view.showPanel(kind, extra);
  }

  /* ----- Plugin commands and panels ----- */

  /**
   * Runs a contributed command with POST /api/commands/{id} and handles the
   * response.
   * @param {{id: string, title: string, module?: string}} command
   */
  async runCommand(command) {
    if (!this.api) {
      return;
    }
    const context = this.commandContext();
    announce("Running " + command.title);
    let resp;
    try {
      resp = await this.api("POST", "/api/commands/" + encodeURIComponent(command.id), { context });
    } catch (err) {
      this.showBanner("command", { variant: "critical", title: command.title + " failed", description: (err && err.message) || "" });
      return;
    }
    await this.handleCommandResponse(resp, { title: command.title, module: command.module || command.id.split(".")[0], context });
  }

  /**
   * Handles a command or action response (docs/plugins.md). toast becomes a
   * Banner, openPanel opens a rail tab, navigate routes, job reports the job
   * and ui opens a Dialog with the plugin UI, or updates the UI it came from.
   * @param {object} resp
   * @param {{title?: string, module?: string, context?: object, plugin?: {update: Function} | null, dialog?: object | null}} options
   */
  async handleCommandResponse(resp, { title = "", module = "", context = {}, plugin = null, dialog = null } = {}) {
    if (!resp || typeof resp !== "object") {
      return;
    }
    // A plugin view shows its own toast Banner through renderPluginUI.
    if (resp.toast && resp.toast.message && !plugin) {
      const variant = toastVariant(resp.toast.kind);
      const into = dialog && dialog.body ? dialog.body : this.banners;
      banner({ variant, title: String(resp.toast.message), dismissible: variant !== "critical", into });
    }
    if (resp.job && resp.job.id) {
      this.showBanner("job-" + resp.job.id, {
        variant: "info",
        title: (title ? title + " started" : "A job started"),
        description: "It runs in the background. The jobs page shows its progress.",
        dismissible: true,
        actions: [{ label: "View jobs", href: "/jobs" }],
      });
    }
    if (resp.ui) {
      if (plugin && typeof plugin.update === "function") {
        plugin.update(resp.ui);
      } else {
        this.openPluginDialog(title || "Plugin", resp.ui, { module, context });
      }
    }
    if (resp.openPanel && resp.openPanel.id) {
      if (dialog) {
        dialog.close("navigate");
      }
      await this.openPluginPanel(resp.openPanel);
    }
    if (typeof resp.navigate === "string" && resp.navigate) {
      if (dialog) {
        dialog.close("navigate");
      }
      await this.navigate(resp.navigate);
    }
  }

  /**
   * Opens a rail panel by id for an item: on this page when it is the item on
   * screen, or after routing to the item.
   * @param {{id: string, itemId?: string}} target
   */
  async openPluginPanel({ id, itemId = "" }) {
    const view = this.view || {};
    const here = !itemId || itemId === view.noteId || itemId === view.itemId;
    if (here && typeof view.showPanel === "function") {
      this.pendingPanel = id;
      view.showPanel(id);
      return;
    }
    await this.navigate(itemPermalinkUrl(itemId), { state: { itemId, panel: id } });
  }

  /**
   * Mounts a plugin UI tree with plugins.js. A missing renderer shows a
   * Blankslate. Actions post to POST /api/modules/{module}/actions/{id}.
   * @param {HTMLElement} host
   * @param {object} tree
   * @param {{module: string, context: object, title?: string, dialog?: object | null}} options
   * @returns {Promise<{update: Function, destroy: Function} | null>}
   */
  async mountPluginUI(host, tree, { module, context, title = "", dialog = null }) {
    const mod = await loadPluginRenderer();
    if (!mod) {
      host.replaceChildren(
        blankslate({
          icon: "alert",
          title: "This plugin view cannot be shown",
          description: "The plugin interface did not load. Reload the page to try again.",
          size: "small",
          border: true,
        })
      );
      return null;
    }
    let handle = null;
    try {
      handle = mod.renderPluginUI(host, tree, {
        context,
        onAction: async (actionId, payload, values) => {
          const resp = await this.api("POST", "/api/modules/" + encodeURIComponent(module) + "/actions/" + encodeURIComponent(actionId), { context, payload, values });
          await this.handleCommandResponse(resp, { title, module, context, plugin: handle, dialog });
          return resp;
        },
      });
    } catch (err) {
      console.error("[markspace] the plugin view failed to render", err);
      host.replaceChildren(blankslate({ icon: "alert", title: "This plugin view could not be shown", description: (err && err.message) || "", size: "small", border: true }));
      return null;
    }
    return handle;
  }

  /** Opens a Dialog that holds the UI tree of a command result. */
  openPluginDialog(title, tree, { module, context }) {
    const host = h("div", { class: "plugin-ui-host" });
    let plugin = null;
    const dialog = openDialog({
      title,
      width: "xlarge",
      narrow: "fullscreen",
      content: host,
      onClose: () => {
        if (plugin && typeof plugin.destroy === "function") {
          plugin.destroy();
        }
      },
    });
    this.mountPluginUI(host, tree, { module, context, title, dialog }).then((handle) => {
      plugin = handle;
    });
    return dialog;
  }

  /**
   * Adds the plugin rail panels from contributions to the rail of a view.
   * Built-in tabs keep the client rules. A panel asked for by a command or
   * by history state is selected once its tab exists.
   * @param {object} view
   */
  async applyRailContributions(view) {
    const tabs = view && view.rail && view.rail.railTabs;
    const context = this.commandContext();
    if (!tabs || !context.itemId) {
      return;
    }
    const wanted = this.pendingPanel || (history.state && history.state.panel) || "";
    const contrib = await this.contributions(context);
    if (this.view !== view || !contrib) {
      return;
    }
    const plan = railFromContributions(contrib.panels, tabs.builtinIds());
    if (plan && context.kind === "note" && plan.builtin.length) {
      // The server leaves out built-in panels whose module is off, such as AI.
      tabs.restrict(plan.builtin);
    }
    const panels = this.mods.panels || {};
    for (const panel of (plan && plan.plugin) || []) {
      const host = h("div", { class: "panel-host plugin-panel-host" });
      let loaded = false;
      let handle = null;
      tabs.addTab(
        { id: panel.id, label: panel.title || panel.id },
        {
          node: host,
          onSelect: () => {
            if (loaded) {
              return;
            }
            loaded = true;
            if (typeof panels.openPluginPanel === "function") {
              try {
                handle = panels.openPluginPanel(host, {
                  panelId: panel.id,
                  itemId: context.itemId,
                  kind: context.kind,
                  spaceId: context.spaceId,
                  title: panel.title || panel.id,
                  api: this.api,
                  store: this.store,
                  sync: this.sync,
                  user: this.user,
                  events: this.events,
                  isOnline: () => this.isOnline(),
                  openPanel: (id) => this.openPanel(id),
                  navigate: (url) => this.navigate(url),
                });
              } catch (err) {
                console.error("[markspace] the plugin panel failed to open", err);
                host.replaceChildren(blankslate({ icon: "alert", title: (panel.title || "This panel") + " could not open", size: "small", border: true }));
              }
            } else {
              this.loadPluginPanel(host, panel, context).then((mounted) => {
                handle = mounted;
              });
            }
          },
        }
      );
      const previous = view.destroy;
      view.destroy = () => {
        if (handle && typeof handle.unmount === "function") {
          handle.unmount();
        } else if (handle && typeof handle.destroy === "function") {
          handle.destroy();
        }
        if (typeof previous === "function") {
          previous.call(view);
        }
      };
    }
    if (wanted && tabs.has(wanted) && tabs.select(wanted)) {
      this.pendingPanel = null;
      this.showRail({ focus: false });
    }
  }

  /**
   * Loads a plugin rail panel with POST /api/panels/{id} and renders its tree.
   * @param {HTMLElement} host
   * @param {{id: string, module?: string, title?: string}} panel
   * @param {object} context
   * @returns {Promise<{update: Function, destroy: Function} | null>}
   */
  async loadPluginPanel(host, panel, context) {
    host.setAttribute("aria-busy", "true");
    host.replaceChildren(spinner({ size: "medium", label: "Loading " + (panel.title || "panel"), delay: 1000, className: "plugin-panel-spinner" }));
    try {
      if (!this.api || !this.isOnline()) {
        throw Object.assign(new Error(panel.title + " needs a connection"), { offline: true });
      }
      const resp = await this.api("POST", "/api/panels/" + encodeURIComponent(panel.id), { context });
      host.replaceChildren();
      return await this.mountPluginUI(host, (resp && resp.ui) || { type: "stack", children: [] }, { module: panel.module || panel.id.split(".")[0], context, title: panel.title });
    } catch (err) {
      host.replaceChildren(
        err && err.offline
          ? banner({ variant: "warning", title: err.message, hideTitle: false, layout: "compact" })
          : blankslate({
              icon: "alert",
              title: (panel.title || "This panel") + " could not be loaded",
              description: (err && err.message) || "",
              size: "small",
              border: true,
              primaryAction: { label: "Try again", onClick: () => this.loadPluginPanel(host, panel, context) },
            })
      );
      return null;
    } finally {
      host.removeAttribute("aria-busy");
    }
  }

  /* ----- Comments ----- */

  /**
   * Returns the comments panel of a note, creating it on first use. The panel
   * element and its comment source outlive re-renders of the same note, so a
   * draft survives a sync update. Views place entry.host in their rail.
   * @param {object} note
   * @param {string} access
   * @returns {{key: string, noteId: string, host: HTMLElement, source: object, handle: object, count: number} | null}
   */
  commentsFor(note, access) {
    const panels = this.mods.panels || {};
    if (!note || typeof panels.openComments !== "function" || typeof panels.createCommentSource !== "function") {
      return null;
    }
    const key = note.id + ":" + access + ":" + (note.deleted ? "trash" : "live");
    if (this.comments && this.comments.key === key) {
      return this.comments;
    }
    this.disposeComments();
    const host = h("div", { class: "comments-host" });
    const source = panels.createCommentSource({ noteId: note.id, api: this.api, store: this.store, isOnline: () => this.isOnline() });
    const entry = { key, noteId: note.id, host, source, handle: null, count: 0 };
    this.comments = entry;
    const viewHook = (name, ...args) => {
      if (this.comments === entry && this.view && this.view.noteId === note.id && typeof this.view[name] === "function") {
        return this.view[name](...args);
      }
      return undefined;
    };
    try {
      entry.handle = panels.openComments(host, {
        note,
        access,
        user: this.user,
        api: this.api,
        store: this.store,
        sync: this.sync,
        isOnline: () => this.isOnline(),
        source,
        getBody: () => {
          const body = viewHook("commentBody");
          return typeof body === "string" ? body : String(note.content || "");
        },
        resolve: (body, anchor) => {
          const anchors = this.mods.anchors;
          return anchors && typeof anchors.resolveAnchor === "function" ? anchors.resolveAnchor(body, anchor) : null;
        },
        onActivate: (id) => viewHook("onCommentActivate", id),
        onReveal: (id) => {
          // On narrow viewports the rail is a Dialog over the note. It closes
          // first, so the note can scroll and the passage is not hidden.
          if (this.railDialog) {
            this.closeRailDialog({ restore: false });
            setTimeout(() => viewHook("onCommentReveal", id), 50);
            return;
          }
          viewHook("onCommentReveal", id);
        },
        onCount: (n) => {
          entry.count = n;
          viewHook("onCommentCount", n);
        },
      });
    } catch (err) {
      console.error("[markspace] the comments panel failed to open", err);
      this.comments = null;
      return null;
    }
    source.load();
    return entry;
  }

  /** Unmounts the comments panel of the previous note. */
  disposeComments() {
    const entry = this.comments;
    this.comments = null;
    if (entry && entry.handle && typeof entry.handle.unmount === "function") {
      try {
        entry.handle.unmount();
      } catch (err) {
        console.error("[markspace] comments cleanup failed", err);
      }
    }
  }

  /**
   * Returns the rail panel of one kind for a note: history, share, files or
   * ai. Like the comments panel, its host outlives re-renders of the same
   * note, so an expanded version or a half-typed question survives a sync.
   * The panel mounts on first use, in activateRailPanel.
   * @param {"history" | "share" | "files" | "ai"} kind
   * @param {object} note
   * @param {string} access
   * @param {{mode?: string, hooks?: object}} [options] hooks are editor callbacks such as insertText.
   * @returns {{kind: string, key: string, noteId: string, note: object, access: string, hooks: object, host: HTMLElement, handle: object | null} | null}
   */
  railPanel(kind, note, access, { mode = "view", hooks = {} } = {}) {
    if (!note || !this.railOpener(kind)) {
      return null;
    }
    const key = [note.id, access, mode, note.deleted ? "trash" : "live"].join(":");
    let entry = this.railPanels.get(kind);
    if (entry && entry.key === key) {
      entry.note = note;
      entry.hooks = hooks;
      return entry;
    }
    if (entry) {
      this.unmountRailPanel(entry);
    }
    entry = { kind, key, noteId: note.id, note, access, hooks, host: h("div", { class: "panel-host" }), handle: null };
    this.railPanels.set(kind, entry);
    return entry;
  }

  /**
   * Mounts a rail panel on first use. With extra, such as an AI task from the
   * editor, it mounts the panel again with those options.
   * @param {object} entry from railPanel
   * @param {object | null} [extra]
   */
  activateRailPanel(entry, extra = null) {
    if (!entry || (entry.handle && !extra)) {
      return;
    }
    const open = this.railOpener(entry.kind);
    if (!open) {
      return;
    }
    this.unmountRailPanel(entry, { keep: true });
    entry.host.replaceChildren();
    const options = Object.assign(
      {
        access: entry.access,
        store: this.store,
        sync: this.sync,
        user: this.user,
        api: this.api,
        isOnline: () => this.isOnline(),
        navigate: (url) => this.navigate(url),
        hrefFor: (kind, rec) => this.hrefFor(kind, rec),
        aiEnabled: this.aiState !== "off",
        events: this.events,
        openPanel: (id) => this.openPanel(id),
        onCount: (n) => this.setRailCount(entry.noteId, entry.kind, n),
      },
      entry.hooks || {},
      extra || {}
    );
    // The note stays current across re-renders of the same note.
    Object.defineProperty(options, "note", { get: () => entry.note, enumerable: true });
    try {
      entry.handle = open(entry.host, options);
    } catch (err) {
      console.error("[markspace] the " + entry.kind + " panel failed to open", err);
      entry.host.replaceChildren(blankslate({ icon: "alert", title: "This panel could not open", size: "small", border: true }));
    }
  }

  /** Unmounts a rail panel. Without keep it also forgets the entry. */
  unmountRailPanel(entry, { keep = false } = {}) {
    if (entry.handle && typeof entry.handle.unmount === "function") {
      try {
        entry.handle.unmount();
      } catch (err) {
        console.error("[markspace] panel cleanup failed", err);
      }
    }
    entry.handle = null;
    if (!keep && this.railPanels.get(entry.kind) === entry) {
      this.railPanels.delete(entry.kind);
    }
  }

  /** Unmounts the rail panels of every note but noteId. */
  disposeRailPanels(noteId) {
    for (const entry of Array.from(this.railPanels.values())) {
      if (entry.noteId !== noteId) {
        this.unmountRailPanel(entry);
      }
    }
  }

  /** Stores a rail count and shows it on the current view's tab. */
  setRailCount(noteId, kind, n) {
    const entry = this.railCounts.get(noteId) || { counts: {}, loadedAt: 0 };
    entry.counts[kind] = n;
    this.railCounts.set(noteId, entry);
    if (this.view && this.view.noteId === noteId && typeof this.view.onRailCount === "function") {
      this.view.onRailCount(kind, n);
    }
  }

  /**
   * Learns whether AI is switched on, from the settings in /api/auth/me and
   * then the provider list, and remembers the answer for the next start. The
   * rail hides its AI tab while AI is off.
   */
  async probeAI() {
    if (!this.api || !this.isOnline() || this.probingAI) {
      return;
    }
    this.probingAI = true;
    let state = "on";
    try {
      const me = await this.api("GET", "/api/auth/me");
      if (me && me.settings && me.settings.aiEnabled === false) {
        state = "off";
      } else {
        await this.api("GET", "/api/ai/providers");
      }
    } catch (err) {
      const status = err && err.status;
      if (status === 404 || status === 501 || status === 503) {
        state = "off";
      } else {
        this.probingAI = false;
        return;
      }
    }
    this.probingAI = false;
    if (state !== this.aiState) {
      this.aiState = state;
      storageSet(AI_KEY, state);
      if (this.view && typeof this.view.onAIState === "function") {
        this.view.onAIState(state);
      }
    }
  }

  /**
   * Opens the comments of the current view: focuses a thread, or starts a
   * comment on a passage.
   * @param {{focusId?: string, draftAnchor?: object}} [options]
   */
  showComments(options = {}) {
    if (this.view && typeof this.view.showComments === "function") {
      this.view.showComments(options);
    }
  }

  /**
   * Reloads comments when the sync engine goes idle, so the list reflects
   * anchors the server moved after a saved edit.
   * @param {object | null} status
   */
  reloadCommentsAfterSync(status) {
    const busy = !!(status && ((status.pending || 0) > 0 || status.syncing));
    if (this.syncWasBusy && !busy && this.comments) {
      this.reloadComments();
    }
    this.syncWasBusy = busy;
  }

  /** Returns the rail tab picked last. */
  railTab() {
    return storageGet(RAIL_TAB_KEY);
  }

  /**
   * Remembers the rail tab picked last.
   * @param {string} id
   */
  setRailTab(id) {
    storageSet(RAIL_TAB_KEY, id || null);
  }

  /* ----- Uploads ----- */

  /** Shows the header upload Button with a CounterLabel while uploads exist. */
  renderUploads(uploads) {
    const list = Array.isArray(uploads) ? uploads : [];
    const active = list.filter((u) => u.state === "queued" || u.state === "uploading" || u.state === "failed");
    const failed = active.filter((u) => u.state === "failed").length;
    this.uploadButton.hidden = active.length === 0;
    const counter = this.uploadButton.querySelector(".counter-label");
    if (counter) {
      setCount(counter, active.length || null);
    }
    this.uploadButton.dataset.failed = failed ? "true" : "false";
    this.uploadButton.setAttribute("aria-label", "Uploads (" + active.length + ")" + (failed ? ", " + failed + " failed" : ""));
  }

  /** Opens the upload list in an AnchoredOverlay. */
  openUploads() {
    const list = uploadList({ sync: this.sync, emptyText: "Nothing is uploading" });
    const content = h("div", { class: "upload-overlay" }, h("h2", { class: "upload-overlay-title", text: "Uploads" }), list);
    this.menu = anchoredOverlay(this.uploadButton, content, {
      placement: "bottom-end",
      label: "Uploads",
      width: 320,
      onClose: () => {
        if (typeof list.destroy === "function") {
          list.destroy();
        }
      },
    });
    return this.menu;
  }

  /* ----- Quick switcher ----- */

  /**
   * Opens the quick switcher: a Dialog, xlarge, at the top, with a visually
   * hidden title "Jump to", the filter first and an ActionList listbox below
   * (docs/primer.md 5.5). It lists pages from the local store and the commands
   * contributed for the current context. A query that starts with ">" lists
   * commands only.
   * @param {{commandMode?: boolean}} [options]
   */
  openSwitcher({ commandMode = false } = {}) {
    if (this.switcher) {
      if (commandMode && !this.switcher.input.value.startsWith(">")) {
        this.switcher.input.value = "> " + this.switcher.input.value;
        this.switcher.run();
      }
      this.switcher.input.focus();
      return;
    }
    const listId = uid("switcher-list");
    const field = formControl({ label: "Jump to a page or run a command", hideLabel: true, name: "q", type: "text", leadingVisual: "search", size: "large", autocomplete: "off" });
    const input = field.querySelector("input");
    input.setAttribute("role", "combobox");
    input.setAttribute("aria-expanded", "true");
    input.setAttribute("aria-controls", listId);
    input.setAttribute("aria-autocomplete", "list");
    input.setAttribute("spellcheck", "false");
    input.setAttribute("enterkeyhint", "go");
    const hint = h("p", { class: "switcher-hint", hidden: true, text: "Type > to list commands." });
    const list = h("ul", { class: "action-list switcher-list", role: "listbox", id: listId, "aria-label": "Results" });
    const state = { input, list, entries: [], index: -1, seq: 0, commands: null, handle: null, run: null };
    this.switcher = state;
    if (commandMode) {
      input.value = "> ";
    }

    const select = (index) => {
      state.index = index;
      state.entries.forEach((entry, i) => {
        const on = i === index;
        entry.el.setAttribute("aria-selected", on ? "true" : "false");
        entry.el.classList.toggle("is-active", on);
        if (on) {
          entry.el.scrollIntoView({ block: "nearest" });
          input.setAttribute("aria-activedescendant", entry.el.id);
        }
      });
      if (index < 0) {
        input.removeAttribute("aria-activedescendant");
      }
    };
    const choose = (entry) => {
      state.handle.close("select");
      entry.run();
    };
    const group = (heading) => {
      const headingId = uid("switcher-group");
      const items = h("ul", { class: "action-list-group-list", role: "group", "aria-labelledby": headingId });
      list.appendChild(h("li", { class: "action-list-group", role: "presentation" }, h("span", { class: "action-list-heading", id: headingId, text: heading }), items));
      return items;
    };
    const addEntry = (target, { label, description = "", visual = null, trailing = "", run }) => {
      const el = h(
        "li",
        { class: "action-list-item switcher-option", role: "option", id: uid("switcher-option"), "aria-selected": "false" },
        h("span", { class: "action-list-leading", "aria-hidden": "true" }, visual),
        h(
          "span",
          { class: "action-list-text" + (description ? " has-block-description" : "") },
          h("span", { class: "action-list-label", text: label }),
          description ? h("span", { class: "action-list-description", text: description }) : null
        ),
        trailing ? h("kbd", { class: "action-list-trailing keybinding-hint", text: trailing }) : null
      );
      const entry = { el, run };
      el.addEventListener("click", () => choose(entry));
      el.addEventListener("pointermove", () => {
        const i = state.entries.indexOf(entry);
        if (i !== state.index) {
          select(i);
        }
      });
      state.entries.push(entry);
      target.appendChild(el);
    };
    const noteVisual = (note) => (note.icon ? h("span", { class: "switcher-emoji", text: note.icon }) : icon("note", 16));
    const message = (text) => list.appendChild(h("li", { class: "switcher-empty", role: "presentation", text }));

    const run = async () => {
      const seq = ++state.seq;
      const query = input.value;
      const { commandMode: onlyCommands, text, commands } = matchCommands(state.commands || [], query);
      let hits = [];
      if (!onlyCommands) {
        try {
          if (text) {
            hits = (await this.store.searchNotes(text, { limit: 20 })) || [];
          } else {
            hits = ((await this.store.recentNotes(8)) || []).map((note) => ({ note, snippet: "" }));
          }
        } catch (err) {
          console.error("[markspace] search failed", err);
        }
      }
      if (seq !== state.seq) {
        return;
      }
      list.replaceChildren();
      state.entries = [];
      const notes = hits.filter((hit) => hit && hit.note && !hit.note.deleted);
      if (notes.length) {
        const target = group(text ? "Pages" : "Recent");
        for (const hit of notes) {
          const note = hit.note;
          const space = this.spaceBy(note.spaceId);
          addEntry(target, {
            label: note.title || "Untitled",
            description: [space ? space.name : "", stripMd(note.path || "")].filter(Boolean).join(" / "),
            visual: noteVisual(note),
            run: () => this.navigate(noteHref(note, space && space.slug), { state: { noteId: note.id } }),
          });
        }
      }
      if (commands.length) {
        const target = group("Commands");
        for (const cmd of commands) {
          addEntry(target, {
            label: cmd.title,
            visual: icon(cmd.icon && ICON_NAMES.includes(cmd.icon) ? cmd.icon : "chevron-right", 16),
            trailing: cmd.shortcut || "",
            run: () => this.runCommand(cmd),
          });
        }
      }
      if (onlyCommands) {
        if (state.commands === null) {
          message(this.isOnline() ? "Loading commands" : "Commands need a connection");
        } else if (!commands.length) {
          message(text ? "No commands match “" + text + "”" : "No commands are available here");
        }
      } else if (text) {
        if (!notes.length && !commands.length) {
          message("No pages match “" + text + "”");
        }
        const target = group("Actions");
        addEntry(target, { label: "Search all pages for “" + text + "”", visual: icon("search", 16), run: () => this.navigate(searchUrl({ q: text })) });
        const space = this.defaultSpace();
        if (space) {
          addEntry(target, { label: "Create “" + text + "”", description: "New page in " + space.name, visual: icon("plus", 16), run: () => this.createNote({ space, title: text }) });
        }
      } else if (!notes.length) {
        message("Type to search your pages");
      }
      hint.hidden = onlyCommands || !(state.commands && state.commands.length);
      select(state.entries.length ? 0 : -1);
      announce(state.entries.length === 1 ? "1 result" : state.entries.length + " results");
    };
    state.run = run;
    const debounced = debounce(run, 60);
    input.addEventListener("input", debounced);
    input.addEventListener("keydown", (event) => {
      if (event.key === "ArrowDown" || event.key === "ArrowUp") {
        event.preventDefault();
        select(moveIndex(state.index, event.key === "ArrowDown" ? 1 : -1, state.entries.length));
      } else if (event.key === "Enter") {
        event.preventDefault();
        const entry = state.entries[state.index];
        if (entry) {
          choose(entry);
        }
      }
    });
    state.handle = openDialog({
      title: "Jump to",
      hideTitle: true,
      width: "xlarge",
      align: "top",
      className: "switcher-dialog",
      content: h("div", { class: "switcher" }, field, hint, list),
      initialFocus: input,
      // Ctrl+K opens the switcher from anywhere. Without a focused trigger,
      // focus returns to the visible Search button, never to body (docs/primer.md 5.6).
      returnFocus: [this.searchButton, this.searchIcon].find((b) => b && b.getClientRects().length) || this.main,
      onClose: () => {
        if (this.switcher === state) {
          this.switcher = null;
        }
      },
    });
    run();
    this.contributions(this.commandContext()).then((contrib) => {
      if (this.switcher !== state) {
        return;
      }
      state.commands = (contrib && Array.isArray(contrib.commands) ? contrib.commands : []).filter((cmd) => cmd && cmd.id && cmd.title);
      run();
    });
  }
}

/* ---------- Boot ---------- */

/** Replaces the boot screen with a centred Blankslate. */
function renderMessage(root, { title, text, actionLabel, onAction }) {
  root.replaceChildren(
    h(
      "main",
      { class: "plain-main", id: "main", tabindex: "-1" },
      pageBlankslate({ icon: "cloud-offline", title, description: text, primaryAction: actionLabel ? { label: actionLabel, onClick: onAction } : null })
    )
  );
}

/**
 * Starts the application in a root element.
 * @param {HTMLElement} root
 * @returns {Promise<object | null>} the running app, or null when it could not start.
 */
export async function start(root) {
  // The shell document opened at its own address starts at home.
  if (!parseRoute(location.pathname, location.search)) {
    history.replaceState(null, "", "/");
  }
  const mods = await loadModules();
  const getSession = need(mods.session, "getSession", "session.js");
  const openStore = need(mods.store, "openStore", "store.js");
  const createSync = need(mods.sync, "createSync", "sync.js");
  const api = need(mods.api, "api", "api.js");
  if (!getSession || !openStore || !createSync || !api) {
    renderMessage(root, { title: "The app is still loading", text: "A part of the application is missing. Reload in a moment.", actionLabel: "Reload", onAction: () => location.reload() });
    return null;
  }
  let session;
  try {
    session = await getSession();
  } catch (err) {
    console.error("[markspace] session check failed", err);
    session = { user: null, offline: !navigator.onLine };
  }
  if (!session || !session.user) {
    if (!(session && session.offline) && navigator.onLine) {
      location.replace("/login?next=" + encodeURIComponent(location.pathname + location.search));
      return null;
    }
    renderMessage(root, {
      title: "Sign in when you are back online",
      text: "This device has no saved pages for an account yet. Connect to the internet and sign in to start using them offline.",
      actionLabel: "Try again",
      onAction: () => location.reload(),
    });
    window.addEventListener("online", () => location.reload(), { once: true });
    return null;
  }
  let store;
  try {
    store = await openStore(session.user.id);
  } catch (err) {
    console.error("[markspace] could not open the local store", err);
    renderMessage(root, {
      title: "Your pages could not be opened",
      text: "The browser refused local storage for this site. Private browsing modes often do this.",
      actionLabel: "Reload",
      onAction: () => location.reload(),
    });
    return null;
  }
  const sync = createSync({ store, api, upload: mods.api.upload });
  let fileCache = null;
  if (mods.filecache && typeof mods.filecache.createFileCache === "function") {
    try {
      fileCache = mods.filecache.createFileCache({ userId: session.user.id, store });
    } catch (err) {
      console.error("[markspace] the offline file cache could not start", err);
    }
  }
  const app = new App(root, { user: session.user, store, sync, mods, offline: session.offline, fileCache });
  window.markspaceApp = app;
  await app.mount();
  if (typeof sync.start === "function") {
    sync.start();
  }
  return app;
}

if (typeof document !== "undefined" && document.querySelector("[data-shell-root]")) {
  start(document.querySelector("[data-shell-root]")).catch((err) => {
    console.error("[markspace] the shell failed to start", err);
  });
}
