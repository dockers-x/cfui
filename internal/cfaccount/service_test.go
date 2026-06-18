package cfaccount

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"cfui/internal/cfoauth"

	cloudflare "github.com/cloudflare/cloudflare-go"
)

func TestMapZoneIncludesPlanAndNameServers(t *testing.T) {
	created := time.Date(2026, 6, 18, 8, 0, 0, 0, time.UTC)
	modified := created.Add(time.Hour)

	zone := mapZone(cloudflare.Zone{
		ID:          "zone-1",
		Name:        "example.com",
		Status:      "active",
		Type:        "full",
		Paused:      true,
		NameServers: []string{"fred.ns.cloudflare.com", "lola.ns.cloudflare.com"},
		OriginalNS:  []string{"ns1.example.com"},
		CreatedOn:   created,
		ModifiedOn:  modified,
		Account:     cloudflare.Account{ID: "account-1", Name: "Example Account", Type: "standard"},
		Plan:        cloudflare.ZonePlan{ZonePlanCommon: cloudflare.ZonePlanCommon{ID: "free", Name: "Free Website", Frequency: "monthly", Currency: "USD", Price: 0}, LegacyID: "free"},
	})

	if zone.ID != "zone-1" || zone.AccountID != "account-1" || zone.Plan.Name != "Free Website" {
		t.Fatalf("unexpected zone mapping: %#v", zone)
	}
	if len(zone.NameServers) != 2 || zone.NameServers[0] != "fred.ns.cloudflare.com" {
		t.Fatalf("unexpected name servers: %#v", zone.NameServers)
	}
	if zone.CreatedOn == nil || !zone.CreatedOn.Equal(created) || zone.ModifiedOn == nil || !zone.ModifiedOn.Equal(modified) {
		t.Fatalf("unexpected timestamps: created=%v modified=%v", zone.CreatedOn, zone.ModifiedOn)
	}
}

func TestMapTunnelIncludesControlPlaneDetails(t *testing.T) {
	created := time.Date(2026, 6, 18, 8, 0, 0, 0, time.UTC)
	deleted := created.Add(2 * time.Hour)
	activeAt := created.Add(10 * time.Minute)
	inactiveAt := created.Add(time.Hour)

	tunnel := mapTunnel(cloudflare.Tunnel{
		ID:             "tunnel-1",
		Name:           "prod-edge",
		Status:         "healthy",
		TunnelType:     "cfd_tunnel",
		RemoteConfig:   true,
		CreatedAt:      &created,
		DeletedAt:      &deleted,
		ConnsActiveAt:  &activeAt,
		ConnInactiveAt: &inactiveAt,
		Connections: []cloudflare.TunnelConnection{{
			ID:                 "conn-1",
			ColoName:           "SJC",
			ClientID:           "client-1",
			ClientVersion:      "2026.6.1",
			OpenedAt:           "2026-06-18T08:10:00Z",
			OriginIP:           "198.51.100.10",
			IsPendingReconnect: true,
		}},
	})

	if tunnel.ID != "tunnel-1" || tunnel.Name != "prod-edge" || tunnel.Status != "healthy" || tunnel.Type != "cfd_tunnel" || !tunnel.RemoteConfig {
		t.Fatalf("unexpected tunnel identity: %#v", tunnel)
	}
	if tunnel.CreatedAt == nil || !tunnel.CreatedAt.Equal(created) || tunnel.DeletedAt == nil || !tunnel.DeletedAt.Equal(deleted) {
		t.Fatalf("unexpected tunnel lifecycle timestamps: %#v", tunnel)
	}
	if tunnel.ConnectionsActiveAt == nil || !tunnel.ConnectionsActiveAt.Equal(activeAt) || tunnel.ConnectionsInactiveAt == nil || !tunnel.ConnectionsInactiveAt.Equal(inactiveAt) {
		t.Fatalf("unexpected connection timestamps: %#v", tunnel)
	}
	if tunnel.ConnectionCount != 1 || len(tunnel.Connections) != 1 {
		t.Fatalf("unexpected connection count: %#v", tunnel)
	}
	conn := tunnel.Connections[0]
	if conn.ID != "conn-1" || conn.ColoName != "SJC" || conn.ClientVersion != "2026.6.1" || conn.OpenedAt == "" || conn.OriginIP != "198.51.100.10" || !conn.IsPendingReconnect {
		t.Fatalf("unexpected connection mapping: %#v", conn)
	}
}

func TestMapD1DatabaseIncludesDetailFields(t *testing.T) {
	created := time.Date(2026, 6, 18, 9, 0, 0, 0, time.UTC)
	database := mapD1Database(cloudflare.D1Database{
		UUID:      "database-1",
		Name:      "prod-db",
		Version:   "beta",
		NumTables: 7,
		FileSize:  42 << 20,
		CreatedAt: &created,
	})

	if database.UUID != "database-1" || database.Name != "prod-db" || database.Version != "beta" {
		t.Fatalf("unexpected database identity: %#v", database)
	}
	if database.NumTables != 7 || database.FileSize != 42<<20 {
		t.Fatalf("unexpected database detail fields: %#v", database)
	}
	if database.CreatedAt == nil || !database.CreatedAt.Equal(created) {
		t.Fatalf("unexpected created time: %v", database.CreatedAt)
	}
}

func TestDNSRecordCountFromResult(t *testing.T) {
	records := []cloudflare.DNSRecord{{ID: "record-1"}, {ID: "record-2"}}
	tests := []struct {
		name string
		info *cloudflare.ResultInfo
		want int
	}{
		{name: "uses total count", info: &cloudflare.ResultInfo{Total: 42, Count: 5}, want: 42},
		{name: "falls back to page count", info: &cloudflare.ResultInfo{Count: 2}, want: 2},
		{name: "falls back to records length", info: &cloudflare.ResultInfo{}, want: 2},
		{name: "nil info", info: nil, want: 2},
		{name: "empty records zero total", info: &cloudflare.ResultInfo{}, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRecords := records
			if tt.name == "empty records zero total" {
				gotRecords = nil
			}
			if got := dnsRecordCountFromResult(gotRecords, tt.info); got != tt.want {
				t.Fatalf("count = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestNormalizeZoneSettingValue(t *testing.T) {
	tests := []struct {
		name      string
		settingID string
		value     any
		want      any
		wantErr   bool
	}{
		{name: "development mode on", settingID: "development_mode", value: "on", want: "on"},
		{name: "development mode off", settingID: "development_mode", value: "off", want: "off"},
		{name: "security high", settingID: "security_level", value: "high", want: "high"},
		{name: "security under attack", settingID: "security_level", value: "under_attack", want: "under_attack"},
		{name: "always use https on", settingID: "always_use_https", value: "on", want: "on"},
		{name: "automatic https rewrites off", settingID: "automatic_https_rewrites", value: "off", want: "off"},
		{name: "brotli on", settingID: "brotli", value: "on", want: "on"},
		{name: "rocket loader off", settingID: "rocket_loader", value: "off", want: "off"},
		{name: "cache level aggressive", settingID: "cache_level", value: "aggressive", want: "aggressive"},
		{name: "cache level simplified", settingID: "cache_level", value: "simplified", want: "simplified"},
		{name: "ssl full", settingID: "ssl", value: "full", want: "full"},
		{name: "ssl origin pull", settingID: "ssl", value: "origin_pull", want: "origin_pull"},
		{name: "browser cache ttl number", settingID: "browser_cache_ttl", value: float64(7200), want: 7200},
		{name: "browser cache ttl string", settingID: "browser_cache_ttl", value: "14400", want: 14400},
		{name: "browser cache ttl zero", settingID: "browser_cache_ttl", value: 0, want: 0},
		{name: "reject non string", settingID: "development_mode", value: true, wantErr: true},
		{name: "reject bad development mode", settingID: "development_mode", value: "enabled", wantErr: true},
		{name: "reject bad security level", settingID: "security_level", value: "max", wantErr: true},
		{name: "reject bad cache level", settingID: "cache_level", value: "cache_everything", wantErr: true},
		{name: "reject bad ssl mode", settingID: "ssl", value: "partial", wantErr: true},
		{name: "reject browser cache ttl decimal", settingID: "browser_cache_ttl", value: 12.5, wantErr: true},
		{name: "reject browser cache ttl negative", settingID: "browser_cache_ttl", value: -1, wantErr: true},
		{name: "reject browser cache ttl too high", settingID: "browser_cache_ttl", value: 31536001, wantErr: true},
		{name: "reject unsupported setting", settingID: "mirage", value: "on", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeZoneSettingValue(tt.settingID, tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				var validationErr ValidationError
				if !errors.As(err, &validationErr) {
					t.Fatalf("expected ValidationError, got %T", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNormalizeKVValueTarget(t *testing.T) {
	accountID, namespaceID, key, err := normalizeKVValueTarget(" account ", " namespace ", " folder/key ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if accountID != "account" || namespaceID != "namespace" || key != "folder/key" {
		t.Fatalf("unexpected normalized values: %q %q %q", accountID, namespaceID, key)
	}

	if _, _, _, err := normalizeKVValueTarget("account", "namespace", " "); err == nil {
		t.Fatalf("expected empty key error")
	} else {
		var validationErr ValidationError
		if !errors.As(err, &validationErr) {
			t.Fatalf("expected ValidationError, got %T", err)
		}
	}
}

func TestNormalizeD1QueryRequest(t *testing.T) {
	req, err := normalizeD1QueryRequest(D1QueryRequest{
		SQL:    " SELECT * FROM users WHERE id = ? ",
		Params: []string{" 42 "},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.SQL != "SELECT * FROM users WHERE id = ?" {
		t.Fatalf("unexpected sql: %q", req.SQL)
	}
	if len(req.Params) != 1 || req.Params[0] != " 42 " {
		t.Fatalf("unexpected params: %#v", req.Params)
	}

	tests := []struct {
		name string
		req  D1QueryRequest
	}{
		{name: "empty sql", req: D1QueryRequest{SQL: " "}},
		{name: "too large sql", req: D1QueryRequest{SQL: strings.Repeat("x", maxD1SQLBytes+1)}},
		{name: "too many params", req: D1QueryRequest{SQL: "SELECT ?", Params: make([]string, maxD1Parameters+1)}},
		{name: "too large param", req: D1QueryRequest{SQL: "SELECT ?", Params: []string{strings.Repeat("x", maxD1CellValueBytes+1)}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := normalizeD1QueryRequest(tt.req); err == nil {
				t.Fatalf("expected error")
			} else {
				var validationErr ValidationError
				if !errors.As(err, &validationErr) {
					t.Fatalf("expected ValidationError, got %T", err)
				}
			}
		})
	}
}

func TestD1TableHelpers(t *testing.T) {
	if got := quoteSQLIdentifier(`bad"name`); got != `"bad""name"` {
		t.Fatalf("unexpected quoted identifier: %s", got)
	}
	if got := d1RowIDKey([]D1Column{{Name: d1RowIDDefaultKey}}); got != d1RowIDDefaultKey+"_" {
		t.Fatalf("unexpected rowid key: %s", got)
	}

	limit, offset, err := normalizeD1TablePage("25", "50")
	if err != nil {
		t.Fatalf("unexpected page error: %v", err)
	}
	if limit != 25 || offset != 50 {
		t.Fatalf("unexpected page: %d %d", limit, offset)
	}
	if _, _, err := normalizeD1TablePage("101", "0"); err == nil {
		t.Fatalf("expected limit error")
	}
}

func TestSplitStatusComponents(t *testing.T) {
	components := []StatusPageComponent{
		{ID: "services", Name: "Cloudflare Sites and Services", Status: "degraded_performance", Group: true},
		{ID: "dash", Name: "Dashboard", Status: "degraded_performance", GroupID: "services"},
		{ID: "api", Name: "API", Status: "operational", GroupID: "services"},
		{ID: "europe", Name: "Europe", Status: "operational", Group: true},
		{ID: "ams", Name: "Amsterdam", Status: "under_maintenance", GroupID: "europe"},
		{ID: "fra", Name: "Frankfurt", Status: "operational", GroupID: "europe"},
	}

	affected, total, regions := splitStatusComponents(components)
	if total != 2 {
		t.Fatalf("product total = %d, want 2", total)
	}
	if len(affected) != 1 || affected[0].ID != "dash" {
		t.Fatalf("unexpected affected products: %#v", affected)
	}
	if len(regions) != 1 || regions[0].Name != "Europe" || regions[0].Total != 2 || regions[0].Impacted != 1 {
		t.Fatalf("unexpected regions: %#v", regions)
	}
}

func TestCloudflareStatusUsesPublicStatuspageAPI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/summary.json", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("summary method = %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"page":{"id":"page","name":"Cloudflare","url":"https://www.cloudflarestatus.com","time_zone":"Etc/UTC","updated_at":"2026-06-18T11:00:00Z"},
			"status":{"indicator":"minor","description":"Minor Service Outage"},
			"components":[
				{"id":"services","name":"Cloudflare Sites and Services","status":"degraded_performance","group":true},
				{"id":"dash","name":"Dashboard","status":"degraded_performance","group_id":"services"},
				{"id":"api","name":"API","status":"operational","group_id":"services"},
				{"id":"asia","name":"Asia","status":"operational","group":true},
				{"id":"hkg","name":"Hong Kong","status":"under_maintenance","group_id":"asia"}
			],
			"incidents":[{"id":"active","name":"Active incident","status":"investigating","impact":"minor"}],
			"scheduled_maintenances":[{"id":"maint","name":"Maintenance","status":"scheduled","impact":"maintenance","scheduled_for":"2026-06-19T00:00:00Z"}]
		}`))
	})
	mux.HandleFunc("/incidents.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"incidents":[
				{"id":"active","name":"Active incident","status":"investigating","impact":"minor"},
				{"id":"old","name":"Resolved incident","status":"resolved","impact":"minor","shortlink":"https://stspg.io/example"}
			]
		}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	svc := NewService(nil)
	svc.statusEndpoint = server.URL
	resp, err := svc.CloudflareStatus(t.Context())
	if err != nil {
		t.Fatalf("CloudflareStatus: %v", err)
	}
	if resp.Overall.Indicator != "minor" {
		t.Fatalf("indicator = %q", resp.Overall.Indicator)
	}
	if len(resp.ActiveIncidents) != 1 || resp.ActiveIncidents[0].ID != "active" {
		t.Fatalf("unexpected active incidents: %#v", resp.ActiveIncidents)
	}
	if len(resp.RecentIncidents) != 1 || resp.RecentIncidents[0].ID != "old" {
		t.Fatalf("unexpected recent incidents: %#v", resp.RecentIncidents)
	}
	if resp.ProductTotal != 2 || len(resp.AffectedProducts) != 1 || resp.AffectedProducts[0].ID != "dash" {
		t.Fatalf("unexpected product summary: total=%d affected=%#v", resp.ProductTotal, resp.AffectedProducts)
	}
	if len(resp.Regions) != 1 || resp.Regions[0].Name != "Asia" || resp.Regions[0].Impacted != 1 {
		t.Fatalf("unexpected regions: %#v", resp.Regions)
	}
	if resp.FetchedAt.IsZero() {
		t.Fatalf("expected fetched_at")
	}
}

func TestCreateTunnelCreatesRemoteManagedTunnelAndFetchesToken(t *testing.T) {
	ctx := context.Background()
	createSeen := false
	tokenSeen := false
	mux := http.NewServeMux()
	mux.HandleFunc("/client/v4/accounts/account-1/cfd_tunnel", func(w http.ResponseWriter, r *http.Request) {
		assertBearer(t, r)
		if r.Method != http.MethodPost {
			t.Fatalf("create tunnel method = %s", r.Method)
		}
		var req cloudflare.TunnelCreateParams
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode create tunnel request: %v", err)
		}
		if req.Name != "edge-prod" || req.ConfigSrc != "cloudflare" {
			t.Fatalf("unexpected create tunnel request: %#v", req)
		}
		secret, err := base64.StdEncoding.DecodeString(req.Secret)
		if err != nil {
			t.Fatalf("tunnel secret is not base64: %v", err)
		}
		if len(secret) != 32 {
			t.Fatalf("tunnel secret len = %d, want 32", len(secret))
		}
		createSeen = true
		writeCFEnvelope(w, `{"id":"tunnel-1","name":"edge-prod","status":"inactive","tun_type":"cfd_tunnel","remote_config":true}`, nil)
	})
	mux.HandleFunc("/client/v4/accounts/account-1/cfd_tunnel/tunnel-1/token", func(w http.ResponseWriter, r *http.Request) {
		assertBearer(t, r)
		if r.Method != http.MethodGet {
			t.Fatalf("get tunnel token method = %s", r.Method)
		}
		tokenSeen = true
		writeCFEnvelope(w, `"connector-token"`, nil)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	svc := NewServiceWithEndpoints(testOAuthServiceWithScopes(t, "cloudflare-tunnel.read cloudflare-tunnel.write"), EndpointOverrides{
		REST: server.URL + "/client/v4",
	})
	resp, err := svc.CreateTunnel(ctx, "account-1", TunnelCreateRequest{Name: " edge-prod "})
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	if !createSeen || !tokenSeen {
		t.Fatalf("expected create and token requests, create=%v token=%v", createSeen, tokenSeen)
	}
	if resp.Tunnel.ID != "tunnel-1" || resp.Tunnel.Name != "edge-prod" || !resp.Tunnel.RemoteConfig {
		t.Fatalf("unexpected tunnel response: %#v", resp.Tunnel)
	}
	if resp.Token != "connector-token" {
		t.Fatalf("unexpected connector token: %q", resp.Token)
	}
	if !resp.Capabilities["tunnels"].Write {
		t.Fatalf("expected tunnel write capability: %#v", resp.Capabilities["tunnels"])
	}
}

func TestCreateTunnelRequiresWriteScope(t *testing.T) {
	svc := NewService(testOAuthServiceWithScopes(t, "cloudflare-tunnel.read"))
	_, err := svc.CreateTunnel(context.Background(), "account-1", TunnelCreateRequest{Name: "edge"})
	if err == nil {
		t.Fatal("expected error")
	}
	var validationErr ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected ValidationError, got %T", err)
	}
}

func TestOverviewAggregatesCloudflareAccountResources(t *testing.T) {
	ctx := context.Background()
	mux := http.NewServeMux()
	mux.HandleFunc("/client/v4/accounts", func(w http.ResponseWriter, r *http.Request) {
		assertBearer(t, r)
		writeCFEnvelope(w, `[{"id":"account-1","name":"Example Account","type":"standard"}]`, nil)
	})
	mux.HandleFunc("/client/v4/zones", func(w http.ResponseWriter, r *http.Request) {
		assertBearer(t, r)
		if got := r.URL.Query().Get("account.id"); got != "account-1" {
			t.Fatalf("account.id = %q, want account-1", got)
		}
		writeCFEnvelope(w, `[{"id":"zone-1","name":"example.com","status":"active","account_id":"account-1"}]`, nil)
	})
	mux.HandleFunc("/client/v4/zones/zone-1/dns_records", func(w http.ResponseWriter, r *http.Request) {
		assertBearer(t, r)
		writeCFEnvelope(w, `[{"id":"record-1"}]`, map[string]int{"count": 1, "total_count": 7})
	})
	mux.HandleFunc("/client/v4/accounts/account-1/cfd_tunnel", func(w http.ResponseWriter, r *http.Request) {
		assertBearer(t, r)
		writeCFEnvelope(w, `[{"id":"tunnel-1"},{"id":"tunnel-2"}]`, nil)
	})
	mux.HandleFunc("/client/v4/accounts/account-1/workers/scripts", func(w http.ResponseWriter, r *http.Request) {
		assertBearer(t, r)
		writeCFEnvelope(w, `[{"id":"worker-1"}]`, nil)
	})
	mux.HandleFunc("/client/v4/accounts/account-1/r2/buckets", func(w http.ResponseWriter, r *http.Request) {
		assertBearer(t, r)
		writeCFEnvelope(w, `{"buckets":[{"name":"assets"},{"name":"logs"}]}`, nil)
	})
	mux.HandleFunc("/client/v4/accounts/account-1/d1/database", func(w http.ResponseWriter, r *http.Request) {
		assertBearer(t, r)
		writeCFEnvelope(w, `[{"uuid":"db-1"},{"uuid":"db-2"},{"uuid":"db-3"}]`, nil)
	})
	mux.HandleFunc("/client/v4/accounts/account-1/storage/kv/namespaces", func(w http.ResponseWriter, r *http.Request) {
		assertBearer(t, r)
		writeCFEnvelope(w, `[{"id":"namespace-1"}]`, map[string]int{"count": 1, "total_count": 4})
	})
	mux.HandleFunc("/client/v4/zones/zone-1/snippets", func(w http.ResponseWriter, r *http.Request) {
		assertBearer(t, r)
		writeCFEnvelope(w, `[{"snippet_name":"snippet_1"}]`, nil)
	})
	mux.HandleFunc("/client/v4/zones/zone-1/rulesets/phases/http_request_firewall_custom/entrypoint", func(w http.ResponseWriter, r *http.Request) {
		assertBearer(t, r)
		writeCFEnvelope(w, `{"id":"ruleset-1","rules":[{"id":"rule-1"},{"id":"rule-2"}]}`, nil)
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
	server := httptest.NewServer(mux)
	defer server.Close()

	svc := NewService(testOAuthServiceWithScopes(t, strings.Join([]string{
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
	}, " ")))
	svc.restEndpoint = server.URL + "/client/v4"
	svc.statusEndpoint = server.URL + "/status"

	resp, err := svc.Overview(ctx, "account-1")
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if resp.Account == nil || resp.Account.ID != "account-1" {
		t.Fatalf("unexpected overview account: %#v", resp.Account)
	}
	if resp.Zone == nil || resp.Zone.ID != "zone-1" {
		t.Fatalf("unexpected overview zone: %#v", resp.Zone)
	}
	if resp.Status == nil || resp.Status.Indicator != "none" {
		t.Fatalf("unexpected status summary: %#v", resp.Status)
	}
	metrics := overviewMetricMap(resp.Metrics)
	want := map[string]int{
		"accounts":      1,
		"zones":         1,
		"active_zones":  1,
		"dns_records":   7,
		"tunnels":       2,
		"workers":       1,
		"r2_buckets":    2,
		"d1_databases":  3,
		"kv_namespaces": 4,
		"snippets":      1,
		"waf_rules":     2,
	}
	for id, value := range want {
		metric, ok := metrics[id]
		if !ok {
			t.Fatalf("missing metric %q in %#v", id, resp.Metrics)
		}
		if !metric.Available || metric.Value != value {
			t.Fatalf("metric %s = %#v, want value %d available", id, metric, value)
		}
	}
	if resp.Session.ID != "session-1" {
		t.Fatalf("unexpected session summary: %#v", resp.Session)
	}
}

func TestAccountsUsesEndpointOverrideAndPaginates(t *testing.T) {
	ctx := context.Background()
	requests := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/client/v4/accounts", func(w http.ResponseWriter, r *http.Request) {
		assertBearer(t, r)
		requests++
		switch page := r.URL.Query().Get("page"); page {
		case "1":
			writeCFEnvelope(w, `[{"id":"account-1","name":"First Account","type":"standard"}]`, map[string]int{"count": 1, "total_count": 2, "total_pages": 2})
		case "2":
			writeCFEnvelope(w, `[{"id":"account-2","name":"Second Account","type":"standard"}]`, map[string]int{"count": 1, "total_count": 2, "total_pages": 2})
		default:
			t.Fatalf("unexpected accounts page %q", page)
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	svc := NewServiceWithEndpoints(testOAuthServiceWithScopes(t, "account-settings.read"), EndpointOverrides{
		REST: server.URL + "/client/v4",
	})
	resp, err := svc.Accounts(ctx)
	if err != nil {
		t.Fatalf("Accounts: %v", err)
	}
	if requests != 2 {
		t.Fatalf("accounts requests = %d, want 2", requests)
	}
	if len(resp.Data) != 2 || resp.Data[0].ID != "account-1" || resp.Data[1].ID != "account-2" {
		t.Fatalf("unexpected accounts response: %#v", resp.Data)
	}
	if resp.Session.ID != "session-1" {
		t.Fatalf("unexpected session summary: %#v", resp.Session)
	}
}

func TestOverviewMarksMissingScopesUnavailable(t *testing.T) {
	ctx := context.Background()
	mux := http.NewServeMux()
	mux.HandleFunc("/client/v4/zones", func(w http.ResponseWriter, r *http.Request) {
		assertBearer(t, r)
		writeCFEnvelope(w, `[{"id":"zone-1","name":"example.com","status":"active","account_id":"account-1"}]`, nil)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	svc := NewService(testOAuthServiceWithScopes(t, "zone.read"))
	svc.restEndpoint = server.URL + "/client/v4"
	svc.statusEndpoint = server.URL + "/status-missing"

	resp, err := svc.Overview(ctx, "")
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	metrics := overviewMetricMap(resp.Metrics)
	if got := metrics["zones"]; !got.Available || got.Value != 1 {
		t.Fatalf("zones metric = %#v, want available count 1", got)
	}
	for _, id := range []string{"accounts", "dns_records", "workers", "r2_buckets", "waf_rules"} {
		metric, ok := metrics[id]
		if !ok {
			t.Fatalf("missing metric %s", id)
		}
		if metric.Available || metric.Error != "missing_scope" {
			t.Fatalf("metric %s = %#v, want missing_scope unavailable", id, metric)
		}
	}
}

func TestD1UpdateRowStatementQuotesIdentifiersAndParameterizesValues(t *testing.T) {
	sql, params, err := d1UpdateRowStatement(`user"table`, []D1Column{
		{Name: `name"col`},
		{Name: "note"},
	}, D1RowMutationRequest{
		RowID: "7",
		Changes: map[string]string{
			`name"col`: `Robert'); DROP TABLE users; --`,
			"note":     "safe",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantSQL := `UPDATE "user""table" SET "name""col" = ?, "note" = ? WHERE rowid = ?`
	if sql != wantSQL {
		t.Fatalf("sql = %q, want %q", sql, wantSQL)
	}
	wantParams := []string{`Robert'); DROP TABLE users; --`, "safe", "7"}
	if strings.Join(params, "\x00") != strings.Join(wantParams, "\x00") {
		t.Fatalf("params = %#v, want %#v", params, wantParams)
	}

	if _, _, err := d1UpdateRowStatement("users", []D1Column{{Name: "name"}}, D1RowMutationRequest{
		RowID:   "1",
		Changes: map[string]string{"unknown": "value"},
	}); err == nil {
		t.Fatalf("expected unknown column error")
	}
}

func TestNormalizeSnippetRequest(t *testing.T) {
	req, err := normalizeSnippetRequest(SnippetRequest{
		Name: " snippet_1 ",
		Code: " export default {\n  async fetch(request) { return fetch(request); }\n};\n",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Name != "snippet_1" || req.MainFile != "snippet.js" {
		t.Fatalf("unexpected normalized request: %#v", req)
	}
	if !strings.HasPrefix(req.Code, " ") || !strings.HasSuffix(req.Code, "\n") {
		t.Fatalf("snippet code should preserve user whitespace: %q", req.Code)
	}

	tests := []struct {
		name string
		req  SnippetRequest
	}{
		{name: "empty name", req: SnippetRequest{Name: " ", Code: "export default {};"}},
		{name: "bad name", req: SnippetRequest{Name: "bad-name", Code: "export default {};"}},
		{name: "empty code", req: SnippetRequest{Name: "snippet_1", Code: " \n\t"}},
		{name: "too large code", req: SnippetRequest{Name: "snippet_1", Code: strings.Repeat("x", maxSnippetCodeBytes+1)}},
		{name: "bad main file", req: SnippetRequest{Name: "snippet_1", Code: "export default {};", MainFile: "../snippet.js"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := normalizeSnippetRequest(tt.req); err == nil {
				t.Fatalf("expected error")
			}
		})
	}
}

func TestMapSnippetContent(t *testing.T) {
	content := mapSnippetContent(
		"snippet_1",
		"text/javascript",
		`attachment; filename="edge.js"`,
		[]byte("export default {};"),
	)
	if content.Name != "snippet_1" || content.MainFile != "edge.js" || content.Value != "export default {};" || content.Encoding != "utf-8" {
		t.Fatalf("unexpected raw content: %#v", content)
	}

	content = mapSnippetContent("snippet_1", "application/octet-stream", "", []byte{0xff, 0xfe, 0xfd})
	if content.Encoding != "binary" || content.Value != "" || content.Bytes != 3 {
		t.Fatalf("expected binary content, got %#v", content)
	}
}

func TestMapMultipartSnippetContent(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("files", "main.js")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := file.Write([]byte("export default { fetch() {} };")); err != nil {
		t.Fatalf("write file: %v", err)
	}
	field, err := writer.CreateFormField("metadata")
	if err != nil {
		t.Fatalf("CreateFormField: %v", err)
	}
	if _, err := field.Write([]byte(`{"main_module":"main.js"}`)); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	content := mapSnippetContent("snippet_1", writer.FormDataContentType(), "", body.Bytes())
	if content.Name != "snippet_1" || content.MainFile != "main.js" || content.Value != "export default { fetch() {} };" {
		t.Fatalf("unexpected multipart content: %#v", content)
	}
}

func TestNormalizeSnippetRuleRequest(t *testing.T) {
	rule, err := normalizeSnippetRuleRequest(SnippetRuleRequest{
		SnippetName: "snippet_1",
		Expression:  ` http.request.uri.path contains "/admin" `,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rule.SnippetName != "snippet_1" || rule.Expression != `http.request.uri.path contains "/admin"` {
		t.Fatalf("unexpected rule: %#v", rule)
	}
	if rule.Enabled == nil || *rule.Enabled != true {
		t.Fatalf("expected enabled default true")
	}

	disabled := false
	rule, err = normalizeSnippetRuleRequest(SnippetRuleRequest{
		SnippetName: "snippet_1",
		Expression:  "true",
		Enabled:     &disabled,
	})
	if err != nil {
		t.Fatalf("unexpected disabled rule error: %v", err)
	}
	if rule.Enabled == nil || *rule.Enabled != false {
		t.Fatalf("expected explicit disabled false")
	}

	if _, err := normalizeSnippetRuleRequest(SnippetRuleRequest{SnippetName: "snippet_1", Expression: " "}); err == nil {
		t.Fatalf("expected empty expression error")
	}
}

func TestNormalizeWorkerScriptName(t *testing.T) {
	got, err := normalizeWorkerScriptName(" worker-one ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "worker-one" {
		t.Fatalf("got %q, want worker-one", got)
	}

	tests := []string{"", " ", "bad/name", `bad\name`, strings.Repeat("x", maxWorkerScriptNameLen+1)}
	for _, value := range tests {
		t.Run(value, func(t *testing.T) {
			if _, err := normalizeWorkerScriptName(value); err == nil {
				t.Fatalf("expected error")
			} else {
				var validationErr ValidationError
				if !errors.As(err, &validationErr) {
					t.Fatalf("expected ValidationError, got %T", err)
				}
			}
		})
	}
}

func TestMapWorkerContent(t *testing.T) {
	content := mapWorkerContent("export default {};")
	if content.Encoding != "utf-8" || content.Truncated || content.Bytes != len("export default {};") || content.Value != "export default {};" {
		t.Fatalf("unexpected content: %#v", content)
	}

	large := strings.Repeat("界", maxWorkerScriptContentBytes)
	content = mapWorkerContent(large)
	if content.Encoding != "utf-8" || !content.Truncated || content.Bytes != len(large) {
		t.Fatalf("unexpected truncated metadata: %#v", content)
	}
	if !utf8.ValidString(content.Value) || len(content.Value) > maxWorkerScriptContentBytes {
		t.Fatalf("content should be valid UTF-8 within byte limit")
	}

	content = mapWorkerContent(string([]byte{0xff, 0xfe, 0xfd}))
	if content.Encoding != "binary" || content.Value != "" || content.Bytes != 3 {
		t.Fatalf("unexpected binary content: %#v", content)
	}
}

func TestWorkerMetricsMapping(t *testing.T) {
	p50 := 1200.0
	p99 := 9800.0
	data := workerMetricsGraphQLData{}
	data.Viewer.Accounts = append(data.Viewer.Accounts, struct {
		Summary  []workerMetricsGroup `json:"summary"`
		ByStatus []workerStatusGroup  `json:"byStatus"`
	}{
		Summary: []workerMetricsGroup{{
			Sum:       &workerMetricsSum{Requests: 10, Errors: 2, Subrequests: 4},
			Quantiles: &workerMetricsQuantiles{CPUTimeP50: &p50, CPUTimeP99: &p99},
		}},
		ByStatus: []workerStatusGroup{
			workerStatusGroupWithRequests("success", 8),
			workerStatusGroupWithRequests("scriptThrewException", 2),
		},
	})
	summary, statuses := mapWorkerMetricsSummary(data)
	if summary.Requests != 10 || summary.Errors != 2 || summary.Subrequests != 4 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	if summary.CPUTimeP50Us == nil || *summary.CPUTimeP50Us != p50 || summary.CPUTimeP99Us == nil || *summary.CPUTimeP99Us != p99 {
		t.Fatalf("unexpected quantiles: %#v", summary)
	}
	if len(statuses) != 2 || statuses[0].Status != "success" || statuses[0].Requests != 8 {
		t.Fatalf("unexpected statuses: %#v", statuses)
	}

	cpu := 12345.0
	cpuData := workerCPUTimeGraphQLData{}
	cpuData.Viewer.Accounts = append(cpuData.Viewer.Accounts, struct {
		Summary []workerMetricsGroup `json:"summary"`
	}{
		Summary: []workerMetricsGroup{{Sum: &workerMetricsSum{CPUTimeUs: &cpu}}},
	})
	if got := mapWorkerCPUTotal(cpuData); got == nil || *got != cpu {
		t.Fatalf("unexpected cpu total: %v", got)
	}

	seriesData := workerSeriesGraphQLData{}
	seriesData.Viewer.Accounts = append(seriesData.Viewer.Accounts, struct {
		Series []workerSeriesGroup `json:"series"`
	}{
		Series: []workerSeriesGroup{workerSeriesGroupWithRequests("2026-06-18T01:00:00Z", "", 7, 1)},
	})
	points := mapWorkerSeries(seriesData, false)
	if len(points) != 1 || points[0].Requests != 7 || points[0].Errors != 1 {
		t.Fatalf("unexpected series: %#v", points)
	}
}

func TestAccountUsageMapping(t *testing.T) {
	p50 := 1000.0
	p99 := 9000.0
	data := accountUsageGraphQLData{}
	data.Viewer.Accounts = append(data.Viewer.Accounts, struct {
		Period    []workerMetricsGroup `json:"period"`
		Today     []workerMetricsGroup `json:"today"`
		R2Ops     []r2OpsGroup         `json:"r2Ops"`
		R2Storage []r2StorageGroup     `json:"r2Storage"`
	}{
		Period: []workerMetricsGroup{{
			Sum:       &workerMetricsSum{Requests: 100, Errors: 3, Subrequests: 17},
			Quantiles: &workerMetricsQuantiles{CPUTimeP50: &p50, CPUTimeP99: &p99},
		}},
		Today: []workerMetricsGroup{{Sum: &workerMetricsSum{Requests: 11}}},
		R2Ops: []r2OpsGroup{
			r2OpsGroupWithRequests("PutObject", 7),
			r2OpsGroupWithRequests("GetObject", 13),
			r2OpsGroupWithRequests("DeleteObject", 99),
		},
		R2Storage: []r2StorageGroup{r2StorageGroupWithMax(100, 25, 4)},
	})
	workers, r2 := mapAccountUsage(data)
	if workers.RequestsPeriod != 100 || workers.RequestsToday != 11 || workers.ErrorsPeriod != 3 || workers.Subrequests != 17 {
		t.Fatalf("unexpected workers usage: %#v", workers)
	}
	if workers.CPUTimeP50Us == nil || *workers.CPUTimeP50Us != p50 || workers.CPUTimeP99Us == nil || *workers.CPUTimeP99Us != p99 {
		t.Fatalf("unexpected cpu quantiles: %#v", workers)
	}
	if r2.ClassAOperations != 7 || r2.ClassBOperations != 13 || r2.StorageBytes != 125 || r2.ObjectCount != 4 {
		t.Fatalf("unexpected r2 usage: %#v", r2)
	}

	cpu := 1234.0
	cpuData := accountUsageCPUGraphQLData{}
	cpuData.Viewer.Accounts = append(cpuData.Viewer.Accounts, struct {
		Period []workerMetricsGroup `json:"period"`
		Today  []workerMetricsGroup `json:"today"`
	}{
		Period: []workerMetricsGroup{{Sum: &workerMetricsSum{CPUTimeUs: &cpu}}},
		Today:  []workerMetricsGroup{{Sum: &workerMetricsSum{CPUTimeUs: &cpu}}},
	})
	periodCPU, todayCPU := mapAccountUsageCPU(cpuData)
	if periodCPU == nil || *periodCPU != cpu || todayCPU == nil || *todayCPU != cpu {
		t.Fatalf("unexpected cpu totals: %v %v", periodCPU, todayCPU)
	}
	errorsData := accountWorkersErrorsGraphQLData{}
	errorsData.Viewer.Accounts = append(errorsData.Viewer.Accounts, struct {
		Window []workerMetricsGroup `json:"window"`
	}{
		Window: []workerMetricsGroup{{Sum: &workerMetricsSum{Errors: 4}}, {Sum: &workerMetricsSum{Errors: 2}}},
	})
	errorsLastHour := mapAccountWorkersErrorsLastHour(errorsData)
	if errorsLastHour == nil || *errorsLastHour != 6 {
		t.Fatalf("unexpected last-hour errors: %v", errorsLastHour)
	}

	d1Data := d1UsageGraphQLData{}
	d1Data.Viewer.Accounts = append(d1Data.Viewer.Accounts, struct {
		Period []d1UsageGroup `json:"period"`
		Today  []d1UsageGroup `json:"today"`
	}{
		Period: []d1UsageGroup{d1UsageGroupWithSum(10, 2, 5, 1)},
		Today:  []d1UsageGroup{d1UsageGroupWithSum(3, 1, 0, 0)},
	})
	d1 := mapD1Usage(d1Data)
	if d1.RowsReadPeriod != 10 || d1.RowsWrittenPeriod != 2 || d1.RowsReadToday != 3 || d1.RowsWrittenToday != 1 || d1.ReadQueriesPeriod != 5 || d1.WriteQueriesPeriod != 1 {
		t.Fatalf("unexpected d1 usage: %#v", d1)
	}

	kvData := kvUsageGraphQLData{}
	kvData.Viewer.Accounts = append(kvData.Viewer.Accounts, struct {
		Period []kvOpsGroup `json:"period"`
		Today  []kvOpsGroup `json:"today"`
	}{
		Period: []kvOpsGroup{kvOpsGroupWithRequests("read", 8), kvOpsGroupWithRequests("write", 2)},
		Today:  []kvOpsGroup{kvOpsGroupWithRequests("read", 3), kvOpsGroupWithRequests("write", 1)},
	})
	kv := mapKVUsage(kvData)
	if kv.ReadsPeriod != 8 || kv.WritesPeriod != 2 || kv.ReadsToday != 3 || kv.WritesToday != 1 {
		t.Fatalf("unexpected kv usage: %#v", kv)
	}
}

func TestDeriveAccountBillingInfo(t *testing.T) {
	billing := deriveAccountBillingInfo([]AccountSubscription{
		{
			ID:                 "workers-sub",
			State:              "Paid",
			Frequency:          "monthly",
			CurrentPeriodStart: "2026-06-01T00:00:00Z",
			CurrentPeriodEnd:   "2026-07-01T00:00:00Z",
			RatePlan:           AccountSubscriptionRatePlan{ID: "workers-paid", PublicName: "Workers Paid"},
		},
		{
			ID:       "r2-sub",
			State:    "Provisioned",
			RatePlan: AccountSubscriptionRatePlan{ID: "r2-basic", PublicName: "R2 Basic"},
		},
		{
			ID:       "old-r2",
			State:    "Cancelled",
			RatePlan: AccountSubscriptionRatePlan{ID: "r2-enterprise", PublicName: "R2 Enterprise"},
		},
	})

	if !billing.Available || !billing.WorkersPaid || !billing.R2Paid {
		t.Fatalf("unexpected billing flags: %#v", billing)
	}
	if billing.PeriodStart == nil || billing.PeriodStart.Format(time.RFC3339) != "2026-06-01T00:00:00Z" {
		t.Fatalf("unexpected period start: %#v", billing.PeriodStart)
	}
	if len(billing.Subscriptions) != 3 {
		t.Fatalf("expected subscription summaries, got %#v", billing.Subscriptions)
	}
	if billing.Subscriptions[2].Active {
		t.Fatalf("cancelled subscription should not be active: %#v", billing.Subscriptions[2])
	}
}

func TestAccountUsageIncludesBillingInfoAndUsesBillingPeriod(t *testing.T) {
	ctx := context.Background()
	periodStart := time.Now().UTC().AddDate(0, 0, -10).Truncate(time.Second)
	periodEnd := time.Now().UTC().AddDate(0, 0, 20).Truncate(time.Second)
	var seenPeriodStart string

	mux := http.NewServeMux()
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access-token" {
			t.Fatalf("unexpected authorization header: %q", r.Header.Get("Authorization"))
		}
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode graphql request: %v", err)
		}
		if strings.Contains(req.Query, "r2OperationsAdaptiveGroups") {
			seenPeriodStart, _ = req.Variables["periodStart"].(string)
		}
		writeGraphQLUsageFixture(t, w, req.Query)
	})
	mux.HandleFunc("/client/v4/accounts/account-1/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access-token" {
			t.Fatalf("unexpected authorization header: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(`{
			"success": true,
			"result": [{
				"id": "workers-sub",
				"state": "Paid",
				"frequency": "monthly",
				"current_period_start": %q,
				"current_period_end": %q,
				"rate_plan": {"id": "workers-paid", "public_name": "Workers Paid"}
			}]
		}`, periodStart.Format(time.RFC3339), periodEnd.Format(time.RFC3339))))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	svc := NewService(testOAuthService(t))
	svc.graphQLEndpoint = server.URL + "/graphql"
	svc.restEndpoint = server.URL + "/client/v4"

	resp, err := svc.AccountUsage(ctx, "account-1")
	if err != nil {
		t.Fatalf("AccountUsage: %v", err)
	}
	if !resp.Billing.Available || !resp.Billing.WorkersPaid || resp.Billing.PeriodStart == nil {
		t.Fatalf("unexpected billing info: %#v", resp.Billing)
	}
	if got := resp.PeriodStart.Format(time.RFC3339); got != periodStart.Format(time.RFC3339) {
		t.Fatalf("period start = %s, want %s", got, periodStart.Format(time.RFC3339))
	}
	if seenPeriodStart != periodStart.Format(time.RFC3339) {
		t.Fatalf("graphql periodStart = %q, want %q", seenPeriodStart, periodStart.Format(time.RFC3339))
	}
	if resp.Workers.ErrorsLastHour == nil || *resp.Workers.ErrorsLastHour != 5 {
		t.Fatalf("unexpected last-hour worker errors: %#v", resp.Workers.ErrorsLastHour)
	}
}

func TestAccountUsageDoesNotFailWhenBillingPermissionDenied(t *testing.T) {
	ctx := context.Background()
	mux := http.NewServeMux()
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		writeGraphQLUsageFixture(t, w, mustDecodeGraphQLQuery(t, r))
	})
	mux.HandleFunc("/client/v4/accounts/account-1/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"permission denied"}]}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	svc := NewService(testOAuthService(t))
	svc.graphQLEndpoint = server.URL + "/graphql"
	svc.restEndpoint = server.URL + "/client/v4"

	resp, err := svc.AccountUsage(ctx, "account-1")
	if err != nil {
		t.Fatalf("AccountUsage should not fail on billing 403: %v", err)
	}
	if resp.Billing.Available || resp.Billing.Reason != "permission_denied" {
		t.Fatalf("unexpected billing fallback: %#v", resp.Billing)
	}
	if resp.Workers.RequestsPeriod != 100 {
		t.Fatalf("usage data was not loaded: %#v", resp.Workers)
	}
}

func TestAccountUsageOverridesR2StorageWithRESTMetrics(t *testing.T) {
	ctx := context.Background()
	mux := http.NewServeMux()
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		writeGraphQLUsageFixture(t, w, mustDecodeGraphQLQuery(t, r))
	})
	mux.HandleFunc("/client/v4/accounts/account-1/r2/metrics", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access-token" {
			t.Fatalf("unexpected authorization header: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"success": true,
			"result": {
				"standard": {
					"published": {"objects": 5, "payloadSize": 1000, "metadataSize": 100},
					"uploaded": {"objects": 6, "payloadSize": 200, "metadataSize": 20}
				},
				"infrequentAccess": {
					"published": {"objects": 99, "payloadSize": 9000, "metadataSize": 900}
				}
			}
		}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	svc := NewService(testOAuthService(t))
	svc.graphQLEndpoint = server.URL + "/graphql"
	svc.restEndpoint = server.URL + "/client/v4"

	resp, err := svc.AccountUsage(ctx, "account-1")
	if err != nil {
		t.Fatalf("AccountUsage: %v", err)
	}
	if resp.R2.StorageBytes != 1320 || resp.R2.ObjectCount != 11 {
		t.Fatalf("expected R2 storage to come from REST metrics, got %#v", resp.R2)
	}
	if resp.R2.ClassAOperations != 7 {
		t.Fatalf("R2 operations should still come from GraphQL, got %#v", resp.R2)
	}
}

func TestR2MetricsLoadsAccountMetrics(t *testing.T) {
	ctx := context.Background()
	mux := http.NewServeMux()
	mux.HandleFunc("/client/v4/accounts/account-1/r2/metrics", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer access-token" {
			t.Fatalf("unexpected authorization header: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"success": true,
			"result": {
				"standard": {
					"published": {"objects": 2, "payloadSize": 100, "metadataSize": 11},
					"uploaded": {"objects": 1, "payloadSize": 7, "metadataSize": 3}
				},
				"infrequentAccess": {
					"published": {"objects": 4, "payloadSize": 20, "metadataSize": 1},
					"unpublished": {"objectCount": 5, "payload_size": 30, "metadata_size": 2}
				}
			}
		}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	svc := NewService(testOAuthService(t))
	svc.restEndpoint = server.URL + "/client/v4"

	resp, err := svc.R2Metrics(ctx, "account-1")
	if err != nil {
		t.Fatalf("R2Metrics: %v", err)
	}
	if resp.Standard == nil || resp.Standard.Published == nil || resp.Standard.Uploaded == nil {
		t.Fatalf("missing standard metrics: %#v", resp.Standard)
	}
	if resp.Standard.Published.TotalBytes != 111 || resp.Standard.Published.Objects != 2 {
		t.Fatalf("unexpected standard published metrics: %#v", resp.Standard.Published)
	}
	if resp.Standard.Uploaded.TotalBytes != 10 || resp.Standard.Uploaded.Objects != 1 {
		t.Fatalf("unexpected standard uploaded metrics: %#v", resp.Standard.Uploaded)
	}
	if resp.InfrequentAccess == nil || resp.InfrequentAccess.Uploaded == nil {
		t.Fatalf("missing infrequent access uploaded metrics: %#v", resp.InfrequentAccess)
	}
	if resp.InfrequentAccess.Uploaded.TotalBytes != 32 || resp.InfrequentAccess.Uploaded.Objects != 5 {
		t.Fatalf("unexpected infrequent access uploaded metrics: %#v", resp.InfrequentAccess.Uploaded)
	}
	if resp.Session.ID != "session-1" {
		t.Fatalf("unexpected session summary: %#v", resp.Session)
	}
}

func TestR2MetricsRequiresAccountID(t *testing.T) {
	svc := NewService(testOAuthService(t))
	_, err := svc.R2Metrics(context.Background(), " ")
	var validationErr ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected validation error, got %T %v", err, err)
	}
}

func TestR2ObjectValueIncludesBinaryPreview(t *testing.T) {
	ctx := context.Background()
	payload := []byte{0x00, 0x01, 0x02, 'A', '~', '\n', 0xff, ' '}
	mux := http.NewServeMux()
	mux.HandleFunc("/client/v4/accounts/account-1/r2/buckets/bucket-one/objects/folder%2Ffile.bin", func(w http.ResponseWriter, r *http.Request) {
		assertBearer(t, r)
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write(payload)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	svc := NewServiceWithEndpoints(testOAuthServiceWithScopes(t, "workers-r2.read"), EndpointOverrides{
		REST: server.URL + "/client/v4",
	})
	resp, err := svc.R2ObjectValue(ctx, "account-1", "bucket-one", "folder/file.bin")
	if err != nil {
		t.Fatalf("R2ObjectValue: %v", err)
	}
	if resp.Encoding != "binary" || resp.Value != "" || resp.Bytes != len(payload) || resp.ContentType != "application/octet-stream" {
		t.Fatalf("unexpected binary object response: %#v", resp)
	}
	if resp.BinaryPreview == nil {
		t.Fatal("expected binary preview")
	}
	if resp.BinaryPreview.Bytes != len(payload) || resp.BinaryPreview.Truncated {
		t.Fatalf("unexpected binary preview metadata: %#v", resp.BinaryPreview)
	}
	if !strings.Contains(resp.BinaryPreview.Hexdump, "00 01 02 41 7e 0a ff 20") || !strings.Contains(resp.BinaryPreview.Hexdump, "|...A~.. |") {
		t.Fatalf("unexpected hexdump: %q", resp.BinaryPreview.Hexdump)
	}
}

func TestR2BinaryPreviewTruncatesLargeSamples(t *testing.T) {
	raw := bytes.Repeat([]byte{0xff}, maxR2ObjectBinaryPreviewBytes+2)
	raw[0] = 'A'
	raw[16] = 'B'

	preview := r2BinaryPreview(raw, false)
	if preview.Bytes != maxR2ObjectBinaryPreviewBytes || !preview.Truncated {
		t.Fatalf("unexpected preview metadata: %#v", preview)
	}
	if strings.Contains(preview.Hexdump, strings.Repeat("ff ", maxR2ObjectBinaryPreviewBytes+1)) {
		t.Fatalf("hexdump appears to include more than the preview sample")
	}
	if !strings.Contains(preview.Hexdump, "00000000") || !strings.Contains(preview.Hexdump, "00000010") || !strings.Contains(preview.Hexdump, "|A") {
		t.Fatalf("unexpected hexdump: %q", preview.Hexdump)
	}
}

func TestCopyR2ObjectStreamsSourceToDestination(t *testing.T) {
	ctx := context.Background()
	const sourceKey = "folder/source.txt"
	const destinationKey = "folder/destination.txt"
	const sourcePayload = "hello copied object"

	var putBody string
	var putContentType string
	var sawDelete bool
	mux := http.NewServeMux()
	mux.HandleFunc("/client/v4/accounts/account-1/r2/buckets/bucket-one/objects/", func(w http.ResponseWriter, r *http.Request) {
		assertBearer(t, r)
		rawKey := strings.TrimPrefix(r.URL.EscapedPath(), "/client/v4/accounts/account-1/r2/buckets/bucket-one/objects/")
		key, err := url.PathUnescape(rawKey)
		if err != nil {
			t.Fatalf("unescape key: %v", err)
		}
		switch r.Method {
		case http.MethodGet:
			if key != sourceKey {
				t.Fatalf("GET key = %q, want %q", key, sourceKey)
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Content-Length", strconv.Itoa(len(sourcePayload)))
			_, _ = w.Write([]byte(sourcePayload))
		case http.MethodPut:
			if key != destinationKey {
				t.Fatalf("PUT key = %q, want %q", key, destinationKey)
			}
			putContentType = r.Header.Get("Content-Type")
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read PUT body: %v", err)
			}
			putBody = string(body)
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			if key != sourceKey {
				t.Fatalf("DELETE key = %q, want %q", key, sourceKey)
			}
			sawDelete = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	svc := NewServiceWithEndpoints(testOAuthServiceWithScopes(t, "workers-r2.read workers-r2.write"), EndpointOverrides{
		REST: server.URL + "/client/v4",
	})
	resp, err := svc.CopyR2Object(ctx, "account-1", "bucket-one", R2ObjectCopyRequest{
		SourceKey:      sourceKey,
		DestinationKey: destinationKey,
		DeleteSource:   true,
	})
	if err != nil {
		t.Fatalf("CopyR2Object: %v", err)
	}
	if resp.Key != destinationKey || resp.Bytes != len(sourcePayload) || resp.ContentType != "text/plain; charset=utf-8" {
		t.Fatalf("unexpected copy response: %#v", resp)
	}
	if putBody != sourcePayload {
		t.Fatalf("PUT body = %q, want %q", putBody, sourcePayload)
	}
	if putContentType != "text/plain; charset=utf-8" {
		t.Fatalf("PUT content-type = %q", putContentType)
	}
	if !sawDelete {
		t.Fatal("expected source object to be deleted for move")
	}
}

func TestCopyR2ObjectAllowsCrossBucketDestination(t *testing.T) {
	ctx := context.Background()
	const sourceKey = "folder/source.txt"
	const destinationKey = "folder/source.txt"
	const sourcePayload = "hello other bucket"

	var putPath string
	var sawDelete bool
	mux := http.NewServeMux()
	mux.HandleFunc("/client/v4/accounts/account-1/r2/buckets/source-bucket/objects/", func(w http.ResponseWriter, r *http.Request) {
		assertBearer(t, r)
		rawKey := strings.TrimPrefix(r.URL.EscapedPath(), "/client/v4/accounts/account-1/r2/buckets/source-bucket/objects/")
		key, err := url.PathUnescape(rawKey)
		if err != nil {
			t.Fatalf("unescape source key: %v", err)
		}
		switch r.Method {
		case http.MethodGet:
			if key != sourceKey {
				t.Fatalf("GET key = %q, want %q", key, sourceKey)
			}
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Content-Length", strconv.Itoa(len(sourcePayload)))
			_, _ = w.Write([]byte(sourcePayload))
		case http.MethodDelete:
			if key != sourceKey {
				t.Fatalf("DELETE key = %q, want %q", key, sourceKey)
			}
			sawDelete = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected source method: %s", r.Method)
		}
	})
	mux.HandleFunc("/client/v4/accounts/account-1/r2/buckets/destination-bucket/objects/", func(w http.ResponseWriter, r *http.Request) {
		assertBearer(t, r)
		if r.Method != http.MethodPut {
			t.Fatalf("unexpected destination method: %s", r.Method)
		}
		rawKey := strings.TrimPrefix(r.URL.EscapedPath(), "/client/v4/accounts/account-1/r2/buckets/destination-bucket/objects/")
		key, err := url.PathUnescape(rawKey)
		if err != nil {
			t.Fatalf("unescape destination key: %v", err)
		}
		if key != destinationKey {
			t.Fatalf("PUT key = %q, want %q", key, destinationKey)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read PUT body: %v", err)
		}
		if string(body) != sourcePayload {
			t.Fatalf("PUT body = %q, want %q", string(body), sourcePayload)
		}
		putPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	svc := NewServiceWithEndpoints(testOAuthServiceWithScopes(t, "workers-r2.read workers-r2.write"), EndpointOverrides{
		REST: server.URL + "/client/v4",
	})
	resp, err := svc.CopyR2Object(ctx, "account-1", "source-bucket", R2ObjectCopyRequest{
		SourceKey:         sourceKey,
		DestinationBucket: "destination-bucket",
		DestinationKey:    destinationKey,
		DeleteSource:      true,
	})
	if err != nil {
		t.Fatalf("CopyR2Object: %v", err)
	}
	if resp.Key != destinationKey || resp.Bytes != len(sourcePayload) || resp.ContentType != "text/plain" {
		t.Fatalf("unexpected copy response: %#v", resp)
	}
	if !strings.Contains(putPath, "/destination-bucket/objects/") {
		t.Fatalf("PUT did not target destination bucket: %s", putPath)
	}
	if !sawDelete {
		t.Fatal("expected source object to be deleted for cross-bucket move")
	}
}

func TestCopyR2ObjectRejectsSameKey(t *testing.T) {
	svc := NewService(testOAuthServiceWithScopes(t, "workers-r2.read workers-r2.write"))
	_, err := svc.CopyR2Object(context.Background(), "account-1", "bucket-one", R2ObjectCopyRequest{
		SourceKey:      "same.txt",
		DestinationKey: "same.txt",
	})
	var validationErr ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected validation error, got %T %v", err, err)
	}
}

func r2OpsGroupWithRequests(action string, requests int) r2OpsGroup {
	group := r2OpsGroup{}
	group.Dimensions.ActionType = action
	group.Sum = &struct {
		Requests int `json:"requests"`
	}{Requests: requests}
	return group
}

func r2StorageGroupWithMax(payload, metadata, objects int) r2StorageGroup {
	group := r2StorageGroup{}
	group.Max = &struct {
		PayloadSize  int `json:"payloadSize"`
		MetadataSize int `json:"metadataSize"`
		ObjectCount  int `json:"objectCount"`
	}{PayloadSize: payload, MetadataSize: metadata, ObjectCount: objects}
	return group
}

func d1UsageGroupWithSum(rowsRead, rowsWritten, readQueries, writeQueries int) d1UsageGroup {
	group := d1UsageGroup{}
	group.Sum = &struct {
		RowsRead     int `json:"rowsRead"`
		RowsWritten  int `json:"rowsWritten"`
		ReadQueries  int `json:"readQueries"`
		WriteQueries int `json:"writeQueries"`
	}{RowsRead: rowsRead, RowsWritten: rowsWritten, ReadQueries: readQueries, WriteQueries: writeQueries}
	return group
}

func kvOpsGroupWithRequests(action string, requests int) kvOpsGroup {
	group := kvOpsGroup{}
	group.Dimensions.ActionType = action
	group.Sum = &struct {
		Requests int `json:"requests"`
	}{Requests: requests}
	return group
}

func workerStatusGroupWithRequests(status string, requests int) workerStatusGroup {
	group := workerStatusGroup{}
	group.Dimensions.Status = status
	group.Sum = &struct {
		Requests int `json:"requests"`
	}{Requests: requests}
	return group
}

func workerSeriesGroupWithRequests(datetimeHour, date string, requests, errors int) workerSeriesGroup {
	group := workerSeriesGroup{}
	group.Dimensions.DatetimeHour = datetimeHour
	group.Dimensions.Date = date
	group.Sum = &struct {
		Requests int `json:"requests"`
		Errors   int `json:"errors"`
	}{Requests: requests, Errors: errors}
	return group
}

func TestNormalizeR2BucketRequest(t *testing.T) {
	req, err := normalizeR2BucketRequest(R2BucketRequest{
		Name:         " bucket-one ",
		LocationHint: " ENAM ",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Name != "bucket-one" || req.LocationHint != "ENAM" {
		t.Fatalf("unexpected normalized request: %#v", req)
	}

	tests := []struct {
		name string
		req  R2BucketRequest
	}{
		{name: "too short", req: R2BucketRequest{Name: "ab"}},
		{name: "too long", req: R2BucketRequest{Name: strings.Repeat("a", 64)}},
		{name: "uppercase", req: R2BucketRequest{Name: "Bucket"}},
		{name: "underscore", req: R2BucketRequest{Name: "bucket_one"}},
		{name: "leading hyphen", req: R2BucketRequest{Name: "-bucket"}},
		{name: "trailing hyphen", req: R2BucketRequest{Name: "bucket-"}},
		{name: "bad location hint", req: R2BucketRequest{Name: "bucket-one", LocationHint: "EN AM"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := normalizeR2BucketRequest(tt.req); err == nil {
				t.Fatalf("expected error")
			} else {
				var validationErr ValidationError
				if !errors.As(err, &validationErr) {
					t.Fatalf("expected ValidationError, got %T", err)
				}
			}
		})
	}
}

func TestNormalizeR2ObjectTarget(t *testing.T) {
	accountID, bucketName, key, err := normalizeR2ObjectTarget(" account ", " bucket-one ", " folder/file name.txt ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if accountID != "account" || bucketName != "bucket-one" || key != "folder/file name.txt" {
		t.Fatalf("unexpected normalized target: %q %q %q", accountID, bucketName, key)
	}
	if got := r2ObjectPath(accountID, bucketName, key); got != "/accounts/account/r2/buckets/bucket-one/objects/folder%2Ffile%20name.txt" {
		t.Fatalf("unexpected object path: %s", got)
	}

	tests := []struct {
		name       string
		accountID  string
		bucketName string
		key        string
	}{
		{name: "empty account", bucketName: "bucket-one", key: "file.txt"},
		{name: "bad bucket", accountID: "account", bucketName: "Bucket", key: "file.txt"},
		{name: "empty key", accountID: "account", bucketName: "bucket-one", key: " "},
		{name: "large key", accountID: "account", bucketName: "bucket-one", key: strings.Repeat("x", maxR2ObjectKeyBytes+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, _, err := normalizeR2ObjectTarget(tt.accountID, tt.bucketName, tt.key); err == nil {
				t.Fatalf("expected error")
			} else {
				var validationErr ValidationError
				if !errors.As(err, &validationErr) {
					t.Fatalf("expected ValidationError, got %T", err)
				}
			}
		})
	}
}

func TestNormalizeWAFRuleRequest(t *testing.T) {
	rule, err := normalizeWAFRuleRequest(WAFRuleRequest{
		Action:      " BLOCK ",
		Expression:  ` http.request.uri.path contains "/admin" `,
		Description: " Block admin ",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rule.Action != "block" || rule.Expression != `http.request.uri.path contains "/admin"` || rule.Description != "Block admin" {
		t.Fatalf("unexpected rule: %#v", rule)
	}
	if rule.Enabled == nil || *rule.Enabled != true {
		t.Fatalf("expected enabled default true")
	}

	disabled := false
	rule, err = normalizeWAFRuleRequest(WAFRuleRequest{
		Action:     "log",
		Expression: "true",
		Enabled:    &disabled,
	})
	if err != nil {
		t.Fatalf("unexpected disabled rule error: %v", err)
	}
	if rule.Enabled == nil || *rule.Enabled != false {
		t.Fatalf("expected explicit disabled false")
	}

	rule, err = normalizeWAFRuleRequest(WAFRuleRequest{
		Action:      "skip",
		Expression:  `http.request.uri.path contains "/trusted"`,
		Description: "Bypass legacy controls",
		ActionParameters: &WAFRuleActionParameters{
			Ruleset:  "current",
			Products: []string{"zoneLockdown", "uaBlock"},
			Phases:   []string{"http_ratelimit"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected skip rule error: %v", err)
	}
	if rule.Action != "skip" || rule.ActionParameters == nil {
		t.Fatalf("unexpected skip rule: %#v", rule)
	}
	if rule.ActionParameters.Ruleset != "current" {
		t.Fatalf("unexpected skip ruleset: %#v", rule.ActionParameters)
	}
	if len(rule.ActionParameters.Products) != 2 || rule.ActionParameters.Products[0] != "zoneLockdown" || rule.ActionParameters.Products[1] != "uaBlock" {
		t.Fatalf("unexpected skip products: %#v", rule.ActionParameters.Products)
	}
	if len(rule.ActionParameters.Phases) != 1 || rule.ActionParameters.Phases[0] != "http_ratelimit" {
		t.Fatalf("unexpected skip phases: %#v", rule.ActionParameters.Phases)
	}

	rule, err = normalizeWAFRuleRequest(WAFRuleRequest{
		Action:      "block",
		Expression:  `http.request.uri.path contains "/login"`,
		Description: "rate limit login",
		RateLimit:   wafJSON(`{"characteristics":["ip.src"],"requests_per_period":20,"period":60,"mitigation_timeout":300,"requests_to_origin":true}`),
	})
	if err != nil {
		t.Fatalf("normalize rate limit rule: %v", err)
	}
	if rule.RateLimit == nil || len(rule.RateLimit.Characteristics) != 1 || rule.RateLimit.Characteristics[0] != "ip.src" || rule.RateLimit.RequestsPerPeriod != 20 || rule.RateLimit.Period != 60 || !rule.RateLimit.RequestsToOrigin {
		t.Fatalf("unexpected rate limit: %#v", rule.RateLimit)
	}

	tests := []struct {
		name string
		req  WAFRuleRequest
	}{
		{name: "missing skip parameters", req: WAFRuleRequest{Action: "skip", Expression: "true"}},
		{name: "unsupported skip ruleset", req: WAFRuleRequest{Action: "skip", Expression: "true", ActionParameters: &WAFRuleActionParameters{Ruleset: "all"}}},
		{name: "unsupported managed target", req: WAFRuleRequest{Action: "skip", Expression: "true", ActionParameters: &WAFRuleActionParameters{Rulesets: []string{"managed-ruleset"}}}},
		{name: "unsupported skip product", req: WAFRuleRequest{Action: "skip", Expression: "true", ActionParameters: &WAFRuleActionParameters{Products: []string{"ratelimit"}}}},
		{name: "unsupported skip phase", req: WAFRuleRequest{Action: "skip", Expression: "true", ActionParameters: &WAFRuleActionParameters{Phases: []string{"http_request_firewall_custom"}}}},
		{name: "empty expression", req: WAFRuleRequest{Action: "block", Expression: " "}},
		{name: "too long expression", req: WAFRuleRequest{Action: "block", Expression: strings.Repeat("x", maxWAFExpressionLen+1)}},
		{name: "too long description", req: WAFRuleRequest{Action: "block", Expression: "true", Description: strings.Repeat("x", maxWAFDescriptionLen+1)}},
		{name: "invalid rate limit json", req: WAFRuleRequest{Action: "block", Expression: "true", RateLimit: wafJSON(`[]`)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := normalizeWAFRuleRequest(tt.req); err == nil {
				t.Fatalf("expected error")
			} else {
				var validationErr ValidationError
				if !errors.As(err, &validationErr) {
					t.Fatalf("expected ValidationError, got %T", err)
				}
			}
		})
	}
}

func TestNormalizeWAFManagedExceptionRequest(t *testing.T) {
	rule, err := normalizeWAFRuleRequestWithOptions(WAFRuleRequest{
		Expression:  `http.request.uri.path starts_with "/legacy"`,
		Description: "Skip noisy managed rules",
		ActionParameters: &WAFRuleActionParameters{
			Ruleset:  "current",
			Rulesets: []string{"managed-ruleset", "managed-ruleset"},
			Rules: map[string][]string{
				"managed-ruleset": {"100001", "100001", "100002"},
			},
		},
	}, true, true)
	if err != nil {
		t.Fatalf("unexpected managed exception error: %v", err)
	}
	if rule.Action != "skip" || rule.ActionParameters == nil {
		t.Fatalf("expected skip rule, got %#v", rule)
	}
	if rule.ActionParameters.Ruleset != "current" || len(rule.ActionParameters.Rulesets) != 1 || rule.ActionParameters.Rulesets[0] != "managed-ruleset" {
		t.Fatalf("unexpected managed rulesets: %#v", rule.ActionParameters)
	}
	if got := rule.ActionParameters.Rules["managed-ruleset"]; len(got) != 2 || got[0] != "100001" || got[1] != "100002" {
		t.Fatalf("unexpected managed rule ids: %#v", rule.ActionParameters.Rules)
	}

	block := "block"
	tests := []struct {
		name string
		req  WAFRuleRequest
	}{
		{name: "non skip action", req: WAFRuleRequest{Action: block, Expression: "true", ActionParameters: &WAFRuleActionParameters{Rulesets: []string{"managed-ruleset"}}}},
		{name: "products are custom phase only", req: WAFRuleRequest{Expression: "true", ActionParameters: &WAFRuleActionParameters{Products: []string{"waf"}}}},
		{name: "phases are custom phase only", req: WAFRuleRequest{Expression: "true", ActionParameters: &WAFRuleActionParameters{Phases: []string{"http_request_firewall_managed"}}}},
		{name: "empty rule ids", req: WAFRuleRequest{Expression: "true", ActionParameters: &WAFRuleActionParameters{Rules: map[string][]string{"managed-ruleset": {" "}}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := normalizeWAFRuleRequestWithOptions(tt.req, true, true); err == nil {
				t.Fatalf("expected error")
			} else {
				var validationErr ValidationError
				if !errors.As(err, &validationErr) {
					t.Fatalf("expected ValidationError, got %T", err)
				}
			}
		})
	}
}

func TestNormalizeWAFManagedOverrideRequest(t *testing.T) {
	rulesetEnabled := true
	categoryDisabled := false
	rule, err := normalizeWAFManagedOverrideRequest(WAFManagedOverrideRequest{
		ManagedRulesetID: " managed-ruleset ",
		Expression:       ` http.request.uri.path contains "/login" `,
		Description:      " Managed ruleset override ",
		Enabled:          &rulesetEnabled,
		Overrides: &WAFManagedOverrides{
			Action:           "log",
			SensitivityLevel: "medium",
			Categories: []WAFManagedCategoryOverride{{
				Category: "wordpress",
				Action:   "block",
				Enabled:  &categoryDisabled,
			}},
			Rules: []WAFManagedRuleOverride{{
				ID:               "100001",
				Action:           "managed_challenge",
				ScoreThreshold:   42,
				SensitivityLevel: "low",
			}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected managed override error: %v", err)
	}
	if rule.Action != "execute" || rule.ActionParameters == nil || rule.ActionParameters.ID != "managed-ruleset" {
		t.Fatalf("expected execute managed ruleset rule, got %#v", rule)
	}
	if rule.Expression != `http.request.uri.path contains "/login"` || rule.Description != "Managed ruleset override" {
		t.Fatalf("managed override text was not normalized: %#v", rule)
	}
	overrides := rule.ActionParameters.Overrides
	if overrides == nil || overrides.Action != "log" || overrides.SensitivityLevel != "medium" || len(overrides.Categories) != 1 || len(overrides.Rules) != 1 {
		t.Fatalf("managed overrides were not normalized: %#v", overrides)
	}
	if overrides.Categories[0].Enabled == nil || *overrides.Categories[0].Enabled {
		t.Fatalf("category enabled override not preserved: %#v", overrides.Categories[0])
	}

	tests := []struct {
		name string
		req  WAFManagedOverrideRequest
	}{
		{name: "missing managed ruleset", req: WAFManagedOverrideRequest{Expression: "true"}},
		{name: "missing expression", req: WAFManagedOverrideRequest{ManagedRulesetID: "managed-ruleset"}},
		{name: "bad action", req: WAFManagedOverrideRequest{ManagedRulesetID: "managed-ruleset", Expression: "true", Overrides: &WAFManagedOverrides{Action: "allow"}}},
		{name: "duplicate category", req: WAFManagedOverrideRequest{ManagedRulesetID: "managed-ruleset", Expression: "true", Overrides: &WAFManagedOverrides{Categories: []WAFManagedCategoryOverride{{Category: "wordpress"}, {Category: "wordpress"}}}}},
		{name: "bad score threshold", req: WAFManagedOverrideRequest{ManagedRulesetID: "managed-ruleset", Expression: "true", Overrides: &WAFManagedOverrides{Rules: []WAFManagedRuleOverride{{ID: "100001", ScoreThreshold: 101}}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := normalizeWAFManagedOverrideRequest(tt.req); err == nil {
				t.Fatalf("expected error")
			} else {
				var validationErr ValidationError
				if !errors.As(err, &validationErr) {
					t.Fatalf("expected ValidationError, got %T", err)
				}
			}
		})
	}
}

func TestApplyWAFRuleUpdate(t *testing.T) {
	enabled := true
	disabled := false
	loggingEnabled := true
	version := "12"
	existing := cloudflare.RulesetRule{
		ID:          "rule-1",
		Ref:         "cfui-ref",
		Version:     &version,
		Action:      "skip",
		Expression:  "http.request.uri.path contains \"/legacy\"",
		Description: "Legacy bypass",
		Enabled:     &enabled,
		ActionParameters: &cloudflare.RulesetRuleActionParameters{
			Ruleset:  "current",
			Products: []string{"waf"},
			Phases:   []string{"http_request_firewall_managed"},
		},
		RateLimit: &cloudflare.RulesetRuleRateLimit{
			Characteristics:    []string{"ip.src"},
			RequestsPerPeriod:  100,
			Period:             60,
			CountingExpression: "true",
		},
		Logging: &cloudflare.RulesetRuleLogging{Enabled: &loggingEnabled},
		ExposedCredentialCheck: &cloudflare.RulesetRuleExposedCredentialCheck{
			UsernameExpression: "lookup_json_string(http.request.body.raw, \"username\")",
			PasswordExpression: "lookup_json_string(http.request.body.raw, \"password\")",
		},
	}

	rule, err := applyWAFRuleUpdate(existing, WAFRuleUpdateRequest{Enabled: &disabled})
	if err != nil {
		t.Fatalf("toggle update failed: %v", err)
	}
	if rule.Enabled == nil || *rule.Enabled {
		t.Fatalf("expected enabled=false, got %#v", rule.Enabled)
	}
	if rule.Action != existing.Action || rule.ActionParameters == nil || rule.ActionParameters.Ruleset != "current" {
		t.Fatalf("toggle update should preserve action parameters: %#v", rule)
	}
	if rule.RateLimit == nil || rule.Logging == nil || rule.ExposedCredentialCheck == nil {
		t.Fatalf("toggle update dropped advanced fields: %#v", rule)
	}

	expression := ` http.request.uri.path contains "/admin" `
	description := " Admin block "
	rule, err = applyWAFRuleUpdate(existing, WAFRuleUpdateRequest{
		Expression:  &expression,
		Description: &description,
	})
	if err != nil {
		t.Fatalf("text update failed: %v", err)
	}
	if rule.Expression != `http.request.uri.path contains "/admin"` || rule.Description != "Admin block" {
		t.Fatalf("text update did not normalize fields: %#v", rule)
	}
	if rule.RateLimit == nil || rule.Logging == nil || rule.ExposedCredentialCheck == nil || rule.ActionParameters == nil {
		t.Fatalf("text update dropped existing fields: %#v", rule)
	}

	action := "log"
	rule, err = applyWAFRuleUpdate(existing, WAFRuleUpdateRequest{Action: &action})
	if err != nil {
		t.Fatalf("action update failed: %v", err)
	}
	if rule.Action != "log" || rule.ActionParameters != nil {
		t.Fatalf("switching to non-skip should clear action parameters: %#v", rule)
	}
	if rule.RateLimit == nil || rule.Logging == nil || rule.ExposedCredentialCheck == nil {
		t.Fatalf("action update dropped advanced fields: %#v", rule)
	}

	rule, err = applyWAFRuleUpdate(existing, WAFRuleUpdateRequest{
		ActionParameters: &WAFRuleActionParameters{
			Products: []string{"zoneLockdown", "zoneLockdown"},
			Phases:   []string{"http_ratelimit"},
		},
	})
	if err != nil {
		t.Fatalf("skip params update failed: %v", err)
	}
	if rule.ActionParameters == nil || len(rule.ActionParameters.Products) != 1 || rule.ActionParameters.Products[0] != "zoneLockdown" || len(rule.ActionParameters.Phases) != 1 {
		t.Fatalf("unexpected normalized skip params: %#v", rule.ActionParameters)
	}

	_, err = applyWAFRuleUpdate(existing, WAFRuleUpdateRequest{
		ActionParameters: &WAFRuleActionParameters{Products: []string{"ratelimit"}},
	})
	if err == nil {
		t.Fatal("expected unsupported skip product error")
	}
	var validationErr ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected ValidationError, got %T", err)
	}

	_, err = applyWAFRuleUpdate(existing, WAFRuleUpdateRequest{})
	if err == nil {
		t.Fatal("expected empty update error")
	}
}

func TestApplyWAFManagedExceptionUpdateRejectsCustomSkipTargets(t *testing.T) {
	enabled := true
	existing := cloudflare.RulesetRule{
		ID:         "rule-1",
		Action:     "skip",
		Expression: "true",
		Enabled:    &enabled,
		ActionParameters: &cloudflare.RulesetRuleActionParameters{
			Rulesets: []string{"managed-ruleset"},
		},
	}

	_, err := applyWAFRuleUpdateWithOptions(existing, WAFRuleUpdateRequest{
		ActionParameters: &WAFRuleActionParameters{Products: []string{"waf"}},
	}, true, true)
	if err == nil {
		t.Fatal("expected visual products update to be rejected for managed exception")
	}
	var validationErr ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected ValidationError, got %T", err)
	}

	_, err = applyWAFRuleUpdateWithOptions(existing, WAFRuleUpdateRequest{
		ActionParametersJSON: wafJSON(`{"products":["waf"]}`),
	}, true, true)
	if err == nil {
		t.Fatal("expected advanced products update to be rejected for managed exception")
	}
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected ValidationError, got %T", err)
	}
}

func TestApplyWAFManagedOverrideUpdate(t *testing.T) {
	enabled := true
	disabled := false
	existing := cloudflare.RulesetRule{
		ID:          "rule-1",
		Action:      "execute",
		Expression:  "true",
		Description: "Managed ruleset",
		Enabled:     &enabled,
		ActionParameters: &cloudflare.RulesetRuleActionParameters{
			ID: "managed-ruleset",
			Overrides: &cloudflare.RulesetRuleActionParametersOverrides{
				Action: "log",
			},
		},
	}

	managedRulesetID := "new-managed-ruleset"
	expression := `http.request.uri.path contains "/admin"`
	description := "Admin managed override"
	rule, err := applyWAFManagedOverrideUpdate(existing, WAFManagedOverrideUpdateRequest{
		ManagedRulesetID: &managedRulesetID,
		Expression:       &expression,
		Description:      &description,
		Enabled:          &disabled,
		Overrides:        wafJSON(`{"action":"block","enabled":false,"rules":[{"id":"100001","action":"log"}]}`),
	})
	if err != nil {
		t.Fatalf("managed override update failed: %v", err)
	}
	if rule.Enabled == nil || *rule.Enabled || rule.Expression != expression || rule.Description != description {
		t.Fatalf("simple managed override fields not applied: %#v", rule)
	}
	if rule.ActionParameters == nil || rule.ActionParameters.ID != "new-managed-ruleset" || rule.ActionParameters.Overrides == nil || rule.ActionParameters.Overrides.Action != "block" {
		t.Fatalf("managed override params not applied: %#v", rule.ActionParameters)
	}
	if rule.ActionParameters.Overrides.Enabled == nil || *rule.ActionParameters.Overrides.Enabled {
		t.Fatalf("ruleset enabled override not applied: %#v", rule.ActionParameters.Overrides)
	}

	rule, err = applyWAFManagedOverrideUpdate(existing, WAFManagedOverrideUpdateRequest{Overrides: wafJSON(`null`)})
	if err != nil {
		t.Fatalf("managed override clear failed: %v", err)
	}
	if rule.ActionParameters == nil || rule.ActionParameters.ID != "managed-ruleset" || rule.ActionParameters.Overrides != nil {
		t.Fatalf("managed override clear should preserve id and clear overrides: %#v", rule.ActionParameters)
	}

	if _, err := applyWAFManagedOverrideUpdate(existing, WAFManagedOverrideUpdateRequest{}); err == nil {
		t.Fatal("expected empty managed override update error")
	}
	_, err = applyWAFManagedOverrideUpdate(cloudflare.RulesetRule{Action: "skip"}, WAFManagedOverrideUpdateRequest{Enabled: &disabled})
	if err == nil {
		t.Fatal("expected non-execute managed override update error")
	}
	var validationErr ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected ValidationError, got %T", err)
	}
}

func TestApplyWAFRuleUpdateAdvancedJSONFields(t *testing.T) {
	loggingEnabled := true
	existing := cloudflare.RulesetRule{
		ID:          "rule-1",
		Action:      "block",
		Expression:  `http.request.uri.path contains "/login"`,
		Description: "Login protection",
		ActionParameters: &cloudflare.RulesetRuleActionParameters{
			Response: &cloudflare.RulesetRuleActionParametersBlockResponse{
				StatusCode:  403,
				ContentType: "text/plain",
				Content:     "blocked",
			},
		},
		RateLimit: &cloudflare.RulesetRuleRateLimit{
			Characteristics:   []string{"ip.src"},
			RequestsPerPeriod: 100,
			Period:            60,
		},
		Logging: &cloudflare.RulesetRuleLogging{Enabled: &loggingEnabled},
		ExposedCredentialCheck: &cloudflare.RulesetRuleExposedCredentialCheck{
			UsernameExpression: "lookup_json_string(http.request.body.raw, \"username\")",
			PasswordExpression: "lookup_json_string(http.request.body.raw, \"password\")",
		},
	}

	rule, err := applyWAFRuleUpdate(existing, WAFRuleUpdateRequest{
		ActionParametersJSON: wafJSON(`{"response":{"status_code":429,"content_type":"application/json","content":"{\"error\":\"rate limited\"}"}}`),
		RateLimit:            wafJSON(`{"characteristics":["cf.colo.id","ip.src"],"requests_per_period":10,"period":60,"mitigation_timeout":300,"requests_to_origin":true}`),
		Logging:              wafJSON(`{"enabled":false}`),
		ExposedCredentialCheck: wafJSON(`{
			"username_expression":"lookup_json_string(http.request.body.raw, \"email\")",
			"password_expression":"lookup_json_string(http.request.body.raw, \"password\")"
		}`),
	})
	if err != nil {
		t.Fatalf("advanced update failed: %v", err)
	}
	if rule.ActionParameters == nil || rule.ActionParameters.Response == nil || rule.ActionParameters.Response.StatusCode != 429 {
		t.Fatalf("advanced action parameters not applied: %#v", rule.ActionParameters)
	}
	if rule.RateLimit == nil || rule.RateLimit.RequestsPerPeriod != 10 || !rule.RateLimit.RequestsToOrigin {
		t.Fatalf("advanced ratelimit not applied: %#v", rule.RateLimit)
	}
	if rule.Logging == nil || rule.Logging.Enabled == nil || *rule.Logging.Enabled {
		t.Fatalf("advanced logging not applied: %#v", rule.Logging)
	}
	if rule.ExposedCredentialCheck == nil || !strings.Contains(rule.ExposedCredentialCheck.UsernameExpression, "email") {
		t.Fatalf("advanced credential check not applied: %#v", rule.ExposedCredentialCheck)
	}

	rule, err = applyWAFRuleUpdate(existing, WAFRuleUpdateRequest{
		ActionParametersJSON:   wafJSON(`null`),
		RateLimit:              wafJSON(`null`),
		Logging:                wafJSON(`null`),
		ExposedCredentialCheck: wafJSON(`null`),
	})
	if err != nil {
		t.Fatalf("advanced clear failed: %v", err)
	}
	if rule.ActionParameters != nil || rule.RateLimit != nil || rule.Logging != nil || rule.ExposedCredentialCheck != nil {
		t.Fatalf("advanced clear did not remove fields: %#v", rule)
	}

	_, err = applyWAFRuleUpdate(existing, WAFRuleUpdateRequest{RateLimit: wafJSON(`[]`)})
	if err == nil {
		t.Fatal("expected array advanced json to be rejected")
	}
	var validationErr ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected ValidationError, got %T", err)
	}
}

func TestWAFRuleUpdateRequestDecodesAdvancedJSONPresence(t *testing.T) {
	var req WAFRuleUpdateRequest
	if err := json.Unmarshal([]byte(`{"ratelimit":null,"logging":{"enabled":true}}`), &req); err != nil {
		t.Fatalf("decode update request: %v", err)
	}
	if !req.RateLimit.Set || strings.TrimSpace(string(req.RateLimit.Raw)) != "null" {
		t.Fatalf("ratelimit null presence was not preserved: %#v", req.RateLimit)
	}
	if !req.Logging.Set || !strings.Contains(string(req.Logging.Raw), `"enabled"`) {
		t.Fatalf("logging object presence was not preserved: %#v", req.Logging)
	}
	if req.ExposedCredentialCheck.Set {
		t.Fatalf("omitted credential check should not be marked set: %#v", req.ExposedCredentialCheck)
	}
}

func TestApplyWAFRuleUpdateAllowsSimpleFieldsOnUnsupportedExistingAction(t *testing.T) {
	enabled := true
	disabled := false
	existing := cloudflare.RulesetRule{
		ID:          "rule-1",
		Action:      "execute",
		Expression:  "true",
		Description: "Managed ruleset",
		Enabled:     &enabled,
		ActionParameters: &cloudflare.RulesetRuleActionParameters{
			ID: "managed-ruleset",
			Overrides: &cloudflare.RulesetRuleActionParametersOverrides{
				Action: "log",
			},
		},
	}

	description := "Updated description"
	rule, err := applyWAFRuleUpdate(existing, WAFRuleUpdateRequest{
		Enabled:     &disabled,
		Description: &description,
	})
	if err != nil {
		t.Fatalf("simple update on unsupported action failed: %v", err)
	}
	if rule.Action != "execute" || rule.ActionParameters == nil || rule.ActionParameters.ID != "managed-ruleset" {
		t.Fatalf("simple update should preserve unsupported action fields: %#v", rule)
	}
	if rule.Enabled == nil || *rule.Enabled || rule.Description != "Updated description" {
		t.Fatalf("simple update did not apply requested fields: %#v", rule)
	}

	action := "execute"
	if _, err := applyWAFRuleUpdate(existing, WAFRuleUpdateRequest{Action: &action}); err == nil {
		t.Fatal("expected explicit unsupported action update to fail")
	}

	rule, err = applyWAFRuleUpdate(existing, WAFRuleUpdateRequest{
		ActionParametersJSON: wafJSON(`{"id":"managed-ruleset","overrides":{"action":"block"}}`),
	})
	if err != nil {
		t.Fatalf("advanced update on unsupported action failed: %v", err)
	}
	if rule.Action != "execute" || rule.ActionParameters == nil || rule.ActionParameters.ID != "managed-ruleset" || rule.ActionParameters.Overrides == nil || rule.ActionParameters.Overrides.Action != "block" {
		t.Fatalf("advanced update should preserve unsupported action and replace parameters: %#v", rule)
	}
}

func wafJSON(raw string) optionalWAFJSON {
	return optionalWAFJSON{Set: true, Raw: json.RawMessage(raw)}
}

func TestMapWAFRulesetIncludesAdvancedRuleFields(t *testing.T) {
	updated := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	version := "17"
	enabled := false
	loggingEnabled := true
	ruleset := mapWAFRuleset(cloudflare.Ruleset{
		ID:          "ruleset-1",
		Name:        "Custom WAF",
		Phase:       string(cloudflare.RulesetPhaseHTTPRequestFirewallCustom),
		LastUpdated: &updated,
		Rules: []cloudflare.RulesetRule{{
			ID:          "rule-1",
			Ref:         "cfui-rule",
			Version:     &version,
			Action:      "execute",
			Expression:  "true",
			Description: "Managed override",
			Enabled:     &enabled,
			LastUpdated: &updated,
			ActionParameters: &cloudflare.RulesetRuleActionParameters{
				ID:      "managed-ruleset",
				Ruleset: "current",
				Rulesets: []string{
					"managed-1",
				},
				Rules: map[string][]string{
					"managed-1": {"100001", "100002"},
				},
				Products: []string{"waf"},
				Phases:   []string{"http_request_firewall_managed"},
				Version:  &version,
				Overrides: &cloudflare.RulesetRuleActionParametersOverrides{
					Action: "log",
					Categories: []cloudflare.RulesetRuleActionParametersCategories{{
						Category: "wordpress",
						Action:   "block",
					}},
				},
			},
			ScoreThreshold: 42,
			RateLimit: &cloudflare.RulesetRuleRateLimit{
				Characteristics:    []string{"ip.src"},
				RequestsPerPeriod:  100,
				Period:             60,
				MitigationTimeout:  600,
				CountingExpression: "http.request.uri.path contains \"/login\"",
				RequestsToOrigin:   true,
			},
			Logging: &cloudflare.RulesetRuleLogging{Enabled: &loggingEnabled},
			ExposedCredentialCheck: &cloudflare.RulesetRuleExposedCredentialCheck{
				UsernameExpression: "lookup_json_string(http.request.body.raw, \"username\")",
				PasswordExpression: "lookup_json_string(http.request.body.raw, \"password\")",
			},
		}},
	})

	if ruleset.ID != "ruleset-1" || ruleset.LastUpdated == nil || !ruleset.LastUpdated.Equal(updated) {
		t.Fatalf("unexpected ruleset metadata: %#v", ruleset)
	}
	if len(ruleset.Rules) != 1 {
		t.Fatalf("unexpected rules: %#v", ruleset.Rules)
	}
	rule := ruleset.Rules[0]
	if rule.ID != "rule-1" || rule.Ref != "cfui-rule" || rule.Version != version || rule.ScoreThreshold != 42 {
		t.Fatalf("missing rule metadata: %#v", rule)
	}
	if rule.RateLimit == nil || rule.RateLimit.RequestsPerPeriod != 100 || !rule.RateLimit.RequestsToOrigin {
		t.Fatalf("missing ratelimit: %#v", rule.RateLimit)
	}
	if rule.Logging == nil || rule.Logging.Enabled == nil || !*rule.Logging.Enabled {
		t.Fatalf("missing logging: %#v", rule.Logging)
	}
	if rule.CredentialCheck == nil || rule.CredentialCheck.UsernameExpression == "" || rule.CredentialCheck.PasswordExpression == "" {
		t.Fatalf("missing credential check: %#v", rule.CredentialCheck)
	}
	if rule.ActionParameters == nil || rule.ActionParameters.ID != "managed-ruleset" || len(rule.ActionParameters.Rulesets) != 1 || len(rule.ActionParameters.Rules["managed-1"]) != 2 {
		t.Fatalf("missing action parameters: %#v", rule.ActionParameters)
	}
	if rule.ActionParameters.Overrides == nil || rule.ActionParameters.Overrides.Action != "log" || len(rule.ActionParameters.Overrides.Categories) != 1 {
		t.Fatalf("missing structured overrides: %#v", rule.ActionParameters.Overrides)
	}
	if _, ok := rule.ActionParameters.Raw["overrides"]; !ok {
		t.Fatalf("raw action parameters should expose advanced fields: %#v", rule.ActionParameters.Raw)
	}
}

func TestMapWAFRulesetFilteredKeepsOnlySkipRules(t *testing.T) {
	enabled := true
	ruleset := mapWAFRulesetFiltered(cloudflare.Ruleset{
		ID:    "ruleset-1",
		Phase: string(cloudflare.RulesetPhaseHTTPRequestFirewallManaged),
		Rules: []cloudflare.RulesetRule{
			{ID: "execute-1", Action: "execute", Expression: "true", Enabled: &enabled},
			{ID: "skip-1", Action: "skip", Expression: "true", Enabled: &enabled, ActionParameters: &cloudflare.RulesetRuleActionParameters{Rulesets: []string{"managed-ruleset"}}},
		},
	}, true)
	if ruleset.Phase != string(cloudflare.RulesetPhaseHTTPRequestFirewallManaged) {
		t.Fatalf("unexpected phase: %#v", ruleset)
	}
	if len(ruleset.Rules) != 1 || ruleset.Rules[0].ID != "skip-1" {
		t.Fatalf("expected only skip rule, got %#v", ruleset.Rules)
	}
}

func TestMapWAFRulesetByActionKeepsManagedOverrides(t *testing.T) {
	enabled := true
	ruleset := mapWAFRulesetByAction(cloudflare.Ruleset{
		ID:    "ruleset-1",
		Phase: string(cloudflare.RulesetPhaseHTTPRequestFirewallManaged),
		Rules: []cloudflare.RulesetRule{
			{ID: "execute-1", Action: "execute", Expression: "true", Enabled: &enabled, ActionParameters: &cloudflare.RulesetRuleActionParameters{ID: "managed-ruleset"}},
			{ID: "skip-1", Action: "skip", Expression: "true", Enabled: &enabled, ActionParameters: &cloudflare.RulesetRuleActionParameters{Rulesets: []string{"managed-ruleset"}}},
		},
	}, "execute")
	if ruleset.Phase != string(cloudflare.RulesetPhaseHTTPRequestFirewallManaged) {
		t.Fatalf("unexpected phase: %#v", ruleset)
	}
	if len(ruleset.Rules) != 1 || ruleset.Rules[0].ID != "execute-1" || ruleset.Rules[0].ActionParameters == nil || ruleset.Rules[0].ActionParameters.ID != "managed-ruleset" {
		t.Fatalf("expected only execute managed override, got %#v", ruleset.Rules)
	}
}

func TestNormalizeAnalyticsRange(t *testing.T) {
	now := time.Date(2026, 6, 18, 10, 30, 0, 0, time.UTC)
	tests := []struct {
		name      string
		value     string
		wantRange string
		wantSince time.Time
		wantErr   bool
	}{
		{name: "default", wantRange: "24h", wantSince: now.Add(-24 * time.Hour)},
		{name: "24h", value: "24H", wantRange: "24h", wantSince: now.Add(-24 * time.Hour)},
		{name: "7d", value: "7d", wantRange: "7d", wantSince: now.Add(-7 * 24 * time.Hour)},
		{name: "30d", value: "30d", wantRange: "30d", wantSince: now.Add(-30 * 24 * time.Hour)},
		{name: "bad", value: "90d", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRange, gotSince, gotUntil, err := normalizeAnalyticsRange(tt.value, now)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				var validationErr ValidationError
				if !errors.As(err, &validationErr) {
					t.Fatalf("expected ValidationError, got %T", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotRange != tt.wantRange || !gotSince.Equal(tt.wantSince) || !gotUntil.Equal(now) {
				t.Fatalf("range = %q %s %s, want %q %s %s", gotRange, gotSince, gotUntil, tt.wantRange, tt.wantSince, now)
			}
		})
	}
}

func testOAuthService(t *testing.T) *cfoauth.Service {
	t.Helper()
	return testOAuthServiceWithScopes(t, "analytics.read account-analytics.read")
}

func testOAuthServiceWithScopes(t *testing.T, scopes string) *cfoauth.Service {
	t.Helper()

	store := cfoauth.NewStore(t.TempDir())
	if err := store.SaveSession(context.Background(), cfoauth.Session{
		ID:           "session-1",
		Label:        "test@example.com",
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    time.Now().UTC().Add(time.Hour),
		Scope:        scopes,
		Current:      true,
	}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	return cfoauth.NewService(cfoauth.Config{}, store)
}

func assertBearer(t *testing.T, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
		t.Fatalf("unexpected authorization header: %q", got)
	}
}

func writeCFEnvelope(w http.ResponseWriter, result string, info map[string]int) {
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

func overviewMetricMap(items []OverviewMetric) map[string]OverviewMetric {
	out := make(map[string]OverviewMetric, len(items))
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}

func mustDecodeGraphQLQuery(t *testing.T, r *http.Request) string {
	t.Helper()

	var req struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Fatalf("decode graphql request: %v", err)
	}
	return req.Query
}

func writeGraphQLUsageFixture(t *testing.T, w http.ResponseWriter, query string) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.Contains(query, "r2OperationsAdaptiveGroups"):
		_, _ = w.Write([]byte(`{
			"data": {"viewer": {"accounts": [{
				"period": [{"sum": {"requests": 100, "errors": 3, "subrequests": 17}, "quantiles": {"cpuTimeP50": 1000, "cpuTimeP99": 9000}}],
				"today": [{"sum": {"requests": 11, "errors": 0, "subrequests": 2}}],
				"r2Ops": [{"dimensions": {"actionType": "PutObject"}, "sum": {"requests": 7}}],
				"r2Storage": [{"dimensions": {"bucketName": "bucket"}, "max": {"payloadSize": 100, "metadataSize": 25, "objectCount": 4}}]
			}]}}
		}`))
	case strings.Contains(query, "cpuTimeUs"):
		_, _ = w.Write([]byte(`{
			"data": {"viewer": {"accounts": [{
				"period": [{"sum": {"cpuTimeUs": 1234}}],
				"today": [{"sum": {"cpuTimeUs": 123}}]
			}]}}
		}`))
	case strings.Contains(query, "window: workersInvocationsAdaptive"):
		_, _ = w.Write([]byte(`{
			"data": {"viewer": {"accounts": [{
				"window": [{"sum": {"errors": 5}}]
			}]}}
		}`))
	case strings.Contains(query, "d1AnalyticsAdaptiveGroups"):
		_, _ = w.Write([]byte(`{
			"data": {"viewer": {"accounts": [{
				"period": [{"sum": {"rowsRead": 10, "rowsWritten": 2, "readQueries": 5, "writeQueries": 1}}],
				"today": [{"sum": {"rowsRead": 3, "rowsWritten": 1, "readQueries": 2, "writeQueries": 1}}]
			}]}}
		}`))
	case strings.Contains(query, "kvOperationsAdaptiveGroups"):
		_, _ = w.Write([]byte(`{
			"data": {"viewer": {"accounts": [{
				"period": [{"dimensions": {"actionType": "read"}, "sum": {"requests": 8}}],
				"today": [{"dimensions": {"actionType": "read"}, "sum": {"requests": 3}}]
			}]}}
		}`))
	case strings.Contains(query, "kvStorageAdaptiveGroups"):
		_, _ = w.Write([]byte(`{
			"data": {"viewer": {"accounts": [{
				"storage": [{"dimensions": {"namespaceId": "namespace"}, "max": {"byteCount": 64, "keyCount": 9}}]
			}]}}
		}`))
	default:
		t.Fatalf("unexpected graphql query: %s", query)
	}
}
