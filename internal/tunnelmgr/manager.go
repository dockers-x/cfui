package tunnelmgr

import (
	"cfui/internal/config"
	"cfui/internal/logger"
	"context"
	stderrors "errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	cloudflare "github.com/cloudflare/cloudflare-go"
)

var (
	ErrDisabled = stderrors.New("tunnel management is disabled")
)

type cloudflareClient interface {
	GetTunnel(ctx context.Context, rc *cloudflare.ResourceContainer, tunnelID string) (cloudflare.Tunnel, error)
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
	TunnelKey         string   `json:"tunnel_key,omitempty"`
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
	TunnelName         string        `json:"tunnel_name,omitempty"`
	Version            int           `json:"version"`
	WarpRoutingEnabled bool          `json:"warp_routing_enabled"`
	Entries            []IngressRule `json:"entries"`
}

type TunnelDetailsResponse struct {
	TunnelKey string `json:"tunnel_key,omitempty"`
	TunnelID  string `json:"tunnel_id"`
	Name      string `json:"name"`
	Status    string `json:"status,omitempty"`
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
	Comment          string `json:"comment,omitempty"`
	NoTLSVerify      bool   `json:"no_tls_verify"`
	HTTPHostHeader   string `json:"http_host_header,omitempty"`
	OriginServerName string `json:"origin_server_name,omitempty"`
}

type S3WebDAVTunnelStatus struct {
	Hostname string `json:"hostname"`
	Service  string `json:"service"`
	Status   string `json:"status"`
	Message  string `json:"message"`
	Synced   bool   `json:"synced"`
}

// PermissionCheck represents whether a specific permission is granted.
type PermissionCheck struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Granted     bool   `json:"granted"`
	Status      string `json:"status"`
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
	TokenActive bool              `json:"token_active"`
	TokenStatus string            `json:"token_status"`
	Permissions []PermissionCheck `json:"permissions"`
	Error       string            `json:"error,omitempty"`
}

const (
	permissionGranted = "granted"
	permissionDenied  = "denied"
	permissionUnknown = "unknown"
	permTunnelEdit    = "Cloudflare Tunnel Write"
	permZoneRead      = "Zone Read"
	permDNSEdit       = "DNS Write"
)

var (
	tunnelWritePermissionNames = []string{
		"Cloudflare One Connectors Write",
		"Cloudflare One Connectors Edit",
		"Cloudflare One Connector: cloudflared Write",
		"Cloudflare One Connector: cloudflared Edit",
		permTunnelEdit,
		"Cloudflare Tunnel Edit",
		"Argo Tunnel (Legacy)",
	}
	zoneReadPermissionNames = []string{permZoneRead, "Zone"}
	dnsWritePermissionNames = []string{permDNSEdit, "DNS Edit", "DNS"}
)

const (
	S3WebDAVTunnelCommentMarker     = "cfui:s3-webdav"
	S3WebDAVTunnelStatusSynced      = "synced"
	S3WebDAVTunnelStatusMissing     = "missing"
	S3WebDAVTunnelStatusUnavailable = "unavailable"
	S3WebDAVTunnelStatusError       = "error"
)

func NewManager(cfgMgr *config.Manager) *Manager {
	return &Manager{cfgMgr: cfgMgr, newClient: newSDKClient}
}

func NewManagerWithClient(cfgMgr *config.Manager, factory clientFactory) *Manager {
	return &Manager{cfgMgr: cfgMgr, newClient: factory}
}

func (m *Manager) Settings() SettingsResponse {
	return m.SettingsFor("")
}

func (m *Manager) SettingsFor(tunnelKey string) SettingsResponse {
	cfg := m.cfgMgr.Get()
	persisted := cfg.TunnelManagement
	effective, derived, deriveFailed := effectiveWithTokenIdentityFor(cfg, tunnelKey)
	resp := settingsResponse(effective, persisted, derived, deriveFailed)
	if tunnel, ok := cfg.TunnelProfile(tunnelKey); ok {
		resp.TunnelKey = tunnel.Key
	}
	return resp
}

func (m *Manager) SaveSettings(req SettingsRequest) error {
	return m.saveSettingsFor("", req, true)
}

func (m *Manager) SaveSettingsFor(tunnelKey string, req SettingsRequest) error {
	return m.saveSettingsFor(tunnelKey, req, false)
}

func (m *Manager) saveSettingsFor(tunnelKey string, req SettingsRequest, legacyGlobalDisable bool) error {
	cfg := m.cfgMgr.Get()
	current := cfg.TunnelManagement
	profileRequest := strings.TrimSpace(tunnelKey) != ""
	selectedTunnel, ok := cfg.TunnelProfile(tunnelKey)
	if profileRequest && !ok {
		return fmt.Errorf("tunnel profile %q not found", tunnelKey)
	}
	if !profileRequest && (req.Enabled || legacyGlobalDisable) {
		current.Enabled = req.Enabled
	}

	accountID := strings.TrimSpace(req.AccountID)
	tunnelID := strings.TrimSpace(req.TunnelID)
	apiEmail := strings.TrimSpace(req.APIEmail)
	apiToken := strings.TrimSpace(req.APIToken)
	apiKey := strings.TrimSpace(req.APIKey)

	if !profileRequest && (req.Enabled || accountID != "") {
		current.AccountID = accountID
	}
	if !profileRequest && (req.Enabled || tunnelID != "") {
		current.TunnelID = tunnelID
	}
	if accountID == "" && selectedTunnel.AccountID != "" {
		accountID = selectedTunnel.AccountID
	}
	if tunnelID == "" && selectedTunnel.TunnelID != "" {
		tunnelID = selectedTunnel.TunnelID
	}
	if accountID == "" || tunnelID == "" {
		token := selectedTunnel.Token
		if token == "" {
			token = cfg.Token
		}
		if identity, err := parseTunnelToken(token); err == nil {
			if accountID == "" {
				accountID = identity.AccountID
			}
			if tunnelID == "" {
				tunnelID = identity.TunnelID
			}
		}
	}
	if !profileRequest && (current.AccountID == "" || current.TunnelID == "") {
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
	cfg = saveProfileTunnelManagementFields(cfg, tunnelKey, req.Enabled, accountID, tunnelID)
	return m.cfgMgr.Save(cfg)
}

func (m *Manager) VerifyPermissions(ctx context.Context, req VerifyTokenRequest) VerifyTokenResponse {
	return m.VerifyPermissionsFor(ctx, "", req)
}

func (m *Manager) VerifyPermissionsFor(ctx context.Context, tunnelKey string, req VerifyTokenRequest) VerifyTokenResponse {
	perms := defaultPermissionChecks()

	// Fall back to stored credentials when the request has none.
	// Frontend never sees saved secrets, so blank fields mean "use what's saved".
	appCfg := m.cfgMgr.Get()
	stored, _, _ := effectiveWithTokenIdentityFor(appCfg, tunnelKey)
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

	client, err := m.newClient(config.TunnelManagementConfig{
		Enabled:   true,
		AccountID: stored.AccountID,
		TunnelID:  stored.TunnelID,
		APIToken:  strings.TrimSpace(req.APIToken),
		APIEmail:  strings.TrimSpace(req.APIEmail),
		APIKey:    strings.TrimSpace(req.APIKey),
	})
	if err != nil {
		return VerifyTokenResponse{Valid: false, Permissions: perms, Error: "Failed to create API client: " + err.Error()}
	}

	// Try /user/tokens/verify to get the token ID, then fetch its
	// permission groups. Some token types (e.g. cfat_*) don't support
	// /user/tokens/verify; in that case probe permissions via actual
	// API calls using the stored account ID.
	verifyResp, err := client.VerifyAPIToken(ctx)
	if err != nil {
		probed, authenticated := probePermissions(ctx, client, req.AuthMode, stored.AccountID, stored.TunnelID)
		return permissionVerificationResponse(authenticated, "unknown", probed)
	}

	tokenActive := strings.EqualFold(verifyResp.Status, "active")
	resp := permissionVerificationResponse(tokenActive, verifyResp.Status, perms)

	if !resp.TokenActive {
		return resp
	}

	if token, err := client.GetAPIToken(ctx, verifyResp.ID); err == nil {
		checkPermissionsFromToken(token.Policies, perms, stored.AccountID, configuredDDNSZoneIDs(appCfg))
		resp.Permissions = perms
	} else {
		resp.Permissions, _ = probePermissions(ctx, client, req.AuthMode, stored.AccountID, stored.TunnelID)
	}
	resp.Valid = allRequiredPermissionsGranted(resp.Permissions)

	return resp
}

func checkPermissionsFromToken(policies []cloudflare.APITokenPolicies, checks []PermissionCheck, accountID string, zoneIDs []string) {
	for i := range checks {
		switch checks[i].Name {
		case "account_tunnel_edit":
			setPermissionStatus(&checks[i], permissionStatusForPolicies(policies, tunnelWritePermissionNames, "account", []string{accountID}))
		case "zone_read":
			setPermissionStatus(&checks[i], permissionStatusForPolicies(policies, zoneReadPermissionNames, "zone", zoneIDs))
		case "zone_dns_edit":
			setPermissionStatus(&checks[i], permissionStatusForPolicies(policies, dnsWritePermissionNames, "zone", zoneIDs))
		}
	}
}

func permissionStatusForPolicies(policies []cloudflare.APITokenPolicies, names []string, resourceKind string, targets []string) string {
	wantedNames := make(map[string]bool, len(names))
	for _, name := range names {
		wantedNames[strings.ToLower(strings.TrimSpace(name))] = true
	}
	wantedTargets := make(map[string]bool, len(targets))
	for _, target := range targets {
		if target = strings.TrimSpace(target); target != "" {
			wantedTargets[target] = true
		}
	}
	hasPermission := false
	hasRecognizedResource := false
	coveredTargets := make(map[string]bool, len(wantedTargets))
	wildcard := false
	for _, policy := range policies {
		if !strings.EqualFold(policy.Effect, "allow") || !policyHasPermission(policy, wantedNames) {
			continue
		}
		hasPermission = true
		for resource := range policy.Resources {
			recognized, all, target := parsePermissionResource(resourceKind, resource)
			if !recognized {
				continue
			}
			hasRecognizedResource = true
			if all {
				wildcard = true
				continue
			}
			if len(wantedTargets) == 0 || wantedTargets[target] {
				coveredTargets[target] = true
			}
		}
	}
	if !hasPermission {
		return permissionDenied
	}
	if !hasRecognizedResource {
		return permissionUnknown
	}
	if wildcard || len(wantedTargets) == 0 {
		return permissionGranted
	}
	for target := range wantedTargets {
		if !coveredTargets[target] {
			return permissionDenied
		}
	}
	return permissionGranted
}

func policyHasPermission(policy cloudflare.APITokenPolicies, wanted map[string]bool) bool {
	for _, group := range policy.PermissionGroups {
		if wanted[strings.ToLower(strings.TrimSpace(group.Name))] {
			return true
		}
	}
	return false
}

func parsePermissionResource(kind, resource string) (recognized, wildcard bool, target string) {
	resource = strings.TrimSpace(resource)
	switch kind {
	case "account":
		const prefix = "com.cloudflare.api.account."
		if !strings.HasPrefix(resource, prefix) {
			return false, false, ""
		}
		target = strings.TrimPrefix(resource, prefix)
		if target == "" || strings.HasPrefix(target, "zone.") {
			return false, false, ""
		}
		if target == "*" {
			return true, true, ""
		}
		if index := strings.IndexByte(target, '.'); index >= 0 {
			target = target[:index]
		}
		return true, false, target
	case "zone":
		const prefix = "com.cloudflare.api.account.zone."
		if !strings.HasPrefix(resource, prefix) {
			return false, false, ""
		}
		target = strings.TrimPrefix(resource, prefix)
		if target == "" {
			return false, false, ""
		}
		return true, target == "*", target
	default:
		return false, false, ""
	}
}

func configuredDDNSZoneIDs(cfg config.Config) []string {
	seen := make(map[string]bool)
	zoneIDs := make([]string, 0, len(cfg.DDNS.Records))
	for _, record := range cfg.DDNS.Records {
		zoneID := strings.TrimSpace(record.ZoneID)
		if zoneID == "" || seen[zoneID] {
			continue
		}
		seen[zoneID] = true
		zoneIDs = append(zoneIDs, zoneID)
	}
	return zoneIDs
}

func probePermissions(ctx context.Context, client cloudflareClient, authMode, accountID, tunnelID string) ([]PermissionCheck, bool) {
	checks := defaultPermissionChecks()
	authenticated := false
	globalAPIKey := strings.EqualFold(strings.TrimSpace(authMode), "key")

	// Reading the current tunnel configuration proves account/tunnel access,
	// but it does not prove write access for an API token. A successful Global
	// API Key request is different because that credential is not scope-limited.
	if accountID != "" && tunnelID != "" {
		_, err := client.GetTunnelConfiguration(ctx, cloudflare.AccountIdentifier(accountID), tunnelID)
		switch {
		case err == nil:
			authenticated = true
			if globalAPIKey {
				setPermissionStatus(&checks[0], permissionGranted)
			}
		case isPermissionError(err):
			setPermissionStatus(&checks[0], permissionDenied)
		}
	}

	// Probe Zone:Read by listing zones
	_, err := client.ListZonesContext(ctx)
	switch {
	case err == nil:
		authenticated = true
		setPermissionStatus(&checks[1], permissionGranted)
		if globalAPIKey {
			setPermissionStatus(&checks[2], permissionGranted)
		}
	case isPermissionError(err):
		setPermissionStatus(&checks[1], permissionDenied)
	}

	// DNS write access cannot be verified without mutating a DNS record. Keep
	// it unknown for scoped API tokens instead of inferring it from Zone Read.
	return checks, authenticated
}

func defaultPermissionChecks() []PermissionCheck {
	return []PermissionCheck{
		{Name: "account_tunnel_edit", Description: "Account · Cloudflare Tunnel · Edit", Status: permissionUnknown, Required: true},
		{Name: "zone_read", Description: "Zone · Zone · Read", Status: permissionUnknown, Required: true},
		{Name: "zone_dns_edit", Description: "Zone · DNS · Edit", Status: permissionUnknown, Required: true},
	}
}

func permissionVerificationResponse(tokenActive bool, tokenStatus string, permissions []PermissionCheck) VerifyTokenResponse {
	return VerifyTokenResponse{
		Valid:       tokenActive && allRequiredPermissionsGranted(permissions),
		TokenActive: tokenActive,
		TokenStatus: tokenStatus,
		Permissions: permissions,
	}
}

func allRequiredPermissionsGranted(checks []PermissionCheck) bool {
	for _, check := range checks {
		if check.Required && check.Status != permissionGranted {
			return false
		}
	}
	return true
}

func setPermissionStatus(check *PermissionCheck, status string) {
	check.Status = status
	check.Granted = status == permissionGranted
}

func isPermissionError(err error) bool {
	if err == nil {
		return false
	}
	var authErr *cloudflare.AuthenticationError
	return stderrors.As(err, &authErr)
}

func (m *Manager) Fetch(ctx context.Context) (ConfigurationResponse, error) {
	return m.FetchFor(ctx, "")
}

func (m *Manager) FetchFor(ctx context.Context, tunnelKey string) (ConfigurationResponse, error) {
	cfg, client, err := m.clientFor(tunnelKey)
	if err != nil {
		return ConfigurationResponse{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	result, err := client.GetTunnelConfiguration(ctx, cloudflare.AccountIdentifier(cfg.AccountID), cfg.TunnelID)
	if err != nil {
		return ConfigurationResponse{}, err
	}
	resp := toConfigurationResponse(result)
	resp.TunnelName = m.refreshTunnelProfileName(ctx, tunnelKey, cfg, client)
	return resp, nil
}

func (m *Manager) ListZones(ctx context.Context) ([]ZoneResponse, error) {
	return m.ListZonesFor(ctx, "")
}

func (m *Manager) FetchTunnelDetailsFor(ctx context.Context, tunnelKey string) (TunnelDetailsResponse, error) {
	cfg, client, err := m.clientFor(tunnelKey)
	if err != nil {
		return TunnelDetailsResponse{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	tunnel, err := client.GetTunnel(ctx, cloudflare.AccountIdentifier(cfg.AccountID), cfg.TunnelID)
	if err != nil {
		return TunnelDetailsResponse{}, err
	}
	name := strings.TrimSpace(tunnel.Name)
	if name != "" {
		m.applyFetchedTunnelName(tunnelKey, cfg.TunnelID, name)
	}
	resp := TunnelDetailsResponse{
		TunnelID: cfg.TunnelID,
		Name:     name,
		Status:   strings.TrimSpace(tunnel.Status),
	}
	if profile, ok := m.cfgMgr.Get().TunnelProfile(tunnelKey); ok {
		resp.TunnelKey = profile.Key
	}
	return resp, nil
}

func (m *Manager) ListZonesFor(ctx context.Context, tunnelKey string) ([]ZoneResponse, error) {
	cfg, client, err := m.accountClientFor(tunnelKey)
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
	return m.AddEntryFor(ctx, "", entry)
}

func (m *Manager) AddEntryFor(ctx context.Context, tunnelKey string, entry IngressRule) (ConfigurationResponse, error) {
	tunnelCfg, _, cfgErr := m.clientFor(tunnelKey)
	if cfgErr != nil {
		return ConfigurationResponse{}, cfgErr
	}
	resp, err := m.mutateFor(ctx, tunnelKey, func(cfg *cloudflare.TunnelConfiguration) error {
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
		if dnsErr := m.syncDNSForHostnameWithCommentFor(ctx, tunnelKey, entry.Hostname, entry.Comment); dnsErr != nil {
			logger.Sugar.Warnf("Tunnel config updated but DNS sync failed for %s: %v. Create a CNAME record manually: %s → %s.cfargotunnel.com", entry.Hostname, dnsErr, entry.Hostname, tunnelCfg.TunnelID)
		}
	}
	return resp, err
}

func (m *Manager) UpdateEntry(ctx context.Context, index int, entry IngressRule) (ConfigurationResponse, error) {
	return m.UpdateEntryFor(ctx, "", index, entry)
}

func (m *Manager) UpdateEntryFor(ctx context.Context, tunnelKey string, index int, entry IngressRule) (ConfigurationResponse, error) {
	tunnelCfg, _, cfgErr := m.clientFor(tunnelKey)
	if cfgErr != nil {
		return ConfigurationResponse{}, cfgErr
	}
	resp, err := m.mutateFor(ctx, tunnelKey, func(cfg *cloudflare.TunnelConfiguration) error {
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
		if dnsErr := m.syncDNSForHostnameWithCommentFor(ctx, tunnelKey, entry.Hostname, entry.Comment); dnsErr != nil {
			logger.Sugar.Warnf("Tunnel config updated but DNS sync failed for %s: %v. Create a CNAME record manually: %s → %s.cfargotunnel.com", entry.Hostname, dnsErr, entry.Hostname, tunnelCfg.TunnelID)
		}
	}
	return resp, err
}

func (m *Manager) DeleteEntry(ctx context.Context, index int) (ConfigurationResponse, error) {
	return m.DeleteEntryFor(ctx, "", index)
}

func (m *Manager) DeleteEntryFor(ctx context.Context, tunnelKey string, index int) (ConfigurationResponse, error) {
	return m.mutateFor(ctx, tunnelKey, func(cfg *cloudflare.TunnelConfiguration) error {
		if index < 0 || index >= len(cfg.Ingress) {
			return fmt.Errorf("entry index %d is out of range", index)
		}
		cfg.Ingress = append(cfg.Ingress[:index], cfg.Ingress[index+1:]...)
		ensureCatchAll(cfg)
		return nil
	})
}

func (m *Manager) ReorderEntries(ctx context.Context, order []int) (ConfigurationResponse, error) {
	return m.ReorderEntriesFor(ctx, "", order)
}

func (m *Manager) ReorderEntriesFor(ctx context.Context, tunnelKey string, order []int) (ConfigurationResponse, error) {
	return m.mutateFor(ctx, tunnelKey, func(cfg *cloudflare.TunnelConfiguration) error {
		if len(order) != len(cfg.Ingress) {
			return fmt.Errorf("entry order length %d does not match current rule count %d", len(order), len(cfg.Ingress))
		}
		if len(order) == 0 {
			return nil
		}
		if hasCatchAll(cfg.Ingress) && order[len(order)-1] != len(cfg.Ingress)-1 {
			return fmt.Errorf("catch-all rule must remain last")
		}
		next := make([]cloudflare.UnvalidatedIngressRule, 0, len(cfg.Ingress))
		seen := make([]bool, len(cfg.Ingress))
		for _, index := range order {
			if index < 0 || index >= len(cfg.Ingress) {
				return fmt.Errorf("entry index %d is out of range", index)
			}
			if seen[index] {
				return fmt.Errorf("entry index %d appears more than once", index)
			}
			seen[index] = true
			next = append(next, cfg.Ingress[index])
		}
		cfg.Ingress = next
		ensureCatchAll(cfg)
		return nil
	})
}

func (m *Manager) CheckS3WebDAVHostname(ctx context.Context, hostname, service string) S3WebDAVTunnelStatus {
	return m.CheckS3WebDAVHostnameFor(ctx, "", hostname, service)
}

func (m *Manager) CheckS3WebDAVHostnameFor(ctx context.Context, tunnelKey, hostname, service string) S3WebDAVTunnelStatus {
	hostname = normalizeHostname(hostname)
	service = strings.TrimSpace(service)
	status := S3WebDAVTunnelStatus{
		Hostname: hostname,
		Service:  service,
		Status:   S3WebDAVTunnelStatusMissing,
		Message:  "Tunnel binding is missing.",
	}
	if hostname == "" || service == "" {
		return status
	}

	cfg, client, err := m.clientFor(tunnelKey)
	if err != nil {
		status.Status = S3WebDAVTunnelStatusError
		status.Message = err.Error()
		if stderrors.Is(err, ErrDisabled) {
			status.Status = S3WebDAVTunnelStatusUnavailable
			status.Message = "Tunnel Manager is disabled."
		}
		return status
	}

	current, err := client.GetTunnelConfiguration(ctx, cloudflare.AccountIdentifier(cfg.AccountID), cfg.TunnelID)
	if err != nil {
		status.Status = S3WebDAVTunnelStatusError
		status.Message = err.Error()
		return status
	}

	hasRule := false
	for _, rule := range current.Config.Ingress {
		if strings.EqualFold(strings.TrimSpace(rule.Hostname), hostname) &&
			strings.TrimSpace(rule.Path) == "" &&
			strings.TrimSpace(rule.Service) == service {
			hasRule = true
			break
		}
	}

	hasDNSBinding, err := m.s3WebDAVDNSBindingExistsFor(ctx, tunnelKey, hostname)
	if err != nil {
		status.Status = S3WebDAVTunnelStatusError
		status.Message = err.Error()
		return status
	}
	if hasRule && hasDNSBinding {
		status.Status = S3WebDAVTunnelStatusSynced
		status.Message = "Tunnel binding is synced."
		status.Synced = true
		return status
	}
	switch {
	case !hasRule && !hasDNSBinding:
		status.Message = "Tunnel rule and DNS marker are missing."
	case !hasRule:
		status.Message = "Tunnel rule is missing."
	case !hasDNSBinding:
		status.Message = "DNS marker is missing."
	}
	return status
}

func (m *Manager) mutate(ctx context.Context, mutate func(*cloudflare.TunnelConfiguration) error) (ConfigurationResponse, error) {
	return m.mutateFor(ctx, "", mutate)
}

func (m *Manager) mutateFor(ctx context.Context, tunnelKey string, mutate func(*cloudflare.TunnelConfiguration) error) (ConfigurationResponse, error) {
	cfg, client, err := m.clientFor(tunnelKey)
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
	return m.syncDNSForHostnameWithComment(ctx, hostname, "")
}

func (m *Manager) syncDNSForHostnameWithComment(ctx context.Context, hostname, comment string) error {
	return m.syncDNSForHostnameWithCommentFor(ctx, "", hostname, comment)
}

func (m *Manager) syncDNSForHostnameWithCommentFor(ctx context.Context, tunnelKey, hostname, comment string) error {
	cfg, client, err := m.accountClientFor(tunnelKey)
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
		params := cloudflare.UpdateDNSRecordParams{
			ID:      existing[0].ID,
			Type:    "CNAME",
			Name:    hostname,
			Content: target,
			Proxied: cloudflare.BoolPtr(true),
			TTL:     1,
		}
		if strings.TrimSpace(comment) != "" {
			params.Comment = cloudflare.StringPtr(strings.TrimSpace(comment))
		}
		_, err = client.UpdateDNSRecord(ctx, cloudflare.ZoneIdentifier(zone.ID), params)
	} else {
		_, err = client.CreateDNSRecord(ctx, cloudflare.ZoneIdentifier(zone.ID), cloudflare.CreateDNSRecordParams{
			Type:    "CNAME",
			Name:    hostname,
			Content: target,
			Proxied: cloudflare.BoolPtr(true),
			TTL:     1,
			Comment: strings.TrimSpace(comment),
		})
	}
	if err != nil {
		return fmt.Errorf("failed to create/update DNS CNAME record: %w", err)
	}

	logger.Sugar.Infof("DNS CNAME record synced: %s → %s", hostname, target)
	return nil
}

func (m *Manager) s3WebDAVDNSBindingExists(ctx context.Context, hostname string) (bool, error) {
	return m.s3WebDAVDNSBindingExistsFor(ctx, "", hostname)
}

func (m *Manager) s3WebDAVDNSBindingExistsFor(ctx context.Context, tunnelKey, hostname string) (bool, error) {
	cfg, client, err := m.accountClientFor(tunnelKey)
	if err != nil {
		return false, err
	}

	zonesResp, err := client.ListZonesContext(ctx, cloudflare.WithZoneFilters("", cfg.AccountID, ""))
	if err != nil {
		return false, fmt.Errorf("failed to list zones: %w", err)
	}

	zone := findZoneForHostname(hostname, zonesResp.Result)
	if zone == nil {
		return false, fmt.Errorf("no matching zone found for %s", hostname)
	}

	existing, _, err := client.ListDNSRecords(ctx, cloudflare.ZoneIdentifier(zone.ID), cloudflare.ListDNSRecordsParams{
		Type: "CNAME",
		Name: hostname,
	})
	if err != nil {
		return false, fmt.Errorf("failed to list DNS records: %w", err)
	}
	target := fmt.Sprintf("%s.cfargotunnel.com", cfg.TunnelID)
	for _, record := range existing {
		if strings.EqualFold(strings.TrimSpace(record.Content), target) && strings.Contains(record.Comment, S3WebDAVTunnelCommentMarker) {
			return true, nil
		}
	}
	return false, nil
}

func S3WebDAVTunnelComment(hostname, service string) string {
	return fmt.Sprintf("%s hostname=%s service=%s", S3WebDAVTunnelCommentMarker, normalizeHostname(hostname), strings.TrimSpace(service))
}

func normalizeHostname(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if u, err := url.Parse(value); err == nil && u.Host != "" {
		value = u.Host
	}
	value = strings.Trim(value, "/")
	if host, _, ok := strings.Cut(value, ":"); ok {
		value = host
	}
	return strings.ToLower(strings.TrimSpace(value))
}

func findZoneForHostname(hostname string, zones []cloudflare.Zone) *cloudflare.Zone {
	var matched *cloudflare.Zone
	matchedLength := -1
	for i := range zones {
		zoneName := normalizeHostname(zones[i].Name)
		if zoneName != "" && (strings.HasSuffix(hostname, "."+zoneName) || hostname == zoneName) && len(zoneName) > matchedLength {
			matched = &zones[i]
			matchedLength = len(zoneName)
		}
	}
	return matched
}

func (m *Manager) client() (config.TunnelManagementConfig, cloudflareClient, error) {
	return m.clientFor("")
}

func (m *Manager) clientFor(tunnelKey string) (config.TunnelManagementConfig, cloudflareClient, error) {
	cfg, client, err := m.accountClientFor(tunnelKey)
	if err != nil {
		return cfg, nil, err
	}
	if strings.TrimSpace(cfg.TunnelID) == "" {
		return cfg, nil, fmt.Errorf("tunnel id is required")
	}
	return cfg, client, nil
}

func (m *Manager) accountClient() (config.TunnelManagementConfig, cloudflareClient, error) {
	return m.accountClientFor("")
}

func (m *Manager) accountClientFor(tunnelKey string) (config.TunnelManagementConfig, cloudflareClient, error) {
	appCfg := m.cfgMgr.Get()
	if strings.TrimSpace(tunnelKey) != "" {
		if _, ok := appCfg.TunnelProfile(tunnelKey); !ok {
			return config.TunnelManagementConfig{}, nil, fmt.Errorf("tunnel profile %q not found", tunnelKey)
		}
	}
	cfg, _, _ := effectiveWithTokenIdentityFor(appCfg, tunnelKey)
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

func (m *Manager) refreshTunnelProfileName(ctx context.Context, tunnelKey string, cfg config.TunnelManagementConfig, client cloudflareClient) string {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	tunnel, err := client.GetTunnel(ctx, cloudflare.AccountIdentifier(cfg.AccountID), cfg.TunnelID)
	if err != nil {
		if logger.Sugar != nil {
			logger.Sugar.Debugf("Failed to fetch Cloudflare tunnel name for %s: %v", cfg.TunnelID, err)
		}
		return ""
	}
	name := strings.TrimSpace(tunnel.Name)
	if name == "" {
		return ""
	}
	m.applyFetchedTunnelName(tunnelKey, cfg.TunnelID, name)
	return name
}

func (m *Manager) applyFetchedTunnelName(tunnelKey, tunnelID, name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	cfg := m.cfgMgr.Get()
	target := cfg.ActiveTunnelKey
	if tunnel, ok := cfg.TunnelProfile(tunnelKey); ok {
		target = tunnel.Key
	}
	for i, tunnel := range cfg.Tunnels {
		if tunnel.Key != target {
			continue
		}
		if tunnel.TunnelID != "" && tunnelID != "" && tunnel.TunnelID != tunnelID {
			return
		}
		if !shouldReplaceTunnelProfileName(tunnel, i) {
			return
		}
		tunnel.Name = name
		if _, err := m.cfgMgr.SaveTunnelProfile(tunnel.Key, tunnel); err != nil && logger.Sugar != nil {
			logger.Sugar.Warnf("Failed to save Cloudflare tunnel name for profile %s: %v", tunnel.Key, err)
		}
		return
	}
}

func shouldReplaceTunnelProfileName(tunnel config.TunnelProfileConfig, index int) bool {
	name := strings.TrimSpace(tunnel.Name)
	if name == "" || name == tunnel.Key || name == "Default Tunnel" || name == "Tunnel" {
		return true
	}
	if name == fmt.Sprintf("Tunnel %d", index+1) {
		return true
	}
	return false
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
	return effectiveWithTokenIdentityFor(cfg, "")
}

func effectiveWithTokenIdentityFor(cfg config.Config, tunnelKey string) (config.TunnelManagementConfig, bool, bool) {
	effective := cfg.EffectiveTunnelManagementFor(tunnelKey)
	if effective.AccountID != "" && effective.TunnelID != "" {
		return effective, false, false
	}

	token := cfg.Token
	if tunnel, ok := cfg.TunnelProfile(tunnelKey); ok && strings.TrimSpace(tunnel.Token) != "" {
		token = tunnel.Token
	}
	identity, err := parseTunnelToken(token)
	if err != nil {
		return effective, false, token != ""
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

func saveProfileTunnelManagementFields(cfg config.Config, tunnelKey string, enabled bool, accountID, tunnelID string) config.Config {
	target := cfg.ActiveTunnelKey
	if tunnel, ok := cfg.TunnelProfile(tunnelKey); ok {
		target = tunnel.Key
	}
	for i := range cfg.Tunnels {
		if cfg.Tunnels[i].Key != target {
			continue
		}
		cfg.Tunnels[i].RemoteManagementEnabled = enabled
		if strings.TrimSpace(accountID) != "" {
			cfg.Tunnels[i].AccountID = strings.TrimSpace(accountID)
		}
		if strings.TrimSpace(tunnelID) != "" {
			cfg.Tunnels[i].TunnelID = strings.TrimSpace(tunnelID)
		}
		break
	}
	return cfg
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
