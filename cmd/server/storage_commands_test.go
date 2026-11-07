package main

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pikpak2directlink/internal/app"
)

func TestRunStorageCommandOnlyHandlesStoragePrefix(t *testing.T) {
	handled, err := runStorageCommandWithOutput(nil, app.Config{}, &bytes.Buffer{})
	if handled || err != nil {
		t.Fatalf("empty args handled=%v err=%v", handled, err)
	}
	handled, err = runStorageCommandWithOutput([]string{"serve"}, app.Config{}, &bytes.Buffer{})
	if handled || err != nil {
		t.Fatalf("serve args handled=%v err=%v", handled, err)
	}
}

func TestRunStorageCommandRequiresExplicitConfirmation(t *testing.T) {
	handled, err := runStorageCommandWithOutput(
		[]string{"storage", "restore-db", "--backup", "backup.db"},
		app.Config{DBFile: "target.db"},
		&bytes.Buffer{},
	)
	if !handled || err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
}

func TestRunStorageCommandRestoresDatabase(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.db")
	backup := filepath.Join(dir, "backup.db")
	writeCommandTestDatabase(t, target, "current")
	writeCommandTestDatabase(t, backup, "backup")

	var output bytes.Buffer
	handled, err := runStorageCommandWithOutput(
		[]string{"storage", "restore-db", "--backup", backup, "--yes"},
		app.Config{DBFile: target},
		&output,
	)
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if !strings.Contains(output.String(), "storage restore completed") || !strings.Contains(output.String(), "safety backup") {
		t.Fatalf("command output = %q", output.String())
	}

	db, err := sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err := db.QueryRow(`SELECT value FROM marker`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "backup" {
		t.Fatalf("restored value = %q", value)
	}
}

func writeCommandTestDatabase(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	server, err := app.NewServer(app.Config{Addr: "127.0.0.1:51873", DBFile: path, BackupDir: filepath.Join(filepath.Dir(path), "backups"), DataEncryptionKey: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE marker(value TEXT NOT NULL)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO marker(value) VALUES(?)`, value); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRunStorageCommandRejectsRemovedMigrationCommand(t *testing.T) {
	handled, err := runStorageCommandWithOutput([]string{"storage", "restore-migration", "--backup", "old", "--yes"}, app.Config{}, &bytes.Buffer{})
	if !handled || err == nil || !strings.Contains(err.Error(), "unknown storage command") {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
}
