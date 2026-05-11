package ddns

import (
	"cfui/internal/config"
	"cfui/internal/logger"
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"
)

var ddnsTestLoggerOnce sync.Once

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
