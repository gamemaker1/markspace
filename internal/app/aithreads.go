package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gamemaker1/markspace/internal/core"
)

// Limits of AI conversations.
const (
	// maxAIThreadTitleRunes caps a thread title.
	maxAIThreadTitleRunes = 120
	// maxAIMessageRunes caps one user turn.
	maxAIMessageRunes = 8000
	// aiThreadHistoryTurns caps the earlier turns sent with a new question.
	aiThreadHistoryTurns = 10
	// aiThreadWriteTimeout bounds storing an answer after the client left.
	aiThreadWriteTimeout = 5 * time.Second
)

var _ core.AIThreadService = (*App)(nil)

// threadCutoff returns the time before which a thread counts as expired.
func (a *App) threadCutoff() time.Time {
	return a.now().Add(-core.AIThreadRetention)
}

// ownThread loads a thread of the actor. A missing thread, an expired one
// and a thread of another user are all core.ErrNotFound, so a caller learns
// nothing about threads that are not theirs.
func (a *App) ownThread(ctx context.Context, actor *core.Actor, id string) (*core.AIThread, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	notFound := fmt.Errorf("ai thread %s: %w", id, core.ErrNotFound)
	if strings.TrimSpace(id) == "" {
		return nil, notFound
	}
	t, err := a.db.GetAIThread(ctx, id)
	if errors.Is(err, core.ErrNotFound) {
		return nil, notFound
	}
	if err != nil {
		return nil, fmt.Errorf("load ai thread: %w", err)
	}
	if t.UserID != actor.UserID() || t.UpdatedAt.Before(a.threadCutoff()) {
		return nil, notFound
	}
	return t, nil
}

// purgeOwnAIThreads deletes the actor's expired threads. A failure is logged,
// because reads already hide expired threads.
func (a *App) purgeOwnAIThreads(ctx context.Context, userID string) {
	if n, err := a.db.DeleteAIThreadsBefore(ctx, userID, a.threadCutoff()); err != nil {
		a.log.Warn("ai thread purge failed", "userId", userID, "err", err)
	} else if n > 0 {
		a.log.Info("ai threads expired", "userId", userID, "deleted", n)
	}
}

// PurgeAIThreads deletes every thread without activity for
// core.AIThreadRetention and returns how many it deleted. The daily
// retention job calls it. Listing a note's threads also purges the actor's
// own expired threads, and every read hides them.
func (a *App) PurgeAIThreads(ctx context.Context) (int, error) {
	n, err := a.db.DeleteAIThreadsBefore(ctx, "", a.threadCutoff())
	if err != nil {
		return 0, fmt.Errorf("purge ai threads: %w", err)
	}
	return n, nil
}

// ListAIThreads returns the actor's conversations about a note, newest
// first, each with its messages oldest first.
func (a *App) ListAIThreads(ctx context.Context, actor *core.Actor, noteID string) ([]*core.AIThread, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	if strings.TrimSpace(noteID) == "" {
		return nil, core.Invalid("noteId", "a note is required")
	}
	if _, _, err := a.requireNoteAny(ctx, actor, noteID, core.AccessView); err != nil {
		return nil, err
	}
	a.purgeOwnAIThreads(ctx, actor.UserID())
	threads, err := a.db.ListAIThreads(ctx, actor.UserID(), noteID, a.threadCutoff())
	if err != nil {
		return nil, fmt.Errorf("list ai threads: %w", err)
	}
	out := make([]*core.AIThread, 0, len(threads))
	for _, t := range threads {
		if t.UserID != actor.UserID() {
			continue
		}
		msgs, err := a.db.ListAIMessages(ctx, t.ID)
		if err != nil {
			return nil, fmt.Errorf("list ai messages: %w", err)
		}
		t.Messages = msgs
		if t.Messages == nil {
			t.Messages = []*core.AIThreadMessage{}
		}
		out = append(out, t)
	}
	return out, nil
}

// CreateAIThread starts a conversation about a note the actor may view. An
// empty title takes the note title.
func (a *App) CreateAIThread(ctx context.Context, actor *core.Actor, in core.AIThreadInput) (*core.AIThread, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.NoteID) == "" {
		return nil, core.Invalid("noteId", "a note is required")
	}
	note, _, err := a.requireNoteAny(ctx, actor, in.NoteID, core.AccessView)
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = note.Title
	}
	now := a.now()
	t := &core.AIThread{
		ID: core.NewID(), UserID: actor.UserID(), NoteID: note.ID, SpaceID: note.SpaceID,
		Title: truncateRunes(title, maxAIThreadTitleRunes), CreatedAt: now, UpdatedAt: now,
		Messages: []*core.AIThreadMessage{},
	}
	if err := a.db.CreateAIThread(ctx, t); err != nil {
		return nil, fmt.Errorf("create ai thread: %w", err)
	}
	return t, nil
}

// threadHistory turns the last stored turns into prompt history.
func threadHistory(msgs []*core.AIThreadMessage) []core.AIMessage {
	var out []core.AIMessage
	for _, m := range msgs {
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		out = append(out, core.AIMessage{Role: m.Role, Content: m.Content})
	}
	if len(out) > aiThreadHistoryTurns {
		out = out[len(out)-aiThreadHistoryTurns:]
	}
	return out
}

// threadRequest builds the AI request of a turn: an ask on the chat route
// with the thread's note, the composer chips, the earlier turns and, for the
// space scope, a search of the space.
func threadRequest(t *core.AIThread, in core.AIThreadMessageInput, content string, history []core.AIMessage) core.AIRequest {
	refs := append([]core.AIContextRef(nil), in.Context...)
	if len(refs) == 0 && t.NoteID != "" {
		refs = append(refs, core.AIContextRef{Kind: core.ContextNote, NoteID: t.NoteID})
	}
	if in.Scope == core.AIScopeSpace {
		refs = append(refs, core.AIContextRef{Kind: core.ContextSearch, Query: content})
	}
	return core.AIRequest{
		Task: core.TaskAsk, NoteID: t.NoteID, SpaceID: t.SpaceID, Question: content, History: history,
		Context: refs, Feature: core.FeatureChat, Route: core.RouteChat, Output: core.AIOutputAnswer,
		ProviderID: in.ProviderID, Model: in.Model,
	}
}

// StreamAIThreadMessage stores the user turn once the answer starts, streams
// the answer through StreamAI, and stores the answer with its sources when the
// stream ends with Done. A stream that fails keeps the user turn and stores no
// answer.
func (a *App) StreamAIThreadMessage(ctx context.Context, actor *core.Actor, threadID string, in core.AIThreadMessageInput) (<-chan core.AIThreadChunk, error) {
	t, err := a.ownThread(ctx, actor, threadID)
	if err != nil {
		return nil, err
	}
	content := strings.TrimSpace(in.Content)
	switch {
	case content == "":
		return nil, core.Invalid("content", "a message is required")
	case utf8.RuneCountInString(content) > maxAIMessageRunes:
		return nil, core.Invalid("content", fmt.Sprintf("a message is at most %d characters", maxAIMessageRunes))
	case len(in.Context) > maxContextRefs:
		return nil, core.Invalid("context", fmt.Sprintf("a message carries at most %d context references", maxContextRefs))
	}
	switch in.Scope {
	case "", core.AIScopeNote, core.AIScopeSpace:
	default:
		return nil, core.Invalid("scope", "scope must be note or space")
	}
	earlier, err := a.db.ListAIMessages(ctx, t.ID)
	if err != nil {
		return nil, fmt.Errorf("list ai messages: %w", err)
	}
	req := threadRequest(t, in, content, threadHistory(earlier))
	chunks, err := a.StreamAI(ctx, actor, req)
	if err != nil {
		return nil, err
	}
	user := &core.AIThreadMessage{
		ID: core.NewID(), ThreadID: t.ID, Role: core.AIRoleUser, Content: content,
		Context: append([]core.AIContextRef{}, in.Context...), Sources: []core.AISource{}, CreatedAt: a.now(),
	}
	if err := a.db.AddAIMessage(ctx, user); err != nil {
		go func() {
			for range chunks {
			}
		}()
		return nil, fmt.Errorf("store ai message: %w", err)
	}

	out := make(chan core.AIThreadChunk, 1)
	go func() {
		defer close(out)
		var text strings.Builder
		for chunk := range chunks {
			tc := core.AIThreadChunk{AIChunk: chunk}
			text.WriteString(chunk.Delta)
			if chunk.Done {
				answer := &core.AIThreadMessage{
					ID: core.NewID(), ThreadID: t.ID, Role: core.AIRoleAssistant, Content: text.String(),
					Context: []core.AIContextRef{}, Sources: append([]core.AISource{}, chunk.Sources...), CreatedAt: a.now(),
				}
				wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), aiThreadWriteTimeout)
				if err := a.db.AddAIMessage(wctx, answer); err != nil {
					a.log.Warn("store ai answer failed", "threadId", t.ID, "err", err)
					tc.Done, tc.Err = false, fmt.Errorf("the answer could not be saved: %w", err)
				} else {
					tc.Route, tc.UserMessage, tc.Message = core.RouteChat, user, answer
				}
				cancel()
			}
			select {
			case out <- tc:
			case <-ctx.Done():
				go func() {
					for range chunks {
					}
				}()
				return
			}
			if chunk.Done || chunk.Err != nil {
				// StreamAI records the usage row before it closes its channel.
				// Waiting for that close means a caller who reads this stream to
				// its end finds the row stored.
				for range chunks {
				}
				return
			}
		}
	}()
	return out, nil
}

// SetAIMessageApplied marks an answer of the actor's thread as applied to the
// note.
func (a *App) SetAIMessageApplied(ctx context.Context, actor *core.Actor, threadID, messageID string, applied bool) error {
	t, err := a.ownThread(ctx, actor, threadID)
	if err != nil {
		return err
	}
	if err := a.db.SetAIMessageApplied(ctx, t.ID, messageID, applied); err != nil {
		if errors.Is(err, core.ErrNotFound) {
			return fmt.Errorf("ai message %s: %w", messageID, core.ErrNotFound)
		}
		return fmt.Errorf("set ai message applied: %w", err)
	}
	return nil
}

// DeleteAIThread deletes one of the actor's conversations.
func (a *App) DeleteAIThread(ctx context.Context, actor *core.Actor, threadID string) error {
	t, err := a.ownThread(ctx, actor, threadID)
	if err != nil {
		return err
	}
	if err := a.db.DeleteAIThread(ctx, t.ID); err != nil && !errors.Is(err, core.ErrNotFound) {
		return fmt.Errorf("delete ai thread: %w", err)
	}
	return nil
}
