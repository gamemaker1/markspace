// Tests for the Primer components in ui.js: buttons, tooltips, ActionMenu,
// SelectPanel, Autocomplete, Dialog, ConfirmationDialog, AnchoredOverlay,
// messaging, undo rows, loading states, labels, form controls, edit in
// place, expanded rows, TreeView, SegmentedControl, ToggleSwitch, Details
// and the drop area. They run on the fake DOM in fakedom.mjs.
import { test, beforeEach } from "node:test";
import assert from "node:assert/strict";
import { installDOM, FakeEvent } from "./fakedom.mjs";

installDOM();
const ui = await import("../../internal/web/static/js/ui.js");
const { h } = ui;

const sleep = (ms = 0) => new Promise((resolve) => setTimeout(resolve, ms));
const click = (el) => el.dispatchEvent(new FakeEvent("click", { bubbles: true }));
const key = (el, name, mods = {}) => {
  const event = new FakeEvent("keydown", { bubbles: true, key: name, ...mods });
  el.dispatchEvent(event);
  return event;
};
const active = () => document.activeElement;
/** Compares nodes by identity. node:assert would inspect the whole fake DOM on failure. */
const same = (actual, expected, message = "") => {
  const name = (node) => (node && node.nodeType ? `<${String(node.tagName || "#node").toLowerCase()}${node.id ? "#" + node.id : ""}${node.getAttribute && node.getAttribute("data-id") ? "[data-id=" + node.getAttribute("data-id") + "]" : ""} ${String(node.textContent || "").slice(0, 30)}>` : String(node));
  assert.ok(actual === expected, `${message} got ${name(actual)}, want ${name(expected)}`);
};

beforeEach(() => {
  installDOM({ focusEvents: true });
});

/** Adds a focused trigger button to the page. */
function trigger(text = "Actions") {
  const el = h("button", { type: "button", text });
  document.body.appendChild(el);
  el.focus();
  return el;
}

/* ---------- Buttons, spinner and busy state ---------- */

test("button builds Primer classes, visuals, a counter and inactive state", () => {
  const el = ui.button({ label: "New page", variant: "primary", size: "large", leadingVisual: "plus", count: 3, inactive: true });
  assert.equal(el.tagName, "BUTTON");
  assert.equal(el.getAttribute("type"), "button");
  for (const cls of ["btn", "btn-primary", "btn-lg"]) assert.ok(el.classList.contains(cls), cls);
  assert.ok(el.querySelector(".btn-leading svg"));
  assert.equal(el.querySelector(".btn-label").textContent, "New page");
  assert.equal(el.querySelector(".counter-label").textContent, "3(3)");
  assert.equal(el.getAttribute("aria-disabled"), "true");
  const link = ui.button({ label: "Search", href: "/search", variant: "invisible", size: "small" });
  assert.equal(link.tagName, "A");
  assert.ok(link.classList.contains("btn-invisible") && link.classList.contains("btn-sm"));
});

test("iconButton names the button through a label tooltip, or describes it when it has a hint", () => {
  const plain = ui.iconButton({ icon: "kebab-horizontal", label: "More actions" });
  document.body.appendChild(plain);
  const tipId = plain.getAttribute("aria-labelledby");
  assert.ok(tipId);
  const tip = document.getElementById(tipId);
  assert.equal(tip.textContent, "More actions");
  assert.equal(tip.getAttribute("aria-hidden"), "true");
  assert.ok(plain.classList.contains("btn-icon") && plain.classList.contains("btn-invisible"));

  const hinted = ui.iconButton({ icon: "sidebar-collapse", label: "Toggle sidebar", keybindingHint: "Ctrl \\" });
  document.body.appendChild(hinted);
  assert.equal(hinted.getAttribute("aria-label"), "Toggle sidebar");
  const described = document.getElementById(hinted.getAttribute("aria-describedby"));
  assert.match(described.textContent, /Toggle sidebar/);
  assert.ok(described.querySelector("kbd.keybinding-hint"));
});

test("spinner is hidden from screen readers and carries a hidden label", async () => {
  const el = ui.spinner({ size: "medium", label: "Loading comments" });
  assert.equal(el.querySelector("svg").getAttribute("aria-hidden"), "true");
  assert.equal(el.querySelector("svg").getAttribute("width"), "32");
  assert.equal(el.querySelector(".sr-only").textContent, "Loading comments");
  const late = ui.spinner({ delay: 20 });
  assert.equal(late.hidden, true);
  await sleep(40);
  assert.equal(late.hidden, false);
});

test("setBusy keeps focus, sets aria-disabled, replaces the leading visual and blocks clicks", () => {
  let clicks = 0;
  const el = ui.button({ label: "Save", leadingVisual: "check", onClick: () => clicks++ });
  document.body.appendChild(el);
  el.focus();
  ui.setBusy(el, true, { message: "Saving profile" });
  same(active(),el);
  assert.equal(el.getAttribute("aria-disabled"), "true");
  assert.equal(el.hasAttribute("disabled"), false);
  assert.ok(el.querySelector(".btn-leading").classList.contains("is-replaced"));
  assert.ok(el.querySelector(".btn-spinner"));
  assert.equal(document.querySelector("[data-status-region]").textContent, "Saving profile");
  click(el);
  assert.equal(clicks, 0);
  ui.setBusy(el, false);
  assert.equal(el.hasAttribute("aria-disabled"), false);
  assert.equal(el.querySelector(".btn-spinner"), null);
  click(el);
  assert.equal(clicks, 1);

  const bare = h("button", { class: "btn" }, "Delete");
  document.body.appendChild(bare);
  ui.setBusy(bare, true);
  assert.ok(bare.classList.contains("is-busy-centered"));
  assert.equal(bare.querySelector(".btn-label").textContent, "Delete");
});

test("announce writes to one status region and repeats identical text as a change", () => {
  const region = ui.announce("Copied");
  assert.equal(region.getAttribute("role"), "status");
  assert.equal(region.getAttribute("aria-live"), "polite");
  assert.equal(region.textContent, "Copied");
  assert.equal(ui.announce("Copied"), region);
  assert.notEqual(region.textContent, "Copied");
  assert.equal(region.textContent.trim(), "Copied");
  assert.equal(document.querySelectorAll("[data-status-region]").length, 1);
});

/* ---------- Tooltip ---------- */

test("tooltip opens on keyboard focus, closes on blur and on Escape without moving focus", async () => {
  const el = h("button", { type: "button", "aria-label": "Copy link", title: "Copy link" });
  document.body.appendChild(el);
  const tip = ui.tooltip(el, "Copy link", { type: "description" });
  assert.equal(el.hasAttribute("title"), false, "title attributes go");
  assert.equal(el.getAttribute("aria-describedby"), tip.element.id);
  el.focus();
  assert.equal(tip.element.getAttribute("data-open"), "true");
  key(el, "Escape");
  assert.equal(tip.element.hasAttribute("data-open"), false);
  same(active(),el);
  el.dispatchEvent(new FakeEvent("pointerenter", { pointerType: "mouse" }));
  await sleep(80);
  assert.equal(tip.element.getAttribute("data-open"), "true");
  el.blur();
  assert.equal(tip.element.hasAttribute("data-open"), false);
  el.dispatchEvent(new FakeEvent("pointerenter", { pointerType: "touch" }));
  await sleep(80);
  assert.equal(tip.element.hasAttribute("data-open"), false, "touch never opens a tooltip");
  tip.destroy();
  assert.equal(el.hasAttribute("aria-describedby"), false);
});

/* ---------- ActionMenu ---------- */

const menuItems = (log) => [
  { label: "Rename", icon: "pencil", onClick: () => log.push("rename") },
  { label: "Duplicate", icon: "duplicate", onClick: () => log.push("duplicate") },
  { label: "Download", icon: "download", onClick: () => log.push("download") },
  { separator: true },
  { label: "Delete", icon: "trash", danger: true, onClick: () => log.push("delete") },
];

test("openMenu renders menu roles, focuses the first item and wires the trigger", () => {
  const anchor = trigger();
  const menu = ui.openMenu(anchor, menuItems([]));
  const list = menu.element.querySelector("[role=menu]");
  assert.ok(list);
  assert.equal(list.getAttribute("aria-labelledby"), anchor.id);
  assert.equal(anchor.getAttribute("aria-haspopup"), "true");
  assert.equal(anchor.getAttribute("aria-expanded"), "true");
  const items = menu.element.querySelectorAll("[role=menuitem]");
  assert.equal(items.length, 4);
  same(active(),items[0]);
  assert.ok(menu.element.querySelector("[role=separator]"));
  assert.ok(items[3].classList.contains("is-danger"));
});

test("openMenu arrow keys wrap, Home, End, PageUp and PageDown jump", () => {
  const anchor = trigger();
  const menu = ui.openMenu(anchor, menuItems([]));
  const items = menu.element.querySelectorAll("[role=menuitem]");
  key(active(), "ArrowUp");
  same(active(),items[3], "ArrowUp from the first item wraps to the last");
  key(active(), "ArrowDown");
  same(active(),items[0], "ArrowDown from the last item wraps to the first");
  key(active(), "End");
  same(active(),items[3]);
  key(active(), "Home");
  same(active(),items[0]);
  key(active(), "PageDown");
  same(active(),items[3]);
  key(active(), "PageUp");
  same(active(),items[0]);
});

test("openMenu type-ahead moves to the next item that starts with the typed letter", () => {
  const anchor = trigger();
  const menu = ui.openMenu(anchor, menuItems([]));
  const items = menu.element.querySelectorAll("[role=menuitem]");
  key(active(), "d");
  same(active(),items[1], "d goes to Duplicate");
  key(active(), "d");
  same(active(),items[1], "dd stays on an item that starts with dd? no, it stays when nothing matches");
});

test("openMenu type-ahead cycles through items with the same letter after the buffer clears", async () => {
  const anchor = trigger();
  const menu = ui.openMenu(anchor, menuItems([]));
  const items = menu.element.querySelectorAll("[role=menuitem]");
  key(active(), "d");
  same(active(),items[1]);
  await sleep(600);
  key(active(), "d");
  same(active(),items[2]);
  await sleep(600);
  key(active(), "d");
  same(active(),items[3]);
});

test("openMenu Escape closes and returns focus to the trigger", () => {
  const anchor = trigger();
  const menu = ui.openMenu(anchor, menuItems([]));
  key(active(), "Escape");
  assert.equal(menu.element.isConnected, false);
  same(active(),anchor);
  assert.equal(anchor.getAttribute("aria-expanded"), "false");
});

test("openMenu Enter runs the item, closes and returns focus; Tab closes onward from the trigger", () => {
  const log = [];
  const anchor = trigger();
  let menu = ui.openMenu(anchor, menuItems(log));
  key(active(), "ArrowDown");
  key(active(), "Enter");
  assert.deepEqual(log, ["duplicate"]);
  assert.equal(menu.element.isConnected, false);
  same(active(),anchor);
  menu = ui.openMenu(anchor, menuItems(log));
  const tab = key(active(), "Tab");
  assert.equal(tab.defaultPrevented, false, "Tab keeps its default so focus moves onward");
  assert.equal(menu.element.isConnected, false);
  same(active(),anchor);
});

test("openMenu skips disabled items, renders groups and checked radio items", () => {
  const anchor = trigger();
  const menu = ui.openMenu(anchor, [
    { group: "Theme" },
    { label: "System", checked: false, onClick() {} },
    { label: "Light", checked: true, onClick() {} },
    { label: "Dark", checked: false, disabled: true, onClick() {} },
  ]);
  const group = menu.element.querySelector("[role=group]");
  assert.ok(group);
  const heading = document.getElementById(group.getAttribute("aria-labelledby"));
  assert.equal(heading.textContent, "Theme");
  const radios = menu.element.querySelectorAll("[role=menuitemradio]");
  assert.equal(radios.length, 3);
  assert.equal(radios[1].getAttribute("aria-checked"), "true");
  assert.equal(radios[2].getAttribute("aria-disabled"), "true");
  key(active(), "End");
  same(active(),radios[1], "the disabled item takes no focus");
});

test("openMenu toggles closed when opened again for the same anchor, and a click outside closes", () => {
  const anchor = trigger();
  ui.openMenu(anchor, menuItems([]));
  const again = ui.openMenu(anchor, menuItems([]));
  assert.equal(again.element, null);
  assert.equal(document.querySelector("[role=menu]"), null);
  const menu = ui.openMenu(anchor, menuItems([]));
  const outside = h("div", {});
  document.body.appendChild(outside);
  outside.dispatchEvent(new FakeEvent("pointerdown", { bubbles: true }));
  assert.equal(menu.element.isConnected, false);
});

test("openMenu goes full screen with a Close button on narrow viewports", () => {
  installDOM({ narrow: true, focusEvents: true });
  const anchor = trigger();
  const menu = ui.openMenu(anchor, menuItems([]));
  assert.equal(menu.element.getAttribute("data-fullscreen-narrow"), "true");
  const close = menu.element.querySelector(".overlay-close");
  assert.equal(close.getAttribute("aria-label"), "Close menu", "the name stays unique beside a Dialog Close button");
  assert.equal(menu.element.style.top, "", "no anchored position on narrow viewports");
  click(close);
  assert.equal(menu.element.isConnected, false);
  same(active(),anchor);
});

test("openMenu confirm opens a ConfirmationDialog before running the item", async () => {
  const log = [];
  const anchor = trigger();
  ui.openMenu(anchor, [{ label: "Delete forever", danger: true, confirm: "Confirm delete", onClick: () => log.push("gone") }]);
  key(active(), "Enter");
  const dialog = document.querySelector("dialog[role=alertdialog]");
  assert.ok(dialog);
  assert.equal(log.length, 0);
  const confirm = dialog.querySelector(".btn-danger");
  assert.equal(confirm.textContent, "Delete");
  click(confirm);
  await sleep(0);
  assert.deepEqual(log, ["gone"]);
});

test("actionMenu opens on ArrowDown at the first item and on ArrowUp at the last item", () => {
  const el = trigger("More");
  const handle = ui.actionMenu(el, () => menuItems([]));
  assert.equal(el.getAttribute("aria-expanded"), "false");
  key(el, "ArrowUp");
  const items = document.querySelectorAll("[role=menuitem]");
  same(active(),items[items.length - 1]);
  key(active(), "Escape");
  key(el, "ArrowDown");
  same(active(),document.querySelectorAll("[role=menuitem]")[0]);
  handle.destroy();
});

/* ---------- AnchoredOverlay ---------- */

test("anchoredOverlay focuses its first control, closes on Escape and returns focus", () => {
  const anchor = trigger("Sync");
  const input = h("input", { type: "text" });
  const overlay = ui.anchoredOverlay(anchor, h("div", {}, h("p", { text: "All changes synced" }), input), { label: "Sync details" });
  assert.equal(overlay.element.getAttribute("role"), "dialog");
  assert.equal(overlay.element.getAttribute("aria-label"), "Sync details");
  same(active(),input, "focus moves to the first control of the content");
  key(active(), "Escape");
  assert.equal(overlay.element.isConnected, false);
  same(active(),anchor);
});

/* ---------- SelectPanel ---------- */

const spaces = [
  { id: "a", label: "Design notes" },
  { id: "b", label: "Research" },
  { id: "c", label: "Recipes", description: "Shared with family" },
  { id: "d", label: "Roadmap" },
];

test("selectPanel keeps focus in the filter, filters, announces the count and moves the active option", () => {
  const anchor = trigger("Space: Research");
  const panel = ui.selectPanel(anchor, { title: "Switch space", items: spaces, selected: "b" });
  const input = panel.element.querySelector("input[role=combobox]");
  assert.equal(anchor.getAttribute("aria-haspopup"), "dialog");
  assert.equal(panel.element.getAttribute("role"), "dialog");
  same(active(),input);
  const options = () => panel.element.querySelectorAll("[role=option]");
  assert.equal(options()[0].textContent, "Research", "selected items come first");
  assert.equal(options()[0].getAttribute("aria-selected"), "true");
  input.value = "re";
  input.dispatchEvent(new FakeEvent("input", { bubbles: true }));
  assert.deepEqual(Array.from(options()).map((o) => o.querySelector(".action-list-label").textContent), ["Research", "Recipes"]);
  assert.equal(panel.element.querySelector("[role=status]").textContent, "2 items");
  assert.equal(input.getAttribute("aria-activedescendant"), options()[0].id);
  key(input, "ArrowDown");
  assert.equal(input.getAttribute("aria-activedescendant"), options()[1].id);
  key(input, "Home");
  assert.equal(input.getAttribute("aria-activedescendant"), options()[0].id);
  key(input, "End");
  assert.equal(input.getAttribute("aria-activedescendant"), options()[1].id);
  same(active(),input);
});

test("selectPanel single select applies on Enter, closes and returns focus", () => {
  const anchor = trigger();
  let chosen = null;
  const panel = ui.selectPanel(anchor, { items: spaces, selected: "a", onSelect: (id) => { chosen = id; } });
  const input = panel.element.querySelector("input");
  key(input, "ArrowDown");
  key(input, "Enter");
  assert.equal(chosen, "b");
  assert.equal(panel.element.isConnected, false);
  same(active(),anchor);
});

test("selectPanel multiple select applies when it closes and Escape in the modal variant discards", () => {
  const anchor = trigger();
  let chosen = null;
  const panel = ui.selectPanel(anchor, { items: spaces, selected: ["a"], multiple: true, onSelect: (ids) => { chosen = ids; } });
  assert.equal(panel.element.querySelector("[role=listbox]").getAttribute("aria-multiselectable"), "true");
  const input = panel.element.querySelector("input");
  key(input, "ArrowDown");
  key(input, "Enter");
  assert.equal(chosen, null, "nothing applies before the panel closes");
  key(input, "Escape");
  assert.deepEqual(chosen.sort(), ["a", "b"]);

  let modalChoice = "untouched";
  const modal = ui.selectPanel(anchor, { items: spaces, selected: "a", variant: "modal", onSelect: (id) => { modalChoice = id; } });
  assert.ok(modal.element.querySelector(".select-panel-footer"));
  const modalInput = modal.element.querySelector("input");
  key(modalInput, "ArrowDown");
  key(modalInput, "Enter");
  assert.equal(modal.element.isConnected, true, "modal single select waits for Save");
  key(modalInput, "Escape");
  assert.equal(modalChoice, "untouched");
});

test("selectPanel fills the screen with Cancel and Save on narrow viewports", () => {
  installDOM({ narrow: true, focusEvents: true });
  const anchor = trigger();
  let chosen = null;
  const panel = ui.selectPanel(anchor, { items: spaces, selected: "a", onSelect: (id) => { chosen = id; } });
  assert.equal(panel.element.getAttribute("data-fullscreen-narrow"), "true");
  const [cancel, save] = panel.element.querySelectorAll(".select-panel-footer .btn");
  assert.equal(cancel.textContent, "Cancel");
  assert.equal(save.textContent, "Save");
  const second = panel.element.querySelectorAll("[role=option]")[1];
  click(second);
  assert.equal(chosen, null, "a choice waits for Save");
  click(save);
  assert.equal(chosen, "b");
});

/* ---------- Autocomplete ---------- */

test("autocomplete sets combobox ARIA, lists matches, moves with arrows and chooses with Enter", async () => {
  const input = h("input", { type: "text" });
  document.body.appendChild(h("span", { class: "text-input" }, input));
  input.focus();
  let picked = null;
  const ac = ui.autocomplete(input, {
    source: () => [{ id: "1", label: "Ana Lima" }, { id: "2", label: "Anders" }, { id: "3", label: "Bea" }],
    onSelect: (item) => { picked = item; },
    allowAdd: (value) => ({ id: "new", label: `Invite ${value}` }),
  });
  assert.equal(input.getAttribute("role"), "combobox");
  assert.equal(input.getAttribute("aria-autocomplete"), "list");
  input.value = "an";
  input.dispatchEvent(new FakeEvent("input", { bubbles: true }));
  await sleep(0);
  assert.equal(input.getAttribute("aria-expanded"), "true");
  const list = document.getElementById(input.getAttribute("aria-controls"));
  assert.equal(list.getAttribute("role"), "listbox");
  const options = list.querySelectorAll("[role=option]");
  assert.equal(options.length, 3, "two matches and the add item");
  assert.match(options[2].textContent, /Invite an/);
  assert.equal(input.getAttribute("aria-activedescendant"), options[0].id);
  key(input, "ArrowDown");
  assert.equal(input.getAttribute("aria-activedescendant"), options[1].id);
  key(input, "Enter");
  assert.equal(picked.label, "Anders");
  assert.equal(input.getAttribute("aria-expanded"), "false");
  input.value = "zz";
  input.dispatchEvent(new FakeEvent("input", { bubbles: true }));
  await sleep(0);
  const escape = key(input, "Escape");
  assert.equal(escape.defaultPrevented, true);
  assert.equal(input.getAttribute("aria-expanded"), "false");
  ac.destroy();
});

/* ---------- Dialog ---------- */

test("openDialog labels the dialog, focuses the close button, traps Tab and locks scrolling", () => {
  const opener = trigger("Invite");
  const handle = ui.openDialog({ title: "Invite people", subtitle: "They get an email", content: h("p", { text: "Nothing to fill in" }), footer: [{ label: "Cancel" }, { label: "Send", variant: "primary" }] });
  const dialog = handle.element;
  assert.equal(dialog.tagName, "DIALOG");
  assert.equal(dialog.getAttribute("aria-modal"), "true");
  assert.equal(document.getElementById(dialog.getAttribute("aria-labelledby")).tagName, "H1");
  assert.equal(document.getElementById(dialog.getAttribute("aria-describedby")).textContent, "They get an email");
  assert.equal(dialog.hasAttribute("open"), true);
  const close = dialog.querySelector(".dialog-close");
  same(active(),close);
  assert.equal(document.body.style.overflow, "hidden");
  const buttons = dialog.querySelectorAll(".dialog-footer .btn");
  buttons[1].focus();
  const tab = key(buttons[1], "Tab");
  assert.equal(tab.defaultPrevented, true);
  same(active(),close, "Tab from the last control wraps to the first");
  key(close, "Tab", { shiftKey: true });
  same(active(),buttons[1], "Shift+Tab from the first control wraps to the last");
  key(active(), "Escape");
  assert.equal(dialog.isConnected, false);
  assert.equal(document.body.style.overflow, "");
  same(active(),opener, "focus returns to the trigger");
});

test("openDialog with fields is full screen on narrow viewports, without fields a bottom sheet", () => {
  const form = ui.openDialog({ title: "New space", content: ui.formControl({ label: "Name", name: "name" }) });
  assert.equal(form.element.getAttribute("data-narrow"), "fullscreen");
  form.close();
  const info = ui.openDialog({ title: "Details", content: h("p", { text: "Read only" }) });
  assert.equal(info.element.getAttribute("data-narrow"), "bottom");
  info.close();
});

test("openDialog backdrop click closes, but not while a form has unsaved changes", () => {
  const plain = ui.openDialog({ title: "Details", content: h("p", { text: "x" }) });
  plain.element.dispatchEvent(new FakeEvent("click", { bubbles: true }));
  assert.equal(plain.element.isConnected, false);
  const input = h("input", { type: "text", name: "title" });
  const edit = ui.openDialog({ title: "Rename", content: h("form", {}, input) });
  input.dispatchEvent(new FakeEvent("input", { bubbles: true }));
  edit.element.dispatchEvent(new FakeEvent("click", { bubbles: true }));
  assert.equal(edit.element.isConnected, true);
  edit.close();
});

test("openDialog nests: Escape closes only the top dialog and the lock holds until the last closes", () => {
  const first = ui.openDialog({ title: "Move to", content: h("p", { text: "tree" }) });
  const second = ui.openDialog({ title: "New folder", content: h("input", { type: "text" }) });
  key(active(), "Escape");
  assert.equal(second.element.isConnected, false);
  assert.equal(first.element.isConnected, true);
  assert.equal(document.body.style.overflow, "hidden");
  first.close();
  assert.equal(document.body.style.overflow, "");
});

test("openDialog keeps the dialog open while an action runs and shows a Banner when it fails", async () => {
  let fail = true;
  const handle = ui.openDialog({ title: "Create token", content: h("p", { text: "x" }), footer: [{ label: "Create", variant: "primary", onClick: async () => { if (fail) throw new Error("the name is taken"); } }] });
  const create = handle.element.querySelector(".btn-primary");
  click(create);
  await sleep(0);
  await sleep(0);
  assert.equal(handle.element.isConnected, true);
  const banner = handle.element.querySelector(".dialog-banner .banner-critical");
  assert.ok(banner);
  assert.match(banner.textContent, /The name is taken\./);
  fail = false;
  click(create);
  await sleep(0);
  await sleep(0);
  assert.equal(handle.element.isConnected, false);
});

test("announce writes inside the top open dialog", () => {
  const handle = ui.openDialog({ title: "Share", content: h("p", { text: "x" }) });
  const region = ui.announce("Link copied");
  assert.ok(handle.element.contains(region));
  handle.close();
});

/* ---------- ConfirmationDialog ---------- */

test("confirmDialog is an alertdialog that focuses Cancel for danger and resolves false on Escape", async () => {
  const opener = trigger("Empty trash");
  const result = ui.confirmDialog({ title: "Empty trash?", body: "3 pages will be deleted forever.", confirmLabel: "Empty trash" });
  const dialog = document.querySelector("dialog");
  assert.equal(dialog.getAttribute("role"), "alertdialog");
  assert.equal(dialog.getAttribute("data-width"), "medium");
  assert.equal(dialog.getAttribute("data-narrow"), "center");
  assert.equal(document.getElementById(dialog.getAttribute("aria-describedby")).textContent, "3 pages will be deleted forever.");
  const [cancel, confirm] = dialog.querySelectorAll(".dialog-footer .btn");
  assert.equal(cancel.textContent, "Cancel");
  assert.ok(confirm.classList.contains("btn-danger"));
  same(active(),cancel);
  key(active(), "Escape");
  assert.equal(await result, false);
  same(active(),opener);
});

test("confirmDialog keeps the confirm button busy until onConfirm settles and shows failures", async () => {
  let release;
  let attempt = 0;
  const result = ui.confirmDialog({
    title: "Revoke this link?",
    confirmLabel: "Revoke link",
    onConfirm: () => {
      attempt++;
      if (attempt === 1) return Promise.reject(new Error("offline"));
      return new Promise((resolve) => { release = resolve; });
    },
  });
  const dialog = document.querySelector("dialog");
  const confirm = dialog.querySelector(".btn-danger");
  click(confirm);
  await sleep(0);
  await sleep(0);
  assert.ok(dialog.querySelector(".banner-critical"), "a failure shows a critical banner");
  assert.equal(dialog.isConnected, true);
  click(confirm);
  await sleep(0);
  assert.equal(confirm.getAttribute("aria-busy"), "true");
  assert.equal(dialog.isConnected, true);
  release();
  assert.equal(await result, true);
  assert.equal(dialog.isConnected, false);
});

/* ---------- Messaging ---------- */

test("banner builds a labelled section, dismisses and replaces a lower severity banner", () => {
  const host = h("div", {});
  document.body.appendChild(host);
  const after = h("button", { type: "button", text: "Next" });
  host.appendChild(after);
  let dismissed = 0;
  const info = ui.banner({ variant: "info", title: "Import running", description: "12 of 40 files", dismissible: true, onDismiss: () => dismissed++, into: host });
  assert.equal(info.tagName, "SECTION");
  assert.equal(info.getAttribute("aria-label"), "Information");
  assert.equal(info.querySelector("h2").textContent, "Import running");
  same(host.firstChild, info);
  const critical = ui.banner({ variant: "critical", title: "Save failed", dismissible: true, actions: [{ label: "Retry", primary: true }], into: host });
  assert.equal(info.isConnected, false, "a critical banner replaces the info banner");
  assert.equal(critical.querySelector(".banner-dismiss"), null, "critical banners cannot be dismissed");
  assert.equal(critical.querySelectorAll(".banner-actions .btn").length, 1);
  const lower = ui.banner({ variant: "success", title: "Saved", into: host });
  assert.equal(lower.isConnected, false, "a lower severity banner does not replace a critical one");
  host.replaceChildren(after);
  const again = ui.banner({ variant: "warning", title: "Offline", dismissible: true, onDismiss: () => dismissed++, into: host });
  click(again.querySelector(".banner-dismiss"));
  assert.equal(again.isConnected, false);
  assert.equal(dismissed, 1);
  same(active(),after, "focus moves to the element after the banner");
});

test("inlineMessage uses the outline icon at medium size and the filled icon at small size", () => {
  const medium = ui.inlineMessage({ variant: "success", text: "Profile saved" });
  assert.ok(medium.classList.contains("inline-message-success"));
  assert.ok(medium.querySelector("svg").getAttribute("class").includes("check-circle"));
  const small = ui.inlineMessage({ variant: "critical", size: "small", text: "Upload failed" });
  assert.ok(small.querySelector("svg").getAttribute("class").includes("alert-fill"));
  assert.equal(small.textContent, "Upload failed");
});

/* ---------- Undo ---------- */

test("undoRow replaces the row, moves focus to Undo and restores the row on Undo", async () => {
  const list = h("ul", {});
  const remove = h("button", { type: "button", text: "Remove" });
  const row = h("li", { class: "list-row" }, h("span", { text: "Grace Hopper" }), remove);
  list.appendChild(row);
  document.body.appendChild(list);
  remove.focus();
  let undone = 0;
  const handle = ui.undoRow(row, { message: "Removed Grace Hopper", onUndo: () => undone++ });
  assert.match(row.textContent, /Removed Grace Hopper/);
  same(active(),handle.button);
  assert.equal(document.querySelector("[data-status-region]").textContent, "Removed Grace Hopper");
  click(handle.button);
  assert.equal(await handle.done, "undone");
  assert.equal(undone, 1);
  assert.match(row.textContent, /Grace Hopper/);
  assert.ok(row.querySelector("button"));
});

test("undoRow has no timer and ends when the person acts elsewhere in the list", async () => {
  const list = h("ul", {});
  const row = h("li", {}, "Old draft");
  const other = h("li", {}, h("button", { type: "button", text: "Other" }));
  list.append(row, other);
  document.body.appendChild(list);
  const handle = ui.undoRow(row, { message: "Moved to trash" });
  await sleep(50);
  assert.equal(row.isConnected, true, "no timer removes the row");
  other.firstChild.dispatchEvent(new FakeEvent("pointerdown", { bubbles: true }));
  assert.equal(await handle.done, "kept");
  assert.equal(row.isConnected, false);
});

test("undoable with where offers Undo in the row; without where in an info banner; failures show a critical banner", async () => {
  const list = h("ul", {});
  const row = h("li", {}, "Alan Turing");
  list.appendChild(row);
  document.body.appendChild(list);
  const log = [];
  const settled = ui.undoable({ label: "Removed Alan Turing", where: row, run: () => log.push("run"), undo: () => log.push("undo") });
  await sleep(0);
  click(row.querySelector(".undo-btn"));
  assert.equal(await settled, "undone");
  assert.deepEqual(log, ["run", "undo"]);

  const host = h("div", {});
  document.body.appendChild(host);
  assert.equal(await ui.undoable({ label: "Moved “Budget” to the trash", into: host, run: () => {}, undo: () => log.push("undo2") }), "kept");
  const bannerEl = host.querySelector(".banner-info");
  assert.match(bannerEl.textContent, /Moved “Budget” to the trash/);
  click(bannerEl.querySelector(".banner-actions .btn"));
  await sleep(0);
  await sleep(0);
  assert.ok(log.includes("undo2"));

  assert.equal(await ui.undoable({ label: "x", into: host, run: () => { throw new Error("forbidden"); }, undo: () => {} }), "failed");
  assert.match(host.querySelector(".banner-critical").textContent, /Forbidden\./);
});

/* ---------- Labels, counters, tokens, avatars ---------- */

test("counterLabel hides the visible number from screen readers and hides when empty", () => {
  const el = ui.counterLabel(12);
  assert.equal(el.firstChild.getAttribute("aria-hidden"), "true");
  assert.equal(el.querySelector(".sr-only").textContent, "(12)");
  ui.setCount(el, null);
  assert.equal(el.hidden, true);
});

test("label, token and avatar follow Primer's variants, removal keys and initials", () => {
  const admin = ui.label("Admin", { variant: "accent", size: "large" });
  assert.ok(admin.classList.contains("ui-label") && admin.classList.contains("ui-label-accent") && admin.classList.contains("ui-label-lg"));
  let removed = 0;
  const tok = ui.token("Design notes", { size: "large", onRemove: () => removed++ });
  assert.equal(tok.querySelector(".token-remove").getAttribute("aria-label"), "Remove Design notes");
  key(tok, "Backspace");
  key(tok, "Delete");
  assert.equal(removed, 2);
  assert.equal(ui.token("tag", { size: "medium", onRemove: () => {} }).querySelector(".token-remove"), null, "medium tokens take no remove button");
  const av = ui.avatar({ name: "Ada Lovelace", color: "#8250df", size: 32 });
  assert.equal(av.textContent, "AL");
  assert.equal(av.getAttribute("aria-label"), "Ada Lovelace");
  assert.equal(av.style.props["--avatar-size"], "32px");
  assert.equal(ui.avatar({ name: "Ada", alt: "" }).hasAttribute("aria-label"), false);
});

/* ---------- Loading and empty states ---------- */

test("skeleton, progressBar and blankslate carry their ARIA and structure", () => {
  const rows = ui.skeleton({ variant: "rows", rows: 3 });
  assert.equal(rows.getAttribute("aria-busy"), "true");
  assert.equal(rows.querySelectorAll(".skeleton-row").length, 3);
  const bar = ui.progressBar({ value: 3.2, max: 8, label: "Upload", valueText: "3.2 MB of 8 MB" });
  assert.equal(bar.getAttribute("role"), "progressbar");
  assert.equal(bar.getAttribute("aria-valuenow"), "40");
  assert.equal(bar.getAttribute("aria-valuetext"), "3.2 MB of 8 MB");
  bar.set(8);
  assert.equal(bar.getAttribute("aria-valuenow"), "100");
  const slate = ui.blankslate({ icon: "comment", title: "No comments yet", description: "Select text", primaryAction: { label: "Comment" }, size: "small", border: true });
  assert.ok(slate.classList.contains("blankslate-small") && slate.classList.contains("blankslate-border"));
  assert.equal(slate.querySelector("h2").textContent, "No comments yet");
  assert.equal(slate.querySelectorAll(".btn-primary").length, 1);
});

/* ---------- Form controls ---------- */

test("formControl links label, caption and validation and marks required fields", () => {
  const el = ui.formControl({ label: "Name", name: "name", caption: "Shown to members", required: true, error: "Enter a name" });
  const input = el.querySelector("input");
  assert.equal(el.querySelector("label").getAttribute("for"), input.id);
  assert.equal(input.required, true);
  assert.equal(input.getAttribute("aria-invalid"), "true");
  const described = input.getAttribute("aria-describedby").split(" ");
  assert.equal(described.length, 2);
  assert.ok(document.body && described.every((id) => el.querySelector("#" + id)));
  assert.ok(el.querySelector(".form-required"));
  assert.ok(el.classList.contains("field"), "older callers still find .field");
  const select = ui.formControl({ label: "Access", name: "access", type: "select", value: "edit", options: [{ value: "view", label: "Can view" }, { value: "edit", label: "Can edit" }] });
  assert.ok(select.querySelector(".select-caret"));
  const check = ui.formControl({ label: "Include child pages", name: "children", type: "checkbox", value: true });
  assert.ok(check.classList.contains("form-control-horizontal"));
  assert.equal(check.querySelector("input").checked, true);
});

test("showFieldErrors focuses the first invalid field for one or two errors", () => {
  const form = h("form", {}, ui.formControl({ label: "Name", name: "name" }), ui.formControl({ label: "Slug", name: "slug" }));
  document.body.appendChild(form);
  assert.equal(ui.showFieldErrors(form, [{ field: "slug", message: "use lowercase letters" }, { field: "name", message: "enter a name" }]), true);
  same(active(),form.querySelector("[name=slug]"));
  assert.match(form.querySelector("[name=slug]").closest(".field").textContent, /Use lowercase letters\./);
  assert.equal(form.querySelector("[data-form-banner]"), null);
  ui.clearFieldErrors(form);
  assert.equal(form.querySelector("[aria-invalid=true]"), null);
});

test("showFieldErrors shows an error summary banner with links for three or more errors", () => {
  const form = h("form", {}, ...["name", "slug", "icon"].map((name) => ui.formControl({ label: name, name })));
  document.body.appendChild(form);
  ui.showFieldErrors(form, { name: "enter a name", slug: "enter a slug", icon: "choose an icon" });
  const summary = form.querySelector("[data-form-banner]");
  assert.ok(summary.classList.contains("banner-critical"));
  same(active(),summary);
  const links = summary.querySelectorAll("a");
  assert.equal(links.length, 3);
  click(links[1]);
  same(active(),form.querySelector("[name=slug]"));
});

test("showFieldErrors puts a message without a field in a critical banner", () => {
  const form = h("form", {}, ui.formControl({ label: "Name", name: "name" }));
  document.body.appendChild(form);
  assert.equal(ui.showFieldErrors(form, { message: "the server is busy" }), false);
  assert.match(form.querySelector("[data-form-banner]").textContent, /The server is busy\./);
});

/* ---------- Edit in place ---------- */

test("editInPlace shows Save and Cancel beside the field, saves on Enter and on Save", async () => {
  const name = h("a", { href: "#", text: "Budget" });
  document.body.appendChild(h("div", { class: "row" }, name));
  const saved = [];
  const edit = ui.editInPlace(name, { onSave: (v) => saved.push(v) });
  const buttons = edit.element.querySelectorAll(".btn");
  assert.equal(buttons[0].textContent, "Save");
  assert.ok(buttons[0].classList.contains("btn-primary") && buttons[0].classList.contains("btn-sm"));
  assert.equal(buttons[1].textContent, "Cancel");
  assert.ok(buttons[1].classList.contains("btn-invisible"));
  assert.equal(name.hidden, true);
  same(active(),edit.input);
  edit.input.value = "Budget 2027";
  key(edit.input, "Enter");
  assert.equal(await edit.done, "Budget 2027");
  assert.deepEqual(saved, ["Budget 2027"]);
  assert.equal(name.hidden, false);
  same(active(),name, "focus returns to the renamed element");

  const second = ui.editInPlace(name, { onSave: (v) => saved.push(v) });
  second.input.value = "Plans";
  edit.element.isConnected;
  second.element.querySelector("form").dispatchEvent(new FakeEvent("submit", { bubbles: true }));
  assert.equal(await second.done, "Plans");
});

test("editInPlace cancels on Escape and on Cancel and keeps errors under the field", async () => {
  const text = h("span", { text: "Team" });
  document.body.appendChild(text);
  const first = ui.editInPlace(text, {});
  first.input.value = "Changed";
  key(first.input, "Escape");
  assert.equal(await first.done, null);
  const second = ui.editInPlace(text, {});
  click(second.element.querySelectorAll(".btn")[1]);
  assert.equal(await second.done, null);
  const third = ui.editInPlace(text, { validate: (v) => (v.length < 3 ? "use at least 3 characters" : null), onSave: () => { throw new Error("a space with that name exists"); } });
  third.input.value = "ab";
  assert.equal(await third.save(), false);
  assert.match(third.element.textContent, /Use at least 3 characters\./);
  assert.equal(third.input.getAttribute("aria-invalid"), "true");
  third.input.value = "Harbour";
  assert.equal(await third.save(), false);
  assert.match(third.element.textContent, /A space with that name exists\./);
  third.cancel();
});

/* ---------- Expanded rows ---------- */

test("expandRow keeps one expanded row per list and collapses on Escape with focus back", () => {
  const list = h("ul", {});
  const a = h("li", { class: "list-row" }, "A");
  const b = h("li", { class: "list-row" }, "B");
  list.append(a, b);
  document.body.appendChild(list);
  const toggle = h("button", { type: "button", text: "Details" });
  a.appendChild(toggle);
  const first = ui.expandRow(a, h("button", { type: "button", text: "Inside" }), { trigger: toggle });
  assert.equal(toggle.getAttribute("aria-expanded"), "true");
  key(first.element.firstChild, "Escape");
  assert.equal(a.querySelector(".row-expand"), null);
  same(active(),toggle);
  ui.expandRow(a, h("p", { text: "details of A" }));
  ui.expandRow(b, h("p", { text: "details of B" }));
  assert.equal(document.querySelectorAll(".row-expand").length, 1);
  const tbody = h("tbody", {});
  const tr = h("tr", {}, h("td", {}, "x"), h("td", {}, "y"));
  tbody.appendChild(tr);
  document.body.appendChild(h("table", {}, tbody));
  ui.expandRow(tr, h("p", { text: "actions" }));
  assert.equal(tr.nextSibling.firstChild.colSpan, 2);
});

/* ---------- TreeView ---------- */

function sampleTree(extra = {}) {
  const opened = [];
  const toggled = [];
  const view = ui.treeView({
    label: "Pages",
    items: [
      { id: "a", label: "Alpha", icon: "file-directory-fill", expanded: true, children: [{ id: "a1", label: "Apple" }, { id: "a2", label: "Avocado" }] },
      { id: "b", label: "Beta", icon: "file-directory-fill", children: [{ id: "b1", label: "Banana" }] },
      { id: "c", label: "Cherry", icon: "note", current: true },
    ],
    onOpen: (item) => opened.push(item.id),
    onToggle: (item, on) => toggled.push([item.id, on]),
    ...extra,
  });
  document.body.appendChild(view.element);
  const item = (id) => view.element.querySelector(`[data-id=${id}]`);
  return { view, item, opened, toggled };
}

test("treeView renders tree ARIA with one tab stop on the current item", () => {
  const { view, item } = sampleTree();
  assert.equal(view.element.getAttribute("role"), "tree");
  assert.equal(view.element.getAttribute("aria-label"), "Pages");
  assert.equal(item("a").getAttribute("aria-level"), "1");
  assert.equal(item("a1").getAttribute("aria-level"), "2");
  assert.equal(item("a").getAttribute("aria-expanded"), "true");
  assert.equal(item("b").getAttribute("aria-expanded"), "false");
  assert.equal(item("c").hasAttribute("aria-expanded"), false);
  assert.equal(item("c").getAttribute("aria-current"), "true");
  assert.equal(view.element.querySelectorAll("[tabindex='0']").length, 1);
  assert.equal(item("c").getAttribute("tabindex"), "0");
  assert.equal(item("a1").parentElement.getAttribute("role"), "group");
});

test("treeView arrows move between visible rows, Right and Left expand, collapse and move", () => {
  const { item, toggled } = sampleTree();
  item("a").focus();
  key(active(), "ArrowDown");
  same(active(),item("a1"));
  key(active(), "ArrowDown");
  key(active(), "ArrowDown");
  same(active(),item("b"), "collapsed children are skipped");
  key(active(), "ArrowRight");
  assert.equal(item("b").getAttribute("aria-expanded"), "true");
  same(active(),item("b"));
  key(active(), "ArrowRight");
  same(active(),item("b1"));
  key(active(), "ArrowLeft");
  same(active(),item("b"), "Left on a child moves to its parent");
  key(active(), "ArrowLeft");
  assert.equal(item("b").getAttribute("aria-expanded"), "false");
  assert.deepEqual(toggled, [["b", true], ["b", false]]);
  key(active(), "Home");
  same(active(),item("a"));
  key(active(), "End");
  same(active(),item("c"));
  assert.equal(item("c").getAttribute("tabindex"), "0");
  assert.equal(item("a").getAttribute("tabindex"), "-1");
});

test("treeView Enter opens, type-ahead skips collapsed rows, and a chevron click only toggles", async () => {
  const { item, opened } = sampleTree();
  item("a").focus();
  key(active(), "Enter");
  assert.deepEqual(opened, ["a"]);
  key(active(), "b");
  same(active(),item("b"), "Banana is collapsed, so b goes to Beta");
  await sleep(350);
  key(active(), "a");
  same(active(),item("a"), "type-ahead wraps");
  key(active(), "v");
  same(active(), item("a2"), "av moves on to Avocado");
  await sleep(350);
  click(item("b").querySelector(".tree-view-toggle"));
  assert.equal(item("b").getAttribute("aria-expanded"), "true");
  assert.deepEqual(opened, ["a"], "the chevron does not open the item");
});

test("treeView loads children on expand and passes other keys to onKey", async () => {
  const keys = [];
  const view = ui.treeView({
    label: "Pages",
    items: [{ id: "f", label: "Folder", hasChildren: true }],
    loadChildren: async () => [{ id: "g", label: "Guide" }],
    onKey: (item, event) => keys.push([item.id, event.key]),
  });
  document.body.appendChild(view.element);
  const folder = view.element.querySelector("[data-id=f]");
  folder.focus();
  key(folder, "ArrowRight");
  assert.ok(view.element.querySelector(".tree-view-loading"));
  await sleep(0);
  await sleep(0);
  assert.ok(view.element.querySelector("[data-id=g]"));
  key(folder, "F2");
  assert.deepEqual(keys, [["f", "F2"]]);
});

/* ---------- SegmentedControl, ToggleSwitch, Details ---------- */

test("segmentedControl uses aria-current, applies at once and moves focus with arrow keys", () => {
  const changes = [];
  const el = ui.segmentedControl({
    label: "Editor mode",
    items: [{ label: "Live", value: "live", icon: "pencil", selected: true }, { label: "Source", value: "source", icon: "code" }, { label: "Reading", value: "reading", icon: "eye", iconOnly: true }],
    onChange: (value) => changes.push(value),
  });
  document.body.appendChild(el);
  assert.equal(el.tagName, "UL");
  assert.equal(el.getAttribute("aria-label"), "Editor mode");
  const buttons = el.querySelectorAll("button");
  assert.equal(buttons[0].getAttribute("aria-current"), "true");
  buttons[0].focus();
  key(buttons[0], "ArrowRight");
  same(active(),buttons[1]);
  key(buttons[1], "End");
  same(active(),buttons[2]);
  key(buttons[2], "ArrowRight");
  same(active(),buttons[0], "arrows wrap");
  key(buttons[0], "ArrowLeft");
  same(active(),buttons[2]);
  click(buttons[1]);
  assert.deepEqual(changes, ["source"]);
  assert.equal(buttons[1].getAttribute("aria-current"), "true");
  assert.equal(buttons[0].getAttribute("aria-current"), "false");
  assert.ok(buttons[2].getAttribute("aria-labelledby"), "icon-only segments are named by their tooltip");
});

test("toggleSwitch toggles aria-pressed, shows a spinner while saving and reverts on failure", async () => {
  const label = h("span", { id: "ai-label", text: "AI features" });
  document.body.appendChild(label);
  let fail = true;
  const sw = ui.toggleSwitch({ labelledBy: "ai-label", onChange: () => (fail ? Promise.reject(new Error("no")) : Promise.resolve()) });
  document.body.appendChild(sw);
  assert.equal(sw.button.getAttribute("aria-labelledby"), "ai-label");
  assert.equal(sw.button.getAttribute("aria-pressed"), "false");
  click(sw.button);
  assert.equal(sw.button.getAttribute("aria-pressed"), "true");
  assert.ok(sw.querySelector(".toggle-switch-spinner"));
  await sleep(0);
  await sleep(0);
  assert.equal(sw.button.getAttribute("aria-pressed"), "false", "a failed save turns the switch back");
  fail = false;
  click(sw.button);
  await sleep(0);
  await sleep(0);
  assert.equal(sw.button.getAttribute("aria-pressed"), "true");
  assert.equal(sw.querySelector(".toggle-switch-spinner"), null);
});

test("details builds a native disclosure with a Button invisible small summary", () => {
  const el = ui.details({ summary: "More options", content: h("p", { text: "x" }) });
  assert.equal(el.tagName, "DETAILS");
  const summary = el.firstChild;
  assert.equal(summary.tagName, "SUMMARY");
  assert.ok(summary.classList.contains("btn-invisible") && summary.classList.contains("btn-sm"));
  assert.ok(summary.querySelector(".details-closed svg") && summary.querySelector(".details-open svg"));
  const add = ui.details({ summary: "New space", kind: "add" });
  assert.ok(add.firstChild.querySelector("svg").getAttribute("class").includes("plus"));
});

test("relativeTime renders a relative-time element without a title", () => {
  const el = ui.relativeTime("2026-09-13T10:00:00Z");
  assert.equal(el.tagName, "RELATIVE-TIME");
  assert.equal(el.getAttribute("datetime"), "2026-09-13T10:00:00.000Z");
  assert.equal(el.hasAttribute("no-title"), true);
  assert.equal(el.textContent, "Sep 13, 2026");
  assert.equal(ui.relativeTime(new Date("2026-08-26T15:05:00Z"), { precise: true }).getAttribute("format"), "datetime");
});

/* ---------- Drop area and uploads ---------- */

test("createDropzone offers a Choose files button as the keyboard route and reports rejected files", () => {
  const got = [];
  const zone = ui.createDropzone({ accept: "image/*", onFiles: (files) => got.push(...files) });
  assert.equal(zone.hasAttribute("tabindex"), false, "the box is not focusable");
  assert.equal(zone.querySelector(".btn").textContent, "Choose files");
  const drop = new FakeEvent("drop", { bubbles: true, dataTransfer: { types: ["Files"], files: [{ name: "a.png", type: "image/png" }, { name: "b.pdf", type: "application/pdf" }] } });
  zone.dispatchEvent(drop);
  assert.equal(got.length, 1);
  assert.ok(zone.classList.contains("has-error"));
  assert.match(zone.querySelector(".drop-area-error").textContent, /1 file is not an accepted type/);
});

test("uploadList shows a small progress bar while uploading and an inline message on failure", () => {
  const uploads = [
    { key: "1", filename: "a.png", mime: "image/png", size: 2048, loaded: 1024, state: "uploading", createdAt: 2 },
    { key: "2", filename: "b.pdf", size: 10, state: "failed", error: "too large", createdAt: 1 },
  ];
  const list = ui.uploadList({ sync: { uploads: () => uploads, retryUpload() {}, cancelUpload() {} } });
  document.body.appendChild(list);
  const [first, second] = list.querySelectorAll(".upload-row");
  assert.equal(first.querySelector("[role=progressbar]").getAttribute("aria-valuenow"), "50");
  assert.equal(first.querySelector("[role=progressbar]").hidden, false);
  assert.match(second.querySelector(".inline-message-critical").textContent, /Too large\./);
  const labels = Array.from(second.querySelectorAll(".upload-actions button")).map((b) => b.getAttribute("aria-label"));
  assert.deepEqual(labels, ["Retry upload", "Cancel upload"]);
  click(second.querySelectorAll(".upload-actions button")[1]);
  assert.ok(document.querySelector("dialog[role=alertdialog]"), "cancelling asks first");
});

/* ---------- Package 11 shared APIs ---------- */

test("openMenu gives a section with icons the check in the leading slot and no check column", () => {
  const anchor = trigger();
  const handle = ui.openMenu(anchor, [
    { label: "Open", icon: "file" },
    { label: "Keep available offline", icon: "cloud", checked: true, selectionType: "multiple" },
    { label: "Share", icon: "people", checked: false, selectionType: "multiple" },
    { group: "Theme" },
    { label: "Light", checked: true, selectionType: "single" },
    { label: "Dark", checked: false, selectionType: "single" },
  ]);
  const rows = handle.element.querySelectorAll(".action-list-item");
  assert.equal(rows.length, 5);
  const [open, offline, share, light, dark] = rows;
  assert.equal(open.querySelector(".action-list-check"), null, "a section with icons has no check column");
  assert.equal(offline.querySelector(".action-list-check"), null);
  assert.ok(offline.querySelector(".action-list-leading svg").getAttribute("class").includes("octicon-check"), "a checked item shows the check in the leading slot");
  assert.ok(share.querySelector(".action-list-leading svg").getAttribute("class").includes("octicon-people"), "an unchecked item keeps its icon");
  assert.equal(offline.getAttribute("aria-checked"), "true");
  assert.ok(light.querySelector(".action-list-check"), "a section without icons keeps the check column");
  assert.ok(dark.querySelector(".action-list-check"));
  handle.close();
});

test("openMenu group headings show a description that describes the group", () => {
  const anchor = trigger();
  const handle = ui.openMenu(anchor, [{ group: "Ada Lovelace", description: "ada@example.com" }, { label: "Settings", href: "/settings/profile" }]);
  const group = handle.element.querySelector("[role=group]");
  const nameId = group.getAttribute("aria-labelledby");
  const descId = group.getAttribute("aria-describedby");
  assert.equal(document.getElementById(nameId).textContent, "Ada Lovelace");
  assert.equal(document.getElementById(descId).textContent, "ada@example.com");
  assert.ok(document.getElementById(descId).classList.contains("action-list-heading-description"));
  const plain = ui.openMenu(trigger("Other"), [{ group: "Theme" }, { label: "Light" }]);
  assert.equal(plain.element.querySelector("[role=group]").getAttribute("aria-describedby"), null);
  plain.close();
  handle.close();
});

test("placeOverlay caps an overlay taller than the viewport to the viewport less 8 px margins", () => {
  const panel = h("div", {});
  let maxHeight = "";
  let natural = 416;
  Object.defineProperty(panel, "offsetWidth", { get: () => 200 });
  Object.defineProperty(panel, "offsetHeight", { get: () => (maxHeight ? Math.min(natural, parseInt(maxHeight, 10)) : natural) });
  const style = panel.style;
  Object.defineProperty(style, "maxHeight", { get: () => maxHeight, set: (v) => { maxHeight = v; }, configurable: true });
  const saved = [window.innerWidth, window.innerHeight];
  window.innerWidth = 844;
  window.innerHeight = 390;
  try {
    ui.placeOverlay(panel, { top: 100, bottom: 132, left: 600, right: 632, width: 32, height: 32 }, "bottom-end");
    assert.equal(maxHeight, "374px");
    assert.equal(style.top, "8px");
    natural = 200;
    ui.placeOverlay(panel, { top: 100, bottom: 132, left: 600, right: 632, width: 32, height: 32 }, "bottom-end");
    assert.equal(maxHeight, "", "a short overlay keeps the stylesheet maximum");
  } finally {
    [window.innerWidth, window.innerHeight] = saved;
  }
});

test("banner with into announces a lower severity banner that it keeps out of the page", () => {
  const host = h("div", {});
  document.body.appendChild(host);
  ui.banner({ variant: "critical", title: "Save failed", into: host });
  const lower = ui.banner({ variant: "success", title: "Moved to trash", description: "Budget 2025", into: host });
  assert.equal(lower.isConnected, false);
  assert.match(document.querySelector("[data-status-region]").textContent, /Moved to trash\. Budget 2025/);
});

test("blankslate accepts heading level 1 and clamps other levels", () => {
  assert.equal(ui.blankslate({ title: "Page not found", headingLevel: 1 }).querySelector(".blankslate-heading").tagName, "H1");
  assert.equal(ui.blankslate({ title: "Empty" }).querySelector(".blankslate-heading").tagName, "H2");
  assert.equal(ui.blankslate({ title: "Deep", headingLevel: 9 }).querySelector(".blankslate-heading").tagName, "H6");
});

test("openDialog position left opens a left side sheet and ignores the narrow position", () => {
  trigger("Open navigation");
  const handle = ui.openDialog({ title: "Navigation", position: "left", content: h("input", { type: "text" }) });
  assert.equal(handle.element.getAttribute("data-position"), "left");
  assert.equal(handle.element.getAttribute("data-narrow"), "left");
  assert.ok(handle.element.querySelector(".dialog-close"));
  handle.close();
  const centred = ui.openDialog({ title: "Details", content: "Text" });
  assert.equal(centred.element.getAttribute("data-position"), null);
  assert.equal(centred.element.getAttribute("data-narrow"), "bottom");
  centred.close();
});

test("treeView selection marks rows, survives update and keeps focus on the same row", () => {
  const items = () => [
    { id: "a", label: "Alpha", icon: "note" },
    { id: "b", label: "Beta", icon: "file-directory-fill", expanded: true, children: [{ id: "c", label: "Gamma", icon: "note" }] },
  ];
  const view = ui.treeView({ label: "Pages", items: items(), selection: "multiple" });
  document.body.appendChild(view.element);
  assert.equal(view.element.getAttribute("aria-multiselectable"), "true");
  const row = (id) => view.element.querySelector(`[role=treeitem][data-id=${id}]`);
  assert.equal(row("a").getAttribute("aria-selected"), "false");
  view.setSelection(["a", "c", "missing"]);
  assert.deepEqual(view.getSelection().sort(), ["a", "c"]);
  assert.equal(row("c").getAttribute("aria-selected"), "true");
  view.focusItem("c");
  view.update(items());
  same(active(), row("c"), "focus stays on the rebuilt row");
  assert.equal(row("c").getAttribute("tabindex"), "0");
  assert.equal(row("a").getAttribute("aria-selected"), "true", "selection survives update");
  view.update(items().slice(1));
  assert.deepEqual(view.getSelection(), ["c"], "removed rows leave the selection");
  view.setIcon("c", "file");
  const svg = row("c").querySelector(".tree-view-visual svg");
  assert.ok(svg.getAttribute("class").includes("octicon-file"));
  const single = ui.treeView({ label: "Folders", items: items(), selection: "single" });
  single.setSelection(["a", "b"]);
  assert.deepEqual(single.getSelection(), ["a"]);
  assert.equal(single.element.getAttribute("aria-multiselectable"), null);
  const plain = ui.treeView({ label: "Plain", items: items() });
  assert.equal(plain.element.querySelector("[role=treeitem]").getAttribute("aria-selected"), null);
});

test("treeView update keeps the tab stop on the last focused row when focus is elsewhere", () => {
  const items = [{ id: "a", label: "Alpha", current: true }, { id: "b", label: "Beta" }];
  const view = ui.treeView({ label: "Pages", items });
  document.body.appendChild(view.element);
  view.focusItem("b");
  trigger("Elsewhere");
  view.update(items);
  const b = view.element.querySelector("[data-id=b]");
  assert.equal(b.getAttribute("tabindex"), "0");
  assert.equal(view.element.querySelector("[data-id=a]").getAttribute("tabindex"), "-1");
  assert.notEqual(active(), b, "update does not take focus from outside the tree");
});

test("avatar without a name is anonymous with a person Octicon", () => {
  const anon = ui.avatar({ size: 32 });
  assert.ok(anon.classList.contains("avatar-anon"));
  assert.ok(anon.querySelector("svg").getAttribute("class").includes("octicon-person"));
  assert.equal(anon.getAttribute("aria-hidden"), "true");
  const named = ui.avatar({ name: "Ada Lovelace" });
  assert.equal(named.textContent, "AL");
});

test("hideTooltips hides the open tooltip and a tooltip does not open over an open menu", () => {
  const button = ui.iconButton({ icon: "star", label: "Star" });
  document.body.appendChild(button);
  const tip = button._tooltip;
  tip.show();
  assert.equal(tip.element.getAttribute("data-open"), "true");
  ui.hideTooltips();
  assert.equal(tip.element.getAttribute("data-open"), null);
  const menu = ui.openMenu(trigger("Menu"), [{ label: "Rename" }]);
  tip.show();
  assert.equal(tip.element.getAttribute("data-open"), null, "no tooltip over an open menu");
  menu.close();
  tip.show();
  assert.equal(tip.element.getAttribute("data-open"), "true");
  tip.hide();
});
