# MCP 平台化契约（v1.1）

本文档描述 AI LocalBase 当前 MCP 工具调用的稳定扩展契约。它建立在现有 MCP `2024-11-05` HTTP JSON-RPC 接口之上，**已有工具的参数和权限级别保持兼容；新增能力只做增量扩展**。

## 1. 版本标识

- JSON-RPC：`2.0`
- MCP 协议：`2024-11-05`
- 默认工具结果契约：`1.0`
- 支持工具结果契约：`1.0`、`1.1`
- 服务信息：`GET /mcp`
- 工具列表：`GET /mcp/tools`
- 观测指标：`GET /mcp/metrics`

服务信息、`initialize`、`tools/list`、工具描述和工具结果都会返回 `resultContractVersion` 或 `contractVersion`，并公布 `errorCodes` 目录。**未声明版本时始终使用 `1.0`**，因此现有客户端不需要修改即可继续工作。

### 1.1 协商

由于当前 HTTP MCP 不保存会话，客户端需要在每次请求中声明 `1.1`：

- HTTP 请求头：`X-MCP-Result-Contract-Version: 1.1`
- JSON-RPC `initialize.params.resultContractVersion`：`1.1`
- 也接受 `tools/call` 等请求参数中的 `resultContractVersion`，便于无自定义请求头的客户端使用

HTTP 响应会返回：

- `X-MCP-Result-Contract-Version`：本次实际使用的版本
- `X-MCP-Supported-Contract-Versions`：当前支持的版本列表

`initialize`、`tools/list`、服务信息和能力摘要还会返回 `contractNegotiation`，其中包含请求版本、实际版本和是否回退。未支持的版本会安全回退到 `1.0`，不会破坏旧客户端；客户端应检查实际返回版本。

## 2. 工具成功结果

`tools/call` 成功时，JSON-RPC `result` 使用以下字段：

```json
{
  "contractVersion": "1.0",
  "summary": "已完成操作",
  "content": [{"type": "text", "text": "面向模型的可读结果"}],
  "data": {"items": []},
  "warnings": [],
  "nextActions": [],
  "requestId": "req-xxx",
  "isError": false
}
```

- `summary`：简短结果摘要。
- `content`：兼容 MCP 客户端的可读内容。
- `data`：稳定的结构化结果；不存在时可以省略。
- `warnings`：不会阻断结果、但需要调用方注意的问题。
- `nextActions`：建议的后续工具或人工动作。
- `requestId`：与 HTTP `X-Request-Id` 对应，用于日志关联。

当实际版本为 `1.1` 时，结果会额外提供 `meta`，核心字段保持不变：

```json
{
  "meta": {
    "contractVersion": "1.1",
    "requestId": "req-xxx"
  }
}
```

如果结果包含工具错误，`meta` 还会提供 `errorCode` 和 `retryable`，方便客户端统一处理错误。

## 3. 工具错误

工具执行失败时仍返回 JSON-RPC error，但 `error.data` 提供统一错误契约：

```json
{
  "code": -32000,
  "message": "document not found",
  "data": {
    "contractVersion": "1.0",
    "tool": "get_document_detail",
    "requestId": "req-xxx",
    "error": {
      "code": "not_found",
      "message": "document not found",
      "retryable": false,
      "requestId": "req-xxx"
    }
  }
}
```

当前稳定错误码（服务信息和工具描述中的 `errorCodes` 会返回完整目录）：

| 错误码 | 含义 | 是否建议重试 |
| --- | --- | --- |
| `invalid_argument` | 参数缺失、类型不正确或值无效 | 否 |
| `unauthenticated` | 缺少或无法验证 MCP 凭据 | 否 |
| `not_found` | 知识库、文档、任务等资源不存在 | 否 |
| `permission_denied` | 权限或 scope 不足 | 否 |
| `conflict` | 请求与资源当前状态冲突 | 否 |
| `index_not_ready` | 文档索引尚未就绪 | 是 |
| `dependency_unavailable` | Qdrant、模型或其他依赖不可用 | 是 |
| `timeout` | MCP 请求超过服务端超时 | 是 |
| `rate_limited` | 超过当前身份的频率限制 | 是 |
| `confirmation_required` | 危险操作缺少有效的一次性确认 | 否 |
| `cancelled` | 请求被取消 | 是 |
| `internal_error` | 未分类的服务端错误 | 否 |

客户端不应根据中文错误消息判断错误类型，应优先读取 `error.data.error.code`、`retryable` 和 `requestId`；使用 `1.1` 时也可以读取 `error.data.meta`。HTTP 鉴权、scope、限流和危险操作拒绝也会保留字符串 `error` 兼容字段，同时返回 `errorCode`、`requestId` 和契约版本。

## 4. 权限与敏感信息

- `/mcp`、`/mcp/tools`、`/mcp/metrics` 至少需要 `mcp:read`。
- 工具实际调用继续使用现有权限矩阵和 `mcp:admin` 管理员覆盖。
- `start_import_job` 根据 `jobType` 选择 scope：`import` / `batch_index` 使用 `mcp:upload`，`reindex` 使用 `mcp:write`，`eval_dataset` 使用 `mcp:eval`；工具描述中的 `annotations.scopeVariants` 会公布这组映射。
- `retry_job` 使用 `mcp:write`，但只允许重试当前身份拥有的失败 Job；原任务的输入权限在首次启动时已经校验，重试不会绕过资源归属检查。
- `tools/list` 的每个工具描述都会公布 `contractVersions`、`annotations.permissionLevel`、`annotations.requiredScopes` 和 `annotations.retryPolicy`。
- `/mcp/metrics` 只返回计数、延迟和错误类别，不返回 Token、参数内容、文件路径、Qdrant 地址或原文。
- 危险工具仍必须使用一次性 `confirmNonce`，不会恢复旧确认头。

## 5. 观测指标

`GET /mcp/metrics` 返回：

- 请求总数、成功数、失败数。
- 工具调用总数、成功数、失败数。
- 限流、鉴权失败、scope 拒绝次数。
- 请求和工具调用的 P50、P95、最大耗时，单位为毫秒。
- `toolMetrics`：按工具名称统计调用量、成功/失败数量和 P50、P95、最大耗时。
- 服务启动时间和当前契约版本。
- 支持的契约版本列表；通过 `X-MCP-Result-Contract-Version` 请求头选择本次响应版本。

延迟样本按全局和工具分别在进程内保留最近 512 条，仅用于当前进程诊断；重启后重新统计。该端点受 MCP 鉴权和 `mcp:read` scope 保护。

## 6. 知识库治理数据

只读工具 `get_mcp_capabilities` 会返回服务能力摘要，并与服务信息端点使用相同的契约版本和 `errorCodes` 目录；其中 `start_import_job` 会公开 `import`、`batch_index`、`reindex` 和 `eval_dataset` 对应的动态 scope。

只读工具 `inspect_knowledge_base_quality` 会返回安全化的索引治理信息：

- 当前索引规则版本。
- 文档索引状态和重建建议。
- 最近索引运行记录、触发来源、结果和错误分类。

索引错误分类包括 `source_missing`、`source_unreadable`、`vector_dimension_mismatch`、`index_rule_outdated` 和 `index_failed`。详细错误不会通过 MCP 质量结果泄露内部路径或基础设施地址。

## 7. 异步 Job 重试

长任务结果会返回以下重试字段：

- `retryable`：当前 Job 失败后是否允许显式重试。
- `retryCount`：当前 Job 已经被重试的次数。
- `parentJobId`：重试产生的子 Job 对应的原 Job ID。

客户端应先使用 `get_job_status` 等待任务进入 `failed`，再调用 `retry_job`。每次重试都会创建新的 Job，原 Job 不会被覆盖；最多允许 3 次。运行中、成功、取消或达到上限的 Job 会返回 `conflict`，不会因为重复请求再次执行。
