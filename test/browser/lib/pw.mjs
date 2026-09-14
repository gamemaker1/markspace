// Loads playwright-core from PLAYWRIGHT_CORE and launches the Chromium build
// that Playwright keeps in ~/.cache/ms-playwright.
import { createRequire } from "node:module";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
export const PLAYWRIGHT_VERSION = /^1\.63\./;
export const INSTALL_HINT = "npm install --prefix test/browser playwright-core@1.63.0";

/**
 * Finds the playwright-core package directory. The order is PLAYWRIGHT_CORE,
 * then test/browser/node_modules, then any ~/.npm/_npx/<hash>/node_modules
 * that holds playwright-core 1.63.x. PLAYWRIGHT_CORE may name a node_modules
 * directory or the package directory itself.
 */
export function playwrightDir() {
  const pkgOf = (dir) => (existsSync(join(dir, "playwright-core", "package.json")) ? join(dir, "playwright-core") : dir);
  const meta = (pkg) => {
    try {
      return JSON.parse(readFileSync(join(pkg, "package.json"), "utf8"));
    } catch {
      return null;
    }
  };
  const tried = [];
  const candidates = [];
  if (process.env.PLAYWRIGHT_CORE) candidates.push([process.env.PLAYWRIGHT_CORE, false]);
  candidates.push([join(HERE, "..", "node_modules"), false]);
  const npx = join(homedir(), ".npm", "_npx");
  if (existsSync(npx)) for (const d of readdirSync(npx).sort()) candidates.push([join(npx, d, "node_modules"), true]);
  for (const [dir, needVersion] of candidates) {
    const pkg = pkgOf(dir);
    const m = meta(pkg);
    tried.push(dir);
    if (!m || m.name !== "playwright-core") continue;
    if (needVersion && !PLAYWRIGHT_VERSION.test(m.version)) continue;
    return pkg;
  }
  throw new Error(`playwright-core not found. Run: ${INSTALL_HINT}\nSearched: ${tried.join(", ")}`);
}

/** Returns the playwright-core module. */
export function loadPlaywright() {
  const pkg = playwrightDir();
  const require = createRequire(join(pkg, "package.json"));
  return require(pkg);
}

/** Finds the newest chromium-NNNN build under the Playwright browser cache. */
export function chromiumPath() {
  const base = process.env.PLAYWRIGHT_BROWSERS_PATH || join(homedir(), ".cache/ms-playwright");
  if (!existsSync(base)) throw new Error(`no Playwright browser cache at ${base}`);
  const dirs = readdirSync(base)
    .filter((d) => /^chromium-\d+$/.test(d))
    .sort((a, b) => Number(a.split("-")[1]) - Number(b.split("-")[1]));
  for (const d of dirs.reverse()) {
    for (const sub of ["chrome-linux64/chrome", "chrome-linux/chrome", "chrome-mac/Chromium.app/Contents/MacOS/Chromium"]) {
      const p = join(base, d, sub);
      if (existsSync(p)) return p;
    }
  }
  throw new Error(`no chromium build in ${base}`);
}

/** Launches headless Chromium. */
export async function launch() {
  const { chromium } = loadPlaywright();
  // Chromium keeps a Unix socket in TMPDIR, and socket paths over 107 bytes abort the browser.
  const env = { ...process.env };
  if ((env.TMPDIR || "").length > 60) env.TMPDIR = "/tmp";
  return chromium.launch({ executablePath: chromiumPath(), headless: true, env, args: ["--disable-gpu", "--font-render-hinting=none"] });
}
