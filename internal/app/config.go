package app

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/meshcore-go/meshcore-bot/internal/config"
	"github.com/meshcore-go/meshcore-bot/internal/store"
)

// The database is the source of truth for config; files are one-time imports.
// Config is stored relationally (the internal/store config_* tables);
// the assemble/disassemble seam lives in config_tables.go.

func saveConfig(ctx context.Context, db *store.Store, cfg *config.Config) error {
	return persistToTables(ctx, db, cfg)
}

func loadConfigFromDB(ctx context.Context, db *store.Store) (*config.Config, error) {
	return readConfigFromTables(ctx, db)
}

// resolveConfig determines the active config at startup: an explicit -config
// flag imports (and overwrites) into the database; otherwise the database wins.
// initConfigTables handles first-run population (migrate the legacy JSON blob,
// import a default-named file, or bootstrap a quiet default).
func resolveConfig(ctx context.Context, db *store.Store, importPath string) (*config.Config, error) {
	if importPath != "" {
		return importConfigFile(ctx, db, importPath)
	}
	cfg, err := initConfigTables(ctx, db)
	if err != nil {
		return nil, err
	}
	if verr := cfg.Validate(); verr != nil {
		return nil, fmt.Errorf("stored config invalid: %w", verr)
	}
	return cfg, nil
}

func importConfigFile(ctx context.Context, db *store.Store, path string) (*config.Config, error) {
	cfg, resolved, err := config.LoadFromPath(path)
	if err != nil {
		return nil, err
	}
	if err := cfg.MigrateKeyFiles(filepath.Dir(resolved)); err != nil {
		return nil, err
	}
	if err := cfg.EnsureNodeKeys(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", resolved, err)
	}
	// Importing a file is a deliberate configuration step, so skip the wizard.
	complete := true
	cfg.SetupComplete = &complete
	if err := saveConfig(ctx, db, cfg); err != nil {
		return nil, err
	}
	slog.Info("imported config file into database; the file is no longer read at runtime", "path", resolved)
	// Re-read from the tables so the returned config carries the surrogate
	// companion ids the runtime keys history on (the parsed file has none).
	return readConfigFromTables(ctx, db)
}
