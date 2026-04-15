# AI LocalBase

一个本地优先的 AI 知识库系统（RAG），用于把本地文档接入向量检索与大模型对话流程。项目提供完整的 Web UI，支持知识库管理、文档上传、检索增强问答、聊天记录持久化，以及基于 Ollama 或 OpenAI 兼容接口的模型接入。

后端基于 Go + Gin，前端基于 React + Vite + TypeScript，向量数据库使用 Qdrant，适合个人或小团队在本地环境、自托管环境中快速搭建可用的知识库问答系统。

## 界面预览

### 首页

![首页预览](./assets/home-page.png)

## 功能特性

### 核心能力

- 知识库管理：创建、删除知识库，查看文档列表
- 文档上传与索引：支持 TXT、Markdown、PDF、xlsx、csv文件上传与解析
- 检索增强问答：基于 Qdrant 做向量检索并把命中内容注入对话上下文
- 聊天记录持久化：会话消息保存到本地 SQLite 数据库，重启后仍可恢复
- 配置持久化：模型配置与知识库状态保存到本地 JSON 文件
- Docker Compose 部署：支持一键拉起前端、后端、Qdrant

### 模型接入能力

- 原生支持 Ollama 聊天与嵌入调用
- 支持 OpenAI 兼容 API 聊天模型接入
- Chat 与 Embedding 可分别配置 Provider、Base URL、Model、API Key
- 模型调用失败时支持降级提示，避免前端直接报错

### 检索增强能力

- 文本自动切分与批量嵌入
- 候选结果动态召回
- 关键词覆盖增强重排
- MMR 去冗余选择
- 低置信度场景二次扩召回
- 嵌入缓存与可选语义缓存
- 可选 Hybrid Search、Semantic Reranker、Query Rewrite、Context Compression

## 适用场景

- 本地个人知识库
- 团队内部文档问答
- 自托管 RAG 原型验证
- Ollama / OpenAI 兼容模型接入测试
- 检索策略实验与评估

## 技术栈

| 层级 | 技术 |
|------|------|
| 后端 | Go + Gin |
| 前端 | React 18 + Vite 5 + TypeScript |
| 向量数据库 | Qdrant |
| 文档解析 | TXT / Markdown / PDF |
| 数据持久化 | JSON + SQLite |
| 模型接口 | Ollama / OpenAI Compatible API |
| 部署方式 | 本地启动 / Docker Compose |

## 项目结构

```text
ai-localbase/
├── backend/
│   ├── main.go
│   ├── internal/
│   │   ├── config/
│   │   ├── handler/
│   │   ├── model/
│   │   ├── router/
│   │   ├── service/
│   │   └── util/
│   ├── eval/
│   └── data/
├── frontend/
│   ├── src/
│   ├── package.json
│   └── vite.config.ts
├── docker/
├── docs/
├── docker-compose.yml
├── docker-compose.qdrant.yml
└── docker-compose.app.yml
```

## 启动方式

### 方式一：本地开发启动

适合日常开发、调试接口、修改前端页面。

#### 1. 环境要求

- Go 1.21+
- Node.js 18+
- Docker Desktop（用于启动 Qdrant）
- Ollama，或任意 OpenAI 兼容模型服务

#### 2. 启动 Qdrant

在项目根目录执行：

```bash
docker compose -f docker-compose.qdrant.yml up -d
```

启动后默认地址：

- HTTP: `http://localhost:6333`
- gRPC: `localhost:6334`

#### 3. 启动后端

```bash
cd backend
go run .
```

后端默认监听：`http://localhost:8080`

首次启动会自动创建：

- `backend/data/uploads/`
- `backend/data/app-state.json`
- `backend/data/chat-history.db`

#### 4. 启动前端

```bash
cd frontend
npm install
npm run dev
```

前端默认地址：`http://localhost:5173`

---

### 方式二：Docker Compose 一键启动

适合快速体验或自托管部署验证。

在项目根目录执行：

```bash
docker compose up --build
```

默认启动以下服务：

| 服务 | 地址 |
|------|------|
| 前端 | `http://localhost:4173` |
| 后端 | `http://localhost:8080` |
| Qdrant HTTP API | `http://localhost:6333` |
| Qdrant gRPC | `localhost:6334` |

---

### 方式三：使用预构建镜像快速部署（推荐）

如果你不想本地编译，可以直接使用自动构建的 Docker 镜像：

```bash
docker compose -f docker-compose.prod.yml up -d
```

前端地址：`http://localhost:4173`  
后端地址：`http://localhost:8080`

> 📖 了解更多镜像构建、版本管理和部署细节，请查看 [Docker 镜像与部署指南](./DOCKER_DEPLOY.md)

---

### 方式四：仅启动应用编排

如果你希望单独使用项目提供的完整应用编排文件，也可以执行：

```bash
docker compose -f docker-compose.app.yml up --build
```

该文件同样会启动：

- `qdrant`
- `backend`
- `frontend`

## 快速使用流程

### 遇到问题？

如果在启动或使用过程中遇到问题，请查看 [故障排查指南](./TROUBLESHOOTING.md)，涵盖常见错误诊断、Docker + Ollama 集成、模型调用失败等问题的解决方案。

### 1. 配置模型

打开前端后，进入 Settings 页面，分别配置 Chat 与 Embedding。

![设置页面](./assets/setting.png)

#### Ollama 示例

**Chat 配置**

- Provider: `ollama`
- Base URL: `http://localhost:11434`
- Model: `qwen2.5:7b` 或 `llama3.2`
- API Key: 留空

**Embedding 配置**

- Provider: `ollama`
- Base URL: `http://localhost:11434`
- Model: `bge-m3` 或 `nomic-embed-text`
- API Key: 留空

#### OpenAI Compatible 示例

**Chat 配置**

- Provider: `openai`
- Base URL: 你的兼容接口地址，例如 `https://your-api.example.com/v1`
- Model: 对应聊天模型名
- API Key: 对应访问密钥

**Embedding 配置**

- Provider: `openai`
- Base URL: 你的兼容接口地址
- Model: 对应嵌入模型名
- API Key: 对应访问密钥

### 2. 创建知识库并上传文档

1. 打开左侧知识库面板
2. 创建一个新的知识库
3. 选择 TXT、Markdown 或 PDF 文档上传
4. 等待文档状态变为 `indexed`

### 3. 发起问答

1. 切换到聊天界面
2. 选择目标知识库
3. 输入问题并发送
4. 系统会自动完成检索、重排、上下文拼装与模型调用

### Demo 演示

<p align="center">
  <img src="./assets/demo-1.1.png" alt="Demo 1" width="48%" />
  <img src="./assets/demo-1.2.png" alt="Demo 2" width="48%" />
</p>

### 4. 查看持久化数据

默认本地数据位于：

- `backend/data/app-state.json`：应用配置与知识库状态
- `backend/data/chat-history.db`：聊天记录
- `backend/data/uploads/`：上传文件

## 启动命令速查

### 后端

```bash
cd backend
go run .
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

### 后端测试

```bash
cd backend
go test ./...
```

### 启动 Qdrant

```bash
docker compose -f docker-compose.qdrant.yml up -d
```

### 一键启动全部服务

```bash
docker compose up --build
```

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

> 注意：`QDRANT_VECTOR_SIZE` 必须与所使用的嵌入模型输出维度一致。切换嵌入模型时，如果维度变化，建议清理旧集合或创建新的知识库。

## 对外接口概览

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

## 检索流程说明

当用户发起问题时，系统通常会执行以下步骤：

1. 读取当前知识库与配置
2. 为用户问题生成嵌入向量
3. 在 Qdrant 中召回候选文档片段
4. 对候选片段执行重排与去冗余
5. 在低置信度场景下做二次扩召回
6. 组装上下文并请求 Chat 模型
7. 返回答案与命中文档来源
8. 持久化当前会话记录

## 测试与质量验证

### 后端测试

```bash
cd backend
go test ./...
```

当前包含：

- 单元测试
- 检索策略测试
- 路由 E2E 测试

### 前端构建验证

```bash
cd frontend
npm run build
```

## 已知限制

- 当前更适合本地单机或轻量自托管使用
- PDF 解析效果受文档排版复杂度影响
- 向量维度需与嵌入模型严格匹配
- 部分高级检索能力默认关闭，需通过环境变量手动启用
- 语义缓存、查询改写、上下文压缩等能力仍以实验性增强为主

## 开源协作

- License: [LICENSE](./LICENSE)
- 贡献指南: [CONTRIBUTING.md](./CONTRIBUTING.md)
- 安全策略: [SECURITY.md](./SECURITY.md)
- 更新记录: [CHANGELOG.md](./CHANGELOG.md)
- 架构文档: [docs/architecture.md](./docs/architecture.md)
- 开源计划: [docs/open-source-plan.md](./docs/open-source-plan.md)

## 后续规划

- 嵌入维度自动适配
- 批量嵌入并发优化
- 更多文档类型支持
- 知识库导入与导出
- 多用户隔离与权限能力

**如果这个项目对你有帮助，请给个 ⭐ Star!**

## Star History

[![Star History Chart](https://api.star-history.com/image?repos=veyliss/ai-localbase&type=date&legend=top-left)](https://www.star-history.com/?repos=veyliss%2Fai-localbase&type=date&legend=top-left)