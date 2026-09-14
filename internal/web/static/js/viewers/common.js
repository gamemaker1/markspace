// Shared pieces of the file viewers: the Primer toolbar and its IconButtons,
// the loading, error and no-preview Blankslates, downloads and small helpers.

import { h, icon, iconButton, button, blankslate, spinner, focusables, formatBytes } from "../ui.js";

/**
 * Builds a toolbar IconButton: invisible, medium, with a Tooltip that names it.
 * Narrow viewports enlarge it through files.css.
 * @param {{label: string, icon: string, onClick?: Function, pressed?: boolean, className?: string, size?: "small"|"medium"|"large", variant?: string, attrs?: object}} options
 * @returns {HTMLButtonElement}
 */
export function toolButton({ label, icon: glyph, onClick, pressed, className = "", size = "medium", variant = "invisible", attrs = {} }) {
  return iconButton({ icon: glyph, label, variant, size, pressed, onClick, className: ("fv-tool " + className).trim(), attrs });
}

/**
 * Sets the inactive state of a toolbar button. Primer keeps such a control
 * focusable with aria-disabled instead of disabled.
 * @param {HTMLElement} el
 * @param {boolean} off
 */
export function setInactive(el, off) {
  if (off) el.setAttribute("aria-disabled", "true");
  else el.removeAttribute("aria-disabled");
}

/**
 * Swaps the Octicon inside an IconButton.
 * @param {HTMLElement} el
 * @param {string} glyph
 */
export function setToolIcon(el, glyph) {
  const slot = el.querySelector(".btn-visual");
  if (slot) slot.replaceChildren(icon(glyph, 16));
}

/**
 * Builds a viewer toolbar with role toolbar. Left, Right, Home and End move
 * focus between its controls, as docs/primer.md 9.5 lists. Text fields and
 * SegmentedControls keep their own arrow keys.
 * @param {string} label
 * @param {Array<Node|null>} children
 * @param {{className?: string}} [options]
 * @returns {HTMLElement}
 */
export function toolbar(label, children, { className = "" } = {}) {
  const el = h("div", { class: ("fv-toolbar " + className).trim(), role: "toolbar", "aria-label": label }, children);
  el.addEventListener("keydown", (event) => {
    if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey) return;
    if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
    const target = event.target;
    if (!(target instanceof Element) || target.matches("input, textarea, select")) return;
    const items = focusables(el).filter((node) => node.getClientRects().length > 0);
    const at = items.indexOf(target);
    if (at < 0 || !items.length) return;
    let next = at;
    if (event.key === "ArrowRight") next = (at + 1) % items.length;
    else if (event.key === "ArrowLeft") next = (at - 1 + items.length) % items.length;
    else if (event.key === "Home") next = 0;
    else next = items.length - 1;
    event.preventDefault();
    items[next].focus();
  });
  return el;
}

/**
 * Builds a Primer ButtonGroup: adjoining default buttons in a group.
 * @param {string} label
 * @param {HTMLElement[]} buttons
 * @returns {HTMLElement}
 */
export function buttonGroup(label, buttons) {
  return h("div", { class: "fv-button-group", role: "group", "aria-label": label }, buttons);
}

/**
 * Marks one segment of a SegmentedControl as selected, or none for null. A
 * custom zoom level matches no segment.
 * @param {HTMLElement} control
 * @param {string|null} value
 */
export function markSegment(control, value) {
  for (const btn of control.querySelectorAll(".segmented-control-button")) {
    const on = value !== null && btn.dataset.value === value;
    btn.setAttribute("aria-current", on ? "true" : "false");
    if (btn.parentElement) btn.parentElement.classList.toggle("is-selected", on);
  }
}

/**
 * Builds the find bar of a viewer: a small TextInput with a leading search
 * Octicon, the match count in 12px muted text, and small IconButtons for the
 * previous match, the next match and Close. Enter runs the query or moves to
 * the next match, Shift+Enter to the previous one, and Escape closes.
 * @param {{label?: string, onQuery: (query: string, options: {step?: number}) => void, onStep: (dir: number) => void, onClose: (focusTrigger: boolean) => void}} options
 * @returns {{element: HTMLElement, input: HTMLInputElement, open(): void, close(): void, setCount(text: string, matches: number): void}}
 */
export function findBar({ label = "Find", onQuery, onStep, onClose }) {
  const input = h("input", { type: "search", class: "text-input-control fv-find-input", "aria-label": label, autocomplete: "off", enterkeyhint: "search", spellcheck: "false" });
  const count = h("span", { class: "fv-find-count", "aria-live": "polite" });
  const prev = toolButton({ label: "Previous match", icon: "chevron-up", size: "small", onClick: () => onStep(-1) });
  const next = toolButton({ label: "Next match", icon: "chevron-down", size: "small", onClick: () => onStep(1) });
  const close = toolButton({ label: "Close find", icon: "x", size: "small", onClick: () => onClose(true) });
  const box = h("span", { class: "text-input text-input-sm has-leading-visual fv-find-box" }, h("span", { class: "text-input-visual", "aria-hidden": "true" }, icon("search", 16)), input);
  const element = h("div", { class: "fv-find", role: "search", "aria-label": label, hidden: true }, box, count, prev, next, close);
  setInactive(prev, true);
  setInactive(next, true);
  let timer = 0;
  input.addEventListener("input", () => {
    clearTimeout(timer);
    timer = setTimeout(() => onQuery(input.value.trim(), {}), 200);
  });
  input.addEventListener("keydown", (event) => {
    if (event.key === "Enter") {
      event.preventDefault();
      clearTimeout(timer);
      onQuery(input.value.trim(), { step: event.shiftKey ? -1 : 1 });
    } else if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      onClose(true);
    }
  });
  return {
    element,
    input,
    open() {
      element.hidden = false;
      input.focus();
      input.select();
    },
    close() {
      element.hidden = true;
      clearTimeout(timer);
      input.value = "";
      count.textContent = "";
      setInactive(prev, true);
      setInactive(next, true);
    },
    setCount(text, matches) {
      count.textContent = text;
      setInactive(prev, !(matches > 1));
      setInactive(next, !(matches > 1));
    },
  };
}

/**
 * Builds the loading state of a viewer: a medium Spinner, centred, shown
 * after 1s as docs/primer.md 6.5 requires.
 * @param {string} label
 * @returns {HTMLElement}
 */
export function loadingState(label = "Loading file") {
  return h("div", { class: "fv-loading" }, spinner({ size: "medium", label, delay: 1000 }));
}

/**
 * Starts a download through a temporary link that the shell leaves to the
 * browser.
 * @param {string} url
 * @param {string} [name]
 */
export function startDownload(url, name = "") {
  if (!url) return;
  const a = h("a", { href: url, download: name, "data-full-nav": "", hidden: true });
  document.body.appendChild(a);
  a.click();
  a.remove();
}

/**
 * Builds a Download Button for a Blankslate or a Banner.
 * @param {{downloadUrl?: string, file?: object}} ctx
 * @param {"primary"|"default"} [variant]
 * @returns {HTMLElement|null}
 */
export function downloadButton(ctx, variant = "default") {
  if (!ctx || !ctx.downloadUrl) return null;
  const name = (ctx.file && ctx.file.name) || "";
  return button({ label: "Download", variant, leadingVisual: "download", onClick: () => startDownload(ctx.downloadUrl, name) });
}

/** Appends an action row to a Blankslate built by ui.js. */
function addBlankslateAction(slate, node) {
  if (!node) return;
  const inner = slate.querySelector(".blankslate-inner") || slate;
  inner.appendChild(h("div", { class: "blankslate-action" }, node));
}

/**
 * Builds the error state a viewer shows when its content cannot load: a
 * medium Blankslate with alert, the title "This file could not be shown",
 * the reason and a default Download button.
 * @param {{downloadUrl?: string, file?: object}} ctx
 * @param {string} message
 * @returns {HTMLElement}
 */
export function errorState(ctx, message) {
  const slate = blankslate({ icon: "alert", title: "This file could not be shown", description: message || "", size: "medium", headingLevel: 2 });
  slate.classList.add("fv-error");
  slate.setAttribute("role", "alert");
  addBlankslateAction(slate, downloadButton(ctx, "default"));
  return h("div", { class: "fv-state" }, slate);
}

/**
 * Builds the no-preview state: a medium Blankslate with the file type
 * Octicon, a description list of size, type and dates, and a primary
 * Download button.
 * @param {{downloadUrl?: string, file?: object}} ctx
 * @param {{icon: string, title: string, description?: string}} options
 * @returns {HTMLElement}
 */
export function noPreviewState(ctx, { icon: glyph, title, description = "" }) {
  const file = (ctx && ctx.file) || {};
  const slate = blankslate({ icon: glyph, title, description, size: "medium", headingLevel: 2 });
  slate.classList.add("fv-no-preview");
  const facts = fileFacts(file);
  if (facts.length) {
    const inner = slate.querySelector(".blankslate-inner") || slate;
    inner.appendChild(h("dl", { class: "fv-facts" }, facts.map(([k, v]) => h("div", { class: "fv-fact" }, h("dt", { text: k }), h("dd", { text: v })))));
  }
  addBlankslateAction(slate, downloadButton(ctx, "primary"));
  return h("div", { class: "fv-state" }, slate);
}

/**
 * Lists the facts of a file for the no-preview state, skipping empty values.
 * @param {object} file
 * @returns {[string, string][]}
 */
export function fileFacts(file) {
  return [
    ["Size", formatBytes((file && file.size) || 0)],
    ["Type", (file && file.mime) || "Unknown"],
    ["Created", formatDate(file && file.createdAt)],
    ["Updated", formatDate(file && (file.updatedAt || file.modTime))],
  ].filter(([, v]) => v);
}

/**
 * Formats a timestamp as a local date and time, or returns "" when it is missing.
 * @param {string|number|Date} value
 * @returns {string}
 */
export function formatDate(value) {
  if (!value) return "";
  const t = value instanceof Date ? value.getTime() : typeof value === "number" ? value : Date.parse(value);
  if (!Number.isFinite(t) || t <= 0) return "";
  try {
    return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(t));
  } catch {
    return new Date(t).toISOString();
  }
}

/**
 * Reports whether an error came from an aborted fetch or unmount.
 * @param {any} err
 * @returns {boolean}
 */
export function isAbort(err) {
  return Boolean(err && (err.name === "AbortError" || err.name === "AbortException"));
}

/**
 * Clamps a number into a range.
 * @param {number} v
 * @param {number} min
 * @param {number} max
 * @returns {number}
 */
export function clamp(v, min, max) {
  return Math.min(max, Math.max(min, v));
}
