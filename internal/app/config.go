package app

import (
	"errors"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr                       string
	PublicBaseURL              string
	DataEncryptionKey          string
	DataEncryptionPreviousKeys []string
	Username                   string
	Password                   string
	RootFolderName             string
	DBFile                     string
	BackupDir                  string
	BackupInterval             time.Duration
	BackupRetention            int
	RequestTimeout             time.Duration
	ResolveTimeout             time.Duration
	QueueTimeout               time.Duration
	ParallelTimeout            time.Duration
	ResolveConcurrency         int
	PollInterval               time.Duration
	ShareParseTimeout          time.Duration // 分享链接解析超时
	ShareURLTimeout            time.Duration // 分享文件直链获取超时
	SharePollInterval          time.Duration // 分享文件直链轮询间隔
	AccountHealthURL           string
	AccountHealthEvery         time.Duration
	AccountRefreshGap          time.Duration
	AccountHealthTimeout       time.Duration
}

func LoadConfig() Config {
	requestTimeout := positiveDurationOrDefault("PIKPAK_REQUEST_TIMEOUT", 20*time.Second)
	return Config{
		Addr:                       envOrDefault("ADDR", "127.0.0.1:51873"),
		PublicBaseURL:              strings.TrimRight(os.Getenv("PUBLIC_BASE_URL"), "/"),
		DataEncryptionKey:          os.Getenv("DATA_ENCRYPTION_KEY"),
		DataEncryptionPreviousKeys: commaSeparatedEnv("DATA_ENCRYPTION_PREVIOUS_KEYS"),
		Username:                   strings.TrimSpace(os.Getenv("PIKPAK_USERNAME")),
		Password:                   os.Getenv("PIKPAK_PASSWORD"),
		RootFolderName:             envOrDefault("PIKPAK_ROOT_FOLDER", "Pikpak2DirectLink"),
		DBFile:                     envOrDefault("DB_FILE", "data/personal.db"),
		BackupDir:                  envOrDefault("BACKUP_DIR", "data/backups"),
		BackupInterval:             positiveDurationOrDefault("BACKUP_INTERVAL", defaultBackupInterval),
		BackupRetention:            positiveIntOrDefault("BACKUP_RETENTION", defaultBackupRetention),
		RequestTimeout:             requestTimeout,
		ResolveTimeout:             positiveDurationOrDefault("RESOLVE_TIMEOUT", 12*time.Minute),
		QueueTimeout:               positiveDurationOrDefault("QUEUE_TIMEOUT", 60*time.Second),
		ParallelTimeout:            positiveDurationOrDefault("PARALLEL_QUEUE_TIMEOUT", 2*time.Minute),
		ResolveConcurrency:         intOrDefault("RESOLVE_CONCURRENCY", 1),
		PollInterval:               positiveDurationOrDefault("POLL_INTERVAL", 5*time.Second),
		ShareParseTimeout:          positiveDurationOrDefault("SHARE_PARSE_TIMEOUT", 60*time.Second),
		ShareURLTimeout:            positiveDurationOrDefault("SHARE_URL_TIMEOUT", 60*time.Second),
		SharePollInterval:          positiveDurationOrDefault("SHARE_POLL_INTERVAL", 3*time.Second),
		AccountHealthURL:           envOrDefault("ACCOUNT_HEALTH_CHECK_URL", defaultAccountHealthCheckURL),
		AccountHealthEvery:         positiveDurationOrDefault("ACCOUNT_HEALTH_CHECK_INTERVAL", defaultAccountHealthCheckInterval),
		AccountRefreshGap:          positiveDurationOrDefault("ACCOUNT_AUTO_REFRESH_GAP", defaultAccountAutoRefreshGap),
		AccountHealthTimeout:       positiveDurationOrDefault("ACCOUNT_HEALTH_CHECK_TIMEOUT", maxDuration(requestTimeout*3, time.Minute)),
	}
}

func commaSeparatedEnv(key string) []string {
	value := os.Getenv(key)
	if value == "" {
		return nil
	}
	return strings.Split(value, ",")
}

func (c Config) IsConfigured() bool {
	return c.Username != "" && c.Password != ""
}

// ValidateLocal keeps this personal application on the local machine.
func (c Config) ValidateLocal() error {
	host, port, err := net.SplitHostPort(c.Addr)
	if err != nil || !isLoopbackHost(host) {
		return errors.New("ADDR must use a loopback IP address or localhost with an explicit port")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return errors.New("ADDR must include a valid numeric port")
	}
	if c.PublicBaseURL != "" {
		u, err := url.Parse(c.PublicBaseURL)
		if err != nil || u.Scheme != "http" || !isLoopbackHost(u.Hostname()) || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return errors.New("PUBLIC_BASE_URL must be a local HTTP origin")
		}
		if u.Port() != "" {
			port, err := strconv.Atoi(u.Port())
			if err != nil || port < 1 || port > 65535 {
				return errors.New("PUBLIC_BASE_URL has an invalid port")
			}
		}
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func intOrDefault(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	if parsed, err := strconv.Atoi(value); err == nil {
		return parsed
	}
	return fallback
}

func positiveDurationOrDefault(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
		return parsed
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}

func positiveIntOrDefault(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
		return parsed
	}
	return fallback
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
