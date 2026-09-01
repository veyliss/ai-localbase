import type { ChatMessageMetadata, ChatSourceMetadata } from '../../App'

const citationFields: Array<keyof ChatSourceMetadata> = [
  'knowledgeBaseId',
  'documentId',
  'documentName',
  'chunkId',
]

export const isDocumentCitationSource = (source: ChatSourceMetadata) => (
  citationFields.every((field) => String(source[field] ?? '').trim().length > 0) &&
  Boolean(String(source.snippet || source.citationSnippet || '').trim())
)

export const filterDocumentCitationSources = (sources?: ChatSourceMetadata[]) =>
  (sources ?? []).filter(isDocumentCitationSource)

export const filterCitationMetadata = (metadata?: ChatMessageMetadata) => {
  if (!metadata) return undefined

  const { sources: rawSources, ...rest } = metadata
  const sources = filterDocumentCitationSources(rawSources)
  const normalized = sources.length > 0 ? { ...rest, sources } : rest
  return Object.keys(normalized).length > 0 ? normalized : undefined
}
