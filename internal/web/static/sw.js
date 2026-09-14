// Service worker for the Markspace offline client.
//
// The Go handler at /sw.js fills in BUILD with a hash of the embedded static
// files and ASSETS with the list of /static/ URLs to precache, so every
// deploy installs a fresh cache. This file is a template and is never
// registered from /static/.

const BUILD = "__MARKSPACE_BUILD__";
const ASSETS = __MARKSPACE_ASSETS__;

const STATIC_PREFIX = "markspace-static-";
const STATIC_CACHE = STATIC_PREFIX + BUILD;
const PDFJS_PREFIX = "markspace-pdfjs-";
const PDFJS_CACHE = PDFJS_PREFIX + BUILD;
const THUMBS_CACHE = "markspace-thumbs-v1";
const THUMBS_MAX_ENTRIES = 500;
const META_CACHE = "markspace-sw-meta";
const USER_KEY = "/__markspace/user";
const FILES_PREFIX = "markspace-files-";
const LEGACY_FILES_CACHE = "markspace-files-v1";
const SHELL_URL = "/app-shell";
const OFFLINE_URL = "/offline";
const MANIFEST_URL = "/manifest.webmanifest";

// Navigations to these paths get the cached application shell.
const SHELL_ROUTES = [
  /^\/$/,
  /^\/s\/[^/]+$/,
  /^\/s\/[^/]+\/n\/.+$/,
  /^\/s\/[^/]+\/d\/.+$/,
  /^\/s\/[^/]+\/f\/.+$/,
  /^\/s\/[^/]+\/t\/[^/]+$/,
  /^\/s\/[^/]+\/trash$/,
  /^\/s\/[^/]+\/files$/,
  /^\/n\/[^/]+$/,
  /^\/i\/[^/]+$/,
  /^\/search$/,
  /^\/starred$/,
  /^\/recent$/,
  /^\/shared$/,
];

/**
 * Reports whether a path is one of the client-rendered shell routes.
 * @param {string} pathname
 * @returns {boolean}
 */
function isShellRoute(pathname) {
  return SHELL_ROUTES.some((re) => re.test(pathname));
}

// ---------------------------------------------------------------------------
// Range requests. parseRange behaves exactly as parseRange in
// static/js/filecache.js.

/**
 * Parses an HTTP Range header against a body size. Only the first range of a
 * multi-range header counts.
 * @param {string|null|undefined} header
 * @param {number} size
 * @returns {null|{start: number, end: number}|{unsatisfiable: true}}
 */
function parseRange(header, size) {
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
 * Builds the response for a cached blob. Without a usable Range header the
 * answer is 200 with the whole body. A satisfiable range answers 206 with the
 * slice. An unsatisfiable range answers 416.
 * @param {Blob} blob
 * @param {string|null} rangeHeader
 * @param {Headers|object} headers stored headers such as Content-Type
 * @returns {Response}
 */
function rangeResponse(blob, rangeHeader, headers) {
  const out = new Headers(headers || {});
  out.set("Accept-Ranges", "bytes");
  const size = blob.size;
  const range = parseRange(rangeHeader, size);
  if (range && range.unsatisfiable) {
    out.delete("Content-Length");
    out.set("Content-Range", `bytes */${size}`);
    return new Response(null, { status: 416, statusText: "Range Not Satisfiable", headers: out });
  }
  if (!range) {
    out.set("Content-Length", String(size));
    return new Response(blob, { status: 200, headers: out });
  }
  const slice = blob.slice(range.start, range.end + 1);
  out.set("Content-Length", String(slice.size));
  out.set("Content-Range", `bytes ${range.start}-${range.end}/${size}`);
  return new Response(slice, { status: 206, statusText: "Partial Content", headers: out });
}

// ---------------------------------------------------------------------------
// Caches

/**
 * Fetches a URL, bypassing the HTTP cache, and stores a good response.
 * Redirected and failed responses are not stored.
 * @param {Cache} cache
 * @param {string} url
 * @returns {Promise<void>}
 */
async function fetchIntoCache(cache, url) {
  const res = await fetch(new Request(url, { cache: "reload", credentials: "same-origin" }));
  if (!res.ok || res.redirected) throw new Error(`${url}: status ${res.status}${res.redirected ? " (redirected)" : ""}`);
  await cache.put(url, res);
}

/**
 * Precaches the shell, the offline page, the manifest and every asset. One
 * failed URL is logged and does not fail the install.
 * @returns {Promise<void>}
 */
async function precache() {
  const cache = await caches.open(STATIC_CACHE);
  const urls = [SHELL_URL, OFFLINE_URL, MANIFEST_URL, ...ASSETS];
  const results = await Promise.allSettled(urls.map((url) => fetchIntoCache(cache, url)));
  for (const r of results) {
    if (r.status === "rejected") console.warn("markspace sw: precache skipped", r.reason && r.reason.message);
  }
}

/**
 * Deletes static and pdf.js caches from earlier builds and the attachment
 * cache of the old model.
 * @returns {Promise<void>}
 */
async function deleteOldCaches() {
  const names = await caches.keys();
  const stale = (n) =>
    (n.startsWith(STATIC_PREFIX) && n !== STATIC_CACHE) || (n.startsWith(PDFJS_PREFIX) && n !== PDFJS_CACHE) || n === LEGACY_FILES_CACHE;
  await Promise.all(names.filter(stale).map((n) => caches.delete(n)));
}

/**
 * Looks a URL up in the current static cache.
 * @param {string|Request} request
 * @returns {Promise<Response|undefined>}
 */
async function fromStaticCache(request) {
  const cache = await caches.open(STATIC_CACHE);
  return cache.match(request);
}

/**
 * Refreshes the cached shell in the background. Failures are ignored.
 * @returns {Promise<void>}
 */
async function refreshShell() {
  try {
    const cache = await caches.open(STATIC_CACHE);
    await fetchIntoCache(cache, SHELL_URL);
  } catch {
    // Offline or signed out. The cached copy stays.
  }
}

/**
 * Answers a shell route with the cached shell, falling back to the network
 * and then to the offline page.
 * @param {FetchEvent} event
 * @returns {Promise<Response>}
 */
async function shellResponse(event) {
  const cached = await fromStaticCache(SHELL_URL);
  if (cached) {
    event.waitUntil(refreshShell());
    return cached;
  }
  try {
    const res = await fetch(event.request);
    event.waitUntil(refreshShell());
    return res;
  } catch {
    return (await fromStaticCache(OFFLINE_URL)) || Response.error();
  }
}

/**
 * Answers any other navigation from the network, falling back to the cached
 * offline page.
 * @param {Request} request
 * @returns {Promise<Response>}
 */
async function networkNavigation(request) {
  try {
    return await fetch(request);
  } catch {
    return (await fromStaticCache(OFFLINE_URL)) || Response.error();
  }
}

/**
 * Serves a static asset from the cache, fetching and storing it on a miss.
 * @param {Request} request
 * @param {string} cacheName
 * @returns {Promise<Response>}
 */
async function cacheFirst(request, cacheName) {
  const cache = await caches.open(cacheName);
  const cached = await cache.match(request);
  if (cached) return cached;
  const res = await fetch(request);
  if (res.ok && !res.redirected) await cache.put(request, res.clone());
  return res;
}

/**
 * Drops the oldest entries of a cache until it holds at most max entries.
 * @param {Cache} cache
 * @param {number} max
 * @returns {Promise<void>}
 */
async function trimCache(cache, max) {
  const keys = await cache.keys();
  for (let i = 0; i < keys.length - max; i++) await cache.delete(keys[i]);
}

/**
 * Serves a thumbnail from its runtime cache, fetching and storing it on a
 * miss. The cache keeps at most 500 entries.
 * @param {FetchEvent} event
 * @returns {Promise<Response>}
 */
async function thumbnail(event) {
  const cache = await caches.open(THUMBS_CACHE);
  const cached = await cache.match(event.request);
  if (cached) return cached;
  const res = await fetch(event.request);
  if (res.status === 200 && !res.redirected && res.type === "basic") {
    await cache.put(event.request, res.clone());
    event.waitUntil(trimCache(cache, THUMBS_MAX_ENTRIES));
  }
  return res;
}

// ---------------------------------------------------------------------------
// File content

let currentUser = null;
let userLoaded = null;

/**
 * Returns the signed-in user id the page announced, reading the persisted
 * value once after a restart.
 * @returns {Promise<string|null>}
 */
async function userId() {
  if (currentUser) return currentUser;
  if (!userLoaded) {
    userLoaded = (async () => {
      try {
        const cache = await caches.open(META_CACHE);
        const res = await cache.match(USER_KEY);
        if (res) currentUser = (await res.text()) || null;
      } catch {
        // No stored user. File requests go to the network.
      }
    })();
  }
  await userLoaded;
  return currentUser;
}

/**
 * Records the signed-in user id and persists it for a restarted worker.
 * @param {string} id
 * @returns {Promise<void>}
 */
async function setUser(id) {
  currentUser = id || null;
  userLoaded = Promise.resolve();
  const cache = await caches.open(META_CACHE);
  if (currentUser) await cache.put(USER_KEY, new Response(currentUser, { headers: { "Content-Type": "text/plain" } }));
  else await cache.delete(USER_KEY);
}

/**
 * Parses /files/{space}/{id}/{name} into the id and the decoded name.
 * @param {string} pathname
 * @returns {{id: string, name: string}|null}
 */
function fileParts(pathname) {
  const m = /^\/files\/[^/]+\/([^/]+)\/(.+)$/.exec(pathname);
  if (!m) return null;
  try {
    return { id: decodeURIComponent(m[1]), name: decodeURIComponent(m[2]) };
  } catch {
    return null;
  }
}

/**
 * Tells the page that a cached file was read, so its index updates
 * lastAccess.
 * @param {FetchEvent} event
 * @param {string} fileId
 * @returns {Promise<void>}
 */
async function notifyAccess(event, fileId) {
  const message = { type: "file-accessed", fileId };
  const id = event.clientId || event.resultingClientId;
  if (id) {
    const client = await self.clients.get(id);
    if (client) {
      client.postMessage(message);
      return;
    }
  }
  const all = await self.clients.matchAll({ type: "window" });
  for (const c of all) c.postMessage(message);
}

/**
 * Serves file content from the user's file cache, answering Range requests
 * from the cached blob, and falls back to the network. Nothing is stored
 * here. The page fills the cache.
 * @param {FetchEvent} event
 * @param {URL} url
 * @returns {Promise<Response>}
 */
async function fileContent(event, url) {
  const request = event.request;
  const parts = fileParts(url.pathname);
  if (!parts || url.searchParams.has("v")) return fetch(request);
  const user = await userId();
  if (!user) return fetch(request);
  const download = url.searchParams.get("download") === "1";
  if (download && self.navigator && self.navigator.onLine !== false) {
    try {
      return await fetch(request);
    } catch {
      // Offline after all. Try the cache.
    }
  }
  let cached;
  try {
    const cache = await caches.open(FILES_PREFIX + user);
    cached = await cache.match("/__markspace/files/" + encodeURIComponent(parts.id));
  } catch {
    cached = undefined;
  }
  if (!cached) {
    try {
      return await fetch(request);
    } catch {
      return Response.error();
    }
  }
  const blob = await cached.blob();
  const headers = new Headers(cached.headers);
  if (download) headers.set("Content-Disposition", "attachment; filename*=UTF-8''" + encodeURIComponent(parts.name));
  event.waitUntil(notifyAccess(event, parts.id).catch(() => {}));
  return rangeResponse(blob, request.headers.get("Range"), headers);
}

/**
 * Serves the manifest from the network, falling back to the cached copy.
 * @param {Request} request
 * @returns {Promise<Response>}
 */
async function manifest(request) {
  try {
    return await fetch(request);
  } catch {
    return (await fromStaticCache(MANIFEST_URL)) || Response.error();
  }
}

self.addEventListener("install", (event) => {
  event.waitUntil(precache());
});

self.addEventListener("activate", (event) => {
  event.waitUntil(deleteOldCaches().then(() => self.clients.claim()));
});

self.addEventListener("message", (event) => {
  const data = event.data;
  if (!data) return;
  if (data.type === "SKIP_WAITING") self.skipWaiting();
  if (data.type === "filecache-user" && typeof data.userId === "string") {
    const done = setUser(data.userId).catch(() => {});
    if (typeof event.waitUntil === "function") event.waitUntil(done);
  }
});

self.addEventListener("fetch", (event) => {
  const request = event.request;
  if (request.method !== "GET") return;
  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;
  if (url.pathname.startsWith("/api/")) return;
  if (request.mode === "navigate") {
    event.respondWith(isShellRoute(url.pathname) ? shellResponse(event) : networkNavigation(request));
    return;
  }
  if (url.pathname.startsWith("/static/vendor/pdf")) {
    event.respondWith(cacheFirst(request, PDFJS_CACHE));
    return;
  }
  if (url.pathname.startsWith("/static/")) {
    event.respondWith(cacheFirst(request, STATIC_CACHE));
    return;
  }
  if (url.pathname.startsWith("/files/")) {
    event.respondWith(fileContent(event, url));
    return;
  }
  if (url.pathname.startsWith("/thumbs/")) {
    event.respondWith(thumbnail(event));
    return;
  }
  if (url.pathname === MANIFEST_URL) {
    event.respondWith(manifest(request));
  }
});
