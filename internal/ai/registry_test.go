package ai

import (
	"errors"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// validCfg returns a configuration that passes validation.
func validCfg() *core.AIProviderConfig {
	return &core.AIProviderConfig{
		ID:             "p1",
		Kind:           core.ProviderOpenAI,
		BaseURL:        "http://127.0.0.1:8080/v1",
		Model:          "test-model",
		TimeoutSeconds: 5,
	}
}

func TestRegistryNewValidates(t *testing.T) {
	tests := []struct {
		name  string
		cfg   *core.AIProviderConfig
		field string
	}{
		{"nil config", nil, "provider"},
		{"missing base url", &core.AIProviderConfig{ID: "a", Kind: core.ProviderOpenAI}, "baseUrl"},
		{"blank base url", &core.AIProviderConfig{ID: "a", Kind: core.ProviderOpenAI, BaseURL: "   "}, "baseUrl"},
		{"relative base url", &core.AIProviderConfig{ID: "a", Kind: core.ProviderOpenAI, BaseURL: "/v1"}, "baseUrl"},
		{"missing host", &core.AIProviderConfig{ID: "a", Kind: core.ProviderOpenAI, BaseURL: "http://"}, "baseUrl"},
		{"wrong scheme", &core.AIProviderConfig{ID: "a", Kind: core.ProviderOpenAI, BaseURL: "ftp://host/v1"}, "baseUrl"},
		{"bad url", &core.AIProviderConfig{ID: "a", Kind: core.ProviderOpenAI, BaseURL: "http://host:port/v1"}, "baseUrl"},
		{"empty kind", &core.AIProviderConfig{ID: "a", BaseURL: "http://host"}, "kind"},
		{"unknown kind", &core.AIProviderConfig{ID: "a", Kind: "gemini", BaseURL: "http://host"}, "kind"},
	}
	registry := testRegistry()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := registry.New(tc.cfg)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !errors.Is(err, core.ErrInvalid) {
				t.Fatalf("error %v is not core.ErrInvalid", err)
			}
			var verr *core.ValidationError
			if !errors.As(err, &verr) || verr.Field != tc.field {
				t.Fatalf("error %v does not name field %q", err, tc.field)
			}
		})
	}
}

func TestRegistryNewBuildsEveryKind(t *testing.T) {
	registry := testRegistry()
	for _, kind := range core.AIProviderKinds {
		cfg := validCfg()
		cfg.ID = string(kind)
		cfg.Kind = kind
		provider, err := registry.New(cfg)
		if err != nil {
			t.Fatalf("New(%s): %v", kind, err)
		}
		if provider.Kind() != kind {
			t.Fatalf("kind = %s, want %s", provider.Kind(), kind)
		}
		if provider.ID() != string(kind) {
			t.Fatalf("id = %s", provider.ID())
		}
	}
	if registry.providerCount() != len(core.AIProviderKinds) {
		t.Fatalf("cached = %d", registry.providerCount())
	}
}

func TestRegistryCachesAndReplaces(t *testing.T) {
	registry := testRegistry()
	first, err := registry.New(validCfg())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	same, err := registry.New(validCfg())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if first != same {
		t.Fatal("a repeated configuration built a second provider")
	}

	changed := validCfg()
	changed.Model = "other-model"
	replaced, err := registry.New(changed)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if replaced == first {
		t.Fatal("a changed model reused the cached provider")
	}
	if registry.providerCount() != 1 {
		t.Fatalf("cached = %d, want 1", registry.providerCount())
	}

	for _, mutate := range []func(*core.AIProviderConfig){
		func(c *core.AIProviderConfig) { c.BaseURL = "http://127.0.0.1:9090/v1" },
		func(c *core.AIProviderConfig) { c.APIKey = "new-key" },
		func(c *core.AIProviderConfig) { c.TimeoutSeconds = 30 },
		func(c *core.AIProviderConfig) { c.Headers = map[string]string{"X-A": "1"} },
		func(c *core.AIProviderConfig) { c.Kind = core.ProviderOllama },
	} {
		base := validCfg()
		mutate(base)
		if configHash(base) == configHash(validCfg()) {
			t.Fatal("a material change did not change the config hash")
		}
	}
}

func TestRegistryCopiesConfig(t *testing.T) {
	registry := testRegistry()
	cfg := validCfg()
	cfg.Headers = map[string]string{"X-A": "1"}
	provider, err := registry.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cfg.Model = "mutated"
	cfg.Headers["X-A"] = "2"
	if provider.Config().Model != "test-model" {
		t.Fatalf("model = %q", provider.Config().Model)
	}
	if provider.Config().Headers["X-A"] != "1" {
		t.Fatalf("headers = %v", provider.Config().Headers)
	}
}

func TestRegistryNewConcurrent(t *testing.T) {
	registry := testRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := registry.New(validCfg()); err != nil {
				t.Errorf("New: %v", err)
			}
		}()
	}
	wg.Wait()
	if registry.providerCount() != 1 {
		t.Fatalf("cached = %d, want 1", registry.providerCount())
	}
}

func TestRegistryRemoveEvicts(t *testing.T) {
	registry := testRegistry()
	first, err := registry.New(validCfg())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	other := validCfg()
	other.ID = "p2"
	if _, err := registry.New(other); err != nil {
		t.Fatalf("New: %v", err)
	}

	var evictor ports.AIEvictor = registry
	evictor.Remove("p1")
	evictor.Remove("never-cached")
	if registry.providerCount() != 1 {
		t.Fatalf("cached = %d, want 1", registry.providerCount())
	}

	rebuilt, err := registry.New(validCfg())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if rebuilt == first {
		t.Fatal("New after Remove returned the evicted provider")
	}
	again, err := registry.New(validCfg())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if again != rebuilt {
		t.Fatal("the rebuilt provider was not cached")
	}
}

func TestRegistryMaxContextRunes(t *testing.T) {
	if got := testRegistry().MaxContextRunes(); got != 48000 {
		t.Fatalf("MaxContextRunes = %d", got)
	}
}

func TestSimilarity(t *testing.T) {
	tests := []struct {
		name string
		a    []float32
		b    []float32
		want float64
	}{
		{"identical", []float32{1, 2, 3}, []float32{1, 2, 3}, 1},
		{"scaled", []float32{1, 0}, []float32{4, 0}, 1},
		{"orthogonal", []float32{1, 0}, []float32{0, 1}, 0},
		{"opposite", []float32{1, 0}, []float32{-1, 0}, -1},
		{"partial", []float32{1, 1}, []float32{1, 0}, math.Sqrt2 / 2},
		{"length mismatch", []float32{1, 0, 0}, []float32{1, 0}, 0},
		{"both empty", nil, nil, 0},
		{"zero norm on the left", []float32{0, 0}, []float32{1, 1}, 0},
		{"zero norm on the right", []float32{1, 1}, []float32{0, 0}, 0},
	}
	registry := testRegistry()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := registry.Similarity(tc.a, tc.b)
			if math.Abs(got-tc.want) > 1e-6 {
				t.Fatalf("Similarity = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRegistryImageGenerator(t *testing.T) {
	registry := testRegistry()
	build := func(kind core.AIProviderKind, generate, edit bool) *core.AIProviderConfig {
		cfg := validCfg()
		cfg.ID = string(kind)
		cfg.Kind = kind
		cfg.ImageGeneration, cfg.ImageEditing = generate, edit
		return cfg
	}
	for _, kind := range []core.AIProviderKind{core.ProviderOpenAI, core.ProviderComfyUI} {
		if _, err := registry.ImageGenerator(build(kind, true, true), false); err != nil {
			t.Fatalf("%s generate: %v", kind, err)
		}
		if _, err := registry.ImageGenerator(build(kind, true, true), true); err != nil {
			t.Fatalf("%s edit: %v", kind, err)
		}
		if _, err := registry.ImageGenerator(build(kind, false, true), false); !errors.Is(err, core.ErrUnsupported) {
			t.Fatalf("%s without imageGeneration: %v", kind, err)
		}
		if _, err := registry.ImageGenerator(build(kind, true, false), true); !errors.Is(err, core.ErrUnsupported) {
			t.Fatalf("%s without imageEditing: %v", kind, err)
		}
	}
	for _, kind := range []core.AIProviderKind{core.ProviderOllama, core.ProviderAnthropic} {
		_, err := registry.ImageGenerator(build(kind, true, true), false)
		if !errors.Is(err, core.ErrUnsupported) || !strings.Contains(err.Error(), "protocol cannot generate") {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	if _, err := registry.ImageGenerator(&core.AIProviderConfig{ID: "x", Kind: core.ProviderOpenAI}, false); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("invalid config: %v", err)
	}
}

func TestConfigHashCoversImageFields(t *testing.T) {
	for _, mutate := range []func(*core.AIProviderConfig){
		func(c *core.AIProviderConfig) { c.ImageGeneration = true },
		func(c *core.AIProviderConfig) { c.ImageEditing = true },
		func(c *core.AIProviderConfig) { c.ImageModel = "m" },
		func(c *core.AIProviderConfig) { c.ImageSizes = []string{"512x512"} },
		func(c *core.AIProviderConfig) { c.ComfyGenerate = "{}" },
		func(c *core.AIProviderConfig) { c.ComfyEdit = "{}" },
	} {
		cfg := validCfg()
		mutate(cfg)
		if configHash(cfg) == configHash(validCfg()) {
			t.Fatal("an image field change did not change the config hash")
		}
	}
	cfg := validCfg()
	cfg.ImageSizes = []string{"512x512"}
	provider, err := testRegistry().New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ImageSizes[0] = "changed"
	if provider.Config().ImageSizes[0] != "512x512" {
		t.Fatal("the provider shares the caller's size list")
	}
}
