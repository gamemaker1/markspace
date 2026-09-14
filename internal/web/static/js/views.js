/**
 * Markspace shell views: home, space home, note view and editor, tag,
 * starred, recent, shared, trash and search. Each view renders from the
 * local store first, so a route change never waits on the network, and
 * returns a description the shell uses for the title, sidebar and rail.
 * Views follow docs/primer.md 5.2 (PageHeader, section headings, boxes and rows)
 * and 6 (patterns).
 *
 * A view returns {title, crumbs, space, noteId, rail, focus, ownsFocus, live,
 * mounted(), refresh(change), flush(), destroy(), onEdit()}. Every field is
 * optional.
 */

import {
  h,
  icon,
  announce,
  banner,
  blankslate,
  button,
  confirmDialog,
  counterLabel,
  editInPlace,
  formControl,
  iconButton,
  inlineMessage,
  label as uiLabel,
  openMenu,
  prefersReducedMotion,
  relativeTime,
  setCount,
  spinner,
  toggleSwitch,
  token,
  uid,
} from "./ui.js";
import {
  accessAtLeast,
  buildBreadcrumbs,
  filesUrl,
  indexById,
  makeLinkResolver,
  noteHref,
  searchUrl,
  spaceAccess,
  spaceUrl,
  stripMd,
  tagUrl,
  toggleTask,
  wordCount,
} from "./shell.js";
import { buildItemResolver, crumbsFor, fileView, filesPageView, folderView, itemRedirectView, newMenuButton, spaceContents } from "./fileviews.js";
import { itemMenuItems } from "./filetree.js";

/* ---------- Pure helpers ---------- */

/**
 * Counts tags across notes, most used first, then by name.
 * @param {{tags?: string[], deleted?: boolean}[]} notes
 * @returns {{tag: string, count: number}[]}
 */
export function groupTags(notes) {
  const counts = new Map();
  for (const note of notes || []) {
    if (!note || note.deleted) {
      continue;
    }
    for (const tag of new Set(note.tags || [])) {
      counts.set(tag, (counts.get(tag) || 0) + 1);
    }
  }
  return Array.from(counts, ([tag, count]) => ({ tag, count })).sort((a, b) => b.count - a.count || a.tag.localeCompare(b.tag));
}

/**
 * Sorts notes by position, then title, for page lists.
 * @param {object[]} notes
 * @returns {object[]} a new array.
 */
export function byPosition(notes) {
  return (notes || []).slice().sort((a, b) => (a.position || 0) - (b.position || 0) || String(a.title || "").localeCompare(String(b.title || "")));
}

/**
 * Sorts notes by last update, newest first.
 * @param {object[]} notes
 * @returns {object[]} a new array.
 */
export function byUpdated(notes) {
  const time = (note) => Date.parse(note.updatedAt || note.createdAt || 0) || 0;
  return (notes || []).slice().sort((a, b) => time(b) - time(a));
}

/**
 * Classifies an attachment for a files filter.
 * @param {string} mime
 * @param {string} [name]
 * @returns {"image" | "video" | "audio" | "document" | "archive" | "other"}
 */
export function attachmentKind(mime, name = "") {
  const type = String(mime || "").toLowerCase();
  const ext = String(name || "").toLowerCase().split(".").pop();
  if (type.startsWith("image/")) {
    return "image";
  }
  if (type.startsWith("video/")) {
    return "video";
  }
  if (type.startsWith("audio/")) {
    return "audio";
  }
  if (/zip|tar|gzip|compressed|x-7z|rar/.test(type) || ["zip", "tar", "gz", "tgz", "7z", "rar"].includes(ext)) {
    return "archive";
  }
  if (
    type.startsWith("text/") ||
    /pdf|msword|officedocument|opendocument|rtf|json|xml|csv/.test(type) ||
    ["pdf", "md", "txt", "doc", "docx", "odt", "xls", "xlsx", "ods", "ppt", "pptx", "csv", "json"].includes(ext)
  ) {
    return "document";
  }
  return "other";
}

/**
 * Filters attachments by a name query and a kind.
 * @param {{name: string, mime?: string}[]} list
 * @param {{query?: string, kind?: string}} filter kind "all" keeps every kind.
 * @returns {object[]}
 */
export function filterAttachments(list, { query = "", kind = "all" } = {}) {
  const q = String(query || "").trim().toLowerCase();
  return (list || []).filter((att) => {
    if (kind && kind !== "all" && attachmentKind(att.mime, att.name) !== kind) {
      return false;
    }
    return !q || String(att.name || "").toLowerCase().includes(q);
  });
}

/**
 * Splits an escaped search snippet with <mark> tags into text segments, so a
 * server snippet can be shown without innerHTML.
 * @param {string} snippet
 * @returns {{text: string, mark: boolean}[]}
 */
export function parseMarked(snippet) {
  const out = [];
  const entities = { "&amp;": "&", "&lt;": "<", "&gt;": ">", "&quot;": '"', "&#39;": "'", "&#x27;": "'", "&#34;": '"' };
  // Strip real tags first, then decode, so escaped text keeps its brackets.
  const decode = (text) => text.replace(/<[^>]*>/g, "").replace(/&(amp|lt|gt|quot|#39|#x27|#34);/g, (m) => entities[m] || m);
  let mark = false;
  for (const part of String(snippet || "").split(/(<\/?mark>)/i)) {
    if (/^<mark>$/i.test(part)) {
      mark = true;
    } else if (/^<\/mark>$/i.test(part)) {
      mark = false;
    } else if (part) {
      out.push({ text: decode(part), mark });
    }
  }
  return out;
}

/**
 * Labels an access level for a reader.
 * @param {string} access
 * @returns {string}
 */
export function accessLabel(access) {
  return { view: "Can view", comment: "Can comment", edit: "Can edit", manage: "Can manage" }[access] || "No access";
}

/**
 * Picks the rail tab to show: the saved one when the rail has it, otherwise
 * the first tab.
 * @param {string | null} saved
 * @param {string[]} available tab ids in order.
 * @returns {string} "" when there are no tabs.
 */
export function pickRailTab(saved, available) {
  const ids = available || [];
  return ids.includes(saved) ? saved : ids[0] || "";
}

/**
 * Collapses a breadcrumb trail longer than 4 items: the root stays, the
 * middle items go into a menu and the last two stay (docs/primer.md 5.1).
 * @param {{label: string, href: string | null}[]} crumbs
 * @returns {{visible: object[], hidden: object[]}} hidden sits after the first visible item.
 */
export function collapseCrumbs(crumbs) {
  const list = crumbs || [];
  if (list.length <= 4) {
    return { visible: list.slice(), hidden: [] };
  }
  return { visible: [list[0], ...list.slice(-2)], hidden: list.slice(1, -2) };
}

/* ---------- DOM helpers ---------- */

/** Resolves a store call, logging and returning a fallback on failure. */
async function safe(fn, fallback) {
  try {
    const value = await fn();
    return value === undefined || value === null ? fallback : value;
  } catch (err) {
    console.error("[markspace] store read failed", err);
    return fallback;
  }
}

/** Reports whether the sync engine has never finished a pull. */
function neverSynced(app) {
  const status = app.syncStatus();
  return !(status && status.lastSyncAt);
}

/** Builds Primer Breadcrumbs: a nav with an ordered list, the last item current. */
function breadcrumbs(crumbs) {
  const { visible, hidden } = collapseCrumbs(crumbs);
  const list = h("ol", { class: "breadcrumbs-list" });
  visible.forEach((crumb, index) => {
    const last = index === visible.length - 1;
    list.appendChild(
      h(
        "li",
        { class: "breadcrumbs-item" },
        crumb.href && !last ? h("a", { class: "breadcrumbs-link", href: crumb.href, text: crumb.label }) : h("span", { class: "breadcrumbs-link", "aria-current": last ? "page" : null, text: crumb.label })
      )
    );
    if (index === 0 && hidden.length) {
      const more = iconButton({
        icon: "kebab-horizontal",
        label: "More breadcrumb items",
        size: "small",
        tooltip: false,
        onClick: (event) => openMenu(event.currentTarget, hidden.map((item) => ({ label: item.label, href: item.href || undefined })), { label: "Breadcrumbs" }),
      });
      list.appendChild(h("li", { class: "breadcrumbs-item" }, more));
    }
  });
  return h("nav", { class: "breadcrumbs", "aria-label": "Breadcrumbs" }, list);
}

/**
 * Builds a Primer PageHeader (docs/primer.md 5.2). Actions are specs. On narrow
 * viewports the primary action stays as Button large, IconButtons marked
 * keep stay large, and every other action moves into one kebab ActionMenu.
 * @param {{title: string, titleClass?: string, visual?: Node | string | null, parent?: {label: string, href: string} | null, crumbs?: object[] | null, description?: Node | string | null, trailing?: Node | null, actions?: object[], more?: (() => object[]) | null, moreLabel?: string, large?: boolean, border?: boolean, navigation?: Node | null}} options
 * @returns {HTMLElement}
 */
function pageHeader({ title, titleClass = "", visual = null, parent = null, crumbs = null, description = null, trailing = null, actions = [], more = null, moreLabel = "More actions", large = false, border = false, navigation = null }) {
  const regular = h("div", { class: "page-header-actions-regular ms-regular-only" });
  const narrow = h("div", { class: "page-header-actions-narrow ms-narrow-only" });
  const overflow = [];
  for (const spec of actions.filter(Boolean)) {
    if (spec instanceof Node) {
      regular.appendChild(spec);
      continue;
    }
    const build = (size) => {
      let el;
      if (spec.iconOnly) {
        // A counter is visible text, so the button takes the default variant with its border (docs/primer.md 5.4).
        el = iconButton({ icon: spec.icon, label: spec.label, size, variant: spec.count !== undefined ? "default" : "invisible", pressed: spec.pressed, className: spec.className || "", attrs: spec.attrs || {}, onClick: spec.onClick });
        if (spec.count !== undefined) {
          const counter = counterLabel(spec.count || null);
          el.appendChild(counter);
          el.classList.add("has-counter");
          if (spec.onCounter) {
            spec.onCounter(counter);
          }
        }
      } else {
        el = button({ label: spec.label, variant: spec.primary ? "primary" : spec.variant || "default", size, leadingVisual: spec.icon, href: spec.href, className: spec.className || "", attrs: spec.attrs || {}, onClick: spec.onClick });
      }
      if (spec.onElement) {
        spec.onElement(el, size);
      }
      return el;
    };
    if (spec.regular !== false) {
      regular.appendChild(build("medium"));
    }
    if (spec.primary || spec.narrow === "keep") {
      narrow.appendChild(build("large"));
    } else if (spec.narrow !== "hide") {
      overflow.push(spec);
    }
  }
  if (more) {
    regular.appendChild(iconButton({ icon: "kebab-horizontal", label: moreLabel, attrs: { "aria-haspopup": "true" }, onClick: (event) => openMenu(event.currentTarget, more(), { placement: "bottom-end", label: moreLabel }) }));
  }
  if (overflow.length || more) {
    narrow.appendChild(
      iconButton({
        icon: "kebab-horizontal",
        label: moreLabel,
        size: "large",
        tooltip: false,
        attrs: { "aria-haspopup": "true" },
        onClick: (event) => {
          const items = overflow.map((spec) => ({ label: spec.menuLabel || spec.label, icon: spec.icon, href: spec.href, checked: spec.checked, selectionType: spec.checked !== undefined ? "multiple" : undefined, onClick: spec.onClick }));
          const extra = more ? more() : [];
          if (items.length && extra.length) {
            items.push({ separator: true });
          }
          openMenu(event.currentTarget, [...items, ...extra], { placement: "bottom-end", label: moreLabel });
        },
      })
    );
  }
  const heading = h("h1", { class: ("page-header-title " + titleClass).trim(), tabindex: "-1" }, typeof title === "string" ? h("span", { class: "page-header-title-text", text: title }) : title);
  return h(
    "div",
    { class: "page-header" + (large ? " page-header-large" : ""), "data-border": border ? "true" : null },
    parent ? h("div", { class: "page-header-context" }, h("a", { class: "page-header-parent", href: parent.href }, icon("arrow-left", 16), h("span", { text: parent.label }))) : null,
    crumbs && crumbs.length > 1 ? h("div", { class: "page-header-breadcrumbs ms-regular-only" }, breadcrumbs(crumbs)) : null,
    visual ? h("div", { class: "page-header-leading-visual", "aria-hidden": "true" }, typeof visual === "string" ? h("span", { class: "page-header-emoji", text: visual }) : visual) : null,
    h("div", { class: "page-header-title-area" }, heading, trailing),
    regular.childNodes.length || narrow.childNodes.length ? h("div", { class: "page-header-actions" }, regular, narrow) : null,
    description ? h("div", { class: "page-header-description" }, description) : null,
    navigation ? h("div", { class: "page-header-navigation" }, navigation) : null
  );
}

/** Older page head used by fileviews.js: a PageHeader with a subtitle and action nodes. */
function pageHead(title, { glyph = null, subtitle = "", actions = [] } = {}) {
  return pageHeader({ title, visual: glyph, description: subtitle || null, actions, border: true });
}

/**
 * Builds a section heading: an h2 at title small, an optional CounterLabel
 * and actions at the end (docs/primer.md 5.2).
 */
function section(title, children, { count = null, actions = [], description = "" } = {}) {
  const id = uid("section");
  return h(
    "section",
    { class: "view-section", "aria-labelledby": id },
    h(
      "div",
      { class: "section-heading" },
      h("h2", { class: "section-heading-title", id }, h("span", { text: title }), count !== null ? counterLabel(count) : null),
      actions.length ? h("div", { class: "section-heading-actions" }, actions) : null
    ),
    description ? h("p", { class: "section-heading-description", text: description }) : null,
    children
  );
}

/**
 * Builds a Blankslate for the helpers contract of fileviews.js. actions are
 * nodes. level "h1" makes the heading the page heading.
 */
function emptyState({ glyph = "file", title, text = "", actions = [], level = "h2", border = false }) {
  const el = blankslate({ icon: glyph ? "note" : "", title, description: text, size: level === "h1" ? "spacious" : "medium", border });
  const visual = el.querySelector(".blankslate-visual");
  if (visual && glyph) {
    visual.replaceChildren(icon(glyph, 24));
  }
  const heading = el.querySelector(".blankslate-heading");
  if (level === "h1" && heading) {
    heading.replaceWith(h("h1", { class: heading.className, tabindex: "-1", text: heading.textContent }));
  }
  const nodes = (actions || []).filter(Boolean);
  if (nodes.length) {
    el.querySelector(".blankslate-inner").appendChild(h("div", { class: "blankslate-action" }, nodes));
  }
  return el;
}

/** A Blankslate that is the only content of a page, with an h1 heading. */
function pageBlankslate(options) {
  const el = blankslate(Object.assign({ size: "spacious" }, options));
  const heading = el.querySelector(".blankslate-heading");
  if (heading) {
    heading.replaceWith(h("h1", { class: heading.className, tabindex: "-1", text: heading.textContent }));
  }
  return el;
}

/** Builds the loading state shown before the first sync finishes (docs/primer.md 6.5). */
function loadingState(text) {
  const el = pageBlankslate({ title: text, description: "Your pages are being copied to this device. After that they open at once, even offline." });
  el.setAttribute("aria-busy", "true");
  const inner = el.querySelector(".blankslate-inner");
  inner.prepend(h("span", { class: "blankslate-visual" }, spinner({ size: "medium", label: "", delay: 1000 })));
  return el;
}

/** Formats a time as a RelativeTime element with an optional prefix. */
function timeText(value, prefix = "") {
  const date = value ? new Date(value) : null;
  if (!date || Number.isNaN(date.getTime())) {
    return h("span", { text: prefix + "unknown" });
  }
  return h("span", { class: "time-text" }, prefix, relativeTime(date));
}

/** Builds the leading visual of a note. */
function noteGlyph(note) {
  return note.icon ? h("span", { class: "view-emoji", text: note.icon }) : icon("note", 16);
}

/**
 * Builds an ActionList of note links (docs/primer.md 5.5 ActionList). Each item
 * shows the title, and a block description with the space, the update time
 * and a search snippet.
 */
function noteList(app, notes, { showSpace = false, meta = null, snippet = null, label = "" } = {}) {
  return h(
    "ul",
    { class: "action-list view-list", "aria-label": label || null },
    notes.map((note) => {
      const space = app.spaceBy(note.spaceId);
      const description = h("span", { class: "action-list-description" }, showSpace && space ? space.name + " · " : "", meta ? meta(note) : timeText(note.updatedAt, "Updated "));
      const extra = snippet ? snippet(note) : null;
      return h(
        "li",
        { class: "action-list-row" },
        h(
          "a",
          { class: "action-list-item", href: noteHref(note, space && space.slug), dataset: { noteId: note.id } },
          h("span", { class: "action-list-leading", "aria-hidden": "true" }, noteGlyph(note)),
          h("span", { class: "action-list-text has-block-description" }, h("span", { class: "action-list-label", text: note.title || "Untitled" }), description, extra)
        )
      );
    })
  );
}

/** Builds Tokens that link to tag pages. */
function tagTokens(slug, tags, { counts = false } = {}) {
  return h(
    "ul",
    { class: "view-tokens", "aria-label": "Tags" },
    tags.map((entry) => {
      const tag = typeof entry === "string" ? entry : entry.tag;
      const text = "#" + tag + (counts && entry.count ? " " + entry.count : "");
      return h("li", {}, token(text, { href: tagUrl(slug, tag) }));
    })
  );
}

/** Builds the view for a space slug that is not on this device. */
function missingSpace(app, el, slug) {
  if (!app.spaces.length && neverSynced(app) && app.isOnline()) {
    el.appendChild(h("div", { class: "page" }, loadingState("Getting your pages")));
    return { title: "Loading", live: true };
  }
  el.appendChild(
    h(
      "div",
      { class: "page" },
      pageBlankslate({
        icon: "file-directory-fill",
        title: "This space is not here",
        description: "“" + slug + "” does not exist, or you are not a member of it.",
        primaryAction: { label: "Go home", href: "/" },
      })
    )
  );
  return { title: "Space not found", crumbs: [{ label: "Not found", href: null }] };
}

/* ---------- Router entry ---------- */

/**
 * Renders a route into a container and returns the view description.
 * @param {object} app the running shell.
 * @param {{name: string, params: object}} route
 * @param {HTMLElement} el detached container the shell swaps in.
 * @param {{state?: object, refresh?: boolean}} [options]
 * @returns {Promise<object>}
 */
export async function renderView(app, route, el, options = {}) {
  const helpers = { pageHead, pageHeader, emptyState, timeText, railTabs, noteView, loadingState, neverSynced, missingSpace };
  switch (route.name) {
    case "folder":
      return folderView(app, route.params, el, { ...options, helpers });
    case "file":
      return fileView(app, route.params, el, { ...options, helpers });
    case "item":
      return itemRedirectView(app, route.params, el, { ...options, helpers });
    case "files":
      return filesPageView(app, route.params, el, { ...options, helpers });
    default:
      break;
  }
  const params = route.params || {};
  switch (route.name) {
    case "home":
      return homeView(app, el);
    case "space":
      return spaceView(app, params, el);
    case "note":
      return noteView(app, params, el, options);
    case "permalink":
      return noteView(app, { id: params.id, edit: params.edit }, el, options);
    case "tag":
      return tagView(app, params, el);
    case "starred":
    case "recent":
    case "shared":
      return listView(app, route.name, el);
    case "trash":
      return trashView(app, params, el);
    case "search":
      return searchView(app, params, el);
  }
  el.appendChild(h("div", { class: "page" }, pageBlankslate({ icon: "alert", title: "Page not found", primaryAction: { label: "Go home", href: "/" } })));
  return { title: "Not found" };
}

/* ---------- Home ---------- */

async function homeView(app, el) {
  const page = h("div", { class: "page" });
  if (!app.spaces.length) {
    if (neverSynced(app) && app.isOnline()) {
      page.appendChild(loadingState("Getting your pages"));
    } else if (neverSynced(app)) {
      page.appendChild(
        pageBlankslate({
          icon: "cloud-offline",
          title: "Your pages appear after the first sync",
          description: "Connect to the internet once, and this device keeps a copy of every page you can open.",
        })
      );
    } else {
      page.appendChild(
        pageBlankslate({
          icon: "file-directory-fill",
          title: "Create your first space",
          description: "A space holds related pages, folders and files.",
          primaryAction: { label: "New space", onClick: () => app.newSpace() },
        })
      );
    }
    el.appendChild(page);
    return { title: "Home", live: true };
  }

  const [recent, starred] = await Promise.all([safe(() => app.store.recentNotes(10), []), safe(() => app.store.starredNotes(), [])]);
  page.appendChild(pageHeader({ title: "Home", border: true, actions: [{ label: "New space", onClick: () => app.newSpace() }] }));
  const liveStarred = byUpdated(starred.filter((note) => !note.deleted));
  if (liveStarred.length) {
    page.appendChild(section("Starred", noteList(app, liveStarred.slice(0, 8), { showSpace: true }), { count: liveStarred.length }));
  }
  const liveRecent = recent.filter((note) => !note.deleted);
  page.appendChild(
    section(
      "Recent",
      liveRecent.length ? noteList(app, liveRecent, { showSpace: true }) : blankslate({ icon: "clock", title: "No recent pages", description: "Pages you open or edit show here.", size: "small", border: true })
    )
  );
  const grid = h("ul", { class: "card-grid view-cards" });
  for (const space of app.spaces) {
    grid.appendChild(
      h(
        "li",
        {},
        h(
          "a",
          { class: "card", href: spaceUrl(space.slug) },
          h("span", { class: "card-icon", "aria-hidden": "true" }, space.icon ? space.icon : icon("file-directory-fill", 24)),
          h("span", { class: "card-title", text: space.name }),
          space.description ? h("span", { class: "card-foot", text: space.description }) : null
        )
      )
    );
  }
  page.appendChild(section("Spaces", grid, { count: app.spaces.length }));
  el.appendChild(page);
  return { title: "Home" };
}

/* ---------- Space home ---------- */

async function spaceView(app, params, el) {
  const space = app.spaceBy(params.space);
  if (!space) {
    return missingSpace(app, el, params.space);
  }
  const notes = (await safe(() => app.store.listNotes(space.id), [])).filter((note) => !note.deleted);
  const canEdit = accessAtLeast(spaceAccess(space, app.user), "edit");
  const page = h("div", { class: "page" });
  const actions = canEdit ? [{ label: "New page", icon: "plus", primary: true, attrs: { "data-new-page": "" }, onClick: () => app.createNote({ space }) }, newMenuButton(app, space, "")] : [];
  page.appendChild(pageHeader({ title: space.name, visual: space.icon || null, description: space.description || null, actions, border: true }));
  const contents = await spaceContents(app, space);

  if (!notes.length && !contents.count) {
    page.appendChild(
      blankslate({
        icon: "note",
        title: canEdit ? "Start your first page" : "This space is empty",
        description: canEdit ? "A page is saved on this device at once and syncs when a connection exists." : "Nobody has written anything here yet.",
        primaryAction: canEdit ? { label: "New page", onClick: () => app.createNote({ space }) } : null,
      })
    );
  } else {
    const columns = h("div", { class: "space-columns" });
    columns.appendChild(section("Contents", contents.element, { count: contents.count }));
    const side = h("div", { class: "space-side" });
    const recent = byUpdated(notes).slice(0, 6);
    if (recent.length) {
      side.appendChild(section("Recently updated", noteList(app, recent)));
    }
    const tags = groupTags(notes);
    if (tags.length) {
      side.appendChild(section("Tags", tagTokens(space.slug, tags, { counts: true }), { count: tags.length }));
    }
    columns.appendChild(side);
    page.appendChild(columns);
  }
  el.appendChild(page);
  return { title: space.name, crumbs: [{ label: space.name, href: null }], space };
}

/* ---------- Note ---------- */

/** Finds the note of a note route: by id, by the id in history, then by path. */
async function resolveNote(app, params, state) {
  if (params.id) {
    return safe(() => app.store.getNote(params.id), null);
  }
  const space = app.spaceBy(params.space);
  if (state && state.noteId) {
    const byId = await safe(() => app.store.getNote(state.noteId), null);
    if (byId) {
      return byId;
    }
  }
  if (!space) {
    return null;
  }
  let note = await safe(() => app.store.getNoteByPath(space.slug, params.path), null);
  if (!note) {
    note = await safe(() => app.store.getNoteByPath(space.slug, params.path + ".md"), null);
  }
  if (!note) {
    const all = await safe(() => app.store.listNotes(space.id, { includeDeleted: true }), []);
    note = all.find((n) => stripMd(n.path).toLowerCase() === params.path.toLowerCase()) || null;
  }
  return note;
}

async function noteView(app, params, el, { state = {}, refresh = false, keepUrl = false, header = null, extraBelowHeader = null } = {}) {
  const note = await resolveNote(app, params, state);
  if (!note) {
    if (neverSynced(app) && app.isOnline()) {
      el.appendChild(h("div", { class: "page" }, loadingState("Getting your pages")));
      return { title: "Loading", live: true };
    }
    const space = params.space ? app.spaceBy(params.space) : null;
    el.appendChild(
      h(
        "div",
        { class: "page" },
        pageBlankslate({
          icon: "note",
          title: "This page is not here",
          description: "It may have been deleted, moved to a space you cannot open, or not synced to this device yet.",
          primaryAction: { label: space ? "Go to " + space.name : "Go home", href: space ? spaceUrl(space.slug) : "/" },
        })
      )
    );
    return { title: "Page not found", space };
  }

  const space = app.spaceBy(note.spaceId) || (params.space ? app.spaceBy(params.space) : null);
  const slug = (space && space.slug) || note.spaceSlug;
  const access = app.accessFor(note);
  const canEdit = accessAtLeast(access, "edit") && !note.deleted;
  const wantEdit = !!params.edit;

  // Keep the address canonical and remember the id for later renames.
  const canonical = noteHref(note, slug, { edit: wantEdit && canEdit });
  const stateNow = Object.assign({}, history.state || {}, { noteId: note.id });
  if (!keepUrl && (location.pathname + location.search !== canonical || !(history.state && history.state.noteId === note.id))) {
    history.replaceState(stateNow, "", canonical + location.hash);
  }

  const spaceNotes = await safe(() => app.store.listNotes(note.spaceId), []);
  const byId = indexById(spaceNotes);
  byId.set(note.id, note);
  const spaceItems = space ? await safe(() => app.store.listItems(space.id), null) : null;
  const crumbs = space && spaceItems ? crumbsFor(app, space, spaceItems.folders, note.path, note.title || "Untitled") : buildBreadcrumbs({ space, note, byId });
  const comments = typeof app.commentsFor === "function" ? app.commentsFor(note, access) : null;

  if (wantEdit && canEdit) {
    // "Switch to Source to replace" in the note view's find bar passes its query, and the editor opens Replace with it.
    return editorView(app, { note, space, slug, access, crumbs, el, byId, comments, findState: state && state.find ? state.find : null });
  }

  /* Reading view. */
  const article = h("article", { class: "note-view" });
  const editHref = noteHref(note, slug, { edit: true });
  const openEditor = () => app.navigate(editHref, { state: { noteId: note.id } });
  const noteItem = { kind: "note", rec: note };
  const counters = [];
  const starButtons = [];

  const parentCrumb = crumbs.length > 1 ? crumbs[crumbs.length - 2] : null;
  const words = wordCount(note.content);
  const trailing = !accessAtLeast(access, "edit") ? uiLabel(access === "comment" ? "Can comment" : "Read only", { variant: "secondary" }) : null;
  /** Opens the full-screen version history Dialog of the History tab (docs/primer.md 5.10). */
  const openVersionHistory = () => {
    // The menu has closed and returned focus to its button, which takes focus back after the Dialog.
    const trigger = document.activeElement;
    const entry = typeof app.railPanel === "function" ? app.railPanel("history", note, access, { mode: "view" }) : null;
    if (!entry) {
      return;
    }
    app.activateRailPanel(entry);
    if (entry.handle && typeof entry.handle.openDialog === "function") {
      entry.handle.openDialog(trigger);
    }
  };
  // The Reading find bar of this view, created on first use. find.js loads with the CodeMirror bundle, so it loads late.
  let readingFind = null;
  let destroyed = false;
  /**
   * Opens Find. A mounted editor opens its own find bar through its handle. Without one, a Reading find bar marks
   * the rendered note, as the editor's Reading mode does (docs/primer.md 5.10 Find bar).
   */
  const findInNote = async () => {
    const editorEl = document.querySelector(".ms-editor");
    if (editorEl && editorEl.msHandle && typeof editorEl.msHandle.openFind === "function") {
      editorEl.msHandle.openFind();
      return;
    }
    if (!readingFind) {
      let mod = null;
      try {
        mod = await import("/static/js/find.js");
      } catch (err) {
        console.error("[markspace] find.js failed to load", err);
        return;
      }
      if (destroyed || readingFind) {
        return;
      }
      readingFind = mod.createFind({
        getMode: () => "reading",
        // "Switch to Source to replace" opens the editor in Source with the same query.
        setMode: (mode) => {
          try {
            localStorage.setItem("markspace.editor.mode", mode);
          } catch {
            /* storage unavailable */
          }
          app.navigate(editHref, { state: { noteId: note.id, find: readingFind.bar.state } });
        },
        canEdit,
        readRoot: body,
        readPane: body,
      });
      article.insertBefore(readingFind.element, body);
    }
    readingFind.open();
  };
  const moreItems = () => {
    const items = itemMenuItems(app, { space, item: noteItem, where: "view", skip: ["Open", "New note", "New folder", "Upload"] });
    if (canEdit) {
      const at = items.findIndex((item) => item.label === "Copy to");
      items.splice(at >= 0 ? at + 1 : items.length, 0, { label: "Duplicate", icon: "copy", onClick: () => app.duplicateNote(note) });
    }
    if (typeof app.railPanel === "function" && typeof app.railOpener === "function" && app.railOpener("history")) {
      const sep = items.findIndex((item) => item.separator);
      items.splice(sep >= 0 ? sep : items.length, 0, { label: "Version history", icon: "history", onClick: openVersionHistory });
    }
    return items;
  };
  const toggleStar = async () => {
    const next = !note.starred;
    note.starred = next;
    for (const el of starButtons) {
      el.setAttribute("aria-pressed", next ? "true" : "false");
      const glyph = el.querySelector(".btn-visual");
      if (glyph) {
        glyph.replaceChildren(icon(next ? "star-fill" : "star", 16));
      }
    }
    await app.setStar(note, next);
  };
  const noteHeader = pageHeader({
    title: note.title || "Untitled",
    titleClass: "note-title",
    large: true,
    visual: note.icon || null,
    // A page at the top of a space gets no parent link: the app header links to the space.
    parent: parentCrumb && parentCrumb.href && parentCrumb.href !== (space ? spaceUrl(space.slug) : "") ? { label: parentCrumb.label, href: parentCrumb.href } : null,
    crumbs,
    trailing,
    description: h(
      "span",
      { class: "note-description" },
      h("span", { text: words === 1 ? "1 word" : words + " words" }),
      h("span", { "aria-hidden": "true", text: "·" }),
      timeText(note.updatedAt, "Updated ")
    ),
    actions: [
      canEdit ? { label: "Edit", icon: "pencil", primary: true, href: editHref, attrs: { "data-note-id": note.id, "aria-keyshortcuts": "E" } } : null,
      {
        iconOnly: true,
        icon: note.starred ? "star-fill" : "star",
        label: "Star",
        pressed: !!note.starred,
        checked: !!note.starred,
        onClick: toggleStar,
        onElement: (el) => starButtons.push(el),
      },
      comments
        ? { iconOnly: true, icon: "comment", label: "Comments", count: comments.count || null, regular: false, onCounter: (c) => counters.push(c), onClick: () => showComments({}) }
        : null,
      // Narrow viewports keep only Edit and the kebab beside the title (docs/primer.md 5.2), so these open from the kebab menu.
      // Find in note is the only way to open Find on a phone without a hardware keyboard.
      { label: "Find in note", icon: "search", regular: false, onClick: () => findInNote() },
      { iconOnly: true, icon: "info", label: "Page details", regular: false, onClick: () => showPanel("info") },
      { iconOnly: true, icon: "sidebar-expand", label: "Page details", className: "is-mirrored", narrow: "hide", attrs: { "aria-controls": "rail", "aria-expanded": "false", "data-rail-toggle": "" }, onClick: () => app.toggleRail() },
    ],
    more: moreItems,
  });
  article.appendChild(noteHeader);

  if (note.deleted) {
    article.appendChild(
      banner({
        variant: "warning",
        title: "This page is in the trash",
        actions: accessAtLeast(access, "edit")
          ? [
              {
                label: "Restore",
                onClick: async () => {
                  try {
                    await app.sync.restoreNote(note.id);
                    announce("Restored");
                  } catch (err) {
                    banner({ variant: "critical", title: "The page could not be restored", into: article });
                  }
                },
              },
            ]
          : [],
      })
    );
  } else if (params.edit && !canEdit && !refresh) {
    article.appendChild(banner({ variant: "warning", title: "You can view this page but not edit it", dismissible: true }));
  }

  if (note.tags && note.tags.length) {
    article.appendChild(tagTokens(slug, note.tags));
  }

  // Pending uploads render from blobs stored on this device.
  const objectUrls = new Map();
  for (const match of String(note.content || "").matchAll(/markspace-pending:[^\s)"'<>\]]+/g)) {
    const url = match[0];
    if (objectUrls.has(url) || typeof app.store.getBlob !== "function") {
      continue;
    }
    const record = await safe(() => app.store.getBlob(url.slice("markspace-pending:".length)), null);
    const blob = record instanceof Blob ? record : record && record.blob instanceof Blob ? record.blob : null;
    if (blob) {
      objectUrls.set(url, URL.createObjectURL(blob));
    }
  }

  const body = h("div", { class: "md-body markdown-body" });
  let rendered = { toc: [] };
  let itemResolver = null;
  if (String(note.content || "").trim()) {
    itemResolver = await safe(() => buildItemResolver(app, note), null);
    rendered = app.renderMarkdown(
      body,
      note.content,
      itemResolver
        ? { resolveItem: itemResolver.resolveItem, resolveImage: (url) => objectUrls.get(url) || itemResolver.resolveImage(url), sourceMap: true }
        : { resolveLink: makeLinkResolver(Array.from(byId.values()), slug), resolveImage: (url) => objectUrls.get(url) || url, sourceMap: true }
    );
    if (itemResolver) {
      itemResolver.attach(body);
    }
  } else {
    // The empty state is not markdown. The markdown link colour would paint the primary button text.
    body.className = "note-empty";
    body.appendChild(
      blankslate({
        icon: "note",
        title: "This page is empty",
        // The PageHeader holds the one primary Edit (docs/primer.md 5.4).
        description: canEdit ? "Press E or choose Edit to start writing." : "",
        size: "small",
      })
    );
  }
  article.appendChild(body);

  // Task checkboxes toggle the markdown source in reading view.
  let suppressUntil = 0;
  const boxes = Array.from(body.querySelectorAll("li > input[type='checkbox']"));
  if (canEdit && boxes.length) {
    body.classList.add("tasks-editable");
    boxes.forEach((box, index) => {
      box.disabled = false;
      box.addEventListener("change", async () => {
        const next = toggleTask(note.content || "", index, box.checked);
        if (next === note.content) {
          return;
        }
        note.content = next;
        suppressUntil = Date.now() + 1500;
        try {
          await app.sync.updateNote(note.id, { content: next });
        } catch (err) {
          box.checked = !box.checked;
          banner({ variant: "critical", title: "The task could not be updated", into: article });
        }
      });
    });
  }

  // A link to a missing page creates it.
  body.addEventListener("click", (event) => {
    // A highlighted passage opens its thread, unless the click selected text or hit a link.
    const mark = event.target.closest("mark.comment-anchor");
    const selection = document.getSelection();
    if (mark && comments && !event.target.closest("a") && (!selection || selection.isCollapsed)) {
      showComments({ focusId: mark.dataset.commentId });
      return;
    }
    const link = event.target.closest("a.wikilink-new");
    if (!link) {
      return;
    }
    event.preventDefault();
    if (canEdit) {
      app.createNote({ space, title: link.textContent.trim() });
    } else {
      banner({ variant: "info", title: "“" + link.textContent.trim() + "” does not exist yet", dismissible: true, into: article });
    }
  });

  // Folder pages pass their own header and listing, placed above the note.
  if (header) {
    el.appendChild(header);
  }
  if (extraBelowHeader) {
    el.appendChild(extraBelowHeader);
  }
  el.appendChild(article);

  const rail = await buildRail(app, { note, space, slug, access, rendered, byId, comments });
  const panels = app.mods.panels || {};
  const anchors = app.mods.anchors || {};
  const content = String(note.content || "");
  let stopOutline = null;
  let undoMarks = null;
  let activeId = null;
  let revealTimer = 0;
  let unsubscribe = null;
  let stopSelection = null;

  /** Wraps the passages of open anchored threads in highlight marks. */
  const applyHighlights = () => {
    if (undoMarks) {
      try {
        undoMarks();
      } catch (err) {
        console.error("[markspace] removing comment highlights failed", err);
      }
      undoMarks = null;
    }
    if (!comments || typeof anchors.highlightAnchors !== "function" || typeof panels.groupThreads !== "function" || !content.trim()) {
      return;
    }
    const groups = panels.groupThreads(comments.source.threads, { body: content, resolve: anchors.resolveAnchor });
    const items = groups.anchored
      .filter((entry) => entry.start >= 0 && entry.end > entry.start)
      .map((entry) => ({ id: entry.thread.id, start: entry.start, end: entry.end, active: entry.thread.id === activeId }));
    if (!items.length) {
      return;
    }
    try {
      undoMarks = anchors.highlightAnchors(body, content, items);
    } catch (err) {
      console.error("[markspace] comment highlights failed", err);
    }
  };

  /** Shows the open thread count on the Comments tab and the PageHeader button. */
  const setCommentCount = (n) => {
    rail.setCount("comments", n);
    for (const counter of counters) {
      setCount(counter, n || null);
    }
  };

  /** Selects a rail tab and opens the rail. */
  const showPanel = (kind, extra = null) => {
    if (rail.select(kind, { extra })) {
      app.showRail({ focus: false });
      return true;
    }
    return false;
  };

  /** Opens the Comments tab, then focuses a thread or starts a comment on a passage. */
  const showComments = ({ focusId = null, draftAnchor = null } = {}) => {
    if (!comments || !comments.handle) {
      return;
    }
    rail.select("comments");
    app.showRail({ focus: false });
    if (draftAnchor) {
      comments.handle.draft(draftAnchor);
    } else if (focusId) {
      comments.handle.focus(focusId);
    }
  };
  if (comments) {
    setCommentCount(comments.count || 0);
  }

  return {
    title: note.title || "Untitled",
    crumbs,
    space,
    noteId: note.id,
    kind: "note",
    rail: rail.element,
    onEdit: canEdit ? openEditor : null,
    showComments,
    /** Opens a rail tab, with extra options such as an AI task. */
    showPanel,
    /** Renames the page in its PageHeader with Save and Cancel beside the field. */
    renameItem(id) {
      const text = noteHeader.querySelector(".page-header-title-text");
      if (id !== note.id || !canEdit || !text) {
        return null;
      }
      return editInPlace(text, { value: note.title || "", label: "Title", validate: (v) => (String(v).trim() ? null : "Enter a title"), onSave: (v) => app.sync.updateNote(note.id, { title: String(v).trim() }) });
    },
    onRailCount: (kind, n) => rail.setCount(kind, n),
    onAIState: () => rail.updateTabs(),
    commentBody: () => content,
    onCommentCount: setCommentCount,
    /** Emphasises a thread's passage. It does not scroll, so hovering a card never moves the page. */
    onCommentActivate(id) {
      if (id === activeId) {
        return;
      }
      activeId = id;
      applyHighlights();
    },
    /** Scrolls a thread's passage to the middle of the page, emphasises it and flashes it once. */
    onCommentReveal(id) {
      if (!id) {
        return;
      }
      if (id !== activeId) {
        activeId = id;
        applyHighlights();
      }
      const marks = [...body.querySelectorAll(`mark.comment-anchor[data-comment-id="${CSS.escape(id)}"]`)];
      if (!marks.length) {
        return;
      }
      marks[0].scrollIntoView({ block: "center", behavior: prefersReducedMotion() ? "auto" : "smooth" });
      clearTimeout(revealTimer);
      for (const mark of marks) {
        mark.classList.remove("is-flash");
        void mark.offsetWidth;
        mark.classList.add("is-flash");
      }
      revealTimer = setTimeout(() => {
        for (const mark of marks) {
          mark.classList.remove("is-flash");
        }
      }, 1300);
    },
    mounted() {
      if (state && state.panel && !refresh) {
        if (!showPanel(state.panel)) {
          app.pendingPanel = state.panel;
        }
      }
      stopOutline = rail.trackOutline();
      if (comments) {
        unsubscribe = comments.source.subscribe(() => applyHighlights());
        applyHighlights();
        if (accessAtLeast(access, "comment") && !note.deleted && content.trim()) {
          stopSelection = selectionCommenter(app, { body, content, onComment: (anchor) => showComments({ draftAnchor: anchor }) });
        }
      }
    },
    refresh({ ids = [], source } = {}) {
      if (source === "local" && ids.includes(note.id) && Date.now() < suppressUntil) {
        return undefined;
      }
      return app.refreshView();
    },
    destroy() {
      destroyed = true;
      if (readingFind) {
        readingFind.close({ focus: false });
        readingFind.destroy();
      }
      rail.destroy();
      if (stopOutline) {
        stopOutline();
      }
      if (unsubscribe) {
        unsubscribe();
      }
      if (stopSelection) {
        stopSelection();
      }
      for (const url of objectUrls.values()) {
        URL.revokeObjectURL(url);
      }
      if (itemResolver) {
        itemResolver.destroy();
      }
    },
  };
}

/** Builds a titled rail section with an h3 at title small. */
function railSection(title, content) {
  return h("section", { class: "rail-section" }, h("h3", { class: "section-heading-title", text: title }), content);
}

/** The rail tabs whose count shows as a CounterLabel: things that need action. */
const COUNT_TABS = new Set(["comments", "suggestions"]);

/** The rail tabs when panels.js did not load: the panels this file draws. */
const FALLBACK_RAIL_TABS = [
  { id: "outline", label: "Outline" },
  { id: "info", label: "Info" },
];

/** Moves along a tab list for an arrow, Home or End key when panels.js did not load. */
function fallbackTabKey(ids, current, key) {
  const n = ids.length;
  const i = ids.indexOf(current);
  if (!n) {
    return "";
  }
  if (key === "ArrowRight") {
    return ids[(i + 1) % n];
  }
  if (key === "ArrowLeft") {
    return ids[i <= 0 ? n - 1 : i - 1];
  }
  if (key === "Home") {
    return ids[0];
  }
  return key === "End" ? ids[n - 1] : "";
}

/**
 * Builds the rail tabs as Primer UnderlinePanels (docs/primer.md 5.1): text
 * labels, a CounterLabel where a count needs action, manual activation, and
 * one tab panel per tab. Left and Right move focus and wrap, Home and End go
 * to the ends, and Enter or Space shows the panel. idsFor(current) lists the
 * tabs to show, so the AI tab can hide while offline. The shell remembers
 * the tab picked last. addTab adds a plugin panel from contributions.
 * @param {object} app
 * @param {{defs: {id: string, label: string}[], nodes: object, idsFor: (current: string) => string[]}} options
 *   nodes maps a tab id to {node, onSelect(extra)} or null.
 * @returns {{element: HTMLElement, select: Function, setCount: Function, updateTabs: Function, addTab: Function, has: Function, destroy: Function}}
 */
function railTabs(app, { defs, nodes, idsFor }) {
  const panels = app.mods.panels || {};
  const keyTarget = typeof panels.tabKeyTarget === "function" ? (ids, current, key) => (key === "ArrowUp" || key === "ArrowDown" ? "" : panels.tabKeyTarget(ids, current, key)) : fallbackTabKey;
  const strip = h("div", { class: "underline-panels-tablist", role: "tablist", "aria-label": "Page details" });
  const wrap = h("div", { class: "underline-panels" }, strip);
  const entries = new Map();
  const extraIds = [];
  const ac = new AbortController();
  let current = "";
  let visible = [];

  let allowed = null;
  const allIds = (keep) => [...idsFor(keep).filter((id) => !allowed || allowed.includes(id) || id === keep), ...extraIds];
  const showVisible = () => {
    visible = allIds(current);
    for (const [id, entry] of entries) {
      entry.button.hidden = !visible.includes(id);
    }
  };
  const select = (id, { remember = false, extra = null, focus = false } = {}) => {
    const entry = entries.get(id);
    if (!entry || !allIds(id).includes(id)) {
      return false;
    }
    current = id;
    showVisible();
    for (const [key, other] of entries) {
      const on = key === id;
      other.button.setAttribute("aria-selected", on ? "true" : "false");
      other.button.tabIndex = on ? 0 : -1;
      other.panel.hidden = !on;
    }
    if (typeof entry.spec.onSelect === "function") {
      entry.spec.onSelect(extra);
    }
    if (remember && typeof app.setRailTab === "function" && !extraIds.includes(id)) {
      app.setRailTab(id);
    }
    if (typeof app.setRailTitle === "function") {
      app.setRailTitle(entry.def.label);
    }
    if (focus) {
      entry.button.focus();
    }
    return true;
  };
  const updateTabs = () => {
    showVisible();
    if (!visible.includes(current)) {
      select(pickRailTab(typeof app.railTab === "function" ? app.railTab() : null, visible));
    }
  };
  const addEntry = (def, spec) => {
    const count = counterLabel(null);
    const tabId = uid("rail-tab");
    const panelId = uid("rail-panel");
    const labelEl = h("span", { class: "underline-panels-label" }, h("span", { class: "underline-panels-text", text: def.label }), h("span", { class: "underline-panels-reserve", "aria-hidden": "true", text: def.label }));
    const tab = h(
      "button",
      { class: "underline-panels-tab", type: "button", role: "tab", id: tabId, "aria-controls": panelId, "aria-selected": "false", tabindex: "-1", dataset: { tab: def.id, label: def.label } },
      labelEl,
      count
    );
    const panel = h("div", { class: "underline-panels-panel rail-tabpanel", role: "tabpanel", id: panelId, "aria-labelledby": tabId, tabindex: "0", hidden: true }, spec.node);
    tab.addEventListener("click", () => select(def.id, { remember: true }));
    entries.set(def.id, { def, spec, button: tab, panel, count });
    strip.appendChild(tab);
    wrap.appendChild(panel);
  };

  for (const def of defs) {
    const spec = nodes[def.id];
    if (spec) {
      addEntry(def, spec);
    }
  }
  strip.addEventListener("keydown", (event) => {
    const focused = event.target && event.target.closest ? event.target.closest("[role=tab]") : null;
    if (!focused) {
      return;
    }
    const target = keyTarget(visible, focused.dataset.tab, event.key);
    if (!target) {
      return;
    }
    event.preventDefault();
    for (const [id, entry] of entries) {
      entry.button.tabIndex = id === target ? 0 : -1;
    }
    entries.get(target).button.focus();
  });
  // Tab enters the strip on the selected tab again.
  strip.addEventListener("focusout", (event) => {
    if (!strip.contains(event.relatedTarget)) {
      for (const [id, entry] of entries) {
        entry.button.tabIndex = id === current ? 0 : -1;
      }
    }
  });
  window.addEventListener("online", updateTabs, { signal: ac.signal });
  window.addEventListener("offline", updateTabs, { signal: ac.signal });
  showVisible();
  select(pickRailTab(typeof app.railTab === "function" ? app.railTab() : null, visible));

  const api = {
    element: wrap,
    select,
    updateTabs,
    has: (id) => entries.has(id),
    /** Lists the built-in tab ids this rail can show. */
    builtinIds: () => defs.map((def) => def.id).filter((id) => entries.has(id)),
    /** Keeps only the listed built-in tabs, as contributions decide. */
    restrict(ids) {
      allowed = Array.isArray(ids) ? ids.slice() : null;
      updateTabs();
    },
    /** Adds a tab after the others, such as a plugin panel. */
    addTab(def, spec) {
      if (entries.has(def.id)) {
        return false;
      }
      extraIds.push(def.id);
      addEntry(def, spec);
      showVisible();
      return true;
    },
    setCount(id, n) {
      const entry = entries.get(id);
      if (!entry || !COUNT_TABS.has(id)) {
        return;
      }
      setCount(entry.count, n > 0 ? (n > 99 ? "99+" : n) : null);
    },
    destroy() {
      ac.abort();
    },
  };
  wrap.railTabs = api;
  return api;
}

/**
 * Shows a Comment button for a text selection inside a rendered note body,
 * above the selection on desktop and below the selection end on touch
 * screens. The button maps the selection to body offsets with anchors.js and
 * hands onComment the new anchor.
 * @param {object} app
 * @param {{body: HTMLElement, content: string, onComment: (anchor: object) => void}} options
 * @returns {() => void} removes the button and its listeners.
 */
function selectionCommenter(app, { body, content, onComment }) {
  const anchors = app.mods.anchors || {};
  const place = app.mods.panels && app.mods.panels.commentButtonPosition;
  if (typeof anchors.sourceRangeFromDOM !== "function" || typeof anchors.makeAnchor !== "function" || typeof place !== "function") {
    return () => {};
  }
  const commentButton = h("button", { class: "comment-float-btn", type: "button" }, icon("comment", 16), h("span", { text: "Comment" }));
  const float = h("div", { class: "comment-float", role: "toolbar", "aria-label": "Selection", hidden: true }, commentButton);
  document.body.appendChild(float);
  const coarse = window.matchMedia("(pointer: coarse)");
  let pending = null;
  let timer = 0;
  let frame = 0;

  const hide = () => {
    pending = null;
    float.hidden = true;
  };
  const position = () => {
    frame = 0;
    const selection = document.getSelection();
    if (!pending || !selection || !selection.rangeCount) {
      hide();
      return;
    }
    const range = selection.getRangeAt(0);
    const rects = Array.from(range.getClientRects()).filter((rect) => rect.width > 0 || rect.height > 0);
    if (!rects.length) {
      hide();
      return;
    }
    float.hidden = false;
    float.classList.toggle("is-touch", coarse.matches);
    const pos = place(
      range.getBoundingClientRect(),
      rects[rects.length - 1],
      { width: float.offsetWidth, height: float.offsetHeight },
      { width: document.documentElement.clientWidth, height: window.innerHeight, top: 0, bottom: 0 },
      { touch: coarse.matches }
    );
    float.style.left = pos.left + "px";
    float.style.top = pos.top + "px";
    float.dataset.placement = pos.placement;
  };
  const check = () => {
    timer = 0;
    const selection = document.getSelection();
    if (!selection || selection.isCollapsed || !selection.rangeCount || !app.isOnline()) {
      hide();
      return;
    }
    const range = selection.getRangeAt(0);
    if (!body.contains(range.startContainer) || !body.contains(range.endContainer)) {
      hide();
      return;
    }
    let found = null;
    try {
      found = anchors.sourceRangeFromDOM(body, range, content);
    } catch (err) {
      console.error("[markspace] mapping the selection failed", err);
    }
    if (!found || !(found.end > found.start)) {
      hide();
      return;
    }
    pending = found;
    position();
  };
  const onSelection = () => {
    clearTimeout(timer);
    timer = setTimeout(check, coarse.matches ? 220 : 120);
  };
  const onMove = () => {
    if (!float.hidden && !frame) {
      frame = requestAnimationFrame(position);
    }
  };
  // Keeps the selection when a mouse presses the button.
  commentButton.addEventListener("mousedown", (event) => event.preventDefault());
  commentButton.addEventListener("click", () => {
    const range = pending;
    if (!range) {
      return;
    }
    const anchor = anchors.makeAnchor(content, range.start, range.end);
    hide();
    const selection = document.getSelection();
    if (selection) {
      selection.removeAllRanges();
    }
    if (anchor) {
      onComment(anchor);
    }
  });
  document.addEventListener("selectionchange", onSelection);
  window.addEventListener("scroll", onMove, { passive: true });
  window.addEventListener("resize", onMove);
  return () => {
    clearTimeout(timer);
    if (frame) {
      cancelAnimationFrame(frame);
    }
    document.removeEventListener("selectionchange", onSelection);
    window.removeEventListener("scroll", onMove);
    window.removeEventListener("resize", onMove);
    float.remove();
  };
}

/**
 * Builds the rail of a note, in reading view and in the editor: Outline,
 * Comments, History, Share, Files, AI and Info, every one on the panel
 * skeleton of panels.js. History, Share, Files and AI mount on first use and
 * live in the shell, so they survive re-renders of the same note. Plugin
 * panels from contributions are added after the contributions load.
 * @param {object} app
 * @param {{note: object, space: object, slug: string, access: string, rendered: object | null, byId: Map, comments: object | null, mode?: "view" | "edit", editor?: (() => object | null) | null}} options
 * @returns {Promise<{element: HTMLElement, select: Function, setCount: Function, updateTabs: Function, trackOutline: Function, destroy: Function}>}
 */
/**
 * Builds the frame of a built-in rail panel with panels.panelShell. When
 * panels.js is missing, a plain section stands in. The rail tab already
 * shows the title, so its h2 is visually hidden and names the section
 * through aria-labelledby, as panelShell does inside a tab panel.
 * @param {object} panels the panels.js module, possibly empty
 * @param {string} title
 * @param {string} className
 * @returns {{root: HTMLElement, body: HTMLElement}}
 */
export function railPanelShell(panels, title, className) {
  if (panels && typeof panels.panelShell === "function") {
    return panels.panelShell({ title, className });
  }
  const body = h("div", { class: "ms-panel-body" });
  const titleId = "rail-panel-title-" + className;
  return { root: h("section", { class: "ms-panel " + className, "aria-labelledby": titleId }, h("h2", { class: "ms-panel-title sr-only", id: titleId, text: title }), body), body };
}

async function buildRail(app, { note, space, slug, access, rendered, byId, comments, mode = "view", editor = null }) {
  const panels = app.mods.panels || {};
  const shellFor = (title, className) => railPanelShell(panels, title, className);
  const empty = (glyph, text) => (typeof panels.emptyState === "function" ? panels.emptyState({ icon: glyph, text }) : blankslate({ icon: glyph, title: text, size: "small" }));
  const closeDialog = () => {
    if (typeof app.closeRailDialog === "function") {
      app.closeRailDialog({ restore: false });
    }
  };

  /* Outline: the rendered headings in reading view, the live text in the editor. */
  const outline = shellFor("Outline", "rail-outline");
  const outlineLinks = [];
  const drawOutline = () => {
    outlineLinks.length = 0;
    let items = (rendered && rendered.toc) || [];
    if (mode === "edit") {
      const handle = editor ? editor() : null;
      const text = handle && handle.view ? handle.view.state.doc.toString() : String(note.content || "");
      items = typeof panels.outlineOf === "function" ? panels.outlineOf(text) : [];
    }
    if (!items.length) {
      outline.body.replaceChildren(empty("list-unordered", "Headings in the page appear here."));
      return;
    }
    const minLevel = Math.min(...items.map((item) => item.level || 1));
    const list = h("ol", { class: "outline" });
    items.forEach((item, index) => {
      const level = Math.min(3, (item.level || 1) - minLevel + 1);
      let link;
      if (mode === "edit") {
        link = h("button", {
          type: "button",
          text: item.text || "",
          on: {
            click: () => {
              closeDialog();
              const handle = editor ? editor() : null;
              if (handle && typeof handle.goTo === "function") {
                handle.goTo(item.pos, index);
              }
            },
          },
        });
      } else {
        link = h("a", { href: "#" + item.id, text: item.text || item.title || "", dataset: { heading: item.id }, on: { click: closeDialog } });
        outlineLinks.push(link);
      }
      list.appendChild(h("li", { class: "l" + level }, link));
    });
    outline.body.replaceChildren(list);
  };
  drawOutline();

  /* Info: page details, backlinks and child pages, each a list section. */
  const info = shellFor("Info", "rail-info");
  const listOf = (title, rows, emptyText = "") => {
    if (typeof panels.listSection === "function") {
      return panels.listSection({ title, count: title === "Details" ? null : rows.length, rows, empty: emptyText ? h("p", { class: "list-empty", text: emptyText }) : null }).root;
    }
    return railSection(title, rows.length ? h("ul", { class: "link-list" }, rows) : h("p", { class: "list-empty", text: emptyText }));
  };
  const rowOf = (lead, title, sub = "") =>
    typeof panels.listRow === "function" ? panels.listRow({ lead, title, sub }) : h("li", {}, title);
  const details = h("dl", { class: "info-list" });
  const detail = (labelText, value) => details.append(h("div", { class: "info-row" }, h("dt", { text: labelText }), h("dd", {}, value)));
  if (space) {
    // The app header already links to the space, so the name is plain text here.
    detail("Space", space.name);
  }
  detail("Created", timeText(note.createdAt));
  detail("Updated", h("span", {}, timeText(note.updatedAt), note.updatedBy && note.updatedBy === app.user.id ? " by you" : ""));
  detail("Words", String(wordCount(note.content)));
  detail("Access", accessLabel(access));
  if (note.path) {
    detail("File", stripMd(note.path) + ".md");
  }
  info.body.appendChild(h("section", { class: "list-section rail-details-list" }, h("div", { class: "list-head" }, h("h3", { class: "list-title", text: "Details" })), details));

  const backlinks = (await safe(() => app.store.backlinks(note.id), [])).filter((link) => link && link.note && !link.note.deleted);
  info.body.appendChild(
    listOf(
      "Backlinks",
      backlinks.map((link) => {
        const from = app.spaceBy(link.note.spaceId);
        const context = link.context ? (typeof panels.quoteText === "function" ? panels.quoteText(link.context) : link.context) : "";
        return rowOf(
          h("span", { class: "list-row-glyph" }, icon("link", 16)),
          h("a", { href: noteHref(link.note, from && from.slug), dataset: { noteId: link.note.id }, text: link.note.title || "Untitled" }),
          context
        );
      }),
      "No other page links here."
    )
  );

  const children = byPosition(Array.from(byId.values()).filter((n) => n.parentId === note.id && !n.deleted));
  if (children.length) {
    info.body.appendChild(
      listOf(
        "Child pages",
        children.map((child) =>
          rowOf(
            h("span", { class: "list-row-glyph" }, child.icon ? h("span", { text: child.icon }) : icon("note", 16)),
            h("a", { href: noteHref(child, slug), dataset: { noteId: child.id }, text: child.title || "Untitled" })
          )
        )
      )
    );
  }

  /** Highlights the outline entry of the heading nearest the top of the viewport. */
  const trackOutline = () => {
    if (!outlineLinks.length) {
      return null;
    }
    let frame = 0;
    const update = () => {
      frame = 0;
      let active = outlineLinks[0];
      for (const link of outlineLinks) {
        const heading = document.getElementById(link.dataset.heading);
        if (heading && heading.getBoundingClientRect().top <= 90) {
          active = link;
        }
      }
      for (const link of outlineLinks) {
        link.classList.toggle("active", link === active);
        if (link === active) {
          link.setAttribute("aria-current", "location");
        } else {
          link.removeAttribute("aria-current");
        }
      }
    };
    const onScroll = () => {
      if (!frame) {
        frame = requestAnimationFrame(update);
      }
    };
    window.addEventListener("scroll", onScroll, { passive: true });
    update();
    return () => window.removeEventListener("scroll", onScroll);
  };

  /* The panels from panels.js. In the editor the AI and Files panels write into the note. */
  const canEdit = accessAtLeast(access, "edit") && !note.deleted;
  const call = (name) => (...args) => {
    const handle = editor ? editor() : null;
    return handle && typeof handle[name] === "function" ? handle[name](...args) : null;
  };
  const hooks = mode === "edit" && editor && canEdit ? { insertText: call("insertText"), replaceSelection: call("replaceSelection"), getSelection: call("getSelection"), editor } : {};
  const lazy = (kind) => {
    const entry = typeof app.railPanel === "function" ? app.railPanel(kind, note, access, { mode, hooks }) : null;
    if (!entry) {
      return null;
    }
    return {
      node: entry.host,
      onSelect: (extra) => {
        // A request with a task or given context mounts the panel again. A plain request keeps what is there.
        const fresh = extra && (extra.task || extra.textOverride !== undefined || (extra.contextRefs && extra.contextRefs.length)) ? extra : null;
        app.activateRailPanel(entry, fresh);
      },
    };
  };
  const nodes = {
    outline: { node: outline.root, onSelect: mode === "edit" ? drawOutline : null },
    comments: comments ? { node: comments.host } : null,
    suggestions: lazy("suggestions"),
    history: lazy("history"),
    share: accessAtLeast(access, "manage") ? lazy("share") : null,
    files: lazy("files"),
    ai: lazy("ai"),
    info: { node: info.root },
  };
  const defs = Array.isArray(panels.RAIL_TABS) ? panels.RAIL_TABS : FALLBACK_RAIL_TABS;
  const idsFor = (keep) => {
    const ids =
      typeof panels.railTabIds === "function"
        ? panels.railTabIds({ access, comments: !!comments, ai: app.aiState !== "off", online: app.isOnline(), keep })
        : defs.map((def) => def.id);
    return ids.filter((id) => nodes[id]);
  };
  const tabbed = railTabs(app, { defs, nodes, idsFor });

  if (comments) {
    tabbed.setCount("comments", comments.count || 0);
  }
  return { element: tabbed.element, select: tabbed.select, setCount: tabbed.setCount, updateTabs: tabbed.updateTabs, trackOutline, destroy: tabbed.destroy };
}

/**
 * Mounts the editor from editor.js, or a plain fallback when it is missing.
 * The rail starts closed, so the editor has the width, and opens on the tab
 * a panel request names: a comment, the slash menu AI entry or the header
 * rail toggle.
 */
async function editorView(app, { note, space, slug, access, crumbs, el, byId, comments = null, findState = null }) {
  el.classList.add("editor-host");
  const mountEditor = app.mods.editor && app.mods.editor.mountEditor;
  const render =
    app.mods.markdown && typeof app.mods.markdown.render === "function"
      ? app.mods.markdown.render
      : (md) => ({ html: "", toc: [], links: [], tasks: 0, tasksDone: 0, source: md });
  const viewUrl = noteHref(note, slug);
  let handle = null;
  const done = () => app.navigate(viewUrl, { state: { noteId: note.id } });
  const rail = await buildRail(app, { note, space, slug, access, rendered: null, byId: byId || new Map(), comments, mode: "edit", editor: () => handle });

  /** Shows a rail tab beside the editor, or in the rail Dialog on narrow viewports. */
  const showPanel = (kind, extra = null) => {
    if (!rail.select(kind, { extra })) {
      return false;
    }
    app.showRail({ focus: false });
    return true;
  };

  /** Shows the comments panel, then focuses a thread or starts a draft. */
  const showComments = ({ focusId = null, draftAnchor = null } = {}) => {
    if (!comments || !comments.handle || !showPanel("comments")) {
      return;
    }
    if (draftAnchor) {
      comments.handle.draft(draftAnchor);
    } else if (focusId) {
      comments.handle.focus(focusId, { moveFocus: false });
    }
  };
  return {
    title: "Editing " + (note.title || "Untitled"),
    crumbs,
    space,
    noteId: note.id,
    kind: "note",
    live: false,
    ownsFocus: true,
    editing: true,
    rail: rail.element,
    railClosed: true,
    showComments,
    showPanel,
    onCommentCount: (n) => rail.setCount("comments", n),
    onRailCount: (kind, n) => rail.setCount(kind, n),
    onAIState: () => rail.updateTabs(),
    commentBody: () => (handle && handle.view ? handle.view.state.doc.toString() : String(note.content || "")),
    onCommentActivate(id) {
      if (handle && typeof handle.setActiveComment === "function") {
        handle.setActiveComment(id);
      }
    },
    onCommentReveal(id) {
      if (handle && typeof handle.revealComment === "function") {
        handle.revealComment(id);
      }
    },
    mounted() {
      if (typeof mountEditor !== "function") {
        console.error("[markspace] /static/js/editor.js does not export mountEditor() yet. See docs/offline.md.");
        handle = fallbackEditor(app, note, el, done);
        return;
      }
      try {
        handle = mountEditor(el, {
          app,
          note,
          access,
          store: app.store,
          sync: app.sync,
          render,
          navigate: (url) => app.navigate(url),
          isOnline: () => app.isOnline(),
          user: app.user,
          onDone: done,
          setChrome: (options) => app.setChrome(options || { immersive: false }),
          openComments: comments ? (options) => showComments(options || {}) : undefined,
          commentSource: comments ? comments.source : undefined,
          openPanel: (kind, extra) => showPanel(kind, extra || null),
        });
        if (findState && handle && typeof handle.openFind === "function") {
          handle.openFind({ replace: true, state: findState });
        }
      } catch (err) {
        console.error("[markspace] the editor failed to mount", err);
        el.replaceChildren();
        handle = fallbackEditor(app, note, el, done);
      }
    },
    async flush() {
      if (handle && typeof handle.flush === "function") {
        await handle.flush();
      }
    },
    destroy() {
      rail.destroy();
      if (handle && typeof handle.unmount === "function") {
        try {
          handle.unmount();
        } catch (err) {
          console.error("[markspace] editor cleanup failed", err);
        }
      }
      app.setChrome({ immersive: false });
    },
  };
}

/** A plain text editor used only when editor.js cannot load. */
function fallbackEditor(app, note, el, done) {
  let timer = null;
  let dirty = false;
  const titleField = formControl({ label: "Title", name: "title", value: note.title || "" });
  const textField = formControl({ label: "Markdown", name: "content", type: "textarea", value: note.content || "", rows: 20, monospace: true });
  const titleInput = titleField.querySelector("input");
  const text = textField.querySelector("textarea");
  const saveState = h("span", { class: "fallback-save-state", text: "Saved on this device" });
  const save = async () => {
    clearTimeout(timer);
    if (!dirty) {
      return;
    }
    dirty = false;
    saveState.textContent = "Saving";
    try {
      await app.sync.updateNote(note.id, { title: titleInput.value.trim() || "Untitled", content: text.value });
      saveState.textContent = "Saved on this device";
    } catch (err) {
      dirty = true;
      saveState.replaceChildren(inlineMessage({ variant: "critical", size: "small", text: "Not saved" }));
    }
  };
  const onInput = () => {
    dirty = true;
    saveState.textContent = "Unsaved changes";
    clearTimeout(timer);
    timer = setTimeout(save, 800);
  };
  titleInput.addEventListener("input", onInput);
  text.addEventListener("input", onInput);
  el.appendChild(
    h(
      "div",
      { class: "page fallback-editor" },
      pageHeader({ title: "Editing " + (note.title || "Untitled"), actions: [{ label: "Done", primary: true, onClick: async () => { await save(); done(); } }], description: saveState }),
      titleField,
      textField
    )
  );
  text.focus();
  return {
    flush: save,
    isDirty: () => dirty,
    unmount: () => {
      clearTimeout(timer);
      el.replaceChildren();
    },
  };
}

/* ---------- Tag ---------- */

async function tagView(app, params, el) {
  const space = app.spaceBy(params.space);
  if (!space) {
    return missingSpace(app, el, params.space);
  }
  const all = (await safe(() => app.store.listNotes(space.id), [])).filter((note) => !note.deleted);
  const wanted = params.tag.toLowerCase();
  const notes = byUpdated(all.filter((note) => (note.tags || []).some((tag) => tag.toLowerCase() === wanted)));
  const page = h("div", { class: "page" });
  page.appendChild(
    pageHeader({
      title: "#" + params.tag,
      parent: null,
      description: (notes.length === 1 ? "1 page" : notes.length + " pages") + " in " + space.name,
      border: true,
    })
  );
  page.appendChild(
    notes.length
      ? noteList(app, notes, { label: "Pages tagged " + params.tag })
      : blankslate({ icon: "tag", title: "No pages carry this tag", description: "Add #" + params.tag + " to a page, or pick another tag.", secondaryAction: { label: "Back to " + space.name, href: spaceUrl(space.slug) } })
  );
  const others = groupTags(all).filter(({ tag }) => tag.toLowerCase() !== wanted);
  if (others.length) {
    page.appendChild(section("Other tags", tagTokens(space.slug, others, { counts: true }), { count: others.length }));
  }
  el.appendChild(page);
  return { title: "#" + params.tag, crumbs: [{ label: space.name, href: spaceUrl(space.slug) }, { label: "#" + params.tag, href: null }], space };
}

/* ---------- Starred, recent, shared ---------- */

async function listView(app, kind, el) {
  const config = {
    starred: {
      title: "Starred",
      load: () => app.store.starredNotes(),
      glyph: "star",
      emptyTitle: "Nothing starred yet",
      emptyText: "Star a page to keep it one click away.",
    },
    recent: {
      title: "Recent",
      load: () => app.store.recentNotes(50),
      glyph: "clock",
      emptyTitle: "No recent pages",
      emptyText: "Pages you open or edit show here.",
    },
    shared: {
      title: "Shared with me",
      load: () => app.store.sharedNotes(app.user.id),
      glyph: "people",
      emptyTitle: "Nothing shared with you",
      emptyText: "When someone shares a page with you, it shows here.",
    },
  }[kind];
  const notes = (await safe(config.load, [])).filter((note) => note && !note.deleted);
  const sorted = kind === "starred" ? byUpdated(notes) : notes;
  const page = h("div", { class: "page" });
  if (!sorted.length && neverSynced(app) && app.isOnline()) {
    page.appendChild(loadingState("Getting your pages"));
  } else {
    page.appendChild(pageHeader({ title: config.title, border: true }));
    page.appendChild(sorted.length ? noteList(app, sorted, { showSpace: true, label: config.title }) : blankslate({ icon: config.glyph, title: config.emptyTitle, description: config.emptyText }));
  }
  el.appendChild(page);
  return { title: config.title };
}

/* ---------- Trash ---------- */

async function trashView(app, params, el) {
  const space = app.spaceBy(params.space);
  if (!space) {
    return missingSpace(app, el, params.space);
  }
  const canEdit = accessAtLeast(spaceAccess(space, app.user), "edit");
  const canManage = accessAtLeast(spaceAccess(space, app.user), "manage");
  const time = (rec) => Date.parse(rec.deletedAt || rec.updatedAt || 0) || 0;
  const trashed = await safe(() => app.store.listItems(space.id, { includeDeleted: true }), { folders: [], notes: [], files: [] });
  const trashedFolderIds = new Set(trashed.folders.filter((f) => f.deleted).map((f) => f.id));
  const allNotes = await safe(() => app.store.listNotes(space.id, { includeDeleted: true }), []);
  const deletedNotes = allNotes.filter((note) => note.deleted);
  const deletedNoteIds = new Set(deletedNotes.map((note) => note.id));
  // An item inside a trashed folder or page goes with its parent.
  const items = [
    ...trashed.folders.filter((f) => f.deleted && !trashedFolderIds.has(f.parentId)).map((rec) => ({ kind: "folder", rec })),
    ...trashed.files.filter((f) => f.deleted && !trashedFolderIds.has(f.parentId)).map((rec) => ({ kind: "file", rec })),
    ...deletedNotes.filter((note) => !(note.parentId && (trashedFolderIds.has(note.parentId) || deletedNoteIds.has(note.parentId)))).map((rec) => ({ kind: "note", rec })),
  ].sort((a, b) => time(b.rec) - time(a.rec));

  const nameOf = (item) => (item.kind === "note" ? item.rec.title || "Untitled" : item.rec.name || "Untitled");
  const deleteItem = (item) => (item.kind === "note" ? app.sync.deleteNote(item.rec.id) : app.sync.deleteItem(item.rec.id));
  const restoreItem = (item) => (item.kind === "note" ? app.sync.restoreNote(item.rec.id) : app.sync.restoreItem(item.rec.id));

  const page = h("div", { class: "page" });
  const emptyTrash = async () => {
    for (const item of items) {
      await deleteItem(item);
    }
    announce(items.length === 1 ? "Deleted 1 item forever" : "Deleted " + items.length + " items forever");
    app.focusAfterRefresh = (container) => container.querySelector("h1");
  };
  page.appendChild(
    pageHeader({
      title: "Trash",
      parent: null,
      description: "Deleted pages, folders and files from " + space.name + ". Restore puts an item back where it was.",
      border: true,
      actions:
        canManage && items.length
          ? [
              {
                label: "Empty trash",
                variant: "danger",
                onClick: (event) =>
                  confirmDialog({
                    title: "Empty trash?",
                    body: (items.length === 1 ? "1 item is" : items.length + " items are") + " deleted forever. You cannot restore them.",
                    confirmLabel: "Empty trash",
                    returnFocus: event.currentTarget,
                    onConfirm: emptyTrash,
                  }),
              },
            ]
          : [],
    })
  );

  if (!items.length) {
    page.appendChild(blankslate({ icon: "trash", title: "The trash is empty", description: "Items you move to the trash stay here until you delete them forever." }));
    el.appendChild(page);
    return { title: "Trash", space };
  }

  /** Moves focus to the next row after a row leaves the list (docs/primer.md 6.2). */
  const focusNext = (item) => {
    const index = items.indexOf(item);
    const next = items[index + 1] || items[index - 1];
    app.focusAfterRefresh = (container) =>
      (next && container.querySelector(`[data-item-id="${CSS.escape(next.rec.id)}"] .btn`)) || container.querySelector("#trash-heading") || container.querySelector("h1");
  };
  const list = h("ul", { class: "box trash-list", "aria-labelledby": "trash-heading" });
  for (const item of items) {
    const name = nameOf(item);
    const kindText = item.kind === "folder" ? "Folder" : item.kind === "note" ? "Page" : formatSize(item.rec.size);
    const trail = [];
    if (canEdit) {
      trail.push(
        button({
          label: "Restore",
          size: "small",
          attrs: { "aria-label": "Restore " + name },
          onClick: async (event) => {
            const btn = event.currentTarget;
            btn.setAttribute("aria-disabled", "true");
            try {
              focusNext(item);
              await restoreItem(item);
              announce("Restored “" + name + "”");
            } catch (err) {
              btn.removeAttribute("aria-disabled");
              app.focusAfterRefresh = null;
              row.querySelector(".trash-row-error").replaceChildren(inlineMessage({ variant: "critical", size: "small", text: (err && err.message) || "“" + name + "” could not be restored" }));
            }
          },
        }),
        iconButton({
          icon: "kebab-horizontal",
          label: "More actions for " + name,
          size: "small",
          attrs: { "aria-haspopup": "true" },
          onClick: (event) =>
            openMenu(
              event.currentTarget,
              [
                {
                  label: "Delete forever",
                  icon: "trash",
                  danger: true,
                  confirm: { title: "Delete “" + name + "” forever?", body: "You cannot restore it.", confirmLabel: "Delete forever" },
                  onClick: async () => {
                    focusNext(item);
                    await deleteItem(item);
                    announce("Deleted “" + name + "” forever");
                  },
                },
              ],
              { placement: "bottom-end", label: "More actions for " + name }
            ),
        })
      );
    }
    const row = h(
      "li",
      { class: "box-row trash-row", dataset: { itemId: item.rec.id, kind: item.kind } },
      h("span", { class: "list-row-lead", "aria-hidden": "true" }, item.kind === "note" && item.rec.icon ? h("span", { class: "view-emoji", text: item.rec.icon }) : icon(item.kind === "folder" ? "file-directory-fill" : item.kind === "note" ? "note" : itemFileIcon(item.rec), 16)),
      h(
        "div",
        { class: "list-row-main" },
        h("span", { class: "list-row-title", text: name }),
        h("span", { class: "list-row-sub" }, kindText + " · ", timeText(item.rec.deletedAt || item.rec.updatedAt, "Deleted ")),
        h("span", { class: "trash-row-error" })
      ),
      trail.length ? h("div", { class: "list-row-trail" }, trail) : null
    );
    list.appendChild(row);
  }
  page.appendChild(
    h(
      "section",
      { class: "view-section", "aria-labelledby": "trash-heading" },
      h("div", { class: "section-heading" }, h("h2", { class: "section-heading-title", id: "trash-heading", tabindex: "-1" }, h("span", { text: "Deleted items" }), counterLabel(items.length))),
      list
    )
  );
  el.appendChild(page);
  return { title: "Trash", space };
}

/** Formats a byte size for a trash row. */
function formatSize(n) {
  const size = Number(n) || 0;
  if (size < 1024) {
    return size + " B";
  }
  const units = ["KB", "MB", "GB", "TB"];
  let value = size / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return value.toFixed(value < 10 ? 1 : 0) + " " + units[unit];
}

/** Picks the Octicon of a file record in the trash. */
function itemFileIcon(rec) {
  const mime = String((rec && rec.mime) || "");
  if (mime.startsWith("image/")) {
    return "image";
  }
  return "file";
}

/* ---------- Search ---------- */

async function searchView(app, params, el) {
  const page = h("div", { class: "page search-page" });
  const queryField = formControl({ label: "Search pages", hideLabel: true, name: "q", type: "search", value: params.q || "", leadingVisual: "search", size: "large", autocomplete: "off", enterkeyhint: "search" });
  const input = queryField.querySelector("input");
  const spaceField = formControl({
    label: "Space",
    name: "space",
    type: "select",
    size: "small",
    value: params.space || "",
    options: [{ value: "", label: "All spaces" }, ...app.spaces.map((space) => ({ value: space.slug, label: space.name }))],
  });
  const spaceSelect = spaceField.querySelector("select");
  const modeField = formControl({
    label: "Mode",
    name: "mode",
    type: "select",
    size: "small",
    value: params.mode || "hybrid",
    options: [
      { value: "hybrid", label: "Hybrid" },
      { value: "semantic", label: "Semantic" },
      { value: "text", label: "Full text" },
    ],
  });
  const modeSelect = modeField.querySelector("select");
  const serverLabelId = uid("server-label");
  let serverOn = !!params.mode;
  const serverSwitch = toggleSwitch({
    checked: serverOn,
    size: "small",
    labelledBy: serverLabelId,
    onChange: (on) => {
      serverOn = on;
      run();
    },
  });
  const serverControl = h("div", { class: "search-server" }, h("span", { class: "search-server-label", id: serverLabelId, text: "Search server" }), serverSwitch);
  const offlineNote = inlineMessage({ variant: "unavailable", size: "small", text: "Server search needs a connection" });
  const results = h("div", { class: "search-results" });

  page.append(
    pageHeader({ title: "Search", border: true }),
    h("form", { class: "search-form-shell", role: "search", novalidate: true, on: { submit: (event) => { event.preventDefault(); clearTimeout(timer); run(); } } }, queryField),
    h("div", { class: "search-options" }, spaceField, serverControl, modeField, offlineNote),
    results
  );
  el.appendChild(page);

  let seq = 0;
  const run = async () => {
    const mine = ++seq;
    const q = input.value.trim();
    const spaceSlug = spaceSelect.value;
    const online = app.isOnline();
    const server = serverOn && online;
    serverControl.hidden = !online;
    offlineNote.hidden = online;
    modeField.hidden = !server;
    history.replaceState(Object.assign({}, history.state || {}), "", searchUrl({ q, space: spaceSlug, mode: serverOn ? modeSelect.value : "" }));
    if (!q) {
      results.replaceChildren(blankslate({ icon: "search", title: "Search your pages", description: "Results come from this device as you type. Turn on server search for semantic matches." }));
      return;
    }
    const space = app.spaceBy(spaceSlug);
    let hits = [];
    if (server) {
      results.setAttribute("aria-busy", "true");
      try {
        const query = new URLSearchParams({ q, mode: modeSelect.value, limit: "30" });
        if (spaceSlug) {
          query.set("space", spaceSlug);
        }
        const res = await app.api("GET", "/api/search?" + query.toString());
        hits = Array.isArray(res) ? res : (res && (res.hits || res.results)) || [];
      } catch (err) {
        if (mine === seq) {
          results.replaceChildren(
            blankslate({ icon: "alert", title: "Server search failed", description: (err && err.message) || "Try again, or search this device instead.", primaryAction: { label: "Try again", onClick: () => run() } })
          );
          announce("Server search failed");
        }
        return;
      } finally {
        results.removeAttribute("aria-busy");
      }
    } else {
      hits = await safe(() => app.store.searchNotes(q, { spaceId: space ? space.id : undefined, limit: 50 }), []);
    }
    if (mine !== seq) {
      return;
    }
    hits = hits.filter((hit) => hit && hit.note && !hit.note.deleted);
    if (!hits.length) {
      results.replaceChildren(blankslate({ icon: "search", title: "No pages match “" + q + "”", description: server ? "Try other words or another mode." : "Try other words, or turn on server search." }));
      announce("No results");
      return;
    }
    const countText = (hits.length === 1 ? "1 result" : hits.length + " results") + (server ? " from the server" : " on this device");
    announce(countText);
    const snippets = new Map(hits.map((hit) => [hit.note.id, hit.snippet || ""]));
    results.replaceChildren(
      section(
        countText,
        noteList(app, hits.map((hit) => hit.note), {
          showSpace: true,
          label: "Results",
          snippet: (note) => {
            const text = snippets.get(note.id);
            if (!text) {
              return null;
            }
            return h("span", { class: "action-list-description search-snippet" }, parseMarked(text).map((seg) => (seg.mark ? h("mark", { text: seg.text }) : seg.text)));
          },
        })
      )
    );
  };

  let timer = null;
  const onInput = () => {
    clearTimeout(timer);
    timer = setTimeout(run, serverOn ? 300 : 80);
  };
  input.addEventListener("input", onInput);
  spaceSelect.addEventListener("change", run);
  modeSelect.addEventListener("change", run);
  window.addEventListener("online", run);
  window.addEventListener("offline", run);
  await run();

  return {
    title: params.q ? params.q + " · Search" : "Search",
    focus: input,
    refresh() {
      if (!serverOn) {
        return run();
      }
      return undefined;
    },
    destroy() {
      clearTimeout(timer);
      window.removeEventListener("online", run);
      window.removeEventListener("offline", run);
    },
  };
}

export { filesUrl };
