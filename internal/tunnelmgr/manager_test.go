package tunnelmgr

import (
	"cfui/internal/config"
	"cfui/internal/logger"
	"context"
	"encoding/base64"
	"os"
	"sync"
	"testing"

	cloudflare "github.com/cloudflare/cloudflare-go"
)

var initLoggerOnce sync.Once

type fakeCFClient struct {
	config  cloudflare.TunnelConfigurationResult
	updates []cloudflare.TunnelConfiguration
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

func TestParseTunnelTokenAcceptsUnpaddedBase64(t *testing.T) {
	identity, err := parseTunnelToken(rawTunnelToken("raw-account", "33333333-3333-3333-3333-333333333333"))
	if err != nil {
		t.Fatalf("parseTunnelToken: %v", err)
	}
	if identity.AccountID != "raw-account" || identity.TunnelID != "33333333-3333-3333-3333-333333333333" {
		t.Fatalf("unexpected identity: %#v", identity)
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
