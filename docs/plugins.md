# Modules and Plugins

Markspace is built from modules. A module contributes HTTP routes, event
handlers, background jobs, commands, panels, settings, importers and AI tools
through one registry. Built-in features are modules compiled into the binary.
A plugin is a module that runs as a separate HTTP service, usually in its own
Podman quadlet, and talks to Markspace over HTTP with JSON bodies.

`files.md` defines folders, notes and files. `ai.md` defines AI requests and
usage accounting. This document defines the registry, the shared services
modules use, and the plugin protocol.

## Layers

```
cmd/markspace          the server and the management commands
internal/wiring        assembles the module engine, the built-in modules and the plugin host
internal/core          types, errors, service interfaces, core.Backend
internal/ports         storage, index, renderer and AI interfaces
internal/app           core.Backend over the ports
internal/module        the module engine: registry, event bus, jobs, scheduler, contributions, suggestions
internal/modules/<m>   one package per built-in module: routes, handlers, contributions
internal/plugin        the plugin host, which adds each plugin to the registry as a module
internal/links         link parsing, resolution and rewriting
internal/api           authentication, middleware, error mapping, origin checks, JSON, multipart and SSE helpers
internal/web           server pages, static client, service worker
sdk                    Go SDK for plugin authors, with sdk/ui for UI trees
sdk/python             Python SDK for plugin authors
sdk/example            the Word count example plugin
plugins/wiki           the bookmark wiki plugin
deploy                 quadlet units for Markspace and its plugins
test/server       Go integration tests of the module engine and the plugin host
test/vectors           protocol and anchor test vectors shared by the SDKs and the host
```

A module package depends on `internal/core`, `internal/module` and
`internal/api` helpers. It never imports another module package, `internal/app`
or a port implementation. Modules talk to each other only through
`core.Backend` and events. `internal/api` has no route table. The API and web
servers mount the registry routes.

## Registry

```go
package module

type Module interface {
	// Manifest describes the module. Built-in modules return a static value.
	Manifest() Manifest
	// Register adds the module's contributions. It runs once at startup.
	Register(r *Registry) error
}

type Registry struct{ /* unexported */ }

func (r *Registry) API(pattern string, h http.HandlerFunc)     // pattern must start with "METHOD /api/"
func (r *Registry) Web(pattern string, h http.HandlerFunc)     // server pages
func (r *Registry) On(eventType string, h EventHandler)        // "note.updated", "file.*", "*"
func (r *Registry) Job(kind string, h JobHandler, opts JobOptions)
func (r *Registry) Schedule(jobKind string, every time.Duration, jitter time.Duration)
func (r *Registry) Command(c Command)
func (r *Registry) Panel(p Panel)
func (r *Registry) Settings(s SettingsSchema)
func (r *Registry) Importer(i Importer)
func (r *Registry) AITool(t AITool)
```

A duplicate route pattern, job kind, command id or panel id fails startup and
names both modules.

### Built-in Modules

| Module | Contents |
| --- | --- |
| `auth` | login, signup, sessions, tokens |
| `users` | users, invites |
| `spaces` | spaces, members, export and import |
| `notes` | notes, tree, tags, trash, stars, recent, versions, markdown rendering |
| `files` | folders, files, items, link resolution |
| `comments` | comments on notes, folders and files |
| `sharing` | shares, public links |
| `search` | full-text and semantic search, suggest |
| `sync` | the sync feed and the event stream |
| `ai` | providers, catalog, routes, runs, streams, conversations, images, usage and budgets |
| `suggestions` | proposed edits and the Suggestions rail panel |
| `jobs` | the job queue API and the Jobs page |
| `admin` | settings, stats, reindex |
| `ui` | the contribution routes and the built-in rail tabs |
| `plugins` | the plugin host API, the plugin admin API and the Plugins page |

Every row is a package under `internal/modules`. `routes.md` lists the routes
of each module. The `ui` module declares the seven rail tabs that the client
renders itself. They are Outline, Comments, AI, History, Share, Files and
Info. The server pages other than Jobs and Plugins, such as the file viewer,
the admin pages and the public link pages, stay in `internal/web`.

## Event Bus

```go
type Event struct {
	ID      string          `json:"id"`      // core.NewID
	Type    string          `json:"type"`    // note.created, file.updated, job.updated, ...
	SpaceID string          `json:"spaceId,omitempty"`
	ItemID  string          `json:"itemId,omitempty"`
	Kind    core.ItemKind   `json:"kind,omitempty"`
	ActorID string          `json:"actorId,omitempty"`
	Path    string          `json:"path,omitempty"`
	OldPath string          `json:"oldPath,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
	At      time.Time       `json:"at"`
}
```

- The bus delivers each event to in-process handlers on one goroutine per
  handler, each with a queue of 1024 events. A full queue drops the oldest
  event and increments a counter shown on the admin page.
- The bus logs a panic in a handler, and the handler keeps receiving later
  events.
- The browser event stream is one subscriber, with the per-user access filter
  from `design.md`.
- Plugin delivery is one subscriber per plugin with a persistent queue,
  described under Plugins.

Event types:

| Type | Data |
| --- | --- |
| `note.created`, `note.updated`, `note.deleted`, `note.trashed`, `note.restored` | `{title, updatedAt}` |
| `folder.*`, `file.*` | the same set as notes, with `{name, mime, size, sha256}` for files |
| `item.moved` | `{oldParentId, newParentId}` |
| `comment.created`, `comment.resolved` | `{commentId}` |
| `suggestion.created`, `suggestion.accepted`, `suggestion.discarded` | `{suggestionId}` |
| `job.updated` | `{jobId, status, progress, total}` |
| `space.created`, `space.updated`, `space.deleted` | `{name}` |
| `plugin.<id>.<name>` | set by the plugin |

## Jobs

Jobs are durable background tasks. The queue lives in the index database, so
jobs survive a restart.

```sql
CREATE TABLE jobs (
	id          TEXT PRIMARY KEY,
	kind        TEXT NOT NULL,             -- "<module>.<name>", e.g. "plugin.bookmark-wiki.ingest"
	module      TEXT NOT NULL,
	space_id    TEXT NOT NULL DEFAULT '',
	created_by  TEXT NOT NULL DEFAULT '',  -- user id, or "plugin:<id>"
	status      TEXT NOT NULL,             -- queued, running, paused, done, failed, canceled
	title       TEXT NOT NULL DEFAULT '',
	progress    INTEGER NOT NULL DEFAULT 0,
	total       INTEGER NOT NULL DEFAULT 0,
	message     TEXT NOT NULL DEFAULT '',
	payload     TEXT NOT NULL DEFAULT '{}',
	result      TEXT NOT NULL DEFAULT '{}',
	attempts    INTEGER NOT NULL DEFAULT 0,
	max_attempts INTEGER NOT NULL DEFAULT 3,
	run_after   TEXT NOT NULL DEFAULT '',
	lease_until TEXT NOT NULL DEFAULT '',
	parent_id   TEXT NOT NULL DEFAULT '',
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL
);
CREATE TABLE job_log (job_id TEXT NOT NULL, at TEXT NOT NULL, level TEXT NOT NULL, message TEXT NOT NULL);
```

- A worker pool of `jobs.workers` goroutines, default 4, claims queued jobs
  whose `run_after` has passed, with a lease of 60 s that the handler renews
  on each progress update. A job whose lease expires returns to `queued` and
  counts an attempt.
- A failed attempt retries after 30 s, then 2 min, then 10 min, up to
  `max_attempts`, then the status becomes `failed`.
- Cancel sets `canceled` and cancels the handler's context. Pause sets
  `paused` after the current attempt returns, and resume sets `queued`.
- A job can create child jobs. The parent's progress counts finished children
  when its own `total` is 0.
- A module or plugin may have at most 10 000 queued jobs.
- The engine deletes finished jobs and their logs after 30 days.

`GET /jobs` lists the actor's jobs, and all jobs for an admin, with progress,
logs, cancel, pause and resume.

## Contributions

The client builds its menus, command palette and rail tabs from
`GET /api/ui/contributions`, filtered for the actor and the current item.

```go
type Command struct {
	ID       string   `json:"id"`       // "<module>.<name>"
	Title    string   `json:"title"`
	Icon     string   `json:"icon,omitempty"`
	Contexts []string `json:"contexts"` // global, space, note, folder, file, selection, link, image
	Shortcut string   `json:"shortcut,omitempty"`
	MinAccess core.Access `json:"minAccess"`
}

type Panel struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Icon     string   `json:"icon"`
	Where    string   `json:"where"`    // rail, page
	Contexts []string `json:"contexts"` // note, folder, file, space, global
	Builtin  string   `json:"builtin,omitempty"` // client renderer name for built-in panels
	Order    int      `json:"order"`
}
```

Built-in panels name a client renderer, such as `comments`, `history`,
`share` or `ai`. Plugin panels have no client code. The client asks the
module for a UI tree, described below, and draws it with the shared panel
components.

The `ui` module declares the rail panels `outline`, `comments`, `ai`,
`history`, `share`, `files` and `info`, and the `suggestions` module declares
`suggestions`. Each has a client renderer name equal to its id.

`GET /api/ui/contributions?itemId=&spaceId=&contexts=` resolves the actor's
access on the item or space. The filter contexts are `global`, plus `space` and
the item kind when they apply, plus `selection`, `link` or `image` from the
query. A plugin's contributions are hidden in spaces the plugin is not granted.

The `ui` module validates every response before it reaches the client, with
`ParseCommandResponse` for plugins and `ValidateUI` for trees. An invalid
response answers 503. A UI tree or an `openPanel` that names an item the actor
cannot view also answers 503. An action may answer `{job}` to run long work as
a job.

Running a command posts `POST /api/commands/{id}` with
`{context: {spaceId, itemId, kind, selection?, link?, imageFileId?}}`. The
response is one of:

```json
{"toast": {"message": "Queued 412 bookmarks", "kind": "info"}}   kind is info, success, warn or error
{"openPanel": {"id": "bookmark-wiki.wiki", "itemId": "..."}}
{"navigate": "/s/docs/n/wiki/index"}
{"job": {"id": "..."}}
{"ui": { "type": "stack", "children": [] }}
```

## Declarative UI

A plugin panel, page, settings form or command result is a JSON tree. The
client renders it in `internal/web/static/js/plugins.js` with the `ui.js`
components, so plugin UI
matches the application, follows `ui.md` and section 5.11 of `primer.md`, and
runs no plugin script. A command response with `toast` shows its message in a
Banner at the top of the plugin panel or page. The `kind` values `info`,
`success`, `warn` and `error` select the info, success, warning and critical
Banner variants. A critical Banner has no dismiss button.

| Type | Fields |
| --- | --- |
| `stack` | `children`, `gap` (`s`, `m`, `l`) |
| `section` | `title`, `description`, `count`, `children` |
| `form` | `id`, `title`, `description`, `fields`, `submit` (label), `action` (required), `more` (fields under a disclosure) |
| field | `{type: text\|textarea\|number\|url\|select\|checkbox\|file\|note\|folder\|space, name, label, hint, value, required, options}`. `options` is `[{value, label}]` and is required for `select` only. A `file` field shows a Choose file button. On submit the module page uploads the chosen file with `POST /api/files` and sends the field value as `{fileId, name, size, type}` |
| `list` | `items: [{title, subtitle, icon, link, badge, actions}]`, `empty`. `badge` has the `badge` fields, and `actions` is a list of `button` nodes |
| `table` | `columns: [{key, label, numeric}]`, `rows`, `rowActions` |
| `text`, `markdown` | `value`. The note renderer renders markdown with raw HTML escaped |
| `progress` | `value`, `max`, `label`, `jobId` (live updates from `job.updated`) |
| `badge` | `label`, `tone` (`neutral`, `good`, `warn`, `bad`, `accent`) |
| `button` | `label`, `variant` (`primary`, `secondary`, `danger`), `action`, `confirm`. `confirm` opens a ConfirmationDialog whose title is the `confirm` text and whose confirm button repeats the button label |
| `link` | `label`, `to` (`{itemId}` or `{url}`) |
| `empty` | `title`, `body`, `action` (a `button` node) |
| `code` | `value`, `language` |

An action is `{id, payload}`. The client posts it to
`POST /api/modules/{module}/actions/{id}` with the form values and the context,
and the response has the same shape as a command response.

The tree is limited to 2000 nodes, a depth of 32 and 1 MiB. Every JSON object
in the tree counts as a node and adds a level of depth, including fields, list
items, actions and payloads. Link targets must be items the actor can view or
`http:` and `https:` URLs.

## Suggestions

A suggestion is a proposed edit to a note. Plugins, AI features and people
can create one. It is a core module, because both the AI editor and plugins
use it.

```go
type Suggestion struct {
	ID         string          `json:"id"`
	SpaceID    string          `json:"spaceId"`
	NoteID     string          `json:"noteId"`
	AuthorKind string          `json:"authorKind"` // user, plugin, ai
	AuthorID   string          `json:"authorId"`
	Title      string          `json:"title"`      // "Add See also links"
	Rationale  string          `json:"rationale,omitempty"`
	Edits      []SuggestedEdit `json:"edits"`
	BaseUpdatedAt time.Time    `json:"baseUpdatedAt"`
	Status     string          `json:"status"`     // open, accepted, partial, discarded, stale
	CreatedAt  time.Time       `json:"createdAt"`
	ResolvedBy string          `json:"resolvedBy,omitempty"`
	ResolvedAt *time.Time      `json:"resolvedAt,omitempty"`
}

// SuggestedEdit replaces the anchored passage with Text. An empty Exact with
// Start == End inserts at a point located by Prefix and Suffix.
type SuggestedEdit struct {
	ID     string             `json:"id"`
	Anchor core.CommentAnchor `json:"anchor"`
	Text   string             `json:"text"`
	Status string             `json:"status"` // open, accepted, discarded, orphaned
}
```

- Anchors use `internal/anchor`, so a suggestion survives edits made after it
  was created. An edit whose passage no longer resolves becomes `orphaned`.
  A suggestion with only orphaned open edits becomes `stale`.
- Accepting one edit applies it through `UpdateNote` with the current
  `updatedAt`, which saves a version. Accept all applies every open edit in
  one update.
- Creating a suggestion needs comment access on the note. Accepting or
  discarding needs edit access.
- The editor shows open edits as tracked changes, and the Suggestions rail tab
  lists them grouped by author. `assistant.md` describes the editor side.

Routes:

```
GET    /api/notes/{id}/suggestions?status=open
POST   /api/notes/{id}/suggestions       {title, rationale, edits: [{anchor, text}]}
POST   /api/suggestions/{id}/accept      {editIds?}   omitted means all open edits
POST   /api/suggestions/{id}/discard     {editIds?}
GET    /api/suggestions?space=&author=&status=   the actor's inbox across notes
GET    /api/suggestions/{id}
DELETE /api/suggestions/{id}             withdraw, author only
```

The inbox lists suggestions on notes the actor may edit. `author=me` lists the
actor's own suggestions on notes the actor may view, and `status=all` matches
every status.

## Plugins

### Identity and Access

- Each installed plugin has a user row of kind `plugin`, named after the
  plugin, which cannot sign in. Notes, files, versions and comments it writes
  show that user as author.
- An admin grants the plugin a list of spaces, each with a role: `viewer`,
  `commenter` or `editor`. The plugin never gets `admin` or `owner` on a
  space and never gets instance admin.
- When a user runs a plugin command or panel action, the request carries an
  acting token. The token is valid for 10 minutes and for that one
  interaction, and its access is the intersection of the user's access and the
  plugin's grant. Writes made with it show the user as author and the plugin
  as `via`.
- Permissions from the manifest limit which host API groups a token may call,
  whatever the space role.
- A plugin is the registry module `plugin.<id>`. Its commands, panels and
  importers have the ids `<id>.<local id>`, and its job kinds are
  `plugin.<id>.<kind>`.
- A plugin whose status is `active`, `down` or `needs_approval` may
  authenticate. A `paused` or `uninstalled` plugin may not.
- An acting token names its interaction, which is `command:<id>`,
  `panel:<id>`, `action:<id>` or `import:<id>`. It lasts 10 minutes. A job started by a person
  gets a token for `job:<id>`, valid while the attempt runs and at most 24 h,
  and revoked when the attempt ends. A token is valid only while its
  interaction names a contribution of the approved manifest, any valid action
  id, or a running job of the plugin.

### Manifest

`GET <baseUrl>/manifest.json`:

```json
{
  "protocol": 1,
  "id": "bookmark-wiki",
  "name": "Bookmark wiki",
  "version": "0.1.0",
  "description": "Reads bookmarks, summarises them and maintains a linked wiki.",
  "homepage": "https://example.invalid/bookmark-wiki",
  "permissions": ["items:read", "items:write", "links:read", "search", "ai:complete",
                  "ai:embed", "web:fetch", "jobs", "storage", "ui", "suggestions", "events"],
  "events": ["note.created", "note.updated", "file.created"],
  "jobs": [{"kind": "ingest", "title": "Ingest bookmarks", "maxAttempts": 3},
           {"kind": "lint", "title": "Lint wiki"}],
  "schedules": [{"job": "lint", "every": "24h"}],
  "commands": [{"id": "add-url", "title": "Add link to wiki", "contexts": ["global", "link", "note"]}],
  "panels": [{"id": "wiki", "title": "Wiki", "icon": "book", "where": "rail", "contexts": ["note", "space"]},
             {"id": "home", "title": "Bookmark wiki", "icon": "book", "where": "page", "contexts": ["global"]}],
  "importers": [{"id": "netscape", "title": "Browser bookmarks", "accept": [".html"]}],
  "settings": {"fields": [{"type": "space", "name": "space", "label": "Wiki space", "required": true}]},
  "secrets": [{"name": "karakeep_token", "label": "Karakeep API token"}]
}
```

Permission groups:

| Permission | Host API |
| --- | --- |
| `items:read` | tree, notes, folders, files, file content |
| `items:write` | create, update, move, rename, copy, upload, replace, trash |
| `links:read` | resolve, backlinks, outbound links |
| `search` | text and semantic search |
| `comments` | list and create comments |
| `suggestions` | create and withdraw suggestions |
| `ai:complete`, `ai:embed`, `ai:vision`, `ai:image` | AI calls, charged to the plugin |
| `web:fetch` | fetch pages through the guarded fetcher |
| `jobs` | create jobs, report progress and logs |
| `storage` | the plugin key-value store and blob store |
| `ui` | panels, pages, commands, notifications |
| `events` | event delivery for the types in `events` |

Commands, panels and importers need the `ui` permission, and event delivery
needs `events`. `GET /me`, `GET /settings` and `GET /secrets/{name}` need no
group, and `GET /ai/models` needs any AI group.

A command's `minAccess` defaults to view when its contexts include an item or
a space, and to none for a global command. It is capped at edit. A schedule's
period must be at least 1 minute, and its jitter is a tenth of the period, at
most 1 h.

### Install

1. An admin opens Admin, then Plugins, then Add plugin, and enters the base
   URL, such as `http://bookmark-wiki:8090`. Only admins can add plugins, and the
   URL may point at a private address.
2. Markspace fetches the manifest and shows the name, version, permissions,
   events, jobs, schedules and secrets.
3. The admin picks spaces and roles, fills in the secrets and settings, and
   approves.
4. Markspace creates the plugin user, a plugin token (`mspl_` followed by 40
   random characters, stored as a hash) and a signing secret of 32 random
   bytes. It sends both to `POST <baseUrl>/install`:

```json
{"pluginId": "bookmark-wiki", "markspaceUrl": "http://markspace:8080",
 "token": "mspl_...", "signingSecret": "base64...", "grants": [{"spaceId": "...", "role": "editor"}],
 "permissions": ["..."]}
```

   The plugin stores them in its own volume. If the call fails, the admin page
   shows the token and secret once, to set as the plugin's environment
   variables `MARKSPACE_URL`, `MARKSPACE_TOKEN` and `MARKSPACE_SIGNING_SECRET`.
5. When a later manifest asks for more permissions, events or secrets, the
   plugin keeps running with its old grant until an admin approves the change.

A first install call is signed with the new secret. Uninstall keeps the
signing secret, and a later install signs its install call with that previous
secret, because the plugin checks a re-install against the secret it stored.
The payload carries the new secret either way. Install fetches the manifest
again and refuses it with 409 when its SHA-256 differs from the one the admin
reviewed.

Check for manifest changes fetches the manifest again. The same manifest
changes nothing. A manifest that asks for more permissions, events or secrets
is stored as pending and sets `needs_approval`, and the old grant stays in
force. Any other change applies at once and registers the module again.

Uninstall always revokes the plugin token and acting tokens, removes the
module from the registry, deletes grants and queued deliveries, removes the
schedules and their `job_schedules` rows, cancels the plugin's jobs and
deactivates the plugin user. The admin chooses what happens to the data.

- Delete removes the key-value entries, blobs, secrets and AI usage rows, and
  the notes and files the plugin user created in its granted spaces, then the
  folders it created that are left empty.
- Keep leaves the key-value entries, blobs, secrets and usage for a later
  install, which reuses the plugin user.

### Calls from Markspace to a Plugin

Every request except `GET /health` and `GET /manifest.json` is a POST with a
JSON body and these headers:

```
X-Markspace-Plugin: bookmark-wiki
X-Markspace-Delivery: <id>
X-Markspace-Signature: t=<unix seconds>,v1=<hex HMAC-SHA256(key, signed string)>
```

An HMAC is a short code computed from the message and a secret key. The key
is the 32 bytes decoded from the base64 signing secret. The signed string
joins five parts with a newline (`\n`):

```
<t>
<METHOD>
<path, without query>
<delivery id>
<body bytes>
```

The plugin recomputes the code and rejects the request when the code differs,
when the timestamp is more than 300 s from its clock, or when it has already
seen the same delivery id with the same timestamp. Markspace signs every
attempt again with a fresh timestamp, so a retry of one delivery is accepted
and a replayed copy is not. Because the path and delivery id are signed, a
captured body cannot be sent to another route.

- The header must match `t=<timestamp>,v1=<digest>` exactly. The timestamp is
  decimal with no sign and no leading zero, and fits in a signed 64-bit
  integer. The digest is 64 lowercase hex characters. Any whitespace, other
  order or extra part is `malformed`.
- The method is signed as sent. The path is the path of the request line as
  sent, with percent-encoding kept and the query left out.
- Checks run in the order malformed, mismatch, expired, missing delivery,
  replay. A difference of exactly 300 s is accepted.
- The receiver records the delivery id and timestamp of each accepted request.
  A later request with the same pair within the window is a replay. It does
  not record rejected requests, and the record holds at most 10 000 entries.
- A first install call is unsigned when the plugin holds no signing secret.
  The SDKs accept it only when `MARKSPACE_TOKEN` is not set and the install
  window is open. The window lasts 10 minutes after the plugin starts.
  `PLUGIN_INSTALL_WINDOW` sets it in seconds, and `0` keeps it open until the
  first install. Use `0` only when nothing but Markspace can reach the plugin
  port.

The SDKs answer a failed check with 401 and one of these codes:

| Code | Cause |
| --- | --- |
| `malformed` | the header does not match the grammar |
| `mismatch` | the code differs from the one computed with the secret |
| `expired` | the timestamp is more than 300 s from the plugin clock |
| `missing_delivery` | `X-Markspace-Delivery` is empty |
| `replay` | a request with the same delivery id and timestamp was already accepted |
| `wrong_plugin` | `X-Markspace-Plugin` names another plugin |

| Path | Body | Response |
| --- | --- | --- |
| `/events` | `{events: [Event]}`, up to 100 per request | `204` |
| `/jobs/{kind}` | `{job: {id, kind, payload, attempt, title, spaceId}, actingToken?, user?}`. `kind` is the local kind from the manifest. `actingToken` and `user` are present when a user started the job | `200 {result}` when done, `202` when the plugin reports completion later through the host API |
| `/commands/{id}` | `{context, actingToken, user: {id, name}}` | a command response |
| `/panels/{id}` | `{context, actingToken, user}` | `{ui}` |
| `/actions/{id}` | `{context, values, payload, actingToken, user}` | a command response, or `{job}` for long work |
| `/import/{importer}` | `{fileId, spaceId, actingToken, user}` | `{job}` or a command response |
| `/health` (GET) | none | `200 {"status": "ok", "version": "..."}` |
| `/install` | the install payload | `200 {"ok": true}` |

- Event, command, panel, import, health and install calls time out after
  10 s. Action and job calls time out after 30 s, so a long job returns `202`
  and reports through the host API.
- Each event goes to a persistent `plugin_deliveries` queue as one delivery. A
  sender per plugin claims up to 100 due deliveries and posts them as one
  batch, with the id of the first delivery as `X-Markspace-Delivery`. It does
  not claim while the plugin is down or paused, and sleeps until the next
  delivery is due.
- A delivery retries after 5 s, doubling to 10 min, and expires after 24 h.
- The host calls `GET /health` every 60 s. A plugin that fails 3 checks in a
  row is marked down, and one success marks it active again.
- A settings change sends a `plugin.settings.updated` event with
  `{pluginId, settings}` to that plugin, whatever its event patterns.

Plugin jobs:

- `200 {result}` completes the job.
- `202` waits for `PATCH /jobs/{id}`. Each progress report renews the 60 s
  lease. When the lease expires the attempt ends and the job retries or
  fails as for any other attempt.
- `status: failed` in the PATCH counts as a failed attempt.
- `400`, `404` and `422` from the plugin fail the job at once.
- A PATCH for a job with no waiting attempt in this process answers 409. A
  PATCH for a canceled job answers 200 with status `canceled`.

### Host API

`/api/plugin/v1/`, authenticated with `Authorization: Bearer <plugin token or acting token>`.

- Lists return `{"items": [...], "nextCursor": "..."}`. `nextCursor` is absent
  on the last page.
- A POST may carry an `Idempotency-Key` header of up to 128 characters. The
  server stores the response for 24 h per plugin and key, and returns the
  stored response for a repeated key. The SDKs set a fresh key on every POST
  and reuse it on retries.
- A replayed response carries `Idempotent-Replayed: true`. Server errors,
  429, streams and responses over 1 MiB are not stored.
- Too many requests answer 429 with `Retry-After`.
- Clients retry 429, 502, 503, 504 and connection errors, honouring
  `Retry-After`. They do not retry 500.
- The search cursor is the offset of the next page.

```
GET    /me                                   {pluginId, name, userId, status, grants, permissions, actingFor?}
GET    /settings                             {settings}
GET    /spaces/{id}/tree
GET    /items/{id}                           a note item carries its body and frontmatter
POST   /notes                                {id?, spaceId, parentId, title, content, tags, icon?, frontmatter?, folderNote?, folderId?}
                                             folderNote with folderId creates <folder>/index.md, 409 when it exists
PATCH  /notes/{id}                           {title?, content?, tags?, icon?, frontmatter?, baseUpdatedAt}, baseUpdatedAt required
POST   /folders                              {spaceId, parentId, name}
POST   /files                                multipart
GET    /files/{id}/content
POST   /items/{id}/move | /copy | /trash
GET    /notes/{id}/links                     outbound links resolved
GET    /items/{id}/backlinks
POST   /resolve                              {spaceId, fromNoteId, targets}
GET    /search?q=&mode=&space=&limit=
POST   /suggestions                          {noteId, title, rationale, edits}
GET    /suggestions?noteId=&status=&author=me&space=&limit=
DELETE /suggestions/{id}
POST   /comments                             {itemId, body, anchor?}
POST   /ai/complete                          AIRequest with purpose? (stream with Accept: text/event-stream)
POST   /ai/embed                             {texts: [string]} -> {provider, model, vectors}
POST   /ai/image                             {prompt, size?, n?, fileId?, maskFileId?} -> data URLs
POST   /web/fetch                            {url, respectRobots?, includeHtml?} -> {finalUrl, status, contentType, title, text, markdown, html?, bodyBase64?, sha256, truncated?, fetchedAt}
POST   /jobs                                 {kind, title, payload, spaceId?, runAfter?, parentId?}
PATCH  /jobs/{id}                            {progress?, total?, message?, status?: done|failed, result?} -> Job. status canceled in the reply means stop
POST   /jobs/{id}/log                        {level, message}
GET    /kv/{key} -> {key, value, updatedAt}   PUT /kv/{key} {value}   DELETE /kv/{key}   GET /kv?prefix=&cursor= -> items of {key, size, updatedAt}
PUT    /blobs/{key}   GET /blobs/{key}   DELETE /blobs/{key}
GET    /secrets/{name}                       -> {name, value}
GET    /files/{id}/text                      -> {text, truncated}
GET    /ai/models                            -> items of {providerId, model, name, contextLength, vision, imageGeneration, imageEditing, embedding, prices, priceSource}
POST   /notify                               {userIds?, spaceId?, message, link?}
POST   /events                               {type: "plugin.<id>.<name>", spaceId?, data}
```

Notes and frontmatter:

- `frontmatter` sets note frontmatter as `{key: value}`. `title`, `tags` and
  `icon` map to the note fields. Other keys Markspace owns, such as `id`,
  `position`, `created`, `updated`, `author`, `updated_by`, `deleted` and
  `parent`, answer 400. Any other key goes to `Note.Extra`, and a null value
  deletes the key.
- `GET /items/{id}` returns `note.frontmatter` with the owned fields and the
  extra keys.
- `content` is the note body. A frontmatter block at the top of `content`
  stays in the body as text, so a plugin sets frontmatter only through
  `frontmatter`.

Web fetch:

- HTML, plain text, markdown and JSON fill `text` and `markdown`. Any other
  type up to 10 MiB fills `bodyBase64`. `sha256` is the digest of the body.
- A final response with a status of 300 or more, such as 404 or 403, answers
  200 with that `status`, `finalUrl` and `contentType` and no body. A fetch
  that fails, such as a refused connection or a timeout, answers 503.
- With `respectRobots`, a URL that robots.txt refuses answers 403 with
  `{"error": "robots.txt disallows this URL", "code": "robots_disallowed"}`.
- The fetcher refuses loopback, private and link-local addresses unless the
  configuration sets `plugins.allowPrivateURLs`, or the environment sets
  `MARKSPACE_PLUGINS_ALLOW_PRIVATE_URLS`.

AI calls:

- `/ai/complete` accepts an optional `purpose`. The route defaults to
  `plugin.<id>.<purpose>` when a purpose is given, and to `default` otherwise.
  A request may name `default` or one of the plugin's own routes, and nothing
  else. The tasks `describe` and `extract` also need `ai:vision`.
- The `custom` task keeps the first 1000 characters of `instruction`. A
  plugin puts a long prompt in `text`, which may use the whole context budget.
- Every AI call records the plugin id, so it counts against the plugin's
  budget.

Limits per plugin:

| Limit | Default |
| --- | --- |
| Requests | 20 per second, bursts of 100 |
| Web fetch | 1 request per second per host, 10 MiB per page, 20 s timeout |
| Key-value store | 50 MiB, values up to 1 MiB |
| Blob store | 2 GiB, blobs up to 256 MiB |
| AI | the plugin's monthly budget from `ai.md` |

The key-value store lives in the index database, and blobs live under
`internal/plugins/<id>/blobs/` in the data directory.

### Identifiers and Validation

`test/vectors/protocol.json` records these rules with test cases.
`sdk/vectors_test.go` and `sdk/python/test/test_vectors.py` run every case.

| Rule | Pattern | Applies to |
| --- | --- | --- |
| plugin id | `^[a-z0-9][a-z0-9-]{0,62}$` | manifest `id`, `X-Markspace-Plugin` |
| local id | `^[a-z0-9][a-z0-9._-]{0,62}$` | command, panel, action, importer and form ids, job kinds |
| qualified id | plugin id, a dot, local id | `openPanel.id`, such as `word-count.counts` |
| object id | `^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$` | job, item, space, user and delivery ids in paths |
| secret name | `^[a-z0-9][a-z0-9_-]{0,62}$` | manifest `secrets[].name`, `GET /secrets/{name}` |
| storage key | 1 to 512 bytes of UTF-8 with no control characters | kv and blob keys, percent-encoded in the path |
| event pattern | `^[a-z0-9*][a-z0-9._*-]{0,127}$` | event handler patterns and manifest `events` |

A local id may contain dots, but a plugin id may not, so the plugin id of a
qualified id ends at the first dot. Every id rule keeps a value safe as one URL
path segment with no escaping, and no id can be `.` or `..`. In an event
pattern, `*` matches any run of characters, dots included.

- `POST /install` needs an HTTP or HTTPS `markspaceUrl` with no query, and a
  signing secret in standard padded base64 of at least 32 bytes.
- A UI tree passes three checks in order, and the validator reports the first
  error. The first check measures the compact UTF-8 JSON encoding. The second
  visits every JSON object in pre-order, with keys in ascending byte order, and
  checks the depth and node limits. The third checks the node structure.
- A list item `badge` is `{label, tone}` with an optional `type` of `badge`.
- Error bodies from a plugin are `{"error", "code", "field"}`, matching the
  Markspace error body.

### SDKs

`sdk/python/markspace` uses only the Python standard library, and `sdk` is the
Go SDK. Both provide:

- an HTTP server with signature checks and the install handshake,
- handler registration for events, jobs, commands, panels, actions and
  importers,
- a host API client that retries 429, 502, 503, 504 and connection errors,
- UI tree builders and validation,
- storage of the install payload in `$PLUGIN_DATA/install.json`.

`sdk/python/readme.md` documents the Python SDK. `sdk/example` holds a small
plugin with its `containerfile` and quadlet unit, and `plugins/wiki` is a full
plugin whose quadlet unit is `deploy/wiki.container`.

### Admin Page

Admin, then Plugins, lists plugins in a table with name, version, base URL,
status (Up, Down, Paused, Needs approval), spaces with roles, queued
deliveries, AI usage this month in tokens and dollars, and the last error.

- The row actions sit in an ActionMenu behind the row's menu button: Settings,
  Secrets, Pause or Resume, Check for manifest changes, Approve when a
  manifest is pending, View jobs, the plugin's page panels, and Uninstall last
  after a divider. On a narrow viewport the menu opens full screen.
- Settings and Secrets open large Dialogs. Settings holds the space grants and
  the fields of the manifest settings schema. An empty secret field keeps the
  stored value.
- View jobs opens `/jobs?view=all&module=plugin.<id>`.
- Uninstall opens a ConfirmationDialog with a choice to keep or delete the
  plugin's data, keep by default.
- Add plugin opens a Dialog with 4 steps. Add plugin takes the base URL.
  Review permissions shows the manifest with the permission groups in plain
  words. Grant access takes spaces and roles. Approve and install takes
  secrets and settings, and installs with the reviewed SHA-256.
- When the install call fails, the page shows `MARKSPACE_URL`, the token and
  the signing secret once.

## Testing

- Registry: duplicate registrations fail with both module names.
- Event bus: ordering per handler, drop-oldest on overflow, panic isolation.
- Jobs: a model fuzz over enqueue, claim, progress, lease expiry, fail,
  retry, cancel, pause and resume, with invariants checked after each step:
  - no job runs twice at once,
  - every job ends in exactly one terminal state,
  - attempts never exceed the maximum,
  - child progress sums correctly.
- Plugin protocol: a fake plugin server in Go tests covers install, signature
  verification, replay rejection, delivery retry and expiry, the health state
  machine, timeouts, the acting token intersection and every permission group
  refusal.
- Access oracle: the differential fuzz in `internal/app/access_oracle_test.go`
  includes plugin users, grants and acting tokens.
- HTTP fuzz: the host API and the contribution routes return no 5xx.
- Declarative UI: `test/client/plugins.test.mjs` renders every node type, rejects
  unknown types, enforces the size limits, and escapes text.
- Browser: the audit in `test/browser` starts the example plugin, installs it,
  runs its command and an action, and seeds plugin jobs and suggestions for the
  page checks.
- `test/server/plugin_test.go` runs the protocol end to end with a Go
  plugin on `sdk`, and `test/server/hostapi_test.go` covers each host API
  route and group. `test/server/pluginhost_test.go` builds the stack they
  share. `test/server/jobtoken_test.go` covers job tokens, and
  `test/server/reinstall_test.go` covers a re-install into kept install
  data.
