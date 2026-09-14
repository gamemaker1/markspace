/**
 * Markspace component kit on GitHub's Primer design system (docs/primer.md).
 * The shell, the editor, the panels and the enhanced server pages build
 * their controls with it: buttons, tooltips, ActionMenu, SelectPanel,
 * Autocomplete, Dialog, ConfirmationDialog, AnchoredOverlay, Banner,
 * InlineMessage, the status region, undo rows, loading states, labels,
 * form controls, TreeView, SegmentedControl, ToggleSwitch, Details, the
 * drop area, the upload list and the Octicon set. docs/offline.md lists the API.
 *
 * Nothing at the top level touches the DOM, so node tests can import it.
 */

import { PATHS as OCTICON_PATHS } from "./octicons.js";

/* ---------- Elements ---------- */

/**
 * Creates an element. Props: class, text, dataset, style, on (event map),
 * attributes with a dash or "role", and any other element property.
 * Children may be nodes, strings, arrays, or null to skip.
 * @param {string} tag
 * @param {object} [props]
 * @param {...any} children
 * @returns {HTMLElement}
 */
export function h(tag, props = {}, ...children) {
  const el = document.createElement(tag);
  for (const [key, value] of Object.entries(props || {})) {
    if (value === undefined || value === null || value === false) {
      continue;
    }
    if (key === "class") {
      el.className = value;
    } else if (key === "text") {
      el.textContent = String(value);
    } else if (key === "on") {
      for (const [type, fn] of Object.entries(value)) {
        el.addEventListener(type, fn);
      }
    } else if (key === "dataset") {
      Object.assign(el.dataset, value);
    } else if (key === "style") {
      for (const [name, v] of Object.entries(value)) {
        el.style.setProperty(name, v);
      }
    } else if (key.includes("-") || key === "role" || key === "for") {
      el.setAttribute(key, value === true ? "" : String(value));
    } else if (key in el) {
      el[key] = value;
    } else {
      el.setAttribute(key, value === true ? "" : String(value));
    }
  }
  appendChildren(el, children);
  return el;
}

/** Appends nodes, strings and nested arrays, skipping empty values. */
function appendChildren(el, children) {
  for (const child of children) {
    if (child === null || child === undefined || child === false) {
      continue;
    }
    if (Array.isArray(child)) {
      appendChildren(el, child);
    } else if (typeof child === "string" || typeof child === "number") {
      el.appendChild(document.createTextNode(String(child)));
    } else {
      el.appendChild(child);
    }
  }
}

/* ---------- Icons ---------- */

const SVG_NS = "http://www.w3.org/2000/svg";

/*
 * Maps each Markspace icon name to its Octicon, as docs/primer.md 4.3 lists.
 * icon() also accepts Octicon names, so new code passes those directly.
 * State variants such as sidebar-expand and file-directory-open-fill are
 * reached by their Octicon names.
 */
const ICON_MAP = {
  sidebar: "sidebar-collapse",
  rail: "sidebar-expand",
  search: "search",
  plus: "plus",
  minus: "dash",
  more: "kebab-horizontal",
  star: "star",
  starFill: "star-fill",
  info: "info",
  alert: "alert",
  check: "check",
  close: "x",
  home: "home",
  clock: "clock",
  users: "people",
  user: "person",
  trash: "trash",
  settings: "gear",
  edit: "pencil",
  link: "link",
  copy: "copy",
  move: "file-moved",
  folder: "file-directory-fill",
  history: "history",
  comment: "comment",
  share: "share",
  file: "file",
  files: "paperclip",
  image: "image",
  video: "video",
  audio: "unmute",
  archive: "file-zip",
  duplicate: "duplicate",
  child: "file-added",
  rename: "pencil",
  signout: "sign-out",
  download: "download",
  upload: "upload",
  retry: "sync",
  back: "arrow-left",
  chevronDown: "chevron-down",
  chevronRight: "chevron-right",
  grid: "apps",
  list: "list-unordered",
  filter: "filter",
  eye: "eye",
  lock: "lock",
  key: "key",
  globe: "globe",
  sparkle: "sparkle",
  cloud: "cloud",
  cloudOff: "cloud-offline",
  sun: "sun",
  moon: "moon",
  tag: "tag",
  hash: "hash",
  device: "device-desktop",
};

/* Markspace names drawn mirrored. Octicons has no right-side pane icon. */
const MIRRORED_ICONS = new Set(["rail"]);

/* Matches one path element in the Octicons path markup. */
const OCTICON_PATH = /<path d="([^"]*)"><\/path>/g;

/** Names icon() accepts: every Markspace name and every vendored Octicon. */
export const ICON_NAMES = [...new Set([...Object.keys(ICON_MAP), ...Object.keys(OCTICON_PATHS).map((key) => key.replace(/-\d+$/, ""))])];

/**
 * Creates an inline SVG Octicon drawn in currentColor. The name is a
 * Markspace name from ICON_MAP or an Octicon name. The size is 16 by
 * default. A size of 24 or more draws the 24 px icon and any smaller size
 * draws the 16 px icon, because Octicons ship only natural heights. The
 * attributes match toSVG in @primer/octicons with the fill and focusable
 * options set. An unknown name logs a warning and returns an empty icon.
 * @param {string} name
 * @param {number} [size]
 * @returns {SVGElement}
 */
export function icon(name, size = 16) {
  const octicon = Object.prototype.hasOwnProperty.call(ICON_MAP, name) ? ICON_MAP[name] : String(name);
  const height = Number(size) >= 24 ? 24 : 16;
  const key = `${octicon}-${height}`;
  const paths = Object.prototype.hasOwnProperty.call(OCTICON_PATHS, key) ? OCTICON_PATHS[key] : "";
  const svg = document.createElementNS(SVG_NS, "svg");
  svg.setAttribute("version", "1.1");
  svg.setAttribute("width", String(height));
  svg.setAttribute("height", String(height));
  svg.setAttribute("viewBox", `0 0 ${height} ${height}`);
  svg.setAttribute("class", paths ? `octicon octicon-${octicon}` : "octicon");
  svg.setAttribute("aria-hidden", "true");
  svg.setAttribute("data-component", "Octicon");
  svg.setAttribute("fill", "currentColor");
  svg.setAttribute("focusable", "false");
  if (!paths) {
    console.warn(`[markspace] ui.js has no icon named "${name}"`);
    return svg;
  }
  if (MIRRORED_ICONS.has(name)) {
    svg.style.transform = "scaleX(-1)";
  }
  for (const match of paths.matchAll(OCTICON_PATH)) {
    const path = document.createElementNS(SVG_NS, "path");
    path.setAttribute("d", match[1]);
    svg.appendChild(path);
  }
  return svg;
}

/* ---------- Shared behaviour ---------- */

/** Reports whether the reader asked for less motion. */
export function prefersReducedMotion() {
  return typeof window !== "undefined" && !!window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

const FOCUSABLE_TAGS = "a, button, input, select, textarea, summary, [tabindex], [contenteditable]";

/** Reports whether an element can take keyboard focus by Tab. */
function isTabbable(el) {
  if (el.hasAttribute("disabled") || el.getAttribute("tabindex") === "-1") {
    return false;
  }
  if (el.tagName === "A" && !el.hasAttribute("href") && !el.hasAttribute("tabindex")) {
    return false;
  }
  if (el.tagName === "INPUT" && el.getAttribute("type") === "hidden") {
    return false;
  }
  if (el.closest("[hidden], [inert]")) {
    return false;
  }
  return el.offsetParent !== null || el === document.activeElement || (typeof getComputedStyle === "function" && getComputedStyle(el).position === "fixed");
}

/**
 * Lists the focusable, visible elements inside a container in document order.
 * @param {Element} container
 * @returns {HTMLElement[]}
 */
/**
 * Keeps a focused control inside a .table-scroll region fully visible. Chrome
 * leaves a partly visible control where it is when focus arrives, so a
 * control at the clipped edge of a wide table would stay cut off. One
 * delegated focusin listener serves every region on the page, including
 * tables added later. Calling it again does nothing.
 * @param {Document} [doc]
 */
export function watchTableScroll(doc = document) {
  if (!doc || doc.__msTableScroll) {
    return;
  }
  doc.__msTableScroll = true;
  doc.addEventListener("focusin", (event) => {
    const target = event.target;
    const region = target && target.closest ? target.closest(".table-scroll") : null;
    if (!region || target === region || typeof target.scrollIntoView !== "function") {
      return;
    }
    target.scrollIntoView({ block: "nearest", inline: "nearest" });
  });
}

export function focusables(container) {
  return Array.from(container.querySelectorAll(FOCUSABLE_TAGS)).filter(isTabbable);
}

/**
 * Keeps Tab inside a container. Call it from a keydown handler.
 * @param {KeyboardEvent} event
 * @param {Element} container
 * @returns {boolean} true when the key was Tab.
 */
export function trapFocus(event, container) {
  if (event.key !== "Tab") {
    return false;
  }
  const items = focusables(container);
  if (!items.length) {
    event.preventDefault();
    return true;
  }
  const first = items[0];
  const last = items[items.length - 1];
  if (!container.contains(document.activeElement)) {
    event.preventDefault();
    first.focus();
  } else if (event.shiftKey && document.activeElement === first) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus();
  }
  return true;
}

/** Moves focus to an element when it is still on the page. */
function restoreFocus(el) {
  if (el && el.isConnected && typeof el.focus === "function") {
    el.focus({ preventScroll: true });
    return document.activeElement === el;
  }
  return false;
}

/** Runs fn after ms. */
function later(fn, ms) {
  return setTimeout(fn, ms);
}

let idCount = 0;

/** Returns a page-unique id with a prefix. */
export function uid(prefix = "ui") {
  idCount += 1;
  return `${prefix}-${idCount}`;
}

/** Gives an element an id when it has none and returns the id. */
function ensureId(el, prefix) {
  if (!el.id) {
    el.id = uid(prefix);
  }
  return el.id;
}

/** Adds an id to a space-separated ARIA reference attribute. */
function addRef(el, attr, id) {
  const ids = (el.getAttribute(attr) || "").split(/\s+/).filter(Boolean);
  if (!ids.includes(id)) {
    ids.push(id);
  }
  el.setAttribute(attr, ids.join(" "));
}

/** Removes an id from a space-separated ARIA reference attribute. */
function removeRef(el, attr, id) {
  const ids = (el.getAttribute(attr) || "").split(/\s+/).filter((x) => x && x !== id);
  if (ids.length) {
    el.setAttribute(attr, ids.join(" "));
  } else {
    el.removeAttribute(attr);
  }
}

/** Reports whether a key event is a printable character without modifiers. */
function isPrintable(event) {
  return typeof event.key === "string" && event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey && /\S/.test(event.key);
}

/**
 * Type-ahead state. push() adds a character and returns the typed text,
 * which resets after timeout ms without a key.
 */
function typeahead(timeout) {
  let text = "";
  let timer = 0;
  return {
    push(key) {
      clearTimeout(timer);
      text += key.toLowerCase();
      timer = later(() => {
        text = "";
      }, timeout);
      return text;
    },
  };
}

/**
 * Finds the index of the next label that starts with typed, after current
 * and wrapping. With more than one typed character the current label counts,
 * so typing "re" on "Rename" stays there.
 */
function matchTypeahead(labels, current, typed) {
  const n = labels.length;
  if (!n) {
    return -1;
  }
  const start = typed.length > 1 && current >= 0 ? current : current + 1;
  for (let i = 0; i < n; i++) {
    const index = (((start + i) % n) + n) % n;
    if (labels[index].startsWith(typed)) {
      return index;
    }
  }
  return -1;
}

/**
 * Formats a byte count with a binary unit, like the server.
 * @param {number} n
 * @returns {string}
 */
export function formatBytes(n) {
  const value = Number(n) || 0;
  if (value < 1024) {
    return value + " B";
  }
  const units = ["KB", "MB", "GB", "TB"];
  let size = value;
  for (const unit of units) {
    size /= 1024;
    if (size < 1024 || unit === "TB") {
      return (size < 10 ? size.toFixed(1) : Math.round(size)) + " " + unit;
    }
  }
  return value + " B";
}

/** Capitalizes a message and ends it with a full stop. */
function sentence(text) {
  const s = String(text || "").trim();
  if (!s) {
    return s;
  }
  const first = s.charAt(0).toUpperCase() + s.slice(1);
  return /[.!?]$/.test(first) ? first : first + ".";
}

/** Turns an error into a sentence for a message. */
function errorMessage(err, fallback = "Something went wrong.") {
  if (err && err.status === 0) {
    return "The server could not be reached.";
  }
  return sentence((err && err.message) || fallback);
}

/* ---------- Layout ---------- */

/** Primer's narrow viewport range, below 768 px. */
export const NARROW_QUERY = "(max-width: calc(48rem - 0.02px))";

/** Reports whether the viewport is in Primer's narrow range, below 768 px. */
export function isNarrow() {
  return typeof window !== "undefined" && !!window.matchMedia && window.matchMedia(NARROW_QUERY).matches;
}

/* ---------- Status region ---------- */

/** Returns the status region of the top open dialog or of the page, creating it on first use. */
function statusRegion(politeness) {
  const assertive = politeness === "assertive";
  const attr = assertive ? "data-alert-region" : "data-status-region";
  const scope = topDialog() || document.body;
  let region = scope.querySelector(`[${attr}]`);
  if (!region && scope === document.body) {
    region = document.getElementById(assertive ? "alert-region" : "status-region");
  }
  if (!region) {
    region = h("div", { class: "sr-only", role: assertive ? "alert" : "status", "aria-live": assertive ? "assertive" : "polite", "aria-atomic": "true" });
    region.setAttribute(attr, "");
    scope.appendChild(region);
  }
  return region;
}

/**
 * Announces text to screen readers through the status region. The region is
 * visually hidden and sits in the page from load, or inside the top open
 * dialog while one is open. Nothing shows on screen.
 * @param {string} text
 * @param {{politeness?: "polite" | "assertive"}} [options]
 * @returns {HTMLElement} the region
 */
export function announce(text, { politeness = "polite" } = {}) {
  const region = statusRegion(politeness);
  const message = String(text || "");
  // The same text twice in a row still counts as a change.
  region.textContent = region.textContent === message ? message + " " : message;
  return region;
}

/* ---------- Buttons ---------- */

const BUTTON_VARIANTS = { default: "", primary: "btn-primary", danger: "btn-danger", invisible: "btn-invisible", link: "btn-link" };
const BUTTON_SIZES = { small: "btn-sm", medium: "", large: "btn-lg" };

/** Builds the class list of a button. */
function buttonClass({ variant = "default", size = "medium", block = false, icon: isIcon = false, className = "" } = {}) {
  return ["btn", BUTTON_VARIANTS[variant] || "", BUTTON_SIZES[size] || "", block ? "btn-block" : "", isIcon ? "btn-icon" : "", className].filter(Boolean).join(" ");
}

/** Accepts an icon name or a node and returns a node. */
function visual(value, size = 16) {
  if (!value) {
    return null;
  }
  return typeof value === "string" ? icon(value, size) : value;
}

/**
 * Builds a Primer Button. With href it is a link styled as a button.
 * inactive keeps the button focusable in Primer's inactive style.
 * @param {{label: string, variant?: "default" | "primary" | "danger" | "invisible" | "link", size?: "small" | "medium" | "large", leadingVisual?: string | Node, trailingVisual?: string | Node, trailingAction?: string | Node, count?: number | null, keybindingHint?: string, block?: boolean, inactive?: boolean, href?: string, type?: string, onClick?: Function, className?: string, attrs?: object}} options
 * @returns {HTMLButtonElement | HTMLAnchorElement}
 */
export function button({ label, variant = "default", size = "medium", leadingVisual, trailingVisual, trailingAction, count = null, keybindingHint: hint, block = false, inactive = false, href, type = "button", onClick, className = "", attrs = {} } = {}) {
  const props = Object.assign({ class: buttonClass({ variant, size, block, className }) }, attrs);
  if (inactive) {
    props["data-inactive"] = "true";
    props["aria-disabled"] = "true";
  }
  const children = [
    leadingVisual ? h("span", { class: "btn-visual btn-leading", "aria-hidden": "true" }, visual(leadingVisual)) : null,
    h("span", { class: "btn-label", text: label }),
    count !== null && count !== undefined ? counterLabel(count) : null,
    hint ? keybindingHint(hint) : null,
    trailingVisual ? h("span", { class: "btn-visual btn-trailing", "aria-hidden": "true" }, visual(trailingVisual)) : null,
    trailingAction ? h("span", { class: "btn-visual btn-trailing-action", "aria-hidden": "true" }, visual(trailingAction)) : null,
  ];
  const el = href ? h("a", Object.assign({ href }, props), children) : h("button", Object.assign({ type }, props), children);
  if (typeof onClick === "function") {
    el.addEventListener("click", (event) => {
      if (el.getAttribute("aria-disabled") === "true") {
        event.preventDefault();
        return;
      }
      onClick(event);
    });
  }
  return el;
}

/**
 * Builds a Primer IconButton: a square button with an Octicon and a Tooltip
 * that names it. With description, the Tooltip describes the button and
 * aria-label names it, as Primer does for buttons with a keyboard hint.
 * @param {{icon: string | Node, label: string, description?: string, keybindingHint?: string, variant?: "default" | "invisible" | "primary" | "danger", size?: "small" | "medium" | "large", iconSize?: 16 | 24, pressed?: boolean, tooltip?: boolean, onClick?: Function, className?: string, attrs?: object}} options
 * @returns {HTMLButtonElement}
 */
export function iconButton({ icon: glyph, label, description, keybindingHint: hint, variant = "invisible", size = "medium", iconSize = 16, pressed, tooltip: withTooltip = true, onClick, className = "", attrs = {} } = {}) {
  const el = h(
    "button",
    Object.assign({ type: "button", class: buttonClass({ variant, size, icon: true, className }), "aria-label": label }, attrs),
    h("span", { class: "btn-visual", "aria-hidden": "true" }, visual(glyph, iconSize))
  );
  if (pressed !== undefined) {
    el.setAttribute("aria-pressed", pressed ? "true" : "false");
  }
  if (typeof onClick === "function") {
    el.addEventListener("click", (event) => {
      if (el.getAttribute("aria-disabled") === "true") {
        event.preventDefault();
        return;
      }
      onClick(event);
    });
  }
  if (withTooltip) {
    if (description || hint) {
      tooltip(el, description || label, { type: "description", keybindingHint: hint });
    } else {
      tooltip(el, label, { type: "label" });
    }
  }
  return el;
}

/**
 * Builds a KeybindingHint: kbd text such as "Ctrl K".
 * @param {string} keys
 * @returns {HTMLElement}
 */
export function keybindingHint(keys) {
  return h("kbd", { class: "keybinding-hint", text: keys });
}

/* ---------- Spinner ---------- */

const SPINNER_SIZES = { small: 16, medium: 32, large: 64 };

/**
 * Builds a Primer Spinner. The SVG is hidden from screen readers and a
 * visually hidden label names the wait. delay hides it for that many ms, so
 * short waits show nothing (docs/primer.md 6.5 uses 1000).
 * @param {{size?: "small" | "medium" | "large", label?: string, delay?: number, className?: string}} [options]
 * @returns {HTMLElement}
 */
export function spinner({ size = "small", label = "Loading", delay = 0, className = "" } = {}) {
  const px = SPINNER_SIZES[size] || 16;
  const svg = document.createElementNS(SVG_NS, "svg");
  svg.setAttribute("class", "spinner-svg");
  svg.setAttribute("viewBox", "0 0 16 16");
  svg.setAttribute("width", String(px));
  svg.setAttribute("height", String(px));
  svg.setAttribute("fill", "none");
  svg.setAttribute("aria-hidden", "true");
  svg.setAttribute("focusable", "false");
  const track = document.createElementNS(SVG_NS, "circle");
  for (const [k, v] of Object.entries({ cx: "8", cy: "8", r: "7", stroke: "currentColor", "stroke-opacity": "0.25", "stroke-width": "2", "vector-effect": "non-scaling-stroke" })) {
    track.setAttribute(k, v);
  }
  const arc = document.createElementNS(SVG_NS, "path");
  for (const [k, v] of Object.entries({ d: "M15 8a7.002 7.002 0 00-7-7", stroke: "currentColor", "stroke-width": "2", "stroke-linecap": "round", "vector-effect": "non-scaling-stroke" })) {
    arc.setAttribute(k, v);
  }
  svg.append(track, arc);
  const el = h("span", { class: ("spinner spinner-" + size + " " + className).trim() }, svg, label ? h("span", { class: "sr-only", text: label }) : null);
  if (delay > 0) {
    el.hidden = true;
    later(() => {
      el.hidden = false;
    }, delay);
  }
  return el;
}

/* ---------- Busy buttons ---------- */

/**
 * Marks a button busy as Primer's Button loading state does. The button keeps
 * its label, width and focus and gets aria-disabled. A small Spinner replaces
 * the leading visual, or the trailing visual, or sits centred over a hidden
 * label. message is announced in the status region, such as "Saving profile".
 * @param {HTMLElement} el
 * @param {boolean} busy
 * @param {{message?: string}} [options]
 */
export function setBusy(el, busy, { message = "" } = {}) {
  if (!el) {
    return;
  }
  const isBusy = el.getAttribute("data-busy") === "true";
  if (busy && !isBusy) {
    el.setAttribute("data-busy", "true");
    el.setAttribute("aria-busy", "true");
    el.dataset.wasAriaDisabled = el.getAttribute("aria-disabled") || "";
    el.setAttribute("aria-disabled", "true");
    const slot = el.querySelector(".btn-leading") || el.querySelector(".btn-trailing, .btn-trailing-action") || el.querySelector(".btn-visual");
    const spin = spinner({ size: "small", label: "", className: "btn-spinner" });
    if (slot) {
      slot.classList.add("is-replaced");
      slot.after(spin);
    } else {
      if (!el.querySelector(".btn-label")) {
        const label = h("span", { class: "btn-label" });
        label.append(...Array.from(el.childNodes));
        el.appendChild(label);
      }
      el.classList.add("is-busy-centered");
      el.appendChild(spin);
    }
    el._busyBlock = (event) => {
      event.preventDefault();
      event.stopImmediatePropagation();
    };
    el.addEventListener("click", el._busyBlock, true);
    if (message) {
      announce(message);
    }
  } else if (!busy && isBusy) {
    el.removeAttribute("data-busy");
    el.removeAttribute("aria-busy");
    const was = el.dataset.wasAriaDisabled;
    delete el.dataset.wasAriaDisabled;
    if (was) {
      el.setAttribute("aria-disabled", was);
    } else {
      el.removeAttribute("aria-disabled");
    }
    for (const spin of el.querySelectorAll(".btn-spinner")) {
      spin.remove();
    }
    for (const slot of el.querySelectorAll(".is-replaced")) {
      slot.classList.remove("is-replaced");
    }
    el.classList.remove("is-busy-centered");
    if (el._busyBlock) {
      el.removeEventListener("click", el._busyBlock, true);
      el._busyBlock = null;
    }
  }
}

/* ---------- Tooltip ---------- */

/** Reports whether the element has keyboard focus, as :focus-visible. */
function hasVisibleFocus(el) {
  try {
    return el.matches(":focus-visible");
  } catch (err) {
    return true;
  }
}

/** Reports whether the popover API is available on an element. */
function supportsPopover(el) {
  return typeof el.showPopover === "function" && typeof el.hidePopover === "function";
}

/** Shows an element in the top layer when the browser supports popover. */
function showInTopLayer(el) {
  if (supportsPopover(el)) {
    try {
      el.showPopover();
    } catch (err) {
      /* already open or not connected */
    }
  }
}

/** Removes an element from the top layer. */
function hideFromTopLayer(el) {
  if (supportsPopover(el)) {
    try {
      el.hidePopover();
    } catch (err) {
      /* already closed */
    }
  }
}

let openTooltip = null;

/**
 * Reports whether an open menu sits over the page outside the trigger. A
 * Tooltip never shows over an open ActionMenu, a server row menu or another
 * anchored overlay, unless its trigger is inside that surface.
 */
function menuCovers(trigger) {
  const layers = Array.from(document.querySelectorAll("[data-overlay], [data-action-menu], .row-menu"));
  return layers.some((layer) => {
    if (layer.contains(trigger)) {
      return false;
    }
    if (layer.hasAttribute("data-overlay")) {
      return layer.isConnected;
    }
    try {
      return layer.matches(":popover-open");
    } catch (err) {
      return false;
    }
  });
}

/** Hides the open Tooltip, if any, as when a menu opens over it. */
export function hideTooltips() {
  if (openTooltip) {
    openTooltip.hide();
  }
}

/**
 * Attaches a Primer Tooltip to an IconButton or an icon-only segment. The
 * tooltip is a popover element with aria-hidden. The trigger references it
 * with aria-labelledby (type "label") or aria-describedby (type
 * "description"). It opens 50 ms after hover or at once on keyboard focus,
 * and closes when the pointer leaves, on blur and on Escape, which does not
 * move focus. Touch input never opens it.
 * @param {HTMLElement} trigger
 * @param {string} text
 * @param {{type?: "label" | "description", keybindingHint?: string, direction?: "s" | "n"}} [options]
 * @returns {{element: HTMLElement, show: Function, hide: Function, setText: (text: string) => void, destroy: Function}}
 */
export function tooltip(trigger, text, { type = "label", keybindingHint: hint, direction = "s" } = {}) {
  const tip = h("span", { class: "tooltip", role: "tooltip", "aria-hidden": "true", popover: "manual", dataset: { direction } });
  const textEl = h("span", { class: "tooltip-text", text });
  tip.append(textEl);
  if (hint) {
    tip.append(keybindingHint(hint));
  }
  const id = ensureId(tip, "tooltip");
  const attr = type === "description" ? "aria-describedby" : "aria-labelledby";
  trigger.removeAttribute("title");
  addRef(trigger, attr, id);
  let timer = 0;
  let shown = false;
  let pointerInside = false;

  const place = () => {
    const rect = trigger.getBoundingClientRect();
    const width = tip.offsetWidth;
    const height = tip.offsetHeight;
    let top = direction === "n" ? rect.top - height - 4 : rect.bottom + 4;
    if (direction !== "n" && top + height > window.innerHeight - 8) {
      top = rect.top - height - 4;
    } else if (direction === "n" && top < 8) {
      top = rect.bottom + 4;
    }
    let left = rect.left + rect.width / 2 - width / 2;
    left = Math.max(8, Math.min(left, window.innerWidth - width - 8));
    tip.style.top = Math.round(Math.max(8, top)) + "px";
    tip.style.left = Math.round(left) + "px";
  };
  const show = () => {
    clearTimeout(timer);
    if (shown || !trigger.isConnected || menuCovers(trigger)) {
      return;
    }
    if (openTooltip && openTooltip !== api) {
      openTooltip.hide();
    }
    const host = trigger.closest("dialog[open]") || document.body;
    if (tip.parentNode !== host) {
      host.appendChild(tip);
    }
    shown = true;
    tip.setAttribute("data-open", "true");
    showInTopLayer(tip);
    place();
    openTooltip = api;
  };
  const hide = () => {
    clearTimeout(timer);
    if (!shown) {
      return;
    }
    shown = false;
    tip.removeAttribute("data-open");
    hideFromTopLayer(tip);
    if (openTooltip === api) {
      openTooltip = null;
    }
  };
  const onEnter = (event) => {
    if (event.pointerType === "touch") {
      return;
    }
    pointerInside = true;
    clearTimeout(timer);
    timer = later(show, 50);
  };
  const onLeave = (event) => {
    pointerInside = false;
    const to = event.relatedTarget;
    if (to && (tip.contains(to) || trigger.contains(to))) {
      return;
    }
    clearTimeout(timer);
    timer = later(() => {
      if (!pointerInside) {
        hide();
      }
    }, 50);
  };
  const onFocus = () => {
    if (hasVisibleFocus(trigger)) {
      show();
    }
  };
  const onBlur = () => hide();
  const onKey = (event) => {
    if (event.key === "Escape" && shown) {
      event.preventDefault();
      event.stopPropagation();
      hide();
    }
  };
  const onDown = () => hide();
  trigger.addEventListener("pointerenter", onEnter);
  trigger.addEventListener("pointerleave", onLeave);
  trigger.addEventListener("focus", onFocus);
  trigger.addEventListener("blur", onBlur);
  trigger.addEventListener("keydown", onKey);
  trigger.addEventListener("pointerdown", onDown);
  tip.addEventListener("pointerenter", () => {
    pointerInside = true;
    clearTimeout(timer);
  });
  tip.addEventListener("pointerleave", onLeave);
  // The label must exist in the page for aria-labelledby, even while hidden.
  document.body.appendChild(tip);
  const api = {
    element: tip,
    show,
    hide,
    setText(next) {
      textEl.textContent = next;
    },
    destroy() {
      hide();
      trigger.removeEventListener("pointerenter", onEnter);
      trigger.removeEventListener("pointerleave", onLeave);
      trigger.removeEventListener("focus", onFocus);
      trigger.removeEventListener("blur", onBlur);
      trigger.removeEventListener("keydown", onKey);
      trigger.removeEventListener("pointerdown", onDown);
      removeRef(trigger, attr, id);
      tip.remove();
    },
  };
  trigger._tooltip = api;
  return api;
}

/* ---------- Overlay positioning ---------- */

/**
 * Places a fixed overlay next to an anchor rectangle, 4 px outside it. It
 * flips to the other side, then to the side of the anchor, and keeps 8 px
 * from the viewport edges. An overlay taller than the viewport less the two
 * margins gets that height as its maximum and scrolls inside.
 * @param {HTMLElement} panel
 * @param {{top: number, bottom: number, left: number, right: number, width: number, height: number}} rect
 * @param {string} placement "bottom-start", "bottom-end", "top-start" or "top-end"
 */
export function placeOverlay(panel, rect, placement = "bottom-start") {
  const gap = 4;
  const margin = 8;
  const vw = window.innerWidth;
  const vh = window.innerHeight;
  const width = panel.offsetWidth;
  panel.style.maxHeight = "";
  if (panel.offsetHeight > vh - 2 * margin) {
    panel.style.maxHeight = Math.max(0, vh - 2 * margin) + "px";
  }
  const height = panel.offsetHeight;
  const [side, align] = String(placement).split("-");
  let top;
  const below = rect.bottom + gap;
  const above = rect.top - gap - height;
  const fitsBelow = below + height <= vh - margin;
  const fitsAbove = above >= margin;
  if (side === "top") {
    top = fitsAbove || !fitsBelow ? above : below;
  } else {
    top = fitsBelow || !fitsAbove ? below : above;
  }
  let left = align === "end" ? rect.right - width : rect.left;
  if (!fitsBelow && !fitsAbove) {
    // Neither side fits: try beside the anchor.
    const after = rect.right + gap;
    const before = rect.left - gap - width;
    if (after + width <= vw - margin) {
      left = after;
      top = rect.top;
    } else if (before >= margin) {
      left = before;
      top = rect.top;
    }
  }
  left = Math.max(margin, Math.min(left, vw - width - margin));
  top = Math.max(margin, Math.min(top, vh - height - margin));
  panel.style.left = Math.round(left) + "px";
  panel.style.top = Math.round(top) + "px";
}

/* ---------- AnchoredOverlay ---------- */

const overlayStack = [];

/** The top open anchored overlay, if any. */
function topOverlay() {
  while (overlayStack.length && !(overlayStack[overlayStack.length - 1].element.ownerDocument === document && overlayStack[overlayStack.length - 1].element.isConnected)) {
    overlayStack.pop();
  }
  return overlayStack[overlayStack.length - 1] || null;
}

let outsideListening = false;

function onOutsidePointer(event) {
  const layer = topOverlay();
  if (!layer || layer.keepOnOutside) {
    return;
  }
  if (layer.element.contains(event.target) || (layer.anchor && layer.anchor.contains && layer.anchor.contains(event.target))) {
    return;
  }
  layer.close({ restore: false, reason: "outside" });
}

function onViewportChange(event) {
  for (const layer of overlayStack) {
    if (event && event.type === "scroll" && layer.element.contains(event.target)) {
      continue;
    }
    layer.reposition();
  }
}

function listenOutside(on) {
  if (on && !outsideListening) {
    document.addEventListener("pointerdown", onOutsidePointer, true);
    if (typeof window.addEventListener === "function") {
      window.addEventListener("resize", onViewportChange);
    }
    document.addEventListener("scroll", onViewportChange, true);
    outsideListening = true;
  } else if (!on && outsideListening) {
    document.removeEventListener("pointerdown", onOutsidePointer, true);
    if (typeof window.removeEventListener === "function") {
      window.removeEventListener("resize", onViewportChange);
    }
    document.removeEventListener("scroll", onViewportChange, true);
    outsideListening = false;
  }
}

const OVERLAY_WIDTHS = { xsmall: 192, small: 320, medium: 480, large: 640, xlarge: 960, auto: 0 };

/**
 * Creates the floating layer shared by AnchoredOverlay, ActionMenu,
 * SelectPanel and Autocomplete. It sits in the top layer through the popover
 * attribute, inside the dialog that holds the anchor, and goes full screen
 * with a Close button on narrow viewports when fullscreen is set.
 */
function createLayer(anchor, { className = "", role = null, label = "", labelledBy = "", placement = "bottom-start", point = null, width = "auto", fullscreen = true, onClose = null, keepOnOutside = false, restoreTo = null, matchAnchorWidth = false, closeLabel = "Close" }) {
  // Opening a layer closes other layers, except the one that holds the anchor.
  for (let i = overlayStack.length - 1; i >= 0; i--) {
    const layer = overlayStack[i];
    if (anchor && layer.element.contains(anchor)) {
      break;
    }
    layer.close({ restore: false, reason: "replaced" });
  }
  const panel = h("div", { class: ("overlay " + className).trim(), popover: "manual", dataset: { overlay: "" } });
  if (role) {
    panel.setAttribute("role", role);
  }
  if (label) {
    panel.setAttribute("aria-label", label);
  }
  if (labelledBy) {
    panel.setAttribute("aria-labelledby", labelledBy);
  }
  if (fullscreen) {
    panel.setAttribute("data-fullscreen-narrow", "true");
  }
  const px = typeof width === "number" ? width : OVERLAY_WIDTHS[width] || 0;
  if (px) {
    panel.style.width = `min(${px}px, calc(100vw - 16px))`;
  }
  const returnFocus = restoreTo || document.activeElement;
  const host = (anchor && anchor.closest && anchor.closest("dialog[open]")) || document.body;
  let closed = false;
  const layer = {
    anchor,
    element: panel,
    keepOnOutside,
    reposition() {
      if (closed) {
        return;
      }
      if (fullscreen && isNarrow()) {
        panel.style.top = "";
        panel.style.left = "";
        panel.style.maxHeight = "";
        return;
      }
      if (matchAnchorWidth && anchor) {
        panel.style.width = Math.round(anchor.getBoundingClientRect().width) + "px";
      }
      const rect = point ? { top: point.y, bottom: point.y, left: point.x, right: point.x, width: 0, height: 0 } : anchor.getBoundingClientRect();
      placeOverlay(panel, rect, placement);
    },
    close({ restore = true, reason = "close" } = {}) {
      if (closed) {
        return;
      }
      closed = true;
      const index = overlayStack.indexOf(layer);
      if (index >= 0) {
        overlayStack.splice(index, 1);
      }
      if (!overlayStack.length) {
        listenOutside(false);
      }
      hideFromTopLayer(panel);
      panel.remove();
      if (anchor && anchor.hasAttribute && anchor.hasAttribute("aria-expanded")) {
        anchor.setAttribute("aria-expanded", "false");
      }
      if (restore) {
        if (!restoreFocus(anchor && anchor.isConnected ? anchor : null)) {
          restoreFocus(returnFocus);
        }
      }
      if (typeof onClose === "function") {
        onClose(reason);
      }
    },
    get closed() {
      return closed;
    },
  };
  if (fullscreen) {
    // The name says what closes, so it stays unique beside a Dialog's own Close button (docs/primer.md 5.0).
    const closeButton = iconButton({ icon: "x", label: closeLabel, variant: "invisible", size: "medium", tooltip: false, className: "overlay-close", onClick: () => layer.close({ reason: "close-button" }) });
    panel.appendChild(closeButton);
  }
  panel.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && !event.defaultPrevented) {
      event.preventDefault();
      event.stopPropagation();
      layer.close({ reason: "escape" });
    }
  });
  host.appendChild(panel);
  hideTooltips();
  showInTopLayer(panel);
  overlayStack.push(layer);
  listenOutside(true);
  if (anchor && anchor.setAttribute && !point) {
    anchor.setAttribute("aria-expanded", "true");
  }
  return layer;
}

/**
 * Opens a Primer AnchoredOverlay: the ActionMenu surface next to an anchor
 * with role dialog. Focus moves to the first interactive element and returns
 * to the anchor on close. Escape and a click outside close it. On narrow
 * viewports it fills the screen with a Close button. Opening it again for the
 * same anchor closes it.
 * @param {Element} anchor
 * @param {Node} content
 * @param {{placement?: string, width?: "xsmall" | "small" | "medium" | "large" | "xlarge" | "auto" | number, label?: string, role?: string | null, className?: string, focus?: boolean, onClose?: Function, padding?: "none" | "condensed" | "normal"}} [options]
 * @returns {{close: Function, element: HTMLElement | null, reposition: Function}}
 */
export function anchoredOverlay(anchor, content, { placement = "bottom-start", width = "auto", label = "", role = "dialog", className = "", focus = true, onClose, padding = "normal" } = {}) {
  const current = topOverlay();
  if (current && current.anchor === anchor) {
    current.close({ restore: false, reason: "toggle" });
    return { close() {}, element: null, reposition() {} };
  }
  if (!label && anchor && anchor.getAttribute) {
    label = anchor.getAttribute("aria-label") || (anchor.textContent || "").trim();
  }
  const layer = createLayer(anchor, { className: ("anchored-overlay popover " + className).trim(), role, label, placement, width, onClose });
  const body = h("div", { class: "overlay-body", dataset: { padding } }, content);
  layer.element.appendChild(body);
  layer.reposition();
  if (focus) {
    const first = focusables(body)[0] || focusables(layer.element)[0];
    if (first) {
      first.focus({ preventScroll: true });
    }
  }
  return { close: (opts) => layer.close(opts), element: layer.element, reposition: () => layer.reposition() };
}

/* ---------- ActionMenu ---------- */

/**
 * Builds the leading, label, description and trailing slots of an ActionList
 * item. checkSlot adds a check column before the leading visual. checkLeading
 * puts the check in the leading slot instead: a checked item shows the check
 * and an unchecked item shows its icon, so the text inset stays 40 px.
 */
function actionListContent(item, { checkSlot = false, checkLeading = false } = {}) {
  const parts = [];
  if (checkSlot) {
    parts.push(h("span", { class: "action-list-check", "aria-hidden": "true" }, icon("check", 16)));
  }
  if (checkLeading && item.checked !== undefined) {
    const glyph = item.checked ? icon("check", 16) : item.icon || item.leadingVisual ? visual(item.icon || item.leadingVisual) : null;
    parts.push(h("span", { class: "action-list-leading" + (item.checked ? " is-checked" : ""), "aria-hidden": "true" }, glyph));
  } else if (item.icon || item.leadingVisual) {
    parts.push(h("span", { class: "action-list-leading", "aria-hidden": "true" }, visual(item.icon || item.leadingVisual)));
  }
  const labelId = uid("al-label");
  const text = h("span", { class: "action-list-text" + (item.description && item.descriptionLayout !== "inline" ? " has-block-description" : "") }, h("span", { class: "action-list-label", id: labelId, text: item.label }));
  if (item.description) {
    text.appendChild(h("span", { class: "action-list-description" + (item.descriptionLayout === "inline" ? " is-inline" : ""), text: item.description }));
  }
  if (item.inactive && item.inactiveText) {
    text.appendChild(h("span", { class: "action-list-inactive-text", text: item.inactiveText }));
  }
  parts.push(text);
  const trailing = item.shortcut || item.trailingText;
  if (trailing) {
    parts.push(h("kbd", { class: "action-list-trailing menu-hint", text: trailing }));
  } else if (item.trailingVisual) {
    parts.push(h("span", { class: "action-list-trailing", "aria-hidden": "true" }, visual(item.trailingVisual)));
  }
  if (item.count !== undefined && item.count !== null) {
    parts.push(h("span", { class: "action-list-trailing" }, counterLabel(item.count)));
  }
  return { parts, labelId };
}

/**
 * Opens a Primer ActionMenu for an anchor. The menu has role menu with
 * menuitem, menuitemradio and menuitemcheckbox items, groups with headings
 * and separators. Focus moves to the first item. ArrowDown and ArrowUp move
 * and wrap, Home and PageUp go to the first item, End and PageDown to the
 * last, and a typed letter moves to the next item that starts with it. Enter
 * or Space runs an item and closes the menu, and focus returns to the anchor
 * unless the item navigates. Escape closes and returns focus. Tab closes and
 * moves focus onward. A click outside closes. On narrow viewports the menu
 * fills the screen with a Close button. Opening it again for the same anchor
 * closes it.
 * @param {Element} anchor
 * @param {object[]} items {label, description, descriptionLayout, icon,
 *   shortcut, trailingVisual, onClick, href, danger, disabled, inactive,
 *   inactiveText, checked, selectionType: "single" | "multiple", confirm} or
 *   {separator: true} or {group: heading, description}. A group heading
 *   with a description shows it below the heading and describes the group.
 *   Checkable items get a check column only in a section, between separators
 *   and group headings, whose items have no icons. In a section with icons
 *   the check replaces the icon of a checked item. confirm opens a
 *   ConfirmationDialog first: a string is its confirm button label, or pass
 *   {title, body, confirmLabel}. count adds a trailing CounterLabel.
 *   {custom: node, focusables} places a node in the menu, and its
 *   focusables, such as menuitemradio segments, join the arrow key order.
 * @param {{placement?: string, point?: {x: number, y: number}, focus?: "first" | "last" | "none", label?: string, onClose?: Function}} [options]
 * @returns {{close: Function, element: HTMLElement | null}}
 */
export function openMenu(anchor, items, { placement = "bottom-start", point = null, focus = "first", label = "", onClose } = {}) {
  const list = (items || []).filter(Boolean);
  if (list.length && list.every((item) => item.custom)) {
    // Custom content is not a menu. It opens as an AnchoredOverlay.
    const wrap = h("div", { class: "overlay-custom" }, list.map((item) => item.custom));
    return anchoredOverlay(anchor, wrap, { placement, onClose });
  }
  const current = topOverlay();
  if (current && current.anchor === anchor && !point) {
    current.close({ restore: false, reason: "toggle" });
    return { close() {}, element: null };
  }
  if (anchor && anchor.setAttribute) {
    anchor.setAttribute("aria-haspopup", "true");
  }
  const anchorId = anchor && anchor.setAttribute ? ensureId(anchor, "menu-anchor") : "";
  const menu = h("ul", { class: "action-list action-menu-list menu-list", role: "menu", tabindex: "-1" });
  if (label) {
    menu.setAttribute("aria-label", label);
  } else if (anchorId) {
    menu.setAttribute("aria-labelledby", anchorId);
  }
  // A section runs between separators and group headings. Its items share
  // one leading layout, so every text in the section starts at one inset.
  const sectionOf = new Map();
  let section = [];
  const sections = [section];
  for (const item of list) {
    if (item.separator || item.group) {
      section = [];
      sections.push(section);
      continue;
    }
    section.push(item);
    sectionOf.set(item, section);
  }
  const layoutOf = new Map(
    sections.map((items) => {
      const checkable = items.some((item) => item.checked !== undefined);
      const icons = items.some((item) => item.icon || item.leadingVisual);
      return [items, { checkSlot: checkable && !icons, checkLeading: checkable && icons }];
    })
  );
  const entries = [];
  let groupList = null;
  let layer = null;
  const run = (item, el, event) => {
    if (item.disabled || item.inactive) {
      event.preventDefault();
      return;
    }
    const navigates = !!item.href;
    if (!navigates) {
      event.preventDefault();
    }
    if (layer) {
      layer.close({ restore: !navigates, reason: "select" });
    }
    const go = () => {
      if (typeof item.onClick === "function") {
        return item.onClick(event);
      }
      if (typeof item.onSelect === "function") {
        return item.onSelect(event);
      }
      return undefined;
    };
    if (item.confirm && !navigates) {
      const conf = typeof item.confirm === "object" ? item.confirm : {};
      const actionLabel = typeof item.confirm === "string" ? item.confirm.replace(/^Confirm\s+/i, "") : conf.confirmLabel || item.label;
      confirmDialog({
        title: conf.title || `${item.label}?`,
        body: conf.body || "",
        confirmLabel: actionLabel.charAt(0).toUpperCase() + actionLabel.slice(1),
        danger: item.danger !== false,
        onConfirm: go,
        returnFocus: anchor,
      });
      return;
    }
    go();
  };
  for (const item of list) {
    if (item.separator) {
      groupList = null;
      menu.appendChild(h("li", { class: "action-list-divider menu-sep", role: "separator", "aria-hidden": "true" }));
      continue;
    }
    if (item.group) {
      const headingId = uid("menu-group");
      groupList = h("ul", { class: "action-list-group-list", role: "group", "aria-labelledby": headingId });
      // The heading text names the group. The description describes it.
      const heading = h("span", { class: "action-list-heading menu-label", role: "presentation" }, h("span", { class: "action-list-heading-text", id: headingId, text: item.group }));
      if (item.description) {
        const descriptionId = uid("menu-group-desc");
        heading.appendChild(h("span", { class: "action-list-heading-description", id: descriptionId, text: item.description }));
        groupList.setAttribute("aria-describedby", descriptionId);
      }
      menu.appendChild(h("li", { class: "action-list-group", role: "none" }, heading, groupList));
      continue;
    }
    const target = groupList || menu;
    if (item.custom) {
      target.appendChild(h("li", { class: "action-list-custom", role: "none" }, item.custom));
      for (const el of item.focusables || []) {
        entries.push({ el, item: {}, label: String(el.getAttribute("aria-label") || el.textContent || "").toLowerCase() });
      }
      continue;
    }
    const { parts } = actionListContent(item, layoutOf.get(sectionOf.get(item)) || {});
    let role = "menuitem";
    if (item.checked !== undefined) {
      role = item.selectionType === "multiple" ? "menuitemcheckbox" : "menuitemradio";
    }
    const classes = "action-list-item menu-item" + (item.danger ? " is-danger danger" : "") + (item.inactive ? " is-inactive" : "");
    const el = item.href ? h("a", { class: classes, href: item.href, role, tabindex: "-1" }, parts) : h("button", { class: classes, type: "button", role, tabindex: "-1" }, parts);
    if (item.checked !== undefined) {
      el.setAttribute("aria-checked", item.checked ? "true" : "false");
    }
    if (item.disabled || item.inactive) {
      el.setAttribute("aria-disabled", "true");
    }
    if (item.description) {
      const describedId = uid("al-desc");
      const desc = el.querySelector(".action-list-description");
      desc.id = describedId;
      el.setAttribute("aria-describedby", describedId);
    }
    el.addEventListener("click", (event) => run(item, el, event));
    target.appendChild(h("li", { class: "action-list-row", role: "none" }, el));
    entries.push({ el, item, label: String(item.label || "").toLowerCase() });
  }
  layer = createLayer(anchor, { className: "action-menu menu-popover popover", placement, point, onClose, restoreTo: anchor, closeLabel: "Close menu" });
  if (list.filter((item) => !item.separator && !item.group).length <= 8) {
    layer.element.setAttribute("data-size", "short");
  }
  layer.element.appendChild(menu);
  layer.reposition();
  const focusable = () => entries.filter((entry) => entry.el.getAttribute("aria-disabled") !== "true" || entry.item.inactive);
  const focusAt = (index) => {
    const pool = focusable();
    if (!pool.length) {
      return;
    }
    const entry = pool[((index % pool.length) + pool.length) % pool.length];
    entry.el.focus({ preventScroll: false });
  };
  const indexOfFocus = () => focusable().findIndex((entry) => entry.el === document.activeElement);
  const keys = typeahead(500);
  layer.element.addEventListener("keydown", (event) => {
    const pool = focusable();
    const index = indexOfFocus();
    switch (event.key) {
      case "ArrowDown":
        event.preventDefault();
        focusAt(index < 0 ? 0 : index + 1);
        return;
      case "ArrowUp":
        event.preventDefault();
        focusAt(index < 0 ? pool.length - 1 : index - 1);
        return;
      case "Home":
      case "PageUp":
        event.preventDefault();
        focusAt(0);
        return;
      case "End":
      case "PageDown":
        event.preventDefault();
        focusAt(pool.length - 1);
        return;
      case "Tab":
        // Focus goes back to the anchor first, so the browser moves it onward from there.
        layer.close({ restore: true, reason: "tab" });
        return;
      case "Enter":
      case " ":
        if (index >= 0) {
          event.preventDefault();
          pool[index].el.click();
        }
        return;
      default:
        if (isPrintable(event)) {
          const typed = keys.push(event.key);
          const hit = matchTypeahead(pool.map((entry) => entry.label), index, typed);
          if (hit >= 0) {
            event.preventDefault();
            focusAt(hit);
          }
        }
    }
  });
  if (focus === "last") {
    focusAt(-1);
  } else if (focus !== "none") {
    focusAt(0);
  }
  return { close: (opts) => layer.close(opts), element: layer.element };
}

/**
 * Wires a trigger button to an ActionMenu. Click, Enter and Space open the
 * menu at its first item, and ArrowDown and ArrowUp open it at the first or
 * last item. The trigger gets aria-haspopup and aria-expanded.
 * @param {HTMLElement} trigger
 * @param {object[] | (() => object[])} items
 * @param {object} [options] as openMenu
 * @returns {{open: Function, close: Function, destroy: Function}}
 */
export function actionMenu(trigger, items, options = {}) {
  let handle = null;
  trigger.setAttribute("aria-haspopup", "true");
  trigger.setAttribute("aria-expanded", "false");
  const open = (focus = "first") => {
    handle = openMenu(trigger, typeof items === "function" ? items() : items, Object.assign({}, options, { focus }));
    return handle;
  };
  const onClick = (event) => {
    event.preventDefault();
    open("first");
  };
  const onKey = (event) => {
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      if (trigger.getAttribute("aria-expanded") !== "true") {
        open(event.key === "ArrowUp" ? "last" : "first");
      }
    }
  };
  trigger.addEventListener("click", onClick);
  trigger.addEventListener("keydown", onKey);
  return {
    open,
    close: () => handle && handle.close(),
    destroy() {
      trigger.removeEventListener("click", onClick);
      trigger.removeEventListener("keydown", onKey);
    },
  };
}

/* ---------- SelectPanel ---------- */

/** Case-insensitive match on the label and description of an item. */
function itemMatches(item, query) {
  const q = String(query || "").trim().toLowerCase();
  if (!q) {
    return true;
  }
  return String(item.label || "").toLowerCase().includes(q) || String(item.description || "").toLowerCase().includes(q);
}

/**
 * Opens a Primer SelectPanel anchored to a trigger. It is a dialog with a
 * title, a filter input and a listbox of options. Real focus stays in the
 * filter input and aria-activedescendant marks the active option. ArrowDown,
 * ArrowUp, Home and End move the active option, Enter selects it and Escape
 * closes. The result count is announced. Selected items come first when the
 * panel opens and after the filter is cleared.
 *
 * Anchored single select applies on selection and closes. Anchored multiple
 * select applies when the panel closes. On narrow viewports, or with variant
 * "modal", the panel fills the screen and a footer with Cancel and Save
 * applies the choice.
 * @param {Element} anchor
 * @param {{title?: string, subtitle?: string, items?: {id: string, label: string, description?: string, icon?: string}[], selected?: string | string[] | null, multiple?: boolean, variant?: "anchored" | "modal", placeholder?: string, filterLabel?: string, onSelect?: (selection: string | string[] | null) => void, onCancel?: Function, onFilter?: (query: string) => Promise<object[]> | object[], emptyTitle?: string, emptyBody?: string, placement?: string, onClose?: Function}} options
 * @returns {{close: Function, element: HTMLElement | null, setItems: (items: object[]) => void, setLoading: (on: boolean, message?: string) => void, setError: (message: string) => void}}
 */
export function selectPanel(anchor, { title = "Select an item", subtitle = "", items = [], selected = null, multiple = false, variant = "anchored", placeholder = "Filter items", filterLabel = "Filter items", onSelect, onCancel, onFilter, emptyTitle = "No items available", emptyBody = "", placement = "bottom-start", onClose } = {}) {
  const current = topOverlay();
  if (current && current.anchor === anchor) {
    current.close({ restore: false, reason: "toggle" });
    return { close() {}, element: null, setItems() {}, setLoading() {}, setError() {} };
  }
  anchor.setAttribute("aria-haspopup", "dialog");
  const modal = variant === "modal" || isNarrow();
  const initial = new Set(multiple ? (Array.isArray(selected) ? selected : []) : selected !== null && selected !== undefined ? [selected] : []);
  const chosen = new Set(initial);
  let all = items.slice();
  let order = [];
  let visible = [];
  let active = -1;
  let applied = false;
  const titleId = uid("sp-title");
  const listId = uid("sp-list");
  const layer = createLayer(anchor, {
    className: "select-panel popover" + (modal ? " is-modal" : ""),
    role: "dialog",
    labelledBy: titleId,
    placement,
    width: "small",
    fullscreen: true,
    onClose: (reason) => {
      if (!applied && multiple && !modal && reason !== "cancel" && reason !== "replaced") {
        applyMultiple();
      } else if (!applied && typeof onCancel === "function" && (reason === "escape" || reason === "cancel" || reason === "close-button")) {
        onCancel();
      }
      if (typeof onClose === "function") {
        onClose(reason);
      }
    },
  });
  const panel = layer.element;
  // The header holds its own Close button, so the generic one goes.
  const genericClose = panel.querySelector(".overlay-close");
  if (genericClose) {
    genericClose.remove();
  }
  const input = h("input", {
    class: "text-input-control",
    type: "text",
    role: "combobox",
    autocomplete: "off",
    spellcheck: "false",
    "aria-label": filterLabel,
    "aria-controls": listId,
    "aria-expanded": "true",
    "aria-autocomplete": "list",
    placeholder,
  });
  const list = h("ul", { class: "action-list select-panel-list", role: "listbox", id: listId, "aria-labelledby": titleId });
  if (multiple) {
    list.setAttribute("aria-multiselectable", "true");
  }
  const message = h("div", { class: "select-panel-message", hidden: true });
  const count = h("div", { class: "sr-only", role: "status", "aria-live": "polite", "aria-atomic": "true" });
  const header = h(
    "div",
    { class: "select-panel-header" },
    h("div", { class: "select-panel-titles" }, h("h2", { class: "select-panel-title", id: titleId, text: title }), subtitle ? h("p", { class: "select-panel-subtitle", text: subtitle }) : null),
    iconButton({ icon: "x", label: "Close", size: "small", tooltip: false, className: "select-panel-close", onClick: () => layer.close({ reason: "close-button" }) })
  );
  const filter = h("div", { class: "select-panel-filter" }, h("span", { class: "text-input text-input-block has-leading-visual" }, h("span", { class: "text-input-visual", "aria-hidden": "true" }, icon("search", 16)), input));
  panel.append(header, filter, list, message, count);
  let saveButton = null;
  if (modal) {
    saveButton = button({ label: "Save", variant: "primary", onClick: () => apply() });
    panel.appendChild(h("div", { class: "select-panel-footer" }, button({ label: "Cancel", onClick: () => layer.close({ reason: "cancel" }) }), saveButton));
  }

  const applyMultiple = () => {
    applied = true;
    if (typeof onSelect === "function") {
      const same = chosen.size === initial.size && [...chosen].every((id) => initial.has(id));
      if (!same) {
        onSelect([...chosen]);
      }
    }
  };
  const apply = () => {
    if (multiple) {
      applyMultiple();
    } else {
      applied = true;
      if (typeof onSelect === "function") {
        onSelect(chosen.size ? [...chosen][0] : null);
      }
    }
    layer.close({ reason: "save" });
  };
  const sortSelectedFirst = () => {
    order = all.filter((item) => chosen.has(item.id)).concat(all.filter((item) => !chosen.has(item.id)));
  };
  const optionId = (item) => listId + "-" + String(item.id).replace(/[^\w-]/g, "_");
  const setActive = (index) => {
    active = visible.length ? Math.max(0, Math.min(index, visible.length - 1)) : -1;
    for (const [i, el] of Array.from(list.children).entries()) {
      el.classList.toggle("is-active", i === active);
    }
    if (active >= 0) {
      const el = list.children[active];
      input.setAttribute("aria-activedescendant", el.id);
      if (typeof el.scrollIntoView === "function") {
        el.scrollIntoView({ block: "nearest" });
      }
    } else {
      input.removeAttribute("aria-activedescendant");
    }
  };
  const toggle = (item) => {
    if (multiple) {
      if (chosen.has(item.id)) {
        chosen.delete(item.id);
      } else {
        chosen.add(item.id);
      }
      render(false);
      return;
    }
    chosen.clear();
    chosen.add(item.id);
    if (modal) {
      render(false);
      return;
    }
    applied = true;
    layer.close({ reason: "select" });
    if (typeof onSelect === "function") {
      onSelect(item.id);
    }
  };
  const render = (resetActive = true) => {
    const query = input.value;
    visible = order.filter((item) => itemMatches(item, query) || onFilter);
    list.replaceChildren();
    for (const item of visible) {
      const isSelected = chosen.has(item.id);
      const lead = multiple
        ? h("span", { class: "select-panel-checkbox", "aria-hidden": "true" }, icon("check", 16))
        : h("span", { class: "action-list-check", "aria-hidden": "true" }, icon("check", 16));
      const content = actionListContent(item, {}).parts;
      const el = h("li", { class: "action-list-item select-panel-option", role: "option", id: optionId(item), "aria-selected": isSelected ? "true" : "false" }, lead, content);
      el.addEventListener("pointerdown", (event) => event.preventDefault());
      el.addEventListener("click", () => {
        setActive(visible.indexOf(item));
        toggle(item);
      });
      list.appendChild(el);
    }
    message.hidden = visible.length > 0;
    if (!visible.length) {
      message.replaceChildren(h("p", { class: "select-panel-message-title", text: query ? "No matches" : emptyTitle }), emptyBody && !query ? h("p", { class: "select-panel-message-body", text: emptyBody }) : null);
    }
    count.textContent = visible.length === 1 ? "1 item" : `${visible.length} items`;
    setActive(resetActive ? 0 : active);
  };
  let filterSeq = 0;
  input.addEventListener("input", async () => {
    if (!input.value) {
      sortSelectedFirst();
    }
    if (typeof onFilter === "function") {
      const seq = ++filterSeq;
      api.setLoading(true);
      try {
        const next = await onFilter(input.value);
        if (seq !== filterSeq || layer.closed) {
          return;
        }
        all = next || [];
        sortSelectedFirst();
      } catch (err) {
        if (seq === filterSeq) {
          api.setError(errorMessage(err, "The items could not be loaded."));
        }
        return;
      }
      api.setLoading(false);
    }
    render(true);
  });
  input.addEventListener("keydown", (event) => {
    switch (event.key) {
      case "ArrowDown":
        event.preventDefault();
        setActive(active + 1);
        break;
      case "ArrowUp":
        event.preventDefault();
        setActive(active - 1);
        break;
      case "Home":
        if (visible.length) {
          event.preventDefault();
          setActive(0);
        }
        break;
      case "End":
        if (visible.length) {
          event.preventDefault();
          setActive(visible.length - 1);
        }
        break;
      case "Enter":
        event.preventDefault();
        if (active >= 0 && visible[active]) {
          toggle(visible[active]);
        } else if (modal) {
          apply();
        }
        break;
      default:
    }
  });
  panel.addEventListener("keydown", (event) => {
    if (event.key === "Tab") {
      trapFocus(event, panel);
    }
  });
  const api = {
    close: (opts) => layer.close(opts),
    element: panel,
    setItems(next) {
      all = (next || []).slice();
      sortSelectedFirst();
      render(true);
    },
    setLoading(on, text = "Loading items") {
      list.hidden = !!on;
      message.hidden = !on;
      if (on) {
        message.replaceChildren(h("div", { class: "select-panel-loading" }, spinner({ size: "medium", label: "" }), h("p", { class: "select-panel-message-body", text })));
      }
    },
    setError(text) {
      list.hidden = true;
      message.hidden = false;
      message.replaceChildren(h("span", { class: "select-panel-error-icon", "aria-hidden": "true" }, icon("alert", 16)), h("p", { class: "select-panel-message-title", text: "Items could not be loaded" }), h("p", { class: "select-panel-message-body", text }));
    },
  };
  sortSelectedFirst();
  render(true);
  layer.reposition();
  input.focus({ preventScroll: true });
  return api;
}

/* ---------- Autocomplete ---------- */

/**
 * Turns a text input into a Primer Autocomplete. The input gets role
 * combobox, and a listbox of suggestions opens 4 px under it, as wide as its
 * box. ArrowDown and ArrowUp move the active option, Enter chooses it and
 * Escape closes the list. Filtering is case-insensitive. With allowAdd, a
 * value that matches nothing offers an "Add" item. The result count is
 * announced. The list stays under the input on narrow viewports.
 * @param {HTMLInputElement} input
 * @param {{source: (query: string) => object[] | Promise<object[]>, onSelect: (item: object, input: HTMLInputElement) => void, allowAdd?: (value: string) => object | null, minLength?: number, emptyText?: string, filter?: boolean}} options
 *   items are {id, label, description, icon}. With filter false the source
 *   filters on its own.
 * @returns {{open: Function, close: Function, destroy: Function}}
 */
export function autocomplete(input, { source, onSelect, allowAdd = null, minLength = 1, emptyText = "No matches", filter = true } = {}) {
  const listId = uid("ac-list");
  input.setAttribute("role", "combobox");
  input.setAttribute("aria-autocomplete", "list");
  input.setAttribute("aria-expanded", "false");
  input.setAttribute("aria-controls", listId);
  input.setAttribute("autocomplete", "off");
  const box = input.closest(".text-input") || input;
  let layer = null;
  let list = null;
  let items = [];
  let active = -1;
  let seq = 0;

  const close = () => {
    if (layer) {
      const l = layer;
      layer = null;
      l.close({ restore: false, reason: "close" });
    }
    input.setAttribute("aria-expanded", "false");
    input.removeAttribute("aria-activedescendant");
  };
  const setActive = (index) => {
    active = items.length ? ((index % items.length) + items.length) % items.length : -1;
    if (!list) {
      return;
    }
    for (const [i, el] of Array.from(list.children).entries()) {
      const on = i === active;
      el.classList.toggle("is-active", on);
      el.setAttribute("aria-selected", on ? "true" : "false");
    }
    if (active >= 0 && list.children[active]) {
      input.setAttribute("aria-activedescendant", list.children[active].id);
    } else {
      input.removeAttribute("aria-activedescendant");
    }
  };
  const choose = (item) => {
    close();
    if (typeof onSelect === "function") {
      onSelect(item, input);
    }
  };
  const ensureLayer = () => {
    if (layer && !layer.closed) {
      return;
    }
    layer = createLayer(box, { className: "autocomplete-overlay", placement: "bottom-start", fullscreen: false, matchAnchorWidth: true, onClose: () => {
      layer = null;
      input.setAttribute("aria-expanded", "false");
    } });
    // The input is not the anchor of aria-expanded; the combobox carries it.
    box.removeAttribute("aria-expanded");
    list = h("ul", { class: "action-list autocomplete-list", role: "listbox", id: listId });
    layer.element.appendChild(list);
    input.setAttribute("aria-expanded", "true");
  };
  const renderItems = (next, query) => {
    items = next.slice();
    if (allowAdd && query && !items.some((item) => String(item.label).toLowerCase() === query.toLowerCase())) {
      const extra = allowAdd(query);
      if (extra) {
        items.push(Object.assign({ isAdd: true }, extra));
      }
    }
    ensureLayer();
    list.replaceChildren();
    if (!items.length) {
      list.appendChild(h("li", { class: "autocomplete-empty", role: "presentation", text: emptyText }));
    }
    for (const [i, item] of items.entries()) {
      const el = h("li", { class: "action-list-item autocomplete-option", role: "option", id: `${listId}-${i}`, "aria-selected": "false" }, actionListContent(item).parts);
      el.addEventListener("pointerdown", (event) => event.preventDefault());
      el.addEventListener("click", () => choose(item));
      list.appendChild(el);
    }
    announce(items.length === 1 ? "1 suggestion" : `${items.length} suggestions`);
    setActive(items.length ? 0 : -1);
    layer.reposition();
  };
  const update = async () => {
    const query = input.value.trim();
    if (query.length < minLength) {
      close();
      return;
    }
    const mine = ++seq;
    ensureLayer();
    list.replaceChildren(h("li", { class: "autocomplete-loading", role: "presentation" }, spinner({ size: "small", label: "Loading suggestions" })));
    layer.reposition();
    let next = [];
    try {
      next = (await source(query)) || [];
    } catch (err) {
      next = [];
    }
    if (mine !== seq || !input.isConnected) {
      return;
    }
    renderItems(filter ? next.filter((item) => itemMatches(item, query)) : next, query);
  };
  const onInput = () => {
    update();
  };
  const onKey = (event) => {
    const open = !!layer;
    switch (event.key) {
      case "ArrowDown":
        event.preventDefault();
        if (!open) {
          update();
        } else {
          setActive(active + 1);
        }
        break;
      case "ArrowUp":
        if (open) {
          event.preventDefault();
          setActive(active - 1);
        }
        break;
      case "Enter":
        if (open && active >= 0 && items[active]) {
          event.preventDefault();
          choose(items[active]);
        }
        break;
      case "Escape":
        if (open) {
          event.preventDefault();
          event.stopPropagation();
          close();
        }
        break;
      default:
    }
  };
  const onBlur = () => {
    later(() => {
      if (document.activeElement !== input) {
        close();
      }
    }, 0);
  };
  input.addEventListener("input", onInput);
  input.addEventListener("keydown", onKey);
  input.addEventListener("blur", onBlur);
  return {
    open: update,
    close,
    destroy() {
      close();
      input.removeEventListener("input", onInput);
      input.removeEventListener("keydown", onKey);
      input.removeEventListener("blur", onBlur);
    },
  };
}

/* ---------- Dialog ---------- */

const dialogStack = [];

/** The top open dialog element, if any. */
function topDialog() {
  while (dialogStack.length && !(dialogStack[dialogStack.length - 1].element.ownerDocument === document && dialogStack[dialogStack.length - 1].element.isConnected)) {
    dialogStack.pop();
  }
  const top = dialogStack[dialogStack.length - 1];
  return top ? top.element : null;
}

let scrollLock = null;

/** Locks page scrolling while a dialog is open, keeping the scrollbar width. */
function lockScroll() {
  if (scrollLock && scrollLock.body !== document.body) {
    scrollLock = null;
  }
  if (scrollLock) {
    scrollLock.count += 1;
    return;
  }
  const bodyStyle = document.body.style;
  const root = document.documentElement;
  const gutter = typeof window.innerWidth === "number" && root && typeof root.clientWidth === "number" ? Math.max(0, window.innerWidth - root.clientWidth) : 0;
  scrollLock = { body: document.body, count: 1, overflow: bodyStyle.overflow || "", paddingRight: bodyStyle.paddingRight || "" };
  bodyStyle.overflow = "hidden";
  if (gutter > 0) {
    bodyStyle.paddingRight = gutter + "px";
  }
  document.body.setAttribute("data-scroll-locked", "true");
}

/** Releases one scroll lock. The page scrolls again after the last one. */
function unlockScroll() {
  if (!scrollLock) {
    return;
  }
  scrollLock.count -= 1;
  if (scrollLock.count > 0) {
    return;
  }
  document.body.style.overflow = scrollLock.overflow;
  document.body.style.paddingRight = scrollLock.paddingRight;
  document.body.removeAttribute("data-scroll-locked");
  scrollLock = null;
}

const DIALOG_WIDTHS = ["small", "medium", "large", "xlarge"];

/** Reports whether a node holds a field a person types into or chooses from. */
function holdsInputs(node) {
  return Array.from(node.querySelectorAll("input")).some((el) => el.getAttribute("type") !== "hidden") || !!node.querySelector("textarea, select, [contenteditable]");
}

/**
 * Opens a Primer Dialog with showModal. The header holds an h1 title, an
 * optional subtitle and a Close button. Focus moves to the first interactive
 * element, which is usually the Close button, or to initialFocus. Tab stays
 * inside. Escape, the Close button and a click on the backdrop close it,
 * except that a backdrop click does nothing while a form inside has unsaved
 * changes. Focus returns to the trigger, or to returnFocus when the trigger
 * is gone. Page scrolling is locked while it is open.
 *
 * On narrow viewports a dialog that holds input fields fills the screen, a
 * dialog without fields is a bottom sheet, and role "alertdialog" stays
 * centred. A footer button runs onClick(close, event). The dialog stays open
 * while a returned promise runs, closes when it resolves unless it resolves
 * false, and shows a critical Banner under the header when it rejects.
 *
 * position "left" opens a left side sheet on every viewport, as Primer's
 * Dialog position="left" for global navigation: full height, 320 px wide up
 * to calc(100vw - 64px), 12 px radius on the end corners, sliding in from
 * the start edge over 250 ms. narrow does not apply to it.
 * @param {{title: string, subtitle?: string, content?: Node | string | ((api: object) => Node), footer?: {label: string, variant?: string, onClick?: Function, autofocus?: boolean}[], actions?: object[], width?: "small" | "medium" | "large" | "xlarge", height?: "auto" | "small" | "large", align?: "center" | "top", position?: "center" | "left", narrow?: "auto" | "fullscreen" | "bottom" | "center", role?: "dialog" | "alertdialog", hideTitle?: boolean, initialFocus?: Element | string, returnFocus?: Element, onClose?: (reason: string) => void, className?: string}} options
 * @returns {{close: (reason?: string) => void, element: HTMLDialogElement, body: HTMLElement, footer: HTMLElement | null, showError: (message: string) => HTMLElement, buttons: HTMLElement[]}}
 */
export function openDialog({ title = "", subtitle = "", content, footer, actions, width = "xlarge", height = "auto", align = "center", position = "center", narrow = "auto", role = "dialog", hideTitle = false, initialFocus = null, returnFocus = null, onClose, className = "" } = {}) {
  const trigger = document.activeElement && document.activeElement !== document.body ? document.activeElement : null;
  const titleId = uid("dialog-title");
  const subtitleId = uid("dialog-subtitle");
  const dialog = h("dialog", {
    class: ("dialog " + className).trim(),
    role: role === "alertdialog" ? "alertdialog" : null,
    "aria-modal": "true",
    "aria-labelledby": titleId,
    "aria-describedby": subtitle ? subtitleId : null,
    dataset: { width: DIALOG_WIDTHS.includes(width) ? width : "xlarge", height, align },
  });
  let closed = false;
  let dirty = false;
  const close = (reason = "close") => {
    if (closed) {
      return;
    }
    closed = true;
    const index = dialogStack.findIndex((entry) => entry.element === dialog);
    if (index >= 0) {
      dialogStack.splice(index, 1);
    }
    // Menus and tooltips opened from the dialog go with it.
    for (let i = overlayStack.length - 1; i >= 0; i--) {
      if (dialog.contains(overlayStack[i].element) || (overlayStack[i].anchor && dialog.contains(overlayStack[i].anchor))) {
        overlayStack[i].close({ restore: false, reason: "dialog-closed" });
      }
    }
    if (typeof dialog.close === "function" && dialog.open) {
      dialog.close();
    }
    dialog.remove();
    unlockScroll();
    if (!restoreFocus(trigger)) {
      if (!restoreFocus(returnFocus)) {
        const below = topDialog();
        if (below) {
          const first = focusables(below)[0];
          if (first) {
            first.focus();
          }
        }
      }
    }
    if (typeof onClose === "function") {
      onClose(reason);
    }
  };
  const closeButton = iconButton({ icon: "x", label: "Close", variant: "invisible", size: "medium", tooltip: false, className: "dialog-close", onClick: () => close("close-button") });
  const header = h(
    "div",
    { class: "dialog-header" },
    h("div", { class: "dialog-title-block" }, h("h1", { class: "dialog-title" + (hideTitle ? " sr-only" : ""), id: titleId, text: title }), subtitle ? h("p", { class: "dialog-subtitle", id: subtitleId, text: subtitle }) : null),
    closeButton
  );
  const bannerSlot = h("div", { class: "dialog-banner", hidden: true });
  const body = h("div", { class: "dialog-body" });
  const api = { close, element: dialog, body, footer: null, buttons: [], showError: null };
  api.showError = (message) => {
    const el = banner({ variant: "critical", title: "Error", hideTitle: true, description: message, flush: true });
    bannerSlot.replaceChildren(el);
    bannerSlot.hidden = false;
    return el;
  };
  const node = typeof content === "function" ? content(api) : content;
  if (node) {
    body.appendChild(typeof node === "string" ? h("p", { text: node }) : node);
  }
  dialog.append(header, bannerSlot, body);
  const buttonsSpec = footer || actions || [];
  if (buttonsSpec.length) {
    const bar = h("div", { class: "dialog-footer" });
    for (const spec of buttonsSpec) {
      const variant = spec.variant === "ghost" ? "invisible" : spec.variant || "default";
      const el = button({ label: spec.label, variant, attrs: spec.autofocus ? { "data-autofocus": "true" } : {} });
      el.addEventListener("click", async (event) => {
        if (el.getAttribute("aria-disabled") === "true") {
          return;
        }
        let result;
        if (typeof spec.onClick === "function") {
          setBusy(el, true);
          try {
            result = await spec.onClick(close, event);
          } catch (err) {
            setBusy(el, false);
            api.showError(errorMessage(err));
            return;
          }
          setBusy(el, false);
        }
        if (result !== false) {
          close("action");
        }
      });
      bar.appendChild(el);
      api.buttons.push(el);
    }
    dialog.appendChild(bar);
    api.footer = bar;
  }
  if (position === "left") {
    dialog.setAttribute("data-position", "left");
    dialog.setAttribute("data-narrow", "left");
  } else {
    let narrowPosition = narrow;
    if (narrow === "auto") {
      narrowPosition = role === "alertdialog" ? "center" : holdsInputs(body) ? "fullscreen" : "bottom";
    }
    dialog.setAttribute("data-narrow", narrowPosition);
  }
  dialog.addEventListener("input", (event) => {
    if (event.target && event.target.closest && event.target.closest("form")) {
      dirty = true;
    }
  });
  dialog.addEventListener("cancel", (event) => {
    event.preventDefault();
    if (topDialog() === dialog) {
      close("escape");
    }
  });
  dialog.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && !event.defaultPrevented) {
      event.preventDefault();
      event.stopPropagation();
      if (topDialog() === dialog) {
        close("escape");
      }
      return;
    }
    trapFocus(event, dialog);
  });
  dialog.addEventListener("click", (event) => {
    if (event.target === dialog && !dirty) {
      close("backdrop");
    }
  });
  document.body.appendChild(dialog);
  lockScroll();
  if (typeof dialog.showModal === "function") {
    dialog.showModal();
  } else {
    dialog.setAttribute("open", "");
  }
  dialogStack.push({ element: dialog, close });
  let target = null;
  if (initialFocus) {
    target = typeof initialFocus === "string" ? dialog.querySelector(initialFocus) : initialFocus;
  }
  if (!target) {
    target = dialog.querySelector("[autofocus], [data-autofocus]");
  }
  if (!target) {
    target = focusables(dialog)[0] || closeButton;
  }
  target.focus({ preventScroll: true });
  const markScroll = () => dialog.classList.toggle("has-scroll", body.scrollHeight > body.clientHeight + 1);
  body.addEventListener("scroll", markScroll);
  later(markScroll, 0);
  return api;
}

/**
 * Opens a Primer ConfirmationDialog with role alertdialog, 320 px wide, and
 * resolves true when the person confirms. Cancel sits on the left and the
 * confirm button on the right. Focus starts on Cancel when the confirm
 * button is danger. Escape cancels. With onConfirm the dialog stays open
 * with a busy confirm button until the promise settles. A failure shows a
 * critical Banner under the header and keeps the dialog open.
 * @param {{title: string, body?: string | Node, confirmLabel?: string, cancelLabel?: string, danger?: boolean, onConfirm?: Function, returnFocus?: Element}} options
 * @returns {Promise<boolean>}
 */
export function confirmDialog({ title, body = "", confirmLabel = "OK", cancelLabel = "Cancel", danger = true, onConfirm, returnFocus = null } = {}) {
  return new Promise((resolve) => {
    let result = false;
    const handle = openDialog({
      title,
      role: "alertdialog",
      width: "medium",
      narrow: "center",
      returnFocus,
      className: "confirmation-dialog",
      content: body ? (typeof body === "string" ? h("p", { class: "confirmation-dialog-body", text: body }) : body) : null,
      footer: [
        { label: cancelLabel, autofocus: danger, onClick: () => undefined },
        {
          label: confirmLabel,
          variant: danger ? "danger" : "primary",
          autofocus: !danger,
          onClick: async () => {
            if (typeof onConfirm === "function") {
              await onConfirm();
            }
            result = true;
            return undefined;
          },
        },
      ],
      onClose: () => resolve(result),
    });
    if (body) {
      const text = handle.body.firstChild;
      if (text) {
        handle.element.setAttribute("aria-describedby", ensureId(text, "confirm-body"));
      }
    } else {
      handle.body.hidden = true;
    }
  });
}

/* ---------- Banner and InlineMessage ---------- */

const BANNER_ICONS = { critical: "stop", warning: "alert", success: "check-circle", info: "info", upsell: "info" };
const BANNER_LABELS = { critical: "Critical", warning: "Warning", success: "Success", info: "Information", upsell: "Recommendation" };
const BANNER_RANK = { info: 0, upsell: 0, success: 1, warning: 2, critical: 3 };

/**
 * Builds a Primer Banner: a section with an icon, a title, a description, up
 * to two actions and an optional Dismiss button. A critical Banner has no
 * Dismiss button. With into, the Banner goes to the top of that container
 * and replaces a Banner of the same or lower severity there, since a page
 * shows one Banner. A Banner of lower severity than the one shown stays out
 * of the page, and its text is announced so the message is not lost. With
 * focus the Banner receives focus, as an essential Banner that appears after
 * load does. Without focus its text is announced.
 * @param {{variant?: "critical" | "warning" | "success" | "info" | "upsell", title?: string, hideTitle?: boolean, description?: string | Node, actions?: {label: string, onClick?: Function, href?: string, primary?: boolean}[], dismissible?: boolean, onDismiss?: Function, layout?: "default" | "compact", flush?: boolean, headingLevel?: number, into?: Element | null, focus?: boolean, announce?: boolean}} options
 * @returns {HTMLElement}
 */
export function banner({ variant = "info", title = "", hideTitle = false, description = "", actions = [], dismissible = false, onDismiss, layout = "default", flush = false, headingLevel = 2, into = null, focus = false, announce: speak = !focus } = {}) {
  const kind = BANNER_ICONS[variant] ? variant : "info";
  const heading = h("h" + Math.min(6, Math.max(2, headingLevel)), { class: "banner-title" + (hideTitle ? " sr-only" : ""), text: title || BANNER_LABELS[kind] });
  const descEl = description ? h("div", { class: "banner-description" }, typeof description === "string" ? h("p", { text: description }) : description) : null;
  const actionEls = (actions || []).slice(0, 2).map((action) =>
    button({ label: action.label, href: action.href, variant: action.primary ? "primary" : "default", size: "small", onClick: action.onClick })
  );
  const el = h(
    "section",
    { class: `banner banner-${kind}`, "aria-label": BANNER_LABELS[kind], tabindex: focus ? "-1" : null, dataset: { variant: kind, layout, uiBanner: "" } },
    h("div", { class: "banner-icon", "aria-hidden": "true" }, icon(BANNER_ICONS[kind], 16)),
    h("div", { class: "banner-container" }, h("div", { class: "banner-content" }, heading, descEl), actionEls.length ? h("div", { class: "banner-actions" }, actionEls) : null)
  );
  if (hideTitle) {
    el.setAttribute("data-title-hidden", "true");
  }
  if (flush) {
    el.setAttribute("data-flush", "true");
  }
  if (dismissible && kind !== "critical") {
    const dismiss = iconButton({
      icon: "x",
      label: "Dismiss banner",
      size: "small",
      tooltip: false,
      className: "banner-dismiss",
      onClick: () => {
        const next = el.nextElementSibling || null;
        el.remove();
        if (next && typeof next.focus === "function") {
          if (!focusables(next)[0]) {
            next.setAttribute("tabindex", "-1");
          }
          (focusables(next)[0] || next).focus();
        }
        if (typeof onDismiss === "function") {
          onDismiss();
        }
      },
    });
    el.appendChild(h("div", { class: "banner-dismiss-cell" }, dismiss));
  }
  if (into) {
    const existing = Array.from(into.children).find((child) => child.hasAttribute("data-ui-banner"));
    if (existing && BANNER_RANK[existing.getAttribute("data-variant")] > BANNER_RANK[kind]) {
      announce([title || BANNER_LABELS[kind], typeof description === "string" ? description : description && description.textContent ? description.textContent.trim() : ""].filter(Boolean).join(". "));
      return el;
    }
    if (existing) {
      into.insertBefore(el, existing);
      existing.remove();
    } else {
      into.insertBefore(el, into.firstChild);
    }
    if (focus) {
      el.focus();
    } else if (speak) {
      announce([title, typeof description === "string" ? description : ""].filter(Boolean).join(". "));
    }
  }
  return el;
}

/** The container that page-level Banners go to. */
export function bannerHost() {
  return document.querySelector("[data-banner-host]") || document.querySelector("#main .page") || document.getElementById("main") || document.querySelector("main") || document.body;
}

const INLINE_ICONS = {
  critical: ["alert", "alert-fill"],
  warning: ["alert", "alert-fill"],
  success: ["check-circle", "check-circle-fill"],
  unavailable: ["alert", "alert-fill"],
};

/**
 * Builds a Primer InlineMessage: an icon and text in the colour of its
 * variant, placed beside the control it describes.
 * @param {{variant?: "critical" | "warning" | "success" | "unavailable", size?: "medium" | "small", text: string | Node}} options
 * @returns {HTMLElement}
 */
export function inlineMessage({ variant = "critical", size = "medium", text = "" } = {}) {
  const kind = INLINE_ICONS[variant] ? variant : "critical";
  const glyph = INLINE_ICONS[kind][size === "small" ? 1 : 0];
  return h(
    "div",
    { class: `inline-message inline-message-${kind}`, dataset: { size } },
    h("span", { class: "inline-message-icon", "aria-hidden": "true" }, icon(glyph, 16)),
    h("span", { class: "inline-message-text" }, text)
  );
}

/* ---------- Undo ---------- */

/** The list a row belongs to. */
function rowList(row) {
  const parent = row.parentElement;
  return (parent && parent.closest("ul, ol, tbody, table, [data-row-list]")) || parent || row;
}

/**
 * Replaces a row in place with an undo row: a muted message naming the
 * result and a Button invisible small "Undo". There is no timer. The row
 * stays until the person acts elsewhere in the same list or leaves the page,
 * and is then removed unless keep is set. Undo runs onUndo and restores the
 * row. The message is announced.
 * @param {Element} row
 * @param {{message: string, undoLabel?: string, onUndo?: Function, keep?: boolean}} options
 * @returns {{element: HTMLElement, button: HTMLElement, finalize: Function, done: Promise<"undone" | "kept">}}
 */
export function undoRow(row, { message, undoLabel = "Undo", onUndo, keep = false } = {}) {
  const list = rowList(row);
  const saved = Array.from(row.childNodes);
  const hadFocus = row.contains(document.activeElement);
  const text = h("span", { class: "undo-row-message undo-text", text: message });
  const undoButton = button({ label: undoLabel, variant: "invisible", size: "small", className: "undo-btn" });
  const inner = h("div", { class: "undo-row undo-bar" }, text, undoButton);
  let content = inner;
  if (row.tagName === "TR") {
    content = h("td", { class: "undo-row-cell", colSpan: Math.max(1, row.cells.length) }, inner);
  }
  row.classList.add("is-undo", "is-undoing");
  row.replaceChildren(content);
  announce(message);
  if (hadFocus) {
    undoButton.focus({ preventScroll: true });
  }
  let settled = false;
  let resolveDone;
  const done = new Promise((resolve) => {
    resolveDone = resolve;
  });
  const stop = () => {
    list.removeEventListener("pointerdown", onListAction, true);
    list.removeEventListener("keydown", onListKey, true);
    if (typeof window.removeEventListener === "function") {
      window.removeEventListener("pagehide", finalize);
    }
  };
  const finalize = () => {
    if (settled) {
      return;
    }
    settled = true;
    stop();
    if (!keep) {
      row.remove();
    }
    resolveDone("kept");
  };
  const onListAction = (event) => {
    if (!row.contains(event.target)) {
      finalize();
    }
  };
  const onListKey = (event) => {
    if ((event.key === "Enter" || event.key === " ") && !row.contains(event.target)) {
      finalize();
    }
  };
  list.addEventListener("pointerdown", onListAction, true);
  list.addEventListener("keydown", onListKey, true);
  if (typeof window.addEventListener === "function") {
    window.addEventListener("pagehide", finalize);
  }
  undoButton.addEventListener("click", async () => {
    if (settled) {
      return;
    }
    setBusy(undoButton, true);
    try {
      if (typeof onUndo === "function") {
        await onUndo();
      }
    } catch (err) {
      setBusy(undoButton, false);
      text.replaceChildren(inlineMessage({ variant: "critical", size: "small", text: errorMessage(err, "The change could not be undone.") }));
      return;
    }
    settled = true;
    stop();
    row.replaceChildren(...saved);
    row.classList.remove("is-undo", "is-undoing");
    const first = focusables(row)[0];
    if (first) {
      first.focus({ preventScroll: true });
    }
    announce("Undone");
    resolveDone("undone");
  });
  return { element: inner, button: undoButton, finalize, done };
}

/**
 * Runs a reversible action at once and offers Undo, as Primer prescribes for
 * non-destructive actions. With where, the Undo replaces that element as an
 * undo row, and the promise settles when the person undoes or acts elsewhere
 * in the list. When where leaves the page first, the Undo moves to a
 * dismissible info Banner. Without where, the Banner goes to the top of into
 * or of the page and the promise settles as soon as it shows. A failed run
 * shows a critical Banner and offers no Undo. There is no timer.
 * @param {{label: string, run: Function, undo: Function, where?: Element | null, undoLabel?: string, failLabel?: string, keepWhere?: boolean, into?: Element | null}} options
 * @returns {Promise<"undone" | "kept" | "failed">}
 */
export async function undoable({ label, run, undo, where = null, undoLabel = "Undo", failLabel = "", keepWhere = false, into = null } = {}) {
  try {
    await run();
  } catch (err) {
    banner({ variant: "critical", title: errorMessage(err, failLabel), hideTitle: false, into: into || bannerHost() });
    return "failed";
  }
  let undone = false;
  const doUndo = async () => {
    await undo();
    undone = true;
  };
  const inBanner = () => {
    const host = into || bannerHost();
    const el = banner({
      variant: "info",
      title: label,
      dismissible: true,
      into: host,
      actions: [
        {
          label: undoLabel,
          onClick: async (event) => {
            const btn = event.currentTarget;
            setBusy(btn, true);
            try {
              await doUndo();
              el.remove();
              announce("Undone");
            } catch (err) {
              setBusy(btn, false);
              const content = el.querySelector(".banner-content");
              const old = el.querySelector(".banner-description");
              if (old) {
                old.remove();
              }
              content.appendChild(h("div", { class: "banner-description" }, h("p", { text: errorMessage(err, "The change could not be undone.") })));
            }
          },
        },
      ],
    });
    return el;
  };
  if (!where || !where.isConnected) {
    inBanner();
    return "kept";
  }
  const handle = undoRow(where, { message: label, undoLabel, keep: keepWhere, onUndo: doUndo });
  const watch = setInterval(() => {
    if (!where.isConnected) {
      clearInterval(watch);
      if (!undone) {
        handle.finalize();
        inBanner();
      }
    }
  }, 250);
  const result = await handle.done;
  clearInterval(watch);
  return result;
}

/* ---------- Labels, counters, tokens and avatars ---------- */

/**
 * Builds a Primer CounterLabel. The visible number is hidden from screen
 * readers and a visually hidden copy in parentheses follows it. A null or
 * empty count renders an empty, hidden counter.
 * @param {number | string | null} count
 * @param {{variant?: "secondary" | "primary"}} [options]
 * @returns {HTMLElement}
 */
export function counterLabel(count, { variant = "secondary" } = {}) {
  const el = h("span", { class: `counter-label counter-${variant}` });
  setCount(el, count);
  return el;
}

/** Updates a CounterLabel built by counterLabel. */
export function setCount(el, count) {
  if (count === null || count === undefined || count === "") {
    el.replaceChildren();
    el.hidden = true;
    return;
  }
  el.hidden = false;
  el.replaceChildren(h("span", { "aria-hidden": "true", text: String(count) }), h("span", { class: "sr-only", text: `(${count})` }));
}

/**
 * Builds a Primer Label.
 * @param {string} text
 * @param {{variant?: "default" | "secondary" | "accent" | "success" | "attention" | "danger" | "done", size?: "small" | "large"}} [options]
 * @returns {HTMLElement}
 */
export function label(text, { variant = "default", size = "small" } = {}) {
  return h("span", { class: `ui-label ui-label-${variant}` + (size === "large" ? " ui-label-lg" : ""), text });
}

/**
 * Builds a Primer Token. With href it is a link. With onRemove it has a
 * remove button named "Remove {text}", and Backspace or Delete on the focused
 * token removes it. Only large and xlarge tokens take a remove button.
 * @param {string} text
 * @param {{size?: "small" | "medium" | "large" | "xlarge", href?: string, onRemove?: Function, leadingVisual?: string | Node, selected?: boolean}} [options]
 * @returns {HTMLElement}
 */
export function token(text, { size = "medium", href, onRemove, leadingVisual, selected = false } = {}) {
  const removable = typeof onRemove === "function" && (size === "large" || size === "xlarge");
  const inner = [
    leadingVisual ? h("span", { class: "token-visual", "aria-hidden": "true" }, visual(leadingVisual)) : null,
    h("span", { class: "token-text", text }),
  ];
  const el = href ? h("a", { class: `token token-${size}`, href }, inner) : h("span", { class: `token token-${size}` }, inner);
  if (selected) {
    el.setAttribute("data-selected", "true");
  }
  if (removable) {
    el.classList.add("is-removable");
    el.setAttribute("tabindex", "0");
    const remove = h("button", { type: "button", class: "token-remove", "aria-label": `Remove ${text}`, tabindex: "-1" }, icon("x", 16));
    remove.addEventListener("click", (event) => {
      event.preventDefault();
      onRemove(event);
    });
    el.addEventListener("keydown", (event) => {
      if (event.key === "Backspace" || event.key === "Delete") {
        event.preventDefault();
        onRemove(event);
      }
    });
    el.appendChild(remove);
  }
  return el;
}

/** Returns up to two initials for a name or an email address. */
export function initialsOf(name) {
  const text = String(name || "").trim();
  if (!text) {
    return "?";
  }
  const words = text.replace(/@.*$/, "").split(/[\s._-]+/).filter(Boolean);
  const letters = words.length > 1 ? words[0][0] + words[words.length - 1][0] : (words[0] || text).slice(0, 2);
  return letters.toUpperCase();
}

const AVATAR_SIZES = [16, 20, 24, 28, 32, 40, 48, 64];

/**
 * Builds a Primer Avatar. Markspace has no profile images, so without src it
 * shows initials on the stored user colour in --fgColor-onEmphasis. With alt
 * empty the avatar is decorative, as when the name is shown beside it.
 * @param {{name?: string, color?: string, src?: string, size?: number, square?: boolean, alt?: string}} options
 * @returns {HTMLElement}
 */
export function avatar({ name = "", color = "", src = "", size = 20, square = false, alt } = {}) {
  const px = AVATAR_SIZES.includes(Number(size)) ? Number(size) : 20;
  const label = alt === undefined ? name : alt;
  const el = h("span", { class: "avatar" + (square ? " avatar-square" : ""), style: { "--avatar-size": px + "px" }, dataset: { size: String(px) } });
  if (src) {
    el.appendChild(h("img", { src, alt: label || "", width: px, height: px }));
  } else if (!String(name || "").trim()) {
    // Without a name the avatar is anonymous: a person Octicon on a muted fill.
    el.classList.add("avatar-anon");
    el.appendChild(icon("person", 16));
    el.setAttribute("aria-hidden", "true");
  } else {
    if (color && /^#[0-9a-fA-F]{3,8}$/.test(color)) {
      el.style.setProperty("--avatar-bg", color);
    }
    el.appendChild(h("span", { "aria-hidden": "true", text: initialsOf(name) }));
    if (label) {
      el.setAttribute("role", "img");
      el.setAttribute("aria-label", label);
    }
  }
  return el;
}

/* ---------- Loading and empty states ---------- */

const SKELETON_WIDTHS = ["85%", "67.5%", "80%", "60%", "75%"];

/**
 * Builds Primer skeleton loaders. variant "box" is a SkeletonBox of width
 * and height. variant "text" is SkeletonText with lines lines, the last one
 * shorter. variant "rows" stands for list rows, each a 32 px square and a
 * text line. The wrapper has aria-busy and a hidden label.
 * @param {{variant?: "box" | "text" | "rows", width?: string, height?: string, lines?: number, rows?: number, label?: string}} [options]
 * @returns {HTMLElement}
 */
export function skeleton({ variant = "text", width = "", height = "", lines = 1, rows = 3, label = "Loading" } = {}) {
  if (variant === "box") {
    return h("span", { class: "skeleton-box", style: Object.assign({}, width ? { width } : {}, height ? { height } : {}), "aria-hidden": "true" });
  }
  const wrap = h("div", { class: "skeleton", "aria-busy": "true" }, h("span", { class: "sr-only", text: label }));
  if (variant === "rows") {
    for (let i = 0; i < rows; i++) {
      wrap.appendChild(h("div", { class: "skeleton-row", "aria-hidden": "true" }, h("span", { class: "skeleton-box skeleton-square" }), h("span", { class: "skeleton-text", style: { width: SKELETON_WIDTHS[i % SKELETON_WIDTHS.length] } })));
    }
    return wrap;
  }
  for (let i = 0; i < lines; i++) {
    const last = lines > 1 && i === lines - 1;
    wrap.appendChild(h("span", { class: "skeleton-text", "aria-hidden": "true", style: { width: last ? "max(50px, 65%)" : width || "100%" } }));
  }
  return wrap;
}

/**
 * Builds a Primer ProgressBar with role progressbar. The value is a
 * percentage unless max is given. valueText names the value when the visible
 * text is not a percentage, such as "3.2 MB of 8 MB".
 * @param {{value?: number, max?: number, size?: "small" | "default" | "large", color?: string, label?: string, valueText?: string}} [options]
 * @returns {HTMLElement & {set: (value: number, valueText?: string) => void}}
 */
export function progressBar({ value = 0, max = 100, size = "default", color = "accent", label: name = "", valueText = "" } = {}) {
  const fill = h("span", { class: "progress-bar-item" });
  const el = h("span", { class: `progress-bar progress-bar-${size}`, role: "progressbar", "aria-valuemin": "0", "aria-valuemax": "100", "aria-label": name || null, dataset: { color } }, fill);
  el.set = (next, text = "") => {
    const percent = max > 0 ? Math.max(0, Math.min(100, (Number(next) / max) * 100)) : 0;
    el.setAttribute("aria-valuenow", String(Math.round(percent)));
    if (text) {
      el.setAttribute("aria-valuetext", text);
    } else {
      el.removeAttribute("aria-valuetext");
    }
    fill.style.width = percent + "%";
  };
  el.set(value, valueText);
  return el;
}

/**
 * Builds a Primer Blankslate: a graphic, a heading, a description, one
 * primary action and a secondary link. border adds the 1 px border used when
 * the Blankslate is not the only content. headingLevel 1 makes the heading
 * the page h1, as on an error page or an empty route without a PageHeader.
 * @param {{icon?: string, title: string, description?: string, primaryAction?: {label: string, onClick?: Function, href?: string}, secondaryAction?: {label: string, href: string}, size?: "small" | "medium" | "spacious", border?: boolean, narrow?: boolean, headingLevel?: 1 | 2 | 3 | 4 | 5 | 6}} options
 * @returns {HTMLElement}
 */
export function blankslate({ icon: glyph = "", title, description = "", primaryAction = null, secondaryAction = null, size = "medium", border = false, narrow = false, headingLevel = 2 } = {}) {
  const el = h(
    "div",
    { class: `blankslate blankslate-${size}` + (border ? " blankslate-border" : "") + (narrow ? " blankslate-narrow" : "") },
    h(
      "div",
      { class: "blankslate-inner" },
      glyph ? h("span", { class: "blankslate-visual", "aria-hidden": "true" }, icon(glyph, 24)) : null,
      h("h" + Math.min(6, Math.max(1, Math.round(Number(headingLevel)) || 2)), { class: "blankslate-heading", text: title }),
      description ? h("p", { class: "blankslate-description", text: description }) : null,
      primaryAction ? h("div", { class: "blankslate-action" }, button({ label: primaryAction.label, href: primaryAction.href, variant: "primary", onClick: primaryAction.onClick })) : null,
      secondaryAction ? h("div", { class: "blankslate-action" }, h("a", { href: secondaryAction.href, text: secondaryAction.label })) : null
    )
  );
  return el;
}

/* ---------- Form controls ---------- */

/**
 * Builds a Primer FormControl: a label, the control, a caption and a
 * validation message. type may be any input type, "textarea" or "select"
 * with options [{value, label}]. Checkboxes and radios use the horizontal
 * layout. leadingVisual, trailingVisual and trailingAction wrap the input in
 * a TextInput box. Other attributes pass through to the control. The wrapper
 * keeps the class "field" for older callers.
 * @param {{label: string, name?: string, type?: string, value?: any, caption?: string, hint?: string, error?: string, success?: string, required?: boolean, options?: {value: string, label: string}[], leadingVisual?: string, trailingVisual?: string, trailingAction?: {icon: string, label: string, onClick: Function}, size?: "small" | "medium" | "large", monospace?: boolean, hideLabel?: boolean, rows?: number}} options
 * @returns {HTMLElement}
 */
export function formControl({ label: text, name, type = "text", value, caption, hint, error, success, required, options, leadingVisual, trailingVisual, trailingAction, size = "medium", monospace = false, hideLabel = false, rows, ...attrs } = {}) {
  const id = attrs.id || uid("fc-" + String(name || "field").replace(/[^\w-]/g, "_"));
  delete attrs.id;
  const captionText = caption || hint;
  const captionId = id + "-caption";
  const validationId = id + "-validation";
  const horizontal = type === "checkbox" || type === "radio";
  let control;
  if (type === "textarea") {
    control = h("textarea", Object.assign({ id, name, rows: rows || 7, class: "textarea" }, attrs));
    control.value = value === undefined || value === null ? "" : String(value);
  } else if (type === "select") {
    control = h("select", Object.assign({ id, name, class: "select" }, attrs));
    for (const opt of options || []) {
      const optionEl = h("option", { value: opt.value, text: opt.label });
      if (String(opt.value) === String(value)) {
        optionEl.selected = true;
      }
      control.appendChild(optionEl);
    }
  } else if (horizontal) {
    control = h("input", Object.assign({ id, name, type, class: type }, attrs));
    control.checked = !!value;
  } else {
    control = h("input", Object.assign({ id, name, type, class: "text-input-control" }, attrs));
    if (value !== undefined && value !== null) {
      control.value = String(value);
    }
  }
  if (monospace) {
    control.classList.add("is-monospace");
  }
  if (required) {
    control.required = true;
  }
  const described = [];
  if (captionText) {
    described.push(captionId);
  }
  if (error || success) {
    described.push(validationId);
  }
  if (described.length) {
    control.setAttribute("aria-describedby", described.join(" "));
  }
  const labelEl = h(
    "label",
    { class: "form-label field-label" + (hideLabel ? " sr-only" : ""), for: id },
    text,
    required ? h("span", { class: "form-required field-required", "aria-hidden": "true", text: "*" }) : null
  );
  const captionEl = captionText ? h("p", { class: "form-caption field-hint", id: captionId, text: captionText }) : null;
  const validationEl = validationMessage(validationId, error || success || "", error ? "error" : "success");
  validationEl.hidden = !(error || success);
  let field = control;
  if (!horizontal && type !== "textarea" && (leadingVisual || trailingVisual || trailingAction || type === "select" || size !== "medium")) {
    const box = h("span", { class: "text-input" + (type === "select" ? " select-box" : "") + (size === "small" ? " text-input-sm" : size === "large" ? " text-input-lg" : "") + " text-input-block" });
    if (leadingVisual) {
      box.classList.add("has-leading-visual");
      box.appendChild(h("span", { class: "text-input-visual", "aria-hidden": "true" }, visual(leadingVisual)));
    }
    box.appendChild(control);
    if (type === "select") {
      box.appendChild(h("span", { class: "select-caret", "aria-hidden": "true" }, icon("triangle-down", 16)));
    } else if (trailingAction) {
      box.classList.add("has-trailing-action");
      box.appendChild(iconButton({ icon: trailingAction.icon, label: trailingAction.label, size: "small", className: "text-input-action", onClick: trailingAction.onClick }));
    } else if (trailingVisual) {
      box.classList.add("has-trailing-visual");
      box.appendChild(h("span", { class: "text-input-visual", "aria-hidden": "true" }, visual(trailingVisual)));
    }
    field = box;
  }
  let wrapper;
  if (horizontal) {
    wrapper = h(
      "div",
      { class: "form-control form-control-horizontal field field-check", dataset: { field: name || "" } },
      control,
      h("div", { class: "form-control-horizontal-text" }, labelEl, captionEl, validationEl)
    );
  } else {
    wrapper = h("div", { class: "form-control field", dataset: { field: name || "" } }, labelEl, field, captionEl, validationEl);
  }
  if (error) {
    control.setAttribute("aria-invalid", "true");
    wrapper.classList.add("has-error");
  } else if (success) {
    control.setAttribute("aria-invalid", "false");
    wrapper.classList.add("has-success");
  }
  return wrapper;
}

/** Builds the validation message element of a FormControl. */
function validationMessage(id, text, kind) {
  const el = h("p", { class: "form-validation field-error" + (kind === "success" ? " is-success" : ""), id });
  if (text) {
    el.append(h("span", { class: "form-validation-icon", "aria-hidden": "true" }, icon(kind === "success" ? "check-circle-fill" : "alert-fill", 16)), h("span", { class: "form-validation-text", text }));
  }
  return el;
}

/**
 * Sets or clears the validation message of a FormControl, or of server
 * markup with the older field classes.
 * @param {Element} wrapper
 * @param {string} message empty to clear
 */
export function setFieldError(wrapper, message) {
  if (!wrapper) {
    return;
  }
  const control = wrapper.querySelector("input, select, textarea");
  let el = wrapper.querySelector(".form-validation") || wrapper.querySelector(".field-error");
  const id = el && el.id ? el.id : (control && control.id ? control.id : uid("field")) + "-validation";
  if (!el) {
    el = validationMessage(id, "", "error");
    const host = wrapper.querySelector(".form-control-horizontal-text") || wrapper;
    host.appendChild(el);
  }
  el.id = id;
  el.classList.remove("is-success");
  el.replaceChildren(...(message ? [h("span", { class: "form-validation-icon", "aria-hidden": "true" }, icon("alert-fill", 16)), h("span", { class: "form-validation-text", text: message })] : []));
  el.hidden = !message;
  wrapper.classList.toggle("has-error", !!message);
  if (control) {
    if (message) {
      control.setAttribute("aria-invalid", "true");
      addRef(control, "aria-describedby", id);
    } else {
      control.removeAttribute("aria-invalid");
      removeRef(control, "aria-describedby", id);
    }
  }
}

/**
 * Clears every validation message and the error summary of a form.
 * @param {Element} form
 */
export function clearFieldErrors(form) {
  for (const wrapper of form.querySelectorAll(".field")) {
    if (wrapper.classList.contains("has-error")) {
      setFieldError(wrapper, "");
    }
  }
  for (const old of form.querySelectorAll("[data-form-banner]")) {
    old.remove();
  }
  for (const legacy of form.querySelectorAll(".form-error")) {
    legacy.hidden = true;
  }
}

/** Normalizes the errors showFieldErrors accepts into field and form messages. */
function normalizeErrors(errors) {
  const fields = [];
  const general = [];
  if (!errors) {
    return { fields, general };
  }
  if (Array.isArray(errors)) {
    for (const e of errors) {
      if (e && e.field) {
        fields.push({ field: e.field, message: e.message });
      } else if (e) {
        general.push(e.message || String(e));
      }
    }
  } else if (errors.fields && typeof errors.fields === "object") {
    for (const [field, message] of Object.entries(errors.fields)) {
      fields.push({ field, message });
    }
    if (errors.message && !fields.length) {
      general.push(errors.message);
    }
  } else if (errors.field) {
    fields.push({ field: errors.field, message: errors.message || "Check this field." });
  } else if (errors.message) {
    general.push(errors.message);
  } else if (typeof errors === "object") {
    for (const [field, message] of Object.entries(errors)) {
      fields.push({ field, message });
    }
  }
  return { fields, general };
}

/**
 * Shows validation errors on a form as Primer's forms pattern prescribes.
 * errors may be an API error {message, field}, an array of {field, message},
 * or a map from field name to message. With one or two field errors, focus
 * moves to the first invalid field. With three or more, a critical error
 * summary Banner at the top of the form lists each error as a link to its
 * field and receives focus. A message without a matching field shows in a
 * critical Banner at the top of the form.
 * @param {HTMLFormElement | Element} form
 * @param {object | object[] | null} errors
 * @returns {boolean} true when at least one error attached to a field.
 */
export function showFieldErrors(form, errors) {
  clearFieldErrors(form);
  const { fields, general } = normalizeErrors(errors);
  const attached = [];
  for (const { field, message } of fields) {
    const control = form.querySelector(`[name="${CSS.escape(field)}"]`);
    const wrapper = control ? control.closest(".field") || control.closest(".form-control") : null;
    if (wrapper) {
      setFieldError(wrapper, sentence(message));
      attached.push({ control, message: sentence(message) });
    } else {
      general.push(message);
    }
  }
  const insertBanner = (el) => {
    el.setAttribute("data-form-banner", "");
    form.insertBefore(el, form.firstChild);
  };
  if (attached.length >= 3) {
    const listEl = h("ul", { class: "banner-error-list" });
    for (const { control, message } of attached) {
      const link = h("a", { href: "#" + ensureId(control, "field"), text: message });
      link.addEventListener("click", (event) => {
        event.preventDefault();
        control.focus();
      });
      listEl.appendChild(h("li", {}, link));
    }
    const el = banner({ variant: "critical", title: `${attached.length} fields need changes`, description: listEl, focus: true });
    el.classList.add("form-error-summary");
    insertBanner(el);
    el.focus();
  } else if (general.length) {
    const el = banner({ variant: "critical", title: sentence(general[0]), description: general.length > 1 ? general.slice(1).map(sentence).join(" ") : "", focus: !attached.length });
    insertBanner(el);
    if (!attached.length) {
      el.focus();
    }
  }
  if (attached.length && attached.length < 3) {
    attached[0].control.focus();
    if (typeof attached[0].control.scrollIntoView === "function") {
      attached[0].control.scrollIntoView({ block: "center" });
    }
  }
  return attached.length > 0;
}

/* ---------- Edit in place ---------- */

/**
 * Turns an element into an inline edit, as Primer's saving pattern
 * prescribes: a small TextInput with Button small primary "Save" and Button
 * small invisible "Cancel" beside it. Enter saves and Escape cancels. The
 * element is hidden while the edit shows after it. An empty or unchanged
 * value cancels. validate returns an error message or null. When validate
 * returns a message or onSave throws, the edit stays open with the message
 * under the field.
 * @param {Element} element
 * @param {{value?: string, onSave?: (value: string) => any, onCancel?: Function, validate?: (value: string) => string | null | Promise<string | null>, label?: string, allowSame?: boolean}} options
 * @returns {{input: HTMLInputElement, save: Function, cancel: Function, done: Promise<string | null>, element: HTMLElement}}
 */
export function editInPlace(element, { value, onSave, onCancel, validate, label: name = "Name", allowSame = false } = {}) {
  const initial = value !== undefined && value !== null ? String(value) : element.textContent.trim();
  const inputId = uid("edit");
  const errorId = inputId + "-error";
  const input = h("input", { class: "text-input-control edit-in-place", id: inputId, type: "text", value: initial, autocomplete: "off", enterkeyhint: "done", "aria-label": name });
  const saveButton = button({ label: "Save", variant: "primary", size: "small", type: "submit" });
  const cancelButton = button({ label: "Cancel", variant: "invisible", size: "small" });
  const error = h("p", { class: "form-validation edit-in-place-error", id: errorId, hidden: true });
  const form = h("form", { class: "edit-in-place-form", novalidate: true }, h("span", { class: "text-input text-input-sm" }, input), saveButton, cancelButton);
  const wrap = h("span", { class: "edit-in-place-wrap" }, form, error);
  const returnFocus = document.activeElement;
  let finished = false;
  let busy = false;
  let resolveDone;
  const done = new Promise((resolve) => {
    resolveDone = resolve;
  });
  const showError = (message) => {
    error.replaceChildren(...(message ? [h("span", { class: "form-validation-icon", "aria-hidden": "true" }, icon("alert-fill", 16)), h("span", { text: message })] : []));
    error.hidden = !message;
    if (message) {
      input.setAttribute("aria-invalid", "true");
      input.setAttribute("aria-describedby", errorId);
    } else {
      input.removeAttribute("aria-invalid");
      input.removeAttribute("aria-describedby");
    }
  };
  const finish = (result) => {
    if (finished) {
      return;
    }
    finished = true;
    wrap.remove();
    element.hidden = false;
    resolveDone(result);
  };
  const cancel = () => {
    if (finished || busy) {
      return;
    }
    finish(null);
    if (typeof onCancel === "function") {
      onCancel();
    }
    restoreFocus(returnFocus);
  };
  const save = async () => {
    if (finished || busy) {
      return false;
    }
    const next = input.value.trim();
    if (next === initial.trim() && !allowSame) {
      cancel();
      return false;
    }
    let message = next ? null : `Enter a ${name.toLowerCase()}.`;
    if (!message && typeof validate === "function") {
      message = await validate(next);
    }
    if (message) {
      showError(sentence(message));
      input.focus();
      return false;
    }
    busy = true;
    input.readOnly = true;
    setBusy(saveButton, true, { message: "Saving" });
    try {
      if (typeof onSave === "function") {
        await onSave(next);
      }
    } catch (err) {
      busy = false;
      input.readOnly = false;
      setBusy(saveButton, false);
      showError(errorMessage(err));
      input.focus();
      return false;
    }
    busy = false;
    finish(next);
    if (!restoreFocus(element.matches && element.matches("a, button, [tabindex]") ? element : null)) {
      restoreFocus(returnFocus);
    }
    return true;
  };
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    save();
  });
  cancelButton.addEventListener("click", (event) => {
    event.preventDefault();
    cancel();
  });
  wrap.addEventListener("keydown", (event) => {
    event.stopPropagation();
    if (event.key === "Enter" && event.target === input) {
      event.preventDefault();
      save();
    } else if (event.key === "Escape") {
      event.preventDefault();
      cancel();
    }
  });
  input.addEventListener("input", () => showError(""));
  wrap.addEventListener("click", (event) => {
    event.stopPropagation();
  });
  element.hidden = true;
  element.after(wrap);
  input.focus({ preventScroll: true });
  if (typeof input.select === "function") {
    input.select();
  }
  return { input, save, cancel, done, element: wrap };
}

/* ---------- Expanded rows ---------- */

const expandedRows = new WeakMap();

/**
 * Expands a row in place with content under it. A list keeps one expanded
 * row, so expanding another collapses the first. content null collapses the
 * row. A table row gets a full-width row after it, a list row holds the
 * content inside, and any other element gets the content after it. Escape
 * inside the content collapses it. Expand and collapse follow
 * --motion-transition-stateChange.
 * @param {Element} row
 * @param {Node | null} content
 * @param {{trigger?: HTMLElement | null, onClose?: Function}} [options] trigger gets aria-expanded and focus back on Escape.
 * @returns {{close: Function, element: HTMLElement} | null}
 */
export function expandRow(row, content, { trigger = null, onClose } = {}) {
  const list = rowList(row);
  const current = expandedRows.get(list);
  if (!content) {
    if (current && current.row === row) {
      current.close();
    }
    return null;
  }
  if (current) {
    current.close({ restore: false, immediate: true });
  }
  const panel = h("div", { class: "row-expand" }, content);
  let holder = panel;
  if (row.tagName === "TR") {
    holder = h("tr", { class: "row-expand-row" }, h("td", { colSpan: Math.max(1, row.cells.length) }, panel));
    row.after(holder);
  } else if (row.matches("li, .list-row, .box-row")) {
    row.appendChild(panel);
  } else {
    row.after(panel);
  }
  row.classList.add("is-expanded");
  if (trigger) {
    trigger.setAttribute("aria-expanded", "true");
  }
  let closed = false;
  const close = ({ restore = true, immediate = false } = {}) => {
    if (closed) {
      return;
    }
    closed = true;
    if (expandedRows.get(list) && expandedRows.get(list).close === close) {
      expandedRows.delete(list);
    }
    row.classList.remove("is-expanded");
    if (trigger) {
      trigger.setAttribute("aria-expanded", "false");
    }
    if (immediate || prefersReducedMotion()) {
      holder.remove();
    } else {
      panel.classList.add("is-closing");
      later(() => holder.remove(), 200);
    }
    if (restore && trigger && trigger.isConnected) {
      trigger.focus({ preventScroll: true });
    }
    if (typeof onClose === "function") {
      onClose();
    }
  };
  panel.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && !event.defaultPrevented) {
      event.preventDefault();
      event.stopPropagation();
      close();
    }
  });
  expandedRows.set(list, { row, close });
  return { close, element: panel };
}

/* ---------- TreeView ---------- */

/**
 * Builds a Primer TreeView: a ul with role tree, items with role treeitem,
 * aria-level, aria-expanded and aria-current, and children in groups. The
 * tree has one tab stop. Down and Up move between visible rows. Right
 * expands a closed parent or moves to its first child. Left collapses an open
 * parent or moves to the parent. Home and End go to the first and last
 * visible rows. Enter opens the item. Printable characters move by
 * type-ahead after 300 ms without a key, wrapping and skipping collapsed
 * rows. A click on the chevron only toggles. Other keys, such as F2 and
 * Delete, go to onKey while the tree has focus.
 *
 * Items are {id, label, icon, children, hasChildren, expanded, current,
 * selected}. With loadChildren, a parent with hasChildren and no children
 * loads them when it expands, and shows a loading row meanwhile.
 *
 * selection "single" or "multiple" gives every row aria-selected, and
 * "multiple" sets aria-multiselectable on the tree. setSelection replaces the
 * selected ids and getSelection returns them. Selected rows show the selected
 * fill without the current bar. setIcon swaps the leading visual of one row.
 * update rebuilds the rows and keeps the selection, the tab stop and, when
 * the tree held focus, keyboard focus on the row with the same id.
 * @param {{label?: string, labelledBy?: string, items: object[], selection?: "none" | "single" | "multiple", onOpen?: (item: object, event: Event) => void, onToggle?: (item: object, expanded: boolean) => void, loadChildren?: (item: object) => Promise<object[]>, onKey?: (item: object, event: KeyboardEvent) => void, onContextMenu?: (item: object, event: Event) => void, emptyText?: string}} options
 * @returns {{element: HTMLElement, focusItem: (id: string) => void, setCurrent: (id: string) => void, expand: (id: string) => Promise<void>, collapse: (id: string) => void, update: (items: object[]) => void, setSelection: (ids: string | string[]) => void, getSelection: () => string[], setIcon: (id: string, icon: string | Node | null) => void}}
 */
export function treeView({ label: name = "", labelledBy = "", items = [], selection: selectionMode = "none", onOpen, onToggle, loadChildren, onKey, onContextMenu, emptyText = "No pages" } = {}) {
  const tree = h("ul", { class: "tree-view", role: "tree", "aria-label": labelledBy ? null : name || null, "aria-labelledby": labelledBy || null });
  if (selectionMode === "multiple") {
    tree.setAttribute("aria-multiselectable", "true");
  }
  const byId = new Map();
  const nodes = new Map();
  const prefix = uid("tv");
  const selected = new Set();
  let lastFocused = null;
  let lastFocusedId = null;
  const selectable = selectionMode === "single" || selectionMode === "multiple";
  const markSelected = (li) => {
    if (selectable && li && !li.classList.contains("tree-view-empty") && !li.classList.contains("tree-view-loading")) {
      li.setAttribute("aria-selected", selected.has(li.dataset.id) ? "true" : "false");
    }
  };

  const itemEl = (id) => nodes.get(String(id)) || null;
  const expandable = (item) => (item.children && item.children.length > 0) || !!item.hasChildren;
  // A row reserves space for the toggle only when a sibling can expand, so
  // rows of a flat list align with the NavList above the tree.
  const buildGroup = (items, level) => {
    const reserve = items.some(expandable);
    return items.map((child) => buildItem(child, level, reserve));
  };
  const buildItem = (item, level, reserve = true) => {
    byId.set(String(item.id), item);
    const hasKids = expandable(item);
    const li = h("li", { class: "tree-view-item", role: "treeitem", id: `${prefix}-${String(item.id).replace(/[^\w-]/g, "_")}`, "aria-level": String(level), tabindex: "-1", dataset: { id: String(item.id) }, style: { "--tree-level": String(level) } });
    const labelId = li.id + "-label";
    li.setAttribute("aria-labelledby", labelId);
    const toggle = h("span", { class: "tree-view-toggle", "aria-hidden": "true", dataset: { reserve: reserve || hasKids ? "true" : "false" } }, hasKids ? [h("span", { class: "tree-view-chevron-closed" }, icon("chevron-right", 16)), h("span", { class: "tree-view-chevron-open" }, icon("chevron-down", 16))] : null);
    const lines = h("span", { class: "tree-view-indent", "aria-hidden": "true" });
    for (let i = 1; i < level; i++) {
      lines.appendChild(h("span", { class: "tree-view-line" }));
    }
    const content = h(
      "div",
      { class: "tree-view-item-content" },
      lines,
      toggle,
      h("span", { class: "tree-view-visual", "aria-hidden": "true" }, item.icon ? visual(item.icon) : null),
      h("span", { class: "tree-view-label", id: labelId, text: item.label })
    );
    li.appendChild(content);
    if (item.current) {
      li.setAttribute("aria-current", "true");
    }
    if (selectable && item.selected !== undefined) {
      if (item.selected) {
        if (selectionMode === "single") {
          selected.clear();
        }
        selected.add(String(item.id));
      } else {
        selected.delete(String(item.id));
      }
    }
    markSelected(li);
    if (hasKids) {
      li.setAttribute("aria-expanded", item.expanded ? "true" : "false");
      const group = h("ul", { class: "tree-view-group", role: "group" });
      if (item.children && item.children.length) {
        group.append(...buildGroup(item.children, level + 1));
      }
      group.hidden = !item.expanded;
      li.appendChild(group);
    }
    toggle.addEventListener("click", (event) => {
      event.preventDefault();
      event.stopPropagation();
      toggleItem(li);
    });
    content.addEventListener("click", (event) => {
      if (event.defaultPrevented) {
        return;
      }
      focusEl(li);
      if (typeof onOpen === "function") {
        onOpen(byId.get(li.dataset.id), event);
      } else if (li.hasAttribute("aria-expanded")) {
        toggleItem(li);
      }
    });
    content.addEventListener("contextmenu", (event) => {
      if (typeof onContextMenu === "function") {
        event.preventDefault();
        focusEl(li);
        onContextMenu(byId.get(li.dataset.id), event);
      }
    });
    nodes.set(String(item.id), li);
    return li;
  };
  const groupOf = (li) => {
    for (const child of li.children) {
      if (child.getAttribute("role") === "group") {
        return child;
      }
    }
    return null;
  };
  const parentItem = (li) => {
    const group = li.parentElement;
    return group && group.getAttribute("role") === "group" ? group.parentElement : null;
  };
  const visibleItems = () => {
    const out = [];
    const walk = (list) => {
      for (const li of list.children) {
        if (li.getAttribute("role") !== "treeitem") {
          continue;
        }
        out.push(li);
        if (li.getAttribute("aria-expanded") === "true") {
          const group = groupOf(li);
          if (group) {
            walk(group);
          }
        }
      }
    };
    walk(tree);
    return out;
  };
  const focusEl = (li) => {
    if (!li) {
      return;
    }
    for (const other of tree.querySelectorAll("[role=treeitem]")) {
      if (other !== li) {
        other.setAttribute("tabindex", "-1");
      }
    }
    li.setAttribute("tabindex", "0");
    lastFocused = li;
    lastFocusedId = li.dataset.id || null;
    li.focus({ preventScroll: false });
  };
  const expandEl = async (li) => {
    if (!li || li.getAttribute("aria-expanded") !== "false") {
      return;
    }
    const item = byId.get(li.dataset.id);
    const group = groupOf(li);
    li.setAttribute("aria-expanded", "true");
    group.hidden = false;
    if (typeof onToggle === "function") {
      onToggle(item, true);
    }
    if (!group.children.length && typeof loadChildren === "function") {
      const loading = h("li", { class: "tree-view-item tree-view-loading", role: "treeitem", "aria-disabled": "true", tabindex: "-1", style: { "--tree-level": String(Number(li.getAttribute("aria-level")) + 1) } }, h("div", { class: "tree-view-item-content" }, h("span", { class: "tree-view-indent" }), spinner({ size: "small", label: "" }), h("span", { class: "tree-view-label", text: "Loading…" })));
      group.appendChild(loading);
      li.setAttribute("aria-busy", "true");
      try {
        const kids = (await loadChildren(item)) || [];
        item.children = kids;
        group.replaceChildren(...buildGroup(kids, Number(li.getAttribute("aria-level")) + 1));
      } catch (err) {
        group.replaceChildren();
        li.setAttribute("aria-expanded", "false");
        group.hidden = true;
        openDialog({
          title: "Couldn't load pages",
          width: "small",
          role: "alertdialog",
          narrow: "center",
          returnFocus: li,
          content: h("p", { text: errorMessage(err, "The pages could not be loaded.") }),
          footer: [{ label: "Cancel" }, { label: "Retry", variant: "primary", onClick: () => { later(() => expandEl(li).then(() => focusEl(li)), 0); } }],
        });
        return;
      } finally {
        li.removeAttribute("aria-busy");
      }
    }
    if (!group.children.length) {
      group.appendChild(h("li", { class: "tree-view-item tree-view-empty", role: "treeitem", "aria-disabled": "true", tabindex: "-1", style: { "--tree-level": String(Number(li.getAttribute("aria-level")) + 1) } }, h("div", { class: "tree-view-item-content" }, h("span", { class: "tree-view-indent" }), h("span", { class: "tree-view-label", text: emptyText }))));
    }
  };
  const collapseEl = (li) => {
    if (!li || li.getAttribute("aria-expanded") !== "true") {
      return;
    }
    li.setAttribute("aria-expanded", "false");
    groupOf(li).hidden = true;
    if (typeof onToggle === "function") {
      onToggle(byId.get(li.dataset.id), false);
    }
    if (li.contains(document.activeElement) && document.activeElement !== li) {
      focusEl(li);
    }
  };
  const toggleItem = (li) => {
    if (li.getAttribute("aria-expanded") === "true") {
      collapseEl(li);
    } else {
      expandEl(li);
    }
  };
  const keys = typeahead(300);
  tree.addEventListener("keydown", (event) => {
    const li = event.target && event.target.closest ? event.target.closest("[role=treeitem]") : null;
    if (!li || !tree.contains(li)) {
      return;
    }
    const rows = visibleItems();
    const index = rows.indexOf(li);
    switch (event.key) {
      case "ArrowDown":
        event.preventDefault();
        focusEl(rows[Math.min(rows.length - 1, index + 1)]);
        return;
      case "ArrowUp":
        event.preventDefault();
        focusEl(rows[Math.max(0, index - 1)]);
        return;
      case "ArrowRight":
        event.preventDefault();
        if (li.getAttribute("aria-expanded") === "false") {
          expandEl(li);
        } else if (li.getAttribute("aria-expanded") === "true") {
          const first = Array.from(groupOf(li).children).find((c) => c.getAttribute("role") === "treeitem");
          if (first) {
            focusEl(first);
          }
        }
        return;
      case "ArrowLeft":
        event.preventDefault();
        if (li.getAttribute("aria-expanded") === "true") {
          collapseEl(li);
        } else {
          focusEl(parentItem(li));
        }
        return;
      case "Home":
        event.preventDefault();
        focusEl(rows[0]);
        return;
      case "End":
        event.preventDefault();
        focusEl(rows[rows.length - 1]);
        return;
      case "Enter":
        event.preventDefault();
        if (li.getAttribute("aria-disabled") === "true") {
          return;
        }
        if (typeof onOpen === "function") {
          onOpen(byId.get(li.dataset.id), event);
        } else if (li.hasAttribute("aria-expanded")) {
          toggleItem(li);
        }
        return;
      default:
        if (isPrintable(event) && !(typeof onKey === "function" && event.key === " ")) {
          const typed = keys.push(event.key);
          const labels = rows.map((row) => (row.querySelector(".tree-view-label") || row).textContent.trim().toLowerCase());
          const hit = matchTypeahead(labels, index, typed);
          if (hit >= 0) {
            event.preventDefault();
            focusEl(rows[hit]);
          }
          return;
        }
        if (typeof onKey === "function") {
          onKey(byId.get(li.dataset.id), event);
        }
    }
  });
  tree.addEventListener("focusin", (event) => {
    const li = event.target.closest && event.target.closest("[role=treeitem]");
    if (li && li !== lastFocused) {
      for (const other of tree.querySelectorAll("[role=treeitem]")) {
        other.setAttribute("tabindex", other === li ? "0" : "-1");
      }
      lastFocused = li;
      lastFocusedId = li.dataset.id || null;
    }
  });
  const render = (next) => {
    const active = document.activeElement;
    const focusedId = active && tree.contains(active) && active.closest ? (active.closest("[role=treeitem]") || {}).dataset?.id || null : null;
    byId.clear();
    nodes.clear();
    tree.replaceChildren(...buildGroup(next, 1));
    for (const id of Array.from(selected)) {
      if (!nodes.has(id)) {
        selected.delete(id);
      }
    }
    for (const li of nodes.values()) {
      markSelected(li);
    }
    // The tab stop stays on the last focused row when it still exists.
    const keep = (focusedId && itemEl(focusedId)) || (lastFocusedId && itemEl(lastFocusedId)) || null;
    const start = keep || tree.querySelector("[aria-current=true]") || tree.querySelector("[role=treeitem]");
    if (start) {
      start.setAttribute("tabindex", "0");
      lastFocused = start;
      lastFocusedId = keep ? start.dataset.id : lastFocusedId;
    }
    if (focusedId && keep) {
      keep.focus({ preventScroll: true });
    }
  };
  render(items);
  return {
    element: tree,
    setSelection(ids) {
      const list = (Array.isArray(ids) ? ids : ids === null || ids === undefined ? [] : [ids]).map(String);
      selected.clear();
      for (const id of selectionMode === "single" ? list.slice(0, 1) : list) {
        if (nodes.has(id)) {
          selected.add(id);
        }
      }
      for (const li of nodes.values()) {
        markSelected(li);
      }
    },
    getSelection: () => Array.from(selected),
    setIcon(id, glyph) {
      const li = itemEl(id);
      const content = li ? Array.from(li.children).find((child) => child.classList.contains("tree-view-item-content")) : null;
      const slot = content ? Array.from(content.children).find((child) => child.classList.contains("tree-view-visual")) : null;
      if (!slot) {
        return;
      }
      slot.replaceChildren(...(glyph ? [visual(glyph)] : []));
      const item = byId.get(String(id));
      if (item) {
        item.icon = glyph;
      }
    },
    focusItem: (id) => focusEl(itemEl(id)),
    setCurrent(id) {
      for (const li of tree.querySelectorAll("[aria-current]")) {
        li.removeAttribute("aria-current");
      }
      const li = itemEl(id);
      if (li) {
        li.setAttribute("aria-current", "true");
      }
    },
    expand: (id) => expandEl(itemEl(id)),
    collapse: (id) => collapseEl(itemEl(id)),
    update: render,
  };
}

/* ---------- SegmentedControl, ToggleSwitch, Details ---------- */

/**
 * Builds a Primer SegmentedControl: a ul with aria-label of buttons with
 * aria-current. Tab reaches each segment, Enter or Space selects it, and
 * ArrowLeft, ArrowRight, Home and End also move focus between segments.
 * Icon-only segments get a Tooltip. With iconsOnlyNarrow, segments with an
 * icon hide their text on narrow viewports and keep it as the accessible
 * name. Selection applies at once through onChange.
 * @param {{label: string, items: {label: string, value?: string, icon?: string, iconOnly?: boolean, selected?: boolean}[], size?: "small" | "medium", onChange?: (value: string, index: number) => void, iconsOnlyNarrow?: boolean, fullWidth?: boolean}} options
 * @returns {HTMLElement & {setSelected: (value: string) => void}}
 */
export function segmentedControl({ label: name, items = [], size = "medium", onChange, iconsOnlyNarrow = false, fullWidth = false, itemRole = "" } = {}) {
  // In a menu the segments are menuitemradio items in a group, so the menu keys reach them.
  const radio = itemRole === "menuitemradio";
  const state = radio ? "aria-checked" : "aria-current";
  const el = h("ul", { class: "segmented-control" + (size === "small" ? " segmented-control-sm" : "") + (iconsOnlyNarrow ? " is-icons-narrow" : "") + (fullWidth ? " is-full-width" : ""), role: radio ? "group" : null, "aria-label": name });
  const buttons = [];
  const select = (index, fire = true) => {
    for (const [i, btn] of buttons.entries()) {
      btn.setAttribute(state, i === index ? "true" : "false");
      btn.parentElement.classList.toggle("is-selected", i === index);
    }
    if (fire && typeof onChange === "function") {
      onChange(items[index].value !== undefined ? items[index].value : String(index), index);
    }
  };
  items.forEach((item, index) => {
    const content = h("span", { class: "segmented-control-content" });
    if (item.icon) {
      content.appendChild(h("span", { class: "segmented-control-icon", "aria-hidden": "true" }, icon(item.icon, 16)));
    }
    if (!item.iconOnly) {
      content.appendChild(h("span", { class: "segmented-control-text", dataset: { text: item.label }, text: item.label }));
    }
    const btn = h("button", { type: "button", class: "segmented-control-button", role: radio ? "menuitemradio" : null, tabindex: radio ? "-1" : null, [state]: item.selected ? "true" : "false", "aria-label": item.iconOnly ? item.label : null, dataset: { value: item.value !== undefined ? item.value : String(index) } }, content);
    btn.addEventListener("click", () => {
      if (btn.getAttribute(state) !== "true") {
        select(index);
      }
    });
    btn.addEventListener("keydown", (event) => {
      let next = -1;
      if (event.key === "ArrowRight") {
        next = (index + 1) % buttons.length;
      } else if (event.key === "ArrowLeft") {
        next = (index - 1 + buttons.length) % buttons.length;
      } else if (event.key === "Home" && !radio) {
        next = 0;
      } else if (event.key === "End" && !radio) {
        next = buttons.length - 1;
      }
      if (next >= 0) {
        event.preventDefault();
        buttons[next].focus();
      }
    });
    if (item.iconOnly) {
      tooltip(btn, item.label, { type: "label" });
      btn.removeAttribute("aria-label");
    }
    buttons.push(btn);
    el.appendChild(h("li", { class: "segmented-control-item" + (item.selected ? " is-selected" : ""), role: radio ? "none" : null }, btn));
  });
  el.setSelected = (value) => {
    const index = buttons.findIndex((btn) => btn.dataset.value === String(value));
    if (index >= 0) {
      select(index, false);
    }
  };
  return el;
}

/**
 * Builds a Primer ToggleSwitch: a button with aria-pressed, labelled by the
 * visible label of the setting through aria-labelledby, with "On" and "Off"
 * status text. It applies at once through onChange. When onChange returns a
 * promise, a small Spinner shows beside the switch until it settles, and a
 * rejection turns the switch back.
 * @param {{checked?: boolean, size?: "small" | "medium", labelledBy?: string, describedBy?: string, label?: string, onChange?: (checked: boolean) => any, disabled?: boolean, statusLabelPosition?: "start" | "end"}} options
 * @returns {HTMLElement & {button: HTMLButtonElement, setChecked: (on: boolean) => void}}
 */
export function toggleSwitch({ checked = false, size = "medium", labelledBy = "", describedBy = "", label: name = "", onChange, disabled = false, statusLabelPosition = "start" } = {}) {
  const track = h(
    "button",
    { type: "button", class: "toggle-switch-track", "aria-pressed": checked ? "true" : "false", "aria-labelledby": labelledBy || null, "aria-describedby": describedBy || null, "aria-label": labelledBy ? null : name || null, "aria-disabled": disabled ? "true" : null },
    h("span", { class: "toggle-switch-icons", "aria-hidden": "true" }, h("span", { class: "toggle-switch-line" }), h("span", { class: "toggle-switch-circle" })),
    h("span", { class: "toggle-switch-knob", "aria-hidden": "true" })
  );
  const status = h("span", { class: "toggle-switch-status", "aria-hidden": "true" }, h("span", { class: "toggle-switch-on", text: "On" }), h("span", { class: "toggle-switch-off", text: "Off" }));
  const wrap = h("span", { class: "toggle-switch" + (size === "small" ? " toggle-switch-sm" : "") + (statusLabelPosition === "end" ? " is-status-end" : ""), dataset: { checked: checked ? "true" : "false" } }, status, track);
  const setChecked = (on) => {
    track.setAttribute("aria-pressed", on ? "true" : "false");
    wrap.setAttribute("data-checked", on ? "true" : "false");
  };
  track.addEventListener("click", async () => {
    if (track.getAttribute("aria-disabled") === "true" || wrap.hasAttribute("data-loading")) {
      return;
    }
    const next = track.getAttribute("aria-pressed") !== "true";
    setChecked(next);
    if (typeof onChange !== "function") {
      return;
    }
    const result = onChange(next);
    if (result && typeof result.then === "function") {
      wrap.setAttribute("data-loading", "true");
      const spin = spinner({ size: "small", label: "Saving" });
      spin.classList.add("toggle-switch-spinner");
      wrap.appendChild(spin);
      try {
        await result;
      } catch (err) {
        setChecked(!next);
      } finally {
        spin.remove();
        wrap.removeAttribute("data-loading");
      }
    }
  });
  wrap.button = track;
  wrap.setChecked = setChecked;
  return wrap;
}

/**
 * Builds a Primer Details disclosure. The summary is a Button invisible
 * small with chevron-down while closed and chevron-up while open. kind "add"
 * uses a Button default medium with a leading plus, for forms such as "New
 * space".
 * @param {{summary: string, content?: Node | Node[], open?: boolean, kind?: "disclosure" | "add", onToggle?: (open: boolean) => void}} options
 * @returns {HTMLDetailsElement}
 */
export function details({ summary, content = null, open = false, kind = "disclosure", onToggle } = {}) {
  const summaryEl =
    kind === "add"
      ? h("summary", { class: "btn details-summary" }, h("span", { class: "btn-visual btn-leading", "aria-hidden": "true" }, icon("plus", 16)), h("span", { class: "btn-label", text: summary }))
      : h(
          "summary",
          { class: "btn btn-invisible btn-sm details-summary" },
          h("span", { class: "btn-visual btn-leading details-chevron", "aria-hidden": "true" }, h("span", { class: "details-closed" }, icon("chevron-down", 16)), h("span", { class: "details-open" }, icon("chevron-up", 16))),
          h("span", { class: "btn-label", text: summary })
        );
  const el = h("details", { class: "details" + (kind === "add" ? " details-add" : ""), open }, summaryEl, h("div", { class: "details-body" }, content));
  if (typeof onToggle === "function") {
    el.addEventListener("toggle", () => onToggle(el.open));
  }
  return el;
}

/* ---------- RelativeTime ---------- */

const RELATIVE_TIME_MODULE = "/static/vendor/relative-time-element-5.3.1.js";
let relativeTimeLoad = null;

/** Loads the vendored relative-time element once, in a browser. */
export function loadRelativeTime() {
  if (!relativeTimeLoad && typeof customElements !== "undefined" && !customElements.get("relative-time")) {
    relativeTimeLoad = import(RELATIVE_TIME_MODULE).catch((err) => {
      console.warn("[markspace] relative-time could not load", err);
    });
  }
  return relativeTimeLoad || Promise.resolve();
}

/**
 * Builds a Primer RelativeTime element, such as "5 minutes ago" or "on Aug
 * 2". precise shows "Thursday, Aug 26, 2026, 3:05 PM". The title tooltip is
 * off. The fallback text is the date.
 * @param {Date | string | number} when
 * @param {{precise?: boolean}} [options]
 * @returns {HTMLElement}
 */
export function relativeTime(when, { precise = false } = {}) {
  const date = when instanceof Date ? when : new Date(when);
  const valid = !Number.isNaN(date.getTime());
  const el = h("relative-time", { "no-title": "" });
  if (valid) {
    el.setAttribute("datetime", date.toISOString());
    el.textContent = date.toLocaleDateString("en-US", { month: "short", day: "numeric", year: "numeric" });
  }
  if (precise) {
    for (const [k, v] of Object.entries({ format: "datetime", weekday: "long", year: "numeric", month: "short", day: "numeric", hour: "numeric", minute: "2-digit" })) {
      el.setAttribute(k, v);
    }
  }
  loadRelativeTime();
  return el;
}

/* ---------- Drop area ---------- */

/**
 * Reports whether a file matches an accept list such as "image/*,.pdf".
 * @param {{name: string, type: string}} file
 * @param {string} accept
 * @returns {boolean}
 */
export function acceptsFile(file, accept) {
  if (!accept) {
    return true;
  }
  const name = String(file.name || "").toLowerCase();
  const type = String(file.type || "").toLowerCase();
  return accept
    .split(",")
    .map((part) => part.trim().toLowerCase())
    .filter(Boolean)
    .some((rule) => {
      if (rule.startsWith(".")) {
        return name.endsWith(rule);
      }
      if (rule.endsWith("/*")) {
        return type.startsWith(rule.slice(0, -1));
      }
      return type === rule;
    });
}

/**
 * Builds the drop area from docs/primer.md 5.3: a dashed box that accepts dropped
 * or pasted files, with a Button default small "Choose files" as the keyboard
 * route. The box itself is not focusable. Files of a type outside accept show
 * a critical InlineMessage at the bottom of the box.
 * @param {{accept?: string, multiple?: boolean, onFiles: (files: File[]) => void, label?: string, buttonLabel?: string}} options
 * @returns {HTMLElement}
 */
export function createDropzone({ accept = "", multiple = true, onFiles, label: text = "Drop files here", buttonLabel = "Choose files" } = {}) {
  const input = h("input", { type: "file", class: "sr-only", tabindex: "-1", multiple, accept: accept || null, "aria-hidden": "true" });
  const choose = button({ label: buttonLabel, size: "small", onClick: () => input.click() });
  const error = h("div", { class: "drop-area-error", hidden: true });
  // The hidden input comes before the button, so the button's 44px hit area paints above it.
  const zone = h("div", { class: "drop-area dropzone" }, input, h("span", { class: "drop-area-text dropzone-label", text: text.replace(/ or choose files$/i, "") }), choose, error);
  const deliver = (list) => {
    let files = Array.from(list || []).filter((file) => acceptsFile(file, accept));
    const rejected = (list ? list.length : 0) - files.length;
    if (!multiple) {
      files = files.slice(0, 1);
    }
    zone.classList.toggle("has-error", rejected > 0);
    error.hidden = rejected === 0;
    if (rejected > 0) {
      error.replaceChildren(inlineMessage({ variant: "critical", size: "small", text: rejected === 1 ? "1 file is not an accepted type" : `${rejected} files are not an accepted type` }));
    }
    if (files.length && typeof onFiles === "function") {
      onFiles(files);
    }
  };
  input.addEventListener("change", () => {
    deliver(input.files);
    input.value = "";
  });
  let depth = 0;
  zone.addEventListener("dragenter", (event) => {
    if (hasFiles(event)) {
      event.preventDefault();
      depth++;
      zone.classList.add("is-over");
    }
  });
  zone.addEventListener("dragover", (event) => {
    if (hasFiles(event)) {
      event.preventDefault();
      event.dataTransfer.dropEffect = "copy";
    }
  });
  zone.addEventListener("dragleave", () => {
    depth = Math.max(0, depth - 1);
    if (!depth) {
      zone.classList.remove("is-over");
    }
  });
  zone.addEventListener("drop", (event) => {
    if (!hasFiles(event)) {
      return;
    }
    event.preventDefault();
    depth = 0;
    zone.classList.remove("is-over");
    deliver(event.dataTransfer.files);
  });
  zone.addEventListener("paste", (event) => {
    const files = event.clipboardData && event.clipboardData.files;
    if (files && files.length) {
      event.preventDefault();
      deliver(files);
    }
  });
  return zone;
}

/** Reports whether a drag event carries files. */
function hasFiles(event) {
  return !!(event.dataTransfer && Array.prototype.indexOf.call(event.dataTransfer.types || [], "Files") >= 0);
}

/* ---------- Upload list ---------- */

/**
 * Describes one upload for display.
 * @param {{state: string, loaded?: number, size?: number, error?: string}} upload
 * @param {boolean} [online]
 * @returns {{text: string, percent: number, showBar: boolean}}
 */
export function uploadStatus(upload, online = true) {
  const size = upload.size || 0;
  const percent = size > 0 ? Math.min(100, Math.round(((upload.loaded || 0) / size) * 100)) : 0;
  switch (upload.state) {
    case "uploading":
      return { text: "Uploading " + percent + "%", percent, showBar: true };
    case "queued":
      return { text: online ? "Waiting to upload" : "Waiting for a connection", percent: 0, showBar: true };
    case "failed":
      return { text: "Failed" + (upload.error ? ": " + upload.error : ""), percent, showBar: false };
    case "done":
      return { text: "Uploaded", percent: 100, showBar: false };
  }
  return { text: String(upload.state || ""), percent, showBar: false };
}

/**
 * Builds a live list of uploads from sync.uploads(), updated on the
 * "uploads-changed" event, as Box rows: a 40 px thumbnail, the name, the
 * size and state, a small ProgressBar while uploading, a small Spinner while
 * queued, a small critical InlineMessage on failure, and IconButton actions.
 * Cancelling an upload drops the file, so it asks through a
 * ConfirmationDialog. Pass noteId to show one note's uploads. The element
 * stops listening once it leaves the page. destroy() stops sooner.
 * @param {{sync: object, noteId?: string, emptyText?: string}} options
 * @returns {HTMLElement & {destroy: Function}}
 */
export function uploadList({ sync, noteId, emptyText = "No uploads yet" } = {}) {
  const list = h("ul", { class: "upload-list box", "aria-label": "Uploads" });
  const rows = new Map();
  const empty = h("li", { class: "upload-empty box-row", text: emptyText });
  let unsubscribe = null;
  let seenConnected = false;

  const current = (uploads) => {
    const all = Array.isArray(uploads) ? uploads : [];
    return all.filter((upload) => !noteId || upload.noteId === noteId).sort((a, b) => (b.createdAt || 0) - (a.createdAt || 0));
  };

  const buildRow = () => {
    const thumb = h("span", { class: "upload-thumb", "aria-hidden": "true" });
    const name = h("span", { class: "upload-name" });
    const meta = h("span", { class: "upload-meta" });
    const bar = progressBar({ size: "small", color: "accent" });
    bar.classList.add("progress");
    const queued = spinner({ size: "small", label: "" });
    queued.classList.add("upload-queued");
    const failure = h("div", { class: "upload-failure" });
    const actions = h("span", { class: "upload-actions" });
    const row = h("li", { class: "upload-row box-row" }, thumb, h("span", { class: "upload-body" }, name, meta, bar, failure), h("span", { class: "upload-trail" }, queued, actions));
    return { row, thumb, name, meta, bar, queued, failure, actions, state: null, url: null };
  };

  const actionButton = (text, iconName, onClick) => iconButton({ icon: iconName, label: text, size: "small", onClick });

  const updateRow = (entry, upload) => {
    const online = typeof navigator === "undefined" || navigator.onLine !== false;
    const state = uploadStatus(upload, online);
    entry.row.dataset.state = upload.state;
    entry.name.textContent = upload.filename || "Untitled file";
    entry.meta.textContent = formatBytes(upload.size) + " · " + (upload.state === "failed" ? "Failed" : upload.state === "queued" ? "Queued" : state.text);
    entry.bar.hidden = upload.state !== "uploading";
    entry.bar.setAttribute("aria-label", "Upload progress for " + (upload.filename || "file"));
    entry.bar.set(state.percent);
    entry.queued.hidden = upload.state !== "queued";
    entry.failure.replaceChildren(upload.state === "failed" ? inlineMessage({ variant: "critical", size: "small", text: upload.error ? sentence(upload.error) : "The upload failed." }) : "");
    const isImage = /^image\//.test(upload.mime || "");
    if (entry.url !== (upload.url || null) || entry.thumb.childNodes.length === 0) {
      entry.url = upload.url || null;
      if (isImage && upload.url && upload.state === "done") {
        entry.thumb.replaceChildren(h("img", { src: upload.url, alt: "", loading: "lazy", decoding: "async" }));
      } else {
        entry.thumb.replaceChildren(icon(fileIconName(upload.mime, upload.filename), 16));
      }
    }
    if (entry.state !== upload.state) {
      entry.state = upload.state;
      const buttons = [];
      if (upload.state === "failed" && typeof sync.retryUpload === "function") {
        buttons.push(
          actionButton("Retry upload", "sync", () =>
            Promise.resolve(sync.retryUpload(upload.key)).catch((err) => {
              entry.failure.replaceChildren(inlineMessage({ variant: "critical", size: "small", text: errorMessage(err, "Retry failed.") }));
            })
          )
        );
      }
      if ((upload.state === "failed" || upload.state === "queued") && typeof sync.cancelUpload === "function") {
        buttons.push(
          actionButton("Cancel upload", "x", () =>
            confirmDialog({
              title: "Cancel this upload?",
              body: `“${upload.filename || "The file"}” will not be uploaded.`,
              confirmLabel: "Cancel upload",
              cancelLabel: "Keep upload",
              onConfirm: () => sync.cancelUpload(upload.key),
            })
          )
        );
      }
      if (upload.state === "done" && upload.url) {
        const copy = actionButton("Copy link", "link", async () => {
          const link = new URL(upload.url, location.origin).href;
          try {
            await navigator.clipboard.writeText(link);
            copyFeedback(copy);
          } catch (err) {
            entry.failure.replaceChildren(h("span", { class: "upload-link", text: link }));
          }
        });
        buttons.push(copy);
      }
      entry.actions.replaceChildren(...buttons);
    }
  };

  const render = (uploads) => {
    if (list.isConnected) {
      seenConnected = true;
    } else if (seenConnected) {
      list.destroy();
      return;
    }
    const items = current(uploads);
    const keep = new Set();
    let previous = null;
    for (const upload of items) {
      keep.add(upload.key);
      let entry = rows.get(upload.key);
      if (!entry) {
        entry = buildRow(upload);
        rows.set(upload.key, entry);
      }
      updateRow(entry, upload);
      const expected = previous ? previous.nextSibling : list.firstChild;
      if (expected !== entry.row) {
        list.insertBefore(entry.row, expected);
      }
      previous = entry.row;
    }
    for (const [key, entry] of rows) {
      if (!keep.has(key)) {
        entry.row.remove();
        rows.delete(key);
      }
    }
    if (items.length) {
      empty.remove();
    } else if (!empty.isConnected && emptyText) {
      list.appendChild(empty);
    }
    list.classList.toggle("is-empty", !items.length);
  };

  list.destroy = () => {
    if (unsubscribe) {
      unsubscribe();
      unsubscribe = null;
    }
  };

  if (!sync || typeof sync.uploads !== "function") {
    console.error("[markspace] uploadList needs sync.uploads(). See docs/offline.md.");
    list.appendChild(h("li", { class: "upload-empty box-row", text: "Uploads are not available yet" }));
    return list;
  }
  render(sync.uploads());
  if (typeof sync.on === "function") {
    unsubscribe = sync.on("uploads-changed", (event) => render(event && event.uploads ? event.uploads : sync.uploads()));
  }
  return list;
}

/**
 * Shows that a copy worked, as Primer's copy buttons do: the icon of the
 * button turns into a check in --fgColor-success for 2 s and "Copied" is
 * announced.
 * @param {HTMLElement} el
 */
export function copyFeedback(el) {
  announce("Copied");
  const slot = el.querySelector(".btn-visual") || (el.querySelector("svg") ? el.querySelector("svg").parentNode : null);
  if (!slot || el.classList.contains("is-copied")) {
    return;
  }
  const saved = Array.from(slot.childNodes);
  slot.replaceChildren(icon("check", 16));
  el.classList.add("is-copied");
  later(() => {
    slot.replaceChildren(...saved);
    el.classList.remove("is-copied");
  }, 2000);
}

/**
 * Picks an icon name for a file from its media type or extension.
 * @param {string} mime
 * @param {string} [name]
 * @returns {string}
 */
export function fileIconName(mime, name = "") {
  const type = String(mime || "");
  const ext = String(name || "").toLowerCase().split(".").pop();
  if (type.startsWith("image/")) {
    return "image";
  }
  if (type.startsWith("video/")) {
    return "video";
  }
  if (type.startsWith("audio/")) {
    return "audio";
  }
  if (/zip|tar|gzip|compressed/.test(type) || ["zip", "tar", "gz", "7z", "rar"].includes(ext)) {
    return "archive";
  }
  return "file";
}
