package app

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestProxyTempCleanupStoreEncryptsFileIDs(t *testing.T) {
	db, err := openDatabase(t.TempDir() + "/cleanup.db")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	cipher := newTestSecretCipher(t, bytes.Repeat([]byte{0x39}, secretKeySize))
	store := newProxyTempCleanupStore(db, cipher)
	now := time.Date(2026, 7, 10, 8, 0, 0, 0, time.UTC)

	if err := store.record("job-encrypted", "account-1", []string{"private-file-id"}, now, now); err != nil {
		t.Fatalf("record encrypted cleanup: %v", err)
	}
	assertCleanupPayloadEncrypted(t, db, "job-encrypted", "private-file-id")

	records, err := store.due(now, 10)
	if err != nil {
		t.Fatalf("read due cleanups: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("due cleanup count = %d, want 1", len(records))
	}
}

func TestProxyTempCleanupStoreSeparatesAccountsForOneJob(t *testing.T) {
	db, err := openDatabase(t.TempDir() + "/cleanup.db")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	cipher := newTestSecretCipher(t, bytes.Repeat([]byte{0x41}, secretKeySize))
	store := newProxyTempCleanupStore(db, cipher)
	now := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)

	if err := store.record("shared-job", "account-a", []string{"file-a"}, now, now); err != nil {
		t.Fatalf("record account-a: %v", err)
	}
	if err := store.record("shared-job", "account-b", []string{"file-b"}, now, now); err != nil {
		t.Fatalf("record account-b: %v", err)
	}
	records, err := store.due(now, 10)
	if err != nil {
		t.Fatalf("read due records: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("record count = %d, want 2", len(records))
	}
	want := map[string]string{
		"account-a": cleanupRecordID("shared-job", "account-a"),
		"account-b": cleanupRecordID("shared-job", "account-b"),
	}
	for _, record := range records {
		if record.ID != want[record.AccountID] {
			t.Fatalf("record ID for %s = %q, want %q", record.AccountID, record.ID, want[record.AccountID])
		}
	}
}

func TestProxyTempCleanupStoreRejectsPlaintextPayload(t *testing.T) {
	db, err := openDatabase(t.TempDir() + "/cleanup.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := newProxyTempCleanupStore(db, newTestSecretCipher(t, bytes.Repeat([]byte{0x52}, secretKeySize)))
	now := time.Now()
	if _, err := db.Exec(`INSERT INTO proxy_temp_cleanups
		(id,job_id,account_id,file_ids_json,cleanup_after,attempts,created_at)
		VALUES(?,?,?,?,?,?,?)`, cleanupRecordID("job", "account"), "job", "account", `["private-id"]`, now.Unix(), 0, now.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.due(now, 10); err == nil {
		t.Fatal("plaintext cleanup payload was accepted")
	}
	if err := store.RotateSecrets(); err == nil {
		t.Fatal("plaintext payload was converted instead of rejected")
	}
}

func TestProxyTempCleanupStorePartiallyRemovesFileIDs(t *testing.T) {
	db, err := openDatabase(t.TempDir() + "/cleanup.db")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	cipher := newTestSecretCipher(t, bytes.Repeat([]byte{0x63}, secretKeySize))
	store := newProxyTempCleanupStore(db, cipher)
	now := time.Date(2026, 7, 10, 11, 0, 0, 0, time.UTC)
	if err := store.record("partial-job", "account-a", []string{"file-a", "file-b", "file-c"}, now, now.Add(-time.Hour)); err != nil {
		t.Fatalf("record cleanup: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin rollback transaction: %v", err)
	}
	if err := store.removeIDsByJobAccountTx(tx, "partial-job", "account-a", []string{"file-a"}); err != nil {
		t.Fatalf("remove before rollback: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback removal: %v", err)
	}
	assertCleanupIDs(t, store, now, []string{"file-a", "file-b", "file-c"})

	tx, err = db.Begin()
	if err != nil {
		t.Fatalf("begin removal transaction: %v", err)
	}
	if err := store.removeIDsByJobAccountTx(tx, "partial-job", "account-a", []string{"file-a", "missing"}); err != nil {
		t.Fatalf("remove by job/account: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit removal: %v", err)
	}
	assertCleanupIDs(t, store, now, []string{"file-b", "file-c"})

	id := cleanupRecordID("partial-job", "account-a")
	if err := store.removeRecordIDs(id, []string{"file-b"}); err != nil {
		t.Fatalf("remove by record: %v", err)
	}
	assertCleanupIDs(t, store, now, []string{"file-c"})
	if err := store.removeRecordIDs(id, []string{"file-c"}); err != nil {
		t.Fatalf("remove final ID: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM proxy_temp_cleanups WHERE id=?`, id).Scan(&count); err != nil {
		t.Fatalf("count final row: %v", err)
	}
	if count != 0 {
		t.Fatalf("remaining row count = %d, want 0", count)
	}
}

func assertCleanupPayloadEncrypted(t *testing.T, db *sql.DB, jobID, plaintext string) {
	t.Helper()
	var stored string
	if err := db.QueryRow(`SELECT file_ids_json FROM proxy_temp_cleanups WHERE job_id=?`, jobID).Scan(&stored); err != nil {
		t.Fatalf("read cleanup payload: %v", err)
	}
	if !strings.HasPrefix(stored, secretEnvelopeVersion+".") || strings.Contains(stored, plaintext) {
		t.Fatalf("cleanup payload was not encrypted: %q", stored)
	}
}

func assertCleanupIDs(t *testing.T, store *proxyTempCleanupStore, now time.Time, want []string) {
	t.Helper()
	records, err := store.due(now, 10)
	if err != nil {
		t.Fatalf("read cleanup IDs: %v", err)
	}
	if len(records) != 1 || !sameCleanupIDs(records[0].FileIDs, want) {
		t.Fatalf("cleanup records = %#v, want IDs %v", records, want)
	}
	var payload string
	if err := store.db.QueryRow(`SELECT file_ids_json FROM proxy_temp_cleanups WHERE id=?`, records[0].ID).Scan(&payload); err != nil {
		t.Fatalf("read cleanup payload: %v", err)
	}
	if !strings.HasPrefix(payload, secretEnvelopeVersion+".") {
		t.Fatalf("cleanup payload is not encrypted: %q", payload)
	}
}

func sameCleanupIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]struct{}, len(got))
	for _, id := range got {
		seen[id] = struct{}{}
	}
	for _, id := range want {
		if _, ok := seen[id]; !ok {
			return false
		}
	}
	return true
}
