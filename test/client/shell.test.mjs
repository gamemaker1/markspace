// Tests for the pure functions of the client shell, its views and ui.js.
import { test } from "node:test";
import assert from "node:assert/strict";

import {
  accessAtLeast,
  ancestorIds,
  attachmentLinks,
  buildBreadcrumbs,
  descendantIds,
  expandToNote,
  filesUrl,
  indexById,
  initialsOf,
  interceptTarget,
  matchCommands,
  makeLinkResolver,
  moveIndex,
  nextPosition,
  noteAccess,
  noteHref,
  noteUrl,
  parseRoute,
  permalinkUrl,
  railFromContributions,
  readExpanded,
  searchUrl,
  spaceAccess,
  spaceUrl,
  stripMd,
  syncLabel,
  tagUrl,
  timeAgo,
  toastVariant,
  toggleExpanded,
  toggleTask,
  trashUrl,
  wordCount,
  writeExpanded,
} from "../../internal/web/static/js/shell.js";
import { attachmentKind, byPosition, byUpdated, collapseCrumbs, filterAttachments, groupTags, parseMarked } from "../../internal/web/static/js/views.js";
import { acceptsFile, fileIconName, formatBytes, uploadStatus } from "../../internal/web/static/js/ui.js";

/** A localStorage stand-in. */
function memoryStorage() {
  const data = new Map();
  return {
    getItem: (key) => (data.has(key) ? data.get(key) : null),
    setItem: (key, value) => data.set(key, String(value)),
    raw: data,
  };
}

test("parseRoute matches every shell route", () => {
  const cases = [
    ["/", "", { name: "home", params: {} }],
    ["/s/docs", "", { name: "space", params: { space: "docs" } }],
    ["/s/docs/", "", { name: "space", params: { space: "docs" } }],
    ["/s/docs/n/a/b", "", { name: "note", params: { space: "docs", path: "a/b", edit: false } }],
    ["/s/docs/n/a/b.md", "?edit=1", { name: "note", params: { space: "docs", path: "a/b", edit: true } }],
    ["/s/docs/n/getting%20started", "", { name: "note", params: { space: "docs", path: "getting started", edit: false } }],
    ["/s/docs/t/go", "", { name: "tag", params: { space: "docs", tag: "go" } }],
    ["/s/docs/trash", "", { name: "trash", params: { space: "docs" } }],
    ["/s/docs/files", "", { name: "files", params: { space: "docs" } }],
    ["/n/06g9e9w2yqk8cgb6a2kk75r2n8", "", { name: "permalink", params: { id: "06g9e9w2yqk8cgb6a2kk75r2n8", edit: false } }],
    ["/search", "?q=hello&space=docs", { name: "search", params: { q: "hello", space: "docs", mode: "" } }],
    ["/starred", "", { name: "starred", params: {} }],
    ["/recent", "", { name: "recent", params: {} }],
    ["/shared", "", { name: "shared", params: {} }],
  ];
  for (const [path, search, want] of cases) {
    assert.deepEqual(parseRoute(path, search), want, path + search);
  }
});

test("parseRoute leaves server pages to the server", () => {
  for (const path of ["/login", "/signup", "/setup", "/spaces", "/s/docs/settings", "/settings/profile", "/admin", "/admin/users", "/p/token", "/s/docs/n/", "/s/%E0%A4%A/n/x", "/app-shell", "/offline", "/s/docs/t/"]) {
    assert.equal(parseRoute(path), null, path);
  }
});

test("an empty edit parameter does not open the editor, like the server", () => {
  assert.equal(parseRoute("/s/docs/n/a", "?edit=").params.edit, false);
  assert.equal(parseRoute("/s/docs/n/a", "?edit=true").params.edit, true);
});

test("URL builders round-trip through parseRoute", () => {
  assert.equal(spaceUrl("docs"), "/s/docs");
  assert.equal(noteUrl("docs", "guide/getting started.md"), "/s/docs/n/guide/getting%20started");
  assert.equal(noteUrl("docs", "a/b", { edit: true }), "/s/docs/n/a/b?edit=1");
  assert.equal(tagUrl("docs", "c#"), "/s/docs/t/c%23");
  assert.equal(trashUrl("docs"), "/s/docs/trash");
  assert.equal(filesUrl("docs"), "/s/docs/files");
  assert.equal(permalinkUrl("abc", { edit: true }), "/n/abc?edit=1");
  assert.equal(searchUrl({ q: "a b", mode: "hybrid" }), "/search?q=a+b&mode=hybrid");
  assert.equal(searchUrl(), "/search");
  const url = new URL(noteUrl("my space", "x/y z.md"), "https://example.com");
  assert.deepEqual(parseRoute(url.pathname, url.search).params, { space: "my space", path: "x/y z", edit: false });
  assert.equal(stripMd("a/b.MD"), "a/b");
});

test("noteHref prefers the canonical path and falls back to the permalink", () => {
  assert.equal(noteHref({ id: "n1", spaceSlug: "docs", path: "a.md" }), "/s/docs/n/a");
  assert.equal(noteHref({ id: "n1", path: "a.md" }, "other"), "/s/other/n/a");
  assert.equal(noteHref({ id: "n1" }, "docs"), "/n/n1");
  assert.equal(noteHref({ id: "n1", path: "a.md" }, "docs", { edit: true }), "/s/docs/n/a?edit=1");
});

test("interceptTarget routes plain clicks on shell links only", () => {
  const origin = "https://notes.example.com";
  const click = { button: 0 };
  assert.equal(interceptTarget(click, { href: origin + "/s/docs/n/a?edit=1#x" }, origin), "/s/docs/n/a?edit=1#x");
  assert.equal(interceptTarget(click, { href: "/starred" }, origin), "/starred");
  assert.equal(interceptTarget(click, { href: origin + "/settings/profile" }, origin), null);
  assert.equal(interceptTarget(click, { href: "https://other.example.com/s/docs" }, origin), null);
  assert.equal(interceptTarget({ button: 0, metaKey: true }, { href: "/s/docs" }, origin), null);
  assert.equal(interceptTarget({ button: 1 }, { href: "/s/docs" }, origin), null);
  assert.equal(interceptTarget({ button: 0, defaultPrevented: true }, { href: "/s/docs" }, origin), null);
  assert.equal(interceptTarget(click, { href: "/s/docs", target: "_blank" }, origin), null);
  assert.equal(interceptTarget(click, { href: "/s/docs", download: true }, origin), null);
  assert.equal(interceptTarget(click, { href: "/s/docs", fullNav: true }, origin), null);
  assert.equal(interceptTarget(click, { href: "/files/docs/a1/x.png" }, origin), null);
});

test("access levels compare and fall back to the space role", () => {
  assert.equal(accessAtLeast("manage", "edit"), true);
  assert.equal(accessAtLeast("comment", "edit"), false);
  assert.equal(accessAtLeast(undefined, "view"), false);
  assert.equal(spaceAccess({ myRole: "editor" }, {}), "edit");
  assert.equal(spaceAccess({ myRole: "viewer" }, { isAdmin: true }), "manage");
  assert.equal(spaceAccess(null, null), "none");
  assert.equal(noteAccess({ access: "comment" }, { myRole: "owner" }, {}), "comment");
  assert.equal(noteAccess({}, { myRole: "owner" }, {}), "manage");
});

const notes = [
  { id: "root", title: "Guide", path: "guide.md", position: 200 },
  { id: "child", title: "Install", path: "guide/install.md", parentId: "root", position: 100 },
  { id: "leaf", title: "Docker", path: "guide/install/docker.md", parentId: "child" },
  { id: "other", title: "Other", path: "other.md", position: 100 },
];

test("breadcrumbs walk the parents by title", () => {
  const byId = indexById(notes);
  const space = { slug: "docs", name: "Docs" };
  assert.deepEqual(buildBreadcrumbs({ space, note: byId.get("leaf"), byId }), [
    { label: "Docs", href: "/s/docs" },
    { label: "Guide", href: "/s/docs/n/guide" },
    { label: "Install", href: "/s/docs/n/guide/install" },
    { label: "Docker", href: null },
  ]);
  assert.deepEqual(buildBreadcrumbs({ space, note: null, byId }), [{ label: "Docs", href: "/s/docs" }]);
});

test("ancestor walks stop on cycles and missing parents", () => {
  const byId = indexById([
    { id: "a", parentId: "b" },
    { id: "b", parentId: "a" },
    { id: "c", parentId: "gone" },
  ]);
  assert.deepEqual(ancestorIds("a", byId), ["b"]);
  assert.deepEqual(ancestorIds("c", byId), ["gone"]);
  assert.deepEqual(Array.from(descendantIds("root", notes)).sort(), ["child", "leaf", "root"]);
  assert.equal(nextPosition(notes.filter((n) => !n.parentId)), 300);
  assert.equal(nextPosition([]), 100);
});

test("tree expand state persists in the server page format", () => {
  const storage = memoryStorage();
  assert.equal(readExpanded(storage, "sp1").size, 0);
  let open = toggleExpanded(new Set(), "root");
  open = toggleExpanded(open, "child", true);
  writeExpanded(storage, "sp1", open);
  assert.equal(storage.raw.get("markspace.tree.sp1"), '{"root":true,"child":true}');
  assert.deepEqual(Array.from(readExpanded(storage, "sp1")), ["root", "child"]);
  open = toggleExpanded(open, "root");
  assert.deepEqual(Array.from(open), ["child"]);
  assert.deepEqual(Array.from(expandToNote(new Set(), "leaf", indexById(notes))).sort(), ["child", "root"]);
  storage.setItem("markspace.tree.bad", "{not json");
  assert.equal(readExpanded(storage, "bad").size, 0);
});

test("syncLabel describes each state", () => {
  const now = Date.parse("2026-09-13T10:00:00Z");
  const base = { online: true, syncing: false, pending: 0, lastSyncAt: now - 5 * 60000, lastError: null, authRequired: false };
  assert.deepEqual(syncLabel(base, now), { state: "synced", text: "Synced", title: "Last synced 5 minutes ago" });
  assert.equal(syncLabel({ ...base, syncing: true }, now).text, "Syncing");
  assert.equal(syncLabel({ ...base, online: false, pending: 3 }, now).text, "Offline, 3 changes waiting");
  assert.equal(syncLabel({ ...base, online: false, pending: 1 }, now).text, "Offline, 1 change waiting");
  assert.equal(syncLabel({ ...base, online: false }, now).text, "Offline");
  assert.equal(syncLabel({ ...base, pending: 2 }, now).text, "2 changes waiting");
  assert.equal(syncLabel({ ...base, authRequired: true, online: false }, now).text, "Sign in to sync");
  assert.equal(syncLabel({ ...base, lastSyncAt: null }, now).title, "Not synced yet");
  assert.match(syncLabel({ ...base, pending: 1, lastError: { message: "boom" } }, now).title, /Last error: boom/);
  assert.equal(syncLabel(null).state, "offline");
});

test("timeAgo uses coarse units", () => {
  const now = Date.parse("2026-09-13T10:00:00Z");
  assert.equal(timeAgo(now - 10000, now), "just now");
  assert.equal(timeAgo(now - 60000, now), "1 minute ago");
  assert.equal(timeAgo(new Date(now - 3 * 3600000).toISOString(), now), "3 hours ago");
  assert.equal(timeAgo(now - 3 * 86400000, now), "3 days ago");
  assert.equal(timeAgo(now + 2 * 3600000, now), "in 2 hours");
  assert.equal(timeAgo("", now), "");
  assert.equal(timeAgo("not a date", now), "");
});

test("toggleTask edits the right checkbox and skips code fences", () => {
  const md = "- [ ] one\n```\n- [ ] not a task\n```\n  * [x] two\n1. [ ] three\n> - [ ] four";
  assert.equal(toggleTask(md, 0, true), md.replace("- [ ] one", "- [x] one"));
  assert.equal(toggleTask(md, 1, false), md.replace("* [x] two", "* [ ] two"));
  assert.equal(toggleTask(md, 2, true), md.replace("1. [ ] three", "1. [x] three"));
  assert.equal(toggleTask(md, 3, true), md.replace("> - [ ] four", "> - [x] four"));
  assert.equal(toggleTask(md, 9, true), md);
});

test("the link resolver matches title, slug and path", () => {
  const resolve = makeLinkResolver(
    [
      { id: "1", title: "Getting Started", slug: "getting-started", path: "getting-started.md" },
      { id: "2", title: "Docker", slug: "docker", path: "guide/install/docker.md" },
      { id: "3", title: "Gone", slug: "gone", path: "gone.md", deleted: true },
    ],
    "docs"
  );
  assert.deepEqual(resolve("getting started"), { href: "/s/docs/n/getting-started", exists: true });
  assert.deepEqual(resolve("docker#run"), { href: "/s/docs/n/guide/install/docker#run", exists: true });
  assert.deepEqual(resolve("guide/install/docker"), { href: "/s/docs/n/guide/install/docker", exists: true });
  assert.deepEqual(resolve("Gone"), { href: "#", exists: false });
  assert.deepEqual(resolve("missing"), { href: "#", exists: false });
});

test("small text helpers", () => {
  assert.equal(wordCount("# Title\n\nTwo words - [x] done"), 5);
  assert.equal(wordCount(""), 0);
  assert.equal(moveIndex(-1, 1, 3), 0);
  assert.equal(moveIndex(-1, -1, 3), 2);
  assert.equal(moveIndex(2, 1, 3), 0);
  assert.equal(moveIndex(0, -1, 3), 2);
  assert.equal(moveIndex(0, 1, 0), -1);
  assert.equal(initialsOf({ name: "Ada Lovelace" }), "AL");
  assert.equal(initialsOf({ email: "mel@example.com" }), "ME");
  assert.equal(initialsOf(null), "?");
  assert.deepEqual(attachmentLinks("![a](/files/docs/1/a.png) [b.pdf](/files/docs/2/b.pdf) ![](markspace-pending:k1) [x](https://e.com)"), [
    { url: "/files/docs/1/a.png", name: "a", pending: false },
    { url: "/files/docs/2/b.pdf", name: "b.pdf", pending: false },
    { url: "markspace-pending:k1", name: "markspace-pending:k1", pending: true },
  ]);
});

test("view helpers group, sort and filter", () => {
  assert.deepEqual(groupTags([{ tags: ["b", "a"] }, { tags: ["a", "a"] }, { tags: ["c"], deleted: true }]), [
    { tag: "a", count: 2 },
    { tag: "b", count: 1 },
  ]);
  assert.deepEqual(byPosition(notes.filter((n) => !n.parentId)).map((n) => n.id), ["other", "root"]);
  assert.deepEqual(byUpdated([{ id: "a", updatedAt: "2026-01-01" }, { id: "b", updatedAt: "2026-02-01" }]).map((n) => n.id), ["b", "a"]);
  assert.equal(attachmentKind("image/png"), "image");
  assert.equal(attachmentKind("application/pdf"), "document");
  assert.equal(attachmentKind("", "notes.MD"), "document");
  assert.equal(attachmentKind("application/zip"), "archive");
  assert.equal(attachmentKind("application/octet-stream", "x.bin"), "other");
  const files = [
    { name: "Photo.PNG", mime: "image/png" },
    { name: "report.pdf", mime: "application/pdf" },
  ];
  assert.deepEqual(filterAttachments(files, { query: "photo" }).map((f) => f.name), ["Photo.PNG"]);
  assert.deepEqual(filterAttachments(files, { kind: "document" }).map((f) => f.name), ["report.pdf"]);
  assert.equal(filterAttachments(files, {}).length, 2);
});

test("parseMarked splits server snippets without HTML", () => {
  assert.deepEqual(parseMarked("a &lt;b&gt; <mark>hit</mark> &amp; <em>x</em>"), [
    { text: "a <b> ", mark: false },
    { text: "hit", mark: true },
    { text: " & x", mark: false },
  ]);
  assert.deepEqual(parseMarked(""), []);
});

test("ui helpers describe files and uploads", () => {
  assert.equal(formatBytes(512), "512 B");
  assert.equal(formatBytes(2048), "2.0 KB");
  assert.equal(formatBytes(50 * 1024 * 1024), "50 MB");
  assert.equal(acceptsFile({ name: "a.PNG", type: "image/png" }, "image/*"), true);
  assert.equal(acceptsFile({ name: "a.md", type: "" }, ".zip,.md"), true);
  assert.equal(acceptsFile({ name: "a.exe", type: "application/x-msdownload" }, "image/*,.pdf"), false);
  assert.equal(acceptsFile({ name: "a", type: "" }, ""), true);
  assert.equal(fileIconName("video/mp4"), "video");
  assert.equal(fileIconName("", "backup.zip"), "archive");
  assert.deepEqual(uploadStatus({ state: "uploading", loaded: 50, size: 200 }), { text: "Uploading 25%", percent: 25, showBar: true });
  assert.equal(uploadStatus({ state: "queued" }, false).text, "Waiting for a connection");
  assert.equal(uploadStatus({ state: "failed", error: "too large" }).text, "Failed: too large");
  assert.equal(uploadStatus({ state: "done", size: 10 }).percent, 100);
});

test("command responses, the command palette and contributed rail panels", () => {
  assert.equal(toastVariant("error"), "critical");
  assert.equal(toastVariant("warn"), "warning");
  assert.equal(toastVariant("success"), "success");
  assert.equal(toastVariant("info"), "info");
  assert.equal(toastVariant(undefined), "info");

  const commands = [
    { id: "wiki.rebuild", title: "Rebuild wiki" },
    { id: "wiki.import", title: "Import bookmarks" },
    { id: "ai.summarise", title: "Summarise page" },
    { id: "bad", title: "" },
  ];
  assert.deepEqual(matchCommands(commands, "").commands, [], "no query lists no commands among pages");
  const all = matchCommands(commands, "> ");
  assert.equal(all.commandMode, true);
  assert.deepEqual(all.commands.map((c) => c.id), ["wiki.import", "wiki.rebuild", "ai.summarise"], "command mode lists every titled command");
  assert.deepEqual(matchCommands(commands, "ai.").commands.map((c) => c.id), [], "titles match, not ids");
  assert.deepEqual(matchCommands(commands, "re").commands.map((c) => c.id), ["wiki.rebuild"]);
  assert.deepEqual(matchCommands(commands, ">page summ").commands.map((c) => c.id), ["ai.summarise"], "every word must match");

  assert.equal(railFromContributions(null, ["outline"]), null);
  assert.equal(railFromContributions([{ id: "x", where: "page" }], ["outline"]), null);
  const plan = railFromContributions(
    [
      { id: "ui.info", where: "rail", builtin: "info", order: 70 },
      { id: "wiki.pages", where: "rail", title: "Wiki", order: 90 },
      { id: "ui.outline", where: "rail", builtin: "outline", order: 10 },
      { id: "suggestions.list", where: "rail", builtin: "suggestions", order: 20 },
    ],
    ["outline", "info"]
  );
  assert.deepEqual(plan.builtin, ["outline", "info"], "built-in panels the client cannot render are left out");
  assert.deepEqual(plan.plugin.map((p) => p.id), ["wiki.pages"]);
});

test("breadcrumbs longer than four items collapse their middle items", () => {
  const crumb = (label) => ({ label, href: "/" + label });
  assert.deepEqual(collapseCrumbs([crumb("a"), crumb("b"), crumb("c")]).hidden, []);
  const long = collapseCrumbs(["a", "b", "c", "d", "e", "f"].map(crumb));
  assert.deepEqual(long.visible.map((c) => c.label), ["a", "e", "f"]);
  assert.deepEqual(long.hidden.map((c) => c.label), ["b", "c", "d"]);
});

import { visibleViewport, paneOffset } from "../../internal/web/static/js/shell.js";

test("the rail sizes to the visual viewport, so the keyboard shortens it", () => {
  assert.deepEqual(visibleViewport({ innerHeight: 844, visualViewport: null }), { height: 844, top: 0 });
  assert.deepEqual(visibleViewport({ innerHeight: 844, visualViewport: { height: 508, offsetTop: 0, scale: 1 } }), { height: 508, top: 0 });
  assert.deepEqual(visibleViewport({ innerHeight: 390, visualViewport: { height: 190.4, offsetTop: 12.2, scale: 1 } }), { height: 190, top: 12 });
  // A pinch zoom keeps the layout viewport height.
  assert.deepEqual(visibleViewport({ innerHeight: 844, visualViewport: { height: 422, offsetTop: 200, scale: 2 } }), { height: 844, top: 0 });
  // The header on screen and the layout ending above the viewport bottom both shorten the pane.
  assert.equal(paneOffset({ top: 64, bottom: 1909 }, 390), 64);
  assert.equal(paneOffset({ top: -1519, bottom: 390 }, 390), 0);
  assert.equal(paneOffset({ top: -1600, bottom: 300 }, 390), 90);
  assert.equal(paneOffset({ top: 64, bottom: 1909 }, 190), 64);
});

import { SYNC_BADGE_ICONS, syncDescription, syncLabel as syncLabelOf } from "../../internal/web/static/js/shell.js";

test("the Avatar badge shows a tick, a dot or an alert, and the menu one status line", () => {
  const now = Date.parse("2026-09-13T12:00:00Z");
  const synced = syncLabelOf({ online: true, pending: 0, lastSyncAt: "2026-09-13T11:58:00Z" }, now);
  assert.equal(SYNC_BADGE_ICONS[synced.state], "check");
  assert.equal(syncDescription(synced, { pending: 0 }), "All changes synced");
  assert.equal(syncDescription(syncLabelOf({ online: true, pending: 3 }, now), { pending: 3 }), "3 not synced");
  assert.equal(syncDescription(syncLabelOf({ online: true, syncing: true, pending: 1 }, now), { pending: 1 }), "Syncing 1 change");
  assert.equal(syncDescription(syncLabelOf({ online: false }, now), { pending: 0 }), "Offline");
  assert.equal(SYNC_BADGE_ICONS[syncLabelOf({ online: true, pending: 2 }, now).state], undefined);
  assert.equal(SYNC_BADGE_ICONS[syncLabelOf({ online: true, syncing: true }, now).state], undefined);
  assert.equal(SYNC_BADGE_ICONS[syncLabelOf({ online: false }, now).state], "alert");
  assert.equal(SYNC_BADGE_ICONS[syncLabelOf({ online: true, authRequired: true }, now).state], "alert");
  assert.equal(SYNC_BADGE_ICONS[syncLabelOf({ online: true, pending: 1, lastError: "boom" }, now).state], "alert");
});
