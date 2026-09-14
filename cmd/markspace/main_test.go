package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/modules/system"
)

// invoke runs the command with the given standard input and returns the exit
// code and both outputs.
func invoke(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, strings.NewReader(stdin), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// isolate keeps a test from reading a configuration file or token in the
// working directory and restores the variables the commands set.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("MARKSPACE_CONFIG", filepath.Join(dir, "missing.yaml"))
	t.Setenv("MARKSPACE_DATA_DIR", dir)
	t.Setenv("MARKSPACE_LISTEN", "")
	t.Setenv("MARKSPACE_TOKEN", "")
	return dir
}

func TestUsageErrors(t *testing.T) {
	isolate(t)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"bogus"}, `unknown command "bogus". Run "markspace help" for usage.`},
		{[]string{"-bogus"}, "flag provided but not defined: -bogus"},
		{[]string{"serve", "-port", "1"}, `flag provided but not defined: -port. Run "markspace help serve"`},
		{[]string{"serve", "now"}, `unexpected argument "now"`},
		{[]string{"version", "x"}, `unexpected argument "x"`},
		{[]string{"config"}, `missing subcommand. Run "markspace help config"`},
		{[]string{"config", "write"}, `unknown subcommand "write"`},
		{[]string{"config", "example", "-config", "x"}, "flag provided but not defined: -config"},
		{[]string{"config", "check", "extra"}, `unexpected argument "extra"`},
		{[]string{"health", "-token-file", "x"}, "flag provided but not defined: -token-file"},
		{[]string{"status", "now"}, `unexpected argument "now"`},
		{[]string{"status", "-json=maybe"}, "invalid boolean value"},
		{[]string{"user"}, "missing subcommand"},
		{[]string{"user", "delete", "-email", "a@b.c"}, `unknown subcommand "delete"`},
		{[]string{"user", "list", "x"}, `unexpected argument "x"`},
		{[]string{"user", "add"}, `-email is required. Run "markspace help user add"`},
		{[]string{"user", "add", "-email", " "}, "-email is required"},
		{[]string{"user", "add", "-email"}, "flag needs an argument: -email"},
		{[]string{"user", "passwd"}, "-email is required"},
		{[]string{"user", "passwd", "-email", "a@b.c", "-name", "x"}, "flag provided but not defined: -name"},
		{[]string{"user", "admin"}, "-email is required"},
		{[]string{"user", "admin", "-email", "a@b.c", "-revoke=sure"}, "invalid boolean value"},
		{[]string{"user", "disable"}, `-email is required. Run "markspace help user disable"`},
		{[]string{"user", "enable"}, `-email is required. Run "markspace help user enable"`},
		{[]string{"reindex", "-space", "notes"}, "flag provided but not defined: -space"},
		{[]string{"backup", "-out"}, "flag needs an argument: -out"},
	}
	for _, tc := range cases {
		code, stdout, stderr := invoke(t, "", tc.args...)
		if code != exitUsage || !strings.Contains(stderr, tc.want) || stdout != "" {
			t.Errorf("%q: exit %d stderr %q stdout %q, want exit 2 and %q", tc.args, code, stderr, stdout, tc.want)
		}
		if strings.Count(stderr, "\n") != 1 {
			t.Errorf("%q: stderr %q is not one line", tc.args, stderr)
		}
	}
}

// TestHelpReachesEveryUsage checks that each usage text prints through
// "markspace help" and through -h, and that nothing goes to stderr.
func TestHelpReachesEveryUsage(t *testing.T) {
	isolate(t)
	for cmd, text := range usages {
		words := strings.Fields(cmd)
		forms := [][]string{append([]string{"help"}, words...), append(append([]string{}, words...), "-h")}
		for _, args := range forms {
			code, stdout, stderr := invoke(t, "", args...)
			if code != exitOK || !strings.HasPrefix(stdout, text) || stderr != "" {
				t.Errorf("%q: exit %d stderr %q, stdout does not start with the %q usage", args, code, stderr, cmd)
			}
		}
	}
	_, stdout, _ := invoke(t, "", "help", "user", "add")
	for _, flag := range []string{"-email", "-name", "-admin", "-password-stdin", "-config", "-data", "-url", "-token-file"} {
		if !strings.Contains(stdout, flag) {
			t.Errorf("user add help lacks %s", flag)
		}
	}
}

func TestVersion(t *testing.T) {
	isolate(t)
	old := buildVersion
	buildVersion = "v1.2.3"
	defer func() { buildVersion = old }()
	for _, args := range [][]string{{"version"}, {"-version"}} {
		if code, stdout, _ := invoke(t, "", args...); code != 0 || stdout != "markspace v1.2.3\n" {
			t.Errorf("%q: exit %d stdout %q", args, code, stdout)
		}
	}
}

func TestConfigCommands(t *testing.T) {
	dir := isolate(t)
	if code, stdout, _ := invoke(t, "", "config", "example"); code != 0 || stdout != exampleConfig {
		t.Fatalf("config example: exit %d, output differs from the example", code)
	}

	path := filepath.Join(dir, "markspace.yaml")
	data := "listen: \"127.0.0.1:9123\"\nai:\n  providers:\n    - id: hosted\n      baseUrl: https://llm.example.com/v1\n      apiKey: sk-very-secret\n      headers:\n        X-Org: org-secret\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "other")
	code, stdout, stderr := invoke(t, "", "config", "check", "-config", path, "-data", other)
	if code != 0 {
		t.Fatalf("config check: exit %d stderr %q", code, stderr)
	}
	for _, want := range []string{"# Loaded from " + path, "listen: 127.0.0.1:9123", "dataDir: " + other, "apiKey: '[redacted]'", "X-Org: '[redacted]'"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("config check output lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "very-secret") || strings.Contains(stdout, "org-secret") {
		t.Errorf("config check printed a secret:\n%s", stdout)
	}

	if err := os.WriteFile(path, []byte("listen: :1\nlisten_typo: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr = invoke(t, "", "config", "check", "-config", path)
	if code != exitFailure || !strings.HasPrefix(stderr, "markspace: config "+path) {
		t.Errorf("invalid file: exit %d stderr %q, want exit 1", code, stderr)
	}
}

// fakeAPI is a server with the routes the management commands call.
type fakeAPI struct {
	mu       sync.Mutex
	srv      *httptest.Server
	created  []core.UserInput
	patched  map[string][]core.UserInput
	archive  []byte
	rejected bool
}

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{patched: map[string][]core.UserInput{}}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "markspace.db", Mode: 0o600, Size: 4})
	tw.Write([]byte("data"))
	tw.Close()
	gz.Close()
	f.archive = buf.Bytes()

	users := []core.User{
		{ID: "u1", Email: "ada@example.com", Name: "Ada", IsAdmin: true, IsActive: true},
		{ID: "u2", Email: "rex@example.com", Name: "Rex", IsActive: false},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /api/users", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"items": users, "total": len(users)})
	})
	mux.HandleFunc("POST /api/users", func(w http.ResponseWriter, r *http.Request) {
		var in core.UserInput
		json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		f.created = append(f.created, in)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(core.User{ID: "u3", Email: strings.ToLower(*in.Email), IsAdmin: in.IsAdmin != nil && *in.IsAdmin})
	})
	mux.HandleFunc("PATCH /api/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in core.UserInput
		json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		f.patched[r.PathValue("id")] = append(f.patched[r.PathValue("id")], in)
		f.mu.Unlock()
		json.NewEncoder(w).Encode(users[0])
	})
	mux.HandleFunc("GET /api/admin/status", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(system.Status{
			Version: "v9", UptimeSeconds: 3725, Listen: ":8080", DataDir: "/data", Users: 2, Spaces: 1,
			Jobs:    map[string]int{"queued": 4, "running": 1},
			Plugins: []system.PluginStatus{{ID: "wiki", Version: "0.1.0", Status: "down", HealthFailures: 3, LastError: "refused"}},
		})
	})
	mux.HandleFunc("POST /api/reindex", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"indexed":12}`))
	})
	mux.HandleFunc("GET /api/admin/backup", func(w http.ResponseWriter, r *http.Request) { w.Write(f.archive) })
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" && (f.rejected || r.Header.Get("Authorization") != "Bearer msk_cli") {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func TestManagementCommands(t *testing.T) {
	dir := isolate(t)
	api := newFakeAPI(t)
	t.Setenv("MARKSPACE_TOKEN", "msk_cli")
	url := api.srv.URL

	code, stdout, stderr := invoke(t, "", "user", "add", "-email", "New@Example.com", "-admin", "-url", url)
	if code != 0 || !strings.HasPrefix(stdout, "created new@example.com with admin rights\n") {
		t.Fatalf("user add: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	if strings.Count(stdout, "password: ") != 1 {
		t.Fatalf("user add printed the generated password %d times", strings.Count(stdout, "password: "))
	}
	sent := *api.created[0].Password
	if len(sent) != 24 || !strings.Contains(stdout, "password: "+sent+"\n") {
		t.Fatalf("generated password %q is not the one printed in %q", sent, stdout)
	}

	code, stdout, _ = invoke(t, "chosen password 1\nsecond line\n", "user", "add", "-email", "b@example.com", "-name", "Bee", "-password-stdin", "-url", url)
	if code != 0 || strings.Contains(stdout, "password:") || *api.created[1].Password != "chosen password 1" || *api.created[1].Name != "Bee" {
		t.Fatalf("user add -password-stdin: exit %d stdout %q input %+v", code, stdout, api.created[1])
	}

	if code, _, stderr := invoke(t, "", "user", "passwd", "-email", "ada@example.com", "-password-stdin", "-url", url); code != 1 || stderr != "markspace: no password on standard input\n" {
		t.Fatalf("empty stdin: exit %d stderr %q", code, stderr)
	}
	code, stdout, _ = invoke(t, "new password 22\r\n", "user", "passwd", "-email", "ADA@example.com", "-password-stdin", "-url", url)
	if code != 0 || len(api.patched["u1"]) != 1 || *api.patched["u1"][0].Password != "new password 22" {
		t.Fatalf("user passwd: exit %d stdout %q patches %+v", code, stdout, api.patched["u1"])
	}
	if code, _, stderr := invoke(t, "", "user", "passwd", "-email", "nobody@example.com", "-url", url); code != 1 || !strings.Contains(stderr, "no account has the email nobody@example.com") {
		t.Fatalf("unknown email: exit %d stderr %q", code, stderr)
	}

	invoke(t, "", "user", "admin", "-email", "rex@example.com", "-url", url)
	invoke(t, "", "user", "admin", "-email", "rex@example.com", "-revoke", "-url", url)
	invoke(t, "", "user", "disable", "-email", "rex@example.com", "-url", url)
	invoke(t, "", "user", "enable", "-email", "rex@example.com", "-url", url)
	p := api.patched["u2"]
	if len(p) != 4 || !*p[0].IsAdmin || *p[1].IsAdmin || *p[2].IsActive || !*p[3].IsActive || p[0].Password != nil {
		t.Fatalf("admin, revoke, disable and enable sent %+v", p)
	}

	code, stdout, _ = invoke(t, "", "user", "list", "-url", url)
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if code != 0 || len(lines) != 3 || !strings.HasPrefix(lines[0], "EMAIL") || !strings.Contains(lines[2], "disabled") {
		t.Fatalf("user list: exit %d\n%s", code, stdout)
	}
	if strings.Index(lines[1], "Ada") != strings.Index(lines[0], "NAME") {
		t.Errorf("user list columns are not aligned:\n%s", stdout)
	}
	code, stdout, _ = invoke(t, "", "user", "list", "-json", "-url", url)
	var listed []core.User
	if code != 0 || json.Unmarshal([]byte(stdout), &listed) != nil || len(listed) != 2 {
		t.Fatalf("user list -json: exit %d\n%s", code, stdout)
	}

	code, stdout, _ = invoke(t, "", "status", "-url", url)
	if code != 0 {
		t.Fatalf("status: exit %d", code)
	}
	fields := map[string]string{}
	valueCol := -1
	for _, line := range strings.Split(stdout, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || strings.ToUpper(line) == line {
			continue
		}
		key, value := strings.Join(f[:len(f)-1], " "), f[len(f)-1]
		fields[key] = value
		if key == "version" || strings.HasPrefix(key, "jobs ") {
			col := strings.LastIndex(line, value)
			if valueCol >= 0 && col != valueCol {
				t.Errorf("status values are not aligned in %q", line)
			}
			valueCol = col
		}
	}
	for key, want := range map[string]string{"version": "v9", "uptime": "1h2m5s", "data dir": "/data", "jobs queued": "4", "jobs failed": "0"} {
		if fields[key] != want {
			t.Errorf("status %s = %q, want %q:\n%s", key, fields[key], want, stdout)
		}
	}
	if !strings.Contains(stdout, "\nPLUGIN") || !regexpMatch(`wiki +0\.1\.0 +down +3 +never +refused`, stdout) {
		t.Errorf("status lacks the plugin table:\n%s", stdout)
	}
	code, stdout, _ = invoke(t, "", "status", "-json", "-url", url)
	var st system.Status
	if code != 0 || json.Unmarshal([]byte(stdout), &st) != nil || st.Jobs["queued"] != 4 {
		t.Fatalf("status -json: exit %d\n%s", code, stdout)
	}

	if code, stdout, _ := invoke(t, "", "reindex", "-url", url); code != 0 || stdout != "reindexed 12 files\n" {
		t.Fatalf("reindex: exit %d stdout %q", code, stdout)
	}

	out := filepath.Join(dir, "backup.tar.gz")
	if code, stdout, stderr := invoke(t, "", "backup", "-out", out, "-url", url); code != 0 || !strings.HasPrefix(stdout, "wrote "+out) {
		t.Fatalf("backup: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	if got, _ := os.ReadFile(out); !bytes.Equal(got, api.archive) {
		t.Fatal("backup -out wrote different bytes")
	}
	code, stdout, _ = invoke(t, "", "backup", "-url", url)
	if code != 0 || stdout != string(api.archive) {
		t.Fatalf("backup to stdout: exit %d, %d bytes", code, len(stdout))
	}

	if code, stdout, _ := invoke(t, "", "health", "-url", url); code != 0 || stdout != "the server at "+url+" is healthy\n" {
		t.Fatalf("health: exit %d stdout %q", code, stdout)
	}

	api.rejected = true
	if code, _, stderr := invoke(t, "", "status", "-url", url); code != 1 || stderr != "markspace: the server rejected the token at "+url+"\n" {
		t.Fatalf("rejected token: exit %d stderr %q", code, stderr)
	}
}

func TestConnectionFailures(t *testing.T) {
	dir := isolate(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	// Without -url the listen address of the configuration is used.
	t.Setenv("MARKSPACE_LISTEN", addr)
	code, _, stderr := invoke(t, "", "health")
	if code != 1 || !strings.HasPrefix(stderr, "markspace: the server is not reachable at http://"+addr+" (") || strings.Count(stderr, "\n") != 1 {
		t.Fatalf("health on a closed port: exit %d stderr %q", code, stderr)
	}

	code, _, stderr = invoke(t, "", "status")
	want := "markspace: no token: " + filepath.Join(dir, "cli.token") + " does not exist, start the server once or set MARKSPACE_TOKEN\n"
	if code != 1 || stderr != want {
		t.Fatalf("status without a token: exit %d stderr %q, want %q", code, stderr, want)
	}

	tokenFile := filepath.Join(dir, "token")
	os.WriteFile(tokenFile, []byte("msk_cli\n"), 0o600)
	code, _, stderr = invoke(t, "", "user", "list", "-token-file", tokenFile)
	if code != 1 || !strings.Contains(stderr, "not reachable at http://"+addr) {
		t.Fatalf("user list with a token file: exit %d stderr %q", code, stderr)
	}
}

// regexpMatch reports whether the pattern matches s.
func regexpMatch(pattern, s string) bool { return regexp.MustCompile(pattern).MatchString(s) }
