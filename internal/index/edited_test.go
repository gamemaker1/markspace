package index

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// TestCommentEditedAtRoundTrip checks that EditedAt is stored by create and
// update, read back, and left alone by an update that does not set it.
func TestCommentEditedAtRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	c := &core.Comment{NoteID: core.NewID(), SpaceID: core.NewID(), UserID: core.NewID(), Body: "first"}
	if err := db.CreateComment(ctx, c); err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	got, err := db.GetComment(ctx, c.ID)
	if err != nil || got.EditedAt != nil {
		t.Fatalf("new comment EditedAt = %v, %v", got.EditedAt, err)
	}
	edited := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	got.Body, got.EditedAt = "second", &edited
	if err := db.UpdateComment(ctx, got); err != nil {
		t.Fatalf("UpdateComment: %v", err)
	}
	got, _ = db.GetComment(ctx, c.ID)
	if got.EditedAt == nil || !got.EditedAt.Equal(edited) {
		t.Fatalf("EditedAt after edit = %v, want %v", got.EditedAt, edited)
	}
	got.Resolved = true
	if err := db.UpdateComment(ctx, got); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	got, _ = db.GetComment(ctx, c.ID)
	if got.EditedAt == nil || !got.EditedAt.Equal(edited) || !got.Resolved {
		t.Fatalf("after resolve EditedAt = %v resolved %v", got.EditedAt, got.Resolved)
	}
}

// TestMigration10AddsCommentEditedAt upgrades a version 9 database and checks
// that an existing comment reads as never edited.
func TestMigration10AddsCommentEditedAt(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v9.db")
	sdb, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	sdb.SetMaxOpenConns(1)
	old := &DB{sql: sdb, logger: testLogger()}
	if _, err := sdb.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	for i, script := range migrations[:9] {
		if err := old.applyMigration(ctx, i+1, script); err != nil {
			t.Fatalf("apply v%d: %v", i+1, err)
		}
	}
	if _, err := sdb.Exec(`INSERT INTO comments (id, note_id, body, created_at, updated_at) VALUES ('c-old', 'n-old', 'old', '2026-09-01T00:00:00Z', '2026-09-02T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := sdb.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path, testLogger())
	if err != nil {
		t.Fatalf("open v9 database: %v", err)
	}
	defer db.Close()
	var version int
	if err := db.SQL().QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != SchemaVersion() {
		t.Fatalf("version %d (%v), want %d", version, err, SchemaVersion())
	}
	var notNull int
	var def sql.NullString
	if err := db.SQL().QueryRow(`SELECT "notnull", dflt_value FROM pragma_table_info('comments') WHERE name = 'edited_at'`).Scan(&notNull, &def); err != nil {
		t.Fatalf("edited_at column: %v", err)
	}
	if notNull != 1 || def.String != "''" {
		t.Fatalf("edited_at notnull %d default %q", notNull, def.String)
	}
	c, err := db.GetComment(ctx, "c-old")
	if err != nil || c.EditedAt != nil || c.Body != "old" {
		t.Fatalf("old comment %+v, %v", c, err)
	}
}
