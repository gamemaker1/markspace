package ai

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"

	"github.com/gamemaker1/markspace/internal/core"
)

// Reasons a route target is skipped.
const (
	SkipProviderMissing  = "provider not found"
	SkipProviderDisabled = "provider disabled"
	SkipNotAllowed       = "provider not allowed"
	SkipProviderDown     = "provider down"
	SkipModelMissing     = "model not in catalog"
	SkipModelDisabled    = "model disabled"
	SkipModelUnavailable = "model unavailable"
	SkipIncapable        = "model lacks a capability"
	SkipContext          = "context too small"
)

// RouteProvider is the state of one provider that routing needs.
type RouteProvider struct {
	ID      string
	Kind    core.AIProviderKind
	OwnerID string
	Enabled bool
	// Down marks a provider that failed with a connection error, a timeout
	// or a 5xx in the last 60 s.
	Down            bool
	ImageGeneration bool
	ImageEditing    bool
}

// RouteRequest is everything ResolveRoute reads. It holds no pointers into
// shared state, so a caller may build it once per call.
type RouteRequest struct {
	// Task is the routing task.
	Task  string
	Needs core.AINeeds
	// UserID is the caller. An empty id may only use instance providers.
	UserID string
	// Requested is a model chosen in the request. It goes first.
	Requested       *core.AIRouteTarget
	AllowUserRoutes bool
	// UserRoutes and InstanceRoutes map a task to its targets.
	UserRoutes     map[string][]core.AIRouteTarget
	InstanceRoutes map[string][]core.AIRouteTarget
	// Providers maps a provider id to its state.
	Providers map[string]*RouteProvider
	// Models maps ModelKey(provider, model) to a catalog row.
	Models map[string]*core.AIModel
	// BudgetExhausted reports that the user or plugin budget of the call is
	// used up. The first target that would serve the call then ends the
	// chain with core.ErrQuotaExceeded.
	BudgetExhausted bool
}

// ModelKey is the key of a catalog row in RouteRequest.Models.
func ModelKey(providerID, model string) string { return providerID + "\x00" + model }

// RouteCandidate is a target that may serve the call.
type RouteCandidate struct {
	Target   core.AIRouteTarget
	Model    *core.AIModel
	Provider *RouteProvider
}

// RouteSkip is a target that cannot serve the call, and why.
type RouteSkip struct {
	Target core.AIRouteTarget `json:"target"`
	Reason string             `json:"reason"`
}

// RoutePlan is the result of ResolveRoute.
type RoutePlan struct {
	// Targets is the ordered, deduplicated list the plan was built from.
	Targets []core.AIRouteTarget
	// Candidates are the targets to try, in order.
	Candidates []RouteCandidate
	Skipped    []RouteSkip
	// QuotaExceeded is set when a target could serve the call but the budget
	// is used up. Candidates is then empty.
	QuotaExceeded bool
}

// RouteTargets assembles the ordered target list of a call: the requested
// model, the user's route for the task when user routes are allowed, the
// instance route for the task, and the instance route for default. The
// embeddings task uses only the first entry of the instance embeddings
// route, because vectors from different models cannot be mixed. A target
// that appears twice keeps its first place.
func RouteTargets(req RouteRequest) []core.AIRouteTarget {
	if req.Task == core.RouteEmbeddings {
		if list := req.InstanceRoutes[core.RouteEmbeddings]; len(list) > 0 {
			return []core.AIRouteTarget{list[0]}
		}
		return nil
	}
	var lists [][]core.AIRouteTarget
	if req.Requested != nil {
		lists = append(lists, []core.AIRouteTarget{*req.Requested})
	}
	if req.AllowUserRoutes {
		lists = append(lists, req.UserRoutes[req.Task])
	}
	lists = append(lists, req.InstanceRoutes[req.Task])
	if req.Task != core.RouteDefault {
		lists = append(lists, req.InstanceRoutes[core.RouteDefault])
	}
	seen := map[core.AIRouteTarget]bool{}
	var out []core.AIRouteTarget
	for _, list := range lists {
		for _, t := range list {
			if seen[t] {
				continue
			}
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// ResolveRoute decides which targets may serve a call, in order. A target is
// skipped when its provider is missing, disabled, not callable by the user
// or down, when the model is missing, disabled or unavailable, when it lacks
// a needed capability, or when its known context length is below the need.
// A personal provider serves only its owner. It is a pure function.
func ResolveRoute(req RouteRequest) RoutePlan {
	plan := RoutePlan{Targets: RouteTargets(req)}
	for _, t := range plan.Targets {
		reason, model, provider := checkTarget(req, t)
		if reason != "" {
			plan.Skipped = append(plan.Skipped, RouteSkip{Target: t, Reason: reason})
			continue
		}
		if req.BudgetExhausted {
			plan.QuotaExceeded = true
			plan.Candidates = nil
			return plan
		}
		plan.Candidates = append(plan.Candidates, RouteCandidate{Target: t, Model: model, Provider: provider})
	}
	return plan
}

// checkTarget returns the skip reason of one target, or the model and
// provider that serve it.
func checkTarget(req RouteRequest, t core.AIRouteTarget) (string, *core.AIModel, *RouteProvider) {
	p := req.Providers[t.ProviderID]
	switch {
	case p == nil:
		return SkipProviderMissing, nil, nil
	case p.OwnerID != "" && p.OwnerID != req.UserID:
		return SkipNotAllowed, nil, nil
	case !p.Enabled:
		return SkipProviderDisabled, nil, nil
	case p.Down:
		return SkipProviderDown, nil, nil
	}
	m := req.Models[ModelKey(t.ProviderID, t.Model)]
	switch {
	case m == nil:
		return SkipModelMissing, nil, nil
	case !m.Enabled:
		return SkipModelDisabled, nil, nil
	case !m.Available:
		return SkipModelUnavailable, nil, nil
	case !ModelCapable(m, p, req.Needs):
		return SkipIncapable, nil, nil
	case req.Needs.MinContext > 0 && m.ContextLength > 0 && m.ContextLength < req.Needs.MinContext:
		return SkipContext, nil, nil
	}
	return "", m, p
}

// ModelCapable reports whether a model of a provider has every capability in
// needs. A comfyui provider never serves text or embeddings. Image output
// needs the provider's imageGeneration flag, and image editing its
// imageEditing flag and an image input.
func ModelCapable(m *core.AIModel, p *RouteProvider, needs core.AINeeds) bool {
	if m == nil || p == nil {
		return false
	}
	if needs.Text || needs.ImageIn {
		if p.Kind == core.ProviderComfyUI || !m.HasOutput(core.ModalityText) {
			return false
		}
	}
	if needs.ImageIn && !m.HasInput(core.ModalityImage) {
		return false
	}
	if needs.ImageOut && (!p.ImageGeneration || !m.HasOutput(core.ModalityImage)) {
		return false
	}
	if needs.ImageEdit && (!p.ImageEditing || !m.HasOutput(core.ModalityImage) || !m.HasInput(core.ModalityImage)) {
		return false
	}
	if needs.Embedding && (p.Kind == core.ProviderComfyUI || p.Kind == core.ProviderAnthropic || !m.HasOutput(core.ModalityEmbedding)) {
		return false
	}
	return true
}

// contextLengthPhrases appear in the error bodies servers send for a prompt
// longer than the model's context window.
var contextLengthPhrases = []string{
	"context_length_exceeded", "context length", "context window", "maximum context",
	"too many tokens", "prompt is too long", "input is too long", "exceeds the context",
	"reduce the length", "maximum input length",
}

// IsContextLengthError reports whether err says the prompt does not fit the
// model's context window.
func IsContextLengthError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		msg += " " + strings.ToLower(apiErr.Body)
	}
	for _, phrase := range contextLengthPhrases {
		if strings.Contains(msg, phrase) {
			return true
		}
	}
	return false
}

// Retryable reports whether a failed call may move on to the next target.
// Timeouts, connection errors, 408, 429, 5xx and context-length errors are
// retryable. Other 4xx answers and refusals are final, because another model
// would fail the same way. Nothing is retryable once ctx is done, because
// the caller has gone.
func Retryable(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return false
	}
	if IsContextLengthError(err) {
		return true
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Status == http.StatusRequestTimeout || apiErr.Status == http.StatusTooManyRequests ||
			apiErr.Status >= 500
	}
	return ProviderDown(err) || errors.Is(err, core.ErrRateLimited) || errors.Is(err, core.ErrUnavailable) ||
		errors.Is(err, core.ErrUnsupported)
}

// ProviderDown reports whether err means the provider itself is failing: a
// timeout, a connection error or a 5xx answer. Routing skips such a
// provider for a while.
func ProviderDown(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Status >= 500
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

// Fallback tries n candidates in order. try runs candidate i and reports
// whether any output reached the caller before it failed. Fallback stops at
// the first success, at a final error, and after an error that followed
// streamed output. It returns the index of the last candidate tried, the
// number tried, and the error of the last try.
func Fallback(ctx context.Context, n int, try func(i int) (streamed bool, err error)) (last, tried int, err error) {
	last = -1
	for i := 0; i < n; i++ {
		var streamed bool
		streamed, err = try(i)
		last, tried = i, tried+1
		if err == nil || streamed || !Retryable(ctx, err) {
			return last, tried, err
		}
	}
	return last, tried, err
}
