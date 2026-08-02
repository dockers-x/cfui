package ddns

import (
	"cfui/internal/config"
	"cfui/internal/logger"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	cloudflare "github.com/cloudflare/cloudflare-go"
)

var ddnsTestLoggerOnce sync.Once

type fakeDNSClient struct {
	records         []cloudflare.DNSRecord
	listedZoneID    string
	listedParams    cloudflare.ListDNSRecordsParams
	deletedZoneID   string
	deletedRecordID string
}

func (f *fakeDNSClient) ListDNSRecords(_ context.Context, rc *cloudflare.ResourceContainer, params cloudflare.ListDNSRecordsParams) ([]cloudflare.DNSRecord, *cloudflare.ResultInfo, error) {
	f.listedZoneID = rc.Identifier
	f.listedParams = params
	return f.records, &cloudflare.ResultInfo{}, nil
}

func (f *fakeDNSClient) CreateDNSRecord(_ context.Context, _ *cloudflare.ResourceContainer, _ cloudflare.CreateDNSRecordParams) (cloudflare.DNSRecord, error) {
	return cloudflare.DNSRecord{}, nil
}

func (f *fakeDNSClient) UpdateDNSRecord(_ context.Context, _ *cloudflare.ResourceContainer, _ cloudflare.UpdateDNSRecordParams) (cloudflare.DNSRecord, error) {
	return cloudflare.DNSRecord{}, nil
}

func (f *fakeDNSClient) DeleteDNSRecord(_ context.Context, rc *cloudflare.ResourceContainer, recordID string) error {
	f.deletedZoneID = rc.Identifier
	f.deletedRecordID = recordID
	return nil
}

func TestDeleteRecordDeletesCloudflareRecordBeforeRemovingConfig(t *testing.T) {
	cfgMgr, err := config.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	record := config.DDNSRecord{
		Name: "home.example.com", ZoneID: "zone-1", ZoneName: "example.com",
		Type: "A", Value: "{IPV4}", Comment: "cfui", TTL: 1,
	}
	cfg := cfgMgr.Get()
	cfg.DDNS.Records = []config.DDNSRecord{record}
	if err := cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	client := &fakeDNSClient{records: []cloudflare.DNSRecord{{
		ID: "dns-record-1", Type: record.Type, Name: record.Name,
	}}}
	svc := NewService(cfgMgr)
	svc.newDNSClient = func() (dnsRecordClient, error) { return client, nil }

	if err := svc.DeleteRecord(context.Background(), 0); err != nil {
		t.Fatalf("DeleteRecord: %v", err)
	}

	if client.listedZoneID != record.ZoneID || client.listedParams.Type != record.Type || client.listedParams.Name != record.Name {
		t.Fatalf("unexpected Cloudflare lookup: zone=%q params=%#v", client.listedZoneID, client.listedParams)
	}
	if client.deletedZoneID != record.ZoneID || client.deletedRecordID != "dns-record-1" {
		t.Fatalf("unexpected Cloudflare deletion: zone=%q record=%q", client.deletedZoneID, client.deletedRecordID)
	}
	if got := cfgMgr.Get().DDNS.Records; len(got) != 0 {
		t.Fatalf("DDNS config still contains deleted record: %#v", got)
	}
}

type blockingDNSClient struct {
	mu              sync.Mutex
	listCalls       int
	syncEntered     chan struct{}
	syncCanceled    chan struct{}
	deletedRecordID string
}

func (f *blockingDNSClient) ListDNSRecords(ctx context.Context, _ *cloudflare.ResourceContainer, _ cloudflare.ListDNSRecordsParams) ([]cloudflare.DNSRecord, *cloudflare.ResultInfo, error) {
	f.mu.Lock()
	f.listCalls++
	call := f.listCalls
	f.mu.Unlock()

	if call == 1 {
		close(f.syncEntered)
		<-ctx.Done()
		close(f.syncCanceled)
		return nil, nil, ctx.Err()
	}

	select {
	case <-f.syncCanceled:
		return []cloudflare.DNSRecord{{ID: "dns-record-1"}}, &cloudflare.ResultInfo{}, nil
	default:
		return nil, nil, errors.New("delete lookup started before active sync stopped")
	}
}

func (f *blockingDNSClient) CreateDNSRecord(_ context.Context, _ *cloudflare.ResourceContainer, _ cloudflare.CreateDNSRecordParams) (cloudflare.DNSRecord, error) {
	return cloudflare.DNSRecord{}, nil
}

func (f *blockingDNSClient) UpdateDNSRecord(_ context.Context, _ *cloudflare.ResourceContainer, _ cloudflare.UpdateDNSRecordParams) (cloudflare.DNSRecord, error) {
	return cloudflare.DNSRecord{}, nil
}

func (f *blockingDNSClient) DeleteDNSRecord(_ context.Context, _ *cloudflare.ResourceContainer, recordID string) error {
	f.deletedRecordID = recordID
	return nil
}

func TestDeleteRecordStopsActiveSyncBeforeDeletingCloudflareRecord(t *testing.T) {
	ddnsTestLoggerOnce.Do(func() {
		logDir, err := os.MkdirTemp("", "cfui-ddns-test-logs-*")
		if err != nil {
			t.Fatalf("create log dir: %v", err)
		}
		if err := logger.Initialize(&logger.Config{LogDir: logDir, LogLevel: "error"}); err != nil {
			t.Fatalf("initialize logger: %v", err)
		}
	})

	ipSource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("192.0.2.10"))
	}))
	defer ipSource.Close()

	cfgMgr, err := config.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	record := config.DDNSRecord{
		Name: "home.example.com", ZoneID: "zone-1", ZoneName: "example.com",
		Type: "A", Value: "192.0.2.10", Comment: "cfui", TTL: 1,
	}
	cfg := cfgMgr.Get()
	cfg.DDNS.Enabled = true
	cfg.DDNS.MaxRetries = 1
	cfg.DDNS.IPSources = []config.IPSource{{URL: ipSource.URL, IPType: "ipv4"}}
	cfg.DDNS.Records = []config.DDNSRecord{record}
	if err := cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	client := &blockingDNSClient{
		syncEntered:  make(chan struct{}),
		syncCanceled: make(chan struct{}),
	}
	svc := NewService(cfgMgr)
	svc.newDNSClient = func() (dnsRecordClient, error) { return client, nil }
	svc.Start()
	defer svc.Stop()

	select {
	case <-client.syncEntered:
	case <-time.After(time.Second):
		t.Fatal("background sync did not start")
	}

	if err := svc.DeleteRecord(context.Background(), 0); err != nil {
		t.Fatalf("DeleteRecord: %v", err)
	}
	if client.deletedRecordID != "dns-record-1" {
		t.Fatalf("deleted record = %q, want dns-record-1", client.deletedRecordID)
	}
}

func TestDetectIPStopsWhenContextExpiresDuringBackoff(t *testing.T) {
	ddnsTestLoggerOnce.Do(func() {
		logDir, err := os.MkdirTemp("", "cfui-ddns-test-logs-*")
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
	cfg.DDNS.MaxRetries = 3
	if err := cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	svc := NewService(cfgMgr)
	sources := []config.IPSource{{URL: "://bad-url", IPType: "ipv4"}}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = svc.detectIP(ctx, sources, "ipv4")
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("detectIP error = %v, want context cancellation", err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("detectIP waited too long after context cancellation: %v", elapsed)
	}
}

func TestGetConfigReturnsDefaultIPSourcesWhenStoredSourcesEmpty(t *testing.T) {
	ddnsTestLoggerOnce.Do(func() {
		logDir, err := os.MkdirTemp("", "cfui-ddns-test-logs-*")
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
	cfg.DDNS.IPSources = []config.IPSource{}
	if err := cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	resp := NewService(cfgMgr).GetConfig()
	defaults := config.DefaultDDNSConfig().IPSources
	if len(resp.DefaultIPSources) != len(defaults) || len(resp.IPSources) != len(defaults) {
		t.Fatalf("expected default sources in response, got default=%d effective=%d", len(resp.DefaultIPSources), len(resp.IPSources))
	}
}

func TestStopCancelsRunningDetectionBackoff(t *testing.T) {
	ddnsTestLoggerOnce.Do(func() {
		logDir, err := os.MkdirTemp("", "cfui-ddns-test-logs-*")
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
	cfg.DDNS.Enabled = true
	cfg.DDNS.MaxRetries = 10
	cfg.DDNS.IPSources = []config.IPSource{{URL: "://bad-url", IPType: "auto"}}
	cfg.DDNS.Records = []config.DDNSRecord{{
		Name: "home.example.com", ZoneID: "zone-1", ZoneName: "example.com",
		Type: "A", Value: "{IPV4}", TTL: 1,
	}}
	if err := cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	svc := NewService(cfgMgr)
	svc.Start()

	start := time.Now()
	svc.Stop()
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Stop waited too long for DDNS detection to cancel: %v", elapsed)
	}
}
