// Package config loads server configuration from a YAML file, with
// environment variable overrides.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/gamemaker1/markspace/internal/ai"
	"github.com/gamemaker1/markspace/internal/core"
)

// Config is the whole server configuration.
type Config struct {
	// Listen is the bind address, for example ":8080" or "127.0.0.1:8080".
	Listen string `yaml:"listen"`
	// BaseURL is the externally reachable root, used to build share, invite and
	// attachment links. Empty means the origin each request arrived on.
	BaseURL string `yaml:"baseUrl"`
	// DataDir holds spaces/, attachments and the index database.
	DataDir string `yaml:"dataDir"`
	// DBPath defaults to <DataDir>/markspace.db.
	DBPath string `yaml:"dbPath"`

	InstanceName  string        `yaml:"instanceName"`
	AllowSignup   bool          `yaml:"allowSignup"`
	SessionTTL    time.Duration `yaml:"sessionTTL"`
	SecureCookies bool          `yaml:"secureCookies"`
	TrustProxy    bool          `yaml:"trustProxy"`
	MaxUploadMB   int           `yaml:"maxUploadMB"`
	// WatchFiles rereads notes edited outside the app.
	WatchFiles bool `yaml:"watchFiles"`
	// DevMode reloads templates and static files from disk on every request.
	DevMode  bool   `yaml:"devMode"`
	LogLevel string `yaml:"logLevel"`

	AI AIConfig `yaml:"ai"`

	// Jobs configures the background job engine.
	Jobs JobsConfig `yaml:"jobs"`
	// Plugins configures the plugin host.
	Plugins PluginsConfig `yaml:"plugins"`

	// localSet records the providers whose file entry sets local, so a
	// preset does not override an explicit false.
	localSet map[int]bool
}

// AIConfig configures the model providers available to the instance.
type AIConfig struct {
	Enabled bool `yaml:"enabled"`
	// AllowUserKeys lets users register their own providers.
	AllowUserKeys bool `yaml:"allowUserKeys"`
	// Routes maps a task to its ordered models. A task the database has no
	// route for uses this one. docs/models.md lists the tasks.
	Routes map[string][]core.AIRouteTarget `yaml:"routes"`
	// FetchPublicPrices downloads the OpenRouter and LiteLLM price lists once
	// a day to price models whose provider reports no price.
	FetchPublicPrices bool `yaml:"fetchPublicPrices"`
	// ContextChunks is how many note chunks go into an ask prompt.
	ContextChunks int `yaml:"contextChunks"`
	// ContextChars is the character budget for the text of one prompt: the
	// task text plus every resolved context reference.
	ContextChars int `yaml:"contextChars"`
	// AllowPrivateURLs lets url context references reach loopback, private,
	// carrier-grade NAT, link-local and other internal addresses.
	AllowPrivateURLs bool                    `yaml:"allowPrivateURLs"`
	Providers        []core.AIProviderConfig `yaml:"providers"`
}

// JobsConfig configures the job engine. docs/plugins.md describes the queue.
type JobsConfig struct {
	// Workers is the number of job worker goroutines. The default is 4.
	Workers int `yaml:"workers"`
}

// PluginsConfig configures the plugin host.
type PluginsConfig struct {
	// MarkspaceURL is the address plugins use to reach this server, sent in
	// the install payload. Empty means BaseURL, and without a BaseURL the
	// listen address on 127.0.0.1.
	MarkspaceURL string `yaml:"markspaceUrl"`
	// AllowPrivateURLs lets the plugin host API web fetch reach loopback,
	// private and link-local addresses.
	AllowPrivateURLs bool `yaml:"allowPrivateURLs"`
}

// DefaultJobWorkers is the job worker count when the configuration sets none.
const DefaultJobWorkers = 4

// DefaultContextChars is the context budget when the configuration sets none.
const DefaultContextChars = 48000

// ImageTimeoutSeconds is the timeout of a comfyui provider, or of a provider
// with an image capability, when the configuration sets none.
const ImageTimeoutSeconds = 300

// Default returns the configuration used when no file exists.
func Default() *Config {
	return &Config{
		Listen:        ":8080",
		DataDir:       "./data",
		InstanceName:  "Markspace",
		AllowSignup:   true,
		SessionTTL:    30 * 24 * time.Hour,
		SecureCookies: false,
		MaxUploadMB:   1024,
		WatchFiles:    true,
		LogLevel:      "info",
		AI: AIConfig{
			Enabled:           true,
			AllowUserKeys:     true,
			FetchPublicPrices: true,
			ContextChunks:     8,
			ContextChars:      DefaultContextChars,
		},
	}
}

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandEnv replaces ${VAR} with the environment value, or the empty string.
func expandEnv(s string) string {
	return envRef.ReplaceAllStringFunc(s, func(m string) string {
		return os.Getenv(m[2 : len(m)-1])
	})
}

// Load reads path if it exists, fills defaults, applies MARKSPACE_* overrides
// and validates the result. An empty path skips the file.
func Load(path string) (*Config, error) {
	cfg := Default()
	if path != "" {
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			dec := yaml.NewDecoder(strings.NewReader(string(data)))
			dec.KnownFields(true)
			if err := dec.Decode(cfg); err != nil {
				return nil, fmt.Errorf("config %s: %w", path, err)
			}
			if err := providerDefaults(data, cfg); err != nil {
				return nil, fmt.Errorf("config %s: %w", path, err)
			}
		case os.IsNotExist(err):
			// Defaults are enough for a first run.
		default:
			return nil, fmt.Errorf("config %s: %w", path, err)
		}
	}
	cfg.applyEnv()
	if err := cfg.finalize(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// providerDefaults sets Enabled on every file provider that has no explicit
// enabled key, and records which providers set local. A YAML bool cannot tell
// an absent key from false, so this reads the providers a second time as
// plain maps.
func providerDefaults(data []byte, cfg *Config) error {
	var raw struct {
		AI struct {
			Providers []map[string]any `yaml:"providers"`
		} `yaml:"ai"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return err
	}
	for i := range cfg.AI.Providers {
		if i >= len(raw.AI.Providers) {
			break
		}
		if _, set := raw.AI.Providers[i]["enabled"]; !set {
			cfg.AI.Providers[i].Enabled = true
		}
		if _, set := raw.AI.Providers[i]["local"]; set {
			if cfg.localSet == nil {
				cfg.localSet = map[int]bool{}
			}
			cfg.localSet[i] = true
		}
	}
	return nil
}

func (c *Config) applyEnv() {
	if v := os.Getenv("MARKSPACE_LISTEN"); v != "" {
		c.Listen = v
	}
	if v := os.Getenv("MARKSPACE_BASE_URL"); v != "" {
		c.BaseURL = v
	}
	if v := os.Getenv("MARKSPACE_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if v := os.Getenv("MARKSPACE_DB_PATH"); v != "" {
		c.DBPath = v
	}
	if v := os.Getenv("MARKSPACE_INSTANCE_NAME"); v != "" {
		c.InstanceName = v
	}
	if v := os.Getenv("MARKSPACE_LOG_LEVEL"); v != "" {
		c.LogLevel = v
	}
	if v, ok := boolEnv("MARKSPACE_ALLOW_SIGNUP"); ok {
		c.AllowSignup = v
	}
	if v, ok := boolEnv("MARKSPACE_SECURE_COOKIES"); ok {
		c.SecureCookies = v
	}
	if v, ok := boolEnv("MARKSPACE_TRUST_PROXY"); ok {
		c.TrustProxy = v
	}
	if v, ok := boolEnv("MARKSPACE_DEV"); ok {
		c.DevMode = v
	}
	if v, ok := boolEnv("MARKSPACE_WATCH_FILES"); ok {
		c.WatchFiles = v
	}
	if v, ok := boolEnv("MARKSPACE_AI_ENABLED"); ok {
		c.AI.Enabled = v
	}
	if v, ok := boolEnv("MARKSPACE_AI_FETCH_PUBLIC_PRICES"); ok {
		c.AI.FetchPublicPrices = v
	}
	if v := os.Getenv("MARKSPACE_AI_CONTEXT_CHARS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.AI.ContextChars = n
		}
	}
	if v, ok := boolEnv("MARKSPACE_AI_ALLOW_PRIVATE_URLS"); ok {
		c.AI.AllowPrivateURLs = v
	}
	if v := os.Getenv("MARKSPACE_PLUGINS_URL"); v != "" {
		c.Plugins.MarkspaceURL = v
	}
	if v, ok := boolEnv("MARKSPACE_PLUGINS_ALLOW_PRIVATE_URLS"); ok {
		c.Plugins.AllowPrivateURLs = v
	}
	if v := os.Getenv("MARKSPACE_MAX_UPLOAD_MB"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.MaxUploadMB = n
		}
	}
	if v := os.Getenv("MARKSPACE_SESSION_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.SessionTTL = d
		}
	}
}

func boolEnv(key string) (bool, bool) {
	v := os.Getenv(key)
	if v == "" {
		return false, false
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, false
	}
	return b, true
}

func (c *Config) finalize() error {
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.DataDir == "" {
		c.DataDir = "./data"
	}
	abs, err := filepath.Abs(c.DataDir)
	if err != nil {
		return fmt.Errorf("data dir: %w", err)
	}
	c.DataDir = abs
	if c.DBPath == "" {
		c.DBPath = filepath.Join(c.DataDir, "markspace.db")
	} else if !filepath.IsAbs(c.DBPath) {
		c.DBPath, _ = filepath.Abs(c.DBPath)
	}
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	if c.SessionTTL <= 0 {
		c.SessionTTL = 30 * 24 * time.Hour
	}
	if c.MaxUploadMB <= 0 {
		c.MaxUploadMB = 1024
	}
	if c.AI.ContextChunks <= 0 {
		c.AI.ContextChunks = 8
	}
	if c.Jobs.Workers <= 0 {
		c.Jobs.Workers = DefaultJobWorkers
	}
	c.Plugins.MarkspaceURL = strings.TrimRight(c.Plugins.MarkspaceURL, "/")
	if c.AI.ContextChars <= 0 {
		c.AI.ContextChars = DefaultContextChars
	}

	kinds := map[string]core.AIProviderKind{}
	for i := range c.AI.Providers {
		p := &c.AI.Providers[i]
		p.Source = "file"
		p.APIKey = expandEnv(p.APIKey)
		p.BaseURL = expandEnv(strings.TrimRight(p.BaseURL, "/"))
		p.HasAPIKey = p.APIKey != ""
		if p.ID == "" && strings.TrimSpace(p.Name) == "" {
			p.ID = p.Preset
		}
		if p.ID == "" {
			p.ID = core.Slugify(p.Name)
		}
		if p.Name == "" {
			p.Name = p.ID
		}
		local := p.Local
		if err := ai.ApplyPreset(p); err != nil {
			return fmt.Errorf("ai provider %s: %w", p.ID, err)
		}
		if c.localSet[i] {
			p.Local = local
		}
		p.BaseURL = strings.TrimRight(p.BaseURL, "/")
		if p.Kind == "" {
			p.Kind = core.ProviderOpenAI
		}
		if !validKind(p.Kind) {
			return fmt.Errorf("ai provider %s: unknown kind %q", p.ID, p.Kind)
		}
		if p.BaseURL == "" {
			return fmt.Errorf("ai provider %s: baseUrl is required", p.ID)
		}
		if err := p.ValidateImageFields(); err != nil {
			return fmt.Errorf("ai provider %s: %w", p.ID, err)
		}
		if p.Kind == core.ProviderComfyUI && p.EmbedModel != "" {
			return fmt.Errorf("ai provider %s: the comfyui kind has no embeddings, remove embedModel", p.ID)
		}
		if p.TimeoutSeconds <= 0 {
			p.TimeoutSeconds = DefaultTimeoutSeconds(p)
		}
		if p.MaxTokens <= 0 {
			p.MaxTokens = 2048
		}
		if _, dup := kinds[p.ID]; dup {
			return fmt.Errorf("ai provider %s: duplicate id", p.ID)
		}
		kinds[p.ID] = p.Kind
	}
	for task, targets := range c.AI.Routes {
		if !core.ValidRouteTask(task) {
			return fmt.Errorf("ai route %s: unknown task", task)
		}
		if len(targets) > core.MaxRouteTargets {
			return fmt.Errorf("ai route %s: more than %d targets", task, core.MaxRouteTargets)
		}
		needs := core.RouteNeeds(task)
		for _, t := range targets {
			if strings.TrimSpace(t.ProviderID) == "" || strings.TrimSpace(t.Model) == "" {
				return fmt.Errorf("ai route %s: every target needs providerId and model", task)
			}
			if kinds[t.ProviderID] == core.ProviderComfyUI && (needs.Text || needs.Embedding) {
				return fmt.Errorf("ai route %s: the comfyui provider %s has no chat or embeddings", task, t.ProviderID)
			}
		}
	}
	return nil
}

// DefaultTimeoutSeconds is the call timeout of a provider that sets none:
// 300 s for a comfyui provider or a provider with an image capability, and
// 120 s otherwise, because image calls take longer than chat calls.
func DefaultTimeoutSeconds(p *core.AIProviderConfig) int {
	if p.Kind == core.ProviderComfyUI || p.ImageGeneration || p.ImageEditing {
		return ImageTimeoutSeconds
	}
	return 120
}

func validKind(k core.AIProviderKind) bool {
	for _, v := range core.AIProviderKinds {
		if v == k {
			return true
		}
	}
	return false
}

// SpacesDir is where space directories live.
func (c *Config) SpacesDir() string { return filepath.Join(c.DataDir, "spaces") }

// InternalDir holds history, attachments and other app state on disk.
func (c *Config) InternalDir() string { return filepath.Join(c.DataDir, "internal") }

// MaxUploadBytes converts MaxUploadMB to bytes.
func (c *Config) MaxUploadBytes() int64 { return int64(c.MaxUploadMB) * 1 << 20 }
