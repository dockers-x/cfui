package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"cfui/config"
	"cfui/logger"
	"cfui/version"

	"github.com/cloudflare/cloudflared/cmd/cloudflared/cliutil"
	"github.com/cloudflare/cloudflared/cmd/cloudflared/tunnel"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/urfave/cli/v2"
)

// safeRegisterer wraps a Prometheus registry and gracefully handles duplicate registrations
// This prevents panics when cloudflared attempts to register metrics multiple times
type safeRegisterer struct {
	prometheus.Registerer
}

func newSafeRegisterer(reg prometheus.Registerer) prometheus.Registerer {
	return &safeRegisterer{Registerer: reg}
}

func (s *safeRegisterer) Register(c prometheus.Collector) error {
	err := s.Registerer.Register(c)
	if err != nil {
		// Check if this is a duplicate registration error by examining the error string
		// This is more reliable than type assertion across different prometheus versions
		errStr := err.Error()
		if strings.Contains(errStr, "duplicate") || strings.Contains(errStr, "already registered") {
			logger.Sugar.Debugf("Collector already registered (ignored): %v", err)
			return nil // Silently ignore duplicate registration
		}
		return err
	}
	return nil
}

func (s *safeRegisterer) MustRegister(cs ...prometheus.Collector) {
	for _, c := range cs {
		if err := s.Register(c); err != nil {
			// Only panic if it's not a duplicate registration error
			errStr := err.Error()
			if !strings.Contains(errStr, "duplicate") && !strings.Contains(errStr, "already registered") {
				panic(err)
			}
		}
	}
}

// Runner manages the cloudflared tunnel process
type Runner struct {
	cfgMgr            *config.Manager
	ctx               context.Context
	cancel            context.CancelFunc
	wg                sync.WaitGroup
	mu                sync.Mutex
	running           bool
	lastError         error
	restartCount      int
	lastRestart       time.Time
	configFile        string // Track temporary config file for cleanup
	gracefulShutdownC chan struct{}
	initOnce          sync.Once            // Ensure tunnel.Init() is called only once
	metricsRegistry   *prometheus.Registry // Current Prometheus registry for tunnel metrics

	// Protocol fallback management (for auto mode)
	currentProtocol     string         // Currently active protocol (quic, http2, or auto)
	protocolFailures    map[string]int // Track consecutive failures per protocol
	lastProtocolSwitch  time.Time      // Last time we switched protocols
	protocolSwitchCount int            // Number of times we've switched protocols
}

func NewRunner(cfgMgr *config.Manager) *Runner {
	r := &Runner{
		cfgMgr:            cfgMgr,
		gracefulShutdownC: make(chan struct{}),
		protocolFailures:  make(map[string]int),
		currentProtocol:   "auto", // Start with auto
	}
	return r
}

// initTunnel initializes the cloudflared tunnel package with required build info
// Uses the software name from config
// IMPORTANT: This can only be called ONCE due to cloudflared's metrics registration
func (r *Runner) initTunnel() {
	r.initOnce.Do(func() {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Sugar.Errorf("Panic during tunnel initialization: %v", rec)
				// Do NOT re-panic - let the initialization fail gracefully
				// The tunnel will not start, but the cfui process will continue running
			}
		}()

		cfg := r.cfgMgr.Get()
		softwareName := cfg.SoftwareName
		if softwareName == "" {
			softwareName = "cfui" // Fallback to default
		}

		version.ChangeSoftName(softwareName)
		buildInfo := cliutil.GetBuildInfo("dockers-x", version.GetFullVersion())
		tunnel.Init(buildInfo, r.gracefulShutdownC)
		logger.Sugar.Infof("Cloudflared tunnel initialized successfully (software: %s, version: %s)", softwareName, version.GetFullVersion())
	})
}

// Start launches the cloudflared tunnel
func (r *Runner) Start() (err error) {
	// Add panic protection at the outermost level to prevent any initialization panic
	// from crashing the entire cfui process
	defer func() {
		if rec := recover(); rec != nil {
			logger.Sugar.Errorf("Panic during tunnel start (recovered): %v", rec)
			// Don't try to lock here as we might already hold the lock
			// Just set the error and let the caller handle it
			err = fmt.Errorf("start panic: %v", rec)
		}
	}()

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		logger.Sugar.Warn("Attempted to start tunnel that is already running")
		return fmt.Errorf("already running")
	}

	cfg := r.cfgMgr.Get()
	if cfg.Token == "" {
		logger.Sugar.Error("Cannot start tunnel: token is missing")
		return fmt.Errorf("token is required")
	}

	// Initialize tunnel once (uses sync.Once internally)
	// Note: Software name can only be set on FIRST initialization
	// To change software name, you must restart the entire cfui process
	r.initTunnel()

	// Create a new Prometheus registry for this tunnel run
	// Cloudflared registers metrics on each tunnel start (not just on Init)
	// By creating a safe registerer wrapper, we prevent duplicate registration panics
	// while still allowing metrics collection to work
	r.metricsRegistry = prometheus.NewRegistry()
	prometheus.DefaultRegisterer = newSafeRegisterer(r.metricsRegistry)
	logger.Sugar.Debug("Created new Prometheus registry with safe registerer wrapper")

	// Cancel any existing context to prevent context leak
	if r.cancel != nil {
		r.cancel()
	}

	r.ctx, r.cancel = context.WithCancel(context.Background())
	r.running = true
	r.lastError = nil

	logger.Sugar.Info("Starting cloudflared tunnel")
	r.wg.Add(1)
	go r.runTunnel(r.ctx, cfg.Token)

	return nil
}

// Stop terminates the tunnel gracefully with timeout
func (r *Runner) Stop() error {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		logger.Sugar.Debug("Stop called but tunnel is not running")
		return nil
	}

	logger.Sugar.Info("Initiating tunnel shutdown")
	// Cancel the context to signal shutdown
	if r.cancel != nil {
		r.cancel()
	}

	// Signal graceful shutdown to cloudflared
	select {
	case r.gracefulShutdownC <- struct{}{}:
		logger.Sugar.Debug("Graceful shutdown signal sent")
	default:
		// Channel might be full or not being read, continue anyway
		logger.Sugar.Debug("Graceful shutdown channel unavailable")
	}
	r.mu.Unlock()

	// Wait for goroutine to complete with timeout
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Sugar.Info("Tunnel stopped gracefully")
		// Ensure running state is cleared and cleanup resources
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
		// Config file is already cleaned up in runTunnel's defer
		return nil
	case <-time.After(30 * time.Second):
		logger.Sugar.Warn("Tunnel stop timeout exceeded (30s)")
		// Force set running to false even on timeout
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
		// Try to cleanup config file even on timeout
		r.cleanupConfigFile()
		return fmt.Errorf("timeout waiting for tunnel to stop")
	}
}

func (r *Runner) Status() (bool, error, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running, r.lastError, r.currentProtocol
}

// GetMetricsRegistry returns the current Prometheus registry used by the tunnel.
// This can be used to expose metrics via an HTTP endpoint in the future.
// Returns nil if the tunnel is not running or hasn't been started yet.
// GetMetricsRegistry returns the current Prometheus registry used by the tunnel.
// This can be used to expose metrics via an HTTP endpoint in the future.
// Returns nil if the tunnel is not running or hasn't been started yet.
func (r *Runner) GetMetricsRegistry() *prometheus.Registry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.metricsRegistry
}

// selectProtocol determines which protocol to use based on configuration and failure history
// This method should be called with the mutex held
func (r *Runner) selectProtocol(configProtocol string) string {
	// If user explicitly specified a protocol (not auto), always use that
	if configProtocol != "" && configProtocol != "auto" {
		r.currentProtocol = configProtocol
		return configProtocol
	}

	// Auto mode: implement intelligent fallback
	// Priority order: quic -> http2 -> quic (cycle)

	const (
		maxFailuresBeforeSwitch = 3                // Switch after 3 consecutive failures
		protocolCooldown        = 10 * time.Minute // Wait 10 minutes before retrying a failed protocol
	)

	// If current protocol has too many failures, try to switch
	if r.protocolFailures[r.currentProtocol] >= maxFailuresBeforeSwitch {
		// Determine next protocol to try
		var nextProtocol string
		if r.currentProtocol == "quic" || r.currentProtocol == "auto" {
			nextProtocol = "http2"
		} else {
			nextProtocol = "quic"
		}

		logger.Sugar.Warnf("Protocol %s has failed %d times, switching to %s",
			r.currentProtocol, r.protocolFailures[r.currentProtocol], nextProtocol)

		// Important: Reset the CURRENT protocol's failure count when switching away from it
		// This ensures that if we switch back later, it gets a fresh start
		r.protocolFailures[r.currentProtocol] = 0

		r.currentProtocol = nextProtocol
		r.lastProtocolSwitch = time.Now()
		r.protocolSwitchCount++

		return nextProtocol
	}

	// Default to current protocol or quic if not set
	if r.currentProtocol == "" || r.currentProtocol == "auto" {
		r.currentProtocol = "quic"
	}

	return r.currentProtocol
}

// recordProtocolSuccess resets failure count for the current protocol
// Also clears all protocol failure counts if connection has been stable
func (r *Runner) recordProtocolSuccess() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.currentProtocol != "" && r.currentProtocol != "auto" {
		logger.Sugar.Infof("Protocol %s connected successfully, resetting failure counts", r.currentProtocol)

		// Reset current protocol's failure count
		r.protocolFailures[r.currentProtocol] = 0

		// Also reset restart count on successful connection
		r.restartCount = 0

		// If we've had a successful connection for a while (implied by clean exit),
		// clear all protocol failure history to give other protocols a fresh chance
		// This prevents permanent blacklisting of protocols after temporary issues
		for proto := range r.protocolFailures {
			r.protocolFailures[proto] = 0
		}
		logger.Sugar.Debug("Cleared all protocol failure history after successful connection")
	}
}

// recordProtocolFailure increments failure count for the current protocol
func (r *Runner) recordProtocolFailure(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.currentProtocol == "" || r.currentProtocol == "auto" {
		r.currentProtocol = "quic" // Assume quic if not set
	}

	// Only count certain types of errors as protocol failures
	if isProtocolRelatedError(err) {
		r.protocolFailures[r.currentProtocol]++
		logger.Sugar.Warnf("Protocol %s failure count: %d (error: %v)",
			r.currentProtocol, r.protocolFailures[r.currentProtocol], err)
	}
}

// isProtocolRelatedError determines if an error is related to protocol issues
func isProtocolRelatedError(err error) bool {
	if err == nil {
		return false
	}

	errMsg := strings.ToLower(err.Error())

	// QUIC-specific errors
	quicErrors := []string{
		"quic",
		"timeout: no recent network activity",
		"failed to dial to edge with quic",
		"failed to accept quic stream",
	}

	for _, pattern := range quicErrors {
		if strings.Contains(errMsg, pattern) {
			return true
		}
	}

	// General connection errors that might be protocol-related
	connectionErrors := []string{
		"connection refused",
		"connection reset",
		"connection timeout",
	}

	for _, pattern := range connectionErrors {
		if strings.Contains(errMsg, pattern) {
			return true
		}
	}

	return false
}

func (r *Runner) runTunnel(ctx context.Context, token string) {
	defer r.wg.Done()
	defer func() {
		if rec := recover(); rec != nil {
			logger.Sugar.Errorf("Recovered from panic in tunnel: %v", rec)
			r.mu.Lock()
			r.lastError = fmt.Errorf("tunnel panic: %v", rec)
			r.mu.Unlock()
		}

		// Clean up temporary config file
		r.cleanupConfigFile()

		r.mu.Lock()
		r.running = false
		r.mu.Unlock()

		if ctx.Err() == nil {
			logger.Sugar.Warn("Tunnel exited unexpectedly, checking auto-restart policy")
			r.checkAutoRestart()
		}
	}()

	cfg := r.cfgMgr.Get()

	app := &cli.App{
		Name:     "cloudflared-web",
		Commands: tunnel.Commands(),
		// Prevent cli from calling os.Exit on errors
		ExitErrHandler: func(c *cli.Context, err error) {
			if err != nil {
				logger.Sugar.Errorf("CLI error handler caught: %v", err)
			}
		},
	}

	// Disable default exit behavior
	cli.OsExiter = func(exitCode int) {
		// Don't actually exit, just log it
		logger.Sugar.Warnf("CLI attempted to exit with code %d (intercepted)", exitCode)
		if exitCode != 0 {
			panic(fmt.Sprintf("CLI exit with code %d", exitCode))
		}
	}

	// Build args with correct parameter order
	// --config must be between "tunnel" and "run" (it's a tunnel command option, not run option)
	args := []string{"cloudflared", "tunnel"}

	// Create temporary config file if CustomTag is set
	if cfg.CustomTag != "" {
		var err error
		r.mu.Lock()
		r.configFile, err = r.createTempConfig(cfg.CustomTag)
		r.mu.Unlock()

		if err != nil {
			logger.Sugar.Warnf("Failed to create config file for custom tag: %v", err)
		} else {
			args = append(args, "--config", r.configFile)
			logger.Sugar.Infof("Using custom identifier tag: %s", cfg.CustomTag)
		}
	}

	// Add "run" subcommand
	args = append(args, "run", "--token", token)

	// Select protocol based on config and failure history
	r.mu.Lock()
	selectedProtocol := r.selectProtocol(cfg.Protocol)
	r.mu.Unlock()

	// Always specify protocol explicitly when not using cloudflared's default
	if selectedProtocol != "" && selectedProtocol != "auto" {
		args = append(args, "--protocol", selectedProtocol)
		logger.Sugar.Infof("Using protocol: %s (config: %s)", selectedProtocol, cfg.Protocol)
	} else {
		logger.Sugar.Info("Using cloudflared default protocol (auto)")
	}

	if cfg.GracePeriod != "" && cfg.GracePeriod != "30s" {
		args = append(args, "--grace-period", cfg.GracePeriod)
	}

	if cfg.Region != "" {
		args = append(args, "--region", cfg.Region)
	}

	if cfg.Retries > 0 && cfg.Retries != 5 {
		args = append(args, "--retries", fmt.Sprintf("%d", cfg.Retries))
	}

	if cfg.MetricsEnable {
		args = append(args, "--metrics", fmt.Sprintf("localhost:%d", cfg.MetricsPort))
	}

	if cfg.LogLevel != "" && cfg.LogLevel != "info" {
		args = append(args, "--loglevel", cfg.LogLevel)
	}

	if cfg.LogFile != "" {
		args = append(args, "--logfile", cfg.LogFile)
	}

	if cfg.LogJSON {
		args = append(args, "--log-format", "json")
	}

	if cfg.EdgeIPVersion != "" && cfg.EdgeIPVersion != "auto" {
		args = append(args, "--edge-ip-version", cfg.EdgeIPVersion)
	}

	if cfg.EdgeBindAddress != "" {
		args = append(args, "--edge-bind-address", cfg.EdgeBindAddress)
	}

	if cfg.PostQuantum {
		args = append(args, "--post-quantum")
	}

	if cfg.NoTLSVerify {
		args = append(args, "--no-tls-verify")
	}

	// Parse and add extra arguments
	if cfg.ExtraArgs != "" {
		extraArgs := parseExtraArgs(cfg.ExtraArgs)
		args = append(args, extraArgs...)
	}

	logger.Sugar.Infof("Starting cloudflared tunnel with protocol=%s (selected), config_protocol=%s, region=%s, retries=%d",
		selectedProtocol, cfg.Protocol, cfg.Region, cfg.Retries)
	logger.Sugar.Debugf("Full tunnel arguments: %v", args)

	err := app.RunContext(ctx, args)

	// Check if context was cancelled (normal shutdown)
	if ctx.Err() != nil {
		logger.Sugar.Info("Tunnel stopped by user request")
		return
	}

	if err != nil {
		logger.Sugar.Errorf("Tunnel error: %v", err)
		r.mu.Lock()
		r.lastError = err
		r.mu.Unlock()

		// Record protocol failure for intelligent fallback
		r.recordProtocolFailure(err)

		// If error is not retryable, don't attempt auto-restart
		if !isRetryableError(err) {
			logger.Sugar.Warnf("Non-retryable error detected: %v", err)
			return
		}
	} else {
		// Successful exit - record protocol success
		r.recordProtocolSuccess()
		logger.Sugar.Info("Tunnel exited cleanly")
	}
}

// parseExtraArgs parses space-separated extra arguments
func parseExtraArgs(extraArgs string) []string {
	if extraArgs == "" {
		return nil
	}

	var results []string
	var current strings.Builder
	inQuote := false

	for i := 0; i < len(extraArgs); i++ {
		c := extraArgs[i]

		if c == '"' {
			inQuote = !inQuote
		} else if c == ' ' && !inQuote {
			if current.Len() > 0 {
				results = append(results, current.String())
				current.Reset()
			}
		} else {
			current.WriteByte(c)
		}
	}

	if current.Len() > 0 {
		results = append(results, current.String())
	}

	return results
}

// createTempConfig creates a temporary YAML config file with custom tags
func (r *Runner) createTempConfig(customTag string) (string, error) {
	// Create temp file
	tempFile, err := os.CreateTemp("", "cloudflared-*.yaml")
	if err != nil {
		return "", err
	}
	defer tempFile.Close()

	// Write YAML config with tag as array (cloudflared expects string slice)
	configContent := fmt.Sprintf("tag:\n  - version=%s\n", customTag)
	if _, err := tempFile.WriteString(configContent); err != nil {
		os.Remove(tempFile.Name())
		return "", err
	}

	return tempFile.Name(), nil
}

// cleanupConfigFile removes the temporary config file if it exists
func (r *Runner) cleanupConfigFile() {
	r.mu.Lock()
	configFile := r.configFile
	r.configFile = ""
	r.mu.Unlock()

	if configFile != "" {
		if err := os.Remove(configFile); err != nil && !os.IsNotExist(err) {
			logger.Sugar.Warnf("Failed to remove temporary config file %s: %v", configFile, err)
		} else {
			logger.Sugar.Debugf("Cleaned up temporary config file: %s", configFile)
		}
	}
}

func (r *Runner) checkAutoRestart() {
	cfg := r.cfgMgr.Get()
	if !cfg.AutoRestart {
		logger.Sugar.Info("Auto-restart is disabled, tunnel will not restart")
		return
	}

	r.mu.Lock()
	// Reset restart count if last restart was more than 5 minutes ago
	if time.Since(r.lastRestart) > 5*time.Minute {
		r.restartCount = 0
	}

	// Exponential backoff: 5s, 10s, 20s, 40s, max 60s
	delay := time.Duration(5*(1<<r.restartCount)) * time.Second
	if delay > 60*time.Second {
		delay = 60 * time.Second
	}

	// Limit maximum restart attempts
	if r.restartCount >= 10 {
		logger.Sugar.Warnf("Maximum restart attempts reached (%d), stopping auto-restart", r.restartCount)
		r.mu.Unlock()
		return
	}

	r.restartCount++
	r.lastRestart = time.Now()
	attemptNum := r.restartCount
	r.mu.Unlock()

	// Sleep without holding the lock to avoid blocking other operations
	logger.Sugar.Infof("Auto-restarting in %v (attempt %d)...", delay, attemptNum)
	time.Sleep(delay)

	if err := r.Start(); err != nil {
		logger.Sugar.Errorf("Failed to restart tunnel: %v", err)
	}
}

// isRetryableError determines if an error should trigger auto-restart
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	errMsg := err.Error()

	// Network errors - retryable
	retryablePatterns := []string{
		"connection refused",
		"connection reset",
		"timeout",
		"temporary failure",
		"network is unreachable",
		"no route to host",
		"broken pipe",
		"i/o timeout",
	}

	for _, pattern := range retryablePatterns {
		if strings.Contains(strings.ToLower(errMsg), pattern) {
			return true
		}
	}

	// Configuration/authentication errors - not retryable
	nonRetryablePatterns := []string{
		"invalid token",
		"authentication failed",
		"unauthorized",
		"forbidden",
		"bad request",
		"invalid configuration",
		"missing required",
	}

	for _, pattern := range nonRetryablePatterns {
		if strings.Contains(strings.ToLower(errMsg), pattern) {
			return false
		}
	}

	// Default: retry on unknown errors (conservative approach)
	return true
}

// Initialize checks if we should auto-start
func (r *Runner) Initialize() {
	cfg := r.cfgMgr.Get()
	if cfg.AutoStart && cfg.Token != "" {
		logger.Sugar.Info("Auto-starting tunnel...")
		if err := r.Start(); err != nil {
			logger.Sugar.Errorf("Failed to auto-start tunnel: %v", err)
		}
	}
}

// Shutdown performs graceful shutdown of the runner and cleans up resources
func (r *Runner) Shutdown() error {
	logger.Sugar.Info("Shutting down runner...")

	// Stop the tunnel if running
	if err := r.Stop(); err != nil {
		logger.Sugar.Warnf("Error stopping tunnel during shutdown: %v", err)
	}

	// Note: We don't close gracefulShutdownC here because:
	// 1. It's passed to cloudflared's tunnel.Init() and may be used internally
	// 2. Closing it could cause "send on closed channel" panics
	// 3. It will be garbage collected when the Runner is destroyed
	// The channel is created with NewRunner and should live for the entire app lifecycle

	logger.Sugar.Info("Runner shutdown complete")
	return nil
}
