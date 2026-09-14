package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestProviderEnabledDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "markspace.yaml")
	t.Setenv("TEST_PROVIDER_KEY", "from-env")
	data := `
dataDir: ` + dir + `
ai:
  providers:
    - id: implicit
      baseUrl: http://127.0.0.1:8000/v1
      apiKey: ${TEST_PROVIDER_KEY}
    - id: explicit-off
      kind: ollama
      baseUrl: http://127.0.0.1:11434
      enabled: false
    - id: explicit-on
      kind: anthropic
      baseUrl: https://api.anthropic.com
      enabled: true
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := map[string]bool{"implicit": true, "explicit-off": false, "explicit-on": true}
	for _, p := range cfg.AI.Providers {
		if p.Enabled != want[p.ID] {
			t.Errorf("provider %s enabled = %v, want %v", p.ID, p.Enabled, want[p.ID])
		}
	}
	if got := cfg.AI.Providers[0]; got.APIKey != "from-env" || !got.HasAPIKey {
		t.Errorf("env expansion: key %q hasKey %v", got.APIKey, got.HasAPIKey)
	}
	if !cfg.AI.FetchPublicPrices {
		t.Error("fetchPublicPrices should default to true")
	}
	if cfg.DBPath != filepath.Join(dir, "markspace.db") {
		t.Errorf("db path = %q", cfg.DBPath)
	}
}

func TestLoadRejectsUnknownKeysAndDuplicates(t *testing.T) {
	cases := map[string]string{
		"unknown key":        "listen: \":1\"\nlistne: \":2\"\n",
		"duplicate provider": "ai:\n  providers:\n    - id: a\n      baseUrl: http://x\n    - id: a\n      baseUrl: http://y\n",
		"bad kind":           "ai:\n  providers:\n    - id: a\n      kind: gpt\n      baseUrl: http://x\n",
		"missing base url":   "ai:\n  providers:\n    - id: a\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "c.yaml")
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("Load succeeded, want an error")
			}
		})
	}
}

func TestMissingFileUsesDefaults(t *testing.T) {
	t.Setenv("MARKSPACE_DATA_DIR", t.TempDir())
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != ":8080" || !cfg.AllowSignup {
		t.Errorf("defaults not applied: %+v", cfg)
	}
}

func TestAIContextSettings(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(filepath.Join(dir, "absent.yaml"))
	if err != nil {
		t.Fatalf("Load defaults: %v", err)
	}
	if cfg.AI.ContextChars != 48000 || cfg.AI.AllowPrivateURLs {
		t.Fatalf("defaults: contextChars %d allowPrivateURLs %v", cfg.AI.ContextChars, cfg.AI.AllowPrivateURLs)
	}

	write := func(name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	set := write("set.yaml", "dataDir: "+dir+"\nai:\n  contextChars: 1200\n  allowPrivateURLs: true\n")
	cfg, err = Load(set)
	if err != nil {
		t.Fatalf("Load file: %v", err)
	}
	if cfg.AI.ContextChars != 1200 || !cfg.AI.AllowPrivateURLs {
		t.Fatalf("file: contextChars %d allowPrivateURLs %v", cfg.AI.ContextChars, cfg.AI.AllowPrivateURLs)
	}

	zero := write("zero.yaml", "dataDir: "+dir+"\nai:\n  contextChars: 0\n")
	if cfg, err = Load(zero); err != nil || cfg.AI.ContextChars != 48000 {
		t.Fatalf("zero contextChars: %v, got %d, want the default", err, cfg.AI.ContextChars)
	}

	t.Setenv("MARKSPACE_AI_CONTEXT_CHARS", "900")
	t.Setenv("MARKSPACE_AI_ALLOW_PRIVATE_URLS", "false")
	cfg, err = Load(set)
	if err != nil {
		t.Fatalf("Load with env: %v", err)
	}
	if cfg.AI.ContextChars != 900 || cfg.AI.AllowPrivateURLs {
		t.Fatalf("env: contextChars %d allowPrivateURLs %v", cfg.AI.ContextChars, cfg.AI.AllowPrivateURLs)
	}

	t.Setenv("MARKSPACE_AI_CONTEXT_CHARS", "lots")
	t.Setenv("MARKSPACE_AI_ALLOW_PRIVATE_URLS", "maybe")
	cfg, err = Load(set)
	if err != nil {
		t.Fatalf("Load with bad env: %v", err)
	}
	if cfg.AI.ContextChars != 1200 || !cfg.AI.AllowPrivateURLs {
		t.Fatalf("bad env should keep the file values, got %d %v", cfg.AI.ContextChars, cfg.AI.AllowPrivateURLs)
	}
}

func TestImageProviders(t *testing.T) {
	dir := t.TempDir()
	body := `
dataDir: ` + dir + `
ai:
  providers:
    - id: comfy
      kind: comfyui
      baseUrl: http://127.0.0.1:8188
      imageGeneration: true
      imageEditing: true
      imageModel: sdxl.safetensors
      imageSizes: ["1024x1024", "896x1152"]
      comfyGenerate: '{"9": {"class_type": "SaveImage", "inputs": {}}}'
    - id: images
      baseUrl: http://127.0.0.1:8000/v1
      imageGeneration: true
      imageSizes: ["auto", "1536x1024"]
    - id: chat
      baseUrl: http://127.0.0.1:8001/v1
    - id: slow
      baseUrl: http://127.0.0.1:8002/v1
      imageEditing: true
      timeoutSeconds: 45
`
	path := filepath.Join(dir, "images.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	timeouts := map[string]int{"comfy": 300, "images": 300, "chat": 120, "slow": 45}
	for _, p := range cfg.AI.Providers {
		if p.TimeoutSeconds != timeouts[p.ID] {
			t.Errorf("provider %s timeout = %d, want %d", p.ID, p.TimeoutSeconds, timeouts[p.ID])
		}
	}
	comfy := cfg.AI.Providers[0]
	if comfy.Kind != core.ProviderComfyUI || !comfy.ImageGeneration || !comfy.ImageEditing ||
		comfy.ImageModel != "sdxl.safetensors" || len(comfy.ImageSizes) != 2 || comfy.ComfyGenerate == "" {
		t.Errorf("comfy = %+v", comfy)
	}
}

func TestImageProviderValidation(t *testing.T) {
	provider := func(lines string) string {
		return "ai:\n  providers:\n    - id: a\n      baseUrl: http://x\n" + lines
	}
	cases := map[string]string{
		"image flag on ollama":            provider("      kind: ollama\n      imageGeneration: true\n"),
		"edit flag on anthropic":          provider("      kind: anthropic\n      imageEditing: true\n"),
		"bad size":                        provider("      imageSizes: [\"1024\"]\n"),
		"size over 4096":                  provider("      imageSizes: [\"8192x8192\"]\n"),
		"auto on comfyui":                 provider("      kind: comfyui\n      imageSizes: [auto]\n"),
		"template on openai":              provider("      comfyGenerate: '{}'\n"),
		"template not JSON":               provider("      kind: comfyui\n      comfyEdit: 'not json'\n"),
		"template not an object":          provider("      kind: comfyui\n      comfyGenerate: '[1, 2]'\n"),
		"comfyui with embeddings":         provider("      kind: comfyui\n      embedModel: e\n"),
		"comfyui in the default route":    "ai:\n  routes:\n    default: [{providerId: a, model: m}]\n  providers:\n    - id: a\n      kind: comfyui\n      baseUrl: http://x\n",
		"comfyui in the embeddings route": "ai:\n  routes:\n    embeddings: [{providerId: a, model: m}]\n  providers:\n    - id: a\n      kind: comfyui\n      baseUrl: http://x\n",
		"removed isDefault key":           provider("      isDefault: true\n"),
		"removed prices key":              provider("      prices: {}\n"),
		"removed default key":             "ai:\n  default: a\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "c.yaml")
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("Load succeeded, want an error")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "only-comfy.yaml")
	if err := os.WriteFile(path, []byte("ai:\n  providers:\n    - id: a\n      kind: comfyui\n      baseUrl: http://x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("a lone comfyui provider: %v", err)
	}
}

// TestPluginsURLFromEnv checks that MARKSPACE_PLUGINS_URL sets the address
// plugins use, so a quadlet can set it without a configuration file.
func TestPluginsURLFromEnv(t *testing.T) {
	t.Setenv("MARKSPACE_DATA_DIR", t.TempDir())
	t.Setenv("MARKSPACE_PLUGINS_URL", "http://markspace:8080/")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Plugins.MarkspaceURL != "http://markspace:8080" {
		t.Fatalf("Plugins.MarkspaceURL = %q, want http://markspace:8080", cfg.Plugins.MarkspaceURL)
	}
}
