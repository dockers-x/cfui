package s3dav

import (
	"os"
	"strings"
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

func TestS3ObjectKeyFSUsesSlashlessObjectKeys(t *testing.T) {
	source := &recordingFS{Fs: afero.NewMemMapFs()}
	if err := source.Fs.MkdirAll("cc-switch-sync/v2", 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	fs := s3ObjectKeyFS{Fs: source}

	if err := writeFile(fs, "/cc-switch-sync/v2/db.sql", strings.NewReader("hello")); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	if got, want := source.openFileNames[0], "cc-switch-sync/v2/db.sql"; got != want {
		t.Fatalf("expected slashless object key %q, got %q", want, got)
	}

	root, err := listFiles(fs, "/")
	if err != nil {
		t.Fatalf("listFiles root: %v", err)
	}
	if len(root.Entries) != 1 || root.Entries[0].Name != "cc-switch-sync" || !root.Entries[0].IsDir {
		t.Fatalf("unexpected root entries: %#v", root.Entries)
	}

	nested, err := listFiles(fs, "/cc-switch-sync")
	if err != nil {
		t.Fatalf("listFiles nested: %v", err)
	}
	if len(nested.Entries) != 1 || nested.Entries[0].Name != "v2" || !nested.Entries[0].IsDir {
		t.Fatalf("unexpected nested entries: %#v", nested.Entries)
	}
}

func TestS3ObjectKeyFSAppliesRootPrefixWithoutLeadingSlash(t *testing.T) {
	source := &recordingFS{Fs: afero.NewMemMapFs()}
	if err := source.Fs.MkdirAll("datasync/cc-switch-sync/v2", 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	fs := s3ObjectKeyFS{Fs: source, rootPrefix: "datasync"}

	if err := writeFile(fs, "/cc-switch-sync/v2/db.sql", strings.NewReader("hello")); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	if got, want := source.openFileNames[0], "datasync/cc-switch-sync/v2/db.sql"; got != want {
		t.Fatalf("expected prefixed object key %q, got %q", want, got)
	}

	root, err := listFiles(fs, "/")
	if err != nil {
		t.Fatalf("listFiles root: %v", err)
	}
	if len(root.Entries) != 1 || root.Entries[0].Name != "cc-switch-sync" || root.Entries[0].Path != "/cc-switch-sync/" {
		t.Fatalf("unexpected root entries: %#v", root.Entries)
	}
	if len(source.openNames) == 0 || source.openNames[len(source.openNames)-1] != "datasync" {
		t.Fatalf("expected root listing to open root prefix, got %#v", source.openNames)
	}
}

func TestS3ObjectKeyFSListsLegacyLeadingSlashPrefixes(t *testing.T) {
	source := &recordingFS{Fs: afero.NewMemMapFs()}
	fs := s3ObjectKeyFS{
		Fs: source,
		legacyEntries: func(cleaned string) ([]FileEntry, error) {
			switch cleaned {
			case "/":
				return []FileEntry{{Name: "cc-switch-sync", Path: "/cc-switch-sync/", IsDir: true, ModTime: time.Unix(0, 0)}}, nil
			case "/cc-switch-sync":
				return []FileEntry{{Name: "v2", Path: "/cc-switch-sync/v2/", IsDir: true, ModTime: time.Unix(0, 0)}}, nil
			default:
				return nil, nil
			}
		},
	}

	root, err := listFiles(fs, "/")
	if err != nil {
		t.Fatalf("listFiles root: %v", err)
	}
	if len(root.Entries) != 1 || root.Entries[0].Name != "cc-switch-sync" || root.Entries[0].Path != "/cc-switch-sync/" {
		t.Fatalf("unexpected root entries: %#v", root.Entries)
	}

	nested, err := listFiles(fs, "/cc-switch-sync")
	if err != nil {
		t.Fatalf("listFiles nested: %v", err)
	}
	if len(nested.Entries) != 1 || nested.Entries[0].Name != "v2" || nested.Entries[0].Path != "/cc-switch-sync/v2/" {
		t.Fatalf("unexpected nested entries: %#v", nested.Entries)
	}
}

func TestS3ObjectKeyFSStatsLegacyLeadingSlashDirectory(t *testing.T) {
	source := &recordingFS{Fs: afero.NewMemMapFs()}
	fs := s3ObjectKeyFS{
		Fs: source,
		legacyEntries: func(cleaned string) ([]FileEntry, error) {
			if cleaned != "/cc-switch-sync" {
				return nil, nil
			}
			return []FileEntry{{Name: "v2", Path: "/cc-switch-sync/v2/", IsDir: true, ModTime: time.Unix(0, 0)}}, nil
		},
	}

	info, err := fs.Stat("/cc-switch-sync")
	if err != nil {
		t.Fatalf("Stat legacy directory: %v", err)
	}
	if !info.IsDir() || info.Name() != "cc-switch-sync" {
		t.Fatalf("unexpected legacy directory info: %#v", info)
	}
}

func TestS3ObjectKeyFSRenamesLegacyLeadingSlashObjectToSlashlessKey(t *testing.T) {
	source := &recordingFS{
		Fs: afero.NewMemMapFs(),
		renameFunc: func(oldname, newname string) error {
			if oldname == "/cc-switch-sync/db.sql" && newname == "cc-switch-sync/db2.sql" {
				return nil
			}
			return &os.PathError{Op: "rename", Path: oldname, Err: os.ErrNotExist}
		},
	}
	fs := s3ObjectKeyFS{Fs: source}

	if err := fs.Rename("/cc-switch-sync/db.sql", "/cc-switch-sync/db2.sql"); err != nil {
		t.Fatalf("Rename legacy object: %v", err)
	}
	if len(source.renameNames) != 2 {
		t.Fatalf("expected normal and legacy rename attempts, got %#v", source.renameNames)
	}
	if got, want := source.renameNames[0], [2]string{"cc-switch-sync/db.sql", "cc-switch-sync/db2.sql"}; got != want {
		t.Fatalf("expected first rename attempt %#v, got %#v", want, got)
	}
	if got, want := source.renameNames[1], [2]string{"/cc-switch-sync/db.sql", "cc-switch-sync/db2.sql"}; got != want {
		t.Fatalf("expected legacy rename attempt %#v, got %#v", want, got)
	}
}

type recordingFS struct {
	afero.Fs
	openNames     []string
	openFileNames []string
	renameNames   [][2]string
	renameFunc    func(oldname, newname string) error
}

func (fs *recordingFS) Open(name string) (afero.File, error) {
	fs.openNames = append(fs.openNames, name)
	return fs.Fs.Open(name)
}

func (fs *recordingFS) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	fs.openFileNames = append(fs.openFileNames, name)
	return fs.Fs.OpenFile(name, flag, perm)
}

func (fs *recordingFS) Rename(oldname, newname string) error {
	fs.renameNames = append(fs.renameNames, [2]string{oldname, newname})
	if fs.renameFunc != nil {
		return fs.renameFunc(oldname, newname)
	}
	return fs.Fs.Rename(oldname, newname)
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
