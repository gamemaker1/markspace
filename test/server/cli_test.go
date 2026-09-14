package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/control"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/index"
	"github.com/gamemaker1/markspace/internal/modules/system"
)

// ensureCLIToken runs the token step of serve and returns the token file
// content.
func ensureCLIToken(t *testing.T, s *pluginStack) string {
	t.Helper()
	path := filepath.Join(s.cfg.DataDir, control.TokenFile)
	if err := control.EnsureToken(t.Context(), s.app, path, nil); err != nil {
		t.Fatalf("ensure token: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("token file mode %o, want 600", mode)
	}
	tok, err := control.ReadToken(path)
	if err != nil || !strings.HasPrefix(tok, "msk_") {
		t.Fatalf("token file holds %q, %v", tok, err)
	}
	return tok
}

// bearer issues one request with a bearer token and returns the status.
func bearer(t *testing.T, s *pluginStack, token, method, path, body string) int {
	t.Helper()
	req, err := http.NewRequest(method, s.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

// TestCLITokenLifecycle checks the Markspace CLI token: created at the first
// start with mode 0600, kept across restarts, admin rights on the JSON API
// and nothing else, listed for admins, and replaced after a revoke or a lost
// file.
func TestCLITokenLifecycle(t *testing.T) {
	s := newPluginStack(t)
	tok := ensureCLIToken(t, s)
	if again := ensureCLIToken(t, s); again != tok {
		t.Fatal("a restart replaced a valid token")
	}

	for _, path := range []string{"/api/users", "/api/stats", "/api/admin/status", "/api/admin/tokens", "/api/settings"} {
		if code := bearer(t, s, tok, "GET", path, ""); code != 200 {
			t.Errorf("GET %s with the CLI token: %d, want 200", path, code)
		}
	}
	// Outside the JSON API the token is no credential.
	for _, path := range []string{"/admin/users", "/settings/tokens", "/"} {
		if code := bearer(t, s, tok, "GET", path, ""); code == 200 {
			t.Errorf("GET %s with the CLI token: 200, want a redirect to the login page", path)
		}
	}
	authn := &api.Authenticator{Backend: s.app, Logger: slog.New(slog.DiscardHandler)}
	for path, wantSystem := range map[string]bool{"/api/users": true, "/admin/users": false, "/api/plugin/v1/me": false, "/p/x": false} {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		if got := authn.Resolve(req).IsSystem(); got != wantSystem {
			t.Errorf("Resolve(%s).IsSystem() = %v, want %v", path, got, wantSystem)
		}
	}
	// The system actor has no account, so it cannot mint tokens or own
	// spaces.
	if code := bearer(t, s, tok, "POST", "/api/tokens", `{"name":"escalate"}`); code == 201 {
		t.Error("the CLI token created an API token")
	}
	if code := bearer(t, s, tok, "POST", "/api/spaces", `{"name":"Ops"}`); code == 201 {
		t.Error("the CLI token created a space")
	}

	// A person who is not an admin sees no system route.
	s.call(s.adminTok, "POST", "/api/users", core.UserInput{Email: ptr("pat@example.com"), Password: ptr("pat password 123")}, nil, 201)
	patTok := s.tokenFor("pat@example.com", "pat password 123")
	for _, path := range []string{"/api/admin/tokens", "/api/admin/status", "/api/admin/backup"} {
		if code := bearer(t, s, patTok, "GET", path, ""); code != 403 {
			t.Errorf("GET %s as a non-admin: %d, want 403", path, code)
		}
	}

	var list struct{ Items []*core.APIToken }
	s.call(s.adminTok, "GET", "/api/admin/tokens", nil, &list, 200)
	if len(list.Items) != 1 || list.Items[0].Name != control.TokenName || list.Items[0].UserID != "" {
		t.Fatalf("system tokens = %+v, want the one Markspace CLI token", list.Items)
	}
	// The Users page lists the token for admins.
	req, _ := http.NewRequest("GET", s.srv.URL+"/admin/users", nil)
	req.Header.Set("Authorization", "Bearer "+s.adminTok)
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(page), "Server tokens") || !strings.Contains(string(page), list.Items[0].Prefix) {
		t.Fatalf("admin users page: %d, want the Server tokens table with prefix %s", resp.StatusCode, list.Items[0].Prefix)
	}
	s.call(s.adminTok, "DELETE", "/api/admin/tokens/"+list.Items[0].ID, nil, nil, 204)
	if code := bearer(t, s, tok, "GET", "/api/users", ""); code != 401 {
		t.Fatalf("revoked token: %d, want 401", code)
	}

	rotated := ensureCLIToken(t, s)
	if rotated == tok {
		t.Fatal("the restart after a revoke kept the revoked token")
	}
	if code := bearer(t, s, rotated, "GET", "/api/users", ""); code != 200 {
		t.Fatalf("rotated token: %d, want 200", code)
	}

	if err := os.Remove(filepath.Join(s.cfg.DataDir, control.TokenFile)); err != nil {
		t.Fatal(err)
	}
	fresh := ensureCLIToken(t, s)
	if fresh == rotated || bearer(t, s, rotated, "GET", "/api/users", "") != 401 || bearer(t, s, fresh, "GET", "/api/users", "") != 200 {
		t.Fatal("a lost token file did not replace the stored token")
	}
	s.call(s.adminTok, "GET", "/api/admin/tokens", nil, &list, 200)
	if len(list.Items) != 1 {
		t.Fatalf("%d system tokens after two rotations, want 1", len(list.Items))
	}
}

// login signs in and returns the status and the session cookie.
func login(t *testing.T, s *pluginStack, email, password string) (int, *http.Cookie) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	resp, err := s.client.Post(s.srv.URL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name == api.SessionCookie {
			return resp.StatusCode, c
		}
	}
	return resp.StatusCode, nil
}

// TestCLICommands runs the calls of user add, passwd, admin, list, status,
// health, reindex and backup through the client against the whole stack.
func TestCLICommands(t *testing.T) {
	s := newPluginStack(t)
	c := control.New(s.srv.URL, ensureCLIToken(t, s))
	ctx := t.Context()

	if err := c.Health(ctx); err != nil {
		t.Fatalf("health: %v", err)
	}

	u, err := c.CreateUser(ctx, core.UserInput{Email: ptr("Rex@Example.com"), Name: ptr("Rex"), Password: ptr("first password 1")})
	if err != nil || u.Email != "rex@example.com" || u.IsAdmin {
		t.Fatalf("create user: %+v, %v", u, err)
	}
	if _, err := c.CreateUser(ctx, core.UserInput{Email: ptr("rex@example.com"), Password: ptr("first password 1")}); err == nil || !strings.Contains(err.Error(), "HTTP 409") {
		t.Fatalf("duplicate user: %v, want a 409", err)
	}
	status, cookie := login(t, s, "rex@example.com", "first password 1")
	if status != 200 || cookie == nil {
		t.Fatalf("login with the first password: %d", status)
	}

	found, err := c.UserByEmail(ctx, "REX@example.com")
	if err != nil || found.ID != u.ID {
		t.Fatalf("user by email: %+v, %v", found, err)
	}
	if _, err := c.UpdateUser(ctx, u.ID, core.UserInput{Password: ptr("second password 2")}); err != nil {
		t.Fatalf("passwd: %v", err)
	}
	req, _ := http.NewRequest("GET", s.srv.URL+"/api/auth/sessions", nil)
	req.AddCookie(cookie)
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("the old session after passwd: %d, want 401", resp.StatusCode)
	}
	if status, _ := login(t, s, "rex@example.com", "first password 1"); status == 200 {
		t.Fatal("the old password still signs in")
	}
	if status, _ := login(t, s, "rex@example.com", "second password 2"); status != 200 {
		t.Fatalf("login with the new password: %d", status)
	}

	promoted, err := c.UpdateUser(ctx, u.ID, core.UserInput{IsAdmin: ptr(true)})
	if err != nil || !promoted.IsAdmin {
		t.Fatalf("admin: %+v, %v", promoted, err)
	}
	users, err := c.Users(ctx)
	if err != nil || len(users) != 2 {
		t.Fatalf("user list: %d users, %v", len(users), err)
	}
	for _, x := range users {
		if !x.IsAdmin {
			t.Errorf("%s is not an admin", x.Email)
		}
	}

	st, err := c.Status(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.Version != "test" || st.Users != 2 || st.Spaces < 1 || st.DataDir != s.cfg.DataDir || st.Plugins == nil {
		t.Errorf("status = %+v", st)
	}
	if _, ok := st.Jobs["queued"]; !ok {
		t.Errorf("status jobs %v lack the queued count", st.Jobs)
	}

	if _, err := c.Reindex(ctx); err != nil {
		t.Fatalf("reindex: %v", err)
	}

	var archive bytes.Buffer
	if _, err := c.Backup(ctx, &archive); err != nil {
		t.Fatalf("backup: %v", err)
	}
	gz, err := gzip.NewReader(&archive)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var snapshot []byte
	notes := 0
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case hdr.Name == system.SnapshotName:
			snapshot, _ = io.ReadAll(tr)
		case strings.HasPrefix(hdr.Name, "spaces/") && strings.HasSuffix(hdr.Name, ".md"):
			notes++
		case hdr.Name == control.TokenFile:
			t.Error("the backup holds the CLI token")
		}
	}
	if notes == 0 {
		t.Error("the backup holds no note file")
	}
	restored := filepath.Join(t.TempDir(), "markspace.db")
	if err := os.WriteFile(restored, snapshot, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := index.Open(restored, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("open the snapshot: %v", err)
	}
	defer db.Close()
	if n, err := db.CountUsers(ctx); err != nil || n != 2 {
		t.Fatalf("snapshot users = %d, %v, want 2", n, err)
	}
}
