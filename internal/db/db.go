// Package db opens the application database and applies schema migrations.
//
// Two engines are supported. The driver is chosen from the DSN: a postgres://
// or postgresql:// URL uses lib/pq, anything else is a SQLite file path handled
// by modernc.org/sqlite (pure Go, no cgo).
//
// Because placeholder syntax differs between the two, every query in this
// repository is written with `?` placeholders and passed through DB.Rebind
// before use. Do not write `$1` directly.
package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jmoiron/sqlx"

	// Database drivers.
	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

//go:embed migrations/sqlite/*.sql migrations/postgres/*.sql
var migrationFS embed.FS

// Dialect identifies which SQL flavour a DB is speaking.
type Dialect string

const (
	// SQLite is the default, file-backed engine.
	SQLite Dialect = "sqlite"
	// Postgres is used when the DSN is a postgres:// URL.
	Postgres Dialect = "postgres"
)

// DB wraps sqlx.DB with the dialect it was opened against.
type DB struct {
	*sqlx.DB
	Dialect Dialect
}

// DialectFor reports which engine a DSN targets.
func DialectFor(dsn string) Dialect {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		return Postgres
	}
	return SQLite
}

// Open connects to the database named by dsn and verifies the connection.
func Open(ctx context.Context, dsn string) (*DB, error) {
	dialect := DialectFor(dsn)

	driver, target := string(dialect), dsn
	if dialect == SQLite {
		// Foreign keys are off by default in SQLite and must be enabled per
		// connection; the pragma travels in the DSN so pooled connections get it.
		if !strings.Contains(target, "_pragma=") {
			sep := "?"
			if strings.Contains(target, "?") {
				sep = "&"
			}
			target += sep + "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
		}
	}

	handle, err := sqlx.ConnectContext(ctx, driver, target)
	if err != nil {
		return nil, fmt.Errorf("connect to %s database: %w", dialect, err)
	}

	if dialect == SQLite {
		// A file-backed SQLite database serialises writes; more than one open
		// connection just produces SQLITE_BUSY under concurrent writes.
		handle.SetMaxOpenConns(1)
	}

	return &DB{DB: handle, Dialect: dialect}, nil
}

// Migrate applies any migrations newer than the recorded schema version.
func (d *DB) Migrate(ctx context.Context) error {
	if _, err := d.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`,
	); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	var current int
	if err := d.GetContext(ctx, &current,
		`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`,
	); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	migrations, err := loadMigrations(d.Dialect)
	if err != nil {
		return err
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if err := d.applyMigration(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

func (d *DB) applyMigration(ctx context.Context, m migration) error {
	tx, err := d.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %d: %w", m.version, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("apply migration %d (%s): %w", m.version, m.name, err)
	}
	if _, err := tx.ExecContext(ctx,
		d.Rebind(`INSERT INTO schema_migrations (version) VALUES (?)`), m.version,
	); err != nil {
		return fmt.Errorf("record migration %d: %w", m.version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %d: %w", m.version, err)
	}
	return nil
}

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations(dialect Dialect) ([]migration, error) {
	dir := filepath.Join("migrations", string(dialect))
	entries, err := fs.ReadDir(migrationFS, dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations for %s: %w", dialect, err)
	}

	var out []migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		// Files are named NNN_description.sql; the numeric prefix is the version.
		prefix, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("migration %q is missing a version prefix", e.Name())
		}
		var version int
		if _, err := fmt.Sscanf(prefix, "%d", &version); err != nil {
			return nil, fmt.Errorf("migration %q has an unparseable version: %w", e.Name(), err)
		}
		body, err := fs.ReadFile(migrationFS, filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", e.Name(), err)
		}
		out = append(out, migration{version: version, name: e.Name(), sql: string(body)})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}
