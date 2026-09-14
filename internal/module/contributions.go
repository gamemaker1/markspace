package module

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/gamemaker1/markspace/internal/core"
)

// CommandContexts lists the contexts a command may declare.
var CommandContexts = []string{"global", "space", "note", "folder", "file", "selection", "link", "image"}

// PanelContexts lists the contexts a panel may declare.
var PanelContexts = []string{"note", "folder", "file", "space", "global"}

// PanelPlaces lists the values of Panel.Where.
var PanelPlaces = []string{"rail", "page"}

// ToastKinds lists the values of Toast.Kind. An empty kind means info.
var ToastKinds = []string{"info", "success", "warn", "error"}

var (
	shortcutPattern = regexp.MustCompile(`^((Mod|Ctrl|Alt|Shift|Meta)-)*([A-Za-z0-9]|F[1-9]|F1[0-2]|Enter|Escape|Space|Tab|Backspace|Delete|ArrowUp|ArrowDown|ArrowLeft|ArrowRight)$`)
	acceptPattern   = regexp.MustCompile(`^(\.[a-z0-9]{1,16}|[a-z]{1,32}/[a-z0-9.+*-]{1,64})$`)
)

// Actor is the user on whose behalf a contribution runs.
type Actor struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// CommandContext is the context the client sends with a command, panel or
// action request.
type CommandContext struct {
	SpaceID     string              `json:"spaceId,omitempty"`
	ItemID      string              `json:"itemId,omitempty"`
	Kind        core.ItemKind       `json:"kind,omitempty"`
	Selection   *core.CommentAnchor `json:"selection,omitempty"`
	Link        string              `json:"link,omitempty"`
	ImageFileID string              `json:"imageFileId,omitempty"`
}

// CommandRequest is one run of a command, a panel render or a panel action.
type CommandRequest struct {
	Context CommandContext `json:"context"`
	Values  map[string]any `json:"values,omitempty"`
	User    Actor          `json:"user"`
}

// ImportRequest asks an importer to read an uploaded file.
type ImportRequest struct {
	FileID  string `json:"fileId"`
	SpaceID string `json:"spaceId"`
	User    Actor  `json:"user"`
}

// CommandHandler runs a command. The plugin host supplies a handler that
// proxies to the plugin.
type CommandHandler func(ctx context.Context, req CommandRequest) (CommandResponse, error)

// PanelHandler returns the UI tree of a panel without a built-in renderer.
type PanelHandler func(ctx context.Context, req CommandRequest) (Node, error)

// ImportHandler starts an import.
type ImportHandler func(ctx context.Context, req ImportRequest) (CommandResponse, error)

// AIToolHandler runs one AI tool call with the model's JSON input.
type AIToolHandler func(ctx context.Context, user Actor, input json.RawMessage) (json.RawMessage, error)

// Command is an entry in menus and the command palette.
type Command struct {
	ID        string         `json:"id"`
	Module    string         `json:"module"`
	Title     string         `json:"title"`
	Icon      string         `json:"icon,omitempty"`
	Contexts  []string       `json:"contexts"`
	Shortcut  string         `json:"shortcut,omitempty"`
	MinAccess core.Access    `json:"minAccess"`
	Run       CommandHandler `json:"-"`
}

// Panel is a rail tab or a page.
type Panel struct {
	ID        string       `json:"id"`
	Module    string       `json:"module"`
	Title     string       `json:"title"`
	Icon      string       `json:"icon"`
	Where     string       `json:"where"`
	Contexts  []string     `json:"contexts"`
	Builtin   string       `json:"builtin,omitempty"`
	Order     int          `json:"order"`
	MinAccess core.Access  `json:"minAccess"`
	Render    PanelHandler `json:"-"`
}

// SettingsSchema is the settings form of a module.
type SettingsSchema struct {
	Module string  `json:"module"`
	Fields []Field `json:"fields"`
}

// Importer reads an uploaded file into a space.
type Importer struct {
	ID        string        `json:"id"`
	Module    string        `json:"module"`
	Title     string        `json:"title"`
	Accept    []string      `json:"accept"`
	MinAccess core.Access   `json:"minAccess"`
	Import    ImportHandler `json:"-"`
}

// AITool is a tool that AI runs may call.
type AITool struct {
	Name        string          `json:"name"`
	Module      string          `json:"module"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	MinAccess   core.Access     `json:"minAccess"`
	Call        AIToolHandler   `json:"-"`
}

// ContributionFilter selects the contributions shown to one actor.
type ContributionFilter struct {
	// Access is the actor's access on the current item or space. Use
	// core.AccessNone for the global context.
	Access core.Access
	// Contexts are the current contexts, such as ["note", "selection"]. A
	// command or panel passes when it declares at least one of them. An empty
	// list skips the context check.
	Contexts []string
	// Enabled reports whether a module is enabled. Nil enables every module.
	Enabled func(moduleID string) bool
	// Admin includes settings schemas.
	Admin bool
}

// Contributions is the response of GET /api/ui/contributions.
type Contributions struct {
	Commands  []Command        `json:"commands"`
	Panels    []Panel          `json:"panels"`
	Importers []Importer       `json:"importers"`
	AITools   []AITool         `json:"aiTools"`
	Settings  []SettingsSchema `json:"settings,omitempty"`
}

func (f ContributionFilter) allows(module string, min core.Access, contexts []string) bool {
	if f.Enabled != nil && !f.Enabled(module) {
		return false
	}
	if f.Access < min {
		return false
	}
	if len(f.Contexts) == 0 || contexts == nil {
		return true
	}
	for _, c := range contexts {
		if slices.Contains(f.Contexts, c) {
			return true
		}
	}
	return false
}

func validAccess(a core.Access) bool { return a >= core.AccessNone && a <= core.AccessManage }

func validContexts(field string, contexts, allowed []string) error {
	if len(contexts) == 0 {
		return core.Invalid(field, "at least one context is required")
	}
	for i, c := range contexts {
		if !slices.Contains(allowed, c) {
			return core.Invalid(field, fmt.Sprintf("unknown context %q", c))
		}
		if slices.Contains(contexts[:i], c) {
			return core.Invalid(field, fmt.Sprintf("duplicate context %q", c))
		}
	}
	return nil
}

func validTitle(field, s string) error {
	if strings.TrimSpace(s) == "" {
		return core.Invalid(field, "must not be empty")
	}
	if len(s) > 200 {
		return core.Invalid(field, "is longer than 200 bytes")
	}
	return nil
}

// Validate checks the command fields.
func (c Command) Validate() error {
	switch {
	case !ValidIdent(c.ID):
		return core.Invalid("id", fmt.Sprintf("invalid command id %q", c.ID))
	case c.Icon != "" && !iconPattern.MatchString(c.Icon):
		return core.Invalid("icon", fmt.Sprintf("invalid icon %q", c.Icon))
	case c.Shortcut != "" && !shortcutPattern.MatchString(c.Shortcut):
		return core.Invalid("shortcut", fmt.Sprintf("invalid shortcut %q", c.Shortcut))
	case !validAccess(c.MinAccess):
		return core.Invalid("minAccess", "unknown access level")
	case c.Run == nil:
		return core.Invalid("run", "a command needs a handler")
	}
	if err := validTitle("title", c.Title); err != nil {
		return err
	}
	return validContexts("contexts", c.Contexts, CommandContexts)
}

// Validate checks the panel fields.
func (p Panel) Validate() error {
	switch {
	case !ValidIdent(p.ID):
		return core.Invalid("id", fmt.Sprintf("invalid panel id %q", p.ID))
	case !iconPattern.MatchString(p.Icon):
		return core.Invalid("icon", fmt.Sprintf("invalid icon %q", p.Icon))
	case !slices.Contains(PanelPlaces, p.Where):
		return core.Invalid("where", fmt.Sprintf("unknown place %q", p.Where))
	case p.Builtin != "" && !ValidIdent(p.Builtin):
		return core.Invalid("builtin", fmt.Sprintf("invalid renderer name %q", p.Builtin))
	case p.Builtin == "" && p.Render == nil:
		return core.Invalid("render", "a panel needs a built-in renderer or a render handler")
	case !validAccess(p.MinAccess):
		return core.Invalid("minAccess", "unknown access level")
	}
	if err := validTitle("title", p.Title); err != nil {
		return err
	}
	return validContexts("contexts", p.Contexts, PanelContexts)
}

// Validate checks the importer fields.
func (i Importer) Validate() error {
	switch {
	case !ValidIdent(i.ID):
		return core.Invalid("id", fmt.Sprintf("invalid importer id %q", i.ID))
	case len(i.Accept) == 0:
		return core.Invalid("accept", "at least one extension or media type is required")
	case !validAccess(i.MinAccess):
		return core.Invalid("minAccess", "unknown access level")
	case i.Import == nil:
		return core.Invalid("import", "an importer needs a handler")
	}
	for _, a := range i.Accept {
		if !acceptPattern.MatchString(a) {
			return core.Invalid("accept", fmt.Sprintf("invalid extension or media type %q", a))
		}
	}
	return validTitle("title", i.Title)
}

// Validate checks the AI tool fields. An empty InputSchema is valid and the
// registry replaces it with {"type":"object"}.
func (t AITool) Validate() error {
	switch {
	case !ValidToolName(t.Name):
		return core.Invalid("name", fmt.Sprintf("invalid tool name %q", t.Name))
	case strings.TrimSpace(t.Description) == "":
		return core.Invalid("description", "must not be empty")
	case len(t.Description) > 4096:
		return core.Invalid("description", "is longer than 4096 bytes")
	case !validAccess(t.MinAccess):
		return core.Invalid("minAccess", "unknown access level")
	case t.Call == nil:
		return core.Invalid("call", "an AI tool needs a handler")
	}
	if len(t.InputSchema) > 0 {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(t.InputSchema, &obj); err != nil || obj == nil {
			return core.Invalid("inputSchema", "must be a JSON object")
		}
	}
	return nil
}

// Validate checks the settings fields with the declarative UI field rules.
func (s SettingsSchema) Validate() error {
	_, err := canonicalFields(s.Fields)
	return err
}

// canonicalFields validates fields through their JSON form and returns the
// canonical copy.
func canonicalFields(fields []Field) ([]Field, error) {
	if fields == nil {
		fields = []Field{}
	}
	raw, err := marshalCanonical(fields)
	if err != nil {
		return nil, core.Invalid("fields", err.Error())
	}
	return ValidateFields(raw)
}

// Toast is a short message shown by the client.
type Toast struct {
	Message string `json:"message"`
	Kind    string `json:"kind,omitempty"`
}

// OpenPanel asks the client to open a panel.
type OpenPanel struct {
	ID     string `json:"id"`
	ItemID string `json:"itemId,omitempty"`
}

// JobRef names a job the client can follow.
type JobRef struct {
	ID string `json:"id"`
}

// CommandResponse is the response to a command, panel action or import. Exactly
// one field is set.
type CommandResponse struct {
	Toast     *Toast     `json:"toast,omitempty"`
	OpenPanel *OpenPanel `json:"openPanel,omitempty"`
	Navigate  string     `json:"navigate,omitempty"`
	Job       *JobRef    `json:"job,omitempty"`
	UI        *Node      `json:"ui,omitempty"`
}

// responseKinds lists the keys of a command response.
var responseKinds = []string{"toast", "openPanel", "navigate", "job", "ui"}

// Validate checks that exactly one field is set and that the set field is
// valid, with the command response rules in test/vectors/protocol.json. A
// panel id is qualified, as in "bookmark-wiki.wiki". Item and job ids use
// ValidObjectID. A UI tree is checked with ValidateUI through its JSON form.
func (r CommandResponse) Validate() error {
	set := 0
	for _, ok := range []bool{r.Toast != nil, r.OpenPanel != nil, r.Navigate != "", r.Job != nil, r.UI != nil} {
		if ok {
			set++
		}
	}
	if set != 1 {
		return core.Invalid("response", fmt.Sprintf("exactly one of toast, openPanel, navigate, job or ui must be set, got %d", set))
	}
	switch {
	case r.Toast != nil:
		if r.Toast.Message == "" {
			return core.Invalid("toast.message", "must not be empty")
		}
		if r.Toast.Kind != "" && !slices.Contains(ToastKinds, r.Toast.Kind) {
			return core.Invalid("toast.kind", fmt.Sprintf("unknown kind %q", r.Toast.Kind))
		}
	case r.OpenPanel != nil:
		if !ValidQualifiedID(r.OpenPanel.ID) {
			return core.Invalid("openPanel.id", fmt.Sprintf("invalid panel id %q", r.OpenPanel.ID))
		}
		if r.OpenPanel.ItemID != "" && !ValidObjectID(r.OpenPanel.ItemID) {
			return core.Invalid("openPanel.itemId", "invalid item id")
		}
	case r.Navigate != "":
		if !validNavigate(r.Navigate) {
			return core.Invalid("navigate", "must be an application path starting with a single slash")
		}
	case r.Job != nil:
		if !ValidObjectID(r.Job.ID) {
			return core.Invalid("job.id", "invalid job id")
		}
	case r.UI != nil:
		if err := ValidateNode(*r.UI); err != nil {
			return err
		}
	}
	return nil
}

// ParseCommandResponse decodes and validates a command response received from
// a plugin. Unknown keys are rejected before the kinds are counted, and a UI
// tree is validated with ValidateUI with error paths under $.ui and stored in
// its canonical form.
func ParseCommandResponse(raw json.RawMessage) (CommandResponse, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil || top == nil {
		return CommandResponse{}, core.Invalid("response", "must be a JSON object")
	}
	keys := sortedKeys(top)
	for _, k := range keys {
		if !slices.Contains(responseKinds, k) {
			return CommandResponse{}, core.Invalid(k, "unknown field")
		}
	}
	switch len(keys) {
	case 0:
		return CommandResponse{}, core.Invalid("response", "one of toast, openPanel, navigate, job or ui is required")
	case 1:
	default:
		return CommandResponse{}, core.Invalid("response", "only one of toast, openPanel, navigate, job or ui may be set")
	}
	var (
		r   CommandResponse
		err error
	)
	key, v := keys[0], top[keys[0]]
	switch key {
	case "toast":
		r.Toast = &Toast{}
		err = strictObject(v, r.Toast)
	case "openPanel":
		r.OpenPanel = &OpenPanel{}
		err = strictObject(v, r.OpenPanel)
	case "navigate":
		if err = json.Unmarshal(v, &r.Navigate); err == nil && !validNavigate(r.Navigate) {
			err = fmt.Errorf("must be an application path starting with a single slash")
		}
	case "job":
		r.Job = &JobRef{}
		err = strictObject(v, r.Job)
	case "ui":
		n, uerr := validateUIAt(v, (*uiPath)(nil).field("ui"))
		if uerr != nil {
			return CommandResponse{}, uerr
		}
		r.UI = &n
	}
	if err != nil {
		return CommandResponse{}, core.Invalid(key, err.Error())
	}
	return r, r.Validate()
}

// strictObject decodes a JSON object and rejects unknown keys.
func strictObject(raw json.RawMessage, v any) error {
	if jsonKind(raw) != '{' {
		return fmt.Errorf("must be an object")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("trailing data")
	}
	return nil
}

// validNavigate accepts an application path of 1 to 2048 bytes that starts
// with a slash, not with // or /\, and holds no control characters.
func validNavigate(s string) bool {
	if len(s) == 0 || len(s) > 2048 || s[0] != '/' || strings.HasPrefix(s, "//") || strings.HasPrefix(s, "/\\") {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return false
		}
	}
	return true
}
