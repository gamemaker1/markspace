// Runs the service worker template in a vm context with stub globals.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";

import { parseRange as pageParseRange } from "../../internal/web/static/js/filecache.js";

const SOURCE = readFileSync(new URL("../../internal/web/static/sw.js", import.meta.url), "utf8")
  .replace("__MARKSPACE_BUILD__", "b1")
  .replace("__MARKSPACE_ASSETS__", JSON.stringify(["/static/app.css"]));

const ORIGIN = "https://notes.example";

function fakeCaches() {
  const stores = new Map();
  const keyOf = (k) => (typeof k === "string" ? new URL(k, ORIGIN).href : k.url);
  const api = {
    stores,
    async open(name) {
      if (!stores.has(name)) stores.set(name, new Map());
      const m = stores.get(name);
      return {
        async put(key, res) {
          m.delete(keyOf(key));
          m.set(keyOf(key), { body: await res.arrayBuffer(), status: res.status, headers: new Headers(res.headers) });
        },
        async match(key) {
          const hit = m.get(keyOf(key));
          return hit ? new Response(hit.body, { status: hit.status, headers: hit.headers }) : undefined;
        },
        async delete(key) {
          return m.delete(keyOf(key));
        },
        async keys() {
          return [...m.keys()].map((url) => ({ url }));
        },
      };
    },
    async keys() {
      return [...stores.keys()];
    },
    async delete(name) {
      return stores.delete(name);
    },
  };
  return api;
}

function loadWorker({ online = true } = {}) {
  const listeners = new Map();
  const posted = [];
  const client = { id: "c1", postMessage: (m) => posted.push(m) };
  const fetches = [];
  const network = new Map();
  const state = { online };
  const caches = fakeCaches();
  const fetchImpl = async (req) => {
    const url = typeof req === "string" ? req : req.url;
    fetches.push(url);
    if (!state.online) throw new TypeError("offline");
    const make = network.get(new URL(url, ORIGIN).pathname + new URL(url, ORIGIN).search) || network.get(new URL(url, ORIGIN).pathname);
    if (!make) return new Response("net", { status: 200 });
    return make();
  };
  const self = {
    location: new URL(ORIGIN + "/sw.js"),
    navigator: { get onLine() { return state.online; } },
    addEventListener: (type, fn) => listeners.set(type, fn),
    clients: {
      get: async (id) => (id === "c1" ? client : null),
      matchAll: async () => [client],
      claim: async () => {},
    },
    skipWaiting: () => {},
  };
  const context = vm.createContext({
    self, caches, fetch: fetchImpl, Response, Request, Headers, Blob, URL, console,
  });
  vm.runInContext(SOURCE + "\n;globalThis.__sw = { parseRange, rangeResponse, isShellRoute };", context);
  /** Dispatches a fetch event and returns the response, or null when not handled. */
  const dispatch = async (path, { mode = "cors", headers = {}, clientId = "c1" } = {}) => {
    let responded = null;
    const waits = [];
    const request = new Request(ORIGIN + path, { headers });
    Object.defineProperty(request, "mode", { value: mode });
    listeners.get("fetch")({
      request,
      clientId,
      respondWith: (p) => {
        responded = p;
      },
      waitUntil: (p) => waits.push(p),
    });
    const res = responded ? await responded : null;
    await Promise.all(waits);
    return res;
  };
  const message = async (data) => {
    const waits = [];
    listeners.get("message")({ data, waitUntil: (p) => waits.push(p) });
    await Promise.all(waits);
  };
  return { sw: context.__sw, dispatch, message, caches, posted, fetches, network, state, listeners };
}

test("parseRange in the worker matches the page module", () => {
  const { sw } = loadWorker();
  const headers = [null, "", "bytes=0-9", "bytes=90-", "bytes=-10", "bytes=100-", "bytes=-0", "bytes=5-2", "bytes=0-1,4-5", "bytes=10-2000", "nonsense"];
  for (const h of headers) {
    for (const size of [0, 1, 100]) {
      assert.deepEqual(JSON.parse(JSON.stringify(sw.parseRange(h, size))), pageParseRange(h, size), `${h} ${size}`);
    }
  }
});

test("rangeResponse answers 200, 206 slices and 416", async () => {
  const { sw } = loadWorker();
  const blob = new Blob(["0123456789"]);
  const full = sw.rangeResponse(blob, null, { "Content-Type": "text/plain" });
  assert.equal(full.status, 200);
  assert.equal(full.headers.get("Accept-Ranges"), "bytes");
  assert.equal(await full.text(), "0123456789");
  const part = sw.rangeResponse(blob, "bytes=2-5", { "Content-Type": "text/plain" });
  assert.equal(part.status, 206);
  assert.equal(part.headers.get("Content-Range"), "bytes 2-5/10");
  assert.equal(part.headers.get("Content-Length"), "4");
  assert.equal(await part.text(), "2345");
  const tail = sw.rangeResponse(blob, "bytes=-3", {});
  assert.equal(await tail.text(), "789");
  const bad = sw.rangeResponse(blob, "bytes=10-", {});
  assert.equal(bad.status, 416);
  assert.equal(bad.headers.get("Content-Range"), "bytes */10");
});

test("shell routes include folder, file and item permalinks", () => {
  const { sw } = loadWorker();
  for (const p of ["/s/docs/d/Projects/harbour", "/s/docs/f/Projects/report.pdf", "/i/01abc", "/s/docs/n/a", "/s/docs/files"]) {
    assert.equal(sw.isShellRoute(p), true, p);
  }
  for (const p of ["/s/docs/settings", "/files/docs/x/y", "/i/", "/s/docs/d"]) {
    assert.equal(sw.isShellRoute(p), false, p);
  }
});

async function withCachedFile(w, body = "0123456789") {
  await w.message({ type: "filecache-user", userId: "u1" });
  const cache = await w.caches.open("markspace-files-u1");
  await cache.put("/__markspace/files/f1", new Response(body, { headers: { "Content-Type": "text/plain", "X-Markspace-Sha256": "h1" } }));
}

test("a cached file is served with Range support and posts file-accessed", async () => {
  const w = loadWorker();
  await withCachedFile(w);
  const res = await w.dispatch("/files/docs/f1/a.txt", { headers: { Range: "bytes=0-3" } });
  assert.equal(res.status, 206);
  assert.equal(await res.text(), "0123");
  assert.equal(res.headers.get("Content-Type"), "text/plain");
  assert.deepEqual(JSON.parse(JSON.stringify(w.posted)), [{ type: "file-accessed", fileId: "f1" }]);
  assert.equal(w.fetches.length, 0);
  const whole = await w.dispatch("/files/docs/f1/renamed.txt");
  assert.equal(whole.status, 200);
  assert.equal(await whole.text(), "0123456789");
});

test("the user id survives a worker restart", async () => {
  const w = loadWorker();
  await withCachedFile(w);
  // A fresh worker that shares the cache storage reads the stored user id.
  const again = loadWorkerWith(w.caches);
  const res = await again.dispatch("/files/docs/f1/a.txt");
  assert.equal(res.status, 200);
  assert.equal(again.fetches.length, 0);
});

/** Loads a worker that shares an existing cache storage. */
function loadWorkerWith(caches) {
  const w = loadWorker();
  for (const [name, m] of caches.stores) w.caches.stores.set(name, m);
  return w;
}

test("a miss goes to the network and stores nothing, and ?v= bypasses the cache", async () => {
  const w = loadWorker();
  await withCachedFile(w);
  const miss = await w.dispatch("/files/docs/other/b.txt");
  assert.equal(await miss.text(), "net");
  assert.equal(w.caches.stores.get("markspace-files-u1").size, 1);
  const version = await w.dispatch("/files/docs/f1/a.txt?v=ver1");
  assert.equal(await version.text(), "net");
  assert.ok(w.fetches.some((u) => u.includes("?v=ver1")));
  assert.equal(w.posted.length, 0);
});

test("without a known user file requests go to the network", async () => {
  const w = loadWorker();
  const res = await w.dispatch("/files/docs/f1/a.txt");
  assert.equal(await res.text(), "net");
});

test("download prefers the network online and falls back to the cache offline", async () => {
  const w = loadWorker();
  await withCachedFile(w);
  const online = await w.dispatch("/files/docs/f1/a%20b.txt?download=1");
  assert.equal(await online.text(), "net");
  w.state.online = false;
  const offline = await w.dispatch("/files/docs/f1/a%20b.txt?download=1");
  assert.equal(offline.status, 200);
  assert.equal(offline.headers.get("Content-Disposition"), "attachment; filename*=UTF-8''a%20b.txt");
});

test("an offline miss is a network error", async () => {
  const w = loadWorker({ online: false });
  await withCachedFile(w);
  const res = await w.dispatch("/files/docs/nope/x.bin");
  assert.equal(res.type, "error");
});

test("thumbnails are cached first and trimmed to 500 entries", async () => {
  const w = loadWorker();
  // Node Response objects have type "default", so basic responses are faked.
  const basic = () => {
    const r = new Response("img", { status: 200 });
    Object.defineProperty(r, "type", { value: "basic" });
    return r;
  };
  for (let i = 0; i < 503; i++) w.network.set(`/thumbs/docs/f${i}`, basic);
  for (let i = 0; i < 503; i++) await w.dispatch(`/thumbs/docs/f${i}?w=256`);
  const thumbs = w.caches.stores.get("markspace-thumbs-v1");
  assert.equal(thumbs.size, 500);
  assert.ok(!thumbs.has(ORIGIN + "/thumbs/docs/f0?w=256"));
  const before = w.fetches.length;
  await w.dispatch("/thumbs/docs/f502?w=256");
  assert.equal(w.fetches.length, before);
});

test("the pdf.js bundle uses a runtime cache and old caches are deleted on activate", async () => {
  const w = loadWorker();
  await w.dispatch("/static/vendor/pdfjs.js");
  assert.ok(w.caches.stores.get("markspace-pdfjs-b1").size === 1);
  await w.caches.open("markspace-pdfjs-old");
  await w.caches.open("markspace-files-v1");
  await w.caches.open("markspace-static-old");
  await w.caches.open("markspace-files-u1");
  const waits = [];
  w.listeners.get("activate")({ waitUntil: (p) => waits.push(p) });
  await Promise.all(waits);
  const names = [...w.caches.stores.keys()].sort();
  assert.deepEqual(names, ["markspace-files-u1", "markspace-pdfjs-b1"]);
});
