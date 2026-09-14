// Package suggestions is the built-in module for suggested edits. It serves
// the suggestion routes of docs/plugins.md over core.SuggestionService and declares
// the Suggestions rail tab, which the editor renders.
package suggestions

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/module"
)

// Module serves the suggestion routes.
type Module struct {
	backend core.Backend
	log     *slog.Logger
}

// New returns the module.
func New(backend core.Backend, logger *slog.Logger) *Module {
	if logger == nil {
		logger = slog.Default()
	}
	return &Module{backend: backend, log: logger}
}

// Manifest implements module.Module.
func (m *Module) Manifest() module.Manifest {
	return module.Manifest{ID: "suggestions", Name: "Suggestions", Builtin: true}
}

// Register implements module.Module.
func (m *Module) Register(r *module.Registry) error {
	r.API("GET /api/notes/{id}/suggestions", m.list)
	r.API("POST /api/notes/{id}/suggestions", m.create)
	r.API("GET /api/suggestions", m.inbox)
	r.API("GET /api/suggestions/{id}", m.get)
	r.API("POST /api/suggestions/{id}/accept", m.accept)
	r.API("POST /api/suggestions/{id}/discard", m.discard)
	r.API("DELETE /api/suggestions/{id}", m.withdraw)
	r.Panel(module.Panel{
		ID: "suggestions", Title: "Suggestions", Icon: "suggestion", Where: "rail",
		Contexts: []string{"note"}, Builtin: "suggestions", Order: 25, MinAccess: core.AccessView,
	})
	return nil
}

func (m *Module) fail(w http.ResponseWriter, r *http.Request, err error) {
	api.WriteError(w, r, m.log, err)
}

type listResponse struct {
	Items []*core.Suggestion `json:"items"`
}

func items(list []*core.Suggestion) listResponse {
	if list == nil {
		list = []*core.Suggestion{}
	}
	return listResponse{Items: list}
}

// statuses splits a comma-separated status query value.
func statuses(raw string) []string {
	var out []string
	for _, s := range strings.Split(raw, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// list serves GET /api/notes/{id}/suggestions?status=open.
func (m *Module) list(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	list, err := m.backend.ListNoteSuggestions(ctx, api.ActorFrom(ctx), r.PathValue("id"), statuses(r.URL.Query().Get("status")))
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, items(list))
}

type createRequest struct {
	Title     string                     `json:"title"`
	Rationale string                     `json:"rationale"`
	Edits     []core.SuggestionEditInput `json:"edits"`
}

// create serves POST /api/notes/{id}/suggestions.
func (m *Module) create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in createRequest
	if err := api.Decode(r, &in); err != nil {
		m.fail(w, r, err)
		return
	}
	s, err := m.backend.CreateSuggestion(ctx, api.ActorFrom(ctx), core.SuggestionInput{
		NoteID: r.PathValue("id"), Title: in.Title, Rationale: in.Rationale, Edits: in.Edits,
	})
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, s)
}

// inbox serves GET /api/suggestions?space=&author=&status=&limit=.
func (m *Module) inbox(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	list, err := m.backend.SuggestionInbox(ctx, api.ActorFrom(ctx), core.SuggestionQuery{
		SpaceID: q.Get("space"), AuthorID: q.Get("author"), Status: q.Get("status"), Limit: intValue(q.Get("limit")),
	})
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, items(list))
}

func intValue(s string) int {
	n := 0
	for _, c := range strings.TrimSpace(s) {
		if c < '0' || c > '9' || n > 1<<20 {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// get serves GET /api/suggestions/{id}.
func (m *Module) get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, err := m.backend.GetSuggestion(ctx, api.ActorFrom(ctx), r.PathValue("id"))
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, s)
}

type editsRequest struct {
	EditIDs []string `json:"editIds"`
}

func decodeEdits(r *http.Request) ([]string, error) {
	var in editsRequest
	if r.ContentLength == 0 {
		return nil, nil
	}
	if err := api.Decode(r, &in); err != nil {
		return nil, err
	}
	return in.EditIDs, nil
}

// accept serves POST /api/suggestions/{id}/accept {editIds?}.
func (m *Module) accept(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ids, err := decodeEdits(r)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	res, err := m.backend.AcceptSuggestion(ctx, api.ActorFrom(ctx), r.PathValue("id"), ids)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, res)
}

// discard serves POST /api/suggestions/{id}/discard {editIds?}.
func (m *Module) discard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ids, err := decodeEdits(r)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	s, err := m.backend.DiscardSuggestion(ctx, api.ActorFrom(ctx), r.PathValue("id"), ids)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, s)
}

// withdraw serves DELETE /api/suggestions/{id} for the author.
func (m *Module) withdraw(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := m.backend.WithdrawSuggestion(ctx, api.ActorFrom(ctx), r.PathValue("id")); err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}
