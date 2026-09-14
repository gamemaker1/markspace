package plugins

import (
	"net/http"
	"strings"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
)

// pagePanel finds the page panel a full panel id names. The id is the plugin
// id and the manifest panel id joined by a dot. It reports false when the
// plugin is unknown or uninstalled, or the panel is not a page panel.
func (m *Module) pagePanel(r *http.Request, fullID string) (*PluginView, Contribution, bool) {
	pluginID, local, ok := strings.Cut(fullID, ".")
	if !ok || pluginID == "" || local == "" || m.opts.Host == nil {
		return nil, Contribution{}, false
	}
	p, err := m.opts.Host.Get(r.Context(), pluginID)
	if err != nil || p.Status == core.PluginStatusUninstalled {
		return nil, Contribution{}, false
	}
	for _, panel := range p.Approved.Panels {
		if panel.ID == local && panel.Extra == "page" {
			return p, panel, true
		}
	}
	return nil, Contribution{}, false
}

// pluginPage serves GET /plugins/page?panel=&itemId=&spaceId=. It renders the
// module page layout without a Body. The page script then fetches the panel
// through POST /api/panels/{id}, which checks the plugin's state and the
// person's access to the context. Heading is the plugin name and Title the
// panel title. Title is empty when it repeats the plugin name. An unknown
// panel renders without either, and the script shows why it is unavailable.
func (m *Module) pluginPage(w http.ResponseWriter, r *http.Request) {
	actor := api.ActorFrom(r.Context())
	if !actor.IsPerson() {
		m.opts.Pages.RenderError(w, r, core.ErrUnauthorized)
		return
	}
	spec := api.PageSpec{Nav: "plugins"}
	if p, panel, ok := m.pagePanel(r, strings.TrimSpace(r.URL.Query().Get("panel"))); ok {
		spec.Heading, spec.Title = p.Name, panel.Title
		if strings.EqualFold(strings.TrimSpace(panel.Title), strings.TrimSpace(p.Name)) {
			spec.Title = ""
		}
	}
	m.opts.Pages.RenderModulePage(w, r, spec)
}
