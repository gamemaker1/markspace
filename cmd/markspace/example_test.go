package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/gamemaker1/markspace/internal/config"
	"github.com/gamemaker1/markspace/internal/core"
)

// TestExampleConfigLoads checks that the file written by -write-config passes
// strict decoding and yields the providers it documents.
func TestExampleConfigLoads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "markspace.yaml")
	if err := writeExampleConfig(path); err != nil {
		t.Fatalf("write example: %v", err)
	}
	t.Setenv("MARKSPACE_LOCAL_KEY", "local-secret")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("example config does not load: %v", err)
	}
	want := map[string]struct {
		enabled bool
		baseURL string
	}{
		"local":     {true, "http://127.0.0.1:8000/v1"},
		"ollama":    {true, "http://127.0.0.1:11434"},
		"anthropic": {false, "https://api.anthropic.com"},
	}
	if len(cfg.AI.Providers) != len(want) {
		t.Fatalf("got %d providers, want %d", len(cfg.AI.Providers), len(want))
	}
	for _, p := range cfg.AI.Providers {
		w, ok := want[p.ID]
		if !ok {
			t.Errorf("unexpected provider %q", p.ID)
			continue
		}
		if p.Enabled != w.enabled || p.BaseURL != w.baseURL {
			t.Errorf("provider %s: enabled %v baseUrl %q, want %v %q", p.ID, p.Enabled, p.BaseURL, w.enabled, w.baseURL)
		}
	}
	wantRoutes := map[string][]core.AIRouteTarget{
		core.RouteDefault:    {{ProviderID: "local", Model: "qwen3-30b-instruct"}, {ProviderID: "ollama", Model: "llama3.2"}},
		core.RouteEmbeddings: {{ProviderID: "local", Model: "bge-m3"}},
	}
	if len(cfg.AI.Routes) != len(wantRoutes) {
		t.Errorf("routes = %+v", cfg.AI.Routes)
	}
	for task, want := range wantRoutes {
		if !slices.Equal(cfg.AI.Routes[task], want) {
			t.Errorf("route %s = %+v, want %+v", task, cfg.AI.Routes[task], want)
		}
	}
	if !cfg.AI.FetchPublicPrices {
		t.Error("fetchPublicPrices should be on in the example")
	}
	if cfg.Jobs.Workers != config.DefaultJobWorkers || cfg.Plugins.AllowPrivateURLs || cfg.Plugins.MarkspaceURL != "" {
		t.Errorf("jobs.workers %d plugins.allowPrivateURLs %v markspaceUrl %q, want %d, false and empty",
			cfg.Jobs.Workers, cfg.Plugins.AllowPrivateURLs, cfg.Plugins.MarkspaceURL, config.DefaultJobWorkers)
	}
	// The example names every documented key, not only the defaults.
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(exampleConfig), &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ai.fetchPublicPrices", "ai.allowPrivateURLs", "jobs.workers", "plugins.allowPrivateURLs", "plugins.markspaceUrl"} {
		section, name, _ := strings.Cut(key, ".")
		m, _ := raw[section].(map[string]any)
		if _, ok := m[name]; !ok {
			t.Errorf("the example does not set %s", key)
		}
	}
	if cfg.AI.ContextChars != 48000 || cfg.AI.AllowPrivateURLs {
		t.Errorf("contextChars %d allowPrivateURLs %v, want 48000 and false", cfg.AI.ContextChars, cfg.AI.AllowPrivateURLs)
	}
	for _, p := range cfg.AI.Providers {
		if p.Vision != (p.ID == "anthropic") {
			t.Errorf("provider %s: vision %v, want it only on anthropic", p.ID, p.Vision)
		}
	}
	for _, p := range cfg.AI.Providers {
		wantLocal := p.ID != "anthropic"
		if p.Local != wantLocal {
			t.Errorf("provider %s: local %v, want %v", p.ID, p.Local, wantLocal)
		}
	}
	if cfg.AI.Providers[0].APIKey != "local-secret" {
		t.Errorf("apiKey did not expand from the environment")
	}
	local := cfg.AI.Providers[0]
	if local.ImageGeneration || local.ImageEditing || local.ImageModel != "stablediffusion" ||
		!slices.Equal(local.ImageSizes, []string{"1024x1024", "1536x1024", "1024x1536"}) {
		t.Errorf("local image fields = %v %v %q %v", local.ImageGeneration, local.ImageEditing, local.ImageModel, local.ImageSizes)
	}
}

// TestExampleComfyProviderLoads removes the comment marks from the ComfyUI
// example and checks that the result loads.
func TestExampleComfyProviderLoads(t *testing.T) {
	var lines []string
	inBlock := false
	found := false
	for _, line := range strings.Split(exampleConfig, "\n") {
		switch {
		case strings.HasPrefix(line, "    # - id: comfy"):
			inBlock, found = true, true
			line = "    " + strings.TrimPrefix(line, "    # ")
		case inBlock && strings.HasPrefix(line, "    #   "):
			line = "    " + strings.TrimPrefix(line, "    # ")
		default:
			inBlock = false
		}
		lines = append(lines, line)
	}
	if !found {
		t.Fatal("the example has no commented comfy provider")
	}
	path := filepath.Join(t.TempDir(), "markspace.yaml")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("uncommented example does not load: %v", err)
	}
	var comfy *core.AIProviderConfig
	for i := range cfg.AI.Providers {
		if cfg.AI.Providers[i].ID == "comfy" {
			comfy = &cfg.AI.Providers[i]
		}
	}
	if comfy == nil {
		t.Fatal("the uncommented example has no comfy provider")
	}
	if comfy.Kind != core.ProviderComfyUI || !comfy.ImageGeneration || !comfy.ImageEditing ||
		comfy.ImageModel != "sd_xl_base_1.0.safetensors" || len(comfy.ImageSizes) != 3 ||
		comfy.TimeoutSeconds != 300 || !comfy.Enabled || comfy.BaseURL != "http://127.0.0.1:8188" {
		t.Errorf("comfy provider = %+v", comfy)
	}
	if !comfy.Local {
		t.Error("the comfyui preset should mark the provider local")
	}
}
