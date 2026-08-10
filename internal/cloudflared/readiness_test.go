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
				_, _ = fmt.Fprintf(w, `{"status":%d,"readyConnections":%d}`, tc.status, tc.readyConnections)
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
	inst := NewInstance("test", func() (Options, error) { return Options{Token: "tok"}, nil })
	ctx, cancel := context.WithCancel(context.Background())
	inst.mu.Lock()
	inst.running = true
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
		_, _ = w.Write([]byte(`{"status":503,"readyConnections":0}`))
	}))
	t.Cleanup(server.Close)

	inst := NewInstance("test", func() (Options, error) { return Options{Token: "tok"}, nil })
	ctx, cancel := context.WithCancel(context.Background())
	inst.mu.Lock()
	inst.running = true
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
