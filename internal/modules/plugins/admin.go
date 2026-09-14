package plugins

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/module"
)

// registerAdmin adds the admin API and the /admin/plugins page.
func (m *Module) registerAdmin(r *module.Registry) {
	r.API("GET /api/admin/plugins", m.adminList)
	r.API("GET /api/admin/plugins/{id}", m.adminGet)
	r.API("POST /api/admin/plugins/preview", m.adminPreview)
	r.API("POST /api/admin/plugins", m.adminInstall)
	r.API("PATCH /api/admin/plugins/{id}", m.adminConfigure)
	r.API("POST /api/admin/plugins/{id}/pause", m.adminState("pause"))
	r.API("POST /api/admin/plugins/{id}/resume", m.adminState("resume"))
	r.API("POST /api/admin/plugins/{id}/check", m.adminState("check"))
	r.API("POST /api/admin/plugins/{id}/approve", m.adminState("approve"))
	r.API("POST /api/admin/plugins/{id}/uninstall", m.adminUninstall)
	r.Web("GET /admin/plugins", m.page)
	r.Web("POST /admin/plugins", m.pagePost)
	r.Web("GET /modules/plugins/admin.js", m.adminScript)
	// Plugin page panels are open to every signed-in person. The panel API
	// checks access when the page script loads the panel.
	r.Web("GET /plugins/page", m.pluginPage)
}

// adminActor returns the signed-in admin person behind a request. An
// anonymous request, or a plugin actor, is unauthorized. A person who is not
// an admin is forbidden.
func adminActor(r *http.Request) (*core.Actor, error) {
	actor := api.ActorFrom(r.Context())
	if !actor.IsPerson() {
		return nil, fmt.Errorf("sign in required: %w", core.ErrUnauthorized)
	}
	if !actor.IsAdmin() {
		return nil, fmt.Errorf("admin required: %w", core.ErrForbidden)
	}
	return actor, nil
}

func (m *Module) fail(w http.ResponseWriter, r *http.Request, err error) {
	api.WriteError(w, r, m.log, err)
}

// admin runs fn for an admin request and writes the error otherwise.
func (m *Module) admin(w http.ResponseWriter, r *http.Request) bool {
	if _, err := adminActor(r); err != nil {
		m.fail(w, r, err)
		return false
	}
	if m.opts.Host == nil {
		m.fail(w, r, fmt.Errorf("the plugin host is not running: %w", core.ErrUnavailable))
		return false
	}
	return true
}

type pluginList struct {
	Items []PluginView `json:"items"`
}

// adminList serves GET /api/admin/plugins.
func (m *Module) adminList(w http.ResponseWriter, r *http.Request) {
	if !m.admin(w, r) {
		return
	}
	list, err := m.opts.Host.List(r.Context())
	if err != nil {
		m.fail(w, r, err)
		return
	}
	if list == nil {
		list = []PluginView{}
	}
	api.WriteJSON(w, http.StatusOK, pluginList{Items: list})
}

// adminGet serves GET /api/admin/plugins/{id}.
func (m *Module) adminGet(w http.ResponseWriter, r *http.Request) {
	if !m.admin(w, r) {
		return
	}
	p, err := m.opts.Host.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, p)
}

type previewRequest struct {
	BaseURL string `json:"baseUrl"`
}

// adminPreview serves POST /api/admin/plugins/preview {baseUrl}.
func (m *Module) adminPreview(w http.ResponseWriter, r *http.Request) {
	if !m.admin(w, r) {
		return
	}
	var in previewRequest
	if err := api.Decode(r, &in); err != nil {
		m.fail(w, r, err)
		return
	}
	if strings.TrimSpace(in.BaseURL) == "" {
		m.fail(w, r, core.Invalid("baseUrl", "is required"))
		return
	}
	p, err := m.opts.Host.Preview(r.Context(), strings.TrimSpace(in.BaseURL))
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, p)
}

// adminInstall serves POST /api/admin/plugins {InstallInput}.
func (m *Module) adminInstall(w http.ResponseWriter, r *http.Request) {
	if !m.admin(w, r) {
		return
	}
	var in InstallInput
	if err := api.Decode(r, &in); err != nil {
		m.fail(w, r, err)
		return
	}
	if strings.TrimSpace(in.BaseURL) == "" {
		m.fail(w, r, core.Invalid("baseUrl", "is required"))
		return
	}
	res, err := m.opts.Host.Install(r.Context(), in)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, res)
}

// adminConfigure serves PATCH /api/admin/plugins/{id} {ConfigInput}.
func (m *Module) adminConfigure(w http.ResponseWriter, r *http.Request) {
	if !m.admin(w, r) {
		return
	}
	var in ConfigInput
	if err := api.Decode(r, &in); err != nil {
		m.fail(w, r, err)
		return
	}
	p, err := m.opts.Host.Configure(r.Context(), r.PathValue("id"), in)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, p)
}

// runState applies pause, resume, check or approve.
func (m *Module) runState(r *http.Request, id, action string) (*PluginView, error) {
	h, ctx := m.opts.Host, r.Context()
	switch action {
	case "pause":
		return h.SetPaused(ctx, id, true)
	case "resume":
		return h.SetPaused(ctx, id, false)
	case "check":
		return h.CheckManifest(ctx, id)
	case "approve":
		return h.Approve(ctx, id)
	}
	return nil, core.Invalid("action", "unknown action")
}

// adminState serves POST /api/admin/plugins/{id}/pause, resume, check and
// approve.
func (m *Module) adminState(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !m.admin(w, r) {
			return
		}
		p, err := m.runState(r, r.PathValue("id"), action)
		if err != nil {
			m.fail(w, r, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, p)
	}
}

type uninstallRequest struct {
	DeleteData bool `json:"deleteData"`
}

// adminUninstall serves POST /api/admin/plugins/{id}/uninstall {deleteData}.
func (m *Module) adminUninstall(w http.ResponseWriter, r *http.Request) {
	if !m.admin(w, r) {
		return
	}
	var in uninstallRequest
	if r.ContentLength != 0 {
		if err := api.Decode(r, &in); err != nil {
			m.fail(w, r, err)
			return
		}
	}
	if err := m.opts.Host.Uninstall(r.Context(), r.PathValue("id"), in.DeleteData); err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}
