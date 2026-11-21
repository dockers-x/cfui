package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"cfui/internal/cloudflared"
	"cfui/internal/config"
	"cfui/internal/mcpbridge"
	"cfui/internal/service"
)

func BenchmarkTunnelStatusPoll(b *testing.B) {
	for _, count := range []int{1, 32, 128} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			manager, err := config.NewManager(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			cfg := manager.Get()
			cfg.Tunnels = make([]config.TunnelProfileConfig, count)
			for i := range cfg.Tunnels {
				cfg.Tunnels[i] = config.DefaultTunnelProfileConfig()
				cfg.Tunnels[i].Key = fmt.Sprintf("profile-%d", i)
			}
			if err := manager.Save(cfg); err != nil {
				b.Fatal(err)
			}
			s := Server{cfgMgr: manager, runner: service.NewRunner(manager)}
			b.ReportAllocs()
			for b.Loop() {
				if len(s.tunnelsResponse(manager.Get()).Statuses) != count {
					b.Fatal("missing profile status")
				}
			}
		})
	}
}

func TestStatusResponsePreservesTransitionalState(t *testing.T) {
	next := time.Now().UTC().Add(time.Minute)
	resp := statusResponseFrom(cloudflared.Status{
		Running:     false,
		Phase:       "reconnecting",
		LastError:   errors.New("connection refused"),
		RetryCount:  2,
		NextRetryAt: &next,
		Protocol:    "quic",
	})
	if resp.Status != "reconnecting" || resp.RetryCount != 2 || resp.NextRetryAt == nil || resp.Error == "" {
		t.Fatalf("transition metadata lost: %#v", resp)
	}
}

func TestStatusResponseSharesMCPHealthContract(t *testing.T) {
	checked := time.Now().UTC().Truncate(time.Second)
	connected := checked.Add(-time.Minute)
	input := cloudflared.Status{
		Running: true, Phase: "running", Connectivity: "degraded", ReadyConnections: 1,
		ExpectedConnections: 4, LastCheckedAt: &checked, LastConnectedAt: &connected,
		HealthError: "some edge connections unavailable",
	}
	api, err := json.Marshal(statusResponseFrom(input))
	if err != nil {
		t.Fatal(err)
	}
	mcp, err := json.Marshal(mcpbridge.TunnelStatusFrom(input))
	if err != nil {
		t.Fatal(err)
	}
	if string(api) != string(mcp) {
		t.Fatalf("HTTP and MCP differ: %s != %s", api, mcp)
	}
	var fields map[string]any
	if err := json.Unmarshal(api, &fields); err != nil {
		t.Fatal(err)
	}
	for key, expected := range map[string]any{
		"connectivity": "degraded", "ready_connections": float64(1), "expected_connections": float64(4),
		"last_checked_at": checked.Format(time.RFC3339), "last_connected_at": connected.Format(time.RFC3339),
		"health_error": input.HealthError,
	} {
		if fields[key] != expected {
			t.Errorf("%s = %v; want %v", key, fields[key], expected)
		}
	}
	resp := statusResponseFrom(input)
	resp.Reset()
	if resp.LastCheckedAt != nil || resp.Connectivity != "" || resp.HealthError != "" {
		t.Fatalf("pool retains health: %#v", resp)
	}
}
