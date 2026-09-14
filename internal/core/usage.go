package core

import (
	"math"
	"strings"
	"time"
)

// Usage statuses. Every provider call records exactly one of them.
const (
	UsageOK       = "ok"
	UsageError    = "error"
	UsageCanceled = "canceled"
	UsageBudget   = "budget"
)

// Known usage features. Feature is free-form, these are the values the
// server and the bundled UI send.
const (
	FeatureAsk        = "ask"
	FeatureRewrite    = "rewrite"
	FeatureImage      = "image"
	FeatureChat       = "chat"
	FeatureSearch     = "search"
	FeatureEmbeddings = "embeddings"
	FeatureTest       = "test"
)

// Budget subject kinds.
const (
	BudgetUser   = "user"
	BudgetPlugin = "plugin"
)

// AIUsageRecord is one provider call.
type AIUsageRecord struct {
	ID         string `json:"id"`
	UserID     string `json:"userId,omitempty"`
	ProviderID string `json:"providerId,omitempty"`
	Model      string `json:"model,omitempty"`
	Feature    string `json:"feature,omitempty"`
	// Task is the routing task, such as edit, answer or embeddings.
	Task         string `json:"task,omitempty"`
	PluginID     string `json:"pluginId,omitempty"`
	SpaceID      string `json:"spaceId,omitempty"`
	NoteID       string `json:"noteId,omitempty"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
	ImagesIn     int64  `json:"imagesIn"`
	ImagesOut    int64  `json:"imagesOut"`
	DurationMs   int64  `json:"durationMs"`
	// Status is ok, error, canceled or budget.
	Status string `json:"status"`
	// Error is the provider error, cut to 500 bytes.
	Error string `json:"error,omitempty"`
	// CostMicros is the cost in millionths of the currency unit, fixed when
	// the call was recorded. Cost is the same value in currency units.
	CostMicros int64   `json:"costMicros"`
	Cost       float64 `json:"cost"`
	// Estimated reports that the provider sent no token counts and the
	// server estimated them as ceil(characters / 4).
	Estimated bool `json:"estimated"`
	// Attempts is the number of route targets tried up to and including
	// this call. It is 1 when the first target answered.
	Attempts  int       `json:"attempts"`
	CreatedAt time.Time `json:"createdAt"`
}

// Usage group-by keys.
const (
	GroupDay      = "day"
	GroupMonth    = "month"
	GroupUser     = "user"
	GroupProvider = "provider"
	GroupModel    = "model"
	GroupFeature  = "feature"
	GroupPlugin   = "plugin"
)

// UsageGroupKeys lists every valid AIUsageQuery.GroupBy value.
var UsageGroupKeys = []string{GroupDay, GroupMonth, GroupUser, GroupProvider, GroupModel, GroupFeature, GroupPlugin}

// NormalizeUsageGroupBy validates group keys, drops blanks and duplicates,
// and returns the keys in the order of UsageGroupKeys.
func NormalizeUsageGroupBy(keys []string) ([]string, error) {
	want := map[string]bool{}
	for _, k := range keys {
		k = strings.ToLower(strings.TrimSpace(k))
		if k == "" {
			continue
		}
		known := false
		for _, g := range UsageGroupKeys {
			known = known || g == k
		}
		if !known {
			return nil, Invalid("groupBy", "unknown group key "+k+", use day, month, user, provider, model, feature or plugin")
		}
		want[k] = true
	}
	out := []string{}
	for _, k := range UsageGroupKeys {
		if want[k] {
			out = append(out, k)
		}
	}
	return out, nil
}

// Usage call list orders.
const (
	UsageSortRecent  = "recent"
	UsageSortSlowest = "slowest"
	UsageSortFailed  = "failed"
)

// AIUsageQuery selects usage for a report or a call list. From is inclusive
// and To exclusive. A zero time leaves that end open. An empty string filter
// matches every value.
type AIUsageQuery struct {
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`
	UserID     string    `json:"userId,omitempty"`
	ProviderID string    `json:"providerId,omitempty"`
	Model      string    `json:"model,omitempty"`
	Feature    string    `json:"feature,omitempty"`
	PluginID   string    `json:"pluginId,omitempty"`
	// GroupBy holds any of day, month, user, provider, model, feature and
	// plugin. No keys yields one row equal to the totals.
	GroupBy []string `json:"groupBy,omitempty"`
	// Limit caps the rows of a report or a call list.
	Limit int `json:"limit,omitempty"`
}

// AIUsageRow is one group of a usage report. Only the keys named in GroupBy
// are set. A key absent from the JSON is the empty value.
type AIUsageRow struct {
	// Day is YYYY-MM-DD for raw rows. A month that retention has rolled up
	// has no days, so its rows carry the month, YYYY-MM, as the day.
	Day        string `json:"day,omitempty"`
	Month      string `json:"month,omitempty"`
	UserID     string `json:"userId,omitempty"`
	UserName   string `json:"userName,omitempty"`
	ProviderID string `json:"providerId,omitempty"`
	Model      string `json:"model,omitempty"`
	Feature    string `json:"feature,omitempty"`
	PluginID   string `json:"pluginId,omitempty"`

	Calls        int64 `json:"calls"`
	Errors       int64 `json:"errors"`
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
	ImagesIn     int64 `json:"imagesIn"`
	ImagesOut    int64 `json:"imagesOut"`
	DurationMs   int64 `json:"durationMs"`
	// AvgDurationMs is DurationMs divided by Calls, rounded down.
	AvgDurationMs int64 `json:"avgDurationMs"`
	// CostMicros is the summed cost in millionths, Cost the same in units.
	CostMicros     int64   `json:"costMicros"`
	Cost           float64 `json:"cost"`
	EstimatedCalls int64   `json:"estimatedCalls"`
}

// Finish derives AvgDurationMs and Cost from the summed fields.
func (r *AIUsageRow) Finish() {
	r.AvgDurationMs = 0
	if r.Calls > 0 {
		r.AvgDurationMs = r.DurationMs / r.Calls
	}
	r.Cost = MicrosToCost(r.CostMicros)
}

// MicrosToCost converts millionths of a currency unit to units.
func MicrosToCost(micros int64) float64 { return float64(micros) / 1e6 }

// CostToMicros converts currency units to millionths, treating negative,
// NaN and infinite values as 0.
func CostToMicros(cost float64) int64 {
	cost = clean(cost)
	if cost*1e6 >= math.MaxInt64/4 {
		return math.MaxInt64 / 4
	}
	return int64(math.Round(cost * 1e6))
}

// AIUsageReport is the answer to a usage query.
type AIUsageReport struct {
	From    time.Time    `json:"from"`
	To      time.Time    `json:"to"`
	GroupBy []string     `json:"groupBy"`
	Rows    []AIUsageRow `json:"rows"`
	Totals  AIUsageRow   `json:"totals"`
	// Currency is always USD, the currency of every catalog price.
	Currency string `json:"currency"`
}

// AIBudget is a monthly limit for one user or plugin. A nil limit uses the
// instance default for users and means no limit for plugins. A limit of 0
// means no limit.
type AIBudget struct {
	SubjectKind   string   `json:"subjectKind"`
	SubjectID     string   `json:"subjectId"`
	MonthlyTokens *int64   `json:"monthlyTokens"`
	MonthlyCost   *float64 `json:"monthlyCost"`
	// LiftedUntil lets a stopped subject call again until that instant, the
	// start of the next UTC month when set by a lift.
	LiftedUntil *time.Time `json:"liftedUntil,omitempty"`
	// Status is the month-to-date state. Listings fill it in.
	Status *AIBudgetStatus `json:"status,omitempty"`
}

// AIBudgetStatus is the month-to-date usage of a subject against its limits.
type AIBudgetStatus struct {
	// TokenLimit and CostLimit are the effective limits. 0 means no limit.
	TokenLimit int64   `json:"tokenLimit"`
	CostLimit  float64 `json:"costLimit"`
	TokensUsed int64   `json:"tokensUsed"`
	CostUsed   float64 `json:"costUsed"`
	// UsedPct is the higher of tokens and cost used as a percentage of its
	// limit. It is 0 when there is no limit and may exceed 100.
	UsedPct float64 `json:"usedPct"`
	// Warn is set from 80 percent.
	Warn bool `json:"warn"`
	// Exceeded is set from 100 percent. Stopped is Exceeded without an
	// active lift, meaning calls are refused.
	Exceeded    bool       `json:"exceeded"`
	Stopped     bool       `json:"stopped"`
	LiftedUntil *time.Time `json:"liftedUntil,omitempty"`
}

// AIBudgetWarning rides on an AI result or done frame once a subject has
// used 80 percent of a monthly budget.
type AIBudgetWarning struct {
	UsedPct float64 `json:"usedPct"`
	Warn    bool    `json:"warn"`
}

// AIUsagePeriod is the usage of one actor over a period.
type AIUsagePeriod struct {
	Calls        int64   `json:"calls"`
	InputTokens  int64   `json:"inputTokens"`
	OutputTokens int64   `json:"outputTokens"`
	Tokens       int64   `json:"tokens"`
	Cost         float64 `json:"cost"`
}

// AIUsageSummary feeds the small usage indicator in the AI UI. Days and
// months are UTC.
type AIUsageSummary struct {
	Today    AIUsagePeriod  `json:"today"`
	Month    AIUsagePeriod  `json:"month"`
	Currency string         `json:"currency"`
	Budget   AIBudgetStatus `json:"budget"`
}
