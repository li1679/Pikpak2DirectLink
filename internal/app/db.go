package app

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const (
	diskDatabaseMaxOpenConns = 8
	diskDatabaseMaxIdleConns = 4
)

// openDatabase opens (creating if needed) the application SQLite database and
// applies its schema. modernc.org/sqlite is a pure-Go driver, so the binary
// still cross-compiles with CGO disabled.
func openDatabase(path string) (*sql.DB, error) {
	if path != ":memory:" {
		if dir := filepath.Dir(path); dir != "." && dir != "" {
			if err := durableMkdirAll(dir, 0o700); err != nil {
				return nil, err
			}
		}
		if err := prepareDatabaseFile(path); err != nil {
			return nil, err
		}
	}

	dsn, err := sqliteDatabaseDSN(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if path == ":memory:" {
		// Each :memory: connection is a different database, so it must stay on
		// the single connection used to create the schema.
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	} else {
		db.SetMaxOpenConns(diskDatabaseMaxOpenConns)
		db.SetMaxIdleConns(diskDatabaseMaxIdleConns)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	if err := initializeDatabase(db); err != nil {
		db.Close()
		return nil, err
	}
	if path != ":memory:" {
		if err := hardenSQLiteFileModes(path); err != nil {
			db.Close()
			return nil, err
		}
	}
	return db, nil
}

func prepareDatabaseFile(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("prepare database file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("secure database file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close prepared database file: %w", err)
	}
	return nil
}

func hardenSQLiteFileModes(path string) error {
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(candidate, 0o600); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("secure SQLite file %s: %w", candidate, err)
		}
	}
	return nil
}

// sqliteDatabaseDSN builds a URI instead of appending query parameters to the
// supplied filename. This keeps characters such as '?' and '#' in a database
// filename from being interpreted as part of the DSN. The modernc driver
// applies each _pragma whenever it opens a pooled connection.
func sqliteDatabaseDSN(path string) (string, error) {
	query := make(url.Values)
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "synchronous(FULL)")
	query.Set("_txlock", "immediate")

	if path == ":memory:" {
		return path + "?" + query.Encode(), nil
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("absolute database path: %w", err)
	}
	uriPath := filepath.ToSlash(absPath)
	if filepath.VolumeName(absPath) != "" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	query.Add("_pragma", "journal_mode(WAL)")
	u := url.URL{
		Scheme:   "file",
		Path:     uriPath,
		RawQuery: query.Encode(),
	}
	return u.String(), nil
}
