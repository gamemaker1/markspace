package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gamemaker1/markspace/internal/core"
)

// Options configures a Server.
type Options struct {
	// Logger receives unmapped handler errors.
	Logger *slog.Logger
	// Auth resolves the actor of every request.
	Auth *Authenticator
	// Mount registers the module routes into the API mux. The module
	// registry provides it. Without it the server answers every path with
	// the JSON 404 body.
	Mount func(mux *http.ServeMux) error
}

// Server serves the JSON API. The routes come from the built-in modules
// through Options.Mount. The server adds the authentication middleware and
// the JSON 404 for unclaimed paths under /api/.
type Server struct {
	opts Options
	mux  *http.ServeMux
}

// NewServer validates the options and mounts the module routes. It returns an
// error when Logger or Auth is nil.
func NewServer(opts Options) (*Server, error) {
	if opts.Logger == nil {
		return nil, errors.New("api: Options.Logger is nil")
	}
	if opts.Auth == nil {
		return nil, errors.New("api: Options.Auth is nil")
	}
	s := &Server{opts: opts, mux: http.NewServeMux()}
	// Unknown paths under /api answer with the JSON error body.
	s.mux.HandleFunc("/api/", s.notFound)
	if opts.Mount != nil {
		if err := opts.Mount(s.mux); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Handler returns the API routes wrapped in the authentication middleware, so
// every handler can read an actor from the request context.
func (s *Server) Handler() http.Handler {
	return s.opts.Auth.Middleware(s.mux)
}

// notFound serves any path under /api that no route claims.
func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	WriteError(w, r, s.opts.Logger, core.ErrNotFound)
}
