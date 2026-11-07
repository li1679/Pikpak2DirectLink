package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	restoreSafetyDirectory    = "restore-safety"
	restoreSafetyManifestName = "manifest.json"
)

type restoreSafetyBackup struct {
	ID        string              `json:"id"`
	CreatedAt time.Time           `json:"created_at"`
	Files     []restoreSafetyFile `json:"files"`
}

type restoreSafetyFile struct {
	Kind         string `json:"kind"`
	OriginalPath string `json:"original_path"`
	RelativePath string `json:"relative_path"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
}

// StorageRestoreResult describes the files changed by an offline restore and
// where the pre-restore safety copy was saved.
type StorageRestoreResult struct {
	SafetyBackupPath string
	RestoredPaths    []string
}

type restoreOperation struct {
	kind           string
	source         string
	target         string
	staged         string
	checkAppDB     bool
	expectedSize   int64
	expectedSHA256 string
	rollback       string
	hadOriginal    bool
}

// RestoreDatabase replaces dbPath with a verified standalone SQLite backup.
// The caller must stop the server before invoking it.
func RestoreDatabase(ctx context.Context, dbPath, backupPath string) (StorageRestoreResult, error) {
	target, err := absoluteDatabasePath(dbPath)
	if err != nil {
		return StorageRestoreResult{}, err
	}
	lock, err := acquireStorageFileLock(target)
	if err != nil {
		return StorageRestoreResult{}, err
	}
	defer lock.Close()
	if err := recoverInterruptedStorageRestore(Config{DBFile: target}); err != nil {
		return StorageRestoreResult{}, err
	}
	source, err := regularRestoreSource(backupPath)
	if err != nil {
		return StorageRestoreResult{}, fmt.Errorf("validate database backup: %w", err)
	}
	if err := ensureStandaloneSQLiteBackup(source); err != nil {
		return StorageRestoreResult{}, err
	}
	if samePath(source, target) || sameFile(source, target) {
		return StorageRestoreResult{}, errors.New("database backup and target database must be different files")
	}
	if err := checkApplicationDatabaseBackup(ctx, source); err != nil {
		return StorageRestoreResult{}, fmt.Errorf("verify database backup: %w", err)
	}
	digest, size, err := hashBackupFile(source)
	if err != nil {
		return StorageRestoreResult{}, fmt.Errorf("hash database backup: %w", err)
	}

	operations := []restoreOperation{
		{kind: "database", source: source, target: target, checkAppDB: true, expectedSize: size, expectedSHA256: digest},
		{kind: "database_wal", target: target + "-wal"},
		{kind: "database_shm", target: target + "-shm"},
	}
	return executeStorageRestore(ctx, Config{DBFile: target}, operations, restoreJournalModeDatabase)
}

func executeStorageRestore(ctx context.Context, cfg Config, operations []restoreOperation, mode string) (StorageRestoreResult, error) {
	databasePath, err := absoluteDatabasePath(cfg.DBFile)
	if err != nil {
		return StorageRestoreResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return StorageRestoreResult{}, err
	}
	if err := validateRestoreOperations(operations); err != nil {
		return StorageRestoreResult{}, err
	}
	if err := ensureSQLiteWriteUnlocked(ctx, databasePath); err != nil {
		return StorageRestoreResult{}, err
	}
	id, err := newBackupRunID()
	if err != nil {
		return StorageRestoreResult{}, fmt.Errorf("generate restore id: %w", err)
	}
	if err := stageRestoreOperations(ctx, operations, id); err != nil {
		removeStagedRestoreFiles(operations)
		return StorageRestoreResult{}, err
	}
	defer removeStagedRestoreFiles(operations)

	safetyPath, err := createRestoreSafetyBackup(databasePath, operations, id, time.Now().UTC())
	if err != nil {
		return StorageRestoreResult{}, fmt.Errorf("create pre-restore safety backup: %w", err)
	}
	result := StorageRestoreResult{SafetyBackupPath: safetyPath}
	journalPath, err := prepareRestoreJournal(databasePath, operations, id, safetyPath, mode)
	if err != nil {
		return result, err
	}
	if err := applyRestoreOperations(operations, id, journalPath); err != nil {
		recoveryErr := recoverInterruptedStorageRestore(cfg)
		if result.SafetyBackupPath != "" {
			return result, errors.Join(
				fmt.Errorf("apply storage restore; pre-restore safety backup: %s: %w", result.SafetyBackupPath, err),
				recoveryErr,
			)
		}
		return result, errors.Join(err, recoveryErr)
	}
	for _, operation := range operations {
		if operation.source != "" {
			result.RestoredPaths = append(result.RestoredPaths, operation.target)
		}
	}
	return result, nil
}

func validateRestoreOperations(operations []restoreOperation) error {
	seen := make(map[string]struct{}, len(operations))
	targets := make([]string, 0, len(operations))
	for _, operation := range operations {
		if strings.TrimSpace(operation.target) == "" {
			return errors.New("restore target is empty")
		}
		if err := rejectFinalRestoreSymlink(operation.target); err != nil {
			return err
		}
		key := comparablePath(operation.target)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("restore target %q is duplicated", operation.target)
		}
		seen[key] = struct{}{}
		targets = append(targets, operation.target)
	}
	for _, operation := range operations {
		if operation.source == "" {
			continue
		}
		for _, target := range targets {
			if samePath(operation.source, target) || sameFile(operation.source, target) {
				return fmt.Errorf("restore source %q conflicts with restore target %q", operation.source, target)
			}
		}
	}
	return nil
}

func stageRestoreOperations(ctx context.Context, operations []restoreOperation, id string) error {
	for index := range operations {
		operation := &operations[index]
		if operation.source == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := regularRestoreSource(operation.source); err != nil {
			return fmt.Errorf("validate %s restore source: %w", operation.kind, err)
		}
		if operation.checkAppDB {
			if err := ensureStandaloneSQLiteBackup(operation.source); err != nil {
				return err
			}
		}
		if err := durableMkdirAll(filepath.Dir(operation.target), 0o700); err != nil {
			return fmt.Errorf("create restore target directory: %w", err)
		}
		operation.staged = filepath.Join(filepath.Dir(operation.target), "."+filepath.Base(operation.target)+".restore-new-"+id)
		size, digest, err := copyFileWithSHA256(operation.source, operation.staged)
		if err != nil {
			return fmt.Errorf("stage %s restore: %w", operation.kind, err)
		}
		if operation.expectedSHA256 != "" && (size != operation.expectedSize || digest != operation.expectedSHA256) {
			return fmt.Errorf("stage %s restore: source changed after verification", operation.kind)
		}
		if operation.checkAppDB {
			if err := ensureStandaloneSQLiteBackup(operation.source); err != nil {
				return err
			}
		}
		if err := syncBackupFile(operation.staged); err != nil {
			return fmt.Errorf("sync staged %s restore: %w", operation.kind, err)
		}
		if operation.checkAppDB {
			if err := checkApplicationDatabaseBackup(ctx, operation.staged); err != nil {
				return fmt.Errorf("verify staged %s restore: %w", operation.kind, err)
			}
		}
	}
	return nil
}

func removeStagedRestoreFiles(operations []restoreOperation) {
	for _, operation := range operations {
		if operation.staged != "" {
			_ = os.Remove(operation.staged)
		}
	}
}

func createRestoreSafetyBackup(databasePath string, operations []restoreOperation, id string, now time.Time) (string, error) {
	type existingTarget struct {
		kind string
		path string
	}
	var existing []existingTarget
	for _, operation := range operations {
		info, err := os.Stat(operation.target)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("pre-restore target is not a regular file: %s", operation.target)
		}
		existing = append(existing, existingTarget{kind: operation.kind, path: operation.target})
	}
	if len(existing) == 0 {
		return "", nil
	}
	sort.Slice(existing, func(i, j int) bool { return existing[i].path < existing[j].path })

	root := filepath.Join(filepath.Dir(databasePath), restoreSafetyDirectory)
	if err := durableMkdirAll(root, 0o700); err != nil {
		return "", err
	}
	name := "restore-" + now.Format("20060102T150405.000000000Z") + "-" + id
	finalPath := filepath.Join(root, name)
	tempPath := finalPath + ".tmp"
	if err := os.Mkdir(tempPath, 0o700); err != nil {
		return "", err
	}
	if err := syncDirectory(root); err != nil {
		return "", err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tempPath)
		}
	}()

	manifest := restoreSafetyBackup{ID: name, CreatedAt: now}
	for index, target := range existing {
		relative := filepath.Join("files", fmt.Sprintf("%04d-%s", index, filepath.Base(target.path)))
		copiedPath := filepath.Join(tempPath, relative)
		size, checksum, err := copyFileWithSHA256(target.path, copiedPath)
		if err != nil {
			return "", err
		}
		if err := syncBackupFile(copiedPath); err != nil {
			return "", err
		}
		manifest.Files = append(manifest.Files, restoreSafetyFile{
			Kind:         "pre_restore_" + target.kind,
			OriginalPath: target.path,
			RelativePath: filepath.ToSlash(relative),
			Size:         size,
			SHA256:       checksum,
		})
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	manifestPath := filepath.Join(tempPath, restoreSafetyManifestName)
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		return "", err
	}
	if err := syncBackupFile(manifestPath); err != nil {
		return "", err
	}
	if err := syncDirectory(filepath.Join(tempPath, "files")); err != nil {
		return "", err
	}
	if err := syncDirectory(tempPath); err != nil {
		return "", err
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		return "", err
	}
	if err := syncDirectory(root); err != nil {
		return "", err
	}
	cleanup = false
	return finalPath, nil
}

func applyRestoreOperations(operations []restoreOperation, id, journalPath string) error {
	if err := validateRestoreOperations(operations); err != nil {
		return err
	}
	type movedTarget struct {
		original string
		rollback string
	}
	var moved []movedTarget
	rollbackMoved := func() error {
		var rollbackErr error
		for index := len(moved) - 1; index >= 0; index-- {
			if err := os.Rename(moved[index].rollback, moved[index].original); err != nil {
				rollbackErr = errors.Join(rollbackErr, err)
			}
		}
		rollbackErr = errors.Join(rollbackErr, syncRestoreOperationDirectories(operations))
		return rollbackErr
	}

	for _, operation := range operations {
		if _, err := os.Stat(operation.target); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			_ = rollbackMoved()
			return fmt.Errorf("inspect restore target %q: %w", operation.target, err)
		}
		rollbackPath := operation.rollback
		if rollbackPath == "" {
			rollbackPath = filepath.Join(filepath.Dir(operation.target), "."+filepath.Base(operation.target)+".restore-old-"+id)
		}
		if err := os.Rename(operation.target, rollbackPath); err != nil {
			rollbackErr := rollbackMoved()
			return errors.Join(fmt.Errorf("move current restore target %q: %w", operation.target, err), rollbackErr)
		}
		moved = append(moved, movedTarget{original: operation.target, rollback: rollbackPath})
	}
	if err := syncRestoreOperationDirectories(operations); err != nil {
		return errors.Join(fmt.Errorf("sync restore rollback publication: %w", err), rollbackMoved())
	}

	var published []string
	for _, operation := range operations {
		if operation.staged == "" {
			continue
		}
		if err := os.Rename(operation.staged, operation.target); err != nil {
			var rollbackErr error
			for index := len(published) - 1; index >= 0; index-- {
				if removeErr := os.Remove(published[index]); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
					rollbackErr = errors.Join(rollbackErr, removeErr)
				}
			}
			rollbackErr = errors.Join(rollbackErr, rollbackMoved())
			return errors.Join(fmt.Errorf("publish restored target %q: %w", operation.target, err), rollbackErr)
		}
		published = append(published, operation.target)
	}
	if err := syncRestoreOperationDirectories(operations); err != nil {
		var rollbackErr error
		for index := len(published) - 1; index >= 0; index-- {
			if removeErr := os.Remove(published[index]); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				rollbackErr = errors.Join(rollbackErr, removeErr)
			}
		}
		rollbackErr = errors.Join(rollbackErr, rollbackMoved())
		return errors.Join(fmt.Errorf("sync restored targets: %w", err), rollbackErr)
	}
	if err := markRestoreJournalCommitted(journalPath, id); err != nil {
		return err
	}

	var cleanupErr error
	for _, target := range moved {
		if err := os.Remove(target.rollback); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	if cleanupErr != nil {
		return fmt.Errorf("restore completed but temporary file cleanup failed: %w", cleanupErr)
	}
	if err := syncRestoreOperationDirectories(operations); err != nil {
		return fmt.Errorf("restore completed but directory sync failed: %w", err)
	}
	return removeRestoreJournal(journalPath)
}

func syncRestoreOperationDirectories(operations []restoreOperation) error {
	directories := make(map[string]struct{}, len(operations))
	for _, operation := range operations {
		directories[filepath.Dir(operation.target)] = struct{}{}
	}
	var syncErr error
	for directory := range directories {
		syncErr = errors.Join(syncErr, syncDirectory(directory))
	}
	return syncErr
}

func regularRestoreSource(path string) (string, error) {
	absPath, err := absoluteRestorePath(path)
	if err != nil {
		return "", err
	}
	if err := rejectFinalRestoreSymlink(absPath); err != nil {
		return "", err
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("restore source is not a regular file: %s", absPath)
	}
	return absPath, nil
}

func ensureStandaloneSQLiteBackup(path string) error {
	walPath := path + "-wal"
	info, err := os.Stat(walPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size() > 0 {
		return fmt.Errorf("database backup is not standalone: remove or checkpoint %s", walPath)
	}
	return nil
}

func absoluteDatabasePath(path string) (string, error) {
	if strings.TrimSpace(path) == ":memory:" {
		return "", errors.New("offline restore requires a disk database")
	}
	path, err := absoluteRestorePath(path)
	if err != nil {
		return "", fmt.Errorf("resolve target database path: %w", err)
	}
	return path, nil
}

func absoluteRestorePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("path is empty")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absPath), nil
}

func canonicalRestorePath(path string) (string, error) {
	current, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	var missing []string
	for {
		_, err := os.Lstat(current)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no existing ancestor for %q", path)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func rejectFinalRestoreSymlink(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("restore path %q is a symbolic link", path)
	}
	return nil
}

func comparablePath(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

func samePath(a, b string) bool {
	return comparablePath(a) == comparablePath(b)
}

func sameFile(a, b string) bool {
	aInfo, aErr := os.Stat(a)
	bInfo, bErr := os.Stat(b)
	return aErr == nil && bErr == nil && os.SameFile(aInfo, bInfo)
}
