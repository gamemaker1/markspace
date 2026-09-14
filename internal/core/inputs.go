package core

import "time"

// SignupInput creates an account.
type SignupInput struct {
	Email       string `json:"email"`
	Name        string `json:"name"`
	Password    string `json:"password"`
	InviteToken string `json:"inviteToken,omitempty"`
}

// ProfileInput updates the signed-in user. Nil fields stay unchanged.
type ProfileInput struct {
	Name        *string `json:"name,omitempty"`
	Email       *string `json:"email,omitempty"`
	Locale      *string `json:"locale,omitempty"`
	Timezone    *string `json:"timezone,omitempty"`
	AvatarColor *string `json:"avatarColor,omitempty"`
}

// UserInput creates or updates a user as an admin.
type UserInput struct {
	Email    *string `json:"email,omitempty"`
	Name     *string `json:"name,omitempty"`
	Password *string `json:"password,omitempty"`
	IsAdmin  *bool   `json:"isAdmin,omitempty"`
	IsActive *bool   `json:"isActive,omitempty"`
}

// InviteInput creates an invite.
type InviteInput struct {
	Email   string        `json:"email"`
	SpaceID string        `json:"spaceId,omitempty"`
	Role    Role          `json:"role,omitempty"`
	IsAdmin bool          `json:"isAdmin,omitempty"`
	TTL     time.Duration `json:"ttl,omitempty"`
}

// SpaceInput creates or updates a space. Nil fields stay unchanged.
type SpaceInput struct {
	Name        *string `json:"name,omitempty"`
	Slug        *string `json:"slug,omitempty"`
	Description *string `json:"description,omitempty"`
	Icon        *string `json:"icon,omitempty"`
	// PasteFolder names the folder, relative to a note's folder, that files
	// pasted into the note go to. An empty value keeps them next to the note.
	PasteFolder *string `json:"pasteFolder,omitempty"`
}

// NoteInput creates a note. ParentID names a folder, or a note that is not a
// folder note. A note parent becomes the folder note of a new folder with its
// name, and the new note goes into that folder.
type NoteInput struct {
	// ID is optional. An offline client supplies an id it generated, in the
	// shape ValidID accepts, so replaying the create is idempotent.
	ID       string   `json:"id,omitempty"`
	SpaceID  string   `json:"spaceId"`
	ParentID string   `json:"parentId,omitempty"`
	Title    string   `json:"title"`
	Content  string   `json:"content,omitempty"`
	Icon     string   `json:"icon,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Position int      `json:"position,omitempty"`
	// FolderNote creates the folder note <folder>/index.md of the folder
	// named by FolderID. The folder must have no folder note yet.
	FolderNote bool `json:"folderNote,omitempty"`
	// FolderID names the folder of a folder note. It is used only with
	// FolderNote.
	FolderID string `json:"folderId,omitempty"`
	// Extra sets frontmatter keys the application does not own. The plugin
	// host API fills it from frontmatter. Owned keys are ignored.
	Extra map[string]any `json:"-"`
}

// NoteUpdate changes a note. Nil fields stay unchanged. BaseUpdatedAt enables
// optimistic locking: if it is non-zero and older than the stored timestamp,
// the call fails with ErrConflict.
type NoteUpdate struct {
	Title         *string    `json:"title,omitempty"`
	Content       *string    `json:"content,omitempty"`
	Icon          *string    `json:"icon,omitempty"`
	Tags          *[]string  `json:"tags,omitempty"`
	BaseUpdatedAt *time.Time `json:"baseUpdatedAt,omitempty"`
	// Snapshot forces a version snapshot even for a small edit.
	Snapshot bool `json:"snapshot,omitempty"`
	// Extra merges frontmatter keys the application does not own. A nil
	// value removes the key. Owned keys are ignored.
	Extra map[string]any `json:"-"`
}

// OwnedFrontmatter lists the frontmatter keys the application writes itself.
// Extra frontmatter may not set them.
var OwnedFrontmatter = []string{
	"id", "title", "icon", "tags", "position", "created", "updated", "author", "updated_by",
	"updated_via",
	"deleted", "deleted_at", "deleted_by", "trashed_from", "trash_path", "parent",
}

// NoteListOptions filters a note listing.
type NoteListOptions struct {
	ParentID     string `json:"parentId,omitempty"`
	HasParent    bool   `json:"hasParent,omitempty"` // honour ParentID even when empty
	Tag          string `json:"tag,omitempty"`
	IncludeTrash bool   `json:"includeTrash,omitempty"`
	OnlyTrash    bool   `json:"onlyTrash,omitempty"`
	WithContent  bool   `json:"withContent,omitempty"`
	Sort         string `json:"sort,omitempty"` // "position", "title", "updated", "created"
	Limit        int    `json:"limit,omitempty"`
	Offset       int    `json:"offset,omitempty"`
}

// CommentInput creates a comment.
type CommentInput struct {
	NoteID   string         `json:"noteId"`
	ParentID string         `json:"parentId,omitempty"`
	Body     string         `json:"body"`
	Anchor   *CommentAnchor `json:"anchor,omitempty"`
}

// ShareInput grants a user access to a note.
type ShareInput struct {
	NoteID          string `json:"noteId"`
	UserID          string `json:"userId,omitempty"`
	Email           string `json:"email,omitempty"`
	Access          Access `json:"access"`
	IncludeChildren bool   `json:"includeChildren,omitempty"`
}

// PublicLinkInput creates or updates a public link.
type PublicLinkInput struct {
	NoteID          string     `json:"noteId"`
	Access          Access     `json:"access"`
	Password        *string    `json:"password,omitempty"`
	ExpiresAt       *time.Time `json:"expiresAt,omitempty"`
	IncludeChildren bool       `json:"includeChildren,omitempty"`
}

// Heading is one entry of a rendered table of contents.
type Heading struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
	ID    string `json:"id"`
}

// WikiLink is one [[target]] reference found in a note body.
type WikiLink struct {
	Target string `json:"target"`
	Label  string `json:"label,omitempty"`
	// NoteID is filled in when the target resolved to a note.
	NoteID string `json:"noteId,omitempty"`
	// ItemID and Kind are filled in when the target resolved to any item.
	ItemID string   `json:"itemId,omitempty"`
	Kind   ItemKind `json:"kind,omitempty"`
}

// Rendered is the HTML form of a note body.
type Rendered struct {
	HTML      string     `json:"html"`
	TOC       []Heading  `json:"toc,omitempty"`
	Links     []WikiLink `json:"links,omitempty"`
	Tasks     int        `json:"tasks,omitempty"`
	TasksDone int        `json:"tasksDone,omitempty"`
}
