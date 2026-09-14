package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// syncPage is the part of a sync batch these tests read.
type syncPage struct {
	Rev        int64            `json:"rev"`
	More       bool             `json:"more"`
	Notes      []*core.SyncNote `json:"notes"`
	Folders    []*core.Folder   `json:"folders"`
	Files      []*core.File     `json:"files"`
	LiveIDs    []string         `json:"liveIds"`
	StarredIDs []string         `json:"starredIds"`
	AccessKey  string           `json:"accessKey"`
	Spaces     []*core.Space    `json:"spaces"`
}

// pull fetches one page and returns it with the raw JSON.
func (h *harness) pull(since int64, limit int) (*syncPage, string) {
	h.t.Helper()
	var raw json.RawMessage
	path := "/api/sync?since=" + itoa(since)
	if limit > 0 {
		path += "&limit=" + itoa(int64(limit))
	}
	resp := h.do("GET", path, nil, &raw)
	h.mustStatus(resp, 200, "sync pull")
	var page syncPage
	if err := json.Unmarshal(raw, &page); err != nil {
		h.t.Fatalf("decode sync page: %v", err)
	}
	return &page, string(raw)
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func noteIn(page *syncPage, id string) *core.SyncNote {
	for _, n := range page.Notes {
		if n.ID == id {
			return n
		}
	}
	return nil
}

func contains(list []string, id string) bool {
	for _, v := range list {
		if v == id {
			return true
		}
	}
	return false
}

// TestSyncFeed walks the change feed through edits, paging, trash, stars,
// client ids, sharing and revocation.
func TestSyncFeed(t *testing.T) {
	h := newHarness(t)
	h.mustStatus(h.do("POST", "/api/auth/signup", map[string]string{
		"email": "owner@example.com", "name": "Olive Owner", "password": "correct horse battery",
	}, nil), 201, "signup")
	var spaces struct{ Items []*core.Space }
	h.do("GET", "/api/spaces", nil, &spaces)
	space := spaces.Items[0]

	mk := func(title, parent, content string) core.Note {
		var n core.Note
		h.mustStatus(h.do("POST", "/api/notes", core.NoteInput{SpaceID: space.ID, ParentID: parent, Title: title, Content: content}, &n), 201, "create "+title)
		return n
	}
	roadmap := mk("Roadmap", "", "quarter goals\n")
	detail := mk("Detail", roadmap.ID, "nested plan\n")
	private := mk("Private", "", "owner only\n")

	// A full snapshot carries bodies, access and the id lists.
	snap, raw := h.pull(0, 0)
	if snap.More || snap.Rev <= 0 || snap.AccessKey == "" {
		t.Fatalf("snapshot more=%v rev=%d key=%q", snap.More, snap.Rev, snap.AccessKey)
	}
	for _, n := range []core.Note{roadmap, detail, private} {
		got := noteIn(snap, n.ID)
		if got == nil || got.Content == "" || got.Access != core.AccessManage || got.SpaceSlug != space.Slug {
			t.Fatalf("snapshot note %s = %+v", n.Title, got)
		}
		if !contains(snap.LiveIDs, n.ID) {
			t.Fatalf("liveIds lacks %s", n.Title)
		}
	}
	if strings.Contains(raw, `"extra"`) || strings.Contains(raw, "passwordHash") {
		t.Fatalf("snapshot leaks internal fields: %s", raw)
	}

	// An unchanged feed returns nothing and keeps the cursor.
	if again, _ := h.pull(snap.Rev, 0); len(again.Notes) != 0 || again.Rev != snap.Rev {
		t.Fatalf("idle pull = %d notes rev %d, want 0 and %d", len(again.Notes), again.Rev, snap.Rev)
	}

	// An edit brings back only the edited note.
	h.mustStatus(h.do("PATCH", "/api/notes/"+detail.ID, core.NoteUpdate{Content: ptr("revised plan\n")}, nil), 200, "edit")
	edited, _ := h.pull(snap.Rev, 0)
	if len(edited.Notes) != 1 || edited.Notes[0].ID != detail.ID || !strings.Contains(edited.Notes[0].Content, "revised") {
		t.Fatalf("edit pull = %+v", edited.Notes)
	}

	// Paging returns every change exactly once, in revision order.
	cursor := edited.Rev
	for i := 0; i < 5; i++ {
		mk("Batch "+itoa(int64(i)), "", "b\n")
	}
	seen := map[string]bool{}
	var lastRev int64
	for pages := 0; ; pages++ {
		if pages > 10 {
			t.Fatal("paging did not terminate")
		}
		page, raw := h.pull(cursor, 2)
		for _, n := range page.Notes {
			if seen[n.ID] {
				t.Fatalf("note %s delivered twice", n.Title)
			}
			if n.Rev <= lastRev {
				t.Fatalf("revision order broken at %s", n.Title)
			}
			seen[n.ID], lastRev = true, n.Rev
		}
		if page.More {
			if !strings.Contains(raw, `"liveIds":null`) {
				t.Fatalf("liveIds present before the last page: %s", raw)
			}
			cursor = page.Rev
			continue
		}
		if page.LiveIDs == nil {
			t.Fatal("liveIds missing on the last page")
		}
		cursor = page.Rev
		break
	}
	if len(seen) != 5 {
		t.Fatalf("paged pull delivered %d notes, want 5", len(seen))
	}

	// Trash arrives as a deleted note without a body, and stars as ids.
	h.mustStatus(h.do("POST", "/api/notes/"+private.ID+"/trash", nil, nil), 204, "trash")
	h.mustStatus(h.do("PUT", "/api/notes/"+roadmap.ID+"/star", nil, nil), 204, "star")
	after, _ := h.pull(cursor, 0)
	if got := noteIn(after, private.ID); got == nil || !got.Deleted || got.Content != "" {
		t.Fatalf("trashed note in feed = %+v", got)
	}
	if !contains(after.StarredIDs, roadmap.ID) || !contains(after.LiveIDs, private.ID) {
		t.Fatalf("starred %v live has trashed %v", after.StarredIDs, contains(after.LiveIDs, private.ID))
	}
	cursor = after.Rev

	// A client-supplied id makes a replayed create idempotent.
	offlineID := "0" + strings.Repeat("a", 25)
	var first, second core.Note
	h.mustStatus(h.do("POST", "/api/notes", core.NoteInput{ID: offlineID, SpaceID: space.ID, Title: "Written offline"}, &first), 201, "offline create")
	h.mustStatus(h.do("POST", "/api/notes", core.NoteInput{ID: offlineID, SpaceID: space.ID, Title: "Written offline"}, &second), 201, "replayed create")
	if first.ID != offlineID || second.ID != offlineID || second.Path != first.Path {
		t.Fatalf("replay gave %q %q paths %q %q", first.ID, second.ID, first.Path, second.Path)
	}
	h.mustStatus(h.do("POST", "/api/notes", core.NoteInput{ID: "not-a-valid-id", SpaceID: space.ID, Title: "x"}, nil), 400, "bad client id")

	// A user outside the space sees nothing until a share, then only the
	// shared subtree, and the access key changes with each grant.
	var reader core.User
	h.mustStatus(h.do("POST", "/api/users", core.UserInput{
		Email: ptr("reader@example.com"), Name: ptr("Rex Reader"), Password: ptr("another good password"),
	}, &reader), 201, "create reader")
	r := h.as("reader@example.com", "another good password")
	before, _ := r.pull(0, 0)
	if noteIn(before, roadmap.ID) != nil || contains(before.LiveIDs, roadmap.ID) {
		t.Fatal("reader sees the roadmap before any share")
	}

	var share core.Share
	h.mustStatus(h.do("POST", "/api/notes/"+roadmap.ID+"/shares", core.ShareInput{
		NoteID: roadmap.ID, UserID: reader.ID, Access: core.AccessView, IncludeChildren: true,
	}, &share), 201, "share")
	shared, _ := r.pull(0, 0)
	if shared.AccessKey == before.AccessKey {
		t.Fatal("access key did not change after a share")
	}
	if got := noteIn(shared, roadmap.ID); got == nil || got.Access != core.AccessView || got.Content == "" {
		t.Fatalf("shared roadmap = %+v", got)
	}
	if noteIn(shared, detail.ID) == nil || noteIn(shared, private.ID) != nil || noteIn(shared, offlineID) != nil {
		t.Fatalf("reader subtree wrong: detail %v private %v offline %v",
			noteIn(shared, detail.ID) != nil, noteIn(shared, private.ID) != nil, noteIn(shared, offlineID) != nil)
	}
	if len(shared.Spaces) != 1 || shared.Spaces[0].ID != space.ID {
		t.Fatalf("reader spaces = %+v", shared.Spaces)
	}

	// Revoking the share drops the subtree from liveIds.
	h.mustStatus(h.do("DELETE", "/api/shares/"+share.ID, nil, nil), 204, "revoke")
	revoked, _ := r.pull(shared.Rev, 0)
	if revoked.AccessKey == shared.AccessKey || contains(revoked.LiveIDs, roadmap.ID) || contains(revoked.LiveIDs, detail.ID) {
		t.Fatalf("after revoke key changed=%v live=%v", revoked.AccessKey != shared.AccessKey, revoked.LiveIDs)
	}

	// A permanent delete leaves liveIds.
	h.mustStatus(h.do("DELETE", "/api/notes/"+private.ID, nil, nil), 204, "delete forever")
	gone, _ := h.pull(cursor, 0)
	if contains(gone.LiveIDs, private.ID) {
		t.Fatal("permanently deleted note still in liveIds")
	}

	// Anonymous callers get 401.
	resp, err := http.Get(h.server.URL + "/api/sync")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("anonymous sync status %d, want 401", resp.StatusCode)
	}
}
