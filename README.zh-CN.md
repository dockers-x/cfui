# cfui

中文说明 | [English](README.md)

cfui 是一个用于管理 Cloudflare Tunnel（`cloudflared`）的 Web 控制台。它可以运行本地隧道进程，也可以通过 Cloudflare API 管理远程 Tunnel ingress 规则，支持 DDNS、S3 兼容存储的 WebDAV 挂载，以及供 AI 客户端使用的 MCP 端点。

Web UI 已内置在二进制文件中。配置保存在数据目录下的本地 SQLite 数据库里。

## 功能

- **本地 Cloudflare Tunnel 运行管理**
  - 在浏览器里粘贴 Cloudflare Tunnel token，启动或停止本地隧道。
  - 支持自动启动、异常自动重启、协议、区域、重试次数、优雅关闭时间、metrics、后量子模式、边缘 IP 版本、边缘绑定地址、TLS 校验和额外 cloudflared 参数。
  - 显示隧道状态、当前协议、最近错误和版本构建信息。

- **远程 Tunnel 管理**
  - 可选功能，用于管理 Cloudflare 上托管的 Tunnel ingress 配置。
  - 通过 Account ID 和 Tunnel ID 加载已有 Tunnel。
  - 添加、编辑和删除 public hostname 规则，支持 hostname、path、服务类型、服务地址、Host Header、Origin Server Name 和 TLS 校验选项。
  - 能在可解析时从本地 Tunnel token 自动带出 Account ID 和 Tunnel ID。
  - 在执行 Cloudflare 管理操作前，可以校验 API 权限。

- **DDNS**
  - 可选功能，复用远程 Tunnel 管理里的 Cloudflare API 凭据。
  - 从可配置的 IP source URL 检测公网 IPv4 和 IPv6。
  - 创建和更新 Cloudflare `A` / `AAAA` 记录。
  - 支持 Cloudflare 代理状态、TTL、DNS comment、重试设置、手动同步和最近同步记录。

- **S3 WebDAV**
  - 把一个或多个 S3 兼容 bucket 路径挂载成 WebDAV 端点。
  - 支持通用 S3 兼容服务和 Cloudflare R2 预设。
  - 每个挂载可以单独设置 endpoint、region、bucket、root prefix、mount path、Access Key ID、Secret Access Key、WebDAV 账号密码和认证开关。
  - 支持 S3 连接测试和 WebDAV 连接测试。
  - 内置文件面板，可以列表、上传、下载、删除、重命名和创建目录。
  - WebDAV 可以走主 HTTP 服务，也可以走独立 HTTP 端口；两种模式互斥。
  - 独立 WebDAV 模式支持手动启动/停止、可选自动启动、直连端口、自定义公开 URL，以及跳转到 Cloudflare Tunnel 规则创建。
  - 浏览器对 WebDAV 路径发起 `GET` 时，会显示只读文件列表或下载文件；`PROPFIND`、`PUT`、`DELETE`、`MKCOL`、`MOVE`、`COPY`、`LOCK`、`UNLOCK` 等方法保持 WebDAV 行为。

- **MCP 访问**
  - 可选的 Model Context Protocol 端点，路径为 `/mcp`。
  - 使用 UI 中生成的 Bearer Token 认证。
  - 提供隧道状态/配置、隧道启动/停止、最近日志、远程 Tunnel 管理操作，以及启用 DDNS 后的 DDNS 操作工具。

- **日志和运维**
  - 提供最近日志 API 和可选实时日志流。
  - 支持在浏览器里过滤、复制、下载和清空日志。
  - HTTP 服务包含 panic recovery 和请求日志中间件。

- **功能开关**
  - 远程 Tunnel 管理、DDNS、MCP、S3 WebDAV 都是可选功能。
  - 未启用的功能不会显示对应 Tab。
  - DDNS 依赖远程 Tunnel 管理，因为它复用同一套 Cloudflare API 凭据。

- **多语言 UI**
  - 内置英文、中文、日文界面翻译。

## 快速开始

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

打开：

```text
http://localhost:14333
```

Docker Hub 镜像：

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
      # 如果使用独立 S3 WebDAV 端口，可以额外暴露：
      # - "14334:14334"
    volumes:
      - cfui-data:/app/data
      - cfui-logs:/app/logs
    environment:
      BIND_HOST: 0.0.0.0
      PORT: 14333
      TZ: UTC
      # 可选：通过环境变量注入 Cloudflare API 凭据，避免保存在 UI 中。
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

### 二进制运行

下载 release 二进制后运行：

```bash
chmod +x cfui
./cfui
```

打开：

```text
http://localhost:14333
```

## 首次配置

1. 在 Cloudflare Zero Trust 中创建或选择一个 Cloudflare Tunnel。
2. 从隧道安装命令里复制 tunnel token。
3. 在 Tunnel Configuration 页面粘贴 token。
4. 保存配置。
5. 从 UI 启动隧道。

本地隧道运行不需要 Cloudflare API 凭据。只有远程 Tunnel 管理、DDNS 和可选的 R2 bucket 管理需要 API 凭据。

## Cloudflare API 权限

远程 Tunnel 管理和 DDNS 使用 Cloudflare API 凭据。推荐使用 API Token。

远程 Tunnel 管理和 DDNS 需要：

- `Account -> Argo Tunnel (Legacy) -> Edit`
- `Zone -> Zone -> Read`
- `Zone -> DNS -> Edit`

R2 bucket 列表/创建的可选权限：

- `Account -> Workers R2 Storage -> Edit`

S3 WebDAV 的文件访问不使用 Cloudflare API token。它使用 S3 WebDAV 页面里配置的 S3 Access Key ID 和 Secret Access Key。

## S3 WebDAV

S3 WebDAV 是一个可选模块。先在 Features 页面启用，然后在 S3 WebDAV 页面创建挂载。

### 通用 S3

大多数 S3 兼容服务选择通用 S3。这个模式下 cfui 不管理 bucket。需要填写：

- Endpoint URL
- Region
- Bucket 名称
- Access Key ID
- Secret Access Key
- 服务商要求时开启或关闭 path-style
- 可选 root prefix
- WebDAV mount path，例如 `/webdav/my_s3/`

### Cloudflare R2

当你需要 R2 endpoint 预设，或者希望 cfui 可选地列出/创建 R2 bucket 时，选择 Cloudflare R2。

文件访问需要在 R2 控制台创建 R2 API token，并复制：

- Access Key ID
- Secret Access Key
- S3 endpoint，例如 `https://<ACCOUNT_ID>.r2.cloudflarestorage.com`

如果要让 cfui 列出或创建 R2 bucket，需要在远程 Tunnel 管理里配置具备 `Workers R2 Storage -> Edit` 权限的 Cloudflare API token。

### WebDAV 端点

如果挂载路径是 `/webdav/datasync/`，WebDAV endpoint 就是：

```text
https://your-cfui-host/webdav/datasync/
```

下载文件示例：

```bash
curl -u 'user:password' \
  -o db.sql \
  'https://your-cfui-host/webdav/datasync/path/to/db.sql'
```

检查 WebDAV 对象：

```bash
curl -u 'user:password' \
  -X PROPFIND \
  -H 'Depth: 0' \
  'https://your-cfui-host/webdav/datasync/path/to/db.sql'
```

Range 下载：

```bash
curl -u 'user:password' \
  -H 'Range: bytes=0-1023' \
  -o part.bin \
  'https://your-cfui-host/webdav/datasync/path/to/db.sql'
```

## MCP

在 Features 页面启用 MCP，然后在 MCP 页面创建 Bearer Token。MCP 客户端连接：

```text
/mcp
```

MCP token 只会在创建时显示一次。保存后的 token 列表只显示脱敏值。

## 环境变量

| 变量 | 说明 | 默认值 |
| --- | --- | --- |
| `BIND_HOST` | HTTP 服务绑定地址 | `0.0.0.0` |
| `PORT` | 主 HTTP 服务端口 | `14333` |
| `DATA_DIR` | 数据目录 | `./data` |
| `LOG_DIR` | 日志目录 | `${DATA_DIR}/logs` |
| `LOG_LEVEL` | `debug`、`info`、`warn`、`error` | `info` |
| `CFUI_TUNNEL_MGMT_ENABLED` / `CFUI_TUNNEL_MANAGEMENT_ENABLED` | 启用远程 Tunnel 管理 | 未设置 |
| `CFUI_TUNNEL_ACCOUNT_ID` / `CLOUDFLARE_ACCOUNT_ID` / `CLOUDFLARE_APP_ID` | Cloudflare account ID | 未设置 |
| `CFUI_TUNNEL_ID` / `CLOUDFLARE_TUNNEL_ID` | Cloudflare tunnel ID | 未设置 |
| `CFUI_TUNNEL_API_TOKEN` / `CLOUDFLARE_API_TOKEN` | Cloudflare API token | 未设置 |
| `CFUI_TUNNEL_API_EMAIL` / `CLOUDFLARE_API_EMAIL` | 使用 Global API Key 时的 Cloudflare 账号邮箱 | 未设置 |
| `CFUI_TUNNEL_API_KEY` / `CLOUDFLARE_API_KEY` | Cloudflare Global API Key | 未设置 |

通过环境变量提供的 Cloudflare 凭据会在运行时覆盖 UI 中保存的值。

## 数据和迁移

cfui 使用 SQLite 保存配置：

```text
${DATA_DIR}/data.db
```

日志保存在：

```text
${LOG_DIR}
```

旧版 `config.json` 和旧 `app_configs` 表会自动迁移到结构化 SQLite 表。迁移后的 `config.json` 会被重命名为 `config.json.migrated`。

## API 概览

主要接口：

- `GET /api/status`
- `POST /api/control`
- `GET /api/config`
- `POST /api/config`
- `GET /api/logs/recent`
- `GET /api/logs/stream`
- `GET /api/features`
- `POST /api/features`
- `GET /api/version`

可选模块接口：

- `/api/tunnel-manager/*`
- `/api/ddns/*`
- `/api/mcp/*`
- `/api/s3/*`
- `/mcp`
- `/webdav/*`

## 开发

要求：

- Go 1.26 或更新版本
- Git
- Make，可选

构建：

```bash
make build
```

运行：

```bash
make run
```

测试：

```bash
make test
```

手动构建：

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

项目结构：

```text
cfui/
├── internal/config/       # SQLite 配置和迁移
├── internal/ddns/         # DDNS 检测和 Cloudflare DNS 同步
├── internal/mcpbridge/    # MCP 端点和 token 存储
├── internal/s3dav/        # S3 兼容文件系统、文件 API、WebDAV 适配
├── internal/server/       # HTTP 服务、API handler、独立 WebDAV 服务
├── internal/service/      # 本地 cloudflared runner
├── internal/tunnelmgr/    # Cloudflare Tunnel ingress 管理
├── locales/               # UI 翻译
├── web/dist/              # 内置前端
├── version/               # 构建时版本信息
└── main.go
```

## 安全说明

- 不要在没有可信访问控制的情况下，把 cfui 直接暴露到公网。
- Tunnel token、Cloudflare API token、S3 key、WebDAV 密码都应视为敏感信息。
- 优先使用有范围限制的 Cloudflare API token，不建议使用 Global API Key。
- 只有在可信网络中才建议关闭 WebDAV 认证。
- 如果凭据出现在日志、聊天、命令历史或截图中，请及时轮转。

## 排查问题

### Tunnel 无法启动

- 检查 tunnel token 是否正确。
- 查看 UI 中的最近日志。
- 认证或配置错误不会触发自动重启。
- metrics 注册冲突可能需要重启 cfui 进程。

### 远程 Tunnel 管理无法加载配置

- 检查 Account ID 和 Tunnel ID。
- 在远程 Tunnel 管理页面校验 API token 权限。
- 如果 Account ID 或 Tunnel ID 为空，确认是否能从本地 tunnel token 自动解码。

### DDNS 没有更新记录

- 先启用远程 Tunnel 管理。
- 确认具备 `Zone -> DNS -> Edit` 权限。
- 检查配置的 IP source URL。
- 点击手动同步查看最新错误。

### S3 WebDAV 无法列出文件

- 检查 S3 endpoint、region、bucket 名称、path-style 模式和凭据。
- R2 需要使用 R2 S3 endpoint 以及 R2 S3 Access Key ID / Secret Access Key。
- 确认挂载和 WebDAV endpoint 都已启用。
- 使用 UI 中的 S3 测试和 WebDAV 测试按钮。

## License

本项目使用 MIT License。
