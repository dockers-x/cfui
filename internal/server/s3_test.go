package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cfui/internal/config"
	"cfui/internal/s3dav"

	cloudflare "github.com/cloudflare/cloudflare-go"
	"github.com/spf13/afero"
)

type serverFakeR2Client struct{}

func (serverFakeR2Client) ListR2Buckets(context.Context, *cloudflare.ResourceContainer, cloudflare.ListR2BucketsParams) ([]cloudflare.R2Bucket, error) {
	return []cloudflare.R2Bucket{{Name: "bucket"}}, nil
}

func (serverFakeR2Client) CreateR2Bucket(context.Context, *cloudflare.ResourceContainer, cloudflare.CreateR2BucketParameters) (cloudflare.R2Bucket, error) {
	return cloudflare.R2Bucket{Name: "bucket"}, nil
}

func TestS3FeatureEnableDoesNotRequireCloudflareToken(t *testing.T) {
	s := newServerTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/features", strings.NewReader(`{"s3_webdav":true}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleFeatures(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected ok, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"s3_webdav":true`) {
		t.Fatalf("expected s3_webdav enabled response: %s", rec.Body.String())
	}
}

func TestS3SettingsDoesNotLeakSecrets(t *testing.T) {
	s := newServerTestServer(t)
	req := httptest.NewRequest(http.MethodPut, "/api/s3/mounts/default", strings.NewReader(`{
		"enabled": true,
		"name": "My S3",
		"provider": "generic_s3",
		"endpoint_url": "https://s3.example.com",
		"region": "us-east-1",
		"path_style": true,
		"bucket_name": "bucket",
		"mount_path": "/webdav/my_s3/",
		"access_key_id": "access-key",
		"secret_access_key": "secret-access-key",
		"webdav_username": "dav",
		"webdav_password": "secret"
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleS3Mount(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mount status %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "secret-access-key") || strings.Contains(body, "secret\"") || strings.Contains(body, "password_hash") {
		t.Fatalf("settings response leaked secret material: %s", body)
	}
	var resp s3dav.SettingsResponse
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&resp); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if len(resp.Mounts) != 1 || !resp.Mounts[0].PasswordSet || !resp.Mounts[0].SecretAccessKeySet {
		t.Fatalf("expected secret state response: %#v", resp)
	}
}

func TestS3WebDAVTestEndpointChecksWebDAVConfig(t *testing.T) {
	s := newServerTestServer(t)
	memFS := afero.NewMemMapFs()
	if err := afero.WriteFile(memFS, "/readme.txt", []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	s.s3Svc = s3dav.NewServiceForTest(
		s.cfgMgr,
		func(string) (s3dav.CloudflareClient, error) {
			return serverFakeR2Client{}, nil
		},
		func(context.Context, s3dav.FSConfig, s3dav.Credentials) (afero.Fs, error) {
			return memFS, nil
		},
	)

	req := httptest.NewRequest(http.MethodPost, "/api/s3/webdav-test?mount_key=default", strings.NewReader(`{
		"enabled": true,
		"name": "My S3",
		"provider": "generic_s3",
		"endpoint_url": "https://s3.example.com",
		"region": "us-east-1",
		"path_style": true,
		"bucket_name": "bucket",
		"mount_path": "/webdav/my_s3/",
		"access_key_id": "access-key",
		"secret_access_key": "secret-access-key",
		"webdav_enabled": true,
		"webdav_auth_enabled": true,
		"webdav_username": "dav",
		"webdav_password": "secret"
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleS3WebDAVTest(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("webdav test status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"success":true`) {
		t.Fatalf("expected successful WebDAV test response: %s", rec.Body.String())
	}
}

func TestS3BucketsCanUseTemporaryR2AccountID(t *testing.T) {
	s := newServerTestServer(t)
	cfg := s.cfgMgr.Get()
	cfg.TunnelManagement.APIToken = "api-token"
	if err := s.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}
	s.s3Svc = s3dav.NewServiceForTest(
		s.cfgMgr,
		func(string) (s3dav.CloudflareClient, error) {
			return serverFakeR2Client{}, nil
		},
		nil,
	)

	req := httptest.NewRequest(http.MethodGet, "/api/s3/buckets?account_id=account-r2&jurisdiction=eu", nil)
	rec := httptest.NewRecorder()
	s.handleS3Buckets(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bucket status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"name":"bucket"`) {
		t.Fatalf("expected bucket response: %s", rec.Body.String())
	}
}

func TestS3FileUploadAndListWithFakeFS(t *testing.T) {
	s := newServerTestServer(t)
	cfg := s.cfgMgr.Get()
	cfg.S3WebDAV = config.S3WebDAVConfig{
		Enabled:   true,
		ActiveKey: "default",
		Mounts: []config.S3WebDAVMountConfig{{
			Key:                "default",
			Name:               "Default S3",
			Enabled:            true,
			WebDAVEnabled:      true,
			WebDAVAuthEnabled:  true,
			Provider:           s3dav.ProviderGenericS3,
			EndpointURL:        "https://s3.example.com",
			Region:             "us-east-1",
			PathStyle:          true,
			BucketName:         "bucket",
			MountPath:          "/webdav/s3/",
			AccessKeyID:        "ak",
			SecretAccessKey:    "sk",
			WebDAVUsername:     "dav",
			WebDAVPasswordHash: "$2a$10$hash",
		}},
	}
	if err := s.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}
	memFS := afero.NewMemMapFs()
	s.s3Svc = s3dav.NewServiceForTest(
		s.cfgMgr,
		func(string) (s3dav.CloudflareClient, error) {
			return serverFakeR2Client{}, nil
		},
		func(context.Context, s3dav.FSConfig, s3dav.Credentials) (afero.Fs, error) {
			return memFS, nil
		},
	)

	uploadReq := httptest.NewRequest(http.MethodPut, "/api/s3/files/docs/readme.txt?mount_key=default", bytes.NewBufferString("hello"))
	uploadRec := httptest.NewRecorder()
	s.handleS3FileObject(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload status %d: %s", uploadRec.Code, uploadRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/s3/files?mount_key=default&path=/docs", nil)
	listRec := httptest.NewRecorder()
	s.handleS3Files(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status %d: %s", listRec.Code, listRec.Body.String())
	}
	if !strings.Contains(listRec.Body.String(), "readme.txt") {
		t.Fatalf("expected uploaded file in list response: %s", listRec.Body.String())
	}
}

func TestS3SyncEndpointStartsJobAndReportsCompletion(t *testing.T) {
	s := newServerTestServer(t)
	cfg := s.cfgMgr.Get()
	cfg.S3WebDAV = config.S3WebDAVConfig{
		Enabled:   true,
		ActiveKey: "source",
		Mounts: []config.S3WebDAVMountConfig{
			{
				Key:               "source",
				Name:              "Source S3",
				Enabled:           true,
				WebDAVEnabled:     true,
				WebDAVAuthEnabled: false,
				Provider:          s3dav.ProviderGenericS3,
				EndpointURL:       "https://s3.example.com",
				Region:            "us-east-1",
				PathStyle:         true,
				BucketName:        "source-bucket",
				MountPath:         "/webdav/source/",
				AccessKeyID:       "ak-source",
				SecretAccessKey:   "sk-source",
			},
			{
				Key:               "target",
				Name:              "Target S3",
				Enabled:           true,
				WebDAVEnabled:     true,
				WebDAVAuthEnabled: false,
				Provider:          s3dav.ProviderGenericS3,
				EndpointURL:       "https://s3.example.com",
				Region:            "us-east-1",
				PathStyle:         true,
				BucketName:        "target-bucket",
				MountPath:         "/webdav/target/",
				AccessKeyID:       "ak-target",
				SecretAccessKey:   "sk-target",
			},
		},
	}
	if err := s.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}
	sourceFS := afero.NewMemMapFs()
	targetFS := afero.NewMemMapFs()
	if err := sourceFS.MkdirAll("/docs", 0755); err != nil {
		t.Fatalf("MkdirAll source: %v", err)
	}
	if err := afero.WriteFile(sourceFS, "/docs/readme.txt", []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}
	filesystems := map[string]afero.Fs{
		"source-bucket": sourceFS,
		"target-bucket": targetFS,
	}
	s.s3Svc = s3dav.NewServiceForTest(
		s.cfgMgr,
		func(string) (s3dav.CloudflareClient, error) {
			return serverFakeR2Client{}, nil
		},
		func(_ context.Context, cfg s3dav.FSConfig, _ s3dav.Credentials) (afero.Fs, error) {
			return filesystems[cfg.BucketName], nil
		},
	)

	startReq := httptest.NewRequest(http.MethodPost, "/api/s3/files/sync", strings.NewReader(`{
		"source_mount_key": "source",
		"target_mount_keys": ["target"],
		"source_path": "/docs/readme.txt",
		"destination_path": "/backup/readme.txt"
	}`))
	startReq.Header.Set("Content-Type", "application/json")
	startRec := httptest.NewRecorder()
	s.handleS3Sync(startRec, startReq)
	if startRec.Code != http.StatusOK {
		t.Fatalf("sync start status %d: %s", startRec.Code, startRec.Body.String())
	}
	var started s3dav.SyncJobResponse
	if err := json.NewDecoder(startRec.Body).Decode(&started); err != nil {
		t.Fatalf("decode started job: %v", err)
	}
	if started.JobID == "" {
		t.Fatalf("expected job id in response: %#v", started)
	}

	done := waitForServerSyncJob(t, s, started.JobID)
	if done.Status != s3dav.SyncJobCompleted || done.Copied != 1 || done.Total != 1 || done.Processed != 1 {
		t.Fatalf("unexpected sync job completion: %#v", done)
	}
	got, err := afero.ReadFile(targetFS, "/backup/readme.txt")
	if err != nil {
		t.Fatalf("read synced file: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("unexpected synced content %q", string(got))
	}
}

func waitForServerSyncJob(t *testing.T, s *Server, id string) s3dav.SyncJobResponse {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		req := httptest.NewRequest(http.MethodGet, "/api/s3/files/sync/"+id, nil)
		rec := httptest.NewRecorder()
		s.handleS3SyncJob(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("sync job status %d: %s", rec.Code, rec.Body.String())
		}
		var job s3dav.SyncJobResponse
		if err := json.NewDecoder(rec.Body).Decode(&job); err != nil {
			t.Fatalf("decode job: %v", err)
		}
		if job.Status == s3dav.SyncJobCompleted || job.Status == s3dav.SyncJobFailed {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("sync job %s did not finish", id)
	return s3dav.SyncJobResponse{}
}

func TestWebDAVPutThroughServerRouteCreatesNestedObject(t *testing.T) {
	s := newServerTestServer(t)
	hash, err := s3dav.HashPassword("secret")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	cfg := s.cfgMgr.Get()
	cfg.S3WebDAV = config.S3WebDAVConfig{
		Enabled:   true,
		ActiveKey: "datasync",
		Mounts: []config.S3WebDAVMountConfig{{
			Key:                "datasync",
			Name:               "Data Sync",
			Enabled:            true,
			WebDAVEnabled:      true,
			WebDAVAuthEnabled:  true,
			Provider:           s3dav.ProviderGenericS3,
			EndpointURL:        "https://s3.example.com",
			Region:             "us-east-1",
			PathStyle:          true,
			BucketName:         "bucket",
			MountPath:          "/webdav/datasync/",
			AccessKeyID:        "ak",
			SecretAccessKey:    "sk",
			WebDAVUsername:     "dav",
			WebDAVPasswordHash: hash,
		}},
	}
	if err := s.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}
	memFS := afero.NewMemMapFs()
	s.s3Svc = s3dav.NewServiceForTest(
		s.cfgMgr,
		func(string) (s3dav.CloudflareClient, error) {
			return serverFakeR2Client{}, nil
		},
		func(context.Context, s3dav.FSConfig, s3dav.Credentials) (afero.Fs, error) {
			return memFS, nil
		},
	)

	req := httptest.NewRequest(http.MethodPut, "/webdav/datasync/cc-switch-sync/v2/db-v6/default/db.sql", bytes.NewBufferString("hello"))
	req.SetBasicAuth("dav", "secret")
	rec := httptest.NewRecorder()
	s.GetHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusNoContent {
		t.Fatalf("WebDAV PUT status %d: %s", rec.Code, rec.Body.String())
	}
	got, err := afero.ReadFile(memFS, "/cc-switch-sync/v2/db-v6/default/db.sql")
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("unexpected uploaded content %q", string(got))
	}
}

func TestWebDAVAccessModeDedicatedDisablesMainRoute(t *testing.T) {
	s := newServerTestServer(t)
	cfg := s.cfgMgr.Get()
	cfg.S3WebDAV = config.S3WebDAVConfig{
		Enabled:          true,
		ActiveKey:        "datasync",
		WebDAVAccessMode: config.S3WebDAVAccessModeDedicated,
		DedicatedPort:    14334,
		Mounts: []config.S3WebDAVMountConfig{{
			Key:               "datasync",
			Name:              "Data Sync",
			Enabled:           true,
			WebDAVEnabled:     true,
			WebDAVAuthEnabled: false,
			Provider:          s3dav.ProviderGenericS3,
			EndpointURL:       "https://s3.example.com",
			Region:            "us-east-1",
			PathStyle:         true,
			BucketName:        "bucket",
			MountPath:         "/webdav/datasync/",
			AccessKeyID:       "ak",
			SecretAccessKey:   "sk",
		}},
	}
	if err := s.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}
	memFS := afero.NewMemMapFs()
	s.s3Svc = s3dav.NewServiceForTest(
		s.cfgMgr,
		func(string) (s3dav.CloudflareClient, error) {
			return serverFakeR2Client{}, nil
		},
		func(context.Context, s3dav.FSConfig, s3dav.Credentials) (afero.Fs, error) {
			return memFS, nil
		},
	)

	mainReq := httptest.NewRequest(http.MethodPut, "/webdav/datasync/db.sql", bytes.NewBufferString("main"))
	mainRec := httptest.NewRecorder()
	s.GetHandler().ServeHTTP(mainRec, mainReq)
	if mainRec.Code != http.StatusNotFound {
		t.Fatalf("expected main WebDAV route disabled, got %d: %s", mainRec.Code, mainRec.Body.String())
	}

	dedicatedReq := httptest.NewRequest(http.MethodPut, "/webdav/datasync/db.sql", bytes.NewBufferString("dedicated"))
	dedicatedRec := httptest.NewRecorder()
	s.dedicatedWebDAVHandler().ServeHTTP(dedicatedRec, dedicatedReq)
	if dedicatedRec.Code != http.StatusCreated && dedicatedRec.Code != http.StatusNoContent {
		t.Fatalf("dedicated WebDAV PUT status %d: %s", dedicatedRec.Code, dedicatedRec.Body.String())
	}
	got, err := afero.ReadFile(memFS, "/db.sql")
	if err != nil {
		t.Fatalf("read dedicated uploaded file: %v", err)
	}
	if string(got) != "dedicated" {
		t.Fatalf("unexpected dedicated uploaded content %q", string(got))
	}
}

func TestDedicatedWebDAVServerStartsOnConfiguredPort(t *testing.T) {
	s := newServerTestServer(t)
	port := freeLocalPort(t)
	cfg := s.cfgMgr.Get()
	cfg.S3WebDAV = config.S3WebDAVConfig{
		Enabled:            true,
		ActiveKey:          "datasync",
		WebDAVAccessMode:   config.S3WebDAVAccessModeDedicated,
		DedicatedBindHost:  "127.0.0.1",
		DedicatedPort:      port,
		DedicatedAutoStart: true,
		Mounts: []config.S3WebDAVMountConfig{{
			Key:               "datasync",
			Name:              "Data Sync",
			Enabled:           true,
			WebDAVEnabled:     true,
			WebDAVAuthEnabled: false,
			Provider:          s3dav.ProviderGenericS3,
			EndpointURL:       "https://s3.example.com",
			Region:            "us-east-1",
			PathStyle:         true,
			BucketName:        "bucket",
			MountPath:         "/webdav/datasync/",
			AccessKeyID:       "ak",
			SecretAccessKey:   "sk",
		}},
	}
	if err := s.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}
	memFS := afero.NewMemMapFs()
	s.s3Svc = s3dav.NewServiceForTest(
		s.cfgMgr,
		func(string) (s3dav.CloudflareClient, error) {
			return serverFakeR2Client{}, nil
		},
		func(context.Context, s3dav.FSConfig, s3dav.Credentials) (afero.Fs, error) {
			return memFS, nil
		},
	)

	s.StartS3WebDAV()
	defer s.StopS3WebDAV(context.Background())
	settings := s.decorateS3SettingsResponse(s.s3Svc.Settings(context.Background()))
	if !settings.DedicatedRunning || settings.DedicatedError != "" {
		t.Fatalf("expected dedicated WebDAV running, got %#v", settings)
	}

	req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("http://127.0.0.1:%d/webdav/datasync/live.txt", port), bytes.NewBufferString("live"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("dedicated WebDAV PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("dedicated WebDAV PUT status %d", resp.StatusCode)
	}
	got, err := afero.ReadFile(memFS, "/live.txt")
	if err != nil {
		t.Fatalf("read dedicated uploaded file: %v", err)
	}
	if string(got) != "live" {
		t.Fatalf("unexpected dedicated uploaded content %q", string(got))
	}
}

func TestSwitchingWebDAVAccessModeStopsDedicatedServerAndRestoresMainRoute(t *testing.T) {
	s := newServerTestServer(t)
	port := freeLocalPort(t)
	cfg := s.cfgMgr.Get()
	cfg.S3WebDAV = config.S3WebDAVConfig{
		Enabled:            true,
		ActiveKey:          "datasync",
		WebDAVAccessMode:   config.S3WebDAVAccessModeDedicated,
		DedicatedBindHost:  "127.0.0.1",
		DedicatedPort:      port,
		DedicatedAutoStart: true,
		Mounts: []config.S3WebDAVMountConfig{{
			Key:               "datasync",
			Name:              "Data Sync",
			Enabled:           true,
			WebDAVEnabled:     true,
			WebDAVAuthEnabled: false,
			Provider:          s3dav.ProviderGenericS3,
			EndpointURL:       "https://s3.example.com",
			Region:            "us-east-1",
			PathStyle:         true,
			BucketName:        "bucket",
			MountPath:         "/webdav/datasync/",
			AccessKeyID:       "ak",
			SecretAccessKey:   "sk",
		}},
	}
	if err := s.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}
	memFS := afero.NewMemMapFs()
	s.s3Svc = s3dav.NewServiceForTest(
		s.cfgMgr,
		func(string) (s3dav.CloudflareClient, error) {
			return serverFakeR2Client{}, nil
		},
		func(context.Context, s3dav.FSConfig, s3dav.Credentials) (afero.Fs, error) {
			return memFS, nil
		},
	)

	s.StartS3WebDAV()
	defer s.StopS3WebDAV(context.Background())
	if running, _, errMsg := s.s3WebDAV.snapshot(); !running || errMsg != "" {
		t.Fatalf("expected dedicated WebDAV running before switch, running=%v err=%q", running, errMsg)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/s3/settings", strings.NewReader(fmt.Sprintf(`{
		"enabled": true,
		"active_key": "datasync",
		"webdav_access_mode": "main",
		"dedicated_bind_host": "127.0.0.1",
		"dedicated_port": %d
	}`, port)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleS3Settings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings status %d: %s", rec.Code, rec.Body.String())
	}
	var settings s3dav.SettingsResponse
	if err := json.NewDecoder(rec.Body).Decode(&settings); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if settings.WebDAVAccessMode != config.S3WebDAVAccessModeMain || settings.DedicatedRunning {
		t.Fatalf("expected main mode and stopped dedicated server, got %#v", settings)
	}

	dedicatedReq := httptest.NewRequest(http.MethodPut, "/webdav/datasync/dedicated.txt", bytes.NewBufferString("dedicated"))
	dedicatedRec := httptest.NewRecorder()
	s.dedicatedWebDAVHandler().ServeHTTP(dedicatedRec, dedicatedReq)
	if dedicatedRec.Code != http.StatusNotFound {
		t.Fatalf("expected dedicated handler disabled after switch, got %d: %s", dedicatedRec.Code, dedicatedRec.Body.String())
	}

	mainReq := httptest.NewRequest(http.MethodPut, "/webdav/datasync/main.txt", bytes.NewBufferString("main"))
	mainRec := httptest.NewRecorder()
	s.GetHandler().ServeHTTP(mainRec, mainReq)
	if mainRec.Code != http.StatusCreated && mainRec.Code != http.StatusNoContent {
		t.Fatalf("main WebDAV PUT status %d: %s", mainRec.Code, mainRec.Body.String())
	}
	got, err := afero.ReadFile(memFS, "/main.txt")
	if err != nil {
		t.Fatalf("read main uploaded file: %v", err)
	}
	if string(got) != "main" {
		t.Fatalf("unexpected main uploaded content %q", string(got))
	}
}

func TestDedicatedWebDAVServerSkipsStartupWhenAutoStartDisabled(t *testing.T) {
	s := newServerTestServer(t)
	port := freeLocalPort(t)
	cfg := s.cfgMgr.Get()
	cfg.S3WebDAV = config.S3WebDAVConfig{
		Enabled:            true,
		ActiveKey:          "datasync",
		WebDAVAccessMode:   config.S3WebDAVAccessModeDedicated,
		DedicatedBindHost:  "127.0.0.1",
		DedicatedPort:      port,
		DedicatedAutoStart: false,
		Mounts: []config.S3WebDAVMountConfig{{
			Key:               "datasync",
			Name:              "Data Sync",
			Enabled:           true,
			WebDAVEnabled:     true,
			WebDAVAuthEnabled: false,
			Provider:          s3dav.ProviderGenericS3,
			EndpointURL:       "https://s3.example.com",
			Region:            "us-east-1",
			PathStyle:         true,
			BucketName:        "bucket",
			MountPath:         "/webdav/datasync/",
			AccessKeyID:       "ak",
			SecretAccessKey:   "sk",
		}},
	}
	if err := s.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	s.StartS3WebDAV()
	defer s.StopS3WebDAV(context.Background())
	if running, _, errMsg := s.s3WebDAV.snapshot(); running || errMsg != "" {
		t.Fatalf("expected dedicated WebDAV to stay stopped, running=%v err=%q", running, errMsg)
	}
	settings := s.decorateS3SettingsResponse(s.s3Svc.Settings(context.Background()))
	if settings.DedicatedRunning || settings.DedicatedAutoStart {
		t.Fatalf("unexpected dedicated WebDAV settings: %#v", settings)
	}
}

func TestDedicatedWebDAVAutoStartDoesNotOverrideMainMode(t *testing.T) {
	s := newServerTestServer(t)
	port := freeLocalPort(t)
	cfg := s.cfgMgr.Get()
	cfg.S3WebDAV = config.S3WebDAVConfig{
		Enabled:            true,
		ActiveKey:          "datasync",
		WebDAVAccessMode:   config.S3WebDAVAccessModeMain,
		DedicatedBindHost:  "127.0.0.1",
		DedicatedPort:      port,
		DedicatedAutoStart: true,
		Mounts: []config.S3WebDAVMountConfig{{
			Key:               "datasync",
			Name:              "Data Sync",
			Enabled:           true,
			WebDAVEnabled:     true,
			WebDAVAuthEnabled: false,
			Provider:          s3dav.ProviderGenericS3,
			EndpointURL:       "https://s3.example.com",
			Region:            "us-east-1",
			PathStyle:         true,
			BucketName:        "bucket",
			MountPath:         "/webdav/datasync/",
			AccessKeyID:       "ak",
			SecretAccessKey:   "sk",
		}},
	}
	if err := s.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	s.StartS3WebDAV()
	defer s.StopS3WebDAV(context.Background())
	if running, _, errMsg := s.s3WebDAV.snapshot(); running || errMsg != "" {
		t.Fatalf("expected main mode to keep dedicated WebDAV stopped, running=%v err=%q", running, errMsg)
	}
	settings := s.decorateS3SettingsResponse(s.s3Svc.Settings(context.Background()))
	if settings.WebDAVAccessMode != config.S3WebDAVAccessModeMain || !settings.DedicatedAutoStart || settings.DedicatedRunning {
		t.Fatalf("expected main mode to win over dedicated auto-start, got %#v", settings)
	}
}

func TestDedicatedWebDAVControlStartsAndStopsServer(t *testing.T) {
	s := newServerTestServer(t)
	port := freeLocalPort(t)
	cfg := s.cfgMgr.Get()
	cfg.S3WebDAV = config.S3WebDAVConfig{
		Enabled:            true,
		ActiveKey:          "datasync",
		WebDAVAccessMode:   config.S3WebDAVAccessModeDedicated,
		DedicatedBindHost:  "127.0.0.1",
		DedicatedPort:      port,
		DedicatedAutoStart: false,
		Mounts: []config.S3WebDAVMountConfig{{
			Key:               "datasync",
			Name:              "Data Sync",
			Enabled:           true,
			WebDAVEnabled:     true,
			WebDAVAuthEnabled: false,
			Provider:          s3dav.ProviderGenericS3,
			EndpointURL:       "https://s3.example.com",
			Region:            "us-east-1",
			PathStyle:         true,
			BucketName:        "bucket",
			MountPath:         "/webdav/datasync/",
			AccessKeyID:       "ak",
			SecretAccessKey:   "sk",
		}},
	}
	if err := s.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}
	memFS := afero.NewMemMapFs()
	s.s3Svc = s3dav.NewServiceForTest(
		s.cfgMgr,
		func(string) (s3dav.CloudflareClient, error) {
			return serverFakeR2Client{}, nil
		},
		func(context.Context, s3dav.FSConfig, s3dav.Credentials) (afero.Fs, error) {
			return memFS, nil
		},
	)
	defer s.StopS3WebDAV(context.Background())

	startReq := httptest.NewRequest(http.MethodPost, "/api/s3/webdav-control", strings.NewReader(`{"action":"start"}`))
	startReq.Header.Set("Content-Type", "application/json")
	startRec := httptest.NewRecorder()
	s.handleS3WebDAVControl(startRec, startReq)
	if startRec.Code != http.StatusOK {
		t.Fatalf("start status %d: %s", startRec.Code, startRec.Body.String())
	}
	var started s3dav.SettingsResponse
	if err := json.NewDecoder(startRec.Body).Decode(&started); err != nil {
		t.Fatalf("decode started settings: %v", err)
	}
	if !started.DedicatedRunning || started.DedicatedAutoStart {
		t.Fatalf("expected manual start without auto-start, got %#v", started)
	}

	req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("http://127.0.0.1:%d/webdav/datasync/live.txt", port), bytes.NewBufferString("live"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("dedicated WebDAV PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("dedicated WebDAV PUT status %d", resp.StatusCode)
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/api/s3/webdav-control", strings.NewReader(`{"action":"stop"}`))
	stopReq.Header.Set("Content-Type", "application/json")
	stopRec := httptest.NewRecorder()
	s.handleS3WebDAVControl(stopRec, stopReq)
	if stopRec.Code != http.StatusOK {
		t.Fatalf("stop status %d: %s", stopRec.Code, stopRec.Body.String())
	}
	var stopped s3dav.SettingsResponse
	if err := json.NewDecoder(stopRec.Body).Decode(&stopped); err != nil {
		t.Fatalf("decode stopped settings: %v", err)
	}
	if stopped.DedicatedRunning || stopped.DedicatedAutoStart {
		t.Fatalf("expected manual stop without changing auto-start, got %#v", stopped)
	}
}

func freeLocalPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on local port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}
