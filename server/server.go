package server

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"

	"cfui/config"
	"cfui/logger"
	"cfui/service"

	"github.com/BurntSushi/toml"
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

func (s *Server) Run(addr string) error {
	mux := http.NewServeMux()

	// API Endpoints
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/control", s.handleControl)
	mux.HandleFunc("/api/i18n/", s.handleI18n)

	// Static Files
	// The assets are in "web/dist", so we need to strip that prefix
	fsys, err := fs.Sub(s.assets, "web/dist")
	if err != nil {
		logger.Sugar.Errorf("Failed to create sub filesystem: %v", err)
		return err
	}
	mux.Handle("/", http.FileServer(http.FS(fsys)))

	// Apply middleware chain: logging -> panic recovery -> handler
	handler := ChainMiddleware(mux, LoggingMiddleware, PanicRecoveryMiddleware)

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
	running, err := s.runner.Status()
	status := "stopped"
	if running {
		status = "running"
	}

	resp := map[string]interface{}{
		"running": running,
		"status":  status,
	}
	if err != nil {
		resp["error"] = err.Error()
		resp["status"] = "error"
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
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if encodeErr := json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"action":  "stop",
			"message": "Tunnel stop initiated",
		}); encodeErr != nil {
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

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if encodeErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"action":  req.Action,
		"message": "Tunnel started successfully",
	}); encodeErr != nil {
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
