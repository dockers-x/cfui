package s3dav

import (
	"bytes"
	"context"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
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
		if serveBrowserReadOnly(w, r, mountPath, fs) {
			return
		}
		handler := &webdav.Handler{
			Prefix:     mountPath,
			FileSystem: aferoWebDAVFS{fs: fs},
			LockSystem: s.webDAVLockSystem(mount.Key),
		}
		handler.ServeHTTP(w, r)
	})
}

func (s *Service) webDAVLockSystem(key string) webdav.LockSystem {
	key = normalizeMountKey(key)
	s.webDAVLocksMu.Lock()
	defer s.webDAVLocksMu.Unlock()
	if s.webDAVLocks == nil {
		s.webDAVLocks = make(map[string]webdav.LockSystem)
	}
	if ls, ok := s.webDAVLocks[key]; ok {
		return ls
	}
	ls := webdav.NewMemLS()
	s.webDAVLocks[key] = ls
	return ls
}

func serveBrowserReadOnly(w http.ResponseWriter, r *http.Request, mountPath string, fs afero.Fs) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	rel := strings.TrimPrefix(r.URL.Path, mountPath)
	cleaned, err := CleanPath(rel, false)
	if err != nil {
		http.NotFound(w, r)
		return true
	}
	webFS := aferoWebDAVFS{fs: fs}
	info, err := webFS.Stat(r.Context(), cleaned)
	if err != nil {
		http.NotFound(w, r)
		return true
	}
	if !info.IsDir() {
		file, err := afero.NewHttpFs(fs).Open(cleaned)
		if err != nil {
			http.NotFound(w, r)
			return true
		}
		defer file.Close()
		http.ServeContent(w, r, cleaned, info.ModTime(), file)
		return true
	}
	if !strings.HasSuffix(r.URL.Path, "/") {
		redirectURL := *r.URL
		redirectURL.Path += "/"
		http.Redirect(w, r, redirectURL.String(), http.StatusMovedPermanently)
		return true
	}
	entries, err := listFiles(fs, cleaned)
	if err != nil {
		if cleaned == "/" && os.IsNotExist(err) {
			entries = FilesResponse{Path: cleaned, Parent: ParentPath(cleaned)}
		} else {
			http.Error(w, "Failed to list directory", http.StatusInternalServerError)
			return true
		}
	}
	page := browserDirectoryPageFor(mountPath, entries)
	var body bytes.Buffer
	if err := browserDirectoryTemplate.Execute(&body, page); err != nil {
		http.Error(w, "Failed to render directory", http.StatusInternalServerError)
		return true
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		return true
	}
	_, _ = w.Write(body.Bytes())
	return true
}

type browserDirectoryPage struct {
	Title      string
	Path       string
	ParentHref string
	Entries    []browserDirectoryEntry
	Empty      bool
}

type browserDirectoryEntry struct {
	Name     string
	Href     string
	Kind     string
	Size     string
	Modified string
	IsDir    bool
}

func browserDirectoryPageFor(mountPath string, files FilesResponse) browserDirectoryPage {
	entries := make([]browserDirectoryEntry, 0, len(files.Entries))
	for _, entry := range files.Entries {
		name := entry.Name
		kind := "File"
		size := formatBrowserSize(entry.Size)
		if entry.IsDir {
			name += "/"
			kind = "Folder"
			size = "-"
		}
		entries = append(entries, browserDirectoryEntry{
			Name:     name,
			Href:     browserHref(mountPath, entry.Path, entry.IsDir),
			Kind:     kind,
			Size:     size,
			Modified: formatBrowserTime(entry.ModTime),
			IsDir:    entry.IsDir,
		})
	}
	parentHref := ""
	if files.Parent != "" {
		parentHref = browserHref(mountPath, files.Parent, true)
	}
	displayPath := mountPath
	if files.Path != "/" {
		displayPath = strings.TrimRight(mountPath, "/") + "/" + strings.TrimPrefix(files.Path, "/") + "/"
	}
	return browserDirectoryPage{
		Title:      "cfui WebDAV",
		Path:       displayPath,
		ParentHref: parentHref,
		Entries:    entries,
		Empty:      len(entries) == 0,
	}
}

func browserHref(mountPath, cleaned string, dir bool) string {
	cleaned = strings.Trim(cleaned, "/")
	if cleaned == "" {
		return mountPath
	}
	parts := strings.Split(cleaned, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	href := strings.TrimRight(mountPath, "/") + "/" + strings.Join(parts, "/")
	if dir && !strings.HasSuffix(href, "/") {
		href += "/"
	}
	return href
}

func formatBrowserSize(size int64) string {
	if size < 0 {
		return "-"
	}
	if size < 1024 {
		return strconv.FormatInt(size, 10) + " B"
	}
	units := []string{"KB", "MB", "GB", "TB"}
	value := float64(size) / 1024
	for _, unit := range units {
		if value < 1024 {
			return strconv.FormatFloat(value, 'f', 1, 64) + " " + unit
		}
		value /= 1024
	}
	return strconv.FormatFloat(value, 'f', 1, 64) + " PB"
}

func formatBrowserTime(t time.Time) string {
	if t.IsZero() || t.Equal(time.Unix(0, 0)) {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04")
}

var browserDirectoryTemplate = template.Must(template.New("s3dav-browser-directory").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} - {{.Path}}</title>
<style>
:root {
  color-scheme: light dark;
  --bg: #f7f8fb;
  --surface: #ffffff;
  --surface-2: #eef2f6;
  --text: #1f2933;
  --muted: #667085;
  --line: #d9e0e8;
  --accent: #0f766e;
  --accent-soft: #dff3ef;
  --shadow: 0 16px 36px rgba(31, 41, 51, 0.08);
}
@media (prefers-color-scheme: dark) {
  :root {
    --bg: #111827;
    --surface: #182230;
    --surface-2: #202b3a;
    --text: #f2f5f8;
    --muted: #aab4c0;
    --line: #334155;
    --accent: #5eead4;
    --accent-soft: #123832;
    --shadow: 0 18px 45px rgba(0, 0, 0, 0.28);
  }
}
* { box-sizing: border-box; }
body {
  margin: 0;
  min-height: 100dvh;
  background: var(--bg);
  color: var(--text);
  font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  font-size: 16px;
  line-height: 1.5;
}
a { color: inherit; }
.shell {
  width: min(1120px, calc(100% - 32px));
  margin: 0 auto;
  padding: 32px 0 56px;
}
.top {
  padding: 28px 0 22px;
  border-bottom: 1px solid var(--line);
  background: color-mix(in srgb, var(--bg) 88%, transparent);
}
.top__inner {
  width: min(1120px, calc(100% - 32px));
  margin: 0 auto;
}
.brand {
  margin: 0 0 10px;
  color: var(--muted);
  font-size: 13px;
  font-weight: 700;
  letter-spacing: 0;
  text-transform: uppercase;
}
h1 {
  margin: 0;
  overflow-wrap: anywhere;
  font-size: clamp(22px, 4vw, 34px);
  line-height: 1.18;
  letter-spacing: 0;
}
.path {
  display: inline-block;
  margin-top: 14px;
  padding: 8px 10px;
  max-width: 100%;
  overflow-wrap: anywhere;
  border: 1px solid var(--line);
  background: var(--surface-2);
  border-radius: 6px;
  color: var(--muted);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace;
  font-size: 13px;
}
.list {
  overflow: hidden;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--surface);
  box-shadow: var(--shadow);
}
.list__bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-height: 56px;
  padding: 0 18px;
  border-bottom: 1px solid var(--line);
}
.count {
  color: var(--muted);
  font-size: 14px;
}
.parent {
  display: inline-flex;
  align-items: center;
  min-height: 40px;
  color: var(--accent);
  font-weight: 650;
  text-decoration: none;
}
.parent:focus-visible,
.file-link:focus-visible {
  outline: 3px solid color-mix(in srgb, var(--accent) 45%, transparent);
  outline-offset: 3px;
}
table {
  width: 100%;
  border-collapse: collapse;
}
th {
  padding: 12px 18px;
  color: var(--muted);
  font-size: 12px;
  font-weight: 700;
  text-align: left;
  text-transform: uppercase;
  letter-spacing: 0;
  background: color-mix(in srgb, var(--surface-2) 62%, transparent);
}
td {
  padding: 14px 18px;
  border-top: 1px solid var(--line);
  vertical-align: middle;
}
tbody tr:first-child td { border-top: 0; }
tbody tr:hover { background: color-mix(in srgb, var(--accent-soft) 45%, transparent); }
.name-cell { width: 58%; }
.file-link {
  display: inline-flex;
  align-items: center;
  min-height: 34px;
  max-width: 100%;
  color: var(--text);
  font-weight: 620;
  text-decoration: none;
  overflow-wrap: anywhere;
}
.file-link[data-dir="true"] { color: var(--accent); }
.meta {
  color: var(--muted);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.empty {
  padding: 44px 18px;
  color: var(--muted);
  text-align: center;
}
@media (max-width: 720px) {
  .shell, .top__inner { width: min(100% - 24px, 1120px); }
  .top { padding-top: 22px; }
  .list__bar { align-items: flex-start; flex-direction: column; padding: 12px 14px; }
  table, thead, tbody, tr, td { display: block; width: 100%; }
  thead { display: none; }
  tbody tr { border-top: 1px solid var(--line); padding: 10px 0; }
  tbody tr:first-child { border-top: 0; }
  td { border: 0; padding: 5px 14px; }
  .name-cell { width: 100%; }
  .meta { white-space: normal; }
}
</style>
</head>
<body>
<header class="top">
  <div class="top__inner">
    <p class="brand">cfui WebDAV</p>
    <h1>Files</h1>
    <div class="path">{{.Path}}</div>
  </div>
</header>
<main class="shell">
  <section class="list" aria-label="Directory listing">
    <div class="list__bar">
      {{if .ParentHref}}<a class="parent" href="{{.ParentHref}}">Back</a>{{else}}<span></span>{{end}}
      <span class="count">{{len .Entries}} items</span>
    </div>
    {{if .Empty}}
      <div class="empty">No files in this folder.</div>
    {{else}}
      <table>
        <thead>
          <tr>
            <th scope="col">Name</th>
            <th scope="col">Type</th>
            <th scope="col">Size</th>
            <th scope="col">Modified</th>
          </tr>
        </thead>
        <tbody>
        {{range .Entries}}
          <tr>
            <td class="name-cell"><a class="file-link" data-dir="{{.IsDir}}" href="{{.Href}}">{{.Name}}</a></td>
            <td class="meta">{{.Kind}}</td>
            <td class="meta">{{.Size}}</td>
            <td class="meta">{{.Modified}}</td>
          </tr>
        {{end}}
        </tbody>
      </table>
    {{end}}
  </section>
</main>
</body>
</html>
`))

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
	wholeObjectWrite := webDAVWholeObjectWrite(flag)
	if wholeObjectWrite {
		parent := ParentPath(cleaned)
		if parent != "" && parent != "/" {
			if err := a.fs.MkdirAll(parent, 0755); err != nil {
				return nil, err
			}
		}
		flag = (flag &^ os.O_RDWR) | os.O_WRONLY
	} else if flag&os.O_RDWR != 0 {
		flag &^= os.O_RDWR
	}
	file, err := a.fs.OpenFile(cleaned, flag, perm)
	if err != nil {
		return nil, err
	}
	if wholeObjectWrite {
		return &webDAVWriteFile{File: file, name: cleaned, modTime: time.Now()}, nil
	}
	return file, nil
}

func webDAVWholeObjectWrite(flag int) bool {
	return flag&(os.O_CREATE|os.O_TRUNC|os.O_WRONLY) != 0
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
