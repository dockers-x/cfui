package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cfui/internal/localauth"
)

func newLocalAuthServer(t *testing.T) *Server {
	t.Helper()
	return &Server{
		localAuth:        localauth.NewStore(t.TempDir()),
		authLimit:        newLoginLimiter(5, 5*time.Minute),
		authConfirmLimit: newLoginLimiter(5, 5*time.Minute),
	}
}

func TestLocalAuthMiddlewarePreservesHistoricalUnauthenticatedAccess(t *testing.T) {
	s := newLocalAuthServer(t)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	rec := httptest.NewRecorder()
	s.localAuthMiddleware(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("disabled protection changed legacy access: %d %s", rec.Code, rec.Body.String())
	}
}

func TestLocalAuthMiddlewareProtectsAPIButNotMCPOrWebDAV(t *testing.T) {
	s := newLocalAuthServer(t)
	if err := s.localAuth.Setup(t.Context(), "admin", "correct password"); err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := s.localAuthMiddleware(next)
	for _, path := range []string{"/api/config", "/api/tunnels"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s status = %d", path, rec.Code)
		}
	}
	for _, path := range []string{"/mcp", "/webdav/files", "/oauth/callback", "/api/auth/status", "/api/i18n/zh"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("public path %s status = %d", path, rec.Code)
		}
	}
}

func TestLocalAuthSetupImmediatelyLocksTheCurrentBrowser(t *testing.T) {
	s := newLocalAuthServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(`{"username":"admin","password":"correct password"}`))
	req.Host = "cfui.local"
	req.Header.Set("Origin", "http://cfui.local")
	rec := httptest.NewRecorder()
	s.handleLocalAuthSetup(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup status = %d: %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 0 {
		t.Fatalf("setup unexpectedly authenticated the current browser: %#v", cookies)
	}
	if strings.Contains(rec.Body.String(), "correct password") || strings.Contains(rec.Body.String(), "argon2") {
		t.Fatalf("authentication secret leaked in response: %s", rec.Body.String())
	}

	var status localauth.Status
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil || !status.Enabled || status.Authenticated {
		t.Fatalf("unexpected setup response: %#v, %v", status, err)
	}
	protected := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	protectedRec := httptest.NewRecorder()
	s.localAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(protectedRec, protected)
	if protectedRec.Code != http.StatusUnauthorized {
		t.Fatalf("current browser stayed unlocked after setup: %d", protectedRec.Code)
	}
}

func TestLocalAuthRejectsCrossOriginMutation(t *testing.T) {
	s := newLocalAuthServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(`{"username":"admin","password":"correct password"}`))
	req.Host = "cfui.local"
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	s.handleLocalAuthSetup(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin setup status = %d", rec.Code)
	}
}

func TestLocalAuthRejectsCrossSiteMetadataAndSchemeMismatch(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
	}{
		{name: "fetch metadata", headers: map[string]string{"Sec-Fetch-Site": "cross-site"}},
		{name: "scheme mismatch", headers: map[string]string{"Origin": "http://cfui.local", "X-Forwarded-Proto": "https"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := newLocalAuthServer(t)
			req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(`{"username":"admin","password":"correct password"}`))
			req.Host = "cfui.local"
			for name, value := range test.headers {
				req.Header.Set(name, value)
			}
			rec := httptest.NewRecorder()
			s.handleLocalAuthSetup(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("cross-site setup status = %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestLocalAuthAcceptsForwardedExternalOrigin(t *testing.T) {
	s := newLocalAuthServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(`{"username":"admin","password":"correct password"}`))
	req.Host = "127.0.0.1:14333"
	req.Header.Set("Origin", "https://cfui.example")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "cfui.example")
	rec := httptest.NewRecorder()
	s.handleLocalAuthSetup(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("forwarded same-origin setup status = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestLocalAuthMiddlewareRequiresSessionAndSameOriginForProtectedRoutes(t *testing.T) {
	s := newLocalAuthServer(t)
	if err := s.localAuth.Setup(t.Context(), "admin", "correct password"); err != nil {
		t.Fatal(err)
	}
	token, err := s.localAuth.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := s.localAuthMiddleware(next)

	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauthenticated.Code)
	}

	authenticatedReq := httptest.NewRequest(http.MethodPost, "/api/config", nil)
	authenticatedReq.Host = "cfui.local"
	authenticatedReq.Header.Set("Origin", "http://cfui.local")
	authenticatedReq.AddCookie(&http.Cookie{Name: localAuthCookieName, Value: token})
	authenticated := httptest.NewRecorder()
	handler.ServeHTTP(authenticated, authenticatedReq)
	if authenticated.Code != http.StatusNoContent {
		t.Fatalf("authenticated status = %d: %s", authenticated.Code, authenticated.Body.String())
	}

	crossSiteReq := httptest.NewRequest(http.MethodPost, "/api/config", nil)
	crossSiteReq.Host = "cfui.local"
	crossSiteReq.Header.Set("Sec-Fetch-Site", "cross-site")
	crossSiteReq.AddCookie(&http.Cookie{Name: localAuthCookieName, Value: token})
	crossSite := httptest.NewRecorder()
	handler.ServeHTTP(crossSite, crossSiteReq)
	if crossSite.Code != http.StatusForbidden {
		t.Fatalf("cross-site authenticated status = %d: %s", crossSite.Code, crossSite.Body.String())
	}
}

func TestLocalAuthLoginRateLimit(t *testing.T) {
	s := newLocalAuthServer(t)
	if err := s.localAuth.Setup(t.Context(), "admin", "correct password"); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 6; attempt++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"admin","password":"wrong password"}`))
		req.RemoteAddr = "192.0.2.10:1234"
		rec := httptest.NewRecorder()
		s.handleLocalAuthLogin(rec, req)
		want := http.StatusUnauthorized
		if attempt == 6 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("attempt %d status = %d, want %d", attempt, rec.Code, want)
		}
	}
}

func TestLocalAuthLoginRateLimitSeparatesForwardedClientsFromLoopbackProxy(t *testing.T) {
	s := newLocalAuthServer(t)
	if err := s.localAuth.Setup(t.Context(), "admin", "correct password"); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 5; attempt++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"admin","password":"wrong password"}`))
		req.RemoteAddr = "127.0.0.1:40000"
		req.Header.Set("CF-Connecting-IP", "192.0.2.10")
		rec := httptest.NewRecorder()
		s.handleLocalAuthLogin(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("failed attempt %d status = %d", attempt+1, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"admin","password":"correct password"}`))
	req.RemoteAddr = "127.0.0.1:40001"
	req.Header.Set("CF-Connecting-IP", "192.0.2.11")
	rec := httptest.NewRecorder()
	s.handleLocalAuthLogin(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("second forwarded client was rate limited: %d %s", rec.Code, rec.Body.String())
	}
}

func TestLocalAuthClientIPIgnoresForwardingHeadersFromNonLoopbackPeer(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	req.RemoteAddr = "198.51.100.8:443"
	req.Header.Set("CF-Connecting-IP", "192.0.2.99")
	if got := localAuthClientIP(req); got != "198.51.100.8" {
		t.Fatalf("client IP = %q, want direct peer", got)
	}
}

func TestLocalAuthLogoutKeepsCookieWhenSessionDeletionFails(t *testing.T) {
	s := newLocalAuthServer(t)
	if err := s.localAuth.Setup(t.Context(), "admin", "correct password"); err != nil {
		t.Fatal(err)
	}
	token, err := s.localAuth.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil).WithContext(ctx)
	req.AddCookie(&http.Cookie{Name: localAuthCookieName, Value: token})
	rec := httptest.NewRecorder()
	s.handleLocalAuthLogout(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("logout status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if got := rec.Header().Values("Set-Cookie"); len(got) != 0 {
		t.Fatalf("logout cleared cookie after database failure: %v", got)
	}
}

func TestLoginLimiterReservesConcurrentAttempts(t *testing.T) {
	limiter := newLoginLimiter(5, 5*time.Minute)
	now := time.Now()
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := limiter.allow("192.0.2.10", now); ok {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := allowed.Load(); got != 5 {
		t.Fatalf("concurrent allowed attempts = %d, want 5", got)
	}
}

func TestLoginLimiterBoundsAndExpiresKeys(t *testing.T) {
	limiter := newLoginLimiter(5, time.Minute)
	limiter.maxKeys = 2
	now := time.Now()
	if ok, _ := limiter.allow("one", now); !ok {
		t.Fatal("first key rejected")
	}
	if ok, _ := limiter.allow("two", now); !ok {
		t.Fatal("second key rejected")
	}
	if ok, _ := limiter.allow("three", now); ok {
		t.Fatal("limiter exceeded its key bound")
	}
	if ok, _ := limiter.allow("three", now.Add(2*time.Minute)); !ok {
		t.Fatal("expired keys were not reclaimed")
	}
}

func TestLocalAuthConfirmationIsRateLimited(t *testing.T) {
	s := newLocalAuthServer(t)
	if err := s.localAuth.Setup(t.Context(), "admin", "correct password"); err != nil {
		t.Fatal(err)
	}
	token, err := s.localAuth.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 6; attempt++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/sessions/revoke-others", strings.NewReader(`{"password":"wrong password"}`))
		req.AddCookie(&http.Cookie{Name: localAuthCookieName, Value: token})
		rec := httptest.NewRecorder()
		s.handleLocalAuthRevokeOthers(rec, req)
		want := http.StatusUnauthorized
		if attempt == 6 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("attempt %d status = %d, want %d", attempt, rec.Code, want)
		}
	}
}

func TestLocalAuthStreamAuthorizationTracksRevocation(t *testing.T) {
	s := newLocalAuthServer(t)
	if err := s.localAuth.Setup(t.Context(), "admin", "correct password"); err != nil {
		t.Fatal(err)
	}
	token, err := s.localAuth.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/logs/stream", nil)
	req.AddCookie(&http.Cookie{Name: localAuthCookieName, Value: token})
	if ok, err := s.localAuthStreamAuthorized(req); err != nil || !ok {
		t.Fatalf("live session authorization: ok=%v err=%v", ok, err)
	}
	if err := s.localAuth.DeleteSession(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.localAuthStreamAuthorized(req); err != nil || ok {
		t.Fatalf("revoked stream authorization: ok=%v err=%v", ok, err)
	}
}
