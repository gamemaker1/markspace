package sdk

import (
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/gamemaker1/markspace/sdk/ui"
)

// ValidationError is the error type of every validator in this package and
// in package ui.
type ValidationError = ui.Error

// Response is a command, action or import response. It holds exactly one of
// toast, openPanel, navigate, job and ui.
type Response map[string]any

// Toast returns a response that shows a message. kind is info, success, warn
// or error. An empty kind means info.
func Toast(message, kind string) Response {
	t := map[string]any{"message": message}
	if kind != "" {
		t["kind"] = kind
	}
	return Response{"toast": t}
}

// OpenPanel returns a response that opens a panel. id is qualified, such as
// word-count.counts. An empty itemID is omitted.
func OpenPanel(id, itemID string) Response {
	p := map[string]any{"id": id}
	if itemID != "" {
		p["itemId"] = itemID
	}
	return Response{"openPanel": p}
}

// Navigate returns a response that opens a path inside Markspace.
func Navigate(path string) Response { return Response{"navigate": path} }

// JobResponse returns a response that points the client at a job.
func JobResponse(id string) Response { return Response{"job": map[string]any{"id": id}} }

// UIResponse returns a response that shows a UI tree.
func UIResponse(tree ui.Node) Response { return Response{"ui": tree} }

// MaxNavigateBytes is the longest navigate path.
const MaxNavigateBytes = 2048

var responseKinds = []string{"toast", "openPanel", "navigate", "job", "ui"}

func generic(v any) (any, error) {
	b, err := ui.Encode(v)
	if err != nil {
		return nil, err
	}
	var g any
	err = json.Unmarshal(b, &g)
	return g, err
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func checkUnknown(m map[string]any, path string, allowed ...string) error {
	for _, k := range sortedKeys(m) {
		if !contains(allowed, k) {
			return ui.Errorf(ui.CodeUnknownField, path+"."+k, "unknown field %q", k)
		}
	}
	return nil
}

// requireString returns the string at m[k]. It reports missing_field or
// invalid_field.
func requireString(m map[string]any, k, path string) (string, error) {
	v, ok := m[k]
	if !ok {
		return "", ui.Errorf(ui.CodeMissingField, path+"."+k, "missing %s", k)
	}
	s, ok := v.(string)
	if !ok {
		return "", ui.Errorf(ui.CodeInvalidField, path+"."+k, "%s must be a string", k)
	}
	return s, nil
}

// ValidNavigate reports whether s is a path inside Markspace that a navigate
// response may carry.
func ValidNavigate(s string) bool {
	if len(s) == 0 || len(s) > MaxNavigateBytes || s[0] != '/' {
		return false
	}
	if strings.HasPrefix(s, "//") || strings.HasPrefix(s, "/\\") {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return false
		}
	}
	return true
}

// ValidateCommandResponse checks a command, action or import response. It
// returns nil or a *ValidationError.
func ValidateCommandResponse(v any) error {
	g, err := generic(v)
	if err != nil {
		return ui.Errorf(ui.CodeNotObject, "$", "cannot encode response: %v", err)
	}
	m, ok := g.(map[string]any)
	if !ok {
		return ui.Errorf(ui.CodeNotObject, "$", "response must be an object")
	}
	if err := checkUnknown(m, "$", responseKinds...); err != nil {
		return err
	}
	switch len(m) {
	case 0:
		return ui.Errorf(ui.CodeEmptyResponse, "$", "response needs one of %s", strings.Join(responseKinds, ", "))
	case 1:
	default:
		return ui.Errorf(ui.CodeMultipleKinds, "$", "response holds %d kinds, want 1", len(m))
	}
	switch {
	case hasKey(m, "toast"):
		t, ok := m["toast"].(map[string]any)
		if !ok {
			return ui.Errorf(ui.CodeInvalidField, "$.toast", "toast must be an object")
		}
		if err := checkUnknown(t, "$.toast", "message", "kind"); err != nil {
			return err
		}
		msg, err := requireString(t, "message", "$.toast")
		if err != nil {
			return err
		}
		if msg == "" {
			return ui.Errorf(ui.CodeInvalidField, "$.toast.message", "message must not be empty")
		}
		if k, ok := t["kind"]; ok {
			ks, isStr := k.(string)
			if !isStr || !contains([]string{"info", "success", "warn", "error"}, ks) {
				return ui.Errorf(ui.CodeInvalidField, "$.toast.kind", "kind must be info, success, warn or error")
			}
		}
	case hasKey(m, "openPanel"):
		p, ok := m["openPanel"].(map[string]any)
		if !ok {
			return ui.Errorf(ui.CodeInvalidField, "$.openPanel", "openPanel must be an object")
		}
		if err := checkUnknown(p, "$.openPanel", "id", "itemId"); err != nil {
			return err
		}
		id, err := requireString(p, "id", "$.openPanel")
		if err != nil {
			return err
		}
		if !ValidQualifiedID(id) {
			return ui.Errorf(ui.CodeInvalidID, "$.openPanel.id", "panel id %q is not <pluginId>.<panelId>", id)
		}
		if _, ok := p["itemId"]; ok {
			item, err := requireString(p, "itemId", "$.openPanel")
			if err != nil {
				return err
			}
			if !ValidObjectID(item) {
				return ui.Errorf(ui.CodeInvalidID, "$.openPanel.itemId", "invalid item id %q", item)
			}
		}
	case hasKey(m, "navigate"):
		s, ok := m["navigate"].(string)
		if !ok {
			return ui.Errorf(ui.CodeInvalidField, "$.navigate", "navigate must be a string")
		}
		if !ValidNavigate(s) {
			return ui.Errorf(ui.CodeInvalidNav, "$.navigate", "navigate must be a path inside Markspace")
		}
	case hasKey(m, "job"):
		j, ok := m["job"].(map[string]any)
		if !ok {
			return ui.Errorf(ui.CodeInvalidField, "$.job", "job must be an object")
		}
		if err := checkUnknown(j, "$.job", "id"); err != nil {
			return err
		}
		id, err := requireString(j, "id", "$.job")
		if err != nil {
			return err
		}
		if !ValidObjectID(id) {
			return ui.Errorf(ui.CodeInvalidID, "$.job.id", "invalid job id %q", id)
		}
	case hasKey(m, "ui"):
		return ui.ValidateAt(m["ui"], "$.ui")
	}
	return nil
}

func hasKey(m map[string]any, k string) bool {
	_, ok := m[k]
	return ok
}

func hasSpaceOrControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f || unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

// ValidMarkspaceURL reports whether s is an http or https base URL with a
// host and no query or fragment.
func ValidMarkspaceURL(s string) bool {
	if hasSpaceOrControl(s) || strings.ContainsAny(s, "?#") {
		return false
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return (scheme == "http" || scheme == "https") && u.Hostname() != "" && u.RawQuery == "" && u.Fragment == ""
}

// ValidateInstall checks a POST /install body against the install rules in
// test/vectors/protocol.json. Unknown keys are ignored.
func ValidateInstall(body any, manifestID string) error {
	g, err := generic(body)
	if err != nil {
		return ui.Errorf(ui.CodeNotObject, "$", "cannot encode body: %v", err)
	}
	m, ok := g.(map[string]any)
	if !ok {
		return ui.Errorf(ui.CodeNotObject, "$", "install body must be an object")
	}
	id, err := requireString(m, "pluginId", "$")
	if err != nil {
		return err
	}
	if id != manifestID {
		return ui.Errorf(ui.CodeWrongPlugin, "$.pluginId", "install is for %q, this plugin is %q", id, manifestID)
	}
	u, err := requireString(m, "markspaceUrl", "$")
	if err != nil {
		return err
	}
	if !ValidMarkspaceURL(u) {
		return ui.Errorf(ui.CodeInvalidURL, "$.markspaceUrl", "markspaceUrl must be an http or https URL with a host")
	}
	tok, err := requireString(m, "token", "$")
	if err != nil {
		return err
	}
	if tok == "" || hasSpaceOrControl(tok) {
		return ui.Errorf(ui.CodeInvalidField, "$.token", "token must be non-empty with no whitespace")
	}
	sec, err := requireString(m, "signingSecret", "$")
	if err != nil {
		return err
	}
	if _, err := DecodeSecret(sec); err != nil {
		return ui.Errorf(ui.CodeInvalidSecret, "$.signingSecret", "%v", err)
	}
	if v, ok := m["grants"]; ok {
		grants, isArr := v.([]any)
		if !isArr {
			return ui.Errorf(ui.CodeInvalidField, "$.grants", "grants must be an array")
		}
		for i, gv := range grants {
			p := "$.grants[" + strconv.Itoa(i) + "]"
			gm, isObj := gv.(map[string]any)
			if !isObj {
				return ui.Errorf(ui.CodeInvalidField, p, "grant must be an object")
			}
			if _, err := requireString(gm, "spaceId", p); err != nil {
				return err
			}
			role, err := requireString(gm, "role", p)
			if err != nil {
				return err
			}
			if !contains([]string{"viewer", "commenter", "editor"}, role) {
				return ui.Errorf(ui.CodeInvalidField, p+".role", "role must be viewer, commenter or editor")
			}
		}
	}
	if v, ok := m["permissions"]; ok {
		perms, isArr := v.([]any)
		if !isArr {
			return ui.Errorf(ui.CodeInvalidField, "$.permissions", "permissions must be an array")
		}
		for i, pv := range perms {
			if _, isStr := pv.(string); !isStr {
				return ui.Errorf(ui.CodeInvalidField, "$.permissions["+strconv.Itoa(i)+"]", "permission must be a string")
			}
		}
	}
	return nil
}
