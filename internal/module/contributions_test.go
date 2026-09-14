package module

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestCommandResponseSharedVectors(t *testing.T) {
	var doc struct {
		CommandResponses struct {
			Cases []struct {
				Name     string          `json:"name"`
				Response json.RawMessage `json:"response"`
				Error    *uiVectorError  `json:"error"`
			} `json:"cases"`
		} `json:"commandResponses"`
	}
	loadProtocolFile(t, &doc)
	cases := doc.CommandResponses.Cases
	if len(cases) < 20 {
		t.Fatalf("only %d shared command response cases", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			r, err := ParseCommandResponse(c.Response)
			if c.Error == nil {
				if err != nil {
					t.Fatalf("valid response rejected: %v", err)
				}
				raw, err := marshalCanonical(r)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := ParseCommandResponse(raw); err != nil {
					t.Fatalf("re-encoded response %s rejected: %v", raw, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("invalid response accepted, want %s at %s", c.Error.Code, c.Error.Path)
			}
			if !errors.Is(err, core.ErrInvalid) {
				t.Fatalf("error %v does not unwrap to ErrInvalid", err)
			}
			if strings.HasPrefix(c.Error.Path, "$.ui") {
				var ue *UIError
				if !errors.As(err, &ue) || ue.Code != c.Error.Code || ue.Path != c.Error.Path {
					t.Fatalf("got %v, want %s at %s", err, c.Error.Code, c.Error.Path)
				}
			}
		})
	}
}

func TestIDSharedVectors(t *testing.T) {
	var doc struct {
		IDs struct {
			Cases []struct {
				Rule  string `json:"rule"`
				Value string `json:"value"`
				Valid bool   `json:"valid"`
			} `json:"cases"`
		} `json:"ids"`
	}
	loadProtocolFile(t, &doc)
	checks := map[string]func(string) bool{
		"localId":      ValidIdent,
		"objectId":     ValidObjectID,
		"qualifiedId":  ValidQualifiedID,
		"eventPattern": ValidEventPattern,
	}
	n := 0
	for _, c := range doc.IDs.Cases {
		check, ok := checks[c.Rule]
		if !ok {
			continue
		}
		n++
		if got := check(c.Value); got != c.Valid {
			t.Errorf("%s %q: valid=%v, want %v", c.Rule, c.Value, got, c.Valid)
		}
	}
	if n < 20 {
		t.Fatalf("only %d id cases checked", n)
	}
}

func TestCommandResponseValidate(t *testing.T) {
	ok := []CommandResponse{
		{Toast: &Toast{Message: "Queued 412 bookmarks", Kind: "info"}},
		{OpenPanel: &OpenPanel{ID: "bookmark-wiki.wiki", ItemID: core.NewID()}},
		{Navigate: "/s/docs/n/wiki/index"},
		{Job: &JobRef{ID: core.NewID()}},
		{UI: &Node{Type: "stack", Children: []Node{{Type: "text", Value: "hi"}}}},
	}
	for _, r := range ok {
		if err := r.Validate(); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
	bad := []CommandResponse{
		{},
		{Toast: &Toast{Message: "a"}, Navigate: "/a"},
		{Toast: &Toast{Message: ""}},
		{Toast: &Toast{Message: "a", Kind: "danger"}},
		{OpenPanel: &OpenPanel{ID: "wiki"}},
		{Navigate: "//evil.example"},
		{Navigate: "https://example.com"},
		{Job: &JobRef{ID: "../x"}},
		{UI: &Node{Type: "script"}},
	}
	for _, r := range bad {
		if err := r.Validate(); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("%+v: got %v, want ErrInvalid", r, err)
		}
	}
	var ue *UIError
	if err := (CommandResponse{UI: &Node{Type: "script"}}).Validate(); !errors.As(err, &ue) || ue.Code != UIUnknownType {
		t.Fatalf("invalid UI tree: %v", err)
	}
}
