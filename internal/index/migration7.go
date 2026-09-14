package index

// migration7 adds the job queue, the plugin host tables, suggestions and the
// user kind. docs/plugins.md specifies the tables.
//
// Notes on the schema:
//   - jobs carries pause_requested beside the docs/plugins.md columns, because a
//     pause requested while a job runs must survive a restart.
//   - plugins.signing_secret holds the base64 signing secret as is. Markspace
//     signs every request with it, so it cannot be stored as a hash.
//   - plugin_secrets.value is stored as is, like provider API keys.
//   - plugin_kv and plugin_blobs keep size before any blob column, so summing
//     sizes for the quota does not read value pages. The limits are enforced
//     in Go.
//   - plugin_idempotency.status is 0 while the first request is in progress.
//   - Plugin tables reference plugins with ON DELETE CASCADE. Uninstall keeps
//     the plugins row, so the admin's choice of what to keep holds.
const migration7 = `
CREATE TABLE jobs (
	id              TEXT PRIMARY KEY,
	kind            TEXT NOT NULL,
	module          TEXT NOT NULL,
	space_id        TEXT NOT NULL DEFAULT '',
	created_by      TEXT NOT NULL DEFAULT '',
	status          TEXT NOT NULL CHECK (status IN ('queued', 'running', 'paused', 'done', 'failed', 'canceled')),
	title           TEXT NOT NULL DEFAULT '',
	progress        INTEGER NOT NULL DEFAULT 0,
	total           INTEGER NOT NULL DEFAULT 0,
	message         TEXT NOT NULL DEFAULT '',
	payload         TEXT NOT NULL DEFAULT '{}',
	result          TEXT NOT NULL DEFAULT '{}',
	attempts        INTEGER NOT NULL DEFAULT 0,
	max_attempts    INTEGER NOT NULL DEFAULT 3,
	pause_requested INTEGER NOT NULL DEFAULT 0,
	run_after       TEXT NOT NULL DEFAULT '',
	lease_until     TEXT NOT NULL DEFAULT '',
	parent_id       TEXT NOT NULL DEFAULT '',
	created_at      TEXT NOT NULL,
	updated_at      TEXT NOT NULL
);
CREATE INDEX jobs_claim ON jobs(status, run_after, created_at, id);
CREATE INDEX jobs_module_status ON jobs(module, status);
CREATE INDEX jobs_parent ON jobs(parent_id, status);
CREATE INDEX jobs_updated ON jobs(updated_at);
CREATE INDEX jobs_created ON jobs(created_at, id);

CREATE TABLE job_log (
	job_id  TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
	at      TEXT NOT NULL,
	level   TEXT NOT NULL,
	message TEXT NOT NULL
);
CREATE INDEX job_log_job_at ON job_log(job_id, at);

CREATE TABLE job_schedules (
	kind     TEXT PRIMARY KEY,
	last_run TEXT NOT NULL DEFAULT ''
);

ALTER TABLE users ADD COLUMN kind TEXT NOT NULL DEFAULT 'person' CHECK (kind IN ('person', 'plugin'));

CREATE TABLE plugins (
	id                TEXT PRIMARY KEY,
	name              TEXT NOT NULL DEFAULT '',
	version           TEXT NOT NULL DEFAULT '',
	base_url          TEXT NOT NULL,
	status            TEXT NOT NULL CHECK (status IN ('needs_approval', 'active', 'paused', 'down', 'uninstalled')),
	manifest          TEXT NOT NULL DEFAULT '{}',
	approved_manifest TEXT NOT NULL DEFAULT '{}',
	user_id           TEXT NOT NULL DEFAULT '',
	token_hash        TEXT NOT NULL DEFAULT '',
	token_prefix      TEXT NOT NULL DEFAULT '',
	-- The base64 signing secret, stored as is because signing needs it.
	signing_secret    TEXT NOT NULL DEFAULT '',
	permissions       TEXT NOT NULL DEFAULT '[]',
	settings          TEXT NOT NULL DEFAULT '{}',
	health_failures   INTEGER NOT NULL DEFAULT 0,
	last_health_at    TEXT NOT NULL DEFAULT '',
	last_error        TEXT NOT NULL DEFAULT '',
	created_at        TEXT NOT NULL,
	updated_at        TEXT NOT NULL
);
CREATE UNIQUE INDEX plugins_token_hash ON plugins(token_hash) WHERE token_hash <> '';

CREATE TABLE plugin_grants (
	plugin_id TEXT NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
	space_id  TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
	role      TEXT NOT NULL CHECK (role IN ('viewer', 'commenter', 'editor')),
	PRIMARY KEY (plugin_id, space_id)
);
CREATE INDEX plugin_grants_space ON plugin_grants(space_id);

CREATE TABLE plugin_secrets (
	plugin_id  TEXT NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
	name       TEXT NOT NULL,
	value      TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL,
	PRIMARY KEY (plugin_id, name)
);

CREATE TABLE plugin_deliveries (
	id              TEXT PRIMARY KEY,
	plugin_id       TEXT NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
	path            TEXT NOT NULL,
	attempts        INTEGER NOT NULL DEFAULT 0,
	next_attempt_at TEXT NOT NULL,
	expires_at      TEXT NOT NULL,
	last_status     INTEGER NOT NULL DEFAULT 0,
	created_at      TEXT NOT NULL,
	body            BLOB NOT NULL
);
CREATE INDEX plugin_deliveries_due ON plugin_deliveries(plugin_id, next_attempt_at);
CREATE INDEX plugin_deliveries_expires ON plugin_deliveries(expires_at);

CREATE TABLE plugin_kv (
	plugin_id  TEXT NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
	key        TEXT NOT NULL,
	size       INTEGER NOT NULL,
	updated_at TEXT NOT NULL,
	value      BLOB NOT NULL,
	PRIMARY KEY (plugin_id, key)
);

CREATE TABLE plugin_blobs (
	plugin_id  TEXT NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
	key        TEXT NOT NULL,
	size       INTEGER NOT NULL,
	sha256     TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY (plugin_id, key)
);

CREATE TABLE plugin_idempotency (
	plugin_id  TEXT NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
	key        TEXT NOT NULL,
	method     TEXT NOT NULL,
	path       TEXT NOT NULL,
	status     INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	body       BLOB NOT NULL,
	PRIMARY KEY (plugin_id, key)
);
CREATE INDEX plugin_idempotency_created ON plugin_idempotency(created_at);

CREATE TABLE acting_tokens (
	token_hash  TEXT PRIMARY KEY,
	plugin_id   TEXT NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
	user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	interaction TEXT NOT NULL DEFAULT '',
	expires_at  TEXT NOT NULL,
	created_at  TEXT NOT NULL
);
CREATE INDEX acting_tokens_plugin ON acting_tokens(plugin_id);
CREATE INDEX acting_tokens_expires ON acting_tokens(expires_at);

CREATE TABLE suggestions (
	id              TEXT PRIMARY KEY,
	space_id        TEXT NOT NULL,
	note_id         TEXT NOT NULL,
	author_kind     TEXT NOT NULL CHECK (author_kind IN ('user', 'plugin', 'ai')),
	author_id       TEXT NOT NULL DEFAULT '',
	title           TEXT NOT NULL DEFAULT '',
	rationale       TEXT NOT NULL DEFAULT '',
	edits           TEXT NOT NULL DEFAULT '[]',
	base_updated_at TEXT NOT NULL DEFAULT '',
	status          TEXT NOT NULL CHECK (status IN ('open', 'accepted', 'partial', 'discarded', 'stale')),
	created_at      TEXT NOT NULL,
	resolved_by     TEXT NOT NULL DEFAULT '',
	resolved_at     TEXT NOT NULL DEFAULT '',
	version         INTEGER NOT NULL DEFAULT 1,
	updated_at      TEXT NOT NULL
);
CREATE INDEX suggestions_note_status ON suggestions(note_id, status);
CREATE INDEX suggestions_space_status ON suggestions(space_id, status);
CREATE INDEX suggestions_author ON suggestions(author_kind, author_id);
`
