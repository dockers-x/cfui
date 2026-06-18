# cfui

中文说明 | [English](README.md)

cfui 是一个用于管理 Cloudflare Tunnel（`cloudflared`）的 Web 控制台。它可以运行本地隧道进程，也可以通过 Cloudflare API 管理远程 Tunnel ingress 规则，支持 DDNS、S3 兼容存储的 WebDAV 挂载，以及供 AI 客户端使用的 MCP 端点。

Web UI 已内置在二进制文件中。配置保存在数据目录下的本地 SQLite 数据库里。

## 致谢

- [LinuxDo 社区](https://linux.do)

## 功能

- **本地 Cloudflare Tunnel 运行管理**
  - 在浏览器里管理多个 Cloudflare Tunnel 配置。
  - 粘贴 Cloudflare Tunnel token，并独立编辑每个已保存配置。
  - 每个 tunnel 配置都可以独立启动或停止，多个配置可以同时运行。
  - 支持自动启动、异常自动重启、协议、区域、重试次数、优雅关闭时间、metrics、后量子模式、边缘 IP 版本、边缘绑定地址、TLS 校验和额外 cloudflared 参数。
  - 显示隧道状态、当前协议、最近错误和版本构建信息。

- **远程 Tunnel 管理**
  - 可选功能，用于管理 Cloudflare 上托管的 Tunnel ingress 配置。
  - 可以选择任意已保存的 tunnel 配置进行远程管理。
  - 通过 Account ID 和 Tunnel ID 加载已有 Tunnel。API 凭据共用，Account ID 和 Tunnel ID 可以按 tunnel 配置分别保存。
  - 可以通过 Cloudflare API 读取 Tunnel 名称，并在本地配置仍是自动生成名称时自动写回。
  - 添加、编辑和删除 public hostname 规则，支持 hostname、path、服务类型、服务地址、Host Header、Origin Server Name 和 TLS 校验选项。
  - 能在可解析时从所选 Tunnel token 自动带出 Account ID 和 Tunnel ID。
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
  - 独立 WebDAV 模式支持手动启动/停止、可选自动启动、直连端口、自定义公开 URL，以及通过默认 tunnel 配置跳转到 Cloudflare Tunnel 规则创建。
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

- **Cloudflare OAuth 控制台**
  - 可选运行模式，用 Cloudflare OAuth access token 管理 Cloudflare 账号资源。
  - 根据已授权 scope 显示账号、带套餐/名称服务器详情的 Zone、DNS 记录、Cloudflare Tunnel 控制面记录、Workers、R2、D1、KV、Snippets、WAF、Cloudflare 官方状态和部分 Zone 设置。
  - OAuth session 和 PKCE state 保存到本地 SQLite 数据库，不写 JSON 文件。
  - 可以保存并切换多个 OAuth 身份；当前身份决定所有 Cloudflare API 调用。
  - 本地 cloudflared 运行器、MCP、S3 WebDAV 等 cfui 本地能力仍然独立。

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

1. 在 Cloudflare Zero Trust 中创建或选择一个或多个 Cloudflare Tunnel。
2. 从隧道安装命令里复制 tunnel token。
3. 在 Tunnel Configuration 页面把 token 粘贴到一个 tunnel 配置里。
4. 保存配置。
5. 从 UI 启动该 tunnel 配置；需要运行其他配置时重复操作即可。

本地隧道运行不需要 Cloudflare API 凭据。只有远程 Tunnel 管理、DDNS 和可选的 R2 bucket 管理需要 API 凭据。

## Tunnel 配置

cfui 可以保存多个 tunnel 配置。每个配置包含一个本地运行 token，以及对应 Cloudflare Tunnel 的远程管理身份信息。

- 每个配置都可以独立编辑、启动、停止和重启。
- 只要配置不冲突，多个本地 tunnel 配置可以同时运行。
- 远程 Tunnel 管理会使用所选配置的 Account ID 和 Tunnel ID，并复用共享的 Cloudflare API 凭据。
- 如果某个配置没有填写 Account ID 或 Tunnel ID，cfui 会尝试从该配置的 tunnel token 中解码。
- S3 WebDAV 的 Cloudflare Tunnel 发布使用保留下来的默认 tunnel 配置，用于兼容旧接口和默认集成。

## Cloudflare API 权限

远程 Tunnel 管理和 DDNS 使用 Cloudflare API 凭据。推荐使用 API Token。

远程 Tunnel 管理和 DDNS 需要：

- `Account -> Argo Tunnel (Legacy) -> Edit`
- `Zone -> Zone -> Read`
- `Zone -> DNS -> Edit`

R2 bucket 列表/创建的可选权限：

- `Account -> Workers R2 Storage -> Edit`

S3 WebDAV 的文件访问不使用 Cloudflare API token。它使用 S3 WebDAV 页面里配置的 S3 Access Key ID 和 Secret Access Key。

## Cloudflare OAuth 模式

cfui 支持三种顶层运行模式。运行模式只决定默认进入哪个工作台，以及本地 cloudflared runner 是否自动启动；不会把另一套工作台从进程里移除。

- `classic`：默认模式。进入本地 cfui 工作台，并自动启动符合条件的本地 tunnel profile。
- `oauth`：进入 Cloudflare OAuth 工作台，不自动启动本地 tunnel runner。
- `both`：进入本地 cfui 工作台，同时保留 Cloudflare 工作台入口。

两套工作台通过路由隔离：

- `/` 和 `/local`：本地 cfui 工作台，包含 cloudflared runner、Remote Tunnel Manager、DDNS、MCP、S3 WebDAV 和功能设置。
- `/cloudflare`：Cloudflare OAuth 工作台，用于管理 Cloudflare 账号资源。本地工作台只作为切换入口出现。

通过环境变量设置：

```bash
CFUI_RUN_MODE=oauth ./cfui
```

OAuth 模式会直接使用当前 OAuth 身份的 bearer token 调用 Cloudflare API，不会替用户创建 API token。cfui 可以把多个 OAuth 身份保存到 SQLite，并在 OAuth 控制台里重命名、切换或移除；OAuth session 和 PKCE state 不会写入 JSON。每次登录前，OAuth 控制台都可以按已实现模块生成本次身份要申请的 scope 集合；已登录时添加身份会先通过 Cloudflare logout endpoint 打开授权页，避免浏览器 Cookie 静默复用当前账号；`CFUI_OAUTH_SCOPES` 仍作为默认模板和直接访问 `/oauth/start` 时的 fallback。OAuth 未配置时，Cloudflare 工作台会展示当前 relay callback、本地 callback、Worker 脚本路径、Worker 变量、relay 健康检查和 cfui 环境变量，引导完成配置。Cloudflare 页面是否可用取决于当前 OAuth app session 授权的 scopes；MCP 和 S3 WebDAV 保留在本地 cfui 工作台，不会显示在 `/cloudflare` 里。

当前 OAuth 控制台已提供对齐 orange-cloud 的 Overview dashboard、账号、带套餐/名称服务器详情的 Zone、DNS 记录、Cloudflare Tunnel 控制面记录（状态、类型、remote config、活跃连接明细）、Workers 脚本、R2 bucket、object 和账号级指标、D1 数据库、KV namespace、Zone 级 Snippets、自定义 WAF 规则、托管 WAF 规则集覆盖和托管 WAF 例外、Zone Analytics、账号用量、Cloudflare 官方状态和部分 Zone 设置。Overview 通过一个后端端点聚合账号、Zone、DNS、Workers、Tunnel、R2、D1、KV、Snippets、WAF 和公开状态计数；scope 缺失或单个产品 API 失败会显示为不可用指标，不会拖垮整个控制台。DNS 记录支持本地搜索；授权包含 `dns.write` 时，A/AAAA/CNAME 可在列表中一键切换代理状态，DNS 记录也支持创建、更新和删除。Workers 支持列表和详情，详情会展示脚本元数据、设置、Tail consumers，并在授权包含 `workers-scripts.read` 时展示受大小限制的只读脚本预览；授权包含 `analytics.read` 或 `account-analytics.read` 时，通过 GraphQL Analytics API 展示单 Worker 指标；授权包含 `workers-tail.read` 时，可通过后端 SSE 代理查看 Live Tail 实时流。授权包含 `workers-r2.write` 时，R2 bucket 支持创建和删除；R2 object 列表、已加载对象本地搜索、受限制 UTF-8 预览、非 UTF-8 对象的受限 hexdump 样本预览、通过同源下载代理展示常见图片/音频/视频/PDF 预览、文本 object 创建/覆盖、单次 128 MiB 文件上传、同源文件下载、删除和账号级存储指标都通过后端 Cloudflare REST 调用提供，因为当前 Cloudflare Go SDK 只提供 bucket 级 R2 API。R2 指标按需读取，不在本地持久化快照。KV namespace 可进入 key 列表，查看 UTF-8 文本值，并在授权包含 `workers-kv-storage.write` 时编辑/删除值、创建文本 key。D1 数据库已提供 SQL 查询控制台、表浏览、分页行加载，并在授权包含 `d1.write` 时支持参数化更新/删除行。授权包含 `snippets.write` 时，Snippets 支持创建/删除、通过后端 `/content` REST 端点读取并编辑既有 snippet 代码正文，并支持触发规则的新增、启停和删除。授权包含 `zone-waf.write` 时，自定义 WAF 规则支持创建、编辑、启停和删除。WAF 简单编辑器支持 `block`、`challenge`、`managed_challenge`、`js_challenge`、`log`、`skip` 这些安全 action 子集；`skip` 规则支持 Cloudflare action parameters 里的 current ruleset、products 和 phases，表单也提供常用 `ratelimit` 字段的专用速率限制构建器。既有复杂 WAF 规则也可以通过 Advanced JSON 区块编辑 `action_parameters`、`ratelimit`、`logging`、`exposed_credential_check`；未修改的高级 JSON 字段不会提交，写入 `null` 会清空对应字段。WAF 更新采用 read-modify-write，未修改字段会保留既有高级结构。托管 WAF 规则集覆盖隔离在 `http_request_firewall_managed` 阶段，只创建或编辑带托管规则集 ID 的 `execute` 规则，并提供规则集、标签/分类和托管规则三层覆盖字段。托管 WAF 例外隔离在同一阶段，只创建或编辑 `skip` 规则，可跳过当前托管规则集、指定托管规则集或指定规则集下的托管规则 ID。复杂规则仍会展示 ref/version、raw action parameters、rate-limit、logging 和 exposed credential check 等元信息，并可复制审计 JSON，便于审计。授权包含 `analytics.read` 或 `account-analytics.read` 时，Zone Analytics 通过 Cloudflare Go SDK dashboard endpoint 展示 24h/7d/30d 流量汇总；账号用量通过后端 GraphQL Analytics API 查询展示所选账号的 Workers、近 1 小时 Workers 错误数、R2 操作数、D1、KV 用量，其中 R2 存储和对象数会 best-effort 用 REST `accounts/{account_id}/r2/metrics` 的 Standard class 快照覆盖，口径与 orange-cloud 一致；同时后端会 best-effort 调用 `accounts/{account_id}/subscriptions`，用于显示计费周期和 Workers/R2 是否为付费套餐。Cloudflare OAuth 通常没有开放 billing scope，因此订阅接口返回 403 时只会显示计费上下文不可用，不会阻塞用量数据。Cloudflare 状态通过 cfui 后端访问公开 Statuspage API，不需要 OAuth scope。授权包含对应 settings/cache scopes 时，Zone 设置支持 SSL/TLS 模式、development mode、security level、cache level、browser cache TTL、Always Use HTTPS、Automatic HTTPS Rewrites、Brotli、Rocket Loader 和全量 cache purge。大文件断点/分片 R2 上传仍属于后续阶段。当前 OAuth token 不包含所需 scope 时，UI 会隐藏对应模块。

R2 object 详情视图还会提供复制 key、复制对象和移动对象操作，支持同账号跨 bucket 复制/移动，并展示大小、Content-Type、ETag、存储类型、最后修改时间和编码状态等元信息。非 UTF-8 对象会在详情里显示受限 hexdump 样本。媒体/PDF 内联预览上限为 50 MiB，超过后只提供下载查看；上传表单会直接禁用超过当前 128 MiB 单次上传上限的文件。

使用 OAuth 模式：

1. 创建 Cloudflare OAuth app，并把 redirect URI 设置为 Worker relay 地址。
2. 设置 `CFUI_OAUTH_CLIENT_ID` 为 OAuth client ID。
3. `CFUI_OAUTH_RELAY_URL` 可以保持默认 relay（`https://oauth.omarchy.qzz.io/oauth/callback`），也可以换成你自己的 Worker relay。
4. Worker relay 需要把 `code` 和 `state` 转发回 cfui 实例的 `/oauth/callback`，本地使用时通常是 `http://127.0.0.1:14333/oauth/callback`。可直接部署的 Worker 脚本在 `docs/cloudflare-oauth-worker.js`；如果 cfui callback 是固定地址，在 Worker 变量里设置 `CFUI_CALLBACK_URL`；如果要按本次运行动态指定 callback，可以在 `CFUI_OAUTH_RELAY_URL` 后追加 `?cfui_callback_url=<urlencoded cfui callback>`，例如 `CFUI_OAUTH_RELAY_URL=https://oauth.omarchy.qzz.io/oauth/callback?cfui_callback_url=https%3A%2F%2Fcfui.example.internal%2Foauth%2Fcallback`。如果参数指向公网 origin，还需要把 Worker 变量 `CFUI_ALLOWED_CALLBACK_ORIGINS` 设置为逗号分隔的 origin 白名单；loopback 和局域网 callback host 默认允许。Cloudflare OAuth app 里的 redirect URI 要和实际使用的 relay callback URL 匹配，使用 `cfui_callback_url` 时也包括这段 query string。Worker 会暴露 `/health`，cfui 可在 OAuth setup 页面或通过 `GET /api/oauth/relay-check` 检查它。

Zone 概览会用 `GET /api/cf/dns/count` 读取 DNS 记录总数，避免为了计数加载全部记录。D1 会先加载数据库列表，再 best-effort 调用 `GET /api/cf/d1/databases/{database_id}` 回填每个数据库的表数量和文件大小，使展示口径与 Cloudflare 详情端点一致。详情回填失败不会阻塞数据库列表、SQL 控制台或表浏览。

默认 OAuth scope 模板：

```text
account-settings.read zone.read dns.read dns.write cloudflare-tunnel.read
```

可以通过 `CFUI_OAUTH_SCOPES` 覆盖。

如果要创建 Cloudflare Tunnel 并关联成本地隧道配置，添加：

```text
cloudflare-tunnel.read cloudflare-tunnel.write
```

如果要使用 Zone settings 操作，可以为 OAuth app 添加对应 scopes，例如：

```text
account-settings.read zone.read dns.read dns.write cloudflare-tunnel.read zone-settings.read zone-settings.write cache.purge
```

如果要使用 R2 bucket 写操作，添加：

```text
workers-r2.read workers-r2.write
```

如果要使用 Worker Tail 实时流，添加：

```text
workers-tail.read
```

如果要使用 Snippets 和触发规则管理，添加：

```text
snippets.read snippets.write
```

如果要使用自定义 WAF 规则管理，添加：

```text
zone-waf.read zone-waf.write
```

如果要使用 Zone Analytics，添加其中之一：

```text
analytics.read account-analytics.read
```

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
| `CFUI_RUN_MODE` / `CFUI_MODE` | `classic`、`oauth` 或 `both` | `classic` |
| `CFUI_TUNNEL_MGMT_ENABLED` / `CFUI_TUNNEL_MANAGEMENT_ENABLED` | 启用远程 Tunnel 管理 | 未设置 |
| `CFUI_TUNNEL_ACCOUNT_ID` / `CLOUDFLARE_ACCOUNT_ID` / `CLOUDFLARE_APP_ID` | Cloudflare account ID | 未设置 |
| `CFUI_TUNNEL_ID` / `CLOUDFLARE_TUNNEL_ID` | Cloudflare tunnel ID | 未设置 |
| `CFUI_TUNNEL_API_TOKEN` / `CLOUDFLARE_API_TOKEN` | Cloudflare API token | 未设置 |
| `CFUI_TUNNEL_API_EMAIL` / `CLOUDFLARE_API_EMAIL` | 使用 Global API Key 时的 Cloudflare 账号邮箱 | 未设置 |
| `CFUI_TUNNEL_API_KEY` / `CLOUDFLARE_API_KEY` | Cloudflare Global API Key | 未设置 |
| `CFUI_OAUTH_CLIENT_ID` | Cloudflare OAuth client ID | 未设置 |
| `CFUI_OAUTH_RELAY_URL` / `CFUI_OAUTH_REDIRECT_URI` | 在 Cloudflare 注册的 OAuth Worker relay callback URL | `https://oauth.omarchy.qzz.io/oauth/callback` |
| `CFUI_OAUTH_SCOPES` | 空格分隔的 OAuth scope 默认模板，用于初始化登录前选择器和直接访问 `/oauth/start` | `account-settings.read zone.read dns.read dns.write cloudflare-tunnel.read` |
| `CFUI_OAUTH_AUTH_URL` | Cloudflare OAuth authorization endpoint 覆盖值 | Cloudflare 默认值 |
| `CFUI_OAUTH_TOKEN_URL` | Cloudflare OAuth token endpoint 覆盖值 | Cloudflare 默认值 |
| `CFUI_OAUTH_REVOKE_URL` | Cloudflare OAuth revoke endpoint 覆盖值 | Cloudflare 默认值 |
| `CFUI_OAUTH_USERINFO_URL` | Cloudflare OAuth userinfo endpoint 覆盖值 | Cloudflare 默认值 |

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

旧版单 tunnel 配置会迁移为默认 tunnel 配置。Tunnel 配置保存在 `tunnel_profiles` 表中，默认配置 key 会保存在 app settings 中，用于旧单 tunnel 接口和默认集成。

## API 概览

主要接口：

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
- `POST /api/oauth/login`
- `POST /api/oauth/logout`
- `POST /api/oauth/session`
- `PATCH /api/oauth/session`
- `GET /api/version`

可选模块接口：

- `/api/cf/*`
- `/api/tunnel-manager/*`
- `/api/ddns/*`
- `/api/mcp/*`
- `/api/s3/*`
- `/mcp`
- `/webdav/*`

远程 Tunnel 管理接口支持可选的 `tunnel_key` 查询参数，例如 `/api/tunnel-manager/config?tunnel_key=office`，因此可以管理任意已保存配置的远程 ingress 规则。`GET /api/tunnel-manager/tunnel?tunnel_key=office` 可读取 Cloudflare Tunnel 元数据，例如 tunnel 名称。

OAuth Cloudflare 接口：

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
- `GET /api/cf/d1/databases/{database_id}`
- `POST /api/cf/d1/query`
- `GET /api/cf/d1/tables`
- `GET /api/cf/d1/table`
- `PATCH /api/cf/d1/table`
- `DELETE /api/cf/d1/table`
- `GET /api/cf/kv/namespaces`
- `GET /api/cf/kv/keys`
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

- 检查是否选择了正确的 tunnel 配置。
- 检查该配置的 Account ID 和 Tunnel ID。
- 在远程 Tunnel 管理页面校验 API token 权限。
- 如果 Account ID 或 Tunnel ID 为空，确认是否能从所选 tunnel token 自动解码。

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
