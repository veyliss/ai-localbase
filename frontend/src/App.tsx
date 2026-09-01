import './App.css'
import { createEmptyConversation, createId, normalizeChatMetadata } from './app/appHelpers'
import { createDefaultAppConfig, normalizeAppConfig } from './app/appConfig'
import { buildChatRequestBody } from './chat/chatRequest'
import ChatArea from './components/ChatArea'
import Sidebar from './components/Sidebar'
import Login from './components/Login'
import KnowledgePanelWrapper from './components/knowledge/KnowledgePanelWrapper'
import SettingsPanel from './components/settings/SettingsPanel'
import { ToastProvider, useToast } from './components/common/Toast'
import LoadingBar from './components/common/LoadingBar'
import { AuthProvider, useAuth } from './contexts/AuthContext'
import { useKnowledgeWorkspaceState } from './hooks/useKnowledgeWorkspaceState'
import { useAppPreferencesState } from './hooks/useAppPreferencesState'
import { useConversationWorkspaceState } from './hooks/useConversationWorkspaceState'
import { useAppBootstrap } from './app/useAppBootstrap'
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
  fetchConversationDetail,
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
  citationSupport?: CitationSupportMetadata
}

export interface CitationClaimSupport {
  text: string
  supported: boolean
  evidenceIds?: string[]
  evidenceSnippets?: string[]
  missingAnchors?: string[]
  matchedTermCount: number
  requiredTermCount: number
}

export interface CitationSupportMetadata {
  status: 'supported' | 'partial' | 'unsupported' | 'abstained' | string
  summary: string
  claimCount: number
  supportedClaimCount: number
  coverage: number
  issues?: string[]
  claims?: CitationClaimSupport[]
}

export interface ChatSourceMetadata {
  knowledgeBaseId?: string
  documentId?: string
  documentName?: string
  chunkId?: string
  evidenceId?: string
  chunkIndex?: string
  chunkKind?: string
  score?: string
  snippet?: string
  sourceType?: string
  toolName?: string
  charStart?: string
  charEnd?: string
  lineStart?: string
  lineEnd?: string
  tableRow?: string
  tableColumns?: string
  citationSnippet?: string
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
  knowledgeBaseId: string
  documentId: string
  scopeVersion: number
  messages: ChatMessage[]
  createdAt: string
  updatedAt: string
  localOnly?: boolean
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
  indexErrorCode?: string
  indexRunId?: string
  indexVersion?: number
  source?: string
  version?: number
}

export interface KnowledgeBase {
  id: string
  name: string
  description: string
  tags?: string[]
  documents: DocumentItem[]
  createdAt: string
  updatedAt?: string
  currentIndexVersion?: number
}

export interface ChatConfig {
  provider: 'ollama' | 'openai-compatible'
  baseUrl: string
  model: string
  apiKey: string
  apiKeyConfigured?: boolean
  clearApiKey?: boolean
  temperature: number
  knowledgeTemperature: number
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

const FALLBACK_REQUEST_TIMEOUT_MS = 180_000
const STREAM_FIRST_CHUNK_TIMEOUT_MS = 30_000
const STREAM_REQUEST_TIMEOUT_MS = 180_000

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
    citationSupport?: CitationSupportMetadata
  }
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

const conversationMatchesScope = (
  conversation: Conversation,
  knowledgeBaseId: string,
  documentId: string,
) => (
  conversation.knowledgeBaseId === knowledgeBaseId &&
  conversation.documentId === documentId
)

class ConversationScopeConflictError extends Error {}

const isConversationScopeConflictMessage = (message: string) => (
  message.includes('conversation scope mismatch') ||
  message.includes('legacy conversation scope is not trusted')
)

const sleep = (delayMs: number) =>
  new Promise((resolve) => {
    window.setTimeout(resolve, delayMs)
  })

function AppContent() {
  const { isAuthenticated, logout } = useAuth()
  const { showToast } = useToast()
  const {
    knowledgeBases,
    setKnowledgeBases,
    selectedKnowledgeBaseId,
    setSelectedKnowledgeBaseId,
    selectedDocumentId,
    setSelectedDocumentId,
    collapsedKnowledgeBases,
    selectedKnowledgeBase,
    selectedDocument,
    toggleKnowledgeBaseCollapse,
  } = useKnowledgeWorkspaceState()
  const [authWarningsShown, setAuthWarningsShown] = useState(false)
  const [globalLoading] = useState(false)
  const {
    conversations,
    setConversations,
    activeConversationId,
    setActiveConversationId,
    activeWorkspace,
    setActiveWorkspace,
    citationNavigationTarget,
    setCitationNavigationTarget,
    streamingConversationId,
    setStreamingConversationId,
  } = useConversationWorkspaceState(createEmptyConversation)
  const [directoryUploadTask, setDirectoryUploadTask] = useState<DirectoryUploadTask>(
    createEmptyDirectoryUploadTask,
  )
  const [directoryUploadPendingFiles, setDirectoryUploadPendingFiles] = useState<UploadQueueItem[]>([])
  const directoryUploadCancelRef = useRef(false)
  const chatAbortControllerRef = useRef<AbortController | null>(null)
  const activeChatRequestRef = useRef<{ requestId: string; conversationId: string } | null>(null)

  const [config, setConfig] = useState<AppConfig>(createDefaultAppConfig)

  const {
    authCheckDone,
    authRequired,
    backendReady,
    backendWarmupRequired,
    setBackendReady,
    setBackendWarmupRequired,
    waitForBackendReady,
  } = useAppBootstrap({
    isAuthenticated,
    logout,
    setKnowledgeBases,
    setConfig,
    setConversations,
    setActiveConversationId,
    setSelectedKnowledgeBaseId,
    setSelectedDocumentId,
  })

  const {
    sidebarOpen,
    setSidebarOpen,
    chatMode,
    setChatMode,
    setThinkModel,
    chatModeSettings,
  } = useAppPreferencesState(config)

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

    showToast('warning', `当前正在生成「${generatingConversationTitle}」，请等待完成后再${actionText}。`)
    return false
  }

  const startConversationForScope = (knowledgeBaseId: string, documentId: string) => {
    const conversation = createEmptyConversation(knowledgeBaseId, documentId)
    setConversations((prev) => [conversation, ...prev])
    setActiveConversationId(conversation.id)
    return conversation
  }

  const activateConversationScope = (knowledgeBaseId: string, documentId: string) => {
    if (!ensureNoActiveGeneration('切换知识库范围')) {
      return
    }

    setSelectedKnowledgeBaseId(knowledgeBaseId || null)
    setSelectedDocumentId(documentId || null)

    if (activeConversation && conversationMatchesScope(activeConversation, knowledgeBaseId, documentId)) {
      return
    }

    if (activeConversation?.localOnly && activeConversation.messages.length === 0) {
      setConversations((prev) => prev.map((conversation) => (
        conversation.id === activeConversation.id
          ? { ...conversation, knowledgeBaseId, documentId }
          : conversation
      )))
      return
    }

    startConversationForScope(knowledgeBaseId, documentId)
    showToast('info', '已按新的知识库范围创建会话。')
  }

  const handleCreateConversation = () => {
    const conversation = createEmptyConversation(
      selectedKnowledgeBaseId ?? '',
      selectedDocumentId ?? '',
    )

    setConversations((prev) => [conversation, ...prev])
    setActiveConversationId(conversation.id)
  }

  const handleSelectConversation = async (conversationId: string) => {
    const existingConversation = conversations.find((conversation) => conversation.id === conversationId)
    if (
      existingConversation?.localOnly ||
      (existingConversation && existingConversation.messages.length > 0)
    ) {
      setActiveConversationId(conversationId)
      setSelectedKnowledgeBaseId(existingConversation.knowledgeBaseId || null)
      setSelectedDocumentId(existingConversation.documentId || null)
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
      setSelectedKnowledgeBaseId(loadedConversation.knowledgeBaseId || null)
      setSelectedDocumentId(loadedConversation.documentId || null)
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

    const isLocalOnly = Boolean(targetConversation.localOnly)

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

    const isLocalOnly = Boolean(targetConversation.localOnly)

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
          const conversation = createEmptyConversation(
            selectedKnowledgeBaseId ?? '',
            selectedDocumentId ?? '',
          )
          return conversation
        })()

      setConversations(
        remainingConversations.length > 0 ? remainingConversations : [fallbackConversation],
      )

      if (activeConversationId === conversationId) {
        setActiveConversationId(fallbackConversation.id)
        setSelectedKnowledgeBaseId(fallbackConversation.knowledgeBaseId || null)
        setSelectedDocumentId(fallbackConversation.documentId || null)
      }
    } catch (error) {
      const message =
        error instanceof Error ? error.message : '删除会话失败，请稍后重试。'
      window.alert(`删除会话失败：${message}`)
    }
  }

  const handleClearConversation = async () => {
    if (!activeConversation) {
      return
    }

    if (streamingConversationId === activeConversation.id) {
      window.alert('当前会话仍在后台生成，请等待完成后再清空。')
      return
    }

    try {
      if (!activeConversation.localOnly) {
        await deleteConversation(activeConversation.id)
      }
      const emptyConversation = createEmptyConversation(
        selectedKnowledgeBaseId ?? '',
        selectedDocumentId ?? '',
      )
      setConversations((prev) =>
        prev.map((conversation) =>
          conversation.id === activeConversation.id ? emptyConversation : conversation,
        ),
      )
      setActiveConversationId(emptyConversation.id)
    } catch (error) {
      const message = error instanceof Error ? error.message : '清空会话失败，请稍后重试。'
      window.alert(`清空会话失败：${message}`)
    }
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

  const handleCreateKnowledgeBase = async (name: string, description: string, tags: string[] = []) => {
    try {
      const createdKnowledgeBase = await createKnowledgeBase(name, description, tags)

      setKnowledgeBases((prev) => [createdKnowledgeBase, ...prev])
      activateConversationScope(createdKnowledgeBase.id, '')
    } catch (error) {
      const message =
        error instanceof Error ? error.message : '创建知识库失败，请稍后重试。'
      window.alert(`创建知识库失败：${message}`)
    }
  }

  const handleDeleteKnowledgeBase = async (knowledgeBaseId: string) => {
    try {
      await deleteKnowledgeBase(knowledgeBaseId)

      const nextKnowledgeBases = knowledgeBases.filter(
        (knowledgeBase) => knowledgeBase.id !== knowledgeBaseId,
      )
      setKnowledgeBases(nextKnowledgeBases)
      if (selectedKnowledgeBaseId === knowledgeBaseId) {
        activateConversationScope(nextKnowledgeBases[0]?.id ?? '', '')
      }
    } catch (error) {
      const message =
        error instanceof Error ? error.message : '删除知识库失败，请稍后重试。'
      window.alert(`删除知识库失败：${message}`)
    }
  }

  const handleSelectKnowledgeBase = (knowledgeBaseId: string) => {
    activateConversationScope(knowledgeBaseId, '')
  }

  const handleSelectDocument = (
    knowledgeBaseId: string,
    documentId: string | null,
  ) => {
    activateConversationScope(knowledgeBaseId, documentId ?? '')
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
      let pollingCompleted = false
      let pollingCanceled = false

      for (let poll = 0; poll < maxPolls; poll += 1) {
        if (directoryUploadCancelRef.current) {
          pollingCanceled = true
          break
        }

        await sleep(pollInterval)

        if (directoryUploadCancelRef.current) {
          pollingCanceled = true
          break
        }

        const statuses = await Promise.all(
          documentIds.map((docId) => getDocumentIndexStatus(knowledgeBaseId, docId)),
        )

        const indexedCount = statuses.filter((s) => s.status === 'indexed').length
        const failedCount = statuses.filter((s) => s.status === 'failed').length
        const terminalCount = statuses.filter((s) =>
          s.status === 'indexed' || s.status === 'ready' || s.status === 'failed',
        ).length

        setDirectoryUploadTask((prev) => ({
          ...prev,
          failedFiles: prev.failedItems.length + failedCount,
          indexedFiles: indexedCount,
          indexFailedFiles: batchIndexFailedCount + failedCount,
          summaryMessage: `索引中: ${terminalCount}/${documentIds.length} 已处理${failedCount > 0 ? `，失败 ${failedCount}` : ''}`,
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
                          indexErrorCode: statusUpdate.indexErrorCode ?? doc.indexErrorCode,
                          indexRunId: statusUpdate.indexRunId ?? doc.indexRunId,
                          indexVersion: statusUpdate.indexVersion ?? doc.indexVersion,
                        }
                      : doc
                  }),
                }
              : kb,
          ),
        )

        if (terminalCount === documentIds.length) {
          pollingCompleted = true
          break
        }
      }

      const pollingStopped = pollingCanceled || directoryUploadCancelRef.current
      const pollingTimedOut =
        !pollingCompleted &&
        !pollingStopped

      setDirectoryUploadTask((prev) => {
        const finalStatus =
          stopAfterCurrentBatch || pollingStopped
            ? 'canceled'
            : pollingTimedOut
              ? 'partial-failed'
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
          summaryMessage: pollingTimedOut
            ? '索引确认超时，仍有文件未完成，请稍后刷新知识库。'
            : buildDirectoryUploadSummary(nextTask),
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
      status:
        prev.status === 'scanning' ||
        prev.status === 'uploading' ||
        prev.status === 'indexing' ||
        prev.status === 'polling-index'
          ? 'canceling'
          : prev.status,
      summaryMessage:
        prev.status === 'scanning' ||
        prev.status === 'uploading' ||
        prev.status === 'indexing' ||
        prev.status === 'polling-index'
          ? '正在取消，当前文件处理完成后停止。'
          : prev.summaryMessage,
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

      if (selectedDocumentId === documentId) {
        activateConversationScope(knowledgeBaseId, '')
      }
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
      return false
    }

    if (activeConversation.scopeVersion < 1) {
      startConversationForScope(
        selectedKnowledgeBaseId ?? activeConversation.knowledgeBaseId,
        selectedDocumentId ?? activeConversation.documentId,
      )
      showToast('info', '该历史会话仅供查看，已按当前知识库创建新会话。')
      return false
    }

    const selectedScope = {
      knowledgeBaseId: selectedKnowledgeBaseId ?? '',
      documentId: selectedDocumentId ?? '',
    }
    if (!conversationMatchesScope(
      activeConversation,
      selectedScope.knowledgeBaseId,
      selectedScope.documentId,
    )) {
      activateConversationScope(selectedScope.knowledgeBaseId, selectedScope.documentId)
      return false
    }

    if (isOllamaSingleFlightMode && streamingConversationId) {
      showToast(
        'warning',
        `当前模型正在处理会话「${generatingConversationTitle}」，请等待完成。`,
      )
      return false
    }

    if (!backendReady) {
      const isReady = await waitForBackendReady(20, 1000)
      if (!isReady) {
        window.alert('后端服务正在启动或尚未就绪，请稍后再发送问题。')
        return false
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

    const requestBody = buildChatRequestBody({
      conversationId,
      model: selectedChatModel,
      think: chatMode === 'think',
      knowledgeBaseId: activeConversation.knowledgeBaseId,
      documentId: activeConversation.documentId,
      retrievalMode: config.retrieval.defaultSearchMode,
      config: {
        ...config.chat,
        model: selectedChatModel,
      },
      embedding: config.embedding,
      messages: nextMessages,
    })

    const isCurrentRequestActive = () => {
      const activeRequest = activeChatRequestRef.current
      return activeRequest?.requestId === requestId && activeRequest.conversationId === conversationId
    }

    const updateAssistantMessage = (
      updater: (current: ChatMessage) => ChatMessage,
      markPersisted = false,
    ) => {
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
            localOnly: markPersisted ? false : conversation.localOnly,
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
      updateAssistantMessage(
        (current) => ({
          ...current,
          content: contentOverride !== undefined ? contentOverride : current.content,
          metadata: metadata ?? current.metadata,
        }),
        true,
      )
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
        const message = await extractErrorMessage(fallbackResponse)
        if (fallbackResponse.status === 409) {
          throw new ConversationScopeConflictError(message)
        }
        throw new Error(message)
      }

      if (!isCurrentRequestActive()) {
        return
      }

      const data = await parseJsonResponse<ChatCompletionResponse>(fallbackResponse)
      if (!data) {
        throw new Error('聊天接口返回空响应')
      }
      const assistantContent = data.choices[0]?.message?.content
      if (!assistantContent?.trim()) {
        throw new Error('聊天模型返回空回答')
      }
      finalizeAssistantMessage(assistantContent, normalizeChatMetadata(data.metadata))
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
        if (streamResponse.status === 409) {
          throw new ConversationScopeConflictError(await extractErrorMessage(streamResponse))
        }
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
          markChunkReceived()
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
          if (!payload.content?.trim()) {
            throw new Error('聊天模型返回空回答')
          }
          finalizeAssistantMessage(payload.content, normalizeChatMetadata(payload.metadata))
          streamCompleted = true
          return
        }

        if (eventName === 'error') {
          const message = payload.error || '流式响应失败'
          if (isConversationScopeConflictMessage(message)) {
            throw new ConversationScopeConflictError(message)
          }
          throw new Error(message)
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
        throw new Error('流式响应未正常结束')
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

    let inputConsumed = true
    try {
      await requestWithFallback()
    } catch (error) {
      if (error instanceof Error && error.message.includes('Failed to fetch')) {
        setBackendReady(false)
        void waitForBackendReady(8, 1500)
      }
      if (isCurrentRequestActive()) {
        setConversations((prev) =>
          prev.map((conversation) =>
            conversation.id === conversationId
              ? {
                  ...conversation,
                  messages: conversation.messages.filter(
                    (message) => message.id !== assistantMessageId,
                  ),
                }
              : conversation,
          ),
        )
      }
      if (error instanceof ConversationScopeConflictError) {
        inputConsumed = false
        const safeConversation = createEmptyConversation(
          selectedScope.knowledgeBaseId,
          selectedScope.documentId,
        )
        setConversations((prev) => [
          safeConversation,
          ...prev.map((conversation) => (
            conversation.id === conversationId
              ? {
                  ...conversation,
                  messages: conversation.messages.filter(
                    (message) => message.id !== userMessage.id && message.id !== assistantMessageId,
                  ),
                }
              : conversation
          )),
        ])
        setActiveConversationId(safeConversation.id)
        showToast('info', '知识库范围已更新，已切换到新会话。')
      } else {
        showToast('error', buildFriendlyChatError(error), 6000)
      }
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
    return inputConsumed
  }

  const handleSaveSettings = async (nextConfig: AppConfig, nextThinkModel: string) => {
    const savedConfig = await persistConfigToBackend(nextConfig)
    const normalizedThinkModel = nextThinkModel.trim()
    setThinkModel(normalizedThinkModel)
    return savedConfig
  }

  const handleChangeWorkspace = (workspace: WorkspaceView) => {
    setActiveWorkspace(workspace)
  }

  const handleToggleKnowledgeBaseCollapse = (knowledgeBaseId: string) => {
    toggleKnowledgeBaseCollapse(knowledgeBaseId)
  }

  const handleOpenCitationSource = (source: ChatSourceMetadata) => {
    if (!source.knowledgeBaseId || !source.documentId) {
      return
    }
    activateConversationScope(source.knowledgeBaseId, source.documentId)
    setCitationNavigationTarget({
      knowledgeBaseId: source.knowledgeBaseId,
      documentId: source.documentId,
      chunkId: source.chunkId,
    })
    setActiveWorkspace('knowledge')
  }

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
          onToggle={() => setSidebarOpen((current) => !current)}
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
