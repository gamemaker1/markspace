package app

import (
	"errors"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

func signupInput(email string) core.SignupInput {
	return core.SignupInput{Email: email, Name: "Alice Smith", Password: "password123"}
}

func TestSignupFirstUserBecomesAdmin(t *testing.T) {
	h := newRealHarness(t)
	setup, err := h.app.NeedsSetup(h.ctx)
	if err != nil || !setup {
		t.Fatalf("NeedsSetup = %v, %v", setup, err)
	}
	u, err := h.app.Signup(h.ctx, signupInput(" Alice@Example.test "))
	if err != nil {
		t.Fatalf("Signup: %v", err)
	}
	if !u.IsAdmin || !u.IsActive {
		t.Fatalf("first user admin=%v active=%v", u.IsAdmin, u.IsActive)
	}
	if u.Email != "alice@example.test" {
		t.Fatalf("email not normalized: %q", u.Email)
	}
	actor := &core.Actor{User: u}
	spaces, err := h.app.ListSpaces(h.ctx, actor)
	if err != nil || len(spaces) != 1 {
		t.Fatalf("ListSpaces = %v, %v", spaces, err)
	}
	if spaces[0].Slug != "alice-smith" || spaces[0].MyRole != core.RoleOwner {
		t.Fatalf("personal space = %+v", spaces[0])
	}
	if !h.files.Exists("alice-smith", "welcome.md") {
		t.Fatal("welcome note file missing")
	}
	tree, err := h.app.SpaceTree(h.ctx, actor, spaces[0].ID)
	if err != nil || len(tree) != 1 || tree[0].Note == nil || tree[0].Note.Title != "Welcome" {
		t.Fatalf("tree = %v, %v", tree, err)
	}
	setup, _ = h.app.NeedsSetup(h.ctx)
	if setup {
		t.Fatal("NeedsSetup still true")
	}
}

func TestSignupClosedAndInvite(t *testing.T) {
	h := newRealHarness(t)
	h.cfg.AllowSignup = false
	first, err := h.app.Signup(h.ctx, signupInput("admin@example.test"))
	if err != nil {
		t.Fatalf("first signup: %v", err)
	}
	admin := &core.Actor{User: first}
	sp := h.space(admin, "Team")

	if _, err := h.app.Signup(h.ctx, signupInput("bob@example.test")); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("closed signup err = %v, want forbidden", err)
	}

	inv, err := h.app.CreateInvite(h.ctx, admin, core.InviteInput{Email: "bob@example.test", SpaceID: sp.ID, Role: core.RoleEditor})
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	in := signupInput("bob@example.test")
	in.InviteToken = inv.Token
	bob, err := h.app.Signup(h.ctx, in)
	if err != nil {
		t.Fatalf("invite signup: %v", err)
	}
	if bob.IsAdmin {
		t.Fatal("invited user must not be admin")
	}
	m, err := h.db.GetMember(h.ctx, sp.ID, bob.ID)
	if err != nil || m.Role != core.RoleEditor {
		t.Fatalf("membership = %+v, %v", m, err)
	}
	stored, _ := h.db.GetInviteByToken(h.ctx, inv.Token)
	if stored.UsedAt == nil {
		t.Fatal("invite not consumed")
	}

	again := signupInput("carol@example.test")
	again.InviteToken = inv.Token
	if _, err := h.app.Signup(h.ctx, again); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("reused invite err = %v, want invalid", err)
	}

	late, err := h.app.CreateInvite(h.ctx, admin, core.InviteInput{Email: "dave@example.test", TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(2 * time.Hour)
	expired := signupInput("dave@example.test")
	expired.InviteToken = late.Token
	if _, err := h.app.Signup(h.ctx, expired); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("expired invite err = %v, want invalid", err)
	}
}

func TestSignupDuplicateEmail(t *testing.T) {
	h := newRealHarness(t)
	if _, err := h.app.Signup(h.ctx, signupInput("alice@example.test")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.app.Signup(h.ctx, signupInput("ALICE@example.test")); !errors.Is(err, core.ErrExists) {
		t.Fatalf("duplicate err = %v, want exists", err)
	}
}

func TestLogin(t *testing.T) {
	h := newRealHarness(t)
	u, err := h.app.Signup(h.ctx, signupInput("alice@example.test"))
	if err != nil {
		t.Fatal(err)
	}
	disabled := h.user("Disabled", false)
	disabled.User.IsActive = false
	if err := h.db.UpdateUser(h.ctx, disabled.User); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name     string
		email    string
		password string
		wantErr  error
	}{
		{"success", "Alice@example.test", "password123", nil},
		{"wrong password", "alice@example.test", "password124", core.ErrUnauthorized},
		{"unknown email", "nobody@example.test", "password123", core.ErrUnauthorized},
		{"inactive user", disabled.User.Email, "password123", core.ErrUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, token, err := h.app.Login(h.ctx, tc.email, tc.password, "test", "127.0.0.1")
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				if token != "" {
					t.Fatal("token returned on failure")
				}
				return
			}
			if err != nil || got.ID != u.ID || token == "" {
				t.Fatalf("Login = %v, %q, %v", got, token, err)
			}
			actor, err := h.app.ActorFromSession(h.ctx, token)
			if err != nil || actor.UserID() != u.ID {
				t.Fatalf("ActorFromSession = %v, %v", actor, err)
			}
		})
	}
}

func TestSessionExpiry(t *testing.T) {
	h := newRealHarness(t)
	if _, err := h.app.Signup(h.ctx, signupInput("alice@example.test")); err != nil {
		t.Fatal(err)
	}
	u, token, err := h.app.Login(h.ctx, "alice@example.test", "password123", "", "")
	if err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(h.cfg.SessionTTL - time.Minute)
	if _, err := h.app.ActorFromSession(h.ctx, token); err != nil {
		t.Fatalf("session before expiry: %v", err)
	}
	h.clock.Advance(2 * time.Minute)
	if _, err := h.app.ActorFromSession(h.ctx, token); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("expired session err = %v", err)
	}
	sessions, _ := h.db.ListSessions(h.ctx, u.ID)
	if len(sessions) != 0 {
		t.Fatalf("expired session kept: %d", len(sessions))
	}
	if _, err := h.app.ActorFromSession(h.ctx, "garbage"); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("unknown token err = %v", err)
	}
}

func TestAPITokenExpiry(t *testing.T) {
	h := newRealHarness(t)
	actor := h.user("Scripter", false)
	rec, plain, err := h.app.CreateAPIToken(h.ctx, actor, "ci", time.Hour)
	if err != nil || rec.TokenHash == plain {
		t.Fatalf("CreateAPIToken = %+v, %v", rec, err)
	}
	got, err := h.app.ActorFromAPIToken(h.ctx, plain)
	if err != nil || got.Token == nil || got.UserID() != actor.UserID() {
		t.Fatalf("ActorFromAPIToken = %+v, %v", got, err)
	}
	h.clock.Advance(2 * time.Hour)
	if _, err := h.app.ActorFromAPIToken(h.ctx, plain); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("expired token err = %v", err)
	}
}

func TestLookupUsersStripsFields(t *testing.T) {
	h := newRealHarness(t)
	caller := h.user("Caller", false)
	h.user("Target", true)
	list, err := h.app.LookupUsers(h.ctx, caller, "target", 500)
	if err != nil || len(list) != 1 {
		t.Fatalf("LookupUsers = %v, %v", list, err)
	}
	if list[0].PasswordHash != "" || list[0].IsAdmin || list[0].CreatedAt.IsZero() == false {
		t.Fatalf("lookup leaked fields: %+v", list[0])
	}
	if _, err := h.app.LookupUsers(h.ctx, nil, "t", 5); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("anonymous lookup err = %v", err)
	}
	if _, _, err := h.app.ListUsers(h.ctx, caller, "", 10, 0); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("non-admin ListUsers err = %v", err)
	}
}
