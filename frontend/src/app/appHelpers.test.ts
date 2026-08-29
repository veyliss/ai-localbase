import { describe, expect, it } from 'vitest'
import { createEmptyConversation, normalizeChatMetadata } from './appHelpers'

describe('app helpers', () => {
  it('creates a local conversation with a stable initial shape', () => {
    const conversation = createEmptyConversation('kb-1', 'doc-1')

    expect(conversation.id).toBeTruthy()
    expect(conversation.knowledgeBaseId).toBe('kb-1')
    expect(conversation.documentId).toBe('doc-1')
    expect(conversation.localOnly).toBe(true)
  })

  it('keeps citation support metadata while filtering incomplete sources', () => {
    const metadata = normalizeChatMetadata({
      citationSupport: {
        status: 'partial',
        summary: '部分支撑',
        claimCount: 2,
        supportedClaimCount: 1,
        coverage: 0.5,
      },
      sources: [
        {
          knowledgeBaseId: 'kb-1',
          documentId: 'doc-1',
          documentName: '说明.md',
          chunkId: 'chunk-1',
          snippet: '成立于 1893 年。',
        },
        {
          knowledgeBaseId: 'kb-1',
          documentId: 'doc-1',
          documentName: '不完整来源',
          chunkId: 'chunk-2',
        },
      ],
    })

    expect(metadata?.citationSupport?.status).toBe('partial')
    expect(metadata?.sources).toHaveLength(1)
    expect(metadata?.sources?.[0].chunkId).toBe('chunk-1')
  })
})
