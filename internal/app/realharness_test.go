package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/config"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/index"
	"github.com/gamemaker1/markspace/internal/markdown"
	"github.com/gamemaker1/markspace/internal/store"
)

// rharness runs the application over the real store, index and markdown
// renderer in a temporary data directory, with the fake AI, hasher and
// clock. Tests of folders, files, links and the tree use it, because the
// fakes do not model items.
type rharness struct {
	t     testing.TB
	ctx   context.Context
	app   *App
	files *store.Store
	db    *index.DB
	ai    *fakeAI
	clock *fakeClock
	cfg   *config.Config
	dir   string
}

// newRealHarness builds an App over a fresh data directory.
func newRealHarness(t testing.TB) *rharness {
	t.Helper()
	dir := t.TempDir()
	clock := newFakeClock()
	cfg := config.Default()
	cfg.DataDir = dir
	cfg.DBPath = filepath.Join(dir, "markspace.db")
	cfg.WatchFiles = false
	cfg.AI.Enabled = true
	cfg.AI.Providers = []core.AIProviderConfig{{
		ID: "local", Name: "Local", Kind: core.ProviderOpenAI, BaseURL: "http://127.0.0.1:1",
		Model: "chat", EmbedModel: "embed", APIKey: "secret", MaxTokens: 100, Enabled: true,
	}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.New(dir, logger)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	db, err := index.Open(cfg.DBPath, logger)
	if err != nil {
		t.Fatalf("index.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	h := &rharness{t: t, ctx: context.Background(), files: st, db: db, ai: &fakeAI{}, clock: clock, cfg: cfg, dir: dir}
	a, err := New(Deps{
		Files:    st,
		DB:       db,
		Renderer: markdown.New(logger),
		AI:       h.ai,
		Hasher:   fakeHasher{},
		Config:   cfg,
		Logger:   logger,
		Now:      clock.Now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.app = a
	t.Cleanup(func() { _ = a.Close() })
	return h
}

// user inserts an active account directly and returns its actor.
func (h *rharness) user(name string, admin bool) *core.Actor {
	h.t.Helper()
	hash, _ := fakeHasher{}.Hash("password123")
	u := &core.User{
		ID: core.NewID(), Email: strings.ToLower(name) + "@example.test", Name: name,
		PasswordHash: hash, IsAdmin: admin, IsActive: true, CreatedAt: h.clock.Now(),
	}
	if err := h.db.CreateUser(h.ctx, u); err != nil {
		h.t.Fatalf("create user: %v", err)
	}
	return &core.Actor{User: u}
}

// space creates a space owned by owner.
func (h *rharness) space(owner *core.Actor, name string) *core.Space {
	h.t.Helper()
	sp, err := h.app.CreateSpace(h.ctx, owner, core.SpaceInput{Name: &name})
	if err != nil {
		h.t.Fatalf("create space: %v", err)
	}
	return sp
}

// member adds actor to space with role.
func (h *rharness) member(sp *core.Space, actor *core.Actor, role core.Role) {
	h.t.Helper()
	if err := h.db.AddMember(h.ctx, &core.SpaceMember{SpaceID: sp.ID, UserID: actor.User.ID, Role: role}); err != nil {
		h.t.Fatalf("add member: %v", err)
	}
}

// note creates a note through the app. parentID is a folder id, a note id
// or "".
func (h *rharness) note(actor *core.Actor, sp *core.Space, parentID, title, body string) *core.Note {
	h.t.Helper()
	n, err := h.app.CreateNote(h.ctx, actor, core.NoteInput{SpaceID: sp.ID, ParentID: parentID, Title: title, Content: body})
	if err != nil {
		h.t.Fatalf("create note %q: %v", title, err)
	}
	return n
}

// folder creates a folder through the app.
func (h *rharness) folder(actor *core.Actor, sp *core.Space, parentID, name string) *core.Folder {
	h.t.Helper()
	f, err := h.app.CreateFolder(h.ctx, actor, core.FolderInput{SpaceID: sp.ID, ParentID: parentID, Name: name})
	if err != nil {
		h.t.Fatalf("create folder %q: %v", name, err)
	}
	return f
}

// upload stores a file through the app.
func (h *rharness) upload(actor *core.Actor, sp *core.Space, parentID, name, body string) *core.File {
	h.t.Helper()
	f, err := h.app.UploadFile(h.ctx, actor, core.FileUpload{SpaceID: sp.ID, ParentID: parentID, Name: name}, strings.NewReader(body))
	if err != nil {
		h.t.Fatalf("upload %q: %v", name, err)
	}
	return f
}

// share grants a user direct access to an item of any kind.
func (h *rharness) share(itemID string, actor *core.Actor, access core.Access, children bool) {
	h.t.Helper()
	kind, spaceID, err := h.db.ItemKind(h.ctx, itemID)
	if err != nil {
		h.t.Fatalf("share: item kind: %v", err)
	}
	if err := h.db.CreateItemShare(h.ctx, kind, &core.Share{
		ID: core.NewID(), NoteID: itemID, SpaceID: spaceID, UserID: actor.User.ID,
		Access: access, IncludeChildren: children,
	}); err != nil {
		h.t.Fatalf("share: %v", err)
	}
}

// row loads the index row of a note.
func (h *rharness) row(id string) *core.Note {
	h.t.Helper()
	n, err := h.db.GetNote(h.ctx, id)
	if err != nil {
		h.t.Fatalf("row %s: %v", id, err)
	}
	return n
}

// item loads an item of any kind without an access check.
func (h *rharness) item(id string) *core.Item {
	h.t.Helper()
	it, err := h.app.loadItem(h.ctx, id)
	if err != nil {
		h.t.Fatalf("item %s: %v", id, err)
	}
	return it
}

// diskPath returns the absolute path of an entry in a space.
func (h *rharness) diskPath(sp *core.Space, rel string) string {
	return filepath.Join(h.files.SpacesDir(), sp.Slug, filepath.FromSlash(rel))
}

// exists reports whether an entry is on disk.
func (h *rharness) exists(sp *core.Space, rel string) bool {
	_, err := os.Lstat(h.diskPath(sp, rel))
	return err == nil
}

// body reads the body of the note file at rel.
func (h *rharness) body(sp *core.Space, rel string) string {
	h.t.Helper()
	n, err := h.files.Read(sp.Slug, rel)
	if err != nil {
		h.t.Fatalf("read %s: %v", rel, err)
	}
	return n.Content
}

// writeDisk writes a file outside the application, creating folders.
func (h *rharness) writeDisk(sp *core.Space, rel, content string) {
	h.t.Helper()
	full := h.diskPath(sp, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

// renameDisk renames an entry outside the application.
func (h *rharness) renameDisk(sp *core.Space, from, to string) {
	h.t.Helper()
	full := h.diskPath(sp, to)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.Rename(h.diskPath(sp, from), full); err != nil {
		h.t.Fatal(err)
	}
}

// removeDisk deletes an entry outside the application.
func (h *rharness) removeDisk(sp *core.Space, rel string) {
	h.t.Helper()
	if err := os.RemoveAll(h.diskPath(sp, rel)); err != nil {
		h.t.Fatal(err)
	}
}

// rescan runs the startup reconcile of one space.
func (h *rharness) rescan(sp *core.Space) {
	h.t.Helper()
	if _, _, err := h.app.reconcileSpace(h.ctx, sp, false); err != nil {
		h.t.Fatalf("reconcile: %v", err)
	}
}
