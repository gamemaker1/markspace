// Package server runs the whole stack against a temporary data directory.
package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/ai"
	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/app"
	"github.com/gamemaker1/markspace/internal/auth"
	"github.com/gamemaker1/markspace/internal/config"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/index"
	"github.com/gamemaker1/markspace/internal/markdown"
	"github.com/gamemaker1/markspace/internal/store"
	"github.com/gamemaker1/markspace/internal/web"
	"github.com/gamemaker1/markspace/internal/wiring"
)

// hasher adapts the auth package to app.Hasher.
type hasher struct{}

func (hasher) Hash(plain string) (string, error)          { return auth.HashPassword(plain) }
func (hasher) Verify(encoded, plain string) (bool, error) { return auth.VerifyPassword(encoded, plain) }
func (hasher) HashToken(plain string) string              { return auth.HashToken(plain) }
func (hasher) NewSessionToken() (string, string)          { return auth.NewSessionToken() }
func (hasher) NewAPIToken() (string, string, string)      { return auth.NewAPIToken() }
func (hasher) ValidatePassword(plain string) error        { return auth.ValidatePassword(plain) }
func (hasher) NormalizeEmail(s string) string             { return auth.NormalizeEmail(s) }
func (hasher) ValidateEmail(s string) error               { return auth.ValidateEmail(s) }

// harness holds a running server and a cookie-carrying client.
type harness struct {
	t      *testing.T
	cfg    *config.Config
	server *httptest.Server
	client *http.Client
	app    *app.App
}

// newHarness builds the full stack over a temporary data directory.
func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()

	cfg := config.Default()
	cfg.DataDir = dir
	cfg.BaseURL = ""
	cfg.AI.Enabled = false
	cfg.WatchFiles = false
	loaded, err := writeAndLoad(t, dir, cfg)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg = loaded

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	files, err := store.New(cfg.DataDir, logger)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	db, err := index.Open(cfg.DBPath, logger)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	renderer := markdown.New(logger)
	backend, err := app.New(app.Deps{
		Files:    files,
		DB:       db,
		Renderer: renderer,
		AI:       ai.NewRegistry(logger, cfg.AI.ContextChunks),
		Hasher:   hasher{},
		Config:   cfg,
		Logger:   logger,
	})
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	if err := backend.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { backend.Close() })

	authn := &api.Authenticator{Backend: backend, Logger: logger}
	mount, err := wiring.MountCore(wiring.NewEnv(cfg, backend, authn, logger))
	if err != nil {
		t.Fatalf("modules: %v", err)
	}
	apiSrv, err := api.NewServer(api.Options{Logger: logger, Auth: authn, Mount: mount})
	if err != nil {
		t.Fatalf("api: %v", err)
	}
	webSrv, err := web.NewServer(web.Options{
		Backend:      backend,
		Logger:       logger,
		Auth:         authn,
		SessionTTL:   cfg.SessionTTL,
		InstanceName: "Test",
		MaxUpload:    cfg.MaxUploadBytes(),
		MarkdownCSS:  renderer.CSS(),
	})
	if err != nil {
		t.Fatalf("web: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/api/", apiSrv.Handler())
	mux.Handle("/", webSrv.Handler())

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, _ := cookiejar.New(nil)
	return &harness{
		t:      t,
		cfg:    cfg,
		server: srv,
		client: &http.Client{Jar: jar},
		app:    backend,
	}
}

// writeAndLoad round-trips the config through Load so its derived paths are set.
func writeAndLoad(t *testing.T, dir string, cfg *config.Config) (*config.Config, error) {
	t.Helper()
	t.Setenv("MARKSPACE_DATA_DIR", dir)
	t.Setenv("MARKSPACE_AI_ENABLED", "false")
	return config.Load("")
}

// do issues a request and decodes a JSON response into out when out is not nil.
func (h *harness) do(method, path string, body any, out any) *http.Response {
	h.t.Helper()
	var r io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("marshal: %v", err)
		}
		r = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, h.server.URL+path, r)
	if err != nil {
		h.t.Fatalf("request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	if out != nil {
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		if err := json.Unmarshal(data, out); err != nil {
			h.t.Fatalf("%s %s: decode %q: %v", method, path, truncate(string(data)), err)
		}
		return resp
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp
}

func truncate(s string) string {
	if len(s) > 400 {
		return s[:400] + "..."
	}
	return s
}

func (h *harness) mustStatus(resp *http.Response, want int, what string) {
	h.t.Helper()
	if resp.StatusCode != want {
		h.t.Fatalf("%s: status %d, want %d", what, resp.StatusCode, want)
	}
}

// TestSignupThroughSharing walks the main flow: first-run signup, a space, a
// note, search, direct sharing, a public link and the trash.
func TestSignupThroughSharing(t *testing.T) {
	h := newHarness(t)

	var me struct {
		User       *core.User `json:"user"`
		NeedsSetup bool       `json:"needsSetup"`
	}
	resp := h.do("GET", "/api/auth/me", nil, &me)
	h.mustStatus(resp, 200, "me before signup")
	if !me.NeedsSetup {
		t.Fatal("fresh instance should need setup")
	}

	var admin core.User
	resp = h.do("POST", "/api/auth/signup", map[string]string{
		"email":    "admin@example.com",
		"name":     "Ada Admin",
		"password": "correct horse battery",
	}, &admin)
	h.mustStatus(resp, 201, "signup")
	if !admin.IsAdmin {
		t.Fatal("first user should be an instance admin")
	}

	var spaces struct{ Items []*core.Space }
	resp = h.do("GET", "/api/spaces", nil, &spaces)
	h.mustStatus(resp, 200, "list spaces")
	if len(spaces.Items) == 0 {
		t.Fatal("first user should get a personal space")
	}
	space := spaces.Items[0]

	var note core.Note
	resp = h.do("POST", "/api/notes", core.NoteInput{
		SpaceID: space.ID,
		Title:   "Release checklist",
		Content: "# Release checklist\n\n- [ ] tag\n- [x] changelog\n\n#release\n",
		Tags:    []string{"ops"},
	}, &note)
	h.mustStatus(resp, 201, "create note")
	if note.Path != "release-checklist.md" {
		t.Fatalf("note path %q, want release-checklist.md", note.Path)
	}

	onDisk := filepath.Join(h.cfg.SpacesDir(), space.Slug, note.Path)
	raw, err := os.ReadFile(onDisk)
	if err != nil {
		t.Fatalf("read note file: %v", err)
	}
	for _, want := range []string{"---", "id: " + note.ID, "title: Release checklist", "- [x] changelog"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("note file missing %q:\n%s", want, raw)
		}
	}

	var hits struct{ Items []*core.SearchHit }
	resp = h.do("GET", "/api/search?q=changelog", nil, &hits)
	h.mustStatus(resp, 200, "search")
	if len(hits.Items) != 1 || hits.Items[0].Note.ID != note.ID {
		t.Fatalf("search returned %d hits, want the checklist", len(hits.Items))
	}

	// A second user sees nothing until the note is shared.
	var reader core.User
	resp = h.do("POST", "/api/users", core.UserInput{
		Email:    ptr("reader@example.com"),
		Name:     ptr("Rex Reader"),
		Password: ptr("another good password"),
	}, &reader)
	h.mustStatus(resp, 201, "create second user")

	var share core.Share
	resp = h.do("POST", "/api/notes/"+note.ID+"/shares", core.ShareInput{
		NoteID: note.ID,
		UserID: reader.ID,
		Access: core.AccessView,
	}, &share)
	h.mustStatus(resp, 201, "create share")

	var link core.PublicLink
	resp = h.do("POST", "/api/notes/"+note.ID+"/links", core.PublicLinkInput{
		NoteID: note.ID,
		Access: core.AccessView,
	}, &link)
	h.mustStatus(resp, 201, "create public link")

	// The public link works without any session.
	anon := &http.Client{}
	pub, err := anon.Get(h.server.URL + "/p/" + link.Token)
	if err != nil {
		t.Fatalf("public link: %v", err)
	}
	defer pub.Body.Close()
	if pub.StatusCode != 200 {
		t.Fatalf("public link status %d, want 200", pub.StatusCode)
	}
	page, _ := io.ReadAll(pub.Body)
	if !strings.Contains(string(page), "Release checklist") {
		t.Fatal("public page should show the note title")
	}

	// Trash and restore keep the file, moved into the space trash.
	resp = h.do("POST", "/api/notes/"+note.ID+"/trash", nil, nil)
	h.mustStatus(resp, 204, "trash")
	if _, err := os.Stat(onDisk); !os.IsNotExist(err) {
		t.Fatal("trashed note should leave its original path")
	}
	resp = h.do("POST", "/api/notes/"+note.ID+"/restore", nil, nil)
	h.mustStatus(resp, 204, "restore")
	if _, err := os.Stat(onDisk); err != nil {
		t.Fatalf("restored note should return to its path: %v", err)
	}
}

// TestExternalEditIsPickedUp writes a markdown file directly and reindexes.
func TestExternalEditIsPickedUp(t *testing.T) {
	h := newHarness(t)

	var admin core.User
	resp := h.do("POST", "/api/auth/signup", map[string]string{
		"email":    "admin@example.com",
		"name":     "Ada",
		"password": "correct horse battery",
	}, &admin)
	h.mustStatus(resp, 201, "signup")

	var spaces struct{ Items []*core.Space }
	h.do("GET", "/api/spaces", nil, &spaces)
	space := spaces.Items[0]

	path := filepath.Join(h.cfg.SpacesDir(), space.Slug, "written-by-vim.md")
	body := "---\ntitle: Written by vim\ntags: [external]\n---\n\nHand written body.\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	resp = h.do("POST", "/api/reindex", nil, nil)
	h.mustStatus(resp, 200, "reindex")

	var hits struct{ Items []*core.SearchHit }
	resp = h.do("GET", "/api/search?q=vim", nil, &hits)
	h.mustStatus(resp, 200, "search")
	if len(hits.Items) == 0 {
		t.Fatal("reindex should find the hand written note")
	}
	if got := hits.Items[0].Note.Title; got != "Written by vim" {
		t.Fatalf("title %q, want Written by vim", got)
	}

	// The file gained an id on first read.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	if !strings.Contains(string(raw), "id: ") {
		t.Fatalf("store should mint an id into the frontmatter:\n%s", raw)
	}
}

// TestVersionHistory edits a note twice and restores the first body.
func TestVersionHistory(t *testing.T) {
	h := newHarness(t)

	var admin core.User
	h.do("POST", "/api/auth/signup", map[string]string{
		"email":    "admin@example.com",
		"name":     "Ada",
		"password": "correct horse battery",
	}, &admin)

	var spaces struct{ Items []*core.Space }
	h.do("GET", "/api/spaces", nil, &spaces)

	var note core.Note
	h.do("POST", "/api/notes", core.NoteInput{
		SpaceID: spaces.Items[0].ID,
		Title:   "Draft",
		Content: "first body\n",
	}, &note)

	h.do("PATCH", "/api/notes/"+note.ID, core.NoteUpdate{
		Content:  ptr("second body\n"),
		Snapshot: true,
	}, &note)

	var versions struct{ Items []*core.NoteVersion }
	resp := h.do("GET", "/api/notes/"+note.ID+"/versions", nil, &versions)
	h.mustStatus(resp, 200, "versions")
	if len(versions.Items) == 0 {
		t.Fatal("an edit should leave a version snapshot")
	}

	var restored core.Note
	resp = h.do("POST", "/api/notes/"+note.ID+"/versions/"+versions.Items[0].ID+"/restore", nil, &restored)
	h.mustStatus(resp, 200, "restore version")
	if !strings.Contains(restored.Content, "first body") {
		t.Fatalf("restored content %q, want the first body", restored.Content)
	}
}

func ptr[T any](v T) *T { return &v }

// as returns a harness sharing the server but carrying a separate session,
// logged in with the given credentials.
func (h *harness) as(email, password string) *harness {
	h.t.Helper()
	jar, _ := cookiejar.New(nil)
	other := *h
	other.client = &http.Client{Jar: jar}
	resp := other.do("POST", "/api/auth/login", map[string]string{"email": email, "password": password}, nil)
	h.mustStatus(resp, 200, "login as "+email)
	return &other
}

// TestPerNoteSharing checks what a user outside the space sees before, during
// and after a note is shared with them.
func TestPerNoteSharing(t *testing.T) {
	h := newHarness(t)
	h.do("POST", "/api/auth/signup", map[string]string{
		"email": "owner@example.com", "name": "Olive Owner", "password": "correct horse battery",
	}, nil)
	var spaces struct{ Items []*core.Space }
	h.do("GET", "/api/spaces", nil, &spaces)
	space := spaces.Items[0]

	var shared, private, child core.Note
	h.mustStatus(h.do("POST", "/api/notes", core.NoteInput{
		SpaceID: space.ID, Title: "Roadmap", Content: "quarterly zebra goals\n",
	}, &shared), 201, "create shared note")
	h.mustStatus(h.do("POST", "/api/notes", core.NoteInput{
		SpaceID: space.ID, Title: "Salaries", Content: "confidential walrus figures\n",
	}, &private), 201, "create private note")
	h.mustStatus(h.do("POST", "/api/notes", core.NoteInput{
		SpaceID: space.ID, ParentID: shared.ID, Title: "Q3 detail", Content: "nested okapi plan\n",
	}, &child), 201, "create child note")

	var reader core.User
	h.mustStatus(h.do("POST", "/api/users", core.UserInput{
		Email: ptr("reader@example.com"), Name: ptr("Rex Reader"), Password: ptr("another good password"),
	}, &reader), 201, "create reader")
	r := h.as("reader@example.com", "another good password")

	// Before sharing, the reader cannot tell the note exists.
	r.mustStatus(r.do("GET", "/api/notes/"+shared.ID, nil, nil), 404, "reader reads unshared note")

	var share core.Share
	h.mustStatus(h.do("POST", "/api/notes/"+shared.ID+"/shares", core.ShareInput{
		NoteID: shared.ID, UserID: reader.ID, Access: core.AccessView,
	}, &share), 201, "share view")

	r.mustStatus(r.do("GET", "/api/notes/"+shared.ID, nil, nil), 200, "reader reads shared note")
	r.mustStatus(r.do("GET", "/api/notes/"+private.ID, nil, nil), 404, "reader reads sibling note")
	r.mustStatus(r.do("GET", "/api/notes/"+child.ID, nil, nil), 404, "reader reads child without includeChildren")
	r.mustStatus(r.do("PATCH", "/api/notes/"+shared.ID, core.NoteUpdate{Content: ptr("defaced\n")}, nil), 403, "reader edits view-only note")

	var hits struct{ Items []*core.SearchHit }
	r.do("GET", "/api/search?q=walrus", nil, &hits)
	if len(hits.Items) != 0 {
		t.Fatalf("reader search found %d hits in an unshared note", len(hits.Items))
	}
	r.do("GET", "/api/search?q=zebra", nil, &hits)
	if len(hits.Items) != 1 {
		t.Fatalf("reader search found %d hits in the shared note, want 1", len(hits.Items))
	}

	var mine struct{ Items []*core.Share }
	r.mustStatus(r.do("GET", "/api/shares/mine", nil, &mine), 200, "shared with me")
	if len(mine.Items) != 1 || mine.Items[0].NoteID != shared.ID {
		t.Fatalf("shared-with-me lists %d shares, want the roadmap", len(mine.Items))
	}

	// Raising the share to edit with children opens the subtree for writing.
	h.mustStatus(h.do("PATCH", "/api/shares/"+share.ID, map[string]any{
		"access": "edit", "includeChildren": true,
	}, nil), 200, "raise share")
	r.mustStatus(r.do("PATCH", "/api/notes/"+shared.ID, core.NoteUpdate{Content: ptr("reader edit\n")}, nil), 200, "reader edits after raise")
	r.mustStatus(r.do("GET", "/api/notes/"+child.ID, nil, nil), 200, "reader reads child with includeChildren")
	r.mustStatus(r.do("GET", "/api/notes/"+private.ID, nil, nil), 404, "sibling stays hidden")

	// Sharing controls stay with the owner.
	r.mustStatus(r.do("POST", "/api/notes/"+shared.ID+"/shares", core.ShareInput{
		NoteID: shared.ID, Email: "owner@example.com", Access: core.AccessView,
	}, nil), 403, "editor cannot reshare")

	// Revoking the share hides the note again.
	h.mustStatus(h.do("DELETE", "/api/shares/"+share.ID, nil, nil), 204, "revoke share")
	r.mustStatus(r.do("GET", "/api/notes/"+shared.ID, nil, nil), 404, "reader after revoke")
}
