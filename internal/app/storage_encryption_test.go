package app

import (
	"path/filepath"
	"testing"
)

func TestNewServerRejectsWrongDataEncryptionKey(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		Addr:              "127.0.0.1:51873",
		BackupDir:         filepath.Join(dir, "backups"),
		RootFolderName:    "TestRoot",
		DBFile:            filepath.Join(dir, "pikpak.db"),
		DataEncryptionKey: testDataEncryptionKey,
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	cfg.DataEncryptionKey = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="
	if _, err := NewServer(cfg); err == nil {
		t.Fatal("NewServer accepted the wrong data encryption key")
	}
}

func TestNewServerRequiresExplicitDataEncryptionKey(t *testing.T) {
	dir := t.TempDir()
	if server, err := NewServer(Config{Addr: "127.0.0.1:51873", DBFile: filepath.Join(dir, "personal.db"), BackupDir: filepath.Join(dir, "backups")}); err == nil {
		server.Close()
		t.Fatal("NewServer accepted a missing encryption key")
	}
}
