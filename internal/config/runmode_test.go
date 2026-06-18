package config

import "testing"

func TestRunModeFromEnvDefaultsToClassic(t *testing.T) {
	t.Setenv("CFUI_RUN_MODE", "")
	t.Setenv("CFUI_MODE", "")

	got := RunModeFromEnv()
	if got.Mode != RunModeClassic || got.InvalidRaw != "" {
		t.Fatalf("unexpected mode: %#v", got)
	}
}

func TestRunModeFromEnvRecognizesSupportedModes(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want RunMode
	}{
		{name: "classic", raw: "classic", want: RunModeClassic},
		{name: "oauth", raw: "oauth", want: RunModeOAuth},
		{name: "both", raw: "both", want: RunModeBoth},
		{name: "trim and case", raw: " OAuth ", want: RunModeOAuth},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CFUI_RUN_MODE", tt.raw)
			got := RunModeFromEnv()
			if got.Mode != tt.want || got.InvalidRaw != "" {
				t.Fatalf("unexpected mode for %q: %#v", tt.raw, got)
			}
		})
	}
}

func TestRunModeFromEnvInvalidFallsBackToClassic(t *testing.T) {
	t.Setenv("CFUI_RUN_MODE", "account")

	got := RunModeFromEnv()
	if got.Mode != RunModeClassic || got.InvalidRaw != "account" {
		t.Fatalf("unexpected mode: %#v", got)
	}
}

func TestRunModeDefaultWorkspace(t *testing.T) {
	tests := []struct {
		mode RunMode
		want string
	}{
		{mode: RunModeClassic, want: "local"},
		{mode: RunModeOAuth, want: "cloudflare"},
		{mode: RunModeBoth, want: "local"},
	}

	for _, tt := range tests {
		if got := tt.mode.DefaultWorkspace(); got != tt.want {
			t.Fatalf("%s default workspace = %q, want %q", tt.mode, got, tt.want)
		}
	}
}

func TestRunModeAutoStartsLocalRunner(t *testing.T) {
	if !RunModeClassic.AutoStartsLocalRunner() {
		t.Fatal("classic mode should auto-start the local runner")
	}
	if RunModeOAuth.AutoStartsLocalRunner() {
		t.Fatal("oauth mode should skip local runner auto-start")
	}
	if !RunModeBoth.AutoStartsLocalRunner() {
		t.Fatal("both mode should auto-start the local runner")
	}
}
