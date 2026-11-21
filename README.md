# EasybotCE Core

EasybotCE 的主程序，使用 Go 提供管理 API、Vue 3 提供网页管理台，默认使用 SQLite 保存数据。便携包包含程序和页面资源，解压后即可运行。

## 文档导航

- 开发：[开发说明](docs/开发说明.md) · [网页管理台说明](web/README.md) · [项目结构检查](docs/项目结构检查.md)
- 规划：[原始重建规划](docs/PLAN.md) · [后续开发计划](docs/REMAINING_PLAN.md)
- 协作：[贡献规范](CONTRIBUTING.md) · [署名说明](AUTHORS.md) · [许可证](LICENSE)

## 当前进度

| 功能 | 状态 |
| --- | --- |
| 管理台、配置管理、登录认证、持久化运行日志 | 已实现 |
| OneBot 11 正向 WebSocket 适配器 | 已实现连接、消息收发接口、状态管理与断线重连；已通过协议模拟器验证，真实账号仍待验收 |
| QQ 官方 Bot 适配器 | 已实现官方 Gateway 接收、QQBot token、群/C2C 消息收发和频道列表；真实账号仍待验收 |
| MySQL、PostgreSQL 存储 | 已提供实现和集成测试；具体版本以对应 CI 验证结果为准 |
| Minecraft 桥接、基础消息转发 | 已实现；账号绑定尚未实现 |
| JavaScript 插件执行、AI 对话、图片模板渲染 | 尚未实现；已有配置入口不代表运行能力可用 |

后续范围和验收标准见 [开发计划](docs/REMAINING_PLAN.md)。

## 快速开始

1. 从 [发布版本](https://github.com/EasybotCE/Core/releases) 下载适合当前系统的便携压缩包。
2. 解压到可写目录，保留包内的 `web` 目录。
3. Windows 双击 `easybotce.exe`；Linux 在解压目录运行 `./easybotce`，缺少执行权限时先执行 `chmod +x easybotce`。
4. 浏览器打开 `http://127.0.0.1:5001`，运行期间保持程序窗口或终端打开。

默认仅监听本机回环地址，无需注册或登录。普通用户不需要安装 Go、Node.js、数据库服务器，也不需要额外启动前端服务。

Windows 双击启动失败时会显示错误原因，按回车后关闭窗口；从已有终端或自动化任务启动时，程序直接返回错误码。

## 配置与登录

程序在首次启动时于程序目录创建 `config.json`，并生成 12 位数字管理密码写入配置；该密码仅在首次启动控制台显示一次。修改配置后需重启。配置结构为 `server`、`bridge`、`database`、`auth` 和 `logging`，完整示例见 [config.example.json](internal/config/config.example.json)。`EASYBOT_CONFIG` 可指定配置文件，环境变量可覆盖对应设置。

默认仅监听本机，无需登录。改为非本机监听后使用配置中的 `auth.password` 登录；密码以明文保存在配置文件中，请限制文件访问权限。通过公网传输凭据时建议配置 HTTPS。旧版 .NET 格式配置不会被静默覆盖，需迁移到新结构。

## 升级与原版迁移

升级前停止程序，备份并保留 `config.json` 和 `appdata`，再替换程序与 `web` 页面资源。已有 CE 数据库也可通过 `EASYBOT_SQLITE_PATH` 指定路径继续使用。

原版 `.NET appsettings.json` 与 `token.txt` 不再作为 CE 启动配置；请按示例迁移监听地址、数据库、日志和 Bridge 设置。复制启动配置不会导入原版数据库、适配器或插件数据，原版数据库需要单独迁移。

## 源码开发

开发环境要求：Go 1.24+、Node.js 20.19+。在仓库根目录执行：

```powershell
node build.mjs
```

脚本自动安装前端依赖、构建页面、编译程序并生成便携包：Windows 输出 `release/easybotce-windows-amd64.zip`，Linux 输出对应平台的 `.tar.gz`；本地编译程序同时保存在 `bin`。

手动运行、热更新和测试命令见 [开发说明](docs/开发说明.md)，前端开发见 [管理台说明](web/README.md)。CI 包含跨平台测试、静态检查、便携包构建和数据库集成测试。

贡献者和开发代理须遵守 [贡献规范](CONTRIBUTING.md)。建议使用中文说明 PR 和提交；中文 CI 仅提醒，不阻断合并。使用 AI 辅助贡献时须披露工具及用途，详细要求见贡献规范。

## 项目结构

| 路径 | 内容 |
| --- | --- |
| `cmd/easybot/` | 守护进程及管理 Web 服务入口 |
| `internal/` | 配置、存储、适配器、运行日志和版本化管理 API |
| `web/` | Vue 3、TypeScript、Tailwind CSS v4 管理台 |
| `.github/workflows/` | CI 工作流 |
| `docs/PLAN.md` | 原始重建规划 |
| `docs/REMAINING_PLAN.md` | 后续功能范围与验收标准 |

## 许可证与署名

本仓库使用 [CC BY-NC 4.0（`CC-BY-NC-4.0`）](https://creativecommons.org/licenses/by-nc/4.0/) 许可证，该许可证不允许商业使用。分享或改编时，请注明 EasybotCE 及本仓库来源，附上许可证链接、标明改动，并保留已有作者、版权和许可声明。详见 [署名说明](AUTHORS.md)。
