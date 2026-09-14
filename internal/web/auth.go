package web

import (
	"net/http"
	"strings"

	"github.com/gamemaker1/markspace/internal/core"
)

// handleSetup renders the first-run administrator form. When the instance
// already has a user it redirects to the login form.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if !s.setupNeeded(r) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	p := s.newPage(r, "Set up "+s.instanceName, "setup")
	p.Chrome = false
	s.Render(w, r, http.StatusOK, "setup", p)
}

// handleSetupPost creates the first administrator, signs them in and sends
// them to the home page.
func (s *Server) handleSetupPost(w http.ResponseWriter, r *http.Request) {
	if !s.setupNeeded(r) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := s.formValues(r); err != nil {
		s.fail(w, r, core.Invalid("form", "the form could not be read"))
		return
	}
	form := checkAccountForm("setup", r)
	title := "Set up " + s.instanceName
	if form.invalid() {
		s.renderAuthForm(w, r, "setup", title, form, http.StatusBadRequest)
		return
	}
	in := core.SignupInput{Email: form.value("email"), Name: form.value("name"), Password: r.PostFormValue("password")}
	if _, err := s.backend.Signup(r.Context(), in); err != nil {
		form.fail(err, "The account could not be created.", nil)
		s.renderAuthForm(w, r, "setup", title, form, form.status(err))
		return
	}
	if !s.startSession(w, r, in.Email, in.Password) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// checkAccountForm checks the name, email and password fields of the setup
// and signup forms.
func checkAccountForm(name string, r *http.Request) *formState {
	form := newForm(name, r)
	if form.value("name") == "" {
		form.add("name", "Enter your name")
	}
	switch email := form.value("email"); {
	case email == "":
		form.add("email", "Enter your email address")
	case !strings.Contains(email, "@"):
		form.add("email", "Enter an email address such as ada@example.com")
	}
	password := r.PostFormValue("password")
	switch {
	case password == "":
		form.add("password", "Enter a password")
	case len([]rune(password)) < 8:
		form.add("password", "Use at least 8 characters")
	}
	if r.PostFormValue("passwordConfirm") != password {
		form.add("passwordConfirm", "Enter the same password as in Password")
	}
	return form
}

// handleLogin renders the sign-in form.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.setupNeeded(r) {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	p := s.newPage(r, "Sign in", "login")
	p.Chrome = false
	p.Set("Next", safeNext(r.URL.Query().Get("next")))
	s.Render(w, r, http.StatusOK, "login", p)
}

// handleLoginPost verifies the password, sets the session cookie and
// redirects to the next parameter when it is a local path. Refused
// credentials show a Banner at the top of the form, because the message
// belongs to neither field alone.
func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	if err := s.formValues(r); err != nil {
		s.fail(w, r, core.Invalid("form", "the form could not be read"))
		return
	}
	form := newForm("login", r)
	email := form.value("email")
	password := r.PostFormValue("password")
	next := safeNext(r.PostFormValue("next"))
	if email == "" {
		form.add("email", "Enter your email address")
	}
	if password == "" {
		form.add("password", "Enter your password")
	}
	status := http.StatusBadRequest
	if !form.invalid() {
		if s.startSession(w, r, email, password) {
			http.Redirect(w, r, next, http.StatusSeeOther)
			return
		}
		form.message = "That email and password do not match an account."
		status = http.StatusUnauthorized
	}
	p := s.newPage(r, "Sign in", "login")
	p.Chrome = false
	p.Set(formDataKey, form)
	p.Set("Next", next)
	s.Render(w, r, status, "login", p)
}

// handleSignup renders the registration form. It honours an invite query
// parameter and refuses when signup is closed and no invite is present.
func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	if s.setupNeeded(r) {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	p := s.newPage(r, "Create an account", "signup")
	p.Chrome = false
	invite := strings.TrimSpace(r.URL.Query().Get("invite"))
	p.Set("Invite", invite)
	p.Set("Next", safeNext(r.URL.Query().Get("next")))
	if !s.signupOpen(p, invite) {
		p.Set("Closed", true)
		s.Render(w, r, http.StatusForbidden, "signup", p)
		return
	}
	s.Render(w, r, http.StatusOK, "signup", p)
}

// handleSignupPost creates an account, signs it in and redirects.
func (s *Server) handleSignupPost(w http.ResponseWriter, r *http.Request) {
	if err := s.formValues(r); err != nil {
		s.fail(w, r, core.Invalid("form", "the form could not be read"))
		return
	}
	invite := strings.TrimSpace(r.PostFormValue("invite"))
	next := safeNext(r.PostFormValue("next"))
	render := func(status int, form *formState, closed bool) {
		p := s.newPage(r, "Create an account", "signup")
		p.Chrome = false
		p.Set(formDataKey, form)
		p.Set("Invite", invite)
		p.Set("Next", next)
		if closed {
			p.Set("Closed", true)
		}
		s.Render(w, r, status, "signup", p)
	}

	probe := s.newPage(r, "", "")
	if !s.signupOpen(probe, invite) {
		render(http.StatusForbidden, newForm("signup", r), true)
		return
	}
	form := checkAccountForm("signup", r)
	if form.invalid() {
		render(http.StatusBadRequest, form, false)
		return
	}
	in := core.SignupInput{Email: form.value("email"), Name: form.value("name"), Password: r.PostFormValue("password"), InviteToken: invite}
	if _, err := s.backend.Signup(r.Context(), in); err != nil {
		form.fail(err, "The account could not be created.", nil)
		render(form.status(err), form, false)
		return
	}
	if !s.startSession(w, r, in.Email, in.Password) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// handleLogout clears the session cookie and returns to the login form. It
// accepts POST only, so a link cannot sign a reader out.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
		if err := s.backend.Logout(r.Context(), c.Value); err != nil {
			s.log.Debug("web: session delete failed", "err", err)
		}
	}
	s.auth.ClearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// startSession logs in and writes the session cookie. It reports false when
// the credentials are refused.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, email, password string) bool {
	_, token, err := s.backend.Login(r.Context(), email, password, r.UserAgent(), s.auth.ClientIP(r))
	if err != nil {
		s.log.Debug("web: login refused", "email", email, "err", err)
		return false
	}
	s.auth.SetSessionCookie(w, token, s.sessionTTL)
	return true
}

// setupNeeded reports whether the instance has no users yet.
func (s *Server) setupNeeded(r *http.Request) bool {
	need, err := s.backend.NeedsSetup(r.Context())
	if err != nil {
		s.log.Debug("web: setup probe failed", "err", err)
		return false
	}
	return need
}

// signupOpen reports whether a registration may proceed. An invite always
// opens the form, and otherwise the instance setting decides. When settings
// are unreadable the form stays open, because the backend enforces the rule
// again on Signup.
func (s *Server) signupOpen(p *Page, invite string) bool {
	if invite != "" {
		return true
	}
	if p.Settings == nil {
		return true
	}
	return p.Settings.AllowSignup
}

// renderAuthForm renders an interstitial form again with its problems and
// the entered values.
func (s *Server) renderAuthForm(w http.ResponseWriter, r *http.Request, page, title string, form *formState, status int) {
	p := s.newPage(r, title, page)
	p.Chrome = false
	p.Set(formDataKey, form)
	s.Render(w, r, status, page, p)
}

// authMessage prefers a validation message over a generic fallback.
func authMessage(err error, fallback string) string {
	if detail := detailOf(err); detail != "" {
		return detail
	}
	return fallback
}
