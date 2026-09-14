// Tests that rail panel titles do not repeat their tab label on screen: the
// panelShell heading names the panel for screen readers and panels.css hides
// it inside a tab panel, and the views.js fallback frame hides it as well.
import { test, beforeEach } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { register } from "node:module";
import { installDOM } from "../fakedom.mjs";

// Browser modules import each other as /static/js/...; map that to the files.
register("data:text/javascript," + encodeURIComponent(`
export async function resolve(specifier, context, next) {
  if (specifier.startsWith("/static/")) {
    return { url: new URL("../../../internal/web/static/" + specifier.slice(8), ${JSON.stringify(import.meta.url)}).href, shortCircuit: true };
  }
  return next(specifier, context);
}`));

installDOM();
const panels = await import("../../../internal/web/static/js/panels.js");
const views = await import("../../../internal/web/static/js/views.js");

beforeEach(() => {
  installDOM();
});

test("panelShell names the panel through its h2", () => {
  const shell = panels.panelShell({ title: "Comments", className: "ms-comments" });
  const title = shell.root.querySelector(".ms-panel-title");
  assert.equal(title.tagName.toLowerCase(), "h2");
  assert.equal(title.textContent, "Comments");
  assert.equal(shell.root.getAttribute("aria-labelledby"), title.id);
});

test("panels.css hides the panel title inside a rail tab panel but not in the drawer", () => {
  const css = readFileSync(new URL("../../../internal/web/static/css/panels.css", import.meta.url), "utf8");
  const rule = /\.underline-panels-panel \.ms-panel:not\(\.is-drawer\) > \.ms-panel-head > \.ms-panel-title \{([^}]*)\}/.exec(css);
  assert.ok(rule, "the tab panel title rule exists");
  assert.match(rule[1], /clip: rect\(0, 0, 0, 0\)/);
  assert.match(rule[1], /position: absolute/);
});

test("the fallback rail panel frame keeps its title for screen readers only", () => {
  const shell = views.railPanelShell({}, "Outline", "rail-outline");
  const title = shell.root.querySelector("h2");
  assert.equal(title.textContent, "Outline");
  assert.ok(title.classList.contains("sr-only"), "the fallback title is visually hidden");
  assert.equal(shell.root.getAttribute("aria-labelledby"), title.id);
  assert.equal(shell.root.getAttribute("aria-label"), null);
  assert.equal(shell.root.querySelector(".ms-panel-body"), shell.body);
});

test("railPanelShell uses panelShell when panels.js loaded", () => {
  const shell = views.railPanelShell(panels, "Info", "rail-info");
  assert.ok(shell.root.classList.contains("rail-info"));
  assert.ok(shell.root.querySelector(".ms-panel-head .ms-panel-title"));
});
