package index

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// TestMigration11SystemTokens upgrades a version 10 database with a user
// token, then checks that the token survives, that a token without a user
// can be stored, and that the system and user token queries keep apart.
func TestMigration11SystemTokens(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v10.db")
	sdb, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	sdb.SetMaxOpenConns(1)
	old := &DB{sql: sdb, logger: testLogger()}
	if _, err := sdb.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	for i, script := range migrations[:10] {
		if err := old.applyMigration(ctx, i+1, script); err != nil {
			t.Fatalf("apply v%d: %v", i+1, err)
		}
	}
	if _, err := sdb.Exec(`INSERT INTO users (id, email, name) VALUES ('u1', 'ada@example.com', 'Ada')`); err != nil {
		t.Fatal(err)
	}
	if _, err := sdb.Exec(`INSERT INTO api_tokens (id, user_id, name, token_hash, prefix, created_at) VALUES ('t-user', 'u1', 'laptop', 'hash-user', 'msk_user', '2026-09-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := sdb.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(path, testLogger())
	if err != nil {
		t.Fatalf("open v10 database: %v", err)
	}
	defer db.Close()

	mine, err := db.ListAPITokens(ctx, "u1")
	if err != nil || len(mine) != 1 || mine[0].ID != "t-user" || mine[0].Name != "laptop" {
		t.Fatalf("user tokens after the migration = %+v, %v", mine, err)
	}

	sys := &core.APIToken{ID: "t-sys", Name: "Markspace CLI", TokenHash: "hash-sys", Prefix: "msk_sys", CreatedAt: time.Now().UTC()}
	if err := db.CreateAPIToken(ctx, sys); err != nil {
		t.Fatalf("create system token: %v", err)
	}
	got, err := db.GetAPITokenByHash(ctx, "hash-sys")
	if err != nil || !got.IsSystem() || got.Name != "Markspace CLI" {
		t.Fatalf("system token by hash = %+v, %v", got, err)
	}
	system, err := db.ListSystemAPITokens(ctx)
	if err != nil || len(system) != 1 || system[0].ID != "t-sys" {
		t.Fatalf("system tokens = %+v, %v", system, err)
	}
	if mine, _ := db.ListAPITokens(ctx, ""); len(mine) != 0 {
		t.Fatalf("ListAPITokens with an empty user returned %d tokens", len(mine))
	}

	// The system and user deletes do not reach each other's tokens.
	if err := db.DeleteSystemAPIToken(ctx, "t-user"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("system delete of a user token: %v, want ErrNotFound", err)
	}
	if err := db.DeleteAPIToken(ctx, "t-sys", ""); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("user delete of a system token: %v, want ErrNotFound", err)
	}
	if err := db.DeleteSystemAPIToken(ctx, "t-sys"); err != nil {
		t.Fatalf("delete system token: %v", err)
	}

	// Deleting the user still removes its tokens.
	if err := db.DeleteUser(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetAPITokenByHash(ctx, "hash-user"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("user token after the user was deleted: %v, want ErrNotFound", err)
	}
}
