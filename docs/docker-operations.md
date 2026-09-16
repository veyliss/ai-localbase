# Docker 运行与故障恢复

本文说明推荐的 Docker 启动方式、容器重启边界和外部依赖故障判断。项目默认按单机、单后端实例运行。

## 访问地址

- Docker 应用入口：`http://localhost:4173`
- 后端 API：`http://localhost:8080`
- Qdrant HTTP：`http://localhost:6333`
- 单独运行 Vite 开发服务器：`http://localhost:3000`

Docker 前端通过同源 Nginx 代理访问后端，浏览器通常只需要访问 4173。单独运行 Vite 时才会使用 3000；这两种模式不要混用端口说明。

## 启动与重启

首次启动或修改镜像、Compose 环境变量后，重新创建受影响的容器：

```bash
docker compose -f docker-compose.prod.yml up -d
docker compose -f docker-compose.prod.yml ps
```

只重启进程、不重新构建镜像时：

```bash
docker compose -f docker-compose.prod.yml restart backend
docker compose -f docker-compose.prod.yml restart frontend
```

修改 Dockerfile、前端代码构建产物或本地后端源码后，使用对应编排重新构建：

```bash
docker compose up -d --build
```

不要使用 `docker compose down -v` 做普通重启；这会删除命名 volume，可能导致 Qdrant 数据丢失。升级、迁移和灾难恢复前请先阅读 [`backup-restore.md`](backup-restore.md)。

## 健康状态

按以下顺序判断故障：

1. `GET /livez` 只表示后端进程仍在运行，不检查 Qdrant 或模型。
2. `GET /readyz` 返回 200 才表示 Qdrant、应用存储和上传暂存可用；启用 MCP 时还会检查 MCP Job Store。
3. `/readyz` 不主动调用 Chat 或 Embedding 模型，避免 Docker 健康检查反复消耗模型资源。
4. 登录后打开 Settings 的健康诊断，检查 Chat、Embedding 和 Qdrant 的真实连通性。

```bash
curl -i http://localhost:8080/livez
curl -i http://localhost:8080/readyz
docker compose ps
```

后端容器持续运行但处于 `unhealthy` 时，优先查看 `/readyz` 和日志。Qdrant 恢复后，后端会在下一次探测中恢复就绪，通常不需要删除容器或数据。

## 外部依赖故障

### Qdrant

- 查看 `docker compose logs qdrant --tail 100`，确认容器没有因为非回环绑定缺少 `QDRANT_API_KEY` 而退出。
- `QDRANT_URL` 在 Compose 网络内应指向 `http://qdrant:6333`；后端连接受保护的 Qdrant 时，`QDRANT_API_KEY` 必须与服务端一致。
- 修改 Qdrant 地址、API Key 或绑定地址后，重新创建后端和 Qdrant 容器，不要只刷新浏览器。
- 如果出现向量维度错误，确认 `QDRANT_VECTOR_SIZE` 与 Embedding 模型一致，并按 [`backup-restore.md`](backup-restore.md) 的说明使用新的 collection 前缀或重建索引。

### Ollama 或 OpenAI 兼容模型

- 模型不可用不会阻止后端进程启动；`/readyz` 只检查模型配置，不执行推理。
- 登录 Settings 的健康诊断，或从后端容器检查模型服务地址：

```bash
docker compose -f docker-compose.prod.yml exec backend curl -i http://host.docker.internal:11434/api/tags
```

开发编排可在宿主机直接访问 Ollama，或在开发容器内使用可用的 HTTP 客户端检查同一地址。

- Docker Desktop 通常使用 `OLLAMA_BASE_URL=http://host.docker.internal:11434`。Linux 环境需要把 Ollama 绑定到可被容器访问的地址，并按实际网络配置 `OLLAMA_BASE_URL`。
- 只有在模型服务地址或密钥变化后才需要重启后端；单纯模型服务短暂不可用时，先恢复外部服务再重试请求。

### 上传 413

Docker 前端 Nginx 的 `NGINX_CLIENT_MAX_BODY_SIZE` 必须大于 `MAX_UPLOAD_BYTES`，还要为 multipart 额外开销预留空间。修改后重新创建 frontend 容器；不存在外层 Nginx 时，不需要额外配置反向代理。

## 日志与数据

```bash
docker compose logs backend --tail 100
docker compose logs frontend --tail 100
docker compose logs qdrant --tail 100
```

应用状态、聊天记录、MCP Job Store、上传文件和索引快照都在后端数据目录；Qdrant 数据在 `QDRANT_STORAGE_PATH` 或生产命名 volume 中。不要通过删除容器排查问题，先保留日志并确认备份可用。
