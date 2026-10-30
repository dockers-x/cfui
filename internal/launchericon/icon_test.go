package launchericon

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"testing/synctest"
	"time"

	"cfui/internal/cloudflared"
)

func TestAggregateMatchesVisibleHealth(t *testing.T) {
	healthy := cloudflared.Status{Running: true, Phase: "running", Connectivity: "connected"}
	degraded := cloudflared.Status{Running: true, Phase: "running", Connectivity: "degraded"}
	for _, tc := range []struct {
		name     string
		statuses []cloudflared.Status
		want     string
	}{
		{"empty", nil, "unknown"},
		{"healthy", []cloudflared.Status{healthy, healthy}, "connected"},
		{"stopped with stale connection", []cloudflared.Status{{Connectivity: "connected"}, healthy}, "unknown"},
		{"unknown outranks degraded", []cloudflared.Status{{Running: true}, degraded}, "unknown"},
		{"degraded", []cloudflared.Status{healthy, degraded}, "warning"},
		{"recovery outranks unknown", []cloudflared.Status{{Phase: "reconnecting"}, {}}, "warning"},
		{"startup", []cloudflared.Status{{Phase: "starting"}}, "warning"},
		{"stopping", []cloudflared.Status{{Phase: "stopping"}}, "warning"},
		{"disconnected", []cloudflared.Status{healthy, {Running: true, Connectivity: "disconnected"}}, "error"},
		{"lifecycle error", []cloudflared.Status{healthy, {Phase: "error"}, {Phase: "reconnecting"}}, "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := aggregate(tc.statuses); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func testImages(t *testing.T) fstest.MapFS {
	t.Helper()
	assets := fstest.MapFS{}
	for i, state := range []string{"connected", "warning", "error", "unknown"} {
		img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
		img.SetNRGBA(0, 0, color.NRGBA{R: uint8(i * 60), A: 255})
		var data bytes.Buffer
		if err := png.Encode(&data, img); err != nil {
			t.Fatal(err)
		}
		assets["web/dist/icons/tunnel-health/"+state+".png"] = &fstest.MapFile{Data: data.Bytes()}
	}
	return assets
}

func TestWorkerChangesOnlyWithHealthAndResetsOnShutdown(t *testing.T) {
	assets := testImages(t)
	dir := filepath.Join(t.TempDir(), "launcher-icon")
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		var current atomic.Pointer[cloudflared.Status]
		current.Store(&cloudflared.Status{Running: true, Phase: "running", Connectivity: "connected"})
		done := make(chan error, 1)
		go func() {
			done <- run(ctx, dir, assets, func() []cloudflared.Status { return []cloudflared.Status{*current.Load()} }, func(err error) { t.Errorf("unexpected error: %v", err) })
		}()
		synctest.Wait()
		check := func(state string) {
			t.Helper()
			data, err := os.ReadFile(filepath.Join(dir, "icon.png"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(data, assets["web/dist/icons/tunnel-health/"+state+".png"].Data) {
				t.Fatalf("wrong icon, want %s", state)
			}
		}
		check("connected")
		before, err := os.Stat(filepath.Join(dir, "icon.png"))
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(4 * time.Second)
		synctest.Wait()
		after, err := os.Stat(filepath.Join(dir, "icon.png"))
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(before, after) {
			t.Fatal("unchanged state rewrote icon")
		}
		current.Store(&cloudflared.Status{Phase: "error"})
		time.Sleep(2 * time.Second)
		synctest.Wait()
		check("error")
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		check("unknown")
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 1 || entries[0].Name() != "icon.png" {
			t.Fatalf("temporary files leaked: %v, %v", entries, err)
		}
	})
}

func TestWorkerRetriesWriteFailuresWithoutRepeatedLogs(t *testing.T) {
	assets := testImages(t)
	dir := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(dir, []byte("file instead of directory"), 0600); err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		reports := 0
		done := make(chan error, 1)
		go func() {
			done <- run(ctx, dir, assets, func() []cloudflared.Status { return nil }, func(error) { reports++ })
		}()
		synctest.Wait()
		time.Sleep(4 * time.Second)
		synctest.Wait()
		if reports != 1 {
			t.Fatalf("reports=%d", reports)
		}
		if err := os.Remove(dir); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if _, err := os.Stat(filepath.Join(dir, "icon.png")); err != nil {
			t.Fatal(err)
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestMissingAndOversizedArtworkRejected(t *testing.T) {
	assets := testImages(t)
	delete(assets, "web/dist/icons/tunnel-health/connected.png")
	if err := run(t.Context(), t.TempDir(), assets, func() []cloudflared.Status { return nil }, func(error) {}); err == nil {
		t.Fatal("missing artwork accepted")
	}
	assets = testImages(t)
	assets["web/dist/icons/tunnel-health/connected.png"].Data = make([]byte, (1<<20)+1)
	if err := run(t.Context(), t.TempDir(), assets, func() []cloudflared.Status { return nil }, func(error) {}); err == nil {
		t.Fatal("oversized artwork accepted")
	}
}
