package web

import (
	"net/http"
	"net/url"
	"time"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
)

// handleAppShell serves the client-rendered application shell. The document
// carries no user data, because the service worker caches one copy and serves
// it to every shell route for whoever signs in on the device.
func (s *Server) handleAppShell(w http.ResponseWriter, r *http.Request) {
	p := s.anonymousPage(r, "")
	w.Header().Set("Cache-Control", "no-cache")
	s.Render(w, r, http.StatusOK, "shell", p)
}

// handleOffline serves the page the service worker shows for a server-only
// route without a connection. Like the shell it holds no user data.
func (s *Server) handleOffline(w http.ResponseWriter, r *http.Request) {
	p := s.anonymousPage(r, "Offline")
	w.Header().Set("Cache-Control", "no-cache")
	s.Render(w, r, http.StatusOK, "offline", p)
}

// anonymousPage builds page data that ignores the signed-in actor. Only the
// instance name is read from the settings, so a cached copy of the output is
// safe to show to any account.
func (s *Server) anonymousPage(r *http.Request, title string) *Page {
	p := &Page{
		Title:        title,
		InstanceName: s.instanceName,
		BaseURL:      s.auth.Origin(r, s.baseURL),
		DevMode:      s.devMode,
		Now:          time.Now().UTC(),
		Actor:        &core.Actor{},
		Expanded:     map[string]bool{},
		RequestPath:  r.URL.Path,
		Query:        url.Values{},
		Data:         map[string]any{},
	}
	if st, err := s.backend.GetSettings(r.Context(), api.ActorFrom(r.Context())); err == nil && st != nil && st.InstanceName != "" {
		p.InstanceName = st.InstanceName
	}
	return p
}
