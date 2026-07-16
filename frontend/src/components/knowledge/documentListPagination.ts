export const DOCUMENTS_PER_PAGE = 50

interface DocumentPage<T> {
  items: T[]
  page: number
  pageCount: number
}

export const getDocumentPage = <T>(
  documents: T[],
  requestedPage: number,
  pageSize = DOCUMENTS_PER_PAGE,
): DocumentPage<T> => {
  const pageCount = Math.max(1, Math.ceil(documents.length / pageSize))
  const page = Math.min(Math.max(1, requestedPage), pageCount)
  const start = (page - 1) * pageSize

  return {
    items: documents.slice(start, start + pageSize),
    page,
    pageCount,
  }
}
