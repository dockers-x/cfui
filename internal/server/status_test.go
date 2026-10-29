package server

import (
	"cfui/internal/mcpbridge"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"cfui/internal/cloudflared"
)

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
