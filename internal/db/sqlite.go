package db

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Open opens a SQLite database file. Per-connection pragmas are set via DSN
// query params: WAL journal mode, a 5s busy timeout (writers queue instead
// of failing under contention between Go and dev tooling), and foreign
// keys. Per-connection pragmas (including foreign_keys) cannot be
// connection-pool-faithful via a one-shot Exec, so they go in the DSN.
func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)",
		path,
	)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to ping sqlite database: %w", err)
	}

	return db, nil
}
