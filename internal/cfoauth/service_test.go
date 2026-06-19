package cfoauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cfui/internal/persist"
)

func TestStartURLWithScopesUsesRequestedScopes(t *testing.T) {
	ctx := context.Background()
	store := NewStore(t.TempDir())
	t.Cleanup(func() { closeStore(t, store) })

	svc := NewService(Config{
		ClientID:          "client-id",
		RelayCallbackURL:  "https://oauth.example.test/oauth/callback",
		LocalCallbackPath: "/oauth/callback",
		AuthorizationURL:  "https://dash.cloudflare.com/oauth2/auth",
		TokenURL:          "https://dash.cloudflare.com/oauth2/token",
		RevokeURL:         "https://dash.cloudflare.com/oauth2/revoke",
		UserInfoURL:       "https://dash.cloudflare.com/oauth2/userinfo",
		Scopes:            "zone.read",
		Configured:        true,
	}, store)

	startURL, err := svc.StartURLWithScopes(ctx, "dns.write dns.read dns.write")
	if err != nil {
		t.Fatalf("StartURLWithScopes: %v", err)
	}
	parsed, err := url.Parse(startURL)
	if err != nil {
		t.Fatalf("parse start url: %v", err)
	}
	if got, want := parsed.Query().Get("scope"), "dns.read dns.write"; got != want {
		t.Fatalf("scope query mismatch: got %q want %q", got, want)
	}
	if got, want := parsed.Query().Get("redirect_uri"), "https://oauth.example.test/oauth/callback"; got != want {
		t.Fatalf("redirect_uri mismatch: got %q want %q", got, want)
	}
	state := parsed.Query().Get("state")
	callbackURL, ok := RelayStateCallbackURL(state)
	if !ok {
		t.Fatalf("state missing encoded callback URL: %q", state)
	}
	if got, want := callbackURL, defaultLocalCallback; got != want {
		t.Fatalf("callback URL mismatch: got %q want %q", got, want)
	}
	pending, err := store.ConsumeState(ctx, state)
	if err != nil {
		t.Fatalf("ConsumeState: %v", err)
	}
	if pending.Scope != "dns.read dns.write" {
		t.Fatalf("pending state scope mismatch: %#v", pending)
	}
}

func TestStartURLWithScopesFallsBackToConfiguredScopes(t *testing.T) {
	ctx := context.Background()
	store := NewStore(t.TempDir())
	t.Cleanup(func() { closeStore(t, store) })

	svc := NewService(Config{
		ClientID:         "client-id",
		RelayCallbackURL: "https://oauth.example.test/oauth/callback",
		AuthorizationURL: "https://dash.cloudflare.com/oauth2/auth",
		Scopes:           "zone.read dns.read",
		Configured:       true,
	}, store)

	startURL, err := svc.StartURLWithScopes(ctx, "")
	if err != nil {
		t.Fatalf("StartURLWithScopes: %v", err)
	}
	parsed, err := url.Parse(startURL)
	if err != nil {
		t.Fatalf("parse start url: %v", err)
	}
	if got, want := parsed.Query().Get("scope"), "zone.read dns.read"; got != want {
		t.Fatalf("scope query mismatch: got %q want %q", got, want)
	}
}

func TestStartURLWithOptionsEncodesRequestCallbackInState(t *testing.T) {
	ctx := context.Background()
	store := NewStore(t.TempDir())
	t.Cleanup(func() { closeStore(t, store) })

	svc := NewService(Config{
		ClientID:          "client-id",
		RelayCallbackURL:  "https://oauth.example.test/oauth/callback",
		LocalCallbackPath: "/oauth/callback",
		AuthorizationURL:  "https://dash.cloudflare.com/oauth2/auth",
		Scopes:            "zone.read",
		Configured:        true,
	}, store)

	startURL, err := svc.StartURLWithOptions(ctx, StartURLOptions{
		Scopes:      "dns.read",
		CallbackURL: "https://cfui.example.internal/oauth/callback?ignored=1#section",
	})
	if err != nil {
		t.Fatalf("StartURLWithOptions: %v", err)
	}
	parsed, err := url.Parse(startURL)
	if err != nil {
		t.Fatalf("parse start url: %v", err)
	}
	if got, want := parsed.Query().Get("redirect_uri"), "https://oauth.example.test/oauth/callback"; got != want {
		t.Fatalf("redirect_uri mismatch: got %q want %q", got, want)
	}
	callbackURL, ok := RelayStateCallbackURL(parsed.Query().Get("state"))
	if !ok {
		t.Fatalf("state missing encoded callback URL: %s", startURL)
	}
	if got, want := callbackURL, "https://cfui.example.internal/oauth/callback"; got != want {
		t.Fatalf("callback URL mismatch: got %q want %q", got, want)
	}
}

func TestStartURLWithOptionsRejectsInvalidCallbackURL(t *testing.T) {
	ctx := context.Background()
	store := NewStore(t.TempDir())
	t.Cleanup(func() { closeStore(t, store) })

	svc := NewService(Config{
		ClientID:          "client-id",
		RelayCallbackURL:  "https://oauth.example.test/oauth/callback",
		LocalCallbackPath: "/oauth/callback",
		AuthorizationURL:  "https://dash.cloudflare.com/oauth2/auth",
		Scopes:            "zone.read",
		Configured:        true,
	}, store)

	if _, err := svc.StartURLWithOptions(ctx, StartURLOptions{
		Scopes:      "dns.read",
		CallbackURL: "https://cfui.example.internal/not-oauth",
	}); err == nil || !strings.Contains(err.Error(), "path must be /oauth/callback") {
		t.Fatalf("expected callback path error, got %v", err)
	}
}

func TestStartURLWithOptionsFreshLoginWrapsAuthorizeURL(t *testing.T) {
	ctx := context.Background()
	store := NewStore(t.TempDir())
	t.Cleanup(func() { closeStore(t, store) })

	svc := NewService(Config{
		ClientID:         "client-id",
		RelayCallbackURL: "https://oauth.example.test/oauth/callback",
		AuthorizationURL: "https://dash.cloudflare.com/oauth2/auth",
		LogoutURL:        "https://dash.cloudflare.com/logout",
		Scopes:           "zone.read",
		Configured:       true,
	}, store)

	startURL, err := svc.StartURLWithOptions(ctx, StartURLOptions{
		Scopes:     "dns.write dns.read",
		FreshLogin: true,
	})
	if err != nil {
		t.Fatalf("StartURLWithOptions: %v", err)
	}
	logoutURL, err := url.Parse(startURL)
	if err != nil {
		t.Fatalf("parse logout url: %v", err)
	}
	if got, want := logoutURL.Host, "dash.cloudflare.com"; got != want {
		t.Fatalf("logout host = %q, want %q", got, want)
	}
	if got, want := logoutURL.Path, "/logout"; got != want {
		t.Fatalf("logout path = %q, want %q", got, want)
	}
	authorizeRaw := logoutURL.Query().Get("to")
	if authorizeRaw == "" {
		t.Fatalf("fresh login URL missing to parameter: %s", startURL)
	}
	authorizeURL, err := url.Parse(authorizeRaw)
	if err != nil {
		t.Fatalf("parse authorize url: %v", err)
	}
	if got, want := authorizeURL.Path, "/oauth2/auth"; got != want {
		t.Fatalf("authorize path = %q, want %q", got, want)
	}
	if got, want := authorizeURL.Query().Get("scope"), "dns.read dns.write"; got != want {
		t.Fatalf("scope query mismatch: got %q want %q", got, want)
	}
	state := authorizeURL.Query().Get("state")
	if state == "" || authorizeURL.Query().Get("code_challenge") == "" {
		t.Fatalf("authorize URL missing state or PKCE challenge: %s", authorizeRaw)
	}
	pending, err := store.ConsumeState(ctx, state)
	if err != nil {
		t.Fatalf("ConsumeState: %v", err)
	}
	if pending.Scope != "dns.read dns.write" {
		t.Fatalf("pending state scope mismatch: %#v", pending)
	}
}

func TestCompleteCallbackPersistsSessionInSQLiteOnly(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := NewStore(dir)
	t.Cleanup(func() { closeStore(t, store) })

	var tokenRequested bool
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			tokenRequested = true
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			if got, want := r.Form.Get("grant_type"), "authorization_code"; got != want {
				t.Fatalf("grant_type mismatch: got %q want %q", got, want)
			}
			if got, want := r.Form.Get("client_id"), "client-id"; got != want {
				t.Fatalf("client_id mismatch: got %q want %q", got, want)
			}
			if got, want := r.Form.Get("code"), "auth-code"; got != want {
				t.Fatalf("code mismatch: got %q want %q", got, want)
			}
			if got, want := r.Form.Get("redirect_uri"), "https://oauth.example.test/oauth/callback"; got != want {
				t.Fatalf("redirect_uri mismatch: got %q want %q", got, want)
			}
			if strings.TrimSpace(r.Form.Get("code_verifier")) == "" {
				t.Fatal("expected code_verifier to be sent to token endpoint")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"access-token","refresh_token":"refresh-token","expires_in":3600,"scope":"dns.read dns.write"}`))
		case "/userinfo":
			if got, want := r.Header.Get("Authorization"), "Bearer access-token"; got != want {
				t.Fatalf("authorization header mismatch: got %q want %q", got, want)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"email":"me@example.com"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(tokenServer.Close)

	svc := NewService(Config{
		ClientID:         "client-id",
		RelayCallbackURL: "https://oauth.example.test/oauth/callback",
		AuthorizationURL: tokenServer.URL + "/auth",
		TokenURL:         tokenServer.URL + "/token",
		RevokeURL:        tokenServer.URL + "/revoke",
		UserInfoURL:      tokenServer.URL + "/userinfo",
		Scopes:           "zone.read",
		Configured:       true,
	}, store)

	startURL, err := svc.StartURLWithScopes(ctx, "dns.write dns.read")
	if err != nil {
		t.Fatalf("StartURLWithScopes: %v", err)
	}
	parsed, err := url.Parse(startURL)
	if err != nil {
		t.Fatalf("parse start url: %v", err)
	}

	summary, err := svc.CompleteCallback(ctx, "auth-code", parsed.Query().Get("state"))
	if err != nil {
		t.Fatalf("CompleteCallback: %v", err)
	}
	if !tokenRequested {
		t.Fatal("expected token endpoint to be requested")
	}
	if summary.ID == "" || summary.Label != "me@example.com" || !summary.Current {
		t.Fatalf("unexpected session summary: %#v", summary)
	}

	db, err := persist.OpenRawDB(dir)
	if err != nil {
		t.Fatalf("OpenRawDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var sessionRows int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM oauth_sessions WHERE session_id = ? AND access_token = ? AND refresh_token = ? AND current = 1`,
		summary.ID,
		"access-token",
		"refresh-token",
	).Scan(&sessionRows); err != nil {
		t.Fatalf("query oauth_sessions: %v", err)
	}
	if sessionRows != 1 {
		t.Fatalf("expected OAuth session to be stored in SQLite, got %d rows", sessionRows)
	}

	var stateRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM oauth_states`).Scan(&stateRows); err != nil {
		t.Fatalf("query oauth_states: %v", err)
	}
	if stateRows != 0 {
		t.Fatalf("expected callback state to be consumed from SQLite, got %d rows", stateRows)
	}

	status, err := svc.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	publicStatus, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("Marshal status: %v", err)
	}
	if strings.Contains(string(publicStatus), "access-token") || strings.Contains(string(publicStatus), "refresh-token") {
		t.Fatalf("status response leaked OAuth token: %s", publicStatus)
	}
	assertNoJSONFiles(t, dir)
	if matches, err := filepath.Glob(filepath.Join(dir, "*.json")); err != nil || len(matches) != 0 {
		t.Fatalf("OAuth flow must not write JSON files, matches=%v err=%v", matches, err)
	}
}

func TestUpdateSessionLabelValidatesAndReturnsPublicStatus(t *testing.T) {
	ctx := context.Background()
	store := NewStore(t.TempDir())
	t.Cleanup(func() { closeStore(t, store) })
	if err := store.SaveSession(ctx, Session{
		ID:           "session-1",
		Label:        "Original",
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    time.Now().UTC().Add(time.Hour),
		Scope:        "zone.read dns.read",
	}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	svc := NewService(Config{Configured: true}, store)

	status, err := svc.UpdateSessionLabel(ctx, "session-1", "  Production account  ")
	if err != nil {
		t.Fatalf("UpdateSessionLabel: %v", err)
	}
	if status.Current == nil || status.Current.Label != "Production account" {
		t.Fatalf("unexpected status: %#v", status)
	}
	body, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("Marshal status: %v", err)
	}
	if strings.Contains(string(body), "access-token") || strings.Contains(string(body), "refresh-token") {
		t.Fatalf("status response leaked OAuth token: %s", body)
	}

	if _, err := svc.UpdateSessionLabel(ctx, "session-1", " "); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("expected required label error, got %v", err)
	}
	if _, err := svc.UpdateSessionLabel(ctx, "session-1", strings.Repeat("x", maxSessionLabelRunes+1)); err == nil || !strings.Contains(err.Error(), "characters or fewer") {
		t.Fatalf("expected max length label error, got %v", err)
	}
}

func TestCheckRelayUsesHealthEndpoint(t *testing.T) {
	ctx := context.Background()
	store := NewStore(t.TempDir())
	t.Cleanup(func() { closeStore(t, store) })

	var sawHealth bool
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Fatalf("relay path = %q, want /health", r.URL.Path)
		}
		if r.URL.RawQuery != "" {
			t.Fatalf("relay health should strip query, got %q", r.URL.RawQuery)
		}
		sawHealth = true
		w.Header().Set("X-CFUI-OAuth-Relay", "state-v1")
		_, _ = w.Write([]byte("ok state-v1"))
	}))
	t.Cleanup(relay.Close)

	svc := NewService(Config{
		ClientID:         "client-id",
		RelayCallbackURL: relay.URL + "/oauth/callback?ignored=1",
		Scopes:           "zone.read",
		Configured:       true,
	}, store)

	check, err := svc.CheckRelay(ctx)
	if err != nil {
		t.Fatalf("CheckRelay: %v", err)
	}
	if !sawHealth {
		t.Fatal("expected relay health endpoint to be requested")
	}
	if !check.Reachable || check.StatusCode != http.StatusOK || check.Message != "ok state-v1" || !check.SupportsStateCallback {
		t.Fatalf("unexpected relay check: %#v", check)
	}
	if check.HealthURL != relay.URL+"/health" {
		t.Fatalf("health URL = %q", check.HealthURL)
	}
}

func TestCheckRelayDetectsLegacyWorkerWithoutStateCallback(t *testing.T) {
	ctx := context.Background()
	store := NewStore(t.TempDir())
	t.Cleanup(func() { closeStore(t, store) })

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(relay.Close)

	svc := NewService(Config{
		ClientID:         "client-id",
		RelayCallbackURL: relay.URL + "/oauth/callback",
		Scopes:           "zone.read",
		Configured:       true,
	}, store)

	check, err := svc.CheckRelay(ctx)
	if err != nil {
		t.Fatalf("CheckRelay: %v", err)
	}
	if !check.Reachable || check.SupportsStateCallback {
		t.Fatalf("unexpected legacy relay check: %#v", check)
	}
}

func TestCheckRelayReportsInvalidURL(t *testing.T) {
	ctx := context.Background()
	store := NewStore(t.TempDir())
	t.Cleanup(func() { closeStore(t, store) })

	svc := NewService(Config{
		ClientID:         "client-id",
		RelayCallbackURL: "ftp://oauth.example.test/oauth/callback",
		Scopes:           "zone.read",
		Configured:       true,
	}, store)

	check, err := svc.CheckRelay(ctx)
	if err != nil {
		t.Fatalf("CheckRelay: %v", err)
	}
	if check.Reachable || check.HealthURL != "" || !strings.Contains(check.Message, "http or https") {
		t.Fatalf("unexpected invalid relay check: %#v", check)
	}
}

func TestNormalizeRequestedScopesRejectsInvalidScope(t *testing.T) {
	for _, scope := range []string{
		"",
		"dns.read <script>",
		"dns.read/something",
		strings.Repeat("a", maxOAuthScopeBytes+1),
	} {
		if normalized, err := NormalizeRequestedScopes(scope); err == nil {
			t.Fatalf("NormalizeRequestedScopes(%q) = %q, expected error", scope, normalized)
		}
	}
}
