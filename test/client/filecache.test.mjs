import { test } from "node:test";
import assert from "node:assert/strict";

import {
  DEFAULT_BUDGET,
  BUDGET_KEY,
  OPEN_CACHE_MAX,
  cacheName,
  cacheKey,
  fileIdFromUrl,
  parseRange,
  planEviction,
  wantedFiles,
  createFileCache,
} from "../../internal/web/static/js/filecache.js";

test("names and keys", () => {
  assert.equal(cacheName("u1"), "markspace-files-u1");
  assert.equal(cacheKey("abc"), "/__markspace/files/abc");
  assert.equal(fileIdFromUrl("/files/docs/f1/east%20wall.jpg"), "f1");
  assert.equal(fileIdFromUrl("/files/docs/f%201/x"), "f 1");
  assert.equal(fileIdFromUrl("/thumbs/docs/f1"), "");
  assert.equal(fileIdFromUrl("/files/docs"), "");
});

test("parseRange", () => {
  assert.equal(parseRange(null, 100), null);
  assert.equal(parseRange("", 100), null);
  assert.equal(parseRange("items=0-1", 100), null);
  assert.equal(parseRange("bytes=-", 100), null);
  assert.equal(parseRange("bytes=5-2", 100), null);
  assert.deepEqual(parseRange("bytes=0-9", 100), { start: 0, end: 9 });
  assert.deepEqual(parseRange("bytes=90-", 100), { start: 90, end: 99 });
  assert.deepEqual(parseRange("bytes=90-500", 100), { start: 90, end: 99 });
  assert.deepEqual(parseRange("bytes=-10", 100), { start: 90, end: 99 });
  assert.deepEqual(parseRange("bytes=-500", 100), { start: 0, end: 99 });
  assert.deepEqual(parseRange("bytes=0-1, 5-6", 100), { start: 0, end: 1 });
  assert.deepEqual(parseRange("bytes=100-", 100), { unsatisfiable: true });
  assert.deepEqual(parseRange("bytes=-0", 100), { unsatisfiable: true });
  assert.deepEqual(parseRange("bytes=0-", 0), { unsatisfiable: true });
});

test("planEviction removes least recently used unpinned files until the budget fits", () => {
  const entries = [
    { fileId: "a", size: 40, lastAccess: 3, pinned: false },
    { fileId: "b", size: 40, lastAccess: 1, pinned: true },
    { fileId: "c", size: 40, lastAccess: 2, pinned: false },
    { fileId: "d", size: 40, lastAccess: 4, pinned: false },
  ];
  assert.deepEqual(planEviction(entries, 200), []);
  assert.deepEqual(planEviction(entries, 120), ["c"]);
  assert.deepEqual(planEviction(entries, 50), ["c", "a", "d"]);
  // Pinned files stay even when the budget cannot be met.
  assert.deepEqual(planEviction(entries, 0), ["c", "a", "d"]);
});

const folders = [
  { id: "F1", spaceId: "s1", parentId: "", path: "Projects" },
  { id: "F2", spaceId: "s1", parentId: "F1", path: "Projects/harbour" },
  { id: "F3", spaceId: "s1", parentId: "", path: "Archive" },
];

test("wantedFiles covers direct pins, nested folders, later files and skips deleted files", () => {
  const files = [
    { id: "x1", spaceId: "s1", parentId: "F2", path: "Projects/harbour/wall.jpg" },
    { id: "x2", spaceId: "s1", parentId: "F1", path: "Projects/report.pdf" },
    { id: "x3", spaceId: "s1", parentId: "F3", path: "Archive/old.pdf" },
    { id: "x4", spaceId: "s1", parentId: "", path: "root.txt" },
    { id: "x5", spaceId: "s1", parentId: "F2", path: "Projects/harbour/gone.png", deleted: true },
  ];
  assert.deepEqual([...wantedFiles({ files, folders, pins: { folders: ["F1"], files: ["x4"] } })].sort(), ["x1", "x2", "x4"]);
  const later = [...files, { id: "x6", spaceId: "s1", parentId: "F2", path: "Projects/harbour/new.png" }];
  assert.ok(wantedFiles({ files: later, folders, pins: { folders: ["F1"] } }).has("x6"));
  // Path prefix covers a file whose parent folder is not known locally.
  const orphan = [{ id: "x7", spaceId: "s1", parentId: "F9", path: "Archive/deep/x.bin" }];
  assert.ok(wantedFiles({ files: orphan, folders, pins: { folders: ["F3"] } }).has("x7"));
  assert.equal(wantedFiles({ files, folders, pins: {} }).size, 0);
});

// ---------------------------------------------------------------------------
// Fakes

function fakeCaches() {
  const stores = new Map();
  return {
    stores,
    async open(name) {
      if (!stores.has(name)) stores.set(name, new Map());
      const m = stores.get(name);
      return {
        async put(key, res) {
          m.set(String(key), { body: await res.arrayBuffer(), headers: new Headers(res.headers), status: res.status });
        },
        async match(key) {
          const hit = m.get(String(key));
          return hit ? new Response(hit.body, { status: hit.status, headers: hit.headers }) : undefined;
        },
        async delete(key) {
          return m.delete(String(key));
        },
        async keys() {
          return [...m.keys()];
        },
      };
    },
    async delete(name) {
      return stores.delete(name);
    },
  };
}

function fakeStorage() {
  const m = new Map();
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => m.set(k, String(v)),
    removeItem: (k) => m.delete(k),
  };
}

function setup({ files = [], budget } = {}) {
  const data = { files, folders: [...folders] };
  const bodies = new Map();
  const fetched = [];
  const state = { online: true, clock: 1000 };
  const store = {
    async listAllFiles() {
      return data.files.filter((f) => !f.deleted).map((f) => ({ ...f }));
    },
    async listAllFolders() {
      return data.folders.map((f) => ({ ...f }));
    },
  };
  const fetchImpl = async (url) => {
    fetched.push(url);
    if (!state.online) throw new TypeError("offline");
    if (!bodies.has(url)) return new Response("missing", { status: 404 });
    return new Response(bodies.get(url), { status: 200, headers: { "Content-Type": "application/octet-stream" } });
  };
  const caches = fakeCaches();
  const storage = fakeStorage();
  if (budget) storage.setItem(BUDGET_KEY, String(budget));
  const fc = createFileCache({
    userId: "u1",
    store,
    cachesImpl: caches,
    fetchImpl,
    idb: null,
    storage,
    now: () => ++state.clock,
    online: () => state.online,
    serviceWorker: null,
  });
  const addFile = (f, content) => {
    const file = { spaceId: "s1", mime: "application/octet-stream", url: `/files/docs/${f.id}/${f.name || f.id}`, sha256: "h1", ...f };
    file.size = content.length;
    data.files = data.files.filter((x) => x.id !== file.id).concat(file);
    bodies.set(file.url, content);
    return file;
  };
  return { fc, data, bodies, fetched, state, caches, storage, addFile };
}

test("open caches a small file and serves it back, and skips big unpinned files", async () => {
  const { fc, addFile, caches, fetched } = setup();
  await fc.ready;
  assert.equal(fc.budget(), DEFAULT_BUDGET);
  const small = addFile({ id: "x1", parentId: "F2", path: "Projects/harbour/a.txt" }, "hello");
  assert.equal(await fc.open(small), true);
  assert.deepEqual(fc.status("x1"), { cached: true, pinned: false, via: null });
  const blob = await fc.getBlob(small);
  assert.equal(await blob.text(), "hello");
  const stored = caches.stores.get("markspace-files-u1").get(cacheKey("x1"));
  assert.equal(stored.headers.get("X-Markspace-Sha256"), "h1");
  assert.equal(stored.headers.get("Content-Length"), "5");
  assert.equal(await fc.isAvailable(small), true);
  assert.equal(await fc.isAvailable({ ...small, sha256: "other" }), false);

  const big = { id: "big", url: "/files/docs/big/v.mp4", size: OPEN_CACHE_MAX + 1, sha256: "b" };
  const before = fetched.length;
  assert.equal(await fc.open(big), false);
  assert.equal(fetched.length, before);
});

test("open offline does not fetch, and a changed sha drops the stale entry", async () => {
  const { fc, addFile, state, fetched } = setup();
  await fc.ready;
  const f = addFile({ id: "x1", parentId: "F2", path: "Projects/harbour/a.txt" }, "one");
  await fc.open(f);
  state.online = false;
  const replaced = { ...f, sha256: "h2" };
  const before = fetched.length;
  assert.equal(await fc.open(replaced), false);
  assert.equal(fetched.length, before);
  assert.equal(fc.status("x1").cached, false);
  assert.equal(await fc.getBlob(f), null);
});

test("pinning a folder downloads its files, later files and re-downloads a replaced file", async () => {
  const { fc, addFile, bodies, fetched } = setup();
  await fc.ready;
  addFile({ id: "x1", parentId: "F2", path: "Projects/harbour/a.txt" }, "aaa");
  addFile({ id: "x2", parentId: "F3", path: "Archive/b.txt" }, "bbb");
  await fc.pin({ kind: "folder", id: "F1" });
  await fc.reconcile();
  assert.deepEqual(fc.status("x1"), { cached: true, pinned: true, via: "F1" });
  assert.equal(fc.status("x2").cached, false);
  assert.deepEqual(fc.isPinned({ kind: "folder", id: "F2" }), { pinned: true, via: "F1" });
  assert.deepEqual(fc.isPinned({ kind: "folder", id: "F1" }), { pinned: true, via: null });
  assert.deepEqual(fc.isPinned({ kind: "folder", id: "F3" }), { pinned: false, via: null });

  // A file added later under the pinned folder downloads on the next reconcile.
  addFile({ id: "x3", parentId: "F1", path: "Projects/c.pdf", mime: "application/pdf" }, "ccc");
  await fc.reconcile();
  assert.equal(fc.status("x3").cached, true);
  assert.ok(fetched.includes("/static/vendor/pdfjs.js"), "a pinned PDF warms the pdf.js bundle");

  // Replacing a pinned file downloads the new content.
  const replaced = addFile({ id: "x1", parentId: "F2", path: "Projects/harbour/a.txt", sha256: "h2" }, "new content");
  await fc.reconcile();
  assert.equal(await (await fc.getBlob(replaced)).text(), "new content");
  assert.equal(bodies.size, 3);

  // Unpinning keeps the files cached but unpinned.
  await fc.unpin({ kind: "folder", id: "F1" });
  assert.deepEqual(fc.status("x1"), { cached: true, pinned: false, via: null });
  const u = await fc.usage();
  assert.equal(u.pinnedCount, 0);
  assert.equal(u.count, 2);
});

test("budget eviction keeps pinned files and removes the least recently used", async () => {
  const { fc, addFile } = setup({ budget: 10 });
  await fc.ready;
  const a = addFile({ id: "a", parentId: "F3", path: "Archive/a" }, "12345");
  const b = addFile({ id: "b", parentId: "F3", path: "Archive/b" }, "12345");
  const p = addFile({ id: "p", parentId: "", path: "p" }, "1234");
  await fc.pin({ kind: "file", id: "p" });
  await fc.reconcile();
  await fc.open(a);
  await fc.open(b);
  // 4 + 5 + 5 = 14 > 10, so a, opened first, goes.
  assert.equal(fc.status("a").cached, false);
  assert.equal(fc.status("b").cached, true);
  assert.equal(fc.status("p").cached, true);
  // Opening b again makes it the most recent. A budget of 4 evicts b and keeps pinned p.
  await fc.touch("b");
  await fc.setBudget(4);
  assert.equal(fc.status("b").cached, false);
  assert.equal(fc.status("p").cached, true);
  const u = await fc.usage();
  assert.deepEqual(u, { used: 4, count: 1, pinnedBytes: 4, pinnedCount: 1, budget: 4 });
  void p;
});

test("reconcile drops entries of files that left the store, and clear empties everything", async () => {
  const { fc, addFile, data, caches } = setup();
  await fc.ready;
  const f = addFile({ id: "x1", parentId: "F3", path: "Archive/a" }, "abc");
  await fc.open(f);
  data.files = [];
  await fc.reconcile();
  assert.equal(fc.status("x1").cached, false);
  const g = addFile({ id: "x2", parentId: "F3", path: "Archive/b" }, "abc");
  await fc.pin({ kind: "file", id: "x2" });
  await fc.reconcile();
  const events = [];
  fc.on("changed", (e) => events.push(e));
  await fc.clear();
  assert.equal((await fc.usage()).count, 0);
  assert.equal(caches.stores.has("markspace-files-u1"), false);
  assert.equal(fc.isPinned({ kind: "file", id: "x2" }).pinned, false);
  assert.deepEqual(events, [{ fileIds: ["x2"] }]);
  void g;
});

test("concurrent reconcile calls share one run", async () => {
  const { fc, addFile, fetched } = setup();
  await fc.ready;
  addFile({ id: "x1", parentId: "F2", path: "Projects/harbour/a.txt" }, "aaa");
  await fc.pin({ kind: "file", id: "x1" });
  await Promise.all([fc.reconcile(), fc.reconcile(), fc.reconcile()]);
  assert.equal(fetched.filter((u) => u.endsWith("/a.txt") || u.endsWith("/x1")).length, 1);
});
