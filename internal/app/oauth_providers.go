package app

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	errOAuthProviderNotFound   = errors.New("OAuth provider not found")
	errOAuthProviderConflict   = errors.New("OAuth provider configuration changed or is linked to users")
	errOAuthRegistrationClosed = errors.New("OAuth registration is closed")
)

const (
	oauthLegacyMigrationKey = "oauth_providers_legacy_imported"
	oauthLinuxDoDeletedKey  = "oauth_linuxdo_deleted"
)

// OAuthProvider keeps the immutable identity namespace separate from its
// administrator-editable name. Secrets are held in app_secrets and never JSON.
type OAuthProvider struct {
	ID                     string    `json:"id"`
	Name                   string    `json:"name"`
	AuthorizationURL       string    `json:"authorization_url"`
	TokenURL               string    `json:"token_url"`
	UserInfoURL            string    `json:"userinfo_url"`
	ClientID               string    `json:"client_id"`
	ClientSecret           string    `json:"-"`
	ClientSecretConfigured bool      `json:"client_secret_configured"`
	Scopes                 string    `json:"scopes"`
	TokenAuthMethod        string    `json:"token_auth_method"`
	IDField                string    `json:"id_field"`
	UsernameField          string    `json:"username_field"`
	DisplayNameField       string    `json:"display_name_field"`
	EmailField             string    `json:"email_field"`
	AvatarField            string    `json:"avatar_field"`
	LoginEnabled           bool      `json:"login_enabled"`
	RegistrationEnabled    bool      `json:"registration_enabled"`
	PKCEEnabled            bool      `json:"pkce_enabled"`
	SortOrder              int       `json:"sort_order"`
	Revision               int64     `json:"revision"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

type oauthProviderStore struct {
	db     *sql.DB
	cipher *SecretCipher
}

func newOAuthProviderStore(db *sql.DB, cipher *SecretCipher) (*oauthProviderStore, error) {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS oauth_providers (
		id TEXT PRIMARY KEY, name TEXT NOT NULL,
		authorization_url TEXT NOT NULL, token_url TEXT NOT NULL, user_info_url TEXT NOT NULL,
		client_id TEXT NOT NULL, scopes TEXT NOT NULL, token_auth_method TEXT NOT NULL,
		id_field TEXT NOT NULL, username_field TEXT NOT NULL, display_name_field TEXT NOT NULL,
		email_field TEXT NOT NULL, avatar_field TEXT NOT NULL,
		login_enabled INTEGER NOT NULL, registration_enabled INTEGER NOT NULL,
		pkce_enabled INTEGER NOT NULL, sort_order INTEGER NOT NULL,
		revision INTEGER NOT NULL DEFAULT 1, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
	)`); err != nil {
		return nil, fmt.Errorf("create OAuth providers: %w", err)
	}
	s := &oauthProviderStore{db: db, cipher: cipher}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	marker, err := oauthSetting(tx, oauthLegacyMigrationKey, "")
	if err != nil {
		return nil, err
	}
	if marker != "1" {
		if err := s.importLegacyLinuxDoTx(tx); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`INSERT INTO settings(key,value) VALUES(?, '1') ON CONFLICT(key) DO UPDATE SET value='1'`, oauthLegacyMigrationKey); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s, nil
}

const oauthProviderColumns = `p.id,p.name,p.authorization_url,p.token_url,p.user_info_url,
	p.client_id,p.scopes,p.token_auth_method,p.id_field,p.username_field,p.display_name_field,
	p.email_field,p.avatar_field,p.login_enabled,p.registration_enabled,p.pkce_enabled,p.sort_order,
	p.revision,p.created_at,p.updated_at,COALESCE(a.ciphertext,'')`

const oauthProviderSelect = `SELECT ` + oauthProviderColumns + ` FROM oauth_providers p
	LEFT JOIN app_secrets a ON a.key='oauth-provider:' || p.id`

func oauthProviderSecretKey(id string) string { return "oauth-provider:" + id }

func (s *oauthProviderStore) scan(scanner subscriptionScanner) (OAuthProvider, error) {
	var p OAuthProvider
	var login, registration, pkce int
	var created, updated int64
	var ciphertext string
	err := scanner.Scan(&p.ID, &p.Name, &p.AuthorizationURL, &p.TokenURL, &p.UserInfoURL,
		&p.ClientID, &p.Scopes, &p.TokenAuthMethod, &p.IDField, &p.UsernameField, &p.DisplayNameField,
		&p.EmailField, &p.AvatarField, &login, &registration, &pkce, &p.SortOrder, &p.Revision, &created, &updated, &ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthProvider{}, errOAuthProviderNotFound
	}
	if err != nil {
		return OAuthProvider{}, err
	}
	p.LoginEnabled, p.RegistrationEnabled, p.PKCEEnabled = login != 0, registration != 0, pkce != 0
	p.CreatedAt, p.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
	if ciphertext != "" {
		secret, err := s.cipher.Decrypt(appSecretPurpose, oauthProviderSecretKey(p.ID), ciphertext)
		if err != nil {
			return OAuthProvider{}, fmt.Errorf("decrypt OAuth provider secret: %w", err)
		}
		p.ClientSecret = string(secret)
	}
	p.ClientSecretConfigured = p.ClientSecret != ""
	return p, nil
}

func (s *oauthProviderStore) list() ([]OAuthProvider, error) {
	rows, err := s.db.Query(oauthProviderSelect + ` ORDER BY p.sort_order,p.created_at,p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OAuthProvider{}
	for rows.Next() {
		p, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *oauthProviderStore) get(id string) (OAuthProvider, error) {
	return s.scan(s.db.QueryRow(oauthProviderSelect+` WHERE p.id=?`, strings.TrimSpace(id)))
}

func oauthProviderArgs(p OAuthProvider) []any {
	return []any{p.Name, p.AuthorizationURL, p.TokenURL, p.UserInfoURL, p.ClientID, p.Scopes, p.TokenAuthMethod,
		p.IDField, p.UsernameField, p.DisplayNameField, p.EmailField, p.AvatarField, p.LoginEnabled, p.RegistrationEnabled,
		p.PKCEEnabled, p.SortOrder, p.Revision, p.CreatedAt.Unix(), p.UpdatedAt.Unix()}
}

func (s *oauthProviderStore) insert(tx *sql.Tx, p OAuthProvider, secret string) error {
	args := append([]any{p.ID}, oauthProviderArgs(p)...)
	if _, err := tx.Exec(`INSERT INTO oauth_providers(id,name,authorization_url,token_url,user_info_url,client_id,
		scopes,token_auth_method,id_field,username_field,display_name_field,email_field,avatar_field,
		login_enabled,registration_enabled,pkce_enabled,sort_order,revision,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, args...); err != nil {
		return err
	}
	return s.writeSecret(tx, p.ID, secret, p.UpdatedAt)
}

func (s *oauthProviderStore) writeSecret(tx *sql.Tx, id, secret string, now time.Time) error {
	key := oauthProviderSecretKey(id)
	if secret == "" {
		_, err := tx.Exec(`DELETE FROM app_secrets WHERE key=?`, key)
		return err
	}
	ciphertext, err := s.cipher.Encrypt(appSecretPurpose, key, []byte(secret))
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO app_secrets(key,ciphertext,updated_at) VALUES(?,?,?)
		ON CONFLICT(key) DO UPDATE SET ciphertext=excluded.ciphertext,updated_at=excluded.updated_at`, key, ciphertext, now.Unix())
	return err
}

func (s *oauthProviderStore) create(p OAuthProvider, secret string) (OAuthProvider, error) {
	if err := normalizeOAuthProvider(&p); err != nil {
		return OAuthProvider{}, err
	}
	p.ID = "oauth_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	p.CreatedAt, p.UpdatedAt, p.Revision = time.Now().UTC().Truncate(time.Second), time.Now().UTC().Truncate(time.Second), 1
	tx, err := s.db.Begin()
	if err != nil {
		return OAuthProvider{}, err
	}
	defer tx.Rollback()
	secret = strings.TrimSpace(secret)
	if err := s.insert(tx, p, secret); err != nil {
		return OAuthProvider{}, err
	}
	if err := tx.Commit(); err != nil {
		return OAuthProvider{}, err
	}
	p.ClientSecret, p.ClientSecretConfigured = secret, secret != ""
	return p, nil
}

func (s *oauthProviderStore) update(id string, p OAuthProvider, secret *string, clear bool, expectedRevision int64) (OAuthProvider, error) {
	if err := normalizeOAuthProvider(&p); err != nil {
		return OAuthProvider{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return OAuthProvider{}, err
	}
	defer tx.Rollback()
	old, err := s.scan(tx.QueryRow(oauthProviderSelect+` WHERE p.id=?`, id))
	if err != nil {
		return OAuthProvider{}, err
	}
	if expectedRevision < 1 || old.Revision != expectedRevision {
		return OAuthProvider{}, errOAuthProviderConflict
	}
	if old.AuthorizationURL != p.AuthorizationURL || old.TokenURL != p.TokenURL || old.UserInfoURL != p.UserInfoURL || old.ClientID != p.ClientID || old.IDField != p.IDField {
		var linked bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM user_identities WHERE provider=?)`, id).Scan(&linked); err != nil {
			return OAuthProvider{}, err
		}
		if linked {
			return OAuthProvider{}, errOAuthProviderConflict
		}
	}
	p.ID, p.CreatedAt, p.UpdatedAt, p.Revision = old.ID, old.CreatedAt, time.Now().UTC().Truncate(time.Second), old.Revision+1
	args := append(oauthProviderArgs(p), id, expectedRevision)
	result, err := tx.Exec(`UPDATE oauth_providers SET name=?,authorization_url=?,token_url=?,user_info_url=?,client_id=?,
		scopes=?,token_auth_method=?,id_field=?,username_field=?,display_name_field=?,email_field=?,avatar_field=?,
		login_enabled=?,registration_enabled=?,pkce_enabled=?,sort_order=?,revision=?,created_at=?,updated_at=? WHERE id=? AND revision=?`, args...)
	if err != nil {
		return OAuthProvider{}, err
	}
	if n, err := result.RowsAffected(); err != nil {
		return OAuthProvider{}, err
	} else if n != 1 {
		return OAuthProvider{}, errOAuthProviderConflict
	}
	p.ClientSecret = old.ClientSecret
	if clear {
		p.ClientSecret = ""
	} else if secret != nil && strings.TrimSpace(*secret) != "" {
		p.ClientSecret = strings.TrimSpace(*secret)
	}
	if clear || (secret != nil && strings.TrimSpace(*secret) != "") {
		if err := s.writeSecret(tx, id, p.ClientSecret, p.UpdatedAt); err != nil {
			return OAuthProvider{}, err
		}
	}
	p.ClientSecretConfigured = p.ClientSecret != ""
	if err := tx.Commit(); err != nil {
		return OAuthProvider{}, err
	}
	return p, nil
}

func (s *oauthProviderStore) remove(id string, expectedRevision int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var revision int64
	if err := tx.QueryRow(`SELECT revision FROM oauth_providers WHERE id=?`, id).Scan(&revision); errors.Is(err, sql.ErrNoRows) {
		return errOAuthProviderNotFound
	} else if err != nil {
		return err
	}
	if expectedRevision < 1 || revision != expectedRevision {
		return errOAuthProviderConflict
	}
	result, err := tx.Exec(`DELETE FROM oauth_providers WHERE id=? AND revision=?`, id, expectedRevision)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return errOAuthProviderConflict
	}
	if _, err := tx.Exec(`DELETE FROM app_secrets WHERE key=?`, oauthProviderSecretKey(id)); err != nil {
		return err
	}
	if id == "linuxdo" {
		if _, err := tx.Exec(`INSERT INTO settings(key,value) VALUES(?, 'true') ON CONFLICT(key) DO UPDATE SET value='true'`, oauthLinuxDoDeletedKey); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func oauthSetting(db identityDB, key, fallback string) (string, error) {
	var value string
	err := db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return fallback, nil
	}
	return value, err
}

func oauthBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func (s *oauthProviderStore) legacyLinuxDoDeleted() bool {
	value, err := oauthSetting(s.db, oauthLinuxDoDeletedKey, "false")
	// Fail closed when the deletion marker cannot be read.
	return err != nil || oauthBool(value)
}

func (s *oauthProviderStore) importLegacyLinuxDo() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.importLegacyLinuxDoTx(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *oauthProviderStore) importLegacyLinuxDoTx(tx *sql.Tx) error {
	deleted, err := oauthSetting(tx, oauthLinuxDoDeletedKey, "false")
	if err != nil || oauthBool(deleted) {
		return err
	}
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM oauth_providers WHERE id='linuxdo')`).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	clientID, err := oauthSetting(tx, settingKeyLinuxDoClientID, "")
	if err != nil {
		return err
	}
	var ciphertext string
	secret := ""
	err = tx.QueryRow(`SELECT ciphertext FROM app_secrets WHERE key=?`, linuxDoClientSecretName).Scan(&ciphertext)
	if err == nil {
		plain, err := s.cipher.Decrypt(appSecretPurpose, linuxDoClientSecretName, ciphertext)
		if err != nil {
			return err
		}
		secret = string(plain)
	} else if errors.Is(err, sql.ErrNoRows) {
		secret, err = oauthSetting(tx, settingKeyLinuxDoClientSecret, "")
		if err != nil {
			return err
		}
	} else {
		return err
	}
	clientID, secret = strings.TrimSpace(clientID), strings.TrimSpace(secret)
	if clientID == "" && secret == "" {
		return nil
	}
	login, err := oauthSetting(tx, settingKeyLinuxDoLoginEnabled, "true")
	if err != nil {
		return err
	}
	registration, err := oauthSetting(tx, settingKeyLinuxDoRegistrationEnabled, "true")
	if err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Second)
	p := OAuthProvider{ID: "linuxdo", Name: "LinuxDO", AuthorizationURL: linuxDoAuthorizeURL, TokenURL: linuxDoTokenURL, UserInfoURL: linuxDoUserURL,
		ClientID: clientID, Scopes: "read", TokenAuthMethod: "client_secret_post", IDField: "id", UsernameField: "username", DisplayNameField: "name", EmailField: "email", AvatarField: "avatar_url",
		LoginEnabled: oauthBool(login), RegistrationEnabled: oauthBool(registration), Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := s.insert(tx, p, secret); err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM settings WHERE key=?`, settingKeyLinuxDoClientSecret)
	return err
}
