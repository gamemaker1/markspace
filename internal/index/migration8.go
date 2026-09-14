package index

// migration8 adds the conversations of the AI rail tab. docs/assistant.md
// specifies the tables.
//
// Notes on the schema:
//   - A thread belongs to one user. user_id references users with ON DELETE
//     CASCADE, so deleting an account deletes its conversations.
//   - ai_threads_user_note serves the listing of one user's threads about a
//     note, newest first. ai_threads_updated serves the retention delete of
//     threads without activity for 90 days.
//   - Messages cascade with their thread. context and sources hold JSON
//     arrays of core.AIContextRef and core.AISource.
const migration8 = `
CREATE TABLE ai_threads (
	id         TEXT PRIMARY KEY,
	user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	note_id    TEXT NOT NULL DEFAULT '',
	space_id   TEXT NOT NULL DEFAULT '',
	title      TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX ai_threads_user_note ON ai_threads(user_id, note_id, updated_at);
CREATE INDEX ai_threads_updated ON ai_threads(updated_at);

CREATE TABLE ai_messages (
	id         TEXT PRIMARY KEY,
	thread_id  TEXT NOT NULL REFERENCES ai_threads(id) ON DELETE CASCADE,
	role       TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
	content    TEXT NOT NULL,
	context    TEXT NOT NULL DEFAULT '[]',
	sources    TEXT NOT NULL DEFAULT '[]',
	applied    INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL
);
CREATE INDEX ai_messages_thread ON ai_messages(thread_id, created_at, id);
`
