package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cfui/internal/cfaccount"
	"cfui/internal/cfoauth"
	"cfui/internal/persist"
)

func TestOAuthCloudflareResourceHandlersRequireLogin(t *testing.T) {
	s := newServerTestServer(t)
	tests := []struct {
		name    string
		method  string
		target  string
		body    io.Reader
		handler http.HandlerFunc
	}{
		{name: "overview", method: http.MethodGet, target: "/api/cf/overview", handler: s.handleCFOverview},
		{name: "accounts", method: http.MethodGet, target: "/api/cf/accounts", handler: s.handleCFAccounts},
		{name: "account usage", method: http.MethodGet, target: "/api/cf/usage/account?account_id=account-1", handler: s.handleCFAccountUsage},
		{name: "zones", method: http.MethodGet, target: "/api/cf/zones?account_id=account-1", handler: s.handleCFZones},
		{name: "zone detail", method: http.MethodGet, target: "/api/cf/zones/zone-1", handler: s.handleCFZone},
		{name: "dns", method: http.MethodGet, target: "/api/cf/dns?zone_id=zone-1", handler: s.handleCFDNSRecords},
		{name: "dns count", method: http.MethodGet, target: "/api/cf/dns/count?zone_id=zone-1", handler: s.handleCFDNSCount},
		{name: "dns create", method: http.MethodPost, target: "/api/cf/dns?zone_id=zone-1", body: strings.NewReader(`{"type":"A","name":"a.example.com","content":"192.0.2.1"}`), handler: s.handleCFDNSRecords},
		{name: "dns update", method: http.MethodPut, target: "/api/cf/dns/record-1?zone_id=zone-1", body: strings.NewReader(`{"type":"A","name":"a.example.com","content":"192.0.2.2"}`), handler: s.handleCFDNSRecord},
		{name: "dns delete", method: http.MethodDelete, target: "/api/cf/dns/record-1?zone_id=zone-1", handler: s.handleCFDNSRecord},
		{name: "tunnels", method: http.MethodGet, target: "/api/cf/tunnels?account_id=account-1", handler: s.handleCFTunnels},
		{name: "tunnel create", method: http.MethodPost, target: "/api/cf/tunnels?account_id=account-1", body: strings.NewReader(`{"name":"edge","save_local_profile":true}`), handler: s.handleCFTunnels},
		{name: "workers", method: http.MethodGet, target: "/api/cf/workers?account_id=account-1", handler: s.handleCFWorkers},
		{name: "worker detail", method: http.MethodGet, target: "/api/cf/workers/script-one?account_id=account-1", handler: s.handleCFWorker},
		{name: "worker metrics", method: http.MethodGet, target: "/api/cf/workers/script-one/metrics?account_id=account-1&range=24h", handler: s.handleCFWorker},
		{name: "worker tail", method: http.MethodGet, target: "/api/cf/workers/script-one/tail?account_id=account-1", handler: s.handleCFWorker},
		{name: "r2 metrics", method: http.MethodGet, target: "/api/cf/r2/metrics?account_id=account-1", handler: s.handleCFR2Metrics},
		{name: "r2 buckets", method: http.MethodGet, target: "/api/cf/r2/buckets?account_id=account-1", handler: s.handleCFR2Buckets},
		{name: "r2 bucket create", method: http.MethodPost, target: "/api/cf/r2/buckets?account_id=account-1", body: strings.NewReader(`{"name":"bucket-one"}`), handler: s.handleCFR2Buckets},
		{name: "r2 bucket delete", method: http.MethodDelete, target: "/api/cf/r2/buckets/bucket-one?account_id=account-1", handler: s.handleCFR2Bucket},
		{name: "r2 objects", method: http.MethodGet, target: "/api/cf/r2/objects?account_id=account-1&bucket=bucket-one", handler: s.handleCFR2Objects},
		{name: "r2 object get", method: http.MethodGet, target: "/api/cf/r2/object?account_id=account-1&bucket=bucket-one&key=a%2Fb.txt", handler: s.handleCFR2Object},
		{name: "r2 object put", method: http.MethodPut, target: "/api/cf/r2/object?account_id=account-1&bucket=bucket-one&key=a%2Fb.txt", body: strings.NewReader(`{"value":"hello"}`), handler: s.handleCFR2Object},
		{name: "r2 object upload", method: http.MethodPost, target: "/api/cf/r2/object/upload?account_id=account-1&bucket=bucket-one&key=a%2Fb.bin", body: strings.NewReader("binary"), handler: s.handleCFR2ObjectUpload},
		{name: "r2 object copy", method: http.MethodPost, target: "/api/cf/r2/object/copy?account_id=account-1&bucket=bucket-one", body: strings.NewReader(`{"source_key":"a/b.txt","destination_key":"a/c.txt"}`), handler: s.handleCFR2ObjectCopy},
		{name: "r2 object download", method: http.MethodGet, target: "/api/cf/r2/object/download?account_id=account-1&bucket=bucket-one&key=a%2Fb.bin", handler: s.handleCFR2ObjectDownload},
		{name: "r2 object delete", method: http.MethodDelete, target: "/api/cf/r2/object?account_id=account-1&bucket=bucket-one&key=a%2Fb.txt", handler: s.handleCFR2Object},
		{name: "d1 databases", method: http.MethodGet, target: "/api/cf/d1/databases?account_id=account-1", handler: s.handleCFD1Databases},
		{name: "d1 database detail", method: http.MethodGet, target: "/api/cf/d1/databases/database-1?account_id=account-1", handler: s.handleCFD1Database},
		{name: "d1 query", method: http.MethodPost, target: "/api/cf/d1/query?account_id=account-1&database_id=database-1", body: strings.NewReader(`{"sql":"SELECT 1"}`), handler: s.handleCFD1Query},
		{name: "d1 tables", method: http.MethodGet, target: "/api/cf/d1/tables?account_id=account-1&database_id=database-1", handler: s.handleCFD1Tables},
		{name: "d1 table rows", method: http.MethodGet, target: "/api/cf/d1/table?account_id=account-1&database_id=database-1&table=users", handler: s.handleCFD1Table},
		{name: "d1 row update", method: http.MethodPatch, target: "/api/cf/d1/table?account_id=account-1&database_id=database-1&table=users", body: strings.NewReader(`{"rowid":"1","changes":{"name":"Ada"}}`), handler: s.handleCFD1Table},
		{name: "d1 row delete", method: http.MethodDelete, target: "/api/cf/d1/table?account_id=account-1&database_id=database-1&table=users", body: strings.NewReader(`{"rowid":"1"}`), handler: s.handleCFD1Table},
		{name: "kv namespaces", method: http.MethodGet, target: "/api/cf/kv/namespaces?account_id=account-1", handler: s.handleCFKVNamespaces},
		{name: "kv keys", method: http.MethodGet, target: "/api/cf/kv/keys?account_id=account-1&namespace_id=namespace-1", handler: s.handleCFKVKeys},
		{name: "kv value get", method: http.MethodGet, target: "/api/cf/kv/value?account_id=account-1&namespace_id=namespace-1&key=a%2Fb", handler: s.handleCFKVValue},
		{name: "kv value put", method: http.MethodPut, target: "/api/cf/kv/value?account_id=account-1&namespace_id=namespace-1&key=a%2Fb", body: strings.NewReader(`{"value":"hello"}`), handler: s.handleCFKVValue},
		{name: "kv value delete", method: http.MethodDelete, target: "/api/cf/kv/value?account_id=account-1&namespace_id=namespace-1&key=a%2Fb", handler: s.handleCFKVValue},
		{name: "snippets", method: http.MethodGet, target: "/api/cf/snippets?zone_id=zone-1", handler: s.handleCFSnippets},
		{name: "snippet create", method: http.MethodPost, target: "/api/cf/snippets?zone_id=zone-1", body: strings.NewReader(`{"name":"snippet_1","code":"export default {}"}`), handler: s.handleCFSnippets},
		{name: "snippet content get", method: http.MethodGet, target: "/api/cf/snippets/snippet_1/content?zone_id=zone-1", handler: s.handleCFSnippet},
		{name: "snippet content put", method: http.MethodPut, target: "/api/cf/snippets/snippet_1/content?zone_id=zone-1", body: strings.NewReader(`{"main_file":"snippet.js","code":"export default {}"}`), handler: s.handleCFSnippet},
		{name: "snippet delete", method: http.MethodDelete, target: "/api/cf/snippets/snippet_1?zone_id=zone-1", handler: s.handleCFSnippet},
		{name: "snippet rules", method: http.MethodGet, target: "/api/cf/snippets/rules?zone_id=zone-1", handler: s.handleCFSnippetRules},
		{name: "snippet rule create", method: http.MethodPost, target: "/api/cf/snippets/rules?zone_id=zone-1", body: strings.NewReader(`{"snippet_name":"snippet_1","expression":"http.request.uri.path contains \"/\"","enabled":true}`), handler: s.handleCFSnippetRules},
		{name: "snippet rule update", method: http.MethodPatch, target: "/api/cf/snippets/rules/rule-1?zone_id=zone-1", body: strings.NewReader(`{"enabled":false}`), handler: s.handleCFSnippetRule},
		{name: "snippet rule delete", method: http.MethodDelete, target: "/api/cf/snippets/rules/rule-1?zone_id=zone-1", handler: s.handleCFSnippetRule},
		{name: "waf", method: http.MethodGet, target: "/api/cf/waf?zone_id=zone-1", handler: s.handleCFWAF},
		{name: "waf rule create", method: http.MethodPost, target: "/api/cf/waf/rules?zone_id=zone-1", body: strings.NewReader(`{"action":"block","expression":"true","description":"Block all","enabled":true}`), handler: s.handleCFWAFRules},
		{name: "waf rule update", method: http.MethodPatch, target: "/api/cf/waf/rules/rule-1?zone_id=zone-1", body: strings.NewReader(`{"enabled":false}`), handler: s.handleCFWAFRule},
		{name: "waf rule delete", method: http.MethodDelete, target: "/api/cf/waf/rules/rule-1?zone_id=zone-1", handler: s.handleCFWAFRule},
		{name: "waf managed exceptions", method: http.MethodGet, target: "/api/cf/waf/managed-exceptions?zone_id=zone-1", handler: s.handleCFWAFManagedExceptions},
		{name: "waf managed exception create", method: http.MethodPost, target: "/api/cf/waf/managed-exceptions/rules?zone_id=zone-1", body: strings.NewReader(`{"expression":"true","description":"Skip managed ruleset","enabled":true,"action_parameters":{"rulesets":["managed-ruleset-1"]}}`), handler: s.handleCFWAFManagedExceptionRules},
		{name: "waf managed exception update", method: http.MethodPatch, target: "/api/cf/waf/managed-exceptions/rules/rule-1?zone_id=zone-1", body: strings.NewReader(`{"enabled":false}`), handler: s.handleCFWAFManagedExceptionRule},
		{name: "waf managed exception delete", method: http.MethodDelete, target: "/api/cf/waf/managed-exceptions/rules/rule-1?zone_id=zone-1", handler: s.handleCFWAFManagedExceptionRule},
		{name: "waf managed overrides", method: http.MethodGet, target: "/api/cf/waf/managed-overrides?zone_id=zone-1", handler: s.handleCFWAFManagedOverrides},
		{name: "waf managed override create", method: http.MethodPost, target: "/api/cf/waf/managed-overrides/rules?zone_id=zone-1", body: strings.NewReader(`{"managed_ruleset_id":"managed-ruleset-1","expression":"true","description":"Override managed ruleset","enabled":true,"overrides":{"action":"log"}}`), handler: s.handleCFWAFManagedOverrideRules},
		{name: "waf managed override update", method: http.MethodPatch, target: "/api/cf/waf/managed-overrides/rules/rule-1?zone_id=zone-1", body: strings.NewReader(`{"enabled":false}`), handler: s.handleCFWAFManagedOverrideRule},
		{name: "waf managed override delete", method: http.MethodDelete, target: "/api/cf/waf/managed-overrides/rules/rule-1?zone_id=zone-1", handler: s.handleCFWAFManagedOverrideRule},
		{name: "zone analytics", method: http.MethodGet, target: "/api/cf/analytics/zone?zone_id=zone-1&range=24h", handler: s.handleCFZoneAnalytics},
		{name: "zone settings", method: http.MethodGet, target: "/api/cf/zone-settings?zone_id=zone-1", handler: s.handleCFZoneSettings},
		{name: "zone setting update", method: http.MethodPatch, target: "/api/cf/zone-settings/development_mode?zone_id=zone-1", body: strings.NewReader(`{"value":"on"}`), handler: s.handleCFZoneSetting},
		{name: "cache purge", method: http.MethodPost, target: "/api/cf/cache/purge?zone_id=zone-1", handler: s.handleCFCachePurge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.target, tt.body)
			rec := httptest.NewRecorder()

			tt.handler(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestOAuthLoginCallbackAndOverviewEndToEndWithFakeCloudflare(t *testing.T) {
	ctx := context.Background()
	api := newFakeOAuthCloudflareServer(t)
	defer api.Close()

	s := newServerTestServer(t)
	store := cfoauth.NewStore(s.cfgMgr.Dir())
	oauthSvc := cfoauth.NewService(cfoauth.Config{
		ClientID:          "client-id",
		RelayCallbackURL:  api.URL + "/oauth/callback",
		LocalCallbackPath: "/oauth/callback",
		AuthorizationURL:  api.URL + "/oauth/authorize",
		LogoutURL:         api.URL + "/logout",
		TokenURL:          api.URL + "/oauth/token",
		RevokeURL:         api.URL + "/oauth/revoke",
		UserInfoURL:       api.URL + "/oauth/userinfo",
		Scopes: strings.Join([]string{
			"account-settings.read",
			"zone.read",
			"dns.read",
			"argotunnel.read",
			"workers-scripts.read",
			"workers-r2.read",
			"d1.read",
			"workers-kv-storage.read",
			"snippets.read",
			"zone-waf.read",
		}, " "),
		Configured: true,
	}, store)
	s.oauthSvc = oauthSvc
	s.cfSvc = cfaccount.NewServiceWithEndpoints(oauthSvc, cfaccount.EndpointOverrides{
		REST:   api.URL + "/client/v4",
		Status: api.URL + "/status",
	})

	freshLoginReq := httptest.NewRequest(http.MethodPost, "/api/oauth/login", strings.NewReader(`{"scope":"dns.read","fresh_login":true}`))
	freshLoginReq.Header.Set("Content-Type", "application/json")
	freshLoginRec := httptest.NewRecorder()
	s.handleOAuthLogin(freshLoginRec, freshLoginReq)
	if freshLoginRec.Code != http.StatusOK {
		t.Fatalf("fresh login status %d: %s", freshLoginRec.Code, freshLoginRec.Body.String())
	}
	var freshLoginResp struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(freshLoginRec.Body).Decode(&freshLoginResp); err != nil {
		t.Fatalf("decode fresh login response: %v", err)
	}
	freshURL, err := url.Parse(freshLoginResp.URL)
	if err != nil {
		t.Fatalf("parse fresh login url: %v", err)
	}
	if freshURL.Path != "/logout" {
		t.Fatalf("fresh login URL path = %q, want /logout", freshURL.Path)
	}
	freshAuthorize, err := url.Parse(freshURL.Query().Get("to"))
	if err != nil {
		t.Fatalf("parse fresh login authorize URL: %v", err)
	}
	if freshAuthorize.Path != "/oauth/authorize" || freshAuthorize.Query().Get("scope") != "dns.read" || freshAuthorize.Query().Get("state") == "" {
		t.Fatalf("unexpected fresh authorize URL: %s", freshAuthorize.String())
	}

	loginReq := httptest.NewRequest(http.MethodPost, "/api/oauth/login", strings.NewReader(`{"scope":"dns.read zone.read account-settings.read workers-r2.read d1.read workers-kv-storage.read snippets.read zone-waf.read argotunnel.read workers-scripts.read"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	s.handleOAuthLogin(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status %d: %s", loginRec.Code, loginRec.Body.String())
	}
	var loginResp struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(loginRec.Body).Decode(&loginResp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	startURL, err := url.Parse(loginResp.URL)
	if err != nil {
		t.Fatalf("parse start url: %v", err)
	}
	state := startURL.Query().Get("state")
	if state == "" || startURL.Query().Get("code_challenge") == "" {
		t.Fatalf("login URL missing OAuth state or PKCE challenge: %s", loginResp.URL)
	}

	callbackReq := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=auth-code&state="+url.QueryEscape(state), nil)
	callbackRec := httptest.NewRecorder()
	s.handleOAuthCallback(callbackRec, callbackReq)
	if callbackRec.Code != http.StatusFound {
		t.Fatalf("callback status %d: %s", callbackRec.Code, callbackRec.Body.String())
	}
	if location := callbackRec.Header().Get("Location"); location != "/cloudflare?oauth=success" {
		t.Fatalf("callback location = %q, want success redirect", location)
	}

	errorReq := httptest.NewRequest(http.MethodGet, "/oauth/callback?error=invalid_scope&error_description="+url.QueryEscape("scope account-settings.read is not allowed"), nil)
	errorRec := httptest.NewRecorder()
	s.handleOAuthCallback(errorRec, errorReq)
	if errorRec.Code != http.StatusFound {
		t.Fatalf("error callback status %d: %s", errorRec.Code, errorRec.Body.String())
	}
	errorLocation, err := url.Parse(errorRec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse error callback location: %v", err)
	}
	if got := errorLocation.Query().Get("message"); got != "invalid_scope: scope account-settings.read is not allowed" {
		t.Fatalf("error callback message = %q", got)
	}

	assertOAuthSessionRows(t, s.cfgMgr.Dir(), 1)
	assertNoJSONFiles(t, s.cfgMgr.Dir())

	statusReq := httptest.NewRequest(http.MethodGet, "/api/oauth/status", nil)
	statusRec := httptest.NewRecorder()
	s.handleOAuthStatus(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("status code %d: %s", statusRec.Code, statusRec.Body.String())
	}
	if strings.Contains(statusRec.Body.String(), "access-token") || strings.Contains(statusRec.Body.String(), "refresh-token") {
		t.Fatalf("oauth status leaked token material: %s", statusRec.Body.String())
	}
	var status cfoauth.Status
	if err := json.NewDecoder(statusRec.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if !status.LoggedIn || status.Current == nil || status.Current.Label != "me@example.com" {
		t.Fatalf("unexpected oauth status: %#v", status)
	}

	overviewReq := httptest.NewRequest(http.MethodGet, "/api/cf/overview?account_id=account-1", nil)
	overviewRec := httptest.NewRecorder()
	s.handleCFOverview(overviewRec, overviewReq)
	if overviewRec.Code != http.StatusOK {
		t.Fatalf("overview status %d: %s", overviewRec.Code, overviewRec.Body.String())
	}
	if strings.Contains(overviewRec.Body.String(), "access-token") || strings.Contains(overviewRec.Body.String(), "refresh-token") {
		t.Fatalf("overview leaked token material: %s", overviewRec.Body.String())
	}
	var overview cfaccount.OverviewResponse
	if err := json.NewDecoder(overviewRec.Body).Decode(&overview); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	if overview.Account == nil || overview.Account.ID != "account-1" || overview.Zone == nil || overview.Zone.ID != "zone-1" {
		t.Fatalf("unexpected overview context: account=%#v zone=%#v", overview.Account, overview.Zone)
	}
	metrics := overviewMetricMap(overview.Metrics)
	for id, want := range map[string]int{
		"accounts":     1,
		"zones":        1,
		"dns_records":  3,
		"workers":      1,
		"tunnels":      1,
		"r2_buckets":   1,
		"d1_databases": 1,
		"waf_rules":    1,
	} {
		metric, ok := metrics[id]
		if !ok {
			t.Fatalf("missing overview metric %s in %#v", id, overview.Metrics)
		}
		if !metric.Available || metric.Value != want {
			t.Fatalf("metric %s = %#v, want value %d available", id, metric, want)
		}
	}
	if !api.sawTokenRequest || !api.sawUserInfoRequest || !api.sawCloudflareBearer {
		t.Fatalf("expected fake OAuth and Cloudflare endpoints to be exercised: %#v", api)
	}

	if _, err := store.ConsumeState(ctx, state); err != cfoauth.ErrStateExpired {
		t.Fatalf("expected consumed OAuth state to be gone, got %v", err)
	}
}

func TestCFTunnelCreateCanSaveAndActivateLocalProfile(t *testing.T) {
	api := newFakeOAuthCloudflareServer(t)
	defer api.Close()

	s := newServerTestServer(t)
	store := cfoauth.NewStore(s.cfgMgr.Dir())
	if err := store.SaveSession(context.Background(), cfoauth.Session{
		ID:           "session-1",
		Label:        "me@example.com",
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    time.Now().UTC().Add(time.Hour),
		Scope:        "cloudflare-tunnel.read cloudflare-tunnel.write",
		Current:      true,
	}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	oauthSvc := cfoauth.NewService(cfoauth.Config{Configured: true}, store)
	s.oauthSvc = oauthSvc
	s.cfSvc = cfaccount.NewServiceWithEndpoints(oauthSvc, cfaccount.EndpointOverrides{
		REST: api.URL + "/client/v4",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/cf/tunnels?account_id=account-1", strings.NewReader(`{"name":"edge local","save_local_profile":true,"activate_local":true}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleCFTunnels(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create tunnel status %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "connector-token") {
		t.Fatalf("connector token leaked in response: %s", rec.Body.String())
	}
	var resp struct {
		Tunnel       cfaccount.Tunnel `json:"tunnel"`
		LocalProfile *struct {
			Key      string `json:"key"`
			Name     string `json:"name"`
			TunnelID string `json:"tunnel_id"`
			Active   bool   `json:"active"`
		} `json:"local_profile"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode create tunnel response: %v", err)
	}
	if resp.Tunnel.ID != "tunnel-created" || resp.Tunnel.Name != "edge local" {
		t.Fatalf("unexpected tunnel response: %#v", resp.Tunnel)
	}
	if resp.LocalProfile == nil || resp.LocalProfile.Key == "" || !resp.LocalProfile.Active || resp.LocalProfile.TunnelID != "tunnel-created" {
		t.Fatalf("unexpected local profile summary: %#v", resp.LocalProfile)
	}

	cfg := s.cfgMgr.Get()
	if cfg.ActiveTunnelKey != resp.LocalProfile.Key {
		t.Fatalf("active tunnel key = %q, want %q", cfg.ActiveTunnelKey, resp.LocalProfile.Key)
	}
	var savedToken string
	for _, profile := range cfg.Tunnels {
		if profile.Key == resp.LocalProfile.Key {
			if !profile.LocalEnabled || !profile.RemoteManagementEnabled || profile.AccountID != "account-1" || profile.TunnelID != "tunnel-created" {
				t.Fatalf("unexpected saved profile: %#v", profile)
			}
			savedToken = profile.Token
		}
	}
	if savedToken != "connector-token" {
		t.Fatalf("saved connector token = %q, want connector-token", savedToken)
	}
}

func TestOAuthSessionSwitchRequiresValidSession(t *testing.T) {
	s := newServerTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/session", strings.NewReader(`{"session_id":"missing"}`))
	rec := httptest.NewRecorder()

	s.handleOAuthSession(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing session, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestOAuthSessionPatchUpdatesIdentityLabel(t *testing.T) {
	s := newServerTestServer(t)
	store := cfoauth.NewStore(s.cfgMgr.Dir())
	if err := store.SaveSession(context.Background(), cfoauth.Session{
		ID:          "session-1",
		Label:       "Original",
		AccessToken: "access-token",
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
		Scope:       "zone.read",
	}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	s.oauthSvc = cfoauth.NewService(cfoauth.Config{Configured: true}, store)

	req := httptest.NewRequest(http.MethodPatch, "/api/oauth/session", strings.NewReader(`{"session_id":"session-1","label":"  Primary account  "}`))
	rec := httptest.NewRecorder()
	s.handleOAuthSession(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var status cfoauth.Status
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("Decode status: %v", err)
	}
	if status.Current == nil || status.Current.Label != "Primary account" {
		t.Fatalf("unexpected status: %#v", status)
	}
	if strings.Contains(rec.Body.String(), "access-token") {
		t.Fatalf("session response leaked token: %s", rec.Body.String())
	}

	badReq := httptest.NewRequest(http.MethodPatch, "/api/oauth/session", strings.NewReader(`{"session_id":"session-1","label":" "}`))
	badRec := httptest.NewRecorder()
	s.handleOAuthSession(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for blank label, got %d: %s", badRec.Code, badRec.Body.String())
	}
}

func TestOAuthRelayCheckHandler(t *testing.T) {
	var sawHealth bool
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Fatalf("relay path = %q, want /health", r.URL.Path)
		}
		sawHealth = true
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(relay.Close)

	s := newServerTestServer(t)
	store := cfoauth.NewStore(s.cfgMgr.Dir())
	s.oauthSvc = cfoauth.NewService(cfoauth.Config{
		ClientID:          "client-id",
		RelayCallbackURL:  relay.URL + "/oauth/callback",
		LocalCallbackPath: "/oauth/callback",
		Scopes:            "zone.read",
		Configured:        true,
	}, store)

	req := httptest.NewRequest(http.MethodGet, "/api/oauth/relay-check", nil)
	rec := httptest.NewRecorder()
	s.handleOAuthRelayCheck(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("relay check status %d: %s", rec.Code, rec.Body.String())
	}
	if !sawHealth {
		t.Fatal("expected relay health endpoint to be requested")
	}
	var check cfoauth.RelayCheck
	if err := json.NewDecoder(rec.Body).Decode(&check); err != nil {
		t.Fatalf("decode relay check: %v", err)
	}
	if !check.Reachable || check.StatusCode != http.StatusOK || check.HealthURL != relay.URL+"/health" || check.Message != "ok" {
		t.Fatalf("unexpected relay check: %#v", check)
	}
}

func TestR2ObjectContentDispositionPreviewWhitelist(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		preview     bool
		want        string
	}{
		{name: "download image", contentType: "image/png", preview: false, want: "attachment"},
		{name: "preview image", contentType: "image/png", preview: true, want: "inline"},
		{name: "preview image with params", contentType: "image/jpeg; charset=binary", preview: true, want: "inline"},
		{name: "preview audio", contentType: "audio/mpeg", preview: true, want: "inline"},
		{name: "preview video", contentType: "video/mp4", preview: true, want: "inline"},
		{name: "preview pdf", contentType: "application/pdf", preview: true, want: "inline"},
		{name: "preview pdf with params", contentType: "application/pdf; charset=binary", preview: true, want: "inline"},
		{name: "do not inline svg", contentType: "image/svg+xml", preview: true, want: "attachment"},
		{name: "do not inline html", contentType: "text/html", preview: true, want: "attachment"},
		{name: "do not inline unknown", contentType: "application/octet-stream", preview: true, want: "attachment"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r2ObjectContentDisposition(tt.contentType, "object.bin", tt.preview)
			disposition, params, err := mime.ParseMediaType(got)
			if err != nil {
				t.Fatalf("ParseMediaType(%q): %v", got, err)
			}
			if disposition != tt.want {
				t.Fatalf("disposition = %q, want %q (%s)", disposition, tt.want, got)
			}
			if params["filename"] != "object.bin" {
				t.Fatalf("filename = %q, want object.bin", params["filename"])
			}
		})
	}
}

type fakeOAuthCloudflareServer struct {
	*httptest.Server
	sawTokenRequest     bool
	sawUserInfoRequest  bool
	sawCloudflareBearer bool
}

func newFakeOAuthCloudflareServer(t *testing.T) *fakeOAuthCloudflareServer {
	t.Helper()

	api := &fakeOAuthCloudflareServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("token method = %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse token form: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "authorization_code" {
			t.Fatalf("grant_type = %q, want authorization_code", got)
		}
		if got := r.Form.Get("client_id"); got != "client-id" {
			t.Fatalf("client_id = %q, want client-id", got)
		}
		if got := r.Form.Get("code"); got != "auth-code" {
			t.Fatalf("code = %q, want auth-code", got)
		}
		if got := r.Form.Get("code_verifier"); strings.TrimSpace(got) == "" {
			t.Fatalf("expected code_verifier")
		}
		api.sawTokenRequest = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-token",
			"refresh_token": "refresh-token",
			"expires_in":    3600,
			"scope": strings.Join([]string{
				"account-settings.read",
				"zone.read",
				"dns.read",
				"argotunnel.read",
				"workers-scripts.read",
				"workers-r2.read",
				"d1.read",
				"workers-kv-storage.read",
				"snippets.read",
				"zone-waf.read",
			}, " "),
		})
	})
	mux.HandleFunc("/oauth/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("userinfo method = %s", r.Method)
		}
		assertFakeBearer(t, api, r)
		api.sawUserInfoRequest = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"email":"me@example.com"}`))
	})
	mux.HandleFunc("/client/v4/accounts", func(w http.ResponseWriter, r *http.Request) {
		assertFakeBearer(t, api, r)
		writeServerCFEnvelope(w, `[{"id":"account-1","name":"Example Account","type":"standard"}]`, nil)
	})
	mux.HandleFunc("/client/v4/zones", func(w http.ResponseWriter, r *http.Request) {
		assertFakeBearer(t, api, r)
		if got := r.URL.Query().Get("account.id"); got != "account-1" {
			t.Fatalf("account.id = %q, want account-1", got)
		}
		writeServerCFEnvelope(w, `[{"id":"zone-1","name":"example.com","status":"active","account_id":"account-1"}]`, nil)
	})
	mux.HandleFunc("/client/v4/zones/zone-1/dns_records", func(w http.ResponseWriter, r *http.Request) {
		assertFakeBearer(t, api, r)
		writeServerCFEnvelope(w, `[{"id":"record-1"}]`, map[string]int{"count": 1, "total_count": 3})
	})
	mux.HandleFunc("/client/v4/accounts/account-1/cfd_tunnel", func(w http.ResponseWriter, r *http.Request) {
		assertFakeBearer(t, api, r)
		switch r.Method {
		case http.MethodGet:
			writeServerCFEnvelope(w, `[{"id":"tunnel-1"}]`, nil)
		case http.MethodPost:
			var req struct {
				Name      string `json:"name"`
				Secret    string `json:"tunnel_secret"`
				ConfigSrc string `json:"config_src"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode create tunnel request: %v", err)
			}
			if req.Name != "edge local" || req.Secret == "" || req.ConfigSrc != "cloudflare" {
				t.Fatalf("unexpected create tunnel request: %#v", req)
			}
			writeServerCFEnvelope(w, `{"id":"tunnel-created","name":"edge local","status":"inactive","tun_type":"cfd_tunnel","remote_config":true}`, nil)
		default:
			t.Fatalf("unexpected tunnel method = %s", r.Method)
		}
	})
	mux.HandleFunc("/client/v4/accounts/account-1/cfd_tunnel/tunnel-created/token", func(w http.ResponseWriter, r *http.Request) {
		assertFakeBearer(t, api, r)
		if r.Method != http.MethodGet {
			t.Fatalf("tunnel token method = %s", r.Method)
		}
		writeServerCFEnvelope(w, `"connector-token"`, nil)
	})
	mux.HandleFunc("/client/v4/accounts/account-1/workers/scripts", func(w http.ResponseWriter, r *http.Request) {
		assertFakeBearer(t, api, r)
		writeServerCFEnvelope(w, `[{"id":"worker-1"}]`, nil)
	})
	mux.HandleFunc("/client/v4/accounts/account-1/r2/buckets", func(w http.ResponseWriter, r *http.Request) {
		assertFakeBearer(t, api, r)
		writeServerCFEnvelope(w, `{"buckets":[{"name":"assets"}]}`, nil)
	})
	mux.HandleFunc("/client/v4/accounts/account-1/d1/database", func(w http.ResponseWriter, r *http.Request) {
		assertFakeBearer(t, api, r)
		writeServerCFEnvelope(w, `[{"uuid":"database-1","name":"prod-db"}]`, nil)
	})
	mux.HandleFunc("/client/v4/accounts/account-1/storage/kv/namespaces", func(w http.ResponseWriter, r *http.Request) {
		assertFakeBearer(t, api, r)
		writeServerCFEnvelope(w, `[{"id":"namespace-1"}]`, nil)
	})
	mux.HandleFunc("/client/v4/zones/zone-1/snippets", func(w http.ResponseWriter, r *http.Request) {
		assertFakeBearer(t, api, r)
		writeServerCFEnvelope(w, `[{"snippet_name":"snippet_1"}]`, nil)
	})
	mux.HandleFunc("/client/v4/zones/zone-1/rulesets/phases/http_request_firewall_custom/entrypoint", func(w http.ResponseWriter, r *http.Request) {
		assertFakeBearer(t, api, r)
		writeServerCFEnvelope(w, `{"id":"ruleset-1","rules":[{"id":"rule-1"}]}`, nil)
	})
	mux.HandleFunc("/status/summary.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"page":{"id":"page","name":"Cloudflare","url":"https://www.cloudflarestatus.com","time_zone":"Etc/UTC","updated_at":"2026-06-18T11:00:00Z"},
			"status":{"indicator":"none","description":"All Systems Operational"},
			"components":[],
			"incidents":[],
			"scheduled_maintenances":[]
		}`))
	})
	mux.HandleFunc("/status/incidents.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"incidents":[]}`))
	})

	api.Server = httptest.NewServer(mux)
	return api
}

func assertFakeBearer(t *testing.T, api *fakeOAuthCloudflareServer, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
		t.Fatalf("unexpected authorization header: %q", got)
	}
	if strings.HasPrefix(r.URL.Path, "/client/v4/") {
		api.sawCloudflareBearer = true
	}
}

func writeServerCFEnvelope(w http.ResponseWriter, result string, info map[string]int) {
	w.Header().Set("Content-Type", "application/json")
	payload := map[string]any{
		"success": true,
	}
	if strings.TrimSpace(result) == "" {
		payload["result"] = nil
	} else {
		payload["result"] = json.RawMessage(result)
	}
	if info != nil {
		payload["result_info"] = info
	}
	_ = json.NewEncoder(w).Encode(payload)
}

func assertOAuthSessionRows(t *testing.T, dir string, want int) {
	t.Helper()

	db, err := persist.OpenRawDB(dir)
	if err != nil {
		t.Fatalf("OpenRawDB: %v", err)
	}
	defer db.Close()

	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM oauth_sessions`).Scan(&got); err != nil {
		t.Fatalf("query oauth_sessions: %v", err)
	}
	if got != want {
		t.Fatalf("oauth_sessions rows = %d, want %d", got, want)
	}

	assertOAuthTableHasToken(t, db, "access-token", "refresh-token")
}

func assertOAuthTableHasToken(t *testing.T, db *sql.DB, accessToken, refreshToken string) {
	t.Helper()

	var got int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM oauth_sessions WHERE access_token = ? AND refresh_token = ?`,
		accessToken,
		refreshToken,
	).Scan(&got); err != nil {
		t.Fatalf("query oauth session token columns: %v", err)
	}
	if got != 1 {
		t.Fatalf("expected OAuth token material in SQLite session row, got %d rows", got)
	}
}

func assertNoJSONFiles(t *testing.T, dir string) {
	t.Helper()
	if err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			t.Fatalf("OAuth store must not write JSON files, found %s", path)
		}
		return nil
	}); err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
}

func overviewMetricMap(items []cfaccount.OverviewMetric) map[string]cfaccount.OverviewMetric {
	out := make(map[string]cfaccount.OverviewMetric, len(items))
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}
