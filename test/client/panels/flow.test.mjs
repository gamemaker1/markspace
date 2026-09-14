// Tests for the data flow of the rail panels on the fake DOM: the
// Suggestions tab with an editor stub and a server event, the item route
// prefix of the shared Comments, Share and History panels, and the command
// responses of a plugin panel with a stub renderPluginUI.
import { test, beforeEach } from "node:test";
import assert from "node:assert/strict";
import { register } from "node:module";
import { installDOM, FakeEvent } from "../fakedom.mjs";

// Browser modules import each other as /static/js/...; map that to the files.
register("data:text/javascript," + encodeURIComponent(`
export async function resolve(specifier, context, next) {
  if (specifier.startsWith("/static/")) {
    return { url: new URL("../../../internal/web/static/" + specifier.slice(8), ${JSON.stringify(import.meta.url)}).href, shortCircuit: true };
  }
  return next(specifier, context);
}`));

installDOM();
const panels = await import("../../../internal/web/static/js/panels.js");
const filepanels = await import("../../../internal/web/static/js/filepanels.js");

const sleep = (ms = 0) => new Promise((resolve) => setTimeout(resolve, ms));
const click = (el) => el.dispatchEvent(new FakeEvent("click", { bubbles: true }));
const textOf = (el) => String((el && el.textContent) || "");

beforeEach(() => {
  installDOM({ focusEvents: true });
  globalThis.location = { origin: "https://notes.example" };
});

/** Builds a fake api(method, url, body) that answers from [pattern, answer] pairs and records calls. */
function fakeApi(routes) {
  const calls = [];
  const api = async (method, url, body) => {
    calls.push({ method, url, body });
    for (const [pattern, answer] of routes) {
      if (pattern.test(method + " " + url)) return typeof answer === "function" ? answer(body, url) : answer;
    }
    throw Object.assign(new Error("no route for " + method + " " + url), { status: 404 });
  };
  api.calls = calls;
  return api;
}

function mount() {
  const host = document.createElement("div");
  document.body.appendChild(host);
  return host;
}

/** Finds the button in a container whose visible text is label. */
function buttonNamed(root, label) {
  return root.querySelectorAll("button").find((b) => textOf(b).trim() === label) || null;
}

test("pure helpers group suggestions, match events and build routes", () => {
  const list = [
    { id: "a", authorKind: "ai", createdAt: "2026-09-13T10:00:00Z", edits: [{ id: "e1" }] },
    { id: "p", authorKind: "plugin", createdAt: "2026-09-13T09:00:00Z", edits: [{ id: "e2", status: "open" }, { id: "e3", status: "accepted" }] },
    { id: "u2", authorKind: "user", createdAt: "2026-09-13T08:00:00Z", edits: [{ id: "e4" }] },
    { id: "u1", authorKind: "", createdAt: "2026-09-13T07:00:00Z", edits: [{ id: "e5" }] },
    { id: "done", authorKind: "user", status: "accepted", edits: [{ id: "e6" }] },
    { id: "empty", authorKind: "user", status: "open", edits: [{ id: "e7", status: "discarded" }] },
  ];
  const groups = panels.groupSuggestions(list);
  assert.deepEqual(groups.map((g) => [g.title, g.items.map((s) => s.id)]), [["People", ["u1", "u2"]], ["Plugins", ["p"]], ["AI", ["a"]]]);
  assert.deepEqual(panels.openEditIds(list[1]), ["e2"]);
  assert.ok(panels.suggestionEventMatches({ type: "suggestion.created" }, "n1"));
  assert.ok(panels.suggestionEventMatches({ type: "suggestion.accepted", noteId: "n1" }, "n1"));
  assert.ok(!panels.suggestionEventMatches({ type: "suggestion.accepted", noteId: "n2" }, "n1"));
  assert.ok(!panels.suggestionEventMatches({ type: "note.updated", noteId: "n1" }, "n1"));
  assert.equal(panels.itemRoute({ note: { id: "n 1" } }), "/api/notes/n%201");
  assert.equal(panels.itemRoute({ item: { id: "f1" }, note: { id: "n1" } }), "/api/items/f1");
  assert.equal(panels.itemRoute({ route: "/api/items/x/", note: { id: "n1" } }), "/api/items/x");
  assert.equal(panels.pluginModuleOf("bookmark-wiki.wiki"), "bookmark-wiki");
  assert.equal(panels.pluginModuleOf("nodot"), "");
  const days = panels.versionDays(panels.versionDeltas([
    { id: "v1", createdAt: "2026-09-12T10:00:00" },
    { id: "v2", createdAt: "2026-09-13T09:00:00" },
    { id: "v3", createdAt: "2026-09-13T11:00:00" },
  ]));
  assert.deepEqual(days.map((d) => d.rows.map((r) => r.version.id)), [["v3", "v2"], ["v1"]]);
});

test("the Suggestions tab lists, publishes to the editor, reveals, accepts and follows events", async () => {
  let served = [
    { id: "s1", authorKind: "ai", title: "Tighten the intro", rationale: "Shorter sentences", status: "open", createdAt: "2026-09-13T10:00:00Z", edits: [{ id: "e1", status: "open" }, { id: "e2", status: "open" }] },
    { id: "s2", authorKind: "plugin", authorId: "wiki", title: "Add See also links", status: "open", createdAt: "2026-09-13T09:00:00Z", edits: [{ id: "e3", status: "open" }, { id: "e4", status: "accepted" }] },
    { id: "s3", authorKind: "user", authorId: "u2", title: "Fix a typo", status: "open", createdAt: "2026-09-13T08:00:00Z", edits: [{ id: "e5" }] },
  ];
  const api = fakeApi([
    [/^GET \/api\/notes\/n1\/suggestions\?status=open$/, () => ({ items: served })],
    [/^GET \/api\/spaces\/sp\/members$/, { items: [{ userId: "u2", user: { name: "Ana Lima" } }] }],
    [/^POST \/api\/suggestions\/s2\/accept$/, { suggestion: { id: "s2", status: "accepted" }, note: {} }],
  ]);
  const drawn = [];
  const revealed = [];
  const counts = [];
  const handlers = {};
  let synced = 0;
  const events = {
    on(type, fn) {
      handlers[type] = fn;
      return () => delete handlers[type];
    },
  };
  const editor = { setSuggestions: (list) => drawn.push(list.map((s) => s.id)), revealSuggestion: (id) => revealed.push(id) };
  const host = mount();
  const handle = panels.openSuggestions(host, {
    note: { id: "n1", spaceId: "sp" }, access: "edit", api, user: { id: "u1" }, isOnline: () => true,
    onCount: (n) => counts.push(n), editor: () => editor, events, sync: { syncNow: async () => { synced++; } },
  });
  await sleep(20);

  assert.equal(counts.at(-1), 3);
  assert.deepEqual(drawn.at(-1), ["s3", "s2", "s1"], "people, plugins, then AI");
  assert.deepEqual(host.querySelectorAll(".ms-suggestion").map((row) => row.getAttribute("data-suggestion-id")), ["s3", "s2", "s1"]);
  assert.match(textOf(host.querySelector('[data-suggestion-id="s3"]')), /Ana Lima · 1 edit/);
  assert.match(textOf(host.querySelector('[data-suggestion-id="s2"]')), /wiki · 1 edit/);
  assert.match(textOf(host.querySelector('[data-suggestion-id="s1"]')), /AI · 2 edits/);
  assert.match(textOf(host.querySelector('[data-suggestion-id="s1"]')), /Shorter sentences/);

  click(host.querySelector('[data-suggestion-id="s1"] button.ms-suggestion-title'));
  assert.deepEqual(revealed, ["s1"]);
  assert.ok(host.querySelector('[data-suggestion-id="s1"]').classList.contains("is-current"));

  click(buttonNamed(host.querySelector('[data-suggestion-id="s2"]'), "Accept all"));
  await sleep(20);
  const accept = api.calls.find((c) => c.method === "POST" && c.url === "/api/suggestions/s2/accept");
  assert.deepEqual(accept && accept.body, { editIds: ["e3"] });
  assert.equal(synced, 1);
  assert.deepEqual(drawn.at(-1), ["s3", "s1"]);
  assert.equal(counts.at(-1), 2);

  // An event about another note does not reload.
  const loads = () => api.calls.filter((c) => c.url.includes("/suggestions?status=open")).length;
  const before = loads();
  handlers["suggestion.created"]({ type: "suggestion.created", noteId: "other" });
  await sleep(200);
  assert.equal(loads(), before);

  // An event that names no note reloads the list and publishes it again.
  served = served.filter((s) => s.id === "s3");
  handlers["suggestion.accepted"]({ type: "suggestion.accepted", data: { suggestionId: "s1" } });
  await sleep(200);
  assert.equal(loads(), before + 1);
  assert.deepEqual(drawn.at(-1), ["s3"]);
  assert.equal(counts.at(-1), 1);

  handle.unmount();
  assert.deepEqual(Object.keys(handlers), []);
});

test("the Suggestions tab offers no Accept or Discard below edit access", async () => {
  const api = fakeApi([[/suggestions\?status=open$/, { items: [{ id: "s1", authorKind: "ai", title: "Tighten", status: "open", edits: [{ id: "e1" }] }] }]]);
  const host = mount();
  const handle = panels.openSuggestions(host, { note: { id: "n1" }, access: "comment", api, isOnline: () => true });
  await sleep(20);
  assert.ok(host.querySelector('[data-suggestion-id="s1"]'));
  assert.equal(buttonNamed(host, "Accept all"), null);
  assert.equal(buttonNamed(host, "Discard"), null);
  handle.unmount();
});

test("folder panels call the item routes and note panels the note routes", async () => {
  const api = fakeApi([
    [/^GET \/api\/items\/f1\/shares$/, { items: [{ id: "sh1", userId: "u2", access: "view", includeChildren: true, user: { name: "Ana Lima", email: "ana@example.com" } }] }],
    [/^GET \/api\/items\/f1\/links$/, { items: [{ id: "l1", token: "tok123", access: "view", views: 2 }] }],
    [/^GET \/api\/items\/f1\/comments$/, { items: [{ id: "c1", body: "Looks right", userId: "u2", author: { name: "Ana Lima" }, createdAt: "2026-09-13T10:00:00Z" }] }],
    [/^GET \/api\/files\/f1\/versions$/, { items: [] }],
    [/^GET \/api\/notes\/n1\/shares$/, { items: [] }],
    [/^GET \/api\/notes\/n1\/links$/, { items: [] }],
  ]);
  const base = { item: { id: "f1", spaceId: "sp" }, access: "manage", api, user: { id: "u1" }, isOnline: () => true };

  const share = mount();
  filepanels.openItemShare(share, { ...base, kind: "folder" });
  await sleep(20);
  assert.ok(share.querySelector('[data-share-id="sh1"]'), "the person row shows");
  assert.match(textOf(share.querySelector('[data-share-id="sh1"]')), /Includes everything inside/);
  assert.ok(share.querySelector('[data-link-id="l1"] input').value.endsWith("/p/tok123"));
  assert.match(textOf(share), /Include everything inside/);

  const comments = mount();
  filepanels.openItemComments(comments, { ...base, kind: "folder" });
  await sleep(20);
  assert.match(textOf(comments.querySelector(".ms-thread")), /Looks right/);

  const history = mount();
  filepanels.openFileHistory(history, { ...base, kind: "file" });
  await sleep(20);
  assert.match(textOf(history), /No earlier versions/);

  const urls = api.calls.map((c) => c.url);
  for (const want of ["/api/items/f1/shares", "/api/items/f1/links", "/api/items/f1/comments", "/api/files/f1/versions"]) {
    assert.ok(urls.includes(want), "called " + want);
  }
  assert.ok(!urls.some((u) => u.startsWith("/api/notes/")), "no note route for a folder: " + urls.join(", "));

  const noteShare = mount();
  panels.openShare(noteShare, { note: { id: "n1" }, access: "manage", api, isOnline: () => true });
  await sleep(20);
  assert.ok(api.calls.some((c) => c.url === "/api/notes/n1/shares"));
  assert.match(textOf(noteShare), /Include child pages/);
});

test("a resolved thread shows its passage, who resolved it and Reopen, and never Undo", async () => {
  const content = "Alpha beta gamma.";
  const base = { userId: "u2", author: { name: "Ana Lima" }, createdAt: "2026-09-13T10:00:00Z", updatedAt: "2026-09-13T11:00:00Z" };
  let resolved = true;
  const threads = () => [
    { ...base, id: "r1", body: "Done", resolved, resolvedBy: "u1", anchor: { exact: "beta", prefix: "Alpha ", suffix: " gamma.", start: 6, end: 10 }, anchorState: "exact" },
    { ...base, id: "r2", body: "Old", resolved: true, resolvedBy: "u2", anchor: { exact: "zeta words", prefix: "", suffix: "", start: 0, end: 10 }, anchorState: "orphaned" },
  ];
  const api = fakeApi([
    [/^GET \/api\/notes\/n1\/comments$/, () => ({ items: threads() })],
    [/^POST \/api\/comments\/r1\/resolve$/, (body) => { resolved = body.resolved; return threads()[0]; }],
  ]);
  const revealed = [];
  const host = mount();
  const handle = panels.openComments(host, {
    note: { id: "n1", spaceId: "sp", content }, access: "comment", api, user: { id: "u1" }, isOnline: () => true, onReveal: (id) => revealed.push(id),
  });
  await sleep(20);
  const card = (id) => host.querySelector(`.ms-thread[data-comment-id="${id}"]`);
  assert.ok(card("r1") && card("r1").classList.contains("is-resolved"), "the resolved thread renders");
  assert.ok(!card("r1").classList.contains("box"), "a thread has no box");

  const quote = card("r1").querySelector("button.ms-quote");
  assert.match(textOf(quote), /beta/);
  click(quote);
  assert.deepEqual(revealed, ["r1"]);
  assert.match(textOf(card("r1").querySelector(".ms-resolved-event")), /^Resolved by you, /);
  assert.match(textOf(card("r2").querySelector(".ms-resolved-event")), /^Resolved by Ana Lima, /);
  const gone = card("r2").querySelector(".ms-quote.is-missing");
  assert.match(textOf(gone), /Text no longer in the note/);
  assert.equal(card("r2").querySelector("button.ms-quote"), null, "a passage no longer in the note is not a button");
  assert.ok(buttonNamed(card("r1"), "Reopen"));
  assert.doesNotMatch(textOf(host), /Undo|Reopen thread/);

  await handle.reload();
  await sleep(20);
  assert.doesNotMatch(textOf(host), /Undo/, "a render shows no Undo on a resolved thread");

  click(buttonNamed(card("r1"), "Reopen"));
  await sleep(30);
  assert.deepEqual(api.calls.filter((c) => c.method === "POST").map((c) => c.body), [{ resolved: false }]);
  assert.ok(!card("r1").classList.contains("is-resolved"), "the thread moves back to the open threads");
  assert.equal(buttonNamed(card("r1"), "Reopen"), null);
  handle.unmount();
});

test("a plugin panel renders its tree and handles each command response", async () => {
  const updates = [];
  const navigated = [];
  const opened = [];
  let rendered = null;
  let onAction = null;
  let destroyed = 0;
  const renderer = {
    renderPluginUI(container, tree, options) {
      rendered = { tree, context: options.context };
      onAction = options.onAction;
      container.appendChild(document.createElement("p"));
      return { update: (next) => updates.push(next), destroy: () => destroyed++ };
    },
  };
  const responses = {
    toast: { toast: { message: "Saved 3 links", kind: "success" } },
    go: { navigate: "/s/demo/n/links" },
    job: { job: { id: "j1" } },
    redraw: { ui: { type: "text", value: "Updated" } },
    open: { openPanel: { id: "wiki.other", itemId: "n1" } },
  };
  const api = fakeApi([
    [/^POST \/api\/panels\/wiki\.links$/, { ui: { type: "text", value: "Hello" } }],
    [/^POST \/api\/modules\/wiki\/actions\/fail$/, () => { throw Object.assign(new Error("the url is not valid"), { status: 422 }); }],
    [/^POST \/api\/modules\/wiki\/actions\/(\w+)$/, (_body, url) => responses[url.split("/").pop()]],
  ]);
  const host = mount();
  const handle = panels.openPluginPanel(host, {
    panelId: "wiki.links", itemId: "n1", kind: "note", title: "Links", api, isOnline: () => true,
    navigate: (url) => navigated.push(url), openPanel: (id, extra) => opened.push([id, extra]), loadRenderer: async () => renderer,
  });
  await sleep(20);
  assert.deepEqual(rendered.tree, { type: "text", value: "Hello" });
  assert.deepEqual(rendered.context, { itemId: "n1", kind: "note" });
  assert.deepEqual(api.calls[0].body, { context: { itemId: "n1", kind: "note" } });
  assert.match(textOf(host.querySelector(".ms-panel-title")), /Links/);

  await onAction("toast", { id: 7 }, { url: "https://example.com" });
  assert.deepEqual(api.calls.at(-1).body, { context: { itemId: "n1", kind: "note" }, values: { url: "https://example.com" }, payload: { id: 7 } });
  // renderPluginUI shows a toast in the plugin view, so the panel adds no second Banner.
  assert.equal(host.querySelector(".ms-panel-messages .banner-success"), null, "the panel does not repeat the toast Banner");

  await onAction("go");
  assert.deepEqual(navigated, ["/s/demo/n/links"]);

  await onAction("job");
  const progress = host.querySelector('.ms-panel-messages a[href="/jobs"]');
  assert.ok(progress, "the job Banner links to the job list");
  assert.equal(host.querySelector(".ms-panel-messages .banner-success"), null, "the job Banner replaces the toast Banner");

  await onAction("redraw");
  assert.deepEqual(updates, [{ type: "text", value: "Updated" }]);

  await onAction("open");
  assert.deepEqual(opened, [["wiki.other", { itemId: "n1" }]]);

  await assert.rejects(onAction("fail"));
  assert.match(textOf(host.querySelector(".ms-panel-messages .banner-critical")), /The url is not valid/);

  handle.unmount();
  assert.equal(destroyed, 1);
});

test("a plugin panel without plugins.js shows a Blankslate and fetches nothing", async () => {
  const api = fakeApi([]);
  const host = mount();
  panels.openPluginPanel(host, {
    panelId: "wiki.links", itemId: "n1", api, isOnline: () => true,
    loadRenderer: async () => { throw new Error("404 plugins.js"); },
  });
  await sleep(20);
  assert.match(textOf(host.querySelector(".blankslate")), /This panel could not load/);
  assert.equal(api.calls.length, 0);
});
