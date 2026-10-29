package cloudflared

import (
	"fmt"
	"strings"
)

// Options describes one tunnel launch. It mirrors the cloudflared CLI flags
// the control panel exposes and carries no references to the configuration
// store, so callers can derive it from any source (active profile, a specific
// profile for multi-instance use, tests, ...).
type Options struct {
	Token           string
	CustomTag       string
	SoftwareName    string
	Protocol        string // auto, http2, quic
	GracePeriod     string // e.g. "30s"
	Region          string
	Retries         int
	MetricsEnable   bool
	MetricsPort     int
	MetricsAddress  string // explicit host:port, used by the readiness watchdog
	LogLevel        string
	LogFile         string
	LogJSON         bool
	EdgeIPVersion   string // auto, 4, 6
	EdgeBindAddress string
	PostQuantum     bool
	NoTLSVerify     bool
	ExtraArgs       string

	// AutoRestart controls whether the instance restarts itself with
	// exponential backoff after an unexpected exit.
	AutoRestart bool
}

// Validate reports whether the options are sufficient to launch a tunnel.
func (o Options) Validate() error {
	if strings.TrimSpace(o.Token) == "" {
		return fmt.Errorf("token is required")
	}
	return nil
}

// BuildArgs assembles the cloudflared CLI invocation for the given options.
// protocol preserves the configured protocol, including upstream auto fallback;
// configFile is the optional temporary YAML
// config path used for custom tags. cloudflared has two flag scopes here:
// parent tunnel-command flags must sit before "run", and run subcommand flags
// must sit after it.
func BuildArgs(o Options, protocol, configFile string) []string {
	args := []string{"cloudflared", "tunnel"}
	extraParentArgs, extraRunArgs := splitExtraArgs(ParseExtraArgs(o.ExtraArgs))

	if configFile != "" {
		args = append(args, "--config", configFile)
	}
	// Disable auto-update to prevent panics in embedded usage (the updater
	// expects non-nil parameters that only exist in the real CLI).
	args = append(args, "--no-autoupdate")

	if o.GracePeriod != "" && o.GracePeriod != "30s" {
		args = append(args, "--grace-period", o.GracePeriod)
	}
	if o.Region != "" {
		args = append(args, "--region", o.Region)
	}
	if o.Retries > 0 && o.Retries != 5 {
		args = append(args, "--retries", fmt.Sprintf("%d", o.Retries))
	}
	metricsAddress := strings.TrimSpace(o.MetricsAddress)
	if metricsAddress == "" && o.MetricsEnable {
		metricsAddress = fmt.Sprintf("localhost:%d", o.MetricsPort)
	}
	if metricsAddress != "" && metricsAddressFromExtraArgs(o.ExtraArgs) == "" {
		args = append(args, "--metrics", metricsAddress)
	}
	if o.LogLevel != "" && o.LogLevel != "info" {
		args = append(args, "--loglevel", o.LogLevel)
	}
	if o.LogFile != "" {
		args = append(args, "--logfile", o.LogFile)
	}
	if o.LogJSON {
		args = append(args, "--output", "json")
	}
	if o.EdgeIPVersion != "" && o.EdgeIPVersion != "auto" {
		args = append(args, "--edge-ip-version", o.EdgeIPVersion)
	}
	if o.EdgeBindAddress != "" {
		args = append(args, "--edge-bind-address", o.EdgeBindAddress)
	}
	args = append(args, extraParentArgs...)

	args = append(args, "run", "--token", o.Token)

	if protocol != "" {
		args = append(args, "--protocol", protocol)
	}
	if o.PostQuantum {
		args = append(args, "--post-quantum")
	}
	if o.NoTLSVerify {
		args = append(args, "--no-tls-verify")
	}
	args = append(args, extraRunArgs...)
	return args
}

var parentExtraFlags = flagSet(
	"api-ca-key",
	"api-email",
	"api-key",
	"api-url",
	"autoupdate-freq",
	"cacert",
	"compression-quality",
	"config",
	"dial-edge-timeout",
	"edge",
	"edge-bind-address",
	"edge-ip-version",
	"grace-period",
	"ha-connections",
	"heartbeat-count",
	"heartbeat-interval",
	"hostname",
	"id",
	"is-autoupdated",
	"label",
	"lb-pool",
	"log-directory",
	"logfile",
	"loglevel",
	"management-diagnostics",
	"max-edge-addr-retries",
	"max-fetch-size",
	"metrics",
	"metrics-update-freq",
	"name",
	"no-autoupdate",
	"no-prechecks",
	"origincert",
	"output",
	"pidfile",
	"prechecks",
	"proto-loglevel",
	"quick-service",
	"quic-connection-level-flow-control-limit",
	"quic-disable-pmtu-discovery",
	"quic-stream-level-flow-control-limit",
	"region",
	"retries",
	"rpc-timeout",
	"stdin-control",
	"tag",
	"trace-output",
	"transport-loglevel",
	"ui",
	"use-reconnect-token",
	"write-stream-timeout",
)

var parentExtraFlagsWithValue = flagSet(
	"api-ca-key",
	"api-email",
	"api-key",
	"api-url",
	"autoupdate-freq",
	"cacert",
	"compression-quality",
	"config",
	"dial-edge-timeout",
	"edge",
	"edge-bind-address",
	"edge-ip-version",
	"grace-period",
	"ha-connections",
	"heartbeat-count",
	"heartbeat-interval",
	"hostname",
	"id",
	"label",
	"lb-pool",
	"log-directory",
	"logfile",
	"loglevel",
	"max-edge-addr-retries",
	"max-fetch-size",
	"metrics",
	"metrics-update-freq",
	"name",
	"origincert",
	"output",
	"pidfile",
	"proto-loglevel",
	"quick-service",
	"quic-connection-level-flow-control-limit",
	"quic-stream-level-flow-control-limit",
	"region",
	"retries",
	"rpc-timeout",
	"tag",
	"trace-output",
	"transport-loglevel",
	"write-stream-timeout",
)

func flagSet(flags ...string) map[string]bool {
	set := make(map[string]bool, len(flags))
	for _, flag := range flags {
		set[flag] = true
	}
	return set
}

func splitExtraArgs(extraArgs []string) ([]string, []string) {
	var parentArgs []string
	var runArgs []string
	for idx := 0; idx < len(extraArgs); idx++ {
		arg := extraArgs[idx]
		flag := flagName(arg)
		if flag == "" || !parentExtraFlags[flag] {
			runArgs = append(runArgs, arg)
			continue
		}

		parentArgs = append(parentArgs, arg)
		if strings.Contains(arg, "=") || !parentExtraFlagsWithValue[flag] || idx+1 >= len(extraArgs) {
			continue
		}
		idx++
		parentArgs = append(parentArgs, extraArgs[idx])
	}
	return parentArgs, runArgs
}

func flagName(arg string) string {
	arg = strings.TrimSpace(arg)
	arg = strings.TrimLeft(arg, "-")
	if arg == "" {
		return ""
	}
	name, _, _ := strings.Cut(arg, "=")
	return name
}

// ParseExtraArgs splits a space-separated argument string, honoring double
// quotes so values may contain spaces.
func ParseExtraArgs(extraArgs string) []string {
	if extraArgs == "" {
		return nil
	}

	var results []string
	var current strings.Builder
	inQuote := false

	for i := 0; i < len(extraArgs); i++ {
		c := extraArgs[i]

		if c == '"' {
			inQuote = !inQuote
		} else if c == ' ' && !inQuote {
			if current.Len() > 0 {
				results = append(results, current.String())
				current.Reset()
			}
		} else {
			current.WriteByte(c)
		}
	}

	if current.Len() > 0 {
		results = append(results, current.String())
	}

	return results
}
