package server

import (
	"bytes"
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
	"strconv"
	"strings"
	"testing"
	"time"

	"cfui/internal/cfaccount"
	"cfui/internal/cfoauth"
	"cfui/internal/config"
	"cfui/internal/persist"

	cloudflare "github.com/cloudflare/cloudflare-go"
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
		{name: "tunnel delete", method: http.MethodDelete, target: "/api/cf/tunnels/tunnel-1?account_id=account-1", handler: s.handleCFTunnel},
		{name: "tunnel config", method: http.MethodGet, target: "/api/cf/tunnels/tunnel-1/config?account_id=account-1", handler: s.handleCFTunnel},
		{name: "tunnel config entry create", method: http.MethodPost, target: "/api/cf/tunnels/tunnel-1/config/entries?account_id=account-1", body: strings.NewReader(`{"hostname":"app.example.com","service":"http://localhost:8080"}`), handler: s.handleCFTunnel},
		{name: "tunnel config entry reorder", method: http.MethodPost, target: "/api/cf/tunnels/tunnel-1/config/entries/reorder?account_id=account-1", body: strings.NewReader(`{"order":[1,0]}`), handler: s.handleCFTunnel},
		{name: "tunnel config entry update", method: http.MethodPut, target: "/api/cf/tunnels/tunnel-1/config/entries/0?account_id=account-1", body: strings.NewReader(`{"hostname":"app.example.com","service":"http://localhost:8080"}`), handler: s.handleCFTunnel},
		{name: "tunnel config entry delete", method: http.MethodDelete, target: "/api/cf/tunnels/tunnel-1/config/entries/0?account_id=account-1", handler: s.handleCFTunnel},
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
		{name: "r2 object chunked upload start", method: http.MethodPost, target: "/api/cf/r2/object/upload-session", body: strings.NewReader(`{"account_id":"account-1","bucket":"bucket-one","key":"a/b.bin","size":6}`), handler: s.handleCFR2ObjectUploadSession},
		{name: "r2 object copy", method: http.MethodPost, target: "/api/cf/r2/object/copy?account_id=account-1&bucket=bucket-one", body: strings.NewReader(`{"source_key":"a/b.txt","destination_key":"a/c.txt"}`), handler: s.handleCFR2ObjectCopy},
		{name: "r2 object download", method: http.MethodGet, target: "/api/cf/r2/object/download?account_id=account-1&bucket=bucket-one&key=a%2Fb.bin", handler: s.handleCFR2ObjectDownload},
		{name: "r2 object delete", method: http.MethodDelete, target: "/api/cf/r2/object?account_id=account-1&bucket=bucket-one&key=a%2Fb.txt", handler: s.handleCFR2Object},
		{name: "d1 databases", method: http.MethodGet, target: "/api/cf/d1/databases?account_id=account-1", handler: s.handleCFD1Databases},
		{name: "d1 database create", method: http.MethodPost, target: "/api/cf/d1/databases?account_id=account-1", body: strings.NewReader(`{"name":"prod-db"}`), handler: s.handleCFD1Databases},
		{name: "d1 database detail", method: http.MethodGet, target: "/api/cf/d1/databases/database-1?account_id=account-1", handler: s.handleCFD1Database},
		{name: "d1 database delete", method: http.MethodDelete, target: "/api/cf/d1/databases/database-1?account_id=account-1", handler: s.handleCFD1Database},
		{name: "d1 query", method: http.MethodPost, target: "/api/cf/d1/query?account_id=account-1&database_id=database-1", body: strings.NewReader(`{"sql":"SELECT 1"}`), handler: s.handleCFD1Query},
		{name: "d1 tables", method: http.MethodGet, target: "/api/cf/d1/tables?account_id=account-1&database_id=database-1", handler: s.handleCFD1Tables},
		{name: "d1 table rows", method: http.MethodGet, target: "/api/cf/d1/table?account_id=account-1&database_id=database-1&table=users", handler: s.handleCFD1Table},
		{name: "d1 row update", method: http.MethodPatch, target: "/api/cf/d1/table?account_id=account-1&database_id=database-1&table=users", body: strings.NewReader(`{"rowid":"1","changes":{"name":"Ada"}}`), handler: s.handleCFD1Table},
		{name: "d1 row delete", method: http.MethodDelete, target: "/api/cf/d1/table?account_id=account-1&database_id=database-1&table=users", body: strings.NewReader(`{"rowid":"1"}`), handler: s.handleCFD1Table},
		{name: "kv namespaces", method: http.MethodGet, target: "/api/cf/kv/namespaces?account_id=account-1", handler: s.handleCFKVNamespaces},
		{name: "kv namespace create", method: http.MethodPost, target: "/api/cf/kv/namespaces?account_id=account-1", body: strings.NewReader(`{"title":"cache"}`), handler: s.handleCFKVNamespaces},
		{name: "kv namespace rename", method: http.MethodPut, target: "/api/cf/kv/namespaces/namespace-1?account_id=account-1", body: strings.NewReader(`{"title":"cache-renamed"}`), handler: s.handleCFKVNamespace},
		{name: "kv namespace delete", method: http.MethodDelete, target: "/api/cf/kv/namespaces/namespace-1?account_id=account-1", handler: s.handleCFKVNamespace},
		{name: "kv keys", method: http.MethodGet, target: "/api/cf/kv/keys?account_id=account-1&namespace_id=namespace-1", handler: s.handleCFKVKeys},
		{name: "kv keys bulk delete", method: http.MethodPost, target: "/api/cf/kv/keys/bulk-delete?account_id=account-1&namespace_id=namespace-1", body: strings.NewReader(`{"keys":["a","b"]}`), handler: s.handleCFKVKeysBulkDelete},
		{name: "kv value get", method: http.MethodGet, target: "/api/cf/kv/value?account_id=account-1&namespace_id=namespace-1&key=a%2Fb", handler: s.handleCFKVValue},
		{name: "kv value download", method: http.MethodGet, target: "/api/cf/kv/value/download?account_id=account-1&namespace_id=namespace-1&key=a%2Fb", handler: s.handleCFKVValueDownload},
		{name: "kv value upload", method: http.MethodPost, target: "/api/cf/kv/value/upload?account_id=account-1&namespace_id=namespace-1&key=a%2Fb", body: strings.NewReader("binary"), handler: s.handleCFKVValueUpload},
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
	loginReq.Host = "cfui.example.internal"
	loginReq.Header.Set("X-Forwarded-Proto", "https")
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
	callbackURL, ok := cfoauth.RelayStateCallbackURL(state)
	if !ok {
		t.Fatalf("login state missing encoded callback URL: %s", loginResp.URL)
	}
	if got, want := callbackURL, "https://cfui.example.internal/oauth/callback"; got != want {
		t.Fatalf("callback URL in state = %q, want %q", got, want)
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

func TestCFValidationReportArchiveHandlersReadAndDeleteLocalSnapshots(t *testing.T) {
	s := newServerTestServer(t)
	store := cfoauth.NewStore(s.cfgMgr.Dir())
	oauthSvc := cfoauth.NewService(cfoauth.Config{Configured: true}, store)
	s.oauthSvc = oauthSvc

	saved, err := oauthSvc.SaveValidationReportArchive(context.Background(), cfoauth.ValidationReportArchiveInput{
		SessionID:      "session-1",
		SessionLabel:   "Production",
		AccountID:      "account-1",
		AccountName:    "Example Account",
		GeneratedAt:    time.Date(2026, 6, 19, 9, 0, 0, 0, time.UTC),
		ScopeMissing:   1,
		APIUnavailable: 2,
		ActionItems:    3,
		ReportBody:     []byte(`{"version":1,"contains_oauth_token":false,"contains_refresh_token":false,"summary":{"scope_missing":1}}`),
	})
	if err != nil {
		t.Fatalf("SaveValidationReportArchive: %v", err)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/cf/validation-reports?limit=12", nil)
	listRec := httptest.NewRecorder()
	s.handleCFValidationReports(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status %d: %s", listRec.Code, listRec.Body.String())
	}
	if strings.Contains(listRec.Body.String(), "contains_oauth_token") || strings.Contains(listRec.Body.String(), "access-token") || strings.Contains(listRec.Body.String(), "refresh-token") {
		t.Fatalf("list response leaked report or token material: %s", listRec.Body.String())
	}
	var listResp struct {
		Data []cfoauth.ValidationReportArchiveSummary `json:"data"`
	}
	if err := json.NewDecoder(listRec.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listResp.Data) != 1 || listResp.Data[0].ReportID != saved.ReportID || listResp.Data[0].ActionItems != 3 {
		t.Fatalf("unexpected archive list: %#v", listResp.Data)
	}

	detailReq := httptest.NewRequest(http.MethodGet, "/api/cf/validation-reports/"+saved.ReportID, nil)
	detailRec := httptest.NewRecorder()
	s.handleCFValidationReport(detailRec, detailReq)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("detail status %d: %s", detailRec.Code, detailRec.Body.String())
	}
	var detail cfoauth.ValidationReportArchiveDetail
	if err := json.NewDecoder(detailRec.Body).Decode(&detail); err != nil {
		t.Fatalf("decode detail response: %v", err)
	}
	if detail.ReportID != saved.ReportID || !strings.Contains(string(detail.Report), `"contains_oauth_token":false`) {
		t.Fatalf("unexpected archive detail: %#v", detail)
	}

	badPathReq := httptest.NewRequest(http.MethodGet, "/api/cf/validation-reports/"+saved.ReportID+"/extra", nil)
	badPathRec := httptest.NewRecorder()
	s.handleCFValidationReport(badPathRec, badPathReq)
	if badPathRec.Code != http.StatusBadRequest {
		t.Fatalf("bad path status = %d, want 400: %s", badPathRec.Code, badPathRec.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/cf/validation-reports/"+saved.ReportID, nil)
	deleteRec := httptest.NewRecorder()
	s.handleCFValidationReport(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete status %d: %s", deleteRec.Code, deleteRec.Body.String())
	}

	missingReq := httptest.NewRequest(http.MethodGet, "/api/cf/validation-reports/"+saved.ReportID, nil)
	missingRec := httptest.NewRecorder()
	s.handleCFValidationReport(missingRec, missingReq)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("missing detail status = %d, want 404: %s", missingRec.Code, missingRec.Body.String())
	}
}

func TestCFValidationReportArchivePostGeneratesServerSideSnapshot(t *testing.T) {
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
		Scope:        "account-settings.read zone.read dns.read argotunnel.read workers-scripts.read workers-r2.read d1.read workers-kv-storage.read snippets.read zone-waf.read",
		Current:      true,
	}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	oauthSvc := cfoauth.NewService(cfoauth.Config{
		ClientID:   "client-id",
		Scopes:     "account-settings.read zone.read dns.read argotunnel.read workers-scripts.read workers-r2.read d1.read workers-kv-storage.read snippets.read zone-waf.read",
		Configured: true,
	}, store)
	s.oauthSvc = oauthSvc
	s.cfSvc = cfaccount.NewServiceWithEndpoints(oauthSvc, cfaccount.EndpointOverrides{
		REST:   api.URL + "/client/v4",
		Status: api.URL + "/status",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/cf/validation-reports", strings.NewReader(`{"account_id":"account-1"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleCFValidationReports(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("archive post status %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "access-token") || strings.Contains(rec.Body.String(), "refresh-token") {
		t.Fatalf("archive response leaked OAuth token material: %s", rec.Body.String())
	}
	var saved cfoauth.ValidationReportArchiveDetail
	if err := json.NewDecoder(rec.Body).Decode(&saved); err != nil {
		t.Fatalf("decode archive response: %v", err)
	}
	if saved.ReportID == "" || saved.AccountID != "account-1" || saved.SessionID != "session-1" || len(saved.Report) == 0 {
		t.Fatalf("unexpected archive response: %#v", saved)
	}
	var report cfaccount.ValidationReport
	if err := json.Unmarshal(saved.Report, &report); err != nil {
		t.Fatalf("decode saved validation report: %v", err)
	}
	if report.ContainsOAuthToken || report.ContainsRefreshToken {
		t.Fatalf("saved report should be token-free: %#v", report)
	}
	if report.Account == nil || report.Account.ID != "account-1" || report.Summary.ScopeChecks == 0 || len(report.APIChecks) == 0 {
		t.Fatalf("saved report missing generated validation data: %#v", report)
	}

	items, err := oauthSvc.ListValidationReportArchives(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListValidationReportArchives: %v", err)
	}
	if len(items) != 1 || items[0].ReportID != saved.ReportID {
		t.Fatalf("archive was not stored in SQLite: %#v", items)
	}
}

func TestCFPermissionGroupsHandlerOmitsTokens(t *testing.T) {
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
		Scope:        "account-settings.read zone.read",
		Current:      true,
	}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	oauthSvc := cfoauth.NewService(cfoauth.Config{Configured: true}, store)
	s.oauthSvc = oauthSvc
	s.cfSvc = cfaccount.NewServiceWithEndpoints(oauthSvc, cfaccount.EndpointOverrides{
		REST: api.URL + "/client/v4",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/cf/permission-groups", nil)
	rec := httptest.NewRecorder()
	s.handleCFPermissionGroups(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("permission groups status %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "access-token") || strings.Contains(rec.Body.String(), "refresh-token") {
		t.Fatalf("permission groups response leaked OAuth token material: %s", rec.Body.String())
	}
	var resp cfaccount.PermissionGroupsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode permission groups response: %v", err)
	}
	if resp.Session.ID != "session-1" || len(resp.Data) != 2 {
		t.Fatalf("unexpected permission groups response: %#v", resp)
	}
	foundTunnel := false
	for _, group := range resp.Data {
		if group.Key == "argotunnel" {
			foundTunnel = true
			break
		}
	}
	if !foundTunnel {
		t.Fatalf("expected argotunnel permission group: %#v", resp.Data)
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

func TestCFTunnelsIncludesLocalProfileSummaryWithoutToken(t *testing.T) {
	api := newFakeOAuthCloudflareServer(t)
	defer api.Close()

	s := newServerTestServer(t)
	store := cfoauth.NewStore(s.cfgMgr.Dir())
	if err := store.SaveSession(context.Background(), cfoauth.Session{
		ID:          "session-1",
		Label:       "me@example.com",
		AccessToken: "access-token",
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
		Scope:       "cloudflare-tunnel.read",
		Current:     true,
	}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	oauthSvc := cfoauth.NewService(cfoauth.Config{Configured: true}, store)
	s.oauthSvc = oauthSvc
	s.cfSvc = cfaccount.NewServiceWithEndpoints(oauthSvc, cfaccount.EndpointOverrides{
		REST: api.URL + "/client/v4",
	})
	if _, err := s.cfgMgr.SaveTunnelProfile("", config.TunnelProfileConfig{
		Name:                    "edge local",
		Token:                   "connector-token",
		AccountID:               "account-1",
		TunnelID:                "tunnel-1",
		LocalEnabled:            true,
		RemoteManagementEnabled: true,
	}); err != nil {
		t.Fatalf("SaveTunnelProfile: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/cf/tunnels?account_id=account-1", nil)
	rec := httptest.NewRecorder()
	s.handleCFTunnels(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tunnels status %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "connector-token") {
		t.Fatalf("connector token leaked in response: %s", rec.Body.String())
	}
	var resp struct {
		Data          []cfaccount.Tunnel `json:"data"`
		LocalProfiles []struct {
			Key       string `json:"key"`
			Name      string `json:"name"`
			AccountID string `json:"account_id"`
			TunnelID  string `json:"tunnel_id"`
			Active    bool   `json:"active"`
			Running   bool   `json:"running"`
			Status    string `json:"status"`
		} `json:"local_profiles"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode tunnels response: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].ID != "tunnel-1" {
		t.Fatalf("unexpected tunnels: %#v", resp.Data)
	}
	if len(resp.LocalProfiles) != 1 {
		t.Fatalf("local_profiles length = %d, want 1: %#v", len(resp.LocalProfiles), resp.LocalProfiles)
	}
	profile := resp.LocalProfiles[0]
	if profile.Key == "" || profile.Name != "edge local" || profile.AccountID != "account-1" || profile.TunnelID != "tunnel-1" {
		t.Fatalf("unexpected local profile summary: %#v", profile)
	}
	if profile.Running || profile.Status != "unavailable" {
		t.Fatalf("unexpected local runner status in summary: %#v", profile)
	}
}

func TestCFTunnelDeleteCanRemoveLinkedLocalProfile(t *testing.T) {
	api := newFakeOAuthCloudflareServer(t)
	defer api.Close()

	s := newServerTestServer(t)
	store := cfoauth.NewStore(s.cfgMgr.Dir())
	if err := store.SaveSession(context.Background(), cfoauth.Session{
		ID:          "session-1",
		Label:       "me@example.com",
		AccessToken: "access-token",
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
		Scope:       "cloudflare-tunnel.read cloudflare-tunnel.write",
		Current:     true,
	}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	oauthSvc := cfoauth.NewService(cfoauth.Config{Configured: true}, store)
	s.oauthSvc = oauthSvc
	s.cfSvc = cfaccount.NewServiceWithEndpoints(oauthSvc, cfaccount.EndpointOverrides{
		REST: api.URL + "/client/v4",
	})
	cfg, err := s.cfgMgr.SaveTunnelProfile("", config.TunnelProfileConfig{
		Name:                    "edge local",
		Token:                   "connector-token",
		AccountID:               "account-1",
		TunnelID:                "tunnel-1",
		LocalEnabled:            true,
		RemoteManagementEnabled: true,
	})
	if err != nil {
		t.Fatalf("SaveTunnelProfile: %v", err)
	}
	if len(cfg.Tunnels) < 2 {
		t.Fatalf("expected fixture to have default and linked tunnel profiles: %#v", cfg.Tunnels)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/cf/tunnels/tunnel-1?account_id=account-1&delete_local_profile=true", nil)
	rec := httptest.NewRecorder()
	s.handleCFTunnel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete tunnel status %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "connector-token") {
		t.Fatalf("connector token leaked in response: %s", rec.Body.String())
	}
	var resp struct {
		Success             bool `json:"success"`
		LocalProfileRemoved bool `json:"local_profile_removed"`
		LocalProfile        *struct {
			Key      string `json:"key"`
			TunnelID string `json:"tunnel_id"`
		} `json:"local_profile"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if !resp.Success || !resp.LocalProfileRemoved || resp.LocalProfile == nil || resp.LocalProfile.TunnelID != "tunnel-1" {
		t.Fatalf("unexpected delete response: %#v", resp)
	}
	for _, profile := range s.cfgMgr.Get().Tunnels {
		if profile.TunnelID == "tunnel-1" || profile.Token == "connector-token" {
			t.Fatalf("linked local profile was not removed: %#v", s.cfgMgr.Get().Tunnels)
		}
	}
}

func TestCFTunnelDeleteClearsOnlyLinkedLocalProfile(t *testing.T) {
	api := newFakeOAuthCloudflareServer(t)
	defer api.Close()

	s := newServerTestServer(t)
	store := cfoauth.NewStore(s.cfgMgr.Dir())
	if err := store.SaveSession(context.Background(), cfoauth.Session{
		ID:          "session-1",
		Label:       "me@example.com",
		AccessToken: "access-token",
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
		Scope:       "cloudflare-tunnel.read cloudflare-tunnel.write",
		Current:     true,
	}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	oauthSvc := cfoauth.NewService(cfoauth.Config{Configured: true}, store)
	s.oauthSvc = oauthSvc
	s.cfSvc = cfaccount.NewServiceWithEndpoints(oauthSvc, cfaccount.EndpointOverrides{
		REST: api.URL + "/client/v4",
	})
	cfg, err := s.cfgMgr.SaveTunnelProfile(config.DefaultTunnelProfileKey, config.TunnelProfileConfig{
		Name:                    "only linked",
		Token:                   "connector-token",
		AccountID:               "account-1",
		TunnelID:                "tunnel-1",
		LocalEnabled:            true,
		RemoteManagementEnabled: true,
	})
	if err != nil {
		t.Fatalf("SaveTunnelProfile: %v", err)
	}
	if len(cfg.Tunnels) != 1 {
		t.Fatalf("expected one tunnel profile, got %#v", cfg.Tunnels)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/cf/tunnels/tunnel-1?account_id=account-1&delete_local_profile=true", nil)
	rec := httptest.NewRecorder()
	s.handleCFTunnel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete tunnel status %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "connector-token") {
		t.Fatalf("connector token leaked in response: %s", rec.Body.String())
	}
	var resp struct {
		Success             bool `json:"success"`
		LocalProfileRemoved bool `json:"local_profile_removed"`
		LocalProfile        *struct {
			Key      string `json:"key"`
			TunnelID string `json:"tunnel_id"`
		} `json:"local_profile"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if !resp.Success || resp.LocalProfileRemoved || resp.LocalProfile == nil || resp.LocalProfile.TunnelID != "" {
		t.Fatalf("unexpected delete response: %#v", resp)
	}
	next := s.cfgMgr.Get()
	if len(next.Tunnels) != 1 {
		t.Fatalf("expected only profile to be preserved, got %#v", next.Tunnels)
	}
	profile := next.Tunnels[0]
	if profile.Token != "" || profile.AccountID != "" || profile.TunnelID != "" || profile.RemoteManagementEnabled {
		t.Fatalf("linked fields were not cleared: %#v", profile)
	}
}

func TestCFTunnelConfigurationHandlersMutateIngress(t *testing.T) {
	api := newFakeOAuthCloudflareServer(t)
	defer api.Close()

	s := newServerTestServer(t)
	store := cfoauth.NewStore(s.cfgMgr.Dir())
	if err := store.SaveSession(context.Background(), cfoauth.Session{
		ID:          "session-1",
		Label:       "me@example.com",
		AccessToken: "access-token",
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
		Scope:       "cloudflare-tunnel.read cloudflare-tunnel.write",
		Current:     true,
	}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	oauthSvc := cfoauth.NewService(cfoauth.Config{Configured: true}, store)
	s.oauthSvc = oauthSvc
	s.cfSvc = cfaccount.NewServiceWithEndpoints(oauthSvc, cfaccount.EndpointOverrides{
		REST: api.URL + "/client/v4",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/cf/tunnels/tunnel-1/config?account_id=account-1", nil)
	rec := httptest.NewRecorder()
	s.handleCFTunnel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get config status %d: %s", rec.Code, rec.Body.String())
	}
	var loaded struct {
		TunnelID string `json:"tunnel_id"`
		Entries  []struct {
			Hostname    string `json:"hostname"`
			Service     string `json:"service"`
			NoTLSVerify bool   `json:"no_tls_verify"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&loaded); err != nil {
		t.Fatalf("decode tunnel config: %v", err)
	}
	if loaded.TunnelID != "tunnel-1" || len(loaded.Entries) != 2 || loaded.Entries[0].Hostname != "old.example.com" {
		t.Fatalf("unexpected loaded config: %#v", loaded)
	}

	body := strings.NewReader(`{"hostname":"app.example.com","path":"/api/*","service":"http://localhost:8080","no_tls_verify":true}`)
	req = httptest.NewRequest(http.MethodPost, "/api/cf/tunnels/tunnel-1/config/entries?account_id=account-1", body)
	rec = httptest.NewRecorder()
	s.handleCFTunnel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create entry status %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Version int `json:"version"`
		Entries []struct {
			Hostname    string `json:"hostname"`
			Path        string `json:"path"`
			Service     string `json:"service"`
			NoTLSVerify bool   `json:"no_tls_verify"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode created config: %v", err)
	}
	if created.Version != 8 || len(created.Entries) != 3 || created.Entries[1].Hostname != "app.example.com" || !created.Entries[1].NoTLSVerify || created.Entries[2].Service != "http_status:404" {
		t.Fatalf("unexpected created config: %#v", created)
	}

	body = strings.NewReader(`{"order":[1,0,2]}`)
	req = httptest.NewRequest(http.MethodPost, "/api/cf/tunnels/tunnel-1/config/entries/reorder?account_id=account-1", body)
	rec = httptest.NewRecorder()
	s.handleCFTunnel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reorder entry status %d: %s", rec.Code, rec.Body.String())
	}
	var reordered struct {
		Version int `json:"version"`
		Entries []struct {
			Hostname string `json:"hostname"`
			Service  string `json:"service"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&reordered); err != nil {
		t.Fatalf("decode reordered config: %v", err)
	}
	if reordered.Version != 9 || len(reordered.Entries) != 3 || reordered.Entries[0].Hostname != "app.example.com" || reordered.Entries[1].Hostname != "old.example.com" || reordered.Entries[2].Service != "http_status:404" {
		t.Fatalf("unexpected reordered config: %#v", reordered)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/cf/tunnels/tunnel-1/config/entries/0?account_id=account-1", nil)
	rec = httptest.NewRecorder()
	s.handleCFTunnel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete entry status %d: %s", rec.Code, rec.Body.String())
	}
	var deleted struct {
		Entries []struct {
			Service string `json:"service"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&deleted); err != nil {
		t.Fatalf("decode deleted config: %v", err)
	}
	if len(deleted.Entries) < 1 || deleted.Entries[len(deleted.Entries)-1].Service != "http_status:404" {
		t.Fatalf("delete response must keep catch-all last: %#v", deleted)
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
		w.Header().Set("X-CFUI-OAuth-Relay", "state-v1")
		_, _ = w.Write([]byte("ok state-v1"))
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
	if !check.Reachable || !check.SupportsStateCallback || check.StatusCode != http.StatusOK || check.HealthURL != relay.URL+"/health" || check.Message != "ok state-v1" {
		t.Fatalf("unexpected relay check: %#v", check)
	}
}

func TestOAuthConfigPatchPersistsRelayCallbackAndLoginUsesIt(t *testing.T) {
	t.Setenv("CFUI_OAUTH_CLIENT_ID", "client-id")
	t.Setenv("CFUI_OAUTH_AUTH_URL", "https://dash.example.test/oauth2/auth")

	s := newServerTestServer(t)
	req := httptest.NewRequest(http.MethodPatch, "/api/oauth/config", strings.NewReader(`{"relay_callback_url":"https://relay.example.test/oauth/callback?cfui_callback_url=http%3A%2F%2F127.0.0.1%3A14333%2Foauth%2Fcallback#ignored"}`))
	rec := httptest.NewRecorder()
	s.handleOAuthConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("config patch status %d: %s", rec.Code, rec.Body.String())
	}

	var status cfoauth.Status
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decode oauth status: %v", err)
	}
	const wantRelay = "https://relay.example.test/oauth/callback"
	if status.Config.RelayCallbackURL != wantRelay {
		t.Fatalf("relay callback = %q, want %q", status.Config.RelayCallbackURL, wantRelay)
	}
	if got := s.cfgMgr.Get().OAuthRelayCallbackURL; got != wantRelay {
		t.Fatalf("stored relay callback = %q, want %q", got, wantRelay)
	}

	loginReq := httptest.NewRequest(http.MethodPost, "/api/oauth/login", strings.NewReader(`{"scope":"zone.read"}`))
	loginReq.Host = "cfui.example.internal"
	loginReq.Header.Set("X-Forwarded-Proto", "https")
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
		t.Fatalf("parse login url: %v", err)
	}
	if got := startURL.Query().Get("redirect_uri"); got != wantRelay {
		t.Fatalf("redirect_uri = %q, want %q", got, wantRelay)
	}
}

func TestOAuthConfigPatchPersistsClientIDWhenEnvUnset(t *testing.T) {
	t.Setenv("CFUI_OAUTH_CLIENT_ID", "")
	t.Setenv("CFUI_OAUTH_AUTH_URL", "https://dash.example.test/oauth2/auth")

	s := newServerTestServer(t)
	req := httptest.NewRequest(http.MethodPatch, "/api/oauth/config", strings.NewReader(`{"client_id":"saved-client"}`))
	rec := httptest.NewRecorder()
	s.handleOAuthConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("config patch status %d: %s", rec.Code, rec.Body.String())
	}

	var status cfoauth.Status
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decode oauth status: %v", err)
	}
	if status.Config.ClientID != "saved-client" || status.Config.ClientIDSource != "saved" || !status.Config.Configured {
		t.Fatalf("unexpected oauth config: %#v", status.Config)
	}
	if got := s.cfgMgr.Get().OAuthClientID; got != "saved-client" {
		t.Fatalf("stored client id = %q, want saved-client", got)
	}

	loginReq := httptest.NewRequest(http.MethodPost, "/api/oauth/login", strings.NewReader(`{"scope":"zone.read"}`))
	loginReq.Host = "cfui.example.internal"
	loginReq.Header.Set("X-Forwarded-Proto", "https")
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
		t.Fatalf("parse login url: %v", err)
	}
	if got := startURL.Query().Get("client_id"); got != "saved-client" {
		t.Fatalf("client_id = %q, want saved-client", got)
	}
}

func TestOAuthConfigPatchEnvClientIDTakesPrecedence(t *testing.T) {
	t.Setenv("CFUI_OAUTH_CLIENT_ID", "env-client")
	t.Setenv("CFUI_OAUTH_AUTH_URL", "https://dash.example.test/oauth2/auth")

	s := newServerTestServer(t)
	req := httptest.NewRequest(http.MethodPatch, "/api/oauth/config", strings.NewReader(`{"client_id":"saved-client"}`))
	rec := httptest.NewRecorder()
	s.handleOAuthConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("config patch status %d: %s", rec.Code, rec.Body.String())
	}

	var status cfoauth.Status
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decode oauth status: %v", err)
	}
	if status.Config.ClientID != "env-client" || status.Config.ClientIDSource != "env" {
		t.Fatalf("env client id should take precedence: %#v", status.Config)
	}
	if got := s.cfgMgr.Get().OAuthClientID; got != "saved-client" {
		t.Fatalf("stored client id = %q, want saved-client", got)
	}

	loginReq := httptest.NewRequest(http.MethodPost, "/api/oauth/login", strings.NewReader(`{"scope":"zone.read"}`))
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
		t.Fatalf("parse login url: %v", err)
	}
	if got := startURL.Query().Get("client_id"); got != "env-client" {
		t.Fatalf("client_id = %q, want env-client", got)
	}
}

func TestOAuthConfigPatchRejectsInvalidRelayPath(t *testing.T) {
	t.Setenv("CFUI_OAUTH_CLIENT_ID", "client-id")
	s := newServerTestServer(t)
	req := httptest.NewRequest(http.MethodPatch, "/api/oauth/config", strings.NewReader(`{"relay_callback_url":"https://relay.example.test/callback"}`))
	rec := httptest.NewRecorder()

	s.handleOAuthConfig(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid relay path, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestR2ChunkedUploadSessionCompletesToCloudflare(t *testing.T) {
	const chunkSize = 1024 * 1024
	payload := append(bytes.Repeat([]byte("a"), chunkSize), []byte("tail")...)
	var sawPut bool
	mux := http.NewServeMux()
	mux.HandleFunc("/client/v4/accounts/account-1/r2/buckets/assets/objects/big.bin", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		if r.Method != http.MethodPut {
			t.Fatalf("method = %s, want PUT", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "text/plain" {
			t.Fatalf("content type = %q, want text/plain", got)
		}
		if r.ContentLength != int64(len(payload)) {
			t.Fatalf("content length = %d, want %d", r.ContentLength, len(payload))
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upload body: %v", err)
		}
		if !bytes.Equal(raw, payload) {
			t.Fatalf("uploaded payload mismatch")
		}
		sawPut = true
		writeServerCFEnvelope(w, `null`, nil)
	})
	api := httptest.NewServer(mux)
	t.Cleanup(api.Close)

	s := newServerTestServer(t)
	store := cfoauth.NewStore(s.cfgMgr.Dir())
	if err := store.SaveSession(context.Background(), cfoauth.Session{
		ID:           "session-1",
		Label:        "me@example.com",
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    time.Now().UTC().Add(time.Hour),
		Scope:        "workers-r2.write",
		Current:      true,
	}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	oauthSvc := cfoauth.NewService(cfoauth.Config{Configured: true}, store)
	s.oauthSvc = oauthSvc
	s.cfSvc = cfaccount.NewServiceWithEndpoints(oauthSvc, cfaccount.EndpointOverrides{
		REST: api.URL + "/client/v4",
	})

	startBody := strings.NewReader(`{"account_id":"account-1","bucket":"assets","key":"big.bin","content_type":"text/plain","size":1048580,"chunk_size":1048576}`)
	startReq := httptest.NewRequest(http.MethodPost, "/api/cf/r2/object/upload-session", startBody)
	startRec := httptest.NewRecorder()
	s.handleCFR2ObjectUploadSession(startRec, startReq)
	if startRec.Code != http.StatusOK {
		t.Fatalf("start status %d: %s", startRec.Code, startRec.Body.String())
	}
	var start r2UploadStatus
	if err := json.NewDecoder(startRec.Body).Decode(&start); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	if start.UploadID == "" || start.TotalChunks != 2 || start.ChunkSize != chunkSize {
		t.Fatalf("unexpected start status: %#v", start)
	}

	chunks := [][]byte{payload[:chunkSize], payload[chunkSize:]}
	for i, chunk := range chunks {
		req := httptest.NewRequest(http.MethodPut, "/api/cf/r2/object/upload-session/"+start.UploadID+"/chunks/"+strconv.Itoa(i), bytes.NewReader(chunk))
		rec := httptest.NewRecorder()
		s.handleCFR2ObjectUploadSessionItem(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("chunk %d status %d: %s", i, rec.Code, rec.Body.String())
		}
	}

	completeReq := httptest.NewRequest(http.MethodPost, "/api/cf/r2/object/upload-session/"+start.UploadID+"/complete", nil)
	completeRec := httptest.NewRecorder()
	s.handleCFR2ObjectUploadSessionItem(completeRec, completeReq)
	if completeRec.Code != http.StatusOK {
		t.Fatalf("complete status %d: %s", completeRec.Code, completeRec.Body.String())
	}
	if !sawPut {
		t.Fatal("expected upload to Cloudflare")
	}
	var object cfaccount.R2ObjectValue
	if err := json.NewDecoder(completeRec.Body).Decode(&object); err != nil {
		t.Fatalf("decode complete: %v", err)
	}
	if object.Key != "big.bin" || object.Bytes != len(payload) || object.ContentType != "text/plain" {
		t.Fatalf("unexpected object response: %#v", object)
	}
}

func TestR2ChunkedUploadSessionRequiresWriteScope(t *testing.T) {
	s := newServerTestServer(t)
	store := cfoauth.NewStore(s.cfgMgr.Dir())
	if err := store.SaveSession(context.Background(), cfoauth.Session{
		ID:           "session-1",
		Label:        "me@example.com",
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    time.Now().UTC().Add(time.Hour),
		Scope:        "workers-r2.read",
		Current:      true,
	}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	s.oauthSvc = cfoauth.NewService(cfoauth.Config{Configured: true}, store)

	req := httptest.NewRequest(http.MethodPost, "/api/cf/r2/object/upload-session", strings.NewReader(`{"account_id":"account-1","bucket":"assets","key":"big.bin","size":6}`))
	rec := httptest.NewRecorder()
	s.handleCFR2ObjectUploadSession(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
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
	mux.HandleFunc("/client/v4/accounts/account-1/cfd_tunnel/tunnel-1", func(w http.ResponseWriter, r *http.Request) {
		assertFakeBearer(t, api, r)
		if r.Method != http.MethodDelete {
			t.Fatalf("delete tunnel method = %s", r.Method)
		}
		writeServerCFEnvelope(w, `{"id":"tunnel-1","name":"edge local"}`, nil)
	})
	tunnelConfig := cloudflare.TunnelConfiguration{
		Ingress: []cloudflare.UnvalidatedIngressRule{
			{Hostname: "old.example.com", Service: "http://localhost:8080"},
			{Service: "http_status:404"},
		},
	}
	tunnelConfigVersion := 7
	mux.HandleFunc("/client/v4/accounts/account-1/cfd_tunnel/tunnel-1/configurations", func(w http.ResponseWriter, r *http.Request) {
		assertFakeBearer(t, api, r)
		switch r.Method {
		case http.MethodGet:
			result, err := json.Marshal(map[string]any{
				"tunnel_id": "tunnel-1",
				"version":   tunnelConfigVersion,
				"config":    tunnelConfig,
			})
			if err != nil {
				t.Fatalf("marshal tunnel configuration: %v", err)
			}
			writeServerCFEnvelope(w, string(result), nil)
		case http.MethodPut:
			var req cloudflare.TunnelConfigurationParams
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode tunnel configuration update: %v", err)
			}
			tunnelConfig = req.Config
			tunnelConfigVersion++
			result, err := json.Marshal(map[string]any{
				"tunnel_id": "tunnel-1",
				"version":   tunnelConfigVersion,
				"config":    tunnelConfig,
			})
			if err != nil {
				t.Fatalf("marshal tunnel configuration: %v", err)
			}
			writeServerCFEnvelope(w, string(result), nil)
		default:
			t.Fatalf("tunnel configuration method = %s", r.Method)
		}
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
	mux.HandleFunc("/client/v4/user/tokens/permission_groups", func(w http.ResponseWriter, r *http.Request) {
		assertFakeBearer(t, api, r)
		writeServerCFEnvelope(w, `[
			{"id":"pg-tunnel-read","name":"Cloudflare Tunnel Read","scopes":["com.cloudflare.api.account.cloudflare_tunnel"]},
			{"id":"pg-dns-read","name":"DNS Read","scopes":["com.cloudflare.api.account.zone"]}
		]`, nil)
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
