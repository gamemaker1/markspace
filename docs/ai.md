# AI Context

An AI request names the context the answer should draw on. The server reads
each reference with the user's permissions, fits the result into a budget,
and tells the client which sources went in and which it left out.

## Request

`POST /api/ai/run` and `POST /api/ai/stream` take `core.AIRequest`. The
`context` field lists references in priority order:

```json
{
  "task": "summarize",
  "noteId": "0abc...",
  "text": "the selected paragraph",
  "context": [
    {"kind": "selection", "text": "the selected paragraph"},
    {"kind": "section", "noteId": "0abc...", "heading": "Rollout plan"},
    {"kind": "linked", "noteId": "0abc..."},
    {"kind": "file", "fileId": "0def..."},
    {"kind": "url", "url": "https://example.com/spec"},
    {"kind": "search", "query": "rollback procedure"}
  ]
}
```

The result carries `sources`. A citation `[n]` in the answer refers to
`sources[n-1]`, and skipped sources keep their position. `/api/ai/run` returns them in the result body,
and `/api/ai/stream` puts them on the `done` frame. Each source has `kind`, a
title, and the note id, file id or URL it came from. `truncated` means
the budget cut it short. `skipped` gives the reason a reference was left out.

## Resolution

| Kind | Resolves to | Access and limits |
| --- | --- | --- |
| `selection` | `text` as given | none beyond the budget |
| `note` | the note body | view access on the note |
| `section` | the body under the heading whose text matches `heading`, ignoring case and `#` marks, down to the next heading of the same or higher level | view access, and `skipped: "heading not found"` when absent |
| `linked` | notes the note links to by wikilink or relative markdown link, from the index link table, one level deep, at most 10 | each target needs view access, and hidden targets are skipped without naming them |
| `backlinks` | notes linking to the note, at most 10 | as above |
| `file` | a file named by `fileId`: an image as an image; `text/*`, JSON, YAML, CSV and markdown as UTF-8 text; a PDF as extracted text | view access on the file |
| `url` | the readable text of a web page | see below |
| `search` | the top chunks from the note's space by embedding similarity, or the top full-text hits when no embedding provider exists | per-note view access |

`useContext: true` on a request without `context` acts as one `search`
reference built from the question, for every task. A `search` reference needs
`noteId` or `spaceId` on the request. A reference to a note or file the
user cannot view comes back with an empty title and `skipped: "not found"`.
Linked notes and backlinks the user cannot view are dropped without a source.

## Budget

`ai.contextChars` in the configuration sets the budget, 48000 characters by
default. The task text, meaning the selection or the note body, is charged
first. For `ask` it takes at most half the budget. References fill what
remains, in order. Each linked note, backlink, URL or text
file takes at most 8000 characters. A reference that no longer fits is
cut and marked `truncated`. Once the budget is spent, every later reference
gets `skipped: "context budget reached"`.

A request carries at most 20 references and at most 5 web pages. The server
reads at most 32 MB of any one image or PDF. Each `excerpt` in the response is
cut to 300 characters, and the model still receives the full resolved text.

## Images

An image file becomes an `AIImage` on the final user message. The server
decodes PNG, JPEG, GIF and WebP headers. It downscales an image whose longest
side exceeds 1568 px, keeping PNG for images with transparency and writing JPEG
at quality 85 otherwise. A request carries at most 5 images of at most 5 MB
each after scaling. A request with images needs a model with image input, so
routing looks for one in the route. When no model of the route accepts images,
image references get `skipped: "model does not accept images"` and a text model
answers. The `describe` and `extract` tasks use the `vision` route, and without
a model that accepts images the request fails with 400, because those tasks
mean nothing without the image.

Each provider protocol carries images its own way:

| Protocol | Encoding |
| --- | --- |
| OpenAI | `content` becomes an array of `{"type":"text"}` and `{"type":"image_url","image_url":{"url":"data:<type>;base64,..."}}` parts |
| Ollama | `images` on the message, as base64 strings |
| Anthropic | `content` blocks of `{"type":"image","source":{"type":"base64","media_type":...,"data":...}}` before the text block |

## Web Pages

The server fetches a `url` reference itself, with these rules:

- Only `http` and `https`, at most 3 redirects, a 10 s timeout and a 2 MB
  body cap.
- Every connection target is checked at dial time, after DNS resolution and
  again after each redirect. Loopback, private (RFC 1918), carrier-grade NAT
  (100.64.0.0/10), link-local, unique local (fc00::/7), multicast and
  unspecified addresses are refused unless `ai.allowPrivateURLs` is true.
  Checking at dial time closes the gap where a hostname resolves to a public
  address during validation and a private one on connect.
- Accepted types are `text/html`, `text/plain`, `text/markdown` and
  `application/json`. Anything else gets `skipped`.
- HTML nested deeper than 512 elements goes through a flat token scan instead
  of the tree walk, which bounds the parser's work on hostile pages.
- HTML becomes text by walking the parsed tree, dropping `script`, `style`,
  `noscript`, `nav`, `header`, `footer`, `aside` and `form`, and keeping
  headings, paragraphs, list items, table cells and preformatted blocks. The
  page `<title>` becomes the source title.

## Prompt Layout

1. A system message with the task instruction. A request with `output` adds
   the instruction of that output mode at the end, and a canned task with an
   `instruction` adds it as an additional instruction.
2. A system message titled Context, listing each resolved source as
   `[n] <kind>: <title> (<url or path>)` followed by its text. The instruction
   asks the model to cite sources as `[n]`.
3. Up to 10 earlier turns from `history`. For `ask` they are the
   conversation. For any other task they are the previous prompt and result of
   a refinement, and the system message then asks for a complete new result.
4. The final user message holds the task text (the selection, or the note
   body when no selection is given), the question for `ask`, and the images.

## Tasks

`explain` explains the text or media for a reader new to the subject.
`describe` writes a description of the attached images, one short paragraph
each, suitable as alt text. `extract` transcribes the text visible in the
attached images or files, preserving line breaks and tables as markdown.

## Editor Actions

`assistant.md` specifies the editor surfaces. The table lists the request each
one sends.

| Where | Action | Request |
| --- | --- | --- |
| Ask bar, selected text | Improve, Shorten, Fix spelling and grammar, Change tone, Translate, Explain, a free instruction | the task with `output` `replace` or `answer`, the selection as `text`, and `selection` plus `note` or `section` references |
| Ask bar, cursor | Draft this section, Outline, Continue, Summarize above, a free instruction | the task with `output` `insert` or `answer` and the text before the cursor as `text` |
| Ask bar, table or code block | Add column, Fill empty cells, Summarize table, Sort by column, Explain code, Add comments, Find bugs | the task with the whole block as `text` |
| Ask bar, link or wikilink | Summarize page, Quote key points, Summarize linked note, Compare with this note | the task with a `url` reference or a `note` reference for the target |
| Ask bar, refinement | a new instruction after a result | the same task with `instruction` and `history` |
| Image | Describe, Alt text, Extract text, Turn into notes, Ask | `describe`, `extract` or `custom` on the `vision` route with a `file` reference |
| Image | Generate image, Variations, Edit image | `POST /api/ai/image/stream` |
| AI tab | a message | `POST /api/ai/threads/{id}/messages`, which sends `ask` on the `chat` route |

Replace and insert results appear as tracked changes. The note keeps its text
until the user accepts a hunk. `Tab` accepts every hunk of the change, `Esc`
discards it, and one undo reverts one accept.

## Image Providers

A provider with `imageGeneration: true` creates images from a prompt, and a provider with `imageEditing: true` changes an existing image. Only the `openai` and `comfyui` kinds accept these flags. The server gets a `core.ImageGenerator` from `Registry.ImageGenerator(cfg, edit)`. That call fails with `core.ErrUnsupported` when the protocol has no image support or the provider lacks the flag for the call.

| Setting | Effect |
| --- | --- |
| `imageModel` | The image model. For ComfyUI it is the checkpoint file name |
| `imageSizes` | The sizes a user may pick. A generation without a size takes the first entry |
| `comfyGenerate`, `comfyEdit` | Workflow templates that replace the built-in SDXL workflows |
| `timeoutSeconds` | The timeout of one image call. 0 means 300 s, and the configuration file fills 300 for a `comfyui` provider or a provider with an image flag |

Every image call follows these rules:

- N is clamped to 1 through 4.
- Without `imageSizes`, a size must be WIDTHxHEIGHT with 3 or 4 digits on each side and each side at most 4096. A generation without a size uses 1024x1024. An edit without a size keeps the size of the source.
- A source image must be PNG, JPEG or WebP, and a mask must be PNG. A mask is transparent where the image may change.
- The server reads the media type of a returned image from its first bytes and accepts only PNG, JPEG and WebP.
- Each image may be at most 32 MiB, and one call reads at most 140 MiB.
- `AIImageResult.Usage` carries the images in and out, and the token counts when the provider reports them.

### OpenAI-Compatible

A generation sends `POST {baseUrl}/v1/images/generations` with `{model, prompt, size, n, response_format: "b64_json"}`. An edit sends `POST {baseUrl}/v1/images/edits` as multipart form data with the `image` file, the optional `mask` file and the fields `prompt`, `model`, `size`, `n` and `response_format`. Both calls carry the API key and the provider headers. The protocol has no field for the negative prompt or the seed, so the server does not send them.

A server that refuses `response_format` with a 400 or 422 gets the request again without the field, and later calls leave it out. An answer with `url` instead of `b64_json` is downloaded. The URL and every redirect must use the scheme and host of `baseUrl`, and a download elsewhere fails with `core.ErrForbidden`. The first `revised_prompt` becomes `RevisedPrompt`.

### ComfyUI

A ComfyUI provider has no chat and no embeddings. `Complete`, `Stream` and `Embed` return `core.ErrUnsupported`. Routing never gives a `comfyui` model a text or embeddings task, and a route save that tries it fails with 400. The model list holds the checkpoint names from `GET /object_info/CheckpointLoaderSimple`.

A template is a workflow in ComfyUI API format, which is the JSON object ComfyUI exports with "Save (API)". The server parses the template and walks its values. A string equal to a placeholder becomes the typed value. The server replaces a placeholder inside a longer string as text, in a single pass. Object keys stay unchanged. The server encodes the result again, so a prompt cannot change the JSON structure.

| Placeholder | Value |
| --- | --- |
| `{{checkpoint}}` | The request model, else `imageModel`. A template that uses it needs one of the two |
| `{{prompt}}`, `{{negative}}` | The prompt and the negative prompt, as strings |
| `{{width}}`, `{{height}}` | Numbers from the size. An edit without a size uses the source size, and a generation without one uses 1024 |
| `{{seed}}` | A number. Seed 0 in the request becomes a random seed from 1 to 2^53-1 |
| `{{batch}}` | The image count, as a number |
| `{{image}}`, `{{mask}}` | Edit only. The uploaded file names that a `LoadImage` node reads |
| `{{denoise}}` | Edit only. 0.9 with a mask, 0.6 without one |

The built-in templates live in `internal/ai/comfy/`. `sdxl-generate.json` runs an empty latent through `KSampler` and `SaveImage`. `sdxl-edit.json` loads the source and the mask, encodes the source, applies the mask alpha with `SetLatentNoiseMask`, repeats the latent for the batch and samples with `{{denoise}}`.

A call runs these steps:

1. For an edit, upload the source and the mask with `POST /upload/image`, as multipart `image`, `overwrite=true` and `type=input`, under unique file names. An edit without a mask uploads a fully transparent 8x8 PNG, so the whole image may change.
2. Send `POST /prompt` with `{prompt, client_id}` and report progress 0 of N.
3. Poll `GET /history/{prompt_id}` every second. While the job has no history entry, `GET /queue` gives its position, which goes to `AIImageRequest.Queue` whenever it changes. Position 0 means the job is running.
4. When the entry shows the job complete, download every image of type `output` with `GET /view?filename=&subfolder=&type=output`, in node id order, and report progress after each one.

The call fails when the job reports `execution_error`, when it finishes without output images, when the job is in neither the queue nor the history for 3 polls, or when 3 history polls in a row fail. A canceled or timed out call removes a waiting job with `POST /queue {"delete": [id]}`, or also sends `POST /interrupt {"prompt_id": id}` when the job may be running. Each of these calls has a 5 s limit, and the server logs a failure and goes on.

### Storage

A stored provider keeps its image settings as one JSON object in the `ai_providers.image_config` column.

## Usage and Budgets

### Recording

The server stores one row in `ai_usage` for every provider call. `POST /api/ai/run`, `POST /api/ai/stream`, every embedding request and every provider test each record a row. A call that moves to the next model of its route records one row for each model it tried. A streamed call records its row when the stream ends. The stream ends at the done frame, at an error frame, or when the client disconnects.

A row holds the user, provider, model, feature, routing task, plugin, space and note, the input and output tokens, the images in and out, the duration, the status, the error text, the cost, an `estimated` flag and `attempts`. `attempts` is the number of route targets tried up to and including that call, so a call served by the second model after a 503 has a first row with status `error` and attempts 1, and a second row with status `ok` and attempts 2. The result and the done frame carry `provider`, `route` and `attempts` too.

| Status | Meaning |
| --- | --- |
| `ok` | The call finished |
| `error` | The provider returned an error. The row keeps the first 500 bytes of the message |
| `canceled` | The client disconnected or the request context ended before the call finished |
| `budget` | A monthly budget refused the call. Tokens and cost are 0 |

A failed usage write goes to the log at warn level. The AI call still succeeds.

When a provider reports neither token count, the server estimates both as ceil(characters / 4) and sets `estimated`. The input count uses the characters of the prompt, and the output count uses the characters of the answer. An `error` row gets no estimate. Embedding APIs report no counts, so every embedding row is estimated.

### Features

`AIRequest.feature` labels a call in the statistics. It is a free-form string of at most 40 bytes. An empty feature takes the task name, such as `summarize`. The server sets `embeddings` for index builds, `search` for query embeddings and `test` for provider tests. The editor sends `ask`, `rewrite`, `image` and `chat`.

`AIRequest.pluginId` names the plugin that made the call. Only the plugin host sets it. The JSON API clears a value sent by a client.

### Prices

`models.md` describes where prices come from. Every price is in US dollars. The cost of a call is input × inputPerMTok / 10^6 + output × outputPerMTok / 10^6 + imagesIn × perImageIn + imagesOut × perImageOut + perRequest, with the effective price of the model that served the call. A model without a price costs 0, and a failed call costs 0. The row stores the cost in millionths of a dollar at record time, so a later price change leaves recorded costs unchanged. A report sets `currency` to `USD`.

### Reports

`GET /api/ai/usage` returns the caller's usage and `GET /api/admin/ai/usage` returns usage across the instance. Both answer with `core.AIUsageReport`.

```json
{
  "from": "2026-09-01T00:00:00Z",
  "to": "2026-10-01T00:00:00Z",
  "groupBy": ["day", "feature"],
  "rows": [
    {"day": "2026-09-13", "feature": "ask", "calls": 4, "errors": 1,
     "inputTokens": 5210, "outputTokens": 830, "imagesIn": 0, "imagesOut": 0,
     "durationMs": 9120, "avgDurationMs": 2280, "costMicros": 27990,
     "cost": 0.02799, "estimatedCalls": 0}
  ],
  "totals": {"calls": 4, "errors": 1, "inputTokens": 5210, "outputTokens": 830,
     "imagesIn": 0, "imagesOut": 0, "durationMs": 9120, "avgDurationMs": 2280,
     "costMicros": 27990, "cost": 0.02799, "estimatedCalls": 0},
  "currency": "USD"
}
```

| Parameter | Effect |
| --- | --- |
| `from`, `to` | RFC 3339 or YYYY-MM-DD in UTC. `from` is inclusive and `to` exclusive, and a date in `to` includes that whole day. Without both, the range is the current UTC month |
| `groupBy` | A comma list of `day`, `month`, `user`, `provider`, `model`, `feature` and `plugin` |
| `provider`, `model`, `feature`, `plugin` | Filters on exact values |
| `user` | A user filter, on the admin route only |
| `limit` | The row cap, at most 5000 |

A row carries only the keys named in `groupBy`. A key missing from a row means the empty value, for example calls from no plugin. Without group keys, `rows` holds one row equal to `totals`. Rows grouped by day or month come in time order. Other rows come in order of tokens, highest first. `errors` counts every row whose status is not `ok`. An admin report grouped by user adds `userName`.

`GET /api/ai/usage/calls` and `GET /api/admin/ai/usage/calls` list raw rows as `{"items": [...]}` of `core.AIUsageRecord`. `sort=recent` lists the newest first, `slowest` the longest first, and `failed` the rows whose status is not `ok`, newest first. `limit` defaults to 50 and stops at 500. The user route lists only the caller's calls.

`GET /api/ai/usage/summary` feeds the usage indicator.

```json
{
  "today": {"calls": 3, "inputTokens": 1200, "outputTokens": 300, "tokens": 1500, "cost": 0.004},
  "month": {"calls": 41, "inputTokens": 52000, "outputTokens": 9100, "tokens": 61100, "cost": 0.19},
  "currency": "USD",
  "budget": {"tokenLimit": 100000, "costLimit": 0, "tokensUsed": 61100, "costUsed": 0.19,
     "usedPct": 61.1, "warn": false, "exceeded": false, "stopped": false}
}
```

### Budgets

Two instance settings set the monthly limit of every user. `aiMonthlyTokenBudget` counts input plus output tokens, and `aiMonthlyCostBudget` counts currency units. 0 means no limit. Only admins read and write these settings.

The `ai_budgets` table holds overrides, one per user or plugin. A `null` limit in a user override keeps the instance default. A `null` limit in a plugin override means no limit, because plugins have no default. A zero limit means no limit.

Before a run or stream call, the server sums the usage of the user in the current UTC month. When the request carries `pluginId`, it also sums the usage of that plugin. The sum covers every row, embedding and test rows included. Only run and stream calls are checked.

| Used share | Effect |
| --- | --- |
| Below 80% | None |
| 80% or more | The result or the done frame carries `"budget": {"usedPct": 84.2, "warn": true}` |
| 100% or more | The call fails with 429 and records a `budget` row |

The used share is the higher of the token share and the cost share. The warning counts the call that just finished. The stop check counts usage before the call, so the call that crosses 100% completes and the next call fails. Admins have budgets like every other user. An override with a zero limit removes the limit of one admin.

When the server cannot read a budget, the call proceeds and the server logs a warning.

`POST /api/admin/ai/budgets/{kind}/{id}/lift` sets `liftedUntil` to the start of the next UTC month. Until then, calls pass the stop check. The limits stay, so the next month starts with them in force. Saving new limits keeps an active lift. Deleting a user deletes their override.

### Retention

`ai_usage_monthly` holds one row per month, user, provider, model, feature and plugin, with summed counts. A job runs once at startup and then every 24 hours. It folds the raw rows of every whole UTC month before the date 13 months ago into the monthly table and deletes them, one transaction per month. The upsert adds to an existing monthly row, and a folded raw row no longer exists, so a second run changes nothing.

Reports sum raw rows and monthly rows. A rolled-up month has no days, so its rows carry the month, YYYY-MM, as `day`. A range that includes any part of a rolled-up month counts the whole month.
