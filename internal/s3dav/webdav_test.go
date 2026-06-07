package s3dav

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

func TestWebDAVHandlerUsesUpdatedPasswordWithoutRestart(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "/readme.txt", []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	svc := newTestService(t, fakeCloudflareClient{}, fs)
	hash, err := HashPassword("old-secret")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	cfg := svc.cfgMgr.Get()
	cfg.S3WebDAV.Enabled = true
	cfg.S3WebDAV.Mounts[0].EndpointURL = "https://s3.example.com"
	cfg.S3WebDAV.Mounts[0].BucketName = "bucket"
	cfg.S3WebDAV.Mounts[0].MountPath = "/webdav/private/"
	cfg.S3WebDAV.Mounts[0].AccessKeyID = "ak"
	cfg.S3WebDAV.Mounts[0].SecretAccessKey = "sk"
	cfg.S3WebDAV.Mounts[0].WebDAVUsername = "dav"
	cfg.S3WebDAV.Mounts[0].WebDAVPasswordHash = hash
	if err := svc.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}
	handler := svc.Handler()

	oldReq := httptest.NewRequest(http.MethodGet, "/webdav/private/readme.txt", nil)
	oldReq.SetBasicAuth("dav", "old-secret")
	oldRec := httptest.NewRecorder()
	handler.ServeHTTP(oldRec, oldReq)
	if oldRec.Code != http.StatusOK {
		t.Fatalf("expected old password to work before update, got %d: %s", oldRec.Code, oldRec.Body.String())
	}

	req := validMountRequest()
	req.MountPath = "/webdav/private/"
	req.RootPrefix = ""
	req.WebDAVUsername = "dav"
	req.WebDAVPassword = "new-secret"
	if _, err := svc.SaveMount(context.Background(), "default", req); err != nil {
		t.Fatalf("SaveMount new password: %v", err)
	}

	staleReq := httptest.NewRequest(http.MethodGet, "/webdav/private/readme.txt", nil)
	staleReq.SetBasicAuth("dav", "old-secret")
	staleRec := httptest.NewRecorder()
	handler.ServeHTTP(staleRec, staleReq)
	if staleRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected old password to fail after update, got %d: %s", staleRec.Code, staleRec.Body.String())
	}

	newReq := httptest.NewRequest(http.MethodGet, "/webdav/private/readme.txt", nil)
	newReq.SetBasicAuth("dav", "new-secret")
	newRec := httptest.NewRecorder()
	handler.ServeHTTP(newRec, newReq)
	if newRec.Code != http.StatusOK {
		t.Fatalf("expected new password to work without handler restart, got %d: %s", newRec.Code, newRec.Body.String())
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

func TestWebDAVPutUsesSlashlessS3ObjectKey(t *testing.T) {
	source := &recordingFS{Fs: afero.NewMemMapFs()}
	fs := s3ObjectKeyFS{Fs: s3StyleWriteOnlyFS{Fs: source}}
	svc := newTestService(t, fakeCloudflareClient{}, fs)
	cfg := svc.cfgMgr.Get()
	cfg.S3WebDAV.Enabled = true
	cfg.S3WebDAV.Mounts[0].EndpointURL = "https://s3.example.com"
	cfg.S3WebDAV.Mounts[0].BucketName = "bucket"
	cfg.S3WebDAV.Mounts[0].MountPath = "/webdav/datasync/"
	cfg.S3WebDAV.Mounts[0].AccessKeyID = "ak"
	cfg.S3WebDAV.Mounts[0].SecretAccessKey = "sk"
	cfg.S3WebDAV.Mounts[0].WebDAVAuthEnabled = false
	if err := svc.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/webdav/datasync/cc-switch-sync/v2/db-v6/default/db.sql", bytes.NewBufferString("hello"))
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusNoContent {
		t.Fatalf("expected WebDAV PUT to create nested object, got %d: %s", rec.Code, rec.Body.String())
	}
	want := "cc-switch-sync/v2/db-v6/default/db.sql"
	if len(source.openFileNames) == 0 || source.openFileNames[len(source.openFileNames)-1] != want {
		t.Fatalf("expected slashless object key %q, got %#v", want, source.openFileNames)
	}
	got, err := afero.ReadFile(source.Fs, want)
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("unexpected uploaded content %q", string(got))
	}
}

func TestWebDAVOpenFileWriteOnlyCreateCreatesParentPrefixes(t *testing.T) {
	source := s3StyleWriteOnlyFS{Fs: afero.NewMemMapFs()}
	fs := newWebDAVFileSystem(source)

	file, err := fs.OpenFile(context.Background(), "/cc-switch-sync/v2/db.sql", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		t.Fatalf("OpenFile write-only create: %v", err)
	}
	if _, err := file.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	info, err := file.Stat()
	if err != nil {
		t.Fatalf("Stat before close: %v", err)
	}
	if info.Size() != int64(len("hello")) {
		t.Fatalf("expected synthetic size %d, got %d", len("hello"), info.Size())
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got, err := afero.ReadFile(source.Fs, "/cc-switch-sync/v2/db.sql")
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("unexpected uploaded content %q", string(got))
	}
}

func TestWebDAVOpenFileReadWriteWithoutCreateFallsBackToReadOnly(t *testing.T) {
	source := s3StyleWriteOnlyFS{Fs: afero.NewMemMapFs()}
	if err := afero.WriteFile(source.Fs, "/props.txt", []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	fs := newWebDAVFileSystem(source)

	file, err := fs.OpenFile(context.Background(), "/props.txt", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("OpenFile O_RDWR without create should not hit S3 read/write mode: %v", err)
	}
	defer file.Close()
	got := make([]byte, 5)
	if _, err := file.Read(got); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("unexpected content %q", string(got))
	}
}

func TestWebDAVWriteFileStatAfterCloseFallsBackToSyntheticInfo(t *testing.T) {
	source := afero.NewMemMapFs()
	fs := newWebDAVFileSystem(statNeverOpenFileFS{Fs: source})

	file, err := fs.OpenFile(context.Background(), "/db.sql", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if _, err := file.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	info, err := file.Stat()
	if err != nil {
		t.Fatalf("Stat after close: %v", err)
	}
	if info.Name() != "db.sql" {
		t.Fatalf("expected synthetic name db.sql, got %q", info.Name())
	}
	if info.Size() != int64(len("hello")) {
		t.Fatalf("expected synthetic size %d, got %d", len("hello"), info.Size())
	}
}

func TestWebDAVPropfindMountRootWorksWhenS3RootHasNoDirectoryObject(t *testing.T) {
	fs := rootStatNotFoundFS{Fs: afero.NewMemMapFs()}
	svc := newTestService(t, fakeCloudflareClient{}, fs)
	cfg := svc.cfgMgr.Get()
	cfg.S3WebDAV.Enabled = true
	cfg.S3WebDAV.Mounts[0].EndpointURL = "https://s3.example.com"
	cfg.S3WebDAV.Mounts[0].BucketName = "bucket"
	cfg.S3WebDAV.Mounts[0].MountPath = "/webdav/datasync/"
	cfg.S3WebDAV.Mounts[0].AccessKeyID = "ak"
	cfg.S3WebDAV.Mounts[0].SecretAccessKey = "sk"
	cfg.S3WebDAV.Mounts[0].WebDAVAuthEnabled = false
	if err := svc.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	req := httptest.NewRequest("PROPFIND", "/webdav/datasync/", nil)
	req.Header.Set("Depth", "0")
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	if rec.Code != webDAVMultiStatus {
		t.Fatalf("expected mount root PROPFIND to succeed, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWebDAVLockSystemPersistsAcrossRequests(t *testing.T) {
	fs := afero.NewMemMapFs()
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
	if err := svc.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}
	handler := svc.Handler()
	lockBody := `<?xml version="1.0" encoding="utf-8"?>
<D:lockinfo xmlns:D="DAV:">
  <D:lockscope><D:exclusive/></D:lockscope>
  <D:locktype><D:write/></D:locktype>
  <D:owner><D:href>cfui-test</D:href></D:owner>
</D:lockinfo>`

	lockReq := httptest.NewRequest("LOCK", "/webdav/public/docs/readme.txt", strings.NewReader(lockBody))
	lockReq.Header.Set("Content-Type", "application/xml")
	lockReq.Header.Set("Depth", "0")
	lockReq.Header.Set("Timeout", "Second-60")
	lockRec := httptest.NewRecorder()
	handler.ServeHTTP(lockRec, lockReq)
	if lockRec.Code != http.StatusOK {
		t.Fatalf("expected LOCK to succeed, got %d: %s", lockRec.Code, lockRec.Body.String())
	}
	token := lockRec.Header().Get("Lock-Token")
	if token == "" {
		t.Fatalf("expected LOCK response to include Lock-Token")
	}

	unlockReq := httptest.NewRequest("UNLOCK", "/webdav/public/docs/readme.txt", nil)
	unlockReq.Header.Set("Lock-Token", token)
	unlockRec := httptest.NewRecorder()
	handler.ServeHTTP(unlockRec, unlockReq)
	if unlockRec.Code != http.StatusNoContent {
		t.Fatalf("expected UNLOCK to succeed with persisted lock system, got %d: %s", unlockRec.Code, unlockRec.Body.String())
	}
}

func TestBrowserGetDirectoryShowsReadOnlyListing(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := fs.MkdirAll("/docs", 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := afero.WriteFile(fs, "/docs/readme.txt", []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile readme: %v", err)
	}
	if err := afero.WriteFile(fs, "/a<b>.txt", []byte("unsafe name"), 0644); err != nil {
		t.Fatalf("WriteFile unsafe: %v", err)
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
	if err := svc.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/webdav/public/", nil)
	req.Header.Set("Accept", "*/*")
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected browser directory listing, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/html") {
		t.Fatalf("expected HTML content type, got %q", got)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`href="/webdav/public/docs/"`,
		`href="/webdav/public/a%3Cb%3E.txt"`,
		`a&lt;b&gt;.txt`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected listing to contain %q, body: %s", want, body)
		}
	}
	if strings.Contains(body, "<button") || strings.Contains(body, "Upload") || strings.Contains(body, "Delete") {
		t.Fatalf("expected read-only listing without write controls, body: %s", body)
	}
}

func TestBrowserHeadDirectoryReturnsListingHeadersOnly(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := fs.MkdirAll("/docs", 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
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
	if err := svc.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	req := httptest.NewRequest(http.MethodHead, "/webdav/public/", nil)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected browser directory HEAD to succeed, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/html") {
		t.Fatalf("expected HTML content type, got %q", got)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("expected HEAD response body to be empty, got %q", rec.Body.String())
	}
}

func TestBrowserGetFileSupportsRangeRequest(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := fs.MkdirAll("/docs", 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := afero.WriteFile(fs, "/docs/readme.txt", []byte("hello world"), 0644); err != nil {
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
	if err := svc.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/webdav/public/docs/readme.txt", nil)
	req.Header.Set("Range", "bytes=0-4")
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("expected partial content, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "hello" {
		t.Fatalf("expected ranged body %q, got %q", "hello", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Range"); got != "bytes 0-4/11" {
		t.Fatalf("unexpected Content-Range %q", got)
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

type statNeverFile struct {
	afero.File
}

func (f statNeverFile) Stat() (os.FileInfo, error) {
	return nil, &os.PathError{Op: "stat", Path: f.Name(), Err: os.ErrNotExist}
}

type statNeverOpenFileFS struct {
	afero.Fs
}

func (fs statNeverOpenFileFS) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	file, err := fs.Fs.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	return statNeverFile{File: file}, nil
}

type rootStatNotFoundFS struct {
	afero.Fs
}

func (fs rootStatNotFoundFS) Stat(name string) (os.FileInfo, error) {
	if name == "/" {
		return nil, &os.PathError{Op: "stat", Path: name, Err: os.ErrNotExist}
	}
	return fs.Fs.Stat(name)
}

const webDAVMultiStatus = 207
