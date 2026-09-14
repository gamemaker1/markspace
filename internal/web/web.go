// Package web serves the server-rendered HTML interface over core.Backend.
// Templates, stylesheets and scripts are embedded in the binary, so the
// package needs no build step and no external asset directory.
package web

import (
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"sync"
	"time"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
)

// embedded holds the templates and static assets compiled into the binary.
// Page templates sit under templates, and the shared partials under
// templates/partials.
//
//go:embed templates static/*.css static/css/*.css static/*.js static/js/*.js static/js/ai/*.js static/js/viewers/*.js static/icons/* static/vendor
var embedded embed.FS

// devSourceDir is the package directory, relative to the process working
// directory. DevMode reloads templates and static files from it so an edit
// shows up without a rebuild. NewServer falls back to the embedded copy when
// the directory is not present.
const devSourceDir = "internal/web"

// Options configures a Server.
type Options struct {
	// Backend is the application surface. It is required.
	Backend core.Backend
	// Logger receives request and render diagnostics.
	Logger *slog.Logger
	// Auth resolves actors and writes the session cookie.
	Auth *api.Authenticator
	// SessionTTL is how long a session cookie lives.
	SessionTTL time.Duration
	// BaseURL is the externally reachable root, used to build share links.
	BaseURL string
	// InstanceName is the fallback title when instance settings are missing.
	InstanceName string
	// MaxUpload caps a multipart form in bytes.
	MaxUpload int64
	// DevMode reloads templates and static files from disk on each request.
	DevMode bool
	// MarkdownCSS is the syntax-highlighting stylesheet from the renderer,
	// served at /static/highlight.css.
	MarkdownCSS string
	// Mount, when set, registers more page routes into the web mux. The
	// module registry uses it.
	Mount func(mux *http.ServeMux) error
}

// Server renders the HTML interface. Build one with NewServer and mount
// Handler on a ServeMux.
type Server struct {
	backend      core.Backend
	log          *slog.Logger
	auth         *api.Authenticator
	sessionTTL   time.Duration
	baseURL      string
	instanceName string
	maxUpload    int64
	devMode      bool
	markdownCSS  string
	mount        func(mux *http.ServeMux) error

	// root contains the templates and static directories.
	root fs.FS

	mu    sync.RWMutex
	pages map[string]*template.Template
}

// sessionCookieName is the cookie the shared authenticator reads and writes.
const sessionCookieName = api.SessionCookie

// Default limits applied when Options leaves a field zero.
const (
	defaultSessionTTL = 30 * 24 * time.Hour
	defaultMaxUpload  = 50 << 20
)

// NewServer validates the options, resolves the asset root and parses every
// page template. It returns an error when a template fails to parse.
func NewServer(opts Options) (*Server, error) {
	if opts.Backend == nil {
		return nil, errors.New("web: Options.Backend is required")
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	auth := opts.Auth
	if auth == nil {
		auth = &api.Authenticator{Backend: opts.Backend, Logger: logger}
	}
	s := &Server{
		backend:      opts.Backend,
		log:          logger,
		auth:         auth,
		sessionTTL:   opts.SessionTTL,
		baseURL:      opts.BaseURL,
		instanceName: opts.InstanceName,
		maxUpload:    opts.MaxUpload,
		devMode:      opts.DevMode,
		markdownCSS:  opts.MarkdownCSS,
		mount:        opts.Mount,
		root:         assetRoot(opts.DevMode, logger),
	}
	if s.sessionTTL <= 0 {
		s.sessionTTL = defaultSessionTTL
	}
	if s.maxUpload <= 0 {
		s.maxUpload = defaultMaxUpload
	}
	if s.instanceName == "" {
		s.instanceName = "Markspace"
	}
	pages, err := s.parsePages(s.root)
	if err != nil {
		return nil, err
	}
	s.pages = pages
	return s, nil
}

// assetRoot returns the filesystem the templates and static files come from.
// In DevMode it prefers devSourceDir on disk.
func assetRoot(dev bool, logger *slog.Logger) fs.FS {
	if !dev {
		return embedded
	}
	if _, err := os.Stat(path.Join(devSourceDir, "templates", "base.html")); err == nil {
		logger.Info("web: serving assets from disk", "dir", devSourceDir)
		return os.DirFS(devSourceDir)
	}
	logger.Warn("web: dev mode asset directory not found, using embedded assets", "dir", devSourceDir)
	return embedded
}

// Handler returns the router for every web route. Logging, panic recovery and
// actor resolution wrap the whole tree.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	if s.mount != nil {
		if err := s.mount(mux); err != nil {
			s.log.Error("web: mount module routes failed", "err", err)
		}
	}
	return api.Chain(mux,
		api.Recover(s.log),
		api.Logging(s.log),
		s.auth.Middleware,
	)
}

// routes registers every pattern from the web section of docs/routes.md.
func (s *Server) routes(mux *http.ServeMux) {
	// Entry points and authentication.
	mux.HandleFunc("GET /{$}", s.handleHome)
	mux.HandleFunc("GET /setup", s.handleSetup)
	mux.HandleFunc("POST /setup", s.handleSetupPost)
	mux.HandleFunc("GET /login", s.handleLogin)
	mux.HandleFunc("POST /login", s.handleLoginPost)
	mux.HandleFunc("GET /signup", s.handleSignup)
	mux.HandleFunc("POST /signup", s.handleSignupPost)
	mux.HandleFunc("POST /logout", s.handleLogout)

	// Spaces and notes.
	mux.HandleFunc("GET /spaces", s.handleSpaces)
	mux.HandleFunc("POST /spaces", s.handleSpacesPost)
	mux.HandleFunc("GET /s/{space}", s.handleSpaceHome)
	mux.HandleFunc("POST /s/{space}", s.handleSpaceHomePost)
	mux.HandleFunc("GET /s/{space}/t/{tag}", s.handleTag)
	mux.HandleFunc("GET /s/{space}/trash", s.handleTrash)
	mux.HandleFunc("POST /s/{space}/trash", s.handleTrashPost)
	// The files view exists only in the client shell, so the server answers
	// it with the shell document until the service worker takes over.
	mux.HandleFunc("GET /s/{space}/files", s.handleAppShell)
	mux.HandleFunc("GET /s/{space}/f/{path...}", s.handleAppShell)
	mux.HandleFunc("GET /s/{space}/d/{path...}", s.handleAppShell)
	mux.HandleFunc("GET /s/{space}/settings", s.handleSpaceSettings)
	mux.HandleFunc("POST /s/{space}/settings", s.handleSpaceSettingsPost)
	mux.HandleFunc("GET /s/{space}/n/{path...}", s.handleNote)
	mux.HandleFunc("POST /s/{space}/n/{path...}", s.handleNotePost)
	mux.HandleFunc("GET /n/{noteId}", s.handlePermalink)
	mux.HandleFunc("GET /i/{id}", s.handleItemPermalink)

	// Browsing.
	mux.HandleFunc("GET /search", s.handleSearch)
	mux.HandleFunc("GET /starred", s.handleStarred)
	mux.HandleFunc("GET /recent", s.handleRecent)
	mux.HandleFunc("GET /shared", s.handleShared)

	// User settings.
	mux.HandleFunc("GET /settings", s.handleSettingsIndex)
	mux.HandleFunc("GET /settings/sessions", s.handleSessions)
	mux.HandleFunc("POST /settings/sessions", s.handleSessionsPost)
	mux.HandleFunc("GET /settings/offline", s.handleOfflineFiles)
	mux.HandleFunc("GET /settings/profile", s.handleProfile)
	mux.HandleFunc("POST /settings/profile", s.handleProfilePost)
	mux.HandleFunc("GET /settings/tokens", s.handleTokens)
	mux.HandleFunc("POST /settings/tokens", s.handleTokensPost)
	mux.HandleFunc("GET /settings/ai", s.handleUserAI)
	mux.HandleFunc("POST /settings/ai", s.handleUserAIPost)
	mux.HandleFunc("GET /settings/usage", s.handleUsage)

	// Administration.
	mux.HandleFunc("GET /admin", s.handleAdmin)
	mux.HandleFunc("GET /admin/users", s.handleAdminUsers)
	mux.HandleFunc("POST /admin/users", s.handleAdminUsersPost)
	mux.HandleFunc("GET /admin/ai", s.handleAdminAI)
	mux.HandleFunc("POST /admin/ai", s.handleAdminAIPost)
	mux.HandleFunc("GET /admin/usage", s.handleAdminUsage)
	mux.HandleFunc("POST /admin/usage", s.handleAdminUsagePost)
	mux.HandleFunc("GET /admin/settings", s.handleAdminSettings)
	mux.HandleFunc("POST /admin/settings", s.handleAdminSettingsPost)

	// Public and unauthenticated surfaces.
	mux.HandleFunc("GET /p/{token}", s.handlePublicLink)
	mux.HandleFunc("POST /p/{token}", s.handlePublicLink)
	mux.HandleFunc("GET /p/{token}/i/{id}", s.handlePublicChild)
	mux.HandleFunc("GET /p/{token}/raw/{id}/{name}", s.handlePublicRaw)
	mux.HandleFunc("GET /p/{token}/thumb/{id}", s.handlePublicThumb)
	mux.HandleFunc("GET /files/{space}/{id}/{name}", s.handleFile)
	mux.HandleFunc("GET /thumbs/{space}/{id}", s.handleThumb)
	mux.HandleFunc("GET /static/highlight.css", s.handleHighlightCSS)
	mux.HandleFunc("GET /static/{path...}", s.handleStatic)
	mux.HandleFunc("GET /healthz", s.handleHealthz)

	// Anything else is a missing page.
	// Progressive web app. The service worker must be served from the root so
	// its scope covers every page.
	mux.HandleFunc("GET /sw.js", s.handleServiceWorker)
	mux.HandleFunc("GET /manifest.webmanifest", s.handleManifest)
	mux.HandleFunc("GET /app-shell", s.handleAppShell)
	mux.HandleFunc("GET /offline", s.handleOffline)

	mux.HandleFunc("/", s.handleNotFound)
}

// handleHome redirects to the first space the actor can see. A fresh instance
// goes to /setup and an anonymous visitor to /login.
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if need, err := s.backend.NeedsSetup(ctx); err == nil && need {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	actor := api.ActorFrom(ctx)
	if !actor.IsUser() {
		s.redirectToLogin(w, r)
		return
	}
	spaces, err := s.backend.ListSpaces(ctx, actor)
	if err == nil && len(spaces) > 0 {
		http.Redirect(w, r, spaceURL(spaces[0]), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/spaces", http.StatusSeeOther)
}

// handleNotFound renders the 404 page for an unrouted path.
func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	s.fail(w, r, core.ErrNotFound)
}

// RenderModulePage renders a module page inside the application layout. It
// implements api.PageRenderer.
func (s *Server) RenderModulePage(w http.ResponseWriter, r *http.Request, spec api.PageSpec) {
	p := s.newPage(r, spec.Title, spec.Nav)
	p.Set("Module", spec)
	status := spec.Status
	if status == 0 {
		status = http.StatusOK
	}
	// An administration page shows the admin NavList pane instead of the
	// notes sidebar (docs/primer.md 5.2).
	name := "module"
	if spec.AdminNav {
		name = "admin/module"
	}
	s.Render(w, r, status, name, p)
}

// RenderError renders the error page for err. An anonymous visitor goes to
// the login page. It implements api.PageRenderer.
func (s *Server) RenderError(w http.ResponseWriter, r *http.Request, err error) {
	s.fail(w, r, err)
}

var _ api.PageRenderer = (*Server)(nil)
