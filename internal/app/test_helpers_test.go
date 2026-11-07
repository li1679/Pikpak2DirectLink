package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testDataEncryptionKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

func newTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := Config{
		Addr:               "127.0.0.1:51873",
		RootFolderName:     "TestRoot",
		DBFile:             filepath.Join(t.TempDir(), "personal.db"),
		DataEncryptionKey:  testDataEncryptionKey,
		RequestTimeout:     time.Second,
		QueueTimeout:       time.Minute,
		ParallelTimeout:    time.Minute,
		ResolveConcurrency: 1,
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return srv
}

func jsonRequest(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Host = "localhost"
	req.Header.Set("Content-Type", "application/json")
	return req
}

func localRequest(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Host = "localhost"
	return req
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
