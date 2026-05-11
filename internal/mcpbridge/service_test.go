package mcpbridge

import (
	"cfui/internal/config"
	"cfui/internal/ddns"
	"cfui/internal/logger"
	"cfui/internal/service"
	"cfui/internal/tunnelmgr"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var testLoggerOnce sync.Once

func TestMCPHandlerRequiresBearerToken(t *testing.T) {
	svc := newTestService(t)
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rec := httptest.NewRecorder()

	svc.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestMCPClientListsToolsWithToken(t *testing.T) {
	svc := newTestService(t)
	created, err := svc.CreateToken("Agent", "cfui_mcp_client_token")
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if created.Token == "" {
		t.Fatal("expected create response to include token")
	}

	httpServer := httptest.NewServer(svc.Handler())
	defer httpServer.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             httpServer.URL,
		HTTPClient:           &http.Client{Transport: bearerTransport{token: created.Token}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer session.Close()

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	names := map[string]bool{}
	for _, tool := range result.Tools {
		names[tool.Name] = true
	}
	for _, name := range []string{"cfui_get_status", "cfui_update_config", "cfui_add_ingress_rule"} {
		if !names[name] {
			t.Fatalf("expected MCP tool %q in %#v", name, names)
		}
	}

	callResult, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "cfui_get_status"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if callResult.IsError {
		t.Fatalf("tool returned error: %#v", callResult.Content)
	}
}

type bearerTransport struct {
	token string
}

func (t bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(req)
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	testLoggerOnce.Do(func() {
		logDir, err := os.MkdirTemp("", "cfui-mcp-test-logs-*")
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
	return NewService(cfgMgr, service.NewRunner(cfgMgr), tunnelMgr, NewTokenStore(t.TempDir()), ddnsSvc)
}
