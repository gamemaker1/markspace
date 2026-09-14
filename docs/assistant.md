# AI in the Editor

This document specifies the AI features of the editor: the ask bar, tracked
changes, image actions, image generation and editing, the AI rail tab, and
the usage views. `ai.md` covers context resolution, budgets and usage
accounting. `plugins.md` covers stored suggestions, which the editor draws
with the same tracked-change display.

## Modules

The client code lives in `internal/web/static/js/ai/`. `editor.js` holds the
wiring only.

| Module | Holds |
| --- | --- |
| `askbar.js` | the ask bar, the suggested action table, the margin spark and `Mod-j` |
| `tracked.js` | the `trackedChanges` extension, the word diff and the hunks |
| `imageactions.js` | the image action row, generation placeholders and the image editor |
| `aitab.js` | the AI rail tab |
| `composer.js` | the text field, mentions, chips and model chip shared by the bar and the tab |
| `common.js` | the mention parser, the model list, the streaming client and the answer card |

## Principles

1. The user stays in the text. AI opens at the cursor, the selection or the
   image, in the flow of the note. Only Edit image opens a Dialog.
2. Context is inferred and visible. The request shows what it will send, and
   the user can add or remove any part.
3. Every change can be reviewed. Nothing the model writes enters the note
   until the user accepts it.
4. Images are first-class. An image in a note has the same AI actions as
   text.

## The Ask Bar

### Opening

| Trigger | Where it opens |
| --- | --- |
| `Mod-j` | under the selection, or under the cursor line |
| The spark button in the left margin of the cursor line, shown while the editor has focus on desktop | under that line |
| Ask in the selection toolbar | under the selection |
| `/ai` in the slash menu | under the line |
| `/image` in the slash menu | under the line, in Generate image mode |
| The spark key in the phone keyboard bar | docked above the keyboard |
| Ask in the image toolbar | under the image, with the image as context |

The bar is a CodeMirror block widget in the flow of the note, never a
floating layer. It holds a text input that grows to 6 lines, a row of context
chips, a row of suggested actions, an output mode switch and a model chip. The
model chip opens a SelectPanel of capable models for the task, from
`models.md`. `ui.md` applies to everything in this document.

On a phone every trigger docks the bar above the keyboard, inside the keyboard
bar. While the network is off, every trigger stays visible in the inactive style,
and the bar shows an InlineMessage warning "AI needs a connection". A
reader without edit access gets the Answer mode only.

### Suggested Actions

The bar reads the syntax tree at the cursor and offers actions for what is
there. Typing any text replaces the suggestions with a free instruction.

| Cursor or selection | Actions |
| --- | --- |
| Selected prose | Improve, Shorten, Fix spelling and grammar, Change tone, Translate, Explain |
| Empty line under a heading | Draft this section, Outline, Continue |
| End of a paragraph | Continue, Summarize above |
| Selected list | Sort, Group, Turn into table |
| Inside a table | Add column, Fill empty cells, Summarize table, Sort by column |
| Code block | Explain code, Add comments, Find bugs |
| Link to a web page | Summarize page, Quote key points |
| Wikilink | Summarize linked note, Compare with this note |
| Image | the image actions below |

Change tone and Translate open a second row of choices in the bar, such as
formal, friendly and concise, or the last 5 languages used. They never open a
dialog.

### Context Chips

The default context is the selection and its section. For a note shorter
than 4000 characters it is the selection and the whole note. Each chip shows
its kind and title, and removing a chip removes that reference.

Typing `@` expands a list of matches inside the bar, under the input:

| Mention | Chip | Request reference |
| --- | --- | --- |
| `@` note title | Note | `{kind: "note", noteId}` |
| `@` note title then `#` heading | Section | `{kind: "section", noteId, heading}` |
| `@` file name | File, or Image for images | `{kind: "file", fileId}` |
| a pasted `https://` URL | Page | `{kind: "url", url}` |
| `@linked` | Linked notes | `{kind: "linked", noteId}` |
| `@backlinks` | Backlinks | `{kind: "backlinks", noteId}` |
| `@space` | Search this space | `{kind: "search", query}` built from the instruction |

The end of the chip row shows an estimate of the prompt size in tokens, at 4
characters per token. It turns to the warning colour past 75 percent of the
context budget.

### Output Modes

`AIRequest.output` is `replace`, `insert` or `answer`.

| Mode | Default when | Result |
| --- | --- | --- |
| `replace` | a selection exists and the action edits text | tracked change on the selection |
| `insert` | no selection, or the action writes new text such as Draft or Continue | tracked insertion at the cursor |
| `answer` | Explain, Summarize, Compare, Find bugs, or a free instruction ending in `?` | an answer card under the bar |

The switch in the bar changes the mode before sending. The server adds an
instruction for the mode, so the model returns only the replacement text,
only the inserted text, or a markdown answer.

### Streaming and Review

- Text streams into the tracked change or the answer card as it arrives.
- `Esc` stops the stream. A second `Esc` discards the result and closes the
  bar.
- While the bar shows a result, typing in the bar and pressing `Enter` refines it.
  The request carries the previous result and the new instruction as history,
  and the new result replaces the old one in place.
- The answer card renders markdown with citation chips. A citation opens the
  note or file at the cited passage. The card's actions are Insert below,
  Replace selection, Copy, and Continue in AI tab. On a phone the `[n]` marks
  in the text are plain, and the source chips under the answer take the taps.
- `Tab` in the empty bar field accepts every hunk of a result and returns the
  cursor to the note.
- Accepting or discarding every hunk of a result closes the bar.
- Starting a tracked result collapses the selection, so the selection
  highlight does not cover the hunks.

## Tracked Changes

One CodeMirror extension, `trackedChanges`, draws both live AI results and
stored suggestions from `plugins.md`.

- The extension computes a word-level diff between the old and the new text
  and splits it into hunks. A hunk is a run of changed words, separated from
  the next hunk by at least 3 unchanged words.
- Removed text is struck through in `--danger` on `--danger-soft`. Inserted
  text is underlined in `--highlight-edge` on `--highlight`. The colours meet
  WCAG AA in both themes, and neither is yellow.
- On desktop, each hunk has Accept and Discard buttons in the left margin
  of its first line, always visible while the change is open. When two hunks
  start on one line, the pair acts on both. On a phone the
  keyboard bar switches to review mode with Previous, Next, Accept, Discard,
  Accept all and Discard all, and nothing floats over the text.
- A header above the first hunk shows the author and title, such as
  "AI · Improve" or "Bookmark wiki · Add See also links", with Accept all,
  Discard all and, for live AI results, Save as suggestion. On a phone the
  header keeps Save as suggestion only, because the keyboard bar holds the
  review buttons. Save as suggestion shows when
  `GET /api/notes/{id}/suggestions` answers, and a 404 hides it.

Keys while tracked changes are visible:

| Key | Action |
| --- | --- |
| `Tab` | accept all hunks of the change under the cursor, or of the only change |
| `Esc` | discard all, or stop a running stream |
| `Alt-]`, `Alt-[` | move to the next or previous hunk |
| `Mod-Shift-Enter` | accept the hunk at the cursor |
| `Mod-Shift-Backspace` | discard the hunk at the cursor |

Accepting applies the hunk as one editor transaction with `userEvent:
"input.ai"`, so one undo reverts it. A live AI result that the user edits
around keeps its position through CodeMirror position mapping. An edit inside
a hunk ends the review of that hunk and keeps the text as the user edited it.
Text typed at the edge of a hunk stays outside the hunk.

A stored suggestion is one hunk per open edit. Accepting or discarding it calls
`POST /api/suggestions/{id}/accept` or `discard` with the edit ids, and the
note changes through sync.

Save as suggestion stores a live result through `POST /api/notes/{id}/suggestions`,
so another user, or the same user later, can review it.

## Images

### Image Toolbar

Selecting or tapping an image widget in live preview shows a slim action
row directly under the image, in the flow of the note. A tap picks the image
without moving the cursor, and the next change of selection drops the pick. On a phone the keyboard
bar shows the same actions instead. The AI actions need a provider with `vision: true`. Without one, they are
hidden and a single "Add a vision model" link points to the AI settings.

| Action | Result |
| --- | --- |
| Describe | an answer card under the image, with Use as alt text and Insert as caption |
| Alt text | a tracked change on the alt text of the image syntax |
| Extract text | a tracked insertion under the image, keeping line breaks, lists and tables as markdown |
| Turn into notes | a tracked insertion under the image with headings and lists built from a whiteboard, slide, chart or diagram |
| Ask | the ask bar with the image as its first chip |
| Edit image | the image edit panel, when a provider has `imageEditing: true` |
| Variations | 4 new images from the alt text, or from a description when the alt text is empty, when a provider has `imageGeneration: true` |
| Open, Rename file, Copy link | non-AI actions |

### Paste and Drop

A pasted or dropped image shows inline upload progress and nothing else. Its
actions appear only when the user selects it.

### Generating an Image

`/image` in the slash menu, or the Generate image mode in the ask bar, needs
a provider with `imageGeneration: true`.

1. The user writes a prompt and picks a size from the provider's list, 1024 by
   1024 by default, and a count from 1 to 4.
2. A placeholder widget with a progress bar takes the image's place. The
   request streams progress frames.
3. The server saves each result as a file in the note's folder, or in the
   `pasteFolder` from `files.md`, named after the first 6 words of the prompt,
   and inserted as `![prompt](name.png)` as a tracked insertion. With more
   than one image, the insertion shows them side by side and the user accepts
   any of them.
4. Discarding an insertion moves its file to the trash.
5. When the server cannot save files, it answers 415, and the client asks
   again with `save: none`. The insertion then holds data URLs.

### Editing an Image

Edit image opens an extra-large Dialog titled "Edit image" with a close
button. On a narrow viewport the Dialog is full screen. It shows the image with a brush for marking the area to change, a
prompt at the bottom, and a Keep unmasked areas switch.

- The view asks for the edit with `save: none` and shows the result in a
  before and after comparison with a slider. Save uploads that result through
  `PUT /api/files/{id}/content` for a new version, or `POST /api/files` for a
  copy.
- The view has one Save button. It saves a new version of the file when no
  other note embeds it, and a copy, `<stem> edited.<ext>`, otherwise, updating
  this note's link. A line above the button states which will happen and names
  the other notes, and a link beside it switches to the other choice.

## Providers for Images

`core.AIProviderConfig` has these image fields:

```go
ImageGeneration bool     `json:"imageGeneration" yaml:"imageGeneration"`
ImageEditing    bool     `json:"imageEditing" yaml:"imageEditing"`
ImageModel      string   `json:"imageModel,omitempty" yaml:"imageModel"`
ImageSizes      []string `json:"imageSizes,omitempty" yaml:"imageSizes"` // "1024x1024"
// ComfyUI workflow templates in ComfyUI API format.
ComfyGenerate   string   `json:"comfyGenerate,omitempty" yaml:"comfyGenerate"`
ComfyEdit       string   `json:"comfyEdit,omitempty" yaml:"comfyEdit"`
```

The provider kinds are `openai`, `ollama`, `anthropic` and `comfyui`.

```go
// ImageGenerator is implemented by providers that can create or edit images.
type ImageGenerator interface {
	GenerateImages(ctx context.Context, req AIImageRequest, progress func(done, total int)) (*AIImageResult, error)
}

type AIImageRequest struct {
	Prompt         string
	NegativePrompt string
	Size           string   // "1024x1024"
	N              int      // 1 to 4
	Model          string
	Seed           int64    // 0 means random
	Source         *AIImage // set for an edit
	Mask           *AIImage // PNG, transparent where the image may change
	Queue          func(position int) // jobs ahead of this one, 0 when running
}

type AIImageResult struct {
	Images        []AIImage
	RevisedPrompt string
	Model         string
	Usage         AIImageUsage
}
```

| Protocol | Generate | Edit |
| --- | --- | --- |
| OpenAI-compatible | `POST /v1/images/generations` with `response_format: "b64_json"` | `POST /v1/images/edits`, multipart `image`, `mask`, `prompt` |
| ComfyUI | copy the generate template, replace `{{prompt}}`, `{{negative}}`, `{{width}}`, `{{height}}`, `{{seed}}` and `{{batch}}`, `POST /prompt`, poll `GET /history/{id}` every second, fetch outputs with `GET /view` | upload the source and mask with `POST /upload/image`, then run the edit template with `{{image}}` and `{{mask}}` as well |
| Ollama, Anthropic | not supported | not supported |

ComfyUI progress comes from polling. `GET /history/{id}` shows completion,
and `GET /queue` shows the queue position before the job starts. A generate
or edit call times out after `timeoutSeconds`, default 300 for image calls.
The repository ships a generate and an edit template for an SDXL checkpoint,
with the checkpoint name as a setting.

Usage records an image request with feature `image`, `images_out`, and the
price per image.

## Server API

- `AIRequest` has `output` (`replace`, `insert` or `answer`) and `history`
  entries of `{role, content}` for refinement.
- The `file` context kind names a file with `fileId`.
- `POST /api/ai/image/stream` takes
  `{spaceId, noteId?, parentId?, prompt, negativePrompt?, size, n, fileId?, mask?, providerId?, model?, save, name?}`,
  where `mask` is a base64 PNG and `save` is `none`, `new`, `version` or
  `copy`. It streams `progress` frames of `{done, total, queuePosition?}`
  and a final `done` frame of `{files: [File], images: [{dataUrl}] when save is none, usage}`.
- The image request also takes `route`, a plugin route such as
  `plugin.<id>.<purpose>`, and `pluginId`. A plugin route tries its own
  targets first and then the image route. Usage rows and budgets charge
  `pluginId`. Only the plugin host sets `pluginId`, and the JSON API clears a
  client value.
- `POST /api/ai/image/stream` opens the stream with the first progress frame.
  A request that fails before any work answers with a JSON error, and a later
  failure sends an `error` event.
- `GET /api/ai/threads?noteId=` returns the actor's conversations for a note,
  newest first, each with its messages.
  `POST /api/ai/threads` takes `{noteId, title?}` and starts a conversation.
  `POST /api/ai/threads/{id}/messages` takes
  `{content, context?, scope?, providerId?, model?}`, stores the turn and
  streams the answer as `delta` events. The `done` event adds `route`,
  `userMessage` and `message` to the fields of `/api/ai/stream`.
  `PUT /api/ai/threads/{id}/messages/{messageId}/applied` takes `{applied}`.
  `DELETE /api/ai/threads/{id}` deletes the conversation.
- Threads are private to their user. A thread of another user answers 404 on
  every route. A thread without activity for 90 days is hidden from reads,
  listing a note's threads deletes the actor's expired threads, and the daily
  retention job deletes the rest with `App.PurgeAIThreads`.

```sql
CREATE TABLE ai_threads (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, note_id TEXT NOT NULL DEFAULT '',
  space_id TEXT NOT NULL DEFAULT '', title TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE ai_messages (id TEXT PRIMARY KEY, thread_id TEXT NOT NULL REFERENCES ai_threads(id) ON DELETE CASCADE,
  role TEXT NOT NULL CHECK (role IN ('user', 'assistant')), content TEXT NOT NULL, context TEXT NOT NULL DEFAULT '[]', sources TEXT NOT NULL DEFAULT '[]',
  applied INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL);
```

## The AI Rail Tab

The AI tab holds a conversation about the note.

- Earlier turns are listed oldest first. Each answer shows citation chips,
  and a turn applied to the note carries an Applied badge.
- The composer at the bottom uses the same `@` mentions and chips as the ask
  bar. A scope switch chooses between This note and This space. This space
  adds a `search` reference built from the message.
- Continue in AI tab from the ask bar opens the tab with the answer as a chip.
- Clear opens a ConfirmationDialog, and confirming deletes the conversation.
- Each answer's actions are Insert at cursor, which makes a tracked
  insertion, Copy, and Save as note, which creates a note in the same folder
  with the answer and its sources as links.
- A usage line at the foot shows today's tokens, this month's tokens and
  cost, and budget use as a thin bar. The line uses the warning colour at 80
  percent and links to the usage page.

## Usage Views

Both views read the routes in `ai.md` and use the form and list patterns of
`ui.md`.

`/settings/usage`:

- A summary row with today, this month, cost this month, and budget use.
- A 30-day bar chart of daily tokens, stacked by feature, drawn as inline SVG
  with values on hover and a table fallback for screen readers.
- A table by model with calls, tokens in, tokens out, images, average
  duration and cost.
- The 20 most recent calls with feature, note link, model, tokens, duration
  and status.

`/admin/usage`:

- Filters for date range, person, provider, model and plugin.
- The same chart, stacked by person or by plugin.
- Tables by person, provider, model and plugin.
- The slowest and the failed calls.
- A budgets table with the instance defaults and per-person and per-plugin
  overrides. A row menu offers Set budget, Remove override and Lift until
  month end.

## Testing

- JS unit tests:
  - the word diff and hunk splitting, including property tests that applying
    all hunks yields the new text and discarding all yields the old text,
  - hunk accept and discard in any order,
  - position mapping through edits made around a live change,
  - the suggested action table for each syntax context,
  - the mention parser.
- Go tests:
  - the OpenAI image endpoints and ComfyUI flow against `httptest` fakes,
    including polling, timeouts, and malformed or huge outputs, with images
    capped at 32 MiB each,
  - output mode prompts,
  - thread privacy, with a differential check that no user can read another's
    thread,
  - usage rows for image calls.
- Browser tests at desktop and 390 px phone width:
  - ask bar replace, insert and answer, with refinement,
  - partial hunk acceptance,
  - Save as suggestion,
  - image describe, alt text and extract text,
  - generate with 2 results, accepting one,
  - edit with a mask and Save as copy,
  - the AI tab conversation and Insert at cursor,
  - the usage page chart and tables.
