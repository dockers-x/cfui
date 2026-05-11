package server

import (
	"cfui/internal/config"
	"cfui/internal/ddns"
	"cfui/internal/logger"
	"cfui/internal/mcpbridge"
	"cfui/internal/tunnelmgr"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

var mcpServerTestLoggerOnce sync.Once

func TestMCPTokensCreateListAndDelete(t *testing.T) {
	s := newServerTestServer(t)

	createReq := httptest.NewRequest(http.MethodPost, "/api/mcp/tokens", strings.NewReader(`{"name":"Agent","token":"cfui_mcp_visible_once"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	s.handleMCPTokens(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create token status %d: %s", createRec.Code, createRec.Body.String())
	}
	var created mcpbridge.CreatedToken
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode created token: %v", err)
	}
	if created.Token != "cfui_mcp_visible_once" {
		t.Fatalf("create response must reveal token once, got %q", created.Token)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/mcp/tokens", nil)
	listRec := httptest.NewRecorder()
	s.handleMCPTokens(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list token status %d: %s", listRec.Code, listRec.Body.String())
	}
	if strings.Contains(listRec.Body.String(), "cfui_mcp_visible_once") {
		t.Fatalf("list response leaked raw token: %s", listRec.Body.String())
	}
	if !strings.Contains(listRec.Body.String(), "cfui...once") {
		t.Fatalf("list response did not include masked token: %s", listRec.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/mcp/tokens/"+created.ID, nil)
	deleteRec := httptest.NewRecorder()
	s.handleMCPToken(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete token status %d: %s", deleteRec.Code, deleteRec.Body.String())
	}
}

func newServerTestServer(t *testing.T) *Server {
	t.Helper()
	mcpServerTestLoggerOnce.Do(func() {
		logDir, err := os.MkdirTemp("", "cfui-server-test-logs-*")
		if err != nil {
			t.Fatalf("create log dir: %v", err)
		}
		if err := logger.Initialize(&logger.Config{LogDir: logDir, LogLevel: "error"}); err != nil {
			t.Fatalf("initialize logger: %v", err)
		}
	})
	cfgMgr, err := config.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	cfg := cfgMgr.Get()
	cfg.MCPEnabled = true
	if err := cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}
	tunnelMgr := tunnelmgr.NewManager(cfgMgr)
	ddnsSvc := ddns.NewService(cfgMgr)
	return &Server{
		cfgMgr:    cfgMgr,
		tunnelMgr: tunnelMgr,
		mcpSvc:    mcpbridge.NewService(cfgMgr, nil, tunnelMgr, mcpbridge.NewTokenStore(t.TempDir()), ddnsSvc),
		ddnsSvc:   ddnsSvc,
	}
}
