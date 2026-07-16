import { describe, expect, it } from 'vitest'
import { DOCUMENT_SCOPE_RESULT_LIMIT, getDocumentScopeMatches } from './documentScopeOptions'

const createDocuments = (count: number) => Array.from({ length: count }, (_, index) => ({
  id: `doc-${index}`,
  name: `资料-${String(index).padStart(5, '0')}.pdf`,
}))

describe('getDocumentScopeMatches', () => {
  it('limits the rendered result set for very large knowledge bases', () => {
    const result = getDocumentScopeMatches(createDocuments(10_000), '', null)

    expect(result.total).toBe(10_000)
    expect(result.visible).toHaveLength(DOCUMENT_SCOPE_RESULT_LIMIT)
  })

  it('keeps the selected document visible when it is outside the first result window', () => {
    const result = getDocumentScopeMatches(createDocuments(1_000), '', 'doc-999')

    expect(result.visible[0]?.id).toBe('doc-999')
    expect(result.visible).toHaveLength(DOCUMENT_SCOPE_RESULT_LIMIT)
  })

  it('prioritizes exact and prefix filename matches', () => {
    const documents = [
      { id: 'contains', name: '归档-武汉大学.pdf' },
      { id: 'prefix', name: '武汉大学介绍.pdf' },
      { id: 'exact', name: '武汉大学' },
    ]

    const result = getDocumentScopeMatches(documents, '武汉大学', null)

    expect(result.visible.map((document) => document.id)).toEqual(['exact', 'prefix', 'contains'])
  })

  it('does not keep a selected document that does not match the active query', () => {
    const documents = [
      { id: 'selected', name: '财务数据.xlsx' },
      { id: 'match', name: '武汉大学简介.pdf' },
    ]

    const result = getDocumentScopeMatches(documents, '武汉大学', 'selected')

    expect(result.visible.map((document) => document.id)).toEqual(['match'])
  })
})
