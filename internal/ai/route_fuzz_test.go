package ai

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// fuzzBytes reads small numbers from fuzz input and returns 0 once the input
// is used up.
type fuzzBytes struct {
	data []byte
	pos  int
}

func (b *fuzzBytes) next() int {
	if b.pos >= len(b.data) {
		return 0
	}
	v := int(b.data[b.pos])
	b.pos++
	return v
}

// pick returns a number in [0, n).
func (b *fuzzBytes) pick(n int) int { return b.next() % n }

func (b *fuzzBytes) flag() bool { return b.next()%2 == 1 }

// fuzzScenario builds a random routing request from fuzz input.
func fuzzScenario(b *fuzzBytes) RouteRequest {
	kinds := []core.AIProviderKind{core.ProviderOpenAI, core.ProviderOllama, core.ProviderAnthropic, core.ProviderComfyUI}
	owners := []string{"", "", "u1", "u2"}
	users := []string{"u1", "u2", ""}
	tasks := append(append([]string{}, core.AIRouteTasks...), "plugin.wiki.summarise")
	modalities := [][]string{{"text"}, {"text", "image"}, {"image"}, {"embedding"}, {"text", "file"}}
	contexts := []int{0, 100, 1000, 8000, 128000}

	req := RouteRequest{
		Providers: map[string]*RouteProvider{},
		Models:    map[string]*core.AIModel{},
	}
	nProviders := 1 + b.pick(4)
	var targets []core.AIRouteTarget
	for i := 0; i < nProviders; i++ {
		id := fmt.Sprintf("p%d", i)
		req.Providers[id] = &RouteProvider{
			ID: id, Kind: kinds[b.pick(len(kinds))], OwnerID: owners[b.pick(len(owners))],
			Enabled: b.pick(4) != 0, Down: b.pick(5) == 0,
			ImageGeneration: b.flag(), ImageEditing: b.flag(),
		}
		for j := 0; j < b.pick(4); j++ {
			model := fmt.Sprintf("m%d", j)
			req.Models[ModelKey(id, model)] = &core.AIModel{
				ProviderID: id, ModelID: model,
				Inputs:  modalities[b.pick(len(modalities))],
				Outputs: modalities[b.pick(len(modalities))],
				Enabled: b.pick(5) != 0, Available: b.pick(5) != 0,
				ContextLength: contexts[b.pick(len(contexts))],
			}
		}
	}
	// Targets may name providers and models that do not exist.
	randomTarget := func() core.AIRouteTarget {
		return core.AIRouteTarget{ProviderID: fmt.Sprintf("p%d", b.pick(5)), Model: fmt.Sprintf("m%d", b.pick(4))}
	}
	randomList := func() []core.AIRouteTarget {
		var list []core.AIRouteTarget
		for n := b.pick(5); n > 0; n-- {
			list = append(list, randomTarget())
		}
		return list
	}
	req.Task = tasks[b.pick(len(tasks))]
	req.Needs = core.RouteNeeds(req.Task)
	if b.pick(3) == 0 {
		req.Needs.ImageIn = true
	}
	req.Needs.MinContext = []int{0, 0, 500, 5000, 200000}[b.pick(5)]
	req.UserID = users[b.pick(len(users))]
	req.AllowUserRoutes = b.flag()
	req.BudgetExhausted = b.pick(6) == 0
	if b.flag() {
		t := randomTarget()
		req.Requested = &t
	}
	req.UserRoutes = map[string][]core.AIRouteTarget{}
	req.InstanceRoutes = map[string][]core.AIRouteTarget{}
	for n := b.pick(6); n > 0; n-- {
		req.UserRoutes[tasks[b.pick(len(tasks))]] = randomList()
	}
	for n := 1 + b.pick(6); n > 0; n-- {
		req.InstanceRoutes[tasks[b.pick(len(tasks))]] = randomList()
	}
	_ = targets
	return req
}

// refResolve is the reference implementation of ResolveRoute. It is written
// from docs/models.md without sharing code with the implementation.
func refResolve(req RouteRequest) (cands []core.AIRouteTarget, quota bool) {
	var raw []core.AIRouteTarget
	if req.Task == "embeddings" {
		if list := req.InstanceRoutes["embeddings"]; len(list) > 0 {
			raw = append(raw, list[0])
		}
	} else {
		if req.Requested != nil {
			raw = append(raw, *req.Requested)
		}
		if req.AllowUserRoutes {
			raw = append(raw, req.UserRoutes[req.Task]...)
		}
		raw = append(raw, req.InstanceRoutes[req.Task]...)
		if req.Task != "default" {
			raw = append(raw, req.InstanceRoutes["default"]...)
		}
	}
	var list []core.AIRouteTarget
outer:
	for _, t := range raw {
		for _, u := range list {
			if u == t {
				continue outer
			}
		}
		list = append(list, t)
	}
	has := func(xs []string, x string) bool {
		for _, v := range xs {
			if v == x {
				return true
			}
		}
		return false
	}
	for _, t := range list {
		p, ok := req.Providers[t.ProviderID]
		if !ok || !p.Enabled || p.Down {
			continue
		}
		if p.OwnerID != "" && p.OwnerID != req.UserID {
			continue
		}
		m, ok := req.Models[t.ProviderID+"\x00"+t.Model]
		if !ok || !m.Enabled || !m.Available {
			continue
		}
		n := req.Needs
		chat := p.Kind != core.ProviderComfyUI && has(m.Outputs, "text")
		requirements := []bool{
			!n.Text || chat,
			!n.ImageIn || (chat && has(m.Inputs, "image")),
			!n.ImageOut || (p.ImageGeneration && has(m.Outputs, "image")),
			!n.ImageEdit || (p.ImageEditing && has(m.Outputs, "image") && has(m.Inputs, "image")),
			!n.Embedding || (p.Kind != core.ProviderComfyUI && p.Kind != core.ProviderAnthropic && has(m.Outputs, "embedding")),
			n.MinContext == 0 || m.ContextLength == 0 || m.ContextLength >= n.MinContext,
		}
		capable := true
		for _, r := range requirements {
			capable = capable && r
		}
		if !capable {
			continue
		}
		if req.BudgetExhausted {
			return nil, true
		}
		cands = append(cands, t)
	}
	return cands, false
}

// FuzzResolveRoute compares ResolveRoute with the reference over random
// catalogs, capabilities, context lengths, flags, personal providers, user
// routes and budgets, then runs a random failure sequence through Fallback.
func FuzzResolveRoute(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{3, 0, 0, 1, 1, 0, 0, 3, 0, 1, 1, 1, 4, 0, 2, 1, 1, 1, 0, 1, 3, 2, 1, 0, 0, 2})
	f.Add([]byte("the quick brown fox jumps over the lazy dog, twice over, with a budget"))
	f.Add([]byte{255, 254, 253, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20})
	retryable := &APIError{Status: 503}
	final := &APIError{Status: 400}
	f.Fuzz(func(t *testing.T, data []byte) {
		b := &fuzzBytes{data: data}
		req := fuzzScenario(b)
		plan := ResolveRoute(req)
		want, wantQuota := refResolve(req)
		var got []core.AIRouteTarget
		for _, c := range plan.Candidates {
			got = append(got, c.Target)
			if c.Provider.OwnerID != "" && c.Provider.OwnerID != req.UserID {
				t.Fatalf("personal provider %s of %s serves %q", c.Provider.ID, c.Provider.OwnerID, req.UserID)
			}
			if c.Model == nil || c.Provider == nil || c.Target.ProviderID != c.Provider.ID {
				t.Fatalf("candidate without its model or provider: %+v", c)
			}
		}
		if !reflect.DeepEqual(got, want) || plan.QuotaExceeded != wantQuota {
			t.Fatalf("ResolveRoute = %+v quota %v\nreference = %+v quota %v\nrequest %+v",
				got, plan.QuotaExceeded, want, wantQuota, req)
		}
		if len(plan.Candidates)+len(plan.Skipped) > len(plan.Targets) {
			t.Fatalf("more decisions than targets: %+v", plan)
		}
		if req.Task == core.RouteEmbeddings && len(plan.Targets) > 1 {
			t.Fatalf("embeddings considered %d targets", len(plan.Targets))
		}

		// A random failure sequence. 0 succeeds, 1 fails retryably, 2 fails
		// finally, 3 fails retryably after streamed output.
		outcomes := make([]int, len(plan.Candidates))
		for i := range outcomes {
			outcomes[i] = b.pick(4)
		}
		var calls []int
		last, tried, err := Fallback(context.Background(), len(outcomes), func(i int) (bool, error) {
			calls = append(calls, i)
			switch outcomes[i] {
			case 1:
				return false, retryable
			case 2:
				return false, final
			case 3:
				return true, retryable
			}
			return false, nil
		})
		wantCalls := 0
		for i := range outcomes {
			wantCalls++
			if outcomes[i] != 1 {
				break
			}
		}
		if len(outcomes) == 0 {
			wantCalls = 0
		}
		if len(calls) != wantCalls || tried != wantCalls {
			t.Fatalf("outcomes %v: calls %v tried %d, want %d", outcomes, calls, tried, wantCalls)
		}
		for i, c := range calls {
			if c != i {
				t.Fatalf("calls out of order: %v", calls)
			}
			if i < len(calls)-1 && outcomes[c] != 1 {
				t.Fatalf("fallback after a final error or streamed text: outcomes %v calls %v", outcomes, calls)
			}
		}
		if wantCalls > 0 {
			switch outcomes[last] {
			case 0:
				if err != nil {
					t.Fatalf("success reported %v", err)
				}
			case 2:
				if err != final {
					t.Fatalf("final error lost: %v", err)
				}
			default:
				if err != retryable {
					t.Fatalf("retryable error lost: %v", err)
				}
			}
		}
	})
}
