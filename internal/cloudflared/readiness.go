package cloudflared

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

var (
	readinessProbeInterval    = 15 * time.Second
	readinessProbeTimeout     = 3 * time.Second
	readinessStartupGrace     = 2 * time.Minute
	readinessFailureThreshold = 4
)

// configureReadinessProbe enables a local cloudflared /ready probe when
// auto-restart is on. The probe follows cloudflared's own readiness semantics:
// HTTP 200 means at least one active edge connection; HTTP 503 means no active
// edge connections.
func (i *Instance) configureReadinessProbe(opts *Options) string {
	if opts == nil || !opts.AutoRestart {
		return ""
	}

	metricsAddress := effectiveMetricsAddress(*opts)
	if metricsAddress == "" {
		var err error
		metricsAddress, err = allocateLoopbackAddress()
		if err != nil {
			logWarnf("Tunnel %q readiness watchdog disabled: failed to allocate metrics address: %v", i.name, err)
			return ""
		}
		opts.MetricsAddress = metricsAddress
	}

	readyURL := readinessURL(metricsAddress)
	if readyURL == "" {
		logWarnf("Tunnel %q readiness watchdog disabled: unsupported metrics address %q", i.name, metricsAddress)
		return ""
	}
	logDebugf("Tunnel %q readiness watchdog using %s", i.name, readyURL)
	return readyURL
}

func effectiveMetricsAddress(opts Options) string {
	if addr := metricsAddressFromExtraArgs(opts.ExtraArgs); addr != "" {
		return addr
	}
	if strings.TrimSpace(opts.MetricsAddress) != "" {
		return strings.TrimSpace(opts.MetricsAddress)
	}
	if opts.MetricsEnable && opts.MetricsPort > 0 {
		return fmt.Sprintf("localhost:%d", opts.MetricsPort)
	}
	return ""
}

func metricsAddressFromExtraArgs(extraArgs string) string {
	args := ParseExtraArgs(extraArgs)
	for idx, arg := range args {
		if arg == "--metrics" {
			if idx+1 < len(args) {
				return strings.TrimSpace(args[idx+1])
			}
			return ""
		}
		if strings.HasPrefix(arg, "--metrics=") {
			return strings.TrimSpace(strings.TrimPrefix(arg, "--metrics="))
		}
	}
	return ""
}

func allocateLoopbackAddress() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer listener.Close()
	return listener.Addr().String(), nil
}

func readinessURL(metricsAddress string) string {
	metricsAddress = strings.TrimSpace(metricsAddress)
	if metricsAddress == "" || strings.HasSuffix(metricsAddress, ":0") {
		return ""
	}
	if strings.HasPrefix(metricsAddress, "http://") || strings.HasPrefix(metricsAddress, "https://") {
		return strings.TrimRight(metricsAddress, "/") + "/ready"
	}
	return "http://" + metricsAddress + "/ready"
}

type readinessResponse struct {
	Status           int  `json:"status"`
	ReadyConnections uint `json:"readyConnections"`
}

type readinessResult struct {
	ready            bool
	statusCode       int
	readyConnections uint
	err              error
}

func checkReadiness(ctx context.Context, client *http.Client, readyURL string) readinessResult {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, readyURL, nil)
	if err != nil {
		return readinessResult{err: err}
	}
	resp, err := client.Do(req)
	if err != nil {
		return readinessResult{err: err}
	}
	defer resp.Body.Close()

	result := readinessResult{
		ready:      resp.StatusCode == http.StatusOK,
		statusCode: resp.StatusCode,
	}
	var body readinessResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err == nil {
		result.readyConnections = body.ReadyConnections
	}
	return result
}

func (r readinessResult) failureSummary() string {
	if r.err != nil {
		return r.err.Error()
	}
	return fmt.Sprintf("cloudflared /ready returned HTTP %d with %d ready connections", r.statusCode, r.readyConnections)
}

func (i *Instance) monitorReadiness(ctx context.Context, readyURL string) {
	i.mu.Lock()
	generation := i.generation
	i.mu.Unlock()
	i.monitorReadinessForRun(ctx, readyURL, generation)
}

func (i *Instance) monitorReadinessForRun(ctx context.Context, readyURL string, generation uint64) {
	client := &http.Client{Timeout: readinessProbeTimeout}
	ticker := time.NewTicker(readinessProbeInterval)
	defer ticker.Stop()

	startedAt := time.Now()
	readySeen := false
	failures := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		probeCtx, cancel := context.WithTimeout(ctx, readinessProbeTimeout)
		result := checkReadiness(probeCtx, client, readyURL)
		cancel()

		if result.ready {
			if !readySeen {
				logInfof("Tunnel %q readiness confirmed by cloudflared /ready (%d active edge connection(s))", i.name, result.readyConnections)
			}
			readySeen = true
			failures = 0
			i.markRunning(generation)
			continue
		}

		if !readySeen && time.Since(startedAt) < readinessStartupGrace {
			logDebugf("Tunnel %q readiness probe not ready during startup grace: %s", i.name, result.failureSummary())
			continue
		}

		failures++
		logWarnf("Tunnel %q readiness probe failed (%d/%d): %s", i.name, failures, readinessFailureThreshold, result.failureSummary())
		if failures < readinessFailureThreshold {
			continue
		}

		err := fmt.Errorf("cloudflared readiness failed after %d consecutive checks: %s", failures, result.failureSummary())
		if i.requestReadinessRestartForRun(err, generation) {
			logWarnf("Tunnel %q readiness watchdog requested restart", i.name)
		}
		return
	}
}

func (i *Instance) hasRestartRequest() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.restartRequested
}

func (i *Instance) requestReadinessRestart(err error) bool {
	i.mu.Lock()
	generation := i.generation
	i.mu.Unlock()
	return i.requestReadinessRestartForRun(err, generation)
}

func (i *Instance) requestReadinessRestartForRun(err error, generation uint64) bool {
	i.mu.Lock()
	if i.generation != generation || !i.running || i.cancel == nil {
		i.mu.Unlock()
		return false
	}
	if err != nil {
		i.lastError = err
	}
	i.restartRequested = true
	i.phase = "reconnecting"
	cancel := i.cancel
	i.mu.Unlock()

	cancel()
	return true
}
