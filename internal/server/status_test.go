package server

import (
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
