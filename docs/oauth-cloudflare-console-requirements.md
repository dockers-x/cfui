# 格局判断：cfui OAuth Cloudflare Console

## Thesis

OAuth 模式不应该是 classic cfui 的一个登录补丁，而应该是一套独立的 Cloudflare account console。classic 负责本地能力：cloudflared runner、MCP、S3 WebDAV、本地配置；oauth 负责 Cloudflare 账号资源：按 OAuth scope 显示账号、Zone、DNS、Workers、Storage、WAF、Settings、Analytics 等模块。

## Confidence

- **Confidence level**: high
- **Why not certain**: Cloudflare OAuth scope 与部分产品 API 的可用性会随账号套餐、API 版本和 Cloudflare 后台策略变化，需要用真实 OAuth app 和账号验证。

## The Trap

- **Inherited constraint**: 现有 cfui 把 cloudflared 本地运行、远程 Tunnel 管理、DDNS、MCP、S3 WebDAV 放在一个“Tunnel 工具”模型里。
- **Is it real?**: partially
- **Why**: 本地 runner、MCP、S3 WebDAV 的部署和数据是 cfui 自己拥有的真实约束；但 Cloudflare 账号资源不应该被强绑定到本地 tunnel token 或旧 Remote Tunnel Manager。

## High-格局 Direction

运行模式分成三种。它们只决定默认落点和本地 runner auto-start，不决定另一套工作台是否存在：

- `classic`: 默认进入本地 cfui 工作台，本地 runner 和本地模块优先。
- `oauth`: 默认进入 `/cloudflare` Cloudflare OAuth 工作台，不自动启动本地 cloudflared。
- `both`: 默认进入本地 cfui 工作台，同时保留 Cloudflare 工作台入口。

两套工作台通过路由隔离：`/` 和 `/local` 是本地 cfui，`/cloudflare` 是 OAuth Cloudflare console。用户处在其中一个工作台时，不显示另一套工作台的 tabs、状态或内容，只保留 header 里的切换入口。

Cloudflare OAuth token 直接作为 Bearer token 调 Cloudflare API，不静默申请 API token。OAuth session、refresh token、PKCE state 全部存 SQLite。功能可见性由 `workspace route + local feature flags + OAuth capability matrix` 决定。

## Frame-Opening Move

- **Move used**: kill the wrong concept
- **What it reveals**: “Tunnel 管理”不是一个单一产品。它至少有两个生命周期：本地 cloudflared 进程管理，以及 Cloudflare Tunnel 控制面管理。前者属于 classic，本地 token 驱动；后者属于 OAuth/API token Cloudflare 资源管理。

## Bold Takes

- 不要让 OAuth 模式依赖公网 cfui 服务。OAuth Worker relay 只负责转发 `code/state`，token exchange 在 cfui 后端完成。
- 不要把 OAuth token 写 JSON。SQLite 是唯一持久化层。
- 不要把 MCP/S3 WebDAV 伪装成 Cloudflare OAuth 资源。它们是 cfui 本地能力，可以在 `both` 模式下与 OAuth 控制台并存。
- 不要用 Remote Tunnel Manager 的旧 API token 配置模型承载 OAuth console。OAuth console 应按 scope 显示资源模块。

## Options

| Option | What it optimizes | Cost | Verdict |
| --- | --- | --- | --- |
| Conservative path | 在现有 Remote Tunnel Manager 上加 OAuth 登录 | 继续混淆本地 runner、API token、OAuth token 三个生命周期 | reject |
| Clean target | OAuth 模式重建为 Cloudflare console | 初期 diff 更大，需要能力矩阵和新 UI | target |
| Staged clean path | 先保 classic，再逐步把 OAuth console 模块补齐 | 需要阶段边界清晰，避免半成品误称完成 | recommended |

## Current Stage

已实现：

- `CFUI_RUN_MODE=classic|oauth|both`，默认 `classic`；run mode 控制默认工作台和本地 runner auto-start，不隐藏另一套工作台入口。
- OAuth login/callback/logout/status/config，使用 Worker relay，token 和 PKCE state 存 SQLite；登录前可按已实现模块选择本次授权 scopes，`CFUI_OAUTH_SCOPES` 作为默认模板和兼容 fallback；scope selector 可复制不含 OAuth token 的配置模板、下一次请求 scope、当前身份实际授权 scope 和功能读写能力矩阵。
- 可自部署的 Worker relay 脚本放在 `docs/cloudflare-oauth-worker.js`。Cloudflare OAuth Client 的 Redirect URL 固定填写 Worker 的公网 HTTPS 回调，例如 `https://oauth.omarchy.qzz.io/oauth/callback`，不要追加 `cfui_callback_url`。cfui 在发起登录时把当前浏览器访问到的 `/oauth/callback` URL 编码进 OAuth `state`；Worker 从 state 解析目标后，把 `code`、`state` 或 OAuth 错误转发回对应 cfui 实例。Worker 变量 `CFUI_CALLBACK_URL` 仅作为 fallback；一个 Worker 服务公网 cfui 域名时，通过 `CFUI_ALLOWED_CALLBACK_ORIGINS` 配置来源白名单，或明确设置 `*` 作为多用户 relay。Worker 提供 `/health`，cfui 通过 `/api/oauth/relay-check` 从 relay callback URL 推导并检查该健康端点。relay callback 可在 WebUI 保存到 SQLite，保存值覆盖 `CFUI_OAUTH_RELAY_URL`，并要求 URL 为 `http/https`、有 host、路径为 `/oauth/callback`。
- OAuth 未配置时，Cloudflare 工作台只突出单个 relay callback 输入框；输入框旁提供复制、保存和检测动作，检测会通过 `/api/oauth/relay-check` 验证 relay `/health` 是否支持 `state-v1`。输入框下方只保留一行轻量提示和文字动作：“使用提供域名”会填入并保存提供的中转地址，“自建...”只打开弹窗显示自建 Worker 的目标回调 URL、自建步骤和脚本；来源状态与检测结果合并为一行反馈。本地 cfui callback 不在未配置引导中突出展示。引导用户创建 OAuth app 时只强调一个 Cloudflare Redirect callback URL，不展示 `cfui_callback_url` 参数。
- OAuth Cloudflare console 初版 UI，包含对齐 orange-cloud 首页体验的 Overview dashboard；Overview 由后端聚合账号、Zone、DNS、Workers、Tunnel、R2、D1、KV、Snippets、WAF 和 Cloudflare 公开状态，scope 缺失或单个产品 API 失败时只标记对应指标不可用，并可复制不含 OAuth token 的诊断快照用于真实账号/scope 组合回归。Overview 还提供无 token 校验报告，复用只读 API smoke 区分 OAuth app 权限白名单、当前请求/授权 scope 和 Cloudflare API 可用性问题；报告和前端面板包含结构化 action items、缺失 scope、受限 API 和不可用 API，且不包含 OAuth/refresh token、connector token、DNS content、R2 object body、KV value、SQL rows 或 WAF expression。
- 账号、带套餐/名称服务器详情和 orange-cloud 单域名工具 hub 的 Zone、DNS、Cloudflare Tunnel control-plane（状态、类型、remote config、活跃连接明细）、Workers、R2、D1、KV、Snippets、WAF、自选 Zone settings、Cloudflare 官方状态的资源面。Zone 详情按 capability 显示 DNS、Analytics、WAF、Snippets、Settings 入口，并承载常用快捷操作。Cloudflare Go SDK 路径和手写 REST 路径都统一支持后端 endpoint override，便于 fake Cloudflare 端到端验证；账号列表显式按 SDK `ResultInfo.TotalPages` 自动翻页，不只显示第一页；Account 列表、Zone 列表和所选详情可复制不含 OAuth/refresh token 的诊断快照。
- Cloudflare Tunnel 支持通过 OAuth 创建 remote-managed tunnel，后端生成 tunnel secret 并读取 connector token；connector token 不返回前端，只在用户勾选时写入本地 SQLite tunnel profile。OAuth 隧道列表会返回无密钥的本地 profile 摘要，前端能标记远端 tunnel 已关联到哪个本地 profile；删除远端 tunnel 时可同步清理关联的本地 profile；本地 `/local` tunnel profile 列表也会显示对应 Cloudflare Tunnel 的短 ID。OAuth 工作台可读取、创建、编辑、删除和拖拽排序 Cloudflare-hosted tunnel ingress/public hostname 规则，排序写回 Cloudflare Tunnel configuration，并保护 catch-all 规则保留在最后。
- DNS record count、create/update/delete 后端接口、前端表单、本地搜索和 A/AAAA/CNAME 代理状态一键切换；count 通过小分页读取 `result_info.total_count`，不为计数拉全量记录；前端可复制不含 OAuth/refresh token、DNS record content 和 comment 原文的 DNS 诊断快照。
- Zone settings 写操作：`zone-settings.write` 控制 SSL/TLS mode、TLS 1.3、Minimum TLS Version、development mode、security level、cache level、browser cache TTL、Always Use HTTPS、Automatic HTTPS Rewrites、Brotli、Rocket Loader、IPv6、WebSockets、HTTP/2、HTTP/3、Early Hints、Email Obfuscation、Hotlink Protection、Server Side Excludes、Always Online、Browser Integrity Check、IP Geolocation、Opportunistic Encryption、0-RTT；Zone 详情页会把 development mode、security level 和全量 cache purge 下沉成快捷操作；`cache_purge.write` 单独控制全量 cache purge，后端 capability 兼容识别旧的 `cache.purge`；前端可复制不含 OAuth/refresh token 的 Zone Settings 诊断快照。
- R2 bucket 创建/删除，使用 Cloudflare Go SDK bucket 级 API，按 `workers-r2.write` scope 显示；R2 object 列表、已加载对象本地搜索、复制 key、详情元信息面板、受限 UTF-8 预览、非 UTF-8 对象的受限 hexdump 样本预览、50 MiB 内常见图片/音频/视频/PDF 内联预览、文本 object 创建/覆盖、同账号同 bucket/跨 bucket object 复制/移动、128 MiB 内直接文件上传、5 GiB 内浏览器分片上传到 cfui 临时文件后由后端流式写入 R2、同源文件下载、删除和账号级 metrics 通过后端 Cloudflare REST API 实现。OAuth token 只在后端使用，R2 metrics 按需读取，不持久化快照；前端可复制不含 OAuth/refresh token、对象正文、binary preview 和下载 URL 的 R2 诊断快照。
- Workers 列表和详情：通过 Cloudflare Go SDK 读取脚本 metadata、settings、Tail consumers 和受大小限制的只读脚本内容预览；当前不提供 Worker 脚本部署/编辑 UX，因此 capability matrix 和 scope 选择器不报告或申请 `workers-scripts.write`；Worker 详情可复制不含 OAuth/refresh token、脚本正文和 Tail 事件原文的诊断快照。
- Workers metrics：通过 Cloudflare GraphQL Analytics API 提供单 Worker 24h/7d/30d 请求、错误、子请求、CPU、调用状态分解和时间序列；GraphQL 调用只在后端使用当前 OAuth token。
- Account Usage dashboard：通过后端 GraphQL Analytics API 查询所选账号的 Workers、近 1 小时 Workers 错误数、R2 操作数、D1、KV 用量；R2 存储和对象数 best-effort 用 REST `accounts/{account_id}/r2/metrics` 的 Standard class 快照覆盖，口径与 orange-cloud 一致。后端 best-effort 调用 `accounts/{account_id}/subscriptions` 派生计费周期和 Workers/R2 付费套餐上下文。Cloudflare OAuth 通常没有 billing scope，订阅接口 403 要降级为 `billing.available=false`，不能阻塞 usage 数据；OAuth token 只在后端使用，不持久化 usage 快照；前端可复制不含 OAuth/refresh token 的 Usage 诊断快照，用于区分 GraphQL、R2 metrics、billing 和 scope 问题。
- KV namespace 创建/重命名/删除，key 列表、已加载 key 批量删除、UTF-8 value 查看/编辑/删除、文本 key 创建；非 UTF-8 binary value 返回受限 hexdump 样本预览，并可通过同源后端代理上传/下载原始字节，仍避免在文本编辑器里误写二进制；前端可复制不含 OAuth/refresh token、KV value、binary preview 和下载 URL 的 KV 诊断快照。
- D1 数据库创建/删除、详情（file size / table count / jurisdiction / running region / read replication）回填、创建时可选 jurisdiction、primary location hint 和 read replication mode；SQL 查询控制台、table 浏览、分页行加载、按列类型显示数字/布尔/日期时间/文本输入的行编辑器；可空列能显式写回 SQL NULL，非空值仍通过参数化更新提交，删除按 rowid 参数化执行；前端可复制不含 OAuth/refresh token、SQL 文本、SQL 参数、查询结果行和表格行数据的 D1 诊断快照。
- Snippets 创建/删除、既有代码正文读取/编辑、触发规则列表/新增/启停/删除；代码正文通过后端低层 Cloudflare REST `/content` endpoint 读取，写入复用 Cloudflare Go SDK multipart update，规则更新采用 read-modify-write 整组回写。Snippet rule 表达式编辑器带常见 host/path/method/IP/country 片段 helper，可追加或替换现有表达式；前端可复制不含 OAuth/refresh token、Snippet 代码正文、草稿代码、规则表达式和规则描述正文的 Snippets 诊断快照。
- WAF 自定义规则列表/新增/编辑/启停/删除；规则更新采用 entrypoint ruleset read-modify-write，未修改字段会保留既有 action parameters、rate limit、logging、exposed credential check 等高级结构。简单编辑器开放 `block`、`challenge`、`managed_challenge`、`js_challenge`、`log` 这类无需 action parameters 的安全子集，并支持 `skip` 的 current ruleset、products、phases 参数；表单也提供常用 `ratelimit` 字段的专用速率限制构建器。既有复杂规则会展示 ref/version、raw action parameters、rate limit、logging 和 exposed credential check，并可复制审计 JSON；同时提供 Advanced JSON 编辑 `action_parameters`、`ratelimit`、`logging`、`exposed_credential_check`，未修改字段不提交，写入 `null` 清空字段。Custom WAF、Managed Exceptions 和 Managed Overrides 的 expression textarea 都带常见 host/path/method/IP/country 表达式 helper，可追加或替换现有表达式。WAF Managed Ruleset Overrides 单独读取和写入 `http_request_firewall_managed` entrypoint ruleset，只管理 `execute` 规则，可设置托管规则集 ID、规则集 action/enabled/sensitivity 覆盖、category/tag 覆盖和具体 managed rule 覆盖。WAF Managed Exceptions 单独读取和写入同一 phase，只管理 `skip` 规则，可跳过当前托管规则集、指定 managed rulesets 或指定 ruleset 下的 managed rule IDs；前端可复制不含 OAuth/refresh token、表达式、描述、原始 action parameters、托管规则 ID、审计 JSON 和高级 JSON 的 WAF 诊断快照。
- Zone Analytics dashboard：通过 Cloudflare Go SDK dashboard endpoint 提供 24h/7d/30d 请求、带宽、威胁、页面浏览、独立访客和缓存命中汇总。
- Cloudflare Status dashboard：通过后端读取 Cloudflare Statuspage v2 公开 API，展示总体状态、受影响产品、边缘大区、进行中事件、计划维护和最近事件；不需要 OAuth scope，不使用 OAuth token；前端可复制不含 OAuth/refresh token 和 incident update 正文的 Status 诊断快照。
- Worker Tail streaming：通过 Cloudflare Go SDK 创建 tail session，由 cfui 后端连接 Cloudflare `trace-v1` WebSocket，再以同源 SSE 代理给前端；前端不接触 OAuth token 或 Cloudflare 预签名 WebSocket URL。
- OAuth capability matrix 和前端 scope 门控。
- OAuth 前端保持原生 JS，不换框架；静态数据/permission 定义放在 `app-oauth-data.js`，OAuth relay callback 与 setup guide 渲染放在 `app-oauth-setup.js`，主 `app-oauth.js` 继续承载账号资源工作台和业务回调。
- 多 OAuth identity 保存、移除和当前 identity 切换；当前 identity 决定所有 Cloudflare account resource API 调用。已登录时添加新 identity 要支持 fresh-login URL：先进入 Cloudflare logout endpoint，再跳转到 authorize，避免浏览器 Cookie 静默复用当前账号。
- classic 模式下原本本地功能默认保持不变；oauth 模式不自动启动本地 cloudflared，但本地工作台仍可通过 `/local` 使用。

## Orange Cloud Capability Matrix

| Product area | Orange Cloud baseline | cfui OAuth current state | Next gap |
| --- | --- | --- | --- |
| OAuth identity | OAuth 2.0 + PKCE, per-scope selection, multiple accounts | OAuth Worker relay, backend token exchange, SQLite session/state storage, multi-identity switch/rename/remove, scope selector modal with no-token scope/capability matrix export, account list no-token diagnostics export, and Overview no-token scope/API validation report with action items | Run validation report on real OAuth apps and archive the no-token report for allowlist vs granted-scope regressions |
| Dashboard | Account dashboard, recent zones, quick actions, account switch | Overview endpoint aggregates accounts, zones, DNS, Workers, Tunnel, R2, D1, KV, Snippets, WAF, public status, capability-aware quick actions into account and zone resource pages, a copyable no-token diagnostics export, and a no-token validation report with readable action items; missing scopes degrade per metric | Real-account tuning for large accounts and noisy permission mixes |
| Domains / DNS | Zone list/detail, DNS CRUD, proxy toggle, zone settings | Zone list/detail, plan/name servers, no-token Zone diagnostics export, DNS count/list/search/create/update/delete/proxy toggle, no-token/no-record-content diagnostics export, broader common zone settings catalog, cache purge, and no-token Zone Settings diagnostics export | Real-account validation for setting editability across plans and zones |
| Analytics | Zone traffic via GraphQL, 24h/7d/30d | Zone analytics summary with copyable export text, request trend bars, and no-token diagnostics export; single Worker metrics; account usage dashboard with GraphQL plus R2 REST metrics and no-token diagnostics export | Deeper visual chart polish only if real accounts show the compact bars are insufficient |
| Workers | Script list/detail, read-only detail, live tail | Worker list/detail, script preview, settings, tail consumers, worker metrics, backend SSE live tail proxy, and no-token/no-script diagnostics export; `workers-scripts.write` is intentionally not requested or reported until a safe deploy UX exists | Real-account validation of read-only script preview, metrics, and tail behavior |
| Snippets | View/edit/create snippets and trigger rules | Snippet list/create/delete, content read/write, rules create/enable/disable/delete, common expression helper for host/path/method/IP/country matching, and no-token/no-code/no-rule-expression diagnostics export | Real-account tuning for complex matching expressions |
| Storage / R2 | Bucket/object browsing | Bucket list/create/delete, object list/search/preview/create/direct upload/chunked large upload/copy/download/delete, account metrics, and no-token/no-object-content diagnostics export | Native R2 multipart only if OAuth REST exposes it or users provide S3 credentials |
| Storage / D1 | Database list, SQL console | Database list/create/delete/detail including placement metadata, create-time jurisdiction / primary location hint / read replication options, SQL console, table browser, type-aware row editor with explicit SQL NULL handling for nullable columns, row update/delete, and no-token/no-row-data diagnostics export | Real-account validation of placement option availability across plans and jurisdictions |
| Storage / KV | Namespace/key list, value create/edit/delete | Namespace create/rename/delete, key list, loaded-key bulk delete, UTF-8 value create/edit/delete, binary value hexdump preview/upload/download, and no-token/no-value diagnostics export | Optional raw byte editor only if real KV use cases need in-browser binary editing |
| Security / WAF | Custom WAF view/toggle | Custom WAF create/edit/enable/disable/delete plus managed overrides/exceptions, advanced JSON preservation, rate-limit builder, skip parameter builder, common expression helper, and no-token/no-expression diagnostics export | Real-account validation of expression presets and managed ruleset edge cases |
| Network / Tunnel | Tunnel status and public hostnames | Tunnel list/details/connections, create remote-managed tunnel, link local profile without exposing connector token, expose linked local runner status, delete remote tunnel with optional local profile cleanup, public hostname/ingress CRUD and drag reorder with local connector follow-up hints, plus no-token/no-connector-token diagnostics export for remote config and local profile state | Real-account validation for how quickly running cloudflared connectors pick up remote config changes |
| Cloudflare Status | Public service status | Public Statuspage v2 dashboard via backend; no OAuth token required, with no-token/no-incident-update-body diagnostics export | None |

## Follow-Up Stages

下一阶段：

- R2 native multipart 路线验证。当前 object workflow 已覆盖列表、已加载对象本地搜索、复制 key、详情元信息、文本预览/写入、非 UTF-8 对象 hexdump 样本预览、50 MiB 内常见媒体/PDF 预览、同账号同 bucket/跨 bucket 复制移动、128 MiB 内直接上传、5 GiB 内浏览器到 cfui 的分片上传、同源下载和删除；如果 Cloudflare OAuth REST 后续开放 multipart 或用户提供 R2 S3 凭证，再补原生 multipart/resume。

## What Not To Do

- 不要为了复用旧 UI，把 OAuth Cloudflare 资源塞进 Tunnel Manager 页面。
- 不要把所有 Cloudflare API 错误都显示成同一种失败；套餐不支持、scope 缺失、资源为空要区分。
- 不要在前端保存 OAuth token。
- 不要默认请求未实现功能的 scope；新增模块时再扩大默认 scope 或让用户显式配置。

## First Proof Point

在 `CFUI_RUN_MODE=oauth` 下，访问 `/` 会落到 `/cloudflare` 工作台；未配置 OAuth client 时页面能显示 OAuth onboarding；配置 OAuth client 并登录后，`/api/features` 返回能力矩阵，前端只显示当前 token 授权的 Cloudflare 资源模块，且不显示本地 cfui tabs/content。

## Falsifier

如果 Cloudflare OAuth bearer token 不能访问关键账号资源 API，或必须依赖公网 cfui 回调才能完成 OAuth，则当前 “local cfui + Worker relay + backend token exchange” 的目标模型需要调整。
