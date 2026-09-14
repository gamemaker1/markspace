package index

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestAIModelsUpsertKeepsAdminSettings(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	seen := time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC)

	price := &core.AIPrice{InputPerMTok: 2.5, OutputPerMTok: 10, CacheReadPerMTok: 1.25}
	listing := []*core.AIModel{
		{ModelID: "gpt-4o", Name: "GPT-4o", Vendor: "openai", ContextLength: 128000, MaxOutput: 16384,
			Inputs: []string{"text", "image"}, Outputs: []string{"text"}, Tools: true, Price: price,
			PriceSource: core.PriceSourceOpenRouter, PriceMatched: "openai/gpt-4o", PriceFetchedAt: seen, Enabled: true},
		{ModelID: "text-embedding-3-small", Outputs: []string{"embedding"}, PriceSource: core.PriceSourceNone, Enabled: true},
	}
	if err := db.UpsertAIModels(ctx, "p", listing, seen); err != nil {
		t.Fatalf("UpsertAIModels: %v", err)
	}
	got, err := db.GetAIModel(ctx, "p", "gpt-4o")
	if err != nil {
		t.Fatalf("GetAIModel: %v", err)
	}
	want := &core.AIModel{ProviderID: "p", ModelID: "gpt-4o", Name: "GPT-4o", Vendor: "openai",
		ContextLength: 128000, MaxOutput: 16384, Inputs: []string{"text", "image"}, Outputs: []string{"text"},
		Tools: true, Price: price, PriceSource: core.PriceSourceOpenRouter, PriceMatched: "openai/gpt-4o",
		PriceFetchedAt: seen, Enabled: true, Available: true, LastSeen: seen}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("model = %+v\nwant %+v", got, want)
	}
	embed, _ := db.GetAIModel(ctx, "p", "text-embedding-3-small")
	if embed.Price != nil || !reflect.DeepEqual(embed.Inputs, []string{"text"}) || !embed.HasOutput(core.ModalityEmbedding) {
		t.Fatalf("embedding model = %+v", embed)
	}

	override := &core.AIPrice{InputPerMTok: 1}
	if err := db.UpdateAIModelSettings(ctx, "p", "gpt-4o", false, override); err != nil {
		t.Fatalf("UpdateAIModelSettings: %v", err)
	}
	later := seen.Add(24 * time.Hour)
	listing[0].Price = &core.AIPrice{InputPerMTok: 3}
	if err := db.UpsertAIModels(ctx, "p", listing[:1], later); err != nil {
		t.Fatalf("second UpsertAIModels: %v", err)
	}
	got, _ = db.GetAIModel(ctx, "p", "gpt-4o")
	if got.Enabled || !reflect.DeepEqual(got.PriceOverride, override) || got.Price.InputPerMTok != 3 || !got.LastSeen.Equal(later) {
		t.Fatalf("after relisting = %+v", got)
	}
	if p, src := got.Effective(); src != core.PriceSourceOverride || p.InputPerMTok != 1 {
		t.Fatalf("effective = %+v %s", p, src)
	}
	embed, _ = db.GetAIModel(ctx, "p", "text-embedding-3-small")
	if embed.Available || !embed.LastSeen.Equal(seen) {
		t.Fatalf("an unlisted model should stay unavailable with its old last seen: %+v", embed)
	}

	if err := db.UpdateAIModelSettings(ctx, "p", "gpt-4o", true, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetAIModel(ctx, "p", "gpt-4o")
	if got.PriceOverride != nil || !got.Enabled {
		t.Fatalf("cleared override = %+v", got)
	}
	got.Price, got.PriceSource, got.PriceMatched = nil, core.PriceSourceNone, ""
	if err := db.UpdateAIModelPrice(ctx, got); err != nil {
		t.Fatal(err)
	}
	if got, _ = db.GetAIModel(ctx, "p", "gpt-4o"); got.Price != nil || got.PriceSource != core.PriceSourceNone {
		t.Fatalf("price cleared = %+v", got)
	}
	if err := db.UpdateAIModelSettings(ctx, "p", "missing", true, nil); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("update of a missing row = %v", err)
	}
	if _, err := db.GetAIModel(ctx, "q", "gpt-4o"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("get of a missing row = %v", err)
	}

	// An empty listing marks every row unavailable.
	if err := db.UpsertAIModels(ctx, "p", nil, later); err != nil {
		t.Fatal(err)
	}
	all, err := db.ListAIModels(ctx, "")
	if err != nil || len(all) != 2 {
		t.Fatalf("ListAIModels = %d, %v", len(all), err)
	}
	for _, m := range all {
		if m.Available {
			t.Fatalf("still available after an empty listing: %+v", m)
		}
	}
	// A long listing passes the variable limit.
	long := make([]*core.AIModel, 1200)
	for i := range long {
		long[i] = &core.AIModel{ModelID: fmt.Sprintf("m-%04d", i), Enabled: true}
	}
	if err := db.UpsertAIModels(ctx, "big", long, later); err != nil {
		t.Fatalf("long listing: %v", err)
	}
	if rows, _ := db.ListAIModels(ctx, "big"); len(rows) != 1200 {
		t.Fatalf("long listing stored %d rows", len(rows))
	}

	if err := db.CreateAIProvider(ctx, &core.AIProviderConfig{ID: "p", Name: "P", Kind: core.ProviderOpenAI, BaseURL: "http://x"}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteAIProvider(ctx, "p"); err != nil {
		t.Fatal(err)
	}
	if rows, _ := db.ListAIModels(ctx, "p"); len(rows) != 0 {
		t.Fatalf("deleting a provider left %d catalog rows", len(rows))
	}
	if err := db.DeleteAIModels(ctx, "big"); err != nil {
		t.Fatal(err)
	}
	if rows, _ := db.ListAIModels(ctx, "big"); len(rows) != 0 {
		t.Fatalf("DeleteAIModels left %d rows", len(rows))
	}
}

func TestAIRoutesRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	u := mustUser(t, db, "router@example.com")

	edit := core.AIRoute{Task: core.RouteEdit, Targets: []core.AIRouteTarget{{ProviderID: "a", Model: "m1"}, {ProviderID: "b", Model: "m2"}}}
	if err := db.SaveAIRoute(ctx, core.RouteScopeInstance, edit); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAIRoute(ctx, core.RouteScopeInstance, core.AIRoute{Task: core.RouteChat}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAIRoute(ctx, u.ID, core.AIRoute{Task: core.RouteEdit, Targets: edit.Targets[1:]}); err != nil {
		t.Fatal(err)
	}
	got, err := db.ListAIRoutes(ctx, core.RouteScopeInstance)
	want := []core.AIRoute{{Task: core.RouteChat, Targets: []core.AIRouteTarget{}}, edit}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("instance routes = %+v, %v", got, err)
	}
	edit.Targets = edit.Targets[:1]
	if err := db.SaveAIRoute(ctx, core.RouteScopeInstance, edit); err != nil {
		t.Fatal(err)
	}
	if got, _ = db.ListAIRoutes(ctx, core.RouteScopeInstance); len(got[1].Targets) != 1 {
		t.Fatalf("replaced route = %+v", got)
	}
	if err := db.DeleteAIRoute(ctx, core.RouteScopeInstance, core.RouteChat); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteAIRoute(ctx, core.RouteScopeInstance, core.RouteChat); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
	if err := db.SaveAIRoute(ctx, "", edit); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("empty scope = %v", err)
	}
	if mine, _ := db.ListAIRoutes(ctx, u.ID); len(mine) != 1 {
		t.Fatalf("user routes = %+v", mine)
	}
	if err := db.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if mine, _ := db.ListAIRoutes(ctx, u.ID); len(mine) != 0 {
		t.Fatalf("deleting the user left routes %+v", mine)
	}
}

func TestPriceCatalogsKeepLastCopy(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	if _, _, err := db.GetPriceCatalog(ctx, "openrouter"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("missing catalog = %v", err)
	}
	at := time.Date(2026, 9, 13, 3, 0, 0, 0, time.UTC)
	for i, body := range []string{`{"data":[]}`, `{"data":[{"id":"x"}]}`} {
		if err := db.SavePriceCatalog(ctx, "openrouter", []byte(body), at.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	body, fetched, err := db.GetPriceCatalog(ctx, "openrouter")
	if err != nil || string(body) != `{"data":[{"id":"x"}]}` || !fetched.Equal(at.Add(time.Hour)) {
		t.Fatalf("catalog = %s %v %v", body, fetched, err)
	}
}

func TestUsageRecordsTaskAndAttempts(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	rec := core.AIUsageRecord{UserID: "u", Task: core.RouteVision, Attempts: 3, CreatedAt: time.Now()}
	if err := db.RecordAIUsage(ctx, &rec); err != nil {
		t.Fatal(err)
	}
	zero := core.AIUsageRecord{UserID: "u", CreatedAt: time.Now()}
	if err := db.RecordAIUsage(ctx, &zero); err != nil {
		t.Fatal(err)
	}
	calls, err := db.AIUsageCalls(ctx, core.AIUsageQuery{UserID: "u"}, core.UsageSortRecent)
	if err != nil || len(calls) != 2 {
		t.Fatalf("calls = %+v, %v", calls, err)
	}
	byID := map[string]*core.AIUsageRecord{}
	for _, c := range calls {
		byID[c.ID] = c
	}
	if byID[rec.ID].Task != core.RouteVision || byID[rec.ID].Attempts != 3 || byID[zero.ID].Attempts != 1 {
		t.Fatalf("task and attempts = %+v %+v", byID[rec.ID], byID[zero.ID])
	}
}
