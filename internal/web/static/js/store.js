// Local database of spaces, folders, notes, files, the outbox, pending blobs
// and recovered text. openStore uses IndexedDB. createMemoryStore keeps
// everything in memory with the same behaviour, for tests. Both call the same
// pure functions for trees, search, link resolution and backlinks.

import { escapeHtml } from "./markdown.js";
import { FORM, KIND, Snapshot, parse } from "./links.js";

/** Current IndexedDB schema version. Raise it and extend upgradeSchema to migrate. */
export const SCHEMA_VERSION = 4;

/**
 * Returns the IndexedDB database name for a user.
 * @param {string} userId
 * @returns {string}
 */
export function databaseName(userId) {
  return `markspace-${userId}`;
}

// ---------------------------------------------------------------------------
// Shared pure logic

/**
 * Parses a timestamp to milliseconds, treating a missing value as 0.
 * @param {string|undefined} s
 * @returns {number}
 */
function timeOf(s) {
  const t = Date.parse(s || "");
  return Number.isNaN(t) ? 0 : t;
}

/**
 * Orders notes by position, then title without regard to case.
 * @param {object} a
 * @param {object} b
 * @returns {number}
 */
function byPosition(a, b) {
  return (a.position || 0) - (b.position || 0) ||
    String(a.title || "").localeCompare(String(b.title || ""), undefined, { sensitivity: "base" });
}

/**
 * Orders records newest first by updatedAt.
 * @param {object} a
 * @param {object} b
 * @returns {number}
 */
function byUpdatedDesc(a, b) {
  return timeOf(b.updatedAt) - timeOf(a.updatedAt);
}

/** Orders records by path in code point order. */
function byPath(a, b) {
  const x = String(a.path || "");
  const y = String(b.path || "");
  return x < y ? -1 : x > y ? 1 : 0;
}

const KIND_ORDER = { folder: 0, note: 1, file: 2 };

/**
 * Returns the record a tree node or item holds.
 * @param {{kind: string, folder?: object, note?: object, file?: object}} item
 * @returns {object|null}
 */
export function itemRecord(item) {
  if (!item) return null;
  return item.folder || item.note || item.file || null;
}

/**
 * Returns the sort name of an item, as the server's treeName does: the folder
 * name, the note title or the file name.
 * @param {{kind: string, folder?: object, note?: object, file?: object}} item
 * @returns {string}
 */
export function itemName(item) {
  if (item.folder) return String(item.folder.name || "");
  if (item.note) return String(item.note.title || "");
  if (item.file) return String(item.file.name || "");
  return "";
}

/**
 * Orders sibling tree items by position, then kind (folders, notes, files),
 * then lowercased name, then id, as the server's sortTreeItems does.
 * @param {object} a tree item
 * @param {object} b tree item
 * @returns {number}
 */
export function compareTreeItems(a, b) {
  const ra = itemRecord(a) || {};
  const rb = itemRecord(b) || {};
  const pa = ra.position || 0;
  const pb = rb.position || 0;
  if (pa !== pb) return pa - pb;
  const ka = KIND_ORDER[a.kind] ?? 3;
  const kb = KIND_ORDER[b.kind] ?? 3;
  if (ka !== kb) return ka - kb;
  const na = itemName(a).toLowerCase();
  const nb = itemName(b).toLowerCase();
  if (na !== nb) return na < nb ? -1 : 1;
  const ia = String(ra.id || "");
  const ib = String(rb.id || "");
  return ia < ib ? -1 : ia > ib ? 1 : 0;
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

/**
 * Reports whether a note is the folder note of folder: the folder names it as
 * its index note, or the note is index.md directly inside the folder.
 * @param {object} note
 * @param {object} folder
 * @returns {boolean}
 */
export function isFolderNote(note, folder) {
  if (!note || !folder) return false;
  if (folder.indexNoteId) return folder.indexNoteId === note.id;
  return baseOf(note.path) === "index.md" && dirOf(note.path) === folder.path;
}

/**
 * Builds the tree of one space from its live folders, notes and files. Each
 * node is {kind, folder|note|file, children}. A folder node also carries
 * page, its folder note, which is not listed as a child. An item whose parent
 * is missing or trashed sits at the root. Siblings sort by position, kind,
 * then name without regard to case.
 * Passing an array of notes alone builds a tree of notes, for older callers.
 * @param {{folders?: object[], notes?: object[], files?: object[]}|object[]} input
 * @returns {object[]}
 */
export function buildTree(input) {
  const { folders = [], notes = [], files = [] } = Array.isArray(input) ? { notes: input } : input || {};
  const liveFolders = folders.filter((f) => f && !f.deleted);
  const nodes = new Map();
  for (const f of liveFolders) nodes.set(f.id, { kind: KIND.FOLDER, folder: f, page: null, children: [] });
  const byPathMap = new Map(liveFolders.map((f) => [f.path, nodes.get(f.id)]));
  const parentNode = (rec, selfId) => {
    const pid = rec.parentId || "";
    if (pid && pid !== selfId && nodes.has(pid)) return nodes.get(pid);
    return null;
  };
  const roots = [];
  // Folders first, with a cycle guard: a folder whose ancestor chain returns
  // to itself sits at the root.
  for (const f of liveFolders) {
    let parent = parentNode(f, f.id);
    const seen = new Set([f.id]);
    for (let p = parent; p; p = parentNode(p.folder, p.folder.id)) {
      if (seen.has(p.folder.id)) {
        parent = null;
        break;
      }
      seen.add(p.folder.id);
    }
    (parent ? parent.children : roots).push(nodes.get(f.id));
  }
  for (const n of notes) {
    if (!n || n.deleted) continue;
    let parent = parentNode(n, n.id);
    if (!parent && !n.parentId && baseOf(n.path) === "index.md") parent = byPathMap.get(dirOf(n.path)) || null;
    if (parent && !parent.page && isFolderNote(n, parent.folder)) {
      parent.page = n;
      continue;
    }
    (parent ? parent.children : roots).push({ kind: KIND.NOTE, note: n, children: [] });
  }
  for (const f of files) {
    if (!f || f.deleted) continue;
    const parent = parentNode(f, f.id);
    (parent ? parent.children : roots).push({ kind: KIND.FILE, file: f, children: [] });
  }
  const sortLevel = (level, depth) => {
    level.sort(compareTreeItems);
    if (depth > 512) return;
    for (const item of level) sortLevel(item.children, depth + 1);
  };
  sortLevel(roots, 0);
  return roots;
}

/**
 * Splits a search query into lowercase tokens of letters, digits and underscores.
 * @param {string} query
 * @returns {string[]}
 */
export function tokenize(query) {
  const tokens = String(query || "").toLowerCase().match(/[\p{L}\p{N}_]+/gu) || [];
  return [...new Set(tokens)];
}

/**
 * Escapes a string for use inside a regular expression.
 * @param {string} s
 * @returns {string}
 */
function escapeRegExp(s) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/**
 * Builds an HTML snippet of text around the first match. Every piece of note
 * text is escaped and each match is wrapped in <mark>.
 * @param {string} text
 * @param {string[]} tokens
 * @param {number} [radius=80] characters of context on each side
 * @returns {string}
 */
export function makeSnippet(text, tokens, radius = 80) {
  const flat = String(text || "").replace(/\s+/g, " ").trim();
  if (!flat) return "";
  const re = tokens.length ? new RegExp(tokens.map(escapeRegExp).join("|"), "giu") : null;
  let start = 0;
  let end = Math.min(flat.length, radius * 2);
  const first = re ? re.exec(flat) : null;
  if (first) {
    start = Math.max(0, first.index - radius);
    end = Math.min(flat.length, first.index + first[0].length + radius);
    if (start > 0) {
      const space = flat.indexOf(" ", start);
      if (space >= 0 && space < first.index) start = space + 1;
    }
  }
  if (end < flat.length) {
    const space = flat.lastIndexOf(" ", end);
    if (space > start) end = space;
  }
  const window = flat.slice(start, end);
  let html = "";
  let last = 0;
  if (re) {
    re.lastIndex = 0;
    for (let m = re.exec(window); m; m = re.exec(window)) {
      if (m[0].length === 0) {
        re.lastIndex++;
        continue;
      }
      html += escapeHtml(window.slice(last, m.index)) + "<mark>" + escapeHtml(m[0]) + "</mark>";
      last = m.index + m[0].length;
    }
  }
  html += escapeHtml(window.slice(last));
  return (start > 0 ? "…" : "") + html + (end < flat.length ? "…" : "");
}

/**
 * Counts non-overlapping occurrences of a token in lowercased text.
 * @param {string} haystack
 * @param {string} token
 * @param {number} cap
 * @returns {number}
 */
function countOccurrences(haystack, token, cap) {
  let n = 0;
  for (let i = haystack.indexOf(token); i >= 0 && n < cap; i = haystack.indexOf(token, i + token.length)) n++;
  return n;
}

/**
 * Searches notes. Every token must match the title, a tag or the content.
 * Title matches score above tag matches, which score above body matches.
 * @param {object[]} notes candidate notes
 * @param {string} query
 * @param {{spaceId?: string, limit?: number}} [options]
 * @returns {{note: object, snippet: string, score: number}[]}
 */
export function searchIn(notes, query, { spaceId, limit = 50 } = {}) {
  const tokens = tokenize(query);
  if (!tokens.length) return [];
  const hits = [];
  for (const note of notes) {
    if (note.deleted || (spaceId && note.spaceId !== spaceId)) continue;
    const title = String(note.title || "").toLowerCase();
    const tags = (note.tags || []).map((t) => String(t).toLowerCase());
    const content = String(note.content || "").toLowerCase();
    let score = 0;
    let all = true;
    for (const token of tokens) {
      let s = 0;
      if (title.includes(token)) {
        s += 10;
        if (title === token || title.split(/[^\p{L}\p{N}_]+/u).includes(token)) s += 5;
      }
      if (tags.some((t) => t.includes(token))) s += 6;
      s += countOccurrences(content, token, 5);
      if (s === 0) {
        all = false;
        break;
      }
      score += s;
    }
    if (!all) continue;
    const snippet = makeSnippet(note.content, tokens) || makeSnippet(note.title, tokens);
    hits.push({ note, snippet, score });
  }
  hits.sort((a, b) => b.score - a.score || byUpdatedDesc(a.note, b.note));
  return hits.slice(0, limit > 0 ? limit : 50);
}

/**
 * Searches file names. Every token must appear in the name.
 * @param {object[]} files
 * @param {string} query
 * @param {{spaceId?: string, limit?: number}} [options]
 * @returns {{file: object, score: number}[]}
 */
export function searchFilesIn(files, query, { spaceId, limit = 50 } = {}) {
  const tokens = tokenize(query);
  if (!tokens.length) return [];
  const hits = [];
  for (const file of files) {
    if (file.deleted || (spaceId && file.spaceId !== spaceId)) continue;
    const name = String(file.name || "").toLowerCase();
    if (!tokens.every((t) => name.includes(t))) continue;
    hits.push({ file, score: tokens.length * 10 + (name.startsWith(tokens[0]) ? 5 : 0) });
  }
  hits.sort((a, b) => b.score - a.score || byUpdatedDesc(a.file, b.file));
  return hits.slice(0, limit > 0 ? limit : 50);
}

/**
 * Normalizes a link target the way the old server index did: the fragment is
 * cut, a leading "./" or "/" and a trailing "/" are removed, a ".md" or
 * ".markdown" extension is removed, and the result is lowercased.
 * @param {string} target
 * @returns {string}
 */
export function normalizeTarget(target) {
  let s = String(target || "").trim();
  const hash = s.indexOf("#");
  if (hash >= 0) s = s.slice(0, hash);
  s = s.trim();
  if (s.startsWith("./")) s = s.slice(2);
  if (s.startsWith("/")) s = s.slice(1);
  if (s.endsWith("/")) s = s.slice(0, -1);
  for (const ext of [".md", ".markdown"]) {
    if (s.length > ext.length && s.slice(-ext.length).toLowerCase() === ext) {
      s = s.slice(0, -ext.length);
      break;
    }
  }
  return s.trim().toLowerCase();
}

/**
 * Returns the links.js snapshot entry of a record, or null for a trashed one.
 * @param {string} kind
 * @param {object} rec
 * @returns {{id: string, kind: string, path: string, title: string}|null}
 */
export function entryOf(kind, rec) {
  if (!rec || rec.deleted || !rec.path) return null;
  return { id: rec.id, kind, path: rec.path, title: kind === KIND.NOTE ? rec.title || "" : "" };
}

/**
 * Builds the snapshot entries of one space's live items.
 * @param {{folders?: object[], notes?: object[], files?: object[]}} items
 * @returns {object[]}
 */
export function snapshotEntries({ folders = [], notes = [], files = [] }) {
  const out = [];
  for (const [kind, list] of [[KIND.FOLDER, folders], [KIND.NOTE, notes], [KIND.FILE, files]]) {
    for (const rec of list) {
      const e = entryOf(kind, rec);
      if (e) out.push(e);
    }
  }
  out.sort(byPath);
  return out;
}

/**
 * Splits a written link target into the path to resolve and its heading.
 * A wikilink target loses its "|label" and "#heading". A markdown target
 * loses "?query" and "#fragment" and is percent-decoded.
 * @param {string} target
 * @param {string} form a FORM value
 * @returns {{path: string, heading: string}}
 */
export function splitTarget(target, form, { decoded = false } = {}) {
  let s = String(target || "");
  let heading = "";
  if (form === FORM.MARKDOWN_LINK || form === FORM.MARKDOWN_IMAGE) {
    const hash = s.indexOf("#");
    if (hash >= 0) {
      heading = s.slice(hash + 1);
      s = s.slice(0, hash);
    }
    const q = s.indexOf("?");
    if (q >= 0) s = s.slice(0, q);
    if (!decoded) {
      try {
        s = decodeURIComponent(s);
      } catch {
        // Keep the raw text when its escapes are malformed.
      }
    }
    return { path: s, heading };
  }
  const bar = s.indexOf("|");
  if (bar >= 0) s = s.slice(0, bar);
  const hash = s.indexOf("#");
  if (hash >= 0) {
    heading = s.slice(hash + 1).trim();
    s = s.slice(0, hash);
  }
  return { path: s.trim(), heading };
}

/**
 * Resolves a written target from a note path with a snapshot.
 * @param {Snapshot} snapshot
 * @param {string} fromPath entry path of the linking note, "" for the space root
 * @param {string} target
 * @param {string} [form] a FORM value; wikilink by default
 * @param {{decoded?: boolean}} [options] decoded skips percent-decoding a markdown target
 * @returns {{id: string, kind: string, path: string, heading: string}|null}
 */
export function resolveWith(snapshot, fromPath, target, form = FORM.WIKILINK, { decoded = false } = {}) {
  const { path, heading } = splitTarget(target, form, { decoded });
  if (!path) return null;
  const wiki = form !== FORM.MARKDOWN_LINK && form !== FORM.MARKDOWN_IMAGE;
  const e = snapshot.resolveTarget(fromPath || "", path, wiki ? (path.includes("/") ? FORM.PATH_WIKILINK : FORM.WIKILINK) : form);
  return e ? { id: e.id, kind: e.kind, path: e.path, heading } : null;
}

/**
 * Resolves a link target among the live notes of one space from the space
 * root with the links.js rules, for callers that know only a target.
 * @param {object[]} notes
 * @param {string} spaceId
 * @param {string} target
 * @returns {object|null} the note
 */
export function resolveLinkIn(notes, spaceId, target) {
  const inSpace = notes.filter((n) => n.spaceId === spaceId && !n.deleted);
  const snap = new Snapshot(snapshotEntries({ notes: inSpace }));
  const hit = resolveWith(snap, "", target);
  return hit ? inSpace.find((n) => n.id === hit.id) || null : null;
}

/**
 * Returns the line of body around offset, trimmed to about 160 characters.
 * @param {string} body
 * @param {number} offset
 * @returns {string}
 */
function lineAt(body, offset) {
  const text = String(body || "");
  const idx = Math.max(0, Math.min(offset, text.length));
  const start = text.lastIndexOf("\n", idx - 1) + 1;
  let end = text.indexOf("\n", idx);
  if (end < 0) end = text.length;
  let line = text.slice(start, end).trim();
  if (line.length > 160) line = line.slice(0, 157) + "…";
  return line;
}

/**
 * Finds the live notes in a space whose links resolve to itemId.
 * @param {object[]} notes every note of the space
 * @param {Snapshot} snapshot the space snapshot
 * @param {string} itemId a folder, note or file id
 * @returns {{note: object, context: string}[]}
 */
export function backlinksIn(notes, snapshot, itemId) {
  if (!snapshot.byId(itemId)) return [];
  const out = [];
  for (const n of notes) {
    if (n.deleted || n.id === itemId || !n.content) continue;
    let links;
    try {
      links = parse(n.content);
    } catch {
      continue;
    }
    for (const l of links) {
      if (l.external) continue;
      const e = snapshot.resolve(n.path || "", l);
      if (e && e.id === itemId) {
        out.push({ note: n, context: lineAt(n.content, l.start) });
        break;
      }
    }
  }
  out.sort((a, b) => byUpdatedDesc(a.note, b.note));
  return out;
}

/**
 * Notes that reach the user through a share rather than a space membership:
 * the space is unknown locally or the user holds no role in it.
 * @param {object[]} notes
 * @param {object[]} spaces
 * @param {string} userId
 * @returns {object[]}
 */
export function sharedIn(notes, spaces, userId) {
  const roles = new Map(spaces.map((s) => [s.id, s.myRole || ""]));
  return notes
    .filter((n) => !n.deleted && !roles.get(n.spaceId) && n.createdBy !== userId)
    .sort(byUpdatedDesc);
}

/**
 * Finds a space by id, then by slug.
 * @param {object[]} spaces
 * @param {string} idOrSlug
 * @returns {object|null}
 */
function findSpace(spaces, idOrSlug) {
  return spaces.find((s) => s.id === idOrSlug) || spaces.find((s) => s.slug === idOrSlug) || null;
}

/**
 * Returns the candidate spellings of a path from a URL: as written and
 * percent-decoded, without leading and trailing slashes.
 * @param {string} path
 * @returns {string[]}
 */
function pathCandidates(path) {
  const raw = String(path || "");
  let decoded = raw;
  try {
    decoded = decodeURIComponent(raw);
  } catch {
    // Keep the raw path when its escapes are malformed.
  }
  return [...new Set([raw, decoded])].map((p) => p.replace(/^\/+/, "").replace(/\/+$/, ""));
}

/**
 * Finds a note by space slug and path, accepting the path with or without ".md".
 * @param {object[]} notes
 * @param {object[]} spaces
 * @param {string} spaceSlug
 * @param {string} path
 * @returns {object|null}
 */
function findByPath(notes, spaces, spaceSlug, path) {
  const space = findSpace(spaces, spaceSlug);
  const inSpace = (n) => (space ? n.spaceId === space.id : n.spaceSlug === spaceSlug);
  for (const candidate of pathCandidates(path)) {
    const bare = candidate.replace(/\.md$/, "");
    const hit = notes.find((n) => inSpace(n) && n.path === bare + ".md") || notes.find((n) => inSpace(n) && n.path === bare);
    if (hit) return hit;
  }
  return null;
}

/**
 * Keeps the link snapshot of each space. Records update the entries of their
 * space in place, and the Snapshot rebuilds from those entries only when a
 * query needs it after a change.
 */
export class LinkIndex {
  /** @param {() => Promise<{folders: object[], notes: object[], files: object[]}>} load */
  constructor(load) {
    this.load = load;
    this.reset();
  }

  /** Forgets everything, so the next query loads from the store. */
  reset() {
    this.spaces = null;
    this.loading = null;
    this.kindOf = new Map();
  }

  async ensure() {
    // A write during a load discards that load, so this loops until a load
    // finishes with no write in between.
    for (let attempt = 0; !this.spaces && attempt < 20; attempt++) await this.loadOnce();
    if (!this.spaces) await this.loadOnce(true);
  }

  async loadOnce(force = false) {
    if (!this.loading) {
      this.loading = (async () => {
        const { folders, notes, files } = await this.load();
        const spaces = new Map();
        const kindOf = new Map();
        for (const [kind, list] of [[KIND.FOLDER, folders], [KIND.NOTE, notes], [KIND.FILE, files]]) {
          for (const rec of list) {
            kindOf.set(rec.id, rec.spaceId);
            const e = entryOf(kind, rec);
            if (!e) continue;
            if (!spaces.has(rec.spaceId)) spaces.set(rec.spaceId, { entries: new Map(), snapshot: null });
            spaces.get(rec.spaceId).entries.set(rec.id, e);
          }
        }
        return { spaces, kindOf };
      })();
    }
    const loading = this.loading;
    const { spaces, kindOf } = await loading;
    if (this.loading === loading || force) {
      this.spaces = spaces;
      this.kindOf = kindOf;
      this.loading = null;
    }
  }

  /**
   * Applies written records of one kind. Does nothing before the first load.
   * @param {string} kind
   * @param {object[]} records
   */
  update(kind, records) {
    if (!this.spaces) {
      this.loading = null;
      return;
    }
    for (const rec of records || []) {
      const oldSpace = this.kindOf.get(rec.id);
      if (oldSpace !== undefined && oldSpace !== rec.spaceId) this.drop(rec.id);
      this.kindOf.set(rec.id, rec.spaceId);
      if (!this.spaces.has(rec.spaceId)) this.spaces.set(rec.spaceId, { entries: new Map(), snapshot: null });
      const space = this.spaces.get(rec.spaceId);
      const e = entryOf(kind, rec);
      const old = space.entries.get(rec.id);
      if (e) {
        if (!old || old.path !== e.path || old.title !== e.title || old.kind !== e.kind) {
          space.entries.set(rec.id, e);
          space.snapshot = null;
        }
      } else if (old) {
        space.entries.delete(rec.id);
        space.snapshot = null;
      }
    }
  }

  /** Removes records by id. */
  remove(ids) {
    if (!this.spaces) {
      this.loading = null;
      return;
    }
    for (const id of ids || []) this.drop(id);
  }

  drop(id) {
    const spaceId = this.kindOf.get(id);
    this.kindOf.delete(id);
    const space = this.spaces.get(spaceId);
    if (space && space.entries.delete(id)) space.snapshot = null;
  }

  /**
   * Returns the Snapshot of a space.
   * @param {string} spaceId
   * @returns {Promise<Snapshot>}
   */
  async snapshot(spaceId) {
    await this.ensure();
    const space = this.spaces.get(spaceId);
    if (!space) return new Snapshot([]);
    if (!space.snapshot) space.snapshot = new Snapshot([...space.entries.values()].sort(byPath));
    return space.snapshot;
  }
}

/**
 * Wraps the queries every implementation answers from full lists of records.
 * @param {object} src
 * @param {() => Promise<object[]>} src.allNotes
 * @param {() => Promise<object[]>} src.allSpaces
 * @param {() => Promise<object[]>} src.allFolders
 * @param {() => Promise<object[]>} src.allFiles
 * @param {LinkIndex} src.index
 * @param {(id: string) => Promise<object|null>} src.getNote
 * @param {(id: string) => Promise<object|null>} src.getFolder
 * @param {(id: string) => Promise<object|null>} src.getFile
 * @returns {object}
 */
function queryMethods({ allNotes, allSpaces, allFolders, allFiles, index, getNote, getFolder, getFile }) {
  const inSpace = (list, spaceId, includeDeleted) => list.filter((r) => r.spaceId === spaceId && (includeDeleted || !r.deleted));
  return {
    async getSpace(idOrSlug) {
      return findSpace(await allSpaces(), idOrSlug);
    },
    async getNoteByPath(spaceSlug, path) {
      return findByPath(await allNotes(), await allSpaces(), spaceSlug, path);
    },
    async listNotes(spaceId, { includeDeleted = false } = {}) {
      return inSpace(await allNotes(), spaceId, includeDeleted).sort(byPosition);
    },
    async listAllNotes({ includeDeleted = false } = {}) {
      return (await allNotes()).filter((n) => includeDeleted || !n.deleted);
    },
    async listFolders(spaceId, { includeDeleted = false } = {}) {
      return inSpace(await allFolders(), spaceId, includeDeleted).sort(byPath);
    },
    async listAllFolders({ includeDeleted = false } = {}) {
      return (await allFolders()).filter((f) => includeDeleted || !f.deleted);
    },
    async listFiles(spaceId, { includeDeleted = false } = {}) {
      return inSpace(await allFiles(), spaceId, includeDeleted).sort(byPath);
    },
    async listAllFiles({ includeDeleted = false } = {}) {
      return (await allFiles()).filter((f) => includeDeleted || !f.deleted);
    },
    /**
     * Returns every folder, note and file of a space.
     * @param {string} spaceId
     * @param {{includeDeleted?: boolean}} [options]
     * @returns {Promise<{folders: object[], notes: object[], files: object[]}>}
     */
    async listItems(spaceId, { includeDeleted = false } = {}) {
      const [folders, notes, files] = await Promise.all([allFolders(), allNotes(), allFiles()]);
      return {
        folders: inSpace(folders, spaceId, includeDeleted),
        notes: inSpace(notes, spaceId, includeDeleted),
        files: inSpace(files, spaceId, includeDeleted),
      };
    },
    async recentNotes(limit = 20) {
      return (await allNotes()).filter((n) => !n.deleted).sort(byUpdatedDesc).slice(0, limit);
    },
    async starredNotes() {
      return (await allNotes()).filter((n) => n.starred && !n.deleted).sort(byPosition);
    },
    async sharedNotes(userId) {
      return sharedIn(await allNotes(), await allSpaces(), userId);
    },
    async tree(spaceId) {
      const items = await this.listItems(spaceId);
      return buildTree(items);
    },
    async searchNotes(query, options = {}) {
      return searchIn(await allNotes(), query, options);
    },
    async searchFiles(query, options = {}) {
      return searchFilesIn(await allFiles(), query, options);
    },
    /**
     * Returns the item with an id as {kind, folder|note|file}, or null.
     * @param {string} id
     */
    async getItem(id) {
      if (!id) return null;
      const note = await getNote(id);
      if (note) return { kind: KIND.NOTE, note };
      const file = await getFile(id);
      if (file) return { kind: KIND.FILE, file };
      const folder = await getFolder(id);
      if (folder) return { kind: KIND.FOLDER, folder };
      return null;
    },
    /**
     * Finds an item by space id or slug and entry path. A note path may omit
     * ".md". The path may be percent-encoded.
     * @param {string} spaceIdOrSlug
     * @param {string} path
     */
    async getItemByPath(spaceIdOrSlug, path) {
      const space = findSpace(await allSpaces(), spaceIdOrSlug);
      const match = (r) => (space ? r.spaceId === space.id : r.spaceSlug === spaceIdOrSlug);
      const [folders, notes, files] = await Promise.all([allFolders(), allNotes(), allFiles()]);
      for (const p of pathCandidates(path)) {
        if (!p) continue;
        const pick = (list) => list.filter((r) => match(r) && r.path === p).sort((a, b) => Number(a.deleted) - Number(b.deleted))[0];
        const file = pick(files);
        if (file) return { kind: KIND.FILE, file };
        const folder = pick(folders);
        if (folder) return { kind: KIND.FOLDER, folder };
        const note = pick(notes) || notes.filter((n) => match(n) && n.path === p + ".md").sort((a, b) => Number(a.deleted) - Number(b.deleted))[0];
        if (note) return { kind: KIND.NOTE, note };
      }
      return null;
    },
    /**
     * Returns the links.js snapshot of a space's live items.
     * @param {string} spaceId
     * @returns {Promise<Snapshot>}
     */
    async snapshot(spaceId) {
      return index.snapshot(spaceId);
    },
    /**
     * Resolves a link written in a note. With three arguments it returns
     * {kind, id, path, heading, item} or null, where item is
     * {kind, folder|note|file}. fromNoteId may be "" for the space root.
     * options.form is a links.js FORM value and defaults to a wikilink.
     * With two arguments, (spaceId, target), it resolves from the space root
     * and returns the note or null, for older callers.
     */
    async resolveLink(spaceId, fromNoteId, target, { form = FORM.WIKILINK, decoded = false } = {}) {
      if (arguments.length === 2) {
        const snap = await index.snapshot(spaceId);
        const hit = resolveWith(snap, "", fromNoteId, FORM.WIKILINK);
        if (!hit || hit.kind !== KIND.NOTE) return null;
        return getNote(hit.id);
      }
      const snap = await index.snapshot(spaceId);
      let fromPath = "";
      if (fromNoteId) {
        const from = await getNote(fromNoteId);
        fromPath = from && from.path ? from.path : "";
      }
      const hit = resolveWith(snap, fromPath, target, form, { decoded });
      if (!hit) return null;
      const item = await this.getItem(hit.id);
      if (!item) return null;
      return { ...hit, item };
    },
    /**
     * Lists the live notes whose links resolve to an item of any kind.
     * @param {string} itemId
     * @returns {Promise<{note: object, context: string}[]>}
     */
    async backlinks(itemId) {
      const item = await this.getItem(itemId);
      const rec = itemRecord(item);
      if (!rec) return [];
      const snap = await index.snapshot(rec.spaceId);
      const notes = (await allNotes()).filter((n) => n.spaceId === rec.spaceId);
      return backlinksIn(notes, snap, itemId);
    },
  };
}

/**
 * Copies a value the way IndexedDB's structured clone does, so callers never
 * share objects with the store.
 * @param {any} v
 * @returns {any}
 */
function clone(v) {
  if (v === undefined || v === null || typeof v !== "object") return v;
  if (typeof Blob !== "undefined" && v instanceof Blob) return v;
  return structuredClone(v);
}

// ---------------------------------------------------------------------------
// Memory store

/**
 * Creates a store that keeps everything in memory, with the same interface
 * and behaviour as openStore. Beyond the docs/offline.md interface both stores
 * cache comment threads per item with putComments(itemId, list) and
 * listComments(itemId), and getBlob resolves to the stored Blob or null.
 * @returns {object} Store
 */
export function createMemoryStore() {
  let meta = new Map();
  let spaces = new Map();
  let notes = new Map();
  let folders = new Map();
  let files = new Map();
  let outbox = new Map();
  let nextOpId = 1;
  let blobs = new Map();
  let recovered = new Map();
  let comments = new Map();
  const allNotes = async () => [...notes.values()].map(clone);
  const allSpaces = async () => [...spaces.values()].map(clone);
  const allFolders = async () => [...folders.values()].map(clone);
  const allFiles = async () => [...files.values()].map(clone);
  const index = new LinkIndex(async () => ({ folders: [...folders.values()], notes: [...notes.values()], files: [...files.values()] }));
  const getNote = async (id) => clone(notes.get(id)) || null;
  const getFolder = async (id) => clone(folders.get(id)) || null;
  const getFile = async (id) => clone(files.get(id)) || null;
  const dropIds = (map, kindIds) => {
    for (const id of kindIds || []) {
      map.delete(id);
      comments.delete(id);
    }
    index.remove(kindIds);
  };
  return {
    ...queryMethods({ allNotes, allSpaces, allFolders, allFiles, index, getNote, getFolder, getFile }),
    async getMeta(key) {
      return clone(meta.get(key));
    },
    async setMeta(key, value) {
      if (value === undefined) meta.delete(key);
      else meta.set(key, clone(value));
    },
    async putSpaces(list) {
      spaces = new Map((list || []).map((s) => [s.id, clone(s)]));
    },
    async listSpaces() {
      return allSpaces();
    },
    async putNotes(list) {
      for (const n of list || []) notes.set(n.id, clone(n));
      index.update(KIND.NOTE, list);
    },
    async deleteNotes(ids) {
      dropIds(notes, ids);
    },
    getNote,
    async putFolders(list) {
      for (const f of list || []) folders.set(f.id, clone(f));
      index.update(KIND.FOLDER, list);
    },
    async deleteFolders(ids) {
      dropIds(folders, ids);
    },
    getFolder,
    async putFiles(list) {
      for (const f of list || []) files.set(f.id, clone(f));
      index.update(KIND.FILE, list);
    },
    async deleteFiles(ids) {
      dropIds(files, ids);
    },
    getFile,
    /** Deletes items of any kind by id. */
    async deleteItems(ids) {
      dropIds(notes, (ids || []).filter((id) => notes.has(id)));
      dropIds(folders, (ids || []).filter((id) => folders.has(id)));
      dropIds(files, (ids || []).filter((id) => files.has(id)));
    },
    async enqueue(op) {
      const opId = op.opId || nextOpId;
      nextOpId = Math.max(nextOpId, opId) + 1;
      outbox.set(opId, clone({ ...op, opId }));
      return opId;
    },
    async peekOps() {
      return [...outbox.values()].sort((a, b) => a.opId - b.opId).map(clone);
    },
    async removeOp(opId) {
      outbox.delete(opId);
    },
    async replaceOps(ops) {
      outbox = new Map();
      for (const op of ops || []) {
        const opId = op.opId || nextOpId;
        nextOpId = Math.max(nextOpId, opId) + 1;
        outbox.set(opId, clone({ ...op, opId }));
      }
    },
    async countOps() {
      return outbox.size;
    },
    async putBlob(key, blob) {
      blobs.set(key, blob);
    },
    async getBlob(key) {
      return blobs.has(key) ? blobs.get(key) : null;
    },
    async deleteBlob(key) {
      blobs.delete(key);
    },
    async putRecovered(item) {
      const id = item.id || `r${Date.now()}${Math.random().toString(36).slice(2, 8)}`;
      recovered.set(id, clone({ ...item, id }));
      return id;
    },
    async listRecovered() {
      return [...recovered.values()].map(clone).sort((a, b) => timeOf(b.createdAt) - timeOf(a.createdAt));
    },
    async deleteRecovered(id) {
      recovered.delete(id);
    },
    /**
     * Replaces the cached comment threads of an item.
     * @param {string} itemId
     * @param {object[]} list comments as the comments route returns them
     */
    async putComments(itemId, list) {
      comments.set(itemId, clone({ noteId: itemId, list: list || [], cachedAt: new Date().toISOString() }));
    },
    /**
     * Returns the cached comment threads of an item, or [] when none are cached.
     * @param {string} itemId
     * @returns {Promise<object[]>}
     */
    async listComments(itemId) {
      const entry = comments.get(itemId);
      return entry ? clone(entry.list) : [];
    },
    async wipe() {
      comments = new Map();
      meta = new Map();
      spaces = new Map();
      notes = new Map();
      folders = new Map();
      files = new Map();
      outbox = new Map();
      blobs = new Map();
      recovered = new Map();
      index.reset();
    },
    close() {},
  };
}

// ---------------------------------------------------------------------------
// IndexedDB store

const STORES = ["meta", "spaces", "notes", "folders", "files", "outbox", "blobs", "recovered", "comments"];
const openConnections = new Map();

/**
 * Creates or migrates the object stores. Each step runs once for databases
 * older than its version.
 * @param {IDBDatabase} db
 * @param {number} oldVersion
 */
function upgradeSchema(db, oldVersion) {
  if (oldVersion < 1) {
    db.createObjectStore("meta");
    const spaces = db.createObjectStore("spaces", { keyPath: "id" });
    spaces.createIndex("slug", "slug");
    const notes = db.createObjectStore("notes", { keyPath: "id" });
    notes.createIndex("spaceId", "spaceId");
    notes.createIndex("updatedAt", "updatedAt");
    notes.createIndex("path", "path");
    db.createObjectStore("outbox", { keyPath: "opId", autoIncrement: true });
    db.createObjectStore("blobs");
    db.createObjectStore("recovered", { keyPath: "id" });
  }
  if (oldVersion < 3) {
    // Comment threads cached per item for offline reading.
    db.createObjectStore("comments", { keyPath: "noteId" });
  }
  if (oldVersion < 4) {
    // Folders and files replace the attachment model.
    if (db.objectStoreNames.contains("attachments")) db.deleteObjectStore("attachments");
    for (const name of ["folders", "files"]) {
      const s = db.createObjectStore(name, { keyPath: "id" });
      s.createIndex("spaceId", "spaceId");
      s.createIndex("parentId", "parentId");
      s.createIndex("path", "path");
    }
  }
}

/**
 * Wraps an IDBRequest in a promise.
 * @param {IDBRequest} req
 * @returns {Promise<any>}
 */
function promised(req) {
  return new Promise((resolve, reject) => {
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

/**
 * Runs fn inside one transaction and resolves when the transaction commits.
 * @param {IDBDatabase} db
 * @param {string|string[]} names object stores
 * @param {IDBTransactionMode} mode
 * @param {(tx: IDBTransaction) => any} fn
 * @returns {Promise<any>} the value fn returned, after its promise settles
 */
function transact(db, names, mode, fn) {
  return new Promise((resolve, reject) => {
    const tx = db.transaction(names, mode);
    let result;
    tx.oncomplete = () => resolve(result);
    tx.onerror = () => reject(tx.error);
    tx.onabort = () => reject(tx.error || new Error("transaction aborted"));
    Promise.resolve(fn(tx)).then((v) => {
      result = v;
    }, (err) => {
      try {
        tx.abort();
      } catch {
        // The transaction already finished.
      }
      reject(err);
    });
  });
}

/**
 * Opens the user's IndexedDB database, creating or upgrading it.
 * @param {string} userId
 * @returns {Promise<object>} Store
 */
export async function openStore(userId) {
  if (!userId) throw new Error("openStore: userId is required");
  const name = databaseName(userId);
  const db = await new Promise((resolve, reject) => {
    const req = indexedDB.open(name, SCHEMA_VERSION);
    req.onupgradeneeded = (e) => upgradeSchema(req.result, e.oldVersion);
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
    req.onblocked = () => reject(new Error("the database is open in an older tab"));
  });
  db.onversionchange = () => db.close();
  if (!openConnections.has(name)) openConnections.set(name, new Set());
  openConnections.get(name).add(db);

  // Records are cached in memory per object store and dropped when any tab
  // writes them.
  const cache = { notes: null, folders: null, files: null };
  const index = new LinkIndex(async () => ({ folders: await loadAll("folders"), notes: await loadAll("notes"), files: await loadAll("files") }));
  const channel = typeof BroadcastChannel !== "undefined" ? new BroadcastChannel(`${name}-notes`) : null;
  if (channel) channel.onmessage = (e) => {
    const which = e && typeof e.data === "string" && cache[e.data] !== undefined ? [e.data] : Object.keys(cache);
    for (const k of which) cache[k] = null;
    index.reset();
  };
  const invalidate = (storeName) => {
    cache[storeName] = null;
    if (channel) channel.postMessage(storeName);
  };
  async function loadAll(storeName) {
    if (!cache[storeName]) cache[storeName] = await transact(db, storeName, "readonly", (tx) => promised(tx.objectStore(storeName).getAll()));
    return cache[storeName];
  }
  const allOf = (storeName) => async () => (await loadAll(storeName)).map(clone);
  const allSpaces = () => transact(db, "spaces", "readonly", (tx) => promised(tx.objectStore("spaces").getAll()));
  const getOne = (storeName) => async (id) => {
    const v = await transact(db, storeName, "readonly", (tx) => promised(tx.objectStore(storeName).get(id)));
    return v || null;
  };
  const putList = async (storeName, kind, list) => {
    if (!list || !list.length) return;
    await transact(db, storeName, "readwrite", (tx) => {
      const store = tx.objectStore(storeName);
      for (const r of list) store.put(r);
    });
    invalidate(storeName);
    index.update(kind, list);
  };
  const deleteList = async (storeName, ids) => {
    if (!ids || !ids.length) return;
    await transact(db, [storeName, "comments"], "readwrite", (tx) => {
      const store = tx.objectStore(storeName);
      const commentStore = tx.objectStore("comments");
      for (const id of ids) {
        store.delete(id);
        commentStore.delete(id);
      }
    });
    invalidate(storeName);
    index.remove(ids);
  };
  const getNote = getOne("notes");
  const getFolder = getOne("folders");
  const getFile = getOne("files");

  return {
    ...queryMethods({ allNotes: allOf("notes"), allSpaces, allFolders: allOf("folders"), allFiles: allOf("files"), index, getNote, getFolder, getFile }),
    async getMeta(key) {
      return transact(db, "meta", "readonly", (tx) => promised(tx.objectStore("meta").get(key)));
    },
    async setMeta(key, value) {
      await transact(db, "meta", "readwrite", (tx) => {
        const store = tx.objectStore("meta");
        return promised(value === undefined ? store.delete(key) : store.put(value, key));
      });
    },
    async putSpaces(list) {
      await transact(db, "spaces", "readwrite", async (tx) => {
        const store = tx.objectStore("spaces");
        await promised(store.clear());
        for (const s of list || []) store.put(s);
      });
    },
    async listSpaces() {
      return allSpaces();
    },
    putNotes: (list) => putList("notes", KIND.NOTE, list),
    deleteNotes: (ids) => deleteList("notes", ids),
    getNote,
    putFolders: (list) => putList("folders", KIND.FOLDER, list),
    deleteFolders: (ids) => deleteList("folders", ids),
    getFolder,
    putFiles: (list) => putList("files", KIND.FILE, list),
    deleteFiles: (ids) => deleteList("files", ids),
    getFile,
    async deleteItems(ids) {
      for (const s of ["notes", "folders", "files"]) await deleteList(s, ids);
    },
    async enqueue(op) {
      const record = { ...op };
      if (!record.opId) delete record.opId;
      return transact(db, "outbox", "readwrite", (tx) => promised(tx.objectStore("outbox").put(record)));
    },
    async peekOps() {
      return transact(db, "outbox", "readonly", (tx) => promised(tx.objectStore("outbox").getAll()));
    },
    async removeOp(opId) {
      await transact(db, "outbox", "readwrite", (tx) => promised(tx.objectStore("outbox").delete(opId)));
    },
    async replaceOps(ops) {
      await transact(db, "outbox", "readwrite", async (tx) => {
        const store = tx.objectStore("outbox");
        await promised(store.clear());
        for (const op of ops || []) {
          const record = { ...op };
          if (!record.opId) delete record.opId;
          store.put(record);
        }
      });
    },
    async countOps() {
      return transact(db, "outbox", "readonly", (tx) => promised(tx.objectStore("outbox").count()));
    },
    async putBlob(key, blob) {
      await transact(db, "blobs", "readwrite", (tx) => promised(tx.objectStore("blobs").put(blob, key)));
    },
    async getBlob(key) {
      const v = await transact(db, "blobs", "readonly", (tx) => promised(tx.objectStore("blobs").get(key)));
      return v === undefined ? null : v;
    },
    async deleteBlob(key) {
      await transact(db, "blobs", "readwrite", (tx) => promised(tx.objectStore("blobs").delete(key)));
    },
    async putRecovered(item) {
      const record = { ...item, id: item.id || `r${Date.now()}${Math.random().toString(36).slice(2, 8)}` };
      await transact(db, "recovered", "readwrite", (tx) => promised(tx.objectStore("recovered").put(record)));
      return record.id;
    },
    async listRecovered() {
      const all = await transact(db, "recovered", "readonly", (tx) => promised(tx.objectStore("recovered").getAll()));
      return all.sort((a, b) => timeOf(b.createdAt) - timeOf(a.createdAt));
    },
    async deleteRecovered(id) {
      await transact(db, "recovered", "readwrite", (tx) => promised(tx.objectStore("recovered").delete(id)));
    },
    async putComments(itemId, list) {
      const record = { noteId: itemId, list: list || [], cachedAt: new Date().toISOString() };
      await transact(db, "comments", "readwrite", (tx) => promised(tx.objectStore("comments").put(record)));
    },
    async listComments(itemId) {
      const entry = await transact(db, "comments", "readonly", (tx) => promised(tx.objectStore("comments").get(itemId)));
      return entry ? entry.list : [];
    },
    async wipe() {
      await transact(db, STORES, "readwrite", (tx) => {
        for (const s of STORES) tx.objectStore(s).clear();
      });
      for (const k of Object.keys(cache)) invalidate(k);
      index.reset();
    },
    close() {
      if (channel) channel.close();
      db.close();
      const set = openConnections.get(name);
      if (set) set.delete(db);
    },
  };
}

/**
 * Closes this page's connections to the user's database and deletes it.
 * @param {string} userId
 * @returns {Promise<void>}
 */
export async function deleteStore(userId) {
  const name = databaseName(userId);
  for (const db of openConnections.get(name) || []) db.close();
  openConnections.delete(name);
  if (typeof indexedDB === "undefined") return;
  await new Promise((resolve, reject) => {
    const req = indexedDB.deleteDatabase(name);
    req.onsuccess = () => resolve();
    req.onerror = () => reject(req.error);
    req.onblocked = () => resolve();
  });
}
