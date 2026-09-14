package auth

import (
	"context"
	"net/http"
	"testing"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/api/testkit"
	"github.com/gamemaker1/markspace/internal/core"
)

// serve builds the API handler over b with this module registered.
func serve(t *testing.T, b core.Backend) http.Handler {
	t.Helper()
	env := testkit.Env(b)
	return testkit.Serve(t, env, New(env))
}

func TestLoginSetsSessionCookie(t *testing.T) {
	user := &core.User{ID: "u1", Email: "a@b.test", Name: "Ann"}
	fake := &testkit.Fake{
		LoginFn: func(_ context.Context, email, password, ua, ip string) (*core.User, string, error) {
			if email != "a@b.test" || password != "secret" {
				t.Fatalf("credentials not forwarded: %q %q", email, password)
			}
			return user, "session-token", nil
		},
	}
	rec := testkit.Do(t, serve(t, fake), "POST", "/api/auth/login",
		`{"email":"a@b.test","password":"secret"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var got core.User
	testkit.DecodeBody(t, rec, &got)
	if got.ID != "u1" {
		t.Errorf("user id = %q, want u1", got.ID)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d, want 1", len(cookies))
	}
	if cookies[0].Name != api.SessionCookie || cookies[0].Value != "session-token" {
		t.Errorf("cookie = %v, want %s=session-token", cookies[0], api.SessionCookie)
	}
	if !cookies[0].HttpOnly {
		t.Error("cookie is not HttpOnly")
	}
}

func TestUnauthenticatedUserRouteIs401(t *testing.T) {
	rec := testkit.Do(t, serve(t, &testkit.Fake{}), "GET", "/api/tokens", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", rec.Code, rec.Body)
	}
	var body api.ErrorBody
	testkit.DecodeBody(t, rec, &body)
	if body.Error == "" {
		t.Error("error message is empty")
	}
}

func TestMeForAnonymousActor(t *testing.T) {
	fake := &testkit.Fake{
		NeedsSetupFn: func(context.Context) (bool, error) { return true, nil },
		GetSettingsFn: func(context.Context, *core.Actor) (*core.Settings, error) {
			return nil, core.ErrForbidden
		},
	}
	rec := testkit.Do(t, serve(t, fake), "GET", "/api/auth/me", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var body map[string]any
	testkit.DecodeBody(t, rec, &body)
	if body["user"] != nil {
		t.Errorf("user = %v, want null", body["user"])
	}
	if body["needsSetup"] != true {
		t.Errorf("needsSetup = %v, want true", body["needsSetup"])
	}
	if _, ok := body["settings"]; ok {
		t.Errorf("settings should be omitted for a forbidden read, got %v", body["settings"])
	}
}

func TestMeForSignedInActor(t *testing.T) {
	fake := &testkit.Fake{
		ActorFromSessionFn: func(_ context.Context, token string) (*core.Actor, error) {
			if token != "good" {
				return nil, core.ErrUnauthorized
			}
			return &core.Actor{User: &core.User{ID: "u1", Email: "a@b.test"}}, nil
		},
		NeedsSetupFn: func(context.Context) (bool, error) { return false, nil },
		GetSettingsFn: func(context.Context, *core.Actor) (*core.Settings, error) {
			return &core.Settings{InstanceName: "Markspace"}, nil
		},
	}
	rec := testkit.Do(t, serve(t, fake), "GET", "/api/auth/me", "", "good")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var body meResponse
	testkit.DecodeBody(t, rec, &body)
	if body.User == nil || body.User.ID != "u1" {
		t.Errorf("user = %+v", body.User)
	}
	if body.Settings == nil || body.Settings.InstanceName != "Markspace" {
		t.Errorf("settings = %+v", body.Settings)
	}
}

func TestLogoutClearsCookie(t *testing.T) {
	var loggedOut string
	fake := &testkit.Fake{
		LogoutFn: func(_ context.Context, token string) error {
			loggedOut = token
			return nil
		},
	}
	rec := testkit.Do(t, serve(t, fake), "POST", "/api/auth/logout", "", "session-token")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", rec.Code, rec.Body)
	}
	if loggedOut != "session-token" {
		t.Errorf("logged out token = %q", loggedOut)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge >= 0 {
		t.Errorf("cookie was not cleared: %v", cookies)
	}
}
