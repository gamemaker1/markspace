// Package ports declares the interfaces between the application layer and its
// storage, rendering and model-server dependencies.
package ports

import (
	"context"
	"io"
	"io/fs"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// ScannedNote is one markdown file read from disk.
type ScannedNote struct {
	Note    *core.Note
	ModTime time.Time
	Size    int64
	// ParentRel is the folder holding the note, "" at the space root. Only
	// ScanTree sets it.
	ParentRel string
}

// ScannedFolder is one directory found by ScanTree.
type ScannedFolder struct {
	RelPath string
	ModTime time.Time
}

// ScannedFile is one non-note file found by ScanTree. MIME comes from the
// extension, or from the leading bytes when the extension is unknown.
type ScannedFile struct {
	RelPath string
	Size    int64
	ModTime time.Time
	MIME    string
}

// ScanResult is everything ScanTree found in a space, each list sorted by
// path.
type ScanResult struct {
	Folders []ScannedFolder
	Notes   []ScannedNote
	Files   []ScannedFile
}

// SavedFile describes content written by SaveFile or ReplaceFile. Width and
// Height are set for PNG, JPEG, GIF and WebP images.
type SavedFile struct {
	Size   int64
	MIME   string
	SHA256 string
	Width  int
	Height int
}

// FileHistoryKey names the file whose old content becomes a version. Name is
// the file name recorded with the version, and defaults to the base name of
// the path. SHA256 and MIME describe the old content when the caller already
// knows them. The store computes them otherwise.
type FileHistoryKey struct {
	SpaceID  string
	FileID   string
	Name     string
	AuthorID string
	SHA256   string
	MIME     string
}

// CopiedEntry is one entry written by CopyEntry. OldID and NewID are set for
// notes only, since folder and file ids live in the index.
type CopiedEntry struct {
	Kind   core.ItemKind
	OldRel string
	NewRel string
	OldID  string
	NewID  string
}

// TrashedEntry is one item in a space trash. TrashRel is
// ".trash/<id>/<base name>". Note is set for notes. Size is the byte count of
// a file or note, and the total of every regular file below a folder.
type TrashedEntry struct {
	ID          string
	Kind        core.ItemKind
	TrashRel    string
	TrashedFrom string
	DeletedAt   time.Time
	DeletedBy   string
	Size        int64
	ModTime     time.Time
	Note        *core.Note
}

// ImportedFile is one non-markdown file written by ImportArchive.
type ImportedFile struct {
	RelPath string
	SavedFile
}

// ImportResult lists what ImportArchive wrote. Folders holds the folders it
// created, parents first.
type ImportResult struct {
	Folders []string
	Notes   []ImportedNote
	Files   []ImportedFile
}

// ImportedNote is one file written by an import.
type ImportedNote struct {
	RelPath string
	Note    *core.Note
}

// FileEvent reports a change made to a space tree outside the application.
// Op is "write" or "remove". A rename arrives as a remove of the old path and
// a write of the new one. Kind is "note", "file" or "folder". On a write the
// kind comes from the entry on disk. On a remove it comes from the directories
// the watcher knew and otherwise from the name, so a removed note over 8 MiB
// reports "note".
type FileEvent struct {
	SpaceSlug string
	RelPath   string
	Op        string
	IsDir     bool
	Kind      core.ItemKind
}

// Files is the space file store. Every relPath is slash separated and
// relative to the space directory. The note methods take paths ending in
// ".md". The entry methods take any entry path whose segments pass ValidName. Implementations reject any path that escapes the space
// directory, and entry methods refuse to create two names in one folder that
// differ only by case.
type Files interface {
	// EnsureSpace creates the space directory if it is missing.
	EnsureSpace(slug string) error
	// RenameSpace moves a space directory.
	RenameSpace(oldSlug, newSlug string) error
	// ArchiveSpace moves a space directory out of spaces/ into deleted/.
	ArchiveSpace(slug string) error
	// ListSpaceDirs returns the space directory names present on disk.
	ListSpaceDirs() ([]string, error)

	// Read loads one note.
	Read(spaceSlug, relPath string) (*core.Note, error)
	// Write creates or replaces the file at note.Path, creating directories as
	// needed, and writes frontmatter from the note fields plus note.Extra.
	Write(spaceSlug string, note *core.Note) error
	// Exists reports whether a note file is present.
	Exists(spaceSlug, relPath string) bool
	// Stat returns the modification time and size of a note file.
	Stat(spaceSlug, relPath string) (time.Time, int64, error)

	// EmptyTrash permanently deletes the whole space trash directory.
	EmptyTrash(spaceSlug string) error

	// SaveVersion writes a snapshot of a note body and returns its record.
	SaveVersion(spaceID string, note *core.Note, label string) (*core.NoteVersion, error)
	// ListVersions returns snapshots newest first, without content.
	ListVersions(spaceID, noteID string) ([]*core.NoteVersion, error)
	// ReadVersion returns one snapshot with content.
	ReadVersion(spaceID, noteID, versionID string) (*core.NoteVersion, error)
	// PruneVersions keeps the newest keep snapshots of a note.
	PruneVersions(spaceID, noteID string, keep int) error

	// ExportSpace writes a zip holding every note in notes, serialized from
	// the given fields, plus every other entry in the space as it is on disk.
	ExportSpace(spaceSlug string, notes []*core.Note, w io.Writer) error
	// ImportMarkdown writes a single markdown file into a space.
	ImportMarkdown(spaceSlug, filename string, data []byte) (*ImportedNote, error)

	// Watch reports changes under the spaces directory until ctx ends.
	Watch(ctx context.Context) (<-chan FileEvent, error)

	// DiskUsage returns the byte size of the data directory.
	DiskUsage() (int64, error)

	// ValidName checks one folder, note or file name against the name rules.
	// Ignored temporary names such as "Thumbs.db" are refused, because a scan
	// would never report them.
	ValidName(name string) error
	// CaseCollision returns the name of an entry in parentRel that equals name
	// ignoring case, other than the entry at exceptRel.
	CaseCollision(spaceSlug, parentRel, name, exceptRel string) (string, bool)
	// AllocName returns name, or "<stem> (N).<ext>" with the first free N from
	// 2, so that no entry in parentRel other than exceptRel collides. A folder
	// name has no extension.
	AllocName(spaceSlug, parentRel, name, exceptRel string, isDir bool) (string, error)
	// AllocCopyName returns "<stem> copy.<ext>", then "<stem> copy N.<ext>"
	// with N from 2, free in parentRel.
	AllocCopyName(spaceSlug, parentRel, name string, isDir bool) (string, error)
	// AllocNotePath picks a free "<slug>.md" path for a note titled title in
	// folderRel, "" for the root. exceptRel does not count as a collision.
	AllocNotePath(spaceSlug, folderRel, title, exceptRel string) (string, error)

	// ScanTree walks a space and returns its folders, notes and files without
	// hashing. It skips dot names, symlinks, ignored temporary names and names
	// that fail ValidName. A note over 8 MiB, or one whose frontmatter does
	// not parse, is reported as a file.
	ScanTree(spaceSlug string) (ScanResult, error)
	// StatEntry describes the entry at rel without following a symlink.
	StatEntry(spaceSlug, rel string) (fs.FileInfo, error)
	// MakeDir creates the folder rel. Its parent must exist.
	MakeDir(spaceSlug, rel string) error
	// MoveEntry renames a note, file or folder within a space, creating
	// missing parent folders. Moving a folder into itself returns
	// core.ErrInvalid.
	MoveEntry(spaceSlug, oldRel, newRel string) error
	// CopyEntry copies an entry, recursively for a folder, to dstRel in
	// dstSpace. The parent of dstRel must exist. Every copied note gets the id
	// rewriteNoteID returns for its old id, or a new id when rewriteNoteID is
	// nil. The copy is built under a temporary name and renamed into place.
	CopyEntry(spaceSlug, srcRel, dstSpace, dstRel string, rewriteNoteID func(oldID string) string) ([]CopiedEntry, error)
	// RemoveEntry permanently deletes an entry and everything below it.
	RemoveEntry(spaceSlug, rel string) error

	// SaveFile streams r to a temporary file in the target folder, hashing as
	// it writes, and renames it to rel. It returns core.ErrExists when a name
	// equal to rel ignoring case is taken, and core.ErrTooLarge past maxBytes.
	SaveFile(spaceSlug, rel string, r io.Reader, maxBytes int64) (SavedFile, error)
	// ReplaceFile writes new content over the file at rel and keeps the old
	// content as a version. It returns the new content and the version.
	ReplaceFile(spaceSlug, rel string, r io.Reader, maxBytes int64, key FileHistoryKey) (SavedFile, *core.FileVersion, error)
	// OpenFile opens a regular file for reading. The caller closes it.
	OpenFile(spaceSlug, rel string) (io.ReadSeekCloser, fs.FileInfo, error)
	// HashFile returns the hex SHA-256 of a file.
	HashFile(spaceSlug, rel string) (string, error)

	// ListFileVersions returns the stored versions of a file, newest first.
	ListFileVersions(spaceID, fileID string) ([]*core.FileVersion, error)
	// OpenFileVersion opens one stored version. The caller closes it.
	OpenFileVersion(spaceID, fileID, versionID string) (io.ReadSeekCloser, *core.FileVersion, error)
	// RestoreFileVersion copies a version over the file at rel. The current
	// content becomes a new version, which is returned with the restored
	// content. The restored version stays in the history.
	RestoreFileVersion(spaceSlug, rel, versionID string, key FileHistoryKey) (SavedFile, *core.FileVersion, error)
	// PruneFileVersions keeps the newest keep versions of a file, then
	// deletes the oldest versions of any file in the space until the space
	// history fits spaceByteBudget. keep < 0 and spaceByteBudget <= 0 mean no
	// limit. It returns the versions it deleted.
	PruneFileVersions(spaceID, fileID string, keep int, spaceByteBudget int64) ([]*core.FileVersion, error)
	// DeleteFileHistory deletes every version of a file.
	DeleteFileHistory(spaceID, fileID string) error

	// TrashEntry moves a note, file or folder to ".trash/<id>/<base name>"
	// and returns that path. A note gets the trash frontmatter keys. Every
	// kind gets a ".meta.json" sidecar in its trash slot.
	TrashEntry(spaceSlug, rel, id, deletedBy string) (string, error)
	// RestoreEntry moves a trashed entry to targetRel. The parent of
	// targetRel must exist.
	RestoreEntry(spaceSlug, trashRel, targetRel string) error
	// ScanTrashEntries lists every trashed item.
	ScanTrashEntries(spaceSlug string) ([]TrashedEntry, error)
	// PurgeTrashEntry permanently deletes one trash slot.
	PurgeTrashEntry(spaceSlug, trashRel string) error

	// Thumbnail returns the path and media type of a cached thumbnail of an
	// image at most width pixels wide, making it on first request. width is
	// 128, 256, 512 or 1024. sha256 is the hex hash of the current content.
	Thumbnail(spaceSlug, rel, fileID, sha256 string, width int) (string, string, error)
	// DeleteThumbnails removes every cached thumbnail of a file.
	DeleteThumbnails(fileID string) error

	// ImportArchive unpacks a zip into the space root. Markdown entries become
	// notes and every other entry becomes a file. Names that collide get a
	// free name. Limits refuse an entry rather than truncate it.
	ImportArchive(spaceSlug string, r io.ReaderAt, size int64) (*ImportResult, error)
}
