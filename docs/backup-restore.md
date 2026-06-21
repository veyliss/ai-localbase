# 备份与恢复清单

本文档用于自托管部署、升级前备份、迁移服务器或灾难恢复。当前版本只提供**运维清单**，不提供产品内导出/导入功能。

---

## 需要备份的内容

### 配置与密钥

- `.env`：包含端口、认证开关、Qdrant 配置、模型服务地址等运行配置。
- `AUTH_PASSWORD`、`AUTH_SETUP_TOKEN`、`AUTH_RESET_TOKEN`、`AUTH_RESET_PASSWORD`：只建议保存在部署环境或密码管理器中，不要提交到 Git。
- `QDRANT_API_KEY`：如果使用受保护的 Qdrant 实例，需要和 `.env` 一起保管。

### 应用数据

- `STATE_FILE`：默认本地为 `data/app-state.json`，Docker 中通常是 `/app/data/app-state.json`。
- `CHAT_HISTORY_FILE`：默认本地为 `data/chat-history.db`，Docker 中通常是 `/app/data/chat-history.db`。
- `UPLOAD_DIR`：默认本地为 `data/uploads`，Docker 中通常是 `/app/data/uploads`。
- Docker 默认挂载：`./backend/data:/app/data`，因此通常需要备份项目根目录下的 `backend/data`。

### Qdrant 数据

- `QDRANT_STORAGE_PATH`：dev/app compose 默认是 `./qdrant_storage`。
- `docker-compose.prod.yml`：默认使用命名 volume `qdrant_storage`。
- 托管 Qdrant：按服务商提供的快照、备份或 collection 导出能力备份。

---

## 建议备份顺序

1. 暂停写入，避免状态文件、SQLite 与 Qdrant 快照不一致。
2. 停止后端服务，或至少确保没有文档上传、索引、聊天写入正在进行。
3. 备份 `.env` 与部署编排文件。
4. 备份应用数据目录，例如 `backend/data`。
5. 备份 Qdrant 持久化目录或 volume。
6. 记录当前镜像版本、Git tag、Embedding 模型、`QDRANT_VECTOR_SIZE` 与 `QDRANT_COLLECTION_PREFIX`。

Docker Compose 示例：

```bash
docker compose stop backend frontend
```

如果需要完全一致的 Qdrant 数据快照，也可以在业务暂停后停止 Qdrant 再复制数据：

```bash
docker compose stop qdrant
```

---

## 建议恢复顺序

1. 恢复 `.env`，确认端口、认证、数据路径、Qdrant 配置和模型服务地址正确。
2. 恢复应用数据目录到 `UPLOAD_DIR`、`STATE_FILE`、`CHAT_HISTORY_FILE` 对应位置。
3. 恢复 Qdrant 数据到 `QDRANT_STORAGE_PATH` 或 Docker 命名 volume。
4. 确认文件权限允许后端容器读写 `STATE_FILE`、`CHAT_HISTORY_FILE` 和 `UPLOAD_DIR`。
5. 先启动 Qdrant，确认 `/health` 正常。
6. 再启动后端和前端。
7. 打开 Settings 和知识库页面，确认模型配置、知识库列表、文档列表和检索正常。

Docker Compose 示例：

```bash
docker compose up -d qdrant
docker compose up -d backend frontend
```

---

## 权限与安全注意事项

- `.env` 不应提交到仓库；真实密码、Token、API Key 应放在服务器环境或密码管理器里。
- `STATE_FILE` 会保存认证用户、session、API Key 哈希和知识库状态。API Key 明文只会在创建时展示一次，备份不会恢复明文 Token。
- `CHAT_HISTORY_FILE` 是 SQLite 文件，备份时最好暂停写入。
- `UPLOAD_DIR` 存放用户上传原始文件，可能包含敏感内容。
- Qdrant 数据包含文档向量和 payload，迁移时应按同等敏感级别处理。

---

## 向量与 Collection 风险

- `QDRANT_VECTOR_SIZE` 必须与 Embedding 模型输出维度一致。
- 恢复旧 Qdrant 数据时，`QDRANT_COLLECTION_PREFIX` 应与备份时保持一致。
- 如果切换了 Embedding 模型且向量维度变化，不要复用旧 collection；建议更换新的 `QDRANT_COLLECTION_PREFIX` 并重建索引。
- 如果开启 `ENABLE_HYBRID_SEARCH`，旧 collection 可能缺少 named dense/sparse vector 配置；建议使用新前缀并重新索引。
- 如果只恢复 `STATE_FILE` 但没有恢复 Qdrant 数据，知识库列表可能存在，但检索结果会缺失，需要重新索引文档。

---

## 快速核对清单

- `.env`
- `backend/data/app-state.json` 或自定义 `STATE_FILE`
- `backend/data/chat-history.db` 或自定义 `CHAT_HISTORY_FILE`
- `backend/data/uploads` 或自定义 `UPLOAD_DIR`
- `qdrant_storage` 目录或 Docker 命名 volume `qdrant_storage`
- 当前版本号、Embedding 模型、`QDRANT_VECTOR_SIZE`、`QDRANT_COLLECTION_PREFIX`
