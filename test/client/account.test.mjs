// Tests for the account menu: the theme SegmentedControl and its saved
// preference (docs/primer.md 2.3), and the recovered changes entry in the sync
// group with the Avatar badge (docs/primer.md 5.7). It runs on the fake DOM.
import { test, beforeEach } from "node:test";
import assert from "node:assert/strict";
import { installDOM, FakeEvent } from "./fakedom.mjs";

installDOM();
const shell = await import("../../internal/web/static/js/shell.js");
const ui = await import("../../internal/web/static/js/ui.js");

beforeEach(() => {
  installDOM();
});

function memoryStorage() {
  const data = new Map();
  return {
    getItem: (key) => (data.has(key) ? data.get(key) : null),
    setItem: (key, value) => data.set(key, String(value)),
    removeItem: (key) => data.delete(key),
    data,
  };
}

/** Returns the accessible name that aria-label or aria-labelledby gives. */
function nameOf(el) {
  const ids = (el.getAttribute("aria-labelledby") || "").split(/\s+/).filter(Boolean);
  if (ids.length) return ids.map((id) => document.getElementById(id).textContent).join(" ");
  return el.getAttribute("aria-label") || "";
}

function key(el, name) {
  el.dispatchEvent(new FakeEvent("keydown", { key: name, bubbles: true }));
}

test("the theme switch has light, dark and system icon segments with names", () => {
  const sw = shell.themeSwitch("dark", () => {});
  document.body.appendChild(sw.element);
  assert.equal(sw.control.getAttribute("role"), "group");
  assert.equal(sw.control.getAttribute("aria-label"), "Theme");
  assert.deepEqual(sw.buttons.map((b) => b.dataset.value), ["light", "dark", "system"]);
  assert.deepEqual(sw.buttons.map(nameOf), ["Light theme", "Dark theme", "Use system theme"]);
  assert.ok(sw.buttons.every((b) => b.getAttribute("role") === "menuitemradio" && b.getAttribute("tabindex") === "-1"));
  assert.ok(sw.buttons.every((b) => !b.querySelector(".segmented-control-text") && b.querySelector(".segmented-control-icon").childNodes.length === 1), "segments are icon-only");
  assert.deepEqual(sw.buttons.map((b) => b.getAttribute("aria-checked")), ["false", "true", "false"]);
  assert.ok(sw.buttons.every((b) => !b.hasAttribute("aria-current")), "menu segments use aria-checked");
  assert.deepEqual(shell.THEME_CHOICES.map((c) => c.icon), ["sun", "moon", "device-desktop"]);
});

test("choosing a segment saves the preference and sets data-color-mode", () => {
  const storage = memoryStorage();
  const root = document.documentElement;
  const sw = shell.themeSwitch(shell.themePreference(storage), (value) => shell.saveThemePreference(value, storage, root));
  document.body.appendChild(sw.element);
  assert.equal(sw.buttons[2].getAttribute("aria-checked"), "true", "no stored key means the system theme");

  sw.buttons[0].click();
  assert.equal(storage.getItem("markspace.theme"), "light");
  assert.equal(root.getAttribute("data-color-mode"), "light");
  assert.deepEqual(sw.buttons.map((b) => b.getAttribute("aria-checked")), ["true", "false", "false"]);

  sw.buttons[1].click();
  assert.equal(storage.getItem("markspace.theme"), "dark");
  assert.equal(root.getAttribute("data-color-mode"), "dark");

  sw.buttons[2].click();
  assert.equal(storage.data.has("markspace.theme"), false, "the system theme removes the key");
  assert.equal(root.getAttribute("data-color-mode"), "auto");
});

test("themePreference reads only light and dark and survives broken storage", () => {
  const storage = memoryStorage();
  assert.equal(shell.themePreference(storage), "system");
  storage.setItem("markspace.theme", "dark");
  assert.equal(shell.themePreference(storage), "dark");
  storage.setItem("markspace.theme", "sepia");
  assert.equal(shell.themePreference(storage), "system");
  const broken = { getItem() { throw new Error("off"); }, setItem() { throw new Error("off"); }, removeItem() { throw new Error("off"); } };
  assert.equal(shell.themePreference(broken), "system");
  assert.equal(shell.saveThemePreference("light", broken, document.documentElement), "light");
  assert.equal(document.documentElement.getAttribute("data-color-mode"), "light", "the theme applies without storage");
});

test("the menu arrow keys reach the theme segments, and a choice keeps the menu open", () => {
  const anchor = document.createElement("button");
  anchor.textContent = "Account";
  document.body.appendChild(anchor);
  const picked = [];
  const sw = shell.themeSwitch("system", (value) => picked.push(value));
  const menu = ui.openMenu(anchor, [
    { label: "Sync now", icon: "sync", onClick() {} },
    { separator: true },
    { custom: sw.element, focusables: sw.buttons },
    { separator: true },
    { label: "Settings", icon: "gear", href: "/settings/profile" },
  ], { label: "Account" });
  assert.equal(document.activeElement.textContent.includes("Sync now"), true);
  key(document.activeElement, "ArrowDown");
  assert.equal(document.activeElement, sw.buttons[0]);
  key(document.activeElement, "ArrowDown");
  assert.equal(document.activeElement, sw.buttons[1]);
  key(document.activeElement, "Enter");
  assert.deepEqual(picked, ["dark"]);
  assert.equal(sw.buttons[1].getAttribute("aria-checked"), "true");
  assert.ok(menu.element.isConnected, "the menu stays open after a theme choice");
  assert.equal(menu.element.querySelector(".action-list-custom").getAttribute("role"), "none");
});

test("recovered changes show in the sync group and on the badge only while some exist", () => {
  assert.equal(shell.recoveredMenuItem(0, () => {}), null);
  assert.equal(shell.recoveredMenuItem(undefined, () => {}), null);
  const opened = [];
  const item = shell.recoveredMenuItem(2, () => opened.push("dialog"));
  assert.equal(item.label, "Recovered changes");
  assert.equal(item.count, 2);

  const anchor = document.createElement("button");
  document.body.appendChild(anchor);
  const menu = ui.openMenu(anchor, [{ label: "Sync now", icon: "sync", onClick() {} }, item], { label: "Account" });
  const row = Array.from(menu.element.querySelectorAll("[role=menuitem]")).find((el) => el.textContent.includes("Recovered changes"));
  const counter = row.querySelector(".counter-label");
  assert.ok(counter, "the item has a CounterLabel");
  assert.match(counter.textContent, /2/);
  assert.equal(counter.closest("[aria-hidden=true]"), null, "the count is in the accessible name");
  row.click();
  assert.deepEqual(opened, ["dialog"]);

  assert.equal(shell.badgeState("synced", 0), "synced");
  for (const state of ["synced", "syncing", "pending"]) assert.equal(shell.badgeState(state, 1), "recovered", state);
  for (const state of ["offline", "error", "auth"]) assert.equal(shell.badgeState(state, 3), state, state + " keeps its own badge");
  assert.equal(shell.SYNC_BADGE_ICONS.recovered, "alert");

  const synced = { state: "synced", text: "Synced" };
  assert.equal(shell.accountLabel(synced, { pending: 0 }, 0), "Account, All changes synced");
  assert.equal(shell.accountLabel(synced, { pending: 0 }, 1), "Account, All changes synced, 1 recovered change");
  assert.equal(shell.accountLabel({ state: "pending", text: "" }, { pending: 3 }, 2), "Account, 3 not synced, 2 recovered changes");
});
