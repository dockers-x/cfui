package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
