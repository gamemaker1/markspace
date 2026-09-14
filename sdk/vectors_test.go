package sdk

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/sdk/ui"
)

type vecErr struct {
	Code string `json:"code"`
	Path string `json:"path"`
}

type vectors struct {
	Signature struct {
		WindowSeconds int `json:"windowSeconds"`
		Sign          []struct {
			Name, SecretBase64, BodyBase64, Header, Method, Path, DeliveryID string
			Timestamp                                                        int64
		} `json:"sign"`
		Verify []struct {
			Name, SecretBase64, BodyBase64, Header, Method, Path, DeliveryID string
			Now                                                              int64
			Error                                                            *string
		} `json:"verify"`
		ReplaySequences []struct {
			Name         string
			SecretBase64 string
			Requests     []struct {
				Note, DeliveryID, BodyBase64, Header, Method, Path string
				Now                                                int64
				Error                                              *string
			}
		} `json:"replaySequences"`
	} `json:"signature"`
	IDs struct {
		Cases []struct {
			Rule, Value string
			Valid       bool
		} `json:"cases"`
		EventMatches []struct {
			Pattern, Type string
			Match         bool
		} `json:"eventMatches"`
	} `json:"ids"`
	CommandResponses struct {
		Cases []struct {
			Name     string
			Response json.RawMessage
			Error    *vecErr
		} `json:"cases"`
	} `json:"commandResponses"`
	Install struct {
		Cases []struct {
			Name       string
			ManifestID string `json:"manifestId"`
			Body       json.RawMessage
			Error      *vecErr
		} `json:"cases"`
	} `json:"install"`
}

func loadVectors(t testing.TB) *vectors {
	t.Helper()
	raw, err := os.ReadFile("../test/vectors/protocol.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return &v
}

func b64(t testing.TB, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func sigCode(err error) string {
	if err == nil {
		return ""
	}
	var se *SignatureError
	if errors.As(err, &se) {
		return se.Code
	}
	return "other: " + err.Error()
}

func want(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func TestSignatureVectors(t *testing.T) {
	v := loadVectors(t)
	if v.Signature.WindowSeconds != int(DefaultWindow/time.Second) {
		t.Fatalf("window %d", v.Signature.WindowSeconds)
	}
	if len(v.Signature.Sign) == 0 || len(v.Signature.Verify) == 0 || len(v.Signature.ReplaySequences) == 0 {
		t.Fatal("signature vectors missing")
	}
	for _, c := range v.Signature.Sign {
		got := Sign(b64(t, c.SecretBase64), time.Unix(c.Timestamp, 0), c.Method, c.Path, c.DeliveryID, b64(t, c.BodyBase64))
		if got != c.Header {
			t.Errorf("sign %s: got %s want %s", c.Name, got, c.Header)
		}
		if err := Verify(b64(t, c.SecretBase64), got, c.Method, c.Path, c.DeliveryID, b64(t, c.BodyBase64), time.Unix(c.Timestamp, 0), DefaultWindow); err != nil {
			t.Errorf("sign %s: verify own header: %v", c.Name, err)
		}
	}
	for _, c := range v.Signature.Verify {
		err := Verify(b64(t, c.SecretBase64), c.Header, c.Method, c.Path, c.DeliveryID, b64(t, c.BodyBase64), time.Unix(c.Now, 0), DefaultWindow)
		if got := sigCode(err); got != want(c.Error) {
			t.Errorf("verify %s: got %q want %q", c.Name, got, want(c.Error))
		}
	}
	for _, seq := range v.Signature.ReplaySequences {
		g := NewReplayGuard(DefaultReplayCapacity, DefaultWindow)
		secret := b64(t, seq.SecretBase64)
		for i, r := range seq.Requests {
			now := time.Unix(r.Now, 0)
			err := Verify(secret, r.Header, r.Method, r.Path, r.DeliveryID, b64(t, r.BodyBase64), now, DefaultWindow)
			if err == nil {
				err = g.Check(r.DeliveryID, r.Header, now)
			}
			if got := sigCode(err); got != want(r.Error) {
				t.Errorf("%s step %d (%s): got %q want %q", seq.Name, i, r.Note, got, want(r.Error))
			}
		}
	}
}

func TestIDVectors(t *testing.T) {
	v := loadVectors(t)
	rules := map[string]func(string) bool{
		"pluginId": ValidPluginID, "localId": ValidLocalID, "qualifiedId": ValidQualifiedID,
		"objectId": ValidObjectID, "secretName": ValidSecretName, "storageKey": ValidStorageKey,
		"eventPattern": ValidEventPattern,
	}
	for _, c := range v.IDs.Cases {
		f, ok := rules[c.Rule]
		if !ok {
			t.Fatalf("no validator for rule %s", c.Rule)
		}
		if f(c.Value) != c.Valid {
			t.Errorf("%s %q: want valid=%v", c.Rule, c.Value, c.Valid)
		}
	}
	for _, c := range v.IDs.EventMatches {
		if MatchEvent(c.Pattern, c.Type) != c.Match {
			t.Errorf("MatchEvent(%q, %q) want %v", c.Pattern, c.Type, c.Match)
		}
	}
}

func checkVecErr(t *testing.T, name string, err error, w *vecErr) {
	t.Helper()
	if w == nil {
		if err != nil {
			t.Errorf("%s: want valid, got %v", name, err)
		}
		return
	}
	var ve *ui.Error
	if !errors.As(err, &ve) || ve.Code != w.Code || ve.Path != w.Path {
		t.Errorf("%s: want %s at %s, got %v", name, w.Code, w.Path, err)
	}
}

func TestCommandResponseVectors(t *testing.T) {
	v := loadVectors(t)
	if len(v.CommandResponses.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range v.CommandResponses.Cases {
		var resp any
		if err := json.Unmarshal(c.Response, &resp); err != nil {
			t.Fatal(err)
		}
		checkVecErr(t, c.Name, ValidateCommandResponse(resp), c.Error)
	}
}

func TestInstallVectors(t *testing.T) {
	v := loadVectors(t)
	if len(v.Install.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range v.Install.Cases {
		var body any
		if err := json.Unmarshal(c.Body, &body); err != nil {
			t.Fatal(err)
		}
		checkVecErr(t, c.Name, ValidateInstall(body, c.ManifestID), c.Error)
	}
}

func TestResponseHelpers(t *testing.T) {
	for _, r := range []Response{
		Toast("412 words", ""), Toast("x", "success"), OpenPanel("word-count.counts", "n1"),
		Navigate("/s/docs"), JobResponse("j1"), UIResponse(ui.Stack(ui.Text("x"))),
	} {
		if err := ValidateCommandResponse(r); err != nil {
			t.Errorf("%v: %v", r, err)
		}
	}
	if ValidateCommandResponse(Response(nil)) == nil {
		t.Error("nil response accepted")
	}
}

func TestManifestValidate(t *testing.T) {
	m := testManifest()
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(m)
	if _, err := ParseManifest(b); err != nil {
		t.Fatal(err)
	}
	bad := []func(*Manifest){
		func(m *Manifest) { m.Protocol = 2 },
		func(m *Manifest) { m.ID = "Word" },
		func(m *Manifest) { m.Version = "" },
		func(m *Manifest) { m.Permissions = append(m.Permissions, "root") },
		func(m *Manifest) { m.Commands = append(m.Commands, m.Commands[0]) },
		func(m *Manifest) { m.Panels[0].Where = "modal" },
		func(m *Manifest) { m.Schedules = []ManifestSchedule{{Job: "nope", Every: "1h"}} },
		func(m *Manifest) { m.Schedules = []ManifestSchedule{{Job: "count-space", Every: "daily"}} },
		func(m *Manifest) { m.Secrets = []ManifestSecret{{Name: "Bad Name"}} },
		func(m *Manifest) { m.Events = []string{"Note?"} },
	}
	for i, f := range bad {
		m := testManifest()
		f(&m)
		if m.Validate() == nil {
			t.Errorf("bad manifest %d accepted", i)
		}
	}
}

func testManifest() Manifest {
	return Manifest{
		Protocol: 1, ID: "word-count", Name: "Word count", Version: "0.1.0",
		Permissions: []string{"items:read", "jobs", "storage", "ui", "events"},
		Events:      []string{"note.updated"},
		Jobs:        []ManifestJob{{Kind: "count-space", Title: "Count words"}, {Kind: "slow", Title: "Slow"}},
		Schedules:   []ManifestSchedule{{Job: "count-space", Every: "24h"}},
		Commands:    []ManifestCommand{{ID: "count", Title: "Count words", Contexts: []string{"note"}}, {ID: "bad", Title: "Bad", Contexts: []string{"note"}}},
		Panels:      []ManifestPanel{{ID: "counts", Title: "Word count", Icon: "hash", Where: "rail", Contexts: []string{"note"}}},
		Importers:   []ManifestImporter{{ID: "text", Title: "Plain text", Accept: []string{".txt"}}},
		Secrets:     []ManifestSecret{{Name: "api_key", Label: "Key"}},
	}
}
