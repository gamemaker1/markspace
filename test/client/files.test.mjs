// Tests for the file tree, the file views and the new shell routes. The tree
// runs on the fake DOM in fakedom.mjs.
import { test, beforeEach } from "node:test";
import assert from "node:assert/strict";
import { installDOM, FakeEvent } from "./fakedom.mjs";

installDOM();
globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };
const ui = await import("../../internal/web/static/js/ui.js");
const tree = await import("../../internal/web/static/js/filetree.js");
const views = await import("../../internal/web/static/js/fileviews.js");
const shell = await import("../../internal/web/static/js/shell.js");

const sleep = (ms = 0) => new Promise((resolve) => setTimeout(resolve, ms));

beforeEach(() => {
  installDOM();
});

/** Presses a key on an element, with modifier flags. */
function press(el, key, mods = {}) {
  const event = new FakeEvent("keydown", { bubbles: true, key });
  Object.assign(event, { ctrlKey: false, metaKey: false, shiftKey: false, altKey: false }, mods);
  el.dispatchEvent(event);
  return event;
}

const folder = (id, path, parentId = "") => ({ id, name: path.split("/").pop(), path, parentId, spaceId: "s1" });
const file = (id, path, parentId = "", mime = "application/pdf") => ({ id, name: path.split("/").pop(), path, parentId, mime, spaceId: "s1", size: 10 });
const note = (id, title, path, parentId = "") => ({ id, title, path, parentId, spaceId: "s1" });

/** Builds a tree with a folder holding a file, an empty folder, a note and a file at the root. */
function setupTree() {
  const calls = [];
  const app = {
    hrefFor: (kind, rec) => `/${kind}/${rec.id}`,
    itemAccess: () => "edit",
    sync: {
      renameItem: async (id, name) => calls.push(["rename", id, name]),
      trashItem: async (id) => calls.push(["trash", id]),
      restoreItem: async (id) => calls.push(["restore", id]),
      copyItem: async (id, target) => calls.push(["copy", id, target.parentId]),
      moveItem: async (id, parentId) => calls.push(["move", id, parentId]),
    },
    store: { getItem: async () => null },
    view: null,
    clipboard: null,
    openMoveDialog: (mode, targets) => calls.push(["pick", mode, targets.map((t) => t.rec.id)]),
    renderTree: () => {},
    closeDrawer() {},
    navigate: async (url) => calls.push(["navigate", url]),
  };
  const host = ui.h("div", {});
  document.body.appendChild(host);
  const space = { id: "s1", slug: "docs", name: "Docs", myRole: "editor" };
  const expandedSeen = [];
  const ctl = tree.createFileTree({ app, host, space, onExpandedChange: (set) => expandedSeen.push([...set]) });
  const nodes = treeNodes();
  ctl.render(nodes, { expanded: new Set(), currentId: null });
  const li = (id) => host.querySelector(`[role=treeitem][data-id="${id}"]`);
  const content = (id) => li(id).children.find((c) => c.classList.contains("tree-view-item-content"));
  return { app, host, ctl, calls, li, content, expandedSeen, space, nodes };
}

/** The store tree used by the tree tests. */
function treeNodes() {
  return [
    { kind: "folder", folder: folder("f1", "Docs"), children: [{ kind: "file", file: file("x1", "Docs/a.pdf", "f1"), children: [] }] },
    { kind: "folder", folder: folder("f2", "Empty"), children: [] },
    { kind: "note", note: note("n1", "Readme", "readme.md"), children: [] },
    { kind: "file", file: file("x2", "b.png", "", "image/png"), children: [] },
  ];
}

/** Clicks an element with modifier flags. */
function clickWith(el, mods = {}) {
  const e = new FakeEvent("click", { bubbles: true });
  Object.assign(e, { ctrlKey: false, metaKey: false, shiftKey: false }, mods);
  el.dispatchEvent(e);
  return e;
}

test("pure tree helpers", () => {
  const rows = [{ id: "a" }, { id: "b" }, { id: "c" }, { id: "d" }];
  assert.deepEqual(tree.selectRange(rows, "d", "b"), ["b", "c", "d"]);
  assert.deepEqual(tree.selectRange(rows, "zz", "b"), ["b"]);
  assert.deepEqual(tree.selectRange(rows, "a", "zz"), []);
  assert.equal(tree.folderFor({ kind: "folder", rec: { id: "f" } }), "f");
  assert.equal(tree.folderFor({ kind: "file", rec: { id: "x", parentId: "f" } }), "f");
  assert.equal(tree.folderFor(null), "");
  const folders = [folder("a", "A"), folder("b", "A/B"), folder("c", "AB"), folder("d", "A/B/C")];
  assert.deepEqual([...tree.blockedFolders([{ kind: "folder", rec: folders[0] }, { kind: "file", rec: file("x", "AB/x") }], folders)].sort(), ["a", "b", "d"]);
  assert.equal(tree.moveTitle("move", ["one", "two", "three"]), "Move 3 items");
  assert.equal(tree.moveTitle("copy", ["report.pdf"]), "Copy “report.pdf”");
  assert.equal(tree.itemIcon("folder", {}), "file-directory-fill");
  assert.equal(tree.itemIcon("folder", {}, { open: true }), "file-directory-open-fill");
  assert.equal(tree.itemIcon("note", {}), "note");
  assert.equal(tree.itemIcon("file", { mime: "image/png" }), ui.fileIconName("image/png"));
  assert.equal(tree.itemLabel("note", { title: "" }), "Untitled");
  const nodes = [{ kind: "folder", folder: { id: "f" }, children: [{ kind: "file", file: { id: "x" }, children: [] }] }, { kind: "note", note: { id: "n" }, children: [] }];
  assert.deepEqual(tree.flattenVisible(nodes, new Set()).map((r) => r.id), ["f", "n"]);
  assert.deepEqual(tree.flattenVisible(nodes, new Set(["f"])).map((r) => [r.id, r.depth, r.parentId]), [["f", 1, ""], ["x", 2, "f"], ["n", 1, ""]]);
  const nested = [{ kind: "folder", folder: folder("p", "P"), children: [{ kind: "folder", folder: folder("q", "P/Q", "p"), children: [] }, { kind: "note", note: note("m", "M", "P/m.md", "p"), children: [] }] }, { kind: "note", note: note("r", "R", "r.md"), children: [] }];
  const picker = tree.folderPickerItems(nested, { open: new Set(["p"]) });
  assert.deepEqual(picker.map((i) => [i.id, i.expanded, i.children.map((c) => c.id)]), [["p", true, ["q"]]], "notes are not destinations");
  assert.deepEqual(tree.folderPickerItems(nested, { blocked: new Set(["p"]) }), [], "a blocked folder leaves with its subtree");
});

test("treeKeyAction maps Markspace commands and ignores text fields", () => {
  const ev = (key, mods = {}, target = null) => ({ key, ctrlKey: false, metaKey: false, shiftKey: false, altKey: false, target, ...mods });
  assert.deepEqual(tree.treeKeyAction(ev("F2")), { type: "rename" });
  assert.deepEqual(tree.treeKeyAction(ev("Delete")), { type: "trash" });
  assert.deepEqual(tree.treeKeyAction(ev("Backspace", { metaKey: true })), { type: "trash" });
  assert.deepEqual(tree.treeKeyAction(ev("c", { ctrlKey: true })), { type: "copy" });
  assert.deepEqual(tree.treeKeyAction(ev("X", { metaKey: true })), { type: "cut" });
  assert.deepEqual(tree.treeKeyAction(ev("v", { ctrlKey: true })), { type: "paste" });
  assert.deepEqual(tree.treeKeyAction(ev("F10", { shiftKey: true })), { type: "menu" });
  assert.deepEqual(tree.treeKeyAction(ev("ContextMenu")), { type: "menu" });
  assert.equal(tree.treeKeyAction(ev("ArrowDown")), null, "TreeView moves focus itself");
  assert.equal(tree.treeKeyAction(ev("c")), null);
  assert.equal(tree.treeKeyAction(ev("c", { ctrlKey: true, shiftKey: true })), null);
  const input = ui.h("input", {});
  assert.equal(tree.treeKeyAction(ev("Delete", {}, input)), null, "keys typed into a field are ignored");
});

test("the tree is a TreeView with one tab stop, no buttons in rows, and keys that move and expand", () => {
  const { host, ctl, li, expandedSeen } = setupTree();
  const view = host.querySelector("[role=tree]");
  assert.equal(view.getAttribute("aria-multiselectable"), "true");
  assert.deepEqual(ctl.state.rows.map((r) => r.id), ["f1", "f2", "n1", "x2"]);
  assert.equal(host.querySelectorAll("[role=treeitem][tabindex='0']").length, 1);
  assert.equal(li("f1").getAttribute("tabindex"), "0");
  assert.equal(host.querySelectorAll("button").length, 0, "rows hold no buttons");
  assert.equal(li("f2").hasAttribute("aria-expanded"), false, "an empty folder has no chevron");

  press(li("f1"), "ArrowDown");
  assert.equal(document.activeElement, li("f2"));
  press(li("f2"), "End");
  assert.equal(document.activeElement, li("x2"));
  press(li("x2"), "Home");
  assert.equal(document.activeElement, li("f1"));

  press(li("f1"), "ArrowRight");
  assert.equal(li("f1").getAttribute("aria-expanded"), "true");
  assert.deepEqual(ctl.state.rows.map((r) => r.id), ["f1", "x1", "f2", "n1", "x2"]);
  assert.deepEqual(expandedSeen.at(-1), ["f1"]);
  press(li("f1"), "ArrowRight");
  assert.equal(document.activeElement, li("x1"), "a second ArrowRight moves into the folder");
  press(li("x1"), "ArrowLeft");
  assert.equal(document.activeElement, li("f1"), "ArrowLeft on a child goes to its folder");
  press(li("f1"), "ArrowLeft");
  assert.equal(li("f1").getAttribute("aria-expanded"), "false");
});

test("Enter and a click open the item, and a render keeps focus on the same row", () => {
  const { ctl, calls, li, content, nodes } = setupTree();
  li("n1").focus();
  press(li("n1"), "Enter");
  clickWith(content("x2"));
  assert.deepEqual(calls, [["navigate", "/note/n1"], ["navigate", "/file/x2"]]);
  li("n1").focus();
  ctl.render(nodes, { currentId: "n1" });
  assert.equal(document.activeElement, li("n1"));
  assert.equal(li("n1").getAttribute("aria-current"), "true");
});

test("F2 renames in place and Enter saves", async () => {
  const { host, li, calls } = setupTree();
  press(li("n1"), "F2");
  const input = host.querySelector("input.edit-in-place");
  assert.ok(input, "the name became a text field");
  assert.equal(input.value, "Readme");
  input.value = "Guide";
  press(input, "Enter");
  await sleep(5);
  assert.deepEqual(calls, [["rename", "n1", "Guide"]]);
});

test("F2 on a file validates the name before saving", async () => {
  const { host, li, calls } = setupTree();
  press(li("x2"), "F2");
  const input = host.querySelector("input.edit-in-place");
  input.value = "a/b.png";
  press(input, "Enter");
  await sleep(5);
  assert.deepEqual(calls, []);
  const error = host.querySelector(".edit-in-place-error");
  assert.ok(error && !error.hidden && /slash/.test(error.textContent), "the error shows under the field");
});

test("Shift with arrows extends the selection and Delete trashes it with Undo", async () => {
  const { li, ctl, calls } = setupTree();
  li("f1").focus();
  press(li("f1"), "ArrowDown", { shiftKey: true });
  press(li("f2"), "ArrowDown", { shiftKey: true });
  assert.deepEqual(ctl.selection(), ["f1", "f2", "n1"]);
  assert.equal(li("f2").getAttribute("aria-selected"), "true");
  press(li("n1"), "Delete");
  await sleep(5);
  assert.deepEqual(calls, [["trash", "f1"], ["trash", "f2"], ["trash", "n1"]]);
  const note = document.querySelector("[data-ui-banner]");
  assert.match(note.textContent, /Moved 3 items to the trash/);
  assert.ok(note.textContent.includes("Undo"));
  assert.equal(ctl.state.focusId, "x2", "focus goes to the next row after the trashed ones");
});

test("Ctrl+C then Ctrl+V copies into the focused folder, Ctrl+X then Ctrl+V moves", async () => {
  const { app, li, calls } = setupTree();
  press(li("x2"), "c", { ctrlKey: true });
  assert.equal(app.clipboard.mode, "copy");
  press(li("f1"), "v", { ctrlKey: true });
  await sleep(5);
  assert.deepEqual(calls, [["copy", "x2", "f1"]]);
  assert.ok(app.clipboard, "a copy can be pasted again");

  press(li("n1"), "x", { metaKey: true });
  press(li("f2"), "v", { metaKey: true });
  await sleep(5);
  assert.deepEqual(calls.slice(1), [["move", "n1", "f2"]]);
  assert.equal(app.clipboard, null, "a cut pastes once");

  press(li("x2"), "v", { ctrlKey: true });
  await sleep(5);
  assert.equal(calls.length, 2, "nothing to paste");
});

test("Escape clears a multiple selection and the clipboard", () => {
  const { app, ctl, li } = setupTree();
  li("f1").focus();
  press(li("f1"), "ArrowDown", { shiftKey: true });
  press(li("f2"), "c", { ctrlKey: true });
  assert.equal(ctl.selection().length, 2);
  const event = press(li("f2"), "Escape");
  assert.equal(event.defaultPrevented, true);
  assert.deepEqual(ctl.selection(), ["f2"]);
  assert.equal(app.clipboard, null);
});

test("Ctrl and Shift clicks select without navigating", () => {
  const { ctl, content, calls } = setupTree();
  clickWith(content("f1"));
  assert.deepEqual(ctl.selection(), ["f1"]);
  clickWith(content("x2"), { ctrlKey: true });
  assert.deepEqual(ctl.selection().sort(), ["f1", "x2"]);
  clickWith(content("f2"), { shiftKey: true });
  assert.deepEqual(ctl.selection(), ["f2", "n1", "x2"], "Shift selects the range from the last clicked row");
  clickWith(content("n1"));
  clickWith(content("f1"), { shiftKey: true });
  assert.deepEqual(ctl.selection(), ["f1", "f2", "n1"]);
  assert.deepEqual(calls.map((c) => c[0]), ["navigate", "navigate"], "only plain clicks navigate");
});

test("Select mode on phones toggles items with a tap and shows the bar", () => {
  const { host, ctl, content, calls } = setupTree();
  ctl.setSelecting(true);
  clickWith(content("n1"));
  clickWith(content("x2"));
  const bar = host.querySelector(".tree-select-bar");
  assert.ok(bar);
  assert.equal(bar.querySelector(".tree-select-count").textContent, "2 selected");
  bar.querySelectorAll("button").find((b) => b.textContent === "Move to").click();
  assert.deepEqual(calls, [["pick", "move", ["n1", "x2"]]]);
  ctl.setSelecting(false);
  assert.equal(host.querySelector(".tree-select-bar"), null);
});

test("the Move dialog picks a folder in a TreeView and blocks a folder's own subtree", async () => {
  const { app, calls, space, nodes } = setupTree();
  const handle = tree.openMoveDialog(app, { mode: "move", targets: [{ kind: "file", rec: file("x2", "b.png", "", "image/png") }], space, nodes });
  const dialog = handle.element;
  assert.match(dialog.textContent, /Move “b.png”/);
  const row = (id) => dialog.querySelector(`[role=treeitem][data-id="${id}"]`);
  assert.equal(row("__root__").getAttribute("aria-selected"), "true", "the item's own folder starts selected");
  assert.equal(row("n1"), null, "pages are not destinations");
  row("f1").focus();
  press(row("f1"), "Enter");
  assert.equal(row("f1").getAttribute("aria-selected"), "true");
  assert.equal(row("__root__").getAttribute("aria-selected"), "false");
  handle.buttons[1].click();
  await sleep(20);
  assert.deepEqual(calls, [["move", "x2", "f1"]]);

  const second = tree.openMoveDialog(app, { mode: "move", targets: [{ kind: "folder", rec: folder("f1", "Docs") }], space, nodes });
  assert.equal(second.element.querySelector('[role=treeitem][data-id="f1"]'), null, "a folder cannot move into itself");
  second.close();

  const third = tree.openMoveDialog(app, { mode: "copy", targets: [{ kind: "file", rec: file("x2", "b.png") }, { kind: "note", rec: note("n1", "Readme", "readme.md") }], space, nodes });
  assert.match(third.element.textContent, /Copy 2 items/);
  assert.equal(third.buttons[1].textContent, "Copy here");
  third.close();
});

test("the item menu offers the docs/files.md actions and the multiple selection menu", () => {
  const { app } = setupTree();
  app.fileCache = { isPinned: () => ({ pinned: false }) };
  app.createNote = () => {};
  const space = { id: "s1", slug: "docs", name: "Docs" };
  const labels = (items) => items.filter((i) => i.label).map((i) => i.label);
  assert.deepEqual(labels(tree.itemMenuItems(app, { space, item: { kind: "file", rec: file("x2", "b.png") } })), [
    "Open", "New note", "New folder", "Upload", "Rename", "Move to", "Copy to", "Replace", "Download", "Keep available offline", "Copy link", "Move to trash",
  ]);
  app.itemAccess = () => "manage";
  assert.ok(labels(tree.itemMenuItems(app, { space, item: { kind: "folder", rec: folder("f1", "Docs") } })).includes("Share"));
  assert.ok(!labels(tree.itemMenuItems(app, { space, item: { kind: "note", rec: note("n1", "R", "r.md") } })).includes("Keep available offline"));
  const multi = tree.itemMenuItems(app, { space, item: { kind: "file", rec: file("x2", "b.png") }, targets: [{ kind: "file", rec: file("x2", "b.png") }, { kind: "note", rec: note("n1", "R", "r.md") }] });
  assert.deepEqual(labels(multi), ["Move to", "Copy to", "Move to trash"]);
  app.itemAccess = () => "view";
  assert.deepEqual(labels(tree.itemMenuItems(app, { space, item: { kind: "file", rec: file("x2", "b.png") } })), ["Open", "Copy to", "Download", "Keep available offline", "Copy link"]);
});

test("file view helpers classify, sort, filter and summarise", () => {
  assert.equal(views.fileTypeOf({ mime: "image/webp", name: "a.webp" }), "image");
  assert.equal(views.fileTypeOf({ mime: "", name: "Report.PDF" }), "pdf");
  assert.equal(views.fileTypeOf({ mime: "application/zip", name: "a.zip" }), "archive");
  assert.equal(views.fileTypeOf({ mime: "text/csv", name: "a.csv" }), "document");
  assert.equal(views.fileTypeOf({ mime: "application/octet-stream", name: "a.bin" }), "other");
  assert.equal(views.kindLabel("file", { mime: "image/jpeg", name: "x.jpg" }), "JPG image");
  assert.equal(views.kindLabel("folder", {}), "Folder");
  const items = [
    { kind: "file", rec: { id: "1", name: "b.pdf", size: 5, updatedAt: "2026-01-02T00:00:00Z" } },
    { kind: "note", rec: { id: "2", title: "Alpha", updatedAt: "2026-03-01T00:00:00Z" } },
    { kind: "folder", rec: { id: "3", name: "zeta", updatedAt: "2025-01-01T00:00:00Z" } },
    { kind: "file", rec: { id: "4", name: "A2.pdf", size: 50, updatedAt: "2026-02-01T00:00:00Z" } },
    { kind: "file", rec: { id: "5", name: "a10.pdf", size: 1, updatedAt: "2026-01-01T00:00:00Z" } },
  ];
  assert.deepEqual(views.sortItems(items, "name").map((t) => t.rec.id), ["3", "2", "4", "5", "1"]);
  assert.deepEqual(views.sortItems(items, "size").map((t) => t.rec.id), ["4", "1", "5", "3", "2"]);
  assert.deepEqual(views.sortItems(items, "updated").map((t) => t.rec.id), ["2", "4", "1", "5", "3"]);
  const files = [{ name: "East wall.jpg", mime: "image/jpeg" }, { name: "wall plan.pdf", mime: "application/pdf" }, { name: "gone.jpg", mime: "image/jpeg", deleted: true }];
  assert.deepEqual(views.filterFiles(files, { type: "image" }).map((f) => f.name), ["East wall.jpg"]);
  assert.deepEqual(views.filterFiles(files, { query: "WALL" }).map((f) => f.name), ["East wall.jpg", "wall plan.pdf"]);
  assert.deepEqual(views.filterFiles(files, { query: "wall east" }).map((f) => f.name), ["East wall.jpg"]);
  const tree = {
    folders: [{ id: "f", path: "P", name: "P", parentId: "" }, { id: "g", path: "P/Q", name: "Q", parentId: "f" }],
    notes: [{ id: "i", path: "P/index.md", parentId: "f", title: "P page" }, { id: "n", path: "P/n.md", parentId: "f" }, { id: "r", path: "root.md", parentId: "" }],
    files: [{ id: "x", path: "P/x.pdf", parentId: "f", size: 2048 }, { id: "y", path: "P/Q/y.pdf", parentId: "g", size: 1 }],
  };
  assert.deepEqual(views.childrenOf(tree, tree.folders[0]).map((t) => t.rec.id), ["g", "n", "x"], "the folder note is the page, not a child");
  assert.deepEqual(views.childrenOf(tree, null).map((t) => t.rec.id), ["f", "r"]);
  assert.equal(views.summarize(views.childrenOf(tree, tree.folders[0])), "1 folder, 1 note, 1 file, 2.0 KB");
  assert.equal(views.summarize([]), "Empty");
  assert.equal(views.formatDuration(75), "1:15");
  assert.equal(views.formatDuration(3725), "1:02:05");
});

test("listing helpers sort in both directions and page like Primer's Pagination", () => {
  const items = [
    { kind: "file", rec: { id: "1", name: "b.pdf", size: 5, mime: "application/pdf" } },
    { kind: "folder", rec: { id: "3", name: "zeta" } },
    { kind: "file", rec: { id: "4", name: "A2.png", size: 50, mime: "image/png" } },
    { kind: "note", rec: { id: "2", title: "Alpha" } },
  ];
  assert.deepEqual(views.sortItems(items, "name", "desc").map((t) => t.rec.id), ["3", "2", "1", "4"], "folders and notes stay first");
  assert.deepEqual(views.sortItems(items, "size", "asc").map((t) => t.rec.id), ["3", "2", "1", "4"]);
  assert.deepEqual(views.sortItems(items, "kind").map((t) => t.rec.id), ["3", "2", "1", "4"]);
  assert.deepEqual(views.pageSlice(84, 1), { page: 1, count: 5, start: 0, end: 20 });
  assert.deepEqual(views.pageSlice(84, 9), { page: 5, count: 5, start: 80, end: 84 });
  assert.deepEqual(views.pageSlice(0, 3), { page: 1, count: 1, start: 0, end: 0 });
  assert.equal(views.rangeText(0, 20, 84), "1 through 20 of 84");
  assert.deepEqual(views.paginationItems(1, 1), [1]);
  assert.deepEqual(views.paginationItems(1, 5), [1, 2, 3, 4, 5]);
  assert.deepEqual(views.paginationItems(1, 12), [1, 2, 3, "gap", 12]);
  assert.deepEqual(views.paginationItems(6, 12), [1, "gap", 4, 5, 6, 7, 8, "gap", 12]);
  assert.deepEqual(views.paginationItems(4, 12), [1, 2, 3, 4, 5, 6, "gap", 12]);
  assert.deepEqual(views.paginationItems(7, 14), [1, "gap", 5, 6, 7, 8, 9, "gap", 14]);
});

test("shell routes and URLs for folders, files and permalinks", () => {
  assert.deepEqual(shell.parseRoute("/s/docs/d/Projects/harbour"), { name: "folder", params: { space: "docs", path: "Projects/harbour" } });
  assert.deepEqual(shell.parseRoute("/s/docs/f/Projects/east%20wall.jpg/"), { name: "file", params: { space: "docs", path: "Projects/east wall.jpg" } });
  assert.deepEqual(shell.parseRoute("/i/01abc"), { name: "item", params: { id: "01abc" } });
  assert.equal(shell.parseRoute("/s/docs/d"), null);
  assert.equal(shell.parseRoute("/s/docs/f/%E0%A4%A"), null);
  assert.equal(shell.parseRoute("/i/a/b"), null);
  assert.equal(shell.folderUrl("docs", "Projects/east wall"), "/s/docs/d/Projects/east%20wall");
  assert.equal(shell.fileViewUrl("my space", "a/b (1).pdf"), "/s/my%20space/f/a/b%20(1).pdf");
  assert.equal(shell.itemPermalinkUrl("x y"), "/i/x%20y");
  assert.ok(shell.isShellPath("/s/docs/f/report.pdf"));
  assert.ok(shell.isShellPath("/i/abc"));
});

test("thumbSrc asks for a width the thumbnail handler accepts", () => {
  const rec = { thumb: "/thumbs/demo/f1", sha256: "78fbedda68a3ffff" };
  for (const large of [false, true]) {
    const url = new URL(views.thumbSrc(rec, large), "http://x");
    assert.ok(views.THUMB_WIDTHS.includes(Number(url.searchParams.get("w"))), url.href);
    assert.equal(url.searchParams.get("h"), "78fbedda68a3");
  }
  assert.equal(views.thumbSrc({ thumb: "/thumbs/demo/f2" }), "/thumbs/demo/f2?w=128");
});
