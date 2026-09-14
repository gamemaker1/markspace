// Link library for the browser client. It ports internal/links and must
// return the same results for every case in test/vectors/links.json. Offsets are
// UTF-16 code unit offsets into the body, where the Go package uses UTF-8
// byte offsets. The package doc of internal/links states the rules.

/** Link forms, as named in test/vectors/links.json. */
export const FORM = Object.freeze({
  WIKILINK: "wikilink",
  PATH_WIKILINK: "pathWikilink",
  MARKDOWN_LINK: "markdownLink",
  MARKDOWN_IMAGE: "markdownImage",
});

/** Entry kinds. */
export const KIND = Object.freeze({ FOLDER: "folder", NOTE: "note", FILE: "file" });

const isWikiForm = (form) => form === FORM.WIKILINK || form === FORM.PATH_WIKILINK;

// ---------------------------------------------------------------------------
// Character classes

const BS = 92; // backslash
const isPunct = (c) => (c >= 33 && c <= 47) || (c >= 58 && c <= 64) || (c >= 91 && c <= 96) || (c >= 123 && c <= 126);
const isAlpha = (c) => (c >= 97 && c <= 122) || (c >= 65 && c <= 90);
const isHex = (c) => (c >= 48 && c <= 57) || (c >= 97 && c <= 102) || (c >= 65 && c <= 70);

function run(body, k, end, c) {
  let n = 0;
  while (k + n < end && body.charCodeAt(k + n) === c) n++;
  return n;
}

function isBlankFrom(body, pos, end) {
  for (; pos < end; pos++) {
    const c = body.charCodeAt(pos);
    if (c !== 32 && c !== 9 && c !== 13) return false;
  }
  return true;
}

/** Returns [column, position] after the spaces and tabs at pos. */
function indent(body, pos, end, col) {
  while (pos < end) {
    const c = body.charCodeAt(pos);
    if (c === 32) col++;
    else if (c === 9) col = (Math.floor(col / 4) + 1) * 4;
    else break;
    pos++;
  }
  return [col, pos];
}

// ---------------------------------------------------------------------------
// Block pass

function quotePrefix(body, pos, end) {
  let q = 0;
  for (;;) {
    let k = pos;
    for (let n = 0; k < end && body.charCodeAt(k) === 32 && n < 3; n++) k++;
    if (k >= end || body.charCodeAt(k) !== 62) return [q, pos];
    k++;
    if (k < end && (body.charCodeAt(k) === 32 || body.charCodeAt(k) === 9)) k++;
    pos = k;
    q++;
  }
}

function listMarker(body, k, end, w) {
  let m = k;
  const c0 = k < end ? body.charCodeAt(k) : -1;
  if (c0 === 45 || c0 === 43 || c0 === 42) {
    m = k + 1;
  } else {
    while (m < end && m - k < 9 && body.charCodeAt(m) >= 48 && body.charCodeAt(m) <= 57) m++;
    if (m === k || m >= end || (body.charCodeAt(m) !== 46 && body.charCodeAt(m) !== 41)) return [false, 0, 0];
    m++;
  }
  if (m < end) {
    const c = body.charCodeAt(m);
    if (c !== 32 && c !== 9 && c !== 13) return [false, 0, 0];
  }
  const markerCol = w + (m - k);
  const [col, pos] = indent(body, m, end, markerCol);
  if (pos >= end || body.charCodeAt(pos) === 13 || col - markerCol > 4 || col === markerCol) {
    return [true, m, markerCol + 1];
  }
  return [true, pos, col];
}

function opensFence(body, k, end) {
  if (k >= end) return [0, 0];
  const c = body.charCodeAt(k);
  if (c !== 96 && c !== 126) return [0, 0];
  const n = run(body, k, end, c);
  if (n < 3) return [0, 0];
  if (c === 96 && body.slice(k + n, end).includes("`")) return [0, 0];
  return [c, n];
}

function closesFence(body, k, end, c, n) {
  const m = run(body, k, end, c);
  return m >= n && isBlankFrom(body, k + m, end);
}

function isATXHeading(body, k, end) {
  const n = run(body, k, end, 35);
  if (n < 1 || n > 6) return false;
  if (k + n >= end) return true;
  const c = body.charCodeAt(k + n);
  return c === 32 || c === 9 || c === 13;
}

/** Returns the [start, end) ranges whose text is parsed for inline links. */
function inlineBlocks(body) {
  const st = {
    blocks: [], blockStart: -1, blockEnd: 0, para: false, quote: 0, lists: [],
    fence: 0, fenceLen: 0, fenceBase: 0, fenceQuote: 0, comment: false,
  };
  const endBlock = () => {
    if (st.blockStart >= 0) st.blocks.push([st.blockStart, st.blockEnd]);
    st.blockStart = -1;
  };
  const line = (start, end) => {
    const [q, qpos] = quotePrefix(body, start, end);
    if (st.fence !== 0) {
      if (q >= st.fenceQuote) {
        const [w, k] = indent(body, qpos, end, 0);
        if (w < st.fenceBase + 4 && closesFence(body, k, end, st.fence, st.fenceLen)) st.fence = 0;
        return;
      }
      st.fence = 0;
    }
    if (st.comment) {
      if (body.slice(start, end).includes("-->")) st.comment = false;
      return;
    }
    let [w, k] = indent(body, qpos, end, 0);
    if (isBlankFrom(body, k, end)) {
      endBlock();
      st.para = false;
      return;
    }
    if (q !== st.quote) {
      endBlock();
      st.para = false;
      st.lists.length = 0;
      st.quote = q;
    }
    const [marker, contentPos, contentCol] = listMarker(body, k, end, w);
    if (!st.para || marker) {
      while (st.lists.length > 0 && w < st.lists[st.lists.length - 1]) st.lists.pop();
    }
    let base = st.lists.length > 0 ? st.lists[st.lists.length - 1] : 0;
    if (w - base >= 4) {
      if (st.para) {
        st.blockEnd = end;
        return;
      }
      endBlock();
      return;
    }
    if (marker) {
      endBlock();
      st.para = false;
      st.lists.push(contentCol);
      if (isBlankFrom(body, contentPos, end)) return;
      k = contentPos;
      base = contentCol;
    }
    const [fc, fn] = opensFence(body, k, end);
    if (fn > 0) {
      endBlock();
      st.para = false;
      st.fence = fc;
      st.fenceLen = fn;
      st.fenceBase = base;
      st.fenceQuote = q;
      return;
    }
    if (body.startsWith("<!--", k) && k + 4 <= end) {
      endBlock();
      st.para = false;
      if (!body.slice(k + 2, end).includes("-->")) st.comment = true;
      return;
    }
    if (isATXHeading(body, k, end)) {
      endBlock();
      st.para = false;
      st.blockStart = k;
      st.blockEnd = end;
      endBlock();
      return;
    }
    if (!st.para) {
      endBlock();
      st.blockStart = k;
    }
    st.blockEnd = end;
    st.para = true;
  };
  for (let pos = 0; pos < body.length; ) {
    let lineEnd = body.indexOf("\n", pos);
    let next = body.length;
    if (lineEnd < 0) lineEnd = body.length;
    else next = lineEnd + 1;
    line(pos, lineEnd);
    pos = next;
  }
  endBlock();
  return st.blocks;
}

// ---------------------------------------------------------------------------
// Inline scan

function codeClose(body, from, e, n) {
  for (let j = from; j < e; ) {
    if (body.charCodeAt(j) !== 96) {
      j++;
      continue;
    }
    const m = run(body, j, e, 96);
    if (m === n) return j + m;
    j += m;
  }
  return -1;
}

/** Length of a URI scheme of 2 to 32 characters followed by ":", or 0. */
function schemeLen(s) {
  if (s.length === 0 || !isAlpha(s.charCodeAt(0))) return 0;
  for (let n = 1; n < s.length && n <= 32; n++) {
    const c = s.charCodeAt(n);
    if (c === 58) return n >= 2 ? n : 0;
    if (!isAlpha(c) && !(c >= 48 && c <= 57) && c !== 43 && c !== 46 && c !== 45) return 0;
  }
  return 0;
}

function autolinkEnd(body, i, e) {
  const n = schemeLen(body.slice(i + 1, Math.min(e, i + 35)));
  if (n === 0) return 0;
  for (let j = i + 1 + n + 1; j < e; j++) {
    const c = body.charCodeAt(j);
    if (c === 62) return j + 1;
    if (c <= 32 || c === 60 || c === 127) return 0;
  }
  return 0;
}

function trimRange(body, s, e) {
  while (s < e && (body.charCodeAt(s) === 32 || body.charCodeAt(s) === 9)) s++;
  while (e > s && (body.charCodeAt(e - 1) === 32 || body.charCodeAt(e - 1) === 9)) e--;
  return [s, e];
}

function wikilink(body, start, from, close, embed) {
  let tEnd = close;
  let lStart = -1;
  const p = body.slice(from, close).indexOf("|");
  if (p >= 0) {
    tEnd = from + p;
    lStart = tEnd + 1;
    if (tEnd > from && body.charCodeAt(tEnd - 1) === BS) tEnd--;
  }
  if (tEnd === from || lStart === close) return null;
  let rawEnd = tEnd;
  let hash = body.slice(from, tEnd).indexOf("#");
  if (hash >= 0) {
    hash += from;
    rawEnd = hash;
  }
  const [ts, te] = trimRange(body, from, rawEnd);
  const raw = body.slice(ts, te);
  const l = {
    form: raw.includes("/") ? FORM.PATH_WIKILINK : FORM.WIKILINK,
    embed, external: false, target: raw, heading: "", label: "", raw, angle: false,
    start, end: close + 2, targetStart: ts, targetEnd: te, destStart: ts, destEnd: te,
  };
  if (hash >= 0) {
    const [hs, he] = trimRange(body, hash + 1, tEnd);
    l.heading = body.slice(hs, he);
  }
  if (lStart >= 0) {
    const [ls, le] = trimRange(body, lStart, close);
    l.label = body.slice(ls, le);
  }
  if (raw === "") {
    if (hash < 0) return null;
    l.external = true;
  }
  return l;
}

function skipSpace(body, i, e) {
  while (i < e && (body.charCodeAt(i) === 32 || body.charCodeAt(i) === 9)) i++;
  if (i < e && body.charCodeAt(i) === 13) {
    i++;
    if (i < e && body.charCodeAt(i) === 10) i++;
  } else if (i < e && body.charCodeAt(i) === 10) {
    i++;
  } else {
    return i;
  }
  while (i < e && (body.charCodeAt(i) === 32 || body.charCodeAt(i) === 9)) i++;
  return i;
}

function inlineLink(body, op, close, e) {
  const j = close + 1;
  if (j >= e || body.charCodeAt(j) !== 40) return null;
  let k = skipSpace(body, j + 1, e);
  const l = {
    form: op.image ? FORM.MARKDOWN_IMAGE : FORM.MARKDOWN_LINK,
    embed: false, external: false, target: "", heading: "", label: "", raw: "", angle: false,
    start: op.pos, end: 0, targetStart: 0, targetEnd: 0, destStart: k, destEnd: 0,
  };
  l.label = body.slice(op.pos + (op.image ? 2 : 1), close);
  let innerS;
  let innerE;
  if (k < e && body.charCodeAt(k) === 60) {
    let m = k + 1;
    while (m < e) {
      const c = body.charCodeAt(m);
      if (c === BS && m + 1 < e && isPunct(body.charCodeAt(m + 1))) {
        m += 2;
        continue;
      }
      if (c === 62) break;
      if (c === 60 || c === 10 || c === 13) return null;
      m++;
    }
    if (m >= e) return null;
    innerS = k + 1;
    innerE = m;
    l.angle = true;
    k = m + 1;
  } else {
    let m = k;
    let depth = 0;
    while (m < e) {
      const c = body.charCodeAt(m);
      if (c === BS && m + 1 < e && isPunct(body.charCodeAt(m + 1))) {
        m += 2;
        continue;
      }
      if (c <= 32 || c === 127) break;
      if (c === 40) depth++;
      else if (c === 41) {
        if (depth === 0) break;
        depth--;
      }
      m++;
    }
    if (depth !== 0) return null;
    innerS = k;
    innerE = m;
    k = m;
  }
  l.destEnd = k;
  const t = skipSpace(body, k, e);
  const tc = t < e ? body.charCodeAt(t) : -1;
  if (t > k && (tc === 34 || tc === 39 || tc === 40)) {
    const closer = tc === 40 ? 41 : tc;
    let m = t + 1;
    while (m < e) {
      const c = body.charCodeAt(m);
      if (c === BS && m + 1 < e && isPunct(body.charCodeAt(m + 1))) {
        m += 2;
        continue;
      }
      if (c === closer) break;
      if (tc === 40 && c === 40) return null;
      m++;
    }
    if (m >= e) return null;
    k = skipSpace(body, m + 1, e);
  } else {
    k = t;
  }
  if (k >= e || body.charCodeAt(k) !== 41) return null;
  l.end = k + 1;

  let split = innerE;
  let hash = -1;
  for (let m = innerS; m < innerE; ) {
    const c = body.charCodeAt(m);
    if (c === BS && m + 1 < innerE && isPunct(body.charCodeAt(m + 1))) {
      m += 2;
      continue;
    }
    if (c === 63 && split === innerE) split = m;
    if (c === 35) {
      if (split === innerE) split = m;
      hash = m;
      break;
    }
    m++;
  }
  l.targetStart = innerS;
  l.targetEnd = split;
  l.raw = body.slice(innerS, split);
  l.target = decodeDest(l.raw);
  if (hash >= 0) l.heading = decodeDest(body.slice(hash + 1, innerE));
  l.external = l.raw === "" || l.raw.startsWith("//") || schemeLen(l.raw) > 0;
  return l;
}

const utf8Encoder = new TextEncoder();
const utf8Decoder = new TextDecoder("utf-8", { fatal: true });

/**
 * Removes backslash escapes, then decodes %XX sequences. When the decoded
 * bytes are not valid UTF-8 the percent sequences are kept.
 * @param {string} s
 * @returns {string}
 */
export function decodeDest(s) {
  if (s.includes("\\")) {
    let out = "";
    for (let i = 0; i < s.length; i++) {
      if (s.charCodeAt(i) === BS && i + 1 < s.length && isPunct(s.charCodeAt(i + 1))) i++;
      out += s[i];
    }
    s = out;
  }
  if (!s.includes("%")) return s;
  const src = utf8Encoder.encode(s);
  const buf = new Uint8Array(src.length);
  let n = 0;
  for (let i = 0; i < src.length; i++) {
    if (src[i] === 37 && i + 2 < src.length && isHex(src[i + 1]) && isHex(src[i + 2])) {
      buf[n++] = parseInt(String.fromCharCode(src[i + 1], src[i + 2]), 16);
      i += 2;
      continue;
    }
    buf[n++] = src[i];
  }
  try {
    return utf8Decoder.decode(buf.subarray(0, n));
  } catch {
    return s;
  }
}

function scanInline(body, s, e, out) {
  const first = out.length;
  const stack = [];
  let active = 0;
  let lineEnd = -1;
  let wikiFrom = -1;
  let wikiClose = -1;
  let commentNone = -1;
  let i = s;
  while (i < e) {
    const c = body.charCodeAt(i);
    if (c === BS) {
      i += i + 1 < e && isPunct(body.charCodeAt(i + 1)) ? 2 : 1;
    } else if (c === 96) {
      const n = run(body, i, e, 96);
      const cl = codeClose(body, i + n, e, n);
      i = cl >= 0 ? cl : i + n;
    } else if (c === 60) {
      let j = 0;
      if (body.startsWith("<!--", i) && i + 4 <= e) {
        if (commentNone < 0 || i + 2 < commentNone) {
          const cl = body.slice(i + 2, e).indexOf("-->");
          if (cl >= 0) j = i + 2 + cl + 3;
          else commentNone = i + 2;
        }
      } else {
        j = autolinkEnd(body, i, e);
      }
      i = j > 0 ? j : i + 1;
    } else if (c === 33 || c === 91) {
      const image = c === 33;
      let at = i;
      if (image) {
        at = i + 1;
        if (at >= e || body.charCodeAt(at) !== 91) {
          i++;
          continue;
        }
      }
      if (at + 1 < e && body.charCodeAt(at + 1) === 91) {
        if (i >= lineEnd) {
          lineEnd = body.indexOf("\n", i);
          if (lineEnd < 0 || lineEnd > e) lineEnd = e;
          wikiFrom = -1;
          wikiClose = -1;
        }
        const from = at + 2;
        if (wikiFrom < 0 || from < wikiFrom || (wikiClose >= 0 && wikiClose < from)) {
          wikiFrom = from;
          wikiClose = from <= lineEnd ? body.slice(from, lineEnd).indexOf("]]") : -1;
          if (wikiClose >= 0) wikiClose += from;
        }
        if (wikiClose >= 0) {
          const l = wikilink(body, i, from, wikiClose, image);
          if (l) {
            out.push(l);
            if (!image) active = stack.length;
            i = l.end;
            continue;
          }
        }
      }
      stack.push({ pos: i, image });
      i = at + 1;
    } else if (c === 93) {
      if (stack.length === 0) {
        i++;
        continue;
      }
      const op = stack.pop();
      const inactive = stack.length < active;
      if (active > stack.length) active = stack.length;
      if (inactive) {
        i++;
        continue;
      }
      const l = inlineLink(body, op, i, e);
      if (!l) {
        i++;
        continue;
      }
      out.push(l);
      if (!op.image) active = stack.length;
      i = l.end;
    } else {
      i++;
    }
  }
  if (out.length - first > 1) {
    const tail = out.splice(first).sort((a, b) => a.start - b.start);
    out.push(...tail);
  }
  return out;
}

/**
 * Returns the links in body in document order. Offsets are UTF-16 code
 * units. The body must not include frontmatter.
 * @param {string} body
 * @returns {object[]}
 */
export function parse(body) {
  body = String(body ?? "");
  const out = [];
  for (const [s, e] of inlineBlocks(body)) scanInline(body, s, e, out);
  return out;
}

// ---------------------------------------------------------------------------
// Names

const RESERVED_NAMES = new Set([
  "con", "prn", "aux", "nul", "com1", "com2", "com3", "com4",
  "lpt1", "lpt2", "lpt3", "lpt4", ".", "..", "index",
]);
const GO_SPACE = /^[\t\n\v\f\r  \p{Z}]$/u;
const LETTER_OR_DIGIT = /^[\p{L}\p{Nd}]$/u;

/** Lowercases one code point the way Go's unicode.ToLower does. */
function lowerRune(ch) {
  const lowered = Array.from(ch.toLowerCase());
  return lowered.length === 1 ? lowered[0] : lowered[0] ?? ch;
}

/** Lowercases every code point, as the Go package's lower does. */
export function lower(s) {
  let out = "";
  for (const ch of s) out += lowerRune(ch);
  return out;
}

/**
 * Converts a title into a slug exactly as core.Slugify does.
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

/** Returns the number of UTF-8 bytes in s. */
export function utf8Length(s) {
  let n = 0;
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i);
    if (c < 0x80) n += 1;
    else if (c < 0x800) n += 2;
    else if (c >= 0xd800 && c <= 0xdbff && i + 1 < s.length && s.charCodeAt(i + 1) >= 0xdc00 && s.charCodeAt(i + 1) <= 0xdfff) {
      n += 4;
      i++;
    } else n += 3;
  }
  return n;
}

/** Compares two strings in code point order, which is Go's byte order. */
export function compareCodePoints(a, b) {
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i++) {
    let x = a.charCodeAt(i);
    let y = b.charCodeAt(i);
    if (x === y) continue;
    x = x >= 0xe000 ? x - 0x800 : x >= 0xd800 ? x + 0x2000 : x;
    y = y >= 0xe000 ? y - 0x800 : y >= 0xd800 ? y + 0x2000 : y;
    return x < y ? -1 : 1;
  }
  return a.length === b.length ? 0 : a.length < b.length ? -1 : 1;
}

const dirName = (p) => {
  const i = p.lastIndexOf("/");
  return i >= 0 ? p.slice(0, i) : "";
};
const baseName = (p) => p.slice(p.lastIndexOf("/") + 1);
const segments = (p) => (p === "" ? [] : p.split("/"));
const trimMD = (s) => (s.endsWith(".md") ? s.slice(0, -3) : s);

function treeDistance(a, b) {
  const as = segments(a);
  const bs = segments(b);
  let c = 0;
  while (c < as.length && c < bs.length && as[c] === bs[c]) c++;
  return as.length + bs.length - 2 * c;
}

/** Resolves a markdown path against folder dir, or returns null. */
function cleanPath(dir, target) {
  const out = target.startsWith("/") ? [] : segments(dir);
  for (const seg of target.split("/")) {
    if (seg === "" || seg === ".") continue;
    if (seg === "..") {
      if (out.length === 0) return null;
      out.pop();
    } else out.push(seg);
  }
  return out.length === 0 ? null : out.join("/");
}

// ---------------------------------------------------------------------------
// Snapshot

function pushTo(map, key, value) {
  const list = map.get(key);
  if (list) list.push(value);
  else map.set(key, [value]);
}

/** Snapshot indexes the entries of one space for resolution. */
export class Snapshot {
  /** @param {{id: string, kind: string, path: string, name?: string, title?: string}[]} entries */
  constructor(entries) {
    this.entries = (entries || []).map((e) => ({
      id: e.id, kind: e.kind, path: e.path, name: e.name || baseName(e.path), title: e.title || "",
    }));
    this.bytes = this.entries.map((e) => utf8Length(e.path));
    this.byIdMap = new Map();
    this.byPathMap = new Map();
    this.bySuffix = new Map();
    this.filesByName = new Map();
    this.notesByStem = new Map();
    this.notesByName = new Map();
    this.entries.forEach((e, i) => {
      if (!this.byIdMap.has(e.id)) this.byIdMap.set(e.id, i);
      if (this.byPathMap.has(e.path)) return;
      this.byPathMap.set(e.path, i);
      if (e.kind === KIND.FOLDER) return;
      for (let j = 0; j < e.path.length; j++) {
        if (e.path.charCodeAt(j) === 47) pushTo(this.bySuffix, e.path.slice(j + 1), i);
      }
      if (e.kind === KIND.FILE) {
        pushTo(this.filesByName, e.name, i);
      } else if (e.kind === KIND.NOTE) {
        pushTo(this.notesByStem, trimMD(e.name), i);
        if (e.title) pushTo(this.notesByName, lower(e.title), i);
        if (e.name === "index.md") {
          const d = dirName(e.path);
          if (d !== "") {
            const k = lower(baseName(d));
            if (!e.title || k !== lower(e.title)) pushTo(this.notesByName, k, i);
          }
        }
      }
    });
  }

  byId(id) {
    const i = this.byIdMap.get(id);
    return i === undefined ? null : this.entries[i];
  }

  byPath(p) {
    const i = this.byPathMap.get(p);
    return i === undefined ? null : this.entries[i];
  }

  /** Resolves a parsed link from the note at fromPath. */
  resolve(fromPath, link) {
    if (!link || link.external) return null;
    return this.resolveTarget(fromPath, link.target, link.form);
  }

  /** Resolves a decoded target without heading or label. */
  resolveTarget(fromPath, target, form) {
    if (!target) return null;
    if (!isWikiForm(form)) {
      const p = cleanPath(dirName(fromPath), target);
      return p === null ? null : this.exact(p);
    }
    const from = dirName(fromPath);
    if (target.includes("/")) {
      const rooted = target.startsWith("/");
      const p = rooted ? target.slice(1) : target;
      if (p === "") return null;
      const e = this.exact(p);
      if (e || rooted) return e;
      return this.pick(from, [...(this.bySuffix.get(p) || []), ...(this.bySuffix.get(p + ".md") || [])]);
    }
    const files = this.filesByName.get(target);
    if (files) return this.pick(from, files);
    const stems = this.notesByStem.get(slugify(target));
    if (stems) return this.pick(from, stems);
    return this.pick(from, this.notesByName.get(lower(target)) || []);
  }

  exact(p) {
    let i = this.byPathMap.get(p);
    if (i === undefined) i = this.byPathMap.get(p + ".md");
    return i === undefined ? null : this.entries[i];
  }

  pick(from, cands) {
    let best = -1;
    let bestDist = 0;
    for (const i of cands) {
      const p = this.entries[i].path;
      const d = treeDistance(from, dirName(p));
      if (best < 0) {
        best = i;
        bestDist = d;
        continue;
      }
      const lp = this.bytes[i];
      const lb = this.bytes[best];
      if (d < bestDist || (d === bestDist && (lp < lb || (lp === lb && compareCodePoints(p, this.entries[best].path) < 0)))) {
        best = i;
        bestDist = d;
      }
    }
    return best < 0 ? null : this.entries[best];
  }
}

/** @param {object[]} entries */
export function newSnapshot(entries) {
  return new Snapshot(entries);
}

// ---------------------------------------------------------------------------
// Rewriting

/**
 * Returns a copy of entries with the changes applied in order.
 * @param {object[]} entries
 * @param {{kind: string, id: string, oldPath: string, newPath: string}[]} changes
 */
export function applyChanges(entries, changes) {
  const out = entries.map((e) => ({ ...e }));
  const byId = new Map();
  out.forEach((e, i) => {
    if (!byId.has(e.id)) byId.set(e.id, i);
  });
  for (const ch of changes || []) {
    let i = ch.id ? byId.get(ch.id) : undefined;
    if (i === undefined) {
      i = out.findIndex((e) => e.path === ch.oldPath);
      if (i < 0) continue;
    }
    const old = out[i].path;
    if (old === ch.newPath || !ch.newPath) continue;
    for (const e of out) {
      if (e.path === old || e.path.startsWith(old + "/")) {
        e.path = ch.newPath + e.path.slice(old.length);
        e.name = baseName(e.path);
      }
    }
  }
  return out;
}

function relPath(dir, p) {
  const ds = segments(dir);
  const ps = segments(p);
  let c = 0;
  while (c < ds.length && c < ps.length && ds[c] === ps[c]) c++;
  const parts = [];
  for (let k = c; k < ds.length; k++) parts.push("..");
  parts.push(...ps.slice(c));
  const out = parts.join("/");
  if (out === "") return ".";
  return schemeLen(out) > 0 ? "./" + out : out;
}

const needsAngle = (p) => /[ ()]/.test(p);
const DEST_ESCAPE = /[ -%\\#?<>[\]`|]/g;

/** Percent-encodes a markdown destination as the Go package does. */
export function encodeDest(p) {
  return p.replace(DEST_ESCAPE, (c) => "%" + c.charCodeAt(0).toString(16).toUpperCase().padStart(2, "0"));
}

function escapeLabel(s) {
  return s.replace(/[`\\[\]<>|]/g, (c) => (c === "`" ? "&#96;" : "\\" + c));
}

function wikiTargetOK(c) {
  if (c === "" || c.startsWith(" ") || c.endsWith(" ")) return false;
  return !/[ -|#[\]`<>\\]/.test(c);
}

/** Rewriter computes rewrites for one set of changes. */
export class Rewriter {
  constructor(before, changes) {
    this.before = before;
    this.after = new Snapshot(applyChanges(before.entries, changes));
  }

  /**
   * Returns the edits for one note body, sorted by start.
   * @returns {{start: number, end: number, text: string}[]}
   */
  rewrite(noteOldPath, noteNewPath, body) {
    const edits = [];
    for (const l of parse(body)) {
      if (l.external) continue;
      const e0 = this.before.resolve(noteOldPath, l);
      if (!e0) continue;
      const e1 = this.after.resolve(noteNewPath, l);
      if (e1 && e1.id === e0.id) continue;
      const target = this.after.byId(e0.id);
      if (!target) continue;
      edits.push(isWikiForm(l.form) ? this.wikiEdit(noteNewPath, l, target) : this.markdownEdit(noteNewPath, l, target, body));
    }
    edits.sort((a, b) => a.start - b.start);
    return edits;
  }

  markdownEdit(notePath, l, target, body) {
    const rooted = l.target.startsWith("/");
    const mk = (p) => (rooted ? "/" + p : relPath(dirName(notePath), p));
    let cand = mk(target.path);
    if (!l.target.endsWith(".md") && target.kind === KIND.NOTE && target.path.endsWith(".md")) {
      const short = mk(trimMD(target.path));
      const e = this.after.resolveTarget(notePath, short, l.form);
      if (e && e.id === target.id) cand = short;
    }
    const enc = encodeDest(cand);
    if (l.angle || !needsAngle(cand)) return { start: l.targetStart, end: l.targetEnd, text: enc };
    const suffix = body.slice(l.targetEnd, l.destEnd).replaceAll("<", "%3C").replaceAll(">", "%3E");
    return { start: l.destStart, end: l.destEnd, text: "<" + enc + suffix + ">" };
  }

  wikiEdit(notePath, l, target) {
    const cands = [];
    if (l.form === FORM.WIKILINK) {
      if (target.kind === KIND.FILE) {
        cands.push(target.name);
      } else if (target.kind === KIND.NOTE) {
        if (target.name === "index.md") {
          const d = dirName(target.path);
          if (d !== "") cands.push(baseName(d));
        } else {
          cands.push(trimMD(target.name));
        }
        if (target.title) cands.push(target.title);
      }
    }
    const hasMD = l.form === FORM.PATH_WIKILINK && l.target.endsWith(".md");
    const rooted = l.form === FORM.PATH_WIKILINK && l.target.startsWith("/");
    let bases = [target.path];
    if (!hasMD && target.kind === KIND.NOTE && target.path.endsWith(".md")) bases = [trimMD(target.path), target.path];
    if (!rooted) for (const b of bases) if (b.includes("/")) cands.push(b);
    for (const b of bases) cands.push("/" + b);
    for (const c of cands) {
      if (!wikiTargetOK(c)) continue;
      const e = this.after.resolveTarget(notePath, c, FORM.WIKILINK);
      if (e && e.id === target.id) return { start: l.targetStart, end: l.targetEnd, text: c };
    }
    let dest = encodeDest(relPath(dirName(notePath), target.path));
    if (l.heading) dest += "#" + encodeDest(l.heading);
    let label = l.label;
    if (!label && !l.embed) {
      label = l.raw;
      if (l.heading) label += "#" + l.heading;
    }
    let text = "[" + escapeLabel(label) + "](<" + dest + ">)";
    if (l.embed) text = "!" + text;
    return { start: l.start, end: l.end, text };
  }
}

/** @param {Snapshot} before @param {object[]} changes */
export function newRewriter(before, changes) {
  return new Rewriter(before, changes);
}

/**
 * Returns the edits for one note body, given the snapshot before the
 * changes and the note's old and new entry paths.
 */
export function rewrite(before, changes, noteOldPath, noteNewPath, body) {
  return new Rewriter(before, changes).rewrite(noteOldPath, noteNewPath, body);
}

/**
 * Returns body with sorted, non-overlapping edits applied.
 * @param {string} body
 * @param {{start: number, end: number, text: string}[]} edits
 */
export function apply(body, edits) {
  if (!edits || edits.length === 0) return body;
  let out = "";
  let last = 0;
  for (const e of edits) {
    out += body.slice(last, e.start) + e.text;
    last = e.end;
  }
  return out + body.slice(last);
}

const uniq = (list) => [...new Set(list)];

/** Returns the index keys of an entry. */
export function entryKeys(e) {
  const name = e.name || baseName(e.path);
  const keys = [lower(name), lower(trimMD(name))];
  if (e.kind === KIND.NOTE) {
    if (e.title) keys.push(lower(e.title));
    if (name === "index.md") {
      const d = dirName(e.path);
      if (d !== "") keys.push(lower(baseName(d)));
    }
  }
  return uniq(keys);
}

/** Returns the index keys of one link in the note at fromPath. */
export function linkKeys(fromPath, l) {
  if (l.external) return [];
  if (isWikiForm(l.form)) {
    const base = baseName(l.target);
    if (base === "") return [];
    return uniq([lower(base), lower(trimMD(base)), slugify(base)]);
  }
  const p = cleanPath(dirName(fromPath), l.target);
  if (p === null) return [];
  const base = baseName(p);
  return uniq([lower(base), lower(trimMD(base))]);
}

/**
 * Returns the sorted ids of notes whose bodies may need rewrite for the
 * changes. notesWithKey(key) returns the ids of notes with a link that has
 * that linkKeys key.
 */
export function affected(before, changes, notesWithKey) {
  const after = applyChanges(before.entries, changes);
  const ids = new Set();
  const keys = new Set();
  after.forEach((a, i) => {
    const b = before.entries[i];
    if (a.path === b.path) return;
    if (a.kind === KIND.NOTE) ids.add(a.id);
    for (const k of entryKeys(b)) keys.add(k);
    for (const k of entryKeys(a)) keys.add(k);
  });
  if (notesWithKey) for (const k of keys) for (const id of notesWithKey(k) || []) ids.add(id);
  return [...ids].sort(compareCodePoints);
}
