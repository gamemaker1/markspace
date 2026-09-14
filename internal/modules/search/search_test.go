package search

import (
	"context"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/api/testkit"
	"github.com/gamemaker1/markspace/internal/core"
)

// serve builds the API handler over b with this module registered.
func serve(t *testing.T, b core.Backend) http.Handler {
	t.Helper()
	env := testkit.Env(b)
	return testkit.Serve(t, env, New(env))
}

func TestSearchQueryFrom(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want core.SearchQuery
	}{
		{
			name: "defaults",
			raw:  "q=hello",
			want: core.SearchQuery{Text: "hello", Limit: 25},
		},
		{
			name: "every field",
			raw:  "q=deploy&mode=hybrid&space=sp1&space=sp2&tag=ops&tag=docs&author=u9&limit=10&offset=20&trash=1",
			want: core.SearchQuery{
				Text:         "deploy",
				Mode:         "hybrid",
				SpaceIDs:     []string{"sp1", "sp2"},
				Tags:         []string{"ops", "docs"},
				AuthorID:     "u9",
				IncludeTrash: true,
				Limit:        10,
				Offset:       20,
			},
		},
		{
			name: "limit clamped",
			raw:  "q=x&limit=5000",
			want: core.SearchQuery{Text: "x", Limit: 100},
		},
		{
			name: "bad numbers fall back",
			raw:  "q=x&limit=abc&offset=-4",
			want: core.SearchQuery{Text: "x", Limit: 25},
		},
		{
			name: "trash false words",
			raw:  "q=x&trash=maybe",
			want: core.SearchQuery{Text: "x", Limit: 25},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := url.ParseQuery(tc.raw)
			if err != nil {
				t.Fatalf("ParseQuery: %v", err)
			}
			got := searchQueryFrom(v)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestSearchRouteForwardsQuery(t *testing.T) {
	var got core.SearchQuery
	fake := &testkit.Fake{
		SearchFn: func(_ context.Context, _ *core.Actor, q core.SearchQuery) ([]*core.SearchHit, error) {
			got = q
			return []*core.SearchHit{{Note: &core.Note{ID: "n1"}, Snippet: "<mark>hi</mark>"}}, nil
		},
	}
	rec := testkit.Do(t, serve(t, fake), "GET", "/api/search?q=hi&space=sp1&tag=t1&mode=semantic", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	want := core.SearchQuery{
		Text:     "hi",
		Mode:     "semantic",
		SpaceIDs: []string{"sp1"},
		Tags:     []string{"t1"},
		Limit:    25,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("query = %+v, want %+v", got, want)
	}
	var body struct {
		Items []*core.SearchHit `json:"items"`
	}
	testkit.DecodeBody(t, rec, &body)
	if len(body.Items) != 1 || body.Items[0].Note.ID != "n1" {
		t.Errorf("items = %+v", body.Items)
	}
}

func TestEmptyCollectionIsEmptyArray(t *testing.T) {
	fake := &testkit.Fake{
		SearchFn: func(context.Context, *core.Actor, core.SearchQuery) ([]*core.SearchHit, error) {
			return nil, nil
		},
	}
	rec := testkit.Do(t, serve(t, fake), "GET", "/api/search?q=nothing", "", "")
	if got := strings.TrimSpace(rec.Body.String()); got != `{"items":[]}` {
		t.Errorf("body = %s", got)
	}
}
