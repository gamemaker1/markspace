package index

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// testLogger discards output so a failing test shows only its own messages.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestDB opens a database in a temporary file. A file is used rather than
// :memory:, because every connection of an in-memory database is a separate
// database.
func newTestDB(t testing.TB) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.db")
	db, err := Open(path, testLogger())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return db
}

// mustUser creates a user and returns it.
func mustUser(t *testing.T, db *DB, email string) *core.User {
	t.Helper()
	u := &core.User{Email: email, Name: email, IsActive: true}
	if err := db.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser(%q): %v", email, err)
	}
	return u
}

// mustSpace creates a space and returns it.
func mustSpace(t *testing.T, db *DB, slug string) *core.Space {
	t.Helper()
	s := &core.Space{Slug: slug, Name: slug}
	if err := db.CreateSpace(context.Background(), s); err != nil {
		t.Fatalf("CreateSpace(%q): %v", slug, err)
	}
	return s
}

// noteOpt customises a note before it is written.
type noteOpt func(*core.Note)

// withTags sets the tag list of a test note.
func withTags(tags ...string) noteOpt {
	return func(n *core.Note) { n.Tags = tags }
}

// withParent sets the parent id of a test note.
func withParent(id string) noteOpt {
	return func(n *core.Note) { n.ParentID = id }
}

// withUpdated sets the update time of a test note.
func withUpdated(at time.Time) noteOpt {
	return func(n *core.Note) { n.UpdatedAt = at }
}

// withPosition sets the sidebar position of a test note.
func withPosition(p int) noteOpt {
	return func(n *core.Note) { n.Position = p }
}

// mustNote upserts a note with a derived slug and path.
func mustNote(t *testing.T, db *DB, spaceID, title, body string, opts ...noteOpt) *core.Note {
	t.Helper()
	slug := core.Slugify(title)
	n := &core.Note{
		ID:        core.NewID(),
		SpaceID:   spaceID,
		Slug:      slug,
		Path:      slug + ".md",
		Title:     title,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	for _, opt := range opts {
		opt(n)
	}
	row := ports.NoteRow{Note: n, Plain: body, ModTime: n.UpdatedAt, Size: int64(len(body))}
	if err := db.UpsertNote(context.Background(), row); err != nil {
		t.Fatalf("UpsertNote(%q): %v", title, err)
	}
	return n
}

// TestTimeOutsideStorableRange covers the FuzzParseTime failures: an offset
// that moves the UTC year to -1 or 10000 produced a stored string that did
// not parse back and did not sort as text.
func TestTimeOutsideStorableRange(t *testing.T) {
	for _, s := range []string{"0000-01-01T00:00:00+00:01", "9999-12-31T23:59:59-23:59"} {
		if got := parseTime(s); !got.IsZero() {
			t.Errorf("parseTime(%q) = %v, want the zero time", s, got)
		}
	}
	late := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	early := time.Date(-1, 12, 31, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		in   time.Time
		want string
	}{
		{late, "9999-12-31T23:59:59.999999999Z"},
		{early, "0000-01-01T00:00:00.000000000Z"},
	} {
		got := fmtTime(tc.in)
		if got != tc.want {
			t.Errorf("fmtTime(%v) = %q, want %q", tc.in, got, tc.want)
		}
		if parseTime(got).IsZero() {
			t.Errorf("fmtTime(%v) = %q does not parse back", tc.in, got)
		}
	}
}

func TestOpenAppliesMigrationsAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "index.db")

	first, err := Open(path, testLogger())
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	var version, count int
	q := `SELECT COALESCE(MAX(version), 0), COUNT(*) FROM schema_migrations`
	if err := first.SQL().QueryRowContext(ctx, q).Scan(&version, &count); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if version != SchemaVersion() || count != SchemaVersion() {
		t.Fatalf("after first open: version %d, count %d, want %d", version, count, SchemaVersion())
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	second, err := Open(path, testLogger())
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer second.Close()
	if err := second.SQL().QueryRowContext(ctx, q).Scan(&version, &count); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if version != SchemaVersion() || count != SchemaVersion() {
		t.Fatalf("after second open: version %d, count %d, want %d", version, count, SchemaVersion())
	}
	if err := second.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	if _, err := Open("", testLogger()); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("Open(\"\") = %v, want core.ErrInvalid", err)
	}
}

func TestTimeRoundTrip(t *testing.T) {
	at := time.Date(2026, 9, 13, 9, 12, 13, 123456789, time.UTC)
	got := parseTime(fmtTime(at))
	if !got.Equal(at) {
		t.Fatalf("round trip gave %v, want %v", got, at)
	}
	if fmtTime(time.Time{}) != "" {
		t.Fatal("the zero time should format as the empty string")
	}
	if !parseTime("").IsZero() {
		t.Fatal("the empty string should parse as the zero time")
	}
	if !parseTime("not a time").IsZero() {
		t.Fatal("unreadable input should parse as the zero time")
	}
	if got := parseTime("2026-09-13T09:12:13Z"); !got.Equal(time.Date(2026, 9, 13, 9, 12, 13, 0, time.UTC)) {
		t.Fatalf("plain RFC 3339 parsed as %v", got)
	}
	// The fixed-width layout has to sort in the same order as the instants.
	early := fmtTime(time.Date(2026, 9, 13, 9, 12, 13, 0, time.UTC))
	late := fmtTime(time.Date(2026, 9, 13, 9, 12, 13, 500000000, time.UTC))
	if !(early < late) {
		t.Fatalf("string order is wrong: %q is not before %q", early, late)
	}
}

func TestTolerantTimeParsingOfPointers(t *testing.T) {
	if parseTimePtr("") != nil {
		t.Fatal("the empty string should parse as a nil time pointer")
	}
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	got := parseTimePtr(fmtTime(at))
	if got == nil || !got.Equal(at) {
		t.Fatalf("parseTimePtr gave %v, want %v", got, at)
	}
	if fmtTimePtr(nil) != "" {
		t.Fatal("a nil time pointer should format as the empty string")
	}
}

func TestUsersCRUDAndUniqueEmail(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	u := &core.User{Email: "Ada@example.com", Name: "Ada", IsActive: true, IsAdmin: true}
	if err := db.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u.ID == "" || u.CreatedAt.IsZero() {
		t.Fatal("CreateUser should fill the id and the creation time")
	}

	got, err := db.GetUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.Email != "Ada@example.com" || !got.IsAdmin || !got.IsActive {
		t.Fatalf("GetUser gave %+v", got)
	}

	// The email column collates case-insensitively both for lookups and for
	// the unique constraint.
	byEmail, err := db.GetUserByEmail(ctx, "ADA@EXAMPLE.COM")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if byEmail.ID != u.ID {
		t.Fatalf("GetUserByEmail gave %q, want %q", byEmail.ID, u.ID)
	}
	dup := &core.User{Email: "ada@EXAMPLE.com", Name: "Impostor"}
	if err := db.CreateUser(ctx, dup); !errors.Is(err, core.ErrExists) {
		t.Fatalf("duplicate email gave %v, want core.ErrExists", err)
	}

	u.Name = "Ada Lovelace"
	u.IsAdmin = false
	if err := db.UpdateUser(ctx, u); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	got, err = db.GetUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUser after update: %v", err)
	}
	if got.Name != "Ada Lovelace" || got.IsAdmin {
		t.Fatalf("update did not stick: %+v", got)
	}

	at := time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC)
	if err := db.TouchUserLogin(ctx, u.ID, at); err != nil {
		t.Fatalf("TouchUserLogin: %v", err)
	}
	got, _ = db.GetUser(ctx, u.ID)
	if !got.LastLoginAt.Equal(at) {
		t.Fatalf("LastLoginAt is %v, want %v", got.LastLoginAt, at)
	}

	mustUser(t, db, "grace@example.com")
	all, total, err := db.ListUsers(ctx, "", 0, 0)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if total != 2 || len(all) != 2 {
		t.Fatalf("ListUsers gave %d rows and total %d, want 2 and 2", len(all), total)
	}
	if all[0].Email != "Ada@example.com" {
		t.Fatalf("ListUsers is not ordered by email: %q first", all[0].Email)
	}
	filtered, total, err := db.ListUsers(ctx, "GRACE", 0, 0)
	if err != nil {
		t.Fatalf("ListUsers filtered: %v", err)
	}
	if total != 1 || len(filtered) != 1 || filtered[0].Email != "grace@example.com" {
		t.Fatalf("filtered listing gave %d rows, total %d", len(filtered), total)
	}
	paged, total, err := db.ListUsers(ctx, "", 1, 1)
	if err != nil {
		t.Fatalf("ListUsers paged: %v", err)
	}
	if total != 2 || len(paged) != 1 || paged[0].Email != "grace@example.com" {
		t.Fatalf("paged listing gave %d rows, total %d", len(paged), total)
	}

	if n, err := db.CountUsers(ctx); err != nil || n != 2 {
		t.Fatalf("CountUsers = %d, %v, want 2", n, err)
	}

	if err := db.DeleteUser(ctx, u.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := db.GetUser(ctx, u.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("GetUser after delete gave %v, want core.ErrNotFound", err)
	}
	if err := db.DeleteUser(ctx, u.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("second DeleteUser gave %v, want core.ErrNotFound", err)
	}
	if err := db.UpdateUser(ctx, &core.User{ID: "missing"}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("UpdateUser on a missing id gave %v, want core.ErrNotFound", err)
	}
	if _, err := db.GetUserByEmail(ctx, "nobody@example.com"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("GetUserByEmail for a missing address gave %v", err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	u := mustUser(t, db, "ada@example.com")

	live := &core.Session{
		UserID:    u.ID,
		TokenHash: "hash-live",
		UserAgent: "test",
		IP:        "127.0.0.1",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	if err := db.CreateSession(ctx, live); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if live.ID == "" || live.CreatedAt.IsZero() || live.LastSeen.IsZero() {
		t.Fatal("CreateSession should fill the id, the creation time and the last-seen time")
	}
	if err := db.CreateSession(ctx, &core.Session{UserID: u.ID, TokenHash: "hash-live"}); !errors.Is(err, core.ErrExists) {
		t.Fatalf("a repeated token hash gave %v, want core.ErrExists", err)
	}

	got, err := db.GetSessionByHash(ctx, "hash-live")
	if err != nil {
		t.Fatalf("GetSessionByHash: %v", err)
	}
	if got.ID != live.ID || got.UserID != u.ID || got.IP != "127.0.0.1" {
		t.Fatalf("GetSessionByHash gave %+v", got)
	}

	expired := &core.Session{
		UserID:    u.ID,
		TokenHash: "hash-expired",
		ExpiresAt: time.Now().UTC().Add(-time.Minute),
	}
	if err := db.CreateSession(ctx, expired); err != nil {
		t.Fatalf("CreateSession expired: %v", err)
	}
	if _, err := db.GetSessionByHash(ctx, "hash-expired"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("an expired session gave %v, want core.ErrNotFound", err)
	}

	list, err := db.ListSessions(ctx, u.ID)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListSessions gave %d rows, want 2 including the expired one", len(list))
	}

	seen := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	if err := db.TouchSession(ctx, live.ID, seen); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}
	got, _ = db.GetSessionByHash(ctx, "hash-live")
	if !got.LastSeen.Equal(seen) {
		t.Fatalf("LastSeen is %v, want %v", got.LastSeen, seen)
	}

	purged, err := db.PurgeExpiredSessions(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("PurgeExpiredSessions: %v", err)
	}
	if purged != 1 {
		t.Fatalf("PurgeExpiredSessions removed %d rows, want 1", purged)
	}
	if list, _ = db.ListSessions(ctx, u.ID); len(list) != 1 {
		t.Fatalf("after the purge %d sessions remain, want 1", len(list))
	}

	if err := db.DeleteSessionByHash(ctx, "hash-live"); err != nil {
		t.Fatalf("DeleteSessionByHash: %v", err)
	}
	if _, err := db.GetSessionByHash(ctx, "hash-live"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("a deleted session gave %v, want core.ErrNotFound", err)
	}
	if err := db.DeleteSession(ctx, "missing"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("deleting a missing session gave %v, want core.ErrNotFound", err)
	}

	// Deleting the user cascades to the sessions.
	if err := db.CreateSession(ctx, &core.Session{UserID: u.ID, TokenHash: "hash-cascade",
		ExpiresAt: time.Now().UTC().Add(time.Hour)}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := db.DeleteUser(ctx, u.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if list, _ = db.ListSessions(ctx, u.ID); len(list) != 0 {
		t.Fatalf("%d sessions survived the user, want 0", len(list))
	}
}

func TestAPITokensAndInvites(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	u := mustUser(t, db, "ada@example.com")
	other := mustUser(t, db, "grace@example.com")

	tok := &core.APIToken{UserID: u.ID, Name: "laptop", TokenHash: "digest", Prefix: "msk_abcdefgh"}
	if err := db.CreateAPIToken(ctx, tok); err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}
	got, err := db.GetAPITokenByHash(ctx, "digest")
	if err != nil {
		t.Fatalf("GetAPITokenByHash: %v", err)
	}
	if got.Prefix != "msk_abcdefgh" || got.ExpiresAt != nil || got.LastUsed != nil {
		t.Fatalf("GetAPITokenByHash gave %+v", got)
	}

	past := time.Now().UTC().Add(-time.Hour)
	dead := &core.APIToken{UserID: u.ID, Name: "old", TokenHash: "dead", ExpiresAt: &past}
	if err := db.CreateAPIToken(ctx, dead); err != nil {
		t.Fatalf("CreateAPIToken expired: %v", err)
	}
	// The index returns expired tokens. internal/app rejects them against its
	// own clock.
	if got, err := db.GetAPITokenByHash(ctx, "dead"); err != nil || got.ExpiresAt == nil || !got.ExpiresAt.Equal(past) {
		t.Fatalf("expired token = %+v, %v, want the row with its expiry", got, err)
	}

	used := time.Date(2026, 9, 13, 11, 0, 0, 0, time.UTC)
	if err := db.TouchAPIToken(ctx, tok.ID, used); err != nil {
		t.Fatalf("TouchAPIToken: %v", err)
	}
	got, _ = db.GetAPITokenByHash(ctx, "digest")
	if got.LastUsed == nil || !got.LastUsed.Equal(used) {
		t.Fatalf("LastUsed is %v, want %v", got.LastUsed, used)
	}

	if list, err := db.ListAPITokens(ctx, u.ID); err != nil || len(list) != 2 {
		t.Fatalf("ListAPITokens gave %d rows, %v, want 2", len(list), err)
	}
	if err := db.DeleteAPIToken(ctx, tok.ID, other.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("deleting another user's token gave %v, want core.ErrNotFound", err)
	}
	if err := db.DeleteAPIToken(ctx, tok.ID, u.ID); err != nil {
		t.Fatalf("DeleteAPIToken: %v", err)
	}

	inv := &core.Invite{
		Email:     "new@example.com",
		Token:     "invite-token",
		Role:      core.RoleEditor,
		CreatedBy: u.ID,
		ExpiresAt: time.Now().UTC().Add(48 * time.Hour),
	}
	if err := db.CreateInvite(ctx, inv); err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	gotInv, err := db.GetInviteByToken(ctx, "invite-token")
	if err != nil {
		t.Fatalf("GetInviteByToken: %v", err)
	}
	if gotInv.Role != core.RoleEditor || gotInv.UsedAt != nil {
		t.Fatalf("GetInviteByToken gave %+v", gotInv)
	}
	if err := db.CreateInvite(ctx, &core.Invite{Email: "x@example.com", Token: "invite-token"}); !errors.Is(err, core.ErrExists) {
		t.Fatalf("a repeated invite token gave %v, want core.ErrExists", err)
	}

	at := time.Now().UTC()
	if err := db.MarkInviteUsed(ctx, inv.ID, at); err != nil {
		t.Fatalf("MarkInviteUsed: %v", err)
	}
	gotInv, _ = db.GetInviteByToken(ctx, "invite-token")
	if gotInv.UsedAt == nil {
		t.Fatal("UsedAt is still nil after MarkInviteUsed")
	}
	if list, err := db.ListInvites(ctx); err != nil || len(list) != 1 {
		t.Fatalf("ListInvites gave %d rows, %v, want 1", len(list), err)
	}
	if err := db.DeleteInvite(ctx, inv.ID); err != nil {
		t.Fatalf("DeleteInvite: %v", err)
	}
	if _, err := db.GetInviteByToken(ctx, "invite-token"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("a deleted invite gave %v, want core.ErrNotFound", err)
	}
}

func TestSpacesAndMembership(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	ada := mustUser(t, db, "ada@example.com")
	grace := mustUser(t, db, "grace@example.com")

	docs := mustSpace(t, db, "docs")
	docs.Name = "Docs"
	if err := db.UpdateSpace(ctx, docs); err != nil {
		t.Fatalf("UpdateSpace: %v", err)
	}
	notebook := mustSpace(t, db, "notebook")
	notebook.Name = "Notebook"
	if err := db.UpdateSpace(ctx, notebook); err != nil {
		t.Fatalf("UpdateSpace: %v", err)
	}
	if err := db.CreateSpace(ctx, &core.Space{Slug: "docs", Name: "Clash"}); !errors.Is(err, core.ErrExists) {
		t.Fatalf("a repeated slug gave %v, want core.ErrExists", err)
	}

	if got, err := db.GetSpaceBySlug(ctx, "docs"); err != nil || got.ID != docs.ID {
		t.Fatalf("GetSpaceBySlug gave %v, %v", got, err)
	}
	if _, err := db.GetSpace(ctx, "missing"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("GetSpace for a missing id gave %v", err)
	}
	if all, err := db.ListSpaces(ctx); err != nil || len(all) != 2 {
		t.Fatalf("ListSpaces gave %d rows, %v, want 2", len(all), err)
	}
	if n, err := db.CountSpaces(ctx); err != nil || n != 2 {
		t.Fatalf("CountSpaces = %d, %v, want 2", n, err)
	}

	if err := db.AddMember(ctx, &core.SpaceMember{SpaceID: docs.ID, UserID: ada.ID, Role: core.RoleOwner}); err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	if err := db.AddMember(ctx, &core.SpaceMember{SpaceID: docs.ID, UserID: grace.ID, Role: core.RoleViewer}); err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	if err := db.AddMember(ctx, &core.SpaceMember{SpaceID: notebook.ID, UserID: ada.ID, Role: core.RoleEditor}); err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	if err := db.AddMember(ctx, &core.SpaceMember{SpaceID: docs.ID, UserID: ada.ID, Role: core.RoleAdmin}); !errors.Is(err, core.ErrExists) {
		t.Fatalf("a repeated membership gave %v, want core.ErrExists", err)
	}

	mustNote(t, db, docs.ID, "One", "body one")
	mustNote(t, db, docs.ID, "Two", "body two")
	trashed := mustNote(t, db, docs.ID, "Three", "body three")
	now := time.Now().UTC()
	if err := db.SetNoteDeleted(ctx, trashed.ID, true, &now, ada.ID); err != nil {
		t.Fatalf("SetNoteDeleted: %v", err)
	}

	forAda, err := db.ListSpacesForUser(ctx, ada.ID)
	if err != nil {
		t.Fatalf("ListSpacesForUser: %v", err)
	}
	if len(forAda) != 2 {
		t.Fatalf("ListSpacesForUser gave %d spaces, want 2", len(forAda))
	}
	if forAda[0].Slug != "docs" || forAda[0].MyRole != core.RoleOwner || forAda[0].NoteCount != 2 {
		t.Fatalf("docs row is %+v, want role owner and 2 live notes", forAda[0])
	}
	if forAda[1].Slug != "notebook" || forAda[1].MyRole != core.RoleEditor || forAda[1].NoteCount != 0 {
		t.Fatalf("notebook row is %+v, want role editor and 0 notes", forAda[1])
	}

	forGrace, err := db.ListSpacesForUser(ctx, grace.ID)
	if err != nil {
		t.Fatalf("ListSpacesForUser: %v", err)
	}
	if len(forGrace) != 1 || forGrace[0].MyRole != core.RoleViewer {
		t.Fatalf("Grace sees %d spaces with role %v", len(forGrace), forGrace)
	}

	if err := db.UpdateMember(ctx, docs.ID, grace.ID, core.RoleCommenter); err != nil {
		t.Fatalf("UpdateMember: %v", err)
	}
	m, err := db.GetMember(ctx, docs.ID, grace.ID)
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if m.Role != core.RoleCommenter {
		t.Fatalf("role is %q, want commenter", m.Role)
	}

	members, err := db.ListMembers(ctx, docs.ID)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("ListMembers gave %d rows, want 2", len(members))
	}
	for _, mem := range members {
		if mem.User == nil || mem.User.ID != mem.UserID {
			t.Fatalf("member %+v has no user attached", mem)
		}
	}

	memberships, err := db.ListMembershipsForUser(ctx, ada.ID)
	if err != nil {
		t.Fatalf("ListMembershipsForUser: %v", err)
	}
	if len(memberships) != 2 {
		t.Fatalf("ListMembershipsForUser gave %d rows, want 2", len(memberships))
	}

	if err := db.RemoveMember(ctx, docs.ID, grace.ID); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if _, err := db.GetMember(ctx, docs.ID, grace.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("GetMember after removal gave %v, want core.ErrNotFound", err)
	}
	if err := db.UpdateMember(ctx, docs.ID, grace.ID, core.RoleViewer); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("UpdateMember on a missing row gave %v, want core.ErrNotFound", err)
	}

	if err := db.DeleteSpace(ctx, docs.ID); err != nil {
		t.Fatalf("DeleteSpace: %v", err)
	}
	if _, err := db.GetSpace(ctx, docs.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("GetSpace after delete gave %v", err)
	}
	if notes, err := db.ListNotes(ctx, docs.ID, core.NoteListOptions{IncludeTrash: true}); err != nil || len(notes) != 0 {
		t.Fatalf("%d notes survived the space, %v", len(notes), err)
	}
	if left, _ := db.ListMembershipsForUser(ctx, ada.ID); len(left) != 1 {
		t.Fatalf("%d memberships remain, want 1", len(left))
	}
}
