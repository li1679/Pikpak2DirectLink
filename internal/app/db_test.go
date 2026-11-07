package app

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPersonalDatabaseRefusesLegacyAndForeignSchemas(t *testing.T) {
	for _, ddl := range []string{`CREATE TABLE users(id TEXT)`, `CREATE TABLE schema_migrations(version INTEGER)`, `CREATE TABLE foreign_data(value TEXT)`} {
		t.Run(ddl, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "existing.db")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ddl); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if db, err := openDatabase(path); err == nil {
				db.Close()
				t.Fatal("existing database was accepted")
			}
			db, err = sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table'`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("rejected database was modified: count=%d err=%v", count, err)
			}
		})
	}
}

func TestOpenDatabaseCreatesPrivateDirectoryAndDatabase(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix permission bits")
	}
	root := t.TempDir()
	directory := filepath.Join(root, "private", "nested")
	path := filepath.Join(directory, "app.db")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatalf("openDatabase: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	directoryInfo, err := os.Stat(directory)
	if err != nil {
		t.Fatalf("stat database directory: %v", err)
	}
	if got := directoryInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("database directory mode = %o, want 700", got)
	}
	databaseInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat database file: %v", err)
	}
	if got := databaseInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("database file mode = %o, want 600", got)
	}
}

func TestHardenSQLiteFileModesSecuresExistingSidecars(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix permission bits")
	}
	root := t.TempDir()
	path := filepath.Join(root, "app.db")
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.WriteFile(candidate, []byte("test"), 0o666); err != nil {
			t.Fatalf("write %s: %v", candidate, err)
		}
		if err := os.Chmod(candidate, 0o666); err != nil {
			t.Fatalf("chmod %s: %v", candidate, err)
		}
	}
	if err := hardenSQLiteFileModes(path); err != nil {
		t.Fatalf("hardenSQLiteFileModes: %v", err)
	}
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(candidate)
		if err != nil {
			t.Fatalf("stat %s: %v", candidate, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("%s mode = %o, want 600", candidate, got)
		}
	}
}
