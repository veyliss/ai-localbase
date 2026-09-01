import type {
  AppConfig,
  ChatConfig,
  EmbeddingConfig,
  MCPConfig,
  RetrievalConfig,
} from '../App'

export const defaultRetrievalConfig: RetrievalConfig = {
  defaultSearchMode: 'dense',
  hybridSearchEnabled: false,
  rerankStrategy: 'keyword',
  enableQueryRewrite: false,
  queryRewriteMaxVariants: 3,
  topKDocument: 6,
  candidateTopKDocument: 12,
  topKKnowledgeBase: 10,
  candidateTopKAllDocs: 32,
  maxChunksPerDocument: 2,
  maxContextChars: 2400,
  enableLowConfidenceBoost: false,
}

export const createDefaultAppConfig = (): AppConfig => ({
  chat: {
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
  mcp: {
    enabled: false,
    basePath: '/mcp',
    token: '',
  },
  retrieval: { ...defaultRetrievalConfig },
})

const clampNumber = (value: unknown, fallback: number, min: number, max: number) => {
  const parsed = Number(value)
  if (!Number.isFinite(parsed)) return fallback
  return Math.max(min, Math.min(max, Math.round(parsed)))
}

const normalizeSecretValue = (incomingValue: unknown) => (
  typeof incomingValue === 'string' ? incomingValue : ''
)

const normalizeSecretValueWithFallback = (
  incomingValue: unknown,
  configured: unknown,
  fallbackValue: string,
) => {
  const value = normalizeSecretValue(incomingValue)
  if (value) return value
  if (configured && fallbackValue) return fallbackValue
  return ''
}

export const normalizeAppConfig = (config: Partial<AppConfig>, fallback: AppConfig): AppConfig => {
  const retrieval = {
    ...fallback.retrieval,
    ...(config.retrieval ?? {}),
  }
  const topKDocument = clampNumber(retrieval.topKDocument, fallback.retrieval.topKDocument, 1, 30)
  const topKKnowledgeBase = clampNumber(retrieval.topKKnowledgeBase, fallback.retrieval.topKKnowledgeBase, 1, 40)

  const chatConfig: Partial<ChatConfig> = config.chat ?? {}
  const embeddingConfig: Partial<EmbeddingConfig> = config.embedding ?? {}
  const mcpConfig: Partial<MCPConfig> = config.mcp ?? {}
  const chatApiKey = normalizeSecretValue(chatConfig.apiKey)
  const embeddingApiKey = normalizeSecretValue(embeddingConfig.apiKey)
  const mcpToken = normalizeSecretValueWithFallback(
    mcpConfig.token,
    mcpConfig.tokenConfigured,
    fallback.mcp.token,
  )
  const knowledgeTemperature =
    typeof chatConfig.knowledgeTemperature === 'number' && Number.isFinite(chatConfig.knowledgeTemperature)
      ? Math.max(0.1, Math.min(0.5, chatConfig.knowledgeTemperature))
      : fallback.chat.knowledgeTemperature

  return {
    chat: {
      ...fallback.chat,
      ...chatConfig,
      apiKey: chatApiKey,
      apiKeyConfigured: Boolean(chatConfig.apiKeyConfigured || chatApiKey),
      clearApiKey: false,
      knowledgeTemperature,
    },
    embedding: {
      ...fallback.embedding,
      ...embeddingConfig,
      apiKey: embeddingApiKey,
      apiKeyConfigured: Boolean(embeddingConfig.apiKeyConfigured || embeddingApiKey),
      clearApiKey: false,
    },
    mcp: {
      ...fallback.mcp,
      ...mcpConfig,
      token: mcpToken,
      tokenConfigured: Boolean(mcpConfig.tokenConfigured || mcpToken),
      legacyTokenEnabled: Boolean(mcpConfig.legacyTokenEnabled),
    },
    retrieval: {
      defaultSearchMode: retrieval.defaultSearchMode === 'hybrid' ? 'hybrid' : 'dense',
      hybridSearchEnabled: Boolean(retrieval.hybridSearchEnabled),
      rerankStrategy: retrieval.rerankStrategy === 'semantic' ? 'semantic' : 'keyword',
      enableQueryRewrite: Boolean(retrieval.enableQueryRewrite),
      queryRewriteMaxVariants: clampNumber(
        retrieval.queryRewriteMaxVariants,
        fallback.retrieval.queryRewriteMaxVariants,
        1,
        5,
      ),
      topKDocument,
      candidateTopKDocument: clampNumber(
        retrieval.candidateTopKDocument,
        fallback.retrieval.candidateTopKDocument,
        topKDocument,
        80,
      ),
      topKKnowledgeBase,
      candidateTopKAllDocs: clampNumber(
        retrieval.candidateTopKAllDocs,
        fallback.retrieval.candidateTopKAllDocs,
        topKKnowledgeBase,
        120,
      ),
      maxChunksPerDocument: clampNumber(
        retrieval.maxChunksPerDocument,
        fallback.retrieval.maxChunksPerDocument,
        1,
        10,
      ),
      maxContextChars: clampNumber(
        retrieval.maxContextChars,
        fallback.retrieval.maxContextChars,
        800,
        20000,
      ),
      enableLowConfidenceBoost: Boolean(retrieval.enableLowConfidenceBoost),
    },
  }
}
