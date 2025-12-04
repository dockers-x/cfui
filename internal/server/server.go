package server

import (
	"cfui/internal/config"
	"cfui/internal/logger"
	"cfui/internal/pool"
	"cfui/internal/service"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
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
	cfgMgr  *config.Manager
	runner  *service.Runner
	assets  embed.FS
	locales embed.FS
}

func NewServer(cfgMgr *config.Manager, runner *service.Runner, assets embed.FS, locales embed.FS) *Server {
	return &Server{
		cfgMgr:  cfgMgr,
		runner:  runner,
		assets:  assets,
		locales: locales,
	}
}

// GetHandler creates and returns the HTTP handler
func (s *Server) GetHandler() http.Handler {
	mux := http.NewServeMux()

	// API Endpoints
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/control", s.handleControl)
	mux.HandleFunc("/api/version", s.handleVersion)
	mux.HandleFunc("/api/i18n/", s.handleI18n)
	mux.HandleFunc("/api/logs/stream", s.handleLogStream)
	mux.HandleFunc("/api/logs/recent", s.handleRecentLogs)

	// Static Files
	// The assets are in "web/dist", so we need to strip that prefix
	fsys, err := fs.Sub(s.assets, "web/dist")
	if err != nil {
		logger.Sugar.Errorf("Failed to create sub filesystem: %v", err)
		panic(err)
	}
	mux.Handle("/", http.FileServer(http.FS(fsys)))

	// Apply middleware chain: logging -> panic recovery -> handler
	return ChainMiddleware(mux, LoggingMiddleware, PanicRecoveryMiddleware)
}

func (s *Server) Run(addr string) error {
	handler := s.GetHandler()
	logger.Sugar.Infof("Server listening on %s", addr)
	return http.ListenAndServe(addr, handler)
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
		var cfg config.Config
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
		w.WriteHeader(http.StatusOK)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
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

	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.Sugar.Warnf("Invalid control request from %s: %v", r.RemoteAddr, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var err error
	switch req.Action {
	case "start":
		logger.Sugar.Infof("Starting tunnel (requested by %s)", r.RemoteAddr)
		err = s.runner.Start()
		if err != nil {
			logger.Sugar.Errorf("Failed to start tunnel: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		logger.Sugar.Info("Tunnel started successfully")
	case "stop":
		logger.Sugar.Infof("Stopping tunnel (requested by %s)", r.RemoteAddr)
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
			if stopErr := s.runner.Stop(); stopErr != nil {
				logger.Sugar.Errorf("Error stopping tunnel: %v", stopErr)
			} else {
				logger.Sugar.Info("Tunnel stopped successfully")
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
