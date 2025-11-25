package service

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"cfui/config"

	"github.com/cloudflare/cloudflared/cmd/cloudflared/cliutil"
	"github.com/cloudflare/cloudflared/cmd/cloudflared/tunnel"
	"github.com/urfave/cli/v2"
)

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
	initOnce          sync.Once
}

func NewRunner(cfgMgr *config.Manager) *Runner {
	r := &Runner{
		cfgMgr:            cfgMgr,
		gracefulShutdownC: make(chan struct{}),
	}
	// Initialize cloudflared tunnel package
	r.initTunnel()
	return r
}

// initTunnel initializes the cloudflared tunnel package with required build info
func (r *Runner) initTunnel() {
	r.initOnce.Do(func() {
		buildInfo := cliutil.GetBuildInfo("", "dev")
		tunnel.Init(buildInfo, r.gracefulShutdownC)
		log.Println("Cloudflared tunnel initialized successfully")
	})
}

// Start launches the cloudflared tunnel
func (r *Runner) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		return fmt.Errorf("already running")
	}

	cfg := r.cfgMgr.Get()
	if cfg.Token == "" {
		return fmt.Errorf("token is required")
	}

	r.ctx, r.cancel = context.WithCancel(context.Background())
	r.running = true
	r.lastError = nil

	r.wg.Add(1)
	go r.runTunnel(r.ctx, cfg.Token)

	return nil
}

// Stop terminates the tunnel gracefully with timeout
func (r *Runner) Stop() error {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return nil
	}

	// Cancel the context to signal shutdown
	if r.cancel != nil {
		r.cancel()
	}

	// Signal graceful shutdown to cloudflared
	select {
	case r.gracefulShutdownC <- struct{}{}:
	default:
		// Channel might be full or not being read, continue anyway
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
		log.Println("Tunnel stopped gracefully")
		// Ensure running state is cleared and cleanup resources
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
		// Config file is already cleaned up in runTunnel's defer
		return nil
	case <-time.After(30 * time.Second):
		log.Println("Warning: Tunnel stop timeout exceeded")
		// Force set running to false even on timeout
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
		// Try to cleanup config file even on timeout
		r.cleanupConfigFile()
		return fmt.Errorf("timeout waiting for tunnel to stop")
	}
}

func (r *Runner) Status() (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running, r.lastError
}

func (r *Runner) runTunnel(ctx context.Context, token string) {
	defer r.wg.Done()
	defer func() {
		shouldAutoRestart := true

		if rec := recover(); rec != nil {
			log.Printf("Recovered from panic in tunnel: %v", rec)
			r.mu.Lock()
			r.lastError = fmt.Errorf("tunnel panic: %v", rec)
			r.mu.Unlock()

			// Don't auto-restart on metrics registration errors - they won't resolve with restart
			if strings.Contains(fmt.Sprintf("%v", rec), "duplicate metrics") {
				log.Printf("Metrics registration error detected - this requires process restart, not tunnel restart")
				shouldAutoRestart = false
			}
		}

		// Clean up temporary config file
		r.cleanupConfigFile()

		r.mu.Lock()
		r.running = false
		r.mu.Unlock()

		if ctx.Err() == nil && shouldAutoRestart {
			log.Println("Tunnel exited unexpectedly")
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
				log.Printf("CLI error handler caught: %v", err)
			}
		},
	}

	// Disable default exit behavior
	cli.OsExiter = func(exitCode int) {
		// Don't actually exit, just log it
		log.Printf("CLI attempted to exit with code %d (intercepted)", exitCode)
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
			log.Printf("Warning: Failed to create config file for custom tag: %v", err)
		} else {
			args = append(args, "--config", r.configFile)
			log.Printf("Using custom identifier tag: %s", cfg.CustomTag)
		}
	}

	// Add "run" subcommand
	args = append(args, "run", "--token", token)
	if cfg.Protocol != "" && cfg.Protocol != "auto" {
		args = append(args, "--protocol", cfg.Protocol)
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

	log.Printf("Starting cloudflared tunnel with args: %v", args)

	err := app.RunContext(ctx, args)

	// Check if context was cancelled (normal shutdown)
	if ctx.Err() != nil {
		log.Println("Tunnel stopped by user request")
		return
	}

	if err != nil {
		log.Printf("Tunnel error: %v", err)
		r.mu.Lock()
		r.lastError = err
		r.mu.Unlock()

		// If error is not retryable, don't attempt auto-restart
		if !isRetryableError(err) {
			log.Printf("Non-retryable error detected, skipping auto-restart")
			return
		}
	} else {
		// Reset restart count on successful exit
		r.mu.Lock()
		r.restartCount = 0
		r.mu.Unlock()
		log.Println("Tunnel exited cleanly")
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
			log.Printf("Warning: Failed to remove temporary config file %s: %v", configFile, err)
		} else {
			log.Printf("Cleaned up temporary config file: %s", configFile)
		}
	}
}

func (r *Runner) checkAutoRestart() {
	cfg := r.cfgMgr.Get()
	if !cfg.AutoRestart {
		log.Println("Auto-restart is disabled, tunnel will not restart")
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
		log.Printf("Maximum restart attempts reached (%d), stopping auto-restart", r.restartCount)
		r.mu.Unlock()
		return
	}

	r.restartCount++
	r.lastRestart = time.Now()
	attemptNum := r.restartCount
	r.mu.Unlock()

	// Sleep without holding the lock to avoid blocking other operations
	log.Printf("Auto-restarting in %v (attempt %d)...", delay, attemptNum)
	time.Sleep(delay)

	if err := r.Start(); err != nil {
		log.Printf("Failed to restart tunnel: %v", err)
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
		log.Println("Auto-starting tunnel...")
		if err := r.Start(); err != nil {
			log.Printf("Failed to auto-start tunnel: %v", err)
		}
	}
}
