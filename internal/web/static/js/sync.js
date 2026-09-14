// Sync engine: local-first mutations, an outbox replayed through the REST
// API, and pulls from GET /api/sync.
//
// Every mutation writes to the store and queues an op before it resolves. The
// outbox replays ops one at a time in order. Pulls copy server changes into
// the store without overwriting items that still have queued ops.

import { newId } from "./ids.js";
import { slugify } from "./markdown.js";
import { encodeDest, parse as parseLinks, FORM } from "./links.js";

const PUSH_DEBOUNCE_MS = 1000;
const PULL_INTERVAL_MS = 60000;
const BACKOFF_MIN_MS = 2000;
const BACKOFF_MAX_MS = 60000;
const PULL_LIMIT = 200;
const DONE_UPLOADS_KEPT = 50;
const UPLOAD_EVENT_MS = 100;

const META_REV = "rev";
const META_ACCESS_KEY = "accessKey";
const META_USER = "user";
const META_UPLOADS = "uploads";
const SERVER_COPY = "server:";

const NOTE_FIELDS = ["title", "content", "tags", "icon"];
const NOTE_OPS = new Set(["create", "update", "move", "trash", "restore", "delete", "star"]);
const CREATE_OPS = new Set(["folder.create", "file.upload", "item.copy"]);

/** Op types for folders and files, in docs/files.md order. */
export const ITEM_OPS = Object.freeze([
  "folder.create", "folder.rename", "file.upload", "file.rename", "file.replace",
  "item.move", "item.copy", "item.trash", "item.restore", "item.delete",
]);

/**
 * Returns the access level string from either a string or a grant object.
 * @param {any} v
 * @returns {string}
 */
function accessOf(v) {
  if (typeof v === "string") return v;
  if (v && typeof v === "object" && typeof v.access === "string") return v.access;
  return "";
}

/**
 * Maps a space role to the access it grants.
 * @param {string} role
 * @returns {string}
 */
function roleAccess(role) {
  switch (role) {
    case "owner":
    case "admin":
      return "manage";
    case "editor":
      return "edit";
    case "commenter":
      return "comment";
    case "viewer":
      return "view";
    default:
      return "edit";
  }
}

/**
 * Compares two note field values, treating missing tags as an empty list
 * and a missing string as empty.
 * @param {string} field
 * @param {any} a
 * @param {any} b
 * @returns {boolean}
 */
function sameField(field, a, b) {
  if (field === "tags") return JSON.stringify(a || []) === JSON.stringify(b || []);
  return (a ?? "") === (b ?? "");
}

/**
 * Returns the directory that holds a note's children, or a folder path as is.
 * @param {string} path
 * @returns {string}
 */
function childDir(path) {
  return path ? path.replace(/\.md$/, "") : "";
}

/** Returns the folder part of a slash-separated path. */
export function dirOf(path) {
  const p = String(path || "");
  const i = p.lastIndexOf("/");
  return i >= 0 ? p.slice(0, i) : "";
}

/** Returns the last segment of a slash-separated path. */
export function baseOf(path) {
  const p = String(path || "");
  return p.slice(p.lastIndexOf("/") + 1);
}

/** Joins a folder path and a name. */
export function joinPath(dir, name) {
  return dir ? `${dir}/${name}` : String(name);
}

/**
 * Allocates a path the way the server's AllocPath does: the parent's child
 * directory, slugify(title) and ".md", with -2, -3 appended on a collision.
 * The parent path is a folder path, or a note path whose ".md" is dropped.
 * @param {object[]} notes live notes of the space
 * @param {string} parentPath
 * @param {string} title
 * @param {string} [exceptPath] a path that counts as free, the note's own
 * @returns {string}
 */
export function allocPath(notes, parentPath, title, exceptPath = "") {
  const dir = childDir(parentPath);
  const slug = slugify(title);
  const taken = new Set(notes.filter((n) => !n.deleted).map((n) => String(n.path).toLowerCase()));
  for (let attempt = 1; ; attempt++) {
    const name = attempt > 1 ? `${slug}-${attempt}` : slug;
    const candidate = dir ? `${dir}/${name}.md` : `${name}.md`;
    if (candidate === exceptPath || !taken.has(candidate.toLowerCase())) return candidate;
  }
}

/**
 * Formats a local date and time as "YYYY-MM-DD HH:MM".
 * @param {number} ms
 * @returns {string}
 */
function stampOf(ms) {
  const d = new Date(ms);
  const pad = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/**
 * Formats the title of a conflict copy with the local date and time.
 * @param {string} title
 * @param {number} ms
 * @returns {string}
 */
export function conflictTitle(title, ms) {
  return `${title || "Untitled"} (conflicted copy ${stampOf(ms)})`;
}

/**
 * Splits a name into stem and extension. A folder name has no extension, and
 * neither has a name whose only dot is its first character.
 * @param {string} name
 * @param {boolean} [isDir]
 * @returns {[string, string]}
 */
export function splitExt(name, isDir = false) {
  const s = String(name || "");
  const i = s.lastIndexOf(".");
  if (isDir || i <= 0) return [s, ""];
  return [s.slice(0, i), s.slice(i)];
}

/**
 * Names the conflict copy of a file: "<stem> (conflicted copy YYYY-MM-DD HH:MM).<ext>".
 * @param {string} name
 * @param {number} ms
 * @returns {string}
 */
export function conflictFileName(name, ms) {
  const [stem, ext] = splitExt(name);
  return `${stem || "file"} (conflicted copy ${stampOf(ms)})${ext}`;
}

/**
 * Returns name, or "<stem> (N).<ext>" with the first free N from 2, as the
 * server's freeName does. Names compare without regard to case.
 * @param {Iterable<string>} taken names in the target folder
 * @param {string} name
 * @param {boolean} [isDir]
 * @returns {string}
 */
export function freeName(taken, name, isDir = false) {
  const used = new Set([...taken].map((n) => String(n).toLowerCase()));
  if (!used.has(String(name).toLowerCase())) return name;
  const [stem, ext] = splitExt(name, isDir);
  for (let n = 2; ; n++) {
    const cand = `${stem} (${n})${ext}`;
    if (!used.has(cand.toLowerCase())) return cand;
  }
}

/**
 * Returns name when it is free, else "<stem> copy.<ext>", then
 * "<stem> copy N.<ext>", as the server's copyName does.
 * @param {Iterable<string>} taken
 * @param {string} name
 * @param {boolean} [isDir]
 * @returns {string}
 */
export function copyName(taken, name, isDir = false) {
  const used = new Set([...taken].map((n) => String(n).toLowerCase()));
  if (!used.has(String(name).toLowerCase())) return name;
  const [stem, ext] = splitExt(name, isDir);
  let cand = `${stem} copy${ext}`;
  for (let n = 2; used.has(cand.toLowerCase()); n++) cand = `${stem} copy ${n}${ext}`;
  return cand;
}

const IGNORED_NAME = /^(?:~\$.*|\.DS_Store|Thumbs\.db|desktop\.ini|.*\.tmp-.*)$/i;

/**
 * Checks a folder or file name against the docs/files.md name rules.
 * @param {string} name
 * @param {{kind?: string}} [options] kind "file" also refuses names ending in ".md"
 * @returns {string} an error message, or "" when the name is valid
 */
export function validateName(name, { kind = "" } = {}) {
  const s = String(name ?? "");
  if (s === "") return "Enter a name.";
  if (s === "." || s === "..") return "This name is not allowed.";
  if (s.startsWith(".")) return "A name cannot start with a dot.";
  if (s.trim() !== s) return "A name cannot start or end with a space.";
  if (/[/\\\u0000-\u001f\u007f-\u009f]/.test(s)) return "A name cannot contain a slash, a backslash or a control character.";
  if (new TextEncoder().encode(s).length > 255) return "This name is too long.";
  if (IGNORED_NAME.test(s)) return "This name is reserved for temporary or system files.";
  if (kind === "file" && /\.md$/i.test(s)) return "A file name cannot end in .md, because a .md file is a note.";
  return "";
}

/**
 * Returns the relative path from folder fromDir to path, as links.js writes
 * relative links, percent-encoded with encodeDest. Spaces, and parentheses
 * that do not balance, are also encoded, so the result works as a bare
 * destination such as ![](east%20wall.png).
 * @param {string} fromDir
 * @param {string} path
 * @returns {string}
 */
export function relativeDest(fromDir, path) {
  const ds = fromDir ? fromDir.split("/") : [];
  const ps = path ? path.split("/") : [];
  let c = 0;
  while (c < ds.length && c < ps.length && ds[c] === ps[c]) c++;
  const parts = [];
  for (let k = c; k < ds.length; k++) parts.push("..");
  parts.push(...ps.slice(c));
  let out = parts.join("/") || ".";
  if (/^[A-Za-z][A-Za-z0-9+.-]{1,31}:/.test(out)) out = "./" + out;
  // A bare destination may not hold spaces or unbalanced parentheses, so
  // those are percent-encoded on top of what encodeDest escapes.
  let enc = encodeDest(out).replace(/ /g, "%20");
  let depth = 0;
  let balanced = true;
  for (const c of enc) {
    if (c === "(") depth++;
    else if (c === ")" && --depth < 0) balanced = false;
  }
  if (!balanced || depth !== 0) enc = enc.replace(/\(/g, "%28").replace(/\)/g, "%29");
  return enc;
}

/**
 * Resolves a markdown destination against a folder path, as links.js does.
 * @param {string} dir
 * @param {string} target decoded destination
 * @returns {string|null} the entry path, or null when it leaves the space
 */
function cleanPath(dir, target) {
  const out = target.startsWith("/") || !dir ? [] : dir.split("/");
  for (const seg of target.split("/")) {
    if (seg === "" || seg === ".") continue;
    if (seg === "..") {
      if (!out.length) return null;
      out.pop();
    } else out.push(seg);
  }
  return out.length ? out.join("/") : null;
}

/**
 * Finds the markdown links and images in note content whose destination
 * resolves to path from the note's folder, however the destination is
 * encoded. Wikilinks are left out, because a pasted file is inserted as a
 * markdown link.
 * @param {string} content
 * @param {string} noteDir
 * @param {string} path
 * @returns {object[]} links.js links
 */
export function linksToPath(content, noteDir, path) {
  let links;
  try {
    links = parseLinks(String(content || ""));
  } catch {
    return [];
  }
  return links.filter((l) => !l.external && (l.form === FORM.MARKDOWN_LINK || l.form === FORM.MARKDOWN_IMAGE) && cleanPath(noteDir, l.target) === path);
}

/**
 * Applies edits sorted by start to a string.
 * @param {string} body
 * @param {{start: number, end: number, text: string}[]} edits
 * @returns {string}
 */
function applyEdits(body, edits) {
  let out = "";
  let last = 0;
  for (const e of edits.sort((a, b) => a.start - b.start)) {
    out += body.slice(last, e.start) + e.text;
    last = e.end;
  }
  return out + body.slice(last);
}

/**
 * Replaces every markdown link or image to a file with the file name as
 * plain text, for an upload that was cancelled.
 * @param {string} content
 * @param {string} noteDir the folder path of the note
 * @param {string} path entry path of the file
 * @param {string} filename
 * @returns {string}
 */
export function removeFileLinks(content, noteDir, path, filename) {
  const body = String(content || "");
  const hits = linksToPath(body, noteDir, path);
  return hits.length ? applyEdits(body, hits.map((l) => ({ start: l.start, end: l.end, text: filename }))) : body;
}

/**
 * Points every markdown link or image to oldPath at newPath. The new
 * destination is relative to the note's folder and encoded as relativeDest
 * writes it. A title after the destination stays.
 * @param {string} content
 * @param {string} noteDir
 * @param {string} oldPath
 * @param {string} newPath
 * @returns {string}
 */
export function replaceFileLinks(content, noteDir, oldPath, newPath) {
  const body = String(content || "");
  if (oldPath === newPath) return body;
  const dest = relativeDest(noteDir, newPath);
  const hits = linksToPath(body, noteDir, oldPath);
  return hits.length ? applyEdits(body, hits.map((l) => ({ start: l.targetStart, end: l.targetEnd, text: dest }))) : body;
}

/**
 * Moves the paths of records under oldPath to newPath, in place. The record
 * at oldPath itself also changes. Returns the records that changed.
 * @param {object[]} records
 * @param {string} oldPath
 * @param {string} newPath
 * @returns {object[]}
 */
export function repathRecords(records, oldPath, newPath) {
  if (!oldPath || oldPath === newPath) return [];
  const out = [];
  for (const r of records) {
    const p = String(r.path || "");
    if (p === oldPath || p.startsWith(oldPath + "/")) {
      r.path = newPath + p.slice(oldPath.length);
      if (r.name !== undefined && p === oldPath) r.name = baseOf(newPath);
      out.push(r);
    }
  }
  return out;
}

/**
 * Reports whether an error status should be retried with backoff.
 * @param {number} status
 * @returns {boolean}
 */
function retryable(status) {
  return status === 0 || status === 408 || status === 429 || status >= 500;
}

/**
 * Returns the HTTP status of an error, or -1 for an error without one.
 * @param {any} err
 * @returns {number}
 */
function statusOf(err) {
  return err && typeof err.status === "number" ? err.status : -1;
}

/**
 * Returns a file content URL as a root-relative path. The server may prefix
 * its configured base URL, which can differ from the page origin.
 * @param {string} url
 * @returns {string}
 */
function relativeUrl(url) {
  if (typeof url !== "string") return url;
  const m = /^https?:\/\/[^/]+(\/(?:files|thumbs)\/.*)$/i.exec(url);
  if (m) return m[1];
  return url;
}

/**
 * Builds the content URL of a file record.
 * @param {string} slug
 * @param {string} id
 * @param {string} name
 * @returns {string}
 */
export function fileUrl(slug, id, name) {
  return `/files/${encodeURIComponent(slug || "")}/${encodeURIComponent(id)}/${encodeURIComponent(name || "file")}`;
}

/** Returns the op key of an item op: the item id for item ops, the note id for note ops. */
const opItem = (o) => o.itemId || o.noteId || "";

/**
 * Creates the sync engine.
 * @param {object} deps
 * @param {object} deps.store a Store from store.js
 * @param {Function} deps.api api(method, path, body) from api.js
 * @param {Function} deps.upload upload(path, file, {onProgress, fields, method}) from api.js
 * @param {() => number} [deps.now] clock in milliseconds
 * @param {() => boolean} [deps.online] reports connectivity
 * @param {{setTimeout: Function, clearTimeout: Function}} [deps.timers]
 * @param {() => boolean} [deps.visible] reports whether the page is visible
 * @param {EventTarget} [deps.globalTarget] receives online and offline events
 * @param {EventTarget} [deps.documentTarget] receives visibilitychange events
 * @param {LockManager|null} [deps.locks] Web Locks, so one tab replays the outbox at a time
 * @returns {object} Sync
 */
export function createSync({
  store,
  api,
  upload,
  now = Date.now,
  online = () => typeof navigator === "undefined" || navigator.onLine !== false,
  timers = { setTimeout: (fn, ms) => setTimeout(fn, ms), clearTimeout: (id) => clearTimeout(id) },
  visible = () => typeof document === "undefined" || document.visibilityState !== "hidden",
  globalTarget = typeof window !== "undefined" ? window : null,
  documentTarget = typeof document !== "undefined" ? document : null,
  locks = typeof navigator !== "undefined" && navigator.locks ? navigator.locks : null,
} = {}) {
  const listeners = new Map();
  let started = false;
  let pushing = null;
  let pushAgain = false;
  let pulling = null;
  let pullAgain = false;
  let inflightOpId = null;
  let pending = 0;
  let lastSyncAt = null;
  let lastError = null;
  let authRequired = false;
  let conflicts = 0;
  let failures = 0;
  let pushTimer = null;
  let retryTimer = null;
  let pullTimer = null;
  let uploadEventTimer = null;
  let lastUploadEvent = 0;
  const uploadItems = new Map();
  const doneUploads = [];
  let chain = Promise.resolve();

  const iso = () => new Date(now()).toISOString();
  const enc = encodeURIComponent;
  const notePath = (id) => `/api/notes/${enc(id)}`;

  /**
   * Runs fn after every earlier locked call finishes, so queue and item
   * writes never interleave. Locked functions must not call locked.
   * @param {() => Promise<any>} fn
   * @returns {Promise<any>}
   */
  function locked(fn) {
    const run = chain.then(() => fn());
    chain = run.catch(() => {});
    return run;
  }

  // -------------------------------------------------------------------------
  // Events and status

  function emit(event, payload) {
    for (const fn of [...(listeners.get(event) || [])]) {
      try {
        fn(payload);
      } catch (err) {
        if (typeof reportError === "function") reportError(err);
      }
    }
  }

  function status() {
    return {
      online: Boolean(online()),
      syncing: Boolean(pulling || pushing),
      pending,
      lastSyncAt,
      lastError,
      authRequired,
      conflicts,
    };
  }

  function emitStatus() {
    emit("status", status());
  }

  async function refreshPending() {
    pending = await store.countOps();
  }

  function uploadsSnapshot() {
    const live = [...uploadItems.values()].map((u) => {
      const { cancelRequested, op, ...rest } = u;
      return rest;
    });
    return [...live, ...doneUploads.map((u) => ({ ...u }))].sort((a, b) => a.createdAt - b.createdAt);
  }

  /**
   * Emits uploads-changed. A forced event goes out at once; progress events
   * go out at most every 100 ms.
   * @param {boolean} force
   */
  function emitUploads(force) {
    const fire = () => {
      uploadEventTimer = null;
      lastUploadEvent = now();
      emit("uploads-changed", { uploads: uploadsSnapshot() });
    };
    if (force) {
      if (uploadEventTimer) timers.clearTimeout(uploadEventTimer);
      fire();
      return;
    }
    const wait = UPLOAD_EVENT_MS - (now() - lastUploadEvent);
    if (wait <= 0) {
      if (uploadEventTimer) timers.clearTimeout(uploadEventTimer);
      fire();
    } else if (!uploadEventTimer) {
      uploadEventTimer = timers.setTimeout(fire, wait);
    }
  }

  async function persistUploads() {
    const saved = [...uploadItems.values()].map((u) => {
      const { cancelRequested, ...rest } = u;
      return { ...rest, state: rest.state === "uploading" ? "queued" : rest.state, loaded: 0 };
    });
    await store.setMeta(META_UPLOADS, saved);
  }

  function setAuthRequired() {
    if (!authRequired) {
      authRequired = true;
      emit("auth-required", {});
    }
  }

  const ready = (async () => {
    const saved = (await store.getMeta(META_UPLOADS)) || [];
    for (const item of saved) {
      // Items from the attachment model carry no file id and are dropped.
      if (item && item.key && item.fileId) uploadItems.set(item.key, { ...item, loaded: 0, state: item.state === "failed" ? "failed" : "queued" });
    }
    await refreshPending();
  })();

  // -------------------------------------------------------------------------
  // Local records

  async function currentUser() {
    return (await store.getMeta(META_USER)) || null;
  }

  /**
   * Builds the local record of a server note, keeping local-only fields.
   * @param {object} server
   * @param {object|null} local
   * @returns {object}
   */
  function fromServer(server, local) {
    const note = { ...server };
    delete note.kind;
    note.access = accessOf(server.access) || (local && local.access) || "edit";
    note.starred = local ? Boolean(local.starred) : Boolean(server.starred);
    note.spaceSlug = server.spaceSlug || (local && local.spaceSlug) || "";
    note.tags = server.tags || [];
    note.serverUpdatedAt = server.updatedAt || null;
    note.provisional = false;
    return note;
  }

  /**
   * Builds the local record of a server folder or file, keeping local-only
   * fields. Folders and files in the feed carry no access, so the record
   * keeps its local access or takes the access the space role grants.
   * @param {string} kind "folder" or "file"
   * @param {object} server
   * @param {object|null} local
   * @param {object|null} space
   * @param {object|null} user
   * @returns {object}
   */
  function fromServerItem(kind, server, local, space, user) {
    const rec = { ...server };
    delete rec.kind;
    rec.spaceSlug = server.spaceSlug || (local && local.spaceSlug) || (space && space.slug) || "";
    let access = accessOf(server.access) || (local && local.access) || "";
    if (!access) {
      if (user && user.isAdmin) access = "manage";
      else if (space && space.myRole) access = roleAccess(space.myRole);
      else access = "view";
    }
    rec.access = access;
    rec.serverUpdatedAt = server.updatedAt || null;
    rec.provisional = false;
    if (kind === "file") {
      rec.url = relativeUrl(server.url) || fileUrl(rec.spaceSlug, rec.id, rec.name);
      if (server.thumb) rec.thumb = relativeUrl(server.thumb);
      delete rec.pendingKey;
      delete rec.uploadFailed;
    }
    return rec;
  }

  /** Stores records of one kind. */
  async function putKind(kind, list) {
    if (!list.length) return;
    if (kind === "folder") await store.putFolders(list);
    else if (kind === "file") await store.putFiles(list);
    else await store.putNotes(list);
  }

  /** Returns {kind, rec} for an id, or null. */
  async function findItem(id) {
    const item = await store.getItem(id);
    if (!item) return null;
    return { kind: item.kind, rec: item.folder || item.note || item.file };
  }

  /** Returns every record of a space, trashed ones included, tagged with its kind. */
  async function spaceRecords(spaceId) {
    const { folders, notes, files } = await store.listItems(spaceId, { includeDeleted: true });
    return [
      ...folders.map((r) => ({ kind: "folder", rec: r })),
      ...notes.map((r) => ({ kind: "note", rec: r })),
      ...files.map((r) => ({ kind: "file", rec: r })),
    ];
  }

  /** Writes tagged records grouped by kind. */
  async function putTagged(tagged) {
    for (const kind of ["folder", "note", "file"]) await putKind(kind, tagged.filter((t) => t.kind === kind).map((t) => t.rec));
  }

  /**
   * Returns the folder path a parent id stands for: a folder's path, or for a
   * note the folder it becomes, and "" for the root.
   * @param {string} parentId
   * @returns {Promise<{path: string, folder: object|null, note: object|null}>}
   */
  async function parentInfo(parentId) {
    if (!parentId) return { path: "", folder: null, note: null };
    const folder = await store.getFolder(parentId);
    if (folder) return { path: folder.path, folder, note: null };
    const note = await store.getNote(parentId);
    if (note) return { path: baseOf(note.path) === "index.md" ? dirOf(note.path) : childDir(note.path), folder: null, note };
    return { path: "", folder: null, note: null };
  }

  /** Names of live items directly inside folder path dir, except one id. */
  function namesIn(records, dir, exceptId = "") {
    return records
      .filter((t) => !t.rec.deleted && t.rec.id !== exceptId && dirOf(t.rec.path) === dir)
      .map((t) => (t.kind === "note" ? baseOf(t.rec.path) : t.rec.name || baseOf(t.rec.path)));
  }

  /** The next position after the live items in folder path dir. */
  function nextPositionIn(records, dir) {
    return records.filter((t) => !t.rec.deleted && dirOf(t.rec.path) === dir).reduce((max, t) => Math.max(max, t.rec.position || 0), 0) + 1;
  }

  /** Access a new item under a parent gets locally. */
  async function accessUnder(space, parentAccess) {
    const user = await currentUser();
    if (user && user.isAdmin) return "manage";
    return parentAccess || (space && space.myRole ? roleAccess(space.myRole) : "edit");
  }

  /** Throws an Error with a field, as the API does for invalid input. */
  function invalid(message, field = "name") {
    const err = new Error(message);
    err.field = field;
    err.status = 400;
    return err;
  }

  /**
   * Gives a note a new path for its title and folder. A folder note keeps
   * the name index.md. Returns the note when its path changed, else nothing.
   * @param {object} note the note, already holding its new title and parent
   * @param {object[]} spaceNotes every note of the space
   * @param {string} dir the folder path the note belongs in
   * @returns {object[]}
   */
  function repathNote(note, spaceNotes, dir) {
    const others = spaceNotes.filter((n) => n.id !== note.id && !n.deleted);
    const oldPath = note.path || "";
    const newPath = baseOf(oldPath) === "index.md" && dir === dirOf(oldPath) ? oldPath : allocPath(others, dir, note.title, oldPath);
    if (newPath === oldPath) return [];
    note.path = newPath;
    note.slug = baseOf(newPath).replace(/\.md$/, "");
    return [note];
  }

  /**
   * Builds a new local note with a fresh client id and a provisional path.
   * @param {object} input
   * @returns {Promise<object>}
   */
  async function buildNote({ spaceId, parentId = "", title = "", content = "", tags = [], icon = "" }) {
    const space = await store.getSpace(spaceId);
    if (!space) throw new Error(`unknown space ${spaceId}`);
    const user = await currentUser();
    const spaceNotes = await store.listNotes(space.id);
    const parent = await parentInfo(parentId);
    const records = await spaceRecords(space.id);
    const position = nextPositionIn(records, parent.path);
    const stamp = iso();
    const path = allocPath(spaceNotes, parent.path, title);
    let access = (parent.folder && parent.folder.access) || (parent.note && parent.note.access) || roleAccess(space.myRole);
    if (user && user.isAdmin) access = "manage";
    return {
      id: newId(),
      spaceId: space.id,
      spaceSlug: space.slug,
      parentId: parentId || "",
      path,
      slug: baseOf(path).replace(/\.md$/, ""),
      title,
      icon: icon || "",
      tags: tags || [],
      content: content || "",
      position,
      createdAt: stamp,
      updatedAt: stamp,
      createdBy: user ? user.id : "",
      updatedBy: user ? user.id : "",
      deleted: false,
      rev: 0,
      access: access || "edit",
      starred: false,
      serverUpdatedAt: null,
      provisional: true,
    };
  }

  function createOp(note) {
    return {
      type: "create",
      noteId: note.id,
      at: now(),
      body: {
        id: note.id,
        spaceId: note.spaceId,
        parentId: note.parentId,
        title: note.title,
        content: note.content,
        tags: note.tags,
        icon: note.icon,
      },
    };
  }

  /**
   * Adds an op to the outbox after coalescing it with queued ops. Runs under
   * the lock. An op that is being sent is never changed.
   * @param {object} op
   * @returns {Promise<{coalesced?: object}>} coalesced is the queued op that absorbed op
   */
  async function enqueueOp(op) {
    const ops = await store.peekOps();
    const idle = (o) => o.opId !== inflightOpId;
    if (NOTE_OPS.has(op.type)) {
      const forNote = ops.filter((o) => o.noteId === op.noteId);
      const last = forNote[forNote.length - 1];
      if (op.type === "update") {
        const create = forNote.find((o) => o.type === "create" && idle(o));
        if (create) {
          Object.assign(create.body, op.fields);
          await store.replaceOps(ops);
          return { coalesced: create };
        }
        if (last && last.type === "update" && idle(last)) {
          last.fields = { ...last.fields, ...op.fields };
          await store.replaceOps(ops);
          return { coalesced: last };
        }
      }
      if (op.type === "star" && last && last.type === "star" && idle(last)) {
        last.starred = op.starred;
        await store.replaceOps(ops);
        return { coalesced: last };
      }
      if (op.type === "delete") {
        const ids = new Set([op.noteId, ...(op.descendants || [])]);
        const neverSent = ops.some((o) => o.type === "create" && o.noteId === op.noteId && idle(o));
        const matches = (o) => idle(o) && ids.has(opItem(o));
        const dropped = ops.filter(matches);
        if (dropped.length) {
          for (const o of dropped) if (o.key) await discardUpload(o.key);
          await store.replaceOps(ops.filter((o) => !matches(o)));
        }
        if (neverSent) return {};
      }
      await store.enqueue(op);
      return {};
    }

    const forItem = ops.filter((o) => o.itemId && o.itemId === op.itemId);
    const last = forItem[forItem.length - 1];
    const created = forItem.find((o) => CREATE_OPS.has(o.type) && idle(o));
    const save = async (target) => {
      await store.replaceOps(ops);
      return { coalesced: target };
    };
    switch (op.type) {
      case "folder.rename":
      case "file.rename":
        if (created) {
          created.body.name = op.name;
          return save(created);
        }
        if (last && last.type === op.type && idle(last)) {
          last.name = op.name;
          return save(last);
        }
        break;
      case "file.replace":
        if (created && created.type === "file.upload") {
          const blob = await store.getBlob(op.key);
          if (blob) await store.putBlob(created.key, blob);
          await store.deleteBlob(op.key);
          if (op.name) created.body.name = op.name;
          const item = uploadItems.get(created.key);
          if (item && blob) Object.assign(item, { size: blob.size || 0, mime: blob.type || item.mime, filename: op.name || item.filename });
          uploadItems.delete(op.key);
          await persistUploads();
          return save(created);
        }
        if (last && last.type === "file.replace" && idle(last)) {
          if (last.key !== op.key) {
            await store.deleteBlob(last.key);
            uploadItems.delete(last.key);
            await persistUploads();
          }
          last.key = op.key;
          last.name = op.name;
          return save(last);
        }
        break;
      case "item.move":
        if (created) {
          created.body.parentId = op.parentId;
          if (created.type !== "item.copy") created.body.position = op.position;
          return save(created);
        }
        if (last && last.type === "item.move" && idle(last)) {
          last.parentId = op.parentId;
          last.position = op.position;
          return save(last);
        }
        break;
      case "item.trash":
      case "item.restore": {
        const opposite = op.type === "item.trash" ? "item.restore" : "item.trash";
        if (last && last.type === opposite && idle(last)) {
          await store.replaceOps(ops.filter((o) => o !== last));
          return { coalesced: last };
        }
        break;
      }
      case "item.delete": {
        const ids = new Set([op.itemId, ...(op.descendants || [])]);
        const matches = (o) => idle(o) && ids.has(opItem(o));
        const neverSent = ops.some((o) => matches(o) && o.itemId === op.itemId && CREATE_OPS.has(o.type));
        const dropped = ops.filter(matches);
        if (dropped.length) {
          for (const o of dropped) if (o.key) await discardUpload(o.key);
          await store.replaceOps(ops.filter((o) => !matches(o)));
        }
        if (neverSent) return {};
        break;
      }
      default:
        break;
    }
    await store.enqueue(op);
    return {};
  }

  /**
   * Writes changed fields to a local note, moves its path on a title change
   * and queues an update. Runs under the lock.
   * @param {object} note
   * @param {object} fields
   * @returns {Promise<object[]>} the notes written
   */
  async function applyLocalUpdate(note, fields) {
    const user = await currentUser();
    Object.assign(note, fields);
    note.updatedAt = iso();
    if (user) note.updatedBy = user.id;
    if (fields.title !== undefined) {
      const spaceNotes = await store.listNotes(note.spaceId, { includeDeleted: true });
      repathNote(note, spaceNotes, dirOf(note.path));
    }
    await store.putNotes([note]);
    await enqueueOp({ type: "update", noteId: note.id, fields, baseUpdatedAt: note.serverUpdatedAt || null, at: now() });
    return [note];
  }

  function afterLocal(ids) {
    refreshPending().then(() => {
      emit("notes-changed", { ids, source: "local" });
      emitStatus();
    });
    schedulePush();
  }

  // -------------------------------------------------------------------------
  // Note mutations

  async function createNote(input = {}) {
    await ready;
    const note = await locked(async () => {
      const n = await buildNote(input);
      await store.putNotes([n]);
      await enqueueOp(createOp(n));
      return n;
    });
    afterLocal([note.id]);
    return note;
  }

  async function updateNote(id, patch = {}) {
    await ready;
    const result = await locked(async () => {
      const note = await store.getNote(id);
      if (!note) throw new Error(`note ${id} not found`);
      const fields = {};
      for (const k of NOTE_FIELDS) {
        if (patch[k] === undefined) continue;
        if (!sameField(k, note[k], patch[k])) fields[k] = patch[k];
      }
      if (!Object.keys(fields).length) return { note, ids: [] };
      const written = await applyLocalUpdate(note, fields);
      return { note, ids: written.map((n) => n.id) };
    });
    if (result.ids.length) afterLocal(result.ids);
    return result.note;
  }

  async function moveNote(id, parentId, position) {
    await ready;
    const result = await locked(async () => {
      const note = await store.getNote(id);
      if (!note) throw new Error(`note ${id} not found`);
      if (parentId === id) throw new Error("a note cannot move under itself");
      const spaceNotes = await store.listNotes(note.spaceId, { includeDeleted: true });
      const parent = await parentInfo(parentId);
      note.parentId = parentId || "";
      note.position = Number(position) || 0;
      repathNote(note, spaceNotes, parent.path);
      await store.putNotes([note]);
      await enqueueOp({ type: "move", noteId: id, parentId: note.parentId, position: note.position, at: now() });
      return { note, ids: [note.id] };
    });
    afterLocal(result.ids);
    return result.note;
  }

  async function setDeleted(id, deleted, type) {
    await ready;
    const ids = await locked(async () => {
      const note = await store.getNote(id);
      if (!note) throw new Error(`note ${id} not found`);
      if (Boolean(note.deleted) !== deleted) {
        note.deleted = deleted;
        if (deleted) note.deletedAt = iso();
        else delete note.deletedAt;
        await store.putNotes([note]);
      }
      await enqueueOp({ type, noteId: id, at: now() });
      return [id];
    });
    afterLocal(ids);
  }

  async function deleteNote(id) {
    await ready;
    const ids = await locked(async () => {
      const note = await store.getNote(id);
      if (!note) return [];
      await store.deleteNotes([id]);
      await store.setMeta(SERVER_COPY + id, undefined);
      await enqueueOp({ type: "delete", noteId: id, descendants: [], at: now() });
      return [id];
    });
    if (ids.length) afterLocal(ids);
  }

  async function setStar(id, starred) {
    await ready;
    await locked(async () => {
      const note = await store.getNote(id);
      if (!note) throw new Error(`note ${id} not found`);
      note.starred = Boolean(starred);
      await store.putNotes([note]);
      await enqueueOp({ type: "star", noteId: id, starred: Boolean(starred), at: now() });
    });
    afterLocal([id]);
  }

  // -------------------------------------------------------------------------
  // Folder and file mutations

  /**
   * Refuses a change to an item copied offline whose copy has not synced,
   * unless the item is the root of that copy, whose op absorbs the change.
   * @param {object} rec
   * @returns {Promise<boolean>} true for the root of a queued copy
   */
  async function checkCopySynced(rec) {
    if (!rec || !rec.provisional || !rec.copyOf) return false;
    const ops = await store.peekOps();
    if (ops.some((o) => o.type === "item.copy" && o.itemId === rec.id && o.opId !== inflightOpId)) return true;
    throw invalid("This copy is still syncing. Try again when it is done.", "");
  }

  async function createFolder({ spaceId, parentId = "", name } = {}) {
    await ready;
    const folder = await locked(async () => {
      const space = await store.getSpace(spaceId);
      if (!space) throw new Error(`unknown space ${spaceId}`);
      const clean = String(name ?? "").trim();
      const problem = validateName(clean);
      if (problem) throw invalid(problem);
      const parent = await parentInfo(parentId);
      if (parentId && !parent.folder) throw invalid("The parent folder is not on this device.", "parentId");
      const records = await spaceRecords(space.id);
      if (namesIn(records, parent.path).some((n) => n.toLowerCase() === clean.toLowerCase())) throw invalid(`An item named “${clean}” already exists here.`);
      const user = await currentUser();
      const stamp = iso();
      const rec = {
        id: newId(),
        spaceId: space.id,
        spaceSlug: space.slug,
        parentId: parentId || "",
        name: clean,
        path: joinPath(parent.path, clean),
        position: nextPositionIn(records, parent.path),
        createdAt: stamp,
        updatedAt: stamp,
        createdBy: user ? user.id : "",
        deleted: false,
        rev: 0,
        access: await accessUnder(space, parent.folder && parent.folder.access),
        provisional: true,
      };
      await store.putFolders([rec]);
      await enqueueOp({ type: "folder.create", itemId: rec.id, kind: "folder", at: now(), body: { id: rec.id, spaceId: rec.spaceId, parentId: rec.parentId, name: rec.name } });
      return rec;
    });
    afterLocal([folder.id]);
    return folder;
  }

  /**
   * Stores a blob and a provisional file record, and queues its upload. Runs
   * under the lock.
   * @returns {Promise<object>} the file record
   */
  async function queueUpload({ space, parentId, dir, blob, name, noteId = "", pasteFor = "", records }) {
    const user = await currentUser();
    const id = newId();
    const stamp = iso();
    const parent = parentId ? await store.getFolder(parentId) : null;
    const rec = {
      id,
      spaceId: space.id,
      spaceSlug: space.slug,
      parentId: parentId || "",
      name,
      path: joinPath(dir, name),
      mime: (blob && blob.type) || "application/octet-stream",
      size: (blob && blob.size) || 0,
      sha256: "",
      position: nextPositionIn(records, dir),
      versions: 0,
      modTime: stamp,
      createdAt: stamp,
      updatedAt: stamp,
      createdBy: user ? user.id : "",
      updatedBy: user ? user.id : "",
      deleted: false,
      rev: 0,
      access: await accessUnder(space, parent && parent.access),
      url: fileUrl(space.slug, id, name),
      provisional: true,
      pendingKey: id,
    };
    records.push({ kind: "file", rec });
    await store.putBlob(id, blob);
    await store.putFiles([rec]);
    const body = { spaceId: space.id, name, onConflict: "rename" };
    if (pasteFor) body.pasteFor = pasteFor;
    else body.parentId = parentId || "";
    const op = { type: "file.upload", itemId: id, kind: "file", key: id, at: now(), body };
    uploadItems.set(id, {
      key: id,
      fileId: id,
      kind: "upload",
      noteId,
      spaceId: space.id,
      filename: name,
      size: rec.size,
      mime: rec.mime,
      loaded: 0,
      state: "queued",
      createdAt: now(),
    });
    await persistUploads();
    await enqueueOp(op);
    return rec;
  }

  /**
   * Uploads desktop files into a folder. Names that are taken get
   * "<stem> (N).<ext>".
   * @param {string} spaceId
   * @param {string} parentId a folder id or "" for the root
   * @param {(File|Blob)[]} files
   * @returns {Promise<object[]>} the provisional file records
   */
  async function uploadFiles(spaceId, parentId, files) {
    await ready;
    const recs = await locked(async () => {
      const space = await store.getSpace(spaceId);
      if (!space) throw new Error(`unknown space ${spaceId}`);
      const parent = await parentInfo(parentId);
      if (parentId && !parent.folder) throw invalid("The folder is not on this device.", "parentId");
      const records = await spaceRecords(space.id);
      const out = [];
      for (const blob of files || []) {
        let name = String((blob && blob.name) || "file").replace(/[/\\]/g, "_").trim() || "file";
        const problem = validateName(name, { kind: "file" });
        if (problem) throw invalid(`${name}: ${problem}`);
        name = freeName(namesIn(records, parent.path), name);
        out.push(await queueUpload({ space, parentId, dir: parent.path, blob, name, records }));
      }
      return out;
    });
    await refreshPending();
    emitUploads(true);
    afterLocal(recs.map((r) => r.id));
    schedulePush(0);
    return recs;
  }

  /**
   * Uploads a pasted or dropped file for a note into the note's folder, or
   * the space's pasteFolder below it. Resolves before any network call with
   * the destination to insert, relative to the note's folder and
   * percent-encoded as links.js writes it.
   * @param {string} noteId
   * @param {File|Blob} file
   * @returns {Promise<{url: string, pending: boolean, fileId: string, name: string}>}
   */
  async function uploadAttachment(noteId, file) {
    await ready;
    const result = await locked(async () => {
      const note = await store.getNote(noteId);
      if (!note) throw new Error(`note ${noteId} not found`);
      const space = await store.getSpace(note.spaceId);
      if (!space) throw new Error(`unknown space ${note.spaceId}`);
      const noteDir = dirOf(note.path);
      const paste = String(space.pasteFolder || "").replace(/^\/+|\/+$/g, "");
      const dir = paste ? joinPath(noteDir, paste) : noteDir;
      const records = await spaceRecords(space.id);
      const folder = paste ? records.find((t) => t.kind === "folder" && !t.rec.deleted && t.rec.path === dir) : null;
      const parentId = paste ? (folder ? folder.rec.id : "") : note.parentId && (await store.getFolder(note.parentId)) ? note.parentId : "";
      let name = String((file && file.name) || "file").replace(/[/\\]/g, "_").trim() || "file";
      if (validateName(name, { kind: "file" })) {
        const [, ext] = splitExt(name);
        name = `file${/\.md$/i.test(ext) ? ".txt" : ext.replace(/[^\w.]/g, "")}`;
      }
      name = freeName(namesIn(records, dir), name);
      // Without the paste folder on this device, or for a note the server
      // has not seen, pasteFor lets the server pick and create the folder.
      const usePasteFor = (paste && !folder) || note.provisional;
      const rec = await queueUpload({ space, parentId, dir, blob: file, name, noteId, pasteFor: usePasteFor ? noteId : "", records });
      return { url: relativeDest(noteDir, rec.path), fileId: rec.id, name };
    });
    await refreshPending();
    emitUploads(true);
    emitStatus();
    afterLocal([result.fileId]);
    schedulePush(0);
    return { url: result.url, pending: true, fileId: result.fileId, name: result.name };
  }

  /**
   * Renames a folder or a file in place, or retitles a note.
   * @param {string} id
   * @param {string} name
   * @returns {Promise<object>} the record
   */
  async function renameItem(id, name) {
    await ready;
    const found = await findItem(id);
    if (!found) throw new Error(`item ${id} not found`);
    if (found.kind === "note") return updateNote(id, { title: String(name ?? "").trim() || "Untitled" });
    const result = await locked(async () => {
      const cur = await findItem(id);
      const rec = cur.rec;
      const clean = String(name ?? "").trim();
      const problem = validateName(clean, { kind: cur.kind });
      if (problem) throw invalid(problem);
      if (clean === rec.name) return { rec, ids: [] };
      await checkCopySynced(rec);
      const records = await spaceRecords(rec.spaceId);
      const dir = dirOf(rec.path);
      if (namesIn(records, dir, id).some((n) => n.toLowerCase() === clean.toLowerCase())) throw invalid(`An item named “${clean}” already exists here.`);
      const tagged = records.filter((t) => t.rec.path === rec.path || String(t.rec.path).startsWith(rec.path + "/"));
      const self = tagged.find((t) => t.rec.id === id) || { kind: cur.kind, rec };
      self.rec.updatedAt = iso();
      const changed = repathRecords(tagged.map((t) => t.rec), rec.path, joinPath(dir, clean));
      self.rec.name = clean;
      if (cur.kind === "file") self.rec.url = fileUrl(self.rec.spaceSlug, id, clean);
      await putTagged(tagged.filter((t) => changed.includes(t.rec)));
      await enqueueOp({ type: cur.kind === "folder" ? "folder.rename" : "file.rename", itemId: id, kind: cur.kind, name: clean, at: now() });
      return { rec: self.rec, ids: changed.map((r) => r.id) };
    });
    if (result.ids.length) afterLocal(result.ids);
    return result.rec;
  }

  /**
   * Moves an item of any kind into a folder, or to the root with "".
   * @param {string} id
   * @param {string} parentId
   * @param {number} [position]
   * @returns {Promise<object>} the record
   */
  async function moveItem(id, parentId, position) {
    await ready;
    const found = await findItem(id);
    if (!found) throw new Error(`item ${id} not found`);
    if (found.kind === "note") {
      const pos = position === undefined ? await locked(async () => nextPositionIn(await spaceRecords(found.rec.spaceId), (await parentInfo(parentId)).path)) : position;
      return moveNote(id, parentId, pos);
    }
    const result = await locked(async () => {
      const cur = await findItem(id);
      const rec = cur.rec;
      await checkCopySynced(rec);
      const parent = await parentInfo(parentId);
      if (parentId && !parent.folder) throw invalid("The destination folder is not on this device.", "parentId");
      if (parent.folder && parent.folder.spaceId !== rec.spaceId) throw invalid("A move stays inside its space. Use Copy to for another space.", "parentId");
      if (cur.kind === "folder" && parent.folder && (parent.path === rec.path || parent.path.startsWith(rec.path + "/"))) {
        throw invalid("A folder cannot move into itself.", "parentId");
      }
      const records = await spaceRecords(rec.spaceId);
      const pos = position === undefined ? nextPositionIn(records, parent.path) : Number(position) || 0;
      if ((rec.parentId || "") === (parentId || "") && dirOf(rec.path) === parent.path) {
        if ((rec.position || 0) === pos) return { rec, ids: [] };
      } else if (namesIn(records, parent.path, id).some((n) => n.toLowerCase() === String(rec.name).toLowerCase())) {
        throw invalid(`An item named “${rec.name}” already exists there.`, "parentId");
      }
      const tagged = records.filter((t) => t.rec.path === rec.path || String(t.rec.path).startsWith(rec.path + "/"));
      const self = tagged.find((t) => t.rec.id === id) || { kind: cur.kind, rec };
      const changed = repathRecords(tagged.map((t) => t.rec), rec.path, joinPath(parent.path, rec.name));
      self.rec.parentId = parentId || "";
      self.rec.position = pos;
      if (!changed.includes(self.rec)) changed.push(self.rec);
      await putTagged(tagged.filter((t) => changed.includes(t.rec)));
      await enqueueOp({ type: "item.move", itemId: id, kind: cur.kind, parentId: parentId || "", position: pos, at: now() });
      return { rec: self.rec, ids: changed.map((r) => r.id) };
    });
    if (result.ids.length) afterLocal(result.ids);
    return result.rec;
  }

  /**
   * Copies an item into a folder of a space. The copy shows locally at once
   * with provisional ids. The server assigns the real ids, and the next pull
   * brings the copy with its children.
   * @param {string} id
   * @param {{spaceId?: string, parentId?: string, name?: string}} target
   * @returns {Promise<object>} the provisional copy record
   */
  async function copyItem(id, { spaceId, parentId = "", name } = {}) {
    await ready;
    const result = await locked(async () => {
      const src = await findItem(id);
      if (!src) throw new Error(`item ${id} not found`);
      await checkCopySynced(src.rec);
      const targetSpace = await store.getSpace(spaceId || src.rec.spaceId);
      if (!targetSpace) throw new Error(`unknown space ${spaceId}`);
      const parent = await parentInfo(parentId);
      if (parentId && !parent.folder) throw invalid("The destination folder is not on this device.", "parentId");
      if (src.kind === "folder" && targetSpace.id === src.rec.spaceId && (parent.path === src.rec.path || parent.path.startsWith(src.rec.path + "/"))) {
        throw invalid("A folder cannot be copied into itself.", "parentId");
      }
      const explicit = name !== undefined && String(name).trim() !== "";
      if (explicit) {
        const problem = validateName(String(name).trim(), { kind: src.kind === "note" ? "" : src.kind });
        if (problem) throw invalid(problem);
      }
      const targetRecords = await spaceRecords(targetSpace.id);
      const sourceRecords = src.rec.spaceId === targetSpace.id ? targetRecords : await spaceRecords(src.rec.spaceId);
      const stamp = iso();
      const user = await currentUser();
      const access = await accessUnder(targetSpace, parent.folder && parent.folder.access);
      const base = (rec, extra) => ({
        ...rec,
        spaceId: targetSpace.id,
        spaceSlug: targetSpace.slug,
        createdAt: stamp,
        updatedAt: stamp,
        createdBy: user ? user.id : "",
        deleted: false,
        rev: 0,
        access,
        provisional: true,
        copyOf: rec.id,
        ...extra,
      });
      let rootName;
      let root;
      if (src.kind === "note") {
        const notes = targetRecords.filter((t) => t.kind === "note").map((t) => t.rec);
        let title = explicit ? String(name).trim() : src.rec.title || "Untitled";
        let path = allocPath(notes, parent.path, title);
        if (!explicit && path !== joinPath(parent.path, `${slugify(title)}.md`)) {
          const stem = title;
          title = `${stem} copy`;
          for (let n = 2; notes.some((x) => !x.deleted && x.path.toLowerCase() === joinPath(parent.path, `${slugify(title)}.md`).toLowerCase()); n++) title = `${stem} copy ${n}`;
          path = allocPath(notes, parent.path, title);
        }
        root = { kind: "note", rec: base(src.rec, { id: newId(), parentId: parentId || "", title, path, slug: baseOf(path).replace(/\.md$/, ""), position: nextPositionIn(targetRecords, parent.path), starred: false, serverUpdatedAt: null }) };
      } else {
        const taken = namesIn(targetRecords, parent.path);
        rootName = explicit ? String(name).trim() : copyName(taken, src.rec.name, src.kind === "folder");
        if (explicit && taken.some((n) => n.toLowerCase() === rootName.toLowerCase())) throw invalid(`An item named “${rootName}” already exists there.`);
        const path = joinPath(parent.path, rootName);
        const extra = { id: newId(), parentId: parentId || "", name: rootName, path, position: nextPositionIn(targetRecords, parent.path) };
        if (src.kind === "file") Object.assign(extra, { versions: 0, url: src.rec.url });
        root = { kind: src.kind, rec: base(src.rec, extra) };
      }
      const copies = [root];
      if (src.kind === "folder") {
        const ids = new Map([[src.rec.id, root.rec.id]]);
        const below = sourceRecords
          .filter((t) => !t.rec.deleted && String(t.rec.path).startsWith(src.rec.path + "/"))
          .sort((a, b) => String(a.rec.path).split("/").length - String(b.rec.path).split("/").length);
        for (const t of below) {
          const newIdValue = newId();
          ids.set(t.rec.id, newIdValue);
          const path = root.rec.path + String(t.rec.path).slice(src.rec.path.length);
          const extra = { id: newIdValue, parentId: ids.get(t.rec.parentId) || root.rec.id, path };
          if (t.kind === "note") Object.assign(extra, { starred: false, serverUpdatedAt: null, slug: baseOf(path).replace(/\.md$/, "") });
          if (t.kind === "file") Object.assign(extra, { versions: 0, url: t.rec.url });
          copies.push({ kind: t.kind, rec: base(t.rec, extra) });
        }
      }
      await putTagged(copies);
      const body = { spaceId: targetSpace.id, parentId: parentId || "" };
      if (explicit) body.name = String(name).trim();
      else if (rootName && rootName !== src.rec.name) body.name = rootName;
      await enqueueOp({ type: "item.copy", itemId: root.rec.id, sourceId: id, kind: src.kind, copies: copies.map((t) => t.rec.id), at: now(), body });
      return { rec: root.rec, ids: copies.map((t) => t.rec.id) };
    });
    afterLocal(result.ids);
    return result.rec;
  }

  /**
   * Trashes or restores an item and everything below it locally, then
   * queues the op. Notes use the note routes.
   * @param {string} id
   * @param {boolean} deleted
   */
  async function setItemDeleted(id, deleted) {
    await ready;
    const found = await findItem(id);
    if (!found) throw new Error(`item ${id} not found`);
    if (found.kind === "note") return setDeleted(id, deleted, deleted ? "trash" : "restore");
    const ids = await locked(async () => {
      const cur = await findItem(id);
      const rec = cur.rec;
      if (deleted && (await checkCopySynced(rec))) {
        // A copy that never reached the server is simply dropped.
        const ops = await store.peekOps();
        const op = ops.find((o) => o.type === "item.copy" && o.itemId === id);
        await store.replaceOps(ops.filter((o) => o !== op));
        await store.deleteItems(op.copies || [id]);
        return op.copies || [id];
      }
      const records = await spaceRecords(rec.spaceId);
      const stamp = iso();
      const written = [];
      for (const t of records) {
        const under = t.rec.id === id || String(t.rec.path).startsWith(rec.path + "/");
        if (!under || Boolean(t.rec.deleted) === deleted) continue;
        if (!deleted && t.rec.id !== id && rec.deletedAt && t.rec.deletedAt && t.rec.deletedAt !== rec.deletedAt) continue;
        t.rec.deleted = deleted;
        if (deleted) t.rec.deletedAt = stamp;
        else delete t.rec.deletedAt;
        written.push(t);
      }
      await putTagged(written);
      await enqueueOp({ type: deleted ? "item.trash" : "item.restore", itemId: id, kind: cur.kind, at: now() });
      return written.length ? written.map((t) => t.rec.id) : [id];
    });
    afterLocal(ids);
  }

  /**
   * Deletes an item and everything below it for good.
   * @param {string} id
   */
  async function deleteItem(id) {
    await ready;
    const found = await findItem(id);
    if (!found) return;
    if (found.kind === "note") return deleteNote(id);
    const ids = await locked(async () => {
      const cur = await findItem(id);
      if (!cur) return [];
      const rec = cur.rec;
      const records = await spaceRecords(rec.spaceId);
      const descendants = records.filter((t) => String(t.rec.path).startsWith(rec.path + "/")).map((t) => t.rec.id);
      const all = [id, ...descendants];
      for (const key of all) {
        const item = uploadItems.get(key);
        if (item) await discardUpload(key);
      }
      await store.deleteItems(all);
      for (const x of all) await store.setMeta(SERVER_COPY + x, undefined);
      const ops = await store.peekOps();
      const copy = ops.find((o) => o.type === "item.copy" && o.itemId === id && o.opId !== inflightOpId);
      if (copy) {
        await store.replaceOps(ops.filter((o) => o !== copy));
        await store.deleteItems(copy.copies || []);
        return all;
      }
      await enqueueOp({ type: "item.delete", itemId: id, kind: cur.kind, descendants, at: now() });
      return all;
    });
    if (ids.length) afterLocal(ids);
  }

  /**
   * Replaces the content of a file. The new content shows locally at once
   * and uploads with the hash the replacement was based on.
   * @param {string} id
   * @param {File|Blob} blob
   * @returns {Promise<object>} the record
   */
  async function replaceFile(id, blob) {
    await ready;
    const rec = await locked(async () => {
      const file = await store.getFile(id);
      if (!file) throw new Error(`file ${id} not found`);
      await checkCopySynced(file);
      const key = newId();
      const prev = { name: file.name, path: file.path, size: file.size, mime: file.mime, updatedAt: file.updatedAt, sha256: file.sha256, url: file.url, pendingKey: file.pendingKey };
      await store.putBlob(key, blob);
      const [, oldExt] = splitExt(file.name);
      const [, newExt] = splitExt((blob && blob.name) || "");
      let name = file.name;
      if (newExt && newExt.toLowerCase() !== oldExt.toLowerCase() && !/\.md$/i.test(newExt)) {
        const [stem] = splitExt(file.name);
        name = `${stem}${newExt}`;
      }
      file.size = (blob && blob.size) || 0;
      file.mime = (blob && blob.type) || file.mime;
      file.updatedAt = iso();
      if (name !== file.name) {
        file.path = joinPath(dirOf(file.path), name);
        file.name = name;
        file.url = fileUrl(file.spaceSlug, file.id, name);
      }
      file.pendingKey = key;
      const op = { type: "file.replace", itemId: id, kind: "file", key, baseSha256: file.sha256 || "", name, prev, at: now() };
      uploadItems.set(key, { key, fileId: id, kind: "replace", noteId: "", spaceId: file.spaceId, filename: name, size: file.size, mime: file.mime, loaded: 0, state: "queued", createdAt: now() });
      await persistUploads();
      const { coalesced } = await enqueueOp(op);
      if (coalesced && coalesced.key) file.pendingKey = coalesced.key;
      await store.putFiles([file]);
      return file;
    });
    await refreshPending();
    emitUploads(true);
    afterLocal([id]);
    schedulePush(0);
    return rec;
  }

  /**
   * Forgets an upload item and its blob. Runs under the lock.
   * @param {string} key
   */
  async function discardUpload(key) {
    await store.deleteBlob(key);
    if (uploadItems.delete(key)) await persistUploads();
  }

  async function retryUpload(key) {
    await ready;
    const kick = await locked(async () => {
      const item = uploadItems.get(key);
      if (!item) return false;
      if (item.state === "failed") {
        if (!(await store.getBlob(key)) || !item.op) {
          await discardUpload(key);
          return false;
        }
        item.state = "queued";
        item.loaded = 0;
        delete item.error;
        await persistUploads();
        const { opId, ...op } = item.op;
        await enqueueOp({ ...op, at: now() });
        if (op.type === "file.upload") {
          const rec = await store.getFile(op.itemId);
          if (rec && rec.uploadFailed) {
            delete rec.uploadFailed;
            await store.putFiles([rec]);
          }
        }
      }
      return true;
    });
    emitUploads(true);
    if (kick) {
      resetBackoff();
      await refreshPending();
      emitStatus();
      schedulePush(0);
    }
  }

  async function cancelUpload(key) {
    await ready;
    const ids = await locked(async () => {
      const item = uploadItems.get(key);
      if (!item) return [];
      const ops = await store.peekOps();
      const op = ops.find((o) => (o.type === "file.upload" || o.type === "file.replace") && o.key === key);
      if (op && op.opId === inflightOpId) {
        item.cancelRequested = true;
        return [];
      }
      if (op) await store.removeOp(op.opId);
      return finishCancel(item, op || item.op || null);
    });
    await refreshPending();
    emitUploads(true);
    emitStatus();
    if (ids.length) afterLocal(ids);
  }

  /**
   * Removes a cancelled upload's blob and item. An upload loses its
   * provisional record and the link in its note. A replacement puts the
   * previous metadata back. Runs under the lock.
   * @param {object} item
   * @param {object|null} op
   * @returns {Promise<string[]>} ids of records written
   */
  async function finishCancel(item, op) {
    await discardUpload(item.key);
    const ids = [];
    const file = await store.getFile(item.fileId);
    if (item.kind === "replace") {
      if (file && op && op.prev) {
        Object.assign(file, op.prev);
        if (!op.prev.pendingKey) delete file.pendingKey;
        await store.putFiles([file]);
        ids.push(file.id);
      }
      return ids;
    }
    if (file && file.provisional) {
      await store.deleteFiles([file.id]);
      ids.push(file.id);
      const note = item.noteId ? await store.getNote(item.noteId) : null;
      if (note) {
        const next = removeFileLinks(note.content, dirOf(note.path), file.path, file.name);
        if (next !== note.content) ids.push(...(await applyLocalUpdate(note, { content: next })).map((n) => n.id));
      }
    }
    return ids;
  }

  // -------------------------------------------------------------------------
  // Outbox replay

  function resetBackoff() {
    failures = 0;
    if (retryTimer) {
      timers.clearTimeout(retryTimer);
      retryTimer = null;
    }
  }

  function schedulePush(delay = PUSH_DEBOUNCE_MS) {
    if (!started || retryTimer) return;
    if (pushTimer) timers.clearTimeout(pushTimer);
    pushTimer = timers.setTimeout(() => {
      pushTimer = null;
      push();
    }, delay);
  }

  function scheduleRetry() {
    failures++;
    const delay = Math.min(BACKOFF_MAX_MS, BACKOFF_MIN_MS * 2 ** (failures - 1));
    if (!started) return;
    if (retryTimer) timers.clearTimeout(retryTimer);
    retryTimer = timers.setTimeout(() => {
      retryTimer = null;
      push();
    }, delay);
  }

  function canPush() {
    return Boolean(online()) && !authRequired;
  }

  function push() {
    if (pushing) {
      pushAgain = true;
      return pushing;
    }
    pushing = (async () => {
      emitStatus();
      try {
        do {
          pushAgain = false;
          await withOutboxLock(drain);
        } while (pushAgain && canPush() && !retryTimer);
      } finally {
        pushing = null;
        await refreshPending().catch(() => {});
        emitStatus();
        if (pullAgain && started) {
          pullAgain = false;
          pull();
        }
      }
    })();
    return pushing;
  }

  /**
   * Runs fn while holding the Web Lock that keeps two engines on the same
   * outbox, in one page or in two tabs, from replaying it at once. When
   * another engine holds the lock, this call waits for it and then replays
   * whatever is left, so syncNow resolves only after the outbox was drained.
   * Without the Web Locks API fn runs directly.
   * @param {() => Promise<void>} fn
   * @returns {Promise<void>}
   */
  async function withOutboxLock(fn) {
    if (!locks || typeof locks.request !== "function") return fn();
    await locks.request("markspace-outbox", () => fn());
  }

  async function drain() {
    while (canPush()) {
      const op = await locked(async () => {
        const ops = await store.peekOps();
        inflightOpId = ops.length ? ops[0].opId : null;
        return ops[0] || null;
      });
      if (!op) return;
      let outcome;
      try {
        outcome = await sendOp(op);
      } catch (err) {
        outcome = await handleError(op, err);
      } finally {
        inflightOpId = null;
      }
      await refreshPending();
      emitStatus();
      if (outcome === "retry") {
        scheduleRetry();
        return;
      }
      if (outcome === "stop") return;
      failures = 0;
    }
  }

  /** Returns the record inside an item response, or the response itself. */
  function recordOf(res) {
    if (res && typeof res === "object" && typeof res.kind === "string" && (res.folder || res.note || res.file)) {
      return res.folder || res.note || res.file;
    }
    return res;
  }

  async function sendOp(op) {
    const id = op.noteId;
    const itemPath = op.itemId ? `/api/items/${enc(op.itemId)}` : "";
    switch (op.type) {
      case "create":
        await ackNote(op, await api("POST", "/api/notes", op.body));
        break;
      case "update": {
        const body = { ...op.fields };
        if (op.baseUpdatedAt) body.baseUpdatedAt = op.baseUpdatedAt;
        await ackNote(op, await api("PATCH", notePath(id), body));
        break;
      }
      case "move":
        await ackNote(op, await api("POST", `${notePath(id)}/move`, { parentId: op.parentId || "", position: op.position || 0 }));
        break;
      case "trash":
      case "restore":
        await api("POST", `${notePath(id)}/${op.type}`);
        await ackSimple(op);
        break;
      case "delete":
        await api("DELETE", notePath(id));
        await ackSimple(op);
        break;
      case "star":
        await api(op.starred ? "PUT" : "DELETE", `${notePath(id)}/star`);
        await ackSimple(op);
        break;
      case "folder.create":
        await ackItem(op, await api("POST", "/api/folders", op.body));
        break;
      case "folder.rename":
        await ackItem(op, await api("PATCH", `/api/folders/${enc(op.itemId)}`, { name: op.name }));
        break;
      case "file.rename":
        await ackItem(op, await api("PATCH", `/api/files/${enc(op.itemId)}`, { name: op.name }));
        break;
      case "file.upload":
      case "file.replace":
        await sendFile(op);
        break;
      case "item.move":
        await ackItem(op, recordOf(await api("POST", `${itemPath}/move`, { parentId: op.parentId || "", position: op.position || 0 })));
        break;
      case "item.restore":
        await ackItem(op, recordOf(await api("POST", `${itemPath}/restore`)));
        break;
      case "item.trash":
        await api("POST", `${itemPath}/trash`);
        await ackItemSimple(op);
        break;
      case "item.delete":
        await api("DELETE", itemPath);
        await ackItemSimple(op);
        break;
      case "item.copy":
        await ackCopy(op, await api("POST", `/api/items/${enc(op.sourceId)}/copy`, op.body));
        break;
      default:
        // Ops of an earlier client version, such as attachment uploads.
        await locked(() => store.removeOp(op.opId));
    }
    lastError = null;
    return "done";
  }

  /**
   * Removes a sent op from the outbox. Another engine on the same store, such
   * as a second tab, may have merged newer values into the op while it was in
   * flight. Those values stay queued: a create or update becomes an update of
   * the fields that differ from what was sent, rebased on updatedAt, and a
   * star keeps its newer state. Runs under the lock.
   * @param {object} sent the op as it was sent
   * @param {string|null} updatedAt the server's updatedAt from the reply
   */
  async function settleSentOp(sent, updatedAt) {
    const ops = await store.peekOps();
    const current = ops.find((o) => o.opId === sent.opId);
    if (!current) return;
    const payload = (o) => JSON.stringify([o.type, o.body, o.fields, o.starred, o.parentId, o.position, o.name, o.key]);
    if (payload(current) === payload(sent)) {
      await store.removeOp(sent.opId);
      return;
    }
    if (current.type === "star" && sent.type === "star") return;
    if ((current.type === "create" || current.type === "update") && (sent.type === "create" || sent.type === "update")) {
      const sentFields = (sent.type === "create" ? sent.body : sent.fields) || {};
      const nowFields = (current.type === "create" ? current.body : current.fields) || {};
      const diff = {};
      for (const k of NOTE_FIELDS) {
        if (k in nowFields && !sameField(k, nowFields[k], sentFields[k])) diff[k] = nowFields[k];
      }
      if (Object.keys(diff).length) {
        const next = { opId: current.opId, type: "update", noteId: current.noteId, fields: diff, baseUpdatedAt: updatedAt || null, at: current.at };
        await store.replaceOps(ops.map((o) => (o.opId === current.opId ? next : o)));
        return;
      }
    }
    if (CREATE_OPS.has(sent.type) && current.type === sent.type && current.body && sent.body) {
      const follow = [];
      const kind = current.kind;
      if (current.body.name !== undefined && current.body.name !== sent.body.name) {
        follow.push({ type: kind === "folder" ? "folder.rename" : "file.rename", itemId: current.itemId, kind, name: current.body.name, at: current.at });
      }
      if ((current.body.parentId || "") !== (sent.body.parentId || "")) {
        follow.push({ type: "item.move", itemId: current.itemId, kind, parentId: current.body.parentId || "", position: current.body.position || 0, at: current.at });
      }
      if (follow.length) {
        const i = ops.findIndex((o) => o.opId === current.opId);
        ops.splice(i, 1, { ...follow[0], opId: current.opId }, ...follow.slice(1));
        await store.replaceOps(ops);
        return;
      }
    }
    if (ITEM_OPS.includes(sent.type) && !CREATE_OPS.has(sent.type)) return;
    await store.removeOp(sent.opId);
  }

  /**
   * Stores the note a 2xx returned and removes the op. When more ops for the
   * note are queued, only server bookkeeping fields change locally and the
   * queued updates are rebased on the new updatedAt.
   * @param {object} op
   * @param {object} res
   */
  async function ackNote(op, res) {
    if (!res || typeof res !== "object" || !res.id) {
      await ackSimple(op);
      return;
    }
    const ids = await locked(async () => {
      await settleSentOp(op, res.updatedAt);
      const ops = await store.peekOps();
      const rest = ops.filter((o) => o.noteId === op.noteId);
      const local = await store.getNote(op.noteId);
      if (!local) return [];
      let stored;
      if (!rest.length) {
        let server = res;
        const aside = await store.getMeta(SERVER_COPY + op.noteId);
        if (aside && (aside.rev || 0) > (res.rev || 0)) server = aside;
        await store.setMeta(SERVER_COPY + op.noteId, undefined);
        stored = fromServer(server, local);
      } else {
        stored = {
          ...local,
          serverUpdatedAt: res.updatedAt || local.serverUpdatedAt,
          rev: Math.max(local.rev || 0, res.rev || 0),
          createdAt: res.createdAt || local.createdAt,
          createdBy: res.createdBy || local.createdBy,
          provisional: false,
        };
        if (res.path && res.title === local.title && (res.parentId || "") === (local.parentId || "")) {
          stored.path = res.path;
          stored.slug = res.slug || stored.slug;
        }
        let rebased = false;
        for (const o of ops) {
          if (o.noteId === op.noteId && o.type === "update" && res.updatedAt) {
            o.baseUpdatedAt = res.updatedAt;
            rebased = true;
          }
        }
        if (rebased) await store.replaceOps(ops);
      }
      await store.putNotes([stored]);
      return [stored.id];
    });
    if (op.type === "create" || op.type === "move") pullAgain = true;
    if (ids.length) emit("notes-changed", { ids, source: "remote" });
  }

  /**
   * Removes an op whose response carries no note. When it was the last op for
   * the note, a server copy stored by a pull is applied.
   * @param {object} op
   */
  async function ackSimple(op) {
    const ids = await locked(async () => {
      await settleSentOp(op, null);
      return applyAsideIfIdle(op.noteId);
    });
    if (ids.length) emit("notes-changed", { ids, source: "remote" });
  }

  /**
   * Applies the server copy stored beside an item once no ops for it remain.
   * Runs under the lock.
   * @param {string} id
   * @returns {Promise<string[]>}
   */
  async function applyAsideIfIdle(id) {
    const ops = await store.peekOps();
    if (ops.some((o) => opItem(o) === id)) return [];
    const aside = await store.getMeta(SERVER_COPY + id);
    if (!aside) return [];
    await store.setMeta(SERVER_COPY + id, undefined);
    const kind = aside.kind === "folder" || aside.kind === "file" ? aside.kind : "note";
    const found = await findItem(id);
    const local = found ? found.rec : null;
    if (local && (local.rev || 0) > (aside.rev || 0)) return [];
    if (!local && aside.deleted === undefined) return [];
    if (kind === "note") {
      await store.putNotes([fromServer(aside, local)]);
    } else {
      const space = await store.getSpace(aside.spaceId);
      await putKind(kind, [fromServerItem(kind, aside, local, space, await currentUser())]);
    }
    return [id];
  }

  /**
   * Stores the folder or file a 2xx returned and removes the op. A changed
   * path moves the local paths below it. Queued ops for the item keep their
   * local values.
   * @param {object} op
   * @param {object} res
   * @param {(rec: object, local: object) => Promise<string[]>} [after] runs under the lock
   */
  async function ackItem(op, res, after = null) {
    if (!res || typeof res !== "object" || !res.id) {
      await ackItemSimple(op);
      return;
    }
    const kind = op.kind;
    const ids = await locked(async () => {
      await settleSentOp(op, res.updatedAt);
      const ops = await store.peekOps();
      const rest = ops.filter((o) => o.itemId === op.itemId);
      const found = await findItem(op.itemId);
      const local = found ? found.rec : null;
      if (!local) return [];
      const space = await store.getSpace(res.spaceId || local.spaceId);
      const user = await currentUser();
      let stored;
      if (!rest.length) {
        let server = res;
        const aside = await store.getMeta(SERVER_COPY + op.itemId);
        if (aside && (aside.rev || 0) > (res.rev || 0)) server = aside;
        await store.setMeta(SERVER_COPY + op.itemId, undefined);
        stored = fromServerItem(kind, server, local, space, user);
        if (kind === "file" && local.pendingKey && op.type !== "file.upload" && op.type !== "file.replace") stored.pendingKey = local.pendingKey;
      } else {
        stored = { ...local, rev: Math.max(local.rev || 0, res.rev || 0), createdAt: res.createdAt || local.createdAt, provisional: false };
        if (kind === "file") {
          stored.sha256 = res.sha256 || local.sha256;
          if (!rest.some((o) => o.type === "file.rename" || o.type === "item.move")) {
            stored.path = res.path || stored.path;
            stored.name = res.name || stored.name;
          }
        } else if (!rest.some((o) => o.type === "folder.rename" || o.type === "item.move")) {
          stored.path = res.path || stored.path;
          stored.name = res.name || stored.name;
        }
      }
      const written = [{ kind, rec: stored }];
      if (kind === "folder" && local.path && stored.path && local.path !== stored.path) {
        const records = (await spaceRecords(local.spaceId)).filter((t) => t.rec.id !== stored.id && String(t.rec.path).startsWith(local.path + "/"));
        repathRecords(records.map((t) => t.rec), local.path, stored.path);
        written.push(...records);
      }
      await putTagged(written);
      const extra = after ? await after(stored, local) : [];
      return [...written.map((t) => t.rec.id), ...extra];
    });
    pullAgain = true;
    if (ids.length) emit("notes-changed", { ids, source: "remote" });
  }

  /**
   * Removes an item op whose response carries no item, and applies a server
   * copy stored beside the item once no ops for it remain.
   * @param {object} op
   */
  async function ackItemSimple(op) {
    const ids = await locked(async () => {
      await store.removeOp(op.opId);
      return applyAsideIfIdle(op.itemId);
    });
    pullAgain = true;
    if (ids.length) emit("notes-changed", { ids, source: "remote" });
  }

  /**
   * Replaces the provisional copy with the item the server made. The next
   * pull brings the children of a folder copy.
   * @param {object} op
   * @param {object} res
   */
  async function ackCopy(op, res) {
    const rec = recordOf(res);
    const kind = (res && res.kind) || op.kind;
    const ids = await locked(async () => {
      await store.removeOp(op.opId);
      const copies = op.copies || [op.itemId];
      await store.deleteItems(copies);
      if (!rec || !rec.id) return copies;
      const space = await store.getSpace(rec.spaceId);
      if (kind === "note") await store.putNotes([fromServer(rec, null)]);
      else await putKind(kind, [fromServerItem(kind, rec, null, space, await currentUser())]);
      return [...copies, rec.id];
    });
    pullAgain = true;
    emit("copied", { sourceId: op.sourceId, copyId: rec && rec.id, provisionalId: op.itemId, kind });
    emit("notes-changed", { ids, source: "remote" });
  }

  /**
   * Sends a file upload or a replacement with its blob and reports progress.
   * @param {object} op
   */
  async function sendFile(op) {
    const replace = op.type === "file.replace";
    let item = uploadItems.get(op.key);
    const blob = await store.getBlob(op.key);
    if (!blob) {
      await locked(async () => {
        await store.removeOp(op.opId);
        if (item) {
          item.state = "failed";
          item.error = "the file is no longer stored on this device";
          await persistUploads();
        }
      });
      emitUploads(true);
      return;
    }
    const local = await store.getFile(op.itemId);
    if (!item) {
      item = {
        key: op.key, fileId: op.itemId, kind: replace ? "replace" : "upload", noteId: (op.body && op.body.pasteFor) || "",
        spaceId: (op.body && op.body.spaceId) || (local && local.spaceId) || "", filename: (local && local.name) || blob.name || "file",
        size: blob.size || 0, mime: blob.type || "", loaded: 0, state: "queued", createdAt: now(),
      };
      uploadItems.set(op.key, item);
    }
    item.state = "uploading";
    item.loaded = 0;
    delete item.error;
    emitUploads(true);
    const onProgress = (loaded) => {
      item.loaded = Math.min(Number(loaded) || 0, item.size || Number(loaded) || 0);
      emitUploads(false);
    };
    const name = (replace ? op.name : op.body.name) || blob.name || "file";
    let res;
    if (replace) {
      res = await upload(`/api/files/${enc(op.itemId)}/content`, blob, { method: "PUT", fields: { baseSha256: op.baseSha256 || "" }, filename: name, onProgress });
    } else {
      const fields = { spaceId: op.body.spaceId };
      if (op.body.pasteFor) fields.pasteFor = op.body.pasteFor;
      else fields.parentId = op.body.parentId || "";
      fields.id = op.itemId;
      if (op.body.onConflict) fields.onConflict = op.body.onConflict;
      res = await upload("/api/files", blob, { fields, filename: name, onProgress });
    }
    if (item.cancelRequested) {
      // The server holds the file now. A cancelled upload is deleted again,
      // and a cancelled replacement stays, because its version is kept.
      const ids = await locked(async () => {
        await store.removeOp(op.opId);
        await discardUpload(op.key);
        if (!replace) {
          await enqueueOp({ type: "item.delete", itemId: op.itemId, kind: "file", descendants: [], at: now() });
          await store.deleteFiles([op.itemId]);
          const note = item.noteId ? await store.getNote(item.noteId) : null;
          if (note && local) {
            const next = removeFileLinks(note.content, dirOf(note.path), local.path, name);
            if (next !== note.content) await applyLocalUpdate(note, { content: next });
          }
          return [op.itemId, item.noteId].filter(Boolean);
        }
        return [];
      });
      emitUploads(true);
      if (ids.length) emit("notes-changed", { ids, source: "local" });
      return;
    }
    await ackItem({ ...op, kind: "file" }, res, async (stored, before) => {
      await store.deleteBlob(op.key);
      uploadItems.delete(op.key);
      await persistUploads();
      const { cancelRequested, op: saved, ...rest } = item;
      doneUploads.push({ ...rest, state: "done", loaded: item.size, url: stored.url });
      while (doneUploads.length > DONE_UPLOADS_KEPT) doneUploads.shift();
      const current = await store.getFile(stored.id);
      if (current && current.pendingKey === op.key) {
        delete current.pendingKey;
        await store.putFiles([current]);
      }
      // The server may have picked another name. The note that pasted the
      // file then gets the new destination.
      const noteId = item.noteId;
      if (!replace && noteId && before && before.path && stored.path && before.path !== stored.path) {
        const note = await store.getNote(noteId);
        if (note) {
          const next = replaceFileLinks(note.content, dirOf(note.path), before.path, stored.path);
          if (next !== note.content) return (await applyLocalUpdate(note, { content: next })).map((n) => n.id);
        }
      }
      return [];
    });
    emitUploads(true);
    emit("file-stored", { fileId: op.itemId, replaced: replace });
  }

  /**
   * Maps a failed request to an outcome: "done" removes or settles the op,
   * "retry" keeps it with backoff, "stop" pauses replay.
   * @param {object} op
   * @param {any} err
   * @returns {Promise<string>}
   */
  async function handleError(op, err) {
    const status = statusOf(err);
    const message = (err && err.message) || String(err);
    const fileOp = op.type === "file.upload" || op.type === "file.replace";
    if (fileOp) {
      const item = uploadItems.get(op.key);
      if (item && item.cancelRequested) {
        const ids = await locked(async () => {
          await store.removeOp(op.opId);
          return finishCancel(item, op);
        });
        emitUploads(true);
        if (ids.length) emit("notes-changed", { ids, source: "local" });
        return "done";
      }
    }
    if (status < 0 || retryable(status)) {
      lastError = message;
      if (fileOp) await markUploadQueued(op.key, message);
      return "retry";
    }
    if (status === 401) {
      if (fileOp) await markUploadQueued(op.key, message);
      setAuthRequired();
      return "stop";
    }
    switch (op.type) {
      case "create":
        return rejectCreate(op, status, message);
      case "update":
        if (status === 409) return resolveConflict(op);
        if (status === 404) return goneToCopy(op);
        return rejectOp(op, status, message);
      case "move":
      case "trash":
        if (status === 404) return goneToCopy(op);
        return rejectOp(op, status, message);
      case "delete":
        if (status === 404) {
          await ackSimple(op);
          return "done";
        }
        return rejectOp(op, status, message);
      case "folder.create":
        if (status === 409) return renameCreatedFolder(op);
        return rejectItemCreate(op, status, message);
      case "file.upload":
        return failUpload(op, status, message);
      case "file.replace":
        if (status === 409) return replaceConflict(op);
        return failUpload(op, status, message);
      case "item.copy":
        return rejectItemCreate(op, status, message);
      case "item.delete":
        if (status === 404) {
          await ackItemSimple(op);
          return "done";
        }
        return rejectItemOp(op, status, message);
      case "folder.rename":
      case "file.rename":
      case "item.move":
      case "item.trash":
      case "item.restore":
        return rejectItemOp(op, status, message);
      default:
        if (NOTE_OPS.has(op.type)) return rejectOp(op, status, message);
        await locked(() => store.removeOp(op.opId));
        emit("rejected", { op, status, message });
        return "done";
    }
  }

  /**
   * Fetches the server copy of a note. Returns {note}, {gone: true}, or
   * {outcome} when the fetch itself must retry or stop.
   * @param {string} id
   * @returns {Promise<object>}
   */
  async function fetchServerNote(id) {
    try {
      return { note: await api("GET", notePath(id)) };
    } catch (err) {
      const status = statusOf(err);
      if (status === 404) return { gone: true };
      if (status === 401) {
        setAuthRequired();
        return { outcome: "stop" };
      }
      if (status < 0 || retryable(status)) {
        lastError = (err && err.message) || String(err);
        return { outcome: "retry" };
      }
      return { unavailable: true };
    }
  }

  /**
   * Fetches the server copy of any item. Returns {kind, rec}, {gone: true},
   * {outcome} or {unavailable: true}.
   * @param {string} id
   * @returns {Promise<object>}
   */
  async function fetchServerItem(id) {
    try {
      const res = await api("GET", `/api/items/${enc(id)}`);
      const rec = recordOf(res);
      if (!rec || !rec.id) return { unavailable: true };
      if (res.access && !rec.access) rec.access = res.access;
      return { kind: res.kind, rec };
    } catch (err) {
      const status = statusOf(err);
      if (status === 404) return { gone: true };
      if (status === 401) {
        setAuthRequired();
        return { outcome: "stop" };
      }
      if (status < 0 || retryable(status)) {
        lastError = (err && err.message) || String(err);
        return { outcome: "retry" };
      }
      return { unavailable: true };
    }
  }

  /**
   * Saves text that could not reach the server in the recovered store.
   * Runs under the lock.
   * @param {object} note
   * @param {string} reason
   * @returns {Promise<{id: string, title: string}>}
   */
  async function recover(note, reason) {
    const item = {
      id: newId(),
      noteId: note.id,
      spaceId: note.spaceId,
      parentId: note.parentId || "",
      title: note.title,
      content: note.content || "",
      tags: note.tags || [],
      reason,
      createdAt: iso(),
    };
    await store.putRecovered(item);
    return { id: item.id, title: item.title };
  }

  /**
   * Handles a 400, 403 or other refusal: removes the op, restores the server
   * copy and emits rejected. Refused text that differs from the server copy
   * is kept in the recovered store.
   */
  async function rejectOp(op, status, message) {
    const fetched = await fetchServerNote(op.noteId);
    if (fetched.outcome) return fetched.outcome;
    const result = await locked(async () => {
      await store.removeOp(op.opId);
      const local = await store.getNote(op.noteId);
      const ops = await store.peekOps();
      const rest = ops.filter((o) => o.noteId === op.noteId);
      let recovered = null;
      if (op.type === "update" && local) {
        const server = fetched.note || null;
        const differs = !server || NOTE_FIELDS.some((k) => k in (op.fields || {}) && !sameField(k, local[k], server[k]));
        if (differs) recovered = await recover(local, `rejected with status ${status}: ${message}`);
      }
      const ids = [];
      if (fetched.gone) {
        if (!rest.length && local) {
          await store.deleteNotes([op.noteId]);
          ids.push(op.noteId);
        }
      } else if (fetched.note) {
        if (rest.length) {
          await store.setMeta(SERVER_COPY + op.noteId, fetched.note);
        } else {
          const restored = fromServer(fetched.note, local);
          if (status === 403 && !accessOf(fetched.note.access)) restored.access = "view";
          if (op.type === "star" && local) restored.starred = !op.starred;
          await store.putNotes([restored]);
          ids.push(op.noteId);
        }
      }
      return { ids, recovered };
    });
    emit("rejected", { op, status, message });
    if (result.recovered) emit("recovered", result.recovered);
    if (result.ids.length) emit("notes-changed", { ids: result.ids, source: "remote" });
    return "done";
  }

  /**
   * Handles a refused create: the note never reached the server, so its text
   * moves to the recovered store and the local note is removed.
   */
  async function rejectCreate(op, status, message) {
    const result = await locked(async () => {
      const ops = await store.peekOps();
      await store.replaceOps(ops.filter((o) => o.noteId !== op.noteId));
      const local = await store.getNote(op.noteId);
      const base = local || { id: op.noteId, ...op.body };
      const recovered = await recover(base, `create rejected with status ${status}: ${message}`);
      if (local) await store.deleteNotes([op.noteId]);
      return recovered;
    });
    emit("rejected", { op, status, message });
    emit("recovered", result);
    emit("notes-changed", { ids: [op.noteId], source: "remote" });
    return "done";
  }

  /**
   * Handles a 409 on an update. When the server already holds the op's
   * values the op is settled. Otherwise the local text becomes a conflict
   * copy and the original takes the server version.
   */
  async function resolveConflict(op) {
    const fetched = await fetchServerNote(op.noteId);
    if (fetched.outcome) return fetched.outcome;
    if (fetched.gone) return goneToCopy(op);
    if (!fetched.note) return rejectOp(op, 409, "conflict");
    const server = fetched.note;
    const applied = NOTE_FIELDS.every((k) => !(k in (op.fields || {})) || sameField(k, op.fields[k], server[k]));
    if (applied) {
      await ackNote(op, server);
      return "done";
    }
    const result = await locked(async () => {
      const local = await store.getNote(op.noteId);
      const ops = await store.peekOps();
      const kept = ops.filter((o) => !(o.noteId === op.noteId && o.type === "update"));
      await store.replaceOps(kept);
      if (!local) return null;
      const copy = await buildNote({
        spaceId: local.spaceId,
        parentId: local.parentId,
        title: conflictTitle(local.title, now()),
        content: local.content,
        tags: local.tags,
        icon: local.icon,
      });
      await store.putNotes([copy]);
      await enqueueOp(createOp(copy));
      const rest = kept.filter((o) => o.noteId === op.noteId);
      const original = fromServer(server, local);
      if (rest.length) {
        original.parentId = local.parentId;
        original.position = local.position;
        original.deleted = local.deleted;
        original.path = local.path;
      }
      await store.setMeta(SERVER_COPY + op.noteId, undefined);
      await store.putNotes([original]);
      conflicts++;
      return copy.id;
    });
    if (result) {
      emit("conflict", { noteId: op.noteId, copyId: result, kind: "note" });
      emit("notes-changed", { ids: [op.noteId, result], source: "remote" });
      schedulePush(0);
    }
    return "done";
  }

  /**
   * Handles a 404: the note is gone or hidden. The local text becomes a
   * conflict copy at the space root. When that create is refused too, the
   * text moves to the recovered store.
   */
  async function goneToCopy(op) {
    const local = await locked(async () => {
      const note = await store.getNote(op.noteId);
      const ops = await store.peekOps();
      await store.replaceOps(ops.filter((o) => o.noteId !== op.noteId));
      await store.deleteNotes([op.noteId]);
      await store.setMeta(SERVER_COPY + op.noteId, undefined);
      return note;
    });
    if (!local) {
      emit("notes-changed", { ids: [op.noteId], source: "remote" });
      return "done";
    }
    const body = {
      id: newId(),
      spaceId: local.spaceId,
      parentId: "",
      title: conflictTitle(local.title, now()),
      content: local.content || "",
      tags: local.tags || [],
      icon: local.icon || "",
    };
    let outcome = "done";
    try {
      const created = await api("POST", "/api/notes", body);
      const copyId = (created && created.id) || body.id;
      await locked(() => store.putNotes([fromServer({ ...body, ...created }, null)]));
      conflicts++;
      emit("conflict", { noteId: op.noteId, copyId, kind: "note" });
      emit("notes-changed", { ids: [op.noteId, copyId], source: "remote" });
    } catch (err) {
      const status = statusOf(err);
      if (status < 0 || status === 401 || retryable(status)) {
        const copyId = await locked(async () => {
          const copy = await buildNote({ ...body, parentId: "" });
          copy.id = body.id;
          await store.putNotes([copy]);
          await enqueueOp(createOp(copy));
          return copy.id;
        });
        conflicts++;
        if (status === 401) {
          setAuthRequired();
          outcome = "stop";
        }
        emit("conflict", { noteId: op.noteId, copyId, kind: "note" });
        emit("notes-changed", { ids: [op.noteId, copyId], source: "local" });
      } else {
        const recovered = await locked(() => recover(local, `note unavailable, copy refused with status ${status}`));
        emit("recovered", recovered);
        emit("notes-changed", { ids: [op.noteId], source: "remote" });
      }
    }
    return outcome;
  }

  /**
   * Handles a 409 on a folder create: another item took the name. The
   * folder takes the next free name "<name> (N)" and the create is sent
   * again at once.
   */
  async function renameCreatedFolder(op) {
    const ids = await locked(async () => {
      const ops = await store.peekOps();
      const current = ops.find((o) => o.opId === op.opId);
      const folder = await store.getFolder(op.itemId);
      if (!current || !folder) {
        await store.removeOp(op.opId);
        return [];
      }
      const records = await spaceRecords(folder.spaceId);
      const taken = [...namesIn(records, dirOf(folder.path), folder.id), current.body.name];
      const next = freeName(taken, current.body.name, true);
      current.body.name = next;
      current.renamed = (current.renamed || 0) + 1;
      if (current.renamed > 20) {
        await store.replaceOps(ops.filter((o) => o !== current));
        return [];
      }
      await store.replaceOps(ops);
      const tagged = records.filter((t) => t.rec.path === folder.path || String(t.rec.path).startsWith(folder.path + "/"));
      const changed = repathRecords(tagged.map((t) => t.rec), folder.path, joinPath(dirOf(folder.path), next));
      await putTagged(tagged.filter((t) => changed.includes(t.rec)));
      return changed.map((r) => r.id);
    });
    if (ids.length) emit("notes-changed", { ids, source: "local" });
    return "done";
  }

  /**
   * Handles a refused folder create or copy: the item never reached the
   * server, so it and its provisional children are removed locally.
   */
  async function rejectItemCreate(op, status, message) {
    const ids = await locked(async () => {
      const ops = await store.peekOps();
      const folder = op.type === "folder.create" ? await store.getFolder(op.itemId) : null;
      const records = folder ? await spaceRecords(folder.spaceId) : [];
      const below = folder ? records.filter((t) => String(t.rec.path).startsWith(folder.path + "/")).map((t) => t.rec.id) : [];
      const all = new Set([op.itemId, ...below, ...(op.copies || [])]);
      for (const o of ops) if (all.has(opItem(o)) && o.key) await discardUpload(o.key);
      await store.replaceOps(ops.filter((o) => !all.has(opItem(o))));
      await store.deleteItems([...all]);
      return [...all];
    });
    emit("rejected", { op, status, message });
    emit("notes-changed", { ids, source: "remote" });
    emitUploads(true);
    return "done";
  }

  /**
   * Handles a refused rename, move, trash, restore or delete of a folder or
   * file: removes the op, puts back the server copy and emits rejected. An
   * item that is gone on the server is removed locally.
   */
  async function rejectItemOp(op, status, message) {
    const fetched = await fetchServerItem(op.itemId);
    if (fetched.outcome) return fetched.outcome;
    const ids = await locked(async () => {
      await store.removeOp(op.opId);
      const ops = await store.peekOps();
      const rest = ops.filter((o) => o.itemId === op.itemId);
      const found = await findItem(op.itemId);
      const local = found ? found.rec : null;
      if (fetched.gone) {
        if (rest.some((o) => CREATE_OPS.has(o.type))) return [];
        if (!local) return [];
        const records = await spaceRecords(local.spaceId);
        const below = records.filter((t) => String(t.rec.path).startsWith(local.path + "/")).map((t) => t.rec.id);
        await store.replaceOps(ops.filter((o) => opItem(o) !== op.itemId));
        await store.deleteItems([op.itemId, ...below]);
        return [op.itemId, ...below];
      }
      if (!fetched.rec) return [];
      const kind = fetched.kind || op.kind;
      if (rest.length) {
        await store.setMeta(SERVER_COPY + op.itemId, { ...fetched.rec, kind });
        return [];
      }
      const space = await store.getSpace(fetched.rec.spaceId);
      const restored = fromServerItem(kind, fetched.rec, local, space, await currentUser());
      if (status === 403 && !accessOf(fetched.rec.access)) restored.access = "view";
      const written = [{ kind, rec: restored }];
      if (local && local.path && restored.path && local.path !== restored.path) {
        const records = (await spaceRecords(local.spaceId)).filter((t) => t.rec.id !== restored.id && String(t.rec.path).startsWith(local.path + "/"));
        repathRecords(records.map((t) => t.rec), local.path, restored.path);
        written.push(...records);
      }
      if (local && Boolean(local.deleted) !== Boolean(restored.deleted)) {
        const records = (await spaceRecords(local.spaceId)).filter((t) => String(t.rec.path).startsWith(restored.path + "/") && !written.some((w) => w.rec.id === t.rec.id));
        for (const t of records) {
          t.rec.deleted = Boolean(restored.deleted);
          written.push(t);
        }
      }
      await putTagged(written);
      return written.map((t) => t.rec.id);
    });
    emit("rejected", { op, status, message });
    if (ids.length) emit("notes-changed", { ids, source: "remote" });
    return "done";
  }

  /**
   * Handles a 409 on a replacement: the file changed on the server since the
   * replacement was made. The local content becomes a new file named
   * "<stem> (conflicted copy YYYY-MM-DD HH:MM).<ext>" in the same folder, and
   * the original takes the server version.
   */
  async function replaceConflict(op) {
    const fetched = await fetchServerItem(op.itemId);
    if (fetched.outcome) return fetched.outcome;
    const result = await locked(async () => {
      await store.removeOp(op.opId);
      const blob = await store.getBlob(op.key);
      const local = await store.getFile(op.itemId);
      const item = uploadItems.get(op.key);
      if (item) uploadItems.delete(op.key);
      if (!blob || !local) {
        await store.deleteBlob(op.key);
        await persistUploads();
        return null;
      }
      const space = await store.getSpace(local.spaceId);
      const records = await spaceRecords(local.spaceId);
      const dir = dirOf(local.path);
      const name = freeName(namesIn(records, dir), conflictFileName(op.name || local.name, now()));
      const parentId = local.parentId && (await store.getFolder(local.parentId)) ? local.parentId : "";
      const copy = await queueUpload({ space, parentId, dir, blob, name, records });
      await store.deleteBlob(op.key);
      if (fetched.rec) {
        const original = fromServerItem("file", fetched.rec, local, space, await currentUser());
        await store.putFiles([original]);
      } else if (op.prev) {
        Object.assign(local, op.prev);
        delete local.pendingKey;
        await store.putFiles([local]);
      }
      conflicts++;
      return copy.id;
    });
    if (result) {
      emit("conflict", { noteId: op.itemId, itemId: op.itemId, copyId: result, kind: "file" });
      emit("notes-changed", { ids: [op.itemId, result], source: "remote" });
      emitUploads(true);
      schedulePush(0);
    }
    return "done";
  }

  async function markUploadQueued(key, message) {
    const item = uploadItems.get(key);
    if (!item) return;
    item.state = "queued";
    item.loaded = 0;
    item.error = message;
    await locked(() => persistUploads());
    emitUploads(true);
  }

  /**
   * Handles a refused upload or replacement. The blob stays for a retry.
   */
  async function failUpload(op, status, message) {
    await locked(async () => {
      await store.removeOp(op.opId);
      const item = uploadItems.get(op.key);
      if (item) {
        item.state = "failed";
        item.loaded = 0;
        item.error = message;
        const { opId, ...template } = op;
        item.op = template;
        await persistUploads();
      }
      if (op.type === "file.upload") {
        const rec = await store.getFile(op.itemId);
        if (rec) {
          rec.uploadFailed = true;
          await store.putFiles([rec]);
        }
      }
    });
    emit("rejected", { op, status, message });
    emitUploads(true);
    return "done";
  }

  // -------------------------------------------------------------------------
  // Pull

  /**
   * Applies one sync page under the lock.
   * @param {object} batch
   * @returns {Promise<{ids: string[], spacesChanged: boolean}>}
   */
  async function applyBatch(batch) {
    return locked(async () => {
      const ids = new Set();
      let spacesChanged = false;
      if (batch.user) await store.setMeta(META_USER, batch.user);
      const user = batch.user || (await currentUser());
      if (Array.isArray(batch.spaces)) {
        const key = (list) => JSON.stringify([...list].sort((a, b) => (a.id < b.id ? -1 : 1)));
        spacesChanged = key(await store.listSpaces()) !== key(batch.spaces);
        if (spacesChanged) await store.putSpaces(batch.spaces);
      }
      const spaces = new Map((await store.listSpaces()).map((s) => [s.id, s]));
      const ops = await store.peekOps();
      const queued = new Set(ops.map(opItem).filter(Boolean));
      const locals = new Map();
      for (const n of await store.listAllNotes({ includeDeleted: true })) locals.set(n.id, { kind: "note", rec: n });
      for (const f of await store.listAllFolders({ includeDeleted: true })) locals.set(f.id, { kind: "folder", rec: f });
      for (const f of await store.listAllFiles({ includeDeleted: true })) locals.set(f.id, { kind: "file", rec: f });
      const toPut = new Map();
      const inBatch = new Set();
      const moves = [];
      const apply = (kind, list) => {
        for (const sr of list || []) {
          if (!sr || !sr.id) continue;
          inBatch.add(sr.id);
          if (queued.has(sr.id)) {
            toPut.set(`aside:${sr.id}`, { aside: true, kind, rec: kind === "note" ? sr : { ...sr, kind } });
            continue;
          }
          const local = locals.get(sr.id) || null;
          if (local && (local.rec.rev || 0) > (sr.rev || 0)) continue;
          const stored = kind === "note" ? fromServer(sr, local && local.rec) : fromServerItem(kind, sr, local && local.rec, spaces.get(sr.spaceId) || null, user);
          if (kind === "folder" && local && local.rec.path && stored.path && local.rec.path !== stored.path) {
            moves.push({ spaceId: stored.spaceId, from: local.rec.path, to: stored.path, id: stored.id });
          }
          const tagged = { kind, rec: stored };
          locals.set(sr.id, tagged);
          toPut.set(sr.id, tagged);
          ids.add(sr.id);
        }
      };
      apply("folder", batch.folders);
      apply("note", batch.notes);
      apply("file", batch.files);
      // A moved or renamed folder moves the paths of the items below it that
      // this page did not carry.
      for (const m of moves) {
        for (const t of locals.values()) {
          if (t.rec.spaceId !== m.spaceId || t.rec.id === m.id || inBatch.has(t.rec.id) || queued.has(t.rec.id)) continue;
          const p = String(t.rec.path || "");
          if (p.startsWith(m.from + "/")) {
            t.rec.path = m.to + p.slice(m.from.length);
            toPut.set(t.rec.id, t);
            ids.add(t.rec.id);
          }
        }
      }
      for (const entry of toPut.values()) {
        if (entry.aside) await store.setMeta(SERVER_COPY + entry.rec.id, entry.rec);
      }
      if (Array.isArray(batch.liveIds)) {
        const live = new Set(batch.liveIds);
        const starred = new Set(batch.starredIds || []);
        const starQueued = new Set(ops.filter((o) => o.type === "star").map((o) => o.noteId));
        const removed = [];
        for (const t of locals.values()) {
          const n = t.rec;
          if (!live.has(n.id)) {
            if (!queued.has(n.id) && !n.provisional && !((n.rev || 0) > (batch.rev || 0))) removed.push(n.id);
            continue;
          }
          if (t.kind === "note" && !starQueued.has(n.id) && Boolean(n.starred) !== starred.has(n.id)) {
            n.starred = starred.has(n.id);
            toPut.set(n.id, t);
            ids.add(n.id);
          }
        }
        if (removed.length) {
          await store.deleteItems(removed);
          for (const id of removed) {
            toPut.delete(id);
            await store.setMeta(SERVER_COPY + id, undefined);
            ids.add(id);
          }
        }
      }
      await putTagged([...toPut.values()].filter((e) => !e.aside));
      if (typeof batch.rev === "number") await store.setMeta(META_REV, batch.rev);
      return { ids: [...ids], spacesChanged };
    });
  }

  function pull() {
    if (pulling) return pulling;
    if (!online()) return Promise.resolve();
    pulling = (async () => {
      emitStatus();
      try {
        for (let pages = 0; pages < 100000; pages++) {
          const since = (await store.getMeta(META_REV)) || 0;
          const batch = await api("GET", `/api/sync?since=${since}&limit=${PULL_LIMIT}`);
          if (!batch || typeof batch !== "object" || Array.isArray(batch)) throw new Error("the sync response is not a batch");
          if (typeof batch.accessKey === "string") {
            const storedKey = await store.getMeta(META_ACCESS_KEY);
            if (batch.accessKey !== storedKey) {
              await store.setMeta(META_ACCESS_KEY, batch.accessKey);
              if (typeof storedKey === "string" && since !== 0) {
                await store.setMeta(META_REV, 0);
                continue;
              }
            }
          }
          const { ids, spacesChanged } = await applyBatch(batch);
          if (spacesChanged) emit("spaces-changed", {});
          if (ids.length) emit("notes-changed", { ids, source: "remote" });
          if (!batch.more) break;
        }
        lastSyncAt = now();
        lastError = null;
        authRequired = false;
      } catch (err) {
        if (statusOf(err) === 401) setAuthRequired();
        else lastError = (err && err.message) || String(err);
      } finally {
        pulling = null;
        emitStatus();
      }
    })();
    return pulling;
  }

  // -------------------------------------------------------------------------
  // Lifecycle

  async function syncNow() {
    await ready;
    authRequired = false;
    resetBackoff();
    await push();
    pullAgain = false;
    await pull();
    // A second pass sends ops that pulls or earlier replies queued. It is
    // skipped after a failure so one call never retries twice.
    if (failures === 0) await push();
    if (pullAgain && failures === 0) {
      pullAgain = false;
      await pull();
    }
  }

  const onOnline = () => {
    resetBackoff();
    emitStatus();
    syncNow();
  };
  const onOffline = () => emitStatus();
  const onVisibility = () => {
    if (visible()) pull().then(() => push());
  };

  function schedulePull() {
    if (pullTimer) timers.clearTimeout(pullTimer);
    pullTimer = timers.setTimeout(() => {
      pullTimer = null;
      if (!started) return;
      if (visible()) pull().then(() => push());
      schedulePull();
    }, PULL_INTERVAL_MS);
  }

  function start() {
    if (started) return ready;
    started = true;
    if (globalTarget && globalTarget.addEventListener) {
      globalTarget.addEventListener("online", onOnline);
      globalTarget.addEventListener("offline", onOffline);
    }
    if (documentTarget && documentTarget.addEventListener) {
      documentTarget.addEventListener("visibilitychange", onVisibility);
    }
    schedulePull();
    return syncNow();
  }

  function stop() {
    started = false;
    for (const t of [pushTimer, retryTimer, pullTimer, uploadEventTimer]) if (t) timers.clearTimeout(t);
    pushTimer = retryTimer = pullTimer = uploadEventTimer = null;
    if (globalTarget && globalTarget.removeEventListener) {
      globalTarget.removeEventListener("online", onOnline);
      globalTarget.removeEventListener("offline", onOffline);
    }
    if (documentTarget && documentTarget.removeEventListener) {
      documentTarget.removeEventListener("visibilitychange", onVisibility);
    }
  }

  function on(event, fn) {
    if (!listeners.has(event)) listeners.set(event, new Set());
    listeners.get(event).add(fn);
    return () => listeners.get(event).delete(fn);
  }

  return {
    /** Resolves once saved upload items and the pending count are loaded. */
    ready,
    start,
    stop,
    syncNow,
    status,
    on,
    createNote,
    updateNote,
    moveNote,
    trashNote: (id) => setDeleted(id, true, "trash"),
    restoreNote: (id) => setDeleted(id, false, "restore"),
    deleteNote,
    setStar,
    uploadAttachment,
    createFolder,
    uploadFiles,
    renameItem,
    moveItem,
    copyItem,
    trashItem: (id) => setItemDeleted(id, true),
    restoreItem: (id) => setItemDeleted(id, false),
    deleteItem,
    replaceFile,
    uploads: uploadsSnapshot,
    retryUpload,
    cancelUpload,
  };
}
