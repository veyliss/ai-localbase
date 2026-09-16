import { describe, expect, it } from 'vitest'
import {
  extractErrorMessage,
  normalizeConversation,
  normalizeKnowledgeBase,
  serializeConversation,
} from './api'
import type { BackendConversation, BackendKnowledgeBase } from './api'

describe('normalizeConversation', () => {
  it('filters only legacy operational assistant messages', () => {
    const conversation: BackendConversation = {
      id: 'conversation-1',
      title: '测试会话',
      knowledgeBaseId: '',
      documentId: '',
      createdAt: '2026-07-27T00:00:00Z',
      updatedAt: '2026-07-27T00:00:03Z',
      messages: [
        {
          id: 'legacy-welcome',
          role: 'assistant',
          content:
            '你好，我是 AI LocalBase 助手。你可以先选择知识库，或者进一步选中某个文档后再提问。',
          createdAt: '2026-07-27T00:00:00Z',
        },
        {
          id: 'legacy-degraded',
          role: 'assistant',
          content: '⚠️ AI 模型调用已降级\n\n模型超时',
          createdAt: '2026-07-27T00:00:01Z',
        },
        {
          id: 'real-answer',
          role: 'assistant',
          content: '文档介绍了系统的降级设计，但当前回答来自模型。',
          createdAt: '2026-07-27T00:00:02Z',
        },
        {
          id: 'user-message',
          role: 'user',
          content: '继续说明。',
          createdAt: '2026-07-27T00:00:03Z',
        },
      ],
    }

    const normalized = normalizeConversation(conversation)
    expect(normalized.scopeVersion).toBe(0)
    expect(normalized.messages.map((message) => message.id)).toEqual([
      'real-answer',
      'user-message',
    ])
  })

  it('removes legacy tool-only citation sources from stored conversations', () => {
    const conversation: BackendConversation = {
      id: 'conversation-2',
      title: '历史会话',
      knowledgeBaseId: 'kb-1',
      documentId: '',
      createdAt: '2026-07-27T00:00:00Z',
      updatedAt: '2026-07-27T00:00:01Z',
      messages: [{
        id: 'answer-1',
        role: 'assistant',
        content: '模型回答',
        createdAt: '2026-07-27T00:00:01Z',
        metadata: {
          sources: [{ toolName: 'search_knowledge_base' }],
        },
      }],
    }

    expect(normalizeConversation(conversation).messages[0].metadata).toBeUndefined()
  })

  it('preserves the conversation knowledge scope when normalizing and saving', () => {
    const backendConversation: BackendConversation = {
      id: 'conversation-scoped',
      title: '知识库会话',
      knowledgeBaseId: 'kb-school',
      documentId: 'doc-school',
      knowledgeScope: 'selected',
      scopeVersion: 1,
      createdAt: '2026-07-27T00:00:00Z',
      updatedAt: '2026-07-27T00:00:01Z',
      messages: [{
        id: 'message-1',
        role: 'user',
        content: '详细介绍',
        createdAt: '2026-07-27T00:00:01Z',
      }],
    }

    const conversation = normalizeConversation(backendConversation)
    expect(conversation.knowledgeBaseId).toBe('kb-school')
    expect(conversation.documentId).toBe('doc-school')
    expect(conversation.knowledgeScope).toBe('selected')
    expect(conversation.scopeVersion).toBe(1)
    expect(serializeConversation(conversation)).toMatchObject({
      knowledgeBaseId: 'kb-school',
      documentId: 'doc-school',
      knowledgeScope: 'selected',
    })
  })
})

describe('extractErrorMessage', () => {
  it('returns a readable message for an nginx 413 html response', async () => {
    const response = new Response('<html>Request Entity Too Large</html>', {
      status: 413,
      statusText: 'Request Entity Too Large',
    })

    await expect(extractErrorMessage(response)).resolves.toBe(
      '文档超过服务器允许的上传大小，请减小文件后重试',
    )
  })

  it('preserves the backend upload limit message', async () => {
    const response = new Response(JSON.stringify({
      error: 'uploaded file is too large, max size is 25.0 MiB',
    }), {
      status: 413,
      headers: { 'Content-Type': 'application/json' },
    })

    await expect(extractErrorMessage(response)).resolves.toBe(
      'uploaded file is too large, max size is 25.0 MiB',
    )
  })
})

describe('normalizeKnowledgeBase', () => {
  it('keeps governance metadata while tolerating legacy payloads', () => {
    const backendKnowledgeBase: BackendKnowledgeBase = {
      id: 'kb-1',
      name: '产品文档',
      description: '内部资料',
      tags: ['产品', '内部'],
      documents: [],
      createdAt: '2026-08-22T00:00:00Z',
      updatedAt: '2026-08-22T00:01:00Z',
      currentIndexVersion: 2,
    }

    expect(normalizeKnowledgeBase(backendKnowledgeBase)).toMatchObject({
      tags: ['产品', '内部'],
      updatedAt: '2026-08-22T00:01:00Z',
      currentIndexVersion: 2,
    })
    expect(normalizeKnowledgeBase({ ...backendKnowledgeBase, tags: undefined }).tags).toEqual([])
  })
})
