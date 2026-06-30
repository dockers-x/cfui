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
	want := []string{"cloudflared", "tunnel", "--no-autoupdate", "run", "--token", "tok"}
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
		"--edge-ip-version", "4",
		"--edge-bind-address", "192.0.2.1",
		"run", "--token", "tok",
		"--protocol", "http2",
		"--grace-period", "10s",
		"--region", "us",
		"--retries", "3",
		"--metrics", "localhost:60123",
		"--loglevel", "debug",
		"--logfile", "/tmp/t.log",
		"--log-format", "json",
		"--post-quantum",
		"--no-tls-verify",
		"--ha-connections", "8", "--tag", "a b",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("BuildArgs = %v, want %v", args, want)
	}
}

// edgeFlagsMustPrecedeRun guards the cloudflared CLI contract: --edge and
// --edge-ip-version are tunnel-command options. Placed after "run" the
// connector dies with "flag provided but not defined", so they must appear
// before the "run" token.
func TestBuildArgsEdgeFlagsPrecedeRun(t *testing.T) {
	opts := Options{
		Token:         "tok",
		EdgeIPVersion: "6",
		Edge:          "2606:4700:a8::1 2606:4700:a8::2,2606:4700:a8::3",
	}
	args := BuildArgs(opts, "http2", "")

	runIdx := indexOf(args, "run")
	if runIdx < 0 {
		t.Fatalf("no run token in %v", args)
	}
	for _, flag := range []string{"--edge-ip-version", "--edge"} {
		idx := indexOf(args, flag)
		if idx < 0 {
			t.Fatalf("%s missing from %v", flag, args)
		}
		if idx > runIdx {
			t.Fatalf("%s at %d must precede run at %d: %v", flag, idx, runIdx, args)
		}
	}

	// Each edge address must be emitted as its own repeated --edge flag.
	var edges []string
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--edge" {
			edges = append(edges, args[i+1])
		}
	}
	wantEdges := []string{"2606:4700:a8::1", "2606:4700:a8::2", "2606:4700:a8::3"}
	if !reflect.DeepEqual(edges, wantEdges) {
		t.Fatalf("edges = %v, want %v", edges, wantEdges)
	}
}

func TestSplitEdges(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"a", []string{"a"}},
		{"a b\tc", []string{"a", "b", "c"}},
		{"a, b ,c", []string{"a", "b", "c"}},
		{"a\nb", []string{"a", "b"}},
	}
	for _, tc := range cases {
		if got := splitEdges(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("splitEdges(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
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
	// Explicit protocol always wins.
	if got := inst.selectProtocol("http2"); got != "http2" {
		t.Fatalf("explicit protocol = %q, want http2", got)
	}
	// Back to auto: keeps current until failures accumulate.
	inst.currentProtocol = "quic"
	if got := inst.selectProtocol("auto"); got != "quic" {
		t.Fatalf("auto protocol = %q, want quic", got)
	}
	inst.protocolFailures["quic"] = maxProtocolFailuresBeforeSwitch
	if got := inst.selectProtocol("auto"); got != "http2" {
		t.Fatalf("after failures protocol = %q, want http2", got)
	}
	if inst.protocolFailures["quic"] != 0 {
		t.Fatalf("quic failures not reset after switch: %d", inst.protocolFailures["quic"])
	}
	inst.mu.Unlock()
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
