package store

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// debounceWindow collapses the several filesystem events one editor save
// produces into a single report.
const debounceWindow = 300 * time.Millisecond

// watchBuffer is the depth of the event channel handed to the caller.
const watchBuffer = 256

// Watch reports changes to notes, files and folders under the spaces
// directory until ctx ends, at which point the channel is closed. Events are
// debounced per path. Ignored names, names that fail ValidName and the trash
// are skipped. A directory that appears reports itself and every entry
// already inside it. A slow consumer loses events rather than blocking the
// watcher.
func (s *Store) Watch(ctx context.Context) (<-chan ports.FileEvent, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create watcher: %w", err)
	}
	w := &treeWatcher{
		store:   s,
		fsw:     fsw,
		out:     make(chan ports.FileEvent, watchBuffer),
		fired:   make(chan string, watchBuffer),
		pending: map[string]pendingEvent{},
		timers:  map[string]*time.Timer{},
		dirs:    map[string]bool{},
		done:    make(chan struct{}),
	}
	if err := os.MkdirAll(s.spacesDir, dirMode); err != nil {
		fsw.Close()
		return nil, fmt.Errorf("create watch root %q: %w", s.spacesDir, err)
	}
	if err := w.addTree(s.spacesDir, false); err != nil {
		fsw.Close()
		return nil, err
	}
	go w.run(ctx)
	return w.out, nil
}

// pendingEvent is the latest change seen for one path.
type pendingEvent struct {
	op    string
	isDir bool
	kind  core.ItemKind
}

// treeWatcher holds the state of one Watch call. Only the run goroutine
// touches pending, timers and dirs.
type treeWatcher struct {
	store   *Store
	fsw     *fsnotify.Watcher
	out     chan ports.FileEvent
	fired   chan string
	pending map[string]pendingEvent
	timers  map[string]*time.Timer
	dirs    map[string]bool
	done    chan struct{}
}

// addTree adds watches for root and every directory below it that the
// watcher reports on. When report is true, every entry below root is
// scheduled as a write.
func (w *treeWatcher) addTree(root string, report bool) error {
	err := filepath.WalkDir(root, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && full != root {
				return fs.SkipDir
			}
			return nil
		}
		isDir := d.IsDir()
		if full != root {
			if isDir && !w.watchable(full) {
				return fs.SkipDir
			}
			rel, ok := w.relPath(full)
			if ok && report && d.Type()&fs.ModeSymlink == 0 {
				if isDir {
					w.schedule(rel, pendingEvent{op: "write", isDir: true, kind: core.KindFolder})
				} else if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
					w.schedule(rel, pendingEvent{op: "write", kind: entryKind(d.Name(), info)})
				}
			}
		}
		if !isDir {
			return nil
		}
		if err := w.fsw.Add(full); err != nil {
			w.store.logger.Warn("could not watch directory", "path", full, "err", err)
			return nil
		}
		w.dirs[full] = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk watch root %q: %w", root, err)
	}
	return nil
}

// run pumps filesystem events until ctx ends.
func (w *treeWatcher) run(ctx context.Context) {
	defer func() {
		close(w.done)
		for _, t := range w.timers {
			t.Stop()
		}
		w.fsw.Close()
		close(w.out)
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			w.store.logger.Warn("watcher error", "err", err)
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.handle(ev)
		case rel := <-w.fired:
			w.emit(rel)
		}
	}
}

// handle classifies one filesystem event and schedules a report.
func (w *treeWatcher) handle(ev fsnotify.Event) {
	if ev.Op == fsnotify.Chmod {
		return
	}
	if ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
		wasDir := w.forgetDir(ev.Name)
		rel, ok := w.relPath(ev.Name)
		if !ok {
			return
		}
		kind := core.KindFile
		switch {
		case wasDir:
			kind = core.KindFolder
		case strings.HasSuffix(ev.Name, mdExt):
			kind = core.KindNote
		}
		w.schedule(rel, pendingEvent{op: "remove", isDir: wasDir, kind: kind})
		return
	}
	rel, ok := w.relPath(ev.Name)
	if !ok {
		// A new space directory is not an entry, but its contents are.
		if ev.Has(fsnotify.Create) && !w.dirs[ev.Name] && w.isSpaceDir(ev.Name) {
			if info, err := os.Lstat(ev.Name); err == nil && info.IsDir() {
				if err := w.addTree(ev.Name, true); err != nil {
					w.store.logger.Warn("could not watch new space", "path", ev.Name, "err", err)
				}
			}
		}
		return
	}
	info, err := os.Lstat(ev.Name)
	if err != nil {
		// The entry vanished before it could be inspected. The remove event
		// that follows reports it.
		return
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return
	}
	if info.IsDir() {
		if ev.Has(fsnotify.Create) && !w.dirs[ev.Name] {
			if err := w.addTree(ev.Name, true); err != nil {
				w.store.logger.Warn("could not watch new directory", "path", ev.Name, "err", err)
			}
		}
		w.schedule(rel, pendingEvent{op: "write", isDir: true, kind: core.KindFolder})
		return
	}
	if !info.Mode().IsRegular() {
		return
	}
	w.schedule(rel, pendingEvent{op: "write", kind: entryKind(filepath.Base(ev.Name), info)})
}

// forgetDir drops a removed directory and everything below it from the watch
// set and reports whether it was watched.
func (w *treeWatcher) forgetDir(full string) bool {
	if !w.dirs[full] {
		return false
	}
	prefix := full + string(filepath.Separator)
	for dir := range w.dirs {
		if dir == full || strings.HasPrefix(dir, prefix) {
			delete(w.dirs, dir)
			_ = w.fsw.Remove(dir)
		}
	}
	return true
}

// isSpaceDir reports whether full is a space directory directly below the
// spaces directory.
func (w *treeWatcher) isSpaceDir(full string) bool {
	rel, err := filepath.Rel(w.store.spacesDir, full)
	if err != nil || rel == "." || strings.ContainsRune(filepath.ToSlash(rel), '/') {
		return false
	}
	return validSlug(rel) == nil
}

// watchable reports whether the watcher adds a watch on the directory full:
// the spaces directory, a space directory or a folder it reports on.
func (w *treeWatcher) watchable(full string) bool {
	if full == w.store.spacesDir || w.isSpaceDir(full) {
		return true
	}
	_, ok := w.relPath(full)
	return ok
}

// relPath converts an absolute path into a path relative to the spaces
// directory, "<slug>/<entry path>", reporting whether the watcher reports on
// it.
func (w *treeWatcher) relPath(full string) (string, bool) {
	rel, err := filepath.Rel(w.store.spacesDir, full)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	segments := strings.Split(rel, "/")
	if len(segments) < 2 || validSlug(segments[0]) != nil {
		return "", false
	}
	for _, seg := range segments[1:] {
		if ValidName(seg) != nil {
			return "", false
		}
	}
	return rel, true
}

// schedule records the latest change for a path and restarts its debounce
// timer.
func (w *treeWatcher) schedule(rel string, ev pendingEvent) {
	w.pending[rel] = ev
	if t, ok := w.timers[rel]; ok {
		t.Reset(debounceWindow)
		return
	}
	path := rel
	w.timers[rel] = time.AfterFunc(debounceWindow, func() {
		select {
		case w.fired <- path:
		case <-w.done:
		}
	})
}

// emit sends one debounced event to the consumer, dropping it when the
// channel is full.
func (w *treeWatcher) emit(rel string) {
	pe, ok := w.pending[rel]
	if !ok {
		return
	}
	delete(w.pending, rel)
	if t, ok := w.timers[rel]; ok {
		t.Stop()
		delete(w.timers, rel)
	}
	slug, tail, found := strings.Cut(rel, "/")
	if !found || slug == "" || tail == "" {
		return
	}
	ev := ports.FileEvent{SpaceSlug: slug, RelPath: tail, Op: pe.op, IsDir: pe.isDir, Kind: pe.kind}
	select {
	case w.out <- ev:
	default:
		w.store.logger.Warn("dropping file event, consumer is behind", "space", slug, "path", tail, "op", pe.op)
	}
}
