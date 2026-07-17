import './App.css'
import ChatArea from './components/ChatArea'
import Sidebar from './components/Sidebar'
import Login from './components/Login'
import KnowledgePanelWrapper from './components/knowledge/KnowledgePanelWrapper'
import SettingsPanel from './components/settings/SettingsPanel'
import { ToastProvider, useToast } from './components/common/Toast'
import LoadingBar from './components/common/LoadingBar'
import { AuthProvider, useAuth } from './contexts/AuthContext'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  API_BASE_PATH,
  addEvalDatasetCandidate,
  applyCSRFHeader,
  batchIndexDocuments,
  createKnowledgeBase,
  debugKnowledgeBaseRetrieval,
  deleteConversation,
  deleteEvalDataset,
  deleteEvalDatasetItem,
  deleteKnowledgeBase,
  deleteKnowledgeBaseDocument,
  deleteMessage,
  editMessage,
  extractErrorMessage,
  exportConversation,
  fetchKnowledgeBaseHealth,
  fetchKnowledgeBaseDocumentDetail,
  fetchBackendHealth,
  fetchConversationDetail,
  fetchInitialAppData,
  generateEvalDataset,
  getDocumentIndexStatus,
  getEvalDataset,
  listEvalDatasets,
  listEvalRuns,
  parseJsonResponse,
  reindexKnowledgeBaseDocument,
  regenerateMessage,
  resetMcpToken,
  runEvalDataset,
  saveConversation,
  stageUpload,
  updateAppConfig,
  updateEvalDatasetItem,
} from './services/api'
import type {
  DocumentDetailResponse,
  EvalDatasetDetail,
  EvalGroundTruthCase,
  EvalDatasetSummary,
  EvalRunSummary,
  GenerateEvalDatasetResponse,
  KnowledgeBaseHealthResponse,
  RetrievalDebugResponse,
  RetrievalSearchMode,
  RunEvalDatasetResponse,
  UpdateEvalDatasetItemResponse,
  DeleteEvalDatasetItemResponse,
  EvalRunOptions,
} from './services/api'

export interface ChatMessageMetadata {
  degraded?: boolean
  fallbackStrategy?: string
  upstreamError?: string
  sources?: ChatSourceMetadata[]
}

export interface ChatSourceMetadata {
  knowledgeBaseId?: string
  documentId?: string
  documentName?: string
  chunkId?: string
  chunkIndex?: string
  chunkKind?: string
  score?: string
  snippet?: string
  sourceType?: string
  toolName?: string
  citationConfidence?: string
}

export interface CitationNavigationTarget {
  knowledgeBaseId: string
  documentId: string
  chunkId?: string
}

export interface ChatMessage {
  id: string
  role: 'user' | 'assistant'
  content: string
  timestamp: string
  metadata?: ChatMessageMetadata
}

export interface Conversation {
  id: string
  title: string
  messages: ChatMessage[]
  createdAt: string
  updatedAt: string
}

export interface DocumentItem {
  id: string
  name: string
  size?: number
  sizeLabel: string
  uploadedAt: string
  status: 'indexed' | 'ready' | 'processing' | 'failed'
  contentPreview?: string
  chunkCount?: number
  indexedAt?: string
  indexError?: string
}

export interface KnowledgeBase {
  id: string
  name: string
  description: string
  documents: DocumentItem[]
  createdAt: string
}

export interface ChatConfig {
  provider: 'ollama' | 'openai-compatible'
  baseUrl: string
  model: string
  apiKey: string
  apiKeyConfigured?: boolean
  clearApiKey?: boolean
  temperature: number
  contextMessageLimit: number
}

export interface EmbeddingConfig {
  provider: 'ollama' | 'openai-compatible'
  baseUrl: string
  model: string
  apiKey: string
  apiKeyConfigured?: boolean
  clearApiKey?: boolean
}

export interface MCPConfig {
  enabled: boolean
  basePath: string
  token: string
  tokenConfigured?: boolean
  legacyTokenEnabled?: boolean
  deploymentWarnings?: string[]
  recommendedAuthMode?: string
  dangerConfirmationMode?: string
}

export interface RetrievalConfig {
  defaultSearchMode: 'dense' | 'hybrid'
  hybridSearchEnabled: boolean
  rerankStrategy: 'keyword' | 'semantic'
  enableQueryRewrite: boolean
  queryRewriteMaxVariants: number
  topKDocument: number
  candidateTopKDocument: number
  topKKnowledgeBase: number
  candidateTopKAllDocs: number
  maxChunksPerDocument: number
  maxContextChars: number
  enableLowConfidenceBoost: boolean
}

export interface AppConfig {
  chat: ChatConfig
  embedding: EmbeddingConfig
  mcp: MCPConfig
  retrieval: RetrievalConfig
}

export type ChatMode = 'fast' | 'think'
export type WorkspaceView = 'chat' | 'knowledge' | 'settings'

export interface ChatModeSettings {
  fastModel: string
  thinkModel: string
}

const THINK_MODEL_STORAGE_KEY = 'ai-localbase-think-model'
const FALLBACK_REQUEST_TIMEOUT_MS = 90_000
const STREAM_FIRST_CHUNK_TIMEOUT_MS = 30_000
const STREAM_REQUEST_TIMEOUT_MS = 180_000

const defaultRetrievalConfig: RetrievalConfig = {
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

interface ChatCompletionResponse {
  id: string
  object: string
  created: number
  model: string
  choices: Array<{
    index: number
    message: {
      role: 'assistant' | 'user'
      content: string
    }
  }>
  metadata?: {
    degraded?: boolean
    fallbackStrategy?: string
    upstreamError?: string
    sources?: ChatSourceMetadata[]
  }
}

interface ChatRequestBody {
  conversationId: string
  model: string
  knowledgeBaseId: string
  documentId: string
  retrievalMode: RetrievalConfig['defaultSearchMode']
  config: ChatConfig
  embedding: EmbeddingConfig
  messages: Array<{
    role: ChatMessage['role']
    content: string
  }>
}

interface StreamEventPayload {
  content?: string
  error?: string
  metadata?: ChatMessageMetadata
}

interface UploadQueueItem {
  file: File
  name: string
  path: string
}

interface DirectoryUploadIssueItem {
  name: string
  path: string
  reason: string
}

export type DirectoryUploadStatus =
  | 'idle'
  | 'scanning'
  | 'uploading'
  | 'indexing'
  | 'polling-index'
  | 'canceling'
  | 'canceled'
  | 'done'
  | 'partial-failed'
  | 'failed'

export interface DirectoryUploadTask {
  knowledgeBaseId: string | null
  status: DirectoryUploadStatus
  totalFiles: number
  eligibleFiles: number
  skippedFiles: number
  successFiles: number
  failedFiles: number
  pendingFiles: number
  processedFiles: number
  indexingFiles: number
  indexedFiles: number
  indexFailedFiles: number
  currentFileName: string
  currentFilePath: string
  failedItems: DirectoryUploadIssueItem[]
  skippedItems: DirectoryUploadIssueItem[]
  summaryMessage: string
}

const DIRECTORY_UPLOAD_ALLOWED_EXTENSIONS = new Set(['.txt', '.md', '.pdf', '.csv', '.xlsx'])

const createEmptyDirectoryUploadTask = (): DirectoryUploadTask => ({
  knowledgeBaseId: null,
  status: 'idle',
  totalFiles: 0,
  eligibleFiles: 0,
  skippedFiles: 0,
  successFiles: 0,
  failedFiles: 0,
  pendingFiles: 0,
  processedFiles: 0,
  indexingFiles: 0,
  indexedFiles: 0,
  indexFailedFiles: 0,
  currentFileName: '',
  currentFilePath: '',
  failedItems: [],
  skippedItems: [],
  summaryMessage: '',
})

const getUploadFilePath = (file: File) => {
  const relativePath = (file as File & { webkitRelativePath?: string }).webkitRelativePath
  return relativePath && relativePath.trim() ? relativePath : file.name
}

const getFileExtension = (fileName: string) => {
  const dotIndex = fileName.lastIndexOf('.')
  return dotIndex >= 0 ? fileName.slice(dotIndex).toLowerCase() : ''
}

const createId = () => {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID()
  }

  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}

const isDegradedFallbackContent = (content: string) =>
  /降级|fallback|无法完成流式|已切换|上游错误|模型服务暂不可用/.test(content)

const clampNumber = (value: unknown, fallback: number, min: number, max: number) => {
  const parsed = Number(value)
  if (!Number.isFinite(parsed)) return fallback
  return Math.max(min, Math.min(max, Math.round(parsed)))
}

const normalizeSecretValue = (
  incomingValue: unknown,
  configured: unknown,
  fallbackValue: string,
) => {
  const value = typeof incomingValue === 'string' ? incomingValue : ''
  if (value) {
    return value
  }
  if (configured && fallbackValue) {
    return fallbackValue
  }
  return ''
}

const normalizeAppConfig = (config: Partial<AppConfig>, fallback: AppConfig): AppConfig => {
  const retrieval = {
    ...fallback.retrieval,
    ...(config.retrieval ?? {}),
  }
  const topKDocument = clampNumber(retrieval.topKDocument, fallback.retrieval.topKDocument, 1, 30)
  const topKKnowledgeBase = clampNumber(retrieval.topKKnowledgeBase, fallback.retrieval.topKKnowledgeBase, 1, 40)

  const chatConfig: Partial<ChatConfig> = config.chat ?? {}
  const embeddingConfig: Partial<EmbeddingConfig> = config.embedding ?? {}
  const mcpConfig: Partial<MCPConfig> = config.mcp ?? {}
  const chatApiKey = normalizeSecretValue(
    chatConfig.apiKey,
    chatConfig.apiKeyConfigured,
    fallback.chat.apiKey,
  )
  const embeddingApiKey = normalizeSecretValue(
    embeddingConfig.apiKey,
    embeddingConfig.apiKeyConfigured,
    fallback.embedding.apiKey,
  )
  const mcpToken = normalizeSecretValue(
    mcpConfig.token,
    mcpConfig.tokenConfigured,
    fallback.mcp.token,
  )

  return {
    chat: {
      ...fallback.chat,
      ...chatConfig,
      apiKey: chatApiKey,
      apiKeyConfigured: Boolean(chatConfig.apiKeyConfigured || chatApiKey),
      clearApiKey: false,
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

const normalizeChatMetadata = (metadata?: ChatCompletionResponse['metadata'] | ChatMessageMetadata) => {
  if (!metadata) return undefined
  const normalized: ChatMessageMetadata = {}
  if (metadata.degraded !== undefined) normalized.degraded = metadata.degraded
  if (metadata.fallbackStrategy) normalized.fallbackStrategy = metadata.fallbackStrategy
  if (metadata.upstreamError) normalized.upstreamError = metadata.upstreamError
  if (metadata.sources && metadata.sources.length > 0) {
    normalized.sources = metadata.sources
  }
  return Object.keys(normalized).length > 0 ? normalized : undefined
}

const createWelcomeConversation = (): Conversation => {
  const now = new Date().toISOString()

  return {
    id: createId(),
    title: '新的对话',
    createdAt: now,
    updatedAt: now,
    messages: [
      {
        id: createId(),
        role: 'assistant',
        content:
          '你好，我是 AI LocalBase 助手。你可以先选择知识库，或者进一步选中某个文档后再提问。',
        timestamp: now,
      },
    ],
  }
}

const buildDirectoryUploadSummary = (task: DirectoryUploadTask) => {
  const parts = [
    `总文件 ${task.totalFiles}`,
    `可上传 ${task.eligibleFiles}`,
    `成功 ${task.successFiles}`,
    `失败 ${task.failedFiles}`,
    `跳过 ${task.skippedFiles}`,
  ]

  if (task.indexingFiles > 0 || task.indexedFiles > 0) {
    parts.push(`已索引 ${task.indexedFiles}`)
  }

  if (task.indexFailedFiles > 0) {
    parts.push(`索引失败 ${task.indexFailedFiles}`)
  }

  if (task.pendingFiles > 0) {
    parts.push(`未执行 ${task.pendingFiles}`)
  }

  return parts.join(' · ')
}

const sleep = (delayMs: number) =>
  new Promise((resolve) => {
    window.setTimeout(resolve, delayMs)
  })

function AppContent() {
  const { isAuthenticated, logout } = useAuth()
  const { showToast } = useToast()
  const [authCheckDone, setAuthCheckDone] = useState(false)
  const [authRequired, setAuthRequired] = useState(false)
  const [sidebarOpen, setSidebarOpen] = useState(() =>
    typeof window === 'undefined' ? true : window.innerWidth > 768,
  )
  const [knowledgeBases, setKnowledgeBases] = useState<KnowledgeBase[]>([])
  const [streamingConversationId, setStreamingConversationId] = useState<string | null>(null)
  const [backendReady, setBackendReady] = useState(false)
  const [backendWarmupRequired, setBackendWarmupRequired] = useState(true)
  const [authWarningsShown, setAuthWarningsShown] = useState(false)
  const [globalLoading] = useState(false)
  const [conversations, setConversations] = useState<Conversation[]>(() => {
    const initialConversation = createWelcomeConversation()
    return [initialConversation]
  })
  const [activeConversationId, setActiveConversationId] = useState<string | null>(null)
  const [selectedKnowledgeBaseId, setSelectedKnowledgeBaseId] = useState<string | null>(null)
  const [selectedDocumentId, setSelectedDocumentId] = useState<string | null>(null)
  const [activeWorkspace, setActiveWorkspace] = useState<WorkspaceView>('chat')
  const [collapsedKnowledgeBases, setCollapsedKnowledgeBases] = useState<Record<string, boolean>>({})
  const [citationNavigationTarget, setCitationNavigationTarget] =
    useState<CitationNavigationTarget | null>(null)
  const [directoryUploadTask, setDirectoryUploadTask] = useState<DirectoryUploadTask>(
    createEmptyDirectoryUploadTask,
  )
  const [directoryUploadPendingFiles, setDirectoryUploadPendingFiles] = useState<UploadQueueItem[]>([])
  const directoryUploadCancelRef = useRef(false)
  const chatAbortControllerRef = useRef<AbortController | null>(null)
  const activeChatRequestRef = useRef<{ requestId: string; conversationId: string } | null>(null)

  const waitForBackendReady = async (attempts = 12, delayMs = 1500) => {
    for (let index = 0; index < attempts; index += 1) {
      const health = await fetchBackendHealth()
      if ((health?.status ?? '').toLowerCase() === 'ok') {
        setBackendReady(true)
        setBackendWarmupRequired(true)
        return true
      }

      if (index < attempts - 1) {
        await sleep(delayMs)
      }
    }

    setBackendReady(false)
    return false
  }
  const [config, setConfig] = useState<AppConfig>(() => {
    const defaultConfig: AppConfig = {
      chat: {
        provider: 'ollama',
        baseUrl: 'http://localhost:11434/v1',
        model: 'llama3.2',
        apiKey: '',
        temperature: 0.7,
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
      retrieval: defaultRetrievalConfig,
    }

    if (typeof window === 'undefined') {
      return defaultConfig
    }

    return defaultConfig
  })

  const [chatMode, setChatMode] = useState<ChatMode>('fast')
  const [thinkModel, setThinkModel] = useState(() => {
    if (typeof window === 'undefined') {
      return 'deepseek-r1:8b'
    }
    return window.localStorage.getItem(THINK_MODEL_STORAGE_KEY)?.trim() || 'deepseek-r1:8b'
  })
  const chatModeSettings = useMemo<ChatModeSettings>(
    () => ({
      fastModel: config.chat.model,
      thinkModel,
    }),
    [config.chat.model, thinkModel],
  )

  const persistConfigToBackend = async (nextConfig: AppConfig) => {
    const savedConfig = normalizeAppConfig(await updateAppConfig(nextConfig), nextConfig)
    setConfig(savedConfig)
    setBackendReady(true)
    return savedConfig
  }

  useEffect(() => {
    const warnings = config.mcp.deploymentWarnings ?? []
    if (!isAuthenticated || warnings.length === 0 || authWarningsShown) {
      return
    }
    showToast('warning', warnings.join('；'), 5000)
    setAuthWarningsShown(true)
  }, [authWarningsShown, config.mcp.deploymentWarnings, isAuthenticated, showToast])

  const handleCopyMcpToken = async () => {
    if (!config.mcp.token || typeof navigator === 'undefined' || !navigator.clipboard) {
      throw new Error('mcp token is not available')
    }

    await navigator.clipboard.writeText(config.mcp.token)
  }

  const handleResetMcpToken = async () => {
    const mcp = await resetMcpToken()
    setConfig((prev) => ({
      ...prev,
      mcp,
    }))
    setBackendReady(true)
    setAuthWarningsShown(false)
  }

  const activeConversation = useMemo(
    () =>
      conversations.find((conversation) => conversation.id === activeConversationId) ??
      conversations[0],
    [activeConversationId, conversations],
  )

  const selectedKnowledgeBase = useMemo(() => {
    const fallbackKnowledgeBase = knowledgeBases[0] ?? null

    return (
      knowledgeBases.find(
        (knowledgeBase) => knowledgeBase.id === selectedKnowledgeBaseId,
      ) ?? fallbackKnowledgeBase
    )
  }, [knowledgeBases, selectedKnowledgeBaseId])

  const selectedDocument = useMemo(() => {
    if (!selectedKnowledgeBase || !selectedDocumentId) {
      return null
    }

    return (
      selectedKnowledgeBase.documents.find(
        (document) => document.id === selectedDocumentId,
      ) ?? null
    )
  }, [selectedDocumentId, selectedKnowledgeBase])

  useEffect(() => {
    if (!authCheckDone || (authRequired && !isAuthenticated)) {
      return
    }

    let canceled = false

    const bootstrapApp = async () => {
      while (!canceled) {
        try {
          const isReady = await waitForBackendReady()
          if (!isReady) {
            throw new Error('backend is not ready')
          }

          const initialData = await fetchInitialAppData()

          if (canceled) {
            return
          }

          const nextKnowledgeBases = initialData.knowledgeBases
          setKnowledgeBases(nextKnowledgeBases)
          setConfig((prev) => normalizeAppConfig(initialData.config, prev))
          setSelectedKnowledgeBaseId((current) => current ?? nextKnowledgeBases[0]?.id ?? null)
          setSelectedDocumentId(null)

          const conversationItems = initialData.conversations
          if (conversationItems.length > 0) {
            const firstConversationId = conversationItems[0].id
            const firstConversation = await fetchConversationDetail(firstConversationId)
            const restConversations = conversationItems.slice(1).map((conversation) => ({
              id: conversation.id,
              title: conversation.title,
              createdAt: conversation.createdAt,
              updatedAt: conversation.updatedAt,
              messages: [],
            }))

            if (canceled) {
              return
            }

            setConversations([firstConversation, ...restConversations])
            setActiveConversationId(firstConversation.id)
          }

          setBackendReady(true)
          return
        } catch (error) {
          if (canceled) {
            return
          }

          setBackendReady(false)
          console.warn('bootstrap app failed, retrying after backend warmup', error)
          await sleep(2000)
        }
      }
    }

    void bootstrapApp()

    return () => {
      canceled = true
    }
  }, [authCheckDone, authRequired, isAuthenticated])

  useEffect(() => {
    if (typeof window === 'undefined') {
      return
    }

    window.localStorage.removeItem('ai-localbase-config')
  }, [])

  const isOllamaSingleFlightMode =
    config.chat.provider === 'ollama' || config.embedding.provider === 'ollama'

  const generatingConversationTitle =
    conversations.find((conversation) => conversation.id === streamingConversationId)?.title ?? '当前会话'

  const persistConversation = async (conversation: Conversation) => {
    return saveConversation(conversation)
  }

  const replaceConversation = useCallback((updatedConversation: Conversation) => {
    setConversations((prev) => {
      const hasConversation = prev.some(
        (conversation) => conversation.id === updatedConversation.id,
      )
      if (!hasConversation) {
        return [updatedConversation, ...prev]
      }

      return prev.map((conversation) =>
        conversation.id === updatedConversation.id ? updatedConversation : conversation,
      )
    })
  }, [])

  const ensureNoActiveGeneration = (actionText: string) => {
    if (!streamingConversationId) {
      return true
    }

    window.alert(`当前正在生成「${generatingConversationTitle}」，请等待完成后再${actionText}。`)
    return false
  }

  const handleCreateConversation = async () => {
    const conversation = createWelcomeConversation()

    setConversations((prev) => [conversation, ...prev])
    setActiveConversationId(conversation.id)

    try {
      const savedConversation = await persistConversation(conversation)
      setConversations((prev) =>
        prev.map((item) => (item.id === conversation.id ? savedConversation : item)),
      )
    } catch (error) {
      const message =
        error instanceof Error ? error.message : '新建会话失败，请稍后重试。'
      window.alert(`新建会话失败：${message}`)
    }
  }

  const handleSelectConversation = async (conversationId: string) => {
    const existingConversation = conversations.find((conversation) => conversation.id === conversationId)
    if (existingConversation && existingConversation.messages.length > 0) {
      setActiveConversationId(conversationId)
      return
    }

    try {
      const loadedConversation = await fetchConversationDetail(conversationId)
      setConversations((prev) =>
        prev.map((conversation) =>
          conversation.id === conversationId ? loadedConversation : conversation,
        ),
      )
      setActiveConversationId(conversationId)
    } catch (error) {
      const message =
        error instanceof Error ? error.message : '加载会话失败，请稍后重试。'
      window.alert(`加载会话失败：${message}`)
    }
  }

  const handleRenameConversation = async (conversationId: string, title: string) => {
    const nextTitle = title.trim()
    if (!nextTitle) {
      return
    }

    const targetConversation = conversations.find((conversation) => conversation.id === conversationId)
    if (!targetConversation) {
      return
    }

    const isLocalOnly = targetConversation.messages.length > 0 && !targetConversation.messages.some((message) => message.role === 'user')

    if (isLocalOnly) {
      setConversations((prev) =>
        prev.map((conversation) =>
          conversation.id === conversationId
            ? {
                ...conversation,
                title: nextTitle,
                updatedAt: new Date().toISOString(),
              }
            : conversation,
        ),
      )
      return
    }

    try {
      const fullConversation =
        targetConversation.messages.length > 0
          ? targetConversation
          : await fetchConversationDetail(conversationId)

      const updatedConversation = await saveConversation(fullConversation, nextTitle)
      setConversations((prev) =>
        prev.map((conversation) =>
          conversation.id === conversationId
            ? conversation.messages.length > 0
              ? updatedConversation
              : { ...updatedConversation, messages: [] }
            : conversation,
        ),
      )
    } catch (error) {
      const message =
        error instanceof Error ? error.message : '重命名会话失败，请稍后重试。'
      window.alert(`重命名会话失败：${message}`)
    }
  }

  const handleDeleteConversation = async (conversationId: string) => {
    const targetConversation = conversations.find((conversation) => conversation.id === conversationId)
    if (!targetConversation) {
      return
    }

    const isLocalOnly = targetConversation.messages.length > 0 && !targetConversation.messages.some((message) => message.role === 'user')

    try {
      if (!isLocalOnly) {
        await deleteConversation(conversationId)
      }

      const remainingConversations = conversations.filter(
        (conversation) => conversation.id !== conversationId,
      )
      const fallbackConversation =
        remainingConversations[0] ??
        (() => {
          const conversation = createWelcomeConversation()
          return conversation
        })()

      setConversations(
        remainingConversations.length > 0 ? remainingConversations : [fallbackConversation],
      )

      if (activeConversationId === conversationId) {
        setActiveConversationId(fallbackConversation.id)
      }
    } catch (error) {
      const message =
        error instanceof Error ? error.message : '删除会话失败，请稍后重试。'
      window.alert(`删除会话失败：${message}`)
    }
  }

  const handleClearConversation = () => {
    if (!activeConversation) {
      return
    }

    if (streamingConversationId === activeConversation.id) {
      window.alert('当前会话仍在后台生成，请等待完成后再清空。')
      return
    }

    const resetMessage: ChatMessage = {
      id: createId(),
      role: 'assistant',
      content: '当前会话已清空。你可以继续发起新的提问。',
      timestamp: new Date().toISOString(),
    }

    setConversations((prev) =>
      prev.map((conversation) =>
        conversation.id === activeConversation.id
          ? {
              ...conversation,
              title: '新的对话',
              messages: [resetMessage],
              updatedAt: resetMessage.timestamp,
            }
          : conversation,
      ),
    )
  }

  const handleEditMessage = async (messageId: string, newContent: string) => {
    if (!activeConversation || !ensureNoActiveGeneration('编辑消息')) {
      return
    }

    try {
      const updatedConversation = await editMessage(
        activeConversation.id,
        messageId,
        newContent,
      )
      replaceConversation(updatedConversation)
    } catch (error) {
      const message =
        error instanceof Error ? error.message : '编辑消息失败，请稍后重试。'
      window.alert(`编辑消息失败：${message}`)
    }
  }

  const handleDeleteMessage = async (messageId: string) => {
    if (!activeConversation || !ensureNoActiveGeneration('删除消息')) {
      return
    }

    if (activeConversation.messages.length <= 1) {
      window.alert('当前对话至少需要保留一条消息。')
      return
    }

    try {
      const updatedConversation = await deleteMessage(activeConversation.id, messageId)
      replaceConversation(updatedConversation)
    } catch (error) {
      const message =
        error instanceof Error ? error.message : '删除消息失败，请稍后重试。'
      window.alert(`删除消息失败：${message}`)
    }
  }

  const handleRegenerateMessage = async (messageId: string) => {
    if (!activeConversation || !ensureNoActiveGeneration('重新生成')) {
      return
    }

    const conversationId = activeConversation.id
    setStreamingConversationId(conversationId)
    try {
      const updatedConversation = await regenerateMessage(conversationId, messageId)
      replaceConversation(updatedConversation)
    } catch (error) {
      const message =
        error instanceof Error ? error.message : '重新生成失败，请稍后重试。'
      window.alert(`重新生成失败：${message}`)
    } finally {
      setStreamingConversationId((current) =>
        current === conversationId ? null : current,
      )
    }
  }

  const handleExportConversation = async (
    conversationId: string,
    format: 'markdown',
  ) => {
    if (streamingConversationId === conversationId) {
      throw new Error('当前对话仍在生成，请完成后再导出。')
    }

    return exportConversation(conversationId, format)
  }

  const handleCreateKnowledgeBase = async (name: string, description: string) => {
    try {
      const createdKnowledgeBase = await createKnowledgeBase(name, description)

      setKnowledgeBases((prev) => [createdKnowledgeBase, ...prev])
      setSelectedKnowledgeBaseId(createdKnowledgeBase.id)
      setSelectedDocumentId(null)
    } catch (error) {
      const message =
        error instanceof Error ? error.message : '创建知识库失败，请稍后重试。'
      window.alert(`创建知识库失败：${message}`)
    }
  }

  const handleDeleteKnowledgeBase = async (knowledgeBaseId: string) => {
    try {
      await deleteKnowledgeBase(knowledgeBaseId)

      setKnowledgeBases((prev) => {
        const nextKnowledgeBases = prev.filter(
          (knowledgeBase) => knowledgeBase.id !== knowledgeBaseId,
        )

        if (selectedKnowledgeBaseId === knowledgeBaseId) {
          setSelectedKnowledgeBaseId(nextKnowledgeBases[0]?.id ?? null)
          setSelectedDocumentId(null)
        }

        return nextKnowledgeBases
      })
    } catch (error) {
      const message =
        error instanceof Error ? error.message : '删除知识库失败，请稍后重试。'
      window.alert(`删除知识库失败：${message}`)
    }
  }

  const handleSelectKnowledgeBase = (knowledgeBaseId: string) => {
    setSelectedKnowledgeBaseId(knowledgeBaseId)
    setSelectedDocumentId(null)
  }

  const handleSelectDocument = (
    knowledgeBaseId: string,
    documentId: string | null,
  ) => {
    setSelectedKnowledgeBaseId(knowledgeBaseId)
    setSelectedDocumentId(documentId)
  }

  const handleGenerateEvalDataset = async (
    knowledgeBaseId: string,
  ): Promise<GenerateEvalDatasetResponse> => {
    try {
      return await generateEvalDataset(knowledgeBaseId)
    } catch (error) {
      const message =
        error instanceof Error ? error.message : '生成评估集失败，请稍后重试。'
      throw new Error(message)
    }
  }

  const handleListEvalDatasets = async (
    knowledgeBaseId: string,
  ): Promise<EvalDatasetSummary[]> => {
    try {
      const response = await listEvalDatasets(knowledgeBaseId)
      return response.items
    } catch (error) {
      const message =
        error instanceof Error ? error.message : '加载评估集历史失败，请稍后重试。'
      throw new Error(message)
    }
  }

  const handleListEvalRuns = async (
    knowledgeBaseId: string,
  ): Promise<EvalRunSummary[]> => {
    try {
      const response = await listEvalRuns(knowledgeBaseId)
      return response.items
    } catch (error) {
      const message =
        error instanceof Error ? error.message : '加载评估趋势失败，请稍后重试。'
      throw new Error(message)
    }
  }

  const handleFetchEvalDataset = async (
    datasetId: string,
  ): Promise<EvalDatasetDetail> => {
    try {
      return await getEvalDataset(datasetId)
    } catch (error) {
      const message =
        error instanceof Error ? error.message : '加载评估集失败，请稍后重试。'
      throw new Error(message)
    }
  }

  const handleDeleteEvalDataset = async (datasetId: string): Promise<void> => {
    try {
      await deleteEvalDataset(datasetId)
    } catch (error) {
      const message =
        error instanceof Error ? error.message : '删除评估集失败，请稍后重试。'
      throw new Error(message)
    }
  }

  const handleAddEvalDatasetCandidate = async (
    knowledgeBaseId: string,
    documentId: string | null,
    item: EvalGroundTruthCase,
  ): Promise<EvalDatasetSummary> => {
    try {
      const response = await addEvalDatasetCandidate(knowledgeBaseId, documentId, item)
      return response.dataset
    } catch (error) {
      const message =
        error instanceof Error ? error.message : '加入待审核评估集失败，请稍后重试。'
      throw new Error(message)
    }
  }

  const handleUpdateEvalDatasetItem = async (
    datasetId: string,
    itemId: string,
    item: EvalGroundTruthCase,
  ): Promise<UpdateEvalDatasetItemResponse> => {
    try {
      return await updateEvalDatasetItem(datasetId, itemId, item)
    } catch (error) {
      const message =
        error instanceof Error ? error.message : '更新评估样本失败，请稍后重试。'
      throw new Error(message)
    }
  }

  const handleDeleteEvalDatasetItem = async (
    datasetId: string,
    itemId: string,
  ): Promise<DeleteEvalDatasetItemResponse> => {
    try {
      return await deleteEvalDatasetItem(datasetId, itemId)
    } catch (error) {
      const message =
        error instanceof Error ? error.message : '删除评估样本失败，请稍后重试。'
      throw new Error(message)
    }
  }

  const handleRunEvalDataset = async (
    datasetId: string,
    options: RetrievalSearchMode | EvalRunOptions = 'auto',
  ): Promise<RunEvalDatasetResponse> => {
    try {
      return await runEvalDataset(datasetId, options)
    } catch (error) {
      const message =
        error instanceof Error ? error.message : '运行评估失败，请稍后重试。'
      throw new Error(message)
    }
  }

  const processDirectoryUploadQueue = async (
    knowledgeBaseId: string,
    queue: UploadQueueItem[],
    mode: 'new' | 'resume',
  ) => {
    if (queue.length === 0) {
      setDirectoryUploadTask((prev) => {
        const nextTask: DirectoryUploadTask = {
          ...prev,
          knowledgeBaseId,
          status: prev.failedFiles > 0 ? 'partial-failed' : 'done',
          pendingFiles: 0,
          currentFileName: '',
          currentFilePath: '',
        }
        return {
          ...nextTask,
          summaryMessage: buildDirectoryUploadSummary(nextTask),
        }
      })
      return
    }

    directoryUploadCancelRef.current = false
    setSelectedKnowledgeBaseId(knowledgeBaseId)

    setDirectoryUploadTask((prev) => ({
      ...prev,
      knowledgeBaseId,
      status: 'uploading',
      currentFileName: mode === 'resume' ? prev.currentFileName : '',
      currentFilePath: mode === 'resume' ? prev.currentFilePath : '',
      pendingFiles: queue.length,
      summaryMessage: '',
    }))

    const nextPendingQueue: UploadQueueItem[] = []
    const uploadIds: string[] = []

    for (let index = 0; index < queue.length; index += 1) {
      if (directoryUploadCancelRef.current) {
        nextPendingQueue.push(...queue.slice(index))
        break
      }

      const item = queue[index]

      setDirectoryUploadTask((prev) => ({
        ...prev,
        status: prev.status === 'canceling' ? 'canceling' : 'uploading',
        currentFileName: item.name,
        currentFilePath: item.path,
        pendingFiles: queue.length - index,
      }))

      try {
        const uploaded = await stageUpload(item.file)
        uploadIds.push(uploaded.uploadId)

        setDirectoryUploadTask((prev) => ({
          ...prev,
          processedFiles: prev.processedFiles + 1,
          pendingFiles: Math.max(queue.length - index - 1, 0),
          summaryMessage: `已暂存 ${uploadIds.length}/${queue.length} 个文件，等待批量索引...`,
        }))
      } catch (error) {
        const reason = error instanceof Error ? error.message : '暂存文件失败，请稍后重试。'
        setDirectoryUploadTask((prev) => ({
          ...prev,
          failedFiles: prev.failedFiles + 1,
          processedFiles: prev.processedFiles + 1,
          pendingFiles: Math.max(queue.length - index - 1, 0),
          failedItems: [...prev.failedItems, { name: item.name, path: item.path, reason }],
        }))
      }
    }

    setDirectoryUploadPendingFiles(nextPendingQueue)
    const stopAfterCurrentBatch = directoryUploadCancelRef.current && nextPendingQueue.length > 0

    if (directoryUploadCancelRef.current && uploadIds.length === 0) {
      setDirectoryUploadTask((prev) => {
        const nextTask: DirectoryUploadTask = {
          ...prev,
          status: 'canceled',
          currentFileName: '',
          currentFilePath: '',
          pendingFiles: nextPendingQueue.length,
        }
        return {
          ...nextTask,
          summaryMessage: buildDirectoryUploadSummary(nextTask),
        }
      })
      return
    }

    if (stopAfterCurrentBatch) {
      directoryUploadCancelRef.current = false
    }

    if (uploadIds.length === 0) {
      setDirectoryUploadTask((prev) => {
        const nextTask: DirectoryUploadTask = {
          ...prev,
          status: prev.successFiles > 0 ? 'partial-failed' : 'failed',
          currentFileName: '',
          currentFilePath: '',
          pendingFiles: 0,
        }
        return {
          ...nextTask,
          summaryMessage: buildDirectoryUploadSummary(nextTask),
        }
      })
      return
    }

    setDirectoryUploadTask((prev) => ({
      ...prev,
      status: 'indexing',
      currentFileName: '',
      currentFilePath: '',
      indexingFiles: prev.indexingFiles + uploadIds.length,
      summaryMessage: `正在批量索引 ${uploadIds.length} 个文件...`,
    }))

    try {
      const batchResult = await batchIndexDocuments(knowledgeBaseId, uploadIds)
      const successfulResults = batchResult.results.filter(
        (result) => result.success && result.document,
      )
      const failedIndexResults = batchResult.results.filter((result) => !result.success)

      const newDocuments = successfulResults.map((result) => {
        const doc = result.document!
        return {
          id: doc.id,
          name: doc.name,
          size: doc.size,
          sizeLabel: doc.sizeLabel,
          uploadedAt: doc.uploadedAt,
          status: doc.status,
          contentPreview: doc.contentPreview,
          chunkCount: doc.chunkCount,
          indexedAt: doc.indexedAt,
          indexError: doc.indexError,
        }
      })

      const failedIndexItems: DirectoryUploadIssueItem[] = failedIndexResults.map((result) => ({
        name: result.fileName || result.uploadId,
        path: result.fileName || result.uploadId,
        reason: result.error || '批量索引失败',
      }))
      const batchIndexFailedCount = failedIndexItems.length

      setKnowledgeBases((prev) =>
        prev.map((kb) =>
          kb.id === knowledgeBaseId
            ? {
                ...kb,
                documents: [...newDocuments, ...kb.documents],
              }
            : kb,
        ),
      )

      if (newDocuments.length > 0) {
        setSelectedDocumentId((current) => current ?? newDocuments[0].id)
      }

      setDirectoryUploadTask((prev) => ({
        ...prev,
        successFiles: prev.successFiles + newDocuments.length,
        failedFiles: prev.failedFiles + failedIndexItems.length,
        indexFailedFiles: prev.indexFailedFiles + failedIndexItems.length,
        failedItems: [...prev.failedItems, ...failedIndexItems],
      }))

      if (newDocuments.length === 0) {
        setDirectoryUploadTask((prev) => {
          const nextTask: DirectoryUploadTask = {
            ...prev,
            status: stopAfterCurrentBatch
              ? 'canceled'
              : prev.successFiles > 0
                ? 'partial-failed'
                : 'failed',
            currentFileName: '',
            currentFilePath: '',
            pendingFiles: stopAfterCurrentBatch ? nextPendingQueue.length : 0,
          }
          return {
            ...nextTask,
            summaryMessage: buildDirectoryUploadSummary(nextTask),
          }
        })
        return
      }

      setDirectoryUploadTask((prev) => ({
        ...prev,
        status: 'polling-index',
        summaryMessage: `批量索引已触发，正在轮询索引状态...`,
      }))

      const documentIds = newDocuments.map((doc) => doc.id)
      const maxPolls = 60
      const pollInterval = 2000

      for (let poll = 0; poll < maxPolls; poll += 1) {
        if (directoryUploadCancelRef.current) {
          break
        }

        await sleep(pollInterval)

        const statuses = await Promise.all(
          documentIds.map((docId) => getDocumentIndexStatus(knowledgeBaseId, docId)),
        )

        const indexedCount = statuses.filter((s) => s.status === 'indexed').length
        const processingCount = statuses.filter((s) => s.status === 'processing').length
        const failedCount = statuses.filter((s) => s.status === 'failed').length

        setDirectoryUploadTask((prev) => ({
          ...prev,
          failedFiles: prev.failedItems.length + failedCount,
          indexedFiles: indexedCount,
          indexFailedFiles: batchIndexFailedCount + failedCount,
          summaryMessage: `索引中: ${indexedCount}/${documentIds.length} 已完成`,
        }))

        setKnowledgeBases((prev) =>
          prev.map((kb) =>
            kb.id === knowledgeBaseId
              ? {
                  ...kb,
                  documents: kb.documents.map((doc) => {
                    const statusUpdate = statuses.find((s) => s.documentId === doc.id)
                    return statusUpdate
                      ? {
                          ...doc,
                          status: statusUpdate.status,
                          indexedAt: statusUpdate.indexedAt ?? doc.indexedAt,
                          indexError: statusUpdate.indexError ?? doc.indexError,
                        }
                      : doc
                  }),
                }
              : kb,
          ),
        )

        if (processingCount === 0) {
          break
        }
      }

      setDirectoryUploadTask((prev) => {
        const finalStatus =
          stopAfterCurrentBatch
            ? 'canceled'
            : prev.failedFiles > 0 && prev.successFiles === 0
              ? 'failed'
              : prev.failedFiles > 0
                ? 'partial-failed'
                : 'done'

        const nextTask: DirectoryUploadTask = {
          ...prev,
          status: finalStatus,
          currentFileName: '',
          currentFilePath: '',
          pendingFiles: stopAfterCurrentBatch ? nextPendingQueue.length : 0,
        }
        return {
          ...nextTask,
          summaryMessage: buildDirectoryUploadSummary(nextTask),
        }
      })
    } catch (error) {
      const message = error instanceof Error ? error.message : '批量索引失败，请稍后重试。'
      setDirectoryUploadTask((prev) => ({
        ...prev,
        status: prev.successFiles > 0 ? 'partial-failed' : 'failed',
        currentFileName: '',
        currentFilePath: '',
        pendingFiles: stopAfterCurrentBatch ? nextPendingQueue.length : prev.pendingFiles,
        summaryMessage: `批量索引失败: ${message}`,
      }))
    }
  }

  const handleUploadFiles = async (knowledgeBaseId: string, files: FileList | null) => {
    if (!files || files.length === 0) {
      return
    }

    await handleUploadDirectory(knowledgeBaseId, files)
  }

  const handleUploadDirectory = async (knowledgeBaseId: string, files: FileList | null) => {
    if (!files || files.length === 0) {
      return
    }

    directoryUploadCancelRef.current = false
    const allItems = Array.from(files).map((file) => ({
      file,
      name: file.name,
      path: getUploadFilePath(file),
    }))

    const eligibleItems: UploadQueueItem[] = []
    const skippedItems: DirectoryUploadIssueItem[] = []

    setDirectoryUploadTask({
      knowledgeBaseId,
      status: 'scanning',
      totalFiles: allItems.length,
      eligibleFiles: 0,
      skippedFiles: 0,
      successFiles: 0,
      failedFiles: 0,
      pendingFiles: 0,
      processedFiles: 0,
      indexingFiles: 0,
      indexedFiles: 0,
      indexFailedFiles: 0,
      currentFileName: '',
      currentFilePath: '',
      failedItems: [],
      skippedItems: [],
      summaryMessage: '',
    })

    for (const item of allItems) {
      const extension = getFileExtension(item.name)
      if (DIRECTORY_UPLOAD_ALLOWED_EXTENSIONS.has(extension)) {
        eligibleItems.push(item)
      } else {
        skippedItems.push({
          name: item.name,
          path: item.path,
          reason: extension ? `不支持的后缀 ${extension}` : '缺少文件后缀',
        })
      }
    }

    setDirectoryUploadPendingFiles(eligibleItems)

    const scannedTask: DirectoryUploadTask = {
      knowledgeBaseId,
      status: eligibleItems.length > 0 ? 'uploading' : 'done',
      totalFiles: allItems.length,
      eligibleFiles: eligibleItems.length,
      skippedFiles: skippedItems.length,
      successFiles: 0,
      failedFiles: 0,
      pendingFiles: eligibleItems.length,
      processedFiles: 0,
      indexingFiles: 0,
      indexedFiles: 0,
      indexFailedFiles: 0,
      currentFileName: '',
      currentFilePath: '',
      failedItems: [],
      skippedItems,
      summaryMessage: '',
    }

    setDirectoryUploadTask({
      ...scannedTask,
      summaryMessage:
        eligibleItems.length === 0 ? '所选内容中没有可上传的 .txt、.md、.pdf、.csv 或 .xlsx 文件。' : '',
    })

    if (eligibleItems.length === 0) {
      return
    }

    await processDirectoryUploadQueue(knowledgeBaseId, eligibleItems, 'new')
  }

  const handleCancelDirectoryUpload = () => {
    directoryUploadCancelRef.current = true
    setDirectoryUploadTask((prev) => ({
      ...prev,
      status: prev.status === 'uploading' ? 'canceling' : prev.status,
      summaryMessage: prev.status === 'uploading' ? '正在取消，当前文件处理完成后停止。' : prev.summaryMessage,
    }))
  }

  const handleContinueDirectoryUpload = async () => {
    if (!directoryUploadTask.knowledgeBaseId || directoryUploadPendingFiles.length === 0) {
      return
    }

    await processDirectoryUploadQueue(
      directoryUploadTask.knowledgeBaseId,
      directoryUploadPendingFiles,
      'resume',
    )
  }

  const handleRemoveDocument = async (knowledgeBaseId: string, documentId: string) => {
    try {
      await deleteKnowledgeBaseDocument(knowledgeBaseId, documentId)

      setKnowledgeBases((prev) =>
        prev.map((knowledgeBase) =>
          knowledgeBase.id === knowledgeBaseId
            ? {
                ...knowledgeBase,
                documents: knowledgeBase.documents.filter(
                  (document) => document.id !== documentId,
                ),
              }
            : knowledgeBase,
        ),
      )

      setSelectedDocumentId((current) => (current === documentId ? null : current))
    } catch (error) {
      const message =
        error instanceof Error ? error.message : '删除文档失败，请稍后重试。'
      window.alert(`删除文档失败：${message}`)
    }
  }

  const handleFetchDocumentDetail = async (
    knowledgeBaseId: string,
    documentId: string,
    focusChunkId?: string,
  ): Promise<DocumentDetailResponse> => {
    return fetchKnowledgeBaseDocumentDetail(knowledgeBaseId, documentId, focusChunkId)
  }

  const handleFetchKnowledgeBaseHealth = useCallback(async (
    knowledgeBaseId: string,
  ): Promise<KnowledgeBaseHealthResponse> => {
    return fetchKnowledgeBaseHealth(knowledgeBaseId)
  }, [])

  const handleReindexDocument = async (knowledgeBaseId: string, documentId: string) => {
    try {
      const updatedDocument = await reindexKnowledgeBaseDocument(knowledgeBaseId, documentId)
      setKnowledgeBases((prev) =>
        prev.map((knowledgeBase) =>
          knowledgeBase.id === knowledgeBaseId
            ? {
                ...knowledgeBase,
                documents: knowledgeBase.documents.map((document) =>
                  document.id === documentId ? updatedDocument : document,
                ),
              }
            : knowledgeBase,
        ),
      )
      return updatedDocument
    } catch (error) {
      const message =
        error instanceof Error ? error.message : '重建索引失败，请稍后重试。'
      window.alert(`重建索引失败：${message}`)
      throw error
    }
  }

  const handleDebugRetrieval = async (
    knowledgeBaseId: string,
    query: string,
    documentId: string | null,
    searchMode: RetrievalSearchMode = 'auto',
  ): Promise<RetrievalDebugResponse> => {
    return debugKnowledgeBaseRetrieval(knowledgeBaseId, query, documentId, searchMode)
  }

  const handleSendMessage = async (content: string) => {
    if (!activeConversation) {
      return
    }

    if (isOllamaSingleFlightMode && streamingConversationId) {
      setConversations((prev) =>
        prev.map((conversation) => {
          if (conversation.id !== activeConversation.id) {
            return conversation
          }

          const now = new Date().toISOString()
          return {
            ...conversation,
            messages: [
              ...conversation.messages,
              {
                id: createId(),
                role: 'assistant',
                content: `当前模型正在后台处理会话「${generatingConversationTitle}」，请等待其完成后再发起新问题。`,
                timestamp: now,
              },
            ],
            updatedAt: now,
          }
        }),
      )
      return
    }

    if (!backendReady) {
      const isReady = await waitForBackendReady(20, 1000)
      if (!isReady) {
        window.alert('后端服务正在启动或尚未就绪，请稍后再发送问题。')
        return
      }
    }

    const streamAbortController = new AbortController()
    chatAbortControllerRef.current = streamAbortController

    const conversationId = activeConversation.id
    const requestId = createId()
    activeChatRequestRef.current = { requestId, conversationId }
    const timestamp = new Date().toISOString()
    const userMessage: ChatMessage = {
      id: createId(),
      role: 'user',
      content,
      timestamp,
    }
    const assistantMessageId = createId()
    const assistantTimestamp = new Date().toISOString()
    const assistantMessage: ChatMessage = {
      id: assistantMessageId,
      role: 'assistant',
      content: '',
      timestamp: assistantTimestamp,
    }

    const nextMessages = [...activeConversation.messages, userMessage]
    const selectedChatModel =
      chatMode === 'think'
        ? chatModeSettings.thinkModel || config.chat.model
        : chatModeSettings.fastModel || config.chat.model

    const requestBody: ChatRequestBody = {
      conversationId,
      model: selectedChatModel,
      knowledgeBaseId: selectedKnowledgeBaseId ?? '',
      documentId: selectedDocumentId ?? '',
      retrievalMode: config.retrieval.defaultSearchMode,
      config: {
        ...config.chat,
        model: selectedChatModel,
      },
      embedding: config.embedding,
      messages: nextMessages.map((message) => ({
        role: message.role,
        content: message.content,
      })),
    }

    const isCurrentRequestActive = () => {
      const activeRequest = activeChatRequestRef.current
      return activeRequest?.requestId === requestId && activeRequest.conversationId === conversationId
    }

    const updateAssistantMessage = (updater: (current: ChatMessage) => ChatMessage) => {
      if (!isCurrentRequestActive()) {
        return
      }

      setConversations((prev) =>
        prev.map((conversation) => {
          if (conversation.id !== conversationId) {
            return conversation
          }

          return {
            ...conversation,
            messages: conversation.messages.map((message) =>
              message.id === assistantMessageId
                ? {
                    ...updater(message),
                    timestamp: new Date().toISOString(),
                  }
                : message,
            ),
            updatedAt: new Date().toISOString(),
          }
        }),
      )
    }

    const finalizeAssistantMessage = (contentOverride?: string, metadata?: ChatMessageMetadata) => {
      updateAssistantMessage((current) => ({
        ...current,
        content:
          contentOverride !== undefined
            ? contentOverride || '后端未返回有效回答。'
            : current.content || '后端未返回有效回答。',
        metadata: metadata ?? current.metadata,
      }))
    }

    const buildFriendlyChatError = (error: unknown) => {
      if (error instanceof DOMException && error.name === 'AbortError') {
        return '请求已取消。'
      }

      if (error instanceof Error) {
        const message = error.message.trim()
        if (!message) {
          return '聊天接口调用失败，请检查后端服务是否启动。'
        }
        if (message === 'stream-first-chunk-timeout') {
          return '本地模型首包超时，已自动切换为普通请求重试。'
        }
        if (message === 'fallback-request-timeout') {
          return '普通请求等待超时，请稍后重试或切换更轻量模型。'
        }
        if (message === 'stream-request-timeout') {
          return '流式连接等待超时，请稍后重试或切换更轻量模型。'
        }
        if (message.includes('Failed to fetch')) {
          return '无法连接后端服务，请检查服务是否启动，以及 Docker / Ollama 网络是否可达。'
        }
        return `聊天接口调用失败：${message}`
      }

      return '聊天接口调用失败，请检查后端服务是否启动。'
    }

    const withTimeout = async <T,>(promise: Promise<T>, timeoutMs: number, timeoutMessage: string) => {
      let timer = 0
      try {
        return await Promise.race([
          promise,
          new Promise<T>((_, reject) => {
            timer = window.setTimeout(() => {
              reject(new Error(timeoutMessage))
            }, timeoutMs)
          }),
        ])
      } finally {
        window.clearTimeout(timer)
      }
    }

    const requestFallbackCompletion = async (controller: AbortController) => {
      const headers = new Headers({
        'Content-Type': 'application/json',
      })
      applyCSRFHeader(headers, { method: 'POST' })

      const fallbackResponse = await withTimeout(
        fetch(`${API_BASE_PATH}/v1/chat/completions`, {
          method: 'POST',
          credentials: 'same-origin',
          headers,
          body: JSON.stringify(requestBody),
          signal: controller.signal,
        }),
        FALLBACK_REQUEST_TIMEOUT_MS,
        'fallback-request-timeout',
      )

      if (!fallbackResponse.ok) {
        throw new Error(await extractErrorMessage(fallbackResponse))
      }

      if (!isCurrentRequestActive()) {
        return
      }

      const data = await parseJsonResponse<ChatCompletionResponse>(fallbackResponse)
      if (!data) {
        throw new Error('聊天接口返回空响应')
      }
      finalizeAssistantMessage(
        data.choices[0]?.message?.content || '后端未返回有效回答。',
        normalizeChatMetadata(data.metadata),
      )
    }

    const requestWithFallback = async () => {
      if (backendWarmupRequired) {
        const warmupAbortController = new AbortController()
        chatAbortControllerRef.current = warmupAbortController
        await requestFallbackCompletion(warmupAbortController)
        setBackendWarmupRequired(false)
        return
      }

      let streamResponse: Response
      try {
        const headers = new Headers({
          'Content-Type': 'application/json',
          Accept: 'text/event-stream',
        })
        applyCSRFHeader(headers, { method: 'POST' })

        streamResponse = await fetch(`${API_BASE_PATH}/v1/chat/completions/stream`, {
          method: 'POST',
          credentials: 'same-origin',
          headers,
          body: JSON.stringify(requestBody),
          signal: streamAbortController.signal,
        })
      } catch {
        const fallbackAbortController = new AbortController()
        chatAbortControllerRef.current = fallbackAbortController
        await requestFallbackCompletion(fallbackAbortController)
        return
      }

      if (!streamResponse.ok) {
        const fallbackAbortController = new AbortController()
        chatAbortControllerRef.current = fallbackAbortController
        await requestFallbackCompletion(fallbackAbortController)
        return
      }

      if (!streamResponse.body) {
        throw new Error('浏览器不支持流式响应读取')
      }

      const reader = streamResponse.body.getReader()
      const decoder = new TextDecoder('utf-8')
      let buffer = ''
      let streamCompleted = false
      let receivedFirstChunk = false
      const firstChunkTimer = window.setTimeout(() => {
        streamAbortController.abort()
      }, STREAM_FIRST_CHUNK_TIMEOUT_MS)
      const requestTimer = window.setTimeout(() => {
        streamAbortController.abort()
      }, STREAM_REQUEST_TIMEOUT_MS)

      const markChunkReceived = () => {
        if (!receivedFirstChunk) {
          receivedFirstChunk = true
          window.clearTimeout(firstChunkTimer)
        }
      }

      const processEventBlock = (block: string) => {
        if (!isCurrentRequestActive()) {
          return
        }

        const normalizedBlock = block.replace(/\r\n/g, '\n').replace(/\r/g, '\n')
        const lines = normalizedBlock.split('\n')
        const eventLine = lines.find((line) => line.startsWith('event:'))
        const dataLines = lines.filter((line) => line.startsWith('data:'))
        const eventName = eventLine?.slice(6).trim() ?? 'message'
        const rawData = dataLines.map((line) => line.slice(5).trim()).join('\n')

        if (!rawData) {
          return
        }

        const payload = JSON.parse(rawData) as StreamEventPayload

        if (eventName === 'meta') {
          return
        }

        if (eventName === 'chunk') {
          markChunkReceived()
          if (payload.content) {
            updateAssistantMessage((current) => ({
              ...current,
              content: current.content + payload.content,
            }))
          }
          return
        }

        if (eventName === 'done') {
          markChunkReceived()
          const degradedMetadata =
            normalizeChatMetadata(payload.metadata) ??
            (payload.content && isDegradedFallbackContent(payload.content)
              ? {
                  degraded: true,
                  fallbackStrategy: 'stream-fallback-message',
                }
              : undefined)
          finalizeAssistantMessage(payload.content, degradedMetadata)
          streamCompleted = true
          return
        }

        if (eventName === 'error') {
          throw new Error(payload.error || '流式响应失败')
        }
      }

      try {
        for (;;) {
          const { done, value } = await reader.read()
          buffer += decoder.decode(value ?? new Uint8Array(), { stream: !done })
          const normalizedBuffer = buffer.replace(/\r\n/g, '\n').replace(/\r/g, '\n')

          const blocks = normalizedBuffer.split('\n\n')
          buffer = blocks.pop() ?? ''

          for (const block of blocks) {
            processEventBlock(block)
          }

          if (done) {
            break
          }
        }

        const rest = buffer.trim()
        if (rest) {
          processEventBlock(rest)
        }
      } catch (error) {
        if (!receivedFirstChunk && error instanceof DOMException && error.name === 'AbortError') {
          const fallbackAbortController = new AbortController()
          chatAbortControllerRef.current = fallbackAbortController
          await requestFallbackCompletion(fallbackAbortController)
          return
        }
        throw error
      } finally {
        window.clearTimeout(firstChunkTimer)
        window.clearTimeout(requestTimer)
        reader.releaseLock()
      }

      if (!streamCompleted) {
        finalizeAssistantMessage()
      }
    }

    setStreamingConversationId(conversationId)
    setConversations((prev) =>
      prev.map((conversation) => {
        if (conversation.id !== conversationId) {
          return conversation
        }

        return {
          ...conversation,
          title:
            conversation.messages.length <= 1
              ? content.slice(0, 18) || '新的对话'
              : conversation.title,
          messages: [...nextMessages, assistantMessage],
          updatedAt: assistantTimestamp,
        }
      }),
    )

    try {
      await requestWithFallback()
    } catch (error) {
      if (error instanceof Error && error.message.includes('Failed to fetch')) {
        setBackendReady(false)
        void waitForBackendReady(8, 1500)
      }
      updateAssistantMessage((current) => ({
        ...current,
        content: buildFriendlyChatError(error),
      }))
    } finally {
      const activeRequest = activeChatRequestRef.current
      if (activeRequest?.requestId === requestId && activeRequest.conversationId === conversationId) {
        activeChatRequestRef.current = null
        chatAbortControllerRef.current = null
        setStreamingConversationId((current) =>
          current === conversationId ? null : current,
        )
      }
    }
  }

  const handleSaveSettings = async (nextConfig: AppConfig, nextThinkModel: string) => {
    const savedConfig = await persistConfigToBackend(nextConfig)
    const normalizedThinkModel = nextThinkModel.trim()
    setThinkModel(normalizedThinkModel)
    if (typeof window !== 'undefined') {
      if (normalizedThinkModel) {
        window.localStorage.setItem(THINK_MODEL_STORAGE_KEY, normalizedThinkModel)
      } else {
        window.localStorage.removeItem(THINK_MODEL_STORAGE_KEY)
      }
    }
    return savedConfig
  }

  const handleChangeWorkspace = (workspace: WorkspaceView) => {
    setActiveWorkspace(workspace)
    if (workspace !== 'chat') {
      setSidebarOpen(false)
    }
  }

  const handleToggleKnowledgeBaseCollapse = (knowledgeBaseId: string) => {
    setCollapsedKnowledgeBases((current) => ({
      ...current,
      [knowledgeBaseId]: !current[knowledgeBaseId],
    }))
  }

  const handleOpenCitationSource = (source: ChatSourceMetadata) => {
    if (!source.knowledgeBaseId || !source.documentId) {
      return
    }
    setSelectedKnowledgeBaseId(source.knowledgeBaseId)
    setSelectedDocumentId(source.documentId)
    setCitationNavigationTarget({
      knowledgeBaseId: source.knowledgeBaseId,
      documentId: source.documentId,
      chunkId: source.chunkId,
    })
    setActiveWorkspace('knowledge')
    setSidebarOpen(false)
  }

  useEffect(() => {
    const checkAuth = async () => {
      try {
        const health = await fetchBackendHealth()
        if (!health) {
          throw new Error('health check unavailable')
        }
        const authEnabled = health?.config?.auth_enabled === 'true'
        if (!authEnabled) {
          setAuthRequired(false)
          return
        }

        setAuthRequired(true)
        if (!isAuthenticated) {
          return
        }

        const response = await fetch(`${API_BASE_PATH}/api/auth/status`, {
          credentials: 'same-origin',
        })
        if (response.status === 401) {
          setAuthRequired(true)
          void logout()
          return
        }
        if (!response.ok) {
          throw new Error('auth status check failed')
        }
      } catch {
        try {
          const response = await fetch(`${API_BASE_PATH}/api/knowledge-bases`, {
            credentials: 'same-origin',
          })
          if (response.status === 401) {
            setAuthRequired(true)
            if (isAuthenticated) {
              void logout()
            }
            return
          }
          setAuthRequired(!response.ok)
        } catch {
          setAuthRequired(true)
        }
      } finally {
        setAuthCheckDone(true)
      }
    }
    checkAuth()
  }, [isAuthenticated, logout])

  if (!authCheckDone) {
    return <Login checkingConnection />
  }

  if (authRequired && !isAuthenticated) {
    return <Login />
  }

  return (
    <>
      <LoadingBar loading={globalLoading} />
      <div
        className={`chat-page workspace-${activeWorkspace} ${
          activeWorkspace === 'chat' && sidebarOpen ? 'context-open' : 'context-closed'
        }`}
      >
        <Sidebar
          isOpen={sidebarOpen}
          onToggle={() => setSidebarOpen(!sidebarOpen)}
          activeWorkspace={activeWorkspace}
          onChangeWorkspace={handleChangeWorkspace}
          conversations={conversations}
          activeConversationId={activeConversation?.id ?? null}
          onSelectConversation={handleSelectConversation}
          onCreateConversation={handleCreateConversation}
          onRenameConversation={handleRenameConversation}
          onDeleteConversation={handleDeleteConversation}
        />

        {activeWorkspace === 'chat' && (
          <ChatArea
            sidebarOpen={sidebarOpen}
            activeConversation={activeConversation}
            selectedKnowledgeBase={selectedKnowledgeBase}
            selectedDocument={selectedDocument}
            config={config}
            chatMode={chatMode}
            chatModeSettings={chatModeSettings}
            isLoading={streamingConversationId === activeConversation?.id}
            isGlobalGenerating={Boolean(streamingConversationId)}
            generatingConversationTitle={generatingConversationTitle}
            enforceSingleFlight={isOllamaSingleFlightMode}
            onChatModeChange={setChatMode}
            onSendMessage={handleSendMessage}
            onClearConversation={handleClearConversation}
            onEditMessage={handleEditMessage}
            onDeleteMessage={handleDeleteMessage}
            onRegenerateMessage={handleRegenerateMessage}
            onExportConversation={handleExportConversation}
            onOpenCitationSource={handleOpenCitationSource}
          />
        )}

        {activeWorkspace === 'knowledge' && (
          <KnowledgePanelWrapper
            open
            knowledgeBases={knowledgeBases}
            collapsedKnowledgeBases={collapsedKnowledgeBases}
            onToggleCollapse={handleToggleKnowledgeBaseCollapse}
            selectedKnowledgeBaseId={selectedKnowledgeBase?.id ?? null}
            selectedDocumentId={selectedDocumentId}
            onSelectKnowledgeBase={handleSelectKnowledgeBase}
            onSelectDocument={handleSelectDocument}
            onCreateKnowledgeBase={handleCreateKnowledgeBase}
            onDeleteKnowledgeBase={handleDeleteKnowledgeBase}
            onUploadFiles={handleUploadFiles}
            onUploadDirectory={handleUploadDirectory}
            onGenerateEvalDataset={handleGenerateEvalDataset}
            onListEvalDatasets={handleListEvalDatasets}
            onListEvalRuns={handleListEvalRuns}
            onFetchEvalDataset={handleFetchEvalDataset}
            onDeleteEvalDataset={handleDeleteEvalDataset}
            onAddEvalDatasetCandidate={handleAddEvalDatasetCandidate}
            onUpdateEvalDatasetItem={handleUpdateEvalDatasetItem}
            onDeleteEvalDatasetItem={handleDeleteEvalDatasetItem}
            onRunEvalDataset={handleRunEvalDataset}
            directoryUploadTask={directoryUploadTask}
            onCancelDirectoryUpload={handleCancelDirectoryUpload}
            onContinueDirectoryUpload={handleContinueDirectoryUpload}
            onRemoveDocument={handleRemoveDocument}
            onFetchKnowledgeBaseHealth={handleFetchKnowledgeBaseHealth}
            onFetchDocumentDetail={handleFetchDocumentDetail}
            onReindexDocument={handleReindexDocument}
            onDebugRetrieval={handleDebugRetrieval}
            citationNavigationTarget={citationNavigationTarget}
            onCitationNavigationHandled={() => setCitationNavigationTarget(null)}
            onClose={() => handleChangeWorkspace('chat')}
          />
        )}

        {activeWorkspace === 'settings' && (
          <SettingsPanel
            config={config}
            onClose={() => handleChangeWorkspace('chat')}
            chatModeSettings={chatModeSettings}
            onSave={handleSaveSettings}
            onCopyMcpToken={handleCopyMcpToken}
            onResetMcpToken={handleResetMcpToken}
            onLogout={logout}
          />
        )}
      </div>
    </>
  )
}

function App() {
  return (
    <AuthProvider>
      <ToastProvider>
        <AppContent />
      </ToastProvider>
    </AuthProvider>
  )
}

export default App
