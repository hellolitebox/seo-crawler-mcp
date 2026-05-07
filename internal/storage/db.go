package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DB wraps a *sql.DB with migration support.
type DB struct {
	*sql.DB
}

const maxSQLiteConnections = 16

func init() {
	sqlite.RegisterConnectionHook(func(conn sqlite.ExecQuerierContext, _ string) error {
		for _, p := range sqlitePragmas {
			if _, err := conn.ExecContext(context.Background(), p, []driver.NamedValue{}); err != nil {
				return fmt.Errorf("executing connection pragma %q: %w", p, err)
			}
		}
		return nil
	})
}

// Open creates or opens a SQLite database at path, applies pragmas, and runs
// any pending migrations. The database uses WAL mode for concurrent reads.
func Open(path string) (*DB, error) {
	dsn := fmt.Sprintf(
		"file:%s?_txlock=immediate&_busy_timeout=5000",
		path,
	)

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database %q: %w", path, err)
	}

	// WAL permits many concurrent readers with one writer. Keep enough DB
	// connections for status/report requests to stay responsive while the crawl
	// engine is writing. Connection hooks below apply required pragmas to every
	// SQLite connection, not just the first one.
	sqlDB.SetMaxOpenConns(maxSQLiteConnections)
	sqlDB.SetMaxIdleConns(maxSQLiteConnections)

	if err := setPragmas(sqlDB); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("setting pragmas on %q: %w", path, err)
	}

	db := &DB{DB: sqlDB}

	if err := db.migrate(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("running migrations on %q: %w", path, err)
	}

	return db, nil
}

var sqlitePragmas = []string{
	"PRAGMA journal_mode=WAL",
	"PRAGMA synchronous=NORMAL",
	"PRAGMA cache_size=-64000",
	"PRAGMA temp_store=MEMORY",
	"PRAGMA foreign_keys=ON",
	"PRAGMA busy_timeout=5000",
}

func setPragmas(db *sql.DB) error {
	for _, p := range sqlitePragmas {
		if _, err := db.Exec(p); err != nil {
			return fmt.Errorf("executing %q: %w", p, err)
		}
	}
	return nil
}

// migrate reads embedded SQL files and applies any that haven't been run yet.
func (db *DB) migrate() error {
	// Ensure schema_migrations exists before querying it.
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	)`)
	if err != nil {
		return fmt.Errorf("creating schema_migrations: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("reading migrations dir: %w", err)
	}

	type migration struct {
		Version int
		Name    string
	}

	migrations := make([]migration, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		ver, parseErr := extractVersion(e.Name())
		if parseErr != nil {
			return fmt.Errorf("parsing migration filename %q: %w", e.Name(), parseErr)
		}
		migrations = append(migrations, migration{
			Version: ver,
			Name:    e.Name(),
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	for _, m := range migrations {
		var applied int
		err := db.QueryRow(
			"SELECT COUNT(*) FROM schema_migrations WHERE version = ?",
			m.Version,
		).Scan(&applied)
		if err != nil {
			return fmt.Errorf("checking migration version %d: %w", m.Version, err)
		}
		if applied > 0 {
			continue
		}

		content, readErr := migrationsFS.ReadFile(
			filepath.Join("migrations", m.Name),
		)
		if readErr != nil {
			return fmt.Errorf("reading migration %q: %w", m.Name, readErr)
		}

		tx, txErr := db.Begin()
		if txErr != nil {
			return fmt.Errorf("beginning transaction for migration %d: %w", m.Version, txErr)
		}

		if _, execErr := tx.Exec(string(content)); execErr != nil {
			tx.Rollback()
			return fmt.Errorf("executing migration %q: %w", m.Name, execErr)
		}

		if _, execErr := tx.Exec(
			"INSERT INTO schema_migrations (version) VALUES (?)",
			m.Version,
		); execErr != nil {
			tx.Rollback()
			return fmt.Errorf("recording migration %d: %w", m.Version, execErr)
		}

		if commitErr := tx.Commit(); commitErr != nil {
			return fmt.Errorf("committing migration %d: %w", m.Version, commitErr)
		}
	}

	return nil
}

// extractVersion parses the leading numeric prefix from a migration filename.
// For example, "001_init.sql" returns 1.
func extractVersion(filename string) (int, error) {
	base := strings.TrimSuffix(filename, ".sql")
	parts := strings.SplitN(base, "_", 2)
	if len(parts) == 0 {
		return 0, fmt.Errorf("no version prefix in %q", filename)
	}
	ver, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("invalid version prefix %q in %q: %w", parts[0], filename, err)
	}
	return ver, nil
}
