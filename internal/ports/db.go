package ports

import (
	"context"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// IndexState is the recorded file state of one indexed note, used to skip
// unchanged files during a reindex.
type IndexState struct {
	NoteID  string
	ModTime time.Time
	Size    int64
}

// NoteRow carries the derived fields stored alongside a note during indexing.
// LinkRefs, when not nil, replaces Links. Links holds bare targets that the
// index resolves against note titles, slugs and paths, then file and folder
// names.
type NoteRow struct {
	Note     *core.Note
	Plain    string
	ModTime  time.Time
	Size     int64
	Links    []string
	LinkRefs []NoteLink
}

// Link forms stored with each outgoing link.
const (
	LinkWikilink      = "wikilink"
	LinkPathWikilink  = "path-wikilink"
	LinkMarkdown      = "md-link"
	LinkMarkdownImage = "md-image"
)

// NoteLink is one outgoing link of a note.
//
// Target is the link target without its #heading and |label. For the two
// markdown forms the caller passes the target as written, and the index
// stores the percent-decoded entry path from the space root. For the
// wikilink forms the index stores the target without a .md extension.
//
// TargetID is the resolved item id. When it is empty the index resolves the
// target itself. TargetKind is filled by the index from the item found.
type NoteLink struct {
	Target     string
	Form       string
	Embed      bool
	Heading    string
	TargetID   string
	TargetKind core.ItemKind
}

// SpaceLink is one stored outbound link with the path of its note.
type SpaceLink struct {
	NoteID   string
	NotePath string
	NoteLink
}

// ShareGrant is one row of the actor's direct grants, used to resolve access
// without a query per item. NotePath holds the path of the target, whatever
// its kind.
type ShareGrant struct {
	NoteID          string
	SpaceID         string
	NotePath        string
	Access          core.Access
	IncludeChildren bool
	Kind            core.ItemKind
}

// FileTextState is the text extraction state of a file.
type FileTextState string

// File text extraction states.
const (
	TextNone    FileTextState = "none"
	TextPending FileTextState = "pending"
	TextDone    FileTextState = "done"
	TextFailed  FileTextState = "failed"
	TextSkipped FileTextState = "skipped"
)

// FileListOptions filters ListFiles. ParentID with HasParent set, or a
// non-empty ParentID, restricts the list to one folder, where "" is the space
// root. Type is a MIME prefix such as "image/". Sort is name, size or
// updated.
type FileListOptions struct {
	ParentID       string
	HasParent      bool
	Type           string
	Sort           string
	Limit          int
	Offset         int
	IncludeDeleted bool
}

// FileHit is one file search result. Snippet is HTML-escaped text with
// <mark> around matches.
type FileHit struct {
	File    *core.File
	Snippet string
	Score   float64
}

// TreeRows holds the live items of one space as flat lists sorted by path.
// Notes carry no content.
type TreeRows struct {
	Folders []*core.Folder
	Notes   []*core.Note
	Files   []*core.File
}

// DB is the index database. Markdown files stay the source of truth for note
// bodies. Everything here is either derived from them or app state that has
// no file representation.
type DB interface {
	Close() error
	Ping(ctx context.Context) error

	// Users.
	CreateUser(ctx context.Context, u *core.User) error
	UpdateUser(ctx context.Context, u *core.User) error
	DeleteUser(ctx context.Context, id string) error
	GetUser(ctx context.Context, id string) (*core.User, error)
	GetUserByEmail(ctx context.Context, email string) (*core.User, error)
	ListUsers(ctx context.Context, query string, limit, offset int) ([]*core.User, int, error)
	CountUsers(ctx context.Context) (int, error)
	TouchUserLogin(ctx context.Context, id string, at time.Time) error

	// Sessions.
	CreateSession(ctx context.Context, s *core.Session) error
	GetSessionByHash(ctx context.Context, tokenHash string) (*core.Session, error)
	TouchSession(ctx context.Context, id string, at time.Time) error
	DeleteSession(ctx context.Context, id string) error
	DeleteSessionByHash(ctx context.Context, tokenHash string) error
	ListSessions(ctx context.Context, userID string) ([]*core.Session, error)
	PurgeExpiredSessions(ctx context.Context, now time.Time) (int, error)

	// API tokens.
	CreateAPIToken(ctx context.Context, t *core.APIToken) error
	GetAPITokenByHash(ctx context.Context, tokenHash string) (*core.APIToken, error)
	ListAPITokens(ctx context.Context, userID string) ([]*core.APIToken, error)
	DeleteAPIToken(ctx context.Context, id, userID string) error
	// ListSystemAPITokens returns the tokens that belong to no user.
	ListSystemAPITokens(ctx context.Context) ([]*core.APIToken, error)
	// DeleteSystemAPIToken removes one token that belongs to no user.
	DeleteSystemAPIToken(ctx context.Context, id string) error
	TouchAPIToken(ctx context.Context, id string, at time.Time) error

	// Invites.
	CreateInvite(ctx context.Context, in *core.Invite) error
	GetInviteByToken(ctx context.Context, token string) (*core.Invite, error)
	ListInvites(ctx context.Context) ([]*core.Invite, error)
	DeleteInvite(ctx context.Context, id string) error
	MarkInviteUsed(ctx context.Context, id string, at time.Time) error

	// Spaces.
	CreateSpace(ctx context.Context, s *core.Space) error
	UpdateSpace(ctx context.Context, s *core.Space) error
	DeleteSpace(ctx context.Context, id string) error
	GetSpace(ctx context.Context, id string) (*core.Space, error)
	GetSpaceBySlug(ctx context.Context, slug string) (*core.Space, error)
	ListSpaces(ctx context.Context) ([]*core.Space, error)
	// ListSpacesForUser returns spaces the user belongs to, with MyRole and
	// NoteCount filled in.
	ListSpacesForUser(ctx context.Context, userID string) ([]*core.Space, error)
	CountSpaces(ctx context.Context) (int, error)

	// Space membership.
	AddMember(ctx context.Context, m *core.SpaceMember) error
	UpdateMember(ctx context.Context, spaceID, userID string, role core.Role) error
	RemoveMember(ctx context.Context, spaceID, userID string) error
	GetMember(ctx context.Context, spaceID, userID string) (*core.SpaceMember, error)
	ListMembers(ctx context.Context, spaceID string) ([]*core.SpaceMember, error)
	// ListMembershipsForUser returns every space role the user holds.
	ListMembershipsForUser(ctx context.Context, userID string) ([]*core.SpaceMember, error)

	// Notes. UpsertNote writes the note row, its tags, its outgoing links and
	// its full-text entry in one transaction, keyed on note id.
	UpsertNote(ctx context.Context, row NoteRow) error
	GetNote(ctx context.Context, id string) (*core.Note, error)
	GetNoteByPath(ctx context.Context, spaceID, relPath string) (*core.Note, error)
	ListNotes(ctx context.Context, spaceID string, opts core.NoteListOptions) ([]*core.Note, error)
	// ListNotesByIDs preserves the order of ids that exist.
	ListNotesByIDs(ctx context.Context, ids []string) ([]*core.Note, error)
	DeleteNoteRow(ctx context.Context, id string) error
	// DeleteNoteRowsUnder removes index rows for a path prefix, used after a
	// subtree disappears from disk.
	DeleteNoteRowsUnder(ctx context.Context, spaceID, pathPrefix string) error
	SetNoteDeleted(ctx context.Context, id string, deleted bool, at *time.Time, by string) error
	SetNotePath(ctx context.Context, id, relPath, slug, parentID string) error
	// IndexState maps relPath to the recorded file state for one space.
	IndexState(ctx context.Context, spaceID string) (map[string]IndexState, error)
	CountNotes(ctx context.Context) (int, error)
	// NoteIDsInSpace lists ids of live notes, for embedding maintenance.
	NoteIDsInSpace(ctx context.Context, spaceID string) ([]string, error)

	// Note relations.
	Backlinks(ctx context.Context, noteID string) ([]*core.Backlink, error)
	// ResolveLink matches a wikilink target against note titles, slugs and
	// paths within one space.
	ResolveLink(ctx context.Context, spaceID, target string) (*core.Note, error)
	ListTags(ctx context.Context, spaceIDs []string) ([]*core.TagCount, error)

	// Search.
	SearchNotes(ctx context.Context, spaceIDs []string, q core.SearchQuery) ([]*core.SearchHit, error)
	SuggestNotes(ctx context.Context, spaceIDs []string, prefix string, limit int) ([]*core.Note, error)
	RecentNotes(ctx context.Context, spaceIDs []string, limit int) ([]*core.Note, error)

	// Stars.
	SetStar(ctx context.Context, userID, noteID string, starred bool) error
	IsStarred(ctx context.Context, userID, noteID string) (bool, error)
	ListStarred(ctx context.Context, userID string) ([]*core.Note, error)

	// Comments.
	CreateComment(ctx context.Context, c *core.Comment) error
	GetComment(ctx context.Context, id string) (*core.Comment, error)
	UpdateComment(ctx context.Context, c *core.Comment) error
	// UpdateCommentAnchor replaces a comment's anchor only while the stored
	// anchor still equals prev, and changes no other column, UpdatedAt
	// included. It reports whether it wrote. The anchor bookkeeping uses it so
	// a background write never undoes a concurrent edit or resolve.
	UpdateCommentAnchor(ctx context.Context, id string, prev, next *core.CommentAnchor) (bool, error)
	DeleteComment(ctx context.Context, id string) error
	ListComments(ctx context.Context, noteID string) ([]*core.Comment, error)
	CountComments(ctx context.Context) (int, error)

	// Per-note shares.
	CreateShare(ctx context.Context, s *core.Share) error
	GetShare(ctx context.Context, id string) (*core.Share, error)
	UpdateShare(ctx context.Context, s *core.Share) error
	DeleteShare(ctx context.Context, id string) error
	ListSharesForNote(ctx context.Context, noteID string) ([]*core.Share, error)
	ListSharesForUser(ctx context.Context, userID string) ([]*core.Share, error)
	FindShare(ctx context.Context, noteID, userID string) (*core.Share, error)
	// ShareGrantsForUser returns every direct grant with the note path, so the
	// caller can apply IncludeChildren by prefix.
	ShareGrantsForUser(ctx context.Context, userID string) ([]ShareGrant, error)

	// Public links.
	CreatePublicLink(ctx context.Context, l *core.PublicLink) error
	GetPublicLink(ctx context.Context, id string) (*core.PublicLink, error)
	GetPublicLinkByToken(ctx context.Context, token string) (*core.PublicLink, error)
	UpdatePublicLink(ctx context.Context, l *core.PublicLink) error
	DeletePublicLink(ctx context.Context, id string) error
	ListPublicLinks(ctx context.Context, noteID string) ([]*core.PublicLink, error)
	IncrementLinkViews(ctx context.Context, id string) error

	// Folders. Paths compare case-insensitively among live rows. A write
	// whose parent id does not name the live folder at the parent directory
	// of its path returns core.ErrInvalid. A path taken by a live folder,
	// note or file returns core.ErrExists. Every changed row takes the next
	// revision.
	CreateFolder(ctx context.Context, f *core.Folder) error
	GetFolder(ctx context.Context, id string) (*core.Folder, error)
	// GetFolderByPath returns the live folder at relPath.
	GetFolderByPath(ctx context.Context, spaceID, relPath string) (*core.Folder, error)
	// UpdateFolder writes name, path, parent, position, index note and
	// UpdatedAt. A changed path moves the subtree as MoveFolder does.
	UpdateFolder(ctx context.Context, f *core.Folder) error
	// MoveFolder sets the parent and path of a live folder and rewrites the
	// path of every folder, note and file below it in one transaction.
	MoveFolder(ctx context.Context, id, parentID, relPath string) error
	// TrashFolder marks the folder and every live item below it deleted with
	// the same time and actor.
	TrashFolder(ctx context.Context, id string, at time.Time, by string) error
	// RestoreFolder clears the deleted mark on the folder and on the items
	// trashed with it, and places the folder at relPath under parentID.
	RestoreFolder(ctx context.Context, id, parentID, relPath string) error
	// DeleteFolder removes the folder and the items below it that share its
	// state, with their comments, shares, public links and stars.
	DeleteFolder(ctx context.Context, id string) error
	ListFolders(ctx context.Context, spaceID string, includeDeleted bool) ([]*core.Folder, error)

	// Files. The same path, parent and revision rules as folders apply.
	CreateFile(ctx context.Context, f *core.File) error
	GetFile(ctx context.Context, id string) (*core.File, error)
	// GetFileByPath returns the live file at relPath.
	GetFileByPath(ctx context.Context, spaceID, relPath string) (*core.File, error)
	// UpdateFile writes every persisted field except the deleted fields and
	// CreatedAt/CreatedBy. A changed hash or size queues text extraction.
	UpdateFile(ctx context.Context, f *core.File) error
	SetFileHash(ctx context.Context, id, sha256 string, size int64, modTime time.Time) error
	// ListFilesNeedingHash lists live files with no stored hash.
	ListFilesNeedingHash(ctx context.Context, limit int) ([]*core.File, error)
	// FindFilesByHash lists live files in the space with this content.
	FindFilesByHash(ctx context.Context, spaceID, sha256 string, size int64) ([]*core.File, error)
	TrashFile(ctx context.Context, id string, at time.Time, by string) error
	RestoreFile(ctx context.Context, id, parentID, relPath string) error
	// DeleteFile removes the file row with its text, comments, shares and
	// public links. Links to it become unresolved.
	DeleteFile(ctx context.Context, id string) error
	ListFiles(ctx context.Context, spaceID string, opts FileListOptions) ([]*core.File, error)
	// CountFiles returns the number of live files.
	CountFiles(ctx context.Context) (int, error)
	// SetFileText stores extracted text and the extraction state.
	SetFileText(ctx context.Context, id, text string, state FileTextState) error
	// ListFilesNeedingText lists live files whose text state is pending.
	ListFilesNeedingText(ctx context.Context, limit int) ([]*core.File, error)
	SearchFiles(ctx context.Context, spaceIDs []string, q core.SearchQuery) ([]*FileHit, error)

	// Items of any kind.
	// ItemKind returns the kind and space of an id, core.ErrNotFound when no
	// folder, note or file has it.
	ItemKind(ctx context.Context, id string) (core.ItemKind, string, error)
	TreeItems(ctx context.Context, spaceID string) (*TreeRows, error)
	// SiblingNameTaken reports whether a live folder, note or file other than
	// exceptID has the name in the parent directory, compared with Unicode
	// case folding.
	SiblingNameTaken(ctx context.Context, spaceID, parentPath, name, exceptID string) (bool, error)
	// Ancestors returns the ids of the folders above relPath, nearest first.
	// A live folder wins over a trashed one at the same path.
	Ancestors(ctx context.Context, spaceID, relPath string) ([]string, error)
	// ItemsChangedSince returns folders, notes and files that are in spaceIDs
	// or have an id in itemIDs, with a revision in (since, upTo], merged in
	// revision order, at most limit. Trashed items are included.
	ItemsChangedSince(ctx context.Context, spaceIDs, itemIDs []string, since, upTo int64, limit int) ([]*core.TreeItem, error)
	// ItemIDsInSpaces lists every folder, note and file id in the spaces,
	// trashed items included, sorted.
	ItemIDsInSpaces(ctx context.Context, spaceIDs []string) ([]string, error)

	// Links of any kind.
	OutboundLinks(ctx context.Context, noteID string) ([]NoteLink, error)
	// SpaceLinks returns the stored outbound links of every live note in a
	// space, ordered by note path, for the rewrite candidate search.
	SpaceLinks(ctx context.Context, spaceID string) ([]SpaceLink, error)
	// BacklinksTo returns the live notes linking to an item of any kind.
	BacklinksTo(ctx context.Context, itemID string) ([]*core.Backlink, error)
	// NotesLinkingToIDs returns the live notes with a link resolved to any of
	// the ids, sorted by space and path.
	NotesLinkingToIDs(ctx context.Context, ids []string) ([]*core.Note, error)
	// ResolveDanglingLinks points unresolved links in the item's space at the
	// item when they name it. Item writes in this package call it already.
	ResolveDanglingLinks(ctx context.Context, itemID string) error

	// Shares, public links and comments on an item of a given kind. The
	// note-only methods above write the kind note.
	CreateItemShare(ctx context.Context, kind core.ItemKind, s *core.Share) error
	ListItemShares(ctx context.Context, kind core.ItemKind, itemID string) ([]*core.Share, error)
	// ItemShareGrantsForUser returns grants on every kind with the target path.
	ItemShareGrantsForUser(ctx context.Context, userID string) ([]ShareGrant, error)
	CreateItemPublicLink(ctx context.Context, kind core.ItemKind, l *core.PublicLink) error
	ListItemPublicLinks(ctx context.Context, kind core.ItemKind, itemID string) ([]*core.PublicLink, error)
	// PublicLinkKind returns the target kind of a public link.
	PublicLinkKind(ctx context.Context, id string) (core.ItemKind, error)
	CreateItemComment(ctx context.Context, kind core.ItemKind, c *core.Comment) error
	ListItemComments(ctx context.Context, kind core.ItemKind, itemID string) ([]*core.Comment, error)

	// Settings.
	GetSettings(ctx context.Context) (*core.Settings, error)
	SaveSettings(ctx context.Context, s *core.Settings) error

	// AI providers stored in the database, in addition to those from the
	// configuration file.
	CreateAIProvider(ctx context.Context, p *core.AIProviderConfig) error
	UpdateAIProvider(ctx context.Context, p *core.AIProviderConfig) error
	DeleteAIProvider(ctx context.Context, id string) error
	GetAIProvider(ctx context.Context, id string) (*core.AIProviderConfig, error)
	ListAIProviders(ctx context.Context) ([]*core.AIProviderConfig, error)

	// AI model catalog. ListAIModels with an empty provider id lists every
	// row. UpsertAIModels writes the listed rows of one provider, keeping the
	// enabled flag and price override of rows that exist, and marks every
	// other row of the provider unavailable. GetAIModel returns
	// core.ErrNotFound for an unknown row.
	ListAIModels(ctx context.Context, providerID string) ([]*core.AIModel, error)
	GetAIModel(ctx context.Context, providerID, modelID string) (*core.AIModel, error)
	UpsertAIModels(ctx context.Context, providerID string, models []*core.AIModel, seen time.Time) error
	// UpdateAIModelPrice writes the fetched price, source, matched id and
	// fetch time of one row.
	UpdateAIModelPrice(ctx context.Context, m *core.AIModel) error
	// UpdateAIModelSettings writes the enabled flag and the price override,
	// nil for none, of one row.
	UpdateAIModelSettings(ctx context.Context, providerID, modelID string, enabled bool, override *core.AIPrice) error
	DeleteAIModels(ctx context.Context, providerID string) error

	// AI routes. scope is "instance" or a user id. SaveAIRoute replaces the
	// route of one task.
	ListAIRoutes(ctx context.Context, scope string) ([]core.AIRoute, error)
	SaveAIRoute(ctx context.Context, scope string, r core.AIRoute) error
	DeleteAIRoute(ctx context.Context, scope, task string) error

	// Public price catalogs, kept as downloaded. GetPriceCatalog returns
	// core.ErrNotFound when none is stored.
	GetPriceCatalog(ctx context.Context, source string) ([]byte, time.Time, error)
	SavePriceCatalog(ctx context.Context, source string, body []byte, fetchedAt time.Time) error

	// Sync feed. Every note write assigns the next value of one global
	// revision counter, which never decreases.
	MaxNoteRev(ctx context.Context) (int64, error)
	// NotesChangedSince returns notes that are in spaceIDs or have an id in
	// noteIDs, with a revision in (since, upTo], ordered by revision, at most
	// limit. Trashed notes are included.
	NotesChangedSince(ctx context.Context, spaceIDs, noteIDs []string, since, upTo int64, limit int) ([]*core.Note, error)
	// NoteIDsInSpaces lists every note id in the spaces, trashed notes included.
	NoteIDsInSpaces(ctx context.Context, spaceIDs []string) ([]string, error)

	// AI usage. RecordAIUsage stores one call. AIUsageReport sums raw calls
	// and monthly rollups for q and returns the grouped rows and the totals.
	RecordAIUsage(ctx context.Context, rec *core.AIUsageRecord) error
	AIUsageReport(ctx context.Context, q core.AIUsageQuery) ([]core.AIUsageRow, core.AIUsageRow, error)
	// AIUsageCalls lists raw calls matching q in the order named by sort.
	AIUsageCalls(ctx context.Context, q core.AIUsageQuery, sort string) ([]*core.AIUsageRecord, error)
	// RollupAIUsage folds every raw call from a UTC month before the month
	// containing before into the monthly table and deletes the raw rows, one
	// transaction per month. It returns the months and rows it folded.
	RollupAIUsage(ctx context.Context, before time.Time) (int, int, error)

	// AI budgets. GetAIBudget returns core.ErrNotFound when none is stored.
	GetAIBudget(ctx context.Context, kind, id string) (*core.AIBudget, error)
	ListAIBudgets(ctx context.Context) ([]*core.AIBudget, error)
	SaveAIBudget(ctx context.Context, b *core.AIBudget) error
	DeleteAIBudget(ctx context.Context, kind, id string) error

	// AI conversations. GetAIThread returns core.ErrNotFound when absent.
	// ListAIThreads lists one user's threads about a note active at or after
	// since, newest first, without messages. AddAIMessage also moves the
	// thread's updated_at. DeleteAIThreadsBefore deletes threads last active
	// before cutoff, of one user or of everyone when userID is empty.
	CreateAIThread(ctx context.Context, t *core.AIThread) error
	GetAIThread(ctx context.Context, id string) (*core.AIThread, error)
	ListAIThreads(ctx context.Context, userID, noteID string, since time.Time) ([]*core.AIThread, error)
	AddAIMessage(ctx context.Context, m *core.AIThreadMessage) error
	ListAIMessages(ctx context.Context, threadID string) ([]*core.AIThreadMessage, error)
	SetAIMessageApplied(ctx context.Context, threadID, messageID string, applied bool) error
	DeleteAIThread(ctx context.Context, id string) error
	DeleteAIThreadsBefore(ctx context.Context, userID string, cutoff time.Time) (int, error)

	// Embeddings for semantic search.
	ReplaceEmbeddings(ctx context.Context, noteID, spaceID string, chunks []*core.EmbeddingChunk) error
	DeleteEmbeddings(ctx context.Context, noteID string) error
	ListEmbeddings(ctx context.Context, spaceIDs []string, model string) ([]*core.EmbeddingChunk, error)
	// EmbeddingHashes maps note id to the content hash last embedded.
	EmbeddingHashes(ctx context.Context, spaceID string) (map[string]string, error)
	CountEmbeddings(ctx context.Context) (int, error)
}
