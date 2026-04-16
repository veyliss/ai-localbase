# AI LocalBase - 系统架构文档

## 系统架构图

```
[前端 React/Vite] ←→ [后端 Go/Gin] ←→ [Qdrant 向量数据库]
      ↓                      ↓
 [用户界面]           [文档处理流水线]
      ↓                      ↓
 [配置管理]           [AI API 调用层]
                             ↓
                   [Ollama 原生 API / OpenAI 兼容 API]
```

## 项目结构

```
ai-localbase/
├── backend/
│   ├── main.go                          # 启动入口（Qdrant连接、服务组装）
│   ├── go.mod / go.sum
│   ├── internal/
│   │   ├── config/config.go             # 环境变量加载（ServerConfig）
│   │   ├── handler/app_handler.go       # HTTP 请求处理（路由入口）
│   │   ├── model/types.go               # 数据模型（AppState、AppConfig、KnowledgeBase、Document）
│   │   ├── router/
│   │   │   ├── router.go                # 路由注册 + CORS 中间件
│   │   │   └── router_e2e_test.go       # 完整 E2E 测试（mock Qdrant + Ollama）
│   │   ├── service/
│   │   │   ├── app_service.go           # 业务主逻辑（CRUD、索引、检索管道）
│   │   │   ├── app_service_retrieval_test.go  # 检索管道单元测试
│   │   │   ├── app_state_store.go       # 状态 JSON 持久化（原子写入）
│   │   │   ├── app_state_store_test.go
│   │   │   ├── embedding_cache.go       # LRU 嵌入缓存（2048条，thread-safe）
│   │   │   ├── llm_service.go           # LLM 调用（OpenAI兼容 + Ollama原生 + 降级）
│   │   │   ├── qdrant_service.go        # Qdrant HTTP 客户端
│   │   │   ├── rag_service.go           # 切分/嵌入/检索（OpenAI兼容 + Ollama原生）
│   │   │   ├── rag_service_test.go
│   │   │   └── resilience.go            # 指数退避重试
│   │   └── util/
│   │       ├── document_text.go         # 文本提取（TXT/MD/PDF）
│   │       └── helpers.go
│   └── data/
│       ├── uploads/                     # 上传文件
│       └── app-state.json               # 持久化状态（自动生成）
├── frontend/
│   ├── index.html
│   ├── package.json
│   └── src/
│       ├── App.tsx                      # 应用根组件（状态管理、API调用）
│       ├── App.css
│       └── components/
│           ├── Sidebar.tsx              # 侧边栏（会话列表、知识库入口、设置入口）
│           ├── ChatArea.tsx             # 聊天区域（消息显示、流式输出）
│           ├── knowledge/
│           │   └── KnowledgePanel.tsx   # 知识库管理（创建/删除知识库、上传/删除文档）
│           └── settings/
│               └── SettingsPanel.tsx    # 配置面板（Chat + Embedding 独立配置）
├── docker/
│   ├── backend.Dockerfile
│   └── frontend.Dockerfile
├── docs/
│   ├── architecture.md                  # 本文档
│   ├── mcp.md                           # MCP 接入说明
│   ├── getting-started.md               # 快速开始与运行补充说明
│   └── retrieval-improvement-plan.md    # 检索命中优化方案
├── docker-compose.yml                   # 完整三服务编排（Qdrant + 后端 + 前端）
├── docker-compose.qdrant.yml            # 仅 Qdrant
├── docker-compose.app.yml               # 仅应用服务
└── README.md
```

## API 设计

### 后端 REST API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/health` | 健康检查，返回 Qdrant 状态和配置概要 |
| GET | `/api/config` | 获取 Chat + Embedding 配置 |
| PUT | `/api/config` | 更新 Chat + Embedding 配置 |
| GET | `/api/knowledge-bases` | 列出所有知识库 |
| POST | `/api/knowledge-bases` | 创建知识库 |
| DELETE | `/api/knowledge-bases/:id` | 删除知识库（含 Qdrant 集合）|
| GET | `/api/knowledge-bases/:id/documents` | 列出知识库文档 |
| POST | `/api/knowledge-bases/:id/documents` | 上传文档并索引 |
| DELETE | `/api/knowledge-bases/:id/documents/:docId` | 删除文档（含向量点）|
| POST | `/v1/chat/completions` | OpenAI 兼容聊天（含 RAG，非流式）|
| POST | `/v1/chat/completions/stream` | OpenAI 兼容聊天（含 RAG，SSE 流式）|

### 检索管道（RAG Pipeline）

```
用户提问
  → 查询向量化（Ollama /api/embed 或 OpenAI /v1/embeddings）
  → 嵌入缓存查询（LRU，命中则跳过 API）
  → Qdrant 相似度搜索（动态 candidateTopK）
  → rerankCandidates()（关键词覆盖度二次评分）
  → selectWithMMR()（最大边际相关，控制冗余，单文档限额）
  → 低置信度判断（最高分阈值 + 分差判断）
    → 触发二次检索（放宽参数）或直接答复
  → logRetrievalMetrics()（日志记录检索参数与命中情况）
  → 构建 Prompt（检索上下文 + 用户问题）
  → LLM 调用（Ollama /api/chat 或 OpenAI /v1/chat/completions，含重试+降级）
  → 流式/非流式返回
```

### 前端界面

- **侧边栏**：会话管理（新建/切换/删除）、知识库/文档筛选、设置入口
- **聊天区域**：消息显示（Markdown 渲染）、SSE 流式打字机效果、知识库选择
- **知识库面板**：创建知识库、上传文档（TXT/MD/PDF）、删除文档
- **设置面板**：Chat 模型配置（Provider/BaseURL/Model/APIKey/Temperature）、Embedding 模型配置

## 核心服务说明

### AppService
业务逻辑核心，协调 Qdrant、RAG、LLM 三个下游服务。负责知识库/文档 CRUD、文档索引流水线（提取→切分→嵌入→写入Qdrant）、检索管道（动态TopK→rerank→MMR→低置信度兜底）、状态持久化。

### RagService  
纯函数式文本处理层。负责文本切分（800字符窗口/120字符重叠）、嵌入调用（OpenAI兼容 + Ollama原生，含LRU缓存 + 分批 + 失败回退）、Prompt 上下文构建。

### LLMService
LLM 调用封装，支持 OpenAI 兼容（非流式+流式）和 Ollama 原生（非流式+流式）双路由，含指数退避重试和降级回退。

### QdrantService
Qdrant HTTP 客户端封装，支持集合生命周期管理、批量 Upsert（含重试）、过滤搜索。

### AppStateStore
应用状态 JSON 持久化，原子写入（先写临时文件再 rename），支持首次启动默认值和损坏文件降级处理。
