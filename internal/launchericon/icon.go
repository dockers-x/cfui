// Package launchericon publishes deployment-wide LazyCat launcher icons (OS 1.6.2+).
package launchericon

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"cfui/internal/cloudflared"
)

const runtimeDir = "/lzcapp/run"

// Start enables icon publication only inside the LazyCat runtime. The returned
// function cancels and joins the worker before the process exits.
func Start(assets fs.FS, snapshot func() []cloudflared.Status, report func(error)) func() {
	info, err := os.Stat(runtimeDir)
	if err != nil || !info.IsDir() {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := run(ctx, filepath.Join(runtimeDir, "launcher-icon"), assets, snapshot, report); err != nil {
			report(err)
		}
	}()
	return func() { cancel(); <-done }
}

// aggregate mirrors the header's priority: error, recovering, unknown,
// degraded, healthy. A stopped profile cannot make an app appear healthy.
func aggregate(statuses []cloudflared.Status) string {
	rank := 4
	if len(statuses) == 0 {
		return "unknown"
	}
	for _, st := range statuses {
		current := 2
		switch {
		case st.Phase == "error":
			current = 0
		case st.Phase == "starting" || st.Phase == "stopping" || st.Phase == "reconnecting":
			current = 1
		case !st.Running || st.Phase == "stopped":
			current = 2
		case st.Connectivity == "disconnected":
			current = 0
		case st.Connectivity == "degraded":
			current = 3
		case st.Connectivity == "connected":
			current = 4
		}
		rank = min(rank, current)
	}
	switch rank {
	case 0:
		return "error"
	case 1, 3:
		return "warning"
	case 4:
		return "connected"
	default:
		return "unknown"
	}
}

func run(ctx context.Context, dir string, assets fs.FS, snapshot func() []cloudflared.Status, report func(error)) error {
	images := make(map[string][]byte, 4)
	for _, state := range []string{"connected", "warning", "error", "unknown"} {
		data, err := fs.ReadFile(assets, "web/dist/icons/tunnel-health/"+state+".png")
		if err != nil {
			return fmt.Errorf("load launcher icon: %w", err)
		}
		if len(data) > 1<<20 {
			return fmt.Errorf("launcher icon %s exceeds 1 MiB", state)
		}
		images[state] = data
	}
	last := ""
	reported := false
	publish := func(state string) {
		if last == state {
			return
		}
		if err := writeIcon(dir, images[state]); err != nil {
			if !reported {
				report(fmt.Errorf("publish launcher icon: %w", err))
			}
			reported = true
			return
		}
		last, reported = state, false
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			publish("unknown")
			return nil
		}
		publish(aggregate(snapshot()))
		select {
		case <-ctx.Done():
			publish("unknown")
			return nil
		case <-ticker.C:
		}
	}
}

func writeIcon(dir string, image []byte) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".icon-*.png")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0644); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(image); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(dir, "icon.png"))
}
