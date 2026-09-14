package core

import "time"

// ItemKind names the three kinds of item a space holds.
type ItemKind string

// Item kinds.
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
	Versions  int        `json:"versions"` // stored previous versions
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

// Item is one folder, note or file with the actor's access to it. Exactly
// one of Folder, Note and File is set.
type Item struct {
	Kind   ItemKind `json:"kind"`
	Folder *Folder  `json:"folder,omitempty"`
	Note   *Note    `json:"note,omitempty"`
	File   *File    `json:"file,omitempty"`
	Access Access   `json:"access"`
}

// ID returns the id of the item.
func (it *Item) ID() string {
	switch {
	case it == nil:
		return ""
	case it.Folder != nil:
		return it.Folder.ID
	case it.Note != nil:
		return it.Note.ID
	case it.File != nil:
		return it.File.ID
	}
	return ""
}

// SpaceID returns the space of the item.
func (it *Item) SpaceID() string {
	switch {
	case it == nil:
		return ""
	case it.Folder != nil:
		return it.Folder.SpaceID
	case it.Note != nil:
		return it.Note.SpaceID
	case it.File != nil:
		return it.File.SpaceID
	}
	return ""
}

// Path returns the entry path of the item.
func (it *Item) Path() string {
	switch {
	case it == nil:
		return ""
	case it.Folder != nil:
		return it.Folder.Path
	case it.Note != nil:
		return it.Note.Path
	case it.File != nil:
		return it.File.Path
	}
	return ""
}

// Upload conflict policies.
const (
	OnConflictFail    = ""
	OnConflictRename  = "rename"
	OnConflictReplace = "replace"
)

// FolderInput creates a folder. ID is optional. An offline client supplies
// an id it generated, so replaying the create is idempotent.
type FolderInput struct {
	ID       string `json:"id,omitempty"`
	SpaceID  string `json:"spaceId"`
	ParentID string `json:"parentId,omitempty"`
	Name     string `json:"name"`
}

// FileUpload describes one uploaded file. ID is optional, as for FolderInput.
// OnConflict is "", "rename" or "replace". PasteFor names a note. The file
// then goes into the note's folder, or into the space's paste folder below
// it when the space sets one, and SpaceID and ParentID are ignored.
type FileUpload struct {
	ID         string `json:"id,omitempty"`
	SpaceID    string `json:"spaceId"`
	ParentID   string `json:"parentId,omitempty"`
	Name       string `json:"name"`
	OnConflict string `json:"onConflict,omitempty"`
	PasteFor   string `json:"pasteFor,omitempty"`
}

// FileReplace describes new content for an existing file. BaseSHA256 must
// equal the current hash. Name is the upload name, whose extension becomes
// the file's extension.
type FileReplace struct {
	Name       string `json:"name,omitempty"`
	BaseSHA256 string `json:"baseSha256"`
}

// CopyInput places a copy. An empty SpaceID keeps the source space and an
// empty Name picks a copy name.
type CopyInput struct {
	SpaceID  string `json:"spaceId,omitempty"`
	ParentID string `json:"parentId,omitempty"`
	Name     string `json:"name,omitempty"`
}

// FileQuery filters a file listing. Type is a MIME prefix such as "image".
// Sort is name, size or updated.
type FileQuery struct {
	Type   string `json:"type,omitempty"`
	Sort   string `json:"sort,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
}

// ResolveRequest asks how link targets written in a note resolve.
type ResolveRequest struct {
	SpaceID    string   `json:"spaceId"`
	FromNoteID string   `json:"fromNoteId,omitempty"`
	Targets    []string `json:"targets"`
}

// ResolvedTarget is the answer for one target. Kind, ID, Path and URL are
// empty when the target names nothing the actor may view.
type ResolvedTarget struct {
	Target string   `json:"target"`
	Kind   ItemKind `json:"kind,omitempty"`
	ID     string   `json:"id,omitempty"`
	Path   string   `json:"path,omitempty"`
	URL    string   `json:"url,omitempty"`
}
