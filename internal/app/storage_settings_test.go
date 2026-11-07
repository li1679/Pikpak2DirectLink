package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestStorageSettingsBackup(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		DBFile:          filepath.Join(dir, "pikpak.db"),
		BackupDir:       filepath.Join(dir, "backups"),
		BackupInterval:  24 * time.Hour,
		BackupRetention: 7,
	}
	db, err := openDatabase(cfg.DBFile)
	if err != nil {
		t.Fatalf("openDatabase: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	server := &Server{
		config:  cfg,
		db:      db,
		backups: newBackupManager(db, cfg.DBFile, cfg.BackupDir, cfg.BackupInterval, cfg.BackupRetention),
		nowFunc: time.Now,
	}

	recorder := httptest.NewRecorder()
	server.handleCreateStorageBackup(recorder, httptest.NewRequest(http.MethodPost, "/api/settings/storage/backups", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("backup status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var created struct {
		Backup storageBackupRunResponse `json:"backup"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Backup.Status != backupStatusSuccess || created.Backup.SHA256 == "" {
		t.Fatalf("backup response = %+v", created.Backup)
	}

	recorder = httptest.NewRecorder()
	server.handleGetStorageSettings(recorder, httptest.NewRequest(http.MethodGet, "/api/settings/storage", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("storage status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var status storageStatusResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.LastSuccess == nil || status.LastSuccess.ID != created.Backup.ID || status.BackupRetention != 7 {
		t.Fatalf("storage status = %+v", status)
	}

}
