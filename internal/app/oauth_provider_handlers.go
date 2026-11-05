package app

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

type oauthProviderView struct {
	OAuthProvider
	CallbackURL string `json:"callback_url"`
	Configured  bool   `json:"configured"`
}

type oauthProviderRequest struct {
	OAuthProvider
	ClientSecretInput *string `json:"client_secret"`
	ClearClientSecret bool    `json:"clear_client_secret"`
	ExpectedRevision  int64   `json:"expected_revision"`
	CallbackURL       string  `json:"callback_url,omitempty"`
	Configured        bool    `json:"configured,omitempty"`
}

func (s *Server) registerOAuthProviderRoutes() {
	s.mux.HandleFunc("GET /api/settings/oauth/providers", s.handleListOAuthProviders)
	s.mux.HandleFunc("POST /api/settings/oauth/providers", s.handleCreateOAuthProvider)
	s.mux.HandleFunc("PUT /api/settings/oauth/providers/{providerID}", s.handleUpdateOAuthProvider)
	s.mux.HandleFunc("DELETE /api/settings/oauth/providers/{providerID}", s.handleDeleteOAuthProvider)
	s.mux.HandleFunc("GET /api/u/auth/oauth/{providerID}/start", s.handleOAuthStart)
	s.mux.HandleFunc("GET /api/u/auth/oauth/{providerID}/callback", s.handleOAuthCallback)
}

func oauthCallbackPath(id string) string {
	if id == "linuxdo" {
		return "/api/u/auth/linuxdo/callback"
	}
	return "/api/u/auth/oauth/" + url.PathEscape(id) + "/callback"
}

func oauthStartPath(id string) string {
	if id == "linuxdo" {
		return "/api/u/auth/linuxdo/start"
	}
	return "/api/u/auth/oauth/" + url.PathEscape(id) + "/start"
}

func oauthProviderConfigured(p OAuthProvider) bool {
	return p.ClientID != "" && p.ClientSecret != "" && p.AuthorizationURL != "" && p.TokenURL != "" && p.UserInfoURL != "" && p.IDField != ""
}

func (s *Server) oauthProviderView(r *http.Request, p OAuthProvider) oauthProviderView {
	p.ClientSecretConfigured = p.ClientSecret != ""
	return oauthProviderView{OAuthProvider: p, CallbackURL: strings.TrimRight(s.baseURL(r), "/") + oauthCallbackPath(p.ID), Configured: oauthProviderConfigured(p)}
}

func (s *Server) handleListOAuthProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := s.oauthProviders.list()
	if err != nil {
		writeOAuthProviderError(w, err)
		return
	}
	views := make([]oauthProviderView, 0, len(providers))
	for _, p := range providers {
		views = append(views, s.oauthProviderView(r, p))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"providers": views})
}

func (s *Server) handleCreateOAuthProvider(w http.ResponseWriter, r *http.Request) {
	var req oauthProviderRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := normalizeOAuthProvider(&req.OAuthProvider); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	secret := ""
	if req.ClientSecretInput != nil {
		secret = strings.TrimSpace(*req.ClientSecretInput)
	}
	if len(secret) > 8192 {
		writeError(w, http.StatusBadRequest, "Client Secret 过长")
		return
	}
	// IDs are server-issued and cannot be reused to inherit another site's users.
	req.ID = ""
	p, err := s.oauthProviders.create(req.OAuthProvider, secret)
	if err != nil {
		writeOAuthProviderError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, s.oauthProviderView(r, p))
}

func (s *Server) handleUpdateOAuthProvider(w http.ResponseWriter, r *http.Request) {
	var req oauthProviderRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ExpectedRevision < 1 {
		writeError(w, http.StatusBadRequest, "请刷新后提供当前配置版本")
		return
	}
	if err := normalizeOAuthProvider(&req.OAuthProvider); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ClientSecretInput != nil && len(*req.ClientSecretInput) > 8192 {
		writeError(w, http.StatusBadRequest, "Client Secret 过长")
		return
	}
	p, err := s.oauthProviders.update(r.PathValue("providerID"), req.OAuthProvider, req.ClientSecretInput, req.ClearClientSecret, req.ExpectedRevision)
	if err != nil {
		writeOAuthProviderError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.oauthProviderView(r, p))
}

func (s *Server) handleDeleteOAuthProvider(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ExpectedRevision < 1 {
		writeError(w, http.StatusBadRequest, "请刷新后提供当前配置版本")
		return
	}
	if err := s.oauthProviders.remove(r.PathValue("providerID"), req.ExpectedRevision); err != nil {
		writeOAuthProviderError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeOAuthProviderError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errOAuthProviderNotFound):
		writeError(w, http.StatusNotFound, "授权方式不存在")
	case errors.Is(err, errOAuthProviderConflict):
		writeError(w, http.StatusConflict, "配置已改变，或已有用户绑定此站点；请刷新重试，站点身份变更请新建授权方式")
	default:
		writeError(w, http.StatusInternalServerError, "授权方式保存或读取失败")
	}
}

func normalizeOAuthProvider(p *OAuthProvider) error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || utf8.RuneCountInString(p.Name) > 80 || strings.IndexFunc(p.Name, unicode.IsControl) >= 0 {
		return errors.New("名称不能为空，且最多为 80 个字符")
	}
	for _, field := range []*string{&p.AuthorizationURL, &p.TokenURL, &p.UserInfoURL} {
		*field = strings.TrimSpace(*field)
		if err := validateOAuthEndpoint(*field); err != nil {
			return err
		}
	}
	p.ClientID = strings.TrimSpace(p.ClientID)
	if len(p.ClientID) > 1024 || strings.IndexFunc(p.ClientID, unicode.IsControl) >= 0 {
		return errors.New("Client ID 无效或过长")
	}
	p.Scopes = strings.Join(strings.Fields(p.Scopes), " ")
	if len(p.Scopes) > 2048 {
		return errors.New("Scope 过长")
	}
	if p.TokenAuthMethod == "" {
		p.TokenAuthMethod = "client_secret_post"
	}
	if p.TokenAuthMethod != "client_secret_post" && p.TokenAuthMethod != "client_secret_basic" {
		return errors.New("不支持的客户端认证方式")
	}
	if p.IDField == "" {
		p.IDField = "sub"
	}
	for _, field := range []*string{&p.IDField, &p.UsernameField, &p.DisplayNameField, &p.EmailField, &p.AvatarField} {
		*field = strings.TrimSpace(*field)
		if len(*field) > 200 || strings.IndexFunc(*field, func(r rune) bool {
			return !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._-", r))
		}) >= 0 {
			return errors.New("字段映射仅支持由字母、数字、下划线、连字符组成的点分隔路径")
		}
		if *field != "" {
			for _, part := range strings.Split(*field, ".") {
				if part == "" {
					return errors.New("字段映射路径不能为空")
				}
			}
		}
	}
	if p.SortOrder < -10000 || p.SortOrder > 10000 {
		return errors.New("排序值需在 -10000 到 10000 之间")
	}
	if p.IDField == "" {
		return errors.New("用户 ID 字段映射不能为空")
	}
	return nil
}

func validateOAuthEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 4096 || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return errors.New("请填写有效的授权、令牌和用户信息地址，地址不可包含密码或片段")
	}
	host := strings.ToLower(u.Hostname())
	loopback := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return errors.New("OAuth 地址需使用 HTTPS；本机调试地址可使用 HTTP")
	}
	return nil
}
