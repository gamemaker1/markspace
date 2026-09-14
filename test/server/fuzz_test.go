package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/modules/plugins"
)

// fuzzRoute is one documented JSON API route. {id} and {id2} are replaced by
// a real or random identifier.
type fuzzRoute struct{ method, path string }

// fuzzRoutes is the JSON API list from docs/routes.md.
var fuzzRoutes = []fuzzRoute{
	{"POST", "/api/auth/signup"}, {"POST", "/api/auth/login"}, {"POST", "/api/auth/logout"},
	{"GET", "/api/auth/me"}, {"POST", "/api/auth/password"}, {"PATCH", "/api/auth/profile"},
	{"GET", "/api/auth/sessions"}, {"DELETE", "/api/auth/sessions/{id}"},
	{"GET", "/api/tokens"}, {"POST", "/api/tokens"}, {"DELETE", "/api/tokens/{id}"},

	{"GET", "/api/users"}, {"POST", "/api/users"}, {"GET", "/api/users/lookup"},
	{"GET", "/api/users/{id}"}, {"PATCH", "/api/users/{id}"}, {"DELETE", "/api/users/{id}"},
	{"GET", "/api/invites"}, {"POST", "/api/invites"}, {"DELETE", "/api/invites/{id}"},
	{"GET", "/api/settings"}, {"PATCH", "/api/settings"}, {"GET", "/api/stats"}, {"POST", "/api/reindex"},

	{"GET", "/api/spaces"}, {"POST", "/api/spaces"}, {"GET", "/api/spaces/{id}"},
	{"PATCH", "/api/spaces/{id}"}, {"DELETE", "/api/spaces/{id}"},
	{"GET", "/api/spaces/{id}/members"}, {"POST", "/api/spaces/{id}/members"},
	{"PATCH", "/api/spaces/{id}/members/{id2}"}, {"DELETE", "/api/spaces/{id}/members/{id2}"},
	{"GET", "/api/spaces/{id}/notes"}, {"GET", "/api/spaces/{id}/tree"}, {"GET", "/api/spaces/{id}/tags"},
	{"GET", "/api/spaces/{id}/trash"}, {"POST", "/api/spaces/{id}/trash/empty"},
	{"GET", "/api/spaces/{id}/export"}, {"POST", "/api/spaces/{id}/import"},

	{"POST", "/api/notes"}, {"GET", "/api/notes/{id}"}, {"PATCH", "/api/notes/{id}"},
	{"DELETE", "/api/notes/{id}"}, {"POST", "/api/notes/{id}/move"}, {"POST", "/api/notes/{id}/duplicate"},
	{"POST", "/api/notes/{id}/trash"}, {"POST", "/api/notes/{id}/restore"},
	{"PUT", "/api/notes/{id}/star"}, {"DELETE", "/api/notes/{id}/star"},
	{"GET", "/api/notes/{id}/backlinks"}, {"GET", "/api/notes/{id}/versions"},
	{"GET", "/api/notes/{id}/versions/{id2}"}, {"GET", "/api/notes/{id}/versions/{id2}/diff"},
	{"POST", "/api/notes/{id}/versions/{id2}/restore"},
	{"GET", "/api/starred"}, {"GET", "/api/recent"},

	{"GET", "/api/spaces/{id}/files"}, {"POST", "/api/folders"}, {"GET", "/api/folders/{id}"},
	{"PATCH", "/api/folders/{id}"}, {"POST", "/api/files"}, {"GET", "/api/files/{id}"},
	{"PATCH", "/api/files/{id}"}, {"PUT", "/api/files/{id}/content"}, {"GET", "/api/files/{id}/versions"},
	{"POST", "/api/files/{id}/versions/{id2}/restore"}, {"GET", "/api/files/{id}/backlinks"},
	{"GET", "/api/items/{id}"}, {"POST", "/api/items/{id}/move"}, {"POST", "/api/items/{id}/copy"},
	{"POST", "/api/items/{id}/trash"}, {"POST", "/api/items/{id}/restore"}, {"DELETE", "/api/items/{id}"},
	{"GET", "/api/items/{id}/backlinks"}, {"GET", "/api/items/{id}/comments"}, {"POST", "/api/items/{id}/comments"},
	{"GET", "/api/items/{id}/shares"}, {"POST", "/api/items/{id}/shares"},
	{"GET", "/api/items/{id}/links"}, {"POST", "/api/items/{id}/links"}, {"POST", "/api/resolve"},

	{"GET", "/api/notes/{id}/comments"}, {"POST", "/api/notes/{id}/comments"},
	{"PATCH", "/api/comments/{id}"}, {"DELETE", "/api/comments/{id}"}, {"POST", "/api/comments/{id}/resolve"},
	{"GET", "/api/notes/{id}/shares"}, {"POST", "/api/notes/{id}/shares"},
	{"PATCH", "/api/shares/{id}"}, {"DELETE", "/api/shares/{id}"}, {"GET", "/api/shares/mine"},
	{"GET", "/api/notes/{id}/links"}, {"POST", "/api/notes/{id}/links"},
	{"PATCH", "/api/links/{id}"}, {"DELETE", "/api/links/{id}"},

	{"GET", "/api/search"}, {"GET", "/api/suggest"}, {"POST", "/api/render"}, {"GET", "/api/events"},
	{"GET", "/api/ai/providers"}, {"POST", "/api/ai/providers"}, {"PATCH", "/api/ai/providers/{id}"},
	{"DELETE", "/api/ai/providers/{id}"}, {"POST", "/api/ai/providers/{id}/test"},
	{"GET", "/api/ai/providers/{id}/models"}, {"POST", "/api/ai/run"}, {"POST", "/api/ai/stream"},
	{"POST", "/api/ai/reindex"},
	{"GET", "/api/ai/usage"}, {"GET", "/api/ai/usage/summary"}, {"GET", "/api/ai/usage/calls"},
	{"GET", "/api/admin/ai/usage"}, {"GET", "/api/admin/ai/usage/calls"}, {"GET", "/api/admin/ai/budgets"},
	{"PUT", "/api/admin/ai/budgets/{id}/{id2}"}, {"DELETE", "/api/admin/ai/budgets/{id}/{id2}"},
	{"POST", "/api/admin/ai/budgets/{id}/{id2}/lift"},
	{"GET", "/api/ai/models"}, {"PATCH", "/api/ai/models/{id}/{id2}"}, {"POST", "/api/ai/providers/{id}/refresh"},
	{"GET", "/api/ai/routes"}, {"PUT", "/api/ai/routes/instance/{id}"}, {"PUT", "/api/ai/routes/me/{id}"},
	{"DELETE", "/api/ai/routes/me/{id}"}, {"GET", "/api/ai/presets"}, {"POST", "/api/admin/ai/prices/refresh"},

	// Plugin host API.
	{"GET", "/api/plugin/v1/me"}, {"GET", "/api/plugin/v1/settings"}, {"GET", "/api/plugin/v1/secrets/{id}"},
	{"GET", "/api/plugin/v1/spaces/{id}/tree"}, {"GET", "/api/plugin/v1/items/{id}"},
	{"GET", "/api/plugin/v1/files/{id}/content"}, {"GET", "/api/plugin/v1/files/{id}/text"},
	{"POST", "/api/plugin/v1/notes"}, {"PATCH", "/api/plugin/v1/notes/{id}"}, {"POST", "/api/plugin/v1/folders"},
	{"POST", "/api/plugin/v1/files"}, {"POST", "/api/plugin/v1/items/{id}/move"},
	{"POST", "/api/plugin/v1/items/{id}/copy"}, {"POST", "/api/plugin/v1/items/{id}/trash"},
	{"GET", "/api/plugin/v1/notes/{id}/links"}, {"GET", "/api/plugin/v1/items/{id}/backlinks"},
	{"POST", "/api/plugin/v1/resolve"}, {"GET", "/api/plugin/v1/search"}, {"POST", "/api/plugin/v1/comments"},
	{"POST", "/api/plugin/v1/suggestions"}, {"GET", "/api/plugin/v1/suggestions"},
	{"DELETE", "/api/plugin/v1/suggestions/{id}"}, {"POST", "/api/plugin/v1/ai/complete"},
	{"POST", "/api/plugin/v1/ai/embed"}, {"POST", "/api/plugin/v1/ai/image"}, {"GET", "/api/plugin/v1/ai/models"},
	{"POST", "/api/plugin/v1/web/fetch"}, {"POST", "/api/plugin/v1/jobs"}, {"PATCH", "/api/plugin/v1/jobs/{id}"},
	{"POST", "/api/plugin/v1/jobs/{id}/log"}, {"GET", "/api/plugin/v1/kv"}, {"GET", "/api/plugin/v1/kv/{id}"},
	{"PUT", "/api/plugin/v1/kv/{id}"}, {"DELETE", "/api/plugin/v1/kv/{id}"}, {"PUT", "/api/plugin/v1/blobs/{id}"},
	{"GET", "/api/plugin/v1/blobs/{id}"}, {"DELETE", "/api/plugin/v1/blobs/{id}"},
	{"POST", "/api/plugin/v1/notify"}, {"POST", "/api/plugin/v1/events"},

	// Contributions, suggestions, jobs and plugin administration.
	{"GET", "/api/ui/contributions"}, {"POST", "/api/commands/{id}"}, {"POST", "/api/panels/{id}"},
	{"POST", "/api/modules/{id}/actions/{id2}"}, {"POST", "/api/import/{id}"},
	{"GET", "/api/notes/{id}/suggestions"}, {"POST", "/api/notes/{id}/suggestions"}, {"GET", "/api/suggestions"},
	{"GET", "/api/suggestions/{id}"}, {"POST", "/api/suggestions/{id}/accept"},
	{"POST", "/api/suggestions/{id}/discard"}, {"DELETE", "/api/suggestions/{id}"},
	{"GET", "/api/jobs"}, {"GET", "/api/jobs/{id}"}, {"POST", "/api/jobs/{id}/cancel"},
	{"POST", "/api/jobs/{id}/pause"}, {"POST", "/api/jobs/{id}/resume"},
	{"GET", "/api/admin/plugins"}, {"GET", "/api/admin/plugins/{id}"}, {"POST", "/api/admin/plugins/preview"},
	{"POST", "/api/admin/plugins"}, {"PATCH", "/api/admin/plugins/{id}"}, {"POST", "/api/admin/plugins/{id}/pause"},
	{"POST", "/api/admin/plugins/{id}/resume"}, {"POST", "/api/admin/plugins/{id}/check"},
	{"POST", "/api/admin/plugins/{id}/approve"}, {"POST", "/api/admin/plugins/{id}/uninstall"},

	// Web routes. They answer with HTML or content, so only the status is checked.
	{"GET", "/jobs"}, {"POST", "/jobs"}, {"GET", "/admin/plugins"}, {"POST", "/admin/plugins"},
	{"GET", "/files/{id2}/{id}/{id}"}, {"GET", "/thumbs/{id2}/{id}"}, {"GET", "/i/{id}"},
	{"GET", "/s/{id2}/f/{id}"}, {"GET", "/s/{id2}/d/{id}"}, {"GET", "/p/{id}/raw/{id2}/x"},
	{"GET", "/p/{id}"}, {"GET", "/p/{id}/i/{id2}"}, {"GET", "/p/{id}/thumb/{id2}"},
}

var fuzzMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}

// Actors.
const (
	actorAdmin = iota
	actorUser
	actorAnon
	actorPlugin
	actorCount
)

// fuzzPluginID is the id of the plugin installed in the fuzz stack.
const fuzzPluginID = "fuzz-plugin"

// Positions in fuzzStack.ids.
const (
	idAdmin = iota
	idUser
	idAdminSpace
	idTeamSpace
	idUserSpace
	idNote
	idChild
	idPersonalNote
	idUserNote
	idComment
	idShare
	idLink
	idInvite
	idVersion
	idFolder
	idFile
	idTeamSlug
	idLinkToken
	idFileLink
	idEmpty
	idCount
)

const (
	// The admin is the one newPluginStack signs up.
	fuzzAdminEmail    = "admin@example.com"
	fuzzAdminPassword = "correct horse battery"
	fuzzUserPassword  = "another good password"
)

// fuzzStack is the real server plus the identities and ids one fuzz worker
// uses for every input.
type fuzzStack struct {
	srv       *httptest.Server
	db        *sql.DB
	client    *http.Client
	ids       [idCount]string
	adminTok  string
	userTok   string
	userEmail string
	userN     int
	// original rows restored before every input.
	adminHash, userHash string
	// ps is the stack with the plugin host, tp the installed plugin and
	// pluginTok its plugin token.
	ps        *pluginStack
	tp        *testPlugin
	pluginTok string
}

// buildFuzzStack assembles the same stack as newHarness over a temporary
// directory and seeds it with an admin, a normal user, spaces, notes and the
// objects that hang off a note.
func buildFuzzStack(tb testing.TB) *fuzzStack {
	tb.Helper()
	// The stack is the one the plugin tests use: the module engine, the
	// built-in modules and the plugin host, with the admin signed up.
	ps := newPluginStack(tb)
	s := &fuzzStack{
		srv: ps.srv,
		db:  ps.db.SQL(),
		ps:  ps,
		client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
	}
	s.ids[idAdmin] = ps.adminID
	s.adminTok = ps.adminTok

	s.createUser(tb)

	var spaces struct{ Items []*core.Space }
	s.mustCall(tb, nil, s.adminTok, "GET", "/api/spaces", nil, &spaces, 200)
	if len(spaces.Items) == 0 {
		tb.Fatal("admin has no personal space")
	}
	s.ids[idAdminSpace] = spaces.Items[0].ID
	var team, mine core.Space
	s.mustCall(tb, nil, s.adminTok, "POST", "/api/spaces", core.SpaceInput{Name: ptr("Fuzz team")}, &team, 201)
	s.ids[idTeamSpace] = team.ID
	s.mustCall(tb, nil, s.userTok, "POST", "/api/spaces", core.SpaceInput{Name: ptr("User space")}, &mine, 201)
	s.ids[idUserSpace] = mine.ID

	var note, child, personal, userNote core.Note
	s.mustCall(tb, nil, s.adminTok, "POST", "/api/notes", core.NoteInput{
		SpaceID: team.ID, Title: "Plan", Content: "# Plan\n\n#tag [[Child]]\n",
	}, &note, 201)
	s.ids[idNote] = note.ID
	s.mustCall(tb, nil, s.adminTok, "POST", "/api/notes", core.NoteInput{
		SpaceID: team.ID, ParentID: note.ID, Title: "Child", Content: "child body\n",
	}, &child, 201)
	s.ids[idChild] = child.ID
	s.mustCall(tb, nil, s.adminTok, "POST", "/api/notes", core.NoteInput{
		SpaceID: spaces.Items[0].ID, Title: "Private", Content: "secret\n",
	}, &personal, 201)
	s.ids[idPersonalNote] = personal.ID
	s.mustCall(tb, nil, s.userTok, "POST", "/api/notes", core.NoteInput{
		SpaceID: mine.ID, Title: "Mine", Content: "user body\n",
	}, &userNote, 201)
	s.ids[idUserNote] = userNote.ID

	s.mustCall(tb, nil, s.adminTok, "PATCH", "/api/notes/"+note.ID, core.NoteUpdate{
		Content: ptr("# Plan\n\nsecond\n"), Snapshot: true,
	}, nil, 200)
	var versions struct{ Items []*core.NoteVersion }
	s.mustCall(tb, nil, s.adminTok, "GET", "/api/notes/"+note.ID+"/versions", nil, &versions, 200)
	if len(versions.Items) > 0 {
		s.ids[idVersion] = versions.Items[0].ID
	}
	var comment core.Comment
	s.mustCall(tb, nil, s.adminTok, "POST", "/api/notes/"+note.ID+"/comments", core.CommentInput{
		NoteID: note.ID, Body: "a comment",
	}, &comment, 201)
	s.ids[idComment] = comment.ID
	var share core.Share
	s.mustCall(tb, nil, s.adminTok, "POST", "/api/notes/"+note.ID+"/shares", core.ShareInput{
		NoteID: note.ID, UserID: s.ids[idUser], Access: core.AccessView,
	}, &share, 201)
	s.ids[idShare] = share.ID
	var link core.PublicLink
	s.mustCall(tb, nil, s.adminTok, "POST", "/api/notes/"+note.ID+"/links", core.PublicLinkInput{
		NoteID: note.ID, Access: core.AccessView,
	}, &link, 201)
	s.ids[idLink] = link.ID
	var folder core.Folder
	s.mustCall(tb, nil, s.adminTok, "POST", "/api/folders", core.FolderInput{SpaceID: team.ID, Name: "Assets"}, &folder, 201)
	s.ids[idFolder] = folder.ID
	s.ids[idTeamSlug] = team.Slug
	s.ids[idLinkToken] = link.Token
	var file core.File
	s.upload(tb, s.adminTok, [][2]string{{"spaceId", team.ID}, {"parentId", folder.ID}}, "data.txt", []byte("0123456789"), &file)
	s.ids[idFile] = file.ID
	var fileLink core.PublicLink
	s.mustCall(tb, nil, s.adminTok, "POST", "/api/items/"+file.ID+"/links", core.PublicLinkInput{
		NoteID: file.ID, Access: core.AccessView,
	}, &fileLink, 201)
	s.ids[idFileLink] = fileLink.Token
	s.mustCall(tb, nil, s.adminTok, "PATCH", "/api/notes/"+note.ID, core.NoteUpdate{
		Content: ptr("# Plan\n\n![](Assets/data.txt) [[data.txt]]\n"),
	}, nil, 200)
	var invite core.Invite
	s.mustCall(tb, nil, s.adminTok, "POST", "/api/invites", core.InviteInput{Email: "invitee@example.com"}, &invite, 201)
	s.ids[idInvite] = invite.ID

	if err := s.db.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, s.ids[idAdmin]).Scan(&s.adminHash); err != nil {
		tb.Fatalf("read admin hash: %v", err)
	}

	// A plugin with every permission and an editor grant on the team space.
	// Its slow job is released at once, so fuzzed jobs never hold a worker.
	s.tp = newTestPlugin(tb, testManifest(fuzzPluginID, allPermissions...))
	close(s.tp.release)
	ps.install(s.tp.srv.URL, []map[string]string{{"spaceId": team.ID, "role": "editor"}})
	s.pluginTok = s.tp.pluginToken()
	return s
}

// healPlugin reinstalls, resumes or regrants the fuzz plugin when an input
// uninstalled, paused or changed it.
func (s *fuzzStack) healPlugin(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	host := s.ps.host
	grants := []plugins.Grant{{SpaceID: s.ids[idTeamSpace], Role: core.RoleEditor}}
	v, err := host.Get(ctx, fuzzPluginID)
	if err != nil {
		res, err := host.Install(ctx, plugins.InstallInput{
			BaseURL: s.tp.srv.URL, Grants: grants,
			Secrets: map[string]string{"api_key": "k"}, Settings: map[string]any{"greeting": "hi"},
		})
		if err != nil {
			t.Fatalf("heal: reinstall the plugin: %v", err)
		}
		if res.Token != "" {
			s.pluginTok = res.Token
		} else {
			s.pluginTok = s.tp.sdk.Credentials().Token
		}
		return
	}
	if v.Status == core.PluginStatusPaused {
		if _, err := host.SetPaused(ctx, fuzzPluginID, false); err != nil {
			t.Fatalf("heal: resume the plugin: %v", err)
		}
	}
	if len(v.Grants) != 1 || v.Grants[0].SpaceID != s.ids[idTeamSpace] || v.Grants[0].Role != core.RoleEditor {
		// A deleted team space leaves nothing to grant, which is fine.
		_, _ = host.Configure(ctx, fuzzPluginID, plugins.ConfigInput{Grants: &grants})
	}
}

// checkPluginSuccess fails when the plugin reached an item or space outside
// its grant, or an admin route.
func checkPluginSuccess(t *testing.T, s *fuzzStack, desc, p string, data []byte) {
	t.Helper()
	if strings.HasPrefix(p, "/api/admin/") {
		t.Fatalf("%s: a plugin token succeeded on an admin route: %s", desc, data)
	}
	if !strings.HasPrefix(p, "/api/plugin/v1/") {
		return
	}
	// Key-value, blob, secret, job and suggestion ids are the plugin's own
	// names, not items.
	for _, own := range []string{"/kv/", "/blobs/", "/secrets/", "/jobs/", "/suggestions/"} {
		if strings.HasPrefix(p, "/api/plugin/v1"+own) {
			return
		}
	}
	for _, i := range []int{idAdminSpace, idUserSpace, idPersonalNote, idUserNote} {
		if id := s.ids[i]; id != "" && strings.Contains(p, "/"+id) {
			t.Fatalf("%s: the plugin reached an ungranted space or item: %s", desc, data)
		}
	}
}

// upload sends a multipart upload during setup and fails unless it is created.
func (s *fuzzStack) upload(tb testing.TB, token string, fields [][2]string, name string, body []byte, out any) {
	tb.Helper()
	buf, ct := multipartBody(fields, name, body)
	req, _ := http.NewRequest("POST", s.srv.URL+"/api/files", buf)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := s.client.Do(req)
	if err != nil {
		tb.Fatalf("upload: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 201 {
		tb.Fatalf("setup upload: status %d: %s", resp.StatusCode, data)
	}
	if err := json.Unmarshal(data, out); err != nil {
		tb.Fatalf("setup upload: decode %s: %v", data, err)
	}
}

// fuzzBoundary is the multipart boundary a fuzz body starting with "--" uses.
const fuzzBoundary = "fuzzb"

// createUser adds a normal user through the admin API, logs it in and mints
// its API token.
func (s *fuzzStack) createUser(tb testing.TB) {
	tb.Helper()
	s.userN++
	s.userEmail = fmt.Sprintf("fuzz-user-%d@example.com", s.userN)
	var user core.User
	s.mustCall(tb, nil, s.adminTok, "POST", "/api/users", core.UserInput{
		Email: ptr(s.userEmail), Name: ptr("Fuzz User"), Password: ptr(fuzzUserPassword),
	}, &user, 201)
	s.ids[idUser] = user.ID
	s.userTok = s.login(tb, s.userEmail, fuzzUserPassword)
	if err := s.db.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, user.ID).Scan(&s.userHash); err != nil {
		tb.Fatalf("read user hash: %v", err)
	}
}

// login opens a session and exchanges it for an API token.
func (s *fuzzStack) login(tb testing.TB, email, password string) string {
	tb.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	s.mustCall(tb, c, "", "POST", "/api/auth/login", map[string]string{"email": email, "password": password}, nil, 200)
	return s.tokenFor(tb, c)
}

func (s *fuzzStack) tokenFor(tb testing.TB, c *http.Client) string {
	tb.Helper()
	var tok struct {
		Token string `json:"token"`
	}
	s.mustCall(tb, c, "", "POST", "/api/tokens", map[string]string{"name": "fuzz"}, &tok, 201)
	if tok.Token == "" {
		tb.Fatal("token response has no token")
	}
	return tok.Token
}

// mustCall sends a setup request and fails unless the status is want.
func (s *fuzzStack) mustCall(tb testing.TB, c *http.Client, token, method, p string, body, out any, want int) {
	tb.Helper()
	var r io.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		r = bytes.NewReader(buf)
	}
	req, _ := http.NewRequest(method, s.srv.URL+p, r)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if c == nil {
		c = s.client
	}
	resp, err := c.Do(req)
	if err != nil {
		tb.Fatalf("%s %s: %v", method, p, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		tb.Fatalf("setup %s %s: status %d, want %d: %s", method, p, resp.StatusCode, want, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			tb.Fatalf("setup %s %s: decode %s: %v", method, p, data, err)
		}
	}
}

// whoami returns the user behind a token, or nil.
func (s *fuzzStack) whoami(token string) *core.User {
	req, _ := http.NewRequest("GET", s.srv.URL+"/api/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var me struct {
		User *core.User `json:"user"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&me) != nil {
		return nil
	}
	return me.User
}

// heal puts the two identities back into their starting state, so a change
// made by one input, such as promoting the user, cannot make a later input's
// authorization check wrong.
func (s *fuzzStack) heal(t *testing.T) {
	t.Helper()
	exec := func(q string, args ...any) {
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatalf("heal %q: %v", q, err)
		}
	}
	exec(`UPDATE users SET is_admin = CASE WHEN id = ? THEN 1 ELSE 0 END`, s.ids[idAdmin])
	exec(`UPDATE users SET is_active = 1, password_hash = ? WHERE id = ?`, s.adminHash, s.ids[idAdmin])
	s.db.Exec(`UPDATE users SET email = ? WHERE id = ?`, fuzzAdminEmail, s.ids[idAdmin])
	exec(`UPDATE users SET is_active = 1, password_hash = ? WHERE id = ?`, s.userHash, s.ids[idUser])
	s.db.Exec(`UPDATE users SET email = ? WHERE id = ?`, s.userEmail, s.ids[idUser])

	if me := s.whoami(s.adminTok); me == nil || me.ID != s.ids[idAdmin] || !me.IsAdmin {
		s.adminTok = s.login(t, fuzzAdminEmail, fuzzAdminPassword)
		if me := s.whoami(s.adminTok); me == nil || !me.IsAdmin {
			t.Fatalf("harness admin cannot be restored: %+v", me)
		}
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM users WHERE id = ?`, s.ids[idUser]).Scan(&n)
	if n == 0 {
		s.createUser(t)
		return
	}
	if me := s.whoami(s.userTok); me == nil || me.ID != s.ids[idUser] {
		s.userTok = s.login(t, s.userEmail, fuzzUserPassword)
	}
}

func (s *fuzzStack) isMember(spaceID, userID string) bool {
	var n int
	s.db.QueryRow(`SELECT count(*) FROM space_members WHERE space_id = ? AND user_id = ?`, spaceID, userID).Scan(&n)
	return n > 0
}

var spaceRoutePattern = regexp.MustCompile(`^/api/spaces/([^/]+)(/members(/[^/]+)?)?$`)

func fuzzRouteIndex(method, p string) uint8 {
	for i, r := range fuzzRoutes {
		if r.method == method && r.path == p {
			return uint8(i)
		}
	}
	panic("unknown fuzz route " + method + " " + p)
}

// FuzzHTTPAPI sends one request per input over the whole JSON API, as the
// admin, a normal user or an anonymous client. No response may be a 5xx,
// every error must carry a JSON error body, the normal user must never
// succeed on an admin route or on PATCH, DELETE or member routes of a space
// it does not belong to, and the instance must always keep an active admin.
func FuzzHTTPAPI(f *testing.F) {
	type seed struct {
		route   uint8
		method  uint8
		id, id2 uint8
		random  string
		body    string
		actor   uint8
	}
	r := fuzzRouteIndex
	for _, sd := range []seed{
		{r("GET", "/api/auth/me"), 0, 0, 0, "", "", actorAnon},
		{r("PATCH", "/api/users/{id}"), 0, idAdmin, 0, "", `{"isAdmin":false}`, actorAdmin},
		{r("PATCH", "/api/users/{id}"), 0, idAdmin, 0, "", `{"isActive":false}`, actorAdmin},
		{r("DELETE", "/api/users/{id}"), 0, idAdmin, 0, "", "", actorAdmin},
		{r("PATCH", "/api/users/{id}"), 0, idUser, 0, "", `{"isAdmin":true}`, actorUser},
		{r("GET", "/api/users"), 0, 0, 0, "", "", actorUser},
		{r("GET", "/api/users/{id}"), 0, idAdmin, 0, "", "", actorUser},
		{r("GET", "/api/users/lookup"), 0, 0, 0, "fuzz", "", actorUser},
		{r("POST", "/api/invites"), 0, 0, 0, "", `{"email":"x@example.com","isAdmin":true}`, actorUser},
		{r("PATCH", "/api/settings"), 0, 0, 0, "", `{"allowSignup":true}`, actorUser},
		{r("POST", "/api/reindex"), 0, 0, 0, "", "", actorUser},
		{r("PATCH", "/api/spaces/{id}"), 0, idTeamSpace, 0, "", `{"name":"taken"}`, actorUser},
		{r("DELETE", "/api/spaces/{id}"), 0, idAdminSpace, 0, "", "", actorUser},
		{r("POST", "/api/spaces/{id}/members"), 0, idTeamSpace, 0, "", `{"userId":"$USER","role":"owner"}`, actorUser},
		{r("PATCH", "/api/spaces/{id}/members/{id2}"), 0, idTeamSpace, idAdmin, "", `{"role":"viewer"}`, actorUser},
		{r("DELETE", "/api/spaces/{id}/members/{id2}"), 0, idUserSpace, idUser, "", "", actorUser},
		{r("DELETE", "/api/spaces/{id}/members/{id2}"), 0, idTeamSpace, idAdmin, "", "", actorAdmin},
		{r("POST", "/api/notes"), 0, 0, 0, "", `{"spaceId":"$SPACE","title":"\x00","parentId":"$NOTE"}`, actorAdmin},
		{r("POST", "/api/notes/{id}/move"), 0, idNote, 0, "", `{"parentId":"$CHILD"}`, actorAdmin},
		{r("POST", "/api/notes/{id}/move"), 0, idNote, 0, "", `{"parentId":"$NOTE","position":-1}`, actorAdmin},
		{r("PATCH", "/api/notes/{id}"), 0, idNote, 0, "", `{"baseUpdatedAt":"9999-12-31T23:59:59-23:59","content":"x"}`, actorAdmin},
		{r("GET", "/api/notes/{id}/versions/{id2}/diff"), 0, idNote, idVersion, "", "", actorAdmin},
		{r("GET", "/api/search"), 0, 0, 0, "\x00", "", actorUser},
		{r("POST", "/api/render"), 0, 0, 0, "", `{"markdown":"<script>alert(1)</script>"}`, actorAnon},
		{r("POST", "/api/notes/{id}/comments"), 0, idNote, 0, "", `{"body":"hi","parentId":"$NOTE"}`, actorUser},
		{r("GET", "/api/notes/{id}"), 0, idCount, 0, "../../etc/passwd", "", actorUser},
		{r("POST", "/api/spaces"), 3, 0, 0, "", "{", actorAnon},
		{r("GET", "/api/events"), 0, 0, 0, "", "", actorUser},
		{r("POST", "/api/ai/run"), 0, 0, 0, "", `{"task":"ask"}`, actorUser},
		{r("GET", "/api/ai/usage"), 0, 0, 0, "day,user,plugin", "", actorUser},
		{r("GET", "/api/ai/usage"), 0, 0, 0, "9999-12-31T23:59:59-23:59", "", actorAdmin},
		{r("GET", "/api/ai/usage/calls"), 0, 0, 0, "slowest", "", actorUser},
		{r("GET", "/api/ai/usage/summary"), 0, 0, 0, "", "", actorAnon},
		{r("GET", "/api/admin/ai/usage"), 0, 0, 0, "month,feature", "", actorUser},
		{r("GET", "/api/admin/ai/usage/calls"), 0, 0, 0, "failed", "", actorAdmin},
		{r("PUT", "/api/admin/ai/budgets/{id}/{id2}"), 0, idCount, idUser, "user", `{"monthlyTokens":-1}`, actorAdmin},
		{r("PUT", "/api/admin/ai/budgets/{id}/{id2}"), 0, idCount, idUser, "user", `{"monthlyCost":1e308}`, actorAdmin},
		{r("PUT", "/api/admin/ai/budgets/{id}/{id2}"), 0, idCount, idUser, "user", `{"monthlyTokens":1}`, actorUser},
		{r("POST", "/api/admin/ai/budgets/{id}/{id2}/lift"), 0, idCount, idAdmin, "user", "", actorAdmin},
		{r("DELETE", "/api/admin/ai/budgets/{id}/{id2}"), 0, idCount, idCount, "plugin", "", actorAdmin},
		{r("PATCH", "/api/settings"), 0, 0, 0, "", `{"aiMonthlyTokenBudget":-5,"aiEnabled":true}`, actorAdmin},
		{r("GET", "/api/ai/models"), 0, 0, 0, "vision", "", actorUser},
		{r("GET", "/api/ai/models"), 0, 0, 0, "image.edit", "", actorAdmin},
		{r("PATCH", "/api/ai/models/{id}/{id2}"), 0, idCount, idCount, "openai/gpt-4o", `{"priceOverride":{"inputPerMTok":-1}}`, actorAdmin},
		{r("PATCH", "/api/ai/models/{id}/{id2}"), 0, idCount, idCount, "m", `{"priceOverride":null,"enabled":false}`, actorUser},
		{r("POST", "/api/ai/providers/{id}/refresh"), 0, idCount, 0, "local", "", actorUser},
		{r("PUT", "/api/ai/routes/instance/{id}"), 0, idCount, 0, "edit", `{"targets":[{"providerId":"x","model":"y"}]}`, actorUser},
		{r("PUT", "/api/ai/routes/me/{id}"), 0, idCount, 0, "plugin.a.b", `{"targets":[{"providerId":"x","model":""}]}`, actorUser},
		{r("DELETE", "/api/ai/routes/me/{id}"), 0, idCount, 0, "../edit", "", actorUser},
		{r("POST", "/api/admin/ai/prices/refresh"), 0, 0, 0, "", "", actorUser},
		{r("POST", "/api/admin/ai/prices/refresh"), 0, 0, 0, "", "", actorAdmin},
		{r("POST", "/api/files"), 0, 0, 0, "", "--fuzzb\r\nContent-Disposition: form-data; name=\"spaceId\"\r\n\r\n$SPACE\r\n--fuzzb\r\nContent-Disposition: form-data; name=\"onConflict\"\r\n\r\nrename\r\n--fuzzb\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.txt\"\r\n\r\nhello\r\n--fuzzb--\r\n", actorAdmin},
		{r("POST", "/api/files"), 0, 0, 0, "", "--fuzzb\r\nContent-Disposition: form-data; name=\"file\"; filename=\"../..\"\r\n\r\nx", actorUser},
		{r("PUT", "/api/files/{id}/content"), 0, idFile, 0, "", "--fuzzb\r\nContent-Disposition: form-data; name=\"baseSha256\"\r\n\r\nzz\r\n--fuzzb\r\nContent-Disposition: form-data; name=\"file\"; filename=\"d.md\"\r\n\r\nx\r\n--fuzzb--\r\n", actorAdmin},
		{r("POST", "/api/folders"), 0, 0, 0, "", `{"spaceId":"$SPACE","parentId":"$FOLDER","name":"."}`, actorAdmin},
		{r("POST", "/api/items/{id}/move"), 0, idFolder, 0, "", `{"parentId":"$FOLDER"}`, actorAdmin},
		{r("POST", "/api/items/{id}/copy"), 0, idFolder, 0, "", `{"parentId":"$FOLDER"}`, actorAdmin},
		{r("POST", "/api/items/{id}/copy"), 0, idFile, 0, "", `{"spaceId":"$SPACE","name":"x.md"}`, actorUser},
		{r("POST", "/api/items/{id}/trash"), 0, idFolder, 0, "", "", actorAdmin},
		{r("POST", "/api/items/{id}/restore"), 0, idFile, 0, "", "", actorAdmin},
		{r("DELETE", "/api/items/{id}"), 0, idFile, 0, "", "", actorUser},
		{r("PATCH", "/api/files/{id}"), 0, idFile, 0, "", `{"name":"CON\u0000"}`, actorAdmin},
		{r("PATCH", "/api/folders/{id}"), 0, idFolder, 0, "", `{"name":"plan.md"}`, actorAdmin},
		{r("POST", "/api/items/{id}/comments"), 0, idFolder, 0, "", `{"body":"hi","anchor":{"exact":"x"}}`, actorAdmin},
		{r("POST", "/api/resolve"), 0, 0, 0, "", `{"spaceId":"$SPACE","fromNoteId":"$NOTE","targets":["[[data.txt]]","../x","![](a)"]}`, actorUser},
		{r("GET", "/api/spaces/{id}/files"), 0, idTeamSpace, 0, "image", "", actorAdmin},
		{r("GET", "/api/spaces/{id}/trash"), 0, idTeamSpace, 0, "1", "", actorAdmin},
		{r("GET", "/files/{id2}/{id}/{id}"), 0, idFile, idTeamSlug, "", "", actorAdmin},
		{r("GET", "/thumbs/{id2}/{id}"), 0, idFile, idTeamSlug, "256", "", actorAdmin},
		{r("GET", "/i/{id}"), 0, idFolder, 0, "", "", actorUser},
		{r("GET", "/p/{id}/raw/{id2}/x"), 0, idFileLink, idFile, "1", "", actorAnon},
		{r("GET", "/p/{id}/i/{id2}"), 0, idLinkToken, idChild, "", "", actorAnon},
		{r("GET", "/api/plugin/v1/me"), 0, 0, 0, "", "", actorPlugin},
		{r("GET", "/api/plugin/v1/spaces/{id}/tree"), 0, idTeamSpace, 0, "", "", actorPlugin},
		{r("GET", "/api/plugin/v1/spaces/{id}/tree"), 0, idAdminSpace, 0, "", "", actorPlugin},
		{r("GET", "/api/plugin/v1/items/{id}"), 0, idPersonalNote, 0, "", "", actorPlugin},
		{r("POST", "/api/plugin/v1/notes"), 0, 0, 0, "abc", `{"spaceId":"$SPACE","title":"P","frontmatter":{"k":[1,{"a":null}],"tags":["x"]}}`, actorPlugin},
		{r("PATCH", "/api/plugin/v1/notes/{id}"), 0, idNote, 0, "", `{"content":"x","baseUpdatedAt":"2000-01-01T00:00:00Z","frontmatter":{"id":"x"}}`, actorPlugin},
		{r("PUT", "/api/plugin/v1/kv/{id}"), 0, idCount, 0, "a/b", `{"value":{"n":1}}`, actorPlugin},
		{r("GET", "/api/plugin/v1/kv"), 0, 0, 0, "", "", actorPlugin},
		{r("PUT", "/api/plugin/v1/blobs/{id}"), 0, idCount, 0, "blob", "bytes", actorPlugin},
		{r("POST", "/api/plugin/v1/jobs"), 0, 0, 0, "", `{"kind":"tick","spaceId":"$SPACE","payload":{"a":1}}`, actorPlugin},
		{r("PATCH", "/api/plugin/v1/jobs/{id}"), 0, idCount, 0, "x", `{"status":"done","result":[1]}`, actorPlugin},
		{r("POST", "/api/plugin/v1/suggestions"), 0, 0, 0, "", `{"noteId":"$NOTE","title":"T","edits":[{"anchor":{"exact":"Plan","start":2,"end":6},"text":"Goal"}]}`, actorPlugin},
		{r("POST", "/api/plugin/v1/notify"), 0, 0, 0, "", `{"userIds":["$USER"],"message":"hi","link":"//evil"}`, actorPlugin},
		{r("POST", "/api/plugin/v1/events"), 0, 0, 0, "", `{"type":"plugin.fuzz-plugin.x","data":"\u0000"}`, actorPlugin},
		{r("POST", "/api/plugin/v1/web/fetch"), 0, 0, 0, "", `{"url":"http://127.0.0.1:1/x","respectRobots":true}`, actorPlugin},
		{r("POST", "/api/plugin/v1/ai/complete"), 0, 0, 0, "", `{"task":"ask","purpose":"../x","route":"chat"}`, actorPlugin},
		{r("GET", "/api/plugin/v1/search"), 0, 0, 0, "plan", "", actorPlugin},
		{r("GET", "/api/spaces"), 0, 0, 0, "", "", actorPlugin},
		{r("GET", "/api/ui/contributions"), 0, 0, 0, "", "", actorUser},
		{r("POST", "/api/commands/{id}"), 0, idCount, 0, "fuzz-plugin.hello", `{"context":{"itemId":"$NOTE"}}`, actorAdmin},
		{r("POST", "/api/panels/{id}"), 0, idCount, 0, "fuzz-plugin.stats", `{"context":{"itemId":"$NOTE"}}`, actorAdmin},
		{r("POST", "/api/modules/{id}/actions/{id2}"), 0, idCount, idCount, "plugin.fuzz-plugin", `{"context":{"spaceId":"$SPACE"},"payload":{"v":1}}`, actorAdmin},
		{r("POST", "/api/import/{id}"), 0, idCount, 0, "fuzz-plugin.text", `{"fileId":"$FILE","spaceId":"$SPACE"}`, actorUser},
		{r("POST", "/api/notes/{id}/suggestions"), 0, idNote, 0, "", `{"title":"T","edits":[{"anchor":{"exact":"","start":0,"end":0},"text":"x"}]}`, actorUser},
		{r("GET", "/api/suggestions"), 0, 0, 0, "me", "", actorUser},
		{r("GET", "/api/jobs"), 0, 0, 0, "queued,bogus", "", actorUser},
		{r("POST", "/api/jobs/{id}/cancel"), 0, idCount, 0, "x", "", actorAdmin},
		{r("GET", "/api/admin/plugins"), 0, 0, 0, "", "", actorUser},
		{r("POST", "/api/admin/plugins/preview"), 0, 0, 0, "", `{"baseUrl":"http://127.0.0.1:1"}`, actorAdmin},
		{r("PATCH", "/api/admin/plugins/{id}"), 0, idCount, 0, fuzzPluginID, `{"grants":[{"spaceId":"$SPACE","role":"owner"}],"settings":{"nope":1}}`, actorAdmin},
		{r("POST", "/api/admin/plugins/{id}/pause"), 0, idCount, 0, fuzzPluginID, "", actorAdmin},
		{r("POST", "/api/admin/plugins/{id}/uninstall"), 0, idCount, 0, fuzzPluginID, `{"deleteData":true}`, actorAdmin},
		{r("GET", "/admin/plugins"), 0, 0, 0, "", "", actorAdmin},
		{r("GET", "/jobs"), 0, 0, 0, "", "", actorUser},
	} {
		f.Add(sd.route, sd.method, sd.id, sd.id2, sd.random, []byte(sd.body), sd.actor)
	}

	s := buildFuzzStack(f)

	f.Fuzz(func(t *testing.T, routeIdx, methodIdx, idIdx, id2Idx uint8, random string, body []byte, actor uint8) {
		s.heal(t)
		s.healPlugin(t)
		route := fuzzRoutes[int(routeIdx)%len(fuzzRoutes)]
		method := route.method
		if m := int(methodIdx) % (len(fuzzMethods) + 1); m > 0 {
			method = fuzzMethods[m-1]
		}
		pick := func(i uint8) string {
			if n := int(i) % (idCount + 1); n < idCount {
				return s.ids[n]
			}
			return random
		}
		id, id2 := pick(idIdx), pick(id2Idx)
		p := strings.NewReplacer("{id}", url.PathEscape(id), "{id2}", url.PathEscape(id2)).Replace(route.path)
		target := s.srv.URL + p + "?q=" + url.QueryEscape(random) + "&limit=5"
		if strings.HasPrefix(p, "/api/ai/models") {
			// The catalog listing reads these, and the random string fills them.
			for _, key := range []string{"capability", "provider"} {
				target += "&" + key + "=" + url.QueryEscape(random)
			}
		}
		if !strings.HasPrefix(p, "/api/") || strings.Contains(p, "/files") || strings.Contains(p, "/trash") {
			// The web content routes, the file listing and the trash read these.
			for _, key := range []string{"w", "v", "download", "type", "sort", "items"} {
				target += "&" + key + "=" + url.QueryEscape(random)
			}
		}
		if strings.Contains(p, "/plugin/v1/") || strings.Contains(p, "/suggestions") || strings.Contains(p, "/jobs") ||
			strings.HasPrefix(p, "/api/ui/") {
			// The host API, suggestion, job and contribution listings read these.
			for _, key := range []string{"prefix", "cursor", "status", "author", "noteId", "space", "kind", "module", "itemId", "spaceId", "contexts", "mode"} {
				target += "&" + key + "=" + url.QueryEscape(random)
			}
		}
		if strings.Contains(p, "/usage") {
			// The usage routes read these, and the random string fills them.
			for _, key := range []string{"from", "to", "groupBy", "sort", "feature", "user"} {
				target += "&" + key + "=" + url.QueryEscape(random)
			}
		}
		payload := strings.NewReplacer(
			"$ADMIN", s.ids[idAdmin], "$USER", s.ids[idUser], "$SPACE", s.ids[idTeamSpace],
			"$NOTE", s.ids[idNote], "$CHILD", s.ids[idChild], "$FOLDER", s.ids[idFolder], "$FILE", s.ids[idFile],
		).Replace(string(body))
		who := int(actor) % actorCount
		if (p == "/api/plugin/v1/web/fetch" || strings.HasPrefix(p, "/api/admin/plugins")) && !loopbackOnly(payload) {
			// These routes fetch the URL in the body with timeouts of 10 to
			// 20 s. An address outside this machine would stall the worker
			// without testing anything more.
			return
		}

		// Membership is read before the request, so a request that removes
		// the user from a space is judged on the state it saw.
		wasMember := false
		if m := spaceRoutePattern.FindStringSubmatch(p); who == actorUser && m != nil {
			if spaceID, err := url.PathUnescape(m[1]); err == nil {
				wasMember = s.isMember(spaceID, s.ids[idUser])
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, method, target, strings.NewReader(payload))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if strings.HasPrefix(payload, "--") {
			req.Header.Set("Content-Type", "multipart/form-data; boundary="+fuzzBoundary)
		}
		switch who {
		case actorAdmin:
			req.Header.Set("Authorization", "Bearer "+s.adminTok)
		case actorUser:
			req.Header.Set("Authorization", "Bearer "+s.userTok)
		case actorPlugin:
			req.Header.Set("Authorization", "Bearer "+s.pluginTok)
			if method == "POST" && len(random)%3 == 0 && validHeaderValue(random) {
				req.Header.Set("Idempotency-Key", random)
			}
		}
		desc := fmt.Sprintf("actor %d %s %s body %q", who, method, p, payload)
		resp, err := s.client.Do(req)
		if err != nil {
			t.Fatalf("%s: no response: %v", desc, err)
		}
		defer resp.Body.Close()
		status := resp.StatusCode
		isStream := strings.Contains(resp.Header.Get("Content-Type"), "event-stream")
		var data []byte
		if !isStream {
			data, _ = io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		}

		switch {
		case status == http.StatusServiceUnavailable && strings.HasPrefix(p, "/api/ai/"):
			// AI is switched off in this stack, which is not a server fault.
		case status == http.StatusServiceUnavailable && pluginUnavailableRoute(p):
			// An unreachable or misbehaving plugin, a remote site that does not
			// answer, or AI switched off, is not a server fault.
		case status >= 500:
			t.Fatalf("%s: status %d: %s", desc, status, data)
		}
		if !strings.HasPrefix(p, "/api/") {
			// A web route answers with a page, a redirect or file content.
		} else if status >= 300 && status <= 399 && resp.Header.Get("Location") != "" && path.Clean(p) != p {
			// net/http ServeMux path cleaning (301 or 307, as for an empty
			// id in "/api/spaces//members/x"), not an API response.
		} else if status < 200 || status > 299 {
			var e struct {
				Error string `json:"error"`
			}
			if !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") ||
				json.Unmarshal(data, &e) != nil || e.Error == "" {
				t.Fatalf("%s: status %d without a JSON error body: %q (%s)", desc, status, data, resp.Header.Get("Content-Type"))
			}
		}

		if who == actorUser && status >= 200 && status <= 299 {
			checkUserSuccess(t, s, desc, method, p, id, data, wasMember)
		}
		if who == actorPlugin && status >= 200 && status <= 299 {
			checkPluginSuccess(t, s, desc, p, data)
		}

		var admins int
		s.db.QueryRow(`SELECT count(*) FROM users WHERE is_admin = 1 AND is_active = 1`).Scan(&admins)
		if admins == 0 {
			t.Fatalf("%s: status %d left the instance without an active admin", desc, status)
		}
	})
}

// TestSettingsVisibilityByCaller covers the FuzzHTTPAPI finding stored in
// testdata/fuzz/FuzzHTTPAPI/a98c5260419a35b2, where GET /api/settings gave the
// full settings, including defaultSpaceId and the provider ids, to every
// caller. The fixed policy: an admin gets every field, an anonymous caller only
// instanceName and allowSignup, and a signed-in user also the AI switches,
// plus defaultSpaceId only for a space they can view. The budget defaults
// stay admin-only.
func TestSettingsVisibilityByCaller(t *testing.T) {
	s := buildFuzzStack(t)
	get := func(token string) map[string]any {
		t.Helper()
		req, _ := http.NewRequest("GET", s.srv.URL+"/api/settings", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := s.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/settings status %d: %s", resp.StatusCode, body)
		}
		out := map[string]any{}
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode %s: %v", body, err)
		}
		return out
	}
	set := func(defaultSpace string) {
		t.Helper()
		s.mustCall(t, nil, s.adminTok, "PATCH", "/api/settings", core.Settings{
			InstanceName: "Fuzz", AllowSignup: true, AIEnabled: true, AIAllowUserKeys: true,
			AIAllowUserRoutes: true, AIMonthlyTokenBudget: 7, DefaultSpaceID: defaultSpace,
		}, nil, 200)
	}

	// The default space is the team space, which the normal user cannot view.
	set(s.ids[idTeamSpace])
	admin, user, anon := get(s.adminTok), get(s.userTok), get("")

	if admin["aiMonthlyTokenBudget"] != float64(7) || admin["defaultSpaceId"] != s.ids[idTeamSpace] || admin["aiAllowUserRoutes"] != true {
		t.Fatalf("admin view lacks fields: %v", admin)
	}
	if anon["instanceName"] != admin["instanceName"] || anon["allowSignup"] != admin["allowSignup"] {
		t.Fatalf("anonymous view lost the public fields: %v", anon)
	}
	for _, key := range []string{"aiMonthlyTokenBudget", "defaultSpaceId"} {
		if _, ok := anon[key]; ok {
			t.Errorf("anonymous view has %s: %v", key, anon)
		}
	}
	if anon["aiEnabled"] == true || anon["aiAllowUserKeys"] == true || anon["aiAllowUserRoutes"] == true {
		t.Errorf("anonymous view has the AI switches set: %v", anon)
	}

	if user["aiEnabled"] != admin["aiEnabled"] || user["aiAllowUserKeys"] != admin["aiAllowUserKeys"] || user["aiAllowUserRoutes"] != true {
		t.Errorf("user view lacks the AI fields: %v (admin %v)", user, admin)
	}
	if _, ok := user["aiMonthlyTokenBudget"]; ok {
		t.Errorf("user view has aiMonthlyTokenBudget: %v", user)
	}
	if _, ok := user["defaultSpaceId"]; ok {
		t.Errorf("user view has defaultSpaceId of a space it cannot view: %v", user)
	}

	// A default space the user can view is shown to them.
	set(s.ids[idUserSpace])
	if got := get(s.userTok)["defaultSpaceId"]; got != s.ids[idUserSpace] {
		t.Errorf("user view defaultSpaceId = %v, want their own space %s", got, s.ids[idUserSpace])
	}
}

// checkUserSuccess fails when the normal user was allowed something only an
// admin or a space member may do.
func checkUserSuccess(t *testing.T, s *fuzzStack, desc, method, p, id string, data []byte, wasMember bool) {
	t.Helper()
	switch {
	case p == "/api/users/lookup" && method == "GET":
		// The share recipient picker is open to every signed-in user.
	case strings.HasPrefix(p, "/api/users/") && method == "GET" && strings.Count(p, "/") == 3:
		// Any signed-in user may read another user's id, name and avatar
		// colour, and nothing more.
		if id != s.ids[idUser] {
			var u map[string]any
			json.Unmarshal(data, &u)
			if email, _ := u["email"].(string); email != "" || u["isAdmin"] == true {
				t.Fatalf("%s: user record leaks email or admin flag: %s", desc, data)
			}
		}
	case p == "/api/settings" && method == "GET":
		// Any caller may read the public settings, see
		// TestSettingsVisibilityByCaller. The budget defaults stay
		// admin-only.
		var st map[string]any
		json.Unmarshal(data, &st)
		if _, ok := st["aiMonthlyTokenBudget"]; ok {
			t.Fatalf("%s: settings leak aiMonthlyTokenBudget to a normal user: %s", desc, data)
		}
	case strings.HasPrefix(p, "/api/ai/routes/instance/"),
		strings.HasPrefix(p, "/api/ai/models/") && method == "PATCH":
		t.Fatalf("%s: normal user changed an instance route or a catalog row: %s", desc, data)
	case strings.HasPrefix(p, "/api/ai/providers/") && strings.HasSuffix(p, "/refresh"):
		var owner string
		s.db.QueryRow(`SELECT owner_id FROM ai_providers WHERE id = ?`, id).Scan(&owner)
		if owner != s.ids[idUser] {
			t.Fatalf("%s: normal user refreshed a provider it does not own: %s", desc, data)
		}
	case p == "/api/ai/usage/calls" && method == "GET":
		// A user lists only their own calls.
		var calls struct{ Items []core.AIUsageRecord }
		json.Unmarshal(data, &calls)
		for _, c := range calls.Items {
			if c.UserID != s.ids[idUser] {
				t.Fatalf("%s: user sees the call of %s: %s", desc, c.UserID, data)
			}
		}
	case p == "/api/ai/usage" && method == "GET":
		var report core.AIUsageReport
		json.Unmarshal(data, &report)
		for _, row := range report.Rows {
			if row.UserID != "" && row.UserID != s.ids[idUser] {
				t.Fatalf("%s: user report has a row of %s: %s", desc, row.UserID, data)
			}
		}
	case strings.HasPrefix(p, "/api/admin/"):
		t.Fatalf("%s: normal user got a success on an admin route: %s", desc, data)
	case p == "/api/users" || strings.HasPrefix(p, "/api/users/"),
		p == "/api/invites" || strings.HasPrefix(p, "/api/invites/"),
		p == "/api/settings" || strings.HasPrefix(p, "/api/settings/"),
		p == "/api/reindex":
		t.Fatalf("%s: normal user got a success on an admin route: %s", desc, data)
	}
	if m := spaceRoutePattern.FindStringSubmatch(p); m != nil && !wasMember {
		if m[2] != "" || method == "PATCH" || method == "DELETE" {
			t.Fatalf("%s: normal user changed or read members of a space it does not belong to: %s", desc, data)
		}
	}
}

// pluginUnavailableRoute reports whether a 503 on a path means a plugin or a
// remote service was unreachable.
func pluginUnavailableRoute(p string) bool {
	for _, prefix := range []string{
		"/api/commands/", "/api/panels/", "/api/modules/", "/api/import/",
		"/api/plugin/v1/ai/", "/api/plugin/v1/web/fetch",
		// Preview, install and check fetch the manifest from the base URL.
		"/api/admin/plugins",
	} {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// validHeaderValue reports whether s can travel as an HTTP header value.
func validHeaderValue(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// loopbackOnly reports whether every URL scheme separator in s is followed by
// a loopback host, so a fetch fails or answers at once.
func loopbackOnly(s string) bool {
	rest := s
	for {
		i := strings.Index(rest, "://")
		if i < 0 {
			return true
		}
		rest = rest[i+3:]
		host := strings.ToLower(rest)
		if !strings.HasPrefix(host, "127.0.0.1") && !strings.HasPrefix(host, "localhost") && !strings.HasPrefix(host, "[::1]") {
			return false
		}
	}
}
