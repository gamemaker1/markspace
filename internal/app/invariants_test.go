package app

// Regression tests for business rules that keep the instance consistent: at
// least one active admin, an owner on every space, no orphaned or dangling
// rows, and no access through trashed or deleted notes.

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: err = %v, want %v", what, err, want)
	}
}

func noErr(t *testing.T, what string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// roleOf returns the member role of a user in a space, or "" when none.
func (h *rharness) roleOf(sp *core.Space, actor *core.Actor) core.Role {
	h.t.Helper()
	m, err := h.db.GetMember(h.ctx, sp.ID, actor.User.ID)
	if err != nil || m == nil {
		return ""
	}
	return m.Role
}

func boolp(v bool) *bool { return &v }

func strp(v string) *string { return &v }

// --- instance admins -------------------------------------------------------

func TestLastAdminCannotBeDeleted(t *testing.T) {
	h := newRealHarness(t)
	admin := h.user("Admin", true)
	dormant := h.user("Dormant", true)
	dormant.User.IsActive = false
	noErr(t, "deactivate", h.db.UpdateUser(h.ctx, dormant.User))

	wantErr(t, "admin deletes themselves", h.app.DeleteUser(h.ctx, admin, admin.UserID()), core.ErrConflict)
	if _, err := h.db.GetUser(h.ctx, admin.UserID()); err != nil {
		t.Fatalf("last admin was deleted: %v", err)
	}

	// An inactive admin does not count, so the refusal stands.
	wantErr(t, "inactive admin does not count", h.app.DeleteUser(h.ctx, admin, admin.UserID()), core.ErrConflict)

	second := h.user("Second", true)
	noErr(t, "delete with another active admin", h.app.DeleteUser(h.ctx, second, admin.UserID()))
	wantErr(t, "delete the now last admin", h.app.DeleteUser(h.ctx, second, second.UserID()), core.ErrConflict)
}

func TestLastAdminCannotBeDemotedOrDeactivated(t *testing.T) {
	h := newRealHarness(t)
	admin := h.user("Admin", true)
	dormant := h.user("Dormant", true)
	dormant.User.IsActive = false
	noErr(t, "deactivate", h.db.UpdateUser(h.ctx, dormant.User))

	_, err := h.app.UpdateUser(h.ctx, admin, admin.UserID(), core.UserInput{IsAdmin: boolp(false)})
	wantErr(t, "demote last admin", err, core.ErrConflict)
	_, err = h.app.UpdateUser(h.ctx, admin, admin.UserID(), core.UserInput{IsActive: boolp(false)})
	wantErr(t, "deactivate last admin", err, core.ErrConflict)
	_, err = h.app.UpdateUser(h.ctx, admin, admin.UserID(), core.UserInput{IsAdmin: boolp(false), IsActive: boolp(false)})
	wantErr(t, "demote and deactivate last admin", err, core.ErrConflict)
	stored, _ := h.db.GetUser(h.ctx, admin.UserID())
	if !stored.IsAdmin || !stored.IsActive {
		t.Fatalf("refused update changed the admin: %+v", stored)
	}

	// Reactivating the dormant admin makes the first one removable.
	_, err = h.app.UpdateUser(h.ctx, admin, dormant.UserID(), core.UserInput{IsActive: boolp(true)})
	noErr(t, "reactivate dormant admin", err)
	_, err = h.app.UpdateUser(h.ctx, admin, admin.UserID(), core.UserInput{IsAdmin: boolp(false)})
	noErr(t, "demote with another active admin", err)
	fresh, _ := h.db.GetUser(h.ctx, dormant.UserID())
	dormant = &core.Actor{User: fresh}
	_, err = h.app.UpdateUser(h.ctx, dormant, dormant.UserID(), core.UserInput{IsActive: boolp(false)})
	wantErr(t, "deactivate the remaining admin", err, core.ErrConflict)
}

func TestDeleteUserTransfersSoleOwnedSpaces(t *testing.T) {
	h := newRealHarness(t)
	admin := h.user("Admin", true)
	bob := h.user("Bob", false)
	carol := h.user("Carol", false)

	solo := h.space(bob, "Solo")
	shared := h.space(bob, "Shared")
	h.member(shared, carol, core.RoleOwner)
	joined := h.space(bob, "Joined")
	h.member(joined, admin, core.RoleEditor)

	noErr(t, "delete bob", h.app.DeleteUser(h.ctx, admin, bob.UserID()))
	if r := h.roleOf(solo, admin); r != core.RoleOwner {
		t.Fatalf("solo space: admin role %q, want owner", r)
	}
	if r := h.roleOf(shared, admin); r != "" {
		t.Fatalf("shared space kept an owner, admin should not be added, got %q", r)
	}
	if r := h.roleOf(shared, carol); r != core.RoleOwner {
		t.Fatalf("shared space: carol role %q", r)
	}
	if r := h.roleOf(joined, admin); r != core.RoleOwner {
		t.Fatalf("joined space: admin role %q, want upgrade to owner", r)
	}

	// An admin deleting themselves hands their spaces to another active admin.
	heir := h.user("Heir", true)
	mine := h.space(admin, "Mine")
	noErr(t, "admin deletes themselves", h.app.DeleteUser(h.ctx, admin, admin.UserID()))
	if r := h.roleOf(mine, heir); r != core.RoleOwner {
		t.Fatalf("self delete: heir role %q, want owner", r)
	}
	if r := h.roleOf(solo, heir); r != core.RoleOwner {
		t.Fatalf("self delete: second hand space role %q, want owner", r)
	}
}

func TestDeleteUserLeavesNoDanglingState(t *testing.T) {
	h := newRealHarness(t)
	admin := h.user("Admin", true)
	bob := h.user("Bob", true)
	carol := h.user("Carol", false)
	sp := h.space(bob, "Bob space")
	n := h.note(bob, sp, "", "Plan", "body")

	_, session, err := h.app.Login(h.ctx, bob.User.Email, "password123", "", "")
	noErr(t, "login", err)
	_, token, err := h.app.CreateAPIToken(h.ctx, bob, "ci", 0)
	noErr(t, "token", err)
	_, err = h.app.CreateComment(h.ctx, bob, core.CommentInput{NoteID: n.ID, Body: "bob was here"})
	noErr(t, "comment", err)
	_, err = h.app.CreateShare(h.ctx, bob, core.ShareInput{NoteID: n.ID, UserID: carol.UserID(), Access: core.AccessView})
	noErr(t, "share", err)
	noErr(t, "star", h.app.SetStar(h.ctx, bob, n.ID, true))
	plainInvite, err := h.app.CreateInvite(h.ctx, bob, core.InviteInput{Email: "new@example.test"})
	noErr(t, "invite", err)
	adminInvite, err := h.app.CreateInvite(h.ctx, bob, core.InviteInput{Email: "boss@example.test", IsAdmin: true})
	noErr(t, "admin invite", err)

	noErr(t, "delete bob", h.app.DeleteUser(h.ctx, admin, bob.UserID()))

	_, err = h.app.ActorFromSession(h.ctx, session)
	wantErr(t, "session after delete", err, core.ErrUnauthorized)
	_, err = h.app.ActorFromAPIToken(h.ctx, token)
	wantErr(t, "token after delete", err, core.ErrUnauthorized)
	if s, _ := h.db.ListSessions(h.ctx, bob.UserID()); len(s) != 0 {
		t.Fatalf("sessions kept: %d", len(s))
	}
	if tk, _ := h.db.ListAPITokens(h.ctx, bob.UserID()); len(tk) != 0 {
		t.Fatalf("tokens kept: %d", len(tk))
	}

	if _, _, err := h.app.GetNote(h.ctx, admin, n.ID); err != nil {
		t.Fatalf("GetNote after delete: %v", err)
	}
	comments, err := h.app.ListComments(h.ctx, admin, n.ID)
	noErr(t, "ListComments", err)
	if len(comments) != 1 || comments[0].Author == nil {
		t.Fatalf("comments = %+v", comments)
	}
	if a := comments[0].Author; a.ID != bob.UserID() || a.Name != deletedUserName {
		t.Fatalf("deleted author = %+v", a)
	}
	shares, err := h.app.ListShares(h.ctx, admin, n.ID)
	noErr(t, "ListShares", err)
	if len(shares) != 1 {
		t.Fatalf("share created by the deleted user was dropped: %d", len(shares))
	}
	if _, _, err := h.app.GetNote(h.ctx, carol, n.ID); err != nil {
		t.Fatalf("recipient of the deleted user's share: %v", err)
	}
	tree, err := h.app.SpaceTree(h.ctx, admin, sp.ID)
	if err != nil || len(tree) != 1 {
		t.Fatalf("SpaceTree = %v, %v", tree, err)
	}
	if r := h.roleOf(sp, admin); r != core.RoleOwner {
		t.Fatalf("space owner after delete: %q", r)
	}

	h.cfg.AllowSignup = false
	in := signupInput("new@example.test")
	in.InviteToken = plainInvite.Token
	u, err := h.app.Signup(h.ctx, in)
	if err != nil || u.IsAdmin {
		t.Fatalf("plain invite from a deleted user = %+v, %v", u, err)
	}
	in = signupInput("boss@example.test")
	in.InviteToken = adminInvite.Token
	_, err = h.app.Signup(h.ctx, in)
	wantErr(t, "admin invite from a deleted admin", err, core.ErrInvalid)
}

func TestDeactivationAndPasswordResetRevokeAccess(t *testing.T) {
	h := newRealHarness(t)
	admin := h.user("Admin", true)
	bob := h.user("Bob", false)
	_, session, err := h.app.Login(h.ctx, bob.User.Email, "password123", "", "")
	noErr(t, "login", err)
	_, token, err := h.app.CreateAPIToken(h.ctx, bob, "ci", 0)
	noErr(t, "token", err)

	_, err = h.app.UpdateUser(h.ctx, admin, bob.UserID(), core.UserInput{IsActive: boolp(false)})
	noErr(t, "deactivate", err)
	_, err = h.app.ActorFromSession(h.ctx, session)
	wantErr(t, "session of deactivated user", err, core.ErrUnauthorized)
	_, err = h.app.ActorFromAPIToken(h.ctx, token)
	wantErr(t, "token of deactivated user", err, core.ErrUnauthorized)
	_, _, err = h.app.Login(h.ctx, bob.User.Email, "password123", "", "")
	wantErr(t, "login of deactivated user", err, core.ErrUnauthorized)

	_, err = h.app.UpdateUser(h.ctx, admin, bob.UserID(), core.UserInput{IsActive: boolp(true)})
	noErr(t, "reactivate", err)
	_, session, err = h.app.Login(h.ctx, bob.User.Email, "password123", "", "")
	noErr(t, "login after reactivation", err)

	_, err = h.app.UpdateUser(h.ctx, admin, bob.UserID(), core.UserInput{Password: strp("reset-password-1")})
	noErr(t, "admin password reset", err)
	_, err = h.app.ActorFromSession(h.ctx, session)
	wantErr(t, "session after admin password reset", err, core.ErrUnauthorized)
}

func TestInviteRules(t *testing.T) {
	h := newRealHarness(t)
	h.cfg.AllowSignup = false
	admin := h.user("Admin", true)
	normal := h.user("Normal", false)

	_, err := h.app.CreateInvite(h.ctx, normal, core.InviteInput{Email: "x@example.test", IsAdmin: true})
	wantErr(t, "non-admin creates admin invite", err, core.ErrForbidden)

	bound, err := h.app.CreateInvite(h.ctx, admin, core.InviteInput{Email: "bound@example.test"})
	noErr(t, "bound invite", err)
	in := signupInput("intruder@example.test")
	in.InviteToken = bound.Token
	_, err = h.app.Signup(h.ctx, in)
	wantErr(t, "invite used by another email", err, core.ErrInvalid)
	in = signupInput("BOUND@example.test")
	in.InviteToken = bound.Token
	_, err = h.app.Signup(h.ctx, in)
	noErr(t, "invite used by its email", err)
	in = signupInput("bound2@example.test")
	in.InviteToken = bound.Token
	_, err = h.app.Signup(h.ctx, in)
	wantErr(t, "used invite", err, core.ErrInvalid)

	// An admin invite dies with its creator's admin rights.
	second := h.user("Second", true)
	promo, err := h.app.CreateInvite(h.ctx, second, core.InviteInput{IsAdmin: true})
	noErr(t, "admin invite", err)
	_, err = h.app.UpdateUser(h.ctx, admin, second.UserID(), core.UserInput{IsAdmin: boolp(false)})
	noErr(t, "demote creator", err)
	in = signupInput("climber@example.test")
	in.InviteToken = promo.Token
	_, err = h.app.Signup(h.ctx, in)
	wantErr(t, "admin invite from a demoted admin", err, core.ErrInvalid)

	// An invite into a space that was deleted still creates the account but
	// leaves no membership row behind.
	sp := h.space(admin, "Gone")
	spaceInvite, err := h.app.CreateInvite(h.ctx, admin, core.InviteInput{SpaceID: sp.ID, Role: core.RoleEditor})
	noErr(t, "space invite", err)
	noErr(t, "delete space", h.app.DeleteSpace(h.ctx, admin, sp.ID))
	in = signupInput("late@example.test")
	in.InviteToken = spaceInvite.Token
	late, err := h.app.Signup(h.ctx, in)
	noErr(t, "signup into deleted space", err)
	if ms, _ := h.db.ListMembershipsForUser(h.ctx, late.ID); len(ms) != 0 {
		t.Fatalf("dangling membership: %+v", ms[0])
	}

	// Signups racing one invite: exactly one wins.
	open, err := h.app.CreateInvite(h.ctx, admin, core.InviteInput{})
	noErr(t, "open invite", err)
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
	)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := signupInput(string(rune('a'+i)) + "-racer@example.test")
			in.InviteToken = open.Token
			if _, err := h.app.Signup(h.ctx, in); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			} else if !errors.Is(err, core.ErrInvalid) {
				t.Errorf("racer err = %v", err)
			}
		}(i)
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d signups used one invite", wins)
	}
}

func TestEmailUniquenessIgnoresCase(t *testing.T) {
	h := newRealHarness(t)
	admin := h.user("Admin", true)
	bob := h.user("Bob", false)
	h.user("Alice", false)
	_, err := h.app.CreateUser(h.ctx, admin, core.UserInput{Email: strp("ALICE@Example.test"), Password: strp("password123")})
	wantErr(t, "create with case variant", err, core.ErrExists)
	_, err = h.app.UpdateProfile(h.ctx, bob, core.ProfileInput{Email: strp("Alice@example.TEST")})
	wantErr(t, "profile to case variant", err, core.ErrExists)
	_, err = h.app.UpdateUser(h.ctx, admin, bob.UserID(), core.UserInput{Email: strp("alice@EXAMPLE.test")})
	wantErr(t, "admin update to case variant", err, core.ErrExists)
	_, err = h.app.Signup(h.ctx, signupInput("aLiCe@example.test"))
	wantErr(t, "signup with case variant", err, core.ErrExists)
}

func TestChangePasswordRevokesOtherSessions(t *testing.T) {
	h := newRealHarness(t)
	pat := h.user("Pat", false)
	_, current, err := h.app.Login(h.ctx, pat.User.Email, "password123", "", "")
	noErr(t, "login 1", err)
	_, other, err := h.app.Login(h.ctx, pat.User.Email, "password123", "", "")
	noErr(t, "login 2", err)
	actor, err := h.app.ActorFromSession(h.ctx, current)
	noErr(t, "resolve", err)

	err = h.app.ChangePassword(h.ctx, actor, "not-the-password", "new-password-1")
	wantErr(t, "wrong old password", err, core.ErrInvalid)
	if errors.Is(err, core.ErrUnauthorized) {
		t.Fatal("a wrong old password must not look like a lost session")
	}
	if _, err := h.app.ActorFromSession(h.ctx, other); err != nil {
		t.Fatalf("refused change revoked a session: %v", err)
	}

	noErr(t, "change", h.app.ChangePassword(h.ctx, actor, "password123", "new-password-1"))
	if _, err := h.app.ActorFromSession(h.ctx, current); err != nil {
		t.Fatalf("current session revoked: %v", err)
	}
	_, err = h.app.ActorFromSession(h.ctx, other)
	wantErr(t, "other session", err, core.ErrUnauthorized)
	_, _, err = h.app.Login(h.ctx, pat.User.Email, "password123", "", "")
	wantErr(t, "old password", err, core.ErrUnauthorized)

	// Through an API token there is no current session, so every session goes.
	fresh, err := h.app.ActorFromSession(h.ctx, current)
	noErr(t, "resolve again", err)
	_, plain, err := h.app.CreateAPIToken(h.ctx, fresh, "ci", 0)
	noErr(t, "token", err)
	tokActor, err := h.app.ActorFromAPIToken(h.ctx, plain)
	noErr(t, "token actor", err)
	noErr(t, "change via token", h.app.ChangePassword(h.ctx, tokActor, "new-password-1", "new-password-2"))
	_, err = h.app.ActorFromSession(h.ctx, current)
	wantErr(t, "session after token change", err, core.ErrUnauthorized)
}

// --- spaces -----------------------------------------------------------------

func TestLastOwnerCannotBeRemovedOrDemoted(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	admin := h.user("Admin", true)
	sp := h.space(owner, "Docs")

	wantErr(t, "owner removes self", h.app.RemoveMember(h.ctx, owner, sp.ID, owner.UserID()), core.ErrConflict)
	_, err := h.app.UpdateMember(h.ctx, owner, sp.ID, owner.UserID(), core.RoleEditor)
	wantErr(t, "owner demotes self", err, core.ErrConflict)
	wantErr(t, "admin removes last owner", h.app.RemoveMember(h.ctx, admin, sp.ID, owner.UserID()), core.ErrConflict)
	_, err = h.app.UpdateMember(h.ctx, admin, sp.ID, owner.UserID(), core.RoleViewer)
	wantErr(t, "admin demotes last owner", err, core.ErrConflict)
	if r := h.roleOf(sp, owner); r != core.RoleOwner {
		t.Fatalf("owner role = %q", r)
	}

	co := h.user("Co", false)
	_, err = h.app.AddMember(h.ctx, owner, sp.ID, co.UserID(), core.RoleOwner)
	noErr(t, "add co-owner", err)
	_, err = h.app.UpdateMember(h.ctx, owner, sp.ID, owner.UserID(), core.RoleEditor)
	noErr(t, "step down with a co-owner", err)
	wantErr(t, "remove the now last owner", h.app.RemoveMember(h.ctx, co, sp.ID, co.UserID()), core.ErrConflict)
}

func TestOnlyOwnersGrantOrRevokeOwnership(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	admin := h.user("Admin", true)
	sp := h.space(owner, "Docs")
	spAdmin := h.user("SpaceAdmin", false)
	h.member(sp, spAdmin, core.RoleAdmin)
	editor := h.user("Editor", false)
	h.member(sp, editor, core.RoleEditor)
	newbie := h.user("Newbie", false)

	_, err := h.app.UpdateMember(h.ctx, spAdmin, sp.ID, spAdmin.UserID(), core.RoleOwner)
	wantErr(t, "space admin promotes self", err, core.ErrForbidden)
	_, err = h.app.UpdateMember(h.ctx, spAdmin, sp.ID, editor.UserID(), core.RoleOwner)
	wantErr(t, "space admin promotes another", err, core.ErrForbidden)
	_, err = h.app.AddMember(h.ctx, spAdmin, sp.ID, newbie.UserID(), core.RoleOwner)
	wantErr(t, "space admin adds an owner", err, core.ErrForbidden)
	if r := h.roleOf(sp, spAdmin); r != core.RoleAdmin {
		t.Fatalf("refused promotion changed role to %q", r)
	}

	_, err = h.app.UpdateMember(h.ctx, owner, sp.ID, editor.UserID(), core.RoleOwner)
	noErr(t, "owner grants owner", err)
	_, err = h.app.UpdateMember(h.ctx, spAdmin, sp.ID, editor.UserID(), core.RoleViewer)
	wantErr(t, "space admin demotes an owner", err, core.ErrForbidden)
	wantErr(t, "space admin removes an owner", h.app.RemoveMember(h.ctx, spAdmin, sp.ID, editor.UserID()), core.ErrForbidden)

	_, err = h.app.AddMember(h.ctx, spAdmin, sp.ID, newbie.UserID(), core.RoleViewer)
	noErr(t, "space admin adds a viewer", err)
	_, err = h.app.UpdateMember(h.ctx, spAdmin, sp.ID, newbie.UserID(), core.RoleCommenter)
	noErr(t, "space admin changes a non-owner", err)
	noErr(t, "space admin removes a non-owner", h.app.RemoveMember(h.ctx, spAdmin, sp.ID, newbie.UserID()))

	_, err = h.app.UpdateMember(h.ctx, admin, sp.ID, spAdmin.UserID(), core.RoleOwner)
	noErr(t, "instance admin grants owner", err)
}

func TestAddMemberNeedsAnActiveUser(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	gone := h.user("Gone", false)
	gone.User.IsActive = false
	noErr(t, "deactivate", h.db.UpdateUser(h.ctx, gone.User))

	_, err := h.app.AddMember(h.ctx, owner, sp.ID, core.NewID(), core.RoleViewer)
	wantErr(t, "missing user", err, core.ErrNotFound)
	_, err = h.app.AddMember(h.ctx, owner, sp.ID, gone.UserID(), core.RoleViewer)
	wantErr(t, "inactive user", err, core.ErrConflict)
	if r := h.roleOf(sp, gone); r != "" {
		t.Fatalf("inactive user added as %q", r)
	}
}

func TestUpdateSpaceSlugRules(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	alpha := h.space(owner, "Alpha")
	beta := h.space(owner, "Beta")

	_, err := h.app.UpdateSpace(h.ctx, owner, beta.ID, core.SpaceInput{Slug: strp("Alpha")})
	wantErr(t, "taken slug", err, core.ErrExists)
	for _, bad := range []string{"", "   ", "!!!", "--"} {
		_, err = h.app.UpdateSpace(h.ctx, owner, beta.ID, core.SpaceInput{Slug: strp(bad)})
		wantErr(t, "empty slug "+bad, err, core.ErrInvalid)
	}
	stored, _ := h.db.GetSpace(h.ctx, beta.ID)
	if stored.Slug != "beta" {
		t.Fatalf("refused slug changes renamed the space to %q", stored.Slug)
	}
	got, err := h.app.UpdateSpace(h.ctx, owner, beta.ID, core.SpaceInput{Slug: strp("Gamma Ray")})
	if err != nil || got.Slug != "gamma-ray" {
		t.Fatalf("rename = %+v, %v", got, err)
	}
	_, err = h.app.UpdateSpace(h.ctx, owner, alpha.ID, core.SpaceInput{Slug: strp("alpha")})
	noErr(t, "same slug", err)

	// A blank slug on create falls back to the name.
	sp, err := h.app.CreateSpace(h.ctx, owner, core.SpaceInput{Name: strp("Delta"), Slug: strp(" ")})
	if err != nil || sp.Slug != "delta" {
		t.Fatalf("create with blank slug = %+v, %v", sp, err)
	}
	_, err = h.app.CreateSpace(h.ctx, owner, core.SpaceInput{Name: strp("Epsilon"), Slug: strp("???")})
	wantErr(t, "create with empty slug", err, core.ErrInvalid)
}

func TestDeleteSpaceClearsDefaultSpace(t *testing.T) {
	h := newRealHarness(t)
	admin := h.user("Admin", true)
	sp := h.space(admin, "Home")
	_, err := h.app.UpdateSettings(h.ctx, admin, &core.Settings{InstanceName: "x", DefaultSpaceID: sp.ID})
	noErr(t, "settings", err)
	noErr(t, "delete space", h.app.DeleteSpace(h.ctx, admin, sp.ID))
	s, err := h.app.GetSettings(h.ctx, admin)
	noErr(t, "get settings", err)
	if s.DefaultSpaceID != "" {
		t.Fatalf("default space still points at the deleted space %s", s.DefaultSpaceID)
	}
}

// --- items and trash --------------------------------------------------------

func TestMoveAndCreateRespectTrashAndSpace(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	other := h.space(owner, "Other")
	a := h.folder(owner, sp, "", "A")
	b := h.note(owner, sp, "", "B", "")
	far := h.folder(owner, other, "", "Far")
	noErr(t, "trash a", h.app.TrashItem(h.ctx, owner, a.ID))

	_, err := h.app.MoveNote(h.ctx, owner, b.ID, a.ID, 0)
	wantErr(t, "move into trashed folder", err, core.ErrConflict)
	_, err = h.app.CreateNote(h.ctx, owner, core.NoteInput{SpaceID: sp.ID, ParentID: a.ID, Title: "x"})
	wantErr(t, "create in trashed folder", err, core.ErrConflict)
	_, err = h.app.CreateFolder(h.ctx, owner, core.FolderInput{SpaceID: sp.ID, ParentID: a.ID, Name: "x"})
	wantErr(t, "folder in trashed folder", err, core.ErrConflict)
	_, err = h.app.MoveNote(h.ctx, owner, b.ID, far.ID, 0)
	wantErr(t, "move into another space", err, core.ErrInvalid)
	_, err = h.app.CreateNote(h.ctx, owner, core.NoteInput{SpaceID: sp.ID, ParentID: far.ID, Title: "x"})
	wantErr(t, "create with folder in another space", err, core.ErrInvalid)
	if row := h.row(b.ID); row.ParentID != "" || row.Path != "b.md" {
		t.Fatalf("refused move changed the row: %+v", row)
	}
	if !h.exists(sp, "b.md") || h.exists(sp, "x.md") {
		t.Fatal("refused operations changed the disk")
	}
}

func TestTrashAndRestoreFolderWithChildren(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	a := h.folder(owner, sp, "", "A")
	sub := h.folder(owner, sp, a.ID, "Sub")
	n := h.note(owner, sp, sub.ID, "Inner", "inner body")
	f := h.upload(owner, sp, a.ID, "pic.png", "png")

	noErr(t, "trash a", h.app.TrashItem(h.ctx, owner, a.ID))
	if h.exists(sp, "A") {
		t.Fatal("trashed folder still on disk")
	}
	for _, id := range []string{a.ID, sub.ID, n.ID, f.ID} {
		if !itemDeleted(h.item(id)) {
			t.Fatalf("item %s not trashed with its folder", id)
		}
	}
	trash, err := h.app.ListTrashItems(h.ctx, owner, sp.ID)
	noErr(t, "list trash", err)
	if len(trash) != 1 || trash[0].Folder == nil || trash[0].Folder.ID != a.ID {
		t.Fatalf("trash lists %d items, want only the folder", len(trash))
	}
	got, _, err := h.app.GetNote(h.ctx, owner, n.ID)
	noErr(t, "GetNote trashed child", err)
	_ = got

	_, err = h.app.RestoreItem(h.ctx, owner, a.ID)
	noErr(t, "restore a", err)
	for id, want := range map[string]string{a.ID: "A", sub.ID: "A/Sub", n.ID: "A/Sub/inner.md", f.ID: "A/pic.png"} {
		it := h.item(id)
		if itemDeleted(it) || it.Path() != want {
			t.Fatalf("restored %s: path %q deleted %v, want %q", id, it.Path(), itemDeleted(it), want)
		}
		if !h.exists(sp, want) {
			t.Fatalf("restored %s missing on disk", want)
		}
	}
	if got := h.body(sp, "A/Sub/inner.md"); got != "inner body" {
		t.Fatalf("restored body = %q", got)
	}
	if row := h.row(n.ID); row.ParentID != sub.ID {
		t.Fatalf("restored note parent = %q", row.ParentID)
	}
}

func TestRestoreUnderTrashedParentGoesToRoot(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	a := h.folder(owner, sp, "", "A")
	b := h.note(owner, sp, a.ID, "B", "bee")
	noErr(t, "trash b", h.app.TrashItem(h.ctx, owner, b.ID))
	h.clock.Advance(time.Second)
	noErr(t, "trash a", h.app.TrashItem(h.ctx, owner, a.ID))

	_, err := h.app.RestoreItem(h.ctx, owner, b.ID)
	noErr(t, "restore b", err)
	row := h.row(b.ID)
	if row.Deleted || row.ParentID != "" || row.Path != "b.md" {
		t.Fatalf("restored child = %+v, want a live root note", row)
	}
	got, _, err := h.app.GetNote(h.ctx, owner, b.ID)
	if err != nil || got.Content != "bee" {
		t.Fatalf("GetNote restored = %+v, %v", got, err)
	}
	if !itemDeleted(h.item(a.ID)) {
		t.Fatal("parent left the trash")
	}

	_, err = h.app.RestoreItem(h.ctx, owner, a.ID)
	noErr(t, "restore a", err)
	if row := h.row(b.ID); row.ParentID != "" || row.Path != "b.md" || row.Deleted {
		t.Fatalf("restoring the parent moved the child back: %+v", row)
	}
}

func TestRestoreNameCollisionPicksFreeName(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	first := h.upload(owner, sp, "", "report.pdf", "one")
	noErr(t, "trash", h.app.TrashItem(h.ctx, owner, first.ID))
	h.upload(owner, sp, "", "Report.PDF", "two")
	it, err := h.app.RestoreItem(h.ctx, owner, first.ID)
	noErr(t, "restore", err)
	if it.Path() != "report (2).pdf" || !h.exists(sp, "report (2).pdf") {
		t.Fatalf("restored path = %q", it.Path())
	}
}

func TestRestoreParentKeepsSeparatelyTrashedChild(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	a := h.folder(owner, sp, "", "A")
	b := h.folder(owner, sp, a.ID, "B")
	c := h.note(owner, sp, b.ID, "C", "sea")
	noErr(t, "trash b", h.app.TrashItem(h.ctx, owner, b.ID))
	h.clock.Advance(time.Second)
	noErr(t, "trash a", h.app.TrashItem(h.ctx, owner, a.ID))
	_, err := h.app.RestoreItem(h.ctx, owner, a.ID)
	noErr(t, "restore a", err)

	for _, id := range []string{b.ID, c.ID} {
		if !itemDeleted(h.item(id)) {
			t.Fatalf("separately trashed item %s left the trash", id)
		}
	}
	if _, _, err := h.app.GetNote(h.ctx, owner, c.ID); err != nil {
		t.Fatalf("GetNote trashed c: %v", err)
	}
	_, err = h.app.RestoreItem(h.ctx, owner, b.ID)
	noErr(t, "restore b", err)
	if it := h.item(b.ID); itemDeleted(it) || it.Folder.ParentID != a.ID || it.Path() != "A/B" {
		t.Fatalf("restored b = %+v", it.Folder)
	}
	if row := h.row(c.ID); row.Deleted || row.Path != "A/B/c.md" || row.ParentID != b.ID {
		t.Fatalf("restored c = %+v", row)
	}
	got, _, err := h.app.GetNote(h.ctx, owner, c.ID)
	if err != nil || got.Content != "sea" {
		t.Fatalf("GetNote c = %+v, %v", got, err)
	}
}

func TestRestoreInsideTrashedFolderIsRefused(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	a := h.folder(owner, sp, "", "A")
	b := h.note(owner, sp, a.ID, "B", "")
	f := h.upload(owner, sp, a.ID, "f.txt", "x")
	noErr(t, "trash a", h.app.TrashItem(h.ctx, owner, a.ID))
	for _, id := range []string{b.ID, f.ID} {
		_, err := h.app.RestoreItem(h.ctx, owner, id)
		wantErr(t, "restore child of trashed folder", err, core.ErrConflict)
		wantErr(t, "delete child of trashed folder", h.app.DeleteItem(h.ctx, owner, id), core.ErrConflict)
		if !itemDeleted(h.item(id)) {
			t.Fatal("refused restore changed the child")
		}
	}
	_, err := h.app.RestoreItem(h.ctx, owner, a.ID)
	noErr(t, "restore a", err)
	if row := h.row(b.ID); row.Deleted || row.ParentID != a.ID {
		t.Fatalf("child after parent restore = %+v", row)
	}
}

func TestRestoreFollowsMovedParentAndSurvivesMissingParent(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	a := h.folder(owner, sp, "", "A")
	b := h.note(owner, sp, a.ID, "B", "")
	h.note(owner, sp, a.ID, "Keep", "")
	d := h.folder(owner, sp, "", "D")
	noErr(t, "trash b", h.app.TrashItem(h.ctx, owner, b.ID))
	_, err := h.app.MoveItem(h.ctx, owner, a.ID, d.ID, 0)
	noErr(t, "move a", err)
	_, err = h.app.RestoreItem(h.ctx, owner, b.ID)
	noErr(t, "restore b", err)
	if row := h.row(b.ID); row.ParentID != a.ID || row.Path != "D/A/b.md" {
		t.Fatalf("restore after parent move = %+v", row)
	}
	if _, _, err := h.app.GetNote(h.ctx, owner, b.ID); err != nil {
		t.Fatal(err)
	}

	// The parent disappears outside the app while the child is in the trash.
	noErr(t, "trash b again", h.app.TrashItem(h.ctx, owner, b.ID))
	h.removeDisk(sp, "D/A")
	h.rescan(sp)
	if _, err := h.app.loadItem(h.ctx, a.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("vanished folder row kept: %v", err)
	}
	_, err = h.app.RestoreItem(h.ctx, owner, b.ID)
	noErr(t, "restore orphan", err)
	if row := h.row(b.ID); row.ParentID != "" || row.Path != "b.md" {
		t.Fatalf("restore with missing parent = %+v", row)
	}
}

func TestDeleteItemRemovesSharesLinksAndComments(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	friend := h.user("Friend", false)
	sp := h.space(owner, "Docs")
	parent := h.folder(owner, sp, "", "Parent")
	child := h.note(owner, sp, parent.ID, "Child", "")
	file := h.upload(owner, sp, parent.ID, "a.txt", "x")
	loose := h.note(owner, sp, "", "Loose", "")

	decorate := func(id string) *core.PublicLink {
		t.Helper()
		_, err := h.app.CreateShare(h.ctx, owner, core.ShareInput{NoteID: id, UserID: friend.UserID(), Access: core.AccessEdit, IncludeChildren: true})
		noErr(t, "share", err)
		l, err := h.app.CreatePublicLink(h.ctx, owner, core.PublicLinkInput{NoteID: id, Access: core.AccessView})
		noErr(t, "link", err)
		_, err = h.app.CreateComment(h.ctx, owner, core.CommentInput{NoteID: id, Body: "hi"})
		noErr(t, "comment", err)
		return l
	}
	parentLink := decorate(parent.ID)
	decorate(child.ID)
	decorate(file.ID)
	looseLink := decorate(loose.ID)

	check := func(ids ...string) {
		t.Helper()
		for _, id := range ids {
			if s, _ := h.db.ListSharesForNote(h.ctx, id); len(s) != 0 {
				t.Fatalf("shares of deleted item %s kept", id)
			}
			if l, _ := h.db.ListPublicLinks(h.ctx, id); len(l) != 0 {
				t.Fatalf("links of deleted item %s kept", id)
			}
			if c, _ := h.db.ListComments(h.ctx, id); len(c) != 0 {
				t.Fatalf("comments of deleted item %s kept", id)
			}
			if _, err := h.app.loadItem(h.ctx, id); !errors.Is(err, core.ErrNotFound) {
				t.Fatalf("row of deleted item %s kept: %v", id, err)
			}
		}
	}
	noErr(t, "delete parent", h.app.DeleteItem(h.ctx, owner, parent.ID))
	check(parent.ID, child.ID, file.ID)
	if h.exists(sp, "Parent") {
		t.Fatal("deleted folder still on disk")
	}
	_, _, err := h.app.ResolvePublicItem(h.ctx, parentLink.Token, "")
	wantErr(t, "link to deleted folder", err, core.ErrNotFound)

	noErr(t, "trash loose", h.app.TrashNote(h.ctx, owner, loose.ID))
	noErr(t, "empty trash", h.app.EmptyTrash(h.ctx, owner, sp.ID))
	check(loose.ID)
	_, _, err = h.app.ResolvePublicLink(h.ctx, looseLink.Token, "")
	wantErr(t, "link to emptied note", err, core.ErrNotFound)
	mine, err := h.app.ListSharedWithMe(h.ctx, friend)
	if err != nil || len(mine) != 0 {
		t.Fatalf("ListSharedWithMe = %v, %v", mine, err)
	}
}

func TestTrashedItemsAreClosedToLinksAndShares(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	friend := h.user("Friend", false)
	sp := h.space(owner, "Docs")
	parent := h.folder(owner, sp, "", "Parent")
	child := h.note(owner, sp, parent.ID, "Child", "secret child")

	link, err := h.app.CreatePublicLink(h.ctx, owner, core.PublicLinkInput{NoteID: parent.ID, Access: core.AccessComment, IncludeChildren: true})
	noErr(t, "link", err)
	linkActor, item, err := h.app.ResolvePublicItem(h.ctx, link.Token, "")
	noErr(t, "resolve", err)
	if item.Folder == nil || item.Folder.ID != parent.ID {
		t.Fatalf("link resolves to %+v", item)
	}
	if _, _, err := h.app.GetNote(h.ctx, linkActor, child.ID); err != nil {
		t.Fatalf("link reads live child: %v", err)
	}
	_, err = h.app.CreateShare(h.ctx, owner, core.ShareInput{NoteID: child.ID, UserID: friend.UserID(), Access: core.AccessEdit})
	noErr(t, "share", err)

	noErr(t, "trash child", h.app.TrashNote(h.ctx, owner, child.ID))
	_, _, err = h.app.GetNote(h.ctx, linkActor, child.ID)
	wantErr(t, "link reads trashed child", err, core.ErrNotFound)
	body := "edit"
	_, err = h.app.UpdateNote(h.ctx, friend, child.ID, core.NoteUpdate{Content: &body})
	wantErr(t, "share edits trashed note", err, core.ErrConflict)
	_, err = h.app.CreateComment(h.ctx, friend, core.CommentInput{NoteID: child.ID, Body: "x"})
	wantErr(t, "share comments on trashed note", err, core.ErrConflict)

	noErr(t, "trash parent", h.app.TrashItem(h.ctx, owner, parent.ID))
	_, _, err = h.app.ResolvePublicItem(h.ctx, link.Token, "")
	wantErr(t, "link to trashed target", err, core.ErrNotFound)
	_, err = h.app.GetItem(h.ctx, linkActor, parent.ID)
	wantErr(t, "held link actor reads trashed target", err, core.ErrNotFound)
}

func TestClientIDReplay(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	other := h.user("Other", false)
	sp := h.space(owner, "Docs")
	h.member(sp, other, core.RoleEditor)

	noteID, folderID, fileID := core.NewID(), core.NewID(), core.NewID()
	n1, err := h.app.CreateNote(h.ctx, owner, core.NoteInput{ID: noteID, SpaceID: sp.ID, Title: "Offline", Content: "one"})
	noErr(t, "create note", err)
	n2, err := h.app.CreateNote(h.ctx, owner, core.NoteInput{ID: noteID, SpaceID: sp.ID, Title: "Offline", Content: "one"})
	if err != nil || n2.ID != n1.ID || n2.Path != n1.Path {
		t.Fatalf("replayed note = %+v, %v", n2, err)
	}
	f1, err := h.app.CreateFolder(h.ctx, owner, core.FolderInput{ID: folderID, SpaceID: sp.ID, Name: "Box"})
	noErr(t, "create folder", err)
	f2, err := h.app.CreateFolder(h.ctx, owner, core.FolderInput{ID: folderID, SpaceID: sp.ID, Name: "Box"})
	if err != nil || f2.ID != f1.ID {
		t.Fatalf("replayed folder = %+v, %v", f2, err)
	}
	u1, err := h.app.UploadFile(h.ctx, owner, core.FileUpload{ID: fileID, SpaceID: sp.ID, ParentID: folderID, Name: "a.txt"}, strings.NewReader("aaa"))
	noErr(t, "upload", err)
	u2, err := h.app.UploadFile(h.ctx, owner, core.FileUpload{ID: fileID, SpaceID: sp.ID, ParentID: folderID, Name: "a.txt"}, strings.NewReader("aaa"))
	if err != nil || u2.ID != u1.ID || u2.Path != "Box/a.txt" {
		t.Fatalf("replayed upload = %+v, %v", u2, err)
	}

	_, err = h.app.CreateFolder(h.ctx, other, core.FolderInput{ID: folderID, SpaceID: sp.ID, Name: "Other"})
	wantErr(t, "another user replays a folder id", err, core.ErrExists)
	_, err = h.app.CreateNote(h.ctx, owner, core.NoteInput{ID: fileID, SpaceID: sp.ID, Title: "Clash"})
	wantErr(t, "note with a file id", err, core.ErrExists)
	_, err = h.app.UploadFile(h.ctx, owner, core.FileUpload{ID: "not-an-id", SpaceID: sp.ID, Name: "b.txt"}, strings.NewReader("b"))
	wantErr(t, "malformed id", err, core.ErrInvalid)

	tree, err := h.app.SpaceTree(h.ctx, owner, sp.ID)
	noErr(t, "tree", err)
	if len(tree) != 2 {
		t.Fatalf("replays created extra items: %d roots", len(tree))
	}
}

// TestTrashedPathIsReusable covers a trashed item keeping its path in the
// index. Live paths are unique, so a new note, a rename or a move onto that
// path must succeed, and the trashed item returns under a free name.
func TestTrashedPathIsReusable(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	plan := h.note(owner, sp, "", "Plan", "old plan")
	other := h.note(owner, sp, "", "Other", "other body")
	box := h.folder(owner, sp, "", "Box")
	boxed := h.note(owner, sp, box.ID, "Plan", "boxed")
	noErr(t, "trash plan", h.app.TrashNote(h.ctx, owner, plan.ID))

	fresh, err := h.app.CreateNote(h.ctx, owner, core.NoteInput{SpaceID: sp.ID, Title: "Plan", Content: "new plan"})
	noErr(t, "create with a trashed note's title", err)
	if fresh.Path != "plan.md" {
		t.Fatalf("fresh path = %s", fresh.Path)
	}
	noErr(t, "trash fresh", h.app.TrashNote(h.ctx, owner, fresh.ID))
	renamed, err := h.app.UpdateNote(h.ctx, owner, other.ID, core.NoteUpdate{Title: strp("Plan")})
	noErr(t, "rename to a trashed note's title", err)
	noErr(t, "trash renamed", h.app.TrashNote(h.ctx, owner, renamed.ID))
	moved, err := h.app.MoveNote(h.ctx, owner, boxed.ID, "", 0)
	noErr(t, "move onto a trashed note's path", err)
	if moved.Path != "plan.md" {
		t.Fatalf("moved path = %s", moved.Path)
	}

	for _, id := range []string{plan.ID, fresh.ID, renamed.ID} {
		noErr(t, "restore", h.app.RestoreNote(h.ctx, owner, id))
	}
	paths := map[string]string{}
	for id, want := range map[string]string{plan.ID: "old plan", fresh.ID: "new plan", other.ID: "other body", boxed.ID: "boxed"} {
		got, _, err := h.app.GetNote(h.ctx, owner, id)
		if err != nil || got.Content != want {
			t.Fatalf("GetNote %s = %+v, %v", id, got, err)
		}
		key := strings.ToLower(got.Path)
		if prev, dup := paths[key]; dup {
			t.Fatalf("note %s shares path %s with %s", id, got.Path, prev)
		}
		paths[key] = id
		if !h.exists(sp, got.Path) {
			t.Fatalf("note %s missing at %s", id, got.Path)
		}
	}
}

func TestLinkGrantCapsStoredAccess(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	n := h.note(owner, sp, "", "Plan", "")
	other := h.space(owner, "Other")
	stranger := h.note(owner, other, "", "Stranger", "")

	stored := &core.PublicLink{ID: core.NewID(), Token: "tok", NoteID: n.ID, SpaceID: sp.ID, Access: core.AccessManage}
	noErr(t, "store link", h.db.CreatePublicLink(h.ctx, stored))
	g, err := h.app.noteGrant(h.ctx, &core.Actor{Link: stored}, h.row(n.ID))
	noErr(t, "grant", err)
	if g.Access != core.AccessComment {
		t.Fatalf("link grant = %s, want comment", g.Access)
	}
	// A link whose space does not match the note grants nothing.
	wrong := &core.PublicLink{ID: "x", NoteID: stranger.ID, SpaceID: sp.ID, Access: core.AccessView}
	g, err = h.app.noteGrant(h.ctx, &core.Actor{Link: wrong}, h.row(stranger.ID))
	noErr(t, "grant", err)
	if g.Access != core.AccessNone {
		t.Fatalf("cross-space link grant = %s", g.Access)
	}
}

// --- shares -----------------------------------------------------------------

func TestShareRules(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	n := h.note(owner, sp, "", "Plan", "")
	editor := h.user("Editor", false)
	h.member(sp, editor, core.RoleEditor)
	friend := h.user("Friend", false)
	gone := h.user("Gone", false)
	gone.User.IsActive = false
	noErr(t, "deactivate", h.db.UpdateUser(h.ctx, gone.User))

	for _, acc := range []core.Access{core.AccessNone, core.AccessManage + 1, -1} {
		_, err := h.app.CreateShare(h.ctx, owner, core.ShareInput{NoteID: n.ID, UserID: friend.UserID(), Access: acc})
		wantErr(t, "share access "+acc.String(), err, core.ErrInvalid)
	}
	_, err := h.app.CreateShare(h.ctx, editor, core.ShareInput{NoteID: n.ID, UserID: friend.UserID(), Access: core.AccessView})
	wantErr(t, "editor shares", err, core.ErrForbidden)
	_, err = h.app.CreateShare(h.ctx, owner, core.ShareInput{NoteID: n.ID, UserID: gone.UserID(), Access: core.AccessView})
	wantErr(t, "share with inactive user", err, core.ErrConflict)
	if s, _ := h.db.ListSharesForNote(h.ctx, n.ID); len(s) != 0 {
		t.Fatal("refused share was stored")
	}
	s, err := h.app.CreateShare(h.ctx, owner, core.ShareInput{NoteID: n.ID, UserID: friend.UserID(), Access: core.AccessView})
	noErr(t, "share", err)
	_, err = h.app.UpdateShare(h.ctx, owner, s.ID, core.AccessManage+1, false)
	wantErr(t, "update share past manage", err, core.ErrInvalid)

	l, err := h.app.CreatePublicLink(h.ctx, owner, core.PublicLinkInput{NoteID: n.ID, Access: core.AccessView})
	noErr(t, "link", err)
	up, err := h.app.UpdatePublicLink(h.ctx, owner, l.ID, core.PublicLinkInput{Access: core.AccessManage})
	if err != nil || up.Access != core.AccessComment {
		t.Fatalf("update link = %+v, %v", up, err)
	}
}

// TestRemovedMemberLosesAccessImmediately documents the decision that shares a
// member created while in the space stay valid after the member leaves. A share
// is a grant from the note's managers, not from the person who clicked, and it
// can be revoked by any remaining manager.
func TestRemovedMemberLosesAccessImmediately(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	n := h.note(owner, sp, "", "Plan", "")
	manager := h.user("Manager", false)
	h.member(sp, manager, core.RoleAdmin)
	guest := h.user("Guest", false)

	_, err := h.app.CreateShare(h.ctx, manager, core.ShareInput{NoteID: n.ID, UserID: guest.UserID(), Access: core.AccessView})
	noErr(t, "manager shares", err)
	noErr(t, "remove manager", h.app.RemoveMember(h.ctx, owner, sp.ID, manager.UserID()))

	_, _, err = h.app.GetNote(h.ctx, manager, n.ID)
	wantErr(t, "removed member reads", err, core.ErrNotFound)
	_, err = h.app.CreateShare(h.ctx, manager, core.ShareInput{NoteID: n.ID, UserID: owner.UserID(), Access: core.AccessView})
	wantErr(t, "removed member shares", err, core.ErrNotFound)
	if _, _, err := h.app.GetNote(h.ctx, guest, n.ID); err != nil {
		t.Fatalf("share created by the removed member stopped working: %v", err)
	}
}

// --- AI providers -----------------------------------------------------------

func TestAIProviderOwnerNeverChanges(t *testing.T) {
	h := newRealHarness(t)
	admin := h.user("Admin", true)
	user := h.user("User", false)
	other := h.user("Other", false)

	_, err := h.app.SaveAIProvider(h.ctx, user, &core.AIProviderConfig{ID: "mine", BaseURL: "http://127.0.0.1:2", Enabled: true, OwnerID: other.UserID()})
	noErr(t, "user creates", err)
	owner := func() string {
		p, err := h.db.GetAIProvider(h.ctx, "mine")
		noErr(t, "load", err)
		return p.OwnerID
	}
	if owner() != user.UserID() {
		t.Fatalf("create owner = %q", owner())
	}
	for _, try := range []struct {
		actor *core.Actor
		owner string
	}{
		{user, other.UserID()},
		{user, ""},
		{admin, ""},
		{admin, admin.UserID()},
		{admin, other.UserID()},
	} {
		_, err := h.app.SaveAIProvider(h.ctx, try.actor, &core.AIProviderConfig{ID: "mine", BaseURL: "http://127.0.0.1:3", Enabled: true, OwnerID: try.owner})
		noErr(t, "update", err)
		if owner() != user.UserID() {
			t.Fatalf("update by %s with owner %q changed owner to %q", try.actor.User.Name, try.owner, owner())
		}
	}
	created, err := h.app.SaveAIProvider(h.ctx, admin, &core.AIProviderConfig{ID: "planted", BaseURL: "http://127.0.0.1:4", OwnerID: user.UserID()})
	if err != nil || created.OwnerID != "" {
		t.Fatalf("admin plants a personal provider for another user = %+v, %v", created, err)
	}

	noErr(t, "delete user", h.app.DeleteUser(h.ctx, admin, user.UserID()))
	if _, err := h.db.GetAIProvider(h.ctx, "mine"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("personal provider survived its owner: %v", err)
	}
	if _, err := h.db.GetAIProvider(h.ctx, "planted"); err != nil {
		t.Fatalf("instance provider removed with a user: %v", err)
	}
}

// --- comments ---------------------------------------------------------------

func TestCommentEditOnTrashedNoteIsRefused(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	n := h.note(owner, sp, "", "Plan", "")
	c, err := h.app.CreateComment(h.ctx, owner, core.CommentInput{NoteID: n.ID, Body: "first"})
	noErr(t, "comment", err)
	noErr(t, "trash", h.app.TrashNote(h.ctx, owner, n.ID))
	_, err = h.app.UpdateComment(h.ctx, owner, c.ID, "edited")
	wantErr(t, "edit comment on trashed note", err, core.ErrConflict)
	stored, _ := h.db.GetComment(h.ctx, c.ID)
	if !strings.Contains(stored.Body, "first") {
		t.Fatalf("refused edit changed body to %q", stored.Body)
	}
}
