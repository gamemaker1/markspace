package ui

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

type vectorError struct {
	Code string `json:"code"`
	Path string `json:"path"`
}

type uiCase struct {
	Name     string          `json:"name"`
	Tree     json.RawMessage `json:"tree"`
	Generate *struct {
		Kind string `json:"kind"`
		N    int    `json:"n"`
	} `json:"generate"`
	Error *vectorError `json:"error"`
}

func loadUICases(t *testing.T) []uiCase {
	t.Helper()
	raw, err := os.ReadFile("../../test/vectors/protocol.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		UI struct {
			Limits struct {
				MaxNodes, MaxBytes, MaxDepth int
			} `json:"limits"`
			Cases []uiCase `json:"cases"`
		} `json:"ui"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	l := doc.UI.Limits
	if l.MaxNodes != MaxNodes || l.MaxBytes != MaxBytes || l.MaxDepth != MaxDepth {
		t.Fatalf("limits in vectors %+v differ from package constants", l)
	}
	return doc.UI.Cases
}

// generate builds the large trees the vector file describes by name.
func generate(kind string, n int) any {
	repeat := func(v func() any) []any {
		c := make([]any, n)
		for i := range c {
			c[i] = v()
		}
		return c
	}
	switch kind {
	case "wide":
		return map[string]any{"type": "stack", "children": repeat(func() any { return map[string]any{"type": "text", "value": "x"} })}
	case "wideUnknown":
		return map[string]any{"type": "stack", "children": repeat(func() any { return map[string]any{"type": "nope"} })}
	case "wideList":
		return map[string]any{"type": "list", "items": repeat(func() any { return map[string]any{"title": "x"} })}
	case "wideTable":
		return map[string]any{"type": "table", "columns": []any{map[string]any{"key": "a", "label": "A"}},
			"rows": repeat(func() any { return map[string]any{"a": 1} })}
	case "buttons":
		return map[string]any{"type": "stack", "children": repeat(func() any {
			return map[string]any{"type": "button", "label": "b", "action": map[string]any{"id": "a"}}
		})}
	case "selectOptions":
		return map[string]any{"type": "form", "id": "f", "action": map[string]any{"id": "a"},
			"fields": []any{map[string]any{"type": "select", "name": "s",
				"options": repeat(func() any { return map[string]any{"value": "v", "label": "V"} })}}}
	case "deep":
		var node any = map[string]any{"type": "stack", "children": []any{}}
		for i := 1; i < n; i++ {
			node = map[string]any{"type": "stack", "children": []any{node}}
		}
		return node
	case "deepPayload":
		var payload any = map[string]any{}
		for i := 1; i < n; i++ {
			payload = map[string]any{"a": payload}
		}
		return map[string]any{"type": "button", "label": "b", "action": map[string]any{"id": "a", "payload": payload}}
	case "bigText":
		return map[string]any{"type": "text", "value": strings.Repeat("a", n)}
	case "bigUnknown":
		return map[string]any{"type": "nope", "value": strings.Repeat("a", n)}
	}
	panic("unknown generator " + kind)
}

func TestVectors(t *testing.T) {
	cases := loadUICases(t)
	if len(cases) < 50 {
		t.Fatalf("only %d ui cases loaded", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var err error
			if c.Generate != nil {
				err = Validate(generate(c.Generate.Kind, c.Generate.N))
			} else {
				err = ValidateJSON(c.Tree)
			}
			checkErr(t, err, c.Error)
		})
	}
}

func checkErr(t *testing.T, err error, want *vectorError) {
	t.Helper()
	if want == nil {
		if err != nil {
			t.Fatalf("want valid, got %v", err)
		}
		return
	}
	var ve *Error
	if !errors.As(err, &ve) {
		t.Fatalf("want %s at %s, got %v", want.Code, want.Path, err)
	}
	if ve.Code != want.Code || ve.Path != want.Path {
		t.Fatalf("want %s at %s, got %s at %s (%s)", want.Code, want.Path, ve.Code, ve.Path, ve.Message)
	}
}

func TestBuilders(t *testing.T) {
	tree := Stack(
		Section("Counts",
			Text("412 words"),
			Markdown("**bold**"),
			Code("x", "go"),
			Progress(3).With("max", 10).With("label", "Counting"),
			Badge("ok", "good"),
		).With("count", 5),
		Form("settings", Act("save", map[string]any{"a": 1}),
			Field("text", "title", "Title").With("required", true),
			Field("select", "mode", "Mode").With("options", []map[string]any{Option("a", "A")}),
		).With("submit", "Save"),
		List(Item("SQLite").With("link", ToItem("n1")).With("badge", Badge("new", "")).
			With("actions", []Node{Button("Open", Act("open", nil))})),
		Table([]map[string]any{Col("name", "Name")}, []map[string]any{{"name": "a"}}),
		LinkItem("Open", "n1"),
		LinkURL("Site", "https://example.com"),
		Empty("Nothing").With("body", "Run it").With("action", Button("Run", Act("run", nil))),
	).With("gap", "m")
	if err := Validate(tree); err != nil {
		t.Fatal(err)
	}
	if err := Validate(Stack()); err != nil {
		t.Fatal(err)
	}
	if err := Validate(Text("x").With("html", "<b>")); err == nil {
		t.Fatal("unknown field accepted")
	}
	if err := Validate(Button("x", Act("Bad", nil))); err == nil {
		t.Fatal("bad action id accepted")
	}
}

func TestEncodeNoHTMLEscape(t *testing.T) {
	b, err := Encode(Text("<a&b>"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "<a&b>") || strings.HasSuffix(string(b), "\n") {
		t.Fatalf("encoding %q", b)
	}
}
