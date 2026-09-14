package api

import (
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// Default Env values applied by Env.WithDefaults when a field is zero.
const (
	DefaultSessionTTL = 30 * 24 * time.Hour
	DefaultMaxUpload  = 1024 << 20
)

// Env is what the HTTP handlers of the built-in modules share. A module keeps
// one Env and reaches the application only through Backend.
type Env struct {
	// Backend is the only application dependency the handlers use.
	Backend core.Backend
	// Logger receives unmapped handler errors and stream failures.
	Logger *slog.Logger
	// Auth resolves actors and writes the session cookie.
	Auth *Authenticator
	// SessionTTL is the lifetime given to a session cookie on login.
	SessionTTL time.Duration
	// MaxUpload is the largest accepted upload body in bytes.
	MaxUpload int64
	// BaseURL is the external origin used to build absolute URLs, without a
	// trailing slash.
	BaseURL string
}

// WithDefaults returns a copy with a default logger, session lifetime and
// upload limit filled in where they are zero.
func (e Env) WithDefaults() Env {
	if e.Logger == nil {
		e.Logger = slog.Default()
	}
	if e.SessionTTL <= 0 {
		e.SessionTTL = DefaultSessionTTL
	}
	if e.MaxUpload <= 0 {
		e.MaxUpload = DefaultMaxUpload
	}
	for len(e.BaseURL) > 0 && e.BaseURL[len(e.BaseURL)-1] == '/' {
		e.BaseURL = e.BaseURL[:len(e.BaseURL)-1]
	}
	return e
}

// Fail writes the error body for err with WriteError.
func (e Env) Fail(w http.ResponseWriter, r *http.Request, err error) {
	WriteError(w, r, e.Logger, err)
}

// PageSpec describes a server page a module renders inside the application
// layout. Body is trusted HTML produced by the module's own templates.
type PageSpec struct {
	// Title is the document title.
	Title string
	// Nav names the current sidebar or tab group, such as "admin".
	Nav string
	// AdminNav shows the administration tabs above the body.
	AdminNav bool
	// Heading is the page heading. Empty omits it.
	Heading string
	// Body is the page content.
	Body template.HTML
	// Scripts are module script URLs loaded after the layout scripts.
	Scripts []string
	// Status is the HTTP status. Zero means 200.
	Status int
}

// PageRenderer renders module pages inside the application layout. The web
// server implements it.
type PageRenderer interface {
	RenderModulePage(w http.ResponseWriter, r *http.Request, spec PageSpec)
	// RenderError renders the error page for err, or redirects an anonymous
	// visitor to the login page.
	RenderError(w http.ResponseWriter, r *http.Request, err error)
	// Octicon renders an Octicon as the octicon template function of the web
	// server does, as in {{ octicon "stop" 16 }}. An unknown name or size is
	// an error.
	Octicon(name string, size ...int) (template.HTML, error)
}

// PageFuncs returns the template functions a module page template is parsed
// with. Its octicon function only fails, and ExecutePage replaces it with the
// renderer's, so module templates call octicon as the web templates do.
func PageFuncs() template.FuncMap {
	return template.FuncMap{
		"octicon": func(string, ...int) (template.HTML, error) {
			return "", errors.New("octicon: execute module page templates with api.ExecutePage")
		},
		"breakable": Breakable,
	}
}

// breakAfter holds the characters after which Breakable allows a line break.
const breakAfter = ".-_/:@"

// Breakable escapes v for HTML and adds a <wbr> after each ".", "-", "_",
// "/", ":" and "@" that another character follows. A long email, URL or id
// in a narrow cell then wraps at those points and never inside a word
// (docs/primer.md 5.9).
func Breakable(v any) template.HTML {
	s := ""
	if v != nil {
		s = fmt.Sprint(v)
	}
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		b.WriteString(template.HTMLEscapeString(string(r)))
		if i < len(runes)-1 && strings.ContainsRune(breakAfter, r) {
			b.WriteString("<wbr>")
		}
	}
	return template.HTML(b.String())
}

// ExecutePage executes a module page template parsed with PageFuncs, with the
// octicon function of pages. It runs a clone, so t itself is never executed
// and stays clonable.
func ExecutePage(w io.Writer, t *template.Template, pages PageRenderer, data any) error {
	c, err := t.Clone()
	if err != nil {
		return err
	}
	c.Funcs(template.FuncMap{"octicon": pages.Octicon})
	return c.Execute(w, data)
}
