package web

import (
	"context"
	"html/template"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// Limits and sizes of the usage pages.
const (
	usageRecentLimit  = 20
	usageShortLimit   = 8
	usageUserLimit    = 500
	usageDefaultDays  = 30
	usageMaxRangeDays = 400
	usageWarnPct      = 80
	usagePlotWidth    = 880
)

// usageFeatureSlots gives the features the server and the editor send a
// fixed chart colour, so a feature keeps its colour in every range.
var usageFeatureSlots = map[string]int{
	core.FeatureAsk:        1,
	core.FeatureChat:       2,
	core.FeatureRewrite:    3,
	core.FeatureImage:      4,
	core.FeatureSearch:     5,
	core.FeatureEmbeddings: 6,
	core.FeatureTest:       7,
}

// usageOption is one choice of a select.
type usageOption struct{ Value, Label string }

// usageRanges are the date range presets of the admin page.
var usageRanges = []usageOption{
	{"7d", "Last 7 days"},
	{"30d", "Last 30 days"},
	{"month", "This month"},
	{"lastmonth", "Last month"},
	{"custom", "Custom dates"},
}

// usageInlineFields are the admin page fields that show a refused form's
// message under the field. The banner stays for other errors.
var usageInlineFields = map[string]bool{
	"from": true, "to": true, "defaultTokens": true, "defaultCost": true,
	"monthlyTokens": true, "monthlyCost": true, "person": true, "pluginId": true, "subjectKind": true,
}

// usageRowResults are the messages a budget row shows after an action.
var usageRowResults = map[string]string{
	"added":  "Override added.",
	"saved":  "Budget saved.",
	"lifted": "Lifted until the month ends.",
}

// anchorUnsafe matches the characters a budget row anchor replaces.
var anchorUnsafe = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

// The usage templates format numbers with these functions. They join the
// shared function map before NewServer parses the templates.
func init() {
	funcMap["usageTokens"] = usageTokens
	funcMap["usageNum"] = usageNum
	funcMap["usageMoney"] = usageMoney
	funcMap["usageDuration"] = usageDuration
	funcMap["usagePct"] = usagePct
	funcMap["usageProgress"] = usageProgress
	funcMap["usageCount"] = usageCount
	funcMap["usageSum"] = func(a, b int64) int64 { return a + b }
	funcMap["usageDate"] = func(t time.Time) string { return t.UTC().Format("2 Jan") }
}

// usageCall is one call record with the names a table shows.
type usageCall struct {
	*core.AIUsageRecord
	// Person names the caller on the admin page.
	Person string
	// NoteTitle is empty when the call has no note or the note is not
	// readable.
	NoteTitle string
}

// usageRow is one report row with its display name.
type usageRow struct {
	core.AIUsageRow
	Label string
	Sub   string
}

// usageView is the data of /settings/usage.
type usageView struct {
	From, To time.Time
	Currency string
	Summary  *core.AIUsageSummary
	Chart    *usageChart
	Models   []usageRow
	Recent   []usageCall
	Empty    bool
}

// usageFilter is the parsed filter form of /admin/usage. Start is inclusive
// and End exclusive. From and To are the dates the form shows, To inclusive.
type usageFilter struct {
	Range, From, To               string
	User, Provider, Model, Plugin string
	Stack                         string
	Start, End                    time.Time
}

// usageBudget is one budget override row.
type usageBudget struct {
	*core.AIBudget
	Index  int
	Anchor string
	Name   string
	Sub    string
	// TokenLimit and CostLimit describe the limits for the row summary.
	TokenLimit string
	CostLimit  string
	// TokensValue and CostValue fill the edit form. Empty means no override.
	TokensValue string
	CostValue   string
	State       core.AIBudgetStatus
	Limited     bool
	LiftActive  bool
	LiftEnds    time.Time
	// EditURL opens the row's Set budget form without JavaScript.
	EditURL string
	// Editing opens the Set budget Dialog on load, after a refused save or
	// through EditURL. Result is the message of the last action on the row,
	// which the page only announces because the row shows the change.
	Editing bool
	Result  string
}

// adminUsageView is the data of /admin/usage.
type adminUsageView struct {
	Filter          usageFilter
	Ranges          []usageOption
	Currency        string
	Totals          core.AIUsageRow
	Chart           *usageChart
	People          []usageRow
	Providers       []usageRow
	Models          []usageRow
	Plugins         []usageRow
	Slowest         []usageCall
	Failed          []usageCall
	Users           []*core.User
	ProviderOptions []usageOption
	ModelOptions    []string
	PluginOptions   []string
	DefaultTokens   string
	DefaultCost     string
	DefaultsSaved   bool
	Budgets         []*usageBudget
	Removed         bool
	AddOpen         bool
	ErrorField      string
	ErrorMsg        string
	// kept holds the values of a refused form, so its Dialog comes back with
	// what the person entered.
	kept url.Values
}

// FieldError returns the message of a refused form when it belongs to the
// named field.
func (v *adminUsageView) FieldError(name string) string {
	if v == nil || v.ErrorField != name {
		return ""
	}
	return v.ErrorMsg
}

// Value returns a value of the refused form, or the empty string.
func (v *adminUsageView) Value(name string) string {
	if v == nil || v.kept == nil {
		return ""
	}
	return v.kept.Get(name)
}

// usageKeptFields are the fields a refused budget form sends back with its
// error.
var usageKeptFields = []string{"subjectKind", "person", "pluginId", "monthlyTokens", "monthlyCost", "defaultTokens", "defaultCost"}

// handleUsage renders the signed-in user's AI usage for the last 30 days.
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	to := usageDay(time.Now()).AddDate(0, 0, 1)
	from := to.AddDate(0, 0, -usageDefaultDays)
	daily, err := s.backend.MyAIUsage(ctx, actor, core.AIUsageQuery{From: from, To: to, GroupBy: []string{core.GroupDay, core.GroupFeature}})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	models, err := s.backend.MyAIUsage(ctx, actor, core.AIUsageQuery{From: from, To: to, GroupBy: []string{core.GroupModel}})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v := &usageView{From: from, To: to, Currency: daily.Currency}
	for _, row := range models.Rows {
		v.Models = append(v.Models, usageRow{AIUsageRow: row, Label: orDefault(row.Model, "Unknown model")})
	}
	if summary, err := s.backend.AIUsageSummary(ctx, actor); err == nil {
		v.Summary = summary
	} else {
		s.log.Debug("web: usage summary failed", "err", err)
	}
	calls, err := s.backend.AIUsageCalls(ctx, actor, core.AIUsageQuery{UserID: actor.UserID(), Limit: usageRecentLimit}, core.UsageSortRecent)
	if err != nil {
		s.log.Debug("web: recent usage calls failed", "err", err)
	}
	v.Recent = s.describeCalls(ctx, actor, calls, nil)
	v.Chart = buildUsageChart(daily.Rows, from, to, featureSeries, chartOptions{
		PlotWidth: usagePlotWidth,
		Fixed:     usageFeatureSlots,
		Label:     "Tokens per day for the last 30 days, stacked by feature",
	})
	v.Empty = daily.Totals.Calls == 0 && models.Totals.Calls == 0 && len(v.Recent) == 0 &&
		(v.Summary == nil || v.Summary.Month.Calls == 0)
	p := s.newPage(r, "AI usage", "usage")
	p.Set("Usage", v)
	s.Render(w, r, http.StatusOK, "usage", p)
}

// handleAdminUsage renders usage across the instance with filters, and the
// budget defaults and overrides.
func (s *Server) handleAdminUsage(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	query := r.URL.Query()
	now := time.Now().UTC()
	p := s.newPage(r, "AI usage", "admin")
	v := &adminUsageView{Ranges: usageRanges}
	if usageInlineFields[p.ErrorField] {
		// The message shows under its field, so no page Banner repeats it.
		v.ErrorField, v.ErrorMsg = p.ErrorField, p.Error
		p.Error, p.ErrorField = "", ""
		v.kept = query
	}
	f, err := parseUsageFilter(query, now)
	if err != nil {
		v.ErrorField, v.ErrorMsg = fieldOf(err), detailOf(err)
	}
	v.Filter = f

	base := core.AIUsageQuery{From: f.Start, To: f.End, UserID: f.User, ProviderID: f.Provider, Model: f.Model, PluginID: f.Plugin}
	report := func(keys ...string) (*core.AIUsageReport, error) {
		q := base
		q.GroupBy = keys
		return s.backend.AIUsage(ctx, actor, q)
	}
	daily, err := report(core.GroupDay, f.Stack)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v.Currency, v.Totals = daily.Currency, daily.Totals

	people := map[string]*core.User{}
	if users, _, err := s.backend.ListUsers(ctx, actor, "", usageUserLimit, 0); err == nil {
		v.Users = users
		for _, u := range users {
			people[u.ID] = u
		}
	}
	providerNames := map[string]string{}
	if providers, err := s.backend.ListAIProviders(ctx, actor); err == nil {
		for _, pr := range providers {
			providerNames[pr.ID] = pr.Name
			v.ProviderOptions = append(v.ProviderOptions, usageOption{pr.ID, orDefault(pr.Name, pr.ID)})
		}
	}

	opts := chartOptions{PlotWidth: usagePlotWidth, Label: "Tokens per day, stacked by person"}
	key := func(row core.AIUsageRow) (string, string) {
		return row.UserID, personName(people, row.UserID, row.UserName)
	}
	if f.Stack == core.GroupPlugin {
		opts.Label = "Tokens per day, stacked by plugin"
		key = func(row core.AIUsageRow) (string, string) { return row.PluginID, orDefault(row.PluginID, "No plugin") }
	}
	v.Chart = buildUsageChart(daily.Rows, f.Start, f.End, key, opts)

	breakdowns := []struct {
		group string
		dst   *[]usageRow
		label func(core.AIUsageRow) (string, string)
	}{
		{core.GroupUser, &v.People, func(row core.AIUsageRow) (string, string) {
			sub := ""
			if u := people[row.UserID]; u != nil && u.Name != "" {
				sub = u.Email
			}
			return personName(people, row.UserID, row.UserName), sub
		}},
		{core.GroupProvider, &v.Providers, func(row core.AIUsageRow) (string, string) {
			name := providerNames[row.ProviderID]
			switch {
			case row.ProviderID == "":
				return "No provider", ""
			case name == "" || name == row.ProviderID:
				return row.ProviderID, ""
			}
			return name, row.ProviderID
		}},
		{core.GroupModel, &v.Models, func(row core.AIUsageRow) (string, string) {
			return orDefault(row.Model, "Unknown model"), ""
		}},
		{core.GroupPlugin, &v.Plugins, func(row core.AIUsageRow) (string, string) {
			if row.PluginID == "" {
				return "No plugin", "Calls from Markspace itself"
			}
			return row.PluginID, ""
		}},
	}
	for _, b := range breakdowns {
		rep, err := report(b.group)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		for _, row := range rep.Rows {
			label, sub := b.label(row)
			*b.dst = append(*b.dst, usageRow{AIUsageRow: row, Label: label, Sub: sub})
		}
	}
	v.ModelOptions = usageChoices(v.Models, f.Model, func(row usageRow) string { return row.Model })
	v.PluginOptions = usageChoices(v.Plugins, f.Plugin, func(row usageRow) string { return row.PluginID })

	callQuery := base
	callQuery.Limit = usageShortLimit
	if list, err := s.backend.AIUsageCalls(ctx, actor, callQuery, core.UsageSortSlowest); err == nil {
		v.Slowest = s.describeCalls(ctx, actor, list, people)
	} else {
		s.log.Debug("web: slowest usage calls failed", "err", err)
	}
	if list, err := s.backend.AIUsageCalls(ctx, actor, callQuery, core.UsageSortFailed); err == nil {
		v.Failed = s.describeCalls(ctx, actor, list, people)
	} else {
		s.log.Debug("web: failed usage calls failed", "err", err)
	}

	if st := p.Settings; st != nil {
		v.DefaultTokens = strconv.FormatInt(st.AIMonthlyTokenBudget, 10)
		v.DefaultCost = strconv.FormatFloat(st.AIMonthlyCostBudget, 'f', -1, 64)
	}
	if v.ErrorField == "defaultTokens" || v.ErrorField == "defaultCost" {
		v.DefaultTokens, v.DefaultCost = v.Value("defaultTokens"), v.Value("defaultCost")
	}
	budgets, err := s.backend.ListAIBudgets(ctx, actor)
	if err != nil {
		s.log.Debug("web: budget listing failed", "err", err)
	}
	openRow, done := query.Get("row"), query.Get("done")
	rowError := openRow != "" && (v.ErrorField == "monthlyTokens" || v.ErrorField == "monthlyCost")
	for i, b := range budgets {
		vb := budgetView(i, b, people, p.Settings, v.Currency, now)
		if b.SubjectKind+":"+b.SubjectID == openRow {
			vb.Result = usageRowResults[done]
			vb.Editing = rowError || query.Get("edit") == "1"
			if rowError {
				vb.TokensValue, vb.CostValue = v.Value("monthlyTokens"), v.Value("monthlyCost")
			}
		}
		v.Budgets = append(v.Budgets, vb)
	}
	v.DefaultsSaved = done == "defaults"
	v.Removed = done == "removed"
	switch v.ErrorField {
	case "person", "pluginId", "subjectKind":
		v.AddOpen = true
	case "monthlyTokens", "monthlyCost":
		v.AddOpen = openRow == ""
	}
	if query.Get("add") == "1" {
		v.AddOpen = true
	}
	p.Set("AdminUsage", v)
	s.Render(w, r, http.StatusOK, "admin/usage", p)
}

// handleAdminUsagePost saves the budget defaults, and saves, lifts or
// removes a budget override. Every form has an explicit Save button, and
// Remove asks first in a ConfirmationDialog in the page. A refused form
// redirects back with its message, its field and the entered values.
func (s *Server) handleAdminUsagePost(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := s.formValues(r); err != nil {
		s.fail(w, r, core.Invalid("form", "the form could not be read"))
		return
	}
	ctx := r.Context()
	kind := strings.TrimSpace(r.PostFormValue("subjectKind"))
	id := strings.TrimSpace(r.PostFormValue("subjectId"))
	action := r.PostFormValue("action")

	if action == "defaults-save" {
		if err := s.saveBudgetDefaults(ctx, actor, r); err != nil {
			s.formFail(w, r, keepUsageValues(r, "/admin/usage#budget-defaults"), err, "The default budget could not be saved.")
			return
		}
		http.Redirect(w, r, "/admin/usage?done=defaults#budget-defaults", http.StatusSeeOther)
		return
	}

	back := "/admin/usage#budgets"
	next := ""
	var err error
	switch action {
	case "budget-save":
		done := "added"
		if id != "" {
			done = "saved"
			back = budgetURL(kind, id, "")
		}
		var saved string
		if saved, err = s.saveBudget(ctx, actor, r, kind, id); err == nil {
			next = budgetURL(kind, saved, "done="+done)
		}
	case "budget-lift":
		back = budgetURL(kind, id, "")
		_, err = s.backend.LiftAIBudget(ctx, actor, kind, id)
		next = budgetURL(kind, id, "done=lifted")
	case "budget-delete":
		back = budgetURL(kind, id, "")
		err = s.backend.DeleteAIBudget(ctx, actor, kind, id)
		next = "/admin/usage?done=removed#budgets"
	default:
		err = core.Invalid("action", "unknown usage action")
	}
	if err != nil {
		if action == "budget-save" {
			back = keepUsageValues(r, back)
		}
		s.formFail(w, r, back, err, "The budget could not be saved.")
		return
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// keepUsageValues adds the non-empty kept fields of a refused form to back.
func keepUsageValues(r *http.Request, back string) string {
	kept := url.Values{}
	for _, name := range usageKeptFields {
		if v := strings.TrimSpace(r.PostFormValue(name)); v != "" {
			kept.Set(name, v)
		}
	}
	if len(kept) == 0 {
		return back
	}
	return withQuery(back, kept)
}

// saveBudgetDefaults stores the monthly limits every user gets. An empty
// field counts as 0, which means no limit.
func (s *Server) saveBudgetDefaults(ctx context.Context, actor *core.Actor, r *http.Request) error {
	tokens, err := formLimitTokens(r, "defaultTokens")
	if err != nil {
		return err
	}
	cost, err := formLimitCost(r, "defaultCost")
	if err != nil {
		return err
	}
	current, err := s.backend.GetSettings(ctx, actor)
	if err != nil {
		return err
	}
	next := *current
	next.AIMonthlyTokenBudget, next.AIMonthlyCostBudget = 0, 0
	if tokens != nil {
		next.AIMonthlyTokenBudget = *tokens
	}
	if cost != nil {
		next.AIMonthlyCostBudget = *cost
	}
	_, err = s.backend.UpdateSettings(ctx, actor, &next)
	return err
}

// saveBudget stores one override and returns the id of its subject. A new
// user override names the person by email address, name or id.
func (s *Server) saveBudget(ctx context.Context, actor *core.Actor, r *http.Request, kind, id string) (string, error) {
	tokens, err := formLimitTokens(r, "monthlyTokens")
	if err != nil {
		return "", err
	}
	cost, err := formLimitCost(r, "monthlyCost")
	if err != nil {
		return "", err
	}
	switch kind {
	case core.BudgetUser:
		if id == "" {
			if id, err = s.resolvePerson(ctx, actor, r.PostFormValue("person")); err != nil {
				return "", err
			}
		}
	case core.BudgetPlugin:
		if id == "" {
			id = strings.TrimSpace(r.PostFormValue("pluginId"))
		}
		if id == "" {
			return "", core.Invalid("pluginId", "enter the id of the plugin")
		}
	default:
		return "", core.Invalid("subjectKind", "choose a person or a plugin")
	}
	_, err = s.backend.SaveAIBudget(ctx, actor, core.AIBudget{SubjectKind: kind, SubjectID: id, MonthlyTokens: tokens, MonthlyCost: cost})
	return id, err
}

// resolvePerson finds the account a lookup field names. An id or email
// address must match exactly. A name must match exactly one account.
func (s *Server) resolvePerson(ctx context.Context, actor *core.Actor, raw string) (string, error) {
	q := strings.TrimSpace(raw)
	if q == "" {
		return "", core.Invalid("person", "enter the name or email address of a person")
	}
	found, err := s.backend.LookupUsers(ctx, actor, q, 10)
	if err != nil {
		return "", err
	}
	var named []*core.User
	for _, u := range found {
		if u.ID == q || strings.EqualFold(u.Email, q) {
			return u.ID, nil
		}
		if strings.EqualFold(u.Name, q) {
			named = append(named, u)
		}
	}
	switch len(named) {
	case 1:
		return named[0].ID, nil
	case 0:
		return "", core.Invalid("person", "no account matches "+q)
	}
	return "", core.Invalid("person", "more than one account is named "+q+", enter the email address instead")
}

// formLimitTokens reads an optional token limit. Empty means no value.
func formLimitTokens(r *http.Request, field string) (*int64, error) {
	raw := strings.TrimSpace(r.PostFormValue(field))
	if raw == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil, core.Invalid(field, "enter a whole number of tokens")
	}
	if n < 0 {
		return nil, core.Invalid(field, "a token limit must be 0 or more")
	}
	return &n, nil
}

// formLimitCost reads an optional cost limit. Empty means no value.
func formLimitCost(r *http.Request, field string) (*float64, error) {
	raw := strings.TrimSpace(r.PostFormValue(field))
	if raw == "" {
		return nil, nil
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, core.Invalid(field, "enter an amount such as 12.50")
	}
	if f < 0 {
		return nil, core.Invalid(field, "a cost limit must be 0 or more")
	}
	return &f, nil
}

// parseUsageFilter reads the admin filter form. A bad custom range returns
// an error together with the default 30-day range, and keeps the submitted
// dates so the form can show them.
func parseUsageFilter(q url.Values, now time.Time) (usageFilter, error) {
	f := usageFilter{
		Range:    strings.TrimSpace(q.Get("range")),
		User:     strings.TrimSpace(q.Get("user")),
		Provider: strings.TrimSpace(q.Get("provider")),
		Model:    strings.TrimSpace(q.Get("model")),
		Plugin:   strings.TrimSpace(q.Get("plugin")),
		Stack:    core.GroupUser,
	}
	if q.Get("stack") == core.GroupPlugin {
		f.Stack = core.GroupPlugin
	}
	today := usageDay(now)
	from, to := strings.TrimSpace(q.Get("from")), strings.TrimSpace(q.Get("to"))
	if f.Range == "" && (from != "" || to != "") {
		f.Range = "custom"
	}
	var err error
	switch f.Range {
	case "7d":
		f.Start, f.End = today.AddDate(0, 0, -6), today.AddDate(0, 0, 1)
	case "month":
		f.Start, f.End = usageMonth(today), today.AddDate(0, 0, 1)
	case "lastmonth":
		f.End = usageMonth(today)
		f.Start = f.End.AddDate(0, -1, 0)
	case "custom":
		f.Start, f.End, err = customUsageRange(from, to, today)
	default:
		f.Range = "30d"
		f.Start, f.End = today.AddDate(0, 0, -(usageDefaultDays-1)), today.AddDate(0, 0, 1)
	}
	if err != nil {
		f.Start, f.End = today.AddDate(0, 0, -(usageDefaultDays-1)), today.AddDate(0, 0, 1)
		f.From, f.To = from, to
		return f, err
	}
	f.From, f.To = f.Start.Format(time.DateOnly), f.End.AddDate(0, 0, -1).Format(time.DateOnly)
	return f, nil
}

// customUsageRange parses inclusive YYYY-MM-DD dates into a half-open range.
// A missing end is today and a missing start is 30 days before the end.
func customUsageRange(from, to string, today time.Time) (time.Time, time.Time, error) {
	end := today.AddDate(0, 0, 1)
	if to != "" {
		t, err := time.Parse(time.DateOnly, to)
		if err != nil {
			return time.Time{}, time.Time{}, core.Invalid("to", "enter the end date as YYYY-MM-DD")
		}
		end = t.AddDate(0, 0, 1)
	}
	start := end.AddDate(0, 0, -usageDefaultDays)
	if from != "" {
		t, err := time.Parse(time.DateOnly, from)
		if err != nil {
			return time.Time{}, time.Time{}, core.Invalid("from", "enter the start date as YYYY-MM-DD")
		}
		start = t
	}
	if !start.Before(end) {
		return time.Time{}, time.Time{}, core.Invalid("to", "the end date must be on or after the start date")
	}
	if end.Sub(start) > usageMaxRangeDays*24*time.Hour {
		return time.Time{}, time.Time{}, core.Invalid("from", "choose a range of 400 days or less")
	}
	return start, end, nil
}

// describeCalls adds the note title and, when people is set, the caller's
// name to call records. Each note is looked up once.
func (s *Server) describeCalls(ctx context.Context, actor *core.Actor, records []*core.AIUsageRecord, people map[string]*core.User) []usageCall {
	titles := map[string]string{}
	out := make([]usageCall, 0, len(records))
	for _, rec := range records {
		if rec == nil {
			continue
		}
		c := usageCall{AIUsageRecord: rec}
		if rec.NoteID != "" {
			title, seen := titles[rec.NoteID]
			if !seen {
				if note, _, err := s.backend.GetNote(ctx, actor, rec.NoteID); err == nil && note != nil {
					title = orDefault(note.Title, "Untitled")
				}
				titles[rec.NoteID] = title
			}
			c.NoteTitle = title
		}
		if people != nil {
			c.Person = personName(people, rec.UserID, "")
		}
		out = append(out, c)
	}
	return out
}

// budgetView describes one override for the budgets list.
func budgetView(i int, b *core.AIBudget, people map[string]*core.User, st *core.Settings, currency string, now time.Time) *usageBudget {
	vb := &usageBudget{AIBudget: b, Index: i, Anchor: budgetAnchor(b.SubjectKind, b.SubjectID), EditURL: budgetURL(b.SubjectKind, b.SubjectID, "edit=1")}
	if b.Status != nil {
		vb.State = *b.Status
	}
	vb.Limited = vb.State.TokenLimit > 0 || vb.State.CostLimit > 0
	if b.LiftedUntil != nil && b.LiftedUntil.After(now) {
		vb.LiftActive, vb.LiftEnds = true, *b.LiftedUntil
	}
	var defTokens int64
	var defCost float64
	if st != nil {
		defTokens, defCost = st.AIMonthlyTokenBudget, st.AIMonthlyCostBudget
	}
	user := b.SubjectKind == core.BudgetUser
	if user {
		vb.Name, vb.Sub = personName(people, b.SubjectID, ""), "Person"
		if u := people[b.SubjectID]; u != nil && u.Name != "" {
			vb.Sub = u.Email
		}
	} else {
		vb.Name, vb.Sub = b.SubjectID, "Plugin"
	}
	vb.TokenLimit = limitText(b.MonthlyTokens != nil, user, defTokens > 0, func() string {
		if b.MonthlyTokens != nil {
			return compactTokens(*b.MonthlyTokens) + " tokens"
		}
		return compactTokens(defTokens) + " tokens"
	}, b.MonthlyTokens != nil && *b.MonthlyTokens == 0, "No token limit")
	vb.CostLimit = limitText(b.MonthlyCost != nil, user, defCost > 0, func() string {
		if b.MonthlyCost != nil {
			return limitMoney(*b.MonthlyCost, currency)
		}
		return limitMoney(defCost, currency)
	}, b.MonthlyCost != nil && *b.MonthlyCost == 0, "No cost limit")
	if b.MonthlyTokens != nil {
		vb.TokensValue = strconv.FormatInt(*b.MonthlyTokens, 10)
	}
	if b.MonthlyCost != nil {
		vb.CostValue = strconv.FormatFloat(*b.MonthlyCost, 'f', -1, 64)
	}
	return vb
}

// limitText describes one limit of an override. A user override without a
// value shows the instance default. A zero value means no limit.
func limitText(set, user, hasDefault bool, amount func() string, zero bool, none string) string {
	switch {
	case !set && user && hasDefault:
		return amount() + " (default)"
	case !set && user:
		return none + " (default)"
	case !set, zero:
		return none
	}
	return amount()
}

// limitMoney formats a cost limit. Without a report currency it shows the
// bare amount.
func limitMoney(v float64, currency string) string {
	if currency == "" {
		return strconv.FormatFloat(v, 'f', 2, 64)
	}
	return usageMoney(v, currency)
}

// budgetAnchor is the element id of an override row.
func budgetAnchor(kind, id string) string {
	return "budget-" + kind + "-" + anchorUnsafe.ReplaceAllString(id, "_")
}

// budgetURL links to the budgets list with one row expanded. extra is a
// raw query fragment such as "done=saved".
func budgetURL(kind, id, extra string) string {
	target := "/admin/usage?" + url.Values{"row": {kind + ":" + id}}.Encode()
	if extra != "" {
		target += "&" + extra
	}
	return target + "#" + budgetAnchor(kind, id)
}

// usageChoices lists the distinct non-empty values of a breakdown for a
// datalist, with the current filter value first.
func usageChoices(rows []usageRow, current string, value func(usageRow) string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	add(current)
	for _, row := range rows {
		add(value(row))
	}
	return out
}

// personName names a user for a table. It prefers the account name, then
// the name the report carries, then the id.
func personName(people map[string]*core.User, id, reported string) string {
	if u := people[id]; u != nil {
		return orDefault(u.Name, u.Email)
	}
	if reported != "" {
		return reported
	}
	if id == "" {
		return "No person"
	}
	return id
}

// featureSeries keys a chart layer by feature.
func featureSeries(row core.AIUsageRow) (string, string) {
	return row.Feature, orDefault(row.Feature, "Unlabelled")
}

// compactTokens shortens a count to three significant figures at most, such
// as 842, 12.3k, 123k and 1.2M.
func compactTokens(n int64) string {
	sign := ""
	if n < 0 {
		sign, n = "-", -n
	}
	f := float64(n)
	unit := 0
	for unit < 3 && f >= 999.5 {
		f /= 1000
		unit++
	}
	if unit == 0 {
		return sign + strconv.FormatInt(n, 10)
	}
	var s string
	if f < 99.95 {
		s = strings.TrimSuffix(strconv.FormatFloat(f, 'f', 1, 64), ".0")
	} else {
		s = strconv.FormatFloat(f, 'f', 0, 64)
	}
	return sign + s + []string{"", "k", "M", "B"}[unit]
}

// usageNum writes a count with thousands separators.
func usageNum(n int64) string {
	s := strconv.FormatInt(n, 10)
	sign := ""
	if n < 0 {
		sign, s = "-", s[1:]
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return sign + b.String()
}

// usageTokens renders a compact token count. The exact counts are in the
// chart table and the call records. The markup holds only digits and unit
// letters.
func usageTokens(n int64) template.HTML {
	return template.HTML(`<span class="usage-num">` + compactTokens(n) + `</span>`)
}

// usageMoney formats a cost with two decimals and the report currency. An
// empty currency means no price is set, so the cost shows as a dash.
func usageMoney(v float64, currency string) string {
	if currency == "" {
		return "—"
	}
	amount := strconv.FormatFloat(v, 'f', 2, 64)
	if v > 0 && v < 0.005 {
		amount = "<0.01"
	}
	if currency == "mixed" {
		return amount + " (mixed)"
	}
	return amount + " " + currency
}

// usageDuration formats milliseconds as ms, seconds or minutes.
func usageDuration(ms int64) string {
	switch {
	case ms < 1000:
		return strconv.FormatInt(max(ms, 0), 10) + " ms"
	case ms < 60_000:
		return strconv.FormatFloat(float64(ms)/1000, 'f', 1, 64) + " s"
	}
	return strconv.FormatFloat(float64(ms)/60_000, 'f', 1, 64) + " min"
}

// usagePct formats a share of a budget with at most one decimal.
func usagePct(p float64) string {
	return strings.TrimSuffix(strconv.FormatFloat(math.Round(p*10)/10, 'f', 1, 64), ".0") + "%"
}

// usageProgress renders a Primer ProgressBar for a share of a budget
// (docs/primer.md 5.9). The fill is the accent colour below 80 percent, the
// attention colour from 80 percent and the danger colour from 100 percent.
// label names the bar for screen readers.
func usageProgress(p float64, label string) template.HTML {
	color := ""
	switch {
	case p >= 100:
		color = ` data-color="danger"`
	case p >= usageWarnPct:
		color = ` data-color="attention"`
	}
	clamped := math.Max(0, math.Min(p, 100))
	return template.HTML(`<span class="progress-bar usage-progress"` + color + ` role="progressbar" aria-label="` + template.HTMLEscapeString(label) +
		`" aria-valuemin="0" aria-valuemax="100" aria-valuenow="` + strconv.Itoa(int(math.Round(clamped))) + `" aria-valuetext="` + usagePct(p) + ` used">` +
		`<span class="progress-bar-item" style="width: ` + svgNum(clamped) + `%"></span></span>`)
}

// usageCount formats a count with its noun.
func usageCount(n int64, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return usageNum(n) + " " + many
}
