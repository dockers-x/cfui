// Package service adapts the configuration store to the cloudflared runtime.
// Runner manages one cloudflared.Instance per tunnel profile so several
// tunnels can run side by side. The legacy single-tunnel methods
// (Start/Stop/Status) keep operating on the active profile for backward
// compatibility with existing API consumers.
package service

import (
	"fmt"
	"sync"

	"cfui/internal/cloudflared"
	"cfui/internal/config"
	"cfui/internal/logger"

	"github.com/prometheus/client_golang/prometheus"
)

// Runner manages cloudflared tunnel instances, one per tunnel profile.
type Runner struct {
	cfgMgr *config.Manager

	mu    sync.Mutex
	insts map[string]*cloudflared.Instance // keyed by canonical profile key
}

func NewRunner(cfgMgr *config.Manager) *Runner {
	return &Runner{
		cfgMgr: cfgMgr,
		insts:  make(map[string]*cloudflared.Instance),
	}
}

// optionsFor derives launch options for one profile. It is re-evaluated on
// every start and auto-restart so configuration changes apply immediately and
// deleted profiles stop restarting.
func (r *Runner) optionsFor(key string) (cloudflared.Options, error) {
	cfg := r.cfgMgr.Get()
	profile, ok := cfg.TunnelProfile(key)
	if !ok {
		return cloudflared.Options{}, fmt.Errorf("tunnel profile %q not found", key)
	}
	if !profile.LocalEnabled {
		return cloudflared.Options{}, fmt.Errorf("tunnel profile %q is not enabled for local running", profile.Key)
	}
	if profile.Token == "" {
		return cloudflared.Options{}, fmt.Errorf("token is required")
	}
	return OptionsFromProfile(profile), nil
}

// OptionsFromProfile maps a tunnel profile onto cloudflared launch options.
func OptionsFromProfile(p config.TunnelProfileConfig) cloudflared.Options {
	return cloudflared.Options{
		Token:           p.Token,
		CustomTag:       p.CustomTag,
		SoftwareName:    p.SoftwareName,
		Protocol:        p.Protocol,
		GracePeriod:     p.GracePeriod,
		Region:          p.Region,
		Retries:         p.Retries,
		MetricsEnable:   p.MetricsEnable,
		MetricsPort:     p.MetricsPort,
		LogLevel:        p.LogLevel,
		LogFile:         p.LogFile,
		LogJSON:         p.LogJSON,
		EdgeIPVersion:   p.EdgeIPVersion,
		EdgeBindAddress: p.EdgeBindAddress,
		Edge:            p.Edge,
		PostQuantum:     p.PostQuantum,
		NoTLSVerify:     p.NoTLSVerify,
		ExtraArgs:       p.ExtraArgs,
		AutoRestart:     p.AutoRestart,
	}
}

// resolveKey maps a request key ("" means active) onto the canonical profile
// key. Unknown keys are returned as-is so instances of just-deleted profiles
// can still be stopped.
func (r *Runner) resolveKey(key string) string {
	if profile, ok := r.cfgMgr.Get().TunnelProfile(key); ok {
		return profile.Key
	}
	return key
}

// instanceFor returns (creating if needed) the instance for a profile.
func (r *Runner) instanceFor(key string) (*cloudflared.Instance, error) {
	profile, ok := r.cfgMgr.Get().TunnelProfile(key)
	if !ok {
		return nil, fmt.Errorf("tunnel profile %q not found", key)
	}
	canonical := profile.Key

	r.mu.Lock()
	defer r.mu.Unlock()
	inst := r.insts[canonical]
	if inst == nil {
		boundKey := canonical
		inst = cloudflared.NewInstance(boundKey, func() (cloudflared.Options, error) {
			return r.optionsFor(boundKey)
		})
		r.insts[canonical] = inst
	}
	return inst, nil
}

// StartProfile launches the tunnel for the given profile key ("" = active).
func (r *Runner) StartProfile(key string) error {
	inst, err := r.instanceFor(key)
	if err != nil {
		return err
	}
	if err := r.checkMetricsPortConflict(inst.Name()); err != nil {
		return err
	}
	return inst.Start()
}

// checkMetricsPortConflict refuses to start a profile whose metrics listener
// would collide with an already running instance; otherwise the new tunnel
// would crash-loop on the occupied port.
func (r *Runner) checkMetricsPortConflict(key string) error {
	cfg := r.cfgMgr.Get()
	target, ok := cfg.TunnelProfile(key)
	if !ok || !target.MetricsEnable {
		return nil
	}
	for _, p := range cfg.Tunnels {
		if p.Key == target.Key || !p.MetricsEnable || p.MetricsPort != target.MetricsPort {
			continue
		}
		if st, exists := r.ProfileStatus(p.Key); exists && st.Running {
			return fmt.Errorf("metrics port %d is already used by running tunnel %q; choose a different metrics port", target.MetricsPort, p.Key)
		}
	}
	return nil
}

// StopProfile stops the tunnel for the given profile key ("" = active).
// Stopping a profile that never started is a no-op.
func (r *Runner) StopProfile(key string) error {
	canonical := r.resolveKey(key)
	r.mu.Lock()
	inst := r.insts[canonical]
	r.mu.Unlock()
	if inst == nil {
		return nil
	}
	return inst.Stop()
}

// RemoveProfile stops and forgets the instance of a (typically just deleted)
// profile.
func (r *Runner) RemoveProfile(key string) error {
	canonical := r.resolveKey(key)
	r.mu.Lock()
	inst := r.insts[canonical]
	delete(r.insts, canonical)
	r.mu.Unlock()
	if inst == nil {
		return nil
	}
	return inst.Stop()
}

// ProfileStatus reports the status of one profile's instance. exists is false
// when the profile has never been started in this process.
func (r *Runner) ProfileStatus(key string) (cloudflared.Status, bool) {
	canonical := r.resolveKey(key)
	r.mu.Lock()
	inst := r.insts[canonical]
	r.mu.Unlock()
	if inst == nil {
		return cloudflared.Status{}, false
	}
	return inst.Status(), true
}

// RunningCount returns how many tunnel instances are currently running.
func (r *Runner) RunningCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, inst := range r.insts {
		if inst.Status().Running {
			count++
		}
	}
	return count
}

// Legacy single-tunnel API operating on the active profile.

// Start launches the cloudflared tunnel for the active profile.
func (r *Runner) Start() error {
	return r.StartProfile("")
}

// Stop terminates the active profile's tunnel gracefully with a timeout.
func (r *Runner) Stop() error {
	return r.StopProfile("")
}

// Status reports whether the active profile's tunnel is running, its last
// error, and the currently selected protocol.
func (r *Runner) Status() (bool, error, string) {
	st, _ := r.ProfileStatus("")
	return st.Running, st.LastError, st.Protocol
}

// GetMetricsRegistry returns the shared Prometheus registry used by all
// tunnel instances.
func (r *Runner) GetMetricsRegistry() *prometheus.Registry {
	return cloudflared.MetricsRegistry()
}

// Initialize auto-starts every local-enabled profile that requests it.
func (r *Runner) Initialize() {
	cfg := r.cfgMgr.Get()
	for _, profile := range cfg.Tunnels {
		if !profile.LocalEnabled || !profile.AutoStart || profile.Token == "" {
			continue
		}
		logger.Sugar.Infof("Auto-starting tunnel %q...", profile.Key)
		if err := r.StartProfile(profile.Key); err != nil {
			logger.Sugar.Errorf("Failed to auto-start tunnel %q: %v", profile.Key, err)
		}
	}
}

// Shutdown stops all tunnels concurrently and broadcasts a process-wide
// graceful shutdown to the embedded cloudflared runtime. Call only on
// application exit.
func (r *Runner) Shutdown() error {
	logger.Sugar.Info("Shutting down runner...")

	r.mu.Lock()
	insts := make([]*cloudflared.Instance, 0, len(r.insts))
	for _, inst := range r.insts {
		insts = append(insts, inst)
	}
	r.mu.Unlock()

	var wg sync.WaitGroup
	for _, inst := range insts {
		wg.Add(1)
		go func(in *cloudflared.Instance) {
			defer wg.Done()
			if err := in.Stop(); err != nil {
				logger.Sugar.Warnf("Error stopping tunnel %q during shutdown: %v", in.Name(), err)
			}
		}(inst)
	}
	wg.Wait()
	cloudflared.ShutdownProcess()

	logger.Sugar.Info("Runner shutdown complete")
	return nil
}
