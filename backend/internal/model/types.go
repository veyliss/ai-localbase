package model

import "sync"

type ServerConfig struct {
	Port                           string
	UploadDir                      string
	MaxUploadBytes                 int64
	StateFile                      string
	ChatHistoryFile                string
	QdrantURL                      string
	QdrantAPIKey                   string
	QdrantCollectionPrefix         string
	QdrantVectorSize               int
	QdrantDistance                 string
	QdrantTimeoutSeconds           int
	EnableHybridSearch             bool
	EnableSemanticReranker         bool
	EnableQueryRewrite             bool
	EnableSemanticCache            bool
	EnableContextCompression       bool
	OllamaBaseURL                  string
	EnableMCP                      bool
	EnableMCPLegacyToken           bool
	MCPBasePath                    string
	MCPRequestTimeoutSeconds       int
	MCPRequestsPerMinute           int
	YouComAPIKey                   string
	YouComSearchMaxResults         int
	YouComTimeoutSeconds           int
	RetrievalTopKDocument          int
	RetrievalCandidateTopKDocument int
	RetrievalTopKKnowledgeBase     int
	RetrievalCandidateTopKAllDocs  int
	RetrievalMaxChunksPerDocument  int
	RetrievalMaxContextChars       int
	RetrievalEnableAutoExpand      bool
	EvalKnowledgeBaseID            string
	EnableAuth                     bool
	AuthUsername                   string
	AuthPassword                   string
	AuthSetupToken                 string
	AuthResetToken                 string
	AuthResetPassword              string
	JWTSecret                      string
}

type AppState struct {
	Mu             sync.RWMutex
	Config         AppConfig
	KnowledgeBases map[string]KnowledgeBase
	EvalDatasets   map[string]EvalDataset
	EvalRuns       map[string]RunEvalDatasetResponse
	Auth           AuthState
}

type AuthState struct {
	Users                      map[string]AuthUser    `json:"users,omitempty"`
	Sessions                   map[string]AuthSession `json:"sessions,omitempty"`
	APIKeys                    map[string]APIKey      `json:"apiKeys,omitempty"`
	AppliedPasswordResetTokens []string               `json:"appliedPasswordResetTokens,omitempty"`
	SecurityEvents             []SecurityEvent        `json:"securityEvents,omitempty"`
}

type AuthUser struct {
	ID                string `json:"id"`
	Username          string `json:"username"`
	PasswordHash      string `json:"passwordHash"`
	Role              string `json:"role"`
	CreatedAt         string `json:"createdAt"`
	UpdatedAt         string `json:"updatedAt"`
	PasswordChangedAt string `json:"passwordChangedAt"`
}

type AuthSession struct {
	ID         string `json:"id"`
	UserID     string `json:"userId"`
	TokenHash  string `json:"tokenHash"`
	CreatedAt  string `json:"createdAt"`
	ExpiresAt  string `json:"expiresAt"`
	LastSeenAt string `json:"lastSeenAt"`
	RevokedAt  string `json:"revokedAt,omitempty"`
	UserAgent  string `json:"userAgent,omitempty"`
	IP         string `json:"ip,omitempty"`
}

type APIKey struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Prefix     string   `json:"prefix"`
	KeyHash    string   `json:"keyHash"`
	Scopes     []string `json:"scopes"`
	CreatedAt  string   `json:"createdAt"`
	LastUsedAt string   `json:"lastUsedAt,omitempty"`
	RevokedAt  string   `json:"revokedAt,omitempty"`
}

type SecurityEvent struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Username  string `json:"username,omitempty"`
	IP        string `json:"ip,omitempty"`
	UserAgent string `json:"userAgent,omitempty"`
	CreatedAt string `json:"createdAt"`
	Message   string `json:"message,omitempty"`
}

type HealthResponse struct {
	Status string            `json:"status"`
	Name   string            `json:"name"`
	Config map[string]string `json:"config"`
}

type ChatConfig struct {
	Provider            string  `json:"provider"`
	BaseURL             string  `json:"baseUrl"`
	Model               string  `json:"model"`
	APIKey              string  `json:"apiKey"`
	APIKeyConfigured    bool    `json:"apiKeyConfigured,omitempty"`
	ClearAPIKey         bool    `json:"clearApiKey,omitempty"`
	Temperature         float64 `json:"temperature"`
	ContextMessageLimit int     `json:"contextMessageLimit"`
}

type EmbeddingConfig struct {
	Provider         string `json:"provider"`
	BaseURL          string `json:"baseUrl"`
	Model            string `json:"model"`
	APIKey           string `json:"apiKey"`
	APIKeyConfigured bool   `json:"apiKeyConfigured,omitempty"`
	ClearAPIKey      bool   `json:"clearApiKey,omitempty"`
}

type MCPConfig struct {
	Enabled                 bool     `json:"enabled"`
	BasePath                string   `json:"basePath"`
	Token                   string   `json:"token"`
	TokenConfigured         bool     `json:"tokenConfigured,omitempty"`
	LegacyTokenEnabled      bool     `json:"legacyTokenEnabled,omitempty"`
	DeploymentWarnings      []string `json:"deploymentWarnings,omitempty"`
	RecommendedAuthMode     string   `json:"recommendedAuthMode,omitempty"`
	DangerConfirmationMode  string   `json:"dangerConfirmationMode,omitempty"`
}

type MCPDangerConfirmationRequest struct {
	ToolName   string         `json:"toolName"`
	Arguments  map[string]any `json:"arguments,omitempty"`
	ParamHash  string         `json:"paramHash,omitempty"`
	TTLSeconds int            `json:"ttlSeconds,omitempty"`
}

type MCPDangerConfirmationResponse struct {
	ConfirmNonce string `json:"confirmNonce"`
	ExpiresAt    string `json:"expiresAt"`
	ToolName     string `json:"toolName"`
	ParamHash    string `json:"paramHash"`
}

type MCPStartImportJobRequest struct {
	KnowledgeBaseID string `json:"knowledgeBaseId"`
	FileName        string `json:"fileName"`
	Content         string `json:"content,omitempty"`
}

type MCPJob struct {
	ID          string         `json:"jobId"`
	Type        string         `json:"type"`
	Status      string         `json:"status"`
	Progress    int            `json:"progress"`
	Summary     string         `json:"summary"`
	Result      map[string]any `json:"result,omitempty"`
	Error       string         `json:"error,omitempty"`
	Warnings    []string       `json:"warnings,omitempty"`
	CreatedAt   string         `json:"createdAt"`
	UpdatedAt   string         `json:"updatedAt"`
	CompletedAt string         `json:"completedAt,omitempty"`
}

type RetrievalConfig struct {
	DefaultSearchMode        string `json:"defaultSearchMode"`
	HybridSearchEnabled      bool   `json:"hybridSearchEnabled"`
	RerankStrategy           string `json:"rerankStrategy"`
	EnableQueryRewrite       bool   `json:"enableQueryRewrite"`
	QueryRewriteMaxVariants  int    `json:"queryRewriteMaxVariants"`
	TopKDocument             int    `json:"topKDocument"`
	CandidateTopKDocument    int    `json:"candidateTopKDocument"`
	TopKKnowledgeBase        int    `json:"topKKnowledgeBase"`
	CandidateTopKAllDocs     int    `json:"candidateTopKAllDocs"`
	MaxChunksPerDocument     int    `json:"maxChunksPerDocument"`
	MaxContextChars          int    `json:"maxContextChars"`
	EnableLowConfidenceBoost bool   `json:"enableLowConfidenceBoost"`
}

type AppConfig struct {
	Chat      ChatConfig      `json:"chat"`
	Embedding EmbeddingConfig `json:"embedding"`
	MCP       MCPConfig       `json:"mcp"`
	Retrieval RetrievalConfig `json:"retrieval"`
}

type KnowledgeBase struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Documents   []Document `json:"documents"`
	CreatedAt   string     `json:"createdAt"`
}

type KnowledgeBaseInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type Document struct {
	ID              string `json:"id"`
	KnowledgeBaseID string `json:"knowledgeBaseId"`
	Name            string `json:"name"`
	Size            int64  `json:"size"`
	SizeLabel       string `json:"sizeLabel"`
	UploadedAt      string `json:"uploadedAt"`
	Status          string `json:"status"`
	Path            string `json:"path"`
	ContentPreview  string `json:"contentPreview"`
	ChunkCount      int    `json:"chunkCount,omitempty"`
	IndexedAt       string `json:"indexedAt,omitempty"`
	IndexError      string `json:"indexError,omitempty"`
}

type DocumentChunkPreview struct {
	ID    string `json:"id"`
	Index int    `json:"index"`
	Kind  string `json:"kind"`
	Text  string `json:"text"`
}

type DocumentIndexDiagnostics struct {
	RawContentChars       int  `json:"rawContentChars"`
	ChunkCount            int  `json:"chunkCount"`
	VectorCount           int  `json:"vectorCount"`
	SummaryChunkCount     int  `json:"summaryChunkCount"`
	StructuredRowCount    int  `json:"structuredRowCount"`
	RawContentAvailable   bool `json:"rawContentAvailable"`
	QdrantEnabled         bool `json:"qdrantEnabled"`
	RawContentTruncated   bool `json:"rawContentTruncated"`
	ChunkPreviewTruncated bool `json:"chunkPreviewTruncated"`
}

type DocumentDetailResponse struct {
	KnowledgeBaseID string                   `json:"knowledgeBaseId"`
	Document        Document                 `json:"document"`
	Diagnostics     DocumentIndexDiagnostics `json:"diagnostics"`
	RawContent      string                   `json:"rawContent"`
	Summary         string                   `json:"summary"`
	Chunks          []DocumentChunkPreview   `json:"chunks"`
}

type KnowledgeBaseHealthMetrics struct {
	DocumentCount      int    `json:"documentCount"`
	IndexedCount       int    `json:"indexedCount"`
	ProcessingCount    int    `json:"processingCount"`
	FailedCount        int    `json:"failedCount"`
	EmptyContentCount  int    `json:"emptyContentCount"`
	ChunkCount         int    `json:"chunkCount"`
	VectorCount        int    `json:"vectorCount"`
	SummaryChunkCount  int    `json:"summaryChunkCount"`
	StructuredRowCount int    `json:"structuredRowCount"`
	RawContentChars    int    `json:"rawContentChars"`
	QdrantEnabled      bool   `json:"qdrantEnabled"`
	LastIndexedAt      string `json:"lastIndexedAt,omitempty"`
}

type KnowledgeBaseDocumentHealth struct {
	DocumentID          string `json:"documentId"`
	DocumentName        string `json:"documentName"`
	Status              string `json:"status"`
	IndexedAt           string `json:"indexedAt,omitempty"`
	IndexError          string `json:"indexError,omitempty"`
	ChunkCount          int    `json:"chunkCount"`
	VectorCount         int    `json:"vectorCount"`
	SummaryChunkCount   int    `json:"summaryChunkCount"`
	StructuredRowCount  int    `json:"structuredRowCount"`
	RawContentChars     int    `json:"rawContentChars"`
	RawContentAvailable bool   `json:"rawContentAvailable"`
	NeedsReindex        bool   `json:"needsReindex"`
	Recommendation      string `json:"recommendation,omitempty"`
}

type KnowledgeBaseHealthResponse struct {
	KnowledgeBaseID string                        `json:"knowledgeBaseId"`
	Name            string                        `json:"name"`
	Status          string                        `json:"status"`
	Score           int                           `json:"score"`
	Metrics         KnowledgeBaseHealthMetrics    `json:"metrics"`
	Recommendations []string                      `json:"recommendations"`
	Documents       []KnowledgeBaseDocumentHealth `json:"documents"`
}

type UploadResponse struct {
	Message       string   `json:"message"`
	KnowledgeBase string   `json:"knowledgeBaseId"`
	Uploaded      Document `json:"uploaded"`
}

type StagedUpload struct {
	ID         string `json:"id"`
	FileName   string `json:"fileName"`
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	SizeLabel  string `json:"sizeLabel"`
	SHA256     string `json:"sha256"`
	CreatedAt  string `json:"createdAt"`
	ExpiresAt  string `json:"expiresAt"`
	Status     string `json:"status"`
	Source     string `json:"source,omitempty"`
	ConsumedAt string `json:"consumedAt,omitempty"`
}

type StageUploadResponse struct {
	Message  string       `json:"message"`
	Staged   StagedUpload `json:"staged"`
	UploadID string       `json:"uploadId"`
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatModelConfig struct {
	Provider            string  `json:"provider"`
	BaseURL             string  `json:"baseUrl"`
	Model               string  `json:"model"`
	APIKey              string  `json:"apiKey"`
	Temperature         float64 `json:"temperature"`
	ContextMessageLimit int     `json:"contextMessageLimit"`
}

type EmbeddingModelConfig struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl"`
	Model    string `json:"model"`
	APIKey   string `json:"apiKey"`
}

type ChatCompletionRequest struct {
	ConversationID          string               `json:"conversationId"`
	Model                   string               `json:"model"`
	Messages                []ChatMessage        `json:"messages"`
	KnowledgeBaseID         string               `json:"knowledgeBaseId"`
	DocumentID              string               `json:"documentId"`
	RetrievalMode           string               `json:"retrievalMode,omitempty"`
	RerankStrategy          string               `json:"rerankStrategy,omitempty"`
	EnableQueryRewrite      *bool                `json:"enableQueryRewrite,omitempty"`
	QueryRewriteMaxVariants int                  `json:"queryRewriteMaxVariants,omitempty"`
	Config                  ChatModelConfig      `json:"config"`
	Embedding               EmbeddingModelConfig `json:"embedding"`
}

type ChatCompletionChoice struct {
	Index   int         `json:"index"`
	Message ChatMessage `json:"message"`
}

type ChatCompletionResponse struct {
	ID       string                 `json:"id"`
	Object   string                 `json:"object"`
	Created  int64                  `json:"created"`
	Model    string                 `json:"model"`
	Choices  []ChatCompletionChoice `json:"choices"`
	Metadata map[string]any         `json:"metadata"`
}

type ToolUseMetadata struct {
	ToolName        string         `json:"toolName"`
	Reason          string         `json:"reason"`
	PermissionLevel string         `json:"permissionLevel"`
	Arguments       map[string]any `json:"arguments,omitempty"`
	Data            map[string]any `json:"data,omitempty"`
	IsError         bool           `json:"isError,omitempty"`
	Error           string         `json:"error,omitempty"`
}

type ConfigUpdateRequest struct {
	Chat      ChatConfig      `json:"chat"`
	Embedding EmbeddingConfig `json:"embedding"`
	MCP       MCPConfig       `json:"mcp"`
	Retrieval RetrievalConfig `json:"retrieval"`
}

type Conversation struct {
	ID              string              `json:"id"`
	Title           string              `json:"title"`
	KnowledgeBaseID string              `json:"knowledgeBaseId"`
	DocumentID      string              `json:"documentId"`
	CreatedAt       string              `json:"createdAt"`
	UpdatedAt       string              `json:"updatedAt"`
	Messages        []StoredChatMessage `json:"messages"`
}

type StoredChatMessage struct {
	ID        string         `json:"id"`
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	CreatedAt string         `json:"createdAt"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type ConversationListItem struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	KnowledgeBaseID string `json:"knowledgeBaseId"`
	DocumentID      string `json:"documentId"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
	MessageCount    int    `json:"messageCount"`
}

type SaveConversationRequest struct {
	ID              string              `json:"id"`
	Title           string              `json:"title"`
	KnowledgeBaseID string              `json:"knowledgeBaseId"`
	DocumentID      string              `json:"documentId"`
	Messages        []StoredChatMessage `json:"messages"`
}

type EditMessageRequest struct {
	Content string `json:"content"`
}

type ErrorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId,omitempty"`
}

type APIError struct {
	Error ErrorDetail `json:"error"`
}

type EvalSourceDocument struct {
	KnowledgeBaseID string `json:"knowledge_base_id"`
	DocumentID      string `json:"document_id"`
	ChunkID         string `json:"chunk_id"`
}

type EvalGroundTruthCase struct {
	ID              string               `json:"id"`
	Question        string               `json:"question"`
	Answer          string               `json:"answer"`
	AnswerSnippets  []string             `json:"answer_snippets"`
	SourceDocuments []EvalSourceDocument `json:"source_documents"`
	AnswerType      string               `json:"answer_type"`
	Difficulty      string               `json:"difficulty"`
	ReviewStatus    string               `json:"review_status,omitempty"`
	Disabled        bool                 `json:"disabled,omitempty"`
	Notes           string               `json:"notes,omitempty"`
}

type EvalDataset struct {
	ID              string                `json:"id"`
	Name            string                `json:"name"`
	Kind            string                `json:"kind,omitempty"`
	KnowledgeBaseID string                `json:"knowledgeBaseId,omitempty"`
	DocumentID      string                `json:"documentId,omitempty"`
	Count           int                   `json:"count"`
	DocumentCount   int                   `json:"documentCount"`
	CreatedAt       string                `json:"createdAt"`
	UpdatedAt       string                `json:"updatedAt,omitempty"`
	Items           []EvalGroundTruthCase `json:"items"`
}

type EvalDatasetSummary struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Kind            string `json:"kind,omitempty"`
	KnowledgeBaseID string `json:"knowledgeBaseId,omitempty"`
	DocumentID      string `json:"documentId,omitempty"`
	Count           int    `json:"count"`
	DocumentCount   int    `json:"documentCount"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt,omitempty"`
}

type GenerateEvalDatasetRequest struct {
	KnowledgeBaseID string `json:"knowledgeBaseId"`
	DocumentID      string `json:"documentId"`
	MaxPerDocument  int    `json:"maxPerDocument"`
}

type GenerateEvalDatasetResponse struct {
	DatasetID       string                `json:"datasetId,omitempty"`
	KnowledgeBaseID string                `json:"knowledgeBaseId,omitempty"`
	DocumentID      string                `json:"documentId,omitempty"`
	Count           int                   `json:"count"`
	DocumentCount   int                   `json:"documentCount"`
	CreatedAt       string                `json:"createdAt,omitempty"`
	Items           []EvalGroundTruthCase `json:"items"`
}

type AddEvalDatasetCandidateRequest struct {
	KnowledgeBaseID string              `json:"knowledgeBaseId"`
	DocumentID      string              `json:"documentId"`
	Item            EvalGroundTruthCase `json:"item"`
}

type AddEvalDatasetCandidateResponse struct {
	Dataset EvalDatasetSummary  `json:"dataset"`
	Item    EvalGroundTruthCase `json:"item"`
	Created bool                `json:"created"`
}

type UpdateEvalDatasetItemRequest struct {
	Item EvalGroundTruthCase `json:"item"`
}

type UpdateEvalDatasetItemResponse struct {
	Dataset EvalDatasetSummary  `json:"dataset"`
	Item    EvalGroundTruthCase `json:"item"`
}

type DeleteEvalDatasetItemResponse struct {
	Dataset EvalDatasetSummary `json:"dataset"`
	Deleted string             `json:"deleted"`
}

type RunEvalDatasetRequest struct {
	IncludeDisabled         bool   `json:"includeDisabled,omitempty"`
	TopK                    int    `json:"topK,omitempty"`
	SearchMode              string `json:"searchMode,omitempty"`
	RerankStrategy          string `json:"rerankStrategy,omitempty"`
	EnableQueryRewrite      *bool  `json:"enableQueryRewrite,omitempty"`
	QueryRewriteMaxVariants int    `json:"queryRewriteMaxVariants,omitempty"`
}

type EvalRunMetrics struct {
	TotalCases             int     `json:"totalCases"`
	HitCount               int     `json:"hitCount"`
	MissCount              int     `json:"missCount"`
	HitRate                float64 `json:"hitRate"`
	MRR                    float64 `json:"mrr"`
	LatencyP50Ms           int64   `json:"latencyP50Ms"`
	LatencyP95Ms           int64   `json:"latencyP95Ms"`
	LowConfidence          int     `json:"lowConfidence"`
	ErrorCount             int     `json:"errorCount"`
	SkippedDisabled        int     `json:"skippedDisabled"`
	EvidenceSupportedCount int     `json:"evidenceSupportedCount"`
	EvidenceSupportRate    float64 `json:"evidenceSupportRate"`
	CitationMismatchCount  int     `json:"citationMismatchCount"`
	DirectEvidenceHitCount int     `json:"directEvidenceHitCount"`
	DirectEvidenceHitRate  float64 `json:"directEvidenceHitRate"`
}

type EvalRunCaseResult struct {
	CaseID          string                   `json:"caseId"`
	Question        string                   `json:"question"`
	ExpectedAnswer  string                   `json:"expectedAnswer"`
	Hit             bool                     `json:"hit"`
	HitRank         int                      `json:"hitRank"`
	ReciprocalRank  float64                  `json:"reciprocalRank"`
	MatchedBy       string                   `json:"matchedBy,omitempty"`
	ElapsedMs       int64                    `json:"elapsedMs"`
	LowConfidence   bool                     `json:"lowConfidence"`
	Confidence      RetrievalDebugConfidence `json:"confidence,omitempty"`
	EvidenceSupport bool                     `json:"evidenceSupport"`
	EvidenceIssue   string                   `json:"evidenceIssue,omitempty"`
	DirectEvidence  bool                     `json:"directEvidence"`
	Error           string                   `json:"error,omitempty"`
	Retrieved       []RetrievalDebugChunk    `json:"retrieved"`
}

type RunEvalDatasetResponse struct {
	RunID            string              `json:"runId"`
	DatasetID        string              `json:"datasetId"`
	DatasetName      string              `json:"datasetName"`
	KnowledgeBaseID  string              `json:"knowledgeBaseId,omitempty"`
	DocumentID       string              `json:"documentId,omitempty"`
	SearchMode       string              `json:"searchMode"`
	RerankStrategy   string              `json:"rerankStrategy"`
	QueryRewriteUsed bool                `json:"queryRewriteUsed"`
	StartedAt        string              `json:"startedAt"`
	ElapsedMs        int64               `json:"elapsedMs"`
	Metrics          EvalRunMetrics      `json:"metrics"`
	Cases            []EvalRunCaseResult `json:"cases"`
}

type EvalRunSummary struct {
	RunID            string         `json:"runId"`
	DatasetID        string         `json:"datasetId"`
	DatasetName      string         `json:"datasetName"`
	KnowledgeBaseID  string         `json:"knowledgeBaseId,omitempty"`
	DocumentID       string         `json:"documentId,omitempty"`
	SearchMode       string         `json:"searchMode"`
	RerankStrategy   string         `json:"rerankStrategy"`
	QueryRewriteUsed bool           `json:"queryRewriteUsed"`
	StartedAt        string         `json:"startedAt"`
	ElapsedMs        int64          `json:"elapsedMs"`
	Metrics          EvalRunMetrics `json:"metrics"`
}

type EvalRunListResponse struct {
	Items []EvalRunSummary `json:"items"`
}

type RetrievalDebugRequest struct {
	Query                   string `json:"query"`
	KnowledgeBaseID         string `json:"knowledgeBaseId"`
	DocumentID              string `json:"documentId"`
	TopK                    int    `json:"topK"`
	SearchMode              string `json:"searchMode,omitempty"`
	RerankStrategy          string `json:"rerankStrategy,omitempty"`
	EnableQueryRewrite      *bool  `json:"enableQueryRewrite,omitempty"`
	QueryRewriteMaxVariants int    `json:"queryRewriteMaxVariants,omitempty"`
	Verbose                 bool   `json:"verbose,omitempty"`
}

type RetrievalDebugChunk struct {
	ID                string   `json:"id"`
	KnowledgeBaseID   string   `json:"knowledgeBaseId"`
	DocumentID        string   `json:"documentId"`
	DocumentName      string   `json:"documentName"`
	Index             int      `json:"index"`
	Kind              string   `json:"kind"`
	Score             float64  `json:"score"`
	Text              string   `json:"text"`
	MatchReasons      []string `json:"matchReasons,omitempty"`
	RetrievalChannels []string `json:"retrievalChannels,omitempty"`
	DenseRank         int      `json:"denseRank,omitempty"`
	SparseRank        int      `json:"sparseRank,omitempty"`
}

type RetrievalDebugTraceStep struct {
	Stage       string         `json:"stage"`
	Status      string         `json:"status"`
	Reason      string         `json:"reason,omitempty"`
	InputCount  int            `json:"inputCount,omitempty"`
	OutputCount int            `json:"outputCount,omitempty"`
	ElapsedMs   int64          `json:"elapsedMs,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
}

type RetrievalDebugConfidence struct {
	Status           string   `json:"status"`
	Summary          string   `json:"summary"`
	Reasons          []string `json:"reasons,omitempty"`
	Suggestions      []string `json:"suggestions,omitempty"`
	TopScore         float64  `json:"topScore"`
	AverageScore     float64  `json:"averageScore"`
	EvidenceCoverage float64  `json:"evidenceCoverage"`
}

type RetrievalEvidenceGateDiagnostic struct {
	Enabled             bool                  `json:"enabled"`
	Reason              string                `json:"reason,omitempty"`
	CandidateCount      int                   `json:"candidateCount"`
	SelectedCount       int                   `json:"selectedCount"`
	DirectEvidenceCount int                   `json:"directEvidenceCount"`
	WeakEvidenceCount   int                   `json:"weakEvidenceCount"`
	RemovedCount        int                   `json:"removedCount"`
	TopBefore           []RetrievalDebugChunk `json:"topBefore,omitempty"`
	TopAfter            []RetrievalDebugChunk `json:"topAfter,omitempty"`
}

type RetrievalDebugResponse struct {
	Query             string                          `json:"query"`
	KnowledgeBaseID   string                          `json:"knowledgeBaseId,omitempty"`
	DocumentID        string                          `json:"documentId,omitempty"`
	SearchMode        string                          `json:"searchMode"`
	RerankStrategy    string                          `json:"rerankStrategy"`
	QueryRewriteUsed  bool                            `json:"queryRewriteUsed"`
	QueryVariants     []string                        `json:"queryVariants,omitempty"`
	StructuredIntent  string                          `json:"structuredIntent,omitempty"`
	TargetField       string                          `json:"targetField,omitempty"`
	DeterministicUsed bool                            `json:"deterministicUsed"`
	ElapsedMs         int64                           `json:"elapsedMs"`
	Count             int                             `json:"count"`
	LowConfidence     bool                            `json:"lowConfidence"`
	Confidence        RetrievalDebugConfidence        `json:"confidence"`
	EvidenceGate      RetrievalEvidenceGateDiagnostic `json:"evidenceGate,omitempty"`
	ContextPreview    string                          `json:"contextPreview"`
	Sources           []map[string]string             `json:"sources"`
	EvalCandidate     *EvalGroundTruthCase            `json:"evalCandidate,omitempty"`
	Trace             []RetrievalDebugTraceStep       `json:"trace,omitempty"`
	Items             []RetrievalDebugChunk           `json:"items"`
	VerboseDetails    *RetrievalDebugVerboseDetails   `json:"verboseDetails,omitempty"`
}

type RetrievalDebugVerboseDetails struct {
	QueryEmbeddingMs    int64                     `json:"queryEmbeddingMs,omitempty"`
	VectorSearchMs      int64                     `json:"vectorSearchMs,omitempty"`
	RerankMs            int64                     `json:"rerankMs,omitempty"`
	MMRMs               int64                     `json:"mmrMs,omitempty"`
	CandidatesCount     int                       `json:"candidatesCount"`
	AfterRerankCount    int                       `json:"afterRerankCount"`
	AfterMMRCount       int                       `json:"afterMMRCount"`
	TopCandidates       []RetrievalDebugChunk     `json:"topCandidates,omitempty"`
	TopAfterRerank      []RetrievalDebugChunk     `json:"topAfterRerank,omitempty"`
	TopAfterMMR         []RetrievalDebugChunk     `json:"topAfterMMR,omitempty"`
	MMREffect           *MMREffectAnalysis        `json:"mmrEffect,omitempty"`
	QueryRewriteDetails *QueryRewriteDebugDetails `json:"queryRewriteDetails,omitempty"`
}

type MMREffectAnalysis struct {
	RemovedDuplicates int                   `json:"removedDuplicates"`
	ReorderedItems    int                   `json:"reorderedItems"`
	DiversityScore    float64               `json:"diversityScore"`
	BeforeMMR         []RetrievalDebugChunk `json:"beforeMMR,omitempty"`
	AfterMMR          []RetrievalDebugChunk `json:"afterMMR,omitempty"`
	RankingChanges    []RankingChange       `json:"rankingChanges,omitempty"`
}

type RankingChange struct {
	ChunkID      string  `json:"chunkId"`
	DocumentName string  `json:"documentName"`
	BeforeRank   int     `json:"beforeRank"`
	AfterRank    int     `json:"afterRank"`
	ScoreBefore  float64 `json:"scoreBefore"`
	ScoreAfter   float64 `json:"scoreAfter"`
}

type QueryRewriteDebugDetails struct {
	OriginalQuery    string   `json:"originalQuery"`
	RewrittenQueries []string `json:"rewrittenQueries"`
	RewriteMs        int64    `json:"rewriteMs"`
	TotalQueries     int      `json:"totalQueries"`
	HitsPerQuery     []int    `json:"hitsPerQuery,omitempty"`
}
