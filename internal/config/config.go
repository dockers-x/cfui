package config

import (
	"cfui/internal/logger"
	"cfui/internal/persist"
	"cfui/internal/persist/ent"
	"cfui/internal/persist/ent/appconfig"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type Config struct {
	Token        string `json:"token"`
	AutoStart    bool   `json:"auto_start"`    // Auto-start tunnel when service starts
	AutoRestart  bool   `json:"auto_restart"`  // Auto-restart tunnel on abnormal exit
	CustomTag    string `json:"custom_tag"`    // Custom identifier tag shown in Cloudflare dashboard (displayed as "version=xxx" tag)
	SoftwareName string `json:"software_name"` // Software name shown in Cloudflare dashboard (default: "cfui")

	// Advanced cloudflared parameters
	Protocol      string `json:"protocol"`     // auto, http2, quic
	GracePeriod   string `json:"grace_period"` // e.g., "30s"
	Region        string `json:"region"`       // empty or "us"
	Retries       int    `json:"retries"`      // max retries
	MetricsEnable bool   `json:"metrics_enable"`
	MetricsPort   int    `json:"metrics_port"`

	// Additional common parameters
	LogLevel        string `json:"log_level"`         // debug, info, warn, error, fatal
	LogFile         string `json:"log_file"`          // path to log file
	LogJSON         bool   `json:"log_json"`          // Output logs in JSON format (available since 2025.6.1)
	EdgeIPVersion   string `json:"edge_ip_version"`   // auto, 4, 6
	EdgeBindAddress string `json:"edge_bind_address"` // IP address to bind for outgoing connections to Cloudflare edge
	PostQuantum     bool   `json:"post_quantum"`      // Enable PQC for QUIC
	NoTLSVerify     bool   `json:"no_tls_verify"`     // Disable TLS verification for backend services

	// Custom extra arguments (space-separated: "--key1 val1 --key2 val2")
	ExtraArgs string `json:"extra_args"`

	// Optional Cloudflare API-backed tunnel configuration manager.
	TunnelManagement TunnelManagementConfig `json:"tunnel_management"`

	// DDNS configuration for automatic DNS record updating.
	DDNS DDNSConfig `json:"ddns"`

	// MCPEnabled gates the Model Context Protocol HTTP endpoint.
	MCPEnabled bool `json:"mcp_enabled"`
}

// DDNSConfig stores settings for the built-in DDNS client.
type DDNSConfig struct {
	Enabled      bool         `json:"enabled"`
	IPSources    []IPSource   `json:"ip_sources"`
	Records      []DDNSRecord `json:"records"`
	IntervalMins int          `json:"interval_mins"`  // check interval in minutes
	OnlyOnChange bool         `json:"only_on_change"` // only update on IP change
	MaxRetries   int          `json:"max_retries"`    // retries per source on failure
}

// IPSource defines a remote endpoint that returns the public IP address.
type IPSource struct {
	URL    string `json:"url"`
	IPType string `json:"ip_type"` // "ipv4", "ipv6", "auto"
}

// DDNSRecord defines a DNS record managed by the DDNS client.
type DDNSRecord struct {
	Name     string `json:"name"`      // full hostname (e.g., home.example.com)
	ZoneID   string `json:"zone_id"`   // Cloudflare zone ID
	ZoneName string `json:"zone_name"` // zone name for display
	Type     string `json:"type"`      // "A" or "AAAA"
	Value    string `json:"value"`     // "{IPV4}"/"{IPV6}" placeholder or a fixed IP
	Proxied  bool   `json:"proxied"`
	TTL      int    `json:"ttl"` // 1 = Auto
}

// DefaultDDNSConfig returns sensible defaults.
func DefaultDDNSConfig() DDNSConfig {
	return DDNSConfig{
		Enabled: false,
		IPSources: []IPSource{
			// IPv4
			{URL: "https://api-ipv4.ip.sb/ip", IPType: "ipv4"},
			{URL: "http://v4.66666.host:66/ip", IPType: "ipv4"},
			{URL: "https://myip.ipip.net", IPType: "ipv4"},
			{URL: "https://ipv4.ddnspod.com", IPType: "ipv4"},
			{URL: "https://4.ipw.cn", IPType: "ipv4"},
			{URL: "https://ip.3322.net", IPType: "ipv4"},
			// IPv6
			{URL: "https://api-ipv6.ip.sb/ip", IPType: "ipv6"},
			{URL: "http://v6.66666.host:66/ip", IPType: "ipv6"},
			{URL: "http://myip6.ipip.net", IPType: "ipv6"},
			{URL: "https://6.ipw.cn", IPType: "ipv6"},
			{URL: "https://ipv6.ddnspod.com", IPType: "ipv6"},
			{URL: "https://v6.66666.host:66/ip", IPType: "ipv6"},
		},
		Records:      []DDNSRecord{},
		IntervalMins: 5,
		OnlyOnChange: true,
		MaxRetries:   3,
	}
}

// TunnelManagementConfig stores optional credentials and identifiers used to
// manage remotely hosted Cloudflare Tunnel configuration. It is intentionally
// separate from the local cloudflared runner configuration so disabling it does
// not affect the existing token-based tunnel start/stop workflow.
type TunnelManagementConfig struct {
	Enabled   bool   `json:"enabled"`
	AccountID string `json:"account_id"`
	TunnelID  string `json:"tunnel_id"`
	APIToken  string `json:"api_token"`
	APIEmail  string `json:"api_email"`
	APIKey    string `json:"api_key"`
}

// DefaultConfig returns a Config with default values
func DefaultConfig() Config {
	return Config{
		AutoRestart:     true, // Enable auto-restart by default
		CustomTag:       "",
		SoftwareName:    "cfui", // Default software name
		Protocol:        "auto",
		GracePeriod:     "30s",
		Region:          "",
		Retries:         5,
		MetricsEnable:   false,
		MetricsPort:     60123,
		LogLevel:        "info",
		LogFile:         "",
		LogJSON:         false,
		EdgeIPVersion:   "auto",
		EdgeBindAddress: "",
		PostQuantum:     false,
		NoTLSVerify:     false, // Verify TLS by default for security
		ExtraArgs:       "",
		TunnelManagement: TunnelManagementConfig{
			Enabled: false,
		},
		DDNS: DefaultDDNSConfig(),
	}
}

// EffectiveTunnelManagement returns tunnel-management settings after applying
// environment-variable overrides. Explicit environment values win over saved UI
// settings so deployments can inject credentials without writing secrets to disk.
func (c Config) EffectiveTunnelManagement() TunnelManagementConfig {
	cfg := c.TunnelManagement

	if v, ok := firstEnv("CFUI_TUNNEL_MGMT_ENABLED", "CFUI_TUNNEL_MANAGEMENT_ENABLED"); ok {
		cfg.Enabled = parseBool(v)
	}
	if v, ok := firstEnv("CFUI_TUNNEL_ACCOUNT_ID", "CLOUDFLARE_ACCOUNT_ID", "CLOUDFLARE_APP_ID"); ok {
		cfg.AccountID = v
	}
	if v, ok := firstEnv("CFUI_TUNNEL_ID", "CLOUDFLARE_TUNNEL_ID"); ok {
		cfg.TunnelID = v
	}
	if v, ok := firstEnv("CFUI_TUNNEL_API_TOKEN", "CLOUDFLARE_API_TOKEN"); ok {
		cfg.APIToken = v
	}
	if v, ok := firstEnv("CFUI_TUNNEL_API_EMAIL", "CLOUDFLARE_API_EMAIL"); ok {
		cfg.APIEmail = v
	}
	if v, ok := firstEnv("CFUI_TUNNEL_API_KEY", "CLOUDFLARE_API_KEY"); ok {
		cfg.APIKey = v
	}

	return cfg
}

func firstEnv(keys ...string) (string, bool) {
	for _, key := range keys {
		if v := os.Getenv(key); v != "" {
			return v, true
		}
	}
	return "", false
}

func parseBool(v string) bool {
	switch v {
	case "1", "true", "TRUE", "True", "yes", "YES", "Yes", "on", "ON", "On", "enabled", "ENABLED", "Enabled":
		return true
	default:
		return false
	}
}

type Manager struct {
	dir    string
	client *ent.Client
	mu     sync.RWMutex
	cfg    Config
}

func NewManager(dir string) (*Manager, error) {
	client, err := persist.OpenClient(dir)
	if err != nil {
		return nil, err
	}

	m := &Manager{
		dir:    dir,
		client: client,
		cfg:    DefaultConfig(),
	}

	if err := m.Load(); err != nil {
		if logger.Sugar != nil {
			logger.Sugar.Errorf("Failed to load config: %v", err)
		}
		_ = client.Close()
		return nil, err
	}

	if logger.Sugar != nil {
		logger.Sugar.Infof("Loaded configuration from %s", persist.DBPath(dir))
	}

	return m, nil
}

func (m *Manager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.loadLocked(context.Background())
	if err != nil {
		return err
	}

	m.cfg = cfg
	return nil
}

func (m *Manager) Save(cfg Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.saveLocked(context.Background(), cfg); err != nil {
		if logger.Sugar != nil {
			logger.Sugar.Errorf("Failed to write config: %v", err)
		}
		return err
	}

	m.cfg = cfg
	if logger.Sugar != nil {
		logger.Sugar.Debugf("Configuration saved successfully to %s", persist.DBPath(m.dir))
	}
	return nil
}

func (m *Manager) Get() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

func (m *Manager) Dir() string {
	return m.dir
}

const defaultConfigKey = "default"

func (m *Manager) loadLocked(ctx context.Context) (Config, error) {
	row, err := m.client.AppConfig.Query().Where(appconfig.Key(defaultConfigKey)).Only(ctx)
	if err == nil {
		return decodeConfig(row.Payload)
	}
	if !ent.IsNotFound(err) {
		return Config{}, err
	}

	legacyPath := filepath.Join(m.dir, "config.json")
	cfg, migrated, err := loadLegacyConfig(legacyPath)
	if err != nil {
		return Config{}, err
	}
	if migrated {
		if err := m.saveLocked(ctx, cfg); err != nil {
			return Config{}, err
		}
		if err := persist.MarkLegacyMigrated(legacyPath); err != nil && !os.IsNotExist(err) {
			if logger.Sugar != nil {
				logger.Sugar.Warnf("Failed to rename migrated legacy config %s: %v", legacyPath, err)
			}
		}
		if logger.Sugar != nil {
			logger.Sugar.Infof("Migrated legacy config from %s to %s", legacyPath, persist.DBPath(m.dir))
		}
		return cfg, nil
	}

	cfg = DefaultConfig()
	if err := m.saveLocked(ctx, cfg); err != nil {
		return Config{}, err
	}
	if logger.Sugar != nil {
		logger.Sugar.Infof("Initialized default configuration in %s", persist.DBPath(m.dir))
	}
	return cfg, nil
}

func (m *Manager) saveLocked(ctx context.Context, cfg Config) error {
	payload, err := json.Marshal(cfg)
	if err != nil {
		return err
	}

	row, err := m.client.AppConfig.Query().Where(appconfig.Key(defaultConfigKey)).Only(ctx)
	if ent.IsNotFound(err) {
		_, err = m.client.AppConfig.Create().
			SetKey(defaultConfigKey).
			SetPayload(payload).
			Save(ctx)
		return err
	}
	if err != nil {
		return err
	}

	_, err = m.client.AppConfig.UpdateOneID(row.ID).
		SetPayload(payload).
		Save(ctx)
	return err
}

func loadLegacyConfig(path string) (Config, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, false, nil
		}
		return Config{}, false, err
	}

	cfg := DefaultConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, false, err
	}
	return cfg, true, nil
}

func decodeConfig(payload []byte) (Config, error) {
	cfg := DefaultConfig()
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
