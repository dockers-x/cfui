package s3dav

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"cfui/internal/config"

	cloudflare "github.com/cloudflare/cloudflare-go"
	"github.com/spf13/afero"
)

var errTestPermission = errors.New("permission denied")

type fakeCloudflareClient struct {
	listErr error
}

func (f fakeCloudflareClient) ListR2Buckets(context.Context, *cloudflare.ResourceContainer, cloudflare.ListR2BucketsParams) ([]cloudflare.R2Bucket, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return []cloudflare.R2Bucket{{Name: "bucket"}}, nil
}

func (f fakeCloudflareClient) CreateR2Bucket(context.Context, *cloudflare.ResourceContainer, cloudflare.CreateR2BucketParameters) (cloudflare.R2Bucket, error) {
	return cloudflare.R2Bucket{Name: "bucket"}, nil
}

func newTestService(t *testing.T, client CloudflareClient, fs afero.Fs) *Service {
	t.Helper()
	cfgMgr, err := config.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return NewServiceForTest(
		cfgMgr,
		func(string) (CloudflareClient, error) { return client, nil },
		func(context.Context, FSConfig, Credentials) (afero.Fs, error) { return fs, nil },
	)
}

func validMountRequest() MountRequest {
	return MountRequest{
		Key:             "my-s3",
		Name:            "My S3",
		Enabled:         true,
		Provider:        ProviderGenericS3,
		EndpointURL:     "https://s3.example.com",
		Region:          "us-east-1",
		PathStyle:       true,
		BucketName:      "bucket",
		RootPrefix:      "backups/cfui",
		MountPath:       "/webdav/my_s3/",
		AccessKeyID:     "access-key",
		SecretAccessKey: "secret-key",
		WebDAVUsername:  "dav",
		WebDAVPassword:  "secret",
	}
}

func TestSaveMountHashesPasswordAndKeepsExistingHash(t *testing.T) {
	svc := newTestService(t, fakeCloudflareClient{}, afero.NewMemMapFs())

	resp, err := svc.SaveMount(context.Background(), "default", validMountRequest())
	if err != nil {
		t.Fatalf("SaveMount: %v", err)
	}
	mount := resp.Mounts[0]
	if !mount.PasswordSet || !mount.SecretAccessKeySet {
		t.Fatalf("expected secret state in response: %#v", mount)
	}
	hash := svc.cfgMgr.Get().S3WebDAV.Mounts[0].WebDAVPasswordHash
	if hash == "" || strings.Contains(hash, "secret") {
		t.Fatalf("password hash was not stored safely: %q", hash)
	}

	req := validMountRequest()
	req.WebDAVUsername = "dav2"
	req.WebDAVPassword = ""
	if _, err := svc.SaveMount(context.Background(), "default", req); err != nil {
		t.Fatalf("SaveMount keep password: %v", err)
	}
	if got := svc.cfgMgr.Get().S3WebDAV.Mounts[0].WebDAVPasswordHash; got != hash {
		t.Fatalf("expected existing password hash to be preserved")
	}
}

func TestSaveMountPreservesSecretAccessKeyWhenBlank(t *testing.T) {
	svc := newTestService(t, fakeCloudflareClient{}, afero.NewMemMapFs())
	if _, err := svc.SaveMount(context.Background(), "default", validMountRequest()); err != nil {
		t.Fatalf("SaveMount: %v", err)
	}

	req := validMountRequest()
	req.SecretAccessKey = ""
	req.AccessKeyID = "new-access-key"
	if _, err := svc.SaveMount(context.Background(), "default", req); err != nil {
		t.Fatalf("SaveMount preserve secret: %v", err)
	}
	got := svc.cfgMgr.Get().S3WebDAV.Mounts[0]
	if got.AccessKeyID != "new-access-key" || got.SecretAccessKey != "secret-key" {
		t.Fatalf("unexpected stored credentials: %#v", got)
	}
}

func TestCreateMountRejectsDuplicateMountPath(t *testing.T) {
	svc := newTestService(t, fakeCloudflareClient{}, afero.NewMemMapFs())
	req := validMountRequest()
	req.MountPath = "/webdav/s3/"
	if _, err := svc.CreateMount(context.Background(), req); err == nil {
		t.Fatal("expected duplicate mount path to be rejected")
	}
}

func TestFeatureAvailabilityDoesNotDependOnCloudflareToken(t *testing.T) {
	svc := newTestService(t, fakeCloudflareClient{listErr: errTestPermission}, afero.NewMemMapFs())
	availability := svc.FeatureAvailability(context.Background(), svc.cfgMgr.Get().S3WebDAV)
	if !availability.CanEnable || availability.Status != StatusReady {
		t.Fatalf("unexpected feature availability: %#v", availability)
	}
}

func TestAvailabilityRequiresS3AndWebDAVConfig(t *testing.T) {
	svc := newTestService(t, fakeCloudflareClient{}, afero.NewMemMapFs())
	mount := config.S3WebDAVMountConfig{Enabled: true, MountPath: "/webdav/s3/"}
	if got := svc.Availability(context.Background(), mount); got.Status != StatusEndpointRequired {
		t.Fatalf("expected endpoint status, got %#v", got)
	}
	mount.EndpointURL = "https://s3.example.com"
	if got := svc.Availability(context.Background(), mount); got.Status != StatusBucketRequired {
		t.Fatalf("expected bucket status, got %#v", got)
	}
	mount.BucketName = "bucket"
	if got := svc.Availability(context.Background(), mount); got.Status != StatusCredentialsRequired {
		t.Fatalf("expected credentials status, got %#v", got)
	}
	mount.AccessKeyID = "ak"
	mount.SecretAccessKey = "sk"
	if got := svc.Availability(context.Background(), mount); got.Status != StatusWebDAVCredentialsRequired {
		t.Fatalf("expected WebDAV credentials status, got %#v", got)
	}
	mount.WebDAVUsername = "dav"
	mount.WebDAVPasswordHash = "$2a$10$hash"
	if got := svc.Availability(context.Background(), mount); !got.CanEnable || got.Status != StatusReady {
		t.Fatalf("expected ready status, got %#v", got)
	}
}

func TestAvailabilityRejectsInvalidPaths(t *testing.T) {
	svc := newTestService(t, fakeCloudflareClient{}, afero.NewMemMapFs())
	mount := config.S3WebDAVMountConfig{
		Enabled:            true,
		EndpointURL:        "https://s3.example.com",
		BucketName:         "bucket",
		AccessKeyID:        "ak",
		SecretAccessKey:    "sk",
		WebDAVUsername:     "dav",
		WebDAVPasswordHash: "$2a$10$hash",
	}

	mount.MountPath = "/bad/"
	if got := svc.Availability(context.Background(), mount); got.Status != StatusMountPathInvalid {
		t.Fatalf("expected invalid mount path status, got %#v", got)
	}
	mount.MountPath = "/webdav/s3/"
	mount.RootPrefix = "../secret"
	if got := svc.Availability(context.Background(), mount); got.Status != StatusMountPathInvalid {
		t.Fatalf("expected invalid root prefix status, got %#v", got)
	}
}

func TestR2ManagementIsOptionalAndUsesBucketProbe(t *testing.T) {
	svc := newTestService(t, fakeCloudflareClient{}, afero.NewMemMapFs())
	cfg := svc.cfgMgr.Get()
	cfg.TunnelManagement.APIToken = "token"
	cfg.S3WebDAV.Mounts[0].Provider = ProviderCloudflareR2
	cfg.S3WebDAV.Mounts[0].AccountID = "account"
	if err := svc.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	management := svc.Settings(context.Background()).Mounts[0].R2BucketManagement
	if !management.Enabled || management.Status != "READY" {
		t.Fatalf("unexpected management status: %#v", management)
	}

	svc = newTestService(t, fakeCloudflareClient{listErr: errTestPermission}, afero.NewMemMapFs())
	cfg = svc.cfgMgr.Get()
	cfg.TunnelManagement.APIToken = "token"
	cfg.S3WebDAV.Mounts[0].Provider = ProviderCloudflareR2
	cfg.S3WebDAV.Mounts[0].AccountID = "account"
	if err := svc.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}
	management = svc.Settings(context.Background()).Mounts[0].R2BucketManagement
	if management.Enabled || management.Status != "PERMISSION_DENIED" {
		t.Fatalf("unexpected denied management status: %#v", management)
	}
}

func TestR2MountUsesAccountIDFromTunnelToken(t *testing.T) {
	svc := newTestService(t, fakeCloudflareClient{}, afero.NewMemMapFs())
	cfg := svc.cfgMgr.Get()
	cfg.Token = base64.StdEncoding.EncodeToString([]byte(`{"a":"account-from-token","t":"22222222-2222-2222-2222-222222222222","s":"secret"}`))
	cfg.S3WebDAV.Mounts[0].Provider = ProviderCloudflareR2
	if err := svc.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	mount := svc.Settings(context.Background()).Mounts[0]
	if mount.AccountID != "account-from-token" {
		t.Fatalf("expected account id from tunnel token, got %#v", mount)
	}
	if mount.EndpointURL != "https://account-from-token.r2.cloudflarestorage.com" {
		t.Fatalf("expected R2 endpoint from token account id, got %q", mount.EndpointURL)
	}
}

func TestTestConnectionListsRoot(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "/readme.txt", []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	svc := newTestService(t, fakeCloudflareClient{}, fs)

	resp, err := svc.TestConnection(context.Background(), "default", validMountRequest())
	if err != nil {
		t.Fatalf("TestConnection: %v", err)
	}
	if !resp.Success || !resp.Availability.CanEnable {
		t.Fatalf("unexpected test response: %#v", resp)
	}
}

func TestListFilesUsesSelectedAferoFilesystem(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := fs.MkdirAll("/docs", 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := afero.WriteFile(fs, "/docs/readme.txt", []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	svc := newTestService(t, fakeCloudflareClient{}, fs)
	cfg := svc.cfgMgr.Get()
	cfg.S3WebDAV = config.S3WebDAVConfig{
		Enabled:   true,
		ActiveKey: "docs",
		Mounts: []config.S3WebDAVMountConfig{{
			Key:                "docs",
			Name:               "Docs",
			Enabled:            true,
			Provider:           ProviderGenericS3,
			EndpointURL:        "https://s3.example.com",
			Region:             "us-east-1",
			PathStyle:          true,
			BucketName:         "bucket",
			MountPath:          "/webdav/docs/",
			AccessKeyID:        "ak",
			SecretAccessKey:    "sk",
			WebDAVUsername:     "dav",
			WebDAVPasswordHash: "$2a$10$hash",
		}},
	}
	if err := svc.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	resp, err := svc.ListFiles(context.Background(), "docs", "/docs")
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(resp.Entries) != 1 || resp.Entries[0].Name != "readme.txt" {
		t.Fatalf("unexpected entries: %#v", resp.Entries)
	}
}
