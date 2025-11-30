package db

// migrate runs any needed migrations for existing databases
func (db *DB) migrate() error {
	// Migration 1: Add llm_summary columns to sessions table
	if err := db.migration001AddLLMSummaryColumns(); err != nil {
		return err
	}

	// Migration 2: Create hierarchical summarization tables
	if err := db.migration002CreateSummaryTables(); err != nil {
		return err
	}

	return nil
}

// migration001AddLLMSummaryColumns adds basic llm_summary columns to sessions
func (db *DB) migration001AddLLMSummaryColumns() error {
	var count int
	err := db.conn.QueryRow(`
		SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name='llm_summary'
	`).Scan(&count)
	if err != nil {
		return err
	}

	if count == 0 {
		_, err = db.conn.Exec(`ALTER TABLE sessions ADD COLUMN llm_summary TEXT`)
		if err != nil {
			return err
		}
		_, err = db.conn.Exec(`ALTER TABLE sessions ADD COLUMN llm_summary_at DATETIME`)
		if err != nil {
			return err
		}
	}
	return nil
}

// migration002CreateSummaryTables creates the hierarchical summarization tables
func (db *DB) migration002CreateSummaryTables() error {
	schema := `
	-- Session summaries (progressive summarization)
	CREATE TABLE IF NOT EXISTS session_summaries (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id INTEGER NOT NULL UNIQUE,
		one_line_summary TEXT,           -- Short summary for lists (10-15 words)
		full_summary TEXT,               -- Detailed summary (2-4 paragraphs)
		summary_version INTEGER DEFAULT 1, -- Increment when session extends
		last_message_count INTEGER DEFAULT 0, -- Track if session has new messages
		tokens_approx INTEGER,           -- Token count for context planning
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_session_summaries_session ON session_summaries(session_id);

	-- Summary chunks (for long sessions - progressive chunking)
	CREATE TABLE IF NOT EXISTS summary_chunks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id INTEGER NOT NULL,
		chunk_index INTEGER NOT NULL,    -- Which chunk (0, 1, 2...)
		message_start INTEGER,           -- Start message sequence
		message_end INTEGER,             -- End message sequence
		summary TEXT,                    -- Summary of this chunk
		tokens_approx INTEGER,           -- Token count
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE,
		UNIQUE(session_id, chunk_index)
	);

	CREATE INDEX IF NOT EXISTS idx_summary_chunks_session ON summary_chunks(session_id, chunk_index);

	-- Extracted issue IDs for instant lookups
	CREATE TABLE IF NOT EXISTS session_issues (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id INTEGER NOT NULL,
		issue_id TEXT NOT NULL,          -- e.g., "ENA-6530", "PROJ-1234"
		issue_id_lower TEXT NOT NULL,    -- Lowercase for case-insensitive search
		first_mention_seq INTEGER,       -- First message sequence mentioning it
		last_mention_seq INTEGER,        -- Last message sequence mentioning it
		mention_count INTEGER DEFAULT 1, -- How many times mentioned
		FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE,
		UNIQUE(session_id, issue_id_lower)
	);

	CREATE INDEX IF NOT EXISTS idx_session_issues_lookup ON session_issues(issue_id_lower);
	CREATE INDEX IF NOT EXISTS idx_session_issues_session ON session_issues(session_id);

	-- Extracted file paths for file-based lookups
	CREATE TABLE IF NOT EXISTS session_files (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id INTEGER NOT NULL,
		file_path TEXT NOT NULL,         -- Files mentioned/modified
		file_name TEXT NOT NULL,         -- Just the filename for easier search
		mention_count INTEGER DEFAULT 1, -- How many times mentioned
		first_mention_seq INTEGER,       -- First message sequence
		last_mention_seq INTEGER,        -- Last message sequence
		FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE,
		UNIQUE(session_id, file_path)
	);

	CREATE INDEX IF NOT EXISTS idx_session_files_path ON session_files(file_path);
	CREATE INDEX IF NOT EXISTS idx_session_files_name ON session_files(file_name);
	CREATE INDEX IF NOT EXISTS idx_session_files_session ON session_files(session_id);
	`

	_, err := db.conn.Exec(schema)
	return err
}
