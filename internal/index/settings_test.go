package index

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestSettingsDefaultsThenSave(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	got, err := db.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings on a fresh database: %v", err)
	}
	want := core.Settings{InstanceName: "Markspace", AllowSignup: true, AIEnabled: true, AIAllowUserKeys: true, AIAllowUserRoutes: true}
	if *got != want {
		t.Fatalf("defaults are %+v, want %+v", *got, want)
	}

	s := &core.Settings{
		InstanceName:      "Team notes",
		AllowSignup:       false,
		DefaultSpaceID:    "space-1",
		AIEnabled:         false,
		AIAllowUserKeys:   false,
		AIAllowUserRoutes: false,
	}
	if err := db.SaveSettings(ctx, s); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	got, err = db.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got.InstanceName != "Team notes" || got.AllowSignup || got.DefaultSpaceID != "space-1" ||
		got.AIEnabled || got.AIAllowUserKeys || got.AIAllowUserRoutes || got.UpdatedAt.IsZero() {
		t.Fatalf("saved settings read back as %+v", got)
	}

	// A second save updates the single row.
	s.AllowSignup = true
	if err := db.SaveSettings(ctx, s); err != nil {
		t.Fatalf("second SaveSettings: %v", err)
	}
	var rows int
	_ = db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM settings`).Scan(&rows)
	if rows != 1 {
		t.Fatalf("settings holds %d rows, want 1", rows)
	}
	if got, _ = db.GetSettings(ctx); !got.AllowSignup {
		t.Fatal("the second save did not stick")
	}
}

func TestAIProviders(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	local := &core.AIProviderConfig{
		Name:           "Local",
		Kind:           core.ProviderOllama,
		BaseURL:        "http://127.0.0.1:11434",
		Model:          "llama3",
		EmbedModel:     "nomic-embed-text",
		Models:         []string{"llama3", "qwen"},
		Headers:        map[string]string{"X-Test": "1"},
		Temperature:    0.25,
		MaxTokens:      1024,
		TimeoutSeconds: 60,
		Enabled:        true,
		Preset:         "ollama",
		Local:          true,
	}
	if err := db.CreateAIProvider(ctx, local); err != nil {
		t.Fatalf("CreateAIProvider: %v", err)
	}
	remote := &core.AIProviderConfig{Name: "Remote", Kind: core.ProviderOpenAI, APIKey: "sk-test",
		Enabled: true}
	if err := db.CreateAIProvider(ctx, remote); err != nil {
		t.Fatalf("CreateAIProvider remote: %v", err)
	}

	got, err := db.GetAIProvider(ctx, local.ID)
	if err != nil {
		t.Fatalf("GetAIProvider: %v", err)
	}
	if got.Kind != core.ProviderOllama || !slices.Equal(got.Models, []string{"llama3", "qwen"}) ||
		got.Headers["X-Test"] != "1" || math.Abs(got.Temperature-0.25) > 1e-9 ||
		got.MaxTokens != 1024 || got.TimeoutSeconds != 60 || got.Source != "db" || got.HasAPIKey ||
		got.Preset != "ollama" || !got.Local {
		t.Fatalf("GetAIProvider gave %+v", got)
	}

	gotRemote, _ := db.GetAIProvider(ctx, remote.ID)
	if !gotRemote.HasAPIKey || gotRemote.APIKey != "sk-test" {
		t.Fatalf("the API key did not round trip: %+v", gotRemote)
	}

	list, err := db.ListAIProviders(ctx)
	if err != nil {
		t.Fatalf("ListAIProviders: %v", err)
	}
	if len(list) != 2 || list[0].ID != local.ID {
		t.Fatalf("ListAIProviders should order by name, got %d rows", len(list))
	}

	got.Models = nil
	got.Local = false
	if err := db.UpdateAIProvider(ctx, got); err != nil {
		t.Fatalf("UpdateAIProvider: %v", err)
	}
	if got, _ = db.GetAIProvider(ctx, local.ID); got.Models != nil || got.Local {
		t.Fatalf("Models should read back as nil and Local as false, got %+v", got)
	}

	if err := db.DeleteAIProvider(ctx, local.ID); err != nil {
		t.Fatalf("DeleteAIProvider: %v", err)
	}
	if _, err := db.GetAIProvider(ctx, local.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("GetAIProvider after delete gave %v", err)
	}
	if err := db.UpdateAIProvider(ctx, local); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("UpdateAIProvider on a deleted row gave %v", err)
	}
}

func TestAIProviderOwnerPersists(t *testing.T) {
	db := newTestDB(t)
	ctx := t.Context()

	personal := &core.AIProviderConfig{ID: "mine", Name: "Mine", Kind: core.ProviderOllama,
		BaseURL: "http://127.0.0.1:11434", Model: "llama3.2", OwnerID: "user-1"}
	if err := db.CreateAIProvider(ctx, personal); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := db.GetAIProvider(ctx, "mine")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.OwnerID != "user-1" {
		t.Fatalf("owner %q, want user-1", got.OwnerID)
	}

	// An update carrying a different owner does not reassign the provider.
	got.OwnerID = "user-2"
	got.Name = "Renamed"
	if err := db.UpdateAIProvider(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	again, err := db.GetAIProvider(ctx, "mine")
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if again.OwnerID != "user-1" || again.Name != "Renamed" {
		t.Fatalf("after update owner %q name %q, want user-1 and Renamed", again.OwnerID, again.Name)
	}
}
