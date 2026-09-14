package index

import (
	"context"
	"slices"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestAIProviderImageFields(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	comfy := &core.AIProviderConfig{
		Name: "Comfy", Kind: core.ProviderComfyUI, BaseURL: "http://127.0.0.1:8188", Enabled: true,
		ImageGeneration: true, ImageEditing: true, ImageModel: "sdxl.safetensors",
		ImageSizes: []string{"1024x1024", "896x1152"}, ComfyGenerate: `{"1":{}}`, ComfyEdit: `{"2":{}}`,
	}
	plain := &core.AIProviderConfig{Name: "Plain", Kind: core.ProviderOpenAI, BaseURL: "http://x"}
	for _, p := range []*core.AIProviderConfig{comfy, plain} {
		if err := db.CreateAIProvider(ctx, p); err != nil {
			t.Fatalf("CreateAIProvider: %v", err)
		}
	}

	same := func(got *core.AIProviderConfig, want *core.AIProviderConfig) bool {
		return got.ImageGeneration == want.ImageGeneration && got.ImageEditing == want.ImageEditing &&
			got.ImageModel == want.ImageModel && slices.Equal(got.ImageSizes, want.ImageSizes) &&
			got.ComfyGenerate == want.ComfyGenerate && got.ComfyEdit == want.ComfyEdit
	}
	got, err := db.GetAIProvider(ctx, comfy.ID)
	if err != nil || !same(got, comfy) || got.Kind != core.ProviderComfyUI {
		t.Fatalf("GetAIProvider = %+v, %v", got, err)
	}
	list, err := db.ListAIProviders(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListAIProviders = %d rows, %v", len(list), err)
	}
	for _, p := range list {
		want := plain
		if p.ID == comfy.ID {
			want = comfy
		}
		if !same(p, want) {
			t.Fatalf("listed %s = %+v", p.ID, p)
		}
	}

	got.ImageEditing = false
	got.ImageSizes = nil
	got.ComfyEdit = ""
	if err := db.UpdateAIProvider(ctx, got); err != nil {
		t.Fatalf("UpdateAIProvider: %v", err)
	}
	again, err := db.GetAIProvider(ctx, comfy.ID)
	if err != nil || !same(again, got) || again.ImageSizes != nil {
		t.Fatalf("after update = %+v, %v", again, err)
	}
}
