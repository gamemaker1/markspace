// Tests for plugins.js: the shared UI vectors, rendering of every node
// type, escaping, limits, form values and validation, confirm buttons, and
// the ui, toast and error responses of actions. They run on the fake DOM in
// fakedom.mjs.
import { test, beforeEach } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { installDOM, FakeEvent } from "./fakedom.mjs";

installDOM();
const { renderPluginUI, validatePluginUI } = await import("../../internal/web/static/js/plugins.js");

const repo = new URL("../../", import.meta.url);
const protocol = JSON.parse(readFileSync(new URL("test/vectors/protocol.json", repo), "utf8"));
const serverVectors = JSON.parse(readFileSync(new URL("internal/module/testdata/ui-tree.json", repo), "utf8"));

const sleep = (ms = 0) => new Promise((resolve) => setTimeout(resolve, ms));
const click = (el) => el.dispatchEvent(new FakeEvent("click", { bubbles: true }));
const key = (el, name) => el.dispatchEvent(new FakeEvent("keydown", { bubbles: true, key: name }));
const submit = (form) => form.dispatchEvent(new FakeEvent("submit", { bubbles: true }));
const texts = (els) => els.map((el) => el.textContent);

beforeEach(() => {
  installDOM({ focusEvents: true });
});

/** Adds a container to the page and renders tree into it. */
function mount(tree, options = {}) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const handle = renderPluginUI(container, tree, options);
  return { container, handle };
}

/* ---------- Vectors ---------- */

const many = (n, make) => Array.from({ length: n }, make);

/** Builds the generated cases named in both vector files. */
function generate({ kind, n }) {
  switch (kind) {
    case "wide":
      return { type: "stack", children: many(n, () => ({ type: "text", value: "x" })) };
    case "wideUnknown":
      return { type: "stack", children: many(n, () => ({ type: "nope" })) };
    case "wideList":
      return { type: "list", items: many(n, () => ({ title: "x" })) };
    case "wideTable":
      return { type: "table", columns: [{ key: "a", label: "A" }], rows: many(n, () => ({ a: 1 })) };
    case "buttons":
    case "wideButtons":
      return { type: "stack", children: many(n, () => ({ type: "button", label: "b", action: { id: "a" } })) };
    case "selectOptions":
      return { type: "form", id: "f", action: { id: "a" }, fields: [{ type: "select", name: "s", options: many(n, () => ({ value: "v", label: "V" })) }] };
    case "deep": {
      let node = { type: "stack", children: [] };
      for (let i = 1; i < n; i++) node = { type: "stack", children: [node] };
      return node;
    }
    case "deepPayload": {
      let payload = {};
      for (let i = 1; i < n; i++) payload = { a: payload };
      return { type: "button", label: "b", action: { id: "a", payload } };
    }
    case "bigText":
    case "bigTextIndented":
      return { type: "text", value: "a".repeat(n) };
    case "bigUnknown":
      return { type: "nope", value: "a".repeat(n) };
  }
  throw new Error("unknown generator " + kind);
}

const pointerOf = (segments) => segments.map((s) => "/" + String(s).replace(/~/g, "~0").replace(/\//g, "~1")).join("");

/**
 * Finds the first repeated key the way the server's structure pass meets it:
 * an object's own keys are checked before its values are visited. JSON.parse
 * keeps the last value silently, so the raw vectors need this.
 */
function duplicateKey(text) {
  let i = 0;
  const ws = () => {
    while (i < text.length && " \t\r\n".includes(text[i])) i++;
  };
  const readString = () => {
    const start = i++;
    while (text[i] !== '"') i += text[i] === "\\" ? 2 : 1;
    i++;
    return JSON.parse(text.slice(start, i));
  };
  const skip = () => {
    ws();
    const c = text[i];
    if (c === '"') {
      readString();
      return;
    }
    if (c === "{" || c === "[") {
      const close = c === "{" ? "}" : "]";
      i++;
      ws();
      if (text[i] === close) {
        i++;
        return;
      }
      for (;;) {
        if (c === "{") {
          ws();
          readString();
          ws();
          i++;
        }
        skip();
        ws();
        if (text[i++] === close) return;
      }
    }
    while (i < text.length && !",]} \t\r\n".includes(text[i])) i++;
  };
  const visit = (start, path) => {
    i = start;
    ws();
    const children = [];
    if (text[i] === "{") {
      i++;
      ws();
      const seen = new Set();
      if (text[i] !== "}") {
        for (;;) {
          ws();
          const name = readString();
          ws();
          i++;
          ws();
          if (seen.has(name)) return [...path, name];
          seen.add(name);
          children.push([name, i]);
          skip();
          ws();
          if (text[i++] === "}") break;
        }
      }
    } else if (text[i] === "[") {
      i++;
      ws();
      if (text[i] !== "]") {
        for (let n = 0; ; n++) {
          ws();
          children.push([n, i]);
          skip();
          ws();
          if (text[i++] === "]") break;
        }
      }
    }
    for (const [name, at] of children) {
      const found = visit(at, [...path, name]);
      if (found) return found;
    }
    return null;
  };
  const found = visit(0, []);
  return found ? pointerOf(found) : null;
}

/** The verdict for one vector case, from raw text, a generator or a tree. */
function verdict(c) {
  if (c.raw !== undefined) {
    let value;
    try {
      value = JSON.parse(c.raw);
    } catch {
      return { ok: false, code: "invalid_json", pointer: "", path: "$" };
    }
    const dup = duplicateKey(c.raw);
    if (dup !== null) return { ok: false, code: "duplicate_key", pointer: dup };
    return validatePluginUI(value);
  }
  return validatePluginUI(c.generate ? generate(c.generate) : c.tree);
}

function assertVerdict(got, want, name) {
  if (want === null) {
    assert.deepEqual(got, { ok: true }, name);
    return;
  }
  assert.equal(got.ok, false, `${name}: want ${want.code}`);
  assert.equal(got.code, want.code, name);
  if (want.path !== undefined && got.path !== undefined) assert.equal(got.path, want.path, name);
  if (want.pointer !== undefined) assert.equal(got.pointer, want.pointer, name);
}

test("every UI case in test/vectors/protocol.json gets the server verdict", async (t) => {
  assert.ok(protocol.ui.cases.length > 100);
  assert.deepEqual(protocol.ui.limits, { maxNodes: 2000, maxBytes: 1048576, maxDepth: 32 });
  for (const c of protocol.ui.cases) {
    await t.test(c.name, () => assertVerdict(verdict(c), c.error, c.name));
  }
});

test("the ui command responses in test/vectors/protocol.json get the server verdict", async (t) => {
  const cases = protocol.commandResponses.cases.filter((c) => c.response && typeof c.response === "object" && Object.keys(c.response).join() === "ui");
  assert.ok(cases.length >= 3);
  for (const c of cases) {
    await t.test(c.name, () => {
      const want = c.error && { code: c.error.code, path: c.error.path.replace(/^\$\.ui/, "$") };
      assertVerdict(validatePluginUI(c.response.ui), want, c.name);
    });
  }
});

test("every case in internal/module/testdata/ui-tree.json gets the server verdict", async (t) => {
  assert.ok(serverVectors.cases.length >= 20);
  for (const c of serverVectors.cases) {
    await t.test(c.name, () => assertVerdict(verdict(c), c.error, c.name));
  }
});

test("link URLs follow ValidLinkURL", () => {
  // Verdicts from internal/module.ValidLinkURL.
  const want = {
    "http://[::1]:8080/": true, "http://[::ffff:1.2.3.4]/": true, "http://[127.0.0.1]/": false, "http://[fe80::1%25en0]/": true,
    "http://[fe80::1%25]/": false, "http://[::1": false, "http://a:b:c/": false, "http://a:80/": true, "https://example.com:/": true,
    "http://:80/": false, "http://%41/": false, "http://%C3%A9/": true, "http://%25/": true, "https://ex.com/%zz": false,
    "https://ex.com/#%zz": false, "https://ex.com/?x=%zz": true, "https://ex.com/#frag": true, "http://exa mple.com": false,
    "http://ex﻿ample.com": true, "HTTP://EXAMPLE.COM": true, "http:/example.com": false, "http:///example.com": false,
    "http://ex{ample.com/": false, "http://ex|ample.com/": false, "http://[1:2:3:4:5:6:7:8]/": true, "http://[1:2:3:4:5:6:7:8:9]/": false,
    "http://[1::2::3]/": false, "http://[::]/": true, "http://[:1]/": false, "http://[1:2:3:4:5:6:1.2.3.4]/": true,
    "http://[1:2:3:4:5:1.2.3.4]/": false, "http://[::01.2.3.4]/": false, "http://[12345::]/": false, "http://a[b]/": false,
    "http://example.com\u2028/": false, "https://é.example/": true, "http://1.2.3.4/": true, "http://example.com?@evil": true,
    "http://example.com#@evil": true, "http://user@example.com": false, "http://ex%2Fample.com": false, "https://example.com/a%20b": true,
    "http://[::1]:x/": false, "http://[::1]]/": false, "http://[fe80::1%25%41]/": true, "http://[fe80::1%25%zz]/": false,
    "http://[::1%25en0]:80/": true, "ftp://example.com": false, "http://-/": true, "http://a_b.example/": true,
    "https://example.com:65536/": true, "http://[1:2::3:4:5:6:7:8]/": false, "http://[::1.2.3.4]/": true,
    "http://[1:2:3:4:5:6:7::]/": true, "http://[::1:2:3:4:5:6:7:8]/": false, "http:example.com/x": false,
  };
  for (const [url, ok] of Object.entries(want)) {
    const got = validatePluginUI({ type: "link", label: "x", to: { url } });
    assert.equal(got.ok, ok, `${JSON.stringify(url)}: ${JSON.stringify(got)}`);
    if (!ok) assert.equal(got.code, "invalid_url", url);
  }
});

test("values that are not JSON are invalid_json at the root", () => {
  for (const tree of [{ type: "progress", value: NaN }, { type: "progress", value: Infinity }, { type: "text", value: () => "x" }, { type: "text", value: "\uD800" }, undefined]) {
    assert.deepEqual(validatePluginUI(tree), { ok: false, code: "invalid_json", pointer: "", path: "$", message: validatePluginUI(tree).message });
  }
  // Members that are undefined are absent, as in the JSON encoding.
  assert.deepEqual(validatePluginUI({ type: "section", title: "a", description: undefined }), { ok: true });
});

test("size counts U+2028 and U+2029 as Go encodes them", () => {
  // The UTF-8 size is exactly 1 MiB, and Go writes the separator as \u2028.
  const tree = { type: "text", value: "a".repeat(1048547) + "\u2028" };
  assert.equal(validatePluginUI(tree).code, "too_large");
  assert.deepEqual(validatePluginUI({ type: "text", value: "a".repeat(1048544) + "\u2029" }), { ok: true });
});

/* ---------- Rendering ---------- */

const everyNodeType = protocol.ui.cases.find((c) => c.name === "every node type").tree;

test("renders every node type with Primer components", () => {
  const { container } = mount(everyNodeType);
  const root = container.querySelector(".plugin-ui");
  assert.ok(root.firstChild.classList.contains("stack"));

  const section = container.querySelector("section.plugin-ui-section");
  const heading = section.querySelector("h2.section-heading-title");
  assert.equal(heading.firstChild.textContent, "Counts");
  assert.equal(heading.querySelector(".counter-label").textContent, "3(3)");
  assert.equal(section.getAttribute("aria-labelledby"), heading.id);
  assert.equal(section.querySelector(".section-heading-description").textContent, "Words in this note");
  assert.equal(section.querySelector("p.plugin-ui-text").textContent, "412 words");

  const md = section.querySelector(".plugin-ui-markdown");
  assert.match(md.innerHTML, /<strong>bold<\/strong> &lt;b&gt;escaped&lt;\/b&gt;/);
  const codes = section.querySelectorAll(".plugin-ui-code");
  assert.equal(codes.length, 2);
  assert.equal(codes[0].getAttribute("role"), "region");
  assert.equal(codes[0].getAttribute("tabindex"), "0");
  assert.equal(codes[0].getAttribute("aria-label"), "Code");
  assert.equal(codes[0].querySelector("pre code").className, "language-python");
  assert.equal(codes[0].textContent, "print(1)");
  assert.equal(codes[1].querySelector("code").className, "");

  const form = container.querySelector("form.plugin-ui-form");
  assert.ok(form.hasAttribute("novalidate"));
  assert.equal(form.querySelector("h2.section-heading-title").textContent, "Import");
  const title = form.querySelector("input[name='title']");
  assert.equal(title.type, "text");
  assert.ok(title.required);
  assert.ok(title.closest(".form-control").querySelector(".form-required"));
  assert.equal(title.closest(".form-control").querySelector(".form-caption").textContent, "Shown in the tree");
  assert.equal(form.querySelector("textarea[name='body']").tagName, "TEXTAREA");
  assert.equal(form.querySelector("input[name='limit']").type, "number");
  assert.equal(form.querySelector("input[name='limit']").value, "10");
  assert.equal(form.querySelector("input[name='url']").type, "url");
  const radios = form.querySelectorAll("input[name='mode']");
  assert.deepEqual(radios.map((r) => [r.type, r.value, r.checked]), [["radio", "fast", true], ["radio", "full", false]]);
  assert.equal(radios[0].closest("fieldset").querySelector("legend").textContent, "mode");
  assert.equal(form.querySelector("input[name='robots']").checked, true);
  const file = form.querySelector("input[name='file']");
  assert.equal(file.type, "file");
  assert.ok(file.hidden);
  assert.equal(file.closest(".form-control").querySelector(".btn").textContent, "Choose file");
  for (const name of ["note", "folder", "space"]) assert.ok(form.querySelector(`input[name='${name}']`), name);
  const more = form.querySelector("details");
  assert.equal(more.querySelector("summary").textContent, "More options");
  assert.ok(more.querySelector("input[name='concurrency']"));
  const submitButton = form.children[form.children.length - 1];
  assert.equal(submitButton.getAttribute("type"), "submit");
  assert.ok(submitButton.classList.contains("btn-primary"));
  assert.equal(submitButton.textContent, "Import");

  const rows = container.querySelectorAll("ul.list-rows li.list-row");
  assert.equal(rows.length, 2);
  const itemLink = rows[0].querySelector("a");
  assert.equal(itemLink.getAttribute("href"), "/i/n1");
  assert.equal(itemLink.textContent, "SQLite");
  assert.equal(rows[0].querySelector(".ui-label-accent").textContent, "new");
  assert.ok(rows[0].querySelector(".plugin-ui-item-icon svg.octicon-book"), "book is a vendored Octicon, so the item shows it");
  assert.equal(rows[0].querySelector(".btn.btn-sm").textContent, "Count");
  assert.ok(rows[1].classList.contains("no-lead"));
  assert.equal(rows[1].querySelector("a").getAttribute("rel"), "noopener noreferrer");
  assert.equal(rows[1].querySelector("a").getAttribute("href"), "https://example.com/a");

  const table = container.querySelector(".plugin-ui-table table.data-table");
  assert.deepEqual(texts(table.querySelectorAll("thead th")), ["Name", "Words", "Actions"]);
  assert.ok(table.querySelectorAll("thead th")[1].classList.contains("col-num"));
  assert.deepEqual(texts(table.querySelectorAll("tbody tr")[1].querySelectorAll("td")).slice(0, 2), ["b", "2.5"]);
  assert.equal(table.querySelector("tbody .col-actions button").getAttribute("aria-label"), "Actions: a");

  const bars = container.querySelectorAll("[role='progressbar']");
  assert.equal(bars[0].getAttribute("aria-valuenow"), "30");
  assert.equal(bars[0].getAttribute("aria-valuetext"), "3 of 10");
  assert.equal(bars[0].getAttribute("aria-label"), "Counting");
  assert.equal(bars[1].getAttribute("aria-valuetext"), "0%");

  assert.deepEqual(texts(root.firstChild.children.filter((el) => el.classList.contains("ui-label"))), ["ok", "bad"]);
  assert.ok(root.querySelector(".ui-label-default"));
  assert.ok(root.querySelector(".ui-label-danger"));
  const danger = root.firstChild.children.find((el) => el.classList.contains("btn-danger"));
  assert.equal(danger.textContent, "Delete");
  const links = root.firstChild.children.filter((el) => el.tagName === "A");
  assert.equal(links[0].getAttribute("href"), "/i/n1");
  assert.equal(links[1].getAttribute("href"), "http://example.com");
  assert.equal(links[1].getAttribute("rel"), "noopener noreferrer");

  const slates = root.firstChild.children.filter((el) => el.classList.contains("blankslate"));
  assert.equal(slates.length, 2);
  assert.equal(slates[0].querySelector(".blankslate-heading").textContent, "Nothing yet");
  assert.equal(slates[0].querySelector(".blankslate-description").textContent, "Run a count.");
  assert.ok(slates[0].querySelector(".blankslate-action .btn-primary"));
  assert.equal(slates[1].querySelector(".blankslate-heading").textContent, "Nothing");
});

test("stack gaps, nested section levels and empty lists and tables", () => {
  const { container } = mount({
    type: "stack",
    gap: "s",
    children: [
      { type: "section", title: "Outer", children: [{ type: "section", title: "Inner" }] },
      { type: "list", items: [], empty: "No pages yet" },
      { type: "table", columns: [{ key: "a", label: "A" }], rows: [] },
      { type: "stack", gap: "l", children: [] },
    ],
  });
  const stack = container.querySelector(".plugin-ui-stack");
  assert.equal(stack.style.getPropertyValue("gap"), "var(--stack-gap-condensed)");
  assert.equal(container.querySelectorAll(".plugin-ui-stack")[1].style.getPropertyValue("gap"), "var(--stack-gap-spacious)");
  assert.ok(container.querySelector("h2.section-heading-title"));
  assert.equal(container.querySelector("h3.section-heading-title").textContent, "Inner");
  const slates = container.querySelectorAll(".blankslate");
  assert.equal(slates[0].querySelector(".blankslate-heading").textContent, "No pages yet");
  assert.ok(slates[0].classList.contains("blankslate-border"));
  assert.equal(slates[1].querySelector(".blankslate-heading").textContent, "No rows");
  assert.equal(container.querySelector("table"), null);
});

test("unknown icons are ignored and a select with more than 6 options is a Select", () => {
  const options = many(7, (_, i) => ({ value: "v" + i, label: "Option " + i }));
  const { container } = mount({
    type: "stack",
    children: [
      { type: "list", items: [{ title: "a", icon: "no-such-icon" }] },
      { type: "list", items: [{ title: "b", icon: "hash" }] },
      { type: "form", id: "f", action: { id: "a" }, fields: [{ type: "select", name: "s", options }, { type: "select", name: "t", value: "v2", options }] },
    ],
  });
  const icons = container.querySelectorAll(".plugin-ui-item-icon");
  assert.equal(icons.length, 1, "the unknown icon is ignored");
  assert.equal(icons[0].closest("li").textContent, "b");
  assert.ok(icons[0].querySelector("svg"));
  const selects = container.querySelectorAll("select");
  assert.equal(selects.length, 2);
  assert.equal(selects[0].querySelectorAll("option")[0].textContent, "Choose an option");
  assert.equal(selects[1].querySelectorAll("option").length, 7);
});

test("plugin text is escaped everywhere", () => {
  const evil = "<img src=x onerror=alert(1)><script>alert(2)</script>";
  const { container } = mount({
    type: "stack",
    children: [
      { type: "section", title: evil, description: evil, children: [{ type: "text", value: evil }] },
      { type: "markdown", value: evil + "\n\n[x](javascript:alert(3))" },
      { type: "code", value: evil, language: "js onclick=x" },
      { type: "badge", label: evil },
      { type: "button", label: evil, action: { id: "a" } },
      { type: "link", label: evil, to: { itemId: "n1" } },
      { type: "list", items: [{ title: evil, subtitle: evil, badge: { label: evil } }] },
      { type: "table", columns: [{ key: "a", label: evil }], rows: [{ a: evil }] },
      { type: "empty", title: evil, body: evil },
      { type: "form", id: "f", title: evil, action: { id: "a" }, fields: [{ type: "text", name: "n", label: evil, hint: evil, value: evil }] },
    ],
  });
  assert.equal(container.querySelectorAll("img").length, 0);
  assert.equal(container.querySelectorAll("script").length, 0);
  assert.ok(container.textContent.split(evil).length > 10);
  const md = container.querySelector(".plugin-ui-markdown").innerHTML;
  assert.ok(!md.includes("<img") && !md.includes("<script"), md);
  assert.ok(!md.includes("javascript:"), md);
  assert.equal(container.querySelector(".plugin-ui-code code").className, "", "a language with spaces adds no classes");
});

test("an invalid tree renders an InlineMessage with the pointer and nothing else", () => {
  const { container } = mount({ type: "stack", children: [{ type: "text", value: "shown?" }, { type: "iframe", src: "https://evil.example" }] });
  const message = container.querySelector(".inline-message-critical");
  assert.ok(message);
  assert.match(message.textContent, /unknown_type at \/children\/1/);
  assert.equal(container.querySelector(".plugin-ui-text"), null);
  assert.ok(!container.textContent.includes("shown?"));

  const { container: second } = mount({ type: "link", label: "x", to: { url: "javascript:alert(1)" } });
  assert.match(second.textContent, /invalid_url at \/to\/url/);
  assert.equal(second.querySelector("a"), null);
  const { container: third } = mount({ type: "text", value: "a", html: "<b>" });
  assert.match(third.textContent, /unknown_field at \/html/);
});

test("limits: 2000 nodes, depth 32 and 1 MiB", () => {
  const cases = [
    [generate({ kind: "wide", n: 1999 }), null],
    [generate({ kind: "wide", n: 2000 }), "too_many_nodes at /children/1999"],
    [generate({ kind: "deep", n: 32 }), null],
    [generate({ kind: "deep", n: 33 }), "too_deep at /children/0"],
    [generate({ kind: "bigText", n: 1048550 }), null],
    [generate({ kind: "bigText", n: 1048551 }), "too_large at /"],
  ];
  for (const [tree, error] of cases) {
    const { container, handle } = mount(tree);
    const message = container.querySelector(".inline-message-critical");
    if (error === null) {
      assert.equal(message, null);
    } else {
      assert.ok(message && message.textContent.includes(error), `${error}: ${message && message.textContent}`);
    }
    handle.destroy();
    assert.equal(container.children.length, 0);
  }
});

/* ---------- Forms ---------- */

function valuesForm(extra = {}) {
  return {
    type: "form",
    id: "settings",
    submit: "Save",
    action: { id: "save", payload: { spaceId: "s1" } },
    fields: [
      { type: "text", name: "title", label: "Title", value: "Old" },
      { type: "textarea", name: "body", label: "Body" },
      { type: "number", name: "limit", label: "Limit" },
      { type: "number", name: "empty", label: "Empty number" },
      { type: "url", name: "site", label: "Site" },
      { type: "checkbox", name: "robots", label: "Robots" },
      { type: "checkbox", name: "off", label: "Off", value: true },
      { type: "select", name: "mode", label: "Mode", options: [{ value: "fast", label: "Fast" }, { value: "full", label: "Full" }] },
      { type: "select", name: "long", label: "Long", options: many(8, (_, i) => ({ value: "v" + i, label: "V" + i })) },
      { type: "file", name: "upload", label: "Upload" },
      { type: "note", name: "note", label: "Note" },
    ],
    more: [{ type: "number", name: "concurrency", label: "Concurrency", value: 4 }],
    ...extra,
  };
}

test("a form collects typed values by field name and passes the action payload", async () => {
  const calls = [];
  let release;
  const { container } = mount(valuesForm(), {
    context: { spaceId: "s1" },
    upload: async (path, chosen) => ({ id: "f1", name: chosen.name }),
    onAction: (id, payload, values) => {
      calls.push({ id, payload, values });
      return new Promise((resolve) => {
        release = resolve;
      });
    },
  });
  const form = container.querySelector("form");
  const q = (sel) => form.querySelector(sel);
  q("input[name='title']").value = "New title";
  q("textarea[name='body']").value = "Line 1\nLine 2";
  q("input[name='limit']").value = "12.5";
  q("input[name='site']").value = "https://example.com/a";
  q("input[name='robots']").checked = true;
  q("input[name='off']").checked = false;
  form.querySelectorAll("input[name='mode']")[1].checked = true;
  q("select[name='long']").value = "v3";
  const upload = q("input[name='upload']");
  const file = new File(["hello"], "notes.txt", { type: "text/plain" });
  upload.files = [file];
  upload.dispatchEvent(new FakeEvent("change"));
  assert.equal(upload.closest(".form-control").querySelector(".form-caption").textContent, "notes.txt");
  q("input[name='note']").value = "01j9z3k5q8r2m4n6p7s8t9v0w1";

  submit(form);
  await sleep();
  assert.equal(calls.length, 1);
  assert.equal(calls[0].id, "save");
  assert.deepEqual(calls[0].payload, { spaceId: "s1" });
  const { upload: sent, ...rest } = calls[0].values;
  assert.deepEqual(sent, { fileId: "f1", name: "notes.txt", size: 5, type: "text/plain" }, "the chosen file is uploaded and sent as its record");
  assert.deepEqual(rest, {
    title: "New title",
    body: "Line 1\nLine 2",
    limit: 12.5,
    empty: null,
    site: "https://example.com/a",
    robots: true,
    off: false,
    mode: "full",
    long: "v3",
    note: "01j9z3k5q8r2m4n6p7s8t9v0w1",
    concurrency: 4,
  });

  const button = form.querySelector("button[type='submit']");
  assert.equal(button.getAttribute("aria-busy"), "true");
  submit(form);
  await sleep();
  assert.equal(calls.length, 1, "a busy form does not submit twice");
  release({ toast: { message: "Saved", kind: "success" } });
  await sleep();
  assert.equal(button.getAttribute("aria-busy"), null);
});

test("required fields and bad numbers stop the submit before onAction", async () => {
  let calls = 0;
  const { container } = mount(
    {
      type: "form",
      id: "f",
      action: { id: "a" },
      fields: [
        { type: "text", name: "title", label: "Title", required: true },
        { type: "number", name: "limit", label: "Limit" },
      ],
      more: [{ type: "file", name: "attachment", label: "Attachment", required: true }],
    },
    { onAction: () => calls++, context: { spaceId: "s1" }, upload: async (path, chosen) => ({ id: "f2", name: chosen.name }) }
  );
  const form = container.querySelector("form");
  form.querySelector("input[name='limit']").value = "12abc";
  submit(form);
  await sleep();
  assert.equal(calls, 0);
  const banner = form.querySelector("[data-form-banner]");
  assert.ok(banner, "three errors show an error summary");
  assert.equal(banner.querySelectorAll("li").length, 3);
  assert.ok(form.querySelector("details").open, "an error in more options opens the disclosure");

  form.querySelector("input[name='limit']").value = "7";
  form.querySelector("input[name='attachment']").files = [new File(["x"], "a.txt")];
  submit(form);
  await sleep();
  assert.equal(calls, 0);
  const title = form.querySelector("input[name='title']");
  assert.equal(title.getAttribute("aria-invalid"), "true");
  assert.match(title.closest(".form-control").querySelector(".form-validation").textContent, /Title is required/);
  assert.equal(document.activeElement, title);

  title.value = "Now set";
  submit(form);
  await sleep();
  assert.equal(calls, 1);
  assert.equal(title.getAttribute("aria-invalid"), null);
});

test("a response or rejection shaped {error, field} marks the field", async () => {
  const answers = [
    { error: "That name is taken", field: "title" },
    Object.assign(new Error("Enter a shorter body"), { field: "body" }),
    { error: "The plugin is paused" },
  ];
  const { container } = mount(valuesForm({ fields: [{ type: "text", name: "title", label: "Title" }, { type: "text", name: "body", label: "Body" }], more: undefined }), {
    onAction: () => {
      const next = answers.shift();
      return next instanceof Error ? Promise.reject(next) : Promise.resolve(next);
    },
  });
  const form = container.querySelector("form");
  const wrapper = (name) => form.querySelector(`input[name='${name}']`).closest(".form-control");

  submit(form);
  await sleep();
  assert.ok(wrapper("title").classList.contains("has-error"));
  assert.match(wrapper("title").textContent, /That name is taken/);
  assert.equal(document.activeElement, form.querySelector("input[name='title']"));

  submit(form);
  await sleep();
  assert.ok(!wrapper("title").classList.contains("has-error"));
  assert.match(wrapper("body").textContent, /Enter a shorter body/);

  submit(form);
  await sleep();
  const banner = form.querySelector("[data-form-banner]");
  assert.ok(banner && banner.classList.contains("banner-critical"));
  assert.match(banner.textContent, /The plugin is paused/);
});

test("a file field focuses its button after an error from the server", async () => {
  const { container } = mount({ type: "form", id: "f", action: { id: "a" }, fields: [{ type: "file", name: "doc", label: "Document" }] }, { onAction: async () => ({ error: "The file is too large", field: "doc" }) });
  const form = container.querySelector("form");
  submit(form);
  await sleep();
  const wrapper = form.querySelector("input[name='doc']").closest(".form-control");
  assert.match(wrapper.textContent, /The file is too large/);
  assert.equal(document.activeElement, wrapper.querySelector(".btn"));
});

test("note and space fields use context.lookup and send the chosen id", async () => {
  const queries = [];
  const calls = [];
  const { container } = mount(
    { type: "form", id: "f", action: { id: "a" }, fields: [{ type: "note", name: "note", label: "Note", required: true }] },
    {
      onAction: async (id, payload, values) => calls.push(values),
      context: {
        lookup: async (kind, query) => {
          queries.push([kind, query]);
          return [{ id: "n42", label: "Harbour survey", description: "field-notes/harbour" }];
        },
      },
    }
  );
  const form = container.querySelector("form");
  const hidden = form.querySelector("input[name='note']");
  assert.equal(hidden.type, "hidden");
  const input = hidden.closest(".form-control").querySelector("input[role='combobox']");
  assert.ok(input);
  input.value = "Harb";
  submit(form);
  await sleep();
  assert.equal(calls.length, 0, "typed text that was not chosen is refused");
  assert.match(hidden.closest(".form-control").textContent, /Choose a note from the list/);

  input.value = "Harb";
  input.dispatchEvent(new FakeEvent("input", { bubbles: true }));
  await sleep(10);
  assert.deepEqual(queries[queries.length - 1], ["note", "Harb"]);
  const option = document.querySelector("[role='option']");
  assert.ok(option, "the listbox shows the lookup result");
  click(option);
  await sleep();
  assert.equal(input.value, "Harbour survey");
  submit(form);
  await sleep();
  assert.deepEqual(calls, [{ note: "n42" }]);
});

/* ---------- Buttons and responses ---------- */

test("a button with confirm opens a ConfirmationDialog before it runs", async () => {
  const calls = [];
  const { container } = mount({ type: "button", label: "Delete", variant: "danger", confirm: "Delete every count?", action: { id: "delete", payload: { all: true } } }, { onAction: async (...args) => calls.push(args) });
  const trigger = container.querySelector(".btn-danger");
  click(trigger);
  let dialog = document.querySelector("dialog");
  assert.equal(dialog.getAttribute("role"), "alertdialog");
  assert.match(dialog.textContent, /Delete every count\?/);
  let [cancel, confirm] = dialog.querySelectorAll(".dialog-footer .btn");
  assert.equal(cancel.textContent, "Cancel");
  assert.equal(confirm.textContent, "Delete");
  assert.ok(confirm.classList.contains("btn-danger"));
  assert.equal(calls.length, 0);
  key(document.activeElement, "Escape");
  await sleep();
  assert.equal(calls.length, 0, "cancel does not run the action");

  click(trigger);
  dialog = document.querySelector("dialog");
  [cancel, confirm] = dialog.querySelectorAll(".dialog-footer .btn");
  click(confirm);
  await sleep();
  await sleep();
  assert.deepEqual(calls, [["delete", { all: true }, {}]]);
  assert.equal(dialog.isConnected, false);
});

test("a button without confirm is busy while the action runs", async () => {
  let release;
  const calls = [];
  const { container } = mount({ type: "button", label: "Count", action: { id: "count" } }, {
    onAction: (...args) => {
      calls.push(args);
      return new Promise((resolve) => {
        release = resolve;
      });
    },
  });
  const btn = container.querySelector(".btn");
  assert.ok(!btn.classList.contains("btn-primary"), "secondary maps to Primer's default button");
  click(btn);
  await sleep();
  assert.equal(btn.getAttribute("aria-busy"), "true");
  click(btn);
  assert.equal(calls.length, 1);
  release({ navigate: "/s/docs" });
  await sleep();
  assert.equal(btn.getAttribute("aria-busy"), null);
  assert.equal(container.querySelector(".banner"), null, "navigate is left to the host");
});

test("a ui response replaces the tree through update", async () => {
  const { container } = mount({ type: "stack", children: [{ type: "text", value: "Before" }, { type: "button", label: "Refresh", action: { id: "refresh" } }] }, {
    onAction: async () => ({ ui: { type: "section", title: "After", children: [{ type: "badge", label: "fresh", tone: "good" }] } }),
  });
  const btn = container.querySelector(".btn");
  btn.focus();
  click(btn);
  await sleep();
  await sleep();
  assert.ok(!container.textContent.includes("Before"));
  assert.equal(container.querySelector(".section-heading-title").textContent, "After");
  assert.ok(container.querySelector(".ui-label-success"));
  assert.equal(document.activeElement, container.querySelector(".plugin-ui"), "focus stays in the plugin UI");
  assert.equal(container.querySelectorAll(".plugin-ui").length, 1);
});

test("an invalid ui response shows the error, not the old tree", async () => {
  const { container } = mount({ type: "button", label: "Refresh", action: { id: "refresh" } }, { onAction: async () => ({ ui: { type: "stack", children: [{ type: "nope" }] } }) });
  click(container.querySelector(".btn"));
  await sleep();
  await sleep();
  assert.equal(container.querySelector(".btn"), null);
  assert.match(container.textContent, /unknown_type at \/children\/0/);
});

test("a toast response shows a Banner in the container and is returned unchanged", async () => {
  const responses = [
    { toast: { message: "13 words in Harbour survey", kind: "info" } },
    { toast: { message: "Counted", kind: "success" } },
    { toast: { message: "The count failed", kind: "error" } },
    { job: { id: "01j9z3k5q8r2m4n6p7s8t9v0w1" } },
  ];
  const seen = [];
  const { container, handle } = mount({ type: "button", label: "Count", action: { id: "count" } }, {
    onAction: async () => {
      const r = responses.shift();
      seen.push(r);
      return r;
    },
  });
  const btn = container.querySelector(".btn");
  click(btn);
  await sleep();
  let banner = container.firstChild;
  assert.ok(banner.classList.contains("banner-info"));
  assert.equal(banner.querySelector(".banner-description").textContent, "13 words in Harbour survey");
  assert.ok(banner.querySelector(".banner-title").classList.contains("sr-only"));
  assert.ok(banner.querySelector(".banner-dismiss"));
  assert.ok(container.querySelector(".plugin-ui .btn"), "the tree stays");

  click(btn);
  await sleep();
  assert.equal(container.querySelectorAll(".banner").length, 1, "a new Banner replaces one of the same or lower severity");
  assert.ok(container.firstChild.classList.contains("banner-success"));

  click(btn);
  await sleep();
  banner = container.firstChild;
  assert.ok(banner.classList.contains("banner-critical"));
  assert.equal(banner.querySelector(".banner-dismiss"), null);

  click(btn);
  await sleep();
  assert.equal(container.querySelectorAll(".banner").length, 1, "job is left to the host");
  assert.equal(seen.length, 4);

  handle.destroy();
  assert.equal(container.children.length, 0, "destroy removes the tree and its Banners");
});

test("a failed button action shows a critical Banner", async () => {
  const { container } = mount({ type: "button", label: "Count", action: { id: "count" } }, { onAction: async () => Promise.reject(new Error("The plugin host could not complete the action.")) });
  click(container.querySelector(".btn"));
  await sleep();
  const banner = container.querySelector(".banner-critical");
  assert.ok(banner);
  assert.match(banner.textContent, /could not complete the action/);
});

test("row actions sit in an ActionMenu and pass the row as values", async () => {
  const calls = [];
  const { container } = mount(
    {
      type: "table",
      columns: [{ key: "name", label: "Name" }, { key: "words", label: "Words", numeric: true }],
      rows: [{ name: "Harbour", words: 13, id: "n1" }],
      rowActions: [
        { type: "button", label: "Remove", variant: "danger", confirm: "Remove this row?", action: { id: "remove" } },
        { type: "button", label: "Open", action: { id: "open", payload: { from: "table" } } },
      ],
    },
    { onAction: async (...args) => calls.push(args) }
  );
  const trigger = container.querySelector(".col-actions button");
  assert.equal(trigger.getAttribute("aria-label"), "Actions: Harbour");
  assert.equal(container.querySelector("td.col-num").textContent, "13");
  click(trigger);
  const items = document.querySelectorAll("[role='menu'] .action-list-item");
  assert.deepEqual(texts(items), ["Open", "Remove"], "danger items go last");
  assert.ok(document.querySelector("[role='menu'] [role='separator']"));
  click(items[0]);
  await sleep();
  assert.deepEqual(calls, [["open", { from: "table" }, { name: "Harbour", words: 13, id: "n1" }]]);

  click(trigger);
  click(document.querySelectorAll("[role='menu'] .action-list-item")[1]);
  const dialog = document.querySelector("dialog");
  assert.match(dialog.textContent, /Remove this row\?/);
  const confirm = dialog.querySelectorAll(".dialog-footer .btn")[1];
  assert.equal(confirm.textContent, "Remove");
  click(confirm);
  await sleep();
  await sleep();
  assert.equal(calls.length, 2);
  assert.equal(calls[1][0], "remove");
});

test("a list item with two actions uses an ActionMenu named after the item", () => {
  const { container } = mount({
    type: "list",
    items: [{ title: "Harbour", actions: [{ type: "button", label: "A", action: { id: "a" } }, { type: "button", label: "B", action: { id: "b" } }] }],
  });
  const trigger = container.querySelector(".list-row button");
  assert.equal(trigger.getAttribute("aria-label"), "Actions: Harbour");
  assert.equal(trigger.getAttribute("aria-haspopup"), "true");
});

test("progress follows job.updated events and unsubscribes on update and destroy", () => {
  const handlers = new Set();
  let unsubscribed = 0;
  const context = {
    subscribe(type, fn) {
      assert.equal(type, "job.updated");
      handlers.add(fn);
      return () => {
        handlers.delete(fn);
        unsubscribed++;
      };
    },
  };
  const jobId = "01j9z3k5q8r2m4n6p7s8t9v0w1";
  const { container, handle } = mount({ type: "progress", value: 0, max: 10, label: "Counting", jobId }, { context });
  const bar = container.querySelector("[role='progressbar']");
  const emit = (data) => [...handlers].forEach((fn) => fn({ type: "job.updated", data }));
  emit({ jobId: "other", status: "running", progress: 9, total: 10 });
  assert.equal(bar.getAttribute("aria-valuenow"), "0");
  emit({ jobId, status: "running", progress: 5, total: 10 });
  assert.equal(bar.getAttribute("aria-valuenow"), "50");
  assert.equal(container.querySelector(".plugin-ui-progress-value").textContent, "5 of 10");
  emit({ jobId, status: "done", progress: 10, total: 10 });
  assert.equal(bar.getAttribute("aria-valuenow"), "100");
  assert.equal(bar.getAttribute("data-color"), "success");

  handle.update({ type: "progress", value: 1, jobId });
  assert.equal(unsubscribed, 1);
  assert.equal(handlers.size, 1);
  handle.destroy();
  assert.equal(unsubscribed, 2);
  assert.equal(handlers.size, 0);
  handle.update({ type: "text", value: "late" });
  assert.equal(container.children.length, 0, "update after destroy does nothing");
});

test("a late response after destroy changes nothing", async () => {
  let release;
  const { container, handle } = mount({ type: "button", label: "Count", action: { id: "count" } }, { onAction: () => new Promise((resolve) => (release = resolve)) });
  click(container.querySelector(".btn"));
  await sleep();
  handle.destroy();
  release({ toast: { message: "Too late" } });
  await sleep();
  assert.equal(container.children.length, 0);
});

/* ---------- File uploads ---------- */

/** Mounts a form with one file field and sets its chosen file. */
function fileForm(options, file = { name: "survey.csv", size: 1200, type: "text/csv" }) {
  const mounted = mount({ type: "form", id: "f", action: { id: "import" }, fields: [{ type: "file", name: "doc", label: "Document", required: true }] }, options);
  const form = mounted.container.querySelector("form");
  const input = form.querySelector("input[name='doc']");
  Object.defineProperty(input, "files", { value: file ? [file] : [], configurable: true });
  input.dispatchEvent(new FakeEvent("change", { bubbles: true }));
  return { ...mounted, form, input, file };
}

test("a file field uploads to the context space on submit, shows progress and sends the file record", async () => {
  const uploads = [];
  const calls = [];
  let finish;
  const upload = (path, file, { fields, onProgress }) => {
    uploads.push({ path, name: file.name, fields });
    onProgress(600, 1200);
    return new Promise((resolve) => {
      finish = () => resolve({ id: "f9", name: "survey (2).csv", mime: "text/csv", size: 1200 });
    });
  };
  const { form, file } = fileForm({ upload, context: { spaceId: "s1" }, onAction: async (id, payload, values) => calls.push([id, values]) });
  const submitButton = form.querySelector("button[type='submit']");
  submit(form);
  await sleep();
  assert.equal(uploads.length, 1);
  assert.deepEqual(uploads[0], { path: "/api/files", name: file.name, fields: { spaceId: "s1", onConflict: "rename" } });
  assert.equal(submitButton.getAttribute("aria-disabled"), "true", "the submit button is busy during the upload");
  const bar = form.querySelector(".plugin-ui-upload-progress [role='progressbar']");
  assert.ok(bar, "the field shows a progress bar");
  assert.equal(bar.getAttribute("aria-valuenow"), "50");
  assert.equal(bar.getAttribute("aria-valuetext"), "50%");
  assert.equal(calls.length, 0, "the action waits for the upload");
  finish();
  await sleep(5);
  assert.equal(calls.length, 1);
  assert.deepEqual(calls[0], ["import", { doc: { fileId: "f9", name: "survey (2).csv", size: 1200, type: "text/csv" } }]);
  assert.equal(form.querySelector(".plugin-ui-upload-progress [role='progressbar']"), null, "the progress bar goes when the upload ends");
  assert.equal(submitButton.hasAttribute("aria-disabled"), false);
});

test("a failed upload shows a field error and does not run the action", async () => {
  const calls = [];
  const upload = async () => {
    throw new Error("file too large");
  };
  const { form } = fileForm({ upload, context: { spaceId: "s1" }, onAction: async (...args) => calls.push(args) });
  submit(form);
  await sleep(5);
  assert.equal(calls.length, 0);
  const wrapper = form.querySelector("input[name='doc']").closest(".form-control");
  assert.match(wrapper.textContent, /The file could not be uploaded\. File too large\./);
  assert.ok(document.activeElement === wrapper.querySelector(".btn"), "focus moves to the Choose file button");
  assert.equal(form.querySelector("button[type='submit']").hasAttribute("aria-disabled"), false);
});

test("a file upload resolves the space through context.spaceOf and needs a space", async () => {
  const seen = [];
  const upload = async (path, file, { fields }) => {
    seen.push(fields.spaceId);
    return { file: { id: "f1", name: file.name } };
  };
  const calls = [];
  const first = fileForm({ upload, context: { itemId: "n1", spaceOf: async () => "s7" }, onAction: async (id, payload, values) => calls.push(values) });
  submit(first.form);
  await sleep(5);
  assert.deepEqual(seen, ["s7"]);
  assert.equal(calls[0].doc.fileId, "f1");

  const second = fileForm({ upload, context: {}, onAction: async (...args) => calls.push(args) });
  submit(second.form);
  await sleep(5);
  assert.equal(calls.length, 1, "without a space nothing is uploaded or sent");
  assert.match(second.form.textContent, /A file needs a space/);
});
