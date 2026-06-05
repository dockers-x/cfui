package s3dav

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

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

type captureCloudflareClient struct {
	listAccount string
}

func (f *captureCloudflareClient) ListR2Buckets(_ context.Context, rc *cloudflare.ResourceContainer, _ cloudflare.ListR2BucketsParams) ([]cloudflare.R2Bucket, error) {
	f.listAccount = rc.Identifier
	return []cloudflare.R2Bucket{{Name: "bucket"}}, nil
}

func (f *captureCloudflareClient) CreateR2Bucket(context.Context, *cloudflare.ResourceContainer, cloudflare.CreateR2BucketParameters) (cloudflare.R2Bucket, error) {
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

func newSyncTestService(t *testing.T, filesystems map[string]afero.Fs) *Service {
	t.Helper()
	cfgMgr, err := config.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	cfg := cfgMgr.Get()
	cfg.S3WebDAV = config.S3WebDAVConfig{
		Enabled:   true,
		ActiveKey: "source",
		Mounts: []config.S3WebDAVMountConfig{
			syncTestMount("source", "Source", "source-bucket", true),
			syncTestMount("target", "Target", "target-bucket", true),
		},
	}
	if err := cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}
	return NewServiceForTest(
		cfgMgr,
		func(string) (CloudflareClient, error) { return fakeCloudflareClient{}, nil },
		func(_ context.Context, cfg FSConfig, _ Credentials) (afero.Fs, error) {
			fs, ok := filesystems[cfg.BucketName]
			if !ok {
				return nil, errors.New("missing test filesystem for " + cfg.BucketName)
			}
			return fs, nil
		},
	)
}

func syncTestMount(key, name, bucket string, enabled bool) config.S3WebDAVMountConfig {
	return config.S3WebDAVMountConfig{
		Key:               key,
		Name:              name,
		Enabled:           enabled,
		WebDAVEnabled:     true,
		WebDAVAuthEnabled: false,
		Provider:          ProviderGenericS3,
		EndpointURL:       "https://s3.example.com",
		Region:            "us-east-1",
		PathStyle:         true,
		BucketName:        bucket,
		MountPath:         "/webdav/" + key + "/",
		AccessKeyID:       "ak-" + key,
		SecretAccessKey:   "sk-" + key,
	}
}

func validMountRequest() MountRequest {
	return MountRequest{
		Key:               "my-s3",
		Name:              "My S3",
		Enabled:           boolPtr(true),
		WebDAVEnabled:     boolPtr(true),
		WebDAVAuthEnabled: boolPtr(true),
		Provider:          ProviderGenericS3,
		EndpointURL:       "https://s3.example.com",
		Region:            "us-east-1",
		PathStyle:         true,
		BucketName:        "bucket",
		RootPrefix:        "backups/cfui",
		MountPath:         "/webdav/my_s3/",
		AccessKeyID:       "access-key",
		SecretAccessKey:   "secret-key",
		WebDAVUsername:    "dav",
		WebDAVPassword:    "secret",
	}
}

func boolPtr(v bool) *bool {
	return &v
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

func TestSaveMountPreservesPathStyleFalse(t *testing.T) {
	svc := newTestService(t, fakeCloudflareClient{}, afero.NewMemMapFs())
	req := validMountRequest()
	req.Provider = ProviderCloudflareR2
	req.AccountID = "account"
	req.EndpointURL = "https://account.r2.cloudflarestorage.com"
	req.PathStyle = false

	resp, err := svc.SaveMount(context.Background(), "default", req)
	if err != nil {
		t.Fatalf("SaveMount: %v", err)
	}
	if resp.Mounts[0].PathStyle {
		t.Fatalf("expected response to preserve path_style=false: %#v", resp.Mounts[0])
	}
	if got := svc.cfgMgr.Get().S3WebDAV.Mounts[0]; got.PathStyle {
		t.Fatalf("expected stored config to preserve path_style=false: %#v", got)
	}
}

func TestSaveSettingsPreservesDedicatedAutoStart(t *testing.T) {
	svc := newTestService(t, fakeCloudflareClient{}, afero.NewMemMapFs())
	autoStart := true
	port := 15432
	customDomain := "https://dav.example.com/base/"
	tunnelHostname := "https://dav.example.com/webdav/"

	resp, err := svc.SaveSettings(context.Background(), SettingsRequest{
		WebDAVAccessMode:        "dedicated",
		DedicatedPort:           &port,
		DedicatedAutoStart:      &autoStart,
		DedicatedDomainMode:     "tunnel",
		DedicatedCustomDomain:   &customDomain,
		DedicatedTunnelHostname: &tunnelHostname,
	})
	if err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	if resp.WebDAVAccessMode != "dedicated" || !resp.DedicatedAutoStart || resp.DedicatedDomainMode != "tunnel" || resp.DedicatedCustomDomain != "https://dav.example.com/base" || resp.DedicatedTunnelHostname != "dav.example.com" {
		t.Fatalf("expected dedicated settings to be saved, got %#v", resp)
	}
	if got := svc.cfgMgr.Get().S3WebDAV; got.WebDAVAccessMode != "dedicated" || !got.DedicatedAutoStart || got.DedicatedDomainMode != "tunnel" || got.DedicatedCustomDomain != "https://dav.example.com/base" || got.DedicatedTunnelHostname != "dav.example.com" {
		t.Fatalf("expected stored dedicated settings, got %#v", got)
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

func TestAvailabilityRequiresS3Config(t *testing.T) {
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
	if got := svc.Availability(context.Background(), mount); !got.CanEnable || got.Status != StatusReady {
		t.Fatalf("expected ready status, got %#v", got)
	}
}

func TestWebDAVAvailabilitySeparatesEndpointAndAuthState(t *testing.T) {
	svc := newTestService(t, fakeCloudflareClient{}, afero.NewMemMapFs())
	mount := config.S3WebDAVMountConfig{
		Enabled:           true,
		WebDAVEnabled:     true,
		WebDAVAuthEnabled: true,
		EndpointURL:       "https://s3.example.com",
		BucketName:        "bucket",
		MountPath:         "/webdav/s3/",
		AccessKeyID:       "ak",
		SecretAccessKey:   "sk",
	}

	if got := svc.WebDAVAvailability(context.Background(), mount); got.Status != StatusWebDAVCredentialsRequired {
		t.Fatalf("expected WebDAV credentials status, got %#v", got)
	}
	mount.WebDAVAuthEnabled = false
	if got := svc.WebDAVAvailability(context.Background(), mount); !got.CanEnable || got.Status != StatusWebDAVAuthDisabled {
		t.Fatalf("expected no-auth WebDAV ready status, got %#v", got)
	}
	mount.WebDAVEnabled = false
	if got := svc.WebDAVAvailability(context.Background(), mount); got.Status != StatusWebDAVDisabled {
		t.Fatalf("expected WebDAV disabled status, got %#v", got)
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

func TestR2BucketListCanOverrideSavedMountAccountID(t *testing.T) {
	client := &captureCloudflareClient{}
	svc := newTestService(t, client, afero.NewMemMapFs())
	cfg := svc.cfgMgr.Get()
	cfg.TunnelManagement.APIToken = "api-token"
	cfg.S3WebDAV.Mounts[0].Provider = ProviderCloudflareR2
	cfg.S3WebDAV.Mounts[0].AccountID = "old-account"
	if err := svc.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	if _, err := svc.ListBucketsFor(context.Background(), BucketRequest{
		MountKey:  "default",
		AccountID: "new-account",
	}); err != nil {
		t.Fatalf("ListBucketsFor: %v", err)
	}
	if client.listAccount != "new-account" {
		t.Fatalf("expected explicit account id to win, got %q", client.listAccount)
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

func TestTestWebDAVConnectionChecksWebDAVReadiness(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "/readme.txt", []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	svc := newTestService(t, fakeCloudflareClient{}, fs)

	resp, err := svc.TestWebDAVConnection(context.Background(), "default", validMountRequest())
	if err != nil {
		t.Fatalf("TestWebDAVConnection: %v", err)
	}
	if !resp.Success || resp.Availability.Status != StatusReady {
		t.Fatalf("unexpected WebDAV test response: %#v", resp)
	}

	req := validMountRequest()
	req.WebDAVPassword = ""
	resp, err = svc.TestWebDAVConnection(context.Background(), "default", req)
	if err != nil {
		t.Fatalf("TestWebDAVConnection missing password: %v", err)
	}
	if resp.Success || resp.Availability.Status != StatusWebDAVCredentialsRequired {
		t.Fatalf("expected missing WebDAV credentials, got %#v", resp)
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

func TestSyncCopiesSingleFileToTargetMount(t *testing.T) {
	source := afero.NewMemMapFs()
	target := afero.NewMemMapFs()
	if err := source.MkdirAll("/docs", 0755); err != nil {
		t.Fatalf("MkdirAll source: %v", err)
	}
	if err := afero.WriteFile(source, "/docs/readme.txt", []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}
	svc := newSyncTestService(t, map[string]afero.Fs{
		"source-bucket": source,
		"target-bucket": target,
	})

	resp, err := svc.Sync(context.Background(), SyncRequest{
		SourceMountKey:  "source",
		TargetMountKeys: []string{"target"},
		SourcePath:      "/docs/readme.txt",
		DestinationPath: "/backup/readme.txt",
	})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if resp.Copied != 1 || resp.Skipped != 0 || resp.Failed != 0 {
		t.Fatalf("unexpected sync counts: %#v", resp)
	}
	got, err := afero.ReadFile(target, "/backup/readme.txt")
	if err != nil {
		t.Fatalf("ReadFile target: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("unexpected target content %q", string(got))
	}
}

func TestSyncSkipsExistingTargetFileByDefault(t *testing.T) {
	source := afero.NewMemMapFs()
	target := afero.NewMemMapFs()
	if err := source.MkdirAll("/docs", 0755); err != nil {
		t.Fatalf("MkdirAll source: %v", err)
	}
	if err := target.MkdirAll("/backup", 0755); err != nil {
		t.Fatalf("MkdirAll target: %v", err)
	}
	if err := afero.WriteFile(source, "/docs/readme.txt", []byte("new"), 0644); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}
	if err := afero.WriteFile(target, "/backup/readme.txt", []byte("old"), 0644); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	svc := newSyncTestService(t, map[string]afero.Fs{
		"source-bucket": source,
		"target-bucket": target,
	})

	resp, err := svc.Sync(context.Background(), SyncRequest{
		SourceMountKey:  "source",
		TargetMountKeys: []string{"target"},
		SourcePath:      "/docs/readme.txt",
		DestinationPath: "/backup/readme.txt",
	})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if resp.Copied != 0 || resp.Skipped != 1 || resp.Failed != 0 {
		t.Fatalf("unexpected sync counts: %#v", resp)
	}
	got, err := afero.ReadFile(target, "/backup/readme.txt")
	if err != nil {
		t.Fatalf("ReadFile target: %v", err)
	}
	if string(got) != "old" {
		t.Fatalf("expected target content to be preserved, got %q", string(got))
	}
}

func TestSyncOverwritesExistingTargetFileWhenRequested(t *testing.T) {
	source := afero.NewMemMapFs()
	target := afero.NewMemMapFs()
	if err := source.MkdirAll("/docs", 0755); err != nil {
		t.Fatalf("MkdirAll source: %v", err)
	}
	if err := target.MkdirAll("/backup", 0755); err != nil {
		t.Fatalf("MkdirAll target: %v", err)
	}
	if err := afero.WriteFile(source, "/docs/readme.txt", []byte("new"), 0644); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}
	if err := afero.WriteFile(target, "/backup/readme.txt", []byte("old"), 0644); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	svc := newSyncTestService(t, map[string]afero.Fs{
		"source-bucket": source,
		"target-bucket": target,
	})

	resp, err := svc.Sync(context.Background(), SyncRequest{
		SourceMountKey:  "source",
		TargetMountKeys: []string{"target"},
		SourcePath:      "/docs/readme.txt",
		DestinationPath: "/backup/readme.txt",
		Overwrite:       true,
	})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if resp.Copied != 1 || resp.Skipped != 0 || resp.Failed != 0 {
		t.Fatalf("unexpected sync counts: %#v", resp)
	}
	got, err := afero.ReadFile(target, "/backup/readme.txt")
	if err != nil {
		t.Fatalf("ReadFile target: %v", err)
	}
	if string(got) != "new" {
		t.Fatalf("expected target content to be overwritten, got %q", string(got))
	}
}

func TestSyncRecursivelyCopiesDirectory(t *testing.T) {
	source := afero.NewMemMapFs()
	target := afero.NewMemMapFs()
	if err := source.MkdirAll("/docs/nested", 0755); err != nil {
		t.Fatalf("MkdirAll source: %v", err)
	}
	if err := source.MkdirAll("/docs/empty", 0755); err != nil {
		t.Fatalf("MkdirAll empty source: %v", err)
	}
	if err := afero.WriteFile(source, "/docs/readme.txt", []byte("readme"), 0644); err != nil {
		t.Fatalf("WriteFile readme: %v", err)
	}
	if err := afero.WriteFile(source, "/docs/nested/config.json", []byte("{}"), 0644); err != nil {
		t.Fatalf("WriteFile nested: %v", err)
	}
	svc := newSyncTestService(t, map[string]afero.Fs{
		"source-bucket": source,
		"target-bucket": target,
	})

	resp, err := svc.Sync(context.Background(), SyncRequest{
		SourceMountKey:  "source",
		TargetMountKeys: []string{"target"},
		SourcePath:      "/docs",
		DestinationPath: "/mirror/docs",
	})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if resp.Copied != 2 || resp.Skipped != 0 || resp.Failed != 0 {
		t.Fatalf("unexpected sync counts: %#v", resp)
	}
	assertFileContent(t, target, "/mirror/docs/readme.txt", "readme")
	assertFileContent(t, target, "/mirror/docs/nested/config.json", "{}")
	if info, err := target.Stat("/mirror/docs/empty"); err != nil || !info.IsDir() {
		t.Fatalf("expected empty directory to be created, info=%#v err=%v", info, err)
	}
}

func TestSyncRootCopiesVisibleTree(t *testing.T) {
	source := afero.NewMemMapFs()
	target := afero.NewMemMapFs()
	if err := source.MkdirAll("/nested/deep", 0755); err != nil {
		t.Fatalf("MkdirAll source: %v", err)
	}
	if err := afero.WriteFile(source, "/root.txt", []byte("root"), 0644); err != nil {
		t.Fatalf("WriteFile root: %v", err)
	}
	if err := afero.WriteFile(source, "/nested/deep/db.sql", []byte("sql"), 0644); err != nil {
		t.Fatalf("WriteFile nested: %v", err)
	}
	svc := newSyncTestService(t, map[string]afero.Fs{
		"source-bucket": source,
		"target-bucket": target,
	})

	resp, err := svc.Sync(context.Background(), SyncRequest{
		SourceMountKey:  "source",
		TargetMountKeys: []string{"target"},
		SourcePath:      "/",
		DestinationPath: "/",
	})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if resp.Copied != 2 || resp.Skipped != 0 || resp.Failed != 0 {
		t.Fatalf("unexpected sync counts: %#v", resp)
	}
	assertFileContent(t, target, "/root.txt", "root")
	assertFileContent(t, target, "/nested/deep/db.sql", "sql")
}

func TestSyncRejectsSourceMountAsTarget(t *testing.T) {
	source := afero.NewMemMapFs()
	if err := afero.WriteFile(source, "/readme.txt", []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}
	svc := newSyncTestService(t, map[string]afero.Fs{
		"source-bucket": source,
		"target-bucket": afero.NewMemMapFs(),
	})

	_, err := svc.Sync(context.Background(), SyncRequest{
		SourceMountKey:  "source",
		TargetMountKeys: []string{"source"},
		SourcePath:      "/readme.txt",
		DestinationPath: "/readme.txt",
	})
	if err == nil || !strings.Contains(err.Error(), "cannot sync to itself") {
		t.Fatalf("expected same-mount target to be rejected, got %v", err)
	}
}

func TestStartSyncTracksJobProgressAndCompletion(t *testing.T) {
	source := afero.NewMemMapFs()
	target := afero.NewMemMapFs()
	if err := source.MkdirAll("/docs", 0755); err != nil {
		t.Fatalf("MkdirAll source: %v", err)
	}
	if err := afero.WriteFile(source, "/docs/readme.txt", []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}
	svc := newSyncTestService(t, map[string]afero.Fs{
		"source-bucket": source,
		"target-bucket": target,
	})

	started, err := svc.StartSync(context.Background(), SyncRequest{
		SourceMountKey:  "source",
		TargetMountKeys: []string{"target"},
		SourcePath:      "/docs/readme.txt",
		DestinationPath: "/backup/readme.txt",
	})
	if err != nil {
		t.Fatalf("StartSync: %v", err)
	}
	if started.JobID == "" || started.Status != SyncJobRunning {
		t.Fatalf("unexpected initial job: %#v", started)
	}

	done := waitForSyncJob(t, svc, started.JobID)
	if done.Status != SyncJobCompleted || done.Total != 1 || done.Processed != 1 || done.Copied != 1 || done.Skipped != 0 || done.Failed != 0 {
		t.Fatalf("unexpected completed job: %#v", done)
	}
	if done.BytesTotal != 5 || done.BytesCopied != 5 {
		t.Fatalf("expected byte progress, got %#v", done)
	}
	assertFileContent(t, target, "/backup/readme.txt", "hello")
}

func TestSyncReportsCurrentCopyProgress(t *testing.T) {
	source := afero.NewMemMapFs()
	target := afero.NewMemMapFs()
	if err := source.MkdirAll("/docs", 0755); err != nil {
		t.Fatalf("MkdirAll source: %v", err)
	}
	if err := afero.WriteFile(source, "/docs/readme.txt", []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}
	svc := newSyncTestService(t, map[string]afero.Fs{
		"source-bucket": source,
		"target-bucket": target,
	})

	var updates []syncProgressUpdate
	resp, err := svc.runSync(context.Background(), SyncRequest{
		SourceMountKey:  "source",
		TargetMountKeys: []string{"target"},
		SourcePath:      "/docs/readme.txt",
		DestinationPath: "/backup/readme.txt",
	}, func(update syncProgressUpdate) {
		updates = append(updates, update)
	})
	if err != nil {
		t.Fatalf("runSync: %v", err)
	}
	if resp.Copied != 1 || resp.BytesCopied != 5 || resp.BytesTotal != 5 {
		t.Fatalf("unexpected sync response: %#v", resp)
	}

	var sawCurrent bool
	for _, update := range updates {
		if update.CurrentMountKey != "target" ||
			update.CurrentSourcePath != "/docs/readme.txt" ||
			update.CurrentDestinationPath != "/backup/readme.txt" {
			continue
		}
		if update.CurrentSize != 5 {
			t.Fatalf("expected current size 5, got %#v", update)
		}
		if update.CurrentBytes > 0 {
			sawCurrent = true
			break
		}
	}
	if !sawCurrent {
		t.Fatalf("expected current copy progress update, got %#v", updates)
	}
}

func waitForSyncJob(t *testing.T, svc *Service, id string) SyncJobResponse {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		job, err := svc.SyncJob(id)
		if err != nil {
			t.Fatalf("SyncJob: %v", err)
		}
		if job.Status == SyncJobCompleted || job.Status == SyncJobFailed {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	job, err := svc.SyncJob(id)
	if err != nil {
		t.Fatalf("SyncJob after timeout: %v", err)
	}
	t.Fatalf("sync job did not finish: %#v", job)
	return SyncJobResponse{}
}

func assertFileContent(t *testing.T, fs afero.Fs, name, want string) {
	t.Helper()
	file, err := fs.Open(name)
	if err != nil {
		t.Fatalf("Open %s: %v", name, err)
	}
	defer file.Close()
	got, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("ReadAll %s: %v", name, err)
	}
	if string(got) != want {
		t.Fatalf("unexpected content for %s: got %q want %q", name, string(got), want)
	}
}
