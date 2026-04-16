# MCP 接入说明

## 概述

项目已内置 MCP Server 能力，作为后端服务的一部分运行，可供外部 Agent、脚本或支持 MCP 的客户端通过 HTTP / JSON-RPC 接入。

当前能力包括：

- 提供 HTTP 形式的 MCP 入口
- 提供工具列表发现能力
- 提供只读 / 写入 / 危险工具调用能力
- 提供 Bearer Token 鉴权
- 提供调用日志与耗时记录
- 提供工具权限分级（只读 / 写入 / 危险）
- 提供 MCP 级限流与超时保护
- 为危险工具提供二次确认机制
- 复用现有知识库、会话、配置与检索服务

---

## 启用方式

后端通过环境变量控制 MCP：

- `ENABLE_MCP`：是否启用 MCP，默认 `true`
- `MCP_BASE_PATH`：MCP 挂载路径，默认 `/mcp`
- `MCP_REQUEST_TIMEOUT_SECONDS`：单次 MCP 请求超时时间，默认 `15`
- `MCP_REQUESTS_PER_MINUTE`：MCP 每分钟最大请求数，默认 `120`
- MCP Token 会在首次启动时自动生成并持久化到应用配置中

示例：

```bash
ENABLE_MCP=true
MCP_BASE_PATH=/mcp
MCP_REQUEST_TIMEOUT_SECONDS=15
MCP_REQUESTS_PER_MINUTE=120
```

启动后可访问：

- `GET /mcp`：查看 MCP 服务基础信息
- `GET /mcp/tools`：查看当前可用工具列表
- `POST /mcp`：通过 JSON-RPC 调用 MCP 方法
- `POST /api/config/mcp/reset-token`：重置 MCP Token

> 所有 MCP 接口均需携带请求头 `Authorization: Bearer <token>`。

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

---

## 当前内置工具

当前共提供 **13 个 MCP 工具**，分为 **6 个只读工具**、**4 个写工具**、**3 个危险工具**。

### 权限级别说明

- `read-only`：只读工具，不修改系统状态
- `write`：普通写工具，会修改系统状态但风险相对可控
- `danger`：危险工具，通常涉及删除等不可逆操作

### 工具总览

| 工具名 | 权限级别 | 作用 |
|------|------|------|
| `get_config_summary` | `read-only` | 获取当前 Chat / Embedding 配置摘要 |
| `list_knowledge_bases` | `read-only` | 列出全部知识库及统计信息 |
| `list_documents` | `read-only` | 按知识库列出文档 |
| `list_conversations` | `read-only` | 列出全部会话 |
| `get_conversation` | `read-only` | 获取单个会话详情 |
| `search_knowledge_base` | `read-only` | 按知识库执行检索 |
| `create_knowledge_base` | `write` | 创建知识库 |
| `save_conversation` | `write` | 保存完整会话 |
| `upload_text_document` | `write` | 上传纯文本文档 |
| `upload_document` | `write` | 上传 Base64 编码的真实文件 |
| `delete_knowledge_base` | `danger` | 删除知识库 |
| `delete_document` | `danger` | 删除文档 |
| `delete_conversation` | `danger` | 删除会话 |

### 只读工具

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
- 适合真实文件二进制上传
- 当前代码层面默认支持 `.txt` / `.md` / `.pdf`
- 若服务配置满足条件，结构化敏感文件类型会额外放行
- 如果把普通文本伪装成二进制文件，解析阶段会失败

返回内容：

- 已上传并完成索引的文档对象
- 知识库 ID

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

- Bearer Token 鉴权
- 工具调用日志
- 调用耗时日志
- 方法不存在日志
- 工具调用失败日志
- 工具权限级别日志（read-only / write / danger）
- 每分钟请求数限制
- 单次请求超时保护
- 危险工具二次确认机制

日志输出位置在 [`backend/internal/mcp/server.go`](../backend/internal/mcp/server.go:1)。

### 危险工具二次确认

当调用 `danger` 工具时，除 `Authorization` 外，还需要提供二次确认：

优先方式：

```http
X-MCP-Confirm: <token>
```

兼容方式：

```text
?confirm_token=<token>
```

如果未提供确认头或确认值错误，服务将返回 `403`。

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
  -H "Authorization: Bearer <MCP_TOKEN>"
```

### 2. 创建知识库

```bash
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <MCP_TOKEN>" \
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
  -H "Authorization: Bearer <MCP_TOKEN>" \
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

### 4. 上传真实文件二进制

先把文件转成 Base64：

```bash
base64 -i ./example.pdf
```

再调用：

```bash
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <MCP_TOKEN>" \
  -d '{
    "jsonrpc": "2.0",
    "id": 3,
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

### 5. 检索知识库

```bash
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <MCP_TOKEN>" \
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

```bash
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <MCP_TOKEN>" \
  -H "X-MCP-Confirm: <MCP_TOKEN>" \
  -d '{
    "jsonrpc": "2.0",
    "id": 5,
    "method": "tools/call",
    "params": {
      "name": "delete_document",
      "arguments": {
        "knowledgeBaseId": "kb-1",
        "documentId": "doc-1"
      }
    }
  }'
```

### 7. 保存会话

```bash
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <MCP_TOKEN>" \
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
  - `Authorization: Bearer <你的 MCP Token>`


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
- 查看当前 Token
- 一键复制 Token
- 一键重置 Token

这部分主要用于方便你管理对外服务的接入凭证，而不是作为 MCP 消费端展示工具轨迹。

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
