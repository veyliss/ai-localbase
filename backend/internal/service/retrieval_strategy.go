package service

import (
	"context"
	"fmt"
	"log"
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"ai-localbase/internal/model"
)

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

func trimRetrievedChunksToContextLimit(chunks []RetrievedChunk, maxChars int, query string) []RetrievedChunk {
	if len(chunks) == 0 || maxChars <= 0 {
		return chunks
	}

	chunkLimit := 3
	if isListDetailQuery(query) {
		chunkLimit = 4
	}
	chunkLimit = minInt(len(chunks), chunkLimit)
	trimmed := make([]RetrievedChunk, 0, chunkLimit)
	remaining := maxChars
	for index, chunk := range chunks[:chunkLimit] {
		text := strings.TrimSpace(chunk.Text)
		if text == "" {
			continue
		}
		remainingChunks := chunkLimit - index
		if remaining <= 0 || remainingChunks <= 0 {
			break
		}

		budget := remaining / remainingChunks
		next := chunk
		next.Text = relevantChunkExcerpt(query, text, budget)
		trimmed = append(trimmed, next)
		remaining -= len([]rune(next.Text))
	}

	return trimmed
}

func relevantChunkExcerpt(query, text string, limit int) string {
	runes := []rune(strings.TrimSpace(text))
	if limit <= 0 || len(runes) <= limit {
		return string(runes)
	}

	terms := queryEvidenceTerms(query)
	sort.SliceStable(terms, func(i, j int) bool {
		return len([]rune(terms[i])) > len([]rune(terms[j]))
	})
	lowered := strings.ToLower(string(runes))
	matchRuneIndex := -1
	for _, term := range terms {
		byteIndex := strings.Index(lowered, strings.ToLower(term))
		if byteIndex < 0 {
			continue
		}
		matchRuneIndex = len([]rune(lowered[:byteIndex]))
		break
	}

	start := 0
	if matchRuneIndex > 0 {
		start = matchRuneIndex - limit/4
		if start < 0 {
			start = 0
		}
		if start+limit > len(runes) {
			start = len(runes) - limit
		}
	}
	end := minInt(start+limit, len(runes))
	return string(runes[start:end])
}

func (s *AppService) collectCandidates(ctx context.Context, knowledgeBaseIDs []string, req model.ChatCompletionRequest, queryVector []float64, candidateTopK int, useHybrid bool, query string) ([]RetrievedChunk, error) {
	ctx = normalizeServiceContext(ctx)
	startedAt := time.Now()
	results := make([]RetrievedChunk, 0)
	seenChunkIDs := make(map[string]struct{})

	// 尝试从查询中提取文件名并找到对应文档
	var filenameDetectedDocID string
	if strings.TrimSpace(req.DocumentID) == "" {
		filenames := extractFilenamesFromQuery(query)
		if len(filenames) > 0 && len(knowledgeBaseIDs) > 0 {
			// 使用第一个知识库和第一个文件名来查找
			filenameDetectedDocID = s.findDocumentByFilename(knowledgeBaseIDs[0], filenames[0])
			if filenameDetectedDocID != "" {
				log.Printf("[filename-detection] query=%q detected_filename=%q matched_doc_id=%q", query, filenames[0], filenameDetectedDocID)
			} else {
				log.Printf("[filename-detection] query=%q detected_filename=%q matched_doc_id=<none>", query, filenames[0])
			}
		}
	}

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
		} else if filenameDetectedDocID != "" {
			// 如果从查询中检测到文件名，限制检索范围到该文档
			filter = map[string]any{
				"must": []map[string]any{{
					"key":   "document_id",
					"match": map[string]any{"value": filenameDetectedDocID},
				}},
			}
		}

		var items []SearchResult
		if useHybrid {
			log.Printf("hybrid search enabled for knowledge base %s", knowledgeBaseID)
			sparseVector := BuildSparseVector(query)
			searchResults, err := s.rag.SearchHybrid(ctx, s.qdrant, knowledgeBaseID, queryVector, sparseVector, candidateTopK, filter)
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
			searchResults, err := s.qdrant.Search(ctx, knowledgeBaseID, queryVector, candidateTopK, filter)
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
					EvidenceID:      payloadString(item.Payload, "evidence_id", ""),
					KnowledgeBaseID: payloadString(item.Payload, "knowledge_base_id", knowledgeBaseID),
					DocumentID:      payloadString(item.Payload, "document_id", ""),
					DocumentName:    payloadString(item.Payload, "document_name", "未知文档"),
					Text:            text,
					Index:           payloadInt(item.Payload, "chunk_index"),
					Kind:            payloadString(item.Payload, "chunk_kind", "text"),
					CharStart:       payloadInt(item.Payload, "char_start"),
					CharEnd:         payloadInt(item.Payload, "char_end"),
					LineStart:       payloadInt(item.Payload, "line_start"),
					LineEnd:         payloadInt(item.Payload, "line_end"),
					TableRow:        payloadInt(item.Payload, "table_row"),
					TableColumns:    payloadStringSlice(item.Payload, "table_columns"),
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
			"status":         "ok",
			"knowledge_base": knowledgeBaseID,
			"search_mode":    ternaryString(useHybrid, "hybrid", "dense"),
			"candidate_topk": candidateTopK,
			"raw_hits":       len(items),
			"added_hits":     added,
		})
	}
	logRetrievalStageMetrics(req, query, "collect_candidates_total", startedAt, map[string]any{
		"status":            "ok",
		"knowledge_bases":   len(knowledgeBaseIDs),
		"candidate_topk":    candidateTopK,
		"unique_candidates": len(results),
		"search_mode":       ternaryString(useHybrid, "hybrid", "dense"),
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

		candidates, err := s.collectCandidates(ctx, knowledgeBaseIDs, req, vector, candidateTopK, useHybrid, searchQuery)
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
	sortRetrievedChunks(results)
	logRetrievalStageMetrics(req, originalQuery, "collect_query_variants", time.Now(), map[string]any{
		"status":            "ok",
		"query_variants":    len(searchQueries),
		"unique_candidates": len(results),
	})
	return results, nil
}

func retrievedChunkKey(item RetrievedChunk) string {
	if strings.TrimSpace(item.ID) != "" {
		return item.DocumentID + "#" + item.ID
	}
	return item.DocumentID + "#" + strconv.Itoa(item.Index) + "#" + strings.TrimSpace(item.Text)
}

// extractFilenamesFromQuery 从查询中提取文件名
// 支持格式：《文件名》、"文件名"、'文件名'、以及带扩展名的裸文件名
func extractFilenamesFromQuery(query string) []string {
	filenames := make([]string, 0)

	// 提取《》包裹的文件名
	re1 := regexp.MustCompile(`《([^》]+\.(xlsx|xls|csv|pdf|txt|md))》`)
	matches := re1.FindAllStringSubmatch(query, -1)
	for _, match := range matches {
		if len(match) > 1 {
			filenames = append(filenames, match[1])
		}
	}

	// 提取引号包裹的文件名
	re2 := regexp.MustCompile(`["']([^"']+\.(xlsx|xls|csv|pdf|txt|md))["']`)
	matches = re2.FindAllStringSubmatch(query, -1)
	for _, match := range matches {
		if len(match) > 1 && !containsString(filenames, match[1]) {
			filenames = append(filenames, match[1])
		}
	}

	// 如果没有找到任何包裹的文件名，才提取裸文件名（带扩展名的）
	// 这避免了误匹配中文文件名中的数字部分（如"工作簿1.xlsx"中的"1.xlsx"）
	if len(filenames) == 0 {
		re3 := regexp.MustCompile(`\b([a-zA-Z0-9_\-\.]+\.(xlsx|xls|csv|pdf|txt|md))\b`)
		matches = re3.FindAllStringSubmatch(query, -1)
		for _, match := range matches {
			if len(match) > 1 && !containsString(filenames, match[1]) {
				filenames = append(filenames, match[1])
			}
		}
	}

	return filenames
}

// findDocumentByFilename 根据文件名查找文档 ID
// 支持多种匹配策略：精确匹配、部分匹配、扩展名匹配
func (s *AppService) findDocumentByFilename(knowledgeBaseID string, filename string) string {
	if s.state == nil || strings.TrimSpace(filename) == "" {
		return ""
	}

	kb, exists := s.state.KnowledgeBases[knowledgeBaseID]
	if !exists {
		return ""
	}

	cleanFilename := strings.TrimSpace(filename)

	// 先尝试精确匹配
	for _, doc := range kb.Documents {
		if doc.Name == cleanFilename {
			return doc.ID
		}
	}

	// 再尝试部分匹配（文档名包含查询中的文件名，或反之）
	for _, doc := range kb.Documents {
		if strings.Contains(doc.Name, cleanFilename) || strings.Contains(cleanFilename, doc.Name) {
			return doc.ID
		}
	}

	if !shouldAllowFilenameExtensionFallback(cleanFilename) {
		return ""
	}

	// 最后尝试扩展名匹配：如果知识库中只有一个该扩展名的文档，返回它。
	// 只允许明显的临时上传文件名走该兜底，避免 users.csv 误命中唯一的其它 csv 文档。
	ext := filepath.Ext(cleanFilename)
	if ext != "" {
		var matchedDocs []model.Document
		for _, doc := range kb.Documents {
			if filepath.Ext(doc.Name) == ext {
				matchedDocs = append(matchedDocs, doc)
			}
		}
		// 只有当该扩展名唯一时才返回，避免歧义
		if len(matchedDocs) == 1 {
			return matchedDocs[0].ID
		}
	}

	return ""
}

func shouldAllowFilenameExtensionFallback(filename string) bool {
	base := strings.TrimSuffix(filepath.Base(strings.TrimSpace(filename)), filepath.Ext(filename))
	if strings.Contains(base, "____") {
		return true
	}
	digits := 0
	for _, r := range base {
		if r >= '0' && r <= '9' {
			digits++
		}
	}
	return digits >= 12 && digits*2 >= len([]rune(base))
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

const (
	retrievalEvidenceCoverageThreshold  = 0.16
	retrievalSemanticOnlyScoreThreshold = 0.78
	retrievalSemanticScoreMargin        = 0.08
)

type evidenceGateStats struct {
	InputCount   int
	OutputCount  int
	DroppedCount int
}

// applyEvidenceGate prevents low-signal vector matches from entering the
// answer context. Structured results already come from deterministic table
// queries and therefore bypass this gate.
func applyEvidenceGate(query string, chunks []RetrievedChunk) []RetrievedChunk {
	filtered, _ := applyEvidenceGateWithStats(query, chunks)
	return filtered
}

func applyEvidenceGateWithStats(query string, chunks []RetrievedChunk) ([]RetrievedChunk, evidenceGateStats) {
	stats := evidenceGateStats{InputCount: len(chunks), OutputCount: len(chunks)}
	if len(chunks) == 0 || strings.TrimSpace(query) == "" {
		return chunks, stats
	}

	queryTerms := queryEvidenceTerms(query)
	factSpecs := strictFactQuerySpecs(query)
	if len(queryTerms) == 0 && len(factSpecs) == 0 {
		return chunks, stats
	}
	isFactQuery := len(factSpecs) > 0

	type evidenceDecision struct {
		chunk    RetrievedChunk
		direct   bool
		rawScore float64
	}
	decisions := make([]evidenceDecision, 0, len(chunks))
	topRawScore := math.Inf(-1)
	hasDirectEvidence := false
	for _, chunk := range chunks {
		if chunk.Kind == "structured_query" || containsString(chunk.RetrievalChannels, "structured") {
			decisions = append(decisions, evidenceDecision{chunk: chunk, direct: true, rawScore: chunkRawScore(chunk)})
			hasDirectEvidence = true
			continue
		}

		hits := evidenceHitCount(queryTerms, chunk.Text)
		coverage := 0.0
		if len(queryTerms) > 0 {
			coverage = float64(hits) / float64(len(queryTerms))
		}
		direct := false
		if isFactQuery {
			// 事实型问题必须在片段中找到所问属性（或可靠别名）。
			// 这样可以阻止“同主题但缺少目标字段”的高分片段进入回答上下文。
			direct = factEvidenceScore(query, chunk) >= 5
		} else {
			direct = hits >= 2 || coverage >= retrievalEvidenceCoverageThreshold
		}
		rawScore := chunkRawScore(chunk)
		decisions = append(decisions, evidenceDecision{
			chunk:    chunk,
			direct:   direct,
			rawScore: rawScore,
		})
		if direct {
			hasDirectEvidence = true
		}
		if rawScore > topRawScore {
			topRawScore = rawScore
		}
	}

	if !hasDirectEvidence && (isFactQuery || topRawScore < retrievalSemanticOnlyScoreThreshold) {
		stats.OutputCount = 0
		stats.DroppedCount = stats.InputCount
		return nil, stats
	}

	kept := make([]RetrievedChunk, 0, len(decisions))
	for _, decision := range decisions {
		if decision.direct ||
			(!isFactQuery && !hasDirectEvidence && decision.rawScore >= topRawScore-retrievalSemanticScoreMargin && decision.rawScore >= retrievalSemanticOnlyScoreThreshold) {
			kept = append(kept, decision.chunk)
		}
	}
	stats.OutputCount = len(kept)
	stats.DroppedCount = stats.InputCount - stats.OutputCount
	return kept, stats
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
	if specs := strictFactQuerySpecs(query); len(specs) > 0 {
		return factEvidenceCoverage(specs, chunks)
	}
	return entityCoverage(query, chunks)
}

func strictFactQuerySpecs(query string) []factQuerySpec {
	specs := parseFactQuerySpecs(query)
	if len(specs) == 0 {
		return nil
	}
	strict := make([]factQuerySpec, 0, len(specs))
	for _, spec := range specs {
		// 只有已识别且有可靠别名的属性才启用严格门控。
		// 未知属性不能被当成事实字段，否则普通的开放式问题会因为
		// 解析出的自然语言谓词未逐字出现在片段中而丢失全部证据。
		if isKnownFactAttribute(spec.Attribute) {
			strict = append(strict, spec)
		}
	}
	return strict
}

func factEvidenceCoverage(specs []factQuerySpec, chunks []RetrievedChunk) float64 {
	if len(specs) == 0 || len(chunks) == 0 {
		return 0
	}

	// 多属性问题按属性分别计算，避免某一个属性命中就把整体覆盖率
	// 误报为完整支持。
	type requirementKey struct {
		name    string
		aliases string
	}
	requirements := make([]factAttributeRequirement, 0)
	seen := make(map[requirementKey]struct{})
	for _, spec := range specs {
		current := spec.Requirements
		if len(current) == 0 {
			current = []factAttributeRequirement{{Name: spec.Attribute, Aliases: mergeRetrievalQueries([]string{spec.Attribute}, spec.Aliases)}}
		}
		for _, requirement := range current {
			key := requirementKey{
				name:    strings.ToLower(strings.TrimSpace(requirement.Name)),
				aliases: strings.Join(mergeRetrievalQueries(requirement.Aliases), "\x00"),
			}
			if key.name == "" || key.aliases == "" {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			requirements = append(requirements, requirement)
		}
	}
	if len(requirements) == 0 {
		return 0
	}

	matched := 0
	for _, requirement := range requirements {
		for _, chunk := range chunks {
			if _, ok := matchFactEvidence(factQuerySpec{
				Attribute:    requirement.Name,
				Requirements: []factAttributeRequirement{requirement},
			}, chunk.Text); ok {
				matched++
				break
			}
		}
	}
	return float64(matched) / float64(len(requirements))
}

type factQuerySpec struct {
	Subject      string
	Attribute    string
	Aliases      []string
	Requirements []factAttributeRequirement
}

type factAttributeRequirement struct {
	Name    string
	Aliases []string
}

type factEvidenceMatch struct {
	SubjectMatched   bool
	AttributeMatched bool
	AttributeExact   bool
	AttributeAlias   string
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
		match, ok := matchFactEvidence(spec, text)
		if !ok {
			continue
		}

		// 属性命中是事实证据的必要条件；主题词命中只能提高置信度，
		// 不能替代属性本身。
		score := 2
		if match.AttributeExact {
			score++
		}
		if match.SubjectMatched {
			score += 3
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

func matchFactEvidence(spec factQuerySpec, text string) (factEvidenceMatch, bool) {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return factEvidenceMatch{}, false
	}

	match := factEvidenceMatch{}
	if spec.Subject != "" && strings.Contains(text, strings.ToLower(spec.Subject)) {
		match.SubjectMatched = true
	}

	requirements := spec.Requirements
	if len(requirements) == 0 {
		requirements = []factAttributeRequirement{{Name: spec.Attribute, Aliases: mergeRetrievalQueries([]string{spec.Attribute}, spec.Aliases)}}
	}
	for _, requirement := range requirements {
		aliases := mergeRetrievalQueries([]string{requirement.Name}, requirement.Aliases)
		for _, alias := range aliases {
			alias = strings.ToLower(strings.TrimSpace(alias))
			if alias == "" || !strings.Contains(text, alias) {
				continue
			}
			match.AttributeMatched = true
			match.AttributeAlias = alias
			match.AttributeExact = strings.EqualFold(alias, spec.Attribute)
			return match, true
		}
	}
	return factEvidenceMatch{}, false
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

func parseFactQuerySpecs(query string) []factQuerySpec {
	normalized := normalizeFactQueryText(query)
	if normalized == "" {
		return nil
	}

	specs := make([]factQuerySpec, 0, 2)
	for searchStart := 0; searchStart < len(normalized); {
		relativeIndex := strings.Index(normalized[searchStart:], "的")
		if relativeIndex < 0 {
			break
		}
		index := searchStart + relativeIndex
		if index > 0 && index < len(normalized)-len("的") {
			subject := cleanFactSubject(normalized[:index])
			attribute := cleanFactAttribute(normalized[index+len("的"):])
			if subject != "" && attribute != "" {
				// 优先使用第一个“对象的属性”分界点，避免把
				// “类型的信息”等属性内部短语误识别为目标属性。
				specs = append(specs, newFactQuerySpec(subject, attribute))
				break
			}
		}
		searchStart = index + len("的")
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
		if subject != "" && attribute != "" && isKnownFactAttribute(attribute) {
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
		Subject:      cleanFactSubject(subject),
		Attribute:    attribute,
		Aliases:      factAttributeAliases(attribute),
		Requirements: factAttributeRequirements(attribute),
	}
}

func factAttributeAliases(attribute string) []string {
	requirements := factAttributeRequirements(attribute)
	aliases := make([]string, 0)
	for _, requirement := range requirements {
		aliases = append(aliases, requirement.Aliases...)
	}
	return mergeRetrievalQueries(aliases)
}

func isKnownFactAttribute(attribute string) bool {
	attribute = strings.ToLower(strings.TrimSpace(attribute))
	if attribute == "" {
		return false
	}
	for _, group := range factAttributeAliasGroups() {
		for _, alias := range group {
			alias = strings.ToLower(strings.TrimSpace(alias))
			if alias == "" || (alias == "时间" && attribute != alias) {
				continue
			}
			if attribute == alias || strings.Contains(attribute, alias) {
				return true
			}
		}
	}
	return false
}

func factAttributeRequirements(attribute string) []factAttributeRequirement {
	attribute = strings.ToLower(strings.TrimSpace(attribute))
	if attribute == "" {
		return nil
	}

	requirements := make([]factAttributeRequirement, 0, 2)
	for _, group := range factAttributeAliasGroups() {
		matched := false
		longest := ""
		for _, alias := range group {
			alias = strings.ToLower(strings.TrimSpace(alias))
			if alias == "" {
				continue
			}
			// “时间”等单字概念过于宽泛，只有在用户明确询问该属性时
			// 才作为别名；复合属性必须优先使用完整短语或可靠别名。
			if alias == "时间" && attribute != alias {
				continue
			}
			if attribute == alias || strings.Contains(attribute, alias) {
				matched = true
				if len([]rune(alias)) > len([]rune(longest)) {
					longest = alias
				}
			}
		}
		if !matched {
			continue
		}
		aliases := make([]string, 0, len(group)+1)
		for _, alias := range mergeRetrievalQueries([]string{attribute}, group) {
			if alias == "时间" && attribute != alias {
				continue
			}
			aliases = append(aliases, alias)
		}
		requirements = append(requirements, factAttributeRequirement{
			Name:    longest,
			Aliases: aliases,
		})
	}

	if len(requirements) == 0 {
		return []factAttributeRequirement{{Name: attribute, Aliases: []string{attribute}}}
	}
	return requirements
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
		{"payload", "载荷", "有效载荷"},
		{"价格", "售价", "费用", "金额", "单价", "总价", "薪资", "工资", "收入"},
		{"年龄", "岁数"},
		{"编号", "工号", "教师编号", "员工编号", "学号", "身份证号", "证件号"},
		{"负责人", "联系人", "校长", "法人", "法定代表人", "负责人姓名"},
		{"时间", "日期", "年份", "年度"},
		{"响应时间", "在线响应时间", "延迟时间", "时延", "延迟", "耗时", "处理时间"},
		{"吞吐量", "在线吞吐量", "处理吞吐量", "并发量"},
		{"发布日期", "发布时间", "发布于", "上线时间", "上线日期"},
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
	for _, marker := range []string{"是否", "能否", "有没有", "有无"} {
		if index := strings.Index(subject, marker); index > 0 {
			subject = subject[:index]
			break
		}
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
	for _, marker := range []string{"可以", "能够", "是否", "能否", "支持", "提供", "包含", "包括", "具有"} {
		if index := strings.Index(attribute, marker); index > 0 {
			attribute = attribute[:index]
			break
		}
	}
	for _, suffix := range []string{"是什么时候", "是哪一年", "是几几年", "是什么", "是多少", "有多少", "是谁", "哪位", "哪一个", "吗", "呢"} {
		attribute = strings.TrimSuffix(attribute, suffix)
	}
	attribute = strings.Trim(attribute, " ，,。.!！?？：:；;“”\"'`")
	attribute = strings.TrimPrefix(attribute, "是")
	if strings.HasPrefix(attribute, "什么时候") || strings.HasPrefix(attribute, "何时") {
		attribute = strings.TrimPrefix(attribute, "什么时候")
		attribute = strings.TrimPrefix(attribute, "何时")
		attribute = strings.TrimSpace(attribute)
		if attribute != "" {
			attribute += "时间"
		}
	}
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

func limitRetrievalQueries(queries []string, limit int) []string {
	if limit <= 0 || len(queries) <= limit {
		return queries
	}
	return append([]string(nil), queries[:limit]...)
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
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
