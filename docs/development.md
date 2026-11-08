# 运行与开发

本指南面向 Hub 开发者与部署者。当前服务使用数据库保存端点、凭据、规则组和路由绑定；YAML 仅保存启动参数。示例使用 PowerShell，命令在仓库根目录执行。

## 前置条件

- Go 1.27。
- 默认 SQLite 不需要外部服务；SQLite 文件所在目录必须可写且应持久化。
- MySQL/PostgreSQL 需要预先创建数据库，并提供驱动要求的 DSN。
- Docker 仅在运行 MySQL/PostgreSQL 集成测试时需要。

## 启动 Hub

```powershell
$bytes = New-Object byte[] 32
$rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
$rng.GetBytes($bytes)
$rng.Dispose()
$env:EASYBOTCE_ADMIN_TOKEN = [Convert]::ToBase64String($bytes)
go run ./cmd/hub
```

默认监听 `127.0.0.1:8080`，默认使用当前目录下的 `easybotce.db`。生产环境应使用 TLS 反向代理或安全的私网链路，不要把明文 WS/HTTP 暴露到不可信网络。设置 `EASYBOTCE_LISTEN_ADDR=0.0.0.0:8080` 前，必须先配置网络访问控制与 TLS 终止。

准备 YAML 时，复制 [`config/example.yaml`](../config/example.yaml)，并按需设置 `database_driver` 与 `database_dsn`。MySQL/PostgreSQL 必须显式配置 DSN；敏感值优先通过环境变量注入：

```powershell
$env:EASYBOTCE_DATABASE_DRIVER = 'postgres'
$env:EASYBOTCE_DATABASE_DSN = 'postgres://user:password@127.0.0.1:5432/easybotce?sslmode=require'
go run ./cmd/hub -config config.local.yaml
```

配置优先级为默认值 < 显式 YAML < 环境变量。仅通过 `-config` 指定的文件才会被读取。数据库连接池目前限制为单连接；该部署假设一个 Hub 进程独占业务数据库。SQLite 默认启用 WAL 与外键约束。

## 探针与管理 API

- `GET /healthz`：进程存活探针，不检查数据库。
- `GET /readyz`：数据库就绪检查。
- `/v1/*`：要求 `Authorization: Bearer <admin token>`。
- `GET /v1/system/info`：版本、数据库驱动和会话摘要。
- `GET /openapi.json`、`GET /protocol/schema.json`：内嵌契约路由按实际服务定义开放。
- `GET /v1/gateway`：CE 端点专用 WS，必须使用端点凭据；管理员令牌不能代替端点令牌。

当前管理 API 可创建/编辑/删除端点、签发/撤销凭据、管理规则组和方向路由绑定。完整请求、响应、错误码与鉴权细节以 [`api/openapi.json`](../api/openapi.json) 为准。

### 准备一个最小路由

创建两个 `mc-server` 端点（例如 `lobby` 和 `survival`），分别为它们签发同时包含 `message.publish`、`message.receive` 的凭据。凭据明文仅在签发响应返回一次，应立即安全保存。端点与凭据管理请求使用管理员令牌：

```powershell
$admin = @{ Authorization = "Bearer $env:EASYBOTCE_ADMIN_TOKEN" }
$headers = $admin + @{ 'Content-Type' = 'application/json' }
Invoke-RestMethod http://127.0.0.1:8080/v1/endpoints -Method Post -Headers $headers -Body '{"id":"lobby","type":"mc-server","name":"Lobby","enabled":true}'
Invoke-RestMethod http://127.0.0.1:8080/v1/endpoints/lobby/credentials -Method Post -Headers $headers -Body '{"capabilities":["message.publish","message.receive"]}'
```

对 `survival` 重复创建和签发步骤。随后创建规则组和方向绑定；该规则组可以被多个绑定共享：

```powershell
$rules = '{"id":"shared","name":"共享规则","rules":{"include":"","exclude":"","transforms":[],"template":"[{source_endpoint_id}] {sender_name}: {text}"}}'
Invoke-RestMethod http://127.0.0.1:8080/v1/rule-groups -Method Post -Headers $headers -Body $rules
$binding = '{"id":"lobby-to-survival","source_endpoint_id":"lobby","target_endpoint_id":"survival","source_channel":"chat","target_channel":"chat","rule_group_id":"shared","enabled":true}'
Invoke-RestMethod http://127.0.0.1:8080/v1/routing-bindings -Method Post -Headers $headers -Body $binding
```

将两个签发响应的 `token` 分别安全放入 `$env:EASYBOTCE_ENDPOINT_TOKEN`，在两个终端启动模拟器：

```powershell
$env:EASYBOTCE_ENDPOINT_TOKEN = '<lobby-issued-token>'
go run ./cmd/mc-simulator -endpoint lobby -publish 'hello from lobby' -reconnect=false
```

```powershell
$env:EASYBOTCE_ENDPOINT_TOKEN = '<survival-issued-token>'
go run ./cmd/mc-simulator -endpoint survival -reconnect=false
```

接收端应看到 `message.deliver`，发送端应看到 `message.accepted`。`queued` 只表示 Hub 将消息加入目标会话内存队列；不保证客户端收到、展示或执行。模拟器为诊断工具，会把消息内容打印到 stdout；Hub 默认不记录消息正文。

> 禁止把接收的同步消息再次作为新消息发布，否则双向路由会产生回环。实际 MC 插件应识别 Hub 注入的聊天并避免回发；更复杂的跨服拓扑还需定义来源标记。

## MC 模拟器

```powershell
go run ./cmd/mc-simulator -endpoint <endpoint-id>
go run ./cmd/mc-simulator -endpoint <endpoint-id> -publish 'hello' -channel chat -sender player-1 -sender-name Steve
```

令牌只从 `EASYBOTCE_ENDPOINT_TOKEN` 读取，不接受 URL query、命令行参数或 URL 内凭据。`-once` 仅验证握手，不能与 `-publish` 同时使用。模拟器不会在重连后自动重放结果未知的发布请求，因为 Hub 去重缓存仅在单次会话中有效。

## 配置参考

| YAML 字段 | 环境变量 | 默认值 |
| --- | --- | --- |
| `listen_addr` | `EASYBOTCE_LISTEN_ADDR` | `127.0.0.1:8080` |
| `admin_token` | `EASYBOTCE_ADMIN_TOKEN` | 必填 |
| `log_level` | `EASYBOTCE_LOG_LEVEL` | `info` |
| `shutdown_timeout` | `EASYBOTCE_SHUTDOWN_TIMEOUT` | `10s` |
| `database_driver` | `EASYBOTCE_DATABASE_DRIVER` | `sqlite` |
| `database_dsn` | `EASYBOTCE_DATABASE_DSN` | SQLite 为 `easybotce.db`；其他驱动必填 |

YAML 必须是 UTF-8、单文档顶层映射，字段和值均严格校验；未知字段、重复字段、非字符串值和 YAML 锚点不接受。管理员令牌需要 32–256 个可见 ASCII 字符。

## Docker 部署

仓库提供 Dockerfile 与 Compose 配置，默认使用持久化 SQLite volume，并只绑定宿主机 `127.0.0.1:8080`。本地启动：

```powershell
$bytes = New-Object byte[] 32
$rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
$rng.GetBytes($bytes)
$rng.Dispose()
$env:EASYBOTCE_ADMIN_TOKEN = [Convert]::ToBase64String($bytes)
docker compose -f compose.yaml up --build -d
Invoke-RestMethod http://127.0.0.1:8080/readyz
docker compose -f compose.yaml logs -f hub
```

停止服务可运行 `docker compose -f compose.yaml down`；默认保留数据库 volume。不要添加 `-v`，除非确实要删除持久化数据。Compose 中的 SQLite 文件位于容器 `/data/easybotce.db`。镜像以非 root 用户运行；镜像中的 `/data` 目录归容器用户所有，Compose 命名卷首次挂载时会沿用该权限。若改用宿主机 bind mount，请先确保宿主机目录允许容器 UID `65532` 写入。该配置没有 TLS 终止，生产部署应让可信反向代理处理 HTTPS/WSS 并限制 Hub 端口访问。

当前环境未安装 Docker，因此尚未本地构建或运行该镜像；CI 已配置镜像构建 job，但远程运行结果仍需以 GitHub Actions 为准。
## 测试、格式检查和构建

```powershell
$env:CGO_ENABLED = '0'
go test -mod=readonly -count=1 ./...
go vet -mod=readonly ./...
gofmt -l cmd internal api
go build -mod=readonly -trimpath ./cmd/...
```

MySQL/PostgreSQL 集成测试要求 Docker：

```powershell
go test -mod=readonly -tags integration -count=1 -timeout 10m ./internal/store
```

`sqlc.yaml` 为三个 SQL 方言生成查询代码：

```powershell
go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0 generate
```

CI 配置在 Linux/Windows 执行格式、vet、单测及无 CGO 构建，并在 Linux 执行 race；数据库 CI job 使用容器测试 MySQL/PostgreSQL。配置工作流并不等于已经在远程 CI 成功运行。

## 常见问题

| 现象 | 检查方式 |
| --- | --- |
| `/healthz` 正常但 `/readyz` 失败 | 检查 DSN、数据库可达性和 SQLite 文件权限。 |
| 管理 API 返回 401 | 使用单个 `Authorization: Bearer …` 管理令牌，不要使用 query token。 |
| Gateway 返回 401/403 | 使用该端点签发的专属令牌，检查端点启用状态及凭据是否撤销。 |
| 有 accepted 但目标没有显示消息 | `queued` 不是远端执行确认；检查目标会话、能力和客户端队列处理。 |
| 消息重复出现 | 检查 MC 端是否把 Hub 注入的消息重新发布。 |
| MySQL/PostgreSQL 集成测试无法启动 | 确认 Docker daemon 可访问后重试。 |
| OneBot11、命令、账号绑定或 AI API 不存在 | 这些仍属 v1 未完成功能；查看[实现状态](implementation-status.md)。 |



