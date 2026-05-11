package ddns

import (
	"cfui/internal/config"
	"cfui/internal/logger"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cloudflare/backoff"
	cloudflare "github.com/cloudflare/cloudflare-go"
)

const (
	ipSourceRetryBaseDelay = 1 * time.Second
	ipSourceRetryMaxDelay  = 10 * time.Second
)

// Service manages periodic IP detection and DNS record synchronization.
type Service struct {
	cfgMgr *config.Manager

	mu        sync.Mutex
	currentV4 string
	currentV6 string
	lastCheck time.Time
	lastError string
	results   []SyncResult // recent sync results (circular)
	running   bool

	stopCh chan struct{}
}

// SyncResult records the outcome of a single DNS sync operation.
type SyncResult struct {
	Time     time.Time `json:"time"`
	Hostname string    `json:"hostname"`
	Type     string    `json:"type"`
	IP       string    `json:"ip"`
	Success  bool      `json:"success"`
	Message  string    `json:"message"`
}

// StatusResponse is the public status of the DDNS service.
type StatusResponse struct {
	Enabled   bool         `json:"enabled"`
	Running   bool         `json:"running"`
	CurrentV4 string       `json:"current_v4"`
	CurrentV6 string       `json:"current_v6"`
	LastCheck string       `json:"last_check"`
	LastError string       `json:"last_error,omitempty"`
	Results   []SyncResult `json:"results"`
	NextCheck string       `json:"next_check,omitempty"`
	Records   int          `json:"records"`
}

// ConfigResponse wraps DDNS config for the frontend.
type ConfigResponse struct {
	config.DDNSConfig
	HasCredentials bool `json:"has_credentials"`
}

// SaveRequest is the request body for updating DDNS config.
type SaveRequest struct {
	Enabled      bool                `json:"enabled"`
	IPSources    []config.IPSource   `json:"ip_sources"`
	Records      []config.DDNSRecord `json:"records"`
	IntervalMins int                 `json:"interval_mins"`
	OnlyOnChange bool                `json:"only_on_change"`
	MaxRetries   int                 `json:"max_retries"`
}

// AddRecordRequest is the request body for adding DDNS records.
// One request may create up to two records (A + AAAA).
type AddRecordRequest struct {
	Subdomain string `json:"subdomain"` // e.g., "home"
	ZoneID    string `json:"zone_id"`
	ZoneName  string `json:"zone_name"`
	IPv4      bool   `json:"ipv4"` // create A record
	IPv6      bool   `json:"ipv6"` // create AAAA record
	IPv4Value string `json:"ipv4_value"`
	IPv6Value string `json:"ipv6_value"`
	Value     string `json:"value"` // single-record edit value
	Proxied   bool   `json:"proxied"`
	TTL       int    `json:"ttl"`
}

// ZoneResponse is a lightweight zone for the frontend.
type ZoneResponse struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

func NewService(cfgMgr *config.Manager) *Service {
	return &Service{
		cfgMgr:  cfgMgr,
		results: make([]SyncResult, 0, 100),
		stopCh:  make(chan struct{}),
	}
}

// Start begins the background DDNS loop if enabled.
func (s *Service) Start() {
	cfg := s.cfgMgr.Get()
	if !cfg.DDNS.Enabled {
		return
	}
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.stopCh = make(chan struct{})
	s.mu.Unlock()

	go s.loop()
	logger.Sugar.Info("DDNS service started")
}

// Stop halts the background loop.
func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	s.running = false
	close(s.stopCh)
	logger.Sugar.Info("DDNS service stopped")
}

// Restart stops and starts the service with the latest config.
func (s *Service) Restart() {
	s.Stop()
	s.Start()
}

func (s *Service) loop() {
	cfg := s.cfgMgr.Get()
	interval := time.Duration(cfg.DDNS.IntervalMins) * time.Minute
	if interval < time.Minute {
		interval = time.Minute
	}
	if interval > 60*time.Minute {
		interval = 60 * time.Minute
	}

	// Run immediately on start
	s.checkAndSync()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.checkAndSync()
		}
	}
}

// DetectIPs fetches the current public IPv4 and IPv6 addresses from configured sources.
func (s *Service) DetectIPs(ctx context.Context) (v4, v6 string, err error) {
	cfg := s.cfgMgr.Get()
	sources := cfg.DDNS.IPSources
	if len(sources) == 0 {
		sources = config.DefaultDDNSConfig().IPSources
	}

	var v4Err, v6Err error
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		v4, v4Err = s.detectIP(ctx, sources, "ipv4")
	}()

	go func() {
		defer wg.Done()
		v6, v6Err = s.detectIP(ctx, sources, "ipv6")
	}()

	wg.Wait()

	if v4 == "" && v6 == "" {
		return "", "", fmt.Errorf("failed to detect any IP: v4=%v v6=%v", v4Err, v6Err)
	}

	return v4, v6, nil
}

func newIPSourceRetryBackoff() *backoff.Backoff {
	return backoff.NewWithoutJitter(ipSourceRetryMaxDelay, ipSourceRetryBaseDelay)
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func validateDetectedIP(ip, targetType string) (string, error) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return "", fmt.Errorf("empty IP response")
	}

	parsed := net.ParseIP(ip)
	if parsed == nil {
		return "", fmt.Errorf("invalid IP response: %q", ip)
	}

	if targetType == "ipv4" && parsed.To4() == nil {
		return "", fmt.Errorf("expected IPv4 response, got %q", ip)
	}
	if targetType == "ipv6" && parsed.To4() != nil {
		return "", fmt.Errorf("expected IPv6 response, got %q", ip)
	}

	return ip, nil
}

func (s *Service) detectIP(ctx context.Context, sources []config.IPSource, targetType string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	maxRetries := s.cfgMgr.Get().DDNS.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}

	var lastErr error
	for _, src := range sources {
		srcType := src.IPType
		if srcType == "" {
			srcType = "auto"
		}
		if targetType != "auto" && srcType != "auto" && srcType != targetType {
			continue
		}

		retryBackoff := newIPSourceRetryBackoff()
		for attempt := 0; attempt < maxRetries; attempt++ {
			ip, err := s.fetchIP(ctx, client, src.URL)
			if err == nil {
				ip, err = validateDetectedIP(ip, targetType)
				if err == nil {
					return ip, nil
				}
			}

			lastErr = err
			if attempt == maxRetries-1 {
				break
			}

			if waitErr := waitForRetry(ctx, retryBackoff.Duration()); waitErr != nil {
				return "", waitErr
			}
		}

		if lastErr != nil {
			logger.Sugar.Debugf("DDNS %s source %s exhausted after %d attempts: %v", targetType, src.URL, maxRetries, lastErr)
		}
	}
	if lastErr != nil {
		return "", fmt.Errorf("no %s address found from %d sources: %w", targetType, len(sources), lastErr)
	}
	return "", fmt.Errorf("no %s address found from %d sources", targetType, len(sources))
}

func (s *Service) fetchIP(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

func (s *Service) checkAndSync() {
	cfg := s.cfgMgr.Get()
	if !cfg.DDNS.Enabled || len(cfg.DDNS.Records) == 0 {
		s.mu.Lock()
		s.lastCheck = time.Now()
		s.mu.Unlock()
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	v4, v6, err := s.DetectIPs(ctx)
	s.mu.Lock()
	s.lastCheck = time.Now()
	if err != nil {
		s.lastError = err.Error()
		s.mu.Unlock()
		logger.Sugar.Warnf("DDNS IP detection failed: %v", err)
		return
	}
	s.lastError = ""

	changed := false
	if hasFixedValueRecords(cfg.DDNS.Records) {
		changed = true
	} else if cfg.DDNS.OnlyOnChange {
		if v4 != s.currentV4 || v6 != s.currentV6 {
			changed = true
		}
	} else {
		changed = true
	}

	if !changed {
		s.mu.Unlock()
		return
	}

	s.currentV4 = v4
	s.currentV6 = v6
	records := NormalizeRecords(cfg.DDNS.Records)
	s.mu.Unlock()

	s.syncAllRecords(ctx, records, v4, v6)
}

// SyncNow triggers an immediate detection and sync cycle.
func (s *Service) SyncNow(ctx context.Context) (*StatusResponse, error) {
	v4, v6, err := s.DetectIPs(ctx)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.lastCheck = time.Now()
	s.lastError = ""
	s.currentV4 = v4
	s.currentV6 = v6
	s.mu.Unlock()

	cfg := s.cfgMgr.Get()
	s.syncAllRecords(ctx, NormalizeRecords(cfg.DDNS.Records), v4, v6)

	return s.Status(), nil
}

func hasFixedValueRecords(records []config.DDNSRecord) bool {
	for _, rec := range records {
		if !UsesAutoValue(rec.Type, rec.Value) {
			return true
		}
	}
	return false
}

func (s *Service) syncAllRecords(ctx context.Context, records []config.DDNSRecord, v4, v6 string) {
	client, err := s.newCFClient()
	if err != nil {
		s.mu.Lock()
		s.lastError = "failed to create API client: " + err.Error()
		s.mu.Unlock()
		return
	}

	for _, rec := range records {
		ip, err := ResolveRecordIP(rec, v4, v6)
		if err != nil {
			s.addResult(SyncResult{
				Time:     time.Now(),
				Hostname: rec.Name,
				Type:     rec.Type,
				Success:  false,
				Message:  err.Error(),
			})
			continue
		}

		err = s.syncDNSRecord(ctx, client, rec, ip)
		if err != nil {
			s.addResult(SyncResult{
				Time:     time.Now(),
				Hostname: rec.Name,
				Type:     rec.Type,
				IP:       ip,
				Success:  false,
				Message:  err.Error(),
			})
		} else {
			s.addResult(SyncResult{
				Time:     time.Now(),
				Hostname: rec.Name,
				Type:     rec.Type,
				IP:       ip,
				Success:  true,
				Message:  "synced",
			})
		}
	}
}

func (s *Service) syncDNSRecord(ctx context.Context, client *cloudflare.API, rec config.DDNSRecord, ip string) error {
	rc := cloudflare.ZoneIdentifier(rec.ZoneID)

	// Look for existing record
	existing, _, err := client.ListDNSRecords(ctx, rc, cloudflare.ListDNSRecordsParams{
		Type: rec.Type,
		Name: rec.Name,
	})
	if err != nil {
		return fmt.Errorf("list failed: %w", err)
	}

	if len(existing) > 0 {
		// Update only if content differs
		if existing[0].Content == ip {
			return nil // already up to date
		}
		_, err = client.UpdateDNSRecord(ctx, rc, cloudflare.UpdateDNSRecordParams{
			ID:      existing[0].ID,
			Type:    rec.Type,
			Name:    rec.Name,
			Content: ip,
			Proxied: cloudflare.BoolPtr(rec.Proxied),
			TTL:     rec.TTL,
		})
		if err != nil {
			return fmt.Errorf("update failed: %w", err)
		}
	} else {
		_, err = client.CreateDNSRecord(ctx, rc, cloudflare.CreateDNSRecordParams{
			Type:    rec.Type,
			Name:    rec.Name,
			Content: ip,
			Proxied: cloudflare.BoolPtr(rec.Proxied),
			TTL:     rec.TTL,
		})
		if err != nil {
			return fmt.Errorf("create failed: %w", err)
		}
	}
	return nil
}

func (s *Service) addResult(r SyncResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results = append(s.results, r)
	if len(s.results) > 100 {
		s.results = s.results[len(s.results)-100:]
	}
}

// newCFClient creates a Cloudflare API client from the effective tunnel management config.
func (s *Service) newCFClient() (*cloudflare.API, error) {
	cfg := s.cfgMgr.Get()
	effective := cfg.EffectiveTunnelManagement()
	if strings.TrimSpace(effective.APIToken) != "" {
		return cloudflare.NewWithAPIToken(effective.APIToken)
	}
	if strings.TrimSpace(effective.APIEmail) != "" && strings.TrimSpace(effective.APIKey) != "" {
		return cloudflare.New(effective.APIKey, effective.APIEmail)
	}
	return nil, fmt.Errorf("no API credentials configured in Remote Tunnel Manager")
}

// Status returns the current DDNS service state.
func (s *Service) Status() *StatusResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg := s.cfgMgr.Get()
	st := &StatusResponse{
		Enabled:   cfg.DDNS.Enabled,
		Running:   s.running,
		CurrentV4: s.currentV4,
		CurrentV6: s.currentV6,
		LastCheck: s.lastCheck.Format(time.RFC3339),
		LastError: s.lastError,
		Records:   len(cfg.DDNS.Records),
	}
	if !s.lastCheck.IsZero() && s.running {
		interval := time.Duration(cfg.DDNS.IntervalMins) * time.Minute
		if interval < time.Minute {
			interval = time.Minute
		}
		st.NextCheck = s.lastCheck.Add(interval).Format(time.RFC3339)
	}

	// Return last 20 results
	results := s.results
	start := 0
	if len(results) > 20 {
		start = len(results) - 20
	}
	st.Results = make([]SyncResult, len(results)-start)
	copy(st.Results, results[start:])
	return st
}

// GetConfig returns the current DDNS config.
func (s *Service) GetConfig() ConfigResponse {
	cfg := s.cfgMgr.Get()
	cfg.DDNS.Records = NormalizeRecords(cfg.DDNS.Records)
	effective := cfg.EffectiveTunnelManagement()
	return ConfigResponse{
		DDNSConfig:     cfg.DDNS,
		HasCredentials: effective.APIToken != "" || (effective.APIEmail != "" && effective.APIKey != ""),
	}
}

// SaveConfig updates the DDNS config and restarts the service.
func (s *Service) SaveConfig(req SaveRequest) error {
	cfg := s.cfgMgr.Get()
	cfg.DDNS.Enabled = req.Enabled
	cfg.DDNS.IntervalMins = req.IntervalMins
	cfg.DDNS.OnlyOnChange = req.OnlyOnChange
	cfg.DDNS.MaxRetries = req.MaxRetries

	if req.IPSources != nil {
		cfg.DDNS.IPSources = req.IPSources
	}
	if req.Records != nil {
		records := NormalizeRecords(req.Records)
		for i := range records {
			value, err := ValidateRecordValue(records[i].Type, records[i].Value)
			if err != nil {
				return fmt.Errorf("record %d: %w", i, err)
			}
			records[i].Value = value
		}
		cfg.DDNS.Records = records
	}

	if cfg.DDNS.IntervalMins < 1 {
		cfg.DDNS.IntervalMins = 1
	}
	if cfg.DDNS.IntervalMins > 60 {
		cfg.DDNS.IntervalMins = 60
	}
	if cfg.DDNS.MaxRetries < 1 {
		cfg.DDNS.MaxRetries = 1
	}
	if cfg.DDNS.MaxRetries > 10 {
		cfg.DDNS.MaxRetries = 10
	}

	if err := s.cfgMgr.Save(cfg); err != nil {
		return err
	}

	go s.Restart()
	return nil
}

// ListZones fetches available zones using the tunnel management credentials.
func (s *Service) ListZones(ctx context.Context) ([]ZoneResponse, error) {
	client, err := s.newCFClient()
	if err != nil {
		return nil, err
	}

	cfg := s.cfgMgr.Get()
	effective := cfg.EffectiveTunnelManagement()

	resp, err := client.ListZonesContext(ctx, cloudflare.WithZoneFilters("", effective.AccountID, ""))
	if err != nil {
		return nil, err
	}

	zones := make([]ZoneResponse, 0, len(resp.Result))
	for _, z := range resp.Result {
		zones = append(zones, ZoneResponse{ID: z.ID, Name: z.Name, Status: z.Status})
	}
	return zones, nil
}
