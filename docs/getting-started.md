# 快速开始与使用指南

本文档补充 [`README.md`](README.md) 中未展开的运行说明，重点覆盖常用命令、环境变量、接口概览、检索流程与测试限制。

## 适合何时阅读

如果你已经看过 [`README.md`](../README.md)，并且需要以下更细节的信息，可以继续阅读本文：

- 常用开发命令
- 后端关键环境变量
- 对外接口概览
- 检索流程说明
- 测试方式与当前限制

部署方式、设置页面配置与 MCP 简介已放回 [`README.md`](../README.md)，避免入口信息分散。

---

## 常用命令

### 推荐：启动全部开发服务

```bash
cp .env.example .env
docker compose -f docker-compose.dev.yml up --build
```

默认开发地址：

- 前端：`http://localhost:4173`
- 后端：`http://localhost:8080`
- Qdrant：`http://localhost:6333`

`docker-compose.dev.yml` 会同时启动 Qdrant、后端和前端开发服务器，并挂载本地代码，适合日常开发和 UI 调试。

### 后端运行（可选）

只有在需要单独调试后端进程时使用：

```bash
cd backend
go run .
```

### 后端测试

```bash
cd backend
go test ./...
```

### 前端开发（可选）

只有在已经单独启动后端与 Qdrant，且需要直接运行 Vite 时使用：

```bash
cd frontend
npm install
npm run dev
```

单独运行 Vite 时默认监听 `http://localhost:3000`；项目推荐的 Docker 开发编排默认访问地址仍是 `http://localhost:4173`。

### 前端构建

```bash
cd frontend
npm run build
```

### 启动 Qdrant（可选）

```bash
docker compose -f docker-compose.qdrant.yml up -d
```

### 启动全部生产形态服务

```bash
docker compose up --build
```

如果开启认证，推荐在启动前复制 `.env.example` 为 `.env`，并设置 `ENABLE_AUTH=true` 与 `AUTH_PASSWORD`。如果不预置 `AUTH_PASSWORD`，至少应设置 `AUTH_SETUP_TOKEN`；否则首次初始化只允许本机回环地址完成。

---

## 关键环境变量

后端环境变量由 `backend/internal/config/config.go` 加载，常用项如下：

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PORT` | `8080` | 后端服务监听端口 |
| `UPLOAD_DIR` | `data/uploads` | 上传文件目录 |
| `MAX_UPLOAD_BYTES` | `26214400` | 单文件上传大小上限，默认 25 MiB |
| `STATE_FILE` | `data/app-state.json` | 应用状态文件 |
| `CHAT_HISTORY_FILE` | `data/chat-history.db` | 聊天记录 SQLite 文件 |
| `ENABLE_AUTH` | `false` | 是否启用 Web 登录和 API Key 鉴权 |
| `AUTH_USERNAME` | `root` | root 登录用户名 |
| `AUTH_PASSWORD` | 空 | 首次启动自动创建 root 用户的密码 |
| `AUTH_SETUP_TOKEN` | 空 | 首次初始化向导保护 Token |
| `AUTH_RESET_TOKEN` | 空 | root 密码一次性重置 Token |
| `AUTH_RESET_PASSWORD` | 空 | root 密码一次性重置密码 |
| `JWT_SECRET` | 空 | 旧版兼容项，当前 session 认证不再要求 |
| `QDRANT_URL` | `http://localhost:6333` | Qdrant 地址 |
| `QDRANT_API_KEY` | 空 | Qdrant API Key |
| `QDRANT_BIND_ADDRESS` | `127.0.0.1` | Docker 暴露 Qdrant 端口时绑定的宿主机地址 |
| `QDRANT_COLLECTION_PREFIX` | `kb_` | 知识库集合名前缀 |
| `QDRANT_VECTOR_SIZE` | `768` | 向量维度 |
| `QDRANT_DISTANCE` | `Cosine` | 距离算法 |
| `QDRANT_TIMEOUT_SECONDS` | `5` | Qdrant 超时秒数 |
| `ENABLE_HYBRID_SEARCH` | `false` | 启用 Hybrid Search |
| `ENABLE_SEMANTIC_RERANKER` | `false` | 语义重排启动默认值，可在高级检索中切换 |
| `ENABLE_QUERY_REWRITE` | `false` | Query Rewrite 启动默认值，可在高级检索中开关 |
| `ENABLE_SEMANTIC_CACHE` | `false` | 启用语义缓存 |
| `ENABLE_CONTEXT_COMPRESSION` | `false` | 启用上下文压缩 |
| `ENABLE_MCP` | `false` | 启用内置 MCP Server；服务器部署需同时开启认证 |
| `ENABLE_MCP_LEGACY_TOKEN` | `false` | 允许旧版 MCP Token 鉴权，仅迁移旧客户端时开启 |
| `MCP_BASE_PATH` | `/mcp` | MCP HTTP 挂载路径 |
| `MCP_REQUEST_TIMEOUT_SECONDS` | `15` | MCP 单次请求超时 |
| `MCP_REQUESTS_PER_MINUTE` | `120` | MCP 每分钟限流 |

> 注意：`QDRANT_VECTOR_SIZE` 必须与嵌入模型输出维度一致。切换嵌入模型时，如果维度变化，旧 Qdrant 集合不能直接复用；请清理旧集合、使用新的 `QDRANT_COLLECTION_PREFIX`，或重新创建知识库后重建索引。

Docker Compose 默认只把 Qdrant 端口绑定到 `127.0.0.1`。服务器部署时不要直接开放 `6333/6334` 到公网；如确需开放，请设置 `QDRANT_API_KEY` 并配合防火墙白名单。

### 认证初始化

本地开发默认关闭认证：

```bash
ENABLE_AUTH=false
```

服务器部署建议开启认证：

```bash
ENABLE_AUTH=true
AUTH_USERNAME=root
AUTH_PASSWORD=your-secure-password
```

首次启动时，如果设置了 `AUTH_PASSWORD`，后端会自动创建 root 用户并保存密码哈希。如果未设置 `AUTH_PASSWORD`，Web 页面会进入首次初始化向导。公网部署时建议至少设置 `AUTH_SETUP_TOKEN`，避免初始化窗口被他人抢占；如果两者都未设置，当前版本默认只允许本机回环地址完成首次初始化。

更多认证接口、API Key 和密码重置说明见 [`docs/AUTH.md`](./AUTH.md)。

升级、迁移或服务器故障恢复前，请先阅读 [`docs/backup-restore.md`](./backup-restore.md)，确认 `.env`、应用状态、聊天记录、上传文件和 Qdrant 数据都已备份。

---

## 接口概览

### 基础接口

- `GET /`：服务首页
- `GET /health`：健康检查
- `POST /upload`：通用上传入口

### 应用接口

- `GET /api/config`
- `PUT /api/config`
- `GET /api/conversations`
- `GET /api/conversations/:id`
- `PUT /api/conversations/:id`
- `DELETE /api/conversations/:id`
- `GET /api/knowledge-bases`
- `POST /api/knowledge-bases`
- `DELETE /api/knowledge-bases/:id`
- `GET /api/knowledge-bases/:id/documents`
- `POST /api/knowledge-bases/:id/documents`
- `DELETE /api/knowledge-bases/:id/documents/:documentId`

### OpenAI Compatible Chat 接口

- `POST /v1/chat/completions`
- `POST /v1/chat/completions/stream`

如需 MCP 专用接口与 JSON-RPC 示例，请查看 [`docs/mcp.md`](./mcp.md)。

---

## 检索流程说明

当用户发起问题时，系统通常会执行以下步骤：

1. 读取当前知识库与配置。
2. 为用户问题生成嵌入向量。
3. 在 Qdrant 中召回候选文档片段。
4. 对候选片段执行重排与去冗余。
5. 在低置信度场景下做二次扩召回。
6. 组装上下文并请求 Chat 模型。
7. 返回答案与命中文档来源。
8. 持久化当前会话记录。

更完整的架构与组件说明见 [`docs/architecture.md`](./architecture.md)。

---

## 测试与限制

### 测试与质量验证

当前包含：

- 后端单元测试
- 检索策略测试
- 路由 E2E 测试
- 前端构建验证

### 已知限制

- 当前更适合本地单机或轻量自托管使用
- PDF 解析效果受文档排版复杂度影响
- 向量维度需与嵌入模型严格匹配
- 部分高级检索能力默认关闭，需通过环境变量手动启用
- 语义缓存、查询改写、上下文压缩等能力仍以实验性增强为主

### 后续规划

- 嵌入维度自动适配
- 批量嵌入并发优化
- 更多文档类型支持
- 知识库导入与导出
- 多用户隔离与权限能力

---

## 相关文档

- [`README.md`](../README.md)
- [`docs/architecture.md`](./architecture.md)
- [`docs/backup-restore.md`](./backup-restore.md)
- [`docs/mcp.md`](./mcp.md)
- [`docs/retrieval-improvement-plan.md`](./retrieval-improvement-plan.md)
- [`DOCKER_DEPLOY.md`](../DOCKER_DEPLOY.md)
- [`TROUBLESHOOTING.md`](../TROUBLESHOOTING.md)
