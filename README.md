# cfui

[中文说明](README.zh-CN.md) | English

cfui is a web control panel for Cloudflare Tunnel (`cloudflared`). It runs the local tunnel process, manages selected Cloudflare Tunnel ingress rules through the Cloudflare API, updates DNS records for DDNS use cases, exposes S3-compatible storage through WebDAV, and provides an MCP endpoint for AI clients.

The web UI is built into the binary. Configuration is stored in a local SQLite database under the data directory.

## Acknowledgements

- [LinuxDo Community](https://linux.do)

## Features

- **Local Cloudflare Tunnel runner**
  - Manage multiple Cloudflare Tunnel profiles from the browser.
  - Paste Cloudflare Tunnel tokens and edit each saved profile independently.
  - Start or stop each tunnel profile independently; multiple profiles can run at the same time.
  - Configure auto-start, auto-restart, protocol, region, retries, graceful shutdown, metrics, post-quantum mode, edge IP version, edge bind address, TLS verification, and extra cloudflared arguments.
  - Show tunnel status, active protocol, last error, and version/build information.

- **Remote Tunnel Manager**
  - Optional feature for managing Cloudflare-hosted tunnel ingress configuration.
  - Select any saved tunnel profile for remote management.
  - Load an existing tunnel by Account ID and Tunnel ID. API credentials are shared, while Account ID and Tunnel ID can be stored per tunnel profile.
  - Read the Cloudflare Tunnel name through the Cloudflare API and apply it to a profile when the local profile is still using an auto-generated name.
  - Add, edit, and delete public hostname rules with hostname, path, service type, service URL, host header, origin server name, and TLS verification options.
  - Decode Account ID and Tunnel ID from the selected tunnel token when possible.
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
  - Dedicated WebDAV mode supports manual start/stop, optional auto-start, direct-port endpoints, custom public URLs, and Cloudflare Tunnel rule handoff through the current tunnel profile.
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

- **Cloudflare OAuth console**
  - Optional run mode for managing Cloudflare account resources with a Cloudflare OAuth access token.
  - Lists accounts, zones with plan/name-server detail, DNS records, Cloudflare Tunnel control-plane records, Workers, R2, D1, KV, Snippets, WAF, Cloudflare public status, and selected zone settings when the granted scopes allow it.
  - OAuth sessions and PKCE state are stored in the local SQLite database, not JSON files.
  - Multiple OAuth identities can be saved and switched; the current identity controls all Cloudflare API calls.
  - Local cfui tools such as the cloudflared runner, MCP, and S3 WebDAV remain separate local capabilities.

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

1. Create or select one or more Cloudflare Tunnels in Cloudflare Zero Trust.
2. Copy the tunnel token from the tunnel install command.
3. Paste the token into a tunnel profile on the Tunnel Configuration page.
4. Save the configuration.
5. Start the tunnel profile from the UI. Repeat for any other profiles you want to run.

The local tunnel runner does not require Cloudflare API credentials. API credentials are only needed for Remote Tunnel Manager, DDNS, and optional R2 bucket management.

## Tunnel Profiles

cfui can store multiple tunnel profiles. A tunnel profile contains the local cloudflared token and the remote-management identity for one Cloudflare Tunnel.

- Each profile can be edited, started, stopped, and restarted independently.
- Several local tunnel profiles can run at the same time when their settings do not conflict.
- Remote Tunnel Manager uses the selected profile's Account ID and Tunnel ID with the shared Cloudflare API credentials.
- If a profile's Account ID or Tunnel ID is blank, cfui tries to decode them from that profile's tunnel token.
- S3 WebDAV Cloudflare Tunnel publishing uses the current tunnel profile retained for legacy integrations.

## Cloudflare API Permissions

Remote Tunnel Manager and DDNS use Cloudflare API credentials. An API Token is recommended.

Required permissions for Remote Tunnel Manager and DDNS:

- `Account -> Argo Tunnel (Legacy) -> Edit`
- `Zone -> Zone -> Read`
- `Zone -> DNS -> Edit`

Optional permission for R2 bucket list/create:

- `Account -> Workers R2 Storage -> Edit`

S3 WebDAV file access does not use the Cloudflare API token. It uses the S3-compatible Access Key ID and Secret Access Key configured on the S3 WebDAV page.

## Cloudflare OAuth Mode

cfui supports three top-level run modes. They choose the default workspace and
whether the local cloudflared runner auto-starts; they do not remove the other
workspace from the process.

- `classic`: default. Start in the local cfui workspace and auto-start eligible local tunnel profiles.
- `oauth`: start in the Cloudflare OAuth workspace. The local tunnel runner is not auto-started.
- `both`: start in the local cfui workspace while keeping the Cloudflare workspace entry available.

The workspaces are route-isolated:

- `/` and `/local`: local cfui workspace for cloudflared runner, Remote Tunnel Manager, DDNS, MCP, S3 WebDAV, and feature settings.
- `/cloudflare`: Cloudflare OAuth workspace for Cloudflare account resources. The local workspace is only exposed as a switch entry.

Set the mode with:

```bash
CFUI_RUN_MODE=oauth ./cfui
```

OAuth mode calls Cloudflare APIs directly with the current OAuth identity's bearer token. It does not create API tokens on behalf of the user. cfui can store multiple OAuth identities in SQLite and rename, switch, or remove them from the OAuth console; no OAuth session or PKCE state is written to JSON. Before each sign-in, the OAuth console can build a module-based scope set for the next identity; when adding an identity while already signed in, cfui opens Cloudflare through the logout endpoint first so browser cookies do not silently reuse the current account; `CFUI_OAUTH_SCOPES` remains the default template and fallback for direct `/oauth/start` use. When OAuth is not configured, the Cloudflare workspace shows the current relay callback, local callback, Worker script path, Worker variable, relay health check, and cfui environment variables needed to finish setup. The relay callback can be edited from the WebUI; the saved SQLite value overrides `CFUI_OAUTH_RELAY_URL` and must exactly match the Cloudflare OAuth client's redirect callback URL. Available Cloudflare pages are controlled by the scopes granted to the current OAuth app session; local MCP and S3 WebDAV remain in the local cfui workspace and are not shown inside `/cloudflare`.

The OAuth console currently exposes an orange-cloud-style Overview dashboard, accounts, zones with plan/name-server detail, DNS records, Cloudflare Tunnel control-plane records with status/type/remote config/active connection detail, Workers scripts, R2 buckets, objects, and account-level metrics, D1 databases, KV namespaces, zone-level Snippets, custom WAF rules, managed WAF ruleset overrides, and managed WAF exceptions, Zone Analytics, Account Usage, Cloudflare public status, and selected zone settings. The Overview uses one backend endpoint to aggregate account, zone, DNS, Workers, Tunnel, R2, D1, KV, Snippets, WAF, and public status counts; missing scopes or per-product API failures are shown as unavailable metrics instead of breaking the whole console. DNS records can be searched locally; A/AAAA/CNAME proxy state can be toggled from the list, and records can be created, updated, and deleted when `dns.write` is granted. Workers include list and detail views with script metadata, settings, Tail consumers, a bounded read-only script preview when `workers-scripts.read` is granted, per-script metrics through the GraphQL Analytics API when `analytics.read` or `account-analytics.read` is granted, and Live Tail streaming through a backend SSE proxy when `workers-tail.read` is granted. R2 buckets can be created and deleted when `workers-r2.write` is granted; bucket object lists, local search over loaded objects, bounded UTF-8 previews, bounded hexdump previews for non-UTF-8 objects, common image/audio/video/PDF previews through the same-origin download proxy, text object create/update, direct file upload up to 128 MiB, chunked browser-to-cfui upload up to 5 GiB followed by backend streaming to R2, same-origin file download, object delete, and account-level storage metrics are available through backend Cloudflare REST calls because the current Cloudflare Go SDK only exposes bucket-level R2 APIs. R2 metrics are read on demand and are not persisted as local snapshots. KV namespaces can be created, renamed, deleted, drilled into keys, used to view UTF-8 values, edit/delete values with `workers-kv-storage.write`, create text keys, and bulk delete loaded keys through Cloudflare's bulk KV API. D1 databases can be created and deleted with confirmation when `d1.write` is granted, and include a SQL query console plus table browsing, paged row loading, and parameterized row update/delete. Snippets can be created/deleted, existing snippet code can be loaded and edited through the backend `/content` REST endpoint, and trigger rules can be added, enabled, disabled, or deleted when `snippets.write` is granted. Custom WAF rules can be created, edited, enabled, disabled, or deleted when `zone-waf.write` is granted. The simple WAF editor supports the safe action subset (`block`, `challenge`, `managed_challenge`, `js_challenge`, `log`, and `skip`); `skip` rules support Cloudflare action parameters for current ruleset, products, and phases, and the form includes a dedicated rate-limit builder for common `ratelimit` fields. Existing and complex WAF rules can also be edited through an Advanced JSON section for `action_parameters`, `ratelimit`, `logging`, and `exposed_credential_check`; unchanged advanced JSON fields are not submitted, and `null` clears a field. WAF updates use read-modify-write and preserve untouched advanced fields. Managed WAF ruleset overrides are isolated to the `http_request_firewall_managed` phase and only create or edit `execute` rules with a managed ruleset ID plus ruleset, tag/category, and managed-rule override fields. Managed WAF exceptions are isolated to the same phase and only create or edit `skip` rules for the current managed ruleset, selected managed rulesets, or selected managed rule IDs. Complex rules still expose ref/version, raw action parameters, rate-limit, logging, exposed credential check metadata, and a copyable audit JSON payload for auditing. Zone Analytics uses the Cloudflare Go SDK dashboard endpoint for 24h/7d/30d traffic summaries when `analytics.read` or `account-analytics.read` is granted. Account Usage uses backend GraphQL Analytics API queries to show Workers, last-hour Workers errors, R2 operations, D1, and KV usage for the selected account with the same analytics scopes; R2 storage and object count are best-effort overwritten from REST `accounts/{account_id}/r2/metrics` using the same Standard-class snapshot as orange-cloud. Account Usage also makes a best-effort backend call to `accounts/{account_id}/subscriptions` for billing period and Workers/R2 paid-plan context. Cloudflare OAuth normally does not expose a billing scope, so subscription 403 responses are shown as unavailable billing context and do not block usage data. Cloudflare Status uses the public Statuspage API through the cfui backend and does not require an OAuth scope. Zone settings support SSL/TLS mode, development mode, security level, cache level, browser cache TTL, Always Use HTTPS, Automatic HTTPS Rewrites, Brotli, Rocket Loader, and full cache purge when the matching settings/cache scopes are granted. Native R2 multipart remains follow-up work unless OAuth REST exposes a multipart endpoint or users provide R2 S3 credentials. The UI hides modules when the current OAuth token does not include the required scope.

R2 object detail views also expose copy-key, copy-object, and move-object actions, including same-account cross-bucket copies/moves, plus metadata including size, content type, ETag, storage class, last modified time, and encoding status. Non-UTF-8 objects show a bounded hexdump sample in the detail panel. Inline media/PDF previews are capped at 50 MiB; larger objects stay download-only. The upload form uses direct upload for files up to 128 MiB and switches larger files to chunked browser-to-cfui upload up to 5 GiB.

To use OAuth mode:

1. Create a Cloudflare OAuth app and set its redirect URI to the Worker relay URL.
2. Set `CFUI_OAUTH_CLIENT_ID` to the OAuth client ID.
3. Keep `CFUI_OAUTH_RELAY_URL` at the default relay (`https://oauth.omarchy.qzz.io/oauth/callback`) or point it to your own Worker relay. You can also edit the relay callback from the OAuth setup page; that saved SQLite value overrides the environment default.
4. Deploy or use the Worker relay from `docs/cloudflare-oauth-worker.js`. The Cloudflare OAuth app redirect URI should be only the Worker's public HTTPS callback URL, for example `https://oauth.omarchy.qzz.io/oauth/callback`; do not append `cfui_callback_url`. cfui encodes the current browser-facing `/oauth/callback` URL into OAuth `state`, and the Worker reads that state to forward `code` and `state` back to the correct cfui instance. If one Worker serves public cfui domains, configure the Worker variable `CFUI_ALLOWED_CALLBACK_ORIGINS` with a comma-separated origin allowlist, or intentionally set `*` for a multi-user relay. Loopback, private/LAN IPs, `.local`, `.internal`, `.lan`, `.home.arpa`, and `.test` callback hosts are allowed by default. `CFUI_CALLBACK_URL` remains an optional fallback only. The Worker exposes `/health` with a `state-v1` marker, and cfui can check whether the deployed relay is new enough from the OAuth setup page or `GET /api/oauth/relay-check`.

Zone overview uses `GET /api/cf/dns/count` to fetch DNS record totals without loading every record. For D1, cfui loads the database list first and then best-effort refreshes each database with `GET /api/cf/d1/databases/{database_id}` so table count and file size match Cloudflare's detail endpoint. Failed detail lookups do not block the database list, SQL console, or table browser.

The default OAuth scope template is:

```text
account-settings.read zone.read dns.read dns.write cloudflare-tunnel.read
```

You can override them with `CFUI_OAUTH_SCOPES`. The scopes selected when creating the Cloudflare OAuth app are the maximum allowlist for that OAuth client. The scopes cfui sends during sign-in, from the UI selector or `CFUI_OAUTH_SCOPES`, must be a subset of that allowlist; Cloudflare does not merge both lists into the final token.

For Cloudflare Tunnel creation and local profile linking, add:

```text
cloudflare-tunnel.read cloudflare-tunnel.write
```

For zone settings actions, add the required scopes for your OAuth app, for example:

```text
account-settings.read zone.read dns.read dns.write cloudflare-tunnel.read zone-settings.read zone-settings.write cache.purge
```

For R2 bucket write actions, add:

```text
workers-r2.read workers-r2.write
```

For Worker Tail streaming, add:

```text
workers-tail.read
```

For Snippets and trigger rule management, add:

```text
snippets.read snippets.write
```

For custom WAF rule management, add:

```text
zone-waf.read zone-waf.write
```

For Zone Analytics, add one of:

```text
analytics.read account-analytics.read
```

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
| `CFUI_RUN_MODE` / `CFUI_MODE` | `classic`, `oauth`, or `both` | `classic` |
| `CFUI_TUNNEL_MGMT_ENABLED` / `CFUI_TUNNEL_MANAGEMENT_ENABLED` | Enable Remote Tunnel Manager | unset |
| `CFUI_TUNNEL_ACCOUNT_ID` / `CLOUDFLARE_ACCOUNT_ID` / `CLOUDFLARE_APP_ID` | Cloudflare account ID | unset |
| `CFUI_TUNNEL_ID` / `CLOUDFLARE_TUNNEL_ID` | Cloudflare tunnel ID | unset |
| `CFUI_TUNNEL_API_TOKEN` / `CLOUDFLARE_API_TOKEN` | Cloudflare API token | unset |
| `CFUI_TUNNEL_API_EMAIL` / `CLOUDFLARE_API_EMAIL` | Cloudflare account email for global API key auth | unset |
| `CFUI_TUNNEL_API_KEY` / `CLOUDFLARE_API_KEY` | Cloudflare global API key | unset |
| `CFUI_OAUTH_CLIENT_ID` | Cloudflare OAuth client ID | unset |
| `CFUI_OAUTH_RELAY_URL` / `CFUI_OAUTH_REDIRECT_URI` | Default OAuth Worker relay callback URL registered in Cloudflare; a WebUI-saved SQLite value overrides it | `https://oauth.omarchy.qzz.io/oauth/callback` |
| `CFUI_OAUTH_SCOPES` | Default space-separated OAuth scope template used to initialize the sign-in selector and direct `/oauth/start` | `account-settings.read zone.read dns.read dns.write cloudflare-tunnel.read` |
| `CFUI_OAUTH_AUTH_URL` | Cloudflare OAuth authorization endpoint override | Cloudflare default |
| `CFUI_OAUTH_LOGOUT_URL` | Cloudflare logout endpoint used before adding another identity | Cloudflare default |
| `CFUI_OAUTH_TOKEN_URL` | Cloudflare OAuth token endpoint override | Cloudflare default |
| `CFUI_OAUTH_REVOKE_URL` | Cloudflare OAuth revoke endpoint override | Cloudflare default |
| `CFUI_OAUTH_USERINFO_URL` | Cloudflare OAuth userinfo endpoint override | Cloudflare default |

Environment-provided Cloudflare credentials override saved UI values at runtime. OAuth relay callback configuration is the exception: a WebUI-saved SQLite value overrides `CFUI_OAUTH_RELAY_URL`.

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

Legacy single-tunnel settings are migrated into the first tunnel profile. Tunnel profiles are stored in the `tunnel_profiles` table, and the internal `default` profile key is retained for old single-tunnel endpoints and legacy integrations.

## API Overview

Main endpoints:

- `GET /api/status`
- `POST /api/control`
- `GET /api/config`
- `POST /api/config`
- `GET /api/tunnels`
- `POST /api/tunnels`
- `GET /api/tunnels/{key}`
- `PUT /api/tunnels/{key}`
- `DELETE /api/tunnels/{key}`
- `POST /api/tunnels/{key}/activate-local`
- `GET /api/logs/recent`
- `GET /api/logs/stream`
- `GET /api/features`
- `POST /api/features`
- `GET /api/oauth/status`
- `GET /api/oauth/relay-check`
- `PATCH /api/oauth/config`
- `POST /api/oauth/login`
- `POST /api/oauth/logout`
- `POST /api/oauth/session`
- `PATCH /api/oauth/session`
- `GET /api/version`

Optional module endpoints:

- `/api/cf/*`
- `/api/tunnel-manager/*`
- `/api/ddns/*`
- `/api/mcp/*`
- `/api/s3/*`
- `/mcp`
- `/webdav/*`

Remote Tunnel Manager endpoints accept an optional `tunnel_key` query parameter, for example `/api/tunnel-manager/config?tunnel_key=office`, so remote ingress rules can be managed for any saved profile. `GET /api/tunnel-manager/tunnel?tunnel_key=office` reads Cloudflare Tunnel metadata such as the tunnel name.

OAuth Cloudflare endpoints:

- `GET /api/cf/overview`
- `GET /api/cf/accounts`
- `GET /api/cf/status`
- `GET /api/cf/usage/account`
- `GET /api/cf/zones`
- `GET /api/cf/zones/{zone_id}`
- `GET /api/cf/dns/count`
- `GET /api/cf/dns`
- `POST /api/cf/dns`
- `PUT /api/cf/dns/{id}`
- `DELETE /api/cf/dns/{id}`
- `GET /api/cf/tunnels`
- `GET /api/cf/workers`
- `GET /api/cf/workers/{script}`
- `GET /api/cf/workers/{script}/metrics`
- `GET /api/cf/workers/{script}/tail`
- `GET /api/cf/r2/metrics`
- `GET /api/cf/r2/buckets`
- `POST /api/cf/r2/buckets`
- `DELETE /api/cf/r2/buckets/{bucket}`
- `GET /api/cf/r2/objects`
- `GET /api/cf/r2/object`
- `PUT /api/cf/r2/object`
- `POST /api/cf/r2/object/upload`
- `GET /api/cf/r2/object/download`
- `DELETE /api/cf/r2/object`
- `GET /api/cf/d1/databases`
- `POST /api/cf/d1/databases`
- `GET /api/cf/d1/databases/{database_id}`
- `DELETE /api/cf/d1/databases/{database_id}`
- `POST /api/cf/d1/query`
- `GET /api/cf/d1/tables`
- `GET /api/cf/d1/table`
- `PATCH /api/cf/d1/table`
- `DELETE /api/cf/d1/table`
- `GET /api/cf/kv/namespaces`
- `POST /api/cf/kv/namespaces`
- `PUT /api/cf/kv/namespaces/{namespace_id}`
- `DELETE /api/cf/kv/namespaces/{namespace_id}`
- `GET /api/cf/kv/keys`
- `POST /api/cf/kv/keys/bulk-delete`
- `GET /api/cf/snippets`
- `POST /api/cf/snippets`
- `GET /api/cf/snippets/{name}/content`
- `PUT /api/cf/snippets/{name}/content`
- `DELETE /api/cf/snippets/{name}`
- `GET /api/cf/snippets/rules`
- `POST /api/cf/snippets/rules`
- `PATCH /api/cf/snippets/rules/{id}`
- `DELETE /api/cf/snippets/rules/{id}`
- `GET /api/cf/waf`
- `POST /api/cf/waf/rules`
- `PATCH /api/cf/waf/rules/{id}`
- `DELETE /api/cf/waf/rules/{id}`
- `GET /api/cf/waf/managed-overrides`
- `POST /api/cf/waf/managed-overrides/rules`
- `PATCH /api/cf/waf/managed-overrides/rules/{id}`
- `DELETE /api/cf/waf/managed-overrides/rules/{id}`
- `GET /api/cf/waf/managed-exceptions`
- `POST /api/cf/waf/managed-exceptions/rules`
- `PATCH /api/cf/waf/managed-exceptions/rules/{id}`
- `DELETE /api/cf/waf/managed-exceptions/rules/{id}`
- `GET /api/cf/analytics/zone`
- `GET /api/cf/zone-settings`

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

- Verify that the correct tunnel profile is selected.
- Verify Account ID and Tunnel ID for that profile.
- Check API token permissions from the Remote Tunnel Manager page.
- If Account ID or Tunnel ID are blank, check whether they can be decoded from the selected tunnel token.

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
