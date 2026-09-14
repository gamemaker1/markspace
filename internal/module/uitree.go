package module

// Declarative UI validation.
//
// ValidateUI checks a UI tree against the node table in docs/plugins.md and the UI
// rules in test/vectors/protocol.json, which sdk and the Python
// SDK follow too. The server validates plugin UI with ValidateUI. Error codes
// and paths match that file. internal/module/testdata/ui-tree.json adds cases
// only a server sees, such as input that is not JSON and repeated keys.
//
// Validation runs in three passes and reports the first error only:
//
//  1. Size. Raw input over 4 MiB is refused before decoding. The compact JSON
//     encoding with no HTML escaping must be at most 1 MiB. The input must be
//     UTF-8 JSON.
//  2. Limits. Every JSON object in the tree is visited in pre-order, object
//     keys in ascending byte order and array elements in index order. Every
//     object counts as a node, as docs/plugins.md states: nodes, fields, options,
//     columns, list items, badges, rows, actions, link targets and payload
//     objects. The depth of an object is the number of objects on the path
//     from the root to it, both included. At each object the pass reports
//     too_deep above depth 32, then too_many_nodes above 2000 objects.
//  3. Structure, depth-first. At each UI node, field, list item and row it
//     checks the JSON type, repeated keys, the node type, unknown keys in
//     ascending byte order, then each declared key in table order. A missing
//     required key is reported when its turn comes. Payload contents are only
//     checked for repeated keys.
//
// The canonical form written by MarshalUI must also fit in 1 MiB, so an
// accepted tree always validates again.
//
// Error codes. Each error carries a JSON path such as $.children[0].title and
// the matching JSON pointer /children/0/title:
//
//	too_large       the input exceeds 4 MiB, or its compact encoding or its
//	                canonical form exceeds 1 MiB
//	invalid_json    the input is not UTF-8 JSON, or a number is out of range
//	duplicate_key   an object repeats a key
//	too_deep        an object lies deeper than 32 objects
//	too_many_nodes  the object that would be number 2001
//	not_object      a node, field, list item or table row is not an object
//	unknown_type    a node has no type, a type that is not a string, or an
//	                unknown type
//	unknown_field   a key the object does not define
//	missing_field   a required key is absent
//	invalid_field   a value has the wrong JSON type or breaks a range or enum
//	                rule. Also a button slot that holds another known node
//	                type, a link target without exactly one of itemId and url,
//	                a badge type other than badge, and an empty field name
//	invalid_id      a form id, action id, job id or item id has the wrong format
//	invalid_url     a link url is not an absolute http or https URL with a host
//	duplicate_name  a field name repeats across fields and more
//
// invalid_json and duplicate_key cannot occur in trees the SDKs build, so the
// shared vectors do not use them.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gamemaker1/markspace/internal/core"
)

// UI tree limits.
const (
	MaxUINodes = 2000
	MaxUIBytes = 1 << 20
	MaxUIDepth = 32
	MaxUIInput = 4 << 20
)

// UI validation error codes.
const (
	UITooLarge      = "too_large"
	UIInvalidJSON   = "invalid_json"
	UIDuplicateKey  = "duplicate_key"
	UITooDeep       = "too_deep"
	UITooManyNodes  = "too_many_nodes"
	UINotObject     = "not_object"
	UIUnknownType   = "unknown_type"
	UIUnknownField  = "unknown_field"
	UIMissingField  = "missing_field"
	UIInvalidField  = "invalid_field"
	UIInvalidID     = "invalid_id"
	UIInvalidURL    = "invalid_url"
	UIDuplicateName = "duplicate_name"
)

// UIErrorCodes lists every UI validation error code.
var UIErrorCodes = []string{
	UITooLarge, UIInvalidJSON, UIDuplicateKey, UITooDeep, UITooManyNodes, UINotObject,
	UIUnknownType, UIUnknownField, UIMissingField, UIInvalidField, UIInvalidID, UIInvalidURL, UIDuplicateName,
}

// UIError reports the first problem found in a UI tree.
type UIError struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Pointer string `json:"pointer"`
	Message string `json:"message"`
}

func (e *UIError) Error() string {
	return fmt.Sprintf("ui %s at %s: %s", e.Code, e.Path, e.Message)
}

// Unwrap maps too_large to core.ErrTooLarge and every other code to
// core.ErrInvalid.
func (e *UIError) Unwrap() error {
	if e.Code == UITooLarge {
		return core.ErrTooLarge
	}
	return core.ErrInvalid
}

// Enumerations used by nodes and fields.
var (
	UIGaps       = []string{"s", "m", "l"}
	UITones      = []string{"neutral", "good", "warn", "bad", "accent"}
	UIVariants   = []string{"primary", "secondary", "danger"}
	UIFieldTypes = []string{"text", "textarea", "number", "url", "select", "checkbox", "file", "note", "folder", "space"}
)

// uiFieldOrder lists the keys of each node type in check and output order.
var uiFieldOrder = map[string][]string{
	"stack":    {"children", "gap"},
	"section":  {"title", "description", "count", "children"},
	"form":     {"id", "title", "description", "fields", "submit", "action", "more"},
	"list":     {"items", "empty"},
	"table":    {"columns", "rows", "rowActions"},
	"text":     {"value"},
	"markdown": {"value"},
	"progress": {"value", "max", "label", "jobId"},
	"badge":    {"label", "tone"},
	"button":   {"label", "variant", "action", "confirm"},
	"link":     {"label", "to"},
	"empty":    {"title", "body", "action"},
	"code":     {"value", "language"},
}

// uiRequired lists the required keys of each node type.
var uiRequired = map[string][]string{
	"stack":    {"children"},
	"section":  {"title"},
	"form":     {"id", "fields", "action"},
	"list":     {"items"},
	"table":    {"columns", "rows"},
	"text":     {"value"},
	"markdown": {"value"},
	"progress": {"value"},
	"badge":    {"label"},
	"button":   {"label", "action"},
	"link":     {"label", "to"},
	"empty":    {"title"},
	"code":     {"value"},
}

var (
	uiFieldKeys  = []string{"type", "name", "label", "hint", "value", "required", "options"}
	uiOptionKeys = []string{"value", "label"}
	uiItemKeys   = []string{"title", "subtitle", "icon", "link", "badge", "actions"}
	uiBadgeKeys  = []string{"type", "label", "tone"}
	uiColumnKeys = []string{"key", "label", "numeric"}
	uiTargetKeys = []string{"itemId", "url"}
	uiActionKeys = []string{"id", "payload"}
)

// Node is one node of a declarative UI tree. Which fields a node carries
// depends on Type, as the table in docs/plugins.md lists. Value holds a string for
// text, markdown and code, and a float64 for progress. Action is the action
// of a button or form. Button is the action of an empty node, which is a
// button node. Node encodes to its canonical JSON form. Decode trees with
// ValidateUI, not json.Unmarshal.
type Node struct {
	Type        string           `json:"type"`
	ID          string           `json:"id,omitempty"`
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description,omitempty"`
	Body        string           `json:"body,omitempty"`
	Label       string           `json:"label,omitempty"`
	Count       *int64           `json:"count,omitempty"`
	Gap         string           `json:"gap,omitempty"`
	Children    []Node           `json:"children,omitempty"`
	Fields      []Field          `json:"fields,omitempty"`
	Submit      string           `json:"submit,omitempty"`
	Action      *Action          `json:"action,omitempty"`
	Button      *Node            `json:"-"`
	More        []Field          `json:"more,omitempty"`
	Items       []ListItem       `json:"items,omitempty"`
	Empty       string           `json:"empty,omitempty"`
	Columns     []Column         `json:"columns,omitempty"`
	Rows        []map[string]any `json:"rows,omitempty"`
	RowActions  []Node           `json:"rowActions,omitempty"`
	Value       any              `json:"value,omitempty"`
	Max         *float64         `json:"max,omitempty"`
	JobID       string           `json:"jobId,omitempty"`
	Tone        string           `json:"tone,omitempty"`
	Variant     string           `json:"variant,omitempty"`
	Confirm     string           `json:"confirm,omitempty"`
	To          *LinkTarget      `json:"to,omitempty"`
	Language    string           `json:"language,omitempty"`
}

// Field is one input of a form or settings schema. Value is a string, a
// float64, a bool or nil.
type Field struct {
	Type     string   `json:"type"`
	Name     string   `json:"name"`
	Label    string   `json:"label,omitempty"`
	Hint     string   `json:"hint,omitempty"`
	Value    any      `json:"value,omitempty"`
	Required bool     `json:"required,omitempty"`
	Options  []Option `json:"options,omitempty"`
}

// Option is one choice of a select field.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// ListItem is one row of a list node. Actions are button nodes.
type ListItem struct {
	Title    string      `json:"title"`
	Subtitle string      `json:"subtitle,omitempty"`
	Icon     string      `json:"icon,omitempty"`
	Link     *LinkTarget `json:"link,omitempty"`
	Badge    *Badge      `json:"badge,omitempty"`
	Actions  []Node      `json:"actions,omitempty"`
}

// Badge is the badge of a list item. It has the fields of a badge node. The
// input may also carry "type": "badge", which the canonical form omits.
type Badge struct {
	Label string `json:"label"`
	Tone  string `json:"tone,omitempty"`
}

// Column is one column of a table node.
type Column struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Numeric bool   `json:"numeric,omitempty"`
}

// Action is posted to POST /api/modules/{module}/actions/{id}. Payload numbers
// are float64.
type Action struct {
	ID      string         `json:"id"`
	Payload map[string]any `json:"payload,omitempty"`
}

// LinkTarget is an item or an http or https URL. Exactly one field is set.
type LinkTarget struct {
	ItemID string `json:"itemId,omitempty"`
	URL    string `json:"url,omitempty"`
}

// NodeTypes returns the node type names in byte order.
func NodeTypes() []string {
	return sortedKeys(uiFieldOrder)
}

// canonBuf writes a JSON object member by member with no HTML escaping.
type canonBuf struct {
	buf bytes.Buffer
	n   int
	err error
}

func (c *canonBuf) member(key string, v any) {
	if c.err != nil {
		return
	}
	if c.n == 0 {
		c.buf.WriteByte('{')
	} else {
		c.buf.WriteByte(',')
	}
	c.n++
	kb, err := marshalCanonical(key)
	if err != nil {
		c.err = err
		return
	}
	vb, err := marshalCanonical(v)
	if err != nil {
		c.err = err
		return
	}
	c.buf.Write(kb)
	c.buf.WriteByte(':')
	c.buf.Write(vb)
}

func (c *canonBuf) bytes() ([]byte, error) {
	if c.n == 0 {
		c.buf.WriteByte('{')
	}
	c.buf.WriteByte('}')
	return c.buf.Bytes(), c.err
}

// MarshalJSON writes the canonical form: type first, then the keys of the
// node type in table order. Required keys are always written. Optional keys
// are written when they hold a value.
func (n Node) MarshalJSON() ([]byte, error) {
	c := &canonBuf{}
	c.member("type", n.Type)
	for _, name := range uiFieldOrder[n.Type] {
		if v, ok := n.member(name); ok {
			c.member(name, v)
		}
	}
	return c.bytes()
}

func (n *Node) member(name string) (any, bool) {
	req := slices.Contains(uiRequired[n.Type], name)
	text := func(s string) (any, bool) { return s, req || s != "" }
	switch name {
	case "id":
		return text(n.ID)
	case "title":
		return text(n.Title)
	case "description":
		return text(n.Description)
	case "body":
		return text(n.Body)
	case "label":
		return text(n.Label)
	case "gap":
		return text(n.Gap)
	case "submit":
		return text(n.Submit)
	case "empty":
		return text(n.Empty)
	case "jobId":
		return text(n.JobID)
	case "tone":
		return text(n.Tone)
	case "variant":
		return text(n.Variant)
	case "confirm":
		return text(n.Confirm)
	case "language":
		return text(n.Language)
	case "count":
		return n.Count, n.Count != nil
	case "max":
		return n.Max, n.Max != nil
	case "to":
		return n.To, n.To != nil
	case "action":
		if n.Type == "empty" {
			return n.Button, n.Button != nil
		}
		return n.Action, n.Action != nil
	case "children":
		return slot(n.Children, req)
	case "fields":
		return slot(n.Fields, req)
	case "more":
		return slot(n.More, req)
	case "items":
		return slot(n.Items, req)
	case "columns":
		return slot(n.Columns, req)
	case "rows":
		return slot(n.Rows, req)
	case "rowActions":
		return slot(n.RowActions, req)
	case "value":
		if n.Type == "progress" {
			if f, ok := toFloat(n.Value); ok {
				return f, true
			}
		}
		return n.Value, n.Value != nil
	}
	return nil, false
}

func slot[T any](s []T, required bool) (any, bool) {
	if len(s) == 0 {
		if required {
			return []T{}, true
		}
		return nil, false
	}
	return s, true
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	}
	return 0, false
}

// MarshalJSON writes the canonical form of a field. A select field always
// carries options.
func (f Field) MarshalJSON() ([]byte, error) {
	c := &canonBuf{}
	c.member("type", f.Type)
	c.member("name", f.Name)
	if f.Label != "" {
		c.member("label", f.Label)
	}
	if f.Hint != "" {
		c.member("hint", f.Hint)
	}
	if f.Value != nil {
		c.member("value", f.Value)
	}
	if f.Required {
		c.member("required", true)
	}
	if f.Type == "select" || len(f.Options) > 0 {
		opts := f.Options
		if opts == nil {
			opts = []Option{}
		}
		c.member("options", opts)
	}
	return c.bytes()
}

// ValidateUI decodes and validates a UI tree and returns it in typed form.
// The error is a *UIError.
func ValidateUI(raw json.RawMessage) (Node, error) {
	return validateUIAt(raw, nil)
}

func validateUIAt(raw json.RawMessage, root *uiPath) (Node, error) {
	if e := checkUIInput(raw, root); e != nil {
		return Node{}, e
	}
	v := &uiValidator{}
	n, e := v.node(raw, root, "")
	if e != nil {
		return Node{}, e
	}
	out, err := MarshalUI(n)
	if err != nil {
		return Node{}, uiErr(UIInvalidJSON, root, "%v", err)
	}
	if len(out) > MaxUIBytes {
		return Node{}, uiErr(UITooLarge, root, "the canonical tree is %d bytes, the limit is %d", len(out), MaxUIBytes)
	}
	return n, nil
}

// checkUIInput runs the size pass and the limits pass.
func checkUIInput(raw json.RawMessage, root *uiPath) *UIError {
	if len(raw) > MaxUIInput {
		return uiErr(UITooLarge, root, "the input is %d bytes, the limit is %d", len(raw), MaxUIInput)
	}
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return uiErr(UIInvalidJSON, root, "the input is not valid UTF-8 JSON")
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return uiErr(UIInvalidJSON, root, "%v", err)
	}
	enc, err := marshalCanonical(generic)
	if err != nil {
		return uiErr(UIInvalidJSON, root, "%v", err)
	}
	if len(enc) > MaxUIBytes {
		return uiErr(UITooLarge, root, "the tree is %d bytes, the limit is %d", len(enc), MaxUIBytes)
	}
	count := 0
	return checkUILimits(generic, root, 0, &count)
}

// checkUILimits visits every object in pre-order, keys in ascending byte
// order, and applies the depth and node limits.
func checkUILimits(v any, p *uiPath, depth int, count *int) *UIError {
	switch x := v.(type) {
	case map[string]any:
		depth++
		if depth > MaxUIDepth {
			return uiErr(UITooDeep, p, "depth %d exceeds %d", depth, MaxUIDepth)
		}
		*count++
		if *count > MaxUINodes {
			return uiErr(UITooManyNodes, p, "the tree has more than %d nodes", MaxUINodes)
		}
		for _, k := range sortedKeys(x) {
			switch x[k].(type) {
			case map[string]any, []any:
				if e := checkUILimits(x[k], p.field(k), depth, count); e != nil {
					return e
				}
			}
		}
	case []any:
		for i, el := range x {
			switch el.(type) {
			case map[string]any, []any:
				if e := checkUILimits(el, p.elem(i), depth, count); e != nil {
					return e
				}
			}
		}
	}
	return nil
}

// ValidateNode validates a tree built in Go through its canonical JSON form.
func ValidateNode(n Node) error {
	raw, err := MarshalUI(n)
	if err != nil {
		return &UIError{Code: UIInvalidJSON, Path: "$", Message: err.Error()}
	}
	_, err = ValidateUI(raw)
	return err
}

// ValidateFields validates a JSON array of form fields, such as the fields of
// a settings schema, with the field rules and the tree limits.
func ValidateFields(raw json.RawMessage) ([]Field, error) {
	if e := checkUIInput(raw, nil); e != nil {
		return nil, e
	}
	if jsonKind(raw) != '[' {
		return nil, uiErr(UIInvalidField, nil, "fields must be an array")
	}
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return nil, uiErr(UIInvalidJSON, nil, "%v", err)
	}
	v := &uiValidator{}
	names := map[string]bool{}
	out := []Field{}
	for i, el := range elems {
		f, e := v.field(el, (*uiPath)(nil).elem(i), names)
		if e != nil {
			return nil, e
		}
		out = append(out, f)
	}
	return out, nil
}

// MarshalUI writes the canonical JSON form of a tree: compact, keys in table
// order, and no HTML escaping.
func MarshalUI(n Node) ([]byte, error) {
	return marshalCanonical(n)
}

func marshalCanonical(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// LinkItemIDs returns the item ids of every link target in the tree, in tree
// order, so the caller can check that the actor may view each item.
func LinkItemIDs(n Node) []string {
	var out []string
	var walk func(n *Node)
	walk = func(n *Node) {
		if n.To != nil && n.To.ItemID != "" {
			out = append(out, n.To.ItemID)
		}
		for i := range n.Children {
			walk(&n.Children[i])
		}
		for _, it := range n.Items {
			if it.Link != nil && it.Link.ItemID != "" {
				out = append(out, it.Link.ItemID)
			}
		}
	}
	walk(&n)
	return out
}

// ValidLinkURL reports whether s is an absolute http or https URL with a
// host. The scheme is compared without case. Whitespace, control characters,
// backslashes and user information are refused.
func ValidLinkURL(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == '\\' || unicode.IsSpace(r) {
			return false
		}
	}
	u, err := url.Parse(s)
	if err != nil || u.User != nil || u.Opaque != "" {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return (scheme == "http" || scheme == "https") && u.Hostname() != ""
}

// uiPath is a location in the tree. A nil path is the root.
type uiPath struct {
	parent *uiPath
	key    string
	index  int
}

func (p *uiPath) field(k string) *uiPath { return &uiPath{parent: p, key: k, index: -1} }

func (p *uiPath) elem(i int) *uiPath { return &uiPath{parent: p, index: i} }

func (p *uiPath) chain() []*uiPath {
	var out []*uiPath
	for q := p; q != nil; q = q.parent {
		out = append(out, q)
	}
	slices.Reverse(out)
	return out
}

func (p *uiPath) jsonPath() string {
	var b strings.Builder
	b.WriteString("$")
	for _, s := range p.chain() {
		if s.index < 0 {
			b.WriteString(".")
			b.WriteString(s.key)
		} else {
			b.WriteString("[")
			b.WriteString(strconv.Itoa(s.index))
			b.WriteString("]")
		}
	}
	return b.String()
}

func (p *uiPath) pointer() string {
	var b strings.Builder
	for _, s := range p.chain() {
		b.WriteString("/")
		if s.index < 0 {
			b.WriteString(strings.ReplaceAll(strings.ReplaceAll(s.key, "~", "~0"), "/", "~1"))
		} else {
			b.WriteString(strconv.Itoa(s.index))
		}
	}
	return b.String()
}

func uiErr(code string, p *uiPath, format string, args ...any) *UIError {
	return &UIError{Code: code, Path: p.jsonPath(), Pointer: p.pointer(), Message: fmt.Sprintf(format, args...)}
}

func jsonKind(raw json.RawMessage) byte {
	raw = bytes.TrimLeft(raw, " \t\r\n")
	if len(raw) == 0 {
		return 0
	}
	switch c := raw[0]; c {
	case '{', '[', '"', 'n':
		return c
	case 't', 'f':
		return 'b'
	}
	return '0'
}

func kindName(k byte) string {
	switch k {
	case '{':
		return "an object"
	case '[':
		return "an array"
	case '"':
		return "a string"
	case 'n':
		return "null"
	case 'b':
		return "a boolean"
	}
	return "a number"
}

type member struct {
	key string
	raw json.RawMessage
}

func parseMembers(raw json.RawMessage, p *uiPath) ([]member, *UIError) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if _, err := dec.Token(); err != nil {
		return nil, uiErr(UIInvalidJSON, p, "%v", err)
	}
	var out []member
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, uiErr(UIInvalidJSON, p, "%v", err)
		}
		key, _ := tok.(string)
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return nil, uiErr(UIInvalidJSON, p, "%v", err)
		}
		if seen[key] {
			return nil, uiErr(UIDuplicateKey, p.field(key), "key %q repeats", key)
		}
		seen[key] = true
		out = append(out, member{key: key, raw: val})
	}
	return out, nil
}

// uiValidator runs the structure pass. The limits pass has already bounded
// the depth and the number of objects.
type uiValidator struct{}

type uiObject struct {
	members []member
	p       *uiPath
}

// object parses an object. notObject is the code for a value that is not an
// object.
func (v *uiValidator) object(raw json.RawMessage, p *uiPath, notObject, what string) (uiObject, *UIError) {
	if jsonKind(raw) != '{' {
		return uiObject{}, uiErr(notObject, p, "%s must be an object, got %s", what, kindName(jsonKind(raw)))
	}
	ms, e := parseMembers(raw, p)
	if e != nil {
		return uiObject{}, e
	}
	return uiObject{members: ms, p: p}, nil
}

func (o uiObject) get(k string) (json.RawMessage, bool) {
	for _, m := range o.members {
		if m.key == k {
			return m.raw, true
		}
	}
	return nil, false
}

func (o uiObject) has(k string) bool {
	_, ok := o.get(k)
	return ok
}

func (o uiObject) unknown(allowed []string) *UIError {
	keys := make([]string, 0, len(o.members))
	for _, m := range o.members {
		keys = append(keys, m.key)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !slices.Contains(allowed, k) {
			return uiErr(UIUnknownField, o.p.field(k), "unknown field %q", k)
		}
	}
	return nil
}

func (o uiObject) str(k string, required bool) (string, bool, *UIError) {
	raw, ok := o.get(k)
	if !ok {
		if required {
			return "", false, uiErr(UIMissingField, o.p.field(k), "missing %s", k)
		}
		return "", false, nil
	}
	if jsonKind(raw) != '"' {
		return "", true, uiErr(UIInvalidField, o.p.field(k), "%s must be a string, got %s", k, kindName(jsonKind(raw)))
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", true, uiErr(UIInvalidJSON, o.p.field(k), "%v", err)
	}
	return s, true, nil
}

func (o uiObject) enum(k string, values []string, required bool) (string, *UIError) {
	s, ok, e := o.str(k, required)
	if e != nil || !ok {
		return "", e
	}
	if !slices.Contains(values, s) {
		return "", uiErr(UIInvalidField, o.p.field(k), "%s must be one of %s", k, strings.Join(values, ", "))
	}
	return s, nil
}

func (o uiObject) boolean(k string) (bool, *UIError) {
	raw, ok := o.get(k)
	if !ok {
		return false, nil
	}
	if jsonKind(raw) != 'b' {
		return false, uiErr(UIInvalidField, o.p.field(k), "%s must be a boolean", k)
	}
	var b bool
	_ = json.Unmarshal(raw, &b)
	return b, nil
}

func (o uiObject) array(k string, required bool) ([]json.RawMessage, bool, *UIError) {
	raw, ok := o.get(k)
	if !ok {
		if required {
			return nil, false, uiErr(UIMissingField, o.p.field(k), "missing %s", k)
		}
		return nil, false, nil
	}
	if jsonKind(raw) != '[' {
		return nil, true, uiErr(UIInvalidField, o.p.field(k), "%s must be an array", k)
	}
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return nil, true, uiErr(UIInvalidJSON, o.p.field(k), "%v", err)
	}
	return elems, true, nil
}

func (o uiObject) number(k string, required bool) (float64, bool, *UIError) {
	raw, ok := o.get(k)
	if !ok {
		if required {
			return 0, false, uiErr(UIMissingField, o.p.field(k), "missing %s", k)
		}
		return 0, false, nil
	}
	f, isNum := numberValue(raw)
	if !isNum {
		return 0, true, uiErr(UIInvalidField, o.p.field(k), "%s must be a number", k)
	}
	return f, true, nil
}

func numberValue(raw json.RawMessage) (float64, bool) {
	if jsonKind(raw) != '0' {
		return 0, false
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, false
	}
	return f, true
}

// scalarValue decodes a string, number, boolean or null.
func scalarValue(raw json.RawMessage) (any, bool) {
	switch jsonKind(raw) {
	case '"':
		var s string
		err := json.Unmarshal(raw, &s)
		return s, err == nil
	case 'b':
		var b bool
		err := json.Unmarshal(raw, &b)
		return b, err == nil
	case 'n':
		return nil, true
	case '0':
		return numberValue(raw)
	}
	return nil, false
}

func (v *uiValidator) node(raw json.RawMessage, p *uiPath, only string) (Node, *UIError) {
	o, e := v.object(raw, p, UINotObject, "a node")
	if e != nil {
		return Node{}, e
	}
	var typ string
	if tr, ok := o.get("type"); ok && jsonKind(tr) == '"' {
		_ = json.Unmarshal(tr, &typ)
	}
	order, known := uiFieldOrder[typ]
	if !known {
		return Node{}, uiErr(UIUnknownType, p, "unknown node type %q", typ)
	}
	if only != "" && typ != only {
		return Node{}, uiErr(UIInvalidField, p, "expected a %s node, got %s", only, typ)
	}
	if e := o.unknown(append([]string{"type"}, order...)); e != nil {
		return Node{}, e
	}
	n := Node{Type: typ}
	if e := v.nodeFields(o, &n); e != nil {
		return Node{}, e
	}
	return n, nil
}

func (v *uiValidator) nodeFields(o uiObject, n *Node) *UIError {
	var e *UIError
	str := func(dst *string, k string, required bool) bool {
		if e != nil {
			return false
		}
		*dst, _, e = o.str(k, required)
		return e == nil
	}
	enum := func(dst *string, k string, values []string) bool {
		if e != nil {
			return false
		}
		*dst, e = o.enum(k, values, false)
		return e == nil
	}
	p := o.p
	switch n.Type {
	case "stack":
		if n.Children, e = v.nodes(o, "children", true, ""); e != nil {
			return e
		}
		enum(&n.Gap, "gap", UIGaps)
	case "section":
		if !str(&n.Title, "title", true) || !str(&n.Description, "description", false) {
			return e
		}
		f, ok, ne := o.number("count", false)
		if ne != nil {
			return ne
		}
		if ok {
			if f < 0 || f != math.Trunc(f) || f > 1<<53 {
				return uiErr(UIInvalidField, p.field("count"), "count must be an integer of at least 0")
			}
			c := int64(f)
			n.Count = &c
		}
		n.Children, e = v.nodes(o, "children", false, "")
	case "form":
		if !str(&n.ID, "id", true) {
			return e
		}
		if !ValidIdent(n.ID) {
			return uiErr(UIInvalidID, p.field("id"), "form id %q is not a valid id", n.ID)
		}
		if !str(&n.Title, "title", false) || !str(&n.Description, "description", false) {
			return e
		}
		names := map[string]bool{}
		if n.Fields, e = v.fields(o, "fields", true, names); e != nil {
			return e
		}
		if !str(&n.Submit, "submit", false) {
			return e
		}
		raw, ok := o.get("action")
		if !ok {
			return uiErr(UIMissingField, p.field("action"), "missing action")
		}
		if n.Action, e = v.action(raw, p.field("action")); e != nil {
			return e
		}
		n.More, e = v.fields(o, "more", false, names)
	case "text", "markdown":
		var s string
		if str(&s, "value", true) {
			n.Value = s
		}
	case "code":
		var s string
		if str(&s, "value", true) {
			n.Value = s
		}
		str(&n.Language, "language", false)
	case "progress":
		val, _, ne := o.number("value", true)
		if ne != nil {
			return ne
		}
		if val < 0 {
			return uiErr(UIInvalidField, p.field("value"), "value must be at least 0")
		}
		n.Value = val
		mx, hasMax, ne := o.number("max", false)
		if ne != nil {
			return ne
		}
		if hasMax && mx <= 0 {
			return uiErr(UIInvalidField, p.field("max"), "max must be above 0")
		}
		if hasMax && val > mx {
			return uiErr(UIInvalidField, p.field("value"), "value %v exceeds max %v", val, mx)
		}
		if hasMax {
			n.Max = &mx
		}
		if !str(&n.Label, "label", false) || !str(&n.JobID, "jobId", false) {
			return e
		}
		if o.has("jobId") && !ValidObjectID(n.JobID) {
			return uiErr(UIInvalidID, p.field("jobId"), "invalid job id %q", n.JobID)
		}
	case "badge":
		if str(&n.Label, "label", true) {
			enum(&n.Tone, "tone", UITones)
		}
	case "button":
		if !str(&n.Label, "label", true) || !enum(&n.Variant, "variant", UIVariants) {
			return e
		}
		raw, ok := o.get("action")
		if !ok {
			return uiErr(UIMissingField, p.field("action"), "missing action")
		}
		if n.Action, e = v.action(raw, p.field("action")); e != nil {
			return e
		}
		str(&n.Confirm, "confirm", false)
	case "link":
		if !str(&n.Label, "label", true) {
			return e
		}
		raw, ok := o.get("to")
		if !ok {
			return uiErr(UIMissingField, p.field("to"), "missing to")
		}
		n.To, e = v.target(raw, p.field("to"))
	case "empty":
		if !str(&n.Title, "title", true) || !str(&n.Body, "body", false) {
			return e
		}
		if raw, ok := o.get("action"); ok {
			var b Node
			if b, e = v.node(raw, p.field("action"), "button"); e != nil {
				return e
			}
			n.Button = &b
		}
	case "list":
		e = v.list(o, n)
	case "table":
		e = v.table(o, n)
	}
	return e
}

func (v *uiValidator) nodes(o uiObject, k string, required bool, only string) ([]Node, *UIError) {
	elems, _, e := o.array(k, required)
	if e != nil {
		return nil, e
	}
	var out []Node
	for i, el := range elems {
		c, e := v.node(el, o.p.field(k).elem(i), only)
		if e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, nil
}

func (v *uiValidator) fields(o uiObject, k string, required bool, names map[string]bool) ([]Field, *UIError) {
	elems, _, e := o.array(k, required)
	if e != nil {
		return nil, e
	}
	var out []Field
	for i, el := range elems {
		f, e := v.field(el, o.p.field(k).elem(i), names)
		if e != nil {
			return nil, e
		}
		out = append(out, f)
	}
	return out, nil
}

func (v *uiValidator) field(raw json.RawMessage, p *uiPath, names map[string]bool) (Field, *UIError) {
	o, e := v.object(raw, p, UINotObject, "a field")
	if e != nil {
		return Field{}, e
	}
	if e := o.unknown(uiFieldKeys); e != nil {
		return Field{}, e
	}
	var f Field
	if f.Type, e = o.enum("type", UIFieldTypes, true); e != nil {
		return Field{}, e
	}
	if f.Name, _, e = o.str("name", true); e != nil {
		return Field{}, e
	}
	if f.Name == "" {
		return Field{}, uiErr(UIInvalidField, p.field("name"), "name must not be empty")
	}
	if names[f.Name] {
		return Field{}, uiErr(UIDuplicateName, p.field("name"), "duplicate field name %q", f.Name)
	}
	names[f.Name] = true
	if f.Label, _, e = o.str("label", false); e != nil {
		return Field{}, e
	}
	if f.Hint, _, e = o.str("hint", false); e != nil {
		return Field{}, e
	}
	if vr, ok := o.get("value"); ok {
		val, ok := scalarValue(vr)
		if !ok {
			return Field{}, uiErr(UIInvalidField, p.field("value"), "value must be a string, number, boolean or null")
		}
		f.Value = val
	}
	if f.Required, e = o.boolean("required"); e != nil {
		return Field{}, e
	}
	isSelect := f.Type == "select"
	opts, has, e := o.array("options", isSelect)
	if e != nil {
		return Field{}, e
	}
	if has && !isSelect {
		return Field{}, uiErr(UIInvalidField, p.field("options"), "options is only allowed on select fields")
	}
	for i, or := range opts {
		oo, e := v.object(or, p.field("options").elem(i), UIInvalidField, "an option")
		if e != nil {
			return Field{}, e
		}
		if e := oo.unknown(uiOptionKeys); e != nil {
			return Field{}, e
		}
		var opt Option
		if opt.Value, _, e = oo.str("value", true); e != nil {
			return Field{}, e
		}
		if opt.Label, _, e = oo.str("label", true); e != nil {
			return Field{}, e
		}
		f.Options = append(f.Options, opt)
	}
	return f, nil
}

func (v *uiValidator) action(raw json.RawMessage, p *uiPath) (*Action, *UIError) {
	o, e := v.object(raw, p, UIInvalidField, "an action")
	if e != nil {
		return nil, e
	}
	if e := o.unknown(uiActionKeys); e != nil {
		return nil, e
	}
	a := &Action{}
	if a.ID, _, e = o.str("id", true); e != nil {
		return nil, e
	}
	if !ValidIdent(a.ID) {
		return nil, uiErr(UIInvalidID, p.field("id"), "action id %q is not a valid id", a.ID)
	}
	praw, ok := o.get("payload")
	if !ok {
		return a, nil
	}
	pp := p.field("payload")
	if jsonKind(praw) != '{' {
		return nil, uiErr(UIInvalidField, pp, "payload must be an object")
	}
	w := &payloadWalker{dec: json.NewDecoder(bytes.NewReader(praw))}
	w.dec.UseNumber()
	val, e := w.value(pp)
	if e != nil {
		return nil, e
	}
	if m, _ := val.(map[string]any); len(m) > 0 {
		a.Payload = m
	}
	return a, nil
}

// payloadWalker decodes an action payload in one pass over its tokens, so
// deeply nested arrays cost linear time. It refuses repeated keys.
type payloadWalker struct {
	dec *json.Decoder
}

func (w *payloadWalker) value(p *uiPath) (any, *UIError) {
	tok, err := w.dec.Token()
	if err != nil {
		return nil, uiErr(UIInvalidJSON, p, "%v", err)
	}
	switch t := tok.(type) {
	case json.Delim:
		if t == '[' {
			arr := []any{}
			for i := 0; w.dec.More(); i++ {
				el, e := w.value(p.elem(i))
				if e != nil {
					return nil, e
				}
				arr = append(arr, el)
			}
			_, _ = w.dec.Token()
			return arr, nil
		}
		m := map[string]any{}
		for w.dec.More() {
			kt, err := w.dec.Token()
			if err != nil {
				return nil, uiErr(UIInvalidJSON, p, "%v", err)
			}
			key, _ := kt.(string)
			if _, dup := m[key]; dup {
				return nil, uiErr(UIDuplicateKey, p.field(key), "key %q repeats", key)
			}
			el, e := w.value(p.field(key))
			if e != nil {
				return nil, e
			}
			m[key] = el
		}
		_, _ = w.dec.Token()
		return m, nil
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return nil, uiErr(UIInvalidJSON, p, "number out of range")
		}
		return f, nil
	}
	return tok, nil
}

func (v *uiValidator) target(raw json.RawMessage, p *uiPath) (*LinkTarget, *UIError) {
	o, e := v.object(raw, p, UIInvalidField, "a link target")
	if e != nil {
		return nil, e
	}
	if e := o.unknown(uiTargetKeys); e != nil {
		return nil, e
	}
	if o.has("itemId") == o.has("url") {
		return nil, uiErr(UIInvalidField, p, "a link target needs exactly one of itemId and url")
	}
	t := &LinkTarget{}
	if o.has("itemId") {
		if t.ItemID, _, e = o.str("itemId", true); e != nil {
			return nil, e
		}
		if !ValidObjectID(t.ItemID) {
			return nil, uiErr(UIInvalidID, p.field("itemId"), "invalid item id %q", t.ItemID)
		}
		return t, nil
	}
	if t.URL, _, e = o.str("url", true); e != nil {
		return nil, e
	}
	if !ValidLinkURL(t.URL) {
		return nil, uiErr(UIInvalidURL, p.field("url"), "a link url must be an absolute http or https URL")
	}
	return t, nil
}

func (v *uiValidator) badge(raw json.RawMessage, p *uiPath) (*Badge, *UIError) {
	o, e := v.object(raw, p, UIInvalidField, "a badge")
	if e != nil {
		return nil, e
	}
	if e := o.unknown(uiBadgeKeys); e != nil {
		return nil, e
	}
	if tr, ok := o.get("type"); ok {
		var typ string
		if jsonKind(tr) != '"' || json.Unmarshal(tr, &typ) != nil || typ != "badge" {
			return nil, uiErr(UIInvalidField, p.field("type"), "a list item badge has type badge")
		}
	}
	b := &Badge{}
	if b.Label, _, e = o.str("label", true); e != nil {
		return nil, e
	}
	if b.Tone, e = o.enum("tone", UITones, false); e != nil {
		return nil, e
	}
	return b, nil
}

func (v *uiValidator) list(o uiObject, n *Node) *UIError {
	items, _, e := o.array("items", true)
	if e != nil {
		return e
	}
	for i, raw := range items {
		ip := o.p.field("items").elem(i)
		io, e := v.object(raw, ip, UINotObject, "a list item")
		if e != nil {
			return e
		}
		if e := io.unknown(uiItemKeys); e != nil {
			return e
		}
		var it ListItem
		if it.Title, _, e = io.str("title", true); e != nil {
			return e
		}
		if it.Subtitle, _, e = io.str("subtitle", false); e != nil {
			return e
		}
		if it.Icon, _, e = io.str("icon", false); e != nil {
			return e
		}
		if lr, ok := io.get("link"); ok {
			if it.Link, e = v.target(lr, ip.field("link")); e != nil {
				return e
			}
		}
		if br, ok := io.get("badge"); ok {
			if it.Badge, e = v.badge(br, ip.field("badge")); e != nil {
				return e
			}
		}
		if it.Actions, e = v.nodes(io, "actions", false, "button"); e != nil {
			return e
		}
		n.Items = append(n.Items, it)
	}
	n.Empty, _, e = o.str("empty", false)
	return e
}

func (v *uiValidator) table(o uiObject, n *Node) *UIError {
	cols, _, e := o.array("columns", true)
	if e != nil {
		return e
	}
	for i, raw := range cols {
		co, e := v.object(raw, o.p.field("columns").elem(i), UIInvalidField, "a column")
		if e != nil {
			return e
		}
		if e := co.unknown(uiColumnKeys); e != nil {
			return e
		}
		var c Column
		if c.Key, _, e = co.str("key", true); e != nil {
			return e
		}
		if c.Label, _, e = co.str("label", true); e != nil {
			return e
		}
		if c.Numeric, e = co.boolean("numeric"); e != nil {
			return e
		}
		n.Columns = append(n.Columns, c)
	}
	rows, _, e := o.array("rows", true)
	if e != nil {
		return e
	}
	for i, raw := range rows {
		rp := o.p.field("rows").elem(i)
		ro, e := v.object(raw, rp, UINotObject, "a row")
		if e != nil {
			return e
		}
		// Sorting the members once keeps a row with many keys linear after the
		// sort. parseMembers has already refused repeated keys.
		cells := slices.Clone(ro.members)
		slices.SortFunc(cells, func(a, b member) int { return strings.Compare(a.key, b.key) })
		row := make(map[string]any, len(cells))
		for _, c := range cells {
			val, ok := scalarValue(c.raw)
			if !ok {
				return uiErr(UIInvalidField, rp.field(c.key), "row values must be strings, numbers, booleans or null")
			}
			row[c.key] = val
		}
		n.Rows = append(n.Rows, row)
	}
	n.RowActions, e = v.nodes(o, "rowActions", false, "button")
	return e
}
