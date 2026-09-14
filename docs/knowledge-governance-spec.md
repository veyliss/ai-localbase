# 知识库治理规格（v1.0）

本文档定义知识库、文档和索引运行记录的治理边界。它服务于本地优先部署，不包含原文、评估样本、运行结果或密钥。

## 1. 治理目标

- 能判断一个文档当前是否可读、是否完成索引、是否需要重建。
- 能区分原文问题、索引规则问题、向量配置问题和未知失败。
- 能追溯最近一次索引由什么操作触发、使用哪个索引版本以及结果如何。
- 对外返回诊断结论时不泄露本地绝对路径、Qdrant 地址或密钥。
- 兼容已有 `app-state.json`，旧状态启动时自动补齐默认治理字段。

## 2. 数据字段

### 2.1 知识库

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string | 稳定知识库标识 |
| `tags` | string[] | 用于分类和筛选的标签，单个标签最多 32 个 Unicode 字符 |
| `createdAt` | RFC3339 | 创建时间 |
| `updatedAt` | RFC3339 | 最近一次文档或索引治理变更时间 |
| `currentIndexVersion` | int | 当前应用使用的索引规则版本 |
| `indexHistory` | IndexRunRecord[] | 最近索引运行记录，最多保留 50 条 |

### 2.2 文档

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `source` | string | `http`、`directory`、`mcp`、`upload` 或迁移产生的 `legacy` |
| `version` | int | 文档内容版本，旧数据默认为 1 |
| `checksum` | string | 原文 SHA-256，仅持久化，不通过公开文档接口返回 |
| `indexVersion` | int | 该文档最近一次成功索引使用的规则版本 |
| `indexRunId` | string | 最近一次索引运行记录 ID |
| `indexErrorCode` | string | 稳定的失败分类 |

## 3. 索引错误分类

| 错误码 | 含义 | 建议动作 |
| --- | --- | --- |
| `source_missing` | 原文路径为空或文件不存在 | 检查数据目录，必要时重新上传 |
| `source_changed` | 原文 SHA-256 与登记值不一致 | 确认新版本后重新上传或重建 |
| `source_unreadable` | 文件存在但无法读取或解析 | 检查格式、权限和文件完整性 |
| `vector_dimension_mismatch` | 当前向量维度与集合配置不一致 | 统一 Embedding 配置后重建集合 |
| `index_rule_outdated` | 文档使用了旧索引规则 | 执行重建索引 |
| `index_failed` | 未归类的索引失败 | 查看服务日志和索引运行记录 |

错误详情只用于本地持久化和日志关联；健康接口、前端和 MCP 只返回错误码及脱敏后的通用提示。

## 4. Qdrant 健康统计

健康检查不会通过读取全部 Payload 来推断向量数量，而是读取目标 collection 的元数据，并使用按知识库、文档和当前索引代际过滤的精确 Count 请求。

- `vectorCountSource=actual`：Qdrant 已返回当前文档实际点数。
- `vectorCountSource=unknown`：collection 不存在、维度不匹配、鉴权失败、连接失败或 Count 失败；此时 `vectorCount=0` 只是占位，不能解释为零向量。
- `vectorCountSource=estimated`：兼容状态，表示估算值；新健康检查不使用估算值替代实际 Count。
- `vectorCountSource=not_applicable`：文档未索引或未启用 Qdrant。

健康结果还会返回 collection 是否存在、collection 点数、当前配置维度、collection 维度和 sparse 向量配置。Collection 点数与当前文档实际点数不一致时，通常意味着存在旧索引代际或残留点，应检查索引代际后再重建，不应直接把差值当作有效文档向量。

## 5. 索引运行生命周期

1. 上传或重建开始时创建运行上下文，记录 `trigger`、文档和开始时间。
2. 在写入 Qdrant 前校验原文存在性和 checksum；批量重建先完成全部原文预检。
3. 成功时记录 chunk 数、索引版本、完成时间，并更新文档 `indexRunId`。
4. 失败时记录稳定错误码；单文档重建保留原文元数据，避免错误被静默吞掉。
5. 运行记录按最新优先保存，超过 50 条时淘汰最旧记录。

当前版本的批量重建在首个索引失败时停止并记录失败运行；后续任务队列和逐文档重试属于下一阶段。

## 6. 迁移规则

- 缺失知识库 ID 时使用 `knowledgeBases` map key。
- 缺失 `createdAt` 时写入当前 UTC 时间，缺失 `updatedAt` 时回填 `createdAt`。
- 缺失 `currentIndexVersion` 时回填当前索引规则版本。
- 缺失文档 `source` 时标记为 `legacy`，`version` 默认为 1。
- 缺失文档 `indexErrorCode` 但存在旧错误文本时，按兼容规则分类并替换为脱敏消息。
- 不在启动迁移阶段读取所有原文计算 checksum；checksum 在文档索引或重建时补齐。

## 7. 对外接口

- `GET /api/knowledge-bases/:id/health`：健康摘要、文档诊断和最近运行记录。
- `GET /api/knowledge-bases/:id/index-history`：分页前的最近运行记录接口，当前返回最多 50 条。
- MCP `inspect_knowledge_base_quality`：返回安全化治理摘要，不返回原文路径、checksum 或详细内部错误。

后续扩展可以在不改变现有字段语义的前提下增加分页、标签筛选、文档版本对比和可恢复重试。
