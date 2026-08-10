package cloudflared

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/cloudflare/backoff"
	"github.com/cloudflare/cloudflared/cmd/cloudflared/tunnel"
	"github.com/urfave/cli/v2"
)

const (
	defaultStopTimeout = 30 * time.Second

	maxProtocolFailuresBeforeSwitch = 3
)

// ErrAlreadyRunning is returned by Start when the instance is running.
var ErrAlreadyRunning = errors.New("already running")

// OptionsProvider returns fresh launch options. It is called on every start
// and auto-restart so configuration changes apply without recreating the
// instance. Returning an error blocks the (re)start.
type OptionsProvider func() (Options, error)

// Status is a point-in-time snapshot of an instance.
type Status struct {
	Running bool
	// Phase is one of stopped, starting, running, stopping, reconnecting, or
	// error. It lets API clients distinguish transitional states from a stable
	// stopped tunnel.
	Phase       string
	LastError   error
	RetryCount  int
	NextRetryAt *time.Time
	// Protocol is the transport currently selected by the fallback logic
	// (quic, http2, or auto before the first start).
	Protocol string
}

// Instance manages the lifecycle of one cloudflared tunnel: start, stop,
// protocol fallback, and auto-restart with exponential backoff. Each tunnel
// profile gets its own Instance; all instances share the process-wide
// cloudflared runtime set up by EnsureInit.
type Instance struct {
	name   string
	optsFn OptionsProvider

	mu          sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{} // closed when the current run's goroutine exits
	running     bool
	phase       string
	lastError   error
	configFile  string
	stopTimeout time.Duration
	generation  uint64
	// restartRequested is set when cfui cancels a run to recover a tunnel that
	// is still running locally but no longer has active edge connections.
	restartRequested bool

	restartCount   int
	lastRestart    time.Time
	nextRetryAt    time.Time
	restartBackoff *backoff.Backoff

	// Protocol fallback management (for auto mode).
	currentProtocol     string
	protocolFailures    map[string]int
	lastProtocolSwitch  time.Time
	protocolSwitchCount int
}

// NewInstance creates an instance named after its tunnel profile. The name
// only appears in logs and error messages.
func NewInstance(name string, optsFn OptionsProvider) *Instance {
	return &Instance{
		name:             name,
		optsFn:           optsFn,
		stopTimeout:      defaultStopTimeout,
		protocolFailures: make(map[string]int),
		restartBackoff:   NewRestartBackoff(),
		currentProtocol:  "auto",
		phase:            "stopped",
	}
}

// Name returns the instance name.
func (i *Instance) Name() string {
	return i.name
}

// Start launches the tunnel. It returns ErrAlreadyRunning when called twice
// without an intervening stop or exit.
func (i *Instance) Start() (err error) {
	return i.start(nil, nil)
}

// start reserves and launches a lifecycle. Auto-restart callers pass the
// generation and context of the run they are replacing so a concurrent Stop
// can invalidate the restart before it reserves a new lifecycle.
func (i *Instance) start(expectedGeneration *uint64, expectedCtx context.Context) (err error) {
	var generation uint64
	// Outermost panic guard: a failure inside the embedded library during
	// launch must not take down the whole control panel.
	defer func() {
		if rec := recover(); rec != nil {
			logErrorf("Panic during tunnel %q start (recovered): %v", i.name, rec)
			err = fmt.Errorf("start panic: %v", rec)
			i.setTerminalErrorFor(generation, err)
		}
	}()

	// Reserve this lifecycle before reading configuration. Otherwise an
	// invalid duplicate start could overwrite the state owned by a live run.
	i.mu.Lock()
	if expectedGeneration != nil &&
		(i.generation != *expectedGeneration || i.phase != "reconnecting" || expectedCtx == nil || expectedCtx.Err() != nil) {
		i.mu.Unlock()
		return context.Canceled
	}
	if i.running || i.phase == "starting" {
		i.mu.Unlock()
		logWarnf("Attempted to start tunnel %q that is already running", i.name)
		return ErrAlreadyRunning
	}
	oldCancel := i.cancel
	i.generation++
	generation = i.generation
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	i.ctx, i.cancel, i.done = ctx, cancel, done
	i.phase = "starting"
	i.lastError = nil
	i.nextRetryAt = time.Time{}
	i.restartRequested = false
	i.mu.Unlock()
	if oldCancel != nil {
		oldCancel()
	}

	opts, err := i.optsFn()
	if err != nil {
		logErrorf("Cannot start tunnel %q: %v", i.name, err)
		i.setTerminalErrorFor(generation, err)
		return err
	}
	if err := opts.Validate(); err != nil {
		logErrorf("Cannot start tunnel %q: %v", i.name, err)
		i.setTerminalErrorFor(generation, err)
		return err
	}
	if err := EnsureInit(opts.SoftwareName); err != nil {
		i.setTerminalErrorFor(generation, err)
		return err
	}

	i.mu.Lock()
	if i.generation != generation || i.phase != "starting" || ctx.Err() != nil {
		i.mu.Unlock()
		cancel()
		return context.Canceled
	}
	i.running = true
	i.mu.Unlock()

	logInfof("Starting cloudflared tunnel %q", i.name)
	go i.runTunnel(ctx, opts, done, generation)

	return nil
}

// Stop terminates the tunnel via context cancellation and waits for the run
// goroutine to exit. Individual instances must not touch the shared graceful
// shutdown channel: cloudflared closes it on SIGTERM (so sending could panic)
// and a stray token could stop an unrelated instance.
func (i *Instance) Stop() error {
	i.mu.Lock()
	if !i.running {
		cancel := i.cancel
		if cancel != nil {
			i.generation++
		}
		i.ctx = nil
		i.cancel = nil
		i.done = nil
		i.restartRequested = false
		i.phase = "stopped"
		i.nextRetryAt = time.Time{}
		i.mu.Unlock()
		if cancel != nil {
			cancel()
			logDebugf("Canceled pending restart of tunnel %q", i.name)
			return nil
		}
		logDebugf("Stop called but tunnel %q is not running", i.name)
		return nil
	}

	logInfof("Initiating shutdown of tunnel %q", i.name)
	i.phase = "stopping"
	cancel := i.cancel
	i.cancel = nil
	i.restartRequested = false
	done := i.done
	timeout := i.stopTimeout
	i.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		logInfof("Tunnel %q stopped gracefully", i.name)
		return nil
	case <-timer.C:
		logWarnf("Tunnel %q stop timeout exceeded (%v)", i.name, timeout)
		// The embedded run may still own connections and its temporary config.
		// Keep it non-startable until the goroutine actually exits; reporting a
		// stopped instance here could launch a second run for the same profile.
		timeoutErr := fmt.Errorf("timeout waiting for tunnel %q to stop", i.name)
		i.mu.Lock()
		if i.done == done && i.running {
			i.phase = "stopping"
			i.lastError = timeoutErr
		}
		i.mu.Unlock()
		return timeoutErr
	}
}

// Status returns a snapshot of the instance state.
func (i *Instance) Status() Status {
	i.mu.Lock()
	defer i.mu.Unlock()
	status := Status{
		Running:    i.running,
		Phase:      i.phase,
		LastError:  i.lastError,
		RetryCount: i.restartCount,
		Protocol:   i.currentProtocol,
	}
	if status.Phase == "" {
		if status.Running {
			status.Phase = "running"
		} else {
			status.Phase = "stopped"
		}
	}
	if !i.nextRetryAt.IsZero() {
		next := i.nextRetryAt
		status.NextRetryAt = &next
	}
	return status
}

func (i *Instance) setTerminalErrorFor(generation uint64, err error) {
	if err == nil {
		return
	}
	i.mu.Lock()
	if generation == 0 || generation != i.generation {
		i.mu.Unlock()
		return
	}
	cancel := i.cancel
	i.ctx = nil
	i.cancel = nil
	i.done = nil
	i.running = false
	i.phase = "error"
	i.lastError = err
	i.nextRetryAt = time.Time{}
	i.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (i *Instance) markRunning(generation uint64) {
	i.mu.Lock()
	if i.generation == generation && i.running && (i.phase == "starting" || i.phase == "running") {
		i.phase = "running"
		i.lastError = nil
		i.nextRetryAt = time.Time{}
	}
	i.mu.Unlock()
}

// selectProtocol determines which protocol to use based on configuration and
// failure history. Callers must hold i.mu.
func (i *Instance) selectProtocol(configProtocol string) string {
	// If the user explicitly chose a protocol, always use it.
	if configProtocol != "" && configProtocol != "auto" {
		i.currentProtocol = configProtocol
		return configProtocol
	}

	// Auto mode: cycle quic -> http2 -> quic after repeated failures.
	if i.protocolFailures[i.currentProtocol] >= maxProtocolFailuresBeforeSwitch {
		var nextProtocol string
		if i.currentProtocol == "quic" || i.currentProtocol == "auto" {
			nextProtocol = "http2"
		} else {
			nextProtocol = "quic"
		}

		logWarnf("Tunnel %q: protocol %s has failed %d times, switching to %s",
			i.name, i.currentProtocol, i.protocolFailures[i.currentProtocol], nextProtocol)

		// Reset the failing protocol's count so it gets a fresh start if we
		// ever switch back.
		i.protocolFailures[i.currentProtocol] = 0

		i.currentProtocol = nextProtocol
		i.lastProtocolSwitch = time.Now()
		i.protocolSwitchCount++

		return nextProtocol
	}

	if i.currentProtocol == "" || i.currentProtocol == "auto" {
		i.currentProtocol = "quic"
	}
	return i.currentProtocol
}

// recordProtocolSuccess clears failure history after a clean exit so no
// protocol stays blacklisted forever.
func (i *Instance) recordProtocolSuccess() {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.currentProtocol != "" && i.currentProtocol != "auto" {
		logInfof("Tunnel %q: protocol %s connected successfully, resetting failure counts", i.name, i.currentProtocol)

		i.restartCount = 0
		if i.restartBackoff != nil {
			i.restartBackoff.Reset()
		}
		for proto := range i.protocolFailures {
			i.protocolFailures[proto] = 0
		}
	}
}

// recordProtocolFailure increments the failure count for the current protocol
// when the error looks transport-related.
func (i *Instance) recordProtocolFailure(err error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.currentProtocol == "" || i.currentProtocol == "auto" {
		i.currentProtocol = "quic"
	}

	if IsProtocolRelatedError(err) {
		i.protocolFailures[i.currentProtocol]++
		logWarnf("Tunnel %q: protocol %s failure count: %d (error: %v)",
			i.name, i.currentProtocol, i.protocolFailures[i.currentProtocol], err)
	}
}

func (i *Instance) runTunnel(ctx context.Context, opts Options, done chan struct{}, generation uint64) {
	restartAllowed := true
	var configFile string
	defer close(done)
	defer func() {
		if rec := recover(); rec != nil {
			logErrorf("Recovered from panic in tunnel %q: %v", i.name, rec)
			i.mu.Lock()
			if i.generation == generation {
				i.lastError = fmt.Errorf("tunnel panic: %v", rec)
			}
			i.mu.Unlock()
		}

		i.cleanupConfigFile(generation, configFile)

		i.mu.Lock()
		if i.generation != generation {
			i.mu.Unlock()
			return
		}
		restartRequested := i.restartRequested
		i.restartRequested = false
		i.running = false
		if !shouldRestartAfterExit(ctx, restartAllowed, restartRequested) {
			if ctx.Err() != nil {
				i.phase = "stopped"
				i.lastError = nil
			} else if i.lastError == nil {
				i.phase = "stopped"
			} else {
				i.phase = "error"
			}
			i.nextRetryAt = time.Time{}
		}
		restartCtx := ctx
		if restartRequested {
			var restartCancel context.CancelFunc
			restartCtx, restartCancel = context.WithCancel(context.Background())
			i.ctx = restartCtx
			i.cancel = restartCancel
		}
		i.mu.Unlock()

		if shouldRestartAfterExit(ctx, restartAllowed, restartRequested) {
			logWarnf("Tunnel %q exited unexpectedly, checking auto-restart policy", i.name)
			i.maybeAutoRestartForRun(restartCtx, generation)
		}
	}()

	app := &cli.App{
		Name:     "cloudflared-web",
		Commands: tunnel.Commands(),
		// Prevent cli from calling os.Exit on errors.
		ExitErrHandler: func(c *cli.Context, err error) {
			if err != nil {
				logErrorf("Tunnel %q CLI error handler caught: %v", i.name, err)
			}
		},
	}

	if opts.CustomTag != "" {
		file, err := createTempConfig(opts.CustomTag)
		if err != nil {
			logWarnf("Tunnel %q: failed to create config file for custom tag: %v", i.name, err)
		} else {
			configFile = file
			i.mu.Lock()
			if i.generation == generation {
				i.configFile = file
			}
			i.mu.Unlock()
			logInfof("Tunnel %q using custom identifier tag: %s", i.name, opts.CustomTag)
		}
	}

	i.mu.Lock()
	selectedProtocol := i.selectProtocol(opts.Protocol)
	if opts.Protocol == "auto" {
		logDebugf("Tunnel %q protocol failure counts: quic=%d, http2=%d",
			i.name, i.protocolFailures["quic"], i.protocolFailures["http2"])
	}
	i.mu.Unlock()

	readinessURL := i.configureReadinessProbe(&opts)
	args := BuildArgs(opts, selectedProtocol, configFile)

	logInfof("Starting cloudflared tunnel %q with protocol=%s (selected), config_protocol=%s, region=%s, retries=%d",
		i.name, selectedProtocol, opts.Protocol, opts.Region, opts.Retries)

	// The run we are about to launch registers an upstream signal watcher;
	// schedule pulses that strip it (and any stale ones) again.
	scheduleSignalReclaim()
	if readinessURL != "" {
		go i.monitorReadinessForRun(ctx, readinessURL, generation)
	} else {
		i.markRunning(generation)
	}

	err := app.RunContext(ctx, args)
	restartAllowed = shouldAutoRestartAfterRun(ctx, err)

	// Context cancellation means a user-requested stop.
	if ctx.Err() != nil {
		if i.hasRestartRequest() {
			logWarnf("Tunnel %q stopped by readiness watchdog for restart", i.name)
		} else {
			logInfof("Tunnel %q stopped by user request", i.name)
		}
		return
	}

	if err != nil {
		logErrorf("Tunnel %q error: %v", i.name, err)
		i.mu.Lock()
		i.lastError = err
		i.mu.Unlock()

		i.recordProtocolFailure(err)

		if !restartAllowed {
			logWarnf("Tunnel %q: non-retryable error detected: %v", i.name, err)
			return
		}
	} else {
		i.recordProtocolSuccess()
		logInfof("Tunnel %q exited cleanly", i.name)
	}
}

// createTempConfig writes a temporary YAML config carrying the custom tag
// (cloudflared expects tags as a string slice).
func createTempConfig(customTag string) (string, error) {
	tempFile, err := os.CreateTemp("", "cloudflared-*.yaml")
	if err != nil {
		return "", err
	}
	defer tempFile.Close()

	configContent := fmt.Sprintf("tag:\n  - version=%s\n", customTag)
	if _, err := tempFile.WriteString(configContent); err != nil {
		os.Remove(tempFile.Name())
		return "", err
	}

	return tempFile.Name(), nil
}

// cleanupConfigFile removes the temporary config file if one exists.
func (i *Instance) cleanupConfigFile(generation uint64, configFile string) {
	i.mu.Lock()
	if i.generation == generation && i.configFile == configFile {
		i.configFile = ""
	}
	i.mu.Unlock()
	removeConfigFile(configFile)
}

func removeConfigFile(configFile string) {
	if configFile == "" {
		return
	}
	if err := os.Remove(configFile); err != nil && !os.IsNotExist(err) {
		logWarnf("Failed to remove temporary config file %s: %v", configFile, err)
	} else {
		logDebugf("Cleaned up temporary config file: %s", configFile)
	}
}
