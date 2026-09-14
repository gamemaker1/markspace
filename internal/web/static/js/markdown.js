// Markdown renderer for the offline client.
//
// The renderer parses the source into blocks and inline nodes and builds the
// HTML only from escaped text and a fixed set of tags and attributes. Raw
// HTML in the source comes out as escaped text. Class names match the server
// renderer so one stylesheet serves both.

const ESCAPES = { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" };

/**
 * Escapes text for use in HTML content and quoted attribute values.
 * @param {any} s
 * @returns {string}
 */
export function escapeHtml(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ESCAPES[c]);
}

// ---------------------------------------------------------------------------
// URLs

const ALLOWED_SCHEMES = new Set(["http", "https", "mailto", "markspace-pending"]);

/**
 * Returns url when it is relative or uses http, https, mailto or
 * markspace-pending, and "#" otherwise. Control characters are removed.
 * @param {string} url
 * @returns {string}
 */
export function safeUrl(url) {
  const raw = String(url ?? "").replace(/[\u0000-\u001f\u007f]/g, "").trim();
  if (!raw) return "";
  const probe = raw.replace(/[\s\u0080-\u009f]/g, "");
  const m = /^([^:/?#]*):/.exec(probe);
  if (!m) return raw;
  const scheme = m[1].toLowerCase();
  if (!/^[a-z][a-z0-9+.-]*$/.test(scheme) || !ALLOWED_SCHEMES.has(scheme)) return "#";
  return raw;
}

/**
 * Reports whether an already safe href leaves the site.
 * @param {string} href
 * @returns {boolean}
 */
function isExternal(href) {
  return /^(?:https?:)?\/\//i.test(href);
}

/**
 * Builds the attributes of an anchor from a safe href.
 * @param {string} href
 * @param {string} [title]
 * @param {string} [cls]
 * @returns {string}
 */
function anchorAttrs(href, title, cls) {
  let out = ` href="${escapeHtml(href)}"`;
  if (cls) out += ` class="${escapeHtml(cls)}"`;
  if (title) out += ` title="${escapeHtml(title)}"`;
  if (isExternal(href)) out += ` rel="noopener noreferrer nofollow" target="_blank"`;
  return out;
}

// ---------------------------------------------------------------------------
// Entities and escapes

const NAMED_ENTITIES = {
  amp: "&", lt: "<", gt: ">", quot: '"', apos: "'", nbsp: "\u00a0", copy: "\u00a9",
  reg: "\u00ae", trade: "\u2122", hellip: "\u2026", mdash: "\u2014", ndash: "\u2013",
  laquo: "\u00ab", raquo: "\u00bb", lsquo: "\u2018", rsquo: "\u2019", ldquo: "\u201c",
  rdquo: "\u201d", middot: "\u00b7", times: "\u00d7", divide: "\u00f7", deg: "\u00b0",
  euro: "\u20ac", pound: "\u00a3", yen: "\u00a5", cent: "\u00a2", sect: "\u00a7",
  para: "\u00b6", bull: "\u2022", larr: "\u2190", rarr: "\u2192", uarr: "\u2191", darr: "\u2193",
};

const ENTITY_RE = /&(?:#[xX]([0-9a-fA-F]{1,6})|#([0-9]{1,7})|([A-Za-z][A-Za-z0-9]{1,31}));/y;

/**
 * Decodes the entity that starts at position i of s, the way the renderer
 * decodes entities in text.
 * @param {string} s
 * @param {number} i index of "&"
 * @returns {{text: string, length: number}|null} the decoded text and the
 *   length of the entity in s, or null when no known entity starts at i
 */
export function decodeEntityAt(s, i) {
  ENTITY_RE.lastIndex = i;
  const m = ENTITY_RE.exec(s);
  if (!m) return null;
  if (m[3]) {
    const v = NAMED_ENTITIES[m[3]];
    return v === undefined ? null : { text: v, length: m[0].length };
  }
  let cp = m[1] ? parseInt(m[1], 16) : parseInt(m[2], 10);
  if (!cp || cp > 0x10ffff || (cp >= 0xd800 && cp <= 0xdfff)) cp = 0xfffd;
  return { text: String.fromCodePoint(cp), length: m[0].length };
}

const ASCII_PUNCT = /[!-/:-@[-`{-~]/;

/**
 * Applies backslash escapes and entities to a link destination or title.
 * @param {string} s
 * @returns {string}
 */
function unescapeString(s) {
  let out = "";
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (c === "\\" && i + 1 < s.length && ASCII_PUNCT.test(s[i + 1])) {
      out += s[i + 1];
      i++;
    } else if (c === "&") {
      const e = decodeEntityAt(s, i);
      if (e) {
        out += e.text;
        i += e.length - 1;
      } else out += c;
    } else out += c;
  }
  return out;
}

// ---------------------------------------------------------------------------
// Slugs and heading ids

const RESERVED_NAMES = new Set([
  "con", "prn", "aux", "nul", "com1", "com2", "com3", "com4",
  "lpt1", "lpt2", "lpt3", "lpt4", ".", "..", "index",
]);
const GO_SPACE = /^[\t\n\v\f\r \u0085\u00a0\p{Z}]$/u;
const LETTER_OR_DIGIT = /^[\p{L}\p{Nd}]$/u;

/**
 * Lowercases one code point the way Go's unicode.ToLower does, which never
 * expands one rune into two.
 * @param {string} ch
 * @returns {string}
 */
function lowerRune(ch) {
  const lowered = Array.from(ch.toLowerCase());
  return lowered.length === 1 ? lowered[0] : lowered[0] ?? ch;
}

/**
 * Converts a title into a slug exactly as core.Slugify does: letters and
 * digits from any script are kept, other runs fold to one hyphen, the result
 * stops at 80 runes, an empty result is "untitled" and a reserved name gets
 * "-note" appended.
 * @param {string} title
 * @returns {string}
 */
export function slugify(title) {
  const runes = Array.from(String(title ?? ""));
  let start = 0;
  let end = runes.length;
  while (start < end && GO_SPACE.test(runes[start])) start++;
  while (end > start && GO_SPACE.test(runes[end - 1])) end--;
  let out = "";
  let lastHyphen = false;
  let count = 0;
  for (let i = start; i < end; i++) {
    if (count >= 80) break;
    const r = lowerRune(runes[i]);
    if (LETTER_OR_DIGIT.test(r)) {
      out += r;
      lastHyphen = false;
      count++;
    } else if (r === "_") {
      out += "_";
      lastHyphen = false;
      count++;
    } else if (!lastHyphen && out.length > 0) {
      out += "-";
      lastHyphen = true;
      count++;
    }
  }
  out = out.replace(/^-+/, "").replace(/-+$/, "");
  if (out === "") return "untitled";
  if (RESERVED_NAMES.has(out)) return out + "-note";
  return out;
}

/**
 * Builds a heading id the way goldmark's automatic heading ids do: ASCII
 * letters and digits lowercased, spaces, hyphens and underscores as hyphens,
 * everything else dropped, and a numeric suffix for a repeated id.
 * @param {string} text
 * @param {Map<string, boolean>} used
 * @returns {string}
 */
function headingId(text, used) {
  let id = "";
  for (const ch of text.trim()) {
    if (/^[A-Za-z0-9]$/.test(ch)) id += ch.toLowerCase();
    else if (ch === " " || ch === "\t" || ch === "\n" || ch === "-" || ch === "_") id += "-";
  }
  if (!id) id = "heading";
  if (!used.has(id)) {
    used.set(id, true);
    return id;
  }
  for (let i = 1; ; i++) {
    const next = `${id}-${i}`;
    if (!used.has(next)) {
      used.set(next, true);
      return next;
    }
  }
}

// ---------------------------------------------------------------------------
// Block parsing

const MAX_DEPTH = 24;
const RE_FENCE = /^( {0,3})(`{3,}|~{3,})(.*)$/;
const RE_ATX = /^ {0,3}(#{1,6})(?:[ \t]+(.*?))?[ \t]*$/;
const RE_HR = /^ {0,3}(?:(?:\*[ \t]*){3,}|(?:-[ \t]*){3,}|(?:_[ \t]*){3,})$/;
const RE_QUOTE = /^ {0,3}>/;
const RE_SETEXT = /^ {0,3}(=+|-+)[ \t]*$/;
const RE_BULLET = /^( {0,3})([-+*])([ \t]+|$)/;
const RE_ORDERED = /^( {0,3})(\d{1,9})([.)])([ \t]+|$)/;
const RE_REFDEF = /^ {0,3}\[((?:[^\]\\^]|\\.)(?:[^\]\\]|\\.){0,998})\]:[ \t]*(<[^>\n]*>|\S+)(?:[ \t]+("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|\((?:[^)\\]|\\.)*\)))?[ \t]*$/;
const RE_FOOTDEF = /^ {0,3}\[\^([^\]\s]+)\]:[ \t]?(.*)$/;

/**
 * Reports whether a line holds only whitespace.
 * @param {string} line
 * @returns {boolean}
 */
function isBlank(line) {
  return /^[ \t]*$/.test(line);
}

/**
 * Counts the leading spaces of a line.
 * @param {string} line
 * @returns {number}
 */
function indentOf(line) {
  let n = 0;
  while (n < line.length && line[n] === " ") n++;
  return n;
}

/**
 * Expands tabs in the leading whitespace of a line to four-column stops.
 * @param {string} line
 * @returns {string}
 */
function expandIndent(line) {
  if (!line.includes("\t")) return line;
  let col = 0;
  let i = 0;
  let out = "";
  for (; i < line.length; i++) {
    if (line[i] === " ") {
      out += " ";
      col++;
    } else if (line[i] === "\t") {
      const width = 4 - (col % 4);
      out += " ".repeat(width);
      col += width;
    } else break;
  }
  return out + line.slice(i);
}

// ---------------------------------------------------------------------------
// Source maps
//
// With the sourceMap option every line the block parser handles has a line
// map beside it. A line map is either a number, the offset in the original
// markdown of the line's index 0 when each later index follows linearly, or
// an Int32Array holding the original offset of every index of the line plus
// one entry for its end. Arrays appear only after tab expansion.

/**
 * Returns the original offset of index k of a line.
 * @param {number|Int32Array} map
 * @param {number} k
 * @returns {number}
 */
function mapAt(map, k) {
  return typeof map === "number" ? map + k : map[Math.min(k, map.length - 1)];
}

/**
 * Returns the line map of line.slice(k).
 * @param {number|Int32Array} map
 * @param {string} line
 * @param {number} k
 * @returns {number|Int32Array}
 */
function sliceMap(map, line, k) {
  const at = Math.max(0, Math.min(k, line.length));
  return typeof map === "number" ? map + at : map.subarray(Math.min(at, map.length - 1));
}

/**
 * Returns the line map of expandIndent(line).
 * @param {string} line
 * @param {number|Int32Array} map
 * @returns {number|Int32Array}
 */
function expandIndentMap(line, map) {
  const expanded = expandIndent(line);
  if (expanded === line) return map;
  const out = new Int32Array(expanded.length + 1);
  let col = 0;
  let i = 0;
  for (; i < line.length; i++) {
    if (line[i] === " ") out[col++] = mapAt(map, i);
    else if (line[i] === "\t") {
      const width = 4 - (col % 4);
      for (let w = 0; w < width; w++) out[col++] = mapAt(map, i);
    } else break;
  }
  for (; i <= line.length; i++) out[col++] = mapAt(map, i);
  return out;
}

/**
 * Returns the offset in src of the start of every line, splitting on CRLF,
 * CR and LF the way parseDocument does.
 * @param {string} src
 * @returns {number[]}
 */
function lineOrigins(src) {
  const out = [0];
  const re = /\r\n?|\n/g;
  for (let m = re.exec(src); m; m = re.exec(src)) out.push(m.index + m[0].length);
  return out;
}

/**
 * Moves an offset at the end of a line past the line break that follows it.
 * @param {string} src
 * @param {number} e
 * @returns {number}
 */
function pastBreak(src, e) {
  if (src[e] === "\r") e++;
  if (src[e] === "\n") e++;
  return e;
}

/**
 * Returns the smallest start and largest end of the blocks that carry a
 * source range, or null when none does.
 * @param {object[]} blocks
 * @returns {number[]|null}
 */
function spanOf(blocks) {
  let s = Infinity;
  let e = -Infinity;
  for (const b of blocks) {
    if (!b.sp) continue;
    s = Math.min(s, b.sp[0]);
    e = Math.max(e, b.sp[1]);
  }
  return s === Infinity ? null : [s, e];
}

/**
 * Normalizes a reference label for lookup.
 * @param {string} label
 * @returns {string}
 */
function normalizeLabel(label) {
  return label.trim().replace(/\s+/g, " ").toLowerCase();
}

/**
 * Matches an opening code fence.
 * @param {string} line
 * @returns {{indent: number, fence: string, info: string}|null}
 */
function fenceOpen(line) {
  const m = RE_FENCE.exec(line);
  if (!m) return null;
  if (m[2][0] === "`" && m[3].includes("`")) return null;
  return { indent: m[1].length, fence: m[2], info: m[3].trim() };
}

/**
 * Matches a list marker at the start of a line.
 * @param {string} line
 * @returns {{ordered: boolean, bullet?: string, delim?: string, start: number, contentIndent: number, rest: string, restStart: number}|null}
 */
function listMarker(line) {
  let m = RE_BULLET.exec(line);
  let ordered = false;
  let width;
  let spaces;
  if (m) {
    width = m[1].length + 1;
    spaces = m[3];
  } else {
    m = RE_ORDERED.exec(line);
    if (!m) return null;
    ordered = true;
    width = m[1].length + m[2].length + 1;
    spaces = m[4];
  }
  const contentIndent = spaces.length === 0 || spaces.length > 4 ? width + 1 : width + spaces.length;
  const rest = line.length > contentIndent ? line.slice(contentIndent) : "";
  return {
    ordered,
    bullet: ordered ? undefined : m[2],
    delim: ordered ? m[3] : undefined,
    start: ordered ? parseInt(m[2], 10) : 1,
    contentIndent,
    rest: spaces.length > 4 ? line.slice(width + 1) : rest,
    restStart: spaces.length > 4 ? width + 1 : Math.min(contentIndent, line.length),
  };
}

/**
 * Reports whether a line starts a block that interrupts a paragraph.
 * @param {string} line
 * @returns {boolean}
 */
function interruptsParagraph(line) {
  if (indentOf(line) >= 4) return false;
  if (fenceOpen(line) || RE_ATX.test(line) || RE_HR.test(line) || RE_QUOTE.test(line)) return true;
  const mk = listMarker(line);
  return Boolean(mk && mk.rest.trim() !== "" && (!mk.ordered || mk.start === 1));
}

/**
 * Splits a table row into trimmed cells. An escaped pipe stays in the cell.
 * @param {string} line
 * @returns {string[]}
 */
function splitRow(line) {
  let s = line.trim();
  if (s.startsWith("|")) s = s.slice(1);
  if (s.endsWith("|") && !s.endsWith("\\|")) s = s.slice(0, -1);
  const cells = [];
  let cur = "";
  for (let i = 0; i < s.length; i++) {
    if (s[i] === "\\" && s[i + 1] === "|") {
      cur += "|";
      i++;
    } else if (s[i] === "|") {
      cells.push(cur.trim());
      cur = "";
    } else cur += s[i];
  }
  cells.push(cur.trim());
  return cells;
}

/**
 * Returns the [start, end) index in line of every cell splitRow returns,
 * without the surrounding whitespace.
 * @param {string} line
 * @returns {number[][]}
 */
function splitRowSpans(line) {
  let from = line.length - line.trimStart().length;
  let to = line.trimEnd().length;
  if (line[from] === "|" && from < to) from++;
  if (to > from && line[to - 1] === "|" && line[to - 2] !== "\\") to--;
  const spans = [];
  const push = (a, b) => {
    const cell = line.slice(a, b);
    const lead = cell.length - cell.trimStart().length;
    spans.push(lead === cell.length ? [a, a] : [a + lead, a + cell.trimEnd().length]);
  };
  let cellStart = from;
  for (let i = from; i < to; i++) {
    if (line[i] === "\\" && line[i + 1] === "|") i++;
    else if (line[i] === "|") {
      push(cellStart, i);
      cellStart = i + 1;
    }
  }
  push(cellStart, to);
  return spans;
}

/**
 * Parses a table delimiter row into column alignments.
 * @param {string} line
 * @returns {string[]|null}
 */
function delimiterRow(line) {
  if (!line.includes("|") && !/^ {0,3}:?-+:?[ \t]*$/.test(line)) return null;
  if (indentOf(line) >= 4) return null;
  const cells = splitRow(line);
  const align = [];
  for (const c of cells) {
    if (!/^:?-+:?$/.test(c)) return null;
    align.push(c.startsWith(":") && c.endsWith(":") ? "center" : c.endsWith(":") ? "right" : c.startsWith(":") ? "left" : "");
  }
  return align;
}

/**
 * Reports whether lines[i] is a table header followed by a delimiter row.
 * @param {string[]} lines
 * @param {number} i
 * @returns {string[]|null} the alignments when a table starts
 */
function tableStart(lines, i) {
  if (i + 1 >= lines.length || !lines[i].includes("|") || indentOf(lines[i]) >= 4) return null;
  const align = delimiterRow(lines[i + 1]);
  if (!align || splitRow(lines[i]).length !== align.length) return null;
  return align;
}

/**
 * Removes link reference and footnote definitions from the top level of the
 * document and records them in ctx.
 * @param {string[]} lines
 * @param {object} ctx
 * @param {(number|Int32Array)[]} [maps] line maps of lines, with the sourceMap option
 * @param {(number|Int32Array)[]} [outMaps] receives the line maps of the result
 * @returns {string[]}
 */
function collectDefinitions(lines, ctx, maps, outMaps) {
  const out = [];
  const emit = (line, map) => {
    out.push(line);
    if (maps) outMaps.push(map);
  };
  let fence = null;
  let prevAllows = true;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const map = maps ? maps[i] : 0;
    if (fence) {
      emit(line, map);
      const m = RE_FENCE.exec(line);
      if (m && m[2][0] === fence[0] && m[2].length >= fence.length && m[3].trim() === "") fence = null;
      continue;
    }
    const open = fenceOpen(line);
    if (open) {
      fence = open.fence;
      emit(line, map);
      prevAllows = false;
      continue;
    }
    let m = prevAllows ? RE_FOOTDEF.exec(line) : null;
    if (m) {
      const body = [m[2]];
      const bodyMaps = maps ? [sliceMap(map, line, line.length - m[2].length)] : null;
      let j = i + 1;
      while (j < lines.length) {
        const l = lines[j];
        let k = -1;
        if (indentOf(l) >= 4) k = 4;
        else if (isBlank(l) && j + 1 < lines.length && indentOf(lines[j + 1]) >= 4) k = l.length;
        else if (!isBlank(l) && !isBlank(lines[j - 1]) && !interruptsParagraph(l) && !RE_FOOTDEF.test(l)) k = 0;
        else break;
        body.push(k === 4 ? l.slice(4) : k === 0 ? l : "");
        if (maps) bodyMaps.push(sliceMap(maps[j], l, k));
        j++;
      }
      if (!ctx.footnotes.has(m[1])) {
        ctx.footnotes.set(m[1], body);
        if (maps) ctx.footnoteMaps.set(m[1], bodyMaps);
      }
      for (let k = i; k < j; k++) emit("", maps ? maps[k] : 0);
      i = j - 1;
      prevAllows = true;
      continue;
    }
    m = prevAllows ? RE_REFDEF.exec(line) : null;
    if (m) {
      const key = normalizeLabel(m[1]);
      let dest = m[2];
      if (dest.startsWith("<")) dest = dest.slice(1, -1);
      const title = m[3] ? m[3].slice(1, -1) : "";
      if (key && !ctx.refs.has(key)) ctx.refs.set(key, { dest: unescapeString(dest), title: unescapeString(title) });
      emit("", map);
      prevAllows = true;
      continue;
    }
    emit(line, map);
    prevAllows = isBlank(line);
  }
  return out;
}

/**
 * Parses lines into block nodes. With line maps every block gets sp, its
 * [start, end) range in the original markdown.
 * @param {string[]} lines
 * @param {number} depth container nesting depth
 * @param {(number|Int32Array)[]} [maps] line maps of lines
 * @param {string} [src] the original markdown, with maps
 * @returns {object[]}
 */
function parseBlocks(lines, depth, maps, src) {
  const out = [];
  let i = 0;
  const n = lines.length;
  while (i < n) {
    const line = lines[i];
    if (isBlank(line)) {
      i++;
      continue;
    }
    const open = fenceOpen(line);
    if (open) {
      const body = [];
      const bodyMaps = maps ? [] : null;
      const openLine = i;
      i++;
      while (i < n) {
        const m = RE_FENCE.exec(lines[i]);
        if (m && m[2][0] === open.fence[0] && m[2].length >= open.fence.length && m[3].trim() === "") {
          i++;
          break;
        }
        let l = lines[i];
        let strip = 0;
        while (strip < open.indent && l[strip] === " ") strip++;
        body.push(l.slice(strip));
        if (maps) bodyMaps.push(sliceMap(maps[i], l, strip));
        i++;
      }
      const lang = (open.info.split(/\s+/)[0] || "").replace(/[^A-Za-z0-9_+#.-]/g, "");
      const block = { type: "code", lang, text: body.length ? body.join("\n") + "\n" : "" };
      if (maps) {
        if (body.length) {
          const last = body.length - 1;
          block.sp = [mapAt(bodyMaps[0], 0), pastBreak(src, mapAt(bodyMaps[last], body[last].length))];
        } else {
          const e = pastBreak(src, mapAt(maps[openLine], lines[openLine].length));
          block.sp = [e, e];
        }
      }
      out.push(block);
      continue;
    }
    if (indentOf(line) >= 4) {
      const body = [];
      const bodyMaps = maps ? [] : null;
      while (i < n && (isBlank(lines[i]) || indentOf(lines[i]) >= 4)) {
        body.push(lines[i].slice(4));
        if (maps) bodyMaps.push(sliceMap(maps[i], lines[i], 4));
        i++;
      }
      while (body.length && isBlank(body[body.length - 1])) body.pop();
      const block = { type: "code", lang: "", text: body.join("\n") + "\n" };
      if (maps) {
        const last = body.length - 1;
        block.sp = [mapAt(bodyMaps[0], 0), pastBreak(src, mapAt(bodyMaps[last], body[last].length))];
      }
      out.push(block);
      continue;
    }
    let m = RE_ATX.exec(line);
    if (m) {
      let text = m[2] || "";
      text = text.replace(/(?:^|[ \t]+)#+[ \t]*$/, "");
      const block = { type: "heading", level: m[1].length, text: text.trim() };
      if (maps) {
        const from = /^ {0,3}#{1,6}[ \t]*/.exec(line)[0].length + (text.length - text.trimStart().length);
        block.sp = [mapAt(maps[i], from), mapAt(maps[i], from + block.text.length)];
      }
      out.push(block);
      i++;
      continue;
    }
    if (RE_HR.test(line)) {
      const block = { type: "hr" };
      if (maps) block.sp = [mapAt(maps[i], 0), mapAt(maps[i], line.length)];
      out.push(block);
      i++;
      continue;
    }
    if (RE_QUOTE.test(line) && depth < MAX_DEPTH) {
      const body = [];
      const bodyMaps = maps ? [] : null;
      const firstLine = i;
      while (i < n) {
        const l = lines[i];
        if (RE_QUOTE.test(l)) {
          const stripped = l.replace(/^ {0,3}> ?/, "");
          body.push(expandIndent(stripped));
          if (maps) bodyMaps.push(expandIndentMap(stripped, sliceMap(maps[i], l, l.length - stripped.length)));
        } else if (!isBlank(l) && body.length && !isBlank(body[body.length - 1]) && !interruptsParagraph(l)) {
          body.push(l);
          if (maps) bodyMaps.push(maps[i]);
        } else break;
        i++;
      }
      const block = { type: "quote", children: parseBlocks(body, depth + 1, bodyMaps || undefined, src) };
      if (maps) block.sp = spanOf(block.children) || [mapAt(maps[firstLine], 0), mapAt(maps[firstLine], lines[firstLine].length)];
      out.push(block);
      continue;
    }
    const mk = listMarker(line);
    if (mk && !RE_HR.test(line) && depth < MAX_DEPTH) {
      i = parseList(lines, i, depth, out, maps, src);
      continue;
    }
    const align = tableStart(lines, i);
    if (align) {
      const head = splitRow(line);
      const rows = [];
      const block = { type: "table", align, head, rows };
      const cellSpans = (k, count) => {
        const spans = splitRowSpans(lines[k]).slice(0, count).map(([a, b]) => [mapAt(maps[k], a), mapAt(maps[k], b)]);
        const endOfLine = mapAt(maps[k], lines[k].length);
        while (spans.length < count) spans.push([endOfLine, endOfLine]);
        return spans;
      };
      if (maps) {
        block.headSp = cellSpans(i, head.length);
        block.rowSp = [];
      }
      const firstLine = i;
      i += 2;
      while (i < n && !isBlank(lines[i]) && !interruptsParagraph(lines[i])) {
        const cells = splitRow(lines[i]).slice(0, align.length);
        while (cells.length < align.length) cells.push("");
        rows.push(cells);
        if (maps) block.rowSp.push(cellSpans(i, align.length));
        i++;
      }
      if (maps) block.sp = [mapAt(maps[firstLine], 0), mapAt(maps[i - 1], lines[i - 1].length)];
      out.push(block);
      continue;
    }
    const para = [line.trim()];
    const firstMap = maps ? sliceMap(maps[i], line, line.length - line.trimStart().length) : 0;
    let lastMap = firstMap;
    i++;
    let setext = 0;
    while (i < n) {
      const l = lines[i];
      if (isBlank(l)) break;
      const s = RE_SETEXT.exec(l);
      if (s) {
        setext = s[1][0] === "=" ? 1 : 2;
        i++;
        break;
      }
      if (interruptsParagraph(l) || tableStart(lines, i)) break;
      const stripped = l.replace(/^[ \t]+/, "");
      para.push(stripped);
      if (maps) lastMap = sliceMap(maps[i], l, l.length - stripped.length);
      i++;
    }
    const text = para.join("\n").replace(/[ \t]+$/, "");
    const block = setext ? { type: "heading", level: setext, text: text.trim() } : { type: "para", text };
    if (maps) {
      const lastText = para[para.length - 1].replace(/[ \t]+$/, "");
      block.sp = [mapAt(firstMap, 0), mapAt(lastMap, lastText.length)];
      block.spMap = firstMap;
    }
    out.push(block);
  }
  return out;
}

/**
 * Parses one list starting at lines[i] and appends it to out.
 * @param {string[]} lines
 * @param {number} i
 * @param {number} depth
 * @param {object[]} out
 * @param {(number|Int32Array)[]} [maps] line maps of lines
 * @param {string} [src] the original markdown, with maps
 * @returns {number} the index after the list
 */
function parseList(lines, i, depth, out, maps, src) {
  const first = listMarker(lines[i]);
  const list = { type: "list", ordered: first.ordered, start: first.start, tight: true, items: [] };
  const n = lines.length;
  const sameList = (mk) =>
    mk && mk.ordered === first.ordered && (mk.ordered ? mk.delim === first.delim : mk.bullet === first.bullet);
  while (i < n) {
    const mk = listMarker(lines[i]);
    if (!sameList(mk) || RE_HR.test(lines[i])) break;
    const body = [mk.rest];
    const bodyMaps = maps ? [sliceMap(maps[i], lines[i], mk.restStart)] : null;
    i++;
    while (i < n) {
      const l = lines[i];
      if (isBlank(l)) {
        body.push("");
        if (maps) bodyMaps.push(sliceMap(maps[i], l, l.length));
        i++;
        continue;
      }
      if (indentOf(l) >= mk.contentIndent) {
        body.push(l.slice(mk.contentIndent));
        if (maps) bodyMaps.push(sliceMap(maps[i], l, mk.contentIndent));
        i++;
        continue;
      }
      const prev = body[body.length - 1];
      if (isBlank(prev) || listMarker(l) || interruptsParagraph(l)) break;
      body.push(l);
      if (maps) bodyMaps.push(maps[i]);
      i++;
    }
    let trailingBlank = false;
    while (body.length > 1 && isBlank(body[body.length - 1])) {
      body.pop();
      if (maps) bodyMaps.pop();
      trailingBlank = true;
    }
    const children = parseBlocks(body, depth + 1, bodyMaps || undefined, src);
    if (children.length > 1 && body.some((l, k) => k > 0 && isBlank(l))) {
      const inner = body.slice(1);
      const blankOutsideCode = inner.some(isBlank) && !(children.length === 2 && children[1].type === "code" && !isBlank(body[1]));
      if (blankOutsideCode) list.tight = false;
    }
    let task = null;
    if (children[0] && children[0].type === "para") {
      const t = /^\[([ xX])\](?:[ \t]+|$)/.exec(children[0].text);
      if (t && children[0].text.length > t[0].length) {
        task = t[1] !== " ";
        const para = { type: "para", text: children[0].text.slice(t[0].length) };
        // The rendered item starts with the checkbox and a space, so the range
        // starts right after "]" to include that space.
        if (maps) para.sp = [mapAt(children[0].spMap, 3), children[0].sp[1]];
        children[0] = para;
      }
    }
    const item = { children, task };
    if (maps) {
      const at = mapAt(bodyMaps[0], 0);
      item.sp = spanOf(children) || [at, at];
    }
    list.items.push(item);
    if (trailingBlank) {
      if (i < n && sameList(listMarker(lines[i]))) list.tight = false;
      else break;
    }
  }
  if (maps) list.sp = spanOf(list.items);
  out.push(list);
  return i;
}

// ---------------------------------------------------------------------------
// Inline parsing

/**
 * Creates an inline node.
 * @param {string} t node type
 * @param {object} [props]
 * @returns {object}
 */
function node(t, props) {
  return Object.assign({ t, prev: null, next: null }, props);
}

/** A doubly linked list of inline nodes used while parsing. */
class NodeList {
  constructor() {
    this.head = null;
    this.tail = null;
  }
  append(n) {
    n.prev = this.tail;
    n.next = null;
    if (this.tail) this.tail.next = n;
    else this.head = n;
    this.tail = n;
  }
  insertAfter(ref, n) {
    n.prev = ref;
    n.next = ref.next;
    if (ref.next) ref.next.prev = n;
    else this.tail = n;
    ref.next = n;
  }
  remove(n) {
    if (n.prev) n.prev.next = n.next;
    else this.head = n.next;
    if (n.next) n.next.prev = n.prev;
    else this.tail = n.prev;
    n.prev = null;
    n.next = null;
  }
  toArray(from) {
    const out = [];
    for (let n = from === undefined ? this.head : from; n; n = n.next) out.push(n);
    return out;
  }
}

const RE_AUTOLINK = /<([A-Za-z][A-Za-z0-9+.-]{1,31}:[^\s<>]*)>/y;
const RE_EMAIL = /<([A-Za-z0-9.!#$%&'*+/=?^_`{|}~-]+@[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*)>/y;
const RE_WIKI = /\[\[([^\[\]\n|]+)(?:\|([^\[\]\n]*))?\]\]/y;
const RE_FOOTREF = /\[\^([^\]\s]+)\]/y;
const RE_TAG = /#\p{L}[\p{L}\p{N}_\/-]{0,49}/uy;
const RE_WWW = /(?:https?:\/\/|www\.)[A-Za-z0-9_-][^\s<]*/iy;
const RE_WS = /^\s$/u;
const RE_PUNCT = /^[\p{P}\p{S}]$/u;
const RE_TAG_RUNE = /^[\p{L}\p{N}_\/-]$/u;
const RE_TAG_BLOCKER = /^[\p{L}\p{N}\-_\/#:=&?.%@]$/u;

/**
 * Returns the code point that ends just before index i, or "\n" at the start.
 * @param {string} s
 * @param {number} i
 * @returns {string}
 */
function charBefore(s, i) {
  if (i <= 0) return "\n";
  const low = s.charCodeAt(i - 1);
  if (low >= 0xdc00 && low <= 0xdfff && i >= 2) {
    const high = s.charCodeAt(i - 2);
    if (high >= 0xd800 && high <= 0xdbff) return s.slice(i - 2, i);
  }
  return s[i - 1];
}

/**
 * Returns the code point starting at index i, or "\n" past the end.
 * @param {string} s
 * @param {number} i
 * @returns {string}
 */
function charAt(s, i) {
  if (i >= s.length) return "\n";
  return String.fromCodePoint(s.codePointAt(i));
}

/**
 * Parses the "(destination title)" part of an inline link.
 * @param {string} s
 * @param {number} j index of "("
 * @returns {{dest: string, title: string, end: number}|null}
 */
function parseLinkTail(s, j) {
  let k = j + 1;
  const skipSpace = () => {
    while (k < s.length && (s[k] === " " || s[k] === "\t" || s[k] === "\n")) k++;
  };
  skipSpace();
  let dest = "";
  if (s[k] === "<") {
    const close = s.indexOf(">", k + 1);
    if (close < 0) return null;
    dest = s.slice(k + 1, close);
    if (/[\n<]/.test(dest)) return null;
    k = close + 1;
  } else {
    let depth = 0;
    const startDest = k;
    while (k < s.length) {
      const c = s[k];
      if (c === "\\" && k + 1 < s.length && ASCII_PUNCT.test(s[k + 1])) {
        k += 2;
        continue;
      }
      if (c === " " || c === "\t" || c === "\n" || c.charCodeAt(0) < 0x20) break;
      if (c === "(") {
        depth++;
        if (depth > 32) return null;
      } else if (c === ")") {
        if (depth === 0) break;
        depth--;
      }
      k++;
    }
    if (depth !== 0) return null;
    dest = s.slice(startDest, k);
  }
  const beforeTitle = k;
  skipSpace();
  let title = "";
  const q = s[k];
  if ((q === '"' || q === "'" || q === "(") && k > beforeTitle) {
    const closeChar = q === "(" ? ")" : q;
    let e = k + 1;
    while (e < s.length && s[e] !== closeChar) {
      if (s[e] === "\\") e++;
      e++;
    }
    if (e >= s.length) return null;
    title = s.slice(k + 1, e);
    k = e + 1;
    skipSpace();
  }
  if (s[k] !== ")") return null;
  return { dest: unescapeString(dest), title: unescapeString(title), end: k + 1 };
}

/**
 * Parses inline markdown into a tree of nodes.
 * @param {string} src
 * @param {object} ctx
 * @returns {object[]}
 */
function parseInline(src, ctx) {
  const list = new NodeList();
  const brackets = [];
  let text = "";
  const flush = () => {
    if (text) {
      list.append(node("text", { v: text }));
      text = "";
    }
  };
  let i = 0;
  const len = src.length;
  while (i < len) {
    const c = src[i];
    switch (c) {
      case "\\": {
        const next = src[i + 1];
        if (next === "\n") {
          flush();
          list.append(node("br"));
          i += 2;
          while (src[i] === " " || src[i] === "\t") i++;
        } else if (next !== undefined && ASCII_PUNCT.test(next)) {
          text += next;
          i += 2;
        } else {
          text += "\\";
          i++;
        }
        break;
      }
      case "`": {
        let run = 1;
        while (src[i + run] === "`") run++;
        let j = i + run;
        let found = -1;
        while (j < len) {
          const k = src.indexOf("`", j);
          if (k < 0) break;
          let r = 1;
          while (src[k + r] === "`") r++;
          if (r === run) {
            found = k;
            break;
          }
          j = k + r;
        }
        if (found < 0) {
          text += "`".repeat(run);
          i += run;
        } else {
          let code = src.slice(i + run, found).replace(/\n/g, " ");
          if (code.length > 2 && code[0] === " " && code[code.length - 1] === " " && code.trim() !== "") {
            code = code.slice(1, -1);
          }
          flush();
          list.append(node("code", { v: code }));
          i = found + run;
        }
        break;
      }
      case "&": {
        const e = decodeEntityAt(src, i);
        if (e) {
          text += e.text;
          i += e.length;
        } else {
          text += "&";
          i++;
        }
        break;
      }
      case "<": {
        RE_AUTOLINK.lastIndex = i;
        let m = RE_AUTOLINK.exec(src);
        if (m) {
          flush();
          list.append(node("autolink", { href: m[1], v: m[1] }));
          i += m[0].length;
          break;
        }
        RE_EMAIL.lastIndex = i;
        m = RE_EMAIL.exec(src);
        if (m) {
          flush();
          list.append(node("autolink", { href: "mailto:" + m[1], v: m[1] }));
          i += m[0].length;
          break;
        }
        text += "<";
        i++;
        break;
      }
      case "!":
      case "[": {
        const image = c === "!";
        const at = image ? i + 1 : i;
        if (image && src[at] !== "[") {
          text += "!";
          i++;
          break;
        }
        if (src[at + 1] === "[") {
          RE_WIKI.lastIndex = at;
          const m = RE_WIKI.exec(src);
          if (m && m[1].trim()) {
            flush();
            const raw = m[1].trim();
            const hash = raw.indexOf("#");
            const target = (hash >= 0 ? raw.slice(0, hash) : raw).trim();
            const fragment = hash >= 0 ? raw.slice(hash + 1).trim() : "";
            const label = m[2] !== undefined && m[2].trim() ? m[2].trim() : raw;
            list.append(node("wikilink", { target, fragment, label, embed: image }));
            i = at + m[0].length;
            break;
          }
        }
        if (!image) {
          RE_FOOTREF.lastIndex = i;
          const m = RE_FOOTREF.exec(src);
          if (m && ctx.footnotes.has(m[1])) {
            flush();
            list.append(node("fnref", { label: m[1] }));
            i += m[0].length;
            break;
          }
        }
        flush();
        const opener = node("text", { v: image ? "![" : "[" });
        list.append(opener);
        brackets.push({ node: opener, image, pos: at, active: true });
        i = at + 1;
        break;
      }
      case "]": {
        flush();
        const opener = brackets.pop();
        if (!opener || !opener.active) {
          text += "]";
          i++;
          break;
        }
        const labelRaw = src.slice(opener.pos + 1, i);
        let dest = null;
        let title = "";
        let end = i + 1;
        if (src[i + 1] === "(") {
          const tail = parseLinkTail(src, i + 1);
          if (tail) {
            dest = tail.dest;
            title = tail.title;
            end = tail.end;
          }
        }
        if (dest === null) {
          let label = labelRaw;
          let after = i + 1;
          if (src[i + 1] === "[") {
            const close = src.indexOf("]", i + 2);
            if (close > 0 && close - i < 1000 && !src.slice(i + 2, close).includes("[")) {
              const inner = src.slice(i + 2, close);
              if (inner.trim()) label = inner;
              after = close + 1;
            }
          }
          const ref = ctx.refs.get(normalizeLabel(label));
          if (ref && !/^\s*\^/.test(label)) {
            dest = ref.dest;
            title = ref.title;
            end = after;
          }
        }
        if (dest === null) {
          text += "]";
          i++;
          break;
        }
        processEmphasis(list, opener.node);
        const children = list.toArray(opener.node.next);
        for (const child of children) list.remove(child);
        list.remove(opener.node);
        list.append(node(opener.image ? "image" : "link", { dest, title, children }));
        if (!opener.image) {
          for (const b of brackets) if (!b.image) b.active = false;
        }
        i = end;
        break;
      }
      case "*":
      case "_":
      case "~": {
        let run = 1;
        while (src[i + run] === c) run++;
        if (c === "~" && run > 2) {
          text += "~".repeat(run);
          i += run;
          break;
        }
        const before = charBefore(src, i);
        const after = charAt(src, i + run);
        const wsBefore = RE_WS.test(before);
        const wsAfter = RE_WS.test(after);
        const pBefore = RE_PUNCT.test(before);
        const pAfter = RE_PUNCT.test(after);
        const left = !wsAfter && (!pAfter || wsBefore || pBefore);
        const right = !wsBefore && (!pBefore || wsAfter || pAfter);
        let canOpen = left;
        let canClose = right;
        if (c === "_") {
          canOpen = left && (!right || pBefore);
          canClose = right && (!left || pAfter);
        }
        flush();
        list.append(node("delim", { ch: c, count: run, orig: run, canOpen, canClose }));
        i += run;
        break;
      }
      case "#": {
        const before = charBefore(src, i);
        if (i > 0 && RE_TAG_BLOCKER.test(before)) {
          text += "#";
          i++;
          break;
        }
        RE_TAG.lastIndex = i;
        const m = RE_TAG.exec(src);
        if (!m) {
          text += "#";
          i++;
          break;
        }
        const next = src.slice(i + m[0].length, i + m[0].length + 2);
        if (next && RE_TAG_RUNE.test(String.fromCodePoint(next.codePointAt(0)))) {
          text += m[0];
          i += m[0].length;
          break;
        }
        const tag = m[0].slice(1).replace(/[-_/]+$/, "").toLowerCase();
        if (!tag) {
          text += m[0];
          i += m[0].length;
          break;
        }
        flush();
        list.append(node("tag", { raw: m[0], tag }));
        i += m[0].length;
        break;
      }
      case "\n": {
        const hard = /  +$/.test(text);
        text = text.replace(/[ \t]+$/, "");
        flush();
        list.append(node(hard ? "br" : "softbreak"));
        i++;
        while (src[i] === " " || src[i] === "\t") i++;
        break;
      }
      case "h":
      case "H":
      case "w":
      case "W": {
        const before = charBefore(src, i);
        if (i === 0 || RE_WS.test(before) || "*_~(".includes(before)) {
          RE_WWW.lastIndex = i;
          const m = RE_WWW.exec(src);
          if (m) {
            let url = m[0];
            for (;;) {
              if (/[?!.,:*_~'";]$/.test(url)) url = url.slice(0, -1);
              else if (url.endsWith(")") && (url.match(/\)/g) || []).length > (url.match(/\(/g) || []).length) url = url.slice(0, -1);
              else break;
            }
            if (/^(?:https?:\/\/|www\.)[A-Za-z0-9_-]/i.test(url)) {
              flush();
              const href = /^www\./i.test(url) ? "http://" + url : url;
              list.append(node("autolink", { href, v: url }));
              i += url.length;
              break;
            }
          }
        }
        text += c;
        i++;
        break;
      }
      default:
        text += c;
        i++;
    }
  }
  flush();
  processEmphasis(list, null);
  return list.toArray();
}

/**
 * Turns a delimiter node into a text node in place.
 * @param {object} n
 */
function delimToText(n) {
  n.v = n.ch.repeat(n.count);
  n.t = "text";
}

/**
 * Resolves emphasis, strong emphasis and strikethrough delimiters after the
 * bottom node, following the CommonMark delimiter algorithm.
 * @param {NodeList} list
 * @param {object|null} bottom
 */
function processEmphasis(list, bottom) {
  const openersBottom = new Map();
  let closer = bottom ? bottom.next : list.head;
  while (closer) {
    if (closer.t !== "delim" || !closer.canClose) {
      closer = closer.next;
      continue;
    }
    const key = closer.ch + (closer.canOpen ? "1" : "0") + (closer.orig % 3);
    const floor = openersBottom.get(key);
    let opener = closer.prev;
    let found = null;
    while (opener && opener !== bottom && opener !== floor) {
      if (opener.t === "delim" && opener.ch === closer.ch && opener.canOpen) {
        let ok = true;
        if (closer.ch === "~") ok = opener.count === closer.count;
        else if ((opener.canClose || closer.canOpen) && (opener.orig + closer.orig) % 3 === 0 &&
          !(opener.orig % 3 === 0 && closer.orig % 3 === 0)) ok = false;
        if (ok) {
          found = opener;
          break;
        }
      }
      opener = opener.prev;
    }
    if (found) {
      const use = closer.ch === "~" ? closer.count : closer.count >= 2 && found.count >= 2 ? 2 : 1;
      const type = closer.ch === "~" ? "del" : use === 2 ? "strong" : "em";
      const children = [];
      let n = found.next;
      while (n && n !== closer) {
        const next = n.next;
        list.remove(n);
        if (n.t === "delim") delimToText(n);
        children.push(n);
        n = next;
      }
      list.insertAfter(found, node(type, { children }));
      found.count -= use;
      closer.count -= use;
      if (found.count === 0) list.remove(found);
      if (closer.count === 0) {
        const next = closer.next;
        list.remove(closer);
        closer = next;
      }
    } else {
      openersBottom.set(key, closer.prev);
      const next = closer.next;
      if (!closer.canOpen) delimToText(closer);
      closer = next;
    }
  }
  for (let n = bottom ? bottom.next : list.head; n; n = n.next) {
    if (n.t === "delim") delimToText(n);
  }
}

/**
 * Returns the plain text of inline nodes.
 * @param {object[]} nodes
 * @returns {string}
 */
function plainOf(nodes) {
  let out = "";
  for (const n of nodes) {
    switch (n.t) {
      case "text":
      case "code":
      case "autolink":
        out += n.v;
        break;
      case "wikilink":
        out += n.label;
        break;
      case "tag":
        out += n.raw;
        break;
      case "br":
      case "softbreak":
        out += " ";
        break;
      case "fnref":
        break;
      default:
        if (n.children) out += plainOf(n.children);
    }
  }
  return out;
}

// ---------------------------------------------------------------------------
// Documents

/**
 * Parses a whole document into blocks with a shared context.
 * @param {string} md
 * @param {boolean} [sourceMap] record the source range of every block
 * @returns {{blocks: object[], ctx: object}}
 */
function parseDocument(md, sourceMap = false) {
  const raw = String(md ?? "");
  const src = raw.replace(/\r\n?/g, "\n").replace(/\u0000/g, "\ufffd");
  const ctx = { refs: new Map(), footnotes: new Map() };
  const split = src.split("\n");
  if (!sourceMap) {
    const lines = collectDefinitions(split.map(expandIndent), ctx);
    return { blocks: parseBlocks(lines, 0), ctx };
  }
  const origins = lineOrigins(raw);
  const maps = split.map((line, k) => expandIndentMap(line, origins[k]));
  const outMaps = [];
  ctx.footnoteMaps = new Map();
  ctx.src = raw;
  const lines = collectDefinitions(split.map(expandIndent), ctx, maps, outMaps);
  return { blocks: parseBlocks(lines, 0, outMaps, raw), ctx };
}

/**
 * Returns the parsed inline nodes of a text-bearing block, parsing on first use.
 * @param {object} block
 * @param {string} key the property holding the source text
 * @param {object} ctx
 * @returns {object[]}
 */
function inlineOf(block, key, ctx) {
  const cacheKey = "_" + key;
  if (!block[cacheKey]) block[cacheKey] = parseInline(block[key], ctx);
  return block[cacheKey];
}

/**
 * Calls visit for every inline node in blocks, depth first.
 * @param {object[]} blocks
 * @param {object} ctx
 * @param {(n: object, inLink: boolean) => void} visit
 */
function walkInline(blocks, ctx, visit) {
  const walkNodes = (nodes, inLink) => {
    for (const n of nodes) {
      visit(n, inLink);
      if (n.children) walkNodes(n.children, inLink || n.t === "link");
    }
  };
  const walkBlocks = (list) => {
    for (const b of list) {
      switch (b.type) {
        case "para":
        case "heading":
          walkNodes(inlineOf(b, "text", ctx), false);
          break;
        case "quote":
          walkBlocks(b.children);
          break;
        case "list":
          for (const item of b.items) walkBlocks(item.children);
          break;
        case "table":
          for (const cell of b.head) walkNodes(parseInline(cell, ctx), false);
          for (const row of b.rows) for (const cell of row) walkNodes(parseInline(cell, ctx), false);
          break;
      }
    }
  };
  walkBlocks(blocks);
  for (const body of ctx.footnotes.values()) walkBlocks(parseBlocks(body, 1));
}

/**
 * Renders one document.
 * @param {object[]} blocks
 * @param {object} ctx
 * @param {object} opts
 * @returns {{html: string, toc: object[], links: object[], tasks: number, tasksDone: number}}
 */
function renderDocument(blocks, ctx, opts) {
  const state = {
    ctx,
    opts,
    ids: new Map(),
    toc: [],
    links: [],
    tasks: 0,
    tasksDone: 0,
    footnoteNumbers: new Map(),
    footnoteOrder: [],
    footnoteRefCount: new Map(),
  };
  let html = renderBlocks(blocks, state, false);
  if (state.footnoteOrder.length) {
    let items = "";
    for (let k = 0; k < state.footnoteOrder.length; k++) {
      const label = state.footnoteOrder[k];
      const num = k + 1;
      const body = opts.sourceMap && ctx.footnoteMaps
        ? parseBlocks(ctx.footnotes.get(label), 1, ctx.footnoteMaps.get(label), ctx.src)
        : parseBlocks(ctx.footnotes.get(label), 1);
      let inner = renderBlocks(body, state, false);
      const back = `<a href="#fnref:${num}" class="footnote-backref" role="doc-backlink">\u21a9\ufe0e</a>`;
      if (inner.endsWith("</p>\n")) inner = inner.slice(0, -5) + ` ${back}</p>\n`;
      else inner += `<p>${back}</p>\n`;
      items += `<li id="fn:${num}">\n${inner}</li>\n`;
    }
    html += `<div class="footnotes" role="doc-endnotes">\n<hr>\n<ol>\n${items}</ol>\n</div>\n`;
  }
  return { html, toc: state.toc, links: state.links, tasks: state.tasks, tasksDone: state.tasksDone };
}

/**
 * Formats a source range as a data-sp attribute. The value holds only digits
 * and one colon.
 * @param {number[]} range [start, end)
 * @returns {string}
 */
function spAttr(range) {
  const a = Math.max(0, Math.trunc(Number(range[0]) || 0));
  const b = Math.max(a, Math.trunc(Number(range[1]) || 0));
  return ` data-sp="${a}:${b}"`;
}

/**
 * Renders blocks to HTML.
 * @param {object[]} blocks
 * @param {object} state
 * @param {boolean} tight paragraphs render without <p> inside a tight list
 * @returns {string}
 */
function renderBlocks(blocks, state, tight) {
  let out = "";
  const mapped = Boolean(state.opts.sourceMap);
  const sp = (range) => (mapped && range ? spAttr(range) : "");
  for (const b of blocks) {
    switch (b.type) {
      case "para": {
        const inner = renderInline(inlineOf(b, "text", state.ctx), state, false);
        out += tight ? inner : `<p${sp(b.sp)}>${inner}</p>\n`;
        break;
      }
      case "heading": {
        const nodes = inlineOf(b, "text", state.ctx);
        const text = plainOf(nodes).trim();
        const id = headingId(text, state.ids);
        if (b.level <= 4) state.toc.push({ level: b.level, text, id });
        out += `<h${b.level} id="${escapeHtml(id)}"${sp(b.sp)}>${renderInline(nodes, state, false)}</h${b.level}>\n`;
        break;
      }
      case "code": {
        const cls = b.lang ? ` class="language-${escapeHtml(b.lang)}"` : "";
        out += `<pre${sp(b.sp)}><code${cls}>${escapeHtml(b.text)}</code></pre>\n`;
        break;
      }
      case "hr":
        out += "<hr>\n";
        break;
      case "quote":
        out += `<blockquote>\n${renderBlocks(b.children, state, false)}</blockquote>\n`;
        break;
      case "list": {
        const hasTask = b.items.some((it) => it.task !== null);
        const cls = hasTask ? ` class="contains-task-list"` : "";
        const tag = b.ordered ? "ol" : "ul";
        const start = b.ordered && b.start !== 1 ? ` start="${b.start}"` : "";
        out += `<${tag}${start}${cls}>\n`;
        for (const item of b.items) {
          let box = "";
          if (item.task !== null) {
            state.tasks++;
            if (item.task) state.tasksDone++;
            box = item.task ? `<input checked="" disabled="" type="checkbox"> ` : `<input disabled="" type="checkbox"> `;
          }
          let inner = renderBlocks(item.children, state, b.tight);
          const openP = /^<p(?: data-sp="\d+:\d+")?>/.exec(inner);
          if (!b.tight && box && openP) inner = openP[0] + box + inner.slice(openP[0].length);
          else inner = box + inner;
          out += `<li${sp(item.sp)}>${inner}</li>\n`;
        }
        out += `</${tag}>\n`;
        break;
      }
      case "table": {
        const alignAttr = (k) => (b.align[k] ? ` align="${b.align[k]}"` : "");
        out += "<table>\n<thead>\n<tr>\n";
        b.head.forEach((cell, k) => {
          out += `<th${alignAttr(k)}${sp(b.headSp && b.headSp[k])}>${renderInline(parseInline(cell, state.ctx), state, false)}</th>\n`;
        });
        out += "</tr>\n</thead>\n";
        if (b.rows.length) {
          out += "<tbody>\n";
          b.rows.forEach((row, r) => {
            out += "<tr>\n";
            row.forEach((cell, k) => {
              out += `<td${alignAttr(k)}${sp(b.rowSp && b.rowSp[r][k])}>${renderInline(parseInline(cell, state.ctx), state, false)}</td>\n`;
            });
            out += "</tr>\n";
          });
          out += "</tbody>\n";
        }
        out += "</table>\n";
        break;
      }
    }
  }
  return out;
}

const IMAGE_EXT = /\.(?:png|jpe?g|gif|webp|svg|avif|bmp)$/i;
const ITEM_IMAGE_EXT = /\.(?:apng|avif|gif|jpe?g|png|svg|webp)$/i;

/**
 * Calls opts.resolveItem and returns its answer, or null when it throws or
 * answers nothing usable.
 * @param {object} opts
 * @param {string} target
 * @param {string} form links.js form name
 * @param {boolean} embed
 * @returns {{kind: string, href: string, fileUrl?: string, mime?: string, name?: string}|null}
 */
function callResolveItem(opts, target, form, embed) {
  try {
    const r = opts.resolveItem(target, { form, embed });
    return r && typeof r === "object" && (r.href || r.fileUrl) ? r : null;
  } catch {
    return null;
  }
}

/**
 * Maps a markdown link or image destination through opts.resolveItem, as the
 * server renderer does: a relative path that resolves takes the item URL, or
 * the file URL for an image, and keeps its #fragment on a link. Anything else
 * passes through safeUrl unchanged.
 * @param {string} dest
 * @param {object} opts
 * @param {boolean} image
 * @returns {string}
 */
function itemDest(dest, opts, image) {
  if (typeof opts.resolveItem !== "function" || !isRelativeDestination(dest)) return safeUrl(dest);
  const hit = callResolveItem(opts, dest, image ? "markdownImage" : "markdownLink", image);
  if (!hit) return safeUrl(dest);
  if (image) return hit.fileUrl ? safeUrl(hit.fileUrl) || safeUrl(dest) : safeUrl(dest);
  if (!hit.href) return safeUrl(dest);
  const hash = String(dest).indexOf("#");
  const frag = hash >= 0 ? String(dest).slice(hash) : "";
  const safe = safeUrl(hit.href + frag);
  return safe === "#" ? safeUrl(dest) : safe;
}

/**
 * Renders a wikilink through opts.resolveItem with the classes of the server
 * renderer: an embedded image file becomes an img with class wikilink-embed,
 * another embedded file a file card, an embedded note a note card, and a
 * target that does not resolve a wikilink-new anchor.
 * @param {object} n wikilink node
 * @param {object} state
 * @param {boolean} inLink
 * @returns {string}
 */
function renderItemWikilink(n, state, inLink) {
  const opts = state.opts;
  const form = n.target.includes("/") ? "pathWikilink" : "wikilink";
  const hit = n.target ? callResolveItem(opts, n.target, form, n.embed) : null;
  const href = hit ? safeUrl(hit.href || hit.fileUrl || "") : "";
  if (!hit || !href || href === "#") {
    return inLink ? escapeHtml(n.label) : `<a class="wikilink wikilink-new" href="#">${escapeHtml(n.label)}</a>`;
  }
  const full = n.fragment ? `${href}#${n.fragment}` : href;
  if (n.embed && hit.kind === "file") {
    const image = hit.fileUrl && (String(hit.mime || "").toLowerCase().startsWith("image/") || (!hit.mime && ITEM_IMAGE_EXT.test(hit.name || n.target)));
    if (image) {
      const src = imageSrc(safeUrl(hit.fileUrl), opts);
      return `<img src="${escapeHtml(src)}" alt="${escapeHtml(n.label)}" class="wikilink-embed">`;
    }
    const written = n.fragment ? `${n.target}#${n.fragment}` : n.target;
    const name = !hit.name || n.label !== written ? n.label : hit.name;
    if (inLink) return escapeHtml(name);
    return `<a${anchorAttrs(href, "", "wikilink file-card")}><span class="file-card-name">${escapeHtml(name)}</span></a>`;
  }
  if (inLink) return escapeHtml(n.label);
  const cls = n.embed && hit.kind === "note" ? "wikilink note-card" : "wikilink";
  return `<a${anchorAttrs(full, "", cls)}>${escapeHtml(n.label)}</a>`;
}

/**
 * Picks an image source: resolveImage may map a pending upload to a blob URL,
 * and every other value must pass safeUrl.
 * @param {string} dest
 * @param {object} opts
 * @returns {string}
 */
function imageSrc(dest, opts) {
  if (typeof opts.resolveImage === "function") {
    const mapped = opts.resolveImage(dest);
    if (typeof mapped === "string" && mapped) {
      if (/^blob:/i.test(mapped)) return mapped.replace(/[\u0000- "'<>`]/g, "");
      const safe = safeUrl(mapped);
      if (safe !== "#") return safe;
    }
  }
  return safeUrl(dest);
}

/**
 * Renders inline nodes to HTML.
 * @param {object[]} nodes
 * @param {object} state
 * @param {boolean} inLink true inside an anchor, where nested anchors are not allowed
 * @returns {string}
 */
function renderInline(nodes, state, inLink) {
  const opts = state.opts;
  let out = "";
  for (const n of nodes) {
    switch (n.t) {
      case "text":
        out += escapeHtml(n.v);
        break;
      case "code":
        out += `<code>${escapeHtml(n.v)}</code>`;
        break;
      case "br":
        out += "<br>\n";
        break;
      case "softbreak":
        out += "\n";
        break;
      case "em":
        out += `<em>${renderInline(n.children, state, inLink)}</em>`;
        break;
      case "strong":
        out += `<strong>${renderInline(n.children, state, inLink)}</strong>`;
        break;
      case "del":
        out += `<del>${renderInline(n.children, state, inLink)}</del>`;
        break;
      case "link":
        if (inLink) out += renderInline(n.children, state, true);
        else out += `<a${anchorAttrs(itemDest(n.dest, opts, false), n.title)}>${renderInline(n.children, state, true)}</a>`;
        break;
      case "image": {
        const title = n.title ? ` title="${escapeHtml(n.title)}"` : "";
        out += `<img src="${escapeHtml(imageSrc(itemDest(n.dest, opts, true), opts))}" alt="${escapeHtml(plainOf(n.children))}"${title}>`;
        break;
      }
      case "autolink":
        if (inLink) out += escapeHtml(n.v);
        else out += `<a${anchorAttrs(safeUrl(n.href))}>${escapeHtml(n.v)}</a>`;
        break;
      case "wikilink": {
        state.links.push({ target: n.target, label: n.label });
        if (typeof opts.resolveItem === "function") {
          out += renderItemWikilink(n, state, inLink);
          break;
        }
        let resolved = null;
        if (typeof opts.resolveLink === "function" && n.target) {
          try {
            resolved = opts.resolveLink(n.target);
          } catch {
            resolved = null;
          }
        }
        const href = resolved && resolved.exists && resolved.href ? safeUrl(resolved.href) : "";
        if (href && href !== "#") {
          const full = n.fragment ? `${href}#${n.fragment}` : href;
          if (n.embed && IMAGE_EXT.test(n.target)) {
            out += `<img src="${escapeHtml(imageSrc(full, opts))}" alt="${escapeHtml(n.label)}" class="wikilink-embed">`;
          } else if (inLink) {
            out += escapeHtml(n.label);
          } else {
            out += `<a${anchorAttrs(full, "", "wikilink")}>${escapeHtml(n.label)}</a>`;
          }
        } else if (inLink) {
          out += escapeHtml(n.label);
        } else {
          out += `<a class="wikilink wikilink-new" href="#">${escapeHtml(n.label)}</a>`;
        }
        break;
      }
      case "tag": {
        const href = !inLink && typeof opts.tagHref === "function" ? safeUrl(opts.tagHref(n.tag)) : "";
        if (href && href !== "#") out += `<a class="tag" href="${escapeHtml(href)}">${escapeHtml(n.raw)}</a>`;
        else out += `<span class="tag">${escapeHtml(n.raw)}</span>`;
        break;
      }
      case "fnref": {
        let num = state.footnoteNumbers.get(n.label);
        if (!num) {
          state.footnoteOrder.push(n.label);
          num = state.footnoteOrder.length;
          state.footnoteNumbers.set(n.label, num);
        }
        const seen = (state.footnoteRefCount.get(n.label) || 0) + 1;
        state.footnoteRefCount.set(n.label, seen);
        const id = seen === 1 ? `fnref:${num}` : `fnref-${seen}:${num}`;
        const link = `<a href="#fn:${num}" class="footnote-ref" role="doc-noteref">${num}</a>`;
        out += `<sup id="${id}">${inLink ? String(num) : link}</sup>`;
        break;
      }
    }
  }
  return out;
}

/**
 * Renders markdown to HTML built from escaped text and a fixed tag set.
 * @param {string} md markdown source
 * @param {{resolveLink?: (target: string) => ({href: string, exists: boolean}|null),
 *   resolveItem?: (target: string, info: {form: string, embed: boolean}) =>
 *     ({kind: string, href: string, fileUrl?: string, mime?: string, name?: string}|null),
 *   resolveImage?: (url: string) => (string|null), tagHref?: (tag: string) => string,
 *   sourceMap?: boolean}} [options]
 *   resolveItem, when given, resolves wikilinks, embeds and relative markdown
 *   links and images to folders, notes and files. target is the wikilink
 *   target without heading and label, or the markdown destination as
 *   written, and form is a links.js form name. href is the item view URL and
 *   fileUrl the content URL of a file. It replaces resolveLink.
 *   resolveLink maps a wikilink target to a URL; resolveImage maps an image
 *   URL, such as markspace-pending:<key>, to a displayable URL; tagHref,
 *   when given, turns #tags into links. sourceMap adds data-sp="start:end"
 *   to every paragraph, heading, list item, table cell and code block pre,
 *   holding the UTF-16 offsets of the block in md. Without it the output is
 *   unchanged.
 * @returns {{html: string, toc: {level: number, text: string, id: string}[],
 *   links: {target: string, label: string}[], tasks: number, tasksDone: number}}
 */
export function render(md, { resolveLink, resolveItem, resolveImage, tagHref, sourceMap } = {}) {
  const { blocks, ctx } = parseDocument(md, Boolean(sourceMap));
  return renderDocument(blocks, ctx, { resolveLink, resolveItem, resolveImage, tagHref, sourceMap: Boolean(sourceMap) });
}

/**
 * Collapses whitespace: a run with two or more line feeds becomes one blank
 * line and any other run becomes one space.
 * @param {string} s
 * @returns {string}
 */
function collapseSpace(s) {
  return s
    .replace(/\s+/gu, (run) => ((run.match(/\n/g) || []).length >= 2 ? "\n\n" : " "))
    .trim();
}

/**
 * Returns the text of a markdown document without markup. Blocks are
 * separated by a blank line and code is kept.
 * @param {string} md
 * @returns {string}
 */
export function plainText(md) {
  const { blocks, ctx } = parseDocument(md);
  const parts = [];
  const walk = (list) => {
    for (const b of list) {
      switch (b.type) {
        case "para":
        case "heading":
          parts.push(plainOf(inlineOf(b, "text", ctx)));
          break;
        case "code":
          parts.push(b.text);
          break;
        case "quote":
          walk(b.children);
          break;
        case "list":
          for (const item of b.items) walk(item.children);
          break;
        case "table":
          parts.push([...b.head, ...b.rows.flat()].map((c) => plainOf(parseInline(c, ctx))).join(" "));
          break;
      }
    }
  };
  walk(blocks);
  return collapseSpace(parts.filter((p) => p.trim()).join("\n\n"));
}

/**
 * Returns up to maxChars characters of plain text, cut at a word boundary
 * with a trailing ellipsis when the text was cut, like the server's Excerpt.
 * @param {string} md
 * @param {number} [maxChars=240]
 * @returns {string}
 */
export function excerpt(md, maxChars = 240) {
  if (!(maxChars > 0)) maxChars = 240;
  const plain = plainText(md).split(/\s+/u).filter(Boolean).join(" ");
  const runes = Array.from(plain);
  if (runes.length <= maxChars) return plain;
  let cut = runes.slice(0, maxChars).join("");
  const idx = cut.lastIndexOf(" ");
  if (idx > 0) cut = cut.slice(0, idx);
  return cut.replace(/[ ,;:.]+$/, "") + "\u2026";
}

/**
 * Normalizes a link destination: cuts the query and fragment, decodes percent
 * escapes and removes a trailing ".md".
 * @param {string} dest
 * @returns {string}
 */
function linkTarget(dest) {
  let s = dest.trim();
  const cut = s.search(/[#?]/);
  if (cut >= 0) s = s.slice(0, cut);
  try {
    s = decodeURIComponent(s);
  } catch {
    // Keep the raw value when the escapes are malformed.
  }
  return s.endsWith(".md") ? s.slice(0, -3) : s;
}

/**
 * Reports whether a link destination points inside the space.
 * @param {string} dest
 * @returns {boolean}
 */
function isRelativeDestination(dest) {
  const s = dest.trim();
  if (!s || s.startsWith("#") || s.startsWith("//")) return false;
  return !/^[A-Za-z][A-Za-z0-9+.-]*:/.test(s);
}

/**
 * Returns every [[wikilink]] and every relative markdown link, with ".md"
 * removed from the target, in document order, deduplicated by target and
 * label.
 * @param {string} md
 * @returns {{target: string, label: string}[]}
 */
export function extractLinks(md) {
  const { blocks, ctx } = parseDocument(md);
  const out = [];
  const seen = new Set();
  const add = (target, label) => {
    if (!target) return;
    const key = target + "\u0000" + label;
    if (seen.has(key)) return;
    seen.add(key);
    out.push({ target, label });
  };
  walkInline(blocks, ctx, (n) => {
    if (n.t === "wikilink") {
      const target = linkTarget(n.target);
      add(target, n.label || target);
    } else if (n.t === "link" && isRelativeDestination(n.dest)) {
      add(linkTarget(n.dest), plainOf(n.children).trim());
    }
  });
  return out;
}

/**
 * Returns the lowercased #tags in the body, deduplicated and sorted. Tags in
 * code, autolinks, wikilinks and link destinations are ignored.
 * @param {string} md
 * @returns {string[]}
 */
export function extractTags(md) {
  const { blocks, ctx } = parseDocument(md);
  const found = new Set();
  walkInline(blocks, ctx, (n) => {
    if (n.t === "tag") found.add(n.tag);
  });
  return [...found].sort();
}
