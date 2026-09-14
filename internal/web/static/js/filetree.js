// Sidebar tree of folders, notes and files on Primer TreeView (docs/primer.md 5.1).
// Rows hold no buttons besides the chevron. Item actions open from the
// context menu: a right-click, Shift+F10 or the Menu key. The tree supports
// multiple selection with Shift and Ctrl or a Select mode on phones, F2
// rename in place, Delete to trash with Undo, Ctrl+C, Ctrl+X and Ctrl+V,
// drag to move and drop of desktop files to upload. Move to and Copy to open
// a Dialog with a TreeView folder picker. The item actions are shared with
// the folder, file and note views.

import { h, icon, openMenu, openDialog, editInPlace, undoable, announce, banner, bannerHost, button, treeView, isNarrow, fileIconName, uid } from "./ui.js";
import { validateName, freeName } from "./sync.js";

/** The drag data type that carries item ids between rows. */
export const DRAG_TYPE = "application/x-markspace-items";

const ACCESS_RANK = { none: 0, view: 1, comment: 2, edit: 3, manage: 4 };

/** Reports whether an access level reaches another. */
export function accessAtLeast(have, want) {
  return (ACCESS_RANK[have] || 0) >= (ACCESS_RANK[want] || 0);
}

/** Returns the record of a tree node. */
export function recordOf(node) {
  return node ? node.folder || node.note || node.file || null : null;
}

/**
 * Returns the display name of an item: the note title or the folder or file name.
 * @param {string} kind
 * @param {object} rec
 * @returns {string}
 */
export function itemLabel(kind, rec) {
  if (!rec) return "";
  if (kind === "note") return rec.title || "Untitled";
  return rec.name || String(rec.path || "").split("/").pop() || "Untitled";
}

/**
 * Picks the Octicon of an item (docs/primer.md 4.3 and 5.1).
 * @param {string} kind
 * @param {object} rec
 * @param {{open?: boolean}} [options] open picks the open folder icon.
 * @returns {string}
 */
export function itemIcon(kind, rec, { open = false } = {}) {
  if (kind === "folder") return open ? "file-directory-open-fill" : "file-directory-fill";
  if (kind === "note") return "note";
  return fileIconName(rec && rec.mime, rec && rec.name);
}

/**
 * Lists the visible rows of a tree in order.
 * @param {object[]} nodes tree nodes from store.tree
 * @param {Set<string>} expanded ids of open folders
 * @returns {{id: string, kind: string, depth: number, parentId: string, hasChildren: boolean, node: object}[]}
 */
export function flattenVisible(nodes, expanded, depth = 1, parentId = "", out = []) {
  for (const node of nodes || []) {
    const rec = recordOf(node);
    if (!rec) continue;
    const hasChildren = (node.children || []).length > 0;
    out.push({ id: rec.id, kind: node.kind, depth, parentId, hasChildren, node });
    if (hasChildren && expanded.has(rec.id)) flattenVisible(node.children, expanded, depth + 1, rec.id, out);
  }
  return out;
}

/**
 * Returns the ids of the visible rows from the anchor to the target, both included.
 * @param {{id: string}[]} rows
 * @param {string} anchorId
 * @param {string} targetId
 * @returns {string[]}
 */
export function selectRange(rows, anchorId, targetId) {
  const b = rows.findIndex((r) => r.id === targetId);
  if (b < 0) return [];
  const a = rows.findIndex((r) => r.id === anchorId);
  if (a < 0) return [targetId];
  const [lo, hi] = a < b ? [a, b] : [b, a];
  return rows.slice(lo, hi + 1).map((r) => r.id);
}

/**
 * Maps a key press in the tree to a Markspace command, or null. TreeView
 * handles movement, expansion and type-ahead itself. Keys typed into a text
 * field are ignored.
 * @param {KeyboardEvent} event
 * @returns {{type: string}|null}
 */
export function treeKeyAction(event) {
  const target = event.target;
  if (target && typeof target.closest === "function" && target.closest("input, textarea, select")) return null;
  const mod = event.ctrlKey || event.metaKey;
  const key = event.key || "";
  if (!mod && !event.altKey) {
    switch (key) {
      case "F2":
        return { type: "rename" };
      case "Delete":
        return { type: "trash" };
      case "ContextMenu":
        return { type: "menu" };
      case "F10":
        return event.shiftKey ? { type: "menu" } : null;
      case "Escape":
        return { type: "clear" };
      default:
        return null;
    }
  }
  if (event.metaKey && key === "Backspace") return { type: "trash" };
  if (mod && !event.altKey && !event.shiftKey) {
    switch (key.toLowerCase()) {
      case "c":
        return { type: "copy" };
      case "x":
        return { type: "cut" };
      case "v":
        return { type: "paste" };
      case "a":
        return { type: "selectAll" };
      default:
        return null;
    }
  }
  return null;
}

/**
 * Returns the folder id that receives a drop or a paste on an item: the
 * folder itself, or the folder that holds a note or file. "" is the root.
 * @param {{kind: string, rec: object}|null} item
 * @returns {string}
 */
export function folderFor(item) {
  if (!item) return "";
  return item.kind === "folder" ? item.rec.id : item.rec.parentId || "";
}

/**
 * Returns the ids of the folders items may not move into: each moving folder
 * and every folder below it.
 * @param {{kind: string, rec: object}[]} items
 * @param {object[]} folders every folder of the space
 * @returns {Set<string>}
 */
export function blockedFolders(items, folders) {
  const out = new Set();
  for (const t of items) {
    if (!t || t.kind !== "folder") continue;
    out.add(t.rec.id);
    for (const f of folders) if (String(f.path).startsWith(t.rec.path + "/")) out.add(f.id);
  }
  return out;
}

/**
 * Titles the Move or Copy dialog, such as "Move 3 items".
 * @param {"move"|"copy"} mode
 * @param {string[]} names
 * @returns {string}
 */
export function moveTitle(mode, names) {
  const verb = mode === "copy" ? "Copy" : "Move";
  if (names.length === 1) return `${verb} “${names[0]}”`;
  return `${verb} ${names.length} items`;
}

/**
 * Builds the TreeView items of the folder picker. Blocked folders and their
 * subtrees are left out. Folders on the path to open are expanded.
 * @param {object[]} nodes tree nodes from store.tree
 * @param {{blocked?: Set<string>, open?: Set<string>}} [options]
 * @returns {{id: string, label: string, icon: string, children: object[], expanded: boolean}[]}
 */
export function folderPickerItems(nodes, { blocked = new Set(), open = new Set() } = {}) {
  const out = [];
  for (const node of nodes || []) {
    if (node.kind !== "folder" || !node.folder || blocked.has(node.folder.id)) continue;
    const children = folderPickerItems(node.children, { blocked, open });
    const expanded = children.length > 0 && open.has(node.folder.id);
    out.push({ id: node.folder.id, label: node.folder.name, icon: itemIcon("folder", node.folder, { open: expanded }), children, expanded });
  }
  return out;
}

/* ---------- Messages ---------- */

const plural = (n, one, many) => (n === 1 ? one : `${n} ${many}`);

/** Shows a failure as a critical Banner at the top of the page, as docs/primer.md 6.4 requires. */
function failed(err, fallback) {
  banner({ variant: "critical", title: (err && err.message) || fallback, into: bannerHost() });
}

/* ---------- Shared item actions ---------- */

/**
 * Moves items into a folder at once, with Undo in a Banner.
 * @param {object} app
 * @param {{kind: string, rec: object}[]} items
 * @param {string} parentId
 */
export async function moveItemsTo(app, items, parentId) {
  const moving = items.filter((t) => t && (t.rec.parentId || "") !== (parentId || ""));
  if (!moving.length) return "kept";
  const from = moving.map((t) => ({ id: t.rec.id, parentId: t.rec.parentId || "", position: t.rec.position || 0 }));
  const label = moving.length === 1 ? `Moved “${itemLabel(moving[0].kind, moving[0].rec)}”` : `Moved ${moving.length} items`;
  return undoable({
    label,
    failLabel: "The move did not work.",
    run: async () => {
      for (const t of moving) await app.sync.moveItem(t.rec.id, parentId || "");
    },
    undo: async () => {
      for (const f of from) await app.sync.moveItem(f.id, f.parentId, f.position);
    },
  });
}

/**
 * Copies items into a folder. The copies show in the tree, so success is only
 * announced.
 * @param {object} app
 * @param {{kind: string, rec: object}[]} items
 * @param {object} space the target space
 * @param {string} parentId
 */
export async function copyItemsTo(app, items, space, parentId) {
  try {
    for (const t of items) await app.sync.copyItem(t.rec.id, { spaceId: space.id, parentId: parentId || "" });
    announce(items.length === 1 ? `Copied “${itemLabel(items[0].kind, items[0].rec)}”` : `Copied ${items.length} items`);
  } catch (err) {
    failed(err, "The copy did not work.");
  }
}

/**
 * Moves items to the trash at once, with Undo in a Banner. When the page on
 * screen is one of them, or inside one, the view goes to the folder that
 * held it.
 * @param {object} app
 * @param {{kind: string, rec: object}[]} items
 */
export async function trashItemsWithUndo(app, items) {
  const list = items.filter(Boolean);
  if (!list.length) return "kept";
  const label = list.length === 1 ? `Moved “${itemLabel(list[0].kind, list[0].rec)}” to the trash` : `Moved ${list.length} items to the trash`;
  const back = typeof location !== "undefined" ? location.pathname + location.search : "";
  const view = app.view || {};
  const currentId = view.itemId || view.noteId || "";
  let leave = null;
  if (currentId && app.store && typeof app.store.getItem === "function") {
    const cur = await app.store.getItem(currentId);
    const rec = cur && (cur.folder || cur.note || cur.file);
    const hit = rec && list.find((t) => t.rec.id === rec.id || String(rec.path).startsWith(t.rec.path + "/"));
    if (hit) leave = hit;
  }
  return undoable({
    label,
    failLabel: "The trash did not work.",
    run: async () => {
      for (const t of list) await app.sync.trashItem(t.rec.id);
      if (leave && typeof app.navigate === "function") {
        const space = app.spaceBy ? app.spaceBy(leave.rec.spaceId) : null;
        const parent = leave.rec.parentId && app.store ? await app.store.getFolder(leave.rec.parentId) : null;
        await app.navigate(parent ? app.hrefFor("folder", parent) : space ? "/s/" + encodeURIComponent(space.slug) : "/", { replace: true });
      }
    },
    undo: async () => {
      for (const t of list) await app.sync.restoreItem(t.rec.id);
      if (leave && back && typeof app.navigate === "function") await app.navigate(back, { state: { itemId: currentId } });
    },
  });
}

/**
 * Asks the browser for files through a hidden file input.
 * @param {{multiple?: boolean, accept?: string}} [options]
 * @returns {Promise<File[]>} resolves with the chosen files, or [] after a cancel
 */
export function chooseFiles({ multiple = true, accept = "" } = {}) {
  return new Promise((resolve) => {
    const input = h("input", { type: "file", multiple, accept: accept || null, hidden: true, class: "file-picker-input", tabindex: "-1", "aria-hidden": "true" });
    let settled = false;
    const done = (files) => {
      if (settled) return;
      settled = true;
      input.remove();
      resolve(files);
    };
    input.addEventListener("change", () => done(Array.from(input.files || [])));
    input.addEventListener("cancel", () => done([]));
    document.body.appendChild(input);
    input.click();
  });
}

/**
 * Uploads files into a folder. The header upload indicator shows progress,
 * so the start is only announced.
 * @param {object} app
 * @param {object} space
 * @param {string} folderId
 * @param {File[]} files
 * @returns {Promise<object[]>} the new file records
 */
export async function uploadInto(app, space, folderId, files) {
  if (!files || !files.length) return [];
  try {
    const recs = await app.sync.uploadFiles(space.id, folderId || "", files);
    announce(recs.length === 1 ? `Uploading “${recs[0].name}”` : `Uploading ${recs.length} files`);
    return recs;
  } catch (err) {
    failed(err, "The upload could not start.");
    return [];
  }
}

/**
 * Starts a download of a file, or of one version of it.
 * @param {object} rec file record
 * @param {{version?: string}} [options]
 */
export function downloadFile(rec, { version = "" } = {}) {
  const url = `${rec.url}?${version ? `v=${encodeURIComponent(version)}&` : ""}download=1`;
  const a = h("a", { href: url, download: rec.name || "", "data-full-nav": "", hidden: true });
  document.body.appendChild(a);
  a.click();
  a.remove();
}

/**
 * Creates a folder named "New folder", or the next free variant, and asks
 * where to rename it in place.
 * @param {object} app
 * @param {object} space
 * @param {string} parentId
 * @param {"tree"|"view"} where
 */
export async function newFolderIn(app, space, parentId, where = "tree") {
  try {
    const { folders, notes, files } = await app.store.listItems(space.id);
    const parent = parentId ? folders.find((f) => f.id === parentId) : null;
    const dir = parent ? parent.path : "";
    const names = [...folders, ...files].filter((r) => (r.path.includes("/") ? r.path.slice(0, r.path.lastIndexOf("/")) : "") === dir).map((r) => r.name);
    for (const n of notes) if ((n.path.includes("/") ? n.path.slice(0, n.path.lastIndexOf("/")) : "") === dir) names.push(n.path.split("/").pop());
    const rec = await app.sync.createFolder({ spaceId: space.id, parentId: parentId || "", name: freeName(names, "New folder", true) });
    app.pendingRename = { id: rec.id, where };
    if (typeof app.expandFolder === "function" && parentId) app.expandFolder(parentId);
    return rec;
  } catch (err) {
    failed(err, "The folder could not be created.");
    return null;
  }
}

/**
 * Replaces the content of a file. The upload indicator shows progress.
 * @param {object} app
 * @param {object} rec
 * @param {File} file
 */
export async function replaceWith(app, rec, file) {
  if (!file) return;
  try {
    await app.sync.replaceFile(rec.id, file);
    announce(`Replacing “${rec.name}”`);
  } catch (err) {
    failed(err, "The file could not be replaced.");
  }
}

async function copyItemLink(rec) {
  const url = location.origin + "/i/" + encodeURIComponent(rec.id);
  try {
    await navigator.clipboard.writeText(url);
    announce("Link copied");
  } catch {
    banner({ variant: "warning", title: "The link could not be copied", description: url, dismissible: true, into: bannerHost() });
  }
}

async function togglePin(app, item, pinned) {
  const ref = { kind: item.kind, id: item.rec.id };
  try {
    if (pinned) {
      await app.fileCache.unpin(ref);
      announce(`“${itemLabel(item.kind, item.rec)}” is no longer kept offline`);
    } else {
      await app.fileCache.pin(ref);
      announce(`“${itemLabel(item.kind, item.rec)}” will be kept available offline`);
    }
  } catch (err) {
    failed(err, "The offline setting could not change.");
  }
}

/**
 * Lists the menu items of an item for the tree, the folder view, the file
 * view and the note PageHeader. With more than one target the menu holds the
 * actions that apply to all of them. Danger items go last after a divider.
 * @param {object} app
 * @param {{space: object, item: {kind: string, rec: object}, targets?: object[], where?: "tree"|"view", skip?: string[]}} options
 *   skip lists labels the caller shows elsewhere, such as Download in the file header.
 * @returns {object[]} items for ui.js openMenu
 */
export function itemMenuItems(app, { space, item, targets = null, where = "tree", skip = [] }) {
  const list = targets && targets.length ? targets : [item];
  const out = [];
  const add = (entry) => {
    if (!skip.includes(entry.label)) out.push(entry);
  };
  if (list.length > 1) {
    const allEdit = list.every((t) => accessAtLeast(app.itemAccess(t.kind, t.rec), "edit") && !t.rec.deleted);
    out.push({ group: `${list.length} items selected` });
    if (allEdit) add({ label: "Move to", icon: "move", onClick: () => app.openMoveDialog("move", list) });
    add({ label: "Copy to", icon: "copy", onClick: () => app.openMoveDialog("copy", list) });
    if (allEdit) {
      out.push({ separator: true });
      add({ label: "Move to trash", icon: "trash", danger: true, onClick: () => trashItemsWithUndo(app, list) });
    }
    return out;
  }
  const { kind, rec } = item;
  const access = app.itemAccess(kind, rec);
  const canEdit = accessAtLeast(access, "edit") && !rec.deleted;
  const folderId = folderFor(item);
  add({ label: "Open", icon: itemIcon(kind, rec), onClick: () => app.navigate(app.hrefFor(kind, rec), { state: kind === "note" ? { noteId: rec.id } : { itemId: rec.id } }) });
  if (canEdit) {
    add({ label: "New note", icon: "plus", onClick: () => app.createNote({ space, parentId: folderId }) });
    add({ label: "New folder", icon: "file-directory-fill", onClick: () => newFolderIn(app, space, folderId, where) });
    add({ label: "Upload", icon: "upload", onClick: async () => uploadInto(app, space, folderId, await chooseFiles()) });
    out.push({ separator: true });
    add({ label: "Rename", icon: "pencil", onClick: () => app.renameItem(kind, rec, where) });
    add({ label: "Move to", icon: "move", onClick: () => app.openMoveDialog("move", [item]) });
  }
  add({ label: "Copy to", icon: "copy", onClick: () => app.openMoveDialog("copy", [item]) });
  if (kind === "file" && canEdit) {
    add({ label: "Replace", icon: "sync", onClick: async () => replaceWith(app, rec, (await chooseFiles({ multiple: false }))[0]) });
  }
  if (kind === "file") add({ label: "Download", icon: "download", onClick: () => downloadFile(rec) });
  if (kind !== "note" && app.fileCache && !rec.provisional) {
    const pin = app.fileCache.isPinned({ kind, id: rec.id }) || {};
    const inherited = pin.pinned && pin.via && pin.via !== rec.id;
    add({ label: "Keep available offline", icon: "cloud", checked: !!pin.pinned, selectionType: "multiple", disabled: !!inherited, onClick: () => togglePin(app, item, !!pin.pinned) });
  }
  add({ label: "Copy link", icon: "link", onClick: () => copyItemLink(rec) });
  if (accessAtLeast(access, "manage") && !rec.provisional && !rec.deleted) {
    add({ label: "Share", icon: "people", onClick: () => app.openItemPanel(kind, rec, "share") });
  }
  if (canEdit) {
    out.push({ separator: true });
    add({ label: "Move to trash", icon: "trash", danger: true, onClick: () => trashItemsWithUndo(app, [item]) });
  }
  while (out.length && out[0].separator) out.shift();
  for (let i = out.length - 1; i > 0; i--) if (out[i].separator && out[i - 1].separator) out.splice(i, 1);
  while (out.length && out[out.length - 1].separator) out.pop();
  return out;
}

/* ---------- Move and copy dialog ---------- */

/**
 * Opens the Move to or Copy to Dialog (docs/primer.md 5.5): a large Dialog with a
 * TreeView of folders in single select and a footer with Cancel and "Move
 * here" or "Copy here". A move acts at once with Undo in a Banner.
 * @param {object} app
 * @param {{mode: "move"|"copy", targets: {kind: string, rec: object}[], space: object, nodes: object[]}} options
 *   nodes is the tree of the space from store.tree.
 * @returns {{close: Function, element: HTMLElement} | null}
 */
export function openMoveDialog(app, { mode, targets, space, nodes }) {
  const items = (targets || []).filter(Boolean);
  if (!items.length || !space) return null;
  const names = items.map((t) => itemLabel(t.kind, t.rec));
  const index = new Map();
  const walk = (list) => {
    for (const node of list || []) {
      if (node.kind === "folder" && node.folder) {
        index.set(node.folder.id, node.folder);
        walk(node.children);
      }
    }
  };
  walk(nodes);
  const parents = new Set(items.map((t) => t.rec.parentId || ""));
  const origin = parents.size === 1 ? [...parents][0] : null;
  const blocked = mode === "move" ? blockedFolders(items, [...index.values()]) : new Set();
  const open = new Set();
  for (let f = origin ? index.get(origin) : null; f; f = f.parentId ? index.get(f.parentId) : null) open.add(f.id);
  const ROOT = "__root__";
  let chosen = origin === null || blocked.has(origin) ? ROOT : origin || ROOT;
  const rootItem = { id: ROOT, label: space.name, icon: "home", expanded: true, children: folderPickerItems(nodes, { blocked, open }) };
  const where = h("p", { class: "move-dialog-where", id: uid("move-where") });
  const describe = () => {
    const folder = chosen === ROOT ? null : index.get(chosen);
    where.textContent = "Destination: " + (folder ? space.name + " / " + folder.path.split("/").join(" / ") : space.name);
  };
  const tree = treeView({
    label: "Folders",
    items: [rootItem],
    onOpen: (item) => {
      chosen = item.id;
      mark();
      announce(where.textContent);
    },
    onToggle: (item, isOpen) => {
      const li = tree.element.querySelector(`[role=treeitem][data-id="${cssEscape(item.id)}"]`);
      const slot = rowPart(li, "tree-view-visual");
      if (slot && item.id !== ROOT) slot.replaceChildren(icon(itemIcon("folder", null, { open: isOpen }), 16));
    },
  });
  const mark = () => {
    for (const li of tree.element.querySelectorAll("[role=treeitem]")) {
      li.setAttribute("aria-selected", li.dataset.id === chosen ? "true" : "false");
    }
    describe();
  };
  mark();
  const handle = openDialog({
    title: moveTitle(mode, names),
    subtitle: "Choose a folder.",
    width: "large",
    className: "move-dialog",
    content: h("div", { class: "move-dialog-body" }, tree.element, where),
    footer: [
      { label: "Cancel" },
      {
        label: mode === "copy" ? "Copy here" : "Move here",
        variant: "primary",
        onClick: async () => {
          const parentId = chosen === ROOT ? "" : chosen;
          if (mode === "move" && origin !== null && parentId === origin) {
            throw new Error(names.length === 1 ? "The item is already in this folder." : "The items are already in this folder.");
          }
          if (mode === "move") {
            // The dialog closes first, so the Undo Banner is not under it.
            setTimeout(() => moveItemsTo(app, items, parentId), 0);
          } else {
            await copyItemsTo(app, items, space, parentId);
          }
          return undefined;
        },
      },
    ],
    initialFocus: tree.element.querySelector(`[role=treeitem][data-id="${cssEscape(chosen)}"]`),
  });
  const selectedRow = tree.element.querySelector(`[role=treeitem][data-id="${cssEscape(chosen)}"]`);
  if (selectedRow) tree.focusItem(chosen);
  return handle;
}

/** Returns the content row of a TreeView item, or one slot inside it. */
function rowPart(li, className = "tree-view-item-content") {
  const content = li ? Array.from(li.children).find((c) => c.classList && c.classList.contains("tree-view-item-content")) : null;
  if (!content || className === "tree-view-item-content") return content || null;
  return Array.from(content.children).find((c) => c.classList && c.classList.contains(className)) || null;
}

/** Escapes an id for an attribute selector. */
function cssEscape(value) {
  return typeof CSS !== "undefined" && CSS.escape ? CSS.escape(String(value)) : String(value).replace(/["\\]/g, "\\$&");
}

/* ---------- Tree controller ---------- */

/**
 * Creates the tree controller of one space.
 * @param {{app: object, host: HTMLElement, space: object, labelledBy?: string, onExpandedChange?: (expanded: Set<string>) => void, onSelectingChange?: (on: boolean) => void}} options
 * @returns {object} controller
 */
export function createFileTree({ app, host, space, labelledBy = "", onExpandedChange = null, onSelectingChange = null }) {
  const state = {
    nodes: [],
    rows: [],
    byId: new Map(),
    expanded: new Set(),
    currentId: null,
    selection: new Set(),
    anchorId: null,
    focusId: null,
    selecting: false,
    editing: null,
    stale: false,
    drag: null,
  };
  const ac = new AbortController();
  const listen = (el, type, fn) => el.addEventListener(type, fn, { signal: ac.signal });
  host.classList.add("file-tree-host");

  const canEdit = (t) => t && accessAtLeast(app.itemAccess(t.kind, t.rec), "edit") && !t.rec.deleted;
  const spaceCanEdit = () => accessAtLeast(app.itemAccess("space", space), "edit");
  const liOf = (id) => host.querySelector(`[role=treeitem][data-id="${cssEscape(id)}"]`);
  const folders = () => Array.from(state.byId.values()).filter((t) => t.kind === "folder").map((t) => t.rec);
  const targetsFor = (id) => (state.selection.has(id) && state.selection.size > 1 ? Array.from(state.selection) : [id]).map((x) => state.byId.get(x)).filter(Boolean);

  function index(nodes, parentId = "") {
    for (const node of nodes || []) {
      const rec = recordOf(node);
      if (!rec) continue;
      state.byId.set(rec.id, { kind: node.kind, rec, node, parentId });
      if (node.page) state.byId.set(node.page.id, { kind: "note", rec: node.page, node: null, parentId: rec.id, page: true });
      index(node.children, rec.id);
    }
  }

  /** Maps store nodes to TreeView items. */
  function toItems(nodes) {
    const out = [];
    for (const node of nodes || []) {
      const rec = recordOf(node);
      if (!rec) continue;
      const children = toItems(node.children);
      const expanded = children.length > 0 && state.expanded.has(rec.id);
      const glyph = node.kind === "note" && rec.icon ? h("span", { class: "tree-emoji", text: rec.icon }) : itemIcon(node.kind, rec, { open: expanded });
      out.push({
        id: rec.id,
        label: itemLabel(node.kind, rec),
        icon: glyph,
        children,
        expanded,
        current: rec.id === state.currentId || !!(node.page && node.page.id === state.currentId),
      });
    }
    return out;
  }

  const tree = treeView({
    labelledBy,
    label: labelledBy ? "" : "Pages in " + space.name,
    items: [],
    emptyText: "No pages",
    onOpen,
    onToggle,
    onKey,
    onContextMenu,
  });
  tree.element.classList.add("file-tree");
  tree.element.setAttribute("aria-multiselectable", "true");
  const empty = h("p", { class: "tree-empty", text: "No pages yet" });
  const selectBarHost = h("div", { class: "tree-select-host" });
  host.replaceChildren(selectBarHost, tree.element);

  function decorate() {
    const touch = isNarrow();
    for (const li of tree.element.querySelectorAll("[role=treeitem]")) {
      const t = state.byId.get(li.dataset.id);
      if (!t) continue;
      li.setAttribute("aria-selected", state.selection.has(li.dataset.id) ? "true" : "false");
      li.draggable = !touch && canEdit(t) && state.editing !== li.dataset.id;
      if (t.rec.uploadFailed) {
        const content = rowPart(li);
        if (content && !content.querySelector(".tree-flag")) {
          content.appendChild(h("span", { class: "tree-flag" }, icon("alert", 16), h("span", { class: "sr-only", text: "Upload failed" })));
        }
      }
    }
  }

  function render(nodes, { expanded, currentId } = {}) {
    if (nodes) state.nodes = nodes;
    if (expanded) state.expanded = new Set(expanded);
    if (currentId !== undefined) state.currentId = currentId;
    state.byId = new Map();
    index(state.nodes);
    for (const id of Array.from(state.selection)) if (!state.byId.has(id)) state.selection.delete(id);
    if (state.editing) {
      state.stale = true;
      return;
    }
    state.rows = flattenVisible(state.nodes, state.expanded);
    const active = document.activeElement;
    const hadFocus = tree.element.contains(active);
    if (hadFocus && active && active.dataset && active.dataset.id) state.focusId = active.dataset.id;
    tree.update(toItems(state.nodes));
    if (!state.nodes.length) {
      tree.element.hidden = true;
      if (!empty.isConnected) host.appendChild(empty);
    } else {
      tree.element.hidden = false;
      empty.remove();
    }
    decorate();
    drawSelectBar();
    const target = state.focusId && liOf(state.focusId) ? state.focusId : null;
    if (target) {
      for (const li of tree.element.querySelectorAll("[role=treeitem]")) li.setAttribute("tabindex", li.dataset.id === target ? "0" : "-1");
      if (hadFocus) tree.focusItem(target);
    }
    const pending = app.pendingRename;
    if (pending && pending.where === "tree" && state.byId.has(pending.id)) {
      app.pendingRename = null;
      revealItem(pending.id);
      rename(pending.id);
    }
  }

  function drawSelectBar() {
    if (!state.selecting) {
      selectBarHost.replaceChildren();
      return;
    }
    const items = Array.from(state.selection).map((id) => state.byId.get(id)).filter(Boolean);
    const n = items.length;
    const allEdit = n > 0 && items.every(canEdit);
    const actions = [];
    if (allEdit) actions.push(button({ label: "Move to", size: "small", onClick: () => app.openMoveDialog("move", items) }));
    if (n) actions.push(button({ label: "Copy to", size: "small", onClick: () => app.openMoveDialog("copy", items) }));
    if (allEdit) actions.push(button({ label: "Move to trash", size: "small", variant: "danger", onClick: () => trashItemsWithUndo(app, items).then(() => setSelecting(false)) }));
    actions.push(button({ label: "Done", size: "small", variant: "invisible", onClick: () => setSelecting(false) }));
    selectBarHost.replaceChildren(
      h(
        "div",
        { class: "tree-select-bar", role: "toolbar", "aria-label": "Selected items" },
        h("span", { class: "tree-select-count", text: n === 1 ? "1 selected" : `${n} selected` }),
        h("div", { class: "tree-select-actions" }, actions)
      )
    );
  }

  function applySelection() {
    for (const li of tree.element.querySelectorAll("[role=treeitem]")) {
      li.setAttribute("aria-selected", state.selection.has(li.dataset.id) ? "true" : "false");
    }
    drawSelectBar();
  }

  function setSelection(ids, { keepAnchor = false } = {}) {
    state.selection = new Set(ids);
    if (!keepAnchor) state.anchorId = ids[ids.length - 1] || null;
    applySelection();
  }

  function toggleSelected(id) {
    if (state.selection.has(id)) state.selection.delete(id);
    else state.selection.add(id);
    applySelection();
    announce(state.selection.size === 1 ? "1 selected" : `${state.selection.size} selected`);
  }

  function onToggle(item, open) {
    const t = state.byId.get(item.id);
    if (open) state.expanded.add(item.id);
    else state.expanded.delete(item.id);
    if (t && t.kind === "folder") {
      const li = liOf(item.id);
      const slot = rowPart(li, "tree-view-visual");
      if (slot) slot.replaceChildren(icon(itemIcon("folder", t.rec, { open }), 16));
    }
    state.rows = flattenVisible(state.nodes, state.expanded);
    decorate();
    if (typeof onExpandedChange === "function") onExpandedChange(new Set(state.expanded));
  }

  function onOpen(item, event) {
    const id = item.id;
    const t = state.byId.get(id);
    if (!t) return;
    state.focusId = id;
    const pointer = event && event.type === "click";
    if (state.selecting) {
      toggleSelected(id);
      return;
    }
    if (pointer && event.shiftKey) {
      setSelection(selectRange(state.rows, state.anchorId || state.currentId || id, id), { keepAnchor: true });
      return;
    }
    if (pointer && (event.ctrlKey || event.metaKey)) {
      toggleSelected(id);
      state.anchorId = id;
      return;
    }
    setSelection([id]);
    if (typeof app.closeDrawer === "function") app.closeDrawer({ restore: false });
    if (typeof app.navigate === "function") {
      app.navigate(app.hrefFor(t.kind, t.rec), { state: t.kind === "note" ? { noteId: id } : { itemId: id } });
    }
  }

  /** Opens every folder above an item so its row shows. */
  function revealItem(id) {
    let changed = false;
    for (let t = state.byId.get(id); t && t.parentId; t = state.byId.get(t.parentId)) {
      if (!state.expanded.has(t.parentId)) {
        state.expanded.add(t.parentId);
        changed = true;
      }
    }
    if (changed) {
      render();
      if (typeof onExpandedChange === "function") onExpandedChange(new Set(state.expanded));
    }
  }

  function focusRow(id) {
    state.focusId = id;
    tree.focusItem(id);
  }

  function rename(id) {
    const t = state.byId.get(id);
    if (!t || !canEdit(t)) return null;
    const li = liOf(id);
    const label = rowPart(li, "tree-view-label");
    if (!label) return null;
    state.editing = id;
    li.draggable = false;
    const edit = editInPlace(label, {
      value: t.kind === "note" ? t.rec.title || "" : t.rec.name || "",
      label: t.kind === "note" ? "Title" : "Name",
      validate: (v) => (t.kind === "note" ? (String(v).trim() ? null : "Enter a title") : validateName(String(v).trim(), { kind: t.kind }) || null),
      onSave: (v) => app.sync.renameItem(id, String(v).trim()),
    });
    // Keys and clicks in the field belong to the field, not to the tree row.
    if (edit && edit.element) {
      for (const type of ["keydown", "click", "pointerdown", "contextmenu"]) {
        edit.element.addEventListener(type, (event) => event.stopPropagation());
      }
    }
    edit.done.then(() => {
      state.editing = null;
      if (state.stale) {
        state.stale = false;
        if (typeof app.renderTree === "function") app.renderTree();
        else render();
      } else {
        decorate();
      }
      if (liOf(id)) focusRow(id);
    });
    return edit;
  }

  function openItemMenu(id, anchor, point = null) {
    const t = state.byId.get(id);
    if (!t || !anchor) return null;
    if (!state.selection.has(id)) setSelection([id]);
    const targets = targetsFor(id);
    const items = itemMenuItems(app, { space, item: t, targets, where: "tree" });
    const menu = openMenu(anchor, items, { placement: "bottom-start", point, label: "Actions for " + itemLabel(t.kind, t.rec) });
    app.menu = menu;
    return menu;
  }

  function setClipboard(mode, items) {
    if (!items.length) return;
    app.clipboard = { mode, spaceId: space.id, items };
    announce(mode === "cut" ? plural(items.length, "1 item ready to move", "items ready to move") : plural(items.length, "1 item copied", "items copied"));
  }

  async function paste(id) {
    const clip = app.clipboard;
    if (!clip || !clip.items || !clip.items.length) {
      announce("Nothing to paste");
      return;
    }
    const target = id ? folderFor(state.byId.get(id)) : "";
    const targetItem = target ? state.byId.get(target) : null;
    if (targetItem ? !canEdit(targetItem) : !spaceCanEdit()) {
      announce("You cannot add items here", { politeness: "assertive" });
      return;
    }
    if (clip.mode === "cut") {
      app.clipboard = null;
      if (clip.spaceId !== space.id) {
        banner({ variant: "warning", title: "A move stays inside its space", description: "Use Copy to put items in another space.", dismissible: true, into: bannerHost() });
        return;
      }
      await moveItemsTo(app, clip.items, target);
    } else {
      await copyItemsTo(app, clip.items, space, target);
    }
  }

  /** Returns the id of the row that takes focus after the given rows leave. */
  function nextFocusAfter(ids) {
    const gone = new Set(ids);
    const i = state.rows.findIndex((r) => gone.has(r.id));
    if (i < 0) return null;
    const after = state.rows.slice(i).find((r) => !gone.has(r.id));
    const before = state.rows.slice(0, i).reverse().find((r) => !gone.has(r.id));
    return (after || before || {}).id || null;
  }

  function onKey(item, event) {
    const action = treeKeyAction(event);
    if (!action) return;
    const id = item.id;
    switch (action.type) {
      case "rename":
        event.preventDefault();
        rename(id);
        return;
      case "trash": {
        const items = targetsFor(id).filter(canEdit);
        if (!items.length) return;
        event.preventDefault();
        state.focusId = nextFocusAfter(items.map((t) => t.rec.id));
        trashItemsWithUndo(app, items);
        return;
      }
      case "copy":
      case "cut":
        event.preventDefault();
        setClipboard(action.type, targetsFor(id));
        return;
      case "paste":
        event.preventDefault();
        paste(id);
        return;
      case "selectAll":
        event.preventDefault();
        setSelection(state.rows.map((r) => r.id));
        announce(`${state.rows.length} selected`);
        return;
      case "menu":
        event.preventDefault();
        openItemMenu(id, liOf(id));
        return;
      case "clear":
        if (state.selection.size > 1 || app.clipboard) {
          event.preventDefault();
          event.stopPropagation();
          app.clipboard = null;
          setSelection([id]);
        }
        return;
      default:
    }
  }

  function onContextMenu(item, event) {
    const li = liOf(item.id);
    openItemMenu(item.id, li, event && typeof event.clientX === "number" && (event.clientX || event.clientY) ? { x: event.clientX, y: event.clientY } : null);
  }

  /** Shift with an arrow, Home or End extends the selection after TreeView moves focus. */
  function onHostKey(event) {
    if (!event.shiftKey || !["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
    const li = document.activeElement && document.activeElement.closest ? document.activeElement.closest("[role=treeitem]") : null;
    if (!li || !tree.element.contains(li)) return;
    const from = event.target && event.target.closest ? event.target.closest("[role=treeitem]") : null;
    if (!state.anchorId || !state.selection.has(state.anchorId)) state.anchorId = from ? from.dataset.id : li.dataset.id;
    setSelection(selectRange(state.rows, state.anchorId, li.dataset.id), { keepAnchor: true });
    announce(state.selection.size === 1 ? "1 selected" : `${state.selection.size} selected`);
  }

  /* Drag to move and drop to upload. */

  function dropFolderAt(event) {
    const li = event.target.closest && event.target.closest("[role=treeitem]");
    if (li && tree.element.contains(li) && state.byId.has(li.dataset.id)) return folderFor(state.byId.get(li.dataset.id));
    return host.contains(event.target) ? "" : null;
  }
  function canDropInto(folderId) {
    const target = folderId ? state.byId.get(folderId) : null;
    return target ? canEdit(target) : spaceCanEdit();
  }
  function clearDrop() {
    host.classList.remove("is-drop-target");
    for (const el of host.querySelectorAll(".is-drop-target")) el.classList.remove("is-drop-target");
  }
  function markDrop(folderId) {
    clearDrop();
    if (!folderId) {
      host.classList.add("is-drop-target");
      return;
    }
    const li = liOf(folderId);
    const row = rowPart(li);
    if (row) row.classList.add("is-drop-target");
  }
  function onDragStart(event) {
    const li = event.target.closest && event.target.closest("[role=treeitem]");
    if (!li || state.editing) return;
    const id = li.dataset.id;
    const ids = state.selection.has(id) ? Array.from(state.selection) : [id];
    state.drag = ids;
    event.dataTransfer.effectAllowed = "move";
    event.dataTransfer.setData(DRAG_TYPE, JSON.stringify(ids));
    event.dataTransfer.setData("text/plain", ids.map((x) => itemLabel((state.byId.get(x) || {}).kind, (state.byId.get(x) || {}).rec)).join("\n"));
  }
  function onDragOver(event) {
    const types = Array.from((event.dataTransfer && event.dataTransfer.types) || []);
    const files = types.includes("Files");
    const items = types.includes(DRAG_TYPE) && state.drag;
    if (!files && !items) return;
    const folderId = dropFolderAt(event);
    if (folderId === null || !canDropInto(folderId)) {
      clearDrop();
      return;
    }
    if (items) {
      const moving = state.drag.map((x) => state.byId.get(x)).filter(Boolean);
      if (blockedFolders(moving, folders()).has(folderId) || moving.every((t) => (t.rec.parentId || "") === folderId)) {
        clearDrop();
        return;
      }
    }
    event.preventDefault();
    event.dataTransfer.dropEffect = files ? "copy" : "move";
    markDrop(folderId);
  }
  async function onDrop(event) {
    const folderId = dropFolderAt(event);
    clearDrop();
    if (folderId === null) return;
    const files = Array.from((event.dataTransfer && event.dataTransfer.files) || []);
    if (files.length) {
      event.preventDefault();
      if (!canDropInto(folderId)) return;
      await uploadInto(app, space, folderId, files);
      if (folderId) tree.expand(folderId);
      return;
    }
    if (state.drag) {
      event.preventDefault();
      const moving = state.drag.map((x) => state.byId.get(x)).filter(Boolean);
      state.drag = null;
      if (blockedFolders(moving, folders()).has(folderId)) return;
      await moveItemsTo(app, moving, folderId);
    }
  }

  function setSelecting(on) {
    state.selecting = !!on;
    if (!on) state.selection = new Set(state.currentId && state.byId.has(state.currentId) ? [state.currentId] : []);
    else state.selection = new Set();
    applySelection();
    if (typeof onSelectingChange === "function") onSelectingChange(state.selecting);
  }

  listen(host, "keydown", onHostKey);
  listen(host, "focusin", (event) => {
    const li = event.target.closest && event.target.closest("[role=treeitem]");
    if (li && li.dataset.id) state.focusId = li.dataset.id;
  });
  listen(host, "dragstart", onDragStart);
  listen(host, "dragover", onDragOver);
  listen(host, "dragleave", (event) => {
    if (!event.relatedTarget || !host.contains(event.relatedTarget)) clearDrop();
  });
  listen(host, "drop", onDrop);
  listen(host, "dragend", () => {
    state.drag = null;
    clearDrop();
  });

  const controller = {
    state,
    element: tree.element,
    render,
    rename,
    revealItem,
    focus: focusRow,
    expand: (id) => {
      if (!state.expanded.has(id)) tree.expand(id);
    },
    openMenuFor: (id) => openItemMenu(id, liOf(id)),
    setSelecting,
    get editing() {
      return !!state.editing;
    },
    get selecting() {
      return state.selecting;
    },
    selection: () => Array.from(state.selection),
    setCurrent(id) {
      state.currentId = id;
      render();
    },
    destroy() {
      ac.abort();
    },
  };
  return controller;
}
