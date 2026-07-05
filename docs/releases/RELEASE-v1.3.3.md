# Archived Release Notes: AI LocalBase v1.3.3

> 历史版本归档，不代表当前项目版本。当前版本以 Git tag 与 GitHub Release 为准。

v1.3.3 聚焦认证完善、设置页与知识库工作台体验优化，并补齐开源自部署场景下的安全默认值。

## 主要变化

- 认证体系改为 root 单用户、服务端 session 和独立 API Key 模型。
- Web session 使用 HttpOnly Cookie，避免前端持久化 session token。
- 支持首次启动自动创建 root 用户，也支持首次初始化向导。
- Settings 页面重构为更清晰的双栏设置面板，并完善账号、安全、Token、密码交互。
- Chat 顶部交互简化，整体样式与新设置面板统一。
- 知识库 UI 重构为工作台布局，优化左侧导航、文档列表、检索调试、索引健康、评估历史和质量趋势。
- 修复质量趋势在窄区域下挤压导致文字竖排的问题。
- 引入更统一的设计代币、按钮、表单、Tab、空状态、骨架屏和 Toast 样式。

## 部署与认证

- 本地开发默认关闭认证：`ENABLE_AUTH=false`。
- 服务器部署建议启用认证：`ENABLE_AUTH=true`。
- 推荐生产环境设置 `AUTH_PASSWORD`，首次启动自动创建 root 用户。
- 如果不设置 `AUTH_PASSWORD`，首次访问 Web 页面会进入初始化向导。
- 暴露到公网时建议设置 `AUTH_SETUP_TOKEN`，避免首次初始化窗口被他人抢占。
- 反向代理 HTTPS 时需要转发 `X-Forwarded-Proto: https`，后端会自动为 Cookie 设置 `Secure`。
- 当前版本对基于 session 的写请求增加了 CSRF Token 校验；MCP 与 OpenAI-compatible API 的 Bearer/API Key 路径不受影响。
- 未配置 `AUTH_SETUP_TOKEN` 时，首次初始化默认仅允许本机回环地址完成。
- MCP 危险工具确认统一收口到 `confirmNonce`，旧 `X-MCP-Confirm` / `confirm_token` 已停用。

## 验证

- 前端类型检查通过。
- 前端测试通过。
- 前端生产构建通过。
- 后端 `go test ./...` 通过。
