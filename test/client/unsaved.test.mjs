// Tests for the unsaved changes warning in static/app.js (docs/primer.md 6.1).
// app.js is a classic script that runs every page feature at load, so the
// test runs only its "Unsaved changes" section on the fake DOM. The section
// takes its globals as arguments for this purpose.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { installDOM, FakeEvent } from "./fakedom.mjs";

const source = readFileSync(new URL("../../internal/web/static/app.js", import.meta.url), "utf8");
const start = source.indexOf("/* ---------- Unsaved changes ---------- */");
const end = source.indexOf("/* ---------- Live events ---------- */");
assert.ok(start > 0 && end > start, "app.js has the Unsaved changes section");
const section = source.slice(start, end);

/** Builds a page with one data-unsaved form and runs the section on it. */
function setup({ href = "http://localhost/settings/profile" } = {}) {
  const { document } = installDOM();
  const assigned = [];
  const confirms = [];
  const loadUI = () => Promise.resolve({
    confirmDialog: (options) => {
      confirms.push(options);
      return Promise.resolve(false);
    },
  });
  // The page listens for clicks on the window. The fake DOM stops events at
  // the document, so the window stand-in sends click listeners there.
  const win = {
    location: { href, assign: (url) => assigned.push(url) },
    addEventListener: (type, fn, options) => (type === "click" ? document : window).addEventListener(type, fn, options),
  };
  const form = document.createElement("form");
  form.setAttribute("data-unsaved", "");
  const name = document.createElement("input");
  name.setAttribute("name", "name");
  name.value = "Ada";
  const hidden = document.createElement("input");
  hidden.setAttribute("type", "hidden");
  hidden.setAttribute("name", "action");
  hidden.value = "profile";
  form.append(hidden, name);
  document.body.appendChild(form);
  new Function("document", "window", "ui", section)(document, win, loadUI);
  return { document, form, name, assigned, confirms };
}

function edit(field, value, type = "input") {
  field.value = value;
  field.dispatchEvent(new FakeEvent(type, { bubbles: true }));
}

/** Reports whether the page asks before it unloads. */
function warnsOnUnload() {
  const event = new FakeEvent("beforeunload", { cancelable: true });
  window.dispatchEvent(event);
  return event.defaultPrevented;
}

function click(document, attrs) {
  const link = document.createElement("a");
  for (const [k, v] of Object.entries(attrs)) link.setAttribute(k, v);
  link.textContent = "Go";
  document.body.appendChild(link);
  const event = new FakeEvent("click", { bubbles: true, cancelable: true });
  link.dispatchEvent(event);
  return event;
}

const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

test("a changed form warns before unload until it is submitted", () => {
  const { form, name } = setup();
  assert.equal(warnsOnUnload(), false, "an untouched form does not warn");
  edit(name, "Grace");
  assert.equal(warnsOnUnload(), true, "an input event marks the form changed");
  edit(name, "Ada", "change");
  assert.equal(warnsOnUnload(), false, "values back at their baseline clear the mark");
  edit(name, "Grace", "change");
  assert.equal(warnsOnUnload(), true, "a change event marks the form changed");
  form.dispatchEvent(new FakeEvent("submit", { bubbles: true, cancelable: true }));
  assert.equal(warnsOnUnload(), false, "a submit clears the mark");
});

test("a submit that validation cancels keeps the warning", () => {
  const { document, form, name } = setup();
  document.addEventListener("submit", (event) => event.preventDefault(), true);
  edit(name, "Grace");
  form.dispatchEvent(new FakeEvent("submit", { bubbles: true, cancelable: true }));
  assert.equal(warnsOnUnload(), true);
});

test("a form without data-unsaved never warns", () => {
  const { document } = setup();
  const other = document.createElement("form");
  const field = document.createElement("input");
  field.setAttribute("name", "q");
  other.appendChild(field);
  document.body.appendChild(other);
  edit(field, "search text");
  assert.equal(warnsOnUnload(), false);
});

test("a form that saves through a script takes a new baseline on markspace:saved", () => {
  const { form, name } = setup();
  edit(name, "Grace");
  form.dispatchEvent(new FakeEvent("markspace:saved", { bubbles: true }));
  assert.equal(warnsOnUnload(), false);
  edit(name, "Ada");
  assert.equal(warnsOnUnload(), true, "the saved value is the new baseline");
});

test("a link to another page asks in a ConfirmationDialog and leaves on confirm", async () => {
  const { document, name, confirms, assigned } = setup();
  edit(name, "Grace");
  const event = click(document, { href: "/settings/tokens" });
  assert.equal(event.defaultPrevented, true, "the navigation waits for the answer");
  await settle();
  assert.equal(confirms.length, 1);
  assert.equal(confirms[0].title, "Discard unsaved changes?");
  assert.equal(confirms[0].confirmLabel, "Discard changes");
  assert.equal(confirms[0].cancelLabel, "Keep editing");
  assert.deepEqual(assigned, [], "nothing navigates before the answer");
  confirms[0].onConfirm();
  assert.deepEqual(assigned, ["http://localhost/settings/tokens"]);
  assert.equal(warnsOnUnload(), false, "the browser does not ask a second time");
});

test("links that stay on the page, open elsewhere or run their own action do not ask", async () => {
  const { document, name, confirms } = setup();
  edit(name, "Grace");
  for (const attrs of [
    { href: "#password" },
    { href: "/settings/profile#password" },
    { href: "/settings/tokens", target: "_blank" },
    { href: "/settings/tokens", download: "" },
    { href: "/admin/plugins?open=x", "data-dialog-open": "plugin-secrets-x" },
    { href: "https://example.com/elsewhere" },
  ]) {
    assert.equal(click(document, attrs).defaultPrevented, false, JSON.stringify(attrs));
  }
  // A link whose own handler already cancelled the click does not ask either.
  document.addEventListener("click", (event) => {
    if (event.target.getAttribute("data-confirm") !== null) event.preventDefault();
  }, true);
  click(document, { href: "/settings/tokens", "data-confirm": "Leave?" });
  await settle();
  assert.equal(confirms.length, 0);
});

test("a link does not ask while no form has changes", async () => {
  const { document, confirms } = setup();
  assert.equal(click(document, { href: "/settings/tokens" }).defaultPrevented, false);
  await settle();
  assert.equal(confirms.length, 0);
});
