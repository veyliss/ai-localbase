package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"ai-localbase/internal/model"
)

// RetrievalOrchestrator is the stable application boundary for retrieval.
// The first extraction keeps the existing strategy implementation intact while
// making chat, evaluation, debug and MCP callers share one entry point.
type RetrievalOrchestrator struct {
	appService *AppService
}

func NewRetrievalOrchestrator(appService *AppService) *RetrievalOrchestrator {
	return &RetrievalOrchestrator{appService: appService}
}

func (s *AppService) retrievalBoundary() *RetrievalOrchestrator {
	if s == nil {
		return NewRetrievalOrchestrator(nil)
	}
	if s.retrievalOrchestrator == nil {
		return NewRetrievalOrchestrator(s)
	}
	return s.retrievalOrchestrator
}

func (o *RetrievalOrchestrator) BuildContext(ctx context.Context, req model.ChatCompletionRequest) (string, []map[string]string, error) {
	if o == nil || o.appService == nil {
		return "", nil, fmt.Errorf("retrieval orchestrator is unavailable")
	}

	service := o.appService
	ctx = normalizeServiceContext(ctx)
	chunks, err := o.Evaluate(ctx, req)
	if err != nil {
		return "", nil, err
	}
	knowledgeBaseIDs, err := service.resolveRetrievalKnowledgeBaseIDs(req)
	if err != nil {
		return "", nil, err
	}
	chunks = service.filterRetrievedChunksToScope(req, knowledgeBaseIDs, chunks)

	query := latestUserMessage(req.Messages)
	return service.buildRetrievedContext(ctx, req, query, chunks)
}

func (s *AppService) buildRetrievedContext(ctx context.Context, req model.ChatCompletionRequest, query string, chunks []RetrievedChunk) (string, []map[string]string, error) {
	startedAt := time.Now()
	dedupStartedAt := time.Now()
	chunks = deduplicateRetrievedChunks(chunks)
	logRetrievalStageMetrics(req, query, "context_deduplicate", dedupStartedAt, map[string]any{
		"status":           "ok",
		"remaining_chunks": len(chunks),
	})

	maxContextChars := s.retrievalMaxContextChars()
	compressedContext := ""
	if s.contextCompressor != nil && maxContextChars > 0 && chunksTotalChars(chunks) > maxContextChars {
		compressStartedAt := time.Now()
		compressed, compressErr := s.contextCompressor.Compress(ctx, query, chunks)
		if compressErr == nil && strings.TrimSpace(compressed) != "" {
			compressedContext = strings.TrimSpace(compressed)
			logRetrievalStageMetrics(req, query, "context_compress", compressStartedAt, map[string]any{
				"status":           "ok",
				"compressed_chars": len(compressedContext),
			})
		} else {
			logRetrievalStageMetrics(req, query, "context_compress", compressStartedAt, map[string]any{
				"status": "error",
				"error":  fmt.Sprint(compressErr),
			})
		}
	}

	trimStartedAt := time.Now()
	if compressedContext == "" && maxContextChars > 0 {
		chunks = trimRetrievedChunksToContextLimit(chunks, maxContextChars, query)
	}
	logRetrievalStageMetrics(req, query, "context_trim", trimStartedAt, map[string]any{
		"status":            ternaryString(compressedContext != "", "skipped_after_compression", "ok"),
		"remaining_chunks":  len(chunks),
		"max_context_chars": maxContextChars,
		"context_chars":     chunksTotalChars(chunks),
	})

	buildStartedAt := time.Now()
	contextText, sources := s.rag.BuildContext(chunks)
	if compressedContext != "" {
		contextText = compressedContext
	}
	logRetrievalStageMetrics(req, query, "context_build", buildStartedAt, map[string]any{
		"status":        "ok",
		"sources":       len(sources),
		"context_chars": len(contextText),
	})
	logRetrievalStageMetrics(req, query, "build_retrieval_context_total", startedAt, map[string]any{
		"status":        "ok",
		"sources":       len(sources),
		"context_chars": len(contextText),
	})
	return contextText, sources, nil
}

func (o *RetrievalOrchestrator) Evaluate(ctx context.Context, req model.ChatCompletionRequest) ([]RetrievedChunk, error) {
	chunks, _, err := o.evaluateWithEvidenceGate(ctx, req)
	return chunks, err
}

func (o *RetrievalOrchestrator) evaluateWithEvidenceGate(ctx context.Context, req model.ChatCompletionRequest) ([]RetrievedChunk, evidenceGateStats, error) {
	chunks, err := o.evaluateRaw(ctx, req)
	if err != nil {
		return nil, evidenceGateStats{}, err
	}

	query := latestUserMessage(req.Messages)
	gateStartedAt := time.Now()
	filtered, stats := applyEvidenceGateWithStats(query, chunks)
	logRetrievalStageMetrics(req, query, "evidence_gate", gateStartedAt, map[string]any{
		"status":         "ok",
		"input_count":    stats.InputCount,
		"output_count":   stats.OutputCount,
		"dropped_chunks": stats.DroppedCount,
	})
	return filtered, stats, nil
}

func (o *RetrievalOrchestrator) evaluateRaw(ctx context.Context, req model.ChatCompletionRequest) ([]RetrievedChunk, error) {
	if o == nil || o.appService == nil {
		return nil, fmt.Errorf("retrieval orchestrator is unavailable")
	}

	service := o.appService
	ctx = normalizeServiceContext(ctx)
	if service == nil {
		return nil, fmt.Errorf("app service is nil")
	}
	startedAt := time.Now()
	query := latestUserMessage(req.Messages)
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}

	structuredStartedAt := time.Now()
	if chunks, ok, err := service.retrieveStructuredDataChunks(req); err != nil {
		logRetrievalStageMetrics(req, query, "structured_retrieve", structuredStartedAt, map[string]any{
			"status":   "error",
			"error":    err.Error(),
			"fallback": true,
		})
	} else if ok {
		logRetrievalStageMetrics(req, query, "structured_retrieve", structuredStartedAt, map[string]any{
			"status":          "ok",
			"selected_chunks": len(chunks),
		})
		return chunks, nil
	}

	if service.qdrant == nil || !service.qdrant.IsEnabled() {
		logRetrievalStageMetrics(req, query, "evaluate_retrieve_total", startedAt, map[string]any{
			"status":          "skipped",
			"selected_chunks": 0,
			"reason":          "qdrant_disabled",
		})
		return nil, nil
	}

	var queryVector []float64
	embeddingStartedAt := time.Now()
	if !service.queryRewriteEnabledForRequest(req) {
		embedCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
		vectors, err := service.rag.EmbedTexts(embedCtx, service.resolveEmbeddingConfig(req), []string{query}, service.qdrantVectorSize())
		if err == nil && len(vectors) == 0 {
			err = fmt.Errorf("embedding api returned no vectors")
		}
		if err != nil {
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

	chunks, err := service.retrieveRelevantChunksWithContext(ctx, req, queryVector)
	logRetrievalStageMetrics(req, query, "evaluate_retrieve_total", startedAt, map[string]any{
		"status":          retrievalStatus(err),
		"selected_chunks": len(chunks),
	})
	if err != nil {
		return nil, err
	}
	return chunks, nil
}

func (o *RetrievalOrchestrator) Debug(ctx context.Context, req model.RetrievalDebugRequest) (model.RetrievalDebugResponse, error) {
	if o == nil || o.appService == nil {
		return model.RetrievalDebugResponse{}, fmt.Errorf("retrieval orchestrator is unavailable")
	}

	service := o.appService
	ctx = normalizeServiceContext(ctx)
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
		Config:                  service.currentChatConfig(),
		Embedding:               service.currentEmbeddingConfig(),
		Messages: []model.ChatMessage{{
			Role:    "user",
			Content: query,
		}},
	}

	var verboseDetails *model.RetrievalDebugVerboseDetails
	var chunks []RetrievedChunk
	var queryVariants []string
	structuredUsed := false
	var structuredErr error
	var err error

	if structuredChunks, ok, retrieveErr := service.retrieveStructuredDataChunks(chatReq); retrieveErr != nil {
		structuredErr = retrieveErr
	} else if ok {
		chunks = structuredChunks
		structuredUsed = true
		if req.Verbose {
			verboseDetails = &model.RetrievalDebugVerboseDetails{
				CandidatesCount:  len(chunks),
				AfterRerankCount: len(chunks),
				AfterMMRCount:    len(chunks),
			}
		}
	} else if req.Verbose {
		chunks, verboseDetails, queryVariants, err = service.debugRetrieveVerboseWithContext(ctx, chatReq, query)
	} else {
		chunks, err = o.evaluateRaw(ctx, chatReq)
	}
	if err != nil {
		return model.RetrievalDebugResponse{}, err
	}

	chunks, gateStats := applyEvidenceGateWithStats(query, chunks)
	if verboseDetails != nil {
		verboseDetails.AfterEvidenceGateCount = gateStats.OutputCount
		verboseDetails.TopAfterEvidenceGate = convertToDebugChunks(chunks, query, 5)
	}

	trace := make([]model.RetrievalDebugTraceStep, 0, 6)
	if structuredUsed {
		trace = append(trace, model.RetrievalDebugTraceStep{
			Stage:       "structured_retrieve",
			Status:      "ok",
			Reason:      fmt.Sprintf("结构化确定性查询直接返回 %d 个证据片段，无需向量召回", gateStats.InputCount),
			OutputCount: gateStats.InputCount,
		})
	} else if structuredErr != nil {
		trace = append(trace, model.RetrievalDebugTraceStep{
			Stage:  "structured_retrieve",
			Status: "error",
			Reason: "结构化解析失败，已回落常规检索",
		})
	}
	trace = append(trace, model.RetrievalDebugTraceStep{
		Stage:       "retrieve",
		Status:      "ok",
		Reason:      "基础检索、重排、MMR 和相关性过滤后的候选",
		OutputCount: gateStats.InputCount,
	})
	gateReason := "所有候选均通过证据门控"
	if gateStats.DroppedCount > 0 {
		gateReason = fmt.Sprintf("证据门控移除了 %d 个低信号片段", gateStats.DroppedCount)
	}
	trace = append(trace, model.RetrievalDebugTraceStep{
		Stage:       "evidence_gate",
		Status:      "ok",
		Reason:      gateReason,
		InputCount:  gateStats.InputCount,
		OutputCount: gateStats.OutputCount,
	})
	if structuredUsed {
		trace = append(trace, model.RetrievalDebugTraceStep{
			Stage:  "rerank",
			Status: "skipped",
			Reason: "确定性结构化结果无需重排",
		})
	} else {
		trace = append(trace, model.RetrievalDebugTraceStep{
			Stage:  "rerank",
			Status: "ok",
			Reason: fmt.Sprintf("当前重排策略：%s", service.rerankStrategyForRequest(chatReq)),
		})
	}
	if structuredUsed {
		trace = append(trace, model.RetrievalDebugTraceStep{
			Stage:  "query_rewrite",
			Status: "skipped",
			Reason: "确定性结构化查询已直接处理原始问题",
		})
	} else if service.queryRewriteEnabledForRequest(chatReq) {
		trace = append(trace, model.RetrievalDebugTraceStep{
			Stage:  "query_rewrite",
			Status: "ok",
			Reason: fmt.Sprintf("已启用查询改写，最多生成 %d 个查询变体", service.queryRewriteMaxVariantsForRequest(chatReq)),
		})
	} else {
		trace = append(trace, model.RetrievalDebugTraceStep{
			Stage:  "query_rewrite",
			Status: "skipped",
			Reason: "未启用查询改写",
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
	confidence := buildRetrievalDebugConfidence(query, chunks)
	retrievalLowConfidence := confidence.Status == "low"
	contextText, sources := service.rag.BuildContext(chunks)
	contextText = truncateRunes(strings.TrimSpace(contextText), retrievalDebugContextLimit)
	evalCandidate := buildRetrievalDebugEvalCandidate(chatReq, query, retrievalLowConfidence, chunks, contextText)

	items := make([]model.RetrievalDebugChunk, 0, len(chunks))
	for _, chunk := range chunks {
		items = append(items, buildRetrievalDebugChunk(query, chunk))
	}

	return model.RetrievalDebugResponse{
		Query:                    query,
		KnowledgeBaseID:          chatReq.KnowledgeBaseID,
		DocumentID:               chatReq.DocumentID,
		SearchMode:               service.resolvedRetrievalSearchMode(chatReq),
		RerankStrategy:           service.rerankStrategyForRequest(chatReq),
		QueryRewriteUsed:         service.queryRewriteEnabledForRequest(chatReq),
		QueryVariants:            queryVariants,
		ElapsedMs:                time.Since(startedAt).Milliseconds(),
		Count:                    len(items),
		LowConfidence:            retrievalLowConfidence,
		Confidence:               confidence,
		EvidenceGateInputCount:   gateStats.InputCount,
		EvidenceGateOutputCount:  gateStats.OutputCount,
		EvidenceGateDroppedCount: gateStats.DroppedCount,
		ContextPreview:           contextText,
		Sources:                  sources,
		EvalCandidate:            evalCandidate,
		Trace:                    trace,
		Items:                    items,
		VerboseDetails:           verboseDetails,
	}, nil
}
