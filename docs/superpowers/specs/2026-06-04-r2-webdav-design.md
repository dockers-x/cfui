# R2 WebDAV Design

Date: 2026-06-04

## Goal

Add an optional R2 WebDAV feature to cfui. When enabled, users can select or create a Cloudflare R2 bucket, configure a WebDAV username and password, browse/manage objects from the web UI, and access the bucket through a WebDAV endpoint exposed by this program.

## Non-goals

- Support Cloudflare Global API Key for R2 object access.
- Provide multi-user WebDAV accounts in the first version.
- Implement full POSIX filesystem semantics on top of R2.
- Add public unauthenticated object sharing.
- Support multipart upload resume from the web UI in the first version.

## Product Decisions

R2 WebDAV uses Cloudflare API Token authentication only. The feature will not support API Email + Global API Key because R2 object access through the S3-compatible API needs S3 credentials. For R2 API tokens, the service can derive S3 credentials from the token identity and token value:

- Access Key ID: API token ID returned by token verification.
- Secret Access Key: SHA-256 digest of the API token value.

The frontend must explain this clearly when the user configures R2. If the current credentials are in Global API Key mode, the R2 panel and feature availability should say that R2 WebDAV requires API Token mode.

## Current Project Fit

cfui already has a feature toggle endpoint at `GET/POST /api/features` and stores Cloudflare API token settings under `TunnelManagementConfig`. DDNS also depends on those API credentials. R2 WebDAV should follow that pattern but add stronger permission-gated feature availability, because the user explicitly wants feature switches constrained by API token permissions.

The feature should not disrupt existing local tunnel, remote tunnel manager, DDNS, or MCP behavior. Existing response fields in `/api/features` remain compatible; new R2 fields and availability details are additive.

## Backend Architecture

Add package `internal/r2dav` with these responsibilities:

- Build a Cloudflare API client from stored API token credentials.
- Verify R2 API token permissions and derive R2 S3 credentials.
- Manage buckets through `cloudflare-go`.
- Build an R2-backed `afero.Fs` using `github.com/fclairamb/afero-s3`.
- Expose a WebDAV handler backed by the same filesystem abstraction.
- Provide file operations for the web UI using the same filesystem abstraction.

Use these libraries:

- `github.com/cloudflare/cloudflare-go` for account-level R2 bucket list/create/get.
- `github.com/fclairamb/afero-s3` for S3-compatible object storage as `afero.Fs`.
- `github.com/spf13/afero` as the shared filesystem interface.
- `golang.org/x/net/webdav` for the WebDAV protocol endpoint.
- `golang.org/x/crypto/bcrypt` for WebDAV password hashing.

## Configuration

Add an R2 WebDAV config to `config.Config`:

```go
type R2WebDAVConfig struct {
    Enabled            bool   `json:"enabled"`
    AccountID          string `json:"account_id"`
    BucketName         string `json:"bucket_name"`
    Jurisdiction       string `json:"jurisdiction"`
    WebDAVUsername     string `json:"webdav_username"`
    WebDAVPasswordHash string `json:"-"`
}
```

`Jurisdiction` accepts `default`, `eu`, and `fedramp`; empty means `default`. The S3 endpoint is built from the account ID and jurisdiction:

- default: `https://<account_id>.r2.cloudflarestorage.com`
- eu: `https://<account_id>.eu.r2.cloudflarestorage.com`
- fedramp: `https://<account_id>.fedramp.r2.cloudflarestorage.com`

API responses must never include `WebDAVPasswordHash`. They return `password_set: true|false` instead.

Persist the new settings in SQLite using the existing structured config approach. A new Ent schema such as `R2WebDAVSetting` is acceptable, or the app settings schema can be expanded if that is cleaner. Keep secrets separate from non-secret display fields where practical.

## Feature Availability

Extend `/api/features` to include R2 state and availability:

```json
{
  "tunnel_manager": true,
  "ddns": false,
  "mcp": true,
  "r2_webdav": false,
  "availability": {
    "r2_webdav": {
      "can_enable": false,
      "status": "MISSING_R2_PERMISSION",
      "message": "API Token needs R2 read/write permission.",
      "missing_permissions": ["Workers R2 Storage Write"]
    }
  }
}
```

R2 can be enabled only when:

- API Token mode is configured.
- Account ID is available from R2 config or tunnel management settings.
- The token verifies as active.
- The token grants R2 read/write capability.
- A bucket is selected.
- A WebDAV username and password are configured.

The initial permission check should accept either broad account-level R2 write permission or bucket object write permission where Cloudflare reports it in token policy groups. The known group names to recognize are:

- `Workers R2 Storage Write`
- `Workers R2 Storage Read`
- `Workers R2 Storage Bucket Item Write`
- `Workers R2 Storage Bucket Item Read`

For write-capable WebDAV, write permission is required. Read-only tokens should fail feature enablement with an explicit message.

When token policy inspection is unavailable, fall back to safe probing:

- List buckets for read/admin capability.
- Get selected bucket metadata.
- Optionally attempt a clearly named zero-byte probe object only if the user is saving settings or enabling the feature, then delete it immediately. The probe path should be under a reserved prefix such as `.cfui-healthcheck/`.

## API Design

Add R2 settings endpoints:

- `GET /api/r2/settings`
- `POST /api/r2/settings`
- `GET /api/r2/buckets`
- `POST /api/r2/buckets`
- `GET /api/r2/files?path=<path>`
- `GET /api/r2/files/download?path=<path>`
- `PUT /api/r2/files/*`
- `DELETE /api/r2/files/*`
- `POST /api/r2/files/mkdir`
- `POST /api/r2/files/rename`

Settings response:

```json
{
  "enabled": false,
  "account_id": "abc123",
  "bucket_name": "my-bucket",
  "jurisdiction": "default",
  "webdav_username": "dav",
  "password_set": true,
  "endpoint": "/webdav/r2/",
  "availability": {
    "can_enable": true,
    "status": "READY",
    "message": "R2 WebDAV is ready."
  }
}
```

Settings request:

```json
{
  "enabled": true,
  "account_id": "abc123",
  "bucket_name": "my-bucket",
  "jurisdiction": "default",
  "webdav_username": "dav",
  "webdav_password": "new password or empty to keep existing"
}
```

Validation rules:

- Bucket names must match Cloudflare R2/S3 bucket naming constraints and be trimmed.
- Paths must be normalized to an object key inside the selected bucket.
- Reject `..`, repeated path traversal, leading drive letters, and empty object names where an object is required.
- Directory paths are represented as prefixes.
- API errors use existing cfui JSON error semantics.

## WebDAV Endpoint

Mount R2 WebDAV at:

```text
/webdav/r2/
```

The endpoint is available only when the feature is enabled. It always requires HTTP Basic Auth using the configured WebDAV username and password.

Supported behavior:

- `PROPFIND`: list prefix or object metadata.
- `GET`: stream object download.
- `PUT`: upload/replace object.
- `DELETE`: delete object or prefix contents.
- `MKCOL`: create a directory marker or ensure prefix exists.
- `MOVE`: rename through copy then delete.

Object storage limitations must be surfaced in the UI:

- Folders are simulated with prefixes.
- Rename is copy then delete and can be slow for large files.
- Append writes and random writes are not supported.
- Modification times may be object-store metadata rather than local filesystem timestamps.

## Web UI

Add an R2 WebDAV tab and a feature toggle row.

Feature row UX:

- Toggle is enabled only when availability says it can be enabled.
- When disabled, show a concise reason next to the toggle.
- Use clear messages for missing API token, Global API Key mode, missing R2 permissions, missing bucket, and missing WebDAV credentials.

R2 tab sections:

- Status: current feature state, permission state, and WebDAV endpoint.
- Cloudflare connection: account ID, jurisdiction, API Token mode requirement.
- Bucket: refresh list, select bucket, create bucket.
- WebDAV credentials: username, password, save button, password-set indicator.
- File manager: breadcrumb, upload, new folder, refresh, list rows, download, rename, delete.

Frontend copy should explain that R2 WebDAV requires an API token with R2 read/write permissions. If users are currently using Global API Key mode for tunnel management, show a blocking notice instead of allowing an ambiguous failure.

## Security

- Do not store WebDAV password plaintext.
- Hash WebDAV password with bcrypt.
- Never return saved API token, derived S3 secret, WebDAV password, or password hash in API responses.
- Never log `Authorization`, Cloudflare API token, S3 secret, or WebDAV password.
- Use constant-time comparison for Basic Auth username and bcrypt verification for password.
- Reject path traversal and normalize all paths before object operations.
- Set conservative upload size handling. Do not read large uploads fully into memory.
- Avoid creating or deleting buckets without an explicit user action.

## Error Handling

Use actionable, user-facing errors:

- `API_TOKEN_REQUIRED`: R2 WebDAV requires API Token mode.
- `ACCOUNT_ID_REQUIRED`: Account ID is required for R2.
- `R2_PERMISSION_DENIED`: API Token does not grant R2 read/write access.
- `BUCKET_REQUIRED`: Select or create an R2 bucket first.
- `WEBDAV_CREDENTIALS_REQUIRED`: Set WebDAV username and password first.
- `R2_BUCKET_NOT_FOUND`: Selected bucket no longer exists.
- `R2_OBJECT_NOT_FOUND`: File or folder was not found.

Cloudflare and S3 errors should be mapped to safe messages and status codes without leaking credentials or raw internal details.

## Testing

Backend unit tests:

- Feature toggle rejects R2 enablement when token mode is not API Token.
- Feature toggle rejects R2 enablement when permissions are missing.
- Settings save hashes WebDAV password and omits it from response.
- Settings save keeps existing password when password field is empty.
- Basic Auth rejects missing/wrong credentials.
- Path normalization rejects traversal.
- Bucket list/create calls use configured account ID.

Filesystem/service tests:

- Use an in-memory or fake `afero.Fs` for file API behavior.
- Test list, upload, download metadata, delete, mkdir, and rename behavior.
- Test WebDAV handler wiring separately from real Cloudflare calls.

Frontend/manual verification:

- Feature row shows availability reasons.
- R2 tab is hidden when disabled and visible when enabled.
- Bucket selection/create flow updates settings.
- WebDAV endpoint copy text matches the actual mount path.
- File manager handles empty bucket, upload progress, delete confirmation, and long object names.

## Implementation Sequence

1. Add config model and persistence for R2 WebDAV settings.
2. Add R2 permission availability service.
3. Extend `/api/features` with `r2_webdav` and availability.
4. Add R2 settings and bucket API.
5. Add `afero-s3` filesystem construction and WebDAV handler.
6. Add web UI settings and feature messaging.
7. Add web UI file manager API and UI.
8. Add tests and run full verification.

## Release Notes

This feature should ship as a new minor feature release because it adds dependencies, new persisted settings, new HTTP endpoints, and a user-visible R2 WebDAV integration.
