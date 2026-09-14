// PDF viewer built on the vendored pdf.js bundle. Pages render lazily near
// the viewport. The toolbar holds the thumbnails toggle, the page field, zoom
// and find. Thumbnails show in a collapsible pane on wide viewports and in a
// Dialog on narrow ones.

import { h, uid, segmentedControl, openDialog, NARROW_QUERY } from "../ui.js";
import { toolButton, toolbar, buttonGroup, findBar, markSegment, setInactive, setToolIcon, errorState, loadingState, isAbort, clamp } from "./common.js";

export const WORKER_SRC = "/static/vendor/pdfworker.js";
export const MIN_ZOOM = 0.25;
export const MAX_ZOOM = 5;
export const ZOOM_STEPS = [0.25, 0.33, 0.5, 0.67, 0.8, 0.9, 1, 1.1, 1.25, 1.5, 1.75, 2, 2.5, 3, 4, 5];
const MAX_CANVAS_PIXELS = 16e6;
const MAX_RENDERED = 8;
/** The largest zoom the opening fit uses, so a wide screen does not blow a page up. */
export const AUTO_MAX_ZOOM = 1.5;
const THUMB_WIDTH = 144;
const THUMBS_KEY = "markspace.pdf.thumbs";

let libPromise = null;

/**
 * Loads the pdf.js bundle once and points it at the vendored worker.
 * @returns {Promise<object>}
 */
export function loadPdfjs() {
  if (!libPromise) {
    libPromise = import("../../vendor/pdfjs.js").then((lib) => {
      lib.GlobalWorkerOptions.workerSrc = WORKER_SRC;
      return lib;
    });
    libPromise.catch(() => {
      libPromise = null;
    });
  }
  return libPromise;
}

/**
 * Joins the text items of one page. Items without str are marked content and
 * are skipped. An item that ends a line adds a line feed. starts holds the
 * offset of each item with str, in the order the text layer creates its spans.
 * @param {{str?: string, hasEOL?: boolean}[]} items
 * @returns {{text: string, starts: number[]}}
 */
export function pageText(items) {
  let text = "";
  const starts = [];
  for (const item of items || []) {
    if (typeof item.str !== "string") continue;
    starts.push(text.length);
    text += item.str;
    if (item.hasEOL) text += "\n";
  }
  return { text, starts };
}

/**
 * Finds every case-insensitive occurrence of query in the page texts.
 * Matches do not overlap.
 * @param {string[]} texts the text of each page, page 1 first
 * @param {string} query
 * @returns {{page: number, index: number, length: number}[]} page is 1-based
 */
export function findMatches(texts, query) {
  const q = String(query || "").toLowerCase();
  const out = [];
  if (!q) return out;
  (texts || []).forEach((t, i) => {
    const lower = String(t || "").toLowerCase();
    for (let at = lower.indexOf(q); at >= 0; at = lower.indexOf(q, at + q.length)) {
      out.push({ page: i + 1, index: at, length: q.length });
    }
  });
  return out;
}

/**
 * Returns the 1-based page that holds the vertical offset y.
 * @param {{top: number, height: number}[]} pages in document order
 * @param {number} y
 * @returns {number} 0 when there are no pages
 */
export function pageAt(pages, y) {
  if (!pages || !pages.length) return 0;
  let lo = 0;
  let hi = pages.length - 1;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if (pages[mid].top <= y) lo = mid;
    else hi = mid - 1;
  }
  return lo + 1;
}

/**
 * Returns the next zoom step above or below the current scale.
 * @param {number} scale
 * @param {1|-1} direction
 * @returns {number}
 */
export function nextZoom(scale, direction) {
  if (direction > 0) return ZOOM_STEPS.find((s) => s > scale + 0.001) || MAX_ZOOM;
  return [...ZOOM_STEPS].reverse().find((s) => s < scale - 0.001) || MIN_ZOOM;
}

/**
 * Returns the scale that fits a page width into a box width.
 * @param {number} pageWidth page width in PDF units
 * @param {number} boxWidth available CSS pixels
 * @returns {number}
 */
export function fitWidthScale(pageWidth, boxWidth) {
  if (!(pageWidth > 0) || !(boxWidth > 0)) return 1;
  return clamp(boxWidth / pageWidth, MIN_ZOOM, MAX_ZOOM);
}

/**
 * Parses the page field. It accepts a whole number within the document and
 * clamps anything else that holds a number.
 * @param {string} value
 * @param {number} count
 * @returns {number|null} null when the value holds no number
 */
export function parsePageInput(value, count) {
  const n = Number.parseInt(String(value || "").replace(/[^\d-]/g, ""), 10);
  if (!Number.isFinite(n) || count < 1) return null;
  return clamp(n, 1, count);
}

/**
 * Merges match ranges into non-overlapping marks per text span.
 * @param {number[]} starts offset of each span in the page text
 * @param {string[]} strs text of each span
 * @param {{index: number, length: number, current?: boolean}[]} matches matches on this page
 * @returns {Map<number, {start: number, end: number, current: boolean}[]>} span index to local ranges
 */
export function spanRanges(starts, strs, matches) {
  const out = new Map();
  for (const m of matches) {
    const end = m.index + m.length;
    for (let i = 0; i < starts.length; i++) {
      const s = starts[i];
      const e = s + String(strs[i] ?? "").length;
      if (e <= m.index || s >= end || e === s) continue;
      const list = out.get(i) || [];
      list.push({ start: Math.max(0, m.index - s), end: Math.min(e, end) - s, current: Boolean(m.current) });
      out.set(i, list);
    }
  }
  return out;
}

/**
 * Mounts the PDF viewer.
 * @param {HTMLElement} container
 * @param {{file: object, url: string, downloadUrl: string, isNarrow: () => boolean, signal: AbortSignal}} ctx
 * @returns {Promise<{unmount(): void}>}
 */
export async function mountViewer(container, ctx) {
  const file = ctx.file || {};
  const mql = typeof window.matchMedia === "function" ? window.matchMedia(NARROW_QUERY) : null;
  const isNarrow = () => (ctx.isNarrow ? ctx.isNarrow() : Boolean(mql && mql.matches));
  const ac = new AbortController();
  const on = (target, type, fn, opts = {}) => target.addEventListener(type, fn, { ...opts, signal: ac.signal });
  let destroyed = false;
  let lib = null;
  let doc = null;
  let loadingTask = null;

  /* Toolbar. */
  let thumbsOpen = true;
  try {
    thumbsOpen = localStorage.getItem(THUMBS_KEY) !== "0";
  } catch {
    thumbsOpen = true;
  }
  const thumbsId = uid("fv-pdf-thumbs");
  const findId = uid("fv-pdf-find");
  const pageId = uid("fv-pdf-page");
  const thumbsButton = toolButton({ label: "Page thumbnails", icon: "sidebar-expand", className: "fv-pdf-thumbs-toggle", attrs: { "aria-controls": thumbsId }, onClick: () => toggleThumbs() });
  const prevButton = toolButton({ label: "Previous page", icon: "chevron-up", onClick: () => goToPage(current - 1) });
  const nextButton = toolButton({ label: "Next page", icon: "chevron-down", onClick: () => goToPage(current + 1) });
  const pageInput = h("input", { id: pageId, class: "text-input-control fv-page-input", type: "text", inputmode: "numeric", enterkeyhint: "go", autocomplete: "off", value: "1" });
  const pageTotal = h("span", { class: "fv-page-total", text: "of 0" });
  const pageField = h(
    "div",
    { class: "fv-page-field" },
    h("label", { class: "fv-page-label", for: pageId, text: "Page" }),
    h("span", { class: "text-input text-input-sm fv-page-box" }, pageInput),
    pageTotal
  );
  const zoomOut = toolButton({ label: "Zoom out", icon: "dash", variant: "default", onClick: () => setScale(nextZoom(scale, -1), "custom") });
  const zoomIn = toolButton({ label: "Zoom in", icon: "plus", variant: "default", onClick: () => setScale(nextZoom(scale, 1), "custom") });
  const zoomLevel = h("span", { class: "fv-zoom-level", text: "100%" });
  const presets = segmentedControl({
    label: "Zoom preset",
    size: "small",
    items: [
      { label: "Fit", value: "fit", selected: true },
      { label: "100%", value: "actual" },
    ],
    onChange: (value) => (value === "fit" ? fitWidth() : setScale(1, "custom")),
  });
  presets.classList.add("fv-zoom-presets");
  const findButton = toolButton({ label: "Find in document", icon: "search", attrs: { "aria-expanded": "false", "aria-controls": findId }, onClick: () => (findUi.element.hidden ? openFind() : closeFind(true)) });

  const bar = toolbar(
    "PDF",
    [
      thumbsButton,
      h("div", { class: "fv-group fv-page-nav" }, prevButton, pageField, nextButton),
      h("span", { class: "fv-spacer" }),
      h("div", { class: "fv-group fv-zoom" }, buttonGroup("Zoom", [zoomOut, zoomIn]), zoomLevel, presets),
      findButton,
    ],
    { className: "fv-pdf-toolbar" }
  );
  const find = { query: "", texts: [], starts: [], matches: [], current: -1, token: 0, pendingScroll: false };
  const findUi = findBar({
    label: "Find in document",
    onQuery: (query, { step }) => {
      if (step && query && query === find.query && find.matches.length) stepMatch(step);
      else runFind(query);
    },
    onStep: (dir) => stepMatch(dir),
    onClose: (focusTrigger) => closeFind(focusTrigger),
  });
  findUi.element.id = findId;

  const thumbList = h("ol", { class: "fv-pdf-thumb-list" });
  const thumbs = h("aside", { id: thumbsId, class: "fv-pdf-thumbs", "aria-label": "Page thumbnails", hidden: !thumbsOpen }, thumbList);
  const pagesEl = h("div", { class: "fv-pdf-pages" });
  const scroller = h("div", { class: "fv-pdf-scroll", tabindex: "0", role: "region", "aria-label": (file.name || "PDF") + " pages" }, pagesEl);
  const body = h("div", { class: "fv-pdf-body" }, thumbs, scroller);
  const loading = loadingState("Loading PDF");
  const root = h("div", { class: "fv-pdf is-loading", "data-page-count": "0", "data-current-page": "0" }, bar, findUi.element, body, loading);
  container.appendChild(root);

  /* State. */
  let pages = [];
  let scale = 1;
  let mode = "fit";
  let current = 1;
  let count = 0;
  let base = { width: 612, height: 792 };
  const renderQueue = new Set();
  let rendering = false;
  let rerenderTimer = 0;
  let scrollFrame = 0;
  let dialog = null;

  let pageObserver = null;
  let thumbObserver = null;
  let resize = null;
  const handle = {
    unmount() {
      if (destroyed) return;
      destroyed = true;
      ac.abort();
      clearTimeout(rerenderTimer);
      cancelAnimationFrame(scrollFrame);
      if (dialog) dialog.close();
      if (pageObserver) pageObserver.disconnect();
      if (thumbObserver) thumbObserver.disconnect();
      if (resize) resize.disconnect();
      for (const p of pages) releasePage(p);
      // pdfjs-dist 6 destroys the document through its loading task.
      try {
        if (loadingTask) loadingTask.destroy();
        else if (doc && typeof doc.destroy === "function") doc.destroy();
      } catch (err) {
        console.warn("[markspace] pdf cleanup failed", err);
      }
      root.remove();
    },
  };
  if (ctx.signal) ctx.signal.addEventListener("abort", () => handle.unmount(), { once: true });

  /** Describes the thumbnails toggle for the viewport: a pane toggle, or a Dialog trigger. */
  const syncThumbsButton = () => {
    if (isNarrow()) {
      thumbsButton.removeAttribute("aria-pressed");
      thumbsButton.setAttribute("aria-haspopup", "dialog");
      setToolIcon(thumbsButton, "sidebar-expand");
    } else {
      thumbsButton.removeAttribute("aria-haspopup");
      thumbsButton.setAttribute("aria-pressed", thumbsOpen ? "true" : "false");
      setToolIcon(thumbsButton, thumbsOpen ? "sidebar-collapse" : "sidebar-expand");
    }
  };
  syncThumbsButton();
  if (mql) on(mql, "change", syncThumbsButton);

  /* Load. */
  let needsPassword = false;
  try {
    const [loadedLib, data] = await Promise.all([
      loadPdfjs(),
      fetch(ctx.url, { credentials: "same-origin", signal: ac.signal }).then((res) => {
        if (!res.ok) throw new Error("status " + res.status);
        return res.arrayBuffer();
      }),
    ]);
    if (destroyed) return handle;
    lib = loadedLib;
    loadingTask = lib.getDocument({
      data: new Uint8Array(data),
      isEvalSupported: false,
      useSystemFonts: true,
      useWorkerFetch: false,
      enableXfa: false,
      disableAutoFetch: true,
      disableStream: true,
    });
    loadingTask.onPassword = () => {
      needsPassword = true;
      loadingTask.destroy();
    };
    doc = await loadingTask.promise;
    const first = await doc.getPage(1);
    const vp = first.getViewport({ scale: 1 });
    base = { width: vp.width, height: vp.height };
    if (destroyed) return handle;
  } catch (err) {
    if (destroyed || (isAbort(err) && !needsPassword)) return handle;
    const message = needsPassword || (err && err.name === "PasswordException")
      ? "This PDF is protected by a password. Download it to open it in another app."
      : "The PDF could not be loaded.";
    root.classList.remove("is-loading");
    root.replaceChildren(errorState(ctx, message));
    return handle;
  }

  /* Pages and thumbnails. */
  count = doc.numPages;
  root.dataset.pageCount = String(count);
  pageTotal.textContent = "of " + count;
  pages = Array.from({ length: count }, (_, i) => {
    const num = i + 1;
    const el = h("div", { class: "fv-pdf-page", "data-page": String(num), role: "img", "aria-label": "Page " + num });
    const thumbBox = h("span", { class: "fv-pdf-thumb-box" });
    const thumbButton = h(
      "button",
      { class: "fv-pdf-thumb", type: "button", "aria-label": "Page " + num, dataset: { page: String(num) } },
      thumbBox,
      h("span", { class: "fv-pdf-thumb-num", "aria-hidden": "true", text: String(num) })
    );
    on(thumbButton, "click", () => {
      goToPage(num);
      if (dialog) dialog.close();
    });
    pagesEl.appendChild(el);
    thumbList.appendChild(h("li", {}, thumbButton));
    return { num, el, width: base.width, height: base.height, proxy: null, canvas: null, textEl: null, textLayer: null, task: null, renderedScale: 0, token: 0, near: false, thumbButton, thumbBox, thumbDone: false, marked: [] };
  });
  const byEl = new Map(pages.map((p) => [p.el, p]));
  const byThumb = new Map(pages.map((p) => [p.thumbButton, p]));

  const getPage = async (p) => {
    if (!p.proxy) {
      p.proxy = await doc.getPage(p.num);
      const vp = p.proxy.getViewport({ scale: 1 });
      if (Math.abs(vp.width - p.width) > 0.5 || Math.abs(vp.height - p.height) > 0.5) {
        p.width = vp.width;
        p.height = vp.height;
        sizePage(p);
      }
    }
    return p.proxy;
  };
  const sizePage = (p) => {
    p.el.style.width = Math.floor(p.width * scale) + "px";
    p.el.style.height = Math.floor(p.height * scale) + "px";
    p.el.style.setProperty("--scale-factor", String(scale));
    p.el.style.setProperty("--total-scale-factor", String(scale));
    p.thumbBox.style.aspectRatio = p.width + " / " + p.height;
  };

  const releasePage = (p) => {
    p.token++;
    if (p.task) {
      p.task.cancel();
      p.task = null;
    }
    if (p.textLayer) p.textLayer.cancel();
    if (p.canvas) {
      p.canvas.width = 0;
      p.canvas.height = 0;
      p.canvas.remove();
    }
    if (p.textEl) p.textEl.remove();
    p.canvas = p.textEl = p.textLayer = null;
    p.marked = [];
    p.renderedScale = 0;
    p.el.classList.remove("is-rendered");
  };

  const renderPage = async (p) => {
    const token = ++p.token;
    const target = scale;
    const page = await getPage(p);
    if (destroyed || token !== p.token) return;
    const cssViewport = page.getViewport({ scale: target });
    let out = Math.min(window.devicePixelRatio || 1, 2);
    if (cssViewport.width * cssViewport.height * out * out > MAX_CANVAS_PIXELS) {
      out = Math.sqrt(MAX_CANVAS_PIXELS / (cssViewport.width * cssViewport.height));
    }
    const viewport = page.getViewport({ scale: target * out });
    const canvas = h("canvas", { class: "fv-pdf-canvas", "aria-hidden": "true" });
    canvas.width = Math.floor(viewport.width);
    canvas.height = Math.floor(viewport.height);
    p.task = page.render({ canvas, viewport });
    try {
      await p.task.promise;
    } catch (err) {
      if (err && err.name === "RenderingCancelledException") return;
      throw err;
    } finally {
      if (token === p.token) p.task = null;
    }
    if (destroyed || token !== p.token) return;
    if (p.canvas) {
      p.canvas.width = 0;
      p.canvas.remove();
    }
    p.el.prepend(canvas);
    p.canvas = canvas;
    p.renderedScale = target;
    p.el.classList.add("is-rendered");
    if (p.textLayer) p.textLayer.cancel();
    if (p.textEl) p.textEl.remove();
    const textEl = h("div", { class: "textLayer" });
    p.el.appendChild(textEl);
    const textLayer = new lib.TextLayer({ textContentSource: page.streamTextContent(), container: textEl, viewport: cssViewport });
    p.textEl = textEl;
    p.textLayer = textLayer;
    p.marked = [];
    try {
      await textLayer.render();
    } catch (err) {
      if (!isAbort(err)) console.warn("[markspace] pdf text layer failed", err);
      return;
    }
    if (destroyed || token !== p.token) return;
    applyHighlights(p);
  };

  const trimRendered = () => {
    const rendered = pages.filter((p) => p.canvas && !p.near);
    const total = pages.filter((p) => p.canvas).length;
    if (total <= MAX_RENDERED) return;
    rendered.sort((a, b) => Math.abs(b.num - current) - Math.abs(a.num - current));
    for (const p of rendered.slice(0, total - MAX_RENDERED)) releasePage(p);
  };

  const pump = async () => {
    if (rendering || destroyed) return;
    rendering = true;
    try {
      while (renderQueue.size && !destroyed) {
        const next = [...renderQueue].sort((a, b) => Math.abs(a.num - current) - Math.abs(b.num - current))[0];
        renderQueue.delete(next);
        if (!next.near || next.renderedScale === scale) continue;
        try {
          await renderPage(next);
        } catch (err) {
          if (!destroyed) console.warn("[markspace] pdf page render failed", next.num, err);
        }
        trimRendered();
      }
    } finally {
      rendering = false;
    }
  };
  const queue = (p) => {
    renderQueue.add(p);
    pump();
  };

  /* Thumbnails. */
  const thumbQueue = new Set();
  let thumbing = false;
  const pumpThumbs = async () => {
    if (thumbing || destroyed) return;
    thumbing = true;
    try {
      while (thumbQueue.size && !destroyed) {
        const p = thumbQueue.values().next().value;
        thumbQueue.delete(p);
        if (p.thumbDone) continue;
        p.thumbDone = true;
        try {
          const page = await getPage(p);
          if (destroyed) return;
          const w = page.getViewport({ scale: 1 }).width;
          const viewport = page.getViewport({ scale: (THUMB_WIDTH / w) * Math.min(window.devicePixelRatio || 1, 2) });
          const canvas = h("canvas", { class: "fv-pdf-thumb-canvas", "aria-hidden": "true" });
          canvas.width = Math.floor(viewport.width);
          canvas.height = Math.floor(viewport.height);
          await page.render({ canvas, viewport }).promise;
          if (!destroyed) p.thumbBox.replaceChildren(canvas);
        } catch (err) {
          if (!destroyed && !(err && err.name === "RenderingCancelledException")) console.warn("[markspace] pdf thumbnail failed", err);
        }
      }
    } finally {
      thumbing = false;
    }
  };
  /** Watches the thumbnails inside a scroll root: the pane or the Dialog body. */
  const observeThumbs = (scrollRoot) => {
    if (thumbObserver) thumbObserver.disconnect();
    thumbObserver = new IntersectionObserver((entries) => {
      for (const entry of entries) {
        const p = byThumb.get(entry.target);
        if (p && entry.isIntersecting && !p.thumbDone) {
          thumbQueue.add(p);
          pumpThumbs();
        }
      }
    }, { root: scrollRoot, rootMargin: "200px 0px" });
    for (const p of pages) thumbObserver.observe(p.thumbButton);
  };

  /* Layout, zoom and navigation. */
  const pad = () => (isNarrow() ? 8 : 16);
  const layout = () => {
    pagesEl.style.setProperty("--fv-pdf-pad", pad() + "px");
    for (const p of pages) sizePage(p);
  };
  const updateCurrent = () => {
    scrollFrame = 0;
    const y = scroller.scrollTop + scroller.clientHeight * 0.35;
    const n = pageAt(pages.map((p) => ({ top: p.el.offsetTop, height: p.el.offsetHeight })), y) || 1;
    setCurrent(n);
  };
  const revealThumb = (n) => {
    const b = pages[n - 1] && pages[n - 1].thumbButton;
    const scrollRoot = dialog ? dialog.body : thumbs;
    if (!b || !scrollRoot || !scrollRoot.clientHeight || (!dialog && thumbs.hidden)) return;
    const box = scrollRoot.getBoundingClientRect();
    const r = b.getBoundingClientRect();
    if (r.top < box.top || r.bottom > box.bottom) scrollRoot.scrollTop += r.top - box.top - scrollRoot.clientHeight / 2 + r.height / 2;
  };
  const setCurrent = (n) => {
    const changed = n !== current || root.dataset.currentPage !== String(n);
    current = n;
    root.dataset.currentPage = String(n);
    if (document.activeElement !== pageInput) pageInput.value = String(n);
    setInactive(prevButton, n <= 1);
    setInactive(nextButton, n >= count);
    if (!changed) return;
    for (const p of pages) {
      if (p.num === n) p.thumbButton.setAttribute("aria-current", "page");
      else p.thumbButton.removeAttribute("aria-current");
    }
    revealThumb(n);
  };
  const goToPage = (n) => {
    const target = clamp(Math.round(Number(n) || 1), 1, count);
    scroller.scrollTop = pages[target - 1].el.offsetTop - pad();
    setCurrent(target);
  };
  const setScale = (next, nextMode) => {
    const value = clamp(next, MIN_ZOOM, MAX_ZOOM);
    mode = nextMode;
    markSegment(presets, mode === "fit" ? "fit" : Math.abs(value - 1) < 0.001 ? "actual" : null);
    zoomLevel.textContent = Math.round(value * 100) + "%";
    setInactive(zoomOut, value <= MIN_ZOOM + 0.001);
    setInactive(zoomIn, value >= MAX_ZOOM - 0.001);
    if (Math.abs(value - scale) < 0.0005) return;
    const anchor = pages[current - 1];
    const frac = anchor ? (scroller.scrollTop - anchor.el.offsetTop) / Math.max(1, anchor.el.offsetHeight) : 0;
    scale = value;
    layout();
    if (anchor) scroller.scrollTop = anchor.el.offsetTop + frac * anchor.el.offsetHeight;
    clearTimeout(rerenderTimer);
    rerenderTimer = setTimeout(() => {
      for (const p of pages) if (p.near) queue(p);
    }, 150);
  };
  const fitWidth = () => {
    const width = scroller.clientWidth - pad() * 2;
    setScale(fitWidthScale(base.width, width), "fit");
  };
  /** The opening zoom: fit to width, but no larger than AUTO_MAX_ZOOM on wide screens. */
  const autoFit = () => {
    const width = scroller.clientWidth - pad() * 2;
    const fit = fitWidthScale(base.width, width);
    if (fit <= AUTO_MAX_ZOOM) setScale(fit, "fit");
    else setScale(AUTO_MAX_ZOOM, "auto");
  };
  const setThumbs = (open) => {
    thumbsOpen = open;
    thumbs.hidden = !open;
    syncThumbsButton();
    try {
      localStorage.setItem(THUMBS_KEY, open ? "1" : "0");
    } catch {
      /* Storage is unavailable. */
    }
    if (open) revealThumb(current);
  };
  /** Opens the thumbnails in a Dialog on narrow viewports. The list moves into the Dialog and back. */
  const openThumbsDialog = () => {
    const currentThumb = pages[current - 1] && pages[current - 1].thumbButton;
    dialog = openDialog({
      title: "Pages",
      subtitle: file.name || "",
      content: thumbList,
      width: "large",
      className: "fv-pdf-thumbs-dialog",
      initialFocus: currentThumb || null,
      returnFocus: thumbsButton,
      onClose: () => {
        dialog = null;
        if (destroyed) return;
        thumbs.appendChild(thumbList);
        observeThumbs(thumbs);
      },
    });
    observeThumbs(dialog.body);
    revealThumb(current);
  };
  const toggleThumbs = () => {
    if (isNarrow()) openThumbsDialog();
    else setThumbs(!thumbsOpen);
  };

  /* Find. */
  const openFind = () => {
    findUi.open();
    findButton.setAttribute("aria-expanded", "true");
  };
  const closeFind = (focusButton = false) => {
    findUi.close();
    findButton.setAttribute("aria-expanded", "false");
    find.token++;
    find.query = "";
    find.matches = [];
    find.current = -1;
    for (const p of pages) applyHighlights(p);
    if (focusButton) findButton.focus();
  };
  const extractText = async (token) => {
    for (let i = find.texts.length; i < count; i++) {
      if (destroyed || token !== find.token) return false;
      if (i % 5 === 0) findUi.setCount(`Searching page ${i + 1} of ${count}`, 0);
      const page = await getPage(pages[i]);
      const content = await page.getTextContent();
      const { text, starts } = pageText(content.items);
      find.texts[i] = text;
      find.starts[i] = starts;
    }
    return true;
  };
  const runFind = async (query) => {
    const token = ++find.token;
    find.query = query;
    if (!query) {
      find.matches = [];
      find.current = -1;
      findUi.setCount("", 0);
      for (const p of pages) applyHighlights(p);
      return;
    }
    if (!(await extractText(token))) return;
    if (token !== find.token) return;
    find.matches = findMatches(find.texts, query);
    const startAt = find.matches.findIndex((m) => m.page >= current);
    find.current = find.matches.length ? (startAt >= 0 ? startAt : 0) : -1;
    showMatch();
  };
  const stepMatch = (dir) => {
    if (!find.matches.length) return;
    find.current = (find.current + dir + find.matches.length) % find.matches.length;
    showMatch();
  };
  const showMatch = () => {
    const n = find.matches.length;
    findUi.setCount(n ? `${find.current + 1} of ${n}` : "No matches", n);
    for (const p of pages) applyHighlights(p);
    if (!n) return;
    const m = find.matches[find.current];
    const p = pages[m.page - 1];
    find.pendingScroll = true;
    if (p.textLayer && p.renderedScale) {
      scrollToMark(p);
    } else {
      goToPage(m.page);
    }
  };
  const scrollToMark = (p) => {
    const mark = p.el.querySelector("mark.fv-hit-current");
    if (!mark) return;
    find.pendingScroll = false;
    const box = scroller.getBoundingClientRect();
    const r = mark.getBoundingClientRect();
    // The match sits just above the line that decides the current page, so
    // the page number shows the page of the match.
    scroller.scrollTop += r.top - box.top - scroller.clientHeight * 0.3;
    const dx = r.left - box.left;
    if (dx < 0 || dx > scroller.clientWidth - r.width) scroller.scrollLeft += dx - scroller.clientWidth / 2;
  };
  /** Rewrites the text spans of a rendered page to mark the find matches. */
  const applyHighlights = (p) => {
    const layer = p.textLayer;
    if (!layer || !p.textEl) return;
    const divs = layer.textDivs || [];
    const strs = layer.textContentItemsStr || [];
    for (const i of p.marked) if (divs[i]) divs[i].textContent = strs[i] ?? "";
    p.marked = [];
    const starts = find.starts[p.num - 1];
    if (!find.matches.length || !starts) return;
    const onPage = [];
    find.matches.forEach((m, i) => {
      if (m.page === p.num) onPage.push({ ...m, current: i === find.current });
    });
    if (!onPage.length) return;
    for (const [i, ranges] of spanRanges(starts, strs, onPage)) {
      const div = divs[i];
      if (!div) continue;
      const str = strs[i] ?? "";
      ranges.sort((a, b) => a.start - b.start);
      const parts = [];
      let at = 0;
      for (const r of ranges) {
        if (r.start > at) parts.push(document.createTextNode(str.slice(at, r.start)));
        const from = Math.max(r.start, at);
        if (r.end > from) parts.push(h("mark", { class: "fv-hit" + (r.current ? " fv-hit-current" : ""), text: str.slice(from, r.end) }));
        at = Math.max(at, r.end);
      }
      if (at < str.length) parts.push(document.createTextNode(str.slice(at)));
      div.replaceChildren(...parts);
      p.marked.push(i);
    }
    if (find.pendingScroll && find.current >= 0 && find.matches[find.current].page === p.num) scrollToMark(p);
  };

  /* Events. */
  on(scroller, "scroll", () => {
    if (!scrollFrame) scrollFrame = requestAnimationFrame(updateCurrent);
  }, { passive: true });
  on(scroller, "wheel", (event) => {
    if (!event.ctrlKey) return;
    event.preventDefault();
    setScale(scale * Math.exp(-event.deltaY * 0.01), "custom");
  }, { passive: false });
  const commitPage = () => {
    const n = parsePageInput(pageInput.value, count);
    if (n === null) pageInput.value = String(current);
    else goToPage(n);
  };
  on(pageInput, "keydown", (event) => {
    if (event.key === "Enter") {
      event.preventDefault();
      commitPage();
      pageInput.select();
    }
  });
  on(pageInput, "change", commitPage);
  on(pageInput, "blur", () => {
    pageInput.value = String(current);
  });
  on(pageInput, "focus", () => pageInput.select());
  on(root, "keydown", (event) => {
    const tag = event.target && event.target.tagName;
    if (tag === "INPUT" || tag === "TEXTAREA" || event.ctrlKey || event.metaKey || event.altKey) return;
    if (event.target && event.target.closest && event.target.closest("[role=toolbar]")) return;
    const moves = { PageDown: () => goToPage(current + 1), PageUp: () => goToPage(current - 1), Home: () => goToPage(1), End: () => goToPage(count) };
    if (moves[event.key]) {
      event.preventDefault();
      moves[event.key]();
    }
  });

  pageObserver = new IntersectionObserver((entries) => {
    for (const entry of entries) {
      const p = byEl.get(entry.target);
      if (!p) continue;
      p.near = entry.isIntersecting;
      if (p.near && p.renderedScale !== scale) queue(p);
    }
  }, { root: scroller, rootMargin: "100% 0px" });

  root.classList.remove("is-loading");
  loading.remove();
  layout();
  autoFit();
  for (const p of pages) pageObserver.observe(p.el);
  observeThumbs(thumbs);
  setCurrent(1);
  resize = new ResizeObserver(() => {
    if (destroyed) return;
    if (mode === "fit") fitWidth();
    else if (mode === "auto") autoFit();
  });
  resize.observe(scroller);
  return handle;
}
