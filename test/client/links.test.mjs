import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

import {
  FORM,
  KIND,
  parse,
  newSnapshot,
  newRewriter,
  rewrite,
  apply,
  applyChanges,
  affected,
  linkKeys,
  encodeDest,
  slugify,
  lower,
  utf8Length,
  compareCodePoints,
} from "../../internal/web/static/js/links.js";
import { slugify as markdownSlugify } from "../../internal/web/static/js/markdown.js";

const cases = JSON.parse(readFileSync(new URL("../vectors/links.json", import.meta.url), "utf8"));

/** Converts a parsed link to the vector shape, with UTF-8 byte offsets. */
function toVector(body, l) {
  const b = (off) => utf8Length(body.slice(0, off));
  const out = { form: l.form };
  if (l.embed) out.embed = true;
  if (l.external) out.external = true;
  if (l.angle) out.angle = true;
  out.target = l.target;
  if (l.heading) out.heading = l.heading;
  if (l.label) out.label = l.label;
  out.raw = l.raw;
  for (const k of ["start", "end", "targetStart", "targetEnd", "destStart", "destEnd"]) out[k] = b(l[k]);
  return out;
}

test("the case file has enough cases", () => {
  assert.ok(cases.parse.length > 0 && cases.resolve.length > 0 && cases.rewrite.length > 0);
  assert.ok(cases.parse.length + cases.resolve.length + cases.rewrite.length >= 60);
});

test("parse reproduces every reference case", () => {
  for (const c of cases.parse) {
    const got = parse(c.body).map((l) => toVector(c.body, l));
    assert.deepEqual(got, c.want, c.name);
    for (const l of parse(c.body)) assert.equal(c.body.slice(l.targetStart, l.targetEnd), l.raw, c.name);
  }
});

const snapshots = new Map();
function snapshotOf(name) {
  assert.ok(cases.trees[name], `tree ${name}`);
  if (!snapshots.has(name)) snapshots.set(name, newSnapshot(cases.trees[name]));
  return snapshots.get(name);
}

test("resolveTarget reproduces every reference case", () => {
  for (const c of cases.resolve) {
    const got = snapshotOf(c.tree).resolveTarget(c.from, c.target, c.form);
    assert.equal(got ? got.path : null, c.want, c.name);
  }
});

test("rewrite reproduces every reference case and is then a fixed point", () => {
  for (const c of cases.rewrite) {
    const rw = newRewriter(snapshotOf(c.tree), c.changes);
    const got = apply(c.body, rw.rewrite(c.noteOld, c.noteNew, c.body));
    assert.equal(got, c.want, c.name);
    assert.deepEqual(newRewriter(rw.after, []).rewrite(c.noteNew, c.noteNew, got), [], c.name);
  }
});

test("slugify matches the renderer's port of core.Slugify", () => {
  for (const s of ["Harbour Survey!", "  Café  Notes ", "index", "İstanbul ΣΊΣΥΦΟΣ", "a_b-c", "", "😀 x", "x".repeat(100)]) {
    assert.equal(slugify(s), markdownSlugify(s), s);
  }
});

test("helpers count bytes and order code points like Go", () => {
  assert.equal(utf8Length("aé日😀"), 1 + 2 + 3 + 4);
  assert.ok(compareCodePoints("￿", "😀") < 0, "U+FFFF sorts before U+1F600");
  assert.ok(compareCodePoints("a", "ab") < 0);
  assert.equal(compareCodePoints("x", "x"), 0);
  assert.equal(lower("CAFÉ İ"), "café i");
  assert.equal(encodeDest("a b%#?<>[]`|\\.png"), "a b%25%23%3F%3C%3E%5B%5D%60%7C%5C.png");
});

// ---------------------------------------------------------------------------
// Property tests

/** mulberry32 returns a seeded PRNG in [0, 1). */
function mulberry32(seed) {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const NAME_PARTS = [
  "a", "b", "Ab", "é", "x y", "(1)", "#", "%", "'", "`", "[", "]", "|", "<", ">",
  ":", "&", "?", "!", "日本", "-", "_", ".", "md", "index", "ab:c", "😀", "%20", "x",
];
const FILLERS = ["", " ", " ", "\n", "text ", "`c` ", "\n\n", "\n- ", "\n> ", "[", "]", "`", "\n    ", "(", "!", "\n```\n", "<!-- ", " -->", "| "];

const dirOf = (p) => (p.includes("/") ? p.slice(0, p.lastIndexOf("/")) : "");
const baseOf = (p) => p.slice(p.lastIndexOf("/") + 1);
const joinPath = (d, n) => (d === "" ? n : d + "/" + n);

function relPath(dir, p) {
  const ds = dir === "" ? [] : dir.split("/");
  const ps = p.split("/");
  let c = 0;
  while (c < ds.length && c < ps.length && ds[c] === ps[c]) c++;
  const out = [...ds.slice(c).map(() => ".."), ...ps.slice(c)].join("/");
  if (out === "") return ".";
  return /^[A-Za-z][A-Za-z0-9+.-]{1,31}:/.test(out) ? "./" + out : out;
}

/** Generates a random space, note bodies and a change sequence. */
function generate(rnd) {
  const int = (n) => Math.floor(rnd() * n);
  const pick = (list) => list[int(list.length)];
  let next = 0;
  const id = () => `e${String(++next).padStart(3, "0")}`;
  const rawName = () => {
    let s = "";
    for (let n = 1 + int(3); n > 0; n--) s += pick(NAME_PARTS);
    s = s.replace(/^ +| +$/g, "");
    if (s === "" || s.startsWith(".")) s = "n" + s;
    return s;
  };
  const taken = (entries, dir, name) => entries.some((e) => dirOf(e.path) === dir && lower(baseOf(e.path)) === lower(name));
  const folders = (entries) => ["", ...entries.filter((e) => e.kind === KIND.FOLDER).map((e) => e.path)];
  const noteName = (dir, allowIndex) => {
    if (allowIndex && dir !== "" && int(4) === 0) return "index.md";
    let n = rawName();
    if (int(2) === 0) n = slugify(n);
    if (n === "index") n = "index-note";
    return n + ".md";
  };
  const fileName = () => rawName() + pick(["", ".png", ".pdf", ".png", ".md"]);

  const entries = [];
  const add = (kind) => {
    const dir = pick(folders(entries));
    for (let t = 0; t < 10; t++) {
      let name;
      let title = "";
      if (kind === KIND.FOLDER) name = rawName();
      else if (kind === KIND.NOTE) {
        name = noteName(dir, true);
        title = rawName();
        if (int(3) === 0 && entries.length > 0) title = pick(entries).name.replace(/\.md$/, "");
      } else name = fileName();
      if (taken(entries, dir, name)) continue;
      entries.push({ id: id(), kind, path: joinPath(dir, name), name, title });
      return;
    }
  };
  const nFolders = int(6);
  const nNotes = 1 + int(7);
  const nFiles = int(7);
  for (let i = 0; i < nFolders; i++) add(KIND.FOLDER);
  for (let i = 0; i < nNotes; i++) add(KIND.NOTE);
  for (let i = 0; i < nFiles; i++) add(KIND.FILE);

  const link = (note, t) => {
    const stem = t.name.replace(/\.md$/, "");
    const heading = int(4) === 0 ? "#h" + pick(NAME_PARTS) : "";
    if (int(2) === 0) {
      let target;
      switch (int(6)) {
        case 0: target = t.name; break;
        case 1: target = stem; break;
        case 2: target = t.title; break;
        case 3: target = baseOf(dirOf(t.path)); break;
        case 4: target = t.path.replace(/\.md$/, ""); break;
        default:
          target = "/" + t.path;
          if (t.path.includes("/") && int(2) === 0) target = t.path.slice(t.path.indexOf("/") + 1);
      }
      if (!target) target = t.name;
      const label = int(3) === 0 ? "|" + pick(NAME_PARTS) : "";
      return (int(4) === 0 ? "!" : "") + "[[" + target + heading + label + "]]";
    }
    let p = t.path;
    if (t.kind === KIND.NOTE && int(3) === 0) p = p.replace(/\.md$/, "");
    p = int(4) === 0 ? "/" + p : relPath(dirOf(note.path), p);
    let dest = int(6) === 0 ? p : encodeDest(p);
    dest += heading;
    if (/[ ()]/.test(dest) || int(4) === 0) dest = "<" + dest + ">";
    const title = int(5) === 0 ? ' "t"' : "";
    return (int(4) === 0 ? "!" : "") + "[" + pick(NAME_PARTS) + "](" + dest + title + ")";
  };
  const bodies = new Map();
  for (const e of entries) {
    if (e.kind !== KIND.NOTE) continue;
    let b = "";
    for (let n = 1 + int(6); n > 0; n--) b += pick(FILLERS) + link(e, pick(entries)) + pick(FILLERS);
    bodies.set(e.id, b);
  }

  let cur = entries.map((e) => ({ ...e }));
  const changes = [];
  for (let n = 1 + int(4); n > 0; n--) {
    const e = pick(cur);
    let newPath;
    if (int(2) === 0) {
      if (e.name === "index.md") continue;
      const name = e.kind === KIND.FOLDER ? rawName() : e.kind === KIND.NOTE ? noteName("", false) : fileName();
      if (taken(cur, dirOf(e.path), name)) continue;
      newPath = joinPath(dirOf(e.path), name);
    } else {
      const dir = pick(folders(cur));
      if (dir === dirOf(e.path) || dir === e.path || dir.startsWith(e.path + "/") || taken(cur, dir, e.name)) continue;
      newPath = joinPath(dir, e.name);
    }
    const ch = { kind: dirOf(newPath) === dirOf(e.path) ? "rename" : "move", id: e.id, oldPath: e.path, newPath };
    changes.push(ch);
    cur = applyChanges(cur, [ch]);
  }
  return { entries, bodies, changes };
}

test("rewrite keeps every link on its target after random renames and moves", () => {
  let edits = 0;
  for (let iter = 0; iter < 500; iter++) {
    const seed = 0x5eed + iter;
    const { entries, bodies, changes } = generate(mulberry32(seed));
    const before = newSnapshot(entries);
    const index = new Map();
    for (const e of entries) {
      if (e.kind !== KIND.NOTE) continue;
      for (const l of parse(bodies.get(e.id))) {
        for (const k of linkKeys(e.path, l)) index.set(k, [...(index.get(k) || []), e.id]);
      }
    }
    const aff = new Set(affected(before, changes, (k) => index.get(k)));
    const rw = newRewriter(before, changes);
    const after = rw.after;
    for (const e of entries) {
      if (e.kind !== KIND.NOTE) continue;
      const body = bodies.get(e.id);
      const oldPath = e.path;
      const newPath = after.byId(e.id).path;
      const ctx = () => JSON.stringify({ seed, note: e.id, oldPath, newPath, changes, body });
      assert.deepEqual(rewrite(before, [], oldPath, oldPath, body), [], ctx());
      const ids0 = parse(body).map((l) => before.resolve(oldPath, l)?.id ?? null);
      const ed = rw.rewrite(oldPath, newPath, body);
      for (let i = 1; i < ed.length; i++) assert.ok(ed[i - 1].end <= ed[i].start, ctx());
      if (ed.length > 0) assert.ok(aff.has(e.id), "not in affected: " + ctx());
      edits += ed.length;
      const nb = apply(body, ed);
      const links1 = parse(nb);
      assert.equal(links1.length, ids0.length, `${nb} ${ctx()}`);
      links1.forEach((l, i) => {
        if (ids0[i] === null) return;
        assert.equal(after.resolve(newPath, l)?.id, ids0[i], `link ${i} of ${nb} ${ctx()}`);
      });
      assert.deepEqual(newRewriter(after, []).rewrite(newPath, newPath, nb), [], `second rewrite of ${nb} ${ctx()}`);
    }
  }
  assert.ok(edits > 100, `only ${edits} edits, the generator is too weak`);
});

test("parse invariants hold on random token soup", () => {
  const tokens = ["[", "]", "[[", "]]", "![[", "!", "(", ")", "<", ">", "`", "``", "```", "\\", "|", "#", "\n", "\r\n", "\n\n", "    ", "- ", "> ", "<!--", "-->", "\"", "a", "b c", "é", "😀", "x.png", "https:"];
  const rnd = mulberry32(42);
  for (let iter = 0; iter < 2000; iter++) {
    let body = "";
    for (let n = Math.floor(rnd() * 30); n > 0; n--) body += tokens[Math.floor(rnd() * tokens.length)];
    const ls = parse(body);
    ls.forEach((l, i) => {
      assert.ok(l.start >= 0 && l.end <= body.length && l.start < l.end, body);
      assert.ok(l.destStart >= l.start && l.destStart <= l.targetStart && l.targetStart <= l.targetEnd && l.targetEnd <= l.destEnd && l.destEnd <= l.end, body);
      assert.equal(body.slice(l.targetStart, l.targetEnd), l.raw, body);
      if (i > 0) assert.ok(ls[i - 1].start < l.start, body);
      for (let j = 0; j < i; j++) {
        const o = ls[j];
        if (l.start >= o.end) continue;
        const nested = (o.form === FORM.MARKDOWN_LINK || o.form === FORM.MARKDOWN_IMAGE) && l.end <= o.destStart && (l.form === FORM.MARKDOWN_IMAGE || l.embed);
        assert.ok(nested, `overlap in ${JSON.stringify(body)}`);
      }
    });
  }
});

test("parse stays fast on pathological input", () => {
  const body = "[".repeat(20000) + "`".repeat(200) + "<!--".repeat(2000) + "[[a".repeat(5000) + "\n" + "[a](".repeat(5000);
  const t = Date.now();
  parse(body);
  assert.ok(Date.now() - t < 2000);
});
