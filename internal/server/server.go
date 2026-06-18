package server

import (
	"bytes"
	"cfui/internal/cfaccount"
	"cfui/internal/cfoauth"
	"cfui/internal/cloudflared"
	"cfui/internal/config"
	"cfui/internal/ddns"
	"cfui/internal/logger"
	"cfui/internal/mcpbridge"
	"cfui/internal/pool"
	"cfui/internal/s3dav"
	"cfui/internal/service"
	"cfui/internal/tunnelmgr"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"cfui/version"

	"github.com/BurntSushi/toml"
)

// API Response structures for type safety

// StatusResponse represents the tunnel status response
type StatusResponse struct {
	Running  bool   `json:"running"`
	Status   string `json:"status"`
	Protocol string `json:"protocol"`
	Error    string `json:"error,omitempty"`
}

// Reset resets the StatusResponse to its zero state
func (r *StatusResponse) Reset() {
	r.Running = false
	r.Status = ""
	r.Protocol = ""
	r.Error = ""
}

// ControlResponse represents the control action response
type ControlResponse struct {
	Success bool   `json:"success"`
	Action  string `json:"action"`
	Message string `json:"message"`
}

// Reset resets the ControlResponse to its zero state
func (r *ControlResponse) Reset() {
	r.Success = false
	r.Action = ""
	r.Message = ""
}

// RecentLogsResponse represents the recent logs response
type RecentLogsResponse struct {
	Logs  []string `json:"logs"`
	Count int      `json:"count"`
}

// Reset resets the RecentLogsResponse to its zero state
func (r *RecentLogsResponse) Reset() {
	r.Logs = nil
	r.Count = 0
}

// VersionResponse represents the version information response
type VersionResponse struct {
	Version   string `json:"version"`
	BuildTime string `json:"build_time"`
	GitCommit string `json:"git_commit"`
	FullInfo  string `json:"full_info"`
}

// Reset resets the VersionResponse to its zero state
func (r *VersionResponse) Reset() {
	r.Version = ""
	r.BuildTime = ""
	r.GitCommit = ""
	r.FullInfo = ""
}

// Response struct pools for efficient memory reuse
var (
	statusResponsePool     = pool.New(func() *StatusResponse { return &StatusResponse{} })
	controlResponsePool    = pool.New(func() *ControlResponse { return &ControlResponse{} })
	recentLogsResponsePool = pool.New(func() *RecentLogsResponse { return &RecentLogsResponse{} })
	versionResponsePool    = pool.New(func() *VersionResponse { return &VersionResponse{} })
)

type Server struct {
	cfgMgr    *config.Manager
	runner    *service.Runner
	tunnelMgr *tunnelmgr.Manager
	mcpSvc    *mcpbridge.Service
	ddnsSvc   *ddns.Service
	s3Svc     *s3dav.Service
	s3WebDAV  *s3DedicatedServer
	runMode   config.RunMode
	oauthSvc  *cfoauth.Service
	cfSvc     *cfaccount.Service
	assets    embed.FS
	locales   embed.FS

	// shutdownC is closed by PrepareShutdown so long-lived connections
	// (SSE log streams) exit promptly instead of stalling http.Server.Shutdown
	// until its timeout.
	shutdownC chan struct{}
}

func NewServer(cfgMgr *config.Manager, runner *service.Runner, assets embed.FS, locales embed.FS) *Server {
	return NewServerWithMode(cfgMgr, runner, assets, locales, config.RunModeClassic)
}

func NewServerWithMode(cfgMgr *config.Manager, runner *service.Runner, assets embed.FS, locales embed.FS, runMode config.RunMode) *Server {
	tunnelMgr := tunnelmgr.NewManager(cfgMgr)
	tokenStore := mcpbridge.NewTokenStore(cfgMgr.Dir())
	ddnsSvc := ddns.NewService(cfgMgr)
	s3Svc := s3dav.NewService(cfgMgr)
	oauthSvc := cfoauth.NewService(cfoauth.ConfigFromEnv(), cfoauth.NewStore(cfgMgr.Dir()))
	return &Server{
		cfgMgr:    cfgMgr,
		runner:    runner,
		tunnelMgr: tunnelMgr,
		mcpSvc:    mcpbridge.NewService(cfgMgr, runner, tunnelMgr, tokenStore, ddnsSvc),
		ddnsSvc:   ddnsSvc,
		s3Svc:     s3Svc,
		s3WebDAV:  newS3DedicatedServer(),
		runMode:   runMode,
		oauthSvc:  oauthSvc,
		cfSvc:     cfaccount.NewService(oauthSvc),
		assets:    assets,
		locales:   locales,
		shutdownC: make(chan struct{}),
	}
}

// PrepareShutdown asks long-lived connections (log streams) to close so the
// HTTP server can shut down promptly. Call before http.Server.Shutdown.
func (s *Server) PrepareShutdown() {
	select {
	case <-s.shutdownC:
		// already closed
	default:
		close(s.shutdownC)
	}
}

func (s *Server) ensureOAuthService() *cfoauth.Service {
	if s.oauthSvc == nil {
		s.oauthSvc = cfoauth.NewService(cfoauth.ConfigFromEnv(), cfoauth.NewStore(s.cfgMgr.Dir()))
	}
	if s.cfSvc == nil {
		s.cfSvc = cfaccount.NewService(s.oauthSvc)
	}
	return s.oauthSvc
}

func (s *Server) effectiveRunMode() config.RunMode {
	if s.runMode == "" {
		return config.RunModeClassic
	}
	return s.runMode
}

// GetHandler creates and returns the HTTP handler
func (s *Server) GetHandler() http.Handler {
	mux := http.NewServeMux()

	// API Endpoints
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/control", s.handleControl)
	mux.HandleFunc("/api/tunnels", s.handleTunnels)
	mux.HandleFunc("/api/tunnels/", s.handleTunnel)
	mux.HandleFunc("/api/version", s.handleVersion)
	mux.HandleFunc("/api/i18n/", s.handleI18n)
	mux.HandleFunc("/api/logs/stream", s.handleLogStream)
	mux.HandleFunc("/api/logs/recent", s.handleRecentLogs)
	mux.HandleFunc("/api/tunnel-manager/settings", s.handleTunnelManagerSettings)
	mux.HandleFunc("/api/tunnel-manager/tunnel", s.handleTunnelManagerTunnel)
	mux.HandleFunc("/api/tunnel-manager/config", s.handleTunnelManagerConfig)
	mux.HandleFunc("/api/tunnel-manager/zones", s.handleTunnelManagerZones)
	mux.HandleFunc("/api/tunnel-manager/entries", s.handleTunnelManagerEntries)
	mux.HandleFunc("/api/tunnel-manager/entries/", s.handleTunnelManagerEntry)
	mux.HandleFunc("/api/tunnel-manager/verify-token", s.handleTunnelManagerVerifyToken)
	mux.HandleFunc("/api/mcp/status", s.handleMCPStatus)
	mux.HandleFunc("/api/mcp/tokens", s.handleMCPTokens)
	mux.HandleFunc("/api/mcp/tokens/", s.handleMCPToken)
	mux.HandleFunc("/api/features", s.handleFeatures)
	mux.HandleFunc("/api/oauth/status", s.handleOAuthStatus)
	mux.HandleFunc("/api/oauth/relay-check", s.handleOAuthRelayCheck)
	mux.HandleFunc("/api/oauth/login", s.handleOAuthLogin)
	mux.HandleFunc("/api/oauth/logout", s.handleOAuthLogout)
	mux.HandleFunc("/api/oauth/session", s.handleOAuthSession)
	mux.HandleFunc("/oauth/start", s.handleOAuthStart)
	mux.HandleFunc("/oauth/callback", s.handleOAuthCallback)
	mux.HandleFunc("/api/cf/overview", s.handleCFOverview)
	mux.HandleFunc("/api/cf/accounts", s.handleCFAccounts)
	mux.HandleFunc("/api/cf/status", s.handleCFStatus)
	mux.HandleFunc("/api/cf/usage/account", s.handleCFAccountUsage)
	mux.HandleFunc("/api/cf/zones", s.handleCFZones)
	mux.HandleFunc("/api/cf/zones/", s.handleCFZone)
	mux.HandleFunc("/api/cf/dns/count", s.handleCFDNSCount)
	mux.HandleFunc("/api/cf/dns", s.handleCFDNSRecords)
	mux.HandleFunc("/api/cf/dns/", s.handleCFDNSRecord)
	mux.HandleFunc("/api/cf/tunnels", s.handleCFTunnels)
	mux.HandleFunc("/api/cf/workers", s.handleCFWorkers)
	mux.HandleFunc("/api/cf/workers/", s.handleCFWorker)
	mux.HandleFunc("/api/cf/r2/metrics", s.handleCFR2Metrics)
	mux.HandleFunc("/api/cf/r2/buckets", s.handleCFR2Buckets)
	mux.HandleFunc("/api/cf/r2/buckets/", s.handleCFR2Bucket)
	mux.HandleFunc("/api/cf/r2/objects", s.handleCFR2Objects)
	mux.HandleFunc("/api/cf/r2/object/copy", s.handleCFR2ObjectCopy)
	mux.HandleFunc("/api/cf/r2/object/upload", s.handleCFR2ObjectUpload)
	mux.HandleFunc("/api/cf/r2/object/download", s.handleCFR2ObjectDownload)
	mux.HandleFunc("/api/cf/r2/object", s.handleCFR2Object)
	mux.HandleFunc("/api/cf/d1/databases", s.handleCFD1Databases)
	mux.HandleFunc("/api/cf/d1/databases/", s.handleCFD1Database)
	mux.HandleFunc("/api/cf/d1/query", s.handleCFD1Query)
	mux.HandleFunc("/api/cf/d1/tables", s.handleCFD1Tables)
	mux.HandleFunc("/api/cf/d1/table", s.handleCFD1Table)
	mux.HandleFunc("/api/cf/kv/namespaces", s.handleCFKVNamespaces)
	mux.HandleFunc("/api/cf/kv/keys", s.handleCFKVKeys)
	mux.HandleFunc("/api/cf/kv/value", s.handleCFKVValue)
	mux.HandleFunc("/api/cf/snippets", s.handleCFSnippets)
	mux.HandleFunc("/api/cf/snippets/rules", s.handleCFSnippetRules)
	mux.HandleFunc("/api/cf/snippets/rules/", s.handleCFSnippetRule)
	mux.HandleFunc("/api/cf/snippets/", s.handleCFSnippet)
	mux.HandleFunc("/api/cf/waf", s.handleCFWAF)
	mux.HandleFunc("/api/cf/waf/managed-exceptions", s.handleCFWAFManagedExceptions)
	mux.HandleFunc("/api/cf/waf/managed-exceptions/rules", s.handleCFWAFManagedExceptionRules)
	mux.HandleFunc("/api/cf/waf/managed-exceptions/rules/", s.handleCFWAFManagedExceptionRule)
	mux.HandleFunc("/api/cf/waf/managed-overrides", s.handleCFWAFManagedOverrides)
	mux.HandleFunc("/api/cf/waf/managed-overrides/rules", s.handleCFWAFManagedOverrideRules)
	mux.HandleFunc("/api/cf/waf/managed-overrides/rules/", s.handleCFWAFManagedOverrideRule)
	mux.HandleFunc("/api/cf/waf/rules", s.handleCFWAFRules)
	mux.HandleFunc("/api/cf/waf/rules/", s.handleCFWAFRule)
	mux.HandleFunc("/api/cf/analytics/zone", s.handleCFZoneAnalytics)
	mux.HandleFunc("/api/cf/zone-settings", s.handleCFZoneSettings)
	mux.HandleFunc("/api/cf/zone-settings/", s.handleCFZoneSetting)
	mux.HandleFunc("/api/cf/cache/purge", s.handleCFCachePurge)
	mux.Handle("/mcp", s.mcpSvc.Handler())
	mux.Handle("/mcp/", s.mcpSvc.Handler())
	mux.Handle("/webdav/", s.mainWebDAVHandler())

	// DDNS endpoints
	mux.HandleFunc("/api/ddns/config", s.handleDDNSConfig)
	mux.HandleFunc("/api/ddns/status", s.handleDDNSStatus)
	mux.HandleFunc("/api/ddns/sync-now", s.handleDDNSSyncNow)
	mux.HandleFunc("/api/ddns/zones", s.handleDDNSZones)
	mux.HandleFunc("/api/ddns/records/", s.handleDDNSRecord)
	mux.HandleFunc("/api/ddns/records", s.handleDDNSRecords)

	// S3 WebDAV endpoints
	mux.HandleFunc("/api/s3/settings", s.handleS3Settings)
	mux.HandleFunc("/api/s3/webdav-control", s.handleS3WebDAVControl)
	mux.HandleFunc("/api/s3/mounts/", s.handleS3Mount)
	mux.HandleFunc("/api/s3/mounts", s.handleS3Mounts)
	mux.HandleFunc("/api/s3/test", s.handleS3Test)
	mux.HandleFunc("/api/s3/webdav-test", s.handleS3WebDAVTest)
	mux.HandleFunc("/api/s3/buckets", s.handleS3Buckets)
	mux.HandleFunc("/api/s3/files/download", s.handleS3Download)
	mux.HandleFunc("/api/s3/files/mkdir", s.handleS3Mkdir)
	mux.HandleFunc("/api/s3/files/rename", s.handleS3Rename)
	mux.HandleFunc("/api/s3/files/sync/", s.handleS3SyncJob)
	mux.HandleFunc("/api/s3/files/sync", s.handleS3Sync)
	mux.HandleFunc("/api/s3/files/", s.handleS3FileObject)
	mux.HandleFunc("/api/s3/files", s.handleS3Files)

	// Static Files
	// The assets are in "web/dist", so we need to strip that prefix
	fsys, err := fs.Sub(s.assets, "web/dist")
	if err != nil {
		logger.Sugar.Errorf("Failed to create sub filesystem: %v", err)
		panic(err)
	}
	indexHandler := serveEmbeddedIndex(fsys)
	mux.HandleFunc("/cloudflare", indexHandler)
	mux.HandleFunc("/cloudflare/", indexHandler)
	mux.HandleFunc("/local", indexHandler)
	mux.HandleFunc("/local/", indexHandler)
	mux.Handle("/", s.staticHandler(fsys))

	// Apply middleware chain: logging -> panic recovery -> handler
	return ChainMiddleware(mux, LoggingMiddleware, PanicRecoveryMiddleware)
}

func serveEmbeddedIndex(fsys fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		index, err := fs.ReadFile(fsys, "index.html")
		if err != nil {
			logger.Sugar.Errorf("Failed to read embedded index.html: %v", err)
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
	}
}

func (s *Server) staticHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" && s.effectiveRunMode().DefaultWorkspace() == "cloudflare" {
			http.Redirect(w, r, "/cloudflare", http.StatusFound)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func (s *Server) handleMCPStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	status, err := s.mcpSvc.Status("/mcp")
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, status)
}

// FeaturesResponse describes feature toggle state.
type FeaturesResponse struct {
	Mode           string                        `json:"mode"`
	ClassicEnabled bool                          `json:"classic_enabled"`
	OAuthEnabled   bool                          `json:"oauth_enabled"`
	Local          LocalFeaturesResponse         `json:"local"`
	Cloudflare     CloudflareFeaturesResponse    `json:"cloudflare"`
	TunnelManager  bool                          `json:"tunnel_manager"`
	DDNS           bool                          `json:"ddns"`
	MCP            bool                          `json:"mcp"`
	S3WebDAV       bool                          `json:"s3_webdav"`
	Availability   map[string]s3dav.Availability `json:"availability,omitempty"`
}

type LocalFeaturesResponse struct {
	TunnelRunner bool `json:"tunnel_runner"`
	DDNS         bool `json:"ddns"`
	MCP          bool `json:"mcp"`
	S3WebDAV     bool `json:"s3_webdav"`
}

type CloudflareFeaturesResponse struct {
	Enabled       bool                     `json:"enabled"`
	OAuth         cfoauth.Config           `json:"oauth"`
	Capabilities  cfoauth.CapabilityMatrix `json:"capabilities"`
	Authenticated bool                     `json:"authenticated"`
}

// FeaturesRequest carries partial feature toggle updates.
type FeaturesRequest struct {
	TunnelManager *bool `json:"tunnel_manager,omitempty"`
	DDNS          *bool `json:"ddns,omitempty"`
	MCP           *bool `json:"mcp,omitempty"`
	S3WebDAV      *bool `json:"s3_webdav,omitempty"`
}

func (s *Server) handleFeatures(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		cfg := s.cfgMgr.Get()
		writeJSON(w, s.featuresResponse(r.Context(), cfg))
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req FeaturesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	cfg := s.cfgMgr.Get()
	ddnsChanged := false
	if req.TunnelManager != nil {
		cfg.TunnelManagement.Enabled = *req.TunnelManager
		// DDNS reuses tunnel manager API credentials; disabling the
		// manager must also disable DDNS.
		if !*req.TunnelManager && cfg.DDNS.Enabled {
			cfg.DDNS.Enabled = false
			ddnsChanged = true
		}
	}
	if req.DDNS != nil {
		// DDNS depends on Remote Tunnel Manager for API credentials.
		if *req.DDNS && !cfg.TunnelManagement.Enabled {
			writeAPIError(w, http.StatusBadRequest, fmt.Errorf("DDNS requires Remote Tunnel Manager to be enabled"))
			return
		}
		if cfg.DDNS.Enabled != *req.DDNS {
			ddnsChanged = true
		}
		cfg.DDNS.Enabled = *req.DDNS
	}
	if req.MCP != nil {
		cfg.MCPEnabled = *req.MCP
	}
	if req.S3WebDAV != nil {
		cfg.S3WebDAV.Enabled = *req.S3WebDAV
	}
	if err := s.cfgMgr.Save(cfg); err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	if req.S3WebDAV != nil {
		s.restartS3WebDAVDedicated(context.Background())
	}
	if ddnsChanged {
		s.ddnsSvc.Restart()
	}
	writeJSON(w, s.featuresResponse(r.Context(), cfg))
}

func (s *Server) featuresResponse(ctx context.Context, cfg config.Config) FeaturesResponse {
	runMode := s.effectiveRunMode()
	tunnelManagerEnabled := cfg.TunnelManagement.Enabled
	ddnsEnabled := cfg.DDNS.Enabled
	mcpEnabled := cfg.MCPEnabled
	s3WebDAVEnabled := cfg.S3WebDAV.Enabled
	oauthStatus, _ := s.ensureOAuthService().Status(ctx)
	return FeaturesResponse{
		Mode:           string(runMode),
		ClassicEnabled: true,
		OAuthEnabled:   true,
		Local: LocalFeaturesResponse{
			TunnelRunner: runMode.AutoStartsLocalRunner(),
			DDNS:         ddnsEnabled,
			MCP:          mcpEnabled,
			S3WebDAV:     s3WebDAVEnabled,
		},
		Cloudflare: CloudflareFeaturesResponse{
			Enabled:       true,
			OAuth:         oauthStatus.Config,
			Capabilities:  oauthStatus.Capabilities,
			Authenticated: oauthStatus.LoggedIn,
		},
		TunnelManager: tunnelManagerEnabled,
		DDNS:          ddnsEnabled,
		MCP:           mcpEnabled,
		S3WebDAV:      s3WebDAVEnabled,
		Availability: map[string]s3dav.Availability{
			"s3_webdav": s.s3Svc.FeatureAvailability(ctx, cfg.S3WebDAV),
		},
	}
}

func (s *Server) handleS3Settings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.decorateS3SettingsResponse(s.s3Svc.Settings(r.Context())))
	case http.MethodPost:
		var req s3dav.SettingsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		resp, err := s.s3Svc.SaveSettings(r.Context(), req)
		if err != nil {
			writeS3Error(w, err)
			return
		}
		s.restartS3WebDAVDedicated(context.Background())
		writeJSON(w, s.decorateS3SettingsResponse(resp))
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleS3WebDAVControl(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	switch strings.TrimSpace(req.Action) {
	case "start":
		if err := s.StartS3WebDAVNow(r.Context()); err != nil {
			writeS3Error(w, err)
			return
		}
	case "stop":
		if err := s.StopS3WebDAV(r.Context()); err != nil {
			writeS3Error(w, err)
			return
		}
	default:
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("unsupported WebDAV control action %q", req.Action))
		return
	}
	writeJSON(w, s.decorateS3SettingsResponse(s.s3Svc.Settings(r.Context())))
}

func (s *Server) handleS3Mounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req s3dav.MountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.s3Svc.CreateMount(r.Context(), req)
	if err != nil {
		writeS3Error(w, err)
		return
	}
	writeJSON(w, s.decorateS3SettingsResponse(resp))
}

func (s *Server) handleS3Mount(w http.ResponseWriter, r *http.Request) {
	key := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/s3/mounts/"), "/")
	if key == "" {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("mount key is required"))
		return
	}
	switch r.Method {
	case http.MethodPut, http.MethodPost:
		var req s3dav.MountRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		resp, err := s.s3Svc.SaveMount(r.Context(), key, req)
		if err != nil {
			writeS3Error(w, err)
			return
		}
		writeJSON(w, s.decorateS3SettingsResponse(resp))
	case http.MethodDelete:
		resp, err := s.s3Svc.DeleteMount(r.Context(), key)
		if err != nil {
			writeS3Error(w, err)
			return
		}
		writeJSON(w, s.decorateS3SettingsResponse(resp))
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleS3Test(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req s3dav.MountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.s3Svc.TestConnection(r.Context(), r.URL.Query().Get("mount_key"), req)
	if err != nil {
		writeS3Error(w, err)
		return
	}
	writeJSON(w, resp)
}

func (s *Server) handleS3WebDAVTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req s3dav.MountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.s3Svc.TestWebDAVConnection(r.Context(), r.URL.Query().Get("mount_key"), req)
	if err != nil {
		writeS3Error(w, err)
		return
	}
	writeJSON(w, resp)
}

func (s *Server) handleS3Buckets(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		q := r.URL.Query()
		resp, err := s.s3Svc.ListBucketsFor(r.Context(), s3dav.BucketRequest{
			MountKey:     q.Get("mount_key"),
			AccountID:    q.Get("account_id"),
			Jurisdiction: q.Get("jurisdiction"),
		})
		if err != nil {
			writeS3Error(w, err)
			return
		}
		writeJSON(w, resp)
	case http.MethodPost:
		var req s3dav.CreateBucketRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		if req.MountKey == "" {
			req.MountKey = r.URL.Query().Get("mount_key")
		}
		bucket, err := s.s3Svc.CreateBucket(r.Context(), req)
		if err != nil {
			writeS3Error(w, err)
			return
		}
		writeJSON(w, bucket)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleS3Files(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp, err := s.s3Svc.ListFiles(r.Context(), r.URL.Query().Get("mount_key"), r.URL.Query().Get("path"))
	if err != nil {
		writeS3Error(w, err)
		return
	}
	writeJSON(w, resp)
}

func (s *Server) handleS3Download(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	file, info, err := s.s3Svc.OpenFile(r.Context(), r.URL.Query().Get("mount_key"), r.URL.Query().Get("path"))
	if err != nil {
		writeS3Error(w, err)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": info.Name()}))
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

func (s *Server) handleS3FileObject(w http.ResponseWriter, r *http.Request) {
	rawPath, err := s3ObjectPath(r.URL.Path)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	switch r.Method {
	case http.MethodPut:
		if err := s.s3Svc.WriteFile(r.Context(), r.URL.Query().Get("mount_key"), rawPath, r.Body); err != nil {
			writeS3Error(w, err)
			return
		}
		writeJSON(w, map[string]bool{"success": true})
	case http.MethodDelete:
		if err := s.s3Svc.Delete(r.Context(), r.URL.Query().Get("mount_key"), rawPath); err != nil {
			writeS3Error(w, err)
			return
		}
		writeJSON(w, map[string]bool{"success": true})
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleS3Mkdir(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req s3dav.MkdirRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	if req.MountKey == "" {
		req.MountKey = r.URL.Query().Get("mount_key")
	}
	if err := s.s3Svc.Mkdir(r.Context(), req.MountKey, req.Path); err != nil {
		writeS3Error(w, err)
		return
	}
	writeJSON(w, map[string]bool{"success": true})
}

func (s *Server) handleS3Rename(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req s3dav.RenameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	if req.MountKey == "" {
		req.MountKey = r.URL.Query().Get("mount_key")
	}
	if err := s.s3Svc.Rename(r.Context(), req.MountKey, req.From, req.To); err != nil {
		writeS3Error(w, err)
		return
	}
	writeJSON(w, map[string]bool{"success": true})
}

func (s *Server) handleS3Sync(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.s3Svc.SyncJobs())
		return
	case http.MethodPost:
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req s3dav.SyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.s3Svc.StartSync(r.Context(), req)
	if err != nil {
		writeS3Error(w, err)
		return
	}
	writeJSON(w, resp)
}

func (s *Server) handleS3SyncJob(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/s3/files/sync/")
	if strings.TrimSpace(id) == "" || id == r.URL.Path {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("sync job id is required"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		resp, err := s.s3Svc.SyncJob(id)
		if err != nil {
			writeS3Error(w, err)
			return
		}
		writeJSON(w, resp)
	case http.MethodPost:
		var req s3dav.SyncJobActionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		resp, err := s.s3Svc.ControlSyncJob(id, req.Action)
		if err != nil {
			writeS3Error(w, err)
			return
		}
		writeJSON(w, resp)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
}

func s3ObjectPath(requestPath string) (string, error) {
	raw := strings.TrimPrefix(requestPath, "/api/s3/files/")
	if raw == "" || raw == requestPath {
		return "", fmt.Errorf("object path is required")
	}
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return "", err
	}
	return "/" + strings.TrimPrefix(decoded, "/"), nil
}

func writeS3Error(w http.ResponseWriter, err error) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "requires Cloudflare API Token"), strings.Contains(msg, "permission"), strings.Contains(msg, "not active"):
		writeAPIError(w, http.StatusForbidden, err)
	case strings.Contains(msg, "required"), strings.Contains(msg, "not allowed"), strings.Contains(msg, "bucket name"):
		writeAPIError(w, http.StatusBadRequest, err)
	case strings.Contains(msg, "disabled"), strings.Contains(msg, "not enabled"), strings.Contains(msg, "not configured"):
		writeAPIError(w, http.StatusConflict, err)
	default:
		writeAPIError(w, http.StatusBadGateway, err)
	}
}

func (s *Server) handleMCPTokens(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		tokens, err := s.mcpSvc.Status("/mcp")
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string][]mcpbridge.TokenSummary{"tokens": tokens.Tokens})
	case http.MethodPost:
		var req struct {
			Name  string `json:"name"`
			Token string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		created, err := s.mcpSvc.CreateToken(req.Name, req.Token)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, created)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleMCPToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/mcp/tokens/")
	if err := s.mcpSvc.DeleteToken(id); err != nil {
		writeAPIError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, map[string]bool{"deleted": true})
}

func (s *Server) handleTunnelManagerSettings(w http.ResponseWriter, r *http.Request) {
	tunnelKey := r.URL.Query().Get("tunnel_key")
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.tunnelMgr.SettingsFor(tunnelKey))
	case http.MethodPost:
		var req tunnelmgr.SettingsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		if err := s.tunnelMgr.SaveSettingsFor(tunnelKey, req); err != nil {
			writeAPIError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, s.tunnelMgr.SettingsFor(tunnelKey))
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleTunnelManagerZones(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	zones, err := s.tunnelMgr.ListZonesFor(r.Context(), r.URL.Query().Get("tunnel_key"))
	if err != nil {
		writeTunnelManagerError(w, err)
		return
	}
	writeJSON(w, map[string][]tunnelmgr.ZoneResponse{"zones": zones})
}

func (s *Server) handleTunnelManagerVerifyToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req tunnelmgr.VerifyTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	resp := s.tunnelMgr.VerifyPermissionsFor(r.Context(), r.URL.Query().Get("tunnel_key"), req)
	writeJSON(w, resp)
}

func (s *Server) handleTunnelManagerTunnel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp, err := s.tunnelMgr.FetchTunnelDetailsFor(r.Context(), r.URL.Query().Get("tunnel_key"))
	if err != nil {
		writeTunnelManagerError(w, err)
		return
	}
	writeJSON(w, resp)
}

func (s *Server) handleTunnelManagerConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg, err := s.tunnelMgr.FetchFor(r.Context(), r.URL.Query().Get("tunnel_key"))
	if err != nil {
		writeTunnelManagerError(w, err)
		return
	}
	writeJSON(w, cfg)
}

func (s *Server) handleTunnelManagerEntries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var entry tunnelmgr.IngressRule
	if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	cfg, err := s.tunnelMgr.AddEntryFor(r.Context(), r.URL.Query().Get("tunnel_key"), entry)
	if err != nil {
		writeTunnelManagerError(w, err)
		return
	}
	writeJSON(w, cfg)
}

func (s *Server) handleTunnelManagerEntry(w http.ResponseWriter, r *http.Request) {
	indexText := strings.TrimPrefix(r.URL.Path, "/api/tunnel-manager/entries/")
	index, err := strconv.Atoi(indexText)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, errors.New("invalid entry index"))
		return
	}

	switch r.Method {
	case http.MethodPut:
		var entry tunnelmgr.IngressRule
		if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		cfg, err := s.tunnelMgr.UpdateEntryFor(r.Context(), r.URL.Query().Get("tunnel_key"), index, entry)
		if err != nil {
			writeTunnelManagerError(w, err)
			return
		}
		writeJSON(w, cfg)
	case http.MethodDelete:
		cfg, err := s.tunnelMgr.DeleteEntryFor(r.Context(), r.URL.Query().Get("tunnel_key"), index)
		if err != nil {
			writeTunnelManagerError(w, err)
			return
		}
		writeJSON(w, cfg)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func writeTunnelManagerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, tunnelmgr.ErrDisabled):
		writeAPIError(w, http.StatusConflict, err)
	case strings.Contains(err.Error(), "not found"):
		writeAPIError(w, http.StatusNotFound, err)
	case strings.Contains(err.Error(), "required"), strings.Contains(err.Error(), "out of range"):
		writeAPIError(w, http.StatusBadRequest, err)
	default:
		writeAPIError(w, http.StatusBadGateway, err)
	}
}

func writeAPIError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if encodeErr := json.NewEncoder(w).Encode(map[string]string{"error": err.Error()}); encodeErr != nil {
		logger.Sugar.Errorf("Failed to encode error response: %v", encodeErr)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logger.Sugar.Errorf("Failed to encode JSON response: %v", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

// StartDDNS starts the DDNS background service if configured.
func (s *Server) StartDDNS() {
	s.ddnsSvc.Start()
}

// StopDDNS stops the DDNS background service.
func (s *Server) StopDDNS() {
	s.ddnsSvc.Stop()
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		cfg := s.cfgMgr.Get()
		if err := json.NewEncoder(w).Encode(cfg); err != nil {
			logger.Sugar.Errorf("Failed to encode config: %v", err)
			http.Error(w, "Failed to encode config", http.StatusInternalServerError)
		}
		return
	}

	if r.Method == http.MethodPost {
		cfg := s.cfgMgr.Get()
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			logger.Sugar.Warnf("Invalid config request from %s: %v", r.RemoteAddr, err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := s.cfgMgr.Save(cfg); err != nil {
			logger.Sugar.Errorf("Failed to save config: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		logger.Sugar.Infof("Configuration updated by %s", r.RemoteAddr)
		writeJSON(w, s.cfgMgr.Get())
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

type TunnelsResponse struct {
	ActiveTunnelKey string                       `json:"active_tunnel_key"`
	Tunnels         []config.TunnelProfileConfig `json:"tunnels"`
	// Statuses maps profile key -> live runner status so the UI can show
	// every tunnel's state in one round trip.
	Statuses map[string]StatusResponse `json:"statuses,omitempty"`
}

func (s *Server) tunnelsResponse(cfg config.Config) TunnelsResponse {
	resp := TunnelsResponse{ActiveTunnelKey: cfg.ActiveTunnelKey, Tunnels: cfg.Tunnels}
	if s.runner == nil {
		return resp
	}
	statuses := make(map[string]StatusResponse, len(cfg.Tunnels))
	for _, profile := range cfg.Tunnels {
		st, _ := s.runner.ProfileStatus(profile.Key)
		statuses[profile.Key] = statusResponseFrom(st)
	}
	resp.Statuses = statuses
	return resp
}

func statusResponseFrom(st cloudflared.Status) StatusResponse {
	resp := StatusResponse{Running: st.Running, Protocol: st.Protocol}
	if st.Running {
		resp.Status = "running"
	} else {
		resp.Status = "stopped"
	}
	if st.LastError != nil {
		resp.Error = st.LastError.Error()
		resp.Status = "error"
	}
	return resp
}

func (s *Server) handleTunnels(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.tunnelsResponse(s.cfgMgr.Get()))
	case http.MethodPost:
		var req config.TunnelProfileConfig
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		cfg, err := s.cfgMgr.SaveTunnelProfile("", req)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, s.tunnelsResponse(cfg))
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleTunnel(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/tunnels/"), "/")
	if rest == "" {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("tunnel key is required"))
		return
	}
	parts := strings.Split(rest, "/")
	key := strings.TrimSpace(parts[0])
	action := ""
	if len(parts) > 1 {
		action = strings.TrimSpace(parts[1])
	}

	switch action {
	case "":
		s.handleTunnelProfile(w, r, key)
	case "activate-local":
		s.handleTunnelActivateLocal(w, r, key)
	case "status":
		s.handleTunnelStatus(w, r, key)
	case "control":
		s.handleTunnelControl(w, r, key)
	default:
		writeAPIError(w, http.StatusNotFound, fmt.Errorf("unknown tunnel action %q", action))
	}
}

func (s *Server) handleTunnelProfile(w http.ResponseWriter, r *http.Request, key string) {
	switch r.Method {
	case http.MethodGet:
		tunnel, ok := s.cfgMgr.Get().TunnelProfile(key)
		if !ok {
			writeAPIError(w, http.StatusNotFound, fmt.Errorf("tunnel profile %q not found", key))
			return
		}
		writeJSON(w, tunnel)
	case http.MethodPut, http.MethodPost:
		var req config.TunnelProfileConfig
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		cfg, err := s.cfgMgr.SaveTunnelProfile(key, req)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, s.tunnelsResponse(cfg))
	case http.MethodDelete:
		cfg, err := s.cfgMgr.DeleteTunnelProfile(key)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		// Stop the deleted profile's tunnel asynchronously: the instance may
		// take up to its stop timeout, and the profile is already gone.
		if s.runner != nil {
			go func() {
				if err := s.runner.RemoveProfile(key); err != nil {
					logger.Sugar.Warnf("Error stopping tunnel for deleted profile %q: %v", key, err)
				}
			}()
		}
		writeJSON(w, s.tunnelsResponse(cfg))
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleTunnelActivateLocal(w http.ResponseWriter, r *http.Request, key string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Tunnels run independently per profile, so switching the active profile
	// (which legacy endpoints and the top-level config mirror) no longer
	// requires stopping anything.
	cfg, err := s.cfgMgr.ActivateTunnelProfile(key)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, s.tunnelsResponse(cfg))
}

func (s *Server) handleTunnelStatus(w http.ResponseWriter, r *http.Request, key string) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg := s.cfgMgr.Get()
	if _, ok := cfg.TunnelProfile(key); !ok {
		writeAPIError(w, http.StatusNotFound, fmt.Errorf("tunnel profile %q not found", key))
		return
	}
	if s.runner == nil {
		writeJSON(w, StatusResponse{Running: false, Status: "unavailable"})
		return
	}
	st, _ := s.runner.ProfileStatus(key)
	writeJSON(w, statusResponseFrom(st))
}

func (s *Server) handleTunnelControl(w http.ResponseWriter, r *http.Request, key string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg := s.cfgMgr.Get()
	if _, ok := cfg.TunnelProfile(key); !ok {
		writeAPIError(w, http.StatusNotFound, fmt.Errorf("tunnel profile %q not found", key))
		return
	}
	s.handleControlFor(w, r, key)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.writeRunnerStatus(w)
}

func (s *Server) writeRunnerStatus(w http.ResponseWriter) {
	if s.runner == nil {
		writeJSON(w, StatusResponse{Running: false, Status: "unavailable"})
		return
	}
	running, err, protocol := s.runner.Status()
	status := "stopped"
	if running {
		status = "running"
	}

	resp := statusResponsePool.Get()
	defer statusResponsePool.Put(resp)

	resp.Running = running
	resp.Status = status
	resp.Protocol = protocol
	if err != nil {
		resp.Error = err.Error()
		resp.Status = "error"
		logger.Sugar.Warnf("Tunnel status error: %v", err)
	}

	if encodeErr := json.NewEncoder(w).Encode(resp); encodeErr != nil {
		logger.Sugar.Errorf("Failed to encode status response: %v", encodeErr)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

func (s *Server) handleControl(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Legacy endpoint: controls the active profile.
	s.handleControlFor(w, r, "")
}

// handleControlFor starts or stops the tunnel of one profile (""= active).
func (s *Server) handleControlFor(w http.ResponseWriter, r *http.Request, key string) {
	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.Sugar.Warnf("Invalid control request from %s: %v", r.RemoteAddr, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	label := key
	if label == "" {
		label = "active"
	}

	switch req.Action {
	case "start":
		logger.Sugar.Infof("Starting tunnel %q (requested by %s)", label, r.RemoteAddr)
		if err := s.runner.StartProfile(key); err != nil {
			logger.Sugar.Errorf("Failed to start tunnel %q: %v", label, err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		logger.Sugar.Infof("Tunnel %q started successfully", label)
	case "stop":
		logger.Sugar.Infof("Stopping tunnel %q (requested by %s)", label, r.RemoteAddr)
		// For stop action, respond immediately and stop asynchronously
		// This prevents the client from getting "Failed to fetch" when the tunnel shuts down
		resp := controlResponsePool.Get()
		resp.Success = true
		resp.Action = "stop"
		resp.Message = "Tunnel stop initiated"

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		encodeErr := json.NewEncoder(w).Encode(resp)
		controlResponsePool.Put(resp)

		if encodeErr != nil {
			logger.Sugar.Errorf("Failed to encode stop response: %v", encodeErr)
		}
		go func() {
			if stopErr := s.runner.StopProfile(key); stopErr != nil {
				logger.Sugar.Errorf("Error stopping tunnel %q: %v", label, stopErr)
			} else {
				logger.Sugar.Infof("Tunnel %q stopped successfully", label)
			}
		}()
		return
	default:
		logger.Sugar.Warnf("Invalid action '%s' from %s", req.Action, r.RemoteAddr)
		http.Error(w, "Invalid action", http.StatusBadRequest)
		return
	}

	resp := controlResponsePool.Get()
	defer controlResponsePool.Put(resp)

	resp.Success = true
	resp.Action = req.Action
	resp.Message = "Tunnel started successfully"

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if encodeErr := json.NewEncoder(w).Encode(resp); encodeErr != nil {
		logger.Sugar.Errorf("Failed to encode control response: %v", encodeErr)
	}
}

func (s *Server) handleI18n(w http.ResponseWriter, r *http.Request) {
	// Extract language from path: /api/i18n/en -> "en"
	lang := r.URL.Path[len("/api/i18n/"):]
	if lang == "" {
		lang = "en"
	}
	if !isValidLangCode(lang) {
		http.Error(w, "Language not found", http.StatusNotFound)
		return
	}

	// Read the corresponding TOML file
	filePath := "locales/" + lang + ".toml"
	data, err := s.locales.ReadFile(filePath)
	if err != nil {
		logger.Sugar.Warnf("Language file not found: %s (requested by %s)", lang, r.RemoteAddr)
		http.Error(w, "Language not found", http.StatusNotFound)
		return
	}

	// Parse TOML into a map
	var translations map[string]map[string]string
	if err := toml.Unmarshal(data, &translations); err != nil {
		logger.Sugar.Errorf("Failed to parse translations for %s: %v", lang, err)
		http.Error(w, "Failed to parse translations", http.StatusInternalServerError)
		return
	}

	// Convert to simplified format: key -> translation
	simple := make(map[string]string)
	for key, value := range translations {
		if other, ok := value["other"]; ok {
			simple[key] = other
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if encodeErr := json.NewEncoder(w).Encode(simple); encodeErr != nil {
		logger.Sugar.Errorf("Failed to encode i18n response for %s: %v", lang, encodeErr)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

// isValidLangCode accepts short locale codes like "en", "zh", "zh-cn".
func isValidLangCode(lang string) bool {
	if len(lang) == 0 || len(lang) > 16 {
		return false
	}
	for _, r := range lang {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// handleLogStream streams logs to client using Server-Sent Events (SSE)
func (s *Server) handleLogStream(w http.ResponseWriter, r *http.Request) {
	// Set headers for SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	broadcaster := logger.GetBroadcaster()
	if broadcaster == nil {
		logger.Sugar.Error("Log broadcaster not initialized")
		http.Error(w, "Log streaming not available", http.StatusInternalServerError)
		return
	}

	// Subscribe to log broadcasts with client address for tracking
	logChan := broadcaster.Subscribe(r.RemoteAddr)
	defer broadcaster.Unsubscribe(logChan)

	// Get flusher for SSE
	flusher, ok := w.(http.Flusher)
	if !ok {
		logger.Sugar.Error("Streaming not supported")
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	logger.Sugar.Infof("Log stream client connected: %s", r.RemoteAddr)

	// Send initial recent logs
	recentLogs := broadcaster.GetRecentLogs()
	for _, line := range recentLogs {
		_, err := w.Write([]byte("data: " + line + "\n\n"))
		if err != nil {
			logger.Sugar.Warnf("Failed to send recent logs to %s: %v", r.RemoteAddr, err)
			return
		}
	}
	flusher.Flush()

	// Stream new logs with periodic heartbeat to detect dead connections
	ctx := r.Context()
	heartbeatTicker := time.NewTicker(30 * time.Second)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Sugar.Infof("Log stream client disconnected: %s", r.RemoteAddr)
			return
		case <-s.shutdownC:
			// Server is shutting down; close the stream so http.Server.Shutdown
			// does not wait for its full timeout.
			logger.Sugar.Infof("Log stream closed for shutdown: %s", r.RemoteAddr)
			return
		case <-heartbeatTicker.C:
			// Send SSE comment as heartbeat to detect dead connections
			_, err := w.Write([]byte(": heartbeat\n\n"))
			if err != nil {
				logger.Sugar.Warnf("Heartbeat failed for %s, closing connection: %v", r.RemoteAddr, err)
				return
			}
			flusher.Flush()
			// Mark subscriber as active
			broadcaster.MarkActive(logChan)
		case logLine, ok := <-logChan:
			if !ok {
				logger.Sugar.Infof("Log channel closed for %s", r.RemoteAddr)
				return
			}
			// Send log line as SSE event
			_, err := w.Write([]byte("data: " + logLine + "\n\n"))
			if err != nil {
				logger.Sugar.Warnf("Failed to send log to %s: %v", r.RemoteAddr, err)
				return
			}
			flusher.Flush()
			// Activity is already updated in Broadcast() on successful send
		}
	}
}

// handleRecentLogs returns recent logs from the circular buffer
func (s *Server) handleRecentLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	broadcaster := logger.GetBroadcaster()
	if broadcaster == nil {
		logger.Sugar.Error("Log broadcaster not initialized")
		http.Error(w, "Log broadcaster not available", http.StatusInternalServerError)
		return
	}

	recentLogs := broadcaster.GetRecentLogs()

	resp := recentLogsResponsePool.Get()
	defer recentLogsResponsePool.Put(resp)

	resp.Logs = recentLogs
	resp.Count = len(recentLogs)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logger.Sugar.Errorf("Failed to encode recent logs response: %v", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

// handleVersion returns version information
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp := versionResponsePool.Get()
	defer versionResponsePool.Put(resp)

	resp.Version = version.GetVersion()
	resp.BuildTime = version.BuildTime
	resp.GitCommit = version.GitCommit
	resp.FullInfo = version.GetFullVersion()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logger.Sugar.Errorf("Failed to encode version response: %v", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

// DDNS handlers

func (s *Server) handleDDNSConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.ddnsSvc.GetConfig())
	case http.MethodPost:
		var req ddns.SaveRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		if err := s.ddnsSvc.SaveConfig(req); err != nil {
			writeAPIError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, s.ddnsSvc.GetConfig())
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleDDNSStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.ddnsSvc.Status())
}

func (s *Server) handleDDNSSyncNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	status, err := s.ddnsSvc.SyncNow(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, status)
}

func (s *Server) handleDDNSZones(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	zones, err := s.ddnsSvc.ListZones(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, map[string][]ddns.ZoneResponse{"zones": zones})
}

func (s *Server) handleDDNSRecords(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg := s.cfgMgr.Get()
	var req ddns.AddRecordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	req.Subdomain = strings.TrimSpace(req.Subdomain)
	req.ZoneID = strings.TrimSpace(req.ZoneID)
	req.ZoneName = strings.TrimSpace(req.ZoneName)
	req.Comment = config.NormalizeDDNSRecordComment(req.Comment)
	if req.Subdomain == "" || req.ZoneID == "" {
		writeAPIError(w, http.StatusBadRequest, errors.New("subdomain and zone_id are required"))
		return
	}
	if !req.IPv4 && !req.IPv6 {
		writeAPIError(w, http.StatusBadRequest, errors.New("at least one of ipv4 or ipv6 must be selected"))
		return
	}
	if req.TTL <= 0 {
		req.TTL = 1
	}

	// Resolve zone name if not provided
	if req.ZoneName == "" {
		if zones, err := s.tunnelMgr.ListZones(r.Context()); err == nil {
			for _, z := range zones {
				if z.ID == req.ZoneID {
					req.ZoneName = z.Name
					break
				}
			}
		}
	}
	if req.ZoneName == "" {
		writeAPIError(w, http.StatusBadRequest, errors.New("zone_name is required"))
		return
	}

	hostname := req.Subdomain + "." + req.ZoneName
	if req.IPv4 {
		value, err := ddns.ValidateRecordValue("A", req.IPv4Value)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, fmt.Errorf("ipv4_value: %w", err))
			return
		}
		cfg.DDNS.Records = append(cfg.DDNS.Records, config.DDNSRecord{
			Name: hostname, ZoneID: req.ZoneID, ZoneName: req.ZoneName,
			Type: "A", Value: value, Comment: req.Comment, Proxied: req.Proxied, TTL: req.TTL,
		})
	}
	if req.IPv6 {
		value, err := ddns.ValidateRecordValue("AAAA", req.IPv6Value)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, fmt.Errorf("ipv6_value: %w", err))
			return
		}
		cfg.DDNS.Records = append(cfg.DDNS.Records, config.DDNSRecord{
			Name: hostname, ZoneID: req.ZoneID, ZoneName: req.ZoneName,
			Type: "AAAA", Value: value, Comment: req.Comment, Proxied: req.Proxied, TTL: req.TTL,
		})
	}
	if err := s.cfgMgr.Save(cfg); err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	s.ddnsSvc.Restart()
	writeJSON(w, s.ddnsSvc.GetConfig())
}

func (s *Server) handleDDNSRecord(w http.ResponseWriter, r *http.Request) {
	indexText := strings.TrimPrefix(r.URL.Path, "/api/ddns/records/")
	index, err := strconv.Atoi(indexText)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, errors.New("invalid record index"))
		return
	}

	cfg := s.cfgMgr.Get()
	if index < 0 || index >= len(cfg.DDNS.Records) {
		writeAPIError(w, http.StatusNotFound, errors.New("record not found"))
		return
	}

	switch r.Method {
	case http.MethodPut:
		var req ddns.AddRecordRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		rec := &cfg.DDNS.Records[index]
		subdomain := strings.TrimSpace(req.Subdomain)
		if subdomain == "" {
			subdomain = ddnsRecordSubdomain(rec.Name, rec.ZoneName)
		}
		zoneID := strings.TrimSpace(req.ZoneID)
		if zoneID == "" {
			zoneID = rec.ZoneID
		}
		zoneName := strings.TrimSpace(req.ZoneName)
		if zoneName == "" {
			zoneName = rec.ZoneName
		}
		if zoneName == "" || zoneID == "" {
			writeAPIError(w, http.StatusBadRequest, errors.New("zone_id and zone_name are required"))
			return
		}
		hostname := subdomain + "." + zoneName
		value, err := ddns.ValidateRecordValue(rec.Type, req.Value)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, fmt.Errorf("value: %w", err))
			return
		}
		rec.Name = hostname
		rec.ZoneID = zoneID
		rec.ZoneName = zoneName
		rec.Value = value
		rec.Comment = config.NormalizeDDNSRecordComment(req.Comment)
		rec.Proxied = req.Proxied
		rec.TTL = req.TTL
		if rec.TTL <= 0 {
			rec.TTL = 1
		}
		if err := s.cfgMgr.Save(cfg); err != nil {
			writeAPIError(w, http.StatusInternalServerError, err)
			return
		}
		s.ddnsSvc.Restart()
		writeJSON(w, s.ddnsSvc.GetConfig())
	case http.MethodDelete:
		cfg.DDNS.Records = append(cfg.DDNS.Records[:index], cfg.DDNS.Records[index+1:]...)
		if err := s.cfgMgr.Save(cfg); err != nil {
			writeAPIError(w, http.StatusInternalServerError, err)
			return
		}
		s.ddnsSvc.Restart()
		writeJSON(w, s.ddnsSvc.GetConfig())
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func ddnsRecordSubdomain(hostname, zoneName string) string {
	hostname = strings.TrimSpace(hostname)
	zoneName = strings.TrimSpace(zoneName)
	if hostname == "" || zoneName == "" {
		return hostname
	}

	suffix := "." + zoneName
	if strings.HasSuffix(hostname, suffix) {
		return strings.TrimSuffix(hostname, suffix)
	}
	return hostname
}
