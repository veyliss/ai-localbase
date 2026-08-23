import { describe, expect, it } from 'vitest'
import {
  filterCitationMetadata,
  filterDocumentCitationSources,
  isDocumentCitationSource,
} from './citationSources'

describe('citation source filtering', () => {
  const documentSource = {
    knowledgeBaseId: 'kb-1',
    documentId: 'doc-1',
    documentName: 'demo.md',
    chunkId: 'chunk-1',
    snippet: '示例文档片段',
  }

  it('keeps complete document chunk sources', () => {
    expect(isDocumentCitationSource(documentSource)).toBe(true)
  })

  it('drops tool records and sources without snippets', () => {
    expect(filterDocumentCitationSources([
      { toolName: 'search_knowledge_base' },
      { ...documentSource, snippet: '' },
    ])).toEqual([])
  })

  it('removes invalid historical sources without dropping other metadata', () => {
    expect(filterCitationMetadata({
      degraded: true,
      sources: [{ toolName: 'search_knowledge_base' }],
    })).toEqual({ degraded: true })
  })
})
