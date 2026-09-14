package app

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// The fake DB keeps AI threads in a store looked up by the fakeDB pointer, so
// fake_test.go needs no new fields. Rows are copied on the way in and out.

type fakeThreadStore struct {
	mu       sync.Mutex
	threads  map[string]*core.AIThread
	messages map[string][]*core.AIThreadMessage
	seq      int
}

var fakeThreadStores sync.Map // *fakeDB -> *fakeThreadStore

func (d *fakeDB) threadStore() *fakeThreadStore {
	s, _ := fakeThreadStores.LoadOrStore(d, &fakeThreadStore{
		threads:  map[string]*core.AIThread{},
		messages: map[string][]*core.AIThreadMessage{},
	})
	return s.(*fakeThreadStore)
}

func copyThread(t *core.AIThread) *core.AIThread {
	cp := *t
	cp.Messages = nil
	return &cp
}

func copyMessage(m *core.AIThreadMessage) *core.AIThreadMessage {
	cp := *m
	cp.Context = append([]core.AIContextRef{}, m.Context...)
	cp.Sources = append([]core.AISource{}, m.Sources...)
	return &cp
}

func (d *fakeDB) CreateAIThread(ctx context.Context, t *core.AIThread) error {
	s := d.threadStore()
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.UserID == "" {
		return core.ErrInvalid
	}
	if t.ID == "" {
		t.ID = core.NewID()
	}
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = t.CreatedAt
	}
	if _, ok := s.threads[t.ID]; ok {
		return core.ErrExists
	}
	s.threads[t.ID] = copyThread(t)
	return nil
}

func (d *fakeDB) GetAIThread(ctx context.Context, id string) (*core.AIThread, error) {
	s := d.threadStore()
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.threads[id]
	if !ok {
		return nil, fmt.Errorf("fake thread %s: %w", id, core.ErrNotFound)
	}
	return copyThread(t), nil
}

func (d *fakeDB) ListAIThreads(ctx context.Context, userID, noteID string, since time.Time) ([]*core.AIThread, error) {
	s := d.threadStore()
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*core.AIThread
	for _, t := range s.threads {
		if t.UserID == userID && t.NoteID == noteID && !t.UpdatedAt.Before(since) {
			out = append(out, copyThread(t))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

func (d *fakeDB) AddAIMessage(ctx context.Context, m *core.AIThreadMessage) error {
	s := d.threadStore()
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.threads[m.ThreadID]
	if !ok {
		return core.ErrNotFound
	}
	if m.Role != core.AIRoleUser && m.Role != core.AIRoleAssistant {
		return core.ErrInvalid
	}
	if m.ID == "" {
		m.ID = core.NewID()
	}
	if m.CreatedAt.After(t.UpdatedAt) {
		t.UpdatedAt = m.CreatedAt
	}
	s.messages[m.ThreadID] = append(s.messages[m.ThreadID], copyMessage(m))
	return nil
}

func (d *fakeDB) ListAIMessages(ctx context.Context, threadID string) ([]*core.AIThreadMessage, error) {
	s := d.threadStore()
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*core.AIThreadMessage
	for _, m := range s.messages[threadID] {
		out = append(out, copyMessage(m))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (d *fakeDB) SetAIMessageApplied(ctx context.Context, threadID, messageID string, applied bool) error {
	s := d.threadStore()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.messages[threadID] {
		if m.ID == messageID {
			m.Applied = applied
			return nil
		}
	}
	return core.ErrNotFound
}

func (d *fakeDB) DeleteAIThread(ctx context.Context, id string) error {
	s := d.threadStore()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.threads[id]; !ok {
		return core.ErrNotFound
	}
	delete(s.threads, id)
	delete(s.messages, id)
	return nil
}

func (d *fakeDB) DeleteAIThreadsBefore(ctx context.Context, userID string, cutoff time.Time) (int, error) {
	s := d.threadStore()
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, t := range s.threads {
		if t.UpdatedAt.Before(cutoff) && (userID == "" || t.UserID == userID) {
			delete(s.threads, id)
			delete(s.messages, id)
			n++
		}
	}
	return n, nil
}

// threadCount returns the number of stored threads, expired ones included.
func (d *fakeDB) threadCount() int {
	s := d.threadStore()
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.threads)
}
