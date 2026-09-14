# Bookmark Wiki Plugin

The plugin reads a user's bookmarks, saves the readable text of each page as a
source note, summarises it, and maintains a wiki of linked pages about the
people, tools, projects and ideas the sources mention. Wiki pages link to
each other, to source notes and to the user's own notes. The plugin proposes
links inside the user's notes as suggestions and never edits them directly.

The design follows Karpathy's LLM wiki pattern. The pattern keeps raw sources
that nothing rewrites, pages that a language model maintains, and a schema
page with the conventions the model follows. It runs three operations, which
are ingest, query and lint.

The plugin is a Python 3.12 service built on `sdk/python/markspace`, with no
other dependencies. Its id is `bookmark-wiki`. It runs in its own container.
`docs/plugins.md` defines the protocol it uses.

## Layout in the Wiki Space

An admin picks the wiki space at install. It can be a space that already holds
the user's notes. Folder names are settings.

```
Sources/
  <domain>/
    <slug>.md                 one source note per bookmark
    <slug>.pdf                the original file, when the bookmark is a PDF
Wiki/
  index.md                    catalogue of every page, grouped by type, created as the folder note of Wiki
  log.md                      append-only record of every run
  schema.md                   conventions the model follows, editable by the user
  Entities/<slug>.md          people, organisations, tools, projects, places
  Concepts/<slug>.md          ideas, methods, terms
  Comparisons/<slug>.md       pages comparing two or more entities or concepts
  Answers/<slug>.md           answers filed from the Wiki tab
  Lint/<yyyy-mm-dd>.md        lint reports
  Lint/Drift/<title>.md       text of a source page that changed after the first capture
```

### Source Note

```yaml
---
title: "How SQLite uses write-ahead logging"
type: source
source_url: https://www.sqlite.org/wal.html
canonical_url: https://www.sqlite.org/wal.html
site: sqlite.org
source_author: ""
published: ""
bookmarked: 2024-03-02T10:14:00Z
bookmark_folder: Databases/SQLite
bookmark_tags: [sqlite, storage]
fetched: 2026-09-13T11:02:00Z
sha256: 9f2c...
status: ok            # pending, ok, dead, blocked, not-text
wiki_state: compiled  # pending, fetched, summarised, compiled, failed
language: en
generated_by: bookmark-wiki
tags: [source]
---
```

Markspace owns the frontmatter keys `id`, `title`, `tags`, `created`,
`updated` and `author`. The plugin sends the body as `content`, the title and
tags as note fields, and every other key in the `frontmatter` object of
`POST /notes` and `PATCH /notes/{id}`. The author of the page is
`source_author`, because `author` names the Markspace user who created the
note. The `created` and `updated` keys of a wiki page below are the note's own
times.

The body has two parts, each opened by a marker comment:

```markdown
<!-- wiki:summary -->
## Summary
Two to five sentences.

## Key points
- A claim, with the passage it comes from in the captured text.

## Mentions
[[SQLite]], [[Write-ahead logging]], [[Checkpointing]]

<!-- wiki:captured -->
## Captured text
The readable text of the page as markdown.
```

The plugin writes the summary part again when it re-summarises. It writes the
captured part once, at the first successful fetch. A later fetch whose hash
differs writes the new text to a drift note under `Wiki/Lint/Drift/`, lists it
on the source record, and does not change the source note.

### Wiki Page

```yaml
---
title: Write-ahead logging
type: concept          # entity, concept, comparison, answer
entity_kind: ""        # person, organisation, tool, project, place, for entities
aliases: [WAL]
tags: [databases]
sources: ["[[How SQLite uses write-ahead logging]]", "[[PostgreSQL WAL internals]]"]
related_notes: ["[[Storage design notes]]"]
confidence: high       # low, medium, high
contested: false
created: 2026-09-13T11:05:00Z
updated: 2026-09-13T11:05:00Z
generated_by: bookmark-wiki
---
```

`Wiki/schema.md` states these page rules for the model:

1. The page opens with one paragraph that defines the subject.
2. Every factual sentence ends with at least one citation to a source note, as
   `([[source title]])`.
3. The page links to at least 2 other wiki pages or user notes.
4. A claim that two sources disagree on goes under `## Disagreements`, with
   both citations, and the page frontmatter sets `contested` to true.
5. A page longer than 1500 words gets split into subpages, and the lint
   report lists it until that happens.
6. User notes appear under `## In your notes`, as wikilinks with one line each
   on how they relate.

### The Write Rule

The plugin writes a note only when its frontmatter sets `generated_by` to
`bookmark-wiki` and no one else edited it since the plugin wrote it. Two
signals mark an edit. The first is `updatedBy` on the note differing from the
plugin user id that `GET /me` returns. The second is a hash of the note body
and frontmatter differing from the hash the plugin stored after its last
write. The hash leaves out the keys Markspace owns and the tags, because
Markspace may add inline hashtags to the tags.

Every other change becomes a suggestion. The suggestion holds one edit per
changed run of lines, from a line diff of the body. The plugin withdraws an
open suggestion of the same kind on the note before it creates a new one. When
the new edits equal the open ones, the open suggestion stays.

Anchors follow `core.CommentAnchor`. Offsets count UTF-16 code units of the
body without frontmatter. `anchors.py` reproduces every make case in
`test/vectors/anchors.json`. An insertion uses an empty `exact` with `start`
equal to `end`.

## Importers

| Importer | Input |
| --- | --- |
| `netscape` | the bookmark HTML export from Chrome, Firefox, Safari and Edge, as `.html` or `.htm`. Folder paths become `bookmark_folder`, and `ADD_DATE` becomes `bookmarked` |
| `urls` | a `.txt` file with one URL per line. Lines starting with `#` are ignored |
| `karakeep` | the Karakeep API, with base URL and token as secrets. Pages through `/api/v1/bookmarks`, keeping tags and lists |
| `linkding` | the Linkding API, with base URL and token as secrets. Pages through `/api/bookmarks/`, keeping tags |

The manifest declares `netscape` and `urls` as importers. Karakeep and Linkding
imports run from the Connections section of the Bookmark wiki page and from
the hourly `sync` job, which imports bookmarks added since the last run once
`importEveryHours` have passed.

URL normalisation, used to find duplicates:

- lower-case the scheme and host, and drop a `www.` prefix,
- drop the fragment, default ports, and the parameters `utm_*`, `fbclid`,
  `gclid`, `mc_cid`, `mc_eid`, `ref` and `ref_src`,
- sort the remaining query parameters,
- drop one trailing slash from a path longer than `/`.

The key-value store maps each normalised URL to its source note id. An import
of a known URL merges tags and folders into the existing note.

## Manifest

| Field | Value |
| --- | --- |
| id | `bookmark-wiki` |
| permissions | `items:read`, `items:write`, `links:read`, `search`, `ai:complete`, `web:fetch`, `jobs`, `storage`, `ui`, `suggestions`, `events` |
| events | `suggestion.accepted`, `suggestion.discarded`, `note.deleted`, `note.trashed` |
| purposes | `summarise`, `compile`, `lint`, `ask`, sent as the routes `plugin.bookmark-wiki.<purpose>` |
| jobs | `import`, `fetch`, `summarise`, `compile`, `link-suggest`, `lint`, `refetch`, `sync` |
| schedules | `lint` every 24h, `refetch` every 24h, `sync` every 1h |
| commands | `add-url` in the `link` and `global` contexts, `ask` in the `global` context |
| panels | `home` page "Bookmark wiki", `wiki` rail tab "Wiki" |
| importers | `netscape` for `.html` and `.htm`, `urls` for `.txt` |
| secrets | `karakeep_url`, `karakeep_token`, `linkding_url`, `linkding_token` |

## Jobs

Every job is idempotent. Every job reads its input again when it runs, so a
job that runs twice for the same input leaves the same notes. Jobs queue
follow-up jobs with the plugin token. The SDK reports a long job with the token
of the job call, and an acting token expires after 10 minutes.

| Job | Input | Work |
| --- | --- | --- |
| `import` | importer id and file or connection | parse, normalise, create pending source notes under `Sources/<domain>/`, enqueue `fetch` for each |
| `fetch` | source note id | host web fetch. See below. Enqueues `summarise` |
| `summarise` | source note id | one model call that returns JSON with summary, key points, mentions (name, kind, aliases), tags, and language. Writes the summary part. Enqueues `compile` |
| `compile` | source note id | see below. Enqueues `link-suggest` for each page it touched |
| `link-suggest` | wiki page id | search the user's notes for the page title and aliases. For each note that mentions the subject without linking to it, create one suggestion that links the first mention. At most `suggestionsPerDay` suggestions per day, and at most 1 open suggestion per note from this plugin |
| `lint` | none, daily and on demand | see below |
| `refetch` | none, daily | re-fetch the 50 sources with the oldest `fetched`, to record drift and dead links |
| `sync` | none, hourly | import from Karakeep and Linkding when `importEveryHours` have passed |

While the plugin has 10,000 queued jobs, `POST /jobs` answers 429. The import
then waits 30 seconds, reports progress and tries again.

### Fetch

The plugin waits at least 1 second between requests to one host and runs at
most `fetchConcurrency` fetches at once.

- HTML and text become the captured text.
- A PDF is uploaded next to the source note, and its text comes from
  `GET /files/{id}/text`. An empty text means extraction has not finished, so
  the job queues itself again after 60 seconds, up to 5 times.
- A 404 or 410 sets `dead`.
- A 401, 403 or 429 queues a retry, and the third one sets `blocked`.
- A robots.txt refusal sets `blocked` at once.
- A response with no readable text and no bytes sets `not-text`.

### Compile

`compile` jobs run one at a time per wiki space, so two jobs never update one
page at once.

1. For each mention from the summary, find the existing page through the
   key-value alias index, which maps lower-cased names and aliases to page ids,
   then through the host link resolver, then through a text search limited to
   `Wiki/`.
2. Search the user's notes, outside `Sources/` and `Wiki/`, for the five notes
   most related to the source, using semantic search when the space has
   embeddings and text search otherwise.
3. Choose at most `maxPagesPerSource` pages to create or update, default 12,
   favouring mentions the summary lists first and pages that already exist.
4. For each page, send the current page, the source summary and key points,
   the captured passages that support them, the related user notes' titles
   and excerpts, and `schema.md`. The model returns the full new page body and
   frontmatter changes as JSON.
5. Check the answer. The checks are listed below. A page that fails gets one
   repair call. A second failure logs the problems in the job log and skips the
   page. The source then keeps no compile fingerprint, so the next compile
   tries the skipped page again.
6. Write each page with `baseUpdatedAt`, or as a suggestion under the write
   rule. A 409 conflict reads the page again and runs step 4 again once.
7. Add new pages to `index.md` under their type, with the one-paragraph
   definition cut to 160 characters.
8. Append one line to `log.md`, such as
   `2026-09-13 11:05 · compile · [[How SQLite uses write-ahead logging]] · created 2, updated 5`.

The checks in step 5:

- Every citation `([[title]])` names a source note in the key-value store.
- The page cites the source being compiled.
- A citation of the current page that the new page drops appears in
  `removed_citations` with a reason.
- Every other wikilink names a page of this compile, an existing page, a
  source, or a note the host resolver finds.
- The page links to at least 2 other wiki pages or user notes.
- The page opens with a paragraph. A page that opens with a heading gets its
  `definition` in front.

Compile skips work at three levels.

1. The source fingerprint hashes the summary, the source title, `schema.md`
   and `maxPagesPerSource`. A compile whose fingerprint equals the one stored
   after the last complete compile returns at once. `force` skips this check.
2. Each page record stores the fingerprint of every source compiled into it. A
   page that already holds this input gets no model call. Only a Regenerate
   page run, which names the page in `only`, calls the model again.
3. The plugin does not send a write whose body and frontmatter equal the note,
   ignoring `updated`.

Markspace resolves `[[name]]` by slug and by case-insensitive title. A source
titled "Write-Ahead Logging" and a page titled "Write-ahead logging" would
share one link. So a new page never takes a title that already resolves to
another item. It gets a qualifier, as in "Write-ahead logging (concept)", and
the plain name becomes an alias. A source title that resolves to an existing
note gets the site as a qualifier. Titles also lose `[`, `]`, `|`, `#` and
`/`, because a slash turns a name wikilink into a path wikilink.

### Lint

The lint writes `Wiki/Lint/<date>.md` with one task-list item per finding, and
a suggestion for each finding that has a mechanical fix.

| Check | Finding | Fix |
| --- | --- | --- |
| Orphans | a wiki page with no backlinks | a suggestion adding it to `index.md` or to a related page |
| Broken links | a wikilink on a wiki page that resolves to nothing | a suggestion removing or retargeting it |
| Missing pages | a name mentioned by 3 or more sources with no page | a `compile` run for those sources |
| Contradictions | a model call over each `contested` page and each page updated since the last lint | a `## Disagreements` section update |
| Drift | a source whose re-fetched hash changed | none. The report links the drift note |
| Dead sources | `status: dead` | none |
| Long pages | over 1500 words | none |
| Index | pages missing from `index.md`, or entries whose page is gone | a suggestion updating `index.md` |

## Model Calls

Every call sends the task `custom`, the route of its purpose, a one-sentence
`instruction`, and the prompt as `text`. The prompt is the system text, a blank
line, then the user text. Markspace keeps only the first 1000 characters of a
custom instruction, and `text` may use the whole context budget. The user text
holds the structured input as JSON between the lines `<<<INPUT` and `INPUT>>>`.
A model setting of the form `providerId/model` adds `providerId` and `model` to
the request.

`jsonparse.extract_json` reads the first JSON object in the answer. It accepts
code fences, prose before the object and text after it. `schemas.py` then
checks the structure. When parsing or a check fails, the plugin sends the
repair prompt once. The repair prompt holds the schema, the list of problems,
the previous answer and the original request. A second failure fails the step.

| Prompt | Answer schema |
| --- | --- |
| `summarise` | `{title, author, published, language, summary, key_points: [{claim, quote}], mentions: [{name, kind, aliases}], tags}` |
| `compile` | `{title, type, entity_kind, aliases, tags, confidence, contested, definition, body, related_notes, removed_citations: [{source, reason}]}` |
| `lint` | `{contested, disagreements: [{claim, positions: [{source, says}]}], section}` |
| `ask` | `{title, answer, cited}` |
| `repair` | the schema of the prompt it repairs |

A quote that does not occur in the captured text is cleared, and the claim
stays. A mention kind outside the schema becomes `concept`.

## Ask

The Wiki tab and the Ask the wiki command take a question.

1. Read `index.md` and run a semantic or text search limited to `Wiki/` and
   `Sources/`.
2. Send the eight best pages and the question to the model, which answers with
   citations to pages and source notes.
3. The answer shows with File this answer, which writes
   `Wiki/Answers/<slug>.md` with the type `answer` and links the pages it
   cites.

## Interface

| Contribution | Contents |
| --- | --- |
| Page "Bookmark wiki" | counts of sources by state, running jobs with progress, the import form with a file field and the Karakeep and Linkding connections, Pause jobs and Resume jobs, Run lint now, the cost estimate, this month's AI usage, the settings form |
| Rail tab "Wiki" on a source note | fetch status, fetched date, Re-fetch, Re-summarise, the pages built from it |
| Rail tab "Wiki" on a wiki page | sources, Regenerate page |
| Rail tab "Wiki" on another note | related wiki pages, Ask the wiki |
| Command "Add link to wiki" | contexts `link`, `global`. Queues `fetch` for the URL |
| Command "Ask the wiki" | context `global` |
| Importer "Browser bookmarks" and "URL list" | file upload |

### Cost Estimate

Before an import runs, the page shows the number of new URLs and an estimate
of tokens and cost:

- fetch and summarise at 3000 input and 500 output tokens per source,
- compile at 6000 input and 1500 output tokens per page, times the average
  pages per source measured over the last 100 compiles, or 8 before any.

Prices come from `GET /api/plugin/v1/ai/models`. The user confirms the
estimate before the plugin queues the jobs.

## Settings

Settings merge in this order, later values winning. The defaults come first,
then the settings an admin saved in Markspace, read from `GET /settings`, then
the values saved from the settings form on the Bookmark wiki page. The plugin
reads `GET /settings` at most every 5 seconds and again after a
`plugin.settings.updated` event. When `GET /settings` fails, the plugin reads
`settings` from the stored install payload. Markspace does not send settings in
the install payload, so in that case the defaults apply.

| Setting | Name | Default |
| --- | --- | --- |
| Wiki space | `space` | required. With no value, the single editor grant from `GET /me` |
| Sources folder | `sourcesFolder` | `Sources` |
| Wiki folder | `wikiFolder` | `Wiki` |
| Summarise model | `summariseModel` | empty, which uses the route |
| Compile model | `compileModel` | empty, which uses the route |
| Maximum pages per source | `maxPagesPerSource` | 12, from 1 to 50 |
| Suggestions per day | `suggestionsPerDay` | 20, from 0 to 1000 |
| Fetch concurrency | `fetchConcurrency` | 4, from 1 to 16 |
| Respect robots.txt | `respectRobots` | true |
| Summary language | `summaryLanguage` | empty, which keeps the source language |
| Import schedule for API importers, in hours | `importEveryHours` | 6, from 1 to 168 |

## Host API Behaviour

The plugin depends on these behaviours of the Markspace plugin host.

| Behaviour | How the plugin uses it |
| --- | --- |
| `POST /web/fetch` returns `contentType` and, for a PDF, the file as `bodyBase64` | without bytes, the source becomes `not-text` |
| a remote 404, 403 or other error status answers 200 with that `status` and no body | 404 and 410 set `dead`, and 401, 403 and 429 retry, then set `blocked` |
| a robots.txt refusal answers 403 with the code `robots_disallowed` | the source becomes `blocked` at once |
| `GET /me` returns `userId` | edits by other users mark a note as edited by the user |
| a frontmatter block in `content` stays in the body as text | the plugin sends frontmatter only in the `frontmatter` object |
| `POST /resolve` accepts an empty `fromNoteId` | titles are checked before a source note exists |
| `GET /settings` returns the settings an admin saved | see Settings |
| search with `mode=semantic` returns no hits when no note has embeddings | text search per mention name |
| `POST /jobs` answers 429 while the plugin has 10,000 queued jobs | the import waits 30 seconds, reports progress and tries again |
| a note titled `index` is stored as `index-note.md`, because `index.md` is a folder note | the plugin finds its index through the key-value store |

## Code Layout

| Path under `plugins/wiki/source/wiki/` | Contents |
| --- | --- |
| `__main__.py` | starts the HTTP server |
| `app.py` | registers jobs, panels, commands, actions, importers and events on the SDK plugin |
| `manifest.py` | the manifest and the routing purposes |
| `service.py` | shared state for handlers: settings, host client, locks, job queueing |
| `settings.py` | defaults, form validation, model choice |
| `state.py` | plugin state in the host key-value store |
| `notes.py` | folders, notes, the write rule and diff-based suggestions |
| `wikifiles.py` | `Wiki/schema.md`, `Wiki/index.md` and `Wiki/log.md` |
| `sources.py` | source notes and the import, fetch, summarise, refetch and sync jobs |
| `compile.py` | the compile job |
| `linksuggest.py` | the link-suggest job |
| `lint.py` | the lint job |
| `ask.py` | Ask the wiki and File this answer |
| `cost.py` | the import cost estimate |
| `panels.py` | the Bookmark wiki page, the Wiki rail tab, commands and actions |
| `ai.py` | model calls with JSON answers and one repair call |
| `jsonparse.py` | tolerant JSON extraction from model output |
| `schemas.py` | structural checks of each model answer |
| `anchors.py` | suggestion anchors in UTF-16 code units |
| `wikilinks.py` | wikilinks, citations and unlinked mentions |
| `frontmatter.py` | the frontmatter subset the plugin reads and writes |
| `urls.py` | URL normalisation, slugs and stable ids |
| `ratelimit.py` | one request per second per host and the fetch concurrency limit |
| `importers/` | `netscape.py`, `urllist.py`, `karakeep.py`, `linkding.py` |
| `prompts/` | summarise, compile, repair, lint and ask templates |

## Container

`plugins/wiki/containerfile` builds the image from the repository root. The
image copies `sdk/python/markspace` and `plugins/wiki/source/wiki` onto a
`python:3.14-slim` base and runs as a non-root user with a volume at `/data`.
The published image is `ghcr.io/gamemaker1/markspace-wiki`.

The quadlet unit `deploy/wiki.container` joins `markspace.network`, mounts
`deploy/wiki.volume` at `/data`, sets a `HealthCmd`, and names the container
`bookmark-wiki`, so Markspace reaches the plugin at
`http://bookmark-wiki:8090`. The plugin reaches Markspace at
`http://markspace:8080`. When the install call cannot reach the plugin, the
unit reads `MARKSPACE_URL`, `MARKSPACE_TOKEN` and `MARKSPACE_SIGNING_SECRET`
from Podman secrets through its commented `Secret=` lines.

## Testing

The tests in `plugins/wiki/test` use `unittest` and no network. Run them from
the repository root with
`python3 -m unittest discover -s plugins/wiki/test`.

- `fakehost.py` implements every host method the plugin calls in memory.
- `fakeai.py` answers model calls from fixture summaries and builds pages from
  the compile input.
- `fakeweb.py` serves the fixture pages.
- `fakehttp.py` serves the fake host over HTTP with list envelopes and
  `Idempotency-Key` replay, and runs jobs against the plugin with signed
  requests.

Unit tests cover URL normalisation, the Chrome, Firefox and Safari exports,
Karakeep and Linkding paging, JSON parsing of model output with the repair
call, the compile checks, idempotency, the write rule, the daily suggestion
limit, and the lint checks, including a planted orphan and planted broken
links.

`test_e2e.py` starts the plugin and the fake host on ports 18190 to 18199,
installs the plugin, imports 30 bookmarks from a Netscape export, runs every
job and lint, and checks the resulting wiki. The pages must link to each
other, to sources and to the seeded user notes, each seeded note must get one
suggestion, and no seeded note body may change.

`test/fixtures/` holds hand-written Chrome, Firefox and Safari exports, a URL
list, Karakeep and Linkding API pages, and web page entries with their
summaries. The entries include a PDF, a 404, a 403, a robots.txt refusal and a
redirect.
