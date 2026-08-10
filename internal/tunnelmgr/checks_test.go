package tunnelmgr

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"cfui/internal/config"

	cloudflare "github.com/cloudflare/cloudflare-go"
)

func checkManager(t *testing.T, client *fakeCFClient) *Manager {
	t.Helper()
	cfgMgr := newConfigManager(t)
	cfg := cfgMgr.Get()
	cfg.TunnelManagement = config.TunnelManagementConfig{Enabled: true, AccountID: "account-1", TunnelID: "tunnel-1", APIToken: "token"}
	cfg.Tunnels[0].RemoteManagementEnabled = true
	cfg.Tunnels[0].AccountID = "account-1"
	cfg.Tunnels[0].TunnelID = "tunnel-1"
	if err := cfgMgr.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return NewManagerWithClient(cfgMgr, func(config.TunnelManagementConfig) (cloudflareClient, error) { return client, nil })
}

func TestCheckRulesBatchesDNSByZoneAndClassifiesResults(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	t.Cleanup(origin.Close)
	proxied := true
	client := &fakeCFClient{
		config: cloudflare.TunnelConfigurationResult{TunnelID: "tunnel-1", Config: cloudflare.TunnelConfiguration{Ingress: []cloudflare.UnvalidatedIngressRule{
			{Hostname: "ok.example.com", Service: origin.URL},
			{Hostname: "conflict.example.com", Service: origin.URL},
			{Hostname: "missing.example.com", Service: origin.URL},
			{Service: "http_status:404"},
		}}},
		dnsRecords: []cloudflare.DNSRecord{
			{Type: "CNAME", Name: "ok.example.com", Content: "tunnel-1.cfargotunnel.com", Proxied: &proxied},
			{Type: "A", Name: "conflict.example.com", Content: "192.0.2.1", Proxied: &proxied},
		},
	}
	resp, err := checkManager(t, client).CheckRulesFor(t.Context(), "", RuleCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if client.dnsListCalls != 3 {
		t.Fatalf("DNS list calls = %d, want one exact-name query per rule", client.dnsListCalls)
	}
	if len(resp.Results) != 3 || resp.Results[0].DNS.Status != "ok" || resp.Results[1].DNS.Status != "conflict" || resp.Results[2].DNS.Status != "missing" {
		t.Fatalf("unexpected DNS results: %#v", resp.Results)
	}
	if resp.TunnelID != "tunnel-1" || resp.Version != client.config.Version {
		t.Fatalf("check snapshot identity missing: %#v", resp)
	}
	if resp.Summary.DNSOK != 1 || resp.Summary.DNSConflict != 1 || resp.Summary.DNSMissing != 1 || resp.Summary.OriginOK != 3 {
		t.Fatalf("unexpected summary: %#v", resp.Summary)
	}
}

func TestCheckRulesSingleIndexIsReadOnlyAndScoped(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(origin.Close)
	client := &fakeCFClient{config: cloudflare.TunnelConfigurationResult{TunnelID: "tunnel-1", Config: cloudflare.TunnelConfiguration{Ingress: []cloudflare.UnvalidatedIngressRule{
		{Hostname: "one.example.com", Service: origin.URL},
		{Hostname: "two.example.com", Service: origin.URL},
	}}}}
	resp, err := checkManager(t, client).CheckRulesFor(t.Context(), "", RuleCheckRequest{Indexes: []int{1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Index != 1 {
		t.Fatalf("single check was not scoped: %#v", resp.Results)
	}
	if len(client.updates) != 0 || len(client.dnsRecords) != 0 {
		t.Fatal("read-only check mutated Cloudflare resources")
	}
}

func TestOriginChecksUseAtMostThreeWorkers(t *testing.T) {
	var current atomic.Int32
	var maximum atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := current.Add(1)
		defer current.Add(-1)
		for {
			old := maximum.Load()
			if now <= old || maximum.CompareAndSwap(old, now) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(origin.Close)
	entries := make([]cloudflare.UnvalidatedIngressRule, 6)
	for idx := range entries {
		entries[idx] = cloudflare.UnvalidatedIngressRule{Hostname: "app" + string(rune('a'+idx)) + ".example.com", Service: origin.URL}
	}
	client := &fakeCFClient{config: cloudflare.TunnelConfigurationResult{TunnelID: "tunnel-1", Config: cloudflare.TunnelConfiguration{Ingress: entries}}}
	if _, err := checkManager(t, client).CheckRulesFor(t.Context(), "", RuleCheckRequest{}); err != nil {
		t.Fatal(err)
	}
	if got := maximum.Load(); got < 2 || got > originCheckWorkers {
		t.Fatalf("origin concurrency = %d, want 2..%d", got, originCheckWorkers)
	}
}

func TestClassifyOriginErrors(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{context.DeadlineExceeded, "timeout"},
		{&net.DNSError{Err: "no such host", Name: "missing.invalid"}, "dns_error"},
		{syscall.ECONNREFUSED, "connection_refused"},
		{errors.New("remote error: tls: bad certificate"), "tls_error"},
	}
	for _, tc := range cases {
		if got := classifyOriginError(tc.err).Status; got != tc.want {
			t.Errorf("classifyOriginError(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestFindZoneForHostnamePrefersMostSpecificZone(t *testing.T) {
	zones := []cloudflare.Zone{
		{ID: "parent", Name: "example.com"},
		{ID: "child", Name: "sub.example.com"},
	}
	zone := findZoneForHostname("app.sub.example.com", zones)
	if zone == nil || zone.ID != "child" {
		t.Fatalf("matched zone = %#v, want child zone", zone)
	}
}

func TestCheckRulesReturnsContextErrorInsteadOfPartialResults(t *testing.T) {
	client := &fakeCFClient{config: cloudflare.TunnelConfigurationResult{TunnelID: "tunnel-1", Config: cloudflare.TunnelConfiguration{Ingress: []cloudflare.UnvalidatedIngressRule{
		{Hostname: "app.example.com", Service: "http://localhost:8080"},
	}}}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := checkManager(t, client).CheckRulesFor(ctx, "", RuleCheckRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled checks error = %v, want context.Canceled", err)
	}
}
