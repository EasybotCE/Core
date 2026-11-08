# 账号绑定、玩家状态与占位符 API

本文描述 Hub v1 当前提供的工作流；服务器插件/MOD 由集成方实现，并使用绑定到端点的 Bearer credential。所有路径必须经 HTTPS/WSS 暴露，token 只放 `Authorization: Bearer`，不得放 URL。

## 能力配置

- MC server credential：`accounts.challenge` 创建游戏内验证码；`players.state` 发布状态并解析占位符。
- Chat bot credential：`accounts.bind` 兑换验证码。机器人必须先验证平台事件中的真实用户 ID，再把该 ID 作为 `platform_user_id` 提交。

能力与 endpoint 类型均由 Hub 强制检查。禁用 endpoint 或撤销 credential 后调用立即失效。

## 绑定流程

1. 玩家在游戏中触发绑定命令。插件调用 `POST /plugin/v1/accounts/challenge`，JSON：`{"player_id":"<稳定的MC UUID>"}`。
2. Hub 返回 `code`（16 位十六进制、有效期 5 分钟）和 `expires_at`。插件私聊展示该码；每个服务器+玩家只保留最新一个 challenge。数据库只存码的 SHA-256 摘要。
3. 用户在聊天平台向 BOT 提交兑换命令。BOT 必须在平台侧获取可信的发言用户 ID，并调用 `POST /plugin/v1/accounts/claim`：`{"code":"...","platform_user_id":"..."}`。
4. 成功返回 201 绑定记录。challenge 一次性消费；过期/已消费码返回 404。同一个 MC endpoint/player 及同一个 BOT endpoint/platform user 各自最多一条绑定；冲突返回 409。不同 BOT endpoint 的用户命名空间互相独立。
5. 管理员可通过 `GET /v1/account-bindings?endpoint_id=...&player_id=...` 查询，并通过 `DELETE /v1/account-bindings/{binding_id}` 解绑。解绑写入审计事件。

绑定唯一性目前限定到单个服务器实例；同一 MC 玩家可在不同服务器分别绑定，不同聊天平台 BOT endpoint 也属于不同账号命名空间。解绑/换绑需要管理员执行，避免第三方 BOT 事件伪造解绑请求。

## 玩家状态和占位符

插件在上线、状态变化、下线时调用 `POST /plugin/v1/players/state`：

```json
{"player_id":"uuid-1","player_name":"Alex","online":true,"world":"world","x":1,"y":64,"z":-2}
```

同一服务器+玩家状态为 upsert。下线事件仍需提交最后的名称/world/坐标，同时 `online:false`。状态表保存最后一次观测，不自动过期清理。

插件可调用 `POST /plugin/v1/players/resolve`：`{"player_id":"uuid-1"}`，获得 `state` 与 `placeholders`。占位符返回值包含：

- `player.id`、`player.name`、`player.online`、`player.world`、`player.x`、`player.y`、`player.z`
- `server.id`、`server.name`
- 已绑定时的 `account.platform_user_id`、`account.chat_endpoint_id`

这是结构化解析结果，插件或命令/模板系统负责替换文本。不同服务器实例的状态彼此隔离。请不要将未净化的玩家名称/坐标直接拼入控制台命令；插件应使用安全的命令 API/参数化执行。

## 限制与安全

请求体统一严格 JSON 校验，未知/重复/null 字段拒绝，最大 16 KiB。平台用户 ID 必须由 BOT 从已验证的原生事件中取得；Hub 无法独立验证平台侧身份。challenge 为短随机码，因此应在私聊展示并限制兑换命令频率；当前没有平台消息发送、群退自动解绑、强制进服检查、昵称同步或绑定事件推送。这些行为由后续功能补齐，不能由本 API 推断已实现。

