package app

import (
	"errors"
	"testing"
	"time"
)

func TestOAuthIdentitySeparatesProvidersSubjectsAndEmail(t *testing.T) {
	db := openAccountStoreTestDatabase(t)
	users := newUserStore(db)
	now := time.Unix(1700000000, 0)
	if _, err := db.Exec(`INSERT INTO users(id,email,display_name,avatar_url,disabled,created_at,updated_at) VALUES('email-user','same@example.com','Email User','',0,?,?)`, now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO user_identities(provider,provider_user_id,user_id,email,created_at,updated_at) VALUES('email','same@example.com','email-user','same@example.com',?,?)`, now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	profile := LinuxDoProfile{ID: "subject-123", Username: "Alice", Email: "Same@Example.com", Name: "Alice One"}
	a, created, err := users.upsertOAuthUser("site-a", profile, true, now)
	if err != nil || !created || a.ID == "email-user" {
		t.Fatalf("first OAuth identity = %+v %t %v", a, created, err)
	}
	b, created, err := users.upsertOAuthUser("site-b", profile, true, now)
	if err != nil || !created || b.ID == a.ID || b.ID == "email-user" {
		t.Fatalf("cross-provider subject merged: %+v %t %v", b, created, err)
	}
	profile.ID = "subject-456"
	c, created, err := users.upsertOAuthUser("site-a", profile, true, now)
	if err != nil || !created || c.ID == a.ID || c.ID == b.ID {
		t.Fatalf("same-email subject merged: %+v %t %v", c, created, err)
	}
	profile.ID, profile.Name, profile.Email = "subject-123", "Alice Updated", "changed@example.com"
	updated, created, err := users.upsertOAuthUser("site-a", profile, false, now.Add(time.Hour))
	if err != nil || created || updated.ID != a.ID || updated.DisplayName != "Alice Updated" || updated.Email != "changed@example.com" {
		t.Fatalf("existing login with registration closed = %+v %t %v", updated, created, err)
	}
	profile.ID = "unknown-subject"
	if _, _, err := users.upsertOAuthUser("site-a", profile, false, now); !errors.Is(err, errOAuthRegistrationClosed) {
		t.Fatalf("closed registration = %v", err)
	}
	if exists, err := users.oauthIdentityExists("site-a", profile.ID); err != nil || exists {
		t.Fatalf("closed registration created identity: %t %v", exists, err)
	}
	if _, _, err := users.upsertOAuthUser("email", profile, true, now); err == nil {
		t.Fatal("OAuth service may not create email credentials")
	}
}

func TestOAuthIdentityDisabledUserIsNotUpdated(t *testing.T) {
	db := openAccountStoreTestDatabase(t)
	users := newUserStore(db)
	now := time.Unix(1700000000, 0)
	profile := LinuxDoProfile{ID: "old-linuxdo-subject", Name: "Original Name", Email: "original@example.com"}
	user, created, err := users.upsertLinuxDoUser(profile, now)
	if err != nil || !created {
		t.Fatalf("legacy create: %+v %t %v", user, created, err)
	}
	if exists, err := users.linuxDoIdentityExists(profile.ID); err != nil || !exists {
		t.Fatalf("legacy identity: %t %v", exists, err)
	}
	loggedIn, created, err := users.upsertOAuthUser("linuxdo", profile, false, now.Add(time.Hour))
	if err != nil || created || loggedIn.ID != user.ID {
		t.Fatalf("legacy identity namespace was not preserved: %+v %t %v", loggedIn, created, err)
	}
	if _, err := db.Exec(`UPDATE users SET disabled=1 WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	profile.Name, profile.Email = "Forbidden Update", "forbidden@example.com"
	if _, _, err := users.upsertOAuthUser("linuxdo", profile, true, now.Add(2*time.Hour)); !errors.Is(err, errUserDisabled) {
		t.Fatalf("disabled login = %v", err)
	}
	loaded, ok, err := users.get(user.ID)
	if err != nil || !ok || loaded.DisplayName != "Original Name" || loaded.Email != "original@example.com" {
		t.Fatalf("disabled login mutated user: %+v %t %v", loaded, ok, err)
	}
	if name := queryString(t, db, `SELECT display_name FROM user_identities WHERE provider='linuxdo' AND provider_user_id=?`, profile.ID); name != "Original Name" {
		t.Fatalf("disabled login mutated identity: %q", name)
	}
}
