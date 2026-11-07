package app

import (
	"testing"
	"time"
)

func TestLoadConfigDefaultsToLoopbackAddress(t *testing.T) {
	t.Setenv("ADDR", "")

	if got := LoadConfig().Addr; got != "127.0.0.1:51873" {
		t.Fatalf("Addr = %q, want 127.0.0.1:51873", got)
	}
}

func TestLoadConfigAllowsExplicitListenAddress(t *testing.T) {
	t.Setenv("ADDR", ":8080")

	if got := LoadConfig().Addr; got != ":8080" {
		t.Fatalf("Addr = %q, want :8080", got)
	}
}

func TestLoadConfigRejectsNonPositiveDurations(t *testing.T) {
	tests := []struct {
		name     string
		env      string
		fallback time.Duration
		value    func(Config) time.Duration
	}{
		{"request timeout", "PIKPAK_REQUEST_TIMEOUT", 20 * time.Second, func(c Config) time.Duration { return c.RequestTimeout }},
		{"resolve timeout", "RESOLVE_TIMEOUT", 12 * time.Minute, func(c Config) time.Duration { return c.ResolveTimeout }},
		{"queue timeout", "QUEUE_TIMEOUT", time.Minute, func(c Config) time.Duration { return c.QueueTimeout }},
		{"parallel queue timeout", "PARALLEL_QUEUE_TIMEOUT", 2 * time.Minute, func(c Config) time.Duration { return c.ParallelTimeout }},
		{"poll interval", "POLL_INTERVAL", 5 * time.Second, func(c Config) time.Duration { return c.PollInterval }},
		{"share parse timeout", "SHARE_PARSE_TIMEOUT", time.Minute, func(c Config) time.Duration { return c.ShareParseTimeout }},
		{"share URL timeout", "SHARE_URL_TIMEOUT", time.Minute, func(c Config) time.Duration { return c.ShareURLTimeout }},
		{"share poll interval", "SHARE_POLL_INTERVAL", 3 * time.Second, func(c Config) time.Duration { return c.SharePollInterval }},
		{"account health interval", "ACCOUNT_HEALTH_CHECK_INTERVAL", defaultAccountHealthCheckInterval, func(c Config) time.Duration { return c.AccountHealthEvery }},
		{"account refresh gap", "ACCOUNT_AUTO_REFRESH_GAP", defaultAccountAutoRefreshGap, func(c Config) time.Duration { return c.AccountRefreshGap }},
		{"account health timeout", "ACCOUNT_HEALTH_CHECK_TIMEOUT", time.Minute, func(c Config) time.Duration { return c.AccountHealthTimeout }},
	}

	for _, tt := range tests {
		t.Setenv(tt.env, "")
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, invalid := range []string{"0", "-1s"} {
				t.Setenv(tt.env, invalid)
				if got := tt.value(LoadConfig()); got != tt.fallback {
					t.Fatalf("%s=%q produced %v, want fallback %v", tt.env, invalid, got, tt.fallback)
				}
			}
		})
	}
}

func TestPersonalConfigDefaultsToSeparateDatabase(t *testing.T) {
	t.Setenv("DB_FILE", "")
	if got := LoadConfig().DBFile; got != "data/personal.db" {
		t.Fatalf("DBFile=%q", got)
	}
}

func TestValidateLocal(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:51873", "127.4.3.2:1234", "[::1]:51873", "localhost:51873", "127.0.0.1:0"} {
		if err := (Config{Addr: addr}).ValidateLocal(); err != nil {
			t.Errorf("%s: %v", addr, err)
		}
	}
	for _, addr := range []string{"", ":51873", "0.0.0.0:51873", "[::]:51873", "192.168.1.3:51873", "example.com:51873", "127.0.0.1:http", "127.0.0.1:65536"} {
		if err := (Config{Addr: addr}).ValidateLocal(); err == nil {
			t.Errorf("accepted %q", addr)
		}
	}
	for _, base := range []string{"http://127.0.0.1:51873", "http://localhost:51873", "http://[::1]:51873"} {
		if err := (Config{Addr: "127.0.0.1:51873", PublicBaseURL: base}).ValidateLocal(); err != nil {
			t.Errorf("%s: %v", base, err)
		}
	}
	for _, base := range []string{"https://example.com", "http://192.168.1.1", "http://127.0.0.1/path", "http://user@localhost", "http://localhost?token=x", "http://localhost:99999"} {
		if err := (Config{Addr: "127.0.0.1:51873", PublicBaseURL: base}).ValidateLocal(); err == nil {
			t.Errorf("accepted %q", base)
		}
	}
}
