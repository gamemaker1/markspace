package core

import (
	"context"
	"slices"
	"time"
)

// Plugin token prefixes. A plugin token authenticates the plugin itself. An
// acting token lets a plugin act for one user during one interaction.
const (
	PluginTokenPrefix = "mspl_"
	ActingTokenPrefix = "msat_"
)

// User kinds. A plugin user cannot sign in.
const (
	UserKindPerson = "person"
	UserKindPlugin = "plugin"
)

// Plugin statuses as stored in the plugins table.
const (
	PluginStatusNeedsApproval = "needs_approval"
	PluginStatusActive        = "active"
	PluginStatusPaused        = "paused"
	PluginStatusDown          = "down"
	PluginStatusUninstalled   = "uninstalled"
)

// PluginStatusRuns reports whether a plugin in this status may authenticate
// and receive calls. A plugin that needs approval of a new manifest keeps
// running with its old grant. A down plugin failed its health checks, and a
// call from it counts as a sign of life.
func PluginStatusRuns(status string) bool {
	return status == PluginStatusActive || status == PluginStatusDown || status == PluginStatusNeedsApproval
}

// PluginIdentity is the plugin behind a plugin token or an acting token, as
// resolved when the request authenticated.
type PluginIdentity struct {
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	UserID string `json:"userId"`
	Status string `json:"status"`
	// Permissions are the permission groups of the approved manifest.
	Permissions []string `json:"permissions"`
	// Grants maps a space id to the plugin's role in it: viewer, commenter or
	// editor.
	Grants map[string]Role `json:"grants"`
}

// Allows reports whether the approved manifest holds a permission group.
func (p *PluginIdentity) Allows(permission string) bool {
	return p != nil && slices.Contains(p.Permissions, permission)
}

// SpaceAccess returns the access the plugin's grant gives on a space. It is
// capped at edit, so a plugin never manages a space.
func (p *PluginIdentity) SpaceAccess(spaceID string) Access {
	if p == nil || spaceID == "" {
		return AccessNone
	}
	return PluginRoleAccess(p.Grants[spaceID])
}

// SpaceIDs returns the granted space ids in sorted order.
func (p *PluginIdentity) SpaceIDs() []string {
	if p == nil {
		return nil
	}
	out := make([]string, 0, len(p.Grants))
	for id, role := range p.Grants {
		if PluginRoleAccess(role) > AccessNone {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

// PluginRoleAccess maps a plugin grant role to access. Only viewer,
// commenter and editor count. Owner and admin grant nothing, because a
// plugin may never hold them.
func PluginRoleAccess(r Role) Access {
	switch r {
	case RoleViewer:
		return AccessView
	case RoleCommenter:
		return AccessComment
	case RoleEditor:
		return AccessEdit
	}
	return AccessNone
}

// ValidPluginRole reports whether a role may be granted to a plugin.
func ValidPluginRole(r Role) bool { return PluginRoleAccess(r) > AccessNone }

// ActingToken is a resolved acting token.
type ActingToken struct {
	Plugin *PluginIdentity
	UserID string
	// Interaction names what the token was issued for, such as
	// "command:count", "panel:counts", "action:count-space",
	// "import:text" or "job:<job id>".
	Interaction string
	ExpiresAt   time.Time
}

// PluginResolver resolves plugin credentials. The plugin host implements it
// and the application uses it to authenticate plugin requests.
type PluginResolver interface {
	// ResolvePluginToken returns the plugin that holds a plugin token. An
	// unknown token returns ErrUnauthorized.
	ResolvePluginToken(ctx context.Context, token string) (*PluginIdentity, error)
	// ResolveActingToken returns an unexpired acting token. An unknown or
	// expired token returns ErrUnauthorized.
	ResolveActingToken(ctx context.Context, token string) (*ActingToken, error)
	// PluginIdentity reloads a plugin by id, for callers that keep an
	// identity and must see grant changes. An uninstalled plugin returns
	// ErrNotFound.
	PluginIdentity(ctx context.Context, pluginID string) (*PluginIdentity, error)
	// ValidInteraction reports whether an acting token's interaction still
	// names something the plugin may act on: a contribution of its approved
	// manifest, or a running job of the plugin.
	ValidInteraction(ctx context.Context, pluginID, interaction string) bool
}

// EventFilter decides which events one subscriber may see.
type EventFilter interface {
	Allows(ctx context.Context, ev Event) bool
}

// AIEmbedResult is the answer of an embedding request made by a plugin.
type AIEmbedResult struct {
	Provider string      `json:"provider"`
	Model    string      `json:"model"`
	Vectors  [][]float32 `json:"vectors"`
}

// PluginSupportService holds the application calls the plugin host API needs
// beyond the rest of Backend. Every method checks access for the actor.
type PluginSupportService interface {
	// OutboundLinks lists the links of a note resolved as the reader would
	// see them. Targets the actor cannot view come back unresolved.
	OutboundLinks(ctx context.Context, actor *Actor, noteID string) ([]*ResolvedTarget, error)
	// FileText returns the extracted text of a file and whether extraction
	// cut it short.
	FileText(ctx context.Context, actor *Actor, fileID string) (string, bool, error)
	// EmbedTexts embeds texts with the instance embeddings route and records
	// the usage for pluginID.
	EmbedTexts(ctx context.Context, actor *Actor, texts []string, pluginID string) (*AIEmbedResult, error)
}
