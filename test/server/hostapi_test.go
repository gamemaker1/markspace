package server

// Tests of the plugin host API under /api/plugin/v1/: authentication,
// permission groups, grants, rate limiting, idempotency, list envelopes,
// storage quotas, notes, files, suggestions, jobs, notifications and the web
// fetcher. They run over the whole stack with the test plugin.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/index"
	"github.com/gamemaker1/markspace/sdk"
)

// allPermissions lists every permission group.
var allPermissions = []string{
	"items:read", "items:write", "links:read", "search", "comments", "suggestions",
	"ai:complete", "ai:embed", "ai:vision", "ai:image", "web:fetch", "jobs", "storage", "ui", "events",
}

// hostRoute is one host API route with a sample path and its group.
type hostRoute struct {
	method, path, group string
}

// hostRoutes lists every host API route. {id} is replaced by a real id where
// a test needs one.
var hostRoutes = []hostRoute{
	{"GET", "/me", ""}, {"GET", "/settings", ""}, {"GET", "/secrets/api_key", ""},
	{"GET", "/spaces/{space}/tree", "items:read"}, {"GET", "/items/{note}", "items:read"},
	{"GET", "/files/{note}/content", "items:read"}, {"GET", "/files/{note}/text", "items:read"},
	{"POST", "/notes", "items:write"}, {"PATCH", "/notes/{note}", "items:write"},
	{"POST", "/folders", "items:write"}, {"POST", "/files", "items:write"},
	{"POST", "/items/{note}/move", "items:write"}, {"POST", "/items/{note}/copy", "items:write"},
	{"POST", "/items/{note}/trash", "items:write"},
	{"GET", "/notes/{note}/links", "links:read"}, {"GET", "/items/{note}/backlinks", "links:read"},
	{"POST", "/resolve", "links:read"}, {"GET", "/search?q=x", "search"},
	{"POST", "/comments", "comments"},
	{"POST", "/suggestions", "suggestions"}, {"GET", "/suggestions", "suggestions"},
	{"DELETE", "/suggestions/x", "suggestions"},
	{"POST", "/ai/complete", "ai:complete"}, {"POST", "/ai/embed", "ai:embed"},
	{"POST", "/ai/image", "ai:image"}, {"GET", "/ai/models", "ai:*"},
	{"POST", "/web/fetch", "web:fetch"},
	{"POST", "/jobs", "jobs"}, {"PATCH", "/jobs/x", "jobs"}, {"POST", "/jobs/x/log", "jobs"},
	{"GET", "/kv", "storage"}, {"GET", "/kv/k", "storage"}, {"PUT", "/kv/k", "storage"},
	{"DELETE", "/kv/k", "storage"}, {"PUT", "/blobs/b", "storage"}, {"GET", "/blobs/b", "storage"},
	{"DELETE", "/blobs/b", "storage"},
	{"POST", "/notify", "ui"}, {"POST", "/events", "events"},
}

// hostEnv is a stack with the test plugin installed with every permission
// and an editor grant on one space.
type hostEnv struct {
	*pluginStack
	sp    *core.Space
	nt    *core.Note
	tp    *testPlugin
	token string
}

func newHostEnv(t *testing.T) *hostEnv {
	t.Helper()
	s := newPluginStack(t)
	sp := s.space("Team")
	note := s.note(sp.ID, "Target", "Hello world, see [[Other]].\n")
	tp := newTestPlugin(t, testManifest("test-plugin", allPermissions...))
	res := s.install(tp.srv.URL, []map[string]string{{"spaceId": sp.ID, "role": "editor"}})
	if res["delivered"] != true {
		t.Fatalf("install not delivered: %v", res)
	}
	return &hostEnv{pluginStack: s, sp: sp, nt: note, tp: tp, token: tp.pluginToken()}
}

// installPlugin starts and installs another test plugin.
func (e *hostEnv) installPlugin(t *testing.T, id string, perms []string, grants []map[string]string) *testPlugin {
	t.Helper()
	if perms == nil {
		perms = []string{}
	}
	man := testManifest(id, "ui")
	man.Permissions = perms
	tp := newTestPlugin(t, man)
	e.install(tp.srv.URL, grants)
	return tp
}

// person creates a user with a token.
func (e *hostEnv) person(t *testing.T, name string) (string, string) {
	t.Helper()
	email := strings.ToLower(name) + "@example.com"
	var u core.User
	e.call(e.adminTok, "POST", "/api/users", core.UserInput{Email: ptr(email), Name: ptr(name), Password: ptr("another good password")}, &u, 201)
	return u.ID, e.tokenFor(email, "another good password")
}

// member adds a user to a space.
func (e *hostEnv) member(t *testing.T, spaceID, userID string, role core.Role) {
	t.Helper()
	e.call(e.adminTok, "POST", "/api/spaces/"+spaceID+"/members", map[string]any{"userId": userID, "role": role}, nil, 201)
}

// acting runs the hello command as a user on an item and returns the acting
// token the plugin received.
func (e *hostEnv) acting(t *testing.T, tp *testPlugin, pluginID, userToken, itemID string) string {
	t.Helper()
	st, body, _ := e.request(userToken, "POST", "/api/commands/"+pluginID+".hello", map[string]any{"context": map[string]string{"itemId": itemID}})
	if st != 200 {
		t.Fatalf("command: %d %s", st, body)
	}
	tp.mu.Lock()
	defer tp.mu.Unlock()
	if !strings.HasPrefix(tp.lastToken, core.ActingTokenPrefix) {
		t.Fatalf("no acting token, got %q", tp.lastToken)
	}
	return tp.lastToken
}

// host sends a host API request.
func (e *hostEnv) host(token, method, path string, body any, header ...string) (int, []byte, http.Header) {
	return e.request(token, method, "/api/plugin/v1"+path, body, header...)
}

func (e *hostEnv) routePath(r hostRoute) string {
	return strings.NewReplacer("{space}", e.sp.ID, "{note}", e.nt.ID).Replace(r.path)
}

func isJSONError(data []byte) bool {
	var v struct {
		Error string `json:"error"`
	}
	return json.Unmarshal(data, &v) == nil && v.Error != ""
}

func TestHostAPIUnauthorized(t *testing.T) {
	e := newHostEnv(t)
	tokens := map[string]string{
		"none": "", "bad plugin token": "mspl_notarealtokennotarealtokennotarealtoke",
		"unknown acting token": "msat_unknownunknownunknownunknownunknownun",
		"person API token":     e.adminTok,
	}
	for name, tok := range tokens {
		for _, r := range hostRoutes {
			st, body, _ := e.host(tok, r.method, e.routePath(r), "{}")
			if st != 401 || !isJSONError(body) {
				t.Errorf("%s %s %s: status %d %s, want 401", name, r.method, r.path, st, body)
			}
		}
	}
	// A plugin token outside the host API counts as no credential.
	if st, body, _ := e.request(e.token, "GET", "/api/tokens", nil); st != 401 {
		t.Errorf("plugin token on /api/tokens: %d %s", st, body)
	}
	// An acting token expires after ten minutes on the host clock.
	act := e.acting(t, e.tp, "test-plugin", e.adminTok, e.nt.ID)
	if st, body, _ := e.host(act, "GET", "/items/"+e.nt.ID, nil); st != 200 {
		t.Fatalf("fresh acting token: %d %s", st, body)
	}
	e.clock.Advance(11 * time.Minute)
	if st, _, _ := e.host(act, "GET", "/items/"+e.nt.ID, nil); st != 401 {
		t.Errorf("expired acting token: status %d, want 401", st)
	}
}

func TestHostAPIPermissionGroups(t *testing.T) {
	e := newHostEnv(t)
	groups := []string{}
	seen := map[string]bool{}
	for _, r := range hostRoutes {
		if r.group != "" && !seen[r.group] {
			seen[r.group] = true
			groups = append(groups, r.group)
		}
	}
	grant := []map[string]string{{"spaceId": e.sp.ID, "role": "editor"}}
	for i, group := range groups {
		var perms []string
		for _, p := range allPermissions {
			if p == group || (group == "ai:*" && strings.HasPrefix(p, "ai:")) {
				continue
			}
			perms = append(perms, p)
		}
		id := fmt.Sprintf("group-%d", i)
		tp := e.installPlugin(t, id, perms, grant)
		tok := tp.pluginToken()
		for _, r := range hostRoutes {
			st, body, _ := e.host(tok, r.method, e.routePath(r), "{}")
			switch {
			case r.group == group:
				if st != 403 || !isJSONError(body) {
					t.Errorf("without %s: %s %s = %d %s, want 403", group, r.method, r.path, st, body)
				}
			case r.group == "" || (group == "ai:*" && strings.HasPrefix(r.group, "ai:")):
				if r.group == "" && st >= 400 {
					t.Errorf("without %s: %s %s = %d %s, want success", group, r.method, r.path, st, body)
				}
			default:
				if st == 403 && strings.Contains(string(body), "permission") {
					t.Errorf("without %s: %s %s refused for its own group: %s", group, r.method, r.path, body)
				}
			}
		}
	}
	// A plugin with no permissions still reads its identity, settings and
	// secrets.
	bare := e.installPlugin(t, "bare", nil, grant)
	for _, p := range []string{"/me", "/settings", "/secrets/api_key"} {
		if st, body, _ := e.host(bare.pluginToken(), "GET", p, nil); st != 200 {
			t.Errorf("bare %s: %d %s", p, st, body)
		}
	}
}

func TestHostAPIGrants(t *testing.T) {
	e := newHostEnv(t)
	other := e.space("Other")
	hidden := e.note(other.ID, "Hidden", "secret\n")
	if st, _, _ := e.host(e.token, "GET", "/spaces/"+e.sp.ID+"/tree", nil); st != 200 {
		t.Fatalf("granted tree: %d", st)
	}
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/spaces/" + other.ID + "/tree", nil},
		{"GET", "/items/" + hidden.ID, nil},
		{"POST", "/notes", map[string]string{"spaceId": other.ID, "title": "Nope"}},
	} {
		if st, body, _ := e.host(e.token, c.method, c.path, c.body); st != 404 {
			t.Errorf("ungranted %s %s: %d %s, want 404", c.method, c.path, st, body)
		}
	}

	viewer := e.installPlugin(t, "viewer", allPermissions, []map[string]string{{"spaceId": e.sp.ID, "role": "viewer"}})
	if st, body, _ := e.host(viewer.pluginToken(), "POST", "/notes", map[string]string{"spaceId": e.sp.ID, "title": "No"}); st != 403 {
		t.Errorf("viewer write: %d %s, want 403", st, body)
	}
	if st, _, _ := e.host(viewer.pluginToken(), "GET", "/items/"+e.nt.ID, nil); st != 200 {
		t.Errorf("viewer read: %d", st)
	}

	// Acting tokens: space A is granted to the plugin and the person, B only
	// to the plugin, C only to the person.
	both := e.installPlugin(t, "both", allPermissions, []map[string]string{
		{"spaceId": e.sp.ID, "role": "editor"}, {"spaceId": other.ID, "role": "editor"},
	})
	userID, userTok := e.person(t, "Pat")
	e.member(t, e.sp.ID, userID, core.RoleEditor)
	personOnly := e.space("Personal of Pat")
	e.member(t, personOnly.ID, userID, core.RoleEditor)
	mine := e.note(personOnly.ID, "Pat note", "pat\n")
	act := e.acting(t, both, "both", userTok, e.nt.ID)
	if st, body, _ := e.host(act, "GET", "/items/"+e.nt.ID, nil); st != 200 {
		t.Errorf("acting on shared space: %d %s", st, body)
	}
	if st, _, _ := e.host(act, "GET", "/items/"+hidden.ID, nil); st != 404 {
		t.Errorf("acting token reached a note the person cannot see: %d", st)
	}
	if st, _, _ := e.host(act, "GET", "/items/"+mine.ID, nil); st != 404 {
		t.Errorf("acting token reached a note the plugin is not granted: %d", st)
	}
	// A viewer person with an editor plugin cannot write.
	e.call(e.adminTok, "PATCH", "/api/spaces/"+e.sp.ID+"/members/"+userID, map[string]any{"role": "viewer"}, nil, 200)
	if st, body, _ := e.host(act, "POST", "/notes", map[string]string{"spaceId": e.sp.ID, "title": "Pat writes"}); st != 403 {
		t.Errorf("acting write as a viewer person: %d %s, want 403", st, body)
	}
}

func TestHostAPIRateLimit(t *testing.T) {
	e := newHostEnv(t)
	limited := 0
	retry := ""
	for i := 0; i < 150; i++ {
		st, _, h := e.host(e.token, "GET", "/me", nil)
		if st == 429 {
			limited++
			retry = h.Get("Retry-After")
		}
	}
	if limited == 0 || retry == "" {
		t.Fatalf("150 quick requests: %d limited, Retry-After %q", limited, retry)
	}
	time.Sleep(1100 * time.Millisecond)
	if st, _, _ := e.host(e.token, "GET", "/me", nil); st != 200 {
		t.Errorf("after a second: %d, want 200", st)
	}
}

func TestHostAPIIdempotency(t *testing.T) {
	e := newHostEnv(t)
	body := map[string]string{"spaceId": e.sp.ID, "title": "Once"}
	st1, b1, h1 := e.host(e.token, "POST", "/notes", body, "Idempotency-Key", "k-1")
	st2, b2, h2 := e.host(e.token, "POST", "/notes", body, "Idempotency-Key", "k-1")
	if st1 != 201 || st2 != 201 || !bytes.Equal(b1, b2) {
		t.Fatalf("replay: %d %s / %d %s", st1, b1, st2, b2)
	}
	if h1.Get("Idempotent-Replayed") != "" || h2.Get("Idempotent-Replayed") != "true" {
		t.Errorf("replay header: first %q second %q", h1.Get("Idempotent-Replayed"), h2.Get("Idempotent-Replayed"))
	}
	var tree struct{ Items []*core.TreeItem }
	e.call(e.token, "GET", "/api/plugin/v1/spaces/"+e.sp.ID+"/tree", nil, &tree, 200)
	n := 0
	for _, it := range tree.Items {
		if it.Note != nil && it.Note.Title == "Once" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("notes titled Once: %d, want 1", n)
	}
	if st, body, _ := e.host(e.token, "POST", "/folders", map[string]string{"spaceId": e.sp.ID, "name": "F"}, "Idempotency-Key", "k-1"); st != 409 {
		t.Errorf("key on another path: %d %s, want 409", st, body)
	}
	if st, _, _ := e.host(e.token, "POST", "/notes", body, "Idempotency-Key", strings.Repeat("k", 129)); st != 400 {
		t.Errorf("long key: %d, want 400", st)
	}
	// AI is off in this stack, so the call fails with 503 and the key is
	// released: a retry runs again rather than replaying.
	ai := map[string]string{"task": "ask", "question": "hi"}
	st, b, _ := e.host(e.token, "POST", "/ai/complete", ai, "Idempotency-Key", "k-ai")
	if st < 500 {
		t.Fatalf("ai with AI off: %d %s, want 5xx", st, b)
	}
	st, _, h := e.host(e.token, "POST", "/ai/complete", ai, "Idempotency-Key", "k-ai")
	if st < 500 || h.Get("Idempotent-Replayed") != "" {
		t.Errorf("retry after 5xx: %d replayed %q", st, h.Get("Idempotent-Replayed"))
	}
	// PUT ignores the header.
	e.host(e.token, "PUT", "/kv/p", map[string]any{"value": 1}, "Idempotency-Key", "k-put")
	_, _, h = e.host(e.token, "PUT", "/kv/p", map[string]any{"value": 2}, "Idempotency-Key", "k-put")
	var got struct{ Value int }
	e.call(e.token, "GET", "/api/plugin/v1/kv/p", nil, &got, 200)
	if got.Value != 2 || h.Get("Idempotent-Replayed") != "" {
		t.Errorf("PUT with a key: value %d replayed %q", got.Value, h.Get("Idempotent-Replayed"))
	}
}

// envelope decodes a list envelope and reports whether nextCursor is present.
func envelope(t *testing.T, data []byte) ([]json.RawMessage, string, bool) {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("envelope %s: %v", data, err)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw["items"], &items); err != nil || items == nil {
		t.Fatalf("envelope without items array: %s", data)
	}
	var next string
	_, has := raw["nextCursor"]
	json.Unmarshal(raw["nextCursor"], &next)
	return items, next, has
}

func TestHostAPIListEnvelopes(t *testing.T) {
	e := newHostEnv(t)
	for i := 0; i < 5; i++ {
		e.call(e.token, "PUT", fmt.Sprintf("/api/plugin/v1/kv/list-%d", i), map[string]any{"value": i}, nil, 204)
	}
	var keys []string
	cursor := ""
	for page := 0; page < 5; page++ {
		st, data, _ := e.host(e.token, "GET", "/kv?prefix=list-&limit=2&cursor="+url.QueryEscape(cursor), nil)
		if st != 200 {
			t.Fatalf("kv list: %d %s", st, data)
		}
		items, next, has := envelope(t, data)
		for _, it := range items {
			var k struct{ Key string }
			json.Unmarshal(it, &k)
			keys = append(keys, k.Key)
		}
		if !has {
			break
		}
		if next == "" {
			t.Fatalf("empty nextCursor present: %s", data)
		}
		cursor = next
	}
	if strings.Join(keys, ",") != "list-0,list-1,list-2,list-3,list-4" {
		t.Errorf("paged keys: %v", keys)
	}

	var s struct{ ID string }
	e.call(e.token, "POST", "/api/plugin/v1/suggestions", map[string]any{
		"noteId": e.nt.ID, "title": "Wording",
		"edits": []map[string]any{{"anchor": map[string]any{"exact": "world", "start": 6, "end": 11}, "text": "there"}},
	}, &s, 201)
	for _, p := range []string{
		"/spaces/" + e.sp.ID + "/tree", "/search?q=hello", "/suggestions?author=me",
		"/notes/" + e.nt.ID + "/links", "/items/" + e.nt.ID + "/backlinks",
	} {
		st, data, _ := e.host(e.token, "GET", p, nil)
		if st != 200 {
			t.Errorf("%s: %d %s", p, st, data)
			continue
		}
		envelope(t, data)
	}
	st, data, _ := e.host(e.token, "POST", "/resolve", map[string]any{"spaceId": e.sp.ID, "targets": []string{"Target"}})
	if st != 200 {
		t.Fatalf("resolve: %d %s", st, data)
	}
	envelope(t, data)
}

func TestHostAPIKeyValue(t *testing.T) {
	e := newHostEnv(t)
	e.call(e.token, "PUT", "/api/plugin/v1/kv/a%2Fb", map[string]any{"value": map[string]any{"n": 1, "s": "x"}}, nil, 204)
	var got struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	e.call(e.token, "GET", "/api/plugin/v1/kv/a%2Fb", nil, &got, 200)
	if got.Key != "a/b" || string(got.Value) != `{"n":1,"s":"x"}` {
		t.Errorf("kv get: %s %s", got.Key, got.Value)
	}
	e.call(e.token, "DELETE", "/api/plugin/v1/kv/a%2Fb", nil, nil, 204)
	if st, _, _ := e.host(e.token, "GET", "/kv/a%2Fb", nil); st != 404 {
		t.Errorf("deleted key: %d", st)
	}
	if st, _, _ := e.host(e.token, "PUT", "/kv/bad", `{"value": }`); st != 400 {
		t.Errorf("invalid JSON: %d, want 400", st)
	}
	big := `{"value":"` + strings.Repeat("x", index.MaxKVValue) + `"}`
	if st, body, _ := e.host(e.token, "PUT", "/kv/big", big); st != 413 {
		t.Errorf("value over 1 MiB: %d %s, want 413", st, body)
	}
	// Fill the store to the quota directly, then one more value is refused.
	fill := bytes.Repeat([]byte("1"), index.MaxKVValue)
	for i := 0; i < index.MaxKVTotal/index.MaxKVValue; i++ {
		if err := e.db.PutKV(context.Background(), "test-plugin", fmt.Sprintf("fill-%02d", i), fill, time.Now()); err != nil {
			t.Fatalf("fill %d: %v", i, err)
		}
	}
	if st, body, _ := e.host(e.token, "PUT", "/kv/over", map[string]any{"value": 1}); st != 429 {
		t.Errorf("kv quota: %d %s, want 429", st, body)
	}
}

func TestHostAPIBlobs(t *testing.T) {
	e := newHostEnv(t)
	put := func(key, ct string, body []byte) (int, []byte) {
		req, _ := http.NewRequest("PUT", e.srv.URL+"/api/plugin/v1/blobs/"+key, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+e.token)
		req.Header.Set("Content-Type", ct)
		resp, err := e.client.Do(req)
		if err != nil {
			t.Fatalf("put blob: %v", err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, data
	}
	if st, body := put("docs%2Fa.txt", "text/plain", []byte("hello blob")); st != 200 {
		t.Fatalf("put: %d %s", st, body)
	}
	st, data, h := e.host(e.token, "GET", "/blobs/docs%2Fa.txt", nil)
	if st != 200 || string(data) != "hello blob" || h.Get("Content-Type") != "text/plain" {
		t.Errorf("get: %d %q %q", st, data, h.Get("Content-Type"))
	}
	sum := sha256.Sum256([]byte("docs/a.txt"))
	file := filepath.Join(e.cfg.DataDir, "internal", "plugins", "test-plugin", "blobs", hex.EncodeToString(sum[:]))
	if _, err := os.Stat(file); err != nil {
		t.Errorf("blob file: %v", err)
	}
	e.call(e.token, "DELETE", "/api/plugin/v1/blobs/docs%2Fa.txt", nil, nil, 204)
	if st, _, _ := e.host(e.token, "GET", "/blobs/docs%2Fa.txt", nil); st != 404 {
		t.Errorf("deleted blob: %d", st)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("blob file left after delete: %v", err)
	}

	// A declared length over 256 MiB is refused before the body is read.
	u, _ := url.Parse(e.srv.URL)
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(conn, "PUT /api/plugin/v1/blobs/huge HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nContent-Type: application/octet-stream\r\nContent-Length: %d\r\n\r\n",
		u.Host, e.token, int64(index.MaxBlobSize)+1)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("huge blob: %v", err)
	}
	if resp.StatusCode != 413 {
		t.Errorf("huge blob: %d, want 413", resp.StatusCode)
	}
	conn.Close()

	// Fake usage up to the quota through metadata, then any blob is refused.
	for i := 0; i < index.MaxBlobTotal/index.MaxBlobSize; i++ {
		meta := index.BlobMeta{PluginID: "test-plugin", Key: fmt.Sprintf("fake-%d", i), Size: index.MaxBlobSize,
			SHA256: strings.Repeat("0", 64), UpdatedAt: time.Now()}
		if err := e.db.PutBlobMeta(context.Background(), meta); err != nil {
			t.Fatalf("fake meta %d: %v", i, err)
		}
	}
	if st, body := put("more", "application/octet-stream", []byte("x")); st != 429 {
		t.Errorf("blob quota: %d %s, want 429", st, body)
	}
}

func TestHostAPISecretsSettingsAndMe(t *testing.T) {
	e := newHostEnv(t)
	var sec struct{ Name, Value string }
	e.call(e.token, "GET", "/api/plugin/v1/secrets/api_key", nil, &sec, 200)
	if sec.Value != "k-123" {
		t.Errorf("secret: %+v", sec)
	}
	if st, _, _ := e.host(e.token, "GET", "/secrets/other", nil); st != 404 {
		t.Errorf("undeclared secret: %d", st)
	}
	var settings struct {
		Settings map[string]any `json:"settings"`
	}
	e.call(e.token, "GET", "/api/plugin/v1/settings", nil, &settings, 200)
	if settings.Settings["greeting"] != "hi" {
		t.Errorf("settings: %+v", settings)
	}
	var me struct {
		PluginID    string               `json:"pluginId"`
		UserID      string               `json:"userId"`
		Grants      []map[string]string  `json:"grants"`
		Permissions []string             `json:"permissions"`
		ActingFor   *struct{ ID string } `json:"actingFor"`
	}
	e.call(e.token, "GET", "/api/plugin/v1/me", nil, &me, 200)
	var kind string
	e.db.SQL().QueryRow(`SELECT kind FROM users WHERE id = ?`, me.UserID).Scan(&kind)
	if me.PluginID != "test-plugin" || kind != "plugin" || len(me.Grants) != 1 || me.Grants[0]["spaceId"] != e.sp.ID ||
		len(me.Permissions) != len(allPermissions) || me.ActingFor != nil {
		t.Errorf("me: %+v kind %s", me, kind)
	}
	act := e.acting(t, e.tp, "test-plugin", e.adminTok, e.nt.ID)
	me.ActingFor = nil
	e.call(act, "GET", "/api/plugin/v1/me", nil, &me, 200)
	if me.ActingFor == nil || me.ActingFor.ID != e.adminID {
		t.Errorf("acting me: %+v", me)
	}
}

func TestHostAPINotesAndFrontmatter(t *testing.T) {
	e := newHostEnv(t)
	var created struct {
		ID          string         `json:"id"`
		Title       string         `json:"title"`
		UpdatedAt   string         `json:"updatedAt"`
		Frontmatter map[string]any `json:"frontmatter"`
	}
	e.call(e.token, "POST", "/api/plugin/v1/notes", map[string]any{
		"spaceId": e.sp.ID, "content": "body\n",
		"frontmatter": map[string]any{"title": "From FM", "tags": []string{"wiki"}, "custom": "x"},
	}, &created, 201)
	if created.Title != "From FM" || created.Frontmatter["custom"] != "x" {
		t.Fatalf("create: %+v", created)
	}
	var item struct {
		Kind string `json:"kind"`
		Note struct {
			Content     string         `json:"content"`
			UpdatedAt   string         `json:"updatedAt"`
			Frontmatter map[string]any `json:"frontmatter"`
		} `json:"note"`
	}
	e.call(e.token, "GET", "/api/plugin/v1/items/"+created.ID, nil, &item, 200)
	if item.Note.Frontmatter["custom"] != "x" || item.Note.Frontmatter["title"] != "From FM" || item.Note.Content != "body\n" {
		t.Errorf("item: %+v", item)
	}
	if st, _, _ := e.host(e.token, "PATCH", "/notes/"+created.ID, map[string]any{"content": "x"}); st != 400 {
		t.Errorf("patch without baseUpdatedAt: %d, want 400", st)
	}
	base, _ := time.Parse(time.RFC3339Nano, item.Note.UpdatedAt)
	if st, body, _ := e.host(e.token, "PATCH", "/notes/"+created.ID, map[string]any{"content": "x", "baseUpdatedAt": base.Add(-time.Hour)}); st != 409 {
		t.Errorf("stale patch: %d %s, want 409", st, body)
	}
	e.call(e.token, "PATCH", "/api/plugin/v1/notes/"+created.ID, map[string]any{
		"frontmatter": map[string]any{"other": 2}, "baseUpdatedAt": item.Note.UpdatedAt,
	}, &created, 200)
	if created.Frontmatter["custom"] != "x" || created.Frontmatter["other"] != float64(2) {
		t.Errorf("merge: %+v", created.Frontmatter)
	}
	created.Frontmatter = nil
	e.call(e.token, "PATCH", "/api/plugin/v1/notes/"+created.ID, map[string]any{
		"frontmatter": map[string]any{"custom": nil}, "baseUpdatedAt": created.UpdatedAt,
	}, &created, 200)
	if _, ok := created.Frontmatter["custom"]; ok {
		t.Errorf("null did not delete custom: %+v", created.Frontmatter)
	}
	if st, _, _ := e.host(e.token, "POST", "/notes", map[string]any{"spaceId": e.sp.ID, "title": "T", "frontmatter": map[string]any{"id": "x"}}); st != 400 {
		t.Errorf("owned frontmatter key: %d, want 400", st)
	}
}

func TestHostAPIFolderNote(t *testing.T) {
	e := newHostEnv(t)
	st, b, _ := e.host(e.token, "POST", "/folders", map[string]string{"spaceId": e.sp.ID, "name": "Wiki"})
	var folder core.Folder
	if st != 201 || json.Unmarshal(b, &folder) != nil || folder.ID == "" {
		t.Fatalf("create folder: %d %s", st, b)
	}
	body := map[string]any{"spaceId": e.sp.ID, "folderId": folder.ID, "folderNote": true, "title": "Wiki index", "content": "# Index\n"}
	var created struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	e.call(e.token, "POST", "/api/plugin/v1/notes", body, &created, 201)
	var tree struct{ Items []*core.TreeItem }
	e.call(e.token, "GET", "/api/plugin/v1/spaces/"+e.sp.ID+"/tree", nil, &tree, 200)
	index := ""
	for _, it := range tree.Items {
		if it.Folder != nil && it.Folder.ID == folder.ID {
			index = it.Folder.IndexNoteID
		}
	}
	if created.ID == "" || index != created.ID || created.Title != "Wiki index" {
		t.Fatalf("folder note %+v, folder index note %q", created, index)
	}
	if st, b, _ := e.host(e.token, "POST", "/notes", body); st != 409 {
		t.Errorf("second folder note: %d %s, want 409", st, b)
	}
	if st, b, _ := e.host(e.token, "POST", "/notes", map[string]any{"spaceId": e.sp.ID, "folderNote": true, "title": "X"}); st != 400 {
		t.Errorf("folderNote without folderId: %d %s, want 400", st, b)
	}
}

func TestHostAPIFiles(t *testing.T) {
	e := newHostEnv(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("spaceId", e.sp.ID)
	fw, _ := mw.CreateFormFile("file", "hello.txt")
	fw.Write([]byte("hello plugin file"))
	mw.Close()
	st, data, _ := e.host(e.token, "POST", "/files", buf.Bytes(), "Content-Type", mw.FormDataContentType())
	if st != 201 {
		t.Fatalf("upload: %d %s", st, data)
	}
	var f core.File
	json.Unmarshal(data, &f)
	broken := "--b\r\nContent-Disposition: form-data; name=\"spaceId\"\r\n\r\n" + e.sp.ID +
		"\r\n--b\r\nContent-Disposition: form-data; name=\"file\"; filename=\"x.txt\"\r\n\r\nhello\r\n--b-\xaf\xaf"
	if st, body, _ := e.host(e.token, "POST", "/files", broken, "Content-Type", "multipart/form-data; boundary=b"); st != 400 {
		t.Errorf("broken multipart upload: %d %s, want 400", st, body)
	}
	st, data, _ = e.host(e.token, "GET", "/files/"+f.ID+"/content", nil, "Range", "bytes=0-4")
	if st != 206 || string(data) != "hello" {
		t.Errorf("range: %d %q", st, data)
	}
	var text struct {
		Text      string `json:"text"`
		Truncated bool   `json:"truncated"`
	}
	waitFor(t, "file text", func() bool {
		e.call(e.token, "GET", "/api/plugin/v1/files/"+f.ID+"/text", nil, &text, 200)
		return strings.Contains(text.Text, "hello plugin file")
	})
	if text.Truncated {
		t.Error("short text reported truncated")
	}
}

func TestHostAPISuggestionsAndJobs(t *testing.T) {
	e := newHostEnv(t)
	var s struct{ ID, AuthorKind, AuthorID string }
	e.call(e.token, "POST", "/api/plugin/v1/suggestions", map[string]any{
		"noteId": e.nt.ID, "title": "Wording",
		"edits": []map[string]any{{"anchor": map[string]any{"exact": "world", "start": 6, "end": 11}, "text": "there"}},
	}, &s, 201)
	if s.AuthorKind != "plugin" || s.AuthorID != "test-plugin" {
		t.Errorf("author: %+v", s)
	}
	st, data, _ := e.host(e.token, "GET", "/suggestions?author=me&noteId="+e.nt.ID, nil)
	items, _, _ := envelope(t, data)
	if st != 200 || len(items) != 1 {
		t.Errorf("mine: %d %s", st, data)
	}
	other := e.installPlugin(t, "other", allPermissions, []map[string]string{{"spaceId": e.sp.ID, "role": "editor"}})
	if st, _, _ := e.host(other.pluginToken(), "DELETE", "/suggestions/"+s.ID, nil); st != 403 && st != 404 {
		t.Errorf("other plugin withdraw: %d", st)
	}
	st, data, _ = e.host(other.pluginToken(), "GET", "/suggestions?author=me", nil)
	if items, _, _ := envelope(t, data); st != 200 || len(items) != 0 {
		t.Errorf("other plugin sees: %s", data)
	}
	e.call(e.token, "DELETE", "/api/plugin/v1/suggestions/"+s.ID, nil, nil, 204)

	if st, _, _ := e.host(e.token, "POST", "/jobs", map[string]any{"kind": "nope"}); st != 400 {
		t.Errorf("unknown kind: %d", st)
	}
	var job struct{ ID, Status string }
	e.call(e.token, "POST", "/api/plugin/v1/jobs", map[string]any{
		"kind": "slow", "runAfter": time.Now().Add(24 * time.Hour),
	}, &job, 201)
	if job.Status != "queued" {
		t.Fatalf("job: %+v", job)
	}
	if st, _, _ := e.host(other.pluginToken(), "PATCH", "/jobs/"+job.ID, map[string]any{"progress": 1}); st != 404 {
		t.Errorf("other plugin patch: %d", st)
	}
	if st, _, _ := e.host(other.pluginToken(), "POST", "/jobs/"+job.ID+"/log", map[string]any{"level": "info", "message": "x"}); st != 404 {
		t.Errorf("other plugin log: %d", st)
	}
	if st, body, _ := e.host(e.token, "PATCH", "/jobs/"+job.ID, map[string]any{"progress": 1}); st != 409 {
		t.Errorf("patch a queued job: %d %s", st, body)
	}
	e.call(e.token, "POST", "/api/plugin/v1/jobs/"+job.ID+"/log", map[string]any{"level": "info", "message": "queued"}, nil, 204)
	if st, _, _ := e.host(e.token, "POST", "/jobs/"+job.ID+"/log", map[string]any{"level": "loud", "message": "x"}); st != 400 {
		t.Errorf("bad level: %d", st)
	}
}

func TestHostAPINotifyAndEvents(t *testing.T) {
	e := newHostEnv(t)
	userID, userTok := e.person(t, "Robin")
	e.member(t, e.sp.ID, userID, core.RoleViewer)
	outsiderID, _ := e.person(t, "Sam")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer "+userTok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("event stream: %v %v", err, resp)
	}
	defer resp.Body.Close()
	got := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		event := ""
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: ") && event == "plugin.notify":
				got <- strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	time.Sleep(100 * time.Millisecond)

	if st, body, _ := e.host(e.token, "POST", "/notify", map[string]any{"userIds": []string{outsiderID}, "message": "hi"}); st != 404 {
		t.Errorf("notify outsider: %d %s, want 404", st, body)
	}
	e.call(e.token, "POST", "/api/plugin/v1/notify", map[string]any{"userIds": []string{userID}, "message": "Wiki updated", "link": "/jobs"}, nil, 204)
	select {
	case data := <-got:
		if !strings.Contains(data, "Wiki updated") || !strings.Contains(data, "test-plugin") {
			t.Errorf("notify event: %s", data)
		}
	case <-ctx.Done():
		t.Fatal("no plugin.notify event on the user's stream")
	}
	if st, _, _ := e.host(e.token, "POST", "/notify", map[string]any{"message": "x", "link": "javascript:alert(1)", "userIds": []string{userID}}); st != 400 {
		t.Errorf("bad link: %d", st)
	}
	if st, _, _ := e.host(e.token, "POST", "/events", map[string]any{"type": "plugin.other.x"}); st != 400 {
		t.Errorf("foreign event type: %d", st)
	}
	e.call(e.token, "POST", "/api/plugin/v1/events", map[string]any{"type": "plugin.test-plugin.synced", "spaceId": e.sp.ID, "data": map[string]int{"n": 1}}, nil, 204)
}

func TestHostAPIWebFetch(t *testing.T) {
	e := newHostEnv(t)
	pdf := []byte("%PDF-1.4\n\x00\x01binary")
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			w.Header().Set("Content-Type", "text/plain")
			io.WriteString(w, "User-agent: *\nDisallow: /private\n")
		case "/page":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, "<html><head><title>Harbour</title></head><body><p>The east wall.</p></body></html>")
		case "/doc.pdf":
			w.Header().Set("Content-Type", "application/pdf")
			w.Write(pdf)
		default:
			w.Header().Set("Content-Type", "text/plain")
			io.WriteString(w, "private text")
		}
	}))
	defer site.Close()

	st, data, _ := e.host(e.token, "POST", "/web/fetch", map[string]any{"url": site.URL + "/private/x", "respectRobots": true})
	if st != 403 || errBody(data) != "robots.txt disallows this URL|robots_disallowed" {
		t.Errorf("robots: %d %s", st, data)
	}
	start := time.Now()
	var page sdk.WebPage
	e.call(e.token, "POST", "/api/plugin/v1/web/fetch", map[string]any{"url": site.URL + "/page", "respectRobots": true}, &page, 200)
	if page.Title != "Harbour" || !strings.Contains(page.Text, "east wall") || !strings.HasPrefix(page.Markdown, "# Harbour") {
		t.Errorf("html fetch: %+v", page)
	}
	var bin struct {
		ContentType string `json:"contentType"`
		BodyBase64  string `json:"bodyBase64"`
	}
	e.call(e.token, "POST", "/api/plugin/v1/web/fetch", map[string]any{"url": site.URL + "/doc.pdf"}, &bin, 200)
	if raw, _ := base64.StdEncoding.DecodeString(bin.BodyBase64); bin.ContentType != "application/pdf" || !bytes.Equal(raw, pdf) {
		t.Errorf("binary fetch: %+v", bin)
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Errorf("two fetches to one host took %s, want about 1 s apart", elapsed)
	}
}

// TestHostAPIWebFetchErrorStatus checks that a remote error status comes back
// as the status of a 200 result with no body, and that an HTML page that
// opens with its own heading does not get the title heading twice.
func TestHostAPIWebFetchErrorStatus(t *testing.T) {
	e := newHostEnv(t)
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gone":
			http.Error(w, "gone", http.StatusNotFound)
		case "/paywall":
			http.Error(w, "subscribers only", http.StatusForbidden)
		case "/moved":
			http.Redirect(w, r, "/gone", http.StatusMovedPermanently)
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, "<html><head><title>Harbour</title></head><body><h1>Harbour</h1><p>The east wall.</p></body></html>")
		}
	}))
	defer site.Close()

	cases := []struct {
		path, final string
		status      int
	}{{"/gone", "/gone", 404}, {"/paywall", "/paywall", 403}, {"/moved", "/gone", 404}}
	for _, c := range cases {
		var res struct {
			Status     int    `json:"status"`
			FinalURL   string `json:"finalUrl"`
			Text       string `json:"text"`
			BodyBase64 string `json:"bodyBase64"`
		}
		e.call(e.token, "POST", "/api/plugin/v1/web/fetch", map[string]any{"url": site.URL + c.path}, &res, 200)
		if res.Status != c.status || res.FinalURL != site.URL+c.final || res.Text != "" || res.BodyBase64 != "" {
			t.Errorf("%s: %+v, want status %d at %s with no body", c.path, res, c.status, c.final)
		}
	}
	var page sdk.WebPage
	e.call(e.token, "POST", "/api/plugin/v1/web/fetch", map[string]any{"url": site.URL + "/titled"}, &page, 200)
	if !strings.HasPrefix(page.Markdown, "# Harbour\n") || strings.Count(page.Markdown, "# Harbour") != 1 {
		t.Errorf("markdown heading: %q", page.Markdown)
	}
}
