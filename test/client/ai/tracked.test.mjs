import { test } from "node:test";
import assert from "node:assert/strict";
import { register } from "node:module";

// Browser modules import each other as /static/js/...; map that to the files.
register("data:text/javascript," + encodeURIComponent(`
export async function resolve(specifier, context, next) {
  if (specifier.startsWith("/static/")) {
    return { url: new URL("../../../internal/web/static/" + specifier.slice(8), ${JSON.stringify(import.meta.url)}).href, shortCircuit: true };
  }
  return next(specifier, context);
}`));

const T = await import("../../../internal/web/static/js/ai/tracked.js");
const { EditorState, history, undo } = await import("../../../internal/web/static/vendor/codemirror.js");

/** A deterministic random source, so a failing case can be replayed. */
function rng(seed) {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const WORDS = ["the", "quay", "silt", "rose", "tide", "north", "crane", "rail", "is", "sound", "é", "日本", "don't", "18", "cm", "and", "a"];
const SEPS = [" ", " ", " ", ", ", ". ", "\n", "  ", " - "];

function randomText(r, n) {
  let out = "";
  for (let i = 0; i < n; i++) out += WORDS[Math.floor(r() * WORDS.length)] + SEPS[Math.floor(r() * SEPS.length)];
  return out.trimEnd();
}

function mutate(r, text) {
  const toks = T.tokenize(text);
  const out = [];
  for (const tok of toks) {
    const x = r();
    if (x < 0.08) continue;
    if (x < 0.16) out.push(WORDS[Math.floor(r() * WORDS.length)]);
    else out.push(tok);
    if (r() < 0.06) out.push(" " + WORDS[Math.floor(r() * WORDS.length)] + " ");
  }
  return out.join("");
}

/** A minimal view over an EditorState, enough for the commands. */
function fakeView(doc, extra = []) {
  const view = { state: EditorState.create({ doc, extensions: [T.trackedChanges({}), ...extra] }) };
  // Commands destructure dispatch, so it must not depend on this.
  view.dispatch = (...specs) => {
    const tr = specs.length === 1 && specs[0] && typeof specs[0].startState === "object" ? specs[0] : view.state.update(...specs);
    view.state = tr.state;
  };
  return view;
}

function shuffle(r, list) {
  const a = [...list];
  for (let i = a.length - 1; i > 0; i--) {
    const j = Math.floor(r() * (i + 1));
    [a[i], a[j]] = [a[j], a[i]];
  }
  return a;
}

test("tokenize keeps every character and splits words, spaces and punctuation", () => {
  const text = "Silt rose 18 cm, don't panic.\n\tDone — 日本語!";
  assert.equal(T.tokenize(text).join(""), text);
  assert.deepEqual(T.tokenize("don't go."), ["don't", " ", "go", "."]);
  assert.equal(T.isWordToken("go"), true);
  assert.equal(T.isWordToken(" "), false);
  assert.equal(T.isWordToken(","), false);
});

test("diffWords marks replaced words and keeps the rest", () => {
  assert.deepEqual(T.diffWords("The quick fox", "The slow fox"), [
    { op: "eq", text: "The " }, { op: "del", text: "quick" }, { op: "ins", text: "slow" }, { op: "eq", text: " fox" },
  ]);
  assert.deepEqual(T.diffWords("", "new"), [{ op: "ins", text: "new" }]);
  assert.deepEqual(T.diffWords("old", ""), [{ op: "del", text: "old" }]);
  assert.deepEqual(T.diffWords("same", "same"), [{ op: "eq", text: "same" }]);
});

test("computeHunks joins changes closer than 3 unchanged words", () => {
  const close = T.computeHunks("The quick brown fox jumps", "The slow brown fox leaps");
  assert.deepEqual(close, [{ from: 4, to: 25, insert: "slow brown fox leaps" }]);
  const far = T.computeHunks("The quick brown red fox jumps", "The slow brown red fox leaps");
  assert.equal(far.length, 2);
  assert.deepEqual(far[0], { from: 4, to: 9, insert: "slow" });
  assert.deepEqual(far[1], { from: 24, to: 29, insert: "leaps" });
  assert.deepEqual(T.computeHunks("a b c", "a b c"), []);
  assert.deepEqual(T.computeHunks("", "Draft"), [{ from: 0, to: 0, insert: "Draft" }]);
});

test("property: all hunks give the new text, none give the old, any subset matches applyHunks", () => {
  const r = rng(20260913);
  for (let i = 0; i < 400; i++) {
    const oldText = randomText(r, 1 + Math.floor(r() * 30));
    const newText = r() < 0.1 ? randomText(r, Math.floor(r() * 10)) : mutate(r, oldText);
    const hunks = T.computeHunks(oldText, newText);
    assert.equal(T.applyHunks(oldText, hunks), newText, `all hunks: ${JSON.stringify([oldText, newText])}`);
    assert.equal(T.applyHunks(oldText, hunks, new Set()), oldText);
    for (let k = 1; k < hunks.length; k++) {
      assert.ok(hunks[k].from >= hunks[k - 1].to, "hunks overlap");
      const between = oldText.slice(hunks[k - 1].to, hunks[k].from);
      assert.ok(T.tokenize(between).filter(T.isWordToken).length >= 3, `hunks closer than 3 words: ${JSON.stringify(between)}`);
    }
  }
});

test("property: accepting hunks one at a time in any order gives the same text", () => {
  const r = rng(7);
  for (let i = 0; i < 150; i++) {
    const oldText = randomText(r, 4 + Math.floor(r() * 40));
    const newText = mutate(r, oldText);
    const prefix = "Title\n\n";
    const view = fakeView(prefix + oldText + "\n\nEnd.");
    const id = T.startChange(view, { title: "Improve", from: prefix.length, to: prefix.length + oldText.length, text: newText, status: "open" });
    const change = T.getChange(view.state, id);
    if (!change) {
      assert.equal(oldText, newText);
      continue;
    }
    const relative = change.hunks.map((hk) => ({ ...hk, from: hk.from - prefix.length, to: hk.to - prefix.length }));
    const keep = new Set(change.hunks.map((hk, idx) => (r() < 0.6 ? idx : -1)).filter((x) => x >= 0));
    for (const idx of shuffle(r, change.hunks.map((_hk, k) => k))) {
      const hk = change.hunks[idx];
      if (keep.has(idx)) assert.ok(T.acceptHunks(view, id, [hk.id]), "accept failed");
      else assert.ok(T.discardHunks(view, id, [hk.id]), "discard failed");
    }
    const expected = prefix + T.applyHunks(oldText, relative, keep) + "\n\nEnd.";
    assert.equal(view.state.doc.toString(), expected, JSON.stringify({ oldText, newText, keep: [...keep] }));
    assert.equal(T.hasChanges(view.state), false, "a finished change stays on screen");
    if (keep.size === change.hunks.length) assert.equal(view.state.doc.toString(), prefix + newText + "\n\nEnd.");
  }
});

test("accept all is one transaction and one undo step, discard all leaves the note", () => {
  const view = fakeView("The quick brown red fox jumps", [history()]);
  const id = T.startChange(view, { title: "Improve", from: 0, to: 29, text: "The slow brown red fox leaps", status: "open" });
  assert.equal(T.getChange(view.state, id).hunks.length, 2);
  const [first] = T.getChange(view.state, id).hunks;
  assert.ok(T.acceptHunks(view, id, [first.id]));
  assert.equal(view.state.doc.toString(), "The slow brown red fox jumps");
  assert.ok(T.acceptAll(view, id));
  assert.equal(view.state.doc.toString(), "The slow brown red fox leaps");
  undo(view);
  assert.equal(view.state.doc.toString(), "The slow brown red fox jumps", "one undo reverts only the last accept");
  undo(view);
  assert.equal(view.state.doc.toString(), "The quick brown red fox jumps");

  const other = fakeView("keep this text");
  const id2 = T.startChange(other, { title: "Shorten", from: 0, to: 14, text: "keep", status: "open" });
  assert.ok(T.discardAll(other, id2));
  assert.equal(other.state.doc.toString(), "keep this text");
  assert.equal(T.hasChanges(other.state), false);
});

test("positions map through edits around a live change, and an edit inside a hunk ends that hunk", () => {
  const doc = "Intro line.\nThe quick brown red fox jumps over.\nEnd.";
  const view = fakeView(doc);
  const from = doc.indexOf("The");
  const to = doc.indexOf(".\nEnd");
  const id = T.startChange(view, { title: "Improve", from, to, text: "The slow brown red fox leaps over", status: "open" });
  const [quick, jumps] = T.getChange(view.state, id).hunks;
  assert.equal(view.state.doc.sliceString(quick.from, quick.to), "quick");
  assert.equal(view.state.doc.sliceString(jumps.from, jumps.to), "jumps");

  // Typing before the change moves it.
  view.dispatch({ changes: { from: 0, insert: "Hello. " } });
  let c = T.getChange(view.state, id);
  assert.equal(view.state.doc.sliceString(c.hunks[0].from, c.hunks[0].to), "quick");
  assert.equal(view.state.doc.sliceString(c.hunks[1].from, c.hunks[1].to), "jumps");
  // Typing after it and deleting before it keep it in place.
  view.dispatch({ changes: { from: view.state.doc.length, insert: " Bye." } });
  view.dispatch({ changes: { from: 0, to: 7 } });
  c = T.getChange(view.state, id);
  assert.equal(view.state.doc.sliceString(c.hunks[0].from, c.hunks[0].to), "quick");
  // Typing at the edge of a hunk keeps the hunk.
  view.dispatch({ changes: { from: c.hunks[1].to, insert: "!" } });
  c = T.getChange(view.state, id);
  assert.equal(c.hunks.length, 2);
  assert.equal(view.state.doc.sliceString(c.hunks[1].from, c.hunks[1].to), "jumps");
  // An edit inside the first hunk ends its review and keeps the typed text.
  view.dispatch({ changes: { from: c.hunks[0].from + 2, insert: "i" } });
  c = T.getChange(view.state, id);
  assert.equal(c.hunks.length, 1);
  assert.ok(view.state.doc.toString().includes("quiick"));
  assert.ok(T.acceptAll(view, id));
  assert.equal(view.state.doc.toString(), "Intro line.\nThe quiick brown red fox leaps! over.\nEnd. Bye.");
});

test("an insertion hunk stays before text typed at its position", () => {
  const view = fakeView("Heading\n\nBody");
  const id = T.startChange(view, { title: "Continue", from: 9, to: 9, text: "New line. ", status: "open" });
  view.dispatch({ changes: { from: 9, insert: "typed " } });
  const c = T.getChange(view.state, id);
  assert.equal(c.hunks[0].from, 9);
  assert.ok(T.acceptAll(view, id));
  assert.equal(view.state.doc.toString(), "Heading\n\nNew line. typed Body");
});

test("streaming updates recompute hunks, and review moves between hunks", () => {
  const view = fakeView("one two three four five six seven");
  const id = T.startChange(view, { title: "Improve", from: 0, to: 33 });
  assert.equal(T.getChange(view.state, id).status, "streaming");
  assert.equal(T.acceptHunks(view, id), false, "a streaming change cannot be accepted");
  T.streamChange(view, id, { text: "one 2 three" });
  T.streamChange(view, id, { text: "one 2 three four five 6 seven", status: "open" });
  const c = T.getChange(view.state, id);
  assert.equal(c.hunks.length, 2);
  assert.ok(T.moveToHunk(view, 1));
  let st = T.reviewState(view.state);
  assert.deepEqual([st.index, st.total], [0, 2]);
  assert.ok(T.moveToHunk(view, 1));
  st = T.reviewState(view.state);
  assert.equal(st.index, 1);
  assert.ok(T.resolveHunkAt(view, true));
  assert.equal(view.state.doc.toString(), "one two three four five 6 seven");
  assert.equal(T.reviewState(view.state).total, 1);
});

test("stored suggestions draw one hunk per open edit", () => {
  const view = fakeView("See also nothing here.");
  const suggestion = {
    id: "s1", title: "Add See also links", authorKind: "plugin", authorId: "bookmark-wiki", status: "open",
    edits: [
      { id: "e1", anchor: { exact: "nothing" }, text: "[[Methods]]", status: "open" },
      { id: "e2", anchor: { exact: "gone" }, text: "x", status: "open" },
      { id: "e3", anchor: { exact: "here" }, text: "there", status: "discarded" },
    ],
  };
  const resolve = (anchor, text) => {
    const at = text.indexOf(anchor.exact);
    return at < 0 ? null : { start: at, end: at + anchor.exact.length };
  };
  const events = [];
  const state = EditorState.create({ doc: "See also nothing here.", extensions: [T.trackedChanges({ onSuggestion: (e) => events.push(e) })] });
  view.state = state;
  assert.equal(T.setSuggestions(view, [suggestion], { resolve, authorName: () => "Bookmark wiki" }), 1);
  const c = T.listChanges(view.state)[0];
  assert.equal(c.kind, "suggestion");
  assert.equal(c.author, "Bookmark wiki");
  assert.deepEqual(c.hunks.map((hk) => [hk.editId, hk.insert]), [["e1", "[[Methods]]"]]);
  assert.ok(T.acceptHunks(view, c.id));
  assert.deepEqual(events, [{ suggestion, editIds: ["e1"], action: "accept" }]);
  assert.equal(view.state.doc.toString(), "See also nothing here.", "a stored suggestion is applied by the server");
});

test("review actions for the phone keyboard bar", () => {
  const view = fakeView("alpha beta gamma delta epsilon zeta");
  const id = T.startChange(view, { title: "Improve", from: 0, to: 35, text: "alpha BETA gamma delta epsilon ZETA", status: "open" });
  const actions = T.reviewActions(view);
  assert.deepEqual(actions.map((a) => a.id), ["prev", "next", "accept", "discard", "accept-all", "discard-all"]);
  assert.equal(actions.find((a) => a.id === "accept").disabled, true, "no hunk at the cursor yet");
  actions.find((a) => a.id === "next").run();
  const again = T.reviewActions(view);
  assert.equal(again.find((a) => a.id === "accept").disabled, false);
  again.find((a) => a.id === "accept").run();
  assert.equal(view.state.doc.toString(), "alpha BETA gamma delta epsilon zeta");
  T.reviewActions(view).find((a) => a.id === "accept-all").run();
  assert.equal(view.state.doc.toString(), "alpha BETA gamma delta epsilon ZETA");
  assert.equal(T.getChange(view.state, id), null);
});
