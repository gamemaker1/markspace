// The page states the audit visits. Each state navigates, sets up an
// interactive state when needed, and may undo what it changed afterwards.
//
// kind: "app" runs signed in with the service worker, so shell routes render
// in the offline client and other paths render on the server. "fallback"
// runs signed in without the service worker, which shows the server version
// of shell routes. "anon" runs signed out.

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const enc = (p) => String(p).split("/").map(encodeURIComponent).join("/");
const noMd = (p) => String(p).replace(/\.md$/, "");

export const PROFILES = [
  { name: "1440x900", viewport: { width: 1440, height: 900 } },
  { name: "1280x720", viewport: { width: 1280, height: 720 } },
  { name: "1024x768", viewport: { width: 1024, height: 768 } },
  { name: "390x844", viewport: { width: 390, height: 844 }, phone: true, keyboard: 336 },
  { name: "844x390", viewport: { width: 844, height: 390 }, phone: true, keyboard: 200 },
  { name: "320x256", viewport: { width: 320, height: 256 }, phone: true },
  { name: "720x450@2x", viewport: { width: 720, height: 450 }, zoom: true },
];

/* ---------- Helpers ---------- */

async function settle(page, ms = 450) {
  await page.evaluate(() => (document.fonts ? document.fonts.ready.then(() => true) : true)).catch(() => {});
  await sleep(ms);
}

async function waitShell(page) {
  await page.waitForFunction(() => window.markspaceApp && window.markspaceApp.spaces && window.markspaceApp.spaces.length > 0, null, { timeout: 20000 });
}

/** Full navigation, then waits for a ready selector. */
async function go(h, path, { ready = "main h1, #main h1, .blankslate", shell = false, wait = 450 } = {}) {
  const { page, base } = h;
  await page.goto(base + path, { waitUntil: "domcontentloaded", timeout: 30000 });
  if (shell) await waitShell(page);
  if (ready) await page.waitForSelector(ready, { state: "visible", timeout: 20000 });
  await settle(page, wait);
}

const SHELL_READY = "#main h1, #main .blankslate, #main .blankslate-heading";

async function clickFirst(page, selector, opts = {}) {
  const loc = page.locator(selector, opts).filter({ visible: true }).first();
  await loc.waitFor({ state: "visible", timeout: 10000 });
  await loc.click();
  return loc;
}

async function openRail(h) {
  const { page } = h;
  if (h.narrow) {
    // Narrow PageHeaders keep only Edit and the kebab. Page details opens from the kebab menu.
    await clickFirst(page, ".page-header-actions-narrow [aria-label='More actions']");
    await page.locator("[role^=menuitem]", { hasText: /^\s*Page details\s*$/ }).filter({ visible: true }).first().click();
    await page.waitForSelector("dialog.rail-dialog[open] .underline-panels-tab", { state: "visible", timeout: 10000 });
  } else {
    // The rail is open by default on wide screens. Toggle it only when no tab shows.
    await page.locator(".underline-panels-tab").filter({ visible: true }).first().waitFor({ timeout: 2500 }).catch(() => {});
    const visibleTab = await page.locator(".underline-panels-tab").filter({ visible: true }).count();
    if (!visibleTab) await clickFirst(page, ".page-header-actions-regular [data-rail-toggle]");
    await page.locator(".underline-panels-tab").filter({ visible: true }).first().waitFor({ timeout: 10000 });
  }
  await settle(page, 300);
}

async function selectTab(h, label) {
  const { page } = h;
  // The tab text holds a hidden weight-600 copy of the label, so match on data-label.
  const tab = page.locator(`.underline-panels-tab[data-label="${label}"]`).filter({ visible: true }).first();
  await tab.waitFor({ timeout: 10000 });
  await tab.click();
  await page.waitForFunction((l) => {
    const t = [...document.querySelectorAll(".underline-panels-tab[aria-selected='true']")].find((x) => x.getClientRects().length);
    return t && (t.dataset.label || t.textContent).trim().startsWith(l);
  }, label, { timeout: 5000 });
  await settle(page, 900);
}

/** Runs code with the CodeMirror view of the open editor. */
async function cm(page, fn, arg) {
  return page.evaluate(
    async ([src, arg]) => {
      const el = document.querySelector(".cm-content");
      let view = el && (el.cmTile || el.cmView) ? (el.cmTile || el.cmView).view : null;
      if (!view) {
        const mod = await import("/static/vendor/codemirror.js");
        view = mod.EditorView.findFromDOM(document.querySelector(".cm-editor"));
      }
      return new Function("view", "arg", src)(view, arg);
    },
    [fn, arg]
  );
}

async function openEditor(h, mode = "live") {
  const { page } = h;
  const note = h.sandbox.editor;
  await go(h, `/n/${note.id}?edit=1`, { shell: true, ready: ".cm-content, .ms-editor" });
  const seg = page.locator(`.ms-seg button[data-value='${mode}']`).first();
  if (await seg.count()) {
    const current = await seg.getAttribute("aria-current");
    if (current !== "true") {
      await seg.click();
      await settle(page, 500);
    }
  }
  if (mode !== "reading") await page.waitForSelector(".cm-content", { timeout: 15000 });
  await settle(page, 400);
}

async function caret(h, needle, { length = 0, offset = 0 } = {}) {
  const ok = await cm(h.page, "const at = arg.needle === null ? view.state.doc.length : view.state.doc.toString().indexOf(arg.needle); if (at < 0) return false; view.focus(); view.dispatch({ selection: { anchor: at + arg.offset, head: at + arg.offset + arg.length }, scrollIntoView: true }); return true;", { needle, length, offset });
  if (!ok) throw new Error("text not found in the editor: " + needle);
  await sleep(250);
}

async function openAskBar(h) {
  const { page } = h;
  if (h.profile.phone) {
    const btn = page.locator(".ms-kbar [data-kbar='ai']").filter({ visible: true }).first();
    await btn.waitFor({ timeout: 8000 });
    await btn.tap();
  } else {
    await page.keyboard.press("Control+j");
  }
  await page.locator(".ask-bar").first().waitFor({ state: "visible", timeout: 8000 });
  await settle(page, 300);
}

async function undoEditorTyping(h, count) {
  const { page } = h;
  await page.keyboard.press("Escape").catch(() => {});
  await cm(page, "const t = view.state.doc.toString(); const at = t.lastIndexOf(arg.marker); if (at >= 0) view.dispatch({ changes: { from: at, to: t.length } });", { marker: "\naudit" }).catch(() => {});
  // Let the autosave finish, so the next state does not start from a stale base and get a 409.
  await sleep(count ? 3000 : 0);
}

async function treeContextMenu(h, label) {
  const { page } = h;
  await go(h, `/s/${h.S.space.slug}`, { shell: true, ready: SHELL_READY });
  let scope = page;
  if (h.narrow || h.profile.name === "844x390") {
    const opener = page.locator(".app-header [aria-label='Open navigation'], .app-header-sidebar-toggle").filter({ visible: true }).first();
    const tree = page.locator(".file-tree [role=treeitem]").filter({ visible: true });
    if (!(await tree.count()) && (await opener.count())) {
      await opener.click();
      await page.waitForSelector("dialog.nav-drawer[open] [role=treeitem]", { timeout: 8000 });
      await settle(page, 300);
    }
  }
  const row = scope.locator(".file-tree [role=treeitem]", { hasText: label }).filter({ visible: true }).first();
  await row.waitFor({ timeout: 10000 });
  await row.focus();
  await page.keyboard.press("Shift+F10");
  await page.waitForSelector("[role=menu]", { state: "visible", timeout: 6000 });
  await settle(page, 300);
}

/** Sets or removes localStorage keys on the server origin, then navigates. */
async function withStorage(h, path, entries, opts) {
  const { page, base } = h;
  if (!page.url().startsWith(base)) await page.goto(base + "/offline", { waitUntil: "domcontentloaded", timeout: 30000 });
  await page.evaluate((e) => {
    for (const [k, v] of Object.entries(e)) {
      if (v === null) localStorage.removeItem(k);
      else localStorage.setItem(k, v);
    }
  }, entries);
  await go(h, path, opts);
}

const LAYOUT_KEYS = ["markspace.sidebar", "markspace.rail", "markspace.railRegular"];
const clearLayout = (h) => h.page.evaluate((keys) => keys.forEach((k) => localStorage.removeItem(k)), LAYOUT_KEYS).catch(() => {});

async function openMenuItem(h, text) {
  await h.page.locator("[role^=menuitem]", { hasText: text }).filter({ visible: true }).first().click();
}

async function clickUndo(page) {
  const undo = page.locator("[data-ui-banner] button, .undo-row button", { hasText: /^\s*Undo\s*$/ }).filter({ visible: true }).first();
  if (await undo.count()) {
    await undo.click();
    await sleep(900);
  }
}

/* ---------- States ---------- */

export function buildStates(S) {
  const slug = S.space.slug;
  const survey = S.notes.survey;
  const list = [];
  const add = (s) => list.push({ kind: "app", group: "page", focus: true, ...s });

  /* Shell routes. */
  const shellPage = (id, path, extra = {}) => add({ id, group: "page", run: (h) => go(h, typeof path === "function" ? path(h) : path, { shell: true, ready: extra.ready || SHELL_READY }), ...extra });
  shellPage("home", "/");
  shellPage("space", `/s/${slug}`);
  shellPage("folder", `/s/${slug}/d/${enc(S.folders.harbour.path)}`, { ready: "#main h1" });
  shellPage("folder-deep", `/s/${slug}/d/${enc(S.folders.season.path)}`, { ready: "#main h1" });
  shellPage("files", `/s/${slug}/files`);
  shellPage("file-pdf", `/s/${slug}/f/${enc(S.files.pdf.path)}`, { ready: ".file-view-header h1", after: (h) => h.page.waitForFunction(() => Number((document.querySelector(".fv-pdf") || {}).dataset?.pageCount) > 1, null, { timeout: 15000 }).catch(() => {}) });
  shellPage("file-image", `/s/${slug}/f/${enc(S.files.png.path)}`, { ready: ".file-view-header h1" });
  shellPage("file-csv", `/s/${slug}/f/${enc(S.files.csv.path)}`, { ready: ".file-view-header h1" });
  shellPage("file-text", `/s/${slug}/f/${enc(S.files.json.path)}`, { ready: ".file-view-header h1" });
  if (S.files.video) shellPage("file-video", `/s/${slug}/f/${enc(S.files.video.path)}`, { ready: ".file-view-header h1" });
  shellPage("note", `/n/${survey.id}`, { ready: "#main h1" });
  shellPage("note-long", `/n/${S.notes.manual.id}`, { ready: "#main h1" });
  if (S.notes.empty) shellPage("note-empty", `/n/${S.notes.empty.id}`, { ready: "#main h1" });
  for (const mode of ["live", "source", "reading"]) add({ id: `editor-${mode}`, editor: true, run: (h) => openEditor(h, mode) });
  shellPage("search", "/search?q=survey");
  shellPage("starred", "/starred");
  shellPage("recent", "/recent");
  shellPage("shared", "/shared");
  shellPage("tag", `/s/${slug}/t/survey`);
  shellPage("trash", `/s/${slug}/trash`);

  /* Server pages. */
  const serverPage = (id, path, extra = {}) => add({ id, run: (h) => go(h, path, { ready: extra.ready || "main h1, h1, .blankslate" }), ...extra });
  serverPage("spaces", "/spaces");
  serverPage("space-settings", `/s/${slug}/settings`);
  serverPage("settings", "/settings");
  serverPage("settings-profile", "/settings/profile");
  serverPage("settings-tokens", "/settings/tokens");
  serverPage("settings-ai", "/settings/ai");
  serverPage("settings-usage", "/settings/usage");
  serverPage("settings-sessions", "/settings/sessions");
  serverPage("settings-offline", "/settings/offline");
  serverPage("admin", "/admin");
  serverPage("admin-users", "/admin/users");
  serverPage("admin-ai", "/admin/ai");
  serverPage("admin-usage", "/admin/usage");
  serverPage("admin-settings", "/admin/settings");
  serverPage("admin-plugins", "/admin/plugins");
  serverPage("jobs", "/jobs?view=all");
  serverPage("plugin-page", `/plugins/page?panel=${encodeURIComponent((S.plugin && S.plugin.panel) || "word-count.counts")}&itemId=${survey.id}&spaceId=${S.space.id}`);
  serverPage("error-404", "/audit-no-such-page", { expectConsole: [/audit-no-such-page|status of 404/] });
  serverPage("offline-page", "/offline");

  /* Signed out. */
  const anon = (id, path, extra = {}) => list.push({ kind: "anon", group: "page", focus: true, id, run: (h) => go(h, path, { ready: extra.ready || "main h1, h1, form, .blankslate" }), ...extra });
  anon("sign-in", "/login");
  anon("sign-up", "/signup");
  if (S.links.survey) anon("public-note", `/p/${S.links.survey.token}`);
  if (S.links.manual) anon("public-long-note", `/p/${S.links.manual.token}`);
  if (S.links.password) anon("public-password", `/p/${S.links.password.token}`);
  if (S.links.folder && S.links.folder.token) {
    anon("public-folder", `/p/${S.links.folder.token}`);
    anon("public-folder-item", `/p/${S.links.folder.token}/i/${S.notes.methods.id}`);
  }
  if (S.links.file && S.links.file.token) anon("public-file", `/p/${S.links.file.token}`);
  list.push({
    kind: "anon", group: "interactive", focus: false, id: "sign-in-invalid",
    run: async (h) => {
      await go(h, "/login", { ready: "form" });
      await h.page.fill("input[name=email]", "ada@example.com");
      await h.page.fill("input[name=password]", "wrong password");
      await Promise.all([h.page.waitForLoadState("domcontentloaded"), h.page.locator("form button[type=submit]").first().click()]);
      await h.page.waitForSelector(".field-error, [data-flash=error], .banner-critical", { timeout: 10000 });
      await settle(h.page);
    },
    expectConsole: [/status of (400|401|403|422)/],
  });
  list.push({
    kind: "anon", group: "interactive", focus: false, id: "sign-up-invalid",
    run: async (h) => {
      await go(h, "/signup", { ready: "form" });
      await h.page.fill("input[name=email]", "not-an-email");
      await Promise.all([h.page.waitForLoadState("domcontentloaded"), h.page.locator("form button[type=submit]").first().click()]);
      await h.page.waitForSelector(".field-error, [data-flash=error], .banner-critical", { timeout: 10000 });
      await settle(h.page);
    },
    expectConsole: [/status of (400|401|403|422)/],
  });

  /* Server versions of shell routes, without the service worker. */
  const fallback = (id, path, ready = "main h1, h1, .blankslate") => list.push({ kind: "fallback", group: "page", focus: false, id, run: (h) => go(h, path, { ready }) });
  fallback("server-space", `/s/${slug}`);
  fallback("server-note", `/s/${slug}/n/${enc(noMd(survey.path))}`);
  fallback("server-editor", `/s/${slug}/n/${enc(noMd(survey.path))}?edit=1`, "main, form, textarea, .cm-content");
  fallback("server-search", "/search?q=survey");
  fallback("server-trash", `/s/${slug}/trash`);
  fallback("server-starred", "/starred");
  fallback("server-tag", `/s/${slug}/t/survey`);

  /* Interactive states. */
  const act = (id, run, extra = {}) => add({ id, group: "interactive", focus: false, run, ...extra });
  const RAIL_TABS = ["Outline", "Comments", "Suggestions", "History", "Share", "Files", "AI", "Info", "Word count"];
  for (const tab of RAIL_TABS) {
    act(`rail-${tab.toLowerCase().replace(/\s+/g, "-")}`, async (h) => {
      await go(h, `/n/${survey.id}`, { shell: true, ready: "#main h1" });
      await openRail(h);
      await selectTab(h, tab);
    }, { focus: tab === "Share" || tab === "Comments" });
  }
  act("rail-share-add-link", async (h) => {
    await go(h, `/n/${survey.id}`, { shell: true, ready: "#main h1" });
    await openRail(h);
    await selectTab(h, "Share");
    await clickFirst(h.page, ".underline-panels-panel:not([hidden]) summary", { hasText: "Add public link" });
    await settle(h.page, 400);
  });
  act("menu-page-actions", async (h) => {
    await go(h, `/n/${survey.id}`, { shell: true, ready: "#main h1" });
    await clickFirst(h.page, "[aria-label='More actions']");
    await h.page.waitForSelector("[role=menu]", { state: "visible", timeout: 6000 });
    await settle(h.page, 300);
  });
  act("menu-account", async (h) => {
    await go(h, `/s/${slug}`, { shell: true, ready: SHELL_READY });
    await clickFirst(h.page, ".app-header-avatar");
    await h.page.waitForSelector("[role=menu]", { state: "visible", timeout: 6000 });
    await settle(h.page, 300);
  });
  act("menu-tree-context", (h) => treeContextMenu(h, "Meeting notes"));
  act("menu-folder-row", async (h) => {
    await go(h, `/s/${slug}/d/${enc(S.folders.harbour.path)}`, { shell: true, ready: "#main h1" });
    await clickFirst(h.page, ".file-table tbody tr .col-actions button");
    await h.page.waitForSelector("[role=menu]", { state: "visible", timeout: 6000 });
    await settle(h.page, 300);
  });
  act("menu-file-header", async (h) => {
    await go(h, `/s/${slug}/f/${enc(S.files.pdf.path)}`, { shell: true, ready: ".file-view-header h1" });
    await clickFirst(h.page, ".header-menu");
    await h.page.waitForSelector("[role=menu]", { state: "visible", timeout: 6000 });
    await settle(h.page, 300);
  });
  act("menu-trash-row", async (h) => {
    await go(h, `/s/${slug}/trash`, { shell: true, ready: SHELL_READY });
    await clickFirst(h.page, ".trash-row .btn-icon, .trash-row button[aria-haspopup]");
    await h.page.waitForSelector("[role=menu]", { state: "visible", timeout: 6000 });
    await settle(h.page, 300);
  });
  act("confirm-delete-forever", async (h) => {
    await go(h, `/s/${slug}/trash`, { shell: true, ready: SHELL_READY });
    await clickFirst(h.page, ".trash-row .btn-icon, .trash-row button[aria-haspopup]");
    await h.page.waitForSelector("[role=menu]", { state: "visible", timeout: 6000 });
    await openMenuItem(h, "Delete forever");
    await h.page.waitForSelector("dialog[open]", { state: "visible", timeout: 6000 });
    await settle(h.page, 400);
  }, { focus: true, teardown: (h) => h.page.keyboard.press("Escape") });
  act("nav-drawer", async (h) => {
    await go(h, `/n/${survey.id}`, { shell: true, ready: "#main h1" });
    await clickFirst(h.page, ".app-header [aria-label='Open navigation']");
    await h.page.waitForSelector("dialog.nav-drawer[open]", { timeout: 6000 });
    await settle(h.page, 400);
  }, { when: (p) => p.viewport.width < 768, focus: true });
  act("sync-overlay", async (h) => {
    await go(h, `/s/${slug}`, { shell: true, ready: SHELL_READY });
    // The sync state is a badge on the Avatar. Its details and Sync now open in the account menu.
    await clickFirst(h.page, ".app-header-avatar");
    await h.page.waitForSelector("[role=menu]", { state: "visible", timeout: 6000 });
    await settle(h.page, 400);
  });
  act("quick-switcher", async (h) => {
    await go(h, `/s/${slug}`, { shell: true, ready: SHELL_READY });
    if (h.narrow) await clickFirst(h.page, ".app-header [aria-label='Search']");
    else await h.page.keyboard.press("Control+k");
    await h.page.waitForSelector("dialog.switcher-dialog[open]", { timeout: 6000 });
    await h.page.keyboard.type("harb");
    await h.page.waitForSelector("dialog.switcher-dialog [role=option]", { timeout: 6000 });
    await settle(h.page, 400);
  }, { focus: true });
  for (const [id, item] of [["move-dialog", "Move to"], ["copy-dialog", "Copy to"]]) {
    act(id, async (h) => {
      await treeContextMenu(h, "Meeting notes");
      await h.page.locator("[role=menuitem]", { hasText: new RegExp(`^\\s*${item}\\s*$`) }).filter({ visible: true }).first().click();
      await h.page.waitForSelector("dialog.move-dialog[open]", { timeout: 6000 });
      await settle(h.page, 400);
    }, { focus: id === "move-dialog" });
  }
  act("undo-move-banner", async (h) => {
    // Real flow: the folder row menu, Move to, the target folder, Move here.
    const box = h.sandbox;
    await go(h, `/s/${slug}/d/${enc(box.box.path)}`, { shell: true, ready: "#main h1" });
    const row = h.page.locator(".file-table tbody tr", { hasText: /Move me \d/ }).filter({ visible: true }).first();
    await row.waitFor({ timeout: 8000 });
    await row.locator(".col-actions button").first().click();
    await h.page.waitForSelector("[role=menu]", { state: "visible", timeout: 6000 });
    await h.page.locator("[role^=menuitem]", { hasText: /^\s*Move to\s*$/ }).filter({ visible: true }).first().click();
    await h.page.waitForSelector("dialog.move-dialog[open]", { timeout: 6000 });
    await h.page.locator("dialog.move-dialog[open] .tree-view-item-content", { hasText: /^\s*Target \d+\s*$/ }).filter({ visible: true }).first().click();
    await h.page.locator("dialog.move-dialog[open] button", { hasText: /^\s*Move here\s*$/ }).first().click();
    await h.page.locator("[data-ui-banner], .undo-row", { hasText: "Moved" }).filter({ visible: true }).first().waitFor({ timeout: 8000 });
    await settle(h.page, 400);
  }, { mutates: true, teardown: (h) => clickUndo(h.page) });
  act("undo-drag-move", async (h) => {
    // Real flow: drag the note onto the target folder in the page tree.
    const box = h.sandbox;
    await go(h, `/s/${slug}/d/${enc(box.box.path)}`, { shell: true, ready: "#main h1" });
    // An earlier state can leave the note outside its sandbox when its Undo did not run. Put it back first.
    await h.page.evaluate(async ({ noteId, boxId }) => {
      const app = window.markspaceApp;
      const notes = await app.store.listNotes(app.currentSpace.id);
      const rec = notes.find((n) => n.id === noteId);
      if (rec && (rec.parentId || "") !== boxId) await app.mods.filetree.moveItemsTo(app, [{ kind: "note", rec }], boxId);
    }, { noteId: box.move.id, boxId: box.box.id });
    await go(h, `/s/${slug}/d/${enc(box.box.path)}`, { shell: true, ready: "#main h1" });
    const tree = h.page.locator(".sidebar .file-tree").first();
    const sandboxRow = tree.locator("[role=treeitem][aria-expanded='false'] > .tree-view-item-content", { hasText: /^\s*Sandbox \d+\s*$/ }).filter({ visible: true }).first();
    if (await sandboxRow.count()) {
      await sandboxRow.locator(".tree-view-toggle").first().click();
      await settle(h.page, 400);
    }
    const src = tree.locator(".tree-view-item-content", { hasText: /^\s*Move me \d+\s*$/ }).filter({ visible: true }).first();
    const dst = tree.locator(".tree-view-item-content", { hasText: /^\s*Target \d+\s*$/ }).filter({ visible: true }).first();
    await src.waitFor({ timeout: 8000 });
    await dst.waitFor({ timeout: 8000 });
    await src.dragTo(dst);
    await h.page.locator("[data-ui-banner], .undo-row", { hasText: "Moved" }).filter({ visible: true }).first().waitFor({ timeout: 8000 });
    await settle(h.page, 400);
  }, { mutates: true, when: (p) => p.viewport.width >= 1400 && !p.phone, teardown: (h) => clickUndo(h.page) });
  act("undo-trash-banner", async (h) => {
    // Real flow: the page actions menu, Move to trash.
    const box = h.sandbox;
    await go(h, `/n/${box.trash.id}`, { shell: true, ready: "#main h1" });
    await clickFirst(h.page, "[aria-label='More actions']");
    await h.page.waitForSelector("[role=menu]", { state: "visible", timeout: 6000 });
    await h.page.locator("[role^=menuitem]", { hasText: /^\s*Move to trash\s*$/ }).filter({ visible: true }).first().click();
    await h.page.locator("[data-ui-banner] button, .undo-row button", { hasText: /^\s*Undo\s*$/ }).filter({ visible: true }).first().waitFor({ timeout: 8000 });
    await settle(h.page, 400);
  }, { mutates: true, teardown: (h) => clickUndo(h.page) });
  act("undo-remove-share", async (h) => {
    const box = h.sandbox;
    await go(h, `/n/${box.shared.id}`, { shell: true, ready: "#main h1" });
    await openRail(h);
    await selectTab(h, "Share");
    const row = h.page.locator(".underline-panels-panel:not([hidden]) li", { hasText: box.people[1] }).filter({ visible: true }).first();
    await row.waitFor({ timeout: 8000 });
    await row.locator("button[aria-haspopup]").last().click();
    await openMenuItem(h, "Remove access");
    await h.page.locator(".undo-row").filter({ visible: true }).first().waitFor({ timeout: 8000 });
    await settle(h.page, 400);
  }, { mutates: true, teardown: (h) => clickUndo(h.page) });
  act("undo-resolve-comment", async (h) => {
    const box = h.sandbox;
    await go(h, `/n/${box.shared.id}`, { shell: true, ready: "#main h1" });
    await openRail(h);
    await selectTab(h, "Comments");
    // Resolve thread sits in the overflow menu of the first comment.
    await clickFirst(h.page, ".underline-panels-panel:not([hidden]) .ms-thread .ms-comment-actions button");
    await openMenuItem(h, "Resolve thread");
    await h.page.locator(".undo-row, [data-ui-banner]", { hasText: "Undo" }).filter({ visible: true }).first().waitFor({ timeout: 8000 });
    await settle(h.page, 400);
  }, { mutates: true, teardown: (h) => clickUndo(h.page) });
  act("form-token-invalid", async (h) => {
    await go(h, "/settings/tokens", { ready: "main h1" });
    const form = h.page.locator("form[aria-label='New token']");
    if (!(await form.isVisible())) await clickFirst(h.page, "summary, button", { hasText: "New token" });
    await form.waitFor({ state: "visible", timeout: 5000 });
    await form.locator("button[type=submit]").click();
    await h.page.locator(".form-validation, .field-error, [data-flash=error]").filter({ visible: true }).first().waitFor({ timeout: 10000 });
    await settle(h.page);
  }, { focus: true, expectConsole: [/status of (400|422)/] });
  act("offline-shell", async (h) => {
    await go(h, `/n/${survey.id}`, { shell: true, ready: "#main h1" });
    await h.context.setOffline(true);
    await h.page.evaluate((u) => window.markspaceApp.navigate(u), `/n/${S.notes.meeting.id}`);
    await sleep(2500);
    await settle(h.page);
  }, { teardown: (h) => h.context.setOffline(false), expectConsole: [/ERR_INTERNET_DISCONNECTED|Failed to fetch|NetworkError|net::ERR/] });
  act("pdf-thumbnails", async (h) => {
    await go(h, `/s/${slug}/f/${enc(S.files.pdf.path)}`, { shell: true, ready: ".file-view-header h1" });
    await h.page.waitForFunction(() => Number((document.querySelector(".fv-pdf") || {}).dataset?.pageCount) > 1, null, { timeout: 15000 });
    const paneVisible = await h.page.locator(".fv-pdf-thumbs").filter({ visible: true }).count();
    if (h.narrow || !paneVisible) await clickFirst(h.page, ".fv-pdf-thumbs-toggle");
    await h.page.waitForSelector(".fv-pdf-thumb", { state: "visible", timeout: 8000 });
    await settle(h.page, 800);
  }, { focus: true });

  act("history-dialog", async (h) => {
    await go(h, `/n/${survey.id}`, { shell: true, ready: "#main h1" });
    await openRail(h);
    await selectTab(h, "History");
    await clickFirst(h.page, ".ms-history-open button");
    await h.page.waitForSelector("dialog.history-dialog[open]", { timeout: 15000 });
    await settle(h.page, 900);
  }, { focus: true });

  /* Layout states. Each page in every sidebar, rail and drawer state, with the click pass. */
  const layout = (id, variant, when, run) => add({ id: `layout-${id}-${variant}`, group: "layout", focus: false, clicks: true, when, run, teardown: clearLayout });
  const wide = (p) => p.viewport.width >= 768;
  const narrowOnly = (p) => p.viewport.width < 768;
  const LAYOUT_PAGES = [
    ["space", `/s/${slug}`, { shell: true, ready: SHELL_READY }],
    ["file-pdf", `/s/${slug}/f/${enc(S.files.pdf.path)}`, { shell: true, ready: ".file-view-header h1" }],
    ["admin", "/admin", { ready: "main h1" }],
    ["admin-users", "/admin/users", { ready: "main h1" }],
    ["admin-settings", "/admin/settings", { ready: "main h1" }],
    ["settings", "/settings", { ready: "main h1" }],
  ];
  for (const [id, path, opts] of LAYOUT_PAGES) {
    layout(id, "sidebar-hidden", wide, (h) => withStorage(h, path, { "markspace.sidebar": "collapsed" }, opts));
    layout(id, "sidebar-shown", wide, (h) => withStorage(h, path, { "markspace.sidebar": null }, opts));
    layout(id, "drawer-closed", narrowOnly, (h) => withStorage(h, path, { "markspace.sidebar": null }, opts));
    layout(id, "drawer-open", narrowOnly, async (h) => {
      await withStorage(h, path, { "markspace.sidebar": null }, opts);
      await clickFirst(h.page, ".app-header [aria-label='Open navigation']");
      // The shell opens dialog.nav-drawer. Server pages open their own sidebar sheet.
      await h.page.waitForFunction(() => [...document.querySelectorAll("dialog[open], .sidebar, [data-sidebar]")].some((e) => {
        const r = e.getBoundingClientRect();
        return r.width > 200 && r.left <= 1 && getComputedStyle(e).visibility !== "hidden" && (e.matches("dialog[open]") || ["fixed", "absolute"].includes(getComputedStyle(e).position));
      }), null, { timeout: 6000 });
      await settle(h.page, 400);
    });
  }
  const railTab = (page) => page.locator(".underline-panels-tab").filter({ visible: true }).count();
  for (const sidebar of ["hidden", "shown"]) {
    for (const rail of ["open", "closed"]) {
      layout("note", `sidebar-${sidebar}-rail-${rail}`, wide, async (h) => {
        await withStorage(h, `/n/${survey.id}`, { "markspace.sidebar": sidebar === "hidden" ? "collapsed" : null, "markspace.rail": null, "markspace.railRegular": null }, { shell: true, ready: "#main h1" });
        if (rail === "open") await openRail(h);
        else if (await railTab(h.page)) {
          await clickFirst(h.page, ".page-header-actions-regular [data-rail-toggle]");
          await h.page.waitForFunction(() => ![...document.querySelectorAll(".underline-panels-tab")].some((t) => t.getClientRects().length), null, { timeout: 5000 });
          await settle(h.page, 300);
        }
      });
    }
  }
  layout("note", "rail-open-drawer-open", (p) => p.viewport.width >= 768 && p.viewport.width < 1400, async (h) => {
    await withStorage(h, `/n/${survey.id}`, { "markspace.sidebar": null, "markspace.rail": null, "markspace.railRegular": null }, { shell: true, ready: "#main h1" });
    await openRail(h);
    await clickFirst(h.page, ".app-header-sidebar-toggle, .app-header [aria-controls][aria-expanded]");
    await h.page.waitForSelector("dialog[open]", { timeout: 6000 });
    await settle(h.page, 400);
  });
  layout("note", "drawer-closed", narrowOnly, (h) => withStorage(h, `/n/${survey.id}`, { "markspace.sidebar": null }, { shell: true, ready: "#main h1" }));
  layout("note", "drawer-open", narrowOnly, async (h) => {
    await withStorage(h, `/n/${survey.id}`, { "markspace.sidebar": null }, { shell: true, ready: "#main h1" });
    await clickFirst(h.page, ".app-header [aria-label='Open navigation']");
    await h.page.waitForSelector("dialog.nav-drawer[open]", { timeout: 6000 });
    await settle(h.page, 400);
  });
  layout("note", "rail-open", narrowOnly, async (h) => {
    await withStorage(h, `/n/${survey.id}`, { "markspace.sidebar": null }, { shell: true, ready: "#main h1" });
    await openRail(h);
  });

  /* Editor states. */
  act("editor-slash-menu", async (h) => {
    await openEditor(h, "live");
    await caret(h, null);
    await h.page.keyboard.press("Enter");
    await h.page.keyboard.type("audit /");
    await h.page.waitForSelector(".cm-tooltip-autocomplete", { state: "visible", timeout: 6000 });
    await settle(h.page, 300);
  }, { editor: true, teardown: (h) => undoEditorTyping(h, 1) });
  act("editor-link-completion", async (h) => {
    await openEditor(h, "live");
    await caret(h, null);
    await h.page.keyboard.press("Enter");
    await h.page.keyboard.type("audit [[");
    await h.page.waitForSelector(".cm-tooltip-autocomplete li", { state: "visible", timeout: 6000 });
    await settle(h.page, 300);
  }, { editor: true, teardown: (h) => undoEditorTyping(h, 1) });
  act("ask-bar-answer", async (h) => {
    await openEditor(h, "live");
    await caret(h, "north quay", { offset: 3 });
    await openAskBar(h);
    const input = h.page.locator(".ask-bar .ask-input").first();
    await input.fill("What does the survey cover?");
    await input.press("Enter");
    await h.page.locator(".ask-bar .answer-actions, .ask-bar .answer-body").first().waitFor({ state: "visible", timeout: 20000 });
    await settle(h.page, 600);
  }, { editor: true, teardown: async (h) => { await h.page.keyboard.press("Escape"); await h.page.keyboard.press("Escape"); await sleep(3000); } });
  act("ask-bar-tracked-changes", async (h) => {
    await openEditor(h, "live");
    const needle = "The closing estimate of forty tonnes looks too high for one tide.";
    await caret(h, needle, { length: needle.length });
    await openAskBar(h);
    await clickFirst(h.page, ".ask-bar .ask-action", { hasText: "Improve" });
    await h.page.locator(".change-header[data-status='open'], .hunk-ins, .hunk-del").first().waitFor({ state: "visible", timeout: 20000 });
    await settle(h.page, 600);
  }, {
    editor: true,
    teardown: async (h) => {
      const discard = h.page.locator(".change-header button, .ms-kbar button", { hasText: /Discard all/ }).filter({ visible: true }).first();
      if (await discard.count()) await discard.click().catch(() => {});
      await h.page.keyboard.press("Escape").catch(() => {});
      // Let the autosave finish, so the next editor state does not start from a stale base and get a 409.
      await sleep(3000);
    },
  });
  act("image-actions", async (h) => {
    await openEditor(h, "live");
    const img = h.page.locator(".cm-ms-media img").first();
    await img.scrollIntoViewIfNeeded({ timeout: 8000 });
    if (h.profile.phone) await img.tap();
    else await img.click();
    await h.page.locator(".image-actions, .ms-kbar[aria-label='Image actions']").filter({ visible: true }).first().waitFor({ timeout: 8000 });
    await settle(h.page, 500);
  }, { editor: true });
  act("image-editor", async (h) => {
    await openEditor(h, "live");
    const img = h.page.locator(".cm-ms-media img").first();
    await img.scrollIntoViewIfNeeded({ timeout: 8000 });
    if (h.profile.phone) await img.tap();
    else await img.click();
    await h.page.locator(".image-actions, .ms-kbar[aria-label='Image actions']").filter({ visible: true }).first().waitFor({ timeout: 8000 });
    const edit = h.page.locator(".image-action, .ms-kbar button, [role=menuitem]", { hasText: "Edit image" }).filter({ visible: true }).first();
    if (await edit.count()) await edit.click();
    else await h.page.getByRole("button", { name: "Edit image" }).filter({ visible: true }).first().click();
    await h.page.locator(".image-editor").filter({ visible: true }).first().waitFor({ timeout: 8000 });
    await settle(h.page, 600);
  }, { editor: true, focus: true, teardown: (h) => h.page.locator(".image-editor-back").filter({ visible: true }).first().click({ timeout: 3000 }).catch(() => {}) });
  // The find bar with matches. Below 382px the count shows its short form, and the editor bar holds More editor actions.
  act("editor-find", async (h) => {
    await openEditor(h, "live");
    await h.page.evaluate(() => document.querySelector(".ms-editor").msHandle.openFind());
    await h.page.locator(".ms-editor .ms-find-input").filter({ visible: true }).first().waitFor({ timeout: 6000 });
    await h.page.keyboard.type("tide");
    await settle(h.page, 600);
  }, { editor: true, teardown: async (h) => { await h.page.keyboard.press("Escape").catch(() => {}); } });
  // The note view's Reading find bar opens from "Find in note" in the narrow More actions menu.
  act("note-find", async (h) => {
    await go(h, `/n/${survey.id}`, { shell: true, ready: "#main h1" });
    await clickFirst(h.page, ".page-header-actions-narrow [aria-label='More actions']");
    await h.page.locator("[role^=menuitem]", { hasText: /^\s*Find in note\s*$/ }).filter({ visible: true }).first().click();
    await h.page.locator(".note-view > .ms-find .ms-find-input").filter({ visible: true }).first().waitFor({ timeout: 8000 });
    await h.page.keyboard.type("tide");
    await settle(h.page, 900);
  }, { when: (p) => p.viewport.width < 768, teardown: async (h) => { await h.page.keyboard.press("Escape").catch(() => {}); } });

  return list;
}

/**
 * Chooses which states run on a profile and theme, and whether the focus
 * walk runs. The matrix keeps the full run under 15 minutes.
 */
export function plan(states, profile, theme) {
  const out = [];
  for (const s of states) {
    if (s.when && !s.when(profile)) continue;
    // Layout states run in the light theme on the profiles that change the layout.
    if (s.group === "layout" && (theme === "dark" || !["1440x900", "1024x768", "390x844", "844x390"].includes(profile.name))) continue;
    if (profile.zoom) {
      // 200% zoom: every page once in light, and the overlays in light.
      if (theme === "dark") continue;
      if (s.group === "interactive" && !/^(menu-page-actions|menu-account|quick-switcher|move-dialog|confirm-delete-forever|sync-overlay|rail-share|rail-comments|editor-slash-menu|ask-bar-answer|pdf-thumbnails)/.test(s.id)) continue;
    }
    if (s.kind === "fallback" && theme === "dark" && !["1440x900", "390x844"].includes(profile.name)) continue;
    const focusProfiles = ["1440x900", "1024x768", "390x844", "720x450@2x"];
    out.push({ ...s, focus: !!s.focus && theme === "light" && focusProfiles.includes(profile.name) });
  }
  return out;
}
