package s3dav

import (
	"context"
	"io"
	"os"
	"path"
	"sort"
	"strings"

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
	if cfg.RootPrefix == "" {
		return fs, nil
	}
	return afero.NewBasePathFs(fs, "/"+cfg.RootPrefix), nil
}

func listFiles(fs afero.Fs, rawPath string) (FilesResponse, error) {
	cleaned, err := CleanPath(rawPath, false)
	if err != nil {
		return FilesResponse{}, err
	}
	file, err := fs.Open(cleaned)
	if err != nil {
		return FilesResponse{}, err
	}
	defer file.Close()

	infos, err := file.Readdir(0)
	if err != nil {
		return FilesResponse{}, err
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
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	return FilesResponse{
		Path:    cleaned,
		Parent:  ParentPath(cleaned),
		Entries: entries,
	}, nil
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
