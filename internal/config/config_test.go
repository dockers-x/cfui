package config

import "testing"

func TestEffectiveTunnelManagementEnvironmentOverrides(t *testing.T) {
	t.Setenv("CFUI_TUNNEL_MGMT_ENABLED", "true")
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "env-account")
	t.Setenv("CLOUDFLARE_TUNNEL_ID", "env-tunnel")
	t.Setenv("CLOUDFLARE_API_TOKEN", "env-token")

	cfg := DefaultConfig()
	cfg.TunnelManagement = TunnelManagementConfig{
		Enabled:   false,
		AccountID: "saved-account",
		TunnelID:  "saved-tunnel",
		APIToken:  "saved-token",
	}

	effective := cfg.EffectiveTunnelManagement()
	if !effective.Enabled {
		t.Fatal("expected environment to enable tunnel management")
	}
	if effective.AccountID != "env-account" || effective.TunnelID != "env-tunnel" || effective.APIToken != "env-token" {
		t.Fatalf("unexpected effective config: %#v", effective)
	}
}
