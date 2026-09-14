package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// fixture reads one file from testdata.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// modelByID indexes parsed models by id.
func modelByID(models []core.AIModel) map[string]core.AIModel {
	out := map[string]core.AIModel{}
	for _, m := range models {
		out[m.ModelID] = m
	}
	return out
}

func TestParseOpenAIModelsPlain(t *testing.T) {
	models, err := parseOpenAIModels(fixture(t, "openai-models.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 3 {
		t.Fatalf("models = %+v", models)
	}
	for _, m := range models {
		if m.CapsListed || m.Price != nil || m.PriceSource != "" || m.ContextLength != 0 ||
			!reflect.DeepEqual(m.Inputs, []string{"text"}) || !reflect.DeepEqual(m.Outputs, []string{"text"}) {
			t.Fatalf("plain listing model = %+v", m)
		}
	}
	if _, err := parseOpenAIModels([]byte(`{"data":`)); err == nil {
		t.Fatal("truncated JSON parsed")
	}
	if models, err := parseOpenAIModels([]byte(`{"object":"list"}`)); err != nil || len(models) != 0 {
		t.Fatalf("listing without data = %v, %v", models, err)
	}
}

func TestParseOpenAIModelsOpenRouterStyle(t *testing.T) {
	models, err := parseOpenAIModels(fixture(t, "openrouter-models.json"))
	if err != nil {
		t.Fatal(err)
	}
	byID := modelByID(models)
	gpt := byID["openai/gpt-4o"]
	wantPrice := &core.AIPrice{InputPerMTok: 2.5, OutputPerMTok: 10, CacheReadPerMTok: 1.25, PerImageIn: 0.003613}
	if !reflect.DeepEqual(gpt.Price, wantPrice) || gpt.PriceSource != core.PriceSourceProvider {
		t.Fatalf("gpt-4o price = %+v %s", gpt.Price, gpt.PriceSource)
	}
	if gpt.Price.InputPerMTok != 2.5 {
		t.Fatalf("0.0000025 per token should be exactly 2.5 per million, got %v", gpt.Price.InputPerMTok)
	}
	if !gpt.CapsListed || !reflect.DeepEqual(gpt.Inputs, []string{"text", "image", "file"}) ||
		!reflect.DeepEqual(gpt.Outputs, []string{"text"}) || !gpt.Tools || gpt.ContextLength != 128000 ||
		gpt.MaxOutput != 16384 || gpt.Name != "OpenAI: GPT-4o" {
		t.Fatalf("gpt-4o = %+v", gpt)
	}
	if auto := byID["openrouter/auto"]; auto.Price != nil || auto.PriceSource != "" {
		t.Fatalf("variable pricing -1 should be unknown: %+v", auto)
	}
	if code := byID["mistralai/codestral-2501"]; code.Price != nil || !code.CapsListed || code.ContextLength != 262144 {
		t.Fatalf("missing pricing = %+v", code)
	}
	if bare := byID["some/bare-model"]; bare.CapsListed || bare.Price != nil || !reflect.DeepEqual(bare.Outputs, []string{"text"}) {
		t.Fatalf("missing architecture = %+v", bare)
	}
	embed := byID["openai/text-embedding-3-small"]
	if !reflect.DeepEqual(embed.Outputs, []string{"embedding"}) || embed.Price == nil ||
		embed.Price.InputPerMTok != 0.02 || embed.Price.OutputPerMTok != 0 {
		t.Fatalf("embedding model = %+v price %+v", embed, embed.Price)
	}
}

func TestParseOllamaListing(t *testing.T) {
	names, err := parseOllamaTags(fixture(t, "ollama-tags.json"))
	if err != nil || !reflect.DeepEqual(names, []string{"llama3.2:latest", "llava:7b", "nomic-embed-text:latest"}) {
		t.Fatalf("tags = %v, %v", names, err)
	}
	llama, err := parseOllamaShow("llama3.2:latest", fixture(t, "ollama-show-llama3.2.json"))
	if err != nil || llama.ContextLength != 131072 || !llama.Tools || !llama.CapsListed ||
		!reflect.DeepEqual(llama.Inputs, []string{"text"}) || !reflect.DeepEqual(llama.Outputs, []string{"text"}) {
		t.Fatalf("llama = %+v, %v", llama, err)
	}
	llava, _ := parseOllamaShow("llava:7b", fixture(t, "ollama-show-llava.json"))
	if llava.ContextLength != 32768 || !llava.HasInput(core.ModalityImage) || llava.Tools {
		t.Fatalf("llava = %+v", llava)
	}
	nomic, _ := parseOllamaShow("nomic-embed-text:latest", fixture(t, "ollama-show-nomic.json"))
	if !reflect.DeepEqual(nomic.Outputs, []string{"embedding"}) || nomic.HasOutput(core.ModalityText) || nomic.ContextLength != 2048 {
		t.Fatalf("nomic = %+v", nomic)
	}
	old, err := parseOllamaShow("old", []byte(`{"details":{},"model_info":{"llama.context_length":"8192"}}`))
	if err != nil || old.CapsListed || old.ContextLength != 8192 || !reflect.DeepEqual(old.Outputs, []string{"text"}) {
		t.Fatalf("show without capabilities = %+v, %v", old, err)
	}
	if _, err := parseOllamaTags([]byte(`[`)); err == nil {
		t.Fatal("bad tags parsed")
	}
}

func TestParseAnthropicModels(t *testing.T) {
	models, err := parseAnthropicModels(fixture(t, "anthropic-models.json"))
	if err != nil || len(models) != 3 {
		t.Fatalf("models = %+v, %v", models, err)
	}
	for _, m := range models {
		if !m.CapsListed || !m.HasInput(core.ModalityImage) || !reflect.DeepEqual(m.Outputs, []string{"text"}) || m.Name == "" {
			t.Fatalf("claude model = %+v", m)
		}
	}
}

func TestParseComfyObjectInfo(t *testing.T) {
	for _, tc := range []struct {
		file    string
		editing bool
		want    []string
	}{
		{"comfy-object-info.json", false, []string{"juggernautXL_v9.safetensors", "sd_xl_base_1.0.safetensors"}},
		{"comfy-object-info-combo.json", true, []string{"flux1-dev-fp8.safetensors", "sd_xl_base_1.0.safetensors"}},
	} {
		models, err := parseComfyObjectInfo(fixture(t, tc.file), tc.editing)
		if err != nil {
			t.Fatalf("%s: %v", tc.file, err)
		}
		var ids []string
		for _, m := range models {
			ids = append(ids, m.ModelID)
			if !reflect.DeepEqual(m.Outputs, []string{"image"}) || m.HasInput(core.ModalityImage) != tc.editing || !m.CapsListed {
				t.Fatalf("%s: checkpoint = %+v", tc.file, m)
			}
		}
		if !reflect.DeepEqual(ids, tc.want) {
			t.Fatalf("%s: ids = %v", tc.file, ids)
		}
	}
	if _, err := parseComfyObjectInfo([]byte(`{"CheckpointLoaderSimple":{}}`), false); err == nil {
		t.Fatal("object info without ckpt_name parsed")
	}
}

// catalogServer serves fixtures by path.
func catalogServer(t *testing.T, routes map[string]string, calls *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			var in struct{ Model string }
			_ = json.Unmarshal(body, &in)
			key += " " + in.Model
		}
		if calls != nil {
			*calls = append(*calls, key)
		}
		name, ok := routes[key]
		if !ok {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, name))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRegistryListCatalogPerKind(t *testing.T) {
	ctx := context.Background()
	r := testRegistry()

	or := catalogServer(t, map[string]string{"GET /api/v1/models": "openrouter-models.json"}, nil)
	models, err := r.ListCatalog(ctx, &core.AIProviderConfig{ID: "or", Kind: core.ProviderOpenAI, BaseURL: or.URL + "/api/v1", Preset: "openrouter"})
	if err != nil {
		t.Fatal(err)
	}
	gpt := modelByID(models)["openai/gpt-4o"]
	if gpt.ProviderID != "or" || !gpt.Enabled || !gpt.Available || gpt.Vendor != "openai" || gpt.PriceSource != core.PriceSourceProvider {
		t.Fatalf("openrouter model = %+v", gpt)
	}
	if models[0].ModelID > models[len(models)-1].ModelID {
		t.Fatal("models are not sorted")
	}

	oa := catalogServer(t, map[string]string{"GET /v1/models": "openai-models.json"}, nil)
	models, err = r.ListCatalog(ctx, &core.AIProviderConfig{ID: "oa", Kind: core.ProviderOpenAI, BaseURL: oa.URL + "/v1", Preset: "openai"})
	if err != nil || len(models) != 3 || models[0].Vendor != "openai" || models[0].Name != models[0].ModelID {
		t.Fatalf("openai models = %+v, %v", models, err)
	}

	var calls []string
	ol := catalogServer(t, map[string]string{
		"GET /api/tags":                          "ollama-tags.json",
		"POST /api/show llama3.2:latest":         "ollama-show-llama3.2.json",
		"POST /api/show nomic-embed-text:latest": "ollama-show-nomic.json",
	}, &calls)
	models, err = r.ListCatalog(ctx, &core.AIProviderConfig{ID: "ol", Kind: core.ProviderOllama, BaseURL: ol.URL, Local: true})
	if err != nil || len(models) != 3 {
		t.Fatalf("ollama models = %+v, %v", models, err)
	}
	byID := modelByID(models)
	nomic := byID["nomic-embed-text:latest"]
	if byID["llama3.2:latest"].ContextLength != 131072 || !nomic.HasOutput(core.ModalityEmbedding) {
		t.Fatalf("ollama shows were not applied: %+v", models)
	}
	if llava := byID["llava:7b"]; llava.CapsListed || !reflect.DeepEqual(llava.Outputs, []string{"text"}) {
		t.Fatalf("a failed show should keep the model as text only: %+v", llava)
	}
	if len(calls) != 4 {
		t.Fatalf("ollama calls = %v", calls)
	}

	an := catalogServer(t, map[string]string{"GET /v1/models": "anthropic-models.json"}, &calls)
	models, err = r.ListCatalog(ctx, &core.AIProviderConfig{ID: "an", Kind: core.ProviderAnthropic, BaseURL: an.URL, APIKey: "k", Preset: "anthropic"})
	if err != nil || len(models) != 3 || models[0].Vendor != "anthropic" {
		t.Fatalf("anthropic models = %+v, %v", models, err)
	}

	cf := catalogServer(t, map[string]string{"GET /object_info/CheckpointLoaderSimple": "comfy-object-info-combo.json"}, nil)
	models, err = r.ListCatalog(ctx, &core.AIProviderConfig{ID: "cf", Kind: core.ProviderComfyUI, BaseURL: cf.URL, ImageGeneration: true, ImageEditing: true})
	if err != nil || len(models) != 2 || !models[0].HasInput(core.ModalityImage) {
		t.Fatalf("comfy models = %+v, %v", models, err)
	}

	missing := catalogServer(t, map[string]string{}, nil)
	var apiErr *APIError
	if _, err := r.ListCatalog(ctx, &core.AIProviderConfig{ID: "an2", Kind: core.ProviderAnthropic, BaseURL: missing.URL, Model: "claude"}); !errors.As(err, &apiErr) || apiErr.Status != 404 {
		t.Fatalf("failed anthropic listing = %v", err)
	}
}

func TestRegistryListCatalogBodyCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[`))
		chunk := []byte(strings.Repeat(" ", 1<<20))
		for i := 0; i < 33; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	_, err := testRegistry().ListCatalog(context.Background(), &core.AIProviderConfig{ID: "big", Kind: core.ProviderOpenAI, BaseURL: srv.URL})
	if !errors.Is(err, core.ErrTooLarge) {
		t.Fatalf("oversized listing = %v", err)
	}
}
