package config

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"cfui/internal/persist"

	_ "github.com/lib-x/entsqlite"
)

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

func TestEffectiveTunnelManagementForUsesSelectedProfileState(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ActiveTunnelKey = "home"
	cfg.TunnelManagement = TunnelManagementConfig{
		Enabled:  false,
		APIToken: "shared-token",
	}
	cfg.Tunnels = []TunnelProfileConfig{
		{
			Key:           "home",
			Name:          "Home",
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
			Name:                    "Office",
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

	office := cfg.EffectiveTunnelManagementFor("office")
	if !office.Enabled || office.AccountID != "office-account" || office.TunnelID != "office-tunnel" || office.APIToken != "shared-token" {
		t.Fatalf("expected selected non-active profile to be manageable, got %#v", office)
	}
	home := cfg.EffectiveTunnelManagementFor("home")
	if home.Enabled {
		t.Fatalf("expected active profile remote management to remain disabled, got %#v", home)
	}
}

func TestNewManagerAutoCreatesDatabase(t *testing.T) {
	dir := t.TempDir()

	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if got := mgr.Get().SoftwareName; got != "cfui" {
		t.Fatalf("default config not loaded, software_name = %q", got)
	}
	if got := mgr.Get().Tunnels[0].Name; got != "Tunnel 1" {
		t.Fatalf("default tunnel profile should use neutral display name, got %q", got)
	}

	if _, err := os.Stat(persist.DBPath(dir)); err != nil {
		t.Fatalf("expected database file to exist: %v", err)
	}
}

func TestDDNSRecordCommentPersistsInDatabase(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	cfg := mgr.Get()
	cfg.DDNS.Records = []DDNSRecord{{
		Name: "home.example.com", ZoneID: "zone-1", ZoneName: "example.com",
		Type: "A", Value: "{IPV4}", Comment: "custom comment", TTL: 1,
	}}
	if err := mgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	reloaded, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager reload: %v", err)
	}
	records := reloaded.Get().DDNS.Records
	if len(records) != 1 || records[0].Comment != "custom comment" {
		t.Fatalf("expected persisted DDNS comment, got %#v", records)
	}
}

func TestManagerGetReturnsIndependentConfigSlices(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	cfg := mgr.Get()
	cfg.DDNS.Records = []DDNSRecord{{
		Name: "home.example.com", ZoneID: "zone-1", ZoneName: "example.com",
		Type: "A", Value: "{IPV4}", Comment: "custom comment", TTL: 1,
	}}
	cfg.S3WebDAV.Mounts[0].BucketName = "original-bucket"
	if err := mgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	got := mgr.Get()
	got.DDNS.IPSources[0].URL = "https://mutated.example.com"
	got.DDNS.Records[0].Name = "mutated.example.com"
	got.S3WebDAV.Mounts[0].BucketName = "mutated-bucket"

	again := mgr.Get()
	if again.DDNS.IPSources[0].URL == "https://mutated.example.com" {
		t.Fatal("mutating returned IP sources changed manager state")
	}
	if again.DDNS.Records[0].Name != "home.example.com" {
		t.Fatalf("mutating returned DDNS records changed manager state: %#v", again.DDNS.Records)
	}
	if again.S3WebDAV.Mounts[0].BucketName != "original-bucket" {
		t.Fatalf("mutating returned S3 mounts changed manager state: %#v", again.S3WebDAV.Mounts)
	}
}

func TestS3WebDAVPersistsInDatabase(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	cfg := mgr.Get()
	cfg.S3WebDAV = S3WebDAVConfig{
		Enabled:                 true,
		ActiveKey:               "my-r2",
		WebDAVAccessMode:        S3WebDAVAccessModeDedicated,
		DedicatedBindHost:       "127.0.0.1",
		DedicatedPort:           18080,
		DedicatedAutoStart:      true,
		DedicatedDomainMode:     S3WebDAVDomainModeTunnel,
		DedicatedCustomDomain:   "https://dav.example.com",
		DedicatedTunnelHostname: "dav.example.com",
		Mounts: []S3WebDAVMountConfig{{
			Key:                "my-r2",
			Name:               "My R2",
			Enabled:            true,
			WebDAVEnabled:      true,
			WebDAVAuthEnabled:  false,
			Provider:           "cloudflare_r2",
			EndpointURL:        "https://account-r2.r2.cloudflarestorage.com",
			Region:             "auto",
			PathStyle:          true,
			AccountID:          "account-r2",
			BucketName:         "cfui-r2",
			RootPrefix:         "backups/cfui",
			MountPath:          "/webdav/my_r2/",
			Jurisdiction:       "eu",
			AccessKeyID:        "access-key",
			SecretAccessKey:    "secret-key",
			WebDAVUsername:     "dav-user",
			WebDAVPasswordHash: "$2a$10$hash",
		}},
	}
	if err := mgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	reloaded, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager reload: %v", err)
	}
	got := reloaded.Get().S3WebDAV
	if !got.Enabled || got.ActiveKey != "my-r2" || len(got.Mounts) != 1 {
		t.Fatalf("expected persisted S3 WebDAV settings, got %#v", got)
	}
	if got.WebDAVAccessMode != S3WebDAVAccessModeDedicated || got.DedicatedBindHost != "127.0.0.1" || got.DedicatedPort != 18080 || !got.DedicatedAutoStart || got.DedicatedDomainMode != S3WebDAVDomainModeTunnel || got.DedicatedCustomDomain != "https://dav.example.com" || got.DedicatedTunnelHostname != "dav.example.com" {
		t.Fatalf("expected persisted S3 WebDAV access mode, got %#v", got)
	}
	mount := got.Mounts[0]
	if mount.Provider != "cloudflare_r2" || !mount.WebDAVEnabled || mount.WebDAVAuthEnabled || mount.EndpointURL == "" || mount.AccountID != "account-r2" || mount.BucketName != "cfui-r2" || mount.RootPrefix != "backups/cfui" || mount.MountPath != "/webdav/my_r2/" || mount.Jurisdiction != "eu" || mount.AccessKeyID != "access-key" || mount.SecretAccessKey != "secret-key" || mount.WebDAVUsername != "dav-user" || mount.WebDAVPasswordHash == "" {
		t.Fatalf("expected persisted S3 WebDAV mount, got %#v", mount)
	}
}

func TestS3WebDAVDefaults(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	cfg := mgr.Get()
	cfg.S3WebDAV.Mounts = []S3WebDAVMountConfig{{Key: "", Provider: "", Region: "", MountPath: "", Jurisdiction: ""}}
	if err := mgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	reloaded, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager reload: %v", err)
	}
	got := reloaded.Get().S3WebDAV
	if got.WebDAVAccessMode != S3WebDAVAccessModeMain || got.DedicatedPort != 14334 || got.DedicatedDomainMode != S3WebDAVDomainModeNone || got.DedicatedAutoStart {
		t.Fatalf("expected default S3 WebDAV access mode, got %#v", got)
	}
	if len(got.Mounts) != 1 || got.Mounts[0].Provider != "generic_s3" || got.Mounts[0].Region != "auto" || got.Mounts[0].MountPath != "/webdav/s3/" || got.Mounts[0].Jurisdiction != "default" || !got.Mounts[0].WebDAVEnabled || !got.Mounts[0].WebDAVAuthEnabled {
		t.Fatalf("expected S3 defaults, got %#v", got)
	}
}

func TestNewManagerMigratesLegacyConfigJSON(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "config.json")
	legacyCfg := DefaultConfig()
	legacyCfg.Token = "legacy-token"
	legacyCfg.AutoStart = true
	legacyCfg.MCPEnabled = true
	legacyCfg.DDNS.Enabled = true
	legacyCfg.DDNS.IntervalMins = 9
	legacyCfg.ActiveTunnelKey = ""
	legacyCfg.Tunnels = nil
	legacyCfg.S3WebDAV = S3WebDAVConfig{
		Enabled:   true,
		ActiveKey: "legacy",
		Mounts: []S3WebDAVMountConfig{{
			Key:                "legacy",
			Name:               "Legacy S3",
			Enabled:            true,
			WebDAVEnabled:      true,
			WebDAVAuthEnabled:  true,
			Provider:           "generic_s3",
			EndpointURL:        "https://s3.example.com",
			Region:             "us-east-1",
			PathStyle:          true,
			AccessKeyID:        "legacy-ak",
			SecretAccessKey:    "legacy-sk",
			AccountID:          "legacy-account",
			BucketName:         "legacy-bucket",
			RootPrefix:         "legacy-prefix",
			MountPath:          "/webdav/legacy/",
			Jurisdiction:       "fedramp",
			WebDAVUsername:     "legacy-dav",
			WebDAVPasswordHash: "legacy-hash",
		}},
	}
	legacyCfg.DDNS.Records = []DDNSRecord{{
		Name:    "home.example.com",
		ZoneID:  "zone-1",
		Type:    "A",
		Proxied: true,
		TTL:     1,
	}}

	data, err := json.Marshal(legacyCfg)
	if err != nil {
		t.Fatalf("Marshal legacy config: %v", err)
	}
	if err := os.WriteFile(legacyPath, data, 0644); err != nil {
		t.Fatalf("Write legacy config: %v", err)
	}

	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	got := mgr.Get()
	if got.Token != legacyCfg.Token || got.AutoStart != legacyCfg.AutoStart || !got.MCPEnabled {
		t.Fatalf("legacy config not migrated correctly: %#v", got)
	}
	if got.DDNS.IntervalMins != 9 || len(got.DDNS.Records) != 1 || got.DDNS.Records[0].Name != "home.example.com" {
		t.Fatalf("legacy DDNS config not migrated correctly: %#v", got.DDNS)
	}
	if !got.S3WebDAV.Enabled || len(got.S3WebDAV.Mounts) != 1 || got.S3WebDAV.Mounts[0].EndpointURL != "https://s3.example.com" || got.S3WebDAV.Mounts[0].BucketName != "legacy-bucket" || got.S3WebDAV.Mounts[0].MountPath != "/webdav/legacy/" {
		t.Fatalf("legacy S3 WebDAV config not migrated correctly: %#v", got.S3WebDAV)
	}

	if _, err := os.Stat(filepath.Join(dir, "config.json.migrated")); err != nil {
		t.Fatalf("expected migrated backup to exist: %v", err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("expected legacy config.json to be renamed, stat err = %v", err)
	}

	reloaded, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager reload: %v", err)
	}
	if reloaded.Get().Token != legacyCfg.Token {
		t.Fatalf("expected config to load from database after migration, got %#v", reloaded.Get())
	}
}

func TestNewManagerMigratesLegacyAppConfigsTable(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(persist.DBPath(dir))+"?cache=shared&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatalf("Open legacy db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE app_configs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		"key" TEXT UNIQUE NOT NULL,
		payload BLOB NOT NULL
	)`); err != nil {
		t.Fatalf("Create legacy app_configs: %v", err)
	}

	legacyCfg := DefaultConfig()
	legacyCfg.Token = "db-legacy-token"
	legacyCfg.TunnelManagement.APIToken = "api-token-from-db"
	legacyCfg.ActiveTunnelKey = ""
	legacyCfg.Tunnels = nil
	legacyCfg.DDNS.Enabled = true
	legacyCfg.DDNS.IPSources = []IPSource{{URL: "https://example.com/ip", IPType: "ipv4"}}
	legacyCfg.DDNS.Records = []DDNSRecord{{
		Name:     "host.example.com",
		ZoneID:   "zone-db",
		ZoneName: "example.com",
		Type:     "A",
		Value:    "{IPV4}",
		Proxied:  true,
		TTL:      120,
	}}

	payload, err := json.Marshal(legacyCfg)
	if err != nil {
		t.Fatalf("Marshal legacy payload: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO app_configs("key", payload) VALUES(?, ?)`, defaultConfigKey, payload); err != nil {
		t.Fatalf("Insert legacy payload: %v", err)
	}

	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	got := mgr.Get()
	if got.Token != legacyCfg.Token || got.TunnelManagement.APIToken != legacyCfg.TunnelManagement.APIToken {
		t.Fatalf("legacy app_configs data not migrated correctly: %#v", got)
	}
	if !got.DDNS.Enabled || len(got.DDNS.IPSources) != 1 || len(got.DDNS.Records) != 1 {
		t.Fatalf("legacy DDNS data not migrated correctly: %#v", got.DDNS)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='app_configs'`).Scan(&count); err != nil {
		t.Fatalf("Check legacy table removal: %v", err)
	}
	if count != 0 {
		t.Fatal("expected app_configs table to be dropped after migration")
	}
}

func TestTunnelProfilesPersistAndActiveProfileMirrorsLegacyFields(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	cfg := mgr.Get()
	cfg.ActiveTunnelKey = "office"
	cfg.Tunnels = []TunnelProfileConfig{
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
			Protocol:                "quic",
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
			AutoStart:               true,
			AutoRestart:             true,
			SoftwareName:            "cfui-office",
			Protocol:                "http2",
			GracePeriod:             "45s",
			Retries:                 7,
			MetricsEnable:           true,
			MetricsPort:             61111,
			LogLevel:                "debug",
			EdgeIPVersion:           "4",
		},
	}
	cfg = applyActiveTunnelToTopLevel(cfg)
	if err := mgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	reloaded, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager reload: %v", err)
	}
	got := reloaded.Get()
	if got.ActiveTunnelKey != "office" || got.Token != "office-token" || got.Protocol != "http2" || got.TunnelManagement.AccountID != "office-account" || got.TunnelManagement.TunnelID != "office-tunnel" {
		t.Fatalf("active tunnel did not mirror top-level fields: %#v", got)
	}
	if len(got.Tunnels) != 2 || got.Tunnels[0].Key != "home" || got.Tunnels[1].Key != "office" {
		t.Fatalf("expected two persisted tunnel profiles, got %#v", got.Tunnels)
	}
}

func TestActivateTunnelProfileUpdatesLegacyConfigSurface(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	cfg := mgr.Get()
	cfg.Tunnels = []TunnelProfileConfig{
		tunnelProfileFromTopLevel(cfg, TunnelProfileConfig{
			Key:          "home",
			Name:         "Home",
			LocalEnabled: true,
		}, 0),
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
			Protocol:                "http2",
			GracePeriod:             "30s",
			Retries:                 5,
			MetricsPort:             60123,
			LogLevel:                "info",
			EdgeIPVersion:           "auto",
		},
	}
	cfg.ActiveTunnelKey = "home"
	cfg = applyActiveTunnelToTopLevel(cfg)
	if err := mgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	got, err := mgr.ActivateTunnelProfile("office")
	if err != nil {
		t.Fatalf("ActivateTunnelProfile: %v", err)
	}
	if got.ActiveTunnelKey != "office" || got.Token != "office-token" || got.TunnelManagement.AccountID != "office-account" {
		t.Fatalf("expected active profile to update legacy fields, got %#v", got)
	}
}

func TestDeleteActiveTunnelProfileSelectsNextDefault(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	cfg := mgr.Get()
	cfg.ActiveTunnelKey = "home"
	cfg.Tunnels = []TunnelProfileConfig{
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
	if err := mgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	got, err := mgr.DeleteTunnelProfile("home")
	if err != nil {
		t.Fatalf("DeleteTunnelProfile: %v", err)
	}
	if got.ActiveTunnelKey != "office" || len(got.Tunnels) != 1 {
		t.Fatalf("active profile delete did not choose next default: %#v", got)
	}
	if got.Token != "office-token" {
		t.Fatalf("legacy tunnel fields did not mirror next default: %#v", got)
	}
}

func TestConcurrentProfileSavesSurviveReload(t *testing.T) {
	mgr, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const count = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range count {
		wg.Go(func() {
			<-start
			key := fmt.Sprintf("office-%d", i)
			if _, err := mgr.SaveTunnelProfile(key, DefaultTunnelProfileConfig()); err != nil {
				t.Errorf("save %s: %v", key, err)
			}
		})
	}
	close(start)
	wg.Wait()
	if err := mgr.Load(); err != nil {
		t.Fatal(err)
	}
	if got := len(mgr.Get().Tunnels); got != count+1 {
		t.Fatalf("got %d profiles after reload, want %d", got, count+1)
	}
}

func TestNormalizeDuplicateTunnelAndS3Keys(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ActiveTunnelKey = "office"
	cfg.Tunnels = []TunnelProfileConfig{
		{Key: "office"},
		{Key: "office"},
		{Key: "office"},
	}
	got := normalizeTunnelProfiles(cfg)
	if len(got.Tunnels) != 3 || got.Tunnels[0].Key != "office" || got.Tunnels[1].Key != "office-2" || got.Tunnels[2].Key != "office-3" {
		t.Fatalf("unexpected tunnel keys after normalization: %#v", got.Tunnels)
	}

	s3 := normalizeS3WebDAVConfig(S3WebDAVConfig{
		ActiveKey: "docs",
		Mounts: []S3WebDAVMountConfig{
			{Key: "docs"},
			{Key: "docs"},
			{Key: "docs"},
		},
	})
	if len(s3.Mounts) != 3 || s3.Mounts[0].Key != "docs" || s3.Mounts[1].Key != "docs-2" || s3.Mounts[2].Key != "docs-3" {
		t.Fatalf("unexpected S3 mount keys after normalization: %#v", s3.Mounts)
	}
}
