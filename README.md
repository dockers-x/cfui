# cfui

[中文说明](README.zh-CN.md) | English

cfui is a web control panel for Cloudflare Tunnel (`cloudflared`). It runs the local tunnel process, manages selected Cloudflare Tunnel ingress rules through the Cloudflare API, updates DNS records for DDNS use cases, exposes S3-compatible storage through WebDAV, and provides an MCP endpoint for AI clients.

The web UI is built into the binary. Configuration is stored in a local SQLite database under the data directory.

## Acknowledgements

- [LinuxDo Community](https://linux.do)

## Features

- **Local Cloudflare Tunnel runner**
  - Paste a Cloudflare Tunnel token and start or stop the local tunnel from the browser.
  - Configure auto-start, auto-restart, protocol, region, retries, graceful shutdown, metrics, post-quantum mode, edge IP version, edge bind address, TLS verification, and extra cloudflared arguments.
  - Show tunnel status, active protocol, last error, and version/build information.

- **Remote Tunnel Manager**
  - Optional feature for managing Cloudflare-hosted tunnel ingress configuration.
  - Load an existing tunnel by Account ID and Tunnel ID.
  - Add, edit, and delete public hostname rules with hostname, path, service type, service URL, host header, origin server name, and TLS verification options.
  - Decode Account ID and Tunnel ID from the local tunnel token when possible.
  - Verify Cloudflare API permissions before using Cloudflare-managed operations.

- **DDNS**
  - Optional feature that reuses Remote Tunnel Manager Cloudflare credentials.
  - Detect public IPv4 and IPv6 from configurable IP source URLs.
  - Create and update Cloudflare `A` and `AAAA` records.
  - Supports Cloudflare proxy mode, TTL, DNS comments, retry settings, manual sync, and recent sync history.

- **S3 WebDAV**
  - Mount one or more S3-compatible bucket paths as WebDAV endpoints.
  - Supports generic S3-compatible services and Cloudflare R2 presets.
  - Each mount can use its own endpoint, region, bucket, root prefix, mount path, Access Key ID, Secret Access Key, WebDAV username/password, and auth toggle.
  - Includes S3 connection testing and WebDAV connection testing.
  - Includes a browser file panel for listing, uploading, downloading, deleting, renaming, and creating folders.
  - WebDAV can be served from the main HTTP service or from a dedicated HTTP port. These modes are mutually exclusive.
  - Dedicated WebDAV mode supports manual start/stop, optional auto-start, direct-port endpoints, custom public URLs, and Cloudflare Tunnel rule handoff.
  - Browser `GET` requests on a WebDAV path show a read-only file listing or file download. WebDAV methods such as `PROPFIND`, `PUT`, `DELETE`, `MKCOL`, `MOVE`, `COPY`, `LOCK`, and `UNLOCK` keep WebDAV behavior.

- **MCP access**
  - Optional Model Context Protocol endpoint at `/mcp`.
  - Uses bearer tokens generated in the UI.
  - Exposes tools for tunnel status/configuration, tunnel start/stop, recent logs, Remote Tunnel Manager operations, and DDNS operations when DDNS is enabled.

- **Logs and operations**
  - Recent logs API and optional real-time log streaming in the UI.
  - Log filtering, copying, downloading, and clearing from the browser.
  - Panic recovery and request logging middleware.

- **Feature switches**
  - Remote Tunnel Manager, DDNS, MCP, and S3 WebDAV are optional tabs.
  - Disabled features stay hidden in the UI.
  - DDNS depends on Remote Tunnel Manager because it reuses the same Cloudflare API credentials.

- **Internationalized UI**
  - English, Chinese, and Japanese UI translations are included.

## Quick Start

### Docker

```bash
docker run -d \
  --name cfui \
  -p 14333:14333 \
  -v cfui-data:/app/data \
  -v cfui-logs:/app/logs \
  --restart unless-stopped \
  ghcr.io/dockers-x/cfui:latest
```

Open:

```text
http://localhost:14333
```

Docker Hub image:

```bash
docker run -d \
  --name cfui \
  -p 14333:14333 \
  -v cfui-data:/app/data \
  -v cfui-logs:/app/logs \
  --restart unless-stopped \
  czyt/cfui:latest
```

### Docker Compose

```yaml
services:
  cfui:
    image: ghcr.io/dockers-x/cfui:latest
    container_name: cfui
    restart: unless-stopped
    ports:
      - "14333:14333"
      # Optional: expose this if you use dedicated S3 WebDAV mode.
      # - "14334:14334"
    volumes:
      - cfui-data:/app/data
      - cfui-logs:/app/logs
    environment:
      BIND_HOST: 0.0.0.0
      PORT: 14333
      TZ: UTC
      # Optional: inject Cloudflare API credentials without saving them in the UI.
      # CFUI_TUNNEL_MGMT_ENABLED: "true"
      # CLOUDFLARE_ACCOUNT_ID: your-account-id
      # CLOUDFLARE_TUNNEL_ID: your-tunnel-id
      # CLOUDFLARE_API_TOKEN: your-api-token
    healthcheck:
      test: ["CMD", "sh", "-c", "wget --no-verbose --tries=1 --spider http://localhost:$$PORT/ || exit 1"]
      interval: 30s
      timeout: 3s
      start_period: 5s
      retries: 3

volumes:
  cfui-data:
  cfui-logs:
```

### Binary

Download a release binary, then run:

```bash
chmod +x cfui
./cfui
```

Open:

```text
http://localhost:14333
```

## First Setup

1. Create or select a Cloudflare Tunnel in Cloudflare Zero Trust.
2. Copy the tunnel token from the tunnel install command.
3. Paste the token into the Tunnel Configuration page.
4. Save the configuration.
5. Start the tunnel from the UI.

The local tunnel runner does not require Cloudflare API credentials. API credentials are only needed for Remote Tunnel Manager, DDNS, and optional R2 bucket management.

## Cloudflare API Permissions

Remote Tunnel Manager and DDNS use Cloudflare API credentials. An API Token is recommended.

Required permissions for Remote Tunnel Manager and DDNS:

- `Account -> Argo Tunnel (Legacy) -> Edit`
- `Zone -> Zone -> Read`
- `Zone -> DNS -> Edit`

Optional permission for R2 bucket list/create:

- `Account -> Workers R2 Storage -> Edit`

S3 WebDAV file access does not use the Cloudflare API token. It uses the S3-compatible Access Key ID and Secret Access Key configured on the S3 WebDAV page.

## S3 WebDAV

S3 WebDAV is a feature-gated module. Enable it from the Features page, then create a mount from the S3 WebDAV page.

### Generic S3

Use Generic S3 for most S3-compatible providers. cfui does not manage buckets in this mode. Provide:

- Endpoint URL
- Region
- Bucket name
- Access Key ID
- Secret Access Key
- Path-style setting if your provider requires it
- Optional root prefix
- WebDAV mount path, for example `/webdav/my_s3/`

### Cloudflare R2

Use Cloudflare R2 when you want R2 endpoint presets or optional bucket list/create from cfui.

For file access, create an R2 API token from the R2 console and copy:

- Access Key ID
- Secret Access Key
- S3 endpoint, for example `https://<ACCOUNT_ID>.r2.cloudflarestorage.com`

For optional bucket list/create, configure Remote Tunnel Manager with a Cloudflare API token that has `Workers R2 Storage -> Edit`.

### WebDAV Endpoint

If a mount path is `/webdav/datasync/`, the WebDAV endpoint is:

```text
https://your-cfui-host/webdav/datasync/
```

File example:

```bash
curl -u 'user:password' \
  -o db.sql \
  'https://your-cfui-host/webdav/datasync/path/to/db.sql'
```

Check a WebDAV object:

```bash
curl -u 'user:password' \
  -X PROPFIND \
  -H 'Depth: 0' \
  'https://your-cfui-host/webdav/datasync/path/to/db.sql'
```

Range download:

```bash
curl -u 'user:password' \
  -H 'Range: bytes=0-1023' \
  -o part.bin \
  'https://your-cfui-host/webdav/datasync/path/to/db.sql'
```

## MCP

Enable MCP from the Features page, then create a bearer token from the MCP page. Connect MCP clients to:

```text
/mcp
```

The MCP token is shown only when it is created. Saved tokens are listed in masked form.

## Environment Variables

| Variable | Description | Default |
| --- | --- | --- |
| `BIND_HOST` | HTTP server bind address | `0.0.0.0` |
| `PORT` | Main HTTP server port | `14333` |
| `DATA_DIR` | Data directory | `./data` |
| `LOG_DIR` | Log directory | `${DATA_DIR}/logs` |
| `LOG_LEVEL` | `debug`, `info`, `warn`, `error` | `info` |
| `CFUI_TUNNEL_MGMT_ENABLED` / `CFUI_TUNNEL_MANAGEMENT_ENABLED` | Enable Remote Tunnel Manager | unset |
| `CFUI_TUNNEL_ACCOUNT_ID` / `CLOUDFLARE_ACCOUNT_ID` / `CLOUDFLARE_APP_ID` | Cloudflare account ID | unset |
| `CFUI_TUNNEL_ID` / `CLOUDFLARE_TUNNEL_ID` | Cloudflare tunnel ID | unset |
| `CFUI_TUNNEL_API_TOKEN` / `CLOUDFLARE_API_TOKEN` | Cloudflare API token | unset |
| `CFUI_TUNNEL_API_EMAIL` / `CLOUDFLARE_API_EMAIL` | Cloudflare account email for global API key auth | unset |
| `CFUI_TUNNEL_API_KEY` / `CLOUDFLARE_API_KEY` | Cloudflare global API key | unset |

Environment-provided Cloudflare credentials override saved UI values at runtime.

## Data and Migration

cfui stores configuration in SQLite:

```text
${DATA_DIR}/data.db
```

Logs are stored under:

```text
${LOG_DIR}
```

Old `config.json` and legacy `app_configs` database data are migrated into structured SQLite tables automatically. A migrated `config.json` is renamed to `config.json.migrated`.

## API Overview

Main endpoints:

- `GET /api/status`
- `POST /api/control`
- `GET /api/config`
- `POST /api/config`
- `GET /api/logs/recent`
- `GET /api/logs/stream`
- `GET /api/features`
- `POST /api/features`
- `GET /api/version`

Optional module endpoints:

- `/api/tunnel-manager/*`
- `/api/ddns/*`
- `/api/mcp/*`
- `/api/s3/*`
- `/mcp`
- `/webdav/*`

## Development

Requirements:

- Go 1.26 or newer
- Git
- Make, optional

Build:

```bash
make build
```

Run:

```bash
make run
```

Test:

```bash
make test
```

Manual build:

```bash
VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(date -u '+%Y-%m-%d_%H:%M:%S_UTC')
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")

CGO_ENABLED=0 go build -trimpath \
  -ldflags="-s -w \
    -X 'cfui/version.Version=${VERSION}' \
    -X 'cfui/version.BuildTime=${BUILD_TIME}' \
    -X 'cfui/version.GitCommit=${GIT_COMMIT}'" \
  -o cfui .
```

Project layout:

```text
cfui/
├── internal/config/       # SQLite-backed configuration and migration
├── internal/ddns/         # DDNS detection and Cloudflare DNS sync
├── internal/mcpbridge/    # MCP endpoint and token store
├── internal/s3dav/        # S3-compatible filesystem, file API, WebDAV adapter
├── internal/server/       # HTTP server, API handlers, dedicated WebDAV server
├── internal/service/      # Local cloudflared runner
├── internal/tunnelmgr/    # Cloudflare Tunnel ingress management
├── locales/               # UI translations
├── web/dist/              # Embedded frontend
├── version/               # Build-time version metadata
└── main.go
```

## Security Notes

- Do not expose cfui directly to the public internet without a trusted access control layer.
- Treat Tunnel tokens, Cloudflare API tokens, S3 keys, and WebDAV passwords as secrets.
- Prefer scoped Cloudflare API tokens over global API keys.
- Disable WebDAV authentication only on trusted networks.
- Rotate credentials if they were pasted into logs, chat, shell history, or screenshots.

## Troubleshooting

### Tunnel does not start

- Check that the tunnel token is correct.
- Review recent logs in the UI.
- Authentication/configuration errors are not auto-restarted.
- Metrics registration conflicts may require restarting the cfui process.

### Remote Tunnel Manager cannot load config

- Verify Account ID and Tunnel ID.
- Check API token permissions from the Remote Tunnel Manager page.
- If Account ID or Tunnel ID are blank, check whether they can be decoded from the local tunnel token.

### DDNS does not update records

- Enable Remote Tunnel Manager first.
- Verify `Zone -> DNS -> Edit` permission.
- Check configured IP source URLs.
- Use manual sync to see the latest error.

### S3 WebDAV cannot list files

- Check S3 endpoint, region, bucket name, path-style mode, and credentials.
- For R2, use the R2 S3 endpoint and an R2 S3 Access Key ID / Secret Access Key.
- Confirm the mount and WebDAV endpoint are enabled.
- Use the S3 test and WebDAV test buttons from the UI.

## License

This project is licensed under the MIT License.
