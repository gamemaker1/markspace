package system

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/api/testkit"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/index"
	"github.com/gamemaker1/markspace/internal/module"
	"github.com/gamemaker1/markspace/internal/modules/plugins"
)

// statsBackend answers Stats and resolves every session to an admin or a
// plain user.
type statsBackend struct {
	*testkit.Fake
}

func (statsBackend) Stats(context.Context, *core.Actor) (*core.Stats, error) {
	return &core.Stats{Users: 3, Spaces: 2, Notes: 7, Files: 1}, nil
}

type fakeJobs map[module.JobStatus]int

func (f fakeJobs) CountByStatus(context.Context) (map[module.JobStatus]int, error) { return f, nil }

type fakePlugins []plugins.PluginView

func (f fakePlugins) List(context.Context) ([]plugins.PluginView, error) { return f, nil }

func TestStatus(t *testing.T) {
	start := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	opts := Options{
		Jobs:    fakeJobs{module.JobQueued: 2, module.JobFailed: 1},
		Plugins: fakePlugins{{ID: "wiki", Name: "Wiki", Status: "down", HealthFailures: 3, LastError: "connection refused"}, {ID: "alpha", Status: "active"}},
		Listen:  ":8080", DataDir: "/data", Version: "v1.2.3",
		Started: start,
		Now:     func() time.Time { return start.Add(90 * time.Minute) },
	}

	opts.Backend = statsBackend{testkit.SignedIn(false)}
	h := testkit.Serve(t, testkit.Env(opts.Backend), New(opts))
	if rec := testkit.Do(t, h, "GET", "/api/admin/status", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status: %d, want 401", rec.Code)
	}
	if rec := testkit.Do(t, h, "GET", "/api/admin/status", "", "session"); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin status: %d, want 403", rec.Code)
	}
	if rec := testkit.Do(t, h, "GET", "/api/admin/backup", "", "session"); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin backup: %d, want 403", rec.Code)
	}

	opts.Backend = statsBackend{testkit.SignedIn(true)}
	h = testkit.Serve(t, testkit.Env(opts.Backend), New(opts))
	rec := testkit.Do(t, h, "GET", "/api/admin/status", "", "session")
	if rec.Code != http.StatusOK {
		t.Fatalf("admin status: %d %s", rec.Code, rec.Body)
	}
	var st Status
	testkit.DecodeBody(t, rec, &st)
	if st.Version != "v1.2.3" || st.UptimeSeconds != 5400 || st.Users != 3 || st.Spaces != 2 || st.Listen != ":8080" || st.DataDir != "/data" {
		t.Errorf("status = %+v", st)
	}
	if len(st.Jobs) != len(module.JobStatuses) || st.Jobs["queued"] != 2 || st.Jobs["failed"] != 1 || st.Jobs["running"] != 0 {
		t.Errorf("jobs = %v, want every status with queued 2 and failed 1", st.Jobs)
	}
	if len(st.Plugins) != 2 || st.Plugins[0].ID != "alpha" || st.Plugins[1].HealthFailures != 3 || st.Plugins[1].LastError == "" {
		t.Errorf("plugins = %+v, want alpha then wiki with its failures", st.Plugins)
	}
}

func TestBackupArchive(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.DiscardHandler)
	dbPath := filepath.Join(dir, "markspace.db")
	db, err := index.Open(dbPath, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.CreateUser(t.Context(), &core.User{ID: "u1", Email: "ada@example.com", Name: "Ada", IsActive: true}); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"spaces/home/note.md":         "# Note\n",
		"spaces/home/.tmp-0123":       "partial",
		"internal/history/a.md":       "old body\n",
		"plugins/wiki/state.json":     "{}",
		"plugins/wiki/.upload-99":     "partial",
		"cli.token":                   "msk_secret\n",
		".backup-left-from-before.db": "stale",
	}
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	backend := statsBackend{testkit.SignedIn(true)}
	h := testkit.Serve(t, testkit.Env(backend), New(Options{
		Backend: backend, DB: db.SQL(), DataDir: dir, DBPath: dbPath, Skip: []string{"cli.token"},
	}))
	rec := testkit.Do(t, h, "GET", "/api/admin/backup", "", "session")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/gzip" {
		t.Fatalf("backup: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	gz, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	got := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(tr)
		got[hdr.Name] = string(data)
	}
	if !strings.HasPrefix(got[SnapshotName], "SQLite format 3\x00") {
		t.Fatalf("the archive has no database snapshot, entries %v", keys(got))
	}
	for _, want := range []string{"spaces/home/note.md", "internal/history/a.md", "plugins/wiki/state.json", "spaces/", "spaces/home/"} {
		if _, ok := got[want]; !ok {
			t.Errorf("the archive lacks %s, entries %v", want, keys(got))
		}
	}
	for name := range got {
		if strings.Contains(name, ".tmp-") || strings.Contains(name, ".upload-") || strings.Contains(name, ".backup-") ||
			name == "cli.token" || strings.HasPrefix(name, "markspace.db-") {
			t.Errorf("the archive holds %s", name)
		}
	}

	// The snapshot is a working database with the row written before it.
	restored := filepath.Join(t.TempDir(), "markspace.db")
	if err := os.WriteFile(restored, []byte(got[SnapshotName]), 0o600); err != nil {
		t.Fatal(err)
	}
	rdb, err := index.Open(restored, logger)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer rdb.Close()
	if n, err := rdb.CountUsers(t.Context()); err != nil || n != 1 {
		t.Fatalf("snapshot users = %d, %v, want 1", n, err)
	}

	left, _ := filepath.Glob(filepath.Join(dir, snapshotPrefix+"*"))
	if len(left) != 1 {
		t.Fatalf("snapshot files left in the data dir: %v, want only the stale one", left)
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
