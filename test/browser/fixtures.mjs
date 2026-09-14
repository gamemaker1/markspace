// Self-test for the audit. Serves the synthetic pages in fixtures/ and fails
// unless each page produces exactly the findings named in its
// <meta name="audit-expect"> tag. <meta name="audit-open"> names a
// selector to click before the audit, and <meta name="audit-passes">
// turns on the overlay dismissal pass ("overlays") and the click pass
// ("clicks").
// Usage: node test/browser/fixtures.mjs [fixture-name-filter]
import { createServer } from "node:http";
import { readFileSync, readdirSync, existsSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, extname } from "node:path";
import { fileURLToPath } from "node:url";
import { launch } from "./lib/pw.mjs";
import { attach, auditPage, clickPass, dismissalPass } from "./lib/audit.mjs";

const HERE = dirname(fileURLToPath(import.meta.url));
const FIX = join(HERE, "fixtures");
const spec = JSON.parse(readFileSync(join(HERE, "spec.json"), "utf8"));
const allow = JSON.parse(readFileSync(join(HERE, "allow.json"), "utf8")).entries;
const filter = process.argv[2] || "";
const meta = (html, name) => {
  const m = new RegExp(`<meta name="${name}" content="([^"]*)">`).exec(html);
  return m ? m[1] : "";
};

const server = createServer((req, res) => {
  const path = decodeURIComponent(new URL(req.url, "http://x").pathname);
  if (path === "/favicon.ico") {
    res.writeHead(204);
    res.end();
    return;
  }
  const file = join(FIX, path.replace(/^\/fixtures\//, ""));
  if (path.startsWith("/fixtures/") && file.startsWith(FIX) && existsSync(file)) {
    res.writeHead(200, { "content-type": { ".html": "text/html; charset=utf-8", ".css": "text/css" }[extname(file)] || "application/octet-stream" });
    res.end(readFileSync(file));
    return;
  }
  res.writeHead(404, { "content-type": "text/plain" });
  res.end("not found");
});
await new Promise((r) => server.listen(0, "127.0.0.1", r));
const base = `http://127.0.0.1:${server.address().port}`;

const shotDir = mkdtempSync(join(tmpdir(), "audit-fixtures-"));
const browser = await launch();
let failures = 0;
const files = readdirSync(FIX).filter((f) => f.endsWith(".html") && f.includes(filter)).sort();
for (const file of files) {
  const html = readFileSync(join(FIX, file), "utf8");
  const expected = meta(html, "audit-expect").split(",").map((s) => s.trim()).filter(Boolean).sort();
  const opener = meta(html, "audit-open");
  const passes = meta(html, "audit-passes").split(",").map((s) => s.trim()).filter(Boolean);
  const context = await browser.newContext({ viewport: { width: 1280, height: 720 }, colorScheme: "light" });
  const page = await context.newPage();
  const collector = attach(page, base);
  const open = async () => {
    await page.goto(`${base}/fixtures/${file}`, { waitUntil: "networkidle" });
    if (opener) {
      await page.click(opener);
      await page.waitForTimeout(250);
    }
  };
  await open();
  let { findings } = await auditPage(page, { spec, allow, phone: false, focus: opener ? false : 20, collector, shots: new Map(), shotDir, key: file });
  if (passes.includes("overlays")) {
    await open();
    findings = findings.concat(await dismissalPass(page, { spec, allow, reopen: open }));
  }
  if (passes.includes("clicks")) {
    await open();
    findings = findings.concat(await clickPass(page, { spec, allow, reopen: open }));
  }
  const got = findings.filter((f) => !f.allowed).map((f) => `${f.check}:${f.rule}`).sort();
  const ok = JSON.stringify(got) === JSON.stringify(expected);
  if (!ok) failures++;
  console.log(`${ok ? "PASS" : "FAIL"} ${file}: expected [${expected.join(", ")}] got [${got.join(", ")}]`);
  if (!ok || process.env.VERBOSE) {
    for (const f of findings) console.log(`     ${f.allowed ? "allowed " : ""}${f.check}:${f.rule} ${f.selector} "${f.text}" ${JSON.stringify(f.measured).slice(0, 300)}`);
  }
  await context.close();
}
await browser.close();
server.close();
console.log(`\nfixtures: ${files.length - failures} of ${files.length} fixtures passed. Screenshots in ${shotDir}`);
process.exit(failures ? 1 : 0);
