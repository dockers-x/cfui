package s3dav

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	aferos3 "github.com/fclairamb/afero-s3"
	"github.com/spf13/afero"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
)

type FSFactory func(context.Context, FSConfig, Credentials) (afero.Fs, error)

func newS3FS(_ context.Context, cfg FSConfig, creds Credentials) (afero.Fs, error) {
	awsCfg := aws.Config{
		Region: cfg.Region,
		Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(
			creds.AccessKeyID,
			creds.SecretAccessKey,
			"",
		)),
		EndpointResolverWithOptions: aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
			return aws.Endpoint{
				PartitionID:       "aws",
				URL:               cfg.Endpoint,
				SigningRegion:     cfg.Region,
				HostnameImmutable: true,
			}, nil
		}),
	}
	client := awss3.NewFromConfig(awsCfg, func(options *awss3.Options) {
		options.UsePathStyle = cfg.PathStyle
	})
	fs := aferos3.NewFsFromClient(cfg.BucketName, client)
	return s3ObjectKeyFS{Fs: fs, client: client, bucket: cfg.BucketName, rootPrefix: cfg.RootPrefix}, nil
}

type s3ObjectKeyFS struct {
	afero.Fs
	client        *awss3.Client
	bucket        string
	rootPrefix    string
	legacyEntries func(cleaned string) ([]FileEntry, error)
}

func (fs s3ObjectKeyFS) s3Path(name string) string {
	rootPrefix := strings.Trim(fs.rootPrefix, "/")
	key := strings.TrimLeft(name, "/")
	if key == "" {
		if rootPrefix != "" {
			return rootPrefix
		}
		return "/"
	}
	if rootPrefix == "" {
		return key
	}
	return path.Join(rootPrefix, key)
}

func (fs s3ObjectKeyFS) legacyS3Path(name string) string {
	rootPrefix := strings.Trim(fs.rootPrefix, "/")
	key := strings.TrimLeft(name, "/")
	if rootPrefix == "" {
		return "/" + key
	}
	if key == "" {
		return rootPrefix + "/"
	}
	return rootPrefix + "//" + key
}

func (fs s3ObjectKeyFS) legacyS3Prefix(cleaned string) string {
	rootPrefix := strings.Trim(fs.rootPrefix, "/")
	key := strings.TrimLeft(cleaned, "/")
	if rootPrefix != "" {
		if key == "" {
			return rootPrefix + "//"
		}
		return rootPrefix + "//" + key + "/"
	}
	if key == "" {
		return "/"
	}
	return "/" + key + "/"
}

func (fs s3ObjectKeyFS) Create(name string) (afero.File, error) {
	return fs.Fs.Create(fs.s3Path(name))
}

func (fs s3ObjectKeyFS) Chmod(name string, mode os.FileMode) error {
	return fs.Fs.Chmod(fs.s3Path(name), mode)
}

func (fs s3ObjectKeyFS) Chown(name string, uid, gid int) error {
	return fs.Fs.Chown(fs.s3Path(name), uid, gid)
}

func (fs s3ObjectKeyFS) Chtimes(name string, atime, mtime time.Time) error {
	return fs.Fs.Chtimes(fs.s3Path(name), atime, mtime)
}

func (fs s3ObjectKeyFS) Mkdir(name string, perm os.FileMode) error {
	return fs.Fs.Mkdir(fs.s3Path(name), perm)
}

func (fs s3ObjectKeyFS) MkdirAll(name string, perm os.FileMode) error {
	return fs.Fs.MkdirAll(fs.s3Path(name), perm)
}

func (fs s3ObjectKeyFS) Open(name string) (afero.File, error) {
	file, err := fs.Fs.Open(fs.s3Path(name))
	if err == nil {
		return file, nil
	}
	if legacyFile, legacyErr := fs.Fs.Open(fs.legacyS3Path(name)); legacyErr == nil {
		return legacyFile, nil
	}
	return nil, err
}

func (fs s3ObjectKeyFS) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	return fs.Fs.OpenFile(fs.s3Path(name), flag, perm)
}

func (fs s3ObjectKeyFS) Remove(name string) error {
	err := fs.Fs.Remove(fs.s3Path(name))
	legacyErr := fs.Fs.Remove(fs.legacyS3Path(name))
	if err == nil && (legacyErr == nil || errors.Is(legacyErr, os.ErrNotExist)) {
		return nil
	}
	if err == nil {
		return legacyErr
	}
	if legacyErr == nil {
		return nil
	}
	return err
}

func (fs s3ObjectKeyFS) RemoveAll(name string) error {
	err := fs.Fs.RemoveAll(fs.s3Path(name))
	removed, legacyErr := fs.removeAllLegacy(name)
	if err == nil && legacyErr == nil {
		return nil
	}
	if err == nil {
		return legacyErr
	}
	if legacyErr == nil && removed {
		return nil
	}
	return err
}

func (fs s3ObjectKeyFS) Rename(oldname, newname string) error {
	err := fs.Fs.Rename(fs.s3Path(oldname), fs.s3Path(newname))
	if err == nil {
		return nil
	}
	if legacyErr := fs.Fs.Rename(fs.legacyS3Path(oldname), fs.s3Path(newname)); legacyErr == nil {
		return nil
	}
	return err
}

func (fs s3ObjectKeyFS) Stat(name string) (os.FileInfo, error) {
	info, err := fs.Fs.Stat(fs.s3Path(name))
	if err == nil {
		return info, nil
	}
	if legacyInfo, legacyErr := fs.Fs.Stat(fs.legacyS3Path(name)); legacyErr == nil {
		return legacyInfo, nil
	}
	if cleaned, cleanErr := CleanPath(name, false); cleanErr == nil {
		legacyEntries, legacyErr := fs.listLegacyEntries(cleaned)
		if legacyErr == nil && len(legacyEntries) > 0 {
			return s3ListFileInfo{name: path.Base(cleaned), dir: true, modTime: time.Unix(0, 0)}, nil
		}
	}
	return nil, err
}

type s3ListFileInfo struct {
	name    string
	dir     bool
	size    int64
	modTime time.Time
}

func (i s3ListFileInfo) Name() string { return i.name }
func (i s3ListFileInfo) Size() int64  { return i.size }
func (i s3ListFileInfo) Mode() os.FileMode {
	if i.dir {
		return os.ModeDir | 0755
	}
	return 0644
}
func (i s3ListFileInfo) ModTime() time.Time { return i.modTime }
func (i s3ListFileInfo) IsDir() bool        { return i.dir }
func (i s3ListFileInfo) Sys() any           { return nil }

func (fs s3ObjectKeyFS) removeAllLegacy(name string) (bool, error) {
	if fs.client == nil || fs.bucket == "" {
		return false, nil
	}
	cleaned, err := CleanPath(name, false)
	if err != nil {
		return false, err
	}
	prefix := fs.legacyS3Prefix(cleaned)
	paginator := awss3.NewListObjectsV2Paginator(fs.client, &awss3.ListObjectsV2Input{
		Bucket: aws.String(fs.bucket),
		Prefix: aws.String(prefix),
	})
	removed := false
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(context.Background())
		if err != nil {
			return removed, err
		}
		for _, object := range page.Contents {
			key := aws.ToString(object.Key)
			if key == "" {
				continue
			}
			if _, err := fs.client.DeleteObject(context.Background(), &awss3.DeleteObjectInput{
				Bucket: aws.String(fs.bucket),
				Key:    aws.String(key),
			}); err != nil {
				return removed, err
			}
			removed = true
		}
	}
	return removed, nil
}

func (fs s3ObjectKeyFS) listEntries(cleaned string) ([]FileEntry, error) {
	normal, normalErr := readDirEntries(fs, cleaned)
	legacy, legacyErr := fs.listLegacyEntries(cleaned)
	if normalErr != nil && len(legacy) == 0 {
		return nil, normalErr
	}
	if legacyErr != nil && normalErr != nil && !errors.Is(normalErr, os.ErrNotExist) {
		return nil, normalErr
	}
	return mergeFileEntries(normal, legacy), nil
}

func (fs s3ObjectKeyFS) listLegacyEntries(cleaned string) ([]FileEntry, error) {
	if fs.legacyEntries != nil {
		return fs.legacyEntries(cleaned)
	}
	if fs.client == nil || fs.bucket == "" {
		return nil, nil
	}
	prefix := fs.legacyS3Prefix(cleaned)
	input := &awss3.ListObjectsV2Input{
		Bucket:    aws.String(fs.bucket),
		Prefix:    aws.String(prefix),
		Delimiter: aws.String("/"),
	}
	paginator := awss3.NewListObjectsV2Paginator(fs.client, input)
	var entries []FileEntry
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(context.Background())
		if err != nil {
			return nil, err
		}
		for _, common := range page.CommonPrefixes {
			name, ok := legacyEntryName(prefix, aws.ToString(common.Prefix))
			if !ok {
				continue
			}
			entries = append(entries, FileEntry{
				Name:    name,
				Path:    ensureDirPath(JoinPath(cleaned, name)),
				IsDir:   true,
				ModTime: time.Unix(0, 0),
			})
		}
		for _, object := range page.Contents {
			key := aws.ToString(object.Key)
			if strings.HasSuffix(key, "/") {
				continue
			}
			name, ok := legacyEntryName(prefix, key)
			if !ok {
				continue
			}
			modTime := time.Time{}
			if object.LastModified != nil {
				modTime = *object.LastModified
			}
			entries = append(entries, FileEntry{
				Name:    name,
				Path:    JoinPath(cleaned, name),
				IsDir:   false,
				Size:    aws.ToInt64(object.Size),
				ModTime: modTime,
			})
		}
	}
	return entries, nil
}

func listFiles(fs afero.Fs, rawPath string) (FilesResponse, error) {
	cleaned, err := CleanPath(rawPath, false)
	if err != nil {
		return FilesResponse{}, err
	}
	var entries []FileEntry
	if lister, ok := fs.(interface {
		listEntries(cleaned string) ([]FileEntry, error)
	}); ok {
		entries, err = lister.listEntries(cleaned)
	} else {
		entries, err = readDirEntries(fs, cleaned)
	}
	if err != nil {
		return FilesResponse{}, err
	}
	sortFileEntries(entries)
	return FilesResponse{
		Path:    cleaned,
		Parent:  ParentPath(cleaned),
		Entries: entries,
	}, nil
}

func readDirEntries(fs afero.Fs, cleaned string) ([]FileEntry, error) {
	file, err := fs.Open(cleaned)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	infos, err := file.Readdir(0)
	if err != nil {
		return nil, err
	}
	entries := make([]FileEntry, 0, len(infos))
	for _, info := range infos {
		name, ok := listEntryName(info.Name())
		if !ok {
			continue
		}
		p := JoinPath(cleaned, name)
		if info.IsDir() && !strings.HasSuffix(p, "/") {
			p += "/"
		}
		entries = append(entries, FileEntry{
			Name:    name,
			Path:    p,
			IsDir:   info.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}
	return entries, nil
}

func sortFileEntries(entries []FileEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
}

func mergeFileEntries(groups ...[]FileEntry) []FileEntry {
	seen := make(map[string]struct{})
	var merged []FileEntry
	for _, entries := range groups {
		for _, entry := range entries {
			key := entry.Path + "|" + strconv.FormatBool(entry.IsDir)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, entry)
		}
	}
	return merged
}

func legacyEntryName(prefix, key string) (string, bool) {
	rel := strings.TrimPrefix(key, prefix)
	rel = strings.Trim(rel, "/")
	if rel == "" || rel == "." {
		return "", false
	}
	if strings.Contains(rel, "/") {
		rel = strings.Split(rel, "/")[0]
	}
	if rel == "" || rel == "." {
		return "", false
	}
	return rel, true
}

func ensureDirPath(p string) string {
	if strings.HasSuffix(p, "/") {
		return p
	}
	return p + "/"
}

func listEntryName(raw string) (string, bool) {
	name := strings.TrimSpace(raw)
	name = strings.Trim(name, "/")
	if name == "" || name == "." {
		return "", false
	}
	if strings.Contains(name, "/") {
		name = path.Base(name)
	}
	if name == "" || name == "." || name == "/" {
		return "", false
	}
	return name, true
}

func writeFile(fs afero.Fs, rawPath string, body io.Reader) error {
	cleaned, err := CleanPath(rawPath, true)
	if err != nil {
		return err
	}
	if parent := ParentPath(cleaned); parent != "" && parent != "/" {
		if err := fs.MkdirAll(parent, 0755); err != nil {
			return err
		}
	}
	file, err := fs.OpenFile(cleaned, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, body)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func openFile(fs afero.Fs, rawPath string) (afero.File, os.FileInfo, error) {
	cleaned, err := CleanPath(rawPath, true)
	if err != nil {
		return nil, nil, err
	}
	info, err := fs.Stat(cleaned)
	if err != nil {
		return nil, nil, err
	}
	file, err := fs.Open(cleaned)
	if err != nil {
		return nil, nil, err
	}
	return file, info, nil
}

func deletePath(fs afero.Fs, rawPath string) error {
	cleaned, err := CleanPath(rawPath, true)
	if err != nil {
		return err
	}
	info, err := fs.Stat(cleaned)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fs.RemoveAll(cleaned)
	}
	return fs.Remove(cleaned)
}

func mkdir(fs afero.Fs, rawPath string) error {
	cleaned, err := CleanPath(rawPath, true)
	if err != nil {
		return err
	}
	return fs.MkdirAll(cleaned, 0755)
}

func renamePath(fs afero.Fs, from, to string) error {
	cleanFrom, err := CleanPath(from, true)
	if err != nil {
		return err
	}
	cleanTo, err := CleanPath(to, true)
	if err != nil {
		return err
	}
	return fs.Rename(cleanFrom, cleanTo)
}

func endpointFor(accountID, jurisdiction string) string {
	switch jurisdiction {
	case "eu":
		return "https://" + accountID + ".eu.r2.cloudflarestorage.com"
	case "fedramp":
		return "https://" + accountID + ".fedramp.r2.cloudflarestorage.com"
	default:
		return "https://" + accountID + ".r2.cloudflarestorage.com"
	}
}

func normalizeJurisdiction(v string) string {
	switch strings.TrimSpace(v) {
	case "eu", "fedramp":
		return strings.TrimSpace(v)
	default:
		return "default"
	}
}
