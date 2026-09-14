// Tests for icon() in ui.js and the generated octicons.js path data. They
// run on the small fake DOM in fakedom.mjs.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { installDOM } from "./fakedom.mjs";

installDOM();
const ui = await import("../../internal/web/static/js/ui.js");
const { PATHS, VERSION } = await import("../../internal/web/static/js/octicons.js");

const WEB = fileURLToPath(new URL("../../internal/web/", import.meta.url));

// Output of toSVG from @primer/octicons 19.36.0, called with the height and
// the options fill "currentColor" and focusable "false".
const GOLDEN = {
  "x-16": `<svg version="1.1" width="16" height="16" viewBox="0 0 16 16" class="octicon octicon-x" aria-hidden="true" data-component="Octicon" fill="currentColor" focusable="false"><path d="M3.72 3.72a.75.75 0 0 1 1.06 0L8 6.94l3.22-3.22a.749.749 0 0 1 1.275.326.749.749 0 0 1-.215.734L9.06 8l3.22 3.22a.749.749 0 0 1-.326 1.275.749.749 0 0 1-.734-.215L8 9.06l-3.22 3.22a.751.751 0 0 1-1.042-.018.751.751 0 0 1-.018-1.042L6.94 8 3.72 4.78a.75.75 0 0 1 0-1.06Z"></path></svg>`,
  "x-24": `<svg version="1.1" width="24" height="24" viewBox="0 0 24 24" class="octicon octicon-x" aria-hidden="true" data-component="Octicon" fill="currentColor" focusable="false"><path d="M5.72 5.72a.75.75 0 0 1 1.06 0L12 10.94l5.22-5.22a.749.749 0 0 1 1.275.326.749.749 0 0 1-.215.734L13.06 12l5.22 5.22a.749.749 0 0 1-.326 1.275.749.749 0 0 1-.734-.215L12 13.06l-5.22 5.22a.751.751 0 0 1-1.042-.018.751.751 0 0 1-.018-1.042L10.94 12 5.72 6.78a.75.75 0 0 1 0-1.06Z"></path></svg>`,
  "kebab-horizontal-16": `<svg version="1.1" width="16" height="16" viewBox="0 0 16 16" class="octicon octicon-kebab-horizontal" aria-hidden="true" data-component="Octicon" fill="currentColor" focusable="false"><path d="M8 9a1.5 1.5 0 1 0 0-3 1.5 1.5 0 0 0 0 3ZM1.5 9a1.5 1.5 0 1 0 0-3 1.5 1.5 0 0 0 0 3Zm13 0a1.5 1.5 0 1 0 0-3 1.5 1.5 0 0 0 0 3Z"></path></svg>`,
  "kebab-horizontal-24": `<svg version="1.1" width="24" height="24" viewBox="0 0 24 24" class="octicon octicon-kebab-horizontal" aria-hidden="true" data-component="Octicon" fill="currentColor" focusable="false"><path d="M20 14a2 2 0 1 1-.001-3.999A2 2 0 0 1 20 14ZM6 12a2 2 0 1 1-3.999.001A2 2 0 0 1 6 12Zm8 0a2 2 0 1 1-3.999.001A2 2 0 0 1 14 12Z"></path></svg>`,
  "file-directory-open-fill-16": `<svg version="1.1" width="16" height="16" viewBox="0 0 16 16" class="octicon octicon-file-directory-open-fill" aria-hidden="true" data-component="Octicon" fill="currentColor" focusable="false"><path d="M.513 1.513A1.75 1.75 0 0 1 1.75 1h3.5c.55 0 1.07.26 1.4.7l.9 1.2a.25.25 0 0 0 .2.1H13a1 1 0 0 1 1 1v.5H2.75a.75.75 0 0 0 0 1.5h11.978a1 1 0 0 1 .994 1.117L15 13.25A1.75 1.75 0 0 1 13.25 15H1.75A1.75 1.75 0 0 1 0 13.25V2.75c0-.464.184-.91.513-1.237Z"></path></svg>`,
  "file-directory-open-fill-24": `<svg version="1.1" width="24" height="24" viewBox="0 0 24 24" class="octicon octicon-file-directory-open-fill" aria-hidden="true" data-component="Octicon" fill="currentColor" focusable="false"><path d="M2 4.75C2 3.784 2.784 3 3.75 3h4.971c.58 0 1.12.286 1.447.765l1.404 2.063a.25.25 0 0 0 .207.11h6.224c.966 0 1.75.783 1.75 1.75v.117H5.408a.848.848 0 0 0 0 1.695h15.484a1 1 0 0 1 .995 1.102L21 19.25c-.106 1.05-.784 1.75-1.75 1.75H3.75A1.75 1.75 0 0 1 2 19.25V4.75Z"></path></svg>`,
};

// The old-name map from docs/primer.md 4.3, plus the template icon_device.
const EXPECTED_MAP = {
  sidebar: "sidebar-collapse", rail: "sidebar-expand", search: "search", plus: "plus", minus: "dash",
  more: "kebab-horizontal", star: "star", starFill: "star-fill", info: "info", alert: "alert", check: "check",
  close: "x", home: "home", clock: "clock", users: "people", user: "person", trash: "trash", settings: "gear",
  edit: "pencil", link: "link", copy: "copy", move: "file-moved", folder: "file-directory-fill", history: "history",
  comment: "comment", share: "share", file: "file", files: "paperclip", image: "image", video: "video",
  audio: "unmute", archive: "file-zip", duplicate: "duplicate", child: "file-added", rename: "pencil",
  signout: "sign-out", download: "download", upload: "upload", retry: "sync", back: "arrow-left",
  chevronDown: "chevron-down", chevronRight: "chevron-right", grid: "apps", list: "list-unordered",
  filter: "filter", eye: "eye", lock: "lock", key: "key", globe: "globe", sparkle: "sparkle", cloud: "cloud",
  cloudOff: "cloud-offline", sun: "sun", moon: "moon", tag: "tag", hash: "hash", device: "device-desktop",
};

/** Serialises a fake DOM element as markup. */
function serialize(node) {
  if (node.nodeType === 3) return node.data;
  const tag = node.tagName.toLowerCase();
  const attrs = [...node.attrs].map(([k, v]) => ` ${k}="${v}"`).join("");
  return `<${tag}${attrs}>${Array.from(node.childNodes, serialize).join("")}</${tag}>`;
}

/** Returns the sorted attributes and inner markup of an svg element. */
function canonical(markup) {
  const m = /^<svg((?:\s+[\w:-]+="[^"]*")*)>(.*)<\/svg>$/s.exec(markup);
  assert.ok(m, `not a single svg element: ${markup}`);
  return { attrs: [...m[1].matchAll(/[\w:-]+="[^"]*"/g)].map((a) => a[0]).sort(), inner: m[2] };
}

/** Reports whether icon() draws a known Octicon for a name. */
function resolves(name) {
  const svg = ui.icon(name);
  return svg.childNodes.length > 0 && /^octicon octicon-[a-z0-9-]+$/.test(svg.getAttribute("class"));
}

/** Lists files under a directory, recursively. */
function walk(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => (e.isDirectory() ? walk(join(dir, e.name)) : [join(dir, e.name)]));
}

test("icon draws a 16 px Octicon by default with Primer attributes", () => {
  const svg = ui.icon("search");
  assert.equal(svg.tagName, "SVG");
  assert.equal(svg.getAttribute("width"), "16");
  assert.equal(svg.getAttribute("height"), "16");
  assert.equal(svg.getAttribute("viewBox"), "0 0 16 16");
  assert.equal(svg.getAttribute("class"), "octicon octicon-search");
  assert.equal(svg.getAttribute("aria-hidden"), "true");
  assert.equal(svg.getAttribute("fill"), "currentColor");
  assert.equal(svg.getAttribute("focusable"), "false");
  assert.equal(svg.childNodes[0].tagName, "PATH");
});

test("icon matches Octicons toSVG output for 3 icons at 16 and 24 px apart from attribute order", () => {
  for (const [key, want] of Object.entries(GOLDEN)) {
    const [, name, size] = /^(.*)-(\d+)$/.exec(key);
    const got = canonical(serialize(ui.icon(name, Number(size))));
    const expected = canonical(want);
    assert.deepEqual(got.attrs, expected.attrs, key);
    assert.equal(got.inner, expected.inner, key);
  }
});

test("icon maps every Markspace name to its Octicon", () => {
  for (const [name, octicon] of Object.entries(EXPECTED_MAP)) {
    assert.equal(ui.icon(name).getAttribute("class"), `octicon octicon-${octicon}`, name);
    assert.equal(ui.icon(octicon).getAttribute("class"), `octicon octicon-${octicon}`, octicon);
  }
  for (const name of ui.ICON_NAMES) {
    assert.ok(resolves(name), name);
    assert.ok(ui.icon(name, 24).childNodes.length > 0, `${name} at 24 px`);
  }
});

test("the vendored set holds grabber, book and person at 16 and 24 px", () => {
  for (const name of ["grabber", "book", "person"]) {
    for (const size of [16, 24]) {
      assert.ok(PATHS[`${name}-${size}`], `${name}-${size} in octicons.js`);
      assert.equal(ui.icon(name, size).getAttribute("class"), `octicon octicon-${name}`, `${name} at ${size} px`);
      assert.ok(ui.icon(name, size).childNodes.length > 0, `${name} at ${size} px has paths`);
    }
    assert.ok(ui.ICON_NAMES.includes(name), `${name} in ICON_NAMES`);
  }
});

test("icon draws 24 px from size 24 up and 16 px below it", () => {
  for (const size of [12, 17, 18, 20, 22]) assert.equal(ui.icon("x", size).getAttribute("width"), "16", String(size));
  for (const size of [24, 26, 32, 40]) assert.equal(ui.icon("x", size).getAttribute("width"), "24", String(size));
  assert.equal(serialize(ui.icon("x", 26)), serialize(ui.icon("x", 24)));
});

test("icon mirrors the rail icon only", () => {
  assert.equal(ui.icon("rail").style.transform, "scaleX(-1)");
  assert.equal(ui.icon("sidebar").style.transform, undefined);
  assert.equal(ui.icon("sidebar-expand").style.transform, undefined);
});

test("icon warns and returns an empty octicon for an unknown name", (t) => {
  const warn = t.mock.method(console, "warn", () => {});
  for (const name of ["no-such-icon", "toString", "constructor", "x-16"]) {
    const svg = ui.icon(name);
    assert.equal(svg.getAttribute("class"), "octicon", name);
    assert.equal(svg.childNodes.length, 0, name);
  }
  assert.equal(warn.mock.callCount(), 4);
});

test("every icon name used in scripts and templates resolves to an Octicon", () => {
  const names = new Map();
  const note = (name, where) => {
    if (!names.has(name)) names.set(name, where);
  };
  const scripts = walk(join(WEB, "static", "js")).filter((f) => f.endsWith(".js") && !/[\\/](vendor-[^\\/]*|octicons)\.js$/.test(f));
  const patterns = [
    /\bicon\(\s*["']([^"']*)["']/g,
    /\bicon\([^()]*?\?\s*["']([^"']*)["']\s*:\s*["']([^"']*)["']/g,
    /\b(?:icon|glyph|iconName)\s*:\s*["']([A-Za-z][\w-]*)["']/g,
  ];
  for (const file of scripts) {
    const src = readFileSync(file, "utf8");
    for (const re of patterns) {
      for (const m of src.matchAll(re)) m.slice(1).filter(Boolean).forEach((name) => note(name, file));
    }
  }
  for (const file of walk(join(WEB, "templates")).filter((f) => f.endsWith(".html"))) {
    const src = readFileSync(file, "utf8");
    for (const m of src.matchAll(/\bicon_([A-Za-z]+)\b/g)) note(m[1], file);
    for (const m of src.matchAll(/\bocticon\s+"([^"]*)"/g)) {
      assert.ok(PATHS[`${m[1]}-16`], `${file}: octicon "${m[1]}" is not vendored`);
    }
  }
  assert.ok(names.size > 20, `found only ${names.size} names, so the patterns no longer match the code`);
  for (const [name, where] of names) assert.ok(resolves(name), `${where}: icon "${name}" has no Octicon`);
  for (const [mime, file] of [["image/png", ""], ["video/mp4", ""], ["audio/ogg", ""], ["application/zip", ""], ["", "a.tar"], ["text/plain", ""]]) {
    assert.ok(resolves(ui.fileIconName(mime, file)), `fileIconName(${mime}, ${file})`);
  }
});

test("octicons.js path data matches the vendored SVG files", () => {
  const dir = join(WEB, "static", "vendor", `octicons-${VERSION}`);
  const files = readdirSync(dir).filter((f) => f.endsWith(".svg")).sort();
  assert.deepEqual(files, Object.keys(PATHS).map((k) => `${k}.svg`).sort());
  const d = (s) => [...s.matchAll(/ d="([^"]*)"/g)].map((m) => m[1]);
  for (const file of files) {
    const fromFile = d(readFileSync(join(dir, file), "utf8"));
    assert.ok(fromFile.length > 0, file);
    assert.deepEqual(d(PATHS[file.slice(0, -4)]), fromFile, file);
  }
  const license = readFileSync(join(dir, "LICENSE"), "utf8");
  assert.match(license, /MIT License/);
  assert.match(license, /GitHub Inc\./);
});
