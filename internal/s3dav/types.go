package s3dav

import "time"

const DefaultMountPath = "/webdav/s3/"

const (
	ProviderGenericS3    = "generic_s3"
	ProviderCloudflareR2 = "cloudflare_r2"
)

const (
	StatusReady                     = "READY"
	StatusEndpointRequired          = "S3_ENDPOINT_REQUIRED"
	StatusCredentialsRequired       = "S3_CREDENTIALS_REQUIRED"
	StatusMountPathInvalid          = "S3_MOUNT_PATH_INVALID"
	StatusBucketRequired            = "BUCKET_REQUIRED"
	StatusWebDAVCredentialsRequired = "WEBDAV_CREDENTIALS_REQUIRED"
	StatusWebDAVDisabled            = "WEBDAV_DISABLED"
	StatusWebDAVAuthDisabled        = "WEBDAV_AUTH_DISABLED"
	StatusS3ConfigurationIncomplete = "S3_CONFIGURATION_INCOMPLETE"
	StatusS3FilesystemUnavailable   = "S3_FILESYSTEM_UNAVAILABLE"
)

type Availability struct {
	CanEnable          bool     `json:"can_enable"`
	Status             string   `json:"status"`
	Message            string   `json:"message"`
	MissingPermissions []string `json:"missing_permissions,omitempty"`
}

type SettingsRequest struct {
	Enabled   bool   `json:"enabled"`
	ActiveKey string `json:"active_key"`
}

type SettingsResponse struct {
	Enabled      bool            `json:"enabled"`
	ActiveKey    string          `json:"active_key"`
	Mounts       []MountResponse `json:"mounts"`
	Availability Availability    `json:"availability"`
}

type MountRequest struct {
	Key               string `json:"key"`
	Name              string `json:"name"`
	Enabled           *bool  `json:"enabled"`
	WebDAVEnabled     *bool  `json:"webdav_enabled"`
	WebDAVAuthEnabled *bool  `json:"webdav_auth_enabled"`
	Provider          string `json:"provider"`
	EndpointURL       string `json:"endpoint_url"`
	Region            string `json:"region"`
	PathStyle         bool   `json:"path_style"`
	AccountID         string `json:"account_id"`
	BucketName        string `json:"bucket_name"`
	RootPrefix        string `json:"root_prefix"`
	MountPath         string `json:"mount_path"`
	Jurisdiction      string `json:"jurisdiction"`
	AccessKeyID       string `json:"access_key_id"`
	SecretAccessKey   string `json:"secret_access_key"`
	WebDAVUsername    string `json:"webdav_username"`
	WebDAVPassword    string `json:"webdav_password"`
}

type MountResponse struct {
	Key                string       `json:"key"`
	Name               string       `json:"name"`
	Enabled            bool         `json:"enabled"`
	WebDAVEnabled      bool         `json:"webdav_enabled"`
	WebDAVAuthEnabled  bool         `json:"webdav_auth_enabled"`
	Provider           string       `json:"provider"`
	EndpointURL        string       `json:"endpoint_url"`
	Region             string       `json:"region"`
	PathStyle          bool         `json:"path_style"`
	AccountID          string       `json:"account_id"`
	BucketName         string       `json:"bucket_name"`
	RootPrefix         string       `json:"root_prefix"`
	MountPath          string       `json:"mount_path"`
	Jurisdiction       string       `json:"jurisdiction"`
	AccessKeyID        string       `json:"access_key_id"`
	SecretAccessKeySet bool         `json:"secret_access_key_set"`
	WebDAVUsername     string       `json:"webdav_username"`
	PasswordSet        bool         `json:"password_set"`
	Endpoint           string       `json:"endpoint"`
	R2BucketManagement R2Management `json:"r2_bucket_management"`
	Availability       Availability `json:"availability"`
	WebDAVAvailability Availability `json:"webdav_availability"`
}

type TestConnectionResponse struct {
	Success      bool         `json:"success"`
	Message      string       `json:"message"`
	Availability Availability `json:"availability"`
}

type R2Management struct {
	Enabled bool   `json:"enabled"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type BucketRequest struct {
	MountKey     string `json:"mount_key"`
	AccountID    string `json:"account_id"`
	Jurisdiction string `json:"jurisdiction"`
}

type Bucket struct {
	Name         string     `json:"name"`
	CreationDate *time.Time `json:"creation_date,omitempty"`
	Location     string     `json:"location,omitempty"`
}

type BucketsResponse struct {
	Buckets []Bucket `json:"buckets"`
}

type CreateBucketRequest struct {
	MountKey     string `json:"mount_key"`
	AccountID    string `json:"account_id"`
	Jurisdiction string `json:"jurisdiction"`
	Name         string `json:"name"`
	LocationHint string `json:"location_hint"`
}

type FileEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	IsDir   bool      `json:"is_dir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

type FilesResponse struct {
	Path    string      `json:"path"`
	Parent  string      `json:"parent"`
	Entries []FileEntry `json:"entries"`
}

type MkdirRequest struct {
	MountKey string `json:"mount_key"`
	Path     string `json:"path"`
}

type RenameRequest struct {
	MountKey string `json:"mount_key"`
	From     string `json:"from"`
	To       string `json:"to"`
}

type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
}

type FSConfig struct {
	BucketName string
	Endpoint   string
	Region     string
	PathStyle  bool
	RootPrefix string
}
