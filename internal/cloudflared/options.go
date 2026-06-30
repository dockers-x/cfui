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
	LogLevel        string
	LogFile         string
	LogJSON         bool
	EdgeIPVersion   string // auto, 4, 6
	EdgeBindAddress string
	Edge            string // space/comma separated edge addresses to pin (host:port)
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
// protocol is the concrete protocol chosen by the fallback logic (may differ
// from o.Protocol in auto mode); configFile is the optional temporary YAML
// config path used for custom tags. --config must sit between "tunnel" and
// "run" because it is a tunnel-command option, not a run option.
func BuildArgs(o Options, protocol, configFile string) []string {
	args := []string{"cloudflared", "tunnel"}

	if configFile != "" {
		args = append(args, "--config", configFile)
	}
	// Disable auto-update to prevent panics in embedded usage (the updater
	// expects non-nil parameters that only exist in the real CLI).
	args = append(args, "--no-autoupdate")

	// Edge selection flags are tunnel-command options, not run options, so
	// they must precede "run". cloudflared rejects them after "run" with
	// "flag provided but not defined", which crash-loops the connector.
	if o.EdgeIPVersion != "" && o.EdgeIPVersion != "auto" {
		args = append(args, "--edge-ip-version", o.EdgeIPVersion)
	}
	if o.EdgeBindAddress != "" {
		args = append(args, "--edge-bind-address", o.EdgeBindAddress)
	}
	for _, e := range splitEdges(o.Edge) {
		args = append(args, "--edge", e)
	}

	args = append(args, "run", "--token", o.Token)

	if protocol != "" && protocol != "auto" {
		args = append(args, "--protocol", protocol)
	}
	if o.GracePeriod != "" && o.GracePeriod != "30s" {
		args = append(args, "--grace-period", o.GracePeriod)
	}
	if o.Region != "" {
		args = append(args, "--region", o.Region)
	}
	if o.Retries > 0 && o.Retries != 5 {
		args = append(args, "--retries", fmt.Sprintf("%d", o.Retries))
	}
	if o.MetricsEnable {
		args = append(args, "--metrics", fmt.Sprintf("localhost:%d", o.MetricsPort))
	}
	if o.LogLevel != "" && o.LogLevel != "info" {
		args = append(args, "--loglevel", o.LogLevel)
	}
	if o.LogFile != "" {
		args = append(args, "--logfile", o.LogFile)
	}
	if o.LogJSON {
		args = append(args, "--log-format", "json")
	}
	if o.PostQuantum {
		args = append(args, "--post-quantum")
	}
	if o.NoTLSVerify {
		args = append(args, "--no-tls-verify")
	}
	if o.ExtraArgs != "" {
		args = append(args, ParseExtraArgs(o.ExtraArgs)...)
	}
	return args
}

// splitEdges splits a user-provided edge list on whitespace and commas,
// dropping empty fields, so a single profile string can pin multiple edges.
func splitEdges(edge string) []string {
	if strings.TrimSpace(edge) == "" {
		return nil
	}
	fields := strings.FieldsFunc(edge, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	return fields
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
