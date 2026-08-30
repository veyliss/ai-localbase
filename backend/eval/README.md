# RAG 评估框架 (Eval Framework)

## 概述

本模块为 ai-localbase 的离线 RAG 评估框架 Phase 1，提供：

- **数据集管理**：从 JSON 文件加载 Ground Truth 测试用例
- **评估指标**：Hit Rate、MRR、检索/生成时延 P50/P95，以及确定性的 Faithfulness/未支撑答案检测
- **核心评估器**：通过接口注入检索和生成函数，支持 mock 测试和受控并发
- **报告输出**：生成 JSON 和 Markdown 格式报告
- **CLI 入口**：命令行运行评估流程

> 真实评测数据、评估结果和本地上传文件只允许在本机使用。`backend/eval/results/`、`backend/data/` 以及 `backend/eval/data/` 中除公开样本外的文件均已加入 Git 忽略规则，禁止提交、推送或上传。当前允许公开提交的回归样本是 `backend/eval/data/ground_truth_v1.small.json`，目前包含 51 条审核通过样本；其中只包含公开合成事实和经过整理的官方技术文档短事实，不包含真实用户数据。

---

## 目录结构

```
backend/eval/
├── offline/
│   ├── dataset.go      # 数据集类型与加载
│   ├── metrics.go      # 指标类型与计算函数
│   └── evaluator.go    # 核心评估器
├── report/
│   └── report.go       # 报告生成器（JSON + Markdown）
├── cmd/
│   └── eval_main.go    # CLI 入口
├── data/
│   └── ground_truth_v1.small.json  # 公开回归数据集
├── fixtures/
│   └── public-v1/       # 公开、可重复上传的测试夹具
└── README.md
```

### 公开 fixture

`eval/fixtures/public-v1/` 提供与公开评测集配套的短 Markdown 夹具和 manifest，覆盖 Qdrant、Hugging Face Transformers、scikit-learn、PyTorch 与 TensorFlow 的公开技术事实。夹具不包含网页缓存、用户文件、知识库快照或评测运行结果，并且整个目录保持 Git 忽略，不随项目上传。`public_dataset_test.go` 在本机发现夹具时会校验样本、答案片段、来源映射和 Markdown 锚点是否一致；在干净 checkout 中会明确跳过这项本地 fixture 校验，公开 JSON 数据集的校验仍然默认执行。

---

## 数据集格式

数据集为 JSON 数组，每个元素为一个 `GroundTruthCase`：

```json
[
  {
    "id": "case-001",
    "question": "什么是向量数据库？",
    "answer": "向量数据库是专门存储和检索向量数据的数据库系统。",
    "answer_snippets": ["向量数据库", "存储和检索向量数据"],
    "source_documents": [
      {
        "knowledge_base_id": "kb-001",
        "document_id": "doc-001",
        "chunk_id": "chunk-001"
      }
    ],
    "answer_type": "extractive",
    "difficulty": "easy"
  }
]
```

字段说明：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `id` | string | 是 | 用例唯一 ID |
| `question` | string | 是 | 测试问题 |
| `answer` | string | 是 | 参考答案 |
| `answer_snippets` | []string | 否 | 答案关键片段（用于命中匹配） |
| `source_documents` | []SourceDocument | 否 | 期望检索到的文档来源 |
| `answer_type` | string | 否 | 答案类型：extractive/abstractive/yesno/numeric |
| `difficulty` | string | 否 | 难度：easy/medium/hard |
| `review_status` | string | 否 | 审核状态：pending/approved |
| `disabled` | bool | 否 | 是否暂不参与评估 |

---

## 评估指标

| 指标 | 说明 |
|------|------|
| **Answerable Cases** | 可回答样本数；Hit Rate、MRR 和证据命中率的统计基数 |
| **No-answer Cases** | 明确标注为 `no_answer`/`unanswerable`/`unknown` 的无答案样本数 |
| **No-answer Accuracy** | 无答案样本正确进入拒答或低置信路径的比例 |
| **Hit Rate** | 命中率，检索结果中包含正确答案片段的用例比例 |
| **Document Hit Rate** | 命中文档的用例比例；仅代表范围命中，不代表证据准确 |
| **Chunk Hit Rate** | 命中标准答案指定 Chunk 的用例比例 |
| **Answer Snippet Hit Rate（召回层）** | 检索片段包含答案片段的用例比例，不代表生成答案正确 |
| **Direct Evidence Hit Rate（召回层）** | 命中指定 Chunk 或答案片段的用例比例，不代表生成答案正确 |
| **Faithfulness（陈述级）** | 生成答案中能在检索片段中找到支撑的陈述比例；使用确定性词法基线，不调用额外模型 |
| **Hallucination Rate（答案级）** | 包含至少一条未被检索证据支撑陈述的答案比例；只在有可评估陈述的答案中统计 |
| **Unsupported Claim Rate（陈述级）** | 未被检索证据支撑的陈述占全部可评估陈述的比例，因此可能与答案级指标不同 |
| **MRR** | Mean Reciprocal Rank，首个命中结果的排名倒数均值 |
| **Retrieval Latency P50/P95** | 检索时延的第 50/95 百分位数 |
| **Generation Latency P50/P95** | LLM 生成时延的第 50/95 百分位数 |

命中判断逻辑（`IsHit`）：
1. `source_documents` 提供 `chunk_id` 时，必须同时匹配知识库（若结果带有）、文档和 Chunk，避免同一文档中的无关片段被误判为命中。
2. `source_documents` 只有文档信息、没有 `chunk_id` 时，退回文档级匹配，兼容历史评估集。
3. 没有 `source_documents` 时，使用 `answer_snippets` 做文本包含匹配。
4. 没有 `source_documents` 时，`answer_snippets` 使用归一化文本和二元片段覆盖率判断，`EvaluatorConfig.HitThreshold` 会实际参与命中计算；完整包含仍视为精确命中。

Faithfulness 检测会按句号、问号、感叹号和分号拆分答案，跳过明确的“无法确认/信息不足”拒答句；数字和 ASCII 标识必须在证据中保持一致，避免把“1898 年”与“1900 年”误判为同一事实。该指标是低成本回归信号，不能替代人工审核或模型辅助评审。

对于 `answer_type` 为 `no_answer`、`unanswerable` 或 `unknown` 的样本，评估器不会把“没有命中答案”当作普通召回失败，也不会将其计入 Hit Rate、MRR 和证据命中率的分母；报告会单独输出无答案样本数、正确数和正确率。若无答案样本返回了可命中的标注证据，仍会标记为 `no_answer_policy_miss`。

---

## 快速开始

### 编译

```bash
cd backend
go build ./eval/...
```

### 运行评估（mock 模式）

```bash
cd backend
go run ./eval/cmd/ \
  -dataset eval/data/ground_truth_v1.small.json \
  -output eval/results \
  -mock=true
```

### 从现有知识库生成评估集

开源版本支持直接从已上传并索引的知识库文档生成小型 Ground Truth 数据集。可以在前端知识库面板点击“评估集”下载 JSON，也可以通过 API 调用：

```bash
curl -X POST http://localhost:8080/api/eval/datasets/generate \
  -H 'Content-Type: application/json' \
  -d '{"knowledgeBaseId":"kb-xxx","maxPerDocument":5}'
```

响应中的 `items` 字段即为可直接保存到 `eval/data/*.json` 的数据集数组。

生成成功后，后端也会把本次评估集保存到应用状态中，响应会返回 `datasetId` 和 `createdAt`。可通过以下接口管理已保存的评估集：

```bash
# 列表
curl http://localhost:8080/api/eval/datasets

# 按知识库过滤
curl "http://localhost:8080/api/eval/datasets?knowledgeBaseId=kb-xxx"

# 查看详情
curl http://localhost:8080/api/eval/datasets/eval-xxx

# 删除
curl -X DELETE http://localhost:8080/api/eval/datasets/eval-xxx
```

检索调试台发现低置信结果后，也可以把候选样本加入待审核评估集。样本会以 `review_status=pending`、`disabled=true` 保存，后续需人工复核后再启用：

```bash
curl -X POST http://localhost:8080/api/eval/datasets/review-candidates \
  -H 'Content-Type: application/json' \
  -d '{
    "knowledgeBaseId":"kb-xxx",
    "documentId":"doc-xxx",
    "item":{
      "id":"debug-low-confidence-kb-xxx-001",
      "question":"示例问题",
      "answer":"候选答案",
      "answer_snippets":["候选证据片段"],
      "source_documents":[{"knowledge_base_id":"kb-xxx","document_id":"doc-xxx","chunk_id":"chunk-xxx"}],
      "answer_type":"retrieval-debug-candidate",
      "difficulty":"hard"
    }
  }'
```

待审核样本可以继续通过样本级 API 维护：

```bash
# 编辑样本、审核状态和启用状态
curl -X PUT http://localhost:8080/api/eval/datasets/eval-xxx/items/case-xxx \
  -H 'Content-Type: application/json' \
  -d '{
    "item":{
      "id":"case-xxx",
      "question":"修订后的问题",
      "answer":"修订后的答案",
      "answer_snippets":["修订后的证据片段"],
      "source_documents":[{"knowledge_base_id":"kb-xxx","document_id":"doc-xxx","chunk_id":"chunk-xxx"}],
      "answer_type":"extractive",
      "difficulty":"medium",
      "review_status":"approved",
      "disabled":false
    }
  }'

# 删除单条样本
curl -X DELETE http://localhost:8080/api/eval/datasets/eval-xxx/items/case-xxx
```

已保存的评估集可以直接从 Web API 触发一次检索评估运行。默认只运行已启用样本，并返回 Hit Rate、MRR、检索时延、低置信数量和逐条命中结果：

```bash
curl -X POST http://localhost:8080/api/eval/datasets/eval-xxx/runs \
  -H 'Content-Type: application/json' \
  -d '{"includeDisabled":false,"topK":12}'
```

如果要对比检索策略，可以通过 `searchMode` 指定运行模式：

```bash
# 自动模式：使用当前服务配置
curl -X POST http://localhost:8080/api/eval/datasets/eval-xxx/runs \
  -H 'Content-Type: application/json' \
  -d '{"includeDisabled":false,"topK":12,"searchMode":"auto"}'

# 强制向量检索
curl -X POST http://localhost:8080/api/eval/datasets/eval-xxx/runs \
  -H 'Content-Type: application/json' \
  -d '{"includeDisabled":false,"topK":12,"searchMode":"dense"}'

# 强制混合检索
curl -X POST http://localhost:8080/api/eval/datasets/eval-xxx/runs \
  -H 'Content-Type: application/json' \
  -d '{"includeDisabled":false,"topK":12,"searchMode":"hybrid"}'
```

运行结果会保存为质量趋势历史，可按知识库或评估集查询：

```bash
# 查看某个知识库的评估运行历史
curl "http://localhost:8080/api/eval/runs?knowledgeBaseId=kb-xxx"

# 查看某个评估集的运行历史
curl "http://localhost:8080/api/eval/runs?datasetId=eval-xxx"
```

### 运行评估（真实模式）

```bash
cd backend
go run ./eval/cmd/ \
  -dataset eval/data/ground_truth_v1.small.json \
  -output eval/results \
  -mock=false \
  -run-prefix baseline \
  -run-label phase1-baseline
```

### 本地运行四组策略矩阵

仓库提供了只负责编排命令的本地脚本。数据集路径和报告目录由调用者提供，脚本不会把它们复制到仓库：

```bash
cd backend
./eval/run_strategy_matrix.sh /绝对路径/到/本地审核数据集.json eval/results/strategy-matrix
```

可选环境变量：`EVAL_KB_ID`、`EVAL_EMBEDDING_BASE_URL`、`EVAL_CHAT_BASE_URL`、`EVAL_PATH_MAP`、`EVAL_CONCURRENCY`。四组报告生成后，使用推荐命令按质量、证据、错误数和 P95 延迟门槛选择默认策略：

```bash
cd backend
go run ./eval/cmd/recommend_strategy \
  -baseline eval/results/strategy-matrix/matrix_YYYYMMDD-HHMMSS_dense-keyword-no-rewrite.json \
  -candidate hybrid=eval/results/strategy-matrix/matrix_YYYYMMDD-HHMMSS_hybrid-keyword-no-rewrite.json \
  -candidate semantic=eval/results/strategy-matrix/matrix_YYYYMMDD-HHMMSS_hybrid-semantic-no-rewrite.json \
  -candidate advanced=eval/results/strategy-matrix/matrix_YYYYMMDD-HHMMSS_hybrid-semantic-rewrite.json
```

推荐器默认要求 Hit Rate、MRR、Faithfulness、直接证据命中率不下降，未支撑答案/陈述率和错误数不增加，检索与生成 P95 不超过 baseline 的 130%。没有候选通过时继续使用 baseline，不会自动修改生产配置。

宿主机直接运行根目录的 baseline 包装脚本时，也可以通过环境变量覆盖容器内地址。尤其是 Docker 后端的 app-state 可能保存了 `http://host.docker.internal:11434`，该地址适用于容器，不一定能被宿主机上的 Go 评测进程访问：

```bash
EVAL_EMBEDDING_BASE_URL=http://localhost:11434 \
EVAL_CHAT_BASE_URL=http://localhost:11434 \
./scripts/run-rag-baseline.sh \
  eval/data/ground_truth_v1.small.json \
  kb-xxx \
  eval/results/local-baseline
```

其中 `EVAL_EMBEDDING_BASE_URL` 用于查询向量化；启用查询改写或真实 LLM 生成时，再设置 `EVAL_CHAT_BASE_URL`。两个变量只覆盖本次评测请求，不会修改 `.env`、app-state、知识库文档或索引。`EVAL_FIXTURE_MANIFEST` 可写项目根目录相对路径、backend 相对路径或绝对路径。脚本仍会默认启用 fixture 预检：如果公开 fixture 与当前知识库内容、索引状态或 Qdrant 点不一致，会在评测开始前停止，避免把连接问题、旧索引和数据缺失误读为模型准确率。

### Qdrant 索引迁移

`migrate_qdrant_vectors` 用于把旧 collection 中的文本 payload 重新生成 dense + sparse 向量并写入新 collection。迁移会复用原 point ID 和 payload，支持 dry-run、分批处理、失败重试以及迁移后的 ID/文本校验；命令输出仅包含统计和错误码，不输出文档正文。

先执行只读扫描。`source-prefix` 必须提供，`target-prefix` 默认读取 `QDRANT_COLLECTION_PREFIX`：

```bash
cd backend
go run ./eval/cmd/migrate_qdrant_vectors \
  -source-prefix legacy_ \
  -target-prefix qdrant_ \
  -kb kb-xxx \
  -dry-run
```

确认扫描结果后执行迁移。建议先在一个知识库上验证，再扩大到全部知识库：

```bash
cd backend
go run ./eval/cmd/migrate_qdrant_vectors \
  -source-prefix legacy_ \
  -target-prefix qdrant_ \
  -kb kb-xxx \
  -batch-size 100 \
  -max-attempts 3 \
  -retry-backoff 500ms \
  -timeout 30m \
  -validate=true
```

关键参数和行为：

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-source-prefix` | 空（必填） | 源 Qdrant collection 前缀 |
| `-target-prefix` | `QDRANT_COLLECTION_PREFIX` | 目标 collection 前缀，必须与源不同 |
| `-kb` | 空 | 只迁移指定知识库；为空时遍历状态文件中的全部知识库 |
| `-dry-run` | `false` | 只扫描源 payload；不需要目标 Qdrant、Embedding，也不写入数据 |
| `-batch-size` | `100` | 每批 Embedding 和 upsert 的 point 数 |
| `-max-attempts` | `3` | collection、Embedding、upsert 的最大尝试次数 |
| `-retry-backoff` | `200ms` | 重试初始退避时间，每次失败后倍增 |
| `-validate` | `true` | 写入后校验 point ID 和 text payload |
| `-timeout` | `30m` | 全部知识库迁移的总超时时间 |
| `-continue-on-error` | `false` | 一个知识库失败后是否继续处理其他知识库 |

命令按知识库输出一条 JSON 统计，最后输出汇总 JSON。成功退出码为 `0`；存在迁移失败、校验失败或指定知识库不存在时退出码为 `1`。迁移前应备份 Qdrant 和应用状态；迁移完成后仍需通过索引健康页或索引校验接口抽查全文快照、chunk 数量和证据定位。

### 索引快照校验

索引完成后，文档详情和检索不应依赖已经被清理的原始文件。可以调用以下接口检查持久化快照、chunk 数量、结构化表格和证据定位：

```bash
curl http://localhost:8080/api/knowledge-bases/kb-xxx/documents/doc-xxx/index-verification
```

响应中的 `valid` 表示当前文档是否通过校验；`issues` 是稳定的问题码，例如 `indexed_content_snapshot_missing`、`chunk_count_mismatch`、`evidence_location_missing` 和 `index_version_outdated`。接口不会返回本地文件路径或文档正文。知识库管理页的“索引健康”视图可以按文档触发同一校验，并显示结果。

如需直接覆盖评估时使用的检索参数，可追加：

```bash
cd backend
go run ./eval/cmd/ \
  -dataset eval/data/ground_truth_v1.small.json \
  -output eval/results \
  -mock=false \
  -eval-kb-id kb-1 \
  -retrieval-topk-document 6 \
  -retrieval-candidate-topk-document 12 \
  -retrieval-topk-kb 10 \
  -retrieval-candidate-topk-all-docs 32 \
  -retrieval-max-chunks-per-document 2 \
  -retrieval-max-context-chars 2400 \
  -retrieval-auto-expand false \
  -retrieval-search-mode hybrid \
  -retrieval-rerank-strategy keyword \
  -retrieval-query-rewrite false \
  -eval-fixture-manifest eval/fixtures/public-v1/manifest.json \
  -run-prefix baseline \
  -run-label dense-only
```

### 参数说明

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-dataset` | `eval/data/ground_truth_v1.small.json` | 数据集文件路径 |
| `-output` | `eval/results` | 报告输出目录；不存在时自动创建 |
| `-hit-threshold` | `0.5` | 文本命中匹配阈值 |
| `-mock` | `true` | 是否使用 mock 检索/生成函数 |
| `-real-llm` | `false` | 真实模式下是否调用真实 LLM 生成答案 |
| `-run-prefix` | mock 为 `eval`，真实模式为 `baseline` | 报告文件名前缀 |
| `-run-label` | 空 | 报告标签，会追加到文件名末尾 |
| `-eval-kb-id` | 空 | 真实模式下覆盖评估知识库 ID |
| `-retrieval-topk-document` | `-1` | 覆盖文档范围 finalTopK；`-1` 表示沿用环境变量或默认配置 |
| `-retrieval-candidate-topk-document` | `-1` | 覆盖文档范围 candidateTopK |
| `-retrieval-topk-kb` | `-1` | 覆盖知识库范围 finalTopK |
| `-retrieval-candidate-topk-all-docs` | `-1` | 覆盖知识库范围 candidateTopK |
| `-retrieval-max-chunks-per-document` | `-1` | 覆盖每文档最大 chunk 数 |
| `-retrieval-max-context-chars` | `-1` | 覆盖上下文最大字符数 |
| `-retrieval-auto-expand` | 空 | 覆盖自动扩召回开关，支持 `true/false` |
| `-retrieval-search-mode` | 空 | 按请求覆盖检索模式，支持 `auto/dense/hybrid` |
| `-retrieval-rerank-strategy` | 空 | 按请求覆盖重排策略，支持 `keyword/semantic` |
| `-retrieval-query-rewrite` | 空 | 按请求覆盖查询改写开关，支持 `true/false` |
| `-retrieval-query-rewrite-max-variants` | `-1` | 按请求覆盖查询改写最大变体数 |
| `-eval-embedding-base-url` | 空 | 覆盖评估请求使用的 Embedding Base URL，适合宿主机与 Docker 地址不一致时使用 |
| `-eval-chat-base-url` | 空 | 覆盖评估请求使用的 Chat Base URL，适合 Query Rewrite 或真实 LLM 评估 |
| `-eval-path-map` | 空 | 临时映射 app-state 中的文档路径，例如 Docker dev 中宿主机运行可用 `/app=.` |
| `-eval-fixture-manifest` | `auto` | 校验 fixture 版本、SHA-256、索引状态、Qdrant 点数量和答案覆盖；`auto` 仅对 public-v1 数据集自动发现，`none` 关闭 |
| `-eval-allow-missing-sources` | `false` | 允许 `source_documents` 引用当前 app-state 中不存在的知识库或文档；只建议兼容旧数据时使用 |
| `-eval-allow-fixture-mismatch` | `false` | 允许 fixture 与当前索引不一致并继续运行；只用于诊断，报告结果不可信 |
| `-eval-concurrency` | `1` | 评估用例并发数；默认串行，真实模式建议从 2 开始逐步验证服务承载能力 |

真实模式会在运行前校验数据集的 `source_documents` 是否仍存在于当前 app-state。启用 fixture manifest 时，还会校验 fixture 文件与当前上传文档的 SHA-256、文档索引状态、Qdrant 点数量以及评测答案是否实际进入索引。任一检查失败时默认直接停止，避免把过期索引或缺失数据误判为检索召回下降；只有诊断历史状态时才使用 `-eval-allow-fixture-mismatch`，此时结果必须视为不可信。

fixture 校验只读取本地公开夹具和已启动服务，不会上传文件、修改用户文档或写入 Git。公开夹具目录被 `.gitignore` 忽略，运行结果也只保存在本地 `eval/results/`。

### 当前版本 Baseline 策略矩阵

当前版本建议固定复跑以下四组策略，避免被应用状态中的默认检索配置影响：

```bash
cd backend

# A. dense + keyword + rewrite off
env STATE_FILE=data/app-state.json UPLOAD_DIR=data/uploads QDRANT_URL=http://localhost:6333 \
go run ./eval/cmd/ -dataset eval/data/ground_truth_kb30_v3.json -output eval/results/current-baseline \
  -mock=false -eval-kb-id kb-30 -retrieval-search-mode dense -retrieval-rerank-strategy keyword -retrieval-query-rewrite false \
  -eval-embedding-base-url http://localhost:11434 -eval-path-map /app=. \
  -run-prefix current -run-label dense-keyword-no-rewrite

# B. hybrid + keyword + rewrite off
env STATE_FILE=data/app-state.json UPLOAD_DIR=data/uploads QDRANT_URL=http://localhost:6333 \
go run ./eval/cmd/ -dataset eval/data/ground_truth_kb30_v3.json -output eval/results/current-baseline \
  -mock=false -eval-kb-id kb-30 -retrieval-search-mode hybrid -retrieval-rerank-strategy keyword -retrieval-query-rewrite false \
  -eval-embedding-base-url http://localhost:11434 -eval-path-map /app=. \
  -run-prefix current -run-label hybrid-keyword-no-rewrite

# C. hybrid + semantic + rewrite off
env STATE_FILE=data/app-state.json UPLOAD_DIR=data/uploads QDRANT_URL=http://localhost:6333 \
go run ./eval/cmd/ -dataset eval/data/ground_truth_kb30_v3.json -output eval/results/current-baseline \
  -mock=false -eval-kb-id kb-30 -retrieval-search-mode hybrid -retrieval-rerank-strategy semantic -retrieval-query-rewrite false \
  -eval-embedding-base-url http://localhost:11434 -eval-path-map /app=. \
  -run-prefix current -run-label hybrid-semantic-no-rewrite

# D. hybrid + semantic + rewrite on
env STATE_FILE=data/app-state.json UPLOAD_DIR=data/uploads QDRANT_URL=http://localhost:6333 \
go run ./eval/cmd/ -dataset eval/data/ground_truth_kb30_v3.json -output eval/results/current-baseline \
  -mock=false -eval-kb-id kb-30 -retrieval-search-mode hybrid -retrieval-rerank-strategy semantic -retrieval-query-rewrite true -retrieval-query-rewrite-max-variants 3 \
  -eval-embedding-base-url http://localhost:11434 -eval-chat-base-url http://localhost:11434 -eval-path-map /app=. \
  -run-prefix current -run-label hybrid-semantic-rewrite
```

### 输出文件

运行后在 `eval/results/` 目录生成：

- mock 模式默认：`eval_<timestamp>.json` / `eval_<timestamp>.md`
- 真实模式默认：`baseline_<timestamp>.json` / `baseline_<timestamp>.md`
- 若传入 `-run-label phase1-baseline`，文件名示例：`baseline_<timestamp>_phase1-baseline.json`

### 阶段 1 推荐执行流程

1. 先使用 [`backend/eval/cmd/reindex_kb/main.go`](backend/eval/cmd/reindex_kb/main.go) 为目标知识库重建索引。
2. 使用真实模式跑一份 baseline 报告，并固定 `-run-prefix` 与 `-run-label`。
3. 调整环境变量或命令行覆盖参数后，再跑一份对比报告。
4. 将生成的 `.json` 与 `.md` 报告归档到 `eval/results/`。

### 检索参数配置入口

评估真实模式默认复用 [`backend/internal/config/config.go`](backend/internal/config/config.go:11) 中的服务配置，当前可通过环境变量调整：

- `RETRIEVAL_TOPK_DOCUMENT`
- `RETRIEVAL_CANDIDATE_TOPK_DOCUMENT`
- `RETRIEVAL_TOPK_KNOWLEDGE_BASE`
- `RETRIEVAL_CANDIDATE_TOPK_ALL_DOCS`
- `RETRIEVAL_MAX_CHUNKS_PER_DOCUMENT`
- `RETRIEVAL_MAX_CONTEXT_CHARS`
- `RETRIEVAL_ENABLE_AUTO_EXPAND`
- `EVAL_KNOWLEDGE_BASE_ID`

---

## 接入真实 RAG 服务

`Evaluator` 通过 `RetrievalFunc` 和 `GenerationFunc` 两个函数类型注入依赖，解耦评估逻辑与具体实现：

```go
type RetrievalFunc func(ctx context.Context, question string) (chunks []RetrievedChunkInfo, latency time.Duration, err error)
type GenerationFunc func(ctx context.Context, question string, chunks []RetrievedChunkInfo) (answer string, latency time.Duration, err error)
```

当前 [`backend/eval/cmd/eval_main.go`](backend/eval/cmd/eval_main.go:27) 已可直接切换 mock/真实模式，并支持在评估运行时覆盖知识库与检索参数配置，无需手改源码。

---

## 当前完成度与后续

- Phase 1：公开样本校验、评估报告、策略矩阵和并发评估工具已完成。
- Phase 2：真实 `AppService` 检索、证据门控、引用校准和无答案样本评估已接入。
- Phase 3：索引快照、Qdrant 分批迁移、重试、校验和知识库健康诊断已完成。
- 当前约束：正式策略结论仍应使用本地审核后的 active-source 数据；真实评估结果、上传文件、原文和 Qdrant 数据不提交。
- 后续方向：扩展公开评测集、完善失败分类、继续拆分大文件，并根据实际 baseline 决定默认检索策略。
