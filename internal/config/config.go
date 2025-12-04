package config

import (
	"cfui/internal/logger"
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
	}
}

type Manager struct {
	path string
	mu   sync.RWMutex
	cfg  Config
}

func NewManager(dir string) (*Manager, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	path := filepath.Join(dir, "config.json")
	m := &Manager{
		path: path,
		cfg:  DefaultConfig(),
	}

	if err := m.Load(); err != nil {
		if os.IsNotExist(err) {
			logger.Sugar.Infof("Config file not found, creating default config at %s", path)
			if saveErr := m.Save(m.cfg); saveErr != nil {
				logger.Sugar.Errorf("Failed to save default config: %v", saveErr)
			}
		} else {
			logger.Sugar.Errorf("Failed to load config: %v", err)
		}
	} else {
		logger.Sugar.Infof("Loaded configuration from %s", path)
	}

	return m, nil
}

func (m *Manager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.path)
	if err != nil {
		return err
	}

	m.cfg = DefaultConfig()
	return json.Unmarshal(data, &m.cfg)
}

func (m *Manager) Save(cfg Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cfg = cfg

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		logger.Sugar.Errorf("Failed to marshal config: %v", err)
		return err
	}

	if err := os.WriteFile(m.path, data, 0644); err != nil {
		logger.Sugar.Errorf("Failed to write config file: %v", err)
		return err
	}

	logger.Sugar.Debugf("Configuration saved successfully to %s", m.path)
	return nil
}

func (m *Manager) Get() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}
