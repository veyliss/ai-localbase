# MCP 接入说明

## 概述

项目已内置 MCP Server 能力，作为后端服务的一部分运行，可供外部 Agent、脚本或支持 MCP 的客户端通过 HTTP / JSON-RPC 接入。

当前能力包括：

- MCP Server 版本：**0.2.0**
- 提供 HTTP 形式的 MCP 入口
- 提供工具列表发现能力
- 提供只读 / 写入 / 危险工具调用能力
- 提供 API Key Scope 鉴权；旧 MCP 全权限 Token 已废弃，仅保留迁移兼容开关
- 提供调用日志与耗时记录
- 提供工具权限分级（只读 / 写入 / 危险）
- 提供 MCP 级限流与超时保护
- 为危险工具提供一次性确认机制
- 复用现有知识库、会话、配置与检索服务

---

## 启用方式

后端通过环境变量控制 MCP：

- `ENABLE_MCP`：是否启用 MCP，默认 `false`
- `ENABLE_MCP_LEGACY_TOKEN`：是否允许已废弃的旧版 MCP Token 鉴权，默认 `false`，仅迁移旧客户端时临时开启
- `MCP_BASE_PATH`：MCP 挂载路径，默认 `/mcp`。Docker 前端同源代理支持 `/mcp` 或以 `/mcp` 结尾的嵌套路径，例如 `/agent/mcp`
- `MCP_REQUEST_TIMEOUT_SECONDS`：单次 MCP 请求超时时间，默认 `15`
- `MCP_REQUESTS_PER_MINUTE`：MCP 每分钟最大请求数，默认 `120`
- MCP Token 会在首次启动时自动生成并持久化到应用配置中，等价 MCP 全权限；该方式已废弃，默认不允许鉴权，仅作为旧客户端迁移凭证

示例：

```bash
ENABLE_AUTH=true
ENABLE_MCP=true
ENABLE_MCP_LEGACY_TOKEN=false
MCP_BASE_PATH=/mcp
MCP_REQUEST_TIMEOUT_SECONDS=15
MCP_REQUESTS_PER_MINUTE=120
```

MCP 默认关闭。服务器部署如需开启 MCP，必须同时设置 `ENABLE_AUTH=true`，并使用 API Key Scope 模式接入。旧版 MCP Token 等价 MCP 全权限，已废弃且默认不允许鉴权；仅迁移旧客户端时临时设置 `ENABLE_MCP_LEGACY_TOKEN=true`，且 Token 为空时不会放行旧 Token 请求。

当前危险工具确认 **只接受 `confirmNonce`**。即使启用了 `ENABLE_MCP_LEGACY_TOKEN=true`，服务端也不会再接受 `X-MCP-Confirm` 或 `?confirm_token=` 作为危险操作确认方式。

如果将 `MCP_BASE_PATH` 改成不以 `/mcp` 结尾的路径，需要自行配置外部反向代理，或让 MCP 客户端直接访问后端端口。

启动后可访问：

- `GET /mcp`：查看 MCP 服务基础信息
- `GET /mcp/tools`：查看当前可用工具列表
- `POST /mcp`：通过 JSON-RPC 调用 MCP 方法
- `POST /api/config/mcp/danger-confirmations`：创建危险工具一次性确认 nonce
- `POST /api/config/mcp/reset-token`：重置 MCP Token（仅在 `ENABLE_MCP_LEGACY_TOKEN=true` 时用于旧客户端迁移）

> MCP 新接入均应携带请求头 `Authorization: Bearer <API_KEY>`。旧版 MCP Token 已废弃，只有在 `ENABLE_MCP_LEGACY_TOKEN=true` 时才可用且等价全权限；新客户端必须使用带 MCP scope 的 API Key。

---

## API Key Scope

在 Settings 的“系统授权”页创建 API Key，并选择所需 MCP scope：

| Scope | 权限 |
|------|------|
| `mcp:read` | 工具发现、列表、检索、文档详情、会话读取等只读工具 |
| `mcp:write` | 创建知识库、保存会话、重建索引等写入工具 |
| `mcp:upload` | `upload_text_document`、`upload_document`、`register_staged_upload`、`start_import_job` |
| `mcp:eval` | `generate_eval_dataset`、`create_eval_case_from_query` |
| `mcp:danger` | 删除知识库、删除文档、删除会话 |
| `mcp:admin` | 允许调用全部 MCP 工具 |

权限是精确匹配的：`mcp:write` 不包含上传、评估或危险工具权限；需要批量授权时可以使用 `mcp:admin`。

---

## 当前支持的方法

### `initialize`

用于初始化 MCP 会话，返回协议版本、服务信息与工具能力描述。

### `tools/list`

返回全部已注册工具。

### `tools/call`

调用指定工具。

请求格式示例：

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "list_knowledge_bases",
    "arguments": {}
  }
}
```

工具调用成功时，`result` 统一包含：

- `summary`：一句话摘要
- `content`：兼容 MCP 客户端的文本内容
- `data`：结构化数据
- `warnings`：警告列表
- `nextActions`：建议后续动作
- `requestId`：服务端请求 ID
- `isError`：工具级错误标记

旧客户端可继续读取 `content` 和 `data`；新客户端建议优先读取 `summary`、`data` 和 `requestId`。

---

## 当前内置工具

当前共提供 **31 个 MCP 工具**，分为 **19 个只读工具**、**9 个写工具**、**3 个危险工具**。

### 权限级别说明

- `read-only`：只读工具，不修改系统状态
- `write`：普通写工具，会修改系统状态但风险相对可控
- `danger`：危险工具，通常涉及删除等不可逆操作

### 工具总览

| 工具名 | 权限级别 | 作用 |
|------|------|------|
| `get_mcp_capabilities` | `read-only` | 获取 MCP Server 版本、协议、工具数量和权限分布 |
| `get_config_summary` | `read-only` | 获取当前 Chat / Embedding 配置摘要 |
| `list_knowledge_bases` | `read-only` | 列出全部知识库及统计信息 |
| `list_documents` | `read-only` | 按知识库列出文档 |
| `get_document_detail` | `read-only` | 获取文档详情、索引诊断和 chunk 预览 |
| `list_conversations` | `read-only` | 列出全部会话 |
| `get_conversation` | `read-only` | 获取单个会话详情 |
| `search_knowledge_base` | `read-only` | 按知识库执行检索 |
| `search_document` | `read-only` | 按单个文档执行检索 |
| `search_web` | `read-only` | 基于 You.com Search API 执行互联网检索 |
| `query_structured_data` | `read-only` | 对 CSV / XLSX 执行确定性结构化查询 |
| `debug_retrieval` | `read-only` | 调试检索命中、低置信和确定性补全 |
| `answer_with_sources` | `read-only` | 基于知识库或文档生成带来源的答案草稿 |
| `inspect_knowledge_base_quality` | `read-only` | 聚合索引健康、最近评估和质量建议 |
| `compare_retrieval_modes` | `read-only` | 对比 dense 与 hybrid 检索结果 |
| `summarize_document` | `read-only` | 返回文档摘要、索引诊断和 chunk 预览 |
| `generate_eval_dataset` | `read-only` | 生成 RAG 评估数据集 |
| `create_eval_case_from_query` | `write` | 根据检索问题创建待审核评测样本 |
| `create_knowledge_base` | `write` | 创建知识库 |
| `save_conversation` | `write` | 保存完整会话 |
| `upload_text_document` | `write` | 上传纯文本文档 |
| `upload_document` | `write` | 上传 Base64 编码的真实文件 |
| `register_staged_upload` | `write` | 注册 HTTP 暂存上传文件 |
| `reindex_document` | `write` | 重建文档索引 |
| `start_import_job` | `write` | 启动异步导入任务 |
| `get_job_status` | `read-only` | 查询 Job 状态 |
| `cancel_job` | `write` | 取消 Job |
| `list_recent_jobs` | `read-only` | 列出最近 Job |
| `delete_knowledge_base` | `danger` | 删除知识库 |
| `delete_document` | `danger` | 删除文档 |
| `delete_conversation` | `danger` | 删除会话 |

### 只读工具

#### `get_mcp_capabilities`

权限级别：`read-only`

输入参数：无

返回内容：

- MCP Server 名称与版本
- MCP 协议版本与 JSON-RPC 版本
- HTTP 挂载路径与启用状态
- 工具总数
- 按 `read-only` / `write` / `danger` 统计的权限分布
- 当前工具清单
- 鉴权类型与 Token 是否已配置
- 危险工具确认 nonce 端点与兼容头信息
- `authModel`、`requiredScopes`、`jobSupport`
- `resultContractVersion`

#### `get_config_summary`

权限级别：`read-only`

输入参数：无

返回内容：

- 当前 Chat 模型的 Provider 与 Model
- 当前 Embedding 模型的 Provider 与 Model
- 完整配置摘要结构

#### `list_knowledge_bases`

权限级别：`read-only`

输入参数：无

返回内容：

- 知识库 `id`
- 知识库 `name`
- `description`
- `documentCount`
- `createdAt`

#### `list_documents`

权限级别：`read-only`

输入参数：

- `knowledgeBaseId`（必填）

返回内容：

- 文档 `id`
- `knowledgeBaseId`
- `name`
- `sizeLabel`
- `uploadedAt`
- `status`
- `contentPreview`

#### `get_document_detail`

权限级别：`read-only`

输入参数：

- `knowledgeBaseId`（必填）
- `documentId`（必填）

返回内容：

- 文档基础信息
- 原文预览
- 摘要预览
- chunk 预览
- 索引诊断信息，包括 chunk 数、向量数、摘要 chunk 数、结构化行 chunk 数和 Qdrant 状态

#### `list_conversations`

权限级别：`read-only`

输入参数：无

返回内容：

- 全部会话列表
- 会话基础信息

#### `get_conversation`

权限级别：`read-only`

输入参数：

- `conversationId`（必填）

返回内容：

- 会话标题
- 消息列表
- 关联知识库 / 文档信息

#### `search_knowledge_base`

权限级别：`read-only`

输入参数：

- `knowledgeBaseId`（必填）
- `query`（必填）

返回内容：

- 检索命中的上下文文本
- `sources` 来源列表
- 请求使用的知识库 ID 与查询词

#### `search_document`

权限级别：`read-only`

输入参数：

- `documentId`（必填）
- `query`（必填）

返回内容：

- 单文档范围内检索命中的上下文文本
- `sources` 来源列表
- 请求使用的文档 ID 与查询词

#### `search_web`

权限级别：`read-only`

输入参数：

- `query`（必填）
- `count`（选填，返回结果数量，最大 20）
- `freshness`（选填，day/week/month/year）
- `language`（选填，BCP 47 格式，例如 zh-CN）
- `country`（选填，ISO 3166-1 alpha-2 格式，例如 CN）

说明：

- 基于 You.com Search API（`GET https://ydc-index.io/v1/search`）执行互联网检索
- 需要配置环境变量 `YDC_API_KEY`；未配置时返回提示信息而非报错，`data.configured` 为 `false`
- 检索失败（鉴权、限流、服务端错误等）时以工具错误形式返回，错误信息不包含 API Key

返回内容：

- 编号列出的网页 / 新闻结果，含标题、链接、描述和首条摘录
- `web`、`news` 结构化结果列表
- 请求使用的查询词

#### `query_structured_data`

权限级别：`read-only`

输入参数：

- `query`（必填）
- `documentId`（选填）
- `knowledgeBaseId`（选填）

说明：

- `documentId` 或 `knowledgeBaseId` 至少提供一个
- 支持 CSV / XLSX 表格的预览、筛选、计数、最大值、最小值、平均值和分布统计
- 当前结构化查询会直接读取原始表格行，适合“薪资最高是谁”“平均年龄是多少”这类确定性问题

返回内容：

- Markdown 格式的结构化查询结果
- `sources` 来源列表
- `matched` 是否成功匹配结构化查询计划

#### `debug_retrieval`

权限级别：`read-only`

输入参数：

- `query`（必填）
- `knowledgeBaseId`（选填）
- `documentId`（选填）
- `topK`（选填）

说明：

- `knowledgeBaseId` 或 `documentId` 至少提供一个
- 用于调试真实检索命中、chunk 分数、结构化确定性补全和低置信状态
- 当结果低置信时，会返回可人工复核的评测候选 `evalCandidate`

返回内容：

- 命中 chunk 列表
- 检索耗时
- `lowConfidence`
- `deterministicUsed`
- `structuredIntent`
- `targetField`
- `contextPreview`
- `evalCandidate`

#### `answer_with_sources`

权限级别：`read-only`，需要 `mcp:read` 或 `mcp:admin`

输入参数：

- `query`（必填）
- `knowledgeBaseId`（选填）
- `documentId`（选填）

说明：

- `knowledgeBaseId` 或 `documentId` 至少提供一个
- 优先尝试结构化确定性查询，未命中时返回检索上下文答案草稿
- 适合 Agent 在回答用户前先获取可引用证据包

返回内容：

- `answer`
- `sources`
- `mode`
- `warnings`
- `nextActions`

#### `inspect_knowledge_base_quality`

权限级别：`read-only`，需要 `mcp:read` 或 `mcp:admin`

输入参数：

- `knowledgeBaseId`（必填）

返回内容：

- 知识库健康检查 `health`
- 最近评估历史 `evalRuns`
- 最近一次评估 `latestEvalRun`
- 可执行质量建议 `insights`

#### `compare_retrieval_modes`

权限级别：`read-only`，需要 `mcp:read` 或 `mcp:admin`

输入参数：

- `query`（必填）
- `knowledgeBaseId`（选填）
- `documentId`（选填）
- `topK`（选填，默认 `5`）

说明：

- `knowledgeBaseId` 或 `documentId` 至少提供一个
- 对同一问题分别运行 `dense` 与 `hybrid`
- 返回推荐模式、两组调试结果和质量提示

#### `summarize_document`

权限级别：`read-only`，需要 `mcp:read` 或 `mcp:admin`

输入参数：

- `knowledgeBaseId`（必填）
- `documentId`（必填）

返回内容：

- 文档摘要文本
- 文档基础信息
- 索引诊断
- 前若干个 chunk 预览

#### `generate_eval_dataset`

权限级别：`read-only`，需要 `mcp:eval` 或 `mcp:admin`

输入参数：

- `knowledgeBaseId`（选填）
- `documentId`（选填）
- `maxPerDocument`（选填，默认 `5`，最大 `20`）

返回内容：

- 评估数据集
- 覆盖文档数量
- 生成样本数量

#### `create_eval_case_from_query`

权限级别：`write`，需要 `mcp:eval` 或 `mcp:admin`

输入参数：

- `query`（必填）
- `knowledgeBaseId`（选填）
- `documentId`（选填）
- `topK`（选填，默认 `5`）

说明：

- `knowledgeBaseId` 或 `documentId` 至少提供一个
- 基于检索调试结果生成待审核样本
- 样本默认写入评测候选数据集，处于待审核状态

返回内容：

- `candidate`
- `dataset`
- `created`
- `debug`

### 写工具

#### `create_knowledge_base`

权限级别：`write`

输入参数：

- `name`（必填）
- `description`（选填）

返回内容：

- 新建知识库对象
- 创建成功提示

#### `save_conversation`

权限级别：`write`

输入参数：

- `id`（必填）
- `messages`（必填）
- `title`（选填）
- `knowledgeBaseId`（选填）
- `documentId`（选填）

其中 `messages` 为数组，数组元素通常包含：

- `id`（选填）
- `role`（必填）
- `content`（必填）
- `createdAt`（选填，未传时自动补齐）

返回内容：

- 保存后的完整会话对象

#### `upload_text_document`

权限级别：`write`

输入参数：

- `knowledgeBaseId`（必填）
- `fileName`（必填）
- `content`（必填）

说明：

- 仅用于纯文本上传
- 支持 `.txt` / `.md` / `.csv`
- 不支持 `.pdf` / `.xlsx`
- 适合作为 MCP 主上传通道
- 适合直接粘贴的小文本内容，不适合大体积二进制文件

返回内容：

- 已上传并完成索引的文档对象
- 知识库 ID

#### `upload_document`

权限级别：`write`

输入参数：

- `knowledgeBaseId`（必填）
- `fileName`（必填）
- `contentBase64`（必填）

说明：

- 使用 Base64 传输文件内容
- **仅适用于小文件兼容场景**
- 当前代码层面默认支持 `.txt` / `.md` / `.pdf`
- 若服务配置满足条件，结构化敏感文件类型会额外放行
- 如果把普通文本伪装成二进制文件，解析阶段会失败
- 当前内联上传大小限制为约 `256KB`
- 超限时会提示先走 HTTP [`/api/uploads`](docs/mcp.md) 暂存，再调用 `register_staged_upload`

返回内容：

- 已上传并完成索引的文档对象
- 知识库 ID
- 临时上传 ID（小文件路径下也会经过 staging 注册）

#### `register_staged_upload`

权限级别：`write`

输入参数：

- `uploadId`（必填）
- `knowledgeBaseId`（必填）
- `fileName`（选填）

说明：

- 用于把已通过 HTTP [`/api/uploads`](docs/mcp.md) 暂存的文件注册到知识库
- **这是大文件推荐上传通道**
- 服务端会基于 `uploadId` 读取暂存文件并执行索引

返回内容：

- 已注册并完成索引的文档对象
- 知识库 ID
- 对应的 `uploadId`

#### `reindex_document`

权限级别：`write`

输入参数：

- `knowledgeBaseId`（必填）
- `documentId`（必填）

说明：

- 重新解析原始文件
- 重建文档 chunk
- 刷新向量索引
- 适合模型配置、向量维度、混合检索或结构化解析逻辑变更后使用

返回内容：

- 重建后的文档对象
- 知识库 ID

### Job 工作流工具

Job 工具用于避免长任务占用一次 JSON-RPC 调用。当前实现使用内存状态，不引入新数据库；服务重启后历史 Job 会清空。

统一 Job 返回结构：

- `jobId`：任务 ID
- `status`：`queued` / `running` / `succeeded` / `failed` / `cancelled`
- `progress`：0-100 的进度值
- `summary`：简短状态摘要
- `result`：成功结果
- `error`：失败原因
- `warnings`：警告列表；取消类 Job 会提示取消是 best-effort，底层导入进入注册或索引阶段后可能已经完成副作用

#### `start_import_job`

权限级别：`write`，需要 `mcp:upload` 或 `mcp:admin`

输入参数：

- `knowledgeBaseId`（必填）
- `fileName`（必填）
- `content`（选填，文本内容；留空会形成失败 Job）

#### `get_job_status`

权限级别：`read-only`

输入参数：

- `jobId`（必填）

#### `cancel_job`

权限级别：`write`

输入参数：

- `jobId`（必填）

#### `list_recent_jobs`

权限级别：`read-only`

输入参数：

- `limit`（选填，默认 20，最大 20）

### 危险工具

#### `delete_knowledge_base`

权限级别：`danger`

输入参数：

- `knowledgeBaseId`（必填）

返回内容：

- 被删除的知识库 ID
- 当前剩余知识库数量

#### `delete_document`

权限级别：`danger`

输入参数：

- `knowledgeBaseId`（必填）
- `documentId`（必填）

返回内容：

- 被删除的文档对象

#### `delete_conversation`

权限级别：`danger`

输入参数：

- `id`（必填）

返回内容：

- 被删除的会话 ID

---

## 审计与安全

当前 MCP 接口已具备：

- API Key Scope 鉴权
- 工具调用日志
- 调用耗时日志
- 方法不存在日志
- 工具调用失败日志
- 工具权限级别日志（read-only / write / danger）
- 每分钟请求数限制
- 单次请求超时保护
- 危险工具一次性确认机制

日志输出位置在 [`backend/internal/mcp/server.go`](../backend/internal/mcp/server.go:1)。

### 危险工具一次性确认

当调用 `danger` 工具时，除 API Key 必须具备 `mcp:danger` 或 `mcp:admin` 外，还需要先创建一次性确认 nonce。

创建确认：

```bash
curl -X POST http://localhost:8080/api/config/mcp/danger-confirmations \
  -H "Content-Type: application/json" \
  -b "ai_localbase_session=<WEB_SESSION_COOKIE>" \
  -d '{
    "toolName": "delete_document",
    "arguments": {
      "knowledgeBaseId": "kb-1",
      "documentId": "doc-1"
    }
  }'
```

返回：

```json
{
  "confirmNonce": "mcp_confirm_xxx",
  "expiresAt": "2026-06-22T12:00:00Z",
  "toolName": "delete_document",
  "paramHash": "..."
}
```

调用危险工具时，把 `confirmNonce` 放入工具 `arguments`。nonce 会绑定工具名和参数 hash，过期或使用后立即失效。

旧版 `X-MCP-Confirm` 和 `?confirm_token=` 已完全停用；即使 `ENABLE_MCP_LEGACY_TOKEN=true` 也不能再作为危险操作确认方式。新客户端必须先通过 `POST /api/config/mcp/danger-confirmations` 获取 nonce，再把 `confirmNonce` 放进危险工具 `arguments`。

如果未提供 nonce、nonce 错误、重复使用或已过期，服务将返回 `403`。

---

## 限流与超时

### 限流

MCP 服务按进程内窗口计数方式做每分钟限流：

- 由 `MCP_REQUESTS_PER_MINUTE` 控制
- 超出后返回 `429 Too Many Requests`

### 超时

MCP 工具调用统一包裹请求超时：

- 由 `MCP_REQUEST_TIMEOUT_SECONDS` 控制
- 超时后返回 `504 Gateway Timeout`

---

## 外部接入示例

### 1. 获取工具列表

```bash
curl -X GET http://localhost:8080/mcp/tools \
  -H "Authorization: Bearer <MCP_API_KEY>"
```

### 2. 创建知识库

```bash
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <MCP_API_KEY>" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "tools/call",
    "params": {
      "name": "create_knowledge_base",
      "arguments": {
        "name": "测试知识库",
        "description": "通过 MCP 创建"
      }
    }
  }'
```

### 3. 上传纯文本文档

```bash
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <MCP_API_KEY>" \
  -d '{
    "jsonrpc": "2.0",
    "id": 2,
    "method": "tools/call",
    "params": {
      "name": "upload_text_document",
      "arguments": {
        "knowledgeBaseId": "kb-1",
        "fileName": "example.txt",
        "content": "这是一段直接写入知识库的纯文本内容。"
      }
    }
  }'
```

### 4. 大文件推荐：HTTP 暂存 + MCP 注册

先通过 HTTP 接口上传文件流：

```bash
curl -X POST http://localhost:8080/api/uploads \
  -F "file=@./example.pdf"
```

返回中会包含 `uploadId`。然后再调用 MCP 注册：

```bash
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <MCP_API_KEY>" \
  -d '{
    "jsonrpc": "2.0",
    "id": 3,
    "method": "tools/call",
    "params": {
      "name": "register_staged_upload",
      "arguments": {
        "uploadId": "upl_xxx",
        "knowledgeBaseId": "kb-1",
        "fileName": "example.pdf"
      }
    }
  }'
```

### 5. 小文件兼容：Base64 内联上传

先把文件转成 Base64：

```bash
base64 -i ./example.pdf
```

再调用：

```bash
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <MCP_API_KEY>" \
  -d '{
    "jsonrpc": "2.0",
    "id": 4,
    "method": "tools/call",
    "params": {
      "name": "upload_document",
      "arguments": {
        "knowledgeBaseId": "kb-1",
        "fileName": "example.pdf",
        "contentBase64": "<BASE64_CONTENT>"
      }
    }
  }'
```

> 当文件较大时，`upload_document` 会直接拒绝，并提示改走 [`/api/uploads`](docs/mcp.md) + `register_staged_upload`。

### 6. 检索知识库

```bash
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <MCP_API_KEY>" \
  -d '{
    "jsonrpc": "2.0",
    "id": 4,
    "method": "tools/call",
    "params": {
      "name": "search_knowledge_base",
      "arguments": {
        "knowledgeBaseId": "kb-1",
        "query": "总结这份文档的核心观点"
      }
    }
  }'
```

### 6. 删除文档（危险工具）

该示例需要 API Key 具备 `mcp:danger` 或 `mcp:admin` scope。

先创建一次性确认：

```bash
curl -X POST http://localhost:8080/api/config/mcp/danger-confirmations \
  -H "Content-Type: application/json" \
  -b "ai_localbase_session=<WEB_SESSION_COOKIE>" \
  -d '{
    "toolName": "delete_document",
    "arguments": {
      "knowledgeBaseId": "kb-1",
      "documentId": "doc-1"
    }
  }'
```

再调用工具：

```bash
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <MCP_API_KEY>" \
  -d '{
    "jsonrpc": "2.0",
    "id": 5,
    "method": "tools/call",
    "params": {
      "name": "delete_document",
      "arguments": {
        "knowledgeBaseId": "kb-1",
        "documentId": "doc-1",
        "confirmNonce": "mcp_confirm_xxx"
      }
    }
  }'
```

### 7. 保存会话

```bash
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <MCP_API_KEY>" \
  -d '{
    "jsonrpc": "2.0",
    "id": 6,
    "method": "tools/call",
    "params": {
      "name": "save_conversation",
      "arguments": {
        "id": "conv-1",
        "title": "测试会话",
        "messages": [
          {
            "id": "msg-1",
            "role": "user",
            "content": "你好",
            "createdAt": "2026-04-16T00:00:00Z"
          }
        ]
      }
    }
  }'
```

---

## Cherry Studio 接入示例

如果你希望在 Cherry Studio 中通过 MCP 接入本项目，可以按以下方式配置：

- **类型**：可流式传输的 HTTP（`streamableHttp`）
- **URL**：`http://127.0.0.1:8080/mcp`
- **请求头**：
  - `Content-Type: application/json`
  - `Authorization: Bearer <带 MCP scope 的 API Key>`
- **建议 scope**：`mcp:read`、`mcp:upload`、`mcp:eval`

Settings 的 MCP 页面也提供了可复制模板。以下示例中的 `<MCP_API_KEY>` 需要替换为带 MCP scope 的 API Key。

### Cherry Studio 模板

```json
{
  "name": "AI LocalBase",
  "type": "streamable-http",
  "url": "http://127.0.0.1:8080/mcp",
  "headers": {
    "Authorization": "Bearer <MCP_API_KEY>"
  }
}
```

### Claude Desktop 模板

```json
{
  "mcpServers": {
    "ai-localbase": {
      "type": "http",
      "url": "http://127.0.0.1:8080/mcp",
      "headers": {
        "Authorization": "Bearer <MCP_API_KEY>"
      }
    }
  }
}
```

建议 scope：`mcp:read`、`mcp:eval`。如果客户端需要写入或上传，再额外授予 `mcp:write`、`mcp:upload`。

### Cursor / 通用 HTTP MCP 模板

```json
{
  "server": "ai-localbase",
  "transport": "http",
  "endpoint": "http://127.0.0.1:8080/mcp",
  "headers": {
    "Authorization": "Bearer <MCP_API_KEY>"
  }
}
```

建议 scope：`mcp:read`、`mcp:write`、`mcp:upload`、`mcp:eval`。删除类工具需要单独授予 `mcp:danger` 并走一次性 `confirmNonce`。


![Cherry Studio MCP 设置页面](../assets/mcp_setting.png)

### MCP 演示

<p align="center">
  <img src="../assets/demo_mcp_1.1.png" alt="MCP Demo 1" width="48%" />
  <img src="../assets/demo_mcp_1.2.png" alt="MCP Demo 2" width="48%" />
</p>

---

## 实现结构

MCP 模块位于：

```text
backend/internal/mcp/
├── planner.go
├── server.go
├── tool_registry.go
├── tools.go
└── types.go
```

职责划分：

- [`server.go`](../backend/internal/mcp/server.go:1)：协议入口、鉴权、限流、超时、危险工具确认、JSON-RPC 分发
- [`tool_registry.go`](../backend/internal/mcp/tool_registry.go:1)：工具注册与调用调度
- [`tools.go`](../backend/internal/mcp/tools.go:1)：工具定义、参数校验、只读 / 写入 / 危险工具注册
- [`planner.go`](../backend/internal/mcp/planner.go:1)：聊天链路中的 Tool Use 规划与执行
- [`types.go`](../backend/internal/mcp/types.go:1)：MCP / JSON-RPC 基础结构

---

## 已接入的后端入口

- 启动挂载：[`backend/main.go`](../backend/main.go:1)
- 路由挂载：[`backend/internal/router/router.go`](../backend/internal/router/router.go:1)
- 配置读取：[`backend/internal/config/config.go`](../backend/internal/config/config.go:1)
- 配置模型：[`backend/internal/model/types.go`](../backend/internal/model/types.go:1)

---

## 前端设置支持

前端设置面板已支持：

- 查看 MCP 是否启用
- 查看 MCP Base Path
- 查看旧 Token 迁移状态
- 在旧 Token 迁移模式开启时复制迁移 Token
- 在旧 Token 迁移模式开启时重置迁移 Token

这部分主要用于兼容既有 MCP 客户端。新接入请在系统授权中创建带 MCP scope 的 API Key。

---

## 当前建议的服务定位

当前项目更适合作为：

- **本地知识库 MCP 服务端**
- **团队内部文档检索与会话管理 MCP 能力中心**
- **外部 Agent / 自动化系统的知识操作后端**

而不是在本项目前端内部重点展示工具调用细节。

---

## 相关文档

- [`README.md`](../README.md)
- [`docs/getting-started.md`](./getting-started.md)
- [`docs/architecture.md`](./architecture.md)
- [`backend/internal/mcp/tools.go`](../backend/internal/mcp/tools.go:1)
- [`backend/internal/mcp/tool_registry.go`](../backend/internal/mcp/tool_registry.go:1)
