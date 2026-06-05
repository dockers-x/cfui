package config

import (
	"cfui/internal/logger"
	"cfui/internal/persist"
	"cfui/internal/persist/ent"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
)

const DefaultDDNSRecordComment = "cfui"

const (
	S3WebDAVAccessModeMain      = "main"
	S3WebDAVAccessModeDedicated = "dedicated"
)

const (
	S3WebDAVDomainModeNone   = "none"
	S3WebDAVDomainModeCustom = "custom"
	S3WebDAVDomainModeTunnel = "tunnel"
)

func NormalizeDDNSRecordComment(comment string) string {
	comment = strings.TrimSpace(comment)
	if comment == "" {
		return DefaultDDNSRecordComment
	}
	return comment
}

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

	// S3WebDAV exposes S3-compatible bucket paths through WebDAV.
	S3WebDAV S3WebDAVConfig `json:"s3_webdav"`
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
	Comment  string `json:"comment"`   // Cloudflare DNS record comment
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

type TunnelTokenIdentity struct {
	AccountID string
	TunnelID  string
}

type encodedTunnelToken struct {
	AccountTag string `json:"a"`
	TunnelID   string `json:"t"`
}

// S3WebDAVConfig stores global state for optional S3-backed WebDAV mounts.
type S3WebDAVConfig struct {
	Enabled                 bool                  `json:"enabled"`
	ActiveKey               string                `json:"active_key"`
	WebDAVAccessMode        string                `json:"webdav_access_mode"`
	DedicatedBindHost       string                `json:"dedicated_bind_host"`
	DedicatedPort           int                   `json:"dedicated_port"`
	DedicatedAutoStart      bool                  `json:"dedicated_auto_start"`
	DedicatedDomainMode     string                `json:"dedicated_domain_mode"`
	DedicatedCustomDomain   string                `json:"dedicated_custom_domain"`
	DedicatedTunnelHostname string                `json:"dedicated_tunnel_hostname"`
	Mounts                  []S3WebDAVMountConfig `json:"mounts"`
}

// S3WebDAVMountConfig stores settings for one S3-backed WebDAV mount.
type S3WebDAVMountConfig struct {
	Key                string `json:"key"`
	Name               string `json:"name"`
	Enabled            bool   `json:"enabled"`
	WebDAVEnabled      bool   `json:"webdav_enabled"`
	WebDAVAuthEnabled  bool   `json:"webdav_auth_enabled"`
	Provider           string `json:"provider"`
	EndpointURL        string `json:"endpoint_url"`
	Region             string `json:"region"`
	PathStyle          bool   `json:"path_style"`
	AccountID          string `json:"account_id"`
	BucketName         string `json:"bucket_name"`
	RootPrefix         string `json:"root_prefix"`
	MountPath          string `json:"mount_path"`
	Jurisdiction       string `json:"jurisdiction"`
	AccessKeyID        string `json:"access_key_id"`
	SecretAccessKey    string `json:"-"`
	WebDAVUsername     string `json:"webdav_username"`
	WebDAVPasswordHash string `json:"-"`
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
		S3WebDAV: S3WebDAVConfig{
			Enabled:             false,
			ActiveKey:           "default",
			WebDAVAccessMode:    S3WebDAVAccessModeMain,
			DedicatedPort:       14334,
			DedicatedDomainMode: S3WebDAVDomainModeNone,
			Mounts:              []S3WebDAVMountConfig{DefaultS3WebDAVMountConfig()},
		},
	}
}

func DefaultS3WebDAVMountConfig() S3WebDAVMountConfig {
	return S3WebDAVMountConfig{
		Key:               "default",
		Name:              "Default S3",
		Enabled:           true,
		WebDAVEnabled:     true,
		WebDAVAuthEnabled: true,
		Provider:          "generic_s3",
		Region:            "auto",
		PathStyle:         true,
		MountPath:         "/webdav/s3/",
		Jurisdiction:      "default",
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

func (c Config) TunnelTokenIdentity() (TunnelTokenIdentity, error) {
	return ParseTunnelTokenIdentity(c.Token)
}

func ParseTunnelTokenIdentity(token string) (TunnelTokenIdentity, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return TunnelTokenIdentity{}, errors.New("tunnel token is empty")
	}

	content, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		content, err = base64.RawStdEncoding.DecodeString(token)
		if err != nil {
			content, err = base64.RawURLEncoding.DecodeString(token)
			if err != nil {
				return TunnelTokenIdentity{}, err
			}
		}
	}

	var encoded encodedTunnelToken
	if err := json.Unmarshal(content, &encoded); err != nil {
		return TunnelTokenIdentity{}, err
	}

	if strings.TrimSpace(encoded.AccountTag) == "" || strings.TrimSpace(encoded.TunnelID) == "" {
		return TunnelTokenIdentity{}, errors.New("tunnel token does not contain account and tunnel identifiers")
	}

	return TunnelTokenIdentity{
		AccountID: strings.TrimSpace(encoded.AccountTag),
		TunnelID:  strings.TrimSpace(encoded.TunnelID),
	}, nil
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

	if cfg.DDNS.IPSources == nil {
		cfg.DDNS.IPSources = m.cfg.DDNS.IPSources
	}
	if cfg.DDNS.Records == nil {
		cfg.DDNS.Records = m.cfg.DDNS.Records
	}

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
