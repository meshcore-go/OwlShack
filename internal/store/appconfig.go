package store

import (
	"database/sql"
)

// AppConfigRepo persists the bot's configuration as a single JSON document —
// the database is the source of truth; config files are one-time imports.
type AppConfigRepo struct {
	db *sql.DB
}

// Get returns the stored config JSON, or sql.ErrNoRows when none is set.
func (r *AppConfigRepo) Get() (string, error) {
	var cfg string
	err := r.db.QueryRow(`SELECT config FROM app_config WHERE id = 1`).Scan(&cfg)
	return cfg, err
}

// Set stores the config JSON. Call inside WriteSync/WriteAsync.
func (r *AppConfigRepo) Set(cfgJSON string) error {
	_, err := r.db.Exec(`
		INSERT INTO app_config (id, config, updated_at) VALUES (1, ?, CURRENT_TIMESTAMP)
		ON CONFLICT (id) DO UPDATE SET config = excluded.config, updated_at = CURRENT_TIMESTAMP`,
		cfgJSON)
	return err
}
