package ports

import (
	"context"
	"time"

	"github.com/gamemaker1/markspace/internal/module"
)

// StoredSuggestion is one stored suggestion. Version starts at 1 and grows by
// one on every UpdateSuggestion. It is the compare-and-swap token.
type StoredSuggestion struct {
	module.Suggestion
	Version   int64
	UpdatedAt time.Time
}

// SuggestionFilter selects suggestions in ListSuggestions. Empty fields match
// every row.
type SuggestionFilter struct {
	SpaceIDs   []string
	NoteID     string
	AuthorKind string
	AuthorID   string
	Statuses   []string
	Limit      int
}

// SuggestionDB stores suggested edits. The index database implements it
// through index.DB.Suggestions.
type SuggestionDB interface {
	// CreateSuggestion inserts a suggestion and sets Version to 1.
	CreateSuggestion(ctx context.Context, s *StoredSuggestion, now time.Time) error
	// GetSuggestion returns core.ErrNotFound for an unknown id.
	GetSuggestion(ctx context.Context, id string) (*StoredSuggestion, error)
	// ListNoteSuggestions returns the suggestions of a note, oldest first.
	ListNoteSuggestions(ctx context.Context, noteID string, statuses []string) ([]*StoredSuggestion, error)
	// ListSuggestions returns matching suggestions, newest first.
	ListSuggestions(ctx context.Context, f SuggestionFilter) ([]*StoredSuggestion, error)
	// UpdateSuggestion writes edits, status and resolution while the stored
	// version equals s.Version, and returns core.ErrConflict otherwise.
	UpdateSuggestion(ctx context.Context, s *StoredSuggestion, now time.Time) error
	DeleteSuggestion(ctx context.Context, id string) error
	DeleteNoteSuggestions(ctx context.Context, noteID string) (int, error)
	DeleteSpaceSuggestions(ctx context.Context, spaceID string) (int, error)
}

// UserKinds reports account kinds. The index database implements it. The
// application refuses sign-in, sessions and API tokens for a user it reports
// as unable to sign in.
type UserKinds interface {
	UserCanSignIn(ctx context.Context, userID string) (bool, error)
	UserKind(ctx context.Context, userID string) (string, error)
}
