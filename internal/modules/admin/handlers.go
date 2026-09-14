package admin

import (
	"net/http"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
)

// indexedResponse reports how many records a reindex touched.
type indexedResponse struct {
	Indexed int `json:"indexed"`
}

// getSettings serves GET /api/settings.
func (m *Module) getSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := m.Backend.GetSettings(r.Context(), api.ActorFrom(r.Context()))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, settings)
}

// updateSettings serves PATCH /api/settings.
func (m *Module) updateSettings(w http.ResponseWriter, r *http.Request) {
	var in core.Settings
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	settings, err := m.Backend.UpdateSettings(r.Context(), api.ActorFrom(r.Context()), &in)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, settings)
}

// stats serves GET /api/stats.
func (m *Module) stats(w http.ResponseWriter, r *http.Request) {
	out, err := m.Backend.Stats(r.Context(), api.ActorFrom(r.Context()))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, out)
}

// reindex serves POST /api/reindex.
func (m *Module) reindex(w http.ResponseWriter, r *http.Request) {
	n, err := m.Backend.Reindex(r.Context(), api.ActorFrom(r.Context()))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, indexedResponse{Indexed: n})
}

// listSystemTokens serves GET /api/admin/tokens.
func (m *Module) listSystemTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := m.Backend.ListSystemTokens(r.Context(), api.ActorFrom(r.Context()))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, tokens)
}

// revokeSystemToken serves DELETE /api/admin/tokens/{id}.
func (m *Module) revokeSystemToken(w http.ResponseWriter, r *http.Request) {
	if err := m.Backend.RevokeSystemToken(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id")); err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}
