package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestCreateShare(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	n := h.note(owner, sp, "", "Plan", "")
	friend := h.user("Friend", false)
	viewer := h.user("Viewer", false)
	h.member(sp, viewer, core.RoleViewer)

	if _, err := h.app.CreateShare(h.ctx, owner, core.ShareInput{NoteID: n.ID, UserID: owner.UserID(), Access: core.AccessView}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("self share err = %v", err)
	}
	if _, err := h.app.CreateShare(h.ctx, viewer, core.ShareInput{NoteID: n.ID, UserID: friend.UserID(), Access: core.AccessView}); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("viewer share err = %v", err)
	}
	s, err := h.app.CreateShare(h.ctx, owner, core.ShareInput{NoteID: n.ID, Email: strings.ToUpper(friend.User.Email), Access: core.AccessEdit})
	if err != nil || s.UserID != friend.UserID() {
		t.Fatalf("CreateShare = %+v, %v", s, err)
	}
	body := "edited by friend"
	if _, err := h.app.UpdateNote(h.ctx, friend, n.ID, core.NoteUpdate{Content: &body}); err != nil {
		t.Fatalf("shared edit: %v", err)
	}
	again, err := h.app.CreateShare(h.ctx, owner, core.ShareInput{NoteID: n.ID, UserID: friend.UserID(), Access: core.AccessView})
	if err != nil || again.ID != s.ID || again.Access != core.AccessView {
		t.Fatalf("repeat share = %+v, %v", again, err)
	}
	if _, err := h.app.UpdateNote(h.ctx, friend, n.ID, core.NoteUpdate{Content: &body}); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("downgraded edit err = %v", err)
	}

	// A share on a folder or a file goes through the same call.
	box := h.folder(owner, sp, "", "Box")
	pic := h.upload(owner, sp, box.ID, "pic.png", "png")
	fs, err := h.app.CreateShare(h.ctx, owner, core.ShareInput{NoteID: box.ID, UserID: friend.UserID(), Access: core.AccessEdit, IncludeChildren: true})
	if err != nil {
		t.Fatalf("folder share: %v", err)
	}
	if _, err := h.app.RenameFile(h.ctx, friend, pic.ID, "renamed.png"); err != nil {
		t.Fatalf("edit through folder share: %v", err)
	}
	list, err := h.app.ListShares(h.ctx, owner, box.ID)
	if err != nil || len(list) != 1 || list[0].ID != fs.ID || list[0].User == nil {
		t.Fatalf("ListShares on folder = %+v, %v", list, err)
	}
	if _, err := h.app.UpdateShare(h.ctx, owner, fs.ID, core.AccessView, true); err != nil {
		t.Fatal(err)
	}
	if _, err := h.app.RenameFile(h.ctx, friend, pic.ID, "again.png"); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("edit after downgrade err = %v", err)
	}
	grants, _ := h.db.ItemShareGrantsForUser(h.ctx, friend.UserID())
	kinds := map[core.ItemKind]bool{}
	for _, g := range grants {
		kinds[g.Kind] = true
	}
	if !kinds[core.KindFolder] || !kinds[core.KindNote] {
		t.Fatalf("share grants lack a kind: %+v", grants)
	}
	if _, err := h.app.CreateShare(h.ctx, owner, core.ShareInput{NoteID: pic.ID, UserID: friend.UserID(), Access: core.AccessComment}); err != nil {
		t.Fatalf("file share: %v", err)
	}
	if err := h.app.DeleteShare(h.ctx, friend, fs.ID); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("recipient deletes share err = %v", err)
	}
	if err := h.app.DeleteShare(h.ctx, owner, fs.ID); err != nil {
		t.Fatal(err)
	}
}

func TestPublicLinks(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	n := h.note(owner, sp, "", "Plan", "public body")
	child := h.note(owner, sp, n.ID, "Child", "child body")
	if row := h.row(n.ID); row.Path != "plan/index.md" {
		t.Fatalf("folder note path = %s", row.Path)
	}

	pw := "hunter22"
	link, err := h.app.CreatePublicLink(h.ctx, owner, core.PublicLinkInput{NoteID: n.ID, Access: core.AccessManage, Password: &pw})
	if err != nil {
		t.Fatal(err)
	}
	if link.Access != core.AccessComment || !link.HasPassword || link.PasswordHash == pw {
		t.Fatalf("link = %+v", link)
	}
	if _, _, err := h.app.ResolvePublicLink(h.ctx, link.Token, ""); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("no password err = %v", err)
	}
	if _, _, err := h.app.ResolvePublicLink(h.ctx, link.Token, "wrong"); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("wrong password err = %v", err)
	}
	actor, note, err := h.app.ResolvePublicLink(h.ctx, link.Token, pw)
	if err != nil || note.Content != "public body" || actor.Link == nil || actor.User != nil {
		t.Fatalf("ResolvePublicLink = %+v, %+v, %v", actor, note, err)
	}
	stored, _ := h.db.GetPublicLink(h.ctx, link.ID)
	if stored.Views != 1 {
		t.Fatalf("views = %d", stored.Views)
	}
	if _, _, err := h.app.GetNote(h.ctx, actor, child.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("link without children reads child err = %v", err)
	}
	kids, err := h.app.CreatePublicLink(h.ctx, owner, core.PublicLinkInput{NoteID: n.ID, IncludeChildren: true})
	if err != nil {
		t.Fatal(err)
	}
	kidActor, _, err := h.app.ResolvePublicLink(h.ctx, kids.Token, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, _, err := h.app.GetNote(h.ctx, kidActor, child.ID); err != nil || got.Content != "child body" {
		t.Fatalf("folder note link with children reads child = %+v, %v", got, err)
	}
	if list, err := h.app.ListPublicLinks(h.ctx, owner, n.ID); err != nil || len(list) != 2 {
		t.Fatalf("ListPublicLinks = %d, %v", len(list), err)
	}
	body := "vandal"
	if _, err := h.app.UpdateNote(h.ctx, actor, n.ID, core.NoteUpdate{Content: &body}); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("link edit err = %v", err)
	}

	exp := h.clock.Now().Add(time.Hour)
	short, err := h.app.CreatePublicLink(h.ctx, owner, core.PublicLinkInput{NoteID: n.ID, ExpiresAt: &exp})
	if err != nil || short.Access != core.AccessView {
		t.Fatalf("short link = %+v, %v", short, err)
	}
	h.clock.Advance(2 * time.Hour)
	if _, _, err := h.app.ResolvePublicLink(h.ctx, short.Token, ""); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("expired link err = %v", err)
	}
}

func TestCommentsThreadAndTombstone(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	n := h.note(owner, sp, "", "Plan", "")
	commenter := h.user("Commenter", false)
	h.member(sp, commenter, core.RoleCommenter)
	viewer := h.user("Viewer", false)
	h.member(sp, viewer, core.RoleViewer)

	root, err := h.app.CreateComment(h.ctx, commenter, core.CommentInput{NoteID: n.ID, Body: "root"})
	if err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(time.Second)
	reply, err := h.app.CreateComment(h.ctx, owner, core.CommentInput{NoteID: n.ID, ParentID: root.ID, Body: "reply"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.app.CreateComment(h.ctx, viewer, core.CommentInput{NoteID: n.ID, Body: "nope"}); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("viewer comment err = %v", err)
	}
	if _, err := h.app.UpdateComment(h.ctx, viewer, reply.ID, "hijack"); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("non-author edit err = %v", err)
	}
	if err := h.app.DeleteComment(h.ctx, commenter, root.ID); err != nil {
		t.Fatalf("author delete: %v", err)
	}
	list, err := h.app.ListComments(h.ctx, viewer, n.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListComments = %v, %v", list, err)
	}
	if list[0].Body != "" || list[0].DeletedAt == nil || len(list[0].Children) != 1 {
		t.Fatalf("tombstone = %+v", list[0])
	}
	if list[0].Children[0].Author == nil || list[0].Children[0].Author.Name != "Owner" {
		t.Fatalf("reply author = %+v", list[0].Children[0].Author)
	}
	if err := h.app.DeleteComment(h.ctx, owner, reply.ID); err != nil {
		t.Fatalf("manager delete: %v", err)
	}
}

func TestRenderHidesInaccessibleLinks(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	target := h.note(owner, sp, "", "Secret Target", "")
	page := h.note(owner, sp, "", "Page", "see [[Secret Target]]")
	guest := h.user("Guest", false)
	h.share(page.ID, guest, core.AccessView, false)

	out, err := h.app.RenderNote(h.ctx, owner, &core.Note{ID: page.ID, Content: page.Content})
	if err != nil || !strings.Contains(out.HTML, `/s/docs/n/secret-target`) || out.Links[0].NoteID != target.ID {
		t.Fatalf("owner render = %+v, %v", out, err)
	}
	guestOut, err := h.app.RenderNote(h.ctx, guest, &core.Note{ID: page.ID, Content: page.Content})
	if err != nil || strings.Contains(guestOut.HTML, "secret-target") || guestOut.Links[0].NoteID != "" {
		t.Fatalf("guest render leaked the link: %+v, %v", guestOut, err)
	}
}

func TestAIProviders(t *testing.T) {
	h := newHarness(t)
	admin := h.user("Admin", true)
	user := h.user("User", false)

	list, err := h.app.ListAIProviders(h.ctx, user)
	if err != nil || len(list) != 1 || list[0].APIKey != "" || !list[0].HasAPIKey {
		t.Fatalf("ListAIProviders = %+v, %v", list, err)
	}
	if _, err := h.app.SaveAIProvider(h.ctx, user, &core.AIProviderConfig{ID: "local", BaseURL: "http://evil"}); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("user overrides file provider err = %v", err)
	}
	saved, err := h.app.SaveAIProvider(h.ctx, user, &core.AIProviderConfig{ID: "mine", BaseURL: "http://127.0.0.1:2", APIKey: "k", Enabled: true})
	if err != nil || saved.APIKey != "" || !saved.HasAPIKey {
		t.Fatalf("user save = %+v, %v", saved, err)
	}
	if saved.OwnerID != user.UserID() {
		t.Fatalf("user provider owner = %q, want %q", saved.OwnerID, user.UserID())
	}
	if _, err := h.app.SaveAIProvider(h.ctx, user, &core.AIProviderConfig{ID: "mine", BaseURL: "http://127.0.0.1:4", Enabled: true}); err != nil {
		t.Fatalf("owner updates own provider err = %v", err)
	}

	// An owner still lists a provider they disabled, so they can re-enable it,
	// but cannot call it while it is off.
	if _, err := h.app.SaveAIProvider(h.ctx, user, &core.AIProviderConfig{ID: "mine", BaseURL: "http://127.0.0.1:4", Enabled: false}); err != nil {
		t.Fatalf("owner disables own provider err = %v", err)
	}
	ownList, err := h.app.ListAIProviders(h.ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, p := range ownList {
		if p.ID == "mine" {
			listed = !p.Enabled
		}
	}
	if !listed {
		t.Fatal("owner does not see their own disabled provider")
	}
	// A request that names the disabled provider skips it, as docs/models.md
	// describes, and the route serves the call.
	if res, err := h.app.RunAI(h.ctx, user, core.AIRequest{Task: core.TaskSummarize, Text: "hi", ProviderID: "mine"}); err != nil || res.Provider == "mine" {
		t.Fatalf("owner calls disabled provider = %+v, %v", res, err)
	}
	if _, err := h.app.SaveAIProvider(h.ctx, user, &core.AIProviderConfig{ID: "mine", BaseURL: "http://127.0.0.1:4", Enabled: true}); err != nil {
		t.Fatalf("owner re-enables own provider err = %v", err)
	}

	// Another user cannot see, call, change or delete a personal provider,
	// because it carries the owner's API key.
	other := h.user("Other", false)
	if _, err := h.app.SaveAIProvider(h.ctx, other, &core.AIProviderConfig{ID: "mine", BaseURL: "http://evil", Enabled: true}); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("other user update err = %v", err)
	}
	otherList, err := h.app.ListAIProviders(h.ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range otherList {
		if p.ID == "mine" {
			t.Fatal("another user's personal provider is listed")
		}
	}
	if _, err := h.app.RunAI(h.ctx, other, core.AIRequest{Task: core.TaskSummarize, Text: "hi", ProviderID: "mine"}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("other user calls personal provider err = %v", err)
	}
	if err := h.app.DeleteAIProvider(h.ctx, other, "mine"); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("other user delete err = %v", err)
	}

	// An admin update without a key keeps the stored key and the owner.
	if _, err := h.app.SaveAIProvider(h.ctx, admin, &core.AIProviderConfig{ID: "mine", BaseURL: "http://127.0.0.1:3", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	stored, _ := h.db.GetAIProvider(h.ctx, "mine")
	if stored.APIKey != "k" {
		t.Fatal("admin update without a key wiped the stored key")
	}
	if stored.OwnerID != user.UserID() {
		t.Fatalf("admin update changed the owner to %q", stored.OwnerID)
	}

	// A provider an admin creates is an instance provider.
	shared, err := h.app.SaveAIProvider(h.ctx, admin, &core.AIProviderConfig{ID: "shared", BaseURL: "http://127.0.0.1:5", Enabled: true})
	if err != nil || shared.OwnerID != "" {
		t.Fatalf("admin provider = %+v, %v", shared, err)
	}
	if err := h.app.DeleteAIProvider(h.ctx, user, "shared"); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("user deletes instance provider err = %v", err)
	}
	if err := h.app.DeleteAIProvider(h.ctx, user, "mine"); err != nil {
		t.Fatalf("owner deletes own provider err = %v", err)
	}

	res, err := h.app.RunAI(h.ctx, user, core.AIRequest{Task: core.TaskSummarize, Text: "hello"})
	if err != nil || res.Provider != "local" || !strings.Contains(res.Text, "hello") {
		t.Fatalf("RunAI = %+v, %v", res, err)
	}
	h.cfg.AI.Enabled = false
	if _, err := h.app.RunAI(h.ctx, user, core.AIRequest{Task: core.TaskSummarize, Text: "hello"}); !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("disabled AI err = %v", err)
	}
}

func TestAskUsesOnlyVisibleContext(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	open := h.note(owner, sp, "", "Open", "zzzz open")
	secret := h.note(owner, sp, "", "Secret", "zzzz secret")
	guest := h.user("Guest", false)
	h.share(open.ID, guest, core.AccessView, false)
	if _, err := h.app.ReindexEmbeddings(h.ctx, owner, sp.ID); err != nil {
		t.Fatal(err)
	}
	res, err := h.app.RunAI(h.ctx, guest, core.AIRequest{Task: core.TaskAsk, NoteID: open.ID, Question: "zzzz", UseContext: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sources) == 0 {
		t.Fatal("no sources")
	}
	for _, s := range res.Sources {
		if s.NoteID == secret.ID {
			t.Fatal("ask leaked a note the actor cannot view")
		}
	}
	if _, err := h.app.RunAI(h.ctx, guest, core.AIRequest{Task: core.TaskSummarize, NoteID: secret.ID}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("AI on hidden note err = %v", err)
	}
}
