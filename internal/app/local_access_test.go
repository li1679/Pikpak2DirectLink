package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPersonalRoutesNeedNoProjectLogin(t *testing.T) {
	s := newTestServer(t)
	for _, path := range []string{"/", "/api/config", "/api/settings", "/api/settings/storage", "/api/accounts", "/api/library", "/api/logs"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, localRequest(http.MethodGet, path))
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if len(rec.Result().Cookies()) != 0 {
				t.Fatal("unexpected project session cookie")
			}
			if path == "/api/config" && (strings.Contains(rec.Body.String(), "auth") || strings.Contains(rec.Body.String(), "password")) {
				t.Fatalf("obsolete auth config: %s", rec.Body.String())
			}
		})
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, jsonRequest(http.MethodPut, "/api/settings", `{"concurrency":2,"task_timeout_seconds":240}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("settings update=%d %s", rec.Code, rec.Body.String())
	}
}

func TestRemovedProjectRoutesReturnNotFound(t *testing.T) {
	s := newTestServer(t)
	for _, route := range []struct{ method, path string }{
		{"GET", "/gate"}, {"GET", "/gate.html"}, {"GET", "/user.html"}, {"GET", "/u"},
		{"GET", "/api/auth/status"}, {"POST", "/api/auth/setup"}, {"POST", "/api/auth/login"},
		{"POST", "/api/auth/logout"}, {"POST", "/api/auth/password"},
		{"GET", "/api/users"}, {"GET", "/api/users/user"}, {"POST", "/api/users/user/subscriptions"},
		{"PATCH", "/api/users/user/subscriptions/sub"}, {"POST", "/api/users/user/subscriptions/sub/terminate"},
		{"GET", "/api/cdks"}, {"POST", "/api/cdks"}, {"PATCH", "/api/cdks/code"}, {"DELETE", "/api/cdks/code"},
		{"GET", "/api/u/auth/config"}, {"GET", "/api/u/auth/linuxdo/start"}, {"GET", "/api/u/auth/linuxdo/callback"},
		{"POST", "/api/u/auth/email/register"}, {"POST", "/api/u/auth/email/login"}, {"POST", "/api/u/login"},
		{"GET", "/api/u/status"}, {"POST", "/api/u/logout"}, {"POST", "/api/u/cdks/redeem"},
		{"POST", "/api/u/jobs"}, {"GET", "/api/u/jobs/job"}, {"POST", "/api/u/jobs/job/select"},
		{"GET", "/api/u/history"}, {"GET", "/api/u/history/job"},
		{"GET", "/api/settings/auth"}, {"PUT", "/api/settings/auth"}, {"DELETE", "/api/settings/storage/migration-backup"},
		{"GET", "/api/oauth/providers"}, {"GET", "/api/update"}, {"POST", "/api/update/check"}, {"POST", "/api/update/install"},
	} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, localRequest(route.method, route.path))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s=%d %s", route.method, route.path, rec.Code, rec.Body.String())
		}
	}
}

func TestLocalOnlyAccessBoundary(t *testing.T) {
	handler := localOnlyMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	tests := []struct {
		name, peer, host, origin, site, forwarded string
		want                                      int
	}{
		{name: "local downloader", peer: "127.0.0.1:123", host: "localhost:8080", want: 204},
		{name: "same origin browser", peer: "127.0.0.1:123", host: "127.0.0.1:8080", origin: "http://127.0.0.1:8080", site: "same-origin", want: 204},
		{name: "ipv6", peer: "[::1]:123", host: "[::1]:8080", want: 204},
		{name: "remote peer", peer: "192.168.1.2:123", host: "localhost:8080", want: 403},
		{name: "dns rebinding", peer: "127.0.0.1:123", host: "evil.example:8080", want: 403},
		{name: "host suffix", peer: "127.0.0.1:123", host: "localhost.evil.example", want: 403},
		{name: "cross origin", peer: "127.0.0.1:123", host: "localhost:8080", origin: "https://evil.example", want: 403},
		{name: "null origin", peer: "127.0.0.1:123", host: "localhost:8080", origin: "null", want: 403},
		{name: "different port", peer: "127.0.0.1:123", host: "localhost:8080", origin: "http://localhost:8081", want: 403},
		{name: "different scheme", peer: "127.0.0.1:123", host: "localhost:8080", origin: "https://localhost:8080", want: 403},
		{name: "cross site navigation", peer: "127.0.0.1:123", host: "localhost:8080", site: "cross-site", want: 403},
		{name: "same site different origin", peer: "127.0.0.1:123", host: "localhost:8080", site: "same-site", want: 403},
		{name: "forwarded", peer: "127.0.0.1:123", host: "localhost:8080", forwarded: "Forwarded", want: 403},
		{name: "forwarded for", peer: "127.0.0.1:123", host: "localhost:8080", forwarded: "X-Forwarded-For", want: 403},
		{name: "real ip", peer: "127.0.0.1:123", host: "localhost:8080", forwarded: "X-Real-IP", want: 403},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
			req.RemoteAddr, req.Host = tc.peer, tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.site != "" {
				req.Header.Set("Sec-Fetch-Site", tc.site)
			}
			if tc.forwarded != "" {
				req.Header.Set(tc.forwarded, "127.0.0.1")
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
