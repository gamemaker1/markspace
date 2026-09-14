package index

// migration6 adds the model catalog, per-task routes and the public price
// catalog cache, and removes the fields that routes and fetched prices
// replace: provider prices and the default flag, the instance default and
// embedding provider, and the per-user provider and model.
const migration6 = `
ALTER TABLE ai_providers ADD COLUMN image_config TEXT NOT NULL DEFAULT '{}';
ALTER TABLE ai_providers ADD COLUMN preset TEXT NOT NULL DEFAULT '';
ALTER TABLE ai_providers ADD COLUMN local INTEGER NOT NULL DEFAULT 0;
ALTER TABLE ai_providers DROP COLUMN prices;
ALTER TABLE ai_providers DROP COLUMN is_default;

ALTER TABLE settings DROP COLUMN ai_default_provider;
ALTER TABLE settings DROP COLUMN embedding_provider;
ALTER TABLE settings ADD COLUMN ai_allow_user_routes INTEGER NOT NULL DEFAULT 1;

ALTER TABLE users DROP COLUMN ai_provider_id;
ALTER TABLE users DROP COLUMN ai_model;

ALTER TABLE ai_usage ADD COLUMN task TEXT NOT NULL DEFAULT '';
ALTER TABLE ai_usage ADD COLUMN attempts INTEGER NOT NULL DEFAULT 1;

CREATE TABLE ai_models (
	provider_id      TEXT NOT NULL,
	model_id         TEXT NOT NULL,
	name             TEXT NOT NULL DEFAULT '',
	vendor           TEXT NOT NULL DEFAULT '',
	context_length   INTEGER NOT NULL DEFAULT 0,
	max_output       INTEGER NOT NULL DEFAULT 0,
	inputs           TEXT NOT NULL DEFAULT '["text"]',
	outputs          TEXT NOT NULL DEFAULT '["text"]',
	tools            INTEGER NOT NULL DEFAULT 0,
	price            TEXT NOT NULL DEFAULT '{}',
	price_source     TEXT NOT NULL DEFAULT '',
	price_matched    TEXT NOT NULL DEFAULT '',
	price_fetched_at TEXT NOT NULL DEFAULT '',
	price_override   TEXT NOT NULL DEFAULT '',
	enabled          INTEGER NOT NULL DEFAULT 1,
	available        INTEGER NOT NULL DEFAULT 1,
	last_seen        TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (provider_id, model_id)
);

CREATE TABLE ai_routes (
	scope   TEXT NOT NULL,
	task    TEXT NOT NULL,
	targets TEXT NOT NULL DEFAULT '[]',
	PRIMARY KEY (scope, task)
);

CREATE TABLE price_catalogs (
	source     TEXT PRIMARY KEY,
	body       BLOB,
	fetched_at TEXT NOT NULL DEFAULT ''
);
`
