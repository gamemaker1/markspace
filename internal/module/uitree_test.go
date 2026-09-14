package module

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// loadProtocolFile decodes test/vectors/protocol.json, the vectors shared
// with the plugin SDKs.
func loadProtocolFile(t testing.TB, v any) {
	t.Helper()
	raw, err := os.ReadFile("../../test/vectors/protocol.json")
	if err != nil {
		t.Fatalf("read shared plugin protocol vectors: %v", err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("decode shared plugin protocol vectors: %v", err)
	}
}

type uiVectorError struct {
	Code    string  `json:"code"`
	Path    string  `json:"path"`
	Pointer *string `json:"pointer"`
}

type uiVector struct {
	Name     string          `json:"name"`
	Tree     json.RawMessage `json:"tree"`
	Raw      *string         `json:"raw"`
	Generate *struct {
		Kind string `json:"kind"`
		N    int    `json:"n"`
	} `json:"generate"`
	Error *uiVectorError `json:"error"`
}

func (c uiVector) input(t testing.TB) []byte {
	t.Helper()
	switch {
	case c.Raw != nil:
		return []byte(*c.Raw)
	case c.Generate != nil:
		return generateUI(t, c.Generate.Kind, c.Generate.N)
	}
	return c.Tree
}

// generateUI builds the large trees both vector files describe by name.
func generateUI(t testing.TB, kind string, n int) []byte {
	t.Helper()
	obj := func(kv ...any) map[string]any {
		m := map[string]any{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}
	repeat := func(v func() any) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = v()
		}
		return out
	}
	var tree any
	switch kind {
	case "wide":
		tree = obj("type", "stack", "children", repeat(func() any { return obj("type", "text", "value", "x") }))
	case "wideList":
		tree = obj("type", "list", "items", repeat(func() any { return obj("title", "x") }))
	case "wideTable":
		tree = obj("type", "table", "columns", []any{obj("key", "a", "label", "A")},
			"rows", repeat(func() any { return obj("a", 1) }))
	case "wideButtons", "buttons":
		tree = obj("type", "stack", "children", repeat(func() any {
			return obj("type", "button", "label", "b", "action", obj("id", "a"))
		}))
	case "wideUnknown":
		tree = obj("type", "stack", "children", repeat(func() any { return obj("type", "nope") }))
	case "selectOptions":
		tree = obj("type", "form", "id", "f", "action", obj("id", "a"),
			"fields", []any{obj("type", "select", "name", "s",
				"options", repeat(func() any { return obj("value", "v", "label", "V") }))})
	case "deep":
		var node any = obj("type", "stack", "children", []any{})
		for i := 1; i < n; i++ {
			node = obj("type", "stack", "children", []any{node})
		}
		tree = node
	case "deepPayload":
		var p any = obj()
		for i := 1; i < n; i++ {
			p = obj("a", p)
		}
		tree = obj("type", "button", "label", "b", "action", obj("id", "a", "payload", p))
	case "bigText", "bigTextIndented":
		tree = obj("type", "text", "value", strings.Repeat("a", n))
	case "bigUnknown":
		tree = obj("type", "nope", "value", strings.Repeat("a", n))
	default:
		t.Fatalf("unknown generator %q", kind)
	}
	raw, err := marshalCanonical(tree)
	if err != nil {
		t.Fatal(err)
	}
	if kind == "bigTextIndented" {
		var buf bytes.Buffer
		if err := json.Indent(&buf, raw, "", "  "); err != nil {
			t.Fatal(err)
		}
		raw = buf.Bytes()
	}
	return raw
}

func loadOwnUIVectors(t testing.TB) []uiVector {
	t.Helper()
	raw, err := os.ReadFile("testdata/ui-tree.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases []uiVector `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Cases
}

func loadSharedUIVectors(t testing.TB) []uiVector {
	t.Helper()
	var doc struct {
		UI struct {
			Limits struct {
				MaxNodes int `json:"maxNodes"`
				MaxBytes int `json:"maxBytes"`
				MaxDepth int `json:"maxDepth"`
			} `json:"limits"`
			Cases []uiVector `json:"cases"`
		} `json:"ui"`
	}
	loadProtocolFile(t, &doc)
	l := doc.UI.Limits
	if l.MaxNodes != MaxUINodes || l.MaxBytes != MaxUIBytes || l.MaxDepth != MaxUIDepth {
		t.Fatalf("shared limits %+v differ from the package constants", l)
	}
	return doc.UI.Cases
}

// sharedUIDifferences lists shared cases where this package decides
// differently, with the result it gives instead. It is empty while the shared
// vectors and docs/plugins.md agree.
var sharedUIDifferences = map[string]*uiVectorError{}

func checkUICase(t *testing.T, c uiVector, want *uiVectorError) {
	t.Helper()
	n, err := ValidateUI(c.input(t))
	if want == nil {
		if err != nil {
			t.Fatalf("valid tree rejected: %v", err)
		}
		checkRoundTrip(t, n)
		return
	}
	var ue *UIError
	if !errors.As(err, &ue) {
		t.Fatalf("got %v, want %s at %s", err, want.Code, want.Path)
	}
	if ue.Code != want.Code || ue.Path != want.Path {
		t.Fatalf("got %s at %s (%s), want %s at %s", ue.Code, ue.Path, ue.Message, want.Code, want.Path)
	}
	if want.Pointer != nil && ue.Pointer != *want.Pointer {
		t.Fatalf("pointer %q, want %q", ue.Pointer, *want.Pointer)
	}
}

func checkRoundTrip(t testing.TB, n Node) {
	t.Helper()
	out, err := MarshalUI(n)
	if err != nil {
		t.Fatalf("marshal accepted tree: %v", err)
	}
	if len(out) > MaxUIBytes {
		t.Fatalf("canonical form of an accepted tree is %d bytes", len(out))
	}
	n2, err := ValidateUI(out)
	if err != nil {
		t.Fatalf("canonical form rejected: %v\n%s", err, truncate(string(out), 2000))
	}
	out2, err := MarshalUI(n2)
	if err != nil || !bytes.Equal(out, out2) {
		t.Fatalf("canonical form changed on the second pass:\n%s\n%s", truncate(string(out), 2000), truncate(string(out2), 2000))
	}
	if !reflect.DeepEqual(n, n2) {
		t.Fatalf("typed tree changed on the second pass:\n%#v\n%#v", n, n2)
	}
}

func TestUIVectors(t *testing.T) {
	cases := loadOwnUIVectors(t)
	if len(cases) < 15 {
		t.Fatalf("only %d cases loaded", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) { checkUICase(t, c, c.Error) })
	}
}

func TestUISharedVectors(t *testing.T) {
	cases := loadSharedUIVectors(t)
	if len(cases) < 90 {
		t.Fatalf("only %d shared cases loaded", len(cases))
	}
	used := map[string]bool{}
	for _, c := range cases {
		want := c.Error
		if d, ok := sharedUIDifferences[c.Name]; ok {
			want, used[c.Name] = d, true
		}
		t.Run(c.Name, func(t *testing.T) { checkUICase(t, c, want) })
	}
	for name := range sharedUIDifferences {
		if !used[name] {
			t.Errorf("difference %q names no shared case", name)
		}
	}
}

func TestMarshalUICanonical(t *testing.T) {
	ten := 10.0
	n := Node{Type: "stack", Children: []Node{
		{Type: "list", Items: []ListItem{{Title: "A & <B>", Link: &LinkTarget{ItemID: "n1"},
			Badge: &Badge{Label: "new", Tone: "accent"}}}},
		{Type: "empty", Title: "Nothing", Button: &Node{Type: "button", Label: "Add",
			Action: &Action{ID: "add", Payload: map[string]any{"k": 1.5}}}},
		{Type: "progress", Value: 3, Max: &ten},
		{Type: "section", Title: ""},
		{Type: "form", ID: "f", Fields: []Field{{Type: "select", Name: "name"}}, Action: &Action{ID: "save"}},
		{Type: "link", Label: "Site", To: &LinkTarget{URL: "https://example.com/?a=1&b=<2>"}},
	}}
	out, err := MarshalUI(n)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"stack","children":[` +
		`{"type":"list","items":[{"title":"A & <B>","link":{"itemId":"n1"},"badge":{"label":"new","tone":"accent"}}]},` +
		`{"type":"empty","title":"Nothing","action":{"type":"button","label":"Add","action":{"id":"add","payload":{"k":1.5}}}},` +
		`{"type":"progress","value":3,"max":10},` +
		`{"type":"section","title":""},` +
		`{"type":"form","id":"f","fields":[{"type":"select","name":"name","options":[]}],"action":{"id":"save"}},` +
		`{"type":"link","label":"Site","to":{"url":"https://example.com/?a=1&b=<2>"}}]}`
	if string(out) != want {
		t.Fatalf("canonical form:\n%s\nwant:\n%s", out, want)
	}
	if err := ValidateNode(n); err != nil {
		t.Fatal(err)
	}
	if ids := LinkItemIDs(n); !slices.Equal(ids, []string{"n1"}) {
		t.Fatalf("LinkItemIDs = %v", ids)
	}
	var ue *UIError
	if err := ValidateNode(Node{Type: "button", Label: "x"}); !errors.As(err, &ue) || ue.Code != UIMissingField || ue.Path != "$.action" {
		t.Fatalf("button without action: %v", err)
	}
	bad := Node{Type: "list", Items: []ListItem{{Title: "a", Actions: []Node{{Type: "text", Value: "x"}}}}}
	if err := ValidateNode(bad); !errors.As(err, &ue) || ue.Code != UIInvalidField || ue.Path != "$.items[0].actions[0]" {
		t.Fatalf("list item action that is not a button: %v", err)
	}
}

func TestValidateUIServerInput(t *testing.T) {
	cases := map[string]struct {
		raw  string
		code string
	}{
		"invalid UTF-8": {"{\"type\":\"text\",\"value\":\"\xff\"}", UIInvalidJSON},
		"empty input":   {"", UIInvalidJSON},
		"over 4 MiB":    {strings.Repeat(" ", MaxUIInput+1), UITooLarge},
	}
	for name, c := range cases {
		_, err := ValidateUI([]byte(c.raw))
		var ue *UIError
		if !errors.As(err, &ue) || ue.Code != c.code || ue.Path != "$" || ue.Pointer != "" {
			t.Errorf("%s: got %v, want %s at $", name, err, c.code)
		}
	}
	_, err := ValidateUI(generateUI(t, "bigText", MaxUIBytes))
	if !errors.Is(err, core.ErrTooLarge) {
		t.Fatalf("too_large does not unwrap to ErrTooLarge: %v", err)
	}
	_, err = ValidateUI([]byte(`{"type":"nope"}`))
	if !errors.Is(err, core.ErrInvalid) || errors.Is(err, core.ErrTooLarge) {
		t.Fatalf("unknown_type does not unwrap to ErrInvalid: %v", err)
	}
}

func TestValidateFields(t *testing.T) {
	fields, err := ValidateFields([]byte(`[{"type":"space","name":"space","label":"Wiki space","required":true},
		{"type":"select","name":"mode","options":[{"value":"a","label":"A"}]}]`))
	if err != nil || len(fields) != 2 || !fields[0].Required || fields[1].Options[0].Label != "A" {
		t.Fatalf("ValidateFields = %+v, %v", fields, err)
	}
	var ue *UIError
	_, err = ValidateFields([]byte(`[{"type":"text","name":"a"},{"type":"text","name":"a"}]`))
	if !errors.As(err, &ue) || ue.Code != UIDuplicateName || ue.Path != "$[1].name" || ue.Pointer != "/1/name" {
		t.Fatalf("duplicate name: %v", err)
	}
	if _, err := ValidateFields([]byte(`{}`)); !errors.As(err, &ue) || ue.Code != UIInvalidField {
		t.Fatalf("fields object: %v", err)
	}
}

// jsonObjectStats counts the objects of a JSON document and their deepest
// nesting, independently of the validator.
func jsonObjectStats(raw []byte) (objects, maxDepth int) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var stack []bool
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return objects, maxDepth
		}
		d, ok := tok.(json.Delim)
		if !ok {
			continue
		}
		switch d {
		case '{':
			objects++
			depth++
			maxDepth = max(maxDepth, depth)
			stack = append(stack, true)
		case '[':
			stack = append(stack, false)
		default:
			if stack[len(stack)-1] {
				depth--
			}
			stack = stack[:len(stack)-1]
		}
	}
}

func resolvePointer(doc any, ptr string) bool {
	if ptr == "" {
		return true
	}
	cur := doc
	for _, tok := range strings.Split(ptr[1:], "/") {
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
		switch v := cur.(type) {
		case map[string]any:
			next, ok := v[tok]
			if !ok {
				return false
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(v) {
				return false
			}
			cur = v[i]
		default:
			return false
		}
	}
	return true
}

func checkErrorLocation(t *testing.T, raw []byte, ue *UIError) {
	t.Helper()
	if !strings.HasPrefix(ue.Path, "$") {
		t.Fatalf("path %q does not start at $", ue.Path)
	}
	if ue.Code == UIInvalidJSON || ue.Code == UITooLarge {
		if ue.Path != "$" || ue.Pointer != "" {
			t.Fatalf("%s reported at %s", ue.Code, ue.Path)
		}
		return
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("%s reported for input that does not decode: %v", ue.Code, err)
	}
	if ue.Pointer != "" && !resolvePointer(doc, ue.Pointer[:strings.LastIndex(ue.Pointer, "/")]) {
		t.Fatalf("%s at %s: the parent of %q is not in the input", ue.Code, ue.Path, ue.Pointer)
	}
	if exists := resolvePointer(doc, ue.Pointer); exists == (ue.Code == UIMissingField) {
		t.Fatalf("%s at %s: pointer %q exists=%v", ue.Code, ue.Path, ue.Pointer, exists)
	}
}

func FuzzValidateUI(f *testing.F) {
	for _, c := range append(loadOwnUIVectors(f), loadSharedUIVectors(f)...) {
		if c.Generate == nil {
			f.Add(c.input(f))
		}
	}
	f.Add([]byte(`{"type":"stack","children":[{"type":"button","label":"b","action":{"id":"a","payload":{"x":[{"y":{}}],"n":1e3}}}]}`))
	f.Add([]byte(`{"type":"progress","value":-0,"max":1e-300,"jobId":"a"}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		n, err := ValidateUI(raw)
		if err != nil {
			var ue *UIError
			if !errors.As(err, &ue) {
				t.Fatalf("error %T %v is not a *UIError", err, err)
			}
			if !slices.Contains(UIErrorCodes, ue.Code) {
				t.Fatalf("undocumented code %q", ue.Code)
			}
			checkErrorLocation(t, raw, ue)
			return
		}
		objects, depth := jsonObjectStats(raw)
		if objects > MaxUINodes || depth > MaxUIDepth {
			t.Fatalf("accepted a tree with %d objects and depth %d", objects, depth)
		}
		var generic any
		if err := json.Unmarshal(raw, &generic); err != nil {
			t.Fatalf("accepted input that does not decode: %v", err)
		}
		if enc, _ := marshalCanonical(generic); len(enc) > MaxUIBytes {
			t.Fatalf("accepted a tree of %d bytes", len(enc))
		}
		checkRoundTrip(t, n)
	})
}
