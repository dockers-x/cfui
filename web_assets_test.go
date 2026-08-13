package main

import (
	"regexp"
	"strings"
	"testing"
)

func TestLocalAuthenticationUsesCustomDialogsAndSafeScriptOrder(t *testing.T) {
	indexBytes, err := assets.ReadFile("web/dist/index.html")
	if err != nil {
		t.Fatal(err)
	}
	index := string(indexBytes)
	for _, id := range []string{"local-auth-login-dialog", "local-auth-setup-dialog", "local-auth-manage-dialog"} {
		if !strings.Contains(index, `id="`+id+`" class="modal-backdrop"`) {
			t.Fatalf("custom dialog %q is missing", id)
		}
	}
	uiPos := strings.Index(index, `/js/app-ui.js`)
	authPos := strings.Index(index, `/js/app-auth.js`)
	initPos := strings.Index(index, `/js/app-init.js`)
	if uiPos < 0 || authPos <= uiPos || initPos <= authPos {
		t.Fatalf("unsafe auth script order: ui=%d auth=%d init=%d", uiPos, authPos, initPos)
	}

	authBytes, err := assets.ReadFile("web/dist/js/app-auth.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"window.alert(", "window.confirm(", "window.prompt("} {
		if strings.Contains(string(authBytes), forbidden) {
			t.Fatalf("local authentication uses browser dialog %q", forbidden)
		}
	}
}

func TestLocalAuthenticationSetupImmediatelyOpensLoginGate(t *testing.T) {
	authBytes, err := assets.ReadFile("web/dist/js/app-auth.js")
	if err != nil {
		t.Fatal(err)
	}
	auth := string(authBytes)
	setupStart := strings.Index(auth, "async function submitSetup(event)")
	if setupStart < 0 {
		t.Fatal("local authentication setup handler is missing")
	}
	setupEnd := strings.Index(auth[setupStart:], "async function submitPasswordChange(event)")
	if setupEnd < 0 {
		t.Fatal("local authentication password-change handler is missing")
	}
	setup := auth[setupStart : setupStart+setupEnd]
	loginPos := strings.Index(setup, "openLoginDialog();")
	pollPos := strings.Index(setup, "startLoginStatusPolling();")
	if loginPos < 0 || pollPos <= loginPos {
		t.Fatalf("setup does not immediately open and maintain the login gate: login=%d poll=%d", loginPos, pollPos)
	}
	recoveryPos := strings.Index(setup, "const recovered = await fetchLocalAuthStatus();")
	recoveryGatePos := strings.LastIndex(setup, "openLoginDialog();")
	if recoveryPos < 0 || recoveryGatePos <= recoveryPos {
		t.Fatalf("setup cannot recover when protection was enabled before the response failed: status=%d gate=%d", recoveryPos, recoveryGatePos)
	}
}

func TestLocalAuthenticationRepeatedGateEventsPreserveLoginInput(t *testing.T) {
	authBytes, err := assets.ReadFile("web/dist/js/app-auth.js")
	if err != nil {
		t.Fatal(err)
	}
	auth := string(authBytes)
	openStart := strings.Index(auth, "function openLoginDialog()")
	if openStart < 0 {
		t.Fatal("local authentication login gate is missing")
	}
	openEnd := strings.Index(auth[openStart:], "function waitForLogin()")
	if openEnd < 0 {
		t.Fatal("local authentication login waiter is missing")
	}
	open := auth[openStart : openStart+openEnd]
	guardPos := strings.Index(open, "if (!dialog.hidden) return;")
	resetPos := strings.Index(open, "$('local-auth-login-password').value = '';")
	if guardPos < 0 || resetPos <= guardPos {
		t.Fatalf("repeated login gates can reset in-progress input: guard=%d reset=%d", guardPos, resetPos)
	}
}

func TestIndexElementIDsRemainUnique(t *testing.T) {
	indexBytes, err := assets.ReadFile("web/dist/index.html")
	if err != nil {
		t.Fatal(err)
	}
	matches := regexp.MustCompile(`\bid="([^"]+)"`).FindAllStringSubmatch(string(indexBytes), -1)
	seen := make(map[string]bool, len(matches))
	for _, match := range matches {
		if seen[match[1]] {
			t.Fatalf("duplicate element id %q", match[1])
		}
		seen[match[1]] = true
	}
}
