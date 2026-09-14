package plugin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/module"
	"github.com/gamemaker1/markspace/internal/modules/plugins"
	"github.com/gamemaker1/markspace/sdk"
)

// normalizeBaseURL checks a plugin base URL and removes a trailing slash. It
// allows private addresses, because plugins usually run next to Markspace.
func normalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", core.Invalid("baseUrl", "enter an http or https address, such as http://bookmark-wiki:8090")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", core.Invalid("baseUrl", "the address may not hold a user, a query or a fragment")
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host+u.EscapedPath(), "/"), nil
}

// fetchManifest downloads and validates a manifest. The raw bytes are
// returned so the stored manifest is exactly what the admin approved.
func (h *Host) fetchManifest(ctx context.Context, baseURL string) ([]byte, sdk.Manifest, error) {
	var man sdk.Manifest
	base, err := normalizeBaseURL(baseURL)
	if err != nil {
		return nil, man, err
	}
	ctx, cancel := context.WithTimeout(ctx, manifestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/manifest.json", nil)
	if err != nil {
		return nil, man, core.Invalid("baseUrl", "the address is not valid")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, man, fmt.Errorf("fetch manifest from %s: %v: %w", base, err, core.ErrUnavailable)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, man, fmt.Errorf("fetch manifest from %s: status %d: %w", base, resp.StatusCode, core.ErrUnavailable)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes+1))
	if err != nil {
		return nil, man, fmt.Errorf("read manifest: %v: %w", err, core.ErrUnavailable)
	}
	if len(raw) > maxManifestBytes {
		return nil, man, fmt.Errorf("the manifest is larger than 1 MiB: %w", core.ErrTooLarge)
	}
	man, err = sdk.ParseManifest(raw)
	if err != nil {
		return nil, man, core.Invalid("manifest", err.Error())
	}
	if err := h.validateModule(man); err != nil {
		return nil, man, err
	}
	return bytes.TrimSpace(raw), man, nil
}

func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// validateModule adds the plugin's module to a scratch registry, which
// checks icons, contexts, schedules and settings fields the way the real
// registry will.
func (h *Host) validateModule(man sdk.Manifest) error {
	if err := module.NewRegistry().Add(h.buildModule(man.ID, man)); err != nil {
		return core.Invalid("manifest", err.Error())
	}
	return nil
}

// itemContexts lists the command contexts that need access on an item.
var itemContexts = []string{"space", "note", "folder", "file", "selection", "link", "image"}

// defaultAccess is view for a contribution used on items, none for a global one.
func defaultAccess(raw string, contexts []string) core.Access {
	if a, ok := core.ParseAccess(strings.TrimSpace(raw)); ok && raw != "" {
		return min(a, core.AccessEdit)
	}
	for _, c := range contexts {
		if slices.Contains(itemContexts, c) {
			return core.AccessView
		}
	}
	return core.AccessNone
}

// scheduleJitter is a tenth of the period, at most one hour.
func scheduleJitter(every time.Duration) time.Duration {
	return min(every/10, time.Hour)
}

// buildModule adapts a manifest into a registry module. Commands, panels and
// importers need the ui permission.
func (h *Host) buildModule(id string, man sdk.Manifest) module.Module {
	return module.New(module.Manifest{
		ID: ModuleID(id), Name: man.Name, Version: man.Version, Permissions: slices.Clone(man.Permissions),
	}, func(r *module.Registry) error {
		ui := slices.Contains(man.Permissions, "ui")
		if ui {
			for _, c := range man.Commands {
				r.Command(module.Command{
					ID: id + "." + c.ID, Title: c.Title, Icon: c.Icon, Contexts: slices.Clone(c.Contexts),
					Shortcut: c.Shortcut, MinAccess: defaultAccess(c.MinAccess, c.Contexts), Run: h.commandProxy(id, c.ID),
				})
			}
			for _, p := range man.Panels {
				icon := p.Icon
				if icon == "" {
					icon = "puzzle"
				}
				r.Panel(module.Panel{
					ID: id + "." + p.ID, Title: p.Title, Icon: icon, Where: p.Where, Contexts: slices.Clone(p.Contexts),
					Order: 100 + p.Order, MinAccess: defaultAccess("", p.Contexts), Render: h.panelProxy(id, p.ID),
				})
			}
			for _, imp := range man.Importers {
				r.Importer(module.Importer{
					ID: id + "." + imp.ID, Title: imp.Title, Accept: slices.Clone(imp.Accept),
					MinAccess: core.AccessEdit, Import: h.importProxy(id, imp.ID),
				})
			}
		}
		for _, j := range man.Jobs {
			r.Job(JobKind(id, j.Kind), h.jobHandler(id, j.Kind), module.JobOptions{Title: j.Title, MaxAttempts: j.MaxAttempts})
		}
		for _, s := range man.Schedules {
			every, err := time.ParseDuration(s.Every)
			if err != nil {
				return fmt.Errorf("schedule %s: %w", s.Job, err)
			}
			r.Schedule(JobKind(id, s.Job), every, scheduleJitter(every))
		}
		if man.Settings != nil {
			raw, err := json.Marshal(man.Settings.Fields)
			if err != nil {
				return err
			}
			fields, err := module.ValidateFields(raw)
			if err != nil {
				return fmt.Errorf("settings: %w", err)
			}
			r.Settings(module.SettingsSchema{Fields: fields})
		}
		return nil
	})
}

// summary converts a manifest into the review shape of the admin page.
func summary(man sdk.Manifest) plugins.ManifestSummary {
	s := plugins.ManifestSummary{
		ID: man.ID, Name: man.Name, Version: man.Version, Description: man.Description, Homepage: man.Homepage,
		Permissions: nonNil(man.Permissions), Events: nonNil(man.Events),
		Jobs: []plugins.Contribution{}, Schedules: []plugins.Contribution{}, Commands: []plugins.Contribution{},
		Panels: []plugins.Contribution{}, Importers: []plugins.Contribution{}, Secrets: []plugins.SecretView{},
	}
	for _, j := range man.Jobs {
		s.Jobs = append(s.Jobs, plugins.Contribution{ID: j.Kind, Title: j.Title})
	}
	for _, sc := range man.Schedules {
		s.Schedules = append(s.Schedules, plugins.Contribution{ID: sc.Job, Title: "Every " + sc.Every, Extra: sc.Every})
	}
	for _, c := range man.Commands {
		s.Commands = append(s.Commands, plugins.Contribution{ID: c.ID, Title: c.Title, Extra: strings.Join(c.Contexts, ", ")})
	}
	for _, p := range man.Panels {
		s.Panels = append(s.Panels, plugins.Contribution{ID: p.ID, Title: p.Title, Extra: p.Where})
	}
	for _, i := range man.Importers {
		s.Importers = append(s.Importers, plugins.Contribution{ID: i.ID, Title: i.Title, Extra: strings.Join(i.Accept, ", ")})
	}
	for _, sec := range man.Secrets {
		s.Secrets = append(s.Secrets, plugins.SecretView{Name: sec.Name, Label: sec.Label})
	}
	if man.Settings != nil && len(man.Settings.Fields) > 0 {
		s.Settings, _ = json.Marshal(man.Settings.Fields)
	}
	return s
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return slices.Clone(list)
}

// asksForMore reports whether a new manifest asks for a permission, an event
// pattern or a secret the approved one lacks.
func asksForMore(old, next sdk.Manifest) bool {
	for _, p := range next.Permissions {
		if !slices.Contains(old.Permissions, p) {
			return true
		}
	}
	for _, e := range next.Events {
		if !slices.Contains(old.Events, e) {
			return true
		}
	}
	for _, s := range next.Secrets {
		if !slices.ContainsFunc(old.Secrets, func(o sdk.ManifestSecret) bool { return o.Name == s.Name }) {
			return true
		}
	}
	return false
}

// settingsFields lists the declared settings fields as name, type and
// required flag.
type settingsField struct {
	Name     string
	Type     string
	Required bool
}

func declaredSettings(man sdk.Manifest) []settingsField {
	if man.Settings == nil {
		return nil
	}
	var out []settingsField
	for _, f := range man.Settings.Fields {
		name, _ := f["name"].(string)
		typ, _ := f["type"].(string)
		req, _ := f["required"].(bool)
		if name != "" {
			out = append(out, settingsField{Name: name, Type: typ, Required: req})
		}
	}
	return out
}

// checkSettings accepts only declared setting names and requires required
// fields. It returns the settings as a JSON object.
func checkSettings(man sdk.Manifest, in map[string]any, partial bool) (json.RawMessage, error) {
	fields := declaredSettings(man)
	for k := range in {
		if !slices.ContainsFunc(fields, func(f settingsField) bool { return f.Name == k }) {
			return nil, core.Invalid("settings."+k, "the manifest declares no such setting")
		}
	}
	if !partial {
		for _, f := range fields {
			v, ok := in[f.Name]
			if f.Required && (!ok || v == nil || v == "") {
				return nil, core.Invalid("settings."+f.Name, "is required")
			}
		}
	}
	if in == nil {
		in = map[string]any{}
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, core.Invalid("settings", err.Error())
	}
	if len(raw) > 64<<10 {
		return nil, fmt.Errorf("settings are larger than 64 KiB: %w", core.ErrTooLarge)
	}
	return raw, nil
}
