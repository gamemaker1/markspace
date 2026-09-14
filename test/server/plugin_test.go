package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/index"
	"github.com/gamemaker1/markspace/internal/module"
	"github.com/gamemaker1/markspace/sdk"
)

// TestPluginEndToEnd walks the plugin protocol with a Go plugin on
// sdk: install, signatures on both sides, event delivery with
// retries and expiry, health, a command, a panel, an action, jobs, a
// schedule, a suggestion accepted by a person, and uninstall with and
// without the plugin's data.
func TestPluginEndToEnd(t *testing.T) {
	s := newPluginStack(t)
	ctx := context.Background()
	wiki := s.space("Wiki")
	other := s.space("Private")
	target := s.note(wiki.ID, "Harbour survey", "The harbour wall is old.\n\nIt needs repair.\n")
	hidden := s.note(other.ID, "Hidden", "not for plugins\n")

	tp := newTestPlugin(t, testManifest("test-plugin"))

	// Install through the admin API.
	res := s.install(tp.srv.URL, []map[string]string{{"spaceId": wiki.ID, "role": "editor"}})
	if res["delivered"] != true {
		t.Fatalf("install was not delivered: %v", res)
	}
	if res["token"] != nil || res["signingSecret"] != nil {
		t.Fatalf("delivered install must not show credentials: %v", res)
	}
	token := tp.pluginToken()
	if !strings.HasPrefix(token, core.PluginTokenPrefix) || len(token) != len(core.PluginTokenPrefix)+40 {
		t.Fatalf("plugin token shape %q", token)
	}
	creds := tp.sdk.Credentials()
	if len(creds.Secret) != 32 {
		t.Fatalf("signing secret is %d bytes", len(creds.Secret))
	}
	if _, err := os.Stat(filepath.Join(tp.dir, sdk.InstallFile)); err != nil {
		t.Fatalf("install.json not stored: %v", err)
	}
	row, err := s.db.GetPlugin(ctx, "test-plugin")
	if err != nil || row.Status != core.PluginStatusActive || row.TokenHash == "" || row.TokenHash == token {
		t.Fatalf("plugin row = %+v, %v", row, err)
	}
	if kind, _ := s.db.UserKind(ctx, row.UserID); kind != core.UserKindPlugin {
		t.Fatalf("plugin user kind %q", kind)
	}

	// Markspace side: the token authenticates on the host API only.
	var me map[string]any
	s.call(token, "GET", "/api/plugin/v1/me", nil, &me, 200)
	if me["pluginId"] != "test-plugin" || me["userId"] != row.UserID {
		t.Fatalf("me = %v", me)
	}
	if status, _, _ := s.request(token, "GET", "/api/tokens", nil); status != 401 {
		t.Fatalf("plugin token outside the host API: status %d, want 401", status)
	}
	if status, _, _ := s.request(token+"x", "GET", "/api/plugin/v1/me", nil); status != 401 {
		t.Fatalf("bad plugin token: status %d, want 401", status)
	}
	if status, _, _ := s.request(token, "GET", "/api/plugin/v1/items/"+hidden.ID, nil); status != 404 {
		t.Fatalf("ungranted note: status %d, want 404", status)
	}

	// Plugin side: unsigned, tampered and replayed requests are refused.
	body := []byte(`{"events":[]}`)
	post := func(sign func(r *http.Request)) int {
		req, _ := http.NewRequest("POST", tp.srv.URL+"/events", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		sign(req)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if st := post(func(*http.Request) {}); st != 401 {
		t.Fatalf("unsigned event call: status %d, want 401", st)
	}
	if st := post(func(r *http.Request) {
		sdk.SignRequest(r, creds.Secret, "test-plugin", "d1", []byte(`{"events":[1]}`), time.Now())
	}); st != 401 {
		t.Fatalf("tampered event call: status %d, want 401", st)
	}
	now := time.Now()
	signed := func(r *http.Request) { sdk.SignRequest(r, creds.Secret, "test-plugin", "d2", body, now) }
	if st := post(signed); st != 204 {
		t.Fatalf("signed event call: status %d, want 204", st)
	}
	if st := post(signed); st != 401 {
		t.Fatalf("replayed event call: status %d, want 401", st)
	}
	req, _ := http.NewRequest("POST", tp.srv.URL+"/commands/hello", bytes.NewReader(body))
	sdk.SignRequest(req, creds.Secret, "test-plugin", "d3", body, time.Now())
	req.URL.Path = "/panels/stats"
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 401 {
		t.Fatalf("a signature for one path must not work on another: %v %v", resp, err)
	}

	// Health: three failures mark the plugin down, a success brings it back.
	s.host.CheckHealth(ctx)
	status := func() string {
		row, err := s.db.GetPlugin(ctx, "test-plugin")
		if err != nil {
			t.Fatal(err)
		}
		return row.Status
	}
	if st := status(); st != core.PluginStatusActive {
		t.Fatalf("status after a good check: %s", st)
	}
	tp.failHealth.Store(true)
	for i := 0; i < 2; i++ {
		s.host.CheckHealth(ctx)
		if st := status(); st != core.PluginStatusActive {
			t.Fatalf("status after %d failures: %s", i+1, st)
		}
	}
	s.host.CheckHealth(ctx)
	if st := status(); st != core.PluginStatusDown {
		t.Fatalf("status after 3 failures: %s", st)
	}
	if status, data, _ := s.request(s.adminTok, "POST", "/api/commands/test-plugin.hello",
		map[string]any{"context": map[string]string{"itemId": target.ID}}); status != 503 {
		t.Fatalf("command on a down plugin: status %d, want 503: %s", status, data)
	}
	tp.failHealth.Store(false)
	s.host.CheckHealth(ctx)
	if st := status(); st != core.PluginStatusActive {
		t.Fatalf("status after recovery: %s", st)
	}

	// Event delivery with retries after 503.
	queued := func() int {
		n, err := s.db.CountDeliveries(ctx, "test-plugin")
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	baseline := queued()
	s.call(s.adminTok, "PATCH", "/api/notes/"+target.ID, core.NoteUpdate{Content: ptr("The harbour wall is old.\n\nIt needs repair soon.\n")}, nil, 200)
	s.note(other.ID, "Also hidden", "x\n")
	waitFor(t, "a queued delivery", func() bool { return queued() > baseline })
	tp.failEvents.Store(true)
	if sent, _, err := s.host.DeliverOnce(ctx, "test-plugin"); sent != 0 || err == nil {
		t.Fatalf("delivery to a failing plugin: sent %d, err %v", sent, err)
	}
	if sent, _, _ := s.host.DeliverOnce(ctx, "test-plugin"); sent != 0 {
		t.Fatal("a failed delivery must wait for its backoff")
	}
	s.clock.Advance(6 * time.Second)
	if sent, _, err := s.host.DeliverOnce(ctx, "test-plugin"); sent != 0 || err == nil {
		t.Fatalf("second attempt: sent %d, err %v", sent, err)
	}
	tp.failEvents.Store(false)
	s.clock.Advance(11 * time.Second)
	sent, _, err := s.host.DeliverOnce(ctx, "test-plugin")
	if err != nil || sent == 0 {
		t.Fatalf("delivery after recovery: sent %d, err %v", sent, err)
	}
	for sent > 0 {
		sent, _, _ = s.host.DeliverOnce(ctx, "test-plugin")
	}
	got := tp.eventTypes()
	if !slices.Contains(got, "note.updated:"+target.ID) {
		t.Fatalf("events %v lack note.updated of the target", got)
	}
	for _, e := range got {
		if strings.HasSuffix(e, ":"+hidden.ID) || strings.Contains(e, other.ID) {
			t.Fatalf("an event of an ungranted space was delivered: %v", got)
		}
	}
	tp.mu.Lock()
	for _, e := range tp.events {
		if e.SpaceID != "" && e.SpaceID != wiki.ID {
			t.Fatalf("event of space %s delivered", e.SpaceID)
		}
	}
	tp.mu.Unlock()

	// Expiry after 24 h.
	tp.failEvents.Store(true)
	before := queued()
	s.call(s.adminTok, "PATCH", "/api/notes/"+target.ID, core.NoteUpdate{Content: ptr("The harbour wall is old.\n\nIt needs repair now.\n")}, nil, 200)
	waitFor(t, "a delivery to expire", func() bool { return queued() > before })
	s.host.DeliverOnce(ctx, "test-plugin")
	s.clock.Advance(25 * time.Hour)
	if n := s.host.ExpireDeliveries(ctx); n == 0 {
		t.Fatal("no delivery expired after 25 h")
	}
	if n := queued(); n != 0 {
		t.Fatalf("%d deliveries left after expiry", n)
	}
	tp.failEvents.Store(false)

	// A command returning a toast, run with an acting token.
	var toast struct {
		Toast *struct{ Message, Kind string } `json:"toast"`
	}
	s.call(s.adminTok, "POST", "/api/commands/test-plugin.hello", map[string]any{"context": map[string]string{"itemId": target.ID}}, &toast, 200)
	if toast.Toast == nil || toast.Toast.Message != "Hello Harbour survey from Admin" || toast.Toast.Kind != "success" {
		t.Fatalf("command response %+v", toast.Toast)
	}
	tp.mu.Lock()
	acting := tp.lastToken
	tp.mu.Unlock()
	if !strings.HasPrefix(acting, core.ActingTokenPrefix) {
		t.Fatalf("the command got no acting token: %q", acting)
	}
	s.call(acting, "GET", "/api/plugin/v1/me", nil, &me, 200)
	if af, _ := me["actingFor"].(map[string]any); af == nil || af["id"] != s.adminID {
		t.Fatalf("acting token me = %v", me)
	}
	if status, _, _ := s.request(acting, "GET", "/api/plugin/v1/items/"+hidden.ID, nil); status != 404 {
		t.Fatalf("an acting token for an admin reached an ungranted space: %d", status)
	}

	// A panel returning a UI tree.
	var panel struct {
		UI map[string]any `json:"ui"`
	}
	s.call(s.adminTok, "POST", "/api/panels/test-plugin.stats", map[string]any{"context": map[string]string{"itemId": target.ID}}, &panel, 200)
	if panel.UI["type"] != "stack" {
		t.Fatalf("panel tree %v", panel.UI)
	}

	// An action with a payload.
	s.call(s.adminTok, "POST", "/api/modules/plugin.test-plugin/actions/echo", map[string]any{
		"context": map[string]string{"itemId": target.ID}, "payload": map[string]any{"value": 42},
	}, &toast, 200)
	if toast.Toast == nil || toast.Toast.Message != "echo 42 for "+target.ID {
		t.Fatalf("action response %+v", toast.Toast)
	}

	// Contributions list the plugin in its space and not elsewhere.
	var contrib module.Contributions
	s.call(s.adminTok, "GET", "/api/ui/contributions?itemId="+target.ID, nil, &contrib, 200)
	if !slices.ContainsFunc(contrib.Commands, func(c module.Command) bool { return c.ID == "test-plugin.hello" }) ||
		!slices.ContainsFunc(contrib.Panels, func(p module.Panel) bool { return p.ID == "test-plugin.stats" }) ||
		!slices.ContainsFunc(contrib.Panels, func(p module.Panel) bool { return p.Builtin == "comments" }) {
		t.Fatalf("contributions in the granted space: %+v", contrib)
	}
	s.call(s.adminTok, "GET", "/api/ui/contributions?itemId="+hidden.ID, nil, &contrib, 200)
	if slices.ContainsFunc(contrib.Commands, func(c module.Command) bool { return c.Module == "plugin.test-plugin" }) {
		t.Fatal("the plugin's command shows in an ungranted space")
	}

	// A job that answers 202 and completes through PATCH with progress.
	var job module.Job
	s.call(token, "POST", "/api/plugin/v1/jobs", map[string]any{"kind": "slow", "title": "Slow one", "spaceId": wiki.ID}, &job, 201)
	if job.Kind != "plugin.test-plugin.slow" || job.CreatedBy != "plugin:test-plugin" {
		t.Fatalf("job %+v", job)
	}
	store := s.parts.Jobs.Store()
	waitFor(t, "the slow job to report progress", func() bool {
		j, err := store.Get(ctx, job.ID)
		return err == nil && j.Status == module.JobRunning && j.Progress >= 2
	})
	j, _ := store.Get(ctx, job.ID)
	if j.Total != 3 || j.Message != "step 2" {
		t.Fatalf("progress %d/%d %q", j.Progress, j.Total, j.Message)
	}
	close(tp.release)
	<-tp.slowDone
	waitFor(t, "the slow job to finish", func() bool {
		j, err := store.Get(ctx, job.ID)
		return err == nil && j.Status.Finished()
	})
	j, _ = store.Get(ctx, job.ID)
	if j.Status != module.JobDone || !bytes.Contains(j.Result, []byte(`"steps":3`)) || j.Attempts != 1 {
		t.Fatalf("slow job ended %s after %d attempts with %s: %s", j.Status, j.Attempts, j.Result, j.Message)
	}
	logs, _ := store.Logs(ctx, job.ID)
	if !slices.ContainsFunc(logs, func(e module.JobLogEntry) bool { return e.Message == "slow job finished" }) {
		t.Fatalf("job log %+v", logs)
	}
	var api struct {
		Job struct {
			Status string `json:"status"`
		} `json:"job"`
	}
	s.call(s.adminTok, "GET", "/api/jobs/"+job.ID, nil, &api, 200)
	if api.Job.Status != "done" {
		t.Fatalf("jobs API status %q", api.Job.Status)
	}

	// A scheduled job.
	// A schedule first runs once its jitter, at most a tenth of the hour, has
	// passed since the scheduler saw it.
	s.parts.Scheduler.SetSchedules(s.parts.Registry.Schedules())
	enqueued, err := s.parts.Scheduler.Tick(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.clock.Advance(7 * time.Minute)
	more, err := s.parts.Scheduler.Tick(ctx)
	if err != nil {
		t.Fatal(err)
	}
	enqueued = append(enqueued, more...)
	if !slices.Contains(enqueued, "plugin.test-plugin.tick") {
		t.Fatalf("scheduled kinds %v", enqueued)
	}
	waitFor(t, "the tick job to finish", func() bool {
		list, _ := store.List(ctx, module.JobFilter{Kind: "plugin.test-plugin.tick", Statuses: []module.JobStatus{module.JobDone}})
		return len(list) == 1
	})

	// A suggestion created by the plugin and accepted by a person.
	versions := func() int {
		var out struct{ Items []*core.NoteVersion }
		s.call(s.adminTok, "GET", "/api/notes/"+target.ID+"/versions", nil, &out, 200)
		return len(out.Items)
	}
	var fresh core.Note
	s.call(s.adminTok, "GET", "/api/notes/"+target.ID, nil, &fresh, 200)
	body0 := fresh.Content
	start := strings.Index(body0, "repair now")
	if start < 0 {
		t.Fatalf("body %q", body0)
	}
	var sug core.Suggestion
	s.call(token, "POST", "/api/plugin/v1/suggestions", map[string]any{
		"noteId": target.ID, "title": "Say when", "rationale": "Be precise.",
		"edits": []map[string]any{{"anchor": map[string]any{"exact": "now", "start": start + 7, "end": start + 10}, "text": "before winter"}},
	}, &sug, 201)
	if sug.AuthorKind != "plugin" || sug.AuthorID != "test-plugin" || sug.Status != "open" {
		t.Fatalf("suggestion %+v", sug)
	}
	nVersions := versions()
	var accepted core.SuggestionResult
	s.call(s.adminTok, "POST", "/api/suggestions/"+sug.ID+"/accept", nil, &accepted, 200)
	if accepted.Suggestion.Status != "accepted" || accepted.Note == nil {
		t.Fatalf("accept result %+v", accepted)
	}
	s.call(s.adminTok, "GET", "/api/notes/"+target.ID, nil, &fresh, 200)
	if !strings.Contains(fresh.Content, "repair before winter.") {
		t.Fatalf("body after accept %q", fresh.Content)
	}
	if versions() <= nVersions {
		t.Fatal("accept saved no version")
	}

	// Plugin data to keep or delete.
	s.call(token, "PUT", "/api/plugin/v1/kv/state", map[string]any{"value": map[string]int{"n": 1}}, nil, 204)
	var made map[string]any
	s.call(token, "POST", "/api/plugin/v1/notes", map[string]any{"spaceId": wiki.ID, "title": "Wiki index", "content": "made by the plugin\n"}, &made, 201)
	madeID, _ := made["id"].(string)
	blob := strings.NewReader("blob bytes")
	breq, _ := http.NewRequest("PUT", s.srv.URL+"/api/plugin/v1/blobs/cache", blob)
	breq.Header.Set("Authorization", "Bearer "+token)
	if resp, err := http.DefaultClient.Do(breq); err != nil || resp.StatusCode != 200 {
		t.Fatalf("blob put: %v %v", resp, err)
	}
	blobDir := filepath.Join(s.cfg.DataDir, "internal", "plugins", "test-plugin", "blobs")

	// Uninstall, keeping the data.
	s.call(s.adminTok, "POST", "/api/admin/plugins/test-plugin/uninstall", map[string]bool{"deleteData": false}, nil, 204)
	if status, _, _ := s.request(token, "GET", "/api/plugin/v1/me", nil); status != 401 {
		t.Fatalf("token after uninstall: status %d", status)
	}
	if status, _, _ := s.request(acting, "GET", "/api/plugin/v1/me", nil); status != 401 {
		t.Fatalf("acting token after uninstall: status %d", status)
	}
	if _, ok := s.parts.Registry.Module("plugin.test-plugin"); ok {
		t.Fatal("the plugin module is still registered")
	}
	if _, err := s.db.GetKV(ctx, "test-plugin", "state"); err != nil {
		t.Fatalf("kv was not kept: %v", err)
	}
	if _, err := os.Stat(blobDir); err != nil {
		t.Fatalf("blobs were not kept: %v", err)
	}
	var item map[string]any
	s.call(s.adminTok, "GET", "/api/items/"+madeID, nil, &item, 200)
	if u, _ := s.db.GetUser(ctx, row.UserID); u == nil || u.IsActive {
		t.Fatal("the plugin user is still active")
	}

	// Install again, then uninstall with the data.
	tp2 := newTestPlugin(t, testManifest("test-plugin"))
	res = s.install(tp2.srv.URL, []map[string]string{{"spaceId": wiki.ID, "role": "editor"}})
	if res["delivered"] != true {
		t.Fatalf("reinstall: %v", res)
	}
	token2 := tp2.pluginToken()
	var kv struct {
		Value map[string]int `json:"value"`
	}
	s.call(token2, "GET", "/api/plugin/v1/kv/state", nil, &kv, 200)
	if kv.Value["n"] != 1 {
		t.Fatalf("kept kv after reinstall: %+v", kv)
	}
	if row2, _ := s.db.GetPlugin(ctx, "test-plugin"); row2.UserID != row.UserID {
		t.Fatal("reinstall made a new plugin user")
	}
	s.call(s.adminTok, "POST", "/api/admin/plugins/test-plugin/uninstall", map[string]bool{"deleteData": true}, nil, 204)
	if _, err := s.db.GetKV(ctx, "test-plugin", "state"); err == nil {
		t.Fatal("kv survived a delete uninstall")
	}
	if _, err := os.Stat(filepath.Dir(blobDir)); !os.IsNotExist(err) {
		t.Fatalf("blob directory survived: %v", err)
	}
	if status, _, _ := s.request(s.adminTok, "GET", "/api/items/"+madeID, nil); status != 404 {
		t.Fatalf("the plugin's note survived a delete uninstall: %d", status)
	}
	s.call(s.adminTok, "GET", "/api/items/"+target.ID, nil, &item, 200)
	if row3, _ := s.db.GetPlugin(ctx, "test-plugin"); row3.Status != core.PluginStatusUninstalled || row3.TokenHash != "" {
		t.Fatalf("row after uninstall %+v", row3)
	}
}

// TestPluginInstallNotDelivered shows the credentials once when the install
// call fails, and needs approval when a new manifest asks for more.
func TestPluginInstallNotDelivered(t *testing.T) {
	s := newPluginStack(t)
	ctx := context.Background()
	wiki := s.space("Wiki")
	man := testManifest("late-plugin", "items:read", "ui")
	man.Events, man.Jobs, man.Schedules, man.Importers, man.Secrets, man.Settings = nil, nil, nil, nil, nil, nil
	tp := newTestPlugin(t, man)
	// The plugin's install window is closed, so the install call fails.
	tp.sdk.InstallWindow = time.Nanosecond
	time.Sleep(time.Millisecond)

	var preview map[string]any
	s.call(s.adminTok, "POST", "/api/admin/plugins/preview", map[string]string{"baseUrl": tp.srv.URL}, &preview, 200)
	var res map[string]any
	s.call(s.adminTok, "POST", "/api/admin/plugins", map[string]any{
		"baseUrl": tp.srv.URL, "sha256": preview["sha256"], "grants": []map[string]string{{"spaceId": wiki.ID, "role": "viewer"}},
	}, &res, 201)
	if res["delivered"] != false || res["token"] == nil || res["signingSecret"] == nil || res["installError"] == "" {
		t.Fatalf("undelivered install result %v", res)
	}
	token, _ := res["token"].(string)
	s.call(token, "GET", "/api/plugin/v1/me", nil, nil, 200)

	// A stale review is refused.
	if status, _, _ := s.request(s.adminTok, "POST", "/api/admin/plugins/preview", map[string]string{"baseUrl": "http://127.0.0.1:1"}); status != 503 {
		t.Fatalf("preview of an unreachable plugin: %d", status)
	}

	// The same manifest changes nothing. More permissions need approval and
	// keep the old grant.
	var view map[string]any
	s.call(s.adminTok, "POST", "/api/admin/plugins/late-plugin/check", nil, &view, 200)
	if view["status"] != "active" {
		t.Fatalf("check with the same manifest: %v", view["status"])
	}
	bigger := man
	bigger.Version = "2.0.0"
	bigger.Permissions = []string{"items:read", "items:write", "ui"}
	tp.sdk = nil
	tp3 := newTestPlugin(t, bigger)
	if err := s.db.UpdatePlugin(ctx, func() *index.Plugin {
		row, _ := s.db.GetPlugin(ctx, "late-plugin")
		row.BaseURL = tp3.srv.URL
		return row
	}()); err != nil {
		t.Fatal(err)
	}
	s.call(s.adminTok, "POST", "/api/admin/plugins/late-plugin/check", nil, &view, 200)
	if view["status"] != "needs_approval" || view["pending"] == nil {
		t.Fatalf("check with more permissions: %v", view)
	}
	if status, _, _ := s.request(token, "POST", "/api/plugin/v1/notes", map[string]any{"spaceId": wiki.ID, "title": "x"}); status != 403 {
		t.Fatalf("items:write before approval: %d, want 403", status)
	}
	s.call(s.adminTok, "POST", "/api/admin/plugins/late-plugin/approve", nil, &view, 200)
	if view["status"] != "active" || view["version"] != "2.0.0" {
		t.Fatalf("approve: %v", view)
	}
	// The grant is viewer, so the write permission alone does not allow a
	// write.
	if status, _, _ := s.request(token, "POST", "/api/plugin/v1/notes", map[string]any{"spaceId": wiki.ID, "title": "x"}); status != 403 {
		t.Fatalf("write with a viewer grant after approval: %d, want 403", status)
	}
	var list struct {
		Items []map[string]any `json:"items"`
	}
	s.call(s.adminTok, "GET", "/api/admin/plugins", nil, &list, 200)
	if len(list.Items) != 1 || list.Items[0]["id"] != "late-plugin" {
		t.Fatalf("admin list %v", list.Items)
	}
	raw, _ := json.Marshal(list.Items[0])
	if bytes.Contains(raw, []byte("signingSecret")) || bytes.Contains(raw, []byte(token)) {
		t.Fatalf("the admin list leaks credentials: %s", raw)
	}
}
