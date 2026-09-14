package control

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// fakeIssuer keeps one token in memory the way app.EnsureSystemToken keeps
// it in the database.
type fakeIssuer struct {
	stored string
	issued int
	err    error
}

func (f *fakeIssuer) EnsureSystemToken(_ context.Context, name, current string) (string, *core.APIToken, error) {
	if f.err != nil {
		return "", nil, f.err
	}
	if name != TokenName {
		return "", nil, fmt.Errorf("name %q, want %q", name, TokenName)
	}
	if current != "" && current == f.stored {
		return "", &core.APIToken{ID: "t", Name: name}, nil
	}
	f.issued++
	f.stored = fmt.Sprintf("msk_token%d", f.issued)
	return f.stored, &core.APIToken{ID: fmt.Sprintf("t%d", f.issued), Name: name, Prefix: "msk_token"}, nil
}

func readFile(t *testing.T, path string) (string, os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := ReadToken(path)
	if err != nil {
		t.Fatal(err)
	}
	return tok, info.Mode().Perm()
}

func TestEnsureTokenCreatesReusesAndRotates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, TokenFile)
	iss := &fakeIssuer{}
	ctx := t.Context()

	if err := EnsureToken(ctx, iss, path, nil); err != nil {
		t.Fatal(err)
	}
	tok, mode := readFile(t, path)
	if tok != "msk_token1" || mode != 0o600 {
		t.Fatalf("first start: token %q mode %o, want msk_token1 and 600", tok, mode)
	}

	// A restart with the same stored token keeps the file.
	if err := EnsureToken(ctx, iss, path, nil); err != nil {
		t.Fatal(err)
	}
	if tok, _ := readFile(t, path); tok != "msk_token1" || iss.issued != 1 {
		t.Fatalf("restart: token %q issued %d, want msk_token1 and 1", tok, iss.issued)
	}

	// A revoked token is replaced.
	iss.stored = ""
	if err := EnsureToken(ctx, iss, path, nil); err != nil {
		t.Fatal(err)
	}
	if tok, mode := readFile(t, path); tok != "msk_token2" || mode != 0o600 {
		t.Fatalf("after revoke: token %q mode %o, want msk_token2 and 600", tok, mode)
	}

	// A missing file is replaced too.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := EnsureToken(ctx, iss, path, nil); err != nil {
		t.Fatal(err)
	}
	if tok, _ := readFile(t, path); tok != "msk_token3" {
		t.Fatalf("after removal: token %q, want msk_token3", tok)
	}

	// A file others can read is narrowed.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureToken(ctx, iss, path, nil); err != nil {
		t.Fatal(err)
	}
	if _, mode := readFile(t, path); mode != 0o600 {
		t.Fatalf("mode %o after a widened file, want 600", mode)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("the directory holds %d entries, want only the token file", len(entries))
	}
}

func TestEnsureTokenIssuerError(t *testing.T) {
	path := filepath.Join(t.TempDir(), TokenFile)
	iss := &fakeIssuer{err: errors.New("database is locked")}
	if err := EnsureToken(t.Context(), iss, path, nil); err == nil {
		t.Fatal("EnsureToken succeeded with a failing issuer")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a failed issue left a token file: %v", err)
	}
}
