package index

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// mustUpsert writes a prepared note row.
func mustUpsert(t *testing.T, db *DB, n *core.Note) {
	t.Helper()
	if err := db.UpsertNote(context.Background(), ports.NoteRow{Note: n, Plain: n.Title}); err != nil {
		t.Fatalf("UpsertNote(%s): %v", n.Path, err)
	}
}

func TestStars(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	u := mustUser(t, db, "ada@example.com")
	space := mustSpace(t, db, "docs")
	a := mustNote(t, db, space.ID, "A", "")
	b := mustNote(t, db, space.ID, "B", "")

	if ok, err := db.IsStarred(ctx, u.ID, a.ID); err != nil || ok {
		t.Fatalf("IsStarred before starring = %v, %v", ok, err)
	}
	if err := db.SetStar(ctx, u.ID, a.ID, true); err != nil {
		t.Fatalf("SetStar: %v", err)
	}
	if err := db.SetStar(ctx, u.ID, a.ID, true); err != nil {
		t.Fatalf("a repeated SetStar should be a no-op, got %v", err)
	}
	if err := db.SetStar(ctx, u.ID, b.ID, true); err != nil {
		t.Fatalf("SetStar: %v", err)
	}
	if ok, _ := db.IsStarred(ctx, u.ID, a.ID); !ok {
		t.Fatal("IsStarred is false after starring")
	}

	starred, err := db.ListStarred(ctx, u.ID)
	if err != nil {
		t.Fatalf("ListStarred: %v", err)
	}
	if len(starred) != 2 {
		t.Fatalf("ListStarred gave %d rows, want 2", len(starred))
	}
	for _, n := range starred {
		if !n.Starred {
			t.Fatalf("note %q is not flagged as starred", n.Title)
		}
	}

	now := time.Now().UTC()
	if err := db.SetNoteDeleted(ctx, b.ID, true, &now, u.ID); err != nil {
		t.Fatalf("SetNoteDeleted: %v", err)
	}
	if starred, _ = db.ListStarred(ctx, u.ID); len(starred) != 1 {
		t.Fatalf("a trashed note is still listed as starred: %d rows", len(starred))
	}

	if err := db.SetStar(ctx, u.ID, a.ID, false); err != nil {
		t.Fatalf("SetStar false: %v", err)
	}
	if err := db.SetStar(ctx, u.ID, a.ID, false); err != nil {
		t.Fatalf("clearing a missing star should be a no-op, got %v", err)
	}
	if ok, _ := db.IsStarred(ctx, u.ID, a.ID); ok {
		t.Fatal("IsStarred is true after clearing")
	}
}

func TestComments(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	u := mustUser(t, db, "ada@example.com")
	space := mustSpace(t, db, "docs")
	n := mustNote(t, db, space.ID, "A", "")

	root := &core.Comment{NoteID: n.ID, SpaceID: space.ID, UserID: u.ID, Body: "first", Anchor: &core.CommentAnchor{Exact: "p1", Start: 3, End: 5}}
	if err := db.CreateComment(ctx, root); err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	reply := &core.Comment{NoteID: n.ID, SpaceID: space.ID, UserID: u.ID, ParentID: root.ID,
		Body: "reply", CreatedAt: root.CreatedAt.Add(time.Second)}
	if err := db.CreateComment(ctx, reply); err != nil {
		t.Fatalf("CreateComment reply: %v", err)
	}

	got, err := db.GetComment(ctx, root.ID)
	if err != nil {
		t.Fatalf("GetComment: %v", err)
	}
	if got.Body != "first" || (got.Anchor == nil || got.Anchor.Exact != "p1") || got.Resolved || got.DeletedAt != nil {
		t.Fatalf("GetComment gave %+v", got)
	}

	got.Body = "edited"
	got.Resolved = true
	got.ResolvedBy = u.ID
	if err := db.UpdateComment(ctx, got); err != nil {
		t.Fatalf("UpdateComment: %v", err)
	}
	got, _ = db.GetComment(ctx, root.ID)
	if got.Body != "edited" || !got.Resolved || got.ResolvedBy != u.ID {
		t.Fatalf("update did not stick: %+v", got)
	}

	list, err := db.ListComments(ctx, n.ID)
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if len(list) != 2 || list[0].ID != root.ID || list[1].ID != reply.ID {
		t.Fatalf("ListComments gave %d rows in the wrong order", len(list))
	}
	if c, err := db.CountComments(ctx); err != nil || c != 2 {
		t.Fatalf("CountComments = %d, %v, want 2", c, err)
	}

	// A soft delete keeps the row but drops it from the count.
	now := time.Now().UTC()
	reply.DeletedAt = &now
	if err := db.UpdateComment(ctx, reply); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if c, _ := db.CountComments(ctx); c != 1 {
		t.Fatalf("CountComments after a soft delete = %d, want 1", c)
	}
	if list, _ = db.ListComments(ctx, n.ID); len(list) != 2 {
		t.Fatalf("ListComments should keep soft-deleted rows, got %d", len(list))
	}

	if err := db.DeleteComment(ctx, root.ID); err != nil {
		t.Fatalf("DeleteComment: %v", err)
	}
	if list, _ = db.ListComments(ctx, n.ID); len(list) != 0 {
		t.Fatalf("%d comments remain after deleting the root, want 0", len(list))
	}
	if _, err := db.GetComment(ctx, root.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("GetComment after delete gave %v", err)
	}
	if err := db.UpdateComment(ctx, root); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("UpdateComment on a deleted row gave %v", err)
	}
}

func TestSharesAndGrants(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	owner := mustUser(t, db, "ada@example.com")
	guest := mustUser(t, db, "grace@example.com")
	space := mustSpace(t, db, "docs")
	guide := mustNote(t, db, space.ID, "Guide", "")
	child := &core.Note{ID: core.NewID(), SpaceID: space.ID, ParentID: guide.ID,
		Slug: "install", Path: "guide/install.md", Title: "Install"}
	mustUpsert(t, db, child)

	share := &core.Share{NoteID: guide.ID, SpaceID: space.ID, UserID: guest.ID,
		Access: core.AccessComment, IncludeChildren: true, CreatedBy: owner.ID}
	if err := db.CreateShare(ctx, share); err != nil {
		t.Fatalf("CreateShare: %v", err)
	}
	dup := &core.Share{NoteID: guide.ID, SpaceID: space.ID, UserID: guest.ID, Access: core.AccessView}
	if err := db.CreateShare(ctx, dup); !errors.Is(err, core.ErrExists) {
		t.Fatalf("a second share for the same note and user gave %v, want core.ErrExists", err)
	}
	second := &core.Share{NoteID: child.ID, SpaceID: space.ID, UserID: guest.ID, Access: core.AccessEdit}
	if err := db.CreateShare(ctx, second); err != nil {
		t.Fatalf("CreateShare second: %v", err)
	}

	got, err := db.GetShare(ctx, share.ID)
	if err != nil {
		t.Fatalf("GetShare: %v", err)
	}
	if got.Access != core.AccessComment || !got.IncludeChildren {
		t.Fatalf("GetShare gave %+v", got)
	}
	found, err := db.FindShare(ctx, guide.ID, guest.ID)
	if err != nil || found.ID != share.ID {
		t.Fatalf("FindShare gave %v, %v", found, err)
	}
	if _, err := db.FindShare(ctx, guide.ID, owner.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("FindShare for an ungranted user gave %v", err)
	}

	got.Access = core.AccessEdit
	got.IncludeChildren = false
	if err := db.UpdateShare(ctx, got); err != nil {
		t.Fatalf("UpdateShare: %v", err)
	}

	grants, err := db.ShareGrantsForUser(ctx, guest.ID)
	if err != nil {
		t.Fatalf("ShareGrantsForUser: %v", err)
	}
	want := []ports.ShareGrant{
		{NoteID: guide.ID, SpaceID: space.ID, NotePath: "guide.md", Access: core.AccessEdit, IncludeChildren: false, Kind: core.KindNote},
		{NoteID: child.ID, SpaceID: space.ID, NotePath: "guide/install.md", Access: core.AccessEdit, IncludeChildren: false, Kind: core.KindNote},
	}
	if len(grants) != len(want) {
		t.Fatalf("ShareGrantsForUser gave %d grants, want %d", len(grants), len(want))
	}
	for i := range want {
		if grants[i] != want[i] {
			t.Errorf("grant %d is %+v, want %+v", i, grants[i], want[i])
		}
	}

	if list, err := db.ListSharesForNote(ctx, guide.ID); err != nil || len(list) != 1 {
		t.Fatalf("ListSharesForNote gave %d rows, %v", len(list), err)
	}
	if list, err := db.ListSharesForUser(ctx, guest.ID); err != nil || len(list) != 2 {
		t.Fatalf("ListSharesForUser gave %d rows, %v", len(list), err)
	}

	if err := db.DeleteShare(ctx, share.ID); err != nil {
		t.Fatalf("DeleteShare: %v", err)
	}
	if err := db.DeleteShare(ctx, share.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("second DeleteShare gave %v", err)
	}
	if grants, _ = db.ShareGrantsForUser(ctx, guest.ID); len(grants) != 1 {
		t.Fatalf("%d grants remain, want 1", len(grants))
	}
}

func TestPublicLinks(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	owner := mustUser(t, db, "ada@example.com")
	space := mustSpace(t, db, "docs")
	n := mustNote(t, db, space.ID, "Public", "")

	expires := time.Now().UTC().Add(-time.Hour)
	link := &core.PublicLink{Token: "tok-1", NoteID: n.ID, SpaceID: space.ID, Access: core.AccessView,
		PasswordHash: "$argon2id$stub", ExpiresAt: &expires, CreatedBy: owner.ID}
	if err := db.CreatePublicLink(ctx, link); err != nil {
		t.Fatalf("CreatePublicLink: %v", err)
	}
	if err := db.CreatePublicLink(ctx, &core.PublicLink{Token: "tok-1", NoteID: n.ID}); !errors.Is(err, core.ErrExists) {
		t.Fatalf("a repeated token gave %v, want core.ErrExists", err)
	}

	got, err := db.GetPublicLinkByToken(ctx, "tok-1")
	if err != nil {
		t.Fatalf("GetPublicLinkByToken: %v", err)
	}
	if got.ID != link.ID || !got.HasPassword || got.Access != core.AccessView {
		t.Fatalf("GetPublicLinkByToken gave %+v", got)
	}
	if !got.Expired(time.Now()) {
		t.Fatal("the link should be reported as expired, and still returned")
	}
	if _, err := db.GetPublicLinkByToken(ctx, "nope"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("an unknown token gave %v", err)
	}

	if err := db.IncrementLinkViews(ctx, link.ID); err != nil {
		t.Fatalf("IncrementLinkViews: %v", err)
	}
	if err := db.IncrementLinkViews(ctx, link.ID); err != nil {
		t.Fatalf("IncrementLinkViews: %v", err)
	}

	got.PasswordHash = ""
	got.ExpiresAt = nil
	got.Access = core.AccessComment
	got.IncludeChildren = true
	if err := db.UpdatePublicLink(ctx, got); err != nil {
		t.Fatalf("UpdatePublicLink: %v", err)
	}
	got, err = db.GetPublicLink(ctx, link.ID)
	if err != nil {
		t.Fatalf("GetPublicLink: %v", err)
	}
	if got.HasPassword || got.ExpiresAt != nil || got.Access != core.AccessComment || !got.IncludeChildren {
		t.Fatalf("update did not stick: %+v", got)
	}
	if got.Views != 2 {
		t.Fatalf("Views is %d, want 2", got.Views)
	}

	if list, err := db.ListPublicLinks(ctx, n.ID); err != nil || len(list) != 1 {
		t.Fatalf("ListPublicLinks gave %d rows, %v", len(list), err)
	}
	if err := db.DeletePublicLink(ctx, link.ID); err != nil {
		t.Fatalf("DeletePublicLink: %v", err)
	}
	if err := db.IncrementLinkViews(ctx, link.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("IncrementLinkViews on a deleted link gave %v", err)
	}
}

func TestSpaceLinksAndPasteFolder(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	space := mustSpace(t, db, "docs")
	space.PasteFolder = "attachments"
	if err := db.UpdateSpace(ctx, space); err != nil {
		t.Fatalf("UpdateSpace: %v", err)
	}
	if got, err := db.GetSpace(ctx, space.ID); err != nil || got.PasteFolder != "attachments" {
		t.Fatalf("GetSpace paste folder = %+v, %v", got, err)
	}
	target := mustNote(t, db, space.ID, "Target", "")
	from := &core.Note{ID: core.NewID(), SpaceID: space.ID, Path: "from.md", Slug: "from", Title: "From"}
	row := ports.NoteRow{Note: from, LinkRefs: []ports.NoteLink{
		{Target: "Target", Form: ports.LinkWikilink, TargetID: target.ID},
		{Target: "missing%20x.png", Form: ports.LinkMarkdownImage, Embed: true},
	}}
	if err := db.UpsertNote(ctx, row); err != nil {
		t.Fatalf("UpsertNote: %v", err)
	}
	links, err := db.SpaceLinks(ctx, space.ID)
	if err != nil {
		t.Fatalf("SpaceLinks: %v", err)
	}
	if len(links) != 2 || links[0].NoteID != from.ID || links[0].NotePath != "from.md" {
		t.Fatalf("SpaceLinks = %+v", links)
	}
	if links[0].TargetID != target.ID || links[1].Target != "missing x.png" || !links[1].Embed {
		t.Fatalf("SpaceLinks rows = %+v", links)
	}
}

func TestCommentAnchorStorage(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	space := mustSpace(t, db, "docs")
	n := mustNote(t, db, space.ID, "Page", "body")
	u := &core.User{Email: "anchor@example.com", Name: "A", IsActive: true}
	if err := db.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}

	withAnchor := &core.Comment{NoteID: n.ID, SpaceID: space.ID, UserID: u.ID, Body: "on text",
		Anchor: &core.CommentAnchor{Exact: "😀 sync", Prefix: "the ", Suffix: " engine", Start: 4, End: 11}}
	general := &core.Comment{NoteID: n.ID, SpaceID: space.ID, UserID: u.ID, Body: "general"}
	for _, c := range []*core.Comment{withAnchor, general} {
		if err := db.CreateComment(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.GetComment(ctx, withAnchor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Anchor == nil || *got.Anchor != *withAnchor.Anchor {
		t.Fatalf("anchor round trip = %+v, want %+v", got.Anchor, withAnchor.Anchor)
	}
	if g, _ := db.GetComment(ctx, general.ID); g.Anchor != nil {
		t.Fatalf("general comment gained an anchor: %+v", g.Anchor)
	}

	// A plain string stored before anchors were structured reads as exact text.
	if _, err := db.SQL().ExecContext(ctx, `UPDATE comments SET anchor = 'legacy words' WHERE id = ?`, general.ID); err != nil {
		t.Fatal(err)
	}
	legacy, _ := db.GetComment(ctx, general.ID)
	if legacy.Anchor == nil || legacy.Anchor.Exact != "legacy words" || legacy.Anchor.Start != 0 {
		t.Fatalf("legacy anchor = %+v", legacy.Anchor)
	}
}

func TestUpdateCommentAnchorIsCompareAndSwap(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	space := mustSpace(t, db, "docs")
	n := mustNote(t, db, space.ID, "Page", "body")
	u := &core.User{Email: "cas@example.com", Name: "C", IsActive: true}
	if err := db.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	first := &core.CommentAnchor{Exact: "sync", Prefix: "the ", Start: 4, End: 8}
	c := &core.Comment{NoteID: n.ID, SpaceID: space.ID, UserID: u.ID, Body: "note", Anchor: first}
	if err := db.CreateComment(ctx, c); err != nil {
		t.Fatal(err)
	}
	before, _ := db.GetComment(ctx, c.ID)

	moved := &core.CommentAnchor{Exact: "sync", Prefix: "the ", Start: 14, End: 18}
	ok, err := db.UpdateCommentAnchor(ctx, c.ID, first, moved)
	if err != nil || !ok {
		t.Fatalf("first swap = %v, %v", ok, err)
	}
	after, _ := db.GetComment(ctx, c.ID)
	if after.Anchor == nil || *after.Anchor != *moved {
		t.Fatalf("anchor after swap = %+v", after.Anchor)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) || after.Body != "note" || after.Resolved != before.Resolved {
		t.Fatalf("swap changed other fields: before %+v after %+v", before, after)
	}

	// A stale prev writes nothing.
	ok, err = db.UpdateCommentAnchor(ctx, c.ID, first, &core.CommentAnchor{Exact: "sync", Start: 99, End: 103})
	if err != nil || ok {
		t.Fatalf("stale swap = %v, %v, want false", ok, err)
	}
	if again, _ := db.GetComment(ctx, c.ID); *again.Anchor != *moved {
		t.Fatalf("stale swap overwrote the anchor: %+v", again.Anchor)
	}

	// A comment without an anchor swaps from nil.
	g := &core.Comment{NoteID: n.ID, SpaceID: space.ID, UserID: u.ID, Body: "general"}
	if err := db.CreateComment(ctx, g); err != nil {
		t.Fatal(err)
	}
	if ok, err := db.UpdateCommentAnchor(ctx, g.ID, nil, first); err != nil || !ok {
		t.Fatalf("swap from nil = %v, %v", ok, err)
	}
	if ok, _ := db.UpdateCommentAnchor(ctx, "missing", nil, first); ok {
		t.Fatal("swap on a missing comment reported a write")
	}
}
