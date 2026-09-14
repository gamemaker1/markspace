# HTTP Surface

Two servers share one `http.ServeMux` in `cmd/markspace`. The API server of
`internal/api` owns `/api/`. The web server of `internal/web` owns every other
path. The module registry mounts the routes of the built-in modules into both
servers. `internal/api` has no route table of its own. It answers any path
under `/api/` that no module claims with the JSON 404 body. The test
`test/server/routes_test.go` compares the mounted module routes with the two module
sections of this file and fails on any difference. The test finds those
sections by the heading prefixes `## JSON API` and `## Module pages`.

## JSON API, packages under `internal/modules`

Each block lists every route of the module named above it. `plugins.md` lists
the plugin host API under `/api/plugin/v1/`, which the `plugins` module
serves.

Auth and account, module `auth`:

```
POST   /api/auth/signup              {email,name,password,inviteToken?}
POST   /api/auth/login               {email,password} -> sets session cookie
POST   /api/auth/logout
GET    /api/auth/me                  -> {user, needsSetup, settings}
POST   /api/auth/password            {oldPassword,newPassword}
PATCH  /api/auth/profile             ProfileInput
GET    /api/auth/sessions
DELETE /api/auth/sessions/{id}
GET    /api/tokens
POST   /api/tokens                   {name,ttlDays?} -> {token, record}
DELETE /api/tokens/{id}
```

Users and invites, module `users`:

```
GET    /api/users?q=&limit=&offset=
POST   /api/users                    UserInput
GET    /api/users/lookup?q=&limit=
GET    /api/users/{id}
PATCH  /api/users/{id}               UserInput
DELETE /api/users/{id}
GET    /api/invites
POST   /api/invites                  InviteInput
DELETE /api/invites/{id}
```

Instance administration, module `admin`:

```
GET    /api/settings
PATCH  /api/settings                 Settings
GET    /api/stats
POST   /api/reindex
GET    /api/admin/tokens
DELETE /api/admin/tokens/{id}
```

`PATCH /api/settings` also takes `aiMonthlyTokenBudget` and
`aiMonthlyCostBudget`, the default monthly limits of every user. Only admins
see them in `GET /api/settings`.

Status and backup, module `system`:

```
GET    /api/admin/status
GET    /api/admin/backup
```

Spaces, module `spaces`:

```
GET    /api/spaces
POST   /api/spaces                   SpaceInput
GET    /api/spaces/{id}
PATCH  /api/spaces/{id}              SpaceInput
DELETE /api/spaces/{id}
GET    /api/spaces/{id}/members
POST   /api/spaces/{id}/members      {userId|email, role}
PATCH  /api/spaces/{id}/members/{userId}   {role}
DELETE /api/spaces/{id}/members/{userId}
GET    /api/spaces/{id}/export       -> application/zip
POST   /api/spaces/{id}/import       multipart file=<zip or md>
```

Notes, module `notes`:

```
POST   /api/notes                    NoteInput, or {spaceId,folderId,folderNote:true,title,content} for <folder>/index.md
GET    /api/notes/{id}
PATCH  /api/notes/{id}               NoteUpdate
DELETE /api/notes/{id}               permanent delete
POST   /api/notes/{id}/move          {parentId,position}
POST   /api/notes/{id}/duplicate
POST   /api/notes/{id}/trash
POST   /api/notes/{id}/restore
PUT    /api/notes/{id}/star
DELETE /api/notes/{id}/star
GET    /api/notes/{id}/backlinks
GET    /api/notes/{id}/versions      NoteVersion list, each with author and via
GET    /api/notes/{id}/versions/{versionId}
GET    /api/notes/{id}/versions/{versionId}/diff
POST   /api/notes/{id}/versions/{versionId}/restore
GET    /api/starred
GET    /api/recent?limit=
GET    /api/spaces/{id}/notes?parentId=&tag=&sort=&limit=&offset=
GET    /api/spaces/{id}/tree          [TreeItem], every visible item
GET    /api/spaces/{id}/tags
GET    /api/spaces/{id}/trash         trashed notes, or trashed items of every kind with ?items=1
POST   /api/spaces/{id}/trash/empty
POST   /api/render                   {spaceId?,markdown} -> Rendered
```

A folder note request creates `index.md` in the folder named by `folderId`
and answers 201. It answers 409 when the folder already has an `index.md`,
400 when `folderNote` and `folderId` do not come together, and 404 for a
folder the caller cannot see.

Folders, files and items, module `files`:

```
POST   /api/folders                  {id?,spaceId,parentId,name}
GET    /api/folders/{id}             folder with its direct children
PATCH  /api/folders/{id}             {name}
POST   /api/files                    multipart: spaceId, parentId, id?, onConflict?, pasteFor?, then file
GET    /api/files/{id}
PATCH  /api/files/{id}               {name}
PUT    /api/files/{id}/content       multipart: baseSha256, then file
GET    /api/files/{id}/versions
POST   /api/files/{id}/versions/{versionId}/restore
GET    /api/files/{id}/backlinks
GET    /api/spaces/{id}/files?type=&sort=name|size|updated&limit=&offset=
GET    /api/items/{id}               {kind, folder|note|file, access}
POST   /api/items/{id}/move          {parentId,position}
POST   /api/items/{id}/copy          {spaceId,parentId,name?}
POST   /api/items/{id}/trash
POST   /api/items/{id}/restore
DELETE /api/items/{id}
GET    /api/items/{id}/backlinks
POST   /api/resolve                  {spaceId,fromNoteId,targets} -> [{target,kind,id,path,url}]
```

`POST /api/files` and `PUT /api/files/{id}/content` stream the file part
without buffering it. The text fields must come before the part named `file`,
and the handler ignores fields after it. `onConflict` is `rename` or
`replace`. `pasteFor` names a note, and the file goes into the note's folder,
or into the space's `pasteFolder` below it. A body over the upload limit
answers 413. The API server allows 1024 MiB when no limit is configured.
`PUT /api/files/{id}/content` answers 409 when `baseSha256` does not match the
current content.

Comments, module `comments`:

```
GET    /api/notes/{id}/comments
POST   /api/notes/{id}/comments      CommentInput
GET    /api/items/{id}/comments      as for notes, on an item of any kind
POST   /api/items/{id}/comments      CommentInput
PATCH  /api/comments/{id}            {body}
DELETE /api/comments/{id}
POST   /api/comments/{id}/resolve    {resolved}
```

Shares and public links, module `sharing`:

```
GET    /api/notes/{id}/shares
POST   /api/notes/{id}/shares        ShareInput
GET    /api/items/{id}/shares        as for notes, on an item of any kind
POST   /api/items/{id}/shares        ShareInput
PATCH  /api/shares/{id}              {access,includeChildren}
DELETE /api/shares/{id}
GET    /api/shares/mine
GET    /api/notes/{id}/links
POST   /api/notes/{id}/links         PublicLinkInput
GET    /api/items/{id}/links         as for notes, on an item of any kind
POST   /api/items/{id}/links         PublicLinkInput
PATCH  /api/links/{id}               PublicLinkInput
DELETE /api/links/{id}
```

Search, module `search`:

```
GET    /api/search?q=&mode=text|semantic|hybrid&space=&tag=&limit=&offset=
GET    /api/suggest?q=&limit=
```

Sync feed and events, module `sync`:

```
GET    /api/sync?since=&limit=       SyncBatch, see offline.md
GET    /api/events                   text/event-stream of core.Event
```

AI, module `ai`:

```
GET    /api/ai/providers
POST   /api/ai/providers             AIProviderConfig
PATCH  /api/ai/providers/{id}        AIProviderConfig
DELETE /api/ai/providers/{id}
POST   /api/ai/providers/{id}/test
GET    /api/ai/providers/{id}/models catalog rows of one provider
POST   /api/ai/providers/{id}/refresh  list the models again, admin or owner
GET    /api/ai/models?capability=&provider=&q=   catalog rows with effective prices
PATCH  /api/ai/models/{providerId}/{model...}   {enabled?,priceOverride?}, admin
GET    /api/ai/routes                AIRoutes: instance routes, and the caller's own when allowed
PUT    /api/ai/routes/instance/{task}  {targets}, admin
PUT    /api/ai/routes/me/{task}      {targets}, when aiAllowUserRoutes is on
DELETE /api/ai/routes/me/{task}
GET    /api/ai/presets               provider presets
POST   /api/ai/run                   AIRequest -> AIResult
POST   /api/ai/stream                AIRequest -> text/event-stream
POST   /api/ai/reindex               {spaceId?}
GET    /api/ai/threads?noteId=       AIThread list of the caller, newest first, with messages
POST   /api/ai/threads               AIThreadInput {noteId, title?} -> AIThread
POST   /api/ai/threads/{id}/messages AIThreadMessageInput -> text/event-stream
PUT    /api/ai/threads/{id}/messages/{messageId}/applied   {applied}
DELETE /api/ai/threads/{id}
POST   /api/ai/image/stream          AIImageGenerateRequest -> text/event-stream of progress, done
GET    /api/ai/usage?from=&to=&groupBy=&provider=&model=&feature=&plugin=&limit=
                                     AIUsageReport of the caller
GET    /api/ai/usage/summary         AIUsageSummary: today, month, budget
GET    /api/ai/usage/calls?sort=recent|slowest|failed&limit=   own calls
GET    /api/admin/ai/usage?from=&to=&groupBy=&user=&provider=&model=&feature=&plugin=&limit=
                                     AIUsageReport across users
GET    /api/admin/ai/usage/calls?sort=recent|slowest|failed&user=&limit=
GET    /api/admin/ai/budgets         AIBudget list with month-to-date status
PUT    /api/admin/ai/budgets/{kind}/{id}        {monthlyTokens?,monthlyCost?}, kind user|plugin
DELETE /api/admin/ai/budgets/{kind}/{id}
POST   /api/admin/ai/budgets/{kind}/{id}/lift   lift until the next UTC month
POST   /api/admin/ai/prices/refresh  download the public price catalogs now
```

`ai.md` describes the usage and budget shapes. `models.md` describes the
catalog, routes and prices. A request over its monthly budget answers 429. A
model id may contain slashes, as in `PATCH /api/ai/models/openrouter/openai/gpt-4o`.

Suggestions, module `suggestions`:

```
GET    /api/notes/{id}/suggestions?status=
POST   /api/notes/{id}/suggestions   {title, rationale, edits: [{anchor, text}]}
GET    /api/suggestions?space=&author=&status=&limit=   the actor's inbox across notes
GET    /api/suggestions/{id}
POST   /api/suggestions/{id}/accept  {editIds?} -> {suggestion, note}
POST   /api/suggestions/{id}/discard {editIds?}
DELETE /api/suggestions/{id}         withdraw, author only
```

Jobs, module `jobs`:

```
GET    /api/jobs?status=&kind=&module=&space=&limit=&mine=   {items}, the actor's jobs, every job for an admin
GET    /api/jobs/{id}                {job, log}
POST   /api/jobs/{id}/cancel         a canceled job also cancels its unfinished children
POST   /api/jobs/{id}/pause
POST   /api/jobs/{id}/resume
```

Contributions, module `ui`:

```
GET    /api/ui/contributions?itemId=&spaceId=&contexts=   {commands, panels, importers, aiTools, settings?}
POST   /api/commands/{id}            {context} -> a command response
POST   /api/panels/{id}              {context} -> {ui}
POST   /api/modules/{module}/actions/{id}   {context, values, payload} -> a command response
POST   /api/import/{importer}        {fileId, spaceId} -> {job} or a command response
```

Plugin administration, admin only, module `plugins`:

```
GET    /api/admin/plugins            {items}
GET    /api/admin/plugins/{id}
POST   /api/admin/plugins/preview    {baseUrl} -> {baseUrl, manifest, sha256, installed}
POST   /api/admin/plugins            {baseUrl, sha256, grants, secrets, settings} -> 201 {plugin, delivered, installError?, token?, signingSecret?, markspaceUrl?}
PATCH  /api/admin/plugins/{id}       {grants?, settings?, secrets?}
POST   /api/admin/plugins/{id}/pause
POST   /api/admin/plugins/{id}/resume
POST   /api/admin/plugins/{id}/check
POST   /api/admin/plugins/{id}/approve
POST   /api/admin/plugins/{id}/uninstall   {deleteData} -> 204
```

A plugin interaction that fails, times out or returns an invalid response
answers 503. A UI tree that links to an item the actor cannot view also
answers 503.

The error body is `{"error":"message","field":"name"}`. The status codes are
`ErrInvalid` 400, `ErrUnauthorized` 401, `ErrForbidden` 403, `ErrNotFound` 404,
`ErrConflict` and `ErrExists` 409, `ErrTooLarge` 413, `ErrUnsupported` 415,
`ErrRateLimited` and `ErrQuotaExceeded` 429, `ErrUnavailable` 503, anything else 500. An error that
wraps both `ErrUnavailable` and `ErrUnsupported` maps to 503.

## Module pages, packages under `internal/modules`

The registry mounts these page routes into the web server. Each page renders
inside the application layout through `api.PageRenderer`.

Jobs page, module `jobs`:

```
GET    /jobs                         ?view=active|finished|all&module=&kind=
POST   /jobs                         pause, resume and cancel
GET    /modules/jobs/jobs.js         script of the jobs page
```

Plugins page, admin only, module `plugins`:

```
GET    /admin/plugins                plugin table and Add plugin
POST   /admin/plugins                every action of the page
GET    /modules/plugins/admin.js     script of the plugins page
```

Plugin pages, signed-in people, module `plugins`:

```
GET    /plugins/page                 ?panel=&itemId=&spaceId= a plugin page panel in the module page layout
```

## Web Pages, Package `internal/web`

```
GET  /                               redirect to the first space, or /setup
GET  /setup                          first-run admin form
POST /setup
GET  /login                          POST /login
POST /logout
GET  /signup?invite=                 POST /signup
GET  /s/{space}                      space home
GET  /s/{space}/t/{tag}              notes with one tag
GET  /s/{space}/trash
GET  /s/{space}/files                files in the space, client-rendered shell
GET  /s/{space}/f/{path...}          file view, client-rendered shell
GET  /s/{space}/d/{path...}          folder view, client-rendered shell
GET  /s/{space}/settings             POST for name, members, import
GET  /s/{space}/n/{path...}          note view, path has no .md suffix
GET  /s/{space}/n/{path...}?edit=1   editor
GET  /n/{noteId}                     permalink, redirects to the canonical path
GET  /i/{id}                         permalink for a folder, note or file
GET  /spaces                         space list, create form
GET  /search?q=
GET  /starred
GET  /recent
GET  /shared                         notes shared with me
GET  /settings/profile               POST
GET  /settings/tokens                POST
GET  /settings/ai                    own routes and personal providers, POST
GET  /settings/usage                 own AI usage: summary, 30-day chart, models, recent calls
GET  /admin                          stats
GET  /admin/users                    POST for create, invite, deactivate
GET  /admin/ai                       tasks, models and providers, POST for their forms
GET  /admin/usage                    ?range=&from=&to=&user=&provider=&model=&plugin=&stack=user|plugin
                                     POST budget defaults and overrides: save, lift, remove in two steps
GET  /admin/settings                 POST
GET  /p/{token}                      public view of a note, file or folder
POST /p/{token}                      password form for a protected link
GET  /p/{token}/i/{id}               an item inside a public folder link
GET  /p/{token}/raw/{id}/{name}      file content through a public link
GET  /p/{token}/thumb/{id}?w=        thumbnail through a public link
GET  /files/{space}/{id}/{name}      file content with Range; ?v={versionId}; ?download=1
GET  /thumbs/{space}/{id}?w=         thumbnail, w is 128, 256, 512 or 1024
GET  /static/{path...}               embedded CSS and JavaScript
GET  /healthz
```
