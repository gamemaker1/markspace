# Offline-First Client

The browser keeps a full copy of every note the user may view in IndexedDB,
which is a database built into the browser. The interface reads and writes that
copy first. A sync engine copies changes to and from the server whenever a
connection exists. A service worker, which is a script the browser runs
between the page and the network, serves the application shell and static
files without a connection. The result installs on a phone as a progressive
web app (PWA) and works with no network.

## Routes

The client-rendered shell owns the note experience. The service worker answers
navigations to these paths with the cached `/app-shell` document, online or
offline, and the shell routes on the client:

```
/                          home: recent and starred notes
/s/{space}                 space home
/s/{space}/n/{path...}     note view, path without .md; ?edit=1 opens the editor
/s/{space}/t/{tag}         notes with a tag
/s/{space}/trash           trash
/s/{space}/files           every file in the space, with type filter and sort
/s/{space}/d/{path...}     folder view, or the folder note of the folder
/s/{space}/f/{path...}     file view
/i/{id}                    permalink of a folder, note or file
/n/{noteId}                permalink; ?edit=1 opens the editor
/search                    search
/starred  /recent  /shared lists
```

Every other page stays server-rendered and needs a connection: `/login`,
`/signup`, `/setup`, `/logout`, `/spaces`, `/s/{space}/settings`,
`/settings/*`, `/admin*`, `/p/{token}`. Offline, the service worker answers a
navigation to one of them with the cached `/offline` page.

Before the service worker is installed, the server renders the shell routes
itself. A browser with JavaScript disabled keeps using those server pages.

## Server Contract

### Pull

```
GET /api/sync?since=<rev>&limit=<n>
```

`since=0` returns a full snapshot. `limit` defaults to 200 and caps at 1000.
The response is `core.SyncBatch`:

```json
{
  "rev": 1834,
  "more": false,
  "serverTime": "2026-09-13T09:00:00Z",
  "user": {"id": "...", "name": "...", "email": "...", "isAdmin": true},
  "spaces": [{"id": "...", "slug": "docs", "name": "Docs", "myRole": "owner"}],
  "folders": [{"id": "...", "spaceId": "...", "parentId": "", "name": "Projects", "path": "Projects", "rev": 1830}],
  "files": [{"id": "...", "spaceId": "...", "parentId": "...", "name": "survey.pdf", "path": "Projects/survey.pdf",
             "mime": "application/pdf", "size": 29343, "sha256": "...", "url": "/files/docs/.../survey.pdf", "rev": 1831}],
  "notes": [{"id": "...", "spaceId": "...", "spaceSlug": "docs", "parentId": "",
             "path": "a/b.md", "slug": "b", "title": "B", "icon": "", "tags": [],
             "content": "...", "position": 100, "createdAt": "...", "updatedAt": "...",
             "createdBy": "...", "updatedBy": "...", "deleted": false, "rev": 1834,
             "access": "edit"}],
  "liveIds": ["..."],
  "starredIds": ["..."],
  "accessKey": "..."
}
```

- Folders, notes and files arrive in revision order from one counter.
  Folder and file records carry no access. The client takes the access of
  the space role, or view for an item that reaches the user through a share.
- A pull that moves or renames a folder moves the local paths of the items
  below it that the page did not carry.
- The client stores `rev` and passes it as `since` next time. While `more`
  is true it pulls again at once.
- `liveIds` and `starredIds` are arrays on the last page and `null` before it.
  On the last page the client deletes every local folder, note and file whose
  id is missing from `liveIds`, and sets the starred flag from `starredIds`.
- `accessKey` changes when the user's admin flag, space roles or note shares
  change. When it differs from the stored value, the client resets its cursor
  to 0 and pulls a full snapshot, because a newly shared note can carry a
  revision older than the cursor.
- A trashed note has `deleted: true` and empty `content`.
- `access` is `view`, `comment`, `edit` or `manage`. The interface hides edit
  controls below `edit`.
- A 401 means the session ended. The client keeps its data and its outbox and
  asks the user to sign in.

### Push

There is no batch push endpoint. The outbox replays the ordinary REST calls
from `routes.md`, one at a time, in the order the user made the changes.

| Outbox op | Request |
| --- | --- |
| `create` | `POST /api/notes` with `{id, spaceId, parentId, title, content, tags, icon}` |
| `update` | `PATCH /api/notes/{id}` with changed fields plus `baseUpdatedAt` |
| `move` | `POST /api/notes/{id}/move` with `{parentId, position}` |
| `trash` | `POST /api/notes/{id}/trash` |
| `restore` | `POST /api/notes/{id}/restore` |
| `delete` | `DELETE /api/notes/{id}` |
| `star` | `PUT` or `DELETE /api/notes/{id}/star` |

A client creates notes with ids it generates, in the format of `core.NewID`:
26 characters from the alphabet `0123456789abcdefghjkmnpqrstvwxyz`. The first
48 bits are the millisecond timestamp and the remaining 80 are random. The
server accepts a supplied id when it has that shape. If a note with that id
already exists and the same user created it in the same space, the server
returns the existing note, so replaying a create after a lost response is
safe.

The server allocates the file path. A note created offline carries a
provisional path computed on the client, and the next pull replaces it.

### Response Handling in the Outbox

| Response | Action |
| --- | --- |
| 2xx | Store the returned note locally, remove the op |
| 409 on `update` | `GET /api/notes/{id}`. If the server title and content equal the op's, the op already applied, so remove it. Otherwise make a conflict copy, described below |
| 401 | Stop replaying, keep every op, emit `auth-required` |
| 403, 400 | Remove the op, restore the server copy of the note locally, emit `rejected` |
| 404 on `update`, `move`, `trash` | The note is gone or access was lost. Keep the local text as a conflict copy at the space root. If that create also fails, save the text in the `recovered` store and emit `recovered` |
| 5xx, network failure | Keep the op, retry with backoff from 2 s doubling to 60 s |

A conflict copy is a new note created with a fresh client id, in the same space
and under the same parent, titled `<title> (conflicted copy YYYY-MM-DD HH:MM)`,
with the local text. The original note takes the server version. The engine
emits `conflict` with both ids. No edit is ever discarded.

Ops coalesce before they are sent. Consecutive updates to one note merge into
one update that keeps the first `baseUpdatedAt`. An update to a note whose
create is still queued folds into that create. A create followed by a delete
of the same note, both unsent, removes both.

A pull never overwrites a note that has queued ops. The engine stores the
server copy beside it and uses it for the conflict check.

### Folder and File Ops

| Outbox op | Request |
| --- | --- |
| `folder.create` | `POST /api/folders` with `{id, spaceId, parentId, name}` |
| `folder.rename` | `PATCH /api/folders/{id}` with `{name}` |
| `file.upload` | `POST /api/files`, multipart `spaceId`, `parentId` or `pasteFor`, `id`, `onConflict: rename`, then `file` from `blobs` |
| `file.rename` | `PATCH /api/files/{id}` with `{name}` |
| `file.replace` | `PUT /api/files/{id}/content`, multipart `baseSha256`, then `file` |
| `item.move` | `POST /api/items/{id}/move` with `{parentId, position}` |
| `item.copy` | `POST /api/items/{sourceId}/copy` with `{spaceId, parentId, name?}` |
| `item.trash` | `POST /api/items/{id}/trash` |
| `item.restore` | `POST /api/items/{id}/restore` |
| `item.delete` | `DELETE /api/items/{id}` |

An op for a folder or file carries `itemId` and `kind`. Notes keep the note
ops above, so a move, trash, restore or delete of a note uses the note routes.
The text fields of a multipart body go before the part named `file`.

Every mutation changes the local records first. A rename or move of a folder
moves the paths of every folder, note and file below it. A trash or restore
covers the subtree. An upload stores the blob in `blobs` under the new client
id of the file, and the local record carries `pendingKey` until the upload
finishes, so views show the local blob. A copy shows provisional records with
provisional ids for the copy and everything below it. Those records cannot
change until the copy syncs, except the root, whose rename, move, trash or
delete folds into the queued copy.

Item ops coalesce before they are sent. A rename folds into a queued
`folder.create` or `file.upload`, and consecutive renames merge. A move folds
into a queued create, upload or copy, and consecutive moves merge. A
`file.replace` of a queued upload swaps the blob of the upload. Consecutive
replaces merge into one that keeps the first `baseSha256`. A trash and a
restore of the same item, both unsent, remove each other. A delete removes the
unsent ops of the item and of everything below it, and a delete of an item
whose create was never sent removes both.

| Response | Action |
| --- | --- |
| 2xx | Store the returned folder or file, move local paths below a folder whose path changed, remove the op, and pull again after the outbox drains |
| 409 on `folder.create` | Take the next free name `<name> (N)` and send the create again |
| 409 on `file.replace` | Keep the local blob as a new file `<stem> (conflicted copy YYYY-MM-DD HH:MM).<ext>` in the same folder, queued as an upload. The original takes the server version. The engine emits `conflict` with `kind: "file"` |
| 404 on `item.delete` | The item is gone, remove the op |
| Other 4xx on rename, move, trash or restore | Remove the op, restore the server copy from `GET /api/items/{id}` and emit `rejected`. An item that is gone is removed locally |
| Other 4xx on `folder.create` or `item.copy` | Remove the op and the provisional records, and emit `rejected` |
| Other 4xx on `file.upload` or `file.replace` | Remove the op, keep the blob, mark the upload failed and the record `uploadFailed`. `retryUpload` queues it again |
| 401, 5xx, network failure | As for notes |

The server rewrites links in notes when an item moves or is renamed. Those
notes arrive as note updates on the next pull.

## Browser Modules

Every file is a native ES module served from `/static/js/`. There is no build
step at runtime. The one third-party dependency is CodeMirror 6, vendored as a
single committed bundle at `/static/vendor/codemirror.js`. Its pinned
source, lockfile and `build.sh` live in `internal/web/vendor/codemirror/`,
and rebuilding it needs bun.

### `ids.js`

```js
export function newId(): string           // core.NewID format
export function isId(s: string): boolean
```

### `api.js`

```js
export class ApiError extends Error { status: number; field?: string }
export async function api(method, path, body?, {signal}?)   // JSON in, JSON out, throws ApiError
export async function upload(path, file, {onProgress, fields, method = "POST", filename}?)   // text fields, then multipart "file"
```

A network failure throws `ApiError` with `status` 0.

### `session.js`

```js
export async function getSession(): Promise<{user: User|null, offline: boolean}>
export async function signOut(): Promise<void>
```

`getSession` calls `GET /api/auth/me` and caches the user in `localStorage`
under `markspace.user`. Offline it returns the cached user with
`offline: true`. `signOut` deletes that user's IndexedDB database, clears the
cache, then posts the logout form. The caller confirms first when the outbox
is not empty.

### `store.js`

```js
export async function openStore(userId: string): Promise<Store>
export function createMemoryStore(): Store      // same interface, for tests

interface Store {
  getMeta(key), setMeta(key, value)
  putSpaces(spaces), listSpaces(), getSpace(idOrSlug)
  putNotes(notes), deleteNotes(ids), getNote(id), getNoteByPath(spaceSlug, path)   // path with or without ".md"
  putFolders(folders), deleteFolders(ids), getFolder(id)
  putFiles(files), deleteFiles(ids), getFile(id)
  deleteItems(ids)                                       // any kind
  listNotes(spaceId, {includeDeleted = false} = {})      // content included
  listAllNotes({includeDeleted = false} = {})
  listFolders(spaceId, opts), listAllFolders(opts), listFiles(spaceId, opts), listAllFiles(opts)
  listItems(spaceId, {includeDeleted = false} = {})      // {folders, notes, files}
  recentNotes(limit), starredNotes(), sharedNotes(userId)
  getItem(id)                                            // {kind, folder|note|file} or null
  getItemByPath(spaceIdOrSlug, path)                     // a note path may omit ".md"
  tree(spaceId)                                          // [{kind, folder|note|file, page?, children}]
  searchNotes(query, {spaceId, limit = 50} = {})         // [{note, snippet, score}]
  searchFiles(query, {spaceId, limit = 50} = {})         // [{file, score}]
  snapshot(spaceId)                                      // links.js Snapshot of the live items
  backlinks(itemId)                                      // [{note, context}] for any kind
  resolveLink(spaceId, fromNoteId, target, {form, decoded})   // {kind, id, path, heading, item} or null
  enqueue(op), peekOps(), removeOp(opId), replaceOps(ops), countOps()
  putBlob(key, blob), getBlob(key), deleteBlob(key)      // getBlob resolves to a Blob or null
  putRecovered(item), listRecovered(), deleteRecovered(id)
  wipe()
}
```

The database is named `markspace-<userId>`, schema version 4, with object
stores `meta`, `spaces`, `notes` (indexes on `spaceId`, `updatedAt`, `path`),
`folders` and `files` (indexes on `spaceId`, `parentId`, `path`), `outbox`
(auto-increment key), `blobs`, `recovered` and `comments`. Version 4 drops the
`attachments` store.

`tree` sorts siblings by position, then folders, notes and files, then name
without regard to case, as the server does. A folder node carries `page`, its
folder note, which is not listed as a child. `resolveLink` resolves with
links.js from the folder of the note, so a relative markdown path works as on
the server. `form` is a links.js form name and defaults to a wikilink.
`decoded` skips percent-decoding of a markdown target that links.js already
decoded. The two-argument form `resolveLink(spaceId, target)` resolves from the
space root and returns a note or null, for older callers. The store keeps one
snapshot per space and updates its entries on every write, so a query rebuilds
the Snapshot only after a path or title changed. `searchNotes` tokenizes title,
tags and content, ranks title matches above body matches, and returns an HTML
snippet with every piece of note text escaped and `<mark>` around matches.

### `sync.js`

```js
export function createSync({store, api, upload, now = Date.now, online = () => navigator.onLine}): Sync

interface Sync {
  start(), stop(), syncNow(): Promise<void>
  status(): {online, syncing, pending, lastSyncAt, lastError, authRequired, conflicts}
  on(event, fn): () => void          // returns an unsubscribe function

  createNote({spaceId, parentId, title, content, tags, icon}): Promise<Note>
  updateNote(id, {title?, content?, tags?, icon?}): Promise<Note>
  moveNote(id, parentId, position): Promise<Note>
  trashNote(id), restoreNote(id), deleteNote(id): Promise<void>
  setStar(id, starred): Promise<void>
  uploadAttachment(noteId, file): Promise<{url: string, pending: boolean, fileId: string, name: string}>

  createFolder({spaceId, parentId, name}): Promise<Folder>
  uploadFiles(spaceId, parentId, files): Promise<File[]>
  renameItem(id, name): Promise<record>      // a note name is its title
  moveItem(id, parentId, position?): Promise<record>
  copyItem(id, {spaceId, parentId, name?}): Promise<record>
  trashItem(id), restoreItem(id), deleteItem(id): Promise<void>
  replaceFile(id, file): Promise<File>

  uploads(): Upload[]                // queued, running, failed and the last 50 finished
  retryUpload(key): Promise<void>
  cancelUpload(key): Promise<void>   // removes a queued or failed upload and its placeholder
}

interface Upload {
  key: string, fileId: string, kind: "upload" | "replace",
  noteId: string, spaceId: string, filename: string, size: number, mime: string,
  loaded: number,                    // bytes sent so far
  state: "queued" | "uploading" | "failed" | "done",
  error?: string, url?: string, createdAt: number
}
```

Every mutation writes to the store first and resolves before any network
call. The engine pulls on `start`, on the `online` event, when the page
becomes visible, and every 60 s while visible, and replays the outbox after
each local mutation, debounced by 1 s.

Events:

| Event | Payload |
| --- | --- |
| `status` | the `status()` object |
| `notes-changed` | `{ids: string[], source: "local" \| "remote"}`, ids of folders, notes and files |
| `spaces-changed` | `{}` |
| `conflict` | `{noteId, copyId, kind}`, kind `note` or `file` |
| `copied` | `{sourceId, copyId, provisionalId, kind}` after a copy reached the server |
| `file-stored` | `{fileId, replaced}` after an upload or replacement finished |
| `rejected` | `{op, status, message}` |
| `recovered` | `{id, title}` |
| `auth-required` | `{}` |
| `uploads-changed` | `{uploads: Upload[]}`, at most every 100 ms while bytes move |

`uploadAttachment` puts the file in the folder of the note, or in the space
`pasteFolder` below it. A taken name gets `<stem> (N).<ext>`. It returns at
once, before any network call, with the path from the folder of the note, percent-encoded with links.js `encodeDest`, with spaces and unbalanced
parentheses also encoded, such as `attachments/east%20wall.png`. The editor
inserts that path. The local file record resolves the link at once, and views
show the blob until the upload finishes. When the paste folder is not on the
device, or the note has not reached the server, the upload sends `pasteFor`
and the server picks and creates the folder. When the server stores the file
under another name, the engine points the links of the note at the new path.
A cancelled upload removes the record and turns its links in the note into
the file name.

### `markdown.js`

```js
export function render(md, {resolveLink, resolveItem, resolveImage} = {}): {html, toc, links, tasks, tasksDone}
// toc: [{level, text, id}], where id matches the heading's id attribute in html
export function plainText(md), excerpt(md, maxChars = 240), extractLinks(md), extractTags(md), slugify(title)
```

The renderer covers headings with ids, paragraphs, emphasis, strong,
strikethrough, inline code, fenced code with a `language-x` class, block
quotes, ordered, unordered and task lists, tables, horizontal rules, links,
images, autolinks, `[[target]]` and `[[target|label]]` wikilinks, `#tags` and
footnotes. It builds the output from escaped text and a fixed set of tags, so
no input HTML reaches the page. It passes raw HTML in the source through as
escaped text. Link and image URLs must be relative, `http:`, `https:`,
`mailto:` or `markspace-pending:`. Any other URL becomes `#`. External links
get `rel="noopener noreferrer nofollow"` and `target="_blank"`.
`resolveLink(target)` returns `{href, exists}`, and a missing target renders
with class `wikilink wikilink-new`. `resolveItem(target, {form, embed})`
replaces `resolveLink` and resolves folders, notes and files. It returns
`{kind, href, fileUrl, mime, name}` or null. With it, an embedded image file
renders as `img.wikilink-embed` with the file URL, another embedded file as
`a.wikilink.file-card` holding `span.file-card-name`, an embedded note as
`a.wikilink.note-card`, and a relative markdown link or image that resolves
takes the item URL or the file URL. A relative link that does not resolve
keeps its destination. The views build a synchronous `resolveItem` over the
store snapshot of the space before they render. `resolveImage(url)` maps a pending upload
to an object URL. `slugify` matches `core.Slugify`. The class names match the
server renderer, so the stylesheet serves both.

### `boot.js`

Every page loads `boot.js` as a module. It registers `/sw.js` with scope `/`, shows
a "new version available, reload" prompt when an updated worker is waiting,
stores the `beforeinstallprompt` event, and exposes
`window.markspace.install()` plus a `markspace:installable` event on
`document`. When an updated worker is waiting it dispatches
`markspace:update-available` on `document` with `detail.reload()`, which
activates the new worker and reloads the page.

### `sw.js`

The server serves the worker at `/sw.js` with `Cache-Control: no-cache`. The Go handler replaces
`__MARKSPACE_BUILD__` with a hash of the embedded static files and
`__MARKSPACE_ASSETS__` with a JSON array of the `/static/` URLs to precache,
so each deploy gets a new cache.

| Request | Strategy |
| --- | --- |
| install | precache `/app-shell`, `/offline`, `/manifest.webmanifest` and every asset except the pdf.js bundle and the vendored Octicon SVG files |
| navigation to a shell route | cached `/app-shell` |
| other navigation | network, falling back to cached `/offline` |
| `GET /static/vendor/pdf*` | cache first in `markspace-pdfjs-<build>`, stored on first use |
| `GET /static/*` | cache first |
| `GET /files/{space}/{id}/{name}` | the user's file cache first, then network. Nothing is stored by the worker |
| `GET /thumbs/*` | cache first in `markspace-thumbs-v1`, keyed by the full URL, at most 500 entries |
| `/api/*` | network only. The sync engine handles offline |

On activate it deletes static and pdf.js caches from earlier builds and the
old `markspace-files-v1` cache, and claims open clients.

The page posts `{type: "filecache-user", userId}` to the worker. The worker
keeps the id in memory and in the `markspace-sw-meta` cache, so a restarted
worker still has it. Without a known user, file requests go to the network.

A file request with `?v=` goes to the network. Any other file request looks
up the file id in `markspace-files-<userId>`. A hit without a Range header
answers 200 with `Accept-Ranges: bytes`. A Range header answers 206 with a
slice of the cached blob, or 416 with `Content-Range: bytes */size` when the
range starts past the end. `?download=1` prefers the network and, offline,
answers from the cache with `Content-Disposition: attachment`. After a hit the
worker posts `{type: "file-accessed", fileId}` to the requesting page. A miss
goes to the network, and an offline miss is a network error. `parseRange` and
`rangeResponse` are pure functions inside the worker, and a node test runs the
worker in `node:vm`.

### `filecache.js`

```js
export const DEFAULT_BUDGET, BUDGET_KEY, OPEN_CACHE_MAX
export function cacheName(userId), cacheKey(fileId), fileIdFromUrl(pathname)
export function parseRange(header, size), planEviction(entries, budget), wantedFiles({files, folders, pins})
export function createFileCache({userId, store, ...}): FileCache
export async function deleteFileCache(userId)

interface FileCache {
  ready
  budget(), setBudget(bytes)
  status(fileId): {cached, pinned, via}
  isAvailable(file), open(file), getBlob(file), touch(fileId)
  pin({kind, id}), unpin({kind, id}), isPinned({kind, id}): {pinned, via}
  reconcile(), usage(): {used, count, pinnedBytes, pinnedCount, budget}
  clear(), on("changed", fn), destroy()
}
```

File bytes live in Cache Storage under `markspace-files-<userId>`, keyed by
`/__markspace/files/<id>`, with `Content-Type`, `Content-Length` and
`X-Markspace-Sha256` headers. An index in its own IndexedDB database of the
same name holds `entries` ({fileId, sha256, size, lastAccess, pinned, url,
mime}) and `pins` ({id, kind}).

The budget defaults to 500 MiB and is stored in localStorage under
`markspace.files.budget`. The Offline files page, `/settings/offline`, sets
it, shows usage, and clears the cache after a ConfirmationDialog. Past the budget, unpinned files evict least
recently used first.

`open` runs when a file view opens or a note embeds an image. It updates
`lastAccess`, drops an entry whose hash changed, and fetches the file into
the cache when online and when the file is pinned or at most 50 MiB.

A pinned file is wanted. So is every live file under a pinned folder at any
depth, including files added later. `reconcile` runs online. It downloads
wanted files that are missing or whose hash changed, drops entries of files
that left the store, unpins entries no longer wanted and evicts over the
budget. Two calls never overlap. When a wanted file is a PDF it also fetches
the pdf.js bundle, so the worker's runtime cache holds it for offline use.
The cache listens for `file-accessed` messages and calls `touch`. The shell
creates the cache at start, calls `reconcile` 2 s after a remote
change, and deletes the cache at sign out.

### `viewers/*.js`

The file view loads one viewer module lazily with
`import("/static/js/viewers/<kind>.js")`. The kind comes from
`viewerKind(file)` in `viewers/kind.js`: image, pdf, video, audio, text, csv
or other. Video and audio load `viewers/media.js`. Every module exports
`mountViewer(container, {file, url, downloadUrl, isNarrow, signal})` and
resolves to `{unmount()}`. The container is a full-bleed flex column, and the
viewer draws its own toolbar inside it. Viewers read content from `url`, so
the service worker serves cached files offline and answers Range requests for
media. The PDF viewer loads `/static/vendor/pdfjs.js` and its module worker
`/static/vendor/pdfworker.js` on first use. It passes no cmap, font or
wasm URLs, so it makes no network requests beyond the file itself. `build.sh`
in `internal/web/vendor/pdfjs/` builds the bundle with bun. A viewer
that cannot load its content shows an error with a Download link, and
`unmount` aborts fetches and frees canvases and the PDF document.

### `filetree.js`, `fileviews.js` and `filepanels.js`

`filetree.js` draws the sidebar tree of folders, notes and files for
`shell.js`. `createFileTree({app, host, space, labelledBy, onExpandedChange,
onSelectingChange})` returns a controller with `render(nodes, {expanded,
currentId})`, `rename(id)`, `revealItem(id)`, `focus(id)`, `expand(id)`,
`openMenuFor(id)`, `setSelecting(on)`, `selection()`, `setCurrent(id)` and
`destroy()`, and the `editing` and `selecting` getters.
The tree is an ARIA tree with one tab stop. Arrow keys move and expand, Shift
extends the selection, F2 renames in place, Delete trashes with Undo, Ctrl+C,
Ctrl+X and Ctrl+V copy, cut and paste into the folder of the focused row, and
Shift+F10 opens the item menu. Shift and Ctrl clicks select on desktop, and a
Select button in the tree header turns taps into selection on phones. Rows
drag onto folders to move with Undo, and desktop files dropped on a folder
upload into it. `itemMenuItems(app, {space, item, targets, where})` lists the
files.md item menu, which the folder and file views use too. Move to and Copy
to call `app.openMoveDialog(mode, targets)`, which opens
`openMoveDialog(app, {mode, targets, space, nodes})`. The Dialog is large,
titled "Move 3 items" or "Copy 3 items", and holds a TreeView of the folders
of the space in single select, with the space as the root row. The folder of
the items starts selected. A move cannot target a moved folder or any folder
inside it, so those rows are absent. The line under the tree names the
destination. The footer holds Cancel and "Move here" or "Copy here". A move
closes the Dialog, then moves with `moveItemsTo` and shows Undo in a Banner.
A copy runs `copyItemsTo` before the Dialog closes. The same Dialog serves
phones and desktops.

`fileviews.js` renders the folder view, the file view, the `/i/{id}` redirect
and the Files page, and exports `buildItemResolver(app, note)` for rendered
notes. A folder with a folder note shows the note under a slim folder header
whose item count expands the listing in place. Any other folder lists its
children as rows of name, kind, size and update time, with a grid toggle that
shows thumbnails. The file view is full-bleed under a header with the name,
Download and a menu. Offline, a file that is not in the file cache shows
"Available when online" and opens the Info tab.

`filepanels.js` holds the rail panels of folders and files: Info, Comments,
History for files, Share and Backlinks. They call the item routes and use the
shared pieces of `panels.js`. It also holds `openNoteFiles`, the Files tab of
a note, which lists the files the note links to and uploads through
`uploadAttachment`.

### Manifest

`/manifest.webmanifest` is generated with the instance name. It sets
`start_url: "/"`, `scope: "/"`, `display: "standalone"`, theme and background
colours, and icons at 192 px and 512 px, a maskable 512 px icon, and the SVG.
`/static/icons/apple-touch-icon.png` is 180 px.

### `ui.js`

`ui.js` is the component kit for the shell, the editor, the panels and the
enhanced server pages. It builds GitHub Primer components as `primer.md` specifies
them. The styles live in `internal/web/static/css/components.css`,
`forms.css`, `pages.css` and `markdown.css`, and use Primer functional tokens. Nothing at
the top level of the module touches the DOM, so node tests import it.

Narrow means Primer's narrow viewport range, below 768 px. `NARROW_QUERY`
holds the media query and `isNarrow()` tests it. Motion runs only under
`prefers-reduced-motion: no-preference`.

#### Elements and Shared Helpers

```js
export function h(tag, props, ...children): HTMLElement
export function icon(name, size = 16): SVGElement   // Octicons, primer.md 4
export const ICON_NAMES: string[]
export function uid(prefix): string
export function focusables(container): HTMLElement[]
export function trapFocus(event, container): boolean
export function prefersReducedMotion(): boolean
export function isNarrow(): boolean
export const NARROW_QUERY: string
export function formatBytes(n): string
export function fileIconName(mime, name): string
export function acceptsFile(file, accept): boolean
export function placeOverlay(panel, rect, placement = "bottom-start")
```

#### Buttons and Tooltips

```js
export function button({label, variant, size, leadingVisual, trailingVisual, trailingAction, count, keybindingHint, block, inactive, href, type, onClick, className, attrs}): HTMLButtonElement | HTMLAnchorElement
export function iconButton({icon, label, description, keybindingHint, variant = "invisible", size, iconSize, pressed, tooltip = true, onClick, className, attrs}): HTMLButtonElement
export function keybindingHint(keys): HTMLElement
export function tooltip(trigger, text, {type = "label", keybindingHint, direction = "s"}): {element, show(), hide(), setText(text), destroy()}
export function setBusy(element, busy, {message}): void
export function spinner({size = "small", label = "Loading", delay = 0, className}): HTMLElement
export function copyFeedback(button): void
```

`variant` is `default`, `primary`, `danger`, `invisible` or `link`. `size`
is `small`, `medium` or `large`. The classes are `btn`, `btn-primary`,
`btn-danger`, `btn-invisible`, `btn-link`, `btn-sm`, `btn-lg`, `btn-block` and
`btn-icon`. The older `btn-quiet`, `small`, `danger` and `icon-btn` classes
render the same components.

`iconButton` names the button through its Tooltip with `aria-labelledby`.
With `description` or `keybindingHint`, `aria-label` names the button and the
Tooltip describes it through `aria-describedby`.

A Tooltip opens 50 ms after the pointer enters the trigger, or at once on
keyboard focus. It closes when the pointer leaves, on blur and on Escape.
Escape leaves focus on the trigger. Touch input never opens it. The tooltip
element is a `popover="manual"` element with `aria-hidden="true"`.

`setBusy` keeps the label, the width and the focus of a button. It sets
`aria-disabled` and never `disabled`. A small Spinner replaces the leading
visual, or the trailing visual, or sits over the hidden label. `message` goes
to the status region. Clicks do nothing while the button is busy.

`copyFeedback` swaps the button icon for a check for 2 s and announces
"Copied".

#### Overlays

```js
export function anchoredOverlay(anchor, content, {placement, width, label, role = "dialog", className, focus = true, onClose, padding}): {close(), element, reposition()}
export function openMenu(anchor, items, {placement = "bottom-start", point, focus = "first", label, onClose}): {close(), element}
export function actionMenu(trigger, items, options): {open(focus), close(), destroy()}
export function selectPanel(anchor, {title, subtitle, items, selected, multiple, variant = "anchored", placeholder, filterLabel, onSelect, onCancel, onFilter, emptyTitle, emptyBody, placement, onClose}): {close(), element, setItems(items), setLoading(on, text), setError(text)}
export function autocomplete(input, {source, onSelect, allowAdd, minLength = 1, emptyText, filter = true}): {open(), close(), destroy()}
```

Every overlay uses the ActionMenu surface. It sits 4 px from its anchor,
flips to the other side when space runs out, and keeps 8 px from the viewport
edges. It uses the `popover` attribute, so it shows above open dialogs. An
overlay opened from inside a dialog is placed inside that dialog. Opening an
overlay again for the same anchor closes it. A click outside closes it.
Scrolling and resizing move it with the anchor.

`openMenu` items take `{label, description, descriptionLayout, icon,
shortcut, trailingVisual, onClick, href, danger, disabled, inactive,
inactiveText, checked, selectionType, confirm}`, `{separator: true}` or
`{group: heading}`. The menu has role `menu`. Items are `menuitem`, or
`menuitemradio` and `menuitemcheckbox` when `checked` is set. Focus moves to
the first item. ArrowDown and ArrowUp move and wrap. Home and PageUp go to the
first item, and End and PageDown to the last. A typed letter moves to the next
item that starts with it. Enter, Space or a click runs the item, closes the
menu and returns focus to the anchor unless the item has `href`. Escape closes
the menu and returns focus. Tab closes the menu and moves focus onward from the
anchor. `confirm` opens a ConfirmationDialog before `onClick` runs. On narrow
viewports the menu fills the screen with a Close button. `point` opens the
menu at a pointer position, for context menus. Items made only of
`{custom: node}` open as an AnchoredOverlay.

`actionMenu` wires a trigger. Click, Enter and Space open the menu at the
first item. ArrowDown opens it at the first item and ArrowUp at the last.

`selectPanel` is a dialog with a title, a filter input and a listbox. Focus
stays in the filter input, and `aria-activedescendant` marks the active
option. ArrowDown, ArrowUp, Home and End move the active option. Enter selects
it. The status region inside the panel announces the result count. Selected
items come first when the panel opens and after the filter is cleared.
Anchored single select calls `onSelect(id)` and closes. Anchored multiple
select calls `onSelect(ids)` when the panel closes. On narrow viewports, and
with `variant: "modal"`, a footer with Cancel and Save applies the choice, and
Escape discards it. `onFilter(query)` replaces local filtering with a request.

`autocomplete` gives the input role `combobox`, `aria-autocomplete="list"`,
`aria-expanded` and `aria-controls`. The listbox opens under the input box at
the same width. ArrowDown and ArrowUp move the active option, Enter chooses it
and Escape closes the list. `allowAdd(value)` returns an extra item, such as
"Invite ana@example.com", when nothing matches exactly. The list stays under
the input on narrow viewports.

#### Dialogs

```js
export function openDialog({title, subtitle, content, footer, width = "xlarge", height = "auto", align = "center", narrow = "auto", role = "dialog", hideTitle, initialFocus, returnFocus, onClose, className}): {close(reason), element, body, footer, buttons, showError(message)}
export function confirmDialog({title, body, confirmLabel, cancelLabel = "Cancel", danger = true, onConfirm, returnFocus}): Promise<boolean>
```

`openDialog` opens a `dialog` element with `showModal()`. The header holds an
`h1` title, an optional subtitle and a Close button. `content` is a node, a
string, or a function that receives the returned handle. Focus moves to
`initialFocus`, to an element with `autofocus`, or to the first control, which
is usually the Close button. Tab and Shift+Tab wrap inside the dialog. Escape
closes the top dialog only. A click on the backdrop closes the dialog unless a
form inside it has changed. Focus returns to the element that had focus before
the dialog opened, or to `returnFocus` when that element is gone. Page
scrolling stays locked while any dialog is open, and the scrollbar width is
kept as padding. Up to 2 dialogs can be open at once.

`width` is `small` (296 px), `medium` (320 px), `large` (480 px) or `xlarge`
(640 px). With `narrow: "auto"`, a dialog that holds an input, a textarea or a
select fills the screen on narrow viewports, a dialog without fields is a
bottom sheet, and an alert dialog stays centred.

A footer button is `{label, variant, onClick(close, event)}`. The dialog stays
open with the button busy while a returned promise runs. It closes when the
promise resolves, unless the value is `false`. A rejection shows a critical
Banner under the header. `showError(message)` shows that Banner directly.

`confirmDialog` opens a Primer ConfirmationDialog with role `alertdialog`,
320 px wide. Cancel sits on the left of the confirm button. Focus starts on
Cancel when `danger` is set, and on the confirm button otherwise. Escape
cancels. The dialog stays open with a busy confirm button while `onConfirm`
runs. A failure shows a critical Banner and keeps the dialog open. The promise
resolves `true` after a confirm and `false` after a cancel.

#### Messages

```js
export function announce(text, {politeness = "polite"}): HTMLElement
export function banner({variant = "info", title, hideTitle, description, actions, dismissible, onDismiss, layout, flush, headingLevel = 2, into, focus, announce}): HTMLElement
export function bannerHost(): HTMLElement
export function inlineMessage({variant = "critical", size = "medium", text}): HTMLElement
export function undoRow(row, {message, undoLabel = "Undo", onUndo, keep}): {element, button, finalize(), done: Promise<"undone"|"kept">}
export function undoable({label, run, undo, where, undoLabel, failLabel, keepWhere, into}): Promise<"undone"|"kept"|"failed">
```

`announce` writes to a visually hidden `role="status"` region. The region is
`[data-status-region]` in the page, or a region inside the top open dialog.
The same text twice in a row still counts as a change.

`banner` builds a `section` with `aria-label`, an icon, an `h2` title, a
description, up to 2 actions and an optional Dismiss button. `variant` is
`critical`, `warning`, `success`, `info` or `upsell`. A critical Banner has no
Dismiss button. With `into`, the Banner goes to the top of that container and
replaces an existing Banner of the same or lower severity. Dismissing a Banner
moves focus to the element after it. With `focus`, the Banner receives focus,
and otherwise its text is announced. `bannerHost()` returns
`[data-banner-host]`, then `#main .page`, then `#main`.

`inlineMessage` variants are `critical`, `warning`, `success` and
`unavailable`. The small size uses the filled 12 px icons.

`undoRow` replaces the content of a row with a muted message and a Button
invisible small "Undo". There is no timer. The row stays until the person
presses a pointer or Enter elsewhere in the same list, or leaves the page.
Then the row goes away, unless `keep` is set. Undo runs `onUndo` and restores
the row. Focus moves to Undo when the row held focus.

`undoable` runs `run` at once. With `where`, it offers Undo in that element
through `undoRow`, and the promise settles on Undo or when the row ends. When
`where` leaves the page first, the Undo moves to a dismissible info Banner.
Without `where`, the Undo shows in an info Banner at the top of `into` or of
`bannerHost()`, and the promise resolves `"kept"` as soon as the Banner shows.
A failed `run` shows a critical Banner and resolves `"failed"`.

#### Labels, Loading and Empty States

```js
export function counterLabel(count, {variant = "secondary"}): HTMLElement
export function setCount(element, count): void
export function label(text, {variant = "default", size = "small"}): HTMLElement
export function token(text, {size = "medium", href, onRemove, leadingVisual, selected}): HTMLElement
export function avatar({name, color, src, size = 20, square, alt}): HTMLElement
export function initialsOf(name): string
export function skeleton({variant = "text", width, height, lines, rows, label}): HTMLElement
export function progressBar({value, max = 100, size = "default", color = "accent", label, valueText}): HTMLElement & {set(value, valueText)}
export function blankslate({icon, title, description, primaryAction, secondaryAction, size = "medium", border, narrow, headingLevel}): HTMLElement
export function relativeTime(when, {precise}): HTMLElement
export function loadRelativeTime(): Promise<void>
```

`counterLabel` hides the visible number from screen readers and adds a hidden
copy in parentheses. A null count hides the counter. `label` uses the class
`ui-label`. Only `large` and `xlarge` tokens take a remove button, which is
named "Remove {text}". Backspace and Delete on a focused removable token
remove it. `avatar` shows initials on `color` in `--fgColor-onEmphasis`.

`relativeTime` builds a `relative-time` element with `no-title` and loads the
vendored `@github/relative-time-element` 5.3.1 once. `precise` shows the full
date and time. The Go template functions `timeAgo`, `relativeTime` and
`preciseTime` render the same element on server pages, and `app.js` loads the
script when a page contains one.

#### Forms and Editing

```js
export function formControl({label, name, type = "text", value, caption, error, success, required, options, leadingVisual, trailingVisual, trailingAction, size, monospace, hideLabel, rows, ...attrs}): HTMLElement
export function setFieldError(wrapper, message): void
export function clearFieldErrors(form): void
export function showFieldErrors(form, errors): boolean
export function editInPlace(element, {value, onSave, onCancel, validate, label = "Name", allowSame}): {input, save(), cancel(), done, element}
export function expandRow(row, content, {trigger, onClose}): {close(), element} | null
export function treeView({label, labelledBy, items, onOpen, onToggle, loadChildren, onKey, onContextMenu, emptyText}): {element, focusItem(id), setCurrent(id), expand(id), collapse(id), update(items)}
export function segmentedControl({label, items, size, onChange, iconsOnlyNarrow, fullWidth}): HTMLElement & {setSelected(value)}
export function toggleSwitch({checked, size, labelledBy, describedBy, label, onChange, disabled, statusLabelPosition}): HTMLElement & {button, setChecked(on)}
export function details({summary, content, open, kind = "disclosure", onToggle}): HTMLDetailsElement
export function createDropzone({accept, multiple = true, onFiles, label, buttonLabel}): HTMLElement
export function uploadList({sync, noteId, emptyText}): HTMLElement & {destroy()}
export function uploadStatus(upload, online): {text, percent, showBar}
```

`formControl` builds a label, the control, a caption and a validation message,
linked with `for` and `aria-describedby`. Checkboxes and radios use the
horizontal layout. Visuals and a trailing action wrap the input in a
`text-input` box. A select shows the `triangle-down` Octicon. The wrapper
keeps the class `field`.

`showFieldErrors` accepts an API error `{message, field}`, an array of
`{field, message}`, or a map from field name to message. With 1 or 2 field
errors, focus moves to the first invalid field. With 3 or more, a critical
error summary Banner at the top of the form lists each error as a link to its
field and receives focus. A message without a matching field shows in a
critical Banner at the top of the form. The function returns `true` when an
error attached to a field.

`editInPlace` hides the element and shows a small TextInput with Button small
primary "Save" and Button small invisible "Cancel" beside it. Enter saves and
Escape cancels. An empty or unchanged value cancels. `validate` returns a
message or null. When `validate` returns a message or `onSave` throws, the
edit stays open with the message under the field. `done` resolves to the saved
value, or to null after a cancel.

`expandRow` shows content in place under a row. A list keeps one expanded row.
Escape inside the content collapses it and returns focus to `trigger`.

`treeView` items are `{id, label, icon, children, hasChildren, expanded,
current}`. The tree has one tab stop. Down and Up move between visible rows.
Right expands a closed parent or moves to its first child. Left collapses an
open parent or moves to its parent. Home and End go to the first and last
visible rows. Enter calls `onOpen`. Printable characters move by type-ahead,
which resets after 300 ms and skips collapsed rows. A click on the chevron only
toggles. Other keys, such as F2 and Delete, go to `onKey`. With
`loadChildren`, a parent loads its children when it expands. A load failure
opens a dialog with Retry.

`segmentedControl` renders a `ul` with `aria-label` and buttons with
`aria-current`. Tab reaches each segment, and Enter or Space selects it.
ArrowLeft, ArrowRight, Home and End also move focus between segments.
Icon-only segments get a Tooltip. The choice applies at once through
`onChange`.

`toggleSwitch` renders a button with `aria-pressed`, labelled by the visible
label of the setting. When `onChange` returns a promise, a small Spinner shows
beside the switch, and a rejection turns the switch back.

`details` renders a native `details` element. Its summary is a Button
invisible small with `chevron-down` or `chevron-up`. `kind: "add"` uses a
Button default with a leading `plus`.

`createDropzone` builds the drop area with a Button small "Choose files" as
the keyboard route. The box itself takes no focus. Files of a type outside
`accept` show a critical InlineMessage inside the box. `uploadList` shows Box
rows with a 40 px thumbnail, a small ProgressBar while uploading, a Spinner
while queued and an InlineMessage on failure. Cancel upload asks through a
ConfirmationDialog.

`ui.js` has no status line, toast, two-step button or inline action row
helpers. No page calls `window.confirm`, `window.alert` or `window.prompt`.

#### Server Pages

`app.js` adds the same behaviour to server markup.

- A row menu is a trigger with `popovertarget` and a `[data-action-menu]` or
  `.row-menu` container with the `popover` attribute. `app.js` adds the menu
  roles, places the menu next to the trigger, and handles the keys that
  `openMenu` handles. ArrowDown and ArrowUp on the trigger open the menu.
- A button with `data-confirm` opens a ConfirmationDialog. `data-confirm` holds
  the question. A second sentence and `data-confirm-detail` form the body. The
  confirm button repeats the button label and is danger for danger buttons.
- A submit button with `data-undo-action`, `data-undo-label` and
  `data-undo-field-*` acts at once. The next page shows a dismissible info
  Banner with Undo, which posts the inverse action.
- An icon button with a `title` gets a Tooltip on first hover or focus, and
  the `title` attribute goes.
- The theme SegmentedControl in the account menu offers light, dark and
  system. It sets `data-color-mode` to `light`, `dark` or `auto`, stores light
  or dark in `markspace.theme`, and removes that key for system.

### Shell and Editor Modules

`shell.js` boots the application. It loads the session, opens the store,
starts sync, renders the layout and routes. `views.js` holds the list and note
views. The editor lives in `editor.js`, and `panels.js` holds the comment,
share, history and AI panels. The shell mounts the editor with:

```js
import {mountEditor} from "/static/js/editor.js";

const handle = mountEditor(container, {
  note,            // local note
  access,          // "view" | "comment" | "edit" | "manage"
  store, sync, render,
  navigate,        // (url) => void
  isOnline,        // () => boolean
  user,
  onDone,          // () => void, closes the editor
  setChrome,       // ({immersive: boolean}) => void, hides or shows shell chrome
  openComments,    // ({focusId, draftAnchor}) => void, opens the Comments rail tab
  openPanel,       // (kind, extra) => void, opens a rail tab: "history", "share", "files", "ai"
  commentSource,   // the shell's comment list for the note
  resolveFileId,   // (url) => fileId, optional, finds the file of a relative image path
});
handle.flush(): Promise<void>   // write pending keystrokes to the store
handle.isDirty(): boolean
handle.unmount(): void
handle.insertText(text, {tracked, title}), handle.replaceSelection(text), handle.getSelection(): string
handle.insertTracked(text, {title})   // puts text in as a tracked insertion for review
handle.openAsk({from, to, action, run, mode})   // opens the ask bar, or reports "AI needs a connection"
handle.setSuggestions(list, {authorName}): number   // draws stored suggestions as tracked changes
handle.changes(): object[]      // the tracked changes on screen
handle.goTo(pos)                // moves the cursor and scrolls it into view
handle.setActiveComment(id), handle.revealComment(id)
```

Every note panel is a tab in the right rail. In order, the tabs have the
accessible names Outline, Comments, Suggestions, History, Share, Files, AI and
Info. Share shows only at manage access, and AI only when the server has it
on and the browser is online. On phones
the rail opens as a bottom sheet with the same tabs. The editor asks the shell
for a panel through `openPanel` and falls back to its own drawer only without
the shell. Every panel is built from the shared pieces in `panels.js`
(`panelShell`, `itemCard`, `cardList`, `emptyState`, `offlineNote`,
`inlineError`, `skeletonCards`, `sectionHead`, `quoteBlock`), so headers,
cards, empty and offline states and quoted text look the same everywhere.

```js
import {openComments, openShare, openHistory, openAI} from "/static/js/panels.js";
import {openNoteFiles} from "/static/js/filepanels.js";   // the Files tab
// each: (container, {note, access, store, sync, user, isOnline, api}) => {unmount()}
// openAI also takes {insertText, insertTracked, getSelection, seed, contextRefs, render}.
// The shell shows the offline notice and opens no panel while offline. A panel
// disables its network controls if the connection drops while it is open.
```

### Editor Surface

The editor is CodeMirror 6 with three modes: live preview (the default),
source, and reading. The markdown text stays the document in every mode, so
saving never reformats a file. Live preview hides markup away from the cursor,
renders checkboxes, rules, images, code highlighting and wikilink pills as
widgets, and shows upload progress inline. `ai.md` covers the AI actions on
selections, links, wikilinks and images.

The AI surfaces live in `static/js/ai/`, and `assistant.md` specifies them.
The editor installs the `askBar`, `trackedChanges` and `imageActions`
extensions. The keyboard bar shows formatting, selection actions, image
actions or review of tracked changes, depending on what is selected. The ask bar docks inside the keyboard
bar on a phone. While `isOnline()` is false, every AI trigger stays visible
in the inactive style, and the ask bar shows an InlineMessage warning "AI
needs a connection". Of the AI features, only Edit image opens a Dialog.

## Testing

JavaScript tests use the built-in `node --test` runner against the pure
modules, with `createMemoryStore` and a fake `api` in place of IndexedDB and
the network. `make client` runs `test/client/*.test.mjs`. `make layout` runs the
Playwright audit in `test/browser`, and its `offline-shell` state switches the
network off mid-session to check the offline path.
