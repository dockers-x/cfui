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
