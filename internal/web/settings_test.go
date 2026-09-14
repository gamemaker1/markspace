package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// settingsFake records the settings calls the page handlers make.
type settingsFake struct {
	*fakeBackend
	calls   []string
	profile core.ProfileInput
}

func newSettingsFake() *settingsFake { return &settingsFake{fakeBackend: newFake()} }

func (f *settingsFake) UpdateProfile(_ context.Context, actor *core.Actor, in core.ProfileInput) (*core.User, error) {
	f.calls = append(f.calls, "profile")
	f.profile = in
	return actor.User, nil
}

func (f *settingsFake) ChangePassword(_ context.Context, _ *core.Actor, old, _ string) error {
	f.calls = append(f.calls, "password")
	if old != "right" {
		return core.Invalid("oldPassword", "the current password is wrong")
	}
	return nil
}

func (f *settingsFake) ListAPITokens(context.Context, *core.Actor) ([]*core.APIToken, error) {
	return []*core.APIToken{{ID: "t1", Name: "Backup script", Prefix: "ms_ab", CreatedAt: testTime}}, nil
}

func (f *settingsFake) RevokeAPIToken(_ context.Context, _ *core.Actor, id string) error {
	f.calls = append(f.calls, "revoke "+id)
	return nil
}

func (f *settingsFake) CreateAPIToken(_ context.Context, _ *core.Actor, name string, ttl time.Duration) (*core.APIToken, string, error) {
	f.calls = append(f.calls, "create "+name)
	return &core.APIToken{ID: "t2", Name: name}, "ms_plain_secret", nil
}

func (f *settingsFake) ListSessions(context.Context, *core.Actor) ([]*core.Session, error) {
	return []*core.Session{{ID: "se1", UserAgent: "Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0", IP: "127.0.0.1", LastSeen: testTime}}, nil
}

func (f *settingsFake) RevokeSession(_ context.Context, _ *core.Actor, id string) error {
	f.calls = append(f.calls, "session "+id)
	return nil
}

func (f *settingsFake) SpaceAccess(context.Context, *core.Actor, string) (*core.Grant, error) {
	return &core.Grant{Access: core.AccessManage, Source: "space", Role: core.RoleOwner}, nil
}

func (f *settingsFake) ListMembers(context.Context, *core.Actor, string) ([]*core.SpaceMember, error) {
	return []*core.SpaceMember{{UserID: f.member.ID, Role: core.RoleOwner, User: f.member}}, nil
}

func (f *settingsFake) RemoveMember(_ context.Context, _ *core.Actor, _, userID string) error {
	f.calls = append(f.calls, "remove "+userID)
	return nil
}

func (f *settingsFake) LookupUsers(context.Context, *core.Actor, string, int) ([]*core.User, error) {
	return nil, nil
}

func (f *settingsFake) DeleteSpace(_ context.Context, _ *core.Actor, id string) error {
	f.calls = append(f.calls, "delete-space "+id)
	return nil
}

func (f *settingsFake) DeleteNote(_ context.Context, _ *core.Actor, id string) error {
	f.calls = append(f.calls, "delete-note "+id)
	return nil
}

func (f *settingsFake) EmptyTrash(_ context.Context, _ *core.Actor, spaceID string) error {
	f.calls = append(f.calls, "empty-trash "+spaceID)
	return nil
}

func (f *settingsFake) RestoreNote(_ context.Context, _ *core.Actor, id string) error {
	f.calls = append(f.calls, "restore-note "+id)
	return nil
}

func (f *settingsFake) ListTrash(context.Context, *core.Actor, string) ([]*core.Note, error) {
	return []*core.Note{{ID: "n9", Title: "Old draft", SpaceSlug: "demo", Path: "old"}}, nil
}

func (f *settingsFake) UpdateSpace(_ context.Context, _ *core.Actor, _ string, in core.SpaceInput) (*core.Space, error) {
	f.calls = append(f.calls, "update-space")
	return f.space, nil
}

func mustContain(t *testing.T, label, body string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("%s is missing %q", label, want)
		}
	}
}

func mustNotContain(t *testing.T, label, body string, gone ...string) {
	t.Helper()
	for _, g := range gone {
		if strings.Contains(body, g) {
			t.Errorf("%s still contains %q", label, g)
		}
	}
}

// Settings forms save only through their Save button, show an InlineMessage
// beside it after a save, and save nothing when nothing changed.
func TestSettingsExplicitSave(t *testing.T) {
	f := newSettingsFake()
	h := newTestServer(t, f).Handler()

	unchanged := url.Values{"action": {"profile"}, "name": {"Mel Member"}, "email": {"mel@example.com"}, "timezone": {""}, "locale": {""}, "avatarColor": {defaultAvatarColor}}
	rec := do(t, h, http.MethodPost, "/settings/profile", memberSession, unchanged)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings/profile" || len(f.calls) != 0 {
		t.Fatalf("unchanged profile: %d %q calls %v", rec.Code, rec.Header().Get("Location"), f.calls)
	}

	changed := url.Values{"action": {"profile"}, "name": {"Mel M."}, "email": {"mel@example.com"}}
	rec = do(t, h, http.MethodPost, "/settings/profile", memberSession, changed)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings/profile?saved=profile#profile" {
		t.Fatalf("changed profile: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if len(f.calls) != 1 || f.profile.Name == nil || *f.profile.Name != "Mel M." || f.profile.Email != nil {
		t.Fatalf("profile update = %v %+v", f.calls, f.profile)
	}

	rec = do(t, h, http.MethodGet, "/settings/profile?saved=profile", memberSession, nil)
	body := rec.Body.String()
	mustContain(t, "saved profile page", body, `>Save profile</button>`, `inline-message-success`, "Profile saved", `>Change password</button>`, `novalidate`)
	mustNotContain(t, "saved profile page", body, `data-pref=`, "Password changed", `banner-success`)
	if strings.Index(body, "Profile saved") < strings.Index(body, ">Save profile</button>") {
		t.Errorf("the saved message is not beside the Save button")
	}

	rec = do(t, h, http.MethodGet, "/settings/offline", memberSession, nil)
	mustContain(t, "offline files page", rec.Body.String(), `>Save limit</button>`, `data-offline-clear`, `aria-current="page"`)

	rec = do(t, h, http.MethodGet, "/settings/sessions", memberSession, nil)
	mustContain(t, "sessions page", rec.Body.String(), "Firefox on Linux", `role="region"`, `data-confirm="Sign out Firefox on Linux?"`)
}

// One or two problems show under their fields and focus the first one.
func TestSettingsFieldErrors(t *testing.T) {
	f := newSettingsFake()
	h := newTestServer(t, f).Handler()
	rec := do(t, h, http.MethodPost, "/settings/profile", memberSession, url.Values{
		"action": {"password"}, "oldPassword": {""}, "newPassword": {"short"}, "newPasswordConfirm": {"short"},
	})
	body := rec.Body.String()
	if rec.Code != http.StatusBadRequest || len(f.calls) != 0 {
		t.Fatalf("status %d calls %v", rec.Code, f.calls)
	}
	mustContain(t, "password errors", body,
		`id="password-old-password-error"`, "Enter your current password.",
		`id="password-new-password-error"`, "Use at least 8 characters.",
		`aria-describedby="password-new-password-caption password-new-password-error"`,
		`aria-invalid="true" autofocus>`)
	mustNotContain(t, "password errors", body, "form-error-summary", `value="short"`)
	if strings.Count(body, " autofocus") != 1 {
		t.Errorf("want one autofocus, got %d", strings.Count(body, " autofocus"))
	}

	// A backend validation error lands under its field.
	rec = do(t, h, http.MethodPost, "/settings/profile", memberSession, url.Values{
		"action": {"password"}, "oldPassword": {"wrong"}, "newPassword": {"long enough"}, "newPasswordConfirm": {"long enough"},
	})
	mustContain(t, "wrong current password", rec.Body.String(), `id="password-old-password-error"`, "The current password is wrong.")

	// A space member that has no account.
	rec = do(t, h, http.MethodPost, "/s/demo/settings", memberSession, url.Values{"action": {"member-add"}, "email": {"nobody@example.com"}, "role": {"viewer"}})
	body = rec.Body.String()
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("member-add status %d", rec.Code)
	}
	mustContain(t, "member-add error", body, `id="member-email-error"`, `value="nobody@example.com"`, `value="viewer" checked`)

	// The public link password.
	rec = do(t, h, http.MethodPost, "/p/tok", "", url.Values{"password": {"nope"}})
	mustContain(t, "link password", rec.Body.String(), `id="link-password-error"`, `aria-invalid="true"`)
}

// Three or more problems show an error summary Banner that takes focus, and
// the entered values stay.
func TestSettingsErrorSummary(t *testing.T) {
	h := newTestServer(t, newSettingsFake()).Handler()
	rec := do(t, h, http.MethodPost, "/signup", "", url.Values{"name": {"Ada"}, "email": {"ada"}, "password": {"short"}, "passwordConfirm": {""}})
	body := rec.Body.String()
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
	mustContain(t, "signup summary", body,
		`class="banner banner-critical form-error-summary"`, `tabindex="-1" autofocus`, "3 fields need changes",
		`href="#signup-email"`, `href="#signup-password"`, `href="#signup-password-confirm"`,
		`value="Ada"`, `value="ada"`, `id="signup-email-error"`)
	mustNotContain(t, "signup summary", body, `value="short"`, `class="form-control field has-error"`)
	if strings.Count(body, " autofocus") != 1 {
		t.Errorf("want one autofocus on the summary, got %d", strings.Count(body, " autofocus"))
	}
	if strings.Index(body, "form-error-summary") > strings.Index(body, `id="signup-name"`) {
		t.Errorf("the summary is not at the top of the form")
	}

	rec = do(t, h, http.MethodPost, "/spaces", memberSession, url.Values{"name": {""}, "slug": {"Bad Slug"}})
	mustContain(t, "new space errors", rec.Body.String(), "The address can use lowercase letters, digits and dashes.", `value="Bad Slug"`, "<details class=\"add-toggle page-add\" id=\"new-space\" open>")
}

// With scripting off, destructive actions answer with a confirmation page,
// and act only when that page posts back.
func TestNoScriptConfirmationPages(t *testing.T) {
	f := newSettingsFake()
	h := newTestServer(t, f).Handler()

	// The token row carries the no-script marker and the ConfirmationDialog.
	rec := do(t, h, http.MethodGet, "/settings/tokens", memberSession, nil)
	mustContain(t, "tokens page", rec.Body.String(), `<noscript><input type="hidden" name="noscript" value="1"></noscript>`, `data-confirm="Revoke Backup script?"`, `aria-labelledby="page-title"`)

	rec = do(t, h, http.MethodPost, "/settings/tokens", memberSession, url.Values{"action": {"revoke"}, "tokenId": {"t1"}, "noscript": {"1"}})
	body := rec.Body.String()
	if rec.Code != http.StatusOK || len(f.calls) != 0 {
		t.Fatalf("no-script revoke: status %d calls %v", rec.Code, f.calls)
	}
	mustContain(t, "token confirmation", body, "Revoke Backup script?", `name="tokenId" value="t1"`, `name="action" value="revoke"`,
		`name="confirmed" value="1"`, `class="btn btn-danger"`, `href="/settings/tokens" autofocus>Cancel</a>`)
	mustNotContain(t, "token confirmation", body, `name="noscript"`)

	rec = do(t, h, http.MethodPost, "/settings/tokens", memberSession, url.Values{"action": {"revoke"}, "tokenId": {"t1"}, "confirmed": {"1"}})
	if rec.Code != http.StatusSeeOther || len(f.calls) != 1 || f.calls[0] != "revoke t1" {
		t.Fatalf("confirmed revoke: status %d calls %v", rec.Code, f.calls)
	}

	// With scripting on the dialog already asked, so the post acts at once.
	f.calls = nil
	rec = do(t, h, http.MethodPost, "/settings/sessions", memberSession, url.Values{"action": {"session-revoke"}, "sessionId": {"se1"}})
	if rec.Code != http.StatusSeeOther || len(f.calls) != 1 {
		t.Fatalf("scripted session revoke: %d %v", rec.Code, f.calls)
	}
	f.calls = nil
	rec = do(t, h, http.MethodPost, "/settings/sessions", memberSession, url.Values{"action": {"session-revoke"}, "sessionId": {"se1"}, "noscript": {"1"}})
	if rec.Code != http.StatusOK || len(f.calls) != 0 || !strings.Contains(rec.Body.String(), "Sign out Firefox on Linux?") {
		t.Fatalf("no-script session revoke: %d %v", rec.Code, f.calls)
	}

	rec = do(t, h, http.MethodPost, "/s/demo/settings", memberSession, url.Values{"action": {"delete"}, "noscript": {"1"}})
	if rec.Code != http.StatusOK || len(f.calls) != 0 {
		t.Fatalf("no-script space delete: %d %v", rec.Code, f.calls)
	}
	mustContain(t, "space confirmation", rec.Body.String(), "Delete Demo?", `action="/s/demo/settings"`, `>Delete space</button>`)
	rec = do(t, h, http.MethodPost, "/s/demo/settings", memberSession, url.Values{"action": {"delete"}, "confirmed": {"1"}})
	if rec.Code != http.StatusSeeOther || len(f.calls) != 1 || f.calls[0] != "delete-space sp1" {
		t.Fatalf("confirmed space delete: %d %v", rec.Code, f.calls)
	}

	// With scripting off the trash forms post noscript=1, and the trash
	// handler renders a confirmation page before it deletes anything.
	rec = do(t, h, http.MethodGet, "/s/demo/trash", memberSession, nil)
	mustContain(t, "trash page", rec.Body.String(),
		`<noscript><form method="post" action="/s/demo/trash"><input type="hidden" name="action" value="empty"><input type="hidden" name="noscript" value="1">`,
		`<input type="hidden" name="noteId" value="n9"><input type="hidden" name="action" value="delete"><input type="hidden" name="noscript" value="1">`)
	if strings.Contains(rec.Body.String(), "/trash/confirm") {
		t.Error("the trash page still links to the removed GET confirmation route")
	}
	f.calls = nil
	rec = do(t, h, http.MethodPost, "/s/demo/trash", memberSession, url.Values{"action": {"delete"}, "noteId": {"n9"}, "noscript": {"1"}})
	if rec.Code != http.StatusOK || len(f.calls) != 0 {
		t.Fatalf("no-script delete forever: %d %v", rec.Code, f.calls)
	}
	mustContain(t, "trash confirmation", rec.Body.String(), "Delete Old draft forever?", `action="/s/demo/trash"`, `name="noteId" value="n9"`, `name="action" value="delete"`, `name="confirmed" value="1"`)
	rec = do(t, h, http.MethodPost, "/s/demo/trash", memberSession, url.Values{"action": {"empty"}, "noscript": {"1"}})
	if rec.Code != http.StatusOK || len(f.calls) != 0 {
		t.Fatalf("no-script empty trash: %d %v", rec.Code, f.calls)
	}
	mustContain(t, "empty confirmation", rec.Body.String(), "Empty the trash?", `name="action" value="empty"`)
	if rec := do(t, h, http.MethodPost, "/s/demo/trash", memberSession, url.Values{"action": {"delete"}, "noteId": {"missing"}, "noscript": {"1"}}); rec.Code != http.StatusNotFound || len(f.calls) != 0 {
		t.Errorf("unknown trashed note: status %d, calls %v", rec.Code, f.calls)
	}
	rec = do(t, h, http.MethodPost, "/s/demo/trash", memberSession, url.Values{"action": {"delete"}, "noteId": {"n9"}, "confirmed": {"1"}})
	if rec.Code != http.StatusSeeOther || len(f.calls) != 1 || f.calls[0] != "delete-note n9" {
		t.Fatalf("confirmed delete forever: %d %v", rec.Code, f.calls)
	}
	// Restore can be undone, so it never asks.
	f.calls = nil
	rec = do(t, h, http.MethodPost, "/s/demo/trash", memberSession, url.Values{"action": {"restore"}, "noteId": {"n9"}, "noscript": {"1"}})
	if rec.Code != http.StatusSeeOther || len(f.calls) != 1 || f.calls[0] != "restore-note n9" {
		t.Fatalf("no-script restore: %d %v", rec.Code, f.calls)
	}
	if rec := do(t, h, http.MethodGet, "/s/demo/trash/confirm?action=empty", memberSession, nil); rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "Empty the trash?") {
		t.Error("GET /s/{space}/trash/confirm still renders a confirmation page")
	}

	// A member removed with scripting off gets an Undo form.
	rec = do(t, h, http.MethodPost, "/s/demo/settings", memberSession, url.Values{"action": {"member-remove"}, "userId": {"u9"}, "role": {"viewer"}, "noscript": {"1"}})
	loc := rec.Header().Get("Location")
	if rec.Code != http.StatusSeeOther || !strings.Contains(loc, "removed=u9") {
		t.Fatalf("no-script member remove: %d %q", rec.Code, loc)
	}
	rec = do(t, h, http.MethodGet, loc, memberSession, nil)
	mustContain(t, "member undo", rec.Body.String(), "Member removed", `name="action" value="member-add"`, `name="userId" value="u9"`, `name="role" value="viewer"`)
}

func TestSettingsNavigation(t *testing.T) {
	h := newTestServer(t, newSettingsFake()).Handler()
	rec := do(t, h, http.MethodGet, "/settings/tokens", memberSession, nil)
	body := rec.Body.String()
	mustContain(t, "tokens page", body, `class="settings-pane"`, `href="/settings/tokens" aria-current="page"`, `class="page-header-parent" href="/settings"`)
	// The app header belongs to the shell package, so only the content counts.
	mustNotContain(t, "tokens content", body[strings.Index(body, `id="main"`):], `class="tabs"`, ` title="`)
	rec = do(t, h, http.MethodGet, "/settings", memberSession, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings index status %d", rec.Code)
	}
	mustContain(t, "settings index", rec.Body.String(), `href="/settings/profile"`, `href="/settings/sessions"`, `href="/settings/offline"`)
}

func TestBrowserName(t *testing.T) {
	for ua, want := range map[string]string{
		"": "Unknown browser",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15":    "Safari on macOS",
		"Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0 Mobile Safari/537.36":              "Chrome on Android",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0 Safari/537.36 Edg/128.0": "Edge on Windows",
		"curl/8.9":  "curl",
		"odd-agent": "odd-agent",
	} {
		if got := browserName(ua); got != want {
			t.Errorf("browserName(%q) = %q, want %q", ua, got, want)
		}
	}
	if got := fieldID("signup", "passwordConfirm"); got != "signup-password-confirm" {
		t.Errorf("fieldID = %q", got)
	}
}
