// Package index implements ports.DB over SQLite with modernc.org/sqlite, the
// pure Go driver, so the binary needs no cgo. Markdown files stay the source
// of truth for note bodies. This package holds the derived index and the
// state that has no file representation.
//
// Concurrency: one *sql.DB with MaxOpenConns(1) serialises every statement in
// the process and removes any chance of SQLITE_BUSY between goroutines. The
// busy_timeout pragma covers a second process touching the same file. A note
// index is small and every query here is indexed, so a single connection is
// the simplest correct choice. Splitting reads onto a second pool is possible
// later without changing this API.
//
// Times are stored as UTC RFC 3339 strings with exactly nine fractional
// digits, for example 2026-09-13T09:00:00.000000000Z. The fixed width makes
// the strings sort and compare in SQL in the same order as the instants they
// name, which a variable-width time.RFC3339Nano string does not. The values
// parse with the ordinary RFC 3339 parser. The zero time is the empty string.
package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	// Registers the "sqlite" driver.
	_ "modernc.org/sqlite"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// timeLayout is the fixed-width UTC RFC 3339 layout used for every stored
// timestamp.
const timeLayout = "2006-01-02T15:04:05.000000000Z"

// DB is the SQLite index database.
type DB struct {
	sql    *sql.DB
	logger *slog.Logger
}

var _ ports.DB = (*DB)(nil)

// Open opens or creates the index database at dbPath and applies every
// pending migration. The caller owns the returned DB and must Close it.
func Open(dbPath string, logger *slog.Logger) (*DB, error) {
	if dbPath == "" {
		return nil, fmt.Errorf("index: open: empty database path: %w", core.ErrInvalid)
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	// The path is part of a file: URI, so a "%", "?" or "#" in it must not
	// start an escape, the query or a fragment.
	dsn := "file:" + strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23").Replace(dbPath) +
		"?_pragma=busy_timeout(10000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=synchronous(NORMAL)"

	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("index: open %s: %w", dbPath, err)
	}
	sdb.SetMaxOpenConns(1)
	sdb.SetMaxIdleConns(1)
	sdb.SetConnMaxLifetime(0)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := sdb.PingContext(ctx); err != nil {
		sdb.Close()
		return nil, fmt.Errorf("index: ping %s: %w", dbPath, err)
	}

	d := &DB{sql: sdb, logger: logger}
	if err := d.migrate(ctx); err != nil {
		sdb.Close()
		return nil, err
	}
	return d, nil
}

// Close releases the database handle.
func (d *DB) Close() error {
	if err := d.sql.Close(); err != nil {
		return fmt.Errorf("index: close: %w", err)
	}
	return nil
}

// Ping checks that the database answers.
func (d *DB) Ping(ctx context.Context) error {
	if err := d.sql.PingContext(ctx); err != nil {
		return fmt.Errorf("index: ping: %w", err)
	}
	return nil
}

// SQL exposes the underlying handle for maintenance tasks such as VACUUM. It
// is not part of ports.DB.
func (d *DB) SQL() *sql.DB { return d.sql }

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// tx runs fn inside a transaction, rolling back on any error.
func (d *DB) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	t, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("index: begin: %w", err)
	}
	if err := fn(t); err != nil {
		if rbErr := t.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			d.logger.Warn("index: rollback failed", "error", rbErr)
		}
		return err
	}
	if err := t.Commit(); err != nil {
		return fmt.Errorf("index: commit: %w", err)
	}
	return nil
}

// Error mapping.

// readErr wraps a failed read. sql.ErrNoRows becomes core.ErrNotFound.
func readErr(what string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("index: %s: %w", what, core.ErrNotFound)
	}
	return fmt.Errorf("index: %s: %w", what, err)
}

// writeErr wraps a failed write. A constraint violation becomes
// core.ErrExists.
func writeErr(what string, err error) error {
	if isConstraintViolation(err) {
		return fmt.Errorf("index: %s: %w", what, core.ErrExists)
	}
	return fmt.Errorf("index: %s: %w", what, err)
}

// notFound builds a core.ErrNotFound for an update or delete that matched no
// row.
func notFound(what string) error {
	return fmt.Errorf("index: %s: %w", what, core.ErrNotFound)
}

// isConstraintViolation reports whether err is a SQLite constraint failure.
// The modernc driver reports every kind through one message, for example
// "constraint failed: UNIQUE constraint failed: users.email (2067)", so this
// matches on the text.
func isConstraintViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "constraint failed")
}

// affected turns a result with no matching row into core.ErrNotFound.
func affected(what string, res sql.Result, err error) error {
	if err != nil {
		return writeErr(what, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("index: %s: rows affected: %w", what, err)
	}
	if n == 0 {
		return notFound(what)
	}
	return nil
}

// Time helpers.

// fmtTime renders a time as a fixed-width UTC RFC 3339 string. The zero time
// becomes the empty string.
func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return clampStoredTime(t.UTC()).Format(timeLayout)
}

// The storable time range. Outside it the RFC 3339 year has more or fewer
// than four digits, so the text neither parses back nor sorts correctly.
var (
	minStoredTime = time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)
	maxStoredTime = time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC)
)

// clampStoredTime moves a UTC time into the storable range.
func clampStoredTime(t time.Time) time.Time {
	switch {
	case t.Before(minStoredTime):
		return minStoredTime
	case t.After(maxStoredTime):
		return maxStoredTime
	}
	return t
}

// fmtTimePtr renders an optional time. A nil or zero time becomes the empty
// string.
func fmtTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return fmtTime(*t)
}

// timeFormats lists the layouts parseTime accepts, most likely first. The
// extra entries tolerate rows written by another tool or an older build.
var timeFormats = []string{
	timeLayout,
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// parseTime reads a stored timestamp. An empty or unreadable value is the
// zero time.
func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range timeFormats {
		if t, err := time.Parse(layout, s); err == nil {
			u := t.UTC()
			if u.Before(minStoredTime) || u.After(maxStoredTime) {
				// A zone offset pushed the year out of four digits, so
				// fmtTime could not write the value back.
				return time.Time{}
			}
			return u
		}
	}
	return time.Time{}
}

// parseTimePtr reads an optional timestamp. An empty or unreadable value is
// nil.
func parseTimePtr(s string) *time.Time {
	t := parseTime(s)
	if t.IsZero() {
		return nil
	}
	return &t
}

// Scalar helpers.

// boolInt converts a bool to the 0 or 1 stored in SQLite.
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// intBool converts a stored 0 or 1 back to a bool.
func intBool(n int) bool { return n != 0 }

// marshalStrings renders a string slice as a JSON array. A nil or empty slice
// becomes "[]".
func marshalStrings(v []string) string {
	if len(v) == 0 {
		return "[]"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// unmarshalStrings reads a JSON array of strings. Anything unreadable yields
// nil rather than an error, so one bad row cannot break a listing.
func unmarshalStrings(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" || s == "null" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// marshalHeaders renders a header map as a JSON object. A nil or empty map
// becomes "{}".
func marshalHeaders(v map[string]string) string {
	if len(v) == 0 {
		return "{}"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// unmarshalHeaders reads a JSON object of headers.
func unmarshalHeaders(s string) map[string]string {
	s = strings.TrimSpace(s)
	if s == "" || s == "{}" || s == "null" {
		return nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Query building helpers.

// placeholders returns "?, ?, ?" for n arguments.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// stringArgs converts a string slice into query arguments.
func stringArgs(v []string) []any {
	out := make([]any, 0, len(v))
	for _, s := range v {
		out = append(out, s)
	}
	return out
}

// likeEscape escapes the LIKE wildcards in a literal. Every query that uses
// it appends ESCAPE '\'.
func likeEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
}

// limitClause appends LIMIT and OFFSET when they are set. A zero limit with a
// non-zero offset still needs a limit, so SQLite gets -1 for "no limit".
func limitClause(limit, offset int) (string, []any) {
	switch {
	case limit > 0 && offset > 0:
		return " LIMIT ? OFFSET ?", []any{limit, offset}
	case limit > 0:
		return " LIMIT ?", []any{limit}
	case offset > 0:
		return " LIMIT -1 OFFSET ?", []any{offset}
	}
	return "", nil
}
