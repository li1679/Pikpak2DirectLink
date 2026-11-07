package app

import (
	"database/sql"
	"errors"
	"fmt"
)

const (
	personalApplicationID = 0x50324450
	personalSchemaVersion = 1
)

// This is a fresh personal database, not an upgrade of the hosted application.
const personalSchema = `
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE pikpak_accounts (
    id TEXT PRIMARY KEY,
    sort_order INTEGER NOT NULL UNIQUE CHECK(sort_order >= 0),
    username TEXT NOT NULL,
    password_encrypted TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'available',
    premium INTEGER NOT NULL DEFAULT 0 CHECK(premium IN (0, 1)),
    premium_type TEXT NOT NULL DEFAULT '',
    premium_until TEXT NOT NULL DEFAULT '',
    premium_error TEXT NOT NULL DEFAULT '',
    premium_checked_at TEXT NOT NULL DEFAULT '',
    traffic_limit INTEGER NOT NULL DEFAULT 0 CHECK(traffic_limit >= 0),
    traffic_used INTEGER NOT NULL DEFAULT 0 CHECK(traffic_used >= 0),
    traffic_period TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    last_failed_at TEXT NOT NULL DEFAULT '',
    credential_checked_at TEXT NOT NULL DEFAULT '',
    credential_next_check_at TEXT NOT NULL DEFAULT '',
    credential_check_error TEXT NOT NULL DEFAULT '',
    parse_errors_json TEXT NOT NULL DEFAULT '[]',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE TABLE pikpak_account_sessions (
    account_id TEXT PRIMARY KEY REFERENCES pikpak_accounts(id) ON DELETE CASCADE,
    session_encrypted TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE TABLE app_secrets (
    key TEXT PRIMARY KEY,
    ciphertext TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE TABLE resolve_jobs (
    id TEXT PRIMARY KEY,
    parent_job_id TEXT REFERENCES resolve_jobs(id) ON DELETE CASCADE,
    child_index INTEGER CHECK(child_index >= 0),
    kind TEXT NOT NULL,
    mode TEXT NOT NULL,
    status TEXT NOT NULL,
    phase TEXT NOT NULL DEFAULT '',
    failure_code TEXT NOT NULL DEFAULT '',
    error_category TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    result_count INTEGER NOT NULL DEFAULT 0 CHECK(result_count >= 0),
    charged_bytes INTEGER NOT NULL DEFAULT 0 CHECK(charged_bytes >= 0),
    payload_encrypted TEXT,
    details_expires_at INTEGER,
    record_expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    completed_at INTEGER,
    CHECK((parent_job_id IS NULL AND child_index IS NULL) OR
          (parent_job_id IS NOT NULL AND child_index IS NOT NULL)),
    UNIQUE(parent_job_id, child_index)
);
CREATE INDEX idx_resolve_jobs_created ON resolve_jobs(created_at DESC);
CREATE INDEX idx_resolve_jobs_parent_child ON resolve_jobs(parent_job_id, child_index);
CREATE INDEX idx_resolve_jobs_status ON resolve_jobs(status);
CREATE INDEX idx_resolve_jobs_details_expiry ON resolve_jobs(details_expires_at) WHERE details_expires_at IS NOT NULL;
CREATE INDEX idx_resolve_jobs_record_expiry ON resolve_jobs(record_expires_at);
CREATE TABLE proxy_temp_cleanups (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    file_ids_json TEXT NOT NULL,
    cleanup_after INTEGER NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    created_at INTEGER NOT NULL
);
CREATE INDEX idx_proxy_temp_cleanups_after ON proxy_temp_cleanups(cleanup_after);
CREATE TABLE backup_runs (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    status TEXT NOT NULL,
    path TEXT NOT NULL DEFAULT '',
    size_bytes INTEGER NOT NULL DEFAULT 0 CHECK(size_bytes >= 0),
    sha256 TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    started_at INTEGER NOT NULL,
    completed_at INTEGER
);
CREATE INDEX idx_backup_runs_started ON backup_runs(started_at DESC);`

func initializeDatabase(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var applicationID, version, objects int
	if err := tx.QueryRow(`PRAGMA application_id`).Scan(&applicationID); err != nil {
		return err
	}
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'`).Scan(&objects); err != nil {
		return err
	}
	if objects == 0 && applicationID == 0 && version == 0 {
		if _, err := tx.Exec(personalSchema + librarySchema); err != nil {
			return fmt.Errorf("initialize personal database: %w", err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA application_id=%d; PRAGMA user_version=%d`, personalApplicationID, personalSchemaVersion)); err != nil {
			return err
		}
	} else if applicationID != personalApplicationID || version != personalSchemaVersion {
		return errors.New("database is not a supported personal database; choose a new DB_FILE (legacy databases are not imported)")
	}
	if err := validatePersonalSchema(tx); err != nil {
		return err
	}
	return tx.Commit()
}

type schemaQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

func validatePersonalSchema(db schemaQuerier) error {
	var applicationID, version int
	if err := db.QueryRow(`PRAGMA application_id`).Scan(&applicationID); err != nil {
		return err
	}
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if applicationID != personalApplicationID || version != personalSchemaVersion {
		return errors.New("database is not a supported Pikpak2DirectLink personal database")
	}
	for _, query := range []string{
		`SELECT key,value FROM settings LIMIT 0`,
		`SELECT ` + accountColumns + `,sort_order FROM pikpak_accounts LIMIT 0`,
		`SELECT account_id,session_encrypted,updated_at FROM pikpak_account_sessions LIMIT 0`,
		`SELECT key,ciphertext,updated_at FROM app_secrets LIMIT 0`,
		`SELECT id,parent_job_id,child_index,kind,mode,status,phase,failure_code,error_category,error_message,result_count,charged_bytes,payload_encrypted,details_expires_at,record_expires_at,created_at,updated_at,completed_at FROM resolve_jobs LIMIT 0`,
		`SELECT id,job_id,account_id,file_ids_json,cleanup_after,attempts,last_error,created_at FROM proxy_temp_cleanups LIMIT 0`,
		`SELECT id,kind,status,path,size_bytes,sha256,error,started_at,completed_at FROM backup_runs LIMIT 0`,
		`SELECT id,parent_id,kind,name,resource_kind,status,error,payload_encrypted,created_at,updated_at FROM library_entries LIMIT 0`,
		`SELECT token_hash,entry_id,file_id,mode,token_encrypted FROM library_links LIMIT 0`,
	} {
		var unused any
		if err := db.QueryRow(query).Scan(&unused); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("personal database schema is incomplete: %w", err)
		}
	}
	return nil
}
