package store

import "embed"

// migrationFiles holds one file per user_version, named NNN_what.sql; a shipped file is frozen by migrations.sum.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS
