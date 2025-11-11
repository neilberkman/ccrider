package db

func (db *DB) initSchema() error {
	schema := `
	-- Sessions table
	CREATE TABLE IF NOT EXISTS sessions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT UNIQUE NOT NULL,
		project_path TEXT NOT NULL,
		summary TEXT,
		leaf_uuid TEXT,
		cwd TEXT,
		git_branch TEXT,
		created_at DATETIME,
		updated_at DATETIME,
		message_count INTEGER DEFAULT 0,
		version TEXT,
		imported_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		last_synced_at DATETIME,
		file_hash TEXT,
		file_size INTEGER,
		file_mtime DATETIME
	);

	CREATE INDEX IF NOT EXISTS idx_sessions_session_id ON sessions(session_id);
	CREATE INDEX IF NOT EXISTS idx_sessions_project_path ON sessions(project_path);
	CREATE INDEX IF NOT EXISTS idx_sessions_updated_at ON sessions(updated_at);
	CREATE INDEX IF NOT EXISTS idx_sessions_git_branch ON sessions(git_branch);

	-- Messages table
	CREATE TABLE IF NOT EXISTS messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		uuid TEXT UNIQUE NOT NULL,
		session_id INTEGER NOT NULL,
		parent_uuid TEXT,
		type TEXT NOT NULL,
		sender TEXT,
		content TEXT,
		text_content TEXT,
		timestamp DATETIME,
		sequence INTEGER,
		is_sidechain BOOLEAN,
		cwd TEXT,
		git_branch TEXT,
		version TEXT,
		FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_messages_uuid ON messages(uuid);
	CREATE INDEX IF NOT EXISTS idx_messages_session_id ON messages(session_id);
	CREATE INDEX IF NOT EXISTS idx_messages_parent_uuid ON messages(parent_uuid);
	CREATE INDEX IF NOT EXISTS idx_messages_timestamp ON messages(timestamp);

	-- Tool uses table
	CREATE TABLE IF NOT EXISTS tool_uses (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		message_id INTEGER NOT NULL,
		tool_name TEXT NOT NULL,
		tool_id TEXT,
		input TEXT,
		output TEXT,
		created_at DATETIME,
		FOREIGN KEY (message_id) REFERENCES messages(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_tool_uses_message_id ON tool_uses(message_id);
	CREATE INDEX IF NOT EXISTS idx_tool_uses_tool_name ON tool_uses(tool_name);

	-- Import log table
	CREATE TABLE IF NOT EXISTS import_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		file_path TEXT NOT NULL,
		file_hash TEXT NOT NULL,
		imported_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		sessions_imported INTEGER,
		messages_imported INTEGER,
		status TEXT CHECK(status IN ('success', 'partial', 'failed')),
		error_message TEXT
	);

	CREATE INDEX IF NOT EXISTS idx_import_log_file_hash ON import_log(file_hash);

	-- FTS5 tables for full-text search
	-- Natural language search with porter stemming
	CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
		text_content,
		content=messages,
		content_rowid=id,
		tokenize='porter unicode61'
	);

	-- Code search without stemming (preserves symbols, camelCase)
	CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts_code USING fts5(
		text_content,
		content=messages,
		content_rowid=id,
		tokenize='unicode61'
	);

	-- Triggers to keep FTS in sync
	CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
		INSERT INTO messages_fts(rowid, text_content) VALUES (new.id, new.text_content);
		INSERT INTO messages_fts_code(rowid, text_content) VALUES (new.id, new.text_content);
	END;

	CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
		DELETE FROM messages_fts WHERE rowid = old.id;
		DELETE FROM messages_fts_code WHERE rowid = old.id;
	END;

	CREATE TRIGGER IF NOT EXISTS messages_au AFTER UPDATE ON messages BEGIN
		UPDATE messages_fts SET text_content = new.text_content WHERE rowid = new.id;
		UPDATE messages_fts_code SET text_content = new.text_content WHERE rowid = new.id;
	END;
	`

	_, err := db.conn.Exec(schema)
	return err
}
