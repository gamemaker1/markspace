package core

import (
	"context"
	"io"
	"time"
)

// AuthService handles credentials, sessions and API tokens.
type AuthService interface {
	// Signup creates an account. The first account on a fresh instance becomes
	// an admin. Later accounts need either open signup or a valid invite.
	Signup(ctx context.Context, in SignupInput) (*User, error)
	// Login verifies a password and returns the plaintext session token.
	Login(ctx context.Context, email, password, userAgent, ip string) (*User, string, error)
	// Logout deletes the session behind the plaintext token.
	Logout(ctx context.Context, sessionToken string) error
	// ActorFromSession resolves a session cookie. It returns ErrUnauthorized
	// for an unknown or expired token.
	ActorFromSession(ctx context.Context, sessionToken string) (*Actor, error)
	// ActorFromAPIToken resolves a bearer token.
	ActorFromAPIToken(ctx context.Context, token string) (*Actor, error)

	ChangePassword(ctx context.Context, actor *Actor, oldPassword, newPassword string) error
	UpdateProfile(ctx context.Context, actor *Actor, in ProfileInput) (*User, error)

	ListSessions(ctx context.Context, actor *Actor) ([]*Session, error)
	RevokeSession(ctx context.Context, actor *Actor, sessionID string) error

	// CreateAPIToken returns the record and the plaintext token, which is shown
	// once and never stored.
	CreateAPIToken(ctx context.Context, actor *Actor, name string, ttl time.Duration) (*APIToken, string, error)
	ListAPITokens(ctx context.Context, actor *Actor) ([]*APIToken, error)
	RevokeAPIToken(ctx context.Context, actor *Actor, id string) error

	// NeedsSetup reports whether the instance has no users yet.
	NeedsSetup(ctx context.Context) (bool, error)
}

// UserService manages accounts and invites. Every method requires an admin
// actor unless noted.
type UserService interface {
	ListUsers(ctx context.Context, actor *Actor, query string, limit, offset int) ([]*User, int, error)
	GetUser(ctx context.Context, actor *Actor, id string) (*User, error)
	// LookupUsers finds users by email or name prefix. Any signed-in user may
	// call it, to pick share recipients.
	LookupUsers(ctx context.Context, actor *Actor, query string, limit int) ([]*User, error)
	CreateUser(ctx context.Context, actor *Actor, in UserInput) (*User, error)
	UpdateUser(ctx context.Context, actor *Actor, id string, in UserInput) (*User, error)
	DeleteUser(ctx context.Context, actor *Actor, id string) error

	CreateInvite(ctx context.Context, actor *Actor, in InviteInput) (*Invite, error)
	ListInvites(ctx context.Context, actor *Actor) ([]*Invite, error)
	RevokeInvite(ctx context.Context, actor *Actor, id string) error
}

// SpaceService manages spaces and their membership.
type SpaceService interface {
	// ListSpaces returns every space the actor may view, with MyRole set.
	ListSpaces(ctx context.Context, actor *Actor) ([]*Space, error)
	// GetSpace accepts an id or a slug.
	GetSpace(ctx context.Context, actor *Actor, idOrSlug string) (*Space, error)
	CreateSpace(ctx context.Context, actor *Actor, in SpaceInput) (*Space, error)
	UpdateSpace(ctx context.Context, actor *Actor, id string, in SpaceInput) (*Space, error)
	// DeleteSpace removes the space record and its notes from the index. The
	// directory on disk moves to <data>/deleted/<slug>-<timestamp>.
	DeleteSpace(ctx context.Context, actor *Actor, id string) error

	ListMembers(ctx context.Context, actor *Actor, spaceID string) ([]*SpaceMember, error)
	AddMember(ctx context.Context, actor *Actor, spaceID, userID string, role Role) (*SpaceMember, error)
	UpdateMember(ctx context.Context, actor *Actor, spaceID, userID string, role Role) (*SpaceMember, error)
	RemoveMember(ctx context.Context, actor *Actor, spaceID, userID string) error

	// SpaceAccess resolves what the actor may do in one space.
	SpaceAccess(ctx context.Context, actor *Actor, spaceID string) (*Grant, error)
}

// NoteService manages notes, the page tree, trash, stars and versions.
type NoteService interface {
	ListNotes(ctx context.Context, actor *Actor, spaceID string, opts NoteListOptions) ([]*Note, error)
	// GetNote returns the note with content and the actor's resolved access.
	GetNote(ctx context.Context, actor *Actor, id string) (*Note, *Grant, error)
	// GetNoteByPath resolves <spaceSlug>/<path> where path omits the .md suffix.
	GetNoteByPath(ctx context.Context, actor *Actor, spaceSlug, path string) (*Note, *Grant, error)

	CreateNote(ctx context.Context, actor *Actor, in NoteInput) (*Note, error)
	UpdateNote(ctx context.Context, actor *Actor, id string, in NoteUpdate) (*Note, error)
	MoveNote(ctx context.Context, actor *Actor, id, newParentID string, position int) (*Note, error)
	DuplicateNote(ctx context.Context, actor *Actor, id string) (*Note, error)

	TrashNote(ctx context.Context, actor *Actor, id string) error
	RestoreNote(ctx context.Context, actor *Actor, id string) error
	DeleteNote(ctx context.Context, actor *Actor, id string) error
	ListTrash(ctx context.Context, actor *Actor, spaceID string) ([]*Note, error)
	EmptyTrash(ctx context.Context, actor *Actor, spaceID string) error

	SetStar(ctx context.Context, actor *Actor, noteID string, starred bool) error
	ListStarred(ctx context.Context, actor *Actor) ([]*Note, error)
	RecentNotes(ctx context.Context, actor *Actor, limit int) ([]*Note, error)

	Backlinks(ctx context.Context, actor *Actor, noteID string) ([]*Backlink, error)
	ListTags(ctx context.Context, actor *Actor, spaceID string) ([]*TagCount, error)

	ListVersions(ctx context.Context, actor *Actor, noteID string) ([]*NoteVersion, error)
	GetVersion(ctx context.Context, actor *Actor, noteID, versionID string) (*NoteVersion, error)
	// DiffVersion returns an HTML line diff from the version to the current body.
	DiffVersion(ctx context.Context, actor *Actor, noteID, versionID string) (string, error)
	RestoreVersion(ctx context.Context, actor *Actor, noteID, versionID string) (*Note, error)
}

// ItemService manages folders, files and the operations that work on items
// of any kind. docs/files.md specifies the rules.
type ItemService interface {
	// SpaceTree returns every item of a space the actor may view, nested by
	// folder. A folder note appears as the folder's IndexNoteID, not as a
	// child row.
	SpaceTree(ctx context.Context, actor *Actor, spaceID string) ([]*TreeItem, error)
	// GetItem returns an item of any kind with the actor's access.
	GetItem(ctx context.Context, actor *Actor, id string) (*Item, error)
	// GetItemByPath resolves an entry path in a space given by id or slug.
	GetItemByPath(ctx context.Context, actor *Actor, space, relPath string) (*Item, error)
	// ListTrashItems lists the trashed items of a space that head their own
	// trash entry.
	ListTrashItems(ctx context.Context, actor *Actor, spaceID string) ([]*TreeItem, error)

	CreateFolder(ctx context.Context, actor *Actor, in FolderInput) (*Folder, error)
	// GetFolder returns the folder with its direct children.
	GetFolder(ctx context.Context, actor *Actor, id string) (*TreeItem, *Grant, error)
	RenameFolder(ctx context.Context, actor *Actor, id, name string) (*Folder, error)

	// UploadFile streams r into a new file.
	UploadFile(ctx context.Context, actor *Actor, in FileUpload, r io.Reader) (*File, error)
	GetFile(ctx context.Context, actor *Actor, id string) (*File, *Grant, error)
	ListFiles(ctx context.Context, actor *Actor, spaceID string, q FileQuery) ([]*File, error)
	RenameFile(ctx context.Context, actor *Actor, id, name string) (*File, error)
	// ReplaceFile writes new content and keeps the old content as a version.
	ReplaceFile(ctx context.Context, actor *Actor, id string, in FileReplace, r io.Reader) (*File, error)
	ListFileVersions(ctx context.Context, actor *Actor, id string) ([]*FileVersion, error)
	RestoreFileVersion(ctx context.Context, actor *Actor, id, versionID string) (*File, error)
	// OpenFile opens the current content, or a version when versionID is set.
	// The caller closes the reader.
	OpenFile(ctx context.Context, actor *Actor, id, versionID string) (*File, *FileVersion, io.ReadSeekCloser, error)
	// OpenThumbnail opens a thumbnail at most width pixels wide and returns
	// its media type. The caller closes the reader.
	OpenThumbnail(ctx context.Context, actor *Actor, id string, width int) (*File, io.ReadSeekCloser, string, error)

	// ItemBacklinks lists the notes linking to an item of any kind.
	ItemBacklinks(ctx context.Context, actor *Actor, id string) ([]*Backlink, error)
	// MoveItem moves an item to another folder of its space and rewrites
	// links. A zero position appends.
	MoveItem(ctx context.Context, actor *Actor, id, parentID string, position int) (*Item, error)
	// CopyItem copies an item, recursively for a folder, to any space.
	CopyItem(ctx context.Context, actor *Actor, id string, in CopyInput) (*Item, error)
	TrashItem(ctx context.Context, actor *Actor, id string) error
	RestoreItem(ctx context.Context, actor *Actor, id string) (*Item, error)
	// DeleteItem removes an item for good.
	DeleteItem(ctx context.Context, actor *Actor, id string) error
	// ResolveLinks resolves link targets as a note in the space would.
	ResolveLinks(ctx context.Context, actor *Actor, in ResolveRequest) ([]*ResolvedTarget, error)
}

// CommentService manages comment threads. The note id of a listing or of
// CommentInput names an item of any kind. Comments on folders and files
// carry no anchor.
type CommentService interface {
	ListComments(ctx context.Context, actor *Actor, noteID string) ([]*Comment, error)
	CreateComment(ctx context.Context, actor *Actor, in CommentInput) (*Comment, error)
	UpdateComment(ctx context.Context, actor *Actor, id, body string) (*Comment, error)
	DeleteComment(ctx context.Context, actor *Actor, id string) error
	ResolveComment(ctx context.Context, actor *Actor, id string, resolved bool) (*Comment, error)
}

// ShareService manages grants and public links. The note ids of the listings
// and of ShareInput and PublicLinkInput name an item of any kind.
type ShareService interface {
	ListShares(ctx context.Context, actor *Actor, noteID string) ([]*Share, error)
	CreateShare(ctx context.Context, actor *Actor, in ShareInput) (*Share, error)
	UpdateShare(ctx context.Context, actor *Actor, id string, access Access, includeChildren bool) (*Share, error)
	DeleteShare(ctx context.Context, actor *Actor, id string) error
	// ListSharedWithMe returns notes shared directly with the actor.
	ListSharedWithMe(ctx context.Context, actor *Actor) ([]*Share, error)

	ListPublicLinks(ctx context.Context, actor *Actor, noteID string) ([]*PublicLink, error)
	CreatePublicLink(ctx context.Context, actor *Actor, in PublicLinkInput) (*PublicLink, error)
	UpdatePublicLink(ctx context.Context, actor *Actor, id string, in PublicLinkInput) (*PublicLink, error)
	DeletePublicLink(ctx context.Context, actor *Actor, id string) error

	// ResolvePublicLink validates a token and optional password, and returns an
	// anonymous actor scoped to that link plus the target note. It returns
	// ErrUnauthorized when a password is required or wrong.
	ResolvePublicLink(ctx context.Context, token, password string) (*Actor, *Note, error)
	// ResolvePublicItem is ResolvePublicLink for a link on an item of any
	// kind. A note item carries its body.
	ResolvePublicItem(ctx context.Context, token, password string) (*Actor, *Item, error)
}

// SearchService answers full-text and semantic queries.
type SearchService interface {
	Search(ctx context.Context, actor *Actor, q SearchQuery) ([]*SearchHit, error)
	// Suggest powers wikilink and quick-open autocomplete.
	Suggest(ctx context.Context, actor *Actor, prefix string, limit int) ([]*Note, error)
}

// RenderService turns markdown into HTML with space-scoped link resolution.
type RenderService interface {
	// RenderMarkdown renders a fragment. spaceID may be empty.
	RenderMarkdown(ctx context.Context, actor *Actor, spaceID, md string) (*Rendered, error)
	// RenderNote renders a stored note and resolves its wikilinks.
	RenderNote(ctx context.Context, actor *Actor, note *Note) (*Rendered, error)
}

// AIService exposes model providers and the canned editor tasks.
type AIService interface {
	// ListAIProviders returns providers visible to the actor. API keys are
	// stripped and HasAPIKey is set instead.
	ListAIProviders(ctx context.Context, actor *Actor) ([]*AIProviderConfig, error)
	SaveAIProvider(ctx context.Context, actor *Actor, cfg *AIProviderConfig) (*AIProviderConfig, error)
	DeleteAIProvider(ctx context.Context, actor *Actor, id string) error
	TestAIProvider(ctx context.Context, actor *Actor, id string) error
	// RefreshAIProvider lists the provider's models again and returns its
	// catalog rows. Admin, or the owner of a personal provider.
	RefreshAIProvider(ctx context.Context, actor *Actor, id string) ([]*AIModel, error)
	// ListAIModels returns the catalog rows the actor may use, with the
	// effective price and its source. Admins see disabled and unavailable
	// rows too.
	ListAIModels(ctx context.Context, actor *Actor, q AIModelQuery) ([]*AIModel, error)
	// UpdateAIModel enables or disables a model or sets its price override.
	// Admin only.
	UpdateAIModel(ctx context.Context, actor *Actor, providerID, model string, in AIModelUpdate) (*AIModel, error)
	// ListAIRoutes returns the instance routes, and the actor's own when user
	// routes are allowed.
	ListAIRoutes(ctx context.Context, actor *Actor) (*AIRoutes, error)
	// SaveAIRoute replaces the targets of one route. scope is "instance",
	// admin only, or "me" for the actor's own route.
	SaveAIRoute(ctx context.Context, actor *Actor, scope, task string, targets []AIRouteTarget) (*AIRoute, error)
	// DeleteAIRoute removes the actor's own route for a task.
	DeleteAIRoute(ctx context.Context, actor *Actor, task string) error
	// ListAIPresets returns the provider preset table.
	ListAIPresets(ctx context.Context, actor *Actor) ([]AIPreset, error)
	// RefreshAIPrices downloads the public price catalogs now and reprices
	// the catalog. Admin only.
	RefreshAIPrices(ctx context.Context, actor *Actor) (*AIPriceRefresh, error)

	RunAI(ctx context.Context, actor *Actor, req AIRequest) (*AIResult, error)
	// StreamAI returns a channel closed after a chunk with Done or Err set.
	StreamAI(ctx context.Context, actor *Actor, req AIRequest) (<-chan AIChunk, error)

	// ReindexEmbeddings rebuilds the semantic index. An empty spaceID covers
	// every space the actor administers.
	ReindexEmbeddings(ctx context.Context, actor *Actor, spaceID string) (int, error)

	// MyAIUsage reports the actor's own usage. UserID is forced to the actor.
	MyAIUsage(ctx context.Context, actor *Actor, q AIUsageQuery) (*AIUsageReport, error)
	// AIUsage reports usage across the instance. Admin only.
	AIUsage(ctx context.Context, actor *Actor, q AIUsageQuery) (*AIUsageReport, error)
	// AIUsageCalls lists raw call records ordered by sort: recent, slowest or
	// failed. An admin may list any calls, a user only calls with q.UserID
	// set to themselves.
	AIUsageCalls(ctx context.Context, actor *Actor, q AIUsageQuery, sort string) ([]*AIUsageRecord, error)
	// AIUsageSummary returns the actor's usage today and this month, with
	// their budget status.
	AIUsageSummary(ctx context.Context, actor *Actor) (*AIUsageSummary, error)

	// ListAIBudgets returns every budget override with its status. Admin only.
	ListAIBudgets(ctx context.Context, actor *Actor) ([]*AIBudget, error)
	// SaveAIBudget creates or replaces the override of one subject. Admin only.
	SaveAIBudget(ctx context.Context, actor *Actor, b AIBudget) (*AIBudget, error)
	// DeleteAIBudget removes an override. Admin only.
	DeleteAIBudget(ctx context.Context, actor *Actor, kind, id string) error
	// LiftAIBudget lets a subject call past its budget until the end of the
	// current UTC month. Admin only.
	LiftAIBudget(ctx context.Context, actor *Actor, kind, id string) (*AIBudget, error)
}

// AdminService covers instance settings, statistics and bulk transfer.
type AdminService interface {
	GetSettings(ctx context.Context, actor *Actor) (*Settings, error)
	UpdateSettings(ctx context.Context, actor *Actor, in *Settings) (*Settings, error)
	Stats(ctx context.Context, actor *Actor) (*Stats, error)
	// Reindex rereads every markdown file into the index.
	Reindex(ctx context.Context, actor *Actor) (int, error)
	// ListSystemTokens returns the API tokens that belong to no user, such
	// as the Markspace CLI token. Admin only.
	ListSystemTokens(ctx context.Context, actor *Actor) ([]*APIToken, error)
	// RevokeSystemToken deletes one system API token. Admin only.
	RevokeSystemToken(ctx context.Context, actor *Actor, id string) error

	// ExportSpace writes a zip of the notes, folders and files of a space.
	ExportSpace(ctx context.Context, actor *Actor, spaceID string, w io.Writer) error
	// ImportSpace reads a zip or a single markdown file into a space and
	// returns the number of notes and files written.
	ImportSpace(ctx context.Context, actor *Actor, spaceID string, r io.ReaderAt, size int64) (int, error)

	// Subscribe returns a channel of change events the actor may see. The
	// returned function unsubscribes and closes the channel.
	Subscribe(ctx context.Context, actor *Actor) (<-chan Event, func(), error)
}

// Backend is the whole application surface. The HTTP layers depend only on
// this interface.
type Backend interface {
	AuthService
	UserService
	SpaceService
	NoteService
	ItemService
	CommentService
	ShareService
	SearchService
	RenderService
	AIService
	AIThreadService
	AIImageService
	AdminService
	SyncService
	SuggestionService
	PluginSupportService
}
