import './App.css'
import ChatArea from './components/ChatArea'
import Sidebar from './components/Sidebar'
import { useEffect, useMemo, useRef, useState } from 'react'

export interface ChatMessageMetadata {
  degraded?: boolean
  fallbackStrategy?: string
  upstreamError?: string
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
  sizeLabel: string
  uploadedAt: string
  status: 'indexed' | 'ready' | 'processing'
  contentPreview?: string
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
  temperature: number
  contextMessageLimit: number
}

export interface EmbeddingConfig {
  provider: 'ollama' | 'openai-compatible'
  baseUrl: string
  model: string
  apiKey: string
}

export interface AppConfig {
  chat: ChatConfig
  embedding: EmbeddingConfig
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
    sources?: Array<{
      knowledgeBaseId: string
      documentId: string
      documentName: string
    }>
  }
}

interface ChatRequestBody {
  conversationId: string
  model: string
  knowledgeBaseId: string
  documentId: string
  config: ChatConfig
  embedding: EmbeddingConfig
  messages: Array<{
    role: ChatMessage['role']
    content: string
  }>
}

interface ApiErrorResponse {
  error?: string
}

interface StreamEventPayload {
  content?: string
  error?: string
  metadata?: ChatMessageMetadata
}

interface HealthResponse {
  status?: string
}

const API_BASE_PATH = ''
const AI_CONFIG_STORAGE_KEY = 'ai-localbase:app-config'
const STREAM_FIRST_CHUNK_TIMEOUT_MS = 60000
const STREAM_REQUEST_TIMEOUT_MS = 150000
const FALLBACK_REQUEST_TIMEOUT_MS = 90000

const createId = () =>
  `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`

interface BackendDocumentItem {
  id: string
  name: string
  sizeLabel: string
  uploadedAt: string
  status: 'indexed' | 'ready' | 'processing'
  contentPreview?: string
}

interface BackendKnowledgeBase {
  id: string
  name: string
  description: string
  documents: BackendDocumentItem[]
  createdAt: string
}

interface KnowledgeBaseListResponse {
  items: BackendKnowledgeBase[]
}

interface ConfigResponse {
  chat: ChatConfig
  embedding: EmbeddingConfig
}

interface BackendConversationListItem {
  id: string
  title: string
  knowledgeBaseId: string
  documentId: string
  createdAt: string
  updatedAt: string
  messageCount: number
}

interface ConversationListResponse {
  items: BackendConversationListItem[]
}

interface BackendConversation {
  id: string
  title: string
  knowledgeBaseId: string
  documentId: string
  createdAt: string
  updatedAt: string
  messages: Array<{
    id: string
    role: 'assistant' | 'user'
    content: string
    createdAt: string
    metadata?: ChatMessageMetadata
  }>
}

interface UploadResponse {
  uploaded: BackendDocumentItem
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

const normalizeDocument = (document: BackendDocumentItem): DocumentItem => ({
  id: document.id,
  name: document.name,
  sizeLabel: document.sizeLabel,
  uploadedAt: document.uploadedAt,
  status: document.status,
  contentPreview: document.contentPreview,
})

const normalizeKnowledgeBase = (knowledgeBase: BackendKnowledgeBase): KnowledgeBase => ({
  id: knowledgeBase.id,
  name: knowledgeBase.name,
  description: knowledgeBase.description,
  documents: (knowledgeBase.documents ?? []).map(normalizeDocument),
  createdAt: knowledgeBase.createdAt,
})

const isDegradedFallbackContent = (content: string): boolean => {
  const normalized = content.trim()
  return (
    normalized.startsWith('⚠️ AI 模型调用失败') ||
    normalized.startsWith('⚠ 当前回答为降级回复') ||
    normalized.includes('模型或检索链路出现异常')
  )
}

const normalizeConversation = (conversation: BackendConversation): Conversation => ({
  id: conversation.id,
  title: conversation.title,
  createdAt: conversation.createdAt,
  updatedAt: conversation.updatedAt,
  messages: (conversation.messages ?? []).map((message) => ({
    id: message.id,
    role: message.role,
    content: message.content,
    timestamp: message.createdAt,
    metadata: message.metadata,
  })),
})

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

const extractErrorMessage = async (response: Response) => {
  try {
    const errorBody = (await response.json()) as ApiErrorResponse
    return errorBody.error || '请求失败'
  } catch {
    return '请求失败'
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

  if (task.pendingFiles > 0) {
    parts.push(`未执行 ${task.pendingFiles}`)
  }

  return parts.join(' · ')
}

function App() {
  const [sidebarOpen, setSidebarOpen] = useState(true)
  const [knowledgeBases, setKnowledgeBases] = useState<KnowledgeBase[]>([])
  const [streamingConversationId, setStreamingConversationId] = useState<string | null>(null)
  const [backendReady, setBackendReady] = useState(false)
  const [backendWarmupRequired, setBackendWarmupRequired] = useState(true)
  const [conversations, setConversations] = useState<Conversation[]>(() => {
    const initialConversation = createWelcomeConversation()
    return [initialConversation]
  })
  const [activeConversationId, setActiveConversationId] = useState<string | null>(null)
  const [selectedKnowledgeBaseId, setSelectedKnowledgeBaseId] = useState<string | null>(null)
  const [selectedDocumentId, setSelectedDocumentId] = useState<string | null>(null)
  const [isSettingsOpen, setIsSettingsOpen] = useState(false)
  const [isKnowledgePanelOpen, setIsKnowledgePanelOpen] = useState(false)
  const [directoryUploadTask, setDirectoryUploadTask] = useState<DirectoryUploadTask>(
    createEmptyDirectoryUploadTask,
  )
  const [directoryUploadPendingFiles, setDirectoryUploadPendingFiles] = useState<UploadQueueItem[]>([])
  const directoryUploadCancelRef = useRef(false)
  const chatAbortControllerRef = useRef<AbortController | null>(null)
  const activeChatRequestRef = useRef<{ requestId: string; conversationId: string } | null>(null)

  const loadConversationDetail = async (conversationId: string): Promise<Conversation> => {
    const response = await fetch(`${API_BASE_PATH}/api/conversations/${conversationId}`)
    if (!response.ok) {
      throw new Error(await extractErrorMessage(response))
    }

    return normalizeConversation((await response.json()) as BackendConversation)
  }

  const waitForBackendReady = async (attempts = 12, delayMs = 1500) => {
    for (let index = 0; index < attempts; index += 1) {
      try {
        const response = await fetch(`${API_BASE_PATH}/health`)
        if (response.ok) {
          const health = (await response.json()) as HealthResponse
          if ((health.status ?? '').toLowerCase() === 'ok') {
            setBackendReady(true)
            setBackendWarmupRequired(true)
            return true
          }
        }
      } catch {
        // 忽略启动阶段探活错误，交给下一轮重试
      }

      if (index < attempts - 1) {
        await new Promise((resolve) => {
          window.setTimeout(resolve, delayMs)
        })
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
    }

    if (typeof window === 'undefined') {
      return defaultConfig
    }

    try {
      const cachedConfig = window.localStorage.getItem(AI_CONFIG_STORAGE_KEY)

      if (!cachedConfig) {
        return defaultConfig
      }

      return {
        ...defaultConfig,
        ...(JSON.parse(cachedConfig) as Partial<AppConfig>),
      }
    } catch {
      return defaultConfig
    }
  })

  const persistConfigToBackend = async (nextConfig: AppConfig) => {
    const response = await fetch(`${API_BASE_PATH}/api/config`, {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(nextConfig),
    })

    if (!response.ok) {
      throw new Error(await extractErrorMessage(response))
    }

    const savedConfig = (await response.json()) as ConfigResponse
    setConfig(savedConfig)
    setBackendReady(true)
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
    const bootstrapApp = async () => {
      try {
        const isReady = await waitForBackendReady()
        if (!isReady) {
          throw new Error('后端服务尚未就绪，请稍后刷新页面重试。')
        }

        const [knowledgeBaseResponse, configResponse, conversationsResponse] = await Promise.all([
          fetch(`${API_BASE_PATH}/api/knowledge-bases`),
          fetch(`${API_BASE_PATH}/api/config`),
          fetch(`${API_BASE_PATH}/api/conversations`),
        ])

        if (!knowledgeBaseResponse.ok) {
          throw new Error(await extractErrorMessage(knowledgeBaseResponse))
        }

        if (!configResponse.ok) {
          throw new Error(await extractErrorMessage(configResponse))
        }

        if (!conversationsResponse.ok) {
          throw new Error(await extractErrorMessage(conversationsResponse))
        }

        const knowledgeBaseData =
          (await knowledgeBaseResponse.json()) as KnowledgeBaseListResponse
        const configData = (await configResponse.json()) as ConfigResponse
        const conversationsData =
          (await conversationsResponse.json()) as ConversationListResponse
        const nextKnowledgeBases = knowledgeBaseData.items.map(normalizeKnowledgeBase)
        setKnowledgeBases(nextKnowledgeBases)
        setConfig(configData)
        setSelectedKnowledgeBaseId((current) => current ?? nextKnowledgeBases[0]?.id ?? null)
        setSelectedDocumentId(null)

        const conversationItems = conversationsData.items ?? []
        if (conversationItems.length > 0) {
          const firstConversationId = conversationItems[0].id
          const firstConversationResponse = await fetch(
            `${API_BASE_PATH}/api/conversations/${firstConversationId}`,
          )
          if (!firstConversationResponse.ok) {
            throw new Error(await extractErrorMessage(firstConversationResponse))
          }
          const firstConversation = normalizeConversation(
            (await firstConversationResponse.json()) as BackendConversation,
          )
          const restConversations = conversationItems.slice(1).map((conversation) => ({
            id: conversation.id,
            title: conversation.title,
            createdAt: conversation.createdAt,
            updatedAt: conversation.updatedAt,
            messages: [],
          }))
          setConversations([firstConversation, ...restConversations])
          setActiveConversationId(firstConversation.id)
        }
      } catch (error) {
        setBackendReady(false)
        const message =
          error instanceof Error ? error.message : '初始化知识库失败，请检查后端服务。'

        setConversations((prev) =>
          prev.map((conversation, index) =>
            index === 0
              ? {
                  ...conversation,
                  messages: [
                    ...conversation.messages,
                    {
                      id: createId(),
                      role: 'assistant',
                      content: `知识库初始化失败：${message}`,
                      timestamp: new Date().toISOString(),
                    },
                  ],
                }
              : conversation,
          ),
        )
      }
    }

    void bootstrapApp()
  }, [])

  useEffect(() => {
    if (typeof window === 'undefined') {
      return
    }

    window.localStorage.setItem(AI_CONFIG_STORAGE_KEY, JSON.stringify(config))
  }, [config])

  const cancelActiveChatRequest = () => {
    chatAbortControllerRef.current?.abort()
    chatAbortControllerRef.current = null
    activeChatRequestRef.current = null
    setStreamingConversationId(null)
  }

  const isOllamaSingleFlightMode =
    config.chat.provider === 'ollama' || config.embedding.provider === 'ollama'

  const generatingConversationTitle =
    conversations.find((conversation) => conversation.id === streamingConversationId)?.title ?? '当前会话'

  const handleCreateConversation = () => {
    const conversation = createWelcomeConversation()

    setConversations((prev) => [conversation, ...prev])
    setActiveConversationId(conversation.id)
  }

  const handleSelectConversation = async (conversationId: string) => {
    const existingConversation = conversations.find((conversation) => conversation.id === conversationId)
    if (existingConversation && existingConversation.messages.length > 0) {
      setActiveConversationId(conversationId)
      return
    }

    try {
      const loadedConversation = await loadConversationDetail(conversationId)
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
          : await loadConversationDetail(conversationId)

      const response = await fetch(`${API_BASE_PATH}/api/conversations/${conversationId}`, {
        method: 'PUT',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          id: fullConversation.id,
          title: nextTitle,
          knowledgeBaseId: '',
          documentId: '',
          messages: fullConversation.messages.map((message) => ({
            id: message.id,
            role: message.role,
            content: message.content,
            createdAt: message.timestamp,
            metadata: message.metadata,
          })),
        }),
      })

      if (!response.ok) {
        throw new Error(await extractErrorMessage(response))
      }

      const updatedConversation = normalizeConversation((await response.json()) as BackendConversation)
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
        const response = await fetch(`${API_BASE_PATH}/api/conversations/${conversationId}`, {
          method: 'DELETE',
        })

        if (!response.ok) {
          throw new Error(await extractErrorMessage(response))
        }
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

  const handleCreateKnowledgeBase = async (name: string, description: string) => {
    try {
      const response = await fetch(`${API_BASE_PATH}/api/knowledge-bases`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ name, description }),
      })

      if (!response.ok) {
        throw new Error(await extractErrorMessage(response))
      }

      const createdKnowledgeBase = normalizeKnowledgeBase(
        (await response.json()) as BackendKnowledgeBase,
      )

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
      const response = await fetch(`${API_BASE_PATH}/api/knowledge-bases/${knowledgeBaseId}`, {
        method: 'DELETE',
      })

      if (!response.ok) {
        throw new Error(await extractErrorMessage(response))
      }

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

  const uploadSingleKnowledgeBaseFile = async (knowledgeBaseId: string, file: File) => {
    const formData = new FormData()
    formData.append('file', file)

    const response = await fetch(`${API_BASE_PATH}/api/knowledge-bases/${knowledgeBaseId}/documents`, {
      method: 'POST',
      body: formData,
    })

    if (!response.ok) {
      throw new Error(await extractErrorMessage(response))
    }

    const data = (await response.json()) as UploadResponse
    return normalizeDocument(data.uploaded)
  }

  const appendUploadedDocument = (knowledgeBaseId: string, document: DocumentItem) => {
    setKnowledgeBases((prev) =>
      prev.map((knowledgeBase) =>
        knowledgeBase.id === knowledgeBaseId
          ? {
              ...knowledgeBase,
              documents: [document, ...knowledgeBase.documents],
            }
          : knowledgeBase,
      ),
    )
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
    let firstUploadedDocumentId: string | null = null

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
        const uploaded = await uploadSingleKnowledgeBaseFile(knowledgeBaseId, item.file)
        appendUploadedDocument(knowledgeBaseId, uploaded)
        firstUploadedDocumentId = firstUploadedDocumentId ?? uploaded.id

        setDirectoryUploadTask((prev) => ({
          ...prev,
          successFiles: prev.successFiles + 1,
          processedFiles: prev.processedFiles + 1,
          pendingFiles: Math.max(queue.length - index - 1, 0),
        }))
      } catch (error) {
        const reason = error instanceof Error ? error.message : '上传文档失败，请稍后重试。'
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
    if (firstUploadedDocumentId) {
      setSelectedDocumentId((current) => current ?? firstUploadedDocumentId)
    }

    setDirectoryUploadTask((prev) => {
      let status: DirectoryUploadStatus = 'done'

      if (directoryUploadCancelRef.current) {
        status = 'canceled'
      } else if (prev.successFiles === 0 && prev.failedFiles > 0) {
        status = 'failed'
      } else if (prev.failedFiles > 0) {
        status = 'partial-failed'
      }

      const nextTask: DirectoryUploadTask = {
        ...prev,
        status,
        currentFileName: '',
        currentFilePath: '',
        pendingFiles: nextPendingQueue.length,
      }

      return {
        ...nextTask,
        summaryMessage: buildDirectoryUploadSummary(nextTask),
      }
    })
  }

  const handleUploadFiles = async (knowledgeBaseId: string, files: FileList | null) => {
    if (!files || files.length === 0) {
      return
    }

    try {
      const uploadedDocuments: DocumentItem[] = []

      for (const file of Array.from(files)) {
        const uploaded = await uploadSingleKnowledgeBaseFile(knowledgeBaseId, file)
        uploadedDocuments.push(uploaded)
      }

      setKnowledgeBases((prev) =>
        prev.map((knowledgeBase) =>
          knowledgeBase.id === knowledgeBaseId
            ? {
                ...knowledgeBase,
                documents: [...uploadedDocuments, ...knowledgeBase.documents],
              }
            : knowledgeBase,
        ),
      )

      setSelectedKnowledgeBaseId(knowledgeBaseId)
      setSelectedDocumentId(uploadedDocuments[0]?.id ?? null)
    } catch (error) {
      const message =
        error instanceof Error ? error.message : '上传文档失败，请稍后重试。'
      window.alert(`上传文档失败：${message}`)
    }
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
      currentFileName: '',
      currentFilePath: '',
      failedItems: [],
      skippedItems,
      summaryMessage: '',
    }

    setDirectoryUploadTask({
      ...scannedTask,
      summaryMessage:
        eligibleItems.length === 0 ? '所选目录中没有可上传的 .txt、.md、.pdf、.csv 或 .xlsx 文件。' : '',
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
      const response = await fetch(
        `${API_BASE_PATH}/api/knowledge-bases/${knowledgeBaseId}/documents/${documentId}`,
        {
          method: 'DELETE',
        },
      )

      if (!response.ok) {
        throw new Error(await extractErrorMessage(response))
      }

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
                content: '后端服务正在启动或尚未就绪，请稍后再试。若刚刚重启服务，建议等待健康检查完成后再发送问题。',
                timestamp: now,
              },
            ],
            updatedAt: now,
          }
        }),
      )
      return
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
    const requestBody: ChatRequestBody = {
      conversationId,
      model: config.chat.model,
      knowledgeBaseId: selectedKnowledgeBaseId ?? '',
      documentId: selectedDocumentId ?? '',
      config: config.chat,
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
      const fallbackResponse = await withTimeout(
        fetch(`${API_BASE_PATH}/v1/chat/completions`, {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
          },
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

      const data = (await fallbackResponse.json()) as ChatCompletionResponse
      finalizeAssistantMessage(
        data.choices[0]?.message?.content || '后端未返回有效回答。',
        data.metadata
          ? {
              degraded: data.metadata.degraded,
              fallbackStrategy: data.metadata.fallbackStrategy,
              upstreamError: data.metadata.upstreamError,
            }
          : undefined,
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
        streamResponse = await fetch(`${API_BASE_PATH}/v1/chat/completions/stream`, {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
            Accept: 'text/event-stream',
          },
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
      let firstChunkTimer = window.setTimeout(() => {
        streamAbortController.abort()
      }, STREAM_FIRST_CHUNK_TIMEOUT_MS)
      let requestTimer = window.setTimeout(() => {
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
            payload.metadata ??
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
        while (true) {
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

  const handleChatConfigChange = <K extends keyof ChatConfig>(
    key: K,
    value: ChatConfig[K],
  ) => {
    setConfig((prev) => {
      const nextConfig = {
        ...prev,
        chat: {
          ...prev.chat,
          [key]:
            key === 'contextMessageLimit'
              ? Math.max(1, Math.min(100, Number(value) || 1))
              : value,
        },
      }
      void persistConfigToBackend(nextConfig)
      return nextConfig
    })
  }

  const handleEmbeddingConfigChange = <K extends keyof EmbeddingConfig>(
    key: K,
    value: EmbeddingConfig[K],
  ) => {
    setConfig((prev) => {
      const nextConfig = {
        ...prev,
        embedding: {
          ...prev.embedding,
          [key]: value,
        },
      }
      void persistConfigToBackend(nextConfig)
      return nextConfig
    })
  }

  const handleToggleSettings = () => {
    setIsSettingsOpen((prev) => {
      const next = !prev
      if (next) {
        setIsKnowledgePanelOpen(false)
      }
      return next
    })
  }

  const handleToggleKnowledgePanel = () => {
    setIsKnowledgePanelOpen((prev) => {
      const next = !prev
      if (next) {
        setIsSettingsOpen(false)
      }
      return next
    })
  }

  return (
    <div className="chat-page">
      <Sidebar
        isOpen={sidebarOpen}
        onToggle={() => setSidebarOpen(!sidebarOpen)}
        knowledgeBases={knowledgeBases}
        selectedKnowledgeBaseId={selectedKnowledgeBase?.id ?? null}
        selectedDocumentId={selectedDocumentId}
        onSelectKnowledgeBase={handleSelectKnowledgeBase}
        onSelectDocument={handleSelectDocument}
        onCreateKnowledgeBase={handleCreateKnowledgeBase}
        onDeleteKnowledgeBase={handleDeleteKnowledgeBase}
        onUploadFiles={handleUploadFiles}
        onUploadDirectory={handleUploadDirectory}
        directoryUploadTask={directoryUploadTask}
        onCancelDirectoryUpload={handleCancelDirectoryUpload}
        onContinueDirectoryUpload={handleContinueDirectoryUpload}
        onRemoveDocument={handleRemoveDocument}
        conversations={conversations}
        activeConversationId={activeConversation?.id ?? null}
        onSelectConversation={handleSelectConversation}
        onCreateConversation={handleCreateConversation}
        onRenameConversation={handleRenameConversation}
        onDeleteConversation={handleDeleteConversation}
        config={config}
        isSettingsOpen={isSettingsOpen}
        isKnowledgePanelOpen={isKnowledgePanelOpen}
        onToggleSettings={handleToggleSettings}
        onToggleKnowledgePanel={handleToggleKnowledgePanel}
        onChatConfigChange={handleChatConfigChange}
        onEmbeddingConfigChange={handleEmbeddingConfigChange}
      />
      <ChatArea
        sidebarOpen={sidebarOpen}
        activeConversation={activeConversation}
        selectedKnowledgeBase={selectedKnowledgeBase}
        selectedDocument={selectedDocument}
        config={config}
        isLoading={streamingConversationId === activeConversation?.id}
        isGlobalGenerating={Boolean(streamingConversationId)}
        generatingConversationTitle={generatingConversationTitle}
        enforceSingleFlight={isOllamaSingleFlightMode}
        onSendMessage={handleSendMessage}
        onClearConversation={handleClearConversation}
      />
    </div>
  )
}

export default App
