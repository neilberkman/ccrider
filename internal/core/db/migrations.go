package db

import (
	"database/sql"
	"fmt"
)

// runMigrations applies database migrations for existing databases
func (db *DB) runMigrations() error {
	// Migration 1: Add llm_models table (Phase 1 - LLM integration)
	if err := db.migration001AddLLMModels(); err != nil {
		return fmt.Errorf("migration 001: %w", err)
	}

	// Migration 2: Add model_id and one_line_summary to session_summaries
	if err := db.migration002UpdateSummaries(); err != nil {
		return fmt.Errorf("migration 002: %w", err)
	}

	// Migration 3: Add model_id to summary_chunks
	if err := db.migration003UpdateChunks(); err != nil {
		return fmt.Errorf("migration 003: %w", err)
	}

	return nil
}

// migration001AddLLMModels creates llm_models table if it doesn't exist
func (db *DB) migration001AddLLMModels() error {
	// Check if table exists
	var tableName string
	err := db.conn.QueryRow(`
		SELECT name FROM sqlite_master
		WHERE type='table' AND name='llm_models'
	`).Scan(&tableName)

	if err == sql.ErrNoRows {
		// Table doesn't exist, create it
		_, err = db.conn.Exec(`
			CREATE TABLE llm_models (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				model_name TEXT UNIQUE NOT NULL,
				model_size_gb REAL,
				description TEXT,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);

			INSERT INTO llm_models (model_name, model_size_gb, description) VALUES
				('qwen-1.5b', 1.0, 'Qwen 2.5 1.5B Instruct - Fast, lightweight model'),
				('llama-8b', 4.6, 'Llama 3.1 8B Instruct - Higher quality, slower');
		`)
		return err
	}

	return err
}

// migration002UpdateSummaries adds model_id and one_line_summary columns
func (db *DB) migration002UpdateSummaries() error {
	// Check if session_summaries table exists
	var tableName string
	err := db.conn.QueryRow(`
		SELECT name FROM sqlite_master
		WHERE type='table' AND name='session_summaries'
	`).Scan(&tableName)

	if err == sql.ErrNoRows {
		// Table doesn't exist yet, will be created by initSchema
		return nil
	}
	if err != nil {
		return err
	}

	// Check if model_id column exists
	var hasModelID bool
	err = db.conn.QueryRow(`
		SELECT COUNT(*) FROM pragma_table_info('session_summaries')
		WHERE name='model_id'
	`).Scan(&hasModelID)
	if err != nil {
		return err
	}

	if !hasModelID {
		// Add model_id column
		// First, ensure we have a default model
		var defaultModelID int64
		err = db.conn.QueryRow(`SELECT id FROM llm_models WHERE model_name = 'qwen-1.5b'`).Scan(&defaultModelID)
		if err != nil {
			return fmt.Errorf("get default model: %w", err)
		}

		// Add column with default value
		_, err = db.conn.Exec(fmt.Sprintf(`
			ALTER TABLE session_summaries ADD COLUMN model_id INTEGER NOT NULL DEFAULT %d;
		`, defaultModelID))
		if err != nil {
			return fmt.Errorf("add model_id column: %w", err)
		}
	}

	// Always ensure index exists (safe to run even if column existed before)
	_, err = db.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_summaries_model ON session_summaries(model_id);`)
	if err != nil {
		// Ignore error if column doesn't exist (schema will create index later)
		// This can happen on fresh databases
	}

	// Check if one_line_summary column exists
	var hasOneLiner bool
	err = db.conn.QueryRow(`
		SELECT COUNT(*) FROM pragma_table_info('session_summaries')
		WHERE name='one_line_summary'
	`).Scan(&hasOneLiner)
	if err != nil {
		return err
	}

	if !hasOneLiner {
		// Add one_line_summary column
		_, err = db.conn.Exec(`ALTER TABLE session_summaries ADD COLUMN one_line_summary TEXT;`)
		if err != nil {
			return fmt.Errorf("add one_line_summary column: %w", err)
		}

		// For existing summaries, generate one-liner from full_summary (first 15 words)
		_, err = db.conn.Exec(`
			UPDATE session_summaries
			SET one_line_summary = SUBSTR(full_summary, 1, 100)
			WHERE one_line_summary IS NULL AND full_summary IS NOT NULL;
		`)
		if err != nil {
			return fmt.Errorf("populate one_line_summary: %w", err)
		}
	}

	// Rename summary_text to full_summary if needed
	var hasSummaryText bool
	err = db.conn.QueryRow(`
		SELECT COUNT(*) FROM pragma_table_info('session_summaries')
		WHERE name='summary_text'
	`).Scan(&hasSummaryText)
	if err != nil {
		return err
	}

	var hasFullSummary bool
	err = db.conn.QueryRow(`
		SELECT COUNT(*) FROM pragma_table_info('session_summaries')
		WHERE name='full_summary'
	`).Scan(&hasFullSummary)
	if err != nil {
		return err
	}

	if hasSummaryText && !hasFullSummary {
		// SQLite doesn't support RENAME COLUMN directly in older versions
		// Create new column and copy data
		_, err = db.conn.Exec(`ALTER TABLE session_summaries ADD COLUMN full_summary TEXT;`)
		if err != nil {
			return fmt.Errorf("add full_summary column: %w", err)
		}

		_, err = db.conn.Exec(`UPDATE session_summaries SET full_summary = summary_text;`)
		if err != nil {
			return fmt.Errorf("copy summary_text to full_summary: %w", err)
		}

		// Note: We can't drop the old column easily in SQLite, but it's okay to leave it
	}

	return nil
}

// migration003UpdateChunks adds model_id column to summary_chunks
func (db *DB) migration003UpdateChunks() error {
	// Check if summary_chunks table exists
	var tableName string
	err := db.conn.QueryRow(`
		SELECT name FROM sqlite_master
		WHERE type='table' AND name='summary_chunks'
	`).Scan(&tableName)

	if err == sql.ErrNoRows {
		// Table doesn't exist yet, will be created by initSchema
		return nil
	}
	if err != nil {
		return err
	}

	// Check if model_id column exists
	var hasModelID bool
	err = db.conn.QueryRow(`
		SELECT COUNT(*) FROM pragma_table_info('summary_chunks')
		WHERE name='model_id'
	`).Scan(&hasModelID)
	if err != nil {
		return err
	}

	if !hasModelID {
		// Add model_id column
		var defaultModelID int64
		err = db.conn.QueryRow(`SELECT id FROM llm_models WHERE model_name = 'qwen-1.5b'`).Scan(&defaultModelID)
		if err != nil {
			return fmt.Errorf("get default model: %w", err)
		}

		// Add column with default value
		_, err = db.conn.Exec(fmt.Sprintf(`
			ALTER TABLE summary_chunks ADD COLUMN model_id INTEGER NOT NULL DEFAULT %d;
		`, defaultModelID))
		if err != nil {
			return fmt.Errorf("add model_id column to chunks: %w", err)
		}
	}

	return nil
}
