package app

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestValidatePersonalDatabaseKeyIsReadOnlyAndRejectsWrongKey(t *testing.T) {
	srv := newTestServer(t)
	path := srv.config.DBFile
	if err := ValidatePersonalDatabaseKey(context.Background(), path, testDataEncryptionKey); err != nil {
		t.Fatal(err)
	}
	wrong := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	if err := ValidatePersonalDatabaseKey(context.Background(), path, wrong); err == nil {
		t.Fatal("wrong key accepted")
	}
	if err := ValidatePersonalDatabaseKey(context.Background(), path, testDataEncryptionKey); err != nil {
		t.Fatal("validation changed DB key")
	}
	missing := filepath.Join(t.TempDir(), "missing.db")
	if err := ValidatePersonalDatabaseKey(context.Background(), missing, testDataEncryptionKey); err == nil {
		t.Fatal("missing database accepted")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("validation created database")
	}
}
