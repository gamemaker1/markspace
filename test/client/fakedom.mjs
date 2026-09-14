// A small DOM for node tests of ui.js: elements, attributes, classes,
// dataset, simple selectors, focus, events with capture and bubble, timers
// from node. It covers what ui.js uses and nothing more.

class FakeEvent {
  constructor(type, { bubbles = false, cancelable = true, key = "", relatedTarget = null, ...extra } = {}) {
    Object.assign(this, { shiftKey: false, ctrlKey: false, metaKey: false, altKey: false, pointerType: "mouse" }, extra);
    this.type = type;
    this.bubbles = bubbles;
    this.cancelable = cancelable;
    this.key = key;
    this.relatedTarget = relatedTarget;
    this.defaultPrevented = false;
    this.target = null;
    this.currentTarget = null;
    this.stopped = false;
    this.stoppedNow = false;
  }
  preventDefault() {
    if (this.cancelable) this.defaultPrevented = true;
  }
  stopPropagation() {
    this.stopped = true;
  }
  stopImmediatePropagation() {
    this.stopped = true;
    this.stoppedNow = true;
  }
}

/** Parses one compound selector such as `li.list-row[data-x='1']`. */
function parseCompound(text) {
  const out = { tag: "", classes: [], attrs: [] };
  const re = /([a-zA-Z][\w-]*)|\.([\w-]+)|\[([\w-]+)(?:=['"]?([^'"\]]*)['"]?)?\]|#([\w-]+)/g;
  let m;
  while ((m = re.exec(text))) {
    if (m[1]) out.tag = m[1].toUpperCase();
    else if (m[2]) out.classes.push(m[2]);
    else if (m[3]) out.attrs.push([m[3], m[4]]);
    else if (m[5]) out.attrs.push(["id", m[5]]);
  }
  return out;
}

function matchCompound(el, c) {
  if (c.tag && el.tagName !== c.tag) return false;
  for (const cls of c.classes) if (!el.classList.contains(cls)) return false;
  for (const [name, value] of c.attrs) {
    if (!el.hasAttribute(name)) return false;
    if (value !== undefined && el.getAttribute(name) !== value) return false;
  }
  return true;
}

/** Matches a selector list with compound parts and descendant combinators. */
function matches(el, selector) {
  return selector.split(",").some((part) => {
    const text = part.trim();
    if (text === ":focus-visible" || text === ":focus") return el.ownerDocument.activeElement === el;
    const chain = text.split(/\s+/).filter(Boolean);
    if (!matchCompound(el, parseCompound(chain[chain.length - 1]))) return false;
    let node = el.parentNode;
    for (let i = chain.length - 2; i >= 0; i--) {
      const c = parseCompound(chain[i]);
      while (node && node.nodeType === 1 && !matchCompound(node, c)) node = node.parentNode;
      if (!node || node.nodeType !== 1) return false;
      node = node.parentNode;
    }
    return true;
  });
}

class FakeNode {
  /** Keeps assertion messages short: node:assert inspects both values. */
  [Symbol.for("nodejs.util.inspect.custom")]() {
    const cls = this.getAttribute && this.getAttribute("class") ? "." + this.getAttribute("class").split(/\s+/).join(".") : "";
    const data = this.getAttribute && this.getAttribute("data-id") ? `[data-id=${this.getAttribute("data-id")}]` : "";
    return this.tagName ? `<${this.tagName.toLowerCase()}${this.id ? "#" + this.id : ""}${cls}${data}>` : `#node${this.nodeType}`;
  }
  constructor(doc) {
    // Back references are not enumerable. node:assert builds a failure
    // message with customInspect off and a depth of 1000, and through
    // ownerDocument and parentNode it would walk the whole cyclic document,
    // growing to many GiB. Without them it prints only the node's subtree.
    Object.defineProperty(this, "ownerDocument", { value: doc, writable: true, configurable: true, enumerable: false });
    Object.defineProperty(this, "parentNode", { value: null, writable: true, configurable: true, enumerable: false });
    Object.defineProperty(this, "listeners", { value: [], writable: true, configurable: true, enumerable: false });
    this.childNodes = [];
  }
  get parentElement() {
    return this.parentNode && this.parentNode.nodeType === 1 ? this.parentNode : null;
  }
  get firstChild() {
    return this.childNodes[0] || null;
  }
  get nextElementSibling() {
    if (!this.parentNode) return null;
    const list = this.parentNode.childNodes;
    for (let i = list.indexOf(this) + 1; i < list.length; i++) if (list[i].nodeType === 1) return list[i];
    return null;
  }
  get nextSibling() {
    if (!this.parentNode) return null;
    const list = this.parentNode.childNodes;
    return list[list.indexOf(this) + 1] || null;
  }
  get isConnected() {
    let node = this;
    while (node.parentNode) node = node.parentNode;
    return node === this.ownerDocument;
  }
  get children() {
    return this.childNodes.filter((n) => n.nodeType === 1);
  }
  appendChild(child) {
    if (child.nodeType === 11) {
      for (const c of [...child.childNodes]) this.appendChild(c);
      return child;
    }
    child.remove();
    child.parentNode = this;
    this.childNodes.push(child);
    return child;
  }
  append(...nodes) {
    for (const n of nodes) this.appendChild(typeof n === "string" ? this.ownerDocument.createTextNode(n) : n);
  }
  prepend(...nodes) {
    const first = this.firstChild;
    for (const n of nodes) this.insertBefore(typeof n === "string" ? this.ownerDocument.createTextNode(n) : n, first);
  }
  insertBefore(child, ref) {
    if (!ref) return this.appendChild(child);
    child.remove();
    child.parentNode = this;
    this.childNodes.splice(this.childNodes.indexOf(ref), 0, child);
    return child;
  }
  after(...nodes) {
    const parent = this.parentNode;
    let ref = this.nextSibling;
    for (const n of nodes) parent.insertBefore(n, ref);
  }
  remove() {
    if (this.parentNode) {
      const list = this.parentNode.childNodes;
      list.splice(list.indexOf(this), 1);
      this.parentNode = null;
    }
  }
  replaceChildren(...nodes) {
    for (const c of [...this.childNodes]) c.remove();
    this.append(...nodes);
  }
  contains(node) {
    for (let n = node; n; n = n.parentNode) if (n === this) return true;
    return false;
  }
  get textContent() {
    return this.childNodes.map((c) => c.textContent).join("");
  }
  set textContent(value) {
    this.replaceChildren(this.ownerDocument.createTextNode(String(value)));
  }
  addEventListener(type, fn, options = {}) {
    const capture = options === true || !!(options && options.capture);
    this.listeners.push({ type, fn, capture });
  }
  removeEventListener(type, fn, options = {}) {
    const capture = options === true || !!(options && options.capture);
    this.listeners = this.listeners.filter((l) => !(l.type === type && l.fn === fn && l.capture === capture));
  }
  dispatchEvent(event) {
    event.target = this;
    const path = [];
    for (let n = this; n; n = n.parentNode) path.push(n);
    const run = (node, capture) => {
      event.currentTarget = node;
      for (const l of [...node.listeners]) {
        if (l.type !== event.type) continue;
        if (node !== this && l.capture !== capture) continue;
        l.fn.call(node, event);
        if (event.stoppedNow) return;
      }
    };
    for (let i = path.length - 1; i > 0 && !event.stopped; i--) run(path[i], true);
    if (!event.stopped) run(this, true);
    if (event.bubbles) for (let i = 1; i < path.length && !event.stopped; i++) run(path[i], false);
    return !event.defaultPrevented;
  }
}

class FakeText extends FakeNode {
  constructor(doc, text) {
    super(doc);
    this.nodeType = 3;
    this.data = text;
  }
  get textContent() {
    return this.data;
  }
  set textContent(v) {
    this.data = String(v);
  }
}

class FakeFragment extends FakeNode {
  constructor(doc) {
    super(doc);
    this.nodeType = 11;
  }
}

const BOOL_PROPS = ["hidden", "disabled", "readOnly", "required", "checked", "multiple", "novalidate"];

class FakeElement extends FakeNode {
  constructor(doc, tag) {
    super(doc);
    this.nodeType = 1;
    this.tagName = tag.toUpperCase();
    this.attrs = new Map();
    this.value = "";
    this.style = { props: {}, setProperty(k, v) { this.props[k] = v; }, getPropertyValue(k) { return this.props[k] || ""; }, removeProperty(k) { delete this.props[k]; } };
    const self = this;
    this.dataset = new Proxy({}, {
      get: (_, key) => self.getAttribute("data-" + dashed(key)) ?? undefined,
      set: (_, key, value) => { self.setAttribute("data-" + dashed(key), String(value)); return true; },
      deleteProperty: (_, key) => { self.removeAttribute("data-" + dashed(key)); return true; },
      has: (_, key) => self.hasAttribute("data-" + dashed(key)),
    });
    this.classList = {
      list: () => (self.getAttribute("class") || "").split(/\s+/).filter(Boolean),
      contains: (c) => self.classList.list().includes(c),
      add: (...cs) => self.setAttribute("class", [...new Set([...self.classList.list(), ...cs])].join(" ")),
      remove: (...cs) => self.setAttribute("class", self.classList.list().filter((x) => !cs.includes(x)).join(" ")),
      toggle: (c, on) => {
        const want = on === undefined ? !self.classList.contains(c) : !!on;
        if (want) self.classList.add(c);
        else self.classList.remove(c);
        return want;
      },
    };
  }
  get className() { return this.getAttribute("class") || ""; }
  set className(v) { this.setAttribute("class", v); }
  get id() { return this.getAttribute("id") || ""; }
  set id(v) { this.setAttribute("id", v); }
  get type() { return this.getAttribute("type") || ""; }
  set type(v) { this.setAttribute("type", v); }
  get tabIndex() { return Number(this.getAttribute("tabindex") || -1); }
  set tabIndex(v) { this.setAttribute("tabindex", String(v)); }
  get colSpan() { return Number(this.getAttribute("colspan") || 1); }
  set colSpan(v) { this.setAttribute("colspan", String(v)); }
  get cells() { return this.children.filter((c) => c.tagName === "TD" || c.tagName === "TH"); }
  getAttribute(name) { return this.attrs.has(name) ? this.attrs.get(name) : null; }
  setAttribute(name, value) { this.attrs.set(name, String(value)); }
  hasAttribute(name) { return this.attrs.has(name); }
  removeAttribute(name) { this.attrs.delete(name); }
  toggleAttribute(name, on) {
    const want = on === undefined ? !this.hasAttribute(name) : !!on;
    if (want) this.setAttribute(name, "");
    else this.removeAttribute(name);
    return want;
  }
  matches(selector) { return matches(this, selector); }
  closest(selector) {
    for (let n = this; n && n.nodeType === 1; n = n.parentNode) if (n.matches(selector)) return n;
    return null;
  }
  querySelectorAll(selector) {
    const out = [];
    const walk = (node) => {
      for (const c of node.childNodes) {
        if (c.nodeType !== 1) continue;
        if (c.matches(selector)) out.push(c);
        walk(c);
      }
    };
    walk(this);
    return out;
  }
  querySelector(selector) { return this.querySelectorAll(selector)[0] || null; }
  getClientRects() { return this.isConnected && !this.closest("[hidden]") ? [{ width: 10, height: 10 }] : []; }
  getBoundingClientRect() { return { top: 0, left: 0, right: 10, bottom: 10, width: 10, height: 10 }; }
  get offsetParent() { return this.isConnected ? this.parentNode : null; }
  get offsetWidth() { return 10; }
  get offsetHeight() { return 10; }
  focus() {
    if (!this.isConnected) return;
    const doc = this.ownerDocument;
    const previous = doc.activeElement;
    if (previous === this) return;
    doc.activeElement = this;
    if (doc.focusEvents) {
      if (previous && previous !== doc.body) {
        previous.dispatchEvent(new FakeEvent("blur", { relatedTarget: this }));
        previous.dispatchEvent(new FakeEvent("focusout", { bubbles: true, relatedTarget: this }));
      }
      this.dispatchEvent(new FakeEvent("focus", { relatedTarget: previous }));
      this.dispatchEvent(new FakeEvent("focusin", { bubbles: true, relatedTarget: previous }));
    }
  }
  blur() {
    if (this.ownerDocument.activeElement !== this) return;
    this.ownerDocument.activeElement = this.ownerDocument.body;
    if (this.ownerDocument.focusEvents) {
      this.dispatchEvent(new FakeEvent("blur"));
      this.dispatchEvent(new FakeEvent("focusout", { bubbles: true }));
    }
  }
  showModal() { this.setAttribute("open", ""); }
  close() { this.removeAttribute("open"); }
  get open() { return this.hasAttribute("open"); }
  set open(v) { if (v) this.setAttribute("open", ""); else this.removeAttribute("open"); }
  select() {}
  scrollIntoView() {}
  click() { this.dispatchEvent(new FakeEvent("click", { bubbles: true })); }
}

for (const prop of BOOL_PROPS) {
  const attr = prop.toLowerCase();
  Object.defineProperty(FakeElement.prototype, prop, {
    get() { return this.hasAttribute(attr); },
    set(v) { if (v) this.setAttribute(attr, ""); else this.removeAttribute(attr); },
  });
}

function dashed(key) {
  return String(key).replace(/[A-Z]/g, (c) => "-" + c.toLowerCase());
}

class FakeDocument extends FakeNode {
  constructor() {
    super(null);
    this.ownerDocument = this;
    this.nodeType = 9;
    this.documentElement = new FakeElement(this, "html");
    this.appendChild(this.documentElement);
    this.body = new FakeElement(this, "body");
    this.documentElement.appendChild(this.body);
    this.activeElement = this.body;
  }
  createElement(tag) { return new FakeElement(this, tag); }
  createElementNS(_ns, tag) { return new FakeElement(this, tag); }
  createTextNode(text) { return new FakeText(this, text); }
  createDocumentFragment() { return new FakeFragment(this); }
  querySelectorAll(selector) { return this.documentElement.querySelectorAll(selector).concat(this.documentElement.matches(selector) ? [this.documentElement] : []); }
  querySelector(selector) { return this.querySelectorAll(selector)[0] || null; }
  getElementById(id) { return this.querySelector("#" + id); }
}

/**
 * Installs a fresh fake document and window on globalThis.
 * @param {{narrow?: boolean, reducedMotion?: boolean}} [options]
 * @returns {{document: FakeDocument, Event: typeof FakeEvent}}
 */
/** Resident memory, in bytes, at which a test process stops itself. */
const MEMORY_LIMIT = Number(process.env.JSTEST_RSS_LIMIT_MB || 768) * 1024 * 1024;
let memoryWatch = null;

/**
 * Stops the process when its resident memory passes MEMORY_LIMIT, so a
 * runaway test fails in about a second instead of exhausting the machine.
 */
function watchMemory() {
  if (memoryWatch) return;
  memoryWatch = setInterval(() => {
    const rss = process.memoryUsage.rss();
    if (rss > MEMORY_LIMIT) {
      process.stderr.write(`fakedom: resident memory ${Math.round(rss / 1048576)} MiB passed ${Math.round(MEMORY_LIMIT / 1048576)} MiB, stopping the test process\n`);
      process.exit(70);
    }
  }, 100);
  memoryWatch.unref();
}

export function installDOM({ narrow = false, reducedMotion = true, focusEvents = false } = {}) {
  watchMemory();
  const document = new FakeDocument();
  document.focusEvents = focusEvents;
  const matchMedia = (query) => ({
    matches: (/reduce/.test(query) && reducedMotion) || (/max-width/.test(query) && narrow),
    addEventListener() {},
    removeEventListener() {},
  });
  const getComputedStyle = (el) => ({
    display: el.hasAttribute && el.hasAttribute("hidden") ? "none" : "block",
    visibility: "visible",
    opacity: "1",
    position: (el.style && el.style.props.position) || "static",
    fontFamily: "sans-serif",
    fontWeight: "400",
    letterSpacing: "normal",
    fontSize: "16px",
  });
  globalThis.document = document;
  const windowTarget = new FakeNode(document);
  globalThis.window = {
    matchMedia, getComputedStyle, innerWidth: narrow ? 390 : 1440, innerHeight: 844,
    addEventListener: (...args) => windowTarget.addEventListener(...args),
    removeEventListener: (...args) => windowTarget.removeEventListener(...args),
    dispatchEvent: (event) => windowTarget.dispatchEvent(event),
  };
  globalThis.getComputedStyle = getComputedStyle;
  globalThis.CSS = { escape: (s) => String(s) };
  return { document, Event: FakeEvent };
}

export { FakeEvent };
