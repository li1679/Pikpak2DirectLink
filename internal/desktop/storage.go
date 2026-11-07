// Package desktop configures isolated storage for the Windows desktop edition.
package desktop

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"pikpak2directlink/internal/app"
)

type Paths struct{ Root, Database, Backups, Logs, Key string }

func StoragePaths(root string) (Paths, error) {
	if !filepath.IsAbs(root) {
		return Paths{}, errors.New("desktop data directory must be absolute")
	}
	root = filepath.Clean(root)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return Paths{}, err
	}
	paths := Paths{Root: root, Database: filepath.Join(root, "personal.db"), Backups: filepath.Join(root, "backups"), Logs: filepath.Join(root, "logs"), Key: filepath.Join(root, "data-key.dpapi")}
	for _, dir := range []string{paths.Backups, paths.Logs} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return Paths{}, err
		}
	}
	return paths, nil
}

// LoadOrCreateKey never replaces a missing or unreadable key for an existing DB.
func LoadOrCreateKey(paths Paths) (string, error) {
	protected, err := os.ReadFile(paths.Key)
	if err == nil {
		plaintext, err := unprotectKey(protected)
		if err != nil {
			return "", fmt.Errorf("无法解密本机密钥；请使用原 Windows 账户或已导出的恢复密钥：%w", err)
		}
		if len(plaintext) != 32 {
			return "", errors.New("本机密钥文件无效；未重新生成密钥")
		}
		return base64.StdEncoding.EncodeToString(plaintext), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if _, err := os.Stat(paths.Database); !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("个人数据库已存在但本机密钥丢失；请先导入对应恢复密钥，程序不会生成新密钥覆盖数据")
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", err
	}
	if err := saveProtectedKey(paths.Key, key); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key), nil
}

func saveProtectedKey(path string, key []byte) error {
	protected, err := protectKey(key)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(protected)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func ExportRecoveryKey(paths Paths, destination string) error {
	// Export must only read an existing key; it must not initialize a new store.
	protected, err := os.ReadFile(paths.Key)
	if err != nil {
		return err
	}
	key, err := unprotectKey(protected)
	if err != nil {
		return err
	}
	if len(key) != 32 {
		return errors.New("invalid protected key")
	}
	if !filepath.IsAbs(destination) {
		return errors.New("recovery key destination must be absolute")
	}
	f, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("恢复密钥文件必须是新文件，不覆盖已有文件：%w", err)
	}
	_, writeErr := io.WriteString(f, base64.StdEncoding.EncodeToString(key)+"\n")
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(destination)
		return err
	}
	return nil
}

// ImportRecoveryKey is an explicit maintenance action, not a legacy data importer.
// A live desktop server's DB lock makes validation fail before any key is replaced.
func ImportRecoveryKey(paths Paths, source string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(decoded) != 32 {
		return errors.New("恢复密钥必须是 32 字节密钥的标准 Base64 编码")
	}
	key := base64.StdEncoding.EncodeToString(decoded)
	if _, err := os.Stat(paths.Database); err == nil {
		cfg := Config(paths, key, "127.0.0.1:0")
		srv, err := app.NewServer(cfg)
		if err != nil {
			return fmt.Errorf("恢复密钥无法打开个人数据库，请检查密钥并关闭正在运行的应用：%w", err)
		}
		if err := srv.Close(); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := os.Stat(paths.Key); err == nil {
		return errors.New("本机密钥已存在；请先安全保留该文件后再进行人工恢复")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return saveProtectedKey(paths.Key, decoded)
}

// Config deliberately ignores server environment variables for desktop isolation.
func Config(paths Paths, key, addr string) app.Config {
	return app.Config{Addr: addr, DataEncryptionKey: key, RootFolderName: "Pikpak2DirectLink", DBFile: paths.Database,
		BackupDir: paths.Backups, BackupInterval: 24 * time.Hour, BackupRetention: 7,
		RequestTimeout: 20 * time.Second, ResolveTimeout: 12 * time.Minute, QueueTimeout: 60 * time.Second,
		ParallelTimeout: 2 * time.Minute, ResolveConcurrency: 1, PollInterval: 5 * time.Second,
		ShareParseTimeout: 60 * time.Second, ShareURLTimeout: 60 * time.Second, SharePollInterval: 3 * time.Second}
}
