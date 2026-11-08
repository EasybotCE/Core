# 贡献说明

先阅读[实现状态](docs/implementation-status.md)和[开发指南](docs/development.md)。目前按 M0 → M1 逐步推进，不引入 Web UI 或 MC 插件实现。

## 提交检查

1. Go 代码经过 `gofmt`，执行 `go test ./...` 和 `go vet ./...`。
2. 行为变更同时更新测试、OpenAPI / Schema 与相关文档，不发布返回假成功的占位接口。
3. 不提交令牌、聊天内容、玩家隐私、数据库文件或本地配置。
4. 新依赖需要说明用途、许可证和无 CGO 构建影响。

## DCO

贡献采用 [Developer Certificate of Origin 1.1](https://developercertificate.org/)，不签 CLA。提交时使用真实且有权作出声明的身份签署：

```sh
git commit -s
```

提交信息包含 `Signed-off-by: Your Name <you@example.com>`。签署表示你满足 DCO 对贡献来源和提交权利的声明，不代表转让著作权。

核心代码采用 AGPL-3.0-only；`api/protocol` 中的 Schema 与示例采用 MIT。维护者应在正式分发前补齐官方许可证全文归档；参见 [LICENSE](LICENSE)。
