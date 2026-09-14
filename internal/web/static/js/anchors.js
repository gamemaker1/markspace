// Comment anchors for the browser client.
//
// makeAnchor and resolveAnchor implement the rules in docs/comments.md and match
// tools/anchors.py case for case. Offsets count UTF-16 code units,
// which are JavaScript string indices.
//
// alignRendered, sourceRangeFromDOM and highlightAnchors move between the
// text of markdown rendered with render(md, {sourceMap: true}) and offsets in
// the markdown body. Every rendered block carries data-sp="start:end", and
// the text of a block is aligned with its source slice by a greedy match that
// skips markup.

import { decodeEntityAt } from "./markdown.js";

/** Units of body text kept before and after the selection. */
export const CONTEXT = 32;

/** Source units searched ahead for each rendered unit before giving up. */
const LOOKAHEAD = 256;

/** Rendered units that must follow a candidate match for it to be preferred. */
const CONFIRM = 6;

/** Source or rendered units a confirmation may skip. */
const CONFIRM_SKIPS = 16;

/** Source characters that are usually markup and may be skipped while confirming. */
const MARKUP = new Set(["*", "_", "~", "`", "[", "]", "(", ")", "!", "<", ">", "|", "\\", "^", "#", "-", "+", ":"]);

/**
 * Reports whether a code unit is a high surrogate.
 * @param {number} u
 * @returns {boolean}
 */
function isHigh(u) {
  return u >= 0xd800 && u <= 0xdbff;
}

/**
 * Reports whether a code unit is a low surrogate.
 * @param {number} u
 * @returns {boolean}
 */
function isLow(u) {
  return u >= 0xdc00 && u <= 0xdfff;
}

/**
 * Reports whether offset i falls between the halves of a surrogate pair.
 * @param {string} s
 * @param {number} i
 * @returns {boolean}
 */
function splitsPair(s, i) {
  return i > 0 && i < s.length && isLow(s.charCodeAt(i)) && isHigh(s.charCodeAt(i - 1));
}

/**
 * Converts a value to an integer offset, treating anything else as 0.
 * @param {any} v
 * @returns {number}
 */
function toInt(v) {
  const n = Math.trunc(Number(v));
  return Number.isFinite(n) ? n : 0;
}

/**
 * Builds an anchor for the selection [start, end) of body. Offsets are
 * clamped and swapped, never split a surrogate pair, and the prefix and
 * suffix hold up to 32 units of context.
 * @param {string} body markdown body without frontmatter
 * @param {number} start
 * @param {number} end
 * @returns {{exact: string, prefix: string, suffix: string, start: number, end: number}|null}
 *   null when the selection is empty after clamping
 */
export function makeAnchor(body, start, end) {
  const b = String(body ?? "");
  let s = Math.max(0, Math.min(toInt(start), b.length));
  let e = Math.max(0, Math.min(toInt(end), b.length));
  if (s > e) [s, e] = [e, s];
  if (splitsPair(b, s)) s--;
  if (splitsPair(b, e)) e++;
  if (s === e) return null;
  let ps = Math.max(0, s - CONTEXT);
  if (splitsPair(b, ps)) ps++;
  let se = Math.min(b.length, e + CONTEXT);
  if (splitsPair(b, se)) se--;
  return { exact: b.slice(s, e), prefix: b.slice(ps, s), suffix: b.slice(e, se), start: s, end: e };
}

/**
 * Finds an anchor in body. Every occurrence of the exact text, overlapping
 * ones included, scores the length of the common suffix of the prefix and
 * the text before it plus the common prefix of the suffix and the text after
 * it. The best score wins, then the smallest distance to the stored start,
 * then the earlier offset.
 * @param {string} body
 * @param {{exact?: string, prefix?: string, suffix?: string, start?: number, end?: number}} anchor
 * @returns {{start: number, end: number, state: "exact"|"moved"|"orphaned"}}
 */
export function resolveAnchor(body, anchor) {
  const b = String(body ?? "");
  const a = anchor || {};
  const exact = String(a.exact ?? "");
  const prefix = String(a.prefix ?? "");
  const suffix = String(a.suffix ?? "");
  const start = toInt(a.start ?? 0);
  const end = toInt(a.end ?? 0);
  const orphaned = { start: -1, end: -1, state: "orphaned" };
  if (!exact) return orphaned;
  let best = -1;
  let bestScore = -1;
  let bestDistance = 0;
  for (let i = b.indexOf(exact); i >= 0; i = b.indexOf(exact, i + 1)) {
    let before = 0;
    while (before < prefix.length && before < i &&
      prefix.charCodeAt(prefix.length - 1 - before) === b.charCodeAt(i - 1 - before)) before++;
    const after0 = i + exact.length;
    let after = 0;
    while (after < suffix.length && after0 + after < b.length &&
      suffix.charCodeAt(after) === b.charCodeAt(after0 + after)) after++;
    const score = before + after;
    const distance = Math.abs(i - start);
    // Occurrences arrive in increasing order, so a full tie keeps the earlier one.
    if (best < 0 || score > bestScore || (score === bestScore && distance < bestDistance)) {
      best = i;
      bestScore = score;
      bestDistance = distance;
    }
  }
  if (best < 0) return orphaned;
  const state = best === start && end === start + exact.length ? "exact" : "moved";
  return { start: best, end: best + exact.length, state };
}

// ---------------------------------------------------------------------------
// Alignment of rendered text with source

/**
 * Reports whether a character is whitespace.
 * @param {string} c
 * @returns {boolean}
 */
function isSpace(c) {
  return c === " " || c === "\n" || c === "\t" || c === "\r" || c === "\f" || c === " ";
}

/**
 * Returns the index of the ")" that closes the "(" at open, or -1. A
 * backslash escapes the next character and a blank line ends the search.
 * @param {string} s
 * @param {number} open
 * @returns {number}
 */
function closingParen(s, open) {
  let depth = 0;
  const limit = Math.min(s.length, open + 2048);
  for (let k = open + 1; k < limit; k++) {
    const c = s[k];
    if (c === "\\") k++;
    else if (c === "\n" && s[k + 1] === "\n") return -1;
    else if (c === "(") depth++;
    else if (c === ")") {
      if (depth === 0) return k;
      depth--;
    }
  }
  return -1;
}

/**
 * Marks the source units that markdown usually does not render: link and
 * image destinations, image alt text, reference labels in [text][label], and
 * the target of a wikilink that has a label. The alignment matches these
 * units only when nothing visible matches, because inline code can show
 * the same characters literally.
 * @param {string} s
 * @returns {Uint8Array} 1 for each hidden unit
 */
function hiddenUnits(s) {
  const hidden = new Uint8Array(s.length);
  for (const m of s.matchAll(/\[\[([^\[\]\n|]+)\|([^\[\]\n]*)\]\]/g)) {
    if (m[2].trim()) hidden.fill(1, m.index, m.index + m[1].length + 3);
  }
  for (let i = s.indexOf("]("); i >= 0; i = s.indexOf("](", i + 1)) {
    const close = closingParen(s, i + 1);
    if (close > 0) hidden.fill(1, i + 1, close + 1);
  }
  for (const m of s.matchAll(/!\[[^\]\n]*\]\(/g)) hidden.fill(1, m.index, m.index + m[0].length);
  for (const m of s.matchAll(/\]\[[^\]\n]*\]/g)) hidden.fill(1, m.index + 1, m.index + m[0].length);
  return hidden;
}

/**
 * Matches the source unit at k against the rendered text at j. An entity in
 * the source matches its decoded text.
 * @param {string} source
 * @param {number} k
 * @param {string} rendered
 * @param {number} j
 * @returns {number} rendered units matched, 0 for no match
 */
function unitMatch(source, k, rendered, j) {
  if (source.charCodeAt(k) === 38) {
    const e = decodeEntityAt(source, k);
    if (e) return rendered.startsWith(e.text, j) ? e.text.length : 0;
  }
  return source.charCodeAt(k) === rendered.charCodeAt(j) ? 1 : 0;
}

/**
 * Returns the source units a match at k consumes: the entity length for an
 * entity, otherwise 1.
 * @param {string} source
 * @param {number} k
 * @returns {number}
 */
function sourceWidth(source, k) {
  if (source.charCodeAt(k) === 38) {
    const e = decodeEntityAt(source, k);
    if (e) return e.length;
  }
  return 1;
}

/**
 * Reports whether the rendered text from j continues to match the source
 * from k for CONFIRM units. The unit at k must already match. After a
 * visible candidate, hidden source units are skipped freely and never match,
 * so a link destination cannot confirm the text around it. After a hidden
 * candidate, hidden units may match, which covers literal link syntax inside
 * code. Up to CONFIRM_SKIPS markup or whitespace units in the source, or
 * whitespace units in the rendered text, may be skipped as well.
 * @param {string} source
 * @param {Uint8Array} hidden
 * @param {number} k
 * @param {string} rendered
 * @param {number} j
 * @returns {boolean}
 */
function confirms(source, hidden, k, rendered, j) {
  const need = Math.min(CONFIRM, rendered.length - j);
  const matchHidden = hidden[k] === 1;
  let matched = 0;
  let skips = 0;
  while (matched < need) {
    if (k < source.length) {
      if (hidden[k] && !matchHidden) {
        k++;
        continue;
      }
      const m = unitMatch(source, k, rendered, j);
      if (m) {
        k += sourceWidth(source, k);
        j += m;
        matched += m;
        continue;
      }
      if (hidden[k]) {
        k++;
        continue;
      }
    }
    if (skips >= CONFIRM_SKIPS) return false;
    skips++;
    if (k < source.length && (MARKUP.has(source[k]) || isSpace(source[k]))) k++;
    else if (isSpace(rendered[j])) {
      j++;
      matched++;
    } else return false;
  }
  return true;
}

/**
 * Aligns the text of a rendered block with its markdown source. For each
 * rendered unit it scans the 256 source units after the last match and
 * takes the first candidate of the best class: confirmed and visible, then
 * confirmed and hidden, then unconfirmed and visible, then unconfirmed and
 * hidden. A candidate is confirmed when the next few rendered units also
 * match, and hidden when it lies in a link destination or a labelled
 * wikilink target. An entity such as &amp; matches its decoded text, and
 * every unit of that text maps to the "&". A unit without a match maps to -1
 * and the search resumes from the last match.
 * @param {string} source markdown slice of the block
 * @param {string} rendered text content of the rendered block
 * @returns {Int32Array} for each rendered unit, its source offset or -1
 */
export function alignRendered(source, rendered) {
  const src = String(source ?? "");
  const out = String(rendered ?? "");
  const map = new Int32Array(out.length).fill(-1);
  const hidden = hiddenUnits(src);
  let from = 0;
  let j = 0;
  while (j < out.length) {
    const limit = Math.min(src.length, from + LOOKAHEAD);
    let chosen = -1;
    let chosenWidth = 0;
    let chosenClass = 4;
    for (let k = from; k < limit; k += sourceWidth(src, k)) {
      const m = unitMatch(src, k, out, j);
      if (!m) continue;
      const cls = (confirms(src, hidden, k, out, j) ? 0 : 2) + hidden[k];
      if (cls < chosenClass) {
        chosen = k;
        chosenWidth = m;
        chosenClass = cls;
        if (cls === 0) break;
      }
    }
    if (chosen < 0) {
      j++;
      continue;
    }
    for (let t = 0; t < chosenWidth; t++) map[j + t] = chosen;
    from = chosen + sourceWidth(src, chosen);
    j += chosenWidth;
  }
  return map;
}

/**
 * Maps an offset in rendered text to an offset in its source. A start snaps
 * forward to the next mapped unit and an end snaps backward to the previous
 * one, so offsets inside markup land on text.
 * @param {string} source the source slice given to alignRendered
 * @param {Int32Array} map the result of alignRendered
 * @param {number} offset rendered offset, 0 to map.length
 * @param {boolean} isStart true for the start of a range, false for its end
 * @returns {number|null} the source offset, or null when no unit is mapped
 */
export function renderedToSource(source, map, offset, isStart) {
  const n = map.length;
  const at = Math.max(0, Math.min(toInt(offset), n));
  if (isStart) {
    for (let r = at; r < n; r++) if (map[r] >= 0) return map[r];
    for (let r = at - 1; r >= 0; r--) if (map[r] >= 0) return map[r] + sourceWidth(source, map[r]);
  } else {
    for (let r = at - 1; r >= 0; r--) if (map[r] >= 0) return map[r] + sourceWidth(source, map[r]);
    for (let r = at; r < n; r++) if (map[r] >= 0) return map[r];
  }
  return null;
}

/**
 * Marks the rendered units a source range covers. A mapped unit is covered
 * when its source offset lies in [start, end). An unmapped unit is covered
 * when the nearest mapped units on both sides are covered.
 * @param {Int32Array} map the result of alignRendered
 * @param {number} start source offset relative to the aligned slice
 * @param {number} end
 * @returns {Uint8Array} 1 for each covered rendered unit
 */
export function coveredUnits(map, start, end) {
  const n = map.length;
  const out = new Uint8Array(n);
  const fwd = new Uint8Array(n);
  let prev = 0;
  for (let r = 0; r < n; r++) {
    if (map[r] >= 0) {
      prev = map[r] >= start && map[r] < end ? 1 : 0;
      out[r] = prev;
    }
    fwd[r] = prev;
  }
  let next = 0;
  for (let r = n - 1; r >= 0; r--) {
    if (map[r] >= 0) next = out[r];
    else out[r] = fwd[r] && next ? 1 : 0;
  }
  return out;
}

/**
 * Parses a data-sp attribute.
 * @param {Element} el
 * @returns {{start: number, end: number}|null}
 */
function spOf(el) {
  const m = /^(\d+):(\d+)$/.exec(el.getAttribute("data-sp") || "");
  return m ? { start: Number(m[1]), end: Number(m[2]) } : null;
}

// ---------------------------------------------------------------------------
// DOM

const SHOW_TEXT = 4;

/** Parents whose direct text is layout whitespace, never wrapped in a mark. */
const STRUCTURAL = new Set(["UL", "OL", "TABLE", "THEAD", "TBODY", "TFOOT", "TR", "BLOCKQUOTE"]);

/**
 * Returns the nearest element with data-sp that contains node inside root.
 * @param {Element} root
 * @param {Node} node
 * @returns {Element|null}
 */
function blockOf(root, node) {
  const el = node.nodeType === 1 ? node : node.parentElement;
  const block = el ? el.closest("[data-sp]") : null;
  return block && root.contains(block) && spOf(block) ? block : null;
}

/**
 * Returns the text nodes under an element in document order.
 * @param {Element} el
 * @returns {Text[]}
 */
function textNodes(el) {
  const doc = el.ownerDocument;
  const walker = doc.createTreeWalker(el, SHOW_TEXT);
  const out = [];
  for (let t = walker.nextNode(); t; t = walker.nextNode()) out.push(t);
  return out;
}

/**
 * Returns the length of the block text before a boundary point.
 * @param {Element} block
 * @param {Node} node
 * @param {number} offset
 * @returns {number}
 */
function textBefore(block, node, offset) {
  const r = block.ownerDocument.createRange();
  r.setStart(block, 0);
  r.setEnd(node, offset);
  return r.toString().length;
}

/**
 * Locates a range boundary as a block and an offset in the block text. A
 * boundary outside every block, such as one on a list or the container, and
 * a boundary that selects none of its block's text, such as the end of a
 * triple-click selection at offset 0 of the next paragraph, move to the
 * nearest block text inside the selection: forward for a start and backward
 * for an end.
 * @param {Element} root
 * @param {Node} node
 * @param {number} offset
 * @param {boolean} isStart
 * @returns {{block: Element, offset: number}|null}
 */
function locate(root, node, offset, isStart) {
  if (!node || !root.contains(node)) return null;
  const block = blockOf(root, node);
  if (block) {
    const at = textBefore(block, node, offset);
    const edge = isStart ? at === block.textContent.length : at === 0;
    if (!edge) return { block, offset: at };
    return nearestText(root, node, offset, isStart) || { block, offset: at };
  }
  return nearestText(root, node, offset, isStart);
}

/**
 * Finds the first block text at or after a boundary point, or the last block
 * text at or before it.
 * @param {Element} root
 * @param {Node} node
 * @param {number} offset
 * @param {boolean} forward
 * @returns {{block: Element, offset: number}|null}
 */
function nearestText(root, node, offset, forward) {
  const isStart = forward;
  const doc = root.ownerDocument;
  const point = doc.createRange();
  point.setStart(node, offset);
  const texts = textNodes(root);
  if (isStart) {
    for (const t of texts) {
      if (point.comparePoint(t, 0) < 0) continue;
      const b = blockOf(root, t);
      if (b) return { block: b, offset: textBefore(b, t, 0) };
    }
  } else {
    for (let i = texts.length - 1; i >= 0; i--) {
      const t = texts[i];
      if (point.comparePoint(t, t.length) > 0) continue;
      const b = blockOf(root, t);
      if (b) return { block: b, offset: textBefore(b, t, t.length) };
    }
  }
  return null;
}

/**
 * Maps a DOM Range inside markdown rendered with {sourceMap: true} to body
 * offsets. Each boundary is measured in the text of its nearest data-sp
 * block, aligned with that block's source slice, and snapped out of markup:
 * forward for the start and backward for the end.
 * @param {Element} root the element holding the rendered markdown
 * @param {Range} range for example window.getSelection().getRangeAt(0)
 * @param {string} body the markdown string that was rendered
 * @returns {{start: number, end: number}|null} null when a boundary lies
 *   outside root or no block text lies inside the range, or the result is empty
 */
export function sourceRangeFromDOM(root, range, body) {
  if (!root || !range || range.collapsed) return null;
  const text = String(body ?? "");
  const start = locate(root, range.startContainer, range.startOffset, true);
  const end = locate(root, range.endContainer, range.endOffset, false);
  if (!start || !end) return null;
  const aligned = new Map();
  const toSource = ({ block, offset }, isStart) => {
    let entry = aligned.get(block);
    if (!entry) {
      const sp = spOf(block);
      const slice = text.slice(sp.start, sp.end);
      entry = { sp, slice, map: alignRendered(slice, block.textContent) };
      aligned.set(block, entry);
    }
    const at = renderedToSource(entry.slice, entry.map, offset, isStart);
    return at === null ? null : entry.sp.start + at;
  };
  const s = toSource(start, true);
  const e = toSource(end, false);
  if (s === null || e === null || e <= s) return null;
  return { start: s, end: e };
}

/**
 * Wraps the rendered text of each anchored passage in
 * <mark class="comment-anchor" data-comment-id="...">, one mark per text
 * node segment, inside any link or code element that holds the text.
 * Overlapping passages produce nested marks, in the order of items. The
 * marks are built with DOM methods only.
 * @param {Element} root the element holding markdown rendered with {sourceMap: true}
 * @param {string} body the markdown string that was rendered
 * @param {{id: string|number, start: number, end: number, active?: boolean}[]} items
 *   body offsets of each passage; items with an empty or negative range are skipped
 * @returns {() => void} removes every mark and merges the split text nodes,
 *   leaving the DOM as it was
 */
export function highlightAnchors(root, body, items) {
  const marks = [];
  const text = String(body ?? "");
  const list = (items || []).filter((it) => it && Number.isFinite(it.start) && Number.isFinite(it.end) &&
    it.start >= 0 && it.end > it.start);
  if (root && list.length) {
    const doc = root.ownerDocument;
    const blocks = [...root.querySelectorAll("[data-sp]")];
    if (root.hasAttribute && root.hasAttribute("data-sp")) blocks.unshift(root);
    const aligned = new Map();
    for (const item of list) {
      for (const block of blocks) {
        const sp = spOf(block);
        if (!sp || !(item.start < sp.end && item.end > sp.start)) continue;
        let map = aligned.get(block);
        if (!map) {
          map = alignRendered(text.slice(sp.start, sp.end), block.textContent);
          aligned.set(block, map);
        }
        const covered = coveredUnits(map, item.start - sp.start, item.end - sp.start);
        let base = 0;
        const segments = [];
        for (const node of textNodes(block)) {
          const len = node.length;
          const parent = node.parentElement;
          if (parent && blockOf(root, node) === block && !STRUCTURAL.has(parent.tagName)) {
            let r = 0;
            while (r < len) {
              if (!covered[base + r]) {
                r++;
                continue;
              }
              let q = r;
              while (q < len && covered[base + q]) q++;
              segments.push({ node, from: r, to: q });
              r = q;
            }
          }
          base += len;
        }
        // Later segments of a node are wrapped first so earlier offsets stay valid.
        for (let k = segments.length - 1; k >= 0; k--) {
          const { node, from, to } = segments[k];
          let target = node;
          if (to < target.length) target.splitText(to);
          if (from > 0) target = target.splitText(from);
          const mark = doc.createElement("mark");
          mark.className = item.active ? "comment-anchor is-active" : "comment-anchor";
          mark.setAttribute("data-comment-id", String(item.id));
          target.parentNode.insertBefore(mark, target);
          mark.appendChild(target);
          marks.push(mark);
        }
      }
    }
  }
  return () => {
    const parents = new Set();
    for (let k = marks.length - 1; k >= 0; k--) {
      const mark = marks[k];
      const parent = mark.parentNode;
      if (!parent) continue;
      while (mark.firstChild) parent.insertBefore(mark.firstChild, mark);
      parent.removeChild(mark);
      parents.add(parent);
    }
    for (const parent of parents) parent.normalize();
    marks.length = 0;
  };
}
