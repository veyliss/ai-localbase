import { describe, expect, it } from 'vitest'
import {
  createDefaultAppConfig,
  defaultRetrievalConfig,
  normalizeAppConfig,
} from './appConfig'

describe('app config', () => {
  it('creates an independent default configuration', () => {
    const first = createDefaultAppConfig()
    const second = createDefaultAppConfig()

    first.retrieval.topKDocument = 20

    expect(second.retrieval.topKDocument).toBe(defaultRetrievalConfig.topKDocument)
    expect(first.chat.provider).toBe('ollama')
    expect(first.embedding.model).toBe('nomic-embed-text')
  })

  it('normalizes retrieval bounds and unsupported enum values', () => {
    const fallback = createDefaultAppConfig()
    const normalized = normalizeAppConfig({
      ...fallback,
      retrieval: {
        ...fallback.retrieval,
        defaultSearchMode: 'unsupported' as 'dense',
        rerankStrategy: 'unsupported' as 'keyword',
        topKDocument: 999,
        candidateTopKDocument: 1,
        topKKnowledgeBase: 0,
        candidateTopKAllDocs: 1,
        maxContextChars: 100,
      },
    }, fallback)

    expect(normalized.retrieval.defaultSearchMode).toBe('dense')
    expect(normalized.retrieval.rerankStrategy).toBe('keyword')
    expect(normalized.retrieval.topKDocument).toBe(30)
    expect(normalized.retrieval.candidateTopKDocument).toBe(30)
    expect(normalized.retrieval.topKKnowledgeBase).toBe(1)
    expect(normalized.retrieval.candidateTopKAllDocs).toBe(1)
    expect(normalized.retrieval.maxContextChars).toBe(800)
  })

  it('keeps configured secrets represented without returning secret values from a blank payload', () => {
    const fallback = createDefaultAppConfig()
    fallback.mcp.token = 'server-token'

    const normalized = normalizeAppConfig({
      ...fallback,
      chat: {
        ...fallback.chat,
        apiKey: '',
        apiKeyConfigured: true,
      },
      embedding: {
        ...fallback.embedding,
        apiKey: '',
        apiKeyConfigured: true,
      },
      mcp: {
        ...fallback.mcp,
        token: '',
        tokenConfigured: true,
      },
    }, fallback)

    expect(normalized.chat.apiKey).toBe('')
    expect(normalized.chat.apiKeyConfigured).toBe(true)
    expect(normalized.embedding.apiKey).toBe('')
    expect(normalized.embedding.apiKeyConfigured).toBe(true)
    expect(normalized.mcp.token).toBe('server-token')
    expect(normalized.mcp.tokenConfigured).toBe(true)
  })
})
