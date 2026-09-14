package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// noteActionFake records the comment, link and restore calls of the note
// page and grants manage access on the test note.
type noteActionFake struct {
	*fakeBackend
	resolved        map[string]bool
	deletedComments []string
	deletedLinks    []string
	restored        []string
}

func newNoteActionFake() *noteActionFake {
	return &noteActionFake{fakeBackend: newFake(), resolved: map[string]bool{}}
}

func (f *noteActionFake) GetNoteByPath(ctx context.Context, actor *core.Actor, slug, path string) (*core.Note, *core.Grant, error) {
	note, _, err := f.fakeBackend.GetNoteByPath(ctx, actor, slug, path)
	if err != nil {
		return nil, nil, err
	}
	return note, &core.Grant{Access: core.AccessManage, Source: "space"}, nil
}

func (f *noteActionFake) GetNote(_ context.Context, _ *core.Actor, id string) (*core.Note, *core.Grant, error) {
	if id != f.note.ID {
		return nil, nil, core.ErrNotFound
	}
	return f.note, &core.Grant{Access: core.AccessManage}, nil
}

func (f *noteActionFake) ResolveComment(_ context.Context, _ *core.Actor, id string, resolved bool) (*core.Comment, error) {
	f.resolved[id] = resolved
	return &core.Comment{ID: id, Resolved: resolved}, nil
}

func (f *noteActionFake) DeleteComment(_ context.Context, _ *core.Actor, id string) error {
	f.deletedComments = append(f.deletedComments, id)
	return nil
}

func (f *noteActionFake) DeletePublicLink(_ context.Context, _ *core.Actor, id string) error {
	f.deletedLinks = append(f.deletedLinks, id)
	return nil
}

func (f *noteActionFake) RestoreNote(_ context.Context, _ *core.Actor, id string) error {
	f.restored = append(f.restored, id)
	return nil
}

func (f *noteActionFake) ListComments(context.Context, *core.Actor, string) ([]*core.Comment, error) {
	return nil, nil
}

func (f *noteActionFake) ListShares(context.Context, *core.Actor, string) ([]*core.Share, error) {
	return nil, nil
}

func (f *noteActionFake) ListPublicLinks(context.Context, *core.Actor, string) ([]*core.PublicLink, error) {
	return nil, nil
}

const notePage = "/s/demo/n/guide/install"

func TestPermanentNoteActionsAskFirstWithoutScripting(t *testing.T) {
	tests := []struct {
		name   string
		form   url.Values
		target string
	}{
		{"comment", url.Values{"action": {"comment-delete"}, "commentId": {"c1"}, "nojs": {"1"}}, "confirm=comment-delete&target=c1#confirm"},
		{"link", url.Values{"action": {"link-delete"}, "linkId": {"l1"}, "nojs": {"1"}}, "confirm=link-delete&target=l1#confirm"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newNoteActionFake()
			h := newTestServer(t, fake).Handler()
			rec := do(t, h, http.MethodPost, notePage, memberSession, tt.form)
			if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != notePage+"?"+tt.target {
				t.Fatalf("first post = %d %q, want the confirmation step", rec.Code, rec.Header().Get("Location"))
			}
			if len(fake.deletedComments)+len(fake.deletedLinks) != 0 {
				t.Fatalf("the action ran before it was confirmed")
			}

			page := do(t, h, http.MethodGet, rec.Header().Get("Location"), memberSession, nil)
			body := page.Body.String()
			for _, want := range []string{`id="confirm"`, `name="confirmed" value="1"`, `name="action" value="` + tt.form.Get("action") + `"`} {
				if !strings.Contains(body, want) {
					t.Errorf("confirmation step lacks %q", want)
				}
			}

			tt.form.Set("confirmed", "1")
			rec = do(t, h, http.MethodPost, notePage, memberSession, tt.form)
			if rec.Code != http.StatusSeeOther || len(fake.deletedComments)+len(fake.deletedLinks) != 1 {
				t.Fatalf("confirmed post = %d, comments %v, links %v", rec.Code, fake.deletedComments, fake.deletedLinks)
			}
		})
	}
}

func TestScriptedDeleteActsAtOnce(t *testing.T) {
	fake := newNoteActionFake()
	h := newTestServer(t, fake).Handler()
	// With scripting the ConfirmationDialog asked, and the noscript field is absent.
	rec := do(t, h, http.MethodPost, notePage, memberSession, url.Values{"action": {"link-delete"}, "linkId": {"l1"}})
	if rec.Code != http.StatusSeeOther || len(fake.deletedLinks) != 1 {
		t.Fatalf("status %d, deleted %v", rec.Code, fake.deletedLinks)
	}
}

func TestResolveCommentAndRestoreNote(t *testing.T) {
	fake := newNoteActionFake()
	h := newTestServer(t, fake).Handler()
	rec := do(t, h, http.MethodPost, notePage, memberSession, url.Values{"action": {"comment-resolve"}, "commentId": {"c1"}, "resolved": {"true"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != notePage+"#comment-c1" || !fake.resolved["c1"] {
		t.Fatalf("resolve = %d %q %v", rec.Code, rec.Header().Get("Location"), fake.resolved)
	}
	// The Undo posts the inverse action.
	do(t, h, http.MethodPost, notePage, memberSession, url.Values{"action": {"comment-resolve"}, "commentId": {"c1"}, "resolved": {"false"}})
	if fake.resolved["c1"] {
		t.Errorf("undo did not reopen the thread")
	}

	// Restore runs before the path lookup, because a trashed note left its path.
	rec = do(t, h, http.MethodPost, "/s/demo/n/gone", memberSession, url.Values{"action": {"restore"}, "noteId": {fake.note.ID}})
	if rec.Code != http.StatusSeeOther || len(fake.restored) != 1 || rec.Header().Get("Location") != noteURL("demo", fake.note) {
		t.Fatalf("restore = %d %q %v", rec.Code, rec.Header().Get("Location"), fake.restored)
	}
}

func TestConfirmationForIgnoresUnknownActions(t *testing.T) {
	if confirmationFor(url.Values{"confirm": {"trash"}, "target": {"n1"}}) != nil {
		t.Error("an unknown action got a confirmation step")
	}
	if confirmationFor(url.Values{"confirm": {"comment-delete"}}) != nil {
		t.Error("a confirmation step without a target")
	}
	c := confirmationFor(url.Values{"confirm": {"link-delete"}, "target": {"l9"}})
	if c == nil || c.Field != "linkId" || c.Target != "l9" || c.Label != "Revoke link" {
		t.Errorf("confirmationFor = %+v", c)
	}
}
