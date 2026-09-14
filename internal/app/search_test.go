package app

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// hitTitles lists the note titles and file names of hits, sorted.
func hitTitles(hits []*core.SearchHit) string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		switch {
		case h.Note != nil:
			out = append(out, h.Note.Title)
		case h.File != nil:
			out = append(out, h.File.Name)
		}
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func TestSearchFiltersByAccess(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	spA := h.space(owner, "Alpha Space")
	spB := h.space(owner, "Beta Space")
	h.note(owner, spA, "", "Alpha plan", "alpha")
	h.note(owner, spB, "", "Alpha secret", "alpha")
	shared := h.note(owner, spB, "", "Alpha shared", "alpha")
	kids := h.folder(owner, spB, "", "Kids")
	h.note(owner, spB, kids.ID, "Alpha kid", "alpha")

	reader := h.user("Reader", false)
	h.member(spA, reader, core.RoleViewer)
	h.share(shared.ID, reader, core.AccessView, false)

	cases := []struct {
		name  string
		actor *core.Actor
		q     core.SearchQuery
		want  string
		err   error
	}{
		{"owner sees all", owner, core.SearchQuery{Text: "alpha"}, "Alpha kid,Alpha plan,Alpha secret,Alpha shared", nil},
		{"reader sees member space and share", reader, core.SearchQuery{Text: "alpha"}, "Alpha plan,Alpha shared", nil},
		{"reader restricted to shared space", reader, core.SearchQuery{Text: "alpha", SpaceIDs: []string{spB.ID}}, "Alpha shared", nil},
		{"stranger sees nothing", h.user("Stranger", false), core.SearchQuery{Text: "alpha"}, "", nil},
		{"link actor is refused", &core.Actor{Link: &core.PublicLink{NoteID: shared.ID}}, core.SearchQuery{Text: "alpha"}, "", core.ErrForbidden},
		{"anonymous is refused", nil, core.SearchQuery{Text: "alpha"}, "", core.ErrUnauthorized},
		{"bad mode", owner, core.SearchQuery{Text: "alpha", Mode: "fuzzy"}, "", core.ErrInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hits, err := h.app.Search(h.ctx, tc.actor, tc.q)
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("err = %v, want %v", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := hitTitles(hits); got != tc.want {
				t.Fatalf("hits = %q, want %q", got, tc.want)
			}
		})
	}

	// A share on the folder with children reaches the note inside it.
	h.share(kids.ID, reader, core.AccessView, true)
	hits, err := h.app.Search(h.ctx, reader, core.SearchQuery{Text: "kid"})
	if err != nil || hitTitles(hits) != "Alpha kid" {
		t.Fatalf("inherited share search = %q, %v", hitTitles(hits), err)
	}
}

// TestSearchReturnsFileHits checks that extracted file text is searchable,
// that file hits follow note hits with kind file, and that access applies.
func TestSearchReturnsFileHits(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	reader := h.user("Reader", false)
	sp := h.space(owner, "Docs")
	private := h.space(owner, "Private")
	h.member(sp, reader, core.RoleViewer)
	h.note(owner, sp, "", "Walrus plan", "walrus notes")
	report := h.upload(owner, sp, "", "report.txt", "the walrus census")
	h.upload(owner, private, "", "secret.txt", "walrus salaries")
	h.app.drainFileQueues(h.ctx)

	hits, err := h.app.Search(h.ctx, reader, core.SearchQuery{Text: "census"})
	if err != nil || len(hits) != 1 || hits[0].Kind != core.KindFile || hits[0].File == nil || hits[0].File.ID != report.ID {
		t.Fatalf("body search hits = %+v, %v", hits, err)
	}
	if hits[0].File.URL == "" || hits[0].File.SpaceSlug != sp.Slug {
		t.Fatalf("file hit not decorated: %+v", hits[0].File)
	}

	hits, err = h.app.Search(h.ctx, reader, core.SearchQuery{Text: "walrus"})
	if err != nil {
		t.Fatal(err)
	}
	if got := hitTitles(hits); got != "Walrus plan,report.txt" {
		t.Fatalf("reader hits = %q", got)
	}
	if hits[0].Kind != core.KindNote || hits[len(hits)-1].Kind != core.KindFile {
		t.Fatalf("file hits must follow note hits: %+v", hits)
	}

	hits, err = h.app.Search(h.ctx, owner, core.SearchQuery{Text: "walrus"})
	if err != nil || hitTitles(hits) != "Walrus plan,report.txt,secret.txt" {
		t.Fatalf("owner hits = %q, %v", hitTitles(hits), err)
	}
}

// The real renderer drops chunks under 20 runes of text, so the bodies below
// are long enough to be embedded.
func TestSemanticAndHybridSearch(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	h.note(owner, sp, "", "Zebra", "zzzz zebra zone zzzz zebra zone zzzz zebra zone")
	h.note(owner, sp, "", "Apple", "aaaa apple aaaa apple aaaa apple aaaa apple")
	private := h.space(owner, "Private")
	h.note(owner, private, "", "Zulu", "zzzz zulu zzzz zulu zzzz zulu zzzz zulu")
	reader := h.user("Reader", false)
	h.member(sp, reader, core.RoleViewer)

	for _, s := range []*core.Space{sp, private} {
		n, err := h.app.ReindexEmbeddings(h.ctx, owner, s.ID)
		if err != nil || n == 0 {
			t.Fatalf("ReindexEmbeddings = %d, %v", n, err)
		}
	}
	if n, err := h.app.ReindexEmbeddings(h.ctx, owner, sp.ID); err != nil || n != 0 {
		t.Fatalf("unchanged reindex = %d, %v", n, err)
	}
	if _, err := h.app.ReindexEmbeddings(h.ctx, reader, sp.ID); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("viewer reindex err = %v", err)
	}

	hits, err := h.app.Search(h.ctx, reader, core.SearchQuery{Text: "zzzz", Mode: "semantic"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Note.Title != "Zebra" {
		t.Fatalf("semantic top hit = %q", hitTitles(hits))
	}
	for _, hit := range hits {
		if hit.Note.Title == "Zulu" {
			t.Fatal("semantic search leaked a note from a private space")
		}
	}

	hybrid, err := h.app.Search(h.ctx, reader, core.SearchQuery{Text: "zebra", Mode: "hybrid"})
	if err != nil || len(hybrid) == 0 || hybrid[0].Note == nil || hybrid[0].Note.Title != "Zebra" {
		t.Fatalf("hybrid = %q, %v", hitTitles(hybrid), err)
	}

	h.cfg.AI.Providers[0].EmbedModel = ""
	fallback, err := h.app.Search(h.ctx, reader, core.SearchQuery{Text: "apple", Mode: "semantic"})
	if err != nil || hitTitles(fallback) != "Apple" {
		t.Fatalf("fallback = %q, %v", hitTitles(fallback), err)
	}
}

func TestFuseRanks(t *testing.T) {
	n := func(id string) *core.SearchHit { return &core.SearchHit{Note: &core.Note{ID: id, Title: id}} }
	text := []*core.SearchHit{n("a"), n("b"), n("c")}
	sem := []*core.SearchHit{n("c"), n("a"), n("d")}
	out := fuseRanks(text, sem)
	ids := make([]string, 0, len(out))
	for _, h := range out {
		ids = append(ids, h.Note.ID)
	}
	if got := strings.Join(ids, ","); got != "a,c,b,d" {
		t.Fatalf("fused order = %s", got)
	}
	want := 1.0/61 + 1.0/62
	if diff := out[0].Score - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("score = %v, want %v", out[0].Score, want)
	}
}

func TestSemanticSearchDropsUnrelatedNotes(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	h.note(owner, sp, "", "Zebra", "zzzz zebra zone zzzz zebra zone zzzz zebra zone")
	h.note(owner, sp, "", "Moon", "moon moon moon moon moon moon moon")
	if _, err := h.app.ReindexEmbeddings(h.ctx, owner, sp.ID); err != nil {
		t.Fatalf("ReindexEmbeddings: %v", err)
	}

	// The fake embeds letter counts, so "zzzz" shares no letter with
	// "moon moon" and scores zero against it.
	hits, err := h.app.Search(h.ctx, owner, core.SearchQuery{Text: "zzzz", Mode: "semantic"})
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range hits {
		if hit.Note.Title == "Moon" {
			t.Fatalf("semantic search returned a note with zero similarity: %q", hitTitles(hits))
		}
		if hit.Score <= 0 {
			t.Fatalf("hit %q has non-positive score %v", hit.Note.Title, hit.Score)
		}
	}
	if len(hits) == 0 || hits[0].Note.Title != "Zebra" {
		t.Fatalf("semantic top hit = %q, want Zebra", hitTitles(hits))
	}

	// A query sharing nothing with any note returns no semantic hits at all.
	none, err := h.app.Search(h.ctx, owner, core.SearchQuery{Text: "qqqq", Mode: "semantic"})
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("unrelated query returned %q", hitTitles(none))
	}
}
