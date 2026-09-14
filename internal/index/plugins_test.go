package index

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

var pluginBase = time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC)

func mustPlugin(tb testing.TB, db *DB, id string) *Plugin {
	tb.Helper()
	p := &Plugin{
		ID: id, Name: id, Version: "0.1.0", BaseURL: "http://" + id + ":8090", Status: PluginActive,
		TokenHash: "hash-" + id, TokenPrefix: "mspl_abcd", SigningSecret: "c2VjcmV0",
		Permissions: []string{"storage", "events"},
	}
	if err := db.CreatePlugin(context.Background(), p); err != nil {
		tb.Fatalf("CreatePlugin %s: %v", id, err)
	}
	return p
}

func wantIs(t *testing.T, err, target error, what string) {
	t.Helper()
	if target == nil && err != nil || target != nil && !errors.Is(err, target) {
		t.Fatalf("%s: got error %v, want %v", what, err, target)
	}
}

func TestPluginCRUDAndTokenLookup(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	p := mustPlugin(t, db, "bookmark-wiki")
	got, err := db.GetPlugin(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseURL != p.BaseURL || got.Status != PluginActive || got.TokenHash != p.TokenHash ||
		got.SigningSecret != "c2VjcmV0" || !slices.Equal(got.Permissions, p.Permissions) ||
		string(got.Manifest) != "{}" || string(got.Settings) != "{}" || got.CreatedAt.IsZero() {
		t.Fatalf("stored plugin differs: %+v", got)
	}

	wantIs(t, db.CreatePlugin(ctx, &Plugin{ID: p.ID, BaseURL: "http://x"}), core.ErrExists, "duplicate id")
	wantIs(t, db.CreatePlugin(ctx, &Plugin{ID: "other", BaseURL: "http://x", TokenHash: p.TokenHash}), core.ErrExists, "duplicate token hash")
	wantIs(t, db.CreatePlugin(ctx, &Plugin{ID: "Bad Id", BaseURL: "http://x"}), core.ErrInvalid, "bad id")
	wantIs(t, db.CreatePlugin(ctx, &Plugin{ID: "x", BaseURL: "http://x", Status: "gone"}), core.ErrInvalid, "bad status")
	wantIs(t, db.CreatePlugin(ctx, &Plugin{ID: "x", BaseURL: "http://x", Manifest: json.RawMessage(`{`)}), core.ErrInvalid, "bad manifest")
	wantIs(t, db.CreatePlugin(ctx, &Plugin{ID: "x"}), core.ErrInvalid, "missing base url")

	fresh := &Plugin{ID: "alpha", Name: "Alpha", BaseURL: "http://alpha"}
	if err := db.CreatePlugin(ctx, fresh); err != nil || fresh.Status != PluginNeedsApproval {
		t.Fatalf("default status: %s %v", fresh.Status, err)
	}
	list, err := db.ListPlugins(ctx)
	if err != nil || len(list) != 2 || list[0].ID != "alpha" || list[1].ID != p.ID {
		t.Fatalf("list: %v %v", list, err)
	}

	byToken, err := db.GetPluginByTokenHash(ctx, p.TokenHash)
	if err != nil || byToken.ID != p.ID {
		t.Fatalf("token lookup: %v %v", byToken, err)
	}
	_, err = db.GetPluginByTokenHash(ctx, "")
	wantIs(t, err, core.ErrNotFound, "empty token hash")
	_, err = db.GetPluginByTokenHash(ctx, fresh.TokenHash)
	wantIs(t, err, core.ErrNotFound, "plugin without a token")

	got.Version = "0.2.0"
	got.Manifest = json.RawMessage(`{"protocol":1}`)
	got.TokenHash = "rotated"
	got.HealthFailures = 99
	if err := db.UpdatePlugin(ctx, got); err != nil {
		t.Fatal(err)
	}
	_, err = db.GetPluginByTokenHash(ctx, p.TokenHash)
	wantIs(t, err, core.ErrNotFound, "old token hash")
	updated, err := db.GetPluginByTokenHash(ctx, "rotated")
	if err != nil || updated.Version != "0.2.0" || string(updated.Manifest) != `{"protocol":1}` || updated.HealthFailures != 0 {
		t.Fatalf("update: %+v %v", updated, err)
	}
	wantIs(t, db.UpdatePlugin(ctx, &Plugin{ID: "missing", BaseURL: "http://x", Status: PluginActive}), core.ErrNotFound, "update unknown")

	wantIs(t, db.SetPluginStatus(ctx, p.ID, PluginPaused, pluginBase), nil, "pause")
	wantIs(t, db.SetPluginStatus(ctx, p.ID, "sleeping", pluginBase), core.ErrInvalid, "bad status")
	wantIs(t, db.SetPluginStatus(ctx, "missing", PluginPaused, pluginBase), core.ErrNotFound, "status of unknown")
	if g, _ := db.GetPlugin(ctx, p.ID); g.Status != PluginPaused {
		t.Fatalf("status %s, want paused", g.Status)
	}

	wantIs(t, db.DeletePlugin(ctx, p.ID), nil, "delete")
	_, err = db.GetPlugin(ctx, p.ID)
	wantIs(t, err, core.ErrNotFound, "get deleted")
	wantIs(t, db.DeletePlugin(ctx, p.ID), core.ErrNotFound, "delete twice")
}

func TestPluginHealthStateMachine(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	type check struct {
		ok           bool
		wantStatus   string
		wantFailures int
	}
	tests := []struct {
		name   string
		start  string
		checks []check
	}{
		{"active goes down after three failures and recovers", PluginActive, []check{
			{false, PluginActive, 1}, {false, PluginActive, 2}, {false, PluginDown, 3}, {false, PluginDown, 4},
			{true, PluginActive, 0}, {false, PluginActive, 1}, {true, PluginActive, 0},
		}},
		{"success resets the count before the third failure", PluginActive, []check{
			{false, PluginActive, 1}, {false, PluginActive, 2}, {true, PluginActive, 0}, {false, PluginActive, 1},
		}},
		{"paused keeps its status", PluginPaused, []check{
			{false, PluginPaused, 1}, {false, PluginPaused, 2}, {false, PluginPaused, 3}, {true, PluginPaused, 0},
		}},
		{"needs approval keeps its status", PluginNeedsApproval, []check{
			{false, PluginNeedsApproval, 1}, {false, PluginNeedsApproval, 2}, {false, PluginNeedsApproval, 3},
		}},
	}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id := fmt.Sprintf("health-%d", i)
			p := mustPlugin(t, db, id)
			if err := db.SetPluginStatus(ctx, p.ID, tc.start, pluginBase); err != nil {
				t.Fatal(err)
			}
			for k, c := range tc.checks {
				at := pluginBase.Add(time.Duration(k) * time.Minute)
				errText := ""
				if !c.ok {
					errText = "connection refused"
				}
				got, err := db.RecordPluginHealth(ctx, p.ID, c.ok, errText, at)
				if err != nil {
					t.Fatal(err)
				}
				if got.Status != c.wantStatus || got.HealthFailures != c.wantFailures || !got.LastHealthAt.Equal(at) {
					t.Fatalf("check %d: status %s failures %d at %s, want %s %d", k, got.Status, got.HealthFailures, got.LastHealthAt, c.wantStatus, c.wantFailures)
				}
				if got.LastError != errText {
					t.Fatalf("check %d: last error %q, want %q", k, got.LastError, errText)
				}
			}
		})
	}
	_, err := db.RecordPluginHealth(ctx, "missing", true, "", pluginBase)
	wantIs(t, err, core.ErrNotFound, "health of unknown plugin")
}

func TestPluginGrantsAndSecrets(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	p := mustPlugin(t, db, "grants")
	a, b := mustSpace(t, db, "docs"), mustSpace(t, db, "wiki")

	if err := db.SetPluginGrants(ctx, p.ID, []PluginGrant{{SpaceID: b.ID, Role: PluginRoleEditor}, {SpaceID: a.ID, Role: PluginRoleViewer}}); err != nil {
		t.Fatal(err)
	}
	grants, err := db.ListPluginGrants(ctx, p.ID)
	if err != nil || len(grants) != 2 {
		t.Fatalf("grants: %v %v", grants, err)
	}
	if role, err := db.GetPluginGrant(ctx, p.ID, a.ID); err != nil || role != PluginRoleViewer {
		t.Fatalf("grant on docs: %q %v", role, err)
	}
	_, err = db.GetPluginGrant(ctx, p.ID, core.NewID())
	wantIs(t, err, core.ErrNotFound, "grant on another space")

	invalid := []struct {
		name    string
		grants  []PluginGrant
		wantErr error
	}{
		{"admin role", []PluginGrant{{SpaceID: a.ID, Role: "admin"}}, core.ErrInvalid},
		{"owner role", []PluginGrant{{SpaceID: a.ID, Role: "owner"}}, core.ErrInvalid},
		{"repeated space", []PluginGrant{{SpaceID: a.ID, Role: PluginRoleViewer}, {SpaceID: a.ID, Role: PluginRoleEditor}}, core.ErrInvalid},
		{"unknown space", []PluginGrant{{SpaceID: a.ID, Role: PluginRoleViewer}, {SpaceID: core.NewID(), Role: PluginRoleViewer}}, core.ErrNotFound},
	}
	for _, tc := range invalid {
		wantIs(t, db.SetPluginGrants(ctx, p.ID, tc.grants), tc.wantErr, tc.name)
		if g, _ := db.ListPluginGrants(ctx, p.ID); len(g) != 2 {
			t.Fatalf("%s changed the grants: %v", tc.name, g)
		}
	}
	wantIs(t, db.SetPluginGrants(ctx, "missing", nil), core.ErrNotFound, "grants of unknown plugin")

	if err := db.SetPluginGrants(ctx, p.ID, []PluginGrant{{SpaceID: a.ID, Role: PluginRoleCommenter}, {SpaceID: b.ID, Role: PluginRoleViewer}}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteSpace(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if g, _ := db.ListPluginGrants(ctx, p.ID); len(g) != 1 || g[0].SpaceID != b.ID {
		t.Fatalf("deleting a space must drop its grant: %v", g)
	}

	at := pluginBase
	wantIs(t, db.SetPluginSecret(ctx, p.ID, "karakeep_token", "v1", at), nil, "set secret")
	wantIs(t, db.SetPluginSecret(ctx, p.ID, "karakeep_token", "v2", at.Add(time.Minute)), nil, "replace secret")
	wantIs(t, db.SetPluginSecret(ctx, p.ID, "api_key", "k", at), nil, "second secret")
	if v, err := db.GetPluginSecret(ctx, p.ID, "karakeep_token"); err != nil || v != "v2" {
		t.Fatalf("secret value %q %v", v, err)
	}
	infos, err := db.ListPluginSecrets(ctx, p.ID)
	if err != nil || len(infos) != 2 || infos[0].Name != "api_key" || !infos[1].UpdatedAt.Equal(at.Add(time.Minute)) {
		t.Fatalf("secret list: %+v %v", infos, err)
	}
	_, err = db.GetPluginSecret(ctx, p.ID, "unset")
	wantIs(t, err, core.ErrNotFound, "unset secret")
	wantIs(t, db.SetPluginSecret(ctx, "missing", "x", "v", at), core.ErrNotFound, "secret of unknown plugin")
	wantIs(t, db.SetPluginSecret(ctx, p.ID, "", "v", at), core.ErrInvalid, "empty secret name")
	wantIs(t, db.DeletePluginSecret(ctx, p.ID, "api_key"), nil, "delete secret")
	wantIs(t, db.DeletePluginSecret(ctx, p.ID, "api_key"), core.ErrNotFound, "delete secret twice")
}

func TestUserKind(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	u := mustUser(t, db, "person@example.com")
	if kind, err := db.UserKind(ctx, u.ID); err != nil || kind != UserKindPerson {
		t.Fatalf("default kind %q %v", kind, err)
	}
	if ok, err := db.UserCanSignIn(ctx, u.ID); err != nil || !ok {
		t.Fatalf("person sign in: %v %v", ok, err)
	}
	wantIs(t, db.SetUserKind(ctx, u.ID, UserKindPlugin), nil, "set plugin kind")
	if ok, err := db.UserCanSignIn(ctx, u.ID); err != nil || ok {
		t.Fatalf("plugin user may sign in: %v %v", ok, err)
	}
	wantIs(t, db.SetUserKind(ctx, u.ID, "robot"), core.ErrInvalid, "unknown kind")
	wantIs(t, db.SetUserKind(ctx, core.NewID(), UserKindPlugin), core.ErrNotFound, "unknown user")
	_, err := db.UserCanSignIn(ctx, core.NewID())
	wantIs(t, err, core.ErrNotFound, "sign in of unknown user")
}

func TestKVLimits(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	mustPlugin(t, db, "kv-a")
	mustPlugin(t, db, "kv-b")
	type step struct {
		name    string
		del     bool
		plugin  string
		key     string
		size    int
		wantErr error
	}
	steps := []step{
		{name: "empty value", plugin: "kv-a", key: "empty", size: 0},
		{name: "value at limit", plugin: "kv-a", key: "v000", size: MaxKVValue},
		{name: "value over limit", plugin: "kv-a", key: "big", size: MaxKVValue + 1, wantErr: core.ErrTooLarge},
		{name: "empty key", plugin: "kv-a", key: "", size: 1, wantErr: core.ErrInvalid},
		{name: "long key", plugin: "kv-a", key: strings.Repeat("k", MaxStoreKey+1), size: 1, wantErr: core.ErrInvalid},
		{name: "unknown plugin", plugin: "missing", key: "k", size: 1, wantErr: core.ErrNotFound},
	}
	for i := 1; i < MaxKVTotal/MaxKVValue; i++ {
		steps = append(steps, step{name: "fill to the total limit", plugin: "kv-a", key: fmt.Sprintf("v%03d", i), size: MaxKVValue})
	}
	steps = append(steps,
		step{name: "one byte over total", plugin: "kv-a", key: "extra", size: 1, wantErr: core.ErrQuotaExceeded},
		step{name: "overwrite at total", plugin: "kv-a", key: "v000", size: MaxKVValue},
		step{name: "shrink a value", plugin: "kv-a", key: "v001", size: MaxKVValue - 1},
		step{name: "use the freed byte", plugin: "kv-a", key: "extra", size: 1},
		step{name: "grow past total", plugin: "kv-a", key: "extra", size: 2, wantErr: core.ErrQuotaExceeded},
		step{name: "other plugin has its own quota", plugin: "kv-b", key: "v000", size: MaxKVValue},
		step{name: "delete frees space", del: true, plugin: "kv-a", key: "v002"},
		step{name: "put after delete", plugin: "kv-a", key: "v002", size: MaxKVValue},
		step{name: "delete missing key", del: true, plugin: "kv-a", key: "nope", wantErr: core.ErrNotFound},
	)
	buf := bytes.Repeat([]byte("0123456789abcdef"), MaxKVValue/16+1)
	for i, s := range steps {
		var err error
		if s.del {
			err = db.DeleteKV(ctx, s.plugin, s.key)
		} else {
			err = db.PutKV(ctx, s.plugin, s.key, buf[:s.size], pluginBase.Add(time.Duration(i)))
		}
		wantIs(t, err, s.wantErr, fmt.Sprintf("step %d %s", i, s.name))
	}
	count, used, err := db.KVUsage(ctx, "kv-a")
	if err != nil || count != MaxKVTotal/MaxKVValue+2 || used != MaxKVTotal {
		t.Fatalf("usage %d entries %d bytes %v, want %d entries %d bytes", count, used, err, MaxKVTotal/MaxKVValue+2, MaxKVTotal)
	}
	e, err := db.GetKV(ctx, "kv-a", "v001")
	if err != nil || e.Size != MaxKVValue-1 || !bytes.Equal(e.Value, buf[:MaxKVValue-1]) {
		t.Fatalf("stored value differs: size %d %v", e.Size, err)
	}
	if e, err := db.GetKV(ctx, "kv-a", "empty"); err != nil || e.Value == nil || len(e.Value) != 0 {
		t.Fatalf("empty value: %+v %v", e, err)
	}
	_, err = db.GetKV(ctx, "kv-a", "big")
	wantIs(t, err, core.ErrNotFound, "rejected value was stored")

	var pages [][]string
	cursor := ""
	for {
		page, next, err := db.ListKV(ctx, "kv-a", "v00", cursor, 4)
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		for _, e := range page {
			if e.Value != nil {
				t.Fatalf("listing returned a value for %s", e.Key)
			}
			keys = append(keys, e.Key)
		}
		pages = append(pages, keys)
		if next == "" {
			break
		}
		cursor = next
	}
	want := [][]string{{"v000", "v001", "v002", "v003"}, {"v004", "v005", "v006", "v007"}, {"v008", "v009"}}
	if fmt.Sprint(pages) != fmt.Sprint(want) {
		t.Fatalf("pages %v, want %v", pages, want)
	}
	if page, _, _ := db.ListKV(ctx, "kv-a", "v_", "", 0); len(page) != 0 {
		t.Fatalf("prefix with a LIKE wildcard matched %d keys", len(page))
	}
	if page, _, _ := db.ListKV(ctx, "kv-a", "V00", "", 0); len(page) != 0 {
		t.Fatalf("prefix matched case-insensitively: %d keys", len(page))
	}
}

func TestBlobQuota(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	mustPlugin(t, db, "blobs")
	sha := strings.Repeat("ab", 32)
	type step struct {
		name    string
		op      string
		key     string
		size    int64
		sha     string
		wantErr error
	}
	steps := []step{
		{name: "blob at size limit", op: "put", key: "b0", size: MaxBlobSize},
		{name: "blob over size limit", op: "check", key: "big", size: MaxBlobSize + 1, wantErr: core.ErrTooLarge},
		{name: "blob over size limit on put", op: "put", key: "big", size: MaxBlobSize + 1, wantErr: core.ErrTooLarge},
		{name: "bad digest", op: "put", key: "bad", size: 1, sha: "XYZ", wantErr: core.ErrInvalid},
		{name: "negative size", op: "put", key: "neg", size: -1, wantErr: core.ErrInvalid},
	}
	for i := int64(1); i < MaxBlobTotal/MaxBlobSize; i++ {
		steps = append(steps, step{name: "fill to the total limit", op: "put", key: fmt.Sprintf("b%d", i), size: MaxBlobSize})
	}
	steps = append(steps,
		step{name: "check one byte over total", op: "check", key: "extra", size: 1, wantErr: core.ErrQuotaExceeded},
		step{name: "put one byte over total", op: "put", key: "extra", size: 1, wantErr: core.ErrQuotaExceeded},
		step{name: "replace at total", op: "check", key: "b0", size: MaxBlobSize},
		step{name: "replace at total on put", op: "put", key: "b0", size: MaxBlobSize},
		step{name: "delete frees space", op: "del", key: "b1"},
		step{name: "put after delete", op: "put", key: "extra", size: MaxBlobSize},
		step{name: "delete missing", op: "del", key: "b1", wantErr: core.ErrNotFound},
	)
	for i, s := range steps {
		digest := sha
		if s.sha != "" {
			digest = s.sha
		}
		var err error
		switch s.op {
		case "put":
			err = db.PutBlobMeta(ctx, BlobMeta{PluginID: "blobs", Key: s.key, Size: s.size, SHA256: digest, UpdatedAt: pluginBase})
		case "check":
			err = db.CheckBlobQuota(ctx, "blobs", s.key, s.size)
		case "del":
			err = db.DeleteBlobMeta(ctx, "blobs", s.key)
		}
		wantIs(t, err, s.wantErr, fmt.Sprintf("step %d %s", i, s.name))
	}
	count, used, err := db.BlobUsage(ctx, "blobs")
	if err != nil || count != int(MaxBlobTotal/MaxBlobSize) || used != MaxBlobTotal {
		t.Fatalf("usage %d blobs %d bytes %v", count, used, err)
	}
	m, err := db.GetBlobMeta(ctx, "blobs", "extra")
	if err != nil || m.SHA256 != sha || m.Size != MaxBlobSize || !m.UpdatedAt.Equal(pluginBase) {
		t.Fatalf("blob meta: %+v %v", m, err)
	}
	list, next, err := db.ListBlobMeta(ctx, "blobs", "b", "", 3)
	if err != nil || len(list) != 3 || list[0].Key != "b0" || next != "b3" {
		t.Fatalf("blob list: %v %q %v", list, next, err)
	}
}

func TestIdempotencyStates(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	mustPlugin(t, db, "idem")
	body := []byte(`{"id":"n1"}`)
	type step struct {
		name       string
		op         string
		at         time.Duration
		key        string
		method     string
		path       string
		status     int
		wantErr    error
		wantReplay int
		wantPurged int
	}
	steps := []step{
		{name: "new key runs", op: "begin", key: "k1", method: "POST", path: "/notes"},
		{name: "duplicate while pending", op: "begin", at: time.Second, key: "k1", method: "POST", path: "/notes", wantErr: ErrIdempotencyPending},
		{name: "other method", op: "begin", at: time.Second, key: "k1", method: "PUT", path: "/notes", wantErr: ErrIdempotencyMismatch},
		{name: "other path", op: "begin", at: time.Second, key: "k1", method: "POST", path: "/folders", wantErr: ErrIdempotencyMismatch},
		{name: "complete", op: "complete", key: "k1", status: 201},
		{name: "complete twice", op: "complete", key: "k1", status: 201, wantErr: core.ErrConflict},
		{name: "replay", op: "begin", at: time.Hour, key: "k1", method: "POST", path: "/notes", wantReplay: 201},
		{name: "other path after completion", op: "begin", at: time.Hour, key: "k1", method: "POST", path: "/files", wantErr: ErrIdempotencyMismatch},
		{name: "replay just before retention ends", op: "begin", at: 24*time.Hour - time.Nanosecond, key: "k1", method: "POST", path: "/notes", wantReplay: 201},
		{name: "expired key runs again", op: "begin", at: 24 * time.Hour, key: "k1", method: "POST", path: "/notes"},
		{name: "release pending", op: "release", key: "k1"},
		{name: "released key runs again", op: "begin", at: 24 * time.Hour, key: "k1", method: "POST", path: "/notes"},
		{name: "pending before the abandon timeout", op: "begin", at: 24*time.Hour + IdempotencyPendingTimeout - time.Nanosecond, key: "k1", method: "POST", path: "/notes", wantErr: ErrIdempotencyPending},
		{name: "abandoned pending key runs again", op: "begin", at: 24*time.Hour + IdempotencyPendingTimeout, key: "k1", method: "POST", path: "/other"},
		{name: "second key", op: "begin", at: 30 * time.Hour, key: "k2", method: "POST", path: "/x"},
		{name: "release keeps a completed key", op: "complete", key: "k2", status: 200},
		{name: "release completed", op: "release", key: "k2"},
		{name: "completed key survives release", op: "begin", at: 30 * time.Hour, key: "k2", method: "POST", path: "/x", wantReplay: 200},
		{name: "complete unknown key", op: "complete", key: "missing", status: 200, wantErr: core.ErrNotFound},
		{name: "bad status", op: "complete", key: "k1", status: 42, wantErr: core.ErrInvalid},
		{name: "empty key", op: "begin", key: "", method: "POST", path: "/x", wantErr: core.ErrInvalid},
		{name: "long key", op: "begin", key: strings.Repeat("k", MaxIdempotencyKey+1), method: "POST", path: "/x", wantErr: core.ErrInvalid},
		{name: "longest key", op: "begin", at: 30 * time.Hour, key: strings.Repeat("k", MaxIdempotencyKey), method: "POST", path: "/x"},
		{name: "purge", op: "purge", at: 48*time.Hour + IdempotencyPendingTimeout, wantPurged: 1},
	}
	for i, s := range steps {
		what := fmt.Sprintf("step %d %s", i, s.name)
		now := pluginBase.Add(s.at)
		switch s.op {
		case "begin":
			r, err := db.BeginIdempotent(ctx, "idem", s.key, s.method, s.path, now)
			wantIs(t, err, s.wantErr, what)
			switch {
			case s.wantReplay == 0 && r != nil:
				t.Fatalf("%s: got a replay of %d, want to run the request", what, r.Status)
			case s.wantReplay != 0 && (r == nil || r.Status != s.wantReplay || !bytes.Equal(r.Body, body)):
				t.Fatalf("%s: got replay %+v, want status %d", what, r, s.wantReplay)
			}
		case "complete":
			wantIs(t, db.CompleteIdempotent(ctx, "idem", s.key, s.status, body), s.wantErr, what)
		case "release":
			wantIs(t, db.ReleaseIdempotent(ctx, "idem", s.key), s.wantErr, what)
		case "purge":
			n, err := db.PurgeIdempotency(ctx, now)
			wantIs(t, err, s.wantErr, what)
			if n != s.wantPurged {
				t.Fatalf("%s: purged %d, want %d", what, n, s.wantPurged)
			}
		}
	}
	_, err := db.BeginIdempotent(ctx, "missing", "k", "POST", "/x", pluginBase)
	wantIs(t, err, core.ErrNotFound, "unknown plugin")
}

func TestIdempotencyConcurrentDuplicates(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	mustPlugin(t, db, "idem")
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		run     int
		pending int
		other   []error
	)
	for range 8 {
		wg.Go(func() {
			r, err := db.BeginIdempotent(ctx, "idem", "same", "POST", "/notes", pluginBase)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil && r == nil:
				run++
			case errors.Is(err, ErrIdempotencyPending):
				pending++
			default:
				other = append(other, fmt.Errorf("replay %v, error %w", r, err))
			}
		})
	}
	wg.Wait()
	if run != 1 || pending != 7 || len(other) != 0 {
		t.Fatalf("run %d, pending %d, other %v; want 1 and 7", run, pending, other)
	}
}

func TestActingTokenExpiry(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	u := mustUser(t, db, "actor@example.com")
	mustPlugin(t, db, "acting-a")
	mustPlugin(t, db, "acting-b")
	t0 := pluginBase
	create := func(tok ActingToken, at time.Time) *ActingToken {
		t.Helper()
		if err := db.CreateActingToken(ctx, &tok, at); err != nil {
			t.Fatalf("create %s: %v", tok.TokenHash, err)
		}
		return &tok
	}
	h1 := create(ActingToken{TokenHash: "h1", PluginID: "acting-a", UserID: u.ID, Interaction: "command:add-url:1"}, t0)
	h2 := create(ActingToken{TokenHash: "h2", PluginID: "acting-a", UserID: u.ID, ExpiresAt: t0.Add(time.Hour)}, t0)
	h3 := create(ActingToken{TokenHash: "h3", PluginID: "acting-a", UserID: u.ID, ExpiresAt: t0.Add(time.Minute)}, t0)
	create(ActingToken{TokenHash: "h4", PluginID: "acting-b", UserID: u.ID}, t0.Add(5*time.Minute))
	if !h1.ExpiresAt.Equal(t0.Add(ActingTokenTTL)) || !h2.ExpiresAt.Equal(t0.Add(ActingTokenTTL)) || !h3.ExpiresAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("expiry defaults: %s %s %s", h1.ExpiresAt, h2.ExpiresAt, h3.ExpiresAt)
	}

	tests := []struct {
		name  string
		hash  string
		at    time.Time
		valid bool
	}{
		{"fresh token", "h1", t0, true},
		{"one nanosecond before expiry", "h1", t0.Add(ActingTokenTTL - time.Nanosecond), true},
		{"at expiry", "h1", t0.Add(ActingTokenTTL), false},
		{"requested lifetime is capped", "h2", t0.Add(ActingTokenTTL), false},
		{"shorter lifetime is kept", "h3", t0.Add(59 * time.Second), true},
		{"shorter lifetime expires", "h3", t0.Add(time.Minute), false},
		{"unknown token", "nope", t0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tok, err := db.ResolveActingToken(ctx, tc.hash, tc.at)
			if !tc.valid {
				wantIs(t, err, core.ErrNotFound, tc.name)
				return
			}
			if err != nil || tok.UserID != u.ID || tok.PluginID != "acting-a" || !tok.CreatedAt.Equal(t0) {
				t.Fatalf("resolved %+v %v", tok, err)
			}
		})
	}
	if tok, _ := db.ResolveActingToken(ctx, "h1", t0); tok.Interaction != "command:add-url:1" {
		t.Fatalf("interaction %q", tok.Interaction)
	}

	err := db.CreateActingToken(ctx, &ActingToken{TokenHash: "h1", PluginID: "acting-a", UserID: u.ID}, t0)
	wantIs(t, err, core.ErrExists, "duplicate hash")
	err = db.CreateActingToken(ctx, &ActingToken{TokenHash: "h9", PluginID: "acting-a"}, t0)
	wantIs(t, err, core.ErrInvalid, "missing user")
	err = db.CreateActingToken(ctx, &ActingToken{TokenHash: "h9", PluginID: "missing", UserID: u.ID}, t0)
	wantIs(t, err, core.ErrNotFound, "unknown plugin")

	if n, err := db.PurgeActingTokens(ctx, t0.Add(ActingTokenTTL)); err != nil || n != 3 {
		t.Fatalf("purge: %d %v, want 3", n, err)
	}
	if _, err := db.ResolveActingToken(ctx, "h4", t0.Add(6*time.Minute)); err != nil {
		t.Fatalf("purge removed a live token: %v", err)
	}
	create(ActingToken{TokenHash: "h5", PluginID: "acting-a", UserID: u.ID}, t0)
	if n, err := db.RevokeActingTokens(ctx, "acting-b"); err != nil || n != 1 {
		t.Fatalf("revoke: %d %v, want 1", n, err)
	}
	_, err = db.ResolveActingToken(ctx, "h4", t0.Add(6*time.Minute))
	wantIs(t, err, core.ErrNotFound, "revoked token")
	if err := db.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	_, err = db.ResolveActingToken(ctx, "h5", t0)
	wantIs(t, err, core.ErrNotFound, "token of a deleted user")
}

func TestUninstallPluginOptions(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	u := mustUser(t, db, "admin@example.com")
	space := mustSpace(t, db, "docs")
	t0 := pluginBase

	fill := func(t *testing.T, id string) {
		t.Helper()
		mustPlugin(t, db, id)
		for _, err := range []error{
			db.SetPluginGrants(ctx, id, []PluginGrant{{SpaceID: space.ID, Role: PluginRoleEditor}}),
			db.SetPluginSecret(ctx, id, "token", "v", t0),
			db.EnqueueDeliveries(ctx, []PluginDelivery{{PluginID: id, Path: "/events", Body: []byte(`{}`)}}, t0),
			db.PutKV(ctx, id, "k", []byte("v"), t0),
			db.PutBlobMeta(ctx, BlobMeta{PluginID: id, Key: "b", Size: 10, SHA256: strings.Repeat("0", 64), UpdatedAt: t0}),
			db.CreateActingToken(ctx, &ActingToken{TokenHash: "acting-" + id, PluginID: id, UserID: u.ID}, t0),
		} {
			if err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.BeginIdempotent(ctx, id, "k", "POST", "/notes", t0); err != nil {
			t.Fatal(err)
		}
	}
	tables := []string{"plugin_grants", "plugin_secrets", "plugin_deliveries", "plugin_kv", "plugin_blobs", "acting_tokens", "plugin_idempotency"}
	counts := func(t *testing.T, id string) map[string]int {
		t.Helper()
		out := map[string]int{}
		for _, table := range tables {
			var n int
			if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE plugin_id = ?`, id).Scan(&n); err != nil {
				t.Fatal(err)
			}
			out[table] = n
		}
		return out
	}
	fill(t, "bystander")

	all := UninstallOptions{DeleteKV: true, DeleteBlobs: true, DeleteGrants: true, DeleteDeliveries: true, DeleteSecrets: true}
	tests := []struct {
		name string
		opts UninstallOptions
	}{
		{"keep everything", UninstallOptions{}},
		{"delete kv", UninstallOptions{DeleteKV: true}},
		{"delete blobs", UninstallOptions{DeleteBlobs: true}},
		{"delete grants", UninstallOptions{DeleteGrants: true}},
		{"delete deliveries", UninstallOptions{DeleteDeliveries: true}},
		{"delete secrets", UninstallOptions{DeleteSecrets: true}},
		{"delete everything", all},
	}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id := fmt.Sprintf("uninstall-%d", i)
			fill(t, id)
			if err := db.UninstallPlugin(ctx, id, tc.opts, t0.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			keep := func(deleted bool) int {
				if deleted {
					return 0
				}
				return 1
			}
			want := map[string]int{
				"plugin_grants":      keep(tc.opts.DeleteGrants),
				"plugin_secrets":     keep(tc.opts.DeleteSecrets),
				"plugin_deliveries":  keep(tc.opts.DeleteDeliveries),
				"plugin_kv":          keep(tc.opts.DeleteKV),
				"plugin_blobs":       keep(tc.opts.DeleteBlobs),
				"acting_tokens":      0,
				"plugin_idempotency": 0,
			}
			if got := counts(t, id); fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("rows after uninstall %v, want %v", got, want)
			}
			p, err := db.GetPlugin(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if p.Status != PluginUninstalled || p.TokenHash != "" || p.TokenPrefix != "" || p.SigningSecret != "c2VjcmV0" || !p.UpdatedAt.Equal(t0.Add(time.Hour)) {
				t.Fatalf("plugin row after uninstall: %+v", p)
			}
			_, err = db.GetPluginByTokenHash(ctx, "hash-"+id)
			wantIs(t, err, core.ErrNotFound, "revoked plugin token")
			_, err = db.ResolveActingToken(ctx, "acting-"+id, t0)
			wantIs(t, err, core.ErrNotFound, "acting token after uninstall")
		})
	}
	for table, n := range counts(t, "bystander") {
		if n != 1 {
			t.Fatalf("uninstall touched another plugin: %s has %d rows", table, n)
		}
	}
	wantIs(t, db.UninstallPlugin(ctx, "missing", all, t0), core.ErrNotFound, "uninstall unknown plugin")
	if err := db.DeletePlugin(ctx, "uninstall-0"); err != nil {
		t.Fatal(err)
	}
	for table, n := range counts(t, "uninstall-0") {
		if n != 0 {
			t.Fatalf("delete plugin left %d rows in %s", n, table)
		}
	}
}

func TestDeliveryBackoff(t *testing.T) {
	tests := []struct {
		attempts int
		want     time.Duration
	}{
		{0, 5 * time.Second}, {1, 5 * time.Second}, {2, 10 * time.Second}, {3, 20 * time.Second},
		{4, 40 * time.Second}, {5, 80 * time.Second}, {6, 160 * time.Second}, {7, 320 * time.Second},
		{8, 10 * time.Minute}, {9, 10 * time.Minute}, {1000, 10 * time.Minute},
	}
	for _, tc := range tests {
		if got := DeliveryBackoff(tc.attempts); got != tc.want {
			t.Errorf("DeliveryBackoff(%d) = %s, want %s", tc.attempts, got, tc.want)
		}
	}
}

func TestDeliveryQueueBasics(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	mustPlugin(t, db, "dq-a")
	mustPlugin(t, db, "dq-b")
	t0 := pluginBase
	batch := make([]PluginDelivery, 150)
	for i := range batch {
		batch[i] = PluginDelivery{PluginID: "dq-a", Path: "/events", Body: []byte(fmt.Sprint(i))}
	}
	if err := db.EnqueueDeliveries(ctx, batch, t0); err != nil {
		t.Fatal(err)
	}
	bEnqueued := []PluginDelivery{{PluginID: "dq-b", Path: "/events", Body: []byte("b")}, {PluginID: "dq-b", Path: "/events"}}
	if err := db.EnqueueDeliveries(ctx, bEnqueued, t0); err != nil {
		t.Fatal(err)
	}
	mixed := []PluginDelivery{{PluginID: "dq-a", Path: "/events"}, {PluginID: "missing", Path: "/events"}}
	wantIs(t, db.EnqueueDeliveries(ctx, mixed, t0), core.ErrNotFound, "unknown plugin in batch")
	wantIs(t, db.EnqueueDeliveries(ctx, []PluginDelivery{{PluginID: "dq-a", Path: "events"}}, t0), core.ErrInvalid, "relative path")
	if n, _ := db.CountDeliveries(ctx, "dq-a"); n != 150 {
		t.Fatalf("a failed batch inserted rows: %d", n)
	}
	if byPlugin, err := db.CountDeliveriesByPlugin(ctx); err != nil || byPlugin["dq-a"] != 150 || byPlugin["dq-b"] != 2 || len(byPlugin) != 2 {
		t.Fatalf("counts by plugin: %v %v", byPlugin, err)
	}

	first, err := db.ClaimDeliveries(ctx, "dq-a", t0, 0)
	if err != nil || len(first) != MaxDeliveryBatch {
		t.Fatalf("first claim: %d %v", len(first), err)
	}
	if first[0].Attempts != 1 || !first[0].NextAttemptAt.Equal(t0.Add(DeliveryClaimLease)) || !first[0].ExpiresAt.Equal(t0.Add(DeliveryTTL)) {
		t.Fatalf("claimed delivery: %+v", first[0])
	}
	second, err := db.ClaimDeliveries(ctx, "dq-a", t0, 500)
	if err != nil || len(second) != 50 {
		t.Fatalf("second claim: %d %v", len(second), err)
	}
	if again, _ := db.ClaimDeliveries(ctx, "dq-a", t0.Add(DeliveryClaimLease-time.Nanosecond), 0); len(again) != 0 {
		t.Fatalf("claimed %d leased deliveries", len(again))
	}
	if next, _ := db.NextDeliveryAt(ctx, "dq-a", t0); !next.Equal(t0.Add(DeliveryClaimLease)) {
		t.Fatalf("next delivery of a %s", next)
	}
	if next, _ := db.NextDeliveryAt(ctx, "dq-b", t0); !next.Equal(t0) {
		t.Fatalf("next delivery of b %s", next)
	}
	if next, err := db.NextDeliveryAt(ctx, "none", t0); err != nil || !next.IsZero() {
		t.Fatalf("next delivery of none %s %v", next, err)
	}

	var firstIDs, allIDs []string
	for _, x := range first {
		firstIDs = append(firstIDs, x.ID)
	}
	for _, x := range append(first, second...) {
		allIDs = append(allIDs, x.ID)
	}
	if err := db.FailDeliveries(ctx, firstIDs, 503, t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	retry, err := db.ClaimDeliveries(ctx, "dq-a", t0.Add(time.Second+DeliveryBackoffMin), 0)
	if err != nil || len(retry) != MaxDeliveryBatch || retry[0].Attempts != 2 || retry[0].LastStatus != 503 {
		t.Fatalf("retry claim: %d %v", len(retry), err)
	}
	if err := db.CompleteDeliveries(ctx, allIDs); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.CountDeliveries(ctx, "dq-a"); n != 0 {
		t.Fatalf("completed deliveries left %d rows", n)
	}

	// Both b deliveries share their due time and CreatedAt, so the random id
	// decides their order. Claim both and match the body by id.
	bs, err := db.ClaimDeliveries(ctx, "dq-b", t0, 0)
	if err != nil || len(bs) != 2 {
		t.Fatalf("claim b: %+v %v", bs, err)
	}
	bodies := map[string]string{}
	for _, x := range bs {
		if x.Path != "/events" {
			t.Fatalf("claim b path %q", x.Path)
		}
		bodies[x.ID] = string(x.Body)
	}
	if bodies[bEnqueued[0].ID] != "b" || bodies[bEnqueued[1].ID] != "" {
		t.Fatalf("claim b bodies %v", bodies)
	}
	if bs, _ := db.ClaimDeliveries(ctx, "dq-b", t0.Add(DeliveryTTL), 0); len(bs) != 0 {
		t.Fatalf("claimed %d expired deliveries", len(bs))
	}
	if n, err := db.ExpireDeliveries(ctx, t0.Add(DeliveryTTL)); err != nil || n != 2 {
		t.Fatalf("expire: %d %v", n, err)
	}
}

// deliveryModel is one queued delivery in the FuzzDeliveryQueue model.
type deliveryModel struct {
	plugin   string
	next     time.Time
	expires  time.Time
	created  time.Time
	attempts int
}

type deliveryRow struct {
	plugin   string
	next     time.Time
	expires  time.Time
	attempts int
}

func readDeliveryRows(t *testing.T, db *DB) map[string]deliveryRow {
	t.Helper()
	rows, err := db.sql.QueryContext(context.Background(),
		`SELECT id, plugin_id, next_attempt_at, expires_at, attempts FROM plugin_deliveries`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]deliveryRow{}
	for rows.Next() {
		var (
			id, next, expires string
			r                 deliveryRow
		)
		if err := rows.Scan(&id, &r.plugin, &next, &expires, &r.attempts); err != nil {
			t.Fatal(err)
		}
		r.next, r.expires = parseTime(next), parseTime(expires)
		out[id] = r
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// modelBackoff restates the delivery backoff rule independently of
// DeliveryBackoff.
func modelBackoff(attempts int) time.Duration {
	delay := 5 * time.Second
	for n := 1; n < attempts && delay < 10*time.Minute; n++ {
		delay *= 2
	}
	return min(delay, 10*time.Minute)
}

// FuzzDeliveryQueue runs a random sequence of enqueue, claim, fail, succeed,
// clock advance and expiry against the store and a model. After every step
// the stored rows must equal the model, which checks that no delivery is
// lost before expiry. Claims must match the model and be ordered by due
// time, backoff delays must not decrease per delivery, and no delivery may
// be claimed or survive an expiry pass once expired.
func FuzzDeliveryQueue(f *testing.F) {
	f.Add([]byte{0, 3, 1, 0, 2, 255, 4, 1, 1, 0, 3, 255})
	f.Add([]byte{0, 2, 0, 130, 1, 4, 2, 1, 4, 1, 1, 4, 2, 3, 4, 2, 1, 4, 2, 7, 4, 8, 5, 0, 1, 0})
	f.Add([]byte{0, 66, 0, 194, 1, 64, 1, 192, 4, 1, 1, 64, 3, 5, 2, 250, 4, 3, 1, 64, 5, 0})
	chain := []byte{0, 0}
	for range 12 {
		chain = append(chain, 1, 0, 2, 1, 4, 4)
	}
	f.Add(append(chain, 4, 8, 5, 0, 1, 0))

	db := newTestDB(f)
	ctx := context.Background()
	plugins := []string{"fuzz-a", "fuzz-b"}
	for _, id := range plugins {
		if err := db.CreatePlugin(ctx, &Plugin{ID: id, BaseURL: "http://" + id, Status: PluginActive}); err != nil {
			f.Fatal(err)
		}
	}
	advances := []time.Duration{time.Second, 5 * time.Second, 30 * time.Second, 2 * time.Minute,
		10 * time.Minute, time.Hour, 6 * time.Hour, 12 * time.Hour, 25 * time.Hour}

	f.Fuzz(func(t *testing.T, ops []byte) {
		if _, err := db.sql.ExecContext(ctx, `DELETE FROM plugin_deliveries`); err != nil {
			t.Fatal(err)
		}
		now := pluginBase
		model := map[string]*deliveryModel{}
		lastDelay := map[string]time.Duration{}
		var inflight []string
		for i := 0; i+1 < len(ops) && i < 600; i += 2 {
			op, arg := ops[i]%6, ops[i+1]
			step := fmt.Sprintf("step %d op %d arg %d", i/2, op, arg)
			plugin := plugins[int(arg>>7)]
			switch op {
			case 0:
				ds := make([]PluginDelivery, int(arg%3)+1)
				for j := range ds {
					ds[j] = PluginDelivery{PluginID: plugin, Path: "/events", Body: []byte{arg, byte(j)}}
					if arg&0x40 != 0 {
						ds[j].NextAttemptAt = now.Add(time.Duration(j*3) * time.Second)
					}
				}
				if err := db.EnqueueDeliveries(ctx, ds, now); err != nil {
					t.Fatalf("%s: enqueue: %v", step, err)
				}
				for j, x := range ds {
					next := now
					if arg&0x40 != 0 {
						next = now.Add(time.Duration(j*3) * time.Second)
					}
					model[x.ID] = &deliveryModel{plugin: plugin, next: next, expires: now.Add(24 * time.Hour), created: now}
				}
			case 1:
				limit := int(arg&0x0f)%6 + 1
				if arg&0x40 != 0 {
					limit = MaxDeliveryBatch
				}
				var due []string
				for id, m := range model {
					if m.plugin == plugin && !m.next.After(now) && m.expires.After(now) {
						due = append(due, id)
					}
				}
				slices.SortFunc(due, func(a, b string) int {
					if c := model[a].next.Compare(model[b].next); c != 0 {
						return c
					}
					if c := model[a].created.Compare(model[b].created); c != 0 {
						return c
					}
					return strings.Compare(a, b)
				})
				if len(due) > limit {
					due = due[:limit]
				}
				got, err := db.ClaimDeliveries(ctx, plugin, now, limit)
				if err != nil {
					t.Fatalf("%s: claim: %v", step, err)
				}
				var gotIDs []string
				for k, x := range got {
					gotIDs = append(gotIDs, x.ID)
					m, ok := model[x.ID]
					if !ok {
						t.Fatalf("%s: claimed %s, which the model does not hold", step, x.ID)
					}
					if !m.expires.After(now) {
						t.Fatalf("%s: claimed %s past its expiry", step, x.ID)
					}
					if k > 0 && model[got[k-1].ID].next.After(m.next) {
						t.Fatalf("%s: claim not ordered by due time at position %d", step, k)
					}
				}
				if !slices.Equal(gotIDs, due) {
					t.Fatalf("%s: claimed %v, model wants %v", step, gotIDs, due)
				}
				for _, x := range got {
					m := model[x.ID]
					m.attempts++
					m.next = now.Add(DeliveryClaimLease)
					if x.Attempts != m.attempts || !x.NextAttemptAt.Equal(m.next) {
						t.Fatalf("%s: claimed %s with attempts %d next %s", step, x.ID, x.Attempts, x.NextAttemptAt)
					}
					if !slices.Contains(inflight, x.ID) {
						inflight = append(inflight, x.ID)
					}
				}
			case 2, 3:
				var picked, rest []string
				for k, id := range inflight {
					if k < 8 && arg&(1<<k) != 0 {
						picked = append(picked, id)
					} else {
						rest = append(rest, id)
					}
				}
				inflight = rest
				if op == 3 {
					if err := db.CompleteDeliveries(ctx, picked); err != nil {
						t.Fatalf("%s: complete: %v", step, err)
					}
					for _, id := range picked {
						delete(model, id)
						delete(lastDelay, id)
					}
					break
				}
				if err := db.FailDeliveries(ctx, picked, 503, now); err != nil {
					t.Fatalf("%s: fail: %v", step, err)
				}
				rows := readDeliveryRows(t, db)
				for _, id := range picked {
					m, ok := model[id]
					if !ok {
						continue
					}
					delay := rows[id].next.Sub(now)
					if want := modelBackoff(m.attempts); delay != want {
						t.Fatalf("%s: %s after %d attempts waits %s, want %s", step, id, m.attempts, delay, want)
					}
					if delay < lastDelay[id] {
						t.Fatalf("%s: backoff of %s fell from %s to %s", step, id, lastDelay[id], delay)
					}
					lastDelay[id] = delay
					m.next = now.Add(delay)
				}
			case 4:
				now = now.Add(advances[int(arg)%len(advances)])
			case 5:
				n, err := db.ExpireDeliveries(ctx, now)
				if err != nil {
					t.Fatalf("%s: expire: %v", step, err)
				}
				want := 0
				for id, m := range model {
					if !m.expires.After(now) {
						delete(model, id)
						delete(lastDelay, id)
						want++
					}
				}
				if n != want {
					t.Fatalf("%s: expired %d, model wants %d", step, n, want)
				}
				for id, r := range readDeliveryRows(t, db) {
					if !r.expires.After(now) {
						t.Fatalf("%s: %s survived expiry at %s", step, id, r.expires)
					}
				}
			}

			rows := readDeliveryRows(t, db)
			for id, m := range model {
				r, ok := rows[id]
				switch {
				case !ok && m.expires.After(now):
					t.Fatalf("%s: %s lost before expiry", step, id)
				case !ok:
					t.Fatalf("%s: %s removed without an expiry pass", step, id)
				case r.plugin != m.plugin || !r.next.Equal(m.next) || !r.expires.Equal(m.expires) || r.attempts != m.attempts:
					t.Fatalf("%s: %s stored %+v, model %+v", step, id, r, *m)
				}
			}
			if len(rows) != len(model) {
				t.Fatalf("%s: store holds %d deliveries, model %d", step, len(rows), len(model))
			}
			for _, p := range plugins {
				count := 0
				var next time.Time
				for _, m := range model {
					if m.plugin != p {
						continue
					}
					count++
					if m.expires.After(now) && (next.IsZero() || m.next.Before(next)) {
						next = m.next
					}
				}
				if n, err := db.CountDeliveries(ctx, p); err != nil || n != count {
					t.Fatalf("%s: count of %s %d %v, model %d", step, p, n, err, count)
				}
				if got, err := db.NextDeliveryAt(ctx, p, now); err != nil || !got.Equal(next) {
					t.Fatalf("%s: next delivery of %s %s %v, model %s", step, p, got, err, next)
				}
			}
		}
	})
}
