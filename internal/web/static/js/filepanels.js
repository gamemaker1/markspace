// Rail panels for folders and files: Info, Comments, History, Share and
// Backlinks, and the Files tab of a note. Comments, History and Share are the
// shared panels of panels.js with the item route prefix. Info, Backlinks and
// Files use the panel frame and rows of panels.js, so every panel follows
// docs/primer.md 5.10.

import { h, icon, button, announce, copyFeedback, formatBytes, fileIconName, createDropzone, uploadList, toggleSwitch, banner, relativeTime } from "./ui.js";
import {
  createPanel,
  listSection,
  listRow,
  rowMenu,
  emptyState,
  accessLabel,
  openComments,
  openHistory,
  openShare,
} from "./panels.js";
import { parse as parseLinks } from "./links.js";

const ACCESS_RANK = { none: 0, view: 1, comment: 2, edit: 3, manage: 4 };
const atLeast = (have, want) => (ACCESS_RANK[have] || 0) >= (ACCESS_RANK[want] || 0);
const enc = encodeURIComponent;

/** Rail tabs of a folder or file view, in order. */
export const ITEM_RAIL_TABS = [
  { id: "info", label: "Info", icon: "info" },
  { id: "comments", label: "Comments", icon: "comment" },
  { id: "history", label: "History", icon: "history" },
  { id: "share", label: "Share", icon: "users" },
  { id: "backlinks", label: "Backlinks", icon: "link" },
];

/**
 * Lists the rail tab ids of an item. History is for files. Share needs
 * manage access.
 * @param {{kind: string, access: string}} options
 * @returns {string[]}
 */
export function itemRailTabIds({ kind, access }) {
  return ITEM_RAIL_TABS.map((t) => t.id).filter((id) => {
    if (id === "history") return kind === "file";
    if (id === "share") return atLeast(access, "manage");
    return true;
  });
}

/**
 * Returns the options the shared panels take for a folder or file: the item
 * as subject, its kind, and the route prefix of its comments and shares.
 * @param {object} opts panel options with item and kind
 * @returns {object}
 */
export function itemPanelOptions(opts) {
  const item = opts.item || {};
  return { ...opts, item, kind: opts.kind, route: `/api/items/${enc(item.id)}` };
}

/* ---------- Info ---------- */

/**
 * Opens the Info panel of a folder or file: details, the offline setting and
 * the permalink. It reads the store and works offline.
 * @param {HTMLElement} container
 * @param {{kind: string, item: object, space: object, access: string, app: object, store: object, fileCache?: object, offline?: boolean}} opts
 */
export function openItemInfo(container, opts) {
  const { kind, app } = opts;
  let offUnsub = null;
  const panel = createPanel(container, opts, { title: "Info", className: "item-info", offlineOk: true }, async (body) => {
    const fresh = kind === "folder" ? await opts.store.getFolder(opts.item.id) : await opts.store.getFile(opts.item.id);
    const rec = fresh || opts.item;
    const dl = h("dl", { class: "ms-details" });
    const row = (label, value) => {
      if (value === null || value === undefined || value === "") return;
      dl.appendChild(h("div", { class: "ms-details-row" }, h("dt", { text: label }), h("dd", {}, value)));
    };
    row("Name", rec.name);
    if (kind === "file") {
      row("Type", rec.mime || "Unknown");
      row("Size", formatBytes(rec.size || 0));
      if (rec.width && rec.height) row("Dimensions", `${rec.width} × ${rec.height} px`);
      if (rec.pages) row("Pages", String(rec.pages));
      if (rec.duration) row("Duration", `${Math.round(rec.duration)} s`);
    }
    const dir = String(rec.path || "").includes("/") ? rec.path.slice(0, rec.path.lastIndexOf("/")) : "";
    if (dir) {
      const folders = await opts.store.listFolders(rec.spaceId);
      const parent = folders.find((f) => f.path === dir);
      row("Folder", parent ? h("a", { href: app.hrefFor("folder", parent), text: dir }) : dir);
    } else if (opts.space) {
      // The app header links to the space, so the top level is named without a link.
      row("Folder", opts.space.name);
    }
    if (kind === "folder") {
      const { folders, notes, files } = await opts.store.listItems(rec.spaceId);
      const inside = (r) => !r.deleted && String(r.path).startsWith(rec.path + "/");
      const f = folders.filter(inside).length;
      const n = notes.filter(inside).length;
      const bytes = files.filter(inside).reduce((sum, x) => sum + (x.size || 0), 0);
      const fileCount = files.filter(inside).length;
      row("Contains", `${f} ${f === 1 ? "folder" : "folders"}, ${n} ${n === 1 ? "note" : "notes"}, ${fileCount} ${fileCount === 1 ? "file" : "files"} (${formatBytes(bytes)})`);
    }
    if (rec.createdAt) row("Created", relativeTime(rec.createdAt, { precise: true }));
    if (rec.updatedAt || rec.modTime) row("Updated", relativeTime(rec.updatedAt || rec.modTime, { precise: true }));
    if (kind === "file") {
      row("Versions", String(rec.versions || 0));
      if (rec.sha256) row("SHA-256", h("code", { class: "ms-hash", text: rec.sha256 }));
    }
    row("Access", accessLabel(opts.access));
    const parts = [];
    if (opts.offline) {
      parts.push(banner({ variant: "warning", title: "Not on this device", description: "This file opens when you are online. It is not saved on this device.", layout: "compact" }));
    }
    parts.push(h("section", { class: "list-section" }, h("div", { class: "section-heading" }, h("h3", { class: "section-heading-title", text: "Details" })), dl));
    if (opts.fileCache && !rec.provisional) parts.push(offlineSection(opts, kind, rec, (fn) => (offUnsub = fn)));
    const copy = button({ label: "Copy link", size: "small", leadingVisual: "link" });
    copy.addEventListener("click", async () => {
      const url = location.origin + "/i/" + enc(rec.id);
      try {
        await navigator.clipboard.writeText(url);
        copyFeedback(copy);
      } catch {
        announce("The link could not be copied. It is " + url);
      }
    });
    parts.push(h("section", { class: "list-section" },
      h("div", { class: "section-heading" },
        h("h3", { class: "section-heading-title", text: "Link" }),
        h("p", { class: "section-heading-description", text: "The link keeps working when the item moves or is renamed." })),
      h("div", {}, copy)));
    body.replaceChildren(...parts);
  });
  return {
    unmount() {
      if (offUnsub) offUnsub();
      panel.unmount();
    },
  };
}

/** Builds the Keep available offline setting of an item: a ToggleSwitch that applies at once. */
function offlineSection(opts, kind, rec, keepUnsub) {
  const cache = opts.fileCache;
  const labelId = `offline-label-${rec.id}`;
  const noteId = `offline-note-${rec.id}`;
  const note = h("p", { class: "section-heading-description", id: noteId });
  const pinOf = () => cache.isPinned({ kind, id: rec.id }) || {};
  const toggle = toggleSwitch({
    checked: !!pinOf().pinned,
    labelledBy: labelId,
    describedBy: noteId,
    onChange: async (on) => {
      if (on) await cache.pin({ kind, id: rec.id });
      else await cache.unpin({ kind, id: rec.id });
      announce(on ? "Kept available offline" : "No longer kept offline");
      draw();
    },
  });
  const draw = () => {
    const pin = pinOf();
    toggle.setChecked(!!pin.pinned);
    const inherited = !!(pin.pinned && pin.via && pin.via !== rec.id);
    toggle.button.setAttribute("aria-disabled", inherited ? "true" : "false");
    if (inherited) {
      note.textContent = "Kept offline with a folder above it.";
    } else if (kind === "file") {
      const st = cache.status(rec.id) || {};
      note.textContent = st.cached ? "Saved on this device." : pin.pinned ? "Downloads when online." : "Not saved on this device.";
    } else {
      note.textContent = pin.pinned ? "Every file in this folder, and each file added later, stays on this device." : "Files open from the network and stay only while there is room.";
    }
  };
  if (typeof cache.on === "function") keepUnsub(cache.on("changed", draw));
  draw();
  return h("section", { class: "list-section ms-offline-setting" },
    h("div", { class: "ms-toggle-row" },
      h("div", { class: "ms-toggle-text" }, h("h3", { class: "section-heading-title", id: labelId, text: "Keep available offline" }), note),
      toggle));
}

/* ---------- Comments, History and Share ---------- */

/**
 * Opens the Comments panel of a folder or file. The threads carry no anchor
 * and cache in the store for offline reading.
 * @param {HTMLElement} container
 * @param {object} opts
 */
export function openItemComments(container, opts) {
  const item = opts.item || {};
  return openComments(container, { ...itemPanelOptions(opts), note: { id: item.id, spaceId: item.spaceId, deleted: !!item.deleted, content: null } });
}

/**
 * Opens the History panel of a file: its stored versions, each with Download
 * and Restore, and Replace for people who can edit.
 * @param {HTMLElement} container
 * @param {object} opts
 */
export function openFileHistory(container, opts) {
  const item = opts.item || {};
  return openHistory(container, { ...opts, kind: "file", route: `/api/files/${enc(item.id)}` });
}

/**
 * Opens the Share panel of a folder or file.
 * @param {HTMLElement} container
 * @param {object} opts
 */
export function openItemShare(container, opts) {
  return openShare(container, itemPanelOptions(opts));
}

/* ---------- Backlinks ---------- */

/**
 * Opens the Backlinks panel: the notes that link to or embed the item, from
 * the local store, so it works offline.
 * @param {HTMLElement} container
 * @param {object} opts
 */
export function openItemBacklinks(container, opts) {
  return createPanel(container, opts, { title: "Backlinks", className: "item-backlinks", offlineOk: true }, async (body) => {
    const links = (await opts.store.backlinks(opts.item.id)).filter((b) => b.note && !b.note.deleted);
    if (typeof opts.onCount === "function") opts.onCount(links.length);
    if (!links.length) {
      body.replaceChildren(emptyState({ icon: "link", title: "No backlinks", text: "Notes that link to this item appear here." }));
      return;
    }
    const rows = links.map((b) =>
      listRow({
        lead: h("span", { class: "list-row-glyph" }, icon("note", 16)),
        title: h("a", { href: opts.app.hrefFor("note", b.note), dataset: { noteId: b.note.id }, text: b.note.title || "Untitled" }),
        sub: b.context || "",
        wrapSub: true,
      })
    );
    body.replaceChildren(listSection({ title: "Linked from", count: rows.length, rows }).root);
  });
}

/* ---------- Files of a note ---------- */

/**
 * Lists the files a note links to or embeds, resolved in the local store.
 * @param {object} store
 * @param {object} note
 * @returns {Promise<object[]>} file records in order of first link
 */
export async function filesLinkedFrom(store, note) {
  let links = [];
  try {
    links = parseLinks(String(note.content || ""));
  } catch {
    return [];
  }
  const seen = new Set();
  const out = [];
  for (const l of links) {
    if (l.external) continue;
    const hit = await store.resolveLink(note.spaceId, note.id, l.target, { form: l.form, decoded: true }).catch(() => null);
    if (!hit || hit.kind !== "file" || seen.has(hit.id)) continue;
    seen.add(hit.id);
    out.push(hit.item.file);
  }
  return out;
}

/**
 * Builds the markdown link of an uploaded file. An image becomes an embed.
 * @param {string} name
 * @param {string} url
 * @param {boolean} image
 * @returns {string}
 */
export function fileLink(name, url, image) {
  const label = String(name || "file").replace(/[[\]\n]/g, "");
  const safe = String(url).replace(/ /g, "%20").replace(/\(/g, "%28").replace(/\)/g, "%29");
  return (image ? "!" : "") + "[" + label + "](" + safe + ")";
}

/**
 * Opens the Files tab of a note (docs/primer.md 5.10): the files the note links
 * to as Box rows with a thumbnail or file type Octicon, the uploads of the
 * note, and the drop area. In the editor an upload inserts a link at the
 * cursor. In reading view it appends the link to the note. Uploads queue
 * while offline.
 * @param {HTMLElement} container
 * @param {{note: object, access: string, store: object, sync: object, hrefFor?: Function, insertText?: Function, isOnline?: Function, onCount?: Function}} opts
 */
export function openNoteFiles(container, opts) {
  const note = () => opts.note;
  const canEdit = atLeast(opts.access, "edit") && !note().deleted;
  let uploads = null;
  let unsubscribe = null;
  let doneKeys = null;
  let reloadTimer = 0;

  const panel = createPanel(container, opts, { title: "Files", className: "ms-files", offlineOk: true }, async (body) => {
    const files = await filesLinkedFrom(opts.store, note());
    if (typeof opts.onCount === "function") opts.onCount(files.length);
    if (!files.length) {
      body.replaceChildren(emptyState({
        icon: "paperclip",
        title: "No files yet",
        text: canEdit ? "Files this note links to appear here. Files you add upload next to the note." : "This note links to no files.",
      }));
      return;
    }
    body.replaceChildren(listSection({ title: "Linked files", count: files.length, className: "ms-file-list", rows: files.map(fileRow) }).root);
  });
  const { ctx } = panel;

  function fileRow(f) {
    const mime = String(f.mime || "").split(";")[0].trim();
    const image = /^image\//.test(mime);
    const href = typeof opts.hrefFor === "function" ? opts.hrefFor("file", f) : f.url;
    const thumb = image && f.url && !f.pendingKey
      ? h("img", { class: "ms-file-thumb", src: f.url, alt: "", loading: "lazy" })
      : h("span", { class: "ms-file-thumb is-generic", "aria-hidden": "true" }, icon(fileIconName(mime, f.name), 16));
    const sub = h("span", {}, formatBytes(f.size || 0), f.pendingKey ? " · Waiting to upload" : f.updatedAt ? " · " : "", !f.pendingKey && f.updatedAt ? relativeTime(f.updatedAt) : null);
    const menu = rowMenu("Actions for " + f.name, () => [
      canEdit && typeof opts.insertText === "function" && f.url
        ? { label: "Insert into the note", icon: "plus", onClick: () => { opts.insertText(fileLink(f.name, f.url, image)); announce("Inserted " + f.name); } }
        : null,
      f.url ? { label: "Copy link", icon: "link", onClick: () => copyLink(new URL(f.url, location.origin).href) } : null,
      f.url && !f.pendingKey ? { label: "Download", icon: "download", href: `${f.url}${f.url.includes("?") ? "&" : "?"}download=1` } : null,
    ]);
    return listRow({
      lead: thumb,
      title: h("a", { href, text: f.name }),
      sub,
      trail: menu,
      className: "ms-file",
      attrs: { "data-file-id": f.id },
    });
  }

  async function copyLink(url) {
    try {
      await navigator.clipboard.writeText(url);
      announce("Copied");
    } catch {
      announce("The link could not be copied.");
    }
  }

  // The upload list and the drop area sit in the foot, under the file list,
  // so reloading the list does not rebuild them.
  const foot = ctx.shell.foot;
  if (opts.sync && typeof opts.sync.uploads === "function") {
    uploads = uploadList({ sync: opts.sync, noteId: note().id, emptyText: "" });
    const wrap = h("section", { class: "list-section ms-uploads", hidden: true },
      h("div", { class: "section-heading" }, h("h3", { class: "section-heading-title", text: "Uploading" })),
      uploads);
    foot.appendChild(wrap);
    const update = (list) => {
      const mine = (Array.isArray(list) ? list : []).filter((u) => u.noteId === note().id);
      wrap.hidden = !mine.some((u) => u.state !== "done");
      const done = new Set(mine.filter((u) => u.state === "done").map((u) => u.key));
      if (doneKeys && [...done].some((k) => !doneKeys.has(k))) {
        clearTimeout(reloadTimer);
        reloadTimer = setTimeout(() => ctx.reload(), 400);
      }
      doneKeys = done;
    };
    update(opts.sync.uploads());
    if (typeof opts.sync.on === "function") {
      unsubscribe = opts.sync.on("uploads-changed", (ev) => update(ev && ev.uploads ? ev.uploads : opts.sync.uploads()));
    }
  }
  if (canEdit && opts.sync && typeof opts.sync.uploadAttachment === "function") {
    const zone = createDropzone({
      multiple: true,
      label: "Drop files here",
      onFiles: async (files) => {
        const added = [];
        for (const file of files) {
          try {
            const res = await opts.sync.uploadAttachment(note().id, file);
            added.push(fileLink(res.name || file.name, res.url, /^image\//.test(file.type || "")));
          } catch (err) {
            ctx.banner({ variant: "critical", title: `${file.name} could not be uploaded`, description: (err && err.message) || "" });
          }
        }
        if (!added.length) return;
        if (typeof opts.insertText === "function") {
          opts.insertText(added.join("\n") + "\n");
        } else if (typeof opts.sync.updateNote === "function") {
          const current = String(note().content || "");
          await opts.sync.updateNote(note().id, { content: current + (current.endsWith("\n") || !current ? "" : "\n") + "\n" + added.join("\n") + "\n" });
        }
        announce(added.length === 1 ? "Uploading 1 file" : `Uploading ${added.length} files`);
        ctx.reload();
      },
    });
    foot.appendChild(h("div", { class: "ms-dropzone" }, zone));
  }

  return {
    reload: () => ctx.reload(),
    unmount() {
      clearTimeout(reloadTimer);
      if (unsubscribe) unsubscribe();
      if (uploads && typeof uploads.destroy === "function") uploads.destroy();
      panel.unmount();
    },
  };
}

/* ---------- Rail ---------- */

/**
 * Builds the rail of a folder or file view. Each tab mounts its panel the
 * first time it opens.
 * @param {object} app
 * @param {{kind: string, rec: object, space: object, access: string, railTabs: Function, offline?: boolean}} options
 * @returns {{element: HTMLElement, select: Function, setCount: Function, destroy: Function}}
 */
export function buildItemRail(app, { kind, rec, space, access, railTabs, offline = false }) {
  const openers = { info: openItemInfo, comments: openItemComments, history: openFileHistory, share: openItemShare, backlinks: openItemBacklinks };
  const ids = itemRailTabIds({ kind, access });
  const handles = new Map();
  let rail = null;
  const opts = (id) => ({
    kind,
    item: rec,
    space,
    access,
    app,
    api: app.api,
    store: app.store,
    sync: app.sync,
    user: app.user,
    fileCache: app.fileCache,
    offline,
    isOnline: () => app.isOnline(),
    navigate: (url) => app.navigate(url),
    hrefFor: (k, r) => app.hrefFor(k, r),
    onCount: (n) => rail && rail.setCount(id, n),
  });
  const nodes = {};
  for (const id of ids) {
    const host = h("div", { class: "panel-host" });
    nodes[id] = {
      node: host,
      onSelect: () => {
        if (handles.has(id)) return;
        try {
          handles.set(id, openers[id](host, opts(id)));
        } catch (err) {
          console.error("[markspace] the " + id + " panel failed to open", err);
          host.replaceChildren(emptyState({ icon: "alert", title: "This panel could not open", text: "Reload the page to try again." }));
          handles.set(id, null);
        }
      },
    };
  }
  const defs = ITEM_RAIL_TABS.filter((d) => ids.includes(d.id));
  rail = railTabs(app, { defs, nodes, idsFor: () => ids });
  return {
    element: rail.element,
    select: (id, options) => rail.select(id, options),
    setCount: (id, n) => rail.setCount(id, n),
    destroy() {
      rail.destroy();
      for (const handle of handles.values()) {
        if (handle && typeof handle.unmount === "function") {
          try {
            handle.unmount();
          } catch (err) {
            console.error("[markspace] panel cleanup failed", err);
          }
        }
      }
    },
  };
}
