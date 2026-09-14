package core

import (
	"encoding/json"
	"strings"
	"time"
	"unicode"
)

// User is an account. PasswordHash is never serialized to clients.
type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	PasswordHash string    `json:"-"`
	IsAdmin      bool      `json:"isAdmin"`
	IsActive     bool      `json:"isActive"`
	Locale       string    `json:"locale,omitempty"`
	Timezone     string    `json:"timezone,omitempty"`
	AvatarColor  string    `json:"avatarColor,omitempty"`
	LastLoginAt  time.Time `json:"lastLoginAt,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// Initials returns up to two uppercase letters for the avatar placeholder.
func (u *User) Initials() string {
	src := strings.TrimSpace(u.Name)
	if src == "" {
		src = u.Email
	}
	var out []rune
	for _, part := range strings.FieldsFunc(src, func(r rune) bool {
		return r == ' ' || r == '.' || r == '-' || r == '_' || r == '@'
	}) {
		out = append(out, unicode.ToUpper([]rune(part)[0]))
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return "?"
	}
	return string(out)
}

// Session is a browser login session stored server side.
type Session struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId"`
	TokenHash string    `json:"-"`
	UserAgent string    `json:"userAgent,omitempty"`
	IP        string    `json:"ip,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	LastSeen  time.Time `json:"lastSeen"`
}

// APIToken is a long-lived bearer credential for scripts. A system token,
// such as the Markspace CLI token, has an empty UserID and authenticates as
// the system actor.
type APIToken struct {
	ID        string     `json:"id"`
	UserID    string     `json:"userId"`
	Name      string     `json:"name"`
	TokenHash string     `json:"-"`
	Prefix    string     `json:"prefix"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	LastUsed  *time.Time `json:"lastUsed,omitempty"`
}

// IsSystem reports whether the token belongs to no user.
func (t *APIToken) IsSystem() bool { return t != nil && t.UserID == "" }

// Invite lets an admin add a user without open signup.
type Invite struct {
	ID        string     `json:"id"`
	Email     string     `json:"email"`
	Token     string     `json:"token"`
	SpaceID   string     `json:"spaceId,omitempty"`
	Role      Role       `json:"role,omitempty"`
	IsAdmin   bool       `json:"isAdmin"`
	CreatedBy string     `json:"createdBy"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt time.Time  `json:"expiresAt"`
	UsedAt    *time.Time `json:"usedAt,omitempty"`
}

// Space is a top-level container. Each space is one directory on disk.
type Space struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
	// PasteFolder is the folder, relative to a note's folder, that pasted
	// files go to. Empty keeps them next to the note.
	PasteFolder string    `json:"pasteFolder,omitempty"`
	CreatedBy   string    `json:"createdBy"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`

	// Populated by list queries for the current actor.
	MyRole    Role `json:"myRole,omitempty"`
	NoteCount int  `json:"noteCount,omitempty"`
}

// SpaceMember grants a user a role inside one space.
type SpaceMember struct {
	SpaceID   string    `json:"spaceId"`
	UserID    string    `json:"userId"`
	Role      Role      `json:"role"`
	CreatedAt time.Time `json:"createdAt"`

	// Populated by list queries.
	User *User `json:"user,omitempty"`
}

// Note is one markdown file. Content excludes the frontmatter block.
type Note struct {
	ID       string   `json:"id"`
	SpaceID  string   `json:"spaceId"`
	ParentID string   `json:"parentId,omitempty"`
	Slug     string   `json:"slug"`
	Path     string   `json:"path"`
	Title    string   `json:"title"`
	Icon     string   `json:"icon,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Content  string   `json:"content,omitempty"`
	Excerpt  string   `json:"excerpt,omitempty"`
	Position int      `json:"position"`
	Words    int      `json:"words"`
	// Rev is the index revision of the last change to this note. It orders
	// the sync change feed.
	Rev       int64     `json:"rev,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	CreatedBy string    `json:"createdBy,omitempty"`
	UpdatedBy string    `json:"updatedBy,omitempty"`
	// UpdatedVia is the plugin id when UpdatedBy made the last change through
	// a plugin acting for them.
	UpdatedVia string `json:"updatedVia,omitempty"`

	Deleted   bool       `json:"deleted"`
	DeletedAt *time.Time `json:"deletedAt,omitempty"`
	DeletedBy string     `json:"deletedBy,omitempty"`

	// Extra frontmatter keys the app does not own, written back verbatim.
	Extra map[string]any `json:"extra,omitempty"`

	// Populated by some queries for display.
	SpaceSlug string `json:"spaceSlug,omitempty"`
	Starred   bool   `json:"starred,omitempty"`
}

// NoteVersion is a snapshot of a note taken before an edit.
type NoteVersion struct {
	ID       string `json:"id"`
	NoteID   string `json:"noteId"`
	SpaceID  string `json:"spaceId"`
	Title    string `json:"title"`
	Content  string `json:"content,omitempty"`
	Size     int    `json:"size"`
	AuthorID string `json:"authorId,omitempty"`
	// Via is the plugin id when the author wrote this version through a
	// plugin acting for them.
	Via       string    `json:"via,omitempty"`
	Label     string    `json:"label,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// CommentAnchor ties a comment to a passage of the note body. Offsets count
// UTF-16 code units in the body without frontmatter, the unit browsers and
// CodeMirror use. Exact is the selected text. Prefix and Suffix hold up to 32
// code units on either side, used to pick the right occurrence when the text
// repeats or moved. docs/comments.md specifies how an anchor is resolved.
type CommentAnchor struct {
	Exact  string `json:"exact"`
	Prefix string `json:"prefix,omitempty"`
	Suffix string `json:"suffix,omitempty"`
	Start  int    `json:"start"`
	End    int    `json:"end"`
}

// Anchor states returned with a listed comment.
const (
	// AnchorExact means the text is still at the stored offsets.
	AnchorExact = "exact"
	// AnchorMoved means the text was found elsewhere in the body.
	AnchorMoved = "moved"
	// AnchorOrphaned means the text no longer appears in the body.
	AnchorOrphaned = "orphaned"
)

// Comment is a thread entry on a note. ParentID empty means a root comment.
type Comment struct {
	ID       string `json:"id"`
	NoteID   string `json:"noteId"`
	SpaceID  string `json:"spaceId"`
	UserID   string `json:"userId"`
	ParentID string `json:"parentId,omitempty"`
	Body     string `json:"body"`
	// Anchor is set on a root comment made on a selection. Replies carry none.
	Anchor *CommentAnchor `json:"anchor,omitempty"`
	// AnchorState is set by listing: exact, moved or orphaned. When the text
	// moved, Anchor holds the offsets in the current body.
	AnchorState string    `json:"anchorState,omitempty"`
	Resolved    bool      `json:"resolved"`
	ResolvedBy  string    `json:"resolvedBy,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	// EditedAt is the time of the last body edit. Creating, resolving,
	// reopening, re-anchoring and deleting leave it unchanged, so a client
	// shows "edited" from it and not from UpdatedAt.
	EditedAt  *time.Time `json:"editedAt,omitempty"`
	DeletedAt *time.Time `json:"deletedAt,omitempty"`

	Author   *User      `json:"author,omitempty"`
	Children []*Comment `json:"children,omitempty"`
}

// Share grants one user access to one note, independent of space membership.
type Share struct {
	ID              string    `json:"id"`
	NoteID          string    `json:"noteId"`
	SpaceID         string    `json:"spaceId"`
	UserID          string    `json:"userId"`
	Access          Access    `json:"access"`
	IncludeChildren bool      `json:"includeChildren"`
	CreatedBy       string    `json:"createdBy"`
	CreatedAt       time.Time `json:"createdAt"`

	User *User `json:"user,omitempty"`
	Note *Note `json:"note,omitempty"`
}

// PublicLink grants anonymous access to one note through a secret token.
type PublicLink struct {
	ID              string     `json:"id"`
	Token           string     `json:"token"`
	NoteID          string     `json:"noteId"`
	SpaceID         string     `json:"spaceId"`
	Access          Access     `json:"access"`
	PasswordHash    string     `json:"-"`
	HasPassword     bool       `json:"hasPassword"`
	IncludeChildren bool       `json:"includeChildren"`
	ExpiresAt       *time.Time `json:"expiresAt,omitempty"`
	Views           int        `json:"views"`
	CreatedBy       string     `json:"createdBy"`
	CreatedAt       time.Time  `json:"createdAt"`
}

// Expired reports whether the link is past its expiry.
func (p *PublicLink) Expired(now time.Time) bool {
	return p.ExpiresAt != nil && now.After(*p.ExpiresAt)
}

// Backlink is a wikilink or markdown link pointing at a note.
type Backlink struct {
	FromNote *Note  `json:"fromNote"`
	Context  string `json:"context,omitempty"`
}

// TagCount is one row of the tag index.
type TagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

// SearchQuery describes a full-text or semantic search.
type SearchQuery struct {
	Text         string   `json:"text"`
	SpaceIDs     []string `json:"spaceIds,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	AuthorID     string   `json:"authorId,omitempty"`
	Mode         string   `json:"mode,omitempty"` // "text" (default), "semantic", "hybrid"
	IncludeTrash bool     `json:"includeTrash,omitempty"`
	Limit        int      `json:"limit,omitempty"`
	Offset       int      `json:"offset,omitempty"`
}

// SearchHit is one search result. Snippet contains HTML-escaped text with
// <mark> around matches.
type SearchHit struct {
	// Kind is note or file. Exactly one of Note and File is set.
	Kind    ItemKind `json:"kind"`
	Note    *Note    `json:"note,omitempty"`
	File    *File    `json:"file,omitempty"`
	Snippet string   `json:"snippet"`
	Score   float64  `json:"score"`
}

// Event is a server-sent change notification.
type Event struct {
	// ID identifies the event. Events published by the application get one
	// when they enter the module event bus.
	ID string `json:"id,omitempty"`
	// Type is note.created, note.updated, note.deleted, folder.created,
	// folder.updated, folder.deleted, file.created, file.updated,
	// file.deleted, item.moved, tree.changed, comment.created,
	// suggestion.created, suggestion.accepted, suggestion.discarded,
	// job.updated, plugin.notify or plugin.<id>.<name>.
	Type string `json:"type"`
	// OldPath is the previous path of a moved item.
	OldPath string `json:"oldPath,omitempty"`
	// Data carries type-specific fields, such as {suggestionId}.
	Data json.RawMessage `json:"data,omitempty"`
	// Audience, when set, limits the event to these user ids. It is not sent.
	Audience []string `json:"-"`
	SpaceID  string   `json:"spaceId,omitempty"`
	// NoteID is set on note events.
	NoteID string `json:"noteId,omitempty"`
	// ItemID and Kind name the item of any event about one item.
	ItemID  string   `json:"itemId,omitempty"`
	Kind    ItemKind `json:"kind,omitempty"`
	ActorID string   `json:"actorId,omitempty"`
	Path    string   `json:"path,omitempty"`
	At      int64    `json:"at"`
}

// Settings holds instance configuration stored in the database and editable
// by an admin at runtime.
type Settings struct {
	InstanceName    string `json:"instanceName"`
	AllowSignup     bool   `json:"allowSignup"`
	DefaultSpaceID  string `json:"defaultSpaceId,omitempty"`
	AIEnabled       bool   `json:"aiEnabled"`
	AIAllowUserKeys bool   `json:"aiAllowUserKeys"`
	// AIAllowUserRoutes lets users choose their own models per task.
	AIAllowUserRoutes bool `json:"aiAllowUserRoutes"`
	// AIMonthlyTokenBudget and AIMonthlyCostBudget are the default monthly
	// limits of every user. 0 means no limit. Only admins see them.
	AIMonthlyTokenBudget int64     `json:"aiMonthlyTokenBudget,omitempty"`
	AIMonthlyCostBudget  float64   `json:"aiMonthlyCostBudget,omitempty"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

// Stats is the admin dashboard summary.
type Stats struct {
	Users     int       `json:"users"`
	Spaces    int       `json:"spaces"`
	Notes     int       `json:"notes"`
	Files     int       `json:"files"`
	Comments  int       `json:"comments"`
	DiskBytes int64     `json:"diskBytes"`
	IndexedAt time.Time `json:"indexedAt"`
}
