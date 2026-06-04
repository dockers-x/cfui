package tunnelmgr

import (
	"cfui/internal/config"
	"cfui/internal/logger"
	"context"
	stderrors "errors"
	"fmt"
	"strings"
	"time"

	cloudflare "github.com/cloudflare/cloudflare-go"
)

var (
	ErrDisabled = stderrors.New("tunnel management is disabled")
)

type cloudflareClient interface {
	GetTunnelConfiguration(ctx context.Context, rc *cloudflare.ResourceContainer, tunnelID string) (cloudflare.TunnelConfigurationResult, error)
	UpdateTunnelConfiguration(ctx context.Context, rc *cloudflare.ResourceContainer, params cloudflare.TunnelConfigurationParams) (cloudflare.TunnelConfigurationResult, error)
	ListZonesContext(ctx context.Context, opts ...cloudflare.ReqOption) (cloudflare.ZonesResponse, error)
	VerifyAPIToken(ctx context.Context) (cloudflare.APITokenVerifyBody, error)
	GetAPIToken(ctx context.Context, tokenID string) (cloudflare.APIToken, error)
	ListDNSRecords(ctx context.Context, rc *cloudflare.ResourceContainer, params cloudflare.ListDNSRecordsParams) ([]cloudflare.DNSRecord, *cloudflare.ResultInfo, error)
	CreateDNSRecord(ctx context.Context, rc *cloudflare.ResourceContainer, params cloudflare.CreateDNSRecordParams) (cloudflare.DNSRecord, error)
	UpdateDNSRecord(ctx context.Context, rc *cloudflare.ResourceContainer, params cloudflare.UpdateDNSRecordParams) (cloudflare.DNSRecord, error)
	DeleteDNSRecord(ctx context.Context, rc *cloudflare.ResourceContainer, recordID string) error
}

type clientFactory func(config.TunnelManagementConfig) (cloudflareClient, error)

// Manager coordinates persisted settings, environment overrides, and the
// Cloudflare SDK calls used to manage a remotely hosted tunnel configuration.
type Manager struct {
	cfgMgr    *config.Manager
	newClient clientFactory
}

type SettingsRequest struct {
	Enabled   bool   `json:"enabled"`
	AccountID string `json:"account_id"`
	TunnelID  string `json:"tunnel_id"`
	APIToken  string `json:"api_token"`
	APIEmail  string `json:"api_email"`
	APIKey    string `json:"api_key"`
}

type SettingsResponse struct {
	Enabled           bool     `json:"enabled"`
	AccountID         string   `json:"account_id"`
	TunnelID          string   `json:"tunnel_id"`
	AuthMode          string   `json:"auth_mode"`
	APIEmail          string   `json:"api_email,omitempty"`
	APIToken          string   `json:"api_token,omitempty"`
	APIKey            string   `json:"api_key,omitempty"`
	APITokenSet       bool     `json:"api_token_set"`
	APIKeySet         bool     `json:"api_key_set"`
	DerivedFromToken  bool     `json:"derived_from_token"`
	DeriveTokenFailed bool     `json:"derive_token_failed"`
	EnvKeys           []string `json:"env_keys,omitempty"`
}

type ConfigurationResponse struct {
	TunnelID           string        `json:"tunnel_id"`
	Version            int           `json:"version"`
	WarpRoutingEnabled bool          `json:"warp_routing_enabled"`
	Entries            []IngressRule `json:"entries"`
}

type ZoneResponse struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type IngressRule struct {
	Index            int    `json:"index"`
	Hostname         string `json:"hostname"`
	Path             string `json:"path"`
	Service          string `json:"service"`
	NoTLSVerify      bool   `json:"no_tls_verify"`
	HTTPHostHeader   string `json:"http_host_header,omitempty"`
	OriginServerName string `json:"origin_server_name,omitempty"`
}

// PermissionCheck represents whether a specific permission is granted.
type PermissionCheck struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Granted     bool   `json:"granted"`
	Required    bool   `json:"required"`
}

// VerifyTokenRequest is the request body for the verify-token endpoint.
type VerifyTokenRequest struct {
	AuthMode string `json:"auth_mode"` // "token" or "key"
	APIToken string `json:"api_token"`
	APIEmail string `json:"api_email"`
	APIKey   string `json:"api_key"`
}

// VerifyTokenResponse is the response from the verify-token endpoint.
type VerifyTokenResponse struct {
	Valid       bool              `json:"valid"`
	TokenStatus string            `json:"token_status"`
	Permissions []PermissionCheck `json:"permissions"`
	Error       string            `json:"error,omitempty"`
}

const (
	permTunnelEdit = "Argo Tunnel (Legacy)"
	permZoneRead   = "Zone"
	permDNSEdit    = "DNS"
)

func NewManager(cfgMgr *config.Manager) *Manager {
	return &Manager{cfgMgr: cfgMgr, newClient: newSDKClient}
}

func NewManagerWithClient(cfgMgr *config.Manager, factory clientFactory) *Manager {
	return &Manager{cfgMgr: cfgMgr, newClient: factory}
}

func (m *Manager) Settings() SettingsResponse {
	cfg := m.cfgMgr.Get()
	persisted := cfg.TunnelManagement
	effective, derived, deriveFailed := effectiveWithTokenIdentity(cfg)
	return settingsResponse(effective, persisted, derived, deriveFailed)
}

func (m *Manager) SaveSettings(req SettingsRequest) error {
	cfg := m.cfgMgr.Get()
	current := cfg.TunnelManagement
	current.Enabled = req.Enabled

	accountID := strings.TrimSpace(req.AccountID)
	tunnelID := strings.TrimSpace(req.TunnelID)
	apiEmail := strings.TrimSpace(req.APIEmail)
	apiToken := strings.TrimSpace(req.APIToken)
	apiKey := strings.TrimSpace(req.APIKey)

	if req.Enabled || accountID != "" {
		current.AccountID = accountID
	}
	if req.Enabled || tunnelID != "" {
		current.TunnelID = tunnelID
	}
	if current.AccountID == "" || current.TunnelID == "" {
		if identity, err := parseTunnelToken(cfg.Token); err == nil {
			if current.AccountID == "" {
				current.AccountID = identity.AccountID
			}
			if current.TunnelID == "" {
				current.TunnelID = identity.TunnelID
			}
		}
	}
	if req.Enabled || apiEmail != "" {
		current.APIEmail = apiEmail
	}
	if apiToken != "" {
		current.APIToken = apiToken
	}
	if apiKey != "" {
		current.APIKey = apiKey
	}
	cfg.TunnelManagement = current
	return m.cfgMgr.Save(cfg)
}

func (m *Manager) VerifyPermissions(ctx context.Context, req VerifyTokenRequest) VerifyTokenResponse {
	perms := defaultPermissionChecks()

	// Fall back to stored credentials when the request has none.
	// Frontend never sees saved secrets, so blank fields mean "use what's saved".
	stored := m.cfgMgr.Get().TunnelManagement
	if strings.TrimSpace(req.APIToken) == "" && strings.TrimSpace(req.APIKey) == "" {
		if req.AuthMode == "key" {
			if req.APIEmail == "" {
				req.APIEmail = stored.APIEmail
			}
			req.APIKey = stored.APIKey
		} else {
			req.APIToken = stored.APIToken
		}
	}

	client, err := newSDKClientFromRequest(req)
	if err != nil {
		return VerifyTokenResponse{Valid: false, Permissions: perms, Error: "Failed to create API client: " + err.Error()}
	}

	// Try /user/tokens/verify to get the token ID, then fetch its
	// permission groups. Some token types (e.g. cfat_*) don't support
	// /user/tokens/verify; in that case probe permissions via actual
	// API calls using the stored account ID.
	verifyResp, err := client.VerifyAPIToken(ctx)
	if err != nil {
		return VerifyTokenResponse{
			Valid:       true,
			TokenStatus: "active",
			Permissions: probePermissions(ctx, client, req, stored.AccountID, stored.TunnelID),
		}
	}

	resp := VerifyTokenResponse{
		Valid:       verifyResp.Status == "active",
		TokenStatus: verifyResp.Status,
		Permissions: perms,
	}

	if !resp.Valid {
		return resp
	}

	if token, err := client.GetAPIToken(ctx, verifyResp.ID); err == nil {
		checkPermissionsFromToken(token.Policies, perms)
		resp.Permissions = perms
	} else {
		resp.Permissions = probePermissions(ctx, client, req, stored.AccountID, stored.TunnelID)
	}

	return resp
}

func checkPermissionsFromToken(policies []cloudflare.APITokenPolicies, checks []PermissionCheck) {
	granted := make(map[string]bool)
	for _, policy := range policies {
		if !strings.EqualFold(policy.Effect, "allow") {
			continue
		}
		for _, group := range policy.PermissionGroups {
			granted[group.Name] = true
		}
	}

	for i := range checks {
		switch checks[i].Name {
		case "account_tunnel_edit":
			checks[i].Granted = granted[permTunnelEdit]
		case "zone_read":
			checks[i].Granted = granted[permZoneRead]
		case "zone_dns_edit":
			checks[i].Granted = granted[permDNSEdit]
		}
	}
}

func probePermissions(ctx context.Context, client cloudflareClient, req VerifyTokenRequest, accountID, tunnelID string) []PermissionCheck {
	checks := defaultPermissionChecks()

	// Probe Tunnel:Edit using the stored account / tunnel IDs when
	// available, so account-scoped tokens are evaluated correctly.
	if accountID != "" && tunnelID != "" {
		cfg := config.TunnelManagementConfig{
			Enabled:   true,
			APIToken:  req.APIToken,
			APIEmail:  req.APIEmail,
			APIKey:    req.APIKey,
			AccountID: accountID,
			TunnelID:  tunnelID,
		}
		if c2, err := newSDKClient(cfg); err == nil {
			_, err = c2.GetTunnelConfiguration(ctx, cloudflare.AccountIdentifier(accountID), tunnelID)
			checks[0].Granted = !isPermissionError(err)
		}
	}

	// Probe Zone:Read by listing zones
	_, err := client.ListZonesContext(ctx)
	checks[1].Granted = !isPermissionError(err)

	// DNS:Edit cannot be probed without side effects; use Zone:Read result.
	checks[2].Granted = checks[1].Granted

	return checks
}

func defaultPermissionChecks() []PermissionCheck {
	return []PermissionCheck{
		{Name: "account_tunnel_edit", Description: "Account · Argo Tunnel (Legacy) · Edit", Required: true},
		{Name: "zone_read", Description: "Zone · Zone · Read", Required: true},
		{Name: "zone_dns_edit", Description: "Zone · DNS · Edit", Required: true},
	}
}

func isPermissionError(err error) bool {
	if err == nil {
		return false
	}
	var authErr *cloudflare.AuthenticationError
	return stderrors.As(err, &authErr)
}

func newSDKClientFromRequest(req VerifyTokenRequest) (cloudflareClient, error) {
	if req.AuthMode == "key" && strings.TrimSpace(req.APIEmail) != "" && strings.TrimSpace(req.APIKey) != "" {
		return cloudflare.New(strings.TrimSpace(req.APIKey), strings.TrimSpace(req.APIEmail))
	}
	if strings.TrimSpace(req.APIToken) != "" {
		return cloudflare.NewWithAPIToken(strings.TrimSpace(req.APIToken))
	}
	return nil, fmt.Errorf("no credentials provided")
}

func (m *Manager) Fetch(ctx context.Context) (ConfigurationResponse, error) {
	cfg, client, err := m.client()
	if err != nil {
		return ConfigurationResponse{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	result, err := client.GetTunnelConfiguration(ctx, cloudflare.AccountIdentifier(cfg.AccountID), cfg.TunnelID)
	if err != nil {
		return ConfigurationResponse{}, err
	}
	return toConfigurationResponse(result), nil
}

func (m *Manager) ListZones(ctx context.Context) ([]ZoneResponse, error) {
	cfg, client, err := m.accountClient()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	resp, err := client.ListZonesContext(ctx, cloudflare.WithZoneFilters("", cfg.AccountID, ""))
	if err != nil {
		return nil, err
	}

	zones := make([]ZoneResponse, 0, len(resp.Result))
	for _, zone := range resp.Result {
		zones = append(zones, ZoneResponse{
			ID:     zone.ID,
			Name:   zone.Name,
			Status: zone.Status,
		})
	}
	return zones, nil
}

func (m *Manager) AddEntry(ctx context.Context, entry IngressRule) (ConfigurationResponse, error) {
	resp, err := m.mutate(ctx, func(cfg *cloudflare.TunnelConfiguration) error {
		if strings.TrimSpace(entry.Service) == "" {
			return fmt.Errorf("service is required")
		}
		rule := fromIngressRule(entry, nil)
		if hasCatchAll(cfg.Ingress) {
			last := len(cfg.Ingress) - 1
			cfg.Ingress = append(cfg.Ingress[:last], append([]cloudflare.UnvalidatedIngressRule{rule}, cfg.Ingress[last:]...)...)
		} else {
			cfg.Ingress = append(cfg.Ingress, rule)
		}
		ensureCatchAll(cfg)
		return nil
	})
	if err == nil && strings.TrimSpace(entry.Hostname) != "" {
		if dnsErr := m.syncDNSForHostname(ctx, entry.Hostname); dnsErr != nil {
			logger.Sugar.Warnf("Tunnel config updated but DNS sync failed for %s: %v. Create a CNAME record manually: %s → %s.cfargotunnel.com", entry.Hostname, dnsErr, entry.Hostname, m.cfgMgr.Get().TunnelManagement.TunnelID)
		}
	}
	return resp, err
}

func (m *Manager) UpdateEntry(ctx context.Context, index int, entry IngressRule) (ConfigurationResponse, error) {
	resp, err := m.mutate(ctx, func(cfg *cloudflare.TunnelConfiguration) error {
		if index < 0 || index >= len(cfg.Ingress) {
			return fmt.Errorf("entry index %d is out of range", index)
		}
		if strings.TrimSpace(entry.Service) == "" {
			return fmt.Errorf("service is required")
		}
		cfg.Ingress[index] = fromIngressRule(entry, &cfg.Ingress[index])
		ensureCatchAll(cfg)
		return nil
	})
	if err == nil && strings.TrimSpace(entry.Hostname) != "" {
		if dnsErr := m.syncDNSForHostname(ctx, entry.Hostname); dnsErr != nil {
			logger.Sugar.Warnf("Tunnel config updated but DNS sync failed for %s: %v. Create a CNAME record manually: %s → %s.cfargotunnel.com", entry.Hostname, dnsErr, entry.Hostname, m.cfgMgr.Get().TunnelManagement.TunnelID)
		}
	}
	return resp, err
}

func (m *Manager) DeleteEntry(ctx context.Context, index int) (ConfigurationResponse, error) {
	return m.mutate(ctx, func(cfg *cloudflare.TunnelConfiguration) error {
		if index < 0 || index >= len(cfg.Ingress) {
			return fmt.Errorf("entry index %d is out of range", index)
		}
		cfg.Ingress = append(cfg.Ingress[:index], cfg.Ingress[index+1:]...)
		ensureCatchAll(cfg)
		return nil
	})
}

func (m *Manager) mutate(ctx context.Context, mutate func(*cloudflare.TunnelConfiguration) error) (ConfigurationResponse, error) {
	cfg, client, err := m.client()
	if err != nil {
		return ConfigurationResponse{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	current, err := client.GetTunnelConfiguration(ctx, cloudflare.AccountIdentifier(cfg.AccountID), cfg.TunnelID)
	if err != nil {
		return ConfigurationResponse{}, err
	}

	next := current.Config
	if err := mutate(&next); err != nil {
		return ConfigurationResponse{}, err
	}

	updated, err := client.UpdateTunnelConfiguration(ctx, cloudflare.AccountIdentifier(cfg.AccountID), cloudflare.TunnelConfigurationParams{
		TunnelID: cfg.TunnelID,
		Config:   next,
	})
	if err != nil {
		return ConfigurationResponse{}, err
	}
	return toConfigurationResponse(updated), nil
}

// syncDNSForHostname creates or updates a CNAME DNS record pointing the
// hostname to the tunnel's cfargotunnel.com subdomain.
func (m *Manager) syncDNSForHostname(ctx context.Context, hostname string) error {
	cfg, client, err := m.accountClient()
	if err != nil {
		return fmt.Errorf("failed to get client for DNS sync: %w", err)
	}

	zonesResp, err := client.ListZonesContext(ctx, cloudflare.WithZoneFilters("", cfg.AccountID, ""))
	if err != nil {
		return fmt.Errorf("failed to list zones: %w", err)
	}

	zone := findZoneForHostname(hostname, zonesResp.Result)
	if zone == nil {
		return fmt.Errorf("no matching zone found for %s", hostname)
	}

	target := fmt.Sprintf("%s.cfargotunnel.com", cfg.TunnelID)

	existing, _, err := client.ListDNSRecords(ctx, cloudflare.ZoneIdentifier(zone.ID), cloudflare.ListDNSRecordsParams{
		Type: "CNAME",
		Name: hostname,
	})
	if err != nil {
		return fmt.Errorf("failed to list DNS records: %w", err)
	}

	if len(existing) > 0 {
		_, err = client.UpdateDNSRecord(ctx, cloudflare.ZoneIdentifier(zone.ID), cloudflare.UpdateDNSRecordParams{
			ID:      existing[0].ID,
			Type:    "CNAME",
			Name:    hostname,
			Content: target,
			Proxied: cloudflare.BoolPtr(true),
			TTL:     1,
		})
	} else {
		_, err = client.CreateDNSRecord(ctx, cloudflare.ZoneIdentifier(zone.ID), cloudflare.CreateDNSRecordParams{
			Type:    "CNAME",
			Name:    hostname,
			Content: target,
			Proxied: cloudflare.BoolPtr(true),
			TTL:     1,
		})
	}
	if err != nil {
		return fmt.Errorf("failed to create/update DNS CNAME record: %w", err)
	}

	logger.Sugar.Infof("DNS CNAME record synced: %s → %s", hostname, target)
	return nil
}

func findZoneForHostname(hostname string, zones []cloudflare.Zone) *cloudflare.Zone {
	for i := range zones {
		if strings.HasSuffix(hostname, "."+zones[i].Name) || hostname == zones[i].Name {
			return &zones[i]
		}
	}
	return nil
}

func (m *Manager) client() (config.TunnelManagementConfig, cloudflareClient, error) {
	cfg, client, err := m.accountClient()
	if err != nil {
		return cfg, nil, err
	}
	if strings.TrimSpace(cfg.TunnelID) == "" {
		return cfg, nil, fmt.Errorf("tunnel id is required")
	}
	return cfg, client, nil
}

func (m *Manager) accountClient() (config.TunnelManagementConfig, cloudflareClient, error) {
	appCfg := m.cfgMgr.Get()
	cfg, _, _ := effectiveWithTokenIdentity(appCfg)
	if !cfg.Enabled {
		return cfg, nil, ErrDisabled
	}
	if strings.TrimSpace(cfg.AccountID) == "" {
		return cfg, nil, fmt.Errorf("account id is required")
	}
	if strings.TrimSpace(cfg.APIToken) == "" && (strings.TrimSpace(cfg.APIEmail) == "" || strings.TrimSpace(cfg.APIKey) == "") {
		return cfg, nil, fmt.Errorf("api token or api email + api key is required")
	}

	client, err := m.newClient(cfg)
	return cfg, client, err
}

func newSDKClient(cfg config.TunnelManagementConfig) (cloudflareClient, error) {
	if strings.TrimSpace(cfg.APIToken) != "" {
		return cloudflare.NewWithAPIToken(strings.TrimSpace(cfg.APIToken))
	}
	return cloudflare.New(strings.TrimSpace(cfg.APIKey), strings.TrimSpace(cfg.APIEmail))
}

func settingsResponse(effective, persisted config.TunnelManagementConfig, derived, deriveFailed bool) SettingsResponse {
	authMode := "none"
	if effective.APIToken != "" {
		authMode = "token"
	} else if effective.APIKey != "" || effective.APIEmail != "" {
		authMode = "key"
	}

	return SettingsResponse{
		Enabled:           effective.Enabled,
		AccountID:         effective.AccountID,
		TunnelID:          effective.TunnelID,
		AuthMode:          authMode,
		APIEmail:          effective.APIEmail,
		APIToken:          effective.APIToken,
		APIKey:            effective.APIKey,
		APITokenSet:       effective.APIToken != "",
		APIKeySet:         effective.APIKey != "",
		DerivedFromToken:  derived,
		DeriveTokenFailed: deriveFailed,
		EnvKeys:           envOverrideKeys(effective, persisted),
	}
}

type tokenIdentity struct {
	AccountID string
	TunnelID  string
}

func effectiveWithTokenIdentity(cfg config.Config) (config.TunnelManagementConfig, bool, bool) {
	effective := cfg.EffectiveTunnelManagement()
	if effective.AccountID != "" && effective.TunnelID != "" {
		return effective, false, false
	}

	identity, err := parseTunnelToken(cfg.Token)
	if err != nil {
		return effective, false, cfg.Token != ""
	}

	derived := false
	if effective.AccountID == "" {
		effective.AccountID = identity.AccountID
		derived = true
	}
	if effective.TunnelID == "" {
		effective.TunnelID = identity.TunnelID
		derived = true
	}
	return effective, derived, false
}

func parseTunnelToken(token string) (tokenIdentity, error) {
	identity, err := config.ParseTunnelTokenIdentity(token)
	if err != nil {
		return tokenIdentity{}, err
	}
	return tokenIdentity{
		AccountID: identity.AccountID,
		TunnelID:  identity.TunnelID,
	}, nil
}

func envOverrideKeys(effective, persisted config.TunnelManagementConfig) []string {
	var keys []string
	if effective.Enabled != persisted.Enabled {
		keys = append(keys, "enabled")
	}
	if effective.AccountID != persisted.AccountID {
		keys = append(keys, "account_id")
	}
	if effective.TunnelID != persisted.TunnelID {
		keys = append(keys, "tunnel_id")
	}
	if effective.APIToken != persisted.APIToken && effective.APIToken != "" {
		keys = append(keys, "api_token")
	}
	if effective.APIEmail != persisted.APIEmail && effective.APIEmail != "" {
		keys = append(keys, "api_email")
	}
	if effective.APIKey != persisted.APIKey && effective.APIKey != "" {
		keys = append(keys, "api_key")
	}
	return keys
}

func toConfigurationResponse(result cloudflare.TunnelConfigurationResult) ConfigurationResponse {
	entries := make([]IngressRule, 0, len(result.Config.Ingress))
	for i, rule := range result.Config.Ingress {
		entry := IngressRule{
			Index:    i,
			Hostname: rule.Hostname,
			Path:     rule.Path,
			Service:  rule.Service,
		}
		if rule.OriginRequest != nil {
			if rule.OriginRequest.NoTLSVerify != nil {
				entry.NoTLSVerify = *rule.OriginRequest.NoTLSVerify
			}
			if rule.OriginRequest.HTTPHostHeader != nil {
				entry.HTTPHostHeader = *rule.OriginRequest.HTTPHostHeader
			}
			if rule.OriginRequest.OriginServerName != nil {
				entry.OriginServerName = *rule.OriginRequest.OriginServerName
			}
		}
		entries = append(entries, entry)
	}

	warp := false
	if result.Config.WarpRouting != nil {
		warp = result.Config.WarpRouting.Enabled
	}

	return ConfigurationResponse{
		TunnelID:           result.TunnelID,
		Version:            result.Version,
		WarpRoutingEnabled: warp,
		Entries:            entries,
	}
}

func fromIngressRule(entry IngressRule, existing *cloudflare.UnvalidatedIngressRule) cloudflare.UnvalidatedIngressRule {
	var rule cloudflare.UnvalidatedIngressRule
	if existing != nil {
		rule = *existing
	}
	rule.Hostname = strings.TrimSpace(entry.Hostname)
	rule.Path = strings.TrimSpace(entry.Path)
	rule.Service = strings.TrimSpace(entry.Service)

	origin := rule.OriginRequest
	if origin == nil {
		origin = &cloudflare.OriginRequestConfig{}
	}
	origin.NoTLSVerify = cloudflare.BoolPtr(entry.NoTLSVerify)
	origin.HTTPHostHeader = stringPtrOrNil(entry.HTTPHostHeader)
	origin.OriginServerName = stringPtrOrNil(entry.OriginServerName)
	rule.OriginRequest = origin
	return rule
}

func stringPtrOrNil(v string) *string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	return &v
}

func ensureCatchAll(cfg *cloudflare.TunnelConfiguration) {
	if hasCatchAll(cfg.Ingress) {
		return
	}
	cfg.Ingress = append(cfg.Ingress, cloudflare.UnvalidatedIngressRule{Service: "http_status:404"})
}

func hasCatchAll(rules []cloudflare.UnvalidatedIngressRule) bool {
	if len(rules) == 0 {
		return false
	}
	last := rules[len(rules)-1]
	return strings.TrimSpace(last.Hostname) == "" && strings.TrimSpace(last.Path) == "" && strings.TrimSpace(last.Service) != ""
}
