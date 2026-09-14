package core

import (
	"context"
	"time"
)

// Suggestion is a proposed edit to a note. docs/plugins.md specifies it, and
// internal/module holds the anchoring and apply logic.
type Suggestion struct {
	ID            string          `json:"id"`
	SpaceID       string          `json:"spaceId"`
	NoteID        string          `json:"noteId"`
	AuthorKind    string          `json:"authorKind"` // user, plugin, ai
	AuthorID      string          `json:"authorId"`
	Title         string          `json:"title"`
	Rationale     string          `json:"rationale,omitempty"`
	Edits         []SuggestedEdit `json:"edits"`
	BaseUpdatedAt time.Time       `json:"baseUpdatedAt"`
	Status        string          `json:"status"` // open, accepted, partial, discarded, stale
	CreatedAt     time.Time       `json:"createdAt"`
	ResolvedBy    string          `json:"resolvedBy,omitempty"`
	ResolvedAt    *time.Time      `json:"resolvedAt,omitempty"`
	UpdatedAt     time.Time       `json:"updatedAt"`
}

// SuggestedEdit replaces the anchored passage with Text. An empty Exact with
// Start == End inserts at a point located by Prefix and Suffix.
type SuggestedEdit struct {
	ID     string        `json:"id"`
	Anchor CommentAnchor `json:"anchor"`
	Text   string        `json:"text"`
	Status string        `json:"status"` // open, accepted, discarded, orphaned
}

// SuggestionEditInput is one edit of a new suggestion. An empty ID gets one.
type SuggestionEditInput struct {
	ID     string        `json:"id,omitempty"`
	Anchor CommentAnchor `json:"anchor"`
	Text   string        `json:"text"`
}

// SuggestionInput creates a suggestion.
type SuggestionInput struct {
	NoteID    string                `json:"noteId"`
	Title     string                `json:"title"`
	Rationale string                `json:"rationale,omitempty"`
	Edits     []SuggestionEditInput `json:"edits"`
	// AuthorKind may be "ai" for a suggestion an AI feature made for the
	// actor. Empty derives it from the actor.
	AuthorKind string `json:"-"`
}

// SuggestionQuery filters the inbox. Empty fields match everything. An empty
// Status means open, and "all" matches every status. AuthorID "me" lists the
// actor's own suggestions: a plugin token's plugin suggestions, or a person's
// suggestions, on notes the actor may view rather than edit.
type SuggestionQuery struct {
	SpaceID  string `json:"spaceId,omitempty"`
	NoteID   string `json:"noteId,omitempty"`
	AuthorID string `json:"authorId,omitempty"`
	Status   string `json:"status,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// SuggestionAuthorMe is the AuthorID of a query for the actor's own
// suggestions.
const SuggestionAuthorMe = "me"

// SuggestionResult is the answer of an accept: the suggestion and, when the
// body changed, the updated note.
type SuggestionResult struct {
	Suggestion *Suggestion `json:"suggestion"`
	Note       *Note       `json:"note,omitempty"`
}

// SuggestionService manages suggested edits.
type SuggestionService interface {
	// CreateSuggestion needs comment access on the note. Every edit must
	// resolve in the current body and no two may overlap.
	CreateSuggestion(ctx context.Context, actor *Actor, in SuggestionInput) (*Suggestion, error)
	// ListNoteSuggestions needs view access. An empty statuses list means
	// every status.
	ListNoteSuggestions(ctx context.Context, actor *Actor, noteID string, statuses []string) ([]*Suggestion, error)
	// SuggestionInbox lists suggestions on notes the actor may edit, newest
	// first.
	SuggestionInbox(ctx context.Context, actor *Actor, q SuggestionQuery) ([]*Suggestion, error)
	GetSuggestion(ctx context.Context, actor *Actor, id string) (*Suggestion, error)
	// AcceptSuggestion applies the named open edits, or every open edit that
	// resolves when editIDs is nil, through the note update path with the
	// current updatedAt. It saves a version. It needs edit access.
	AcceptSuggestion(ctx context.Context, actor *Actor, id string, editIDs []string) (*SuggestionResult, error)
	// DiscardSuggestion discards the named edits, or every open and orphaned
	// edit when editIDs is nil. It needs edit access.
	DiscardSuggestion(ctx context.Context, actor *Actor, id string, editIDs []string) (*Suggestion, error)
	// WithdrawSuggestion deletes a suggestion. Only its author may withdraw
	// it: the plugin for a plugin suggestion, the user otherwise.
	WithdrawSuggestion(ctx context.Context, actor *Actor, id string) error
}
