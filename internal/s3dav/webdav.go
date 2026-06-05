package s3dav

import (
	"context"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/spf13/afero"
	"golang.org/x/net/webdav"
)

func (s *Service) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mount, ok := s.WebDAVMountForPath(r.URL.Path)
		if !ok {
			http.NotFound(w, r)
			return
		}
		mountPath := mount.MountPath
		if !strings.HasPrefix(r.URL.Path, mountPath) {
			http.NotFound(w, r)
			return
		}
		if mount.WebDAVAuthEnabled && !basicAuthOK(r, mount.WebDAVUsername, mount.WebDAVPasswordHash) {
			w.Header().Set("WWW-Authenticate", `Basic realm="cfui S3 WebDAV"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		fs, err := s.Filesystem(r.Context(), mount.Key)
		if err != nil {
			http.Error(w, "S3 WebDAV filesystem unavailable", http.StatusServiceUnavailable)
			return
		}
		handler := &webdav.Handler{
			Prefix:     mountPath,
			FileSystem: aferoWebDAVFS{fs: fs},
			LockSystem: webdav.NewMemLS(),
		}
		handler.ServeHTTP(w, r)
	})
}

type aferoWebDAVFS struct {
	fs afero.Fs
}

func (a aferoWebDAVFS) Mkdir(_ context.Context, name string, perm os.FileMode) error {
	cleaned, err := CleanPath(name, true)
	if err != nil {
		return err
	}
	return a.fs.MkdirAll(cleaned, perm)
}

func (a aferoWebDAVFS) OpenFile(_ context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	cleaned, err := CleanPath(name, false)
	if err != nil {
		return nil, err
	}
	writeCreate := flag&os.O_RDWR != 0 && flag&os.O_CREATE != 0
	if writeCreate {
		parent := ParentPath(cleaned)
		if parent != "" && parent != "/" {
			if err := a.fs.MkdirAll(parent, 0755); err != nil {
				return nil, err
			}
		}
		flag = (flag &^ os.O_RDWR) | os.O_WRONLY
	}
	file, err := a.fs.OpenFile(cleaned, flag, perm)
	if err != nil {
		return nil, err
	}
	if writeCreate {
		return &webDAVWriteFile{File: file, name: cleaned, modTime: time.Now()}, nil
	}
	return file, nil
}

func (a aferoWebDAVFS) RemoveAll(_ context.Context, name string) error {
	cleaned, err := CleanPath(name, true)
	if err != nil {
		return err
	}
	return a.fs.RemoveAll(cleaned)
}

func (a aferoWebDAVFS) Rename(_ context.Context, oldName, newName string) error {
	return renamePath(a.fs, oldName, newName)
}

func (a aferoWebDAVFS) Stat(_ context.Context, name string) (os.FileInfo, error) {
	cleaned, err := CleanPath(name, false)
	if err != nil {
		return nil, err
	}
	if cleaned == "/" {
		return s3ListFileInfo{name: "/", dir: true, modTime: time.Unix(0, 0)}, nil
	}
	info, err := a.fs.Stat(cleaned)
	if err == nil {
		return info, nil
	}
	if os.IsNotExist(err) {
		entries, listErr := listFiles(a.fs, cleaned)
		if listErr == nil && len(entries.Entries) > 0 {
			return s3ListFileInfo{name: path.Base(cleaned), dir: true, modTime: time.Unix(0, 0)}, nil
		}
	}
	return nil, err
}

type webDAVWriteFile struct {
	afero.File
	name    string
	size    int64
	modTime time.Time
	closed  bool
}

func (f *webDAVWriteFile) Write(p []byte) (int, error) {
	n, err := f.File.Write(p)
	f.size += int64(n)
	f.modTime = time.Now()
	return n, err
}

func (f *webDAVWriteFile) Close() error {
	f.closed = true
	return f.File.Close()
}

func (f *webDAVWriteFile) Stat() (os.FileInfo, error) {
	info, err := f.File.Stat()
	if err == nil {
		return info, nil
	}
	if f.closed {
		return nil, err
	}
	if f.modTime.IsZero() {
		f.modTime = time.Now()
	}
	return webDAVFileInfo{name: path.Base(f.name), size: f.size, modTime: f.modTime}, nil
}

type webDAVFileInfo struct {
	name    string
	size    int64
	modTime time.Time
}

func (i webDAVFileInfo) Name() string       { return i.name }
func (i webDAVFileInfo) Size() int64        { return i.size }
func (i webDAVFileInfo) Mode() os.FileMode  { return 0644 }
func (i webDAVFileInfo) ModTime() time.Time { return i.modTime }
func (i webDAVFileInfo) IsDir() bool        { return false }
func (i webDAVFileInfo) Sys() any           { return nil }
