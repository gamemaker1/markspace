# Anchored Comments

A comment can refer to a passage of a note. The note stays a plain markdown
file, so an anchor has to survive edits made in the editor, through the API
and outside the application. This document specifies the anchor, how it is
found again, how it follows edits, and the interface built on it.

## The Anchor

`core.CommentAnchor` is stored as JSON in the comment row.

| Field | Meaning |
| --- | --- |
| `exact` | The selected text of the markdown body, without frontmatter |
| `prefix` | Up to 32 code units of body text before the selection |
| `suffix` | Up to 32 code units of body text after the selection |
| `start`, `end` | Offsets of the selection in the body when last known |

Offsets count UTF-16 code units, the unit that JavaScript strings and
CodeMirror use for positions. An emoji outside the Basic Multilingual Plane
takes two units. The Go side converts with `unicode/utf16`.

A root comment may carry an anchor. A reply never does, and a comment without
an anchor is a general comment on the whole note.

## Making an Anchor

`make(body, start, end)`:

1. Clamp both offsets into `[0, len(body)]`, and swap them if `start > end`.
2. If `start` falls between the two halves of a surrogate pair, move it back
   one unit. If `end` does, move it forward one unit.
3. An empty selection makes no anchor.
4. `prefix` is the text from `start - 32` to `start`, and `suffix` the text
   from `end` to `end + 32`, both clamped to the body. If the prefix would
   begin inside a pair, it starts one unit later. If the suffix would end
   inside a pair, it ends one unit earlier.

The server accepts an anchor whose `exact` is 1 to 5000 units long. It cuts
`prefix` to its last 32 units and `suffix` to its first 32 units, never
splitting a pair, and rejects negative offsets with a 400.

## Finding an Anchor

`resolve(body, anchor)` returns `start`, `end` and a state.

1. An empty `exact` is `orphaned`, with `start` and `end` of -1.
2. Collect every offset `i` where `exact` occurs, overlapping occurrences
   included. With none, the state is `orphaned`.
3. Score each occurrence as the length of the common suffix of `prefix` and
   the text before `i`, plus the length of the common prefix of `suffix` and
   the text after the occurrence.
4. Pick the highest score. Break a tie by the smallest distance `|i - start|`,
   then by the smaller `i`.
5. The state is `exact` when the winner starts at `start` and `end` equals
   `start` plus the length of `exact`, and `moved` otherwise.

The occurrence at the stored offsets gets the same score as every other
occurrence. If edits shift the stored offsets onto another copy of the same
words, accepting the text found at the offsets would move the comment to the
wrong passage. The context score selects the original passage, and the stored
offsets break every tie.

Matching compares code units exactly, so a change of case orphans the anchor.

`tools/anchors.py` implements both functions and generates
`test/vectors/anchors.json`. The Go package `internal/anchor` and the
browser module `internal/web/static/js/anchors.js` must reproduce every case in that file.
Regenerate it after any change to the rules:

```
python3 tools/anchors.py > test/vectors/anchors.json
```

## Following Edits

**Edits through the application.** `UpdateNote` and `RestoreVersion` receive the
old and the new body. For each anchored comment on the note, the server:

1. Resolves the anchor against the old body. An orphaned anchor is left alone.
2. Computes a line-then-character diff from the old body to the new one and
   maps both offsets through it, counting UTF-16 units.
3. Treats text inserted exactly at `start` as outside the passage, so `start`
   moves after it. Text inserted exactly at `end` is also outside, so `end`
   stays before it. Text inserted strictly inside the passage becomes part of
   it.
4. Moves an offset inside deleted text to where the deletion was.
5. If the mapped passage is empty, keeps the old anchor unchanged, which then
   resolves as orphaned. Otherwise it sets `exact` to the new passage and
   rebuilds `prefix` and `suffix` from the new body.

Editing words inside a commented passage keeps the comment on the edited
text.

**Edits outside the application.** The watcher and the startup scan read only
the new file, so no diff exists. Listing resolves each anchor against the
current body instead. When the state is `moved`, the server writes the new
offsets back, so later reads are exact.

**Deleted text.** An orphaned comment keeps its quoted text and appears in a
"Text no longer found" group. The server never deletes it.

## API

`routes.md` lists the comment routes. `POST /api/notes/{id}/comments` accepts an optional
`anchor`. `GET /api/notes/{id}/comments` returns each root comment with
`anchor` resolved against the current body and `anchorState` set to `exact`,
`moved` or `orphaned`. The response sorts threads by `anchor.start`, with
general comments after anchored ones and orphaned threads last.

## Browser Modules

`internal/web/static/js/anchors.js` exports these functions.

```js
export function makeAnchor(body, start, end): CommentAnchor | null
export function resolveAnchor(body, anchor): {start, end, state}
export function alignRendered(source, rendered): Int32Array
  // for each UTF-16 unit of rendered text, the offset of the matching unit in
  // source, or -1; a greedy left-to-right match that skips markup
export function sourceRangeFromDOM(root, range, body): {start, end} | null
  // maps a DOM Range inside rendered markdown to body offsets
export function highlightAnchors(root, body, items): () => void
  // items: [{id, start, end, active}]; wraps the matching rendered text in
  // <mark class="comment-anchor" data-comment-id="...">, returns an undo function
```

In `internal/web/static/js/markdown.js`, `render(md, {sourceMap: true})` adds `data-sp="start:end"` to
every rendered block (paragraph, heading, list item, table cell, block quote
paragraph and code block), giving the UTF-16 offsets of that block in `md`.
`sourceRangeFromDOM` and `highlightAnchors` use these attributes plus
`alignRendered` to move between rendered text and body offsets. Offsets that
land inside markup, such as `**` or a link URL, snap to the nearest text.

`internal/web/static/js/store.js` caches threads with `putComments(itemId, list)`
and `listComments(itemId)`, so comments stay readable offline. Creating a comment needs a connection.

## Interface

- **Side panel.** Comments open in the right rail as a Comments tab on
  desktop, and as a bottom sheet on phones. Threads appear in document order,
  each with its quoted passage above it. General comments and a "Text no
  longer found" group follow. Resolved threads are hidden behind a toggle
  with a count.
- **Creating a comment.** Selecting text in reading view or in the editor
  shows a Comment button in the selection toolbar. It opens the panel with a
  composer headed by the quoted selection. On phones a floating Comment
  button appears near the selection, clear of the system selection menu.
  General comments start from a "Comment on this note" button in the panel.
- **Linking text and threads.** Anchored passages are highlighted in reading
  view and in the editor. Clicking a highlight opens and focuses its thread,
  and hovering or focusing a thread emphasizes its passage without moving the
  page. Clicking the quoted passage in a thread scrolls the note to it and
  flashes it, in reading view and in the editor. Resolved threads lose their highlight.
- **Editor.** The editor draws highlights as CodeMirror decorations and maps
  them through each change, so they move while the user types. Saving sends
  the new body, and the server maps the stored anchors.
- **Server pages without JavaScript.** The note page lists threads in a side
  column with their quoted passages, without highlighting in the body.
