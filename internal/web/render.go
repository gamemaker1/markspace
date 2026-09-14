package web

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
)

// Crumb is one breadcrumb entry above the current note.
type Crumb struct {
	// Name is the label shown to the reader.
	Name string
	// URL is empty for the last crumb, which is the current page.
	URL string
}

// Pagination describes a paged listing for the partials/pagination.html partial.
type Pagination struct {
	// Offset is the index of the first row on this page.
	Offset int
	// Limit is the page size.
	Limit int
	// Count is the number of rows on this page.
	Count int
	// Total is the total row count, or zero when it is unknown.
	Total int
	// PrevURL is empty on the first page.
	PrevURL string
	// NextURL is empty on the last page.
	NextURL string
	// Pages lists the numbered page links. It is empty when Total is
	// unknown, and then only Previous and Next show.
	Pages []PageLink
}

// Page is the data every template receives. Handlers fill the fields their
// page uses and leave the rest zero, so templates must guard with "with".
type Page struct {
	// Title is the page title, used when a template defines no title block.
	Title string
	// InstanceName names the instance in the browser title and the sidebar.
	InstanceName string
	// BaseURL is the externally reachable root.
	BaseURL string
	// DevMode is true when assets reload from disk.
	DevMode bool
	// Now is the render time, used by the relative time helpers.
	Now time.Time
	// Chrome turns on the sidebar and top bar shell.
	Chrome bool
	// Actor is the requesting actor. It is never nil.
	Actor *core.Actor
	// User is the signed-in user, or nil.
	User *core.User
	// IsAdmin reports whether the actor administers the instance.
	IsAdmin bool
	// Nav marks the active top-level navigation item.
	Nav string
	// Spaces is every space the actor may view.
	Spaces []*core.Space
	// Space is the space the page belongs to, or nil.
	Space *core.Space
	// SpaceAccess is the actor's access to Space.
	SpaceAccess core.Access
	// Tree is the recursive item tree of Space.
	Tree []*core.TreeItem
	// Expanded holds the item ids on the path to Note, so the sidebar opens
	// every folder above the current note.
	Expanded map[string]bool
	// Note is the note the page shows, or nil.
	Note *core.Note
	// Grant is the actor's resolved access to Note.
	Grant *core.Grant
	// Crumbs is the breadcrumb trail of Note.
	Crumbs []Crumb
	// Settings is the instance configuration, or nil when it is unreadable.
	Settings *core.Settings
	// Flash is a success message shown above the content.
	Flash string
	// Error is a failure message shown above the content.
	Error string
	// ErrorField names the form field Error belongs to, or is empty.
	ErrorField string
	// Status is the HTTP status the error page reports.
	Status int
	// RequestPath is the path of the current request.
	RequestPath string
	// Query is the parsed query string of the current request.
	Query url.Values
	// Page holds everything specific to one template.
	Data map[string]any
}

// Get returns a page-specific value, or nil when it is absent. It keeps
// templates free of index expressions.
func (p *Page) Get(key string) any { return p.Data[key] }

// Set stores a page-specific value and returns the page, so handlers can
// chain calls.
func (p *Page) Set(key string, value any) *Page {
	if p.Data == nil {
		p.Data = map[string]any{}
	}
	p.Data[key] = value
	return p
}

// Q returns one query parameter of the current request.
func (p *Page) Q(key string) string { return p.Query.Get(key) }

// FieldError returns the error message when it belongs to the named form
// field, and the empty string otherwise. Templates use it to show the message
// under the field.
func (p *Page) FieldError(name string) string {
	if p.Error == "" || p.ErrorField == "" || p.ErrorField != name {
		return ""
	}
	return p.Error
}

// newPage builds the shared page data for a request. It loads the actor's
// spaces and the instance settings, ignoring failures so an error page still
// renders.
func (s *Server) newPage(r *http.Request, title, nav string) *Page {
	ctx := r.Context()
	actor := api.ActorFrom(ctx)
	p := &Page{
		Title:        title,
		InstanceName: s.instanceName,
		BaseURL:      s.auth.Origin(r, s.baseURL),
		DevMode:      s.devMode,
		Now:          time.Now().UTC(),
		Chrome:       actor.IsUser(),
		Actor:        actor,
		User:         actor.User,
		IsAdmin:      actor.IsAdmin(),
		Nav:          nav,
		Expanded:     map[string]bool{},
		RequestPath:  r.URL.Path,
		Query:        r.URL.Query(),
		Data:         map[string]any{},
	}
	// A refused form redirects back with its message in the query string.
	if r.Method == http.MethodGet {
		p.Error = strings.TrimSpace(p.Query.Get("error"))
		p.ErrorField = strings.TrimSpace(p.Query.Get("field"))
	}
	if st, err := s.backend.GetSettings(ctx, actor); err == nil && st != nil {
		p.Settings = st
		if st.InstanceName != "" {
			p.InstanceName = st.InstanceName
		}
	}
	if actor.IsUser() {
		if spaces, err := s.backend.ListSpaces(ctx, actor); err == nil {
			p.Spaces = spaces
		}
	}
	return p
}

// parsePages parses the layout and the partials in templates/partials once,
// then clones that set for every page template. The returned map is keyed by
// the path under templates without the .html suffix, such as "note" or
// "admin/ai".
func (s *Server) parsePages(root fs.FS) (map[string]*template.Template, error) {
	base, err := template.New("markspace").Funcs(funcMap).ParseFS(root,
		"templates/base.html", "templates/partials/*.html")
	if err != nil {
		return nil, fmt.Errorf("web: parse layout: %w", err)
	}
	var names []string
	err = fs.WalkDir(root, "templates", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && name == "templates/partials" {
			return fs.SkipDir
		}
		if !d.IsDir() && path.Ext(name) == ".html" && name != "templates/base.html" {
			names = append(names, name)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("web: list templates: %w", err)
	}
	sort.Strings(names)
	out := make(map[string]*template.Template, len(names))
	for _, name := range names {
		key := strings.TrimSuffix(strings.TrimPrefix(name, "templates/"), ".html")
		clone, err := base.Clone()
		if err != nil {
			return nil, fmt.Errorf("web: clone layout for %s: %w", key, err)
		}
		if _, err := clone.ParseFS(root, name); err != nil {
			return nil, fmt.Errorf("web: parse %s: %w", key, err)
		}
		out[key] = clone
	}
	if len(out) == 0 {
		return nil, errors.New("web: no page templates found")
	}
	return out, nil
}

// PageNames returns every parsed page template name, sorted. Tests use it to
// execute the whole set.
func (s *Server) PageNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.pages))
	for name := range s.pages {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// template returns one page template. In DevMode it reparses the whole set
// from disk first, so a template edit takes effect on the next request.
func (s *Server) template(name string) (*template.Template, error) {
	if s.devMode {
		pages, err := s.parsePages(s.root)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.pages = pages
		s.mu.Unlock()
	}
	s.mu.RLock()
	tmpl, ok := s.pages[name]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("web: unknown page template %q", name)
	}
	return tmpl, nil
}

// Render executes one page template into w with the given status. It buffers
// the output, so a template failure never emits half a page.
func (s *Server) Render(w http.ResponseWriter, r *http.Request, status int, name string, p *Page) {
	tmpl, err := s.template(name)
	if err != nil {
		s.log.Error("web: template lookup failed", "page", name, "err", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	// A page that defines its own document, such as the app shell, skips the
	// shared layout.
	entry := "layout"
	if tmpl.Lookup("document") != nil {
		entry = "document"
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, entry, p); err != nil {
		s.log.Error("web: template execute failed", "page", name, "path", r.URL.Path, "err", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		s.log.Debug("web: response write failed", "path", r.URL.Path, "err", err)
	}
}

// statusFor maps a core sentinel error onto an HTTP status code.
func statusFor(err error) int {
	switch {
	case errors.Is(err, core.ErrInvalid):
		return http.StatusBadRequest
	case errors.Is(err, core.ErrUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, core.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, core.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, core.ErrConflict), errors.Is(err, core.ErrExists):
		return http.StatusConflict
	case errors.Is(err, core.ErrTooLarge):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, core.ErrUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, core.ErrUnsupported):
		return http.StatusUnsupportedMediaType
	case errors.Is(err, core.ErrRateLimited):
		return http.StatusTooManyRequests
	}
	return http.StatusInternalServerError
}

// statusText gives the error page its headline.
func statusText(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "That request did not make sense"
	case http.StatusForbidden:
		return "You do not have access to this"
	case http.StatusNotFound:
		return "Page not found"
	case http.StatusConflict:
		return "That changed while you were away"
	case http.StatusRequestEntityTooLarge:
		return "That file is too large"
	case http.StatusUnsupportedMediaType:
		return "That format is not supported"
	case http.StatusTooManyRequests:
		return "Too many requests"
	}
	return "Something went wrong"
}

// fail renders the error page for err. An unauthorized page request redirects
// to the login form with a next parameter, and an unmapped error logs at
// error level before the 500 page.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	status := statusFor(err)
	if status == http.StatusUnauthorized {
		s.redirectToLogin(w, r)
		return
	}
	if status == http.StatusInternalServerError {
		s.log.Error("web: request failed", "path", r.URL.Path, "err", err)
	} else {
		s.log.Debug("web: request refused", "path", r.URL.Path, "status", status, "err", err)
	}
	// The status text is the page title and the Blankslate heading. Page.Error
	// stays empty, including an ?error= message from the query string, so the
	// flash Banner does not repeat the heading above the Blankslate.
	p := s.newPage(r, statusText(status), "")
	p.Chrome = false
	p.Status = status
	p.Error, p.ErrorField = "", ""
	if status != http.StatusInternalServerError {
		p.Set("Detail", detailOf(err))
	}
	s.Render(w, r, status, "error", p)
}

// Octicon renders an Octicon for module page templates. It implements
// api.PageRenderer with the octicon template function.
func (s *Server) Octicon(name string, size ...int) (template.HTML, error) {
	return octicon(name, size...)
}

// detailOf returns the message of a validation error, as a sentence.
func detailOf(err error) string {
	var ve *core.ValidationError
	if errors.As(err, &ve) {
		return sentence(ve.Message)
	}
	return ""
}

// fieldOf returns the form field a validation error names, or the empty
// string.
func fieldOf(err error) string {
	var ve *core.ValidationError
	if errors.As(err, &ve) {
		return ve.Field
	}
	return ""
}

// sentence capitalizes a message and ends it with a full stop.
func sentence(message string) string {
	s := strings.TrimSpace(message)
	if s == "" {
		return s
	}
	s = strings.ToUpper(s[:1]) + s[1:]
	if strings.HasSuffix(s, ".") || strings.HasSuffix(s, "!") || strings.HasSuffix(s, "?") {
		return s
	}
	return s + "."
}

// formFail sends a refused form back to the page it came from, with the
// message and the field in the query string, so the page shows the error next
// to the form. A lost session, a missing page or an internal failure still
// takes the usual error path.
func (s *Server) formFail(w http.ResponseWriter, r *http.Request, back string, err error, fallback string) {
	switch statusFor(err) {
	case http.StatusUnauthorized, http.StatusNotFound, http.StatusInternalServerError:
		s.fail(w, r, err)
		return
	}
	message := authMessage(err, fallback)
	switch {
	case errors.Is(err, core.ErrForbidden):
		message = "You do not have permission to do that."
	case detailOf(err) == "" && (errors.Is(err, core.ErrConflict) || errors.Is(err, core.ErrExists)):
		message = "That already exists, or it changed in the meantime."
	case detailOf(err) == "" && errors.Is(err, core.ErrTooLarge):
		message = "That file is too large."
	}
	v := url.Values{"error": {message}}
	if field := fieldOf(err); field != "" {
		v.Set("field", field)
	}
	http.Redirect(w, r, withQuery(back, v), http.StatusSeeOther)
}

// withQuery appends query values to a local URL, before any fragment.
func withQuery(target string, v url.Values) string {
	fragment := ""
	if i := strings.Index(target, "#"); i >= 0 {
		target, fragment = target[:i], target[i:]
	}
	sep := "?"
	if strings.Contains(target, "?") {
		sep = "&"
	}
	return target + sep + v.Encode() + fragment
}

// redirectToLogin sends a page request to the login form, preserving where
// the reader was headed.
func (s *Server) redirectToLogin(w http.ResponseWriter, r *http.Request) {
	next := r.URL.Path
	if r.URL.RawQuery != "" {
		next += "?" + r.URL.RawQuery
	}
	if next == "/" || next == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/login?next="+url.QueryEscape(next), http.StatusSeeOther)
}

// currentActor returns the actor resolved by the authentication middleware.
// It never returns nil.
func currentActor(r *http.Request) *core.Actor { return api.ActorFrom(r.Context()) }

// requireUser resolves the signed-in actor. It redirects to the login form
// and reports false when the request is anonymous.
func (s *Server) requireUser(w http.ResponseWriter, r *http.Request) (*core.Actor, bool) {
	actor := api.ActorFrom(r.Context())
	if !actor.IsUser() {
		s.redirectToLogin(w, r)
		return nil, false
	}
	return actor, true
}

// requireAdmin resolves an instance admin. A signed-in non-admin gets the
// 403 error page and an anonymous visitor goes to the login form.
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (*core.Actor, bool) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return nil, false
	}
	if !actor.IsAdmin() {
		s.fail(w, r, core.ErrForbidden)
		return nil, false
	}
	return actor, true
}

// safeNext returns dest when it is a local path with no host, and "/"
// otherwise. It rejects "//evil.com" and any absolute URL.
func safeNext(dest string) string {
	if dest == "" || !strings.HasPrefix(dest, "/") {
		return "/"
	}
	if strings.HasPrefix(dest, "//") || strings.HasPrefix(dest, "/\\") {
		return "/"
	}
	if strings.ContainsAny(dest, "\\\r\n") {
		return "/"
	}
	u, err := url.Parse(dest)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" {
		return "/"
	}
	return dest
}

// formValues parses a form body, capping multipart uploads at MaxUpload.
func (s *Server) formValues(r *http.Request) error {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		return r.ParseMultipartForm(s.maxUpload)
	}
	return r.ParseForm()
}

// formBool reports whether a checkbox was submitted.
func formBool(r *http.Request, key string) bool {
	switch strings.ToLower(strings.TrimSpace(r.PostFormValue(key))) {
	case "", "0", "false", "off", "no":
		return false
	}
	return true
}

// formInt reads an integer form or query value, falling back to def.
func formInt(v string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return n
}

// splitTags turns a comma or newline separated tag field into a clean slice.
func splitTags(v string) []string {
	parts := strings.FieldsFunc(v, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == '\t'
	})
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		t := strings.TrimPrefix(strings.TrimSpace(part), "#")
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// funcMap is the template function set. Every template in the package is
// parsed with it.
var funcMap = template.FuncMap{
	"safeHTML":     safeHTML,
	"noteURL":      noteURL,
	"spaceURL":     spaceURL,
	"tagURL":       tagURL,
	"timeAgo":      relativeTime,
	"relativeTime": relativeTime,
	"preciseTime":  preciseTime,
	"formatTime":   formatTime,
	"initials":     initials,
	"accessLabel":  accessLabel,
	"roleLabel":    roleLabel,
	"hasAccess":    hasAccess,
	"pluralize":    pluralize,
	"join":         join,
	"dict":         dict,
	"truncate":     truncate,
	"breakable":    api.Breakable,
	"bytes":        humanBytes,
	"iconOr":       iconOr,
	"treeID":       treeID,
	"treeLabel":    treeLabel,
	"treeGlyph":    treeGlyph,
	"treeURL":      treeURL,
	"treeCurrent":  treeCurrent,
	"add":          func(a, b int) int { return a + b },
	"stamp":        stamp,
	"cssColor":     cssColor,
	"fileURL":      fileURL,
	"orDefault":    orDefault,
	"queryEscape":  url.QueryEscape,
	"octicon":      octicon,
	"icon_alert": func() template.HTML {
		return iconSVG(`<path d="M10 3.2 17.5 16.5h-15z" stroke-linejoin="round"/><path d="M10 8.5v3.5M10 14.2v.01"/>`, 16)
	},
	"icon_upload":   func() template.HTML { return iconSVG(`<path d="M10 13V3.75M6.5 7.25 10 3.75l3.5 3.5M4 16.5h12"/>`, 22) },
	"icon_download": func() template.HTML { return iconSVG(`<path d="M10 3.5v9M6.5 9 10 12.5 13.5 9M4 16.5h12"/>`, 16) },
}

// iconSVG wraps fixed path markup in an inline SVG icon. Only constant
// strings from funcMap reach it.
func iconSVG(paths string, size int) template.HTML {
	return template.HTML(fmt.Sprintf(`<svg class="icon" viewBox="0 0 20 20" width="%d" height="%d" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true" focusable="false">%s</svg>`, size, size, paths))
}

// safeHTML marks a string as trusted HTML. Pass it only output from the
// markdown renderer or from a backend field documented as escaped, because it
// disables template escaping.
func safeHTML(v any) template.HTML {
	switch s := v.(type) {
	case template.HTML:
		return s
	case string:
		return template.HTML(s)
	case nil:
		return ""
	}
	return template.HTML(fmt.Sprint(v))
}

// slugOf accepts a space, a space slug or a note and returns the space slug.
func slugOf(v any) string {
	switch s := v.(type) {
	case *core.Space:
		if s == nil {
			return ""
		}
		if s.Slug != "" {
			return s.Slug
		}
		return s.ID
	case core.Space:
		return s.Slug
	case *core.Note:
		if s == nil {
			return ""
		}
		return s.SpaceSlug
	case string:
		return s
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}

// notePathOf accepts a note or a path string and returns the note path with
// no .md suffix.
func notePathOf(v any) string {
	switch n := v.(type) {
	case *core.Note:
		if n == nil {
			return ""
		}
		return strings.TrimSuffix(n.Path, ".md")
	case core.Note:
		return strings.TrimSuffix(n.Path, ".md")
	case string:
		return strings.TrimSuffix(n, ".md")
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}

// spaceURL builds the space home URL from a space, a slug or a note.
func spaceURL(space any) string {
	slug := slugOf(space)
	if slug == "" {
		return "/spaces"
	}
	return (&url.URL{Path: "/s/" + slug}).String()
}

// noteURL builds the canonical note URL. space may be a Space, a slug or a
// note carrying SpaceSlug, and note may be a Note or a path.
func noteURL(space, note any) string {
	slug := slugOf(space)
	p := notePathOf(note)
	if slug == "" {
		if n, ok := note.(*core.Note); ok && n != nil {
			return "/n/" + n.ID
		}
		return "/spaces"
	}
	if p == "" {
		return spaceURL(slug)
	}
	return (&url.URL{Path: "/s/" + slug + "/n/" + p}).String()
}

// tagURL builds the URL of a tag listing inside a space.
func tagURL(space any, tag string) string {
	slug := slugOf(space)
	if slug == "" {
		return "/search?q=" + url.QueryEscape("#"+tag)
	}
	return (&url.URL{Path: "/s/" + slug + "/t/" + tag}).String()
}

// timeAgo renders a coarse relative time such as "3 hours ago".
func timeAgo(v any) string {
	t, ok := asTime(v)
	if !ok || t.IsZero() {
		return ""
	}
	d := time.Since(t)
	future := false
	if d < 0 {
		d, future = -d, true
	}
	var out string
	switch {
	case d < 45*time.Second:
		return "just now"
	case d < 90*time.Second:
		out = "a minute"
	case d < time.Hour:
		out = pluralize(int(d.Minutes()), "minute", "minutes")
	case d < 2*time.Hour:
		out = "an hour"
	case d < 24*time.Hour:
		out = pluralize(int(d.Hours()), "hour", "hours")
	case d < 48*time.Hour:
		out = "a day"
	case d < 30*24*time.Hour:
		out = pluralize(int(d.Hours()/24), "day", "days")
	case d < 365*24*time.Hour:
		out = pluralize(int(d.Hours()/(24*30)), "month", "months")
	default:
		out = pluralize(int(d.Hours()/(24*365)), "year", "years")
	}
	if future {
		return "in " + out
	}
	return out + " ago"
}

// relativeTime renders a Primer RelativeTime element. The vendored
// relative-time-element script replaces the fallback date with "3 days ago"
// or "on Aug 2". The title tooltip is off, as docs/primer.md 5.9 requires.
func relativeTime(v any) template.HTML {
	t, ok := asTime(v)
	if !ok || t.IsZero() {
		return ""
	}
	t = t.UTC()
	return template.HTML(`<relative-time datetime="` + t.Format(time.RFC3339) + `" no-title>` +
		template.HTMLEscapeString(t.Format("Jan 2, 2006")) + `</relative-time>`)
}

// preciseTime renders a RelativeTime element that always shows the full date
// and time, such as "Thursday, Aug 26, 2026, 3:05 PM", for places where the
// exact time matters. The fallback text is in UTC.
func preciseTime(v any) template.HTML {
	t, ok := asTime(v)
	if !ok || t.IsZero() {
		return ""
	}
	t = t.UTC()
	return template.HTML(`<relative-time datetime="` + t.Format(time.RFC3339) +
		`" format="datetime" weekday="long" year="numeric" month="short" day="numeric" hour="numeric" minute="2-digit" no-title>` +
		template.HTMLEscapeString(t.Format("Monday, Jan 2, 2006, 3:04 PM MST")) + `</relative-time>`)
}

// formatTime renders an absolute timestamp in UTC.
func formatTime(v any) string {
	t, ok := asTime(v)
	if !ok || t.IsZero() {
		return ""
	}
	return t.UTC().Format("2 Jan 2006, 15:04 MST")
}

// asTime unwraps a time.Time or a *time.Time.
func asTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case *time.Time:
		if t == nil {
			return time.Time{}, false
		}
		return *t, true
	}
	return time.Time{}, false
}

// initials returns up to two uppercase letters for an avatar placeholder.
func initials(v any) string {
	switch u := v.(type) {
	case *core.User:
		if u == nil {
			return "?"
		}
		return u.Initials()
	case core.User:
		return u.Initials()
	case string:
		return (&core.User{Name: u}).Initials()
	}
	return "?"
}

// accessLabel names an access level for a reader.
func accessLabel(v any) string {
	a, ok := asAccess(v)
	if !ok {
		return "no access"
	}
	switch a {
	case core.AccessView:
		return "Can view"
	case core.AccessComment:
		return "Can comment"
	case core.AccessEdit:
		return "Can edit"
	case core.AccessManage:
		return "Can manage"
	}
	return "No access"
}

// roleLabel names a space role for a reader.
func roleLabel(v any) string {
	switch r := v.(type) {
	case core.Role:
		return roleName(r)
	case string:
		return roleName(core.Role(r))
	}
	return ""
}

func roleName(r core.Role) string {
	switch r {
	case core.RoleOwner:
		return "Owner"
	case core.RoleAdmin:
		return "Admin"
	case core.RoleEditor:
		return "Editor"
	case core.RoleCommenter:
		return "Commenter"
	case core.RoleViewer:
		return "Viewer"
	}
	if r == "" {
		return "No role"
	}
	return string(r)
}

// asAccess converts a grant, an access level or a wire name into an Access.
func asAccess(v any) (core.Access, bool) {
	switch a := v.(type) {
	case core.Access:
		return a, true
	case *core.Grant:
		if a == nil {
			return core.AccessNone, false
		}
		return a.Access, true
	case core.Grant:
		return a.Access, true
	case core.Role:
		return a.Access(), true
	case string:
		return core.ParseAccess(a)
	case int:
		return core.Access(a), true
	case nil:
		return core.AccessNone, false
	}
	return core.AccessNone, false
}

// hasAccess reports whether a grant permits at least the wanted level. A nil
// grant permits nothing.
func hasAccess(grant any, level any) bool {
	have, ok := asAccess(grant)
	if !ok {
		return false
	}
	want, ok := asAccess(level)
	if !ok {
		return false
	}
	return have.AtLeast(want)
}

// pluralize formats a count with the singular or plural noun.
func pluralize(n int, one, many string) string {
	if n == 1 || n == -1 {
		return strconv.Itoa(n) + " " + one
	}
	return strconv.Itoa(n) + " " + many
}

// join concatenates a string slice with a separator.
func join(sep string, v any) string {
	switch items := v.(type) {
	case []string:
		return strings.Join(items, sep)
	case nil:
		return ""
	case []any:
		parts := make([]string, 0, len(items))
		for _, it := range items {
			parts = append(parts, fmt.Sprint(it))
		}
		return strings.Join(parts, sep)
	}
	return fmt.Sprint(v)
}

// dict builds a map from alternating key and value arguments, so a partial
// can be called with several named values.
func dict(pairs ...any) map[string]any {
	out := make(map[string]any, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out[fmt.Sprint(pairs[i])] = pairs[i+1]
	}
	return out
}

// truncate shortens a string to n runes and appends an ellipsis.
func truncate(n int, v any) string {
	s := fmt.Sprint(v)
	if v == nil {
		s = ""
	}
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return strings.TrimRight(string(runes[:n]), " ") + "…"
}

// humanBytes renders a byte count with a binary unit.
func humanBytes(v any) string {
	var n float64
	switch b := v.(type) {
	case int:
		n = float64(b)
	case int64:
		n = float64(b)
	case float64:
		n = b
	default:
		return ""
	}
	if n < 1024 {
		return strconv.FormatInt(int64(n), 10) + " B"
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	for _, unit := range units {
		n /= 1024
		if n < 1024 {
			if n < 10 {
				return strconv.FormatFloat(n, 'f', 1, 64) + " " + unit
			}
			return strconv.FormatFloat(n, 'f', 0, 64) + " " + unit
		}
	}
	return strconv.FormatFloat(n, 'f', 0, 64) + " EB"
}

// iconOr returns the icon of a note or space, or a default glyph.
func iconOr(v any, fallback ...string) string {
	def := "◦"
	if len(fallback) > 0 && fallback[0] != "" {
		def = fallback[0]
	}
	var icon string
	switch x := v.(type) {
	case *core.Note:
		if x != nil {
			icon = x.Icon
		}
	case *core.Space:
		if x != nil {
			icon = x.Icon
		}
	case *core.TreeItem:
		if x != nil && x.Note != nil {
			icon = x.Note.Icon
		}
	case string:
		icon = x
	}
	if strings.TrimSpace(icon) == "" {
		return def
	}
	return icon
}

// stamp formats a timestamp as RFC 3339, for a data attribute a script reads
// back.
func stamp(v any) string {
	t, ok := asTime(v)
	if !ok || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// safeColorPattern matches the colour values allowed in an inline style.
var safeColorPattern = regexp.MustCompile(`^(#[0-9a-fA-F]{3,8}|[a-zA-Z]{3,20})$`)

// cssColor passes a colour through to an inline style, and returns nothing
// when the value is not a plain hex colour or colour keyword.
func cssColor(v any) template.CSS {
	s, ok := v.(string)
	if !ok || !safeColorPattern.MatchString(strings.TrimSpace(s)) {
		return ""
	}
	return template.CSS(strings.TrimSpace(s))
}

// fileURL builds the content URL of a file. It prefers the URL the backend
// filled in and otherwise composes one from the space slug.
func fileURL(f *core.File, space any) string {
	if f == nil {
		return ""
	}
	if f.URL != "" {
		return f.URL
	}
	slug := f.SpaceSlug
	if slug == "" {
		slug = slugOf(space)
	}
	if slug == "" {
		slug = f.SpaceID
	}
	return (&url.URL{Path: "/files/" + slug + "/" + f.ID + "/" + f.Name}).String()
}

// entryURL builds the view URL of a folder, prefix "d", or a file, prefix
// "f", from its entry path.
func entryURL(slug, prefix, relPath string) string {
	if slug == "" {
		return "/spaces"
	}
	return (&url.URL{Path: "/s/" + slug + "/" + prefix + "/" + strings.Trim(relPath, "/")}).String()
}

// treeID returns the id of a tree row.
func treeID(node *core.TreeItem) string {
	switch {
	case node == nil:
		return ""
	case node.Folder != nil:
		return node.Folder.ID
	case node.Note != nil:
		return node.Note.ID
	case node.File != nil:
		return node.File.ID
	}
	return ""
}

// treeLabel returns the text of a tree row: a folder or file name, or a note
// title.
func treeLabel(node *core.TreeItem) string {
	switch {
	case node == nil:
		return ""
	case node.Folder != nil:
		return node.Folder.Name
	case node.Note != nil:
		return node.Note.Title
	case node.File != nil:
		return node.File.Name
	}
	return ""
}

// treeGlyph returns the icon of a tree row.
func treeGlyph(node *core.TreeItem) string {
	switch {
	case node == nil:
		return ""
	case node.Folder != nil:
		return "\U0001F4C1"
	case node.File != nil:
		return "\U0001F4C4"
	}
	return iconOr(node)
}

// treeURL returns the link of a tree row. A folder with a folder note opens
// the note.
func treeURL(space any, node *core.TreeItem) string {
	if node == nil {
		return ""
	}
	slug := slugOf(space)
	switch {
	case node.Folder != nil:
		if slug == "" {
			slug = node.Folder.SpaceSlug
		}
		if node.Folder.IndexNoteID != "" {
			return noteURL(slug, strings.Trim(node.Folder.Path, "/")+"/index")
		}
		return entryURL(slug, "d", node.Folder.Path)
	case node.Note != nil:
		if slug == "" {
			slug = node.Note.SpaceSlug
		}
		return noteURL(slug, node.Note)
	case node.File != nil:
		if slug == "" {
			slug = node.File.SpaceSlug
		}
		return entryURL(slug, "f", node.File.Path)
	}
	return ""
}

// treeCurrent reports whether a tree row shows the current note.
func treeCurrent(node *core.TreeItem, current *core.Note) bool {
	if node == nil || current == nil || current.ID == "" {
		return false
	}
	switch {
	case node.Note != nil:
		return node.Note.ID == current.ID
	case node.Folder != nil:
		return node.Folder.IndexNoteID == current.ID
	}
	return false
}

// orDefault returns v when it is a non-empty string, and def otherwise.
func orDefault(v any, def string) string {
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// parseFloat reads a float form value, returning zero when it is not a
// number.
func parseFloat(v string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return 0
	}
	return f
}

// PageLink is one entry of Primer's Pagination: a numbered page, or a gap
// shown as an ellipsis.
type PageLink struct {
	Number  int
	URL     string
	Current bool
	Gap     bool
}

// pageLinks lists the first page, the last page and two pages on each side
// of the current page, with gaps between them, as Primer's Pagination shows.
// current and pages count from 1. link builds the URL of a page.
func pageLinks(current, pages int, link func(page int) string) []PageLink {
	if pages <= 1 {
		return nil
	}
	var out []PageLink
	last := 0
	for n := 1; n <= pages; n++ {
		if n != 1 && n != pages && (n < current-2 || n > current+2) {
			continue
		}
		if last != 0 && n > last+1 {
			out = append(out, PageLink{Gap: true})
		}
		out = append(out, PageLink{Number: n, URL: link(n), Current: n == current})
		last = n
	}
	return out
}

// listPagination builds the previous and next links of a paged listing.
// query is an optional q parameter carried across pages.
func listPagination(base, query string, offset, limit, count, total int) *Pagination {
	page := &Pagination{Offset: offset, Limit: limit, Count: count, Total: total}
	link := func(off int) string {
		v := url.Values{}
		if query != "" {
			v.Set("q", query)
		}
		if off > 0 {
			v.Set("offset", strconv.Itoa(off))
		}
		if len(v) == 0 {
			return base
		}
		return base + "?" + v.Encode()
	}
	if offset > 0 {
		prev := offset - limit
		if prev < 0 {
			prev = 0
		}
		page.PrevURL = link(prev)
	}
	if count >= limit && (total == 0 || offset+count < total) {
		page.NextURL = link(offset + limit)
	}
	if total > 0 && limit > 0 {
		pages := (total + limit - 1) / limit
		page.Pages = pageLinks(offset/limit+1, pages, func(n int) string { return link((n - 1) * limit) })
	}
	return page
}
