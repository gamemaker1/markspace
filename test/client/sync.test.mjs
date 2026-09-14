import { test } from "node:test";
import assert from "node:assert/strict";

import {
  createSync,
  allocPath,
  conflictTitle,
  conflictFileName,
  copyName,
  freeName,
  relativeDest,
  removeFileLinks,
  replaceFileLinks,
  repathRecords,
  validateName,
} from "../../internal/web/static/js/sync.js";
import { createMemoryStore } from "../../internal/web/static/js/store.js";
import { isId } from "../../internal/web/static/js/ids.js";

class HttpError extends Error {
  constructor(status, message) {
    super(message || `status ${status}`);
    this.status = status;
  }
}

/** Timers that only run when a test fires them. */
function fakeTimers() {
  let seq = 0;
  const pending = new Map();
  return {
    pending,
    setTimeout(fn, ms) {
      pending.set(++seq, { fn, ms });
      return seq;
    },
    clearTimeout(id) {
      pending.delete(id);
    },
    fire(id) {
      const t = pending.get(id);
      pending.delete(id);
      t.fn();
    },
  };
}

/** Lets queued promise callbacks and immediates run. */
async function settle(sync) {
  for (let round = 0; round < 20; round++) {
    for (let i = 0; i < 20; i++) await new Promise((r) => setImmediate(r));
    if (!sync || !sync.status().syncing) return;
  }
}

/** An in-memory Markspace server that answers the REST calls the engine makes. */
function createServer() {
  const s = {
    notes: new Map(),
    folders: new Map(),
    files: new Map(),
    contents: new Map(),
    starred: new Set(),
    rev: 0,
    time: Date.UTC(2026, 8, 13, 9, 0, 0),
    accessKey: "k1",
    spaces: [{ id: "s1", slug: "docs", name: "Docs", myRole: "editor" }],
    user: { id: "u1", name: "Ann", email: "ann@example.com", isAdmin: false },
    failures: [],
    calls: [],
    uploadImpl: null,
    serverIds: 0,
    bumpDescendants: false,
  };
  const stamp = () => new Date((s.time += 1000)).toISOString();
  const dirOf = (p) => (p.includes("/") ? p.slice(0, p.lastIndexOf("/")) : "");
  const join = (d, n) => (d ? `${d}/${n}` : n);
  const parentDir = (parentId) => {
    if (!parentId) return "";
    if (s.folders.has(parentId)) return s.folders.get(parentId).path;
    const n = s.notes.get(parentId);
    return n ? n.path.replace(/\.md$/, "") : "";
  };
  const pathFor = (title, parentId, selfId) => {
    const base = String(title || "").toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "") || "untitled";
    const dir = parentDir(parentId);
    let p = join(dir, `${base}.md`);
    for (let i = 2; [...s.notes.values()].some((n) => n.path === p && n.id !== selfId); i++) p = join(dir, `${base}-${i}.md`);
    return p;
  };
  const all = () => [
    ...[...s.folders.values()].map((r) => ["folder", r]),
    ...[...s.notes.values()].map((r) => ["note", r]),
    ...[...s.files.values()].map((r) => ["file", r]),
  ];
  const taken = (dir, name, exceptId) => all().some(([k, r]) => !r.deleted && r.id !== exceptId && dirOf(r.path) === dir && (k === "note" ? r.path.split("/").pop() : r.name).toLowerCase() === name.toLowerCase());
  const serverId = () => `srv${String(++s.serverIds).padStart(23, "0")}`;
  s.seed = (fields) => {
    const n = {
      spaceId: "s1", spaceSlug: "docs", parentId: "", title: fields.id, icon: "", tags: [], content: "",
      position: 100, createdBy: "u1", updatedBy: "u1", deleted: false, ...fields,
    };
    n.path = fields.path || pathFor(n.title, n.parentId, n.id);
    n.slug = n.path.split("/").pop().replace(/\.md$/, "");
    n.createdAt = n.createdAt || stamp();
    n.updatedAt = stamp();
    n.rev = fields.rev || ++s.rev;
    s.notes.set(n.id, n);
    return n;
  };
  s.seedFolder = (fields) => {
    const f = { spaceId: "s1", parentId: "", position: 0, createdBy: "u1", deleted: false, ...fields };
    f.path = fields.path || join(parentDir(f.parentId), f.name);
    f.createdAt = stamp();
    f.updatedAt = f.createdAt;
    f.rev = fields.rev || ++s.rev;
    s.folders.set(f.id, f);
    return f;
  };
  s.seedFile = (fields, content = "data") => {
    const f = { spaceId: "s1", parentId: "", position: 0, mime: "application/octet-stream", createdBy: "u1", deleted: false, versions: 0, ...fields };
    f.path = fields.path || join(parentDir(f.parentId), f.name);
    f.size = content.length;
    f.sha256 = `sha-${content}`;
    f.createdAt = stamp();
    f.updatedAt = f.createdAt;
    f.url = `http://example.test/files/docs/${f.id}/${encodeURIComponent(f.name)}`;
    f.rev = fields.rev || ++s.rev;
    s.files.set(f.id, f);
    s.contents.set(f.id, content);
    return f;
  };
  s.edit = (id, fields) => {
    const n = { ...s.notes.get(id), ...fields, updatedAt: stamp(), rev: ++s.rev };
    s.notes.set(id, n);
    return n;
  };
  /** Moves an item's path and the paths below it. Only the item gets a new revision unless bumpDescendants is set. */
  s.repath = (rec, newPath) => {
    const old = rec.path;
    for (const [, r] of all()) {
      if (r.id !== rec.id && r.path.startsWith(old + "/")) {
        r.path = newPath + r.path.slice(old.length);
        if (s.bumpDescendants) r.rev = ++s.rev;
      }
    }
    rec.path = newPath;
    rec.rev = ++s.rev;
    rec.updatedAt = stamp();
  };
  const out = (n) => ({ ...structuredClone(n), content: n.deleted ? "" : n.content });
  const itemOut = (id) => {
    if (s.notes.has(id)) return { kind: "note", note: out(s.notes.get(id)), access: "edit" };
    if (s.folders.has(id)) return { kind: "folder", folder: structuredClone(s.folders.get(id)), access: "edit" };
    if (s.files.has(id)) return { kind: "file", file: structuredClone(s.files.get(id)), access: "edit" };
    return null;
  };
  const recOf = (id) => s.notes.get(id) || s.folders.get(id) || s.files.get(id) || null;
  const below = (rec) => all().filter(([, r]) => r.path.startsWith(rec.path + "/")).map(([, r]) => r);
  const takeFailure = (method, path) => {
    const i = s.failures.findIndex((f) => f.method === method && f.path.test(path));
    if (i < 0) return null;
    const f = s.failures[i];
    if (!f.sticky) s.failures.splice(i, 1);
    return f;
  };
  async function route(method, path, body) {
    const url = new URL(path, "http://markspace.test");
    const p = url.pathname;
    if (method === "GET" && p === "/api/sync") {
      const since = Number(url.searchParams.get("since"));
      const limit = Number(url.searchParams.get("limit")) || 200;
      const changed = all().filter(([, r]) => r.rev > since).sort((a, b) => a[1].rev - b[1].rev);
      const page = changed.slice(0, limit);
      const more = changed.length > limit;
      return {
        rev: more ? page[page.length - 1][1].rev : s.rev,
        more,
        serverTime: new Date(s.time).toISOString(),
        user: s.user,
        spaces: structuredClone(s.spaces),
        accessKey: s.accessKey,
        notes: page.filter(([k]) => k === "note").map(([, n]) => ({ ...out(n), access: "edit" })),
        folders: page.filter(([k]) => k === "folder").map(([, f]) => structuredClone(f)),
        files: page.filter(([k]) => k === "file").map(([, f]) => structuredClone(f)),
        liveIds: more ? null : all().map(([, r]) => r.id),
        starredIds: more ? null : [...s.starred],
      };
    }
    if (method === "POST" && p === "/api/notes") {
      if (body.id && s.notes.has(body.id)) {
        const existing = s.notes.get(body.id);
        if (existing.createdBy === s.user.id && existing.spaceId === body.spaceId) return out(existing);
        throw new HttpError(409, "id in use");
      }
      return out(s.seed({
        id: body.id, spaceId: body.spaceId, parentId: body.parentId || "", title: body.title,
        content: body.content || "", tags: body.tags || [], icon: body.icon || "",
      }));
    }
    if (method === "POST" && p === "/api/folders") {
      if (body.id && s.folders.has(body.id)) return structuredClone(s.folders.get(body.id));
      const dir = parentDir(body.parentId);
      if (body.parentId && !s.folders.has(body.parentId)) throw new HttpError(404, "parent not found");
      if (taken(dir, body.name)) throw new HttpError(409, "name taken");
      return structuredClone(s.seedFolder({ id: body.id || serverId(), spaceId: body.spaceId, parentId: body.parentId || "", name: body.name }));
    }
    let m = /^\/api\/(folders|files)\/([^/]+)$/.exec(p);
    if (m && method === "PATCH") {
      const map = m[1] === "folders" ? s.folders : s.files;
      const rec = map.get(decodeURIComponent(m[2]));
      if (!rec || rec.deleted) throw new HttpError(404, "not found");
      if (taken(dirOf(rec.path), body.name, rec.id)) throw new HttpError(409, "name taken");
      rec.name = body.name;
      s.repath(rec, join(dirOf(rec.path), body.name));
      return structuredClone(rec);
    }
    m = /^\/api\/items\/([^/]+)(?:\/(move|copy|trash|restore))?$/.exec(p);
    if (m) {
      const id = decodeURIComponent(m[1]);
      const rec = recOf(id);
      if (!rec) throw new HttpError(404, "not found");
      switch (`${method} ${m[2] || ""}`) {
        case "GET ":
          return itemOut(id);
        case "POST move": {
          const dir = parentDir(body.parentId);
          if (body.parentId && !s.folders.has(body.parentId)) throw new HttpError(404, "parent not found");
          rec.parentId = body.parentId || "";
          rec.position = body.position || 0;
          s.repath(rec, join(dir, rec.path.split("/").pop()));
          return itemOut(id);
        }
        case "POST copy": {
          const dir = parentDir(body.parentId);
          const kind = itemOut(id).kind;
          const copyRec = (r, k, parentId, path, name) => {
            const c = { ...structuredClone(r), id: serverId(), parentId, path, rev: ++s.rev };
            if (name !== undefined) c.name = name;
            if (k === "note") s.notes.set(c.id, c);
            else if (k === "folder") s.folders.set(c.id, c);
            else {
              c.versions = 0;
              s.files.set(c.id, c);
              s.contents.set(c.id, s.contents.get(r.id));
            }
            return c;
          };
          let name = body.name || rec.name;
          if (kind !== "note" && !body.name && taken(dir, name)) name = name.replace(/(\.[^.]+)?$/, (ext) => ` copy${ext || ""}`);
          const root = copyRec(rec, kind, body.parentId || "", join(dir, kind === "note" ? rec.path.split("/").pop() : name), kind === "note" ? undefined : name);
          if (kind === "folder") {
            const ids = new Map([[rec.id, root.id]]);
            for (const [k, r] of all().filter(([, x]) => x.path.startsWith(rec.path + "/")).sort((a, b) => a[1].path.length - b[1].path.length)) {
              const c = copyRec(r, k, ids.get(r.parentId) || root.id, root.path + r.path.slice(rec.path.length));
              ids.set(r.id, c.id);
            }
          }
          return itemOut(root.id);
        }
        case "POST trash":
          for (const r of [rec, ...below(rec)]) {
            r.deleted = true;
            r.rev = ++s.rev;
          }
          return null;
        case "POST restore":
          for (const r of [rec, ...below(rec)]) {
            r.deleted = false;
            r.rev = ++s.rev;
          }
          return itemOut(id);
        case "DELETE ":
          for (const r of [rec, ...below(rec)]) {
            s.notes.delete(r.id);
            s.folders.delete(r.id);
            s.files.delete(r.id);
          }
          s.rev++;
          return null;
      }
      throw new HttpError(404, "no route");
    }
    m = /^\/api\/notes\/([^/]+)(?:\/(move|trash|restore|star))?$/.exec(p);
    if (!m) throw new HttpError(404, "no route");
    const id = decodeURIComponent(m[1]);
    const n = s.notes.get(id);
    if (!n) throw new HttpError(404, "not found");
    switch (`${method} ${m[2] || ""}`) {
      case "GET ":
        return { ...out(n), access: { access: "edit", source: "space" } };
      case "PATCH ": {
        if (n.deleted) throw new HttpError(404, "not found");
        const { baseUpdatedAt, ...fields } = body;
        if (baseUpdatedAt && Date.parse(baseUpdatedAt) < Date.parse(n.updatedAt)) throw new HttpError(409, "conflict");
        if (fields.title !== undefined && fields.title !== n.title) fields.path = pathFor(fields.title, n.parentId, id);
        return out(s.edit(id, fields));
      }
      case "POST move":
        return out(s.edit(id, { parentId: body.parentId || "", position: body.position, path: pathFor(n.title, body.parentId, id) }));
      case "POST trash":
        s.edit(id, { deleted: true });
        return null;
      case "POST restore":
        s.edit(id, { deleted: false });
        return null;
      case "DELETE ":
        s.notes.delete(id);
        s.rev++;
        return null;
      case "PUT star":
        s.starred.add(id);
        return null;
      case "DELETE star":
        s.starred.delete(id);
        return null;
    }
    throw new HttpError(404, "no route");
  }
  s.api = async (method, path, body) => {
    s.calls.push({ method, path, body: body === undefined ? undefined : structuredClone(body) });
    const f = takeFailure(method, path);
    if (f && !f.afterApply) throw new HttpError(f.status, f.message);
    const result = await route(method, path, body);
    if (f) throw new HttpError(f.status, f.message);
    return result;
  };
  /** Answers POST /api/files and PUT /api/files/{id}/content like the real routes, with the text fields first. */
  async function uploadRoute(path, file, { fields = {}, method = "POST", filename } = {}) {
    const content = await file.text();
    const name = filename || file.name || "file";
    if (method === "POST" && path === "/api/files") {
      if (fields.id && s.files.has(fields.id)) return structuredClone(s.files.get(fields.id));
      let parentId = fields.parentId || "";
      if (fields.pasteFor) {
        const note = s.notes.get(fields.pasteFor);
        if (!note) throw new HttpError(404, "note not found");
        parentId = note.parentId && s.folders.has(note.parentId) ? note.parentId : "";
      }
      const dir = parentDir(parentId);
      let finalName = name;
      if (taken(dir, finalName)) {
        if (fields.onConflict !== "rename") throw new HttpError(409, "exists");
        const dot = name.lastIndexOf(".");
        const [stem, ext] = dot > 0 ? [name.slice(0, dot), name.slice(dot)] : [name, ""];
        for (let n = 2; taken(dir, finalName); n++) finalName = `${stem} (${n})${ext}`;
      }
      return structuredClone(s.seedFile({ id: fields.id || serverId(), spaceId: fields.spaceId, parentId, name: finalName, mime: file.type || "application/octet-stream" }, content));
    }
    const m = /^\/api\/files\/([^/]+)\/content$/.exec(path);
    if (m && method === "PUT") {
      const rec = s.files.get(decodeURIComponent(m[1]));
      if (!rec) throw new HttpError(404, "not found");
      if (fields.baseSha256 !== rec.sha256) throw new HttpError(409, "the file changed");
      rec.size = content.length;
      rec.sha256 = `sha-${content}`;
      rec.versions++;
      rec.rev = ++s.rev;
      rec.updatedAt = stamp();
      s.contents.set(rec.id, content);
      return structuredClone(rec);
    }
    throw new HttpError(404, "no upload route");
  }
  s.upload = async (path, file, opts = {}) => {
    s.calls.push({ method: opts.method === "PUT" ? "UPLOAD-PUT" : "UPLOAD", path, fields: structuredClone(opts.fields || {}), fieldOrder: Object.keys(opts.fields || {}) });
    const f = takeFailure("UPLOAD", path);
    if (f && !f.afterApply) throw new HttpError(f.status, f.message);
    let result;
    if (s.uploadImpl) result = await s.uploadImpl(path, file, opts, uploadRoute);
    else {
      if (opts.onProgress) opts.onProgress(file.size, file.size);
      result = await uploadRoute(path, file, opts);
    }
    if (f) throw new HttpError(f.status, f.message);
    return result;
  };
  return s;
}

const EVENTS = ["status", "notes-changed", "spaces-changed", "conflict", "rejected", "recovered", "auth-required", "uploads-changed", "copied", "file-stored"];

async function setup({ seed = [], online = true } = {}) {
  const server = createServer();
  for (const n of seed) server.seed(n);
  const store = createMemoryStore();
  const net = { online };
  const clock = { t: Date.UTC(2026, 8, 13, 10, 0, 0) };
  const timers = fakeTimers();
  const events = [];
  const make = () => {
    const sync = createSync({
      store,
      api: server.api,
      upload: server.upload,
      now: () => clock.t,
      online: () => net.online,
      timers,
      visible: () => true,
      globalTarget: null,
      documentTarget: null,
    });
    for (const e of EVENTS) sync.on(e, (p) => events.push({ e, p }));
    return sync;
  };
  const sync = make();
  // Every client pulls once online before it can work offline.
  net.online = true;
  await sync.syncNow();
  net.online = online;
  server.calls.length = 0;
  events.length = 0;
  const of = (e) => events.filter((x) => x.e === e).map((x) => x.p);
  const calls = () => server.calls.map((c) => `${c.method} ${c.path}`);
  return { server, store, sync, net, clock, timers, events, of, calls, make };
}

test("allocPath matches the server's allocation rules", () => {
  const notes = [{ path: "hello.md" }, { path: "hello-2.md" }, { path: "hello/kid.md" }, { path: "gone.md", deleted: true }];
  assert.equal(allocPath(notes, "", "Hello"), "hello-3.md");
  assert.equal(allocPath(notes, "", "Hello", "hello.md"), "hello.md");
  assert.equal(allocPath(notes, "hello.md", "Kid"), "hello/kid-2.md");
  assert.equal(allocPath(notes, "", "Gone"), "gone.md");
  assert.equal(allocPath(notes, "a/b.md", "CON"), "a/b/con-note.md");
  assert.equal(allocPath([], "", ""), "untitled.md");
});

test("mutations write to the store and resolve before any network call", async () => {
  const t = await setup({ seed: [{ id: "hello", title: "Hello" }] });
  const note = await t.sync.createNote({ spaceId: "s1", title: "Hello", content: "hi" });
  assert.equal(t.server.calls.length, 0);
  assert.ok(isId(note.id));
  assert.equal(note.path, "hello-2.md");
  assert.equal(note.provisional, true);
  assert.equal((await t.store.getNote(note.id)).content, "hi");
  const second = await t.sync.createNote({ spaceId: "s1", title: "Hello" });
  assert.equal(second.path, "hello-3.md");
  const kid = await t.sync.createNote({ spaceId: "s1", parentId: "hello", title: "Kid Page" });
  assert.equal(kid.path, "hello/kid-page.md");
  assert.equal((await t.sync.createNote({ spaceId: "s1", title: "index" })).path, "index-note.md");
  await t.sync.updateNote("hello", { content: "edited" });
  await t.sync.setStar("hello", true);
  await t.sync.trashNote("hello");
  assert.equal(t.server.calls.length, 0);
  assert.equal((await t.store.getNote("hello")).deleted, true);
  await settle();
  assert.ok(t.of("notes-changed").some((p) => p.source === "local" && p.ids.includes(note.id)));
  assert.equal(t.sync.status().pending, await t.store.countOps());
});

test("ops coalesce before they are sent", async () => {
  const t = await setup({ seed: [{ id: "doc", title: "Doc", content: "v1" }], online: false });
  const n = await t.sync.createNote({ spaceId: "s1", title: "Draft" });
  await t.sync.updateNote(n.id, { content: "one" });
  await t.sync.updateNote(n.id, { title: "Draft 2", content: "two" });
  let ops = await t.store.peekOps();
  assert.equal(ops.length, 1);
  assert.equal(ops[0].type, "create");
  assert.deepEqual([ops[0].body.title, ops[0].body.content], ["Draft 2", "two"]);

  const base = (await t.store.getNote("doc")).serverUpdatedAt;
  await t.sync.updateNote("doc", { content: "a" });
  await t.sync.updateNote("doc", { title: "Doc!" });
  await t.sync.setStar("doc", true);
  await t.sync.setStar("doc", false);
  ops = await t.store.peekOps();
  const docOps = ops.filter((o) => o.noteId === "doc");
  assert.deepEqual(docOps.map((o) => o.type), ["update", "star"]);
  assert.deepEqual(docOps[0].fields, { content: "a", title: "Doc!" });
  assert.equal(docOps[0].baseUpdatedAt, base, "the merged update keeps the first baseUpdatedAt");
  assert.equal(docOps[1].starred, false);

  await t.sync.updateNote(n.id, { content: "three" });
  await t.sync.setStar(n.id, true);
  await t.sync.deleteNote(n.id);
  ops = await t.store.peekOps();
  assert.equal(ops.filter((o) => o.noteId === n.id).length, 0, "create then delete removes both");
  assert.equal(await t.store.getNote(n.id), null);

  t.net.online = true;
  await t.sync.syncNow();
  assert.equal(t.calls().filter((c) => c.startsWith("POST /api/notes")).length, 0);
  assert.equal(t.server.notes.get("doc").title, "Doc!");
});

test("an update typed while its create is in flight is sent after, with a rebased base", async () => {
  const t = await setup({ online: false });
  const n = await t.sync.createNote({ spaceId: "s1", title: "Race", content: "one" });
  t.net.online = true;
  const realApi = t.server.api;
  let edited = false;
  t.server.api = async (method, path, body) => {
    if (method === "POST" && path === "/api/notes" && !edited) {
      edited = true;
      await t.sync.updateNote(n.id, { content: "two" });
    }
    return realApi(method, path, body);
  };
  const sync = t.make();
  t.server.api = realApi;
  await sync.syncNow();
  // The first engine instance holds the stale api; use the second.
  assert.equal(t.server.notes.get(n.id).content, "two");
  assert.equal(await t.store.countOps(), 0);
  assert.equal(t.of("conflict").length, 0);
});

test("offline create replays online and an idempotent re-create is safe", async () => {
  const t = await setup({ online: false });
  const n = await t.sync.createNote({ spaceId: "s1", title: "Offline note", content: "written on the train" });
  await t.sync.syncNow();
  assert.equal(t.server.calls.length, 0, "no calls while offline");

  t.net.online = true;
  t.server.failures.push({ method: "POST", path: /^\/api\/notes$/, status: 0, afterApply: true });
  await t.sync.syncNow();
  assert.equal(t.server.notes.has(n.id), true, "the server applied the create");
  assert.equal(await t.store.countOps(), 1, "a lost reply keeps the op");
  assert.ok(await t.store.getNote(n.id), "the pull cannot drop a note with a queued create");
  await t.sync.syncNow();
  assert.equal(await t.store.countOps(), 0, "the replayed create returns the existing note");
  assert.equal([...t.server.notes.values()].filter((x) => x.title === "Offline note").length, 1);
  assert.equal((await t.store.getNote(n.id)).content, "written on the train");

  const t2 = await setup({ online: false });
  const m = await t2.sync.createNote({ spaceId: "s1", title: "Lost reply", content: "x" });
  t2.net.online = true;
  t2.server.failures.push({ method: "POST", path: /^\/api\/notes$/, status: 0, afterApply: true });
  t2.server.failures.push({ method: "GET", path: /^\/api\/sync/, status: 503 });
  await t2.sync.syncNow();
  assert.equal(await t2.store.countOps(), 1, "a lost reply keeps the op");
  await t2.sync.syncNow();
  assert.equal(await t2.store.countOps(), 0);
  assert.equal([...t2.server.notes.values()].filter((x) => x.title === "Lost reply").length, 1);
  const local = await t2.store.getNote(m.id);
  assert.equal(local.provisional, false);
  assert.equal(local.path, "lost-reply.md");
  assert.equal(t2.calls().filter((c) => c === "POST /api/notes").length, 2);
});

test("a 2xx stores the returned note", async () => {
  const t = await setup({ seed: [{ id: "doc", title: "Doc", content: "v1" }] });
  await t.sync.updateNote("doc", { content: "v2" });
  await t.sync.syncNow();
  const local = await t.store.getNote("doc");
  const server = t.server.notes.get("doc");
  assert.equal(local.content, "v2");
  assert.equal(local.rev, server.rev);
  assert.equal(local.serverUpdatedAt, server.updatedAt);
  assert.equal(await t.store.countOps(), 0);
  const patch = t.server.calls.find((c) => c.method === "PATCH");
  assert.deepEqual(Object.keys(patch.body).sort(), ["baseUpdatedAt", "content"]);
});

test("a pull never overwrites a note with queued ops, then a 409 makes a conflict copy", async () => {
  const t = await setup({ seed: [{ id: "p", title: "Parent" }, { id: "doc", title: "Plan", parentId: "p", content: "v1" }] });
  await t.sync.updateNote("doc", { content: "local edit" });
  t.server.edit("doc", { content: "server edit" });
  t.server.failures.push({ method: "PATCH", path: /doc/, status: 503, sticky: true });
  await t.sync.syncNow();
  assert.equal((await t.store.getNote("doc")).content, "local edit");
  assert.equal((await t.store.getMeta("server:doc")).content, "server edit");

  t.server.failures.length = 0;
  await t.sync.syncNow();
  const original = await t.store.getNote("doc");
  assert.equal(original.content, "server edit");
  const [conflict] = t.of("conflict");
  assert.equal(conflict.noteId, "doc");
  assert.ok(isId(conflict.copyId));
  const copy = await t.store.getNote(conflict.copyId);
  assert.equal(copy.content, "local edit");
  assert.equal(copy.parentId, "p");
  assert.equal(copy.spaceId, "s1");
  assert.match(copy.title, /^Plan \(conflicted copy \d{4}-\d{2}-\d{2} \d{2}:\d{2}\)$/);
  assert.equal(t.server.notes.get(conflict.copyId).content, "local edit", "the copy reached the server");
  assert.equal(t.sync.status().conflicts, 1);
  assert.equal(await t.store.countOps(), 0);
  assert.equal(await t.store.getMeta("server:doc"), undefined);
});

test("a 409 whose values the server already holds removes the op", async () => {
  const t = await setup({ seed: [{ id: "doc", title: "Doc", content: "v1" }] });
  await t.sync.updateNote("doc", { content: "mine", tags: ["a"] });
  t.server.failures.push({ method: "PATCH", path: /doc/, status: 0, afterApply: true });
  await t.sync.syncNow();
  await t.sync.syncNow();
  assert.ok(t.calls().filter((c) => c.startsWith("PATCH")).length >= 2);
  assert.ok(t.calls().includes("GET /api/notes/doc"), "the 409 check reads the note");
  assert.equal(await t.store.countOps(), 0);
  assert.equal(t.of("conflict").length, 0);
  assert.equal([...t.server.notes.values()].length, 1);
  assert.equal((await t.store.getNote("doc")).content, "mine");
});

test("a 401 pauses replay, keeps every op and asks for sign-in", async () => {
  const t = await setup({ seed: [{ id: "doc", title: "Doc" }] });
  await t.sync.updateNote("doc", { content: "x" });
  await t.sync.setStar("doc", true);
  t.server.failures.push({ method: "PATCH", path: /doc/, status: 401 });
  t.server.failures.push({ method: "GET", path: /^\/api\/sync/, status: 401 });
  await t.sync.syncNow();
  assert.equal(t.of("auth-required").length, 1);
  assert.equal(t.sync.status().authRequired, true);
  assert.equal(await t.store.countOps(), 2);
  assert.equal(t.calls().filter((c) => c.startsWith("PUT")).length, 0, "replay stopped at the 401");
  await t.sync.syncNow();
  assert.equal(t.sync.status().authRequired, false);
  assert.equal(await t.store.countOps(), 0);
  assert.equal(t.server.starred.has("doc"), true);
});

test("a 403 on update restores the server copy, emits rejected and keeps the text", async () => {
  const t = await setup({ seed: [{ id: "doc", title: "Doc", content: "v1" }] });
  await t.sync.updateNote("doc", { content: "forbidden edit" });
  t.server.failures.push({ method: "PATCH", path: /doc/, status: 403, message: "read only" });
  await t.sync.syncNow();
  assert.equal((await t.store.getNote("doc")).content, "v1");
  assert.equal(await t.store.countOps(), 0);
  const [rejected] = t.of("rejected");
  assert.equal(rejected.status, 403);
  assert.equal(rejected.message, "read only");
  assert.equal(rejected.op.type, "update");
  const [saved] = await t.store.listRecovered();
  assert.equal(saved.content, "forbidden edit");
  assert.deepEqual(t.of("recovered"), [{ id: saved.id, title: "Doc" }]);
});

test("a 400 on move restores the server copy", async () => {
  const t = await setup({ seed: [{ id: "p", title: "Parent" }, { id: "doc", title: "Doc" }] });
  const moved = await t.sync.moveNote("doc", "p", 50);
  assert.equal(moved.path, "parent/doc.md");
  t.server.failures.push({ method: "POST", path: /move/, status: 400 });
  await t.sync.syncNow();
  const local = await t.store.getNote("doc");
  assert.equal(local.parentId, "");
  assert.equal(local.path, "doc.md");
  assert.equal(t.of("rejected")[0].status, 400);
  assert.equal(await t.store.countOps(), 0);
});

test("a 404 on update keeps the local text as a conflict copy at the space root", async () => {
  const t = await setup({ seed: [{ id: "p", title: "Parent" }, { id: "doc", title: "Doc", parentId: "p", content: "v1" }] });
  await t.sync.updateNote("doc", { content: "orphaned edit" });
  t.server.notes.delete("doc");
  await t.sync.syncNow();
  assert.equal(await t.store.getNote("doc"), null);
  const [conflict] = t.of("conflict");
  const copy = t.server.notes.get(conflict.copyId);
  assert.equal(copy.parentId, "");
  assert.equal(copy.content, "orphaned edit");
  assert.equal((await t.store.getNote(conflict.copyId)).content, "orphaned edit");
  assert.equal(await t.store.countOps(), 0);
});

test("a 404 whose copy is refused saves the text in recovered", async () => {
  const t = await setup({ seed: [{ id: "doc", title: "Doc", content: "v1" }] });
  await t.sync.trashNote("doc");
  await t.sync.updateNote("doc", { content: "last words" });
  t.server.notes.delete("doc");
  t.server.failures.push({ method: "POST", path: /^\/api\/notes$/, status: 403 });
  await t.sync.syncNow();
  assert.equal(t.of("conflict").length, 0);
  const [saved] = await t.store.listRecovered();
  assert.equal(saved.content, "last words");
  assert.deepEqual(t.of("recovered"), [{ id: saved.id, title: "Doc" }]);
  assert.equal(await t.store.countOps(), 0);
});

test("a refused create moves its text to recovered", async () => {
  const t = await setup({ online: false });
  const n = await t.sync.createNote({ spaceId: "s1", title: "Nope", content: "keep me" });
  t.net.online = true;
  t.server.failures.push({ method: "POST", path: /^\/api\/notes$/, status: 400, message: "bad title" });
  await t.sync.syncNow();
  assert.equal(await t.store.getNote(n.id), null);
  assert.equal((await t.store.listRecovered())[0].content, "keep me");
  assert.equal(t.of("rejected")[0].status, 400);
});

test("trash, restore, star and delete replay in order; a 404 on delete is done", async () => {
  const t = await setup({ seed: [{ id: "doc", title: "Doc" }, { id: "gone", title: "Gone" }] });
  await t.sync.trashNote("doc");
  await t.sync.restoreNote("doc");
  await t.sync.setStar("doc", true);
  await t.sync.deleteNote("gone");
  t.server.notes.delete("gone");
  await t.sync.syncNow();
  assert.deepEqual(t.calls().filter((c) => !c.startsWith("GET")), [
    "POST /api/notes/doc/trash",
    "POST /api/notes/doc/restore",
    "PUT /api/notes/doc/star",
    "DELETE /api/notes/gone",
  ]);
  assert.equal(await t.store.countOps(), 0);
  assert.equal(t.of("rejected").length, 0);
  assert.equal((await t.store.getNote("doc")).starred, true);
});

test("5xx and network failures retry with backoff from 2 s doubling to 60 s", async () => {
  const t = await setup({ seed: [{ id: "doc", title: "Doc" }] });
  await t.sync.updateNote("doc", { content: "x" });
  t.server.failures.push({ method: "PATCH", path: /doc/, status: 503, sticky: true });
  await t.sync.start();
  const pullTimer = Math.min(...t.timers.pending.keys());
  const delays = [];
  for (let i = 0; i < 7; i++) {
    const ids = [...t.timers.pending.keys()].filter((id) => id !== pullTimer);
    assert.equal(ids.length, 1, "one retry timer is pending");
    const id = ids[0];
    delays.push(t.timers.pending.get(id).ms);
    if (i === 3) t.server.failures[0].status = 0;
    t.timers.fire(id);
    await settle(t.sync);
  }
  assert.deepEqual(delays, [2000, 4000, 8000, 16000, 32000, 60000, 60000]);
  assert.equal(await t.store.countOps(), 1);
  assert.ok(t.sync.status().lastError);
  t.server.failures.length = 0;
  const id = [...t.timers.pending.keys()].find((k) => k !== pullTimer);
  t.timers.fire(id);
  await settle(t.sync);
  assert.equal(await t.store.countOps(), 0);
  assert.equal(t.sync.status().lastError, null);
  t.sync.stop();
  assert.equal(t.timers.pending.size, 0);
});

test("local mutations replay after a 1 s debounce once started", async () => {
  const t = await setup({ seed: [{ id: "doc", title: "Doc" }] });
  await t.sync.start();
  await t.sync.updateNote("doc", { content: "a" });
  await t.sync.updateNote("doc", { content: "ab" });
  const debounce = [...t.timers.pending.entries()].filter(([, v]) => v.ms === 1000);
  assert.equal(debounce.length, 1);
  assert.equal(t.calls().filter((c) => c.startsWith("PATCH")).length, 0);
  t.timers.fire(debounce[0][0]);
  await settle(t.sync);
  assert.equal(t.server.notes.get("doc").content, "ab");
  t.sync.stop();
});

test("the last page removes notes missing from liveIds and sets stars", async () => {
  const t = await setup({ seed: [{ id: "keep", title: "Keep" }, { id: "gone", title: "Gone" }, { id: "busy", title: "Busy" }, { id: "fav", title: "Fav" }] });
  await t.sync.updateNote("busy", { content: "unsent" });
  await t.sync.setStar("fav", false);
  t.server.failures.push({ method: "PATCH", path: /busy/, status: 503, sticky: true });
  t.server.notes.delete("gone");
  t.server.notes.delete("busy");
  t.server.starred.add("keep");
  t.server.starred.add("fav");
  await t.sync.syncNow();
  assert.equal(await t.store.getNote("gone"), null);
  assert.equal((await t.store.getNote("busy")).content, "unsent", "a note with queued ops stays");
  assert.equal((await t.store.getNote("keep")).starred, true);
  assert.equal((await t.store.getNote("fav")).starred, false, "a queued star wins");
  assert.ok(t.of("notes-changed").some((p) => p.source === "remote" && p.ids.includes("gone")));
});

test("paging pulls until more is false", async () => {
  const seed = Array.from({ length: 450 }, (_, i) => ({ id: `n${i}`, title: `Note ${i}` }));
  const t = await setup({ seed: [] });
  for (const n of seed) t.server.seed(n);
  await t.sync.syncNow();
  const pulls = t.calls().filter((c) => c.startsWith("GET /api/sync"));
  assert.equal(pulls.length, 3);
  assert.equal((await t.store.listAllNotes()).length, 450);
  assert.equal(await t.store.getMeta("rev"), t.server.rev);
});

test("a changed accessKey resets the cursor and pulls a full snapshot", async () => {
  const t = await setup({ seed: [{ id: "mine", title: "Mine" }, { id: "revoked", title: "Revoked" }] });
  const cursor = await t.store.getMeta("rev");
  await t.sync.updateNote("mine", { content: "queued" });
  t.server.failures.push({ method: "PATCH", path: /mine/, status: 503, sticky: true });
  t.server.accessKey = "k2";
  t.server.seed({ id: "shared", title: "Shared", rev: 1 });
  t.server.notes.delete("revoked");
  await t.sync.syncNow();
  const pulls = t.calls().filter((c) => c.startsWith("GET /api/sync"));
  assert.deepEqual(pulls, [`GET /api/sync?since=${cursor}&limit=200`, "GET /api/sync?since=0&limit=200"]);
  assert.ok(await t.store.getNote("shared"), "the old-revision share arrived");
  assert.equal(await t.store.getNote("revoked"), null);
  assert.equal(await t.store.getMeta("accessKey"), "k2");
  assert.equal(await t.store.countOps(), 1, "the outbox is untouched");

  t.server.calls.length = 0;
  await t.sync.syncNow();
  assert.equal(t.calls().filter((c) => c.startsWith("GET /api/sync")).length, 1, "an unchanged key pulls once");
});

test("a failing sync endpoint is reported without throwing", async () => {
  const t = await setup({ seed: [{ id: "doc", title: "Doc" }] });
  t.server.failures.push({ method: "GET", path: /^\/api\/sync/, status: 404, message: "not implemented" });
  await t.sync.updateNote("doc", { content: "still pushes" });
  await t.sync.syncNow();
  assert.equal(t.sync.status().lastError, "not implemented");
  assert.equal(t.server.notes.get("doc").content, "still pushes");
});

test("spaces-changed fires on a space change and on() unsubscribes", async () => {
  const t = await setup();
  let calls = 0;
  const off = t.sync.on("spaces-changed", () => calls++);
  t.server.spaces.push({ id: "s2", slug: "two", name: "Two", myRole: "owner" });
  await t.sync.syncNow();
  assert.equal(calls, 1);
  await t.sync.syncNow();
  assert.equal(calls, 1, "no event without a change");
  off();
  t.server.spaces.pop();
  await t.sync.syncNow();
  assert.equal(calls, 1);
  assert.equal((await t.store.listSpaces()).length, 1);
});


// ---------------------------------------------------------------------------
// Folders and files

test("file name helpers follow the server rules", () => {
  assert.match(conflictFileName("east wall.jpg", Date.UTC(2026, 0, 2, 3, 4)), /^east wall \(conflicted copy \d{4}-\d{2}-\d{2} \d{2}:\d{2}\)\.jpg$/);
  assert.match(conflictFileName("Makefile", 0), /^Makefile \(conflicted copy .*\)$/);
  assert.equal(freeName(["a.pdf", "A (2).pdf"], "A.pdf"), "A (3).pdf");
  assert.equal(freeName(["x"], "y"), "y");
  assert.equal(freeName(["v1.2"], "v1.2", true), "v1.2 (2)", "folder names keep their dots");
  assert.equal(copyName(["r.pdf"], "r.pdf"), "r copy.pdf");
  assert.equal(copyName(["r.pdf", "r copy.pdf"], "r.pdf"), "r copy 2.pdf");
  assert.equal(copyName([], "r.pdf"), "r.pdf");
  const control = "a" + String.fromCharCode(1) + "b";
  for (const bad of ["", ".", "..", ".hidden", " lead", "trail ", "a/b", "a\\b", control, "~$lock.docx", "Thumbs.db", "a.tmp-1", "y".repeat(256)]) {
    assert.ok(validateName(bad), `refuses ${JSON.stringify(bad)}`);
  }
  assert.equal(validateName("notes.md"), "");
  assert.ok(validateName("notes.md", { kind: "file" }), "a file cannot end in .md");
  assert.equal(validateName("Café (1).pdf"), "");
  assert.equal(relativeDest("Projects/harbour", "Projects/east wall.jpg"), "../east%20wall.jpg");
  assert.equal(relativeDest("", "attachments/shot.png"), "attachments/shot.png");
  assert.equal(relativeDest("a", "a/ab:d.png"), "./ab:d.png");
  assert.equal(relativeDest("a", "a/c:d.png"), "c:d.png", "a one-letter prefix is not a scheme");
  assert.equal(relativeDest("", "shot (2).png"), "shot%20(2).png");
  assert.equal(relativeDest("", "a(b.png"), "a%28b.png");
  assert.equal(removeFileLinks("x ![s](<a b.png>) y [s](../d/a%20b.png) [[a b.png]] `[c](a%20b.png)`", "d", "d/a b.png", "a b.png"), "x a b.png y a b.png [[a b.png]] `[c](a%20b.png)`");
  assert.equal(
    replaceFileLinks("![](shot.png) [x](shot.png \"t\") [y](<shot.png>) shot.png ![](shot%28x%29.png)", "", "shot.png", "shot (2).png"),
    "![](shot%20(2).png) [x](shot%20(2).png \"t\") [y](<shot%20(2).png>) shot.png ![](shot%28x%29.png)",
  );
  const recs = [{ path: "A", name: "A" }, { path: "A/b.md" }, { path: "AB/c" }, { path: "A/d/e.pdf" }];
  assert.equal(repathRecords(recs, "A", "Z").length, 3);
  assert.deepEqual(recs.map((r) => r.path), ["Z", "Z/b.md", "AB/c", "Z/d/e.pdf"]);
  assert.equal(recs[0].name, "Z");
});

/** A client with a Docs folder, an img subfolder, a PDF and a note in Docs, synced once. */
async function setupItems({ online = true } = {}) {
  const t = await setup({ online: true });
  t.server.seedFolder({ id: "f-docs", name: "Docs" });
  t.server.seedFolder({ id: "f-img", name: "img", parentId: "f-docs" });
  t.server.seedFile({ id: "x-pdf", name: "report.pdf", parentId: "f-docs", mime: "application/pdf" }, "v1");
  t.server.seed({ id: "readme", title: "Readme", parentId: "f-docs", content: "See [r](report.pdf)" });
  await t.sync.syncNow();
  t.net.online = online;
  t.server.calls.length = 0;
  t.events.length = 0;
  return t;
}

test("a pull stores folders and files and liveIds removes every kind", async () => {
  const t = await setupItems();
  assert.equal((await t.store.getFolder("f-img")).path, "Docs/img");
  const pdf = await t.store.getFile("x-pdf");
  assert.equal(pdf.path, "Docs/report.pdf");
  assert.equal(pdf.url, "/files/docs/x-pdf/report.pdf", "absolute file URLs become root-relative");
  assert.equal(pdf.access, "edit", "a file takes the access of the space role");
  assert.equal((await t.store.tree("s1"))[0].folder.id, "f-docs");
  t.server.folders.delete("f-img");
  t.server.files.delete("x-pdf");
  await t.sync.syncNow();
  assert.equal(await t.store.getFolder("f-img"), null);
  assert.equal(await t.store.getFile("x-pdf"), null);
  assert.ok(await t.store.getNote("readme"));
});

test("a pull that renames or moves a folder updates the local paths below it", async () => {
  const t = await setupItems();
  const docs = t.server.folders.get("f-docs");
  docs.name = "Papers";
  t.server.repath(docs, "Papers");
  await t.sync.syncNow();
  const calls = t.calls().filter((c) => c.startsWith("GET /api/sync"));
  assert.equal(calls.length, 1);
  assert.equal((await t.store.getFolder("f-docs")).path, "Papers");
  assert.equal((await t.store.getFolder("f-img")).path, "Papers/img", "the subfolder did not arrive but moved");
  assert.equal((await t.store.getFile("x-pdf")).path, "Papers/report.pdf");
  assert.equal((await t.store.getNote("readme")).path, "Papers/readme.md");
  assert.equal((await t.store.resolveLink("s1", "readme", "report.pdf", { form: "markdownLink" })).id, "x-pdf");
});

test("folder.create replays online, is idempotent and takes a free name on a 409", async () => {
  const t = await setupItems({ online: false });
  const folder = await t.sync.createFolder({ spaceId: "s1", parentId: "f-docs", name: "Plans" });
  assert.equal(folder.path, "Docs/Plans");
  assert.equal(folder.provisional, true);
  assert.equal(t.server.calls.length, 0);
  await assert.rejects(() => t.sync.createFolder({ spaceId: "s1", parentId: "f-docs", name: "plans" }), /already exists/);
  await assert.rejects(() => t.sync.createFolder({ spaceId: "s1", name: ".git" }), /dot/);

  t.net.online = true;
  t.server.failures.push({ method: "POST", path: /^\/api\/folders$/, status: 0, afterApply: true });
  await t.sync.syncNow();
  assert.equal(await t.store.countOps(), 1, "a lost reply keeps the op");
  await t.sync.syncNow();
  assert.equal(await t.store.countOps(), 0);
  assert.equal([...t.server.folders.values()].filter((f) => f.name === "Plans").length, 1);
  assert.equal((await t.store.getFolder(folder.id)).provisional, false);

  // Someone else created the same name meanwhile.
  t.net.online = false;
  const clash = await t.sync.createFolder({ spaceId: "s1", name: "Shared" });
  t.server.seedFolder({ id: "other", name: "Shared" });
  t.net.online = true;
  await t.sync.syncNow();
  assert.equal(t.server.folders.get(clash.id).name, "Shared (2)");
  assert.equal((await t.store.getFolder(clash.id)).path, "Shared (2)");
  assert.equal(await t.store.countOps(), 0);
});

test("folder.rename coalesces into its create and moves local paths below it", async () => {
  const t = await setupItems({ online: false });
  const f = await t.sync.createFolder({ spaceId: "s1", name: "Draft" });
  await t.sync.renameItem(f.id, "Drafts");
  await t.sync.renameItem(f.id, "Final");
  let ops = await t.store.peekOps();
  assert.deepEqual(ops.map((o) => o.type), ["folder.create"]);
  assert.equal(ops[0].body.name, "Final");

  await t.sync.renameItem("f-docs", "Docs 1");
  await t.sync.renameItem("f-docs", "Documents");
  ops = await t.store.peekOps();
  assert.deepEqual(ops.map((o) => o.type), ["folder.create", "folder.rename"]);
  assert.equal(ops[1].name, "Documents");
  assert.equal((await t.store.getFolder("f-img")).path, "Documents/img");
  assert.equal((await t.store.getFile("x-pdf")).path, "Documents/report.pdf");
  assert.equal((await t.store.getNote("readme")).path, "Documents/readme.md");
  await assert.rejects(() => t.sync.renameItem("f-img", "a/b"), /slash/);

  t.net.online = true;
  await t.sync.syncNow();
  assert.deepEqual(t.calls().filter((c) => !c.startsWith("GET")), ["POST /api/folders", "PATCH /api/folders/f-docs"]);
  assert.equal(t.server.folders.get("f-docs").path, "Documents");
  assert.equal(t.server.folders.get(f.id).name, "Final");
  assert.equal((await t.store.getFile("x-pdf")).path, "Documents/report.pdf");
});

test("file.upload sends text fields before the file, with the client id, and reports progress", async () => {
  const t = await setupItems({ online: false });
  const [rec, second] = await t.sync.uploadFiles("s1", "f-docs", [new File(["123456789"], "report.pdf", { type: "application/pdf" }), new File(["x"], "notes.txt")]);
  assert.equal(rec.name, "report (2).pdf", "a taken name gets a number");
  assert.equal(rec.path, "Docs/report (2).pdf");
  assert.equal(rec.pendingKey, rec.id);
  assert.ok(await t.store.getBlob(rec.id), "the blob is stored under the client id");
  assert.deepEqual(t.sync.uploads().map((u) => [u.state, u.filename, u.size]), [["queued", "report (2).pdf", 9], ["queued", "notes.txt", 1]]);
  await assert.rejects(() => t.sync.uploadFiles("s1", "", [new File(["x"], "page.md")]), /\.md/);

  t.net.online = true;
  t.server.uploadImpl = async (path, f, opts, real) => {
    for (const loaded of [3, 6, 9]) {
      t.clock.t += 150;
      opts.onProgress(loaded, f.size);
    }
    return real(path, f, opts);
  };
  await t.sync.syncNow();
  const up = t.server.calls.find((c) => c.method === "UPLOAD");
  assert.deepEqual(up.fieldOrder, ["spaceId", "parentId", "id", "onConflict"]);
  assert.deepEqual(up.fields, { spaceId: "s1", parentId: "f-docs", id: rec.id, onConflict: "rename" });
  assert.equal(t.server.files.get(rec.id).name, "report (2).pdf");
  assert.equal(t.server.contents.get(second.id), "x");
  const stored = await t.store.getFile(rec.id);
  assert.equal(stored.provisional, false);
  assert.equal(stored.pendingKey, undefined);
  assert.equal(stored.sha256, "sha-123456789");
  assert.equal(await t.store.getBlob(rec.id), null);
  const loads = t.of("uploads-changed").map((p) => p.uploads[0] && `${p.uploads[0].state}:${p.uploads[0].loaded}`);
  for (const want of ["uploading:0", "uploading:3", "uploading:6", "uploading:9"]) assert.ok(loads.includes(want), `${want} in ${loads}`);
  assert.deepEqual(t.sync.uploads().map((u) => u.state), ["done", "done"]);
});

test("upload progress events are throttled to one per 100 ms", async () => {
  const t = await setupItems();
  let throttled = 0;
  t.server.uploadImpl = async (path, f, opts, real) => {
    const before = t.of("uploads-changed").length;
    for (let i = 1; i <= 10; i++) opts.onProgress(i, 10);
    throttled = t.of("uploads-changed").length - before;
    const timer = [...t.timers.pending.entries()].find(([, v]) => v.ms === 100);
    assert.ok(timer, "a trailing event is scheduled");
    t.timers.fire(timer[0]);
    assert.equal(t.of("uploads-changed").at(-1).uploads[0].loaded, 10);
    return real(path, f, opts);
  };
  await t.sync.uploadFiles("s1", "", [new File(["0123456789"], "f.bin")]);
  await t.sync.syncNow();
  assert.equal(throttled, 0);
});

test("uploadAttachment returns an encoded relative path in the note folder and fixes the link after a server rename", async () => {
  const t = await setupItems({ online: false });
  const res = await t.sync.uploadAttachment("readme", new File(["png"], "east wall.png", { type: "image/png" }));
  assert.equal(res.url, "east%20wall.png");
  assert.equal(res.pending, true);
  const rec = await t.store.getFile(res.fileId);
  assert.equal(rec.path, "Docs/east wall.png");
  assert.equal(rec.parentId, "f-docs");
  await t.sync.updateNote("readme", { content: `![](${res.url}) and [again](<east wall.png>) and [third](east%20wall.png)` });
  assert.equal((await t.store.resolveLink("s1", "readme", res.url, { form: "markdownImage" })).id, res.fileId, "the link resolves locally before the upload");

  t.server.seedFile({ id: "taken", name: "east wall.png", parentId: "f-docs" });
  t.net.online = true;
  await t.sync.syncNow();
  assert.equal(t.server.files.get(res.fileId).name, "east wall (2).png");
  assert.equal(t.server.notes.get("readme").content, "![](east%20wall%20(2).png) and [again](<east%20wall%20(2).png>) and [third](east%20wall%20(2).png)");
  assert.equal((await t.store.getFile(res.fileId)).path, "Docs/east wall (2).png");
});

test("uploadAttachment uses the space pasteFolder, with pasteFor when the folder is not local", async () => {
  const t = await setupItems();
  t.server.spaces[0].pasteFolder = "attachments";
  await t.sync.syncNow();
  t.net.online = false;
  const res = await t.sync.uploadAttachment("readme", new File(["png"], "shot.png", { type: "image/png" }));
  assert.equal(res.url, "attachments/shot.png");
  assert.equal((await t.store.getFile(res.fileId)).path, "Docs/attachments/shot.png");
  const [op] = await t.store.peekOps();
  assert.deepEqual([op.type, op.body.pasteFor, op.body.parentId], ["file.upload", "readme", undefined]);
  t.net.online = true;
  await t.sync.syncNow();
  const up = t.server.calls.find((c) => c.method === "UPLOAD");
  assert.deepEqual(up.fieldOrder, ["spaceId", "pasteFor", "id", "onConflict"]);
  assert.equal(await t.store.countOps(), 0);
});

test("a failed upload can be retried and a network failure leaves it queued", async () => {
  const t = await setupItems();
  const [rec] = await t.sync.uploadFiles("s1", "", [new File(["x"], "big.zip")]);
  t.server.failures.push({ method: "UPLOAD", path: /files/, status: 413, message: "too large" });
  await t.sync.syncNow();
  let [item] = t.sync.uploads();
  assert.deepEqual([item.state, item.error], ["failed", "too large"]);
  assert.equal(t.of("rejected")[0].op.type, "file.upload");
  assert.ok(await t.store.getBlob(rec.id), "the file is kept for a retry");
  assert.equal((await t.store.getFile(rec.id)).uploadFailed, true);
  assert.equal(await t.store.countOps(), 0);

  await t.sync.retryUpload(rec.id);
  assert.equal(t.sync.uploads()[0].state, "queued");
  t.server.failures.push({ method: "UPLOAD", path: /files/, status: 0, message: "offline" });
  await t.sync.syncNow();
  [item] = t.sync.uploads();
  assert.deepEqual([item.state, item.error, item.loaded], ["queued", "offline", 0]);
  assert.equal(await t.store.countOps(), 1);
  await t.sync.syncNow();
  assert.equal(t.sync.uploads()[0].state, "done");
  assert.ok(t.server.files.has(rec.id));
});

test("cancelUpload removes the blob, the op, the record and the link in the note", async () => {
  const t = await setupItems({ online: false });
  const res = await t.sync.uploadAttachment("readme", new File(["x"], "shot.png"));
  await t.sync.updateNote("readme", { content: `before ![alt](${res.url}) after` });
  await t.sync.cancelUpload(res.fileId);
  assert.deepEqual(t.sync.uploads(), []);
  assert.equal(await t.store.getBlob(res.fileId), null);
  assert.equal(await t.store.getFile(res.fileId), null);
  assert.equal((await t.store.getNote("readme")).content, "before shot.png after");
  const ops = await t.store.peekOps();
  assert.deepEqual(ops.map((o) => o.type), ["update"]);
  assert.deepEqual(await t.store.getMeta("uploads"), []);
});

test("queued and failed uploads survive a new engine instance", async () => {
  const t = await setupItems();
  const [failed] = await t.sync.uploadFiles("s1", "", [new File(["x"], "bad.exe")]);
  t.server.failures.push({ method: "UPLOAD", path: /files/, status: 415 });
  await t.sync.syncNow();
  t.net.online = false;
  const [queued] = await t.sync.uploadFiles("s1", "f-img", [new File(["yy"], "later.png")]);
  const again = t.make();
  await again.ready;
  assert.deepEqual(again.uploads().map((u) => [u.key, u.state, u.filename, u.size, u.spaceId]), [
    [failed.id, "failed", "bad.exe", 1, "s1"],
    [queued.id, "queued", "later.png", 2, "s1"],
  ]);
  t.net.online = true;
  await again.syncNow();
  assert.deepEqual(again.uploads().map((u) => u.state).sort(), ["done", "failed"]);
  await again.retryUpload(failed.id);
  await again.syncNow();
  assert.deepEqual(again.uploads().map((u) => u.state), ["done", "done"]);
});

test("file.rename coalesces into its upload and consecutive renames merge", async () => {
  const t = await setupItems({ online: false });
  const [rec] = await t.sync.uploadFiles("s1", "", [new File(["a"], "a.txt")]);
  await t.sync.renameItem(rec.id, "b.txt");
  let ops = await t.store.peekOps();
  assert.deepEqual([ops.length, ops[0].body.name], [1, "b.txt"]);
  await t.sync.renameItem("x-pdf", "r1.pdf");
  await t.sync.renameItem("x-pdf", "r2.pdf");
  ops = await t.store.peekOps();
  assert.deepEqual(ops.map((o) => [o.type, o.name]), [["file.upload", undefined], ["file.rename", "r2.pdf"]]);
  assert.equal((await t.store.getFile("x-pdf")).url, "/files/docs/x-pdf/r2.pdf");
  await assert.rejects(() => t.sync.renameItem("x-pdf", "r2.md"), /\.md/);
  t.net.online = true;
  await t.sync.syncNow();
  assert.equal(t.server.files.get(rec.id).name, "b.txt");
  assert.equal(t.server.files.get("x-pdf").name, "r2.pdf");
  assert.equal(await t.store.countOps(), 0);
});

test("file.replace coalesces into an unsent upload or replace and sends baseSha256 first", async () => {
  const t = await setupItems({ online: false });
  const [rec] = await t.sync.uploadFiles("s1", "", [new File(["one"], "draft.txt")]);
  await t.sync.replaceFile(rec.id, new File(["two!"], "draft.txt"));
  let ops = await t.store.peekOps();
  assert.deepEqual(ops.map((o) => o.type), ["file.upload"]);
  assert.equal(await (await t.store.getBlob(rec.id)).text(), "two!");
  assert.equal(t.sync.uploads().length, 1);
  assert.equal((await t.store.getFile(rec.id)).pendingKey, rec.id);

  await t.sync.replaceFile("x-pdf", new File(["v2"], "report.pdf"));
  await t.sync.replaceFile("x-pdf", new File(["v3"], "report.pdf"));
  ops = await t.store.peekOps();
  const replace = ops.filter((o) => o.type === "file.replace");
  assert.equal(replace.length, 1);
  assert.equal(replace[0].baseSha256, "sha-v1", "the merged replace keeps the first base hash");
  assert.equal(await (await t.store.getBlob(replace[0].key)).text(), "v3");
  assert.equal((await t.store.getFile("x-pdf")).pendingKey, replace[0].key);

  t.net.online = true;
  await t.sync.syncNow();
  assert.equal(t.server.contents.get(rec.id), "two!");
  assert.equal(t.server.contents.get("x-pdf"), "v3");
  const put = t.server.calls.find((c) => c.method === "UPLOAD-PUT");
  assert.equal(put.path, "/api/files/x-pdf/content");
  assert.deepEqual(put.fieldOrder, ["baseSha256"]);
  const pdf = await t.store.getFile("x-pdf");
  assert.deepEqual([pdf.sha256, pdf.pendingKey, pdf.size], ["sha-v3", undefined, 2]);
  assert.equal(await t.store.getBlob(replace[0].key), null);
});

test("a 409 on file.replace keeps the local content as a conflicted copy", async () => {
  const t = await setupItems({ online: false });
  await t.sync.replaceFile("x-pdf", new File(["mine"], "report.pdf", { type: "application/pdf" }));
  const srv = t.server.files.get("x-pdf");
  Object.assign(srv, { sha256: "sha-theirs", size: 6, rev: ++t.server.rev });
  t.server.contents.set("x-pdf", "theirs");
  t.net.online = true;
  await t.sync.syncNow();
  const [conflict] = t.of("conflict");
  assert.deepEqual([conflict.kind, conflict.noteId], ["file", "x-pdf"]);
  const copy = t.server.files.get(conflict.copyId);
  assert.match(copy.name, /^report \(conflicted copy \d{4}-\d{2}-\d{2} \d{2}:\d{2}\)\.pdf$/);
  assert.equal(copy.parentId, "f-docs");
  assert.equal(t.server.contents.get(conflict.copyId), "mine");
  const original = await t.store.getFile("x-pdf");
  assert.deepEqual([original.sha256, original.pendingKey], ["sha-theirs", undefined]);
  assert.equal((await t.store.getFile(conflict.copyId)).path, `Docs/${copy.name}`);
  assert.equal(await t.store.countOps(), 0);
  assert.equal(t.sync.status().conflicts, 1);
});

test("item.move coalesces, moves local paths below a folder and refuses a move into itself", async () => {
  const t = await setupItems({ online: false });
  const f = await t.sync.createFolder({ spaceId: "s1", name: "Box" });
  await t.sync.moveItem(f.id, "f-docs");
  let ops = await t.store.peekOps();
  assert.deepEqual([ops.length, ops[0].body.parentId], [1, "f-docs"], "a move folds into the unsent create");
  await assert.rejects(() => t.sync.moveItem("f-docs", "f-img"), /into itself/);
  await assert.rejects(() => t.sync.moveItem("f-docs", "f-docs"), /into itself/);

  await t.sync.moveItem("f-img", f.id);
  await t.sync.moveItem("f-img", "");
  ops = await t.store.peekOps();
  const moves = ops.filter((o) => o.type === "item.move");
  assert.equal(moves.length, 1);
  assert.equal(moves[0].parentId, "");
  assert.equal((await t.store.getFolder("f-img")).path, "img");
  await t.sync.moveItem("x-pdf", "f-img");
  assert.equal((await t.store.getFile("x-pdf")).path, "img/report.pdf");
  await t.sync.moveItem("readme", "f-img");
  assert.equal((await t.store.getNote("readme")).path, "img/readme.md");

  t.net.online = true;
  await t.sync.syncNow();
  assert.equal(t.server.folders.get(f.id).path, "Docs/Box");
  assert.equal(t.server.folders.get("f-img").path, "img");
  assert.equal(t.server.files.get("x-pdf").path, "img/report.pdf");
  assert.equal(t.server.notes.get("readme").path, "img/readme.md");
  assert.ok(t.calls().includes("POST /api/items/x-pdf/move"));
  assert.ok(t.calls().includes("POST /api/notes/readme/move"), "notes keep the note move route");
  assert.equal(await t.store.countOps(), 0);
});

test("item.copy shows provisional copies with children, then takes the server copy", async () => {
  const t = await setupItems({ online: false });
  const copy = await t.sync.copyItem("f-docs", { parentId: "" });
  assert.equal(copy.name, "Docs copy");
  assert.equal(copy.provisional, true);
  const tree = await t.store.tree("s1");
  const node = tree.find((n) => n.folder && n.folder.id === copy.id);
  assert.deepEqual(node.children.map((c) => (c.folder || c.note || c.file).path).sort(), ["Docs copy/img", "Docs copy/readme.md", "Docs copy/report.pdf"]);
  await assert.rejects(() => t.sync.renameItem(node.children[0].folder.id, "x"), /still syncing/);
  await t.sync.renameItem(copy.id, "Archive");
  const [op] = await t.store.peekOps();
  assert.deepEqual([op.type, op.sourceId, op.body.name], ["item.copy", "f-docs", "Archive"]);
  const fileCopy = await t.sync.copyItem("x-pdf", { parentId: "f-docs" });
  assert.equal(fileCopy.name, "report copy.pdf");
  await assert.rejects(() => t.sync.copyItem("f-docs", { parentId: "f-img" }), /into itself/);

  t.net.online = true;
  await t.sync.syncNow();
  assert.equal(await t.store.countOps(), 0);
  assert.equal(await t.store.getFolder(copy.id), null, "the provisional copy is gone");
  const archive = [...t.server.folders.values()].find((f) => f.name === "Archive");
  assert.ok(archive);
  assert.equal((await t.store.getFolder(archive.id)).path, "Archive");
  const children = (await t.store.tree("s1")).find((n) => n.folder && n.folder.id === archive.id).children;
  assert.equal(children.length, 3, "the pull brought the children of the copy");
  assert.ok([...t.server.files.values()].some((f) => f.name === "report copy.pdf"));
  assert.equal(t.of("copied").length, 2);
});

test("item.trash then item.restore unsent drop both, and a sent trash covers the subtree", async () => {
  const t = await setupItems({ online: false });
  await t.sync.trashItem("f-docs");
  assert.equal((await t.store.getFile("x-pdf")).deleted, true);
  assert.equal((await t.store.getNote("readme")).deleted, true);
  await t.sync.restoreItem("f-docs");
  assert.equal(await t.store.countOps(), 0);
  assert.equal((await t.store.getFile("x-pdf")).deleted, false);
  assert.equal((await t.store.getNote("readme")).deleted, false);

  await t.sync.trashItem("x-pdf");
  t.net.online = true;
  await t.sync.syncNow();
  assert.equal(t.server.files.get("x-pdf").deleted, true);
  assert.equal((await t.store.getFile("x-pdf")).deleted, true);
  await t.sync.restoreItem("x-pdf");
  await t.sync.syncNow();
  assert.deepEqual(t.calls().filter((c) => !c.startsWith("GET")), ["POST /api/items/x-pdf/trash", "POST /api/items/x-pdf/restore"]);
  assert.equal((await t.store.getFile("x-pdf")).deleted, false);
});

test("item.delete drops an unsent create with its children and a 404 counts as done", async () => {
  const t = await setupItems({ online: false });
  const f = await t.sync.createFolder({ spaceId: "s1", name: "Temp" });
  const [rec] = await t.sync.uploadFiles("s1", f.id, [new File(["x"], "t.txt")]);
  await t.sync.renameItem("x-pdf", "keep.pdf");
  await t.sync.deleteItem(f.id);
  let ops = await t.store.peekOps();
  assert.deepEqual(ops.map((o) => o.type), ["file.rename"]);
  assert.equal(await t.store.getBlob(rec.id), null);
  assert.equal(await t.store.getFile(rec.id), null);
  assert.deepEqual(t.sync.uploads(), []);

  await t.sync.deleteItem("f-docs");
  ops = await t.store.peekOps();
  assert.deepEqual(ops.map((o) => o.type), ["item.delete"], "queued ops below a deleted folder go too");
  assert.deepEqual(ops[0].descendants.sort(), ["f-img", "readme", "x-pdf"]);
  assert.equal(await t.store.getNote("readme"), null);
  t.server.folders.delete("f-docs");
  t.net.online = true;
  await t.sync.syncNow();
  assert.equal(await t.store.countOps(), 0);
  assert.equal(t.of("rejected").length, 0);
});

test("a refused rename or move restores the server copy and emits rejected", async () => {
  const t = await setupItems();
  await t.sync.renameItem("f-docs", "Mine");
  await t.sync.moveItem("x-pdf", "");
  t.server.failures.push({ method: "PATCH", path: /folders/, status: 403, message: "read only" });
  t.server.failures.push({ method: "POST", path: /move/, status: 400, message: "bad move" });
  await t.sync.syncNow();
  assert.equal((await t.store.getFolder("f-docs")).path, "Docs");
  assert.equal((await t.store.getFolder("f-img")).path, "Docs/img");
  assert.equal((await t.store.getFile("x-pdf")).path, "Docs/report.pdf");
  assert.deepEqual(t.of("rejected").map((r) => [r.op.type, r.status]), [["folder.rename", 403], ["item.move", 400]]);
  assert.equal(await t.store.countOps(), 0);
});

test("a pull keeps a queued folder local and applies the server copy after the op", async () => {
  const t = await setupItems();
  await t.sync.renameItem("f-img", "images");
  t.server.failures.push({ method: "PATCH", path: /folders/, status: 503, sticky: true });
  const img = t.server.folders.get("f-img");
  img.position = 9;
  img.rev = ++t.server.rev;
  await t.sync.syncNow();
  assert.equal((await t.store.getFolder("f-img")).name, "images");
  assert.equal((await t.store.getMeta("server:f-img")).position, 9);
  t.server.failures.length = 0;
  await t.sync.syncNow();
  const local = await t.store.getFolder("f-img");
  assert.deepEqual([local.name, local.path], ["images", "Docs/images"]);
  assert.equal(await t.store.getMeta("server:f-img"), undefined);
});
