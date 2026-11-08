# OneBot 11 反向 WebSocket 对接

本文面向 OneBot 11 BOT 端维护者，说明如何把 BOT 连接到 EasybotCE Hub。当前实现由 BOT 主动连接 Hub（反向 WebSocket）；Hub 不会主动连接 BOT，也没有独立 BOT 配置/重连管理 API。

## 前置条件

- Hub 已运行，且管理 API 可用。
- 已创建 `chat-bot` 类型的启用端点，并为其签发同时包含 `message.publish`、`message.receive` 的凭据。
- Hub 与 BOT 端之间使用可信 TLS。生产部署应通过 `wss://`；不要将凭据放在 URL/query、日志或客户端源码中。

凭据通过管理 API 的凭据签发接口创建。签发响应中的明文 token 只展示一次；丢失后应撤销并重新签发。

## 连接

连接路径为：

```text
GET /onebot/v11/ws
Authorization: Bearer <endpoint-credential>
```

例如：

```text
wss://hub.example.net/onebot/v11/ws
```

连接要求：

- 仅接受 `GET` WebSocket Upgrade。
- 仅接受 `chat-bot` 端点凭据。
- 禁止 query string（包括 query token）。
- 一个 BOT 端点同时只允许一个活动连接；已连接时 Hub 返回 `409 endpoint_already_connected`。
- 凭据撤销、端点禁用/删除或 Hub 关闭时，活动连接会被断开。
- 最大 WebSocket event frame 为 64 KiB；队列容量有限，拥塞时消息可能被丢弃。

拒绝连接时 Hub 返回 HTTP 错误及 JSON `error.code`。常见状态包括 `400`（query 不允许）、`401`（凭据无效/已撤销）、`403`（端点类型或能力不符）、`409`（端点已有活动连接）和 `503`（容量或存储不可用）。

## 消息映射

### 入站：OneBot 到 Hub

目前只把 `post_type=message` 的 `group`、`private` 事件映射成文本消息。支持 `message` 为字符串或 segment 数组；数组只拼接 `type=text` 的段，CQ 码和非文本段不会被执行/转发。空文本、自发消息、无效 ID、超长文本（超过 2048 字符）和不支持的事件不会进入路由。

| OneBot 字段 | EasybotCE 字段 |
| --- | --- |
| `message_id` | 来源消息 ID |
| `user_id` | `sender_id` |
| `sender.card`，否则 `sender.nickname` | `sender_name` |
| 群聊 `group_id` | `channel_id = group-<十进制群号>` |
| 私聊 `user_id` | `channel_id = private-<十进制用户号>` |
| 文本段 | `text` |

示例事件：

```json
{"time":1730000000,"self_id":10001,"post_type":"message","message_type":"group","message_id":123,"user_id":456,"group_id":789,"message":[{"type":"text","data":{"text":"hello"}}],"sender":{"nickname":"Alex"}}
```

普通 OneBot 非消息事件当前被忽略。无法解析的 `post_type=message` 会关闭连接，以避免把坏数据静默当作正常消息。

### 出站：Hub 到 OneBot

规则绑定路由到 BOT 端点时，目标 `target_channel` 必须使用 `group-<十进制群号>` 或 `private-<十进制用户号>`。Hub 会分别发送 OneBot `send_group_msg` 或 `send_private_msg` action，消息正文编码为 text segment。

BOT 必须按 OneBot 11 规范返回 action response，且 echo 与请求一致。Hub 根据 echo 跟踪并持久化 action 状态；同步 `ok` 且 `retcode=0` 视为成功，错误响应为失败。`async` 只表示已受理，不改变终态；若 BOT 不再回传终态会在 30 秒后（首次查询时）超时。Hub 重启时未终态 action 标为 `timeout`，结果未知且不自动重试。

## 路由配置

BOT 与 Minecraft/其他 CE 端点之间的消息路由仍通过规则组和 routing binding 管理 API 配置。为每个方向分别创建 binding：

- BOT -> 服务器：`source_endpoint_id` 为 BOT，`source_channel` 如 `group-789`，目标 channel 使用服务器插件约定值。
- 服务器 -> BOT：`target_endpoint_id` 为 BOT，`target_channel` 如 `group-789` 或 `private-456`。

使用 `source_channel: "*"` 可匹配该来源端点所有 channel。具体管理 API schema 以 `api/openapi.yaml` 为准。

## 当前限制

- 仅支持 BOT 主动连接的 OneBot 11 反向 WebSocket；正向 WebSocket 尚未实现。
- 没有 Hub 主动连接、自动重连、BOT 池管理或 BOT 状态管理 API；断线重连由 BOT 客户端负责。
- 只路由文本，不处理图片、语音、文件、at 等 segment，也不渲染图片模板。
- 去重仅限单次连接的内存窗口，重启/重连后不保证去重。
- 发送队列仅表示 Hub 本地接收/排队；不表示 BOT 或聊天平台最终送达。
- 生产环境必须由 TLS 保护连接；反向代理需正确支持 WebSocket Upgrade，并限制访问日志中敏感头部。

## Action 回执与最终状态

路由到 OneBot BOT 的发送请求会生成 Hub action ID，并将其作为 OneBot `echo`。`message.accepted`/入队计数只代表 Hub 接受排队，不代表平台执行成功。BOT 回传 OneBot action response 后，可通过管理员 API `GET /v1/onebot/actions/{action_id}`。列表响应在满页时提供 `next_before` 和 `next_before_id`；翻页时两者一起传回，确保相同秒级创建时间的 action 不会被跳过。 查询状态：`queued`、`sent`、`succeeded`、`failed` 或 `timeout`。平台返回 `status=ok` 且 `retcode=0` 才记为 `succeeded`。状态持久化于 Hub 数据库。也可调用管理员 API `GET /v1/onebot/actions?endpoint_id=<id>&limit=20&before=<unix>&before_id=<id>` 按端点分页查询，返回按创建时间倒序的 `items`；`before` 与 `before_id` 共同组成排他游标；只传 `before` 时按秒级时间排除该秒全部记录（兼容简化查询）。`async` 是非终态，不会被误判失败；若 30 秒内未获得终态回执，首次查询时标为 `timeout`。Hub 重启时遗留的 `queued`/`sent` 自动转为 `timeout`；这只表示结果未知，不应推断为平台一定未执行，也不会自动重试。

