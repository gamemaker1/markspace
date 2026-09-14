package web

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
)

// Session cookie values the fake backend recognises.
const (
	adminSession  = "session-admin"
	memberSession = "session-member"
)

var testTime = time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC)

// fakeBackend answers the calls the page handlers make with fixed data.
type fakeBackend struct {
	stubBackend
	admin  *core.User
	member *core.User
	space  *core.Space
	parent *core.Note
	note   *core.Note
	folder *core.Folder
	files  map[string]*fakeFile
}

// fakeFile is one file the fake backend serves.
type fakeFile struct {
	file *core.File
	body string
}

func newFake() *fakeBackend {
	space := &core.Space{ID: "sp1", Slug: "demo", Name: "Demo", MyRole: core.RoleEditor, NoteCount: 2, CreatedAt: testTime}
	parent := &core.Note{ID: "n1", SpaceID: "sp1", SpaceSlug: "demo", Path: "guide", Title: "Guide", UpdatedAt: testTime}
	note := &core.Note{
		ID: "n2", SpaceID: "sp1", SpaceSlug: "demo", ParentID: "n1", Path: "guide/install",
		Title: "Install", Content: "Hello **world**", Tags: []string{"setup"}, Words: 2,
		UpdatedAt: testTime, CreatedAt: testTime, UpdatedBy: "u2",
	}
	folder := &core.Folder{ID: "f1", SpaceID: "sp1", SpaceSlug: "demo", Name: "guide", Path: "guide", IndexNoteID: "n1"}
	files := map[string]*fakeFile{}
	for _, ff := range []*fakeFile{
		{&core.File{ID: "x1", SpaceID: "sp1", SpaceSlug: "demo", ParentID: "f1", Name: "shot.png", Path: "guide/shot.png", MIME: "image/png", Size: 12, SHA256: "abc123", ModTime: testTime}, "\x89PNG\r\n\x1a\nrest"},
		{&core.File{ID: "x2", SpaceID: "sp1", SpaceSlug: "demo", Name: "evil.svg", Path: "evil.svg", MIME: "image/svg+xml", ModTime: testTime}, "<svg><script>alert(1)</script></svg>"},
		{&core.File{ID: "x3", SpaceID: "sp1", SpaceSlug: "demo", Name: "report.pdf", Path: "report.pdf", MIME: "application/pdf", ModTime: testTime}, "%PDF-1.4\n%%EOF"},
		{&core.File{ID: "x4", SpaceID: "sp1", SpaceSlug: "demo", Name: "page.html", Path: "page.html", MIME: "text/html", ModTime: testTime}, "<script>alert(1)</script>"},
	} {
		ff.file.Size = int64(len(ff.body))
		files[ff.file.ID] = ff
	}
	return &fakeBackend{
		admin:  &core.User{ID: "u1", Email: "admin@example.com", Name: "Ada Admin", IsAdmin: true, IsActive: true},
		member: &core.User{ID: "u2", Email: "mel@example.com", Name: "Mel Member", IsActive: true},
		space:  space,
		parent: parent,
		note:   note,
		folder: folder,
		files:  files,
	}
}

func (f *fakeBackend) ActorFromSession(_ context.Context, token string) (*core.Actor, error) {
	switch token {
	case adminSession:
		return &core.Actor{User: f.admin}, nil
	case memberSession:
		return &core.Actor{User: f.member}, nil
	}
	return nil, core.ErrUnauthorized
}

func (f *fakeBackend) NeedsSetup(context.Context) (bool, error) { return false, nil }

func (f *fakeBackend) Login(_ context.Context, email, password, _, _ string) (*core.User, string, error) {
	if email == f.member.Email && password == "correct horse" {
		return f.member, memberSession, nil
	}
	return nil, "", core.ErrUnauthorized
}

func (f *fakeBackend) GetSettings(context.Context, *core.Actor) (*core.Settings, error) {
	return &core.Settings{InstanceName: "Test Space", AllowSignup: true, AIEnabled: true}, nil
}

func (f *fakeBackend) ListSpaces(context.Context, *core.Actor) ([]*core.Space, error) {
	return []*core.Space{f.space}, nil
}

func (f *fakeBackend) GetSpace(_ context.Context, _ *core.Actor, idOrSlug string) (*core.Space, error) {
	if idOrSlug == f.space.Slug || idOrSlug == f.space.ID {
		return f.space, nil
	}
	return nil, core.ErrNotFound
}

func (f *fakeBackend) SpaceAccess(context.Context, *core.Actor, string) (*core.Grant, error) {
	return &core.Grant{Access: core.AccessEdit, Source: "space", Role: core.RoleEditor}, nil
}

// tree is the fake space tree: folder guide with folder note n1, holding note
// n2 and file x1, plus the root files.
func (f *fakeBackend) tree() []*core.TreeItem {
	return []*core.TreeItem{
		{Kind: core.KindFolder, Folder: f.folder, Children: []*core.TreeItem{
			{Kind: core.KindNote, Note: f.note},
			{Kind: core.KindFile, File: f.files["x1"].file},
		}},
		{Kind: core.KindFile, File: f.files["x3"].file},
	}
}

func (f *fakeBackend) SpaceTree(context.Context, *core.Actor, string) ([]*core.TreeItem, error) {
	return f.tree(), nil
}

func (f *fakeBackend) GetItem(_ context.Context, actor *core.Actor, id string) (*core.Item, error) {
	if actor == nil || (actor.User == nil && actor.Link == nil) {
		return nil, core.ErrUnauthorized
	}
	switch {
	case id == f.note.ID:
		return &core.Item{Kind: core.KindNote, Note: f.note, Access: core.AccessView}, nil
	case id == f.folder.ID:
		return &core.Item{Kind: core.KindFolder, Folder: f.folder, Access: core.AccessView}, nil
	case f.files[id] != nil:
		return &core.Item{Kind: core.KindFile, File: f.files[id].file, Access: core.AccessView}, nil
	}
	return nil, core.ErrNotFound
}

func (f *fakeBackend) GetFolder(_ context.Context, _ *core.Actor, id string) (*core.TreeItem, *core.Grant, error) {
	if id != f.folder.ID {
		return nil, nil, core.ErrNotFound
	}
	return f.tree()[0], &core.Grant{Access: core.AccessView}, nil
}

func (f *fakeBackend) OpenFile(_ context.Context, actor *core.Actor, id, versionID string) (*core.File, *core.FileVersion, io.ReadSeekCloser, error) {
	if actor == nil || (actor.User == nil && actor.Link == nil) {
		return nil, nil, nil, core.ErrUnauthorized
	}
	ff := f.files[id]
	if ff == nil {
		return nil, nil, nil, core.ErrNotFound
	}
	var version *core.FileVersion
	body := ff.body
	if versionID != "" {
		if versionID != "v1" {
			return nil, nil, nil, core.ErrNotFound
		}
		body = "old"
		version = &core.FileVersion{ID: "v1", FileID: id, Name: "old-" + ff.file.Name, MIME: "text/plain", Size: 3, SHA256: "oldsum", CreatedAt: testTime}
	}
	return ff.file, version, nopCloser{bytes.NewReader([]byte(body))}, nil
}

func (f *fakeBackend) OpenThumbnail(_ context.Context, actor *core.Actor, id string, width int) (*core.File, io.ReadSeekCloser, string, error) {
	if actor == nil || (actor.User == nil && actor.Link == nil) {
		return nil, nil, "", core.ErrUnauthorized
	}
	ff := f.files[id]
	if ff == nil || ff.file.MIME != "image/png" {
		return nil, nil, "", core.ErrNotFound
	}
	return ff.file, nopCloser{bytes.NewReader([]byte("thumb"))}, "image/webp", nil
}

// nopCloser adds a no-op Close to a bytes.Reader.
type nopCloser struct{ *bytes.Reader }

func (nopCloser) Close() error { return nil }

func (f *fakeBackend) GetNoteByPath(_ context.Context, _ *core.Actor, slug, path string) (*core.Note, *core.Grant, error) {
	if slug == f.space.Slug && path == f.note.Path {
		return f.note, &core.Grant{Access: core.AccessEdit, Source: "space"}, nil
	}
	return nil, nil, core.ErrNotFound
}

func (f *fakeBackend) RenderNote(context.Context, *core.Actor, *core.Note) (*core.Rendered, error) {
	return &core.Rendered{HTML: "<p>Hello <strong>world</strong></p>"}, nil
}

func (f *fakeBackend) ListNotes(context.Context, *core.Actor, string, core.NoteListOptions) ([]*core.Note, error) {
	return nil, nil
}

func (f *fakeBackend) Stats(context.Context, *core.Actor) (*core.Stats, error) {
	return &core.Stats{Users: 2}, nil
}

func (f *fakeBackend) ResolvePublicItem(_ context.Context, token, password string) (*core.Actor, *core.Item, error) {
	switch token {
	case "tok":
		if password != "secret" {
			return nil, nil, core.ErrUnauthorized
		}
		link := &core.PublicLink{ID: "l1", Token: "tok", NoteID: f.note.ID, Access: core.AccessView, HasPassword: true}
		return &core.Actor{Link: link}, &core.Item{Kind: core.KindNote, Note: f.note}, nil
	case "ftok":
		link := &core.PublicLink{ID: "l2", Token: "ftok", NoteID: "x1", Access: core.AccessView}
		return &core.Actor{Link: link}, &core.Item{Kind: core.KindFile, File: f.files["x1"].file}, nil
	case "dtok":
		link := &core.PublicLink{ID: "l3", Token: "dtok", NoteID: "f1", Access: core.AccessView, IncludeChildren: true}
		return &core.Actor{Link: link}, &core.Item{Kind: core.KindFolder, Folder: f.folder}, nil
	}
	return nil, nil, core.ErrNotFound
}

// newTestServer builds a server over backend with a quiet logger.
func newTestServer(t *testing.T, backend core.Backend) *Server {
	t.Helper()
	srv, err := NewServer(Options{
		Backend:     backend,
		Auth:        &api.Authenticator{Backend: backend},
		BaseURL:     "https://notes.example.com",
		MarkdownCSS: ".md-body .chroma { color: red; }",
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return srv
}

// do sends one request through the full handler chain.
func do(t *testing.T, h http.Handler, method, target, session string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, target, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if session != "" {
		req.AddCookie(&http.Cookie{Name: api.SessionCookie, Value: session})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// representativePage fills every field and data key a template reads.
func representativePage(f *fakeBackend) *Page {
	actor := &core.Actor{User: f.admin}
	expires := testTime.Add(24 * time.Hour)
	deleted := testTime
	provider := &core.AIProviderConfig{ID: "p1", Name: "Local", Kind: core.ProviderOllama, BaseURL: "http://localhost:11434", Enabled: true, Source: "db", HasAPIKey: true}
	return &Page{
		Title:        "Representative",
		InstanceName: "Test",
		BaseURL:      "https://notes.example.com",
		Now:          testTime,
		Chrome:       true,
		Actor:        actor,
		User:         f.admin,
		IsAdmin:      true,
		Nav:          "space",
		Spaces:       []*core.Space{f.space},
		Space:        f.space,
		SpaceAccess:  core.AccessManage,
		Tree:         f.tree(),
		Expanded:     map[string]bool{"f1": true, "n2": true},
		Note:         f.note,
		Grant:        &core.Grant{Access: core.AccessManage, Source: "admin"},
		Crumbs:       crumbsFor(f.space, f.note),
		Settings:     &core.Settings{InstanceName: "Test", AllowSignup: true, AIEnabled: true, AIAllowUserKeys: true},
		Flash:        "Saved.",
		Error:        "Something failed",
		Status:       http.StatusNotFound,
		RequestPath:  "/s/demo/n/guide/install",
		Query:        url.Values{"q": {"install"}},
		Data: map[string]any{
			"Rendered":  &core.Rendered{HTML: "<h1 id=\"a\">A</h1>", TOC: []core.Heading{{Level: 1, Text: "A", ID: "a"}, {Level: 2, Text: "B", ID: "b"}, {Level: 3, Text: "C", ID: "c"}}},
			"ShowTOC":   true,
			"Children":  []*core.Note{f.note},
			"Backlinks": []*core.Backlink{{FromNote: f.parent, Context: "see install"}},
			"Comments": []*core.Comment{
				{ID: "c1", Body: "First", Author: f.member, CreatedAt: testTime, Anchor: &core.CommentAnchor{Exact: "Hello", End: 5}, AnchorState: core.AnchorExact, Children: []*core.Comment{{ID: "c2", Body: "Reply", CreatedAt: testTime}}},
				{ID: "c3", Body: "Gone", CreatedAt: testTime, Anchor: &core.CommentAnchor{Exact: "removed text"}, AnchorState: core.AnchorOrphaned},
			},
			"File":          f.files["x1"].file,
			"Folder":        f.folder,
			"Listing":       []publicChild{{Kind: core.KindFolder, Name: "sub", URL: "/p/tok/i/f9"}, {Kind: core.KindFile, Name: "a.png", URL: "/p/tok/raw/x1/a.png", Size: 3, MIME: "image/png"}},
			"Viewer":        "image",
			"RawURL":        "/p/tok/raw/x1/shot.png",
			"DownloadURL":   "/p/tok/raw/x1/shot.png?download=1",
			"Shares":        []*core.Share{{ID: "s1", UserID: "u2", User: f.member, Note: f.note, Access: core.AccessEdit, CreatedAt: testTime}},
			"Links":         []*core.PublicLink{{ID: "l1", Token: "tok", Access: core.AccessView, HasPassword: true, Views: 3}},
			"Author":        f.member,
			"Tags":          []*core.TagCount{{Tag: "setup", Count: 2}},
			"Tag":           "setup",
			"Roots":         []*core.Note{f.parent},
			"Recent":        []*core.Note{f.note},
			"Notes":         []*core.Note{{ID: "n3", Title: "Old", SpaceSlug: "demo", Path: "old", DeletedAt: &deleted}},
			"Members":       []*core.SpaceMember{{UserID: "u2", Role: core.RoleEditor, User: f.member}},
			"Roles":         core.Roles,
			"Sessions":      []*core.Session{{ID: "se1", UserAgent: "Firefox", IP: "127.0.0.1", LastSeen: testTime}},
			"Tokens":        []*core.APIToken{{ID: "t1", Name: "backup", Prefix: "ms_ab", CreatedAt: testTime, ExpiresAt: &expires}},
			"NewToken":      "ms_secret_value",
			"Providers":     []*core.AIProviderConfig{provider},
			"AIProviders":   []*core.AIProviderConfig{provider},
			"AITasks":       core.AITasks,
			"Tasks":         core.AITasks,
			"AI":            sampleAIView(),
			"AllowUserKeys": true,
			"ProviderKinds": core.AIProviderKinds,
			"Stats":         &core.Stats{Users: 2, Spaces: 1, Notes: 5, DiskBytes: 4096, IndexedAt: testTime},
			"Users":         []*core.User{f.admin, f.member},
			"Invites":       []*core.Invite{{ID: "i1", Email: "new@example.com", Token: "inv", ExpiresAt: expires}},
			"InviteSpaces":  []*core.Space{f.space},
			"Pagination":    &Pagination{Offset: 50, Limit: 50, Count: 50, Total: 120, PrevURL: "/admin/users", NextURL: "/admin/users?offset=100"},
			"Search":        "install",
			"Modes":         searchModes,
			"Mode":          "text",
			"SpaceFilter":   "demo",
			"TagFilter":     "setup",
			"Hits":          []*core.SearchHit{{Note: f.note, Snippet: "how to <mark>install</mark>"}},
			"Token":         "tok",
			"Next":          "/s/demo",
			"Email":         "mel@example.com",
			"Name":          "Mel",
			"Invite":        "inv",
			"Detail":        "more detail",
			"Empty":         "Nothing here.",
			"Editing":       true,
			"Usage":         sampleUsageView(),
			"AdminUsage":    sampleAdminUsageView(),
		},
	}
}

func TestEveryTemplateExecutes(t *testing.T) {
	f := newFake()
	srv := newTestServer(t, f)
	names := srv.PageNames()
	want := []string{
		"admin", "admin/ai", "admin/module", "admin/settings", "admin/usage", "admin/users", "ai", "confirm", "editor", "error",
		"login", "module", "note", "offline", "offline/files", "profile", "public", "public/item", "public/password", "recent", "search",
		"sessions", "settings", "setup", "shared", "shell", "signup", "space/home", "space/settings", "spaces", "starred",
		"tag", "tokens", "trash", "usage",
	}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("page templates = %v, want %v", names, want)
	}
	for _, name := range names {
		for _, chrome := range []bool{true, false} {
			t.Run(name, func(t *testing.T) {
				p := representativePage(f)
				p.Chrome = chrome
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				rec := httptest.NewRecorder()
				srv.Render(rec, req, http.StatusOK, name, p)
				body := rec.Body.String()
				if rec.Code != http.StatusOK {
					t.Fatalf("status %d, body %q", rec.Code, body)
				}
				if !strings.Contains(body, "</html>") {
					t.Fatalf("page is incomplete: %q", body)
				}
				if strings.Contains(body, "ZgotmplZ") {
					t.Fatalf("template produced an unsafe value marker")
				}
			})
		}
	}
}

func TestLoginSetsCookieAndRedirects(t *testing.T) {
	srv := newTestServer(t, newFake())
	h := srv.Handler()

	form := url.Values{"email": {"mel@example.com"}, "password": {"correct horse"}, "next": {"/s/demo"}}
	rec := do(t, h, http.MethodPost, "/login", "", form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/s/demo" {
		t.Fatalf("Location %q, want /s/demo", loc)
	}
	var found bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == api.SessionCookie && c.Value == memberSession && c.HttpOnly {
			found = true
		}
	}
	if !found {
		t.Fatalf("session cookie not set: %v", rec.Header().Values("Set-Cookie"))
	}

	form.Set("next", "//evil.com/steal")
	rec = do(t, h, http.MethodPost, "/login", "", form)
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Fatalf("Location %q for a foreign next, want /", loc)
	}

	bad := url.Values{"email": {"mel@example.com"}, "password": {"wrong"}}
	rec = do(t, h, http.MethodPost, "/login", "", bad)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d for bad password, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `value="mel@example.com"`) {
		t.Fatalf("submitted email not preserved")
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatalf("cookie set on failed login")
	}
}

func TestAnonymousRedirectsToLogin(t *testing.T) {
	h := newTestServer(t, newFake()).Handler()
	rec := do(t, h, http.MethodGet, "/s/demo/n/guide/install?edit=1", "", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", rec.Code)
	}
	want := "/login?next=" + url.QueryEscape("/s/demo/n/guide/install?edit=1")
	if loc := rec.Header().Get("Location"); loc != want {
		t.Fatalf("Location %q, want %q", loc, want)
	}
}

func TestNonAdminForbiddenOnAdmin(t *testing.T) {
	h := newTestServer(t, newFake()).Handler()
	for _, path := range []string{"/admin", "/admin/users", "/admin/ai", "/admin/settings"} {
		rec := do(t, h, http.MethodGet, path, memberSession, nil)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "You do not have access") {
			t.Errorf("%s: error page not rendered", path)
		}
	}
	rec := do(t, h, http.MethodGet, "/admin", adminSession, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin status %d, want 200", rec.Code)
	}
}

func TestNoteViewRenders(t *testing.T) {
	h := newTestServer(t, newFake()).Handler()
	rec := do(t, h, http.MethodGet, "/s/demo/n/guide/install", memberSession, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"<title>Install &middot; Test Space</title>",
		"<p>Hello <strong>world</strong></p>",
		`<a href="/s/demo/n/guide">Guide</a>`,
		`<span aria-current="page">Install</span>`,
		`href="/s/demo/n/guide/install?edit=1"`,
		`class="md-body"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("note page is missing %q", want)
		}
	}
	if strings.Contains(body, `id="share"`) {
		t.Errorf("share panel shown without manage access")
	}

	rec = do(t, h, http.MethodGet, "/s/demo/n/missing", memberSession, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing note status %d, want 404", rec.Code)
	}
}

func TestPublicLinkPasswordFlow(t *testing.T) {
	h := newTestServer(t, newFake()).Handler()

	rec := do(t, h, http.MethodGet, "/p/tok", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("X-Robots-Tag"), "noindex") {
		t.Fatalf("X-Robots-Tag missing")
	}
	if !strings.Contains(rec.Body.String(), `name="password"`) {
		t.Fatalf("password form not shown")
	}

	rec = do(t, h, http.MethodPost, "/p/tok", "", url.Values{"password": {"nope"}})
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "does not match") {
		t.Fatalf("wrong password: status %d", rec.Code)
	}

	rec = do(t, h, http.MethodPost, "/p/tok", "", url.Values{"password": {"secret"}})
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("right password: status %d", rec.Code)
	}
	if !strings.Contains(body, "Install") || !strings.Contains(body, "<strong>world</strong>") {
		t.Fatalf("note not rendered: %s", body)
	}
	if strings.Contains(body, "?edit=1") || strings.Contains(body, `class="sidebar"`) {
		t.Fatalf("public view shows editing controls or the sidebar")
	}

	rec = do(t, h, http.MethodGet, "/p/unknown", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown token status %d, want 404", rec.Code)
	}
}

func TestStaticAssets(t *testing.T) {
	h := newTestServer(t, newFake()).Handler()

	rec := do(t, h, http.MethodGet, "/static/css/markdown.css", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("markdown.css status %d", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=3600") || !strings.Contains(cc, "immutable") {
		t.Fatalf("Cache-Control %q", cc)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Fatalf("Content-Type %q", ct)
	}
	if !strings.Contains(rec.Body.String(), ".md-body") {
		t.Fatalf("markdown.css body looks wrong")
	}

	for path, want := range map[string]string{
		"/static/vendor/primer/primitives-11.10.0.css": "--base-size-16",
		"/static/vendor/primer/light-11.10.0.css":      `[data-color-mode="light"][data-light-theme="light"]`,
		"/static/vendor/primer/dark-11.10.0.css":       `[data-color-mode="dark"][data-dark-theme="dark"]`,
		"/static/theme.css":                            "--fgColor-accent: #bf3989",
		"/static/css/base.css":                         "--text: var(--fgColor-default)",
	} {
		rec := do(t, h, http.MethodGet, path, "", nil)
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/css") || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: status %d, type %q, want body with %q", path, rec.Code, rec.Header().Get("Content-Type"), want)
		}
	}

	rec = do(t, h, http.MethodGet, "/static/highlight.css", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), ".chroma") {
		t.Fatalf("highlight.css status %d body %q", rec.Code, rec.Body.String())
	}

	for _, path := range []string{"/static/missing.js", "/static/../web.go"} {
		if rec := do(t, h, http.MethodGet, path, "", nil); rec.Code == http.StatusOK {
			t.Errorf("%s: status 200, want an error", path)
		}
	}

	rec = do(t, h, http.MethodGet, "/healthz", "", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("healthz %d %q", rec.Code, rec.Body.String())
	}
}

func TestSafeNext(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", "/"},
		{"/", "/"},
		{"/s/demo/n/guide?edit=1", "/s/demo/n/guide?edit=1"},
		{"//evil.com", "/"},
		{"//evil.com/path", "/"},
		{"http://evil.com", "/"},
		{"https://evil.com/s/demo", "/"},
		{"/\\evil.com", "/"},
		{"javascript:alert(1)", "/"},
		{"relative/path", "/"},
		{"/ok\r\nSet-Cookie: x=y", "/"},
	}
	for _, tt := range tests {
		if got := safeNext(tt.in); got != tt.want {
			t.Errorf("safeNext(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFuncMapHelpers(t *testing.T) {
	space := &core.Space{Slug: "demo", Icon: "S"}
	note := &core.Note{ID: "n1", SpaceSlug: "demo", Path: "guide/getting started.md", Icon: ""}
	grant := &core.Grant{Access: core.AccessComment}

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"noteURL space and note", noteURL(space, note), "/s/demo/n/guide/getting%20started"},
		{"noteURL slug and path", noteURL("demo", "a/b"), "/s/demo/n/a/b"},
		{"noteURL note twice", noteURL(note, note), "/s/demo/n/guide/getting%20started"},
		{"noteURL without space", noteURL(nil, &core.Note{ID: "x"}), "/n/x"},
		{"spaceURL", spaceURL(space), "/s/demo"},
		{"spaceURL nil", spaceURL(nil), "/spaces"},
		{"tagURL", tagURL("demo", "go"), "/s/demo/t/go"},
		{"pluralize one", pluralize(1, "note", "notes"), "1 note"},
		{"pluralize many", pluralize(3, "note", "notes"), "3 notes"},
		{"pluralize zero", pluralize(0, "note", "notes"), "0 notes"},
		{"truncate short", truncate(10, "short"), "short"},
		{"truncate long", truncate(5, "abcdefgh"), "abcde…"},
		{"truncate runes", truncate(2, "äöü"), "äö…"},
		{"bytes small", humanBytes(int64(512)), "512 B"},
		{"bytes kb", humanBytes(int64(2048)), "2.0 KB"},
		{"bytes mb", humanBytes(int64(50 << 20)), "50 MB"},
		{"iconOr empty", iconOr(note), "◦"},
		{"iconOr fallback", iconOr(note, "#"), "#"},
		{"iconOr space", iconOr(space), "S"},
		{"initials user", initials(&core.User{Name: "Ada Lovelace"}), "AL"},
		{"initials nil", initials((*core.User)(nil)), "?"},
		{"hasAccess below", hasAccess(grant, "edit"), false},
		{"hasAccess equal", hasAccess(grant, "comment"), true},
		{"hasAccess level", hasAccess(core.AccessManage, core.AccessEdit), true},
		{"hasAccess nil grant", hasAccess((*core.Grant)(nil), "view"), false},
		{"accessLabel", accessLabel(core.AccessEdit), "Can edit"},
		{"roleLabel", roleLabel(core.RoleCommenter), "Commenter"},
		{"join", join(", ", []string{"a", "b"}), "a, b"},
		{"join nil", join(", ", nil), ""},
		{"formatTime", formatTime(testTime), "13 Sep 2026, 09:00 UTC"},
		{"formatTime zero", formatTime(time.Time{}), ""},
		{"timeAgo now", timeAgo(time.Now().Add(-10 * time.Second)), "just now"},
		{"timeAgo hours", timeAgo(time.Now().Add(-3 * time.Hour)), "3 hours ago"},
		{"timeAgo days", timeAgo(time.Now().Add(-72 * time.Hour)), "3 days ago"},
		{"stamp", stamp(testTime), "2026-09-13T09:00:00Z"},
		{"cssColor hex", string(cssColor("#4f6bed")), "#4f6bed"},
		{"cssColor unsafe", string(cssColor("red;background:url(x)")), ""},
		{"orDefault", orDefault("", "x"), "x"},
		{"fileURL", fileURL(&core.File{ID: "a1", Name: "a b.png"}, space), "/files/demo/a1/a%20b.png"},
		{"fileURL backend", fileURL(&core.File{ID: "a1", Name: "x", URL: "/files/o/a1/x"}, space), "/files/o/a1/x"},
		{"entryURL", entryURL("demo", "f", "docs/a b.pdf"), "/s/demo/f/docs/a%20b.pdf"},
		{"safeHTML", string(safeHTML("<b>x</b>")), "<b>x</b>"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %#v, want %#v", tt.name, tt.got, tt.want)
		}
	}

	d := dict("A", 1, "B", "two", "odd")
	if len(d) != 2 || d["A"] != 1 || d["B"] != "two" {
		t.Errorf("dict = %v", d)
	}
}

func TestCrumbsAndExpansion(t *testing.T) {
	f := newFake()
	crumbs := crumbsFor(f.space, f.note)
	if len(crumbs) != 2 || crumbs[0].Name != "Guide" || crumbs[0].URL != "/s/demo/n/guide" ||
		crumbs[1].Name != "Install" || crumbs[1].URL != "" {
		t.Fatalf("crumbs = %+v", crumbs)
	}
	tree, _ := f.SpaceTree(context.Background(), nil, "sp1")
	open := expandedFor(tree, "n2")
	if !open["f1"] || !open["n2"] || len(open) != 2 {
		t.Fatalf("expanded = %v", open)
	}
	if open := expandedFor(tree, "n1"); !open["f1"] || len(open) != 1 {
		t.Fatalf("expanded for the folder note = %v", open)
	}
	if got := expandedFor(tree, "absent"); len(got) != 0 {
		t.Fatalf("expanded for a missing note = %v", got)
	}
}

func TestSplitTags(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"go, web", "go|web"},
		{"#go,go,\n  docs ", "go|docs"},
	}
	for _, tt := range tests {
		if got := strings.Join(splitTags(tt.in), "|"); got != tt.want {
			t.Errorf("splitTags(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestStatusMapping(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{core.ErrNotFound, http.StatusNotFound},
		{core.ErrForbidden, http.StatusForbidden},
		{core.ErrUnauthorized, http.StatusUnauthorized},
		{core.Invalid("title", "required"), http.StatusBadRequest},
		{core.ErrConflict, http.StatusConflict},
		{io.EOF, http.StatusInternalServerError},
	}
	for _, tt := range tests {
		if got := statusFor(tt.err); got != tt.want {
			t.Errorf("statusFor(%v) = %d, want %d", tt.err, got, tt.want)
		}
	}
}

func TestAppShellHasNoUserData(t *testing.T) {
	f := newFake()
	h := newTestServer(t, f).Handler()
	for _, session := range []string{"", memberSession} {
		rec := do(t, h, http.MethodGet, "/app-shell", session, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("session %q: status %d", session, rec.Code)
		}
		body := rec.Body.String()
		for _, want := range []string{
			`<script type="module" src="/static/js/shell.js"></script>`,
			`<script type="module" src="/static/js/boot.js"></script>`,
			`href="/manifest.webmanifest"`,
			`data-color-mode="auto" data-light-theme="light" data-dark-theme="dark"`,
			`href="/static/vendor/primer/primitives-11.10.0.css"`,
			`href="/static/theme.css"`,
			`href="/static/css/base.css"`,
			`href="/static/css/shell.css"`,
			`href="/static/css/editor.css"`,
			`href="/static/css/panels.css"`,
			`href="/static/highlight.css"`,
			`data-shell-root`,
			`<title>Test Space</title>`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("session %q: shell is missing %q", session, want)
			}
		}
		for _, leak := range []string{f.member.Email, f.member.Name, f.member.ID, "data-user-id", f.space.Name, "/static/app.js"} {
			if strings.Contains(body, leak) {
				t.Errorf("session %q: shell contains %q", session, leak)
			}
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("session %q: Cache-Control %q, want no-cache", session, cc)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("session %q: Content-Type %q", session, ct)
		}
	}
}

func TestOfflinePage(t *testing.T) {
	f := newFake()
	h := newTestServer(t, f).Handler()
	rec := do(t, h, http.MethodGet, "/offline", memberSession, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"This page needs a connection", `href="/"`, `<script type="module" src="/static/js/boot.js"></script>`} {
		if !strings.Contains(body, want) {
			t.Errorf("offline page is missing %q", want)
		}
	}
	for _, leak := range []string{f.member.Email, f.member.Name, f.member.ID, "data-user-id", `class="sidebar"`} {
		if strings.Contains(body, leak) {
			t.Errorf("offline page contains %q", leak)
		}
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control %q, want no-cache", cc)
	}
}

func TestEditorHasOneDoneButton(t *testing.T) {
	h := newTestServer(t, newFake()).Handler()
	rec := do(t, h, http.MethodGet, "/s/demo/n/guide/install?edit=1", memberSession, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	start := strings.Index(body, `<form class="editor"`)
	end := strings.Index(body[max(start, 0):], "</form>")
	if start < 0 || end < 0 {
		t.Fatalf("editor form not found")
	}
	if n := strings.Count(body[start:start+end], `type="submit"`); n != 1 {
		t.Errorf("editor form has %d submit buttons, want 1", n)
	}
	if !strings.Contains(body, `data-editor-done`) || !strings.Contains(body, `>Done</button>`) {
		t.Errorf("editor is missing the Done submit button")
	}
	for _, gone := range []string{"data-save-now", ">Save</button>"} {
		if strings.Contains(body, gone) {
			t.Errorf("editor still contains %q", gone)
		}
	}
	if !strings.Contains(body, `data-save-status`) {
		t.Errorf("editor is missing the autosave status")
	}
	// The form posts to the note, so Done saves and returns with scripting off.
	if !strings.Contains(body, `<form class="editor" method="post" action="/s/demo/n/guide/install"`) {
		t.Errorf("editor form no longer posts to the note")
	}
}

func TestServerPagesDropDuplicateControls(t *testing.T) {
	h := newTestServer(t, newFake()).Handler()

	rec := do(t, h, http.MethodGet, "/s/demo", memberSession, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("space home status %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Count(body, `value="note-create"`) != 1 {
		t.Errorf("space home should offer exactly one new page control")
	}
	if strings.Contains(body, "data-quick-open") {
		t.Errorf("top bar still has the quick open button next to search")
	}
	if strings.Contains(body, `<a href="/search"`) {
		t.Errorf("sidebar still links to search next to the top bar search field")
	}
	if strings.Count(body, `data-header-search`) != 2 {
		t.Errorf("app header Search button missing")
	}
	if !strings.Contains(body, `data-sidebar-toggle`) || !strings.Contains(body, `aria-controls="sidebar"`) {
		t.Errorf("sidebar toggle missing")
	}

	rec = do(t, h, http.MethodGet, "/search?q=install", memberSession, nil)
	if strings.Contains(rec.Body.String(), `data-header-search`) {
		t.Errorf("search page shows the header Search button above its own form")
	}
}

func TestFilesRouteServesShell(t *testing.T) {
	h := newTestServer(t, newFake()).Handler()
	rec := do(t, h, http.MethodGet, "/s/demo/files", memberSession, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `src="/static/js/shell.js"`) {
		t.Fatalf("files route does not serve the app shell")
	}
}

func TestFormFailRedirectsWithField(t *testing.T) {
	tests := []struct {
		back string
		want string
	}{
		{"/settings/tokens", "/settings/tokens?error=Enter+a+name.&field=name"},
		{"/s/demo/n/a#share", "/s/demo/n/a?error=Enter+a+name.&field=name#share"},
		{"/x?flash=1", "/x?flash=1&error=Enter+a+name.&field=name"},
	}
	srv := newTestServer(t, newFake())
	for _, tt := range tests {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		srv.formFail(rec, req, tt.back, core.Invalid("name", "enter a name"), "fallback")
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != tt.want {
			t.Errorf("formFail(%q) = %d %q, want %q", tt.back, rec.Code, rec.Header().Get("Location"), tt.want)
		}
	}
}

func TestFieldErrorsRenderInline(t *testing.T) {
	h := newTestServer(t, newFake()).Handler()
	// Refused credentials belong to neither field, so they show in a Banner
	// at the top of the form (docs/primer.md 6.6).
	rec := do(t, h, http.MethodPost, "/login", "", url.Values{"email": {"mel@example.com"}, "password": {"wrong"}})
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `id="login-message-title"`) {
		t.Errorf("login does not show the refused credentials in the form: status %d", rec.Code)
	}
	rec = do(t, h, http.MethodPost, "/signup", "", url.Values{"email": {"a@example.com"}, "password": {"longenough"}, "passwordConfirm": {"different"}, "name": {"A"}})
	body := rec.Body.String()
	if rec.Code != http.StatusBadRequest || !strings.Contains(body, `id="signup-password-confirm-error"`) || !strings.Contains(body, `aria-invalid="true"`) {
		t.Errorf("signup mismatch: status %d, inline error missing", rec.Code)
	}
}

// commentFake serves comment threads and records posted comments.
type commentFake struct {
	*fakeBackend
	threads []*core.Comment
	posted  []core.CommentInput
}

func (c *commentFake) ListComments(context.Context, *core.Actor, string) ([]*core.Comment, error) {
	return c.threads, nil
}

func (c *commentFake) CreateComment(_ context.Context, _ *core.Actor, in core.CommentInput) (*core.Comment, error) {
	c.posted = append(c.posted, in)
	return &core.Comment{ID: "c9", NoteID: in.NoteID, ParentID: in.ParentID, Body: in.Body}, nil
}

func TestNoteCommentsInSideColumn(t *testing.T) {
	f := newFake()
	deleted := testTime
	fake := &commentFake{fakeBackend: f, threads: []*core.Comment{
		{ID: "c1", Body: "Is this right?", Author: f.member, CreatedAt: testTime, Anchor: &core.CommentAnchor{Exact: "Hello", End: 5}, AnchorState: core.AnchorExact,
			Children: []*core.Comment{{ID: "c2", ParentID: "c1", Body: "Yes", Author: f.admin, CreatedAt: testTime}}},
		{ID: "c3", Body: "General remark", Author: f.member, CreatedAt: testTime},
		{ID: "c4", Body: "About old text", Author: f.member, CreatedAt: testTime, Anchor: &core.CommentAnchor{Exact: "old words"}, AnchorState: core.AnchorOrphaned},
		{ID: "c5", CreatedAt: testTime, DeletedAt: &deleted},
		{ID: "c6", Body: "Fixed now", UserID: f.admin.ID, Author: f.admin, CreatedAt: testTime, UpdatedAt: testTime, Resolved: true, ResolvedBy: f.admin.ID,
			Anchor: &core.CommentAnchor{Exact: "gone words"}, AnchorState: core.AnchorOrphaned},
	}}
	h := newTestServer(t, fake).Handler()
	rec := do(t, h, http.MethodGet, "/s/demo/n/guide/install", memberSession, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	rail := strings.Index(body, `<aside class="note-rail"`)
	comments := strings.Index(body, `id="comments"`)
	if rail < 0 || comments < rail {
		t.Fatalf("comments are not inside the side column: rail at %d, comments at %d", rail, comments)
	}
	if quote := strings.Index(body, `<blockquote class="ms-quote"><span class="ms-quote-text">Hello</span></blockquote>`); quote < 0 || quote > strings.Index(body, "Is this right?") {
		t.Errorf("the quoted passage is missing or not above its thread")
	}
	for _, want := range []string{
		`<blockquote class="ms-quote is-missing"><span class="ms-quote-caption">Removed from the note</span><span class="ms-quote-text">old words</span></blockquote>`,
		`<li class="comment ms-card" id="comment-c1"`,
		`name="parentId" value="c1"`,
		`id="comment-c3"`,
		// A resolved thread keeps its passage, names who resolved it and offers Reopen.
		`<blockquote class="ms-quote is-missing"><span class="ms-quote-caption">Text no longer in the note</span><span class="ms-quote-text">gone words</span></blockquote>`,
		`Resolved by ` + f.admin.Name + `, `,
		`name="resolved" value="false"`,
		`<span class="btn-label">Reopen</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("note page is missing %q", want)
		}
	}
	if strings.Contains(body, `id="comment-c5"`) {
		t.Errorf("a deleted thread without replies is still listed")
	}
	if strings.Contains(body, `href="#comments"`) {
		t.Errorf("the More menu still has a Comments link next to the side column")
	}
}

func TestCommentFormRedirectsToNewComment(t *testing.T) {
	fake := &commentFake{fakeBackend: newFake()}
	h := newTestServer(t, fake).Handler()
	form := url.Values{"action": {"comment"}, "body": {"Looks good"}, "parentId": {"c1"}}
	rec := do(t, h, http.MethodPost, "/s/demo/n/guide/install", memberSession, form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/s/demo/n/guide/install#comment-c9" {
		t.Errorf("redirect to %q", got)
	}
	if len(fake.posted) != 1 || fake.posted[0].ParentID != "c1" || fake.posted[0].Anchor != nil {
		t.Errorf("posted %+v", fake.posted)
	}
}

func TestTreeRendersFoldersNotesAndFiles(t *testing.T) {
	h := newTestServer(t, newFake()).Handler()
	rec := do(t, h, http.MethodGet, "/s/demo/n/guide/install", memberSession, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`data-tree-item="f1" data-kind="folder"`,
		`href="/s/demo/n/guide/index"`,
		`data-tree-item="n2" data-kind="note"`,
		`href="/s/demo/n/guide/install" aria-current="page"`,
		`data-tree-item="x1" data-kind="file"`,
		`href="/s/demo/f/guide/shot.png"`,
		`href="/s/demo/f/report.pdf"`,
		`<span class="tree-label">shot.png</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("tree is missing %q", want)
		}
	}
	if strings.Count(body, `data-tree-item="n1"`) != 0 {
		t.Errorf("the folder note is listed as a child row")
	}
	// The folder above the current note is open.
	if !strings.Contains(body, `class="tree-item tree-folder open" data-tree-item="f1"`) {
		t.Errorf("folder of the current note is not expanded")
	}

	f := newFake()
	f.folder.IndexNoteID = ""
	rec = do(t, newTestServer(t, f).Handler(), http.MethodGet, "/s/demo/n/guide/install", memberSession, nil)
	if !strings.Contains(rec.Body.String(), `href="/s/demo/d/guide"`) {
		t.Errorf("a folder without a folder note does not link to its folder view")
	}
}

func TestFileHeaders(t *testing.T) {
	h := newTestServer(t, newFake()).Handler()
	tests := []struct {
		target      string
		disposition string
		sandboxed   bool
		ct          string
	}{
		{"/files/demo/x1/shot.png", "inline", true, "image/png"},
		{"/files/demo/x2/evil.svg", "attachment", true, "image/svg+xml"},
		{"/files/demo/x3/report.pdf", "inline", false, "application/pdf"},
		{"/files/demo/x4/page.html", "attachment", true, "text/html"},
		{"/files/demo/x1/shot.png?download=1", "attachment", true, "image/png"},
		{"/files/demo/x3/report.pdf?download=1", "attachment", false, "application/pdf"},
	}
	for _, tt := range tests {
		rec := do(t, h, http.MethodGet, tt.target, memberSession, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", tt.target, rec.Code)
			continue
		}
		hd := rec.Header()
		if got := hd.Get("Content-Disposition"); !strings.HasPrefix(got, tt.disposition) {
			t.Errorf("%s: Content-Disposition %q, want %s", tt.target, got, tt.disposition)
		}
		if csp := hd.Get("Content-Security-Policy"); tt.sandboxed != strings.HasPrefix(csp, "sandbox;") {
			t.Errorf("%s: Content-Security-Policy %q, sandboxed want %v", tt.target, csp, tt.sandboxed)
		}
		if hd.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: nosniff missing", tt.target)
		}
		if got := hd.Get("Content-Type"); got != tt.ct {
			t.Errorf("%s: Content-Type %q, want %q", tt.target, got, tt.ct)
		}
		if !strings.HasPrefix(hd.Get("Cache-Control"), "private") {
			t.Errorf("%s: Cache-Control %q, want private", tt.target, hd.Get("Cache-Control"))
		}
	}
	rec := do(t, h, http.MethodGet, "/files/demo/x1/shot.png", memberSession, nil)
	if rec.Header().Get("ETag") != `"abc123"` {
		t.Errorf("ETag %q", rec.Header().Get("ETag"))
	}
	rec = do(t, h, http.MethodGet, "/files/demo/x1/shot.png?v=v1", memberSession, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "old" || rec.Header().Get("ETag") != `"oldsum"` ||
		!strings.Contains(rec.Header().Get("Content-Disposition"), "old-shot.png") {
		t.Errorf("version: status %d body %q headers %v", rec.Code, rec.Body.String(), rec.Header())
	}
	if rec := do(t, h, http.MethodGet, "/files/demo/missing/x.png", memberSession, nil); rec.Code != http.StatusNotFound {
		t.Errorf("missing file status %d", rec.Code)
	}
	if rec := do(t, h, http.MethodGet, "/files/demo/x1/shot.png", "", nil); rec.Code != http.StatusSeeOther {
		t.Errorf("anonymous file status %d, want a login redirect", rec.Code)
	}
}

func TestFileRangeRequest(t *testing.T) {
	h := newTestServer(t, newFake()).Handler()
	req := httptest.NewRequest(http.MethodGet, "/files/demo/x3/report.pdf", nil)
	req.AddCookie(&http.Cookie{Name: api.SessionCookie, Value: memberSession})
	req.Header.Set("Range", "bytes=0-3")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status %d, want 206", rec.Code)
	}
	if rec.Body.String() != "%PDF" || !strings.HasPrefix(rec.Header().Get("Content-Range"), "bytes 0-3/") {
		t.Fatalf("body %q Content-Range %q", rec.Body.String(), rec.Header().Get("Content-Range"))
	}
}

func TestThumbWidths(t *testing.T) {
	h := newTestServer(t, newFake()).Handler()
	for _, tt := range []struct {
		target string
		want   int
	}{
		{"/thumbs/demo/x1", http.StatusOK},
		{"/thumbs/demo/x1?w=128", http.StatusOK},
		{"/thumbs/demo/x1?w=1024", http.StatusOK},
		{"/thumbs/demo/x1?w=100", http.StatusBadRequest},
		{"/thumbs/demo/x1?w=abc", http.StatusBadRequest},
		{"/thumbs/demo/x3?w=256", http.StatusNotFound},
	} {
		rec := do(t, h, http.MethodGet, tt.target, memberSession, nil)
		if rec.Code != tt.want {
			t.Errorf("%s: status %d, want %d", tt.target, rec.Code, tt.want)
			continue
		}
		if tt.want == http.StatusOK {
			if rec.Header().Get("Content-Type") != "image/webp" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Errorf("%s: headers %v", tt.target, rec.Header())
			}
		}
	}
}

func TestItemPermalinkRedirects(t *testing.T) {
	h := newTestServer(t, newFake()).Handler()
	for id, want := range map[string]string{
		"n2": "/s/demo/n/guide/install",
		"f1": "/s/demo/d/guide",
		"x1": "/s/demo/f/guide/shot.png",
	} {
		rec := do(t, h, http.MethodGet, "/i/"+id, memberSession, nil)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != want {
			t.Errorf("/i/%s: %d %q, want %q", id, rec.Code, rec.Header().Get("Location"), want)
		}
	}
	if rec := do(t, h, http.MethodGet, "/i/x1", "", nil); rec.Code != http.StatusSeeOther ||
		!strings.HasPrefix(rec.Header().Get("Location"), "/login?next=") {
		t.Errorf("anonymous permalink: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := do(t, h, http.MethodGet, "/i/nope", memberSession, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown item status %d", rec.Code)
	}
	for _, p := range []string{"/s/demo/f/guide/shot.png", "/s/demo/d/guide"} {
		rec := do(t, h, http.MethodGet, p, memberSession, nil)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `src="/static/js/shell.js"`) {
			t.Errorf("%s: status %d, want the app shell", p, rec.Code)
		}
	}
}

func TestPublicFileAndFolderPages(t *testing.T) {
	h := newTestServer(t, newFake()).Handler()
	rec := do(t, h, http.MethodGet, "/p/ftok", "", nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("file page status %d", rec.Code)
	}
	for _, want := range []string{`<img src="/p/ftok/raw/x1/shot.png"`, `href="/p/ftok/raw/x1/shot.png?download=1"`, "shot.png", "image/png"} {
		if !strings.Contains(body, want) {
			t.Errorf("public file page is missing %q", want)
		}
	}
	if strings.Contains(body, `class="sidebar"`) {
		t.Errorf("public file page shows the sidebar")
	}
	rec = do(t, h, http.MethodGet, "/p/ftok/raw/x1/shot.png", "", nil)
	if rec.Code != http.StatusOK || rec.Header().Get("X-Content-Type-Options") != "nosniff" ||
		!strings.HasPrefix(rec.Header().Get("Content-Security-Policy"), "sandbox;") {
		t.Errorf("public raw: %d %v", rec.Code, rec.Header())
	}
	if rec := do(t, h, http.MethodGet, "/p/ftok/thumb/x1?w=128", "", nil); rec.Code != http.StatusOK {
		t.Errorf("public thumb status %d", rec.Code)
	}
	if rec := do(t, h, http.MethodGet, "/p/ftok/thumb/x1?w=7", "", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("public thumb bad width status %d", rec.Code)
	}
	if rec := do(t, h, http.MethodGet, "/p/nope/raw/x1/shot.png", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown token raw status %d", rec.Code)
	}

	rec = do(t, h, http.MethodGet, "/p/dtok", "", nil)
	body = rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("folder page status %d", rec.Code)
	}
	for _, want := range []string{`href="/p/dtok/i/n2"`, `>Install</a>`, `href="/p/dtok/raw/x1/shot.png"`} {
		if !strings.Contains(body, want) {
			t.Errorf("public folder page is missing %q", want)
		}
	}
	rec = do(t, h, http.MethodGet, "/p/dtok/i/n2", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<strong>world</strong>") {
		t.Errorf("public child note: status %d", rec.Code)
	}
	rec = do(t, h, http.MethodGet, "/p/dtok/i/f1", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `href="/p/dtok/i/n2"`) {
		t.Errorf("public child folder: status %d", rec.Code)
	}
}

func TestPublicLinkPasswordCookie(t *testing.T) {
	h := newTestServer(t, newFake()).Handler()
	rec := do(t, h, http.MethodPost, "/p/tok", "", url.Values{"password": {"secret"}})
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "ms_link_tok" {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/p/tok" || cookie.MaxAge != 3600 {
		t.Fatalf("link cookie = %+v", cookie)
	}
	req := httptest.NewRequest(http.MethodGet, "/p/tok", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<strong>world</strong>") {
		t.Fatalf("GET with the link cookie: status %d", rec.Code)
	}
	if linkCookieName("bad token;") != "" {
		t.Errorf("a token with unsafe characters produced a cookie name")
	}
}

// TestErrorPageShowsHeadlineOnce checks that the error Blankslate carries the
// status text and that no flash Banner repeats it, also when the query string
// holds an error message.
func TestErrorPageShowsHeadlineOnce(t *testing.T) {
	h := newTestServer(t, newFake()).Handler()
	rec := do(t, h, http.MethodGet, "/no-such-page?error=Something+broke", memberSession, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
	body := rec.Body.String()
	headline := statusText(http.StatusNotFound)
	if !strings.Contains(body, `<h1 class="blankslate-heading">`+headline+`</h1>`) {
		t.Errorf("error page is missing the Blankslate heading %q", headline)
	}
	if strings.Contains(body, `data-flash="error"`) || strings.Contains(body, "Something broke") {
		t.Errorf("error page shows a flash Banner above the Blankslate")
	}
	if strings.Count(body, `class="banner-title">`+headline) != 0 {
		t.Errorf("error page repeats %q in a Banner", headline)
	}
}
