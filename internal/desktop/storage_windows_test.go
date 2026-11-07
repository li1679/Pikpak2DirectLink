//go:build windows

package desktop

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestProtectedKeySurvivesRestartAndIsNotPlaintext(t *testing.T) {
	paths, err := StoragePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := LoadOrCreateKey(paths)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateKey(paths)
	if err != nil || first != second {
		t.Fatalf("key changed across restart: %v", err)
	}
	raw, _ := os.ReadFile(paths.Key)
	decoded, _ := base64.StdEncoding.DecodeString(first)
	if bytes.Contains(raw, []byte(first)) || bytes.Contains(raw, decoded) {
		t.Fatal("key was saved in plaintext")
	}
	if len(decoded) != 32 {
		t.Fatal("key is not 32 bytes")
	}
}
func TestExistingDatabaseNeverGetsReplacementKey(t *testing.T) {
	paths, _ := StoragePaths(t.TempDir())
	if err := os.WriteFile(paths.Database, []byte("database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateKey(paths); err == nil {
		t.Fatal("missing key was regenerated")
	}
	if _, err := os.Stat(paths.Key); !os.IsNotExist(err) {
		t.Fatal("replacement key was written")
	}
	if err := os.WriteFile(paths.Key, []byte("damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateKey(paths); err == nil {
		t.Fatal("damaged key was replaced")
	}
	if got, _ := os.ReadFile(paths.Key); string(got) != "damaged" {
		t.Fatal("damaged key was overwritten")
	}
}
func TestRecoveryKeyExportAndExplicitImport(t *testing.T) {
	paths, _ := StoragePaths(t.TempDir())
	key, err := LoadOrCreateKey(paths)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "recovery.txt")
	if err := ExportRecoveryKey(paths, destination); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(destination)
	if string(data) != key+"\n" {
		t.Fatal("incorrect exported key")
	}
	if err := ExportRecoveryKey(paths, destination); err == nil {
		t.Fatal("export overwrote a file")
	}
	other, _ := StoragePaths(t.TempDir())
	if err := ImportRecoveryKey(other, destination); err != nil {
		t.Fatal(err)
	}
	got, err := LoadOrCreateKey(other)
	if err != nil || got != key {
		t.Fatalf("imported key differs: %v", err)
	}
	if err := ImportRecoveryKey(other, destination); err == nil {
		t.Fatal("import overwrote protected key")
	}
}
func TestConfigIgnoresServerEnvironment(t *testing.T) {
	t.Setenv("DB_FILE", "unrelated.db")
	t.Setenv("DATA_ENCRYPTION_KEY", "inherited")
	t.Setenv("PIKPAK_USERNAME", "not-desktop")
	paths, _ := StoragePaths(t.TempDir())
	cfg := Config(paths, "desktop-key", "127.0.0.1:51873")
	if cfg.DBFile != paths.Database || cfg.DataEncryptionKey != "desktop-key" || cfg.Username != "" {
		t.Fatal("desktop config used inherited server values")
	}
	if _, err := StoragePaths("relative"); err == nil {
		t.Fatal("relative data directory accepted")
	}
}
