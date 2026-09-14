package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/module"
	"github.com/gamemaker1/markspace/sdk"
)

// TestPluginJobActingToken runs the flow the wiki plugin uses for long work.
// A person runs a plugin command, the command queues a job with the command's
// acting token, and the job writes a note with the job-scoped acting token it
// receives. The note, its version and the job name the person as author and
// the plugin as via, and the job token stops working once the job ends.
func TestPluginJobActingToken(t *testing.T) {
	s := newPluginStack(t)
	ctx := context.Background()
	sp := s.space("Wiki")
	note := s.note(sp.ID, "Page", "first body\n")

	var person core.User
	s.call(s.adminTok, "POST", "/api/users", core.UserInput{Email: ptr("pat@example.com"), Name: ptr("Pat"), Password: ptr("another good password")}, &person, 201)
	personTok := s.tokenFor("pat@example.com", "another good password")
	s.call(s.adminTok, "POST", "/api/spaces/"+sp.ID+"/members", map[string]any{"userId": person.ID, "role": core.RoleEditor}, nil, 201)

	const pluginID = "job-writer"
	man := testManifest(pluginID)
	man.Events = nil
	man.Commands = []sdk.ManifestCommand{{ID: "start", Title: "Write with a job", Contexts: []string{"note"}}}
	man.Jobs = []sdk.ManifestJob{{Kind: "write", Title: "Write page", MaxAttempts: 1}}
	man.Schedules = nil
	man.Panels = nil
	man.Importers = nil

	var (
		mu           sync.Mutex
		commandToken string
		jobToken     string
		jobUser      string
		jobID        string
		finished     = make(chan struct{})
		once         sync.Once
	)
	p, err := sdk.New(man)
	if err != nil {
		t.Fatalf("plugin: %v", err)
	}
	p.DataDir = t.TempDir()
	p.JobWait = 50 * time.Millisecond
	p.HeartbeatEvery = 20 * time.Millisecond
	p.Getenv = func(string) string { return "" }
	p.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	p.Command("start", func(ctx context.Context, c *sdk.CommandContext) (sdk.Response, error) {
		job, err := c.Host.CreateJob(ctx, sdk.NewJob{Kind: "write", Title: "Write page", Payload: map[string]string{"noteId": c.ItemID()}})
		if err != nil {
			return nil, err
		}
		mu.Lock()
		commandToken = c.Host.Token
		jobID, _ = job["id"].(string)
		mu.Unlock()
		return sdk.Toast("Queued", "info"), nil
	})
	p.Job("write", func(ctx context.Context, j *sdk.JobContext) (any, error) {
		defer once.Do(func() { close(finished) })
		mu.Lock()
		jobToken = j.Host.Token
		if j.User != nil {
			jobUser = j.User.ID
		}
		mu.Unlock()
		var payload struct {
			NoteID string `json:"noteId"`
		}
		if err := j.DecodePayload(&payload); err != nil {
			return nil, err
		}
		item, err := j.Host.GetItem(ctx, payload.NoteID)
		if err != nil {
			return nil, err
		}
		n, _ := item["note"].(map[string]any)
		base, _ := n["updatedAt"].(string)
		content := "written by the job\n"
		if _, err := j.Host.UpdateNote(ctx, payload.NoteID, sdk.NoteUpdate{Content: &content, BaseUpdatedAt: base}); err != nil {
			return nil, err
		}
		return map[string]any{"written": true}, nil
	})
	srv := newSDKServer(t, p)
	if res := s.install(srv.URL, []map[string]string{{"spaceId": sp.ID, "role": "editor"}}); res["delivered"] != true {
		t.Fatalf("install not delivered: %v", res)
	}

	st, body, _ := s.request(personTok, "POST", "/api/commands/"+pluginID+".start", map[string]any{"context": map[string]string{"itemId": note.ID}})
	if st != http.StatusOK {
		t.Fatalf("command: %d %s", st, body)
	}
	select {
	case <-finished:
	case <-time.After(20 * time.Second):
		t.Fatal("the job did not run")
	}
	mu.Lock()
	gotCommand, gotJob, gotUser, id := commandToken, jobToken, jobUser, jobID
	mu.Unlock()

	store := s.parts.Jobs.Store()
	waitFor(t, "the job to finish", func() bool {
		j, err := store.Get(ctx, id)
		return err == nil && j.Status.Finished()
	})
	j, _ := store.Get(ctx, id)
	if j.Status != module.JobDone || j.CreatedBy != person.ID {
		t.Fatalf("job %s created by %q: %s", j.Status, j.CreatedBy, j.Message)
	}
	if !strings.HasPrefix(gotJob, core.ActingTokenPrefix) || gotJob == gotCommand {
		t.Fatalf("job token %q, command token %q: want a separate acting token", gotJob, gotCommand)
	}
	if gotUser != person.ID {
		t.Fatalf("job user %q, want %q", gotUser, person.ID)
	}

	var n core.Note
	s.call(personTok, "GET", "/api/notes/"+note.ID, nil, &n, 200)
	if n.Content != "written by the job\n" || n.UpdatedBy != person.ID || n.UpdatedVia != pluginID {
		t.Fatalf("note by %q via %q: %q", n.UpdatedBy, n.UpdatedVia, n.Content)
	}
	// A version holds the body before an edit, with that body's author and
	// via. The person's next forced snapshot keeps the job's body as a version.
	next := "edited by the person\n"
	var plain core.Note
	s.call(personTok, "PATCH", "/api/notes/"+note.ID, core.NoteUpdate{Content: &next, BaseUpdatedAt: &n.UpdatedAt, Snapshot: true}, &plain, 200)
	if plain.UpdatedBy != person.ID || plain.UpdatedVia != "" {
		t.Fatalf("plain edit by %q via %q", plain.UpdatedBy, plain.UpdatedVia)
	}
	_, raw, _ := s.request(personTok, "GET", "/api/notes/"+note.ID+"/versions", nil)
	var versions struct {
		Items []core.NoteVersion `json:"items"`
	}
	if err := json.Unmarshal(raw, &versions); err != nil {
		t.Fatalf("versions: %s", raw)
	}
	found := false
	for _, v := range versions.Items {
		if v.Via == pluginID {
			found = true
			if v.AuthorID != person.ID {
				t.Fatalf("version via %q has author %q, want %q", v.Via, v.AuthorID, person.ID)
			}
		}
	}
	if !found {
		t.Fatalf("no version via %s: %s", pluginID, raw)
	}

	// The job token is revoked when the attempt ends.
	waitFor(t, "the job token to be revoked", func() bool {
		st, _, _ := s.request(gotJob, "GET", "/api/plugin/v1/me", nil)
		return st == http.StatusUnauthorized
	})
}

// newSDKServer serves a sdk plugin over HTTP for the test.
func newSDKServer(t *testing.T, p *sdk.Plugin) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(p.Handler())
	t.Cleanup(srv.Close)
	return srv
}
