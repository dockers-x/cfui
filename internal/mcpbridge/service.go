package mcpbridge

import (
	"cfui/internal/config"
	"cfui/internal/ddns"
	"cfui/internal/logger"
	"cfui/internal/service"
	"cfui/internal/tunnelmgr"
	"context"
	"fmt"
	"net/http"
	"strings"

	"cfui/version"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Service struct {
	cfgMgr    *config.Manager
	runner    *service.Runner
	tunnelMgr *tunnelmgr.Manager
	ddnsSvc   *ddns.Service
	tokens    *TokenStore
	server    *mcp.Server
	handler   http.Handler
}

func NewService(cfgMgr *config.Manager, runner *service.Runner, tunnelMgr *tunnelmgr.Manager, tokens *TokenStore, ddnsSvc *ddns.Service) *Service {
	s := &Service{
		cfgMgr:    cfgMgr,
		runner:    runner,
		tunnelMgr: tunnelMgr,
		ddnsSvc:   ddnsSvc,
		tokens:    tokens,
	}
	s.server = s.newMCPServer()
	streamable := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return s.server
	}, &mcp.StreamableHTTPOptions{Stateless: true})
	s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.cfgMgr.Get().MCPEnabled {
			http.Error(w, "MCP access disabled", http.StatusServiceUnavailable)
			return
		}
		if !s.authorized(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="cfui-mcp"`)
			http.Error(w, "MCP bearer token required", http.StatusUnauthorized)
			return
		}
		streamable.ServeHTTP(w, r)
	})
	return s
}

func (s *Service) Handler() http.Handler {
	return s.handler
}

func (s *Service) authorized(r *http.Request) bool {
	if s == nil || s.tokens == nil {
		return false
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	return s.tokens.Verify(strings.TrimSpace(strings.TrimPrefix(auth, prefix)))
}

func (s *Service) Status(endpoint string) (StatusResponse, error) {
	tokens, err := s.tokens.List()
	if err != nil {
		return StatusResponse{}, err
	}
	cfg := s.cfgMgr.Get()
	tools := []string{
		"cfui_get_status",
		"cfui_get_config",
		"cfui_update_config",
		"cfui_start_tunnel",
		"cfui_stop_tunnel",
		"cfui_get_recent_logs",
		"cfui_get_tunnel_manager_settings",
		"cfui_save_tunnel_manager_settings",
		"cfui_get_tunnel_config",
		"cfui_list_zones",
		"cfui_add_ingress_rule",
		"cfui_update_ingress_rule",
		"cfui_delete_ingress_rule",
	}
	if cfg.DDNS.Enabled {
		tools = append(tools,
			"cfui_get_ddns_config",
			"cfui_save_ddns_config",
			"cfui_get_ddns_status",
			"cfui_sync_ddns_now",
			"cfui_list_ddns_zones",
			"cfui_add_ddns_record",
			"cfui_delete_ddns_record",
		)
	}
	return StatusResponse{
		Enabled:  cfg.MCPEnabled,
		Endpoint: endpoint,
		Tokens:   tokens,
		Tools:    tools,
	}, nil
}

func (s *Service) CreateToken(name, token string) (CreatedToken, error) {
	return s.tokens.Create(name, token)
}

func (s *Service) DeleteToken(id string) error {
	return s.tokens.Delete(id)
}

func (s *Service) newMCPServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "cfui",
		Version: version.GetVersion(),
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "cfui_get_status",
		Description: "Get the local cloudflared tunnel running status, status text, active protocol, and last error.",
	}, s.getStatus)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cfui_get_config",
		Description: "Get current cfui tunnel configuration with secrets redacted to boolean flags.",
	}, s.getConfig)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cfui_update_config",
		Description: "Update cfui tunnel runner settings. Omitted fields keep their current value.",
	}, s.updateConfig)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cfui_start_tunnel",
		Description: "Start the local cloudflared tunnel with the saved configuration.",
	}, s.startTunnel)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cfui_stop_tunnel",
		Description: "Stop the local cloudflared tunnel.",
	}, s.stopTunnel)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cfui_get_recent_logs",
		Description: "Return recent cfui log lines.",
	}, s.getRecentLogs)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cfui_get_tunnel_manager_settings",
		Description: "Get remote Cloudflare Tunnel manager settings with secrets redacted.",
	}, s.getTunnelManagerSettings)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cfui_save_tunnel_manager_settings",
		Description: "Save remote Cloudflare Tunnel manager settings. Blank secret fields keep saved values.",
	}, s.saveTunnelManagerSettings)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cfui_get_tunnel_config",
		Description: "Load the selected Cloudflare Tunnel ingress configuration.",
	}, s.getTunnelConfig)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cfui_list_zones",
		Description: "List Cloudflare zones available to the configured account.",
	}, s.listZones)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cfui_add_ingress_rule",
		Description: "Add a Cloudflare Tunnel ingress rule before the catch-all rule.",
	}, s.addIngressRule)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cfui_update_ingress_rule",
		Description: "Update a Cloudflare Tunnel ingress rule by index.",
	}, s.updateIngressRule)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cfui_delete_ingress_rule",
		Description: "Delete a Cloudflare Tunnel ingress rule by index.",
	}, s.deleteIngressRule)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cfui_get_ddns_config",
		Description: "Get DDNS configuration including IP sources, records, interval, and credentials presence. Requires DDNS feature enabled.",
	}, s.getDDNSConfig)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cfui_save_ddns_config",
		Description: "Save DDNS configuration (sources, records, interval, retries, only_on_change). Requires DDNS feature enabled.",
	}, s.saveDDNSConfig)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cfui_get_ddns_status",
		Description: "Get current DDNS status: current public IPv4/IPv6, last check time, recent sync results.",
	}, s.getDDNSStatus)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cfui_sync_ddns_now",
		Description: "Trigger an immediate DDNS sync. Returns the updated status.",
	}, s.syncDDNSNow)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cfui_list_ddns_zones",
		Description: "List Cloudflare zones accessible with the configured API credentials (shared with Tunnel Manager).",
	}, s.listDDNSZones)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cfui_add_ddns_record",
		Description: "Append a DDNS record to the saved list.",
	}, s.addDDNSRecord)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cfui_delete_ddns_record",
		Description: "Delete a DDNS record by zero-based index from the saved list.",
	}, s.deleteDDNSRecord)

	return server
}

type StatusResponse struct {
	Enabled  bool           `json:"enabled"`
	Endpoint string         `json:"endpoint"`
	Tokens   []TokenSummary `json:"tokens"`
	Tools    []string       `json:"tools"`
}

type EmptyInput struct{}

type TunnelStatusOutput struct {
	Running  bool   `json:"running"`
	Status   string `json:"status"`
	Protocol string `json:"protocol"`
	Error    string `json:"error,omitempty"`
}

func (s *Service) getStatus(ctx context.Context, req *mcp.CallToolRequest, in EmptyInput) (*mcp.CallToolResult, TunnelStatusOutput, error) {
	if s.runner == nil {
		return nil, TunnelStatusOutput{}, fmt.Errorf("runner is not available")
	}
	running, err, protocol := s.runner.Status()
	status := "stopped"
	if running {
		status = "running"
	}
	out := TunnelStatusOutput{Running: running, Status: status, Protocol: protocol}
	if err != nil {
		out.Status = "error"
		out.Error = err.Error()
	}
	return nil, out, nil
}

type ConfigOutput struct {
	TokenSet         bool                       `json:"token_set"`
	AutoStart        bool                       `json:"auto_start"`
	AutoRestart      bool                       `json:"auto_restart"`
	CustomTag        string                     `json:"custom_tag"`
	SoftwareName     string                     `json:"software_name"`
	Protocol         string                     `json:"protocol"`
	GracePeriod      string                     `json:"grace_period"`
	Region           string                     `json:"region"`
	Retries          int                        `json:"retries"`
	MetricsEnable    bool                       `json:"metrics_enable"`
	MetricsPort      int                        `json:"metrics_port"`
	LogLevel         string                     `json:"log_level"`
	LogJSON          bool                       `json:"log_json"`
	EdgeIPVersion    string                     `json:"edge_ip_version"`
	EdgeBindAddress  string                     `json:"edge_bind_address"`
	PostQuantum      bool                       `json:"post_quantum"`
	NoTLSVerify      bool                       `json:"no_tls_verify"`
	ExtraArgs        string                     `json:"extra_args"`
	TunnelManagement tunnelmgr.SettingsResponse `json:"tunnel_management"`
}

func (s *Service) getConfig(ctx context.Context, req *mcp.CallToolRequest, in EmptyInput) (*mcp.CallToolResult, ConfigOutput, error) {
	cfg := s.cfgMgr.Get()
	out := ConfigOutput{
		TokenSet:         strings.TrimSpace(cfg.Token) != "",
		AutoStart:        cfg.AutoStart,
		AutoRestart:      cfg.AutoRestart,
		CustomTag:        cfg.CustomTag,
		SoftwareName:     cfg.SoftwareName,
		Protocol:         cfg.Protocol,
		GracePeriod:      cfg.GracePeriod,
		Region:           cfg.Region,
		Retries:          cfg.Retries,
		MetricsEnable:    cfg.MetricsEnable,
		MetricsPort:      cfg.MetricsPort,
		LogLevel:         cfg.LogLevel,
		LogJSON:          cfg.LogJSON,
		EdgeIPVersion:    cfg.EdgeIPVersion,
		EdgeBindAddress:  cfg.EdgeBindAddress,
		PostQuantum:      cfg.PostQuantum,
		NoTLSVerify:      cfg.NoTLSVerify,
		ExtraArgs:        cfg.ExtraArgs,
		TunnelManagement: s.tunnelMgr.Settings(),
	}
	return nil, out, nil
}

type UpdateConfigInput struct {
	Token           *string `json:"token,omitempty" jsonschema:"Cloudflare Tunnel token; omit to keep current token"`
	AutoStart       *bool   `json:"auto_start,omitempty"`
	AutoRestart     *bool   `json:"auto_restart,omitempty"`
	CustomTag       *string `json:"custom_tag,omitempty"`
	SoftwareName    *string `json:"software_name,omitempty"`
	Protocol        *string `json:"protocol,omitempty"`
	GracePeriod     *string `json:"grace_period,omitempty"`
	Region          *string `json:"region,omitempty"`
	Retries         *int    `json:"retries,omitempty"`
	MetricsEnable   *bool   `json:"metrics_enable,omitempty"`
	MetricsPort     *int    `json:"metrics_port,omitempty"`
	EdgeBindAddress *string `json:"edge_bind_address,omitempty"`
	NoTLSVerify     *bool   `json:"no_tls_verify,omitempty"`
	ExtraArgs       *string `json:"extra_args,omitempty"`
}

func (s *Service) updateConfig(ctx context.Context, req *mcp.CallToolRequest, in UpdateConfigInput) (*mcp.CallToolResult, ConfigOutput, error) {
	cfg := s.cfgMgr.Get()
	if in.Token != nil {
		cfg.Token = strings.TrimSpace(*in.Token)
	}
	if in.AutoStart != nil {
		cfg.AutoStart = *in.AutoStart
	}
	if in.AutoRestart != nil {
		cfg.AutoRestart = *in.AutoRestart
	}
	if in.CustomTag != nil {
		cfg.CustomTag = strings.TrimSpace(*in.CustomTag)
	}
	if in.SoftwareName != nil {
		cfg.SoftwareName = strings.TrimSpace(*in.SoftwareName)
	}
	if in.Protocol != nil {
		cfg.Protocol = strings.TrimSpace(*in.Protocol)
	}
	if in.GracePeriod != nil {
		cfg.GracePeriod = strings.TrimSpace(*in.GracePeriod)
	}
	if in.Region != nil {
		cfg.Region = strings.TrimSpace(*in.Region)
	}
	if in.Retries != nil {
		cfg.Retries = *in.Retries
	}
	if in.MetricsEnable != nil {
		cfg.MetricsEnable = *in.MetricsEnable
	}
	if in.MetricsPort != nil {
		cfg.MetricsPort = *in.MetricsPort
	}
	if in.EdgeBindAddress != nil {
		cfg.EdgeBindAddress = strings.TrimSpace(*in.EdgeBindAddress)
	}
	if in.NoTLSVerify != nil {
		cfg.NoTLSVerify = *in.NoTLSVerify
	}
	if in.ExtraArgs != nil {
		cfg.ExtraArgs = strings.TrimSpace(*in.ExtraArgs)
	}
	if err := s.cfgMgr.Save(cfg); err != nil {
		return nil, ConfigOutput{}, err
	}
	return s.getConfig(ctx, req, EmptyInput{})
}

type ControlOutput struct {
	Success bool   `json:"success"`
	Action  string `json:"action"`
	Message string `json:"message"`
}

func (s *Service) startTunnel(ctx context.Context, req *mcp.CallToolRequest, in EmptyInput) (*mcp.CallToolResult, ControlOutput, error) {
	if s.runner == nil {
		return nil, ControlOutput{}, fmt.Errorf("runner is not available")
	}
	if err := s.runner.Start(); err != nil {
		return nil, ControlOutput{}, err
	}
	return nil, ControlOutput{Success: true, Action: "start", Message: "Tunnel started successfully"}, nil
}

func (s *Service) stopTunnel(ctx context.Context, req *mcp.CallToolRequest, in EmptyInput) (*mcp.CallToolResult, ControlOutput, error) {
	if s.runner == nil {
		return nil, ControlOutput{}, fmt.Errorf("runner is not available")
	}
	if err := s.runner.Stop(); err != nil {
		return nil, ControlOutput{}, err
	}
	return nil, ControlOutput{Success: true, Action: "stop", Message: "Tunnel stopped successfully"}, nil
}

type LogsInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"maximum number of recent log lines to return"`
}

type LogsOutput struct {
	Logs  []string `json:"logs"`
	Count int      `json:"count"`
}

func (s *Service) getRecentLogs(ctx context.Context, req *mcp.CallToolRequest, in LogsInput) (*mcp.CallToolResult, LogsOutput, error) {
	broadcaster := logger.GetBroadcaster()
	if broadcaster == nil {
		return nil, LogsOutput{}, fmt.Errorf("log broadcaster is not available")
	}
	logs := broadcaster.GetRecentLogs()
	if in.Limit > 0 && in.Limit < len(logs) {
		logs = logs[len(logs)-in.Limit:]
	}
	return nil, LogsOutput{Logs: logs, Count: len(logs)}, nil
}

func (s *Service) getTunnelManagerSettings(ctx context.Context, req *mcp.CallToolRequest, in EmptyInput) (*mcp.CallToolResult, tunnelmgr.SettingsResponse, error) {
	return nil, s.tunnelMgr.Settings(), nil
}

func (s *Service) saveTunnelManagerSettings(ctx context.Context, req *mcp.CallToolRequest, in tunnelmgr.SettingsRequest) (*mcp.CallToolResult, tunnelmgr.SettingsResponse, error) {
	if err := s.tunnelMgr.SaveSettings(in); err != nil {
		return nil, tunnelmgr.SettingsResponse{}, err
	}
	return nil, s.tunnelMgr.Settings(), nil
}

func (s *Service) getTunnelConfig(ctx context.Context, req *mcp.CallToolRequest, in EmptyInput) (*mcp.CallToolResult, tunnelmgr.ConfigurationResponse, error) {
	out, err := s.tunnelMgr.Fetch(ctx)
	return nil, out, err
}

type ZonesOutput struct {
	Zones []tunnelmgr.ZoneResponse `json:"zones"`
}

func (s *Service) listZones(ctx context.Context, req *mcp.CallToolRequest, in EmptyInput) (*mcp.CallToolResult, ZonesOutput, error) {
	zones, err := s.tunnelMgr.ListZones(ctx)
	return nil, ZonesOutput{Zones: zones}, err
}

func (s *Service) addIngressRule(ctx context.Context, req *mcp.CallToolRequest, in tunnelmgr.IngressRule) (*mcp.CallToolResult, tunnelmgr.ConfigurationResponse, error) {
	out, err := s.tunnelMgr.AddEntry(ctx, in)
	return nil, out, err
}

type UpdateIngressRuleInput struct {
	Index            int    `json:"index" jsonschema:"zero-based ingress rule index"`
	Hostname         string `json:"hostname,omitempty"`
	Path             string `json:"path,omitempty"`
	Service          string `json:"service" jsonschema:"service URL such as http://localhost:8080 or http_status:404"`
	NoTLSVerify      bool   `json:"no_tls_verify,omitempty"`
	HTTPHostHeader   string `json:"http_host_header,omitempty"`
	OriginServerName string `json:"origin_server_name,omitempty"`
}

func (s *Service) updateIngressRule(ctx context.Context, req *mcp.CallToolRequest, in UpdateIngressRuleInput) (*mcp.CallToolResult, tunnelmgr.ConfigurationResponse, error) {
	out, err := s.tunnelMgr.UpdateEntry(ctx, in.Index, tunnelmgr.IngressRule{
		Hostname:         in.Hostname,
		Path:             in.Path,
		Service:          in.Service,
		NoTLSVerify:      in.NoTLSVerify,
		HTTPHostHeader:   in.HTTPHostHeader,
		OriginServerName: in.OriginServerName,
	})
	return nil, out, err
}

type DeleteIngressRuleInput struct {
	Index int `json:"index" jsonschema:"zero-based ingress rule index"`
}

func (s *Service) deleteIngressRule(ctx context.Context, req *mcp.CallToolRequest, in DeleteIngressRuleInput) (*mcp.CallToolResult, tunnelmgr.ConfigurationResponse, error) {
	out, err := s.tunnelMgr.DeleteEntry(ctx, in.Index)
	return nil, out, err
}

// --- DDNS tools ---

func (s *Service) requireDDNSEnabled() error {
	if s.ddnsSvc == nil {
		return fmt.Errorf("DDNS service unavailable")
	}
	if !s.cfgMgr.Get().DDNS.Enabled {
		return fmt.Errorf("DDNS feature is disabled; enable it in Features settings")
	}
	return nil
}

func (s *Service) getDDNSConfig(ctx context.Context, req *mcp.CallToolRequest, in EmptyInput) (*mcp.CallToolResult, ddns.ConfigResponse, error) {
	if err := s.requireDDNSEnabled(); err != nil {
		return nil, ddns.ConfigResponse{}, err
	}
	return nil, s.ddnsSvc.GetConfig(), nil
}

func (s *Service) saveDDNSConfig(ctx context.Context, req *mcp.CallToolRequest, in ddns.SaveRequest) (*mcp.CallToolResult, ddns.ConfigResponse, error) {
	if err := s.requireDDNSEnabled(); err != nil {
		return nil, ddns.ConfigResponse{}, err
	}
	if err := s.ddnsSvc.SaveConfig(in); err != nil {
		return nil, ddns.ConfigResponse{}, err
	}
	s.ddnsSvc.Restart()
	return nil, s.ddnsSvc.GetConfig(), nil
}

func (s *Service) getDDNSStatus(ctx context.Context, req *mcp.CallToolRequest, in EmptyInput) (*mcp.CallToolResult, *ddns.StatusResponse, error) {
	if s.ddnsSvc == nil {
		return nil, nil, fmt.Errorf("DDNS service unavailable")
	}
	return nil, s.ddnsSvc.Status(), nil
}

func (s *Service) syncDDNSNow(ctx context.Context, req *mcp.CallToolRequest, in EmptyInput) (*mcp.CallToolResult, *ddns.StatusResponse, error) {
	if err := s.requireDDNSEnabled(); err != nil {
		return nil, nil, err
	}
	out, err := s.ddnsSvc.SyncNow(ctx)
	return nil, out, err
}

type DDNSZonesOutput struct {
	Zones []ddns.ZoneResponse `json:"zones"`
}

func (s *Service) listDDNSZones(ctx context.Context, req *mcp.CallToolRequest, in EmptyInput) (*mcp.CallToolResult, DDNSZonesOutput, error) {
	if err := s.requireDDNSEnabled(); err != nil {
		return nil, DDNSZonesOutput{}, err
	}
	zones, err := s.ddnsSvc.ListZones(ctx)
	return nil, DDNSZonesOutput{Zones: zones}, err
}

type AddDDNSRecordInput struct {
	Name    string `json:"name" jsonschema:"full hostname, e.g. home.example.com"`
	ZoneID  string `json:"zone_id" jsonschema:"Cloudflare zone id"`
	Type    string `json:"type" jsonschema:"record type: A or AAAA"`
	TTL     int    `json:"ttl,omitempty" jsonschema:"TTL in seconds; 1 means Auto"`
	Proxied bool   `json:"proxied,omitempty"`
}

func (s *Service) addDDNSRecord(ctx context.Context, req *mcp.CallToolRequest, in AddDDNSRecordInput) (*mcp.CallToolResult, ddns.ConfigResponse, error) {
	if err := s.requireDDNSEnabled(); err != nil {
		return nil, ddns.ConfigResponse{}, err
	}
	cur := s.ddnsSvc.GetConfig()
	ttl := in.TTL
	if ttl == 0 {
		ttl = 1
	}
	cur.Records = append(cur.Records, config.DDNSRecord{
		Name:    in.Name,
		ZoneID:  in.ZoneID,
		Type:    in.Type,
		TTL:     ttl,
		Proxied: in.Proxied,
	})
	saveReq := ddns.SaveRequest{
		Enabled:      cur.Enabled,
		IPSources:    cur.IPSources,
		Records:      cur.Records,
		IntervalMins: cur.IntervalMins,
		OnlyOnChange: cur.OnlyOnChange,
		MaxRetries:   cur.MaxRetries,
	}
	if err := s.ddnsSvc.SaveConfig(saveReq); err != nil {
		return nil, ddns.ConfigResponse{}, err
	}
	s.ddnsSvc.Restart()
	return nil, s.ddnsSvc.GetConfig(), nil
}

type DeleteDDNSRecordInput struct {
	Index int `json:"index" jsonschema:"zero-based DDNS record index"`
}

func (s *Service) deleteDDNSRecord(ctx context.Context, req *mcp.CallToolRequest, in DeleteDDNSRecordInput) (*mcp.CallToolResult, ddns.ConfigResponse, error) {
	if err := s.requireDDNSEnabled(); err != nil {
		return nil, ddns.ConfigResponse{}, err
	}
	cur := s.ddnsSvc.GetConfig()
	if in.Index < 0 || in.Index >= len(cur.Records) {
		return nil, ddns.ConfigResponse{}, fmt.Errorf("record index out of range")
	}
	cur.Records = append(cur.Records[:in.Index], cur.Records[in.Index+1:]...)
	saveReq := ddns.SaveRequest{
		Enabled:      cur.Enabled,
		IPSources:    cur.IPSources,
		Records:      cur.Records,
		IntervalMins: cur.IntervalMins,
		OnlyOnChange: cur.OnlyOnChange,
		MaxRetries:   cur.MaxRetries,
	}
	if err := s.ddnsSvc.SaveConfig(saveReq); err != nil {
		return nil, ddns.ConfigResponse{}, err
	}
	s.ddnsSvc.Restart()
	return nil, s.ddnsSvc.GetConfig(), nil
}
