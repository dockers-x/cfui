package mcpbridge

import (
	"cfui/internal/cloudflared"
	"errors"
	"testing"
	"time"
)

func TestTunnelStatusRetainsLifecycleAndHealth(t *testing.T) {
	checked := time.Now().UTC()
	for _, phase := range []string{"starting", "stopping", "reconnecting", "running"} {
		t.Run(phase, func(t *testing.T) {
			got := TunnelStatusFrom(cloudflared.Status{
				Running: true, Phase: phase, LastError: errors.New("previous attempt failed"),
				Connectivity: "disconnected", HealthError: "no edge connections", LastCheckedAt: &checked,
				RetryCount: 3, NextRetryAt: &checked, Protocol: "auto", ActiveProtocols: []string{"quic"},
			})
			if got.Protocol != "auto" || len(got.ActiveProtocols) != 1 || got.ActiveProtocols[0] != "quic" || got.Status != phase || !got.Running || got.RetryCount != 3 || got.NextRetryAt != &checked || got.LastCheckedAt != &checked || got.Connectivity != "disconnected" || got.HealthError == "" || got.Error == "" {
				t.Fatalf("status metadata lost: %#v", got)
			}
		})
	}
}

func TestTunnelStatusUnknownBeforeFirstObservation(t *testing.T) {
	got := TunnelStatusFrom(cloudflared.Status{})
	if got.Status != "stopped" || got.Connectivity != "unknown" || got.LastCheckedAt != nil {
		t.Fatalf("unexpected initial status: %#v", got)
	}
}
