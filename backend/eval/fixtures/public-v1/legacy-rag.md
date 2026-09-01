# 公开合成事实：RAG 与 Qdrant

> 用途：本地公开评测回归。内容为脱敏合成事实，不包含用户数据。

### Qdrant 基础定义 {#qdrant-intro}

Qdrant 是一个开源的向量相似度搜索引擎，用于存储、搜索和管理向量嵌入。

Qdrant 的数据点通常由向量、点 ID 和可选的 payload 组成。

### Qdrant 安装方式 {#qdrant-install}

Qdrant 可以通过 Docker 镜像、二进制文件或源代码进行安装。最简单的方式是使用 Docker。

安装 Qdrant 最简单的方式是使用 Docker 镜像。

### ai-localbase 后端技术栈 {#ai-localbase-arch}

ai-localbase 项目主要使用 Go 语言和 Qdrant 作为后端技术。

ai-localbase 使用 Qdrant 存储和检索向量嵌入，以支持知识库检索。

### Qdrant 多租户与搜索 {#qdrant-features}

是的，Qdrant 通过 Collection 机制支持多租户。

是的，Qdrant 提供向量相似度搜索能力。
