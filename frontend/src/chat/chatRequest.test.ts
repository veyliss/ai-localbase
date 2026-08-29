import { describe, expect, it } from 'vitest'
import { buildChatRequestBody } from './chatRequest'

describe('chat request boundary', () => {
  it('serializes only the message fields accepted by the API', () => {
    const request = buildChatRequestBody({
      conversationId: 'conversation-1',
      model: 'llama3.2',
      think: false,
      knowledgeBaseId: 'kb-1',
      documentId: '',
      retrievalMode: 'dense',
      config: {
        provider: 'ollama',
        baseUrl: 'http://localhost:11434/v1',
        model: 'llama3.2',
        apiKey: '',
        temperature: 0.7,
        knowledgeTemperature: 0.1,
        contextMessageLimit: 12,
      },
      embedding: {
        provider: 'ollama',
        baseUrl: 'http://localhost:11434/v1',
        model: 'nomic-embed-text',
        apiKey: '',
      },
      messages: [{
        id: 'message-1',
        role: 'user',
        content: '问题',
        timestamp: '2026-08-29T00:00:00Z',
        metadata: { degraded: false },
      }],
    })

    expect(request.messages).toEqual([{ role: 'user', content: '问题' }])
    expect(request.conversationId).toBe('conversation-1')
    expect(request.config.model).toBe('llama3.2')
  })
})
