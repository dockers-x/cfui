package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestCloudflaredModuleUsesMatchingQUICFork(t *testing.T) {
	rootMod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	cloudflaredVersion := requiredModuleVersion(t, rootMod, "github.com/cloudflare/cloudflared")
	cacheOutput, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		t.Fatalf("resolve Go module cache: %v", err)
	}
	cloudflaredModPath := filepath.Join(strings.TrimSpace(string(cacheOutput)), "github.com/cloudflare/cloudflared@"+cloudflaredVersion, "go.mod")
	cloudflaredMod, err := os.ReadFile(cloudflaredModPath)
	if err != nil {
		t.Fatalf("read cloudflared go.mod: %v", err)
	}

	wantQUICVersion := requiredModuleVersion(t, cloudflaredMod, "github.com/quic-go/quic-go")
	if got := requiredModuleVersion(t, rootMod, "github.com/quic-go/quic-go"); got != wantQUICVersion {
		t.Fatalf("quic-go version = %s, want cloudflared's %s", got, wantQUICVersion)
	}

	wantPath, wantVersion, cloudflaredReplacesQUIC := moduleReplacement(cloudflaredMod, "github.com/quic-go/quic-go")
	gotPath, gotVersion, rootReplacesQUIC := moduleReplacement(rootMod, "github.com/quic-go/quic-go")
	if rootReplacesQUIC != cloudflaredReplacesQUIC || gotPath != wantPath || gotVersion != wantVersion {
		t.Fatalf("quic-go replacement = %q %q, want cloudflared's %q %q", gotPath, gotVersion, wantPath, wantVersion)
	}
}

func requiredModuleVersion(t *testing.T, goMod []byte, modulePath string) string {
	t.Helper()
	pattern := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(modulePath) + `\s+(v\S+)(?:\s+// indirect)?\s*$`)
	match := pattern.FindSubmatch(goMod)
	if len(match) != 2 {
		t.Fatalf("go.mod is missing required module %s", modulePath)
	}
	return string(match[1])
}

func moduleReplacement(goMod []byte, modulePath string) (string, string, bool) {
	pattern := regexp.MustCompile(`(?m)^\s*replace\s+` + regexp.QuoteMeta(modulePath) + `\s*=>\s*(\S+)(?:\s+(v\S+))?(?:\s+//.*)?\s*$`)
	match := pattern.FindSubmatch(goMod)
	if len(match) == 0 {
		return "", "", false
	}
	return string(match[1]), string(match[2]), true
}
