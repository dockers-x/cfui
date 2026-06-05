package s3dav

import (
	"os"
	"testing"
	"time"

	"github.com/spf13/afero"
)

func TestListFilesSkipsRootPlaceholderEntries(t *testing.T) {
	fs := rootPlaceholderFS{Fs: afero.NewMemMapFs()}
	if err := fs.MkdirAll("/cc-switch-sync", 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := afero.WriteFile(fs, "/cc-switch-sync/db.sql", []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	resp, err := listFiles(fs, "/")
	if err != nil {
		t.Fatalf("listFiles: %v", err)
	}
	if len(resp.Entries) != 1 {
		t.Fatalf("expected one real entry, got %#v", resp.Entries)
	}
	if got := resp.Entries[0]; got.Name != "cc-switch-sync" || got.Path != "/cc-switch-sync/" || !got.IsDir {
		t.Fatalf("unexpected entry: %#v", got)
	}
}

type rootPlaceholderFS struct {
	afero.Fs
}

func (fs rootPlaceholderFS) Open(name string) (afero.File, error) {
	file, err := fs.Fs.Open(name)
	if err != nil {
		return nil, err
	}
	return rootPlaceholderFile{File: file}, nil
}

type rootPlaceholderFile struct {
	afero.File
}

func (f rootPlaceholderFile) Readdir(count int) ([]os.FileInfo, error) {
	infos, err := f.File.Readdir(count)
	if err != nil {
		return infos, err
	}
	return append([]os.FileInfo{staticFileInfo{name: "/", dir: true}}, infos...), nil
}

type staticFileInfo struct {
	name string
	dir  bool
}

func (i staticFileInfo) Name() string { return i.name }
func (i staticFileInfo) Size() int64  { return 0 }
func (i staticFileInfo) Mode() os.FileMode {
	if i.dir {
		return os.ModeDir | 0755
	}
	return 0644
}
func (i staticFileInfo) ModTime() time.Time { return time.Unix(0, 0) }
func (i staticFileInfo) IsDir() bool        { return i.dir }
func (i staticFileInfo) Sys() any           { return nil }
