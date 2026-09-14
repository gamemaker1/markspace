package module

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"
)

var testBase = time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func mustAdd(t testing.TB, r *Registry, m Module) {
	t.Helper()
	if err := r.Add(m); err != nil {
		t.Fatalf("add %s: %v", m.Manifest().ID, err)
	}
}

func builtin(id string, fn func(r *Registry)) Module {
	return New(Manifest{ID: id, Name: id, Builtin: true}, func(r *Registry) error {
		fn(r)
		return nil
	})
}

func plugin(id string, fn func(r *Registry)) Module {
	return New(Manifest{ID: id, Name: id, Permissions: []string{"jobs", "ui"}}, func(r *Registry) error {
		fn(r)
		return nil
	})
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func noopJob(context.Context, *Job, JobRuntime) (json.RawMessage, error) { return nil, nil }

func noopEvent(context.Context, Event) error { return nil }

func noopHTTP(http.ResponseWriter, *http.Request) {}

func okCommand(id string, contexts ...string) Command {
	if len(contexts) == 0 {
		contexts = []string{"global"}
	}
	return Command{ID: id, Title: "Command " + id, Contexts: contexts,
		Run: func(context.Context, CommandRequest) (CommandResponse, error) {
			return CommandResponse{Toast: &Toast{Message: "ok"}}, nil
		}}
}

func okPanel(id string) Panel {
	return Panel{ID: id, Title: "Panel", Icon: "book", Where: "rail", Contexts: []string{"note"}, Builtin: "comments"}
}

func okImporter(id string) Importer {
	return Importer{ID: id, Title: "Importer", Accept: []string{".html", "text/html"},
		Import: func(context.Context, ImportRequest) (CommandResponse, error) { return CommandResponse{}, nil }}
}

func okTool(name string) AITool {
	return AITool{Name: name, Description: "A tool",
		Call: func(context.Context, Actor, json.RawMessage) (json.RawMessage, error) { return nil, nil }}
}

type recordingPublisher struct {
	mu     sync.Mutex
	events []Event
}

func (p *recordingPublisher) Publish(e Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, e)
}

func (p *recordingPublisher) jobUpdates(jobID string) []JobUpdatedData {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []JobUpdatedData
	for _, e := range p.events {
		if e.Type != EventJobUpdated {
			continue
		}
		var d JobUpdatedData
		if json.Unmarshal(e.Data, &d) == nil && d.JobID == jobID {
			out = append(out, d)
		}
	}
	return out
}
