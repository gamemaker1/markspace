package module

import (
	"fmt"
	"regexp"
	"slices"

	"github.com/gamemaker1/markspace/internal/core"
)

// Module is a unit of features. Built-in modules are compiled into the binary.
// The plugin host adapts each plugin manifest into a Module.
type Module interface {
	// Manifest describes the module. Built-in modules return a static value.
	Manifest() Manifest
	// Register adds the module's contributions. It runs once per Registry.Add.
	Register(r *Registry) error
}

// Manifest describes a module.
type Manifest struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version,omitempty"`
	Builtin     bool     `json:"builtin"`
	Permissions []string `json:"permissions,omitempty"`
}

// Permission groups from docs/plugins.md.
var Permissions = []string{
	"items:read", "items:write", "links:read", "search", "comments",
	"suggestions", "ai:complete", "ai:embed", "ai:vision", "ai:image",
	"web:fetch", "jobs", "storage", "ui", "events",
}

var (
	identPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)
	toolNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	iconPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	objectIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
	qualifiedID     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}\.[a-z0-9][a-z0-9._-]{0,62}$`)
)

// ValidObjectID reports whether s has the objectId shape of the plugin
// protocol: 1 to 128 characters from A-Z, a-z, 0-9, underscore and hyphen,
// starting with a letter or digit. core.NewID values match. Job, item, space
// and user ids in plugin input use it.
func ValidObjectID(s string) bool { return objectIDPattern.MatchString(s) }

// ValidQualifiedID reports whether s is "<pluginId>.<localId>", such as
// "bookmark-wiki.wiki". The plugin id ends at the first dot.
func ValidQualifiedID(s string) bool { return qualifiedID.MatchString(s) }

// ValidIdent reports whether s is a valid module id, job kind, command id,
// panel id, importer id or action id: 1 to 63 characters from a-z, 0-9, dot,
// underscore and hyphen, starting with a letter or digit.
func ValidIdent(s string) bool { return identPattern.MatchString(s) }

// ValidToolName reports whether s is a valid AI tool name. Dots are not
// allowed, because provider tool APIs reject them.
func ValidToolName(s string) bool { return toolNamePattern.MatchString(s) }

// Validate checks the manifest fields. It returns a core.ValidationError.
func (m Manifest) Validate() error {
	if !ValidIdent(m.ID) {
		return core.Invalid("id", fmt.Sprintf("invalid module id %q", m.ID))
	}
	if len(m.Name) > 200 {
		return core.Invalid("name", "name is longer than 200 bytes")
	}
	if len(m.Version) > 64 {
		return core.Invalid("version", "version is longer than 64 bytes")
	}
	seen := map[string]bool{}
	for _, p := range m.Permissions {
		if !slices.Contains(Permissions, p) {
			return core.Invalid("permissions", fmt.Sprintf("unknown permission %q", p))
		}
		if seen[p] {
			return core.Invalid("permissions", fmt.Sprintf("duplicate permission %q", p))
		}
		seen[p] = true
	}
	return nil
}

// New returns a Module with a fixed manifest and a register function. The
// plugin host and tests use it instead of declaring a type.
func New(m Manifest, register func(r *Registry) error) Module {
	return funcModule{m: m, register: register}
}

type funcModule struct {
	m        Manifest
	register func(r *Registry) error
}

func (f funcModule) Manifest() Manifest { return f.m }

func (f funcModule) Register(r *Registry) error {
	if f.register == nil {
		return nil
	}
	return f.register(r)
}
