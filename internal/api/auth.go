package api

import (
	"context"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/ericflo/finalechat/internal/auth"
	"github.com/ericflo/finalechat/internal/bus"
	"github.com/ericflo/finalechat/internal/store"
)

const sessionCookie = "fc_session"

// principal is the authenticated caller.
type principal struct {
	user *store.User
	// token is set when the caller authenticated with an API token.
	token *store.APIToken
	// sessionHash is set when the caller authenticated with a browser session.
	sessionHash []byte
}

func (p *principal) viaToken() bool { return p != nil && p.token != nil }

type ctxKey int

const principalKey ctxKey = 1

func principalFrom(ctx context.Context) *principal {
	p, _ := ctx.Value(principalKey).(*principal)
	return p
}

// authenticate resolves the caller from a bearer token or session cookie
// without requiring either.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := s.resolvePrincipal(r)
		if err != nil {
			writeError(w, err)
			return
		}
		if p != nil {
			r = r.WithContext(context.WithValue(r.Context(), principalKey, p))
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) resolvePrincipal(r *http.Request) (*principal, error) {
	if h := r.Header.Get("Authorization"); h != "" {
		parts := strings.SplitN(h, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			return nil, &apiError{Status: http.StatusUnauthorized, Code: "unauthorized", Message: "Use `Authorization: Bearer <token>`."}
		}
		secret := strings.TrimSpace(parts[1])
		if !auth.LooksLikeAPIToken(secret) {
			return nil, &apiError{Status: http.StatusUnauthorized, Code: "invalid_token", Message: "The API token is malformed. Tokens start with fc_ and are created in Settings → Agents."}
		}
		user, token, err := s.store.ResolveAPIToken(r.Context(), auth.HashToken(secret))
		if err == store.ErrNotFound {
			return nil, &apiError{Status: http.StatusUnauthorized, Code: "invalid_token", Message: "The API token is unknown or has been revoked."}
		}
		if err != nil {
			return nil, err
		}
		return &principal{user: user, token: token}, nil
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil, nil
	}
	hash := auth.HashToken(c.Value)
	user, err := s.store.ResolveSession(r.Context(), hash, s.cfg.SessionTTL)
	if err == store.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &principal{user: user, sessionHash: hash}, nil
}

// requireAuth rejects unauthenticated requests and enforces same-origin
// checks for cookie-authenticated mutations.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r.Context())
		if p == nil {
			writeError(w, errUnauthorized)
			return
		}
		if p.sessionHash != nil && !isSafeMethod(r.Method) && !s.sameOrigin(r) {
			writeError(w, errCSRF)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// sameOrigin decides whether a cookie-bearing mutation came from our own
// pages. Browsers always send Origin on cross-site POSTs and Sec-Fetch-Site on
// every fetch, so either header is enough to reject a forged request.
func (s *Server) sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "none"
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func (s *Server) setSessionCookie(w http.ResponseWriter, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl.Seconds()),
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// signupMode reports how registration is gated right now.
func (s *Server) signupMode(ctx context.Context) (string, error) {
	// An invite code, when configured, gates every registration including the
	// first one, so a freshly deployed instance cannot be claimed by a
	// stranger before its owner signs up.
	if s.cfg.InviteCode != "" {
		return "invite", nil
	}
	n, err := s.store.CountUsers(ctx)
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "open", nil
	}
	return "closed", nil
}

// GET /api/v1/auth/status
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	mode, err := s.signupMode(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	resp := map[string]any{"signup": mode, "authenticated": false, "push_enabled": s.push.Enabled(), "attachments_enabled": s.blobs != nil, "version": s.cfg.Version}
	if p := principalFrom(r.Context()); p != nil {
		resp["authenticated"] = true
		resp["user"] = p.user
	}
	writeJSON(w, http.StatusOK, resp)
}

type registerRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	InviteCode  string `json:"invite_code"`
}

// POST /api/v1/auth/register
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !s.authLimiter.allow(s.clientIP(r)) {
		writeError(w, errRateLimited)
		return
	}
	if !s.sameOrigin(r) {
		writeError(w, errCSRF)
		return
	}
	var req registerRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if _, err := mail.ParseAddress(req.Email); err != nil || strings.ContainsAny(req.Email, " <>") {
		writeError(w, errValidation("Enter a valid email address."))
		return
	}
	if len(req.Password) < 10 {
		writeError(w, errValidation("Use a password of at least 10 characters."))
		return
	}
	if len(req.Password) > 1024 {
		writeError(w, errValidation("That password is too long."))
		return
	}
	mode, err := s.signupMode(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	switch mode {
	case "closed":
		writeError(w, &apiError{Status: http.StatusForbidden, Code: "signup_closed", Message: "This Finalechat is private. Sign in instead."})
		return
	case "invite":
		if strings.TrimSpace(req.InviteCode) != s.cfg.InviteCode {
			writeError(w, &apiError{Status: http.StatusForbidden, Code: "invalid_invite", Message: "That invite code is not valid."})
			return
		}
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, err)
		return
	}
	user, err := s.store.CreateUser(r.Context(), req.Email, hash, req.DisplayName)
	if err == store.ErrConflict {
		writeError(w, &apiError{Status: http.StatusConflict, Code: "email_taken", Message: "An account with that email already exists."})
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	if err := s.startSession(w, r, user); err != nil {
		writeError(w, err)
		return
	}
	s.log.Info("account created", "user_id", user.ID, "mode", mode)
	writeJSON(w, http.StatusCreated, map[string]any{"user": user})
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// POST /api/v1/auth/login
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.authLimiter.allow(s.clientIP(r)) {
		writeError(w, errRateLimited)
		return
	}
	if !s.sameOrigin(r) {
		writeError(w, errCSRF)
		return
	}
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	invalid := &apiError{Status: http.StatusUnauthorized, Code: "invalid_credentials", Message: "Email or password is incorrect."}
	user, hash, err := s.store.GetUserByEmail(r.Context(), strings.ToLower(req.Email))
	if err == store.ErrNotFound {
		// Burn comparable time so account existence is not observable.
		_, _ = auth.VerifyPassword("$argon2id$v=19$m=65536,t=2,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", req.Password)
		writeError(w, invalid)
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	ok, err := auth.VerifyPassword(hash, req.Password)
	if err != nil || !ok {
		writeError(w, invalid)
		return
	}
	if err := s.startSession(w, r, user); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, user *store.User) error {
	token, err := auth.NewSessionToken()
	if err != nil {
		return err
	}
	if _, err := s.store.CreateSession(r.Context(), auth.HashToken(token), user.ID, r.UserAgent(), s.cfg.SessionTTL); err != nil {
		return err
	}
	s.setSessionCookie(w, token, s.cfg.SessionTTL)
	return nil
}

// POST /api/v1/auth/logout
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if p := principalFrom(r.Context()); p != nil && p.sessionHash != nil {
		_ = s.store.DeleteSession(r.Context(), p.sessionHash)
	}
	s.clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// GET /api/v1/me
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	counts, err := s.store.GetCounts(r.Context(), p.user.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	resp := map[string]any{"user": p.user, "counts": counts, "push_enabled": s.push.Enabled(), "attachments_enabled": s.blobs != nil, "base_url": s.cfg.BaseURL, "version": s.cfg.Version}
	if p.token != nil {
		resp["token"] = p.token
		resp["auth"] = "token"
	} else {
		resp["auth"] = "session"
	}
	writeJSON(w, http.StatusOK, resp)
}

type profileRequest struct {
	DisplayName     optionalString `json:"display_name"`
	CurrentPassword string         `json:"current_password"`
	NewPassword     string         `json:"new_password"`
}

// PATCH /api/v1/me
func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.viaToken() {
		writeError(w, &apiError{Status: http.StatusForbidden, Code: "forbidden", Message: "Account changes require signing in to the app."})
		return
	}
	var req profileRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	user := p.user
	if req.DisplayName.Set {
		if len(req.DisplayName.Value) > 80 {
			writeError(w, errValidation("Display name must be at most 80 characters."))
			return
		}
		var err error
		user, err = s.store.UpdateUserProfile(r.Context(), user.ID, req.DisplayName.Value)
		if err != nil {
			writeError(w, err)
			return
		}
	}
	if req.NewPassword != "" {
		if len(req.NewPassword) < 10 || len(req.NewPassword) > 1024 {
			writeError(w, errValidation("Use a new password of at least 10 characters."))
			return
		}
		_, hash, err := s.store.GetUserByEmail(r.Context(), user.Email)
		if err != nil {
			writeError(w, err)
			return
		}
		ok, err := auth.VerifyPassword(hash, req.CurrentPassword)
		if err != nil || !ok {
			writeError(w, &apiError{Status: http.StatusForbidden, Code: "invalid_credentials", Message: "Current password is incorrect."})
			return
		}
		newHash, err := auth.HashPassword(req.NewPassword)
		if err != nil {
			writeError(w, err)
			return
		}
		if err := s.store.UpdatePassword(r.Context(), user.ID, newHash, p.sessionHash); err != nil {
			writeError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

// GET /api/v1/settings
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"settings": p.user.Settings})
}

// PATCH /api/v1/settings
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var req struct {
		NotifyAllMessages optionalBool `json:"notify_all_messages"`
		RemoteMode        optionalBool `json:"remote_mode"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	settings := p.user.Settings
	if req.NotifyAllMessages.Set {
		settings.NotifyAllMessages = req.NotifyAllMessages.Value
	}
	if req.RemoteMode.Set {
		settings.RemoteMode = req.RemoteMode.Value
	}
	user, err := s.store.UpdateUserSettings(r.Context(), p.user.ID, settings)
	if err != nil {
		writeError(w, err)
		return
	}
	s.bus.Publish(r.Context(), bus.Event{Type: bus.SettingsUpdated, UserID: p.user.ID.String()})
	writeJSON(w, http.StatusOK, map[string]any{"settings": user.Settings})
}

// GET /api/v1/tokens
func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	tokens, err := s.store.ListAPITokens(r.Context(), p.user.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": tokens})
}

// POST /api/v1/tokens
func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.viaToken() {
		writeError(w, &apiError{Status: http.StatusForbidden, Code: "forbidden", Message: "API tokens can only be created from the app."})
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		req.Name = "Agent token"
	}
	if len(req.Name) > 120 {
		writeError(w, errValidation("Token name must be at most 120 characters."))
		return
	}
	secret, prefix, err := auth.NewAPIToken()
	if err != nil {
		writeError(w, err)
		return
	}
	token, err := s.store.CreateAPIToken(r.Context(), p.user.ID, req.Name, auth.HashToken(secret), prefix)
	if err != nil {
		writeError(w, err)
		return
	}
	s.log.Info("api token created", "user_id", p.user.ID, "token_id", token.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "secret": secret})
}

// DELETE /api/v1/tokens/{id}
func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.viaToken() {
		writeError(w, &apiError{Status: http.StatusForbidden, Code: "forbidden", Message: "API tokens can only be revoked from the app."})
		return
	}
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if err := s.store.RevokeAPIToken(r.Context(), p.user.ID, id); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
