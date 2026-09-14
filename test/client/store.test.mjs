import { test } from "node:test";
import assert from "node:assert/strict";

import { readFileSync } from "node:fs";
import {
  createMemoryStore,
  normalizeTarget,
  makeSnippet,
  buildTree,
  compareTreeItems,
  databaseName,
  resolveWith,
  splitTarget,
  SCHEMA_VERSION,
} from "../../internal/web/static/js/store.js";
import { Snapshot } from "../../internal/web/static/js/links.js";

const T = (min) => new Date(Date.UTC(2026, 8, 13, 9, min)).toISOString();

function note(id, fields = {}) {
  return {
    id,
    spaceId: "s1",
    spaceSlug: "docs",
    parentId: "",
    path: `${id}.md`,
    slug: id,
    title: id.toUpperCase(),
    tags: [],
    content: "",
    position: 100,
    createdAt: T(0),
    updatedAt: T(0),
    createdBy: "u1",
    deleted: false,
    rev: 1,
    access: "edit",
    ...fields,
  };
}

async function seeded() {
  const store = createMemoryStore();
  await store.putSpaces([
    { id: "s1", slug: "docs", name: "Docs", myRole: "editor" },
    { id: "s2", slug: "other", name: "Other", myRole: "" },
  ]);
  await store.putNotes([
    note("guide", { title: "Guide", path: "guide.md", position: 200, updatedAt: T(5), starred: true }),
    note("install", { title: "Install", parentId: "guide", path: "guide/install.md", position: 100, updatedAt: T(3) }),
    note("intro", { title: "Intro", path: "intro.md", position: 100, updatedAt: T(9) }),
    note("old", { title: "Old", path: "old.md", deleted: true, updatedAt: T(20) }),
    note("orphan", { title: "Orphan", parentId: "missing", path: "missing/orphan.md", position: 50, updatedAt: T(1) }),
    note("theirs", { spaceId: "s2", spaceSlug: "other", title: "Theirs", createdBy: "u9", updatedAt: T(7) }),
  ]);
  return store;
}

test("database name", () => {
  assert.equal(databaseName("abc"), "markspace-abc");
});

test("meta values round trip and undefined deletes", async () => {
  const store = createMemoryStore();
  assert.equal(await store.getMeta("rev"), undefined);
  await store.setMeta("rev", 12);
  await store.setMeta("user", { id: "u1" });
  assert.equal(await store.getMeta("rev"), 12);
  const user = await store.getMeta("user");
  user.id = "changed";
  assert.deepEqual(await store.getMeta("user"), { id: "u1" }, "stored values are copies");
  await store.setMeta("rev", undefined);
  assert.equal(await store.getMeta("rev"), undefined);
});

test("spaces are replaced as a set and found by id or slug", async () => {
  const store = await seeded();
  assert.equal((await store.getSpace("s1")).slug, "docs");
  assert.equal((await store.getSpace("other")).id, "s2");
  assert.equal(await store.getSpace("nope"), null);
  await store.putSpaces([{ id: "s3", slug: "new", name: "New" }]);
  assert.deepEqual((await store.listSpaces()).map((s) => s.id), ["s3"]);
});

test("notes: get, copies, delete, path lookup and listing", async () => {
  const store = await seeded();
  const n = await store.getNote("guide");
  n.title = "mutated";
  assert.equal((await store.getNote("guide")).title, "Guide");
  assert.equal(await store.getNote("missing"), null);

  assert.equal((await store.getNoteByPath("docs", "guide/install")).id, "install");
  assert.equal((await store.getNoteByPath("docs", "guide/install.md")).id, "install");
  assert.equal(await store.getNoteByPath("other", "guide/install"), null);

  assert.deepEqual((await store.listNotes("s1")).map((x) => x.id), ["orphan", "install", "intro", "guide"]);
  assert.ok((await store.listNotes("s1", { includeDeleted: true })).some((x) => x.id === "old"));
  assert.equal((await store.listNotes("s1"))[0].content, "", "content is included");
  assert.equal((await store.listAllNotes()).length, 5);
  assert.equal((await store.listAllNotes({ includeDeleted: true })).length, 6);

  await store.deleteNotes(["intro", "orphan"]);
  assert.equal(await store.getNote("intro"), null);
  assert.equal((await store.listAllNotes()).length, 3);
});

test("recent, starred and shared lists", async () => {
  const store = await seeded();
  assert.deepEqual((await store.recentNotes(3)).map((x) => x.id), ["intro", "theirs", "guide"]);
  assert.deepEqual((await store.starredNotes()).map((x) => x.id), ["guide"]);
  assert.deepEqual((await store.sharedNotes("u1")).map((x) => x.id), ["theirs"]);
  assert.deepEqual((await store.sharedNotes("u9")).map((x) => x.id), []);
});

test("search ranks title matches above body matches and requires every token", async () => {
  const store = createMemoryStore();
  await store.putNotes([
    note("body", { title: "Notes", content: "how to deploy the server", updatedAt: T(9) }),
    note("title", { title: "Deploy guide", content: "steps for the server", updatedAt: T(1) }),
    note("tagged", { title: "Misc", tags: ["deploy"], content: "server list", updatedAt: T(2) }),
    note("other", { spaceId: "s2", title: "Deploy elsewhere", content: "server" }),
    note("gone", { title: "Deploy trash", content: "server", deleted: true }),
  ]);
  const hits = await store.searchNotes("deploy server");
  assert.deepEqual(hits.map((h) => h.note.id), ["title", "other", "tagged", "body"]);
  assert.ok(hits[0].score > hits[3].score);
  assert.deepEqual((await store.searchNotes("deploy server", { spaceId: "s1" })).map((h) => h.note.id), ["title", "tagged", "body"]);
  assert.deepEqual((await store.searchNotes("deploy missingword")).length, 0);
  assert.deepEqual(await store.searchNotes("   "), []);
  assert.equal((await store.searchNotes("server", { limit: 2 })).length, 2);
});

test("search snippets escape note text and mark matches", async () => {
  const store = createMemoryStore();
  await store.putNotes([
    note("x", { title: "<img src=x onerror=alert(1)>", content: 'before <script>alert("x")</script> Needle & <b>bold</b> after' }),
  ]);
  const [hit] = await store.searchNotes("needle");
  assert.ok(hit.snippet.includes("<mark>Needle</mark>"), hit.snippet);
  assert.ok(hit.snippet.includes("&lt;script&gt;alert(&quot;x&quot;)&lt;/script&gt;"), hit.snippet);
  assert.ok(hit.snippet.includes("&amp; &lt;b&gt;bold&lt;/b&gt;"), hit.snippet);
  assert.equal(hit.snippet.replace(/<\/?mark>/g, "").includes("<"), false);

  const [titleHit] = await store.searchNotes("onerror");
  assert.equal(titleHit.snippet.replace(/<\/?mark>/g, "").includes("<"), false, titleHit.snippet);
});

test("snippet windows long text with ellipses", () => {
  const text = "word ".repeat(100) + "target " + "word ".repeat(100);
  const s = makeSnippet(text, ["target"], 30);
  assert.ok(s.startsWith("…") && s.endsWith("…"), s);
  assert.ok(s.includes("<mark>target</mark>"));
  assert.ok(s.length < 120);
});

test("normalizeTarget matches the server", () => {
  assert.equal(normalizeTarget("  ./A/B.MD#x "), "a/b");
  assert.equal(normalizeTarget("/notes/page.markdown"), "notes/page");
  assert.equal(normalizeTarget(".md"), ".md");
  assert.equal(normalizeTarget("dir/"), "dir");
});

test("backlinks come from wikilinks and relative links in the same space", async () => {
  const store = createMemoryStore();
  await store.putNotes([
    note("target", { title: "Install Guide", slug: "install-guide", path: "install-guide.md" }),
    note("wiki", { title: "Wiki", content: "intro\nSee [[install guide|the guide]] now\nend", updatedAt: T(4) }),
    note("md", { title: "Md", content: "Read [this](install-guide.md#setup).", updatedAt: T(6) }),
    note("code", { title: "Code", content: "`[[Install Guide]]` is code" }),
    note("unrelated", { title: "Unrelated", content: "[[Something Else]] https://example.com/install-guide" }),
    note("trashed", { title: "Trashed", content: "[[Install Guide]]", deleted: true }),
    note("far", { spaceId: "s2", title: "Far", content: "[[Install Guide]]" }),
  ]);
  const back = await store.backlinks("target");
  assert.deepEqual(back.map((b) => b.note.id), ["md", "wiki"]);
  assert.equal(back.find((b) => b.note.id === "wiki").context, "See [[install guide|the guide]] now");
  assert.deepEqual(await store.backlinks("missing"), []);
});

test("outbox keeps order, replaces ops with their ids and counts", async () => {
  const store = createMemoryStore();
  const a = await store.enqueue({ type: "create", noteId: "n1" });
  const b = await store.enqueue({ type: "update", noteId: "n1" });
  const c = await store.enqueue({ type: "star", noteId: "n2" });
  assert.ok(a < b && b < c);
  assert.equal(await store.countOps(), 3);
  const ops = await store.peekOps();
  assert.deepEqual(ops.map((o) => o.opId), [a, b, c]);
  ops[0].changed = true;
  assert.equal((await store.peekOps())[0].changed, undefined, "peeked ops are copies");

  await store.removeOp(b);
  assert.deepEqual((await store.peekOps()).map((o) => o.type), ["create", "star"]);

  const current = await store.peekOps();
  current[0].body = { title: "merged" };
  await store.replaceOps(current);
  const replaced = await store.peekOps();
  assert.deepEqual(replaced.map((o) => o.opId), [a, c]);
  assert.deepEqual(replaced[0].body, { title: "merged" });
  const d = await store.enqueue({ type: "trash", noteId: "n3" });
  assert.ok(d > c);
  assert.equal(await store.countOps(), 3);
});

test("blobs, recovered items and wipe", async () => {
  const store = await seeded();
  const blob = new Blob(["hello"], { type: "text/plain" });
  await store.putBlob("k1", blob);
  assert.equal(await (await store.getBlob("k1")).text(), "hello");
  await store.deleteBlob("k1");
  assert.equal(await store.getBlob("k1"), null);

  const id = await store.putRecovered({ title: "Lost", content: "text", createdAt: T(1) });
  await store.putRecovered({ id: "r2", title: "Newer", content: "more", createdAt: T(2) });
  assert.deepEqual((await store.listRecovered()).map((r) => r.title), ["Newer", "Lost"]);
  await store.deleteRecovered(id);
  assert.deepEqual((await store.listRecovered()).map((r) => r.id), ["r2"]);

  await store.enqueue({ type: "star", noteId: "guide" });
  await store.setMeta("rev", 5);
  await store.wipe();
  assert.equal((await store.listAllNotes({ includeDeleted: true })).length, 0);
  assert.equal((await store.listSpaces()).length, 0);
  assert.equal(await store.countOps(), 0);
  assert.equal(await store.getMeta("rev"), undefined);
  assert.deepEqual(await store.listRecovered(), []);
});

test("getBlob resolves to the Blob itself or null", async () => {
  const store = createMemoryStore();
  const file = new File(["abc"], "a.txt", { type: "text/plain" });
  await store.putBlob("k", file);
  const got = await store.getBlob("k");
  assert.ok(got instanceof Blob);
  assert.equal(got.name, "a.txt");
  assert.equal(await store.getBlob("missing"), null);
});

test("getNoteByPath normalizes the path", async () => {
  const store = await seeded();
  await store.putNotes([note("spaced", { title: "Café notes", path: "guide/café notes.md" })]);
  for (const p of ["guide/install", "guide/install.md", "/guide/install", "guide/install/", "guide%2Finstall"]) {
    assert.equal((await store.getNoteByPath("docs", p))?.id, "install", p);
  }
  assert.equal((await store.getNoteByPath("docs", "guide/caf%C3%A9%20notes"))?.id, "spaced");
  assert.equal(await store.getNoteByPath("docs", "guide/%E0%A4%A"), null, "malformed escapes do not throw");
  assert.equal((await store.getNoteByPath("s1", "intro"))?.id, "intro", "a space id works too");
});

test("comments are cached per note as copies and replaced as a whole", async () => {
  const store = await seeded();
  assert.deepEqual(await store.listComments("guide"), []);
  const threads = [
    { id: "c1", body: "Why sync?", anchor: { exact: "sync", prefix: "the ", suffix: " engine", start: 4, end: 8 }, anchorState: "exact", replies: [{ id: "c2", body: "Speed." }] },
    { id: "c3", body: "General note", anchor: null },
  ];
  await store.putComments("guide", threads);
  const got = await store.listComments("guide");
  assert.deepEqual(got, threads);
  got[0].anchor.exact = "changed";
  threads[0].body = "changed too";
  assert.equal((await store.listComments("guide"))[0].anchor.exact, "sync", "returned lists are copies");
  assert.equal((await store.listComments("guide"))[0].body, "Why sync?", "stored lists are copies");
  assert.deepEqual(await store.listComments("intro"), [], "lists are per note");

  await store.putComments("guide", [{ id: "c4", body: "Only this" }]);
  assert.deepEqual((await store.listComments("guide")).map((c) => c.id), ["c4"]);
  await store.putComments("guide", null);
  assert.deepEqual(await store.listComments("guide"), []);
});

test("cached comments are dropped with the note and by wipe", async () => {
  const store = await seeded();
  await store.putComments("guide", [{ id: "c1" }]);
  await store.putComments("intro", [{ id: "c2" }]);
  await store.deleteNotes(["guide"]);
  assert.deepEqual(await store.listComments("guide"), []);
  assert.deepEqual((await store.listComments("intro")).map((c) => c.id), ["c2"]);
  await store.wipe();
  assert.deepEqual(await store.listComments("intro"), []);
});

// ---------------------------------------------------------------------------
// Schema 4: folders, files, the item tree and links.js resolution

const folder = (id, path, fields = {}) => ({
  id, spaceId: "s1", spaceSlug: "docs", parentId: "", name: path.split("/").pop(), path, position: 0,
  createdAt: T(0), updatedAt: T(0), deleted: false, rev: 1, ...fields,
});
const file = (id, path, fields = {}) => ({
  id, spaceId: "s1", spaceSlug: "docs", parentId: "", name: path.split("/").pop(), path, mime: "application/pdf", size: 10,
  sha256: "", position: 0, versions: 0, createdAt: T(0), updatedAt: T(0), modTime: T(0), deleted: false, rev: 1, ...fields,
});

async function itemStore() {
  const store = createMemoryStore();
  await store.putSpaces([{ id: "s1", slug: "docs", name: "Docs", myRole: "editor" }]);
  await store.putFolders([
    folder("f-proj", "Projects", { indexNoteId: "n-proj" }),
    folder("f-harb", "Projects/harbour", { parentId: "f-proj" }),
    folder("f-arch", "Archive", { position: 5 }),
    folder("f-gone", "Gone", { deleted: true }),
  ]);
  await store.putNotes([
    note("n-proj", { title: "Project overview", path: "Projects/index.md", parentId: "f-proj", position: 0 }),
    note("n-survey", { title: "Harbour survey", path: "Projects/harbour/harbour-survey.md", parentId: "f-harb", position: 0 }),
    note("n-start", { title: "getting started", path: "getting-started.md", position: 0, content: "![[east wall.jpg]] and [r](Projects/report.pdf)" }),
    note("n-zeta", { title: "Zeta", path: "zeta.md", position: 0 }),
  ]);
  await store.putFiles([
    file("x-wall", "Projects/harbour/east wall.jpg", { parentId: "f-harb", mime: "image/jpeg" }),
    file("x-report", "Projects/report.pdf", { parentId: "f-proj" }),
    file("x-start", "Getting started.pdf", { position: 0 }),
    file("x-old", "old.pdf", { deleted: true }),
    file("x-orphan", "Gone/orphan.txt", { parentId: "f-gone" }),
  ]);
  return store;
}

test("schema version is 4", () => {
  assert.equal(SCHEMA_VERSION, 4);
});

test("tree holds folders, notes and files with the folder note as the folder page", async () => {
  const store = await itemStore();
  const tree = await store.tree("s1");
  const label = (t) => `${t.kind}:${(t.folder || t.note || t.file).id}`;
  // Position 0 first: folders, then notes, then files, by lowercased name. Archive has position 5.
  assert.deepEqual(tree.map(label), ["folder:f-proj", "note:n-start", "note:n-zeta", "file:x-start", "file:x-orphan", "folder:f-arch"]);
  const proj = tree[0];
  assert.equal(proj.page.id, "n-proj", "index.md is the folder page");
  assert.deepEqual(proj.children.map(label), ["folder:f-harb", "file:x-report"]);
  assert.deepEqual(proj.children[0].children.map(label), ["note:n-survey", "file:x-wall"]);
  assert.ok(!JSON.stringify(tree).includes("x-old"), "trashed files are left out");
  const folderIds = [];
  const walk = (level) => level.forEach((t) => { if (t.folder) folderIds.push(t.folder.id); walk(t.children); });
  walk(tree);
  assert.ok(!folderIds.includes("f-gone"), "trashed folders are left out");
});

test("tree finds index.md by path when the folder names no index note", () => {
  const tree = buildTree({
    folders: [folder("f", "A")],
    notes: [note("i", { title: "A page", path: "A/index.md", parentId: "f" }), note("o", { title: "Other", path: "A/other.md", parentId: "f" })],
  });
  assert.equal(tree[0].page.id, "i");
  assert.deepEqual(tree[0].children.map((c) => c.note.id), ["o"]);
});

test("tree sort matches the server order", () => {
  const items = [
    { kind: "file", file: { id: "3", name: "b.pdf", position: 0 } },
    { kind: "note", note: { id: "2", title: "B", position: 0 } },
    { kind: "folder", folder: { id: "1", name: "z", position: 0 } },
    { kind: "note", note: { id: "0", title: "a", position: 0 } },
    { kind: "note", note: { id: "5", title: "A", position: 0 } },
    { kind: "folder", folder: { id: "4", name: "first", position: -1 } },
  ];
  assert.deepEqual(items.sort(compareTreeItems).map((t) => (t.folder || t.note || t.file).id), ["4", "1", "0", "5", "2", "3"]);
});

test("tree survives a folder parent cycle", () => {
  const tree = buildTree({ folders: [folder("a", "a", { parentId: "b" }), folder("b", "b", { parentId: "a" })] });
  assert.equal(tree.length, 2);
  assert.ok(tree.every((t) => t.kind === "folder"));
});

test("getItem and getItemByPath find every kind", async () => {
  const store = await itemStore();
  assert.equal((await store.getItem("x-wall")).kind, "file");
  assert.equal((await store.getItem("f-harb")).folder.path, "Projects/harbour");
  assert.equal((await store.getItem("n-proj")).note.title, "Project overview");
  assert.equal(await store.getItem("nope"), null);
  assert.equal((await store.getItemByPath("docs", "Projects/harbour/east%20wall.jpg")).file.id, "x-wall");
  assert.equal((await store.getItemByPath("s1", "/Projects/harbour/")).folder.id, "f-harb");
  assert.equal((await store.getItemByPath("docs", "Projects/harbour/harbour-survey")).note.id, "n-survey");
  assert.equal(await store.getItemByPath("docs", "projects/report.pdf"), null, "paths are case-sensitive");
});

test("folders and files list by space and delete with their comments", async () => {
  const store = await itemStore();
  assert.deepEqual((await store.listFolders("s1")).map((f) => f.id), ["f-arch", "f-proj", "f-harb"]);
  assert.equal((await store.listFiles("s1", { includeDeleted: true })).length, 5);
  assert.equal((await store.listAllFiles()).length, 4);
  await store.putComments("x-wall", [{ id: "c" }]);
  await store.deleteItems(["x-wall", "f-arch"]);
  assert.equal(await store.getFile("x-wall"), null);
  assert.equal(await store.getFolder("f-arch"), null);
  assert.deepEqual(await store.listComments("x-wall"), []);
});

test("splitTarget strips labels, headings and queries", () => {
  assert.deepEqual(splitTarget("Harbour survey#Findings|the survey", "wikilink"), { path: "Harbour survey", heading: "Findings" });
  assert.deepEqual(splitTarget("../east%20wall.jpg?x=1#top", "markdownLink"), { path: "../east wall.jpg", heading: "top" });
  assert.deepEqual(splitTarget("a%zz", "markdownLink"), { path: "a%zz", heading: "" });
});

const vectors = JSON.parse(readFileSync(new URL("../vectors/links.json", import.meta.url), "utf8"));

test("resolveWith reproduces every links.json resolve case", () => {
  const snaps = new Map();
  for (const c of vectors.resolve) {
    if (!snaps.has(c.tree)) snaps.set(c.tree, new Snapshot(vectors.trees[c.tree]));
    const hit = resolveWith(snaps.get(c.tree), c.from, c.target, c.form, { decoded: true });
    assert.equal(hit ? hit.path : null, c.want, c.name);
  }
});

/** Loads a links.json tree into a memory store as folders, notes and files. */
async function vectorStore(entries) {
  const store = createMemoryStore();
  await store.putSpaces([{ id: "s1", slug: "docs", name: "Docs" }]);
  const base = (p) => p.split("/").pop();
  const rec = (e) => ({ id: e.id, spaceId: "s1", path: e.path, name: base(e.path), deleted: false });
  await store.putFolders(entries.filter((e) => e.kind === "folder").map(rec));
  await store.putFiles(entries.filter((e) => e.kind === "file").map(rec));
  await store.putNotes(entries.filter((e) => e.kind === "note").map((e) => ({ ...rec(e), title: e.title || "" })));
  return store;
}

test("store.resolveLink matches the links.json resolve cases from a note", async () => {
  const stores = new Map();
  for (const c of vectors.resolve) {
    if (!stores.has(c.tree)) stores.set(c.tree, { store: await vectorStore(vectors.trees[c.tree]), entries: vectors.trees[c.tree] });
    const { store, entries } = stores.get(c.tree);
    const from = entries.find((e) => e.kind === "note" && e.path === c.from);
    if (!from) continue;
    const hit = await store.resolveLink("s1", from.id, c.target, { form: c.form, decoded: true });
    assert.equal(hit ? hit.path : null, c.want, c.name);
    if (hit) assert.equal((hit.item.folder || hit.item.note || hit.item.file).id, hit.id, c.name);
  }
});

test("resolveLink resolves relative paths from the note folder and keeps the heading", async () => {
  const store = await itemStore();
  const survey = await store.resolveLink("s1", "n-survey", "../report.pdf#p2", { form: "markdownLink" });
  assert.equal(survey.id, "x-report");
  assert.equal(survey.heading, "p2");
  assert.equal(survey.item.kind, "file");
  assert.equal((await store.resolveLink("s1", "n-survey", "east wall.jpg")).id, "x-wall");
  assert.equal((await store.resolveLink("s1", "n-start", "east wall.jpg")).id, "x-wall", "a unique name resolves anywhere");
  assert.equal(await store.resolveLink("s1", "n-survey", "old.pdf"), null, "trashed files do not resolve");
  assert.equal((await store.resolveLink("s1", "", "Projects/harbour/harbour-survey")).id, "n-survey");
  // The two-argument form returns a note, for older callers.
  assert.equal((await store.resolveLink("s1", "harbour survey")).id, "n-survey");
  assert.equal(await store.resolveLink("s1", "Getting started.pdf"), null, "the legacy form returns notes only");
});

test("the snapshot follows renames, moves, trash and deletes incrementally", async () => {
  const store = await itemStore();
  const first = await store.snapshot("s1");
  assert.equal(first.byId("x-wall").path, "Projects/harbour/east wall.jpg");
  assert.equal(await store.snapshot("s1"), first, "an unchanged space reuses its snapshot");
  const wall = await store.getFile("x-wall");
  await store.putFiles([{ ...wall, name: "north wall.jpg", path: "Projects/harbour/north wall.jpg" }]);
  assert.equal((await store.resolveLink("s1", "n-survey", "north wall.jpg")).id, "x-wall");
  assert.equal(await store.resolveLink("s1", "n-survey", "east wall.jpg"), null);
  await store.putFiles([{ ...wall, deleted: true }]);
  assert.equal((await store.snapshot("s1")).byId("x-wall"), null);
  await store.deleteItems(["x-report"]);
  assert.equal((await store.snapshot("s1")).byId("x-report"), null);
  const unchanged = await store.snapshot("s1");
  await store.putNotes([await store.getNote("n-zeta")]);
  assert.equal(await store.snapshot("s1"), unchanged, "a write that keeps path and title keeps the snapshot");
});

test("backlinks find notes that embed or link a file", async () => {
  const store = await itemStore();
  assert.deepEqual((await store.backlinks("x-wall")).map((b) => b.note.id), ["n-start"]);
  const back = await store.backlinks("x-report");
  assert.deepEqual(back.map((b) => b.note.id), ["n-start"]);
  assert.equal(back[0].context, "![[east wall.jpg]] and [r](Projects/report.pdf)");
  assert.deepEqual(await store.backlinks("nope"), []);
});

test("searchFiles matches every token in the file name", async () => {
  const store = await itemStore();
  assert.deepEqual((await store.searchFiles("wall east")).map((h) => h.file.id), ["x-wall"]);
  assert.deepEqual(await store.searchFiles("old"), [], "trashed files are skipped");
});
