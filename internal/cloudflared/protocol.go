package cloudflared

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"uuid"

	"github.com/cloudflare/cloudflared/connection"
)

// activeProtocols reads the run's own event-backed connection tracker. The
// diagnostic wire format encodes Protocol as an integer and omits HTTP2 (zero).
func activeProtocols(ctx context.Context, client *http.Client, readyURL, connectorID string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(readyURL, "/ready")+"/diag/tunnel", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected tunnel diagnostics status %d", resp.StatusCode)
	}
	var body struct {
		ConnectorID string `json:"connectorID"`
		Connections []struct {
			Connected bool                `json:"isConnected"`
			Protocol  connection.Protocol `json:"protocol"`
		} `json:"connections"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 65536))
	if err := decoder.Decode(&body); err != nil {
		return nil, fmt.Errorf("invalid tunnel diagnostics: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing tunnel diagnostics")
	}
	id, err := uuid.Parse(body.ConnectorID)
	if err != nil || id == uuid.Nil() || id.String() != connectorID {
		return nil, fmt.Errorf("tunnel diagnostics connector identity mismatch")
	}
	var protocols []string
	for _, conn := range body.Connections {
		if !conn.Connected {
			continue
		}
		switch conn.Protocol {
		case connection.QUIC, connection.HTTP2:
			protocols = append(protocols, conn.Protocol.String())
		default:
			return nil, fmt.Errorf("unknown active connection protocol %d", conn.Protocol)
		}
	}
	slices.Sort(protocols)
	return slices.Compact(protocols), nil
}
