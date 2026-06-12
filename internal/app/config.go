package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/meshcore-go/meshcore-bot/internal/config"
	"github.com/meshcore-go/meshcore-bot/internal/store"
)

// The database is the source of truth for config; files are one-time imports.

func saveConfig(db *store.Store, cfg *config.Config) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	var werr error
	db.WriteSync(func() { werr = db.AppConfig.Set(string(data)) })
	if werr != nil {
		return fmt.Errorf("storing config: %w", werr)
	}
	return nil
}

func loadConfigFromDB(db *store.Store) (*config.Config, error) {
	raw, err := db.AppConfig.Get()
	if err != nil {
		return nil, err
	}
	cfg, err := config.UnmarshalConfigJson([]byte(raw))
	if err != nil {
		return nil, fmt.Errorf("parsing stored config: %w", err)
	}
	return cfg, nil
}

// resolveConfig determines the active config at startup: an explicit -config
// flag imports (and overwrites) into the database; otherwise the database
// wins; a first run imports a default-named file from the working directory,
// or bootstraps a default config with one generated companion.
func resolveConfig(db *store.Store, importPath string) (*config.Config, error) {
	if importPath != "" {
		return importConfigFile(db, importPath)
	}

	cfg, err := loadConfigFromDB(db)
	if err == nil {
		if verr := cfg.Validate(); verr != nil {
			return nil, fmt.Errorf("stored config invalid: %w", verr)
		}
		return cfg, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("loading config from database: %w", err)
	}

	if path := config.FindDefaultConfig(); path != "" {
		return importConfigFile(db, path)
	}

	def := config.DefaultConfig()
	cfg = &def
	// Leave Companions empty and SetupComplete false: the app boots quietly
	// (radio observes, nothing adverts) and the web UI shows the first-run
	// wizard. No fabricated identity flooding the mesh.
	if err := saveConfig(db, cfg); err != nil {
		return nil, err
	}
	slog.Info("no config found; bootstrapped a quiet default, complete setup in the web UI")
	return cfg, nil
}

func importConfigFile(db *store.Store, path string) (*config.Config, error) {
	cfg, resolved, err := config.LoadFromPath(path)
	if err != nil {
		return nil, err
	}
	if err := cfg.MigrateKeyFiles(filepath.Dir(resolved)); err != nil {
		return nil, err
	}
	if err := cfg.EnsureCompanionKeys(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", resolved, err)
	}
	// Importing a file is a deliberate configuration step, so skip the wizard.
	complete := true
	cfg.SetupComplete = &complete
	if err := saveConfig(db, cfg); err != nil {
		return nil, err
	}
	slog.Info("imported config file into database; the file is no longer read at runtime", "path", resolved)
	return cfg, nil
}

// inheritCompanionKeys fills missing privateKeys from the previous config by
// companion name, so a PUT that omits keys (or a UI bug) can never silently
// rotate a running identity. Genuinely new companions get fresh keys from
// EnsureCompanionKeys afterwards.
func inheritCompanionKeys(next, prev *config.Config) {
	if prev == nil {
		return
	}
	byName := make(map[string]string, len(prev.Companions))
	for _, c := range prev.Companions {
		if c.PrivateKey != "" {
			byName[c.Name] = c.PrivateKey
		}
	}
	for i := range next.Companions {
		if next.Companions[i].PrivateKey == "" {
			next.Companions[i].PrivateKey = byName[next.Companions[i].Name]
		}
	}
}
