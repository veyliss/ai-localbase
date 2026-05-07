# RAG 评估框架 (Eval Framework)

## 概述

本模块为 ai-localbase 的离线 RAG 评估框架 Phase 1，提供：

- **数据集管理**：从 JSON 文件加载 Ground Truth 测试用例
- **评估指标**：Hit Rate、MRR、检索/生成时延 P50/P95
- **核心评估器**：通过接口注入检索和生成函数，支持 mock 测试
- **报告输出**：生成 JSON 和 Markdown 格式报告
- **CLI 入口**：命令行运行评估流程

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
│   └── ground_truth_v1.small.json  # 示例数据集
└── README.md
```

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

---

## 评估指标

| 指标 | 说明 |
|------|------|
| **Hit Rate** | 命中率，检索结果中包含正确答案片段的用例比例 |
| **MRR** | Mean Reciprocal Rank，首个命中结果的排名倒数均值 |
| **Retrieval Latency P50/P95** | 检索时延的第 50/95 百分位数 |
| **Generation Latency P50/P95** | LLM 生成时延的第 50/95 百分位数 |

命中判断逻辑（`HitEval`）：
1. 优先匹配 `ChunkID`（来自 `source_documents`）
2. 若无 ChunkID 标注，则用 `answer_snippets` 进行文本包含匹配
3. 文本相似度阈值由 `EvaluatorConfig.HitThreshold` 控制（默认 0.5）

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

## 扩展计划

- Phase 2：接入真实 `AppService` 进行端到端评估
- Phase 3：支持并发评估（`MaxConcurrency > 1`）
- Phase 4：添加 Precision@K、Recall@K 等更多指标
