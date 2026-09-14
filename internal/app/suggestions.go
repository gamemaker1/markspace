package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/module"
	"github.com/gamemaker1/markspace/internal/ports"
)

// Suggestion limits of the application layer.
const (
	// suggestionRetries bounds compare-and-swap retries of one suggestion.
	suggestionRetries = 3
	// defaultInboxLimit and maxInboxLimit bound an inbox listing.
	defaultInboxLimit = 100
	maxInboxLimit     = 500
)

// skipRemapKey marks a context whose note update must not remap one
// suggestion, because the caller writes that suggestion itself.
type skipRemapKey struct{}

func withoutRemapOf(ctx context.Context, suggestionID string) context.Context {
	return context.WithValue(ctx, skipRemapKey{}, suggestionID)
}

func (a *App) suggestionStore() (ports.SuggestionDB, error) {
	if a.suggestions == nil {
		return nil, fmt.Errorf("suggestions are not available: %w", core.ErrUnsupported)
	}
	return a.suggestions, nil
}

// coreSuggestion converts a stored suggestion to the core type.
func coreSuggestion(s *ports.StoredSuggestion) *core.Suggestion {
	if s == nil {
		return nil
	}
	out := &core.Suggestion{
		ID: s.ID, SpaceID: s.SpaceID, NoteID: s.NoteID, AuthorKind: s.AuthorKind, AuthorID: s.AuthorID,
		Title: s.Title, Rationale: s.Rationale, BaseUpdatedAt: s.BaseUpdatedAt, Status: s.Status,
		CreatedAt: s.CreatedAt, ResolvedBy: s.ResolvedBy, UpdatedAt: s.UpdatedAt,
		Edits: make([]core.SuggestedEdit, len(s.Edits)),
	}
	if s.ResolvedAt != nil {
		t := *s.ResolvedAt
		out.ResolvedAt = &t
	}
	for i, e := range s.Edits {
		out.Edits[i] = core.SuggestedEdit{ID: e.ID, Anchor: e.Anchor, Text: e.Text, Status: e.Status}
	}
	return out
}

func coreSuggestions(list []*ports.StoredSuggestion) []*core.Suggestion {
	out := make([]*core.Suggestion, 0, len(list))
	for _, s := range list {
		out = append(out, coreSuggestion(s))
	}
	return out
}

// publishSuggestion sends a suggestion event about its note.
func (a *App) publishSuggestion(typ string, s *ports.StoredSuggestion, actorID string, extra map[string]any) {
	data := map[string]any{"suggestionId": s.ID}
	for k, v := range extra {
		data[k] = v
	}
	raw, _ := json.Marshal(data)
	ev := core.Event{
		Type: typ, SpaceID: s.SpaceID, NoteID: s.NoteID, ItemID: s.NoteID, Kind: core.KindNote,
		ActorID: actorID, Data: raw, At: a.now().UnixMilli(),
	}
	a.emit(ev)
}

// suggestionAuthor derives the author of a new suggestion from the actor. A
// plugin token writes as the plugin. A person, or a plugin acting for one,
// writes as that person.
func suggestionAuthor(actor *core.Actor, kind string) (string, string) {
	if actor.Plugin != nil && actor.Via == "" {
		return module.AuthorPlugin, actor.Plugin.ID
	}
	if kind == module.AuthorAI {
		return module.AuthorAI, actor.UserID()
	}
	return module.AuthorUser, actor.UserID()
}

// CreateSuggestion stores a new suggestion. It needs comment access on the
// note, and every edit must resolve in the current body without overlap.
func (a *App) CreateSuggestion(ctx context.Context, actor *core.Actor, in core.SuggestionInput) (*core.Suggestion, error) {
	store, err := a.suggestionStore()
	if err != nil {
		return nil, err
	}
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	if in.AuthorKind != "" && in.AuthorKind != module.AuthorAI {
		return nil, core.Invalid("authorKind", "unknown author kind")
	}
	note, _, err := a.requireNote(ctx, actor, in.NoteID, core.AccessComment)
	if err != nil {
		return nil, err
	}
	space, err := a.space(ctx, note.SpaceID)
	if err != nil {
		return nil, err
	}
	if err := a.loadBody(ctx, space, note); err != nil {
		return nil, err
	}
	if len(in.Edits) > module.MaxSuggestedEdits {
		return nil, core.Invalid("edits", fmt.Sprintf("at most %d edits are allowed", module.MaxSuggestedEdits))
	}
	kind, author := suggestionAuthor(actor, in.AuthorKind)
	now := a.now()
	s := module.Suggestion{
		ID: core.NewID(), SpaceID: note.SpaceID, NoteID: note.ID, AuthorKind: kind, AuthorID: author,
		Title: strings.TrimSpace(in.Title), Rationale: strings.TrimSpace(in.Rationale),
		BaseUpdatedAt: note.UpdatedAt, CreatedAt: now,
	}
	for i, e := range in.Edits {
		id := strings.TrimSpace(e.ID)
		if id == "" {
			id = fmt.Sprintf("e%d", i+1)
		}
		s.Edits = append(s.Edits, module.SuggestedEdit{ID: id, Anchor: e.Anchor, Text: e.Text})
	}
	if err := module.ValidateNewSuggestion(note.Content, &s); err != nil {
		return nil, err
	}
	stored := &ports.StoredSuggestion{Suggestion: s}
	if err := store.CreateSuggestion(ctx, stored, now); err != nil {
		return nil, fmt.Errorf("create suggestion: %w", err)
	}
	a.log.Info("suggestion created", "suggestionId", stored.ID, "noteId", note.ID, "authorKind", kind, "edits", len(s.Edits))
	a.publishSuggestion("suggestion.created", stored, actor.UserID(), nil)
	return coreSuggestion(stored), nil
}

// ListNoteSuggestions lists the suggestions of a note the actor may view.
func (a *App) ListNoteSuggestions(ctx context.Context, actor *core.Actor, noteID string, statuses []string) ([]*core.Suggestion, error) {
	store, err := a.suggestionStore()
	if err != nil {
		return nil, err
	}
	if _, _, err := a.requireNoteAny(ctx, actor, noteID, core.AccessView); err != nil {
		return nil, err
	}
	list, err := store.ListNoteSuggestions(ctx, noteID, statuses)
	if err != nil {
		return nil, err
	}
	return coreSuggestions(list), nil
}

// SuggestionInbox lists suggestions on notes the actor may edit, newest
// first, across the spaces the actor reaches.
func (a *App) SuggestionInbox(ctx context.Context, actor *core.Actor, q core.SuggestionQuery) ([]*core.Suggestion, error) {
	store, err := a.suggestionStore()
	if err != nil {
		return nil, err
	}
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultInboxLimit
	}
	limit = min(limit, maxInboxLimit)
	var statuses []string
	switch status := strings.TrimSpace(q.Status); status {
	case "":
		statuses = []string{module.SuggestionOpen}
	case "all":
	default:
		statuses = []string{status}
	}
	spaces, err := a.accessibleSpaceIDs(ctx, actor)
	if err != nil {
		return nil, err
	}
	if q.SpaceID != "" {
		if !slices.Contains(spaces, q.SpaceID) {
			return []*core.Suggestion{}, nil
		}
		spaces = []string{q.SpaceID}
	}
	if len(spaces) == 0 {
		return []*core.Suggestion{}, nil
	}
	filter := ports.SuggestionFilter{
		SpaceIDs: spaces, NoteID: strings.TrimSpace(q.NoteID), AuthorID: strings.TrimSpace(q.AuthorID),
		Statuses: statuses, Limit: maxInboxLimit * 4,
	}
	// The actor's own suggestions need view access on their notes. The inbox
	// of suggestions to act on needs edit access.
	need := core.AccessEdit
	if filter.AuthorID == core.SuggestionAuthorMe {
		filter.AuthorKind, filter.AuthorID = suggestionAuthor(actor, "")
		if filter.AuthorKind == module.AuthorUser {
			filter.AuthorKind = ""
		}
		need = core.AccessView
	}
	rows, err := store.ListSuggestions(ctx, filter)
	if err != nil {
		return nil, err
	}
	cache := a.grants(actor)
	out := []*core.Suggestion{}
	for _, s := range rows {
		note, err := a.db.GetNote(ctx, s.NoteID)
		if err != nil || note == nil || note.Deleted {
			continue
		}
		g, err := cache.note(ctx, note)
		if err != nil {
			return nil, err
		}
		if !g.Access.AtLeast(need) {
			continue
		}
		if need == core.AccessView && filter.AuthorKind == "" && s.AuthorKind == module.AuthorPlugin {
			continue
		}
		out = append(out, coreSuggestion(s))
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// loadSuggestion reads a suggestion and checks that the actor reaches its
// note with want access. A suggestion on a hidden note reads as not found.
func (a *App) loadSuggestion(ctx context.Context, actor *core.Actor, id string, want core.Access) (*ports.StoredSuggestion, *core.Note, error) {
	store, err := a.suggestionStore()
	if err != nil {
		return nil, nil, err
	}
	if id == "" {
		return nil, nil, core.Invalid("id", "suggestion id is empty")
	}
	s, err := store.GetSuggestion(ctx, id)
	if errors.Is(err, core.ErrNotFound) {
		return nil, nil, fmt.Errorf("suggestion %s: %w", id, denied(actor))
	}
	if err != nil {
		return nil, nil, fmt.Errorf("load suggestion: %w", err)
	}
	note, _, err := a.requireNoteAny(ctx, actor, s.NoteID, core.AccessView)
	if err != nil {
		return nil, nil, fmt.Errorf("suggestion %s: %w", id, err)
	}
	if want > core.AccessView {
		if _, _, err := a.requireNoteAny(ctx, actor, s.NoteID, want); err != nil {
			return nil, nil, err
		}
	}
	return s, note, nil
}

// GetSuggestion returns one suggestion the actor may view.
func (a *App) GetSuggestion(ctx context.Context, actor *core.Actor, id string) (*core.Suggestion, error) {
	s, _, err := a.loadSuggestion(ctx, actor, id, core.AccessView)
	if err != nil {
		return nil, err
	}
	return coreSuggestion(s), nil
}

func resolvedStatus(status string) bool {
	return status == module.SuggestionAccepted || status == module.SuggestionDiscarded || status == module.SuggestionPartial
}

// AcceptSuggestion applies edits through UpdateNote with the note's current
// updatedAt and a forced version, then stores the suggestion with a
// compare-and-swap. A note that changed between the read and the write is
// read again, up to three times.
func (a *App) AcceptSuggestion(ctx context.Context, actor *core.Actor, id string, editIDs []string) (*core.SuggestionResult, error) {
	s, note, err := a.loadSuggestion(ctx, actor, id, core.AccessEdit)
	if err != nil {
		return nil, err
	}
	if note.Deleted {
		return nil, fmt.Errorf("note %s is in the trash: %w", note.ID, core.ErrConflict)
	}
	if resolvedStatus(s.Status) {
		return nil, fmt.Errorf("suggestion %s is %s: %w", id, s.Status, core.ErrConflict)
	}
	if len(editIDs) == 0 {
		editIDs = nil
	}
	// A named edit that is already accepted or discarded is a conflict, not
	// a missing edit.
	for _, id := range editIDs {
		i := slices.IndexFunc(s.Edits, func(e module.SuggestedEdit) bool { return e.ID == id })
		if i >= 0 && s.Edits[i].Status != module.EditOpen && s.Edits[i].Status != module.EditOrphaned {
			return nil, fmt.Errorf("edit %q is %s: %w", id, s.Edits[i].Status, core.ErrConflict)
		}
	}
	space, err := a.space(ctx, note.SpaceID)
	if err != nil {
		return nil, err
	}
	for attempt := 0; ; attempt++ {
		fresh, err := a.db.GetNote(ctx, note.ID)
		if err != nil {
			return nil, err
		}
		if err := a.loadBody(ctx, space, fresh); err != nil {
			return nil, err
		}
		body, base := fresh.Content, fresh.UpdatedAt
		work := s.Suggestion
		work.Edits = slices.Clone(s.Edits)
		newBody, err := module.AcceptEdits(body, &work, editIDs, actor.UserID(), a.now())
		if err != nil {
			return nil, err
		}
		var updated *core.Note
		if newBody != body {
			updated, err = a.UpdateNote(withoutRemapOf(ctx, s.ID), actor, note.ID, core.NoteUpdate{
				Content: &newBody, BaseUpdatedAt: &base, Snapshot: true,
			})
			if errors.Is(err, core.ErrConflict) && attempt < suggestionRetries-1 {
				continue
			}
			if err != nil {
				return nil, err
			}
		}
		accepted := map[string]bool{}
		for i, e := range work.Edits {
			if e.Status == module.EditAccepted && s.Edits[i].Status != module.EditAccepted {
				accepted[e.ID] = true
			}
		}
		stored, err := a.storeSuggestion(ctx, s, work, newBody, func(cur *module.Suggestion) {
			for i := range cur.Edits {
				if accepted[cur.Edits[i].ID] {
					cur.Edits[i].Status = module.EditAccepted
				}
			}
		}, actor.UserID())
		if err != nil {
			return nil, err
		}
		a.log.Info("suggestion accepted", "suggestionId", id, "noteId", note.ID, "status", stored.Status, "edits", len(accepted), "by", actor.UserID())
		a.publishSuggestion("suggestion.accepted", stored, actor.UserID(), map[string]any{"status": stored.Status})
		return &core.SuggestionResult{Suggestion: coreSuggestion(stored), Note: updated}, nil
	}
}

// storeSuggestion writes work over s with a compare-and-swap. On a conflict
// it reads the suggestion again, lets reapply set the caller's edit states on
// the fresh copy, marks orphans against body and retries.
func (a *App) storeSuggestion(ctx context.Context, s *ports.StoredSuggestion, work module.Suggestion, body string,
	reapply func(*module.Suggestion), actorID string) (*ports.StoredSuggestion, error) {
	store, err := a.suggestionStore()
	if err != nil {
		return nil, err
	}
	next := &ports.StoredSuggestion{Suggestion: work, Version: s.Version, UpdatedAt: s.UpdatedAt}
	for attempt := 0; ; attempt++ {
		err := store.UpdateSuggestion(ctx, next, a.now())
		if err == nil {
			return next, nil
		}
		if !errors.Is(err, core.ErrConflict) || attempt >= suggestionRetries-1 {
			return nil, fmt.Errorf("store suggestion: %w", err)
		}
		fresh, gerr := store.GetSuggestion(ctx, s.ID)
		if gerr != nil {
			return nil, fmt.Errorf("reload suggestion: %w", gerr)
		}
		reapply(&fresh.Suggestion)
		module.MarkOrphans(body, &fresh.Suggestion, a.now())
		fresh.UpdateStatus(actorID, a.now())
		next = fresh
	}
}

// DiscardSuggestion marks edits discarded. It needs edit access on the note.
func (a *App) DiscardSuggestion(ctx context.Context, actor *core.Actor, id string, editIDs []string) (*core.Suggestion, error) {
	s, note, err := a.loadSuggestion(ctx, actor, id, core.AccessEdit)
	if err != nil {
		return nil, err
	}
	if resolvedStatus(s.Status) {
		return nil, fmt.Errorf("suggestion %s is %s: %w", id, s.Status, core.ErrConflict)
	}
	if len(editIDs) == 0 {
		editIDs = nil
	}
	work := s.Suggestion
	work.Edits = slices.Clone(s.Edits)
	if err := module.DiscardEdits(&work, editIDs, actor.UserID(), a.now()); err != nil {
		return nil, err
	}
	discarded := map[string]bool{}
	for i, e := range work.Edits {
		if e.Status == module.EditDiscarded && s.Edits[i].Status != module.EditDiscarded {
			discarded[e.ID] = true
		}
	}
	body := ""
	if space, err := a.space(ctx, note.SpaceID); err == nil {
		if fresh, err := a.db.GetNote(ctx, note.ID); err == nil && a.noteBody(ctx, space, fresh) == nil {
			body = fresh.Content
		}
	}
	stored, err := a.storeSuggestion(ctx, s, work, body, func(cur *module.Suggestion) {
		for i := range cur.Edits {
			if discarded[cur.Edits[i].ID] {
				cur.Edits[i].Status = module.EditDiscarded
			}
		}
	}, actor.UserID())
	if err != nil {
		return nil, err
	}
	a.publishSuggestion("suggestion.discarded", stored, actor.UserID(), map[string]any{"status": stored.Status})
	return coreSuggestion(stored), nil
}

// WithdrawSuggestion deletes a suggestion for its author. A plugin token
// withdraws the plugin's suggestions. A person, or a plugin acting for one,
// withdraws that person's suggestions.
func (a *App) WithdrawSuggestion(ctx context.Context, actor *core.Actor, id string) error {
	s, _, err := a.loadSuggestion(ctx, actor, id, core.AccessView)
	if err != nil {
		return err
	}
	author := false
	if actor.Plugin != nil && actor.Via == "" {
		author = s.AuthorKind == module.AuthorPlugin && s.AuthorID == actor.Plugin.ID
	} else {
		author = s.AuthorKind != module.AuthorPlugin && s.AuthorID == actor.UserID()
	}
	if !author {
		return fmt.Errorf("only the author may withdraw suggestion %s: %w", id, core.ErrForbidden)
	}
	if err := a.suggestions.DeleteSuggestion(ctx, id); err != nil {
		return fmt.Errorf("delete suggestion: %w", err)
	}
	a.publishSuggestion("suggestion.discarded", s, actor.UserID(), map[string]any{"withdrawn": true})
	return nil
}

// remapSuggestions moves the anchors of a note's open and stale suggestions
// through a body change and marks orphans, in one batch per note. Each write
// is a compare-and-swap. On a conflict the suggestion is read again and only
// its orphans are marked, because the concurrent writer saw the new body.
// Failures are logged and never fail the edit.
func (a *App) remapSuggestions(ctx context.Context, noteID, oldBody, newBody string) {
	if a.suggestions == nil || oldBody == newBody {
		return
	}
	skip, _ := ctx.Value(skipRemapKey{}).(string)
	list, err := a.suggestions.ListNoteSuggestions(ctx, noteID, []string{module.SuggestionOpen, module.SuggestionStale})
	if err != nil {
		a.log.Warn("list suggestions for remap failed", "noteId", noteID, "err", err)
		return
	}
	for _, s := range list {
		if s.ID == skip {
			continue
		}
		before := s.Status
		changed := module.RemapSuggestion(oldBody, newBody, &s.Suggestion)
		if module.MarkOrphans(newBody, &s.Suggestion, a.now()) {
			changed = true
		}
		if !changed && s.Status == before {
			continue
		}
		for attempt := 0; attempt < suggestionRetries; attempt++ {
			err := a.suggestions.UpdateSuggestion(ctx, s, a.now())
			if err == nil {
				break
			}
			if !errors.Is(err, core.ErrConflict) {
				a.log.Warn("store remapped suggestion failed", "suggestionId", s.ID, "err", err)
				break
			}
			fresh, gerr := a.suggestions.GetSuggestion(ctx, s.ID)
			if gerr != nil || resolvedStatus(fresh.Status) {
				break
			}
			module.MarkOrphans(newBody, &fresh.Suggestion, a.now())
			s = fresh
		}
	}
}

// deleteNoteSuggestions removes the suggestions of deleted notes.
func (a *App) deleteNoteSuggestions(ctx context.Context, noteIDs ...string) {
	if a.suggestions == nil {
		return
	}
	for _, id := range noteIDs {
		if _, err := a.suggestions.DeleteNoteSuggestions(ctx, id); err != nil {
			a.log.Warn("delete suggestions of deleted note failed", "noteId", id, "err", err)
		}
	}
}

// sweepSuggestions removes the suggestions of a space whose note row is gone,
// after a folder delete removed notes in bulk.
func (a *App) sweepSuggestions(ctx context.Context, spaceID string) {
	if a.suggestions == nil {
		return
	}
	list, err := a.suggestions.ListSuggestions(ctx, ports.SuggestionFilter{SpaceIDs: []string{spaceID}})
	if err != nil {
		a.log.Warn("list suggestions for sweep failed", "spaceId", spaceID, "err", err)
		return
	}
	gone := map[string]bool{}
	for _, s := range list {
		if _, seen := gone[s.NoteID]; seen {
			continue
		}
		_, err := a.db.GetNote(ctx, s.NoteID)
		gone[s.NoteID] = errors.Is(err, core.ErrNotFound)
	}
	for id, g := range gone {
		if g {
			a.deleteNoteSuggestions(ctx, id)
		}
	}
}
