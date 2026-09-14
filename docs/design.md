# Markspace Design

Markspace is a self-hosted note application. Notes are markdown files with
YAML frontmatter. An SQLite database indexes them and holds the state that has
no file representation, such as accounts, shares and comments.

## Guarantees

1. Every note is one `.md` file. Deleting the database loses no note text.
2. A file edited outside the application is picked up, either by the watcher
   or by the startup scan.
3. The binary has no cgo dependency. `modernc.org/sqlite` provides SQLite
   3.53.4 with FTS5.

## On-Disk Layout

```
<dataDir>/
  markspace.db                        index database
  spaces/
    <space-slug>/
      getting-started.md              a note
      Getting started.pdf             a file
      Projects/                       a folder
        index.md                      the folder note of Projects
        harbour-survey.md
        east wall.jpg
      .trash/
        <item-id>/<base name>         a trashed note, file or folder
  internal/
    history/<space-id>/<note-id>/<unix-millis>.md
    file-history/<space-id>/<file-id>/<unix-millis>-<name>
    thumbs/<file-id>/<sha256-prefix>-<width>.jpg or .png
  deleted/<space-slug>-<unix-millis>/  archived space directories
```

A space holds folders, notes and files. `files.md` specifies the layout and
the name rules. The folder that contains an item is its parent. Paths are
slash separated and start at the space directory. Each trash slot holds a
`.meta.json` sidecar with the kind, the old path and the deletion time.

## Frontmatter

```yaml
---
id: 0abc123456789abcdefghjkmnp
title: Getting started
icon: "🚀"
tags: [onboarding, docs]
position: 100
created: 2026-09-13T09:00:00Z
updated: 2026-09-13T09:12:00Z
author: 0user00000000000000000000
updated_by: 0user00000000000000000000
---
```

`id` is required and stable for the life of the note. The frontmatter has no
`parent` key, because the containing folder is the parent. `deleted`, `deleted_at`,
`deleted_by` and `trashed_from` appear only on trashed notes. `updated_via` appears only when
`updated_by` made the last change through a plugin acting for them, and holds
the plugin id. Any other key is read into `Note.Extra` and written back
unchanged.

## Packages

| Path | Contents |
| --- | --- |
| `cmd/markspace` | The binary: flags, startup, graceful shutdown and the management commands that `cli.md` describes |
| `internal/core` | Domain types, errors, ids, permissions, the `Backend` interface |
| `internal/ports` | Interfaces for the file store, database, renderer and AI factory |
| `internal/config` | YAML plus environment configuration |
| `internal/store` | `ports.Files` over the filesystem |
| `internal/index` | `ports.DB` over SQLite, with the schema migrations |
| `internal/auth` | argon2id password hashing, token hashing, email and password validation |
| `internal/ai` | `ports.AIFactory` and the three provider protocols |
| `internal/markdown` | `ports.Renderer` over goldmark |
| `internal/links` | Link parsing, resolution against a space snapshot, and rewriting after renames and moves. The browser module `internal/web/static/js/links.js` is a port of it, and both pass the cases in `test/vectors/links.json` |
| `internal/extract` | Image scaling, thumbnails, text decoding and PDF text |
| `internal/anchor` | Comment anchors: making, finding and remapping them through edits, in UTF-16 units |
| `internal/fetch` | Web page fetching with private-address blocking, and HTML to text |
| `internal/app` | `core.Backend` implementation, access control, reconciliation, link rewriting, the hash and text queues |
| `internal/module` | The module engine: registry, event bus, job engine, scheduler, contribution types, declarative UI validation and suggestion logic |
| `internal/module/conformance` | The conformance suite that every `module.JobStore` implementation runs from its tests |
| `internal/modules/<m>` | One package per built-in module, with its routes, handlers and contributions |
| `internal/plugin` | The plugin host: install, tokens, event delivery, health checks, plugin jobs and the storage behind the host API |
| `internal/api` | Authentication, middleware, error mapping, and the JSON, multipart and SSE helpers of the module handlers |
| `internal/api/testkit` | A `core.Backend` stub, a fake and a registry server for handler tests |
| `internal/control` | The Markspace CLI token file and the HTTP client of the `markspace` management commands |
| `internal/web` | Server-rendered pages. `templates` holds the page templates, with partials in `templates/partials` and admin pages such as `templates/admin/ai.html` keyed as `admin/ai`. `static` holds the JavaScript and CSS, with vendored libraries in `static/vendor`, among them the built bundles `codemirror.js`, `pdfjs.js` and `pdfworker.js`. `vendor/codemirror` and `vendor/pdfjs` hold the bundle sources |
| `internal/wiring` | Assembles the engine, bus, scheduler, built-in modules and plugin host for `cmd/markspace` and the tests |
| `sdk` | The Go plugin SDK, package `sdk`, with declarative UI helpers in `sdk/ui` |
| `sdk/python/markspace` | The Python plugin SDK |
| `sdk/example` | The Word count example plugin, `example.py` |
| `plugins/wiki` | The bookmark wiki plugin, plugin id `bookmark-wiki`. The Python package is in `source/wiki` and its tests in `test` |
| `test/server` | End-to-end and HTTP fuzz tests over the real packages and an HTTP server |
| `test/client` | `node --test` unit tests of the browser modules |
| `test/browser` | The browser layout audit that `make layout` runs |
| `test/vectors` | JSON cases shared by the Go and browser implementations: `anchors.json`, `links.json` and `protocol.json` |
| `tools` | `anchors.py`, which generates the anchor vectors, `icons.py`, and the `octicons` generator in Go |
| `deploy` | Rootless Podman quadlet units |
| `docs` | Design documents |

The HTTP layers depend only on `core.Backend`. `internal/app` depends only on
the interfaces in `internal/ports`. A test can replace or fake each port
implementation without changes to the packages that use it.

### Module Layers

`plugins.md` defines modules. The built-in modules are `auth`, `users`,
`spaces`, `notes`, `files`, `comments`, `sharing`, `search`, `sync`, `ai`,
`suggestions`, `admin`, `jobs`, `ui` and `plugins`. Each one is a package under
`internal/modules` that registers its routes through `module.Registry`.

A module package depends on `internal/core`, `internal/module` and the helpers
of `internal/api`. It never imports another module package, `internal/app` or
a port implementation. It reaches the application only through `core.Backend`.
`core.Backend` includes every service a handler calls, among them
`AIThreadService`, `AIImageService`, `SuggestionService` and
`PluginSupportService`, so no handler looks a service up by type assertion.

Most modules keep one `api.Env`, which holds the backend, the logger, the
`api.Authenticator`, the session lifetime, the upload limit and the base URL.
The `api` helpers they share are `WriteJSON`, `WriteItems`, `WriteError`,
`Decode`, `ActorFrom`, `RequireUser`, `RequireAdmin`, `IntParam`,
`UploadParts`, `NewSSE` and the `PageRenderer` interface. The web server
implements `PageRenderer`, so a module page renders inside the application
layout. A module parses its page templates with `api.PageFuncs` and executes
them with `api.ExecutePage`, which supplies `PageRenderer.Octicon`, so module
templates call `{{ octicon "stop" 16 }}` as the web templates do.

`internal/api` holds no route table. `api.NewServer` and `web.NewServer` take
a `Mount` option, and the registry mounts every module route through it. The
API server answers any other path under `/api/` with the JSON 404 body.
`wiring.CoreModules` lists the modules that need only an `api.Env`.
`Parts.AddBuiltins` registers them with `jobs`, `Parts.AddPlugins` adds
`plugins`, and `Parts.AddUI` adds `ui`. `test/server/routes_test.go` compares the
mounted routes with `routes.md`.

`internal/plugin` implements `plugins.Host`, `core.PluginResolver`,
`ui.ActionRunner` and `ui.Availability`. It reads and writes the plugin tables
of `internal/index` through its own `Store` interface, which `index.DB`
satisfies.

`app.Deps` holds `Suggestions`, `Bus`, `Jobs`, `Scheduler`, `ScheduleEvery` and
`EventSink`. `App.Start` starts the job engine, the scheduler and every service
added with `App.AddService`, such as the plugin host, and `App.Close` stops
them in reverse order before it closes the bus. `App.SetPluginResolver`
installs the host's token resolver. `App.NewEventFilter` returns the access
filter of the event stream for use elsewhere, and `App.Emit` publishes an
event from outside the application to the browser streams only.

## Access Control

Every `Backend` method resolves a `core.Grant` for the actor before it reads
or writes. An item is a folder, a note or a file. The grant on an item is the
highest of these sources:

| Source | Access |
| --- | --- |
| Instance admin | manage, on every space |
| Space role | owner and admin manage, editor edits, commenter comments, viewer views |
| Direct share on the item | the share's access |
| Share with `includeChildren` on an ancestor folder | the share's access |
| Share with `includeChildren` on a folder note | the share's access, on its folder and everything below that folder |
| Public link token | the link's access, capped at comment, on the target item and, with `includeChildren`, everything below a target folder or folder note |

A share on a note that is not a folder note, or on a file, covers only that
item. A share on a folder without `includeChildren` lets the user open the
folder, which lists every direct child, but not open the children. A public
link never reaches a trashed item or another space.

Creating an item needs edit access on the parent folder, or on the space at
the root. A move needs edit access on the item, the old parent and the new
parent. A copy needs view access on the source and edit access on the target
parent. A permanent delete needs manage access, or edit access for the item's
creator.

An item the actor cannot view returns `ErrNotFound`, so a probe cannot tell a
hidden item from a missing one. An anonymous request returns `ErrUnauthorized`
instead, so the web layer can redirect to the login page. Links render only
when the target is visible to the reader, so a rendered page does not reveal
the path of a hidden item. For a public link actor, rendered note and folder
links point to `/p/{token}/i/{id}` and embedded file content points to
`/p/{token}/raw/{id}/{name}`.

### Actors

`core.Actor` describes one of five kinds of caller.

| Kind | Fields | Credential |
| --- | --- | --- |
| Person | `User`, and `Session` or `Token` | session cookie, or an `msk_` API token |
| System token | `Token` with no user | an `msk_` API token that belongs to no user |
| Public link | `Link` | a link token |
| Plugin token | `User` is the plugin user, `Plugin` is set | an `mspl_` plugin token |
| Acting token | `User` is the person, `Plugin` and `Via` are set | an `msat_` acting token |

A plugin user is a `users` row of kind `plugin`. `UserCanSignIn` refuses it on
login, on session lookup and on API token lookup, and `CreateAPIToken` refuses
a plugin actor. `UpdateUser` refuses to make a plugin user an admin or give it
a password, and `DeleteUser` refuses a plugin user with `ErrConflict`.

A system token has instance-admin rights. The server creates one named Markspace CLI at start and writes its plaintext to `<dataDir>/cli.token` with mode 0600. A system token authenticates only under `/api/`, outside `/api/plugin/v1/`, and cannot create tokens or spaces. `docs/cli.md` describes its rotation.

Plugin and acting tokens authenticate only under `/api/plugin/v1/`. On any
other path the authenticator treats them as no credential.

### Plugin Access

A plugin token actor gets the role its plugin holds on the space in
`plugin_grants`, capped at editor. Owner and admin grant nothing. Shares to the
plugin user do not count, and `Actor.IsAdmin` is false for every plugin actor,
whatever the user row says. Spaces the plugin is not granted are hidden, so
their items return `ErrNotFound`.

An acting token actor gets the lower of two grants on each item. One is the
person's own grant, shares included. The other is the plugin's grant on the
item's space. An admin person therefore reaches at most what the plugin is
granted. `accessibleSpaceIDs` returns the intersection of the person's spaces
and the plugin's granted spaces, and `ListSpaces` shows the resolved role.

The application resolves both token kinds through the plugin host. It checks
that the plugin is active, down or awaiting approval of a new manifest, that
the plugin user is active and of kind plugin, and for an acting token that the
token has not expired, that its interaction is still valid, and that the
person is active and may sign in.

## Uploaded Files

`GET /files/{space}/{id}/{name}` serves the content of a file through
`http.ServeContent`, so `Range` requests get `206 Partial Content`. `?v=`
serves a stored version, and `?download=1` forces `attachment`. The ETag is
the quoted SHA-256 when it is known, and caching is private. `GET
/p/{token}/raw/{id}/{name}` serves the same content to a public link actor,
and `GET /thumbs/{space}/{id}?w=` and `GET /p/{token}/thumb/{id}` serve
thumbnails.

These routes serve files from the application origin, so a file opened
directly in the browser could otherwise run script with the viewer's session.
Three headers prevent that.

| Header | Value | Effect |
| --- | --- | --- |
| `X-Content-Type-Options` | `nosniff` | The browser uses the stored media type and never guesses HTML from content |
| `Content-Disposition` | `inline` for images, video, audio, plain text and PDF; `attachment` for everything else and always for SVG | An HTML or SVG file downloads instead of rendering as a page |
| `Content-Security-Policy` | `sandbox; default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'`, on every type except PDF | A file rendered as a document gets a unique opaque origin with scripts, forms and plugins off |

An SVG still displays inside a note, because browsers ignore
`Content-Disposition` for `<img>` and never run script inside an image. PDFs
get no sandbox policy, because Chrome refuses to display a PDF under one, and
PDF JavaScript runs inside the browser's PDF engine rather than in this origin.

## Anchored Comments

A root comment can carry an anchor on a passage of the note body. `comments.md`
specifies the anchor, the rules for finding it again after edits, and the
comment panel. `tools/anchors.py` is the reference implementation,
and `test/vectors/anchors.json` holds the cases that both `internal/anchor`
and the browser module `internal/web/static/js/anchors.js` must reproduce.

## Event Stream

`GET /api/events` streams change events. Each subscription filters events on
its own goroutine, so a slow access check never blocks a note write. The
filter keeps a snapshot of the user's access and refreshes it when an event
arrives and the snapshot is older than 2 s, and every 30 s otherwise. A space
the user can view in full passes every event. In a space the user reaches
only through shares, an event passes only when its item is visible, so the
stream never reveals the id or path of a hidden item. The stream closes when
the account is deactivated or deleted, or when the session or API token it
authenticated with is revoked or expires.

Event types are `note.created`, `note.updated`, `note.deleted`,
`folder.created`, `folder.updated`, `folder.deleted`, `file.created`,
`file.updated`, `file.deleted`, `item.moved`, `tree.changed`,
`comment.created`, `suggestion.created`, `suggestion.accepted`,
`suggestion.discarded`, `job.updated`, `plugin.notify` and
`plugin.<id>.<name>`. An event about one item carries `itemId` and `kind`. A
note event also carries `noteId`.

Three rules apply before the space rules. An event with an `Audience` list
reaches only those user ids. A `job.*` event reaches admins and the person who
started the job. A plugin actor passes the space rules only for its granted
spaces, and an acting token actor only where both the person and the plugin
may view.

### Event Flow

1. The application publishes each event to the browser streams and passes it
   to `EventSink`, which feeds the module bus.
2. The job engine and the plugin host publish on the bus.
3. A bridge handler on the bus forwards `job.*` and `plugin.*` events to the
   browser streams. The application never publishes those types, so no event
   loops.
4. Each plugin has one bus handler. It keeps the events that match its approved
   manifest's patterns and that pass `App.NewEventFilter` for the plugin actor,
   then queues them for delivery.

## Suggestions

The `suggestions` table stores suggested edits with a version number that
grows on every write. Every write is a compare-and-swap on that version.
`plugins.md` specifies the model.

Remapping runs wherever comment anchors are remapped, which covers
`UpdateNote`, version restore and link rewrites. It reads the open and stale
suggestions of the note once, moves their anchors through the body change,
marks orphaned edits, and stores each changed suggestion. On a conflict it
reads the suggestion again and only marks orphans against the new body.

Accepting applies the edits through `UpdateNote` with the note's current
`updatedAt` and a forced version, then stores the suggestion. A note that
changed in between is read again, up to 3 times. Deleting a note, a folder
that holds notes, or a space deletes their suggestions.

## Instance Settings

`GET /api/settings` returns fields by caller. An anonymous caller gets the
instance name and whether signup is open, which the login and signup pages
need. A signed-in user also gets the AI switches and the default AI provider,
and the default space id only when they can view that space. An admin gets
every field.

## AI Providers

A provider comes from the configuration file or from the database. A database
provider with an `OwnerID` is personal. Only its owner may list, call, test,
update or delete it, and admins see it only in the admin listing. The
semantic index uses an instance provider only. Deleting a user deletes their
personal providers.

## Usage and Budgets

`ai.md` specifies the recording rules, the reports and the budget checks. This section covers the storage.

| Table | Contents |
| --- | --- |
| `ai_usage` | One row per provider call, with tokens, images, duration, status, error text, cost in millionths and the estimate flag. Indexed on (user_id, created_at), (created_at), (provider_id, created_at) and (plugin_id, created_at) |
| `ai_usage_monthly` | Summed counts per month, user, provider, model, feature and plugin, for raw rows older than 13 months |
| `ai_budgets` | Monthly token and cost limits per user or plugin, and the lift expiry |

`settings` carries the two default user limits, and `ai_providers.prices` holds the price map as JSON. Migration 4 adds all of them.

Cost is an integer count of millionths of the currency unit. The server computes it once, when it records the call, from the prices in force at that time. Summing integers keeps totals exact across any grouping.

The app records usage after the provider answers, with a context that outlives the request, so a client disconnect still produces a `canceled` row. A failed usage write never fails the call. A stream wrapper forwards the provider chunks and writes the row before it closes its output channel.

The retention job rolls whole UTC months only. For any month, a report reads either raw rows or one monthly row per group, and the sums match. `FuzzUsageRollup` in `internal/index` records random rows, rolls them up at a random cutoff, and checks every grouping against the totals from before the rollup and against a brute-force sum.

## Sync Feed

Offline clients replicate through `GET /api/sync`. `offline.md` specifies the
client side.

Every folder, note and file write in the index takes the next value of one
counter stored in the `counters` table. That covers an upsert, a trash, a
restore and a move. The value is the item's `rev`. The counter never
decreases, because it lives apart from the item tables, so deleting the newest
item does not free its number.

A pull reads the counter first, then returns folders, notes and files with
`since < rev <= ceiling` in revision order, in the `folders`, `notes` and
`files` arrays. A write that commits during the pull gets a revision above
the ceiling and arrives on the next pull. The feed covers every item in
spaces the user can view in full, plus items reached through direct shares
and through shares that include children. Each item passes the same grant
check as `GetItem`. File content is not part of the feed.

The last page carries `liveIds`, the complete set of visible folder, note and
file ids, so a client removes items that were deleted or became hidden. It
also carries `accessKey`, a hash of the admin flag, space roles and share
grants on every kind of item. Gaining access does not change an item's
revision, so when the key changes the client pulls a full snapshot.

Offline clients create notes, folders and files with ids they generate.
`CreateNote`, `CreateFolder` and `UploadFile` accept an id in the `core.NewID`
shape. If that id exists with the same kind, creator and space, the call
returns the stored item, so replaying a create is safe.

### Items Index

`files.md` specifies folders and files. Migration 5 adds their storage, drops
the `attachments` table and adds `paste_folder` to `spaces`.

| Table | Contents |
| --- | --- |
| `folders` | One row per folder, with parent id, name, entry path, position, folder note id, revision and deletion fields |
| `files` | One row per file, with every stored `core.File` field and `text_state`, one of none, pending, done, failed and skipped |
| `files_fts` | FTS5 over file names and extracted text, with the tokenizer of `notes_fts` |
| `notes` | Rebuilt so that its path index matches the two new tables |

`folders`, `files` and `notes` each carry a unique index on (space_id, path COLLATE NOCASE) over rows with `deleted = 0`. A trashed row keeps its old path, so it never blocks a new item with the same name. `files` also has an index on (space_id, sha256, size) for rename matching. Migration 5 adds `target_kind` to `shares`, `public_links` and `comments`, where `note_id` holds the target id of any kind. It adds `target_kind`, `form`, `embed` and `heading` to `note_links`, where `target_note_id` holds the resolved item id.

Folder and file writes draw revisions from the `note_rev` counter, so one sequence orders all three kinds. A move, trash or restore of a folder gives each changed row its own revision, in path order, inside one transaction. `ItemsChangedSince` merges the three tables in revision order with the ceiling rule of the note feed.

The subtree of a folder holds every item whose parent chain reaches the folder. It also holds every item below the folder path that shares the folder state. For a live folder that state is live. For a trashed folder it is trashed at the same instant. Trash marks the live items of the subtree with the time and actor of the folder. Restore clears the mark on the items with that time and rewrites paths when the folder returns to a new path. Delete removes the items that share the folder state, with their comments, shares, public links and stars. Links to a deleted item become unresolved. An item trashed on its own below the folder stays in the trash.

A write of a live folder or file checks two rules. The parent id must name the live folder at the parent directory of the path, or be empty at the root. No other live folder, note or file may hold the path under Unicode case folding. A violation returns `ErrInvalid` or `ErrExists`. A live note write checks the second rule against folders and files.

File search snippets come back escaped for HTML. FTS5 marks matches with two private-use characters, the index escapes the snippet and then turns the markers into `<mark>` tags. Stored text has those characters removed.

## Reconcile and Watch

The startup scan and the watcher keep the index in line with the files on
disk. `ScanTree` walks a space and returns its folders, notes and files
without hashing. A reconcile pass then matches items that moved outside the
application before it creates or removes rows.

- A note keeps the id in its frontmatter, so a note found at a new path keeps
  its row. A second file with the same id gets a new id written back.
- A file at an unknown path matches a vanished file row when size and SHA-256
  are equal. The pass hashes a new file only when a vanished row has the same
  size. Ties prefer the same base name, then the nearest folder.
- A folder at an unknown path matches a vanished folder when at least half of
  the note and file ids known below the vanished folder, and at least one,
  appear below the new folder. Parents are matched first, so a moved folder
  carries its subtree before its children are checked. A folder whose name
  changed only in letter case keeps its row.
- A vanished item with no match loses its row, with its comments, shares,
  public links, versions and thumbnails.

The startup scan removes unmatched rows at once. The watcher indexes a note
write in a known folder directly. Every other event schedules a rescan of the
space after 1 s. In a rescan, an item that vanished less than 10 s ago keeps
its row, so a rename that arrives as a remove and a later write keeps its id.
A further rescan runs when the window ends.

The application records every path it writes, moves or trashes, and a folder
mark covers everything below the folder. The watcher ignores events on those
paths for 2 s. Structural writes and scans in one space run under a
per-space lock, so a scan never sees a move half done. An upload streams its
content before it takes the lock.

Two background queues run every 30 s and after each write that queues work.
The hash queue fills a missing SHA-256. The text queue extracts the text of
PDFs and text-like files up to 20 MiB into `files_fts`, keeps the first
200 000 characters, and marks every other file skipped.

## Link Rewriting

A rename or move of an item, whether made in the application or found by a
scan, rewrites the links that would stop resolving to the same item.

1. Before the change, the application reads a snapshot of the space and the
   stored outbound links of every live note. `links.LinkKeys` turns the stored
   links into a key index.
2. `links.Affected` returns the notes whose links may resolve differently
   after the change, from that key index.
3. One `links.NewRewriter` serves the whole change set. For each affected note
   the actor can edit, the rewriter returns byte edits for the body.
4. Before writing, the application saves a version labelled `Links updated`.
   The write happens only while the note's `updatedAt` still matches the value
   read. A mismatch retries once from a fresh read, then skips the note with a
   warning.

A scan rewrites links as the system, so every note is editable. When a note is
indexed, its links are parsed with `internal/links`, resolved against the
snapshot, and stored with form, embed flag, heading, target id and target
kind. Rendering resolves wikilinks, relative markdown links and markdown images
through the same snapshot.

## Conventions

- Services return the sentinel errors from `internal/core`, and the HTTP layer
  maps them to status codes. The errors are `ErrNotFound`, `ErrForbidden`,
  `ErrUnauthorized`, `ErrConflict`, `ErrExists`, `ErrInvalid`, `ErrTooLarge`,
  `ErrUnsupported`, `ErrRateLimited`, `ErrQuotaExceeded` and `ErrUnavailable`.
  `routes.md` lists the status code of each.
- Logging uses `log/slog` with structured keys.
- Routing uses `net/http.ServeMux` method and wildcard patterns.
- The database stores times as UTC RFC 3339 strings with 9 fractional digits,
  so they sort correctly as text.
- Tests run offline. Provider protocol tests use `httptest` servers.
