// In-page layout probe. The runner evaluates this file in the page, which
// defines window.__audit. Every finding carries a check, a rule, a
// selector, the element's classes for owner lookup, and measured values.
// Check numbers follow test/browser/readme.md.
(() => {
  if (window.__audit) return;

  const px = (v) => parseFloat(v) || 0;
  const round = (v) => Math.round(v * 10) / 10;
  const R = (r) => ({ x: round(r.left), y: round(r.top), w: round(r.width), h: round(r.height) });
  const INTERACTIVE = [
    "a[href]", "button", "input:not([type=hidden])", "select", "textarea", "summary",
    "[role=button]", "[role=link]", "[role=menuitem]", "[role=menuitemradio]", "[role=menuitemcheckbox]",
    "[role=tab]", "[role=option]", "[role=switch]", "[role=checkbox]", "[role=radio]", "[role=treeitem]",
    "[tabindex]:not([tabindex='-1'])",
  ].join(",");
  const OVERLAY = "dialog[open], [popover], .anchored-overlay, .quickopen, [role=dialog], [role=alertdialog], [role=menu], [role=listbox], .cm-tooltip, .tooltip";
  const els = [];
  const elId = (el) => {
    let i = els.indexOf(el);
    if (i < 0) {
      els.push(el);
      i = els.length - 1;
    }
    return i;
  };

  /* ---------- Visibility ---------- */

  const csCache = new Map();
  const style = (el, pseudo) => {
    if (pseudo) return getComputedStyle(el, pseudo);
    let cs = csCache.get(el);
    if (!cs) {
      cs = getComputedStyle(el);
      csCache.set(el, cs);
    }
    return cs;
  };

  function srOnly(el) {
    for (let n = el, i = 0; n && n.nodeType === 1 && i < 5; n = n.parentElement, i++) {
      const cs = style(n);
      if (cs.clipPath === "inset(50%)" || cs.clip === "rect(0px, 0px, 0px, 0px)" || cs.clip === "rect(1px, 1px, 1px, 1px)") return true;
      if ((cs.position === "absolute" || cs.position === "fixed") && cs.overflow !== "visible") {
        const r = n.getBoundingClientRect();
        if (r.width <= 1.5 || r.height <= 1.5) return true;
      }
    }
    return false;
  }

  function visible(el) {
    if (!el || !el.isConnected || el.nodeType !== 1) return false;
    if (el.closest("[inert]")) return false;
    const r = el.getBoundingClientRect();
    if (r.width < 1 || r.height < 1) return false;
    if (el.checkVisibility && !el.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true })) return false;
    return !srOnly(el);
  }

  function inPopoverOpen(el) {
    try {
      return el.matches(":popover-open");
    } catch {
      return false;
    }
  }

  function topLayer(el) {
    return (el.matches("dialog") && el.open && el.matches(":modal")) || inPopoverOpen(el);
  }

  /** The roots that receive input: the top modal dialog with open popovers, or the document. */
  function activeRoots() {
    // A menu or popover that fills the viewport, such as a full-screen ActionMenu on a phone, takes the input like a modal.
    const vw = document.documentElement.clientWidth;
    const vh = document.documentElement.clientHeight;
    const sheets = [...document.querySelectorAll("[popover], .overlay, .anchored-overlay, [role=menu]")].filter((p) => {
      if (!visible(p) || (p.matches("[popover]") && !inPopoverOpen(p))) return false;
      if (!["fixed", "absolute"].includes(getComputedStyle(p).position) && !inPopoverOpen(p)) return false;
      const r = p.getBoundingClientRect();
      const w = Math.max(0, Math.min(r.right, vw) - Math.max(r.left, 0));
      const h = Math.max(0, Math.min(r.bottom, vh) - Math.max(r.top, 0));
      return w * h >= 0.9 * vw * vh;
    });
    const outerSheets = sheets.filter((p) => !sheets.some((o) => o !== p && o.contains(p)));
    if (outerSheets.length) return { roots: [outerSheets[outerSheets.length - 1]], modal: outerSheets[outerSheets.length - 1] };
    const modals = [...document.querySelectorAll("dialog[open]")].filter((d) => d.matches(":modal") && visible(d));
    const pops = [...document.querySelectorAll("[popover]")].filter((p) => inPopoverOpen(p) && visible(p));
    if (!modals.length) return { roots: [document.body], modal: null };
    let top = modals[modals.length - 1];
    for (const d of modals) {
      const r = d.getBoundingClientRect();
      const hit = document.elementFromPoint(r.left + r.width / 2, r.top + Math.min(r.height / 2, 20));
      if (hit && d.contains(hit)) top = d;
    }
    return { roots: [top, ...pops.filter((p) => !top.contains(p))], modal: top };
  }

  function inRoots(el, roots) {
    return roots.some((r) => r.contains(el));
  }

  /* ---------- Names ---------- */

  const NOISY = /^(is-|has-|js$|ͼ|cm-line$)|\d{4,}/;
  function selectorOf(el) {
    const parts = [];
    let n = el;
    for (let depth = 0; n && n.nodeType === 1 && n !== document.documentElement && depth < 4; n = n.parentElement, depth++) {
      let p = n.localName;
      if (n.id && !/\d|[0-9a-z]{16,}/i.test(n.id)) {
        parts.unshift(p + "#" + CSS.escape(n.id));
        break;
      }
      const cls = [...n.classList].filter((c) => !NOISY.test(c)).slice(0, 2);
      if (cls.length) p += "." + cls.map((c) => CSS.escape(c)).join(".");
      const role = n.getAttribute("role");
      if (role && !cls.length) p += `[role="${role}"]`;
      if (!cls.length && !role && n.getAttribute("aria-label") && depth === 0) p += `[aria-label]`;
      parts.unshift(p);
      if (depth >= 2 && cls.length) break;
    }
    return parts.join(" > ");
  }

  function classesOf(el) {
    const out = [];
    for (let n = el, i = 0; n && n.nodeType === 1 && i < 5; n = n.parentElement, i++) out.push([...n.classList].filter((c) => !NOISY.test(c)));
    return out;
  }

  function labelOf(el) {
    return (el.getAttribute("aria-label") || el.innerText || el.getAttribute("title") || el.getAttribute("placeholder") || el.value || el.localName || "").trim().replace(/\s+/g, " ").slice(0, 60);
  }

  /* ---------- Findings ---------- */

  let findings = [];
  let allow = [];
  let state = {};
  function report(check, rule, el, measured, extra = {}) {
    const f = { check, rule, selector: el ? selectorOf(el) : "document", text: el ? labelOf(el) : "", classes: el ? classesOf(el) : [], measured, elId: el ? elId(el) : -1, ...extra };
    if (el) {
      const r = el.getBoundingClientRect();
      f.rect = R(r);
      for (const a of allow) {
        if (a.check !== check && a.check !== "*") continue;
        if (a.rule && a.rule !== rule) continue;
        try {
          if (el.matches(a.selector)) {
            f.allowed = a.reason;
            break;
          }
        } catch {
          /* bad selector in allow.json */
        }
      }
    }
    findings.push(f);
    return f;
  }

  /* ---------- Text helpers ---------- */

  const COUNTER = ".counter-label, .counter, .Counter, [data-counter]";
  function visibleText(el, { skipCounters = false } = {}) {
    let out = "";
    const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
    for (let t = walker.nextNode(); t; t = walker.nextNode()) {
      const p = t.parentElement;
      if (!p || !t.nodeValue.trim()) continue;
      if (skipCounters && p.closest(COUNTER)) continue;
      if (p.closest("[aria-hidden='true']")) continue;
      if (!p.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true }) || srOnly(p)) continue;
      out += " " + t.nodeValue;
    }
    return out.replace(/\s+/g, " ").trim();
  }
  const norm = (s) => String(s || "").replace(/\s+/g, " ").trim().toLowerCase();

  function firstTextNode(root, skip) {
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    for (let t = walker.nextNode(); t; t = walker.nextNode()) {
      const p = t.parentElement;
      if (!p || !t.nodeValue.trim()) continue;
      if (skip && skip(p)) continue;
      if (p.closest("[aria-hidden='true']") && !p.closest("svg")) continue;
      if (!p.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true }) || srOnly(p)) continue;
      return t;
    }
    return null;
  }

  function textRects(node) {
    const range = document.createRange();
    range.selectNodeContents(node);
    return [...range.getClientRects()].filter((r) => r.width > 0 && r.height > 0);
  }

  function firstCharRect(node) {
    const i = node.nodeValue.search(/\S/);
    const range = document.createRange();
    range.setStart(node, i);
    range.setEnd(node, i + 1);
    return range.getBoundingClientRect();
  }

  /** Line boxes of the text inside el: [{top, bottom, mid}], using line-height for the box. */
  function lineBoxes(el) {
    const lines = [];
    const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
    for (let t = walker.nextNode(); t; t = walker.nextNode()) {
      const p = t.parentElement;
      if (!p || !t.nodeValue.trim() || !p.checkVisibility({ checkVisibilityCSS: true }) || srOnly(p)) continue;
      for (const r of textRects(t)) {
        const mid = r.top + r.height / 2;
        const line = lines.find((l) => Math.abs(l.mid - mid) < Math.max(3, r.height / 3));
        if (line) {
          line.top = Math.min(line.top, r.top);
          line.bottom = Math.max(line.bottom, r.bottom);
        } else lines.push({ top: r.top, bottom: r.bottom, mid });
      }
    }
    lines.sort((a, b) => a.top - b.top);
    for (const l of lines) l.mid = (l.top + l.bottom) / 2;
    return lines;
  }

  /* ---------- Clipping chain ---------- */

  function fixedContainer(cs) {
    return cs.transform !== "none" || cs.perspective !== "none" || cs.filter !== "none" || cs.backdropFilter !== "none" || /paint|layout|strict|content/.test(cs.contain) || /transform|filter|perspective/.test(cs.willChange);
  }

  /** Ancestors that clip el, inner to outer: {node, kind: scroll|hidden|clip, x, y}. */
  function clipChain(el) {
    const out = [];
    if (topLayer(el)) return out;
    let mode = style(el).position;
    for (let n = el.parentElement; n && n !== document.documentElement; n = n.parentElement) {
      const cs = style(n);
      if (mode === "fixed") {
        if (!fixedContainer(cs)) {
          if (topLayer(n)) break;
          continue;
        }
      } else if (mode === "absolute") {
        if (cs.position === "static" && !fixedContainer(cs)) {
          if (topLayer(n)) break;
          continue;
        }
      }
      const ox = cs.overflowX;
      const oy = cs.overflowY;
      if (n !== document.body && (ox !== "visible" || oy !== "visible")) {
        const kindOf = (o) => (o === "auto" || o === "scroll" ? "scroll" : o === "hidden" ? "hidden" : o === "clip" ? "clip" : "visible");
        out.push({ node: n, x: kindOf(ox), y: kindOf(oy) });
      }
      if (topLayer(n)) break;
      mode = cs.position === "fixed" ? "fixed" : cs.position === "absolute" ? "absolute" : "flow";
    }
    return out;
  }

  function paddingBox(n) {
    const r = n.getBoundingClientRect();
    const left = r.left + n.clientLeft;
    const top = r.top + n.clientTop;
    return { left, top, right: left + (n.clientWidth || r.width), bottom: top + (n.clientHeight || r.height) };
  }

  function viewportBox() {
    const de = document.documentElement;
    return { left: 0, top: 0, right: de.clientWidth || innerWidth, bottom: de.clientHeight || innerHeight };
  }

  /** How far the rect sticks out of each clip (px), with the worst clip. */
  function overflowOf(rect, chain) {
    const boxes = chain.map((c) => {
      const b = paddingBox(c.node);
      return { c, left: c.x === "visible" ? -1e9 : b.left, right: c.x === "visible" ? 1e9 : b.right, top: c.y === "visible" ? -1e9 : b.top, bottom: c.y === "visible" ? 1e9 : b.bottom };
    });
    boxes.push({ c: { node: null, x: "scroll", y: "scroll", viewport: true }, ...viewportBox() });
    let clip = { left: -1e9, top: -1e9, right: 1e9, bottom: 1e9 };
    for (const b of boxes) clip = { left: Math.max(clip.left, b.left), top: Math.max(clip.top, b.top), right: Math.min(clip.right, b.right), bottom: Math.min(clip.bottom, b.bottom) };
    const cw = clip.right - clip.left;
    const ch = clip.bottom - clip.top;
    const visW = Math.max(0, Math.min(rect.right, clip.right) - Math.max(rect.left, clip.left));
    const visH = Math.max(0, Math.min(rect.bottom, clip.bottom) - Math.max(rect.top, clip.top));
    // An element larger than the combined clip only needs to fill it. An empty clip hides everything.
    const miss = cw <= 0 || ch <= 0 ? Math.max(rect.width, rect.height) : Math.max(Math.min(rect.width, cw) - visW, Math.min(rect.height, ch) - visH);
    if (miss <= 1) return { worst: null, clip, visW, visH };
    let worst = null;
    for (const b of boxes) {
      const cut = Math.max(rect.bottom - b.bottom, b.top - rect.top, rect.right - b.right, b.left - rect.left);
      if (!worst || cut > worst.cut) worst = { cut, box: b };
    }
    return { worst: { miss, box: worst.box }, clip, visW, visH };
  }

  function saveScroll(chain) {
    const nodes = chain.map((c) => c.node);
    return { nodes: nodes.map((n) => [n, n.scrollLeft, n.scrollTop]), x: scrollX, y: scrollY };
  }
  function restoreScroll(saved) {
    for (const [n, l, t] of saved.nodes) {
      n.scrollLeft = l;
      n.scrollTop = t;
    }
    window.scrollTo({ left: saved.x, top: saved.y, behavior: "instant" });
  }

  /* ---------- Element set ---------- */

  function interactive(roots, { cap = 1500 } = {}) {
    const seen = new Set();
    const list = [];
    for (const root of roots) {
      for (const el of root.querySelectorAll(INTERACTIVE)) {
        if (seen.has(el)) continue;
        seen.add(el);
        if (el.closest("svg") && el.localName !== "svg") continue;
        if (!visible(el)) continue;
        list.push(el);
      }
    }
    const prose = (el) => !!el.closest(".md-body, .markdown-body, .cm-content, .note-body, .answer-body");
    list.sort((a, b) => prose(a) - prose(b));
    return { list: list.slice(0, cap), total: list.length };
  }

  /* ---------- Check 1: duplicate titles ---------- */

  const HEADINGS = "h1, h2, h3, h4, h5, h6, [role=heading], legend, label, caption, figcaption, .action-list-heading";

  function checkDuplicates(roots) {
    const hits = new Map();
    const add = (el, source, title) => {
      if (!hits.has(el)) hits.set(el, []);
      hits.get(el).push({ source, title });
    };
    const scan = (surface, title, titleEl, source, exclude = () => false) => {
      const want = norm(title);
      if (!want) return;
      for (const h of surface.querySelectorAll(HEADINGS)) {
        if (h === titleEl || (titleEl && (h.contains(titleEl) || titleEl.contains(h)))) continue;
        // Headings inside rendered or edited markdown are the person's own text, not interface titles.
        if (h.closest("[role=tablist], nav[aria-label='Breadcrumbs'], .breadcrumbs, .md-body, .markdown-body, .cm-content, .answer-body") || exclude(h)) continue;
        if (!visible(h)) continue;
        if (norm(visibleText(h, { skipCounters: true })) === want) add(h, source, title);
      }
    };
    for (const root of roots) {
      for (const tab of root.querySelectorAll("[role=tab][aria-selected='true']")) {
        if (!visible(tab)) continue;
        const title = tab.dataset.label || visibleText(tab, { skipCounters: true });
        const id = tab.getAttribute("aria-controls");
        let panel = id ? document.getElementById(id) : null;
        if (!panel) {
          const list = tab.closest("[role=tablist]");
          panel = list && list.parentElement ? [...list.parentElement.querySelectorAll("[role=tabpanel]")].find(visible) : null;
        }
        if (panel && visible(panel)) scan(panel, title, tab, "selected tab");
        // A dialog that holds the tabs must not take the selected tab's label as its title.
        const holder = tab.closest("dialog[open], [role=dialog]");
        if (holder) {
          const hid = (holder.getAttribute("aria-labelledby") || "").split(/\s+/).filter(Boolean)[0];
          const dt = (hid && document.getElementById(hid)) || holder.querySelector(".dialog-title");
          if (dt && visible(dt) && !dt.closest("[role=tablist]") && norm(visibleText(dt, { skipCounters: true })) === norm(title)) {
            if (!hits.has(dt)) hits.set(dt, []);
            hits.get(dt).push({ source: "selected tab", title });
          }
        }
      }
      const dialogs = [...root.querySelectorAll("dialog[open], [role=dialog], [role=alertdialog]")];
      if (root.matches && root.matches("dialog[open], [role=dialog]")) dialogs.unshift(root);
      for (const d of dialogs) {
        if (!visible(d)) continue;
        const ids = (d.getAttribute("aria-labelledby") || "").split(/\s+/).filter(Boolean);
        const titleEl = (ids.length && document.getElementById(ids[0])) || d.querySelector(".dialog-title");
        if (!titleEl || !visible(titleEl)) continue;
        const header = titleEl.closest(".dialog-header");
        scan(d, visibleText(titleEl, { skipCounters: true }), titleEl, "dialog title", (h) => !!(header && header.contains(h)));
      }
    }
    if (roots[0] === document.body) {
      const main = document.querySelector("main, #main, [role=main]");
      const h1 = main && [...main.querySelectorAll(".page-header h1, h1")].find(visible);
      if (h1) scan(main, visibleText(h1, { skipCounters: true }), h1, "page header title", (h) => !!h.closest("dialog, [role=dialog], [role=tabpanel], aside"));
    }
    for (const [el, list] of hits) report(1, "duplicate-title", el, { text: visibleText(el), repeats: list.map((x) => `${x.source} "${x.title}"`) });
  }

  /* ---------- Check 2: reachability ---------- */

  function hitOk(el, hit) {
    if (!hit) return false;
    if (hit === el || el.contains(hit) || hit.contains(el)) return true;
    const lab = hit.closest("label");
    if (lab && (lab.control === el || lab.contains(el))) return true;
    if (el.labels && [...el.labels].some((l) => l.contains(hit))) return true;
    return false;
  }

  function coverOf(hit, el) {
    for (let n = hit; n && n.nodeType === 1; n = n.parentElement) {
      if (n.contains(el)) return null;
      if (n.matches(OVERLAY) || topLayer(n)) return { overlay: n };
      const pos = style(n).position;
      if (pos === "sticky" || pos === "fixed") return { sticky: n };
    }
    return { other: hit };
  }

  function checkReachability(list) {
    const stats = { scrolled: 0 };
    for (const el of list) {
      if (style(el).pointerEvents === "none" && !el.matches("input, select, textarea")) continue;
      const chain = clipChain(el);
      let rect = el.getBoundingClientRect();
      let o = overflowOf(rect, chain);
      if (o.worst) {
        stats.scrolled++;
        const saved = saveScroll(chain);
        const fixedAxes = chain.filter((c) => c.x !== "scroll" || c.y !== "scroll").map((c) => [c, c.node.scrollLeft, c.node.scrollTop]);
        el.scrollIntoView({ block: "nearest", inline: "nearest", behavior: "instant" });
        // A container with overflow hidden or clip cannot be scrolled by a person. Undo what scrollIntoView did there.
        const forced = [];
        for (const [c, l, t] of fixedAxes) {
          const dx = c.x !== "scroll" ? c.node.scrollLeft - l : 0;
          const dy = c.y !== "scroll" ? c.node.scrollTop - t : 0;
          if (Math.abs(dx) > 1 || Math.abs(dy) > 1) forced.push({ node: selectorOf(c.node), overflow: `${c.x}/${c.y}`, scrollDelta: [Math.round(dx), Math.round(dy)] });
          if (c.x !== "scroll") c.node.scrollLeft = l;
          if (c.y !== "scroll") c.node.scrollTop = t;
        }
        rect = el.getBoundingClientRect();
        o = overflowOf(rect, chain);
        if (o.worst) {
          // Scroll every scrollable ancestor to its end, then the page, and measure again.
          for (const c of chain) {
            if (c.y === "scroll" && rect.bottom > paddingBox(c.node).bottom) c.node.scrollTop = c.node.scrollHeight;
            if (c.x === "scroll" && rect.right > paddingBox(c.node).right) c.node.scrollLeft = c.node.scrollWidth;
          }
          window.scrollTo({ left: scrollX, top: document.documentElement.scrollHeight, behavior: "instant" });
          const r2 = el.getBoundingClientRect();
          const o2 = overflowOf(r2, chain);
          if (!o2.worst || o2.worst.miss < o.worst.miss) {
            rect = r2;
            o = o2;
          }
        }
        if (o.worst) {
          const b = o.worst.box;
          const limit = b.c.viewport ? "viewport" : selectorOf(b.c.node);
          const measured = {
            element: R(rect),
            limit,
            limitBox: { top: round(b.top), bottom: round(b.bottom), left: round(b.left), right: round(b.right) },
            hiddenPx: round(o.worst.miss),
            scrollers: chain.filter((c) => c.x === "scroll" || c.y === "scroll").map((c) => ({ node: selectorOf(c.node), scrollTop: Math.round(c.node.scrollTop), scrollHeight: c.node.scrollHeight, clientHeight: c.node.clientHeight })),
            page: { scrollY: Math.round(scrollY), scrollHeight: document.documentElement.scrollHeight, innerHeight },
          };
          if (forced.length) measured.forcedScroll = forced;
          const clipped = !b.c.viewport && (b.c.x === "hidden" || b.c.x === "clip" || b.c.y === "hidden" || b.c.y === "clip");
          report(2, clipped ? "clipped-by-overflow" : "cannot-scroll-into-view", el, measured);
          restoreScroll(saved);
          continue;
        }
        // Centre point after scrolling into view.
        coverCheck(el, rect, o.clip);
        restoreScroll(saved);
        continue;
      }
      coverCheck(el, rect, o.clip);
    }
    return stats;
  }

  /**
   * Returns the element that covers el inside the clip, or null. A small element is tested at its centre. An
   * element larger than 48px is covered only when its centre and 4 inner points are all covered.
   */
  function coveredAt(el, rect, clip) {
    const frags = el.getClientRects();
    const r = frags.length > 1 ? frags[0] : rect;
    const left = Math.max(r.left, clip.left);
    const right = Math.min(r.right, clip.right);
    const top = Math.max(r.top, clip.top);
    const bottom = Math.min(r.bottom, clip.bottom);
    if (right - left < 1 || bottom - top < 1) return null;
    const cx = (left + right) / 2;
    const cy = (top + bottom) / 2;
    const pts = [[cx, cy]];
    if (right - left > 48 || bottom - top > 48) {
      const qx = (right - left) / 4;
      const qy = (bottom - top) / 4;
      pts.push([left + qx, top + qy], [right - qx, top + qy], [left + qx, bottom - qy], [right - qx, bottom - qy]);
    }
    let found = null;
    for (const [x, y] of pts) {
      const hit = document.elementFromPoint(x, y);
      if (!hit || hitOk(el, hit)) return null;
      const cover = coverOf(hit, el);
      if (!cover || cover.overlay) return null;
      if (!found) found = { ...cover, point: [round(x), round(y)] };
    }
    return found;
  }

  function coverCheck(el, rect, clip) {
    if (deadClick(el, rect, clip)) return;
    const cover = coveredAt(el, rect, clip);
    if (!cover) return;
    const by = cover.sticky || cover.other;
    report(2, cover.sticky ? "covered-by-sticky" : "covered", el, { point: cover.point, coveredBy: selectorOf(by), coveredByPosition: style(by).position, coverRect: R(by.getBoundingClientRect()) });
  }

  /* ---------- Check 3: alignment ---------- */

  const ICON = "svg, img, .avatar, .octicon, [class*='-leading'] > *, .glyph";
  const ACTION = "button, a[href], input:not([type=hidden]), select, [role=button], summary";

  function checkAlignment(spec) {
    const done = new Set();
    for (const entry of spec.containers || []) {
      for (const c of document.querySelectorAll(entry.selector)) {
        if (done.has(c) || !visible(c)) continue;
        done.add(c);
        if (entry.mode === "toolbar") alignToolbar(c, entry);
        else alignRow(c, entry);
      }
    }
  }

  const CONTROL = "button, input:not([type=hidden]), select, [role=button], summary";

  function alignRow(c, entry) {
    // A control is a button-like element, or a link without visible text. A link with text, such as a row title, is text.
    const controlOf = (n) => {
      const a = n.closest(CONTROL + ", a[href]");
      if (!a || a === c || !c.contains(a)) return null;
      if (a.matches("a[href]") && !a.matches(CONTROL) && visibleText(a)) return null;
      return a;
    };
    const skipText = (p) => !!controlOf(p) || !!p.closest("svg, .counter-label, .label, kbd, .keybinding-hint, .tooltip");
    let textNode = null;
    let textEl = null;
    if (entry.text) {
      textEl = [...c.querySelectorAll(entry.text)].find(visible) || null;
      textNode = textEl ? firstTextNode(textEl) : null;
    } else textNode = firstTextNode(c, skipText);
    if (!textNode) return;
    const firstRect = firstCharRect(textNode);
    let leading = [...c.querySelectorAll(entry.leading || ICON)].find((n) => {
      if (!visible(n) || n.contains(textNode) || n.closest(".tooltip")) return false;
      const ctl = controlOf(n);
      if (ctl && ctl !== c) return false;
      // An icon inside another text link, such as a PageHeader parent link, belongs to that link.
      const link = n.closest("a[href]");
      if (link && link !== c && !link.contains(textNode)) return false;
      const r = n.getBoundingClientRect();
      return r.right <= firstRect.left + 1 && r.width <= 48 && r.height <= 48;
    });
    // The text block is every text line in the column of the first text: a title and its description.
    let lines;
    if (textEl) lines = lineBoxes(textEl);
    else {
      lines = [];
      const walker = document.createTreeWalker(c, NodeFilter.SHOW_TEXT);
      for (let t = walker.nextNode(); t; t = walker.nextNode()) {
        const p = t.parentElement;
        if (!p || !t.nodeValue.trim() || skipText(p) || !p.checkVisibility({ checkVisibilityCSS: true }) || srOnly(p)) continue;
        const fr = firstCharRect(t);
        if (Math.abs(fr.left - firstRect.left) > 4 || fr.top < firstRect.top - 2) continue;
        for (const r of textRects(t)) {
          const mid = r.top + r.height / 2;
          const line = lines.find((l) => Math.abs(l.mid - mid) < Math.max(3, r.height / 3));
          if (line) {
            line.top = Math.min(line.top, r.top);
            line.bottom = Math.max(line.bottom, r.bottom);
          } else lines.push({ top: r.top, bottom: r.bottom, mid });
        }
      }
      lines.sort((a, b) => a.top - b.top);
      for (const l of lines) l.mid = (l.top + l.bottom) / 2;
    }
    if (!lines.length) return;
    const blockRect = { top: lines[0].top, bottom: lines[lines.length - 1].bottom };
    const first = lines[0];
    // The icon must sit on the rows of the text block. An icon on another row belongs to other content.
    if (leading) {
      const lr = leading.getBoundingClientRect();
      if (lr.bottom < blockRect.top - 8 || lr.top > blockRect.bottom + 8) leading = null;
    }
    // A field above the first text, such as a read-only link input, is a line of its own.
    const fieldAbove = [...c.querySelectorAll("input:not([type=hidden]), select, textarea")].some((f) => visible(f) && f.getBoundingClientRect().bottom <= first.top + 2);
    const multi = lines.length > 1 || fieldAbove;
    const measured = { textMid: round(first.mid), lines: lines.length + (fieldAbove ? 1 : 0) };
    const problems = [];
    if (leading) {
      const lr = leading.getBoundingClientRect();
      const mid = lr.top + lr.height / 2;
      measured.iconMid = round(mid);
      // A leading visual taller than a text line, such as a 32px Avatar beside a name and an email, may centre on the whole text block instead.
      const blockMid = (blockRect.top + blockRect.bottom) / 2;
      const tall = lr.height > first.bottom - first.top + 6;
      if (Math.abs(mid - first.mid) > 2 && !(tall && multi && Math.abs(mid - blockMid) <= 2)) problems.push(`icon centre ${round(mid - first.mid)}px from the first text line`);
    }
    if (!multi) {
      const trailing = entry.trailing
        ? [...c.querySelectorAll(entry.trailing)].filter((a) => a !== c && visible(a) && !a.contains(textNode))
        : [...c.querySelectorAll(CONTROL + ", a[href]")].filter((a) => a !== c && visible(a) && controlOf(a) === a && !a.contains(textNode) && !(leading && a.contains(leading)));
      const trailingIcons = c.matches(ACTION) || entry.trailingVisuals ? [...c.querySelectorAll(".action-list-trailing, [class*='-trailing'] svg")].filter(visible) : [];
      measured.actionMids = [];
      for (const a of [...trailing, ...trailingIcons]) {
        const ar = a.getBoundingClientRect();
        if (ar.left < firstRect.left) continue;
        if (ar.top >= blockRect.bottom - 1 || ar.bottom <= blockRect.top + 1) continue;
        const mid = ar.top + ar.height / 2;
        measured.actionMids.push(round(mid));
        if (Math.abs(mid - first.mid) > 2) problems.push(`"${labelOf(a).slice(0, 24)}" centre ${round(mid - first.mid)}px from the text`);
      }
    }
    if (problems.length) report(3, multi ? "icon-first-line" : "single-line-centres", c, { ...measured, problems });
  }

  function alignToolbar(c, entry) {
    const items = [...c.querySelectorAll(entry.items || ACTION)].filter((a) => visible(a) && !a.closest("[role=menu], [popover]"));
    const rows = [];
    for (const a of items) {
      const r = a.getBoundingClientRect();
      const mid = r.top + r.height / 2;
      const row = rows.find((x) => r.top < x.bottom && r.bottom > x.top);
      if (row) {
        row.items.push([a, mid]);
        row.top = Math.min(row.top, r.top);
        row.bottom = Math.max(row.bottom, r.bottom);
      } else rows.push({ top: r.top, bottom: r.bottom, items: [[a, mid]] });
    }
    for (const row of rows) {
      if (row.items.length < 2) continue;
      const mids = row.items.map((x) => x[1]).sort((a, b) => a - b);
      const median = mids[Math.floor(mids.length / 2)];
      const off = row.items.filter(([, m]) => Math.abs(m - median) > 2);
      if (off.length) report(3, "toolbar-centres", c, { median: round(median), off: off.map(([a, m]) => `"${labelOf(a).slice(0, 24)}" ${round(m - median)}px`) });
    }
  }

  /* ---------- Check 4: spacing ---------- */

  function mediaOk(when) {
    if (!when) return true;
    const w = document.documentElement.clientWidth;
    if (when.minWidth !== undefined && w < when.minWidth) return false;
    if (when.maxWidth !== undefined && w > when.maxWidth) return false;
    if (when.pointer && !matchMedia(`(pointer: ${when.pointer})`).matches) return false;
    return true;
  }

  function checkSpacing(spec, roots) {
    const sides = ["top", "right", "bottom", "left"];
    for (const entry of spec.padding || []) {
      if (!mediaOk(entry.when)) continue;
      for (const el of document.querySelectorAll(entry.selector)) {
        if (!visible(el) || !inRoots(el, roots)) continue;
        if (entry.not && el.matches(entry.not)) continue;
        const cs = style(el);
        const got = [px(cs.paddingTop), px(cs.paddingRight), px(cs.paddingBottom), px(cs.paddingLeft)];
        const bad = [];
        entry.padding.forEach((want, i) => {
          if (want === null || want === undefined) return;
          if (Math.abs(got[i] - want) > 1) bad.push(`${sides[i]} ${got[i]}px, spec ${want}px`);
        });
        if (bad.length) report(4, "padding", el, { expected: entry.padding, actual: got, differences: bad, spec: entry.ref }, { specSelector: entry.selector });
      }
    }
    for (const entry of spec.insets || []) {
      for (const ov of document.querySelectorAll(entry.selector)) {
        if (!visible(ov) || !inRoots(ov, roots)) continue;
        if (entry.not && ov.matches(entry.not)) continue;
        const skip = (p) => !!p.closest("[aria-label='Close'], .dialog-close, .tooltip, .sr-only, kbd") || p.closest(entry.selector) !== ov;
        const t = firstTextNode(ov, skip);
        if (!t) continue;
        const ovr = ov.getBoundingClientRect();
        const cs = style(ov);
        const edgeLeft = ovr.left + px(cs.borderLeftWidth);
        const edgeTop = ovr.top + px(cs.borderTopWidth);
        const cr = firstCharRect(t);
        const p = t.parentElement;
        const lh = px(style(p).lineHeight) || cr.height;
        const lineTop = cr.top - Math.max(0, (lh - cr.height) / 2);
        const item = p.closest("[role^=menuitem], [role=option], .action-list-item, .menu-item, li");
        const hasLeading = !!(item && ov.contains(item) && [...item.querySelectorAll("svg, img, .action-list-leading, .ms-completion-icon")].some((n) => visible(n) && !n.contains(t) && n.getBoundingClientRect().right <= cr.left + 1));
        const narrow = document.documentElement.clientWidth < 768;
        const limitLeft = hasLeading ? entry.left.withLeading : entry.left.without;
        const limitTop = narrow && entry.topNarrow !== undefined ? entry.topNarrow : entry.top;
        let sum = 0;
        for (let n = p; n && n !== ov.parentElement; n = n.parentElement) {
          const s = style(n);
          sum += px(s.paddingLeft) + (n === ov ? 0 : px(s.borderLeftWidth) + Math.max(0, px(s.marginLeft)));
        }
        const left = cr.left - edgeLeft;
        const top = lineTop - edgeTop;
        const measured = { firstText: t.nodeValue.trim().slice(0, 30), textLeft: round(left), limitLeft, leadingVisual: hasLeading, paddingSumLeft: round(sum), lineTop: round(top), limitTop, spec: entry.ref };
        // Text that follows a field or a control is not the first content of the overlay on that side.
        const controls = [...ov.querySelectorAll("input:not([type=hidden]), select, textarea, button, [role=button], img, canvas")].filter((n) => visible(n) && !n.contains(t) && !n.matches("[aria-label='Close'], .dialog-close"));
        const controlAbove = controls.some((n) => n.getBoundingClientRect().bottom <= lineTop + 1);
        const controlLeft = controls.some((n) => {
          const nr = n.getBoundingClientRect();
          return nr.right <= cr.left + 1 && nr.bottom > lineTop && nr.top < lineTop + lh && !(item && item.contains(n));
        });
        if (!controlLeft && (left > limitLeft + 1 || sum > limitLeft + 1)) report(4, "overlay-text-inset-left", ov, measured, { specSelector: entry.selector });
        if (!controlAbove && limitTop !== undefined && top > limitTop + 1) report(4, "overlay-text-inset-top", ov, measured, { specSelector: entry.selector });
      }
    }
  }

  /* ---------- Check 5: overlap and clipped text ---------- */

  function checkOverlap(list) {
    const stickyOf = (el) => {
      for (let n = el; n && n.nodeType === 1; n = n.parentElement) {
        const p = style(n).position;
        if (p === "sticky" || p === "fixed" || topLayer(n)) return n;
      }
      return null;
    };
    const boxes = list
      .filter((el) => !el.closest(".md-body a, .cm-content") || el.matches(".cm-content button, .cm-content [role=button]"))
      .map((el) => {
        const o = overflowOf(el.getBoundingClientRect(), clipChain(el));
        const r = el.getBoundingClientRect();
        const c = o.clip;
        return { el, l: Math.max(r.left, c.left), t: Math.max(r.top, c.top), r: Math.min(r.right, c.right), b: Math.min(r.bottom, c.bottom), layer: stickyOf(el) };
      })
      .filter((b) => b.r - b.l > 1 && b.b - b.t > 1)
      .sort((a, b) => a.l - b.l);
    const seen = new Set();
    for (let i = 0; i < boxes.length; i++) {
      const a = boxes[i];
      for (let k = i + 1; k < boxes.length && boxes[k].l < a.r; k++) {
        const b = boxes[k];
        if (a.el.contains(b.el) || b.el.contains(a.el)) continue;
        if (a.layer !== b.layer) continue;
        const w = Math.min(a.r, b.r) - Math.max(a.l, b.l);
        const h = Math.min(a.b, b.b) - Math.max(a.t, b.t);
        if (w > 1 && h > 1) {
          const la = a.el.closest("label");
          if (la && la.contains(b.el)) continue;
          const key = selectorOf(a.el) + "|" + selectorOf(b.el);
          if (seen.has(key)) continue;
          seen.add(key);
          report(5, "interactive-overlap", a.el, { other: selectorOf(b.el), otherText: labelOf(b.el), overlap: [round(w), round(h)], a: R(a.el.getBoundingClientRect()), b: R(b.el.getBoundingClientRect()) });
        }
      }
    }
  }

  function fullTextAvailable(el) {
    const text = norm(el.textContent);
    for (let n = el, i = 0; n && n.nodeType === 1 && i < 4; n = n.parentElement, i++) {
      if (n.getAttribute("title") && norm(n.getAttribute("title")).includes(text)) return "title";
      if (n.getAttribute("aria-label") && norm(n.getAttribute("aria-label")).includes(text) && n.getAttribute("aria-label").length >= text.length) return "aria-label";
      const desc = (n.getAttribute("aria-describedby") || "") + " " + (n.getAttribute("aria-labelledby") || "");
      for (const id of desc.split(/\s+/).filter(Boolean)) {
        const t = document.getElementById(id);
        if (t && t.matches(".tooltip, [role=tooltip]") && norm(t.textContent).includes(text)) return "tooltip";
      }
    }
    return "";
  }

  function checkTextClipping(roots) {
    const reported = new Set();
    for (const root of roots) {
      for (const el of root.querySelectorAll("*")) {
        if (el.matches("input, textarea, select, option, svg, svg *, script, style, .cm-content, .cm-content *, pre, code, pre *, canvas, video, img")) continue;
        let hasText = false;
        for (const n of el.childNodes) if (n.nodeType === 3 && n.nodeValue.trim()) hasText = true;
        if (!hasText || !visible(el) || el.closest("[aria-hidden='true']")) continue;
        const cs = style(el);
        const clamp = cs.webkitLineClamp && cs.webkitLineClamp !== "none";
        const xHidden = cs.overflowX === "hidden" || cs.overflowX === "clip";
        const yHidden = cs.overflowY === "hidden" || cs.overflowY === "clip";
        if (xHidden && el.scrollWidth > el.clientWidth + 1) {
          if (cs.textOverflow === "ellipsis") {
            if (!fullTextAvailable(el)) report(5, "ellipsis-without-full-text", el, { scrollWidth: el.scrollWidth, clientWidth: el.clientWidth, text: el.textContent.trim().slice(0, 80) });
          } else report(5, "text-clipped", el, { axis: "x", scrollWidth: el.scrollWidth, clientWidth: el.clientWidth, textOverflow: cs.textOverflow, text: el.textContent.trim().slice(0, 80) });
          reported.add(el);
          continue;
        }
        if (clamp && el.scrollHeight > el.clientHeight + 1) {
          if (!fullTextAvailable(el)) report(5, "ellipsis-without-full-text", el, { lineClamp: cs.webkitLineClamp, scrollHeight: el.scrollHeight, clientHeight: el.clientHeight, text: el.textContent.trim().slice(0, 80) });
          reported.add(el);
          continue;
        }
        if (yHidden && !clamp && el.scrollHeight > el.clientHeight + 2) {
          report(5, "text-clipped", el, { axis: "y", scrollHeight: el.scrollHeight, clientHeight: el.clientHeight, text: el.textContent.trim().slice(0, 80) });
          reported.add(el);
          continue;
        }
        // Text that spills out of its box and is cut by a clipping ancestor.
        const full = clipChain(el);
        if (!full.some((c) => c.x === "hidden" || c.x === "clip" || c.y === "hidden" || c.y === "clip")) continue;
        for (const n of el.childNodes) {
          if (n.nodeType !== 3 || !n.nodeValue.trim()) continue;
          for (const tr of textRects(n)) {
            for (let i = 0; i < full.length; i++) {
              const c = full[i];
              if (reported.has(c.node)) continue;
              // Text beyond an inner scroll container can be scrolled to, so the outer clip does not hide it.
              const clipX = (c.x === "hidden" || c.x === "clip") && !full.slice(0, i).some((x) => x.x === "scroll");
              const clipY = (c.y === "hidden" || c.y === "clip") && !full.slice(0, i).some((x) => x.y === "scroll");
              if (!clipX && !clipY) continue;
              const b = paddingBox(c.node);
              const out = Math.max(clipX ? Math.max(tr.right - b.right, b.left - tr.left) : 0, clipY ? Math.max(tr.bottom - b.bottom, b.top - tr.top) : 0);
              if (out > 1.5) {
                const ccs = style(c.node);
                if (ccs.textOverflow === "ellipsis") continue;
                if (srOnly(c.node)) continue;
                reported.add(c.node);
                report(5, "text-clipped", el, { axis: "ancestor", clippedBy: selectorOf(c.node), outsidePx: round(out), text: n.nodeValue.trim().slice(0, 80) });
                break;
              }
            }
          }
        }
      }
    }
  }

  /** Reports word-broken when a title or control label wraps inside a word. */
  function checkBrokenWords(roots) {
    const done = new Set();
    for (const root of roots) {
      for (const el of root.querySelectorAll("h1, h2, h3, h4, .dialog-title, .page-header-title, button, a[href], label, th, [role=tab], [role=menuitem]")) {
        if (el.closest(USER_CONTENT) || done.has(el) || !visible(el)) continue;
        const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
        let found = null;
        for (let t = walker.nextNode(); t && !found; t = walker.nextNode()) {
          if (!t.nodeValue.trim() || srOnly(t.parentElement) || t.parentElement.closest("[aria-hidden='true']")) continue;
          const re = /\S{2,}/g;
          for (let m = re.exec(t.nodeValue); m && !found; m = re.exec(t.nodeValue)) {
            const range = document.createRange();
            range.setStart(t, m.index);
            range.setEnd(t, m.index + m[0].length);
            const rects = [...range.getClientRects()].filter((r) => r.width > 0);
            if (rects.length > 1 && Math.abs(rects[rects.length - 1].top - rects[0].top) > 2) found = { word: m[0].slice(0, 40), lines: new Set(rects.map((r) => Math.round(r.top))).size, width: round(el.getBoundingClientRect().width) };
          }
        }
        if (found) {
          done.add(el);
          report(5, "word-broken", el, found);
        }
      }
    }
  }

  /* ---------- Check 6: page level ---------- */

  function checkPage(roots, opts) {
    const de = document.documentElement;
    const over = de.scrollWidth - de.clientWidth;
    if (over > 1 && style(de).overflowX !== "hidden" && style(document.body).overflowX !== "hidden") {
      const culprits = [];
      for (const el of document.body.querySelectorAll("*")) {
        if (el.closest("svg") && el.localName !== "svg") continue;
        const r = el.getBoundingClientRect();
        if (r.right <= de.clientWidth + 1 || r.width < 1) continue;
        if (clipChain(el).some((c) => c.x !== "visible")) continue;
        if (!el.checkVisibility({ checkVisibilityCSS: true })) continue;
        culprits.push([el, r.right]);
      }
      const leaves = culprits.filter(([el]) => !culprits.some(([o]) => o !== el && el.contains(o)));
      leaves.sort((a, b) => b[1] - a[1]);
      report(6, "horizontal-page-scroll", leaves.length ? leaves[0][0] : document.body, { scrollWidth: de.scrollWidth, clientWidth: de.clientWidth, widest: leaves.slice(0, 4).map(([el, right]) => `${selectorOf(el)} right=${round(right)}`) });
    }
    // One Banner of each severity per surface.
    const groups = new Map();
    for (const b of document.querySelectorAll("[data-ui-banner], section.banner, .banner[role]")) {
      if (!visible(b) || !inRoots(b, roots)) continue;
      const variant = b.dataset.variant || ([...b.classList].find((c) => /^banner-(critical|warning|success|info|upsell|danger|error|ok)$/.test(c)) || "banner-info").slice(7);
      const surface = b.parentElement.closest("dialog, [role=dialog], [role=tabpanel], main, #main, body");
      const key = surface;
      if (!groups.has(key)) groups.set(key, {});
      (groups.get(key)[variant] = groups.get(key)[variant] || []).push(b);
    }
    for (const [surface, byVariant] of groups) {
      for (const [variant, list] of Object.entries(byVariant)) {
        if (list.length > 1) report(6, "banner-count", list[1], { variant, count: list.length, surface: selectorOf(surface), texts: list.map((b) => labelOf(b).slice(0, 40)) });
      }
    }
  }

  function checkTapTargets(list) {
    const de = document.documentElement;
    for (const el of list) {
      if (style(el).pointerEvents === "none") continue;
      const rect0 = el.getBoundingClientRect();
      if (rect0.width >= 43.5 && rect0.height >= 43.5) continue;
      const chain = clipChain(el);
      const saved = saveScroll(chain);
      let r = rect0;
      // Measure with the element scrolled fully into view, so a clip or viewport edge does not cut its hit area.
      el.scrollIntoView({ block: "center", inline: "nearest", behavior: "instant" });
      r = el.getBoundingClientRect();
      const frags = el.getClientRects();
      if (frags.length > 1) r = frags[0];
      const cx = r.left + r.width / 2;
      const cy = r.top + r.height / 2;
      const vw = de.clientWidth;
      const vh = de.clientHeight;
      const hit = (x, y) => {
        if (x < 0 || y < 0 || x >= vw || y >= vh) return true;
        const t = document.elementFromPoint(x, y);
        if (hitOk(el, t)) return true;
        // An open menu or dialog over the page is a separate state. It does not shrink the hit area of the controls behind it.
        const ov = t && t.closest(OVERLAY);
        if (ov && !ov.contains(el) && !el.contains(ov)) return true;
        // Hit areas of neighbouring targets overlap past their boxes. A point belongs to the neighbour only inside the neighbour's box.
        const other = t && t.closest(INTERACTIVE);
        if (other && other !== el && !other.contains(el)) {
          const orr = other.getBoundingClientRect();
          if (x < orr.left || x > orr.right || y < orr.top || y > orr.bottom) return true;
        }
        return false;
      };
      if (!hit(cx, cy)) {
        restoreScroll(saved);
        continue;
      }
      const reach = 30;
      let left = cx;
      while (left > r.left - reach && hit(left - 1, cy)) left -= 1;
      let right = cx;
      while (right < r.right + reach && hit(right + 1, cy)) right += 1;
      let top = cy;
      while (top > r.top - reach && hit(cx, top - 1)) top -= 1;
      let bottom = cy;
      while (bottom < r.bottom + reach && hit(cx, bottom + 1)) bottom += 1;
      const w = right - left + 1;
      const h = bottom - top + 1;
      // 1px steps can lose up to 1px at each edge, so 43px passes.
      if (w < 43 || h < 43) report(6, "tap-target-under-44", el, { box: [round(r.width), round(r.height)], hitArea: [round(w), round(h)] });
      restoreScroll(saved);
    }
  }

  /* ---------- Check 7: colour ---------- */

  const canvas = document.createElement("canvas");
  canvas.width = canvas.height = 1;
  const cx2d = canvas.getContext("2d", { willReadFrequently: true });
  const colorCache = new Map();
  function rgba(str) {
    if (!str || str === "none" || str === "transparent") return null;
    if (colorCache.has(str)) return colorCache.get(str);
    let out = null;
    const m = /^rgba?\(\s*([\d.]+)[,\s]+([\d.]+)[,\s]+([\d.]+)(?:\s*[,/]\s*([\d.]+%?))?\s*\)$/.exec(str);
    if (m) {
      let a = m[4] === undefined ? 1 : m[4].endsWith("%") ? px(m[4]) / 100 : px(m[4]);
      out = [px(m[1]), px(m[2]), px(m[3]), a];
    } else if (CSS.supports("color", str)) {
      cx2d.clearRect(0, 0, 1, 1);
      cx2d.fillStyle = "rgba(0,0,0,0)";
      cx2d.fillStyle = str;
      cx2d.fillRect(0, 0, 1, 1);
      const d = cx2d.getImageData(0, 0, 1, 1).data;
      out = [d[0], d[1], d[2], d[3] / 255];
    }
    colorCache.set(str, out);
    return out;
  }
  function hsl([r, g, b]) {
    r /= 255;
    g /= 255;
    b /= 255;
    const max = Math.max(r, g, b);
    const min = Math.min(r, g, b);
    const l = (max + min) / 2;
    const d = max - min;
    let h = 0;
    const s = d === 0 ? 0 : d / (1 - Math.abs(2 * l - 1));
    if (d) {
      if (max === r) h = 60 * (((g - b) / d) % 6);
      else if (max === g) h = 60 * ((b - r) / d + 2);
      else h = 60 * ((r - g) / d + 4);
    }
    if (h < 0) h += 360;
    return [h, s, l];
  }
  const SYNTAX = "[class*='tok-'], [class*='hljs'], .chroma *, .highlight *, pre code *, .cm-content [class^='ͼ'], .cm-line [class^='ͼ'], .md-body pre *";
  const USER_MEDIA = "img, video, canvas, picture, image, .fv-pdf-page *, .textLayer *";

  function colourProps(el, cs, hasText) {
    const out = [];
    if (hasText) out.push(["color", cs.color]);
    out.push(["background-color", cs.backgroundColor]);
    for (const s of ["Top", "Right", "Bottom", "Left"]) if (px(cs[`border${s}Width`]) > 0 && cs[`border${s}Style`] !== "none") out.push([`border-${s.toLowerCase()}-color`, cs[`border${s}Color`]]);
    if (cs.outlineStyle !== "none" && px(cs.outlineWidth) > 0) out.push(["outline-color", cs.outlineColor]);
    if (cs.textDecorationLine && cs.textDecorationLine !== "none" && hasText) out.push(["text-decoration-color", cs.textDecorationColor]);
    if (cs.boxShadow && cs.boxShadow !== "none") for (const m of cs.boxShadow.matchAll(/rgba?\([^)]*\)|oklch\([^)]*\)|color\([^)]*\)/g)) out.push(["box-shadow", m[0]]);
    if (el instanceof SVGElement) {
      if (cs.fill && cs.fill !== "none" && !cs.fill.startsWith("url")) out.push(["fill", cs.fill]);
      if (cs.stroke && cs.stroke !== "none" && !cs.stroke.startsWith("url")) out.push(["stroke", cs.stroke]);
    }
    return out;
  }

  let canvasColour = null;
  function baseColour() {
    if (canvasColour) return canvasColour;
    const probe = document.createElement("div");
    probe.style.cssText = "position:absolute;width:1px;height:1px;background:Canvas;color:CanvasText";
    document.body.appendChild(probe);
    canvasColour = rgba(getComputedStyle(probe).backgroundColor) || [255, 255, 255, 1];
    probe.remove();
    return canvasColour;
  }
  const over = (fg, bg) => {
    const a = fg[3];
    return [fg[0] * a + bg[0] * (1 - a), fg[1] * a + bg[1] * (1 - a), fg[2] * a + bg[2] * (1 - a), 1];
  };
  const lum = (c) => {
    const f = (v) => {
      v /= 255;
      return v <= 0.04045 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
    };
    return 0.2126 * f(c[0]) + 0.7152 * f(c[1]) + 0.0722 * f(c[2]);
  };
  const ratio = (a, b) => {
    const [l1, l2] = [lum(a), lum(b)].sort((x, y) => y - x);
    return (l1 + 0.05) / (l2 + 0.05);
  };
  const hex = (c) => "#" + c.slice(0, 3).map((v) => Math.round(v).toString(16).padStart(2, "0")).join("") + (c[3] !== undefined && c[3] < 1 ? Math.round(c[3] * 255).toString(16).padStart(2, "0") : "");

  function effectiveBackground(el) {
    const chain = [];
    for (let n = el; n && n.nodeType === 1; n = n.parentElement) chain.push(n);
    let bg = baseColour();
    let opacity = 1;
    for (let i = chain.length - 1; i >= 0; i--) {
      const cs = style(chain[i]);
      opacity *= px(cs.opacity === "" ? 1 : cs.opacity);
      if (cs.backgroundImage && cs.backgroundImage !== "none") return { unknown: "background-image on " + selectorOf(chain[i]) };
      const c = rgba(cs.backgroundColor);
      if (c && c[3] > 0) bg = over(c, bg);
    }
    return { bg, opacity };
  }

  function checkColour(roots) {
    const yellow = new Map();
    const contrast = new Map();
    const consider = (el, cs, pseudo) => {
      for (const [prop, value] of colourProps(el, cs, !pseudo || /["']/.test(cs.content))) {
        const c = rgba(value);
        if (!c || c[3] < 0.05) continue;
        const [h, s] = hsl(c);
        if (h >= 40 && h <= 65 && s > 0.35) {
          const key = selectorOf(el) + (pseudo || "") + "|" + prop + "|" + value;
          const hit = yellow.get(key);
          if (hit) hit.count++;
          else yellow.set(key, { el, pseudo, prop, value, hue: round(h), saturation: round(s), count: 1 });
        }
      }
    };
    for (const root of roots) {
      for (const el of [root, ...root.querySelectorAll("*")]) {
        if (el.matches("script, style, template, head *")) continue;
        if (el.closest(USER_MEDIA)) continue;
        if (!el.checkVisibility || !el.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true })) continue;
        const r = el.getBoundingClientRect();
        if (r.width < 1 || r.height < 1) continue;
        const cs = style(el);
        let hasText = false;
        for (const n of el.childNodes) if (n.nodeType === 3 && n.nodeValue.trim()) hasText = true;
        const syntax = el.matches(SYNTAX);
        if (!syntax) {
          consider(el, cs, "");
          for (const pseudo of ["::before", "::after"]) {
            const ps = style(el, pseudo);
            if (ps.content && ps.content !== "none" && ps.content !== "normal") consider(el, ps, pseudo);
          }
        }
        if (!hasText || srOnly(el)) continue;
        if (el.closest(":disabled, [aria-disabled='true']")) continue;
        if (el.matches("input, textarea, select, option")) continue;
        const fg = rgba(cs.color);
        if (!fg) continue;
        const eb = effectiveBackground(el);
        if (eb.unknown || eb.opacity < 0.99) continue;
        const shown = over(fg, eb.bg);
        const size = px(cs.fontSize);
        const weight = Number(cs.fontWeight) || 400;
        const large = size >= 24 || (size >= 18.66 && weight >= 700);
        const need = large ? 3 : 4.5;
        const got = ratio(shown, eb.bg);
        if (got + 0.005 < need) {
          // Syntax tokens of one code block share a cause, so they form one finding on the block.
          const target = syntax ? el.closest("pre, .highlight, .chroma") || el : el;
          const key = syntax ? selectorOf(target) + "|syntax|" + hex(eb.bg) : selectorOf(el) + "|" + hex(shown) + "|" + hex(eb.bg);
          const hit = contrast.get(key);
          const r2 = Math.round(got * 100) / 100;
          if (hit) {
            hit.count++;
            if (r2 < hit.ratio) Object.assign(hit, { fg: hex(fg), ratio: r2 });
            if (syntax) hit.tokens.add(`${hex(fg)} ${r2}:1`);
          } else contrast.set(key, { el: target, fg: hex(fg), shown: hex(shown), bg: hex(eb.bg), ratio: r2, need, fontSize: size, weight, count: 1, syntax, tokens: new Set(syntax ? [`${hex(fg)} ${r2}:1`] : []) });
        }
      }
    }
    for (const y of yellow.values()) report(7, "yellow-or-amber", y.el, { property: (y.pseudo || "") + y.prop, value: y.value, hue: y.hue, saturation: y.saturation, elements: y.count });
    for (const c of contrast.values()) report(7, "contrast-below-aa", c.el, { fg: c.fg, bg: c.bg, ratio: c.ratio, required: c.need, fontSize: c.fontSize, weight: c.weight, elements: c.count, syntaxToken: c.syntax, ...(c.syntax ? { tokenColours: [...c.tokens].slice(0, 12) } : {}) });
  }

  /* ---------- Shared helpers for checks 1, 2, 7, 8 and 9 ---------- */

  const USER_CONTENT = ".md-body, .markdown-body, .cm-content, .answer-body, .ms-comment-body";
  const LIST_CONTEXT = "li, tr, dl, [role=listitem], [role=row], [role=treeitem], [role=option], [role=menu], [role=listbox], [role=tablist], [role=grid], [role=tree]";
  const r2 = (v) => Math.round(v * 100) / 100;
  // How many elements, rows and groups the checks compared, so a run shows that each rule found its markup.
  let counts = {};

  function accessibleName(el) {
    const ids = (el.getAttribute("aria-labelledby") || "").split(/\s+/).filter(Boolean);
    const by = ids.map((id) => document.getElementById(id)).filter(Boolean).map((n) => n.textContent).join(" ");
    const value = el.matches("input[type=submit], input[type=button]") ? el.value : "";
    return norm(el.getAttribute("aria-label") || by || visibleText(el) || value || el.getAttribute("title") || "");
  }

  function onScreen(el) {
    const o = overflowOf(el.getBoundingClientRect(), clipChain(el));
    return o.visW >= 1 && o.visH >= 1;
  }

  function opacityChain(n) {
    let o = 1;
    for (let x = n; x && x.nodeType === 1; x = x.parentElement) o *= parseFloat(style(x).opacity);
    return o;
  }

  const grow = (map, key) => map.get(key) || map.set(key, []).get(key);

  function allowedReasons(el, check) {
    const out = {};
    for (const a of allow) {
      if (a.check !== check && a.check !== "*") continue;
      try {
        if (el.matches(a.selector)) out[a.rule || "*"] = a.reason;
      } catch {
        /* bad selector in allow.json */
      }
    }
    return out;
  }

  /* ---------- Check 1: duplicate actions ---------- */

  function checkDuplicateActions(list, cfg) {
    const ACTIONS = "a[href], button, [role=button], [role=link], input[type=button], input[type=submit], summary";
    const keys = new Map();
    for (const el of list) {
      if (!el.matches(ACTIONS) || el.matches("[role=tab]")) continue;
      if (el.closest(LIST_CONTEXT) || el.closest(USER_CONTENT)) continue;
      if (cfg.ignore && el.matches(cfg.ignore)) continue;
      if (!onScreen(el)) continue;
      const name = accessibleName(el);
      const href = el.getAttribute("href");
      // Two buttons with one name, or two links with one name and one destination, are one action shown twice.
      if (name && !el.matches("a[href]")) grow(keys, "name: " + name).push(el);
      if (el.matches("a[href]") && href && !href.startsWith("#") && !/^javascript:/i.test(href)) {
        const u = new URL(el.href, location.href);
        grow(keys, "href: " + u.pathname + u.search).push(el);
      }
      for (const a of el.attributes) if (/^data-(cmd|command|action)$/.test(a.name) && a.value) grow(keys, `command: ${a.name}="${a.value}"`).push(el);
    }
    counts.actionKeys = (counts.actionKeys || 0) + keys.size;
    const flagged = new Set();
    for (const [key, group] of keys) {
      const distinct = group.filter((e) => !group.some((o) => o !== e && o.contains(e)));
      if (distinct.length < 2 || flagged.has(distinct[1])) continue;
      flagged.add(distinct[1]);
      report(1, "duplicate-action", distinct[1], { key, count: distinct.length, controls: distinct.slice(0, 4).map((e) => `${selectorOf(e)} "${labelOf(e).slice(0, 30)}"`), spec: cfg.ref });
    }
  }

  /* ---------- Check 2: dead clicks ---------- */

  /** True when the element draws something of its own: a fill, a border, text or replaced content. */
  function paints(n) {
    const cs = style(n);
    if (cs.visibility === "hidden") return false;
    const bg = rgba(cs.backgroundColor);
    if (bg && bg[3] > 0.05) return true;
    if (cs.backgroundImage !== "none" || (cs.backdropFilter && cs.backdropFilter !== "none")) return true;
    for (const s of ["Top", "Right", "Bottom", "Left"]) {
      const c = rgba(cs[`border${s}Color`]);
      if (px(cs[`border${s}Width`]) > 0 && cs[`border${s}Style`] !== "none" && c && c[3] > 0.05) return true;
    }
    if (n.matches("img, svg, video, canvas, iframe, input, textarea, select, picture, embed, object")) return true;
    for (const c of n.childNodes) if (c.nodeType === 3 && c.nodeValue.trim()) return true;
    return false;
  }

  /** Returns hit when nothing between hit and the common ancestor with el paints, or null. */
  function invisibleCover(hit, el) {
    if (hit === document.documentElement || hit === document.body) return null;
    for (let n = hit; n && n.nodeType === 1 && n !== document.body; n = n.parentElement) {
      if (n.contains(el)) break;
      if (paints(n) && opacityChain(n) > 0.05) return null;
    }
    return hit;
  }

  /** Reports dead-click when the centre of el is taken by an element that paints nothing. */
  function deadClick(el, rect, clip) {
    const frags = el.getClientRects();
    const r = frags.length > 1 ? frags[0] : rect;
    const left = Math.max(r.left, clip.left);
    const right = Math.min(r.right, clip.right);
    const top = Math.max(r.top, clip.top);
    const bottom = Math.min(r.bottom, clip.bottom);
    if (right - left < 1 || bottom - top < 1) return false;
    const x = (left + right) / 2;
    const y = (top + bottom) / 2;
    const hit = document.elementFromPoint(x, y);
    if (!hit || hitOk(el, hit)) return false;
    const cover = invisibleCover(hit, el);
    if (!cover) return false;
    report(2, "dead-click", el, { reason: "elementFromPoint at the centre returns an element that paints nothing", point: [round(x), round(y)], hit: selectorOf(cover), hitPosition: style(cover).position, hitZIndex: style(cover).zIndex, hitRect: R(cover.getBoundingClientRect()) });
    return true;
  }

  /* ---------- Check 7: control contrast ---------- */

  /** Colour of a 1px or wider ring drawn with box-shadow and no offset or blur, or null. */
  function ringColour(cs) {
    if (!cs.boxShadow || cs.boxShadow === "none") return null;
    let best = null;
    for (const part of cs.boxShadow.split(/,(?![^(]*\))/)) {
      const cm = /(rgba?\([^)]*\)|oklch\([^)]*\)|#[0-9a-f]{3,8})/i.exec(part);
      if (!cm) continue;
      const lens = part.replace(cm[0], "").replace("inset", "").trim().split(/\s+/).map(px);
      if (lens.length >= 4 && lens[0] === 0 && lens[1] === 0 && lens[2] === 0 && lens[3] >= 1) {
        const c = rgba(cm[0]);
        if (c && (!best || c[3] > best[3])) best = c;
      }
    }
    return best;
  }

  /** Contrast of a box's fill and of its border or ring against the surface behind it. */
  function boundaryRatios(box) {
    const cs = style(box);
    const surface = box.parentElement ? effectiveBackground(box.parentElement) : { bg: baseColour() };
    // A background image on the box itself, such as a select caret, does not change its fill or border.
    if (surface.unknown) return null;
    const fill = rgba(cs.backgroundColor);
    const own = fill && fill[3] > 0 ? over(fill, surface.bg) : surface.bg;
    const bg = ratio(own, surface.bg);
    let border = 1;
    const sides = ["Top", "Right", "Bottom", "Left"].filter((s) => px(cs[`border${s}Width`]) >= 1 && cs[`border${s}Style`] !== "none");
    if (sides.length) {
      border = Math.min(...sides.map((s) => {
        const c = rgba(cs[`border${s}Color`]);
        return c ? ratio(over(c, own), surface.bg) : 1;
      }));
    }
    const ring = ringColour(cs);
    if (ring) border = Math.max(border, ratio(over(ring, surface.bg), surface.bg));
    return { bg, border, surface: hex(surface.bg), fill: hex(own) };
  }

  function checkControlContrast(list, cfg) {
    const min = cfg.min || 3;
    for (const el of list) {
      const field = el.matches(cfg.fields);
      const control = !field && el.matches(cfg.controls);
      if (!field && !control) continue;
      if (el.closest(cfg.exempt) || el.closest(":disabled, [aria-disabled='true']") || el.closest(USER_CONTENT)) continue;
      if (opacityChain(el) < 0.99) continue;
      // An Avatar or image that fills the control is its visible boundary.
      const er = el.getBoundingClientRect();
      if (control && [...el.querySelectorAll(".avatar, img")].some((a) => {
        const ar = a.getBoundingClientRect();
        return visible(a) && ar.width * ar.height >= 0.4 * er.width * er.height;
      })) continue;
      counts.controls = (counts.controls || 0) + 1;
      const h0 = er.height;
      // A field's visible box can be a wrapper up to 3 levels up that contains it, such as .text-input or a composer surface.
      const boxes = [el];
      if (field) {
        for (let n = el.parentElement, i = 0; n && n !== document.body && i < 3; n = n.parentElement, i++) {
          const nr = n.getBoundingClientRect();
          if (nr.height <= h0 + 120 && nr.left <= er.left + 1 && nr.right >= er.right - 1) boxes.push(n);
        }
      }
      let best = null;
      for (const n of boxes) {
        const b = boundaryRatios(n);
        if (b && (!best || Math.max(b.bg, b.border) > Math.max(best.bg, best.border))) best = { ...b, box: n };
      }
      if (!best) continue;
      const text = visibleText(el);
      const measured = { kind: field ? "field" : "button", border: r2(best.border), background: r2(best.bg), required: min, surface: best.surface, fill: best.fill, box: selectorOf(best.box), spec: cfg.ref };
      if (Math.max(best.bg, best.border) + 0.005 < min) {
        if (control && el.matches(cfg.invisible)) {
          if (text && !el.closest(cfg.invisibleTextContexts)) report(7, "control-contrast", el, { ...measured, problem: "a text Button in the invisible variant outside the places docs/primer.md uses one" });
        } else {
          report(7, "control-contrast", el, { ...measured, problem: `${field ? "field" : "button"} has no boundary or fill of ${min}:1 against the surface` });
        }
      }
      if (control && !text) {
        const icon = [...el.querySelectorAll("svg")].find(visible);
        if (!icon) continue;
        const ics = style(icon);
        const paint = [ics.fill, ics.stroke].find((v) => v && v !== "none" && !v.startsWith("url"));
        const c = rgba(paint || ics.color);
        const eb = effectiveBackground(el);
        if (!c || eb.unknown || c[3] < 0.05) continue;
        const got = ratio(over(c, eb.bg), eb.bg);
        if (got + 0.005 < min) report(7, "control-contrast", el, { kind: "icon", icon: hex(c), background: hex(eb.bg), ratio: r2(got), required: min, problem: "the icon of an icon-only control is below 3:1", spec: cfg.ref });
      }
    }
  }

  /* ---------- Check 7: old tokens ---------- */

  const DYNAMIC_PSEUDO = /::?(hover|focus-visible|focus-within|focus|active|visited|link|checked|placeholder-shown|autofill|before|after|placeholder|marker|selection|backdrop|first-line|first-letter|-webkit-[\w-]+|-moz-[\w-]+)(?![\w-])/g;

  function oldTokenRules(tokens) {
    const re = new RegExp(`var\\(\\s*(${tokens.map((t) => t.replace(/[-]/g, "\\-")).join("|")})\\s*[,)]`);
    const out = [];
    const walk = (rules, href) => {
      for (const rule of rules) {
        if (rule.cssRules && !rule.selectorText) {
          try {
            if (rule.media && !matchMedia(rule.media.mediaText).matches) continue;
          } catch {
            /* ignore */
          }
          walk(rule.cssRules, href);
          continue;
        }
        if (!rule.selectorText || !rule.style) continue;
        for (const decl of rule.style.cssText.split(/;(?![^(]*\))/)) {
          const at = decl.indexOf(":");
          if (at < 0) continue;
          const m = re.exec(decl.slice(at + 1));
          if (m) out.push({ selector: rule.selectorText, prop: decl.slice(0, at).trim(), token: m[1], value: decl.slice(at + 1).trim().slice(0, 100), href });
        }
        if (rule.cssRules && rule.cssRules.length) walk(rule.cssRules, href);
      }
    };
    for (const sheet of document.styleSheets) {
      try {
        walk(sheet.cssRules, sheet.href || location.href);
      } catch {
        /* cross-origin */
      }
    }
    return out;
  }

  function checkOldTokens(roots, cfg) {
    const done = new Set();
    for (const r of oldTokenRules(cfg.tokens)) {
      for (const part of r.selector.split(/,(?![^(]*\))/)) {
        let sel = part.replace(DYNAMIC_PSEUDO, "").trim();
        if (!sel || /[>+~]$/.test(sel)) sel += "*";
        let match = null;
        try {
          match = [...document.querySelectorAll(sel)].find((el) => (el === document.documentElement || inRoots(el, roots)) && (el === document.documentElement || visible(el)));
        } catch {
          continue;
        }
        if (!match) continue;
        const path = new URL(r.href, location.href).pathname;
        const file = path.startsWith("/static/") ? "internal/web" + path : path;
        const key = `${file}|${r.selector}|${r.prop}`;
        if (done.has(key)) break;
        done.add(key);
        const f = report(7, "old-token", match, { token: r.token, declaration: `${r.prop}: ${r.value}`, rule: r.selector.slice(0, 160), stylesheet: file, spec: cfg.ref }, { owner: { primary: file, css: [file], markup: [] } });
        f.selector = `${file} ${r.selector.slice(0, 80)} { ${r.prop} }`;
        break;
      }
    }
    for (const el of document.querySelectorAll("[style*='var(--']")) {
      if (!inRoots(el, roots) || !visible(el)) continue;
      const m = new RegExp(`var\\(\\s*(${cfg.tokens.map((t) => t.replace(/[-]/g, "\\-")).join("|")})\\s*[,)]`).exec(el.getAttribute("style"));
      if (m) report(7, "old-token", el, { token: m[1], declaration: el.getAttribute("style").slice(0, 120), rule: "inline style attribute", spec: cfg.ref });
    }
  }

  /* ---------- Check 8: consistency ---------- */

  /** Leftmost painted content of a row: a visible icon, control or the first character, ignoring nested lists. */
  function leadingEdge(row, nested) {
    let left = Infinity;
    let what = "";
    const inNested = (n) => {
      const g = n.closest(nested);
      return !!g && g !== row && row.contains(g);
    };
    for (const n of row.querySelectorAll("svg, img, .avatar, button, [role=button], input, .emoji, .tree-view-toggle")) {
      if (inNested(n)) continue;
      if (n.closest("svg") && n.localName !== "svg") continue;
      if (!visible(n) || opacityChain(n) < 0.05) continue;
      const r = n.getBoundingClientRect();
      if (r.left < left) {
        left = r.left;
        what = selectorOf(n);
      }
    }
    const t = firstTextNode(row, inNested);
    if (t) {
      const cr = firstCharRect(t);
      if (cr.width && cr.left < left) {
        left = cr.left;
        what = "text";
      }
    }
    return { left, what };
  }

  function checkIndentation(cfg) {
    const columns = [...document.querySelectorAll(cfg.columns)].filter(visible);
    const outer = columns.filter((c) => !columns.some((o) => o !== c && o.contains(c)));
    for (const col of outer) {
      const lists = [...col.querySelectorAll(cfg.lists)].filter((l) => visible(l) && !l.parentElement.closest(cfg.lists) && !l.closest(".sr-only"));
      if (lists.length < 2) continue;
      const edges = [];
      for (const list of lists) {
        const items = list.matches("[role=tree]") ? [...list.querySelectorAll("[role=treeitem][aria-level='1']")] : [...list.children].filter((c) => c.localName === "li" || c.matches("[role=listitem], a, button"));
        let best = { left: Infinity, what: "" };
        let first = null;
        for (const item of items.filter(visible).slice(0, 30)) {
          const row = [...item.children].find((c) => visible(c) && !c.matches("[role=group], ul, ol")) || item;
          const e = leadingEdge(row, "[role=group], ul, ol");
          if (e.left < best.left) {
            best = e;
            first = row;
          }
        }
        if (first && best.left < Infinity) edges.push({ list, row: first, ...best });
      }
      counts.indentLists = (counts.indentLists || 0) + edges.length;
      if (edges.length < 2) continue;
      const ref = edges.reduce((a, b) => (b.left < a.left ? b : a));
      for (const e of edges) {
        if (e === ref || e.left - ref.left <= 1) continue;
        report(8, "unexplained-indent", e.row, { offsetPx: round(e.left - ref.left), listLeadingEdge: round(e.left), leadingContent: e.what, siblingList: selectorOf(ref.list), siblingLeadingEdge: round(ref.left), siblingContent: ref.what, spec: cfg.ref });
      }
    }
  }

  /** In one menu or ActionList, every item starts its text at the same left edge: all items have a leading visual, or none do. */
  function checkMenuEdges(roots, cfg) {
    const lists = [...document.querySelectorAll(cfg.lists)].filter((l) => inRoots(l, roots) && visible(l));
    for (const list of lists.filter((l) => !lists.some((o) => o !== l && o.contains(l)))) {
      // Items of a submenu belong to that submenu.
      const items = [...list.querySelectorAll(cfg.items)].filter((it) => {
        if (!visible(it)) return false;
        const sub = it.parentElement.closest("[role=menu]");
        return !sub || sub === list || sub.contains(list);
      });
      if (items.length < 2) continue;
      counts.menuItems = (counts.menuItems || 0) + items.length;
      const lefts = [];
      for (const it of items) {
        const t = firstTextNode(it, (p) => !!p.closest(cfg.skipText));
        if (t) lefts.push([it, round(firstCharRect(t).left)]);
      }
      if (lefts.length < 2) continue;
      const tally = new Map();
      for (const [, l] of lefts) tally.set(l, (tally.get(l) || 0) + 1);
      const mode = [...tally.entries()].sort((a, b) => b[1] - a[1] || a[0] - b[0])[0][0];
      const odd = lefts.filter(([, l]) => Math.abs(l - mode) > 2);
      if (!odd.length) continue;
      report(8, "mixed-leading-visuals", odd[0][0], { textLeft: odd[0][1], othersTextLeft: mode, items: lefts.slice(0, 12).map(([it, l]) => `"${labelOf(it).slice(0, 20)}" ${round(l - mode)}px`), menu: selectorOf(list), spec: cfg.ref });
    }
  }

  function textStyle(root, skip) {
    const t = firstTextNode(root, skip);
    if (!t) return null;
    const p = t.parentElement;
    const cs = style(p);
    const fg = rgba(cs.color);
    const eb = effectiveBackground(p);
    if (!fg || eb.unknown) return null;
    return { el: p, text: t.nodeValue.trim().slice(0, 30), size: px(cs.fontSize), weight: Number(cs.fontWeight) || 400, contrast: r2(ratio(over(fg, eb.bg), eb.bg)), colour: hex(fg) };
  }

  function checkMetadata(roots, cfg) {
    const done = new Set();
    for (const row of document.querySelectorAll(cfg.rows)) {
      if (!inRoots(row, roots) || !visible(row) || row.closest(USER_CONTENT)) continue;
      // Text inside a separate control in the row, such as a menu button, is not metadata.
      const ownAction = (n) => {
        const a = n.closest(ACTION);
        return !!a && a !== row && row.contains(a);
      };
      const metas = [...row.querySelectorAll(cfg.meta)].filter((m) => visible(m) && !ownAction(m));
      const outerMetas = metas.filter((m) => !metas.some((o) => o !== m && o.contains(m)));
      const primaryEl = [...row.querySelectorAll(cfg.primary)].find((p) => visible(p) && !p.closest(cfg.meta) && !p.querySelector(cfg.meta) && !p.matches(".sr-only"));
      if (!primaryEl || !outerMetas.length) continue;
      const P = textStyle(primaryEl);
      if (!P) continue;
      for (const m of outerMetas) {
        if (m.contains(primaryEl) || primaryEl.contains(m)) continue;
        const M = textStyle(m, (p) => ownAction(p) || !!p.closest(".label, .counter-label, .sr-only"));
        if (!M) continue;
        counts.metadataPairs = (counts.metadataPairs || 0) + 1;
        const key = selectorOf(M.el);
        if (done.has(key)) continue;
        const smaller = M.size < P.size - 0.5;
        const muted = M.contrast < P.contrast * 0.8;
        const heavier = M.weight > P.weight;
        if ((smaller || muted) && !heavier) continue;
        done.add(key);
        report(8, "metadata-prominence", M.el, { meta: { text: M.text, size: M.size, weight: M.weight, contrast: M.contrast, colour: M.colour }, primary: { selector: selectorOf(P.el), text: P.text, size: P.size, weight: P.weight, contrast: P.contrast, colour: P.colour }, problem: heavier ? "metadata is heavier than the primary text" : "metadata is neither smaller nor muted", spec: cfg.ref });
      }
    }
  }

  function checkConsistency(roots, cfg) {
    const groups = new Map();
    const firstClass = (c) => [...c.classList].filter((x) => !NOISY.test(x))[0];
    for (const sel of cfg.components) {
      for (const el of document.querySelectorAll(sel)) {
        if (!inRoots(el, roots) || !visible(el) || el.closest(USER_CONTENT)) continue;
        if (/padding|gap|--/.test(el.getAttribute("style") || "")) continue;
        const cls = [...el.classList].filter((c) => !NOISY.test(c)).sort().join(".");
        const kids = [...el.children].filter(visible).map((c) => c.localName + (firstClass(c) ? "." + firstClass(c) : "")).join(",");
        const levelEl = el.closest("[aria-level]");
        const region = el.parentElement ? el.parentElement.closest("dialog, [role=dialog], [role=menu], header, aside, nav, main, footer, [role=toolbar]") : null;
        const attrs = ["data-size", "data-variant", "data-layout", "data-padding"].map((a) => el.getAttribute(a) || "").join("|") + (el.matches("td, th") && el.parentElement ? ` cell ${el.cellIndex} of ${el.parentElement.cells.length}` : "");
        const key = [sel, `${el.localName}.${cls}`, kids, levelEl ? levelEl.getAttribute("aria-level") : "", attrs, region ? selectorOf(region) : ""].join(" | ");
        grow(groups, key).push(el);
      }
    }
    const names = ["padding-top", "padding-right", "padding-bottom", "padding-left", "row-gap", "column-gap"];
    const reported = new Set();
    for (const [key, list] of groups) {
      if (list.length < 2) continue;
      counts.spacingGroups = (counts.spacingGroups || 0) + 1;
      const vals = list.map((el) => {
        const cs = style(el);
        const box = /flex|grid/.test(cs.display);
        return [px(cs.paddingTop), px(cs.paddingRight), px(cs.paddingBottom), px(cs.paddingLeft), box ? px(cs.rowGap) : null, box ? px(cs.columnGap) : null];
      });
      const odd = new Map();
      names.forEach((name, i) => {
        const counts = new Map();
        for (const v of vals) if (v[i] !== null) counts.set(v[i], (counts.get(v[i]) || 0) + 1);
        if (counts.size < 2) return;
        const mode = [...counts.entries()].sort((a, b) => b[1] - a[1] || a[0] - b[0])[0][0];
        vals.forEach((v, k) => {
          if (v[i] !== null && Math.abs(v[i] - mode) > 1) grow(odd, list[k]).push(`${name} ${v[i]}px, the others ${mode}px`);
        });
      });
      const first = [...odd.keys()].find((e) => !reported.has(e));
      if (!first) continue;
      reported.add(first);
      report(8, "inconsistent-spacing", first, { component: key.split(" | ")[0], group: key.split(" | ").slice(1, 2).join(""), differences: odd.get(first), outliers: odd.size, of: list.length, examples: [...odd.keys()].slice(0, 3).map((e) => `${selectorOf(e)} "${labelOf(e).slice(0, 24)}"`), spec: cfg.ref });
    }
  }

  /* ---------- Check 9 and click effects: helpers for the runner ---------- */

  let overlayCfg = {};
  function topOverlay() {
    const cands = [...document.querySelectorAll(overlayCfg.selector)].filter((o) => visible(o) && (!o.matches("[popover]") || inPopoverOpen(o)) && (!o.matches("dialog") || o.open) && !(overlayCfg.ignore && o.matches(overlayCfg.ignore)));
    const inner = cands.filter((o) => !cands.some((p) => p !== o && o.contains(p)));
    let top = inner[inner.length - 1] || null;
    for (const o of inner) {
      const r = o.getBoundingClientRect();
      const hit = document.elementFromPoint(r.left + r.width / 2, r.top + Math.min(r.height / 2, 20));
      if (hit && o.contains(hit)) top = o;
    }
    return top;
  }

  function overlayInfo(cfg, allowList) {
    overlayCfg = cfg;
    allow = allowList || [];
    const o = topOverlay();
    if (!o) return null;
    const kind = o.matches(cfg.menus) ? "menu" : o.matches(cfg.autocomplete) ? "autocomplete" : o.matches("[role=alertdialog]") ? "alertdialog" : "dialog";
    // The Close button of a menu can sit in the overlay that wraps the menu list.
    const shell = (kind === "menu" && o.parentElement && o.parentElement.closest(".overlay, .anchored-overlay, [popover], dialog")) || o;
    const close = [...shell.querySelectorAll(kind === "alertdialog" ? cfg.close + ", " + cfg.cancel : cfg.close)].find((b) => visible(b));
    return { id: elId(o), kind, selector: selectorOf(o), text: labelOf(o), classes: classesOf(o), rect: R(o.getBoundingClientRect()), closeId: close ? elId(close) : -1, closeLabel: close ? labelOf(close) : "", allowed: allowedReasons(o, 9) };
  }

  function overlayState(id) {
    const o = els[id];
    const open = !!o && o.isConnected && visible(o) && (!o.matches("dialog") || o.open) && (!o.matches("[popover]") || inPopoverOpen(o));
    let ae = document.activeElement;
    while (ae && ae.shadowRoot && ae.shadowRoot.activeElement) ae = ae.shadowRoot.activeElement;
    const body = !ae || ae === document.body || ae === document.documentElement;
    return { open, focus: body ? "body" : `${selectorOf(ae)} "${labelOf(ae).slice(0, 30)}"`, focusOnBody: body, focusInside: !!(o && ae && o.contains(ae)), focusVisible: !body && visible(ae) };
  }

  function clickTargets(cfg, allowList) {
    allow = allowList || [];
    csCache.clear();
    const { roots } = activeRoots();
    const { list } = interactive(roots, { cap: 1500 });
    const skip = new RegExp(`\\b(?:${cfg.skipNames})\\b`, "i");
    const perList = new Map();
    const out = [];
    for (const el of list) {
      if (el.closest(USER_CONTENT) || (cfg.skip && el.closest(cfg.skip))) continue;
      if (el.closest(":disabled, [aria-disabled='true']") || style(el).pointerEvents === "none") continue;
      const name = accessibleName(el) || labelOf(el).toLowerCase();
      if (skip.test(name)) continue;
      const href = el.getAttribute("href");
      // A link with a real destination navigates. Only links without one need a click.
      if (el.matches("a[href]") && href && !href.startsWith("#") && !/^javascript:/i.test(href)) continue;
      const lc = el.parentElement && el.parentElement.closest("[role=tree], [role=listbox], [role=menu], ul, ol, tbody, [role=grid], [role=tablist]");
      if (lc) {
        const n = perList.get(lc) || 0;
        if (n >= (cfg.perList || 2)) continue;
        perList.set(lc, n + 1);
      }
      out.push({ id: elId(el), key: selectorOf(el) + "|" + name, selector: selectorOf(el), text: labelOf(el), classes: classesOf(el), allowed: allowedReasons(el, 2) });
    }
    return out;
  }

  let effect = null;
  /** Scrolls the target into view and returns its centre, or null when it is gone. Then records DOM changes. */
  function armClick(id) {
    const el = els[id];
    if (!el || !el.isConnected || !visible(el)) return null;
    el.scrollIntoView({ block: "center", inline: "nearest", behavior: "instant" });
    const r = el.getBoundingClientRect();
    const clip = overflowOf(r, clipChain(el)).clip;
    const left = Math.max(r.left, clip.left);
    const right = Math.min(r.right, clip.right);
    const top = Math.max(r.top, clip.top);
    const bottom = Math.min(r.bottom, clip.bottom);
    if (right - left < 1 || bottom - top < 1) return null;
    const x = (left + right) / 2;
    const y = (top + bottom) / 2;
    const hit = document.elementFromPoint(x, y);
    let ae = document.activeElement;
    effect = { el, url: location.href, focus: ae, value: el.value, checked: el.checked, attrs: ["aria-expanded", "aria-pressed", "aria-checked", "aria-selected", "open"].map((a) => el.getAttribute(a)), mutations: 0, samples: [] };
    const ignored = (n) => {
      const e = n && (n.nodeType === 1 ? n : n.parentElement);
      return !e || !!e.closest(".tooltip, [role=tooltip], #__audit-mark");
    };
    effect.observer = new MutationObserver((recs) => {
      for (const rec of recs) {
        if (ignored(rec.target)) continue;
        if (rec.type === "attributes" && (rec.target === document.documentElement || rec.target === document.body)) continue;
        if (rec.type === "attributes" && rec.target === el && /^(class|style|aria-describedby|data-tooltip.*)$/.test(rec.attributeName)) continue;
        if (rec.type === "childList" && [...rec.addedNodes, ...rec.removedNodes].every((n) => n.nodeType !== 1 || n.matches(".tooltip, [role=tooltip]"))) continue;
        effect.mutations++;
        if (effect.samples.length < 3) effect.samples.push(`${rec.type} ${rec.attributeName || ""} on ${selectorOf(rec.target.nodeType === 1 ? rec.target : rec.target.parentElement)}`);
      }
    });
    effect.observer.observe(document, { subtree: true, childList: true, attributes: true, characterData: true });
    return { x: round(x), y: round(y), hitOk: hitOk(el, hit), hit: hit ? selectorOf(hit) : "none" };
  }

  function readClick() {
    const e = effect;
    if (!e) return null;
    e.observer.disconnect();
    effect = null;
    let ae = document.activeElement;
    const el = e.el;
    const focusMoved = !!ae && ae !== document.body && ae !== e.focus && ae !== el && !el.contains(ae);
    const fieldFocus = el.matches("input, textarea, select, [contenteditable]") && (ae === el || el.contains(ae));
    const changed = el.isConnected && (el.value !== e.value || el.checked !== e.checked || ["aria-expanded", "aria-pressed", "aria-checked", "aria-selected", "open"].some((a, i) => el.getAttribute(a) !== e.attrs[i]));
    return { url: location.href !== e.url, mutations: e.mutations, samples: e.samples, focusMoved, fieldFocus, changed, connected: el.isConnected };
  }

  /* ---------- Focus traversal helpers ---------- */

  const RING_PROPS = /outline|box-shadow|border/;
  let focusRules = null;
  function collectFocusRules() {
    const rules = [];
    const walk = (list) => {
      for (const rule of list) {
        if (rule.cssRules && !rule.selectorText) {
          try {
            if (rule.media && !matchMedia(rule.media.mediaText).matches) continue;
          } catch {
            /* ignore */
          }
          walk(rule.cssRules);
        } else if (rule.selectorText && /:focus/.test(rule.selectorText)) {
          const decl = [...rule.style].filter((p) => RING_PROPS.test(p));
          if (decl.length) rules.push({ selector: rule.selectorText, decl: decl.map((p) => [p, rule.style.getPropertyValue(p)]) });
        }
      }
    };
    for (const sheet of document.styleSheets) {
      try {
        walk(sheet.cssRules);
      } catch {
        /* cross-origin */
      }
    }
    return rules;
  }

  function ringOf(el) {
    const cs = getComputedStyle(el);
    // An editable text surface shows focus with its caret.
    if (el.isContentEditable && cs.caretColor !== "transparent" && !/rgba\(0, 0, 0, 0\)/.test(cs.caretColor)) return { kind: "caret", value: cs.caretColor, outside: 0 };
    // CodeMirror hides the native caret and draws its own cursor layer.
    const cmEditor = el.isContentEditable && el.closest(".cm-editor");
    // The cursor blinks, so a cursor layer counts even when the capture falls on the off phase.
    const cmLayer = cmEditor && cmEditor.querySelector(".cm-cursorLayer, .cm-cursor");
    if (cmLayer) return { kind: "caret", value: "CodeMirror cursor", outside: 0 };
    const oc = rgba(cs.outlineColor);
    if (cs.outlineStyle !== "none" && px(cs.outlineWidth) >= 1 && oc && oc[3] >= 0.5) return { kind: "outline", value: `${cs.outlineWidth} ${cs.outlineStyle} ${cs.outlineColor} offset ${cs.outlineOffset}`, outside: px(cs.outlineWidth) + px(cs.outlineOffset) };
    // A stretched link can draw the ring on its ::before or ::after box.
    for (const pseudo of ["::after", "::before"]) {
      const ps = getComputedStyle(el, pseudo);
      if (!ps.content || ps.content === "none" || ps.content === "normal") continue;
      const pc = rgba(ps.outlineColor);
      if (ps.outlineStyle !== "none" && px(ps.outlineWidth) >= 1 && pc && pc[3] >= 0.5) return { kind: "outline", on: selectorOf(el) + pseudo, value: `${ps.outlineWidth} ${ps.outlineStyle} ${ps.outlineColor}`, outside: 0 };
      if (ps.boxShadow && ps.boxShadow !== "none" && /inset|\d+px \d+px \d+px [1-9]/.test(ps.boxShadow)) return { kind: "box-shadow", on: selectorOf(el) + pseudo, value: ps.boxShadow.slice(0, 80), outside: 0 };
    }
    if (!focusRules) focusRules = collectFocusRules();
    const ancestors = [];
    for (let n = el.parentElement, i = 0; n && i < 3; n = n.parentElement, i++) ancestors.push(n);
    for (const target of [el, ...el.children, ...ancestors].filter(Boolean)) {
      for (const rule of focusRules) {
        let matches = false;
        try {
          matches = target.matches(rule.selector);
        } catch {
          matches = false;
        }
        if (!matches) continue;
        const tcs = getComputedStyle(target);
        for (const [prop, value] of rule.decl) {
          if (/transparent|none|^0$|^0px/.test(value.trim())) continue;
          if (prop.startsWith("outline") && tcs.outlineStyle !== "none" && px(tcs.outlineWidth) >= 1) return { kind: "outline", on: selectorOf(target), value: `${tcs.outlineWidth} ${tcs.outlineStyle} ${tcs.outlineColor}`, outside: px(tcs.outlineWidth) + px(tcs.outlineOffset) };
          if (prop === "box-shadow" && tcs.boxShadow !== "none") return { kind: "box-shadow", on: selectorOf(target), value: tcs.boxShadow.slice(0, 80), outside: 0 };
          if (prop.startsWith("border") && px(tcs.borderBottomWidth) + px(tcs.borderTopWidth) > 0) return { kind: "border", on: selectorOf(target), value: `${prop}: ${value}`, outside: 0 };
        }
      }
    }
    return null;
  }

  function focusInfo() {
    let el = document.activeElement;
    while (el && el.shadowRoot && el.shadowRoot.activeElement) el = el.shadowRoot.activeElement;
    if (!el || el === document.body || el === document.documentElement) return { none: true };
    csCache.clear();
    const r = el.getBoundingClientRect();
    const info = { id: elId(el), selector: selectorOf(el), text: labelOf(el), rect: R(r), classes: classesOf(el) };
    if (!visible(el)) {
      info.invisible = true;
      return info;
    }
    info.focusVisible = el.matches(":focus-visible");
    info.ring = ringOf(el);
    const chain = clipChain(el);
    const o = overflowOf(r, chain);
    // A focused element needs to be fully visible, or show at least 48px in each direction when it is larger.
    const hidden = o.worst && (o.visW < Math.min(r.width, 48) - 1 || o.visH < Math.min(r.height, 48) - 1);
    if (hidden) info.outOfView = { limit: o.worst.box.c.viewport ? "viewport" : selectorOf(o.worst.box.c.node), hiddenPx: round(o.worst.miss) };
    else {
      const cover = coveredAt(el, r, o.clip);
      if (cover) info.coveredBy = { node: selectorOf(cover.sticky || cover.other), position: style(cover.sticky || cover.other).position };
      if (info.ring && info.ring.outside > 0.5) {
        const grown = { left: r.left - info.ring.outside, right: r.right + info.ring.outside, top: r.top - info.ring.outside, bottom: r.bottom + info.ring.outside, width: r.width + 2 * info.ring.outside, height: r.height + 2 * info.ring.outside };
        const og = overflowOf(grown, chain.filter((c) => c.x !== "scroll" || c.y !== "scroll"));
        if (og.worst && !og.worst.box.c.viewport) info.ringClipped = { by: selectorOf(og.worst.box.c.node), px: round(og.worst.miss) };
      }
    }
    return info;
  }

  function reportFocus(list) {
    for (const f of list) {
      const el = els[f.id];
      if (!el) continue;
      if (f.invisible) report(6, "focus-on-invisible-element", el, { rect: f.rect });
      else if (!f.ring) report(6, "focus-ring-missing", el, { focusVisible: f.focusVisible });
      else if (f.ringClipped) report(6, "focus-ring-clipped", el, { ring: f.ring.value, clippedBy: f.ringClipped.by, px: f.ringClipped.px });
      if (f.outOfView) report(6, "focus-obscured", el, { reason: "outside the visible area", ...f.outOfView });
      else if (f.coveredBy) report(6, "focus-obscured", el, { reason: "covered", coveredBy: f.coveredBy.node, position: f.coveredBy.position });
    }
  }

  /* ---------- Entry points ---------- */

  function run(opts) {
    findings = [];
    allow = opts.allow || [];
    state = opts.state || {};
    csCache.clear();
    canvasColour = null;
    const t0 = performance.now();
    const timings = {};
    const time = (name, fn) => {
      const t = performance.now();
      try {
        fn();
      } catch (e) {
        findings.push({ check: 0, rule: "probe-error", selector: name, text: String(e && e.stack || e).slice(0, 400), classes: [], measured: {} });
      }
      timings[name] = Math.round(performance.now() - t);
    };
    const { roots } = activeRoots();
    const { list, total } = interactive(roots, { cap: opts.cap || 1500 });
    const checks = opts.checks || {};
    const on = (n) => checks[n] !== false;
    if (on("duplicates")) time("duplicates", () => checkDuplicates(roots));
    if (on("alignment")) time("alignment", () => checkAlignment(opts.spec.alignment || {}));
    if (on("spacing")) time("spacing", () => checkSpacing(opts.spec, roots));
    if (on("overlap")) time("overlap", () => checkOverlap(list));
    if (on("textClipping")) time("textClipping", () => checkTextClipping(roots));
    if (on("brokenWords")) time("brokenWords", () => checkBrokenWords(roots));
    if (on("page")) time("page", () => checkPage(roots, opts));
    if (on("colour")) time("colour", () => checkColour(roots));
    const spec = opts.spec || {};
    counts = {};
    if (on("duplicateActions") && spec.duplicateActions) time("duplicateActions", () => checkDuplicateActions(list, spec.duplicateActions));
    if (on("controlContrast") && spec.controlContrast) time("controlContrast", () => checkControlContrast(list, spec.controlContrast));
    if (on("oldTokens") && spec.oldTokens) time("oldTokens", () => checkOldTokens(roots, spec.oldTokens));
    if (on("indentation") && spec.indentation) time("indentation", () => checkIndentation(spec.indentation));
    if (on("menuEdges") && spec.menuEdges) time("menuEdges", () => checkMenuEdges(roots, spec.menuEdges));
    if (on("metadata") && spec.metadata) time("metadata", () => checkMetadata(roots, spec.metadata));
    if (on("consistency") && spec.consistency) time("consistency", () => checkConsistency(roots, spec.consistency));
    csCache.clear();
    if (on("reachability")) time("reachability", () => checkReachability(list));
    csCache.clear();
    if (opts.phone && on("tapTargets")) time("tapTargets", () => checkTapTargets(list));
    csCache.clear();
    return { findings, stats: { interactive: total, checked: list.length, ms: Math.round(performance.now() - t0), timings, counts, roots: roots.map(selectorOf) } };
  }

  /** Scrolls a finding's element into view and returns its rect for a screenshot. */
  function reveal(id) {
    const el = els[id];
    if (!el || !el.isConnected) return null;
    const r0 = el.getBoundingClientRect();
    const vb = viewportBox();
    if (r0.bottom < 0 || r0.top > vb.bottom || r0.right < 0 || r0.left > vb.right) el.scrollIntoView({ block: "center", inline: "nearest", behavior: "instant" });
    return R(el.getBoundingClientRect());
  }

  window.__audit = {
    run,
    focusInfo,
    reportFocus: (list, opts) => {
      findings = [];
      allow = (opts && opts.allow) || [];
      reportFocus(list);
      return findings;
    },
    reveal,
    selectorOf,
    overlayInfo,
    overlayState,
    clickTargets,
    armClick,
    readClick,
    clickElement: (id) => {
      const el = els[id];
      if (el && el.isConnected) el.click();
    },
  };
})();
