package config

import (
	"cfui/internal/logger"
	"cfui/internal/persist"
	"cfui/internal/persist/ent"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
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

const DefaultTunnelProfileKey = "default"

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
	Edge            string `json:"edge"`              // space/comma separated edge addresses to pin (host:port)
	PostQuantum     bool   `json:"post_quantum"`      // Enable PQC for QUIC
	NoTLSVerify     bool   `json:"no_tls_verify"`     // Disable TLS verification for backend services

	// Custom extra arguments (space-separated: "--key1 val1 --key2 val2")
	ExtraArgs string `json:"extra_args"`

	// ActiveTunnelKey is the legacy/default profile used by old single-tunnel
	// endpoints and features that still need an implicit tunnel profile.
	ActiveTunnelKey string `json:"active_tunnel_key"`

	// Tunnels stores all configured Cloudflare Tunnel profiles. Top-level
	// tunnel runner fields mirror the active profile for API compatibility.
	Tunnels []TunnelProfileConfig `json:"tunnels"`

	// Optional Cloudflare API-backed tunnel configuration manager.
	TunnelManagement TunnelManagementConfig `json:"tunnel_management"`

	// DDNS configuration for automatic DNS record updating.
	DDNS DDNSConfig `json:"ddns"`

	// MCPEnabled gates the Model Context Protocol HTTP endpoint.
	MCPEnabled bool `json:"mcp_enabled"`

	// S3WebDAV exposes S3-compatible bucket paths through WebDAV.
	S3WebDAV S3WebDAVConfig `json:"s3_webdav"`

	// OAuthRelayCallbackURL overrides CFUI_OAUTH_RELAY_URL for the Cloudflare
	// OAuth workspace. It is not secret and is safe to persist with app settings.
	OAuthRelayCallbackURL string `json:"oauth_relay_callback_url"`

	// OAuthClientID overrides CFUI_OAUTH_CLIENT_ID when that environment
	// variable is not set. Client IDs are public OAuth metadata, not secrets.
	OAuthClientID string `json:"oauth_client_id"`
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

// TunnelProfileConfig stores one Cloudflare Tunnel profile. A profile can be
// used for local running, remote ingress management, or both.
type TunnelProfileConfig struct {
	Key                     string `json:"key"`
	Name                    string `json:"name"`
	Token                   string `json:"token"`
	LocalEnabled            bool   `json:"local_enabled"`
	RemoteManagementEnabled bool   `json:"remote_management_enabled"`
	AccountID               string `json:"account_id"`
	TunnelID                string `json:"tunnel_id"`
	AutoStart               bool   `json:"auto_start"`
	AutoRestart             bool   `json:"auto_restart"`
	CustomTag               string `json:"custom_tag"`
	SoftwareName            string `json:"software_name"`
	Protocol                string `json:"protocol"`
	GracePeriod             string `json:"grace_period"`
	Region                  string `json:"region"`
	Retries                 int    `json:"retries"`
	MetricsEnable           bool   `json:"metrics_enable"`
	MetricsPort             int    `json:"metrics_port"`
	LogLevel                string `json:"log_level"`
	LogFile                 string `json:"log_file"`
	LogJSON                 bool   `json:"log_json"`
	EdgeIPVersion           string `json:"edge_ip_version"`
	EdgeBindAddress         string `json:"edge_bind_address"`
	Edge                    string `json:"edge"`
	PostQuantum             bool   `json:"post_quantum"`
	NoTLSVerify             bool   `json:"no_tls_verify"`
	ExtraArgs               string `json:"extra_args"`
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
	defaultTunnel := DefaultTunnelProfileConfig()
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
		Edge:            "",
		PostQuantum:     false,
		NoTLSVerify:     false, // Verify TLS by default for security
		ExtraArgs:       "",
		ActiveTunnelKey: defaultTunnel.Key,
		Tunnels:         []TunnelProfileConfig{defaultTunnel},
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

func DefaultTunnelProfileConfig() TunnelProfileConfig {
	return TunnelProfileConfig{
		Key:                     DefaultTunnelProfileKey,
		Name:                    "Tunnel 1",
		LocalEnabled:            true,
		RemoteManagementEnabled: false,
		AutoRestart:             true,
		SoftwareName:            "cfui",
		Protocol:                "auto",
		GracePeriod:             "30s",
		Retries:                 5,
		MetricsPort:             60123,
		LogLevel:                "info",
		EdgeIPVersion:           "auto",
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
	return c.EffectiveTunnelManagementFor(c.ActiveTunnelKey)
}

// EffectiveTunnelManagementFor returns tunnel-management settings for a
// selected tunnel profile after applying shared credential settings and
// environment-variable overrides.
func (c Config) EffectiveTunnelManagementFor(tunnelKey string) TunnelManagementConfig {
	cfg := c.TunnelManagement
	if tunnel, ok := c.TunnelProfile(tunnelKey); ok {
		cfg.Enabled = tunnel.RemoteManagementEnabled
		if strings.TrimSpace(tunnel.AccountID) != "" {
			cfg.AccountID = tunnel.AccountID
		}
		if strings.TrimSpace(tunnel.TunnelID) != "" {
			cfg.TunnelID = tunnel.TunnelID
		}
	}

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
	tunnel := c.ActiveTunnelProfile()
	if strings.TrimSpace(tunnel.Token) != "" {
		return ParseTunnelTokenIdentity(tunnel.Token)
	}
	return ParseTunnelTokenIdentity(c.Token)
}

func (c Config) TunnelProfile(key string) (TunnelProfileConfig, bool) {
	cfg := normalizeTunnelProfiles(c)
	key = normalizeTunnelKey(key)
	if key == "" {
		key = cfg.ActiveTunnelKey
	}
	for _, tunnel := range cfg.Tunnels {
		if tunnel.Key == key {
			return tunnel, true
		}
	}
	return TunnelProfileConfig{}, false
}

func (c Config) ActiveTunnelProfile() TunnelProfileConfig {
	cfg := normalizeTunnelProfiles(c)
	for _, tunnel := range cfg.Tunnels {
		if tunnel.Key == cfg.ActiveTunnelKey {
			return tunnel
		}
	}
	return cfg.Tunnels[0]
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
	saveMu sync.Mutex
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
	m.saveMu.Lock()
	defer m.saveMu.Unlock()

	cfg, err := m.loadConfig(context.Background())
	if err != nil {
		return err
	}
	cfg = applyActiveTunnelToTopLevel(cfg)

	m.mu.Lock()
	m.cfg = cloneConfig(cfg)
	m.mu.Unlock()
	return nil
}

func (m *Manager) Save(cfg Config) error {
	m.saveMu.Lock()
	defer m.saveMu.Unlock()

	m.mu.RLock()
	current := cloneConfig(m.cfg)
	m.mu.RUnlock()
	if cfg.DDNS.IPSources == nil {
		cfg.DDNS.IPSources = cloneSlice(current.DDNS.IPSources)
	}
	if cfg.DDNS.Records == nil {
		cfg.DDNS.Records = cloneSlice(current.DDNS.Records)
	}
	if cfg.Tunnels == nil {
		cfg.Tunnels = cloneSlice(current.Tunnels)
	}
	if cfg.ActiveTunnelKey == "" {
		cfg.ActiveTunnelKey = current.ActiveTunnelKey
	}
	if cfg.ActiveTunnelKey == current.ActiveTunnelKey && topLevelTunnelFieldsChanged(cfg, current) {
		cfg = syncActiveTunnelFromTopLevel(cfg)
	} else if cfg.ActiveTunnelKey == current.ActiveTunnelKey && topLevelTunnelManagementFieldsChanged(cfg, current) {
		cfg = syncActiveTunnelManagementFromTopLevel(cfg)
	} else {
		cfg = applyActiveTunnelToTopLevel(cfg)
	}
	cfg = cloneConfig(cfg)

	if err := m.saveConfig(context.Background(), cfg); err != nil {
		if logger.Sugar != nil {
			logger.Sugar.Errorf("Failed to write config: %v", err)
		}
		return err
	}

	m.mu.Lock()
	m.cfg = cloneConfig(cfg)
	m.mu.Unlock()
	if logger.Sugar != nil {
		logger.Sugar.Debugf("Configuration saved successfully to %s", persist.DBPath(m.dir))
	}
	return nil
}

func (m *Manager) Get() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneConfig(m.cfg)
}

func (m *Manager) Dir() string {
	return m.dir
}

func (m *Manager) ListTunnelProfiles() []TunnelProfileConfig {
	cfg := applyActiveTunnelToTopLevel(m.Get())
	return cloneSlice(cfg.Tunnels)
}

func (m *Manager) SaveTunnelProfile(key string, tunnel TunnelProfileConfig) (Config, error) {
	cfg := normalizeTunnelProfiles(m.Get())
	key = normalizeTunnelKey(key)
	tunnel = normalizeTunnelProfile(tunnel, len(cfg.Tunnels))
	if key != "" {
		tunnel.Key = key
	}
	if tunnel.Key == "" {
		tunnel.Key = DefaultTunnelProfileKey
	}

	found := false
	for i := range cfg.Tunnels {
		if cfg.Tunnels[i].Key == tunnel.Key {
			cfg.Tunnels[i] = normalizeTunnelProfile(tunnel, i)
			found = true
			break
		}
	}
	if !found {
		if tunnelProfileExists(cfg.Tunnels, tunnel.Key) {
			return Config{}, fmt.Errorf("tunnel profile %q already exists", tunnel.Key)
		}
		cfg.Tunnels = append(cfg.Tunnels, normalizeTunnelProfile(tunnel, len(cfg.Tunnels)))
	}
	if cfg.ActiveTunnelKey == "" {
		cfg.ActiveTunnelKey = cfg.Tunnels[0].Key
	}
	cfg = applyActiveTunnelToTopLevel(cfg)
	if err := m.Save(cfg); err != nil {
		return Config{}, err
	}
	return m.Get(), nil
}

func (m *Manager) DeleteTunnelProfile(key string) (Config, error) {
	cfg := normalizeTunnelProfiles(m.Get())
	key = normalizeTunnelKey(key)
	if key == "" {
		return Config{}, errors.New("tunnel profile key is required")
	}
	if len(cfg.Tunnels) <= 1 {
		return Config{}, errors.New("cannot delete the only tunnel profile")
	}
	next := cfg.Tunnels[:0]
	deleted := false
	for _, tunnel := range cfg.Tunnels {
		if tunnel.Key == key {
			deleted = true
			continue
		}
		next = append(next, tunnel)
	}
	if !deleted {
		return Config{}, fmt.Errorf("tunnel profile %q not found", key)
	}
	cfg.Tunnels = next
	if cfg.ActiveTunnelKey == key {
		cfg.ActiveTunnelKey = cfg.Tunnels[0].Key
	}
	cfg = applyActiveTunnelToTopLevel(cfg)
	if err := m.Save(cfg); err != nil {
		return Config{}, err
	}
	return m.Get(), nil
}

func (m *Manager) ActivateTunnelProfile(key string) (Config, error) {
	cfg := normalizeTunnelProfiles(m.Get())
	key = normalizeTunnelKey(key)
	if key == "" {
		return Config{}, errors.New("tunnel profile key is required")
	}
	if !tunnelProfileExists(cfg.Tunnels, key) {
		return Config{}, fmt.Errorf("tunnel profile %q not found", key)
	}
	cfg.ActiveTunnelKey = key
	cfg = applyActiveTunnelToTopLevel(cfg)
	if err := m.Save(cfg); err != nil {
		return Config{}, err
	}
	return m.Get(), nil
}

func cloneConfig(cfg Config) Config {
	cfg.Tunnels = cloneSlice(cfg.Tunnels)
	cfg.DDNS.IPSources = cloneSlice(cfg.DDNS.IPSources)
	cfg.DDNS.Records = cloneSlice(cfg.DDNS.Records)
	cfg.S3WebDAV.Mounts = cloneSlice(cfg.S3WebDAV.Mounts)
	return cfg
}

func cloneSlice[T any](in []T) []T {
	if in == nil {
		return nil
	}
	out := make([]T, len(in))
	copy(out, in)
	return out
}

func topLevelTunnelFieldsChanged(next, current Config) bool {
	return next.Token != current.Token ||
		next.AutoStart != current.AutoStart ||
		next.AutoRestart != current.AutoRestart ||
		next.CustomTag != current.CustomTag ||
		next.SoftwareName != current.SoftwareName ||
		next.Protocol != current.Protocol ||
		next.GracePeriod != current.GracePeriod ||
		next.Region != current.Region ||
		next.Retries != current.Retries ||
		next.MetricsEnable != current.MetricsEnable ||
		next.MetricsPort != current.MetricsPort ||
		next.LogLevel != current.LogLevel ||
		next.LogFile != current.LogFile ||
		next.LogJSON != current.LogJSON ||
		next.EdgeIPVersion != current.EdgeIPVersion ||
		next.EdgeBindAddress != current.EdgeBindAddress ||
		next.Edge != current.Edge ||
		next.PostQuantum != current.PostQuantum ||
		next.NoTLSVerify != current.NoTLSVerify ||
		next.ExtraArgs != current.ExtraArgs
}

func topLevelTunnelManagementFieldsChanged(next, current Config) bool {
	return next.TunnelManagement.Enabled != current.TunnelManagement.Enabled ||
		next.TunnelManagement.AccountID != current.TunnelManagement.AccountID ||
		next.TunnelManagement.TunnelID != current.TunnelManagement.TunnelID
}

func normalizeTunnelProfiles(cfg Config) Config {
	if len(cfg.Tunnels) == 0 {
		cfg.Tunnels = []TunnelProfileConfig{tunnelProfileFromTopLevel(cfg, DefaultTunnelProfileConfig(), 0)}
	}
	seen := make(map[string]int, len(cfg.Tunnels))
	tunnels := make([]TunnelProfileConfig, 0, len(cfg.Tunnels))
	for i, tunnel := range cfg.Tunnels {
		tunnel = normalizeTunnelProfile(tunnel, i)
		base := tunnel.Key
		key := base
		for n := 2; seen[key] > 0; n++ {
			key = base + "-" + strconv.Itoa(n)
		}
		seen[key]++
		tunnel.Key = key
		tunnels = append(tunnels, tunnel)
	}
	cfg.Tunnels = tunnels
	cfg.ActiveTunnelKey = normalizeTunnelKey(cfg.ActiveTunnelKey)
	if cfg.ActiveTunnelKey == "" || !tunnelProfileExists(cfg.Tunnels, cfg.ActiveTunnelKey) {
		cfg.ActiveTunnelKey = cfg.Tunnels[0].Key
	}
	return cfg
}

func normalizeTunnelProfile(tunnel TunnelProfileConfig, index int) TunnelProfileConfig {
	tunnel.Key = normalizeTunnelKey(tunnel.Key)
	if tunnel.Key == "" {
		if index == 0 {
			tunnel.Key = DefaultTunnelProfileKey
		} else {
			tunnel.Key = "tunnel-" + strconv.Itoa(index+1)
		}
	}
	tunnel.Name = strings.TrimSpace(tunnel.Name)
	if tunnel.Name == "" {
		tunnel.Name = "Tunnel " + strconv.Itoa(index+1)
	}
	tunnel.Token = strings.TrimSpace(tunnel.Token)
	tunnel.AccountID = strings.TrimSpace(tunnel.AccountID)
	tunnel.TunnelID = strings.TrimSpace(tunnel.TunnelID)
	tunnel.CustomTag = strings.TrimSpace(tunnel.CustomTag)
	tunnel.SoftwareName = strings.TrimSpace(tunnel.SoftwareName)
	if tunnel.SoftwareName == "" {
		tunnel.SoftwareName = "cfui"
	}
	tunnel.Protocol = normalizeTunnelProtocol(tunnel.Protocol)
	if strings.TrimSpace(tunnel.GracePeriod) == "" {
		tunnel.GracePeriod = "30s"
	}
	if tunnel.Retries <= 0 {
		tunnel.Retries = 5
	}
	if tunnel.MetricsPort <= 0 {
		tunnel.MetricsPort = 60123
	}
	if strings.TrimSpace(tunnel.LogLevel) == "" {
		tunnel.LogLevel = "info"
	}
	if strings.TrimSpace(tunnel.EdgeIPVersion) == "" {
		tunnel.EdgeIPVersion = "auto"
	}
	tunnel.Region = strings.TrimSpace(tunnel.Region)
	tunnel.LogFile = strings.TrimSpace(tunnel.LogFile)
	tunnel.EdgeBindAddress = strings.TrimSpace(tunnel.EdgeBindAddress)
	tunnel.Edge = strings.TrimSpace(tunnel.Edge)
	tunnel.ExtraArgs = strings.TrimSpace(tunnel.ExtraArgs)
	return tunnel
}

func normalizeTunnelProtocol(v string) string {
	switch strings.TrimSpace(v) {
	case "http2", "quic":
		return strings.TrimSpace(v)
	default:
		return "auto"
	}
}

func normalizeTunnelKey(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	var b strings.Builder
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func tunnelProfileExists(tunnels []TunnelProfileConfig, key string) bool {
	for _, tunnel := range tunnels {
		if tunnel.Key == key {
			return true
		}
	}
	return false
}

func tunnelProfileFromTopLevel(cfg Config, base TunnelProfileConfig, index int) TunnelProfileConfig {
	tunnel := base
	tunnel.Token = cfg.Token
	tunnel.LocalEnabled = true
	tunnel.AutoStart = cfg.AutoStart
	tunnel.AutoRestart = cfg.AutoRestart
	tunnel.CustomTag = cfg.CustomTag
	tunnel.SoftwareName = cfg.SoftwareName
	tunnel.Protocol = cfg.Protocol
	tunnel.GracePeriod = cfg.GracePeriod
	tunnel.Region = cfg.Region
	tunnel.Retries = cfg.Retries
	tunnel.MetricsEnable = cfg.MetricsEnable
	tunnel.MetricsPort = cfg.MetricsPort
	tunnel.LogLevel = cfg.LogLevel
	tunnel.LogFile = cfg.LogFile
	tunnel.LogJSON = cfg.LogJSON
	tunnel.EdgeIPVersion = cfg.EdgeIPVersion
	tunnel.EdgeBindAddress = cfg.EdgeBindAddress
	tunnel.Edge = cfg.Edge
	tunnel.PostQuantum = cfg.PostQuantum
	tunnel.NoTLSVerify = cfg.NoTLSVerify
	tunnel.ExtraArgs = cfg.ExtraArgs
	tunnel.RemoteManagementEnabled = cfg.TunnelManagement.Enabled
	tunnel.AccountID = cfg.TunnelManagement.AccountID
	tunnel.TunnelID = cfg.TunnelManagement.TunnelID
	return normalizeTunnelProfile(tunnel, index)
}

func syncActiveTunnelFromTopLevel(cfg Config) Config {
	cfg = normalizeTunnelProfiles(cfg)
	for i := range cfg.Tunnels {
		if cfg.Tunnels[i].Key == cfg.ActiveTunnelKey {
			cfg.Tunnels[i] = tunnelProfileFromTopLevel(cfg, cfg.Tunnels[i], i)
			break
		}
	}
	return cfg
}

func syncActiveTunnelManagementFromTopLevel(cfg Config) Config {
	cfg = normalizeTunnelProfiles(cfg)
	for i := range cfg.Tunnels {
		if cfg.Tunnels[i].Key != cfg.ActiveTunnelKey {
			continue
		}
		cfg.Tunnels[i].RemoteManagementEnabled = cfg.TunnelManagement.Enabled
		cfg.Tunnels[i].AccountID = strings.TrimSpace(cfg.TunnelManagement.AccountID)
		cfg.Tunnels[i].TunnelID = strings.TrimSpace(cfg.TunnelManagement.TunnelID)
		break
	}
	return cfg
}

func applyActiveTunnelToTopLevel(cfg Config) Config {
	cfg = normalizeTunnelProfiles(cfg)
	tunnel := cfg.ActiveTunnelProfile()
	cfg.Token = tunnel.Token
	cfg.AutoStart = tunnel.AutoStart
	cfg.AutoRestart = tunnel.AutoRestart
	cfg.CustomTag = tunnel.CustomTag
	cfg.SoftwareName = tunnel.SoftwareName
	cfg.Protocol = tunnel.Protocol
	cfg.GracePeriod = tunnel.GracePeriod
	cfg.Region = tunnel.Region
	cfg.Retries = tunnel.Retries
	cfg.MetricsEnable = tunnel.MetricsEnable
	cfg.MetricsPort = tunnel.MetricsPort
	cfg.LogLevel = tunnel.LogLevel
	cfg.LogFile = tunnel.LogFile
	cfg.LogJSON = tunnel.LogJSON
	cfg.EdgeIPVersion = tunnel.EdgeIPVersion
	cfg.EdgeBindAddress = tunnel.EdgeBindAddress
	cfg.Edge = tunnel.Edge
	cfg.PostQuantum = tunnel.PostQuantum
	cfg.NoTLSVerify = tunnel.NoTLSVerify
	cfg.ExtraArgs = tunnel.ExtraArgs
	cfg.TunnelManagement.Enabled = tunnel.RemoteManagementEnabled
	cfg.TunnelManagement.AccountID = tunnel.AccountID
	cfg.TunnelManagement.TunnelID = tunnel.TunnelID
	return cfg
}
