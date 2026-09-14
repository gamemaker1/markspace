package index

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/module"
)

var suggestionBase = time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC)

func newSuggestion(spaceID, noteID, authorKind, authorID string) *StoredSuggestion {
	return &StoredSuggestion{Suggestion: module.Suggestion{
		SpaceID: spaceID, NoteID: noteID, AuthorKind: authorKind, AuthorID: authorID,
		Title: "Add See also links", Rationale: "Related notes",
		Edits: []module.SuggestedEdit{
			{ID: "e1", Anchor: core.CommentAnchor{Exact: "old", Prefix: "an ", Suffix: " word", Start: 3, End: 6}, Text: "new", Status: module.EditOpen},
			{ID: "e2", Anchor: core.CommentAnchor{Prefix: "end", Start: 20, End: 20}, Text: "\n\nSee also", Status: module.EditOpen},
		},
		BaseUpdatedAt: suggestionBase.Add(-time.Hour),
	}}
}

func mustSuggestion(t *testing.T, db *DB, s *StoredSuggestion, at time.Time) *StoredSuggestion {
	t.Helper()
	if err := db.CreateSuggestion(context.Background(), s, at); err != nil {
		t.Fatalf("CreateSuggestion: %v", err)
	}
	return s
}

func suggestionIDs(list []*StoredSuggestion) []string {
	var ids []string
	for _, s := range list {
		ids = append(ids, s.ID)
	}
	return ids
}

func TestSuggestionRoundTripAndLists(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	spaceA, spaceB := core.NewID(), core.NewID()
	noteA, noteB := core.NewID(), core.NewID()

	s1 := mustSuggestion(t, db, newSuggestion(spaceA, noteA, module.AuthorPlugin, "bookmark-wiki"), suggestionBase)
	s2 := mustSuggestion(t, db, newSuggestion(spaceA, noteA, module.AuthorAI, "u1"), suggestionBase.Add(time.Second))
	s3 := mustSuggestion(t, db, newSuggestion(spaceB, noteB, module.AuthorUser, "u1"), suggestionBase.Add(2*time.Second))

	got, err := db.GetSuggestion(ctx, s1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || got.Status != module.SuggestionOpen || !got.UpdatedAt.Equal(suggestionBase) ||
		!got.CreatedAt.Equal(suggestionBase) || !got.BaseUpdatedAt.Equal(s1.BaseUpdatedAt) || got.ResolvedAt != nil {
		t.Fatalf("stored fields differ: %+v", got)
	}
	if !slices.Equal(got.Edits, s1.Edits) || got.Rationale != "Related notes" || got.AuthorID != "bookmark-wiki" {
		t.Fatalf("stored edits or author differ: %+v", got)
	}
	_, err = db.GetSuggestion(ctx, core.NewID())
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unknown suggestion: %v", err)
	}

	s2.Status = module.SuggestionDiscarded
	resolved := suggestionBase.Add(time.Minute)
	s2.ResolvedBy, s2.ResolvedAt = "u2", &resolved
	if err := db.UpdateSuggestion(ctx, s2, resolved); err != nil {
		t.Fatal(err)
	}

	check := func(name string, list []*StoredSuggestion, err error, want ...*StoredSuggestion) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if g, w := suggestionIDs(list), suggestionIDs(want); !slices.Equal(g, w) {
			t.Fatalf("%s: got %v, want %v", name, g, w)
		}
	}
	list, err := db.ListNoteSuggestions(ctx, noteA, nil)
	check("note, all statuses", list, err, s1, s2)
	list, err = db.ListNoteSuggestions(ctx, noteA, []string{module.SuggestionOpen})
	check("note, open", list, err, s1)
	list, err = db.ListSuggestions(ctx, SuggestionFilter{})
	check("inbox, all", list, err, s3, s2, s1)
	list, err = db.ListSuggestions(ctx, SuggestionFilter{SpaceIDs: []string{spaceA}})
	check("inbox, space", list, err, s2, s1)
	list, err = db.ListSuggestions(ctx, SuggestionFilter{AuthorID: "u1"})
	check("inbox, author id", list, err, s3, s2)
	list, err = db.ListSuggestions(ctx, SuggestionFilter{AuthorKind: module.AuthorAI, AuthorID: "u1"})
	check("inbox, author kind and id", list, err, s2)
	list, err = db.ListSuggestions(ctx, SuggestionFilter{Statuses: []string{module.SuggestionOpen}, SpaceIDs: []string{spaceA, spaceB}})
	check("inbox, open in two spaces", list, err, s3, s1)
	list, err = db.ListSuggestions(ctx, SuggestionFilter{Limit: 2})
	check("inbox, first page", list, err, s3, s2)
	list, err = db.ListSuggestions(ctx, SuggestionFilter{Limit: 2, BeforeCreatedAt: s2.CreatedAt, BeforeID: s2.ID})
	check("inbox, second page", list, err, s1)
	list, err = db.ListSuggestions(ctx, SuggestionFilter{NoteID: noteB})
	check("inbox, note", list, err, s3)

	got, err = db.GetSuggestion(ctx, s2.ID)
	if err != nil || got.ResolvedBy != "u2" || got.ResolvedAt == nil || !got.ResolvedAt.Equal(resolved) {
		t.Fatalf("resolution not stored: %+v %v", got, err)
	}
	if _, err := db.ListSuggestions(ctx, SuggestionFilter{Statuses: []string{"closed"}}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("unknown status filter: %v", err)
	}
	if err := db.CreateSuggestion(ctx, newSuggestion(spaceA, noteA, "robot", "x"), suggestionBase); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("unknown author kind: %v", err)
	}
}

func TestSuggestionCompareAndSwap(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	tests := []struct {
		name        string
		mutate      func(t *testing.T, stored *StoredSuggestion) *StoredSuggestion
		wantErr     error
		wantVersion int64
	}{
		{
			name:        "current version",
			mutate:      func(t *testing.T, s *StoredSuggestion) *StoredSuggestion { return s },
			wantVersion: 2,
		},
		{
			name: "stale version",
			mutate: func(t *testing.T, s *StoredSuggestion) *StoredSuggestion {
				first := *s
				first.Edits = slices.Clone(s.Edits)
				if err := db.UpdateSuggestion(ctx, &first, suggestionBase.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
				return s
			},
			wantErr:     core.ErrConflict,
			wantVersion: 2,
		},
		{
			name: "version from the future",
			mutate: func(t *testing.T, s *StoredSuggestion) *StoredSuggestion {
				s.Version = 5
				return s
			},
			wantErr:     core.ErrConflict,
			wantVersion: 1,
		},
		{
			name: "unknown id",
			mutate: func(t *testing.T, s *StoredSuggestion) *StoredSuggestion {
				c := *s
				c.ID = core.NewID()
				return &c
			},
			wantErr:     core.ErrNotFound,
			wantVersion: 1,
		},
		{
			name: "unknown status",
			mutate: func(t *testing.T, s *StoredSuggestion) *StoredSuggestion {
				s.Status = "closed"
				return s
			},
			wantErr:     core.ErrInvalid,
			wantVersion: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			created := mustSuggestion(t, db, newSuggestion(core.NewID(), core.NewID(), module.AuthorAI, "u1"), suggestionBase)
			stored, err := db.GetSuggestion(ctx, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := module.SetEditStatus(&stored.Suggestion, []string{"e1"}, module.EditAccepted); err != nil {
				t.Fatal(err)
			}
			stored.UpdateStatus("u2", suggestionBase.Add(time.Minute))
			s := tc.mutate(t, stored)
			err = db.UpdateSuggestion(ctx, s, suggestionBase.Add(time.Minute))
			if tc.wantErr == nil && err != nil || tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("got error %v, want %v", err, tc.wantErr)
			}
			after, err := db.GetSuggestion(ctx, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			if after.Version != tc.wantVersion {
				t.Fatalf("stored version %d, want %d", after.Version, tc.wantVersion)
			}
			if tc.wantErr == nil {
				if s.Version != 2 || !s.UpdatedAt.Equal(suggestionBase.Add(time.Minute)) {
					t.Fatalf("caller copy not advanced: version %d updated %s", s.Version, s.UpdatedAt)
				}
				if after.Edits[0].Status != module.EditAccepted || after.Status != module.SuggestionOpen {
					t.Fatalf("edit status not stored: %+v", after)
				}
			}
			if tc.wantErr != nil && after.Version == 1 && after.Edits[0].Status != module.EditOpen {
				t.Fatalf("failed update changed the edits: %+v", after.Edits)
			}
		})
	}
}

func TestDeleteSuggestionsByNoteAndSpace(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	space, other := core.NewID(), core.NewID()
	note, keep := core.NewID(), core.NewID()
	for range 3 {
		mustSuggestion(t, db, newSuggestion(space, note, module.AuthorAI, "u1"), suggestionBase)
	}
	kept := mustSuggestion(t, db, newSuggestion(space, keep, module.AuthorAI, "u1"), suggestionBase)
	mustSuggestion(t, db, newSuggestion(other, core.NewID(), module.AuthorAI, "u1"), suggestionBase)

	if n, err := db.DeleteNoteSuggestions(ctx, note); err != nil || n != 3 {
		t.Fatalf("delete by note: %d %v", n, err)
	}
	if list, _ := db.ListNoteSuggestions(ctx, keep, nil); len(list) != 1 || list[0].ID != kept.ID {
		t.Fatalf("other note lost its suggestion: %v", suggestionIDs(list))
	}
	if n, err := db.DeleteSpaceSuggestions(ctx, space); err != nil || n != 1 {
		t.Fatalf("delete by space: %d %v", n, err)
	}
	if list, _ := db.ListSuggestions(ctx, SuggestionFilter{}); len(list) != 1 {
		t.Fatalf("other space lost its suggestion: %v", suggestionIDs(list))
	}
	if err := db.DeleteSuggestion(ctx, kept.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("delete of removed suggestion: %v", err)
	}
}
