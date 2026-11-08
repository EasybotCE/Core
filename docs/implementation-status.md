# 实现状态与 v1 差距

本页描述仓库当前可验证的实现，不把架构目标或已定义的契约误报为已交付功能。项目仍处于开发阶段，不能称为完整 v1。

## 已实现

- Go 常驻守护进程：严格 YAML/环境引导配置、管理员 Bearer、优雅关闭、health/readiness、结构化日志。
- SQLite（modernc、无 CGO）、MySQL、PostgreSQL 存储驱动；端点、凭据、规则组、路由绑定持久化。SQLite 路径有本地集成测试；MySQL/PostgreSQL 容器集成测试已写入 CI，当前开发环境未运行 Docker 验证。
- 端点管理和凭据签发/撤销 API；凭据只存摘要，签发明文只返回一次。
- 规则组及多对多方向路由绑定 CRUD；include/exclude、正则转换、模板渲染。
- CE WebSocket 接入：凭据绑定身份、能力交集、单端点会话、速率限制、会话内消息去重、限长队列和路由投递。队列确认只表示 Hub 已入队，不表示远端已收到或执行。
- `cmd/mc-simulator` 可作为协议客户端发布/接收消息，并有连接真实 Hub 的集成测试。
- OpenAPI 3.1、端点 JSON Schema、消息样例；CI 包含 sqlc 生成校验、普通测试、无 CGO 构建及 Linux race job 定义。
- 管理 API 不依赖 Web UI；管理员 Bearer 保护的 `/metrics` 暴露 HTTP 请求计数、处理耗时汇总和进程 uptime。SDK 与 MC 插件不属于本仓库交付物。

## OneBot 11 当前进度

Hub 已实现 OneBot 11 反向 WebSocket 初版：`chat-bot` endpoint credential 通过 Authorization Bearer 认证后连接 `/onebot/v11/ws`；支持群/私聊文本事件解析、有限窗口的连接内去重、复用路由规则投递到 CE session，以及把路由到 BOT 的 CE 消息编码为 `send_group_msg` / `send_private_msg`。该实现仍不等于完整 OneBot 产品能力：正向 WebSocket、Hub 主动连接 BOT 的配置/管理 API、连接重试编排及 action 自动重试未实现；反向连接 action 状态与回执已持久化，支持按端点持久分页查询（复合时间/ID 游标，可覆盖同秒记录）；Hub 重启时未终态 action 标为 timeout（结果未知，不自动重试）。详见 [OneBot 11 对接文档](onebot11.md)。

## 玩家策略与审计增量

`/v1/player-policies` 支持管理员创建、查询、删除按服务器或全局作用域的 allow/deny 策略，可设置 kick/ban/mute 动作、原因及 Unix 过期时间（0 为永不过期）。服务器插件以带有 `players.state` 能力的端点凭据调用 `POST /plugin/v1/player-policy`，请求仅含玩家 ID，Hub 从凭据确定服务器身份并返回有效策略及决策；deny 优先，存在 allow 规则时采用白名单默认拒绝。Hub 不直接踢人/封禁，插件团队负责执行并回报状态。管理员还可通过 `/v1/commands` 配置全局/服务器作用域文本命令；具备 `commands.execute` 能力的 `chat-bot` 可调用 `/plugin/v1/commands/execute` 为目标服务器解析命令和模板，MC 插件仍负责完成 console/player 权限上下文中的实际执行。支持 `{{player}}`、`{{server}}`、`{{args}}` 三个占位符；命令创建/删除也写入该审计表。策略变更写入 `ce_audit_events`；`GET /v1/audit/events` 可读最近记录。该审计目前覆盖此策略管理域，不应视作全管理 API 审计。SQLite、MySQL、PostgreSQL 共用新增表，schema ledger 版本为 5。

## 账号绑定、状态与占位符增量（schema v4）

新增游戏内 5 分钟一次性 challenge、聊天 BOT 兑换绑定，及按 MC endpoint/player 与 BOT endpoint/platform user 的唯一约束。Hub 仅信任经 BOT 原生事件校验的 `platform_user_id`。管理员可查询/解绑并审计解绑。插件可上报在线状态、世界和坐标，并通过 resolve API 取得玩家/服务器/绑定身份占位符。状态为最后观测值，不自动过期；上线强制检查、退群解绑、昵称同步及审计绑定创建均未实现。完整字段、流程和安全边界见 [账号绑定与玩家状态](accounts-and-player-state.md)。

## 当前 CE 消息能力边界

协议实现了 `session.hello`、`session.welcome`、`session.error`、`message.publish`、`message.deliver`、`message.accepted`。当前能力只有 `message.publish` 和 `message.receive`。发布事件经匹配的启用路由与规则处理后进入目标端点的内存队列。消息日志默认关闭；当前没有持久化投递、远端执行确认或跨重启去重。客户端不得把接收消息重新发布，否则会形成回环；需要按集成场景实施来源过滤。

## 尚未实现的完整 v1 能力

| 领域 | 完整 v1 验收要求 | 当前状态 |
| --- | --- | --- |
| 原生 BOT 接入 | OneBot11 正向与反向 WebSocket | 反向 WebSocket 已有初版，可鉴权并双向路由文本；持久 action 状态查询及按端点分页已实现；async 回执不作为终态，重启时未终态转为 timeout；正向连接、独立 BOT 管理、自动重连与自动重试未实现 |
| 消息互通 | 群/服双向同步，过滤、转换、模板可配置 | CE 与 OneBot 反向 WS 已支持路由文本；高级内容、完整业务交互与功能配置未实现 |
| 账号工作流 | 游戏码兑换、绑定、强制检查、退群解绑、昵称同步 | 一次性游戏码兑换、服务器内唯一绑定、管理员解绑/查询已实现；其余未实现 |
| 命令 | 文本/图片自定义命令、控制台/指定玩家身份、明确的权限回退策略 | 已实现持久化文本命令定义、全局/服务器作用域、console/player 执行身份及 `{{player}}`/`{{server}}`/`{{args}}` 渲染 API；图片命令、权限回退与服务器实际执行由插件实现 |
| 管理与惩罚 | 黑白名单、联动封禁/解封 | 已实现持久化 allow/deny、kick/ban/mute policy、过期过滤与插件端策略查询；尚未由 Hub 主动联动服务器执行封禁/解封 |
| AI | OpenAI-compatible 接口、预设、模型、base URL、密钥 | 未实现 |
| 占位符 | 账号与玩家状态变量、占位符解析 | 插件玩家状态上报及结构化 resolve（玩家/服务器/绑定账号变量）已实现；消息模板尚未直接集成 |
| 图片 | rod/chromedp 内嵌无头浏览器驱动模板渲染 | 未实现 |
| 跨服 | 共享规则跨服务器同步 | 规则组可被多个绑定引用；账号/状态工作流及完整跨服同步语义未实现 |
| 插件生态 | 插件 manifest、权限范围、完整插件 API | 只有 CE 消息端点能力；插件 API/manifest 授权体系未实现 |
| 管理可观测性 | 管理事件订阅/过滤、审计、metrics、可选消息日志及留存 | 基础 HTTP metrics 已实现；player-policy 管理变更进入持久化审计并有查询 API；通用管理事件订阅、消息日志及留存未实现 |
| 迁移 | LiteDB C# 导出器与 Hub JSON 导入器 | 未实现 |
| 交付 | Docker、正式发行、完整许可证归档 | Dockerfile/Compose 已加入但未在本机验证（Docker 不可用）；AGPL-3.0 全文已归档；正式发行流程未实现 |
| SDK | 独立 TypeScript 优先 SDK，再提供 Go SDK | 另一个仓库的交付，不在本仓库 |
| MC 插件/MOD | 服务器侧实现与发布 | 由另一团队负责；本仓库提供协议、对接文档和模拟器 |

## 验证状态

当前检查至少应运行：

```powershell
$env:CGO_ENABLED = '0'
go test -mod=readonly -count=1 ./...
go vet -mod=readonly ./...
go build -mod=readonly -trimpath ./cmd/...
```

SQLite 测试可在无 CGO 环境执行。MySQL/PostgreSQL 需要 Docker 的集成测试通过 `-tags integration` 显式运行；Docker 不可用时不能把它们记为通过。CI 的 race job 需以托管平台实际结果为准。

版本信息采用开发版本；Milestone 标识不代表 v1 完成。每项 v1 能力必须以实现、自动化测试、API/协议文档及验收结果共同关闭。








