package configbackup

import (
	"cfui/internal/config"
	"errors"
	"time"
)

const (
	Format            = "cfui-config-backup"
	EnvelopeVersion   = 1
	PayloadVersion    = 1
	MaxBackupBytes    = 8 << 20
	MaxStringBytes    = 64 << 10
	MaxTunnelProfiles = 256
	MaxS3Mounts       = 256
	MaxDDNSSources    = 64
	MaxDDNSRecords    = 1024
)

type Section string

const (
	SectionTunnels          Section = "tunnels"
	SectionRemoteManagement Section = "remote_management"
	SectionDDNS             Section = "ddns"
	SectionS3WebDAV         Section = "s3_webdav"
	SectionApplication      Section = "application"
	SectionSensitive        Section = "sensitive"
)

var (
	ErrInvalidBackup             = errors.New("invalid backup")
	ErrUnsupportedVersion        = errors.New("unsupported backup version")
	ErrPasswordRequired          = errors.New("backup password required")
	ErrInvalidPasswordOrTampered = errors.New("invalid password or tampered backup")
	ErrInvalidSelection          = errors.New("invalid backup section selection")
	ErrTooLarge                  = errors.New("backup exceeds size limit")
)

type Envelope struct {
	Format     string      `json:"format"`
	Version    int         `json:"version"`
	Encrypted  bool        `json:"encrypted"`
	Payload    *Payload    `json:"payload,omitempty"`
	Encryption *Encryption `json:"encryption,omitempty"`
	Ciphertext string      `json:"ciphertext,omitempty"`
}

type Encryption struct {
	Algorithm string `json:"algorithm"`
	KDF       string `json:"kdf"`
	N         int    `json:"n"`
	R         int    `json:"r"`
	P         int    `json:"p"`
	Salt      string `json:"salt"`
	Nonce     string `json:"nonce"`
}

type Payload struct {
	SchemaVersion    int                      `json:"schema_version"`
	CreatedAt        time.Time                `json:"created_at"`
	AppVersion       string                   `json:"app_version"`
	Sections         []Section                `json:"sections"`
	Tunnels          *TunnelSection           `json:"tunnels,omitempty"`
	RemoteManagement *RemoteManagementSection `json:"remote_management,omitempty"`
	DDNS             *DDNSSection             `json:"ddns,omitempty"`
	S3WebDAV         *S3WebDAVSection         `json:"s3_webdav,omitempty"`
	Application      *ApplicationSection      `json:"application,omitempty"`
	Sensitive        *SensitiveSection        `json:"sensitive,omitempty"`
}

type TunnelSection struct {
	ActiveKey string          `json:"active_key"`
	Profiles  []TunnelProfile `json:"profiles"`
}

type TunnelProfile struct {
	Key             string `json:"key"`
	Name            string `json:"name"`
	LocalEnabled    bool   `json:"local_enabled"`
	AutoStart       bool   `json:"auto_start"`
	AutoRestart     bool   `json:"auto_restart"`
	CustomTag       string `json:"custom_tag"`
	SoftwareName    string `json:"software_name"`
	Protocol        string `json:"protocol"`
	GracePeriod     string `json:"grace_period"`
	Region          string `json:"region"`
	Retries         int    `json:"retries"`
	MetricsEnable   bool   `json:"metrics_enable"`
	MetricsPort     int    `json:"metrics_port"`
	LogLevel        string `json:"log_level"`
	LogFile         string `json:"log_file"`
	LogJSON         bool   `json:"log_json"`
	EdgeIPVersion   string `json:"edge_ip_version"`
	EdgeBindAddress string `json:"edge_bind_address"`
	PostQuantum     bool   `json:"post_quantum"`
	NoTLSVerify     bool   `json:"no_tls_verify"`
	ExtraArgs       string `json:"extra_args"`
}

type RemoteManagementSection struct {
	Profiles []RemoteProfile `json:"profiles"`
	APIEmail string          `json:"api_email"`
}

type RemoteProfile struct {
	Key       string `json:"key"`
	Enabled   bool   `json:"enabled"`
	AccountID string `json:"account_id"`
	TunnelID  string `json:"tunnel_id"`
}

type DDNSSection struct {
	Enabled      bool         `json:"enabled"`
	IPSources    []IPSource   `json:"ip_sources"`
	Records      []DDNSRecord `json:"records"`
	IntervalMins int          `json:"interval_mins"`
	OnlyOnChange bool         `json:"only_on_change"`
	MaxRetries   int          `json:"max_retries"`
	UseProxy     bool         `json:"use_proxy"`
}

type IPSource struct {
	URL    string `json:"url"`
	IPType string `json:"ip_type"`
}

type DDNSRecord struct {
	Name     string `json:"name"`
	ZoneID   string `json:"zone_id"`
	ZoneName string `json:"zone_name"`
	Type     string `json:"type"`
	Value    string `json:"value"`
	Comment  string `json:"comment"`
	Proxied  bool   `json:"proxied"`
	TTL      int    `json:"ttl"`
}

type S3WebDAVSection struct {
	Enabled                 bool      `json:"enabled"`
	ActiveKey               string    `json:"active_key"`
	WebDAVAccessMode        string    `json:"webdav_access_mode"`
	DedicatedBindHost       string    `json:"dedicated_bind_host"`
	DedicatedPort           int       `json:"dedicated_port"`
	DedicatedAutoStart      bool      `json:"dedicated_auto_start"`
	DedicatedDomainMode     string    `json:"dedicated_domain_mode"`
	DedicatedCustomDomain   string    `json:"dedicated_custom_domain"`
	DedicatedTunnelHostname string    `json:"dedicated_tunnel_hostname"`
	Mounts                  []S3Mount `json:"mounts"`
}

type S3Mount struct {
	Key               string `json:"key"`
	Name              string `json:"name"`
	Enabled           bool   `json:"enabled"`
	WebDAVEnabled     bool   `json:"webdav_enabled"`
	WebDAVAuthEnabled bool   `json:"webdav_auth_enabled"`
	MountType         string `json:"mount_type"`
	Provider          string `json:"provider"`
	EndpointURL       string `json:"endpoint_url"`
	Region            string `json:"region"`
	PathStyle         bool   `json:"path_style"`
	AccountID         string `json:"account_id"`
	BucketName        string `json:"bucket_name"`
	RootPrefix        string `json:"root_prefix"`
	MountPath         string `json:"mount_path"`
	Jurisdiction      string `json:"jurisdiction"`
	WebDAVUsername    string `json:"webdav_username"`
}

type ApplicationSection struct {
	MCPEnabled            bool   `json:"mcp_enabled"`
	OAuthClientID         string `json:"oauth_client_id"`
	OAuthRelayCallbackURL string `json:"oauth_relay_callback_url"`
}

type SensitiveSection struct {
	TunnelTokens map[string]string        `json:"tunnel_tokens,omitempty"`
	APIToken     string                   `json:"api_token,omitempty"`
	APIKey       string                   `json:"api_key,omitempty"`
	S3           map[string]S3Credentials `json:"s3,omitempty"`
}

type S3Credentials struct {
	AccessKeyID        string `json:"access_key_id,omitempty"`
	SecretAccessKey    string `json:"secret_access_key,omitempty"`
	WebDAVPasswordHash string `json:"webdav_password_hash,omitempty"`
}

type Decoded struct {
	Payload   Payload
	Encrypted bool
}

type Inspection struct {
	CreatedAt         time.Time `json:"created_at"`
	AppVersion        string    `json:"app_version"`
	Encrypted         bool      `json:"encrypted"`
	Sections          []Section `json:"sections"`
	ContainsSensitive bool      `json:"contains_sensitive"`
	TunnelProfiles    int       `json:"tunnel_profiles"`
	DDNSSources       int       `json:"ddns_sources"`
	DDNSRecords       int       `json:"ddns_records"`
	S3Mounts          int       `json:"s3_mounts"`
	Warnings          []string  `json:"warnings,omitempty"`
	RemovedTunnels    []string  `json:"removed_tunnels,omitempty"`
	RestartRequired   []string  `json:"restart_required,omitempty"`
}

type ExportOptions struct {
	Sections         []Section
	IncludeSensitive bool
}

type ApplyResult struct {
	Config            config.Config
	ChangedSections   []Section
	Warnings          []string
	RemovedTunnelKeys []string
	ChangedTunnelKeys []string
}

type TunnelDiff struct {
	RemovedKeys []string
	ChangedKeys []string
}

var sectionOrder = []Section{
	SectionTunnels,
	SectionRemoteManagement,
	SectionDDNS,
	SectionS3WebDAV,
	SectionApplication,
	SectionSensitive,
}

var normalSectionOrder = sectionOrder[:len(sectionOrder)-1]
