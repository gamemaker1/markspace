// Tests for the browser-only settings module: the usage line, the preference
// ToggleSwitches and the storage limit radios. It runs on the fake DOM.
import { test, beforeEach } from "node:test";
import assert from "node:assert/strict";
import { installDOM } from "./fakedom.mjs";

installDOM();
const settings = await import("../../internal/web/static/js/preferences.js");
const ui = await import("../../internal/web/static/js/ui.js");

beforeEach(() => {
  installDOM();
});

test("usageText names the used space and the kept files", () => {
  assert.equal(
    settings.usageText({ used: 1048576, count: 3, pinnedCount: 1, budget: 104857600 }),
    `Using ${ui.formatBytes(1048576)} of ${ui.formatBytes(104857600)} in this browser, 1 file kept offline.`,
  );
  assert.match(settings.usageText({ used: 0, count: 0, pinnedCount: 2, budget: 1 }), /2 files kept offline\.$/);
});

test("a preference switch reads, toggles and stores its key", () => {
  const store = new Map([["markspace.editor.linkPreview", "1"]]);
  const storage = {
    getItem: (k) => (store.has(k) ? store.get(k) : null),
    setItem: (k, v) => store.set(k, v),
    removeItem: (k) => store.delete(k),
  };
  const wrap = document.createElement("span");
  wrap.className = "toggle-switch";
  const track = document.createElement("button");
  track.setAttribute("data-pref-switch", "markspace.editor.linkPreview");
  track.setAttribute("aria-pressed", "false");
  wrap.appendChild(track);
  document.body.appendChild(wrap);

  settings.wirePreferenceSwitches(document.body, storage);
  assert.equal(track.getAttribute("aria-pressed"), "true");
  assert.equal(wrap.getAttribute("data-checked"), "true");

  track.click();
  assert.equal(track.getAttribute("aria-pressed"), "false");
  assert.equal(store.has("markspace.editor.linkPreview"), false);

  track.click();
  assert.equal(store.get("markspace.editor.linkPreview"), "1");
});

test("a switch still toggles when storage throws", () => {
  const broken = { getItem() { throw new Error("off"); }, setItem() { throw new Error("off"); }, removeItem() { throw new Error("off"); } };
  const track = document.createElement("button");
  track.setAttribute("data-pref-switch", "k");
  document.body.appendChild(track);
  settings.wirePreferenceSwitches(document.body, broken);
  assert.equal(track.getAttribute("aria-pressed"), "false");
  track.click();
  assert.equal(track.getAttribute("aria-pressed"), "true");
});

test("checkBudget checks an offered size or adds the current one", () => {
  const group = document.createElement("div");
  for (const v of ["104857600", "262144000"]) {
    const label = document.createElement("label");
    const input = document.createElement("input");
    input.type = "radio";
    input.name = "budget";
    input.value = v;
    label.appendChild(input);
    group.appendChild(label);
  }
  assert.equal(settings.checkBudget(group, "262144000").value, "262144000");
  const added = settings.checkBudget(group, "777");
  assert.equal(added.value, "777");
  assert.equal(added.checked, true);
  assert.equal(group.querySelectorAll('input[type="radio"]').length, 3);
});
