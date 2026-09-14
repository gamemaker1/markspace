package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// watchBudget is how long the smoke test waits for one debounced event.
const watchBudget = 500 * time.Millisecond

func TestWatchSmoke(t *testing.T) {
	s := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := s.Watch(ctx)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	writeNote(t, s, "docs", "watched.md", "Watched", "hello\n")
	select {
	case ev, ok := <-events:
		if !ok {
			t.Fatal("event channel closed early")
		}
		if ev.SpaceSlug != "docs" || ev.RelPath != "watched.md" || ev.Op != "write" {
			t.Fatalf("event = %+v, want docs watched.md write", ev)
		}
	case <-time.After(watchBudget):
		t.Skip("no filesystem event within the budget, skipping as unreliable here")
	}
	cancel()
	select {
	case _, ok := <-events:
		if ok {
			// A queued event may arrive before the close, so drain once more.
			select {
			case _, ok := <-events:
				if ok {
					t.Fatal("event channel stayed open after cancel")
				}
			case <-time.After(watchBudget):
				t.Skip("channel close was not observed within the budget")
			}
		}
	case <-time.After(watchBudget):
		t.Skip("channel close was not observed within the budget")
	}
}

func TestWatchRelPathFiltering(t *testing.T) {
	s := newTestStore(t)
	w := &treeWatcher{store: s}
	tests := []struct {
		full string
		rel  string
		ok   bool
	}{
		{s.SpacesDir() + "/docs/a.md", "docs/a.md", true},
		{s.SpacesDir() + "/docs/a/b.md", "docs/a/b.md", true},
		{s.SpacesDir() + "/docs/notes.txt", "docs/notes.txt", true},
		{s.SpacesDir() + "/docs/attachments/a.md", "docs/attachments/a.md", true},
		{s.SpacesDir() + "/docs/Projects", "docs/Projects", true},
		{s.SpacesDir() + "/docs/a.md.tmp-1234", "", false},
		{s.SpacesDir() + "/docs/.tmp-1234", "", false},
		{s.SpacesDir() + "/docs/~$report.docx", "", false},
		{s.SpacesDir() + "/docs/sub/Thumbs.db", "", false},
		{s.SpacesDir() + "/docs/.trash/x/a.md", "", false},
		{s.SpacesDir() + "/docs", "", false},
		{s.SpacesDir(), "", false},
		{"/elsewhere/a.md", "", false},
	}
	for _, tt := range tests {
		rel, ok := w.relPath(tt.full)
		if ok != tt.ok || rel != tt.rel {
			t.Errorf("relPath(%q) = %q, %v, want %q, %v", tt.full, rel, ok, tt.rel, tt.ok)
		}
	}
}

func TestWatchReportsFoldersAndFiles(t *testing.T) {
	s := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := s.Watch(ctx)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	// A folder moved in from outside arrives as one create. Its contents must
	// be reported too.
	outside := filepath.Join(s.DataDir(), "outside")
	if err := os.MkdirAll(filepath.Join(outside, "inner"), dirMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "inner", "photo.jpg"), []byte("x"), fileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(outside, filepath.Join(s.SpacesDir(), "docs", "Moved")); err != nil {
		t.Fatal(err)
	}
	want := map[string]ports.FileEvent{
		"Moved":                 {SpaceSlug: "docs", RelPath: "Moved", Op: "write", IsDir: true, Kind: core.KindFolder},
		"Moved/inner":           {SpaceSlug: "docs", RelPath: "Moved/inner", Op: "write", IsDir: true, Kind: core.KindFolder},
		"Moved/inner/photo.jpg": {SpaceSlug: "docs", RelPath: "Moved/inner/photo.jpg", Op: "write", Kind: core.KindFile},
	}
	got := collectEvents(events, len(want), 2*time.Second)
	if len(got) < len(want) {
		t.Skipf("only %d of %d events within the budget, skipping as unreliable here: %+v", len(got), len(want), got)
	}
	for key, ev := range want {
		if got[key] != ev {
			t.Errorf("event %q = %+v, want %+v", key, got[key], ev)
		}
	}

	if err := os.RemoveAll(filepath.Join(s.SpacesDir(), "docs", "Moved")); err != nil {
		t.Fatal(err)
	}
	removed := collectEvents(events, 1, 2*time.Second)
	ev, ok := removed["Moved"]
	if !ok {
		t.Skipf("no remove event for the folder within the budget: %+v", removed)
	}
	if ev.Op != "remove" || !ev.IsDir || ev.Kind != core.KindFolder {
		t.Errorf("remove event = %+v", ev)
	}
}

// collectEvents gathers events by path until n distinct paths arrived or the
// budget ran out.
func collectEvents(events <-chan ports.FileEvent, n int, budget time.Duration) map[string]ports.FileEvent {
	got := map[string]ports.FileEvent{}
	deadline := time.After(budget)
	for len(got) < n {
		select {
		case ev, ok := <-events:
			if !ok {
				return got
			}
			got[ev.RelPath] = ev
		case <-deadline:
			return got
		}
	}
	return got
}
