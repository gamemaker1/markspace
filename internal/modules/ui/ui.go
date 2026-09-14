// Package ui is the built-in module that serves contributions to the client:
// the merged list of commands, panels and importers, and the routes that run
// a command, render a panel, run a panel action and start an import. It
// declares the built-in rail tabs, which the client renders itself.
//
// Plugin contributions reach the registry as plugin modules added by the
// plugin host. Panel actions have no registry entry, so an ActionRunner
// dispatches them. Every response is validated before it reaches the client,
// and a UI tree may only link to items the actor can view.
package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/module"
)

// ActionRunner runs panel actions of a module.
type ActionRunner interface {
	RunAction(ctx context.Context, moduleID, actionID string, req module.CommandRequest, payload map[string]any) (module.CommandResponse, error)
}

// Availability decides whether a module's contributions apply in a space.
// The plugin host hides a plugin's contributions in spaces it is not granted.
// An empty spaceID is the global context.
type Availability interface {
	ModuleAvailable(ctx context.Context, moduleID, spaceID string) bool
}

// Options configures the module.
type Options struct {
	Registry     *module.Registry
	Backend      core.Backend
	Actions      ActionRunner
	Availability Availability
	Logger       *slog.Logger
}

// Module serves the contribution routes.
type Module struct {
	opts Options
	log  *slog.Logger
}

// New returns the module.
func New(opts Options) *Module {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Module{opts: opts, log: log}
}

// Manifest implements module.Module.
func (m *Module) Manifest() module.Manifest {
	return module.Manifest{ID: "ui", Name: "Interface", Builtin: true}
}

// BuiltinPanels are the rail tabs the client renders itself.
var BuiltinPanels = []module.Panel{
	{ID: "outline", Title: "Outline", Icon: "list", Where: "rail", Contexts: []string{"note"}, Builtin: "outline", Order: 10, MinAccess: core.AccessView},
	{ID: "comments", Title: "Comments", Icon: "comment", Where: "rail", Contexts: []string{"note", "folder", "file"}, Builtin: "comments", Order: 20, MinAccess: core.AccessView},
	{ID: "ai", Title: "AI", Icon: "sparkles", Where: "rail", Contexts: []string{"note"}, Builtin: "ai", Order: 30, MinAccess: core.AccessView},
	{ID: "history", Title: "History", Icon: "history", Where: "rail", Contexts: []string{"note", "file"}, Builtin: "history", Order: 40, MinAccess: core.AccessView},
	{ID: "share", Title: "Share", Icon: "share", Where: "rail", Contexts: []string{"note", "folder", "file"}, Builtin: "share", Order: 50, MinAccess: core.AccessView},
	{ID: "files", Title: "Files", Icon: "folder", Where: "rail", Contexts: []string{"note", "folder", "space"}, Builtin: "files", Order: 60, MinAccess: core.AccessView},
	{ID: "info", Title: "Info", Icon: "info", Where: "rail", Contexts: []string{"note", "folder", "file"}, Builtin: "info", Order: 70, MinAccess: core.AccessView},
}

// Register implements module.Module.
func (m *Module) Register(r *module.Registry) error {
	for _, p := range BuiltinPanels {
		r.Panel(p)
	}
	r.API("GET /api/ui/contributions", m.contributions)
	r.API("POST /api/commands/{id}", m.command)
	r.API("POST /api/panels/{id}", m.panel)
	r.API("POST /api/modules/{module}/actions/{id}", m.action)
	r.API("POST /api/import/{importer}", m.importFile)
	return nil
}

func (m *Module) fail(w http.ResponseWriter, r *http.Request, err error) {
	api.WriteError(w, r, m.log, err)
}

// person returns the signed-in person behind a request.
func person(r *http.Request) (*core.Actor, error) {
	actor := api.ActorFrom(r.Context())
	if !actor.IsPerson() {
		return nil, fmt.Errorf("sign in required: %w", core.ErrUnauthorized)
	}
	return actor, nil
}

// extraContexts lists the contexts a client may add to the item and space
// contexts.
var extraContexts = []string{"selection", "link", "image"}

// scope is a resolved interaction context.
type scope struct {
	ctx      module.CommandContext
	access   core.Access
	contexts []string
}

// resolve checks the actor's access on the context item or space and fills
// the space and kind from the item. A missing item or space is not found.
func (m *Module) resolve(ctx context.Context, actor *core.Actor, c module.CommandContext, extra []string) (*scope, error) {
	s := &scope{ctx: c, contexts: []string{"global"}}
	switch {
	case c.ItemID != "":
		it, err := m.opts.Backend.GetItem(ctx, actor, c.ItemID)
		if err != nil {
			return nil, err
		}
		s.ctx.SpaceID, s.ctx.Kind, s.access = it.SpaceID(), it.Kind, it.Access
		s.contexts = append(s.contexts, "space", string(it.Kind))
	case c.SpaceID != "":
		g, err := m.opts.Backend.SpaceAccess(ctx, actor, c.SpaceID)
		if err != nil {
			return nil, err
		}
		if g == nil || g.Access == core.AccessNone {
			return nil, fmt.Errorf("space %s: %w", c.SpaceID, core.ErrNotFound)
		}
		s.access = g.Access
		s.ctx.Kind = ""
		s.contexts = append(s.contexts, "space")
	default:
		s.ctx.Kind = ""
	}
	if c.ImageFileID != "" {
		it, err := m.opts.Backend.GetItem(ctx, actor, c.ImageFileID)
		if err != nil {
			return nil, err
		}
		if it.File == nil {
			return nil, core.Invalid("context.imageFileId", "is not a file")
		}
	}
	for _, e := range extra {
		if slices.Contains(extraContexts, e) && !slices.Contains(s.contexts, e) {
			s.contexts = append(s.contexts, e)
		}
	}
	if c.Selection != nil && !slices.Contains(s.contexts, "selection") {
		s.contexts = append(s.contexts, "selection")
	}
	if c.Link != "" && !slices.Contains(s.contexts, "link") {
		s.contexts = append(s.contexts, "link")
	}
	if c.ImageFileID != "" && !slices.Contains(s.contexts, "image") {
		s.contexts = append(s.contexts, "image")
	}
	return s, nil
}

func (m *Module) available(ctx context.Context, moduleID, spaceID string) bool {
	return m.opts.Availability == nil || m.opts.Availability.ModuleAvailable(ctx, moduleID, spaceID)
}

func user(actor *core.Actor) module.Actor {
	return module.Actor{ID: actor.User.ID, Name: actor.User.Name}
}

// contributions serves GET /api/ui/contributions?itemId=&kind=&spaceId=&contexts=.
func (m *Module) contributions(w http.ResponseWriter, r *http.Request) {
	actor, err := person(r)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	ctx := r.Context()
	q := r.URL.Query()
	c := module.CommandContext{ItemID: strings.TrimSpace(q.Get("itemId")), SpaceID: strings.TrimSpace(q.Get("spaceId"))}
	s, err := m.resolve(ctx, actor, c, strings.Split(q.Get("contexts"), ","))
	if err != nil {
		m.fail(w, r, err)
		return
	}
	if kind := core.ItemKind(q.Get("kind")); kind != "" && c.ItemID == "" && c.SpaceID == "" {
		return400(w, r, m, "kind needs itemId")
		return
	}
	out := m.opts.Registry.Contributions(module.ContributionFilter{
		Access:   s.access,
		Contexts: s.contexts,
		Admin:    actor.IsAdmin(),
		Enabled:  func(id string) bool { return m.available(ctx, id, s.ctx.SpaceID) },
	})
	api.WriteJSON(w, http.StatusOK, out)
}

func return400(w http.ResponseWriter, r *http.Request, m *Module, msg string) {
	m.fail(w, r, core.Invalid("kind", msg))
}

type interactionRequest struct {
	Context module.CommandContext `json:"context"`
	Values  map[string]any        `json:"values,omitempty"`
	Payload map[string]any        `json:"payload,omitempty"`
}

func decodeInteraction(r *http.Request) (interactionRequest, error) {
	var in interactionRequest
	if r.ContentLength == 0 {
		return in, nil
	}
	err := api.Decode(r, &in)
	return in, err
}

// allowed enforces the minimum access of a contribution. An actor with no
// access at all on a context item never reaches here, because resolve reads
// the item as not found.
func allowed(s *scope, min core.Access) error {
	if s.access < min {
		return fmt.Errorf("needs %s access: %w", min, core.ErrForbidden)
	}
	return nil
}

// command serves POST /api/commands/{id} {context}.
func (m *Module) command(w http.ResponseWriter, r *http.Request) {
	actor, err := person(r)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	ctx := r.Context()
	in, err := decodeInteraction(r)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	cmd, ok := m.opts.Registry.LookupCommand(r.PathValue("id"))
	if !ok {
		m.fail(w, r, fmt.Errorf("command: %w", core.ErrNotFound))
		return
	}
	s, err := m.resolve(ctx, actor, in.Context, nil)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	if !m.available(ctx, cmd.Module, s.ctx.SpaceID) {
		m.fail(w, r, fmt.Errorf("command: %w", core.ErrNotFound))
		return
	}
	if err := allowed(s, cmd.MinAccess); err != nil {
		m.fail(w, r, err)
		return
	}
	resp, err := cmd.Run(ctx, module.CommandRequest{Context: s.ctx, Values: in.Values, User: user(actor)})
	if err != nil {
		m.fail(w, r, err)
		return
	}
	m.respond(w, r, actor, resp)
}

// panel serves POST /api/panels/{id} {context} with {ui}.
func (m *Module) panel(w http.ResponseWriter, r *http.Request) {
	actor, err := person(r)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	ctx := r.Context()
	in, err := decodeInteraction(r)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	p, ok := m.opts.Registry.LookupPanel(r.PathValue("id"))
	if !ok {
		m.fail(w, r, fmt.Errorf("panel: %w", core.ErrNotFound))
		return
	}
	if p.Builtin != "" || p.Render == nil {
		m.fail(w, r, core.Invalid("id", "a built-in panel renders in the client"))
		return
	}
	s, err := m.resolve(ctx, actor, in.Context, nil)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	if !m.available(ctx, p.Module, s.ctx.SpaceID) {
		m.fail(w, r, fmt.Errorf("panel: %w", core.ErrNotFound))
		return
	}
	if err := allowed(s, p.MinAccess); err != nil {
		m.fail(w, r, err)
		return
	}
	node, err := p.Render(ctx, module.CommandRequest{Context: s.ctx, User: user(actor)})
	if err != nil {
		m.fail(w, r, err)
		return
	}
	if err := m.checkTree(ctx, actor, node); err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"ui": node})
}

// action serves POST /api/modules/{module}/actions/{id} {context, values, payload}.
func (m *Module) action(w http.ResponseWriter, r *http.Request) {
	actor, err := person(r)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	ctx := r.Context()
	in, err := decodeInteraction(r)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	moduleID, actionID := r.PathValue("module"), r.PathValue("id")
	if _, ok := m.opts.Registry.Module(moduleID); !ok || m.opts.Actions == nil || !module.ValidIdent(actionID) {
		m.fail(w, r, fmt.Errorf("action: %w", core.ErrNotFound))
		return
	}
	s, err := m.resolve(ctx, actor, in.Context, nil)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	if !m.available(ctx, moduleID, s.ctx.SpaceID) {
		m.fail(w, r, fmt.Errorf("action: %w", core.ErrNotFound))
		return
	}
	if in.Context.ItemID != "" || in.Context.SpaceID != "" {
		if err := allowed(s, core.AccessView); err != nil {
			m.fail(w, r, err)
			return
		}
	}
	resp, err := m.opts.Actions.RunAction(ctx, moduleID, actionID,
		module.CommandRequest{Context: s.ctx, Values: in.Values, User: user(actor)}, in.Payload)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	m.respond(w, r, actor, resp)
}

type importRequest struct {
	FileID  string `json:"fileId"`
	SpaceID string `json:"spaceId"`
}

// importFile serves POST /api/import/{importer} {fileId, spaceId}.
func (m *Module) importFile(w http.ResponseWriter, r *http.Request) {
	actor, err := person(r)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	ctx := r.Context()
	var in importRequest
	if err := api.Decode(r, &in); err != nil {
		m.fail(w, r, err)
		return
	}
	imp, ok := m.opts.Registry.LookupImporter(r.PathValue("importer"))
	if !ok {
		m.fail(w, r, fmt.Errorf("importer: %w", core.ErrNotFound))
		return
	}
	if in.FileID == "" || in.SpaceID == "" {
		m.fail(w, r, core.Invalid("fileId", "fileId and spaceId are required"))
		return
	}
	file, err := m.opts.Backend.GetItem(ctx, actor, in.FileID)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	if file.File == nil {
		m.fail(w, r, core.Invalid("fileId", "is not a file"))
		return
	}
	s, err := m.resolve(ctx, actor, module.CommandContext{SpaceID: in.SpaceID}, nil)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	if !m.available(ctx, imp.Module, in.SpaceID) {
		m.fail(w, r, fmt.Errorf("importer: %w", core.ErrNotFound))
		return
	}
	if err := allowed(s, max(imp.MinAccess, core.AccessView)); err != nil {
		m.fail(w, r, err)
		return
	}
	resp, err := imp.Import(ctx, module.ImportRequest{FileID: file.ID(), SpaceID: in.SpaceID, User: user(actor)})
	if err != nil {
		m.fail(w, r, err)
		return
	}
	m.respond(w, r, actor, resp)
}

// respond validates a command response and writes it.
func (m *Module) respond(w http.ResponseWriter, r *http.Request, actor *core.Actor, resp module.CommandResponse) {
	if err := resp.Validate(); err != nil {
		m.fail(w, r, fmt.Errorf("invalid response: %v: %w", err, core.ErrUnavailable))
		return
	}
	if resp.UI != nil {
		if err := m.checkTree(r.Context(), actor, *resp.UI); err != nil {
			m.fail(w, r, err)
			return
		}
	}
	if resp.OpenPanel != nil && resp.OpenPanel.ItemID != "" {
		if _, err := m.opts.Backend.GetItem(r.Context(), actor, resp.OpenPanel.ItemID); err != nil {
			m.fail(w, r, fmt.Errorf("the response opens a panel on an item you cannot view: %w", core.ErrUnavailable))
			return
		}
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(raw, '\n'))
}

// checkTree validates a UI tree and requires every item link to name an item
// the actor can view.
func (m *Module) checkTree(ctx context.Context, actor *core.Actor, node module.Node) error {
	if err := module.ValidateNode(node); err != nil {
		return fmt.Errorf("invalid UI tree: %v: %w", err, core.ErrUnavailable)
	}
	for _, id := range module.LinkItemIDs(node) {
		if _, err := m.opts.Backend.GetItem(ctx, actor, id); err != nil {
			if errors.Is(err, core.ErrNotFound) || errors.Is(err, core.ErrForbidden) {
				return fmt.Errorf("the UI links to an item you cannot view: %w", core.ErrUnavailable)
			}
			return err
		}
	}
	return nil
}
