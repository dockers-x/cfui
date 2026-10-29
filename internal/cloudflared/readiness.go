package cloudflared

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"uuid"
)

var (
	readinessProbeInterval    = 15 * time.Second
	readinessProbeTimeout     = 3 * time.Second
	readinessStartupGrace     = 2 * time.Minute
	readinessFailureThreshold = 4
	readinessUnknownThreshold = 12
)

// configureReadinessProbe enables local /ready monitoring independently of
// auto-restart. The probe follows cloudflared's own readiness semantics:
// HTTP 200 means at least one active edge connection; HTTP 503 means no active
// edge connections.
func (i *Instance) configureReadinessProbe(opts *Options) string {
	if opts == nil {
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

	// An occupied metrics port could belong to another connector. Upstream may
	// choose a different listener after a bind failure, so never probe that port.
	listener, err := net.Listen("tcp", metricsAddress)
	if err != nil {
		logWarnf("Tunnel %q readiness probe unavailable: metrics address %q is occupied or invalid: %v", i.name, metricsAddress, err)
		return ""
	}
	listener.Close()

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
	host, port, err := net.SplitHostPort(strings.TrimSpace(metricsAddress))
	if err != nil || port == "" || port == "0" {
		return ""
	}
	// Probe the local listener directly, even if cloudflared listens on all
	// interfaces. Remote endpoints cannot prove this embedded run is healthy.
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	case "localhost":
	default:
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return ""
		}
	}
	return "http://" + net.JoinHostPort(host, port) + "/ready"
}

// expectedHAConnections mirrors the upstream CLI default, with its extra flag override.
func expectedHAConnections(opts Options) uint {
	expected := uint(4)
	args := ParseExtraArgs(opts.ExtraArgs)
	for idx, arg := range args {
		value, found := strings.CutPrefix(arg, "--ha-connections=")
		if arg == "--ha-connections" && idx+1 < len(args) {
			value, found = args[idx+1], true
		}
		if found {
			if n, err := strconv.ParseUint(value, 10, 32); err == nil && n > 0 {
				expected = uint(n)
			}
		}
	}
	return expected
}

type readinessResponse struct {
	Status           *int   `json:"status"`
	ReadyConnections *uint  `json:"readyConnections"`
	ConnectorID      string `json:"connectorId"`
}

type readinessResult struct {
	ready            bool
	statusCode       int
	readyConnections uint
	connectorID      string
	err              error
}

func readinessHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &http.Client{
		Timeout:       readinessProbeTimeout,
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func checkReadiness(ctx context.Context, client *http.Client, readyURL string) readinessResult {
	if client == nil {
		client = readinessHTTPClient()
		defer client.CloseIdleConnections()
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
	result := readinessResult{statusCode: resp.StatusCode}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusServiceUnavailable {
		result.err = fmt.Errorf("unexpected readiness HTTP status %d", resp.StatusCode)
		return result
	}
	var body readinessResponse
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 4096))
	if err := decoder.Decode(&body); err != nil {
		result.err = fmt.Errorf("invalid readiness response: %w", err)
		return result
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		result.err = fmt.Errorf("unexpected trailing readiness response")
		return result
	}
	connectorID, err := uuid.Parse(body.ConnectorID)
	if err != nil || connectorID == uuid.Nil() {
		result.err = fmt.Errorf("missing or invalid readiness connector ID")
		return result
	}
	if body.Status == nil || body.ReadyConnections == nil || *body.Status != resp.StatusCode ||
		(resp.StatusCode == http.StatusOK) != (*body.ReadyConnections > 0) {
		result.err = fmt.Errorf("inconsistent readiness status and connection count")
		return result
	}
	result.readyConnections = *body.ReadyConnections
	result.connectorID = connectorID.String()
	result.ready = resp.StatusCode == http.StatusOK
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
	client := readinessHTTPClient()
	defer client.CloseIdleConnections()
	ticker := time.NewTicker(readinessProbeInterval)
	defer ticker.Stop()

	startedAt := time.Now()
	readySeen := false
	failures, unknownFailures := 0, 0
	connectorID := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		probeCtx, cancel := context.WithTimeout(ctx, readinessProbeTimeout)
		result := checkReadiness(probeCtx, client, readyURL)
		cancel()
		if result.err == nil {
			if connectorID == "" {
				connectorID = result.connectorID
			} else if connectorID != result.connectorID {
				result.err = fmt.Errorf("readiness connector identity changed during run")
			}
		}
		if !i.publishReadiness(ctx, generation, result) {
			return
		}
		if result.err == nil && result.ready {
			readySeen = true
			failures, unknownFailures = 0, 0
			continue
		}
		if !readySeen && time.Since(startedAt) < readinessStartupGrace {
			continue
		}
		threshold, count := readinessFailureThreshold, 0
		if result.err != nil {
			unknownFailures++
			failures = 0
			count, threshold = unknownFailures, readinessUnknownThreshold
		} else {
			failures++
			unknownFailures = 0
			count = failures
		}
		if count < threshold {
			continue
		}
		i.mu.Lock()
		autoRestart := i.autoRestart
		i.mu.Unlock()
		// Configuration may disable automatic recovery while this run is live.
		// Keep publishing health even when fresh settings forbid cancellation.
		if i.optsFn != nil {
			opts, err := i.optsFn()
			if err != nil {
				logWarnf("Tunnel %q readiness restart skipped: cannot read configuration: %v", i.name, err)
				continue
			}
			autoRestart = opts.AutoRestart
		}
		if !autoRestart {
			continue
		}
		err := fmt.Errorf("cloudflared readiness failed after %d consecutive checks: %s", count, result.failureSummary())
		if ctx.Err() == nil && i.requestReadinessRestartForRun(err, generation) {
			logWarnf("Tunnel %q readiness watchdog requested restart: %v", i.name, err)
		}
		return
	}
}

// publishReadiness binds every observation to its live lifecycle. Stopping or
// already replaced runs cannot publish a late successful probe.
func (i *Instance) publishReadiness(ctx context.Context, generation uint64, result readinessResult) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if ctx.Err() != nil || i.generation != generation || !i.running ||
		(i.phase != "starting" && i.phase != "running") {
		return false
	}
	now := time.Now()
	i.lastCheckedAt = now
	i.readyConnections = 0
	i.healthError = ""
	if result.err != nil {
		i.connectivity = "unknown"
		i.healthError = result.failureSummary()
		i.healthySince = time.Time{}
		return true
	}
	i.readyConnections = result.readyConnections
	if !result.ready {
		i.connectivity = "disconnected"
		i.healthError = result.failureSummary()
		i.healthySince = time.Time{}
		return true
	}
	i.connectivity = "connected"
	if result.readyConnections < i.expectedConnections {
		i.connectivity = "degraded"
	}
	i.lastConnectedAt = now
	if i.healthySince.IsZero() {
		i.healthySince = now
	}
	if now.Sub(i.healthySince) >= restartBackoffResetAfter {
		i.restartCount = 0
		if i.restartBackoff != nil {
			i.restartBackoff.Reset()
		}
	}
	i.phase = "running"
	i.lastError = nil
	i.nextRetryAt = time.Time{}
	return true
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
	if i.generation != generation || !i.running || i.cancel == nil || i.phase == "stopping" {
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
