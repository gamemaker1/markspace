package web

import (
	"context"
	"io"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// stubBackend implements core.Backend with every method returning
// core.ErrUnsupported. Tests embed it and override the methods they need.
type stubBackend struct{}

var _ core.Backend = stubBackend{}

func (stubBackend) Signup(ctx context.Context, in core.SignupInput) (*core.User, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) Login(ctx context.Context, email, password, userAgent, ip string) (*core.User, string, error) {
	return nil, "", core.ErrUnsupported
}
func (stubBackend) Logout(ctx context.Context, sessionToken string) error { return core.ErrUnsupported }
func (stubBackend) ActorFromSession(ctx context.Context, sessionToken string) (*core.Actor, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) ActorFromAPIToken(ctx context.Context, token string) (*core.Actor, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) ChangePassword(ctx context.Context, actor *core.Actor, oldPassword, newPassword string) error {
	return core.ErrUnsupported
}
func (stubBackend) UpdateProfile(ctx context.Context, actor *core.Actor, in core.ProfileInput) (*core.User, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) ListSessions(ctx context.Context, actor *core.Actor) ([]*core.Session, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) RevokeSession(ctx context.Context, actor *core.Actor, sessionID string) error {
	return core.ErrUnsupported
}
func (stubBackend) CreateAPIToken(ctx context.Context, actor *core.Actor, name string, ttl time.Duration) (*core.APIToken, string, error) {
	return nil, "", core.ErrUnsupported
}
func (stubBackend) ListAPITokens(ctx context.Context, actor *core.Actor) ([]*core.APIToken, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) RevokeAPIToken(ctx context.Context, actor *core.Actor, id string) error {
	return core.ErrUnsupported
}
func (stubBackend) NeedsSetup(ctx context.Context) (bool, error) { return false, core.ErrUnsupported }
func (stubBackend) ListUsers(ctx context.Context, actor *core.Actor, query string, limit, offset int) ([]*core.User, int, error) {
	return nil, 0, core.ErrUnsupported
}
func (stubBackend) GetUser(ctx context.Context, actor *core.Actor, id string) (*core.User, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) LookupUsers(ctx context.Context, actor *core.Actor, query string, limit int) ([]*core.User, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) CreateUser(ctx context.Context, actor *core.Actor, in core.UserInput) (*core.User, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) UpdateUser(ctx context.Context, actor *core.Actor, id string, in core.UserInput) (*core.User, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) DeleteUser(ctx context.Context, actor *core.Actor, id string) error {
	return core.ErrUnsupported
}
func (stubBackend) CreateInvite(ctx context.Context, actor *core.Actor, in core.InviteInput) (*core.Invite, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) ListInvites(ctx context.Context, actor *core.Actor) ([]*core.Invite, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) RevokeInvite(ctx context.Context, actor *core.Actor, id string) error {
	return core.ErrUnsupported
}
func (stubBackend) ListSpaces(ctx context.Context, actor *core.Actor) ([]*core.Space, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) GetSpace(ctx context.Context, actor *core.Actor, idOrSlug string) (*core.Space, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) CreateSpace(ctx context.Context, actor *core.Actor, in core.SpaceInput) (*core.Space, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) UpdateSpace(ctx context.Context, actor *core.Actor, id string, in core.SpaceInput) (*core.Space, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) DeleteSpace(ctx context.Context, actor *core.Actor, id string) error {
	return core.ErrUnsupported
}
func (stubBackend) ListMembers(ctx context.Context, actor *core.Actor, spaceID string) ([]*core.SpaceMember, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) AddMember(ctx context.Context, actor *core.Actor, spaceID, userID string, role core.Role) (*core.SpaceMember, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) UpdateMember(ctx context.Context, actor *core.Actor, spaceID, userID string, role core.Role) (*core.SpaceMember, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) RemoveMember(ctx context.Context, actor *core.Actor, spaceID, userID string) error {
	return core.ErrUnsupported
}
func (stubBackend) SpaceAccess(ctx context.Context, actor *core.Actor, spaceID string) (*core.Grant, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) ListNotes(ctx context.Context, actor *core.Actor, spaceID string, opts core.NoteListOptions) ([]*core.Note, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) GetNote(ctx context.Context, actor *core.Actor, id string) (*core.Note, *core.Grant, error) {
	return nil, nil, core.ErrUnsupported
}
func (stubBackend) GetNoteByPath(ctx context.Context, actor *core.Actor, spaceSlug, path string) (*core.Note, *core.Grant, error) {
	return nil, nil, core.ErrUnsupported
}
func (stubBackend) CreateNote(ctx context.Context, actor *core.Actor, in core.NoteInput) (*core.Note, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) UpdateNote(ctx context.Context, actor *core.Actor, id string, in core.NoteUpdate) (*core.Note, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) MoveNote(ctx context.Context, actor *core.Actor, id, newParentID string, position int) (*core.Note, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) DuplicateNote(ctx context.Context, actor *core.Actor, id string) (*core.Note, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) TrashNote(ctx context.Context, actor *core.Actor, id string) error {
	return core.ErrUnsupported
}
func (stubBackend) RestoreNote(ctx context.Context, actor *core.Actor, id string) error {
	return core.ErrUnsupported
}
func (stubBackend) DeleteNote(ctx context.Context, actor *core.Actor, id string) error {
	return core.ErrUnsupported
}
func (stubBackend) ListTrash(ctx context.Context, actor *core.Actor, spaceID string) ([]*core.Note, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) EmptyTrash(ctx context.Context, actor *core.Actor, spaceID string) error {
	return core.ErrUnsupported
}
func (stubBackend) SetStar(ctx context.Context, actor *core.Actor, noteID string, starred bool) error {
	return core.ErrUnsupported
}
func (stubBackend) ListStarred(ctx context.Context, actor *core.Actor) ([]*core.Note, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) RecentNotes(ctx context.Context, actor *core.Actor, limit int) ([]*core.Note, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) Backlinks(ctx context.Context, actor *core.Actor, noteID string) ([]*core.Backlink, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) ListTags(ctx context.Context, actor *core.Actor, spaceID string) ([]*core.TagCount, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) ListVersions(ctx context.Context, actor *core.Actor, noteID string) ([]*core.NoteVersion, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) GetVersion(ctx context.Context, actor *core.Actor, noteID, versionID string) (*core.NoteVersion, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) DiffVersion(ctx context.Context, actor *core.Actor, noteID, versionID string) (string, error) {
	return "", core.ErrUnsupported
}
func (stubBackend) RestoreVersion(ctx context.Context, actor *core.Actor, noteID, versionID string) (*core.Note, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) ListComments(ctx context.Context, actor *core.Actor, noteID string) ([]*core.Comment, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) CreateComment(ctx context.Context, actor *core.Actor, in core.CommentInput) (*core.Comment, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) UpdateComment(ctx context.Context, actor *core.Actor, id, body string) (*core.Comment, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) DeleteComment(ctx context.Context, actor *core.Actor, id string) error {
	return core.ErrUnsupported
}
func (stubBackend) ResolveComment(ctx context.Context, actor *core.Actor, id string, resolved bool) (*core.Comment, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) ListShares(ctx context.Context, actor *core.Actor, noteID string) ([]*core.Share, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) CreateShare(ctx context.Context, actor *core.Actor, in core.ShareInput) (*core.Share, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) UpdateShare(ctx context.Context, actor *core.Actor, id string, access core.Access, includeChildren bool) (*core.Share, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) DeleteShare(ctx context.Context, actor *core.Actor, id string) error {
	return core.ErrUnsupported
}
func (stubBackend) ListSharedWithMe(ctx context.Context, actor *core.Actor) ([]*core.Share, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) ListPublicLinks(ctx context.Context, actor *core.Actor, noteID string) ([]*core.PublicLink, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) CreatePublicLink(ctx context.Context, actor *core.Actor, in core.PublicLinkInput) (*core.PublicLink, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) UpdatePublicLink(ctx context.Context, actor *core.Actor, id string, in core.PublicLinkInput) (*core.PublicLink, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) DeletePublicLink(ctx context.Context, actor *core.Actor, id string) error {
	return core.ErrUnsupported
}
func (stubBackend) ResolvePublicLink(ctx context.Context, token, password string) (*core.Actor, *core.Note, error) {
	return nil, nil, core.ErrUnsupported
}
func (stubBackend) Search(ctx context.Context, actor *core.Actor, q core.SearchQuery) ([]*core.SearchHit, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) Suggest(ctx context.Context, actor *core.Actor, prefix string, limit int) ([]*core.Note, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) RenderMarkdown(ctx context.Context, actor *core.Actor, spaceID, md string) (*core.Rendered, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) RenderNote(ctx context.Context, actor *core.Actor, note *core.Note) (*core.Rendered, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) ListAIProviders(ctx context.Context, actor *core.Actor) ([]*core.AIProviderConfig, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) SaveAIProvider(ctx context.Context, actor *core.Actor, cfg *core.AIProviderConfig) (*core.AIProviderConfig, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) DeleteAIProvider(ctx context.Context, actor *core.Actor, id string) error {
	return core.ErrUnsupported
}
func (stubBackend) TestAIProvider(ctx context.Context, actor *core.Actor, id string) error {
	return core.ErrUnsupported
}
func (stubBackend) RefreshAIProvider(ctx context.Context, actor *core.Actor, id string) ([]*core.AIModel, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) UpdateAIModel(ctx context.Context, actor *core.Actor, providerID, model string, in core.AIModelUpdate) (*core.AIModel, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) ListAIRoutes(ctx context.Context, actor *core.Actor) (*core.AIRoutes, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) SaveAIRoute(ctx context.Context, actor *core.Actor, scope, task string, targets []core.AIRouteTarget) (*core.AIRoute, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) DeleteAIRoute(ctx context.Context, actor *core.Actor, task string) error {
	return core.ErrUnsupported
}
func (stubBackend) ListAIPresets(ctx context.Context, actor *core.Actor) ([]core.AIPreset, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) RefreshAIPrices(ctx context.Context, actor *core.Actor) (*core.AIPriceRefresh, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) ListAIModels(ctx context.Context, actor *core.Actor, q core.AIModelQuery) ([]*core.AIModel, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) RunAI(ctx context.Context, actor *core.Actor, req core.AIRequest) (*core.AIResult, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) StreamAI(ctx context.Context, actor *core.Actor, req core.AIRequest) (<-chan core.AIChunk, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) ReindexEmbeddings(ctx context.Context, actor *core.Actor, spaceID string) (int, error) {
	return 0, core.ErrUnsupported
}
func (stubBackend) GetSettings(ctx context.Context, actor *core.Actor) (*core.Settings, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) UpdateSettings(ctx context.Context, actor *core.Actor, in *core.Settings) (*core.Settings, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) Stats(ctx context.Context, actor *core.Actor) (*core.Stats, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) Reindex(ctx context.Context, actor *core.Actor) (int, error) {
	return 0, core.ErrUnsupported
}
func (stubBackend) ListSystemTokens(ctx context.Context, actor *core.Actor) ([]*core.APIToken, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) RevokeSystemToken(ctx context.Context, actor *core.Actor, id string) error {
	return core.ErrUnsupported
}
func (stubBackend) ExportSpace(ctx context.Context, actor *core.Actor, spaceID string, w io.Writer) error {
	return core.ErrUnsupported
}
func (stubBackend) ImportSpace(ctx context.Context, actor *core.Actor, spaceID string, r io.ReaderAt, size int64) (int, error) {
	return 0, core.ErrUnsupported
}
func (stubBackend) Subscribe(ctx context.Context, actor *core.Actor) (<-chan core.Event, func(), error) {
	return nil, nil, core.ErrUnsupported
}

func (stubBackend) SyncPull(context.Context, *core.Actor, int64, int) (*core.SyncBatch, error) {
	return nil, core.ErrUnsupported
}

func (stubBackend) MyAIUsage(ctx context.Context, actor *core.Actor, q core.AIUsageQuery) (*core.AIUsageReport, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) AIUsage(ctx context.Context, actor *core.Actor, q core.AIUsageQuery) (*core.AIUsageReport, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) AIUsageCalls(ctx context.Context, actor *core.Actor, q core.AIUsageQuery, sort string) ([]*core.AIUsageRecord, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) AIUsageSummary(ctx context.Context, actor *core.Actor) (*core.AIUsageSummary, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) ListAIBudgets(ctx context.Context, actor *core.Actor) ([]*core.AIBudget, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) SaveAIBudget(ctx context.Context, actor *core.Actor, b core.AIBudget) (*core.AIBudget, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) DeleteAIBudget(ctx context.Context, actor *core.Actor, kind, id string) error {
	return core.ErrUnsupported
}
func (stubBackend) LiftAIBudget(ctx context.Context, actor *core.Actor, kind, id string) (*core.AIBudget, error) {
	return nil, core.ErrUnsupported
}

func (stubBackend) ResolvePublicItem(ctx context.Context, token, password string) (*core.Actor, *core.Item, error) {
	return nil, nil, core.ErrUnsupported
}
func (stubBackend) SpaceTree(ctx context.Context, actor *core.Actor, spaceID string) ([]*core.TreeItem, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) GetItem(ctx context.Context, actor *core.Actor, id string) (*core.Item, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) GetItemByPath(ctx context.Context, actor *core.Actor, space, relPath string) (*core.Item, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) ListTrashItems(ctx context.Context, actor *core.Actor, spaceID string) ([]*core.TreeItem, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) CreateFolder(ctx context.Context, actor *core.Actor, in core.FolderInput) (*core.Folder, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) GetFolder(ctx context.Context, actor *core.Actor, id string) (*core.TreeItem, *core.Grant, error) {
	return nil, nil, core.ErrUnsupported
}
func (stubBackend) RenameFolder(ctx context.Context, actor *core.Actor, id, name string) (*core.Folder, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) UploadFile(ctx context.Context, actor *core.Actor, in core.FileUpload, r io.Reader) (*core.File, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) GetFile(ctx context.Context, actor *core.Actor, id string) (*core.File, *core.Grant, error) {
	return nil, nil, core.ErrUnsupported
}
func (stubBackend) ListFiles(ctx context.Context, actor *core.Actor, spaceID string, q core.FileQuery) ([]*core.File, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) RenameFile(ctx context.Context, actor *core.Actor, id, name string) (*core.File, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) ReplaceFile(ctx context.Context, actor *core.Actor, id string, in core.FileReplace, r io.Reader) (*core.File, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) ListFileVersions(ctx context.Context, actor *core.Actor, id string) ([]*core.FileVersion, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) RestoreFileVersion(ctx context.Context, actor *core.Actor, id, versionID string) (*core.File, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) OpenFile(ctx context.Context, actor *core.Actor, id, versionID string) (*core.File, *core.FileVersion, io.ReadSeekCloser, error) {
	return nil, nil, nil, core.ErrUnsupported
}
func (stubBackend) OpenThumbnail(ctx context.Context, actor *core.Actor, id string, width int) (*core.File, io.ReadSeekCloser, string, error) {
	return nil, nil, "", core.ErrUnsupported
}
func (stubBackend) ItemBacklinks(ctx context.Context, actor *core.Actor, id string) ([]*core.Backlink, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) MoveItem(ctx context.Context, actor *core.Actor, id, parentID string, position int) (*core.Item, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) CopyItem(ctx context.Context, actor *core.Actor, id string, in core.CopyInput) (*core.Item, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) TrashItem(ctx context.Context, actor *core.Actor, id string) error {
	return core.ErrUnsupported
}
func (stubBackend) RestoreItem(ctx context.Context, actor *core.Actor, id string) (*core.Item, error) {
	return nil, core.ErrUnsupported
}
func (stubBackend) DeleteItem(ctx context.Context, actor *core.Actor, id string) error {
	return core.ErrUnsupported
}
func (stubBackend) ResolveLinks(ctx context.Context, actor *core.Actor, in core.ResolveRequest) ([]*core.ResolvedTarget, error) {
	return nil, core.ErrUnsupported
}

// Suggestions and plugin support, added with the module engine.

func (stubBackend) CreateSuggestion(context.Context, *core.Actor, core.SuggestionInput) (*core.Suggestion, error) {
	return nil, core.ErrUnsupported
}

func (stubBackend) ListNoteSuggestions(context.Context, *core.Actor, string, []string) ([]*core.Suggestion, error) {
	return nil, core.ErrUnsupported
}

func (stubBackend) SuggestionInbox(context.Context, *core.Actor, core.SuggestionQuery) ([]*core.Suggestion, error) {
	return nil, core.ErrUnsupported
}

func (stubBackend) GetSuggestion(context.Context, *core.Actor, string) (*core.Suggestion, error) {
	return nil, core.ErrUnsupported
}

func (stubBackend) AcceptSuggestion(context.Context, *core.Actor, string, []string) (*core.SuggestionResult, error) {
	return nil, core.ErrUnsupported
}

func (stubBackend) DiscardSuggestion(context.Context, *core.Actor, string, []string) (*core.Suggestion, error) {
	return nil, core.ErrUnsupported
}

func (stubBackend) WithdrawSuggestion(context.Context, *core.Actor, string) error {
	return core.ErrUnsupported
}

func (stubBackend) OutboundLinks(context.Context, *core.Actor, string) ([]*core.ResolvedTarget, error) {
	return nil, core.ErrUnsupported
}

func (stubBackend) FileText(context.Context, *core.Actor, string) (string, bool, error) {
	return "", false, core.ErrUnsupported
}

func (stubBackend) EmbedTexts(context.Context, *core.Actor, []string, string) (*core.AIEmbedResult, error) {
	return nil, core.ErrUnsupported
}

// Conversations and image generation, part of core.Backend.

func (stubBackend) ListAIThreads(context.Context, *core.Actor, string) ([]*core.AIThread, error) {
	return nil, core.ErrUnsupported
}

func (stubBackend) CreateAIThread(context.Context, *core.Actor, core.AIThreadInput) (*core.AIThread, error) {
	return nil, core.ErrUnsupported
}

func (stubBackend) StreamAIThreadMessage(context.Context, *core.Actor, string, core.AIThreadMessageInput) (<-chan core.AIThreadChunk, error) {
	return nil, core.ErrUnsupported
}

func (stubBackend) SetAIMessageApplied(context.Context, *core.Actor, string, string, bool) error {
	return core.ErrUnsupported
}

func (stubBackend) DeleteAIThread(context.Context, *core.Actor, string) error {
	return core.ErrUnsupported
}

func (stubBackend) GenerateImages(context.Context, *core.Actor, core.AIImageGenerateRequest, func(core.AIImageProgress)) (*core.AIImageGenerateResult, error) {
	return nil, core.ErrUnsupported
}
