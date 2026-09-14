package ai

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// MaxContextRunes is the default context budget of one prompt in runes. It
// matches the ai.contextChars default, and a PromptInput budget overrides it.
const MaxContextRunes = 48000

// Registry builds providers from configuration, caches them and assembles
// prompts. It implements ports.AIFactory and is safe for concurrent use.
type Registry struct {
	logger        *slog.Logger
	contextChunks int
	client        *http.Client

	mu    sync.Mutex
	cache map[string]cachedProvider
}

// cachedProvider is one built provider plus the hash of the configuration it
// was built from.
type cachedProvider struct {
	hash     string
	provider core.AIProvider
}

// NewRegistry returns a registry. contextChunks caps how many retrieved note
// chunks BuildMessages puts into one prompt, and a value of 0 or less means
// no cap.
func NewRegistry(logger *slog.Logger, contextChunks int) *Registry {
	if logger == nil {
		logger = slog.Default()
	}
	return &Registry{
		logger:        logger,
		contextChunks: contextChunks,
		client:        newHTTPClient(),
		cache:         make(map[string]cachedProvider),
	}
}

// compile time checks that a registry satisfies the ports.
var (
	_ ports.AIFactory = (*Registry)(nil)
	_ ports.AIEvictor = (*Registry)(nil)
)

// Remove drops the cached provider with this configuration id, so the next
// New call for that id builds a fresh provider. An unknown id is a no-op.
// Streams already running on the removed provider finish normally.
func (r *Registry) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cache, id)
}

// New returns a provider for cfg. Providers are cached by configuration id,
// and a configuration change replaces the cached provider.
func (r *Registry) New(cfg *core.AIProviderConfig) (core.AIProvider, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	hash := configHash(cfg)

	r.mu.Lock()
	defer r.mu.Unlock()

	if cached, ok := r.cache[cfg.ID]; ok && cached.hash == hash {
		return cached.provider, nil
	}

	copied := copyConfig(cfg)
	var provider core.AIProvider
	switch copied.Kind {
	case core.ProviderOpenAI:
		provider = newOpenAIProvider(copied, r.client, r.logger)
	case core.ProviderOllama:
		provider = newOllamaProvider(copied, r.client, r.logger)
	case core.ProviderAnthropic:
		provider = newAnthropicProvider(copied, r.client, r.logger)
	case core.ProviderComfyUI:
		provider = newComfyProvider(copied, r.client, r.logger)
	default:
		return nil, core.Invalid("kind", "unknown provider kind "+string(copied.Kind))
	}
	r.cache[cfg.ID] = cachedProvider{hash: hash, provider: provider}
	return provider, nil
}

// ImageGenerator returns the image generator of the provider built for cfg.
// edit selects the editing capability instead of generation. The error wraps
// core.ErrUnsupported when the protocol has no image support or the provider
// does not have imageGeneration or imageEditing set.
func (r *Registry) ImageGenerator(cfg *core.AIProviderConfig, edit bool) (core.ImageGenerator, error) {
	provider, err := r.New(cfg)
	if err != nil {
		return nil, err
	}
	gen, ok := provider.(core.ImageGenerator)
	if !ok {
		return nil, fmt.Errorf("ai provider %s: the %s protocol cannot generate or edit images: %w",
			cfg.ID, cfg.Kind, core.ErrUnsupported)
	}
	if err := checkImageCapability(cfg, edit); err != nil {
		return nil, err
	}
	return gen, nil
}

// MaxContextRunes returns the default context budget in runes.
func (r *Registry) MaxContextRunes() int { return MaxContextRunes }

// Similarity returns the cosine similarity of two vectors. It returns 0 when
// the lengths differ, a vector is empty or either norm is 0.
func (r *Registry) Similarity(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		x := float64(a[i])
		y := float64(b[i])
		dot += x * y
		normA += x * x
		normB += y * y
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// validateConfig checks the fields a provider cannot work without.
func validateConfig(cfg *core.AIProviderConfig) error {
	if cfg == nil {
		return core.Invalid("provider", "configuration is required")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return core.Invalid("baseUrl", "is required")
	}
	parsed, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil {
		return core.Invalid("baseUrl", "is not a valid URL")
	}
	if !parsed.IsAbs() || parsed.Host == "" {
		return core.Invalid("baseUrl", "must be an absolute URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return core.Invalid("baseUrl", "must use http or https")
	}
	if !validKind(cfg.Kind) {
		return core.Invalid("kind", "must be one of openai, ollama, anthropic or comfyui")
	}
	return nil
}

// validKind reports whether kind names a supported protocol.
func validKind(kind core.AIProviderKind) bool {
	for _, known := range core.AIProviderKinds {
		if kind == known {
			return true
		}
	}
	return false
}

// configHash digests the configuration fields that change how a provider
// talks to its server. The API key is hashed, never stored in the key.
func configHash(cfg *core.AIProviderConfig) string {
	parts := []string{
		string(cfg.Kind),
		strings.TrimSpace(cfg.BaseURL),
		cfg.APIKey,
		cfg.Model,
		cfg.EmbedModel,
		strconv.Itoa(cfg.TimeoutSeconds),
		strconv.FormatBool(cfg.ImageGeneration),
		strconv.FormatBool(cfg.ImageEditing),
		cfg.ImageModel,
		strings.Join(cfg.ImageSizes, ","),
		cfg.ComfyGenerate,
		cfg.ComfyEdit,
	}
	names := make([]string, 0, len(cfg.Headers))
	for name := range cfg.Headers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		parts = append(parts, name+"="+cfg.Headers[name])
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// copyConfig copies a configuration so later caller edits cannot reach into a
// built provider.
func copyConfig(cfg *core.AIProviderConfig) *core.AIProviderConfig {
	copied := *cfg
	copied.BaseURL = strings.TrimSpace(cfg.BaseURL)
	if len(cfg.Headers) > 0 {
		copied.Headers = make(map[string]string, len(cfg.Headers))
		for name, value := range cfg.Headers {
			copied.Headers[name] = value
		}
	}
	if len(cfg.Models) > 0 {
		copied.Models = append([]string(nil), cfg.Models...)
	}
	if len(cfg.ImageSizes) > 0 {
		copied.ImageSizes = append([]string(nil), cfg.ImageSizes...)
	}
	return &copied
}

// providerCount reports how many providers are cached. It exists for tests.
func (r *Registry) providerCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.cache)
}
