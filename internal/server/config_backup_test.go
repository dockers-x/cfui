package server

import (
	"bytes"
	"cfui/internal/cloudflared"
	"cfui/internal/config"
	"cfui/internal/configbackup"
	"cfui/internal/localauth"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestConfigBackupExportRequiresSelectionAndPlaintextSensitiveConfirmation(t *testing.T) {
	s := newServerTestServer(t)
	cfg := s.cfgMgr.Get()
	cfg.Tunnels[0].Token = "tunnel-secret"
	if err := s.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	getRec := httptest.NewRecorder()
	s.handleConfigBackupExport(getRec, httptest.NewRequest(http.MethodGet, "/api/config-backup/export", nil))
	if getRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d", getRec.Code)
	}

	emptyRec := httptest.NewRecorder()
	emptyReq := httptest.NewRequest(http.MethodPost, "/api/config-backup/export", strings.NewReader(`{"sections":[]}`))
	s.handleConfigBackupExport(emptyRec, emptyReq)
	assertBackupErrorCode(t, emptyRec, http.StatusBadRequest, "invalid_selection")

	sensitiveRec := httptest.NewRecorder()
	sensitiveReq := httptest.NewRequest(http.MethodPost, "/api/config-backup/export", strings.NewReader(`{"sections":["tunnels"],"include_sensitive":true}`))
	s.handleConfigBackupExport(sensitiveRec, sensitiveReq)
	assertBackupErrorCode(t, sensitiveRec, http.StatusBadRequest, "plaintext_sensitive_confirmation_required")
}

func TestConfigBackupRejectsCrossSiteRequests(t *testing.T) {
	s := newServerTestServer(t)
	endpoints := []struct {
		name    string
		target  string
		handler http.HandlerFunc
	}{
		{name: "export", target: "/api/config-backup/export", handler: s.handleConfigBackupExport},
		{name: "inspect", target: "/api/config-backup/inspect", handler: s.handleConfigBackupInspect},
		{name: "import", target: "/api/config-backup/import", handler: s.handleConfigBackupImport},
	}
	attacks := []struct {
		name    string
		headers map[string]string
	}{
		{name: "origin", headers: map[string]string{"Origin": "https://evil.example"}},
		{name: "fetch metadata", headers: map[string]string{"Sec-Fetch-Site": "cross-site"}},
		{name: "scheme", headers: map[string]string{"Origin": "http://cfui.example.internal", "X-Forwarded-Proto": "https"}},
	}
	for _, endpoint := range endpoints {
		for _, attack := range attacks {
			t.Run(endpoint.name+"/"+attack.name, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodPost, endpoint.target, strings.NewReader("ignored"))
				req.Host = "cfui.example.internal"
				for name, value := range attack.headers {
					req.Header.Set(name, value)
				}
				rec := httptest.NewRecorder()

				endpoint.handler(rec, req)

				assertBackupErrorCode(t, rec, http.StatusForbidden, "cross_site_request")
			})
		}
	}
}

func TestConfigBackupAcceptsForwardedExternalOrigin(t *testing.T) {
	s := newServerTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/config-backup/export", strings.NewReader(`{"sections":["application"]}`))
	req.Host = "127.0.0.1:14333"
	req.Header.Set("Origin", "https://cfui.example")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "cfui.example")
	rec := httptest.NewRecorder()
	s.handleConfigBackupExport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("forwarded same-origin export status = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestConfigBackupExportOmitsSecretsByDefaultAndEncryptsWhenRequested(t *testing.T) {
	s := newServerTestServer(t)
	cfg := s.cfgMgr.Get()
	cfg.Tunnels[0].Token = "tunnel-secret"
	cfg.OAuthClientID = "oauth-client"
	if err := s.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	plainRec := httptest.NewRecorder()
	plainReq := httptest.NewRequest(http.MethodPost, "/api/config-backup/export", strings.NewReader(`{"sections":["tunnels","application"]}`))
	s.handleConfigBackupExport(plainRec, plainReq)
	if plainRec.Code != http.StatusOK {
		t.Fatalf("plaintext export status %d: %s", plainRec.Code, plainRec.Body.String())
	}
	if !strings.HasPrefix(plainRec.Header().Get("Content-Disposition"), "attachment;") || !strings.Contains(plainRec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("unexpected attachment headers: %#v", plainRec.Header())
	}
	if strings.Contains(plainRec.Body.String(), "tunnel-secret") || !strings.Contains(plainRec.Body.String(), "oauth-client") {
		t.Fatalf("plaintext export secret handling is wrong: %s", plainRec.Body.String())
	}

	encryptedRec := httptest.NewRecorder()
	encryptedReq := httptest.NewRequest(http.MethodPost, "/api/config-backup/export", strings.NewReader(`{"sections":["tunnels","application"],"include_sensitive":true,"password":"optional-password"}`))
	s.handleConfigBackupExport(encryptedRec, encryptedReq)
	if encryptedRec.Code != http.StatusOK {
		t.Fatalf("encrypted export status %d: %s", encryptedRec.Code, encryptedRec.Body.String())
	}
	if strings.Contains(encryptedRec.Body.String(), "tunnel-secret") || strings.Contains(encryptedRec.Body.String(), "oauth-client") {
		t.Fatalf("encrypted export leaked plaintext: %s", encryptedRec.Body.String())
	}
	decoded, err := configbackup.Decode(encryptedRec.Body.Bytes(), "optional-password")
	if err != nil || !decoded.Encrypted || decoded.Payload.Sensitive == nil {
		t.Fatalf("decode encrypted export: decoded=%#v err=%v", decoded, err)
	}
}

func TestConfigBackupNeverIncludesLocalAuthenticationData(t *testing.T) {
	s := newServerTestServer(t)
	s.localAuth = localauth.NewStore(s.cfgMgr.Dir())
	const username = "backup-auth-admin"
	const password = "backup-auth-password"
	if err := s.localAuth.Setup(t.Context(), username, password); err != nil {
		t.Fatal(err)
	}
	token, err := s.localAuth.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/config-backup/export", strings.NewReader(`{"sections":["tunnels","application"],"include_sensitive":true,"confirm_plaintext_sensitive":true}`))
	rec := httptest.NewRecorder()
	s.handleConfigBackupExport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("export status %d: %s", rec.Code, rec.Body.String())
	}
	for _, secret := range []string{username, password, token, "argon2id", "local_auth_sessions", "local_auth_settings"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("config backup leaked local authentication data %q", secret)
		}
	}
}

func TestConfigBackupInspectPreviewsSelectionWithoutWritingFile(t *testing.T) {
	s := newServerTestServer(t)
	current := s.cfgMgr.Get()
	current.Tunnels = append(current.Tunnels, config.TunnelProfileConfig{Key: "remove-me", Name: "Remove", LocalEnabled: true, SoftwareName: "cfui", Protocol: "auto", GracePeriod: "30s", Retries: 5, MetricsPort: 60124, LogLevel: "info", EdgeIPVersion: "auto"})
	if err := s.cfgMgr.Save(current); err != nil {
		t.Fatalf("Save current: %v", err)
	}
	imported := s.cfgMgr.Get()
	imported.Tunnels = imported.Tunnels[:1]
	imported.Tunnels[0].Protocol = "quic"
	backup := encodeConfigBackup(t, imported, []configbackup.Section{configbackup.SectionTunnels}, false, "")

	before, err := os.ReadDir(s.cfgMgr.Dir())
	if err != nil {
		t.Fatalf("ReadDir before: %v", err)
	}
	s.backupHooks.profileStatus = func(key string) (cloudflared.Status, bool) {
		return cloudflared.Status{Running: key == imported.Tunnels[0].Key}, true
	}
	req := multipartBackupRequest(t, "/api/config-backup/inspect", backup, "", []configbackup.Section{configbackup.SectionTunnels})
	rec := httptest.NewRecorder()
	s.handleConfigBackupInspect(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("inspect status %d: %s", rec.Code, rec.Body.String())
	}
	var inspection configbackup.Inspection
	if err := json.NewDecoder(rec.Body).Decode(&inspection); err != nil {
		t.Fatalf("Decode inspection: %v", err)
	}
	if !slices.Equal(inspection.RemovedTunnels, []string{"remove-me"}) || !slices.Equal(inspection.RestartRequired, []string{imported.Tunnels[0].Key}) {
		t.Fatalf("unexpected preview: %#v", inspection)
	}
	after, err := os.ReadDir(s.cfgMgr.Dir())
	if err != nil {
		t.Fatalf("ReadDir after: %v", err)
	}
	if !reflect.DeepEqual(dirEntryNames(before), dirEntryNames(after)) {
		t.Fatalf("inspection wrote files: before=%v after=%v", dirEntryNames(before), dirEntryNames(after))
	}
}

func TestConfigBackupInspectRejectsOversizedFile(t *testing.T) {
	s := newServerTestServer(t)
	oversized := bytes.Repeat([]byte{'x'}, configbackup.MaxBackupBytes+1)
	req := multipartBackupRequest(t, "/api/config-backup/inspect", oversized, "", nil)
	rec := httptest.NewRecorder()
	s.handleConfigBackupInspect(rec, req)
	assertBackupErrorCode(t, rec, http.StatusRequestEntityTooLarge, "too_large")
}

func TestConfigBackupImportReplacesSelectedSectionAndRunsOnlyMatchingHook(t *testing.T) {
	s := newServerTestServer(t)
	before := s.cfgMgr.Get()
	before.TunnelManagement.Enabled = true
	if err := s.cfgMgr.Save(before); err != nil {
		t.Fatalf("enable tunnel manager: %v", err)
	}
	before = s.cfgMgr.Get()
	imported := before
	imported.DDNS = config.DDNSConfig{
		Enabled: true, IntervalMins: 11, MaxRetries: 4,
		IPSources: []config.IPSource{{URL: "https://new-ip.example", IPType: "ipv6"}},
		Records:   []config.DDNSRecord{{Name: "new.example", ZoneID: "zone", ZoneName: "example", Type: "AAAA", Value: "{IPV6}", Comment: "cfui", TTL: 1}},
	}
	backup := encodeConfigBackup(t, imported, []configbackup.Section{configbackup.SectionDDNS}, false, "")

	var ddnsRestarts, s3Restarts, oauthResets int
	s.backupHooks.restartDDNS = func() { ddnsRestarts++ }
	s.backupHooks.restartS3 = func(_ context.Context) error { s3Restarts++; return nil }
	s.backupHooks.resetOAuth = func() { oauthResets++ }
	req := multipartBackupRequest(t, "/api/config-backup/import", backup, "", []configbackup.Section{configbackup.SectionDDNS})
	rec := httptest.NewRecorder()
	s.handleConfigBackupImport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("import status %d: %s", rec.Code, rec.Body.String())
	}
	after := s.cfgMgr.Get()
	if after.DDNS.IntervalMins != 11 || after.DDNS.IPSources[0].URL != "https://new-ip.example" {
		t.Fatalf("DDNS not imported: %#v", after.DDNS)
	}
	if !reflect.DeepEqual(after.Tunnels, before.Tunnels) || !reflect.DeepEqual(after.S3WebDAV, before.S3WebDAV) {
		t.Fatal("unselected configuration changed")
	}
	if ddnsRestarts != 1 || s3Restarts != 0 || oauthResets != 0 {
		t.Fatalf("unexpected runtime hooks: ddns=%d s3=%d oauth=%d", ddnsRestarts, s3Restarts, oauthResets)
	}
}

func TestConfigBackupImportRejectsInvalidSemanticConfiguration(t *testing.T) {
	tests := []struct {
		name     string
		sections []configbackup.Section
		mutate   func(*config.Config)
	}{
		{
			name:     "DDNS requires tunnel manager",
			sections: []configbackup.Section{configbackup.SectionDDNS},
			mutate: func(cfg *config.Config) {
				cfg.TunnelManagement.Enabled = false
				cfg.DDNS.Enabled = true
			},
		},
		{
			name:     "invalid OAuth relay URL",
			sections: []configbackup.Section{configbackup.SectionApplication},
			mutate: func(cfg *config.Config) {
				cfg.OAuthRelayCallbackURL = "javascript:alert(1)"
			},
		},
		{
			name:     "invalid S3 dedicated port",
			sections: []configbackup.Section{configbackup.SectionS3WebDAV},
			mutate: func(cfg *config.Config) {
				cfg.S3WebDAV.DedicatedPort = 70000
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := newServerTestServer(t)
			before := s.cfgMgr.Get()
			imported := before
			test.mutate(&imported)
			backup := encodeConfigBackup(t, imported, test.sections, false, "")
			req := multipartBackupRequest(t, "/api/config-backup/import", backup, "", test.sections)
			rec := httptest.NewRecorder()

			s.handleConfigBackupImport(rec, req)

			assertBackupErrorCode(t, rec, http.StatusBadRequest, "invalid_backup")
			if !reflect.DeepEqual(s.cfgMgr.Get(), before) {
				t.Fatal("invalid import changed persisted configuration")
			}
		})
	}
}

func TestValidateConfigBackupCandidateUsesActiveProfileRemoteState(t *testing.T) {
	s := newServerTestServer(t)
	cfg := s.cfgMgr.Get()
	cfg.DDNS.Enabled = true

	cfg.TunnelManagement.Enabled = false
	cfg.Tunnels[0].RemoteManagementEnabled = true
	if _, err := s.validateConfigBackupCandidate(cfg); err != nil {
		t.Fatalf("active profile enables Remote Tunnel Manager: %v", err)
	}

	cfg.TunnelManagement.Enabled = true
	cfg.Tunnels[0].RemoteManagementEnabled = false
	if _, err := s.validateConfigBackupCandidate(cfg); err == nil {
		t.Fatal("stale top-level Remote Tunnel Manager state bypassed DDNS dependency validation")
	}
}

func TestConfigBackupImportWarnsWhenS3RuntimeReconciliationFails(t *testing.T) {
	s := newServerTestServer(t)
	before := s.cfgMgr.Get()
	imported := before
	imported.S3WebDAV.Enabled = !before.S3WebDAV.Enabled
	backup := encodeConfigBackup(t, imported, []configbackup.Section{configbackup.SectionS3WebDAV}, false, "")

	s.backupHooks.restartS3 = func(context.Context) error {
		return errors.New("runtime unavailable")
	}
	req := multipartBackupRequest(t, "/api/config-backup/import", backup, "", []configbackup.Section{configbackup.SectionS3WebDAV})
	rec := httptest.NewRecorder()

	s.handleConfigBackupImport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("import status %d: %s", rec.Code, rec.Body.String())
	}
	var response configBackupImportResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("Decode response: %v", err)
	}
	if !slices.Contains(response.Warnings, "S3 WebDAV runtime reconciliation failed") {
		t.Fatalf("runtime warning missing: %#v", response.Warnings)
	}
	if reflect.DeepEqual(s.cfgMgr.Get().S3WebDAV, before.S3WebDAV) {
		t.Fatal("S3 WebDAV configuration was not persisted")
	}
}

func TestConfigBackupImportDoesNotRunHooksWhenSaveFails(t *testing.T) {
	s := newServerTestServer(t)
	before := s.cfgMgr.Get()
	before.TunnelManagement.Enabled = true
	if err := s.cfgMgr.Save(before); err != nil {
		t.Fatalf("enable tunnel manager: %v", err)
	}
	before = s.cfgMgr.Get()
	imported := before
	imported.DDNS.Enabled = !before.DDNS.Enabled
	backup := encodeConfigBackup(t, imported, []configbackup.Section{configbackup.SectionDDNS}, false, "")

	var hookCalls int
	s.backupHooks.saveConfig = func(config.Config) error { return errors.New("database unavailable") }
	s.backupHooks.restartDDNS = func() { hookCalls++ }
	req := multipartBackupRequest(t, "/api/config-backup/import", backup, "", []configbackup.Section{configbackup.SectionDDNS})
	rec := httptest.NewRecorder()
	s.handleConfigBackupImport(rec, req)
	assertBackupErrorCode(t, rec, http.StatusInternalServerError, "save_failed")
	if hookCalls != 0 || !reflect.DeepEqual(s.cfgMgr.Get(), before) {
		t.Fatal("failed import changed runtime or persisted configuration")
	}
}

func TestConfigBackupImportRequestsRemovedStopsAndReportsRunningChanges(t *testing.T) {
	s := newServerTestServer(t)
	current := s.cfgMgr.Get()
	current.Tunnels = []config.TunnelProfileConfig{
		{Key: "keep", Name: "Keep", Token: "keep-token", LocalEnabled: true, SoftwareName: "cfui", Protocol: "auto", GracePeriod: "30s", Retries: 5, MetricsPort: 60123, LogLevel: "info", EdgeIPVersion: "auto"},
		{Key: "remove", Name: "Remove", Token: "remove-token", LocalEnabled: true, SoftwareName: "cfui", Protocol: "auto", GracePeriod: "30s", Retries: 5, MetricsPort: 60124, LogLevel: "info", EdgeIPVersion: "auto"},
	}
	current.ActiveTunnelKey = "keep"
	if err := s.cfgMgr.Save(current); err != nil {
		t.Fatalf("Save current: %v", err)
	}
	imported := s.cfgMgr.Get()
	imported.Tunnels = imported.Tunnels[:1]
	imported.Tunnels[0].Protocol = "quic"
	backup := encodeConfigBackup(t, imported, []configbackup.Section{configbackup.SectionTunnels}, false, "")

	removed := make(chan string, 1)
	s.backupHooks.removeProfile = func(key string) error { removed <- key; return nil }
	s.backupHooks.profileStatus = func(key string) (cloudflared.Status, bool) {
		return cloudflared.Status{Running: key == "keep"}, true
	}
	req := multipartBackupRequest(t, "/api/config-backup/import", backup, "", []configbackup.Section{configbackup.SectionTunnels})
	rec := httptest.NewRecorder()
	s.handleConfigBackupImport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("import status %d: %s", rec.Code, rec.Body.String())
	}
	var response configBackupImportResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("Decode response: %v", err)
	}
	if !slices.Equal(response.StopRequested, []string{"remove"}) || !slices.Equal(response.RestartRequired, []string{"keep"}) {
		t.Fatalf("unexpected runtime response: %#v", response)
	}
	select {
	case key := <-removed:
		if key != "remove" {
			t.Fatalf("removed key = %q", key)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for asynchronous profile removal")
	}
}

func TestConfigBackupImportRechecksSessionBeforeSaving(t *testing.T) {
	s := newServerTestServer(t)
	s.localAuth = localauth.NewStore(t.TempDir())
	if err := s.localAuth.Setup(t.Context(), "admin", "correct password"); err != nil {
		t.Fatal(err)
	}
	token, err := s.localAuth.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.cfgMgr.Get()
	backup := encodeConfigBackup(t, cfg, []configbackup.Section{configbackup.SectionApplication}, false, "")
	req := multipartBackupRequest(t, "/api/config-backup/import", backup, "", []configbackup.Section{configbackup.SectionApplication})
	req.AddCookie(&http.Cookie{Name: localAuthCookieName, Value: token})
	req.Body = &callbackReadCloser{
		ReadCloser: req.Body,
		callback: func() {
			if err := s.localAuth.DeleteSession(context.Background(), token); err != nil {
				t.Errorf("DeleteSession: %v", err)
			}
		},
	}
	saved := false
	s.backupHooks.saveConfig = func(config.Config) error { saved = true; return nil }
	rec := httptest.NewRecorder()
	s.localAuthMiddleware(http.HandlerFunc(s.handleConfigBackupImport)).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("import status = %d, want %d: %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
	if saved {
		t.Fatal("configuration was saved after the request session was revoked")
	}
}

func encodeConfigBackup(t *testing.T, cfg config.Config, sections []configbackup.Section, sensitive bool, password string) []byte {
	t.Helper()
	payload, err := configbackup.Build(cfg, configbackup.ExportOptions{Sections: sections, IncludeSensitive: sensitive}, "test", time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Build backup: %v", err)
	}
	data, err := configbackup.Encode(payload, password, bytes.NewReader(bytes.Repeat([]byte{7}, 64)))
	if err != nil {
		t.Fatalf("Encode backup: %v", err)
	}
	return data
}

func multipartBackupRequest(t *testing.T, target string, file []byte, password string, sections []configbackup.Section) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	filePart, err := writer.CreateFormFile("file", "backup.json")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := filePart.Write(file); err != nil {
		t.Fatalf("Write file: %v", err)
	}
	if password != "" {
		if err := writer.WriteField("password", password); err != nil {
			t.Fatalf("Write password: %v", err)
		}
	}
	if sections != nil {
		encoded, err := json.Marshal(sections)
		if err != nil {
			t.Fatalf("Marshal sections: %v", err)
		}
		if err := writer.WriteField("sections", string(encoded)); err != nil {
			t.Fatalf("Write sections: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close multipart: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, target, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

type callbackReadCloser struct {
	io.ReadCloser
	callback func()
	called   bool
}

func (r *callbackReadCloser) Read(p []byte) (int, error) {
	if !r.called {
		r.called = true
		r.callback()
	}
	return r.ReadCloser.Read(p)
}

func assertBackupErrorCode(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d: %s", rec.Code, status, rec.Body.String())
	}
	var response configBackupErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("Decode error response: %v", err)
	}
	if response.Code != code {
		t.Fatalf("error code = %q, want %q", response.Code, code)
	}
}

func dirEntryNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	return names
}
