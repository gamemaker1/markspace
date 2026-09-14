package core

import (
	"context"
	"time"
)

// SyncNote is one note in a sync batch, with the actor's access to it.
// Content is empty for a trashed note.
type SyncNote struct {
	*Note
	Access Access `json:"access"`
}

// SyncBatch is one page of the change feed that offline clients replicate.
type SyncBatch struct {
	// Rev is the cursor to pass as since on the next pull.
	Rev int64 `json:"rev"`
	// More reports that further changes are waiting, so the client should
	// pull again at once with Rev.
	More       bool      `json:"more"`
	ServerTime time.Time `json:"serverTime"`
	User       *User     `json:"user"`
	// Spaces lists every space the actor may view, on every page.
	Spaces []*Space    `json:"spaces"`
	Notes  []*SyncNote `json:"notes"`
	// Folders and Files hold item metadata changed in the same revision
	// window as Notes, from the same counter.
	Folders []*Folder `json:"folders"`
	Files   []*File   `json:"files"`
	// LiveIDs lists every note the actor may view, trashed notes included.
	// It is an array on the last page, where More is false, and null on
	// earlier pages. A client drops local notes missing from it.
	LiveIDs []string `json:"liveIds"`
	// StarredIDs lists the notes the actor starred. It follows the same rule
	// as LiveIDs.
	StarredIDs []string `json:"starredIds"`
	// AccessKey changes whenever the actor's admin flag, space roles or note
	// shares change. A client that sees a new value discards its cursor and
	// pulls a full snapshot, because a note the actor just gained access to
	// can carry a revision older than the cursor.
	AccessKey string `json:"accessKey"`
}

// SyncService serves the change feed.
type SyncService interface {
	// SyncPull returns notes changed after revision since, in revision
	// order, at most limit per page. since 0 returns a full snapshot.
	SyncPull(ctx context.Context, actor *Actor, since int64, limit int) (*SyncBatch, error)
}
