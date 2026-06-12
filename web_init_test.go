package main

import (
	"os"
	"strings"
	"testing"
)

func TestAppInitStartsStatusPollingBeforeOptionalRemoteFeatures(t *testing.T) {
	js, err := os.ReadFile("web/dist/js/app-init.js")
	if err != nil {
		t.Fatalf("read app-init.js: %v", err)
	}
	src := string(js)

	statusIdx := strings.Index(src, "await fetchStatus();")
	intervalIdx := strings.Index(src, "setInterval(fetchStatus, 2000);")
	managerIdx := strings.Index(src, "if (state.features.tunnel_manager)")
	if statusIdx < 0 || intervalIdx < 0 || managerIdx < 0 {
		t.Fatalf("app-init.js is missing expected init markers")
	}
	if statusIdx > managerIdx || intervalIdx > managerIdx {
		t.Fatalf("tunnel status polling must start before optional Tunnel Manager remote initialization")
	}
}
