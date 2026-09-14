package ai

import (
	"net/http"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
)

// aiBudgetInput is the body of PUT /api/admin/ai/budgets/{kind}/{id}. A null
// or omitted limit uses the instance default for a user and means no limit
// for a plugin. 0 means no limit.
type aiBudgetInput struct {
	MonthlyTokens *int64   `json:"monthlyTokens"`
	MonthlyCost   *float64 `json:"monthlyCost"`
}

// usageTime reads a from or to parameter as RFC 3339 or as a YYYY-MM-DD UTC
// date. A date used as the end of a range covers that whole day.
func usageTime(field, raw string, end bool) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		if end {
			t = t.AddDate(0, 0, 1)
		}
		return t, nil
	}
	return time.Time{}, core.Invalid(field, field+" must be RFC 3339 or YYYY-MM-DD")
}

// usageQuery reads the query parameters shared by the usage routes. The user
// filter is read only for admin routes.
func usageQuery(r *http.Request, admin bool) (core.AIUsageQuery, error) {
	v := r.URL.Query()
	var q core.AIUsageQuery
	var err error
	if q.From, err = usageTime("from", v.Get("from"), false); err != nil {
		return q, err
	}
	if q.To, err = usageTime("to", v.Get("to"), true); err != nil {
		return q, err
	}
	if g := strings.TrimSpace(v.Get("groupBy")); g != "" {
		q.GroupBy = strings.Split(g, ",")
	}
	q.ProviderID = strings.TrimSpace(v.Get("provider"))
	q.Model = strings.TrimSpace(v.Get("model"))
	q.Feature = strings.TrimSpace(v.Get("feature"))
	q.PluginID = strings.TrimSpace(v.Get("plugin"))
	if admin {
		q.UserID = strings.TrimSpace(v.Get("user"))
	}
	q.Limit = api.IntParam(v.Get("limit"), 0, 0)
	return q, nil
}

// myAIUsage serves GET /api/ai/usage.
func (m *Module) myAIUsage(w http.ResponseWriter, r *http.Request) {
	if err := api.RequireUser(api.ActorFrom(r.Context())); err != nil {
		m.Fail(w, r, err)
		return
	}
	q, err := usageQuery(r, false)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	out, err := m.Backend.MyAIUsage(r.Context(), api.ActorFrom(r.Context()), q)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, out)
}

// myAIUsageSummary serves GET /api/ai/usage/summary.
func (m *Module) myAIUsageSummary(w http.ResponseWriter, r *http.Request) {
	if err := api.RequireUser(api.ActorFrom(r.Context())); err != nil {
		m.Fail(w, r, err)
		return
	}
	out, err := m.Backend.AIUsageSummary(r.Context(), api.ActorFrom(r.Context()))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, out)
}

// myAIUsageCalls serves GET /api/ai/usage/calls, the actor's own calls.
func (m *Module) myAIUsageCalls(w http.ResponseWriter, r *http.Request) {
	actor := api.ActorFrom(r.Context())
	if err := api.RequireUser(actor); err != nil {
		m.Fail(w, r, err)
		return
	}
	q, err := usageQuery(r, false)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	q.UserID = actor.UserID()
	calls, err := m.Backend.AIUsageCalls(r.Context(), actor, q, r.URL.Query().Get("sort"))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, calls)
}

// adminAIUsage serves GET /api/admin/ai/usage.
func (m *Module) adminAIUsage(w http.ResponseWriter, r *http.Request) {
	actor := api.ActorFrom(r.Context())
	if err := api.RequireAdmin(actor); err != nil {
		m.Fail(w, r, err)
		return
	}
	q, err := usageQuery(r, true)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	out, err := m.Backend.AIUsage(r.Context(), actor, q)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, out)
}

// adminAIUsageCalls serves GET /api/admin/ai/usage/calls.
func (m *Module) adminAIUsageCalls(w http.ResponseWriter, r *http.Request) {
	actor := api.ActorFrom(r.Context())
	if err := api.RequireAdmin(actor); err != nil {
		m.Fail(w, r, err)
		return
	}
	q, err := usageQuery(r, true)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	calls, err := m.Backend.AIUsageCalls(r.Context(), actor, q, r.URL.Query().Get("sort"))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, calls)
}

// listAIBudgets serves GET /api/admin/ai/budgets.
func (m *Module) listAIBudgets(w http.ResponseWriter, r *http.Request) {
	actor := api.ActorFrom(r.Context())
	if err := api.RequireAdmin(actor); err != nil {
		m.Fail(w, r, err)
		return
	}
	list, err := m.Backend.ListAIBudgets(r.Context(), actor)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, list)
}

// saveAIBudget serves PUT /api/admin/ai/budgets/{kind}/{id}. An empty body
// stores an override with both limits null.
func (m *Module) saveAIBudget(w http.ResponseWriter, r *http.Request) {
	actor := api.ActorFrom(r.Context())
	if err := api.RequireAdmin(actor); err != nil {
		m.Fail(w, r, err)
		return
	}
	var in aiBudgetInput
	if r.ContentLength != 0 {
		if err := api.Decode(r, &in); err != nil {
			m.Fail(w, r, err)
			return
		}
	}
	out, err := m.Backend.SaveAIBudget(r.Context(), actor, core.AIBudget{
		SubjectKind: r.PathValue("kind"), SubjectID: r.PathValue("id"),
		MonthlyTokens: in.MonthlyTokens, MonthlyCost: in.MonthlyCost,
	})
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, out)
}

// deleteAIBudget serves DELETE /api/admin/ai/budgets/{kind}/{id}.
func (m *Module) deleteAIBudget(w http.ResponseWriter, r *http.Request) {
	actor := api.ActorFrom(r.Context())
	if err := api.RequireAdmin(actor); err != nil {
		m.Fail(w, r, err)
		return
	}
	if err := m.Backend.DeleteAIBudget(r.Context(), actor, r.PathValue("kind"), r.PathValue("id")); err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}

// liftAIBudget serves POST /api/admin/ai/budgets/{kind}/{id}/lift.
func (m *Module) liftAIBudget(w http.ResponseWriter, r *http.Request) {
	actor := api.ActorFrom(r.Context())
	if err := api.RequireAdmin(actor); err != nil {
		m.Fail(w, r, err)
		return
	}
	out, err := m.Backend.LiftAIBudget(r.Context(), actor, r.PathValue("kind"), r.PathValue("id"))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, out)
}
