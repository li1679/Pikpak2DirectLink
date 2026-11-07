package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestTemporaryResourceDeadlineIsSixHoursAndCannotBeExtended(t *testing.T) {
	db := openAccountStoreTestDatabase(t)
	cipher := newTestSecretCipher(t, []byte("0123456789abcdef0123456789abcdef"))
	store := newProxyTempCleanupStore(db, cipher)
	created := time.Date(2026, 11, 7, 1, 0, 0, 0, time.UTC)
	if got := temporaryResourceCleanupAfter(created); !got.Equal(created.Add(6 * time.Hour)) {
		t.Fatalf("expiry=%v", got)
	}
	if err := store.record("job", "account", []string{"folder"}, created.Add(48*time.Hour), created); err != nil {
		t.Fatal(err)
	}
	if err := store.record("job", "account", []string{"file"}, created.Add(30*time.Hour), created.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if records, err := store.due(created.Add(6*time.Hour-time.Second), 10); err != nil || len(records) != 0 {
		t.Fatalf("deleted before six hours: %+v %v", records, err)
	}
	records, err := store.due(created.Add(6*time.Hour), 10)
	if err != nil || len(records) != 1 {
		t.Fatalf("six-hour due=%+v err=%v", records, err)
	}
	if !records[0].CreatedAt.Equal(created) || !records[0].CleanupAfter.Equal(created.Add(6*time.Hour)) || !sameCleanupIDs(records[0].FileIDs, []string{"folder", "file"}) {
		t.Fatalf("deadline extended or IDs lost: %+v", records[0])
	}
}

func TestTemporaryResourceCleanupSurvivesRestartAndNeverDeletesUntrackedFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "personal.db")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	cipher := newTestSecretCipher(t, []byte("0123456789abcdef0123456789abcdef"))
	created := time.Date(2026, 11, 7, 1, 0, 0, 0, time.UTC)
	store := newProxyTempCleanupStore(db, cipher)
	if err := store.record("job", "account", []string{"app-created-folder"}, time.Time{}, created); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	client := &fakePikPakClient{}
	srv := &Server{db: db, tempCleanups: newProxyTempCleanupStore(db, cipher), accounts: fakeAccountPool("account", client), jobs: newJobStore(10), logs: newLogStore(10), config: Config{RequestTimeout: time.Second}}
	// The same pass runs immediately at application startup and every minute.
	srv.cleanupJobs(created.Add(6*time.Hour - time.Second))
	if _, deleted := client.counts(); deleted != 0 {
		t.Fatal("resource was deleted early")
	}
	srv.cleanupJobs(created.Add(7 * time.Hour))
	if _, deleted := client.counts(); deleted != 1 {
		t.Fatalf("overdue resource not cleaned after restart: calls=%d", deleted)
	}
	client.mu.Lock()
	deleted := append([]string(nil), client.deletedIDs[0]...)
	client.mu.Unlock()
	if !sameCleanupIDs(deleted, []string{"app-created-folder"}) {
		t.Fatalf("cleanup touched untracked files: %v", deleted)
	}
	if records, err := srv.tempCleanups.due(created.Add(7*time.Hour), 10); err != nil || len(records) != 0 {
		t.Fatalf("successful deletion left ledger: %+v %v", records, err)
	}
}

func TestTemporaryResourceCleanupRetriesWithoutLosingLedger(t *testing.T) {
	db := openAccountStoreTestDatabase(t)
	cipher := newTestSecretCipher(t, []byte("0123456789abcdef0123456789abcdef"))
	store := newProxyTempCleanupStore(db, cipher)
	created := time.Date(2026, 11, 7, 1, 0, 0, 0, time.UTC)
	now := created.Add(6 * time.Hour)
	if err := store.record("job", "account", []string{"app-file"}, time.Time{}, created); err != nil {
		t.Fatal(err)
	}
	client := &fakePikPakClient{deleteFiles: func(context.Context, []string) error { return errors.New("network unavailable") }}
	srv := &Server{tempCleanups: store, accounts: fakeAccountPool("account", client), logs: newLogStore(10), config: Config{RequestTimeout: time.Second}}
	srv.cleanupProxyTempResources(now)
	var attempts int
	if err := db.QueryRow(`SELECT attempts FROM proxy_temp_cleanups`).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("failed cleanup ledger lost: attempts=%d err=%v", attempts, err)
	}
	if err := store.enforceRetentionLimit(); err != nil {
		t.Fatal(err)
	}
	srv.cleanupProxyTempResources(now.Add(time.Minute))
	if _, calls := client.counts(); calls != 1 {
		t.Fatal("failed deletion retry delay was ignored")
	}
	client.mu.Lock()
	client.deleteFiles = nil
	client.mu.Unlock()
	srv.cleanupProxyTempResources(now.Add(proxyTempCleanupRetryDelay))
	if _, calls := client.counts(); calls != 2 {
		t.Fatal("failed deletion was not retried")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM proxy_temp_cleanups`).Scan(&count); err != nil || count != 0 {
		t.Fatal("successful retry did not remove ledger")
	}
}

func TestStartupShortensPendingResourceDeadlineWithoutResettingAge(t *testing.T) {
	srv := newTestServer(t)
	cfg := srv.config
	db := srv.db
	store := srv.tempCleanups
	created := time.Now().UTC().Truncate(time.Second)
	if err := store.record("pending", "not-an-account", []string{"app-file"}, time.Time{}, created); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE proxy_temp_cleanups SET cleanup_after=? WHERE job_id='pending'`, created.Add(48*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	var deadline, age int64
	if err := restarted.db.QueryRow(`SELECT cleanup_after,created_at FROM proxy_temp_cleanups WHERE job_id='pending'`).Scan(&deadline, &age); err != nil {
		t.Fatal(err)
	}
	if deadline != created.Add(6*time.Hour).Unix() || age != created.Unix() {
		t.Fatalf("restart reset resource age or failed retention: deadline=%d age=%d", deadline, age)
	}
}

func TestFailedHealthProbeCleanupIsRecordedForExpiry(t *testing.T) {
	db := openAccountStoreTestDatabase(t)
	cipher := newTestSecretCipher(t, []byte("0123456789abcdef0123456789abcdef"))
	now := time.Date(2026, 11, 7, 1, 0, 0, 0, time.UTC)
	srv := &Server{tempCleanups: newProxyTempCleanupStore(db, cipher), logs: newLogStore(10)}
	srv.trackFailedHealthCleanup(AccountRuntime{ID: "account"}, accountCredentialProbeResult{RestoredIDs: []string{"probe-file"}, CleanupErr: errors.New("delete unavailable")}, now)
	due, err := srv.tempCleanups.due(now.Add(6*time.Hour), 10)
	if err != nil || len(due) != 1 || !sameCleanupIDs(due[0].FileIDs, []string{"probe-file"}) {
		t.Fatalf("probe cleanup was not retained: %+v %v", due, err)
	}
}
