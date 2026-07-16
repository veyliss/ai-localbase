export const DOCUMENT_SCOPE_RESULT_LIMIT = 100

export interface DocumentScopeOption {
  id: string
  name: string
}

interface DocumentScopeMatches<T> {
  total: number
  visible: T[]
}

const matchRank = (name: string, query: string) => {
  const normalizedName = name.toLocaleLowerCase('zh-CN')
  if (normalizedName === query) return 0
  if (normalizedName.startsWith(query)) return 1
  return 2
}

export const getDocumentScopeMatches = <T extends DocumentScopeOption>(
  documents: T[],
  query: string,
  selectedDocumentId: string | null,
  limit = DOCUMENT_SCOPE_RESULT_LIMIT,
): DocumentScopeMatches<T> => {
  const normalizedQuery = query.trim().toLocaleLowerCase('zh-CN')
  const matches = normalizedQuery
    ? documents
      .filter((document) => document.name.toLocaleLowerCase('zh-CN').includes(normalizedQuery))
      .sort((left, right) => {
        const rankDifference = matchRank(left.name, normalizedQuery) - matchRank(right.name, normalizedQuery)
        return rankDifference || left.name.localeCompare(right.name, 'zh-CN')
      })
    : documents

  const visible = matches.slice(0, limit)
  if (!normalizedQuery && selectedDocumentId && !visible.some((document) => document.id === selectedDocumentId)) {
    const selectedDocument = matches.find((document) => document.id === selectedDocumentId)
    if (selectedDocument && limit > 0) {
      visible.splice(Math.max(limit - 1, 0), 1)
      visible.unshift(selectedDocument)
    }
  }

  return {
    total: matches.length,
    visible,
  }
}
