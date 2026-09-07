package app

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/meshcore-go/OwlShack/internal/config"
	"github.com/meshcore-go/OwlShack/internal/store"
)

// The database is the source of truth for config; files are one-time imports.

func saveConfig(ctx context.Context, db *store.Store, cfg *config.Config) error {
	return persistToTables(ctx, db, cfg)
}

func loadConfigFromDB(ctx context.Context, db *store.Store) (*config.Config, error) {
	return readConfigFromTables(ctx, db)
}

// resolveConfig imports and overwrites from an explicit -config flag; otherwise the database wins.
func resolveConfig(ctx context.Context, db *store.Store, importPath string) (*config.Config, error) {
	if importPath != "" {
		return importConfigFile(ctx, db, importPath)
	}
	cfg, err := initConfigTables(ctx, db)
	if err != nil {
		return nil, err
	}
	// A restored database can arrive with keys stripped; mint before Validate, which reads two blank keys as duplicates.
	if err := mintMissingKeys(ctx, db, cfg); err != nil {
		return nil, err
	}
	if verr := cfg.Validate(); verr != nil {
		return nil, fmt.Errorf("stored config invalid: %w", verr)
	}
	return cfg, nil
}

// mintMissingKeys persists identities for stored nodes that have none, so they survive a restart.
func mintMissingKeys(ctx context.Context, db *store.Store, cfg *config.Config) error {
	missing := 0
	for _, c := range cfg.Companions {
		if c.PrivateKey == "" {
			missing++
		}
	}
	if cfg.Repeater != nil && cfg.Repeater.PrivateKey == "" {
		missing++
	}
	if missing == 0 {
		return nil
	}
	if err := cfg.EnsureNodeKeys(); err != nil {
		return err
	}
	if err := persistToTables(ctx, db, cfg); err != nil {
		return fmt.Errorf("persisting generated identities: %w", err)
	}
	slog.Warn("generated missing node identities; this node has a new address on the mesh", "count", missing)
	return nil
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
	// Re-read from the tables for the surrogate companion ids the runtime keys history on; the parsed file has none.
	return readConfigFromTables(ctx, db)
}
