//go:build windows

package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDesktopServiceReadyHTTPAndPipeShutdown(t *testing.T) {
	root := t.TempDir()
	for _, eof := range []bool{false, true} {
		control, writer := io.Pipe()
		outReader, outWriter := io.Pipe()
		done := make(chan error, 1)
		go func() {
			done <- run([]string{"--data-dir", root, "--listen", "127.0.0.1:0"}, control, outWriter)
			outWriter.Close()
		}()
		var ready startupMessage
		if err := json.NewDecoder(outReader).Decode(&ready); err != nil {
			writer.Close()
			t.Fatal(err)
		}
		if ready.Event != "ready" || !strings.HasPrefix(ready.URL, "http://127.0.0.1:") {
			writer.Close()
			t.Fatalf("ready=%+v", ready)
		}
		client := &http.Client{Timeout: 2 * time.Second}
		response, err := client.Get(ready.URL + "/api/config")
		if err != nil {
			writer.Close()
			t.Fatal(err)
		}
		if response.StatusCode != 200 {
			t.Fatalf("status=%d", response.StatusCode)
		}
		response.Body.Close()
		if !eof {
			if _, err := io.WriteString(writer, "shutdown\n"); err != nil {
				t.Fatal(err)
			}
		}
		writer.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("service failed to stop")
		}
		control.Close()
		outReader.Close()
		if _, err := client.Get(ready.URL + "/api/config"); err == nil {
			t.Fatal("port still serves after shutdown")
		}
	}
	if _, err := os.Stat(filepath.Join(root, "data-key.dpapi")); err != nil {
		t.Fatal(err)
	}
}
func TestDesktopServiceRejectsOccupiedPortBeforeOpeningDatabase(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	root := t.TempDir()
	err = run([]string{"--data-dir", root, "--listen", listener.Addr().String()}, strings.NewReader(""), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "占用") {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "personal.db")); !os.IsNotExist(err) {
		t.Fatal("database was opened despite occupied port")
	}
}
