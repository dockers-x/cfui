package cloudflared

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestBuildArgsMinimal(t *testing.T) {
	args := BuildArgs(Options{Token: "tok"}, "auto", "")
	want := []string{"cloudflared", "tunnel", "--no-autoupdate", "run", "--token", "tok", "--protocol", "auto"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("BuildArgs = %v, want %v", args, want)
	}
}

func TestBuildArgsFull(t *testing.T) {
	opts := Options{
		Token:           "tok",
		GracePeriod:     "10s",
		Region:          "us",
		Retries:         3,
		MetricsEnable:   true,
		MetricsPort:     60123,
		LogLevel:        "debug",
		LogFile:         "/tmp/t.log",
		LogJSON:         true,
		EdgeIPVersion:   "4",
		EdgeBindAddress: "192.0.2.1",
		PostQuantum:     true,
		NoTLSVerify:     true,
		ExtraArgs:       `--ha-connections 8 --tag "a b"`,
	}
	args := BuildArgs(opts, "http2", "/tmp/cfg.yaml")
	want := []string{
		"cloudflared", "tunnel",
		"--config", "/tmp/cfg.yaml",
		"--no-autoupdate",
		"--grace-period", "10s",
		"--region", "us",
		"--retries", "3",
		"--metrics", "localhost:60123",
		"--loglevel", "debug",
		"--logfile", "/tmp/t.log",
		"--output", "json",
		"--edge-ip-version", "4",
		"--edge-bind-address", "192.0.2.1",
		"--ha-connections", "8", "--tag", "a b",
		"run", "--token", "tok",
		"--protocol", "http2",
		"--post-quantum",
		"--no-tls-verify",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("BuildArgs = %v, want %v", args, want)
	}
}

func TestBuildArgsDefaultsOmitted(t *testing.T) {
	// Default values must not produce flags.
	opts := Options{
		Token:         "tok",
		GracePeriod:   "30s",
		Retries:       5,
		LogLevel:      "info",
		EdgeIPVersion: "auto",
	}
	args := BuildArgs(opts, "", "")
	want := []string{"cloudflared", "tunnel", "--no-autoupdate", "run", "--token", "tok"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("BuildArgs = %v, want %v", args, want)
	}
}

func TestBuildArgsUsesExplicitMetricsAddress(t *testing.T) {
	args := BuildArgs(Options{Token: "tok", MetricsAddress: "127.0.0.1:34567"}, "", "")
	want := []string{"cloudflared", "tunnel", "--no-autoupdate", "--metrics", "127.0.0.1:34567", "run", "--token", "tok"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("BuildArgs = %v, want %v", args, want)
	}
}

func TestBuildArgsDoesNotDuplicateMetricsFromExtraArgs(t *testing.T) {
	opts := Options{
		Token:          "tok",
		MetricsEnable:  true,
		MetricsPort:    60123,
		MetricsAddress: "127.0.0.1:34567",
		ExtraArgs:      "--metrics localhost:45454 --ha-connections 2",
	}
	args := BuildArgs(opts, "", "")
	want := []string{
		"cloudflared", "tunnel", "--no-autoupdate",
		"--metrics", "localhost:45454", "--ha-connections", "2",
		"run", "--token", "tok",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("BuildArgs = %v, want %v", args, want)
	}
}

func TestBuildArgsPlacesTunnelCommandFlagsBeforeRun(t *testing.T) {
	opts := Options{
		Token:           "tok",
		GracePeriod:     "10s",
		Region:          "us",
		Retries:         3,
		MetricsEnable:   true,
		MetricsPort:     60123,
		LogLevel:        "debug",
		LogFile:         "/tmp/t.log",
		LogJSON:         true,
		EdgeIPVersion:   "4",
		EdgeBindAddress: "192.0.2.1",
		ExtraArgs:       "--metrics localhost:45454 --tag custom=a --ha-connections=2",
	}
	args := BuildArgs(opts, "http2", "/tmp/cfg.yaml")

	for _, flag := range []string{
		"--config",
		"--no-autoupdate",
		"--grace-period",
		"--region",
		"--retries",
		"--loglevel",
		"--logfile",
		"--output",
		"--edge-ip-version",
		"--edge-bind-address",
		"--metrics",
		"--tag",
		"--ha-connections=2",
	} {
		if !argBefore(args, flag, "run") {
			t.Fatalf("%s must be before run in %v", flag, args)
		}
	}
	if !argAfter(args, "--protocol", "run") {
		t.Fatalf("--protocol must remain a run subcommand flag in %v", args)
	}
}

func argBefore(args []string, arg, marker string) bool {
	argIdx := indexArg(args, arg)
	markerIdx := indexArg(args, marker)
	return argIdx >= 0 && markerIdx >= 0 && argIdx < markerIdx
}

func argAfter(args []string, arg, marker string) bool {
	argIdx := indexArg(args, arg)
	markerIdx := indexArg(args, marker)
	return argIdx >= 0 && markerIdx >= 0 && argIdx > markerIdx
}

func indexArg(args []string, want string) int {
	for idx, arg := range args {
		if arg == want {
			return idx
		}
	}
	return -1
}

func TestParseExtraArgs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"--a 1", []string{"--a", "1"}},
		{`--name "hello world" --x`, []string{"--name", "hello world", "--x"}},
		{"  spaced   out  ", []string{"spaced", "out"}},
	}
	for _, tc := range cases {
		if got := ParseExtraArgs(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ParseExtraArgs(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestOptionsValidate(t *testing.T) {
	if err := (Options{}).Validate(); err == nil {
		t.Fatal("expected error for missing token")
	}
	if err := (Options{Token: " "}).Validate(); err == nil {
		t.Fatal("expected error for blank token")
	}
	if err := (Options{Token: "tok"}).Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestIsRetryableError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("connection refused"), true},
		{errors.New("dial tcp: i/o timeout"), true},
		{errors.New("Invalid Token provided"), false},
		{errors.New("Provided Tunnel token is not valid.\nSee 'cloudflared tunnel run --help'."), false},
		{errors.New("authentication failed for tunnel"), false},
		{errors.New("something completely unknown"), true},
	}
	for _, tc := range cases {
		if got := IsRetryableError(tc.err); got != tc.want {
			t.Errorf("IsRetryableError(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestIsProtocolRelatedError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("failed to dial to edge with quic"), true},
		{errors.New("connection reset by peer"), true},
		{errors.New("invalid token"), false},
	}
	for _, tc := range cases {
		if got := IsProtocolRelatedError(tc.err); got != tc.want {
			t.Errorf("IsProtocolRelatedError(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestShouldAutoRestartAfterRun(t *testing.T) {
	ctx := context.Background()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	cases := []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{
			name: "clean unexpected exit restarts",
			ctx:  ctx,
			err:  nil,
			want: true,
		},
		{
			name: "retryable error restarts",
			ctx:  ctx,
			err:  errors.New("dial tcp: i/o timeout"),
			want: true,
		},
		{
			name: "invalid token does not restart",
			ctx:  ctx,
			err:  errors.New("Provided Tunnel token is not valid."),
			want: false,
		},
		{
			name: "canceled context does not restart",
			ctx:  canceled,
			err:  errors.New("dial tcp: i/o timeout"),
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldAutoRestartAfterRun(tc.ctx, tc.err); got != tc.want {
				t.Fatalf("shouldAutoRestartAfterRun() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNewRestartBackoffSchedule(t *testing.T) {
	b := NewBackoff(5*time.Millisecond, 40*time.Millisecond, 5*time.Millisecond, true)

	want := []time.Duration{
		5 * time.Millisecond,
		10 * time.Millisecond,
		20 * time.Millisecond,
		40 * time.Millisecond,
		40 * time.Millisecond,
		40 * time.Millisecond,
	}

	for i, expected := range want {
		if got := b.Duration(); got != expected {
			t.Fatalf("duration[%d] = %v, want %v", i, got, expected)
		}
	}
}

func TestBackoffDecayResetsAttempts(t *testing.T) {
	b := NewBackoff(5*time.Millisecond, 40*time.Millisecond, 5*time.Millisecond, true)

	if got := b.Duration(); got != 5*time.Millisecond {
		t.Fatalf("first duration = %v, want 5ms", got)
	}
	if got := b.Duration(); got != 10*time.Millisecond {
		t.Fatalf("second duration = %v, want 10ms", got)
	}

	time.Sleep(20 * time.Millisecond)

	if got := b.Duration(); got != 5*time.Millisecond {
		t.Fatalf("duration after decay = %v, want 5ms", got)
	}
}

func TestInstanceProtocolSelection(t *testing.T) {
	inst := NewInstance("test", func() (Options, error) { return Options{Token: "tok"}, nil })
	inst.mu.Lock()
	defer inst.mu.Unlock()
	for _, protocol := range []string{"http2", "quic", "auto", ""} {
		want := protocol
		if want == "" {
			want = "auto"
		}
		if got := inst.selectProtocol(protocol); got != want {
			t.Fatalf("selectProtocol(%q) = %q, want %q", protocol, got, want)
		}
	}
}

func TestInstanceStartValidation(t *testing.T) {
	inst := NewInstance("test", func() (Options, error) { return Options{}, nil })
	if err := inst.Start(); err == nil {
		t.Fatal("expected validation error for missing token")
	}
	st := inst.Status()
	if st.Running {
		t.Fatal("instance must not be running after failed start")
	}

	inst = NewInstance("test", func() (Options, error) { return Options{}, errors.New("profile disabled") })
	if err := inst.Start(); err == nil || err.Error() != "profile disabled" {
		t.Fatalf("expected provider error, got %v", err)
	}
}

func TestInstanceStopWhenNotRunning(t *testing.T) {
	inst := NewInstance("test", func() (Options, error) { return Options{Token: "tok"}, nil })
	if err := inst.Stop(); err != nil {
		t.Fatalf("Stop on idle instance returned error: %v", err)
	}
}
