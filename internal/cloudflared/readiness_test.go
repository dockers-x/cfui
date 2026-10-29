package cloudflared

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheckReadinessUsesCloudflaredReadyStatus(t *testing.T) {
	cases := []struct {
		name             string
		status           int
		readyConnections uint
		wantReady        bool
	}{
		{
			name:             "active edge connection",
			status:           http.StatusOK,
			readyConnections: 1,
			wantReady:        true,
		},
		{
			name:             "no active edge connection",
			status:           http.StatusServiceUnavailable,
			readyConnections: 0,
			wantReady:        false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/ready" {
					t.Fatalf("unexpected path %q", r.URL.Path)
				}
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprintf(w, `{"status":%d,"readyConnections":%d,"connectorId":"11111111-1111-4111-8111-111111111111"}`, tc.status, tc.readyConnections)
			}))
			t.Cleanup(server.Close)

			result := checkReadiness(t.Context(), server.Client(), server.URL+"/ready")
			if result.err != nil {
				t.Fatalf("checkReadiness returned error: %v", result.err)
			}
			if result.ready != tc.wantReady {
				t.Fatalf("ready = %v, want %v", result.ready, tc.wantReady)
			}
			if result.readyConnections != tc.readyConnections {
				t.Fatalf("readyConnections = %d, want %d", result.readyConnections, tc.readyConnections)
			}
		})
	}
}

func TestReadinessAddressSelection(t *testing.T) {
	cases := []struct {
		name string
		opts Options
		want string
	}{
		{
			name: "extra args metrics wins",
			opts: Options{
				MetricsEnable:  true,
				MetricsPort:    60123,
				MetricsAddress: "127.0.0.1:44444",
				ExtraArgs:      "--metrics localhost:55555",
			},
			want: "localhost:55555",
		},
		{
			name: "explicit metrics address",
			opts: Options{MetricsAddress: "127.0.0.1:44444"},
			want: "127.0.0.1:44444",
		},
		{
			name: "configured metrics port",
			opts: Options{MetricsEnable: true, MetricsPort: 60123},
			want: "localhost:60123",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveMetricsAddress(tc.opts); got != tc.want {
				t.Fatalf("effectiveMetricsAddress() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRequestReadinessRestartCancelsOutsideLock(t *testing.T) {
	inst := NewInstance("test", func() (Options, error) { return Options{Token: "tok", AutoRestart: true}, nil })
	ctx, cancel := context.WithCancel(context.Background())
	inst.mu.Lock()
	inst.running = true
	inst.phase = "starting"
	inst.autoRestart = true
	inst.cancel = cancel
	inst.mu.Unlock()

	err := errors.New("ready endpoint returned 503")
	if !inst.requestReadinessRestart(err) {
		t.Fatal("requestReadinessRestart returned false")
	}

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("restart request did not cancel the run context")
	}

	st := inst.Status()
	if st.LastError == nil || st.LastError.Error() != err.Error() {
		t.Fatalf("last error = %v, want %v", st.LastError, err)
	}
	if !inst.hasRestartRequest() {
		t.Fatal("restart request flag was not set")
	}
	if st.Phase != "reconnecting" {
		t.Fatalf("phase = %q, want reconnecting", st.Phase)
	}
}

func TestReadinessWatchdogRequestsRestartAfterConsecutiveFailures(t *testing.T) {
	oldInterval := readinessProbeInterval
	oldTimeout := readinessProbeTimeout
	oldGrace := readinessStartupGrace
	oldThreshold := readinessFailureThreshold
	readinessProbeInterval = time.Millisecond
	readinessProbeTimeout = 100 * time.Millisecond
	readinessStartupGrace = 0
	readinessFailureThreshold = 2
	t.Cleanup(func() {
		readinessProbeInterval = oldInterval
		readinessProbeTimeout = oldTimeout
		readinessStartupGrace = oldGrace
		readinessFailureThreshold = oldThreshold
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":503,"readyConnections":0,"connectorId":"11111111-1111-4111-8111-111111111111"}`))
	}))
	t.Cleanup(server.Close)

	inst := NewInstance("test", func() (Options, error) { return Options{Token: "tok", AutoRestart: true}, nil })
	ctx, cancel := context.WithCancel(context.Background())
	inst.mu.Lock()
	inst.running = true
	inst.phase = "starting"
	inst.autoRestart = true
	inst.cancel = cancel
	inst.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		inst.monitorReadiness(ctx, server.URL)
	}()

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("watchdog did not cancel the run context")
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watchdog goroutine did not exit")
	}

	if !inst.hasRestartRequest() {
		t.Fatal("watchdog did not set restart request")
	}
	if st := inst.Status(); st.LastError == nil {
		t.Fatal("watchdog did not record a readiness error")
	}
}

func TestReadinessSuccessCannotOverwriteStoppingPhase(t *testing.T) {
	inst := NewInstance("test", func() (Options, error) { return Options{Token: "tok"}, nil })
	inst.mu.Lock()
	inst.running = true
	inst.phase = "stopping"
	inst.generation = 7
	inst.lastError = errors.New("stop in progress")
	inst.mu.Unlock()

	inst.markRunning(7)
	status := inst.Status()
	if status.Phase != "stopping" || status.LastError == nil {
		t.Fatalf("readiness overwrote stopping state: %#v", status)
	}
}

func TestReadinessRejectsInvalidAndMismatchedResponses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"html success", 200, "<html>OK</html>"},
		{"empty success", 200, `{}`},
		{"missing connection count", 200, `{"status":200,"connectorId":"11111111-1111-4111-8111-111111111111"}`},
		{"zero success", 200, `{"status":200,"readyConnections":0,"connectorId":"11111111-1111-4111-8111-111111111111"}`},
		{"status mismatch", 503, `{"status":200,"readyConnections":1,"connectorId":"11111111-1111-4111-8111-111111111111"}`},
		{"connections on unavailable", 503, `{"status":503,"readyConnections":1,"connectorId":"11111111-1111-4111-8111-111111111111"}`},
		{"bad connector", 200, `{"status":200,"readyConnections":4,"connectorId":"wrong"}`},
		{"nil connector", 200, `{"status":200,"readyConnections":4,"connectorId":"00000000-0000-0000-0000-000000000000"}`},
		{"missing connector", 200, `{"status":200,"readyConnections":4}`},
		{"trailing JSON", 200, `{"status":200,"readyConnections":4,"connectorId":"11111111-1111-4111-8111-111111111111"}{}`},
		{"other error", 502, `{"status":502,"readyConnections":0}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			got := checkReadiness(t.Context(), server.Client(), server.URL)
			if got.err == nil || got.ready {
				t.Fatalf("invalid readiness accepted: %#v", got)
			}
		})
	}
}

func TestReadinessClientDoesNotFollowRedirectsOrProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ready", http.StatusFound)
	}))
	defer server.Close()
	client := readinessHTTPClient()
	defer client.CloseIdleConnections()
	result := checkReadiness(t.Context(), client, server.URL)
	if result.statusCode != http.StatusFound || result.err == nil {
		t.Fatalf("redirect must remain unknown: %#v", result)
	}
	if client.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("local readiness probe must bypass proxy")
	}
}

func TestReadinessStateTracksConnectionsWithoutRestart(t *testing.T) {
	inst := NewInstance("test", nil)
	inst.running = true
	inst.phase = "starting"
	inst.generation = 9
	ctx := t.Context()
	if !inst.publishReadiness(ctx, 9, readinessResult{statusCode: 503}) {
		t.Fatal("disconnection was not published")
	}
	status := inst.Status()
	if status.Connectivity != "disconnected" || status.LastCheckedAt == nil || status.Phase != "starting" || inst.hasRestartRequest() {
		t.Fatalf("first 503 should be visible without restarting: %#v", status)
	}
	inst.publishReadiness(ctx, 9, readinessResult{err: errors.New("probe timed out")})
	status = inst.Status()
	if status.Connectivity != "unknown" || status.ReadyConnections != 0 || status.HealthError == "" {
		t.Fatalf("unknown state lost: %#v", status)
	}
	inst.publishReadiness(ctx, 9, readinessResult{ready: true, statusCode: 200, readyConnections: 1})
	status = inst.Status()
	if status.Connectivity != "degraded" || status.ExpectedConnections != 4 || status.ReadyConnections != 1 || status.LastConnectedAt == nil || status.Phase != "running" || status.HealthError != "" {
		t.Fatalf("partial recovery not represented: %#v", status)
	}
	inst.publishReadiness(ctx, 9, readinessResult{ready: true, statusCode: 200, readyConnections: 4})
	if got := inst.Status().Connectivity; got != "connected" {
		t.Fatalf("state = %q", got)
	}
}

func TestReadinessRejectsStaleGenerationCanceledAndStoppingRuns(t *testing.T) {
	inst := NewInstance("test", nil)
	inst.running = true
	inst.phase = "starting"
	inst.generation = 9
	result := readinessResult{ready: true, readyConnections: 4}
	if inst.publishReadiness(t.Context(), 8, result) {
		t.Fatal("stale generation accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if inst.publishReadiness(ctx, 9, result) {
		t.Fatal("canceled probe accepted")
	}
	inst.phase = "stopping"
	if inst.publishReadiness(t.Context(), 9, result) {
		t.Fatal("stopping probe accepted")
	}
	if status := inst.Status(); status.Connectivity != "unknown" || status.LastCheckedAt != nil {
		t.Fatalf("late probe polluted status: %#v", status)
	}
}

func TestReadinessOnlyResetsRetryBudgetAfterStableHealth(t *testing.T) {
	inst := NewInstance("test", nil)
	inst.running = true
	inst.phase = "running"
	inst.restartCount = 11
	inst.healthySince = time.Now().Add(-restartBackoffResetAfter)
	inst.publishReadiness(t.Context(), 0, readinessResult{ready: true, readyConnections: 4})
	if got := inst.Status().RetryCount; got != 0 {
		t.Fatalf("stable health did not reset retry budget: %d", got)
	}
	inst.mu.Lock()
	inst.restartCount = 11
	inst.mu.Unlock()
	inst.publishReadiness(t.Context(), 0, readinessResult{statusCode: 503})
	inst.publishReadiness(t.Context(), 0, readinessResult{ready: true, readyConnections: 4})
	if got := inst.Status().RetryCount; got != 11 {
		t.Fatalf("brief recovery reset retry budget: %d", got)
	}
}

func TestReadinessConfiguredWhenAutoRestartDisabled(t *testing.T) {
	inst := NewInstance("test", nil)
	opts := Options{AutoRestart: false}
	if got := inst.configureReadinessProbe(&opts); got == "" || opts.MetricsAddress == "" {
		t.Fatalf("readiness disabled with auto-restart off: %q %#v", got, opts)
	}
}

func TestExpectedHAConnections(t *testing.T) {
	for _, tc := range []struct {
		args string
		want uint
	}{
		{"", 4}, {"--ha-connections 1", 1}, {"--ha-connections=8", 8}, {"--ha-connections bad", 4}, {"--ha-connections 2 --ha-connections=3", 3},
	} {
		if got := expectedHAConnections(Options{ExtraArgs: tc.args}); got != tc.want {
			t.Errorf("%q = %d, want %d", tc.args, got, tc.want)
		}
	}
}

func TestReadinessMonitorRecoveryAndDisabledRestart(t *testing.T) {
	oldInterval, oldTimeout := readinessProbeInterval, readinessProbeTimeout
	oldGrace, oldThreshold := readinessStartupGrace, readinessFailureThreshold
	readinessProbeInterval, readinessProbeTimeout = time.Millisecond, time.Second
	readinessStartupGrace, readinessFailureThreshold = 0, 3
	defer func() {
		readinessProbeInterval, readinessProbeTimeout = oldInterval, oldTimeout
		readinessStartupGrace, readinessFailureThreshold = oldGrace, oldThreshold
	}()

	for _, autoRestart := range []bool{false, true} {
		t.Run(fmt.Sprintf("auto_restart_%t", autoRestart), func(t *testing.T) {
			responses := make(chan string)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case body := <-responses:
					status := http.StatusOK
					if body == "disconnected" {
						status = http.StatusServiceUnavailable
					}
					w.WriteHeader(status)
					connections := 4
					if status == http.StatusServiceUnavailable {
						connections = 0
					}
					connectorID := "11111111-1111-4111-8111-111111111111"
					if body == "unknown" {
						connectorID = "22222222-2222-4222-8222-222222222222"
					}
					_, _ = fmt.Fprintf(w, `{"status":%d,"readyConnections":%d,"connectorId":%q}`, status, connections, connectorID)
				case <-r.Context().Done():
				}
			}))
			defer server.Close()
			inst := NewInstance("test", nil)
			ctx, cancel := context.WithCancel(t.Context())
			inst.running, inst.phase, inst.autoRestart, inst.cancel = true, "starting", autoRestart, cancel
			done := make(chan struct{})
			go func() { defer close(done); inst.monitorReadiness(ctx, server.URL) }()
			defer func() { cancel(); <-done }()
			probe := func(state string, expectRestart bool) {
				t.Helper()
				before := inst.Status().LastCheckedAt
				select {
				case responses <- state:
				case <-time.After(time.Second):
					t.Fatal("probe request never arrived")
				}
				deadline := time.Now().Add(time.Second)
				for time.Now().Before(deadline) {
					status := inst.Status()
					if status.LastCheckedAt != nil && (before == nil || status.LastCheckedAt.After(*before)) &&
						status.Connectivity == state && inst.hasRestartRequest() == expectRestart {
						return
					}
					time.Sleep(time.Millisecond)
				}
				t.Fatalf("probe state was not published: %#v", inst.Status())
			}
			probe("disconnected", false)
			probe("disconnected", false)
			probe("connected", false)
			probe("disconnected", false)
			probe("disconnected", false)
			probe("disconnected", autoRestart)
			if !autoRestart {
				probe("connected", false)
				probe("unknown", false)
				if status := inst.Status(); status.ReadyConnections != 0 || status.HealthError == "" {
					t.Fatalf("different connector was accepted: %#v", status)
				}
			}
		})
	}
}

func TestReadinessProbeRefusesOccupiedMetricsPort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	inst := NewInstance("test", nil)
	opts := Options{MetricsAddress: server.Listener.Addr().String()}
	if got := inst.configureReadinessProbe(&opts); got != "" {
		t.Fatalf("would probe unrelated occupied endpoint: %q", got)
	}
}
