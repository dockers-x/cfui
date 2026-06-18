package tunnelmgr

import (
	"cfui/internal/config"
	"cfui/internal/logger"
	"context"
	"encoding/base64"
	"os"
	"strings"
	"sync"
	"testing"

	cloudflare "github.com/cloudflare/cloudflare-go"
)

var initLoggerOnce sync.Once

type fakeCFClient struct {
	config     cloudflare.TunnelConfigurationResult
	tunnel     cloudflare.Tunnel
	updates    []cloudflare.TunnelConfiguration
	dnsRecords []cloudflare.DNSRecord
}

func (f *fakeCFClient) GetTunnel(ctx context.Context, rc *cloudflare.ResourceContainer, tunnelID string) (cloudflare.Tunnel, error) {
	return f.tunnel, nil
}

func (f *fakeCFClient) GetTunnelConfiguration(ctx context.Context, rc *cloudflare.ResourceContainer, tunnelID string) (cloudflare.TunnelConfigurationResult, error) {
	return f.config, nil
}

func (f *fakeCFClient) UpdateTunnelConfiguration(ctx context.Context, rc *cloudflare.ResourceContainer, params cloudflare.TunnelConfigurationParams) (cloudflare.TunnelConfigurationResult, error) {
	f.updates = append(f.updates, params.Config)
	f.config.Config = params.Config
	f.config.Version++
	return f.config, nil
}

func (f *fakeCFClient) ListZonesContext(ctx context.Context, opts ...cloudflare.ReqOption) (cloudflare.ZonesResponse, error) {
	return cloudflare.ZonesResponse{Result: []cloudflare.Zone{
		{ID: "zone-1", Name: "example.com", Status: "active"},
		{ID: "zone-2", Name: "example.net", Status: "pending"},
	}}, nil
}

func (f *fakeCFClient) VerifyAPIToken(ctx context.Context) (cloudflare.APITokenVerifyBody, error) {
	return cloudflare.APITokenVerifyBody{ID: "test-token-id", Status: "active"}, nil
}

func (f *fakeCFClient) GetAPIToken(ctx context.Context, tokenID string) (cloudflare.APIToken, error) {
	return cloudflare.APIToken{
		ID:     tokenID,
		Status: "active",
		Policies: []cloudflare.APITokenPolicies{
			{
				Effect: "allow",
				PermissionGroups: []cloudflare.APITokenPermissionGroups{
					{Name: "Argo Tunnel (Legacy)"},
					{Name: "Zone"},
					{Name: "DNS"},
				},
			},
		},
	}, nil
}

func (f *fakeCFClient) ListDNSRecords(ctx context.Context, rc *cloudflare.ResourceContainer, params cloudflare.ListDNSRecordsParams) ([]cloudflare.DNSRecord, *cloudflare.ResultInfo, error) {
	var matching []cloudflare.DNSRecord
	for _, r := range f.dnsRecords {
		if r.Type == params.Type && r.Name == params.Name {
			matching = append(matching, r)
		}
	}
	return matching, &cloudflare.ResultInfo{}, nil
}

func (f *fakeCFClient) CreateDNSRecord(ctx context.Context, rc *cloudflare.ResourceContainer, params cloudflare.CreateDNSRecordParams) (cloudflare.DNSRecord, error) {
	record := cloudflare.DNSRecord{ID: "dns-1", Type: params.Type, Name: params.Name, Content: params.Content, Comment: params.Comment, Proxied: params.Proxied, TTL: params.TTL}
	f.dnsRecords = append(f.dnsRecords, record)
	return record, nil
}

func (f *fakeCFClient) UpdateDNSRecord(ctx context.Context, rc *cloudflare.ResourceContainer, params cloudflare.UpdateDNSRecordParams) (cloudflare.DNSRecord, error) {
	record := cloudflare.DNSRecord{ID: params.ID, Type: params.Type, Name: params.Name, Content: params.Content, Proxied: params.Proxied, TTL: params.TTL}
	if params.Comment != nil {
		record.Comment = *params.Comment
	}
	for i := range f.dnsRecords {
		if f.dnsRecords[i].ID == params.ID {
			if record.Comment == "" {
				record.Comment = f.dnsRecords[i].Comment
			}
			f.dnsRecords[i] = record
			return record, nil
		}
	}
	f.dnsRecords = append(f.dnsRecords, record)
	return record, nil
}

func (f *fakeCFClient) DeleteDNSRecord(ctx context.Context, rc *cloudflare.ResourceContainer, recordID string) error {
	return nil
}

func tunnelToken(accountID, tunnelID string) string {
	return base64.StdEncoding.EncodeToString([]byte(`{"a":"` + accountID + `","t":"` + tunnelID + `","s":"secret"}`))
}

func rawTunnelToken(accountID, tunnelID string) string {
	return base64.RawStdEncoding.EncodeToString([]byte(`{"a":"` + accountID + `","t":"` + tunnelID + `","s":"secret"}`))
}

func TestSettingsDerivesAccountAndTunnelFromRunnerToken(t *testing.T) {
	cfgMgr := newConfigManager(t)
	cfg := cfgMgr.Get()
	cfg.Token = tunnelToken("account-from-token", "11111111-1111-1111-1111-111111111111")
	cfg.TunnelManagement = config.TunnelManagementConfig{
		Enabled:  true,
		APIToken: "api-token",
	}
	if err := cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	settings := NewManager(cfgMgr).Settings()
	if !settings.DerivedFromToken {
		t.Fatal("expected settings to be marked as derived from token")
	}
	if settings.AccountID != "account-from-token" || settings.TunnelID != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("unexpected derived settings: %#v", settings)
	}
}

func TestSaveSettingsPersistsTokenDerivedIdentityWhenFieldsAreBlank(t *testing.T) {
	cfgMgr := newConfigManager(t)
	cfg := cfgMgr.Get()
	cfg.Token = tunnelToken("account-from-token", "22222222-2222-2222-2222-222222222222")
	if err := cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	mgr := NewManager(cfgMgr)
	if err := mgr.SaveSettings(SettingsRequest{Enabled: true, APIToken: "api-token"}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	saved := cfgMgr.Get().TunnelManagement
	if saved.AccountID != "account-from-token" || saved.TunnelID != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("expected token-derived identity to be persisted, got %#v", saved)
	}
}

func TestSaveSettingsDisablingPreservesStoredFields(t *testing.T) {
	cfgMgr := newConfigManager(t)
	cfg := cfgMgr.Get()
	cfg.TunnelManagement = config.TunnelManagementConfig{
		Enabled:   true,
		AccountID: "account-1",
		TunnelID:  "tunnel-1",
		APIToken:  "api-token",
		APIEmail:  "user@example.com",
		APIKey:    "api-key",
	}
	if err := cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	mgr := NewManager(cfgMgr)
	if err := mgr.SaveSettings(SettingsRequest{Enabled: false}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	saved := cfgMgr.Get().TunnelManagement
	if saved.Enabled {
		t.Fatal("expected tunnel management to be disabled")
	}
	if saved.AccountID != "account-1" || saved.TunnelID != "tunnel-1" || saved.APIToken != "api-token" || saved.APIEmail != "user@example.com" || saved.APIKey != "api-key" {
		t.Fatalf("stored fields were not preserved: %#v", saved)
	}
}

func TestSaveSettingsForNonActiveTunnelDoesNotSwitchLocalRunner(t *testing.T) {
	cfgMgr := newConfigManager(t)
	cfg := cfgMgr.Get()
	cfg.TunnelManagement.Enabled = true
	cfg.TunnelManagement.APIToken = "shared-api-token"
	cfg.ActiveTunnelKey = "home"
	cfg.Tunnels = []config.TunnelProfileConfig{
		{
			Key:                     "home",
			Name:                    "Home",
			Token:                   "home-token",
			LocalEnabled:            true,
			RemoteManagementEnabled: false,
			AccountID:               "home-account",
			TunnelID:                "home-tunnel",
			AutoRestart:             true,
			SoftwareName:            "cfui",
			Protocol:                "auto",
			GracePeriod:             "30s",
			Retries:                 5,
			MetricsPort:             60123,
			LogLevel:                "info",
			EdgeIPVersion:           "auto",
		},
		{
			Key:           "office",
			Name:          "Office",
			Token:         tunnelToken("office-account-from-token", "office-tunnel-from-token"),
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
	if err := cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	mgr := NewManager(cfgMgr)
	if err := mgr.SaveSettingsFor("office", SettingsRequest{Enabled: true}); err != nil {
		t.Fatalf("SaveSettingsFor: %v", err)
	}

	got := cfgMgr.Get()
	if got.ActiveTunnelKey != "home" || got.Token != "home-token" {
		t.Fatalf("remote settings update changed active runner: %#v", got)
	}
	home, ok := got.TunnelProfile("home")
	if !ok {
		t.Fatal("home profile missing")
	}
	if home.RemoteManagementEnabled || home.AccountID != "home-account" || home.TunnelID != "home-tunnel" {
		t.Fatalf("non-active remote settings update polluted active profile: %#v", home)
	}
	office, ok := got.TunnelProfile("office")
	if !ok {
		t.Fatal("office profile missing")
	}
	if !office.RemoteManagementEnabled || office.AccountID != "office-account-from-token" || office.TunnelID != "office-tunnel-from-token" {
		t.Fatalf("office remote identity not derived from office token: %#v", office)
	}
	effective := got.EffectiveTunnelManagementFor("office")
	if !effective.Enabled || effective.AccountID != "office-account-from-token" || effective.TunnelID != "office-tunnel-from-token" || effective.APIToken != "shared-api-token" {
		t.Fatalf("office effective settings not usable for remote management: %#v", effective)
	}
}

func TestFetchForUsesSelectedTunnelProfile(t *testing.T) {
	cfgMgr := newConfigManager(t)
	cfg := cfgMgr.Get()
	cfg.TunnelManagement.Enabled = true
	cfg.TunnelManagement.APIToken = "shared-api-token"
	cfg.ActiveTunnelKey = "home"
	cfg.Tunnels = []config.TunnelProfileConfig{
		{
			Key:                     "home",
			Name:                    "Home",
			Token:                   "home-token",
			LocalEnabled:            true,
			RemoteManagementEnabled: true,
			AccountID:               "home-account",
			TunnelID:                "home-tunnel",
			AutoRestart:             true,
			SoftwareName:            "cfui",
			Protocol:                "auto",
			GracePeriod:             "30s",
			Retries:                 5,
			MetricsPort:             60123,
			LogLevel:                "info",
			EdgeIPVersion:           "auto",
		},
		{
			Key:                     "office",
			Name:                    "Office",
			Token:                   "office-token",
			LocalEnabled:            true,
			RemoteManagementEnabled: true,
			AccountID:               "office-account",
			TunnelID:                "office-tunnel",
			AutoRestart:             true,
			SoftwareName:            "cfui",
			Protocol:                "auto",
			GracePeriod:             "30s",
			Retries:                 5,
			MetricsPort:             60123,
			LogLevel:                "info",
			EdgeIPVersion:           "auto",
		},
	}
	if err := cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	var used config.TunnelManagementConfig
	client := &fakeCFClient{config: cloudflare.TunnelConfigurationResult{TunnelID: "office-tunnel", Version: 3}}
	mgr := NewManagerWithClient(cfgMgr, func(cfg config.TunnelManagementConfig) (cloudflareClient, error) {
		used = cfg
		return client, nil
	})
	resp, err := mgr.FetchFor(context.Background(), "office")
	if err != nil {
		t.Fatalf("FetchFor: %v", err)
	}
	if resp.TunnelID != "office-tunnel" || used.AccountID != "office-account" || used.TunnelID != "office-tunnel" {
		t.Fatalf("fetch did not use selected profile, resp=%#v used=%#v", resp, used)
	}
}

func TestFetchForMissingTunnelProfileDoesNotFallBackToActive(t *testing.T) {
	cfgMgr := newConfigManager(t)
	cfg := cfgMgr.Get()
	cfg.TunnelManagement.Enabled = true
	cfg.TunnelManagement.APIToken = "shared-api-token"
	cfg.ActiveTunnelKey = "home"
	cfg.Tunnels = []config.TunnelProfileConfig{
		{
			Key:                     "home",
			Name:                    "Home",
			Token:                   "home-token",
			LocalEnabled:            true,
			RemoteManagementEnabled: true,
			AccountID:               "home-account",
			TunnelID:                "home-tunnel",
			AutoRestart:             true,
			SoftwareName:            "cfui",
			Protocol:                "auto",
			GracePeriod:             "30s",
			Retries:                 5,
			MetricsPort:             60123,
			LogLevel:                "info",
			EdgeIPVersion:           "auto",
		},
	}
	if err := cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	client := &fakeCFClient{}
	mgr := NewManagerWithClient(cfgMgr, func(config.TunnelManagementConfig) (cloudflareClient, error) {
		return client, nil
	})
	_, err := mgr.FetchFor(context.Background(), "missing")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected missing profile error, got %v", err)
	}
}

func TestFetchTunnelDetailsUpdatesGeneratedProfileName(t *testing.T) {
	cfgMgr := newConfigManager(t)
	cfg := cfgMgr.Get()
	cfg.TunnelManagement.Enabled = true
	cfg.TunnelManagement.APIToken = "shared-api-token"
	cfg.ActiveTunnelKey = "home"
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
			Key:                     "office",
			Name:                    "Tunnel 2",
			Token:                   "office-token",
			LocalEnabled:            true,
			RemoteManagementEnabled: true,
			AccountID:               "office-account",
			TunnelID:                "office-tunnel",
			AutoRestart:             true,
			SoftwareName:            "cfui",
			Protocol:                "auto",
			GracePeriod:             "30s",
			Retries:                 5,
			MetricsPort:             60123,
			LogLevel:                "info",
			EdgeIPVersion:           "auto",
		},
	}
	if err := cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	client := &fakeCFClient{tunnel: cloudflare.Tunnel{ID: "office-tunnel", Name: "Production Tunnel", Status: "healthy"}}
	mgr := NewManagerWithClient(cfgMgr, func(config.TunnelManagementConfig) (cloudflareClient, error) {
		return client, nil
	})
	resp, err := mgr.FetchTunnelDetailsFor(context.Background(), "office")
	if err != nil {
		t.Fatalf("FetchTunnelDetailsFor: %v", err)
	}
	if resp.Name != "Production Tunnel" || resp.Status != "healthy" {
		t.Fatalf("unexpected tunnel details: %#v", resp)
	}
	got := cfgMgr.Get()
	if got.ActiveTunnelKey != "home" || got.Token != "home-token" {
		t.Fatalf("fetching remote tunnel details changed active runner: %#v", got)
	}
	office, ok := got.TunnelProfile("office")
	if !ok || office.Name != "Production Tunnel" {
		t.Fatalf("Cloudflare tunnel name was not saved to generated profile name: %#v", got.Tunnels)
	}
}

func TestFetchTunnelDetailsDoesNotOverwriteManualProfileName(t *testing.T) {
	cfgMgr := newConfigManager(t)
	cfg := cfgMgr.Get()
	cfg.TunnelManagement.Enabled = true
	cfg.TunnelManagement.APIToken = "shared-api-token"
	cfg.ActiveTunnelKey = "office"
	cfg.Tunnels = []config.TunnelProfileConfig{
		{
			Key:                     "office",
			Name:                    "Office Manual Name",
			Token:                   "office-token",
			LocalEnabled:            true,
			RemoteManagementEnabled: true,
			AccountID:               "office-account",
			TunnelID:                "office-tunnel",
			AutoRestart:             true,
			SoftwareName:            "cfui",
			Protocol:                "auto",
			GracePeriod:             "30s",
			Retries:                 5,
			MetricsPort:             60123,
			LogLevel:                "info",
			EdgeIPVersion:           "auto",
		},
	}
	if err := cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	client := &fakeCFClient{tunnel: cloudflare.Tunnel{ID: "office-tunnel", Name: "Production Tunnel"}}
	mgr := NewManagerWithClient(cfgMgr, func(config.TunnelManagementConfig) (cloudflareClient, error) {
		return client, nil
	})
	if _, err := mgr.FetchTunnelDetailsFor(context.Background(), "office"); err != nil {
		t.Fatalf("FetchTunnelDetailsFor: %v", err)
	}
	office, ok := cfgMgr.Get().TunnelProfile("office")
	if !ok || office.Name != "Office Manual Name" {
		t.Fatalf("manual profile name should be preserved: %#v", office)
	}
}

func TestParseTunnelTokenAcceptsUnpaddedBase64(t *testing.T) {
	identity, err := parseTunnelToken(rawTunnelToken("raw-account", "33333333-3333-3333-3333-333333333333"))
	if err != nil {
		t.Fatalf("parseTunnelToken: %v", err)
	}
	if identity.AccountID != "raw-account" || identity.TunnelID != "33333333-3333-3333-3333-333333333333" {
		t.Fatalf("unexpected identity: %#v", identity)
	}
}

func TestCheckPermissionsFromTokenOnlyChecksTunnelAndDNS(t *testing.T) {
	checks := defaultPermissionChecks()
	checkPermissionsFromToken([]cloudflare.APITokenPolicies{
		{
			Effect: "allow",
			PermissionGroups: []cloudflare.APITokenPermissionGroups{
				{Name: permTunnelEdit},
				{Name: permZoneRead},
				{Name: permDNSEdit},
			},
		},
	}, checks)

	granted := map[string]bool{}
	required := map[string]bool{}
	for _, check := range checks {
		granted[check.Name] = check.Granted
		required[check.Name] = check.Required
	}
	if len(checks) != 3 {
		t.Fatalf("expected only tunnel and DNS checks: %#v", checks)
	}
	for _, name := range []string{"account_tunnel_edit", "zone_read", "zone_dns_edit"} {
		if !granted[name] {
			t.Fatalf("expected %s to be granted: %#v", name, checks)
		}
		if !required[name] {
			t.Fatalf("expected %s to be required: %#v", name, checks)
		}
	}
}

func newTestManager(t *testing.T, client *fakeCFClient) *Manager {
	t.Helper()
	cfgMgr := newConfigManager(t)
	cfg := cfgMgr.Get()
	cfg.TunnelManagement = config.TunnelManagementConfig{
		Enabled:   true,
		AccountID: "account-1",
		TunnelID:  "tunnel-1",
		APIToken:  "token-1",
	}
	if err := cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}
	return NewManagerWithClient(cfgMgr, func(config.TunnelManagementConfig) (cloudflareClient, error) {
		return client, nil
	})
}

func newConfigManager(t *testing.T) *config.Manager {
	t.Helper()
	initLoggerOnce.Do(func() {
		logDir, err := os.MkdirTemp("", "cfui-test-logs-*")
		if err != nil {
			t.Fatalf("create log dir: %v", err)
		}
		if err := logger.Initialize(&logger.Config{LogDir: logDir, LogLevel: "error"}); err != nil {
			t.Fatalf("initialize logger: %v", err)
		}
	})
	cfgMgr, err := config.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return cfgMgr
}

func TestAddEntryInsertsBeforeCatchAll(t *testing.T) {
	client := &fakeCFClient{config: cloudflare.TunnelConfigurationResult{
		TunnelID: "tunnel-1",
		Version:  7,
		Config: cloudflare.TunnelConfiguration{Ingress: []cloudflare.UnvalidatedIngressRule{
			{Service: "http_status:404"},
		}},
	}}
	mgr := newTestManager(t, client)

	resp, err := mgr.AddEntry(context.Background(), IngressRule{Hostname: "app.example.com", Service: "http://localhost:8080", NoTLSVerify: true})
	if err != nil {
		t.Fatalf("AddEntry: %v", err)
	}
	if len(resp.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(resp.Entries))
	}
	if resp.Entries[0].Hostname != "app.example.com" || resp.Entries[0].Service != "http://localhost:8080" {
		t.Fatalf("entry was not inserted before catch-all: %#v", resp.Entries[0])
	}
	if resp.Entries[1].Service != "http_status:404" {
		t.Fatalf("catch-all was not preserved last: %#v", resp.Entries[1])
	}
	if len(client.updates) != 1 {
		t.Fatalf("expected one SDK update, got %d", len(client.updates))
	}
}

func TestAddEntryWithS3WebDAVCommentMarksDNSRecord(t *testing.T) {
	client := &fakeCFClient{config: cloudflare.TunnelConfigurationResult{
		TunnelID: "tunnel-1",
		Config: cloudflare.TunnelConfiguration{Ingress: []cloudflare.UnvalidatedIngressRule{
			{Service: "http_status:404"},
		}},
	}}
	mgr := newTestManager(t, client)
	hostname := "dav.example.com"
	service := "http://127.0.0.1:14334"

	if _, err := mgr.AddEntry(context.Background(), IngressRule{
		Hostname: hostname,
		Service:  service,
		Comment:  S3WebDAVTunnelComment(hostname, service),
	}); err != nil {
		t.Fatalf("AddEntry: %v", err)
	}
	if len(client.dnsRecords) != 1 {
		t.Fatalf("expected one DNS record, got %#v", client.dnsRecords)
	}
	if !strings.Contains(client.dnsRecords[0].Comment, S3WebDAVTunnelCommentMarker) {
		t.Fatalf("expected S3 WebDAV DNS comment marker, got %#v", client.dnsRecords[0])
	}
}

func TestCheckS3WebDAVHostnameRequiresTunnelRuleAndDNSComment(t *testing.T) {
	hostname := "dav.example.com"
	service := "http://127.0.0.1:14334"
	client := &fakeCFClient{
		config: cloudflare.TunnelConfigurationResult{
			TunnelID: "tunnel-1",
			Config: cloudflare.TunnelConfiguration{Ingress: []cloudflare.UnvalidatedIngressRule{
				{Hostname: hostname, Service: service},
				{Service: "http_status:404"},
			}},
		},
		dnsRecords: []cloudflare.DNSRecord{{
			ID:      "dns-1",
			Type:    "CNAME",
			Name:    hostname,
			Content: "tunnel-1.cfargotunnel.com",
		}},
	}
	mgr := newTestManager(t, client)

	status := mgr.CheckS3WebDAVHostname(context.Background(), hostname, service)
	if status.Status != S3WebDAVTunnelStatusMissing || !strings.Contains(status.Message, "DNS marker") {
		t.Fatalf("expected missing DNS marker status, got %#v", status)
	}

	client.dnsRecords[0].Comment = S3WebDAVTunnelComment(hostname, service)
	status = mgr.CheckS3WebDAVHostname(context.Background(), hostname, service)
	if status.Status != S3WebDAVTunnelStatusSynced || !status.Synced {
		t.Fatalf("expected synced status, got %#v", status)
	}

	client.dnsRecords[0].Content = "other-tunnel.cfargotunnel.com"
	status = mgr.CheckS3WebDAVHostname(context.Background(), hostname, service)
	if status.Status != S3WebDAVTunnelStatusMissing || !strings.Contains(status.Message, "DNS marker") {
		t.Fatalf("expected wrong CNAME target to be treated as missing DNS marker, got %#v", status)
	}
}

func TestFetchReturnsCurrentConfiguration(t *testing.T) {
	client := &fakeCFClient{config: cloudflare.TunnelConfigurationResult{
		TunnelID: "tunnel-1",
		Version:  3,
		Config: cloudflare.TunnelConfiguration{Ingress: []cloudflare.UnvalidatedIngressRule{
			{Hostname: "app.example.com", Path: "/api/*", Service: "https://localhost:8443"},
			{Service: "http_status:404"},
		}},
	}}
	mgr := newTestManager(t, client)

	resp, err := mgr.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if resp.TunnelID != "tunnel-1" || resp.Version != 3 || len(resp.Entries) != 2 {
		t.Fatalf("unexpected response: %#v", resp)
	}
	if got := resp.Entries[0]; got.Hostname != "app.example.com" || got.Path != "/api/*" || got.Service != "https://localhost:8443" {
		t.Fatalf("unexpected first entry: %#v", got)
	}
}

func TestListZonesUsesCloudflareClient(t *testing.T) {
	client := &fakeCFClient{}
	mgr := newTestManager(t, client)

	zones, err := mgr.ListZones(context.Background())
	if err != nil {
		t.Fatalf("ListZones: %v", err)
	}
	if len(zones) != 2 || zones[0].Name != "example.com" || zones[1].Status != "pending" {
		t.Fatalf("unexpected zones: %#v", zones)
	}
}

func TestUpdateAndDeleteEntry(t *testing.T) {
	client := &fakeCFClient{config: cloudflare.TunnelConfigurationResult{
		TunnelID: "tunnel-1",
		Version:  1,
		Config: cloudflare.TunnelConfiguration{Ingress: []cloudflare.UnvalidatedIngressRule{
			{Hostname: "old.example.com", Service: "http://localhost:8080"},
			{Service: "http_status:404"},
		}},
	}}
	mgr := newTestManager(t, client)

	resp, err := mgr.UpdateEntry(context.Background(), 0, IngressRule{Hostname: "new.example.com", Path: "/api/*", Service: "http://localhost:9090"})
	if err != nil {
		t.Fatalf("UpdateEntry: %v", err)
	}
	if got := resp.Entries[0]; got.Hostname != "new.example.com" || got.Path != "/api/*" || got.Service != "http://localhost:9090" {
		t.Fatalf("unexpected updated entry: %#v", got)
	}

	resp, err = mgr.DeleteEntry(context.Background(), 0)
	if err != nil {
		t.Fatalf("DeleteEntry: %v", err)
	}
	if len(resp.Entries) != 1 || resp.Entries[0].Service != "http_status:404" {
		t.Fatalf("expected only catch-all after delete, got %#v", resp.Entries)
	}
}

func TestReorderEntriesPreservesRequestedOrderAndCatchAll(t *testing.T) {
	client := &fakeCFClient{config: cloudflare.TunnelConfigurationResult{
		TunnelID: "tunnel-1",
		Version:  1,
		Config: cloudflare.TunnelConfiguration{Ingress: []cloudflare.UnvalidatedIngressRule{
			{Hostname: "root.example.com", Path: "/", Service: "http://localhost:8080"},
			{Hostname: "root.example.com", Path: "/aaaa", Service: "http://localhost:8081"},
			{Hostname: "api.example.com", Path: "/", Service: "http://localhost:9090"},
			{Service: "http_status:404"},
		}},
	}}
	mgr := newTestManager(t, client)

	resp, err := mgr.ReorderEntries(context.Background(), []int{1, 0, 2, 3})
	if err != nil {
		t.Fatalf("ReorderEntries: %v", err)
	}
	if len(resp.Entries) != 4 {
		t.Fatalf("expected 4 entries, got %#v", resp.Entries)
	}
	if resp.Entries[0].Path != "/aaaa" || resp.Entries[1].Path != "/" || resp.Entries[3].Service != "http_status:404" {
		t.Fatalf("unexpected reordered entries: %#v", resp.Entries)
	}
	if len(client.updates) != 1 {
		t.Fatalf("expected one SDK update, got %d", len(client.updates))
	}
	updated := client.updates[0].Ingress
	if updated[0].Path != "/aaaa" || updated[1].Path != "/" || updated[3].Service != "http_status:404" {
		t.Fatalf("unexpected SDK update order: %#v", updated)
	}
}

func TestReorderEntriesRejectsMovingCatchAll(t *testing.T) {
	client := &fakeCFClient{config: cloudflare.TunnelConfigurationResult{
		TunnelID: "tunnel-1",
		Config: cloudflare.TunnelConfiguration{Ingress: []cloudflare.UnvalidatedIngressRule{
			{Hostname: "app.example.com", Service: "http://localhost:8080"},
			{Service: "http_status:404"},
		}},
	}}
	mgr := newTestManager(t, client)

	_, err := mgr.ReorderEntries(context.Background(), []int{1, 0})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "catch-all") {
		t.Fatalf("expected catch-all error, got %v", err)
	}
	if len(client.updates) != 0 {
		t.Fatalf("unexpected SDK update after invalid reorder: %#v", client.updates)
	}
}
