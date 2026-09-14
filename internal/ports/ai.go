package ports

import (
	"context"

	"github.com/gamemaker1/markspace/internal/core"
)

// PromptInput is the resolved context of one prompt.
type PromptInput struct {
	// Sources lists the context references in request order. A source with
	// Skipped set is listed for the client but adds nothing to the prompt.
	// The prompt numbers sources by their position, so [n] cites Sources[n-1].
	Sources []core.AISource
	// Images go on the final user message.
	Images []core.AIImage
	// Budget caps the runes of task text plus source text. A value of 0 or
	// less selects MaxContextRunes.
	Budget int
}

// AIFactory builds providers from configuration and assembles prompts.
type AIFactory interface {
	// New returns a provider for cfg. Implementations may cache by config id
	// and are safe for concurrent use.
	New(cfg *core.AIProviderConfig) (core.AIProvider, error)
	// ImageGenerator returns the image generator of the provider built for
	// cfg. edit selects editing instead of generation. The error wraps
	// core.ErrUnsupported when the provider cannot do the call.
	ImageGenerator(cfg *core.AIProviderConfig, edit bool) (core.ImageGenerator, error)
	// ListCatalog lists the models the provider offers, with the context
	// length, modalities and price its listing reports.
	ListCatalog(ctx context.Context, cfg *core.AIProviderConfig) ([]core.AIModel, error)
	// BuildPrompt turns a task request plus resolved context and images into
	// a chat transcript.
	BuildPrompt(req core.AIRequest, in PromptInput) []core.AIMessage
	// Similarity returns the cosine similarity of two vectors, or 0 when the
	// lengths differ.
	Similarity(a, b []float32) float64
	// MaxContextRunes is the default context budget in runes.
	MaxContextRunes() int
}

// AIEvictor is an optional interface an AIFactory may implement so the
// application can drop a cached provider when its configuration is deleted.
// Callers type-assert for it.
type AIEvictor interface {
	// Remove drops the cached provider with this configuration id.
	Remove(id string)
}

// CatalogFetcher downloads a public price catalog. The application calls it
// once a day at most per catalog.
type CatalogFetcher interface {
	// Fetch returns the body at url.
	Fetch(ctx context.Context, url string) ([]byte, error)
}
