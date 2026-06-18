package server

import (
	"cfui/internal/config"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestFeaturesTogglePreservesDDNSRecords(t *testing.T) {
	s := newServerTestServer(t)
	cfg := s.cfgMgr.Get()
	cfg.TunnelManagement.Enabled = true
	cfg.DDNS.Enabled = true
	cfg.DDNS.Records = []config.DDNSRecord{{
		Name: "home.example.com", ZoneID: "zone-1", ZoneName: "example.com",
		Type: "A", Value: "{IPV4}", Comment: "keep me", TTL: 1,
	}}
	if err := s.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/features", strings.NewReader(`{"ddns":false}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleFeatures(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("features status %d: %s", rec.Code, rec.Body.String())
	}

	got := s.cfgMgr.Get().DDNS
	if got.Enabled {
		t.Fatal("expected DDNS to be disabled")
	}
	if len(got.Records) != 1 || got.Records[0].Comment != "keep me" {
		t.Fatalf("DDNS records were not preserved: %#v", got.Records)
	}
}

func TestFeaturesResponseReportsRunModes(t *testing.T) {
	tests := []struct {
		name        string
		mode        config.RunMode
		wantClassic bool
		wantOAuth   bool
		wantRunner  bool
	}{
		{name: "classic", mode: config.RunModeClassic, wantClassic: true, wantOAuth: true, wantRunner: true},
		{name: "oauth", mode: config.RunModeOAuth, wantClassic: true, wantOAuth: true, wantRunner: false},
		{name: "both", mode: config.RunModeBoth, wantClassic: true, wantOAuth: true, wantRunner: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newServerTestServer(t)
			s.runMode = tt.mode

			req := httptest.NewRequest(http.MethodGet, "/api/features", nil)
			rec := httptest.NewRecorder()

			s.handleFeatures(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("features status %d: %s", rec.Code, rec.Body.String())
			}

			var resp FeaturesResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode features response: %v", err)
			}
			if resp.Mode != string(tt.mode) || resp.ClassicEnabled != tt.wantClassic || resp.OAuthEnabled != tt.wantOAuth {
				t.Fatalf("unexpected mode response: %#v", resp)
			}
			if resp.Local.TunnelRunner != tt.wantRunner {
				t.Fatalf("local tunnel runner should follow runner mode: %#v", resp.Local)
			}
			if !resp.Cloudflare.Enabled {
				t.Fatalf("cloudflare workspace should remain available: %#v", resp.Cloudflare)
			}
		})
	}
}

func TestFeaturesResponsePreservesLocalFeaturesInOAuthMode(t *testing.T) {
	s := newServerTestServer(t)
	s.runMode = config.RunModeOAuth
	cfg := s.cfgMgr.Get()
	cfg.TunnelManagement.Enabled = true
	cfg.DDNS.Enabled = true
	cfg.MCPEnabled = true
	cfg.S3WebDAV.Enabled = true
	if err := s.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/features", nil)
	rec := httptest.NewRecorder()
	s.handleFeatures(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("features status %d: %s", rec.Code, rec.Body.String())
	}

	var resp FeaturesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode features response: %v", err)
	}
	if !resp.ClassicEnabled || !resp.OAuthEnabled || !resp.TunnelManager || !resp.DDNS || !resp.MCP || !resp.S3WebDAV {
		t.Fatalf("oauth mode should preserve local feature flags for the local workspace: %#v", resp)
	}
	if resp.Local.TunnelRunner || !resp.Local.DDNS || !resp.Local.MCP || !resp.Local.S3WebDAV {
		t.Fatalf("oauth mode should only disable local runner auto-start metadata: %#v", resp.Local)
	}
	if !s.cfgMgr.Get().MCPEnabled || !s.cfgMgr.Get().S3WebDAV.Enabled {
		t.Fatalf("features response must not mutate saved local configuration")
	}
}

func TestFeaturesPostAllowsLocalFeatureUpdatesInOAuthMode(t *testing.T) {
	s := newServerTestServer(t)
	s.runMode = config.RunModeOAuth
	cfg := s.cfgMgr.Get()
	cfg.MCPEnabled = true
	if err := s.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/features", strings.NewReader(`{"mcp":false}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleFeatures(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected local feature update to be accepted in oauth mode, got %d: %s", rec.Code, rec.Body.String())
	}
	if s.cfgMgr.Get().MCPEnabled {
		t.Fatalf("oauth-mode local feature update did not mutate saved config")
	}
}

func TestWorkspaceIndexFallbackServesEmbeddedIndex(t *testing.T) {
	handler := serveEmbeddedIndex(fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html><title>cfui</title>")},
	})

	for _, target := range []string{"/cloudflare", "/cloudflare/", "/cloudflare/resources", "/local", "/local/", "/local/tunnels"} {
		t.Run(target, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, target, nil)
			rec := httptest.NewRecorder()

			handler(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("workspace route status %d: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "<title>cfui</title>") {
				t.Fatalf("workspace route did not serve index: %q", rec.Body.String())
			}
		})
	}
}

func TestRootRouteUsesRunModeDefaultWorkspace(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html><title>cfui</title>")},
	}

	t.Run("oauth redirects to cloudflare workspace", func(t *testing.T) {
		s := &Server{runMode: config.RunModeOAuth}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()

		s.staticHandler(fsys).ServeHTTP(rec, req)

		if rec.Code != http.StatusFound {
			t.Fatalf("root status %d: %s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Location"); got != "/cloudflare" {
			t.Fatalf("root redirect Location = %q, want /cloudflare", got)
		}
	})

	t.Run("classic serves local root", func(t *testing.T) {
		s := &Server{runMode: config.RunModeClassic}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()

		s.staticHandler(fsys).ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("root status %d: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "<title>cfui</title>") {
			t.Fatalf("root did not serve index: %q", rec.Body.String())
		}
	})
}

func TestConfigPostMergesOmittedFeatureConfig(t *testing.T) {
	s := newServerTestServer(t)
	cfg := s.cfgMgr.Get()
	cfg.MCPEnabled = true
	cfg.DDNS.Enabled = true
	cfg.DDNS.Records = []config.DDNSRecord{{
		Name: "home.example.com", ZoneID: "zone-1", ZoneName: "example.com",
		Type: "A", Value: "{IPV4}", Comment: "preserved", TTL: 1,
	}}
	if err := s.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(`{"token":"new-token","auto_restart":true,"software_name":"cfui"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("config status %d: %s", rec.Code, rec.Body.String())
	}

	var resp config.Config
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Token != "new-token" || !resp.MCPEnabled || len(resp.DDNS.Records) != 1 || resp.DDNS.Records[0].Comment != "preserved" {
		t.Fatalf("config post did not merge omitted fields: %#v", resp)
	}
}

func TestTunnelProfileCanBeEditedWithoutActivatingLocalRunner(t *testing.T) {
	s := newServerTestServer(t)
	cfg := s.cfgMgr.Get()
	cfg.Tunnels = []config.TunnelProfileConfig{
		{
			Key:           "home",
			Name:          "Home",
			Token:         "home-token",
			LocalEnabled:  true,
			AutoRestart:   true,
			SoftwareName:  "cfui",
			Protocol:      "auto",
			GracePeriod:   "30s",
			Retries:       5,
			MetricsPort:   60123,
			LogLevel:      "info",
			EdgeIPVersion: "auto",
		},
		{
			Key:           "office",
			Name:          "Office",
			Token:         "office-token",
			LocalEnabled:  true,
			AutoRestart:   true,
			SoftwareName:  "cfui",
			Protocol:      "auto",
			GracePeriod:   "30s",
			Retries:       5,
			MetricsPort:   60123,
			LogLevel:      "info",
			EdgeIPVersion: "auto",
		},
	}
	cfg.ActiveTunnelKey = "home"
	if err := s.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/tunnels/office", strings.NewReader(`{
		"key":"office",
		"name":"Office Updated",
		"token":"office-token-updated",
		"local_enabled":true,
		"remote_management_enabled":true,
		"account_id":"office-account",
		"tunnel_id":"office-tunnel",
		"auto_restart":true,
		"software_name":"cfui",
		"protocol":"http2",
		"grace_period":"30s",
		"retries":5,
		"metrics_port":60123,
		"log_level":"info",
		"edge_ip_version":"auto"
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleTunnel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update tunnel status %d: %s", rec.Code, rec.Body.String())
	}

	got := s.cfgMgr.Get()
	if got.ActiveTunnelKey != "home" || got.Token != "home-token" {
		t.Fatalf("editing non-active tunnel changed active runner config: %#v", got)
	}
	office, ok := got.TunnelProfile("office")
	if !ok || office.Name != "Office Updated" || office.Token != "office-token-updated" || office.Protocol != "http2" {
		t.Fatalf("office profile was not updated: %#v", got.Tunnels)
	}
}
