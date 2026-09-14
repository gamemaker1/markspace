package index

import (
	"encoding/json"

	"github.com/gamemaker1/markspace/internal/core"
)

// The image settings of a provider live as one JSON object in the
// ai_providers.image_config column.

// providerImageConfig is the JSON stored in ai_providers.image_config.
type providerImageConfig struct {
	ImageGeneration bool     `json:"imageGeneration,omitempty"`
	ImageEditing    bool     `json:"imageEditing,omitempty"`
	ImageModel      string   `json:"imageModel,omitempty"`
	ImageSizes      []string `json:"imageSizes,omitempty"`
	ComfyGenerate   string   `json:"comfyGenerate,omitempty"`
	ComfyEdit       string   `json:"comfyEdit,omitempty"`
}

// marshalImageConfig renders the image fields of a provider as a JSON
// object. A provider without image settings becomes "{}".
func marshalImageConfig(p *core.AIProviderConfig) string {
	b, err := json.Marshal(providerImageConfig{
		ImageGeneration: p.ImageGeneration,
		ImageEditing:    p.ImageEditing,
		ImageModel:      p.ImageModel,
		ImageSizes:      p.ImageSizes,
		ComfyGenerate:   p.ComfyGenerate,
		ComfyEdit:       p.ComfyEdit,
	})
	if err != nil {
		return "{}"
	}
	return string(b)
}

// applyImageConfig sets the image fields of a provider from stored JSON.
// Unreadable JSON leaves the fields empty, so one bad row cannot break a
// listing.
func applyImageConfig(p *core.AIProviderConfig, s string) {
	var c providerImageConfig
	if err := json.Unmarshal([]byte(s), &c); err != nil {
		return
	}
	p.ImageGeneration = c.ImageGeneration
	p.ImageEditing = c.ImageEditing
	p.ImageModel = c.ImageModel
	p.ImageSizes = c.ImageSizes
	p.ComfyGenerate = c.ComfyGenerate
	p.ComfyEdit = c.ComfyEdit
}
