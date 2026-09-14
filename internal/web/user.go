package web

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// Forms on server pages follow docs/primer.md 6.1 and 6.6. A handler checks a
// submitted form, and on failure renders the page again with the entered
// values, a message under each invalid field and, from 3 problems, an error
// summary Banner. Templates read the state through the Form* methods below.

// formProblem is one invalid field of a submitted form.
type formProblem struct {
	// Field is the form field name.
	Field string
	// ID is the id of the field's input, the target of the summary link.
	ID string
	// Message is the validation message, as a sentence.
	Message string
}

// formState is one submitted form carried back into its template.
type formState struct {
	name     string
	values   url.Values
	problems []formProblem
	message  string
}

// formDataKey is the Page.Data key that holds the submitted form.
const formDataKey = "form"

// summaryMin is the number of problems from which a form shows an error
// summary instead of focusing the first invalid field.
const summaryMin = 3

// newForm starts the state of the form called name from the posted values.
func newForm(name string, r *http.Request) *formState {
	values := url.Values{}
	for k, v := range r.PostForm {
		values[k] = v
	}
	return &formState{name: name, values: values}
}

// add records a problem for field. Only the first problem of a field counts.
func (f *formState) add(field, message string) {
	for _, p := range f.problems {
		if p.Field == field {
			return
		}
	}
	f.problems = append(f.problems, formProblem{Field: field, ID: fieldID(f.name, field), Message: sentence(message)})
}

// invalid reports whether the form has any problem.
func (f *formState) invalid() bool { return len(f.problems) > 0 || f.message != "" }

// value returns one submitted value, trimmed.
func (f *formState) value(field string) string { return strings.TrimSpace(f.values.Get(field)) }

// fail records a backend error. A validation error that names a field of
// the form goes under that field, after renaming through rename. Any other
// error becomes the form message.
func (f *formState) fail(err error, fallback string, rename map[string]string) {
	if field := fieldOf(err); field != "" {
		if to, ok := rename[field]; ok {
			field = to
		}
		if _, ok := f.values[field]; ok {
			f.add(field, detailOf(err))
			return
		}
	}
	switch {
	case errors.Is(err, core.ErrForbidden):
		f.message = "You do not have permission to do that."
	case detailOf(err) != "":
		f.message = detailOf(err)
	case errors.Is(err, core.ErrConflict), errors.Is(err, core.ErrExists):
		f.message = "That already exists, or it changed in the meantime."
	case errors.Is(err, core.ErrTooLarge):
		f.message = "That file is too large."
	default:
		f.message = fallback
	}
}

// status returns the HTTP status of a page rendered again with problems.
func (f *formState) status(err error) int {
	if err != nil {
		if s := statusFor(err); s >= 400 && s < 500 {
			return s
		}
	}
	return http.StatusBadRequest
}

// hardFailure reports whether a backend error takes the error page instead
// of a message on the form: a lost session, a missing page or a fault.
func hardFailure(err error) bool {
	switch statusFor(err) {
	case http.StatusUnauthorized, http.StatusNotFound, http.StatusInternalServerError:
		return true
	}
	return false
}

// fieldID returns the input id of a form field: the form name and the field
// name in kebab case, such as "signup-password-confirm".
func fieldID(form, field string) string {
	var b strings.Builder
	b.WriteString(form)
	b.WriteByte('-')
	for i, c := range field {
		if c >= 'A' && c <= 'Z' {
			if i > 0 {
				b.WriteByte('-')
			}
			c += 'a' - 'A'
		}
		b.WriteRune(c)
	}
	return b.String()
}

// submitted returns the form state of the page when it belongs to form.
func (p *Page) submitted(form string) *formState {
	f, _ := p.Data[formDataKey].(*formState)
	if f == nil || f.name != form {
		return nil
	}
	return f
}

// FieldID returns the input id of a form field.
func (p *Page) FieldID(form, field string) string { return fieldID(form, field) }

// FormError returns the validation message of one field. Without a
// submitted form it falls back to the field error named in the query string.
func (p *Page) FormError(form, field string) string {
	f := p.submitted(form)
	if f == nil {
		if p.Data[formDataKey] == nil {
			return p.FieldError(field)
		}
		return ""
	}
	for _, pr := range f.problems {
		if pr.Field == field {
			return pr.Message
		}
	}
	return ""
}

// FormValue returns the submitted value of a field, or fallback when the
// form was not submitted.
func (p *Page) FormValue(form, field string, fallback any) string {
	if f := p.submitted(form); f != nil {
		if _, ok := f.values[field]; ok {
			return f.values.Get(field)
		}
	}
	if fallback == nil {
		return ""
	}
	if s, ok := fallback.(string); ok {
		return s
	}
	return ""
}

// FormSummary returns the problems of a form with 3 or more of them, for the
// error summary Banner. It returns nil otherwise.
func (p *Page) FormSummary(form string) []formProblem {
	if f := p.submitted(form); f != nil && len(f.problems) >= summaryMin {
		return f.problems
	}
	return nil
}

// FormInline reports whether a field shows its problem without a summary,
// so the page script focuses it.
func (p *Page) FormInline(form, field string) bool {
	return p.FormError(form, field) != "" && p.FormSummary(form) == nil
}

// FormFocus reports whether a field takes focus when the page loads: the
// first invalid field of a form with 1 or 2 problems and no form message.
func (p *Page) FormFocus(form, field string) bool {
	f := p.submitted(form)
	if f == nil || f.message != "" || len(f.problems) == 0 || len(f.problems) >= summaryMin {
		return false
	}
	return f.problems[0].Field == field
}

// FormMessage returns the problem of a form that belongs to no field.
func (p *Page) FormMessage(form string) string {
	if f := p.submitted(form); f != nil {
		return f.message
	}
	return ""
}

// FormOpen reports whether a form that sits in a closed disclosure has
// problems, so the disclosure opens.
func (p *Page) FormOpen(form string) bool {
	f := p.submitted(form)
	return f != nil && f.invalid()
}

// FormSaved reports whether the page follows a successful save of form.
func (p *Page) FormSaved(form string) bool { return p.Query.Get("saved") == form }

// Destructive actions confirm in a ConfirmationDialog when scripting runs.
// With scripting off, the form carries noscript=1 from inside a noscript
// element, and the handler answers with a confirmation page instead of
// acting. That page posts the same fields again with confirmed=1.

// confirmField is one hidden field of a confirmation page.
type confirmField struct {
	Name  string
	Value string
}

// confirmView describes a confirmation page.
type confirmView struct {
	Title        string
	Body         string
	ConfirmLabel string
	Danger       bool
	Action       string
	CancelURL    string
	Fields       []confirmField
}

// needsConfirm reports whether a destructive request still needs a
// confirmation page because scripting is off.
func needsConfirm(r *http.Request) bool {
	return r.PostFormValue("noscript") == "1" && r.PostFormValue("confirmed") != "1"
}

// confirmFields copies posted or query values into hidden fields, leaving
// out the markers of the confirmation step.
func confirmFields(values url.Values) []confirmField {
	names := make([]string, 0, len(values))
	for name := range values {
		if name == "noscript" || name == "confirmed" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	var out []confirmField
	for _, name := range names {
		for _, v := range values[name] {
			out = append(out, confirmField{Name: name, Value: v})
		}
	}
	return out
}

// renderConfirm renders the confirmation page of one destructive action.
func (s *Server) renderConfirm(w http.ResponseWriter, r *http.Request, nav string, space *core.Space, c *confirmView) {
	p := s.newPage(r, c.Title, nav)
	p.Space = space
	p.Set("Confirm", c)
	s.Render(w, r, http.StatusOK, "confirm", p)
}

// handleSettingsIndex lists the settings pages. On narrow viewports it is
// the parent page that every settings page links back to.
func (s *Server) handleSettingsIndex(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	s.Render(w, r, http.StatusOK, "settings", s.newPage(r, "Settings", "settings-index"))
}

// handleProfile renders the profile and password forms.
func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	p := s.newPage(r, "Profile", "profile")
	p.Flash = r.URL.Query().Get("flash")
	s.Render(w, r, http.StatusOK, "profile", p)
}

// handleProfilePost saves the profile or changes the password, depending on
// the action field. It still accepts session-revoke from older pages.
func (s *Server) handleProfilePost(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if err := s.formValues(r); err != nil {
		s.fail(w, r, core.Invalid("form", "the form could not be read"))
		return
	}
	switch r.PostFormValue("action") {
	case "password":
		s.changePassword(w, r, actor)
	case "session-revoke":
		s.revokeSession(w, r, actor)
	default:
		s.saveProfile(w, r, actor)
	}
}

// avatarColorPattern matches a colour from the colour input.
var avatarColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// defaultAvatarColor is the colour input's value for a user without one.
const defaultAvatarColor = "#a21caf"

// saveProfile checks and saves the profile form. An unchanged form saves
// nothing and shows nothing.
func (s *Server) saveProfile(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	form := newForm("profile", r)
	user := actor.User
	in := core.ProfileInput{}
	changed := false
	take := func(field, current string, dst **string) (string, bool) {
		if !r.PostForm.Has(field) {
			return "", false
		}
		v := form.value(field)
		if v != current {
			*dst = &v
			changed = true
		}
		return v, true
	}
	if v, ok := take("name", user.Name, &in.Name); ok && v == "" {
		form.add("name", "Enter your name")
	}
	if v, ok := take("email", user.Email, &in.Email); ok {
		switch {
		case v == "":
			form.add("email", "Enter your email address")
		case !strings.Contains(v, "@"):
			form.add("email", "Enter an email address such as ada@example.com")
		}
	}
	take("timezone", user.Timezone, &in.Timezone)
	take("locale", user.Locale, &in.Locale)
	current := user.AvatarColor
	if current == "" {
		current = defaultAvatarColor
	}
	if v, ok := take("avatarColor", current, &in.AvatarColor); ok && v != "" && !avatarColorPattern.MatchString(v) {
		form.add("avatarColor", "Choose a colour")
	}
	if form.invalid() {
		s.renderProfileForm(w, r, form, http.StatusBadRequest)
		return
	}
	if !changed {
		http.Redirect(w, r, "/settings/profile", http.StatusSeeOther)
		return
	}
	if _, err := s.backend.UpdateProfile(r.Context(), actor, in); err != nil {
		if hardFailure(err) {
			s.fail(w, r, err)
			return
		}
		if errors.Is(err, core.ErrExists) && fieldOf(err) == "" {
			form.add("email", "Another account uses this email address")
		} else {
			form.fail(err, "Your profile could not be saved.", nil)
		}
		s.renderProfileForm(w, r, form, form.status(err))
		return
	}
	http.Redirect(w, r, "/settings/profile?saved=profile#profile", http.StatusSeeOther)
}

// changePassword checks and applies the password form. The entered
// passwords are never sent back to the page.
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	form := newForm("password", r)
	old := r.PostFormValue("oldPassword")
	next := r.PostFormValue("newPassword")
	if old == "" {
		form.add("oldPassword", "Enter your current password")
	}
	switch {
	case next == "":
		form.add("newPassword", "Enter a new password")
	case len([]rune(next)) < 8:
		form.add("newPassword", "Use at least 8 characters")
	}
	if r.PostFormValue("newPasswordConfirm") != next {
		form.add("newPasswordConfirm", "Enter the same password as in New password")
	}
	if form.invalid() {
		s.renderProfileForm(w, r, form, http.StatusBadRequest)
		return
	}
	if err := s.backend.ChangePassword(r.Context(), actor, old, next); err != nil {
		if hardFailure(err) {
			s.fail(w, r, err)
			return
		}
		form.fail(err, "The password could not be changed.", map[string]string{"password": "newPassword"})
		s.renderProfileForm(w, r, form, form.status(err))
		return
	}
	http.Redirect(w, r, "/settings/profile?saved=password#password", http.StatusSeeOther)
}

// renderProfileForm renders the profile page again with a refused form.
func (s *Server) renderProfileForm(w http.ResponseWriter, r *http.Request, form *formState, status int) {
	p := s.newPage(r, "Profile", "profile")
	p.Set(formDataKey, form)
	s.Render(w, r, status, "profile", p)
}

// handleSessions renders the signed-in browsers and devices.
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	p := s.newPage(r, "Sessions", "sessions")
	sessions, err := s.backend.ListSessions(r.Context(), actor)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p.Set("Sessions", sessions)
	s.Render(w, r, http.StatusOK, "sessions", p)
}

// handleSessionsPost signs one session out.
func (s *Server) handleSessionsPost(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if err := s.formValues(r); err != nil {
		s.fail(w, r, core.Invalid("form", "the form could not be read"))
		return
	}
	s.revokeSession(w, r, actor)
}

// revokeSession signs one session out, after a confirmation page when
// scripting is off. The signed-out row disappears, so no message shows.
func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	id := strings.TrimSpace(r.PostFormValue("sessionId"))
	if needsConfirm(r) {
		name := "this session"
		if sessions, err := s.backend.ListSessions(r.Context(), actor); err == nil {
			for _, se := range sessions {
				if se.ID == id {
					name = browserName(se.UserAgent)
				}
			}
		}
		s.renderConfirm(w, r, "sessions", nil, &confirmView{
			Title:        "Sign out " + name + "?",
			Body:         "That browser has to sign in again. Notes it saved offline stay on it until then.",
			ConfirmLabel: "Sign out",
			Action:       "/settings/sessions",
			CancelURL:    "/settings/sessions",
			Fields:       confirmFields(r.PostForm),
		})
		return
	}
	if err := s.backend.RevokeSession(r.Context(), actor, id); err != nil {
		s.formFail(w, r, "/settings/sessions", err, "The session could not be signed out.")
		return
	}
	http.Redirect(w, r, "/settings/sessions", http.StatusSeeOther)
}

// browserName shortens a user agent to a browser and system, such as
// "Firefox on Linux". An unknown agent keeps its text.
func browserName(ua string) string {
	ua = strings.TrimSpace(ua)
	if ua == "" {
		return "Unknown browser"
	}
	browser := ""
	for _, b := range []struct{ token, name string }{
		{"Edg/", "Edge"}, {"OPR/", "Opera"}, {"Firefox/", "Firefox"}, {"Chrome/", "Chrome"},
		{"Safari/", "Safari"}, {"curl/", "curl"},
	} {
		if strings.Contains(ua, b.token) {
			browser = b.name
			break
		}
	}
	system := ""
	for _, o := range []struct{ token, name string }{
		{"Android", "Android"}, {"iPhone", "iOS"}, {"iPad", "iPadOS"}, {"Windows", "Windows"},
		{"Mac OS X", "macOS"}, {"CrOS", "ChromeOS"}, {"Linux", "Linux"},
	} {
		if strings.Contains(ua, o.token) {
			system = o.name
			break
		}
	}
	switch {
	case browser != "" && system != "":
		return browser + " on " + system
	case browser != "":
		return browser
	}
	return ua
}

// BrowserName exposes browserName to templates.
func (p *Page) BrowserName(ua string) string { return browserName(ua) }

// handleOfflineFiles renders the offline files settings of this browser.
// The page script fills it in, because the cache lives in the browser.
func (s *Server) handleOfflineFiles(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	s.Render(w, r, http.StatusOK, "offline/files", s.newPage(r, "Offline files", "offline"))
}

// handleTokens renders the API token table and the create form.
func (s *Server) handleTokens(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	p := s.newPage(r, "API tokens", "tokens")
	p.Flash = r.URL.Query().Get("flash")
	if tokens, err := s.backend.ListAPITokens(r.Context(), actor); err == nil {
		p.Set("Tokens", tokens)
	}
	s.Render(w, r, http.StatusOK, "tokens", p)
}

// tokenLifetimes are the expiry choices of the token form, in days. 0 never
// expires.
var tokenLifetimes = map[string]int{"7": 7, "30": 30, "90": 90, "365": 365, "0": 0}

// handleTokensPost creates or revokes an API token. A new token is shown
// once in plaintext on the page rendered again and never stored.
func (s *Server) handleTokensPost(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if err := s.formValues(r); err != nil {
		s.fail(w, r, core.Invalid("form", "the form could not be read"))
		return
	}
	ctx := r.Context()
	if r.PostFormValue("action") == "revoke" {
		id := strings.TrimSpace(r.PostFormValue("tokenId"))
		if needsConfirm(r) {
			name := "this token"
			if tokens, err := s.backend.ListAPITokens(ctx, actor); err == nil {
				for _, t := range tokens {
					if t.ID == id {
						name = t.Name
					}
				}
			}
			s.renderConfirm(w, r, "tokens", nil, &confirmView{
				Title:        "Revoke " + name + "?",
				Body:         "Scripts that use it stop working at once. This cannot be undone.",
				ConfirmLabel: "Revoke",
				Danger:       true,
				Action:       "/settings/tokens",
				CancelURL:    "/settings/tokens",
				Fields:       confirmFields(r.PostForm),
			})
			return
		}
		if err := s.backend.RevokeAPIToken(ctx, actor, id); err != nil {
			s.formFail(w, r, "/settings/tokens", err, "The token could not be revoked.")
			return
		}
		http.Redirect(w, r, "/settings/tokens", http.StatusSeeOther)
		return
	}

	form := newForm("token", r)
	name := form.value("name")
	if name == "" {
		form.add("name", "Enter a name")
	}
	days, known := tokenLifetimes[form.value("ttlDays")]
	if !known {
		form.add("ttlDays", "Choose when the token expires")
	}
	p := s.newPage(r, "API tokens", "tokens")
	status := http.StatusOK
	if !form.invalid() {
		var ttl time.Duration
		if days > 0 {
			ttl = time.Duration(days) * 24 * time.Hour
		}
		record, plaintext, err := s.backend.CreateAPIToken(ctx, actor, name, ttl)
		switch {
		case err != nil && hardFailure(err):
			s.fail(w, r, err)
			return
		case err != nil:
			form.fail(err, "The token could not be created.", nil)
			status = form.status(err)
		default:
			p.Set("NewToken", plaintext)
			p.Set("NewTokenRecord", record)
		}
	} else {
		status = http.StatusBadRequest
	}
	if form.invalid() {
		p.Set(formDataKey, form)
	}
	if tokens, err := s.backend.ListAPITokens(ctx, actor); err == nil {
		p.Set("Tokens", tokens)
	}
	s.Render(w, r, status, "tokens", p)
}

// handleUserAI renders the tasks with the instance choice and the user's own
// routes, and the personal providers when the instance allows user keys.
func (s *Server) handleUserAI(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	p := s.newPage(r, "AI", "userai")
	p.Flash = r.URL.Query().Get("flash")
	v := s.buildAIView(r, actor, false, p.Settings)
	if v.ErrorForm != "" {
		// The refused form shows the message under its field, so the page
		// Banner does not repeat it (docs/primer.md 6.6).
		p.Error, p.ErrorField = "", ""
	}
	p.Set("AI", v)
	s.Render(w, r, http.StatusOK, "ai", p)
}

// handleUserAIPost saves or resets a personal route, or saves, refreshes or
// removes a personal provider.
func (s *Server) handleUserAIPost(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if err := s.formValues(r); err != nil {
		s.fail(w, r, core.Invalid("form", "the form could not be read"))
		return
	}
	switch r.PostFormValue("action") {
	case "route-save", "route-reset", "provider-save", "provider-delete", "delete-ask", "refresh", "test":
		s.aiPost(w, r, actor, "/settings/ai", "me")
	default:
		s.formFail(w, r, "/settings/ai", core.Invalid("action", "unknown AI action"), "That change could not be saved.")
	}
}

// providerFromForm builds a provider configuration from a submitted form.
func providerFromForm(r *http.Request) *core.AIProviderConfig {
	cfg := &core.AIProviderConfig{
		ID:              strings.TrimSpace(r.PostFormValue("providerId")),
		Name:            strings.TrimSpace(r.PostFormValue("name")),
		Preset:          strings.TrimSpace(r.PostFormValue("preset")),
		Kind:            core.AIProviderKind(strings.TrimSpace(r.PostFormValue("kind"))),
		BaseURL:         strings.TrimSpace(r.PostFormValue("baseUrl")),
		APIKey:          strings.TrimSpace(r.PostFormValue("apiKey")),
		Model:           strings.TrimSpace(r.PostFormValue("model")),
		EmbedModel:      strings.TrimSpace(r.PostFormValue("embedModel")),
		ImageModel:      strings.TrimSpace(r.PostFormValue("imageModel")),
		ImageGeneration: formBool(r, "imageGeneration"),
		ImageEditing:    formBool(r, "imageEditing"),
		Vision:          formBool(r, "vision"),
		Local:           formBool(r, "local"),
		MaxTokens:       formInt(r.PostFormValue("maxTokens"), 0),
		TimeoutSeconds:  formInt(r.PostFormValue("timeoutSeconds"), 0),
		Enabled:         !r.PostForm.Has("enabledField") || formBool(r, "enabled"),
	}
	if cfg.Preset != "" && cfg.Preset != "custom" {
		// The preset decides the protocol. A kind field left over from the
		// custom preset does not override it.
		cfg.Kind = ""
	}
	if v := strings.TrimSpace(r.PostFormValue("temperature")); v != "" {
		cfg.Temperature = parseFloat(v)
	}
	return cfg
}
