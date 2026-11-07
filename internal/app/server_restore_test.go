package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewServerRecoversInterruptedPersonalRestoreBeforeOpeningDatabase(t *testing.T) {
	const restoreID = "0123456789abcdef01234567"
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "committed"}[committed], func(t *testing.T) {
			dir := t.TempDir()
			databasePath := filepath.Join(dir, "personal.db")
			stagedPath := filepath.Join(dir, ".personal.db.restore-new-"+restoreID)
			writeRestoreTestDatabase(t, databasePath, "original")
			writeRestoreTestDatabase(t, stagedPath, "restored")
			operations := []restoreOperation{{kind: "database", target: databasePath, staged: stagedPath}}
			journalPath, err := prepareRestoreJournal(databasePath, operations, restoreID, "safety", restoreJournalModeDatabase)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(databasePath, operations[0].rollback); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(stagedPath, databasePath); err != nil {
				t.Fatal(err)
			}
			if committed {
				if err := markRestoreJournalCommitted(journalPath, restoreID); err != nil {
					t.Fatal(err)
				}
			}
			srv, err := NewServer(restoreStartupTestConfig(databasePath))
			if err != nil {
				t.Fatalf("NewServer: %v", err)
			}
			t.Cleanup(func() {
				if err := srv.Close(); err != nil {
					t.Error(err)
				}
			})
			var got string
			if err := srv.db.QueryRow(`SELECT value FROM restore_marker`).Scan(&got); err != nil {
				t.Fatal(err)
			}
			want := "original"
			if committed {
				want = "restored"
			}
			if got != want {
				t.Errorf("opened database marker=%q, want %q", got, want)
			}
			for _, artifact := range []string{journalPath, restoreJournalCommitPath(journalPath), operations[0].rollback, stagedPath} {
				if _, err := os.Stat(artifact); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("restore artifact remains: %s: %v", artifact, err)
				}
			}
		})
	}
}

func TestNewServerStopsBeforeDatabaseCreationWhenRestoreJournalIsInvalid(t *testing.T) {
	dir := t.TempDir()
	databasePath := filepath.Join(dir, "personal.db")
	if err := os.WriteFile(filepath.Join(dir, restoreJournalFilename), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(restoreStartupTestConfig(databasePath))
	if srv != nil {
		srv.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "restore journal") {
		t.Fatalf("startup error=%v, want invalid restore journal", err)
	}
	if _, err := os.Stat(databasePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("database was created before recovery: %v", err)
	}
	lock, err := acquireStorageFileLock(databasePath)
	if err != nil {
		t.Fatalf("storage lock leaked after failed startup: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
}

func restoreStartupTestConfig(databasePath string) Config {
	return Config{Addr: "127.0.0.1:51873", DBFile: databasePath, RootFolderName: "TestRoot",
		DataEncryptionKey: testDataEncryptionKey, RequestTimeout: time.Second,
		QueueTimeout: time.Minute, ParallelTimeout: time.Minute, ResolveConcurrency: 1}
}
