package app

import (
	"context"
	"errors"
	"net/http"
	"time"
)

const backupScheduleCheckMaximum = time.Hour

type storageBackupRunResponse struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	Status      string     `json:"status"`
	Path        string     `json:"path"`
	SizeBytes   int64      `json:"size_bytes"`
	SHA256      string     `json:"sha256"`
	Error       string     `json:"error"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
}

type storageStatusResponse struct {
	BackupDir             string                    `json:"backup_dir"`
	BackupIntervalSeconds int64                     `json:"backup_interval_seconds"`
	BackupRetention       int                       `json:"backup_retention"`
	Running               bool                      `json:"running"`
	LastRun               *storageBackupRunResponse `json:"last_run,omitempty"`
	LastSuccess           *storageBackupRunResponse `json:"last_success,omitempty"`
	NextRunAt             time.Time                 `json:"next_run_at"`
}

func (s *Server) startBackupMonitor() {
	if s == nil || s.backups == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.backupCancel = cancel
	s.backupDone = make(chan struct{})
	go func() {
		defer close(s.backupDone)
		s.runBackupMonitor(ctx)
	}()
}

func (s *Server) runBackupMonitor(ctx context.Context) {
	s.runScheduledBackup(ctx)
	interval := s.backups.interval
	if interval <= 0 || interval > backupScheduleCheckMaximum {
		interval = backupScheduleCheckMaximum
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runScheduledBackup(ctx)
		}
	}
}

func (s *Server) runScheduledBackup(ctx context.Context) {
	if _, ran, err := s.backups.RunIfDue(ctx); err != nil && !errors.Is(err, context.Canceled) {
		s.logJob(LogWarn, "", "scheduled SQLite backup failed", err.Error())
	} else if ran && err == nil {
		s.logJob(LogSuccess, "", "scheduled SQLite backup completed")
	}
}

func (s *Server) handleGetStorageSettings(w http.ResponseWriter, r *http.Request) {
	payload, err := s.storageStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleCreateStorageBackup(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		writeError(w, http.StatusServiceUnavailable, "database backups are not configured")
		return
	}
	run, err := s.backups.RunNow(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.logJob(LogSuccess, "", "manual SQLite backup completed", run.Path)
	writeJSON(w, http.StatusOK, map[string]any{"backup": storageBackupRunPayload(&run)})
}

func (s *Server) storageStatus(ctx context.Context) (storageStatusResponse, error) {
	payload := storageStatusResponse{
		BackupDir:             s.config.BackupDir,
		BackupIntervalSeconds: int64(s.config.BackupInterval / time.Second),
		BackupRetention:       s.config.BackupRetention,
	}
	if s.backups != nil {
		status, err := s.backups.Status(ctx)
		if err != nil {
			return storageStatusResponse{}, err
		}
		payload.BackupDir = s.backups.dir
		payload.BackupIntervalSeconds = int64(s.backups.interval / time.Second)
		payload.BackupRetention = s.backups.retention
		payload.Running = status.Running
		payload.LastRun = storageBackupRunPayload(status.LastRun)
		payload.LastSuccess = storageBackupRunPayload(status.LastSuccess)
		payload.NextRunAt = status.NextRunAt
	}
	return payload, nil
}

func storageBackupRunPayload(run *backupRun) *storageBackupRunResponse {
	if run == nil {
		return nil
	}
	return &storageBackupRunResponse{
		ID: run.ID, Kind: run.Kind, Status: run.Status, Path: run.Path,
		SizeBytes: run.SizeBytes, SHA256: run.SHA256, Error: run.Error,
		StartedAt: run.StartedAt, CompletedAt: run.CompletedAt,
	}
}
