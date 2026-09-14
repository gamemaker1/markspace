// Runs the checks on one page state: the in-page probe, the keyboard focus
// walk, console errors, failed static assets, and a cropped screenshot of
// each new finding.
import { readFileSync, mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
export const PROBE = readFileSync(join(HERE, "probe.js"), "utf8");
const STATIC = /^\/(static|modules)\/|\.(css|js|mjs|woff2?|ttf|svg|webmanifest)$|^\/sw\.js$|^\/manifest\.webmanifest$/;

/**
 * Listens to console errors, page errors and failed same-origin static
 * requests. drain() returns the events since the last drain.
 */
export function attach(page, base) {
  const origin = new URL(base).origin;
  let events = [];
  page.on("console", (m) => {
    if (m.type() !== "error") return;
    const loc = m.location() || {};
    events.push({ kind: "console", text: m.text().slice(0, 400), url: loc.url || "" });
  });
  page.on("pageerror", (e) => events.push({ kind: "pageerror", text: String(e.stack || e.message || e).slice(0, 600) }));
  const isStatic = (req) => {
    const u = new URL(req.url());
    if (u.origin !== origin) return false;
    return ["stylesheet", "script", "font", "manifest"].includes(req.resourceType()) || STATIC.test(u.pathname);
  };
  page.on("response", (res) => {
    const req = res.request();
    if (res.status() >= 400 && isStatic(req)) events.push({ kind: "static", url: req.url(), status: res.status(), type: req.resourceType() });
  });
  page.on("requestfailed", (req) => {
    const err = (req.failure() || {}).errorText || "";
    if (/ERR_ABORTED|ERR_INTERNET_DISCONNECTED/.test(err)) return;
    if (isStatic(req)) events.push({ kind: "static", url: req.url(), status: err, type: req.resourceType() });
  });
  return {
    drain() {
      const out = events;
      events = [];
      return out;
    },
  };
}

/** Turns collected events into check 6 findings. */
export function eventFindings(events, { expectConsole = [] } = {}) {
  const out = [];
  const failed = new Set();
  for (const e of events) {
    if (e.kind !== "static") continue;
    const key = e.url;
    if (failed.has(key)) continue;
    failed.add(key);
    out.push({ check: 6, rule: "failed-static-asset", selector: new URL(e.url).pathname, text: `${e.status} ${e.type}`, classes: [], measured: { url: e.url, status: e.status, resourceType: e.type }, elId: -1 });
  }
  const seen = new Set();
  for (const e of events) {
    if (e.kind === "static") continue;
    // Chrome logs every failed resource. A failed static asset is already a finding.
    if (/Failed to load resource/.test(e.text) && (failed.has(e.url) || expectConsole.some((re) => re.test(e.url || e.text)))) continue;
    if (expectConsole.some((re) => re.test(e.text))) continue;
    const key = e.kind + e.text.slice(0, 120);
    if (seen.has(key)) continue;
    seen.add(key);
    out.push({ check: 6, rule: e.kind === "pageerror" ? "page-error" : "console-error", selector: e.url ? new URL(e.url).pathname : "console", text: e.text.slice(0, 200), classes: [], measured: { message: e.text, url: e.url || "" }, elId: -1 });
  }
  return out;
}

/** Presses Tab up to max times and records where focus lands. */
export async function focusWalk(page, { max = 40 } = {}) {
  const list = [];
  let first = null;
  let last = null;
  let repeats = 0;
  for (let i = 0; i < max; i++) {
    await page.keyboard.press("Tab");
    const info = await page.evaluate(() => window.__audit.focusInfo()).catch(() => ({ none: true }));
    if (info.none) {
      if (++repeats > 3) break;
      continue;
    }
    if (last && info.id === last.id) {
      if (++repeats > 2) break;
      continue;
    }
    repeats = 0;
    if (first && info.id === first.id) break;
    if (!first) first = info;
    last = info;
    list.push(info);
  }
  return list;
}

/**
 * Audits the current page state.
 * @returns {Promise<{findings: object[], stats: object}>}
 */
export async function auditPage(page, opts) {
  const { spec, allow, phone = false, focus = false, collector, shots, shotDir, key, expectConsole = [], checks } = opts;
  await page.evaluate(PROBE);
  const res = await page.evaluate((o) => window.__audit.run(o), { spec, allow, phone, checks });
  let findings = res.findings;
  if (focus) {
    const walk = await focusWalk(page, { max: typeof focus === "number" ? focus : 40 });
    const f = await page.evaluate(([list, allow]) => window.__audit.reportFocus(list, { allow }), [walk, allow]);
    findings = findings.concat(f);
    res.stats.focusSteps = walk.length;
  }
  if (collector) {
    for (const f of eventFindings(collector.drain(), { expectConsole })) {
      const a = allow.find((x) => (x.check === f.check || x.check === "*") && (!x.rule || x.rule === f.rule) && x.url && new RegExp(x.url).test(f.measured.url || ""));
      if (a) f.allowed = a.reason;
      findings.push(f);
    }
  }
  if (shotDir && shots) {
    mkdirSync(shotDir, { recursive: true });
    for (const f of findings) {
      if (f.allowed) continue;
      const k = `${f.check}|${f.rule}|${f.selector}|${key}`;
      if (shots.has(k)) {
        f.screenshot = shots.get(k);
        continue;
      }
      const file = join(shotDir, `${String(shots.size + 1).padStart(4, "0")}-c${f.check}-${f.rule}.png`);
      try {
        await shoot(page, f, file);
        shots.set(k, file);
        f.screenshot = file;
      } catch {
        shots.set(k, null);
      }
    }
  }
  return { findings, stats: res.stats };
}

/** Saves a screenshot cropped around the finding's element, marked with a red outline. */
async function shoot(page, f, file) {
  const vp = page.viewportSize();
  let rect = f.elId >= 0 ? await page.evaluate((id) => window.__audit.reveal(id), f.elId).catch(() => null) : null;
  if (!rect || rect.w < 1) {
    await page.screenshot({ path: file });
    return;
  }
  await page.evaluate((r) => {
    const m = document.createElement("div");
    m.id = "__audit-mark";
    m.style.cssText = `position:fixed;left:${r.x - 2}px;top:${r.y - 2}px;width:${r.w + 4}px;height:${r.h + 4}px;outline:2px solid #e5004f;pointer-events:none;z-index:2147483647`;
    document.documentElement.appendChild(m);
  }, rect);
  const margin = 32;
  const x = Math.max(0, Math.min(rect.x - margin, vp.width - 1));
  const y = Math.max(0, Math.min(rect.y - margin, vp.height - 1));
  const w = Math.max(80, Math.min(vp.width - x, rect.w + 2 * margin));
  const h = Math.max(48, Math.min(vp.height - y, rect.h + 2 * margin));
  await page.screenshot({ path: file, clip: { x, y, width: Math.min(w, vp.width - x), height: Math.min(h, vp.height - y) } });
  await page.evaluate(() => {
    const m = document.getElementById("__audit-mark");
    if (m) m.remove();
  });
}

const pause = (ms) => new Promise((r) => setTimeout(r, ms));

/**
 * Clicks every visible control of the current state at its centre and
 * reports no-click-effect when nothing follows: no navigation, DOM change,
 * focus move, value change, network request, popup or download. reopen()
 * restores the state after a click that navigates or leaves an overlay open.
 * Controls whose names match spec.clicks.skipNames change data and are not
 * clicked. Links with a real destination are not clicked.
 * @returns {Promise<object[]>} findings
 */
export async function clickPass(page, { spec, allow, reopen, touch = false }) {
  const cfg = spec.clicks;
  const findings = [];
  const started = Date.now();
  const background = new RegExp(cfg.background);
  let requests = 0;
  let popups = 0;
  let downloads = 0;
  const onRequest = (req) => {
    if (["eventsource", "websocket"].includes(req.resourceType())) return;
    if (!background.test(new URL(req.url()).pathname)) requests++;
  };
  const onPopup = (p) => {
    popups++;
    p.close().catch(() => {});
  };
  const onDownload = () => downloads++;
  page.on("request", onRequest);
  page.context().on("page", onPopup);
  page.on("download", onDownload);
  const collect = async () => {
    await page.evaluate(PROBE);
    return page.evaluate(([c, a]) => window.__audit.clickTargets(c, a), [cfg, allow]);
  };
  const tested = new Set();
  let reloads = 0;
  try {
    let targets = await collect();
    for (let i = 0; i < targets.length; i++) {
      if (tested.size >= cfg.maxTargets || Date.now() - started > cfg.budgetMs) break;
      const t = targets[i];
      if (tested.has(t.key)) continue;
      const armed = await page.evaluate((id) => window.__audit.armClick(id), t.id).catch(() => null);
      if (!armed) {
        if (++reloads > 12) break;
        await reopen();
        targets = await collect();
        i = -1;
        continue;
      }
      tested.add(t.key);
      // A covered centre is a dead-click or covered finding of the probe.
      if (!armed.hitOk) {
        await page.evaluate(() => window.__audit.readClick()).catch(() => {});
        continue;
      }
      const before = page.url();
      requests = 0;
      popups = 0;
      downloads = 0;
      if (touch) await page.touchscreen.tap(armed.x, armed.y);
      else await page.mouse.click(armed.x, armed.y);
      await pause(cfg.settleMs);
      const res = await page.evaluate(() => (window.__audit ? window.__audit.readClick() : null)).catch(() => null);
      const navigated = page.url() !== before || !res || res.url;
      const effect = navigated || popups || downloads || requests || res.mutations || res.focusMoved || res.fieldFocus || res.changed;
      if (!effect) {
        findings.push({ check: 2, rule: "no-click-effect", selector: t.selector, text: t.text, classes: t.classes, elId: -1, measured: { point: [armed.x, armed.y], waitedMs: cfg.settleMs, observed: "no navigation, DOM change, focus move, value change, request, popup or download", spec: cfg.ref }, allowed: t.allowed["no-click-effect"] || t.allowed["*"] });
      }
      if (navigated) {
        if (++reloads > 12) break;
        await reopen();
        targets = await collect();
        i = -1;
        continue;
      }
      if (res.mutations || res.focusMoved || res.changed) {
        await page.keyboard.press("Escape").catch(() => {});
        await pause(200);
        const open = await page.evaluate((c) => window.__audit.overlayInfo(c, []), spec.overlays).catch(() => null);
        if (open) {
          if (++reloads > 12) break;
          await reopen();
          targets = await collect();
          i = -1;
        }
      }
    }
  } finally {
    page.off("request", onRequest);
    page.context().off("page", onPopup);
    page.off("download", onDownload);
  }
  findings.tested = tested.size;
  return findings;
}

/**
 * Tests the top open overlay of the current state. Escape must close it. A
 * dialog, and a menu on narrow viewports, must have a visible Close control
 * that closes it. After either, focus must sit on a visible element outside
 * the overlay, never on body. reopen() opens the state again between the two
 * tests. seen holds overlay selectors already tested in this profile.
 * @returns {Promise<object[]>} findings
 */
export async function dismissalPass(page, { spec, allow, reopen, narrow = false, seen = null }) {
  const cfg = spec.overlays;
  const out = [];
  const info = async () => {
    await page.evaluate(PROBE);
    return page.evaluate(([c, a]) => window.__audit.overlayInfo(c, a), [cfg, allow]);
  };
  const state = (id) => page.evaluate((x) => (window.__audit ? window.__audit.overlayState(x) : null), id).catch(() => null);
  let o = await info();
  if (!o) return out;
  if (seen) {
    if (seen.has(o.selector)) return out;
    seen.add(o.selector);
  }
  const finding = (ov, rule, measured) => ({ check: 9, rule, selector: ov.selector, text: ov.text, classes: ov.classes, rect: ov.rect, elId: -1, measured: { kind: ov.kind, ...measured, spec: cfg.ref }, allowed: ov.allowed[rule] || ov.allowed["*"] });
  const focusProblem = (st) => st.focusOnBody || !st.focusVisible || st.focusInside;

  await page.keyboard.press("Escape");
  await pause(450);
  let st = await state(o.id);
  if (st && st.open) out.push(finding(o, "overlay-escape", { after: "Escape", stillOpen: true }));
  else if (st && focusProblem(st)) out.push(finding(o, "overlay-focus-return", { after: "Escape", focus: st.focus, focusVisible: st.focusVisible, focusInsideClosedOverlay: st.focusInside }));

  const needsClose = o.kind === "dialog" || o.kind === "alertdialog" || (o.kind === "menu" && narrow);
  if (!needsClose) return out;
  await reopen();
  o = await info();
  if (!o) return out;
  if (o.closeId < 0) {
    out.push(finding(o, "overlay-close-control", { problem: "no visible Close button" }));
    return out;
  }
  await page.evaluate((id) => window.__audit.clickElement(id), o.closeId);
  await pause(450);
  st = await state(o.id);
  if (st && st.open) out.push(finding(o, "overlay-close-control", { problem: "the Close button did not close the overlay", closeLabel: o.closeLabel }));
  else if (st && focusProblem(st)) out.push(finding(o, "overlay-focus-return", { after: `"${o.closeLabel}"`, focus: st.focus, focusVisible: st.focusVisible, focusInsideClosedOverlay: st.focusInside }));
  return out;
}
