package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAccountSaveNeverRequiresRemoteLoginAndSurvivesReload(t *testing.T) {
	pool := newTrafficTestPool(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	saved, err := pool.Add(ctx, "  unverified@example.invalid ", "definitely-wrong-password", 700*bytesPerGB)
	if err != nil {
		t.Fatalf("local save depended on remote login: %v", err)
	}
	if saved.Username != "unverified@example.invalid" || saved.LoggedIn || !saved.Ready || saved.Status != AccountAvailable {
		t.Fatalf("unexpected saved status: %+v", saved)
	}
	var encrypted string
	if err := pool.store.db.QueryRow(`SELECT password_encrypted FROM pikpak_accounts WHERE id=?`, saved.ID).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encrypted, "definitely-wrong-password") {
		t.Fatal("password stored in plaintext")
	}
	var sessions int
	if err := pool.store.db.QueryRow(`SELECT COUNT(*) FROM pikpak_account_sessions WHERE account_id=?`, saved.ID).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("unexpected session: count=%d err=%v", sessions, err)
	}
	reloaded, err := NewAccountPool(pool.config)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := reloaded.Summary(saved.ID); !ok || got.Username != saved.Username || got.LoggedIn {
		t.Fatalf("saved account missing after reload: %+v", got)
	}
	// Listing or premium refresh must not implicitly log in an unverified account.
	if err := reloaded.RefreshPremiumInfo(ctx); err != nil {
		t.Fatalf("pending premium refresh attempted network work: %v", err)
	}
	target, ok := reloaded.NextCredentialCheck(time.Now())
	if !ok || time.Until(target.NextAt) < 5*time.Hour {
		t.Fatal("new account immediately scheduled repeated validation")
	}
}

func TestResavingAccountPreservesUsageAndSessionUntilPasswordChanges(t *testing.T) {
	pool := newTrafficTestPool(t)
	first, err := pool.Add(context.Background(), "user@example.invalid", "original", 1000)
	if err != nil {
		t.Fatal(err)
	}
	pool.AddTraffic(first.ID, 100)
	session := []byte(`{"username":"user@example.invalid","access_token":"access","refresh_token":"refresh","expires_at":"2030-01-01T00:00:00Z"}`)
	if err := pool.store.SessionStore(first.ID).Save(session); err != nil {
		t.Fatal(err)
	}
	pool, err = NewAccountPool(pool.config)
	if err != nil {
		t.Fatal(err)
	}
	same, err := pool.Add(context.Background(), "USER@example.invalid", "original", 2000)
	if err != nil {
		t.Fatal(err)
	}
	if same.ID != first.ID || same.TrafficUsed != 100 || !same.LoggedIn || len(pool.List()) != 1 {
		t.Fatalf("same account not preserved: %+v", same)
	}
	if raw, err := pool.store.SessionStore(first.ID).Load(); err != nil || string(raw) != string(session) {
		t.Fatal("valid session overwritten while re-saving same credentials")
	}
	changed, err := pool.Add(context.Background(), "user@example.invalid", "new-wrong-password", 3000)
	if err != nil {
		t.Fatal(err)
	}
	if changed.ID != first.ID || changed.TrafficUsed != 100 || changed.LoggedIn || len(pool.List()) != 1 {
		t.Fatalf("updated credentials state: %+v", changed)
	}
	var sessions int
	if err := pool.store.db.QueryRow(`SELECT COUNT(*) FROM pikpak_account_sessions WHERE account_id=?`, first.ID).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatal("password change did not clear old session")
	}
}

func TestAccountCredentialSaveRollbackKeepsOldPasswordAndSession(t *testing.T) {
	pool := newTrafficTestPool(t)
	first, err := pool.Add(context.Background(), "atomic@example.invalid", "original", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.store.SessionStore(first.ID).Save([]byte(`{"refresh_token":"keep"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.store.db.Exec(`CREATE TRIGGER fail_session_reset BEFORE DELETE ON pikpak_account_sessions BEGIN SELECT RAISE(ABORT,'session reset failed'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Add(context.Background(), "atomic@example.invalid", "replacement", 2000); err == nil {
		t.Fatal("save succeeded despite transaction failure")
	}
	if pool.accounts[first.ID].record.Password != "original" {
		t.Fatal("failed save published changed credentials to memory")
	}
	records, err := pool.store.List()
	if err != nil || records[0].Password != "original" {
		t.Fatal("failed save changed persisted password")
	}
	if session, err := pool.store.SessionStore(first.ID).Load(); err != nil || !strings.Contains(string(session), "keep") {
		t.Fatal("failed save lost old session")
	}
}

func TestAccountSaveHTTPReturnsCreatedForUnverifiedCredentials(t *testing.T) {
	srv := newTestServer(t)
	req := jsonRequest(http.MethodPost, "/api/accounts", `{"username":"offline@example.invalid","password":"wrong-password","traffic_limit_gb":700}`)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("save status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "wrong-password") {
		t.Fatal("response leaked password")
	}
	var account AccountSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &account); err != nil {
		t.Fatal(err)
	}
	// An explicit login error must not erase saved credentials or the account.
	retry := localRequest(http.MethodPost, "/api/accounts/"+account.ID+"/refresh-login").WithContext(ctx)
	failed := httptest.NewRecorder()
	srv.Handler().ServeHTTP(failed, retry)
	if failed.Code == http.StatusOK {
		t.Fatal("cancelled login unexpectedly succeeded")
	}
	got, ok := srv.accounts.Summary(account.ID)
	if !ok || got.Status != AccountFailed || got.CredentialCheckError == "" {
		t.Fatalf("failed verification not retained: %+v", got)
	}
	records, err := srv.accountStore.List()
	if err != nil || len(records) != 1 || records[0].Password != "wrong-password" {
		t.Fatal("verification failure lost stored credentials")
	}
}

func TestAccountSaveHTTPRejectsOnlyMissingCredentialsOrStorageFailure(t *testing.T) {
	for _, body := range []string{`{"username":"","password":"x"}`, `{"username":"u","password":""}`} {
		srv := newTestServer(t)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/accounts", body))
		if rec.Code != http.StatusBadRequest || len(srv.accounts.List()) != 0 {
			t.Fatalf("empty credentials status=%d", rec.Code)
		}
	}
	pool := newTrafficTestPool(t)
	if err := pool.store.db.Close(); err != nil {
		t.Fatal(err)
	}
	srv := &Server{accounts: pool, logs: newLogStore(10)}
	rec := httptest.NewRecorder()
	srv.handleAddAccount(rec, jsonRequest(http.MethodPost, "/api/accounts", `{"username":"u","password":"p"}`))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "未保存") || len(pool.List()) != 0 {
		t.Fatalf("storage failure was hidden: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := pool.Add(context.Background(), " ", "p", 0); !errors.Is(err, errAccountCredentialsRequired) {
		t.Fatal(err)
	}
}
