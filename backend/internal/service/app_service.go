package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"mime/multipart"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"ai-localbase/internal/model"
	"ai-localbase/internal/util"
)

const (
	ragSearchTopKDocument          = 6
	ragSearchCandidateTopKDocument = 12
	ragSearchTopKKnowledgeBase     = 10
	ragSearchCandidateTopKAllDocs  = 32
	ragMaxChunksPerDocument        = 2

	rerankVectorWeight  = 0.72
	rerankKeywordWeight = 0.28
	mmrLambda           = 0.75

	lowConfidenceTopScoreThreshold = 0.22
	lowConfidenceAvgScoreThreshold = 0.18

	documentDetailRawContentLimit = 20000
	documentDetailChunkLimit      = 30
	documentDetailChunkTextLimit  = 1200
	retrievalDebugContextLimit    = 3000
	retrievalDebugChunkTextLimit  = 1600
	mcpImportJobContentLimit      = 256 * 1024
	maxMultiQuerySearchQueries    = 8
	mcpJobCancelWarning           = "任务取消是 best-effort；如果底层导入已进入注册或索引阶段，文档可能已经完成导入。"
	conversationScopeVersion      = 1
	defaultKnowledgeTemperature   = 0.1
	maxKnowledgeTemperature       = 0.5
)

var (
	ErrConversationScopeMismatch      = errors.New("conversation scope mismatch")
	ErrConversationScopeUpgradeNeeded = errors.New("legacy conversation scope is not trusted")
)

func normalizeServiceContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

type AppService struct {
	state                 *model.AppState
	store                 *AppStateStore
	chatHistory           ChatHistoryStore
	qdrant                *QdrantService
	rag                   *RagService
	indexedContentStore   *IndexedContentStore
	serverConfig          model.ServerConfig
	staging               *UploadStagingService
	stateSaveMu           sync.Mutex
	reranker              SemanticReranker
	queryRewriter         QueryRewriter
	semanticCache         *SemanticCache
	contextCompressor     ContextCompressor
	retrievalOrchestrator *RetrievalOrchestrator
	mcpDangerMu           sync.Mutex
	mcpDangerConfirms     map[string]mcpDangerConfirmationRecord
	mcpDangerRates        map[string][]time.Time
	mcpJobMu              sync.Mutex
	mcpJobs               map[string]model.MCPJob
	mcpJobCancels         map[string]context.CancelFunc
	mcpJobLifecycleMu     sync.Mutex
	mcpJobWG              sync.WaitGroup
	mcpJobsShutdown       bool
	indexReservationMu    sync.Mutex
	indexReservations     map[string]chan struct{}
}

type mcpDangerConfirmationRecord struct {
	Nonce         string
	ToolName      string
	ParamHash     string
	ExpiresAt     time.Time
	OwnerUserID   string
	OwnerAPIKeyID string
}

const (
	mcpDangerConfirmationDefaultTTL = 5 * time.Minute
	mcpDangerConfirmationRateWindow = time.Minute
	mcpDangerConfirmationRateLimit  = 10
)

// ContextCompressor 上下文压缩器接口
// Compress 将多个 chunks 压缩为简洁的上下文文本
// 保留与 query 最相关的信息，去除冗余
type ContextCompressor interface {
	Compress(ctx context.Context, query string, chunks []RetrievedChunk) (string, error)
}

// LLMContextCompressor 基于 LLM 的上下文压缩器
type LLMContextCompressor struct {
	llmSvc    *LLMService
	maxTokens int
	enabled   bool
	configFn  func() model.ChatModelConfig
}

// NewLLMContextCompressor 创建 LLM 上下文压缩器
func NewLLMContextCompressor(llmSvc *LLMService, maxTokens int) *LLMContextCompressor {
	if maxTokens <= 0 {
		maxTokens = 800
	}
	return &LLMContextCompressor{llmSvc: llmSvc, maxTokens: maxTokens, enabled: true}
}

// SetChatConfigProvider 注入 Chat 配置提供函数
func (c *LLMContextCompressor) SetChatConfigProvider(provider func() model.ChatModelConfig) {
	if c == nil {
		return
	}
	c.configFn = provider
}

// Compress 使用 LLM 压缩上下文
// 只在 chunks 总字符数超过阈值（默认 2000 字符）时才压缩
// prompt："请从以下文档中提取与问题最相关的信息，简洁总结（不超过{maxTokens}个token）。\n问题：{query}\n文档：{chunks}"
func (c *LLMContextCompressor) Compress(ctx context.Context, query string, chunks []RetrievedChunk) (string, error) {
	if c == nil || !c.enabled {
		return "", nil
	}
	if c.llmSvc == nil {
		return "", fmt.Errorf("llm service is nil")
	}
	if chunksTotalChars(chunks) <= 2000 {
		return "", nil
	}
	chunkText := buildChunkText(chunks)
	prompt := fmt.Sprintf("请从以下文档中提取与问题最相关的信息，简洁总结（不超过%d个token）。\n问题：%s\n文档：%s", c.maxTokens, query, chunkText)
	request := model.ChatCompletionRequest{
		Messages: []model.ChatMessage{{Role: "user", Content: prompt}},
	}
	if c.configFn != nil {
		request.Config = c.configFn()
	}
	resp, err := c.llmSvc.Chat(request)
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("empty llm response")
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}

func NewAppService(qdrant *QdrantService, store *AppStateStore, chatHistory ChatHistoryStore, serverConfig model.ServerConfig) *AppService {
	stagingDir := strings.TrimSpace(serverConfig.StagingDir)
	if stagingDir == "" && strings.TrimSpace(serverConfig.UploadDir) != "" {
		stagingDir = filepath.Join(filepath.Dir(serverConfig.UploadDir), "staging")
	}
	indexedContentDir := strings.TrimSpace(serverConfig.IndexedContentDir)
	if indexedContentDir == "" && strings.TrimSpace(serverConfig.UploadDir) != "" {
		indexedContentDir = filepath.Join(filepath.Dir(serverConfig.UploadDir), "indexed-content")
	}
	if indexedContentDir == "" && strings.TrimSpace(serverConfig.StateFile) != "" {
		indexedContentDir = filepath.Join(filepath.Dir(serverConfig.StateFile), "indexed-content")
	}
	service := &AppService{
		state:               defaultAppState(serverConfig),
		store:               store,
		chatHistory:         chatHistory,
		qdrant:              qdrant,
		rag:                 NewRagService(),
		indexedContentStore: NewIndexedContentStore(indexedContentDir),
		serverConfig:        serverConfig,
		staging:             NewUploadStagingService(stagingDir, 30*time.Minute),
		mcpDangerConfirms:   map[string]mcpDangerConfirmationRecord{},
		mcpDangerRates:      map[string][]time.Time{},
		mcpJobs:             map[string]model.MCPJob{},
		mcpJobCancels:       map[string]context.CancelFunc{},
		indexReservations:   map[string]chan struct{}{},
	}
	service.retrievalOrchestrator = NewRetrievalOrchestrator(service)
	service.rag.SetQdrantService(qdrant)

	service.reranker = NewEmbeddingReranker(service.rag)
	if embeddingReranker, ok := service.reranker.(*EmbeddingReranker); ok {
		embeddingReranker.SetEmbeddingConfigProvider(service.currentEmbeddingConfig)
		embeddingReranker.SetVectorSizeProvider(service.qdrantVectorSize)
	}

	if serverConfig.EnableSemanticCache {
		service.semanticCache = NewSemanticCache(0, 0, 0)
	}

	llmService := NewLLMService()
	service.SetQueryRewriter(NewLLMQueryRewriter(llmService, 3))
	if serverConfig.EnableContextCompression {
		service.SetContextCompressor(NewLLMContextCompressor(llmService, 800))
	}

	if store != nil {
		if loadedState, err := store.Load(); err != nil {
			log.Printf("failed to load app state: %v", err)
		} else if loadedState != nil {
			service.state = &model.AppState{
				Config:         loadedState.Config,
				KnowledgeBases: loadedState.KnowledgeBases,
				EvalDatasets:   loadedState.EvalDatasets,
				EvalRuns:       loadedState.EvalRuns,
				Auth:           loadedState.Auth,
			}
			if service.state.KnowledgeBases == nil {
				service.state.KnowledgeBases = map[string]model.KnowledgeBase{}
			}
			if service.state.EvalDatasets == nil {
				service.state.EvalDatasets = map[string]model.EvalDataset{}
			}
			if service.state.EvalRuns == nil {
				service.state.EvalRuns = map[string]model.RunEvalDatasetResponse{}
			}
			ensureAuthState(&service.state.Auth)
		}
	}

	if len(service.state.KnowledgeBases) == 0 {
		defaultState := defaultAppState(serverConfig)
		service.state.KnowledgeBases = defaultState.KnowledgeBases
		if service.state.Config.Chat.Provider == "" {
			service.state.Config = defaultState.Config
		}
		if service.state.EvalDatasets == nil {
			service.state.EvalDatasets = map[string]model.EvalDataset{}
		}
		if service.state.EvalRuns == nil {
			service.state.EvalRuns = map[string]model.RunEvalDatasetResponse{}
		}
	}
	ensureAuthState(&service.state.Auth)
	service.state.Config.MCP.Enabled = serverConfig.EnableMCP
	service.state.Config.MCP.BasePath = defaultMCPBasePath(service.state.Config.MCP.BasePath)
	service.state.Config.MCP.LegacyTokenEnabled = serverConfig.EnableMCPLegacyToken
	if strings.TrimSpace(service.state.Config.MCP.Token) == "" {
		service.state.Config.MCP.Token = generateMCPToken()
	}
	service.state.Config.Retrieval = normalizeRetrievalConfig(service.state.Config.Retrieval, serverConfig)

	for kbID := range service.state.KnowledgeBases {
		if err := service.ensureKnowledgeBaseCollection(kbID); err != nil {
			log.Printf("failed to ensure qdrant collection for knowledge base %s: %v", kbID, err)
		}
	}
	if err := service.saveState(); err != nil {
		log.Printf("failed to persist app state during startup: %v", err)
	}

	return service
}

func (s *AppService) saveState() error {
	if s == nil || s.store == nil {
		return nil
	}

	s.stateSaveMu.Lock()
	defer s.stateSaveMu.Unlock()

	s.state.Mu.RLock()
	state := persistentAppState{
		Config:         s.state.Config,
		KnowledgeBases: cloneKnowledgeBases(s.state.KnowledgeBases),
		EvalDatasets:   cloneEvalDatasets(s.state.EvalDatasets),
		EvalRuns:       cloneEvalRuns(s.state.EvalRuns),
		Auth:           cloneAuthState(s.state.Auth),
	}
	s.state.Mu.RUnlock()

	return s.store.Save(state)
}

func cloneKnowledgeBases(source map[string]model.KnowledgeBase) map[string]model.KnowledgeBase {
	if source == nil {
		return map[string]model.KnowledgeBase{}
	}

	cloned := make(map[string]model.KnowledgeBase, len(source))
	for id, kb := range source {
		documents := make([]model.Document, len(kb.Documents))
		copy(documents, kb.Documents)
		kb.Documents = documents
		kb.Tags = append([]string(nil), kb.Tags...)
		kb.IndexHistory = append([]model.IndexRunRecord(nil), kb.IndexHistory...)
		cloned[id] = kb
	}

	return cloned
}

func cloneEvalDatasets(source map[string]model.EvalDataset) map[string]model.EvalDataset {
	if source == nil {
		return map[string]model.EvalDataset{}
	}

	cloned := make(map[string]model.EvalDataset, len(source))
	for id, dataset := range source {
		dataset.Items = cloneEvalGroundTruthCases(dataset.Items)
		cloned[id] = dataset
	}
	return cloned
}

func cloneEvalRuns(source map[string]model.RunEvalDatasetResponse) map[string]model.RunEvalDatasetResponse {
	if source == nil {
		return map[string]model.RunEvalDatasetResponse{}
	}

	cloned := make(map[string]model.RunEvalDatasetResponse, len(source))
	for id, run := range source {
		run.Cases = cloneEvalRunCaseResults(run.Cases)
		cloned[id] = run
	}
	return cloned
}

func ensureAuthState(state *model.AuthState) {
	if state == nil {
		return
	}
	if state.Users == nil {
		state.Users = map[string]model.AuthUser{}
	}
	if state.Sessions == nil {
		state.Sessions = map[string]model.AuthSession{}
	}
	if state.APIKeys == nil {
		state.APIKeys = map[string]model.APIKey{}
	}
	if state.AppliedPasswordResetTokens == nil {
		state.AppliedPasswordResetTokens = []string{}
	}
	if state.SecurityEvents == nil {
		state.SecurityEvents = []model.SecurityEvent{}
	}
}

func hasAuthUser(state model.AuthState) bool {
	return len(state.Users) > 0
}

func cloneAuthState(source model.AuthState) model.AuthState {
	cloned := model.AuthState{
		Users:    make(map[string]model.AuthUser, len(source.Users)),
		Sessions: make(map[string]model.AuthSession, len(source.Sessions)),
		APIKeys:  make(map[string]model.APIKey, len(source.APIKeys)),
		AppliedPasswordResetTokens: append(
			[]string(nil),
			source.AppliedPasswordResetTokens...,
		),
		SecurityEvents: append([]model.SecurityEvent(nil), source.SecurityEvents...),
	}
	for id, user := range source.Users {
		cloned.Users[id] = user
	}
	for id, session := range source.Sessions {
		cloned.Sessions[id] = session
	}
	for id, apiKey := range source.APIKeys {
		apiKey.Scopes = append([]string(nil), apiKey.Scopes...)
		cloned.APIKeys[id] = apiKey
	}
	return cloned
}

func cloneEvalRunCaseResults(source []model.EvalRunCaseResult) []model.EvalRunCaseResult {
	if source == nil {
		return nil
	}
	cloned := make([]model.EvalRunCaseResult, len(source))
	for index, item := range source {
		item.Retrieved = append([]model.RetrievalDebugChunk(nil), item.Retrieved...)
		cloned[index] = item
	}
	return cloned
}

func defaultAppState(serverConfig model.ServerConfig) *model.AppState {
	now := time.Now().UTC().Format(time.RFC3339)
	kbID := util.NextID("kb")
	ollamaBaseURL := serverConfig.OllamaBaseURL
	if ollamaBaseURL == "" {
		ollamaBaseURL = "http://localhost:11434"
	}
	return &model.AppState{
		Config: model.AppConfig{
			Chat: model.ChatConfig{
				Provider:             "ollama",
				BaseURL:              ollamaBaseURL,
				Model:                "qwen3.5:0.8b",
				APIKey:               "",
				APIKeyConfigured:     false,
				Temperature:          0.7,
				KnowledgeTemperature: defaultKnowledgeTemperature,
				ContextMessageLimit:  12,
			},
			Embedding: model.EmbeddingConfig{
				Provider:         "ollama",
				BaseURL:          ollamaBaseURL,
				Model:            "nomic-embed-text",
				APIKey:           "",
				APIKeyConfigured: false,
			},
			MCP: model.MCPConfig{
				Enabled:            serverConfig.EnableMCP,
				BasePath:           defaultMCPBasePath(serverConfig.MCPBasePath),
				Token:              generateMCPToken(),
				TokenConfigured:    true,
				LegacyTokenEnabled: serverConfig.EnableMCPLegacyToken,
			},
			Retrieval: defaultRetrievalConfig(serverConfig),
		},
		KnowledgeBases: map[string]model.KnowledgeBase{
			kbID: {
				ID:                  kbID,
				Name:                "默认知识库",
				Description:         "用于存放本地上传文档的默认知识库",
				Documents:           []model.Document{},
				CreatedAt:           now,
				UpdatedAt:           now,
				CurrentIndexVersion: currentIndexVersion,
			},
		},
		EvalDatasets: map[string]model.EvalDataset{},
		EvalRuns:     map[string]model.RunEvalDatasetResponse{},
		Auth: model.AuthState{
			Users:          map[string]model.AuthUser{},
			Sessions:       map[string]model.AuthSession{},
			APIKeys:        map[string]model.APIKey{},
			SecurityEvents: []model.SecurityEvent{},
		},
	}
}

func (s *AppService) GetHealthConfigMap(serverConfig model.ServerConfig) map[string]string {
	s.state.Mu.RLock()
	kbCount := len(s.state.KnowledgeBases)
	setupRequired := serverConfig.EnableAuth && !hasAuthUser(s.state.Auth)
	s.state.Mu.RUnlock()

	qdrantStatus := "disabled"
	if s.qdrant != nil && s.qdrant.IsEnabled() {
		qdrantStatus = "enabled"
	}

	return map[string]string{
		"auth_enabled":        fmt.Sprintf("%t", serverConfig.EnableAuth),
		"auth_setup_required": fmt.Sprintf("%t", setupRequired),
		"knowledge_bases":     fmt.Sprintf("%d", kbCount),
		"qdrant_status":       qdrantStatus,
	}
}

func (s *AppService) GetConfig() model.AppConfig {
	s.state.Mu.RLock()
	defer s.state.Mu.RUnlock()

	cfg := s.state.Config
	if cfg.Chat.ContextMessageLimit <= 0 {
		cfg.Chat.ContextMessageLimit = 12
	}
	cfg.Chat.KnowledgeTemperature = normalizeKnowledgeTemperature(cfg.Chat.KnowledgeTemperature)
	cfg.Chat.APIKeyConfigured = strings.TrimSpace(cfg.Chat.APIKey) != ""
	cfg.Embedding.APIKeyConfigured = strings.TrimSpace(cfg.Embedding.APIKey) != ""
	cfg.MCP.BasePath = defaultMCPBasePath(cfg.MCP.BasePath)
	if strings.TrimSpace(cfg.MCP.Token) == "" {
		cfg.MCP.Token = generateMCPToken()
	}
	cfg.MCP.TokenConfigured = strings.TrimSpace(cfg.MCP.Token) != ""
	cfg.MCP.LegacyTokenEnabled = s.serverConfig.EnableMCPLegacyToken
	cfg.Retrieval = normalizeRetrievalConfig(cfg.Retrieval, s.serverConfig)
	return cfg
}

func (s *AppService) GetPublicConfig() model.AppConfig {
	cfg := redactAppConfigSecrets(s.GetConfig())
	cfg.MCP.DeploymentWarnings = s.AuthDeploymentWarnings()
	cfg.MCP.RecommendedAuthMode = "api_key_scopes"
	cfg.MCP.DangerConfirmationMode = "confirm_nonce"
	return cfg
}

func redactAppConfigSecrets(cfg model.AppConfig) model.AppConfig {
	cfg.Chat.APIKeyConfigured = strings.TrimSpace(cfg.Chat.APIKey) != ""
	cfg.Chat.APIKey = ""
	cfg.Embedding.APIKeyConfigured = strings.TrimSpace(cfg.Embedding.APIKey) != ""
	cfg.Embedding.APIKey = ""
	cfg.MCP.TokenConfigured = strings.TrimSpace(cfg.MCP.Token) != ""
	cfg.MCP.Token = ""
	return cfg
}

func (s *AppService) AuthDeploymentWarnings() []string {
	if s == nil {
		return nil
	}
	warnings := []string{}
	setupRequired := false
	if s.serverConfig.EnableAuth {
		if s.state == nil {
			setupRequired = true
		} else {
			s.state.Mu.RLock()
			setupRequired = !hasAuthUser(s.state.Auth)
			s.state.Mu.RUnlock()
		}
	}
	if setupRequired && strings.TrimSpace(s.serverConfig.AuthPassword) == "" && strings.TrimSpace(s.serverConfig.AuthSetupToken) == "" {
		warnings = append(warnings, "未配置 AUTH_PASSWORD 或 AUTH_SETUP_TOKEN，首次初始化仅允许本机回环地址完成")
	}
	if s.serverConfig.EnableMCPLegacyToken {
		warnings = append(warnings, "ENABLE_MCP_LEGACY_TOKEN 已开启，仅建议迁移旧客户端时临时使用")
	}
	return warnings
}

func (s *AppService) StageUpload(file *multipart.FileHeader, source string) (model.StagedUpload, error) {
	return s.StageUploadAs(file, source, AuthPrincipal{})
}

func (s *AppService) StageUploadAs(file *multipart.FileHeader, source string, owner AuthPrincipal) (model.StagedUpload, error) {
	if s == nil || s.staging == nil {
		return model.StagedUpload{}, fmt.Errorf("upload staging service is not configured")
	}
	return s.staging.StageMultipartFileAs(file, source, owner)
}

func (s *AppService) StageInlineUpload(fileName string, content []byte, source string) (model.StagedUpload, error) {
	return s.StageInlineUploadAs(fileName, content, source, AuthPrincipal{})
}

func (s *AppService) StageInlineUploadAs(fileName string, content []byte, source string, owner AuthPrincipal) (model.StagedUpload, error) {
	if s == nil || s.staging == nil {
		return model.StagedUpload{}, fmt.Errorf("upload staging service is not configured")
	}
	return s.staging.StageBytesAs(fileName, content, source, owner)
}

func (s *AppService) CleanupUploadStaging() error {
	if s == nil || s.staging == nil {
		return fmt.Errorf("upload staging service is not configured")
	}
	return s.staging.CleanupExpired()
}

func (s *AppService) RegisterStagedUpload(uploadID, knowledgeBaseID, fileName string) (model.Document, error) {
	return s.RegisterStagedUploadAs(context.Background(), uploadID, knowledgeBaseID, fileName, AuthPrincipal{})
}

func (s *AppService) RegisterStagedUploadAs(ctx context.Context, uploadID, knowledgeBaseID, fileName string, owner AuthPrincipal) (model.Document, error) {
	if s == nil || s.staging == nil {
		return model.Document{}, fmt.Errorf("upload staging service is not configured")
	}
	ctx = normalizeServiceContext(ctx)
	if err := ctx.Err(); err != nil {
		return model.Document{}, err
	}
	resolvedKnowledgeBaseID, err := s.ResolveKnowledgeBaseID(knowledgeBaseID)
	if err != nil {
		return model.Document{}, err
	}
	staged, err := s.staging.ClaimAs(uploadID, owner)
	if err != nil {
		return model.Document{}, err
	}
	if err := ctx.Err(); err != nil {
		_ = s.staging.Release(staged.ID)
		return model.Document{}, err
	}
	permanentPath, err := s.staging.CopyTo(staged.ID, s.serverConfig.UploadDir)
	if err != nil {
		_ = s.staging.Release(staged.ID)
		return model.Document{}, err
	}
	documentName := strings.TrimSpace(fileName)
	if documentName == "" {
		documentName = staged.FileName
	}
	document := model.Document{
		ID:              util.NextID("doc"),
		KnowledgeBaseID: resolvedKnowledgeBaseID,
		Name:            documentName,
		Size:            staged.Size,
		SizeLabel:       staged.SizeLabel,
		UploadedAt:      util.NowRFC3339(),
		Status:          "processing",
		Source:          staged.Source,
		Version:         1,
		Checksum:        staged.SHA256,
		Path:            permanentPath,
		ContentPreview:  util.ExtractContentPreview(permanentPath),
	}
	if err := ctx.Err(); err != nil {
		_ = os.Remove(permanentPath)
		_ = s.staging.Release(staged.ID)
		return model.Document{}, err
	}
	uploaded, err := s.IndexDocumentWithContext(ctx, document)
	if err != nil {
		_ = os.Remove(permanentPath)
		_ = s.staging.Release(staged.ID)
		return model.Document{}, err
	}
	if err := s.staging.MarkConsumed(uploadID); err != nil {
		log.Printf("failed to mark staged upload consumed: %v", err)
	}
	if err := s.staging.Delete(uploadID); err != nil {
		log.Printf("failed to remove consumed staged upload: %v", err)
	}
	return uploaded, nil
}

func defaultMCPBasePath(basePath string) string {
	trimmed := strings.TrimSpace(basePath)
	if trimmed == "" {
		return "/mcp"
	}
	if !strings.HasPrefix(trimmed, "/") {
		return "/" + trimmed
	}
	return trimmed
}

func defaultRetrievalConfig(serverConfig model.ServerConfig) model.RetrievalConfig {
	return normalizeRetrievalConfig(model.RetrievalConfig{}, serverConfig)
}

func normalizeRetrievalConfig(cfg model.RetrievalConfig, serverConfig model.ServerConfig) model.RetrievalConfig {
	emptyConfig := strings.TrimSpace(cfg.DefaultSearchMode) == "" &&
		!cfg.HybridSearchEnabled &&
		strings.TrimSpace(cfg.RerankStrategy) == "" &&
		!cfg.EnableQueryRewrite &&
		cfg.QueryRewriteMaxVariants == 0 &&
		cfg.TopKDocument == 0 &&
		cfg.CandidateTopKDocument == 0 &&
		cfg.TopKKnowledgeBase == 0 &&
		cfg.CandidateTopKAllDocs == 0 &&
		cfg.MaxChunksPerDocument == 0 &&
		cfg.MaxContextChars == 0 &&
		!cfg.EnableLowConfidenceBoost
	mode := normalizeRetrievalMode(cfg.DefaultSearchMode)
	if mode == "auto" {
		mode = "dense"
		if serverConfig.EnableHybridSearch {
			mode = "hybrid"
		}
	}
	rerankStrategy := normalizeRerankStrategy(cfg.RerankStrategy)
	if rerankStrategy == "" {
		rerankStrategy = "keyword"
		if serverConfig.EnableSemanticReranker {
			rerankStrategy = "semantic"
		}
	}
	queryRewriteMaxVariants := cfg.QueryRewriteMaxVariants
	if queryRewriteMaxVariants <= 0 {
		queryRewriteMaxVariants = 3
	}
	topKDocument := cfg.TopKDocument
	if topKDocument <= 0 {
		topKDocument = serverConfig.RetrievalTopKDocument
	}
	if topKDocument <= 0 {
		topKDocument = ragSearchTopKDocument
	}
	candidateTopKDocument := cfg.CandidateTopKDocument
	if candidateTopKDocument <= 0 {
		candidateTopKDocument = serverConfig.RetrievalCandidateTopKDocument
	}
	if candidateTopKDocument <= 0 {
		candidateTopKDocument = ragSearchCandidateTopKDocument
	}
	topKKnowledgeBase := cfg.TopKKnowledgeBase
	if topKKnowledgeBase <= 0 {
		topKKnowledgeBase = serverConfig.RetrievalTopKKnowledgeBase
	}
	if topKKnowledgeBase <= 0 {
		topKKnowledgeBase = ragSearchTopKKnowledgeBase
	}
	candidateTopKAllDocs := cfg.CandidateTopKAllDocs
	if candidateTopKAllDocs <= 0 {
		candidateTopKAllDocs = serverConfig.RetrievalCandidateTopKAllDocs
	}
	if candidateTopKAllDocs <= 0 {
		candidateTopKAllDocs = ragSearchCandidateTopKAllDocs
	}
	maxChunksPerDocument := cfg.MaxChunksPerDocument
	if maxChunksPerDocument <= 0 {
		maxChunksPerDocument = serverConfig.RetrievalMaxChunksPerDocument
	}
	if maxChunksPerDocument <= 0 {
		maxChunksPerDocument = ragMaxChunksPerDocument
	}
	maxContextChars := cfg.MaxContextChars
	if maxContextChars <= 0 {
		maxContextChars = serverConfig.RetrievalMaxContextChars
	}
	if maxContextChars <= 0 {
		maxContextChars = 2400
	}
	hybridSearchEnabled := cfg.HybridSearchEnabled
	enableLowConfidenceBoost := cfg.EnableLowConfidenceBoost
	enableQueryRewrite := cfg.EnableQueryRewrite
	if emptyConfig {
		hybridSearchEnabled = serverConfig.EnableHybridSearch
		enableLowConfidenceBoost = serverConfig.RetrievalEnableAutoExpand
		enableQueryRewrite = serverConfig.EnableQueryRewrite
	}

	return model.RetrievalConfig{
		DefaultSearchMode:        mode,
		HybridSearchEnabled:      hybridSearchEnabled,
		RerankStrategy:           rerankStrategy,
		EnableQueryRewrite:       enableQueryRewrite,
		QueryRewriteMaxVariants:  minInt(maxInt(queryRewriteMaxVariants, 1), 5),
		TopKDocument:             topKDocument,
		CandidateTopKDocument:    maxInt(candidateTopKDocument, topKDocument),
		TopKKnowledgeBase:        topKKnowledgeBase,
		CandidateTopKAllDocs:     maxInt(candidateTopKAllDocs, topKKnowledgeBase),
		MaxChunksPerDocument:     maxChunksPerDocument,
		MaxContextChars:          maxContextChars,
		EnableLowConfidenceBoost: enableLowConfidenceBoost,
	}
}

func validateRetrievalConfig(cfg model.RetrievalConfig) error {
	if normalizeRerankStrategy(cfg.RerankStrategy) == "" {
		return fmt.Errorf("rerank strategy must be keyword or semantic")
	}
	if cfg.QueryRewriteMaxVariants < 1 || cfg.QueryRewriteMaxVariants > 5 {
		return fmt.Errorf("query rewrite max variants must be between 1 and 5")
	}
	if cfg.TopKDocument < 1 || cfg.TopKDocument > 30 {
		return fmt.Errorf("document topK must be between 1 and 30")
	}
	if cfg.CandidateTopKDocument < cfg.TopKDocument || cfg.CandidateTopKDocument > 80 {
		return fmt.Errorf("document candidate topK must be between document topK and 80")
	}
	if cfg.TopKKnowledgeBase < 1 || cfg.TopKKnowledgeBase > 40 {
		return fmt.Errorf("knowledge base topK must be between 1 and 40")
	}
	if cfg.CandidateTopKAllDocs < cfg.TopKKnowledgeBase || cfg.CandidateTopKAllDocs > 120 {
		return fmt.Errorf("knowledge base candidate topK must be between knowledge base topK and 120")
	}
	if cfg.MaxChunksPerDocument < 1 || cfg.MaxChunksPerDocument > 10 {
		return fmt.Errorf("max chunks per document must be between 1 and 10")
	}
	if cfg.MaxContextChars < 800 || cfg.MaxContextChars > 20000 {
		return fmt.Errorf("max context chars must be between 800 and 20000")
	}
	return nil
}

func generateMCPToken() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return util.NextID("mcp")
	}
	return "mcp_" + hex.EncodeToString(buffer)
}

func GenerateCSRFToken() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return "csrf_" + hex.EncodeToString(buffer), nil
}

func (s *AppService) CreateMCPDangerConfirmation(req model.MCPDangerConfirmationRequest) (model.MCPDangerConfirmationResponse, error) {
	return s.CreateMCPDangerConfirmationAs(req, AuthPrincipal{})
}

func (s *AppService) CreateMCPDangerConfirmationAs(req model.MCPDangerConfirmationRequest, owner AuthPrincipal) (model.MCPDangerConfirmationResponse, error) {
	if s == nil {
		return model.MCPDangerConfirmationResponse{}, fmt.Errorf("app service is nil")
	}
	toolName := strings.TrimSpace(req.ToolName)
	if toolName == "" {
		return model.MCPDangerConfirmationResponse{}, fmt.Errorf("toolName is required")
	}
	paramHash := strings.ToLower(strings.TrimSpace(req.ParamHash))
	if paramHash == "" {
		var err error
		paramHash, err = hashMCPDangerArguments(req.Arguments)
		if err != nil {
			return model.MCPDangerConfirmationResponse{}, err
		}
	}
	ttl := mcpDangerConfirmationDefaultTTL
	if req.TTLSeconds > 0 {
		ttl = time.Duration(req.TTLSeconds) * time.Second
		if ttl > mcpDangerConfirmationDefaultTTL {
			ttl = mcpDangerConfirmationDefaultTTL
		}
	}
	nonce := generateMCPConfirmNonce()
	expiresAt := time.Now().UTC().Add(ttl)
	ownerAPIKeyID := strings.TrimSpace(owner.APIKeyID)

	s.mcpDangerMu.Lock()
	if s.mcpDangerConfirms == nil {
		s.mcpDangerConfirms = map[string]mcpDangerConfirmationRecord{}
	}
	now := time.Now().UTC()
	s.pruneExpiredMCPDangerConfirmationsLocked(now)
	if err := s.checkMCPDangerConfirmationRateLocked(mcpDangerPrincipalKey(owner), now); err != nil {
		s.mcpDangerMu.Unlock()
		return model.MCPDangerConfirmationResponse{}, err
	}
	s.mcpDangerConfirms[nonce] = mcpDangerConfirmationRecord{
		Nonce:         nonce,
		ToolName:      toolName,
		ParamHash:     paramHash,
		ExpiresAt:     expiresAt,
		OwnerUserID:   strings.TrimSpace(owner.UserID),
		OwnerAPIKeyID: ownerAPIKeyID,
	}
	s.mcpDangerMu.Unlock()

	return model.MCPDangerConfirmationResponse{
		ConfirmNonce: nonce,
		ExpiresAt:    expiresAt.Format(time.RFC3339),
		ToolName:     toolName,
		ParamHash:    paramHash,
	}, nil
}

func (s *AppService) checkMCPDangerConfirmationRateLocked(principalKey string, now time.Time) error {
	if s.mcpDangerRates == nil {
		s.mcpDangerRates = map[string][]time.Time{}
	}
	cutoff := now.Add(-mcpDangerConfirmationRateWindow)
	for key, attempts := range s.mcpDangerRates {
		active := attempts[:0]
		for _, attempt := range attempts {
			if attempt.After(cutoff) {
				active = append(active, attempt)
			}
		}
		if len(active) == 0 {
			delete(s.mcpDangerRates, key)
			continue
		}
		s.mcpDangerRates[key] = active
	}
	attempts := s.mcpDangerRates[principalKey]
	active := attempts
	if len(active) >= mcpDangerConfirmationRateLimit {
		s.mcpDangerRates[principalKey] = active
		return fmt.Errorf("danger confirmation rate limit exceeded: maximum %d confirmations per minute", mcpDangerConfirmationRateLimit)
	}
	s.mcpDangerRates[principalKey] = append(active, now)
	return nil
}

func mcpDangerPrincipalKey(owner AuthPrincipal) string {
	if apiKeyID := strings.TrimSpace(owner.APIKeyID); apiKeyID != "" {
		return "api-key:" + apiKeyID
	}
	if userID := strings.TrimSpace(owner.UserID); userID != "" {
		return "user:" + userID
	}
	if authType := strings.TrimSpace(owner.AuthType); authType != "" {
		return "auth:" + authType
	}
	return "anonymous"
}

func (s *AppService) ConsumeMCPDangerConfirmation(toolName string, args map[string]any, nonce string) error {
	return s.ConsumeMCPDangerConfirmationAs(toolName, args, nonce, AuthPrincipal{})
}

func (s *AppService) ConsumeMCPDangerConfirmationAs(toolName string, args map[string]any, nonce string, owner AuthPrincipal) error {
	if s == nil {
		return fmt.Errorf("app service is nil")
	}
	toolName = strings.TrimSpace(toolName)
	nonce = strings.TrimSpace(nonce)
	if nonce == "" {
		return fmt.Errorf("dangerous tool requires confirmNonce")
	}
	paramHash, err := hashMCPDangerArguments(args)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	s.mcpDangerMu.Lock()
	defer s.mcpDangerMu.Unlock()
	if s.mcpDangerConfirms == nil {
		s.mcpDangerConfirms = map[string]mcpDangerConfirmationRecord{}
	}
	record, ok := s.mcpDangerConfirms[nonce]
	if !ok {
		return fmt.Errorf("invalid or used danger confirmation nonce")
	}
	delete(s.mcpDangerConfirms, nonce)
	if !record.ExpiresAt.After(now) {
		s.pruneExpiredMCPDangerConfirmationsLocked(now)
		return fmt.Errorf("expired danger confirmation nonce")
	}
	if record.ToolName != toolName || record.ParamHash != paramHash {
		s.pruneExpiredMCPDangerConfirmationsLocked(now)
		return fmt.Errorf("invalid danger confirmation nonce")
	}
	if !mcpDangerOwnerMatches(record, owner) {
		s.pruneExpiredMCPDangerConfirmationsLocked(now)
		return fmt.Errorf("danger confirmation belongs to another principal")
	}
	s.pruneExpiredMCPDangerConfirmationsLocked(now)
	return nil
}

func mcpDangerOwnerMatches(record mcpDangerConfirmationRecord, owner AuthPrincipal) bool {
	if record.OwnerAPIKeyID != "" {
		return strings.TrimSpace(owner.APIKeyID) == record.OwnerAPIKeyID
	}
	if record.OwnerUserID != "" {
		return strings.TrimSpace(owner.UserID) == record.OwnerUserID
	}
	return strings.TrimSpace(owner.AuthType) == ""
}

func (s *AppService) pruneExpiredMCPDangerConfirmationsLocked(now time.Time) {
	for nonce, record := range s.mcpDangerConfirms {
		if !record.ExpiresAt.After(now) {
			delete(s.mcpDangerConfirms, nonce)
		}
	}
}

func generateMCPConfirmNonce() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return util.NextID("mcp_confirm")
	}
	return "mcp_confirm_" + hex.EncodeToString(buffer)
}

func hashMCPDangerArguments(args map[string]any) (string, error) {
	// The nonce is a transport-level proof and must not change the hash of the
	// business arguments it confirms.
	arguments := make(map[string]any, len(args))
	for key, value := range args {
		if strings.EqualFold(strings.TrimSpace(key), "confirmNonce") {
			continue
		}
		arguments[key] = value
	}
	sanitized := sanitizeMCPDangerValue(arguments)
	encoded, err := json.Marshal(sanitized)
	if err != nil {
		return "", fmt.Errorf("hash danger confirmation arguments: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func sanitizeMCPDangerValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, item := range typed {
			if key == "confirmNonce" {
				continue
			}
			cloned[key] = sanitizeMCPDangerValue(item)
		}
		return cloned
	case []any:
		cloned := make([]any, len(typed))
		for index, item := range typed {
			cloned[index] = sanitizeMCPDangerValue(item)
		}
		return cloned
	default:
		return value
	}
}

func (s *AppService) StartBatchIndexJob(knowledgeBaseID string, uploadIDs []string, concurrency int) (model.MCPJob, error) {
	return s.StartBatchIndexJobAs(knowledgeBaseID, uploadIDs, concurrency, AuthPrincipal{})
}

func (s *AppService) StartBatchIndexJobAs(knowledgeBaseID string, uploadIDs []string, concurrency int, owner AuthPrincipal) (model.MCPJob, error) {
	if s == nil {
		return model.MCPJob{}, fmt.Errorf("app service is nil")
	}
	knowledgeBaseID = strings.TrimSpace(knowledgeBaseID)
	if knowledgeBaseID == "" {
		return model.MCPJob{}, fmt.Errorf("knowledgeBaseId is required")
	}
	if _, err := s.ResolveKnowledgeBaseID(knowledgeBaseID); err != nil {
		return model.MCPJob{}, err
	}
	if len(uploadIDs) == 0 {
		return model.MCPJob{}, fmt.Errorf("uploadIds cannot be empty")
	}
	if concurrency <= 0 {
		concurrency = 3
	}
	if concurrency > 10 {
		concurrency = 10
	}

	ids := make([]string, 0, len(uploadIDs))
	for _, uploadID := range uploadIDs {
		trimmedID := strings.TrimSpace(uploadID)
		if trimmedID != "" {
			ids = append(ids, trimmedID)
		}
	}
	if len(ids) == 0 {
		return model.MCPJob{}, fmt.Errorf("uploadIds cannot be empty")
	}

	now := util.NowRFC3339()
	job := model.MCPJob{
		ID:            util.NextID("job"),
		Type:          "batch-index",
		Status:        "queued",
		Progress:      0,
		Summary:       fmt.Sprintf("准备索引 %d 个文档。", len(ids)),
		CreatedAt:     now,
		UpdatedAt:     now,
		OwnerUserID:   strings.TrimSpace(owner.UserID),
		OwnerAPIKeyID: strings.TrimSpace(owner.APIKeyID),
	}
	ctx, cancel := context.WithCancel(context.Background())

	if !s.registerMCPJob(job, cancel) {
		cancel()
		return model.MCPJob{}, fmt.Errorf("mcp jobs are shutting down")
	}

	go func() {
		defer s.mcpJobWG.Done()
		s.runBatchIndexJob(ctx, job.ID, knowledgeBaseID, ids, concurrency, owner)
	}()
	return job, nil
}

func (s *AppService) runBatchIndexJob(ctx context.Context, jobID, knowledgeBaseID string, uploadIDs []string, concurrency int, owner AuthPrincipal) {
	s.updateMCPJob(jobID, func(job *model.MCPJob) {
		job.Status = "running"
		job.Progress = 5
		job.Summary = fmt.Sprintf("正在索引 %d 个文档。", len(uploadIDs))
	})

	type result struct {
		value map[string]any
		ok    bool
	}
	sem := make(chan struct{}, concurrency)
	results := make(chan result, len(uploadIDs))
	var wg sync.WaitGroup
	for _, uploadID := range uploadIDs {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			select {
			case <-ctx.Done():
				results <- result{value: map[string]any{"uploadId": id, "success": false, "error": "job cancelled"}}
			case sem <- struct{}{}:
				defer func() { <-sem }()
				document, err := s.RegisterStagedUploadAs(ctx, id, knowledgeBaseID, "", owner)
				if err != nil {
					results <- result{value: map[string]any{"uploadId": id, "success": false, "error": err.Error()}}
					return
				}
				results <- result{value: map[string]any{
					"uploadId":   id,
					"documentId": document.ID,
					"fileName":   document.Name,
					"success":    true,
					"document":   document,
				}, ok: true}
			}
		}(uploadID)
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	items := make([]map[string]any, 0, len(uploadIDs))
	successful := 0
	for item := range results {
		items = append(items, item.value)
		if item.ok {
			successful++
		}
		completed := len(items)
		progress := 10 + completed*85/len(uploadIDs)
		s.updateMCPJob(jobID, func(job *model.MCPJob) {
			job.Progress = progress
			job.Summary = fmt.Sprintf("已完成 %d/%d 个文档。", completed, len(uploadIDs))
		})
	}

	failed := len(items) - successful
	resultData := map[string]any{
		"total": len(uploadIDs), "successful": successful, "failed": failed, "results": items,
	}
	if ctx.Err() != nil {
		s.completeMCPJob(jobID, "cancelled", 0, "批量索引任务已取消。", resultData, "")
		return
	}
	status := "succeeded"
	if failed > 0 {
		status = "failed"
	}
	s.completeMCPJob(jobID, status, 100, fmt.Sprintf("批量索引完成，成功 %d 个，失败 %d 个。", successful, failed), resultData, "")
}

func (s *AppService) StartMCPImportJob(req model.MCPStartImportJobRequest) (model.MCPJob, error) {
	return s.StartMCPImportJobAs(req, AuthPrincipal{})
}

func (s *AppService) StartMCPImportJobAs(req model.MCPStartImportJobRequest, owner AuthPrincipal) (model.MCPJob, error) {
	if s == nil {
		return model.MCPJob{}, fmt.Errorf("app service is nil")
	}
	switch strings.ToLower(strings.TrimSpace(req.JobType)) {
	case "", "import":
		// Preserve the original text import contract.
	case "reindex":
		return s.startMCPReindexJob(req, owner)
	case "eval_dataset":
		return s.startMCPEvalDatasetJob(req, owner)
	case "batch_index":
		return s.StartBatchIndexJobAs(req.KnowledgeBaseID, req.UploadIDs, req.Concurrency, owner)
	default:
		return model.MCPJob{}, fmt.Errorf("unsupported MCP job type: %s", req.JobType)
	}
	knowledgeBaseID := strings.TrimSpace(req.KnowledgeBaseID)
	fileName := strings.TrimSpace(req.FileName)
	if knowledgeBaseID == "" {
		return model.MCPJob{}, fmt.Errorf("knowledgeBaseId is required")
	}
	if fileName == "" {
		return model.MCPJob{}, fmt.Errorf("fileName is required")
	}
	if int64(len(req.Content)) > mcpImportJobContentLimit {
		return model.MCPJob{}, fmt.Errorf("inline import content too large: current=%s, max=%s; please POST file stream to /api/uploads first, then call register_staged_upload", util.FormatFileSize(int64(len(req.Content))), util.FormatFileSize(mcpImportJobContentLimit))
	}

	now := util.NowRFC3339()
	job := model.MCPJob{
		ID:            util.NextID("job"),
		Type:          "import",
		Status:        "queued",
		Progress:      0,
		Summary:       fmt.Sprintf("准备导入《%s》。", fileName),
		CreatedAt:     now,
		UpdatedAt:     now,
		OwnerUserID:   strings.TrimSpace(owner.UserID),
		OwnerAPIKeyID: strings.TrimSpace(owner.APIKeyID),
	}
	ctx, cancel := context.WithCancel(context.Background())

	if !s.registerMCPJob(job, cancel) {
		cancel()
		return model.MCPJob{}, fmt.Errorf("mcp jobs are shutting down")
	}

	go func() {
		defer s.mcpJobWG.Done()
		s.runMCPImportJob(ctx, job.ID, model.MCPStartImportJobRequest{
			KnowledgeBaseID: knowledgeBaseID,
			FileName:        fileName,
			Content:         req.Content,
		}, owner)
	}()

	return job, nil
}

func (s *AppService) startMCPReindexJob(req model.MCPStartImportJobRequest, owner AuthPrincipal) (model.MCPJob, error) {
	knowledgeBaseID := strings.TrimSpace(req.KnowledgeBaseID)
	documentID := strings.TrimSpace(req.DocumentID)
	if knowledgeBaseID == "" {
		return model.MCPJob{}, fmt.Errorf("knowledgeBaseId is required")
	}
	if documentID == "" {
		return model.MCPJob{}, fmt.Errorf("documentId is required")
	}
	if _, err := s.ResolveKnowledgeBaseID(knowledgeBaseID); err != nil {
		return model.MCPJob{}, err
	}

	now := util.NowRFC3339()
	job := model.MCPJob{
		ID:            util.NextID("job"),
		Type:          "reindex",
		Status:        "queued",
		Summary:       fmt.Sprintf("准备重建文档 %s 的索引。", documentID),
		CreatedAt:     now,
		UpdatedAt:     now,
		OwnerUserID:   strings.TrimSpace(owner.UserID),
		OwnerAPIKeyID: strings.TrimSpace(owner.APIKeyID),
	}
	ctx, cancel := context.WithCancel(context.Background())
	if !s.registerMCPJob(job, cancel) {
		cancel()
		return model.MCPJob{}, fmt.Errorf("mcp jobs are shutting down")
	}
	go func() {
		defer s.mcpJobWG.Done()
		s.runMCPReindexJob(ctx, job.ID, knowledgeBaseID, documentID)
	}()
	return job, nil
}

func (s *AppService) runMCPReindexJob(ctx context.Context, jobID, knowledgeBaseID, documentID string) {
	s.updateMCPJob(jobID, func(job *model.MCPJob) {
		job.Status = "running"
		job.Progress = 10
		job.Summary = fmt.Sprintf("正在重建文档 %s 的索引。", documentID)
	})
	if err := ctx.Err(); err != nil {
		s.completeMCPJob(jobID, "cancelled", 0, "重建索引任务已取消。", nil, "")
		return
	}
	document, err := s.ReindexDocumentWithContext(ctx, knowledgeBaseID, documentID)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			s.completeMCPJob(jobID, "cancelled", 10, "重建索引任务已取消。", nil, "")
			return
		}
		s.completeMCPJob(jobID, "failed", 100, "重建索引任务失败。", nil, err.Error())
		return
	}
	s.completeMCPJob(jobID, "succeeded", 100, fmt.Sprintf("文档《%s》索引重建完成。", document.Name), map[string]any{
		"document":        document,
		"knowledgeBaseId": knowledgeBaseID,
	}, "")
}

func (s *AppService) startMCPEvalDatasetJob(req model.MCPStartImportJobRequest, owner AuthPrincipal) (model.MCPJob, error) {
	now := util.NowRFC3339()
	job := model.MCPJob{
		ID:            util.NextID("job"),
		Type:          "eval-dataset",
		Status:        "queued",
		Summary:       "准备生成评估数据集。",
		CreatedAt:     now,
		UpdatedAt:     now,
		OwnerUserID:   strings.TrimSpace(owner.UserID),
		OwnerAPIKeyID: strings.TrimSpace(owner.APIKeyID),
	}
	ctx, cancel := context.WithCancel(context.Background())
	if !s.registerMCPJob(job, cancel) {
		cancel()
		return model.MCPJob{}, fmt.Errorf("mcp jobs are shutting down")
	}
	evalRequest := model.GenerateEvalDatasetRequest{
		KnowledgeBaseID: strings.TrimSpace(req.KnowledgeBaseID),
		DocumentID:      strings.TrimSpace(req.DocumentID),
		MaxPerDocument:  req.MaxPerDocument,
	}
	go func() {
		defer s.mcpJobWG.Done()
		s.runMCPEvalDatasetJob(ctx, job.ID, evalRequest)
	}()
	return job, nil
}

func (s *AppService) runMCPEvalDatasetJob(ctx context.Context, jobID string, req model.GenerateEvalDatasetRequest) {
	s.updateMCPJob(jobID, func(job *model.MCPJob) {
		job.Status = "running"
		job.Progress = 10
		job.Summary = "正在生成评估数据集。"
	})
	response, err := s.GenerateEvalDatasetWithContext(ctx, req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			s.completeMCPJob(jobID, "cancelled", 10, "评估数据集任务已取消。", nil, "")
			return
		}
		s.completeMCPJob(jobID, "failed", 100, "评估数据集任务失败。", nil, err.Error())
		return
	}
	s.completeMCPJob(jobID, "succeeded", 100, fmt.Sprintf("评估数据集生成完成，共 %d 条样本。", response.Count), map[string]any{
		"dataset": response,
	}, "")
}

func (s *AppService) registerMCPJob(job model.MCPJob, cancel context.CancelFunc) bool {
	if s == nil {
		return false
	}
	s.mcpJobLifecycleMu.Lock()
	defer s.mcpJobLifecycleMu.Unlock()
	if s.mcpJobsShutdown {
		return false
	}

	s.mcpJobMu.Lock()
	defer s.mcpJobMu.Unlock()
	if s.mcpJobs == nil {
		s.mcpJobs = map[string]model.MCPJob{}
	}
	if s.mcpJobCancels == nil {
		s.mcpJobCancels = map[string]context.CancelFunc{}
	}
	s.mcpJobs[job.ID] = job
	s.mcpJobCancels[job.ID] = cancel
	s.pruneMCPJobsLocked()
	s.mcpJobWG.Add(1)
	return true
}

// ShutdownJobs cancels all in-memory MCP jobs and waits for their workers to exit.
func (s *AppService) ShutdownJobs(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mcpJobLifecycleMu.Lock()
	s.mcpJobsShutdown = true
	s.mcpJobLifecycleMu.Unlock()

	s.mcpJobMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.mcpJobCancels))
	for _, cancel := range s.mcpJobCancels {
		if cancel != nil {
			cancels = append(cancels, cancel)
		}
	}
	s.mcpJobMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}

	done := make(chan struct{})
	go func() {
		s.mcpJobWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *AppService) runMCPImportJob(ctx context.Context, jobID string, req model.MCPStartImportJobRequest, owner AuthPrincipal) {
	s.updateMCPJob(jobID, func(job *model.MCPJob) {
		job.Status = "running"
		job.Progress = 10
		job.Summary = fmt.Sprintf("正在导入《%s》。", req.FileName)
	})

	select {
	case <-ctx.Done():
		s.completeMCPJob(jobID, "cancelled", 0, "导入任务已取消。", nil, "")
		return
	case <-time.After(50 * time.Millisecond):
	}

	if strings.TrimSpace(req.Content) == "" {
		s.completeMCPJob(jobID, "failed", 100, "导入任务失败。", nil, "content is required")
		return
	}
	if err := validateMCPJobTextFileName(req.FileName, s.GetConfig()); err != nil {
		s.completeMCPJob(jobID, "failed", 100, "导入任务失败。", nil, err.Error())
		return
	}

	s.updateMCPJob(jobID, func(job *model.MCPJob) {
		job.Progress = 45
		job.Summary = "文件已进入暂存。"
	})
	staged, err := s.StageInlineUploadAs(req.FileName, []byte(req.Content), "mcp-job-import", owner)
	if err != nil {
		s.completeMCPJob(jobID, "failed", 100, "导入任务失败。", nil, err.Error())
		return
	}

	select {
	case <-ctx.Done():
		s.completeMCPJob(jobID, "cancelled", 50, "导入任务已取消。", nil, "")
		return
	default:
	}

	s.updateMCPJob(jobID, func(job *model.MCPJob) {
		job.Progress = 70
		job.Summary = "正在注册并索引文档。"
	})
	select {
	case <-ctx.Done():
		s.completeMCPJob(jobID, "cancelled", 70, "导入任务已取消。", nil, "")
		return
	default:
	}
	uploaded, err := s.RegisterStagedUploadAs(ctx, staged.ID, req.KnowledgeBaseID, req.FileName, owner)
	if err != nil {
		s.completeMCPJob(jobID, "failed", 100, "导入任务失败。", nil, err.Error())
		return
	}
	select {
	case <-ctx.Done():
		s.completeMCPJob(jobID, "cancelled", 100, "导入任务已取消。", nil, "")
		return
	default:
	}
	s.completeMCPJob(jobID, "succeeded", 100, fmt.Sprintf("文档《%s》导入完成。", uploaded.Name), map[string]any{
		"uploaded":        uploaded,
		"knowledgeBaseId": uploaded.KnowledgeBaseID,
		"stagedUploadId":  staged.ID,
	}, "")
}

func (s *AppService) GetMCPJobStatus(jobID string) (model.MCPJob, error) {
	return s.GetMCPJobStatusAs(jobID, AuthPrincipal{})
}

func (s *AppService) GetMCPJobStatusAs(jobID string, owner AuthPrincipal) (model.MCPJob, error) {
	if s == nil {
		return model.MCPJob{}, fmt.Errorf("app service is nil")
	}
	jobID = strings.TrimSpace(jobID)
	s.mcpJobMu.Lock()
	defer s.mcpJobMu.Unlock()
	job, ok := s.mcpJobs[jobID]
	if !ok {
		return model.MCPJob{}, fmt.Errorf("job not found")
	}
	if !mcpJobOwnerMatches(job, owner) {
		return model.MCPJob{}, fmt.Errorf("job is not owned by this principal")
	}
	return cloneMCPJob(job), nil
}

func (s *AppService) CancelMCPJob(jobID string) (model.MCPJob, error) {
	return s.CancelMCPJobAs(jobID, AuthPrincipal{})
}

func (s *AppService) CancelMCPJobAs(jobID string, owner AuthPrincipal) (model.MCPJob, error) {
	if s == nil {
		return model.MCPJob{}, fmt.Errorf("app service is nil")
	}
	jobID = strings.TrimSpace(jobID)
	s.mcpJobMu.Lock()
	job, ok := s.mcpJobs[jobID]
	if !ok {
		s.mcpJobMu.Unlock()
		return model.MCPJob{}, fmt.Errorf("job not found")
	}
	if !mcpJobOwnerMatches(job, owner) {
		s.mcpJobMu.Unlock()
		return model.MCPJob{}, fmt.Errorf("job is not owned by this principal")
	}
	cancel := s.mcpJobCancels[jobID]
	if job.Status == "queued" || job.Status == "running" {
		job.Status = "cancelled"
		job.Summary = "任务已取消。"
		job.Warnings = appendMCPJobWarning(job.Warnings, mcpJobCancelWarning)
		job.UpdatedAt = util.NowRFC3339()
		job.CompletedAt = job.UpdatedAt
		s.mcpJobs[jobID] = job
		delete(s.mcpJobCancels, jobID)
	}
	s.mcpJobMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return cloneMCPJob(job), nil
}

func (s *AppService) ListRecentMCPJobs(limit int) []model.MCPJob {
	return s.ListRecentMCPJobsAs(limit, AuthPrincipal{})
}

func (s *AppService) ListRecentMCPJobsAs(limit int, owner AuthPrincipal) []model.MCPJob {
	if s == nil {
		return nil
	}
	if limit <= 0 || limit > 20 {
		limit = 20
	}
	s.mcpJobMu.Lock()
	defer s.mcpJobMu.Unlock()
	items := make([]model.MCPJob, 0, len(s.mcpJobs))
	for _, job := range s.mcpJobs {
		if !mcpJobOwnerMatches(job, owner) {
			continue
		}
		items = append(items, cloneMCPJob(job))
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt > items[j].UpdatedAt
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func mcpJobOwnerMatches(job model.MCPJob, owner AuthPrincipal) bool {
	if strings.TrimSpace(owner.AuthType) == "" {
		return true
	}
	if hasScope(owner.Scopes, "mcp:admin") {
		return true
	}
	if owner.AuthType == "api_key" {
		return strings.TrimSpace(job.OwnerAPIKeyID) != "" && strings.TrimSpace(job.OwnerAPIKeyID) == strings.TrimSpace(owner.APIKeyID)
	}
	return strings.TrimSpace(job.OwnerAPIKeyID) == "" && strings.TrimSpace(job.OwnerUserID) != "" && strings.TrimSpace(job.OwnerUserID) == strings.TrimSpace(owner.UserID)
}

func (s *AppService) updateMCPJob(jobID string, update func(*model.MCPJob)) {
	s.mcpJobMu.Lock()
	defer s.mcpJobMu.Unlock()
	job, ok := s.mcpJobs[jobID]
	if !ok {
		return
	}
	if isMCPJobTerminalStatus(job.Status) {
		return
	}
	update(&job)
	job.UpdatedAt = util.NowRFC3339()
	s.mcpJobs[jobID] = job
}

func (s *AppService) completeMCPJob(jobID, status string, progress int, summary string, result map[string]any, errorMessage string) {
	s.mcpJobMu.Lock()
	defer s.mcpJobMu.Unlock()
	job, ok := s.mcpJobs[jobID]
	if !ok {
		return
	}
	if isMCPJobTerminalStatus(job.Status) {
		return
	}
	job.Status = status
	job.Progress = progress
	job.Summary = summary
	job.Result = result
	job.Error = errorMessage
	if status == "cancelled" {
		job.Warnings = appendMCPJobWarning(job.Warnings, mcpJobCancelWarning)
	}
	job.UpdatedAt = util.NowRFC3339()
	job.CompletedAt = job.UpdatedAt
	s.mcpJobs[jobID] = job
	delete(s.mcpJobCancels, jobID)
}

func isMCPJobTerminalStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "succeeded", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func appendMCPJobWarning(warnings []string, warning string) []string {
	warning = strings.TrimSpace(warning)
	if warning == "" {
		return warnings
	}
	for _, item := range warnings {
		if item == warning {
			return warnings
		}
	}
	return append(warnings, warning)
}

func (s *AppService) pruneMCPJobsLocked() {
	if len(s.mcpJobs) <= 50 {
		return
	}
	terminal := make([]model.MCPJob, 0, len(s.mcpJobs))
	for _, job := range s.mcpJobs {
		if isMCPJobTerminalStatus(job.Status) {
			terminal = append(terminal, job)
		}
	}
	sort.Slice(terminal, func(i, j int) bool {
		return terminal[i].UpdatedAt < terminal[j].UpdatedAt
	})
	removeCount := len(s.mcpJobs) - 50
	if removeCount > len(terminal) {
		removeCount = len(terminal)
	}
	for _, job := range terminal[:removeCount] {
		delete(s.mcpJobs, job.ID)
		delete(s.mcpJobCancels, job.ID)
	}
}

func cloneMCPJob(job model.MCPJob) model.MCPJob {
	if job.Result != nil {
		result := make(map[string]any, len(job.Result))
		for key, value := range job.Result {
			result[key] = value
		}
		job.Result = result
	}
	job.Warnings = append([]string(nil), job.Warnings...)
	return job
}

func validateMCPJobTextFileName(fileName string, cfg model.AppConfig) error {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(fileName)))
	allowed := map[string]struct{}{
		".txt": {},
		".md":  {},
		".csv": {},
	}
	if _, ok := allowed[ext]; !ok {
		if ext == "" {
			return fmt.Errorf("unsupported text upload type: missing extension, allowed types are .txt, .md, .csv")
		}
		return fmt.Errorf("unsupported text upload type: %s, allowed types are .txt, .md, .csv", ext)
	}
	if IsSensitiveStructuredFileExtension(ext) && !IsLocalOllamaConfig(cfg.Chat, cfg.Embedding) {
		return fmt.Errorf("sensitive structured file type %s requires local ollama for both chat and embedding", ext)
	}
	return nil
}

func IsSensitiveStructuredFileExtension(ext string) bool {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".csv", ".xlsx":
		return true
	default:
		return false
	}
}

func IsLocalOllamaConfig(chat model.ChatConfig, embedding model.EmbeddingConfig) bool {
	return strings.EqualFold(strings.TrimSpace(chat.Provider), "ollama") && strings.EqualFold(strings.TrimSpace(embedding.Provider), "ollama")
}

func (s *AppService) hasSensitiveStructuredDocuments() bool {
	if s == nil || s.state == nil {
		return false
	}

	s.state.Mu.RLock()
	defer s.state.Mu.RUnlock()
	for _, kb := range s.state.KnowledgeBases {
		for _, document := range kb.Documents {
			if IsSensitiveStructuredFileExtension(filepath.Ext(document.Name)) {
				return true
			}
		}
	}
	return false
}

func (s *AppService) defaultBaseURL(provider string) string {
	if provider == "ollama" {
		return s.serverConfig.OllamaBaseURL
	}
	return s.serverConfig.OllamaBaseURL + "/v1"
}

func (s *AppService) UpdateConfig(req model.ConfigUpdateRequest) (model.AppConfig, error) {
	s.state.Mu.RLock()
	previousConfig := s.state.Config
	s.state.Mu.RUnlock()

	chatProvider := strings.TrimSpace(req.Chat.Provider)
	chatBaseURL := strings.TrimSpace(req.Chat.BaseURL)
	chatModel := strings.TrimSpace(req.Chat.Model)
	chatAPIKey := strings.TrimSpace(req.Chat.APIKey)
	if req.Chat.ClearAPIKey {
		chatAPIKey = ""
	} else if chatAPIKey == "" && req.Chat.APIKeyConfigured {
		chatAPIKey = strings.TrimSpace(previousConfig.Chat.APIKey)
	}

	if chatProvider == "" || chatModel == "" {
		return model.AppConfig{}, fmt.Errorf("chat provider and model are required")
	}
	if chatBaseURL == "" {
		chatBaseURL = s.defaultBaseURL(chatProvider)
	}
	if req.Chat.Temperature < 0 || req.Chat.Temperature > 2 {
		return model.AppConfig{}, fmt.Errorf("chat temperature must be between 0 and 2")
	}
	knowledgeTemperature := req.Chat.KnowledgeTemperature
	if knowledgeTemperature == 0 {
		knowledgeTemperature = defaultKnowledgeTemperature
	}
	if knowledgeTemperature < defaultKnowledgeTemperature || knowledgeTemperature > maxKnowledgeTemperature {
		return model.AppConfig{}, fmt.Errorf("knowledge temperature must be between 0.1 and 0.5")
	}

	embedProvider := strings.TrimSpace(req.Embedding.Provider)
	embedBaseURL := strings.TrimSpace(req.Embedding.BaseURL)
	embedModel := strings.TrimSpace(req.Embedding.Model)
	embedAPIKey := strings.TrimSpace(req.Embedding.APIKey)
	if req.Embedding.ClearAPIKey {
		embedAPIKey = ""
	} else if embedAPIKey == "" && req.Embedding.APIKeyConfigured {
		embedAPIKey = strings.TrimSpace(previousConfig.Embedding.APIKey)
	}

	if embedProvider == "" || embedModel == "" {
		return model.AppConfig{}, fmt.Errorf("embedding provider and model are required")
	}
	if embedBaseURL == "" {
		embedBaseURL = s.defaultBaseURL(embedProvider)
	}

	contextMessageLimit := req.Chat.ContextMessageLimit
	if contextMessageLimit <= 0 {
		contextMessageLimit = 12
	}
	if contextMessageLimit > 100 {
		return model.AppConfig{}, fmt.Errorf("context message limit must be between 1 and 100")
	}

	mcpBasePath := defaultMCPBasePath(req.MCP.BasePath)
	mcpToken := strings.TrimSpace(req.MCP.Token)
	if mcpToken == "" && req.MCP.TokenConfigured {
		mcpToken = strings.TrimSpace(previousConfig.MCP.Token)
	}
	if mcpToken == "" {
		mcpToken = generateMCPToken()
	}

	nextConfig := model.AppConfig{
		Chat: model.ChatConfig{
			Provider:             chatProvider,
			BaseURL:              chatBaseURL,
			Model:                chatModel,
			APIKey:               chatAPIKey,
			APIKeyConfigured:     chatAPIKey != "",
			Temperature:          req.Chat.Temperature,
			KnowledgeTemperature: knowledgeTemperature,
			ContextMessageLimit:  contextMessageLimit,
		},
		Embedding: model.EmbeddingConfig{
			Provider:         embedProvider,
			BaseURL:          embedBaseURL,
			Model:            embedModel,
			APIKey:           embedAPIKey,
			APIKeyConfigured: embedAPIKey != "",
		},
		MCP: model.MCPConfig{
			Enabled:            req.MCP.Enabled,
			BasePath:           mcpBasePath,
			Token:              mcpToken,
			TokenConfigured:    mcpToken != "",
			LegacyTokenEnabled: s.serverConfig.EnableMCPLegacyToken,
		},
		Retrieval: normalizeRetrievalConfig(req.Retrieval, s.serverConfig),
	}
	if err := validateRetrievalConfig(nextConfig.Retrieval); err != nil {
		return model.AppConfig{}, err
	}
	if s.hasSensitiveStructuredDocuments() && !IsLocalOllamaConfig(nextConfig.Chat, nextConfig.Embedding) {
		return model.AppConfig{}, fmt.Errorf("sensitive structured documents require local ollama for both chat and embedding")
	}

	s.state.Mu.Lock()
	s.state.Config = nextConfig
	s.state.Mu.Unlock()

	if err := s.saveState(); err != nil {
		s.state.Mu.Lock()
		s.state.Config = previousConfig
		s.state.Mu.Unlock()
		return model.AppConfig{}, err
	}
	return nextConfig, nil
}

func (s *AppService) ResetMCPToken() (model.MCPConfig, error) {
	if s == nil {
		return model.MCPConfig{}, fmt.Errorf("app service is nil")
	}
	if !s.serverConfig.EnableMCPLegacyToken {
		return model.MCPConfig{}, fmt.Errorf("mcp legacy token authentication is disabled")
	}

	s.state.Mu.Lock()
	previousConfig := s.state.Config
	nextConfig := s.state.Config
	nextConfig.MCP.Enabled = s.serverConfig.EnableMCP
	nextConfig.MCP.BasePath = defaultMCPBasePath(nextConfig.MCP.BasePath)
	nextConfig.MCP.Token = generateMCPToken()
	nextConfig.MCP.TokenConfigured = true
	nextConfig.MCP.LegacyTokenEnabled = s.serverConfig.EnableMCPLegacyToken
	s.state.Config = nextConfig
	s.state.Mu.Unlock()

	if err := s.saveState(); err != nil {
		s.state.Mu.Lock()
		s.state.Config = previousConfig
		s.state.Mu.Unlock()
		return model.MCPConfig{}, err
	}

	return nextConfig.MCP, nil
}

func (s *AppService) ListKnowledgeBases() []model.KnowledgeBase {
	s.state.Mu.RLock()
	knowledgeBases := make([]model.KnowledgeBase, 0, len(s.state.KnowledgeBases))
	for _, kb := range s.state.KnowledgeBases {
		knowledgeBases = append(knowledgeBases, publicKnowledgeBase(kb))
	}
	s.state.Mu.RUnlock()

	sort.Slice(knowledgeBases, func(i, j int) bool {
		return knowledgeBases[i].CreatedAt > knowledgeBases[j].CreatedAt
	})

	return knowledgeBases
}

func (s *AppService) CreateKnowledgeBase(req model.KnowledgeBaseInput) (model.KnowledgeBase, error) {
	if strings.TrimSpace(req.Name) == "" {
		return model.KnowledgeBase{}, fmt.Errorf("knowledge base name is required")
	}

	knowledgeBase := model.KnowledgeBase{
		ID:                  util.NextID("kb"),
		Name:                strings.TrimSpace(req.Name),
		Description:         strings.TrimSpace(req.Description),
		Tags:                normalizeKnowledgeBaseTags(req.Tags),
		Documents:           []model.Document{},
		CreatedAt:           time.Now().UTC().Format(time.RFC3339),
		UpdatedAt:           time.Now().UTC().Format(time.RFC3339),
		CurrentIndexVersion: currentIndexVersion,
	}

	s.state.Mu.Lock()
	s.state.KnowledgeBases[knowledgeBase.ID] = knowledgeBase
	s.state.Mu.Unlock()

	if err := s.saveState(); err != nil {
		s.state.Mu.Lock()
		delete(s.state.KnowledgeBases, knowledgeBase.ID)
		s.state.Mu.Unlock()
		return model.KnowledgeBase{}, err
	}

	if err := s.ensureKnowledgeBaseCollection(knowledgeBase.ID); err != nil {
		s.state.Mu.Lock()
		delete(s.state.KnowledgeBases, knowledgeBase.ID)
		s.state.Mu.Unlock()
		return model.KnowledgeBase{}, err
	}

	return knowledgeBase, nil
}

func (s *AppService) DeleteKnowledgeBase(id string) (int, error) {
	s.state.Mu.Lock()
	if _, ok := s.state.KnowledgeBases[id]; !ok {
		s.state.Mu.Unlock()
		return 0, fmt.Errorf("knowledge base not found")
	}

	removedKnowledgeBase := s.state.KnowledgeBases[id]
	delete(s.state.KnowledgeBases, id)
	if s.state.EvalDatasets == nil {
		s.state.EvalDatasets = map[string]model.EvalDataset{}
	}
	if s.state.EvalRuns == nil {
		s.state.EvalRuns = map[string]model.RunEvalDatasetResponse{}
	}
	removedEvalDatasets := make(map[string]model.EvalDataset)
	for datasetID, dataset := range s.state.EvalDatasets {
		if dataset.KnowledgeBaseID == id {
			removedEvalDatasets[datasetID] = dataset
			delete(s.state.EvalDatasets, datasetID)
		}
	}
	removedEvalRuns := make(map[string]model.RunEvalDatasetResponse)
	for runID, run := range s.state.EvalRuns {
		if run.KnowledgeBaseID == id {
			removedEvalRuns[runID] = run
			delete(s.state.EvalRuns, runID)
		}
	}
	remaining := len(s.state.KnowledgeBases)
	s.state.Mu.Unlock()

	if err := s.saveState(); err != nil {
		s.state.Mu.Lock()
		s.state.KnowledgeBases[id] = removedKnowledgeBase
		for datasetID, dataset := range removedEvalDatasets {
			s.state.EvalDatasets[datasetID] = dataset
		}
		for runID, run := range removedEvalRuns {
			s.state.EvalRuns[runID] = run
		}
		s.state.Mu.Unlock()
		return remaining, err
	}

	collectionErr := s.deleteKnowledgeBaseCollection(id)
	for _, document := range removedKnowledgeBase.Documents {
		if err := s.deleteIndexedDocument(id, document.ID); err != nil {
			log.Printf("failed to delete indexed content for document %s: %v", document.ID, err)
		}
	}
	if collectionErr != nil {
		return remaining, collectionErr
	}

	return remaining, nil
}

func (s *AppService) GetKnowledgeBaseDocuments(id string) ([]model.Document, error) {
	s.state.Mu.RLock()
	defer s.state.Mu.RUnlock()

	kb, ok := s.state.KnowledgeBases[id]
	if !ok {
		return nil, fmt.Errorf("knowledge base not found")
	}

	documents := make([]model.Document, len(kb.Documents))
	for index, document := range kb.Documents {
		documents[index] = publicDocument(document)
	}
	return documents, nil
}

// GetDocumentIndexStatus returns persisted index fields without reading or
// reparsing the source file. Status polling should not perform document work.
func (s *AppService) GetDocumentIndexStatus(knowledgeBaseID, documentID string) (model.Document, error) {
	document, err := s.findDocument(knowledgeBaseID, documentID)
	if err != nil {
		return model.Document{}, err
	}
	return publicDocument(document), nil
}

type DocumentDetailOptions struct {
	IncludeFullContent bool
	IncludeAllChunks   bool
}

func (s *AppService) GetDocumentDetail(knowledgeBaseID, documentID, focusChunkID string) (model.DocumentDetailResponse, error) {
	return s.GetDocumentDetailWithOptions(knowledgeBaseID, documentID, focusChunkID, DocumentDetailOptions{})
}

func (s *AppService) GetDocumentDetailWithOptions(knowledgeBaseID, documentID, focusChunkID string, options DocumentDetailOptions) (model.DocumentDetailResponse, error) {
	document, err := s.findDocument(knowledgeBaseID, documentID)
	if err != nil {
		return model.DocumentDetailResponse{}, err
	}

	content, contentSource, err := s.resolveDocumentContent(document)
	if err != nil {
		return model.DocumentDetailResponse{}, err
	}

	chunks := s.rag.BuildDocumentChunks(document, content)
	return buildDocumentDetailResponse(s, document, content, contentSource, chunks, focusChunkID, options), nil
}

func (s *AppService) GetKnowledgeBaseHealth(knowledgeBaseID string) (model.KnowledgeBaseHealthResponse, error) {
	knowledgeBaseID = strings.TrimSpace(knowledgeBaseID)
	if knowledgeBaseID == "" {
		return model.KnowledgeBaseHealthResponse{}, fmt.Errorf("knowledge base id is required")
	}
	if s == nil || s.state == nil {
		return model.KnowledgeBaseHealthResponse{}, fmt.Errorf("app service is nil")
	}

	s.state.Mu.RLock()
	kb, ok := s.state.KnowledgeBases[knowledgeBaseID]
	s.state.Mu.RUnlock()
	if !ok {
		return model.KnowledgeBaseHealthResponse{}, fmt.Errorf("knowledge base not found")
	}

	metrics := model.KnowledgeBaseHealthMetrics{
		DocumentCount: len(kb.Documents),
		QdrantEnabled: s.qdrant != nil && s.qdrant.IsEnabled(),
	}
	documents := make([]model.KnowledgeBaseDocumentHealth, 0, len(kb.Documents))
	needsReindexCount := 0
	for _, document := range kb.Documents {
		item := s.buildKnowledgeBaseDocumentHealth(document)
		documents = append(documents, item)

		switch document.Status {
		case "indexed":
			metrics.IndexedCount++
		case "processing":
			metrics.ProcessingCount++
		}
		if documentIndexErrorCode(document) != "" || document.Status == "failed" {
			metrics.FailedCount++
		}
		if !item.RawContentAvailable {
			metrics.EmptyContentCount++
		}
		if item.NeedsReindex {
			needsReindexCount++
		}
		metrics.ChunkCount += item.ChunkCount
		metrics.VectorCount += item.VectorCount
		metrics.SummaryChunkCount += item.SummaryChunkCount
		metrics.StructuredRowCount += item.StructuredRowCount
		metrics.RawContentChars += item.RawContentChars
		if isLaterRFC3339(item.IndexedAt, metrics.LastIndexedAt) {
			metrics.LastIndexedAt = item.IndexedAt
		}
	}

	score := knowledgeBaseHealthScore(metrics, needsReindexCount)
	status := knowledgeBaseHealthStatus(score, metrics, needsReindexCount)
	return model.KnowledgeBaseHealthResponse{
		KnowledgeBaseID:     kb.ID,
		Name:                kb.Name,
		Status:              status,
		Score:               score,
		CurrentIndexVersion: currentKnowledgeBaseIndexVersion(kb),
		Metrics:             metrics,
		Recommendations:     knowledgeBaseHealthRecommendations(metrics, needsReindexCount),
		Documents:           documents,
		IndexHistory:        publicIndexRunRecords(kb.IndexHistory),
	}, nil
}

func (s *AppService) buildKnowledgeBaseDocumentHealth(document model.Document) model.KnowledgeBaseDocumentHealth {
	errorCode := documentIndexErrorCode(document)
	item := model.KnowledgeBaseDocumentHealth{
		DocumentID:     document.ID,
		DocumentName:   document.Name,
		Status:         document.Status,
		IndexedAt:      document.IndexedAt,
		IndexError:     publicIndexError(errorCode),
		IndexErrorCode: errorCode,
		IndexVersion:   document.IndexVersion,
		ChunkCount:     document.ChunkCount,
	}

	content, _, err := s.resolveDocumentContent(document)
	if err == nil {
		item.RawContentChars = len([]rune(content))
		item.RawContentAvailable = strings.TrimSpace(content) != ""
		if s != nil && s.rag != nil {
			chunks := s.rag.BuildDocumentChunks(document, content)
			item.ChunkCount = len(chunks)
			for _, chunk := range chunks {
				if chunk.Kind == "structured_summary" {
					item.SummaryChunkCount++
				}
				if chunk.Kind == "structured_row" {
					item.StructuredRowCount++
				}
			}
		}
	} else {
		item.Recommendation = "无法读取原始文件，建议检查文件是否仍存在后重新上传。"
	}

	if s != nil && s.qdrant != nil && s.qdrant.IsEnabled() && document.Status == "indexed" {
		item.VectorCount = item.ChunkCount
	}
	item.NeedsReindex = documentNeedsReindex(document, item)
	if item.Recommendation == "" {
		item.Recommendation = documentHealthRecommendation(document, item)
	}
	return item
}

func (s *AppService) findDocument(knowledgeBaseID, documentID string) (model.Document, error) {
	knowledgeBaseID = strings.TrimSpace(knowledgeBaseID)
	documentID = strings.TrimSpace(documentID)
	if knowledgeBaseID == "" {
		return model.Document{}, fmt.Errorf("knowledge base id is required")
	}
	if documentID == "" {
		return model.Document{}, fmt.Errorf("document id is required")
	}

	s.state.Mu.RLock()
	defer s.state.Mu.RUnlock()
	kb, ok := s.state.KnowledgeBases[knowledgeBaseID]
	if !ok {
		return model.Document{}, fmt.Errorf("knowledge base not found")
	}
	for _, document := range kb.Documents {
		if document.ID == documentID {
			return document, nil
		}
	}
	return model.Document{}, fmt.Errorf("document not found")
}

func (s *AppService) ResolveKnowledgeBaseID(candidate string) (string, error) {
	s.state.Mu.RLock()
	defer s.state.Mu.RUnlock()

	candidate = strings.TrimSpace(candidate)
	if candidate != "" {
		if _, ok := s.state.KnowledgeBases[candidate]; !ok {
			return "", fmt.Errorf("knowledge base not found")
		}
		return candidate, nil
	}

	for id := range s.state.KnowledgeBases {
		return id, nil
	}

	return "", fmt.Errorf("knowledge base not found")
}

func (s *AppService) AddDocument(knowledgeBaseID string, document model.Document) (model.Document, error) {
	document = enrichDocumentGovernance(document)
	s.state.Mu.Lock()
	kb, ok := s.state.KnowledgeBases[knowledgeBaseID]
	if !ok {
		s.state.Mu.Unlock()
		return model.Document{}, fmt.Errorf("knowledge base not found")
	}
	if existing, duplicate := findDuplicateDocument(kb.Documents, document); duplicate {
		s.state.Mu.Unlock()
		return model.Document{}, &DuplicateDocumentError{Existing: existing}
	}
	kb.Documents = append([]model.Document{document}, kb.Documents...)
	s.state.KnowledgeBases[knowledgeBaseID] = kb
	s.state.Mu.Unlock()
	if err := s.saveState(); err != nil {
		s.state.Mu.Lock()
		currentKB := s.state.KnowledgeBases[knowledgeBaseID]
		filtered := make([]model.Document, 0, len(currentKB.Documents))
		for _, item := range currentKB.Documents {
			if item.ID != document.ID {
				filtered = append(filtered, item)
			}
		}
		currentKB.Documents = filtered
		s.state.KnowledgeBases[knowledgeBaseID] = currentKB
		s.state.Mu.Unlock()
		return model.Document{}, err
	}
	return document, nil
}

func (s *AppService) updateDocument(knowledgeBaseID string, nextDocument model.Document) error {
	s.state.Mu.Lock()
	kb, ok := s.state.KnowledgeBases[knowledgeBaseID]
	if !ok {
		s.state.Mu.Unlock()
		return fmt.Errorf("knowledge base not found")
	}
	updated := false
	for index, document := range kb.Documents {
		if document.ID == nextDocument.ID {
			kb.Documents[index] = nextDocument
			updated = true
			break
		}
	}
	if !updated {
		s.state.Mu.Unlock()
		return fmt.Errorf("document not found")
	}
	s.state.KnowledgeBases[knowledgeBaseID] = kb
	s.state.Mu.Unlock()
	return s.saveState()
}

func (s *AppService) DeleteDocument(knowledgeBaseID, documentID string) (model.Document, error) {
	removedDocument, err := s.findDocument(knowledgeBaseID, documentID)
	if err != nil {
		return model.Document{}, err
	}
	removedDocument = enrichDocumentGovernance(removedDocument)
	reservation, err := s.reserveDocumentIndex(context.Background(), removedDocument)
	if err != nil {
		return model.Document{}, err
	}
	defer reservation()

	// Remove external index state first. If Qdrant is unavailable, keep the
	// document visible and retryable instead of leaving an orphaned searchable
	// document after its metadata has been deleted.
	if err := s.deleteDocumentChunks(knowledgeBaseID, documentID); err != nil {
		return model.Document{}, &IndexCleanupError{Err: err}
	}

	s.state.Mu.Lock()
	kb, ok := s.state.KnowledgeBases[knowledgeBaseID]
	if !ok {
		s.state.Mu.Unlock()
		return model.Document{}, fmt.Errorf("knowledge base not found")
	}

	filtered := make([]model.Document, 0, len(kb.Documents))
	removed := false
	for _, document := range kb.Documents {
		if document.ID == documentID {
			removed = true
			continue
		}
		filtered = append(filtered, document)
	}

	if !removed {
		s.state.Mu.Unlock()
		return model.Document{}, fmt.Errorf("document not found")
	}

	originalDocuments := kb.Documents
	kb.Documents = filtered
	s.state.KnowledgeBases[knowledgeBaseID] = kb
	s.state.Mu.Unlock()

	if err := s.saveState(); err != nil {
		s.state.Mu.Lock()
		kb.Documents = originalDocuments
		s.state.KnowledgeBases[knowledgeBaseID] = kb
		s.state.Mu.Unlock()
		return model.Document{}, err
	}
	if err := s.deleteIndexedDocument(knowledgeBaseID, documentID); err != nil {
		log.Printf("failed to delete indexed content for document %s: %v", documentID, err)
	}
	return removedDocument, nil
}

func (s *AppService) BuildRetrievalContext(req model.ChatCompletionRequest) (string, []map[string]string, error) {
	return s.BuildRetrievalContextWithContext(context.Background(), req)
}

func (s *AppService) BuildRetrievalContextWithContext(ctx context.Context, req model.ChatCompletionRequest) (string, []map[string]string, error) {
	return s.retrievalBoundary().BuildContext(ctx, req)
}

func (s *AppService) EvaluateRetrieve(req model.ChatCompletionRequest) ([]RetrievedChunk, error) {
	return s.EvaluateRetrieveWithContext(context.Background(), req)
}

func (s *AppService) EvaluateRetrieveWithContext(ctx context.Context, req model.ChatCompletionRequest) ([]RetrievedChunk, error) {
	return s.retrievalBoundary().Evaluate(ctx, req)
}

func (s *AppService) DebugRetrieve(req model.RetrievalDebugRequest) (model.RetrievalDebugResponse, error) {
	return s.DebugRetrieveWithContext(context.Background(), req)
}

func (s *AppService) DebugRetrieveWithContext(ctx context.Context, req model.RetrievalDebugRequest) (model.RetrievalDebugResponse, error) {
	return s.retrievalBoundary().Debug(ctx, req)
}

func (s *AppService) CurrentEmbeddingConfig() model.EmbeddingModelConfig {
	if s == nil {
		return model.EmbeddingModelConfig{}
	}
	return s.currentEmbeddingConfig()
}

func (s *AppService) CurrentChatConfig() model.ChatModelConfig {
	if s == nil {
		return model.ChatModelConfig{}
	}
	return s.currentChatConfig()
}

func (s *AppService) KnowledgeTemperature() float64 {
	if s == nil {
		return defaultKnowledgeTemperature
	}
	s.state.Mu.RLock()
	defer s.state.Mu.RUnlock()
	return normalizeKnowledgeTemperature(s.state.Config.Chat.KnowledgeTemperature)
}

func (s *AppService) ServerConfig() model.ServerConfig {
	if s == nil {
		return model.ServerConfig{}
	}
	return s.serverConfig
}

func (s *AppService) BuildChatContext(req model.ChatCompletionRequest, relevantDocumentIDs []string) (string, []map[string]string, error) {
	s.state.Mu.RLock()
	defer s.state.Mu.RUnlock()

	if req.DocumentID != "" {
		if req.KnowledgeBaseID != "" {
			kb, ok := s.state.KnowledgeBases[req.KnowledgeBaseID]
			if !ok {
				return "", nil, fmt.Errorf("knowledge base not found")
			}
			for _, document := range kb.Documents {
				if document.ID == req.DocumentID {
					return fmt.Sprintf("当前问答范围为文档《%s》，所属知识库为“%s”。文档摘要：%s", document.Name, kb.Name, document.ContentPreview), []map[string]string{{
						"knowledgeBaseId": kb.ID,
						"documentId":      document.ID,
						"documentName":    document.Name,
					}}, nil
				}
			}
			return "", nil, fmt.Errorf("document does not belong to knowledge base")
		}

		var matchedKnowledgeBase *model.KnowledgeBase
		var matchedDocument *model.Document
		for _, kb := range s.state.KnowledgeBases {
			for _, document := range kb.Documents {
				if document.ID == req.DocumentID {
					if matchedDocument != nil {
						return "", nil, fmt.Errorf("document id is ambiguous; knowledgeBaseId is required")
					}
					kbCopy := kb
					documentCopy := document
					matchedKnowledgeBase = &kbCopy
					matchedDocument = &documentCopy
				}
			}
		}
		if matchedDocument == nil || matchedKnowledgeBase == nil {
			return "", nil, fmt.Errorf("document not found")
		}
		return fmt.Sprintf("当前问答范围为文档《%s》，所属知识库为“%s”。文档摘要：%s", matchedDocument.Name, matchedKnowledgeBase.Name, matchedDocument.ContentPreview), []map[string]string{{
			"knowledgeBaseId": matchedKnowledgeBase.ID,
			"documentId":      matchedDocument.ID,
			"documentName":    matchedDocument.Name,
		}}, nil
	}

	if req.KnowledgeBaseID != "" {
		kb, ok := s.state.KnowledgeBases[req.KnowledgeBaseID]
		if !ok {
			return "", nil, fmt.Errorf("knowledge base not found")
		}

		relevantIDs := make(map[string]struct{}, len(relevantDocumentIDs))
		for _, documentID := range relevantDocumentIDs {
			if documentID = strings.TrimSpace(documentID); documentID != "" {
				relevantIDs[documentID] = struct{}{}
			}
		}

		summaryLines := []string{fmt.Sprintf("当前问答范围为知识库“%s”，其中包含 %d 份文档。", kb.Name, len(kb.Documents))}
		previewDocuments := make([]model.Document, 0, minInt(len(relevantIDs), 3))
		for _, document := range kb.Documents {
			if _, ok := relevantIDs[document.ID]; !ok {
				continue
			}
			previewDocuments = append(previewDocuments, document)
			if len(previewDocuments) >= 3 {
				break
			}
		}
		if len(previewDocuments) > 0 {
			summaryLines = append(summaryLines, "文档概览：")
			for _, document := range previewDocuments {
				preview := truncateRunes(strings.TrimSpace(document.ContentPreview), 120)
				if preview == "" {
					preview = "暂无内容预览"
				}
				summaryLines = append(summaryLines, fmt.Sprintf("- %s：%s", document.Name, preview))
			}
		}

		return strings.Join(summaryLines, "\n"), nil, nil
	}

	if len(s.state.KnowledgeBases) == 0 {
		return "当前系统中尚未创建知识库。", nil, nil
	}

	kbNames := make([]string, 0, len(s.state.KnowledgeBases))
	for _, kb := range s.state.KnowledgeBases {
		kbNames = append(kbNames, kb.Name)
	}
	sort.Strings(kbNames)

	return "当前未限定知识库范围，系统将默认使用全部知识库作为后续检索候选。当前知识库包括：" + strings.Join(kbNames, "、"), nil, nil
}

func (s *AppService) ensureKnowledgeBaseCollection(knowledgeBaseID string) error {
	return s.ensureKnowledgeBaseCollectionWithContext(context.Background(), knowledgeBaseID)
}

func (s *AppService) ensureKnowledgeBaseCollectionWithContext(ctx context.Context, knowledgeBaseID string) error {
	if s.qdrant == nil || !s.qdrant.IsEnabled() {
		return nil
	}

	ctx, cancel := context.WithTimeout(normalizeServiceContext(ctx), 5*time.Second)
	defer cancel()

	if err := s.qdrant.EnsureCollection(ctx, knowledgeBaseID); err != nil {
		return fmt.Errorf("ensure qdrant collection for knowledge base %s: %w", knowledgeBaseID, err)
	}

	return nil
}

func (s *AppService) deleteKnowledgeBaseCollection(knowledgeBaseID string) error {
	if s.qdrant == nil || !s.qdrant.IsEnabled() {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.qdrant.DeleteCollection(ctx, knowledgeBaseID); err != nil {
		return fmt.Errorf("delete qdrant collection for knowledge base %s: %w", knowledgeBaseID, err)
	}

	return nil
}

func (s *AppService) currentEmbeddingConfig() model.EmbeddingModelConfig {
	s.state.Mu.RLock()
	defer s.state.Mu.RUnlock()

	return model.EmbeddingModelConfig{
		Provider: strings.TrimSpace(s.state.Config.Embedding.Provider),
		BaseURL:  strings.TrimSpace(s.state.Config.Embedding.BaseURL),
		Model:    strings.TrimSpace(s.state.Config.Embedding.Model),
		APIKey:   strings.TrimSpace(s.state.Config.Embedding.APIKey),
	}
}

func (s *AppService) currentChatConfig() model.ChatModelConfig {
	s.state.Mu.RLock()
	defer s.state.Mu.RUnlock()

	contextMessageLimit := s.state.Config.Chat.ContextMessageLimit
	if contextMessageLimit <= 0 {
		contextMessageLimit = 12
	}

	return model.ChatModelConfig{
		Provider:            strings.TrimSpace(s.state.Config.Chat.Provider),
		BaseURL:             strings.TrimSpace(s.state.Config.Chat.BaseURL),
		Model:               strings.TrimSpace(s.state.Config.Chat.Model),
		APIKey:              strings.TrimSpace(s.state.Config.Chat.APIKey),
		Temperature:         s.state.Config.Chat.Temperature,
		ContextMessageLimit: contextMessageLimit,
	}
}

func normalizeKnowledgeTemperature(value float64) float64 {
	if value < defaultKnowledgeTemperature {
		return defaultKnowledgeTemperature
	}
	if value > maxKnowledgeTemperature {
		return maxKnowledgeTemperature
	}
	return value
}

func (s *AppService) resolveEmbeddingConfig(req model.ChatCompletionRequest) model.EmbeddingModelConfig {
	cfg := req.Embedding
	cfg.Provider = strings.TrimSpace(cfg.Provider)
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	if cfg.Provider != "" && cfg.BaseURL != "" && cfg.Model != "" {
		if cfg.APIKey == "" {
			stored := s.currentEmbeddingConfig()
			if sameModelEndpoint(cfg.Provider, cfg.BaseURL, stored.Provider, stored.BaseURL) {
				cfg.APIKey = stored.APIKey
			}
		}
		return cfg
	}
	return s.currentEmbeddingConfig()
}

func (s *AppService) resolveChatConfig(req model.ChatCompletionRequest) model.ChatModelConfig {
	cfg := req.Config
	cfg.Provider = strings.TrimSpace(cfg.Provider)
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	if cfg.Provider != "" && cfg.BaseURL != "" && cfg.Model != "" {
		if cfg.APIKey == "" {
			stored := s.currentChatConfig()
			if sameModelEndpoint(cfg.Provider, cfg.BaseURL, stored.Provider, stored.BaseURL) {
				cfg.APIKey = stored.APIKey
			}
		}
		if cfg.ContextMessageLimit <= 0 {
			cfg.ContextMessageLimit = s.currentChatConfig().ContextMessageLimit
		}
		return cfg
	}
	return s.currentChatConfig()
}

func sameModelEndpoint(provider, baseURL, storedProvider, storedBaseURL string) bool {
	return strings.EqualFold(strings.TrimSpace(provider), strings.TrimSpace(storedProvider)) &&
		strings.TrimRight(strings.TrimSpace(baseURL), "/") == strings.TrimRight(strings.TrimSpace(storedBaseURL), "/")
}

func (s *AppService) ContextMessageLimit() int {
	if s == nil {
		return 12
	}
	limit := s.currentChatConfig().ContextMessageLimit
	if limit <= 0 {
		return 12
	}
	return limit
}

func (s *AppService) SaveConversation(req model.SaveConversationRequest) (*model.Conversation, error) {
	if s == nil {
		return nil, fmt.Errorf("app service is nil")
	}
	if s.chatHistory == nil {
		return nil, fmt.Errorf("chat history store is not configured")
	}
	conversationID := strings.TrimSpace(req.ID)
	if conversationID == "" {
		return nil, fmt.Errorf("conversation id is required")
	}
	if err := s.validateKnowledgeScope(req.KnowledgeBaseID, req.DocumentID); err != nil {
		return nil, err
	}
	existing, err := s.chatHistory.GetConversation(conversationID)
	if err != nil {
		return nil, err
	}
	if existing != nil && !conversationScopesEqual(existing.KnowledgeBaseID, existing.DocumentID, req.KnowledgeBaseID, req.DocumentID) {
		return nil, fmt.Errorf(
			"%w: existing knowledgeBaseId=%q documentId=%q, requested knowledgeBaseId=%q documentId=%q",
			ErrConversationScopeMismatch,
			existing.KnowledgeBaseID,
			existing.DocumentID,
			strings.TrimSpace(req.KnowledgeBaseID),
			strings.TrimSpace(req.DocumentID),
		)
	}
	if existing != nil && existing.ScopeVersion < conversationScopeVersion {
		return nil, fmt.Errorf("%w: create a new conversation before continuing", ErrConversationScopeUpgradeNeeded)
	}
	messages := cloneStoredMessages(req.Messages)
	if len(messages) == 0 {
		return nil, fmt.Errorf("conversation messages cannot be empty")
	}
	createdAt := normalizeTimestamp(messages[0].CreatedAt)
	updatedAt := normalizeTimestamp(messages[len(messages)-1].CreatedAt)
	conversation := model.Conversation{
		ID:              conversationID,
		Title:           strings.TrimSpace(req.Title),
		KnowledgeBaseID: strings.TrimSpace(req.KnowledgeBaseID),
		DocumentID:      strings.TrimSpace(req.DocumentID),
		ScopeVersion:    conversationScopeVersion,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
		Messages:        messages,
	}
	if conversation.Title == "" {
		conversation.Title = buildConversationTitle(messages)
	}
	if err := s.chatHistory.SaveConversation(conversation); err != nil {
		return nil, err
	}
	return &conversation, nil
}

func (s *AppService) ValidateChatRequestScope(req model.ChatCompletionRequest) error {
	if s == nil {
		return fmt.Errorf("app service is nil")
	}
	if err := s.validateKnowledgeScope(req.KnowledgeBaseID, req.DocumentID); err != nil {
		return err
	}
	conversationID := strings.TrimSpace(req.ConversationID)
	if conversationID == "" || s.chatHistory == nil {
		return nil
	}
	existing, err := s.chatHistory.GetConversation(conversationID)
	if err != nil {
		return err
	}
	if existing == nil {
		return nil
	}
	if existing.ScopeVersion < conversationScopeVersion {
		return fmt.Errorf("%w: create a new conversation before continuing", ErrConversationScopeUpgradeNeeded)
	}
	if !conversationScopesEqual(existing.KnowledgeBaseID, existing.DocumentID, req.KnowledgeBaseID, req.DocumentID) {
		return fmt.Errorf(
			"%w: create a new conversation before changing knowledgeBaseId or documentId",
			ErrConversationScopeMismatch,
		)
	}
	return nil
}

func (s *AppService) validateKnowledgeScope(knowledgeBaseID, documentID string) error {
	if s == nil || s.state == nil {
		return fmt.Errorf("app service is nil")
	}
	knowledgeBaseID = strings.TrimSpace(knowledgeBaseID)
	documentID = strings.TrimSpace(documentID)
	s.state.Mu.RLock()
	defer s.state.Mu.RUnlock()

	if knowledgeBaseID != "" {
		kb, ok := s.state.KnowledgeBases[knowledgeBaseID]
		if !ok {
			return fmt.Errorf("knowledge base not found")
		}
		if documentID == "" {
			return nil
		}
		for _, document := range kb.Documents {
			if document.ID == documentID {
				return nil
			}
		}
		return fmt.Errorf("document does not belong to knowledge base")
	}

	if documentID == "" {
		return nil
	}
	matches := 0
	for _, kb := range s.state.KnowledgeBases {
		for _, document := range kb.Documents {
			if document.ID == documentID {
				matches++
			}
		}
	}
	if matches == 0 {
		return fmt.Errorf("document not found")
	}
	if matches > 1 {
		return fmt.Errorf("document id is ambiguous; knowledgeBaseId is required")
	}
	return nil
}

func conversationScopesEqual(leftKnowledgeBaseID, leftDocumentID, rightKnowledgeBaseID, rightDocumentID string) bool {
	return strings.TrimSpace(leftKnowledgeBaseID) == strings.TrimSpace(rightKnowledgeBaseID) &&
		strings.TrimSpace(leftDocumentID) == strings.TrimSpace(rightDocumentID)
}

func (s *AppService) ListConversations() ([]model.ConversationListItem, error) {
	if s == nil {
		return nil, fmt.Errorf("app service is nil")
	}
	if s.chatHistory == nil {
		return []model.ConversationListItem{}, nil
	}
	items, err := s.chatHistory.ListConversations()
	if err != nil {
		return nil, err
	}
	sortConversationItems(items)
	return items, nil
}

func (s *AppService) GetConversation(id string) (*model.Conversation, error) {
	if s == nil {
		return nil, fmt.Errorf("app service is nil")
	}
	if s.chatHistory == nil {
		return nil, nil
	}
	return s.chatHistory.GetConversation(id)
}

func (s *AppService) DeleteConversation(id string) error {
	if s == nil {
		return fmt.Errorf("app service is nil")
	}
	if s.chatHistory == nil {
		return nil
	}
	return s.chatHistory.DeleteConversation(id)
}

func (s *AppService) EditMessage(conversationID, messageID string, req model.EditMessageRequest) (*model.Conversation, error) {
	if s == nil {
		return nil, fmt.Errorf("app service is nil")
	}
	if s.chatHistory == nil {
		return nil, fmt.Errorf("chat history store is not configured")
	}

	conversationID = strings.TrimSpace(conversationID)
	messageID = strings.TrimSpace(messageID)
	newContent := strings.TrimSpace(req.Content)

	if conversationID == "" {
		return nil, fmt.Errorf("conversation id is required")
	}
	if messageID == "" {
		return nil, fmt.Errorf("message id is required")
	}
	if newContent == "" {
		return nil, fmt.Errorf("message content cannot be empty")
	}

	conversation, err := s.chatHistory.GetConversation(conversationID)
	if err != nil {
		return nil, err
	}
	if conversation == nil {
		return nil, fmt.Errorf("conversation not found")
	}

	messageIndex := -1
	for i, msg := range conversation.Messages {
		if msg.ID == messageID {
			messageIndex = i
			break
		}
	}
	if messageIndex == -1 {
		return nil, fmt.Errorf("message not found")
	}

	conversation.Messages[messageIndex].Content = newContent
	conversation.Messages[messageIndex].CreatedAt = time.Now().UTC().Format(time.RFC3339)
	conversation.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := s.chatHistory.SaveConversation(*conversation); err != nil {
		return nil, err
	}

	return conversation, nil
}

func (s *AppService) DeleteMessage(conversationID, messageID string) (*model.Conversation, error) {
	if s == nil {
		return nil, fmt.Errorf("app service is nil")
	}
	if s.chatHistory == nil {
		return nil, fmt.Errorf("chat history store is not configured")
	}

	conversationID = strings.TrimSpace(conversationID)
	messageID = strings.TrimSpace(messageID)

	if conversationID == "" {
		return nil, fmt.Errorf("conversation id is required")
	}
	if messageID == "" {
		return nil, fmt.Errorf("message id is required")
	}

	conversation, err := s.chatHistory.GetConversation(conversationID)
	if err != nil {
		return nil, err
	}
	if conversation == nil {
		return nil, fmt.Errorf("conversation not found")
	}
	if len(conversation.Messages) <= 1 {
		return nil, fmt.Errorf("cannot delete the last message")
	}

	messageIndex := -1
	for i, msg := range conversation.Messages {
		if msg.ID == messageID {
			messageIndex = i
			break
		}
	}
	if messageIndex == -1 {
		return nil, fmt.Errorf("message not found")
	}

	conversation.Messages = append(
		conversation.Messages[:messageIndex],
		conversation.Messages[messageIndex+1:]...,
	)
	conversation.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := s.chatHistory.SaveConversation(*conversation); err != nil {
		return nil, err
	}

	return conversation, nil
}

func (s *AppService) ExportConversation(conversationID string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("app service is nil")
	}
	if s.chatHistory == nil {
		return "", fmt.Errorf("chat history store is not configured")
	}

	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return "", fmt.Errorf("conversation id is required")
	}

	conversation, err := s.chatHistory.GetConversation(conversationID)
	if err != nil {
		return "", err
	}
	if conversation == nil {
		return "", fmt.Errorf("conversation not found")
	}

	var builder strings.Builder
	builder.WriteString("# ")
	builder.WriteString(conversation.Title)
	builder.WriteString("\n\n")

	if conversation.KnowledgeBaseID != "" || conversation.DocumentID != "" {
		builder.WriteString("**对话信息**\n\n")
		if conversation.KnowledgeBaseID != "" {
			builder.WriteString("- 知识库ID: ")
			builder.WriteString(conversation.KnowledgeBaseID)
			builder.WriteString("\n")
		}
		if conversation.DocumentID != "" {
			builder.WriteString("- 文档ID: ")
			builder.WriteString(conversation.DocumentID)
			builder.WriteString("\n")
		}
		builder.WriteString("- 创建时间: ")
		builder.WriteString(conversation.CreatedAt)
		builder.WriteString("\n")
		builder.WriteString("- 更新时间: ")
		builder.WriteString(conversation.UpdatedAt)
		builder.WriteString("\n\n")
	}

	builder.WriteString("---\n\n")

	for _, msg := range conversation.Messages {
		role := strings.TrimSpace(msg.Role)
		if role == "system" {
			continue
		}

		if role == "user" {
			builder.WriteString("## 用户\n\n")
		} else if role == "assistant" {
			builder.WriteString("## 助手\n\n")
		} else {
			builder.WriteString("## ")
			builder.WriteString(strings.Title(role))
			builder.WriteString("\n\n")
		}

		builder.WriteString(msg.Content)
		builder.WriteString("\n\n")

		if len(msg.Metadata) > 0 {
			if sources, ok := msg.Metadata["sources"].([]interface{}); ok && len(sources) > 0 {
				builder.WriteString("**来源**\n\n")
				for _, source := range sources {
					if sourceMap, ok := source.(map[string]interface{}); ok {
						if docName, ok := sourceMap["documentName"].(string); ok && docName != "" {
							builder.WriteString("- ")
							builder.WriteString(docName)
							builder.WriteString("\n")
						}
					}
				}
				builder.WriteString("\n")
			}
		}

		builder.WriteString("---\n\n")
	}

	return builder.String(), nil
}

func (s *AppService) SetReranker(reranker SemanticReranker) {
	s.reranker = reranker
}

func (s *AppService) SetQueryRewriter(rewriter QueryRewriter) {
	s.queryRewriter = rewriter
	if setter, ok := rewriter.(interface {
		SetChatConfigProvider(func() model.ChatModelConfig)
	}); ok {
		setter.SetChatConfigProvider(s.currentChatConfig)
	}
}

func (s *AppService) SetSemanticCache(cache *SemanticCache) {
	s.semanticCache = cache
}

func (s *AppService) SetContextCompressor(compressor ContextCompressor) {
	s.contextCompressor = compressor
	if setter, ok := compressor.(interface {
		SetChatConfigProvider(func() model.ChatModelConfig)
	}); ok {
		setter.SetChatConfigProvider(s.currentChatConfig)
	}
}

func (s *AppService) qdrantVectorSize() int {
	if s.qdrant != nil && s.qdrant.vectorSize > 0 {
		return s.qdrant.vectorSize
	}
	return 768
}

func (s *AppService) upsertDocumentChunks(knowledgeBaseID string, chunks []DocumentChunk, vectors [][]float64) error {
	return s.upsertDocumentChunksWithContext(context.Background(), knowledgeBaseID, chunks, vectors)
}

func (s *AppService) upsertDocumentChunksWithContext(ctx context.Context, knowledgeBaseID string, chunks []DocumentChunk, vectors [][]float64) error {
	if s.qdrant == nil || !s.qdrant.IsEnabled() || len(chunks) == 0 {
		return nil
	}
	points := make([]QdrantPoint, 0, len(chunks))
	for index, chunk := range chunks {
		vector := make([]float64, s.qdrantVectorSize())
		if index < len(vectors) {
			copy(vector, vectors[index])
		}
		points = append(points, QdrantPoint{
			ID:     qdrantPointID(chunk.ID),
			Vector: qdrantPointVectors(vector, BuildSparseVector(chunk.Text)),
			Payload: map[string]any{
				"knowledge_base_id": chunk.KnowledgeBaseID,
				"document_id":       chunk.DocumentID,
				"document_name":     chunk.DocumentName,
				"chunk_id":          chunk.ID,
				"evidence_id":       evidenceIDForChunk(chunk),
				"chunk_index":       chunk.Index,
				"chunk_kind":        chunk.Kind,
				"text":              chunk.Text,
				"char_start":        chunk.CharStart,
				"char_end":          chunk.CharEnd,
				"line_start":        chunk.LineStart,
				"line_end":          chunk.LineEnd,
				"table_row":         chunk.TableRow,
				"table_columns":     chunk.TableColumns,
			},
		})
	}

	ctx, cancel := context.WithTimeout(normalizeServiceContext(ctx), 10*time.Minute)
	defer cancel()
	if err := s.qdrant.UpsertPoints(ctx, knowledgeBaseID, points); err != nil {
		return fmt.Errorf("upsert qdrant points: %w", err)
	}
	return nil
}

func (s *AppService) replaceDocumentChunks(knowledgeBaseID, documentID string, chunks []DocumentChunk, vectors [][]float64) error {
	return s.replaceDocumentChunksWithContext(context.Background(), knowledgeBaseID, documentID, chunks, vectors)
}

func (s *AppService) replaceDocumentChunksWithContext(ctx context.Context, knowledgeBaseID, documentID string, chunks []DocumentChunk, vectors [][]float64) error {
	if s == nil || s.qdrant == nil || !s.qdrant.IsEnabled() {
		return nil
	}

	if err := s.ensureKnowledgeBaseCollectionWithContext(ctx, knowledgeBaseID); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(normalizeServiceContext(ctx), 10*time.Minute)
	defer cancel()
	previousPoints, err := s.qdrant.ScrollPointPayloadsByFilter(ctx, knowledgeBaseID, documentFilter(documentID))
	if err != nil {
		return fmt.Errorf("inspect existing qdrant points for document %s: %w", documentID, err)
	}

	if len(chunks) > 0 {
		if err := s.upsertDocumentChunksWithContext(ctx, knowledgeBaseID, chunks, vectors); err != nil {
			return err
		}
	}

	currentIDs := make(map[string]struct{}, len(chunks))
	for _, chunk := range chunks {
		currentIDs[fmt.Sprint(qdrantPointID(chunk.ID))] = struct{}{}
	}
	staleIDs := make([]any, 0)
	for _, point := range previousPoints {
		pointID := fmt.Sprint(point.ID)
		if _, exists := currentIDs[pointID]; !exists {
			staleIDs = append(staleIDs, point.ID)
		}
	}
	if err := s.qdrant.DeletePointsByIDs(ctx, knowledgeBaseID, staleIDs); err != nil {
		return fmt.Errorf("delete stale qdrant points for document %s: %w", documentID, err)
	}
	return nil
}

func (s *AppService) retrieveRelevantChunks(req model.ChatCompletionRequest, queryVector []float64) ([]RetrievedChunk, error) {
	return s.retrieveRelevantChunksWithContext(context.Background(), req, queryVector)
}

func (s *AppService) retrieveRelevantChunksWithContext(ctx context.Context, req model.ChatCompletionRequest, queryVector []float64) ([]RetrievedChunk, error) {
	if s.qdrant == nil || !s.qdrant.IsEnabled() {
		return nil, nil
	}

	knowledgeBaseIDs, err := s.resolveRetrievalKnowledgeBaseIDs(req)
	if err != nil {
		return nil, err
	}
	cacheScope := s.retrievalCacheScope(req, knowledgeBaseIDs)

	query := latestUserMessage(req.Messages)
	params := s.resolveRetrievalParams(req)
	autoExpand := s.retrievalAutoExpandEnabled()
	ctx = normalizeServiceContext(ctx)

	var queryEmbedding []float32
	if s.semanticCache != nil {
		if len(queryVector) == 0 {
			embedCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
			defer cancel()
			vectors, err := s.rag.EmbedTexts(embedCtx, s.resolveEmbeddingConfig(req), []string{query}, s.qdrantVectorSize())
			if err == nil && len(vectors) == 0 {
				err = fmt.Errorf("embedding api returned no vectors")
			}
			if err != nil {
				return nil, err
			}
			queryVector = vectors[0]
		}
		queryEmbedding = float64ToFloat32(queryVector)
		if entry, ok := s.semanticCache.Get(cacheScope, queryEmbedding); ok {
			cached := s.filterRetrievedChunksToScope(req, knowledgeBaseIDs, entry.Chunks)
			if len(cached) > 0 || len(entry.Chunks) == 0 {
				return cached, nil
			}
		}
	}

	if s.queryRewriteEnabledForRequest(req) {
		if setter, ok := s.queryRewriter.(interface {
			SetChatConfigProvider(func() model.ChatModelConfig)
		}); ok {
			setter.SetChatConfigProvider(func() model.ChatModelConfig {
				return s.resolveChatConfig(req)
			})
		}
		if setter, ok := s.queryRewriter.(interface {
			SetMaxVariants(int)
		}); ok {
			setter.SetMaxVariants(s.queryRewriteMaxVariantsForRequest(req))
		}
		history := recentConversationHistory(req.Messages, 3)
		rewriteStartedAt := time.Now()
		rewriteResult, err := s.queryRewriter.Rewrite(ctx, query, history)
		if err != nil {
			logRetrievalStageMetrics(req, query, "query_rewrite", rewriteStartedAt, map[string]any{
				"status": "error",
				"error":  err.Error(),
			})
		} else {
			logRetrievalStageMetrics(req, query, "query_rewrite", rewriteStartedAt, map[string]any{
				"status":  "ok",
				"queries": len(rewriteResult.RewrittenQueries),
			})
			queries := limitRetrievalQueries(
				mergeRetrievalQueries([]string{query}, rewriteResult.RewrittenQueries),
				maxMultiQuerySearchQueries,
			)
			embeddingConfig := s.resolveEmbeddingConfig(req)

			candidates := make([]RetrievedChunk, 0)
			seenChunkIDs := make(map[string]struct{})
			for _, knowledgeBaseID := range knowledgeBaseIDs {
				results, err := s.rag.MultiQuerySearch(ctx, queries, knowledgeBaseID, params.candidateTopK, 0, embeddingConfig)
				if err != nil {
					return nil, fmt.Errorf("multi query search qdrant collection %s: %w", knowledgeBaseID, err)
				}
				for _, item := range results {
					if strings.TrimSpace(req.DocumentID) != "" && item.DocumentID != req.DocumentID {
						continue
					}
					if _, exists := seenChunkIDs[item.ID]; exists {
						continue
					}
					seenChunkIDs[item.ID] = struct{}{}
					candidates = append(candidates, item)
				}
			}
			candidates = s.filterRetrievedChunksToScope(req, knowledgeBaseIDs, candidates)
			selected := s.applySelectionStrategy(req, query, ctx, candidates, params)

			if autoExpand && strings.TrimSpace(req.DocumentID) == "" && isLowConfidenceSelection(query, selected) {
				expandedCandidateTopK := params.candidateTopK * 2
				expandedCandidates := make([]RetrievedChunk, 0)
				seenChunkIDs = make(map[string]struct{})
				for _, knowledgeBaseID := range knowledgeBaseIDs {
					results, err := s.rag.MultiQuerySearch(ctx, queries, knowledgeBaseID, expandedCandidateTopK, 0, embeddingConfig)
					if err != nil {
						continue
					}
					for _, item := range results {
						if strings.TrimSpace(req.DocumentID) != "" && item.DocumentID != req.DocumentID {
							continue
						}
						if _, exists := seenChunkIDs[item.ID]; exists {
							continue
						}
						seenChunkIDs[item.ID] = struct{}{}
						expandedCandidates = append(expandedCandidates, item)
					}
				}
				expandedCandidates = s.filterRetrievedChunksToScope(req, knowledgeBaseIDs, expandedCandidates)
				if len(expandedCandidates) > 0 {
					expandedParams := params
					expandedParams.perDocumentLimit++
					expandedSelected := s.applySelectionStrategy(req, query, ctx, expandedCandidates, expandedParams)
					if selectionQuality(expandedSelected) > selectionQuality(selected) {
						selected = expandedSelected
					}
				}
			}

			if s.semanticCache != nil && len(queryEmbedding) > 0 {
				s.semanticCache.Set(cacheScope, queryEmbedding, query, selected)
			}
			logRetrievalMetrics(req, query, params, candidates, selected)
			return selected, nil
		}
	}

	useHybrid := s.shouldUseHybridSearch(req)
	var searchQueries []string
	if len(queryVector) == 0 {
		embedCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
		vectors, err := s.rag.EmbedTexts(embedCtx, s.resolveEmbeddingConfig(req), []string{query}, s.qdrantVectorSize())
		if err == nil && len(vectors) == 0 {
			err = fmt.Errorf("embedding api returned no vectors")
		}
		if err != nil {
			return nil, err
		}
		queryVector = vectors[0]
		if s.semanticCache != nil {
			queryEmbedding = float64ToFloat32(queryVector)
		}
	}

	candidates, err := s.collectCandidatesForQueries(ctx, knowledgeBaseIDs, req, queryVector, searchQueries, params.candidateTopK, useHybrid, query)
	if err != nil {
		return nil, err
	}
	candidates = s.filterRetrievedChunksToScope(req, knowledgeBaseIDs, candidates)
	selected := s.applySelectionStrategy(req, query, ctx, candidates, params)

	if autoExpand && strings.TrimSpace(req.DocumentID) == "" && isLowConfidenceSelection(query, selected) {
		expandedCandidateTopK := params.candidateTopK * 2
		expandUseHybrid := useHybrid || s.shouldUseHybridFallback(selected)
		logRetrievalStageMetrics(req, query, "hybrid_fallback_decision", time.Now(), map[string]any{
			"status":           "ok",
			"base_search_mode": ternaryString(useHybrid, "hybrid", "dense"),
			"fallback_enabled": expandUseHybrid,
			"low_confidence":   true,
		})
		expandedCandidates, err := s.collectCandidatesForQueries(ctx, knowledgeBaseIDs, req, queryVector, searchQueries, expandedCandidateTopK, expandUseHybrid, query)
		if err == nil {
			expandedCandidates = s.filterRetrievedChunksToScope(req, knowledgeBaseIDs, expandedCandidates)
			expandedParams := params
			expandedParams.perDocumentLimit++
			expandedSelected := s.applySelectionStrategy(req, query, ctx, expandedCandidates, expandedParams)
			if selectionQuality(expandedSelected) > selectionQuality(selected) {
				selected = expandedSelected
			}
		}
	}

	if s.semanticCache != nil && len(queryEmbedding) > 0 {
		s.semanticCache.Set(cacheScope, queryEmbedding, query, selected)
	}
	logRetrievalMetrics(req, query, params, candidates, selected)
	return selected, nil
}

func (s *AppService) retrievalCacheScope(req model.ChatCompletionRequest, knowledgeBaseIDs []string) string {
	ids := append([]string(nil), knowledgeBaseIDs...)
	sort.Strings(ids)
	cfg := s.retrievalConfigForRequest(req)
	return fmt.Sprintf(
		"kb=%s|doc=%s|mode=%s|rerank=%s|rewrite=%t|variants=%d",
		strings.Join(ids, ","),
		strings.TrimSpace(req.DocumentID),
		s.resolvedRetrievalSearchMode(req),
		cfg.RerankStrategy,
		cfg.EnableQueryRewrite,
		cfg.QueryRewriteMaxVariants,
	)
}

func (s *AppService) filterRetrievedChunksToScope(req model.ChatCompletionRequest, knowledgeBaseIDs []string, chunks []RetrievedChunk) []RetrievedChunk {
	if s == nil || s.state == nil || len(chunks) == 0 {
		return nil
	}

	allowedKnowledgeBases := make(map[string]map[string]struct{}, len(knowledgeBaseIDs))
	s.state.Mu.RLock()
	for _, knowledgeBaseID := range knowledgeBaseIDs {
		knowledgeBaseID = strings.TrimSpace(knowledgeBaseID)
		kb, ok := s.state.KnowledgeBases[knowledgeBaseID]
		if !ok {
			continue
		}
		documents := make(map[string]struct{}, len(kb.Documents))
		for _, document := range kb.Documents {
			documentID := strings.TrimSpace(document.ID)
			if documentID != "" {
				documents[documentID] = struct{}{}
			}
		}
		allowedKnowledgeBases[knowledgeBaseID] = documents
	}
	s.state.Mu.RUnlock()

	expectedDocumentID := strings.TrimSpace(req.DocumentID)
	filtered := make([]RetrievedChunk, 0, len(chunks))
	for _, chunk := range chunks {
		knowledgeBaseID := strings.TrimSpace(chunk.KnowledgeBaseID)
		documentID := strings.TrimSpace(chunk.DocumentID)
		documents, allowed := allowedKnowledgeBases[knowledgeBaseID]
		if !allowed || documentID == "" {
			continue
		}
		if _, allowed = documents[documentID]; !allowed {
			continue
		}
		if expectedDocumentID != "" && documentID != expectedDocumentID {
			continue
		}
		filtered = append(filtered, chunk)
	}
	if len(filtered) != len(chunks) {
		log.Printf(
			"retrieval scope filter dropped %d/%d chunks for knowledgeBaseId=%q documentId=%q",
			len(chunks)-len(filtered),
			len(chunks),
			strings.TrimSpace(req.KnowledgeBaseID),
			expectedDocumentID,
		)
	}
	return filtered
}

func (s *AppService) resolveRetrievalKnowledgeBaseIDs(req model.ChatCompletionRequest) ([]string, error) {
	s.state.Mu.RLock()
	defer s.state.Mu.RUnlock()

	if strings.TrimSpace(req.KnowledgeBaseID) != "" {
		kb, ok := s.state.KnowledgeBases[req.KnowledgeBaseID]
		if !ok {
			return nil, fmt.Errorf("knowledge base not found")
		}
		if strings.TrimSpace(req.DocumentID) != "" {
			found := false
			for _, document := range kb.Documents {
				if document.ID == req.DocumentID {
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("document does not belong to knowledge base")
			}
		}
		return []string{req.KnowledgeBaseID}, nil
	}

	if strings.TrimSpace(req.DocumentID) != "" {
		matchedKnowledgeBaseID := ""
		for _, kb := range s.state.KnowledgeBases {
			for _, document := range kb.Documents {
				if document.ID == req.DocumentID {
					if matchedKnowledgeBaseID != "" {
						return nil, fmt.Errorf("document id is ambiguous; knowledgeBaseId is required")
					}
					matchedKnowledgeBaseID = kb.ID
				}
			}
		}
		if matchedKnowledgeBaseID != "" {
			return []string{matchedKnowledgeBaseID}, nil
		}
		return nil, fmt.Errorf("document not found")
	}

	ids := make([]string, 0, len(s.state.KnowledgeBases))
	for id := range s.state.KnowledgeBases {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func latestUserMessage(messages []model.ChatMessage) string {
	for index := len(messages) - 1; index >= 0; index-- {
		if strings.EqualFold(strings.TrimSpace(messages[index].Role), "user") {
			return messages[index].Content
		}
	}
	return ""
}

func recentConversationHistory(messages []model.ChatMessage, maxItems int) []string {
	if maxItems <= 0 {
		return nil
	}
	latestUserIndex := len(messages)
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(messages[i].Role), "user") {
			latestUserIndex = i
			break
		}
	}
	collected := make([]string, 0, maxItems)
	for i := latestUserIndex - 1; i >= 0 && len(collected) < maxItems; i-- {
		content := strings.TrimSpace(messages[i].Content)
		if content == "" || IsLegacyOperationalAssistantContent(content) {
			continue
		}
		collected = append(collected, content)
	}
	if len(collected) == 0 {
		return nil
	}
	for i, j := 0, len(collected)-1; i < j; i, j = i+1, j-1 {
		collected[i], collected[j] = collected[j], collected[i]
	}
	return collected
}

func (s *AppService) TrimChatMessages(messages []model.ChatMessage) []model.ChatMessage {
	if len(messages) == 0 {
		return nil
	}
	limit := s.ContextMessageLimit()
	trimmed := make([]model.ChatMessage, 0, len(messages))
	systemMessages := make([]model.ChatMessage, 0)
	nonSystem := make([]model.ChatMessage, 0, len(messages))
	for _, message := range messages {
		if strings.EqualFold(strings.TrimSpace(message.Role), "system") {
			systemMessages = append(systemMessages, message)
			continue
		}
		nonSystem = append(nonSystem, message)
	}
	if len(nonSystem) > limit {
		nonSystem = nonSystem[len(nonSystem)-limit:]
	}
	trimmed = append(trimmed, systemMessages...)
	trimmed = append(trimmed, nonSystem...)
	return trimmed
}

func chunkTexts(chunks []DocumentChunk) []string {
	texts := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		texts = append(texts, chunk.Text)
	}
	return texts
}

func (s *AppService) deleteDocumentChunks(knowledgeBaseID, documentID string) error {
	return s.deleteDocumentChunksWithContext(context.Background(), knowledgeBaseID, documentID)
}

func (s *AppService) deleteDocumentChunksWithContext(ctx context.Context, knowledgeBaseID, documentID string) error {
	if s == nil || s.qdrant == nil || !s.qdrant.IsEnabled() {
		return nil
	}
	ctx, cancel := context.WithTimeout(normalizeServiceContext(ctx), 2*time.Minute)
	defer cancel()
	if err := s.qdrant.DeletePointsByFilter(ctx, knowledgeBaseID, documentFilter(documentID)); err != nil {
		return fmt.Errorf("delete qdrant points for document %s: %w", documentID, err)
	}
	return nil
}

func documentFilter(documentID string) map[string]any {
	return map[string]any{
		"must": []map[string]any{
			{
				"key": "document_id",
				"match": map[string]any{
					"value": documentID,
				},
			},
		},
	}
}

func buildDocumentDetailResponse(s *AppService, document model.Document, content, contentSource string, chunks []DocumentChunk, focusChunkID string, options DocumentDetailOptions) model.DocumentDetailResponse {
	document = publicDocument(document)
	rawContent := strings.TrimSpace(content)
	rawContentTruncated := false
	rawContentLimit := documentDetailRawContentLimit
	if options.IncludeFullContent {
		rawContentLimit = 0
	}
	if rawContentLimit > 0 && len([]rune(rawContent)) > rawContentLimit {
		rawContent = truncateRunes(rawContent, rawContentLimit)
		rawContentTruncated = true
	}

	chunkLimit := documentDetailChunkLimit
	if options.IncludeAllChunks {
		chunkLimit = 0
	}
	previewCapacity := len(chunks)
	if chunkLimit > 0 {
		previewCapacity = minInt(len(chunks), chunkLimit)
	}
	chunkPreviews := make([]model.DocumentChunkPreview, 0, previewCapacity)
	summaryParts := make([]string, 0)
	summaryChunkCount := 0
	structuredRowCount := 0
	for index, chunk := range chunks {
		if chunk.Kind == "structured_summary" {
			summaryChunkCount++
			summaryParts = append(summaryParts, chunk.Text)
		}
		if chunk.Kind == "structured_row" {
			structuredRowCount++
		}
		if chunkLimit > 0 && index >= chunkLimit {
			continue
		}
		chunkPreviews = append(chunkPreviews, model.DocumentChunkPreview{
			ID:    chunk.ID,
			Index: chunk.Index,
			Kind:  chunk.Kind,
			Text:  truncateRunes(strings.TrimSpace(chunk.Text), documentDetailChunkTextLimit),
		})
	}
	focusChunkID = strings.TrimSpace(focusChunkID)
	if focusChunkID != "" && !documentChunkPreviewContains(chunkPreviews, focusChunkID) {
		for _, chunk := range chunks {
			if chunk.ID != focusChunkID {
				continue
			}
			chunkPreviews = append(chunkPreviews, model.DocumentChunkPreview{
				ID:    chunk.ID,
				Index: chunk.Index,
				Kind:  chunk.Kind,
				Text:  truncateRunes(strings.TrimSpace(chunk.Text), documentDetailChunkTextLimit),
			})
			break
		}
	}

	summary := strings.TrimSpace(strings.Join(summaryParts, "\n\n"))
	if summary == "" {
		summary = document.ContentPreview
	}

	vectorCount := 0
	if s != nil && s.qdrant != nil && s.qdrant.IsEnabled() && document.Status == "indexed" {
		vectorCount = len(chunks)
	}

	return model.DocumentDetailResponse{
		KnowledgeBaseID: document.KnowledgeBaseID,
		Document:        document,
		Diagnostics: model.DocumentIndexDiagnostics{
			RawContentChars:       len([]rune(content)),
			ChunkCount:            len(chunks),
			VectorCount:           vectorCount,
			SummaryChunkCount:     summaryChunkCount,
			StructuredRowCount:    structuredRowCount,
			RawContentAvailable:   strings.TrimSpace(content) != "",
			QdrantEnabled:         s != nil && s.qdrant != nil && s.qdrant.IsEnabled(),
			RawContentTruncated:   rawContentTruncated,
			ChunkPreviewTruncated: chunkLimit > 0 && len(chunks) > chunkLimit,
			ContentSource:         contentSource,
		},
		RawContent: rawContent,
		Summary:    summary,
		Chunks:     chunkPreviews,
	}
}

func documentChunkPreviewContains(chunks []model.DocumentChunkPreview, chunkID string) bool {
	for _, chunk := range chunks {
		if chunk.ID == chunkID {
			return true
		}
	}
	return false
}

func documentNeedsReindex(document model.Document, health model.KnowledgeBaseDocumentHealth) bool {
	if documentIndexErrorCode(document) != "" {
		return true
	}
	if document.Status != "indexed" {
		return true
	}
	if health.RawContentAvailable && health.ChunkCount == 0 {
		return true
	}
	if !health.RawContentAvailable {
		return true
	}
	if strings.TrimSpace(document.IndexedAt) == "" {
		return true
	}
	if document.IndexVersion != currentIndexVersion {
		return true
	}
	return false
}

func documentHealthRecommendation(document model.Document, health model.KnowledgeBaseDocumentHealth) string {
	switch {
	case documentIndexErrorCode(document) != "":
		return "索引失败，建议查看错误信息后重建索引。"
	case document.Status == "processing":
		return "文档仍在处理中，完成后再观察健康度。"
	case document.Status != "indexed":
		return "文档尚未完成索引，建议重建索引。"
	case !health.RawContentAvailable:
		return "原文不可读或为空，建议重新上传文档。"
	case health.ChunkCount == 0:
		return "未生成 chunk，建议重建索引或检查文件内容。"
	case health.SummaryChunkCount == 0 && health.StructuredRowCount > 0:
		return "结构化行已识别但摘要块缺失，建议重建索引。"
	case document.IndexVersion != currentIndexVersion:
		return "索引规则已更新，建议重建索引以启用最新检索能力。"
	default:
		return ""
	}
}

func knowledgeBaseHealthScore(metrics model.KnowledgeBaseHealthMetrics, needsReindexCount int) int {
	if metrics.DocumentCount == 0 {
		return 100
	}
	score := 100
	score -= metrics.FailedCount * 25
	score -= metrics.ProcessingCount * 10
	score -= metrics.EmptyContentCount * 15
	score -= needsReindexCount * 12
	if metrics.ChunkCount == 0 {
		score -= 25
	}
	if metrics.QdrantEnabled && metrics.IndexedCount > 0 && metrics.VectorCount == 0 {
		score -= 20
	}
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func knowledgeBaseHealthStatus(metricsScore int, metrics model.KnowledgeBaseHealthMetrics, needsReindexCount int) string {
	switch {
	case metrics.DocumentCount == 0:
		return "empty"
	case metrics.FailedCount > 0 || metricsScore < 60:
		return "attention"
	case metrics.ProcessingCount > 0 || needsReindexCount > 0 || metricsScore < 85:
		return "warning"
	default:
		return "healthy"
	}
}

func knowledgeBaseHealthRecommendations(metrics model.KnowledgeBaseHealthMetrics, needsReindexCount int) []string {
	recommendations := make([]string, 0)
	if metrics.DocumentCount == 0 {
		return []string{"当前知识库暂无文档，上传文档后可生成索引健康度。"}
	}
	if metrics.FailedCount > 0 {
		recommendations = append(recommendations, fmt.Sprintf("%d 份文档索引失败，建议查看文档详情并重建索引。", metrics.FailedCount))
	}
	if metrics.ProcessingCount > 0 {
		recommendations = append(recommendations, fmt.Sprintf("%d 份文档仍在处理中，请等待完成后再评估检索效果。", metrics.ProcessingCount))
	}
	if metrics.EmptyContentCount > 0 {
		recommendations = append(recommendations, fmt.Sprintf("%d 份文档原文为空或不可读，建议重新上传。", metrics.EmptyContentCount))
	}
	if needsReindexCount > 0 {
		recommendations = append(recommendations, fmt.Sprintf("%d 份文档建议重建索引。", needsReindexCount))
	}
	if metrics.QdrantEnabled && metrics.IndexedCount > 0 && metrics.VectorCount == 0 {
		recommendations = append(recommendations, "Qdrant 已启用但未统计到向量，建议重建知识库索引。")
	}
	if len(recommendations) == 0 {
		recommendations = append(recommendations, "知识库索引状态良好，可继续通过检索调试台观察命中质量。")
	}
	return recommendations
}

func isLaterRFC3339(candidate, current string) bool {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return false
	}
	if strings.TrimSpace(current) == "" {
		return true
	}
	candidateTime, candidateErr := time.Parse(time.RFC3339, candidate)
	currentTime, currentErr := time.Parse(time.RFC3339, current)
	if candidateErr != nil || currentErr != nil {
		return candidate > current
	}
	return candidateTime.After(currentTime)
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}

func previewFromChunks(chunks []DocumentChunk) string {
	if len(chunks) == 0 {
		return "暂未生成摘要"
	}
	return util.BuildContentPreviewFromText(chunks[0].Text)
}

func buildChunkText(chunks []RetrievedChunk) string {
	chunks = deduplicateRetrievedChunks(chunks)
	if len(chunks) == 0 {
		return ""
	}
	lines := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		lines = append(lines, fmt.Sprintf("[%s#%d] %s", chunk.DocumentName, chunk.Index+1, chunk.Text))
	}
	return strings.Join(lines, "\n\n")
}

func buildRetrievalDebugEvalCandidate(req model.ChatCompletionRequest, query string, lowConfidence bool, chunks []RetrievedChunk, contextText string) *model.EvalGroundTruthCase {
	if !lowConfidence || strings.TrimSpace(query) == "" {
		return nil
	}

	sourceDocuments := make([]model.EvalSourceDocument, 0)
	seen := map[string]struct{}{}
	for _, chunk := range chunks {
		if strings.TrimSpace(chunk.DocumentID) == "" {
			continue
		}
		key := chunk.KnowledgeBaseID + "\x00" + chunk.DocumentID + "\x00" + chunk.ID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		sourceDocuments = append(sourceDocuments, model.EvalSourceDocument{
			KnowledgeBaseID: chunk.KnowledgeBaseID,
			DocumentID:      chunk.DocumentID,
			ChunkID:         chunk.ID,
		})
		if len(sourceDocuments) >= 5 {
			break
		}
	}

	answer := clipEvalRunes(normalizeEvalWhitespace(contextText), 800)
	snippets := make([]string, 0, minInt(3, len(chunks)))
	for _, chunk := range chunks {
		text := clipEvalRunes(normalizeEvalWhitespace(chunk.Text), 120)
		if text == "" {
			continue
		}
		snippets = append(snippets, text)
		if len(snippets) >= 3 {
			break
		}
	}
	if len(snippets) == 0 && answer != "" {
		snippets = []string{clipEvalRunes(answer, 120)}
	}

	scope := strings.TrimSpace(req.DocumentID)
	if scope == "" {
		scope = strings.TrimSpace(req.KnowledgeBaseID)
	}
	if scope == "" {
		scope = "all"
	}

	return &model.EvalGroundTruthCase{
		ID:              fmt.Sprintf("debug-low-confidence-%s-%x", sanitizeEvalIDPart(scope), qdrantPointID(query)),
		Question:        query,
		Answer:          answer,
		AnswerSnippets:  snippets,
		SourceDocuments: sourceDocuments,
		AnswerType:      "retrieval-debug-candidate",
		Difficulty:      "hard",
		ReviewStatus:    evalReviewStatusPending,
		Disabled:        true,
		Notes:           "auto-generated from retrieval debug low-confidence result; please review before using as ground truth",
	}
}

func isListDetailQuery(query string) bool {
	return containsAnyText(strings.TrimSpace(query), []string{
		"全部", "所有", "完整", "有哪些", "名单", "清单", "列出", "逐项", "分别", "多少个", "几个",
	})
}

func containsAnyText(text string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func deduplicateRetrievedChunks(chunks []RetrievedChunk) []RetrievedChunk {
	if len(chunks) <= 1 {
		return chunks
	}
	seen := make(map[string]struct{}, len(chunks))
	filtered := make([]RetrievedChunk, 0, len(chunks))
	for _, chunk := range chunks {
		textKey := normalizeChunkDedupText(chunk.Text)
		if textKey == "" {
			textKey = strings.ToLower(strings.TrimSpace(chunk.DocumentName))
		}
		key := strings.ToLower(strings.TrimSpace(chunk.DocumentID)) + "|" + textKey
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		filtered = append(filtered, chunk)
	}
	return filtered
}

func normalizeChunkDedupText(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.ReplaceAll(trimmed, "\r\n", "\n")
	trimmed = strings.ReplaceAll(trimmed, "\r", "\n")
	trimmed = strings.Join(strings.Fields(trimmed), " ")
	return strings.ToLower(trimmed)
}

func chunksTotalChars(chunks []RetrievedChunk) int {
	if len(chunks) == 0 {
		return 0
	}
	count := 0
	for _, chunk := range chunks {
		count += len([]rune(chunk.Text))
	}
	return count
}

func logRetrievalStageMetrics(req model.ChatCompletionRequest, query, stage string, startedAt time.Time, fields map[string]any) {
	parts := []string{
		fmt.Sprintf("stage=%s", stage),
		fmt.Sprintf("query=%q", strings.TrimSpace(query)),
		fmt.Sprintf("scope_kb=%q", strings.TrimSpace(req.KnowledgeBaseID)),
		fmt.Sprintf("scope_doc=%q", strings.TrimSpace(req.DocumentID)),
		fmt.Sprintf("elapsed_ms=%d", time.Since(startedAt).Milliseconds()),
	}
	if len(fields) > 0 {
		keys := make([]string, 0, len(fields))
		for key := range fields {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			parts = append(parts, fmt.Sprintf("%s=%v", key, fields[key]))
		}
	}
	log.Printf("retrieval_stage %s", strings.Join(parts, " "))
}

func retrievalStatus(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}

func ternaryString(condition bool, whenTrue, whenFalse string) string {
	if condition {
		return whenTrue
	}
	return whenFalse
}

func splitTerms(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsNumber(r))
	})
	terms := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		item := strings.TrimSpace(field)
		if len([]rune(item)) < 2 {
			continue
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		terms = append(terms, item)
	}
	return terms
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func scoreBoost(text string) float64 {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) >= 80 && len(runes) <= 220 {
		return 0.015
	}
	if len(runes) < 20 {
		return -0.02
	}
	return 0
}

func payloadString(payload map[string]any, key, fallback string) string {
	value, ok := payload[key]
	if !ok {
		return fallback
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" || text == "<nil>" {
		return fallback
	}
	return text
}

func payloadStringSlice(payload map[string]any, key string) []string {
	value, ok := payload[key]
	if !ok {
		return nil
	}
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text != "" && text != "<nil>" {
				values = append(values, text)
			}
		}
		return values
	default:
		text := strings.TrimSpace(fmt.Sprint(value))
		if text == "" || text == "<nil>" {
			return nil
		}
		return []string{text}
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func qdrantPointID(value string) any {
	h := fnv.New64a()
	_, _ = h.Write([]byte(value))
	return h.Sum64()
}

func payloadInt(payload map[string]any, key string) int {
	value, ok := payload[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}
