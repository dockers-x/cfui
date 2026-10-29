package cloudflared

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/cloudflare/cloudflared/connection"
	"github.com/cloudflare/cloudflared/diagnostic"
	"github.com/cloudflare/cloudflared/tunnelstate"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

const protocolConnectorID = "11111111-1111-4111-8111-111111111111"

func TestActiveProtocolsFromUpstreamConnectionEvents(t *testing.T) {
	log := zerolog.Nop()
	tracker := tunnelstate.NewConnTracker(&log)
	handler := diagnostic.NewDiagnosticHandler(&log, 0, nil, uuid.New(), uuid.MustParse(protocolConnectorID), tracker, map[string]string{}, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("/diag/tunnel", handler.TunnelStateHandler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	check := func(want []string) {
		t.Helper()
		got, err := activeProtocols(t.Context(), server.Client(), server.URL+"/ready", protocolConnectorID)
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("protocols = %v, %v; want %v", got, err, want)
		}
	}
	tracker.OnTunnelEvent(connection.Event{Index: 0, EventType: connection.Connected, Protocol: connection.QUIC})
	tracker.OnTunnelEvent(connection.Event{Index: 1, EventType: connection.Connected, Protocol: connection.QUIC})
	check([]string{"quic"})
	// Exercise the real upstream serializer: HTTP2 is zero and its protocol field is omitted.
	tracker.OnTunnelEvent(connection.Event{Index: 0, EventType: connection.Connected, Protocol: connection.HTTP2})
	check([]string{"http2", "quic"})
	tracker.OnTunnelEvent(connection.Event{Index: 1, EventType: connection.Disconnected})
	check([]string{"http2"})
	tracker.OnTunnelEvent(connection.Event{Index: 0, EventType: connection.Reconnecting})
	check(nil)
}

func TestActiveProtocolsRejectInvalidObservations(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		wantError  bool
	}{
		{"unknown protocol", `{"connectorID":"` + protocolConnectorID + `","connections":[{"isConnected":true,"protocol":99}]}`, 200, true},
		{"wrong connector", `{"connectorID":"22222222-2222-4222-8222-222222222222"}`, 200, true},
		{"missing connector", `{}`, 200, true},
		{"malformed", `{`, 200, true},
		{"trailing", `{"connectorID":"` + protocolConnectorID + `"} {}`, 200, true},
		{"unavailable", `not found`, 404, true},
		{"inactive ignored", `{"connectorID":"` + protocolConnectorID + `","connections":[{"protocol":99}]}`, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			t.Cleanup(server.Close)
			got, err := activeProtocols(t.Context(), server.Client(), server.URL+"/ready", protocolConnectorID)
			if (err != nil) != tc.wantError || len(got) != 0 {
				t.Fatalf("protocols = %v, err = %v", got, err)
			}
		})
	}
}

func TestProtocolObservationLifecycleAndSnapshot(t *testing.T) {
	inst := NewInstance("test", nil)
	inst.running = true
	inst.phase = "running"
	inst.generation = 9
	inst.currentProtocol = "auto"
	publish := func(protocols []string) {
		t.Helper()
		if !inst.publishReadiness(t.Context(), 9, readinessResult{ready: true, readyConnections: 4, activeProtocols: protocols}) {
			t.Fatal("observation rejected")
		}
	}
	protocols := []string{"quic"}
	publish(protocols)
	protocols[0] = "mutated"
	status := inst.Status()
	if status.Protocol != "auto" || !slices.Equal(status.ActiveProtocols, []string{"quic"}) {
		t.Fatalf("status: %#v", status)
	}
	status.ActiveProtocols[0] = "mutated"
	if inst.Status().ActiveProtocols[0] != "quic" {
		t.Fatal("snapshot aliases state")
	}
	if inst.publishReadiness(t.Context(), 8, readinessResult{ready: true, activeProtocols: []string{"http2"}}) {
		t.Fatal("stale run accepted")
	}
	publish([]string{"http2"})
	if inst.Status().ActiveProtocols[0] != "http2" {
		t.Fatal("fallback not updated")
	}
	// Missing diagnostics preserves health but clears outdated transport observations.
	publish(nil)
	if status = inst.Status(); status.Connectivity != "connected" || len(status.ActiveProtocols) != 0 || inst.hasRestartRequest() {
		t.Fatalf("diagnostic failure affected health: %#v", status)
	}
	publish([]string{"quic"})
	inst.publishReadiness(t.Context(), 9, readinessResult{statusCode: 503})
	if len(inst.Status().ActiveProtocols) != 0 {
		t.Fatal("disconnection retained protocol")
	}
	publish([]string{"quic"})
	inst.publishReadiness(t.Context(), 9, readinessResult{err: context.DeadlineExceeded})
	if len(inst.Status().ActiveProtocols) != 0 {
		t.Fatal("unknown health retained protocol")
	}
	publish([]string{"quic"})
	inst.running = false
	if err := inst.Stop(); err != nil {
		t.Fatal(err)
	}
	if len(inst.Status().ActiveProtocols) != 0 {
		t.Fatal("stop retained protocol")
	}
}

func TestProtocolDiagnosticFailureDoesNotRestartHealthyRun(t *testing.T) {
	old := readinessProbeInterval
	readinessProbeInterval = time.Millisecond
	t.Cleanup(func() { readinessProbeInterval = old })
	observed := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ready" {
			fmt.Fprintf(w, `{"status":200,"readyConnections":4,"connectorId":"%s"}`, protocolConnectorID)
			return
		}
		w.WriteHeader(404)
		select {
		case observed <- struct{}{}:
		default:
		}
	}))
	t.Cleanup(server.Close)
	inst := NewInstance("test", nil)
	inst.running, inst.autoRestart = true, true
	inst.phase = "running"
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); inst.monitorReadiness(ctx, server.URL+"/ready") }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case <-observed:
	case <-time.After(time.Second):
		t.Fatal("diagnostic not requested")
	}
	deadline := time.After(time.Second)
	for inst.Status().LastCheckedAt == nil {
		select {
		case <-deadline:
			t.Fatal("observation not published")
		case <-time.After(time.Millisecond):
		}
	}
	status := inst.Status()
	if status.Connectivity != "connected" || len(status.ActiveProtocols) != 0 || inst.hasRestartRequest() {
		t.Fatalf("diagnostic failure affected health: %#v", status)
	}
}
