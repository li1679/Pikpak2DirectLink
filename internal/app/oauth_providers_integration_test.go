package app

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type oauthIntegrationTokenRequest struct {
	Form          url.Values
	Authorization string
}

type oauthIntegrationStart struct {
	state       string
	nonce       *http.Cookie
	authorize   *url.URL
	callbackURL string
}

func integrationOAuthProvider(t *testing.T, s *Server, upstream, name, authMethod string, pkce bool) OAuthProvider {
	t.Helper()
	p := OAuthProvider{
		Name: name, AuthorizationURL: upstream + "/authorize", TokenURL: upstream + "/token", UserInfoURL: upstream + "/userinfo",
		ClientID: "integration-client", Scopes: "openid profile email", TokenAuthMethod: authMethod, PKCEEnabled: pkce,
		IDField: "sub", UsernameField: "preferred_username", DisplayNameField: "name", EmailField: "email", AvatarField: "picture",
		LoginEnabled: true, RegistrationEnabled: true,
	}
	if err := normalizeOAuthProvider(&p); err != nil {
		t.Fatalf("normalize provider: %v", err)
	}
	p, err := s.oauthProviders.create(p, "integration-secret")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	return p
}

func integrationOAuthStart(t *testing.T, s *Server, p OAuthProvider) oauthIntegrationStart {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, oauthStartPath(p.ID), nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("OAuth start returned %d: %s", rec.Code, rec.Body.String())
	}
	u, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse authorize redirect: %v", err)
	}
	if u.Query().Get("state") == "" || u.Query().Get("client_id") != p.ClientID || u.Query().Get("response_type") != "code" {
		t.Fatalf("invalid authorization parameters: %v", u.Query())
	}
	if u.Query().Get("client_secret") != "" || u.Query().Get("code_verifier") != "" {
		t.Fatal("authorization redirect exposed a secret")
	}
	if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("OAuth start must prevent caching and referrer disclosure")
	}
	name, path := oauthNonceCookie(p.ID)
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			cookie = c
		}
	}
	if cookie == nil || cookie.Value == "" || !cookie.HttpOnly || cookie.Path != path || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("invalid browser nonce cookie: %+v", cookie)
	}
	return oauthIntegrationStart{state: u.Query().Get("state"), nonce: cookie, authorize: u, callbackURL: u.Query().Get("redirect_uri")}
}

func integrationOAuthCallback(s *Server, p OAuthProvider, start oauthIntegrationStart, code string) *httptest.ResponseRecorder {
	q := url.Values{"state": {start.state}, "code": {code}}
	req := httptest.NewRequest(http.MethodGet, oauthCallbackPath(p.ID)+"?"+q.Encode(), nil)
	if start.nonce != nil {
		req.AddCookie(start.nonce)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func integrationOAuthSession(t *testing.T, s *Server, rec *httptest.ResponseRecorder) (*http.Cookie, userStatusResponse) {
	t.Helper()
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/u" {
		t.Fatalf("OAuth callback failed: status=%d location=%q body=%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	var session *http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == userSessionCookieName && cookie.Value != "" && cookie.MaxAge > 0 {
			session = cookie
		}
	}
	if session == nil {
		t.Fatal("successful OAuth callback did not set a user session")
	}
	req := httptest.NewRequest(http.MethodGet, "/api/u/status", nil)
	req.AddCookie(session)
	status := httptest.NewRecorder()
	s.Handler().ServeHTTP(status, req)
	if status.Code != http.StatusOK {
		t.Fatalf("OAuth session cannot access user status: %d %s", status.Code, status.Body.String())
	}
	var payload userStatusResponse
	if err := json.Unmarshal(status.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode user status: %v", err)
	}
	if payload.User.ID == "" {
		t.Fatal("OAuth session did not identify its user")
	}
	return session, payload
}

func integrationOAuthRejected(t *testing.T, rec *httptest.ResponseRecorder, expected string) {
	t.Helper()
	if rec.Code != http.StatusFound {
		t.Fatalf("rejected callback status = %d, want 302: %s", rec.Code, rec.Body.String())
	}
	u, err := url.Parse(rec.Header().Get("Location"))
	if err != nil || u.Path != "/u" || u.Query().Get("error") != expected {
		t.Fatalf("callback error redirect = %q, want error=%s", rec.Header().Get("Location"), expected)
	}
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == userSessionCookieName && cookie.Value != "" && cookie.MaxAge >= 0 {
			t.Fatal("failed callback issued a user session")
		}
	}
}

func integrationOAuthUpstream(t *testing.T, profile func(string) string) (*httptest.Server, <-chan oauthIntegrationTokenRequest) {
	t.Helper()
	tokens := make(chan oauthIntegrationTokenRequest, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			if r.Method != http.MethodPost {
				t.Errorf("token method = %s, want POST", r.Method)
			}
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse token request: %v", err)
			}
			tokens <- oauthIntegrationTokenRequest{Form: r.PostForm, Authorization: r.Header.Get("Authorization")}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "token-" + r.PostForm.Get("code"), "token_type": "Bearer"})
		case "/userinfo":
			if r.Method != http.MethodGet || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer token-") {
				t.Errorf("invalid userinfo request: method=%s authorization=%q", r.Method, r.Header.Get("Authorization"))
			}
			_, _ = w.Write([]byte(profile(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer token-"))))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, tokens
}

func TestOAuthProvidersIsolateSameSubjectAndEmail(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	emailUser, err := s.users.createEmailUser("shared@example.test", "existing-email-password", s.now())
	if err != nil {
		t.Fatalf("create existing email account: %v", err)
	}
	profile := func(string) string {
		return `{"sub":900719925474099312,"preferred_username":"same-user","name":"Same User","email":"shared@example.test","picture":"https://example.test/avatar.png"}`
	}
	first, _ := integrationOAuthUpstream(t, profile)
	second, _ := integrationOAuthUpstream(t, profile)
	p1 := integrationOAuthProvider(t, s, first.URL, "First Community", "client_secret_post", false)
	p2 := integrationOAuthProvider(t, s, second.URL, "Second Community", "client_secret_post", false)
	_, u1 := integrationOAuthSession(t, s, integrationOAuthCallback(s, p1, integrationOAuthStart(t, s, p1), "first"))
	_, u2 := integrationOAuthSession(t, s, integrationOAuthCallback(s, p2, integrationOAuthStart(t, s, p2), "second"))
	if u1.User.ID == u2.User.ID {
		t.Fatal("different providers merged users with the same subject and email")
	}
	if u1.User.ID == emailUser.ID || u2.User.ID == emailUser.ID {
		t.Fatal("OAuth login inherited an existing email account through an unverified email match")
	}
	preservedEmailUser, err := s.users.verifyEmailLogin("shared@example.test", "existing-email-password")
	if err != nil || preservedEmailUser.ID != emailUser.ID {
		t.Fatalf("OAuth logins changed the existing email account: user=%+v err=%v", preservedEmailUser, err)
	}
	for _, entry := range []struct {
		provider OAuthProvider
		status   userStatusResponse
	}{{p1, u1}, {p2, u2}} {
		if entry.status.User.DisplayName != "Same User" || entry.status.User.AvatarURL != "https://example.test/avatar.png" {
			t.Fatalf("profile mapping lost fields: %+v", entry.status.User)
		}
		if entry.status.LoginMethod == nil || entry.status.LoginMethod.ID != entry.provider.ID || entry.status.LoginMethod.Name != entry.provider.Name || entry.status.LoginMethod.Type != "oauth" {
			t.Fatalf("incorrect login method: %+v", entry.status.LoginMethod)
		}
		var subject, userID string
		if err := s.db.QueryRow(`SELECT provider_user_id,user_id FROM user_identities WHERE provider=?`, entry.provider.ID).Scan(&subject, &userID); err != nil {
			t.Fatalf("read provider identity: %v", err)
		}
		if subject != "900719925474099312" || userID != entry.status.User.ID {
			t.Fatalf("subject lost integer precision or identity changed: subject=%q user=%q", subject, userID)
		}
	}
	_, again := integrationOAuthSession(t, s, integrationOAuthCallback(s, p1, integrationOAuthStart(t, s, p1), "repeat"))
	if again.User.ID != u1.User.ID {
		t.Fatal("repeat login created another user")
	}
	public := httptest.NewRecorder()
	s.Handler().ServeHTTP(public, httptest.NewRequest(http.MethodGet, "/api/u/auth/config", nil))
	if public.Code != http.StatusOK {
		t.Fatalf("public auth config: %d %s", public.Code, public.Body.String())
	}
	var config userAuthConfigResponse
	if err := json.Unmarshal(public.Body.Bytes(), &config); err != nil {
		t.Fatalf("decode auth config: %v", err)
	}
	if len(config.OAuthProviders) != 2 {
		t.Fatalf("public auth providers = %+v", config.OAuthProviders)
	}
	if strings.Contains(public.Body.String(), "integration-secret") || strings.Contains(public.Body.String(), "/token") || strings.Contains(public.Body.String(), "/userinfo") {
		t.Fatal("public auth config exposed private provider configuration")
	}
}

func TestOAuthProviderNestedMappingAndClientAuthentication(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"client_secret_post", "client_secret_basic"} {
		t.Run(method, func(t *testing.T) {
			s := newTestServer(t)
			upstream, tokens := integrationOAuthUpstream(t, func(string) string {
				return `{"account":{"identity":{"subject":"nested-user"},"username":"nested","display":"Nested User","mail":"nested@example.test","avatar":"javascript:alert(1)"}}`
			})
			p := integrationOAuthProvider(t, s, upstream.URL, "Nested Community", method, true)
			p.IDField, p.UsernameField, p.DisplayNameField, p.EmailField, p.AvatarField = "account.identity.subject", "account.username", "account.display", "account.mail", "account.avatar"
			var err error
			p, err = s.oauthProviders.update(p.ID, p, nil, false, p.Revision)
			if err != nil {
				t.Fatalf("update field mappings: %v", err)
			}
			start := integrationOAuthStart(t, s, p)
			_, status := integrationOAuthSession(t, s, integrationOAuthCallback(s, p, start, "nested-code"))
			if status.User.DisplayName != "Nested User" || status.User.AvatarURL != "" {
				t.Fatalf("nested mapping or avatar validation failed: %+v", status.User)
			}
			request := <-tokens
			if request.Form.Get("code") != "nested-code" || request.Form.Get("grant_type") != "authorization_code" || request.Form.Get("redirect_uri") != start.callbackURL {
				t.Fatalf("incorrect token parameters: %v", request.Form)
			}
			verifier := request.Form.Get("code_verifier")
			if len(verifier) < 43 || len(verifier) > 128 {
				t.Fatalf("invalid PKCE verifier length: %d", len(verifier))
			}
			digest := sha256.Sum256([]byte(verifier))
			if start.authorize.Query().Get("code_challenge_method") != "S256" || start.authorize.Query().Get("code_challenge") != base64.RawURLEncoding.EncodeToString(digest[:]) {
				t.Fatal("PKCE challenge did not match the exchanged verifier")
			}
			if method == "client_secret_post" {
				if request.Authorization != "" || request.Form.Get("client_id") != p.ClientID || request.Form.Get("client_secret") != "integration-secret" {
					t.Fatalf("incorrect post client authentication: %+v", request)
				}
			} else {
				expected := "Basic " + base64.StdEncoding.EncodeToString([]byte(url.QueryEscape(p.ClientID)+":"+url.QueryEscape("integration-secret")))
				if request.Authorization != expected || request.Form.Get("client_id") != "" || request.Form.Get("client_secret") != "" {
					t.Fatalf("incorrect basic client authentication: %+v", request)
				}
			}
		})
	}
}

func TestOAuthProviderRegistrationClosurePreservesExistingLogin(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	upstream, _ := integrationOAuthUpstream(t, func(code string) string {
		return fmt.Sprintf(`{"sub":%q,"name":"Community User","email":"shared@example.test"}`, code)
	})
	p := integrationOAuthProvider(t, s, upstream.URL, "Closed Community", "client_secret_post", false)
	_, first := integrationOAuthSession(t, s, integrationOAuthCallback(s, p, integrationOAuthStart(t, s, p), "existing"))
	p.RegistrationEnabled = false
	var err error
	p, err = s.oauthProviders.update(p.ID, p, nil, false, p.Revision)
	if err != nil {
		t.Fatalf("close registrations: %v", err)
	}
	_, existing := integrationOAuthSession(t, s, integrationOAuthCallback(s, p, integrationOAuthStart(t, s, p), "existing"))
	if existing.User.ID != first.User.ID {
		t.Fatal("existing user changed when registrations closed")
	}
	integrationOAuthRejected(t, integrationOAuthCallback(s, p, integrationOAuthStart(t, s, p), "new"), "oauth_registration_closed")
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM user_identities WHERE provider=?`, p.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("closed registration created an identity: count=%d err=%v", count, err)
	}
}

func TestOAuthProviderStateRequiresProviderNonceAndSingleUse(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	upstream, tokens := integrationOAuthUpstream(t, func(string) string { return `{"sub":"state-user","name":"State User"}` })
	p1 := integrationOAuthProvider(t, s, upstream.URL, "One", "client_secret_post", false)
	p2 := integrationOAuthProvider(t, s, upstream.URL, "Two", "client_secret_post", false)
	start := integrationOAuthStart(t, s, p1)
	wrongProvider := start
	wrongProvider.nonce = &http.Cookie{Name: "oauth_nonce_" + p2.ID, Value: start.nonce.Value}
	integrationOAuthRejected(t, integrationOAuthCallback(s, p2, wrongProvider, "code"), "invalid_state")
	integrationOAuthRejected(t, integrationOAuthCallback(s, p1, start, "code"), "invalid_state")
	start = integrationOAuthStart(t, s, p1)
	wrongNonce := start
	wrongNonce.nonce = &http.Cookie{Name: start.nonce.Name, Value: "a-different-browser-nonce"}
	integrationOAuthRejected(t, integrationOAuthCallback(s, p1, wrongNonce, "code"), "invalid_state")
	integrationOAuthRejected(t, integrationOAuthCallback(s, p1, start, "code"), "invalid_state")
	start = integrationOAuthStart(t, s, p1)
	missingNonce := start
	missingNonce.nonce = nil
	integrationOAuthRejected(t, integrationOAuthCallback(s, p1, missingNonce, "code"), "invalid_state")
	select {
	case <-tokens:
		t.Fatal("invalid state reached the token endpoint")
	default:
	}
	start = integrationOAuthStart(t, s, p1)
	integrationOAuthSession(t, s, integrationOAuthCallback(s, p1, start, "valid"))
	<-tokens
	integrationOAuthRejected(t, integrationOAuthCallback(s, p1, start, "replay"), "invalid_state")
	select {
	case <-tokens:
		t.Fatal("replayed state reached the token endpoint")
	default:
	}
}

func TestOAuthProviderChangeDuringTokenRequestPreventsSession(t *testing.T) {
	t.Parallel()
	for _, change := range []string{"disable", "edit", "delete"} {
		t.Run(change, func(t *testing.T) {
			s := newTestServer(t)
			entered, release := make(chan struct{}), make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/token" {
					close(entered)
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
					_, _ = w.Write([]byte(`{"access_token":"pending-token","token_type":"Bearer"}`))
					return
				}
				_, _ = w.Write([]byte(`{"sub":"pending-user","name":"Pending User"}`))
			}))
			t.Cleanup(upstream.Close)
			p := integrationOAuthProvider(t, s, upstream.URL, "Pending Community", "client_secret_post", false)
			start := integrationOAuthStart(t, s, p)
			result := make(chan *httptest.ResponseRecorder, 1)
			go func() { result <- integrationOAuthCallback(s, p, start, "pending-code") }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("callback did not enter token exchange")
			}
			var err error
			if change == "delete" {
				err = s.oauthProviders.remove(p.ID, p.Revision)
			} else {
				if change == "disable" {
					p.LoginEnabled = false
				} else {
					p.Name = "Edited While Authorizing"
				}
				_, err = s.oauthProviders.update(p.ID, p, nil, false, p.Revision)
			}
			close(release)
			if err != nil {
				t.Fatalf("change provider while exchanging token: %v", err)
			}
			select {
			case rec := <-result:
				integrationOAuthRejected(t, rec, "oauth_unavailable")
			case <-time.After(5 * time.Second):
				t.Fatal("callback did not finish after releasing token endpoint")
			}
			var users, sessions int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM user_sessions`).Scan(&sessions); err != nil {
				t.Fatal(err)
			}
			if users != 0 || sessions != 0 {
				t.Fatalf("stale provider callback persisted users=%d sessions=%d", users, sessions)
			}
		})
	}
}

func TestOAuthProviderNeverFollowsCredentialBearingRedirects(t *testing.T) {
	t.Parallel()
	for _, endpoint := range []string{"token", "userinfo"} {
		t.Run(endpoint, func(t *testing.T) {
			s := newTestServer(t)
			var forwarded atomic.Int32
			sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				forwarded.Add(1)
				_, _ = w.Write([]byte(`{"sub":"redirect-user","access_token":"redirect-token"}`))
			}))
			t.Cleanup(sink.Close)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/"+endpoint {
					http.Redirect(w, r, sink.URL+"/capture", http.StatusTemporaryRedirect)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"access_token":"secret-bearer","token_type":"Bearer"}`))
			}))
			t.Cleanup(upstream.Close)
			p := integrationOAuthProvider(t, s, upstream.URL, "Redirect Community", "client_secret_post", false)
			rec := integrationOAuthCallback(s, p, integrationOAuthStart(t, s, p), "secret-code")
			integrationOAuthRejected(t, rec, "oauth_auth_failed")
			if forwarded.Load() != 0 {
				t.Fatal("OAuth HTTP redirect forwarded a token, client secret, or authorization code")
			}
			if strings.Contains(rec.Header().Get("Location"), "secret-") || strings.Contains(rec.Body.String(), "integration-secret") {
				t.Fatal("failed upstream request disclosed credentials")
			}
		})
	}
}

func TestOAuthProviderRejectsInvalidAndOversizedUpstreamJSON(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, endpoint, body string
		status               int
	}{
		{"token malformed", "token", `{"access_token":`, http.StatusOK},
		{"token trailing JSON", "token", `{"access_token":"token"}{"unexpected":true}`, http.StatusOK},
		{"token oversized", "token", `{"access_token":"` + strings.Repeat("x", (1<<20)+1) + `"}`, http.StatusOK},
		{"token upstream failure", "token", `{"error":"integration-secret private diagnostic"}`, http.StatusBadGateway},
		{"token missing", "token", `{"token_type":"Bearer"}`, http.StatusOK},
		{"token wrong type", "token", `{"access_token":"token","token_type":"MAC"}`, http.StatusOK},
		{"token header injection", "token", `{"access_token":"token\r\nX-Secret: value"}`, http.StatusOK},
		{"userinfo malformed", "userinfo", `<html>private diagnostic integration-secret</html>`, http.StatusOK},
		{"userinfo trailing JSON", "userinfo", `{"sub":"subject"}null`, http.StatusOK},
		{"userinfo oversized", "userinfo", `{"sub":"subject","name":"` + strings.Repeat("x", (1<<20)+1) + `"}`, http.StatusOK},
		{"userinfo upstream failure", "userinfo", `{"private":"integration-secret"}`, http.StatusUnauthorized},
		{"userinfo missing subject", "userinfo", `{"email":"someone@example.test"}`, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestServer(t)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/"+tc.endpoint {
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
					return
				}
				if r.URL.Path == "/token" {
					_, _ = w.Write([]byte(`{"access_token":"good-token","token_type":"Bearer"}`))
					return
				}
				_, _ = w.Write([]byte(`{"sub":"userinfo-user","name":"User"}`))
			}))
			t.Cleanup(upstream.Close)
			p := integrationOAuthProvider(t, s, upstream.URL, "Invalid Response Community", "client_secret_post", false)
			rec := integrationOAuthCallback(s, p, integrationOAuthStart(t, s, p), "private-code")
			integrationOAuthRejected(t, rec, "oauth_auth_failed")
			if strings.Contains(rec.Header().Get("Location")+rec.Body.String(), "integration-secret") || strings.Contains(rec.Header().Get("Location"), "private") {
				t.Fatal("generic login failure exposed upstream diagnostic or credentials")
			}
			var identities int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM user_identities WHERE provider=?`, p.ID).Scan(&identities); err != nil || identities != 0 {
				t.Fatalf("invalid upstream response created identities: count=%d err=%v", identities, err)
			}
		})
	}
}

func TestOAuthProviderAdminJSONRoundTripAndSessionBoundaries(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	upstream, _ := integrationOAuthUpstream(t, func(string) string {
		return `{"sub":"json-user","name":"JSON User","email":"json-user@example.test"}`
	})
	const collection = "/api/settings/oauth/providers"
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
		target := collection
		if method == http.MethodPut || method == http.MethodDelete {
			target += "/unknown"
		}
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, jsonRequest(method, target, `{}`))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous %s provider administration returned %d, want 401", method, rec.Code)
		}
	}
	setup := httptest.NewRecorder()
	s.Handler().ServeHTTP(setup, localSetupRequest(s, `{"password":"oauth-admin-password"}`))
	if setup.Code != http.StatusOK {
		t.Fatalf("admin setup: %d %s", setup.Code, setup.Body.String())
	}
	admin := sessionCookie(t, setup.Result().Cookies())
	adminJSON := func(method, target string, body any) *httptest.ResponseRecorder {
		t.Helper()
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode admin request: %v", err)
		}
		req := jsonRequest(method, target, string(encoded))
		req.AddCookie(admin)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec
	}
	request := map[string]any{
		"id": "email", "name": "JSON Community", "authorization_url": upstream.URL + "/authorize", "token_url": upstream.URL + "/token",
		"userinfo_url": upstream.URL + "/userinfo", "client_id": "json-client", "client_secret": "json-secret",
		"scopes": "openid profile email", "token_auth_method": "client_secret_post", "id_field": "sub", "username_field": "preferred_username",
		"display_name_field": "name", "email_field": "email", "avatar_field": "picture", "login_enabled": true, "registration_enabled": true,
	}
	created := adminJSON(http.MethodPost, collection, request)
	if created.Code != http.StatusCreated {
		t.Fatalf("create provider JSON: %d %s", created.Code, created.Body.String())
	}
	var provider oauthProviderView
	if err := json.Unmarshal(created.Body.Bytes(), &provider); err != nil {
		t.Fatalf("decode created provider: %v", err)
	}
	if provider.ID == "" || provider.ID == "email" || provider.UserInfoURL != upstream.URL+"/userinfo" || !provider.Configured || !provider.ClientSecretConfigured || provider.Revision != 1 {
		t.Fatalf("incorrect provider JSON round trip: %+v", provider)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(created.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["userinfo_url"]; !ok {
		t.Fatal("provider response omitted the public userinfo_url field")
	}
	if _, ok := raw["user_info_url"]; ok {
		t.Fatal("provider response returned the incompatible user_info_url field")
	}
	if _, ok := raw["client_secret"]; ok || strings.Contains(created.Body.String(), "json-secret") {
		t.Fatal("provider creation response exposed client secret")
	}
	if !strings.HasSuffix(provider.CallbackURL, oauthCallbackPath(provider.ID)) {
		t.Fatalf("incorrect callback URL: %q", provider.CallbackURL)
	}
	listed := adminJSON(http.MethodGet, collection, nil)
	var listing struct {
		Providers []oauthProviderView `json:"providers"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &listing); listed.Code != http.StatusOK || err != nil || len(listing.Providers) != 1 || !listing.Providers[0].ClientSecretConfigured || strings.Contains(listed.Body.String(), "json-secret") {
		t.Fatalf("admin list failed to mask configured secret: %d %s err=%v", listed.Code, listed.Body.String(), err)
	}
	public := httptest.NewRecorder()
	s.Handler().ServeHTTP(public, httptest.NewRequest(http.MethodGet, "/api/u/auth/config", nil))
	var publicPayload struct {
		Providers []map[string]json.RawMessage `json:"oauth_providers"`
	}
	if err := json.Unmarshal(public.Body.Bytes(), &publicPayload); public.Code != http.StatusOK || err != nil || len(publicPayload.Providers) != 1 {
		t.Fatalf("public provider config: %d %s err=%v", public.Code, public.Body.String(), err)
	}
	allowedPublicKeys := map[string]bool{"id": true, "name": true, "start_url": true, "registration_enabled": true}
	for key := range publicPayload.Providers[0] {
		if !allowedPublicKeys[key] {
			t.Fatalf("public provider config exposed private field %q", key)
		}
	}
	if strings.Contains(public.Body.String(), upstream.URL) || strings.Contains(public.Body.String(), "json-secret") || strings.Contains(public.Body.String(), "json-client") {
		t.Fatal("public config exposed provider endpoint or client credentials")
	}
	request["id"], request["name"], request["expected_revision"] = "replacement-id", "Updated JSON Community", provider.Revision
	delete(request, "client_secret")
	updated := adminJSON(http.MethodPut, collection+"/"+provider.ID, request)
	var changed oauthProviderView
	if err := json.Unmarshal(updated.Body.Bytes(), &changed); updated.Code != http.StatusOK || err != nil || changed.ID != provider.ID || changed.Name != "Updated JSON Community" || changed.Revision != provider.Revision+1 || changed.UserInfoURL != upstream.URL+"/userinfo" || !changed.ClientSecretConfigured {
		t.Fatalf("update provider JSON: %d %s err=%v", updated.Code, updated.Body.String(), err)
	}
	stale := adminJSON(http.MethodPut, collection+"/"+provider.ID, request)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale provider update returned %d, want 409: %s", stale.Code, stale.Body.String())
	}
	userSession, status := integrationOAuthSession(t, s, integrationOAuthCallback(s, changed.OAuthProvider, integrationOAuthStart(t, s, changed.OAuthProvider), "json-code"))
	if status.LoginMethod == nil || status.LoginMethod.ID != changed.ID || status.LoginMethod.Name != changed.Name || status.LoginMethod.Type != "oauth" {
		t.Fatalf("OAuth status lost the configured login method: %+v", status.LoginMethod)
	}
	userAdminRequest := jsonRequest(http.MethodGet, collection, "")
	userAdminRequest.AddCookie(userSession)
	userAdminResponse := httptest.NewRecorder()
	s.Handler().ServeHTTP(userAdminResponse, userAdminRequest)
	if userAdminResponse.Code != http.StatusUnauthorized {
		t.Fatalf("OAuth user session gained admin access: %d", userAdminResponse.Code)
	}
	adminUserRequest := httptest.NewRequest(http.MethodGet, "/api/u/status", nil)
	adminUserRequest.AddCookie(admin)
	adminUserResponse := httptest.NewRecorder()
	s.Handler().ServeHTTP(adminUserResponse, adminUserRequest)
	if adminUserResponse.Code != http.StatusUnauthorized {
		t.Fatalf("admin session became a portal user session: %d", adminUserResponse.Code)
	}
	staleDelete := adminJSON(http.MethodDelete, collection+"/"+changed.ID, map[string]any{"expected_revision": provider.Revision})
	if staleDelete.Code != http.StatusConflict {
		t.Fatalf("stale provider deletion returned %d, want 409", staleDelete.Code)
	}
	deleted := adminJSON(http.MethodDelete, collection+"/"+changed.ID, map[string]any{"expected_revision": changed.Revision})
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete provider JSON: %d %s", deleted.Code, deleted.Body.String())
	}
}

func TestOAuthProviderEmailLoginReportsEmailMethod(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	user, err := s.users.createEmailUser("email-method@example.test", "email-method-password", s.now())
	if err != nil {
		t.Fatalf("create email user: %v", err)
	}
	login := httptest.NewRecorder()
	s.Handler().ServeHTTP(login, jsonRequest(http.MethodPost, "/api/u/auth/email/login", `{"email":"email-method@example.test","password":"email-method-password"}`))
	var loggedIn userStatusResponse
	if err := json.Unmarshal(login.Body.Bytes(), &loggedIn); login.Code != http.StatusOK || err != nil {
		t.Fatalf("email login: %d %s err=%v", login.Code, login.Body.String(), err)
	}
	if loggedIn.User.ID != user.ID || loggedIn.LoginMethod == nil || loggedIn.LoginMethod.ID != "email" || loggedIn.LoginMethod.Name != "邮箱" || loggedIn.LoginMethod.Type != "email" {
		t.Fatalf("email login response lost email method: %+v", loggedIn)
	}
	statusRequest := httptest.NewRequest(http.MethodGet, "/api/u/status", nil)
	for _, cookie := range login.Result().Cookies() {
		if cookie.Name == userSessionCookieName {
			statusRequest.AddCookie(cookie)
		}
	}
	status := httptest.NewRecorder()
	s.Handler().ServeHTTP(status, statusRequest)
	var refreshed userStatusResponse
	if err := json.Unmarshal(status.Body.Bytes(), &refreshed); status.Code != http.StatusOK || err != nil {
		t.Fatalf("email session status: %d %s err=%v", status.Code, status.Body.String(), err)
	}
	if refreshed.User.ID != user.ID || refreshed.LoginMethod == nil || refreshed.LoginMethod.ID != "email" || refreshed.LoginMethod.Name != "邮箱" || refreshed.LoginMethod.Type != "email" {
		t.Fatalf("email session refresh lost email method: %+v", refreshed)
	}
}
