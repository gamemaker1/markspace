package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// TestPresetsRoutesAndPublicPrices checks that a preset fills the protocol,
// base URL and local flag, that an explicit local key wins over the preset,
// that routes load, and that the public price switch reads the file and the
// environment.
func TestPresetsRoutesAndPublicPrices(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		t.Helper()
		path := filepath.Join(dir, "c.yaml")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	path := write(`dataDir: ` + dir + `
ai:
  fetchPublicPrices: false
  routes:
    default:
      - {providerId: openrouter, model: openai/gpt-4o-mini}
      - {providerId: box, model: llama3.2}
    embeddings:
      - {providerId: box, model: nomic-embed-text}
  providers:
    - preset: openrouter
      apiKey: k
    - id: box
      preset: ollama
      baseUrl: http://127.0.0.1:11434
    - id: rented
      preset: ollama
      local: false
    - id: pics
      preset: comfyui
      baseUrl: http://127.0.0.1:8188
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	byID := map[string]core.AIProviderConfig{}
	for _, p := range cfg.AI.Providers {
		byID[p.ID] = p
	}
	or := byID["openrouter"]
	if or.Kind != core.ProviderOpenAI || or.BaseURL != "https://openrouter.ai/api/v1" || or.Local || or.Headers["X-Title"] == "" {
		t.Errorf("openrouter = %+v", or)
	}
	if box := byID["box"]; box.Kind != core.ProviderOllama || box.BaseURL != "http://127.0.0.1:11434" || !box.Local || box.TimeoutSeconds != 120 {
		t.Errorf("box = %+v", box)
	}
	if rented := byID["rented"]; rented.Local || rented.BaseURL != "http://ollama:11434" {
		t.Errorf("rented = %+v, want local false and the preset address", rented)
	}
	if pics := byID["pics"]; pics.Kind != core.ProviderComfyUI || !pics.Local || pics.TimeoutSeconds != 300 {
		t.Errorf("pics = %+v", pics)
	}
	if cfg.AI.FetchPublicPrices {
		t.Error("fetchPublicPrices false did not load")
	}
	if got := cfg.AI.Routes[core.RouteDefault]; len(got) != 2 || got[1] != (core.AIRouteTarget{ProviderID: "box", Model: "llama3.2"}) {
		t.Errorf("default route = %+v", got)
	}
	t.Setenv("MARKSPACE_AI_FETCH_PUBLIC_PRICES", "true")
	if cfg, err = Load(path); err != nil || !cfg.AI.FetchPublicPrices {
		t.Fatalf("env override: %v %v", err, cfg.AI.FetchPublicPrices)
	}

	for name, body := range map[string]string{
		"unknown preset": "ai:\n  providers:\n    - id: a\n      preset: skynet\n",
		"unknown task":   "ai:\n  routes:\n    summarise: [{providerId: a, model: m}]\n",
		"empty model":    "ai:\n  routes:\n    edit: [{providerId: a}]\n",
	} {
		if _, err := Load(write(body)); err == nil {
			t.Errorf("%s: Load succeeded, want an error", name)
		} else if name == "unknown preset" && !strings.Contains(err.Error(), "preset") {
			t.Errorf("unknown preset error = %v", err)
		}
	}
}
