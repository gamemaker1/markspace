package search

import (
	"net/http"
	"net/url"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
)

// Search defaults applied to the query string.
const (
	searchDefaultLimit = 25
	searchMaxLimit     = 100
)

// searchQueryFrom builds a search query from the request query string. The
// limit defaults to 25 and is capped at 100, and malformed numbers fall back
// to the default.
func searchQueryFrom(v url.Values) core.SearchQuery {
	return core.SearchQuery{
		Text:         v.Get("q"),
		Mode:         v.Get("mode"),
		SpaceIDs:     v["space"],
		Tags:         v["tag"],
		AuthorID:     v.Get("author"),
		IncludeTrash: api.BoolParam(v.Get("trash")),
		Limit:        api.IntParam(v.Get("limit"), searchDefaultLimit, searchMaxLimit),
		Offset:       api.IntParam(v.Get("offset"), 0, 0),
	}
}

// search serves GET /api/search.
func (m *Module) search(w http.ResponseWriter, r *http.Request) {
	hits, err := m.Backend.Search(r.Context(), api.ActorFrom(r.Context()), searchQueryFrom(r.URL.Query()))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, hits)
}

// suggest serves GET /api/suggest, the wikilink and quick-open autocomplete.
func (m *Module) suggest(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	notes, err := m.Backend.Suggest(r.Context(), api.ActorFrom(r.Context()),
		q.Get("q"), api.IntParam(q.Get("limit"), 10, 50))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, notes)
}
