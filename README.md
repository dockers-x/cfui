# CloudFlared UI

A modern web-based control panel for managing Cloudflare Tunnel (cloudflared) with an intuitive interface.

## Features

- **Easy Management**: Start, stop, and monitor your Cloudflare Tunnel from a web interface
- **Multi-language Support**: Available in English, Chinese (中文), and Japanese (日本語)
- **Auto-restart**: Automatically restart tunnel on failure with configurable options
- **Advanced Configuration**: Support for all major cloudflared parameters
  - Protocol selection (Auto, HTTP/2, QUIC)
  - Region selection
  - Custom tunnel identifiers
  - Metrics server
  - Post-quantum cryptography
  - And more...
- **Real-time Logs**: View system logs and tunnel status in real-time
- **Responsive Design**: Works on desktop, tablet, and mobile devices

## Quick Start

### Using Docker (Recommended)

```bash
docker run -d \
  --name cfui \
  -p 14333:14333 \
  -v cloudflared-data:/app/data \
  --restart unless-stopped \
  czyt/cfui:latest
```

Access the web interface at `http://localhost:14333`

### Using Docker Compose

```yaml
version: '3.8'

services:
  cfui:
    image: ghcr.io/dockers-x/cfui:latest
    # Or use Docker Hub:
    # image: czyt/cfui:latest
    container_name: cfui
    restart: unless-stopped
    ports:
      - "14333:14333"
    volumes:
      # Persist configuration and data
      - cloudflared-data:/app/data
    environment:
      # Optional: Override default bind address (default is 0.0.0.0)
      - BIND_HOST=0.0.0.0
      # Optional: Override default port (default is 14333)
      - PORT=14333
      # Optional: Set timezone
      - TZ=UTC
    healthcheck:
      test: ["CMD", "sh", "-c", "wget --no-verbose --tries=1 --spider http://localhost:$$PORT/ || exit 1"]
      interval: 30s
      timeout: 3s
      start_period: 5s
      retries: 3
    # Optional: Resource limits
    deploy:
      resources:
        limits:
          cpus: '1'
          memory: 512M
        reservations:
          cpus: '0.5'
          memory: 256M

volumes:
  cloudflared-data:
    driver: local

```

### Manual Installation

1. Download the latest release from [GitHub Releases](https://github.com/yourusername/cloudflared-ui/releases)
2. Extract and run:

```bash
# Linux/macOS
chmod +x cloudflared-web
./cloudflared-web

# Windows
cloudflared-web.exe
```

3. Open your browser and navigate to `http://localhost:14333`

## Building from Source

### Prerequisites

- Go 1.25 or higher
- Git
- Make (optional, for easier building)

### Build Steps

#### Using Make (Recommended)

```bash
# Clone the repository
git clone https://github.com/yourusername/cloudflared-ui.git
cd cloudflared-ui

# Build with version info injected
make build

# Or build specific version
VERSION=v1.0.0 make build

# Run
./cfui
```

#### Manual Build

```bash
# Clone the repository
git clone https://github.com/yourusername/cloudflared-ui.git
cd cloudflared-ui

# Build with version info
VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(date -u '+%Y-%m-%d_%H:%M:%S_UTC')
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")

go build -trimpath \
  -ldflags="-s -w \
    -X 'cfui/version.Version=${VERSION}' \
    -X 'cfui/version.BuildTime=${BUILD_TIME}' \
    -X 'cfui/version.GitCommit=${GIT_COMMIT}'" \
  -o cfui .

# Run
./cfui
```

## Configuration

The application stores its configuration in the `data` directory:

- `data/config.json` - Main configuration file
- `data/logs/` - Application logs (if configured)

### Environment Variables

- `BIND_HOST` - Server bind address (default: 0.0.0.0)
- `PORT` - Server port (default: 14333)
- `DATA_DIR` - Data directory path (default: ./data)
- `LOG_DIR` - Log directory path (default: {DATA_DIR}/logs)
- `LOG_LEVEL` - Log level: debug, info, warn, error (default: info)

### Configuration Options

#### Basic Settings
- **Token**: Your Cloudflare Tunnel token (required)
- **Auto-start on launch**: Start tunnel automatically when the application starts
- **Auto-restart on failure**: Automatically restart tunnel on abnormal exit
- **Custom Tunnel Identifier**: Custom tag shown in Cloudflare dashboard

#### Advanced Settings
- **Protocol**: Connection protocol (Auto, HTTP/2, QUIC)
- **Region**: Preferred connection region
- **Grace Period**: Shutdown grace period (e.g., 30s)
- **Max Retries**: Maximum connection retry attempts
- **Metrics**: Enable Prometheus metrics endpoint
- **Edge Bind IP Address**: Local IP address to bind for outgoing connections to Cloudflare edge (optional)
- **Disable Backend TLS Verification**: Disable TLS certificate verification for backend services (not recommended for production)

## Getting a Tunnel Token

1. Go to [Cloudflare Zero Trust Dashboard](https://one.dash.cloudflare.com/)
2. Navigate to **Networks** > **Tunnels**
3. Create a new tunnel or select an existing one
4. Copy the token from the tunnel configuration

## API Endpoints

- `GET /api/status` - Get tunnel status
- `GET /api/config` - Get current configuration
- `POST /api/config` - Update configuration
- `POST /api/control` - Control tunnel (start/stop)
- `GET /api/i18n/:lang` - Get translations

## Development

### Project Structure

```
cloudflared-ui/
├── config/         # Configuration management
├── server/         # HTTP server and API handlers
├── service/        # Tunnel runner and management
├── locales/        # Translation files
├── web/            # Frontend files
│   └── dist/       # Built frontend assets
├── main.go         # Application entry point
└── README.md
```

### Running in Development Mode

```bash
go run main.go
```

## Docker Build

### Using Make

```bash
# Build Docker image with version info
make build-docker

# Or build specific version
VERSION=v1.0.0 make build-docker
```

### Manual Docker Build

```bash
# Build image with version info
VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(date -u '+%Y-%m-%d_%H:%M:%S_UTC')
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")

docker build \
  --build-arg VERSION=${VERSION} \
  --build-arg BUILD_TIME=${BUILD_TIME} \
  --build-arg GIT_COMMIT=${GIT_COMMIT} \
  -t cfui:${VERSION} \
  -t cfui:latest \
  .

# Run container
docker run -d -p 14333:14333 -v $(pwd)/data:/app/data cfui:latest
```

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the MIT License - see the LICENSE file for details.

## Security

- Never expose the web interface directly to the internet without proper authentication
- Keep your tunnel token secure
- Use HTTPS if exposing the interface externally
- Regularly update to the latest version for security patches

## Troubleshooting

### Tunnel won't start

- Verify your tunnel token is correct
- Check system logs for error messages
- Ensure cloudflared binary is accessible
- Check firewall settings

### Duplicate metrics error

- Restart the application (not just the tunnel)
- Disable metrics if not needed
- Check for port conflicts

### Auto-restart not working

- Enable "Auto-restart on failure" in settings
- Check that the error is retryable (authentication errors won't auto-restart)
- Review system logs for restart attempts

## Acknowledgments

- Built with [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-apps/)
- Uses [urfave/cli](https://github.com/urfave/cli) for CLI framework
- Frontend built with vanilla JavaScript and modern CSS

## Support

- GitHub Issues: [Report a bug or request a feature](https://github.com/yourusername/cloudflared-ui/issues)
- Documentation: [Cloudflare Tunnel Docs](https://developers.cloudflare.com/cloudflare-one/connections/connect-apps/)

---

Made with ❤️ for the Cloudflare community
