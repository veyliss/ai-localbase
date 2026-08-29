# MCP 平台化契约（v1.0）

本文档描述 AI LocalBase 当前 MCP 工具调用的稳定扩展契约。它建立在现有 MCP `2024-11-05` HTTP JSON-RPC 接口之上，**不改变已有工具名称、参数和权限级别**。

## 1. 版本标识

- JSON-RPC：`2.0`
- MCP 协议：`2024-11-05`
- 工具结果契约：`1.0`
- 服务信息：`GET /mcp`
- 工具列表：`GET /mcp/tools`
- 观测指标：`GET /mcp/metrics`

服务信息、`initialize`、`tools/list`、工具描述和工具结果都会返回 `resultContractVersion` 或 `contractVersion`，并公布 `errorCodes` 目录。当前版本仍为 `1.0`；本次新增字段均为向后兼容扩展，客户端应使用版本字段和已知字段判断兼容性，不应因为未知字段失败。

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

客户端不应根据中文错误消息判断错误类型，应优先读取 `error.data.error.code`、`retryable` 和 `requestId`。HTTP 鉴权、scope、限流和危险操作拒绝也会保留字符串 `error` 兼容字段，同时返回 `errorCode` 与 `requestId`。

## 4. 权限与敏感信息

- `/mcp`、`/mcp/tools`、`/mcp/metrics` 至少需要 `mcp:read`。
- 工具实际调用继续使用现有权限矩阵和 `mcp:admin` 管理员覆盖。
- `start_import_job` 根据 `jobType` 选择 scope：`import` / `batch_index` 使用 `mcp:upload`，`reindex` 使用 `mcp:write`，`eval_dataset` 使用 `mcp:eval`；工具描述中的 `annotations.scopeVariants` 会公布这组映射。
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

延迟样本按全局和工具分别在进程内保留最近 512 条，仅用于当前进程诊断；重启后重新统计。该端点受 MCP 鉴权和 `mcp:read` scope 保护。

## 6. 知识库治理数据

只读工具 `get_mcp_capabilities` 会返回服务能力摘要，并与服务信息端点使用相同的契约版本和 `errorCodes` 目录；其中 `start_import_job` 会公开 `import`、`batch_index`、`reindex` 和 `eval_dataset` 对应的动态 scope。

只读工具 `inspect_knowledge_base_quality` 会返回安全化的索引治理信息：

- 当前索引规则版本。
- 文档索引状态和重建建议。
- 最近索引运行记录、触发来源、结果和错误分类。

索引错误分类包括 `source_missing`、`source_unreadable`、`vector_dimension_mismatch`、`index_rule_outdated` 和 `index_failed`。详细错误不会通过 MCP 质量结果泄露内部路径或基础设施地址。
