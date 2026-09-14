package module

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/gamemaker1/markspace/internal/anchor"
	"github.com/gamemaker1/markspace/internal/core"
)

// Suggestion states.
const (
	SuggestionOpen      = "open"
	SuggestionAccepted  = "accepted"
	SuggestionPartial   = "partial"
	SuggestionDiscarded = "discarded"
	SuggestionStale     = "stale"
)

// Suggested edit states.
const (
	EditOpen      = "open"
	EditAccepted  = "accepted"
	EditDiscarded = "discarded"
	EditOrphaned  = "orphaned"
)

// Suggestion author kinds.
const (
	AuthorUser   = "user"
	AuthorPlugin = "plugin"
	AuthorAI     = "ai"
)

// Suggestion limits.
const (
	MaxSuggestedEdits    = 500
	MaxSuggestedEditText = 100_000
)

// Suggestion errors.
var (
	// ErrOverlappingEdits reports a set of edits whose passages overlap.
	ErrOverlappingEdits = fmt.Errorf("suggested edits overlap: %w", core.ErrConflict)
	// ErrOrphanedEdit reports an edit whose passage no longer resolves.
	ErrOrphanedEdit = fmt.Errorf("suggested edit no longer matches the note: %w", core.ErrConflict)
)

// Suggestion is a proposed edit to a note.
type Suggestion struct {
	ID            string          `json:"id"`
	SpaceID       string          `json:"spaceId"`
	NoteID        string          `json:"noteId"`
	AuthorKind    string          `json:"authorKind"`
	AuthorID      string          `json:"authorId"`
	Title         string          `json:"title"`
	Rationale     string          `json:"rationale,omitempty"`
	Edits         []SuggestedEdit `json:"edits"`
	BaseUpdatedAt time.Time       `json:"baseUpdatedAt"`
	Status        string          `json:"status"`
	CreatedAt     time.Time       `json:"createdAt"`
	ResolvedBy    string          `json:"resolvedBy,omitempty"`
	ResolvedAt    *time.Time      `json:"resolvedAt,omitempty"`
}

// SuggestedEdit replaces the anchored passage with Text. An empty Exact with
// Start == End inserts at a point located by Prefix and Suffix.
type SuggestedEdit struct {
	ID     string             `json:"id"`
	Anchor core.CommentAnchor `json:"anchor"`
	Text   string             `json:"text"`
	Status string             `json:"status"`
}

// ResolvedEdit is an edit located in a body. Offsets count UTF-16 code units.
// An orphaned edit has State core.AnchorOrphaned and offsets of -1.
type ResolvedEdit struct {
	ID    string `json:"id"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	State string `json:"state"`
	Text  string `json:"text"`
}

// Orphaned reports whether the edit did not resolve.
func (r ResolvedEdit) Orphaned() bool { return r.State == core.AnchorOrphaned }

// IsInsertion reports whether an anchor marks an insertion point.
func IsInsertion(a core.CommentAnchor) bool { return a.Exact == "" && a.Start == a.End }

func toUnits(s string) []uint16 { return utf16.Encode([]rune(s)) }

func fromUnits(u []uint16) string { return string(utf16.Decode(u)) }

func isSurrogateSplit(u []uint16, i int) bool {
	return i > 0 && i < len(u) && u[i] >= 0xDC00 && u[i] <= 0xDFFF && u[i-1] >= 0xD800 && u[i-1] <= 0xDBFF
}

// InsertionAnchor builds the anchor of an insertion point at offset at, with
// up to anchor.Context code units of context on each side. It reports false
// when at lies outside the body or splits a surrogate pair.
func InsertionAnchor(body string, at int) (core.CommentAnchor, bool) {
	return insertionAnchor(toUnits(body), at)
}

func insertionAnchor(u []uint16, at int) (core.CommentAnchor, bool) {
	if at < 0 || at > len(u) || isSurrogateSplit(u, at) {
		return core.CommentAnchor{}, false
	}
	ps := max(0, at-anchor.Context)
	if isSurrogateSplit(u, ps) {
		ps++
	}
	se := min(len(u), at+anchor.Context)
	if isSurrogateSplit(u, se) {
		se--
	}
	return core.CommentAnchor{Prefix: fromUnits(u[ps:at]), Suffix: fromUnits(u[at:se]), Start: at, End: at}, true
}

// resolveInsertion finds an insertion point: the occurrence of Prefix followed
// by Suffix nearest to Start, the earlier one on a tie. Without context the
// point is Start when it lies inside the body.
func resolveInsertion(u []uint16, a core.CommentAnchor) (int, string) {
	p, s := toUnits(a.Prefix), toUnits(a.Suffix)
	if len(p)+len(s) == 0 {
		if a.Start >= 0 && a.Start <= len(u) && !isSurrogateSplit(u, a.Start) {
			return a.Start, core.AnchorExact
		}
		return -1, core.AnchorOrphaned
	}
	pat := append(p, s...)
	hint := max(0, min(a.Start, len(u)))
	best, bestDist := -1, 0
	for i := 0; i+len(pat) <= len(u); i++ {
		if !slices.Equal(u[i:i+len(pat)], pat) {
			continue
		}
		at := i + len(p)
		dist := at - hint
		if dist < 0 {
			dist = -dist
		}
		if best < 0 || dist < bestDist {
			best, bestDist = at, dist
		}
	}
	if best < 0 {
		return -1, core.AnchorOrphaned
	}
	if best == a.Start {
		return best, core.AnchorExact
	}
	return best, core.AnchorMoved
}

// ResolveEdits locates every open or orphaned edit of s in body, in edit
// order. Accepted and discarded edits are skipped.
func ResolveEdits(body string, s *Suggestion) []ResolvedEdit {
	var (
		u  []uint16
		b  anchor.Body
		ok bool
	)
	out := []ResolvedEdit{}
	for _, e := range s.Edits {
		if e.Status != EditOpen && e.Status != EditOrphaned {
			continue
		}
		if !ok {
			u, b, ok = toUnits(body), anchor.NewBody(body), true
		}
		r := ResolvedEdit{ID: e.ID, Text: e.Text}
		if IsInsertion(e.Anchor) {
			at, state := resolveInsertion(u, e.Anchor)
			r.Start, r.End, r.State = at, at, state
		} else {
			r.Start, r.End, r.State = b.Resolve(e.Anchor)
		}
		out = append(out, r)
	}
	return out
}

// ApplicableIDs returns the ids of the edits that resolved, in order.
func ApplicableIDs(edits []ResolvedEdit) []string {
	var ids []string
	for _, e := range edits {
		if !e.Orphaned() {
			ids = append(ids, e.ID)
		}
	}
	return ids
}

// ApplyEdits applies the edits named by ids to body and returns the new body
// and the applied ids in the order given. No ids leaves the body unchanged. It
// returns core.ErrInvalid for a repeated id or offsets outside the body, a
// wrapped core.ErrNotFound for an unknown id, ErrOrphanedEdit for an orphaned
// edit, and ErrOverlappingEdits when two selected passages overlap. Two
// insertions at the same point overlap. An insertion at the start or end of a
// replaced passage does not, and lands before or after the new text.
func ApplyEdits(body string, edits []ResolvedEdit, ids []string) (string, []string, error) {
	if len(ids) == 0 {
		return body, nil, nil
	}
	byID := make(map[string]ResolvedEdit, len(edits))
	for _, e := range edits {
		if _, dup := byID[e.ID]; dup {
			return body, nil, core.Invalid("edits", fmt.Sprintf("edit %q repeats", e.ID))
		}
		byID[e.ID] = e
	}
	sel := make([]ResolvedEdit, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			return body, nil, core.Invalid("editIds", fmt.Sprintf("edit %q repeats", id))
		}
		seen[id] = true
		e, ok := byID[id]
		if !ok {
			return body, nil, fmt.Errorf("edit %q: %w", id, core.ErrNotFound)
		}
		if e.Orphaned() {
			return body, nil, fmt.Errorf("edit %q: %w", id, ErrOrphanedEdit)
		}
		sel = append(sel, e)
	}
	slices.SortStableFunc(sel, func(a, b ResolvedEdit) int {
		if c := cmp.Compare(a.Start, b.Start); c != 0 {
			return c
		}
		return cmp.Compare(a.End, b.End)
	})
	for i, e := range sel {
		if e.Start < 0 || e.End < e.Start {
			return body, nil, core.Invalid("edits", fmt.Sprintf("edit %q has invalid offsets", e.ID))
		}
		if i == 0 {
			continue
		}
		prev := sel[i-1]
		if e.Start < prev.End || (e.Start == prev.Start && e.End == prev.End) {
			return body, nil, fmt.Errorf("edits %q and %q: %w", prev.ID, e.ID, ErrOverlappingEdits)
		}
	}
	offsets := make([]int, 0, 2*len(sel))
	for _, e := range sel {
		offsets = append(offsets, e.Start, e.End)
	}
	bytePos, err := unitsToBytes(body, offsets)
	if err != nil {
		return body, nil, err
	}
	var out strings.Builder
	out.Grow(len(body))
	last := 0
	for i, e := range sel {
		bs, be := bytePos[2*i], bytePos[2*i+1]
		out.WriteString(body[last:bs])
		out.WriteString(e.Text)
		last = be
	}
	out.WriteString(body[last:])
	return out.String(), slices.Clone(ids), nil
}

// unitsToBytes converts ascending UTF-16 offsets into byte offsets of s. Each
// invalid UTF-8 byte counts as one unit, as in the anchor package.
func unitsToBytes(s string, offsets []int) ([]int, error) {
	out := make([]int, len(offsets))
	unit, i, k := 0, 0, 0
	for k < len(offsets) {
		for k < len(offsets) && offsets[k] == unit {
			out[k] = i
			k++
		}
		if k == len(offsets) {
			break
		}
		if i >= len(s) {
			return nil, core.Invalid("edits", fmt.Sprintf("offset %d is past the end of the body", offsets[k]))
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		w := 1
		if r >= 0x10000 {
			w = 2
		}
		if offsets[k] == unit+1 && w == 2 {
			return nil, core.Invalid("edits", fmt.Sprintf("offset %d splits a surrogate pair", offsets[k]))
		}
		unit += w
		i += size
	}
	return out, nil
}

// RemapSuggestion moves the anchors of the open and orphaned edits of s
// through a change from oldBody to newBody. It reports whether any anchor
// changed.
func RemapSuggestion(oldBody, newBody string, s *Suggestion) bool {
	if oldBody == newBody {
		return false
	}
	var (
		edit       *anchor.Edit
		oldU, newU []uint16
		pre, suf   int
	)
	changed := false
	for i := range s.Edits {
		e := &s.Edits[i]
		if e.Status != EditOpen && e.Status != EditOrphaned {
			continue
		}
		if !IsInsertion(e.Anchor) {
			if edit == nil {
				edit = anchor.NewEdit(oldBody, newBody)
			}
			if a, ok := edit.Remap(e.Anchor); ok {
				e.Anchor = a
				changed = true
			}
			continue
		}
		if oldU == nil {
			oldU, newU = toUnits(oldBody), toUnits(newBody)
			for pre < len(oldU) && pre < len(newU) && oldU[pre] == newU[pre] {
				pre++
			}
			for suf < len(oldU)-pre && suf < len(newU)-pre && oldU[len(oldU)-1-suf] == newU[len(newU)-1-suf] {
				suf++
			}
		}
		at, state := resolveInsertion(oldU, e.Anchor)
		if state == core.AnchorOrphaned {
			continue
		}
		var nat int
		switch {
		case at <= pre:
			nat = at
		case at >= len(oldU)-suf:
			nat = at + len(newU) - len(oldU)
		default:
			// The point lies inside the changed text. Its context may still
			// occur in the new body.
			nat, state = resolveInsertion(newU, e.Anchor)
			if state == core.AnchorOrphaned {
				continue
			}
		}
		if a, ok := insertionAnchor(newU, nat); ok && a != e.Anchor {
			e.Anchor = a
			changed = true
		}
	}
	return changed
}

// MarkOrphans resolves the open and orphaned edits of s in body and marks each
// as orphaned or open. It updates the suggestion status when the edits imply
// open or stale, and leaves a resolved status to UpdateStatus, which records
// the actor. It reports whether any edit changed state.
func MarkOrphans(body string, s *Suggestion, now time.Time) bool {
	resolved := ResolveEdits(body, s)
	state := make(map[string]bool, len(resolved))
	for _, r := range resolved {
		state[r.ID] = r.Orphaned()
	}
	changed := false
	for i := range s.Edits {
		e := &s.Edits[i]
		orphaned, ok := state[e.ID]
		if !ok {
			continue
		}
		next := EditOpen
		if orphaned {
			next = EditOrphaned
		}
		if e.Status != next {
			e.Status = next
			changed = true
		}
	}
	if st := s.derivedStatus(); st == SuggestionOpen || st == SuggestionStale {
		s.UpdateStatus("", now)
	}
	return changed
}

// derivedStatus returns the status the edits imply.
func (s *Suggestion) derivedStatus() string {
	var open, orphaned, accepted, discarded int
	for _, e := range s.Edits {
		switch e.Status {
		case EditOpen:
			open++
		case EditOrphaned:
			orphaned++
		case EditAccepted:
			accepted++
		case EditDiscarded:
			discarded++
		}
	}
	switch {
	case open > 0:
		return SuggestionOpen
	case orphaned > 0:
		return SuggestionStale
	case accepted > 0 && discarded > 0:
		return SuggestionPartial
	case accepted > 0:
		return SuggestionAccepted
	}
	return SuggestionDiscarded
}

// SetEditStatus marks edits accepted or discarded. Accepting needs an open
// edit. Discarding needs an open or orphaned edit. An unknown id returns a
// wrapped core.ErrNotFound and a wrong state returns core.ErrConflict. Nothing
// changes on error.
func SetEditStatus(s *Suggestion, ids []string, status string) error {
	if status != EditAccepted && status != EditDiscarded {
		return core.Invalid("status", fmt.Sprintf("cannot set edit status %q", status))
	}
	idx := make([]int, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return core.Invalid("editIds", fmt.Sprintf("edit %q repeats", id))
		}
		seen[id] = true
		i := slices.IndexFunc(s.Edits, func(e SuggestedEdit) bool { return e.ID == id })
		if i < 0 {
			return fmt.Errorf("edit %q: %w", id, core.ErrNotFound)
		}
		cur := s.Edits[i].Status
		if cur != EditOpen && !(status == EditDiscarded && cur == EditOrphaned) {
			return fmt.Errorf("edit %q is %s: %w", id, cur, core.ErrConflict)
		}
		idx = append(idx, i)
	}
	for _, i := range idx {
		s.Edits[i].Status = status
	}
	return nil
}

// UpdateStatus derives the suggestion status from its edits. Any open edit
// keeps it open. Otherwise any orphaned edit makes it stale. Otherwise it is
// accepted, discarded or partial. A resolved status records actorID and now
// the first time. An open or stale status clears them.
func (s *Suggestion) UpdateStatus(actorID string, now time.Time) {
	s.Status = s.derivedStatus()
	switch s.Status {
	case SuggestionOpen, SuggestionStale:
		s.ResolvedBy, s.ResolvedAt = "", nil
	default:
		if s.ResolvedAt == nil {
			t := now.UTC()
			s.ResolvedBy, s.ResolvedAt = actorID, &t
		}
	}
}

// AcceptEdits applies the named open edits to body, or every open edit that
// resolves when ids is nil. It marks them accepted, remaps the remaining
// edits to the new body, marks orphans and updates the status. On error s is
// unchanged.
func AcceptEdits(body string, s *Suggestion, ids []string, actorID string, now time.Time) (string, error) {
	work := cloneSuggestion(s)
	MarkOrphans(body, work, now)
	resolved := ResolveEdits(body, work)
	if ids == nil {
		for _, r := range resolved {
			if !r.Orphaned() {
				ids = append(ids, r.ID)
			}
		}
	}
	newBody, applied, err := ApplyEdits(body, resolved, ids)
	if err != nil {
		return body, err
	}
	if err := SetEditStatus(work, applied, EditAccepted); err != nil {
		return body, err
	}
	RemapSuggestion(body, newBody, work)
	MarkOrphans(newBody, work, now)
	work.UpdateStatus(actorID, now)
	*s = *work
	return newBody, nil
}

// DiscardEdits marks the named edits discarded, or every open and orphaned
// edit when ids is nil, and updates the status. On error s is unchanged.
func DiscardEdits(s *Suggestion, ids []string, actorID string, now time.Time) error {
	if ids == nil {
		for _, e := range s.Edits {
			if e.Status == EditOpen || e.Status == EditOrphaned {
				ids = append(ids, e.ID)
			}
		}
	}
	work := cloneSuggestion(s)
	if err := SetEditStatus(work, ids, EditDiscarded); err != nil {
		return err
	}
	work.UpdateStatus(actorID, now)
	*s = *work
	return nil
}

func cloneSuggestion(s *Suggestion) *Suggestion {
	c := *s
	c.Edits = slices.Clone(s.Edits)
	if s.ResolvedAt != nil {
		t := *s.ResolvedAt
		c.ResolvedAt = &t
	}
	return &c
}

// ValidateNewSuggestion checks a suggestion as created against the current
// body. It normalizes every anchor, requires every edit to resolve and the
// edits not to overlap, and sets every edit and the suggestion to open. Edit
// ids must already be set and unique.
func ValidateNewSuggestion(body string, s *Suggestion) error {
	switch {
	case s.AuthorKind != AuthorUser && s.AuthorKind != AuthorPlugin && s.AuthorKind != AuthorAI:
		return core.Invalid("authorKind", fmt.Sprintf("unknown author kind %q", s.AuthorKind))
	case strings.TrimSpace(s.Title) == "" || len(s.Title) > 200:
		return core.Invalid("title", "must be 1 to 200 bytes")
	case len(s.Rationale) > 10_000:
		return core.Invalid("rationale", "is longer than 10000 bytes")
	case len(s.Edits) == 0:
		return core.Invalid("edits", "at least one edit is required")
	case len(s.Edits) > MaxSuggestedEdits:
		return core.Invalid("edits", fmt.Sprintf("at most %d edits are allowed", MaxSuggestedEdits))
	}
	seen := map[string]bool{}
	for i := range s.Edits {
		e := &s.Edits[i]
		field := fmt.Sprintf("edits[%d]", i)
		if e.ID == "" || len(e.ID) > 64 || seen[e.ID] {
			return core.Invalid(field+".id", "edit ids must be unique and 1 to 64 bytes")
		}
		seen[e.ID] = true
		if len(e.Text) > MaxSuggestedEditText {
			return core.Invalid(field+".text", fmt.Sprintf("is longer than %d bytes", MaxSuggestedEditText))
		}
		if IsInsertion(e.Anchor) {
			if e.Anchor.Start < 0 {
				return core.Invalid(field+".anchor", "anchor offsets must not be negative")
			}
			e.Anchor.Prefix = lastUnits(e.Anchor.Prefix, anchor.Context)
			e.Anchor.Suffix = firstUnits(e.Anchor.Suffix, anchor.Context)
			if e.Text == "" {
				return core.Invalid(field+".text", "an insertion needs text")
			}
		} else {
			a, err := anchor.Normalize(e.Anchor)
			if err != nil {
				return core.Invalid(field+".anchor", err.Error())
			}
			e.Anchor = a
		}
		e.Status = EditOpen
	}
	resolved := ResolveEdits(body, s)
	for _, r := range resolved {
		if r.Orphaned() {
			return fmt.Errorf("edit %q: %w", r.ID, ErrOrphanedEdit)
		}
	}
	if _, _, err := ApplyEdits(body, resolved, ApplicableIDs(resolved)); err != nil {
		return err
	}
	s.Status = SuggestionOpen
	s.ResolvedBy, s.ResolvedAt = "", nil
	return nil
}

func lastUnits(s string, n int) string {
	u := toUnits(s)
	if len(u) <= n {
		return s
	}
	cut := len(u) - n
	if isSurrogateSplit(u, cut) {
		cut++
	}
	return fromUnits(u[cut:])
}

func firstUnits(s string, n int) string {
	u := toUnits(s)
	if len(u) <= n {
		return s
	}
	cut := n
	if isSurrogateSplit(u, cut) {
		cut--
	}
	return fromUnits(u[:cut])
}
