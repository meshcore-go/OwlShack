package store

import (
	"context"
	"database/sql"
	"fmt"
)

// AppConfigRepo persists the bot's configuration as a single JSON document —
// the database is the source of truth; config files are one-time imports.
type AppConfigRepo struct {
	db *sql.DB
}

// Get returns the stored config JSON, or sql.ErrNoRows when none is set.
func (r *AppConfigRepo) Get(ctx context.Context) (string, error) {
	var cfg string
	err := r.db.QueryRowContext(ctx, `SELECT config FROM app_config WHERE id = 1`).Scan(&cfg)
	if err != nil {
		return cfg, fmt.Errorf("getting app config: %w", err)
	}
	return cfg, nil
}

// Set stores the config JSON. Call inside WriteSync/WriteAsync.
func (r *AppConfigRepo) Set(ctx context.Context, cfgJSON string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO app_config (id, config, updated_at) VALUES (1, ?, CURRENT_TIMESTAMP)
		ON CONFLICT (id) DO UPDATE SET config = excluded.config, updated_at = CURRENT_TIMESTAMP`,
		cfgJSON)
	if err != nil {
		return fmt.Errorf("setting app config: %w", err)
	}
	return nil
}
