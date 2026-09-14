package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// adminFake records the writes of the users and settings pages.
type adminFake struct {
	*fakeBackend
	settings core.Settings
	updated  []core.Settings
	created  []core.UserInput
	invites  []core.InviteInput
	deleted  []string
}

func newAdminFake() *adminFake {
	return &adminFake{fakeBackend: newFake(), settings: core.Settings{InstanceName: "Test Space", AllowSignup: true, AIEnabled: true}}
}

func (f *adminFake) GetSettings(context.Context, *core.Actor) (*core.Settings, error) {
	s := f.settings
	return &s, nil
}

func (f *adminFake) UpdateSettings(_ context.Context, _ *core.Actor, in *core.Settings) (*core.Settings, error) {
	if in.DefaultSpaceID == "missing" {
		return nil, core.Invalid("defaultSpaceId", "choose a space that exists")
	}
	f.updated = append(f.updated, *in)
	f.settings = *in
	return in, nil
}

func (f *adminFake) Reindex(context.Context, *core.Actor) (int, error) { return 12, nil }

func (f *adminFake) ListUsers(context.Context, *core.Actor, string, int, int) ([]*core.User, int, error) {
	return []*core.User{f.admin, f.member}, 2, nil
}

func (f *adminFake) ListInvites(context.Context, *core.Actor) ([]*core.Invite, error) {
	return []*core.Invite{{ID: "i1", Email: "new@example.com", Token: "tok1", Role: core.RoleEditor, ExpiresAt: testTime}}, nil
}

func (f *adminFake) CreateUser(_ context.Context, _ *core.Actor, in core.UserInput) (*core.User, error) {
	if *in.Email == "taken@example.com" {
		return nil, core.Invalid("email", "an account with this email address exists")
	}
	f.created = append(f.created, in)
	return &core.User{ID: "u9", Email: *in.Email, Name: *in.Name}, nil
}

func (f *adminFake) CreateInvite(_ context.Context, _ *core.Actor, in core.InviteInput) (*core.Invite, error) {
	f.invites = append(f.invites, in)
	return &core.Invite{ID: "i2", Email: in.Email}, nil
}

func (f *adminFake) DeleteUser(_ context.Context, _ *core.Actor, id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func TestAdminSettingsExplicitSave(t *testing.T) {
	f := newAdminFake()
	h := newTestServer(t, f).Handler()

	rec := do(t, h, http.MethodPost, "/admin/settings", adminSession, url.Values{
		"action": {"save"}, "instanceName": {"Harbour notes"}, "defaultSpaceId": {"sp1"}, "aiEnabled": {"1"},
	})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/settings?done=settings" {
		t.Fatalf("save: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if len(f.updated) != 1 || f.updated[0].InstanceName != "Harbour notes" || f.updated[0].AllowSignup || !f.updated[0].AIEnabled {
		t.Fatalf("updated = %+v", f.updated)
	}

	body := do(t, h, http.MethodGet, "/admin/settings?done=settings", adminSession, nil).Body.String()
	for _, want := range []string{`data-saved-message`, `Settings saved`, `data-validate`, `data-unsaved`, `class="nav-list-item" href="/admin/settings" aria-current="page"`} {
		if !strings.Contains(body, want) {
			t.Errorf("saved page lacks %q", want)
		}
	}
	if strings.Contains(body, `data-flash="ok"`) {
		t.Error("a saved form also shows a page Banner")
	}
	if strings.Contains(do(t, h, http.MethodGet, "/admin/settings", adminSession, nil).Body.String(), "data-saved-message") {
		t.Error("the page claims a save without one")
	}

	// A refused save keeps the entered values and shows the message under the field.
	rec = do(t, h, http.MethodPost, "/admin/settings", adminSession, url.Values{
		"action": {"save"}, "instanceName": {"  "}, "allowSignup": {"1"},
	})
	body = rec.Body.String()
	if rec.Code != http.StatusBadRequest || len(f.updated) != 1 {
		t.Fatalf("empty name: status %d, updates %d", rec.Code, len(f.updated))
	}
	for _, want := range []string{`id="st-name-error"`, `Enter an instance name`, `aria-invalid="true"`, `id="st-signup" type="checkbox" name="allowSignup" value="1" aria-describedby="st-signup-hint" checked`} {
		if !strings.Contains(body, want) {
			t.Errorf("refused save lacks %q", want)
		}
	}
	if strings.Contains(body, `data-flash="error"`) {
		t.Error("a field error also shows a page Banner")
	}

	rec = do(t, h, http.MethodPost, "/admin/settings", adminSession, url.Values{"action": {"save"}, "instanceName": {"Harbour"}, "defaultSpaceId": {"missing"}})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `id="st-space-error"`) || !strings.Contains(rec.Body.String(), "Choose a space that exists") {
		t.Errorf("backend refusal: %d", rec.Code)
	}

	rec = do(t, h, http.MethodPost, "/admin/settings", adminSession, url.Values{"action": {"reindex"}})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "indexed=12+notes+indexed.") {
		t.Errorf("reindex: %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestAdminUsersDialogs(t *testing.T) {
	f := newAdminFake()
	h := newTestServer(t, f).Handler()

	body := do(t, h, http.MethodGet, "/admin/users", adminSession, nil).Body.String()
	for _, want := range []string{
		`data-dialog-open="create-dialog"`, `id="create-dialog"`, `id="invite-dialog"`,
		`role="region" aria-label="Accounts table"`, `<th scope="row">`,
		`popovertarget="user-menu-u2"`, `data-confirm="Delete Mel Member?"`, `value="deactivate" data-undo-label`,
		`data-confirm="Revoke the invite for new@example.com?"`, `data-copy="https://notes.example.com/signup?invite=tok1"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("users page lacks %q", want)
		}
	}
	if strings.Contains(body, "data-dialog-autoopen") || strings.Contains(body, `popovertarget="user-menu-u1"`) && strings.Contains(body, `data-confirm="Delete Ada Admin?"`) {
		t.Error("a dialog opens on a plain load, or the admin may delete their own account")
	}

	// A refused account keeps the entered values, never the password, and
	// opens the Dialog again with the message under the field.
	rec := do(t, h, http.MethodPost, "/admin/users", adminSession, url.Values{
		"action": {"create"}, "name": {"Tess"}, "email": {"taken@example.com"}, "password": {"long enough"}, "isAdmin": {"1"},
	})
	body = rec.Body.String()
	if rec.Code != http.StatusBadRequest || len(f.created) != 0 {
		t.Fatalf("taken email: status %d, created %d", rec.Code, len(f.created))
	}
	for _, want := range []string{`id="create-dialog" data-dialog-title="Create account"`, `data-dialog-autoopen`, `value="taken@example.com"`, `value="Tess"`, `id="new-email-error"`, `An account with this email address exists`} {
		if !strings.Contains(body, want) {
			t.Errorf("refused create lacks %q", want)
		}
	}
	if strings.Contains(body, "long enough") || strings.Contains(body, `data-flash="error"`) {
		t.Error("the refused form echoes the password or repeats the error in a page Banner")
	}

	rec = do(t, h, http.MethodPost, "/admin/users", adminSession, url.Values{"action": {"create"}, "name": {"Tess"}, "email": {"tess@example.com"}, "password": {"short"}})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `id="new-password-error"`) || len(f.created) != 0 {
		t.Errorf("short password: %d, created %d", rec.Code, len(f.created))
	}

	rec = do(t, h, http.MethodPost, "/admin/users", adminSession, url.Values{"action": {"create"}, "name": {"Tess"}, "email": {"tess@example.com"}, "password": {"long enough"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/users?done=create" || len(f.created) != 1 {
		t.Fatalf("create: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	body = do(t, h, http.MethodGet, "/admin/users?done=create", adminSession, nil).Body.String()
	if !strings.Contains(body, `data-saved-message>Account created.</p>`) || strings.Contains(body, `data-flash="ok"`) {
		t.Error("a created account is not announced, or shows a page Banner")
	}

	rec = do(t, h, http.MethodPost, "/admin/users", adminSession, url.Values{"action": {"invite"}, "email": {"x@example.com"}, "ttlDays": {"0"}, "role": {"viewer"}})
	body = rec.Body.String()
	if rec.Code != http.StatusBadRequest || len(f.invites) != 0 || !strings.Contains(body, `id="invite-ttl-error"`) || !strings.Contains(body, `<option value="viewer" selected>`) {
		t.Errorf("bad invite days: %d, invites %d", rec.Code, len(f.invites))
	}
	rec = do(t, h, http.MethodPost, "/admin/users", adminSession, url.Values{"action": {"invite"}, "email": {"x@example.com"}, "ttlDays": {"3"}})
	if rec.Code != http.StatusSeeOther || len(f.invites) != 1 || f.invites[0].TTL.Hours() != 72 {
		t.Errorf("invite: %d %+v", rec.Code, f.invites)
	}

	if rec := do(t, h, http.MethodPost, "/admin/users", adminSession, url.Values{"action": {"delete"}, "userId": {"u2"}}); rec.Code != http.StatusSeeOther || strings.Join(f.deleted, ",") != "u2" {
		t.Errorf("delete: %d %v", rec.Code, f.deleted)
	}

	if body := do(t, h, http.MethodGet, "/admin/users?dialog=invite", adminSession, nil).Body.String(); !strings.Contains(body, `id="invite-dialog" data-dialog-title="Invite someone" data-dialog-subtitle="You get a link to pass on. Markspace sends no email." data-dialog-width="large" data-dialog-autoopen`) {
		t.Error("the dialog query value does not open the invite Dialog")
	}
}
