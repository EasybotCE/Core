# EasybotCE 文档

EasybotCE 经原作者授权独立发展，由社区维护。平台以**服务器实例为主体、功能配置为操作**，组合多个 Minecraft 服务器、聊天机器人和独立插件端点。Web UI 暂缓，先提供完整的管理 API。

当前仓库已有可运行的 Hub、数据库存储、CE WebSocket 端点会话、路由规则与模拟器；这些只是 v1 基础，不代表八类业务功能已完成。请以[实现状态](implementation-status.md)区分已实现能力与目标范围。

## 从任务开始

| 目标 | 文档 |
| --- | --- |
| 启动、配置数据库、测试或构建 | [开发与运行指南](development.md) |
| 确认当前能力与未完成项 | [实现状态](implementation-status.md) |
| 理解端点、路由绑定与功能配置 | [架构概念](architecture.md) |
| 查阅共识和决策背景 | [决议记录](decisions.md) |
| 确认完整 v1 的交付范围 | [v1 范围](v1-scope.md) |
| 对接 MC 插件/MOD | [端点协议](protocol.md) |
| 将 OneBot 11 BOT 接入 Hub | [OneBot 11 反向 WebSocket 对接](onebot11.md) |

## 仓库契约

- [OpenAPI 3.1](../api/openapi.json) 描述当前实现的管理 API 与 WS 路由。
- [端点 JSON Schema](../api/protocol/endpoint.schema.json) 与[消息样例](../api/protocol/examples/)描述 CE 端点协议。
- [贡献说明](../CONTRIBUTING.md)记录检查命令、DCO 与许可边界。

参考上游产品功能：<https://docs.inectar.cn/docs/easybot/intro>。该链接用于功能对照，不表示 CE 与原版插件或线协议兼容。MC 插件/MOD 由另一团队开发；SDK 在独立仓库维护。
