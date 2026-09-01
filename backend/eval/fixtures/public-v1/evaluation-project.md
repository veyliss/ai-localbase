# 公开合成事实：评估项目

> 用途：本地公开评测回归。内容为脱敏合成事实，不包含用户数据。

### RAG 评估框架的 Phase 1 {#rag-evaluation-phase1}

RAG 评估框架的 Phase 1 包含数据层和指标层。

### Hit Rate 指标 {#hit-rate-definition}

Hit Rate 衡量检索结果中包含正确答案片段的用例比例。

### MRR 指标 {#mrr-definition}

MRR 使用首个命中结果排名的倒数计算平均值，数值越高表示正确结果通常排名越靠前。

### source_documents 的作用 {#source-documents-purpose}

记录 source_documents 是为了核对期望文档和 Chunk，避免仅凭摘要或相似度把无关片段误判为命中。

### 结构化查询的确定性计算 {#structured-query-deterministic}

最高、最低、平均等聚合查询需要读取完整行数据并执行确定性计算，单靠向量相似度不能保证数值正确。
