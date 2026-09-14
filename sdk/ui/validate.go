// Package ui builds and validates the declarative UI trees that plugin
// panels, pages and command results return. docs/plugins.md lists the node types.
// test/vectors/protocol.json holds the validation rules and the cases this
// package must reproduce.
package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Tree limits from docs/plugins.md and the conformance vectors.
const (
	MaxNodes = 2000
	MaxBytes = 1 << 20
	MaxDepth = 32
)

// Validation error codes.
const (
	CodeNotObject      = "not_object"
	CodeUnknownType    = "unknown_type"
	CodeUnknownField   = "unknown_field"
	CodeMissingField   = "missing_field"
	CodeInvalidField   = "invalid_field"
	CodeInvalidID      = "invalid_id"
	CodeInvalidURL     = "invalid_url"
	CodeDuplicateName  = "duplicate_name"
	CodeTooManyNodes   = "too_many_nodes"
	CodeTooDeep        = "too_deep"
	CodeTooLarge       = "too_large"
	CodeEmptyResponse  = "empty_response"
	CodeMultipleKinds  = "multiple_kinds"
	CodeInvalidNav     = "invalid_navigate"
	CodeWrongPlugin    = "wrong_plugin"
	CodeInvalidSecret  = "invalid_secret"
	CodeInvalidPattern = "invalid_pattern"
)

// Error is a validation failure. Path is a JSON path such as
// $.children[0].title.
type Error struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s at %s: %s", e.Code, e.Path, e.Message)
}

// Errorf returns an *Error with a formatted message.
func Errorf(code, path, format string, args ...any) *Error {
	return &Error{Code: code, Path: path, Message: fmt.Sprintf(format, args...)}
}

var (
	localIDRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)
	objectIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
)

var fieldOrder = map[string][]string{
	"stack":    {"children", "gap"},
	"section":  {"title", "description", "count", "children"},
	"form":     {"id", "title", "description", "fields", "submit", "action", "more"},
	"text":     {"value"},
	"markdown": {"value"},
	"progress": {"value", "max", "label", "jobId"},
	"badge":    {"label", "tone"},
	"button":   {"label", "variant", "action", "confirm"},
	"link":     {"label", "to"},
	"empty":    {"title", "body", "action"},
	"code":     {"value", "language"},
	"list":     {"items", "empty"},
	"table":    {"columns", "rows", "rowActions"},
}

var (
	fieldKeys    = []string{"type", "name", "label", "hint", "value", "required", "options"}
	optionKeys   = []string{"value", "label"}
	badgeKeys    = []string{"type", "label", "tone"}
	itemKeys     = []string{"title", "subtitle", "icon", "link", "badge", "actions"}
	columnKeys   = []string{"key", "label", "numeric"}
	targetKeys   = []string{"itemId", "url"}
	actionKeys   = []string{"id", "payload"}
	gapEnum      = []string{"s", "m", "l"}
	toneEnum     = []string{"neutral", "good", "warn", "bad", "accent"}
	variantEnum  = []string{"primary", "secondary", "danger"}
	fieldTypeSet = []string{"text", "textarea", "number", "url", "select", "checkbox", "file", "note", "folder", "space"}
)

// Validate checks a UI tree. tree may be a Node, a decoded JSON value or any
// value that encodes to JSON. It returns nil or an *Error.
func Validate(tree any) error {
	return ValidateAt(tree, "$")
}

// ValidateJSON checks a UI tree given as JSON text.
func ValidateJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return Errorf(CodeNotObject, "$", "invalid JSON: %v", err)
	}
	return ValidateAt(v, "$")
}

// ValidateAt checks a UI tree and reports paths below root, such as $.ui.
func ValidateAt(tree any, root string) error {
	enc, err := Encode(tree)
	if err != nil {
		return Errorf(CodeInvalidField, root, "cannot encode tree: %v", err)
	}
	if len(enc) > MaxBytes {
		return Errorf(CodeTooLarge, root, "tree is %d bytes, the limit is %d", len(enc), MaxBytes)
	}
	var generic any
	if err := json.Unmarshal(enc, &generic); err != nil {
		return Errorf(CodeInvalidField, root, "cannot decode tree: %v", err)
	}
	if e := (&limiter{}).visit(generic, root, 0); e != nil {
		return e
	}
	w := &walker{}
	if e := w.node(generic, root, ""); e != nil {
		return e
	}
	return nil
}

// limiter enforces MaxDepth and MaxNodes over every JSON object in the tree.
// It visits objects in pre-order, keys in ascending byte order and array
// elements in index order. Arrays add no depth.
type limiter struct {
	count int
}

func (l *limiter) visit(v any, path string, depth int) *Error {
	switch t := v.(type) {
	case map[string]any:
		depth++
		if depth > MaxDepth {
			return Errorf(CodeTooDeep, path, "depth %d exceeds %d", depth, MaxDepth)
		}
		l.count++
		if l.count > MaxNodes {
			return Errorf(CodeTooManyNodes, path, "more than %d objects", MaxNodes)
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if e := l.visit(t[k], key(path, k), depth); e != nil {
				return e
			}
		}
	case []any:
		for i, e := range t {
			if err := l.visit(e, index(path, i), depth); err != nil {
				return err
			}
		}
	}
	return nil
}

// Encode returns the compact JSON encoding of v with no HTML escaping. The
// size limit applies to this encoding.
func Encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// walker checks the structure of a tree whose limits already passed.
type walker struct{}

func key(path, k string) string       { return path + "." + k }
func index(path string, i int) string { return path + "[" + strconv.Itoa(i) + "]" }

// counted returns v as an object. UI nodes, form fields, list items and
// table rows report not_object when they are not objects.
func (w *walker) counted(v any, path string) (map[string]any, *Error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, Errorf(CodeNotObject, path, "expected an object")
	}
	return m, nil
}

func unknownKeys(m map[string]any, path string, allowed ...[]string) *Error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		found := false
		for _, set := range allowed {
			for _, a := range set {
				if a == k {
					found = true
				}
			}
		}
		if !found {
			return Errorf(CodeUnknownField, key(path, k), "unknown field %q", k)
		}
	}
	return nil
}

// str checks an optional or required string field.
func str(m map[string]any, k, path string, required bool) (string, bool, *Error) {
	v, ok := m[k]
	if !ok {
		if required {
			return "", false, Errorf(CodeMissingField, key(path, k), "missing %s", k)
		}
		return "", false, nil
	}
	s, isStr := v.(string)
	if !isStr {
		return "", true, Errorf(CodeInvalidField, key(path, k), "%s must be a string", k)
	}
	return s, true, nil
}

func enum(m map[string]any, k, path string, values []string, required bool) *Error {
	s, ok, e := str(m, k, path, required)
	if e != nil || !ok {
		return e
	}
	for _, v := range values {
		if v == s {
			return nil
		}
	}
	return Errorf(CodeInvalidField, key(path, k), "%s must be one of %s", k, strings.Join(values, ", "))
}

func boolean(m map[string]any, k, path string) *Error {
	if v, ok := m[k]; ok {
		if _, isBool := v.(bool); !isBool {
			return Errorf(CodeInvalidField, key(path, k), "%s must be a boolean", k)
		}
	}
	return nil
}

func array(m map[string]any, k, path string, required bool) ([]any, bool, *Error) {
	v, ok := m[k]
	if !ok {
		if required {
			return nil, false, Errorf(CodeMissingField, key(path, k), "missing %s", k)
		}
		return nil, false, nil
	}
	a, isArr := v.([]any)
	if !isArr {
		return nil, true, Errorf(CodeInvalidField, key(path, k), "%s must be an array", k)
	}
	return a, true, nil
}

func number(m map[string]any, k, path string, required bool) (float64, bool, *Error) {
	v, ok := m[k]
	if !ok {
		if required {
			return 0, false, Errorf(CodeMissingField, key(path, k), "missing %s", k)
		}
		return 0, false, nil
	}
	f, isNum := v.(float64)
	if !isNum {
		return 0, true, Errorf(CodeInvalidField, key(path, k), "%s must be a number", k)
	}
	return f, true, nil
}

func isScalar(v any) bool {
	switch v.(type) {
	case nil, string, float64, bool:
		return true
	}
	return false
}

// node checks a UI node. only limits the slot to one node type when set.
func (w *walker) node(v any, path string, only string) *Error {
	m, e := w.counted(v, path)
	if e != nil {
		return e
	}
	typ, _ := m["type"].(string)
	order, known := fieldOrder[typ]
	if !known {
		return Errorf(CodeUnknownType, path, "unknown node type %q", typ)
	}
	if only != "" && typ != only {
		return Errorf(CodeInvalidField, path, "expected a %s node, got %s", only, typ)
	}
	if e := unknownKeys(m, path, []string{"type"}, order); e != nil {
		return e
	}
	switch typ {
	case "stack":
		if e := w.children(m, path, true); e != nil {
			return e
		}
		return enum(m, "gap", path, gapEnum, false)
	case "section":
		if _, _, e := str(m, "title", path, true); e != nil {
			return e
		}
		if _, _, e := str(m, "description", path, false); e != nil {
			return e
		}
		if f, ok, e := number(m, "count", path, false); e != nil {
			return e
		} else if ok && (f < 0 || f != math.Trunc(f)) {
			return Errorf(CodeInvalidField, key(path, "count"), "count must be an integer of at least 0")
		}
		return w.children(m, path, false)
	case "form":
		return w.form(m, path)
	case "text", "markdown":
		_, _, e := str(m, "value", path, true)
		return e
	case "code":
		if _, _, e := str(m, "value", path, true); e != nil {
			return e
		}
		_, _, e := str(m, "language", path, false)
		return e
	case "progress":
		return progress(m, path)
	case "badge":
		if _, _, e := str(m, "label", path, true); e != nil {
			return e
		}
		return enum(m, "tone", path, toneEnum, false)
	case "button":
		if _, _, e := str(m, "label", path, true); e != nil {
			return e
		}
		if e := enum(m, "variant", path, variantEnum, false); e != nil {
			return e
		}
		if _, ok := m["action"]; !ok {
			return Errorf(CodeMissingField, key(path, "action"), "missing action")
		}
		if e := action(m["action"], key(path, "action")); e != nil {
			return e
		}
		_, _, e := str(m, "confirm", path, false)
		return e
	case "link":
		if _, _, e := str(m, "label", path, true); e != nil {
			return e
		}
		to, ok := m["to"]
		if !ok {
			return Errorf(CodeMissingField, key(path, "to"), "missing to")
		}
		return target(to, key(path, "to"))
	case "empty":
		if _, _, e := str(m, "title", path, true); e != nil {
			return e
		}
		if _, _, e := str(m, "body", path, false); e != nil {
			return e
		}
		if a, ok := m["action"]; ok {
			return w.node(a, key(path, "action"), "button")
		}
		return nil
	case "list":
		return w.list(m, path)
	case "table":
		return w.table(m, path)
	}
	return nil
}

func (w *walker) children(m map[string]any, path string, required bool) *Error {
	a, _, e := array(m, "children", path, required)
	if e != nil {
		return e
	}
	for i, c := range a {
		if e := w.node(c, index(key(path, "children"), i), ""); e != nil {
			return e
		}
	}
	return nil
}

func (w *walker) buttons(m map[string]any, k, path string) *Error {
	a, _, e := array(m, k, path, false)
	if e != nil {
		return e
	}
	for i, c := range a {
		if e := w.node(c, index(key(path, k), i), "button"); e != nil {
			return e
		}
	}
	return nil
}

func (w *walker) form(m map[string]any, path string) *Error {
	id, _, e := str(m, "id", path, true)
	if e != nil {
		return e
	}
	if !localIDRe.MatchString(id) {
		return Errorf(CodeInvalidID, key(path, "id"), "form id %q does not match %s", id, localIDRe)
	}
	for _, k := range []string{"title", "description"} {
		if _, _, e := str(m, k, path, false); e != nil {
			return e
		}
	}
	names := map[string]bool{}
	if e := w.fields(m, "fields", path, true, names); e != nil {
		return e
	}
	if _, _, e := str(m, "submit", path, false); e != nil {
		return e
	}
	if _, ok := m["action"]; !ok {
		return Errorf(CodeMissingField, key(path, "action"), "missing action")
	}
	if e := action(m["action"], key(path, "action")); e != nil {
		return e
	}
	return w.fields(m, "more", path, false, names)
}

func (w *walker) fields(m map[string]any, k, path string, required bool, names map[string]bool) *Error {
	a, _, e := array(m, k, path, required)
	if e != nil {
		return e
	}
	for i, c := range a {
		if e := w.field(c, index(key(path, k), i), names); e != nil {
			return e
		}
	}
	return nil
}

func (w *walker) field(v any, path string, names map[string]bool) *Error {
	m, e := w.counted(v, path)
	if e != nil {
		return e
	}
	if e := unknownKeys(m, path, fieldKeys); e != nil {
		return e
	}
	if e := enum(m, "type", path, fieldTypeSet, true); e != nil {
		return e
	}
	name, _, e := str(m, "name", path, true)
	if e != nil {
		return e
	}
	if name == "" {
		return Errorf(CodeInvalidField, key(path, "name"), "name must not be empty")
	}
	if names[name] {
		return Errorf(CodeDuplicateName, key(path, "name"), "duplicate field name %q", name)
	}
	names[name] = true
	for _, k := range []string{"label", "hint"} {
		if _, _, e := str(m, k, path, false); e != nil {
			return e
		}
	}
	if val, ok := m["value"]; ok && !isScalar(val) {
		return Errorf(CodeInvalidField, key(path, "value"), "value must be a string, number, boolean or null")
	}
	if e := boolean(m, "required", path); e != nil {
		return e
	}
	isSelect := m["type"] == "select"
	opts, ok, e := array(m, "options", path, isSelect)
	if e != nil {
		return e
	}
	if ok && !isSelect {
		return Errorf(CodeInvalidField, key(path, "options"), "options is only allowed on select fields")
	}
	for i, o := range opts {
		op := index(key(path, "options"), i)
		om, isObj := o.(map[string]any)
		if !isObj {
			return Errorf(CodeInvalidField, op, "option must be an object")
		}
		if e := unknownKeys(om, op, optionKeys); e != nil {
			return e
		}
		for _, k := range optionKeys {
			if _, _, e := str(om, k, op, true); e != nil {
				return e
			}
		}
	}
	return nil
}

func progress(m map[string]any, path string) *Error {
	value, _, e := number(m, "value", path, true)
	if e != nil {
		return e
	}
	if value < 0 {
		return Errorf(CodeInvalidField, key(path, "value"), "value must be at least 0")
	}
	max, hasMax, e := number(m, "max", path, false)
	if e != nil {
		return e
	}
	if hasMax && max <= 0 {
		return Errorf(CodeInvalidField, key(path, "max"), "max must be above 0")
	}
	if hasMax && value > max {
		return Errorf(CodeInvalidField, key(path, "value"), "value %v exceeds max %v", value, max)
	}
	if _, _, e := str(m, "label", path, false); e != nil {
		return e
	}
	jobID, ok, e := str(m, "jobId", path, false)
	if e != nil {
		return e
	}
	if ok && !objectIDRe.MatchString(jobID) {
		return Errorf(CodeInvalidID, key(path, "jobId"), "invalid job id %q", jobID)
	}
	return nil
}

func action(v any, path string) *Error {
	m, ok := v.(map[string]any)
	if !ok {
		return Errorf(CodeInvalidField, path, "action must be an object")
	}
	if e := unknownKeys(m, path, actionKeys); e != nil {
		return e
	}
	id, _, e := str(m, "id", path, true)
	if e != nil {
		return e
	}
	if !localIDRe.MatchString(id) {
		return Errorf(CodeInvalidID, key(path, "id"), "action id %q does not match %s", id, localIDRe)
	}
	if p, ok := m["payload"]; ok {
		if _, isObj := p.(map[string]any); !isObj {
			return Errorf(CodeInvalidField, key(path, "payload"), "payload must be an object")
		}
	}
	return nil
}

func target(v any, path string) *Error {
	m, ok := v.(map[string]any)
	if !ok {
		return Errorf(CodeInvalidField, path, "link target must be an object")
	}
	if e := unknownKeys(m, path, targetKeys); e != nil {
		return e
	}
	_, hasItem := m["itemId"]
	_, hasURL := m["url"]
	if hasItem == hasURL {
		return Errorf(CodeInvalidField, path, "link target needs exactly one of itemId and url")
	}
	if hasItem {
		id, _, e := str(m, "itemId", path, true)
		if e != nil {
			return e
		}
		if !objectIDRe.MatchString(id) {
			return Errorf(CodeInvalidID, key(path, "itemId"), "invalid item id %q", id)
		}
		return nil
	}
	u, _, e := str(m, "url", path, true)
	if e != nil {
		return e
	}
	if !ValidLinkURL(u) {
		return Errorf(CodeInvalidURL, key(path, "url"), "link url must be an absolute http or https URL")
	}
	return nil
}

// ValidLinkURL reports whether s is an http or https URL with a host and no
// whitespace or control characters.
func ValidLinkURL(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f || unicode.IsSpace(r) {
			return false
		}
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return (scheme == "http" || scheme == "https") && u.Hostname() != ""
}

func (w *walker) list(m map[string]any, path string) *Error {
	items, _, e := array(m, "items", path, true)
	if e != nil {
		return e
	}
	for i, it := range items {
		ip := index(key(path, "items"), i)
		im, e := w.counted(it, ip)
		if e != nil {
			return e
		}
		if e := unknownKeys(im, ip, itemKeys); e != nil {
			return e
		}
		for _, k := range []string{"title", "subtitle", "icon"} {
			if _, _, e := str(im, k, ip, k == "title"); e != nil {
				return e
			}
		}
		if l, ok := im["link"]; ok {
			if e := target(l, key(ip, "link")); e != nil {
				return e
			}
		}
		if b, ok := im["badge"]; ok {
			if e := itemBadge(b, key(ip, "badge")); e != nil {
				return e
			}
		}
		if e := w.buttons(im, "actions", ip); e != nil {
			return e
		}
	}
	_, _, e = str(m, "empty", path, false)
	return e
}

// itemBadge checks a list item badge {type?, label, tone}. type is optional
// and must be badge when present.
func itemBadge(v any, path string) *Error {
	m, ok := v.(map[string]any)
	if !ok {
		return Errorf(CodeInvalidField, path, "badge must be an object")
	}
	if e := unknownKeys(m, path, badgeKeys); e != nil {
		return e
	}
	if t, ok := m["type"]; ok && t != "badge" {
		return Errorf(CodeInvalidField, key(path, "type"), "badge type must be badge")
	}
	if _, _, e := str(m, "label", path, true); e != nil {
		return e
	}
	return enum(m, "tone", path, toneEnum, false)
}

func (w *walker) table(m map[string]any, path string) *Error {
	cols, _, e := array(m, "columns", path, true)
	if e != nil {
		return e
	}
	for i, c := range cols {
		cp := index(key(path, "columns"), i)
		cm, ok := c.(map[string]any)
		if !ok {
			return Errorf(CodeInvalidField, cp, "column must be an object")
		}
		if e := unknownKeys(cm, cp, columnKeys); e != nil {
			return e
		}
		for _, k := range []string{"key", "label"} {
			if _, _, e := str(cm, k, cp, true); e != nil {
				return e
			}
		}
		if e := boolean(cm, "numeric", cp); e != nil {
			return e
		}
	}
	rows, _, e := array(m, "rows", path, true)
	if e != nil {
		return e
	}
	for i, r := range rows {
		rp := index(key(path, "rows"), i)
		rm, e := w.counted(r, rp)
		if e != nil {
			return e
		}
		keys := make([]string, 0, len(rm))
		for k := range rm {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !isScalar(rm[k]) {
				return Errorf(CodeInvalidField, key(rp, k), "row values must be strings, numbers, booleans or null")
			}
		}
	}
	return w.buttons(m, "rowActions", path)
}
