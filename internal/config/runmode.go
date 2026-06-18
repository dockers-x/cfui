package config

import (
	"os"
	"strings"
)

const (
	RunModeClassic RunMode = "classic"
	RunModeOAuth   RunMode = "oauth"
	RunModeBoth    RunMode = "both"
)

// RunMode selects the default top-level cfui experience and whether the local
// tunnel runner auto-starts. Both workspaces remain routable in every mode.
type RunMode string

// RunModeSelection records the resolved mode and any invalid raw value.
type RunModeSelection struct {
	Mode       RunMode
	InvalidRaw string
}

// RunModeFromEnv resolves the process run mode. The current cfui behavior is
// the default so existing deployments remain unchanged.
func RunModeFromEnv() RunModeSelection {
	raw := strings.TrimSpace(firstNonEmpty(os.Getenv("CFUI_RUN_MODE"), os.Getenv("CFUI_MODE")))
	if raw == "" {
		return RunModeSelection{Mode: RunModeClassic}
	}
	mode := RunMode(strings.ToLower(raw))
	switch mode {
	case RunModeClassic, RunModeOAuth, RunModeBoth:
		return RunModeSelection{Mode: mode}
	default:
		return RunModeSelection{Mode: RunModeClassic, InvalidRaw: raw}
	}
}

func (m RunMode) DefaultWorkspace() string {
	if m == RunModeOAuth {
		return "cloudflare"
	}
	return "local"
}

func (m RunMode) AutoStartsLocalRunner() bool {
	return m != RunModeOAuth
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
