# Docker 镜像自动构建与部署指南

## 概述

该项目使用 GitHub Actions 自动构建 Docker 镜像并推送到 GitHub Container Registry (GHCR)，使社区用户可以直接拉取预构建的镜像，无需本地构建。

## 工作流程

### 自动构建触发

GitHub Actions 工作流在以下情况自动触发：

- **推送到主分支** (`main`)：构建镜像并标记为 `latest`
- **推送到开发分支** (`develop`)：构建镜像并标记为分支名
- **创建版本标签** (e.g., `v1.0.0`)：构建镜像并标记为版本号

### 镜像标签规则

| 推送情况 | Backend 镜像标签 | Frontend 镜像标签 |
|---------|-------------|-------------|
| 推送到 `main` | `ghcr.io/veyliss/ai-localbase-backend:latest` | `ghcr.io/veyliss/ai-localbase-frontend:latest` |
| 推送到 `develop` | `ghcr.io/veyliss/ai-localbase-backend:develop` | `ghcr.io/veyliss/ai-localbase-frontend:develop` |
| 创建标签 `v1.2.3` | `ghcr.io/veyliss/ai-localbase-backend:v1.2.3` | `ghcr.io/veyliss/ai-localbase-frontend:v1.2.3` |
| Commit SHA | `ghcr.io/veyliss/ai-localbase-backend:main-<sha>` | `ghcr.io/veyliss/ai-localbase-frontend:main-<sha>` |

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
docker compose -f docker-compose.prod.yml up -d

# 查看应用
# 前端: http://localhost:4173
# 后端 API: http://localhost:8080
```

### 环境变量配置

创建 `.env` 文件配置：

```bash
# Ollama 地址 (macOS Docker Desktop 用户)
OLLAMA_BASE_URL=http://host.docker.internal:11434

# Qdrant 向量维度 (根据嵌入模型调整)
QDRANT_VECTOR_SIZE=768

# Qdrant API 密钥 (可选)
QDRANT_API_KEY=
```

然后启动：

```bash
docker compose -f docker-compose.prod.yml --env-file .env up -d
```

### 验证连接

```bash
# 检查后端健康状态
curl http://localhost:8080/health

# 验证 Ollama 连接 (需要在后端容器内)
docker compose -f docker-compose.prod.yml exec backend curl http://host.docker.internal:11434/v1/models
```

---

## 开发工作流 (维护者)

### 首次设置

GitHub Actions 工作流已配置，首次推送后会自动运行。

当前维护流程已拆分为两个独立工作流：

- [`Build and Push Docker Images`](.github/workflows/docker-build.yml)：负责构建并推送 GHCR 镜像
- [`Create GitHub Release`](.github/workflows/release.yml)：负责在推送版本标签时自动创建 GitHub Release

首次推送主分支时，通常会触发镜像构建；推送版本标签时，会同时触发镜像版本构建和 Release 创建：

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
git tag v1.0.0
git push origin v1.0.0
```

推送版本标签后，GitHub Actions 会自动执行两件事：

1. 构建并推送版本镜像：
   - `ghcr.io/veyliss/ai-localbase-backend:v1.0.0`
   - `ghcr.io/veyliss/ai-localbase-frontend:v1.0.0`
2. 在 GitHub Releases 页面自动创建对应版本发布

如果只想构建镜像、不创建 GitHub Release，请不要推送版本标签，而是直接推送到 [`main`](DOCKER_DEPLOY.md:13) 或 [`develop`](DOCKER_DEPLOY.md:14)。

### 本地测试预构建镜像

```bash
# 拉取最新镜像
docker compose -f docker-compose.prod.yml pull

# 启动
docker compose -f docker-compose.prod.yml up -d

# 测试
curl http://localhost:8080/health
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

---

## 相关文件

- `.github/workflows/docker-build.yml` - Docker 镜像构建与推送工作流
- `.github/workflows/release.yml` - GitHub Release 自动创建工作流
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
