# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

CloudFlared UI (cfui) is a web-based control panel for managing Cloudflare Tunnel (cloudflared). It wraps the official cloudflared library and provides a web UI for configuration and control.

## Build & Development Commands

### Building the Binary
```bash
# Build with automatic version detection
make build

# Build with specific version
VERSION=v1.0.0 make build

# Manual build (if Make unavailable)
CGO_ENABLED=0 go build -trimpath \
  -ldflags="-s -w -X 'cfui/version.Version=dev' -X 'cfui/version.BuildTime=$(date -u '+%Y-%m-%d_%H:%M:%S_UTC')' -X 'cfui/version.GitCommit=$(git rev-parse --short HEAD)'" \
  -o cfui .
```

### Running Tests
```bash
make test
# Or directly
go test -v ./...
```

### Running Locally
```bash
# Build and run
make run

# Or run directly without building
go run main.go

# Set custom port
PORT=8080 go run main.go

# Set custom data directory
DATA_DIR=/custom/path go run main.go
```

### Docker Build
```bash
# Build Docker image
make build-docker

# Or manually with version info
VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(date -u '+%Y-%m-%d_%H:%M:%S_UTC')
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")

docker build \
  --build-arg VERSION=${VERSION} \
  --build-arg BUILD_TIME=${BUILD_TIME} \
  --build-arg GIT_COMMIT=${GIT_COMMIT} \
  -t cfui:${VERSION} -t cfui:latest .
```

### Version Information
```bash
# View current version info
make version
```

## Architecture

### Core Components

**main.go**: Application entry point that orchestrates initialization.
- Sets up config directory (env: `DATA_DIR`, default: `~/.cloudflared-web`)
- Initializes config manager, runner, and server
- Embeds web UI assets and locale files

**config/** (config/config.go): Configuration management with thread-safe operations.
- Manages `data/config.json` persistence
- Handles all cloudflared parameters (protocol, region, metrics, etc.)
- Provides default values and atomic read/write operations via mutex

**internal/cloudflared/**: Owns every interaction with the embedded cloudflared library.
- `EnsureInit` performs process-wide one-time setup (`tunnel.Init`, CLI exit interception, shared Prometheus registry with duplicate-tolerant registerer)
- `Instance` manages one tunnel lifecycle: start/stop, protocol fallback (quic ⇄ http2), auto-restart with exponential backoff, temp config files
- Multiple `Instance`s can run in parallel (one per tunnel profile)
- Per-instance stop uses context cancellation only; the shared graceful-shutdown channel is reserved for process exit (`ShutdownProcess`) because cloudflared closes the same channel on SIGTERM

**internal/service/** (runner.go): Multi-instance tunnel manager bridging config to cloudflared.
- Holds one `cloudflared.Instance` per tunnel profile (`StartProfile`/`StopProfile`/`ProfileStatus`)
- Legacy `Start`/`Stop`/`Status` operate on the active profile for API compatibility
- Options are re-read from config on every (re)start, so config edits apply on restart and deleted profiles stop auto-restarting
- Refuses to start a profile whose metrics port collides with a running instance

**internal/server/** (server.go, middleware.go): HTTP server and API handlers.
- Serves embedded static web UI from `web/dist/`
- Provides REST API endpoints for config, status, and control
- `/api/tunnels` GET returns all profiles plus a `statuses` map (key → running/status/protocol/error)
- Serves i18n translations from embedded TOML files
- Middleware for panic recovery and request logging (polling endpoints log at debug level)
- `PrepareShutdown` closes long-lived SSE log streams so HTTP shutdown doesn't stall

**internal/localauth/**: Optional SQLite-backed local management access guard.
- Disabled by default; no historical-user migration or startup wizard is required
- Stores Argon2id password hashes and SHA-256 session-token digests in `data.db`
- Login, password rotation, session revocation, disable, and session creation serialize through the Store mutation lock
- Authentication is configured only through the UI/database; there are no local-auth environment variables

**internal/logger/** (logger.go): Structured logging with rotation.
- Uses `go.uber.org/zap` for structured logging
- Implements file rotation via `lumberjack`
- Logs to both file (`~/.cloudflared-web/logs/cfui.log`) and console
- JSON format for files, colored console output

**version/**: Version information injected at build time via ldflags.

### Critical Architecture Details

**Cloudflared Integration**: The app uses the official cloudflared library as a dependency (not spawning external process). This means:
- `tunnel.Init()` must be called exactly once via `cloudflared.EnsureInit` (the software name shown in the Cloudflare dashboard is fixed by the first call)
- CLI framework is intercepted to prevent `os.Exit()` calls (set once at init)
- All tunnel instances share one Prometheus registry; duplicate registrations are absorbed by a safe registerer
- Custom tags require temporary YAML config files (cleaned up on shutdown)
- Multiple tunnel profiles can run concurrently, each as its own `cloudflared.Instance`

**Multi-Tunnel Instances**:
- Each tunnel profile (`Config.Tunnels[]`) can be started/stopped independently via `/api/tunnels/{key}/control`
- The "active" profile only determines what the legacy `/api/status` + `/api/control` endpoints and top-level config fields mirror
- Deleting a profile stops its instance asynchronously; the active profile cannot be deleted

**Auto-Restart Logic**:
- Enabled per tunnel profile via `auto_restart`
- Exponential backoff: 5s, 10s, 20s, 40s (max 60s)
- Max 10 restart attempts; counter resets after 5 minutes of uptime
- Non-retryable errors (auth, config, invalid token) skip auto-restart
- Options (including the auto-restart flag) are re-read from config before each restart attempt
- Restart attempts are generation-bound so a concurrent explicit Stop invalidates stale timers

**Graceful Shutdown**:
- 30-second timeout for tunnel shutdown
- Context cancellation propagates to cloudflared
- Temporary config files cleaned up via defer
- Stop endpoint responds immediately, then stops async to prevent connection errors
- A timed-out embedded run remains `stopping` and non-startable until its goroutine actually exits

**Thread Safety**:
- Config manager uses `sync.RWMutex` for concurrent access
- Runner uses mutex for state management (running, lastError, etc.)

## Environment Variables

- `BIND_HOST`: Web server bind address (default: `0.0.0.0`)
- `PORT`: Web server port (default: `14333`)
- `DATA_DIR`: Data directory for config (default: `./data`, Docker: `/app/data`)
- `LOG_DIR`: Log directory (default: `{DATA_DIR}/logs`, Docker: `/app/logs`)
- `LOG_LEVEL`: Log level - debug, info, warn, error (default: `info`)

## API Endpoints

- `GET /api/config` - Get current configuration
- `POST /api/config` - Update configuration
- `GET /api/status` - Get active tunnel running status and last error (legacy)
- `POST /api/control` - Control active tunnel (action: "start" | "stop") (legacy)
- `GET /api/tunnels` - List tunnel profiles + per-profile live `statuses` map
- `POST /api/tunnels` - Create tunnel profile
- `GET|PUT|DELETE /api/tunnels/{key}` - Manage one tunnel profile (DELETE stops its instance)
- `POST /api/tunnels/{key}/activate-local` - Make profile the default legacy/mirror profile
- `GET /api/tunnels/{key}/status` - Per-tunnel live status
- `POST /api/tunnels/{key}/control` - Start/stop one tunnel instance
- `POST /api/tunnel-manager/checks` - Check selected public-hostname DNS mapping and origin connectivity
- `GET /api/auth/status` - Read optional local access-protection state
- `POST /api/auth/setup|login|logout|password|disable` - Manage local authentication lifecycle
- `POST /api/auth/sessions/revoke-others` - Revoke all local sessions except the current one
- `GET /api/i18n/{lang}` - Get translations (en, zh, ja)

## Configuration File Structure

Located at `{DATA_DIR}/config.json`:

```json
{
  "token": "cloudflare-tunnel-token",
  "auto_start": false,
  "auto_restart": true,
  "custom_tag": "custom-identifier",
  "protocol": "auto",
  "grace_period": "30s",
  "region": "",
  "retries": 5,
  "metrics_enable": false,
  "metrics_port": 60123,
  "log_level": "info",
  "log_file": "",
  "log_json": false,
  "edge_ip_version": "auto",
  "edge_bind_address": "",
  "post_quantum": false,
  "no_tls_verify": false,
  "extra_args": ""
}
```

## Logging System

**Structured Logging**: Uses zap logger with automatic log rotation via lumberjack.

**Log Locations**:
- Docker: `/app/logs/cfui.log`
- Local: `./data/logs/cfui.log`

**Log Rotation Settings**:
- Max size: 100 MB per file
- Max backups: 10 files
- Max age: 30 days
- Compression: enabled

**Log Levels**: debug, info, warn, error

**Log Format**:
- File: JSON (structured, machine-readable)
- Console: Colored text (human-readable)

**Panic Recovery**:
- Three-layer protection (main, HTTP handlers, tunnel runner)
- All panics are logged with full stack traces
- Application continues running after handler panics

## Dependencies

**Key Dependencies**:
- `github.com/cloudflare/cloudflared` - Official Cloudflare Tunnel library (embedded)
- `github.com/urfave/cli/v2` - CLI framework (forked version via replace directive)
- `go.uber.org/zap` - Structured logging
- `gopkg.in/natefinch/lumberjack.v2` - Log rotation
- `github.com/BurntSushi/toml` - TOML parsing for i18n

**Important**: Uses forked `cli/v2` (`github.com/ipostelnik/cli/v2`) via replace directive in go.mod for compatibility with cloudflared integration.

## Multi-language Support

- Locales stored as embedded TOML files in `locales/`
- Supported languages: en (English), zh (Chinese), ja (Japanese)
- Format: `[key] \n other = "translation"`
- Server converts to simplified JSON: `{"key": "translation"}`

## Development Notes

**When modifying internal/cloudflared**:
- Never call `tunnel.Init()` outside `EnsureInit` (process-wide once)
- Per-instance Stop must rely on context cancellation only — never send on the shared graceful-shutdown channel (cloudflared closes it on SIGTERM; sending would panic)
- Process signals (SIGTERM/SIGINT) must be subscribed only via `cloudflared.OwnProcessSignals`: every tunnel run installs an upstream signal watcher that closes the shared shutdown channel, so with >1 run per process lifetime one OS signal double-closes it and crashes the process. Reclaim pulses `signal.Reset` all subscribers after each run launch — a direct `signal.Notify` anywhere else gets silently dropped
- Always clean up temporary config files in defer blocks
- Do not make a Stop timeout startable; the embedded library may still own live connections
- Auto-restart must reserve a new lifecycle only while its originating generation is still current
- Be aware that panics are recovered and may trigger auto-restart

**When modifying config.go**:
- Always use mutex locks for config access
- Provide sensible defaults in `DefaultConfig()`
- Config is reloaded on save, not on get

**When modifying server endpoints**:
- Stop action must respond immediately before shutdown (prevents connection errors)
- Use middleware for panic recovery and logging
- Return JSON for API endpoints, serve static files for web UI
- Local authentication is optional and defaults off; management API mutations use same-origin checks when enabled
- Long-lived streams and slow configuration imports must re-check Session authorization after the initial middleware decision

**Web UI**:
- Frontend assets are embedded at build time from `web/dist/`
- Assets must exist at build time (not runtime)
- To modify UI, update files in `web/dist/` before building

## Testing Considerations

- Test files currently limited to `version/version_test.go`
- When adding tests, consider cloudflared library initialization requirements
- Mock config manager and runner for server tests
- Test auto-restart logic with simulated failures
