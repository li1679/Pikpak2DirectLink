package app

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type userAuthConfigResponse struct {
	OAuthProviders             []publicOAuthProvider `json:"oauth_providers"`
	LinuxDoAvailable           bool                  `json:"linuxdo_available"`
	LinuxDoConfigured          bool                  `json:"linuxdo_configured"`
	LinuxDoLoginEnabled        bool                  `json:"linuxdo_login_enabled"`
	LinuxDoRegistrationEnabled bool                  `json:"linuxdo_registration_enabled"`
	EmailLoginEnabled          bool                  `json:"email_login_enabled"`
	EmailRegistrationEnabled   bool                  `json:"email_registration_enabled"`
	LinuxDoStartURL            string                `json:"linuxdo_start_url"`
}

type userStatusResponse struct {
	LoginMethod   *userLoginMethod       `json:"login_method,omitempty"`
	User          User                   `json:"user"`
	Quota         UserQuota              `json:"quota"`
	Subscriptions []UserSubscription     `json:"subscriptions"`
	Auth          userAuthConfigResponse `json:"auth"`
	Queue         struct {
		Waiting int  `json:"waiting"`
		Active  bool `json:"active"`
	} `json:"queue"`
}

type emailAuthRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type redeemCDKRequest struct {
	Code string `json:"code"`
}

type selectItemsRequest struct {
	ItemIDs []string `json:"item_ids"`
	ItemID  string   `json:"item_id"`
}

const linuxDoOAuthNonceCookieName = "linuxdo_oauth_nonce"

func (s *Server) handleUserPortal(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(s.userHTML)
}

func (s *Server) userAuthConfigPayload(r *http.Request) userAuthConfigResponse {
	loginEnabled := s.settings.getBool(settingKeyLinuxDoLoginEnabled, true)
	registrationEnabled := s.settings.getBool(settingKeyLinuxDoRegistrationEnabled, true)
	if p, err := s.effectiveOAuthProvider("linuxdo"); err == nil {
		loginEnabled = p.LoginEnabled
		registrationEnabled = p.RegistrationEnabled
	}
	configured := s.linuxDoOAuthConfigured()
	providers, err := s.publicOAuthProviders()
	if err != nil {
		providers = []publicOAuthProvider{}
	}
	return userAuthConfigResponse{
		OAuthProviders:             providers,
		LinuxDoAvailable:           loginEnabled && configured,
		LinuxDoConfigured:          configured,
		LinuxDoLoginEnabled:        loginEnabled,
		LinuxDoRegistrationEnabled: registrationEnabled,
		EmailLoginEnabled:          s.settings.getBool(settingKeyEmailLoginEnabled, true),
		EmailRegistrationEnabled:   s.settings.getBool(settingKeyEmailRegistrationEnabled, false),
		LinuxDoStartURL:            "/api/u/auth/linuxdo/start",
	}
}

func (s *Server) handleUserAuthConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if _, err := s.publicOAuthProviders(); err != nil {
		writeError(w, http.StatusServiceUnavailable, "读取登录方式失败，请稍后重试")
		return
	}
	writeJSON(w, http.StatusOK, s.userAuthConfigPayload(r))
}

func (s *Server) currentUser(r *http.Request) (User, bool) {
	cookie, err := r.Cookie(userSessionCookieName)
	if err != nil {
		return User{}, false
	}
	user, ok, err := s.users.userForSession(cookie.Value, s.now())
	return user, err == nil && ok
}

func (s *Server) setUserSessionCookie(w http.ResponseWriter, userID string, now time.Time) error {
	return s.setUserSessionCookieForMethod(w, userID, "email", "邮箱", now)
}

func (s *Server) setUserSessionCookieForMethod(w http.ResponseWriter, userID, providerID, providerName string, now time.Time) error {
	token, err := s.users.createSessionForMethod(userID, providerID, providerName, now)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     userSessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookies(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(userSessionMaxAge.Seconds()),
		Expires:  now.Add(userSessionMaxAge),
	})
	return nil
}

func clearUserSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     userSessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "cdk",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func (s *Server) userStatusPayload(user User, r *http.Request, now time.Time) (userStatusResponse, error) {
	quota, subs, err := s.users.quota(user.ID, now)
	if err != nil {
		return userStatusResponse{}, err
	}
	resp := userStatusResponse{
		LoginMethod:   s.userLoginMethod(r, user.ID),
		User:          user,
		Quota:         quota,
		Subscriptions: subs,
		Auth:          s.userAuthConfigPayload(r),
	}
	resp.Queue.Waiting = s.resolver.waiting()
	resp.Queue.Active = s.resolver.active()
	return resp, nil
}

func (s *Server) handleLinuxDoAuthStart(w http.ResponseWriter, r *http.Request) {
	s.startOAuthProvider(w, r, "linuxdo")
}

func (s *Server) handleLinuxDoAuthCallback(w http.ResponseWriter, r *http.Request) {
	s.finishOAuthProvider(w, r, "linuxdo")
}
func clearLinuxDoOAuthNonceCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     linuxDoOAuthNonceCookieName,
		Value:    "",
		Path:     "/api/u/auth/linuxdo",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
	})
}

func redirectUserAuthError(w http.ResponseWriter, r *http.Request, message string) {
	q := url.Values{}
	q.Set("error", message)
	http.Redirect(w, r, "/u?"+q.Encode(), http.StatusFound)
}

func (s *Server) handleEmailRegister(w http.ResponseWriter, r *http.Request) {
	if !s.settings.getBool(settingKeyEmailRegistrationEnabled, false) {
		writeError(w, http.StatusForbidden, "email registration is disabled")
		return
	}
	var req emailAuthRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	attempt := authAttemptForRequest(r, "register:"+normalizeEmail(req.Email))
	admission, retryAfter, admitted := s.authLimiter.admit(attempt, s.now())
	if !admitted {
		writeAuthRateLimit(w, retryAfter)
		return
	}
	defer func() { admission.cancel(s.now()) }()

	user, err := s.users.createEmailUserContext(r.Context(), req.Email, req.Password, s.now())
	if errors.Is(err, errPasswordHashBusy) {
		writePasswordHashBusy(w)
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	admission.consume(s.now())
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errEmailExists) {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	if err := s.setUserSessionCookie(w, user.ID, s.now()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	payload, err := s.userStatusPayload(user, r, s.now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	payload.LoginMethod = &userLoginMethod{ID: "email", Name: "邮箱", Type: "email"}
	writeJSON(w, http.StatusCreated, payload)
}

func (s *Server) handleEmailLogin(w http.ResponseWriter, r *http.Request) {
	if !s.settings.getBool(settingKeyEmailLoginEnabled, true) {
		writeError(w, http.StatusForbidden, "email login is disabled")
		return
	}
	var req emailAuthRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	attempt := authAttemptForRequest(r, "email:"+normalizeEmail(req.Email))
	admission, retryAfter, admitted := s.authLimiter.admit(attempt, s.now())
	if !admitted {
		writeAuthRateLimit(w, retryAfter)
		return
	}
	defer func() { admission.cancel(s.now()) }()

	user, err := s.users.verifyEmailLoginContext(r.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, errPasswordHashBusy) {
			writePasswordHashBusy(w)
			return
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		if errors.Is(err, errInvalidCredentials) {
			admission.fail(s.now())
			writeError(w, http.StatusUnauthorized, errInvalidCredentials.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to authenticate user")
		return
	}
	admission.succeed(s.now())
	if err := s.setUserSessionCookie(w, user.ID, s.now()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	payload, err := s.userStatusPayload(user, r, s.now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	payload.LoginMethod = &userLoginMethod{ID: "email", Name: "邮箱", Type: "email"}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleUserStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "user session is invalid or expired")
		return
	}
	payload, err := s.userStatusPayload(user, r, s.now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleUserLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(userSessionCookieName); err == nil {
		_ = s.users.deleteSession(cookie.Value)
	}
	clearUserSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}

func (s *Server) handleUserRedeemCDK(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "user session is invalid or expired")
		return
	}
	var req redeemCDKRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.users.redeemCDK(user.ID, req.Code, s.now()); err != nil {
		writeVoucherError(w, err)
		return
	}
	payload, err := s.userStatusPayload(user, r, s.now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func writeVoucherError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errCDKNotFound):
		writeError(w, http.StatusNotFound, "CDK not found")
	case errors.Is(err, errVoucherRedeemed):
		writeError(w, http.StatusConflict, "CDK has already been redeemed")
	case errors.Is(err, errVoucherRevoked):
		writeError(w, http.StatusForbidden, "CDK has been revoked")
	case errors.Is(err, errCDKExhausted):
		writeError(w, http.StatusForbidden, "CDK has no redeemable quota")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (s *Server) handleUserCreateJob(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "user session is invalid or expired")
		return
	}
	if !s.accounts.HasAccounts() {
		writeError(w, http.StatusServiceUnavailable, "service is not ready")
		return
	}

	var req createJobRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Input = strings.TrimSpace(req.Input)
	req.PassCode = strings.TrimSpace(req.PassCode)
	req.Mode = strings.ToLower(strings.TrimSpace(req.Mode))
	if req.Mode == "" {
		req.Mode = "direct"
	}
	if req.Mode != "direct" && req.Mode != "proxy" {
		writeError(w, http.StatusBadRequest, "mode must be direct or proxy")
		return
	}
	if err := s.users.hasQuota(user.ID, 1, req.Mode == "proxy", s.now()); err != nil {
		writeUserQuotaError(w, err)
		return
	}

	lines := splitResourceLineSpecs(req.Input)
	if len(lines) > 1 {
		parent, status, msg := s.createBatchJob(lines, req.Mode, req.PassCode, user.ID, priorityUser, s.baseURL(r))
		if status != 0 {
			if status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable {
				w.Header().Set("Retry-After", "30")
			}
			writeError(w, status, msg)
			return
		}
		s.logJob(LogInfo, parent.ID, "user batch resolve job created", "user="+user.ID, "links="+itoa(len(lines)))
		view := toUserJobView(parent)
		view.QueueAhead = s.resolver.position(parent.ID)
		writeJSON(w, http.StatusAccepted, view)
		return
	}
	rawInput := req.Input
	if len(lines) == 1 {
		rawInput = lines[0].raw
		req.Input = lines[0].clean
	}

	kind, err := detectResourceKind(req.Input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var share *ShareState
	if kind == ResourceShare {
		var passCode string
		share, passCode, err = shareStateAndPassCode(rawInput, req.PassCode)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		req.PassCode = passCode
	}

	now := s.now()
	jobID, err := newJobID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate a secure job ID")
		return
	}
	job := &Job{
		ID:            jobID,
		Kind:          kind,
		Mode:          req.Mode,
		Input:         req.Input,
		OriginalInput: rawInput,
		PassCode:      req.PassCode,
		Status:        JobQueued,
		Stage:         StageTransfer,
		Message:       "queued",
		BaseURL:       s.baseURL(r),
		UserID:        user.ID,
		ProxyAllowed:  req.Mode == "proxy",
		Share:         share,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	createErr := s.admission.withCapacity(user.ID, 1, func() error { return s.jobs.create(job) })
	if createErr != nil {
		var limitErr *jobAdmissionError
		if errors.As(createErr, &limitErr) {
			w.Header().Set("Retry-After", "30")
			if limitErr.Global {
				writeError(w, http.StatusServiceUnavailable, "resolve service is at capacity")
			} else {
				writeError(w, http.StatusTooManyRequests, "too many active user jobs")
			}
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to persist job")
		return
	}
	s.logJob(LogInfo, job.ID, "user resolve job created", "user="+user.ID, "source="+string(kind))
	if err := s.resolver.enqueue(job.ID, priorityUser, func(ctx context.Context) {
		s.processJob(ctx, job.ID)
	}); err != nil {
		if errors.Is(err, errResolveQueueFull) {
			w.Header().Set("Retry-After", "30")
			writeError(w, http.StatusServiceUnavailable, "resolve service is at capacity")
		} else {
			writeError(w, http.StatusServiceUnavailable, "resolve queue is shutting down")
		}
		return
	}

	view := toUserJobView(job)
	view.QueueAhead = s.resolver.position(job.ID)
	writeJSON(w, http.StatusAccepted, view)
}

func writeUserQuotaError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errUserQuotaExhausted):
		writeError(w, http.StatusForbidden, "user quota has been used up")
	default:
		var overdraw errUserQuotaOverdraw
		if errors.As(err, &overdraw) {
			writeError(w, http.StatusForbidden, overdraw.Error())
			return
		}
		writeError(w, http.StatusForbidden, err.Error())
	}
}

func (s *Server) handleUserGetJob(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "user session is invalid or expired")
		return
	}
	jobID := r.PathValue("id")
	job, ok, err := s.jobs.getWithError(jobID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read job")
		return
	}
	if !ok || job.UserID != user.ID {
		expired, err := s.jobDetailsExpired(jobID, resolveJobOwnerUser, user.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read job")
			return
		}
		if expired {
			writeError(w, http.StatusGone, "job details have expired")
			return
		}
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	view := toUserJobView(job)
	view.QueueAhead = s.resolver.position(job.ID)
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleUserHistoryList(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "user session is invalid or expired")
		return
	}
	now := s.now()
	s.cleanupResolveHistory(now)
	history, err := s.history.listByUser(user.ID, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"history": history})
}

func (s *Server) handleUserHistoryGet(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "user session is invalid or expired")
		return
	}
	now := s.now()
	s.cleanupResolveHistory(now)
	detail, ok, err := s.history.getByUser(user.ID, r.PathValue("id"), now)
	if errors.Is(err, errResolveJobDetailsExpired) {
		writeError(w, http.StatusGone, "history details have expired")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "history not found")
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleUserSelectItem(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "user session is invalid or expired")
		return
	}
	jobID := r.PathValue("id")
	job, ok, err := s.jobs.getWithError(jobID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read job")
		return
	}
	if !ok || job.UserID != user.ID {
		expired, err := s.jobDetailsExpired(jobID, resolveJobOwnerUser, user.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read job")
			return
		}
		if expired {
			writeError(w, http.StatusGone, "job details have expired")
			return
		}
		writeError(w, http.StatusNotFound, "job not found")
		return
	}

	var req selectItemsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ids := req.ItemIDs
	if len(ids) == 0 && req.ItemID != "" {
		ids = []string{req.ItemID}
	}

	updated, status, msg := s.applyItemsSelection(job.ID, ids)
	if status != 0 {
		writeError(w, status, msg)
		return
	}
	view := toUserJobView(updated)
	view.QueueAhead = s.resolver.position(updated.ID)
	writeJSON(w, http.StatusAccepted, view)
}
