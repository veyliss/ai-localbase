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

### 后端运行

```bash
cd backend
go run .
```

### 后端测试

```bash
cd backend
go test ./...
```

### 前端开发

```bash
cd frontend
npm install
npm run dev
```

### 前端构建

```bash
cd frontend
npm run build
```

### 启动 Qdrant

```bash
docker compose -f docker-compose.qdrant.yml up -d
```

### 启动全部服务

```bash
docker compose up --build
```

---

## 关键环境变量

后端环境变量由 `backend/internal/config/config.go` 加载，常用项如下：

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PORT` | `8080` | 后端服务监听端口 |
| `UPLOAD_DIR` | `data/uploads` | 上传文件目录 |
| `STATE_FILE` | `data/app-state.json` | 应用状态文件 |
| `CHAT_HISTORY_FILE` | `data/chat-history.db` | 聊天记录 SQLite 文件 |
| `QDRANT_URL` | `http://localhost:6333` | Qdrant 地址 |
| `QDRANT_API_KEY` | 空 | Qdrant API Key |
| `QDRANT_COLLECTION_PREFIX` | `kb_` | 知识库集合名前缀 |
| `QDRANT_VECTOR_SIZE` | `1024` | 向量维度 |
| `QDRANT_DISTANCE` | `Cosine` | 距离算法 |
| `QDRANT_TIMEOUT_SECONDS` | `5` | Qdrant 超时秒数 |
| `ENABLE_HYBRID_SEARCH` | `false` | 启用 Hybrid Search |
| `ENABLE_SEMANTIC_RERANKER` | `false` | 启用语义重排 |
| `ENABLE_QUERY_REWRITE` | `false` | 启用 Query Rewrite |
| `ENABLE_SEMANTIC_CACHE` | `false` | 启用语义缓存 |
| `ENABLE_CONTEXT_COMPRESSION` | `false` | 启用上下文压缩 |
|

> 注意：`QDRANT_VECTOR_SIZE` 必须与嵌入模型输出维度一致。切换嵌入模型时，如果维度变化，建议清理旧集合或创建新的知识库。

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
- [`docs/mcp.md`](./mcp.md)
- [`docs/retrieval-improvement-plan.md`](./retrieval-improvement-plan.md)
- [`DOCKER_DEPLOY.md`](../DOCKER_DEPLOY.md)
- [`TROUBLESHOOTING.md`](../TROUBLESHOOTING.md)
