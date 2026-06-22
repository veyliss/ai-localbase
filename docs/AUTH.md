# 认证系统使用文档

## 设计目标

当前认证体系面向开源自部署场景，默认采用 **root 单用户 + 服务端会话 + 独立 API Key**。

- Web 登录使用 root 账户和服务端 session，可退出当前设备或退出全部设备。
- Web session 通过 **HttpOnly + SameSite=Lax Cookie** 下发，前端不再把 session token 存在 `localStorage`。
- root 密码只保存 bcrypt 哈希，不再把明文环境变量作为运行态密码。
- OpenAI-compatible `/v1` 接口支持独立 API Key，不需要复用网页登录 token。
- MCP 默认关闭；服务器启用 MCP 时必须同时开启认证。旧 MCP Token 仅作为兼容凭证保留，新接入优先使用 API Key Scope。
- 暂不包含多用户、OIDC 和复杂 RBAC。

---

## 环境变量配置

```bash
# 启用认证（默认 false）
ENABLE_AUTH=true

# root 用户名，默认 root
AUTH_USERNAME=root

# 可选。设置后首次启动会自动创建 root 用户并保存 bcrypt 哈希。
# 留空时，后端会启动并进入首次初始化向导。
AUTH_PASSWORD=your-secure-password

# 可选。只在首次初始化向导中使用。
# 如果设置，初始化 root 密码时必须输入该 Token。
AUTH_SETUP_TOKEN=your-random-setup-token

# 可选。已有 root 用户时的一次性密码重置。
# 必须两个变量同时设置；同一个 AUTH_RESET_TOKEN 只执行一次。
AUTH_RESET_TOKEN=your-random-reset-token
AUTH_RESET_PASSWORD=your-new-secure-password

# 旧版 JWT 配置兼容项。当前服务端 session 不再要求设置。
JWT_SECRET=
```

---

## 初始化流程

1. 推荐服务器部署时设置 `ENABLE_AUTH=true`。
2. 如果设置了 `AUTH_PASSWORD`，首次启动会自动创建 root 用户。
3. 如果没有设置 `AUTH_PASSWORD`，访问 Web 页面会进入初始化向导。
4. 如果设置了 `AUTH_SETUP_TOKEN`，初始化向导会要求输入该 Token。
5. 初始化完成后，后续登录使用 root 用户名和密码。

---

## root 密码重置

当忘记 root 密码或需要服务器侧强制轮换时，可以使用环境变量执行一次性重置：

1. 生成新的重置 Token：

```bash
openssl rand -base64 32
```

2. 设置环境变量并重启后端：

```bash
ENABLE_AUTH=true
AUTH_RESET_TOKEN=your-random-reset-token
AUTH_RESET_PASSWORD=your-new-secure-password
```

3. 后端启动时会更新 root 密码、撤销所有 Web session，并记录安全事件。
4. 确认新密码可登录后，删除 `AUTH_RESET_TOKEN` 和 `AUTH_RESET_PASSWORD` 并再次重启。

**注意：** `AUTH_RESET_TOKEN` 会以哈希形式记录在 `app-state.json`，同一个 Token 后续不会重复执行。

---

## API 端点

### 初始化状态

```bash
GET /api/auth/bootstrap
```

```json
{
  "auth_enabled": true,
  "setup_required": false,
  "setup_token_required": false,
  "username": "root"
}
```

### 首次初始化

```bash
POST /api/auth/setup
Content-Type: application/json

{
  "username": "root",
  "password": "your-secure-password",
  "setupToken": "optional-setup-token"
}
```

### 登录

```bash
POST /api/auth/login
Content-Type: application/json

{
  "username": "root",
  "password": "your-secure-password"
}
```

```json
{
  "expires_at": 1782450000,
  "username": "root"
}
```

登录成功后，后端会通过 `Set-Cookie` 写入 `ai_localbase_session`：

- `HttpOnly`：浏览器脚本不能读取 session token。
- `SameSite=Lax`：降低跨站请求携带 Cookie 的风险。
- HTTPS 请求或反向代理传入 `X-Forwarded-Proto: https` 时会自动设置 `Secure`。

### 会话与密码

```bash
GET  /api/auth/status
POST /api/auth/logout
POST /api/auth/logout-all
POST /api/auth/change-password
GET  /api/auth/sessions
```

### API Key

```bash
GET    /api/auth/api-keys
POST   /api/auth/api-keys
DELETE /api/auth/api-keys/:id
```

创建 API Key：

```bash
POST /api/auth/api-keys
Content-Type: application/json
Cookie: ai_localbase_session=<web-session-cookie>

{
  "name": "server-client",
  "scopes": ["openai:chat", "knowledge:read"]
}
```

当前允许的 API Key scope：

- `openai:chat`：允许调用 `/v1/chat/completions`。
- `knowledge:read`：预留给知识库读取 API。
- `knowledge:write`：预留给知识库变更 API。
- `config:read`：预留给配置读取 API。

MCP 相关 scope 会在 MCP 加固流程中扩展，并用于替代旧版独立 MCP Token。

响应中的 `token` **只显示一次**：

```json
{
  "item": {
    "id": "key_xxx",
    "name": "server-client",
    "prefix": "ailb_sk_xxxxxxxx",
    "scopes": ["openai:chat"],
    "createdAt": "2026-06-21T10:00:00Z"
  },
  "token": "ailb_sk_xxx"
}
```

---

## OpenAI-compatible 调用

`/v1/chat/completions` 支持两种认证方式：

- Web 页面：浏览器自动携带 `ai_localbase_session` Cookie。
- 外部客户端：使用 `Authorization: Bearer ailb_sk_xxx` API Key。

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer ailb_sk_xxx" \
  -H "Content-Type: application/json" \
  -d '{
    "messages": [
      { "role": "user", "content": "你好" }
    ]
  }'
```

---

## 安全建议

1. **生产环境必须启用 HTTPS**，避免 session/API Key 在传输中泄露。
2. **优先使用强密码**，建议 16+ 字符。
3. **推荐设置 `AUTH_PASSWORD` 自动初始化**，避免公网首次部署窗口被他人抢占。
4. 如果不用 `AUTH_PASSWORD`，建议设置 **`AUTH_SETUP_TOKEN`**。
5. API Key 只显示一次，创建后应立即复制保存。
6. API Key 泄露后应立即在设置页撤销。
7. 修改 root 密码会吊销所有已登录 Web 会话。
8. 反向代理部署 HTTPS 时，需要转发 `X-Forwarded-Proto: https`，这样 Cookie 会带上 `Secure`。
9. Web 管理接口依赖同源 Cookie；如果将前后端拆到不同域名，需要额外设计 CSRF Token 与明确的 CORS 白名单。

---

## 开发模式

```bash
# 关闭认证（本地开发）
ENABLE_AUTH=false
```
