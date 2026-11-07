package pikpak

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type memorySessionStore struct {
	data                              []byte
	loadErr, saveErr, deleteErr       error
	loadCalls, saveCalls, deleteCalls int
}

func (s *memorySessionStore) Load() ([]byte, error) {
	s.loadCalls++
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	return append([]byte(nil), s.data...), nil
}
func (s *memorySessionStore) Save(data []byte) error {
	s.saveCalls++
	if s.saveErr != nil {
		return s.saveErr
	}
	s.data = append([]byte(nil), data...)
	return nil
}
func (s *memorySessionStore) Delete() error {
	s.deleteCalls++
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.data = nil
	return nil
}

func TestStatusLoadsSessionStore(t *testing.T) {
	store := &memorySessionStore{data: marshalSession(t, sessionState{
		Username: "store@example.com", AccessToken: "access", RefreshToken: "refresh", DeviceID: "device", ExpiresAt: time.Now().Add(time.Hour),
	})}
	client := NewClient(Config{SessionStore: store})
	status := client.Status()
	if !status.Ready || !status.LoggedIn || !status.Persisted || status.Username != "store@example.com" {
		t.Fatalf("status=%+v", status)
	}
	if client.DeviceID() != "device" || store.loadCalls != 1 {
		t.Fatalf("device=%q loads=%d", client.DeviceID(), store.loadCalls)
	}
}

func TestSaveSessionUsesStore(t *testing.T) {
	store := &memorySessionStore{}
	client := NewClient(Config{SessionStore: store, Username: "demo@example.com"})
	client.refreshToken, client.sessionLoaded = "refresh", true
	if err := client.saveSessionLocked(); err != nil {
		t.Fatal(err)
	}
	var saved sessionState
	if err := json.Unmarshal(store.data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Username != "demo@example.com" || saved.RefreshToken != "refresh" || store.saveCalls != 1 {
		t.Fatalf("saved=%+v calls=%d", saved, store.saveCalls)
	}
	if !client.Status().Persisted {
		t.Fatal("store-backed session is not persisted")
	}
}

func TestSessionStoreLoadErrorLeavesSessionEmpty(t *testing.T) {
	store := &memorySessionStore{loadErr: errors.New("load failed")}
	client := NewClient(Config{SessionStore: store})
	status := client.Status()
	if status.Ready || status.LoggedIn || status.Persisted || store.loadCalls != 1 {
		t.Fatalf("status=%+v loads=%d", status, store.loadCalls)
	}
}

func TestLogoutDeletesSessionStore(t *testing.T) {
	store := &memorySessionStore{data: []byte("session")}
	client := NewClient(Config{SessionStore: store})
	client.accessToken, client.sessionLoaded = "access", true
	if err := client.Logout(); err != nil {
		t.Fatal(err)
	}
	if store.deleteCalls != 1 || store.data != nil || client.Status().LoggedIn {
		t.Fatalf("store=%+v status=%+v", store, client.Status())
	}
}

func TestSessionStorePropagatesWriteAndDeleteErrors(t *testing.T) {
	expected := errors.New("storage unavailable")
	store := &memorySessionStore{saveErr: expected, deleteErr: expected}
	client := NewClient(Config{SessionStore: store})
	if err := client.saveSessionLocked(); !errors.Is(err, expected) {
		t.Fatalf("save=%v", err)
	}
	if err := client.Logout(); !errors.Is(err, expected) {
		t.Fatalf("logout=%v", err)
	}
}

func marshalSession(t *testing.T, session sessionState) []byte {
	t.Helper()
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
