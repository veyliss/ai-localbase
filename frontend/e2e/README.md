# 浏览器级核心流程

Playwright 测试只在显式提供本地环境变量时运行，默认不会读取文件、创建知识库或访问本地服务。

## 本地运行

### 独立渲染回归

这组测试只启动 Vite 并加载仓库内的固定 Markdown fixture，不访问后端、不创建知识库，也不读取本地上传文件：

```bash
npm run test:e2e:fixtures
```

它会在专用的 `4175` 端口启动独立 Vite fixture，避免占用或误复用 Docker 应用使用的 `4173` 端口。测试覆盖普通 Markdown 首屏、表格、代码、引用、Mermaid 流程图、思维导图、动态模块失败回退、多个图表复用和窄屏溢出。

1. 确保后端和前端已经启动，并确认浏览器可以访问前端地址。
2. 首次运行时安装 Chromium：

```bash
npx playwright install chromium
```

3. 使用仓库内公开 fixture 运行可重复的上传、索引、检索和证据详情流程：

```bash
E2E_BASE_URL=http://localhost:4173 \
E2E_PUBLIC_FIXTURE=1 \
npm run test:e2e
```

公开模式使用 `backend/eval/fixtures/public-v1/ai-tech-facts.md` 和固定的 Qdrant 事实问题，不需要指定本地文件，也不会读取用户文件。测试仍会创建临时知识库，并在每个用例结束后尝试删除。

公开模式还会验证无答案问题进入低置信路径，不把相关但未提供答案的页面当作确定证据。

4. 如需使用专门准备的本地合成文件和问题运行完整流程：

```bash
E2E_BASE_URL=http://localhost:4173 \
E2E_ALLOW_EXTERNAL_FILE=1 \
E2E_UPLOAD_FILE=/绝对路径/本地文件.txt \
E2E_QUERY='输入一个与本地文件内容相关的问题' \
npm run test:e2e
```

启用认证时提供 `E2E_PASSWORD`；测试会读取登录页返回的默认用户名，也可以用 `E2E_USERNAME` 显式覆盖。聊天引用流程还需要提供 `E2E_CHAT_QUERY`，并确保当前配置的聊天模型可用。公开 fixture 模式也可以额外提供 `E2E_CHAT_QUERY` 运行聊天引用用例。

测试会创建带有 `E2E` 前缀的临时知识库，并在每个用例结束后尝试删除。外部文件上传必须显式设置 `E2E_ALLOW_EXTERNAL_FILE=1`，请只使用专门准备的合成文件。**本地文件、问题、模型凭据和测试结果都不会写入仓库。**
