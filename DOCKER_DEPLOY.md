# Docker 镜像自动构建与部署指南

## 概述

该项目使用 GitHub Actions 自动构建 Docker 镜像并推送到 GitHub Container Registry (GHCR)，使社区用户可以直接拉取预构建的镜像，无需本地构建。

## 工作流程

### 自动构建触发

GitHub Actions 工作流在以下情况自动触发：

- **推送到主分支** (`main`)：构建镜像并标记为 `main` 与 `latest`
- **推送到开发分支** (`develop`)：构建镜像并标记为分支名
- **创建版本标签** (e.g., `v1.0.0`)：构建镜像并标记为版本号

### 镜像标签规则

| 推送情况 | Backend 镜像标签 | Frontend 镜像标签 |
|---------|-------------|-------------|
| 推送到 `main` | `ghcr.io/veyliss/ai-localbase-backend:main`、`:latest` | `ghcr.io/veyliss/ai-localbase-frontend:main`、`:latest` |
| 推送到 `develop` | `ghcr.io/veyliss/ai-localbase-backend:develop` | `ghcr.io/veyliss/ai-localbase-frontend:develop` |
| 创建标签 `v1.2.3` | `ghcr.io/veyliss/ai-localbase-backend:v1.2.3`、`:1.2.3`、`:1.2` | `ghcr.io/veyliss/ai-localbase-frontend:v1.2.3`、`:1.2.3`、`:1.2` |
| Commit SHA | `ghcr.io/veyliss/ai-localbase-backend:sha-<short>` | `ghcr.io/veyliss/ai-localbase-frontend:sha-<short>` |

---

## 快速开始 (用于其他用户)

### 前提条件

- Docker 和 Docker Compose
- Ollama 运行在本地 (macOS 用户使用 `host.docker.internal`)

### 使用预构建镜像启动应用

```bash
# 克隆仓库
git clone https://github.com/veyliss/ai-localbase.git
cd ai-localbase

# 使用生产环境 docker-compose (使用 GHCR 镜像)
# 请先在 .env 中确认 ENABLE_AUTH=true，并设置 AUTH_PASSWORD 或 AUTH_SETUP_TOKEN。
AI_LOCALBASE_IMAGE_TAG=v1.4.6 docker compose -f docker-compose.prod.yml up -d

# 查看应用
# 前端: http://localhost:4173
# 后端 API: http://localhost:8080（默认仅绑定本机）
```

### 环境变量配置

复制示例配置并按需修改：

```bash
cp .env.example .env
```

然后启动：

```bash
docker compose -f docker-compose.prod.yml up -d
```

关键项：

- `OLLAMA_BASE_URL`：Ollama 地址，macOS Docker Desktop 通常为 `http://host.docker.internal:11434`
- `QDRANT_VECTOR_SIZE`：嵌入模型向量维度，例如 `nomic-embed-text=768`，`bge-m3=1024`
- `QDRANT_COLLECTION_PREFIX`：Qdrant 集合名前缀，切换向量维度时可改前缀避免复用旧集合
- `ENABLE_HYBRID_SEARCH`：开启 dense + sparse 混合检索。开启前建议切换新的 `QDRANT_COLLECTION_PREFIX` 并重建索引，让 Qdrant collection 使用 named dense/sparse vectors。
- `QDRANT_API_KEY`：Qdrant API 密钥，可选
- `QDRANT_BIND_ADDRESS`：Qdrant 暴露端口绑定地址，默认 `127.0.0.1`；改为非回环地址时必须同时设置 `QDRANT_API_KEY`，否则容器拒绝启动
- `AI_LOCALBASE_IMAGE_TAG`：预构建镜像版本，生产环境建议使用具体 tag，例如 `v1.4.6`，不要依赖 `latest`
- 生产 Compose 默认使用已发布的固定 `v1.4.6` 镜像；升级或回滚时通过 `AI_LOCALBASE_IMAGE_TAG` 显式切换。本地源码修改请使用开发或本地构建编排验证。
- `BACKEND_BIND_ADDRESS`：后端端口绑定地址，默认 `127.0.0.1`；前端容器通过内部网络访问后端
- `TRUST_EXTERNAL_PROXY_HEADERS`：是否信任外层代理的 `X-Forwarded-Proto` / `X-Forwarded-Host`，默认 `false`；只有前端端口不直接暴露且前置代理受控时才设为 `true`
- `ENABLE_AUTH`：生产 Compose 在变量未提供时默认 `true`；使用 `.env.example` 时也必须显式确认其为 `true`
- `STAGING_DIR`：上传暂存目录，默认 `/app/data/staging`，必须与应用数据卷保持一致
- `MAX_UPLOAD_BYTES`：单文件上传大小上限，默认 `26214400`，即 25 MiB
- `MAX_JSON_BODY_BYTES`：登录、Chat、配置和 MCP 等非 multipart 请求体上限，默认 `4194304`，即 4 MiB
- `LOG_MAX_SIZE` / `LOG_MAX_FILE`：Docker JSON 日志轮转参数，默认每个日志文件 10 MiB、保留 3 个文件
- `QDRANT_MEMORY_LIMIT` / `QDRANT_CPU_LIMIT`：Qdrant 容器资源上限，默认 `1g` / `2.0`
- `BACKEND_MEMORY_LIMIT` / `BACKEND_CPU_LIMIT`：后端容器资源上限，默认 `1g` / `2.0`
- `FRONTEND_MEMORY_LIMIT` / `FRONTEND_CPU_LIMIT`：前端容器资源上限，默认 `256m` / `1.0`
- 后端业务进程以固定 UID `10001` 的非 root 用户运行。首次启动时会将 `/app/data` 的现有文件权限迁移到该用户，并写入权限迁移标记，以兼容升级前由 root 创建的数据目录且避免每次重启重复扫描。

应用层按单实例运行。SQLite 聊天记录、应用状态文件和内存中的 MCP Job 不支持多个后端副本共享写入，请勿使用 `docker compose scale backend=2`；如未来需要水平扩展，应先替换为共享状态存储和持久化 Job 队列。

### 验证连接

```bash
# 检查后端健康状态
curl http://localhost:8080/readyz

# 验证 Ollama 连接 (需要在后端容器内)
docker compose -f docker-compose.prod.yml exec backend curl http://host.docker.internal:11434/v1/models
```

---

## 开发工作流 (维护者)

### 首次设置

GitHub Actions 工作流已配置，首次推送后会自动运行。

当前维护流程由两个工作流组成：

- [`Quality Gates`](.github/workflows/quality.yml)：负责 PR 与主线质量检查
- [`Build and Push Docker Images`](.github/workflows/docker-build.yml)：负责 tag 质量检查、构建 GHCR 镜像，并在镜像成功后创建 GitHub Release

首次推送主分支时，通常会触发镜像构建；推送版本标签时，会按“质量检查 → 镜像构建 → Release 创建”顺序执行：

```bash
# 在本地构建并测试
docker compose up -d --build

# 推送到 GitHub
git add .
git commit -m "Initial commit with docker automation"
git push origin main
```

### 查看构建状态

1. 前往 [GitHub Actions](https://github.com/veyliss/ai-localbase/actions)
2. 选择 "Build and Push Docker Images" 工作流
3. 查看最新运行的构建日志

### 发布新版本

```bash
# 创建版本标签
git tag -a v1.0.0 -m "v1.0.0" -m "发布说明"
git push origin v1.0.0
```

推送版本标签后，GitHub Actions 会自动执行两件事：

1. 构建并推送版本镜像：
   - `ghcr.io/veyliss/ai-localbase-backend:v1.0.0`
   - `ghcr.io/veyliss/ai-localbase-backend:1.0.0`
   - `ghcr.io/veyliss/ai-localbase-backend:1.0`
   - `ghcr.io/veyliss/ai-localbase-frontend:v1.0.0`
   - `ghcr.io/veyliss/ai-localbase-frontend:1.0.0`
   - `ghcr.io/veyliss/ai-localbase-frontend:1.0`
2. 在 GitHub Releases 页面自动创建对应版本发布

发布 workflow 会在构建前校验 Docker metadata 输出，确保 `v` 前缀标签、无 `v` 版本标签和 `major.minor` 标签同时存在；镜像构建成功后再校验发布说明中的镜像引用与当前 Git tag 一致。

如果只想构建镜像、不创建 GitHub Release，请不要推送版本标签，而是直接推送到 [`main`](DOCKER_DEPLOY.md:13) 或 [`develop`](DOCKER_DEPLOY.md:14)。

### 本地测试预构建镜像

```bash
# 拉取指定版本镜像
export AI_LOCALBASE_IMAGE_TAG=v1.4.6
docker compose -f docker-compose.prod.yml pull

# 启动指定版本
AI_LOCALBASE_IMAGE_TAG=v1.4.6 docker compose -f docker-compose.prod.yml up -d

# 测试
curl http://localhost:8080/readyz
```

---

## GitHub Container Registry (GHCR) 说明

### 什么是 GHCR？

GitHub Container Registry 是 GitHub 提供的容器镜像托管服务，优势：

✅ **免费使用** - 无需额外账户  
✅ **原生集成** - 与 GitHub 仓库绑定  
✅ **自动 Actions** - 支持 GitHub Actions CI/CD  
✅ **访问控制** - 可设置为公开或私有  

### 镜像可见性

当前镜像配置为**公开**，任何人都可以拉取：

```bash
docker pull ghcr.io/veyliss/ai-localbase-backend:latest
docker pull ghcr.io/veyliss/ai-localbase-frontend:latest
```

---

## 常见问题

### Q: 如何改成私有镜像？

A: 在 GitHub 仓库设置 → Packages → 选择镜像 → 更改权限为私有

### Q: 构建失败了怎么办？

A: 检查 GitHub Actions 日志：
1. 前往 Actions 标签
2. 选择失败的工作流运行
3. 查看详细日志找出错误信息

### Q: 如何只构建后端或前端？

A: 修改 `.github/workflows/docker-build.yml`，注释掉不需要的构建步骤，或者创建分离的工作流文件。

### Q: 镜像构建需要多长时间？

A: 第一次构建约 5-10 分钟（取决于网络），后续构建利用缓存会更快。

### Q: 我可以在自己的仓库中使用这个工作流吗？

A: 可以，修改 `veyliss` 为你的 GitHub 用户名，以及对应的镜像名称。

---

## 故障排除

### 镜像拉取失败

```bash
# 检查镜像是否存在
docker manifest inspect ghcr.io/veyliss/ai-localbase-backend:latest

# 清除本地缓存后重试
docker rmi ghcr.io/veyliss/ai-localbase-backend:latest
docker pull ghcr.io/veyliss/ai-localbase-backend:latest
```

### 容器无法连接 Ollama

确保你的系统上：
1. Ollama 正在运行
2. macOS 用户使用 `host.docker.internal` 配置
3. 验证连接：`curl http://host.docker.internal:11434/health`

### Qdrant 向量维度不匹配

根据你使用的嵌入模型调整 `QDRANT_VECTOR_SIZE`：

```bash
# 例如使用 nomic-embed-text (768维)
docker compose -f docker-compose.prod.yml up -d

# 或自定义维度
QDRANT_VECTOR_SIZE=1024 docker compose -f docker-compose.prod.yml up -d
```

如果已经创建过知识库集合，Qdrant 不允许同一个集合混用不同向量维度。出现 `Vector dimension error: expected dim: 1024, got 768` 时，选择其中一种处理方式：

1. 将 `.env` 中的 `QDRANT_VECTOR_SIZE` 改回旧集合维度，并使用同维度 embedding 模型。
2. 修改 `.env` 中的 `QDRANT_COLLECTION_PREFIX`，让新索引写入新集合。
3. 确认不需要旧数据后，删除 `qdrant_storage` 或删除对应 Qdrant collection，再重新上传/重建索引。

---

## 相关文件

- `.github/workflows/docker-build.yml` - 质量检查、Docker 镜像构建与 GitHub Release 工作流
- `docker-compose.prod.yml` - 生产环境配置（使用 GHCR 镜像）
- `docker-compose.yml` - 开发环境配置（本地构建）
- `docker/backend.Dockerfile` - 后端镜像定义
- `docker/frontend.Dockerfile` - 前端镜像定义
- `docker/nginx.conf` - Nginx 反向代理配置

---

## 下一步

- 📖 查看 [TROUBLESHOOTING.md](./TROUBLESHOOTING.md) 解决常见问题
- 🔗 查看 [README.md](./README.md) 了解项目概况
- 🐳 了解更多 [GitHub Container Registry 文档](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)
