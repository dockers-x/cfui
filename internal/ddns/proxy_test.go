package ddns

import (
	"cfui/internal/config"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDetectIPsProxyMode(t *testing.T) {
	// ProxyFromEnvironment caches its first environment, so test each mode in
	// a fresh process with real proxy environment variables.
	mode := os.Getenv("CFUI_TEST_DDNS_PROXY_MODE")
	if mode == "" {
		for _, mode := range []string{"bypass", "inherit"} {
			t.Run(mode, func(t *testing.T) {
				cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestDetectIPsProxyMode$")
				cmd.Env = append(os.Environ(), "CFUI_TEST_DDNS_PROXY_MODE="+mode)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("proxy test: %v\n%s", err, output)
				}
			})
		}
		return
	}

	const directIP, proxyIP = "171.213.201.56", "104.194.8.36"
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, directIP)
	}))
	t.Cleanup(source.Close)
	var proxyHits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		proxyHits.Add(1)
		_, _ = io.WriteString(w, proxyIP)
	}))
	t.Cleanup(proxy.Close)
	for _, key := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"} {
		t.Setenv(key, proxy.URL)
	}
	for _, key := range []string{"NO_PROXY", "no_proxy", "REQUEST_METHOD"} {
		t.Setenv(key, "")
	}

	// Use a non-loopback hostname because Go intentionally bypasses proxies
	// for loopback URLs. Redirect its direct connection to our local source.
	original := http.DefaultTransport
	transport := original.(*http.Transport).Clone()
	dial := transport.DialContext
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if strings.HasPrefix(addr, "ddns-ip-source.invalid:") {
			addr = source.Listener.Addr().String()
		}
		return dial(ctx, network, addr)
	}
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = original; transport.CloseIdleConnections() })

	mgr, err := config.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := mgr.Get()
	cfg.DDNS.UseProxy = mode == "inherit"
	cfg.DDNS.IPSources = []config.IPSource{{URL: "http://ddns-ip-source.invalid/ip", IPType: "ipv4"}}
	cfg.DDNS.MaxRetries = 1
	if err := mgr.Save(cfg); err != nil {
		t.Fatal(err)
	}
	v4, _, err := NewService(mgr).DetectIPs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := proxyIP
	if mode == "bypass" {
		want = directIP
		if proxyHits.Load() != 0 {
			t.Errorf("DDNS sent %d requests through the proxy", proxyHits.Load())
		}
	}
	if v4 != want {
		t.Errorf("detected IP = %s, want %s", v4, want)
	}

	// DDNS must leave the shared transport's proxy policy intact for API calls.
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://ddns-ip-source.invalid/ip", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != proxyIP {
		t.Fatalf("shared HTTP client response = %q, %v; want proxy IP", body, err)
	}
}

func TestSaveConfigProxyOption(t *testing.T) {
	mgr, err := config.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(mgr)
	for _, tt := range []struct {
		name  string
		value *bool
		want  bool
	}{
		{"enable", new(true), true},
		{"omitted preserves setting", nil, true},
		{"disable", new(false), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := svc.SaveConfig(SaveRequest{UseProxy: tt.value, IntervalMins: 5, MaxRetries: 1}); err != nil {
				t.Fatal(err)
			}
			if got := svc.GetConfig().UseProxy; got != tt.want {
				t.Fatalf("saved use_proxy = %v, want %v", got, tt.want)
			}
		})
	}
}
