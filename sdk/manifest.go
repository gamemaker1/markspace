package sdk

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/gamemaker1/markspace/sdk/ui"
)

// ProtocolVersion is the plugin protocol this package implements.
const ProtocolVersion = 1

// Manifest is the document a plugin serves at GET /manifest.json.
type Manifest struct {
	Protocol    int                `json:"protocol"`
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Version     string             `json:"version"`
	Description string             `json:"description,omitempty"`
	Homepage    string             `json:"homepage,omitempty"`
	Permissions []string           `json:"permissions,omitempty"`
	Events      []string           `json:"events,omitempty"`
	Jobs        []ManifestJob      `json:"jobs,omitempty"`
	Schedules   []ManifestSchedule `json:"schedules,omitempty"`
	Commands    []ManifestCommand  `json:"commands,omitempty"`
	Panels      []ManifestPanel    `json:"panels,omitempty"`
	Importers   []ManifestImporter `json:"importers,omitempty"`
	Settings    *ManifestSettings  `json:"settings,omitempty"`
	Secrets     []ManifestSecret   `json:"secrets,omitempty"`
}

// ManifestJob declares a job kind.
type ManifestJob struct {
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	MaxAttempts int    `json:"maxAttempts,omitempty"`
}

// ManifestSchedule runs a job kind periodically. Every is a Go duration such
// as 24h.
type ManifestSchedule struct {
	Job   string `json:"job"`
	Every string `json:"every"`
}

// ManifestCommand declares a command.
type ManifestCommand struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Icon      string   `json:"icon,omitempty"`
	Contexts  []string `json:"contexts"`
	Shortcut  string   `json:"shortcut,omitempty"`
	MinAccess string   `json:"minAccess,omitempty"`
}

// ManifestPanel declares a rail panel or a page.
type ManifestPanel struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Icon     string   `json:"icon,omitempty"`
	Where    string   `json:"where"`
	Contexts []string `json:"contexts"`
	Order    int      `json:"order,omitempty"`
}

// ManifestImporter declares an importer.
type ManifestImporter struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Accept []string `json:"accept,omitempty"`
}

// ManifestSettings holds the settings form fields.
type ManifestSettings struct {
	Fields []ui.Node `json:"fields"`
}

// ManifestSecret declares a secret an admin fills in.
type ManifestSecret struct {
	Name  string `json:"name"`
	Label string `json:"label,omitempty"`
}

// Permissions lists the permission groups from docs/plugins.md.
var Permissions = []string{
	"items:read", "items:write", "links:read", "search", "comments", "suggestions",
	"ai:complete", "ai:embed", "ai:vision", "ai:image", "web:fetch", "jobs", "storage", "ui", "events",
}

// ParseManifest decodes and validates a manifest.
func ParseManifest(b []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("manifest: %w", err)
	}
	return m, m.Validate()
}

// Validate checks the manifest fields this package relies on.
func (m Manifest) Validate() error {
	if m.Protocol != ProtocolVersion {
		return fmt.Errorf("manifest: protocol %d is not supported, want %d", m.Protocol, ProtocolVersion)
	}
	if !ValidPluginID(m.ID) {
		return fmt.Errorf("manifest: invalid plugin id %q", m.ID)
	}
	if m.Name == "" || m.Version == "" {
		return fmt.Errorf("manifest: name and version are required")
	}
	for _, p := range m.Permissions {
		if !contains(Permissions, p) {
			return fmt.Errorf("manifest: unknown permission %q", p)
		}
	}
	for _, e := range m.Events {
		if !ValidEventPattern(e) {
			return fmt.Errorf("manifest: invalid event pattern %q", e)
		}
	}
	jobs := map[string]bool{}
	for _, j := range m.Jobs {
		if err := uniqueLocal("job kind", j.Kind, jobs); err != nil {
			return err
		}
	}
	for _, s := range m.Schedules {
		if !jobs[s.Job] {
			return fmt.Errorf("manifest: schedule names undeclared job %q", s.Job)
		}
		if d, err := time.ParseDuration(s.Every); err != nil || d <= 0 {
			return fmt.Errorf("manifest: schedule for %q has invalid every %q", s.Job, s.Every)
		}
	}
	seen := map[string]bool{}
	for _, c := range m.Commands {
		if err := uniqueLocal("command id", c.ID, seen); err != nil {
			return err
		}
	}
	seen = map[string]bool{}
	for _, p := range m.Panels {
		if err := uniqueLocal("panel id", p.ID, seen); err != nil {
			return err
		}
		if p.Where != "rail" && p.Where != "page" {
			return fmt.Errorf("manifest: panel %q has where %q, want rail or page", p.ID, p.Where)
		}
	}
	seen = map[string]bool{}
	for _, i := range m.Importers {
		if err := uniqueLocal("importer id", i.ID, seen); err != nil {
			return err
		}
	}
	seen = map[string]bool{}
	for _, s := range m.Secrets {
		if !ValidSecretName(s.Name) || seen[s.Name] {
			return fmt.Errorf("manifest: invalid or duplicate secret name %q", s.Name)
		}
		seen[s.Name] = true
	}
	return nil
}

func uniqueLocal(what, id string, seen map[string]bool) error {
	if !ValidLocalID(id) {
		return fmt.Errorf("manifest: invalid %s %q", what, id)
	}
	if seen[id] {
		return fmt.Errorf("manifest: duplicate %s %q", what, id)
	}
	seen[id] = true
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
