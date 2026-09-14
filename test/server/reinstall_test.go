package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"testing"
)

// TestPluginReinstallIntoKeptInstallData uninstalls a plugin with its data
// kept and installs it again into the same plugin process, which still holds
// the credentials of the first install. Both SDKs accept a re-install only
// when its signature matches the secret they stored, so the install call
// must be signed with the previous secret while the payload carries the new
// one.
func TestPluginReinstallIntoKeptInstallData(t *testing.T) {
	s := newPluginStack(t)
	ctx := context.Background()
	wiki := s.space("Wiki")
	tp := newTestPlugin(t, testManifest("test-plugin"))
	grants := []map[string]string{{"spaceId": wiki.ID, "role": "editor"}}

	if res := s.install(tp.srv.URL, grants); res["delivered"] != true {
		t.Fatalf("install: %v", res)
	}
	first := tp.pluginToken()
	row, err := s.db.GetPlugin(ctx, "test-plugin")
	if err != nil {
		t.Fatal(err)
	}
	s.call(s.adminTok, "POST", "/api/admin/plugins/test-plugin/uninstall", map[string]bool{"deleteData": false}, nil, 204)

	res := s.install(tp.srv.URL, grants)
	if res["delivered"] != true || res["token"] != nil {
		t.Fatalf("reinstall into the kept install data: %v", res)
	}
	second := tp.pluginToken()
	if second == first {
		t.Fatal("the plugin still holds the revoked token")
	}
	s.call(second, "GET", "/api/plugin/v1/me", nil, nil, 200)
	if status, _, _ := s.request(first, "GET", "/api/plugin/v1/me", nil); status != 401 {
		t.Fatalf("old token after reinstall: %d", status)
	}
	row2, err := s.db.GetPlugin(ctx, "test-plugin")
	if err != nil {
		t.Fatal(err)
	}
	if row2.SigningSecret == row.SigningSecret || row2.UserID != row.UserID {
		t.Fatalf("reinstall row: secret changed %v, same user %v", row2.SigningSecret != row.SigningSecret, row2.UserID == row.UserID)
	}
	want, err := base64.StdEncoding.DecodeString(row2.SigningSecret)
	if err != nil {
		t.Fatal(err)
	}
	if got := tp.sdk.Credentials().Secret; !bytes.Equal(got, want) {
		t.Fatal("the plugin did not store the new signing secret")
	}
}
