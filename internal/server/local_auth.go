package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"cfui/internal/localauth"
)

const localAuthCookieName = "cfui_session"

const maxLoginLimiterEntries = 4096

const localAuthStreamRecheckInterval = 5 * time.Second

type loginAttempt struct {
	count   int
	resetAt time.Time
}

type loginLimiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	maxKeys  int
	lastGC   time.Time
	attempts map[string]loginAttempt
}

func newLoginLimiter(limit int, window time.Duration) *loginLimiter {
	return &loginLimiter{limit: limit, window: window, maxKeys: maxLoginLimiterEntries, attempts: make(map[string]loginAttempt)}
}

func (l *loginLimiter) allow(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lastGC.IsZero() || now.Sub(l.lastGC) >= l.window {
		for existingKey, attempt := range l.attempts {
			if !now.Before(attempt.resetAt) {
				delete(l.attempts, existingKey)
			}
		}
		l.lastGC = now
	}
	if _, exists := l.attempts[key]; !exists && l.maxKeys > 0 && len(l.attempts) >= l.maxKeys {
		return false, l.window
	}
	attempt := l.attempts[key]
	if attempt.resetAt.IsZero() || !now.Before(attempt.resetAt) {
		attempt = loginAttempt{resetAt: now.Add(l.window)}
	}
	if attempt.count >= l.limit {
		return false, attempt.resetAt.Sub(now)
	}
	attempt.count++
	l.attempts[key] = attempt
	return true, 0
}

func (l *loginLimiter) success(key string) {
	l.mu.Lock()
	delete(l.attempts, key)
	l.mu.Unlock()
}

func (s *Server) localAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || isPublicLocalAuthPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		enabled, err := s.localAuth.Enabled(r.Context())
		if err != nil {
			writeAPIError(w, http.StatusServiceUnavailable, errors.New("local authentication is unavailable"))
			return
		}
		if !enabled {
			next.ServeHTTP(w, r)
			return
		}
		token := localAuthToken(r)
		authenticated, err := s.localAuth.AuthenticateSession(r.Context(), token)
		if err != nil {
			writeAPIError(w, http.StatusServiceUnavailable, errors.New("local authentication is unavailable"))
			return
		}
		if !authenticated {
			w.Header().Set("Cache-Control", "no-store")
			writeAPIError(w, http.StatusUnauthorized, errors.New("authentication required"))
			return
		}
		if isUnsafeMethod(r.Method) && !sameRequestOrigin(r) {
			writeAPIError(w, http.StatusForbidden, errors.New("request origin is not allowed"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isPublicLocalAuthPath(path string) bool {
	if strings.HasPrefix(path, "/api/i18n/") || path == "/api/version" {
		return true
	}
	switch path {
	case "/api/auth/status", "/api/auth/setup", "/api/auth/login", "/api/auth/logout":
		return true
	default:
		return false
	}
}

func isUnsafeMethod(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func sameRequestOrigin(r *http.Request) bool {
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site") {
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Scheme, configBackupRequestScheme(r)) && strings.EqualFold(parsed.Host, requestExternalHost(r))
}

func (s *Server) handleLocalAuthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.writeLocalAuthStatus(w, r)
}

func (s *Server) handleLocalAuthSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameRequestOrigin(r) {
		writeAPIError(w, http.StatusForbidden, errors.New("request origin is not allowed"))
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeLocalAuthJSON(w, r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.localAuth.Setup(r.Context(), req.Username, req.Password); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, localauth.ErrAlreadyEnabled) {
			status = http.StatusConflict
		}
		writeAPIError(w, status, err)
		return
	}
	token, err := s.issueLocalAuthSession(w, r)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, errors.New("failed to create session"))
		return
	}
	s.writeLocalAuthStatusForToken(w, r, token)
}

func (s *Server) handleLocalAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameRequestOrigin(r) {
		writeAPIError(w, http.StatusForbidden, errors.New("request origin is not allowed"))
		return
	}
	key := "login:" + localAuthClientIP(r)
	now := time.Now()
	if allowed, retry := s.authLimit.allow(key, now); !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(retry.Seconds()))))
		writeAPIError(w, http.StatusTooManyRequests, errors.New("too many login attempts; try again later"))
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeLocalAuthJSON(w, r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	token, err := s.localAuth.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		if errors.Is(err, localauth.ErrDisabled) {
			writeAPIError(w, http.StatusConflict, err)
			return
		}
		writeAPIError(w, http.StatusUnauthorized, errors.New("invalid username or password"))
		return
	}
	s.authLimit.success(key)
	setLocalAuthCookie(w, r, token)
	s.writeLocalAuthStatusForToken(w, r, token)
}

func (s *Server) handleLocalAuthLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameRequestOrigin(r) {
		writeAPIError(w, http.StatusForbidden, errors.New("request origin is not allowed"))
		return
	}
	if err := s.localAuth.DeleteSession(r.Context(), localAuthToken(r)); err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, errors.New("failed to revoke local session"))
		return
	}
	clearLocalAuthCookie(w, r)
	writeJSON(w, map[string]bool{"authenticated": false})
}

func (s *Server) handleLocalAuthPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	key, ok := s.reserveLocalAuthConfirmation(w, r)
	if !ok {
		return
	}
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decodeLocalAuthJSON(w, r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	token, err := s.localAuth.ChangePasswordAndCreateSession(r.Context(), req.CurrentPassword, req.NewPassword)
	if err != nil {
		writeLocalAuthMutationError(w, err)
		return
	}
	s.authLimit.success(key)
	setLocalAuthCookie(w, r, token)
	s.writeLocalAuthStatusForToken(w, r, token)
}

func (s *Server) handleLocalAuthRevokeOthers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	key, ok := s.reserveLocalAuthConfirmation(w, r)
	if !ok {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := decodeLocalAuthJSON(w, r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.localAuth.RevokeOtherSessions(r.Context(), localAuthToken(r), req.Password); err != nil {
		writeLocalAuthMutationError(w, err)
		return
	}
	s.authLimit.success(key)
	writeJSON(w, map[string]bool{"success": true})
}

func (s *Server) handleLocalAuthDisable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	key, ok := s.reserveLocalAuthConfirmation(w, r)
	if !ok {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := decodeLocalAuthJSON(w, r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.localAuth.Disable(r.Context(), req.Password); err != nil {
		writeLocalAuthMutationError(w, err)
		return
	}
	s.authLimit.success(key)
	clearLocalAuthCookie(w, r)
	s.writeLocalAuthStatus(w, r)
}

func decodeLocalAuthJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := decodeStrictJSON(r.Body, target); err != nil {
		return fmt.Errorf("invalid request: %w", err)
	}
	return nil
}

func (s *Server) writeLocalAuthStatus(w http.ResponseWriter, r *http.Request) {
	s.writeLocalAuthStatusForToken(w, r, localAuthToken(r))
}

func (s *Server) writeLocalAuthStatusForToken(w http.ResponseWriter, r *http.Request, token string) {
	w.Header().Set("Cache-Control", "no-store")
	status, err := s.localAuth.Status(r.Context(), token)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, errors.New("failed to read local authentication status"))
		return
	}
	writeJSON(w, status)
}

func writeLocalAuthMutationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, localauth.ErrInvalidPassword), errors.Is(err, localauth.ErrInvalidCredentials):
		writeAPIError(w, http.StatusUnauthorized, errors.New("invalid password"))
	case errors.Is(err, localauth.ErrDisabled):
		writeAPIError(w, http.StatusConflict, err)
	default:
		writeAPIError(w, http.StatusBadRequest, err)
	}
}

func (s *Server) issueLocalAuthSession(w http.ResponseWriter, r *http.Request) (string, error) {
	token, err := s.localAuth.CreateSession(r.Context())
	if err != nil {
		return "", err
	}
	setLocalAuthCookie(w, r, token)
	return token, nil
}

func setLocalAuthCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     localAuthCookieName,
		Value:    token,
		Path:     "/api",
		MaxAge:   7 * 24 * 60 * 60,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func clearLocalAuthCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     localAuthCookieName,
		Value:    "",
		Path:     "/api",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func localAuthToken(r *http.Request) string {
	cookie, err := r.Cookie(localAuthCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (s *Server) reserveLocalAuthConfirmation(w http.ResponseWriter, r *http.Request) (string, bool) {
	tokenHash := sha256.Sum256([]byte(localAuthToken(r)))
	key := "confirm:" + hex.EncodeToString(tokenHash[:])
	if allowed, retry := s.authConfirmLimit.allow(key, time.Now()); !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(retry.Seconds()))))
		writeAPIError(w, http.StatusTooManyRequests, errors.New("too many password attempts; try again later"))
		return "", false
	}
	return key, true
}

func (s *Server) localAuthStreamAuthorized(r *http.Request) (bool, error) {
	if s.localAuth == nil {
		return true, nil
	}
	status, err := s.localAuth.Status(r.Context(), localAuthToken(r))
	if err != nil {
		return false, err
	}
	return !status.Enabled || status.Authenticated, nil
}

func requestIsHTTPS(r *http.Request) bool {
	return configBackupRequestScheme(r) == "https"
}

func remoteIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil && host != "" {
		return host
	}
	return remoteAddr
}

func localAuthClientIP(r *http.Request) string {
	direct := remoteIP(r.RemoteAddr)
	directIP := net.ParseIP(strings.TrimSpace(direct))
	if directIP == nil || !directIP.IsLoopback() {
		return direct
	}
	for _, header := range []string{"CF-Connecting-IP", "X-Forwarded-For"} {
		candidate := strings.TrimSpace(strings.Split(r.Header.Get(header), ",")[0])
		if parsed := net.ParseIP(candidate); parsed != nil {
			return parsed.String()
		}
	}
	return direct
}
