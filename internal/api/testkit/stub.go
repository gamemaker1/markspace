package testkit

import (
	"context"
	"io"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// Stub satisfies core.Backend with methods that all fail with
// core.ErrUnsupported, except the actor lookups, which fail with
// core.ErrUnauthorized. Tests embed it and write only the behaviour they
// exercise.
type Stub struct{}

var _ core.Backend = Stub{}

func (Stub) Signup(context.Context, core.SignupInput) (*core.User, error) {
	return nil, core.ErrUnsupported
}

func (Stub) Login(context.Context, string, string, string, string) (*core.User, string, error) {
	return nil, "", core.ErrUnsupported
}

func (Stub) Logout(context.Context, string) error { return core.ErrUnsupported }

func (Stub) ActorFromSession(context.Context, string) (*core.Actor, error) {
	return nil, core.ErrUnauthorized
}

func (Stub) ActorFromAPIToken(context.Context, string) (*core.Actor, error) {
	return nil, core.ErrUnauthorized
}

func (Stub) ChangePassword(context.Context, *core.Actor, string, string) error {
	return core.ErrUnsupported
}

func (Stub) UpdateProfile(context.Context, *core.Actor, core.ProfileInput) (*core.User, error) {
	return nil, core.ErrUnsupported
}

func (Stub) ListSessions(context.Context, *core.Actor) ([]*core.Session, error) {
	return nil, core.ErrUnsupported
}

func (Stub) RevokeSession(context.Context, *core.Actor, string) error {
	return core.ErrUnsupported
}

func (Stub) CreateAPIToken(context.Context, *core.Actor, string, time.Duration) (*core.APIToken, string, error) {
	return nil, "", core.ErrUnsupported
}

func (Stub) ListAPITokens(context.Context, *core.Actor) ([]*core.APIToken, error) {
	return nil, core.ErrUnsupported
}

func (Stub) RevokeAPIToken(context.Context, *core.Actor, string) error {
	return core.ErrUnsupported
}

func (Stub) NeedsSetup(context.Context) (bool, error) { return false, core.ErrUnsupported }

func (Stub) ListUsers(context.Context, *core.Actor, string, int, int) ([]*core.User, int, error) {
	return nil, 0, core.ErrUnsupported
}

func (Stub) GetUser(context.Context, *core.Actor, string) (*core.User, error) {
	return nil, core.ErrUnsupported
}

func (Stub) LookupUsers(context.Context, *core.Actor, string, int) ([]*core.User, error) {
	return nil, core.ErrUnsupported
}

func (Stub) CreateUser(context.Context, *core.Actor, core.UserInput) (*core.User, error) {
	return nil, core.ErrUnsupported
}

func (Stub) UpdateUser(context.Context, *core.Actor, string, core.UserInput) (*core.User, error) {
	return nil, core.ErrUnsupported
}

func (Stub) DeleteUser(context.Context, *core.Actor, string) error {
	return core.ErrUnsupported
}

func (Stub) CreateInvite(context.Context, *core.Actor, core.InviteInput) (*core.Invite, error) {
	return nil, core.ErrUnsupported
}

func (Stub) ListInvites(context.Context, *core.Actor) ([]*core.Invite, error) {
	return nil, core.ErrUnsupported
}

func (Stub) RevokeInvite(context.Context, *core.Actor, string) error {
	return core.ErrUnsupported
}

func (Stub) ListSpaces(context.Context, *core.Actor) ([]*core.Space, error) {
	return nil, core.ErrUnsupported
}

func (Stub) GetSpace(context.Context, *core.Actor, string) (*core.Space, error) {
	return nil, core.ErrUnsupported
}

func (Stub) CreateSpace(context.Context, *core.Actor, core.SpaceInput) (*core.Space, error) {
	return nil, core.ErrUnsupported
}

func (Stub) UpdateSpace(context.Context, *core.Actor, string, core.SpaceInput) (*core.Space, error) {
	return nil, core.ErrUnsupported
}

func (Stub) DeleteSpace(context.Context, *core.Actor, string) error {
	return core.ErrUnsupported
}

func (Stub) ListMembers(context.Context, *core.Actor, string) ([]*core.SpaceMember, error) {
	return nil, core.ErrUnsupported
}

func (Stub) AddMember(context.Context, *core.Actor, string, string, core.Role) (*core.SpaceMember, error) {
	return nil, core.ErrUnsupported
}

func (Stub) UpdateMember(context.Context, *core.Actor, string, string, core.Role) (*core.SpaceMember, error) {
	return nil, core.ErrUnsupported
}

func (Stub) RemoveMember(context.Context, *core.Actor, string, string) error {
	return core.ErrUnsupported
}

func (Stub) SpaceAccess(context.Context, *core.Actor, string) (*core.Grant, error) {
	return nil, core.ErrUnsupported
}

func (Stub) ListNotes(context.Context, *core.Actor, string, core.NoteListOptions) ([]*core.Note, error) {
	return nil, core.ErrUnsupported
}

func (Stub) GetNote(context.Context, *core.Actor, string) (*core.Note, *core.Grant, error) {
	return nil, nil, core.ErrUnsupported
}

func (Stub) GetNoteByPath(context.Context, *core.Actor, string, string) (*core.Note, *core.Grant, error) {
	return nil, nil, core.ErrUnsupported
}

func (Stub) CreateNote(context.Context, *core.Actor, core.NoteInput) (*core.Note, error) {
	return nil, core.ErrUnsupported
}

func (Stub) UpdateNote(context.Context, *core.Actor, string, core.NoteUpdate) (*core.Note, error) {
	return nil, core.ErrUnsupported
}

func (Stub) MoveNote(context.Context, *core.Actor, string, string, int) (*core.Note, error) {
	return nil, core.ErrUnsupported
}

func (Stub) DuplicateNote(context.Context, *core.Actor, string) (*core.Note, error) {
	return nil, core.ErrUnsupported
}

func (Stub) TrashNote(context.Context, *core.Actor, string) error { return core.ErrUnsupported }

func (Stub) RestoreNote(context.Context, *core.Actor, string) error {
	return core.ErrUnsupported
}

func (Stub) DeleteNote(context.Context, *core.Actor, string) error {
	return core.ErrUnsupported
}

func (Stub) ListTrash(context.Context, *core.Actor, string) ([]*core.Note, error) {
	return nil, core.ErrUnsupported
}

func (Stub) EmptyTrash(context.Context, *core.Actor, string) error {
	return core.ErrUnsupported
}

func (Stub) SetStar(context.Context, *core.Actor, string, bool) error {
	return core.ErrUnsupported
}

func (Stub) ListStarred(context.Context, *core.Actor) ([]*core.Note, error) {
	return nil, core.ErrUnsupported
}

func (Stub) RecentNotes(context.Context, *core.Actor, int) ([]*core.Note, error) {
	return nil, core.ErrUnsupported
}

func (Stub) Backlinks(context.Context, *core.Actor, string) ([]*core.Backlink, error) {
	return nil, core.ErrUnsupported
}

func (Stub) ListTags(context.Context, *core.Actor, string) ([]*core.TagCount, error) {
	return nil, core.ErrUnsupported
}

func (Stub) ListVersions(context.Context, *core.Actor, string) ([]*core.NoteVersion, error) {
	return nil, core.ErrUnsupported
}

func (Stub) GetVersion(context.Context, *core.Actor, string, string) (*core.NoteVersion, error) {
	return nil, core.ErrUnsupported
}

func (Stub) DiffVersion(context.Context, *core.Actor, string, string) (string, error) {
	return "", core.ErrUnsupported
}

func (Stub) RestoreVersion(context.Context, *core.Actor, string, string) (*core.Note, error) {
	return nil, core.ErrUnsupported
}

func (Stub) ListComments(context.Context, *core.Actor, string) ([]*core.Comment, error) {
	return nil, core.ErrUnsupported
}

func (Stub) CreateComment(context.Context, *core.Actor, core.CommentInput) (*core.Comment, error) {
	return nil, core.ErrUnsupported
}

func (Stub) UpdateComment(context.Context, *core.Actor, string, string) (*core.Comment, error) {
	return nil, core.ErrUnsupported
}

func (Stub) DeleteComment(context.Context, *core.Actor, string) error {
	return core.ErrUnsupported
}

func (Stub) ResolveComment(context.Context, *core.Actor, string, bool) (*core.Comment, error) {
	return nil, core.ErrUnsupported
}

func (Stub) ListShares(context.Context, *core.Actor, string) ([]*core.Share, error) {
	return nil, core.ErrUnsupported
}

func (Stub) CreateShare(context.Context, *core.Actor, core.ShareInput) (*core.Share, error) {
	return nil, core.ErrUnsupported
}

func (Stub) UpdateShare(context.Context, *core.Actor, string, core.Access, bool) (*core.Share, error) {
	return nil, core.ErrUnsupported
}

func (Stub) DeleteShare(context.Context, *core.Actor, string) error {
	return core.ErrUnsupported
}

func (Stub) ListSharedWithMe(context.Context, *core.Actor) ([]*core.Share, error) {
	return nil, core.ErrUnsupported
}

func (Stub) ListPublicLinks(context.Context, *core.Actor, string) ([]*core.PublicLink, error) {
	return nil, core.ErrUnsupported
}

func (Stub) CreatePublicLink(context.Context, *core.Actor, core.PublicLinkInput) (*core.PublicLink, error) {
	return nil, core.ErrUnsupported
}

func (Stub) UpdatePublicLink(context.Context, *core.Actor, string, core.PublicLinkInput) (*core.PublicLink, error) {
	return nil, core.ErrUnsupported
}

func (Stub) DeletePublicLink(context.Context, *core.Actor, string) error {
	return core.ErrUnsupported
}

func (Stub) ResolvePublicLink(context.Context, string, string) (*core.Actor, *core.Note, error) {
	return nil, nil, core.ErrUnsupported
}

func (Stub) Search(context.Context, *core.Actor, core.SearchQuery) ([]*core.SearchHit, error) {
	return nil, core.ErrUnsupported
}

func (Stub) Suggest(context.Context, *core.Actor, string, int) ([]*core.Note, error) {
	return nil, core.ErrUnsupported
}

func (Stub) RenderMarkdown(context.Context, *core.Actor, string, string) (*core.Rendered, error) {
	return nil, core.ErrUnsupported
}

func (Stub) RenderNote(context.Context, *core.Actor, *core.Note) (*core.Rendered, error) {
	return nil, core.ErrUnsupported
}

func (Stub) ListAIProviders(context.Context, *core.Actor) ([]*core.AIProviderConfig, error) {
	return nil, core.ErrUnsupported
}

func (Stub) SaveAIProvider(context.Context, *core.Actor, *core.AIProviderConfig) (*core.AIProviderConfig, error) {
	return nil, core.ErrUnsupported
}

func (Stub) DeleteAIProvider(context.Context, *core.Actor, string) error {
	return core.ErrUnsupported
}

func (Stub) TestAIProvider(context.Context, *core.Actor, string) error {
	return core.ErrUnsupported
}

func (Stub) RefreshAIProvider(context.Context, *core.Actor, string) ([]*core.AIModel, error) {
	return nil, core.ErrUnsupported
}

func (Stub) UpdateAIModel(context.Context, *core.Actor, string, string, core.AIModelUpdate) (*core.AIModel, error) {
	return nil, core.ErrUnsupported
}

func (Stub) ListAIRoutes(context.Context, *core.Actor) (*core.AIRoutes, error) {
	return nil, core.ErrUnsupported
}

func (Stub) SaveAIRoute(context.Context, *core.Actor, string, string, []core.AIRouteTarget) (*core.AIRoute, error) {
	return nil, core.ErrUnsupported
}

func (Stub) DeleteAIRoute(context.Context, *core.Actor, string) error {
	return core.ErrUnsupported
}

func (Stub) ListAIPresets(context.Context, *core.Actor) ([]core.AIPreset, error) {
	return nil, core.ErrUnsupported
}

func (Stub) RefreshAIPrices(context.Context, *core.Actor) (*core.AIPriceRefresh, error) {
	return nil, core.ErrUnsupported
}

func (Stub) ListAIModels(context.Context, *core.Actor, core.AIModelQuery) ([]*core.AIModel, error) {
	return nil, core.ErrUnsupported
}

func (Stub) RunAI(context.Context, *core.Actor, core.AIRequest) (*core.AIResult, error) {
	return nil, core.ErrUnsupported
}

func (Stub) StreamAI(context.Context, *core.Actor, core.AIRequest) (<-chan core.AIChunk, error) {
	return nil, core.ErrUnsupported
}

func (Stub) ReindexEmbeddings(context.Context, *core.Actor, string) (int, error) {
	return 0, core.ErrUnsupported
}

func (Stub) GetSettings(context.Context, *core.Actor) (*core.Settings, error) {
	return nil, core.ErrUnsupported
}

func (Stub) UpdateSettings(context.Context, *core.Actor, *core.Settings) (*core.Settings, error) {
	return nil, core.ErrUnsupported
}

func (Stub) Stats(context.Context, *core.Actor) (*core.Stats, error) {
	return nil, core.ErrUnsupported
}

func (Stub) Reindex(context.Context, *core.Actor) (int, error) {
	return 0, core.ErrUnsupported
}

func (Stub) ListSystemTokens(context.Context, *core.Actor) ([]*core.APIToken, error) {
	return nil, core.ErrUnsupported
}

func (Stub) RevokeSystemToken(context.Context, *core.Actor, string) error {
	return core.ErrUnsupported
}

func (Stub) ExportSpace(context.Context, *core.Actor, string, io.Writer) error {
	return core.ErrUnsupported
}

func (Stub) ImportSpace(context.Context, *core.Actor, string, io.ReaderAt, int64) (int, error) {
	return 0, core.ErrUnsupported
}

func (Stub) Subscribe(context.Context, *core.Actor) (<-chan core.Event, func(), error) {
	return nil, nil, core.ErrUnsupported
}

// Fake overrides the stub methods tests need through function fields. A nil
// field falls through to Stub.
type Fake struct {
	Stub

	ActorFromSessionFn func(ctx context.Context, token string) (*core.Actor, error)
	LoginFn            func(ctx context.Context, email, password, ua, ip string) (*core.User, string, error)
	LogoutFn           func(ctx context.Context, token string) error
	NeedsSetupFn       func(ctx context.Context) (bool, error)
	GetSettingsFn      func(ctx context.Context, actor *core.Actor) (*core.Settings, error)
	CreateNoteFn       func(ctx context.Context, actor *core.Actor, in core.NoteInput) (*core.Note, error)
	UpdateNoteFn       func(ctx context.Context, actor *core.Actor, id string, in core.NoteUpdate) (*core.Note, error)
	GetNoteFn          func(ctx context.Context, actor *core.Actor, id string) (*core.Note, *core.Grant, error)
	GetSpaceFn         func(ctx context.Context, actor *core.Actor, id string) (*core.Space, error)
	SearchFn           func(ctx context.Context, actor *core.Actor, q core.SearchQuery) ([]*core.SearchHit, error)
	SaveAIProviderFn   func(ctx context.Context, actor *core.Actor, cfg *core.AIProviderConfig) (*core.AIProviderConfig, error)
	SyncPullFn         func(ctx context.Context, actor *core.Actor, since int64, limit int) (*core.SyncBatch, error)
	SubscribeFn        func(ctx context.Context, actor *core.Actor) (<-chan core.Event, func(), error)
	StreamAIFn         func(ctx context.Context, actor *core.Actor, req core.AIRequest) (<-chan core.AIChunk, error)
	UploadFileFn       func(ctx context.Context, actor *core.Actor, in core.FileUpload, r io.Reader) (*core.File, error)
	ReplaceFileFn      func(ctx context.Context, actor *core.Actor, id string, in core.FileReplace, r io.Reader) (*core.File, error)
	SpaceTreeFn        func(ctx context.Context, actor *core.Actor, spaceID string) ([]*core.TreeItem, error)
	MoveItemFn         func(ctx context.Context, actor *core.Actor, id, parentID string, position int) (*core.Item, error)
	CopyItemFn         func(ctx context.Context, actor *core.Actor, id string, in core.CopyInput) (*core.Item, error)
	ResolveLinksFn     func(ctx context.Context, actor *core.Actor, in core.ResolveRequest) ([]*core.ResolvedTarget, error)
	ExportSpaceFn      func(ctx context.Context, actor *core.Actor, spaceID string, w io.Writer) error
	RunAIFn            func(ctx context.Context, actor *core.Actor, req core.AIRequest) (*core.AIResult, error)
	MyAIUsageFn        func(ctx context.Context, actor *core.Actor, q core.AIUsageQuery) (*core.AIUsageReport, error)
	AIUsageCallsFn     func(ctx context.Context, actor *core.Actor, q core.AIUsageQuery, sort string) ([]*core.AIUsageRecord, error)
}

func (f *Fake) ActorFromSession(ctx context.Context, token string) (*core.Actor, error) {
	if f.ActorFromSessionFn != nil {
		return f.ActorFromSessionFn(ctx, token)
	}
	return f.Stub.ActorFromSession(ctx, token)
}

func (f *Fake) Login(ctx context.Context, email, password, ua, ip string) (*core.User, string, error) {
	if f.LoginFn != nil {
		return f.LoginFn(ctx, email, password, ua, ip)
	}
	return f.Stub.Login(ctx, email, password, ua, ip)
}

func (f *Fake) Logout(ctx context.Context, token string) error {
	if f.LogoutFn != nil {
		return f.LogoutFn(ctx, token)
	}
	return f.Stub.Logout(ctx, token)
}

func (f *Fake) NeedsSetup(ctx context.Context) (bool, error) {
	if f.NeedsSetupFn != nil {
		return f.NeedsSetupFn(ctx)
	}
	return f.Stub.NeedsSetup(ctx)
}

func (f *Fake) GetSettings(ctx context.Context, actor *core.Actor) (*core.Settings, error) {
	if f.GetSettingsFn != nil {
		return f.GetSettingsFn(ctx, actor)
	}
	return f.Stub.GetSettings(ctx, actor)
}

func (f *Fake) CreateNote(ctx context.Context, actor *core.Actor, in core.NoteInput) (*core.Note, error) {
	if f.CreateNoteFn != nil {
		return f.CreateNoteFn(ctx, actor, in)
	}
	return f.Stub.CreateNote(ctx, actor, in)
}

func (f *Fake) UpdateNote(ctx context.Context, actor *core.Actor, id string, in core.NoteUpdate) (*core.Note, error) {
	if f.UpdateNoteFn != nil {
		return f.UpdateNoteFn(ctx, actor, id, in)
	}
	return f.Stub.UpdateNote(ctx, actor, id, in)
}

func (f *Fake) GetNote(ctx context.Context, actor *core.Actor, id string) (*core.Note, *core.Grant, error) {
	if f.GetNoteFn != nil {
		return f.GetNoteFn(ctx, actor, id)
	}
	return f.Stub.GetNote(ctx, actor, id)
}

func (f *Fake) GetSpace(ctx context.Context, actor *core.Actor, id string) (*core.Space, error) {
	if f.GetSpaceFn != nil {
		return f.GetSpaceFn(ctx, actor, id)
	}
	return f.Stub.GetSpace(ctx, actor, id)
}

func (f *Fake) Search(ctx context.Context, actor *core.Actor, q core.SearchQuery) ([]*core.SearchHit, error) {
	if f.SearchFn != nil {
		return f.SearchFn(ctx, actor, q)
	}
	return f.Stub.Search(ctx, actor, q)
}

func (f *Fake) Subscribe(ctx context.Context, actor *core.Actor) (<-chan core.Event, func(), error) {
	if f.SubscribeFn != nil {
		return f.SubscribeFn(ctx, actor)
	}
	return f.Stub.Subscribe(ctx, actor)
}

func (f *Fake) StreamAI(ctx context.Context, actor *core.Actor, req core.AIRequest) (<-chan core.AIChunk, error) {
	if f.StreamAIFn != nil {
		return f.StreamAIFn(ctx, actor, req)
	}
	return f.Stub.StreamAI(ctx, actor, req)
}

func (f *Fake) ExportSpace(ctx context.Context, actor *core.Actor, spaceID string, w io.Writer) error {
	if f.ExportSpaceFn != nil {
		return f.ExportSpaceFn(ctx, actor, spaceID, w)
	}
	return f.Stub.ExportSpace(ctx, actor, spaceID, w)
}

func (f *Fake) SaveAIProvider(ctx context.Context, actor *core.Actor, cfg *core.AIProviderConfig) (*core.AIProviderConfig, error) {
	if f.SaveAIProviderFn != nil {
		return f.SaveAIProviderFn(ctx, actor, cfg)
	}
	return f.Stub.SaveAIProvider(ctx, actor, cfg)
}

func (Stub) SyncPull(context.Context, *core.Actor, int64, int) (*core.SyncBatch, error) {
	return nil, core.ErrUnsupported
}

func (f *Fake) SyncPull(ctx context.Context, actor *core.Actor, since int64, limit int) (*core.SyncBatch, error) {
	if f.SyncPullFn != nil {
		return f.SyncPullFn(ctx, actor, since, limit)
	}
	return f.Stub.SyncPull(ctx, actor, since, limit)
}

func (Stub) MyAIUsage(ctx context.Context, actor *core.Actor, q core.AIUsageQuery) (*core.AIUsageReport, error) {
	return nil, core.ErrUnsupported
}
func (Stub) AIUsage(ctx context.Context, actor *core.Actor, q core.AIUsageQuery) (*core.AIUsageReport, error) {
	return nil, core.ErrUnsupported
}
func (Stub) AIUsageCalls(ctx context.Context, actor *core.Actor, q core.AIUsageQuery, sort string) ([]*core.AIUsageRecord, error) {
	return nil, core.ErrUnsupported
}
func (Stub) AIUsageSummary(ctx context.Context, actor *core.Actor) (*core.AIUsageSummary, error) {
	return nil, core.ErrUnsupported
}
func (Stub) ListAIBudgets(ctx context.Context, actor *core.Actor) ([]*core.AIBudget, error) {
	return nil, core.ErrUnsupported
}
func (Stub) SaveAIBudget(ctx context.Context, actor *core.Actor, b core.AIBudget) (*core.AIBudget, error) {
	return nil, core.ErrUnsupported
}
func (Stub) DeleteAIBudget(ctx context.Context, actor *core.Actor, kind, id string) error {
	return core.ErrUnsupported
}
func (Stub) LiftAIBudget(ctx context.Context, actor *core.Actor, kind, id string) (*core.AIBudget, error) {
	return nil, core.ErrUnsupported
}

func (f *Fake) RunAI(ctx context.Context, actor *core.Actor, req core.AIRequest) (*core.AIResult, error) {
	if f.RunAIFn != nil {
		return f.RunAIFn(ctx, actor, req)
	}
	return f.Stub.RunAI(ctx, actor, req)
}

func (f *Fake) MyAIUsage(ctx context.Context, actor *core.Actor, q core.AIUsageQuery) (*core.AIUsageReport, error) {
	if f.MyAIUsageFn != nil {
		return f.MyAIUsageFn(ctx, actor, q)
	}
	return f.Stub.MyAIUsage(ctx, actor, q)
}

func (f *Fake) AIUsageCalls(ctx context.Context, actor *core.Actor, q core.AIUsageQuery, sort string) ([]*core.AIUsageRecord, error) {
	if f.AIUsageCallsFn != nil {
		return f.AIUsageCallsFn(ctx, actor, q, sort)
	}
	return f.Stub.AIUsageCalls(ctx, actor, q, sort)
}

func (Stub) ResolvePublicItem(context.Context, string, string) (*core.Actor, *core.Item, error) {
	return nil, nil, core.ErrUnsupported
}

func (Stub) SpaceTree(context.Context, *core.Actor, string) ([]*core.TreeItem, error) {
	return nil, core.ErrUnsupported
}

func (Stub) GetItem(context.Context, *core.Actor, string) (*core.Item, error) {
	return nil, core.ErrUnsupported
}

func (Stub) GetItemByPath(context.Context, *core.Actor, string, string) (*core.Item, error) {
	return nil, core.ErrUnsupported
}

func (Stub) ListTrashItems(context.Context, *core.Actor, string) ([]*core.TreeItem, error) {
	return nil, core.ErrUnsupported
}

func (Stub) CreateFolder(context.Context, *core.Actor, core.FolderInput) (*core.Folder, error) {
	return nil, core.ErrUnsupported
}

func (Stub) GetFolder(context.Context, *core.Actor, string) (*core.TreeItem, *core.Grant, error) {
	return nil, nil, core.ErrUnsupported
}

func (Stub) RenameFolder(context.Context, *core.Actor, string, string) (*core.Folder, error) {
	return nil, core.ErrUnsupported
}

func (Stub) UploadFile(context.Context, *core.Actor, core.FileUpload, io.Reader) (*core.File, error) {
	return nil, core.ErrUnsupported
}

func (Stub) GetFile(context.Context, *core.Actor, string) (*core.File, *core.Grant, error) {
	return nil, nil, core.ErrUnsupported
}

func (Stub) ListFiles(context.Context, *core.Actor, string, core.FileQuery) ([]*core.File, error) {
	return nil, core.ErrUnsupported
}

func (Stub) RenameFile(context.Context, *core.Actor, string, string) (*core.File, error) {
	return nil, core.ErrUnsupported
}

func (Stub) ReplaceFile(context.Context, *core.Actor, string, core.FileReplace, io.Reader) (*core.File, error) {
	return nil, core.ErrUnsupported
}

func (Stub) ListFileVersions(context.Context, *core.Actor, string) ([]*core.FileVersion, error) {
	return nil, core.ErrUnsupported
}

func (Stub) RestoreFileVersion(context.Context, *core.Actor, string, string) (*core.File, error) {
	return nil, core.ErrUnsupported
}

func (Stub) OpenFile(context.Context, *core.Actor, string, string) (*core.File, *core.FileVersion, io.ReadSeekCloser, error) {
	return nil, nil, nil, core.ErrUnsupported
}

func (Stub) OpenThumbnail(context.Context, *core.Actor, string, int) (*core.File, io.ReadSeekCloser, string, error) {
	return nil, nil, "", core.ErrUnsupported
}

func (Stub) ItemBacklinks(context.Context, *core.Actor, string) ([]*core.Backlink, error) {
	return nil, core.ErrUnsupported
}

func (Stub) MoveItem(context.Context, *core.Actor, string, string, int) (*core.Item, error) {
	return nil, core.ErrUnsupported
}

func (Stub) CopyItem(context.Context, *core.Actor, string, core.CopyInput) (*core.Item, error) {
	return nil, core.ErrUnsupported
}

func (Stub) TrashItem(context.Context, *core.Actor, string) error { return core.ErrUnsupported }

func (Stub) RestoreItem(context.Context, *core.Actor, string) (*core.Item, error) {
	return nil, core.ErrUnsupported
}

func (Stub) DeleteItem(context.Context, *core.Actor, string) error { return core.ErrUnsupported }

func (Stub) ResolveLinks(context.Context, *core.Actor, core.ResolveRequest) ([]*core.ResolvedTarget, error) {
	return nil, core.ErrUnsupported
}

func (f *Fake) UploadFile(ctx context.Context, actor *core.Actor, in core.FileUpload, r io.Reader) (*core.File, error) {
	if f.UploadFileFn != nil {
		return f.UploadFileFn(ctx, actor, in, r)
	}
	return f.Stub.UploadFile(ctx, actor, in, r)
}

func (f *Fake) ReplaceFile(ctx context.Context, actor *core.Actor, id string, in core.FileReplace, r io.Reader) (*core.File, error) {
	if f.ReplaceFileFn != nil {
		return f.ReplaceFileFn(ctx, actor, id, in, r)
	}
	return f.Stub.ReplaceFile(ctx, actor, id, in, r)
}

func (f *Fake) SpaceTree(ctx context.Context, actor *core.Actor, spaceID string) ([]*core.TreeItem, error) {
	if f.SpaceTreeFn != nil {
		return f.SpaceTreeFn(ctx, actor, spaceID)
	}
	return f.Stub.SpaceTree(ctx, actor, spaceID)
}

func (f *Fake) MoveItem(ctx context.Context, actor *core.Actor, id, parentID string, position int) (*core.Item, error) {
	if f.MoveItemFn != nil {
		return f.MoveItemFn(ctx, actor, id, parentID, position)
	}
	return f.Stub.MoveItem(ctx, actor, id, parentID, position)
}

func (f *Fake) CopyItem(ctx context.Context, actor *core.Actor, id string, in core.CopyInput) (*core.Item, error) {
	if f.CopyItemFn != nil {
		return f.CopyItemFn(ctx, actor, id, in)
	}
	return f.Stub.CopyItem(ctx, actor, id, in)
}

func (f *Fake) ResolveLinks(ctx context.Context, actor *core.Actor, in core.ResolveRequest) ([]*core.ResolvedTarget, error) {
	if f.ResolveLinksFn != nil {
		return f.ResolveLinksFn(ctx, actor, in)
	}
	return f.Stub.ResolveLinks(ctx, actor, in)
}

// Suggestions and plugin support, added with the module engine.

func (Stub) CreateSuggestion(context.Context, *core.Actor, core.SuggestionInput) (*core.Suggestion, error) {
	return nil, core.ErrUnsupported
}

func (Stub) ListNoteSuggestions(context.Context, *core.Actor, string, []string) ([]*core.Suggestion, error) {
	return nil, core.ErrUnsupported
}

func (Stub) SuggestionInbox(context.Context, *core.Actor, core.SuggestionQuery) ([]*core.Suggestion, error) {
	return nil, core.ErrUnsupported
}

func (Stub) GetSuggestion(context.Context, *core.Actor, string) (*core.Suggestion, error) {
	return nil, core.ErrUnsupported
}

func (Stub) AcceptSuggestion(context.Context, *core.Actor, string, []string) (*core.SuggestionResult, error) {
	return nil, core.ErrUnsupported
}

func (Stub) DiscardSuggestion(context.Context, *core.Actor, string, []string) (*core.Suggestion, error) {
	return nil, core.ErrUnsupported
}

func (Stub) WithdrawSuggestion(context.Context, *core.Actor, string) error {
	return core.ErrUnsupported
}

func (Stub) OutboundLinks(context.Context, *core.Actor, string) ([]*core.ResolvedTarget, error) {
	return nil, core.ErrUnsupported
}

func (Stub) FileText(context.Context, *core.Actor, string) (string, bool, error) {
	return "", false, core.ErrUnsupported
}

func (Stub) EmbedTexts(context.Context, *core.Actor, []string, string) (*core.AIEmbedResult, error) {
	return nil, core.ErrUnsupported
}

// Conversations and image generation.

func (Stub) ListAIThreads(context.Context, *core.Actor, string) ([]*core.AIThread, error) {
	return nil, core.ErrUnsupported
}

func (Stub) CreateAIThread(context.Context, *core.Actor, core.AIThreadInput) (*core.AIThread, error) {
	return nil, core.ErrUnsupported
}

func (Stub) StreamAIThreadMessage(context.Context, *core.Actor, string, core.AIThreadMessageInput) (<-chan core.AIThreadChunk, error) {
	return nil, core.ErrUnsupported
}

func (Stub) SetAIMessageApplied(context.Context, *core.Actor, string, string, bool) error {
	return core.ErrUnsupported
}

func (Stub) DeleteAIThread(context.Context, *core.Actor, string) error {
	return core.ErrUnsupported
}

func (Stub) GenerateImages(context.Context, *core.Actor, core.AIImageGenerateRequest, func(core.AIImageProgress)) (*core.AIImageGenerateResult, error) {
	return nil, core.ErrUnsupported
}
