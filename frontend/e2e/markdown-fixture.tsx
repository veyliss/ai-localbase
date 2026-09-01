import React from 'react'
import { createRoot } from 'react-dom/client'
import MarkdownRenderer from '../src/components/chat/MarkdownRenderer'
import '../src/index.css'
import '../src/App.css'

const fence = '```'

const fixtures: Record<string, string> = {
  basic: `## 基础渲染

这是一段普通 Markdown 内容，用于验证标题、段落和引用不会触发图表依赖。

> 这是一条可读的引用。

- 条目一
- 条目二

${fence}text
plain source block
${fence}

| 字段 | 值 |
| --- | --- |
| 类型 | fixture |
| 状态 | ready |`,
  flowchart: `## Mermaid 流程图

${fence}mermaid
flowchart TD
  A[开始] --> B[完成]
${fence}`,
  mindmap: `## Mermaid 思维导图

${fence}mermaid
mindmap
  root((知识库))
    文档
      证据
${fence}`,
  architecture: `## Mermaid 架构图

${fence}mermaid
architecture-beta
  group api(cloud)[API]
  service web(server)[Web] in api
  service db(database)[DB] in api
  web:R --> L:db
${fence}`,
  invalid: `## Mermaid 失败回退

${fence}mermaid
this is intentionally invalid mermaid syntax
${fence}`,
  multi: `## 多图表渲染

${fence}mermaid
flowchart TD
  A[一] --> B[二]
${fence}

${fence}mermaid
flowchart LR
  B[二] --> C[三]
${fence}`,
  'load-failure': `## 动态模块失败回退

${fence}mermaid
flowchart TD
  A[加载] --> B[失败回退]
${fence}`,
}

const query = new URLSearchParams(window.location.search).get('case') || 'basic'
const content = fixtures[query] || fixtures.basic

createRoot(document.getElementById('root')!).render(
  <main className="e2e-markdown-fixture">
    <div data-testid="fixture-ready" className="e2e-fixture-ready">
      {query}
    </div>
    <section className="message-content message-content-markdown">
      <MarkdownRenderer content={content} />
    </section>
  </main>,
)
