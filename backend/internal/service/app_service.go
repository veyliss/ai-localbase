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

// SemanticReranker 语义重排器接口
// Rerank 对候选 chunks 按与 query 的语义相关度重新排序
// 返回排序后的 chunks（score 已更新）
type SemanticReranker interface {
	Rerank(ctx context.Context, query string, chunks []RetrievedChunk) ([]RetrievedChunk, error)
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

// EmbeddingReranker 基于 embedding cosine similarity 的重排器
// 这是一个轻量级实现：计算 query embedding 与每个 chunk embedding 的 cosine similarity
// 不依赖外部模型服务，直接复用现有 EmbedTexts 能力
type EmbeddingReranker struct {
	ragSvc          *RagService
	embeddingConfig func() model.EmbeddingModelConfig
	vectorSize      func() int
	embed           func(ctx context.Context, cfg model.EmbeddingModelConfig, texts []string, vectorSize int) ([][]float64, error)
}

// NewEmbeddingReranker 创建基于 embedding 的重排器
func NewEmbeddingReranker(ragSvc *RagService) *EmbeddingReranker {
	return &EmbeddingReranker{ragSvc: ragSvc}
}

// SetEmbeddingConfigProvider 注入 embedding 配置提供函数
func (r *EmbeddingReranker) SetEmbeddingConfigProvider(provider func() model.EmbeddingModelConfig) {
	r.embeddingConfig = provider
}

// SetVectorSizeProvider 注入向量维度提供函数
func (r *EmbeddingReranker) SetVectorSizeProvider(provider func() int) {
	r.vectorSize = provider
}

// Rerank 使用 embedding cosine similarity 重排
func (r *EmbeddingReranker) Rerank(ctx context.Context, query string, chunks []RetrievedChunk) ([]RetrievedChunk, error) {
	if len(chunks) == 0 {
		return nil, nil
	}
	if r == nil {
		return nil, fmt.Errorf("embedding reranker is nil")
	}

	cfg := model.EmbeddingModelConfig{}
	if r.embeddingConfig != nil {
		cfg = r.embeddingConfig()
	}
	vectorSize := 0
	if r.vectorSize != nil {
		vectorSize = r.vectorSize()
	}

	embed := r.embed
	if embed == nil {
		if r.ragSvc == nil {
			return nil, fmt.Errorf("rag service is nil")
		}
		embed = r.ragSvc.EmbedTexts
	}

	queryVectors, err := embed(ctx, cfg, []string{query}, vectorSize)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(queryVectors) == 0 {
		return nil, fmt.Errorf("empty query embedding")
	}

	texts := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		texts = append(texts, chunk.Text)
	}
	chunkVectors, err := embed(ctx, cfg, texts, vectorSize)
	if err != nil {
		return nil, fmt.Errorf("embed chunks: %w", err)
	}
	if len(chunkVectors) != len(chunks) {
		return nil, fmt.Errorf("embedding size mismatch: %d != %d", len(chunkVectors), len(chunks))
	}

	queryVec := float64ToFloat32(queryVectors[0])
	ranked := make([]RetrievedChunk, len(chunks))
	copy(ranked, chunks)
	for i := range ranked {
		chunkVec := float64ToFloat32(chunkVectors[i])
		similarity := cosineSimilarity(queryVec, chunkVec)
		ranked[i].Score = float64(similarity)
	}

	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Score == ranked[j].Score {
			if ranked[i].DocumentID == ranked[j].DocumentID {
				return ranked[i].Index < ranked[j].Index
			}
			return ranked[i].DocumentID < ranked[j].DocumentID
		}
		return ranked[i].Score > ranked[j].Score
	})
	return ranked, nil
}

// LLMReranker 基于 LLM 打分的重排器
// 对每个 (query, chunk) 对，让 LLM 打 0-10 的相关度分数
// 精度更高但延迟更大，适合 topK 较小的场景（≤5个候选）
type LLMReranker struct {
	llmSvc     *LLMService
	chatConfig func() model.ChatModelConfig
}

// SetChatConfigProvider 注入 Chat 配置提供函数
func (r *LLMReranker) SetChatConfigProvider(provider func() model.ChatModelConfig) {
	r.chatConfig = provider
}

// Rerank 使用 LLM 对每个候选打分
func (r *LLMReranker) Rerank(ctx context.Context, query string, chunks []RetrievedChunk) ([]RetrievedChunk, error) {
	if len(chunks) == 0 {
		return nil, nil
	}
	if r == nil || r.llmSvc == nil {
		return nil, fmt.Errorf("llm service is nil")
	}

	config := model.ChatModelConfig{}
	if r.chatConfig != nil {
		config = r.chatConfig()
	}
	if strings.TrimSpace(config.Model) == "" {
		return nil, fmt.Errorf("chat model config is empty")
	}

	scores := make([]float64, len(chunks))
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	errChan := make(chan error, len(chunks))

	for i, chunk := range chunks {
		wg.Add(1)
		go func(idx int, text string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				errChan <- ctx.Err()
				return
			}

			prompt := fmt.Sprintf("请评估以下文档与问题的相关度，返回0-10的整数分数。\n问题：%s\n文档：%s\n分数：", query, text)
			resp, err := r.llmSvc.Chat(model.ChatCompletionRequest{
				Messages: []model.ChatMessage{{Role: "user", Content: prompt}},
				Config:   config,
			})
			if err != nil {
				errChan <- err
				return
			}
			if len(resp.Choices) == 0 {
				errChan <- fmt.Errorf("empty llm response")
				return
			}
			score, err := parseLLMScore(resp.Choices[0].Message.Content)
			if err != nil {
				errChan <- err
				return
			}
			scores[idx] = score
		}(i, chunk.Text)
	}

	wg.Wait()
	close(errChan)
	for err := range errChan {
		if err != nil {
			return nil, err
		}
	}

	ranked := make([]RetrievedChunk, len(chunks))
	copy(ranked, chunks)
	for i := range ranked {
		ranked[i].Score = scores[i]
	}

	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Score == ranked[j].Score {
			if ranked[i].DocumentID == ranked[j].DocumentID {
				return ranked[i].Index < ranked[j].Index
			}
			return ranked[i].DocumentID < ranked[j].DocumentID
		}
		return ranked[i].Score > ranked[j].Score
	})
	return ranked, nil
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

	if serverConfig.EnableSemanticReranker {
		service.reranker = NewEmbeddingReranker(service.rag)
		if embeddingReranker, ok := service.reranker.(*EmbeddingReranker); ok {
			embeddingReranker.SetEmbeddingConfigProvider(service.currentEmbeddingConfig)
			embeddingReranker.SetVectorSizeProvider(service.qdrantVectorSize)
		}
	}

	if serverConfig.EnableSemanticCache {
		service.semanticCache = NewSemanticCache(0, 0, 0)
	}

	if serverConfig.EnableQueryRewrite || serverConfig.EnableContextCompression {
		llmService := NewLLMService()
		if serverConfig.EnableQueryRewrite {
			service.SetQueryRewriter(NewLLMQueryRewriter(llmService, 3))
		}
		if serverConfig.EnableContextCompression {
			service.SetContextCompressor(NewLLMContextCompressor(llmService, 800))
		}
	}

	if store != nil {
		if loadedState, err := store.Load(); err != nil {
			log.Printf("failed to load app state: %v", err)
		} else if loadedState != nil {
			service.state = &model.AppState{
				Config:         loadedState.Config,
				KnowledgeBases: loadedState.KnowledgeBases,
			}
			if service.state.KnowledgeBases == nil {
				service.state.KnowledgeBases = map[string]model.KnowledgeBase{}
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
	remaining := len(s.state.KnowledgeBases)
	s.state.Mu.Unlock()

	if err := s.saveState(); err != nil {
		s.state.Mu.Lock()
		s.state.KnowledgeBases[id] = removedKnowledgeBase
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
	return s.AddDocument(document.KnowledgeBaseID, document), nil
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
	return removedDocument, nil
}

func (s *AppService) BuildRetrievalContext(req model.ChatCompletionRequest) (string, []map[string]string, error) {
	startedAt := time.Now()
	chunks, err := s.EvaluateRetrieve(req)
	if err != nil {
		return "", nil, err
	}

	query := latestUserMessage(req.Messages)
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
	if s.queryRewriter == nil {
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
			Vector: vector,
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
	params := resolveRetrievalParamsWithConfig(req, s.serverConfig)
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

	if s.queryRewriter != nil {
		if setter, ok := s.queryRewriter.(interface {
			SetChatConfigProvider(func() model.ChatModelConfig)
		}); ok {
			setter.SetChatConfigProvider(func() model.ChatModelConfig {
				return s.resolveChatConfig(req)
			})
		}
		history := recentConversationHistory(req.Messages, 3)
		rewriteResult, err := s.queryRewriter.Rewrite(ctx, query, history)
		if err != nil {
			return nil, err
		}
		queries := rewriteResult.RewrittenQueries
		if len(queries) == 0 {
			queries = []string{query}
		}
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
			s.semanticCache.Set(queryEmbedding, query, selected)
		}
		logRetrievalMetrics(req, query, params, candidates, selected)
		return selected, nil
	}

	useHybrid := s.shouldUseHybridSearch(req)
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

	candidates, err := s.collectCandidates(knowledgeBaseIDs, req, queryVector, params.candidateTopK, useHybrid, query)
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
		expandedCandidates, err := s.collectCandidates(knowledgeBaseIDs, req, queryVector, expandedCandidateTopK, expandUseHybrid, query)
		if err == nil {
			expandedParams := params
			expandedParams.perDocumentLimit++
			expandedSelected := s.applySelectionStrategy(req, query, ctx, expandedCandidates, expandedParams)
			if selectionQuality(expandedSelected) > selectionQuality(selected) {
				selected = expandedSelected
			}
		}
	}

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

func (s *AppService) retrievalMaxContextChars() int {
	if s == nil || s.serverConfig.RetrievalMaxContextChars <= 0 {
		return 2400
	}
	return s.serverConfig.RetrievalMaxContextChars
}

func (s *AppService) retrievalAutoExpandEnabled() bool {
	if s == nil {
		return false
	}
	return s.serverConfig.RetrievalEnableAutoExpand
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
		for _, item := range items {
			chunkID := payloadString(item.Payload, "chunk_id", item.ID)
			if _, exists := seenChunkIDs[chunkID]; exists {
				continue
			}
			text := payloadString(item.Payload, "text", "")
			if strings.TrimSpace(text) == "" {
				continue
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
				Score: item.Score,
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

func (s *AppService) rerankCandidates(ctx context.Context, candidates []RetrievedChunk, query string) []RetrievedChunk {
	if len(candidates) == 0 {
		return nil
	}

	if s != nil && s.serverConfig.EnableSemanticReranker && s.reranker != nil {
		ranked, err := s.reranker.Rerank(ctx, query, candidates)
		if err == nil && len(ranked) > 0 {
			return ranked
		}
	}

	ranked := make([]RetrievedChunk, len(candidates))
	copy(ranked, candidates)

	minScore, maxScore := ranked[0].Score, ranked[0].Score
	for _, item := range ranked {
		if item.Score < minScore {
			minScore = item.Score
		}
		if item.Score > maxScore {
			maxScore = item.Score
		}
	}

	for i := range ranked {
		vectorScore := normalizeScore(ranked[i].Score, minScore, maxScore)
		keywordScore := keywordCoverage(query, ranked[i].Text)
		ranked[i].Score = rerankVectorWeight*vectorScore + rerankKeywordWeight*keywordScore + scoreBoost(ranked[i].Text)
	}

	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Score == ranked[j].Score {
			if ranked[i].DocumentID == ranked[j].DocumentID {
				return ranked[i].Index < ranked[j].Index
			}
			return ranked[i].DocumentID < ranked[j].DocumentID
		}
		return ranked[i].Score > ranked[j].Score
	})
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
		selected = s.rerankCandidates(ctx, candidates, query)
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
	queryTerms := splitTerms(query)
	if len(queryTerms) == 0 {
		return 0
	}
	textTerms := splitTerms(text)
	if len(textTerms) == 0 {
		return 0
	}

	textSet := make(map[string]struct{}, len(textTerms))
	for _, term := range textTerms {
		textSet[term] = struct{}{}
	}

	hit := 0
	for _, term := range queryTerms {
		if _, ok := textSet[term]; ok {
			hit++
		}
	}
	return float64(hit) / float64(len(queryTerms))
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
	return entityCoverage(query, chunks) < 0.2
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
	entities := splitTerms(query)
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

func chunkTextsFromRetrieved(chunks []RetrievedChunk) []string {
	texts := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		texts = append(texts, chunk.Text)
	}
	return texts
}

func cosineSimilarity(a, b []float32) float32 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(normA) * math.Sqrt(normB)))
}

func parseLLMScore(content string) (float64, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return 0, fmt.Errorf("empty llm score")
	}

	num := strings.Builder{}
	for _, r := range content {
		if (r >= '0' && r <= '9') || r == '.' {
			num.WriteRune(r)
			continue
		}
		if num.Len() == 0 {
			continue
		}
		break
	}
	if num.Len() == 0 {
		return 0, fmt.Errorf("no numeric score in llm response")
	}
	score, err := strconv.ParseFloat(num.String(), 64)
	if err != nil {
		return 0, fmt.Errorf("parse llm score: %w", err)
	}
	if score < 0 {
		score = 0
	}
	if score > 10 {
		score = 10
	}
	return score, nil
}

func (s *AppService) shouldUseHybridSearch(req model.ChatCompletionRequest) bool {
	if s == nil {
		return false
	}
	return false
}

func (s *AppService) shouldUseHybridFallback(selected []RetrievedChunk) bool {
	if s == nil {
		return false
	}
	if !s.serverConfig.EnableHybridSearch {
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
