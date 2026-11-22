# EasyBotCE Minecraft Bridge（Core 服务端）

本仓库实现的是 Go Bridge 服务端，不实现、不打包 Minecraft 插件本体。Minecraft 插件由使用方从已授权的 EasyBot 插件项目单独构建和部署；Core 仓库不复制插件源码、反编译源码或二进制。

## Core 端配置

1. 在 EasyBotCE 管理台创建并启用 Minecraft 服务器记录，妥善保管记录详情中的 Bridge token。
2. Bridge 默认监听 `0.0.0.0:26991/bridge`。只允许受信网络访问；公网使用需配置 `bridge.tls.certificate` 和 `bridge.tls.privateKey`，并让插件通过 `wss://` 连接。TLS 证书与管理台 HTTPS 配置相互独立。
3. 已有 EasyBot 插件使用旧版 `op=0/1/3/4/5` WebSocket 协议及长期 token。该协议没有挑战认证，已彻底移除支持；所有客户端必须使用带 HMAC 挑战认证的 v2 协议。
4. Bridge v2 客户端使用每连接 nonce 和 HMAC-SHA256 挑战认证。认证帧绑定协议版本、连接 ID、服务器 ID 和时间戳；后续帧校验身份、请求 ID、时间戳和递增序号。TLS 仍是跨不可信网络的必要条件，因为 HMAC 不加密传输内容。
5. 管理台 Bridge 状态来自实际连接，不从服务器记录的 `enabled` 字段推断在线。可使用 `GET /api/v1/bridge/status` 查看连接状态；认证后的 `POST /api/v1/bridge/{id}/send` 可发送测试消息。

## 已实现与边界

Core 接收聊天、进退服、死亡和玩家状态事件；支持 `SEND_TO_CHAT` 广播及 `RUN_COMMAND` 请求/回执；支持心跳、身份认证、连接替换、离线状态和在线人数。消息规则由管理台的 sync-rules 记录控制。`RUN_COMMAND` 是高权限能力，无命令白名单或独立沙箱；插件返回的回执只表示已接收/提交，不代表游戏端命令效果已验证。

本地 Go 测试覆盖挑战认证、协议帧重放拒绝、错误令牌拒绝、状态更新、命令超时及连接生命周期。模拟测试不等于外部插件真实互通。当前没有可用真实 Minecraft 测试服/公网证书，因此插件实际加载、游戏内双向收发、公网 TLS 和命令安全验收仍待人工完成。


## 绑定 Core API（部分实现）

管理员登录后可创建一个限时绑定挑战：

```http
POST /api/v1/bindings/challenges
Content-Type: application/json

{"playerUuid":"<Minecraft UUID>","serverId":"<server record id>","groupId":"<allowed group id>","lifetimeSeconds":300,"maxAttempts":5}
```

响应中的验证码只能成功消费一次。玩家在指定 OneBot 群中发送 `绑定 <验证码>`，Core 将群成员身份与挑战中的玩家 UUID 建立关联。`groupId` 可选；若设置，其他群中的相同验证码将无法使用。限制服务器加入时，可在服务器记录详情 JSON 中设置 `requireBinding: true`，并可通过 `kickMessage` 设置拒绝提示。管理员可用 `GET /api/v1/bindings`、`DELETE /api/v1/bindings/{id}` 和 `GET /api/v1/bindings/audit` 管理与审计。

现阶段挑战由管理员创建，没有插件侧玩家自助命令；退群解绑及真实插件服验证未完成。上述能力需在管理会话认证后调用，真实平台账号/游戏服务器尚未验收。
