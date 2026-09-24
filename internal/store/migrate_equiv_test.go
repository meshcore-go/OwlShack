package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type equivRec struct {
	inner dbExecer
	sql   []string
}

func (r *equivRec) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	r.sql = append(r.sql, q)
	return r.inner.ExecContext(ctx, q, args...)
}

func (r *equivRec) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return r.inner.QueryContext(ctx, q, args...)
}

// normSQL drops comment lines, statement separators and layout, leaving only the SQL's tokens.
func normSQL(s string) string {
	var keep []string
	for _, l := range strings.Split(s, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "--") {
			keep = append(keep, l)
		}
	}
	return strings.Join(strings.Fields(strings.ReplaceAll(strings.Join(keep, "\n"), ";", " ")), " ")
}

// schemaOf is every schema object with its SQL, as SQLite stores it with the author's layout.
func schemaOf(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT type || ' ' || name || ' ' || COALESCE(sql, '') FROM sqlite_master ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, normSQL(s))
	}
	return out
}

// Each file must run what its slot's function ran, statement for statement, before the functions go.
func TestMigrationFiles_MatchTheFunctions(t *testing.T) {
	files, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(files)
	if len(files) != len(migrations) {
		t.Fatalf("%d files for %d slots", len(files), len(migrations))
	}

	old, err := openWritableDB(filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	fresh, err := openWritableDB(filepath.Join(t.TempDir(), "files.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()

	for i, name := range files {
		if want := fmt.Sprintf("migrations/%03d_", i+1); !strings.HasPrefix(name, want) {
			t.Fatalf("file %d is %s, want a %s prefix", i+1, name, want)
		}
		rec := &equivRec{inner: old}
		if err := migrations[i](t.Context(), rec); err != nil {
			t.Fatalf("slot %d function: %v", i+1, err)
		}
		body, err := fs.ReadFile(migrationFiles, name)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := normSQL(string(body)), normSQL(strings.Join(rec.sql, "\n")); got != want {
			t.Errorf("%s differs from slot %d's function:\n file: %s\n func: %s", name, i+1, got, want)
		}
		if _, err := fresh.ExecContext(t.Context(), string(body)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if a, b := schemaOf(t, old), schemaOf(t, fresh); !slices.Equal(a, b) {
		t.Errorf("the files build a different schema:\n funcs: %q\n files: %q", a, b)
	}
}
