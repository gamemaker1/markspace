package app

import (
	"context"
	"errors"
	"fmt"
	"html"
	"sort"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// rrfK is the rank offset of reciprocal rank fusion.
const rrfK = 60

// snippetRunes caps a semantic search snippet.
const snippetRunes = 200

// errNoEmbeddings reports that no model can serve the embeddings route.
var errNoEmbeddings = fmt.Errorf("no embedding model is available: %w: %w", core.ErrUnavailable, core.ErrUnsupported)

// Search answers a full-text, semantic or hybrid query over the spaces the
// actor may read. Every hit passes through the note grant check.
func (a *App) Search(ctx context.Context, actor *core.Actor, q core.SearchQuery) ([]*core.SearchHit, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	ids, err := a.accessibleSpaceIDs(ctx, actor)
	if err != nil {
		return nil, err
	}
	ids = intersect(ids, q.SpaceIDs)
	if len(ids) == 0 {
		return []*core.SearchHit{}, nil
	}
	if q.Limit <= 0 || q.Limit > 100 {
		q.Limit = 20
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	q.SpaceIDs = ids

	var hits []*core.SearchHit
	switch q.Mode {
	case "", "text":
		hits, err = a.textSearch(ctx, q)
	case "semantic", "hybrid":
		var sem []*core.SearchHit
		sem, err = a.semanticSearch(ctx, actor, q)
		if errors.Is(err, errNoEmbeddings) {
			a.log.Warn("semantic search requested without an embedding provider, using text search", "mode", q.Mode)
			hits, err = a.textSearch(ctx, q)
			break
		}
		if err != nil {
			break
		}
		if q.Mode == "semantic" {
			hits = paginate(sem, q.Offset, q.Limit)
			break
		}
		wide := q
		wide.Offset = 0
		wide.Limit = q.Offset + q.Limit*2
		var text []*core.SearchHit
		text, err = a.textSearch(ctx, wide)
		if err != nil {
			break
		}
		hits = paginate(fuseRanks(text, sem), q.Offset, q.Limit)
	default:
		return nil, core.Invalid("mode", "mode must be text, semantic or hybrid")
	}
	if err != nil {
		return nil, err
	}
	return a.filterHits(ctx, actor, hits)
}

// textSearch runs the full-text queries over notes and files. File hits
// follow the note hits and use the same score scale.
func (a *App) textSearch(ctx context.Context, q core.SearchQuery) ([]*core.SearchHit, error) {
	hits, err := a.db.SearchNotes(ctx, q.SpaceIDs, q)
	if err != nil {
		return nil, fmt.Errorf("search notes: %w", err)
	}
	for _, h := range hits {
		h.Kind = core.KindNote
	}
	if q.IncludeTrash {
		return hits, nil
	}
	files, err := a.db.SearchFiles(ctx, q.SpaceIDs, q)
	if err != nil {
		return nil, fmt.Errorf("search files: %w", err)
	}
	for _, h := range files {
		if h == nil || h.File == nil {
			continue
		}
		hits = append(hits, &core.SearchHit{Kind: core.KindFile, File: h.File, Snippet: h.Snippet, Score: h.Score})
	}
	return hits, nil
}

// hitID returns the item id of a search hit.
func hitID(h *core.SearchHit) string {
	switch {
	case h == nil:
		return ""
	case h.Note != nil:
		return h.Note.ID
	case h.File != nil:
		return h.File.ID
	}
	return ""
}

// semanticSearch embeds the query and ranks notes by their best chunk.
func (a *App) semanticSearch(ctx context.Context, actor *core.Actor, q core.SearchQuery) ([]*core.SearchHit, error) {
	if a.ai == nil {
		return nil, errNoEmbeddings
	}
	target, err := a.embeddingTarget(ctx)
	if err != nil {
		return nil, err
	}
	began := time.Now()
	vecs, err := target.provider.Embed(ctx, []string{q.Text}, target.model)
	a.recordEmbed(ctx, actor.UserID(), core.FeatureSearch, "", "", target, []string{q.Text}, began, err)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("embed query returned no vector: %w", core.ErrInvalid)
	}
	chunks, err := a.db.ListEmbeddings(ctx, q.SpaceIDs, target.model)
	if err != nil {
		return nil, fmt.Errorf("list embeddings: %w", err)
	}
	ranked := a.rankChunks(vecs[0], chunks, true)
	noteIDs := make([]string, 0, len(ranked))
	for _, r := range ranked {
		noteIDs = append(noteIDs, r.chunk.NoteID)
	}
	notes, err := a.db.ListNotesByIDs(ctx, noteIDs)
	if err != nil {
		return nil, fmt.Errorf("load notes: %w", err)
	}
	byID := make(map[string]*core.Note, len(notes))
	for _, n := range notes {
		byID[n.ID] = n
	}
	hits := make([]*core.SearchHit, 0, len(ranked))
	for _, r := range ranked {
		n, ok := byID[r.chunk.NoteID]
		if !ok || (n.Deleted && !q.IncludeTrash) {
			continue
		}
		hits = append(hits, &core.SearchHit{
			Kind:    core.KindNote,
			Note:    n,
			Snippet: html.EscapeString(truncateRunes(r.chunk.Text, snippetRunes)),
			Score:   r.score,
		})
	}
	return hits, nil
}

// scoredChunk is one embedding chunk with its similarity to a query.
type scoredChunk struct {
	chunk *core.EmbeddingChunk
	score float64
}

// rankChunks scores chunks against a query vector, best first. With bestPerNote
// set it keeps only the highest scoring chunk of each note.
func (a *App) rankChunks(query []float32, chunks []*core.EmbeddingChunk, bestPerNote bool) []scoredChunk {
	out := make([]scoredChunk, 0, len(chunks))
	best := map[string]int{}
	for _, c := range chunks {
		s := a.ai.Similarity(query, c.Vector)
		// A chunk with no positive similarity shares nothing with the query,
		// so it is not a result and not useful prompt context.
		if s <= 0 {
			continue
		}
		if !bestPerNote {
			out = append(out, scoredChunk{chunk: c, score: s})
			continue
		}
		if i, ok := best[c.NoteID]; ok {
			if s > out[i].score {
				out[i] = scoredChunk{chunk: c, score: s}
			}
			continue
		}
		best[c.NoteID] = len(out)
		out = append(out, scoredChunk{chunk: c, score: s})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	return out
}

// fuseRanks merges two ranked hit lists with reciprocal rank fusion.
func fuseRanks(lists ...[]*core.SearchHit) []*core.SearchHit {
	type fused struct {
		hit   *core.SearchHit
		score float64
		order int
	}
	byID := map[string]*fused{}
	order := 0
	for _, list := range lists {
		for rank, h := range list {
			id := hitID(h)
			if id == "" {
				continue
			}
			f, ok := byID[id]
			if !ok {
				cp := *h
				f = &fused{hit: &cp, order: order}
				order++
				byID[id] = f
			}
			f.score += 1.0 / float64(rrfK+rank+1)
			if f.hit.Snippet == "" {
				f.hit.Snippet = h.Snippet
			}
		}
	}
	all := make([]*fused, 0, len(byID))
	for _, f := range byID {
		all = append(all, f)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return all[i].order < all[j].order
	})
	out := make([]*core.SearchHit, 0, len(all))
	for _, f := range all {
		f.hit.Score = f.score
		out = append(out, f.hit)
	}
	return out
}

// paginate slices a result list.
func paginate(hits []*core.SearchHit, offset, limit int) []*core.SearchHit {
	if offset >= len(hits) {
		return []*core.SearchHit{}
	}
	end := offset + limit
	if end > len(hits) {
		end = len(hits)
	}
	return hits[offset:end]
}

// filterHits drops the hits on items the actor may not view, and fills the
// display fields of file hits.
func (a *App) filterHits(ctx context.Context, actor *core.Actor, hits []*core.SearchHit) ([]*core.SearchHit, error) {
	cache := a.grants(actor)
	spaces := map[string]*core.Space{}
	out := make([]*core.SearchHit, 0, len(hits))
	for _, h := range hits {
		if h == nil {
			continue
		}
		var ref itemRef
		switch {
		case h.Note != nil:
			ref = noteRef(h.Note)
			h.Kind = core.KindNote
		case h.File != nil:
			ref = fileRef(h.File)
			h.Kind = core.KindFile
		default:
			continue
		}
		g, err := cache.item(ctx, ref)
		if err != nil {
			return nil, err
		}
		if !g.Access.AtLeast(core.AccessView) {
			continue
		}
		if h.File != nil {
			sp, ok := spaces[h.File.SpaceID]
			if !ok {
				sp, _ = a.space(ctx, h.File.SpaceID)
				spaces[h.File.SpaceID] = sp
			}
			decorateFile(sp, h.File)
		}
		out = append(out, h)
	}
	return out, nil
}

// Suggest powers wikilink and quick-open autocomplete over the notes the actor
// may view.
func (a *App) Suggest(ctx context.Context, actor *core.Actor, prefix string, limit int) ([]*core.Note, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	ids, err := a.accessibleSpaceIDs(ctx, actor)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []*core.Note{}, nil
	}
	notes, err := a.db.SuggestNotes(ctx, ids, prefix, limit)
	if err != nil {
		return nil, fmt.Errorf("suggest notes: %w", err)
	}
	return a.visibleNotes(ctx, actor, noAccess(), notes)
}

// truncateRunes cuts s to at most n runes.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
