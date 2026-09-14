package web

import (
	"errors"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gamemaker1/markspace/internal/core"
)

// adminUserPageSize is how many accounts one page of the user table shows.
// Primer paginates tables from 20 rows (docs/primer.md 6.11).
const adminUserPageSize = 20

// adminUserResults are the announcements after a users page action. The
// tables show the result, so no Banner repeats it.
var adminUserResults = map[string]string{
	"create": "Account created.", "invite": "Invite created.", "invite-revoke": "Invite revoked.",
	"promote": "Account updated.", "demote": "Account updated.", "activate": "Account updated.",
	"deactivate": "Account updated.", "delete": "Account deleted.",
	"token-revoke": "Token revoked. Restart the server to issue a new one.",
}

// adminUsersForm is the state of the create or invite Dialog: the open
// Dialog, the submitted values and the refusal.
type adminUsersForm struct {
	Dialog string
	Values url.Values
	// Errors maps a field name to its validation message.
	Errors map[string]string
	// Message is a refusal that belongs to no field.
	Message string
}

// Value returns a submitted value. A password is never sent back.
func (f *adminUsersForm) Value(name string) string {
	if f == nil || name == "password" {
		return ""
	}
	return f.Values.Get(name)
}

// refusal reports whether err is a refused form, which the page renders
// again with its values, rather than a failure the error page shows.
func refusal(err error) bool {
	switch statusFor(err) {
	case http.StatusUnauthorized, http.StatusNotFound, http.StatusInternalServerError:
		return false
	}
	return true
}

// refusalMessage is the validation message of a refused form, starting with
// a capital letter.
func refusalMessage(err error, fallback string) string {
	message := detailOf(err)
	switch {
	case errors.Is(err, core.ErrForbidden):
		message = "You do not have permission to do that."
	case message == "" && (errors.Is(err, core.ErrConflict) || errors.Is(err, core.ErrExists)):
		message = "That already exists, or it changed in the meantime."
	case message == "":
		message = fallback
	}
	if r, size := utf8.DecodeRuneInString(message); size > 0 {
		message = string(unicode.ToUpper(r)) + message[size:]
	}
	return message
}

// handleAdmin renders the instance statistics.
func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	p := s.newPage(r, "Administration", "admin")
	if stats, err := s.backend.Stats(r.Context(), actor); err == nil {
		p.Set("Stats", stats)
	}
	s.Render(w, r, http.StatusOK, "admin", p)
}

// handleAdminUsers renders the account table and the pending invites. The
// dialog query value opens the create or invite Dialog, which is also the
// way to reach it without JavaScript.
func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	form := &adminUsersForm{Errors: map[string]string{}}
	switch d := r.URL.Query().Get("dialog"); d {
	case "create", "invite":
		form.Dialog = d
	}
	s.renderAdminUsers(w, r, actor, http.StatusOK, form)
}

// renderAdminUsers renders the users page with the state of its Dialogs.
func (s *Server) renderAdminUsers(w http.ResponseWriter, r *http.Request, actor *core.Actor, status int, form *adminUsersForm) {
	ctx := r.Context()
	p := s.newPage(r, "Users", "admin")
	p.Set("Form", form)
	p.Set("Result", adminUserResults[r.URL.Query().Get("done")])
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	offset := formInt(r.URL.Query().Get("offset"), 0)
	if offset < 0 {
		offset = 0
	}
	users, total, err := s.backend.ListUsers(ctx, actor, query, adminUserPageSize, offset)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p.Set("Users", users)
	p.Set("Search", query)
	p.Set("Pagination", listPagination("/admin/users", query, offset, adminUserPageSize, len(users), total))
	if invites, err := s.backend.ListInvites(ctx, actor); err == nil {
		p.Set("Invites", invites)
	}
	if spaces, err := s.backend.ListSpaces(ctx, actor); err == nil {
		p.Set("InviteSpaces", spaces)
	}
	if tokens, err := s.backend.ListSystemTokens(ctx, actor); err == nil {
		p.Set("SystemTokens", tokens)
	}
	p.Set("Roles", core.Roles)
	s.Render(w, r, status, "admin/users", p)
}

// handleAdminUsersPost creates an account, sends an invite, promotes or
// demotes an admin, deactivates an account, revokes an invite or revokes a
// system API token.
func (s *Server) handleAdminUsersPost(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := s.formValues(r); err != nil {
		s.fail(w, r, core.Invalid("form", "the form could not be read"))
		return
	}
	ctx := r.Context()
	userID := strings.TrimSpace(r.PostFormValue("userId"))
	action := r.PostFormValue("action")
	var err error
	switch action {
	case "create":
		email := strings.TrimSpace(r.PostFormValue("email"))
		name := strings.TrimSpace(r.PostFormValue("name"))
		password := r.PostFormValue("password")
		admin := formBool(r, "isAdmin")
		active := true
		switch {
		case name == "":
			err = core.Invalid("name", "enter a name")
		case email == "":
			err = core.Invalid("email", "enter an email address")
		case len(password) < 8:
			err = core.Invalid("password", "use at least 8 characters")
		default:
			_, err = s.backend.CreateUser(ctx, actor, core.UserInput{
				Email: &email, Name: &name, Password: &password,
				IsAdmin: &admin, IsActive: &active,
			})
		}
	case "invite":
		in := core.InviteInput{
			Email:   strings.TrimSpace(r.PostFormValue("email")),
			SpaceID: strings.TrimSpace(r.PostFormValue("spaceId")),
			Role:    core.Role(strings.TrimSpace(r.PostFormValue("role"))),
			IsAdmin: formBool(r, "isAdmin"),
		}
		days := formInt(r.PostFormValue("ttlDays"), 0)
		switch {
		case in.Email == "":
			err = core.Invalid("email", "enter an email address")
		case strings.TrimSpace(r.PostFormValue("ttlDays")) != "" && days < 1:
			err = core.Invalid("ttlDays", "enter 1 or more days")
		default:
			if days > 0 {
				in.TTL = time.Duration(days) * 24 * time.Hour
			}
			_, err = s.backend.CreateInvite(ctx, actor, in)
		}
	case "invite-revoke":
		err = s.backend.RevokeInvite(ctx, actor, strings.TrimSpace(r.PostFormValue("inviteId")))
	case "promote", "demote":
		admin := action == "promote"
		_, err = s.backend.UpdateUser(ctx, actor, userID, core.UserInput{IsAdmin: &admin})
	case "activate", "deactivate":
		active := action == "activate"
		_, err = s.backend.UpdateUser(ctx, actor, userID, core.UserInput{IsActive: &active})
	case "delete":
		err = s.backend.DeleteUser(ctx, actor, userID)
	case "token-revoke":
		err = s.backend.RevokeSystemToken(ctx, actor, strings.TrimSpace(r.PostFormValue("tokenId")))
	default:
		err = core.Invalid("action", "unknown user action")
	}
	if err != nil && (action == "create" || action == "invite") && refusal(err) {
		// The Dialog opens again with the submitted values and the message
		// under its field (docs/primer.md 6.6).
		form := &adminUsersForm{Dialog: action, Values: r.PostForm, Errors: map[string]string{}}
		message := refusalMessage(err, "That could not be saved.")
		if field := fieldOf(err); field != "" && field != "form" {
			form.Errors[field] = message
		} else {
			form.Message = message
		}
		s.renderAdminUsers(w, r, actor, statusFor(err), form)
		return
	}
	if err != nil {
		s.formFail(w, r, "/admin/users", err, "That change could not be saved.")
		return
	}
	http.Redirect(w, r, "/admin/users?done="+queryValue(action), http.StatusSeeOther)
}

// handleAdminAI renders the tasks, the model catalog and the providers.
func (s *Server) handleAdminAI(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	p := s.newPage(r, "AI", "admin")
	p.Flash = r.URL.Query().Get("flash")
	v := s.buildAIView(r, actor, true, p.Settings)
	if v.ErrorForm != "" {
		// The refused form shows its own message, so the page Banner is not
		// repeated.
		p.Error, p.ErrorField = "", ""
	}
	p.Set("AI", v)
	s.Render(w, r, http.StatusOK, "admin/ai", p)
}

// handleAdminAIPost runs one action of the admin AI page. The page script
// uses the JSON API for the same changes, so these forms are the fallback
// without JavaScript.
func (s *Server) handleAdminAIPost(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := s.formValues(r); err != nil {
		s.fail(w, r, core.Invalid("form", "the form could not be read"))
		return
	}
	s.aiPost(w, r, actor, "/admin/ai", core.RouteScopeInstance)
}

// aiPost runs a POST action shared by /admin/ai and /settings/ai. scope is
// the route scope the page edits.
func (s *Server) aiPost(w http.ResponseWriter, r *http.Request, actor *core.Actor, back, scope string) {
	ctx := r.Context()
	admin := scope == core.RouteScopeInstance
	id := strings.TrimSpace(r.PostFormValue("providerId"))
	var err error
	flash := ""
	anchor := "#providers"
	// failBack is where a refused form returns. It reopens the form that was
	// sent, so its message shows beside the fields.
	failBack := ""
	switch r.PostFormValue("action") {
	case "save", "provider-save":
		failBack = back + "?add=1#providers"
		if id != "" {
			failBack = back + "?edit=" + queryValue(id) + "#providers"
		}
		cfg := providerFromForm(r)
		if !admin {
			cfg.OwnerID = actor.UserID()
		}
		var saved *core.AIProviderConfig
		if saved, err = s.backend.SaveAIProvider(ctx, actor, cfg); err == nil {
			flash = "Provider saved."
			if saved.RefreshError != "" {
				flash = "Provider saved. The model listing failed: " + saved.RefreshError
			} else if saved.ModelCount > 0 {
				flash = "Provider saved. " + pluralize(saved.ModelCount, "model listed.", "models listed.")
			}
		}
	case "delete", "provider-delete":
		err = s.backend.DeleteAIProvider(ctx, actor, id)
		flash = "Provider removed."
	case "test":
		if err = s.backend.TestAIProvider(ctx, actor, id); err == nil {
			flash = "Provider responded."
		}
	case "refresh":
		var models []*core.AIModel
		if models, err = s.backend.RefreshAIProvider(ctx, actor, id); err == nil {
			flash = pluralize(len(models), "model listed.", "models listed.")
		}
	case "reindex":
		var n int
		if n, err = s.backend.ReindexEmbeddings(ctx, actor, strings.TrimSpace(r.PostFormValue("spaceId"))); err == nil {
			flash = pluralize(n, "note embedded.", "notes embedded.")
		}
		anchor = ""
	case "prices-refresh":
		var res *core.AIPriceRefresh
		if res, err = s.backend.RefreshAIPrices(ctx, actor); err == nil {
			flash = priceRefreshFlash(res)
		}
		anchor = "#models"
	case "route-save":
		// Save stays on the page and shows its result beside the Save button
		// of the open task, so no Banner follows.
		task := strings.TrimSpace(r.PostFormValue("task"))
		failBack = back + "?task=" + queryValue(task) + "#task-" + task
		if _, err = s.backend.SaveAIRoute(ctx, actor, scope, task, routeTargetsFromForm(r)); err == nil {
			http.Redirect(w, r, back+"?task="+queryValue(task)+"&saved=1#task-"+task, http.StatusSeeOther)
			return
		}
	case "route-reset":
		task := strings.TrimSpace(r.PostFormValue("task"))
		failBack = back + "?task=" + queryValue(task) + "#task-" + task
		err = s.backend.DeleteAIRoute(ctx, actor, task)
		flash = taskLabel(task) + " uses the instance choice."
		anchor = "#tasks"
	case "model-toggle", "model-price", "model-price-clear":
		if !admin {
			err = core.ErrForbidden
			break
		}
		model := strings.TrimSpace(r.PostFormValue("model"))
		action := r.PostFormValue("action")
		if action == "model-price" && r.PostFormValue("clear") != "" {
			action = "model-price-clear"
		}
		if action != "model-toggle" {
			failBack = back + "?price=" + queryValue(modelKey(id, model)) + "#models"
		}
		var in core.AIModelUpdate
		switch action {
		case "model-toggle":
			enabled := formBool(r, "enabled")
			in.Enabled = &enabled
			flash = model + " is " + map[bool]string{true: "enabled.", false: "disabled."}[enabled]
		case "model-price":
			var price *core.AIPrice
			if price, err = priceFromForm(r); err != nil {
				break
			}
			in.SetPrice, in.PriceOverride = true, price
			flash = "Price of " + model + " saved."
		default:
			in.SetPrice = true
			flash = "Price of " + model + " returns to the fetched price."
		}
		if err == nil {
			_, err = s.backend.UpdateAIModel(ctx, actor, id, model, in)
		}
		anchor = "#models"
	default:
		err = core.Invalid("action", "unknown AI action")
	}
	if err != nil {
		if failBack == "" {
			failBack = back + anchor
		}
		s.formFail(w, r, failBack, err, "That change could not be saved.")
		return
	}
	http.Redirect(w, r, back+"?flash="+queryValue(flash)+anchor, http.StatusSeeOther)
}

// routeTargetsFromForm reads the targets of a route form. Each target value
// is providerId|model. An add value appends a target, and an op value of
// up:N, down:N or remove:N changes the list before it is saved.
func routeTargetsFromForm(r *http.Request) []core.AIRouteTarget {
	values := append([]string(nil), r.PostForm["target"]...)
	if add := strings.TrimSpace(r.PostFormValue("add")); add != "" {
		values = append(values, add)
	}
	if op, idx, found := strings.Cut(r.PostFormValue("op"), ":"); found {
		i := formInt(idx, -1)
		if i >= 0 && i < len(values) {
			switch op {
			case "up":
				if i > 0 {
					values[i-1], values[i] = values[i], values[i-1]
				}
			case "down":
				if i < len(values)-1 {
					values[i+1], values[i] = values[i], values[i+1]
				}
			case "remove":
				values = append(values[:i], values[i+1:]...)
			}
		}
	}
	out := make([]core.AIRouteTarget, 0, len(values))
	for _, v := range values {
		providerID, model, ok := strings.Cut(v, "|")
		providerID, model = strings.TrimSpace(providerID), strings.TrimSpace(model)
		if !ok || providerID == "" || model == "" {
			continue
		}
		out = append(out, core.AIRouteTarget{ProviderID: providerID, Model: model})
	}
	return out
}

// priceFromForm reads a price override from inputPerMTok and outputPerMTok.
func priceFromForm(r *http.Request) (*core.AIPrice, error) {
	read := func(name string) (float64, error) {
		raw := strings.TrimSpace(r.PostFormValue(name))
		if raw == "" {
			return 0, nil
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return 0, core.Invalid(name, "enter a price of 0 or more in dollars per million tokens")
		}
		return v, nil
	}
	in, err := read("inputPerMTok")
	if err != nil {
		return nil, err
	}
	out, err := read("outputPerMTok")
	if err != nil {
		return nil, err
	}
	return &core.AIPrice{InputPerMTok: in, OutputPerMTok: out}, nil
}

// priceRefreshFlash summarises a price catalog download.
func priceRefreshFlash(res *core.AIPriceRefresh) string {
	var parts []string
	for _, src := range res.Sources {
		label := sourceName(src.Source)
		if src.Error != "" {
			parts = append(parts, label+": "+src.Error+".")
			continue
		}
		parts = append(parts, label+": "+pluralize(src.Models, "model.", "models."))
	}
	parts = append(parts, pluralize(res.Repriced, "price changed.", "prices changed."))
	return strings.Join(parts, " ")
}

// handleAdminSettings renders the instance settings form.
func (s *Server) handleAdminSettings(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	p := s.newPage(r, "Instance settings", "admin")
	p.Set("FormErrors", map[string]string{})
	if settings, err := s.backend.GetSettings(r.Context(), actor); err == nil {
		p.Settings = settings
	}
	s.Render(w, r, http.StatusOK, "admin/settings", p)
}

// adminSettingsFields are the fields of the settings form that show their
// validation message under the field.
var adminSettingsFields = map[string]bool{"instanceName": true, "defaultSpaceId": true}

// handleAdminSettingsPost saves the instance settings, or rereads every
// markdown file into the index.
func (s *Server) handleAdminSettingsPost(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := s.formValues(r); err != nil {
		s.fail(w, r, core.Invalid("form", "the form could not be read"))
		return
	}
	ctx := r.Context()
	if r.PostFormValue("action") == "reindex" {
		n, err := s.backend.Reindex(ctx, actor)
		if err != nil {
			s.formFail(w, r, "/admin/settings", err, "The index could not be rebuilt.")
			return
		}
		http.Redirect(w, r, "/admin/settings?indexed="+queryValue(pluralize(n, "note indexed.", "notes indexed."))+"#index-title", http.StatusSeeOther)
		return
	}
	current, err := s.backend.GetSettings(ctx, actor)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	next := *current
	if r.PostForm.Has("instanceName") {
		next.InstanceName = strings.TrimSpace(r.PostFormValue("instanceName"))
	}
	next.AllowSignup = formBool(r, "allowSignup")
	next.AIEnabled = formBool(r, "aiEnabled")
	next.AIAllowUserKeys = formBool(r, "aiAllowUserKeys")
	next.AIAllowUserRoutes = formBool(r, "aiAllowUserRoutes")
	if r.PostForm.Has("defaultSpaceId") {
		next.DefaultSpaceID = strings.TrimSpace(r.PostFormValue("defaultSpaceId"))
	}
	if r.PostForm.Has("instanceName") && next.InstanceName == "" {
		err = core.Invalid("instanceName", "enter an instance name")
	} else {
		_, err = s.backend.UpdateSettings(ctx, actor, &next)
	}
	if err != nil && refusal(err) {
		// The form renders again with the submitted values, and the message
		// shows under its field or in a Banner (docs/primer.md 6.1 and 6.6).
		p := s.newPage(r, "Instance settings", "admin")
		p.Settings = &next
		errs := map[string]string{}
		message := refusalMessage(err, "The settings could not be saved.")
		if field := fieldOf(err); adminSettingsFields[field] {
			errs[field] = message
		} else {
			p.Error = message
		}
		p.Set("FormErrors", errs)
		s.Render(w, r, statusFor(err), "admin/settings", p)
		return
	}
	if err != nil {
		s.formFail(w, r, "/admin/settings", err, "The settings could not be saved.")
		return
	}
	http.Redirect(w, r, "/admin/settings?done=settings", http.StatusSeeOther)
}

// aiView is everything the admin and settings AI pages render.
type aiView struct {
	// Admin selects the admin page. The settings page edits the actor's own
	// routes and personal providers.
	Admin           bool
	Action          string
	Scope           string
	AllowUserRoutes bool
	AllowUserKeys   bool

	Tasks     []*aiTaskView
	Models    []*aiModelView
	Providers []*aiProviderView
	Presets   []aiPresetView
	Kinds     []core.AIProviderKind

	Filter            aiModelFilter
	ProviderOptions   []aiOption
	CapabilityOptions []aiOption
	ShownModels       int

	// Edit and Price name the provider and the model whose Dialog opens on
	// load. AddOpen opens the add provider Dialog.
	Edit    string
	Price   string
	AddOpen bool

	// Error and ErrorField come from a refused form. ErrorForm names that
	// form: a provider id, "new", "price" or "task:<task>".
	Error      string
	ErrorField string
	ErrorForm  string
}

// aiFormFields are the fields the AI forms show a message under. An error
// for any other field shows at the top of its form.
var aiFormFields = map[string]bool{
	"name": true, "apiKey": true, "baseUrl": true, "model": true, "embedModel": true, "imageModel": true,
	"temperature": true, "maxTokens": true, "timeoutSeconds": true, "inputPerMTok": true, "outputPerMTok": true,
}

// FormError returns the message of a refused form when it belongs to the
// named field of the named form.
func (v *aiView) FormError(form, field string) string {
	if v == nil || v.Error == "" || v.ErrorForm != form || v.ErrorField != field {
		return ""
	}
	return v.Error
}

// FormBanner returns the message of a refused form when it belongs to the
// named form and to none of its fields.
func (v *aiView) FormBanner(form string) string {
	if v == nil || v.Error == "" || v.ErrorForm != form || aiFormFields[v.ErrorField] {
		return ""
	}
	return v.Error
}

// aiOption is one choice of a select.
type aiOption struct{ Value, Label string }

// aiModelFilter is the filter row of the model table.
type aiModelFilter struct{ Capability, Provider, Q string }

// aiTaskView is one task row.
type aiTaskView struct {
	Task  string
	Label string
	Hint  string
	// Targets is the list the page edits: the instance route on the admin
	// page and the actor's own route on the settings page.
	Targets []aiChip
	// Instance is the instance route, shown greyed on the settings page.
	Instance []aiChip
	HasOwn   bool
	// Choices lists capable models not yet in Targets, for the form that
	// works without JavaScript.
	Choices []aiChip
	Open    bool
	// Saved shows the result of a route saved without JavaScript.
	Saved bool
}

// aiChip names one model of a route.
type aiChip struct {
	Value      string
	ProviderID string
	Model      string
	Label      string
	Provider   string
	Price      string
	Missing    bool
}

// aiModelView is one row of the model table.
type aiModelView struct {
	*core.AIModel
	Key         string
	Label       string
	Context     string
	InputPrice  string
	OutputPrice string
	Source      string
	Caps        string
	Search      string
	InputMods   []aiOption
	OutputMods  []aiOption
	OverrideIn  string
	OverrideOut string
	Hidden      bool
}

// aiProviderView is one row of the provider table.
type aiProviderView struct {
	*core.AIProviderConfig
	PresetName string
	Status     string
	Refreshed  string
	Failed     bool
	FromFile   bool
	Editing    bool
	Fields     string
}

// aiPresetView is one preset of the add form.
type aiPresetView struct {
	core.AIPreset
	FieldList string
	Sub       string
}

// taskLabels names the built-in tasks and says what uses them.
var taskLabels = map[string][2]string{
	core.RouteDefault:       {"Default", "Anything without its own models."},
	core.RouteEdit:          {"Edit", "Replace and insert, Improve, Shorten, Fix, Tone and Translate."},
	core.RouteAnswer:        {"Answer", "Ask bar answers, Explain and Summarize."},
	core.RouteChat:          {"Chat", "The AI tab conversation."},
	core.RouteVision:        {"Vision", "Describe, Alt text, Extract text and requests with images."},
	core.RouteImageGenerate: {"Generate image", "Generate image and Variations."},
	core.RouteImageEdit:     {"Edit image", "Edit image."},
	core.RouteEmbeddings:    {"Embeddings", "Semantic search and the index. Only the first model is used."},
}

// taskLabel returns the display name of a task.
func taskLabel(task string) string {
	if l, ok := taskLabels[task]; ok {
		return l[0]
	}
	if rest, ok := strings.CutPrefix(task, "plugin."); ok {
		id, purpose, _ := strings.Cut(rest, ".")
		return "Plugin " + id + ", " + purpose
	}
	return task
}

// sourceName returns the display name of a price source.
func sourceName(source string) string {
	switch source {
	case core.PriceSourceOpenRouter:
		return "OpenRouter"
	case core.PriceSourceLiteLLM:
		return "LiteLLM"
	case core.PriceSourceProvider:
		return "Provider"
	case core.PriceSourceLocal:
		return "Local"
	case core.PriceSourceOverride:
		return "Override"
	}
	return "Unknown"
}

// shortMonths are the month names of a price source date.
var shortMonths = [...]string{"Jan", "Feb", "Mar", "Apr", "May", "June", "July", "Aug", "Sept", "Oct", "Nov", "Dec"}

// shortDate renders a date as "13 Sept".
func shortDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return strconv.Itoa(t.Day()) + " " + shortMonths[t.Month()-1]
}

// usd renders a price in dollars with at least two decimals when it has any.
func usd(v float64) string {
	v = math.Round(v*1e6) / 1e6
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if dot := strings.IndexByte(s, '.'); dot >= 0 && len(s)-dot-1 < 2 {
		s += strings.Repeat("0", 2-(len(s)-dot-1))
	}
	return "$" + s
}

// contextLabel renders a context length as 128k or 1M.
func contextLabel(n int) string {
	switch {
	case n <= 0:
		return ""
	case n >= 1_000_000:
		return strings.TrimSuffix(strconv.FormatFloat(float64(n)/1e6, 'f', 1, 64), ".0") + "M"
	case n >= 1000:
		return strconv.Itoa((n+500)/1000) + "k"
	}
	return strconv.Itoa(n)
}

// priceLabels renders the effective input and output price of a model.
func priceLabels(m *core.AIModel) (string, string) {
	p := m.EffectivePrice
	if p == nil {
		return "Unknown", "Unknown"
	}
	if p.InputPerMTok == 0 && p.OutputPerMTok == 0 {
		return "Free", "Free"
	}
	return usd(p.InputPerMTok), usd(p.OutputPerMTok)
}

// chipPrice renders the price of a model for a chip or a search result.
func chipPrice(m *core.AIModel) string {
	in, out := priceLabels(m)
	switch in {
	case "Unknown":
		return "price unknown"
	case "Free":
		return "free"
	}
	return in + " in, " + out + " out per M"
}

// modalityLabels names the modalities of the model table.
var modalityLabels = map[string]string{
	core.ModalityText: "text", core.ModalityImage: "image", core.ModalityFile: "file",
	core.ModalityAudio: "audio", core.ModalityEmbedding: "embedding",
}

// modalities turns a modality list into icon names and accessible labels.
func modalities(list []string, dir string) []aiOption {
	out := make([]aiOption, 0, len(list))
	for _, m := range list {
		label, ok := modalityLabels[m]
		if !ok {
			continue
		}
		out = append(out, aiOption{Value: m, Label: label + " " + dir})
	}
	return out
}

// modelCaps lists the capability filter values a model matches.
func modelCaps(m *core.AIModel, p *core.AIProviderConfig) string {
	has := func(list []string, v string) bool {
		for _, x := range list {
			if x == v {
				return true
			}
		}
		return false
	}
	var caps []string
	kind := core.ProviderOpenAI
	if p != nil {
		kind = p.Kind
	}
	text := kind != core.ProviderComfyUI && has(m.Outputs, core.ModalityText)
	if text {
		caps = append(caps, "text")
		if has(m.Inputs, core.ModalityImage) {
			caps = append(caps, "vision")
		}
	}
	if has(m.Outputs, core.ModalityImage) && p != nil && (p.ImageGeneration || p.ImageEditing) {
		caps = append(caps, "image-output")
	}
	if has(m.Outputs, core.ModalityEmbedding) && kind != core.ProviderComfyUI && kind != core.ProviderAnthropic {
		caps = append(caps, "embedding")
	}
	return strings.Join(caps, " ")
}

// modelKey joins a provider id and a model id for form values and anchors.
func modelKey(providerID, model string) string { return providerID + "|" + model }

// aiCapabilityOptions are the choices of the capability filter.
var aiCapabilityOptions = []aiOption{
	{"", "Any capability"}, {"text", "Text"}, {"vision", "Image input"},
	{"image-output", "Image output"}, {"embedding", "Embeddings"},
}

// buildAIView loads the routes, the catalog, the providers and the presets
// for one of the AI pages. A failed load leaves its section empty.
func (s *Server) buildAIView(r *http.Request, actor *core.Actor, admin bool, settings *core.Settings) *aiView {
	ctx := r.Context()
	q := r.URL.Query()
	v := &aiView{
		Admin: admin, Action: "/settings/ai", Scope: "me", Kinds: core.AIProviderKinds,
		Edit: q.Get("edit"), Price: q.Get("price"), AddOpen: q.Get("add") == "1",
		Filter:            aiModelFilter{Capability: q.Get("capability"), Provider: q.Get("provider"), Q: strings.TrimSpace(q.Get("q"))},
		CapabilityOptions: aiCapabilityOptions,
	}
	if admin {
		v.Action, v.Scope = "/admin/ai", core.RouteScopeInstance
	}
	if msg := strings.TrimSpace(q.Get("error")); msg != "" {
		v.Error, v.ErrorField = msg, strings.TrimSpace(q.Get("field"))
		switch {
		case v.Edit != "":
			v.ErrorForm = v.Edit
		case v.AddOpen:
			v.ErrorForm = "new"
		case v.Price != "":
			v.ErrorForm = "price"
		case q.Get("task") != "":
			v.ErrorForm = "task:" + q.Get("task")
		}
	}
	if settings != nil {
		v.AllowUserKeys, v.AllowUserRoutes = settings.AIAllowUserKeys, settings.AIAllowUserRoutes
	}

	presetNames := map[string]core.AIPreset{}
	if presets, err := s.backend.ListAIPresets(ctx, actor); err == nil {
		for _, p := range presets {
			presetNames[p.ID] = p
			sub := "Prices from public lists"
			switch {
			case p.Local:
				sub = "Runs on your hardware"
			case p.PriceSource == core.PresetPricesProvider:
				sub = "Prices from the provider"
			case p.ID == "custom":
				sub = "Any server"
			}
			v.Presets = append(v.Presets, aiPresetView{AIPreset: p, FieldList: strings.Join(p.Fields, " "), Sub: sub})
		}
	} else {
		s.log.Debug("web: preset listing failed", "err", err)
	}

	providers := map[string]*core.AIProviderConfig{}
	if list, err := s.backend.ListAIProviders(ctx, actor); err == nil {
		for _, p := range list {
			providers[p.ID] = p
			if !admin && p.OwnerID != actor.UserID() {
				continue
			}
			if admin {
				v.ProviderOptions = append(v.ProviderOptions, aiOption{Value: p.ID, Label: p.Name})
			}
			pv := &aiProviderView{
				AIProviderConfig: p, PresetName: "Custom", Status: "Enabled",
				FromFile: p.Source == "file", Editing: p.ID == v.Edit,
			}
			if preset, ok := presetNames[p.Preset]; ok {
				pv.PresetName, pv.Fields = preset.Name, strings.Join(preset.Fields, " ")
			} else if p.Preset == "" {
				pv.PresetName = string(p.Kind)
			}
			switch {
			case !p.Enabled:
				pv.Status = "Disabled"
			case p.RefreshError != "":
				pv.Status, pv.Failed = "Listing failed", true
			}
			pv.Refreshed = "Never"
			if !p.RefreshedAt.IsZero() {
				pv.Refreshed = timeAgo(p.RefreshedAt)
			}
			v.Providers = append(v.Providers, pv)
		}
	} else {
		s.log.Debug("web: provider listing failed", "err", err)
	}

	byKey := map[string]*core.AIModel{}
	all, err := s.backend.ListAIModels(ctx, actor, core.AIModelQuery{})
	if err != nil {
		s.log.Debug("web: model listing failed", "err", err)
	}
	for _, m := range all {
		byKey[modelKey(m.ProviderID, m.ModelID)] = m
	}
	chip := func(t core.AIRouteTarget) aiChip {
		c := aiChip{Value: modelKey(t.ProviderID, t.Model), ProviderID: t.ProviderID, Model: t.Model, Label: t.Model, Provider: t.ProviderID}
		if p := providers[t.ProviderID]; p != nil {
			c.Provider = p.Name
		}
		if m := byKey[c.Value]; m != nil {
			if m.Name != "" {
				c.Label = m.Name
			}
			c.Price = chipPrice(m)
		} else {
			c.Missing, c.Price = true, "not in the catalog"
		}
		return c
	}
	chips := func(list []core.AIRouteTarget) []aiChip {
		out := make([]aiChip, 0, len(list))
		for _, t := range list {
			out = append(out, chip(t))
		}
		return out
	}

	routes, err := s.backend.ListAIRoutes(ctx, actor)
	if err != nil {
		s.log.Debug("web: route listing failed", "err", err)
		routes = &core.AIRoutes{Tasks: core.AIRouteTasks}
	}
	if !admin {
		v.AllowUserRoutes = routes.AllowUserRoutes
	}
	instance := map[string][]core.AIRouteTarget{}
	own := map[string][]core.AIRouteTarget{}
	tasks := append([]string(nil), core.AIRouteTasks...)
	seen := map[string]bool{}
	for _, t := range tasks {
		seen[t] = true
	}
	var extra []string
	for _, list := range [][]core.AIRoute{routes.Instance, routes.User} {
		for _, rt := range list {
			if !seen[rt.Task] {
				seen[rt.Task] = true
				extra = append(extra, rt.Task)
			}
		}
	}
	sort.Strings(extra)
	tasks = append(tasks, extra...)
	for _, rt := range routes.Instance {
		instance[rt.Task] = rt.Targets
	}
	for _, rt := range routes.User {
		own[rt.Task] = rt.Targets
	}
	for _, task := range tasks {
		tv := &aiTaskView{Task: task, Label: taskLabel(task), Hint: taskLabels[task][1], Instance: chips(instance[task]), Open: task == q.Get("task")}
		tv.Saved = tv.Open && q.Get("saved") == "1"
		if tv.Hint == "" {
			tv.Hint = "A purpose a plugin declares."
		}
		if admin {
			tv.Targets = tv.Instance
		} else {
			tv.HasOwn = len(own[task]) > 0
			tv.Targets = chips(own[task])
		}
		if admin || v.AllowUserRoutes {
			chosen := map[string]bool{}
			for _, c := range tv.Targets {
				chosen[c.Value] = true
			}
			if capable, err := s.backend.ListAIModels(ctx, actor, core.AIModelQuery{Capability: task}); err == nil {
				for _, m := range capable {
					key := modelKey(m.ProviderID, m.ModelID)
					if !chosen[key] && m.Enabled && m.Available {
						tv.Choices = append(tv.Choices, chip(core.AIRouteTarget{ProviderID: m.ProviderID, Model: m.ModelID}))
					}
				}
			}
		}
		v.Tasks = append(v.Tasks, tv)
	}

	if !admin {
		return v
	}
	for _, m := range all {
		p := providers[m.ProviderID]
		mv := &aiModelView{
			AIModel: m, Key: modelKey(m.ProviderID, m.ModelID), Label: orDefault(m.Name, m.ModelID),
			Context: contextLabel(m.ContextLength), Caps: modelCaps(m, p),
			InputMods: modalities(m.Inputs, "input"), OutputMods: modalities(m.Outputs, "output"),
			Search: strings.ToLower(m.ModelID + " " + m.Name + " " + m.Vendor + " " + m.ProviderName),
		}
		mv.InputPrice, mv.OutputPrice = priceLabels(m)
		mv.Source = sourceName(m.EffectiveSource)
		if (m.EffectiveSource == core.PriceSourceOpenRouter || m.EffectiveSource == core.PriceSourceLiteLLM) && !m.PriceFetchedAt.IsZero() {
			mv.Source += ", " + shortDate(m.PriceFetchedAt)
		}
		if o := m.PriceOverride; o != nil {
			mv.OverrideIn = strconv.FormatFloat(o.InputPerMTok, 'f', -1, 64)
			mv.OverrideOut = strconv.FormatFloat(o.OutputPerMTok, 'f', -1, 64)
		} else if e := m.EffectivePrice; e != nil {
			mv.OverrideIn = strconv.FormatFloat(e.InputPerMTok, 'f', -1, 64)
			mv.OverrideOut = strconv.FormatFloat(e.OutputPerMTok, 'f', -1, 64)
		}
		f := v.Filter
		mv.Hidden = (f.Provider != "" && m.ProviderID != f.Provider) ||
			(f.Capability != "" && !strings.Contains(" "+mv.Caps+" ", " "+f.Capability+" ")) ||
			(f.Q != "" && !strings.Contains(mv.Search, strings.ToLower(f.Q)))
		if !mv.Hidden {
			v.ShownModels++
		}
		v.Models = append(v.Models, mv)
	}
	return v
}
