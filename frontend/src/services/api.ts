import type {
  AppConfig,
  ChatMessage,
  Conversation,
  DocumentItem,
  KnowledgeBase,
  MCPConfig,
} from '../App'

export const API_BASE_PATH = ''
export const AUTH_UNAUTHORIZED_EVENT = 'ai-localbase:auth-unauthorized'
export const CSRF_HEADER_NAME = 'X-CSRF-Token'

export interface ApiErrorResponse {
  error?: string | {
    code?: string
    message?: string
    requestId?: string
  }
}

export interface HealthResponse {
  status?: string
  config?: Record<string, string>
}

export interface AuthBootstrapResponse {
  auth_enabled: boolean
  setup_required: boolean
  setup_token_required: boolean
  username: string
}

export interface AuthLoginResponse {
  expires_at: number
  username: string
}

export interface AuthStatusResponse {
  authenticated: boolean
  auth_enabled?: boolean
  username?: string
  userId?: string
  sessionId?: string
  authType?: string
  expires_at?: number
}

export interface AuthSessionInfo {
  id: string
  createdAt: string
  expiresAt: string
  lastSeenAt: string
  revokedAt?: string
  userAgent?: string
  ip?: string
  current?: boolean
}

export interface AuthAPIKeyInfo {
  id: string
  name: string
  prefix: string
  scopes: string[]
  createdAt: string
  lastUsedAt?: string
  revokedAt?: string
}

export interface CreatedAuthAPIKey {
  item: AuthAPIKeyInfo
  token: string
}

export interface SecurityEventInfo {
  id: string
  type: string
  username?: string
  ip?: string
  userAgent?: string
  createdAt: string
  message?: string
}

export interface BackendDocumentItem {
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

export interface BackendKnowledgeBase {
  id: string
  name: string
  description: string
  documents: BackendDocumentItem[]
  createdAt: string
}

export interface KnowledgeBaseListResponse {
  items: BackendKnowledgeBase[]
}

export interface ConfigResponse {
  chat: AppConfig['chat']
  embedding: AppConfig['embedding']
  mcp: MCPConfig
  retrieval: AppConfig['retrieval']
}

export interface TestModelResponse {
  success: boolean
  latency_ms?: number
  error_message?: string
  vector_size?: number
  model_info?: string
}

export interface ComponentHealthResponse {
  status: 'ok' | 'error' | 'not_configured' | 'warning' | string
  message?: string
  latency_ms?: number
  error_message?: string
}

export interface HealthSummaryResponse {
  qdrant: ComponentHealthResponse
  chat_model: ComponentHealthResponse
  embedding_model: ComponentHealthResponse
  storage: ComponentHealthResponse
  auth: ComponentHealthResponse
}

export interface BackendConversationListItem {
  id: string
  title: string
  knowledgeBaseId: string
  documentId: string
  createdAt: string
  updatedAt: string
  messageCount: number
}

export interface ConversationListResponse {
  items: BackendConversationListItem[]
}

export interface BackendConversation {
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
    metadata?: ChatMessage['metadata']
  }>
}

export interface UploadResponse {
  uploaded: BackendDocumentItem
}

export interface DocumentChunkPreview {
  id: string
  index: number
  kind: string
  text: string
}

export interface DocumentIndexDiagnostics {
  rawContentChars: number
  chunkCount: number
  vectorCount: number
  summaryChunkCount: number
  structuredRowCount: number
  rawContentAvailable: boolean
  qdrantEnabled: boolean
  rawContentTruncated: boolean
  chunkPreviewTruncated: boolean
}

export interface DocumentDetailResponse {
  knowledgeBaseId: string
  document: BackendDocumentItem
  diagnostics: DocumentIndexDiagnostics
  rawContent: string
  summary: string
  chunks: DocumentChunkPreview[]
}

export interface KnowledgeBaseHealthMetrics {
  documentCount: number
  indexedCount: number
  processingCount: number
  failedCount: number
  emptyContentCount: number
  chunkCount: number
  vectorCount: number
  summaryChunkCount: number
  structuredRowCount: number
  rawContentChars: number
  qdrantEnabled: boolean
  lastIndexedAt?: string
}

export interface KnowledgeBaseDocumentHealth {
  documentId: string
  documentName: string
  status: string
  indexedAt?: string
  indexError?: string
  chunkCount: number
  vectorCount: number
  summaryChunkCount: number
  structuredRowCount: number
  rawContentChars: number
  rawContentAvailable: boolean
  needsReindex: boolean
  recommendation?: string
}

export interface KnowledgeBaseHealthResponse {
  knowledgeBaseId: string
  name: string
  status: 'healthy' | 'warning' | 'attention' | 'empty'
  score: number
  metrics: KnowledgeBaseHealthMetrics
  recommendations: string[]
  documents: KnowledgeBaseDocumentHealth[]
}

export interface ReindexDocumentResponse {
  message: string
  knowledgeBaseId: string
  document: BackendDocumentItem
}

export interface RetrievalDebugChunk {
  id: string
  knowledgeBaseId: string
  documentId: string
  documentName: string
  index: number
  kind: string
  score: number
  text: string
  matchReasons?: string[]
  retrievalChannels?: string[]
  denseRank?: number
  sparseRank?: number
}

export interface RetrievalDebugTraceStep {
  stage: string
  status: string
  reason?: string
  inputCount?: number
  outputCount?: number
}

export interface RetrievalDebugConfidence {
  status: string
  summary: string
  reasons?: string[]
  suggestions?: string[]
  topScore: number
  averageScore: number
  evidenceCoverage: number
}

export interface RetrievalEvidenceGateDiagnostic {
  enabled: boolean
  reason?: string
  candidateCount: number
  selectedCount: number
  directEvidenceCount: number
  weakEvidenceCount: number
  removedCount: number
  topBefore?: RetrievalDebugChunk[]
  topAfter?: RetrievalDebugChunk[]
}

export interface EvalGroundTruthCase {
  id: string
  question: string
  answer: string
  answer_snippets: string[]
  source_documents: Array<{
    knowledge_base_id: string
    document_id: string
    chunk_id: string
  }>
  answer_type: string
  difficulty: string
  review_status?: string
  disabled?: boolean
  notes?: string
}

export interface RetrievalDebugResponse {
  query: string
  knowledgeBaseId?: string
  documentId?: string
  searchMode: string
  rerankStrategy: string
  queryRewriteUsed: boolean
  structuredIntent?: string
  targetField?: string
  deterministicUsed: boolean
  elapsedMs: number
  count: number
  lowConfidence: boolean
  confidence: RetrievalDebugConfidence
  evidenceGate?: RetrievalEvidenceGateDiagnostic
  contextPreview: string
  sources: Array<Record<string, string>>
  evalCandidate?: EvalGroundTruthCase
  trace?: RetrievalDebugTraceStep[]
  items: RetrievalDebugChunk[]
}

export type RetrievalSearchMode = 'auto' | 'dense' | 'hybrid'
export type RetrievalRerankStrategy = 'keyword' | 'semantic'

export interface EvalRunOptions {
  searchMode?: RetrievalSearchMode
  rerankStrategy?: RetrievalRerankStrategy
  enableQueryRewrite?: boolean
  queryRewriteMaxVariants?: number
}

export interface GenerateEvalDatasetResponse {
  datasetId?: string
  knowledgeBaseId?: string
  documentId?: string
  count: number
  documentCount: number
  createdAt?: string
  items: EvalGroundTruthCase[]
}

export interface EvalDatasetSummary {
  id: string
  name: string
  kind?: string
  knowledgeBaseId?: string
  documentId?: string
  count: number
  documentCount: number
  createdAt: string
  updatedAt?: string
}

export interface EvalDatasetListResponse {
  items: EvalDatasetSummary[]
}

export interface EvalDatasetDetail {
  id: string
  name: string
  kind?: string
  knowledgeBaseId?: string
  documentId?: string
  count: number
  documentCount: number
  createdAt: string
  updatedAt?: string
  items: EvalGroundTruthCase[]
}

export interface AddEvalDatasetCandidateResponse {
  dataset: EvalDatasetSummary
  item: EvalGroundTruthCase
  created: boolean
}

export interface UpdateEvalDatasetItemResponse {
  dataset: EvalDatasetSummary
  item: EvalGroundTruthCase
}

export interface DeleteEvalDatasetItemResponse {
  dataset: EvalDatasetSummary
  deleted: string
}

export interface EvalRunMetrics {
  totalCases: number
  hitCount: number
  missCount: number
  hitRate: number
  mrr: number
  latencyP50Ms: number
  latencyP95Ms: number
  lowConfidence: number
  errorCount: number
  skippedDisabled: number
  evidenceSupportedCount: number
  evidenceSupportRate: number
  citationMismatchCount: number
  directEvidenceHitCount: number
  directEvidenceHitRate: number
}

export interface EvalRunCaseResult {
  caseId: string
  question: string
  expectedAnswer: string
  hit: boolean
  hitRank: number
  reciprocalRank: number
  matchedBy?: string
  elapsedMs: number
  lowConfidence: boolean
  confidence?: RetrievalDebugConfidence
  evidenceSupport: boolean
  evidenceIssue?: string
  directEvidence: boolean
  error?: string
  retrieved: RetrievalDebugChunk[]
}

export interface RunEvalDatasetResponse {
  runId: string
  datasetId: string
  datasetName: string
  knowledgeBaseId?: string
  documentId?: string
  searchMode: string
  rerankStrategy: string
  queryRewriteUsed: boolean
  startedAt: string
  elapsedMs: number
  metrics: EvalRunMetrics
  cases: EvalRunCaseResult[]
}

export type EvalRunSummary = Omit<RunEvalDatasetResponse, 'cases'>

export interface EvalRunListResponse {
  items: EvalRunSummary[]
}

export const normalizeDocument = (document: BackendDocumentItem): DocumentItem => ({
  id: document.id,
  name: document.name,
  size: document.size,
  sizeLabel: document.sizeLabel,
  uploadedAt: document.uploadedAt,
  status: document.status,
  contentPreview: document.contentPreview,
  chunkCount: document.chunkCount,
  indexedAt: document.indexedAt,
  indexError: document.indexError,
})

export const normalizeKnowledgeBase = (knowledgeBase: BackendKnowledgeBase): KnowledgeBase => ({
  id: knowledgeBase.id,
  name: knowledgeBase.name,
  description: knowledgeBase.description,
  documents: (knowledgeBase.documents ?? []).map(normalizeDocument),
  createdAt: knowledgeBase.createdAt,
})

export const normalizeConversation = (conversation: BackendConversation): Conversation => ({
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

export const parseJsonResponse = async <T>(response: Response): Promise<T | null> => {
  const text = await response.text()
  if (!text.trim()) {
    return null
  }

  return JSON.parse(text) as T
}

export const extractErrorMessage = async (response: Response) => {
  try {
    const errorBody = await parseJsonResponse<ApiErrorResponse>(response)
    if (!errorBody) {
      return response.statusText || '请求失败'
    }
    if (typeof errorBody.error === 'string') {
      return errorBody.error || '请求失败'
    }
    return errorBody.error?.message || '请求失败'
  } catch {
    return '请求失败'
  }
}

const readCookie = (name: string) => {
  if (typeof document === 'undefined') return ''
  const match = document.cookie
    .split('; ')
    .find((part) => part.startsWith(`${name}=`))
  return match ? decodeURIComponent(match.slice(name.length + 1)) : ''
}

export const applyCSRFHeader = (headers: Headers, init?: RequestInit) => {
  const method = (init?.method || 'GET').toUpperCase()
  if (!['POST', 'PUT', 'PATCH', 'DELETE'].includes(method)) return
  if (headers.has('Authorization')) return
  const csrfToken = readCookie('ai_localbase_csrf')
  if (csrfToken) {
    headers.set(CSRF_HEADER_NAME, csrfToken)
  }
}

async function requestJson<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers)
  applyCSRFHeader(headers, init)

  const response = await fetch(`${API_BASE_PATH}${path}`, {
    ...init,
    credentials: init?.credentials ?? 'same-origin',
    headers,
  })

  if (response.status === 401) {
    clearStoredAuth()
    throw new Error('未授权，请重新登录')
  }

  if (!response.ok) {
    throw new Error(await extractErrorMessage(response))
  }

  const data = await parseJsonResponse<T>(response)
  if (data === null) {
    throw new Error('后端返回了空响应')
  }

  return data
}

async function requestOk(path: string, init?: RequestInit): Promise<void> {
  const headers = new Headers(init?.headers)
  applyCSRFHeader(headers, init)

  const response = await fetch(`${API_BASE_PATH}${path}`, {
    ...init,
    credentials: init?.credentials ?? 'same-origin',
    headers,
  })

  if (response.status === 401) {
    clearStoredAuth()
    throw new Error('未授权，请重新登录')
  }

  if (!response.ok) {
    throw new Error(await extractErrorMessage(response))
  }
}

export const clearStoredAuth = () => {
  localStorage.removeItem('auth_token')
  localStorage.removeItem('token_expires_at')
  localStorage.removeItem('auth_expires_at')
  localStorage.removeItem('auth_username')
  window.dispatchEvent(new Event(AUTH_UNAUTHORIZED_EVENT))
}

const jsonRequest = (body: unknown, init: RequestInit = {}): RequestInit => ({
  ...init,
  headers: {
    'Content-Type': 'application/json',
    ...init.headers,
  },
  body: JSON.stringify(body),
})

const serializeConversation = (conversation: Conversation, title = conversation.title) => ({
  id: conversation.id,
  title,
  knowledgeBaseId: '',
  documentId: '',
  messages: conversation.messages.map((message) => ({
    id: message.id,
    role: message.role,
    content: message.content,
    createdAt: message.timestamp,
    metadata: message.metadata,
  })),
})

export const fetchBackendHealth = async (): Promise<HealthResponse | null> => {
  try {
    const response = await fetch(`${API_BASE_PATH}/health`)
    if (!response.ok) {
      return null
    }

    return await parseJsonResponse<HealthResponse>(response)
  } catch {
    return null
  }
}

export const fetchAuthBootstrap = async (): Promise<AuthBootstrapResponse> => {
  const response = await fetch(`${API_BASE_PATH}/api/auth/bootstrap`, {
    credentials: 'same-origin',
  })
  if (!response.ok) {
    throw new Error(await extractErrorMessage(response))
  }

  const data = await parseJsonResponse<AuthBootstrapResponse>(response)
  if (!data) {
    throw new Error('认证初始化接口返回空响应')
  }
  return data
}

export const setupAuth = async (payload: {
  username: string
  password: string
  setupToken?: string
}): Promise<AuthLoginResponse> => {
  const headers = new Headers({ 'Content-Type': 'application/json' })
  applyCSRFHeader(headers, { method: 'POST' })
  const response = await fetch(`${API_BASE_PATH}/api/auth/setup`, {
    method: 'POST',
    credentials: 'same-origin',
    headers,
    body: JSON.stringify(payload),
  })
  if (!response.ok) {
    throw new Error(await extractErrorMessage(response))
  }

  const data = await parseJsonResponse<AuthLoginResponse>(response)
  if (!data?.expires_at) {
    throw new Error('初始化接口返回空响应')
  }
  return data
}

export const loginAuth = async (payload: {
  username: string
  password: string
}): Promise<AuthLoginResponse> => {
  const headers = new Headers({ 'Content-Type': 'application/json' })
  applyCSRFHeader(headers, { method: 'POST' })
  const response = await fetch(`${API_BASE_PATH}/api/auth/login`, {
    method: 'POST',
    credentials: 'same-origin',
    headers,
    body: JSON.stringify(payload),
  })
  if (!response.ok) {
    throw new Error(await extractErrorMessage(response))
  }

  const data = await parseJsonResponse<AuthLoginResponse>(response)
  if (!data?.expires_at) {
    throw new Error('登录接口返回空响应')
  }
  return data
}

export const fetchAuthStatus = async (): Promise<AuthStatusResponse> => (
  requestJson<AuthStatusResponse>('/api/auth/status')
)

export const logoutAuth = async (): Promise<void> => (
  requestOk('/api/auth/logout', { method: 'POST' })
)

export const logoutAllAuth = async (): Promise<void> => (
  requestOk('/api/auth/logout-all', { method: 'POST' })
)

export const changeAuthPassword = async (payload: {
  currentPassword: string
  newPassword: string
}): Promise<void> => (
  requestOk('/api/auth/change-password', jsonRequest(payload, { method: 'POST' }))
)

export const fetchAuthSessions = async (): Promise<AuthSessionInfo[]> => {
  const data = await requestJson<{ items: AuthSessionInfo[] }>('/api/auth/sessions')
  return data.items ?? []
}

export const fetchAuthAPIKeys = async (): Promise<AuthAPIKeyInfo[]> => {
  const data = await requestJson<{ items: AuthAPIKeyInfo[] }>('/api/auth/api-keys')
  return data.items ?? []
}

export const createAuthAPIKey = async (payload: {
  name: string
  scopes?: string[]
}): Promise<CreatedAuthAPIKey> => (
  requestJson<CreatedAuthAPIKey>('/api/auth/api-keys', jsonRequest(payload, { method: 'POST' }))
)

export const revokeAuthAPIKey = async (id: string): Promise<void> => (
  requestOk(`/api/auth/api-keys/${encodeURIComponent(id)}`, { method: 'DELETE' })
)

export const fetchSecurityEvents = async (limit = 50): Promise<SecurityEventInfo[]> => {
  const data = await requestJson<{ items: SecurityEventInfo[] }>(`/api/auth/security-events?limit=${limit}`)
  return data.items ?? []
}

export const fetchHealthSummary = async (): Promise<HealthSummaryResponse> => (
  requestJson<HealthSummaryResponse>('/api/config/health-summary')
)

export const fetchInitialAppData = async () => {
  const [knowledgeBaseData, configData, conversationsData] = await Promise.all([
    requestJson<KnowledgeBaseListResponse>('/api/knowledge-bases'),
    requestJson<ConfigResponse>('/api/config'),
    requestJson<ConversationListResponse>('/api/conversations'),
  ])

  return {
    knowledgeBases: knowledgeBaseData.items.map(normalizeKnowledgeBase),
    config: configData,
    conversations: conversationsData.items ?? [],
  }
}

export const updateAppConfig = async (nextConfig: AppConfig): Promise<AppConfig> => (
  requestJson<ConfigResponse>('/api/config', jsonRequest(nextConfig, { method: 'PUT' }))
)

export const resetMcpToken = async (): Promise<MCPConfig> => {
  const payload = await requestJson<{ mcp: MCPConfig }>('/api/config/mcp/reset-token', {
    method: 'POST',
  })
  return payload.mcp
}

export const testChatModelConfig = async (
  config: AppConfig['chat'],
): Promise<TestModelResponse> => (
  requestJson<TestModelResponse>(
    '/api/config/test-chat-model',
    jsonRequest(config, { method: 'POST' }),
  )
)

export const testEmbeddingModelConfig = async (
  config: AppConfig['embedding'],
): Promise<TestModelResponse> => (
  requestJson<TestModelResponse>(
    '/api/config/test-embedding-model',
    jsonRequest(config, { method: 'POST' }),
  )
)

export const fetchConversationDetail = async (conversationId: string): Promise<Conversation> => (
  normalizeConversation(await requestJson<BackendConversation>(`/api/conversations/${conversationId}`))
)

export const saveConversation = async (
  conversation: Conversation,
  title = conversation.title,
): Promise<Conversation> => (
  normalizeConversation(
    await requestJson<BackendConversation>(
      `/api/conversations/${conversation.id}`,
      jsonRequest(serializeConversation(conversation, title), { method: 'PUT' }),
    ),
  )
)

export const deleteConversation = async (conversationId: string): Promise<void> => {
  await requestOk(`/api/conversations/${conversationId}`, { method: 'DELETE' })
}

export const createKnowledgeBase = async (
  name: string,
  description: string,
): Promise<KnowledgeBase> => (
  normalizeKnowledgeBase(
    await requestJson<BackendKnowledgeBase>(
      '/api/knowledge-bases',
      jsonRequest({ name, description }, { method: 'POST' }),
    ),
  )
)

export const deleteKnowledgeBase = async (knowledgeBaseId: string): Promise<void> => {
  await requestOk(`/api/knowledge-bases/${knowledgeBaseId}`, { method: 'DELETE' })
}

export const uploadKnowledgeBaseFile = async (
  knowledgeBaseId: string,
  file: File,
): Promise<DocumentItem> => {
  const formData = new FormData()
  formData.append('file', file)

  const data = await requestJson<UploadResponse>(
    `/api/knowledge-bases/${knowledgeBaseId}/documents`,
    {
      method: 'POST',
      body: formData,
    },
  )

  return normalizeDocument(data.uploaded)
}

export const deleteKnowledgeBaseDocument = async (
  knowledgeBaseId: string,
  documentId: string,
): Promise<void> => {
  await requestOk(`/api/knowledge-bases/${knowledgeBaseId}/documents/${documentId}`, {
    method: 'DELETE',
  })
}

export const fetchKnowledgeBaseDocumentDetail = async (
  knowledgeBaseId: string,
  documentId: string,
  focusChunkId?: string,
): Promise<DocumentDetailResponse> => (
  requestJson<DocumentDetailResponse>(
    `/api/knowledge-bases/${knowledgeBaseId}/documents/${documentId}${
      focusChunkId ? `?focusChunkId=${encodeURIComponent(focusChunkId)}` : ''
    }`,
  )
)

export const fetchKnowledgeBaseHealth = async (
  knowledgeBaseId: string,
): Promise<KnowledgeBaseHealthResponse> => (
  requestJson<KnowledgeBaseHealthResponse>(`/api/knowledge-bases/${knowledgeBaseId}/health`)
)

export const reindexKnowledgeBaseDocument = async (
  knowledgeBaseId: string,
  documentId: string,
): Promise<DocumentItem> => {
  const response = await requestJson<ReindexDocumentResponse>(
    `/api/knowledge-bases/${knowledgeBaseId}/documents/${documentId}/reindex`,
    { method: 'POST' },
  )
  return normalizeDocument(response.document)
}

export const generateEvalDataset = async (
  knowledgeBaseId: string,
  maxPerDocument = 5,
): Promise<GenerateEvalDatasetResponse> => (
  requestJson<GenerateEvalDatasetResponse>(
    '/api/eval/datasets/generate',
    jsonRequest({ knowledgeBaseId, maxPerDocument }, { method: 'POST' }),
  )
)

export const listEvalDatasets = async (
  knowledgeBaseId?: string,
): Promise<EvalDatasetListResponse> => {
  const query = knowledgeBaseId ? `?knowledgeBaseId=${encodeURIComponent(knowledgeBaseId)}` : ''
  return requestJson<EvalDatasetListResponse>(`/api/eval/datasets${query}`)
}

export const getEvalDataset = async (
  datasetId: string,
): Promise<EvalDatasetDetail> => (
  requestJson<EvalDatasetDetail>(`/api/eval/datasets/${datasetId}`)
)

export const deleteEvalDataset = async (datasetId: string): Promise<void> => {
  await requestOk(`/api/eval/datasets/${datasetId}`, { method: 'DELETE' })
}

export const addEvalDatasetCandidate = async (
  knowledgeBaseId: string,
  documentId: string | null | undefined,
  item: EvalGroundTruthCase,
): Promise<AddEvalDatasetCandidateResponse> => (
  requestJson<AddEvalDatasetCandidateResponse>(
    '/api/eval/datasets/review-candidates',
    jsonRequest({ knowledgeBaseId, documentId: documentId ?? '', item }, { method: 'POST' }),
  )
)

export const updateEvalDatasetItem = async (
  datasetId: string,
  itemId: string,
  item: EvalGroundTruthCase,
): Promise<UpdateEvalDatasetItemResponse> => (
  requestJson<UpdateEvalDatasetItemResponse>(
    `/api/eval/datasets/${datasetId}/items/${encodeURIComponent(itemId)}`,
    jsonRequest({ item }, { method: 'PUT' }),
  )
)

export const deleteEvalDatasetItem = async (
  datasetId: string,
  itemId: string,
): Promise<DeleteEvalDatasetItemResponse> => (
  requestJson<DeleteEvalDatasetItemResponse>(
    `/api/eval/datasets/${datasetId}/items/${encodeURIComponent(itemId)}`,
    { method: 'DELETE' },
  )
)

export const runEvalDataset = async (
  datasetId: string,
  options: RetrievalSearchMode | EvalRunOptions = 'auto',
): Promise<RunEvalDatasetResponse> => (
  requestJson<RunEvalDatasetResponse>(`/api/eval/datasets/${datasetId}/runs`, jsonRequest({
    includeDisabled: false,
    topK: 12,
    ...(typeof options === 'string' ? { searchMode: options } : options),
  }, { method: 'POST' }))
)

export const listEvalRuns = async (
  knowledgeBaseId?: string,
  datasetId?: string,
): Promise<EvalRunListResponse> => {
  const params = new URLSearchParams()
  if (knowledgeBaseId) params.set('knowledgeBaseId', knowledgeBaseId)
  if (datasetId) params.set('datasetId', datasetId)
  const query = params.toString()
  return requestJson<EvalRunListResponse>(`/api/eval/runs${query ? `?${query}` : ''}`)
}

export const debugKnowledgeBaseRetrieval = async (
  knowledgeBaseId: string,
  query: string,
  documentId?: string | null,
  searchMode: RetrievalSearchMode = 'auto',
): Promise<RetrievalDebugResponse> => (
  requestJson<RetrievalDebugResponse>(
    `/api/knowledge-bases/${knowledgeBaseId}/retrieval/debug`,
    jsonRequest({ query, documentId: documentId ?? '', topK: 12, searchMode }, { method: 'POST' }),
  )
)

export interface StageUploadResponse {
  uploadId: string
  fileName: string
  filePath: string
  status: string
}

export interface BatchIndexRequest {
  uploadIds: string[]
}

export interface BatchIndexResult {
  uploadId: string
  documentId?: string
  fileName: string
  success: boolean
  error?: string
  document?: BackendDocumentItem
}

export interface BatchIndexResponse {
  total: number
  successful: number
  failed: number
  results: BatchIndexResult[]
  duration_ms: number
}

export interface DocumentIndexStatusResponse {
  documentId: string
  status: 'indexed' | 'ready' | 'processing' | 'failed'
  indexedAt?: string
  indexError?: string
}

export const stageUpload = async (file: File): Promise<StageUploadResponse> => {
  const formData = new FormData()
  formData.append('file', file)

  return requestJson<StageUploadResponse>('/api/uploads', {
    method: 'POST',
    body: formData,
  })
}

export const batchIndexDocuments = async (
  knowledgeBaseId: string,
  uploadIds: string[],
  concurrency?: number,
): Promise<BatchIndexResponse> => (
  requestJson<BatchIndexResponse>(
    `/api/knowledge-bases/${knowledgeBaseId}/documents/batch-index`,
    jsonRequest({ uploadIds, concurrency }, { method: 'POST' }),
  )
)

export const getDocumentIndexStatus = async (
  knowledgeBaseId: string,
  documentId: string,
): Promise<DocumentIndexStatusResponse> => (
  requestJson<DocumentIndexStatusResponse>(
    `/api/knowledge-bases/${knowledgeBaseId}/documents/${documentId}/index-status`,
  )
)

export interface EditMessageRequest {
  messageId: string
  content: string
}

export interface EditMessageResponse {
  conversation: BackendConversation
}

type MessageMutationResponse = BackendConversation | {
  conversation: BackendConversation
}

const normalizeMessageMutationResponse = (response: MessageMutationResponse): Conversation => (
  normalizeConversation('conversation' in response ? response.conversation : response)
)

export const editMessage = async (
  conversationId: string,
  messageId: string,
  newContent: string,
): Promise<Conversation> => {
  const response = await requestJson<MessageMutationResponse>(
    `/api/conversations/${conversationId}/messages/${messageId}`,
    jsonRequest({ content: newContent }, { method: 'PUT' }),
  )
  return normalizeMessageMutationResponse(response)
}

export interface RegenerateMessageRequest {
  messageId: string
}

export interface RegenerateMessageResponse {
  conversation: BackendConversation
}

export const regenerateMessage = async (
  conversationId: string,
  messageId: string,
): Promise<Conversation> => {
  const response = await requestJson<RegenerateMessageResponse>(
    `/api/conversations/${conversationId}/messages/${messageId}/regenerate`,
    jsonRequest({}, { method: 'POST' }),
  )
  return normalizeConversation(response.conversation)
}

export interface DeleteMessageResponse {
  conversation: BackendConversation
}

export const deleteMessage = async (
  conversationId: string,
  messageId: string,
): Promise<Conversation> => {
  const response = await requestJson<MessageMutationResponse>(
    `/api/conversations/${conversationId}/messages/${messageId}`,
    { method: 'DELETE' },
  )
  return normalizeMessageMutationResponse(response)
}

export const exportConversation = async (
  conversationId: string,
  format: 'markdown' = 'markdown',
): Promise<string> => {
  const response = await fetch(
    `${API_BASE_PATH}/api/conversations/${conversationId}/export?format=${encodeURIComponent(format)}`,
    { credentials: 'same-origin' },
  )

  if (response.status === 401) {
    clearStoredAuth()
    throw new Error('未授权，请重新登录')
  }

  if (!response.ok) {
    throw new Error(await extractErrorMessage(response))
  }

  return response.text()
}
