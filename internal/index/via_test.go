package index

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// TestNoteViaRoundTrip stores the plugin behind a note write and clears it
// with a later write that has none.
func TestNoteViaRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	space := mustSpace(t, db, "docs")
	n := mustNote(t, db, space.ID, "Wiki page", "body")
	if got, err := db.GetNote(ctx, n.ID); err != nil || got.UpdatedVia != "" {
		t.Fatalf("new note via = %q, %v", got.UpdatedVia, err)
	}

	n.UpdatedBy, n.UpdatedVia = "0person", "bookmark-wiki"
	if err := db.UpsertNote(ctx, ports.NoteRow{Note: n, Plain: "body", ModTime: time.Now()}); err != nil {
		t.Fatalf("UpsertNote: %v", err)
	}
	got, err := db.GetNote(ctx, n.ID)
	if err != nil || got.UpdatedBy != "0person" || got.UpdatedVia != "bookmark-wiki" {
		t.Fatalf("after acting write = %+v, %v", got, err)
	}
	listed, err := db.ListNotes(ctx, space.ID, core.NoteListOptions{})
	if err != nil || len(listed) != 1 || listed[0].UpdatedVia != "bookmark-wiki" {
		t.Fatalf("ListNotes = %+v, %v", listed, err)
	}

	n.UpdatedVia = ""
	if err := db.UpsertNote(ctx, ports.NoteRow{Note: n, Plain: "body", ModTime: time.Now()}); err != nil {
		t.Fatalf("UpsertNote: %v", err)
	}
	if got, err := db.GetNote(ctx, n.ID); err != nil || got.UpdatedVia != "" {
		t.Fatalf("after plain write via = %q, %v", got.UpdatedVia, err)
	}
}

// TestMigration9AddsVia upgrades a version 8 database and checks the via
// column of notes and its default.
func TestMigration9AddsVia(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v8.db")
	sdb, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	sdb.SetMaxOpenConns(1)
	old := &DB{sql: sdb, logger: testLogger()}
	if _, err := sdb.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	for i, script := range migrations[:8] {
		if err := old.applyMigration(ctx, i+1, script); err != nil {
			t.Fatalf("apply v%d: %v", i+1, err)
		}
	}
	if err := sdb.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(path, testLogger())
	if err != nil {
		t.Fatalf("open v8 database: %v", err)
	}
	defer db.Close()
	var version int
	if err := db.SQL().QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != SchemaVersion() || SchemaVersion() < 9 {
		t.Fatalf("version %d (%v), schema %d, want the schema version and at least 9", version, err, SchemaVersion())
	}
	var (
		found   bool
		notNull int
		def     sql.NullString
	)
	rows, err := db.SQL().Query(`SELECT name, "notnull", dflt_value FROM pragma_table_info('notes')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var nn int
		var d sql.NullString
		if err := rows.Scan(&name, &nn, &d); err != nil {
			t.Fatal(err)
		}
		if name == "via" {
			found, notNull, def = true, nn, d
		}
	}
	if !found || notNull != 1 || def.String != "''" {
		t.Fatalf("notes.via found=%v notnull=%d default=%q", found, notNull, def.String)
	}
}
