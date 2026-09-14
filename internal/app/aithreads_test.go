package app

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/ai"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// drainThread reads a conversation stream until it closes and returns the
// text, the Done chunk and the last error.
func drainThread(t *testing.T, ch <-chan core.AIThreadChunk) (string, *core.AIThreadChunk, error) {
	t.Helper()
	var (
		text strings.Builder
		done *core.AIThreadChunk
		err  error
	)
	timeout := time.After(5 * time.Second)
	for {
		select {
		case c, ok := <-ch:
			if !ok {
				return text.String(), done, err
			}
			text.WriteString(c.Delta)
			if c.Done {
				cc := c
				done = &cc
			}
			if c.Err != nil {
				err = c.Err
			}
		case <-timeout:
			t.Fatal("thread stream did not close")
		}
	}
}

// indexedNote inserts a note row into the fake index, so these tests do not
// depend on the file store.
func indexedNote(h *harness, sp *core.Space, title, body string) *core.Note {
	h.t.Helper()
	now := h.clock.Now()
	n := &core.Note{ID: core.NewID(), SpaceID: sp.ID, Title: title, Path: core.Slugify(title) + ".md", Content: body, CreatedAt: now, UpdatedAt: now}
	if err := h.db.UpsertNote(h.ctx, ports.NoteRow{Note: n, Plain: body, ModTime: now}); err != nil {
		h.t.Fatalf("index note %q: %v", title, err)
	}
	return n
}

// threadHarness gives each of three users a space of their own with notes:
// two for the first user and one for each of the others. The fake index has no
// item shares yet, so each user reaches only their own notes.
func threadHarness(t *testing.T) (*harness, []*core.Actor, [][]*core.Note) {
	t.Helper()
	h := newHarness(t)
	users := []*core.Actor{h.user("Owner", false), h.user("Bea", false), h.user("Cy", false)}
	notes := make([][]*core.Note, len(users))
	for i, u := range users {
		sp := h.space(u, "Space "+u.User.Name)
		notes[i] = append(notes[i], indexedNote(h, sp, "Survey "+u.User.Name, "The survey covers the north quay."))
		if i == 0 {
			notes[i] = append(notes[i], indexedNote(h, sp, "Budget", "Dredging quote pending."))
		}
	}
	return h, users, notes
}

// chips is the context of a composer turn in these tests. A selection keeps
// the resolver away from note bodies, which the fake file store cannot read.
var chips = []core.AIContextRef{{Kind: core.ContextSelection, Text: "The survey covers the north quay."}}

func TestAIThreadStreamStoresTurnsAndUsage(t *testing.T) {
	h, users, byUser := threadHarness(t)
	u, notes := users[0], byUser[0]
	setStream(h, func(ctx context.Context, _ []core.AIMessage, opts core.AIOptions) (<-chan core.AIChunk, error) {
		ch := make(chan core.AIChunk, 3)
		ch <- core.AIChunk{Delta: "The quay "}
		ch <- core.AIChunk{Delta: "is sound [1]."}
		ch <- core.AIChunk{Done: true, PromptTokens: 30, CompletionTokens: 5}
		close(ch)
		return ch, nil
	})
	th, err := h.app.CreateAIThread(h.ctx, u, core.AIThreadInput{NoteID: notes[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if th.Title != "Survey Owner" || th.UserID != u.UserID() || th.SpaceID != notes[0].SpaceID {
		t.Fatalf("thread = %+v", th)
	}
	ch, err := h.app.StreamAIThreadMessage(h.ctx, u, th.ID, core.AIThreadMessageInput{Content: "  Is the quay sound?  ", Context: chips})
	if err != nil {
		t.Fatal(err)
	}
	text, done, err := drainThread(t, ch)
	if err != nil || done == nil {
		t.Fatalf("stream = %q, %+v, %v", text, done, err)
	}
	if text != "The quay is sound [1]." || done.Route != core.RouteChat || done.Attempts != 1 || done.Provider != "local" {
		t.Fatalf("done = %+v", done)
	}
	if done.Message == nil || done.Message.Content != text || done.Message.Role != core.AIRoleAssistant {
		t.Fatalf("stored answer = %+v", done.Message)
	}
	if done.UserMessage == nil || done.UserMessage.Content != "Is the quay sound?" {
		t.Fatalf("stored question = %+v", done.UserMessage)
	}
	row := lastUsageOf(t, h, u.UserID())
	if row.Feature != core.FeatureChat || row.Task != core.RouteChat || row.Attempts != 1 || row.Status != core.UsageOK ||
		row.InputTokens != 30 || row.OutputTokens != 5 || row.NoteID != notes[0].ID || row.ProviderID != "local" {
		t.Fatalf("usage row = %+v", row)
	}

	list, err := h.app.ListAIThreads(h.ctx, u, notes[0].ID)
	if err != nil || len(list) != 1 || len(list[0].Messages) != 2 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if list[0].Messages[0].Role != core.AIRoleUser || list[0].Messages[1].ID != done.Message.ID {
		t.Fatalf("messages = %+v %+v", list[0].Messages[0], list[0].Messages[1])
	}
	if err := h.app.SetAIMessageApplied(h.ctx, u, th.ID, done.Message.ID, true); err != nil {
		t.Fatal(err)
	}
	if list, _ := h.app.ListAIThreads(h.ctx, u, notes[0].ID); !list[0].Messages[1].Applied {
		t.Fatal("applied flag lost")
	}

	for _, in := range []core.AIThreadMessageInput{{Content: "   "}, {Content: "q", Scope: "galaxy"}, {Content: strings.Repeat("x", maxAIMessageRunes+1)}} {
		if _, err := h.app.StreamAIThreadMessage(h.ctx, u, th.ID, in); !errors.Is(err, core.ErrInvalid) {
			t.Fatalf("input %q/%q = %v", in.Content[:min(len(in.Content), 8)], in.Scope, err)
		}
	}
}

func TestAIThreadStreamFallsBackAndRecordsAttempts(t *testing.T) {
	h, admin := fallbackHarness(t)
	if _, err := h.app.SaveAIRoute(h.ctx, admin, core.RouteScopeInstance, core.RouteChat, []core.AIRouteTarget{
		{ProviderID: "local", Model: "chat"}, {ProviderID: "backup", Model: "backup-chat"},
	}); err != nil {
		t.Fatal(err)
	}
	owner := h.user("Writer", false)
	sp := h.space(owner, "Notes")
	n := indexedNote(h, sp, "Plan", "Launch in October.")
	setStream(h, func(ctx context.Context, _ []core.AIMessage, opts core.AIOptions) (<-chan core.AIChunk, error) {
		if opts.Model == "chat" {
			return nil, &ai.APIError{Provider: "openai", Status: 503}
		}
		ch := make(chan core.AIChunk, 2)
		ch <- core.AIChunk{Delta: "October."}
		ch <- core.AIChunk{Done: true, PromptTokens: 4, CompletionTokens: 1}
		close(ch)
		return ch, nil
	})
	th, err := h.app.CreateAIThread(h.ctx, owner, core.AIThreadInput{NoteID: n.ID, Title: "Launch"})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := h.app.StreamAIThreadMessage(h.ctx, owner, th.ID, core.AIThreadMessageInput{Content: "When is the launch?", Context: chips})
	if err != nil {
		t.Fatal(err)
	}
	_, done, err := drainThread(t, ch)
	if err != nil || done == nil || done.Attempts != 2 || done.Provider != "backup" || done.Route != core.RouteChat {
		t.Fatalf("done = %+v, %v", done, err)
	}
	rows := usageOf(h, owner.UserID())
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	if r := rows[0]; r.Status != core.UsageError || r.Attempts != 1 || r.Task != core.RouteChat || r.Feature != core.FeatureChat || r.ProviderID != "local" {
		t.Fatalf("failed attempt = %+v", r)
	}
	if r := rows[1]; r.Status != core.UsageOK || r.Attempts != 2 || r.Task != core.RouteChat || r.ProviderID != "backup" {
		t.Fatalf("served attempt = %+v", r)
	}
}

func TestAIThreadRequestCarriesHistoryAndScope(t *testing.T) {
	th := &core.AIThread{ID: "t", NoteID: "n1", SpaceID: "s1"}
	var stored []*core.AIThreadMessage
	for i := 0; i < 13; i++ {
		role := core.AIRoleUser
		if i%2 == 1 {
			role = core.AIRoleAssistant
		}
		stored = append(stored, &core.AIThreadMessage{Role: role, Content: fmt.Sprintf("turn %d", i)})
	}
	stored = append(stored, &core.AIThreadMessage{Role: core.AIRoleAssistant, Content: " "})
	history := threadHistory(stored)
	if len(history) != aiThreadHistoryTurns || history[0].Content != "turn 3" || history[9].Content != "turn 12" {
		t.Fatalf("history = %+v", history)
	}

	req := threadRequest(th, core.AIThreadMessageInput{Content: "q"}, "q", history)
	if req.Task != core.TaskAsk || req.Route != core.RouteChat || req.Feature != core.FeatureChat || req.Output != core.AIOutputAnswer ||
		req.NoteID != "n1" || req.SpaceID != "s1" || req.Question != "q" || len(req.History) != 10 {
		t.Fatalf("request = %+v", req)
	}
	if len(req.Context) != 1 || req.Context[0] != (core.AIContextRef{Kind: core.ContextNote, NoteID: "n1"}) {
		t.Fatalf("default context = %+v", req.Context)
	}
	chips := []core.AIContextRef{{Kind: core.ContextSection, NoteID: "n1", Heading: "Findings"}}
	req = threadRequest(th, core.AIThreadMessageInput{Content: "q", Scope: core.AIScopeSpace, Context: chips, ProviderID: "p", Model: "m"}, "q", nil)
	if len(req.Context) != 2 || req.Context[0] != chips[0] || req.Context[1] != (core.AIContextRef{Kind: core.ContextSearch, Query: "q"}) {
		t.Fatalf("space context = %+v", req.Context)
	}
	if req.ProviderID != "p" || req.Model != "m" {
		t.Fatalf("model choice = %+v", req)
	}
}

// TestAIThreadPrivacyDifferential runs random operations by three users on
// each other's threads and notes and compares every answer with a model in
// which only the owner of a thread can see or change it.
func TestAIThreadPrivacyDifferential(t *testing.T) {
	for seed := uint64(1); seed <= 4; seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			h, users, byUser := threadHarness(t)
			rng := rand.New(rand.NewPCG(seed, seed*7919))
			type modelThread struct {
				owner    int
				noteID   string
				messages []string
			}
			type noteRef struct {
				owner int
				note  *core.Note
			}
			var allNotes []noteRef
			for i, list := range byUser {
				for _, n := range list {
					allNotes = append(allNotes, noteRef{i, n})
				}
			}
			model := map[string]*modelThread{}
			ids := []string{"missing-thread"}
			pickThread := func() string { return ids[rng.IntN(len(ids))] }
			created, streamed, refused := 0, 0, 0
			for step := 0; step < 300; step++ {
				ui := rng.IntN(len(users))
				u := users[ui]
				switch op := rng.IntN(6); op {
				case 0:
					own := byUser[ui][rng.IntN(len(byUser[ui]))]
					th, err := h.app.CreateAIThread(h.ctx, u, core.AIThreadInput{NoteID: own.ID})
					if err != nil {
						t.Fatalf("step %d create: %v", step, err)
					}
					model[th.ID] = &modelThread{owner: ui, noteID: own.ID}
					ids = append(ids, th.ID)
					created++
				case 1:
					ref := allNotes[rng.IntN(len(allNotes))]
					list, err := h.app.ListAIThreads(h.ctx, u, ref.note.ID)
					if ref.owner != ui {
						if err == nil || len(list) != 0 {
							t.Fatalf("step %d: user %d listed %d threads of a note they cannot view: %v", step, ui, len(list), err)
						}
						refused++
						continue
					}
					if err != nil {
						t.Fatalf("step %d list: %v", step, err)
					}
					var got, want []string
					for _, th := range list {
						m := model[th.ID]
						if th.UserID != u.UserID() || m == nil || m.owner != ui || m.noteID != ref.note.ID || len(th.Messages) != len(m.messages) {
							t.Fatalf("step %d: user %d listed %+v, model %+v", step, ui, th, m)
						}
						got = append(got, th.ID)
					}
					for id, m := range model {
						if m.owner == ui && m.noteID == ref.note.ID {
							want = append(want, id)
						}
					}
					sort.Strings(got)
					sort.Strings(want)
					if strings.Join(got, ",") != strings.Join(want, ",") {
						t.Fatalf("step %d: user %d sees %v, want %v", step, ui, got, want)
					}
				case 2, 3:
					id := pickThread()
					ch, err := h.app.StreamAIThreadMessage(h.ctx, u, id, core.AIThreadMessageInput{Content: "question", Context: chips})
					m := model[id]
					if m == nil || m.owner != ui {
						if !errors.Is(err, core.ErrNotFound) {
							t.Fatalf("step %d: user %d wrote to thread %s of %+v: %v", step, ui, id, m, err)
						}
						refused++
						continue
					}
					if err != nil {
						t.Fatalf("step %d stream: %v", step, err)
					}
					_, done, err := drainThread(t, ch)
					if err != nil || done == nil || done.Message == nil {
						t.Fatalf("step %d drain: %+v, %v", step, done, err)
					}
					m.messages = append(m.messages, done.UserMessage.ID, done.Message.ID)
					streamed++
				case 4:
					id := pickThread()
					m := model[id]
					msg := "missing-message"
					if m != nil && len(m.messages) > 0 && rng.IntN(4) > 0 {
						msg = m.messages[rng.IntN(len(m.messages))]
					}
					err := h.app.SetAIMessageApplied(h.ctx, u, id, msg, true)
					switch {
					case m == nil || m.owner != ui || msg == "missing-message":
						if !errors.Is(err, core.ErrNotFound) {
							t.Fatalf("step %d: user %d marked %s/%s of %+v: %v", step, ui, id, msg, m, err)
						}
					case err != nil:
						t.Fatalf("step %d applied: %v", step, err)
					}
				case 5:
					id := pickThread()
					err := h.app.DeleteAIThread(h.ctx, u, id)
					m := model[id]
					if m == nil || m.owner != ui {
						if !errors.Is(err, core.ErrNotFound) {
							t.Fatalf("step %d: user %d deleted thread %s of %+v: %v", step, ui, id, m, err)
						}
						if m != nil {
							if _, err := h.db.GetAIThread(h.ctx, id); err != nil {
								t.Fatalf("step %d: a refused delete removed the thread: %v", step, err)
							}
						}
						continue
					}
					if err != nil {
						t.Fatalf("step %d delete: %v", step, err)
					}
					delete(model, id)
				}
			}
			if created == 0 || streamed == 0 || refused == 0 {
				t.Fatalf("the run did not cover every path: created %d, streamed %d, refused %d", created, streamed, refused)
			}
		})
	}
}

func TestAIThreadRetention90Days(t *testing.T) {
	h, users, byUser := threadHarness(t)
	u, other := users[0], users[1]
	notes := byUser[0]
	day := 24 * time.Hour
	old, err := h.app.CreateAIThread(h.ctx, u, core.AIThreadInput{NoteID: notes[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	active, _ := h.app.CreateAIThread(h.ctx, u, core.AIThreadInput{NoteID: notes[0].ID})
	otherOld, err := h.app.CreateAIThread(h.ctx, other, core.AIThreadInput{NoteID: byUser[1][0].ID})
	if err != nil {
		t.Fatal(err)
	}

	h.clock.Advance(60 * day)
	ch, err := h.app.StreamAIThreadMessage(h.ctx, u, active.ID, core.AIThreadMessageInput{Content: "still here?", Context: chips})
	if err != nil {
		t.Fatal(err)
	}
	if _, done, err := drainThread(t, ch); err != nil || done == nil {
		t.Fatalf("stream at day 60 = %v", err)
	}

	h.clock.Advance(29 * day)
	if list, err := h.app.ListAIThreads(h.ctx, u, notes[0].ID); err != nil || len(list) != 2 {
		t.Fatalf("day 89 lists %d threads: %v", len(list), err)
	}

	h.clock.Advance(2 * day)
	if err := h.app.DeleteAIThread(h.ctx, u, old.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("an expired thread is still reachable: %v", err)
	}
	if _, err := h.app.StreamAIThreadMessage(h.ctx, other, otherOld.ID, core.AIThreadMessageInput{Content: "hi", Context: chips}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("another user's expired thread accepted a message: %v", err)
	}
	list, err := h.app.ListAIThreads(h.ctx, u, notes[0].ID)
	if err != nil || len(list) != 1 || list[0].ID != active.ID {
		t.Fatalf("day 91 list = %+v, %v", list, err)
	}
	if n := h.db.threadCount(); n != 2 {
		t.Fatalf("stored threads after the owner's purge = %d, want the active one and the other user's", n)
	}
	if n, err := h.app.PurgeAIThreads(h.ctx); err != nil || n != 1 {
		t.Fatalf("PurgeAIThreads = %d, %v", n, err)
	}
	if n := h.db.threadCount(); n != 1 {
		t.Fatalf("stored threads after the purge = %d", n)
	}

	h.clock.Advance(60 * day)
	if list, _ := h.app.ListAIThreads(h.ctx, u, notes[0].ID); len(list) != 0 {
		t.Fatalf("a thread idle for 120 days is listed: %+v", list)
	}
	if n := h.db.threadCount(); n != 0 {
		t.Fatalf("stored threads after 120 idle days = %d", n)
	}
}

func TestAIThreadNeedsNoteAccess(t *testing.T) {
	h, users, byUser := threadHarness(t)
	notes := byUser[0]
	stranger := h.user("Stranger", false)
	if _, err := h.app.CreateAIThread(h.ctx, stranger, core.AIThreadInput{NoteID: notes[0].ID}); err == nil {
		t.Fatal("a user without access started a thread")
	}
	if _, err := h.app.ListAIThreads(h.ctx, stranger, notes[0].ID); err == nil {
		t.Fatal("a user without access listed threads")
	}
	if _, err := h.app.ListAIThreads(h.ctx, &core.Actor{}, notes[0].ID); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("anonymous list = %v", err)
	}
	if _, err := h.app.CreateAIThread(h.ctx, users[0], core.AIThreadInput{}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("thread without a note = %v", err)
	}
}
