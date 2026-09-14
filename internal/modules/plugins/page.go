package plugins

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
)

//go:embed assets/*
var assets embed.FS

// pageTemplate is parsed with api.PageFuncs and executed with
// api.ExecutePage, which supplies the web server's octicon function.
var pageTemplate = template.Must(template.New("admin.html").Funcs(api.PageFuncs()).Funcs(template.FuncMap{
	"dict": func(kv ...any) map[string]any {
		out := make(map[string]any, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			if k, ok := kv[i].(string); ok {
				out[k] = kv[i+1]
			}
		}
		return out
	},
}).ParseFS(assets, "assets/admin.html"))

// permissionText describes each permission group, from the docs/plugins.md table.
var permissionText = map[string]string{
	"items:read":  "Read the tree, notes, folders, files and file content",
	"items:write": "Create, update, move, rename, copy, upload, replace and trash items",
	"links:read":  "Resolve links and read backlinks and outbound links",
	"search":      "Run text and semantic search",
	"comments":    "List and create comments",
	"suggestions": "Create and withdraw suggested edits",
	"ai:complete": "Run AI text requests, charged to the plugin",
	"ai:embed":    "Create embeddings, charged to the plugin",
	"ai:vision":   "Send images to AI models, charged to the plugin",
	"ai:image":    "Generate and edit images, charged to the plugin",
	"web:fetch":   "Fetch web pages through the guarded fetcher",
	"jobs":        "Create background jobs and report their progress",
	"storage":     "Keep data in its own key-value and blob store",
	"ui":          "Show panels, pages, commands and notifications",
	"events":      "Receive events about changes in its spaces",
}

// Plugin grant roles offered on the page, in order.
var grantRoles = []roleOption{
	{Value: "", Label: "No access"},
	{Value: string(core.RoleViewer), Label: "Viewer"},
	{Value: string(core.RoleCommenter), Label: "Commenter"},
	{Value: string(core.RoleEditor), Label: "Editor"},
}

type roleOption struct {
	Value, Label string
	Selected     bool
}

type permView struct {
	ID, Description string
}

type option struct {
	Value, Label string
	Selected     bool
}

// fieldView is one settings field ready to render.
type fieldView struct {
	Name, Label, Hint, Value string
	InputType                string // text, url or number for an input
	Required                 bool
	Textarea, Select, Check  bool
	Checked                  bool
	Options                  []option
}

// Display returns the value as a person reads it in the install summary.
func (f fieldView) Display() string {
	switch {
	case f.Check && f.Checked:
		return "On"
	case f.Check:
		return "Off"
	case f.Select:
		for _, o := range f.Options {
			if o.Selected && o.Value != "" {
				return o.Label
			}
		}
		return ""
	}
	return f.Value
}

type grantView struct {
	SpaceID, Name, Role string
}

type spaceGrant struct {
	ID, Name string
	Roles    []roleOption
}

type pluginRow struct {
	PluginView
	StatusLabel, StatusClass string
	Usage                    string
	GrantList                []grantView
	Fields                   []fieldView
	SpaceGrants              []spaceGrant
	PendingPerms             []permView
	CanPause, CanResume      bool
	Uninstalled              bool
	// OpenPanel is settings, secrets or uninstall when that Dialog opens on
	// load, and PanelProblem is the failure it shows.
	OpenPanel    string
	PanelProblem string
	JobsURL      string
	// PageLinks open the plugin's page panels.
	PageLinks []pageLink
}

// pageLink is a row menu link to one page panel of a plugin.
type pageLink struct{ Title, URL string }

type summaryView struct {
	ManifestSummary
	Perms     []permView
	Fields    []fieldView
	HasFields bool
}

// hiddenField is a form value the last add step carries forward.
type hiddenField struct{ Name, Value string }

// secretInput is one secret field of the add form with its entered value.
type secretInput struct {
	Name, Label, Value string
}

type addView struct {
	Open    bool
	Step    int
	BaseURL string
	SHA256  string
	Summary *summaryView
	Exists  bool
	Problem string
	Spaces  []spaceGrant
	// Grants, Settings and SecretValues are the choices of step 3, kept
	// while the person moves between steps 3 and 4.
	Grants       []Grant
	Settings     map[string]any
	SecretValues map[string]string
	Secrets      []secretInput
	Carry        []hiddenField
	ChosenGrants []grantView
}

// StepTitle names the Dialog of the current step.
func (a addView) StepTitle() string {
	switch a.Step {
	case 2:
		return "Review permissions"
	case 3:
		return "Grant access"
	case 4:
		return "Approve and install"
	}
	return "Add plugin"
}

type resultView struct {
	Name, InstallError, MarkspaceURL, Token, SigningSecret string
}

type pageData struct {
	// Notice is a result the page does not show, as a success Banner.
	// Announce is a visible result, only announced. Problem is a failure
	// that belongs to no Dialog.
	Notice, Problem, Announce string
	Plugins                   []pluginRow
	Add                       addView
	Result                    *resultView
}

// statusText gives the Label text and tone of each plugin status.
var statusText = map[string][2]string{
	core.PluginStatusActive:        {"Up", "ui-label ui-label-success"},
	core.PluginStatusDown:          {"Down", "ui-label ui-label-danger"},
	core.PluginStatusPaused:        {"Paused", "ui-label ui-label-secondary"},
	core.PluginStatusNeedsApproval: {"Needs approval", "ui-label ui-label-attention"},
	core.PluginStatusUninstalled:   {"Uninstalled", "ui-label ui-label-secondary"},
}

func perms(list []string) []permView {
	out := make([]permView, 0, len(list))
	for _, p := range list {
		out = append(out, permView{ID: p, Description: permissionText[p]})
	}
	return out
}

// usageText formats tokens and a cost in millionths of a dollar.
func usageText(tokens, micros int64) string {
	if tokens == 0 && micros == 0 {
		return "None"
	}
	cost := "$0.00"
	switch {
	case micros > 0 && micros < 10_000:
		cost = "under $0.01"
	case micros > 0:
		cost = fmt.Sprintf("$%.2f", float64(micros)/1e6)
	}
	return groupDigits(tokens) + " tokens, " + cost
}

func groupDigits(n int64) string {
	s := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 && s[i-1] != '-' {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// rawField is a declarative UI settings field.
type rawField struct {
	Type     string   `json:"type"`
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Hint     string   `json:"hint"`
	Value    any      `json:"value"`
	Required bool     `json:"required"`
	Options  []option `json:"options"`
}

// parseFields reads settings fields from either a list or an object with a
// fields list.
func parseFields(raw json.RawMessage) []rawField {
	if len(raw) == 0 {
		return nil
	}
	var list []rawField
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var obj struct {
		Fields []rawField `json:"fields"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return obj.Fields
	}
	return nil
}

func valueText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// fieldViews builds the settings form from the manifest fields and the
// stored values. File fields are skipped. Note and folder fields take an id.
func fieldViews(raw json.RawMessage, current json.RawMessage, spaces []*core.Space) []fieldView {
	values := map[string]any{}
	_ = json.Unmarshal(current, &values)
	var out []fieldView
	for _, f := range parseFields(raw) {
		if f.Name == "" || f.Type == "file" {
			continue
		}
		v := f.Value
		if cur, ok := values[f.Name]; ok {
			v = cur
		}
		label := f.Label
		if label == "" {
			label = f.Name
		}
		fv := fieldView{Name: f.Name, Label: label, Hint: f.Hint, Required: f.Required, Value: valueText(v)}
		switch f.Type {
		case "textarea":
			fv.Textarea = true
		case "checkbox":
			fv.Check = true
			fv.Checked = v == true || valueText(v) == "true"
		case "select":
			fv.Select = true
			for _, o := range f.Options {
				fv.Options = append(fv.Options, option{Value: o.Value, Label: o.Label, Selected: o.Value == fv.Value})
			}
		case "space":
			fv.Select = true
			fv.Options = append(fv.Options, option{Value: "", Label: "Choose a space"})
			for _, s := range spaces {
				fv.Options = append(fv.Options, option{Value: s.ID, Label: s.Name, Selected: s.ID == fv.Value})
			}
		case "number":
			fv.InputType = "number"
		case "url":
			fv.InputType = "url"
		default:
			fv.InputType = "text"
		}
		out = append(out, fv)
	}
	return out
}

// settingsFrom reads the settings form fields of a schema.
func settingsFrom(r *http.Request, raw json.RawMessage) map[string]any {
	out := map[string]any{}
	for _, f := range parseFields(raw) {
		if f.Name == "" || f.Type == "file" {
			continue
		}
		key := "setting." + f.Name
		switch f.Type {
		case "checkbox":
			if r.PostForm.Has("present." + f.Name) {
				out[f.Name] = r.PostFormValue(key) != ""
			}
		case "number":
			if s := strings.TrimSpace(r.PostFormValue(key)); s != "" {
				if n, err := strconv.ParseFloat(s, 64); err == nil {
					out[f.Name] = n
				} else {
					out[f.Name] = s
				}
			} else if r.PostForm.Has(key) {
				out[f.Name] = nil
			}
		default:
			if r.PostForm.Has(key) {
				out[f.Name] = r.PostFormValue(key)
			}
		}
	}
	return out
}

// grantsFrom reads the grant.<spaceId> select of every space.
func grantsFrom(r *http.Request, spaces []*core.Space) []Grant {
	out := []Grant{}
	for _, s := range spaces {
		role := core.Role(r.PostFormValue("grant." + s.ID))
		if core.ValidPluginRole(role) {
			out = append(out, Grant{SpaceID: s.ID, Role: role})
		}
	}
	return out
}

// secretsFrom reads the secret.<name> fields a manifest declares. Empty
// values are left out.
func secretsFrom(r *http.Request, declared []SecretView) map[string]string {
	out := map[string]string{}
	for _, s := range declared {
		if v := r.PostFormValue("secret." + s.Name); v != "" {
			out[s.Name] = v
		}
	}
	return out
}

func spaceGrants(spaces []*core.Space, grants []Grant) []spaceGrant {
	have := map[string]string{}
	for _, g := range grants {
		have[g.SpaceID] = string(g.Role)
	}
	out := make([]spaceGrant, 0, len(spaces))
	for _, s := range spaces {
		sg := spaceGrant{ID: s.ID, Name: s.Name}
		for _, ro := range grantRoles {
			ro.Selected = ro.Value == have[s.ID]
			sg.Roles = append(sg.Roles, ro)
		}
		out = append(out, sg)
	}
	return out
}

func summaryOf(s ManifestSummary, spaces []*core.Space) *summaryView {
	fields := fieldViews(s.Settings, nil, spaces)
	return &summaryView{ManifestSummary: s, Perms: perms(s.Permissions), Fields: fields, HasFields: len(fields) > 0}
}

// roleLabel names a grant role for the page.
func roleLabel(role core.Role) string {
	for _, ro := range grantRoles {
		if ro.Value == string(role) && ro.Value != "" {
			return ro.Label
		}
	}
	return string(role)
}

// errText is the message a failed action shows.
func errText(err error) string {
	var ve *core.ValidationError
	if errors.As(err, &ve) && ve.Message != "" {
		return ve.Message
	}
	if api.StatusFor(err) == 0 {
		return "The plugin host could not complete the action."
	}
	return err.Error()
}

// sentence starts a message with a capital letter and ends it with a full
// stop, since the host returns lowercase validation messages.
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	s = strings.ToUpper(s[:1]) + s[1:]
	if !strings.HasSuffix(s, ".") && !strings.HasSuffix(s, "?") && !strings.HasSuffix(s, "!") {
		s += "."
	}
	return s
}

// render builds and writes the page.
func (m *Module) render(w http.ResponseWriter, r *http.Request, actor *core.Actor, d pageData, status int) {
	ctx := r.Context()
	spaces, err := m.opts.Backend.ListSpaces(ctx, actor)
	if err != nil {
		m.opts.Pages.RenderError(w, r, err)
		return
	}
	sort.SliceStable(spaces, func(i, j int) bool { return strings.ToLower(spaces[i].Name) < strings.ToLower(spaces[j].Name) })
	names := map[string]string{}
	for _, s := range spaces {
		names[s.ID] = s.Name
	}
	list, err := m.opts.Host.List(ctx)
	if err != nil {
		m.opts.Pages.RenderError(w, r, err)
		return
	}
	q := r.URL.Query()
	openID, panel := q.Get("open"), q.Get("panel")
	if id := q.Get("confirm"); id != "" {
		openID, panel = id, "uninstall"
	}
	for _, p := range list {
		st := statusText[p.Status]
		if st[0] == "" {
			st = [2]string{p.Status, "ui-label ui-label-secondary"}
		}
		row := pluginRow{
			PluginView: p, StatusLabel: st[0], StatusClass: st[1],
			Usage:       usageText(p.AITokensMonth, p.AICostMicrosMonth),
			Fields:      fieldViews(p.Approved.Settings, p.Settings, spaces),
			SpaceGrants: spaceGrants(spaces, p.Grants),
			CanPause:    core.PluginStatusRuns(p.Status),
			CanResume:   p.Status == core.PluginStatusPaused,
			Uninstalled: p.Status == core.PluginStatusUninstalled,
			JobsURL:     "/jobs?" + url.Values{"view": {"all"}, "module": {"plugin." + p.ID}}.Encode(),
		}
		for _, panel := range p.Approved.Panels {
			if panel.Extra == "page" {
				row.PageLinks = append(row.PageLinks, pageLink{Title: orName(panel.Title, panel.ID),
					URL: "/plugins/page?" + url.Values{"panel": {p.ID + "." + panel.ID}}.Encode()})
			}
		}
		if openID == p.ID && !row.Uninstalled {
			switch panel {
			case "settings", "secrets", "uninstall":
				row.OpenPanel = panel
				// The failure shows inside the Dialog, not above the page.
				if d.Problem != "" {
					row.PanelProblem, d.Problem = d.Problem, ""
				}
			}
		}
		for _, g := range p.Grants {
			name := g.SpaceName
			if name == "" {
				name = names[g.SpaceID]
			}
			if name == "" {
				name = g.SpaceID
			}
			row.GrantList = append(row.GrantList, grantView{SpaceID: g.SpaceID, Name: name, Role: roleLabel(g.Role)})
		}
		if p.Pending != nil {
			for _, perm := range p.Pending.Permissions {
				if !slices.Contains(p.Approved.Permissions, perm) {
					row.PendingPerms = append(row.PendingPerms, permView{ID: perm, Description: permissionText[perm]})
				}
			}
		}
		d.Plugins = append(d.Plugins, row)
	}
	if d.Add.Step >= 3 && d.Add.Summary != nil {
		a := &d.Add
		a.Spaces = spaceGrants(spaces, a.Grants)
		current, _ := json.Marshal(a.Settings)
		a.Summary.Fields = fieldViews(a.Summary.Settings, current, spaces)
		a.Summary.HasFields = len(a.Summary.Fields) > 0
		a.Secrets = nil
		for _, s := range a.Summary.Secrets {
			a.Secrets = append(a.Secrets, secretInput{Name: s.Name, Label: orName(s.Label, s.Name), Value: a.SecretValues[s.Name]})
		}
		a.ChosenGrants = nil
		for _, g := range a.Grants {
			a.ChosenGrants = append(a.ChosenGrants, grantView{SpaceID: g.SpaceID, Name: orName(names[g.SpaceID], g.SpaceID), Role: roleLabel(g.Role)})
		}
	}
	if d.Add.Step == 0 {
		d.Add.Step = 1
		d.Add.Open = d.Add.Open || q.Get("add") == "1"
	}
	var buf bytes.Buffer
	if err := api.ExecutePage(&buf, pageTemplate, m.opts.Pages, d); err != nil {
		m.opts.Pages.RenderError(w, r, err)
		return
	}
	m.opts.Pages.RenderModulePage(w, r, api.PageSpec{
		Title: "Plugins", Nav: "admin", AdminNav: true,
		Body: template.HTML(buf.String()), Scripts: []string{"/modules/plugins/admin.js"}, Status: status,
	})
}

func orName(label, name string) string {
	if label != "" {
		return label
	}
	return name
}

// pageActor returns the admin person of a page request or renders the
// refusal: the login page for an anonymous visitor, the error page for
// anyone else.
func (m *Module) pageActor(w http.ResponseWriter, r *http.Request) (*core.Actor, bool) {
	actor, err := adminActor(r)
	if err != nil {
		m.opts.Pages.RenderError(w, r, err)
		return nil, false
	}
	if m.opts.Host == nil {
		m.opts.Pages.RenderError(w, r, fmt.Errorf("the plugin host is not running: %w", core.ErrUnavailable))
		return nil, false
	}
	return actor, true
}

// page serves GET /admin/plugins?notice=&announce=&problem=&open=&panel=
// &confirm=&add=.
func (m *Module) page(w http.ResponseWriter, r *http.Request) {
	actor, ok := m.pageActor(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	m.render(w, r, actor, pageData{Notice: q.Get("notice"), Announce: q.Get("announce"), Problem: q.Get("problem")}, http.StatusOK)
}

func redirect(w http.ResponseWriter, r *http.Request, v url.Values, fragment string) {
	target := "/admin/plugins"
	if len(v) > 0 {
		target += "?" + v.Encode()
	}
	if fragment != "" {
		target += "#" + fragment
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// stateNotices are the results of the row actions. Pause, resume and approve
// change the status Label, so they are only announced.
var stateNotices = map[string]string{
	"pause":   "Plugin paused.",
	"resume":  "Plugin resumed.",
	"check":   "The manifest has no changes that need approval.",
	"approve": "The new manifest is approved.",
}

// pagePost serves POST /admin/plugins, the form fallback of every action.
func (m *Module) pagePost(w http.ResponseWriter, r *http.Request) {
	actor, ok := m.pageActor(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		m.opts.Pages.RenderError(w, r, core.Invalid("body", "the form could not be read"))
		return
	}
	ctx := r.Context()
	id := r.PostFormValue("id")
	action := r.PostFormValue("action")
	switch action {
	case "back":
		m.render(w, r, actor, pageData{Add: addView{Open: true, Step: 1, BaseURL: strings.TrimSpace(r.PostFormValue("baseUrl"))}}, http.StatusOK)
	case "preview", "review":
		m.addStep(w, r, actor, 2)
	case "continue", "choices":
		m.addStep(w, r, actor, 3)
	case "confirm":
		m.addStep(w, r, actor, 4)
	case "install":
		m.install(w, r, actor)
	case "pause", "resume", "check", "approve":
		p, err := m.runState(r, id, action)
		v := url.Values{}
		switch {
		case err != nil:
			v.Set("problem", sentence(errText(err)))
		case action == "check" && p != nil && p.Status == core.PluginStatusNeedsApproval:
			v.Set("notice", "The manifest asks for more access and waits for your approval.")
		case action == "check":
			v.Set("notice", stateNotices[action])
		default:
			v.Set("announce", stateNotices[action])
		}
		redirect(w, r, v, "plugin-"+id)
	case "settings", "secrets":
		p, err := m.opts.Host.Get(ctx, id)
		if err != nil {
			redirect(w, r, url.Values{"problem": {sentence(errText(err))}}, "")
			return
		}
		var in ConfigInput
		if action == "settings" {
			spaces, err := m.opts.Backend.ListSpaces(ctx, actor)
			if err != nil {
				m.opts.Pages.RenderError(w, r, err)
				return
			}
			grants := grantsFrom(r, spaces)
			in.Grants = &grants
			in.Settings = settingsFrom(r, p.Approved.Settings)
		} else {
			in.Secrets = secretsFrom(r, p.Approved.Secrets)
		}
		v := url.Values{}
		if _, err := m.opts.Host.Configure(ctx, id, in); err != nil {
			v.Set("problem", sentence(errText(err)))
			v.Set("open", id)
			v.Set("panel", action)
		} else if action == "settings" {
			v.Set("notice", "Settings of "+p.Name+" saved.")
		} else {
			v.Set("notice", "Secrets of "+p.Name+" saved.")
		}
		redirect(w, r, v, "plugin-"+id)
	case "uninstall-ask":
		redirect(w, r, url.Values{"confirm": {id}}, "plugin-uninstall-"+id)
	case "uninstall":
		deleteData := r.PostFormValue("data") == "delete"
		v := url.Values{}
		if err := m.opts.Host.Uninstall(ctx, id, deleteData); err != nil {
			v.Set("problem", sentence(errText(err)))
			v.Set("open", id)
			v.Set("panel", "uninstall")
		} else if deleteData {
			v.Set("notice", "Plugin uninstalled and its data deleted.")
		} else {
			v.Set("notice", "Plugin uninstalled. Its data is kept.")
		}
		redirect(w, r, v, "")
	default:
		redirect(w, r, url.Values{"problem": {"Unknown action."}}, "")
	}
}

// addStep renders step 2, the manifest review, step 3, spaces, secrets and
// settings, or step 4, the summary to approve. Steps 3 and 4 fetch the
// manifest again and return to the review when it changed.
func (m *Module) addStep(w http.ResponseWriter, r *http.Request, actor *core.Actor, step int) {
	base := strings.TrimSpace(r.PostFormValue("baseUrl"))
	d := pageData{Add: addView{Open: true, Step: 1, BaseURL: base}}
	if base == "" {
		d.Add.Problem = "Enter the address of the plugin."
		m.render(w, r, actor, d, http.StatusBadRequest)
		return
	}
	pv, err := m.opts.Host.Preview(r.Context(), base)
	if err != nil {
		d.Add.Problem = sentence(errText(err))
		m.render(w, r, actor, d, http.StatusOK)
		return
	}
	d.Add.Step, d.Add.SHA256, d.Add.BaseURL = step, pv.SHA256, pv.BaseURL
	d.Add.Summary = summaryOf(pv.Manifest, nil)
	d.Add.Exists = pv.Installed
	if step >= 3 && r.PostFormValue("sha256") != "" && r.PostFormValue("sha256") != pv.SHA256 {
		d.Add.Step = 2
		d.Add.Problem = "The manifest changed since you reviewed it. Review it again."
		m.render(w, r, actor, d, http.StatusOK)
		return
	}
	if d.Add.Step >= 3 {
		if err := m.readChoices(r, actor, &d.Add, pv.Manifest); err != nil {
			m.opts.Pages.RenderError(w, r, err)
			return
		}
	}
	m.render(w, r, actor, d, http.StatusOK)
}

// readChoices reads the spaces, secrets and settings of step 3 from the
// form, and the fields step 4 carries to the install.
func (m *Module) readChoices(r *http.Request, actor *core.Actor, a *addView, manifest ManifestSummary) error {
	spaces, err := m.opts.Backend.ListSpaces(r.Context(), actor)
	if err != nil {
		return err
	}
	a.Grants = grantsFrom(r, spaces)
	a.Settings = settingsFrom(r, manifest.Settings)
	a.SecretValues = secretsFrom(r, manifest.Secrets)
	a.Carry = nil
	keys := make([]string, 0, len(r.PostForm))
	for k := range r.PostForm {
		for _, prefix := range []string{"grant.", "secret.", "setting.", "present."} {
			if strings.HasPrefix(k, prefix) {
				keys = append(keys, k)
				break
			}
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range r.PostForm[k] {
			a.Carry = append(a.Carry, hiddenField{Name: k, Value: v})
		}
	}
	return nil
}

// install runs the final step of the add form.
func (m *Module) install(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	ctx := r.Context()
	base := strings.TrimSpace(r.PostFormValue("baseUrl"))
	sha := r.PostFormValue("sha256")
	spaces, err := m.opts.Backend.ListSpaces(ctx, actor)
	if err != nil {
		m.opts.Pages.RenderError(w, r, err)
		return
	}
	pv, err := m.opts.Host.Preview(ctx, base)
	if err != nil {
		m.render(w, r, actor, pageData{Add: addView{Open: true, Step: 1, BaseURL: base, Problem: sentence(errText(err))}}, http.StatusOK)
		return
	}
	in := InstallInput{BaseURL: base, SHA256: sha, Grants: grantsFrom(r, spaces)}
	in.Secrets = secretsFrom(r, pv.Manifest.Secrets)
	in.Settings = settingsFrom(r, pv.Manifest.Settings)
	res, err := m.opts.Host.Install(ctx, in)
	if err != nil {
		d := pageData{Add: addView{Open: true, Step: 3, BaseURL: pv.BaseURL, SHA256: pv.SHA256, Summary: summaryOf(pv.Manifest, nil), Exists: pv.Installed, Problem: sentence(errText(err))}}
		if sha != "" && sha != pv.SHA256 {
			d.Add.Step = 2
			d.Add.SHA256 = pv.SHA256
		} else if err := m.readChoices(r, actor, &d.Add, pv.Manifest); err != nil {
			m.opts.Pages.RenderError(w, r, err)
			return
		}
		m.render(w, r, actor, d, http.StatusOK)
		return
	}
	if res.Delivered {
		redirect(w, r, url.Values{"announce": {res.Plugin.Name + " is installed."}}, "plugin-"+res.Plugin.ID)
		return
	}
	d := pageData{
		Announce: res.Plugin.Name + " is installed.",
		Result: &resultView{Name: res.Plugin.Name, InstallError: res.InstallError, MarkspaceURL: res.MarkspaceURL,
			Token: res.Token, SigningSecret: res.SigningSecret},
	}
	m.render(w, r, actor, d, http.StatusCreated)
}

// adminScript serves the page script.
func (m *Module) adminScript(w http.ResponseWriter, r *http.Request) {
	b, err := assets.ReadFile("assets/admin.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}
