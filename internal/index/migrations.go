package index

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// migrations holds one SQL script per schema version. Index 0 is version 1.
// Append a new script to add a version. Never edit a script that has shipped,
// because databases in the field have already applied it.
var migrations = []string{
	// Version 1: the initial schema.
	`
CREATE TABLE users (
	id             TEXT PRIMARY KEY,
	email          TEXT NOT NULL COLLATE NOCASE UNIQUE,
	name           TEXT NOT NULL DEFAULT '',
	password_hash  TEXT NOT NULL DEFAULT '',
	is_admin       INTEGER NOT NULL DEFAULT 0,
	is_active      INTEGER NOT NULL DEFAULT 1,
	locale         TEXT NOT NULL DEFAULT '',
	timezone       TEXT NOT NULL DEFAULT '',
	avatar_color   TEXT NOT NULL DEFAULT '',
	last_login_at  TEXT NOT NULL DEFAULT '',
	created_at     TEXT NOT NULL DEFAULT '',
	updated_at     TEXT NOT NULL DEFAULT '',
	ai_provider_id TEXT NOT NULL DEFAULT '',
	ai_model       TEXT NOT NULL DEFAULT ''
);

CREATE TABLE sessions (
	id         TEXT PRIMARY KEY,
	user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	token_hash TEXT NOT NULL UNIQUE,
	user_agent TEXT NOT NULL DEFAULT '',
	ip         TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT '',
	expires_at TEXT NOT NULL DEFAULT '',
	last_seen  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sessions_token_hash ON sessions(token_hash);
CREATE INDEX sessions_user_id ON sessions(user_id);

CREATE TABLE api_tokens (
	id         TEXT PRIMARY KEY,
	user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	name       TEXT NOT NULL DEFAULT '',
	token_hash TEXT NOT NULL UNIQUE,
	prefix     TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT '',
	expires_at TEXT NOT NULL DEFAULT '',
	last_used  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX api_tokens_user_id ON api_tokens(user_id);

CREATE TABLE invites (
	id         TEXT PRIMARY KEY,
	email      TEXT NOT NULL DEFAULT '' COLLATE NOCASE,
	token      TEXT NOT NULL UNIQUE,
	space_id   TEXT NOT NULL DEFAULT '',
	role       TEXT NOT NULL DEFAULT '',
	is_admin   INTEGER NOT NULL DEFAULT 0,
	created_by TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT '',
	expires_at TEXT NOT NULL DEFAULT '',
	used_at    TEXT NOT NULL DEFAULT ''
);

CREATE TABLE spaces (
	id          TEXT PRIMARY KEY,
	slug        TEXT NOT NULL UNIQUE,
	name        TEXT NOT NULL DEFAULT '',
	description TEXT NOT NULL DEFAULT '',
	icon        TEXT NOT NULL DEFAULT '',
	created_by  TEXT NOT NULL DEFAULT '',
	created_at  TEXT NOT NULL DEFAULT '',
	updated_at  TEXT NOT NULL DEFAULT ''
);

CREATE TABLE space_members (
	space_id   TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
	user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	role       TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (space_id, user_id)
);
CREATE INDEX space_members_user_id ON space_members(user_id);

CREATE TABLE notes (
	id         TEXT PRIMARY KEY,
	space_id   TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
	parent_id  TEXT NOT NULL DEFAULT '',
	slug       TEXT NOT NULL DEFAULT '',
	path       TEXT NOT NULL DEFAULT '',
	title      TEXT NOT NULL DEFAULT '',
	icon       TEXT NOT NULL DEFAULT '',
	tags       TEXT NOT NULL DEFAULT '[]',
	excerpt    TEXT NOT NULL DEFAULT '',
	position   INTEGER NOT NULL DEFAULT 0,
	words      INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL DEFAULT '',
	created_by TEXT NOT NULL DEFAULT '',
	updated_by TEXT NOT NULL DEFAULT '',
	deleted    INTEGER NOT NULL DEFAULT 0,
	deleted_at TEXT NOT NULL DEFAULT '',
	deleted_by TEXT NOT NULL DEFAULT '',
	mod_time   TEXT NOT NULL DEFAULT '',
	size       INTEGER NOT NULL DEFAULT 0,
	UNIQUE (space_id, path)
);
CREATE INDEX notes_space_parent ON notes(space_id, parent_id);
CREATE INDEX notes_space_deleted ON notes(space_id, deleted);
CREATE INDEX notes_updated_at ON notes(updated_at);

CREATE TABLE note_tags (
	note_id  TEXT NOT NULL,
	space_id TEXT NOT NULL DEFAULT '',
	tag      TEXT NOT NULL COLLATE NOCASE,
	PRIMARY KEY (note_id, tag)
);
CREATE INDEX note_tags_space_tag ON note_tags(space_id, tag);

CREATE TABLE note_links (
	note_id        TEXT NOT NULL,
	space_id       TEXT NOT NULL DEFAULT '',
	target         TEXT NOT NULL DEFAULT '',
	target_note_id TEXT
);
CREATE INDEX note_links_note_id ON note_links(note_id);
CREATE INDEX note_links_target_note_id ON note_links(target_note_id);
CREATE INDEX note_links_space_target ON note_links(space_id, target);

CREATE TABLE stars (
	user_id    TEXT NOT NULL,
	note_id    TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (user_id, note_id)
);
CREATE INDEX stars_note_id ON stars(note_id);

CREATE TABLE comments (
	id          TEXT PRIMARY KEY,
	note_id     TEXT NOT NULL,
	space_id    TEXT NOT NULL DEFAULT '',
	user_id     TEXT NOT NULL DEFAULT '',
	parent_id   TEXT NOT NULL DEFAULT '',
	body        TEXT NOT NULL DEFAULT '',
	anchor      TEXT NOT NULL DEFAULT '',
	resolved    INTEGER NOT NULL DEFAULT 0,
	resolved_by TEXT NOT NULL DEFAULT '',
	created_at  TEXT NOT NULL DEFAULT '',
	updated_at  TEXT NOT NULL DEFAULT '',
	deleted_at  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX comments_note_id ON comments(note_id, created_at);

CREATE TABLE shares (
	id               TEXT PRIMARY KEY,
	note_id          TEXT NOT NULL,
	space_id         TEXT NOT NULL DEFAULT '',
	user_id          TEXT NOT NULL DEFAULT '',
	access           INTEGER NOT NULL DEFAULT 0,
	include_children INTEGER NOT NULL DEFAULT 0,
	created_by       TEXT NOT NULL DEFAULT '',
	created_at       TEXT NOT NULL DEFAULT '',
	UNIQUE (note_id, user_id)
);
CREATE INDEX shares_user_id ON shares(user_id);

CREATE TABLE public_links (
	id               TEXT PRIMARY KEY,
	token            TEXT NOT NULL UNIQUE,
	note_id          TEXT NOT NULL,
	space_id         TEXT NOT NULL DEFAULT '',
	access           INTEGER NOT NULL DEFAULT 0,
	password_hash    TEXT NOT NULL DEFAULT '',
	include_children INTEGER NOT NULL DEFAULT 0,
	expires_at       TEXT NOT NULL DEFAULT '',
	views            INTEGER NOT NULL DEFAULT 0,
	created_by       TEXT NOT NULL DEFAULT '',
	created_at       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX public_links_note_id ON public_links(note_id);

CREATE TABLE attachments (
	id         TEXT PRIMARY KEY,
	space_id   TEXT NOT NULL DEFAULT '',
	note_id    TEXT NOT NULL DEFAULT '',
	name       TEXT NOT NULL DEFAULT '',
	mime       TEXT NOT NULL DEFAULT '',
	size       INTEGER NOT NULL DEFAULT 0,
	width      INTEGER NOT NULL DEFAULT 0,
	height     INTEGER NOT NULL DEFAULT 0,
	created_by TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX attachments_note_id ON attachments(note_id);
CREATE INDEX attachments_space_id ON attachments(space_id);

CREATE TABLE settings (
	id                  INTEGER PRIMARY KEY CHECK (id = 1),
	instance_name       TEXT NOT NULL DEFAULT '',
	allow_signup        INTEGER NOT NULL DEFAULT 1,
	default_space_id    TEXT NOT NULL DEFAULT '',
	ai_enabled          INTEGER NOT NULL DEFAULT 1,
	ai_default_provider TEXT NOT NULL DEFAULT '',
	ai_allow_user_keys  INTEGER NOT NULL DEFAULT 1,
	embedding_provider  TEXT NOT NULL DEFAULT '',
	updated_at          TEXT NOT NULL DEFAULT ''
);

CREATE TABLE ai_providers (
	id              TEXT PRIMARY KEY,
	name            TEXT NOT NULL DEFAULT '',
	kind            TEXT NOT NULL DEFAULT '',
	base_url        TEXT NOT NULL DEFAULT '',
	api_key         TEXT NOT NULL DEFAULT '',
	model           TEXT NOT NULL DEFAULT '',
	embed_model     TEXT NOT NULL DEFAULT '',
	models          TEXT NOT NULL DEFAULT '[]',
	headers         TEXT NOT NULL DEFAULT '{}',
	temperature     REAL NOT NULL DEFAULT 0,
	max_tokens      INTEGER NOT NULL DEFAULT 0,
	timeout_seconds INTEGER NOT NULL DEFAULT 0,
	enabled         INTEGER NOT NULL DEFAULT 1,
	is_default      INTEGER NOT NULL DEFAULT 0,
	created_at      TEXT NOT NULL DEFAULT '',
	updated_at      TEXT NOT NULL DEFAULT ''
);

CREATE TABLE embeddings (
	id         TEXT PRIMARY KEY,
	note_id    TEXT NOT NULL,
	space_id   TEXT NOT NULL DEFAULT '',
	ordinal    INTEGER NOT NULL DEFAULT 0,
	text       TEXT NOT NULL DEFAULT '',
	vector     BLOB,
	model      TEXT NOT NULL DEFAULT '',
	hash       TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX embeddings_space_model ON embeddings(space_id, model);
CREATE INDEX embeddings_note_id ON embeddings(note_id);

CREATE VIRTUAL TABLE notes_fts USING fts5(
	title,
	body,
	tags,
	note_id UNINDEXED,
	space_id UNINDEXED,
	tokenize='unicode61 remove_diacritics 2'
);
`,
	// 2: personal AI providers record their owner.
	`
ALTER TABLE ai_providers ADD COLUMN owner_id TEXT NOT NULL DEFAULT '';
CREATE INDEX ai_providers_owner ON ai_providers(owner_id);
`,
	// 3: note revisions for the sync change feed. The counter lives in its own
	// table so deleting the newest note never lets a revision be reused.
	`
CREATE TABLE counters (name TEXT PRIMARY KEY, value INTEGER NOT NULL);
ALTER TABLE notes ADD COLUMN rev INTEGER NOT NULL DEFAULT 0;
UPDATE notes SET rev = rowid;
INSERT INTO counters (name, value) VALUES ('note_rev', (SELECT COALESCE(MAX(rev), 0) FROM notes));
CREATE INDEX notes_rev ON notes(rev);
`,
	// 4: AI usage records, monthly rollups, budgets and model prices.
	`
CREATE TABLE ai_usage (
	id            TEXT PRIMARY KEY,
	user_id       TEXT NOT NULL DEFAULT '',
	provider_id   TEXT NOT NULL DEFAULT '',
	model         TEXT NOT NULL DEFAULT '',
	feature       TEXT NOT NULL DEFAULT '',
	plugin_id     TEXT NOT NULL DEFAULT '',
	space_id      TEXT NOT NULL DEFAULT '',
	note_id       TEXT NOT NULL DEFAULT '',
	input_tokens  INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	images_in     INTEGER NOT NULL DEFAULT 0,
	images_out    INTEGER NOT NULL DEFAULT 0,
	duration_ms   INTEGER NOT NULL DEFAULT 0,
	status        TEXT NOT NULL DEFAULT 'ok' CHECK (status IN ('ok', 'error', 'canceled', 'budget')),
	error         TEXT NOT NULL DEFAULT '',
	cost_micros   INTEGER NOT NULL DEFAULT 0,
	estimated     INTEGER NOT NULL DEFAULT 0,
	created_at    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX ai_usage_user_created ON ai_usage(user_id, created_at);
CREATE INDEX ai_usage_created ON ai_usage(created_at);
CREATE INDEX ai_usage_provider_created ON ai_usage(provider_id, created_at);
CREATE INDEX ai_usage_plugin_created ON ai_usage(plugin_id, created_at);

CREATE TABLE ai_usage_monthly (
	month           TEXT NOT NULL,
	user_id         TEXT NOT NULL DEFAULT '',
	provider_id     TEXT NOT NULL DEFAULT '',
	model           TEXT NOT NULL DEFAULT '',
	feature         TEXT NOT NULL DEFAULT '',
	plugin_id       TEXT NOT NULL DEFAULT '',
	calls           INTEGER NOT NULL DEFAULT 0,
	errors          INTEGER NOT NULL DEFAULT 0,
	input_tokens    INTEGER NOT NULL DEFAULT 0,
	output_tokens   INTEGER NOT NULL DEFAULT 0,
	images_in       INTEGER NOT NULL DEFAULT 0,
	images_out      INTEGER NOT NULL DEFAULT 0,
	duration_ms     INTEGER NOT NULL DEFAULT 0,
	cost_micros     INTEGER NOT NULL DEFAULT 0,
	estimated_calls INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (month, user_id, provider_id, model, feature, plugin_id)
);

CREATE TABLE ai_budgets (
	subject_kind   TEXT NOT NULL CHECK (subject_kind IN ('user', 'plugin')),
	subject_id     TEXT NOT NULL,
	monthly_tokens INTEGER,
	monthly_cost   REAL,
	lifted_until   TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (subject_kind, subject_id)
);

ALTER TABLE settings ADD COLUMN ai_monthly_token_budget INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN ai_monthly_cost_budget REAL NOT NULL DEFAULT 0;
ALTER TABLE ai_providers ADD COLUMN prices TEXT NOT NULL DEFAULT '{}';
`,
	// 5: folders and files as items. Notes are rebuilt so that path
	// uniqueness covers live rows only and ignores case, like the new tables.
	// Folders and files take revisions from the note_rev counter. Files
	// replace the attachments table, and spaces gain the paste folder.
	`
CREATE TABLE notes_new (
	id         TEXT PRIMARY KEY,
	space_id   TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
	parent_id  TEXT NOT NULL DEFAULT '',
	slug       TEXT NOT NULL DEFAULT '',
	path       TEXT NOT NULL DEFAULT '',
	title      TEXT NOT NULL DEFAULT '',
	icon       TEXT NOT NULL DEFAULT '',
	tags       TEXT NOT NULL DEFAULT '[]',
	excerpt    TEXT NOT NULL DEFAULT '',
	position   INTEGER NOT NULL DEFAULT 0,
	words      INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL DEFAULT '',
	created_by TEXT NOT NULL DEFAULT '',
	updated_by TEXT NOT NULL DEFAULT '',
	deleted    INTEGER NOT NULL DEFAULT 0,
	deleted_at TEXT NOT NULL DEFAULT '',
	deleted_by TEXT NOT NULL DEFAULT '',
	mod_time   TEXT NOT NULL DEFAULT '',
	size       INTEGER NOT NULL DEFAULT 0,
	rev        INTEGER NOT NULL DEFAULT 0
);
INSERT INTO notes_new (id, space_id, parent_id, slug, path, title, icon, tags, excerpt,
	position, words, created_at, updated_at, created_by, updated_by, deleted, deleted_at,
	deleted_by, mod_time, size, rev)
SELECT id, space_id, parent_id, slug, path, title, icon, tags, excerpt,
	position, words, created_at, updated_at, created_by, updated_by, deleted, deleted_at,
	deleted_by, mod_time, size, rev FROM notes;
DROP TABLE notes;
ALTER TABLE notes_new RENAME TO notes;
CREATE UNIQUE INDEX notes_live_path ON notes(space_id, path COLLATE NOCASE) WHERE deleted = 0;
CREATE INDEX notes_space_path ON notes(space_id, path);
CREATE INDEX notes_space_parent ON notes(space_id, parent_id);
CREATE INDEX notes_space_deleted ON notes(space_id, deleted);
CREATE INDEX notes_updated_at ON notes(updated_at);
CREATE INDEX notes_rev ON notes(rev);

CREATE TABLE folders (
	id            TEXT PRIMARY KEY,
	space_id      TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
	parent_id     TEXT NOT NULL DEFAULT '',
	name          TEXT NOT NULL DEFAULT '',
	path          TEXT NOT NULL DEFAULT '',
	position      INTEGER NOT NULL DEFAULT 0,
	index_note_id TEXT NOT NULL DEFAULT '',
	rev           INTEGER NOT NULL DEFAULT 0,
	created_at    TEXT NOT NULL DEFAULT '',
	updated_at    TEXT NOT NULL DEFAULT '',
	created_by    TEXT NOT NULL DEFAULT '',
	deleted       INTEGER NOT NULL DEFAULT 0,
	deleted_at    TEXT NOT NULL DEFAULT '',
	deleted_by    TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX folders_live_path ON folders(space_id, path COLLATE NOCASE) WHERE deleted = 0;
CREATE INDEX folders_space_parent ON folders(space_id, parent_id);
CREATE INDEX folders_rev ON folders(rev);

CREATE TABLE files (
	id         TEXT PRIMARY KEY,
	space_id   TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
	parent_id  TEXT NOT NULL DEFAULT '',
	name       TEXT NOT NULL DEFAULT '',
	path       TEXT NOT NULL DEFAULT '',
	mime       TEXT NOT NULL DEFAULT '',
	size       INTEGER NOT NULL DEFAULT 0,
	sha256     TEXT NOT NULL DEFAULT '',
	width      INTEGER NOT NULL DEFAULT 0,
	height     INTEGER NOT NULL DEFAULT 0,
	duration   REAL NOT NULL DEFAULT 0,
	pages      INTEGER NOT NULL DEFAULT 0,
	position   INTEGER NOT NULL DEFAULT 0,
	versions   INTEGER NOT NULL DEFAULT 0,
	rev        INTEGER NOT NULL DEFAULT 0,
	mod_time   TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL DEFAULT '',
	created_by TEXT NOT NULL DEFAULT '',
	updated_by TEXT NOT NULL DEFAULT '',
	deleted    INTEGER NOT NULL DEFAULT 0,
	deleted_at TEXT NOT NULL DEFAULT '',
	deleted_by TEXT NOT NULL DEFAULT '',
	text_state TEXT NOT NULL DEFAULT 'none'
		CHECK (text_state IN ('none', 'pending', 'done', 'failed', 'skipped'))
);
CREATE UNIQUE INDEX files_live_path ON files(space_id, path COLLATE NOCASE) WHERE deleted = 0;
CREATE INDEX files_space_parent ON files(space_id, parent_id);
CREATE INDEX files_rev ON files(rev);
CREATE INDEX files_space_hash ON files(space_id, sha256, size);
CREATE INDEX files_needs_hash ON files(id) WHERE sha256 = '' AND deleted = 0;
CREATE INDEX files_text_pending ON files(id) WHERE text_state = 'pending' AND deleted = 0;

CREATE VIRTUAL TABLE files_fts USING fts5(
	name,
	body,
	file_id UNINDEXED,
	space_id UNINDEXED,
	tokenize='unicode61 remove_diacritics 2'
);

ALTER TABLE shares ADD COLUMN target_kind TEXT NOT NULL DEFAULT 'note';
ALTER TABLE public_links ADD COLUMN target_kind TEXT NOT NULL DEFAULT 'note';
ALTER TABLE comments ADD COLUMN target_kind TEXT NOT NULL DEFAULT 'note';

ALTER TABLE note_links ADD COLUMN target_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE note_links ADD COLUMN form TEXT NOT NULL DEFAULT '';
ALTER TABLE note_links ADD COLUMN embed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE note_links ADD COLUMN heading TEXT NOT NULL DEFAULT '';

DROP TABLE attachments;
ALTER TABLE spaces ADD COLUMN paste_folder TEXT NOT NULL DEFAULT '';
`,
	// 6: model catalog, routes, price catalogs and provider presets. See migration6.go.
	migration6,
	// 7: jobs, plugins, deliveries, plugin storage and suggestions. See migration7.go.
	migration7,
	// 8: conversations of the AI rail tab. See migration8.go.
	migration8,
	// 9: the plugin a person wrote a note through.
	migration9,
	// 10: the time of a comment's last body edit.
	migration10,
	// 11: system API tokens, which belong to no user.
	migration11,
}

// migration9 records the plugin behind a note write made with an acting
// token. notes.updated_by holds the person and notes.via the plugin id. The
// same value is the updated_via frontmatter key and the via of a version.
const migration9 = `
ALTER TABLE notes ADD COLUMN via TEXT NOT NULL DEFAULT '';
`

// migration10 stores when a comment body was last edited. updated_at also
// changes when a thread is resolved, so it cannot mark a comment as edited.
// Rows from before the migration read as never edited.
const migration10 = `
ALTER TABLE comments ADD COLUMN edited_at TEXT NOT NULL DEFAULT '';
`

// migration11 lets an API token belong to no user. A system token, such as
// the Markspace CLI token, stores NULL in user_id. SQLite cannot drop a NOT
// NULL constraint, so the table is rebuilt. No table references api_tokens.
const migration11 = `
CREATE TABLE api_tokens_new (
	id         TEXT PRIMARY KEY,
	user_id    TEXT REFERENCES users(id) ON DELETE CASCADE,
	name       TEXT NOT NULL DEFAULT '',
	token_hash TEXT NOT NULL UNIQUE,
	prefix     TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT '',
	expires_at TEXT NOT NULL DEFAULT '',
	last_used  TEXT NOT NULL DEFAULT ''
);
INSERT INTO api_tokens_new (id, user_id, name, token_hash, prefix, created_at, expires_at, last_used)
	SELECT id, user_id, name, token_hash, prefix, created_at, expires_at, last_used FROM api_tokens;
DROP TABLE api_tokens;
ALTER TABLE api_tokens_new RENAME TO api_tokens;
CREATE INDEX api_tokens_user_id ON api_tokens(user_id);
`

// SchemaVersion returns the schema version this build expects, which is the
// number of migrations it carries.
func SchemaVersion() int { return len(migrations) }

// migrate applies every migration the database has not seen, in order.
func (d *DB) migrate(ctx context.Context) error {
	const createTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT ''
	)`
	if _, err := d.sql.ExecContext(ctx, createTable); err != nil {
		return fmt.Errorf("index: create schema_migrations: %w", err)
	}

	var current int
	err := d.sql.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current)
	if err != nil {
		return fmt.Errorf("index: read schema version: %w", err)
	}
	if current >= len(migrations) {
		return nil
	}

	for i := current; i < len(migrations); i++ {
		version := i + 1
		if err := d.applyMigration(ctx, version, migrations[i]); err != nil {
			return err
		}
		d.logger.Info("index: migration applied", "version", version)
	}
	return nil
}

// applyMigration runs one migration script and records its version in the
// same transaction, so a failure leaves no partial version behind.
func (d *DB) applyMigration(ctx context.Context, version int, script string) error {
	err := d.tx(ctx, func(t *sql.Tx) error {
		if _, err := t.ExecContext(ctx, script); err != nil {
			return fmt.Errorf("index: migration %d: %w", version, err)
		}
		_, err := t.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			version, fmtTime(time.Now()))
		if err != nil {
			return fmt.Errorf("index: record migration %d: %w", version, err)
		}
		return nil
	})
	return err
}
