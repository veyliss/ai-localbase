import { filterDocumentCitationSources } from '../components/chat/citationSources'
import type {
  ChatMessageMetadata,
  ChatSourceMetadata,
  CitationSupportMetadata,
  Conversation,
} from '../App'

export const createId = () => {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID()
  }

  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}

export const normalizeChatMetadata = (
  metadata?: Partial<ChatMessageMetadata> & {
    sources?: ChatSourceMetadata[]
    citationSupport?: CitationSupportMetadata
  },
) => {
  if (!metadata) return undefined
  const normalized: ChatMessageMetadata = {}
  if (metadata.degraded !== undefined) normalized.degraded = metadata.degraded
  if (metadata.fallbackStrategy) normalized.fallbackStrategy = metadata.fallbackStrategy
  if (metadata.upstreamError) normalized.upstreamError = metadata.upstreamError
  if (metadata.citationSupport) normalized.citationSupport = metadata.citationSupport
  if (metadata.sources && metadata.sources.length > 0) {
    const sources = filterDocumentCitationSources(metadata.sources)
    if (sources.length > 0) normalized.sources = sources
  }
  return Object.keys(normalized).length > 0 ? normalized : undefined
}

export const createEmptyConversation = (knowledgeBaseId = '', documentId = ''): Conversation => {
  const now = new Date().toISOString()

  return {
    id: createId(),
    title: '新的对话',
    knowledgeBaseId,
    documentId,
    scopeVersion: 1,
    createdAt: now,
    updatedAt: now,
    messages: [],
    localOnly: true,
  }
}
