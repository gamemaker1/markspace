// Groups findings by check and by owner file, ranks them by severity and
// writes report.json and report.md.
import { readFileSync, readdirSync, statSync, writeFileSync } from "node:fs";
import { join, relative } from "node:path";

export const CHECKS = {
  0: "Audit harness",
  1: "Duplicate titles and actions",
  2: "Reachability",
  3: "Alignment",
  4: "Spacing against the spec",
  5: "Overlap and clipping",
  6: "Page level",
  7: "Colour",
  8: "Consistency",
  9: "Overlay dismissal",
};

/* Base severity per rule, 0 to 100. Breadth adds up to 15. */
const SEVERITY = {
  "failed-static-asset": 95,
  "page-error": 90,
  "console-error": 80,
  "state-setup-failed": 60,
  "cannot-scroll-into-view": 88,
  "clipped-by-overflow": 84,
  "horizontal-page-scroll": 82,
  "focus-ring-missing": 78,
  "focus-obscured": 74,
  "focus-on-invisible-element": 70,
  "covered-by-sticky": 72,
  covered: 66,
  "interactive-overlap": 64,
  "duplicate-title": 62,
  "text-clipped": 58,
  "contrast-below-aa": 56,
  "tap-target-under-44": 52,
  "yellow-or-amber": 50,
  "banner-count": 48,
  "focus-ring-clipped": 46,
  "single-line-centres": 42,
  "icon-first-line": 40,
  "toolbar-centres": 38,
  "overlay-text-inset-left": 44,
  "overlay-text-inset-top": 40,
  padding: 34,
  "ellipsis-without-full-text": 36,
  "probe-error": 60,
  "dead-click": 92,
  "word-broken": 56,
  "mixed-leading-visuals": 42,
  "no-click-effect": 80,
  "duplicate-action": 60,
  "control-contrast": 68,
  "old-token": 30,
  "unexplained-indent": 46,
  "metadata-prominence": 44,
  "inconsistent-spacing": 36,
  "overlay-escape": 76,
  "overlay-close-control": 62,
  "overlay-focus-return": 70,
};

function walk(dir, exts, out = []) {
  for (const name of readdirSync(dir)) {
    if (name === "node_modules" || name.startsWith(".")) continue;
    const p = join(dir, name);
    const st = statSync(p);
    if (st.isDirectory()) walk(p, exts, out);
    else if (exts.some((e) => name.endsWith(e))) out.push(p);
  }
  return out;
}

/** Maps class names to the CSS files that style them and the JS or template files that emit them. */
export function ownerIndex(repo) {
  const css = new Map();
  const markup = new Map();
  const add = (map, key, file) => {
    if (!map.has(key)) map.set(key, new Set());
    map.get(key).add(file);
  };
  const web = join(repo, "internal/web");
  for (const file of walk(join(web, "static"), [".css"])) {
    const rel = relative(repo, file);
    const text = readFileSync(file, "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
    for (const block of text.split("{")) {
      const sel = block.split("}").pop();
      for (const m of sel.matchAll(/\.([a-zA-Z][\w-]*)/g)) add(css, m[1], rel);
    }
  }
  const sources = [...walk(join(web, "static"), [".js"]).filter((f) => !/vendor/.test(f)), ...walk(join(web, "templates"), [".html"]), ...walk(join(repo, "internal/modules"), [".js", ".html", ".go"]).filter((f) => !f.endsWith("_test.go"))];
  for (const file of sources) {
    const rel = relative(repo, file);
    const text = readFileSync(file, "utf8");
    for (const m of text.matchAll(/["'`]([^"'`\n]{1,300})["'`]/g)) {
      for (const tok of m[1].split(/[\s.]+/)) if (/^[a-z][\w-]{2,}$/i.test(tok)) add(markup, tok, rel);
    }
  }
  return { css, markup };
}

/** Owner files for a finding: CSS that styles the nearest classed element and the files that emit it. */
export function ownerOf(f, index) {
  if (f.owner) return f.owner;
  if (f.check === 6 && f.rule === "failed-static-asset") return { primary: "internal/web/templates (asset reference)", css: [], markup: [] };
  if (f.check === 6 && (f.rule === "console-error" || f.rule === "page-error")) {
    const m = /\/(static\/[\w/.-]+\.js|modules\/[\w/.-]+\.js)/.exec(f.measured.message || "") || /\/(static\/[\w/.-]+\.js)/.exec(f.measured.url || "");
    if (m) return { primary: m[1].startsWith("static/") ? `internal/web/${m[1]}` : m[1], css: [], markup: [] };
    return { primary: "unknown (console)", css: [], markup: [] };
  }
  const classes = f.classes || [];
  // Generic component classes such as btn or action-list-item appear in many files and name no owner.
  // The nearest class that 3 or fewer JavaScript or template files emit decides the owner.
  const specific = (c) => {
    const m = [...(index.markup.get(c) || [])].filter((x) => !x.endsWith(".go"));
    return (index.css.has(c) || m.length) && m.length <= 3 && !/^(btn|octicon|action-list|menu-item|text-input|form-control|label|token|avatar|box|data-table)/.test(c);
  };
  for (let depth = 0; depth < classes.length; depth++) {
    const cls = classes[depth].filter(specific);
    if (!cls.length) continue;
    const cssFiles = new Set();
    const markupFiles = new Set();
    for (const c of cls) {
      for (const x of index.css.get(c) || []) cssFiles.add(x);
      for (const x of index.markup.get(c) || []) markupFiles.add(x);
    }
    const layout = [3, 4, 5, 7].includes(f.check);
    const cssList = [...cssFiles];
    const markupList = [...markupFiles].filter((x) => !x.endsWith(".go")).concat([...markupFiles].filter((x) => x.endsWith(".go")));
    const primary = (layout ? cssList[0] || markupList[0] : markupList[0] || cssList[0]) || "unknown";
    return { primary, css: cssList.slice(0, 4), markup: markupList.slice(0, 4), viaClass: cls.join(" "), depth };
  }
  // No specific class: fall back to the CSS file of the nearest styled class.
  for (let depth = 0; depth < classes.length; depth++) {
    const c = classes[depth].find((x) => index.css.has(x));
    if (c) return { primary: [...index.css.get(c)][0], css: [...index.css.get(c)].slice(0, 4), markup: [...(index.markup.get(c) || [])].slice(0, 4), viaClass: c, depth };
  }
  return { primary: "unknown", css: [], markup: [] };
}

/**
 * Builds the report from raw occurrences.
 * @param {object[]} occurrences each {finding, state, profile, theme, viewport}
 */
export function buildReport({ occurrences, harness, meta, repo }) {
  const index = ownerIndex(repo);
  const groups = new Map();
  const allowed = new Map();
  for (const o of occurrences) {
    const f = o.finding;
    const key = f.check === 6 && /console|page-error/.test(f.rule) ? `${f.check}|${f.rule}|${f.text.slice(0, 120)}` : `${f.check}|${f.rule}|${f.selector}`;
    const target = f.allowed ? allowed : groups;
    if (!target.has(key)) {
      target.set(key, { id: "", check: f.check, checkName: CHECKS[f.check], rule: f.rule, selector: f.selector, text: f.text, allowedReason: f.allowed || undefined, owner: ownerOf(f, index), specSelector: f.specSelector, occurrences: [], screenshot: null });
    }
    const g = target.get(key);
    g.occurrences.push({ state: o.state, viewport: o.viewport, theme: o.theme, measured: f.measured, rect: f.rect, screenshot: f.screenshot || null });
    if (!g.screenshot && f.screenshot) g.screenshot = f.screenshot;
  }
  const findings = [...groups.values()];
  for (const g of findings) {
    const states = new Set(g.occurrences.map((o) => o.state));
    const views = new Set(g.occurrences.map((o) => o.viewport + " " + o.theme));
    g.states = [...states];
    g.viewports = [...new Set(g.occurrences.map((o) => o.viewport))];
    g.themes = [...new Set(g.occurrences.map((o) => o.theme))];
    let sev = SEVERITY[g.rule] ?? 40;
    const m = g.occurrences[0].measured || {};
    if (g.rule === "cannot-scroll-into-view" || g.rule === "clipped-by-overflow") sev += Math.min(6, (m.hiddenPx || 0) / 8);
    if (g.rule === "contrast-below-aa") sev += Math.min(10, (m.required - m.ratio) * 4);
    if (/centres|first-line/.test(g.rule)) sev += Math.min(8, Math.max(...(m.problems || ["0"]).map((p) => Math.abs(parseFloat((/(-?[\d.]+)px/.exec(p) || [0, 0])[1])))));
    sev += Math.min(15, Math.log2(1 + states.size) * 3 + Math.log2(1 + views.size) * 2);
    g.severity = Math.round(sev * 10) / 10;
  }
  findings.sort((a, b) => b.severity - a.severity || a.check - b.check);
  findings.forEach((g, i) => (g.id = `F${String(i + 1).padStart(3, "0")}`));
  const byCheck = {};
  const byOwner = {};
  for (const g of findings) {
    const c = (byCheck[`${g.check} ${g.checkName}`] = byCheck[`${g.check} ${g.checkName}`] || { findings: 0, occurrences: 0, rules: {} });
    c.findings++;
    c.occurrences += g.occurrences.length;
    c.rules[g.rule] = (c.rules[g.rule] || 0) + 1;
    const o = (byOwner[g.owner.primary] = byOwner[g.owner.primary] || { findings: 0, occurrences: 0, checks: {} });
    o.findings++;
    o.occurrences += g.occurrences.length;
    o.checks[g.check] = (o.checks[g.check] || 0) + 1;
  }
  return {
    meta,
    totals: { findings: findings.length, occurrences: findings.reduce((n, g) => n + g.occurrences.length, 0), allowed: allowed.size, harnessErrors: harness.length },
    byCheck,
    byOwner: Object.fromEntries(Object.entries(byOwner).sort((a, b) => b[1].findings - a[1].findings)),
    findings,
    allowed: [...allowed.values()].map((g) => ({ check: g.check, rule: g.rule, selector: g.selector, reason: g.allowedReason, occurrences: g.occurrences.length })),
    harness,
  };
}

const esc = (s) => String(s ?? "").replace(/\|/g, "\\|").replace(/\n/g, " ");
const short = (m) => {
  const s = JSON.stringify(m);
  return s.length > 360 ? s.slice(0, 357) + "..." : s;
};

export function toMarkdown(r) {
  const L = [];
  L.push("# Markspace layout audit", "");
  L.push(`Run ${r.meta.startedAt}, ${r.meta.durationSec} s, ${r.meta.audits} page states audited across ${r.meta.profiles.length} viewport and theme profiles.`);
  L.push(`Server ${r.meta.base}. Screenshots in \`${r.meta.shotDir}\`.`, "");
  L.push(`**${r.totals.findings} findings** (${r.totals.occurrences} occurrences), ${r.totals.allowed} allowed by allow.json, ${r.totals.harnessErrors} harness errors.`, "");
  L.push("## Findings by check", "", "| Check | Findings | Occurrences | Rules |", "| --- | ---: | ---: | --- |");
  for (const [name, c] of Object.entries(r.byCheck).sort()) L.push(`| ${name} | ${c.findings} | ${c.occurrences} | ${Object.entries(c.rules).map(([k, v]) => `${k} ${v}`).join(", ")} |`);
  L.push("", "## Findings by owner file", "", "| Owner file | Findings | Occurrences | Checks |", "| --- | ---: | ---: | --- |");
  for (const [file, o] of Object.entries(r.byOwner)) L.push(`| \`${file}\` | ${o.findings} | ${o.occurrences} | ${Object.entries(o.checks).map(([k, v]) => `${k}: ${v}`).join(", ")} |`);
  L.push("", "## Most severe findings", "", "| ID | Severity | Check | Rule | Element | Owner | States | Viewports |", "| --- | ---: | --- | --- | --- | --- | ---: | --- |");
  for (const g of r.findings.slice(0, 20)) L.push(`| ${g.id} | ${g.severity} | ${g.check} | ${g.rule} | \`${esc(g.selector)}\` "${esc(g.text).slice(0, 40)}" | \`${g.owner.primary}\` | ${g.states.length} | ${g.viewports.join(", ")} |`);
  for (const [check, name] of Object.entries(CHECKS)) {
    const list = r.findings.filter((g) => String(g.check) === check);
    if (!list.length) continue;
    L.push("", `## ${check}. ${name}`, "");
    for (const g of list) {
      const o = g.occurrences[0];
      L.push(`### ${g.id} ${g.rule}: \`${esc(g.selector)}\``, "");
      L.push(`- Text: "${esc(g.text).slice(0, 80)}"`);
      L.push(`- Owner: \`${g.owner.primary}\`${g.owner.css && g.owner.css.length ? `, CSS ${g.owner.css.map((x) => "`" + x + "`").join(" ")}` : ""}${g.owner.markup && g.owner.markup.length ? `, markup ${g.owner.markup.map((x) => "`" + x + "`").join(" ")}` : ""}`);
      L.push(`- Severity ${g.severity}. ${g.occurrences.length} occurrences in ${g.states.length} states: ${g.states.slice(0, 8).join(", ")}${g.states.length > 8 ? ", ..." : ""}`);
      L.push(`- Viewports: ${g.viewports.join(", ")}. Themes: ${g.themes.join(", ")}`);
      L.push(`- Example (${o.state}, ${o.viewport}, ${o.theme}): \`${esc(short(o.measured))}\``);
      if (g.screenshot) L.push(`- Screenshot: \`${g.screenshot}\``);
      L.push("");
    }
  }
  if (r.allowed.length) {
    L.push("## Allowed", "", "| Check | Rule | Selector | Occurrences | Reason |", "| --- | --- | --- | ---: | --- |");
    for (const a of r.allowed) L.push(`| ${a.check} | ${a.rule} | \`${esc(a.selector)}\` | ${a.occurrences} | ${esc(a.reason)} |`);
  }
  if (r.harness.length) {
    L.push("", "## Harness errors", "", "States the audit could not reach. They count toward the exit status.", "");
    for (const h of r.harness) L.push(`- ${h.state} ${h.viewport} ${h.theme}: ${esc(h.error).slice(0, 300)}`);
  }
  L.push("", "## Coverage", "", "| Viewport | Theme | States | Interactive elements checked |", "| --- | --- | ---: | ---: |");
  for (const p of r.meta.coverage) L.push(`| ${p.viewport} | ${p.theme} | ${p.states} | ${p.checked} |`);
  return L.join("\n") + "\n";
}

export function writeReport(r, dir) {
  writeFileSync(join(dir, "report.json"), JSON.stringify(r, null, 1));
  writeFileSync(join(dir, "report.md"), toMarkdown(r));
}
