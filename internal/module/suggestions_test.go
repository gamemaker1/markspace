package module

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/gamemaker1/markspace/internal/anchor"
	"github.com/gamemaker1/markspace/internal/core"
)

// unitIndex returns the UTF-16 offset of the first occurrence of sub.
func unitIndex(t testing.TB, body, sub string) int {
	t.Helper()
	i := strings.Index(body, sub)
	if i < 0 {
		t.Fatalf("%q not in body", sub)
	}
	return len(utf16.Encode([]rune(body[:i])))
}

func unitLen(s string) int { return len(utf16.Encode([]rune(s))) }

func replaceEdit(t testing.TB, id, body, sub, text string) SuggestedEdit {
	t.Helper()
	start := unitIndex(t, body, sub)
	a, ok := anchor.Make(body, start, start+unitLen(sub))
	if !ok {
		t.Fatalf("cannot anchor %q", sub)
	}
	return SuggestedEdit{ID: id, Anchor: *a, Text: text, Status: EditOpen}
}

func insertEdit(t testing.TB, id, body string, at int, text string) SuggestedEdit {
	t.Helper()
	a, ok := InsertionAnchor(body, at)
	if !ok {
		t.Fatalf("cannot anchor insertion at %d", at)
	}
	return SuggestedEdit{ID: id, Anchor: a, Text: text, Status: EditOpen}
}

func TestResolveAndApplyEdits(t *testing.T) {
	body := "# Plan\n\nShip the sync engine \U0001F680 today.\n\nThen sync docs.\n"
	afterEngine := unitIndex(t, body, " \U0001F680")
	s := &Suggestion{Edits: []SuggestedEdit{
		replaceEdit(t, "e1", body, "sync", "replication"),
		insertEdit(t, "e2", body, afterEngine, " v2"),
		replaceEdit(t, "e3", body, "docs", "the docs"),
		{ID: "done", Anchor: core.CommentAnchor{Exact: "Plan"}, Text: "x", Status: EditAccepted},
	}}
	resolved := ResolveEdits(body, s)
	if len(resolved) != 3 {
		t.Fatalf("resolved %d edits, want the 3 open ones", len(resolved))
	}
	for _, r := range resolved {
		if r.State != core.AnchorExact {
			t.Fatalf("edit %s resolved %s", r.ID, r.State)
		}
	}
	got, applied, err := ApplyEdits(body, resolved, ApplicableIDs(resolved))
	if err != nil {
		t.Fatal(err)
	}
	want := "# Plan\n\nShip the replication engine v2 \U0001F680 today.\n\nThen sync the docs.\n"
	if got != want || !slices.Equal(applied, []string{"e1", "e2", "e3"}) {
		t.Fatalf("ApplyEdits = %q %v", got, applied)
	}
	if same, applied, err := ApplyEdits(body, resolved, nil); same != body || applied != nil || err != nil {
		t.Fatalf("no ids changed the body")
	}

	moved := "Intro line.\n" + body
	for _, r := range ResolveEdits(moved, s) {
		if r.State != core.AnchorMoved {
			t.Fatalf("edit %s after a prepended line resolved %s", r.ID, r.State)
		}
	}
	gone := strings.Replace(body, "docs", "notes", 1)
	r := ResolveEdits(gone, s)
	if !r[2].Orphaned() || r[2].Start != -1 {
		t.Fatalf("removed passage: %+v", r[2])
	}
	// The insertion point holds " docs" in its suffix, and an insertion needs
	// its whole context, so it resolves again only after a remap.
	if !r[1].Orphaned() || len(ApplicableIDs(r)) != 1 {
		t.Fatalf("insertion with changed context: %+v", r[1])
	}
	if _, _, err := ApplyEdits(gone, r, []string{"e3"}); !errors.Is(err, ErrOrphanedEdit) {
		t.Fatalf("applying an orphaned edit: %v", err)
	}
	RemapSuggestion(body, gone, s)
	if r = ResolveEdits(gone, s); r[1].State != core.AnchorExact || r[1].Start != afterEngine {
		t.Fatalf("insertion after remap: %+v", r[1])
	}
}

func TestApplyEditsRejects(t *testing.T) {
	body := "abcdef"
	edits := []ResolvedEdit{
		{ID: "r13", Start: 1, End: 3, Text: "X", State: core.AnchorExact},
		{ID: "r24", Start: 2, End: 4, Text: "Y", State: core.AnchorExact},
		{ID: "i3", Start: 3, End: 3, Text: "<", State: core.AnchorExact},
		{ID: "i3b", Start: 3, End: 3, Text: ">", State: core.AnchorExact},
		{ID: "i2", Start: 2, End: 2, Text: "!", State: core.AnchorExact},
		{ID: "r24b", Start: 2, End: 4, Text: "Z", State: core.AnchorExact},
		{ID: "far", Start: 5, End: 9, Text: "Z", State: core.AnchorExact},
		{ID: "orphan", Start: -1, End: -1, Text: "Z", State: core.AnchorOrphaned},
	}
	cases := []struct {
		ids  []string
		want error
	}{
		{[]string{"r13", "r24"}, ErrOverlappingEdits},
		{[]string{"i3", "i3b"}, ErrOverlappingEdits},
		{[]string{"r24", "i3"}, ErrOverlappingEdits},
		{[]string{"r24", "r24b"}, ErrOverlappingEdits},
		{[]string{"r13", "i2"}, ErrOverlappingEdits},
		{[]string{"far"}, core.ErrInvalid},
		{[]string{"orphan"}, ErrOrphanedEdit},
		{[]string{"missing"}, core.ErrNotFound},
		{[]string{"r13", "r13"}, core.ErrInvalid},
	}
	for _, c := range cases {
		got, _, err := ApplyEdits(body, edits, c.ids)
		if !errors.Is(err, c.want) || got != body {
			t.Errorf("ids %v: %q, %v, want %v", c.ids, got, err, c.want)
		}
	}
	if _, _, err := ApplyEdits(body, append(edits, edits[0]), []string{"r13"}); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("repeated edit id: %v", err)
	}
	edges := []ResolvedEdit{
		{ID: "rep", Start: 2, End: 4, Text: "XY", State: core.AnchorMoved},
		{ID: "before", Start: 2, End: 2, Text: "<", State: core.AnchorExact},
		{ID: "after", Start: 4, End: 4, Text: ">", State: core.AnchorExact},
	}
	got, _, err := ApplyEdits(body, edges, []string{"after", "rep", "before"})
	if err != nil || got != "ab<XY>ef" {
		t.Fatalf("insertions at the edges of a replacement: %q %v", got, err)
	}
	emoji := "a\U0001F600b"
	split := []ResolvedEdit{{ID: "s", Start: 2, End: 3, Text: "x", State: core.AnchorExact}}
	if _, _, err := ApplyEdits(emoji, split, []string{"s"}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("offset inside a surrogate pair: %v", err)
	}
	whole := []ResolvedEdit{{ID: "s", Start: 1, End: 3, Text: "x", State: core.AnchorExact}}
	if got, _, err := ApplyEdits(emoji, whole, []string{"s"}); err != nil || got != "axb" {
		t.Fatalf("replacing an emoji: %q %v", got, err)
	}
}

func TestAcceptAndDiscardFlow(t *testing.T) {
	body := "alpha beta gamma delta"
	now := testBase
	s := &Suggestion{AuthorKind: AuthorAI, Title: "Shout", Edits: []SuggestedEdit{
		replaceEdit(t, "e0", body, "beta", "BETA"),
		replaceEdit(t, "e1", body, "delta", "DELTA"),
		insertEdit(t, "e2", body, unitLen(body), " epsilon"),
	}}
	if err := ValidateNewSuggestion(body, s); err != nil {
		t.Fatal(err)
	}
	body1, err := AcceptEdits(body, s, []string{"e0"}, "u1", now)
	if err != nil {
		t.Fatal(err)
	}
	if body1 != "alpha BETA gamma delta" || s.Status != SuggestionOpen || s.Edits[0].Status != EditAccepted || s.ResolvedAt != nil {
		t.Fatalf("after accepting one edit: %q %+v", body1, s)
	}
	for _, r := range ResolveEdits(body1, s) {
		if r.State != core.AnchorExact {
			t.Fatalf("remaining edit %s is %s after remap", r.ID, r.State)
		}
	}

	body2 := strings.Replace(body1, "gamma ", "", 1)
	if !RemapSuggestion(body1, body2, s) {
		t.Fatalf("remap reported no change")
	}
	if MarkOrphans(body2, s, now) {
		t.Fatalf("user edit orphaned an unrelated suggestion")
	}
	body3, err := AcceptEdits(body2, s, nil, "u1", now)
	if err != nil {
		t.Fatal(err)
	}
	if body3 != "alpha BETA DELTA epsilon" || s.Status != SuggestionAccepted || s.ResolvedBy != "u1" || s.ResolvedAt == nil {
		t.Fatalf("after accept all: %q %+v", body3, s)
	}

	stale := &Suggestion{AuthorKind: AuthorPlugin, Title: "t", Edits: []SuggestedEdit{
		replaceEdit(t, "g", body, "gamma", "GAMMA"),
		replaceEdit(t, "a", body, "alpha", "ALPHA"),
	}}
	noGamma := strings.Replace(body, "gamma", "", 1)
	if !MarkOrphans(noGamma, stale, now) || stale.Edits[0].Status != EditOrphaned || stale.Status != SuggestionOpen {
		t.Fatalf("orphan marking: %+v", stale)
	}
	before := cloneSuggestion(stale)
	if _, err := AcceptEdits(noGamma, stale, []string{"g"}, "u1", now); !errors.Is(err, ErrOrphanedEdit) {
		t.Fatalf("accepting an orphaned edit: %v", err)
	}
	if !slices.Equal(stale.Edits, before.Edits) || stale.Status != before.Status {
		t.Fatalf("failed accept changed the suggestion")
	}
	got, err := AcceptEdits(noGamma, stale, nil, "u1", now)
	if err != nil || got != "ALPHA beta  delta" || stale.Status != SuggestionStale {
		t.Fatalf("accept all with an orphan: %q %v %+v", got, err, stale)
	}
	if !MarkOrphans(body, stale, now) || stale.Edits[0].Status != EditOpen || stale.Status != SuggestionOpen {
		t.Fatalf("restored passage did not reopen the edit: %+v", stale)
	}
	if err := DiscardEdits(stale, nil, "u2", now); err != nil {
		t.Fatal(err)
	}
	if stale.Status != SuggestionPartial || stale.ResolvedBy != "u2" {
		t.Fatalf("accepted plus discarded: %+v", stale)
	}
	if err := DiscardEdits(stale, []string{"a"}, "u2", now); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("discarding an accepted edit: %v", err)
	}
	if err := SetEditStatus(stale, []string{"zzz"}, EditDiscarded); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unknown edit: %v", err)
	}

	all := &Suggestion{Edits: []SuggestedEdit{replaceEdit(t, "x", body, "beta", "b")}}
	if err := DiscardEdits(all, nil, "u3", now); err != nil || all.Status != SuggestionDiscarded {
		t.Fatalf("discard all: %v %+v", err, all)
	}
}

func TestRemapSuggestionInsertion(t *testing.T) {
	body := "one two three"
	s := &Suggestion{Edits: []SuggestedEdit{insertEdit(t, "i", body, unitIndex(t, body, " three"), "!")}}
	longer := "zero one two three"
	if !RemapSuggestion(body, longer, s) {
		t.Fatalf("no change")
	}
	r := ResolveEdits(longer, s)
	if r[0].State != core.AnchorExact || r[0].Start != unitIndex(t, longer, " three") {
		t.Fatalf("insertion after a prefix change: %+v", r[0])
	}
	retyped := "zero one TWO three"
	RemapSuggestion(longer, retyped, s)
	r = ResolveEdits(retyped, s)
	if r[0].Orphaned() || r[0].Start != unitIndex(t, retyped, " three") {
		t.Fatalf("insertion after a change that ends at the point: %+v", r[0])
	}
	got, _, err := ApplyEdits(retyped, r, []string{"i"})
	if err != nil || got != "zero one TWO! three" {
		t.Fatalf("apply after remap: %q %v", got, err)
	}
	empty := &Suggestion{Edits: []SuggestedEdit{{ID: "e", Anchor: core.CommentAnchor{}, Text: "hello", Status: EditOpen}}}
	if r := ResolveEdits("", empty); r[0].State != core.AnchorExact || r[0].Start != 0 {
		t.Fatalf("insertion into an empty body: %+v", r[0])
	}
}

func TestValidateNewSuggestion(t *testing.T) {
	body := "alpha beta gamma"
	good := func() *Suggestion {
		return &Suggestion{AuthorKind: AuthorUser, Title: "t", Edits: []SuggestedEdit{
			replaceEdit(t, "a", body, "alpha", "A"),
			insertEdit(t, "b", body, unitLen(body), "!"),
		}}
	}
	if err := ValidateNewSuggestion(body, good()); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		mut  func(s *Suggestion)
		want error
	}{
		"author kind":     {func(s *Suggestion) { s.AuthorKind = "robot" }, core.ErrInvalid},
		"empty title":     {func(s *Suggestion) { s.Title = " " }, core.ErrInvalid},
		"no edits":        {func(s *Suggestion) { s.Edits = nil }, core.ErrInvalid},
		"duplicate id":    {func(s *Suggestion) { s.Edits[1].ID = "a" }, core.ErrInvalid},
		"empty insertion": {func(s *Suggestion) { s.Edits[1].Text = "" }, core.ErrInvalid},
		"orphaned":        {func(s *Suggestion) { s.Edits[0].Anchor.Exact = "omega" }, ErrOrphanedEdit},
		"overlap": {func(s *Suggestion) {
			s.Edits = append(s.Edits, replaceEdit(t, "c", body, "alpha b", "x"))
		}, ErrOverlappingEdits},
	}
	for name, c := range cases {
		s := good()
		c.mut(s)
		if err := ValidateNewSuggestion(body, s); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", name, err, c.want)
		}
	}
	long := good()
	long.Edits[0].Anchor.Prefix = strings.Repeat("p", 100)
	long.Edits[0].Anchor.Suffix = " beta gamma" + strings.Repeat("s", 100)
	if err := ValidateNewSuggestion(body, long); err != nil {
		t.Fatal(err)
	}
	if unitLen(long.Edits[0].Anchor.Prefix) != anchor.Context || unitLen(long.Edits[0].Anchor.Suffix) != anchor.Context {
		t.Fatalf("context not cut to %d units", anchor.Context)
	}
	ins := good()
	ins.Edits[1].Anchor.Prefix = strings.Repeat("p", 100) + body
	if err := ValidateNewSuggestion(body, ins); !errors.Is(err, ErrOrphanedEdit) {
		t.Fatalf("an insertion prefix cut to %d units must still match in full: %v", anchor.Context, err)
	}
	if unitLen(ins.Edits[1].Anchor.Prefix) != anchor.Context {
		t.Fatalf("insertion prefix not cut to %d units", anchor.Context)
	}
}

type fuzzSpan struct{ s, e int }

func spansOverlap(a, b fuzzSpan) bool {
	return (a.s < b.e && b.s < a.e) || (a.s == a.e && b.s == b.e && a.s == b.s)
}

func FuzzApplyEdits(f *testing.F) {
	f.Add("Ship the sync engine.\nThen ship the sync engine docs.\n", []byte{3, 7, 0, 1, 12, 4, 1, 2, 20, 9, 2, 0}, uint16(5))
	f.Add("aaaa", []byte{0, 1, 0, 0, 1, 1, 0, 1, 2, 2, 1, 2}, uint16(2))
	f.Add("x\U0001F600 sync \U0001F600 sync", []byte{1, 2, 0, 3, 4, 4, 2, 0, 9, 3, 0, 1}, uint16(3))
	f.Add("", []byte{0, 0, 1, 0}, uint16(0))
	f.Add("café 你好 world", []byte{0, 4, 0, 0, 5, 2, 1, 1, 8, 5, 0, 2}, uint16(7))
	f.Add(strings.Repeat("line of text\n", 12), []byte{14, 20, 0, 0, 60, 3, 0, 1, 100, 0, 1, 0, 40, 5, 0, 0}, uint16(90))
	f.Fuzz(func(t *testing.T, body string, seed []byte, insAt uint16) {
		body = strings.ToValidUTF8(body, "?")
		if len(body) > 400 {
			body = strings.ToValidUTF8(body[:400], "")
		}
		u := toUnits(body)
		n := len(u)
		var (
			edits []SuggestedEdit
			spans []fuzzSpan
		)
		for i := 0; i+3 < len(seed) && len(edits) < 12; i += 4 {
			start := int(seed[i]) % (n + 1)
			length := int(seed[i+1]) % (n - start + 1)
			text := fmt.Sprintf("<%d>", i/4)
			if seed[i+3]&1 == 1 {
				text += "\U0001F600"
			}
			id := fmt.Sprintf("e%d", i/4)
			if seed[i+2]&1 == 1 || length == 0 {
				a, ok := InsertionAnchor(body, start)
				if !ok {
					continue
				}
				edits = append(edits, SuggestedEdit{ID: id, Anchor: a, Text: text, Status: EditOpen})
				spans = append(spans, fuzzSpan{start, start})
				continue
			}
			a, ok := anchor.Make(body, start, start+length)
			if !ok {
				continue
			}
			edits = append(edits, SuggestedEdit{ID: id, Anchor: *a, Text: text, Status: EditOpen})
			spans = append(spans, fuzzSpan{a.Start, a.End})
		}
		s := &Suggestion{Edits: edits}
		resolved := ResolveEdits(body, s)
		if len(resolved) != len(edits) {
			t.Fatalf("resolved %d of %d edits", len(resolved), len(edits))
		}
		for i, r := range resolved {
			if r.State != core.AnchorExact || r.Start != spans[i].s || r.End != spans[i].e {
				t.Fatalf("fresh edit %s resolved %s at [%d,%d), made at [%d,%d)", r.ID, r.State, r.Start, r.End, spans[i].s, spans[i].e)
			}
		}

		var kept, allIDs, keptIDs []string
		var keptIdx []int
		anyOverlap := false
		for i := range spans {
			free := true
			for j := 0; j < i; j++ {
				if spansOverlap(spans[i], spans[j]) {
					anyOverlap = true
				}
			}
			for _, k := range keptIdx {
				if spansOverlap(spans[i], spans[k]) {
					free = false
				}
			}
			allIDs = append(allIDs, edits[i].ID)
			if free {
				keptIdx = append(keptIdx, i)
				keptIDs = append(keptIDs, edits[i].ID)
			}
		}
		_ = kept
		if _, _, err := ApplyEdits(body, resolved, allIDs); anyOverlap != errors.Is(err, ErrOverlappingEdits) || (!anyOverlap && err != nil) {
			t.Fatalf("overlap=%v but ApplyEdits of every edit returned %v", anyOverlap, err)
		}

		got, applied, err := ApplyEdits(body, resolved, keptIDs)
		if err != nil || !slices.Equal(applied, keptIDs) {
			t.Fatalf("non-overlapping edits: %v %v", applied, err)
		}
		order := slices.Clone(keptIdx)
		slices.SortStableFunc(order, func(a, b int) int {
			if c := cmp.Compare(spans[a].s, spans[b].s); c != 0 {
				return c
			}
			return cmp.Compare(spans[a].e, spans[b].e)
		})
		ref := slices.Clone(u)
		delta := 0
		for _, k := range order {
			tu := toUnits(edits[k].Text)
			sp := spans[k]
			ref = slices.Concat(ref[:sp.s+delta], tu, ref[sp.e+delta:])
			delta += len(tu) - (sp.e - sp.s)
		}
		if got != fromUnits(ref) {
			t.Fatalf("ApplyEdits = %q, sequential application = %q", got, fromUnits(ref))
		}

		if same, a, err := ApplyEdits(body, resolved, nil); same != body || a != nil || err != nil {
			t.Fatalf("applying no edits changed the body")
		}
		discarded := &Suggestion{Edits: slices.Clone(edits)}
		if err := DiscardEdits(discarded, nil, "u", testBase); err != nil {
			t.Fatal(err)
		}
		if len(ResolveEdits(body, discarded)) != 0 || (len(edits) > 0 && discarded.Status != SuggestionDiscarded) {
			t.Fatalf("discard all left open edits: %+v", discarded)
		}

		marker := ""
		for r := rune(0x2600); marker == ""; r++ {
			m := string(r)
			if strings.Contains(body, m) || slices.ContainsFunc(edits, func(e SuggestedEdit) bool { return strings.Contains(e.Text, m) }) {
				continue
			}
			marker = m
		}
		q := int(insAt) % (n + 1)
		for range 4 {
			if isSurrogateSplit(u, q) {
				q--
			}
			for _, k := range keptIdx {
				if sp := spans[k]; sp.s < q && q < sp.e {
					q = sp.s
				}
			}
		}
		newBody := fromUnits(slices.Concat(u[:q], toUnits(marker), u[q:]))
		moved := &Suggestion{}
		for _, k := range keptIdx {
			moved.Edits = append(moved.Edits, edits[k])
		}
		RemapSuggestion(body, newBody, moved)
		res2 := ResolveEdits(newBody, moved)
		for i, r := range res2 {
			sp := spans[keptIdx[i]]
			ws, we := sp.s, sp.e
			if sp.s == sp.e {
				if sp.s > q {
					ws, we = ws+1, we+1
				}
			} else {
				if sp.s >= q {
					ws++
				}
				if sp.e > q {
					we++
				}
			}
			if r.Orphaned() || r.Start != ws || r.End != we {
				t.Fatalf("edit %s at [%d,%d) after inserting %q at %d resolved %s at [%d,%d), want [%d,%d)\nbody %q",
					r.ID, sp.s, sp.e, marker, q, r.State, r.Start, r.End, ws, we, body)
			}
		}
		got2, _, err := ApplyEdits(newBody, res2, keptIDs)
		if err != nil {
			t.Fatalf("apply after unrelated insertion: %v", err)
		}
		if strings.Replace(got2, marker, "", 1) != got {
			t.Fatalf("after an unrelated insertion the edits produce %q, want %q plus the marker", got2, got)
		}
	})
}
