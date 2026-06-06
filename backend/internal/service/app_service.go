package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"log"
	"math"
	"mime/multipart"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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
	structuredSourceRowLimit       = 12
	structuredSourceContextChars   = 1800

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
)

type AppService struct {
	state             *model.AppState
	store             *AppStateStore
	chatHistory       ChatHistoryStore
	qdrant            *QdrantService
	rag               *RagService
	serverConfig      model.ServerConfig
	staging           *UploadStagingService
	reranker          SemanticReranker
	queryRewriter     QueryRewriter
	semanticCache     *SemanticCache
	contextCompressor ContextCompressor
}

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
	service := &AppService{
		state:        defaultAppState(serverConfig),
		store:        store,
		chatHistory:  chatHistory,
		qdrant:       qdrant,
		rag:          NewRagService(),
		serverConfig: serverConfig,
		staging:      NewUploadStagingService(filepath.Join("data", "staging"), 30*time.Minute),
	}
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
		}
	}

	if len(service.state.KnowledgeBases) == 0 {
		service.state = defaultAppState(serverConfig)
	}
	service.state.Config.MCP.Enabled = serverConfig.EnableMCP
	service.state.Config.MCP.BasePath = defaultMCPBasePath(service.state.Config.MCP.BasePath)
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

	s.state.Mu.RLock()
	state := persistentAppState{
		Config:         s.state.Config,
		KnowledgeBases: cloneKnowledgeBases(s.state.KnowledgeBases),
		EvalDatasets:   cloneEvalDatasets(s.state.EvalDatasets),
		EvalRuns:       cloneEvalRuns(s.state.EvalRuns),
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
				Provider:            "ollama",
				BaseURL:             ollamaBaseURL,
				Model:               "qwen3.5:0.8b",
				APIKey:              "",
				Temperature:         0.7,
				ContextMessageLimit: 12,
			},
			Embedding: model.EmbeddingConfig{
				Provider: "ollama",
				BaseURL:  ollamaBaseURL,
				Model:    "nomic-embed-text",
				APIKey:   "",
			},
			MCP: model.MCPConfig{
				Enabled:  serverConfig.EnableMCP,
				BasePath: defaultMCPBasePath(serverConfig.MCPBasePath),
				Token:    generateMCPToken(),
			},
			Retrieval: defaultRetrievalConfig(serverConfig),
		},
		KnowledgeBases: map[string]model.KnowledgeBase{
			kbID: {
				ID:          kbID,
				Name:        "默认知识库",
				Description: "用于存放本地上传文档的默认知识库",
				Documents:   []model.Document{},
				CreatedAt:   now,
			},
		},
		EvalDatasets: map[string]model.EvalDataset{},
		EvalRuns:     map[string]model.RunEvalDatasetResponse{},
	}
}

func (s *AppService) GetHealthConfigMap(serverConfig model.ServerConfig) map[string]string {
	s.state.Mu.RLock()
	kbCount := len(s.state.KnowledgeBases)
	s.state.Mu.RUnlock()

	qdrantStatus := "disabled"
	if s.qdrant != nil && s.qdrant.IsEnabled() {
		qdrantStatus = "enabled"
	}

	return map[string]string{
		"port":               serverConfig.Port,
		"upload_dir":         serverConfig.UploadDir,
		"state_file":         serverConfig.StateFile,
		"knowledge_bases":    fmt.Sprintf("%d", kbCount),
		"qdrant_url":         serverConfig.QdrantURL,
		"qdrant_status":      qdrantStatus,
		"qdrant_vector_size": fmt.Sprintf("%d", serverConfig.QdrantVectorSize),
		"qdrant_distance":    serverConfig.QdrantDistance,
	}
}

func (s *AppService) GetConfig() model.AppConfig {
	s.state.Mu.RLock()
	defer s.state.Mu.RUnlock()

	cfg := s.state.Config
	if cfg.Chat.ContextMessageLimit <= 0 {
		cfg.Chat.ContextMessageLimit = 12
	}
	cfg.MCP.BasePath = defaultMCPBasePath(cfg.MCP.BasePath)
	if strings.TrimSpace(cfg.MCP.Token) == "" {
		cfg.MCP.Token = generateMCPToken()
	}
	cfg.Retrieval = normalizeRetrievalConfig(cfg.Retrieval, s.serverConfig)
	return cfg
}

func (s *AppService) StageUpload(file *multipart.FileHeader, source string) (model.StagedUpload, error) {
	if s == nil || s.staging == nil {
		return model.StagedUpload{}, fmt.Errorf("upload staging service is not configured")
	}
	return s.staging.StageMultipartFile(file, source)
}

func (s *AppService) StageInlineUpload(fileName string, content []byte, source string) (model.StagedUpload, error) {
	if s == nil || s.staging == nil {
		return model.StagedUpload{}, fmt.Errorf("upload staging service is not configured")
	}
	return s.staging.StageBytes(fileName, content, source)
}

func (s *AppService) RegisterStagedUpload(uploadID, knowledgeBaseID, fileName string) (model.Document, error) {
	if s == nil || s.staging == nil {
		return model.Document{}, fmt.Errorf("upload staging service is not configured")
	}
	staged, err := s.staging.Get(uploadID)
	if err != nil {
		return model.Document{}, err
	}
	resolvedKnowledgeBaseID, err := s.ResolveKnowledgeBaseID(knowledgeBaseID)
	if err != nil {
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
		Path:            staged.Path,
		ContentPreview:  util.ExtractContentPreview(staged.Path),
	}
	uploaded, err := s.IndexDocument(document)
	if err != nil {
		return model.Document{}, err
	}
	if err := s.staging.MarkConsumed(uploadID); err != nil {
		log.Printf("failed to mark staged upload consumed: %v", err)
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
	chatProvider := strings.TrimSpace(req.Chat.Provider)
	chatBaseURL := strings.TrimSpace(req.Chat.BaseURL)
	chatModel := strings.TrimSpace(req.Chat.Model)

	if chatProvider == "" || chatModel == "" {
		return model.AppConfig{}, fmt.Errorf("chat provider and model are required")
	}
	if chatBaseURL == "" {
		chatBaseURL = s.defaultBaseURL(chatProvider)
	}
	if req.Chat.Temperature < 0 || req.Chat.Temperature > 2 {
		return model.AppConfig{}, fmt.Errorf("chat temperature must be between 0 and 2")
	}

	embedProvider := strings.TrimSpace(req.Embedding.Provider)
	embedBaseURL := strings.TrimSpace(req.Embedding.BaseURL)
	embedModel := strings.TrimSpace(req.Embedding.Model)

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
	if mcpToken == "" {
		mcpToken = generateMCPToken()
	}

	nextConfig := model.AppConfig{
		Chat: model.ChatConfig{
			Provider:            chatProvider,
			BaseURL:             chatBaseURL,
			Model:               chatModel,
			APIKey:              strings.TrimSpace(req.Chat.APIKey),
			Temperature:         req.Chat.Temperature,
			ContextMessageLimit: contextMessageLimit,
		},
		Embedding: model.EmbeddingConfig{
			Provider: embedProvider,
			BaseURL:  embedBaseURL,
			Model:    embedModel,
			APIKey:   strings.TrimSpace(req.Embedding.APIKey),
		},
		MCP: model.MCPConfig{
			Enabled:  req.MCP.Enabled,
			BasePath: mcpBasePath,
			Token:    mcpToken,
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
	previousConfig := s.state.Config
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

	s.state.Mu.Lock()
	previousConfig := s.state.Config
	nextConfig := s.state.Config
	nextConfig.MCP.Enabled = s.serverConfig.EnableMCP
	nextConfig.MCP.BasePath = defaultMCPBasePath(nextConfig.MCP.BasePath)
	nextConfig.MCP.Token = generateMCPToken()
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
		knowledgeBases = append(knowledgeBases, kb)
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
		ID:          util.NextID("kb"),
		Name:        strings.TrimSpace(req.Name),
		Description: strings.TrimSpace(req.Description),
		Documents:   []model.Document{},
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
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

	if err := s.deleteKnowledgeBaseCollection(id); err != nil {
		return remaining, err
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

	return kb.Documents, nil
}

func (s *AppService) GetDocumentDetail(knowledgeBaseID, documentID, focusChunkID string) (model.DocumentDetailResponse, error) {
	document, err := s.findDocument(knowledgeBaseID, documentID)
	if err != nil {
		return model.DocumentDetailResponse{}, err
	}

	content, err := util.ExtractDocumentText(document.Path)
	if err != nil {
		return model.DocumentDetailResponse{}, fmt.Errorf("extract document text: %w", err)
	}

	chunks := s.rag.BuildDocumentChunks(document, content)
	return buildDocumentDetailResponse(s, document, content, chunks, focusChunkID), nil
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
		if strings.TrimSpace(document.IndexError) != "" || document.Status == "failed" {
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
		KnowledgeBaseID: kb.ID,
		Name:            kb.Name,
		Status:          status,
		Score:           score,
		Metrics:         metrics,
		Recommendations: knowledgeBaseHealthRecommendations(metrics, needsReindexCount),
		Documents:       documents,
	}, nil
}

func (s *AppService) buildKnowledgeBaseDocumentHealth(document model.Document) model.KnowledgeBaseDocumentHealth {
	item := model.KnowledgeBaseDocumentHealth{
		DocumentID:   document.ID,
		DocumentName: document.Name,
		Status:       document.Status,
		IndexedAt:    document.IndexedAt,
		IndexError:   document.IndexError,
		ChunkCount:   document.ChunkCount,
	}

	content, err := util.ExtractDocumentText(document.Path)
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

func (s *AppService) IndexDocument(document model.Document) (model.Document, error) {
	content, err := util.ExtractDocumentText(document.Path)
	if err != nil {
		return model.Document{}, fmt.Errorf("extract uploaded document text: %w", err)
	}

	chunks := s.rag.BuildDocumentChunks(document, content)
	if len(chunks) == 0 {
		document.ContentPreview = util.BuildContentPreviewFromText(content)
		document.Status = "ready"
		document.ChunkCount = 0
		document.IndexedAt = util.NowRFC3339()
		document.IndexError = ""
		return s.AddDocument(document.KnowledgeBaseID, document), nil
	}

	vectors, err := s.rag.EmbedTexts(context.Background(), s.currentEmbeddingConfig(), chunkTexts(chunks), s.qdrantVectorSize())
	if err != nil {
		return model.Document{}, err
	}

	if err := s.upsertDocumentChunks(document.KnowledgeBaseID, chunks, vectors); err != nil {
		return model.Document{}, err
	}

	document.Status = "indexed"
	document.ContentPreview = previewFromChunks(chunks)
	document.ChunkCount = len(chunks)
	document.IndexedAt = util.NowRFC3339()
	document.IndexError = ""
	return s.AddDocument(document.KnowledgeBaseID, document), nil
}

func (s *AppService) ReindexDocument(knowledgeBaseID, documentID string) (model.Document, error) {
	if s == nil {
		return model.Document{}, fmt.Errorf("app service is nil")
	}

	document, err := s.findDocument(knowledgeBaseID, documentID)
	if err != nil {
		return model.Document{}, err
	}
	if strings.TrimSpace(document.Path) == "" {
		return model.Document{}, fmt.Errorf("document path is empty")
	}

	if err := s.deleteDocumentChunks(knowledgeBaseID, documentID); err != nil {
		return model.Document{}, err
	}

	s.state.Mu.RLock()
	config := s.state.Config
	s.state.Mu.RUnlock()

	indexed, err := reindexDocumentWithConfig(s, config, document)
	if err != nil {
		document.Status = "ready"
		document.IndexError = err.Error()
		document.IndexedAt = util.NowRFC3339()
		_ = s.updateDocument(knowledgeBaseID, document)
		return model.Document{}, err
	}
	if err := s.updateDocument(knowledgeBaseID, indexed); err != nil {
		return model.Document{}, err
	}
	return indexed, nil
}

func (s *AppService) ReindexKnowledgeBase(knowledgeBaseID string) ([]model.Document, error) {
	if s == nil {
		return nil, fmt.Errorf("app service is nil")
	}
	knowledgeBaseID = strings.TrimSpace(knowledgeBaseID)
	if knowledgeBaseID == "" {
		return nil, fmt.Errorf("knowledge base id is required")
	}

	s.state.Mu.RLock()
	kb, ok := s.state.KnowledgeBases[knowledgeBaseID]
	if !ok {
		s.state.Mu.RUnlock()
		return nil, fmt.Errorf("knowledge base not found")
	}
	originalDocs := make([]model.Document, len(kb.Documents))
	copy(originalDocs, kb.Documents)
	config := s.state.Config
	s.state.Mu.RUnlock()

	if err := s.deleteKnowledgeBaseCollection(knowledgeBaseID); err != nil {
		return nil, err
	}
	if err := s.ensureKnowledgeBaseCollection(knowledgeBaseID); err != nil {
		return nil, err
	}

	reindexed := make([]model.Document, 0, len(originalDocs))
	for _, document := range originalDocs {
		doc := document
		if strings.TrimSpace(doc.Path) == "" {
			return nil, fmt.Errorf("document %s path is empty", doc.ID)
		}
		indexed, err := reindexDocumentWithConfig(s, config, doc)
		if err != nil {
			return nil, fmt.Errorf("reindex document %s: %w", doc.ID, err)
		}
		reindexed = append(reindexed, indexed)
	}

	s.state.Mu.Lock()
	kb = s.state.KnowledgeBases[knowledgeBaseID]
	kb.Documents = reindexed
	s.state.KnowledgeBases[knowledgeBaseID] = kb
	s.state.Mu.Unlock()

	if err := s.saveState(); err != nil {
		return nil, err
	}
	return reindexed, nil
}

func reindexDocumentWithConfig(s *AppService, cfg model.AppConfig, document model.Document) (model.Document, error) {
	content, err := util.ExtractDocumentText(document.Path)
	if err != nil {
		return model.Document{}, fmt.Errorf("extract uploaded document text: %w", err)
	}

	chunks := s.rag.BuildDocumentChunks(document, content)
	if len(chunks) == 0 {
		document.ContentPreview = util.BuildContentPreviewFromText(content)
		document.Status = "ready"
		document.ChunkCount = 0
		document.IndexedAt = util.NowRFC3339()
		document.IndexError = ""
		return document, nil
	}

	vectors, err := s.rag.EmbedTexts(context.Background(), model.EmbeddingModelConfig{
		Provider: strings.TrimSpace(cfg.Embedding.Provider),
		BaseURL:  strings.TrimSpace(cfg.Embedding.BaseURL),
		Model:    strings.TrimSpace(cfg.Embedding.Model),
		APIKey:   strings.TrimSpace(cfg.Embedding.APIKey),
	}, chunkTexts(chunks), s.qdrantVectorSize())
	if err != nil {
		return model.Document{}, err
	}

	if err := s.upsertDocumentChunks(document.KnowledgeBaseID, chunks, vectors); err != nil {
		return model.Document{}, err
	}

	document.Status = "indexed"
	document.ContentPreview = previewFromChunks(chunks)
	document.ChunkCount = len(chunks)
	document.IndexedAt = util.NowRFC3339()
	document.IndexError = ""
	return document, nil
}

func (s *AppService) AddDocument(knowledgeBaseID string, document model.Document) model.Document {
	s.state.Mu.Lock()
	kb := s.state.KnowledgeBases[knowledgeBaseID]
	kb.Documents = append([]model.Document{document}, kb.Documents...)
	s.state.KnowledgeBases[knowledgeBaseID] = kb
	s.state.Mu.Unlock()
	if err := s.saveState(); err != nil {
		log.Printf("failed to persist document state: %v", err)
	}
	return document
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
	s.state.Mu.Lock()
	kb, ok := s.state.KnowledgeBases[knowledgeBaseID]
	if !ok {
		s.state.Mu.Unlock()
		return model.Document{}, fmt.Errorf("knowledge base not found")
	}

	filtered := make([]model.Document, 0, len(kb.Documents))
	removed := false
	var removedDocument model.Document
	for _, document := range kb.Documents {
		if document.ID == documentID {
			removed = true
			removedDocument = document
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
	if err := s.deleteDocumentChunks(knowledgeBaseID, documentID); err != nil {
		log.Printf("failed to delete qdrant points for document %s: %v", documentID, err)
	}
	return removedDocument, nil
}

func (s *AppService) BuildRetrievalContext(req model.ChatCompletionRequest) (string, []map[string]string, error) {
	startedAt := time.Now()
	chunks, err := s.EvaluateRetrieve(req)
	if err != nil {
		return "", nil, err
	}

	query := latestUserMessage(req.Messages)
	deterministicStartedAt := time.Now()
	deterministicChunks, deterministicResult, deterministicUsed, err := s.buildStructuredDeterministicChunks(req, query)
	if err != nil {
		return "", nil, err
	}
	if deterministicUsed {
		chunks = append(deterministicChunks, chunks...)
	}
	logRetrievalStageMetrics(req, query, "context_structured_deterministic", deterministicStartedAt, map[string]any{
		"status":      "ok",
		"used":        deterministicUsed,
		"intent":      string(deterministicResult.Plan.Intent),
		"targetField": deterministicResult.Plan.TargetField,
		"chunks":      len(deterministicChunks),
	})

	expandStartedAt := time.Now()
	chunks = s.expandStructuredSourceRows(req, query, chunks)
	logRetrievalStageMetrics(req, query, "context_expand_structured_rows", expandStartedAt, map[string]any{
		"status":           "ok",
		"remaining_chunks": len(chunks),
	})

	dedupStartedAt := time.Now()
	chunks = deduplicateRetrievedChunks(chunks)
	logRetrievalStageMetrics(req, query, "context_deduplicate", dedupStartedAt, map[string]any{
		"status":           "ok",
		"remaining_chunks": len(chunks),
	})

	maxContextChars := s.retrievalMaxContextChars()
	trimStartedAt := time.Now()
	if maxContextChars > 0 {
		chunks = trimRetrievedChunksToContextLimit(chunks, maxContextChars)
	}
	logRetrievalStageMetrics(req, query, "context_trim", trimStartedAt, map[string]any{
		"status":            "ok",
		"remaining_chunks":  len(chunks),
		"max_context_chars": maxContextChars,
		"context_chars":     chunksTotalChars(chunks),
	})

	buildStartedAt := time.Now()
	contextText, sources := s.rag.BuildContext(chunks)
	logRetrievalStageMetrics(req, query, "context_build", buildStartedAt, map[string]any{
		"status":        "ok",
		"sources":       len(sources),
		"context_chars": len(contextText),
	})
	if s.contextCompressor != nil && maxContextChars > 0 && chunksTotalChars(chunks) > maxContextChars {
		compressStartedAt := time.Now()
		compressed, err := s.contextCompressor.Compress(context.Background(), query, chunks)
		if err == nil && strings.TrimSpace(compressed) != "" {
			contextText = compressed
			logRetrievalStageMetrics(req, query, "context_compress", compressStartedAt, map[string]any{
				"status":           "ok",
				"compressed_chars": len(contextText),
			})
		} else {
			logRetrievalStageMetrics(req, query, "context_compress", compressStartedAt, map[string]any{
				"status": "error",
				"error":  fmt.Sprint(err),
			})
		}
	}
	logRetrievalStageMetrics(req, query, "build_retrieval_context_total", startedAt, map[string]any{
		"status":        "ok",
		"sources":       len(sources),
		"context_chars": len(contextText),
	})
	return contextText, sources, nil
}

func (s *AppService) EvaluateRetrieve(req model.ChatCompletionRequest) ([]RetrievedChunk, error) {
	if s == nil {
		return nil, fmt.Errorf("app service is nil")
	}

	startedAt := time.Now()
	query := latestUserMessage(req.Messages)
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}

	var queryVector []float64
	embeddingStartedAt := time.Now()
	if !s.queryRewriteEnabledForRequest(req) {
		embedCtx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		vectors, err := s.rag.EmbedTexts(embedCtx, s.resolveEmbeddingConfig(req), []string{query}, s.qdrantVectorSize())
		if err != nil || len(vectors) == 0 {
			logRetrievalStageMetrics(req, query, "query_embedding", embeddingStartedAt, map[string]any{
				"status": "error",
				"error":  fmt.Sprint(err),
			})
			return nil, err
		}
		queryVector = vectors[0]
		logRetrievalStageMetrics(req, query, "query_embedding", embeddingStartedAt, map[string]any{
			"status":          "ok",
			"vector_size":     len(queryVector),
			"used_rewriter":   false,
			"used_cache_only": false,
		})
	} else {
		logRetrievalStageMetrics(req, query, "query_embedding", embeddingStartedAt, map[string]any{
			"status":        "skipped",
			"used_rewriter": true,
		})
	}

	chunks, err := s.retrieveRelevantChunks(req, queryVector)
	logRetrievalStageMetrics(req, query, "evaluate_retrieve_total", startedAt, map[string]any{
		"status":          retrievalStatus(err),
		"selected_chunks": len(chunks),
	})
	return chunks, err
}

func (s *AppService) DebugRetrieve(req model.RetrievalDebugRequest) (model.RetrievalDebugResponse, error) {
	if s == nil {
		return model.RetrievalDebugResponse{}, fmt.Errorf("app service is nil")
	}

	query := strings.TrimSpace(req.Query)
	if query == "" {
		return model.RetrievalDebugResponse{}, fmt.Errorf("query is required")
	}

	startedAt := time.Now()
	chatReq := model.ChatCompletionRequest{
		KnowledgeBaseID:         strings.TrimSpace(req.KnowledgeBaseID),
		DocumentID:              strings.TrimSpace(req.DocumentID),
		RetrievalMode:           normalizeRetrievalMode(req.SearchMode),
		RerankStrategy:          req.RerankStrategy,
		EnableQueryRewrite:      req.EnableQueryRewrite,
		QueryRewriteMaxVariants: req.QueryRewriteMaxVariants,
		Config:                  s.currentChatConfig(),
		Embedding:               s.currentEmbeddingConfig(),
		Messages: []model.ChatMessage{{
			Role:    "user",
			Content: query,
		}},
	}

	chunks, err := s.EvaluateRetrieve(chatReq)
	if err != nil {
		return model.RetrievalDebugResponse{}, err
	}

	trace := []model.RetrievalDebugTraceStep{{
		Stage:       "retrieve",
		Status:      "ok",
		Reason:      "基础检索、重排、MMR 和相关性过滤后的候选",
		OutputCount: len(chunks),
	}}
	trace = append(trace, model.RetrievalDebugTraceStep{
		Stage:  "rerank",
		Status: "ok",
		Reason: fmt.Sprintf("当前重排策略：%s", s.rerankStrategyForRequest(chatReq)),
	})
	if s.queryRewriteEnabledForRequest(chatReq) {
		trace = append(trace, model.RetrievalDebugTraceStep{
			Stage:  "query_rewrite",
			Status: "ok",
			Reason: fmt.Sprintf("已启用查询改写，最多生成 %d 个查询变体", s.queryRewriteMaxVariantsForRequest(chatReq)),
		})
	} else {
		trace = append(trace, model.RetrievalDebugTraceStep{
			Stage:  "query_rewrite",
			Status: "skipped",
			Reason: "未启用查询改写",
		})
	}
	deterministicChunks, deterministicResult, deterministicUsed, err := s.buildStructuredDeterministicChunks(chatReq, query)
	if err != nil {
		return model.RetrievalDebugResponse{}, err
	}
	if deterministicUsed {
		trace = append(trace, model.RetrievalDebugTraceStep{
			Stage:       "deterministic",
			Status:      "ok",
			Reason:      "命中结构化查询意图，补充确定性结果",
			InputCount:  len(chunks),
			OutputCount: len(chunks) + len(deterministicChunks),
		})
		chunks = append(deterministicChunks, chunks...)
	} else {
		trace = append(trace, model.RetrievalDebugTraceStep{
			Stage:       "deterministic",
			Status:      "skipped",
			Reason:      "未识别到可确定执行的结构化查询意图",
			OutputCount: len(chunks),
		})
	}
	expandedInputCount := len(chunks)
	chunks = s.expandStructuredSourceRows(chatReq, query, chunks)
	if len(chunks) != expandedInputCount {
		trace = append(trace, model.RetrievalDebugTraceStep{
			Stage:       "structured_source_expand",
			Status:      "ok",
			Reason:      "根据结构化摘要补全相关原始行",
			InputCount:  expandedInputCount,
			OutputCount: len(chunks),
		})
	}
	dedupInputCount := len(chunks)
	chunks = deduplicateRetrievedChunks(chunks)
	if len(chunks) != dedupInputCount {
		trace = append(trace, model.RetrievalDebugTraceStep{
			Stage:       "deduplicate",
			Status:      "ok",
			Reason:      "移除重复 chunk，保留首个更靠前结果",
			InputCount:  dedupInputCount,
			OutputCount: len(chunks),
		})
	}
	if req.TopK > 0 && len(chunks) > req.TopK {
		trace = append(trace, model.RetrievalDebugTraceStep{
			Stage:       "topk",
			Status:      "ok",
			Reason:      "根据调试 TopK 截断展示结果",
			InputCount:  len(chunks),
			OutputCount: req.TopK,
		})
		chunks = chunks[:req.TopK]
	}
	confidence := buildRetrievalDebugConfidence(query, chunks, deterministicUsed)
	retrievalLowConfidence := confidence.Status == "low"
	evidenceGate := s.buildRetrievalEvidenceGateDiagnostic(chatReq, query, chunks)

	contextText, sources := s.rag.BuildContext(chunks)
	contextText = truncateRunes(strings.TrimSpace(contextText), retrievalDebugContextLimit)
	evalCandidate := buildRetrievalDebugEvalCandidate(chatReq, query, retrievalLowConfidence, chunks, contextText)

	items := make([]model.RetrievalDebugChunk, 0, len(chunks))
	for _, chunk := range chunks {
		items = append(items, buildRetrievalDebugChunk(query, chunk, deterministicUsed))
	}

	return model.RetrievalDebugResponse{
		Query:             query,
		KnowledgeBaseID:   chatReq.KnowledgeBaseID,
		DocumentID:        chatReq.DocumentID,
		SearchMode:        s.resolvedRetrievalSearchMode(chatReq),
		RerankStrategy:    s.rerankStrategyForRequest(chatReq),
		QueryRewriteUsed:  s.queryRewriteEnabledForRequest(chatReq),
		StructuredIntent:  string(deterministicResult.Plan.Intent),
		TargetField:       deterministicResult.Plan.TargetField,
		DeterministicUsed: deterministicUsed,
		ElapsedMs:         time.Since(startedAt).Milliseconds(),
		Count:             len(items),
		LowConfidence:     retrievalLowConfidence,
		Confidence:        confidence,
		EvidenceGate:      evidenceGate,
		ContextPreview:    contextText,
		Sources:           sources,
		EvalCandidate:     evalCandidate,
		Trace:             trace,
		Items:             items,
	}, nil
}

func buildRetrievalDebugChunk(query string, chunk RetrievedChunk, deterministicUsed bool) model.RetrievalDebugChunk {
	return model.RetrievalDebugChunk{
		ID:                chunk.ID,
		KnowledgeBaseID:   chunk.KnowledgeBaseID,
		DocumentID:        chunk.DocumentID,
		DocumentName:      chunk.DocumentName,
		Index:             chunk.Index,
		Kind:              chunk.Kind,
		Score:             chunk.Score,
		Text:              truncateRunes(strings.TrimSpace(chunk.Text), retrievalDebugChunkTextLimit),
		MatchReasons:      buildRetrievalDebugMatchReasons(query, chunk, deterministicUsed),
		RetrievalChannels: chunk.RetrievalChannels,
		DenseRank:         chunk.DenseRank,
		SparseRank:        chunk.SparseRank,
	}
}

func (s *AppService) buildRetrievalEvidenceGateDiagnostic(req model.ChatCompletionRequest, query string, selected []RetrievedChunk) model.RetrievalEvidenceGateDiagnostic {
	if len(parseFactQuerySpecs(query)) == 0 {
		return model.RetrievalEvidenceGateDiagnostic{
			Enabled: false,
			Reason:  "当前问题未识别为主体 / 属性事实型问法",
		}
	}
	if s == nil || s.qdrant == nil || !s.qdrant.IsEnabled() || s.rag == nil {
		return model.RetrievalEvidenceGateDiagnostic{
			Enabled: false,
			Reason:  "检索服务未启用，无法生成门控诊断",
		}
	}

	knowledgeBaseIDs, err := s.resolveRetrievalKnowledgeBaseIDs(req)
	if err != nil {
		return model.RetrievalEvidenceGateDiagnostic{
			Enabled: false,
			Reason:  err.Error(),
		}
	}

	params := s.resolveRetrievalParams(req)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	vectors, err := s.rag.EmbedTexts(ctx, s.resolveEmbeddingConfig(req), []string{query}, s.qdrantVectorSize())
	if err != nil || len(vectors) == 0 {
		return model.RetrievalEvidenceGateDiagnostic{
			Enabled: false,
			Reason:  fmt.Sprintf("查询向量生成失败：%v", err),
		}
	}

	candidates, err := s.collectCandidatesForQueries(ctx, knowledgeBaseIDs, req, vectors[0], buildRuleBasedRetrievalQueries(query), params.candidateTopK, s.shouldUseHybridSearch(req), query)
	if err != nil {
		return model.RetrievalEvidenceGateDiagnostic{
			Enabled: false,
			Reason:  err.Error(),
		}
	}

	directEvidenceCount := 0
	weakEvidenceCount := 0
	for _, candidate := range candidates {
		switch score := factEvidenceScore(query, candidate); {
		case score >= 5:
			directEvidenceCount++
		case score > 0:
			weakEvidenceCount++
		}
	}

	selectedKeys := make(map[string]struct{}, len(selected))
	for _, item := range selected {
		selectedKeys[retrievedChunkKey(item)] = struct{}{}
	}
	removedCount := 0
	for _, candidate := range candidates {
		if _, ok := selectedKeys[retrievedChunkKey(candidate)]; !ok {
			removedCount++
		}
	}

	return model.RetrievalEvidenceGateDiagnostic{
		Enabled:             true,
		Reason:              "主体 / 属性事实型问法已启用证据优先门控",
		CandidateCount:      len(candidates),
		SelectedCount:       len(selected),
		DirectEvidenceCount: directEvidenceCount,
		WeakEvidenceCount:   weakEvidenceCount,
		RemovedCount:        removedCount,
		TopBefore:           debugChunksFromRetrieved(query, candidates, 5, false),
		TopAfter:            debugChunksFromRetrieved(query, selected, 5, false),
	}
}

func debugChunksFromRetrieved(query string, chunks []RetrievedChunk, limit int, deterministicUsed bool) []model.RetrievalDebugChunk {
	if len(chunks) == 0 || limit <= 0 {
		return nil
	}
	if len(chunks) > limit {
		chunks = chunks[:limit]
	}
	items := make([]model.RetrievalDebugChunk, 0, len(chunks))
	for _, chunk := range chunks {
		items = append(items, buildRetrievalDebugChunk(query, chunk, deterministicUsed))
	}
	return items
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

func (s *AppService) ServerConfig() model.ServerConfig {
	if s == nil {
		return model.ServerConfig{}
	}
	return s.serverConfig
}

func (s *AppService) BuildChatContext(req model.ChatCompletionRequest) (string, []map[string]string, error) {
	s.state.Mu.RLock()
	defer s.state.Mu.RUnlock()

	if req.DocumentID != "" {
		for _, kb := range s.state.KnowledgeBases {
			for _, document := range kb.Documents {
				if document.ID == req.DocumentID {
					return fmt.Sprintf("当前问答范围为文档《%s》，所属知识库为“%s”。文档摘要：%s", document.Name, kb.Name, document.ContentPreview), []map[string]string{{
						"knowledgeBaseId": kb.ID,
						"documentId":      document.ID,
						"documentName":    document.Name,
					}}, nil
				}
			}
		}

		return "", nil, fmt.Errorf("document not found")
	}

	if req.KnowledgeBaseID != "" {
		kb, ok := s.state.KnowledgeBases[req.KnowledgeBaseID]
		if !ok {
			return "", nil, fmt.Errorf("knowledge base not found")
		}

		sources := make([]map[string]string, 0, len(kb.Documents))
		for _, document := range kb.Documents {
			sources = append(sources, map[string]string{
				"knowledgeBaseId": kb.ID,
				"documentId":      document.ID,
				"documentName":    document.Name,
			})
		}

		return fmt.Sprintf("当前问答范围为知识库“%s”，其中包含 %d 份文档。", kb.Name, len(kb.Documents)), sources, nil
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
	if s.qdrant == nil || !s.qdrant.IsEnabled() {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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

func (s *AppService) resolveEmbeddingConfig(req model.ChatCompletionRequest) model.EmbeddingModelConfig {
	cfg := req.Embedding
	cfg.Provider = strings.TrimSpace(cfg.Provider)
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	if cfg.Provider != "" && cfg.BaseURL != "" && cfg.Model != "" {
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
		if cfg.ContextMessageLimit <= 0 {
			cfg.ContextMessageLimit = s.currentChatConfig().ContextMessageLimit
		}
		return cfg
	}
	return s.currentChatConfig()
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
				"chunk_index":       chunk.Index,
				"chunk_kind":        chunk.Kind,
				"text":              chunk.Text,
			},
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := s.qdrant.UpsertPoints(ctx, knowledgeBaseID, points); err != nil {
		return fmt.Errorf("upsert qdrant points: %w", err)
	}
	return nil
}

func (s *AppService) retrieveRelevantChunks(req model.ChatCompletionRequest, queryVector []float64) ([]RetrievedChunk, error) {
	if s.qdrant == nil || !s.qdrant.IsEnabled() {
		return nil, nil
	}

	knowledgeBaseIDs, err := s.resolveRetrievalKnowledgeBaseIDs(req)
	if err != nil {
		return nil, err
	}

	query := latestUserMessage(req.Messages)
	params := s.resolveRetrievalParams(req)
	autoExpand := s.retrievalAutoExpandEnabled()
	ctx := context.Background()

	var queryEmbedding []float32
	if s.semanticCache != nil {
		if len(queryVector) == 0 {
			embedCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
			defer cancel()
			vectors, err := s.rag.EmbedTexts(embedCtx, s.resolveEmbeddingConfig(req), []string{query}, s.qdrantVectorSize())
			if err != nil || len(vectors) == 0 {
				return nil, err
			}
			queryVector = vectors[0]
		}
		queryEmbedding = float64ToFloat32(queryVector)
		if entry, ok := s.semanticCache.Get(queryEmbedding); ok {
			return entry.Chunks, nil
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
		rewriteResult, err := s.queryRewriter.Rewrite(ctx, query, history)
		if err != nil {
			logRetrievalStageMetrics(req, query, "query_rewrite", time.Now(), map[string]any{
				"status": "error",
				"error":  err.Error(),
			})
		} else {
			logRetrievalStageMetrics(req, query, "query_rewrite", time.Now(), map[string]any{
				"status":  "ok",
				"queries": len(rewriteResult.RewrittenQueries),
			})
			queries := mergeRetrievalQueries([]string{query}, rewriteResult.RewrittenQueries, buildRuleBasedRetrievalQueries(query))
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
			candidates = mergeRetrievedChunks(candidates, s.collectLexicalFactCandidates(req, knowledgeBaseIDs, query, params.candidateTopK))

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
				expandedCandidates = mergeRetrievedChunks(expandedCandidates, s.collectLexicalFactCandidates(req, knowledgeBaseIDs, query, expandedCandidateTopK))
				if len(expandedCandidates) > 0 {
					expandedParams := params
					expandedParams.perDocumentLimit++
					expandedSelected := s.applySelectionStrategy(req, query, ctx, expandedCandidates, expandedParams)
					if selectionQuality(expandedSelected) > selectionQuality(selected) {
						selected = expandedSelected
					}
				}
			}

			gatedSelected := filterRelevantChunks(query, selected)
			logRetrievalStageMetrics(req, query, "relevance_gate", time.Now(), map[string]any{
				"status":            "ok",
				"input_count":       len(selected),
				"output_count":      len(gatedSelected),
				"evidence_coverage": queryEvidenceCoverage(query, gatedSelected),
			})
			selected = gatedSelected

			if s.semanticCache != nil && len(queryEmbedding) > 0 {
				s.semanticCache.Set(queryEmbedding, query, selected)
			}
			logRetrievalMetrics(req, query, params, candidates, selected)
			return selected, nil
		}
	}

	useHybrid := s.shouldUseHybridSearch(req)
	searchQueries := buildRuleBasedRetrievalQueries(query)
	if len(queryVector) == 0 {
		embedCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
		vectors, err := s.rag.EmbedTexts(embedCtx, s.resolveEmbeddingConfig(req), []string{query}, s.qdrantVectorSize())
		if err != nil || len(vectors) == 0 {
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
			expandedParams := params
			expandedParams.perDocumentLimit++
			expandedSelected := s.applySelectionStrategy(req, query, ctx, expandedCandidates, expandedParams)
			if selectionQuality(expandedSelected) > selectionQuality(selected) {
				selected = expandedSelected
			}
		}
	}

	gatedSelected := filterRelevantChunks(query, selected)
	logRetrievalStageMetrics(req, query, "relevance_gate", time.Now(), map[string]any{
		"status":            "ok",
		"input_count":       len(selected),
		"output_count":      len(gatedSelected),
		"evidence_coverage": queryEvidenceCoverage(query, gatedSelected),
	})
	selected = gatedSelected

	if s.semanticCache != nil && len(queryEmbedding) > 0 {
		s.semanticCache.Set(queryEmbedding, query, selected)
	}
	logRetrievalMetrics(req, query, params, candidates, selected)
	return selected, nil
}

func (s *AppService) resolveRetrievalKnowledgeBaseIDs(req model.ChatCompletionRequest) ([]string, error) {
	s.state.Mu.RLock()
	defer s.state.Mu.RUnlock()

	if strings.TrimSpace(req.KnowledgeBaseID) != "" {
		if _, ok := s.state.KnowledgeBases[req.KnowledgeBaseID]; !ok {
			return nil, fmt.Errorf("knowledge base not found")
		}
		return []string{req.KnowledgeBaseID}, nil
	}

	if strings.TrimSpace(req.DocumentID) != "" {
		for _, kb := range s.state.KnowledgeBases {
			for _, document := range kb.Documents {
				if document.ID == req.DocumentID {
					return []string{kb.ID}, nil
				}
			}
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
	collected := make([]string, 0, maxItems)
	for i := len(messages) - 1; i >= 0 && len(collected) < maxItems; i-- {
		content := strings.TrimSpace(messages[i].Content)
		if content == "" {
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
	if s == nil || s.qdrant == nil || !s.qdrant.IsEnabled() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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

func buildDocumentDetailResponse(s *AppService, document model.Document, content string, chunks []DocumentChunk, focusChunkID string) model.DocumentDetailResponse {
	rawContent := strings.TrimSpace(content)
	rawContentTruncated := false
	if len([]rune(rawContent)) > documentDetailRawContentLimit {
		rawContent = truncateRunes(rawContent, documentDetailRawContentLimit)
		rawContentTruncated = true
	}

	chunkPreviews := make([]model.DocumentChunkPreview, 0, minInt(len(chunks), documentDetailChunkLimit))
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
		if index >= documentDetailChunkLimit {
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
			ChunkPreviewTruncated: len(chunks) > documentDetailChunkLimit,
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
	if strings.TrimSpace(document.IndexError) != "" {
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
	return false
}

func documentHealthRecommendation(document model.Document, health model.KnowledgeBaseDocumentHealth) string {
	switch {
	case strings.TrimSpace(document.IndexError) != "":
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

func (s *AppService) buildStructuredDeterministicChunks(req model.ChatCompletionRequest, query string) ([]RetrievedChunk, structuredDeterministicResult, bool, error) {
	result, ok, err := s.buildStructuredDeterministicResult(req, query)
	if err != nil || !ok {
		return nil, result, ok, err
	}

	content := strings.TrimSpace(result.Content)
	if content == "" {
		return nil, result, false, nil
	}

	chunk := RetrievedChunk{
		DocumentChunk: DocumentChunk{
			ID:              structuredDeterministicChunkID(req, query, result),
			KnowledgeBaseID: strings.TrimSpace(req.KnowledgeBaseID),
			DocumentID:      strings.TrimSpace(req.DocumentID),
			DocumentName:    "结构化确定性查询",
			Text:            "结构化确定性查询结果（后端直接读取 CSV/XLSX 原始行计算，优先于向量摘要）：\n" + content,
			Kind:            "structured_deterministic",
		},
		Score: 1,
	}

	if len(result.Sources) > 0 {
		first := result.Sources[0]
		chunk.KnowledgeBaseID = first["knowledgeBaseId"]
		chunk.DocumentID = first["documentId"]
		if len(result.Sources) == 1 && strings.TrimSpace(first["documentName"]) != "" {
			chunk.DocumentName = first["documentName"]
		}
	}

	return []RetrievedChunk{chunk}, result, true, nil
}

func structuredDeterministicChunkID(req model.ChatCompletionRequest, query string, result structuredDeterministicResult) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.TrimSpace(req.KnowledgeBaseID)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strings.TrimSpace(req.DocumentID)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strings.TrimSpace(query)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(result.Content))
	return fmt.Sprintf("structured-deterministic-%x", h.Sum64())
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

func (s *AppService) expandStructuredSourceRows(req model.ChatCompletionRequest, query string, chunks []RetrievedChunk) []RetrievedChunk {
	if s == nil || s.rag == nil || !shouldExpandStructuredSourceRows(req, query, chunks) {
		return chunks
	}

	documents := s.resolveStructuredSourceDocuments(req, chunks)
	if len(documents) == 0 {
		return chunks
	}

	rowChunksByDocument := make(map[string][]RetrievedChunk, len(documents))
	for _, document := range documents {
		rowChunks := buildStructuredSourceRowChunks(document, query)
		if len(rowChunks) == 0 {
			continue
		}
		rowChunksByDocument[document.ID] = rowChunks
	}
	if len(rowChunksByDocument) == 0 {
		return chunks
	}

	return insertStructuredSourceRowChunks(chunks, rowChunksByDocument)
}

func shouldExpandStructuredSourceRows(req model.ChatCompletionRequest, query string, chunks []RetrievedChunk) bool {
	if strings.TrimSpace(req.DocumentID) != "" && isStructuredDataDetailQuery(query) {
		return true
	}

	hasStructuredSummary := false
	hasStructuredRow := false
	for _, chunk := range chunks {
		if chunk.Kind == "structured_summary" || strings.Contains(chunk.Text, "统计摘要：") {
			hasStructuredSummary = true
		}
		if chunk.Kind == "structured_row" || containsStructuredRowLine(chunk.Text) {
			hasStructuredRow = true
		}
	}
	return hasStructuredSummary && !hasStructuredRow && isStructuredDataDetailQuery(query)
}

func isStructuredDataDetailQuery(query string) bool {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return false
	}
	markers := []string{
		"表格", "数据", "明细", "原始", "完整", "全部", "所有", "列出", "展示", "读取",
		"有哪些", "名单", "每一行", "行数据", "详情",
	}
	for _, marker := range markers {
		if strings.Contains(trimmed, marker) {
			return true
		}
	}
	return false
}

func (s *AppService) resolveStructuredSourceDocuments(req model.ChatCompletionRequest, chunks []RetrievedChunk) []model.Document {
	if s == nil || s.state == nil {
		return nil
	}

	wanted := make(map[string]struct{})
	if documentID := strings.TrimSpace(req.DocumentID); documentID != "" {
		wanted[documentID] = struct{}{}
	} else {
		for _, chunk := range chunks {
			if strings.TrimSpace(chunk.DocumentID) == "" {
				continue
			}
			if chunk.Kind == "structured_summary" || strings.Contains(chunk.Text, "统计摘要：") || strings.Contains(chunk.Text, "数据行数：") {
				wanted[chunk.DocumentID] = struct{}{}
			}
		}
	}
	if len(wanted) == 0 {
		return nil
	}

	s.state.Mu.RLock()
	defer s.state.Mu.RUnlock()

	documents := make([]model.Document, 0, len(wanted))
	for _, kb := range s.state.KnowledgeBases {
		for _, document := range kb.Documents {
			if _, ok := wanted[document.ID]; !ok {
				continue
			}
			if strings.TrimSpace(document.Path) == "" {
				continue
			}
			documents = append(documents, document)
		}
	}
	sort.Slice(documents, func(i, j int) bool {
		return documents[i].ID < documents[j].ID
	})
	return documents
}

func buildStructuredSourceRowChunks(document model.Document, query string) []RetrievedChunk {
	text, err := util.ExtractDocumentText(document.Path)
	if err != nil || strings.TrimSpace(text) == "" {
		return nil
	}

	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	headerLine := ""
	rowLines := make([]string, 0, structuredSourceRowLimit)
	totalRows := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if headerLine == "" && strings.HasPrefix(trimmed, "文件：") && strings.Contains(trimmed, "字段：") {
			headerLine = trimmed
			continue
		}
		if !isStructuredRowLine(trimmed) {
			continue
		}
		totalRows++
		if len(rowLines) < structuredRowLimitForQuery(query) {
			rowLines = append(rowLines, trimmed)
		}
	}
	if len(rowLines) == 0 {
		return nil
	}

	label := "源文件行数据片段（来自已索引文件）"
	if totalRows > 0 && totalRows <= len(rowLines) {
		label = "源文件完整行数据（来自已索引文件）"
	}

	chunks := make([]RetrievedChunk, 0, 3)
	current := strings.Builder{}
	current.WriteString(label)
	if totalRows > len(rowLines) {
		fmt.Fprintf(&current, "：共 %d 行，以下为前 %d 行", totalRows, len(rowLines))
	}
	current.WriteString("。\n")
	if headerLine != "" {
		current.WriteString(headerLine)
		current.WriteString("\n")
	}

	flush := func() {
		text := strings.TrimSpace(current.String())
		if text == "" {
			return
		}
		chunks = append(chunks, RetrievedChunk{
			DocumentChunk: DocumentChunk{
				ID:              fmt.Sprintf("%s-source-rows-%d", document.ID, len(chunks)),
				KnowledgeBaseID: document.KnowledgeBaseID,
				DocumentID:      document.ID,
				DocumentName:    document.Name,
				Text:            text,
				Index:           len(chunks),
				Kind:            "structured_row",
			},
			Score: 1,
		})
		current.Reset()
	}

	usedChars := current.Len()
	for _, line := range rowLines {
		if usedChars+len(line)+1 > structuredSourceContextChars {
			break
		}
		if current.Len() > 0 && current.Len()+len(line)+1 > defaultChunkSize {
			flush()
		}
		if current.Len() == 0 {
			current.WriteString(label)
			current.WriteString("（续）。\n")
		}
		current.WriteString(line)
		current.WriteString("\n")
		usedChars += len(line) + 1
	}
	flush()
	return chunks
}

func structuredRowLimitForQuery(query string) int {
	if containsAnyText(query, []string{"完整", "全部", "所有", "每一行"}) {
		return structuredSourceRowLimit * 2
	}
	return structuredSourceRowLimit
}

func containsAnyText(text string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func insertStructuredSourceRowChunks(chunks []RetrievedChunk, rowChunksByDocument map[string][]RetrievedChunk) []RetrievedChunk {
	if len(chunks) == 0 {
		out := make([]RetrievedChunk, 0)
		for _, rowChunks := range rowChunksByDocument {
			out = append(out, rowChunks...)
		}
		return out
	}

	inserted := make(map[string]struct{}, len(rowChunksByDocument))
	out := make([]RetrievedChunk, 0, len(chunks)+len(rowChunksByDocument))
	for _, chunk := range chunks {
		out = append(out, chunk)
		if _, ok := inserted[chunk.DocumentID]; ok {
			continue
		}
		rowChunks := rowChunksByDocument[chunk.DocumentID]
		if len(rowChunks) == 0 {
			continue
		}
		out = append(out, rowChunks...)
		inserted[chunk.DocumentID] = struct{}{}
	}

	for documentID, rowChunks := range rowChunksByDocument {
		if _, ok := inserted[documentID]; ok {
			continue
		}
		out = append(out, rowChunks...)
	}
	return out
}

func containsStructuredRowLine(text string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if isStructuredRowLine(strings.TrimSpace(line)) {
			return true
		}
	}
	return false
}

func isStructuredRowLine(line string) bool {
	if !strings.HasPrefix(line, "第") || !strings.Contains(line, "行：") {
		return false
	}
	prefix := strings.TrimPrefix(line, "第")
	if prefix == line {
		return false
	}
	for _, r := range prefix {
		if r == '行' {
			return true
		}
		if !unicode.IsDigit(r) {
			return false
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
		count += len(chunk.Text)
	}
	return count
}

type retrievalParams struct {
	candidateTopK    int
	finalTopK        int
	perDocumentLimit int
}

func resolveRetrievalParams(req model.ChatCompletionRequest) retrievalParams {
	return resolveRetrievalParamsWithConfig(req, model.ServerConfig{})
}

func resolveRetrievalParamsWithConfig(req model.ChatCompletionRequest, cfg model.ServerConfig) retrievalParams {
	documentCandidateTopK := cfg.RetrievalCandidateTopKDocument
	if documentCandidateTopK <= 0 {
		documentCandidateTopK = ragSearchCandidateTopKDocument
	}
	documentFinalTopK := cfg.RetrievalTopKDocument
	if documentFinalTopK <= 0 {
		documentFinalTopK = ragSearchTopKDocument
	}
	knowledgeBaseCandidateTopK := cfg.RetrievalCandidateTopKAllDocs
	if knowledgeBaseCandidateTopK <= 0 {
		knowledgeBaseCandidateTopK = ragSearchCandidateTopKAllDocs
	}
	knowledgeBaseFinalTopK := cfg.RetrievalTopKKnowledgeBase
	if knowledgeBaseFinalTopK <= 0 {
		knowledgeBaseFinalTopK = ragSearchTopKKnowledgeBase
	}
	perDocumentLimit := cfg.RetrievalMaxChunksPerDocument
	if perDocumentLimit <= 0 {
		perDocumentLimit = ragMaxChunksPerDocument
	}

	if strings.TrimSpace(req.DocumentID) != "" {
		return retrievalParams{
			candidateTopK:    documentCandidateTopK,
			finalTopK:        documentFinalTopK,
			perDocumentLimit: maxInt(perDocumentLimit, documentFinalTopK),
		}
	}

	return retrievalParams{
		candidateTopK:    knowledgeBaseCandidateTopK,
		finalTopK:        knowledgeBaseFinalTopK,
		perDocumentLimit: perDocumentLimit,
	}
}

func (s *AppService) currentRetrievalConfig() model.RetrievalConfig {
	if s == nil || s.state == nil {
		if s == nil {
			return defaultRetrievalConfig(model.ServerConfig{})
		}
		return defaultRetrievalConfig(s.serverConfig)
	}
	s.state.Mu.RLock()
	cfg := s.state.Config.Retrieval
	s.state.Mu.RUnlock()
	return normalizeRetrievalConfig(cfg, s.serverConfig)
}

func (s *AppService) retrievalConfigForRequest(req model.ChatCompletionRequest) model.RetrievalConfig {
	cfg := s.currentRetrievalConfig()
	if strategy := normalizeRerankStrategy(req.RerankStrategy); strategy != "" {
		cfg.RerankStrategy = strategy
	}
	if req.EnableQueryRewrite != nil {
		cfg.EnableQueryRewrite = *req.EnableQueryRewrite
	}
	if req.QueryRewriteMaxVariants > 0 {
		cfg.QueryRewriteMaxVariants = minInt(maxInt(req.QueryRewriteMaxVariants, 1), 5)
	}
	return normalizeRetrievalConfig(cfg, s.serverConfig)
}

func (s *AppService) resolveRetrievalParams(req model.ChatCompletionRequest) retrievalParams {
	cfg := s.retrievalConfigForRequest(req)
	if strings.TrimSpace(req.DocumentID) != "" {
		return retrievalParams{
			candidateTopK:    cfg.CandidateTopKDocument,
			finalTopK:        cfg.TopKDocument,
			perDocumentLimit: maxInt(cfg.MaxChunksPerDocument, cfg.TopKDocument),
		}
	}
	return retrievalParams{
		candidateTopK:    cfg.CandidateTopKAllDocs,
		finalTopK:        cfg.TopKKnowledgeBase,
		perDocumentLimit: cfg.MaxChunksPerDocument,
	}
}

func (s *AppService) retrievalMaxContextChars() int {
	cfg := s.currentRetrievalConfig()
	if cfg.MaxContextChars <= 0 {
		return 2400
	}
	return cfg.MaxContextChars
}

func (s *AppService) retrievalAutoExpandEnabled() bool {
	if s == nil {
		return false
	}
	return s.currentRetrievalConfig().EnableLowConfidenceBoost
}

func (s *AppService) queryRewriteEnabled() bool {
	return s.queryRewriteEnabledForRequest(model.ChatCompletionRequest{})
}

func (s *AppService) queryRewriteEnabledForRequest(req model.ChatCompletionRequest) bool {
	if s == nil || s.queryRewriter == nil {
		return false
	}
	return s.retrievalConfigForRequest(req).EnableQueryRewrite
}

func (s *AppService) queryRewriteMaxVariantsForRequest(req model.ChatCompletionRequest) int {
	cfg := s.retrievalConfigForRequest(req)
	if cfg.QueryRewriteMaxVariants < 1 {
		return 3
	}
	if cfg.QueryRewriteMaxVariants > 5 {
		return 5
	}
	return cfg.QueryRewriteMaxVariants
}

func (s *AppService) rerankStrategy() string {
	return s.rerankStrategyForRequest(model.ChatCompletionRequest{})
}

func (s *AppService) rerankStrategyForRequest(req model.ChatCompletionRequest) string {
	if s == nil {
		return "keyword"
	}
	strategy := normalizeRerankStrategy(s.retrievalConfigForRequest(req).RerankStrategy)
	if strategy == "" {
		return "keyword"
	}
	return strategy
}

func trimRetrievedChunksToContextLimit(chunks []RetrievedChunk, maxChars int) []RetrievedChunk {
	if len(chunks) == 0 || maxChars <= 0 {
		return chunks
	}

	trimmed := make([]RetrievedChunk, 0, len(chunks))
	total := 0
	for _, chunk := range chunks {
		text := strings.TrimSpace(chunk.Text)
		if text == "" {
			continue
		}
		remaining := maxChars - total
		if remaining <= 0 {
			break
		}

		next := chunk
		if len(text) > remaining {
			next.Text = text[:remaining]
			trimmed = append(trimmed, next)
			break
		}

		next.Text = text
		trimmed = append(trimmed, next)
		total += len(next.Text)
	}

	return trimmed
}

func (s *AppService) collectCandidates(knowledgeBaseIDs []string, req model.ChatCompletionRequest, queryVector []float64, candidateTopK int, useHybrid bool, query string) ([]RetrievedChunk, error) {
	startedAt := time.Now()
	results := make([]RetrievedChunk, 0)
	seenChunkIDs := make(map[string]struct{})
	preferStructuredSummary := shouldPreferStructuredSummary(query)
	for _, knowledgeBaseID := range knowledgeBaseIDs {
		kbStartedAt := time.Now()
		filter := map[string]any{}
		if strings.TrimSpace(req.DocumentID) != "" {
			filter = map[string]any{
				"must": []map[string]any{{
					"key":   "document_id",
					"match": map[string]any{"value": req.DocumentID},
				}},
			}
		}

		var items []SearchResult
		if useHybrid {
			log.Printf("hybrid search enabled for knowledge base %s", knowledgeBaseID)
			sparseVector := BuildSparseVector(query)
			searchResults, err := s.rag.SearchHybrid(context.Background(), s.qdrant, knowledgeBaseID, queryVector, sparseVector, candidateTopK, filter)
			if err != nil {
				logRetrievalStageMetrics(req, query, "collect_candidates_kb", kbStartedAt, map[string]any{
					"status":         "error",
					"knowledge_base": knowledgeBaseID,
					"search_mode":    "hybrid",
					"candidate_topk": candidateTopK,
					"error":          err.Error(),
				})
				return nil, fmt.Errorf("hybrid search qdrant collection %s: %w", knowledgeBaseID, err)
			}
			items = searchResults
		} else {
			searchResults, err := s.qdrant.Search(context.Background(), knowledgeBaseID, queryVector, candidateTopK, filter)
			if err != nil {
				logRetrievalStageMetrics(req, query, "collect_candidates_kb", kbStartedAt, map[string]any{
					"status":         "error",
					"knowledge_base": knowledgeBaseID,
					"search_mode":    "dense",
					"candidate_topk": candidateTopK,
					"error":          err.Error(),
				})
				return nil, fmt.Errorf("search qdrant collection %s: %w", knowledgeBaseID, err)
			}
			items = searchResults
		}

		added := 0
		for itemIndex, item := range items {
			chunkID := payloadString(item.Payload, "chunk_id", item.ID)
			if _, exists := seenChunkIDs[chunkID]; exists {
				continue
			}
			text := payloadString(item.Payload, "text", "")
			if strings.TrimSpace(text) == "" {
				continue
			}
			retrievalChannels := payloadStringSlice(item.Payload, qdrantPayloadRetrievalChannels)
			if len(retrievalChannels) == 0 {
				retrievalChannels = []string{qdrantDenseVectorName}
			}
			denseRank := payloadInt(item.Payload, qdrantPayloadDenseRank)
			if denseRank == 0 && containsString(retrievalChannels, qdrantDenseVectorName) {
				denseRank = itemIndex + 1
			}
			seenChunkIDs[chunkID] = struct{}{}
			results = append(results, RetrievedChunk{
				DocumentChunk: DocumentChunk{
					ID:              chunkID,
					KnowledgeBaseID: payloadString(item.Payload, "knowledge_base_id", knowledgeBaseID),
					DocumentID:      payloadString(item.Payload, "document_id", ""),
					DocumentName:    payloadString(item.Payload, "document_name", "未知文档"),
					Text:            text,
					Index:           payloadInt(item.Payload, "chunk_index"),
					Kind:            payloadString(item.Payload, "chunk_kind", "text"),
				},
				Score:             item.Score,
				RawScore:          item.Score,
				RetrievalChannels: retrievalChannels,
				DenseRank:         denseRank,
				SparseRank:        payloadInt(item.Payload, qdrantPayloadSparseRank),
			})
			added++
		}
		logRetrievalStageMetrics(req, query, "collect_candidates_kb", kbStartedAt, map[string]any{
			"status":                    "ok",
			"knowledge_base":            knowledgeBaseID,
			"search_mode":               ternaryString(useHybrid, "hybrid", "dense"),
			"candidate_topk":            candidateTopK,
			"raw_hits":                  len(items),
			"added_hits":                added,
			"prefer_structured_summary": preferStructuredSummary,
		})
	}
	if preferStructuredSummary {
		results = prioritizeStructuredSummaryChunks(results)
	}
	logRetrievalStageMetrics(req, query, "collect_candidates_total", startedAt, map[string]any{
		"status":                    "ok",
		"knowledge_bases":           len(knowledgeBaseIDs),
		"candidate_topk":            candidateTopK,
		"unique_candidates":         len(results),
		"search_mode":               ternaryString(useHybrid, "hybrid", "dense"),
		"prefer_structured_summary": preferStructuredSummary,
	})
	return results, nil
}

func (s *AppService) collectCandidatesForQueries(ctx context.Context, knowledgeBaseIDs []string, req model.ChatCompletionRequest, queryVector []float64, queries []string, candidateTopK int, useHybrid bool, originalQuery string) ([]RetrievedChunk, error) {
	baseQuery := strings.TrimSpace(originalQuery)
	if baseQuery == "" {
		baseQuery = latestUserMessage(req.Messages)
	}
	searchQueries := mergeRetrievalQueries([]string{baseQuery}, queries)
	if len(searchQueries) == 0 {
		return nil, nil
	}

	embeddingConfig := s.resolveEmbeddingConfig(req)
	merged := make(map[string]RetrievedChunk)
	for index, searchQuery := range searchQueries {
		vector := queryVector
		if index > 0 || len(vector) == 0 {
			embedCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
			vectors, err := s.rag.EmbedTexts(embedCtx, embeddingConfig, []string{searchQuery}, s.qdrantVectorSize())
			cancel()
			if err == nil && len(vectors) == 0 {
				err = fmt.Errorf("empty query embedding")
			}
			if err != nil {
				if index == 0 {
					return nil, err
				}
				logRetrievalStageMetrics(req, searchQuery, "rule_query_variant_embed", time.Now(), map[string]any{
					"status": "error",
					"error":  err,
				})
				continue
			}
			vector = vectors[0]
		}

		candidates, err := s.collectCandidates(knowledgeBaseIDs, req, vector, candidateTopK, useHybrid, searchQuery)
		if err != nil {
			if index == 0 {
				return nil, err
			}
			continue
		}
		for _, item := range candidates {
			key := retrievedChunkKey(item)
			existing, ok := merged[key]
			if !ok || item.Score > existing.Score {
				merged[key] = item
			}
		}
	}

	results := make([]RetrievedChunk, 0, len(merged))
	for _, item := range merged {
		results = append(results, item)
	}
	results = mergeRetrievedChunks(results, s.collectLexicalFactCandidates(req, knowledgeBaseIDs, originalQuery, candidateTopK))
	sortRetrievedChunks(results)
	logRetrievalStageMetrics(req, originalQuery, "collect_query_variants", time.Now(), map[string]any{
		"status":            "ok",
		"query_variants":    len(searchQueries),
		"unique_candidates": len(results),
	})
	return results, nil
}

func (s *AppService) collectLexicalFactCandidates(req model.ChatCompletionRequest, knowledgeBaseIDs []string, query string, limit int) []RetrievedChunk {
	if s == nil || s.rag == nil || len(parseFactQuerySpecs(query)) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 8
	}

	documents := s.resolveLexicalCandidateDocuments(knowledgeBaseIDs, strings.TrimSpace(req.DocumentID))
	candidates := make([]RetrievedChunk, 0)
	for _, document := range documents {
		text, err := util.ExtractDocumentText(document.Path)
		if err != nil || strings.TrimSpace(text) == "" {
			continue
		}
		chunks := s.rag.BuildDocumentChunks(document, text)
		for _, chunk := range chunks {
			factScore := factEvidenceScore(query, RetrievedChunk{DocumentChunk: chunk})
			if factScore < 5 {
				continue
			}
			score := 0.78 + minFloat(float64(factScore)*0.03, 0.18)
			candidates = append(candidates, RetrievedChunk{
				DocumentChunk:     chunk,
				Score:             score,
				RawScore:          score,
				RetrievalChannels: []string{"lexical"},
			})
		}
	}
	sortRetrievedChunks(candidates)
	if len(candidates) > limit {
		return candidates[:limit]
	}
	return candidates
}

func (s *AppService) resolveLexicalCandidateDocuments(knowledgeBaseIDs []string, documentID string) []model.Document {
	if s == nil || s.state == nil {
		return nil
	}
	wantedKB := make(map[string]struct{}, len(knowledgeBaseIDs))
	for _, id := range knowledgeBaseIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			wantedKB[id] = struct{}{}
		}
	}

	s.state.Mu.RLock()
	defer s.state.Mu.RUnlock()
	documents := make([]model.Document, 0)
	for kbID, kb := range s.state.KnowledgeBases {
		if len(wantedKB) > 0 {
			if _, ok := wantedKB[kbID]; !ok {
				continue
			}
		}
		for _, document := range kb.Documents {
			if documentID != "" && document.ID != documentID {
				continue
			}
			documents = append(documents, document)
		}
	}
	return documents
}

func mergeRetrievedChunks(groups ...[]RetrievedChunk) []RetrievedChunk {
	merged := make(map[string]RetrievedChunk)
	for _, group := range groups {
		for _, item := range group {
			key := retrievedChunkKey(item)
			existing, ok := merged[key]
			if !ok || item.Score > existing.Score {
				merged[key] = item
			}
		}
	}
	results := make([]RetrievedChunk, 0, len(merged))
	for _, item := range merged {
		results = append(results, item)
	}
	sortRetrievedChunks(results)
	return results
}

func retrievedChunkKey(item RetrievedChunk) string {
	if strings.TrimSpace(item.ID) != "" {
		return item.DocumentID + "#" + item.ID
	}
	return item.DocumentID + "#" + strconv.Itoa(item.Index) + "#" + strings.TrimSpace(item.Text)
}

func shouldPreferStructuredSummary(query string) bool {
	lowered := strings.ToLower(strings.TrimSpace(query))
	if lowered == "" {
		return false
	}
	keywords := []string{"多少", "比例", "分布", "统计", "为什么", "占比", "人数", "数量", "资质", "地域"}
	for _, keyword := range keywords {
		if strings.Contains(lowered, keyword) {
			return true
		}
	}
	return false
}

func prioritizeStructuredSummaryChunks(chunks []RetrievedChunk) []RetrievedChunk {
	if len(chunks) <= 1 {
		return chunks
	}
	prioritized := make([]RetrievedChunk, len(chunks))
	copy(prioritized, chunks)
	sort.SliceStable(prioritized, func(i, j int) bool {
		leftSummary := prioritized[i].Kind == "structured_summary"
		rightSummary := prioritized[j].Kind == "structured_summary"
		if leftSummary != rightSummary {
			return leftSummary
		}
		return false
	})
	return prioritized
}

func (s *AppService) rerankCandidates(ctx context.Context, candidates []RetrievedChunk, query string, req model.ChatCompletionRequest) []RetrievedChunk {
	if len(candidates) == 0 {
		return nil
	}

	if s != nil && s.rerankStrategyForRequest(req) == "semantic" && s.reranker != nil {
		ranked, err := s.reranker.Rerank(ctx, query, candidates)
		if err == nil && len(ranked) > 0 {
			return ranked
		}
	}

	ranked, err := KeywordReranker{}.Rerank(ctx, query, candidates)
	if err != nil || len(ranked) == 0 {
		return candidates
	}
	return ranked
}

func (s *AppService) applySelectionStrategy(req model.ChatCompletionRequest, query string, ctx context.Context, candidates []RetrievedChunk, params retrievalParams) []RetrievedChunk {
	if len(candidates) == 0 {
		return nil
	}

	inputCount := len(candidates)
	selected := candidates
	if s.shouldBypassRerank(candidates) {
		logRetrievalStageMetrics(req, query, "rerank_candidates", time.Now(), map[string]any{
			"status":       "skipped",
			"reason":       "high_confidence_top_hit",
			"input_count":  inputCount,
			"output_count": inputCount,
		})
	} else {
		rerankStartedAt := time.Now()
		selected = s.rerankCandidates(ctx, candidates, query, req)
		logRetrievalStageMetrics(req, query, "rerank_candidates", rerankStartedAt, map[string]any{
			"status":       "ok",
			"input_count":  inputCount,
			"output_count": len(selected),
		})
	}

	if s.shouldBypassMMR(selected, params) {
		fastSelected := takeTopChunks(selected, params.finalTopK, params.perDocumentLimit)
		logRetrievalStageMetrics(req, query, "select_with_mmr", time.Now(), map[string]any{
			"status":             "skipped",
			"reason":             "low_candidate_count_or_high_confidence",
			"candidate_count":    len(selected),
			"selected_count":     len(fastSelected),
			"final_topk":         params.finalTopK,
			"per_document_limit": params.perDocumentLimit,
		})
		return fastSelected
	}

	mmrStartedAt := time.Now()
	mmrSelected := selectWithMMR(selected, params.finalTopK, params.perDocumentLimit)
	logRetrievalStageMetrics(req, query, "select_with_mmr", mmrStartedAt, map[string]any{
		"status":             "ok",
		"candidate_count":    len(selected),
		"selected_count":     len(mmrSelected),
		"final_topk":         params.finalTopK,
		"per_document_limit": params.perDocumentLimit,
	})
	return mmrSelected
}

func selectWithMMR(candidates []RetrievedChunk, finalTopK, perDocumentLimit int) []RetrievedChunk {
	if len(candidates) == 0 || finalTopK <= 0 {
		return nil
	}

	remaining := make([]RetrievedChunk, len(candidates))
	copy(remaining, candidates)
	selected := make([]RetrievedChunk, 0, minInt(finalTopK, len(candidates)))
	docSelected := make(map[string]int)

	for len(selected) < finalTopK && len(remaining) > 0 {
		bestIndex := -1
		bestScore := math.Inf(-1)
		for i := range remaining {
			candidate := remaining[i]
			if perDocumentLimit > 0 && docSelected[candidate.DocumentID] >= perDocumentLimit {
				continue
			}

			noveltyPenalty := maxTextSimilarity(candidate.Text, selected)
			mmrScore := mmrLambda*candidate.Score - (1-mmrLambda)*noveltyPenalty
			if mmrScore > bestScore {
				bestScore = mmrScore
				bestIndex = i
			}
		}

		if bestIndex < 0 {
			break
		}

		picked := remaining[bestIndex]
		selected = append(selected, picked)
		docSelected[picked.DocumentID]++
		remaining = append(remaining[:bestIndex], remaining[bestIndex+1:]...)
	}

	sort.Slice(selected, func(i, j int) bool {
		if selected[i].Score == selected[j].Score {
			if selected[i].DocumentID == selected[j].DocumentID {
				return selected[i].Index < selected[j].Index
			}
			return selected[i].DocumentID < selected[j].DocumentID
		}
		return selected[i].Score > selected[j].Score
	})
	return selected
}

func normalizeScore(value, minValue, maxValue float64) float64 {
	if maxValue <= minValue {
		if value <= 0 {
			return 0
		}
		if value >= 1 {
			return 1
		}
		return value
	}
	return (value - minValue) / (maxValue - minValue)
}

func keywordCoverage(query, text string) float64 {
	queryTerms := queryEvidenceTerms(query)
	if len(queryTerms) == 0 {
		return 0
	}
	if strings.TrimSpace(text) == "" {
		return 0
	}
	return float64(evidenceHitCount(queryTerms, text)) / float64(len(queryTerms))
}

func maxTextSimilarity(text string, selected []RetrievedChunk) float64 {
	if len(selected) == 0 {
		return 0
	}
	maxSimilarity := 0.0
	for _, item := range selected {
		similarity := textJaccardSimilarity(text, item.Text)
		if similarity > maxSimilarity {
			maxSimilarity = similarity
		}
	}
	return maxSimilarity
}

func textJaccardSimilarity(a, b string) float64 {
	aTerms := splitTerms(a)
	bTerms := splitTerms(b)
	if len(aTerms) == 0 || len(bTerms) == 0 {
		return 0
	}

	aSet := make(map[string]struct{}, len(aTerms))
	for _, term := range aTerms {
		aSet[term] = struct{}{}
	}
	bSet := make(map[string]struct{}, len(bTerms))
	for _, term := range bTerms {
		bSet[term] = struct{}{}
	}

	intersect := 0
	for term := range aSet {
		if _, ok := bSet[term]; ok {
			intersect++
		}
	}
	union := len(aSet) + len(bSet) - intersect
	if union <= 0 {
		return 0
	}
	return float64(intersect) / float64(union)
}

func isLowConfidenceSelection(query string, chunks []RetrievedChunk) bool {
	if len(chunks) == 0 {
		return true
	}
	topScore := chunks[0].Score
	avgScore := averageScore(chunks)
	if topScore < lowConfidenceTopScoreThreshold || avgScore < lowConfidenceAvgScoreThreshold {
		return true
	}
	if factEvidenceMatched(query, chunks) {
		return false
	}
	return queryEvidenceCoverage(query, chunks) < 0.2
}

func buildRetrievalDebugConfidence(query string, chunks []RetrievedChunk, deterministicUsed bool) model.RetrievalDebugConfidence {
	topScore := 0.0
	if len(chunks) > 0 {
		topScore = chunks[0].Score
	}
	avgScore := averageScore(chunks)
	evidenceCoverage := queryEvidenceCoverage(query, chunks)
	reasons := make([]string, 0, 4)
	suggestions := make([]string, 0, 4)

	if len(chunks) == 0 {
		reasons = append(reasons, "没有命中任何候选 chunk")
		suggestions = append(suggestions,
			"检查当前知识库或文档范围是否过窄",
			"确认文档已完成索引并且原文内容可用",
			"换用更具体的问题后重新检索",
		)
		return model.RetrievalDebugConfidence{
			Status:           "low",
			Summary:          "低置信：没有可用于回答的证据片段。",
			Reasons:          reasons,
			Suggestions:      suggestions,
			TopScore:         topScore,
			AverageScore:     avgScore,
			EvidenceCoverage: evidenceCoverage,
		}
	}

	if topScore < lowConfidenceTopScoreThreshold {
		reasons = append(reasons, fmt.Sprintf("最高命中分 %.4f 低于阈值 %.2f", topScore, lowConfidenceTopScoreThreshold))
		suggestions = append(suggestions, "尝试切换混合检索，补充关键词召回信号")
	}
	if avgScore < lowConfidenceAvgScoreThreshold {
		reasons = append(reasons, fmt.Sprintf("平均命中分 %.4f 低于阈值 %.2f", avgScore, lowConfidenceAvgScoreThreshold))
		suggestions = append(suggestions, "扩大候选 TopK 或检查文档切分是否过碎")
	}
	if evidenceCoverage < 0.2 && !factEvidenceMatched(query, chunks) {
		reasons = append(reasons, fmt.Sprintf("问题实体覆盖率 %.1f%% 低于 20%%", evidenceCoverage*100))
		suggestions = append(suggestions, "启用 Query Rewrite 或改用更贴近文档原文的问法")
	}
	if len(reasons) == 0 {
		summary := "置信正常：命中分数和问题实体覆盖率都处于可接受范围。"
		if deterministicUsed {
			summary = "置信正常：已命中结构化确定性路径，并补充了可核对的结构化结果。"
		}
		return model.RetrievalDebugConfidence{
			Status:           "normal",
			Summary:          summary,
			TopScore:         topScore,
			AverageScore:     avgScore,
			EvidenceCoverage: evidenceCoverage,
		}
	}
	if deterministicUsed {
		reasons = append(reasons, "已命中结构化确定性路径，但仍存在其他低置信信号")
		suggestions = append(suggestions, "优先核对确定性结果是否覆盖目标字段")
	}

	return model.RetrievalDebugConfidence{
		Status:           "low",
		Summary:          "低置信：当前证据不足以稳定支撑回答，建议复核后再作为依据。",
		Reasons:          deduplicateStrings(reasons),
		Suggestions:      deduplicateStrings(suggestions),
		TopScore:         topScore,
		AverageScore:     avgScore,
		EvidenceCoverage: evidenceCoverage,
	}
}

func deduplicateStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result
}

func filterRelevantChunks(query string, chunks []RetrievedChunk) []RetrievedChunk {
	if len(chunks) == 0 {
		return nil
	}
	terms := queryEvidenceTerms(query)
	if len(terms) == 0 {
		return chunks
	}
	factSpecs := parseFactQuerySpecs(query)

	// 检测是否为结构化表格数据查询（仅检查文件扩展名和表格关键词）
	// 注意：不包括 len(factSpecs) > 0，因为事实问题查询（如"校长是谁"）也会产生 factSpecs
	isStructuredQuery := strings.Contains(query, ".xlsx") || strings.Contains(query, ".csv") ||
	                     strings.Contains(query, ".xls") || strings.Contains(query, "工作表") ||
	                     strings.Contains(query, "表格")

	// 对于结构化表格数据查询，完全信任向量检索结果，不进行词汇过滤
	if isStructuredQuery {
		return chunks
	}

	filtered := make([]RetrievedChunk, 0, len(chunks))
	factFiltered := make([]RetrievedChunk, 0, len(chunks))
	preferStructuredSummary := shouldPreferStructuredSummary(query)

	for _, chunk := range chunks {
		if preferStructuredSummary && (chunk.Kind == "structured_summary" || chunk.Kind == "structured_row") {
			filtered = append(filtered, chunk)
			continue
		}
		if len(factSpecs) > 0 && factEvidenceScore(query, chunk) >= 5 {
			factFiltered = append(factFiltered, chunk)
			continue
		}
		hits := evidenceHitCount(terms, chunk.Text)
		coverage := float64(hits) / float64(len(terms))
		rawScore := chunkRawScore(chunk)

		// 原有的严格过滤逻辑（仅用于非结构化查询）
		switch {
		case hits >= 2:
			filtered = append(filtered, chunk)
		case coverage >= 0.25:
			filtered = append(filtered, chunk)
		case hits >= 1 && rawScore >= 0.55:
			filtered = append(filtered, chunk)
		case rawScore >= 0.82 && queryEvidenceCoverage(query, []RetrievedChunk{chunk}) > 0:
			filtered = append(filtered, chunk)
		}
	}

	if len(factFiltered) > 0 {
		sortFactEvidenceChunks(query, factFiltered)
		return factFiltered
	}

	if len(filtered) > 0 {
		if len(factSpecs) > 0 {
			sortFactEvidenceChunks(query, filtered)
		}
		return filtered
	}
	return nil
}

func sortFactEvidenceChunks(query string, chunks []RetrievedChunk) {
	sort.SliceStable(chunks, func(i, j int) bool {
		leftScore := factEvidenceScore(query, chunks[i])
		rightScore := factEvidenceScore(query, chunks[j])
		if leftScore == rightScore {
			return chunkRawScore(chunks[i]) > chunkRawScore(chunks[j])
		}
		return leftScore > rightScore
	})
}

func buildRetrievalDebugMatchReasons(query string, chunk RetrievedChunk, deterministicUsed bool) []string {
	reasons := make([]string, 0, 5)
	terms := queryEvidenceTerms(query)
	if len(terms) > 0 {
		hits := evidenceHitCount(terms, chunk.Text)
		if hits > 0 {
			reasons = append(reasons, fmt.Sprintf("匹配查询证据词 %d/%d", hits, len(terms)))
		} else {
			reasons = append(reasons, "未直接匹配查询证据词，依赖向量相似度")
		}
	}

	rawScore := chunkRawScore(chunk)
	switch {
	case rawScore >= 0.82:
		reasons = append(reasons, "原始检索分较高")
	case rawScore >= 0.55:
		reasons = append(reasons, "原始检索分中等")
	case rawScore > 0:
		reasons = append(reasons, "原始检索分偏低")
	}

	coverage := keywordCoverage(query, chunk.Text)
	if coverage >= 0.5 {
		reasons = append(reasons, "关键词覆盖较好")
	}

	if chunk.Kind == "structured_summary" || chunk.Kind == "structured_row" {
		reasons = append(reasons, "结构化数据片段")
	}
	if containsString(chunk.RetrievalChannels, "lexical") {
		reasons = append(reasons, "主体属性词法兜底命中")
	}
	if deterministicUsed && (chunk.Kind == "structured_summary" || chunk.Kind == "structured_row") {
		reasons = append(reasons, "确定性结构化查询补充")
	}

	if len(reasons) == 0 {
		reasons = append(reasons, "由检索排序策略保留")
	}
	return reasons
}

func (s *AppService) shouldBypassRerank(candidates []RetrievedChunk) bool {
	if len(candidates) == 0 {
		return true
	}
	if len(candidates) == 1 {
		return true
	}
	return candidates[0].Score >= 0.92 && scoreGap(candidates) >= 0.12
}

func (s *AppService) shouldBypassMMR(candidates []RetrievedChunk, params retrievalParams) bool {
	if len(candidates) == 0 {
		return true
	}
	if len(candidates) <= minInt(params.finalTopK, 3) {
		return true
	}
	return candidates[0].Score >= 0.9 && scoreGap(candidates) >= 0.15
}

func takeTopChunks(candidates []RetrievedChunk, finalTopK, perDocumentLimit int) []RetrievedChunk {
	if len(candidates) == 0 || finalTopK <= 0 {
		return nil
	}
	selected := make([]RetrievedChunk, 0, minInt(finalTopK, len(candidates)))
	docSelected := make(map[string]int)
	for _, candidate := range candidates {
		if perDocumentLimit > 0 && docSelected[candidate.DocumentID] >= perDocumentLimit {
			continue
		}
		selected = append(selected, candidate)
		docSelected[candidate.DocumentID]++
		if len(selected) >= finalTopK {
			break
		}
	}
	return selected
}

func scoreGap(chunks []RetrievedChunk) float64 {
	if len(chunks) < 2 {
		return 1
	}
	return chunks[0].Score - chunks[1].Score
}

func entityCoverage(query string, chunks []RetrievedChunk) float64 {
	entities := queryEvidenceTerms(query)
	if len(entities) == 0 {
		return 1
	}
	joined := strings.ToLower(strings.Join(chunkTextsFromRetrieved(chunks), "\n"))
	if strings.TrimSpace(joined) == "" {
		return 0
	}

	hit := 0
	for _, entity := range entities {
		if strings.Contains(joined, strings.ToLower(entity)) {
			hit++
		}
	}
	return float64(hit) / float64(len(entities))
}

func queryEvidenceCoverage(query string, chunks []RetrievedChunk) float64 {
	return entityCoverage(query, chunks)
}

type factQuerySpec struct {
	Subject   string
	Attribute string
	Aliases   []string
}

func factEvidenceMatched(query string, chunks []RetrievedChunk) bool {
	specs := parseFactQuerySpecs(query)
	if len(specs) == 0 || len(chunks) == 0 {
		return false
	}
	joined := strings.ToLower(strings.Join(chunkTextsFromRetrieved(chunks), "\n"))
	if strings.TrimSpace(joined) == "" {
		return false
	}
	for _, spec := range specs {
		if spec.Subject != "" && !strings.Contains(joined, strings.ToLower(spec.Subject)) {
			continue
		}
		for _, alias := range mergeRetrievalQueries([]string{spec.Attribute}, spec.Aliases) {
			if strings.Contains(joined, strings.ToLower(alias)) {
				return true
			}
		}
	}
	return false
}

func factEvidenceScore(query string, chunk RetrievedChunk) int {
	specs := parseFactQuerySpecs(query)
	if len(specs) == 0 {
		return 0
	}
	text := strings.ToLower(strings.TrimSpace(chunk.Text))
	if text == "" {
		return 0
	}
	best := 0
	for _, spec := range specs {
		score := 0
		if spec.Subject != "" && strings.Contains(text, strings.ToLower(spec.Subject)) {
			score += 3
		}
		attributeMatched := false
		for _, alias := range mergeRetrievalQueries([]string{spec.Attribute}, spec.Aliases) {
			if strings.Contains(text, strings.ToLower(alias)) {
				attributeMatched = true
				break
			}
		}
		if attributeMatched {
			score += 2
		}
		if strings.Contains(text, "概况") || strings.Contains(text, "信息") || strings.Contains(text, "详情") || strings.Contains(text, "简介") {
			score++
		}
		if score > best {
			best = score
		}
	}
	return best
}

func evidenceHitCount(terms []string, text string) int {
	if len(terms) == 0 || strings.TrimSpace(text) == "" {
		return 0
	}
	lowered := strings.ToLower(text)
	hits := 0
	for _, term := range terms {
		if strings.Contains(lowered, strings.ToLower(term)) {
			hits++
		}
	}
	return hits
}

func queryEvidenceTerms(query string) []string {
	normalized := strings.TrimSpace(strings.ToLower(query))
	if normalized == "" {
		return nil
	}

	terms := splitTerms(normalized)
	for _, spec := range parseFactQuerySpecs(normalized) {
		terms = append(terms, spec.Subject, spec.Attribute)
		terms = append(terms, spec.Aliases...)
	}
	for _, segment := range continuousCJKSegments(normalized) {
		runes := []rune(segment)
		if len(runes) < 3 {
			continue
		}
		maxN := minInt(4, len(runes))
		for n := 2; n <= maxN; n++ {
			for i := 0; i+n <= len(runes); i++ {
				terms = append(terms, string(runes[i:i+n]))
			}
		}
	}

	stopTerms := map[string]struct{}{
		"什么": {}, "多少": {}, "几个": {}, "如何": {}, "怎么": {}, "是否": {},
		"是谁": {}, "哪些": {}, "有没有": {}, "请问": {}, "告诉": {}, "一下": {},
		"the": {}, "and": {}, "for": {}, "with": {}, "what": {}, "which": {},
		"who": {}, "how": {}, "where": {}, "when": {}, "is": {}, "are": {},
	}
	filtered := make([]string, 0, len(terms))
	seen := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		term = strings.TrimSpace(strings.ToLower(term))
		if len([]rune(term)) < 2 {
			continue
		}
		if _, stop := stopTerms[term]; stop {
			continue
		}
		if _, exists := seen[term]; exists {
			continue
		}
		seen[term] = struct{}{}
		filtered = append(filtered, term)
	}
	return filtered
}

func buildRuleBasedRetrievalQueries(query string) []string {
	specs := parseFactQuerySpecs(query)
	if len(specs) == 0 {
		return nil
	}
	queries := make([]string, 0, len(specs)*8)
	for _, spec := range specs {
		if spec.Subject != "" && spec.Attribute != "" {
			queries = append(queries,
				fmt.Sprintf("%s %s", spec.Subject, spec.Attribute),
				fmt.Sprintf("%s %s", spec.Attribute, spec.Subject),
			)
		}
		for _, alias := range spec.Aliases {
			if spec.Subject != "" {
				queries = append(queries,
					fmt.Sprintf("%s %s", spec.Subject, alias),
					fmt.Sprintf("%s %s", alias, spec.Subject),
				)
			} else {
				queries = append(queries, alias)
			}
		}
		if spec.Subject != "" {
			queries = append(queries,
				fmt.Sprintf("%s 信息", spec.Subject),
				fmt.Sprintf("%s 概况", spec.Subject),
			)
		}
	}
	return mergeRetrievalQueries(queries)
}

func parseFactQuerySpecs(query string) []factQuerySpec {
	normalized := normalizeFactQueryText(query)
	if normalized == "" {
		return nil
	}

	specs := make([]factQuerySpec, 0, 2)
	if index := strings.LastIndex(normalized, "的"); index > 0 && index < len(normalized)-len("的") {
		subject := cleanFactSubject(normalized[:index])
		attribute := cleanFactAttribute(normalized[index+len("的"):])
		if subject != "" && attribute != "" {
			specs = append(specs, newFactQuerySpec(subject, attribute))
		}
	}

	specs = append(specs, parseDelimitedFactQuerySpecs(normalized)...)
	specs = append(specs, parseBoundaryFactQuerySpecs(normalized)...)

	for _, alias := range allFactAttributeAliases() {
		index := strings.Index(normalized, alias)
		if index <= 0 {
			continue
		}
		subject := cleanFactSubject(normalized[:index])
		if subject == "" {
			continue
		}
		specs = append(specs, newFactQuerySpec(subject, alias))
	}

	return deduplicateFactQuerySpecs(specs)
}

func parseDelimitedFactQuerySpecs(query string) []factQuerySpec {
	core := cleanFactAttribute(query)
	replacer := strings.NewReplacer(
		"　", " ",
		",", " ",
		"，", " ",
		":", " ",
		"：", " ",
		";", " ",
		"；", " ",
		"|", " ",
		"/", " ",
		"\\", " ",
	)
	parts := strings.Fields(replacer.Replace(core))
	if len(parts) < 2 {
		return nil
	}
	subject := cleanFactSubject(strings.Join(parts[:len(parts)-1], ""))
	attribute := cleanFactAttribute(parts[len(parts)-1])
	if subject == "" || attribute == "" {
		return nil
	}
	return []factQuerySpec{newFactQuerySpec(subject, attribute)}
}

func parseBoundaryFactQuerySpecs(query string) []factQuerySpec {
	core := cleanFactAttribute(query)
	if strings.ContainsAny(core, " ,，:：;；|/\\") || strings.Contains(core, "的") {
		return nil
	}
	runes := []rune(core)
	if len(runes) < 5 || len(runes) > 40 {
		return nil
	}
	for _, boundary := range factSubjectBoundaryTokens() {
		index := strings.LastIndex(core, boundary)
		if index < 0 {
			continue
		}
		subjectEnd := index + len(boundary)
		if subjectEnd <= 0 || subjectEnd >= len(core) {
			continue
		}
		subject := cleanFactSubject(core[:subjectEnd])
		attribute := cleanFactAttribute(core[subjectEnd:])
		if subject != "" && attribute != "" {
			return []factQuerySpec{newFactQuerySpec(subject, attribute)}
		}
	}
	return nil
}

func factSubjectBoundaryTokens() []string {
	return []string{
		"有限公司", "股份公司", "集团公司", "实验学校", "技术学院",
		"公司", "集团", "学校", "大学", "学院", "中学", "小学", "医院",
		"银行", "中心", "平台", "系统", "项目", "产品", "部门", "团队",
		"机构", "基地", "园区", "工厂", "门店", "网点",
	}
}

func newFactQuerySpec(subject, attribute string) factQuerySpec {
	attribute = cleanFactAttribute(attribute)
	return factQuerySpec{
		Subject:   cleanFactSubject(subject),
		Attribute: attribute,
		Aliases:   factAttributeAliases(attribute),
	}
}

func factAttributeAliases(attribute string) []string {
	attribute = strings.ToLower(strings.TrimSpace(attribute))
	if attribute == "" {
		return nil
	}
	for _, group := range factAttributeAliasGroups() {
		for _, alias := range group {
			if strings.Contains(attribute, alias) || strings.Contains(alias, attribute) {
				return mergeRetrievalQueries([]string{attribute}, group)
			}
		}
	}
	return []string{attribute}
}

func allFactAttributeAliases() []string {
	aliases := make([]string, 0)
	for _, group := range factAttributeAliasGroups() {
		aliases = append(aliases, group...)
	}
	sort.SliceStable(aliases, func(i, j int) bool {
		return len([]rune(aliases[i])) > len([]rune(aliases[j]))
	})
	return mergeRetrievalQueries(aliases)
}

func factAttributeAliasGroups() [][]string {
	return [][]string{
		{"建校时间", "成立时间", "创办时间", "创立时间", "创建时间", "建立时间", "建校", "成立于", "创办于", "始建于", "始建", "办学始于"},
		{"电话", "手机号", "手机号码", "联系电话", "联系方式", "客服电话", "热线"},
		{"地址", "注册地址", "办公地址", "联系地址", "位置", "所在地", "地点"},
		{"邮箱", "电子邮箱", "邮件", "email"},
		{"价格", "售价", "费用", "金额", "单价", "总价", "薪资", "工资", "收入"},
		{"年龄", "岁数"},
		{"编号", "工号", "教师编号", "员工编号", "学号", "身份证号", "证件号"},
		{"负责人", "联系人", "校长", "法人", "法定代表人", "负责人姓名"},
		{"时间", "日期", "年份", "年度"},
		{"数量", "人数", "规模", "总数", "个数"},
		{"职称", "职位", "岗位", "职务", "角色"},
		{"名称", "姓名", "名字"},
	}
}

func deduplicateFactQuerySpecs(specs []factQuerySpec) []factQuerySpec {
	if len(specs) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(specs))
	result := make([]factQuerySpec, 0, len(specs))
	for _, spec := range specs {
		spec.Subject = cleanFactSubject(spec.Subject)
		spec.Attribute = cleanFactAttribute(spec.Attribute)
		if spec.Subject == "" || spec.Attribute == "" {
			continue
		}
		key := strings.ToLower(spec.Subject + "\x00" + spec.Attribute)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, spec)
	}
	return result
}

func cleanFactSubject(subject string) string {
	subject = strings.TrimSpace(strings.ToLower(subject))
	for _, prefix := range []string{"请问", "查询", "告诉我", "帮我查", "我想知道", "想知道"} {
		subject = strings.TrimPrefix(subject, prefix)
	}
	subject = strings.Trim(subject, " ，,。.!！?？：:；;“”\"'`")
	subject = strings.TrimSuffix(subject, "的")
	subject = strings.TrimSuffix(subject, "是")
	subject = strings.TrimSuffix(subject, "在")
	subject = strings.TrimSpace(subject)
	if len([]rune(subject)) < 2 {
		return ""
	}
	return subject
}

func cleanFactAttribute(attribute string) string {
	attribute = strings.TrimSpace(strings.ToLower(attribute))
	for _, suffix := range []string{"是什么", "是多少", "是哪一年", "是什么时候", "是几几年", "有多少", "吗", "呢"} {
		attribute = strings.TrimSuffix(attribute, suffix)
	}
	attribute = strings.Trim(attribute, " ，,。.!！?？：:；;“”\"'`")
	attribute = strings.TrimPrefix(attribute, "是")
	attribute = strings.TrimSpace(attribute)
	if len([]rune(attribute)) < 2 {
		return ""
	}
	return attribute
}

func normalizeFactQueryText(query string) string {
	query = strings.TrimSpace(strings.ToLower(query))
	replacer := strings.NewReplacer(
		"？", "",
		"?", "",
		"：", ":",
		"；", ";",
	)
	return strings.TrimSpace(replacer.Replace(query))
}

func mergeRetrievalQueries(groups ...[]string) []string {
	merged := make([]string, 0)
	seen := make(map[string]struct{})
	for _, group := range groups {
		for _, query := range group {
			query = strings.TrimSpace(query)
			if query == "" {
				continue
			}
			key := strings.ToLower(strings.Join(strings.Fields(query), " "))
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, query)
		}
	}
	return merged
}

func continuousCJKSegments(text string) []string {
	segments := make([]string, 0)
	current := make([]rune, 0)
	flush := func() {
		if len(current) > 0 {
			segments = append(segments, string(current))
			current = current[:0]
		}
	}
	for _, r := range text {
		if unicode.In(r, unicode.Han) {
			current = append(current, r)
			continue
		}
		flush()
	}
	flush()
	return segments
}

func chunkRawScore(chunk RetrievedChunk) float64 {
	if chunk.RawScore != 0 {
		return chunk.RawScore
	}
	return chunk.Score
}

func chunkTextsFromRetrieved(chunks []RetrievedChunk) []string {
	texts := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		texts = append(texts, chunk.Text)
	}
	return texts
}

func (s *AppService) shouldUseHybridSearch(req model.ChatCompletionRequest) bool {
	if s == nil {
		return false
	}
	mode := normalizeRetrievalMode(req.RetrievalMode)
	if mode == "dense" {
		return false
	}
	if mode == "hybrid" {
		return true
	}
	retrievalConfig := s.currentRetrievalConfig()
	if !retrievalConfig.HybridSearchEnabled {
		return false
	}
	if strings.TrimSpace(req.DocumentID) != "" {
		return false
	}
	return retrievalConfig.DefaultSearchMode == "hybrid"
}

func (s *AppService) resolvedRetrievalSearchMode(req model.ChatCompletionRequest) string {
	if s != nil && s.shouldUseHybridSearch(req) {
		return "hybrid"
	}
	return "dense"
}

func normalizeRetrievalMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "dense", "vector":
		return "dense"
	case "hybrid":
		return "hybrid"
	default:
		return "auto"
	}
}

func normalizeRerankStrategy(strategy string) string {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "":
		return ""
	case "keyword", "lexical":
		return "keyword"
	case "semantic", "embedding":
		return "semantic"
	default:
		return ""
	}
}

func (s *AppService) shouldUseHybridFallback(selected []RetrievedChunk) bool {
	if s == nil {
		return false
	}
	if !s.currentRetrievalConfig().HybridSearchEnabled {
		return false
	}
	return len(selected) == 0 || selectionQuality(selected) < 0.55
}

func selectionQuality(chunks []RetrievedChunk) float64 {
	if len(chunks) == 0 {
		return math.Inf(-1)
	}
	return chunks[0].Score + 0.35*averageScore(chunks)
}

func averageScore(chunks []RetrievedChunk) float64 {
	if len(chunks) == 0 {
		return 0
	}
	sum := 0.0
	for _, chunk := range chunks {
		sum += chunk.Score
	}
	return sum / float64(len(chunks))
}

func logRetrievalMetrics(req model.ChatCompletionRequest, query string, params retrievalParams, candidates, selected []RetrievedChunk) {
	docIDs := make(map[string]struct{})
	kbIDs := make(map[string]struct{})
	for _, chunk := range selected {
		if strings.TrimSpace(chunk.DocumentID) != "" {
			docIDs[chunk.DocumentID] = struct{}{}
		}
		if strings.TrimSpace(chunk.KnowledgeBaseID) != "" {
			kbIDs[chunk.KnowledgeBaseID] = struct{}{}
		}
	}
	topScore := 0.0
	if len(selected) > 0 {
		topScore = selected[0].Score
	}

	log.Printf(
		"retrieval_metrics query=%q scope_kb=%q scope_doc=%q candidate_topk=%d final_topk=%d per_doc_limit=%d candidates=%d selected=%d docs=%d knowledge_bases=%d top_score=%.4f avg_score=%.4f low_confidence=%t",
		strings.TrimSpace(query),
		strings.TrimSpace(req.KnowledgeBaseID),
		strings.TrimSpace(req.DocumentID),
		params.candidateTopK,
		params.finalTopK,
		params.perDocumentLimit,
		len(candidates),
		len(selected),
		len(docIDs),
		len(kbIDs),
		topScore,
		averageScore(selected),
		isLowConfidenceSelection(query, selected),
	)
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
