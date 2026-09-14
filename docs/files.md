# Files and Folders

A space is a folder tree that holds folders, notes and files. A note is a markdown file with frontmatter. A file is any other file,
such as a PDF, an image, a video or an archive. The application does not edit
file content. People can upload, rename, move, copy, replace, trash, restore
and delete files, and notes can link to them and embed them.

This document specifies folders and files for the store, the index, the link
library, the application layer, the HTTP API and the browser client.

## Terms

| Term | Meaning |
| --- | --- |
| item | A folder, note or file. Every item has an id in the `core.NewID` format |
| entry path | The slash-separated path of an item relative to the space directory, such as `Projects/harbour/survey.md` or `Projects/harbour` |
| folder note | A note named `index.md`. Opening its folder opens it |
| file version | A stored copy of a file's previous content, made when the file is replaced |

## On-Disk Layout

```
<dataDir>/
  markspace.db
  spaces/
    <space-slug>/
      Getting started.pdf             a file
      getting-started.md              a note
      Projects/                       a folder
        index.md                      the folder note of Projects
        harbour-survey.md
        east wall.jpg
      .trash/
        <item-id>/<base name>         a trashed note, file or folder, layout kept
  internal/
    history/<space-id>/<note-id>/<unix-millis>.md
    file-history/<space-id>/<file-id>/<unix-millis>-<name>
    thumbs/<file-id>/<sha256-prefix>-<width>.jpg or .png
  deleted/<space-slug>-<unix-millis>/
```

The layout follows these rules.

1. The parent of an item is the folder that contains it, or the space root.
   Note frontmatter has no `parent` key.
2. Names are Unicode. A name may not be empty, `.` or `..`, may not contain
   `/`, `\`, NUL or a control character, may not start with `.`, may not have
   leading or trailing spaces, and may not exceed 255 bytes. Names compare
   exactly, and two names that differ only by case may not coexist in one
   folder, because the data directory may sit on a case-insensitive
   filesystem.
3. A note file name is `slugify(title) + ".md"`, except `index.md`,
   which keeps its name when the title changes.
4. Files and folders keep the name the user gives them.
5. Scans skip every name starting with `.`, symlinks, and temporary names
   matching `*.tmp-*`, `~$*`, `.DS_Store`, `Thumbs.db` and `desktop.ini`.
6. A note larger than 8 MiB is indexed as a file, not a note.

## Identity

- A note's id lives in its frontmatter.
- A folder's id and a file's id live in the index only. Markdown never
  contains a folder id, a file id or a `/files/` URL. Notes refer to files
  with relative paths and wikilinks, described in the Links section, so a
  space directory stays portable.
- When the startup scan or the watcher finds a new directory or file on disk
  that the index does not know, it looks for a known item that
  vanished in the same scan or within 10 s in the watcher:
  - A file matches when size and SHA-256 are equal. If more than one candidate
    matches, the one with the same base name wins, then the one with the closest
    old path.
  - A folder matches when at least half of the note and file ids known under
    the vanished folder appear under the new one, with a minimum of one.
  - A match keeps the id and updates the path. No match creates a new item.
    A vanished item with no match loses its index row, with its comments,
    shares and public links.
  - The watcher indexes a write of a note in a known folder directly. Any
    other event schedules a rescan of the space after 1 s, which applies the
    matching rules above.
- The index stores `size`, `mod_time` and `sha256` for each file. The application
  computes the hash after upload, and in a background pass for files found on
  disk. A
  file whose size and mod time are unchanged keeps its stored hash.

## Folder Notes and Nested Pages

- A folder containing `index.md` shows that note when the folder opens. The
  tree lists `index.md` as the folder's own page, not as a child row.
- "New page inside" on a note `a/x.md` that is not a folder note converts it:
  `a/x.md` moves to `a/x/index.md`, keeping its id, and the new page goes in
  `a/x/`. The application rewrites its relative links as for any move.
- `POST /api/notes` with `{spaceId, folderId, folderNote: true, title,
  content}` creates `index.md` in an existing folder. The title goes into the
  frontmatter and does not change the file name. The call answers 409 when
  the folder already has an entry named `index.md`, in the index or on disk.
  `folderId` without `folderNote`, `folderNote` without `folderId`, a
  `parentId` other than `folderId`, and a `folderId` that names a note or a
  file answer 400. The plugin host API `POST /notes` takes the same fields.
- An ordinary note titled `index` gets the file name `index-note.md`, because
  `index.md` names a folder note.
- A folder with no `index.md` opens a folder view that lists its children.

## Links

A shared library, `internal/links` in Go and `static/js/links.js` in the
browser, parses and resolves links. `test/vectors/links.json` holds cases that
both must pass. The package comment in `internal/links/doc.go` is the exact
specification. It adds three rules to this section. A link is rewritten only
when its text would no longer resolve to the same item. A broken name wikilink
becomes the new name, then a path wikilink, then a markdown link. Path matching
is case-sensitive.

### Forms

| Form | Example | Meaning |
| --- | --- | --- |
| wikilink | `[[Harbour survey]]`, `[[harbour-survey\|the survey]]` | link to a note or file by name |
| path wikilink | `[[Projects/harbour/survey]]`, `[[east wall.jpg]]` | link by path from the space root, or by a unique path suffix |
| heading | `[[Harbour survey#Findings]]` | link to a heading |
| embed | `![[east wall.jpg]]`, `![[report.pdf]]`, `![[Harbour survey]]` | image inline, other files as a viewer card, notes as a link card |
| markdown link | `[report](../report.pdf)`, `[x](<east wall.jpg>)` | relative path from the note's folder, or from the space root when it starts with `/` |
| markdown image | `![alt](east%20wall.jpg)` | as above, image inline |

The resolver skips external URLs (`http:`, `https:`, `mailto:`) and anchors
(`#x`).

### Resolving a Name

For `[[target]]` in a note at folder `F`:

1. Strip `#heading` and `|label`. Percent-decode markdown link paths.
2. If the target contains `/`, match the entry path exactly, with or without
   `.md`. If none matches, match notes and files whose entry path ends with
   `/target` or `/target.md`.
3. Otherwise match, in this order:
   - a file whose name equals the target,
   - a note whose file name without `.md` equals the slug of the target,
   - a note whose title equals the target, compared case-insensitively.
4. Among more than one match, prefer the one in `F`, then the one whose folder is
   nearest to `F` by tree distance, then the shortest entry path, then the
   lexically smallest entry path.
5. A folder note matches by its title and by its folder's name.

Markdown links resolve only by path. No fallback to names applies.

### Rewriting

The library computes text edits, as byte ranges in the note body, for these
events:

| Event | Notes that change | What changes |
| --- | --- | --- |
| Rename a file | notes linking to it | wikilinks by name get the new name, and path links get the new path. Labels stay |
| Rename a note | notes linking to it by path | the path. Title-based wikilinks keep working and are left alone |
| Change a note title | none | nothing. The file renames through `slugify`, and path links follow the rename rule |
| Move an item | notes linking to it by path, and, when the item is a note, its own relative markdown links | the path |
| Move or rename a folder | notes outside it that link by a path through it, and notes inside it that link by path to items outside it | the path. Links between two items that both moved stay valid |

A link keeps its form. A markdown link stays relative. A path wikilink stays a
path from the root. A name wikilink becomes a path wikilink only when the
new name would resolve to a different item. The rewrite escapes the result
for its form. It uses `<...>` angle brackets for a markdown path with spaces
or parentheses, and it never breaks a `|label` or `#heading`.

The application applies rewrites only to notes the actor can edit. Before
rewriting a note it saves a version labelled `Links updated`. A rewrite that
fails its compare-and-swap on the note's `updatedAt` retries once from a
fresh read. It then skips the note and logs a warning.

## Core Types

```go
type ItemKind string

const (
	KindFolder ItemKind = "folder"
	KindNote   ItemKind = "note"
	KindFile   ItemKind = "file"
)

// Folder is a directory in a space.
type Folder struct {
	ID          string     `json:"id"`
	SpaceID     string     `json:"spaceId"`
	ParentID    string     `json:"parentId,omitempty"` // "" is the space root
	Name        string     `json:"name"`
	Path        string     `json:"path"`
	Position    int        `json:"position"`
	IndexNoteID string     `json:"indexNoteId,omitempty"`
	Rev         int64      `json:"rev,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	CreatedBy   string     `json:"createdBy,omitempty"`
	Deleted     bool       `json:"deleted"`
	DeletedAt   *time.Time `json:"deletedAt,omitempty"`
	DeletedBy   string     `json:"deletedBy,omitempty"`
	SpaceSlug   string     `json:"spaceSlug,omitempty"`
}

// File is a non-markdown file in a space.
type File struct {
	ID        string     `json:"id"`
	SpaceID   string     `json:"spaceId"`
	ParentID  string     `json:"parentId,omitempty"`
	Name      string     `json:"name"`
	Path      string     `json:"path"`
	MIME      string     `json:"mime"`
	Size      int64      `json:"size"`
	SHA256    string     `json:"sha256,omitempty"` // hex, empty until computed
	Width     int        `json:"width,omitempty"`
	Height    int        `json:"height,omitempty"`
	Duration  float64    `json:"duration,omitempty"` // seconds, audio and video, when known
	Pages     int        `json:"pages,omitempty"`    // PDF
	Position  int        `json:"position"`
	Versions  int        `json:"versions"`           // stored previous versions
	Rev       int64      `json:"rev,omitempty"`
	ModTime   time.Time  `json:"modTime"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	CreatedBy string     `json:"createdBy,omitempty"`
	UpdatedBy string     `json:"updatedBy,omitempty"`
	Deleted   bool       `json:"deleted"`
	DeletedAt *time.Time `json:"deletedAt,omitempty"`
	DeletedBy string     `json:"deletedBy,omitempty"`
	SpaceSlug string     `json:"spaceSlug,omitempty"`
	URL       string     `json:"url,omitempty"`   // /files/{slug}/{id}/{name}
	Thumb     string     `json:"thumb,omitempty"` // /thumbs/{slug}/{id}, images and video posters only
}

// FileVersion is a stored previous content of a file.
type FileVersion struct {
	ID        string    `json:"id"`
	FileID    string    `json:"fileId"`
	SpaceID   string    `json:"spaceId"`
	Name      string    `json:"name"`
	MIME      string    `json:"mime"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	AuthorID  string    `json:"authorId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// TreeItem is one row of the space tree. Exactly one of Folder, Note and
// File is set.
type TreeItem struct {
	Kind     ItemKind    `json:"kind"`
	Folder   *Folder     `json:"folder,omitempty"`
	Note     *Note       `json:"note,omitempty"`
	File     *File       `json:"file,omitempty"`
	Children []*TreeItem `json:"children,omitempty"`
}
```

`Note.ParentID` holds a folder id or "".

Items in one folder sort by position, then by kind (folders, then notes, then
files), then by name case-insensitively. A new item takes the next position.
An item found on disk takes position 0, so it sorts by kind and name.

## Access

The ancestor chain of an item is its parent folder, that folder's parent, and
so on up to the root. `design.md` defines the grants. These rules apply them
to folders and files.

- A share or public link can target a folder, a note or a file. The `shares`
  and `public_links` rows carry a `target_kind` column. `note_id` keeps its
  name in SQL and holds the target id.
- A share on a folder with `includeChildren` covers everything below it. A
  share on a folder without it lets the user open the folder view and see its
  direct children listed, but not open them.
- Access levels on a file are view, comment and manage. Edit access on a
  file allows rename, move, replace and trash, the same as on a note.
- A share with `includeChildren` on a folder note covers its folder and
  everything below that folder.
- Comments work on files and folders. They carry no anchor.
- Creating an item needs edit access on the parent folder, or on the space
  when the parent is the root.
- A move needs edit access on the item, on its old parent and on its new
  parent. A move never crosses spaces. A copy can cross spaces, and it needs view
  access on the source and edit access on the target parent.

## Operations

| Operation | Rule |
| --- | --- |
| Create folder | Name rules apply. A name taken in the parent returns `ErrExists` |
| Upload | Streams to a temporary file in the target folder, then renames. The name of an existing item returns `ErrExists` unless `onConflict` is `rename`, which picks `<stem> (N).<ext>`, or `replace`, which replaces the existing file. A name ending in `.md` returns `ErrInvalid`, because a `.md` file is a note |
| Rename | Folders and files, within the same folder. Rewrites links. A note renames through its title, and a folder note keeps the name `index.md` |
| Move | Within the space. Moving a folder into itself or a descendant returns `ErrInvalid`. Rewrites links |
| Copy | Folder copies are recursive. Every copied note gets a new id in frontmatter, and every copied file gets a new id and no versions. A copy keeps its name when the name is free in the target folder. On a collision the copy name is `<stem> copy.<ext>`, then `<stem> copy N.<ext>`. A single note copy takes the title `<title> copy`, then `<title> copy N`, and its file name follows the title |
| Replace | Edit access. The old content moves to `file-history` as a version. The file keeps its id and name unless the upload has a different extension, which changes the name's extension. `baseSha256` in the request must match the current hash or the call returns `ErrConflict` |
| Restore version | Replaces the current content with the version, keeping the current content as a new version |
| Trash | Moves the item to `.trash/<id>/<base name>`. A folder moves with its whole subtree. Trashed items keep their ids |
| Restore | Returns the item to its old path. If the old parent folder is gone, the item returns to the space root. A name collision picks `<stem> (N).<ext>`. An item that went to the trash inside a folder returns `ErrConflict`, and the folder restores it |
| Delete | Permanent. Needs manage, or edit for the creator of the item. Deletes versions, thumbnails, comments, shares and links. An item that went to the trash inside a folder returns `ErrConflict`, and deleting the folder removes it |

Every write takes the next value of the index revision counter for each item
it changes, as a note write does.

### File Versions

The application keeps up to 20 versions per file and up to 5 GiB of versions
per space. Past either limit the store deletes the oldest versions first.

## Search

A second full-text table, `files_fts(name, body, file_id UNINDEXED, space_id
UNINDEXED)`, indexes file names and extracted text. Extraction runs in a
background queue for PDFs and for text-like files (`text/*`, JSON, CSV, YAML,
XML, source code) up to 20 MiB, and keeps the first 200 000 characters.
`GET /api/search` returns file hits after note hits with the same score scale.
Each hit carries `kind`, and a file hit carries `file` in place of `note`.

## Thumbnails

`GET /thumbs/{space}/{id}?w=` serves a thumbnail for images, with `w` one of
128, 256, 512 or 1024. The server makes it on first request with
`internal/extract` and caches it under `internal/thumbs`. The cache key
includes the file hash, so a replaced file gets new thumbnails. SVG files get
no raster thumbnail, and the client shows them through `<img>`.

## HTTP API

`routes.md` lists every route. The folder, file and item routes are these.

```
GET    /api/spaces/{id}/tree            [TreeItem], every visible item
GET    /api/spaces/{id}/files?type=&sort=name|size|updated&limit=&offset=
POST   /api/folders                     {id?, spaceId, parentId, name}
PATCH  /api/folders/{id}                {name}
GET    /api/folders/{id}                Folder with children
POST   /api/files                       multipart: spaceId, parentId, id?, onConflict?, pasteFor?, then file
GET    /api/files/{id}                  File
PATCH  /api/files/{id}                  {name}
PUT    /api/files/{id}/content          multipart: baseSha256, then file
GET    /api/files/{id}/versions
POST   /api/files/{id}/versions/{versionId}/restore
GET    /api/files/{id}/backlinks
POST   /api/items/{id}/move             {parentId, position}
POST   /api/items/{id}/copy             {spaceId, parentId, name?}
POST   /api/items/{id}/trash
POST   /api/items/{id}/restore
DELETE /api/items/{id}
GET    /api/items/{id}                  {kind, folder|note|file, access}
GET    /api/items/{id}/comments         and POST, as for notes
GET    /api/items/{id}/shares           and POST, as for notes
GET    /api/items/{id}/links            and POST, as for notes
POST   /api/resolve                     {spaceId, fromNoteId, targets: [string]} -> [{target, kind, id, path, url}]
```

In a multipart upload the text fields must come before the part named
`file`. `POST /api/files` also accepts `pasteFor`, the id of a note. The file
then goes into the note's folder, or into the folder named by the space
setting `pasteFolder` below it, which is created when missing.
`GET /api/items/{id}/backlinks` lists backlinks for any kind, and
`GET /api/spaces/{id}/trash?items=1` lists trashed items of every kind.

The web routes for folders and files are these.

```
GET /s/{space}/f/{path...}            file view
GET /s/{space}/d/{path...}            folder view, or its folder note
GET /files/{space}/{id}/{name}        content, with Range support; ?v={versionId} for a version; ?download=1 forces attachment
GET /thumbs/{space}/{id}?w=           thumbnail
GET /i/{id}                           permalink for any item
GET /p/{token}/i/{id}                 an item inside a public folder link
GET /p/{token}/raw/{id}/{name}        file content through a public link
GET /p/{token}/thumb/{id}?w=          thumbnail through a public link
```

## Sync Feed

`SyncBatch` carries `folders` and `files` arrays of metadata, ordered by
revision with the notes, from the same counter. `liveIds` covers all three
kinds. Offline clients create folders with client-generated ids, like notes.
A replayed create with the same id, kind, creator and space returns the
stored item.
The client uploads a file created offline with its client-generated id.

## Events

The event types for folders and files are `folder.created`, `folder.updated`,
`folder.deleted`, `file.created`, `file.updated`, `file.deleted` and
`item.moved`, next to `tree.changed`. The event stream filters them with the same access check as notes. An
event about one item carries `itemId` and `kind`, and a note event also
carries `noteId`.

## Browser Client

### Store and Sync

- Version 4 of the IndexedDB schema adds the `folders` and `files` object stores,
  each with indexes on `spaceId`, `parentId` and `path`.
- `store.tree(spaceId)` returns `[{kind, folder|note|file, children}]`.
- `store.resolveLink(spaceId, fromNoteId, target)` uses `links.js`.
- The outbox ops for folders and files are these.

| Op | Request |
| --- | --- |
| `folder.create` | `POST /api/folders` |
| `folder.rename` | `PATCH /api/folders/{id}` |
| `file.upload` | `POST /api/files` with the blob from `blobs` and the client id |
| `file.rename` | `PATCH /api/files/{id}` |
| `file.replace` | `PUT /api/files/{id}/content` |
| `item.move` | `POST /api/items/{id}/move` |
| `item.copy` | `POST /api/items/{id}/copy` |
| `item.trash` | `POST /api/items/{id}/trash` |
| `item.restore` | `POST /api/items/{id}/restore` |
| `item.delete` | `DELETE /api/items/{id}` |

- A 409 on `file.replace` keeps the local blob as a new file named
  `<stem> (conflicted copy YYYY-MM-DD HH:MM).<ext>` in the same folder.
- A pull that moves or renames an item updates the paths of its descendants
  locally.

### File Content Cache

- File content is not part of the sync feed. The client fetches it when a
  file opens, or when a note embeds an image.
- `internal/web/static/js/filecache.js` manages a Cache Storage cache named
  `markspace-files-<userId>`, with an index in IndexedDB of
  `{fileId, sha256, size, lastAccess, pinned}`.
- The budget is 500 MiB per device by default. A person can change it on the
  `/settings/offline` page. Past it, unpinned files evict least recently used first.
- "Keep available offline" on a file pins it. On a folder, it pins every file
  below it now and each file added later. Pinned files download in the
  background when online, and a replaced pinned file downloads again.
- The service worker serves `GET /files/*` from that cache first, answers
  `Range` requests from a cached blob with `206 Partial Content`, and falls
  back to the network. It posts `{type: "file-accessed", fileId}` to the page
  so the index can update `lastAccess`. It adds nothing to the cache
  beyond what the page puts there.
- Offline, a file that is not cached opens its info view with "Available when
  online", and an embedded image shows a placeholder with its name.

### Tree and Views

- The sidebar tree shows folders, notes and files with type icons. It supports
  drag to move, drop of desktop files onto a folder to upload, and
  multi-select with Shift and Ctrl, or with Select mode on phones. While the
  tree has focus, F2 renames, Delete moves to the trash, Ctrl+C and Ctrl+V
  copy and paste, and Ctrl+X and Ctrl+V move.
- The item menu is an ActionMenu with Open, New note, New folder, Upload,
  Rename, Move to, Copy to, Replace for files, Download, Keep available
  offline, Copy link, Share, and Move to trash last after a divider. Tree rows
  hold no button besides the chevron, so the menu opens from a right-click,
  Shift+F10, the Menu key or the opened item's PageHeader menu. On a narrow
  viewport the menu opens full screen. Rename puts the name in a small text
  field with Save and Cancel beside it. Move to and Copy to open a Dialog with
  a TreeView folder picker. Move to trash acts at once and offers Undo, as
  `ui.md` describes.
- The folder view lists children as rows (name, kind, size, updated) with the
  same actions, and a grid toggle that shows thumbnails.
- The file view is full-bleed, with a slim header holding the name, the
  primary action Download, and a menu for the rest. Its rail tabs are Info,
  Comments, History, Share and Backlinks.

| Type | Viewer |
| --- | --- |
| image | fit to view, zoom with wheel and pinch, pan by drag, 1:1 toggle |
| PDF | vendored pdf.js, lazy loaded, with page thumbnails, search in document, and zoom. It works on phones, where browsers do not display PDFs inline |
| video, audio | native `<video>` and `<audio>` with Range requests |
| text, code, CSV, JSON, markdown source | read-only CodeMirror with syntax highlighting. CSV shows as a table paged at 20 rows, with Download for the full file |
| anything else | icon, size, type, dates, Download |

The editor inserts a relative markdown link for pasted or dropped files. It
inserts `![](name.png)` for images and `[name.pdf](name.pdf)` for other files,
and saves the file into the note's folder. A space setting named
`pasteFolder` can name a subfolder, such as `attachments`, instead. The `[[`
completion lists notes and files. Live preview renders `![[x.pdf]]` as a
viewer card with the first page thumbnail.

## Testing

- Store: `FuzzValidName`, `FuzzEntryPath` and `FuzzFoldKey` in `internal/store`
  check that no accepted name or entry path escapes the space, and that fold
  keys match `strings.EqualFold`, so no two case-folded names coexist.
- Links: `test/vectors/links.json` holds 27 parse, 42 resolve and 23 rewrite
  cases, run by `internal/links` and by `test/client/links.test.mjs`.
  `FuzzRewriteStable` applies the edits after random renames and moves, then
  checks that every link resolves to the same entry id as before.
- App: `FuzzItemOperations` in `internal/app` runs a stateful model over create,
  upload, move, rename, copy, trash, restore, delete and replace, plus renames
  made directly on disk. After each operation it checks these invariants:
  - every indexed item exists on disk at its path,
  - a reconcile finds nothing to change,
  - no id repeats,
  - parent chains end at the root,
  - no two live siblings have case-equal names,
  - link targets are unchanged by moves and renames.
- Access: `FuzzAccessOracle` in `internal/app` compares the resolved access on
  folders, notes and files with a reference model.
- HTTP: `FuzzHTTPAPI` in `test/server/fuzz_test.go` covers the folder,
  file and item routes and fails on any 5xx.
- Browser: `test/client/files.test.mjs`, `filecache.test.mjs` and
  `viewers.test.mjs` cover the client modules. The layout audit in `test/browser`
  opens the PDF viewer and its thumbnails on phone viewports and drags a note
  onto a folder in the tree.
