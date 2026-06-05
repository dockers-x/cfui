package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"cfui/internal/config"
	"cfui/internal/logger"
	"cfui/internal/s3dav"
	"cfui/internal/tunnelmgr"
)

type s3DedicatedServer struct {
	mu      sync.Mutex
	server  *http.Server
	addr    string
	running bool
	errMsg  string
}

func newS3DedicatedServer() *s3DedicatedServer {
	return &s3DedicatedServer{}
}

func (s *Server) StartS3WebDAV() {
	if s.s3WebDAV == nil {
		s.s3WebDAV = newS3DedicatedServer()
	}
	s.reconcileS3WebDAVDedicated(context.Background(), false)
}

func (s *Server) StopS3WebDAV(ctx context.Context) error {
	if s.s3WebDAV == nil {
		return nil
	}
	return s.s3WebDAV.stop(ctx, "")
}

func (s *Server) StartS3WebDAVNow(ctx context.Context) error {
	return s.startS3WebDAVDedicated(ctx)
}

func (s *Server) mainWebDAVHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := s.cfgMgr.Get().S3WebDAV
		if !cfg.Enabled || normalizeS3WebDAVAccessMode(cfg.WebDAVAccessMode) != config.S3WebDAVAccessModeMain {
			http.NotFound(w, r)
			return
		}
		s.s3Svc.Handler().ServeHTTP(w, r)
	})
}

func (s *Server) dedicatedWebDAVHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := s.cfgMgr.Get().S3WebDAV
		if !cfg.Enabled || normalizeS3WebDAVAccessMode(cfg.WebDAVAccessMode) != config.S3WebDAVAccessModeDedicated {
			http.NotFound(w, r)
			return
		}
		s.s3Svc.Handler().ServeHTTP(w, r)
	})
}

func (s *Server) restartS3WebDAVDedicated(ctx context.Context) {
	s.reconcileS3WebDAVDedicated(ctx, true)
}

func (s *Server) reconcileS3WebDAVDedicated(ctx context.Context, keepRunning bool) {
	if s.s3WebDAV == nil {
		s.s3WebDAV = newS3DedicatedServer()
	}
	cfg := s.cfgMgr.Get().S3WebDAV
	if !cfg.Enabled || normalizeS3WebDAVAccessMode(cfg.WebDAVAccessMode) != config.S3WebDAVAccessModeDedicated {
		if err := s.s3WebDAV.stop(ctx, ""); err != nil && logger.Sugar != nil {
			logger.Sugar.Warnf("Failed to stop dedicated S3 WebDAV server: %v", err)
		}
		return
	}
	shouldStart := (!keepRunning && cfg.DedicatedAutoStart) || (keepRunning && s.s3WebDAV.isRunning())
	if !shouldStart {
		if err := s.s3WebDAV.stop(ctx, ""); err != nil && logger.Sugar != nil {
			logger.Sugar.Warnf("Failed to stop dedicated S3 WebDAV server: %v", err)
		}
		return
	}
	if err := s.startS3WebDAVDedicated(ctx); err != nil && logger.Sugar != nil {
		addr, _ := s3DedicatedAddr(cfg)
		logger.Sugar.Warnf("Failed to start dedicated S3 WebDAV server on %s: %v", addr, err)
	}
}

func (s *Server) startS3WebDAVDedicated(ctx context.Context) error {
	if s.s3WebDAV == nil {
		s.s3WebDAV = newS3DedicatedServer()
	}
	cfg := s.cfgMgr.Get().S3WebDAV
	if !cfg.Enabled {
		return errors.New("S3 WebDAV is disabled")
	}
	if normalizeS3WebDAVAccessMode(cfg.WebDAVAccessMode) != config.S3WebDAVAccessModeDedicated {
		return errors.New("dedicated WebDAV access mode is not enabled")
	}
	addr, err := s3DedicatedAddr(cfg)
	if err != nil {
		s.s3WebDAV.setError(err)
		return err
	}
	return s.s3WebDAV.ensure(ctx, addr, s.dedicatedWebDAVHandler())
}

func (s *Server) decorateS3SettingsResponse(resp s3dav.SettingsResponse) s3dav.SettingsResponse {
	if s.s3WebDAV == nil {
		return resp
	}
	running, addr, errMsg := s.s3WebDAV.snapshot()
	resp.DedicatedRunning = running
	resp.DedicatedAddress = addr
	resp.DedicatedError = errMsg
	resp = s.decorateS3TunnelStatus(resp)
	return resp
}

func (s *Server) decorateS3TunnelStatus(resp s3dav.SettingsResponse) s3dav.SettingsResponse {
	if resp.DedicatedDomainMode != config.S3WebDAVDomainModeTunnel {
		return resp
	}
	if strings.TrimSpace(resp.DedicatedTunnelHostname) == "" {
		resp.DedicatedTunnelStatus = tunnelmgr.S3WebDAVTunnelStatusMissing
		resp.DedicatedTunnelStatusMessage = "Tunnel hostname is not configured."
		return resp
	}
	if s.tunnelMgr == nil {
		resp.DedicatedTunnelStatus = tunnelmgr.S3WebDAVTunnelStatusUnavailable
		resp.DedicatedTunnelStatusMessage = "Tunnel Manager is unavailable."
		return resp
	}
	status := s.tunnelMgr.CheckS3WebDAVHostname(context.Background(), resp.DedicatedTunnelHostname, s3DedicatedTunnelService(resp.DedicatedPort))
	resp.DedicatedTunnelStatus = status.Status
	resp.DedicatedTunnelStatusMessage = status.Message
	return resp
}

func (s *s3DedicatedServer) ensure(ctx context.Context, addr string, handler http.Handler) error {
	s.mu.Lock()
	if s.server != nil && s.addr == addr && s.running {
		s.errMsg = ""
		s.mu.Unlock()
		return nil
	}
	old := s.server
	s.server = nil
	s.running = false
	s.mu.Unlock()

	if old != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := old.Shutdown(shutdownCtx); err != nil {
			_ = old.Close()
		}
		cancel()
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		s.setError(err)
		return err
	}
	server := &http.Server{
		Handler: ChainMiddleware(handler, LoggingMiddleware, PanicRecoveryMiddleware),
	}
	s.mu.Lock()
	s.server = server
	s.addr = ln.Addr().String()
	s.running = true
	s.errMsg = ""
	s.mu.Unlock()

	go func() {
		err := server.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.mu.Lock()
			if s.server == server {
				s.running = false
				s.errMsg = err.Error()
			}
			s.mu.Unlock()
			if logger.Sugar != nil {
				logger.Sugar.Errorf("Dedicated S3 WebDAV server stopped unexpectedly: %v", err)
			}
		}
	}()
	if logger.Sugar != nil {
		logger.Sugar.Infof("Dedicated S3 WebDAV server listening on %s", s.addr)
	}
	return nil
}

func (s *s3DedicatedServer) stop(ctx context.Context, errMsg string) error {
	s.mu.Lock()
	server := s.server
	s.server = nil
	s.running = false
	s.addr = ""
	s.errMsg = errMsg
	s.mu.Unlock()
	if server == nil {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
		return err
	}
	return nil
}

func (s *s3DedicatedServer) setError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
	if err != nil {
		s.errMsg = err.Error()
	}
}

func (s *s3DedicatedServer) snapshot() (bool, string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running, s.addr, s.errMsg
}

func (s *s3DedicatedServer) isRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func s3DedicatedAddr(cfg config.S3WebDAVConfig) (string, error) {
	port := cfg.DedicatedPort
	if port <= 0 {
		port = 14334
	}
	if port > 65535 {
		return "", errors.New("dedicated WebDAV port must be between 1 and 65535")
	}
	host := strings.TrimSpace(cfg.DedicatedBindHost)
	if host == "" {
		host = "0.0.0.0"
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func s3DedicatedTunnelService(port int) string {
	if port <= 0 {
		port = 14334
	}
	return "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

func normalizeS3WebDAVAccessMode(mode string) string {
	if strings.TrimSpace(mode) == config.S3WebDAVAccessModeDedicated {
		return config.S3WebDAVAccessModeDedicated
	}
	return config.S3WebDAVAccessModeMain
}
