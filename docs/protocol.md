# MC 与插件端点协议

**状态：开发中的 `0.1.0` 协议。** 本仓库实现 CE WebSocket Hub 和诊断模拟器；MC 插件/MOD 由另一团队实现。本文描述当前可运行消息链路及其边界，不表示协议已稳定或达到 v1。原生 OneBot11 接入使用 OneBot 原生协议，尚未在 Hub 实现。

## 契约入口

- [JSON Schema](../api/protocol/endpoint.schema.json)：信封和消息模型。
- [消息样例](../api/protocol/examples/)：hello、welcome、error、publish、deliver、accepted。
- [Go 编解码器](../internal/protocol)：执行输入长度、UTF-8、重复键、精确字段及数值字面量验证。
- [OpenAPI](../api/openapi.json)：Gateway URL 和当前 HTTP 管理 API。

WS URL 为 `GET /v1/gateway`。客户端必须通过唯一 `Authorization: Bearer <endpoint-token>` 请求头鉴权；不接受 URL query token。管理员令牌不能连接 gateway。Hub 根据令牌确定端点身份，hello 中的自报身份必须与之匹配。每个启用端点最多一个活动会话。

## 信封与限制

文本帧信封包含 `v`、`id`、`type`、`payload`；`reply_to` 仅用于响应关联。当前版本固定为 `0.1.0`，帧最大 64 KiB。字段大小写敏感，重复/未知字段、错误类型、无效 UTF-8、未知消息类型和额外字段都会被拒绝。原始字节解析比 JSON Schema 更严格；Schema 不检查重复对象成员和 JSON 数字字面量形式。

连接限制包括：hello 最长等待 5 秒、最多 128 个未完成握手、最多 1024 个活动会话。会话每秒最多 20 条 publish，突发上限 40；超过限制的连接被关闭。服务端每 20 秒发起 WebSocket Ping，客户端需要通过正常 WS 读取循环回应控制帧。协议错误不应包含令牌或敏感异常。

## 建立会话

客户端发送 `session.hello`，声明端点 ID、类型、支持版本和能力。Hub 验证已认证端点身份、端点启用状态及协议版本，然后返回 `session.welcome`。有效能力是客户端请求、凭据授权和 Hub 实现能力三者的交集。目前 Hub 实现的能力仅为：

- `message.publish`：客户端向 Hub 发布消息。
- `message.receive`：客户端接收 Hub 路由消息。

没有协商到相应能力的操作不得发送或投递。管理员可以通过凭据轮换调整端点授权能力。

## 发布与投递

`message.publish` 的 payload 为：

```json
{"channel_id":"chat","sender_id":"player-1","sender_name":"Steve","text":"hello"}
```

`channel_id`、`sender_id`、`sender_name` 必须非空且受长度限制；text 为 1–2048 个 Unicode 码点。来源端点身份由认证会话确定，不信任客户端为来源指定其他 endpoint ID。

Hub 查找已启用且 source endpoint/channel 匹配的 routing bindings，按绑定 ID 顺序处理最多 200 条路由。`source_channel` 可精确匹配或设为 `*`。每条路由引用一个共享 rule group，依次执行 include/exclude、正则替换和模板渲染。模板支持 `{text}`、`{sender_id}`、`{sender_name}`、`{source_endpoint_id}`、`{target_endpoint_id}`；模板替换是单次、非递归展开。渲染结果最多 2048 个码点。

规则通过后，Hub 将 `message.deliver` 放入目标端点的有界内存队列。目标未连接、无接收能力、规则错误、队列已满或输出超长会计为 dropped；include/exclude 未匹配则计为 filtered。Hub 重启会丢失队列内容，且不提供持久化投递。

## 接收确认语义和去重

发布端收到 `message.accepted`，其中 `queued`、`filtered`、`dropped` 表示本次发布的路由结果；`duplicate` 表示命中当前会话去重缓存。计数合计最多 200。**queued 只表示放入 Hub 目标会话的内存队列，不代表远端已收到、展示或执行。** 当前没有远端执行 ACK，也没有 exactly-once 保证。

Hub 在每个会话内缓存最近 1024 个发布消息 ID 及 payload 摘要。同 ID、同内容返回缓存结果且不重复路由；同 ID、不同内容会关闭连接。重连后缓存清空，因此客户端不得将结果未知的 publish 自动重放。要由 MC 集成自行管理重试和幂等，除非未来协议版本另行增强。

## 双向同步与回环

多个 endpoint 可通过独立的方向 routing binding 组合。需要双向同步时分别创建 A→B 和 B→A 绑定。客户端必须识别 Hub 注入的同步消息，不得将其作为新源消息再次发布，否则会产生回环和重复扩散。当前协议不携带 Hub 来源签名或端到端 trace ID；实现方需基于平台事件区分来源，并在拓扑中谨慎配置规则。

## 连接与安全注意事项

- 客户端应在 `welcome` 前完成协议协商，之后只发送允许的消息类型。
- 控制帧由 WebSocket 层处理；客户端必须保持读取以响应 Ping。应用消息不能代替 WS Pong。
- 发放、轮换和撤销 endpoint token 时，Hub 不存明文凭据；token 仅在创建响应中展示一次。撤销/禁用端点后活动会话会关闭。
- Hub 日志默认不记录聊天正文。诊断模拟器会将消息打印到 stdout，使用时注意终端日志保留和访问权限。
- 建议通过 TLS (`wss`) 或受控私网连接生产客户端；不要将端点凭据放进 URL、配置仓库或公开日志。

完整字段范围以 Schema 与 Go 校验器为准。任何无法被当前 Go 解码器和 Schema 测试同时覆盖的新消息变更，都必须配套更新样例、测试、OpenAPI/文档，并评估兼容性。
