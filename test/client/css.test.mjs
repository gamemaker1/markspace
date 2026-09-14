// Checks on the shared component stylesheets that do not need a browser.
// Button variants: every variant that draws a box has a boundary, a border
// or a fill, with at least 3:1 contrast against the surfaces a button sits
// on, in the light and the dark theme. Banner geometry: the icon, the first
// text line, the actions row and the dismiss button share one vertical
// centre. Token values come from the vendored Primer themes and theme.css.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const STATIC = fileURLToPath(new URL("../../internal/web/static/", import.meta.url));
const read = (path) => readFileSync(STATIC + path, "utf8").replace(/\/\*[\s\S]*?\*\//g, "");

/** Splits a stylesheet into top-level rules, skipping at-rule blocks. */
function topRules(css) {
  const out = [];
  let depth = 0;
  let start = 0;
  let selector = "";
  let skip = false;
  for (let i = 0; i < css.length; i++) {
    const ch = css[i];
    if (ch === "{") {
      if (depth === 0) {
        selector = css.slice(start, i).trim();
        skip = selector.startsWith("@");
        start = i + 1;
      }
      depth++;
    } else if (ch === "}") {
      depth--;
      if (depth === 0) {
        if (!skip) out.push({ selector, body: css.slice(start, i) });
        start = i + 1;
      }
    }
  }
  return out;
}

/** Collects custom properties of the rules whose selector matches. */
function tokensOf(css, match, into = {}) {
  for (const rule of topRules(css)) {
    if (!match(rule.selector)) continue;
    for (const m of rule.body.matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)) into[m[1]] = m[2].trim();
  }
  return into;
}

const primitives = tokensOf(read("vendor/primer/primitives-11.10.0.css"), () => true);
const themes = {
  light: tokensOf(read("theme.css"), (s) => s.includes('[data-light-theme="light"]'), tokensOf(read("vendor/primer/light-11.10.0.css"), () => true, { ...primitives })),
  dark: tokensOf(read("theme.css"), (s) => s.includes('[data-light-theme="dark"]'), tokensOf(read("vendor/primer/dark-11.10.0.css"), () => true, { ...primitives })),
};

/** Resolves var() references, taking the fallback when a token is unknown. */
function resolve(tokens, value, seen = 0) {
  assert.ok(seen < 20, `token loop in ${value}`);
  return value.replace(/var\((--[\w-]+)(?:\s*,\s*([^()]*))?\)/g, (all, name, fallback) => {
    if (tokens[name] !== undefined) return resolve(tokens, tokens[name], seen + 1);
    assert.ok(fallback !== undefined, `unknown token ${name}`);
    return resolve(tokens, fallback, seen + 1);
  });
}

/** Parses #rgb, #rgba, #rrggbb, #rrggbbaa and transparent into [r, g, b, a]. */
function rgba(value) {
  const v = value.trim().toLowerCase();
  if (v === "transparent") return [0, 0, 0, 0];
  const m = /^#([0-9a-f]{3,8})$/.exec(v);
  assert.ok(m, `not a hex colour: ${value}`);
  let hex = m[1];
  if (hex.length <= 4) hex = [...hex].map((c) => c + c).join("");
  const n = (i) => parseInt(hex.slice(i, i + 2), 16);
  return [n(0), n(2), n(4), hex.length === 8 ? n(6) / 255 : 1];
}

const over = (top, bottom) => {
  const a = top[3];
  return [0, 1, 2].map((i) => top[i] * a + bottom[i] * (1 - a)).concat(1);
};

function luminance([r, g, b]) {
  const lin = (c) => {
    const s = c / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
}

function ratio(a, b) {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

const SURFACES = ["--bgColor-default", "--bgColor-muted", "--overlay-bgColor"];

// Variants that draw a box. Invisible and link buttons draw only their text
// or icon by Primer's design, so their text contrast is the check for them.
const VARIANTS = {
  default: { bg: "--button-default-bgColor-rest", border: "--button-default-borderColor-rest" },
  primary: { bg: "--button-primary-bgColor-rest", border: "--button-primary-borderColor-rest" },
  danger: { bg: "--button-danger-bgColor-rest", border: "--button-danger-borderColor-rest" },
  inactive: { bg: "--button-inactive-bgColor", border: "--button-inactive-borderColor" },
};

/** The better of the fill and the border contrast against the surface. */
function boundary(tokens, variant, surfaceName) {
  const surface = over(rgba(resolve(tokens, `var(${surfaceName})`)), [255, 255, 255, 1]);
  const fill = over(rgba(resolve(tokens, `var(${variant.bg})`)), surface);
  const border = over(rgba(resolve(tokens, `var(${variant.border})`)), fill);
  return Math.max(ratio(fill, surface), ratio(border, surface));
}

test("every boxed button variant has a boundary of at least 3:1 against its surface in both themes", () => {
  const failures = [];
  for (const [theme, tokens] of Object.entries(themes)) {
    for (const [name, variant] of Object.entries(VARIANTS)) {
      for (const surface of SURFACES) {
        const r = boundary(tokens, variant, surface);
        if (r < 3) failures.push(`${theme} ${name} on ${surface}: ${r.toFixed(2)}:1`);
      }
    }
  }
  assert.deepEqual(failures, []);
});

test("button text keeps 4.5:1 against the button fill in both themes", () => {
  const failures = [];
  const pairs = [
    ["default", "--button-default-fgColor-rest", "--button-default-bgColor-rest"],
    ["primary", "--button-primary-fgColor-rest", "--button-primary-bgColor-rest"],
    ["danger", "--button-danger-fgColor-rest", "--button-danger-bgColor-rest"],
    ["invisible", "--button-invisible-fgColor-rest", "--bgColor-default"],
  ];
  for (const [theme, tokens] of Object.entries(themes)) {
    for (const [name, fg, bg] of pairs) {
      const base = over(rgba(resolve(tokens, "var(--bgColor-default)")), [255, 255, 255, 1]);
      const fill = over(rgba(resolve(tokens, `var(${bg})`)), base);
      const r = ratio(over(rgba(resolve(tokens, `var(${fg})`)), fill), fill);
      if (r < 4.5) failures.push(`${theme} ${name}: ${r.toFixed(2)}:1`);
    }
  }
  assert.deepEqual(failures, []);
});

/* ---------- Banner geometry ---------- */

const components = topRules(read("css/components.css"));

/** Returns the declarations of the last rules whose selector list holds selector exactly. */
function declarations(selector) {
  const out = {};
  for (const rule of components) {
    if (!rule.selector.split(",").map((s) => s.trim()).includes(selector)) continue;
    for (const m of rule.body.matchAll(/([\w-]+)\s*:\s*([^;]+);/g)) out[m[1]] = m[2].trim();
  }
  return out;
}

/** Evaluates a length in px: numbers, var() tokens and calc() with + - * /. */
function px(value) {
  const text = resolve(themes.light, value).replace(/calc/g, "").replace(/(\d)px/g, "$1").replace(/(\d)rem/g, "$1*16");
  assert.match(text, /^[\d.\s+\-*/()]+$/, `cannot evaluate ${value}`);
  return Function(`"use strict"; return (${text});`)();
}

/** Returns the start value of a shorthand, splitting on spaces outside parentheses. */
function startOf(value) {
  let depth = 0;
  for (let i = 0; i < value.length; i++) {
    if (value[i] === "(") depth++;
    else if (value[i] === ")") depth--;
    else if (/\s/.test(value[i]) && depth === 0) return px(value.slice(0, i));
  }
  return px(value);
}

test("banner icon, first text line, actions and dismiss button share one vertical centre", () => {
  const banner = declarations(".banner:has(> .banner-icon)");
  const pad = startOf(banner.padding);
  const iconCell = declarations(".banner-icon");
  const iconSvg = declarations(".banner-icon > svg");
  const iconCentre = pad + startOf(iconCell.padding) + startOf(iconSvg["margin-block"]) + 8;
  assert.equal(px(iconCell["line-height"]), 20, "the icon sits in a 20px line");

  const content = declarations(".banner-content");
  const title = declarations(".banner-title");
  const fontSize = px("var(--text-body-size-medium)");
  const textCentre = pad + startOf(content["margin-block"]) + (fontSize * Number(title["line-height"])) / 2;

  const actions = declarations(".banner-container > .banner-actions");
  const actionsCentre = pad + startOf(actions["margin-block"]) + px(actions["min-height"]) / 2;

  const dismiss = declarations(".banner-dismiss-cell");
  assert.equal(startOf(dismiss.padding), 8, "docs/primer.md 5.7 Dismiss padding 8px");
  const dismissCentre = pad + startOf(dismiss["margin-block"] || "0") + startOf(dismiss.padding) + px("var(--control-small-size)") / 2;

  for (const [name, centre] of Object.entries({ text: textCentre, actions: actionsCentre, dismiss: dismissCentre })) {
    assert.ok(Math.abs(centre - iconCentre) <= 1, `${name} centre ${centre}px, icon centre ${iconCentre}px`);
  }
});

test("the dismiss cell is no taller than the icon cell, so a one-line banner keeps its height", () => {
  const iconCell = declarations(".banner-icon");
  const dismiss = declarations(".banner-dismiss-cell");
  const iconHeight = 2 * startOf(iconCell.padding) + 20;
  const dismissHeight = 2 * startOf(dismiss.padding) + px("var(--control-small-size)") + 2 * startOf(dismiss["margin-block"] || "0");
  assert.ok(dismissHeight <= iconHeight, `dismiss cell ${dismissHeight}px, icon cell ${iconHeight}px`);
});
