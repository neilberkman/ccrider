package db

// migrate runs any needed migrations for existing databases
func (db *DB) migrate() error {
	// Add llm_summary columns if they don't exist
	// SQLite doesn't have IF NOT EXISTS for ALTER TABLE, so we check first
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
