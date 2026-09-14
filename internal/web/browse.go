package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gamemaker1/markspace/internal/core"
)

// searchPageSize is how many hits one page of results shows.
const searchPageSize = 25

// searchModes are the tabs above the result list.
var searchModes = []string{"text", "semantic", "hybrid"}

// handleSearch renders search results. The mode tabs pick full text,
// semantic or hybrid matching, and the filters narrow by space and tag.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	query := r.URL.Query()
	text := strings.TrimSpace(query.Get("q"))
	mode := strings.TrimSpace(query.Get("mode"))
	if !validMode(mode) {
		mode = "text"
	}
	offset := formInt(query.Get("offset"), 0)
	if offset < 0 {
		offset = 0
	}
	p := s.newPage(r, "Search", "search")
	p.Set("Modes", searchModes)
	p.Set("Mode", mode)
	p.Set("Search", text)
	p.Set("SpaceFilter", query.Get("space"))
	p.Set("TagFilter", query.Get("tag"))

	if spaceSlug := strings.TrimSpace(query.Get("space")); spaceSlug != "" {
		if space, err := s.backend.GetSpace(ctx, actor, spaceSlug); err == nil {
			p.Space = space
			if tags, err := s.backend.ListTags(ctx, actor, space.ID); err == nil {
				p.Set("Tags", tags)
			}
		}
	}
	if text == "" {
		s.Render(w, r, http.StatusOK, "search", p)
		return
	}
	q := core.SearchQuery{
		Text:   text,
		Mode:   mode,
		Limit:  searchPageSize,
		Offset: offset,
	}
	if p.Space != nil {
		q.SpaceIDs = []string{p.Space.ID}
	}
	if tag := strings.TrimSpace(query.Get("tag")); tag != "" {
		q.Tags = []string{tag}
	}
	hits, err := s.backend.Search(ctx, actor, q)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p.Set("Hits", hits)
	p.Set("Pagination", searchPagination(r, offset, searchPageSize, len(hits)))
	s.Render(w, r, http.StatusOK, "search", p)
}

// validMode reports whether mode is one of the offered search modes.
func validMode(mode string) bool {
	for _, m := range searchModes {
		if m == mode {
			return true
		}
	}
	return false
}

// searchPagination builds result page links, keeping every filter.
func searchPagination(r *http.Request, offset, limit, count int) *Pagination {
	page := &Pagination{Offset: offset, Limit: limit, Count: count}
	link := func(off int) string {
		v := r.URL.Query()
		if off > 0 {
			v.Set("offset", strconv.Itoa(off))
		} else {
			v.Del("offset")
		}
		return "/search?" + v.Encode()
	}
	if offset > 0 {
		prev := offset - limit
		if prev < 0 {
			prev = 0
		}
		page.PrevURL = link(prev)
	}
	if count >= limit {
		page.NextURL = link(offset + limit)
	}
	return page
}

// handleStarred lists the notes the actor starred.
func (s *Server) handleStarred(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	p := s.newPage(r, "Starred", "starred")
	notes, err := s.backend.ListStarred(r.Context(), actor)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p.Set("Notes", notes)
	p.Set("Empty", "Star a note to keep it here.")
	s.Render(w, r, http.StatusOK, "starred", p)
}

// handleRecent lists the notes the actor changed most recently.
func (s *Server) handleRecent(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	limit := formInt(r.URL.Query().Get("limit"), 50)
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	p := s.newPage(r, "Recent", "recent")
	notes, err := s.backend.RecentNotes(r.Context(), actor, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p.Set("Notes", notes)
	p.Set("Empty", "Notes you open or edit show up here.")
	s.Render(w, r, http.StatusOK, "recent", p)
}

// handleShared lists the notes other people shared with the actor.
func (s *Server) handleShared(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	p := s.newPage(r, "Shared with me", "shared")
	shares, err := s.backend.ListSharedWithMe(r.Context(), actor)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p.Set("Shares", shares)
	p.Set("Empty", "Nothing has been shared with you yet.")
	s.Render(w, r, http.StatusOK, "shared", p)
}
