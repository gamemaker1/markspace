// Image viewer: fit to view, wheel and pinch zoom, drag pan and actual size.
// The toolbar holds the zoom ButtonGroup, the zoom level and a small
// SegmentedControl for Fit and 100%.

import { h, segmentedControl } from "../ui.js";
import { toolButton, toolbar, buttonGroup, markSegment, setInactive, errorState, loadingState, clamp } from "./common.js";

export const MIN_SCALE = 0.02;
export const MAX_SCALE = 16;
const STEP = 1.25;
const TAP_MS = 300;
const TAP_SLOP = 10;

/**
 * Returns the scale that fits an image into a box without enlarging it.
 * @param {{width: number, height: number}} natural
 * @param {{width: number, height: number}} box
 * @returns {number}
 */
export function fitScale(natural, box) {
  if (!natural || !box || !(natural.width > 0) || !(natural.height > 0) || !(box.width > 0) || !(box.height > 0)) return 1;
  return Math.min(1, box.width / natural.width, box.height / natural.height);
}

/**
 * Keeps an image in view. An image smaller than the box on an axis is
 * centered on that axis. A larger one cannot leave a gap at either edge.
 * @param {{scale: number, x: number, y: number}} state
 * @param {{width: number, height: number}} natural
 * @param {{width: number, height: number}} box
 * @returns {{scale: number, x: number, y: number}}
 */
export function clampPan(state, natural, box) {
  const w = natural.width * state.scale;
  const hgt = natural.height * state.scale;
  const x = w <= box.width ? (box.width - w) / 2 : clamp(state.x, box.width - w, 0);
  const y = hgt <= box.height ? (box.height - hgt) / 2 : clamp(state.y, box.height - hgt, 0);
  return { scale: state.scale, x, y };
}

/**
 * Zooms by factor around a point in box coordinates. The image point under
 * the given point stays under it. The scale stays within the limits.
 * @param {{scale: number, x: number, y: number}} state
 * @param {number} factor
 * @param {{x: number, y: number}} point
 * @param {{min?: number, max?: number}} [limits]
 * @returns {{scale: number, x: number, y: number}}
 */
export function zoomAt(state, factor, point, { min = MIN_SCALE, max = MAX_SCALE } = {}) {
  const scale = clamp(state.scale * factor, min, max);
  const k = scale / state.scale;
  return { scale, x: point.x - (point.x - state.x) * k, y: point.y - (point.y - state.y) * k };
}

/**
 * Names the zoom segment that matches a state: "fit", "actual" or null.
 * @param {"fit"|"custom"} mode
 * @param {number} scale
 * @returns {string|null}
 */
export function zoomSegment(mode, scale) {
  if (mode === "fit") return "fit";
  return Math.abs(scale - 1) < 0.001 ? "actual" : null;
}

/**
 * Mounts the image viewer.
 * @param {HTMLElement} container
 * @param {{file: object, url: string, downloadUrl: string, isNarrow: () => boolean, signal: AbortSignal}} ctx
 * @returns {Promise<{unmount(): void}>}
 */
export async function mountViewer(container, ctx) {
  const file = ctx.file || {};
  const ac = new AbortController();
  const on = (target, type, fn, opts = {}) => target.addEventListener(type, fn, { ...opts, signal: ac.signal });

  const img = h("img", { class: "fv-image-img", alt: file.name || "Image", draggable: false, decoding: "async" });
  const stage = h("div", { class: "fv-image-stage", tabindex: "0", role: "img", "aria-label": (file.name || "Image") + ". Use plus and minus to zoom, 0 to fit, 1 for actual size." }, img);
  const level = h("span", { class: "fv-zoom-level", "aria-live": "off" });
  const zoomOut = toolButton({ label: "Zoom out", icon: "dash", variant: "default", onClick: () => zoomBy(1 / STEP) });
  const zoomIn = toolButton({ label: "Zoom in", icon: "plus", variant: "default", onClick: () => zoomBy(STEP) });
  const segments = segmentedControl({
    label: "Zoom preset",
    size: "small",
    items: [
      { label: "Fit", value: "fit", selected: true },
      { label: "100%", value: "actual" },
    ],
    onChange: (value) => (value === "fit" ? setFit() : setActual()),
  });
  segments.classList.add("fv-zoom-presets");
  const bar = toolbar("Image", [buttonGroup("Zoom", [zoomOut, zoomIn]), level, segments], { className: "fv-image-toolbar" });
  const loading = loadingState("Loading image");
  const root = h("div", { class: "fv-image is-loading" }, bar, stage, loading);
  container.appendChild(root);

  let natural = { width: 1, height: 1 };
  let state = { scale: 1, x: 0, y: 0 };
  let mode = "fit";
  let loaded = false;

  const box = () => ({ width: stage.clientWidth, height: stage.clientHeight });
  const apply = () => {
    state = clampPan(state, natural, box());
    img.style.transform = `translate(${state.x}px, ${state.y}px) scale(${state.scale})`;
    level.textContent = Math.round(state.scale * 100) + "%";
    root.dataset.scale = String(Math.round(state.scale * 1000) / 1000);
    root.dataset.mode = mode;
    markSegment(segments, zoomSegment(mode, state.scale));
    setInactive(zoomOut, state.scale <= MIN_SCALE + 1e-6);
    setInactive(zoomIn, state.scale >= MAX_SCALE - 1e-6);
  };
  const center = () => ({ x: stage.clientWidth / 2, y: stage.clientHeight / 2 });
  const setFit = () => {
    mode = "fit";
    state = { scale: fitScale(natural, box()), x: 0, y: 0 };
    apply();
  };
  const setActual = (point = center()) => {
    mode = "custom";
    state = zoomAt(state, 1 / state.scale, point);
    apply();
  };
  const zoomBy = (factor, point = center()) => {
    mode = "custom";
    state = zoomAt(state, factor, point);
    apply();
  };
  /** Double click or double tap: from fit to actual size, otherwise back to fit. */
  const toggle = (point) => {
    const fit = fitScale(natural, box());
    if (mode === "fit" || Math.abs(state.scale - fit) < 0.001) {
      if (Math.abs(fit - 1) < 0.001) zoomBy(2, point);
      else setActual(point);
    } else {
      setFit();
    }
  };
  const local = (event) => {
    const r = stage.getBoundingClientRect();
    return { x: event.clientX - r.left, y: event.clientY - r.top };
  };

  on(stage, "wheel", (event) => {
    if (!loaded) return;
    event.preventDefault();
    const unit = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? stage.clientHeight : 1;
    const speed = event.ctrlKey ? 0.01 : 0.0015;
    zoomBy(Math.exp(-event.deltaY * unit * speed), local(event));
  }, { passive: false });

  const pointers = new Map();
  let drag = null;
  let pinch = null;
  let lastTap = null;
  let moved = 0;
  const pinchInfo = () => {
    const [a, b] = [...pointers.values()];
    return { dist: Math.hypot(a.x - b.x, a.y - b.y) || 1, mid: { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 } };
  };
  on(stage, "pointerdown", (event) => {
    if (!loaded || (event.pointerType === "mouse" && event.button !== 0)) return;
    stage.setPointerCapture(event.pointerId);
    pointers.set(event.pointerId, local(event));
    if (pointers.size === 1) {
      moved = 0;
      drag = { start: local(event), x: state.x, y: state.y };
      root.classList.add("is-dragging");
    } else if (pointers.size === 2) {
      drag = null;
      pinch = { ...pinchInfo(), state: { ...state } };
    }
  });
  on(stage, "pointermove", (event) => {
    if (!pointers.has(event.pointerId)) return;
    const p = local(event);
    pointers.set(event.pointerId, p);
    if (pinch && pointers.size >= 2) {
      const info = pinchInfo();
      const next = zoomAt(pinch.state, info.dist / pinch.dist, pinch.mid);
      mode = "custom";
      state = { scale: next.scale, x: next.x + info.mid.x - pinch.mid.x, y: next.y + info.mid.y - pinch.mid.y };
      moved = TAP_SLOP + 1;
      apply();
    } else if (drag) {
      const dx = p.x - drag.start.x;
      const dy = p.y - drag.start.y;
      moved = Math.max(moved, Math.hypot(dx, dy));
      if (moved > 2) {
        state = { scale: state.scale, x: drag.x + dx, y: drag.y + dy };
        apply();
      }
    }
  });
  const release = (event) => {
    if (!pointers.has(event.pointerId)) return;
    const p = local(event);
    pointers.delete(event.pointerId);
    if (pointers.size < 2) pinch = null;
    if (pointers.size === 1) {
      const [rest] = [...pointers.values()];
      drag = { start: rest, x: state.x, y: state.y };
    }
    if (pointers.size === 0) {
      drag = null;
      root.classList.remove("is-dragging");
      if (event.type === "pointerup" && event.pointerType !== "mouse" && moved <= TAP_SLOP) {
        const now = event.timeStamp;
        if (lastTap && now - lastTap.time < TAP_MS && Math.hypot(p.x - lastTap.x, p.y - lastTap.y) < 30) {
          lastTap = null;
          toggle(p);
        } else {
          lastTap = { time: now, x: p.x, y: p.y };
        }
      }
    }
  };
  on(stage, "pointerup", release);
  on(stage, "pointercancel", release);
  on(stage, "dblclick", (event) => {
    if (loaded) toggle(local(event));
  });
  on(stage, "keydown", (event) => {
    if (!loaded || event.ctrlKey || event.metaKey || event.altKey) return;
    const pan = 48;
    const panBy = (dx, dy) => {
      state.x += dx;
      state.y += dy;
      mode = "custom";
      apply();
    };
    const keys = {
      "+": () => zoomBy(STEP),
      "=": () => zoomBy(STEP),
      "-": () => zoomBy(1 / STEP),
      0: () => setFit(),
      1: () => setActual(),
      ArrowLeft: () => panBy(pan, 0),
      ArrowRight: () => panBy(-pan, 0),
      ArrowUp: () => panBy(0, pan),
      ArrowDown: () => panBy(0, -pan),
    };
    const fn = keys[event.key];
    if (fn) {
      event.preventDefault();
      fn();
    }
  });

  const resize = typeof ResizeObserver === "function"
    ? new ResizeObserver(() => {
        if (!loaded) return;
        if (mode === "fit") setFit();
        else apply();
      })
    : null;
  if (resize) resize.observe(stage);

  await new Promise((resolve) => {
    on(img, "load", async () => {
      // Decoding first keeps the first painted frame from showing an empty box.
      if (typeof img.decode === "function") await img.decode().catch(() => {});
      if (ac.signal.aborted) return resolve();
      natural = {
        width: img.naturalWidth || Number(file.width) || 800,
        height: img.naturalHeight || Number(file.height) || 600,
      };
      img.style.width = natural.width + "px";
      img.style.height = natural.height + "px";
      loaded = true;
      loading.remove();
      root.classList.remove("is-loading");
      setFit();
      resolve();
    }, { once: true });
    on(img, "error", () => {
      root.replaceChildren(errorState(ctx, "The image could not be loaded."));
      resolve();
    }, { once: true });
    if (ctx.signal) ctx.signal.addEventListener("abort", resolve, { once: true });
    img.src = ctx.url;
  });

  return {
    unmount() {
      ac.abort();
      if (resize) resize.disconnect();
      img.removeAttribute("src");
      root.remove();
    },
  };
}
