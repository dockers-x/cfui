package mcpbridge

import (
	"cfui/internal/config"
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
	tokens    *TokenStore
	server    *mcp.Server
	handler   http.Handler
}

func NewService(cfgMgr *config.Manager, runner *service.Runner, tunnelMgr *tunnelmgr.Manager, tokens *TokenStore) *Service {
	s := &Service{
		cfgMgr:    cfgMgr,
		runner:    runner,
		tunnelMgr: tunnelMgr,
		tokens:    tokens,
	}
	s.server = s.newMCPServer()
	streamable := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return s.server
	}, &mcp.StreamableHTTPOptions{Stateless: true})
	s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	return StatusResponse{
		Enabled:  len(tokens) > 0,
		Endpoint: endpoint,
		Tokens:   tokens,
		Tools: []string{
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
		},
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
