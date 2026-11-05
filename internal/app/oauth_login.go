package app

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type publicOAuthProvider struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	StartURL            string `json:"start_url"`
	RegistrationEnabled bool   `json:"registration_enabled"`
}

// A legacy configuration remains readable for old integrations. Once the
// provider is migrated, the provider record is the only runtime source.
func (s *Server) effectiveOAuthProvider(id string) (OAuthProvider, error) {
	if s.oauthProviders != nil {
		p, err := s.oauthProviders.get(id)
		if err == nil || !errors.Is(err, errOAuthProviderNotFound) {
			return p, err
		}
		if id != "linuxdo" || s.oauthProviders.legacyLinuxDoDeleted() {
			return OAuthProvider{}, err
		}
	}
	if id != "linuxdo" {
		return OAuthProvider{}, errOAuthProviderNotFound
	}
	return OAuthProvider{ID: "linuxdo", Name: "LinuxDO", AuthorizationURL: linuxDoAuthorizeURL, TokenURL: linuxDoTokenURL, UserInfoURL: linuxDoUserURL,
		ClientID: s.legacyLinuxDoClientID(), ClientSecret: s.legacyLinuxDoClientSecret(), Scopes: "read", TokenAuthMethod: "client_secret_post",
		IDField: "id", UsernameField: "username", DisplayNameField: "name", EmailField: "email", AvatarField: "avatar_url",
		LoginEnabled: s.settings.getBool(settingKeyLinuxDoLoginEnabled, true), RegistrationEnabled: s.settings.getBool(settingKeyLinuxDoRegistrationEnabled, true)}, nil
}

func (s *Server) publicOAuthProviders() ([]publicOAuthProvider, error) {
	list := []publicOAuthProvider{}
	var providers []OAuthProvider
	if s.oauthProviders != nil {
		var err error
		providers, err = s.oauthProviders.list()
		if err != nil {
			return nil, err
		}
	}
	hasLegacy := false
	for _, p := range providers {
		if p.ID == "linuxdo" {
			hasLegacy = true
		}
	}
	if !hasLegacy {
		if p, err := s.effectiveOAuthProvider("linuxdo"); err == nil && oauthProviderConfigured(p) {
			providers = append(providers, p)
		}
	}
	for _, p := range providers {
		if p.LoginEnabled && oauthProviderConfigured(p) {
			list = append(list, publicOAuthProvider{ID: p.ID, Name: p.Name, StartURL: oauthStartPath(p.ID), RegistrationEnabled: p.RegistrationEnabled})
		}
	}
	return list, nil
}

func oauthNonceCookie(id string) (string, string) {
	if id == "linuxdo" {
		return linuxDoOAuthNonceCookieName, "/api/u/auth/linuxdo"
	}
	return "oauth_nonce_" + id, "/api/u/auth/oauth/" + id
}

func (s *Server) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	s.startOAuthProvider(w, r, r.PathValue("providerID"))
}

func (s *Server) startOAuthProvider(w http.ResponseWriter, r *http.Request, id string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	p, err := s.effectiveOAuthProvider(id)
	if err != nil || !p.LoginEnabled || !oauthProviderConfigured(p) {
		writeError(w, http.StatusServiceUnavailable, "此登录方式未开放或尚未配置")
		return
	}
	nonce, err := generateSessionID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "创建授权请求失败")
		return
	}
	verifier := ""
	if p.PKCEEnabled {
		verifier, err = generateSessionID()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "创建授权请求失败")
			return
		}
	}
	redirectURI := strings.TrimRight(s.baseURL(r), "/") + oauthCallbackPath(p.ID)
	state, err := s.oauthStates.createForProvider(nonce, p.ID, p.Revision, verifier, redirectURI, s.now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "创建授权请求失败")
		return
	}
	u, err := url.Parse(p.AuthorizationURL)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "授权地址无效")
		return
	}
	q := u.Query()
	// Only supported protocol parameters may originate from this application.
	for _, key := range []string{"client_secret", "code", "code_verifier", "request_uri", "redirect_uri", "response_mode", "nonce"} {
		q.Del(key)
	}
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("state", state)
	q.Set("scope", p.Scopes)
	q.Del("code_challenge")
	q.Del("code_challenge_method")
	if verifier != "" {
		digest := sha256.Sum256([]byte(verifier))
		q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(digest[:]))
		q.Set("code_challenge_method", "S256")
	}
	u.RawQuery = q.Encode()
	name, path := oauthNonceCookie(p.ID)
	http.SetCookie(w, &http.Cookie{Name: name, Value: nonce, Path: path, HttpOnly: true, Secure: s.secureCookies(), SameSite: http.SameSiteLaxMode, MaxAge: int(oauthStateTTL.Seconds()), Expires: s.now().Add(oauthStateTTL)})
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	s.finishOAuthProvider(w, r, r.PathValue("providerID"))
}

func (s *Server) finishOAuthProvider(w http.ResponseWriter, r *http.Request, id string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	name, path := oauthNonceCookie(id)
	nonce := ""
	if cookie, err := r.Cookie(name); err == nil {
		nonce = cookie.Value
	}
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: path, HttpOnly: true, Secure: s.secureCookies(), SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0)})
	p, err := s.effectiveOAuthProvider(id)
	if err != nil || !p.LoginEnabled || !oauthProviderConfigured(p) {
		redirectUserAuthError(w, r, "oauth_unavailable")
		return
	}
	state, ok := s.oauthStates.consumeForProvider(r.URL.Query().Get("state"), nonce, p.ID, p.Revision, s.now())
	if !ok {
		redirectUserAuthError(w, r, "invalid_state")
		return
	}
	if r.URL.Query().Get("error") != "" {
		message := "oauth_auth_failed"
		if r.URL.Query().Get("error") == "access_denied" {
			message = "access_denied"
		}
		redirectUserAuthError(w, r, message)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" || len(code) > 8192 {
		redirectUserAuthError(w, r, "missing_code")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), maxDuration(s.config.RequestTimeout, 20*time.Second))
	defer cancel()
	profile, err := s.fetchOAuthProfile(ctx, p, code, state.redirectURI, state.verifier)
	if err != nil {
		redirectUserAuthError(w, r, "oauth_auth_failed")
		return
	}
	// A provider may have been changed, removed, or disabled during network I/O.
	current, err := s.effectiveOAuthProvider(id)
	if err != nil || !current.LoginEnabled || current.Revision != p.Revision || !oauthProviderConfigured(current) {
		redirectUserAuthError(w, r, "oauth_unavailable")
		return
	}
	user, _, err := s.users.upsertOAuthUser(p.ID, profile, current.RegistrationEnabled, s.now())
	if err != nil {
		message := "oauth_registration_closed"
		if errors.Is(err, errUserDisabled) {
			message = "user_disabled"
		} else if !errors.Is(err, errOAuthRegistrationClosed) {
			message = "user_create_failed"
		}
		redirectUserAuthError(w, r, message)
		return
	}
	if err := s.setUserSessionCookieForMethod(w, user.ID, p.ID, current.Name, s.now()); err != nil {
		redirectUserAuthError(w, r, "session_failed")
		return
	}
	http.Redirect(w, r, "/u", http.StatusFound)
}

const maxOAuthResponseBytes = 1 << 20

func decodeOAuthResponse(resp *http.Response, dst any) error {
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("OAuth endpoint rejected request")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxOAuthResponseBytes+1))
	if err != nil || len(data) > maxOAuthResponseBytes {
		return errors.New("OAuth response exceeds limit or cannot be read")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(dst); err != nil {
		return errors.New("OAuth endpoint did not return valid JSON")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("OAuth endpoint returned multiple JSON values")
	}
	return nil
}

func (s *Server) fetchOAuthProfile(ctx context.Context, p OAuthProvider, code, redirectURI, verifier string) (LinuxDoProfile, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI}}
	if verifier != "" {
		form.Set("code_verifier", verifier)
	}
	if p.TokenAuthMethod != "client_secret_basic" {
		form.Set("client_id", p.ClientID)
		form.Set("client_secret", p.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return LinuxDoProfile{}, errors.New("invalid OAuth token endpoint")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if p.TokenAuthMethod == "client_secret_basic" {
		req.SetBasicAuth(url.QueryEscape(p.ClientID), url.QueryEscape(p.ClientSecret))
	}
	client := &http.Client{Timeout: maxDuration(s.config.RequestTimeout, 20*time.Second), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return LinuxDoProfile{}, errors.New("OAuth token endpoint unavailable")
	}
	var token linuxDoTokenResponse
	if err := decodeOAuthResponse(resp, &token); err != nil {
		return LinuxDoProfile{}, err
	}
	if token.Error != "" || strings.TrimSpace(token.AccessToken) == "" || len(token.AccessToken) > 32768 || strings.ContainsAny(token.AccessToken, "\r\n") {
		return LinuxDoProfile{}, errors.New("OAuth access token missing or invalid")
	}
	if token.TokenType != "" && !strings.EqualFold(token.TokenType, "Bearer") {
		return LinuxDoProfile{}, errors.New("unsupported OAuth token type")
	}
	userReq, err := http.NewRequestWithContext(ctx, http.MethodGet, p.UserInfoURL, nil)
	if err != nil {
		return LinuxDoProfile{}, errors.New("invalid OAuth userinfo endpoint")
	}
	userReq.Header.Set("Authorization", "Bearer "+token.AccessToken)
	userReq.Header.Set("Accept", "application/json")
	userResp, err := client.Do(userReq)
	if err != nil {
		return LinuxDoProfile{}, errors.New("OAuth userinfo endpoint unavailable")
	}
	var raw map[string]any
	if err := decodeOAuthResponse(userResp, &raw); err != nil {
		return LinuxDoProfile{}, err
	}
	profile := LinuxDoProfile{ID: oauthMappedString(raw, p.IDField), Username: oauthMappedString(raw, p.UsernameField), DisplayName: oauthMappedString(raw, p.DisplayNameField), Email: oauthMappedString(raw, p.EmailField), AvatarURL: oauthMappedString(raw, p.AvatarField)}
	// LinuxDO's historical API occasionally wraps its profile in "user".
	if profile.ID == "" && p.ID == "linuxdo" {
		if nested, ok := raw["user"].(map[string]any); ok {
			profile, err = linuxDoProfileFromPayload(nested)
		}
	}
	if err != nil || profile.ID == "" || len(profile.ID) > 512 {
		return LinuxDoProfile{}, errors.New("OAuth stable user ID missing or invalid")
	}
	if len(profile.DisplayName) > 1024 || len(profile.Username) > 1024 || len(profile.Email) > 512 || len(profile.AvatarURL) > 4096 {
		return LinuxDoProfile{}, errors.New("OAuth profile exceeds limit")
	}
	if profile.AvatarURL != "" {
		u, err := url.Parse(profile.AvatarURL)
		if err != nil || u.User != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			profile.AvatarURL = ""
		}
	}
	return profile, nil
}

func oauthMappedString(raw map[string]any, path string) string {
	if path == "" {
		return ""
	}
	var value any = raw
	for _, part := range strings.Split(path, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return ""
		}
		value = object[part]
	}
	return stringFromAny(value)
}
