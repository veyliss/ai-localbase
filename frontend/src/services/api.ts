import type {
  AppConfig,
  ChatMessage,
  Conversation,
  DocumentItem,
  KnowledgeBase,
  MCPConfig,
} from '../App'

export const API_BASE_PATH = ''

export interface ApiErrorResponse {
  error?: string | {
    code?: string
    message?: string
    requestId?: string
  }
}

export interface HealthResponse {
  status?: string
}

export interface BackendDocumentItem {
  id: string
  name: string
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

export const extractErrorMessage = async (response: Response) => {
  try {
    const errorBody = (await response.json()) as ApiErrorResponse
    if (typeof errorBody.error === 'string') {
      return errorBody.error || '请求失败'
    }
    return errorBody.error?.message || '请求失败'
  } catch {
    return '请求失败'
  }
}

async function requestJson<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${API_BASE_PATH}${path}`, init)
  if (!response.ok) {
    throw new Error(await extractErrorMessage(response))
  }

  return (await response.json()) as T
}

async function requestOk(path: string, init?: RequestInit): Promise<void> {
  const response = await fetch(`${API_BASE_PATH}${path}`, init)
  if (!response.ok) {
    throw new Error(await extractErrorMessage(response))
  }
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

    return (await response.json()) as HealthResponse
  } catch {
    return null
  }
}

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
