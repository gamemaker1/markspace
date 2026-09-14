// Flow check for version history: a note with 40 versions spread over several
// days, some written through a plugin. At 1440 x 900 and 390 x 844, in light
// and dark, it checks that
//
//   - the rail History tab shows day groups, clock times, the current
//     version first and Show diff with the version count
//   - a rail point opens the Dialog with that version selected
//   - the Dialog lists every version once its list scrolls to the end
//   - choosing the oldest version shows its diff, and both compare modes work
//   - Up, Home and End move the selection, and Back returns to the list below 768px
//   - Restore of an old version makes it the current text and adds a version
//
// It uses one browser with one context and saves screenshots with --shots=DIR.
//
// node test/browser/history.mjs --bin=<markspace binary> [--tmp=<dir>] [--shots=<dir>]
import { mkdtempSync, mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { loadPlaywright, launch } from "./lib/pw.mjs";
import { startStack } from "./lib/stack.mjs";
import { stopAll } from "./lib/procs.mjs";
import { seed, ADMIN } from "./lib/seed.mjs";
import { seedHistory } from "./lib/history.mjs";

const args = Object.fromEntries(process.argv.slice(2).map((a) => {
  const m = /^--([^=]+)(?:=(.*))?$/.exec(a);
  return m ? [m[1], m[2] ?? "1"] : [a, "1"];
}));
if (!args.bin) {
  console.error("usage: node test/browser/history.mjs --bin=<markspace binary> [--tmp=dir] [--shots=dir]");
  process.exit(2);
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const problems = [];
const check = (ok, text) => {
  console.log((ok ? "  PASS " : "  FAIL ") + text);
  if (!ok) problems.push(text);
};
const tmp = resolve(args.tmp || mkdtempSync(join(tmpdir(), "markspace-history-")));
const shots = args.shots ? resolve(args.shots) : null;
if (shots) mkdirSync(shots, { recursive: true });

let browser = null;
let exitCode = 1;
try {
  const pw = loadPlaywright();
  const stack = await startStack({ bin: resolve(args.bin), tmp });
  const S = await seed(pw, stack.base, { tmp, dataDir: stack.dataDir, pluginBase: stack.pluginBase, workers: 1 });
  browser = await launch();
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, reducedMotion: "reduce", serviceWorkers: "allow" });
  const login = await context.request.post(stack.base + "/api/auth/login", { data: { email: ADMIN.email, password: ADMIN.password } });
  if (!login.ok()) throw new Error("admin login failed: " + login.status());
  const H = await seedHistory(context.request, { base: stack.base, dataDir: stack.dataDir, spaceId: S.space.id, authors: [S.users.admin, S.users.member] });
  const page = await context.newPage();
  page.on("pageerror", (e) => problems.push("page error: " + e.message));
  await page.goto(stack.base + "/", { waitUntil: "domcontentloaded" });
  await page.evaluate(async () => { await navigator.serviceWorker.ready; });
  await page.reload({ waitUntil: "domcontentloaded" });
  await runFlow({ page, context, base: stack.base, H });
  exitCode = problems.length ? 1 : 0;
  console.log(problems.length ? `history flow: ${problems.length} problems` : "history flow: all passed");
} catch (err) {
  console.error(err);
} finally {
  if (browser) await browser.close().catch(() => {});
  await stopAll().catch(() => {});
  process.exit(exitCode);
}

async function runFlow({ page, context, base, H }) {
  const shot = async (name) => {
    if (shots) await page.screenshot({ path: join(shots, name + ".png") });
  };
  const versionCount = async () => {
    const r = await context.request.get(`${base}/api/notes/${H.note.id}/versions`);
    const data = await r.json();
    return (data.items || data).length;
  };
  const dialogState = () => page.evaluate(() => {
    const d = document.querySelector("dialog.history-dialog[open]");
    if (!d) return null;
    const options = [...d.querySelectorAll(".history-listbox [role=option]")];
    const layout = d.querySelector(".history-dialog-layout");
    return {
      options: options.length,
      selected: options.findIndex((o) => o.getAttribute("aria-selected") === "true"),
      selectedId: (options.find((o) => o.getAttribute("aria-selected") === "true") || {}).dataset?.versionId || null,
      focusedIndex: options.indexOf(document.activeElement),
      days: [...d.querySelectorAll(".history-listbox .history-day-label")].map((l) => l.textContent),
      screen: layout && layout.dataset.screen,
      title: (d.querySelector(".history-diff-title") || {}).textContent || "",
      caption: (d.querySelector(".history-diff-caption") || {}).textContent || "",
      diff: d.querySelector(".history-diff .cm-editor") ? "editor" : d.querySelector(".history-diff .ms-diff-same") ? "same" : d.querySelector(".history-diff .inline-message") ? "error" : "loading",
      summary: (d.querySelector(".history-diff-summary") || {}).textContent || "",
      meta: (d.querySelector(".history-diff-meta") || {}).textContent || "",
      firstClock: (options[0] && options[0].querySelector(".history-point-clock") || {}).textContent || "",
      firstCurrent: !!(options[0] && options[0].classList.contains("is-current")),
    };
  });
  const waitDiff = async () => {
    await page.waitForFunction(() => {
      const d = document.querySelector("dialog.history-dialog[open]");
      return d && d.querySelector(".history-diff .cm-editor, .history-diff .ms-diff-same, .history-diff .inline-message");
    }, null, { timeout: 15000 });
    await sleep(300);
  };
  let restored = false;

  for (const vp of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
    for (const theme of ["light", "dark"]) {
      const tag = `${vp.width}x${vp.height}-${theme}`;
      const narrow = vp.width < 768;
      console.log(`== ${tag}`);
      await page.setViewportSize(vp);
      await page.emulateMedia({ colorScheme: theme });
      await page.evaluate((t) => { try { localStorage.setItem("markspace.theme", t); } catch { /* storage off */ } }, theme);
      await page.goto(`${base}/n/${H.note.id}`);
      await page.waitForFunction(() => window.markspaceApp && window.markspaceApp.spaces && window.markspaceApp.spaces.length > 0, null, { timeout: 20000 });
      await page.waitForSelector("#main h1");
      await sleep(700);
      if (narrow) {
        await page.locator(".page-header-actions-narrow [aria-label='More actions']").first().click();
        await page.locator("[role^=menuitem]", { hasText: /^\s*Page details\s*$/ }).filter({ visible: true }).first().click();
        await page.waitForSelector("dialog.rail-dialog[open] .underline-panels-tab");
      } else {
        const tab = page.locator(`.underline-panels-tab[data-label="History"]`).filter({ visible: true }).first();
        await tab.waitFor({ timeout: 2500 }).catch(() => {});
        if (!(await tab.count())) await page.locator(".page-header-actions-regular [data-rail-toggle]").first().click();
      }
      await page.locator(`.underline-panels-tab[data-label="History"]`).filter({ visible: true }).first().click();
      await page.waitForSelector(".ms-history-open button", { timeout: 10000 });
      await sleep(400);
      const total = await versionCount();

      // The rail timeline.
      const rail = await page.evaluate(() => {
        const panel = [...document.querySelectorAll(".underline-panels-panel:not([hidden])")].find((p) => p.getClientRects().length);
        const points = [...panel.querySelectorAll(".history-timeline .history-point")];
        const show = panel.querySelector(".ms-history-open button");
        return {
          points: points.length,
          clocks: points.map((p) => (p.querySelector(".history-point-clock") || {}).textContent),
          firstCurrent: !!(points[0] && points[0].classList.contains("is-current")),
          days: [...panel.querySelectorAll(".history-timeline .history-day-label")].map((d) => d.textContent),
          showText: show ? show.querySelector(".btn-label") ? show.querySelector(".btn-label").textContent.trim() : show.textContent.trim() : "",
          counter: show && show.querySelector(".counter-label") ? (/^\d+/.exec(show.querySelector(".counter-label").textContent.trim()) || [""])[0] : "",
          showName: show ? show.getAttribute("aria-label") : "",
          inlineDiffs: panel.querySelectorAll("details").length,
        };
      });
      check(rail.points === 5 && rail.firstCurrent && rail.clocks.every((c) => /^\d\d:\d\d$/.test(c)), `${tag}: the rail shows five points in time, Current first, each with a clock time ${JSON.stringify(rail.clocks)}`);
      check(rail.days[0] === "Today" && rail.inlineDiffs === 0, `${tag}: the rail groups points under day headings ${JSON.stringify(rail.days)}`);
      check(rail.showText === "Show diff" && rail.counter === String(total) && new RegExp(`^Show diff, ${total} versions$`).test(rail.showName || ""), `${tag}: Show diff carries the version count in a CounterLabel (${JSON.stringify({ text: rail.showText, counter: rail.counter, name: rail.showName, total })})`);
      await shot(`after-rail-${tag}`);

      // A rail point opens the Dialog on that version.
      const railPoint = page.locator(".underline-panels-panel:not([hidden]) .history-timeline .history-point").filter({ visible: true }).nth(2);
      const railPointId = await railPoint.getAttribute("data-version-id");
      await railPoint.click();
      await page.waitForSelector("dialog.history-dialog[open]", { timeout: 15000 });
      await waitDiff();
      let st = await dialogState();
      check(st && st.selectedId === railPointId && (!narrow || st.screen === "diff"), `${tag}: a rail point opens the Dialog with that version selected (${JSON.stringify({ want: railPointId, got: st && st.selectedId, screen: st && st.screen })})`);
      check(st && st.firstCurrent && /^\d\d:\d\d$/.test(st.firstClock) && st.days[0] === "Today", `${tag}: the Dialog list starts with Current under Today (${JSON.stringify(st && { days: st.days.slice(0, 4), clock: st.firstClock })})`);
      await shot(`after-dialog-${tag}`);
      if (narrow) {
        await page.locator("dialog.history-dialog[open] .history-back").click();
        await sleep(300);
        st = await dialogState();
        check(st.screen === "list", `${tag}: Back returns to the version list (${st.screen})`);
      }

      // Every version after scrolling to the end of the list.
      for (let i = 0; i < 12; i++) {
        const done = await page.evaluate((want) => {
          const d = document.querySelector("dialog.history-dialog[open]");
          const pane = d.querySelector(".history-dialog-list");
          pane.scrollTop = pane.scrollHeight;
          return d.querySelectorAll(".history-listbox [role=option]").length >= want;
        }, total + 1);
        if (done) break;
        await sleep(300);
      }
      st = await dialogState();
      check(st.options === total + 1, `${tag}: after scrolling the Dialog lists all ${total} versions and Current (${st.options})`);
      check(st.days.length >= 4 && st.days.includes("Yesterday") && st.days.some((d) => /^[A-Z][a-z]+day$/.test(d)) && st.days.some((d) => /^[A-Z][a-z]{2} \d+/.test(d)), `${tag}: the Dialog groups versions by day ${JSON.stringify(st.days)}`);
      await shot(`after-list-end-${tag}`);

      // The oldest version and both compare modes.
      await page.locator("dialog.history-dialog[open] .history-listbox [role=option]").last().click();
      await waitDiff();
      st = await dialogState();
      const oldest = H.versions.length ? H.versions[H.versions.length - 1] : null;
      const oldestTitle = oldest ? await page.evaluate((t) => {
        const d = new Date(t);
        const W = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];
        const M = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
        return `${W[d.getDay()]}, ${M[d.getMonth()]} ${d.getDate()}, ${d.getFullYear()}`;
      }, oldest.createdAt) : "";
      check(st.selected === total && st.title.includes(oldestTitle) && st.diff !== "error" && st.diff !== "loading" && (!narrow || st.screen === "diff"), `${tag}: the oldest version shows its diff (${JSON.stringify({ selected: st.selected, title: st.title, want: oldestTitle, diff: st.diff, screen: st.screen })})`);
      check(/first version|version before it/.test(st.caption) && /lines?$|No line changes/.test(st.summary), `${tag}: Changes in this version is the default, with a line summary (${JSON.stringify({ caption: st.caption, summary: st.summary })})`);
      check(st.meta && !/null|undefined/.test(st.meta), `${tag}: the diff header names the author without stray text (${JSON.stringify(st.meta)})`);
      await shot(`after-oldest-${tag}`);
      await page.locator("dialog.history-dialog[open] .segmented-control button", { hasText: "Compare with current" }).click();
      await waitDiff();
      st = await dialogState();
      // After the restore in the first run the oldest version is the current text, so its diff is empty.
      check(/to the current version/.test(st.caption) && (st.diff === "editor" || (restored && st.diff === "same")), `${tag}: Compare with current diffs the oldest version against the current text (${JSON.stringify({ caption: st.caption, diff: st.diff, restored })})`);
      await page.locator("dialog.history-dialog[open] .segmented-control button", { hasText: "Changes in this version" }).click();
      await waitDiff();
      st = await dialogState();
      check(/first version|version before it/.test(st.caption), `${tag}: Changes in this version returns (${st.caption})`);

      // Keyboard.
      if (narrow) {
        await page.locator("dialog.history-dialog[open] .history-back").click();
        await sleep(300);
      }
      await page.locator("dialog.history-dialog[open] .history-listbox [role=option][aria-selected='true']").focus();
      await page.keyboard.press("ArrowUp");
      await sleep(250);
      st = await dialogState();
      check(st.selected === total - 1 && st.focusedIndex === total - 1, `${tag}: Up selects and focuses the newer version (${JSON.stringify({ selected: st.selected, focused: st.focusedIndex })})`);
      await page.keyboard.press("Home");
      await sleep(250);
      st = await dialogState();
      check(st.selected === 0 && st.focusedIndex === 0, `${tag}: Home selects Current (${st.selected})`);
      await page.keyboard.press("End");
      await sleep(250);
      st = await dialogState();
      check(st.selected === total && st.focusedIndex === total, `${tag}: End selects the oldest version (${st.selected})`);
      if (narrow) {
        await page.keyboard.press("Enter");
        await sleep(300);
        st = await dialogState();
        check(st.screen === "diff", `${tag}: Enter opens the diff screen (${st.screen})`);
      }

      // Restore the oldest version once.
      if (!restored) {
        restored = true;
        const want = oldest.content;
        await page.locator("dialog.history-dialog[open] .dialog-footer button", { hasText: "Restore this version" }).click();
        await page.waitForFunction(() => !document.querySelector("dialog.history-dialog[open]"), null, { timeout: 10000 });
        // The restore runs after the Dialog closes, so poll the server until the text changes.
        let note = null;
        for (let i = 0; i < 30; i++) {
          note = await (await context.request.get(`${base}/api/notes/${H.note.id}`)).json();
          if (String(note.content || "").trim() === want.trim()) break;
          await sleep(500);
        }
        const after = await versionCount();
        check(String(note.content || "").trim() === want.trim() && after === total + 1, `${tag}: Restore makes the oldest version the current text and keeps the text before it as a version (${JSON.stringify({ same: String(note.content || "").trim() === want.trim(), before: total, after })})`);
        await sleep(800);
        const railAfter = await page.evaluate(() => {
          const panel = [...document.querySelectorAll(".underline-panels-panel:not([hidden])")].find((p) => p.getClientRects().length);
          const show = panel && panel.querySelector(".ms-history-open button");
          return show && show.querySelector(".counter-label") ? (/^\d+/.exec(show.querySelector(".counter-label").textContent.trim()) || [""])[0] : "";
        });
        check(railAfter === String(after), `${tag}: the rail count follows the restore (${railAfter})`);
        await shot(`after-restored-${tag}`);
      } else {
        await page.keyboard.press("Escape");
        await sleep(300);
      }
      if (narrow) {
        await page.keyboard.press("Escape").catch(() => {});
        await sleep(300);
      }
    }
  }
}
