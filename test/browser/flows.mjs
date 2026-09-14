// Flow checks for user-reported shell bugs. Unlike layout.mjs, which measures
// layout, each check drives one flow and asserts its outcome:
//
//   1. the space page shows one New page
//   2. root tree rows start where the NavList visuals start
//   3. the sync state is a badge on the Avatar, and the account menu starts
//      with the sync status and Sync now
//   4. comment metadata is one small muted line with a 16px Avatar, and the
//      comment actions sit in the overflow menu
//   5. the History tab keeps a short list, and the history Dialog is full
//      screen with versions, the compare control, Download and Restore, and a
//      side-by-side diff from 768px or a unified diff below
//   6. every admin control takes a click while the sidebar is hidden
//   7. the undo Banner aligns its icon, first text line, Undo and dismiss
//   8. the Edit button on an empty note has text contrast of at least 4.5:1
//   9. Find at 320 x 568 on a touch phone: "Find in note" in the note view's
//      More actions menu opens a Reading find bar, the editor bar fits its
//      padding without overlaps and opens Find from More editor actions, the
//      selection tools have a More menu with Find and replace, and the match
//      count shows its short form with the full text as its tooltip
//  10. the app header New page is a primary Button with text or icon contrast
//      of at least 4.5:1 or 3:1 and a keyboard focus ring of at least 3:1
//      against the header, in light and dark at 1440 x 900 and 390 x 844, and
//      it stays hidden when the PageHeader has its own New page
//
// Checks 1 to 8 use one context at 1440 x 900 and 390 x 844. Checks 9 and 10
// use their own contexts.
//
// node test/browser/flows.mjs --bin=<markspace binary> [--tmp=<dir>]
// node test/browser/flows.mjs --base=<url> --seed-file=<seed.json> [--only=9]
//
// --base and --seed-file run the checks against a server that an earlier run
// seeded. --only runs only the listed checks: "9", "10", or "1-8".
import { mkdtempSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { loadPlaywright, launch } from "./lib/pw.mjs";
import { startStack } from "./lib/stack.mjs";
import { stopAll } from "./lib/procs.mjs";
import { seed, ADMIN } from "./lib/seed.mjs";

const args = Object.fromEntries(process.argv.slice(2).map((a) => {
  const m = /^--([^=]+)(?:=(.*))?$/.exec(a);
  return m ? [m[1], m[2] ?? "1"] : [a, "1"];
}));
if (!args.bin && !(args.base && args["seed-file"])) {
  console.error("usage: node test/browser/flows.mjs --bin=<markspace binary> [--tmp=dir] | --base=<url> --seed-file=<seed.json> [--only=9|10|1-8]");
  process.exit(2);
}
const only = args.only || "all";
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const problems = [];
const check = (ok, text) => {
  console.log((ok ? "  PASS " : "  FAIL ") + text);
  if (!ok) problems.push(text);
};

const tmp = resolve(args.tmp || mkdtempSync(join(tmpdir(), "markspace-flows-")));
let browser = null;
let exitCode = 1;
try {
  const pw = loadPlaywright();
  const stack = args.base ? { base: args.base.replace(/\/$/, "") } : await startStack({ bin: resolve(args.bin), tmp });
  const S = args["seed-file"] ? JSON.parse(readFileSync(args["seed-file"], "utf8")) : await seed(pw, stack.base, { tmp, dataDir: stack.dataDir, pluginBase: stack.pluginBase, workers: 1 });
  browser = await launch();
  if (only === "all" || only === "9") await findFlows({ browser, base: stack.base, S });
  if (only === "all" || only === "10") await headerNewPageFlows({ browser, base: stack.base, S });
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, reducedMotion: "reduce", serviceWorkers: "allow" });
  await context.addInitScript(() => {
    try {
      localStorage.setItem("markspace.theme", "light");
      localStorage.setItem("markspace.editor.focus", "0");
    } catch {
      /* storage off */
    }
  });
  const login = await context.request.post(stack.base + "/api/auth/login", { data: { email: ADMIN.email, password: ADMIN.password } });
  if (!login.ok()) throw new Error("admin login failed: " + login.status());
  const page = await context.newPage();
  page.on("dialog", (d) => d.dismiss().catch(() => {}));
  await page.goto(stack.base + "/", { waitUntil: "domcontentloaded" });
  await page.evaluate(async () => { await navigator.serviceWorker.ready; });
  await page.reload({ waitUntil: "domcontentloaded" });
  if (only === "all" || only === "1-8") await runFlows({ page, context, base: stack.base, S });
  exitCode = problems.length ? 1 : 0;
  console.log(problems.length ? `flows: ${problems.length} problems` : "flows: all passed");
} catch (err) {
  console.error(err);
} finally {
  if (browser) await browser.close().catch(() => {});
  await stopAll().catch(() => {});
  process.exit(exitCode);
}

async function runFlows({ page, context, base, S }) {
  const slug = S.space.slug;
  const waitShell = () => page.waitForFunction(() => window.markspaceApp && window.markspaceApp.spaces && window.markspaceApp.spaces.length > 0, null, { timeout: 20000 });
  const goShell = async (path, ready = "#main h1") => {
    await page.goto(base + path);
    await waitShell();
    await page.waitForSelector(ready);
    await sleep(700);
  };

  for (const vp of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(vp);
    console.log(`== ${vp.width} x ${vp.height}`);

    // 1. One New page on the space page.
    await goShell(`/s/${slug}`);
    const newPages = await page.evaluate(() => [...document.querySelectorAll("button, a")].filter((b) => b.getClientRects().length && /^\s*New page\s*$/.test(b.getAttribute("aria-label") || b.textContent)).length);
    check(newPages === 1, `the space page shows one New page (${newPages})`);

    // 2. Root tree rows align with the NavList.
    if (vp.width >= 1012) {
      const t = await page.evaluate(() => {
        const left = (e) => (e ? Math.round(e.getBoundingClientRect().left) : null);
        const nav = left(document.querySelector(".sidebar .nav-list-item .nav-list-visual"));
        const roots = [...document.querySelectorAll(".sidebar .file-tree [role=treeitem][aria-level='1']")];
        const anyKids = roots.some((li) => li.hasAttribute("aria-expanded"));
        const leaf = roots.find((li) => !li.hasAttribute("aria-expanded"));
        const branch = roots.find((li) => li.hasAttribute("aria-expanded"));
        return { nav, anyKids, leafVisual: leaf && left(leaf.querySelector(".tree-view-visual")), branchToggle: branch && left(branch.querySelector(".tree-view-toggle")) };
      });
      check(t.anyKids ? t.branchToggle === t.nav : t.leafVisual === t.nav, `root tree rows start at the NavList visual (${JSON.stringify(t)})`);
    }

    // 3. Avatar badge and account menu.
    const badge = await page.evaluate(() => {
      const b = document.querySelector(".app-header-avatar .app-header-sync-badge");
      const r = b && b.getBoundingClientRect();
      return { visible: !!(r && r.width), state: b && b.dataset.state, label: document.querySelector(".app-header-avatar").getAttribute("aria-label"), headerIndicator: !!document.querySelector(".sync-indicator") };
    });
    check(badge.visible && !badge.headerIndicator && /^Account, /.test(badge.label), `the sync state is a badge on the Avatar (${JSON.stringify(badge)})`);
    await page.locator(".app-header-avatar").click();
    await page.waitForSelector("[role=menu]");
    const menu = await page.evaluate(() => {
      const m = document.querySelector("[role=menu]");
      const items = [...m.querySelectorAll("[role^=menuitem]")].map((i) => i.textContent.trim());
      return { first: items[0], status: /synced|Syncing|Offline|Signed out|failed|waiting/i.test(m.textContent), overlay: !!document.querySelector("[aria-label='Sync status']") };
    });
    check(/^Sync now|^Sign in to sync/.test(menu.first) && menu.status && !menu.overlay, `the account menu starts with the sync status and Sync now (${JSON.stringify(menu)})`);
    await page.keyboard.press("Escape");

    // 8. Edit on an empty note.
    const created = await context.request.post(base + "/api/notes", { data: { spaceId: S.space.id, title: "Empty flow check " + vp.width, content: "" } });
    const empty = await created.json();
    await goShell(`/n/${empty.id}`);
    const ratios = await page.evaluate(() => {
      const lum = (c) => {
        const m = c.match(/[\d.]+/g).map(Number);
        const f = (v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; };
        return 0.2126 * f(m[0]) + 0.7152 * f(m[1]) + 0.0722 * f(m[2]);
      };
      return [...document.querySelectorAll("#main a.btn, #main button.btn")].filter((b) => b.getClientRects().length && /^\s*Edit\s*$/.test(b.textContent)).map((b) => {
        const cs = getComputedStyle(b);
        const a = lum(cs.color);
        const g = lum(cs.backgroundColor);
        return Number(((Math.max(a, g) + 0.05) / (Math.min(a, g) + 0.05)).toFixed(2));
      });
    });
    check(ratios.length > 0 && ratios.every((x) => x >= 4.5), `Edit buttons on an empty note have text contrast of at least 4.5:1 ${JSON.stringify(ratios)}`);

    // 4. Comment metadata.
    await goShell(`/n/${S.notes.survey.id}`);
    if (vp.width < 768) {
      await page.locator(".page-header-actions-narrow [aria-label='More actions']").first().click();
      await page.locator("[role^=menuitem]", { hasText: /^\s*Page details\s*$/ }).filter({ visible: true }).first().click();
      await page.waitForSelector("dialog.rail-dialog[open] .underline-panels-tab");
    } else {
      const tab = page.locator(`.underline-panels-tab[data-label="Comments"]`).filter({ visible: true }).first();
      await tab.waitFor({ timeout: 2500 }).catch(() => {});
      if (!(await tab.count())) await page.locator(".page-header-actions-regular [data-rail-toggle]").first().click();
    }
    await page.locator(`.underline-panels-tab[data-label="Comments"]`).filter({ visible: true }).first().click();
    await page.waitForSelector(".ms-comment", { timeout: 10000 });
    await sleep(600);
    const meta = await page.evaluate(() => {
      const c = [...document.querySelectorAll(".ms-comment")].find((x) => x.getClientRects().length);
      const head = c.querySelector(".ms-comment-head");
      const author = head.querySelector(".ms-comment-author");
      const body = c.querySelector(".ms-comment-body");
      const size = (e) => parseFloat(getComputedStyle(e).fontSize);
      return {
        avatar: Math.round(head.querySelector(".avatar").getBoundingClientRect().width),
        headSize: size(author),
        headWeight: Number(getComputedStyle(author).fontWeight),
        muted: getComputedStyle(author).color !== getComputedStyle(body).color,
        bodySize: size(body),
        headHeight: Math.round(head.getBoundingClientRect().height),
        buttons: [...c.closest(".ms-thread").querySelectorAll("button")].filter((b) => b.getClientRects().length).map((b) => (b.getAttribute("aria-label") || b.textContent).trim()),
      };
    });
    check(meta.avatar === 16 && meta.headSize < meta.bodySize && meta.headWeight < 600 && meta.muted && meta.headHeight <= 28, `comment metadata is one small muted line (${JSON.stringify(meta)})`);
    check(!meta.buttons.some((b) => /^(Resolve|Reopen|Edit|Delete)$/.test(b)), "comment actions sit in the overflow menu");

    // 5. History tab and Dialog.
    await page.locator(`.underline-panels-tab[data-label="History"]`).filter({ visible: true }).first().click();
    await page.waitForSelector(".ms-history-open button", { timeout: 10000 });
    const rail = await page.evaluate(() => ({
      rows: document.querySelectorAll(".underline-panels-panel:not([hidden]) .history-timeline .history-point").length,
      inlineDiffs: document.querySelectorAll(".underline-panels-panel:not([hidden]) details").length,
    }));
    check(rail.rows > 0 && rail.rows <= 5 && rail.inlineDiffs === 0, `the History tab keeps a short list (${JSON.stringify(rail)})`);
    await page.locator(".ms-history-open button").filter({ visible: true }).first().click();
    await page.waitForSelector("dialog.history-dialog[open] .history-listbox [role=option]", { timeout: 15000 });
    // Below 768px the Dialog opens on the version list. Choosing a version opens its diff.
    if (vp.width < 768) await page.locator("dialog.history-dialog[open] .history-listbox [role=option][aria-selected='true']").click();
    await page.waitForSelector("dialog.history-dialog[open] .history-diff .cm-editor, dialog.history-dialog[open] .ms-diff-same", { timeout: 15000 });
    await sleep(600);
    const dlg = await page.evaluate(() => {
      const d = document.querySelector("dialog.history-dialog[open]");
      const r = d.getBoundingClientRect();
      return {
        w: Math.round(r.width),
        h: Math.round(r.height),
        versions: d.querySelectorAll(".history-listbox [role=option]").length,
        compare: d.querySelectorAll(".segmented-control button").length,
        footer: [...d.querySelectorAll(".dialog-footer button")].map((b) => b.textContent.trim()),
        sideBySide: !!d.querySelector(".cm-mergeView"),
        unified: !!d.querySelector(".history-diff .cm-editor.cm-merge-b") && !d.querySelector(".cm-mergeView"),
        same: !!d.querySelector(".ms-diff-same"),
      };
    });
    check(dlg.w >= vp.width - 1 && dlg.h >= vp.height - 1, `the history Dialog is full screen (${dlg.w} x ${dlg.h})`);
    check(dlg.versions > 1 && dlg.compare === 2 && dlg.footer.includes("Download") && dlg.footer.some((label) => /^Restore/.test(label)), `the history Dialog has versions, the compare control, Download and Restore (${JSON.stringify(dlg)})`);
    check(dlg.same || (vp.width >= 768 ? dlg.sideBySide : dlg.unified), `the diff is ${vp.width >= 768 ? "side by side" : "unified"}`);
    await page.locator("dialog.history-dialog[open] .segmented-control button", { hasText: "Compare with current" }).click();
    await sleep(1200);
    const prev = await page.evaluate(() => {
      const d = document.querySelector("dialog.history-dialog[open]");
      return { caption: d.querySelector(".history-diff-caption").textContent, diff: !!d.querySelector(".history-diff .cm-editor, .history-diff .ms-diff-same") };
    });
    check(/current version/i.test(prev.caption) && prev.diff, `the diff compares with the current version (${JSON.stringify(prev)})`);
    await page.keyboard.press("Escape");
    await sleep(300);
    if (vp.width < 768) {
      await page.keyboard.press("Escape");
      await sleep(300);
    }

    // 7. Undo Banner geometry.
    const box = S.sandboxes[0];
    await goShell(`/s/${slug}/d/${box.box.path.split("/").map(encodeURIComponent).join("/")}`);
    await page.evaluate(async ({ noteId, target }) => {
      const app = window.markspaceApp;
      const notes = await app.store.listNotes(app.currentSpace.id);
      await app.mods.filetree.moveItemsTo(app, [{ kind: "note", rec: notes.find((n) => n.id === noteId) }], target);
    }, { noteId: box.move.id, target: box.target.id });
    await sleep(900);
    const g = await page.evaluate(() => {
      const b = [...document.querySelectorAll("[data-ui-banner]")].find((x) => x.getClientRects().length && x.textContent.includes("Moved"));
      if (!b) return null;
      const mid = (e) => { const r = e.getBoundingClientRect(); return r.top + r.height / 2; };
      const range = document.createRange();
      range.selectNodeContents(b.querySelector(".banner-title"));
      const line = range.getClientRects()[0];
      const text = line.top + line.height / 2;
      const undo = b.querySelector(".banner-actions .btn");
      const ur = undo.getBoundingClientRect();
      const tr = b.querySelector(".banner-title").getBoundingClientRect();
      return { icon: mid(b.querySelector(".banner-icon svg")) - text, dismiss: mid(b.querySelector(".banner-dismiss")) - text, undoSameLine: ur.top < tr.bottom, undo: mid(undo) - text, undoIndent: Math.round(ur.left - tr.left) };
    });
    const aligned = g && Math.abs(g.icon) <= 2 && Math.abs(g.dismiss) <= 2 && (g.undoSameLine ? Math.abs(g.undo) <= 2 : Math.abs(g.undoIndent) <= 1);
    check(!!aligned, `the undo Banner aligns its icon, text, Undo and dismiss (${JSON.stringify(g)})`);
    const undoButton = page.locator("[data-ui-banner] button", { hasText: /^\s*Undo\s*$/ }).filter({ visible: true }).first();
    if (await undoButton.count()) {
      await undoButton.click();
      await sleep(900);
    }
  }

  // 6. Admin pages with the sidebar hidden.
  for (const vp of [{ width: 1440, height: 900 }, { width: 800, height: 800 }]) {
    await page.setViewportSize(vp);
    for (const path of ["/admin", "/admin/users", "/admin/settings"]) {
      await page.goto(base + path);
      await page.waitForSelector("main h1");
      await page.evaluate(() => { try { localStorage.setItem("markspace.sidebar", "collapsed"); } catch { /* storage off */ } });
      await page.reload();
      await page.waitForSelector("main h1");
      await sleep(400);
      const controls = page.locator("main a[href], main button, main input:not([type=hidden]), main select, main summary");
      const n = Math.min(await controls.count(), 40);
      const blocked = [];
      for (let i = 0; i < n; i++) {
        const c = controls.nth(i);
        if (!(await c.isVisible())) continue;
        try {
          await c.click({ trial: true, timeout: 1500 });
        } catch (e) {
          blocked.push(((await c.textContent()) || "").trim().slice(0, 24) + ": " + (String(e.message).split("\n").find((l) => /intercepts|outside|disabled/.test(l)) || "timeout").trim());
        }
      }
      check(blocked.length === 0, `${path} at ${vp.width} with the sidebar hidden: every one of ${n} controls takes a click ${blocked.length ? JSON.stringify(blocked.slice(0, 3)) : ""}`);
    }
  }
}

/**
 * 9. Find at 320 x 568 on a touch phone. Below 382px the editor bar has no
 * Find button, so the menus below are the only ways to open Find without a
 * hardware keyboard.
 */
async function findFlows({ browser, base, S }) {
  console.log("== 320 x 568 touch");
  const context = await browser.newContext({ viewport: { width: 320, height: 568 }, deviceScaleFactor: 2, isMobile: true, hasTouch: true, reducedMotion: "reduce", serviceWorkers: "allow" });
  await context.addInitScript(() => {
    try {
      localStorage.setItem("markspace.theme", "light");
      localStorage.setItem("markspace.editor.focus", "0");
    } catch {
      /* storage off */
    }
  });
  try {
    const login = await context.request.post(base + "/api/auth/login", { data: { email: ADMIN.email, password: ADMIN.password } });
    if (!login.ok()) throw new Error("admin login failed: " + login.status());
    const page = await context.newPage();
    page.on("dialog", (d) => d.dismiss().catch(() => {}));
    await page.goto(base + "/", { waitUntil: "domcontentloaded" });
    await page.evaluate(async () => { await navigator.serviceWorker.ready; });
    await page.reload({ waitUntil: "domcontentloaded" });
    const shot = (name) => page.screenshot({ path: join(tmp, `find-320-${name}.png`) }).catch(() => {});

    /** Controls outside the padding box of root, overlapping controls, and the save status icon over a control. */
    const geometry = (rootSel) => page.evaluate((rootSel) => {
      const root = [...document.querySelectorAll(rootSel)].find((e) => e.getClientRects().length);
      if (!root) return null;
      const shown = (e) => e.getClientRects().length > 0 && getComputedStyle(e).visibility !== "hidden";
      const name = (e) => (e.getAttribute("aria-label") || e.textContent || e.className).trim().slice(0, 28);
      const controls = [...root.querySelectorAll("button, a[href], input, [role=button]")].filter(shown).map((e) => ({ e, name: name(e), r: e.getBoundingClientRect() }));
      const cs = getComputedStyle(root);
      const rr = root.getBoundingClientRect();
      const left = rr.left + parseFloat(cs.paddingLeft);
      const right = rr.right - parseFloat(cs.paddingRight);
      const outside = controls.filter((c) => c.r.left < left - 0.5 || c.r.right > right + 0.5 || c.r.right > innerWidth + 0.5).map((c) => `${c.name} ${Math.round(c.r.left)}-${Math.round(c.r.right)} of ${Math.round(left)}-${Math.round(right)}`);
      const cross = (a, b) => Math.min(a.right, b.right) - Math.max(a.left, b.left) > 1 && Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top) > 1;
      const overlaps = [];
      for (let i = 0; i < controls.length; i++) {
        for (let j = i + 1; j < controls.length; j++) {
          const a = controls[i];
          const b = controls[j];
          if (a.e.contains(b.e) || b.e.contains(a.e)) continue;
          if (cross(a.r, b.r)) overlaps.push(`${a.name} / ${b.name}`);
        }
      }
      const statusIcon = root.querySelector(".ms-status-icon");
      if (statusIcon && shown(statusIcon)) {
        for (const c of controls) if (cross(statusIcon.getBoundingClientRect(), c.r)) overlaps.push(`save status / ${c.name}`);
      }
      return { outside, overlaps, names: controls.map((c) => c.name) };
    }, rootSel);
    const menuItems = () => page.evaluate(() => {
      const menu = [...document.querySelectorAll("[role=menu]")].find((m) => m.getClientRects().length);
      return menu ? [...menu.querySelectorAll("[role^=menuitem]")].map((i) => ({ text: i.textContent.trim(), svg: (i.querySelector("svg") || { outerHTML: "" }).outerHTML.slice(0, 120) })) : [];
    });
    const clickMenuItem = (text) => page.locator("[role^=menuitem]", { hasText: new RegExp(`^\\s*${text}\\s*$`) }).filter({ visible: true }).first().click();
    const countState = (barSel) => page.evaluate((barSel) => {
      const bar = [...document.querySelectorAll(barSel)].find((e) => e.getClientRects().length);
      if (!bar) return null;
      const full = bar.querySelector(".ms-find-count");
      const short = bar.querySelector(".ms-find-count-short");
      const status = bar.querySelector(".ms-find-status");
      const sr = short.getBoundingClientRect();
      const st = status.getBoundingClientRect();
      return {
        full: full.textContent,
        short: short.textContent,
        title: short.getAttribute("title"),
        shortShown: sr.width > 0,
        fullHidden: full.getBoundingClientRect().width <= 1,
        fits: sr.right <= st.right + 0.5 && status.scrollWidth <= status.clientWidth + 1,
        focus: document.activeElement === bar.querySelector(".ms-find-input") ? "find" : document.activeElement === bar.querySelector(".ms-replace-input") ? "replace" : String(document.activeElement && document.activeElement.className),
        query: bar.querySelector(".ms-find-input").value,
      };
    }, barSel);
    const countOk = (c) => !!c && c.shortShown && c.fullHidden && c.fits && c.title === c.full && /^\d+\+?(\/\d+\+?)?$/.test(c.short) && /\d/.test(c.full);

    // Note view: Find in note in the narrow More actions menu opens a Reading find bar.
    await page.goto(base + `/n/${S.notes.survey.id}`);
    await page.waitForFunction(() => window.markspaceApp && window.markspaceApp.spaces && window.markspaceApp.spaces.length > 0, null, { timeout: 20000 });
    await page.waitForSelector("#main h1");
    await sleep(700);
    await page.locator(".page-header-actions-narrow [aria-label='More actions']").first().click();
    await page.waitForSelector("[role=menu]");
    const noteMenu = await menuItems();
    const findItem = noteMenu.find((i) => i.text === "Find in note");
    check(!!findItem && /search/.test(findItem.svg), `the note view More actions menu has Find in note with the search Octicon (${JSON.stringify(noteMenu.map((i) => i.text))})`);
    if (findItem) {
      await clickMenuItem("Find in note");
      await page.waitForSelector(".note-view > .ms-find:not([hidden])", { timeout: 8000 });
      await sleep(300);
      await page.keyboard.type("tide");
      await sleep(900);
      const marks = await page.evaluate(() => document.querySelectorAll(".note-view .md-body mark.ms-find-hit").length);
      const c = await countState(".note-view > .ms-find");
      check(marks > 1 && !!c && c.query === "tide", `Find in note opens a Reading find bar that marks the note (${marks} marks, ${JSON.stringify(c)})`);
      check(countOk(c), `the Reading match count shows its short form with the full text as tooltip (${JSON.stringify(c)})`);
      const g = await geometry(".note-view > .ms-find");
      check(!!g && !g.outside.length && !g.overlaps.length, `the Reading find bar fits 320px without overlaps (${JSON.stringify(g && { outside: g.outside, overlaps: g.overlaps })})`);
      await shot("note-find");
      // Switch to Source to replace opens the editor in Source with Replace and the same query.
      await page.locator(".note-view > .ms-find .ms-find-source").click();
      await page.waitForSelector(".ms-editor[data-mode='source'] .ms-find:not([hidden]) .ms-replace-input", { state: "visible", timeout: 15000 });
      await sleep(500);
      const src = await page.evaluate(() => ({ query: document.querySelector(".ms-editor .ms-find-input").value, replaceOpen: !document.querySelector(".ms-editor .ms-find-replace").hidden }));
      check(src.query === "tide" && src.replaceOpen, `Switch to Source to replace keeps the query and opens Replace (${JSON.stringify(src)})`);
      await page.keyboard.press("Escape");
      await page.evaluate(() => { try { localStorage.setItem("markspace.editor.mode", "live"); } catch { /* storage off */ } });
    }

    // Editor bar: the controls fit the padding box, and Find opens from More editor actions.
    await page.goto(base + `/n/${S.notes.survey.id}?edit=1`);
    await page.waitForSelector(".ms-editor .cm-content", { timeout: 20000 });
    await sleep(900);
    const bar = await geometry(".ms-editor-bar");
    check(!!bar && !bar.outside.length && !bar.overlaps.length, `the editor bar fits 320px without overlaps (${JSON.stringify(bar)})`);
    await shot("editor-bar");
    const more = page.locator(".ms-editor-bar .ms-bar-more").filter({ visible: true }).first();
    check((await more.count()) === 1, "the editor bar shows More editor actions below 382px");
    if (await more.count()) {
      await more.click();
      await page.waitForSelector("[role=menu]");
      const items = (await menuItems()).map((i) => i.text);
      check(items.includes("Find") && items.includes("Focus mode") && items.includes("Typewriter scrolling"), `More editor actions holds Find, Typewriter scrolling and Focus mode (${JSON.stringify(items)})`);
      await clickMenuItem("Find");
      await page.waitForSelector(".ms-editor .ms-find:not([hidden])", { timeout: 8000 });
      await sleep(400);
      await page.keyboard.type("tide");
      await sleep(700);
      const c = await countState(".ms-editor .ms-find");
      check(!!c && c.focus === "find" && countOk(c), `Find from More editor actions focuses Find, and the count is short with a tooltip (${JSON.stringify(c)})`);
      const g = await geometry(".ms-editor .ms-find");
      check(!!g && !g.outside.length && !g.overlaps.length, `the editor find bar fits 320px without overlaps (${JSON.stringify(g && { outside: g.outside, overlaps: g.overlaps })})`);
      await shot("editor-find");
      await page.keyboard.press("Escape");
      await sleep(300);
    }

    // Selection tools: More selection actions holds Find and replace, which starts with the selected text.
    await page.evaluate(async () => {
      const mod = await import("/static/vendor/codemirror.js");
      const view = mod.EditorView.findFromDOM(document.querySelector(".cm-editor"));
      const at = view.state.doc.toString().indexOf("quay");
      view.focus();
      view.dispatch({ selection: { anchor: at, head: at + 4 }, scrollIntoView: true });
    });
    await page.waitForSelector(".ms-kbar[aria-label='Selection']:not([hidden])", { timeout: 8000 });
    await sleep(400);
    const kbar = await geometry(".ms-kbar-tools");
    check(!!kbar && kbar.names.includes("More selection actions") && !kbar.outside.length && !kbar.overlaps.length, `the selection tools have More selection actions and fit 320px (${JSON.stringify(kbar)})`);
    await shot("selection-tools");
    const selMore = page.locator(".ms-kbar [data-kbar='more']").filter({ visible: true }).first();
    if (await selMore.count()) {
      await selMore.tap();
      await page.waitForSelector("[role=menu]");
      const items = (await menuItems()).map((i) => i.text);
      check(items.includes("Find and replace"), `More selection actions holds Find and replace (${JSON.stringify(items)})`);
      if (items.includes("Find and replace")) {
        await page.locator("[role^=menuitem]", { hasText: /^\s*Find and replace\s*$/ }).filter({ visible: true }).first().tap();
        await page.waitForSelector(".ms-editor .ms-find:not([hidden])", { timeout: 8000 });
        await sleep(600);
        const c = await countState(".ms-editor .ms-find");
        check(!!c && c.query === "quay" && c.focus === "replace" && countOk(c), `Find and replace from the selection starts with the selected text and focuses Replace (${JSON.stringify(c)})`);
        await shot("selection-find");
        await page.keyboard.press("Escape");
      }
    }
  } finally {
    await context.close().catch(() => {});
  }
}

/**
 * 10. The app header New page is a primary Button. It passes text or icon
 * contrast and shows a focus ring on keyboard focus in both themes, and it
 * hides where the PageHeader has its own New page (docs/primer.md 5.1 App header).
 */
async function headerNewPageFlows({ browser, base, S }) {
  for (const theme of ["light", "dark"]) {
    for (const vp of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
      console.log(`== header New page ${vp.width} x ${vp.height} ${theme}`);
      const phone = vp.width < 768;
      const context = await browser.newContext({ viewport: vp, reducedMotion: "reduce", serviceWorkers: "allow", ...(phone ? { hasTouch: true, isMobile: true, deviceScaleFactor: 2 } : {}) });
      await context.addInitScript((t) => {
        try {
          localStorage.setItem("markspace.theme", t);
        } catch {
          /* storage off */
        }
      }, theme);
      try {
        const login = await context.request.post(base + "/api/auth/login", { data: { email: ADMIN.email, password: ADMIN.password } });
        if (!login.ok()) throw new Error("admin login failed: " + login.status());
        const page = await context.newPage();
        await page.goto(base + "/", { waitUntil: "domcontentloaded" });
        await page.evaluate(async () => { await navigator.serviceWorker.ready; });
        const open = async (path) => {
          await page.goto(base + path);
          await page.waitForFunction(() => window.markspaceApp && window.markspaceApp.spaces && window.markspaceApp.spaces.length > 0, null, { timeout: 20000 });
          await page.waitForSelector("#main h1");
          await sleep(700);
        };

        // A note has no New page in its PageHeader, so the header shows one.
        await open(`/n/${S.notes.survey.id}`);
        const header = await page.evaluate(() => {
          const lum = (c) => {
            const m = c.match(/[\d.]+/g).map(Number);
            const f = (v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; };
            return 0.2126 * f(m[0]) + 0.7152 * f(m[1]) + 0.0722 * f(m[2]);
          };
          const ratio = (a, b) => { const x = lum(a); const y = lum(b); return Number(((Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05)).toFixed(2)); };
          const named = [...document.querySelectorAll(".app-header button, .app-header a")].filter((b) => b.getClientRects().length && /^\s*New page\s*$/.test(b.getAttribute("aria-label") || b.textContent));
          const b = named[0];
          if (!b) return { count: 0 };
          const cs = getComputedStyle(b);
          const label = b.querySelector(".btn-label");
          const glyph = b.querySelector("svg");
          return {
            count: named.length,
            primary: b.classList.contains("btn-primary"),
            iconOnly: !label || !label.getClientRects().length,
            text: ratio(getComputedStyle(label && label.getClientRects().length ? label : b).color, cs.backgroundColor),
            icon: glyph ? ratio(getComputedStyle(glyph).color, cs.backgroundColor) : 0,
          };
        });
        const needed = header.iconOnly ? 3 : 4.5;
        check(header.count === 1 && header.primary && (header.iconOnly ? header.icon : header.text) >= needed, `the header New page on a note is one primary Button with contrast of at least ${needed}:1 (${JSON.stringify(header)})`);

        // Keyboard focus shows a ring of at least 3:1 against the header.
        let ring = null;
        for (let i = 0; i < 40 && !ring; i++) {
          await page.keyboard.press("Tab");
          ring = await page.evaluate(() => {
            const b = document.activeElement;
            if (!b || !b.closest(".app-header") || !/^\s*New page\s*$/.test(b.getAttribute("aria-label") || b.textContent)) return null;
            const lum = (c) => {
              const m = c.match(/[\d.]+/g).map(Number);
              const f = (v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; };
              return 0.2126 * f(m[0]) + 0.7152 * f(m[1]) + 0.0722 * f(m[2]);
            };
            const cs = getComputedStyle(b);
            let bgEl = b.closest(".app-header");
            let bg = getComputedStyle(bgEl).backgroundColor;
            while (bgEl && /rgba\(0, 0, 0, 0\)|transparent/.test(bg)) { bgEl = bgEl.parentElement; bg = bgEl ? getComputedStyle(bgEl).backgroundColor : "rgb(255, 255, 255)"; }
            const x = lum(cs.outlineColor);
            const y = lum(bg);
            return { visible: b.matches(":focus-visible"), style: cs.outlineStyle, width: parseFloat(cs.outlineWidth), ratio: Number(((Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05)).toFixed(2)), shadow: cs.boxShadow };
          });
        }
        check(!!ring && ring.visible && ring.style !== "none" && ring.width >= 2 && ring.ratio >= 3, `keyboard focus on the header New page shows a ring of at least 3:1 (${JSON.stringify(ring)})`);

        // The space page has New page in its PageHeader, so the header hides its own.
        await open(`/s/${S.space.slug}`);
        const space = await page.evaluate(() => {
          const visible = [...document.querySelectorAll("button, a")].filter((b) => b.getClientRects().length && /^\s*New page\s*$/.test(b.getAttribute("aria-label") || b.textContent));
          return { total: visible.length, inHeader: visible.filter((b) => b.closest(".app-header")).length };
        });
        check(space.total === 1 && space.inHeader === 0, `the header New page hides when the space PageHeader has New page (${JSON.stringify(space)})`);
      } finally {
        await context.close().catch(() => {});
      }
    }
  }
}
