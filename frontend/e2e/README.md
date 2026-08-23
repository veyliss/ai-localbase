# 浏览器级核心流程

Playwright 测试只在显式提供本地环境变量时运行，默认不会读取文件、创建知识库或访问本地服务。

## 本地运行

1. 确保后端和前端已经启动，并确认浏览器可以访问前端地址。
2. 首次运行时安装 Chromium：

```bash
npx playwright install chromium
```

3. 使用本地文件和问题运行上传、索引、检索调试、文档详情流程：

```bash
E2E_BASE_URL=http://localhost:4173 \
E2E_ALLOW_EXTERNAL_FILE=1 \
E2E_UPLOAD_FILE=/绝对路径/本地文件.txt \
E2E_QUERY='输入一个与本地文件内容相关的问题' \
npm run test:e2e
```

启用认证时再提供 `E2E_USERNAME` 和 `E2E_PASSWORD`。聊天引用流程还需要提供 `E2E_CHAT_QUERY`，并确保当前配置的聊天模型可用。

测试会创建带有 `E2E` 前缀的临时知识库，并在每个用例结束后尝试删除。外部文件上传必须显式设置 `E2E_ALLOW_EXTERNAL_FILE=1`，请只使用专门准备的合成文件。**本地文件、问题、模型凭据和测试结果都不会写入仓库。**
