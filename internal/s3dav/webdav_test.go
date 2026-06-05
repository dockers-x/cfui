package s3dav

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/spf13/afero"
)

func TestWebDAVHandlerRequiresBasicAuth(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := fs.MkdirAll("/docs", 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := afero.WriteFile(fs, "/docs/readme.txt", []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	svc := newTestService(t, fakeCloudflareClient{}, fs)
	hash, err := HashPassword("secret")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	cfg := svc.cfgMgr.Get()
	cfg.S3WebDAV.Enabled = true
	cfg.S3WebDAV.Mounts[0].EndpointURL = "https://s3.example.com"
	cfg.S3WebDAV.Mounts[0].BucketName = "bucket"
	cfg.S3WebDAV.Mounts[0].MountPath = "/webdav/my_r2/"
	cfg.S3WebDAV.Mounts[0].AccessKeyID = "ak"
	cfg.S3WebDAV.Mounts[0].SecretAccessKey = "sk"
	cfg.S3WebDAV.Mounts[0].WebDAVUsername = "dav"
	cfg.S3WebDAV.Mounts[0].WebDAVPasswordHash = hash
	if err := svc.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	wrongPathReq := httptest.NewRequest(http.MethodGet, "/webdav/s3/docs/readme.txt", nil)
	wrongPathRec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(wrongPathRec, wrongPathReq)
	if wrongPathRec.Code != http.StatusNotFound {
		t.Fatalf("expected not found, got %d", wrongPathRec.Code)
	}

	missingReq := httptest.NewRequest(http.MethodGet, "/webdav/my_r2/docs/readme.txt", nil)
	missingRec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(missingRec, missingReq)
	if missingRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized, got %d", missingRec.Code)
	}

	okReq := httptest.NewRequest(http.MethodGet, "/webdav/my_r2/docs/readme.txt", nil)
	okReq.SetBasicAuth("dav", "secret")
	okRec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusOK {
		t.Fatalf("expected ok, got %d: %s", okRec.Code, okRec.Body.String())
	}
	if okRec.Body.String() != "hello" {
		t.Fatalf("unexpected body %q", okRec.Body.String())
	}
}

func TestWebDAVPutCreatesParentPrefixes(t *testing.T) {
	fs := afero.NewMemMapFs()
	svc := newTestService(t, fakeCloudflareClient{}, fs)
	hash, err := HashPassword("secret")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	cfg := svc.cfgMgr.Get()
	cfg.S3WebDAV.Enabled = true
	cfg.S3WebDAV.Mounts[0].EndpointURL = "https://s3.example.com"
	cfg.S3WebDAV.Mounts[0].BucketName = "bucket"
	cfg.S3WebDAV.Mounts[0].MountPath = "/webdav/datasync/"
	cfg.S3WebDAV.Mounts[0].AccessKeyID = "ak"
	cfg.S3WebDAV.Mounts[0].SecretAccessKey = "sk"
	cfg.S3WebDAV.Mounts[0].WebDAVUsername = "dav"
	cfg.S3WebDAV.Mounts[0].WebDAVPasswordHash = hash
	if err := svc.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/webdav/datasync/cc-switch-sync/v2/db-v6/default/db.sql", bytes.NewBufferString("hello"))
	req.SetBasicAuth("dav", "secret")
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusNoContent {
		t.Fatalf("expected WebDAV PUT to create nested object, got %d: %s", rec.Code, rec.Body.String())
	}
	got, err := afero.ReadFile(fs, "/cc-switch-sync/v2/db-v6/default/db.sql")
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("unexpected uploaded content %q", string(got))
	}
}

func TestWebDAVPutWorksWithS3StyleWriteOnlyFiles(t *testing.T) {
	fs := s3StyleWriteOnlyFS{Fs: afero.NewMemMapFs()}
	svc := newTestService(t, fakeCloudflareClient{}, fs)
	hash, err := HashPassword("secret")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	cfg := svc.cfgMgr.Get()
	cfg.S3WebDAV.Enabled = true
	cfg.S3WebDAV.Mounts[0].EndpointURL = "https://s3.example.com"
	cfg.S3WebDAV.Mounts[0].BucketName = "bucket"
	cfg.S3WebDAV.Mounts[0].MountPath = "/webdav/datasync/"
	cfg.S3WebDAV.Mounts[0].AccessKeyID = "ak"
	cfg.S3WebDAV.Mounts[0].SecretAccessKey = "sk"
	cfg.S3WebDAV.Mounts[0].WebDAVUsername = "dav"
	cfg.S3WebDAV.Mounts[0].WebDAVPasswordHash = hash
	if err := svc.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/webdav/datasync/cc-switch-sync/v2/db-v6/default/db.sql", bytes.NewBufferString("hello"))
	req.SetBasicAuth("dav", "secret")
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusNoContent {
		t.Fatalf("expected WebDAV PUT to create nested object, got %d: %s", rec.Code, rec.Body.String())
	}
	got, err := afero.ReadFile(fs.Fs, "/cc-switch-sync/v2/db-v6/default/db.sql")
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("unexpected uploaded content %q", string(got))
	}
}

func TestWebDAVHandlerAllowsRequestsWhenAuthDisabled(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := fs.MkdirAll("/docs", 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := afero.WriteFile(fs, "/docs/readme.txt", []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	svc := newTestService(t, fakeCloudflareClient{}, fs)
	cfg := svc.cfgMgr.Get()
	cfg.S3WebDAV.Enabled = true
	cfg.S3WebDAV.Mounts[0].EndpointURL = "https://s3.example.com"
	cfg.S3WebDAV.Mounts[0].BucketName = "bucket"
	cfg.S3WebDAV.Mounts[0].MountPath = "/webdav/public/"
	cfg.S3WebDAV.Mounts[0].AccessKeyID = "ak"
	cfg.S3WebDAV.Mounts[0].SecretAccessKey = "sk"
	cfg.S3WebDAV.Mounts[0].WebDAVAuthEnabled = false
	cfg.S3WebDAV.Mounts[0].WebDAVUsername = ""
	cfg.S3WebDAV.Mounts[0].WebDAVPasswordHash = ""
	if err := svc.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/webdav/public/docs/readme.txt", nil)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected ok without Basic Auth, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWebDAVHandlerReturnsNotFoundWhenEndpointDisabled(t *testing.T) {
	fs := afero.NewMemMapFs()
	svc := newTestService(t, fakeCloudflareClient{}, fs)
	cfg := svc.cfgMgr.Get()
	cfg.S3WebDAV.Enabled = true
	cfg.S3WebDAV.Mounts[0].EndpointURL = "https://s3.example.com"
	cfg.S3WebDAV.Mounts[0].BucketName = "bucket"
	cfg.S3WebDAV.Mounts[0].MountPath = "/webdav/private/"
	cfg.S3WebDAV.Mounts[0].AccessKeyID = "ak"
	cfg.S3WebDAV.Mounts[0].SecretAccessKey = "sk"
	cfg.S3WebDAV.Mounts[0].WebDAVEnabled = false
	cfg.S3WebDAV.Mounts[0].WebDAVAuthEnabled = false
	if err := svc.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/webdav/private/readme.txt", nil)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected not found, got %d: %s", rec.Code, rec.Body.String())
	}
}

var errS3StyleUnsupported = errors.New("s3 does not support read/write open")

type s3StyleWriteOnlyFS struct {
	afero.Fs
}

func (fs s3StyleWriteOnlyFS) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if flag&os.O_RDWR != 0 {
		return nil, errS3StyleUnsupported
	}
	file, err := fs.Fs.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	if flag&os.O_WRONLY != 0 {
		return &s3StyleWriteOnlyFile{File: file, name: name}, nil
	}
	return file, nil
}

type s3StyleWriteOnlyFile struct {
	afero.File
	name   string
	closed bool
}

func (f *s3StyleWriteOnlyFile) Stat() (os.FileInfo, error) {
	if !f.closed {
		return nil, &os.PathError{Op: "stat", Path: f.name, Err: os.ErrNotExist}
	}
	return f.File.Stat()
}

func (f *s3StyleWriteOnlyFile) Close() error {
	f.closed = true
	return f.File.Close()
}
