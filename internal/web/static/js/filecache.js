// File content cache for the offline client, as docs/files.md "File content
// cache" describes. File bytes live in Cache Storage under a per-user cache.
// An index in its own IndexedDB database records the hash, size, last access
// and pinned state of each cached file, plus the pins the user set. The
// service worker serves /files/* from the same cache.

/** Default budget per device, in bytes. */
export const DEFAULT_BUDGET = 500 * 1024 * 1024;
/** localStorage key of the budget, bytes as a decimal string. */
export const BUDGET_KEY = "markspace.files.budget";
/** An opened, unpinned file larger than this is not cached. */
export const OPEN_CACHE_MAX = 50 * 1024 * 1024;

const PDF_BUNDLE = ["/static/vendor/pdfjs.js", "/static/vendor/pdfworker.js"];
const INDEX_VERSION = 1;

/**
 * Returns the Cache Storage name of a user's file cache.
 * @param {string} userId
 * @returns {string}
 */
export function cacheName(userId) {
  return `markspace-files-${userId}`;
}

/**
 * Returns the same-origin URL used as the Cache Storage key of a file.
 * @param {string} fileId
 * @returns {string}
 */
export function cacheKey(fileId) {
  return `/__markspace/files/${encodeURIComponent(fileId)}`;
}

/**
 * Extracts the file id from a content URL path such as
 * "/files/{space}/{id}/{name}".
 * @param {string} pathname
 * @returns {string} the id, or "" for any other path
 */
export function fileIdFromUrl(pathname) {
  const m = /^\/files\/[^/]+\/([^/?#]+)(?:\/|$)/.exec(String(pathname || ""));
  if (!m) return "";
  try {
    return decodeURIComponent(m[1]);
  } catch {
    return "";
  }
}

/**
 * Parses an HTTP Range header against a body size. Only the first range of a
 * multi-range header counts.
 * @param {string|null|undefined} header
 * @param {number} size
 * @returns {null|{start: number, end: number}|{unsatisfiable: true}} null
 *   for a missing or malformed header, inclusive offsets, or unsatisfiable
 */
export function parseRange(header, size) {
  if (typeof header !== "string") return null;
  const m = /^\s*bytes\s*=\s*([^,]*)/i.exec(header);
  if (!m) return null;
  const spec = m[1].trim();
  const parts = /^(\d*)\s*-\s*(\d*)$/.exec(spec);
  if (!parts || (parts[1] === "" && parts[2] === "")) return null;
  const total = Math.max(0, Number(size) || 0);
  if (parts[1] === "") {
    const n = Number(parts[2]);
    if (n === 0 || total === 0) return { unsatisfiable: true };
    return { start: Math.max(0, total - n), end: total - 1 };
  }
  const start = Number(parts[1]);
  let end = parts[2] === "" ? total - 1 : Number(parts[2]);
  if (parts[2] !== "" && end < start) return null;
  if (start >= total) return { unsatisfiable: true };
  if (end >= total) end = total - 1;
  return { start, end };
}

/**
 * Plans which files to evict so the cache fits the budget. Pinned files are
 * never evicted. Least recently used files go first.
 * @param {{fileId: string, size: number, lastAccess: number, pinned: boolean}[]} entries
 * @param {number} budget bytes
 * @returns {string[]} file ids to evict, in eviction order
 */
export function planEviction(entries, budget) {
  const list = entries || [];
  let total = list.reduce((sum, e) => sum + (Number(e.size) || 0), 0);
  const out = [];
  if (total <= budget) return out;
  const candidates = list.filter((e) => !e.pinned).sort((a, b) => (a.lastAccess || 0) - (b.lastAccess || 0) || String(a.fileId).localeCompare(String(b.fileId)));
  for (const e of candidates) {
    if (total <= budget) break;
    out.push(e.fileId);
    total -= Number(e.size) || 0;
  }
  return out;
}

/**
 * Builds a lookup that reports the pinned folder covering a folder id, by the
 * parentId chain and then by path prefix.
 * @param {object[]} folders
 * @param {Set<string>} pinnedFolders
 * @returns {(folderId: string, path?: string, spaceId?: string) => string|null}
 */
function folderPinLookup(folders, pinnedFolders) {
  const byId = new Map((folders || []).map((f) => [f.id, f]));
  const pinnedPaths = [];
  for (const id of pinnedFolders) {
    const f = byId.get(id);
    if (f && !f.deleted && f.path) pinnedPaths.push({ id, spaceId: f.spaceId, path: f.path });
  }
  const memo = new Map();
  const viaChain = (folderId) => {
    const seen = new Set();
    let id = folderId;
    const walked = [];
    while (id && !seen.has(id)) {
      if (memo.has(id)) {
        const hit = memo.get(id);
        for (const w of walked) memo.set(w, hit);
        return hit;
      }
      seen.add(id);
      walked.push(id);
      if (pinnedFolders.has(id)) {
        for (const w of walked) memo.set(w, id);
        return id;
      }
      const f = byId.get(id);
      id = f ? f.parentId || "" : "";
    }
    for (const w of walked) memo.set(w, null);
    return null;
  };
  return (folderId, path = "", spaceId = "") => {
    const hit = folderId ? viaChain(folderId) : null;
    if (hit) return hit;
    if (path) {
      for (const p of pinnedPaths) {
        if (spaceId && p.spaceId && p.spaceId !== spaceId) continue;
        if (path.startsWith(p.path + "/")) return p.id;
      }
    }
    return null;
  };
}

/**
 * Returns the ids of live files that are pinned directly or that sit under a
 * pinned folder at any depth. A file added to a pinned folder later is
 * included as soon as it is in the list.
 * @param {{files: object[], folders: object[], pins: {files?: string[], folders?: string[]}}} input
 * @returns {Set<string>}
 */
export function wantedFiles({ files = [], folders = [], pins = {} } = {}) {
  return new Set(pinReasons({ files, folders, pins }).keys());
}

/**
 * Maps each wanted file id to the id of the pin that covers it: the file id
 * for a direct pin, or a folder id.
 * @param {{files: object[], folders: object[], pins: {files?: string[], folders?: string[]}}} input
 * @returns {Map<string, string>}
 */
function pinReasons({ files = [], folders = [], pins = {} }) {
  const pinnedFiles = new Set(pins.files || []);
  const pinnedFolders = new Set(pins.folders || []);
  const lookup = folderPinLookup(folders, pinnedFolders);
  const out = new Map();
  for (const f of files || []) {
    if (!f || f.deleted) continue;
    if (pinnedFiles.has(f.id)) {
      out.set(f.id, f.id);
      continue;
    }
    if (!pinnedFolders.size) continue;
    const via = lookup(f.parentId || "", f.path || "", f.spaceId || "");
    if (via) out.set(f.id, via);
  }
  return out;
}

// ---------------------------------------------------------------------------
// Index storage

/**
 * Creates the in-memory index used when IndexedDB is missing.
 * @returns {object}
 */
function memoryIndex() {
  const stores = { entries: new Map(), pins: new Map() };
  return {
    async all(name) {
      return [...stores[name].values()].map((v) => ({ ...v }));
    },
    async put(name, value) {
      const key = name === "entries" ? value.fileId : value.id;
      stores[name].set(key, { ...value });
    },
    async delete(name, key) {
      stores[name].delete(key);
    },
    async clear() {
      stores.entries.clear();
      stores.pins.clear();
    },
    close() {},
  };
}

/**
 * Opens the IndexedDB index of a user's file cache.
 * @param {IDBFactory} idb
 * @param {string} name
 * @returns {Promise<object>}
 */
async function idbIndex(idb, name) {
  const db = await new Promise((resolve, reject) => {
    const req = idb.open(name, INDEX_VERSION);
    req.onupgradeneeded = () => {
      const d = req.result;
      if (!d.objectStoreNames.contains("entries")) d.createObjectStore("entries", { keyPath: "fileId" });
      if (!d.objectStoreNames.contains("pins")) d.createObjectStore("pins", { keyPath: "id" });
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
    req.onblocked = () => reject(new Error("the file cache index is open in an older tab"));
  });
  db.onversionchange = () => db.close();
  const run = (store, mode, fn) =>
    new Promise((resolve, reject) => {
      const tx = db.transaction(store, mode);
      let result;
      const req = fn(tx.objectStore(store));
      if (req) req.onsuccess = () => {
        result = req.result;
      };
      tx.oncomplete = () => resolve(result);
      tx.onerror = () => reject(tx.error);
      tx.onabort = () => reject(tx.error || new Error("transaction aborted"));
    });
  return {
    all: (name) => run(name, "readonly", (s) => s.getAll()),
    put: (name, value) => run(name, "readwrite", (s) => s.put(value)),
    delete: (name, key) => run(name, "readwrite", (s) => s.delete(key)),
    clear: () =>
      new Promise((resolve, reject) => {
        const tx = db.transaction(["entries", "pins"], "readwrite");
        tx.objectStore("entries").clear();
        tx.objectStore("pins").clear();
        tx.oncomplete = () => resolve();
        tx.onerror = () => reject(tx.error);
      }),
    close: () => db.close(),
  };
}

/**
 * Deletes a user's file cache and its index database.
 * @param {string} userId
 * @param {{cachesImpl?: CacheStorage, idb?: IDBFactory}} [deps]
 * @returns {Promise<void>}
 */
export async function deleteFileCache(userId, { cachesImpl = globalThis.caches, idb = globalThis.indexedDB } = {}) {
  if (!userId) return;
  if (cachesImpl) await cachesImpl.delete(cacheName(userId)).catch(() => {});
  if (!idb) return;
  await new Promise((resolve) => {
    const req = idb.deleteDatabase(cacheName(userId));
    req.onsuccess = () => resolve();
    req.onerror = () => resolve();
    req.onblocked = () => resolve();
  });
}

// ---------------------------------------------------------------------------
// File cache

const sameSha = (entry, file) => !file || !file.sha256 || !entry.sha256 || entry.sha256 === file.sha256;
const isPdf = (f) => /pdf/i.test(String((f && f.mime) || "")) || /\.pdf$/i.test(String((f && (f.name || f.path)) || ""));

/**
 * Creates the file cache of one user.
 * @param {object} deps
 * @param {string} deps.userId
 * @param {object} deps.store a Store with listAllFiles and listAllFolders
 * @param {CacheStorage} [deps.cachesImpl]
 * @param {Function} [deps.fetchImpl]
 * @param {IDBFactory|null} [deps.idb] without it the index lives in memory
 * @param {Storage|null} [deps.storage] holds the budget
 * @param {() => number} [deps.now]
 * @param {() => boolean} [deps.online]
 * @param {object|null} [deps.serviceWorker] navigator.serviceWorker
 * @returns {object} FileCache
 */
export function createFileCache({
  userId,
  store,
  cachesImpl = globalThis.caches,
  fetchImpl = globalThis.fetch ? (...args) => globalThis.fetch(...args) : null,
  idb = globalThis.indexedDB,
  storage = (() => {
    try {
      return globalThis.localStorage || null;
    } catch {
      return null;
    }
  })(),
  now = Date.now,
  online = () => typeof navigator === "undefined" || navigator.onLine !== false,
  serviceWorker = typeof navigator !== "undefined" && navigator.serviceWorker ? navigator.serviceWorker : null,
} = {}) {
  if (!userId) throw new Error("createFileCache: userId is required");
  const name = cacheName(userId);
  const entries = new Map();
  const pins = new Map();
  const listeners = new Map();
  const cleanups = [];
  let index = null;
  let destroyed = false;
  let reconciling = null;
  let reconcileAgain = false;
  let lastFiles = null;
  let lastFolders = null;
  let chain = Promise.resolve();

  /** Runs fn after earlier index writes, so writes never interleave. */
  const serial = (fn) => {
    const run = chain.then(fn);
    chain = run.catch(() => {});
    return run;
  };

  const emit = (event, payload) => {
    for (const fn of [...(listeners.get(event) || [])]) {
      try {
        fn(payload);
      } catch (err) {
        if (typeof reportError === "function") reportError(err);
      }
    }
  };
  const changed = (fileIds) => emit("changed", { fileIds: [...new Set(fileIds)] });

  const ready = (async () => {
    try {
      index = idb ? await idbIndex(idb, name) : memoryIndex();
    } catch {
      index = memoryIndex();
    }
    for (const e of await index.all("entries")) entries.set(e.fileId, e);
    for (const p of await index.all("pins")) pins.set(p.id, p);
  })();

  const openCache = () => (cachesImpl ? cachesImpl.open(name) : Promise.resolve(null));

  function pinLists() {
    const files = [];
    const folders = [];
    for (const p of pins.values()) (p.kind === "folder" ? folders : files).push(p.id);
    return { files, folders };
  }

  async function loadItems() {
    if (!store) return { files: [], folders: [] };
    const [files, folders] = await Promise.all([
      typeof store.listAllFiles === "function" ? store.listAllFiles({ includeDeleted: false }) : [],
      typeof store.listAllFolders === "function" ? store.listAllFolders({ includeDeleted: false }) : [],
    ]);
    lastFiles = files || [];
    lastFolders = folders || [];
    return { files: lastFiles, folders: lastFolders };
  }

  function budget() {
    let raw = null;
    try {
      raw = storage ? storage.getItem(BUDGET_KEY) : null;
    } catch {
      raw = null;
    }
    const n = Number(raw);
    return raw !== null && Number.isFinite(n) && n > 0 ? Math.floor(n) : DEFAULT_BUDGET;
  }

  function setBudget(bytes) {
    const n = Math.floor(Number(bytes));
    try {
      if (!storage) {
        // Nothing to store the value in.
      } else if (!Number.isFinite(n) || n <= 0 || n === DEFAULT_BUDGET) storage.removeItem(BUDGET_KEY);
      else storage.setItem(BUDGET_KEY, String(n));
    } catch {
      // Storage is full or disabled.
    }
    return ready.then(() => serial(evictOverBudget));
  }

  async function dropEntry(fileId) {
    const cache = await openCache();
    if (cache) await cache.delete(cacheKey(fileId));
    entries.delete(fileId);
    await index.delete("entries", fileId);
  }

  /** Evicts unpinned files until the cache fits the budget. Runs under serial. */
  async function evictOverBudget() {
    const victims = planEviction([...entries.values()], budget());
    for (const id of victims) await dropEntry(id);
    if (victims.length) changed(victims);
    return victims;
  }

  /**
   * Fetches a file and stores it. Runs under serial.
   * @returns {Promise<boolean>} true when stored
   */
  async function download(file, pinned) {
    if (!fetchImpl || !cachesImpl || !file || !file.url) return false;
    let res;
    try {
      res = await fetchImpl(file.url, { credentials: "same-origin", cache: "no-store" });
    } catch {
      return false;
    }
    if (!res || res.status !== 200 || res.redirected) return false;
    const blob = await res.blob();
    const headers = new Headers({
      "Content-Type": file.mime || res.headers.get("Content-Type") || "application/octet-stream",
      "Content-Length": String(blob.size),
      "X-Markspace-Sha256": file.sha256 || "",
    });
    const cache = await openCache();
    await cache.put(cacheKey(file.id), new Response(blob, { status: 200, headers }));
    const entry = {
      fileId: file.id,
      sha256: file.sha256 || "",
      size: blob.size,
      lastAccess: now(),
      pinned: Boolean(pinned),
      url: file.url,
      mime: file.mime || "",
    };
    entries.set(file.id, entry);
    await index.put("entries", entry);
    return true;
  }

  function status(fileId) {
    const e = entries.get(fileId);
    const reason = pinReasonOf(fileId);
    return { cached: Boolean(e), pinned: Boolean(reason), via: reason && reason !== fileId ? reason : null };
  }

  /** Finds the pin covering a file from the last loaded items. */
  function pinReasonOf(fileId) {
    if (pins.has(fileId)) return fileId;
    if (!lastFiles) {
      const e = entries.get(fileId);
      return e && e.pinned ? fileId : null;
    }
    const file = lastFiles.find((f) => f.id === fileId);
    if (!file) return null;
    return pinReasons({ files: [file], folders: lastFolders || [], pins: pinLists() }).get(fileId) || null;
  }

  function isPinned(item) {
    if (!item || !item.id) return { pinned: false, via: null };
    if (item.kind === "folder") {
      if (pins.has(item.id)) return { pinned: true, via: null };
      const folders = lastFolders || [];
      const folder = folders.find((f) => f.id === item.id);
      if (!folder) return { pinned: false, via: null };
      const lookup = folderPinLookup(folders, new Set(pinLists().folders));
      const via = lookup(folder.parentId || "", folder.path || "", folder.spaceId || "");
      return { pinned: Boolean(via), via: via || null };
    }
    const s = status(item.id);
    return { pinned: s.pinned, via: s.via };
  }

  async function isAvailable(file) {
    await ready;
    const e = file && entries.get(file.id);
    return Boolean(e && sameSha(e, file));
  }

  async function touch(fileId) {
    await ready;
    await serial(async () => {
      const e = entries.get(fileId);
      if (!e) return;
      e.lastAccess = now();
      await index.put("entries", e);
    });
  }

  async function open(file) {
    await ready;
    if (!file || !file.id) return false;
    if (!lastFiles) await loadItems().catch(() => {});
    const result = await serial(async () => {
      const e = entries.get(file.id);
      if (e && !sameSha(e, file)) {
        await dropEntry(file.id);
        changed([file.id]);
      } else if (e) {
        e.lastAccess = now();
        await index.put("entries", e);
        return true;
      }
      const pinned = Boolean(pinReasonOf(file.id));
      if (!online() || (!pinned && (Number(file.size) || 0) > OPEN_CACHE_MAX)) return false;
      const stored = await download(file, pinned);
      if (stored) {
        changed([file.id]);
        await evictOverBudget();
      }
      return entries.has(file.id);
    });
    return result;
  }

  async function getBlob(file) {
    await ready;
    const e = file && entries.get(file.id);
    if (!e || !sameSha(e, file) || !cachesImpl) return null;
    const cache = await openCache();
    const res = await cache.match(cacheKey(file.id));
    return res ? res.blob() : null;
  }

  async function pin(item) {
    await ready;
    if (!item || !item.id) return;
    await serial(async () => {
      const record = { id: item.id, kind: item.kind === "folder" ? "folder" : "file" };
      pins.set(item.id, record);
      await index.put("pins", record);
    });
    changed([item.id]);
    reconcile().catch(() => {});
  }

  async function unpin(item) {
    await ready;
    if (!item || !item.id) return;
    await serial(async () => {
      pins.delete(item.id);
      await index.delete("pins", item.id);
    });
    await reconcile().catch(() => {});
    changed([item.id]);
  }

  /** Brings the pinned flags, downloads and evictions in line with the store. */
  async function reconcileOnce() {
    const { files, folders } = await loadItems();
    const reasons = pinReasons({ files, folders, pins: pinLists() });
    const byId = new Map(files.map((f) => [f.id, f]));
    const touched = [];
    await serial(async () => {
      for (const e of [...entries.values()]) {
        const file = byId.get(e.fileId);
        if (!file) {
          await dropEntry(e.fileId);
          touched.push(e.fileId);
          continue;
        }
        const pinned = reasons.has(e.fileId);
        if (e.pinned !== pinned) {
          e.pinned = pinned;
          await index.put("entries", e);
          touched.push(e.fileId);
        }
        if (!sameSha(e, file) && !pinned) {
          await dropEntry(e.fileId);
          touched.push(e.fileId);
        }
      }
    });
    if (online()) {
      let warmPdf = false;
      for (const id of reasons.keys()) {
        if (destroyed || !online()) break;
        const file = byId.get(id);
        if (!file) continue;
        if (isPdf(file)) warmPdf = true;
        const e = entries.get(id);
        if (e && sameSha(e, file)) continue;
        const stored = await serial(() => download(file, true));
        if (stored) touched.push(id);
      }
      if (warmPdf && fetchImpl) {
        for (const url of PDF_BUNDLE) {
          try {
            await fetchImpl(url, { credentials: "same-origin" });
          } catch {
            // Offline or missing. The next reconcile tries again.
          }
        }
      }
    }
    const evicted = await serial(evictOverBudget);
    const all = touched.filter((id) => !evicted.includes(id));
    if (all.length) changed(all);
  }

  function reconcile() {
    if (reconciling) {
      reconcileAgain = true;
      return reconciling;
    }
    reconciling = (async () => {
      await ready;
      try {
        do {
          reconcileAgain = false;
          await reconcileOnce();
        } while (reconcileAgain && !destroyed);
      } finally {
        reconciling = null;
      }
    })();
    return reconciling;
  }

  async function usage() {
    await ready;
    let used = 0;
    let pinnedBytes = 0;
    let pinnedCount = 0;
    for (const e of entries.values()) {
      used += Number(e.size) || 0;
      if (e.pinned) {
        pinnedBytes += Number(e.size) || 0;
        pinnedCount++;
      }
    }
    return { used, count: entries.size, pinnedBytes, pinnedCount, budget: budget() };
  }

  async function clear() {
    await ready;
    const ids = [...entries.keys()];
    await serial(async () => {
      if (cachesImpl) await cachesImpl.delete(name);
      entries.clear();
      pins.clear();
      await index.clear();
    });
    changed(ids);
  }

  function on(event, fn) {
    if (!listeners.has(event)) listeners.set(event, new Set());
    listeners.get(event).add(fn);
    return () => listeners.get(event).delete(fn);
  }

  function announceUser() {
    if (!serviceWorker) return;
    const target = serviceWorker.controller || null;
    if (target && typeof target.postMessage === "function") target.postMessage({ type: "filecache-user", userId });
    if (serviceWorker.ready && typeof serviceWorker.ready.then === "function") {
      serviceWorker.ready.then((reg) => {
        if (reg && reg.active && reg.active !== target) reg.active.postMessage({ type: "filecache-user", userId });
      }, () => {});
    }
  }

  if (serviceWorker && typeof serviceWorker.addEventListener === "function") {
    const onMessage = (event) => {
      const data = event && event.data;
      if (data && data.type === "file-accessed" && data.fileId) touch(data.fileId).catch(() => {});
    };
    serviceWorker.addEventListener("message", onMessage);
    serviceWorker.addEventListener("controllerchange", announceUser);
    cleanups.push(() => {
      serviceWorker.removeEventListener("message", onMessage);
      serviceWorker.removeEventListener("controllerchange", announceUser);
    });
  }
  announceUser();

  return {
    ready,
    budget,
    setBudget,
    status,
    isAvailable,
    open,
    getBlob,
    touch,
    pin,
    unpin,
    isPinned,
    reconcile,
    usage,
    clear,
    on,
    destroy() {
      destroyed = true;
      for (const fn of cleanups) fn();
      listeners.clear();
      ready.then(() => index && index.close()).catch(() => {});
    },
  };
}
