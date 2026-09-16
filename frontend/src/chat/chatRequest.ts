import type {
  ChatConfig,
  ChatMessage,
  EmbeddingConfig,
  KnowledgeScope,
  RetrievalConfig,
} from '../App'

export interface ChatRequestBody {
  conversationId: string
  model: string
  think: boolean
  knowledgeBaseId: string
  documentId: string
  knowledgeScope: KnowledgeScope
  retrievalMode: RetrievalConfig['defaultSearchMode']
  config: ChatConfig
  embedding: EmbeddingConfig
  messages: Array<{
    role: ChatMessage['role']
    content: string
  }>
}

export const buildChatRequestBody = (input: {
  conversationId: string
  model: string
  think: boolean
  knowledgeBaseId: string
  documentId: string
  knowledgeScope: KnowledgeScope
  retrievalMode: RetrievalConfig['defaultSearchMode']
  config: ChatConfig
  embedding: EmbeddingConfig
  messages: ChatMessage[]
}): ChatRequestBody => ({
  conversationId: input.conversationId,
  model: input.model,
  think: input.think,
  knowledgeBaseId: input.knowledgeBaseId,
  documentId: input.documentId,
  knowledgeScope: input.knowledgeScope,
  retrievalMode: input.retrievalMode,
  config: input.config,
  embedding: input.embedding,
  messages: input.messages.map((message) => ({
    role: message.role,
    content: message.content,
  })),
})
