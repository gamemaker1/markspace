// Browser layout audit for Markspace.
//
// node test/browser/run.mjs --bin=<markspace binary> [--tmp=<dir>]
//   [--profiles=<regex>] [--themes=light,dark] [--states=<regex>] [--workers=4]
//   [--base=<url> --seed-file=<seed.json>] [--report-dir=<dir>]
//
// Starts the binary on a free port with fresh data (or uses --base), seeds it
// through the HTTP API, visits every state on every viewport and theme in 4
// browser contexts, writes report.json and report.md, and exits 1 on any
// finding or any state it could not reach.
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync, cpSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { loadPlaywright, launch } from "./lib/pw.mjs";
import { startStack, REPO } from "./lib/stack.mjs";
import { stopAll } from "./lib/procs.mjs";
import { seed, ADMIN } from "./lib/seed.mjs";
import { attach, auditPage, clickPass, dismissalPass } from "./lib/audit.mjs";
import { PROFILES, buildStates, plan } from "./lib/states.mjs";
import { buildReport, writeReport } from "./lib/report.mjs";

const HERE = dirname(fileURLToPath(import.meta.url));
// make layout exports LAYOUT_ARGS, so regexes with parentheses reach node without shell parsing. Options split on spaces.
const argv = process.argv.slice(2).concat((process.env.LAYOUT_ARGS || "").split(/\s+/).filter(Boolean));
const args = Object.fromEntries(
  argv.map((a) => {
    const m = /^--([^=]+)(?:=(.*))?$/.exec(a);
    return m ? [m[1], m[2] ?? "1"] : [a, "1"];
  })
);
if (args.merge) {
  // Builds one report from the raw.json files of several runs.
  const occurrences = [];
  const harness = [];
  const metas = [];
  for (const dir of args.merge.split(",")) {
    const raw = JSON.parse(readFileSync(join(resolve(dir), "raw.json"), "utf8"));
    occurrences.push(...raw.occurrences);
    harness.push(...raw.harness);
    metas.push(raw.meta);
  }
  const meta = {
    startedAt: metas[0].startedAt,
    durationSec: metas.reduce((n, m) => n + m.durationSec, 0),
    base: metas.map((m) => m.base).join(", "),
    tmp: metas.map((m) => m.tmp).join(", "),
    shotDir: metas.map((m) => m.shotDir).join(", "),
    audits: metas.reduce((n, m) => n + m.audits, 0),
    profiles: metas.flatMap((m) => m.profiles),
    coverage: metas.flatMap((m) => m.coverage),
    states: [...new Set(metas.flatMap((m) => m.states))],
    mergedRuns: metas.length,
  };
  const report = buildReport({ occurrences, harness, repo: REPO, meta });
  const out = resolve(args["report-dir"] || HERE);
  mkdirSync(out, { recursive: true });
  writeReport(report, out);
  console.log(`${report.totals.findings} findings (${report.totals.occurrences} occurrences), ${report.totals.allowed} allowed, ${report.totals.harnessErrors} harness errors, ${meta.audits} audits`);
  for (const [name, c] of Object.entries(report.byCheck).sort()) console.log(`  check ${name}: ${c.findings} findings, ${c.occurrences} occurrences`);
  console.log(`Report: ${join(out, "report.md")}`);
  process.exit(report.totals.findings || report.totals.harnessErrors ? 1 : 0);
}
if (!args.bin && !args.base) {
  console.error("usage: node test/browser/run.mjs --bin=<markspace binary> [--tmp=dir] [--profiles=re] [--themes=light,dark] [--states=re]");
  process.exit(2);
}
const started = Date.now();
const tmp = resolve(args.tmp || mkdtempSync(join(tmpdir(), "markspace-layout-")));
mkdirSync(tmp, { recursive: true });
const shotDir = join(tmp, "shots");
const reportDir = resolve(args["report-dir"] || HERE);
const WORKERS = Number(args.workers || 4);
const spec = JSON.parse(readFileSync(join(HERE, "spec.json"), "utf8"));
const allow = JSON.parse(readFileSync(join(HERE, "allow.json"), "utf8")).entries;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const log = (...a) => console.log(`[${((Date.now() - started) / 1000).toFixed(0).padStart(4)}s]`, ...a);

function withTimeout(promise, ms, what) {
  let timer;
  return Promise.race([promise, new Promise((_, reject) => (timer = setTimeout(() => reject(new Error(`${what} timed out after ${ms} ms`)), ms)))]).finally(() => clearTimeout(timer));
}

let exitCode = 1;
try {
  const pw = loadPlaywright();
  let stack;
  if (args.base) stack = { base: args.base.replace(/\/$/, ""), dataDir: args.data || "", pluginBase: args.plugin || "" };
  else {
    stack = await startStack({ bin: resolve(args.bin), tmp });
    log(`server ${stack.base}, fake AI ${stack.fakeBase}, plugin ${stack.pluginBase}, data ${stack.dataDir}`);
  }
  const S = args["seed-file"] ? JSON.parse(readFileSync(args["seed-file"], "utf8")) : await seed(pw, stack.base, { tmp, dataDir: stack.dataDir, pluginBase: stack.pluginBase, workers: WORKERS });
  writeFileSync(join(tmp, "seed.json"), JSON.stringify(S, null, 1));
  for (const line of S.log || []) log(line);
  log("seeded");

  const browser = await launch();
  const auth = await browser.newContext();
  const login = await auth.request.post(stack.base + "/api/auth/login", { data: { email: ADMIN.email, password: ADMIN.password } });
  if (!login.ok()) throw new Error("admin login failed: " + login.status());
  const adminState = await auth.storageState();
  await auth.close();

  const stateRe = args.states ? new RegExp(args.states) : null;
  const states = buildStates(S).filter((s) => !stateRe || stateRe.test(s.id));
  const themes = (args.themes || "light,dark").split(",");
  const profileRe = args.profiles ? new RegExp(args.profiles) : null;
  const jobs = [];
  for (const profile of PROFILES.filter((p) => !profileRe || profileRe.test(p.name))) for (const theme of themes) jobs.push({ profile, theme });
  // Phones and the widest view take longest, so they start first.
  jobs.sort((a, b) => Number(!!b.profile.phone) - Number(!!a.profile.phone));

  const occurrences = [];
  const harness = [];
  const coverage = [];
  const timings = [];
  const shots = new Map();
  let audits = 0;
  let next = 0;

  async function newPage(context) {
    const page = await context.newPage();
    page.on("dialog", (d) => d.dismiss().catch(() => {}));
    page.collector = attach(page, stack.base);
    return page;
  }

  async function warm(page) {
    await page.goto(stack.base + "/", { waitUntil: "domcontentloaded", timeout: 30000 });
    await withTimeout(page.evaluate(async () => { await navigator.serviceWorker.ready; }), 30000, "service worker");
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.waitForFunction(() => window.markspaceApp && window.markspaceApp.spaces && window.markspaceApp.spaces.length > 0, null, { timeout: 30000 });
    await sleep(800);
    page.collector.drain();
  }

  // The click pass and the overlay dismissal pass run in the light theme on these profiles.
  const CLICK_PROFILES = (args["click-profiles"] || "1440x900,1024x768,390x844").split(",");
  const DISMISS_PROFILES = (args["dismiss-profiles"] || "1440x900,1024x768,390x844,844x390").split(",");

  let lastCounts = {};

  async function runJob({ profile, theme }, w) {
    const planned = plan(states, profile, theme);
    const seenOverlays = new Set();
    const cov = { viewport: profile.name, theme, states: 0, checked: 0 };
    const t0 = Date.now();
    for (const kind of ["app", "fallback", "anon"]) {
      const list = planned.filter((s) => s.kind === kind);
      if (!list.length) continue;
      const context = await browser.newContext({
        viewport: profile.viewport,
        deviceScaleFactor: profile.phone || profile.zoom ? 2 : 1,
        isMobile: !!profile.phone,
        hasTouch: !!profile.phone,
        colorScheme: theme,
        reducedMotion: "reduce",
        serviceWorkers: kind === "app" ? "allow" : "block",
        storageState: kind === "anon" ? undefined : adminState,
      });
      await context.addInitScript((t) => {
        try {
          localStorage.setItem("markspace.theme", t);
          localStorage.setItem("markspace.editor.focus", "0");
        } catch {
          /* storage off */
        }
      }, theme);
      let page = await newPage(context);
      try {
        if (kind === "app") await warm(page);
      } catch (e) {
        harness.push({ state: "service-worker-warmup", viewport: profile.name, theme, error: String(e.message).split("\n")[0] });
      }
      for (const state of list) {
        const ts = Date.now();
        const h = { page, context, base: stack.base, S, profile, theme, narrow: profile.viewport.width < 768, sandbox: S.sandboxes[w % S.sandboxes.length], worker: w };
        page.collector.drain();
        try {
          await withTimeout(state.run(h), 60000, `${state.id} setup`);
          if (state.after) await state.after(h);
        } catch (e) {
          harness.push({ state: state.id, viewport: profile.name, theme, error: String(e.message).split("\n").slice(0, 2).join(" ") });
          if (args.verbose) log(`w${w + 1} ${profile.name} ${theme} ${state.id} HARNESS ${String(e.message).split("\n")[0].slice(0, 160)}`);
          const file = join(shotDir, `harness-${state.id}-${profile.name}-${theme}.png`);
          mkdirSync(shotDir, { recursive: true });
          await page.screenshot({ path: file }).catch(() => {});
          if (state.teardown) await withTimeout(Promise.resolve().then(() => state.teardown(h)), 10000, "teardown").catch(() => {});
          if (page.isClosed()) page = await newPage(context);
          else await page.keyboard.press("Escape").catch(() => {});
          await context.setOffline(false).catch(() => {});
          continue;
        }
        if (args["state-shots"]) {
          const dir = join(tmp, "state-shots");
          mkdirSync(dir, { recursive: true });
          await page.screenshot({ path: join(dir, `${profile.name}-${theme}-${state.id}.png`) }).catch(() => {});
        }
        const opts = { spec, allow, phone: !!profile.phone, focus: state.focus ? 30 : false, collector: page.collector, shots, shotDir, key: "", expectConsole: state.expectConsole || [] };
        const record = (res, viewport) => {
          audits++;
          cov.states++;
          cov.checked += res.stats.checked || 0;
          lastCounts = { ...(res.stats.counts || {}) };
          for (const finding of res.findings) occurrences.push({ finding, state: state.id, viewport, theme });
        };
        try {
          record(await withTimeout(auditPage(page, opts), 120000, `${state.id} audit`), profile.name);
          if (state.editor && profile.keyboard) {
            const vp = profile.viewport;
            const kb = { width: vp.width, height: vp.height - profile.keyboard };
            // The focus walk can end on a skip link, which shows while focused. Focus returns to the editor as a person's would.
            await page.evaluate(() => {
              const cm = document.querySelector(".cm-content");
              if (document.activeElement && document.activeElement.matches(".skip-link")) document.activeElement.blur();
              if (cm) cm.focus();
            }).catch(() => {});
            await page.setViewportSize(kb);
            await sleep(500);
            record(await withTimeout(auditPage(page, { ...opts, focus: false }), 120000, `${state.id} keyboard audit`), `${kb.width}x${kb.height} keyboard`);
            await page.setViewportSize(vp);
          }
        } catch (e) {
          harness.push({ state: state.id, viewport: profile.name, theme, error: "audit: " + String(e.message).split("\n")[0] });
          if (page.isClosed()) page = await newPage(context);
        }
        const reopen = async () => {
          await withTimeout(state.run(h), 60000, `${state.id} reopen`);
          if (state.after) await state.after(h);
        };
        const extra = (list) => {
          if (list.tested !== undefined) lastCounts.clicked = list.tested;
          for (const finding of list) occurrences.push({ finding, state: state.id, viewport: profile.name, theme });
        };
        if (theme === "light" && DISMISS_PROFILES.includes(profile.name) && state.group !== "page" && args.dismiss !== "0") {
          try {
            extra(await withTimeout(dismissalPass(page, { spec, allow, reopen, narrow: h.narrow, seen: seenOverlays }), 120000, `${state.id} dismissal pass`));
          } catch (e) {
            harness.push({ state: state.id + " dismissal pass", viewport: profile.name, theme, error: String(e.message).split("\n")[0] });
            if (page.isClosed()) page = await newPage(context);
          }
        }
        if (theme === "light" && state.clicks && CLICK_PROFILES.includes(profile.name) && args.clicks !== "0") {
          try {
            await reopen();
            extra(await withTimeout(clickPass(page, { spec, allow, reopen, touch: !!profile.phone }), spec.clicks.budgetMs + 90000, `${state.id} click pass`));
          } catch (e) {
            harness.push({ state: state.id + " click pass", viewport: profile.name, theme, error: String(e.message).split("\n")[0] });
            if (page.isClosed()) page = await newPage(context);
          }
        }
        if (state.teardown) {
          try {
            await withTimeout(Promise.resolve().then(() => state.teardown(h)), 15000, `${state.id} teardown`);
          } catch (e) {
            harness.push({ state: state.id + " teardown", viewport: profile.name, theme, error: String(e.message).split("\n")[0] });
          }
        }
        timings.push({ state: state.id, viewport: profile.name, theme, ms: Date.now() - ts });
        if (args.verbose) log(`w${w + 1} ${profile.name} ${theme} ${state.id} ${Date.now() - ts} ms ${JSON.stringify(lastCounts)}`);
      }
      await context.close().catch(() => {});
    }
    coverage.push(cov);
    log(`worker ${w + 1} done ${profile.name} ${theme}: ${cov.states} audits in ${((Date.now() - t0) / 1000).toFixed(0)} s`);
  }

  await Promise.all(
    Array.from({ length: WORKERS }, async (_, w) => {
      while (next < jobs.length) {
        const job = jobs[next++];
        try {
          await runJob(job, w);
        } catch (e) {
          harness.push({ state: "job", viewport: job.profile.name, theme: job.theme, error: String(e.message).split("\n")[0] });
        }
      }
    })
  );
  await browser.close();

  const report = buildReport({
    occurrences,
    harness,
    repo: REPO,
    meta: { startedAt: new Date(started).toISOString(), durationSec: Math.round((Date.now() - started) / 1000), base: stack.base, tmp, shotDir, audits, profiles: jobs.map((j) => `${j.profile.name} ${j.theme}`), coverage, states: states.map((s) => s.id) },
  });
  mkdirSync(reportDir, { recursive: true });
  writeReport(report, tmp);
  writeReport(report, reportDir);
  writeFileSync(join(tmp, "timings.json"), JSON.stringify(timings));
  writeFileSync(join(tmp, "raw.json"), JSON.stringify({ occurrences, harness, meta: report.meta }));
  // --merge reads raw.json from each report directory of a split run.
  writeFileSync(join(reportDir, "raw.json"), JSON.stringify({ occurrences, harness, meta: report.meta }));
  if (args.copy) cpSync(tmp, resolve(args.copy), { recursive: true, filter: (p) => !/\/(data|plugin-data|media)(\/|$)/.test(p) });

  console.log("");
  log(`${report.totals.findings} findings (${report.totals.occurrences} occurrences), ${report.totals.allowed} allowed, ${report.totals.harnessErrors} harness errors, ${audits} audits`);
  for (const [name, c] of Object.entries(report.byCheck).sort()) console.log(`  check ${name}: ${c.findings} findings, ${c.occurrences} occurrences`);
  console.log(`\nReport: ${join(reportDir, "report.md")}\n        ${join(reportDir, "report.json")}\nCopy and screenshots: ${tmp}`);
  exitCode = report.totals.findings || report.totals.harnessErrors ? 1 : 0;
} catch (e) {
  console.error("audit failed:", e.stack || e);
  exitCode = 2;
} finally {
  await stopAll();
}
process.exit(exitCode);
