package app

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func newOAuthProviderTestStore(t *testing.T) (*oauthProviderStore, *sql.DB) {
	t.Helper()
	db := openAccountStoreTestDatabase(t)
	cipher := newTestSecretCipher(t, bytes.Repeat([]byte{0x71}, secretKeySize))
	store, err := newOAuthProviderStore(db, cipher)
	if err != nil {
		t.Fatal(err)
	}
	return store, db
}

func testOAuthProvider() OAuthProvider {
	return OAuthProvider{Name: "Example Community", AuthorizationURL: "https://example.com/oauth/authorize",
		TokenURL: "https://example.com/oauth/token", UserInfoURL: "https://example.com/me", ClientID: "client-1",
		Scopes: "read profile", TokenAuthMethod: "client_secret_post", IDField: "id", UsernameField: "username",
		DisplayNameField: "name", EmailField: "email", AvatarField: "avatar", LoginEnabled: true, RegistrationEnabled: true}
}

func TestOAuthProviderCRUDSecretsAndRevisions(t *testing.T) {
	store, db := newOAuthProviderTestStore(t)
	input := testOAuthProvider()
	input.ID, input.Revision = "linuxdo", 999
	p, err := store.create(input, "private-provider-secret")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.ID, "oauth_") || p.ID == input.ID || p.Revision != 1 || p.CreatedAt.IsZero() {
		t.Fatalf("server metadata = %+v", p)
	}
	raw := queryString(t, db, `SELECT ciphertext FROM app_secrets WHERE key=?`, oauthProviderSecretKey(p.ID))
	if raw == "private-provider-secret" || strings.Contains(raw, "private-provider-secret") {
		t.Fatal("secret was not encrypted")
	}
	encoded, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-provider-secret") || strings.Contains(string(encoded), `"client_secret":`) {
		t.Fatalf("response exposes secret: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"client_secret_configured":true`) {
		t.Fatalf("response misses configured flag: %s", encoded)
	}
	p.Name, p.ID = "Renamed Site", "attacker-selected-id"
	updated, err := store.update("linuxdo", p, nil, false, 1)
	if !errors.Is(err, errOAuthProviderNotFound) {
		t.Fatalf("unknown update: %v, %+v", err, updated)
	}
	list, err := store.list()
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	id := list[0].ID
	updated, err = store.update(id, p, nil, false, 1)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != id || updated.Revision != 2 || updated.Name != "Renamed Site" || updated.ClientSecret != "private-provider-secret" {
		t.Fatalf("update = %+v", updated)
	}
	newSecret := "must-not-commit"
	if _, err := store.update(id, updated, &newSecret, false, 1); !errors.Is(err, errOAuthProviderConflict) {
		t.Fatalf("stale update = %v", err)
	}
	if err := store.remove(id, 1); !errors.Is(err, errOAuthProviderConflict) {
		t.Fatalf("stale remove = %v", err)
	}
	loaded, err := store.get(id)
	if err != nil || loaded.ClientSecret != "private-provider-secret" || loaded.Revision != 2 {
		t.Fatalf("stale operation mutated row: %+v %v", loaded, err)
	}
	blank := " "
	loaded, err = store.update(id, loaded, &blank, false, loaded.Revision)
	if err != nil || loaded.ClientSecret != "private-provider-secret" {
		t.Fatalf("blank secret should preserve: %+v %v", loaded, err)
	}
	loaded, err = store.update(id, loaded, nil, true, loaded.Revision)
	if err != nil || loaded.ClientSecret != "" || loaded.ClientSecretConfigured {
		t.Fatalf("clear secret: %+v %v", loaded, err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM app_secrets WHERE key=?`, oauthProviderSecretKey(id)).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cleared ciphertext remains: %d %v", count, err)
	}
	if err := store.remove(id, loaded.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := store.get(id); !errors.Is(err, errOAuthProviderNotFound) {
		t.Fatalf("deleted get = %v", err)
	}
	p2, err := store.create(input, "another-secret")
	if err != nil || p2.ID == id {
		t.Fatalf("deleted identity namespace reused: %+v %v", p2, err)
	}
}

func TestOAuthProviderProtectsLinkedIdentityNamespace(t *testing.T) {
	store, db := newOAuthProviderTestStore(t)
	p, err := store.create(testOAuthProvider(), "secret")
	if err != nil {
		t.Fatal(err)
	}
	users := newUserStore(db)
	user, _, err := users.upsertOAuthUser(p.ID, LinuxDoProfile{ID: "subject-1", Name: "Alice"}, true, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	changes := []struct {
		name   string
		change func(*OAuthProvider)
	}{
		{"authorization", func(p *OAuthProvider) { p.AuthorizationURL = "https://other.example/authorize" }},
		{"token", func(p *OAuthProvider) { p.TokenURL = "https://other.example/token" }},
		{"user info", func(p *OAuthProvider) { p.UserInfoURL = "https://other.example/me" }},
		{"client id", func(p *OAuthProvider) { p.ClientID = "different-client" }},
		{"subject field", func(p *OAuthProvider) { p.IDField = "another_id" }},
	}
	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			changed := p
			change.change(&changed)
			if _, err := store.update(p.ID, changed, nil, false, p.Revision); !errors.Is(err, errOAuthProviderConflict) {
				t.Fatalf("identity change = %v", err)
			}
		})
	}
	p.Name, p.DisplayNameField, p.LoginEnabled = "Custom Name", "profile.name", false
	renamed, err := store.update(p.ID, p, nil, false, p.Revision)
	if err != nil || renamed.Name != "Custom Name" || renamed.LoginEnabled {
		t.Fatalf("presentation update = %+v %v", renamed, err)
	}
	if err := store.remove(p.ID, renamed.Revision); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := users.get(user.ID); err != nil || !ok {
		t.Fatalf("deleting provider removed linked user: %t %v", ok, err)
	}
	if exists, err := users.oauthIdentityExists(p.ID, "subject-1"); err != nil || !exists {
		t.Fatalf("deleting provider removed identity: %t %v", exists, err)
	}
}

func TestOAuthProviderLegacyImportIsOnceAndDeletionPermanent(t *testing.T) {
	store, db := newOAuthProviderTestStore(t)
	if list, err := store.list(); err != nil || len(list) != 0 {
		t.Fatalf("empty legacy should not create provider: %+v %v", list, err)
	}
	settings := newSettingsStore(db)
	if err := settings.setString(settingKeyLinuxDoClientID, "legacy-client"); err != nil {
		t.Fatal(err)
	}
	if err := settings.setBool(settingKeyLinuxDoLoginEnabled, false); err != nil {
		t.Fatal(err)
	}
	if err := settings.setBool(settingKeyLinuxDoRegistrationEnabled, false); err != nil {
		t.Fatal(err)
	}
	if err := newAppSecretStore(db, store.cipher).set(linuxDoClientSecretName, "legacy-secret"); err != nil {
		t.Fatal(err)
	}
	restarted, err := newOAuthProviderStore(db, store.cipher)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.get("linuxdo"); !errors.Is(err, errOAuthProviderNotFound) {
		t.Fatalf("migration reran despite marker: %v", err)
	}
	if err := store.importLegacyLinuxDo(); err != nil {
		t.Fatal(err)
	}
	p, err := store.get("linuxdo")
	if err != nil || p.ClientID != "legacy-client" || p.ClientSecret != "legacy-secret" || p.LoginEnabled || p.RegistrationEnabled || p.IDField != "id" {
		t.Fatalf("legacy migration = %+v %v", p, err)
	}
	if err := settings.setString(settingKeyLinuxDoClientID, "must-not-overwrite"); err != nil {
		t.Fatal(err)
	}
	if err := store.importLegacyLinuxDo(); err != nil {
		t.Fatal(err)
	}
	if got, err := store.get("linuxdo"); err != nil || got.ClientID != p.ClientID || got.Revision != p.Revision {
		t.Fatalf("repeat import mutated: %+v %v", got, err)
	}
	if err := store.remove("linuxdo", p.Revision); err != nil {
		t.Fatal(err)
	}
	if !store.legacyLinuxDoDeleted() {
		t.Fatal("missing LinuxDO deletion marker")
	}
	if err := store.importLegacyLinuxDo(); err != nil {
		t.Fatal(err)
	}
	if _, err := newOAuthProviderStore(db, store.cipher); err != nil {
		t.Fatal(err)
	}
	if _, err := store.get("linuxdo"); !errors.Is(err, errOAuthProviderNotFound) {
		t.Fatalf("deleted LinuxDO resurrected: %v", err)
	}
}

func TestOAuthProviderMigratesConfiguredLegacyAndRotatesSecret(t *testing.T) {
	db := openAccountStoreTestDatabase(t)
	settings := newSettingsStore(db)
	if err := settings.setString(settingKeyLinuxDoClientID, "legacy-client"); err != nil {
		t.Fatal(err)
	}
	if err := settings.setString(settingKeyLinuxDoClientSecret, "plaintext-legacy-secret"); err != nil {
		t.Fatal(err)
	}
	oldKey, newKey := bytes.Repeat([]byte{0x72}, secretKeySize), bytes.Repeat([]byte{0x73}, secretKeySize)
	store, err := newOAuthProviderStore(db, newTestSecretCipher(t, oldKey))
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.get("linuxdo")
	if err != nil || p.ClientSecret != "plaintext-legacy-secret" || p.Name != "LinuxDO" || !p.LoginEnabled || !p.RegistrationEnabled {
		t.Fatalf("migration = %+v %v", p, err)
	}
	if got := settings.getString(settingKeyLinuxDoClientSecret, ""); got != "" {
		t.Fatal("plaintext legacy secret was retained")
	}
	rotating, err := NewSecretCipher(testEncodedKey(newKey), []string{testEncodedKey(oldKey)})
	if err != nil {
		t.Fatal(err)
	}
	if err := newAppSecretStore(db, rotating).rotateSecrets(); err != nil {
		t.Fatal(err)
	}
	newOnly := newTestSecretCipher(t, newKey)
	reader := &oauthProviderStore{db: db, cipher: newOnly}
	if p, err := reader.get("linuxdo"); err != nil || p.ClientSecret != "plaintext-legacy-secret" {
		t.Fatalf("rotated provider secret = %+v %v", p, err)
	}
}

func TestOAuthProviderPreservesPartialLegacyConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, clientID, secret string
	}{
		{name: "client ID only", clientID: "unfinished-client"},
		{name: "secret only", secret: "unfinished-secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openAccountStoreTestDatabase(t)
			settings := newSettingsStore(db)
			if err := settings.setString(settingKeyLinuxDoClientID, tc.clientID); err != nil {
				t.Fatal(err)
			}
			if err := settings.setString(settingKeyLinuxDoClientSecret, tc.secret); err != nil {
				t.Fatal(err)
			}
			store, err := newOAuthProviderStore(db, newTestSecretCipher(t, bytes.Repeat([]byte{0x74}, secretKeySize)))
			if err != nil {
				t.Fatal(err)
			}
			p, err := store.get("linuxdo")
			if err != nil || p.ClientID != tc.clientID || p.ClientSecret != tc.secret || oauthProviderConfigured(p) {
				t.Fatalf("partial legacy configuration = %+v %v", p, err)
			}
		})
	}
}

func TestOAuthProviderSecretFailureRollsBackProvider(t *testing.T) {
	store, db := newOAuthProviderTestStore(t)
	if _, err := db.Exec(`CREATE TRIGGER reject_oauth_secret BEFORE INSERT ON app_secrets WHEN NEW.key LIKE 'oauth-provider:%' BEGIN SELECT RAISE(ABORT,'secret write rejected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.create(testOAuthProvider(), "secret"); err == nil {
		t.Fatal("create succeeded despite failed secret write")
	}
	if list, err := store.list(); err != nil || len(list) != 0 {
		t.Fatalf("partial create committed: %+v %v", list, err)
	}
	if _, err := db.Exec(`DROP TRIGGER reject_oauth_secret`); err != nil {
		t.Fatal(err)
	}
	p, err := store.create(testOAuthProvider(), "secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_oauth_secret BEFORE UPDATE ON app_secrets WHEN NEW.key LIKE 'oauth-provider:%' BEGIN SELECT RAISE(ABORT,'secret write rejected'); END`); err != nil {
		t.Fatal(err)
	}
	p.Name = "Must Roll Back"
	secret := "new-secret"
	if _, err := store.update(p.ID, p, &secret, false, p.Revision); err == nil {
		t.Fatal("update succeeded despite failed secret write")
	}
	got, err := store.get(p.ID)
	if err != nil || got.Name == p.Name || got.Revision != p.Revision || got.ClientSecret != "secret" {
		t.Fatalf("partial update committed: %+v %v", got, err)
	}
}
