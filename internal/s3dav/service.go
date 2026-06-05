package s3dav

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"cfui/internal/config"

	cloudflare "github.com/cloudflare/cloudflare-go"
	"github.com/spf13/afero"
)

type Service struct {
	cfgMgr    *config.Manager
	newClient ClientFactory
	newFS     FSFactory
}

func NewService(cfgMgr *config.Manager) *Service {
	return &Service{
		cfgMgr:    cfgMgr,
		newClient: defaultClientFactory,
		newFS:     newS3FS,
	}
}

func NewServiceForTest(cfgMgr *config.Manager, newClient ClientFactory, newFS FSFactory) *Service {
	s := NewService(cfgMgr)
	if newClient != nil {
		s.newClient = newClient
	}
	if newFS != nil {
		s.newFS = newFS
	}
	return s
}

func (s *Service) Settings(ctx context.Context) SettingsResponse {
	return s.settingsResponse(ctx, s.effectiveConfig())
}

func (s *Service) SaveSettings(ctx context.Context, req SettingsRequest) (SettingsResponse, error) {
	appCfg := s.cfgMgr.Get()
	cfg := s.normalizeConfig(appCfg.S3WebDAV)
	if req.Enabled != nil {
		cfg.Enabled = *req.Enabled
	}
	if key := normalizeMountKey(req.ActiveKey); key != "" {
		if _, ok := mountByKey(cfg.Mounts, key); !ok {
			return SettingsResponse{}, fmt.Errorf("S3 mount %q was not found", key)
		}
		cfg.ActiveKey = key
	}
	if strings.TrimSpace(req.WebDAVAccessMode) != "" {
		switch strings.TrimSpace(req.WebDAVAccessMode) {
		case config.S3WebDAVAccessModeMain, config.S3WebDAVAccessModeDedicated:
			cfg.WebDAVAccessMode = strings.TrimSpace(req.WebDAVAccessMode)
		default:
			return SettingsResponse{}, fmt.Errorf("WebDAV access mode must be %q or %q", config.S3WebDAVAccessModeMain, config.S3WebDAVAccessModeDedicated)
		}
	}
	if req.DedicatedBindHost != nil {
		cfg.DedicatedBindHost = strings.TrimSpace(*req.DedicatedBindHost)
	}
	if req.DedicatedPort != nil {
		if *req.DedicatedPort < 1 || *req.DedicatedPort > 65535 {
			return SettingsResponse{}, fmt.Errorf("dedicated WebDAV port must be between 1 and 65535")
		}
		cfg.DedicatedPort = *req.DedicatedPort
	}
	if req.DedicatedAutoStart != nil {
		cfg.DedicatedAutoStart = *req.DedicatedAutoStart
	}
	if strings.TrimSpace(req.DedicatedDomainMode) != "" {
		switch strings.TrimSpace(req.DedicatedDomainMode) {
		case config.S3WebDAVDomainModeNone, config.S3WebDAVDomainModeCustom, config.S3WebDAVDomainModeTunnel:
			cfg.DedicatedDomainMode = strings.TrimSpace(req.DedicatedDomainMode)
		default:
			return SettingsResponse{}, fmt.Errorf("dedicated WebDAV domain mode must be %q, %q, or %q", config.S3WebDAVDomainModeNone, config.S3WebDAVDomainModeCustom, config.S3WebDAVDomainModeTunnel)
		}
	}
	if req.DedicatedCustomDomain != nil {
		customDomain, err := normalizePublicBaseURL(*req.DedicatedCustomDomain)
		if err != nil {
			return SettingsResponse{}, err
		}
		cfg.DedicatedCustomDomain = customDomain
	}
	if req.DedicatedTunnelHostname != nil {
		cfg.DedicatedTunnelHostname = normalizeHostname(*req.DedicatedTunnelHostname)
	}
	appCfg.S3WebDAV = cfg
	if err := s.cfgMgr.Save(appCfg); err != nil {
		return SettingsResponse{}, err
	}
	return s.Settings(ctx), nil
}

func (s *Service) CreateMount(ctx context.Context, req MountRequest) (SettingsResponse, error) {
	appCfg := s.cfgMgr.Get()
	cfg := s.normalizeConfig(appCfg.S3WebDAV)
	current := config.DefaultS3WebDAVMountConfig()
	current.Key = uniqueMountKey(cfg.Mounts, suggestedMountKey(req))
	current.Name = strings.TrimSpace(req.Name)
	current.Enabled = true
	current.WebDAVEnabled = true
	current.WebDAVAuthEnabled = true
	mount, err := s.requestMount(req, current, true)
	if err != nil {
		return SettingsResponse{}, err
	}
	mount.Key = uniqueMountKey(cfg.Mounts, mount.Key)
	cfg.Mounts = append(cfg.Mounts, mount)
	cfg.ActiveKey = mount.Key
	if err := validateMounts(cfg.Mounts); err != nil {
		return SettingsResponse{}, err
	}
	appCfg.S3WebDAV = cfg
	if err := s.cfgMgr.Save(appCfg); err != nil {
		return SettingsResponse{}, err
	}
	return s.Settings(ctx), nil
}

func (s *Service) SaveMount(ctx context.Context, key string, req MountRequest) (SettingsResponse, error) {
	appCfg := s.cfgMgr.Get()
	cfg := s.normalizeConfig(appCfg.S3WebDAV)
	key = normalizeMountKey(key)
	idx := mountIndex(cfg.Mounts, key)
	if idx < 0 {
		return SettingsResponse{}, fmt.Errorf("S3 mount %q was not found", key)
	}
	mount, err := s.requestMount(req, cfg.Mounts[idx], false)
	if err != nil {
		return SettingsResponse{}, err
	}
	mount.Key = key
	cfg.Mounts[idx] = mount
	cfg.ActiveKey = key
	if err := validateMounts(cfg.Mounts); err != nil {
		return SettingsResponse{}, err
	}
	appCfg.S3WebDAV = cfg
	if err := s.cfgMgr.Save(appCfg); err != nil {
		return SettingsResponse{}, err
	}
	return s.Settings(ctx), nil
}

func (s *Service) DeleteMount(ctx context.Context, key string) (SettingsResponse, error) {
	appCfg := s.cfgMgr.Get()
	cfg := s.normalizeConfig(appCfg.S3WebDAV)
	key = normalizeMountKey(key)
	idx := mountIndex(cfg.Mounts, key)
	if idx < 0 {
		return SettingsResponse{}, fmt.Errorf("S3 mount %q was not found", key)
	}
	cfg.Mounts = append(cfg.Mounts[:idx], cfg.Mounts[idx+1:]...)
	if len(cfg.Mounts) == 0 {
		cfg.Mounts = []config.S3WebDAVMountConfig{config.DefaultS3WebDAVMountConfig()}
	}
	if cfg.ActiveKey == key {
		cfg.ActiveKey = cfg.Mounts[0].Key
	}
	appCfg.S3WebDAV = cfg
	if err := s.cfgMgr.Save(appCfg); err != nil {
		return SettingsResponse{}, err
	}
	return s.Settings(ctx), nil
}

func (s *Service) TestConnection(ctx context.Context, key string, req MountRequest) (TestConnectionResponse, error) {
	cfg := s.effectiveConfig()
	current, ok := s.mountForKey(cfg, key)
	if !ok {
		current = config.DefaultS3WebDAVMountConfig()
	}
	mount, err := s.requestMount(req, current, key == "")
	if err != nil {
		return TestConnectionResponse{}, err
	}
	availability := s.Availability(ctx, mount)
	if !availability.CanEnable {
		return TestConnectionResponse{
			Success:      false,
			Message:      availability.Message,
			Availability: availability,
		}, nil
	}
	fs, err := s.filesystemForMount(ctx, mount, false)
	if err != nil {
		return TestConnectionResponse{
			Success: false,
			Message: "S3 connection failed: " + err.Error(),
			Availability: Availability{
				CanEnable: false,
				Status:    StatusS3FilesystemUnavailable,
				Message:   "S3 connection failed.",
			},
		}, nil
	}
	if _, err := listFiles(fs, "/"); err != nil {
		return TestConnectionResponse{
			Success: false,
			Message: "S3 list failed: " + err.Error(),
			Availability: Availability{
				CanEnable: false,
				Status:    StatusS3FilesystemUnavailable,
				Message:   "S3 list failed.",
			},
		}, nil
	}
	return TestConnectionResponse{
		Success: true,
		Message: "S3 connection works.",
		Availability: Availability{
			CanEnable: true,
			Status:    StatusReady,
			Message:   "S3 WebDAV is ready.",
		},
	}, nil
}

func (s *Service) TestWebDAVConnection(ctx context.Context, key string, req MountRequest) (TestConnectionResponse, error) {
	cfg := s.effectiveConfig()
	current, ok := s.mountForKey(cfg, key)
	if !ok {
		current = config.DefaultS3WebDAVMountConfig()
	}
	mount, err := s.requestMount(req, current, key == "")
	if err != nil {
		return TestConnectionResponse{}, err
	}
	availability := s.WebDAVAvailability(ctx, mount)
	if !availability.CanEnable {
		return TestConnectionResponse{
			Success:      false,
			Message:      availability.Message,
			Availability: availability,
		}, nil
	}
	fs, err := s.filesystemForMount(ctx, mount, false)
	if err != nil {
		return TestConnectionResponse{
			Success: false,
			Message: "WebDAV filesystem failed: " + err.Error(),
			Availability: Availability{
				CanEnable: false,
				Status:    StatusS3FilesystemUnavailable,
				Message:   "WebDAV filesystem failed.",
			},
		}, nil
	}
	if _, err := listFiles(fs, "/"); err != nil {
		return TestConnectionResponse{
			Success: false,
			Message: "WebDAV list failed: " + err.Error(),
			Availability: Availability{
				CanEnable: false,
				Status:    StatusS3FilesystemUnavailable,
				Message:   "WebDAV list failed.",
			},
		}, nil
	}
	return TestConnectionResponse{
		Success: true,
		Message: "WebDAV endpoint works.",
		Availability: Availability{
			CanEnable: true,
			Status:    StatusReady,
			Message:   "S3 WebDAV is ready.",
		},
	}, nil
}

func (s *Service) FeatureAvailability(_ context.Context, _ config.S3WebDAVConfig) Availability {
	return Availability{CanEnable: true, Status: StatusReady, Message: "S3 WebDAV can be enabled."}
}

func (s *Service) Availability(_ context.Context, mount config.S3WebDAVMountConfig) Availability {
	mount = s.normalizeMount(mount)
	if !mount.Enabled {
		return availability(StatusS3ConfigurationIncomplete, "This S3 mount is disabled.", nil)
	}
	if _, err := NormalizeMountPath(mount.MountPath); err != nil {
		return availability(StatusMountPathInvalid, err.Error(), nil)
	}
	if _, err := NormalizeRootPrefix(mount.RootPrefix); err != nil {
		return availability(StatusMountPathInvalid, err.Error(), nil)
	}
	if strings.TrimSpace(mount.EndpointURL) == "" {
		return availability(StatusEndpointRequired, "S3 endpoint is required.", nil)
	}
	if strings.TrimSpace(mount.BucketName) == "" {
		return availability(StatusBucketRequired, "Bucket name is required.", nil)
	}
	if strings.TrimSpace(mount.AccessKeyID) == "" || strings.TrimSpace(mount.SecretAccessKey) == "" {
		return availability(StatusCredentialsRequired, "S3 Access Key ID and Secret Access Key are required.", nil)
	}
	return Availability{CanEnable: true, Status: StatusReady, Message: "S3 storage is ready."}
}

func (s *Service) WebDAVAvailability(ctx context.Context, mount config.S3WebDAVMountConfig) Availability {
	s3Availability := s.Availability(ctx, mount)
	if !s3Availability.CanEnable {
		return s3Availability
	}
	mount = s.normalizeMount(mount)
	if !mount.WebDAVEnabled {
		return availability(StatusWebDAVDisabled, "WebDAV endpoint is disabled.", nil)
	}
	if !mount.WebDAVAuthEnabled {
		return Availability{CanEnable: true, Status: StatusWebDAVAuthDisabled, Message: "WebDAV endpoint is enabled without authentication."}
	}
	if strings.TrimSpace(mount.WebDAVUsername) == "" || strings.TrimSpace(mount.WebDAVPasswordHash) == "" {
		return availability(StatusWebDAVCredentialsRequired, "Set a WebDAV username and password, or disable WebDAV authentication.", nil)
	}
	return Availability{CanEnable: true, Status: StatusReady, Message: "S3 WebDAV is ready."}
}

func (s *Service) R2Management(ctx context.Context, mount config.S3WebDAVMountConfig) R2Management {
	mount = s.normalizeMount(mount)
	if mount.Provider != ProviderCloudflareR2 {
		return R2Management{Enabled: false, Status: "DISABLED", Message: "R2 bucket management is only available in Cloudflare R2 mode."}
	}
	if strings.TrimSpace(mount.AccountID) == "" {
		return R2Management{Enabled: false, Status: "ACCOUNT_ID_REQUIRED", Message: "Cloudflare Account ID is required to manage R2 buckets."}
	}
	token := strings.TrimSpace(s.cfgMgr.Get().EffectiveTunnelManagement().APIToken)
	if token == "" {
		return R2Management{Enabled: false, Status: "API_TOKEN_REQUIRED", Message: "Cloudflare API Token is required only for listing or creating R2 buckets."}
	}
	client, err := s.newClient(token)
	if err != nil {
		return R2Management{Enabled: false, Status: "API_TOKEN_INVALID", Message: "Cloudflare API Token could not be used for R2 bucket management."}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := client.ListR2Buckets(ctx, cloudflare.AccountIdentifier(mount.AccountID), cloudflare.ListR2BucketsParams{PerPage: 1}); err != nil {
		return R2Management{Enabled: false, Status: "PERMISSION_DENIED", Message: "Cloudflare API Token cannot list R2 buckets. Add Developer Platform Workers R2 Storage Edit if you want bucket management."}
	}
	return R2Management{Enabled: true, Status: "READY", Message: "R2 bucket management is available."}
}

func (s *Service) ListBuckets(ctx context.Context, key string) (BucketsResponse, error) {
	return s.ListBucketsFor(ctx, BucketRequest{MountKey: key})
}

func (s *Service) ListBucketsFor(ctx context.Context, req BucketRequest) (BucketsResponse, error) {
	accountID, client, err := s.r2BucketClient(req.MountKey, req.AccountID, req.Jurisdiction)
	if err != nil {
		return BucketsResponse{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rows, err := client.ListR2Buckets(ctx, cloudflare.AccountIdentifier(accountID), cloudflare.ListR2BucketsParams{})
	if err != nil {
		return BucketsResponse{}, err
	}
	buckets := make([]Bucket, 0, len(rows))
	for _, row := range rows {
		buckets = append(buckets, Bucket{Name: row.Name, CreationDate: row.CreationDate, Location: row.Location})
	}
	return BucketsResponse{Buckets: buckets}, nil
}

func (s *Service) CreateBucket(ctx context.Context, req CreateBucketRequest) (Bucket, error) {
	accountID, client, err := s.r2BucketClient(req.MountKey, req.AccountID, req.Jurisdiction)
	if err != nil {
		return Bucket{}, err
	}
	name := strings.TrimSpace(req.Name)
	if err := validateBucketName(name); err != nil {
		return Bucket{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	row, err := client.CreateR2Bucket(ctx, cloudflare.AccountIdentifier(accountID), cloudflare.CreateR2BucketParameters{
		Name:         name,
		LocationHint: strings.TrimSpace(req.LocationHint),
	})
	if err != nil {
		return Bucket{}, err
	}
	return Bucket{Name: row.Name, CreationDate: row.CreationDate, Location: row.Location}, nil
}

func (s *Service) r2BucketClient(mountKey, accountID, jurisdiction string) (string, CloudflareClient, error) {
	accountID = strings.TrimSpace(accountID)
	if strings.TrimSpace(mountKey) != "" {
		mount, err := s.requireMount(mountKey)
		if err != nil {
			return "", nil, err
		}
		if mount.Provider != ProviderCloudflareR2 {
			return "", nil, fmt.Errorf("bucket management is only available in Cloudflare R2 mode")
		}
		if accountID == "" {
			accountID = mount.AccountID
		}
	} else if accountID == "" {
		accountID = s.defaultCloudflareAccountID()
	}
	_ = normalizeJurisdiction(jurisdiction)
	if strings.TrimSpace(accountID) == "" {
		return "", nil, fmt.Errorf("account id is required")
	}
	token := strings.TrimSpace(s.cfgMgr.Get().EffectiveTunnelManagement().APIToken)
	if token == "" {
		return "", nil, fmt.Errorf("Cloudflare API Token is required for R2 bucket management")
	}
	client, err := s.newClient(token)
	if err != nil {
		return "", nil, err
	}
	return strings.TrimSpace(accountID), client, nil
}

func (s *Service) ListFiles(ctx context.Context, key, rawPath string) (FilesResponse, error) {
	fs, err := s.Filesystem(ctx, key)
	if err != nil {
		return FilesResponse{}, err
	}
	return listFiles(fs, rawPath)
}

func (s *Service) WriteFile(ctx context.Context, key, rawPath string, body io.Reader) error {
	fs, err := s.Filesystem(ctx, key)
	if err != nil {
		return err
	}
	return writeFile(fs, rawPath, body)
}

func (s *Service) OpenFile(ctx context.Context, key, rawPath string) (afero.File, os.FileInfo, error) {
	fs, err := s.Filesystem(ctx, key)
	if err != nil {
		return nil, nil, err
	}
	file, info, err := openFile(fs, rawPath)
	if err != nil {
		return nil, nil, err
	}
	return file, info, nil
}

func (s *Service) Delete(ctx context.Context, key, rawPath string) error {
	fs, err := s.Filesystem(ctx, key)
	if err != nil {
		return err
	}
	return deletePath(fs, rawPath)
}

func (s *Service) Mkdir(ctx context.Context, key, rawPath string) error {
	fs, err := s.Filesystem(ctx, key)
	if err != nil {
		return err
	}
	return mkdir(fs, rawPath)
}

func (s *Service) Rename(ctx context.Context, key, from, to string) error {
	fs, err := s.Filesystem(ctx, key)
	if err != nil {
		return err
	}
	return renamePath(fs, from, to)
}

func (s *Service) Filesystem(ctx context.Context, key string) (afero.Fs, error) {
	mount, err := s.requireMount(key)
	if err != nil {
		return nil, err
	}
	return s.filesystemForMount(ctx, mount, true)
}

func (s *Service) filesystemForMount(ctx context.Context, mount config.S3WebDAVMountConfig, requireEnabled bool) (afero.Fs, error) {
	mount = s.normalizeMount(mount)
	if requireEnabled && !s.effectiveConfig().Enabled {
		return nil, fmt.Errorf("S3 WebDAV is disabled")
	}
	if requireEnabled && !mount.Enabled {
		return nil, fmt.Errorf("S3 mount is disabled")
	}
	if strings.TrimSpace(mount.EndpointURL) == "" {
		return nil, fmt.Errorf("S3 endpoint is required")
	}
	if strings.TrimSpace(mount.BucketName) == "" {
		return nil, fmt.Errorf("bucket name is required")
	}
	if strings.TrimSpace(mount.AccessKeyID) == "" || strings.TrimSpace(mount.SecretAccessKey) == "" {
		return nil, fmt.Errorf("S3 credentials are required")
	}
	if _, err := NormalizeMountPath(mount.MountPath); err != nil {
		return nil, err
	}
	if _, err := NormalizeRootPrefix(mount.RootPrefix); err != nil {
		return nil, err
	}
	return s.newFS(ctx, FSConfig{
		BucketName: mount.BucketName,
		Endpoint:   mount.EndpointURL,
		Region:     mount.Region,
		PathStyle:  mount.PathStyle,
		RootPrefix: mount.RootPrefix,
	}, Credentials{
		AccessKeyID:     mount.AccessKeyID,
		SecretAccessKey: mount.SecretAccessKey,
	})
}

func (s *Service) WebDAVMountForPath(requestPath string) (config.S3WebDAVMountConfig, bool) {
	cfg := s.effectiveConfig()
	if !cfg.Enabled {
		return config.S3WebDAVMountConfig{}, false
	}
	mounts := append([]config.S3WebDAVMountConfig(nil), cfg.Mounts...)
	sort.SliceStable(mounts, func(i, j int) bool {
		return len(mounts[i].MountPath) > len(mounts[j].MountPath)
	})
	for _, mount := range mounts {
		mount = s.normalizeMount(mount)
		if !mount.Enabled || !mount.WebDAVEnabled {
			continue
		}
		if mount.WebDAVAuthEnabled && (strings.TrimSpace(mount.WebDAVUsername) == "" || strings.TrimSpace(mount.WebDAVPasswordHash) == "") {
			continue
		}
		mountPath, err := NormalizeMountPath(mount.MountPath)
		if err != nil {
			continue
		}
		if strings.HasPrefix(requestPath, mountPath) {
			mount.MountPath = mountPath
			return mount, true
		}
	}
	return config.S3WebDAVMountConfig{}, false
}

func (s *Service) effectiveConfig() config.S3WebDAVConfig {
	return s.normalizeConfig(s.cfgMgr.Get().S3WebDAV)
}

func (s *Service) requireMount(key string) (config.S3WebDAVMountConfig, error) {
	cfg := s.effectiveConfig()
	mount, ok := s.mountForKey(cfg, key)
	if !ok {
		return config.S3WebDAVMountConfig{}, fmt.Errorf("S3 mount %q was not found", key)
	}
	return mount, nil
}

func (s *Service) mountForKey(cfg config.S3WebDAVConfig, key string) (config.S3WebDAVMountConfig, bool) {
	key = normalizeMountKey(key)
	if key == "" {
		key = cfg.ActiveKey
	}
	mount, ok := mountByKey(cfg.Mounts, key)
	return s.normalizeMount(mount), ok
}

func (s *Service) requestMount(req MountRequest, current config.S3WebDAVMountConfig, create bool) (config.S3WebDAVMountConfig, error) {
	next := current
	if req.Key != "" {
		next.Key = normalizeMountKey(req.Key)
	}
	if strings.TrimSpace(req.Name) != "" || create {
		next.Name = strings.TrimSpace(req.Name)
	}
	if req.Enabled != nil {
		next.Enabled = *req.Enabled
	} else if create {
		next.Enabled = true
	}
	if req.WebDAVEnabled != nil {
		next.WebDAVEnabled = *req.WebDAVEnabled
	} else if create {
		next.WebDAVEnabled = true
	}
	if req.WebDAVAuthEnabled != nil {
		next.WebDAVAuthEnabled = *req.WebDAVAuthEnabled
	} else if create {
		next.WebDAVAuthEnabled = true
	}
	next.Provider = normalizeProvider(req.Provider)
	next.AccountID = strings.TrimSpace(req.AccountID)
	if next.AccountID == "" && next.Provider == ProviderCloudflareR2 {
		next.AccountID = s.defaultCloudflareAccountID()
	}
	next.Jurisdiction = normalizeJurisdiction(req.Jurisdiction)
	next.EndpointURL = strings.TrimSpace(req.EndpointURL)
	if next.EndpointURL == "" && next.Provider == ProviderCloudflareR2 && next.AccountID != "" {
		next.EndpointURL = endpointFor(next.AccountID, next.Jurisdiction)
	}
	next.Region = normalizeRegion(req.Region)
	next.PathStyle = req.PathStyle
	next.BucketName = strings.TrimSpace(req.BucketName)

	rootPrefix, err := NormalizeRootPrefix(req.RootPrefix)
	if err != nil {
		return config.S3WebDAVMountConfig{}, err
	}
	next.RootPrefix = rootPrefix
	mountPath, err := NormalizeMountPath(req.MountPath)
	if err != nil {
		return config.S3WebDAVMountConfig{}, err
	}
	next.MountPath = mountPath

	next.AccessKeyID = strings.TrimSpace(req.AccessKeyID)
	if strings.TrimSpace(req.SecretAccessKey) != "" {
		next.SecretAccessKey = strings.TrimSpace(req.SecretAccessKey)
	}
	next.WebDAVUsername = strings.TrimSpace(req.WebDAVUsername)
	if strings.TrimSpace(req.WebDAVPassword) != "" {
		hash, err := HashPassword(req.WebDAVPassword)
		if err != nil {
			return config.S3WebDAVMountConfig{}, err
		}
		next.WebDAVPasswordHash = hash
	}
	return s.normalizeMount(next), nil
}

func (s *Service) normalizeConfig(cfg config.S3WebDAVConfig) config.S3WebDAVConfig {
	cfg.WebDAVAccessMode = normalizeAccessMode(cfg.WebDAVAccessMode)
	cfg.DedicatedBindHost = strings.TrimSpace(cfg.DedicatedBindHost)
	if cfg.DedicatedPort <= 0 {
		cfg.DedicatedPort = 14334
	}
	cfg.DedicatedDomainMode = normalizeDomainMode(cfg.DedicatedDomainMode)
	if customDomain, err := normalizePublicBaseURL(cfg.DedicatedCustomDomain); err == nil {
		cfg.DedicatedCustomDomain = customDomain
	} else {
		cfg.DedicatedCustomDomain = strings.TrimSpace(cfg.DedicatedCustomDomain)
	}
	cfg.DedicatedTunnelHostname = normalizeHostname(cfg.DedicatedTunnelHostname)
	if len(cfg.Mounts) == 0 {
		cfg.Mounts = []config.S3WebDAVMountConfig{config.DefaultS3WebDAVMountConfig()}
	}
	for i := range cfg.Mounts {
		cfg.Mounts[i] = s.normalizeMount(cfg.Mounts[i])
		if cfg.Mounts[i].Key == "" {
			cfg.Mounts[i].Key = "mount-" + strconv.Itoa(i+1)
		}
	}
	if cfg.ActiveKey == "" || mountIndex(cfg.Mounts, cfg.ActiveKey) < 0 {
		cfg.ActiveKey = cfg.Mounts[0].Key
	}
	return cfg
}

func normalizeAccessMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case config.S3WebDAVAccessModeDedicated:
		return config.S3WebDAVAccessModeDedicated
	default:
		return config.S3WebDAVAccessModeMain
	}
}

func normalizeDomainMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case config.S3WebDAVDomainModeCustom:
		return config.S3WebDAVDomainModeCustom
	case config.S3WebDAVDomainModeTunnel:
		return config.S3WebDAVDomainModeTunnel
	default:
		return config.S3WebDAVDomainModeNone
	}
}

func normalizePublicBaseURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("dedicated WebDAV custom domain must be a full http:// or https:// URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("dedicated WebDAV custom domain must use http:// or https://")
	}
	u.Path = strings.TrimRight(u.EscapedPath(), "/")
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func normalizeHostname(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if u, err := url.Parse(value); err == nil && u.Host != "" {
		value = u.Host
	}
	value = strings.Trim(value, "/")
	if host, _, err := strings.Cut(value, ":"); err {
		value = host
	}
	return strings.ToLower(strings.TrimSpace(value))
}

func (s *Service) normalizeMount(mount config.S3WebDAVMountConfig) config.S3WebDAVMountConfig {
	mount.Key = normalizeMountKey(mount.Key)
	mount.Name = strings.TrimSpace(mount.Name)
	if mount.Name == "" {
		mount.Name = "S3 Mount"
	}
	mount.Provider = normalizeProvider(mount.Provider)
	mount.AccountID = strings.TrimSpace(mount.AccountID)
	if mount.AccountID == "" && mount.Provider == ProviderCloudflareR2 {
		mount.AccountID = s.defaultCloudflareAccountID()
	}
	mount.Jurisdiction = normalizeJurisdiction(mount.Jurisdiction)
	mount.EndpointURL = strings.TrimSpace(mount.EndpointURL)
	if mount.EndpointURL == "" && mount.Provider == ProviderCloudflareR2 && mount.AccountID != "" {
		mount.EndpointURL = endpointFor(mount.AccountID, mount.Jurisdiction)
	}
	mount.Region = normalizeRegion(mount.Region)
	mount.BucketName = strings.TrimSpace(mount.BucketName)
	if rootPrefix, err := NormalizeRootPrefix(mount.RootPrefix); err == nil {
		mount.RootPrefix = rootPrefix
	} else {
		mount.RootPrefix = strings.TrimSpace(mount.RootPrefix)
	}
	if mountPath, err := NormalizeMountPath(mount.MountPath); err == nil {
		mount.MountPath = mountPath
	} else {
		mount.MountPath = strings.TrimSpace(mount.MountPath)
	}
	mount.AccessKeyID = strings.TrimSpace(mount.AccessKeyID)
	mount.SecretAccessKey = strings.TrimSpace(mount.SecretAccessKey)
	mount.WebDAVUsername = strings.TrimSpace(mount.WebDAVUsername)
	return mount
}

func (s *Service) defaultCloudflareAccountID() string {
	cfg := s.cfgMgr.Get()
	if accountID := strings.TrimSpace(cfg.EffectiveTunnelManagement().AccountID); accountID != "" {
		return accountID
	}
	if identity, err := cfg.TunnelTokenIdentity(); err == nil {
		return strings.TrimSpace(identity.AccountID)
	}
	return ""
}

func (s *Service) settingsResponse(ctx context.Context, cfg config.S3WebDAVConfig) SettingsResponse {
	cfg = s.normalizeConfig(cfg)
	mounts := make([]MountResponse, 0, len(cfg.Mounts))
	for _, mount := range cfg.Mounts {
		mounts = append(mounts, s.mountResponse(ctx, mount))
	}
	return SettingsResponse{
		Enabled:                 cfg.Enabled,
		ActiveKey:               cfg.ActiveKey,
		WebDAVAccessMode:        cfg.WebDAVAccessMode,
		DedicatedBindHost:       cfg.DedicatedBindHost,
		DedicatedPort:           cfg.DedicatedPort,
		DedicatedAutoStart:      cfg.DedicatedAutoStart,
		DedicatedDomainMode:     cfg.DedicatedDomainMode,
		DedicatedCustomDomain:   cfg.DedicatedCustomDomain,
		DedicatedTunnelHostname: cfg.DedicatedTunnelHostname,
		Mounts:                  mounts,
		Availability:            Availability{CanEnable: true, Status: StatusReady, Message: "S3 WebDAV can be enabled."},
	}
}

func (s *Service) mountResponse(ctx context.Context, mount config.S3WebDAVMountConfig) MountResponse {
	availability := s.Availability(ctx, mount)
	webDAVAvailability := s.WebDAVAvailability(ctx, mount)
	return MountResponse{
		Key:                mount.Key,
		Name:               mount.Name,
		Enabled:            mount.Enabled,
		WebDAVEnabled:      mount.WebDAVEnabled,
		WebDAVAuthEnabled:  mount.WebDAVAuthEnabled,
		Provider:           mount.Provider,
		EndpointURL:        mount.EndpointURL,
		Region:             mount.Region,
		PathStyle:          mount.PathStyle,
		AccountID:          mount.AccountID,
		BucketName:         mount.BucketName,
		RootPrefix:         mount.RootPrefix,
		MountPath:          mount.MountPath,
		Jurisdiction:       mount.Jurisdiction,
		AccessKeyID:        mount.AccessKeyID,
		SecretAccessKeySet: strings.TrimSpace(mount.SecretAccessKey) != "",
		WebDAVUsername:     mount.WebDAVUsername,
		PasswordSet:        strings.TrimSpace(mount.WebDAVPasswordHash) != "",
		Endpoint:           mount.MountPath,
		R2BucketManagement: s.R2Management(ctx, mount),
		Availability:       availability,
		WebDAVAvailability: webDAVAvailability,
	}
}

func availability(status, message string, missing []string) Availability {
	return Availability{CanEnable: false, Status: status, Message: message, MissingPermissions: missing}
}

func normalizeMountKey(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	var b strings.Builder
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func suggestedMountKey(req MountRequest) string {
	if key := normalizeMountKey(req.Key); key != "" {
		return key
	}
	if key := normalizeMountKey(req.Name); key != "" {
		return key
	}
	if key := normalizeMountKey(strings.Trim(req.MountPath, "/")); key != "" {
		return key
	}
	return "s3"
}

func uniqueMountKey(mounts []config.S3WebDAVMountConfig, base string) string {
	base = normalizeMountKey(base)
	if base == "" {
		base = "s3"
	}
	key := base
	for i := 2; mountIndex(mounts, key) >= 0; i++ {
		key = base + "-" + strconv.Itoa(i)
	}
	return key
}

func mountIndex(mounts []config.S3WebDAVMountConfig, key string) int {
	key = normalizeMountKey(key)
	for i, mount := range mounts {
		if mount.Key == key {
			return i
		}
	}
	return -1
}

func mountByKey(mounts []config.S3WebDAVMountConfig, key string) (config.S3WebDAVMountConfig, bool) {
	idx := mountIndex(mounts, key)
	if idx < 0 {
		return config.S3WebDAVMountConfig{}, false
	}
	return mounts[idx], true
}

func validateMounts(mounts []config.S3WebDAVMountConfig) error {
	paths := make(map[string]string, len(mounts))
	for _, mount := range mounts {
		mountPath, err := NormalizeMountPath(mount.MountPath)
		if err != nil {
			return err
		}
		if prev, ok := paths[mountPath]; ok {
			return fmt.Errorf("WebDAV path %s is already used by %s", mountPath, prev)
		}
		paths[mountPath] = mount.Name
	}
	return nil
}
