package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"ai-localbase/internal/model"
)

// debugRetrieveVerboseWithContext exposes the retrieval stages without making
// the regular answer path depend on debug-only response shaping.
func (s *AppService) debugRetrieveVerboseWithContext(ctx context.Context, req model.ChatCompletionRequest, query string) ([]RetrievedChunk, *model.RetrievalDebugVerboseDetails, []string, error) {
	verboseDetails := &model.RetrievalDebugVerboseDetails{}
	ctx = normalizeServiceContext(ctx)
	if s.qdrant == nil || !s.qdrant.IsEnabled() {
		return nil, verboseDetails, nil, nil
	}

	knowledgeBaseIDs, err := s.resolveRetrievalKnowledgeBaseIDs(req)
	if err != nil {
		return nil, nil, nil, err
	}

	params := s.resolveRetrievalParams(req)
	useHybrid := s.shouldUseHybridSearch(req)
	var queryVariants []string
	var candidates []RetrievedChunk

	embeddingStart := time.Now()
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

		rewriteStart := time.Now()
		history := recentConversationHistory(req.Messages, 3)
		rewriteResult, err := s.queryRewriter.Rewrite(ctx, query, history)
		rewriteMs := time.Since(rewriteStart).Milliseconds()

		if err == nil && len(rewriteResult.RewrittenQueries) > 0 {
			queryVariants = rewriteResult.RewrittenQueries
			queries := limitRetrievalQueries(
				mergeRetrievalQueries([]string{query}, rewriteResult.RewrittenQueries),
				maxMultiQuerySearchQueries,
			)
			embeddingConfig := s.resolveEmbeddingConfig(req)

			searchStart := time.Now()
			seenChunkIDs := make(map[string]struct{})
			for _, knowledgeBaseID := range knowledgeBaseIDs {
				filter := map[string]any{}
				if documentID := strings.TrimSpace(req.DocumentID); documentID != "" {
					filter = map[string]any{
						"must": []map[string]any{{
							"key":   "document_id",
							"match": map[string]any{"value": documentID},
						}},
					}
				}
				filter = s.withCurrentIndexFenceFilter(knowledgeBaseID, filter, req.DocumentID)
				results, err := s.rag.MultiQuerySearchWithFilter(ctx, queries, knowledgeBaseID, params.candidateTopK, 0, embeddingConfig, filter)
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
					candidates = append(candidates, item)
				}
			}
			verboseDetails.VectorSearchMs = time.Since(searchStart).Milliseconds()
			verboseDetails.QueryRewriteDetails = &model.QueryRewriteDebugDetails{
				OriginalQuery:    query,
				RewrittenQueries: rewriteResult.RewrittenQueries,
				RewriteMs:        rewriteMs,
				TotalQueries:     len(queries),
			}
		}
	}

	if len(candidates) == 0 {
		embedCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
		vectors, err := s.rag.EmbedTexts(embedCtx, s.resolveEmbeddingConfig(req), []string{query}, s.qdrantVectorSize())
		if err == nil && len(vectors) == 0 {
			err = fmt.Errorf("embedding api returned no vectors")
		}
		if err != nil {
			return nil, nil, nil, err
		}
		queryVector := vectors[0]
		verboseDetails.QueryEmbeddingMs = time.Since(embeddingStart).Milliseconds()

		searchStart := time.Now()
		candidates, err = s.collectCandidatesForQueries(ctx, knowledgeBaseIDs, req, queryVector, nil, params.candidateTopK, useHybrid, query)
		if err != nil {
			return nil, nil, nil, err
		}
		verboseDetails.VectorSearchMs = time.Since(searchStart).Milliseconds()
	}
	candidates = s.filterRetrievedChunksToScope(req, knowledgeBaseIDs, candidates)

	verboseDetails.CandidatesCount = len(candidates)
	verboseDetails.TopCandidates = convertToDebugChunks(candidates, query, 5)

	rerankStart := time.Now()
	var afterRerank []RetrievedChunk
	if s.shouldBypassRerank(candidates) {
		afterRerank = candidates
		verboseDetails.RerankMs = 0
	} else {
		afterRerank = s.rerankCandidates(ctx, candidates, query, req)
		verboseDetails.RerankMs = time.Since(rerankStart).Milliseconds()
	}
	verboseDetails.AfterRerankCount = len(afterRerank)
	verboseDetails.TopAfterRerank = convertToDebugChunks(afterRerank, query, 5)

	mmrStart := time.Now()
	var selected []RetrievedChunk
	beforeMMR := afterRerank
	if s.shouldBypassMMR(afterRerank, params) {
		selected = takeTopChunks(afterRerank, params.finalTopK, params.perDocumentLimit)
		verboseDetails.MMRMs = 0
	} else {
		selected = selectWithMMR(afterRerank, params.finalTopK, params.perDocumentLimit)
		verboseDetails.MMRMs = time.Since(mmrStart).Milliseconds()
	}
	verboseDetails.AfterMMRCount = len(selected)
	verboseDetails.TopAfterMMR = convertToDebugChunks(selected, query, 5)
	verboseDetails.MMREffect = analyzeMMREffect(beforeMMR, selected, query)

	return selected, verboseDetails, queryVariants, nil
}

func convertToDebugChunks(chunks []RetrievedChunk, query string, limit int) []model.RetrievalDebugChunk {
	if len(chunks) == 0 {
		return nil
	}
	count := minInt(len(chunks), limit)
	result := make([]model.RetrievalDebugChunk, count)
	for i := 0; i < count; i++ {
		result[i] = buildRetrievalDebugChunk(query, chunks[i])
	}
	return result
}

func analyzeMMREffect(before, after []RetrievedChunk, query string) *model.MMREffectAnalysis {
	if len(before) == 0 || len(after) == 0 {
		return nil
	}

	beforeMap := make(map[string]int)
	for i, chunk := range before {
		beforeMap[chunk.ID] = i
	}

	afterMap := make(map[string]int)
	for i, chunk := range after {
		afterMap[chunk.ID] = i
	}

	var rankingChanges []model.RankingChange
	reorderedCount := 0
	for _, chunk := range after {
		beforeRank, existedBefore := beforeMap[chunk.ID]
		afterRank := afterMap[chunk.ID]

		if existedBefore && beforeRank != afterRank {
			reorderedCount++
			rankingChanges = append(rankingChanges, model.RankingChange{
				ChunkID:      chunk.ID,
				DocumentName: chunk.DocumentName,
				BeforeRank:   beforeRank + 1,
				AfterRank:    afterRank + 1,
				ScoreBefore:  before[beforeRank].Score,
				ScoreAfter:   chunk.Score,
			})
		}
	}

	removedCount := len(before) - len(after)
	diversityScore := calculateDiversityScore(after)

	return &model.MMREffectAnalysis{
		RemovedDuplicates: removedCount,
		ReorderedItems:    reorderedCount,
		DiversityScore:    diversityScore,
		BeforeMMR:         convertToDebugChunks(before, query, 10),
		AfterMMR:          convertToDebugChunks(after, query, 10),
		RankingChanges:    rankingChanges,
	}
}

func calculateDiversityScore(chunks []RetrievedChunk) float64 {
	if len(chunks) <= 1 {
		return 1.0
	}

	totalSimilarity := 0.0
	count := 0
	for i := 0; i < len(chunks)-1; i++ {
		for j := i + 1; j < len(chunks); j++ {
			sim := textJaccardSimilarity(chunks[i].Text, chunks[j].Text)
			totalSimilarity += sim
			count++
		}
	}

	if count == 0 {
		return 1.0
	}
	avgSimilarity := totalSimilarity / float64(count)
	return 1.0 - avgSimilarity
}

func buildRetrievalDebugChunk(query string, chunk RetrievedChunk) model.RetrievalDebugChunk {
	evidenceID, charStart, charEnd, lineStart, lineEnd, tableRow, tableColumns := evidenceDebugFields(chunk)
	return model.RetrievalDebugChunk{
		ID:                chunk.ID,
		EvidenceID:        evidenceID,
		KnowledgeBaseID:   chunk.KnowledgeBaseID,
		DocumentID:        chunk.DocumentID,
		DocumentName:      chunk.DocumentName,
		Index:             chunk.Index,
		Kind:              chunk.Kind,
		Score:             chunk.Score,
		Text:              truncateRunes(strings.TrimSpace(chunk.Text), retrievalDebugChunkTextLimit),
		CharStart:         charStart,
		CharEnd:           charEnd,
		LineStart:         lineStart,
		LineEnd:           lineEnd,
		TableRow:          tableRow,
		TableColumns:      tableColumns,
		MatchReasons:      buildRetrievalDebugMatchReasons(query, chunk),
		RetrievalChannels: chunk.RetrievalChannels,
		DenseRank:         chunk.DenseRank,
		SparseRank:        chunk.SparseRank,
	}
}

func isLowConfidenceSelection(_ string, chunks []RetrievedChunk) bool {
	if len(chunks) == 0 {
		return true
	}
	topScore := chunks[0].Score
	avgScore := averageScore(chunks)
	return topScore < lowConfidenceTopScoreThreshold || avgScore < lowConfidenceAvgScoreThreshold
}

func buildRetrievalDebugConfidence(query string, chunks []RetrievedChunk) model.RetrievalDebugConfidence {
	topScore := 0.0
	if len(chunks) > 0 {
		topScore = chunks[0].Score
	}
	avgScore := averageScore(chunks)
	evidenceCoverage := queryEvidenceCoverage(query, chunks)
	factSpecs := strictFactQuerySpecs(query)
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
	if len(factSpecs) > 0 && evidenceCoverage < 1 {
		if evidenceCoverage == 0 {
			reasons = append(reasons, "证据片段未出现问题要求的事实属性或可靠别名")
		} else {
			reasons = append(reasons, fmt.Sprintf("问题要求的事实属性覆盖率 %.1f%%，仍有属性缺失", evidenceCoverage*100))
		}
		suggestions = append(suggestions, "确认文档包含所询问的字段或属性，不要只依据主题相似度判断")
	}
	if len(factSpecs) == 0 && evidenceCoverage < 0.2 {
		reasons = append(reasons, fmt.Sprintf("问题实体覆盖率 %.1f%% 低于 20%%", evidenceCoverage*100))
		suggestions = append(suggestions, "启用 Query Rewrite 或改用更贴近文档原文的问法")
	}
	if len(reasons) == 0 {
		return model.RetrievalDebugConfidence{
			Status:           "normal",
			Summary:          "置信正常：命中分数和问题实体覆盖率都处于可接受范围。",
			TopScore:         topScore,
			AverageScore:     avgScore,
			EvidenceCoverage: evidenceCoverage,
		}
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

func buildRetrievalDebugMatchReasons(query string, chunk RetrievedChunk) []string {
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
	if specs := strictFactQuerySpecs(query); len(specs) > 0 {
		matched := false
		for _, spec := range specs {
			if evidence, ok := matchFactEvidence(spec, chunk.Text); ok {
				matched = true
				reasons = append(reasons, fmt.Sprintf("匹配问题属性：%s", evidence.AttributeAlias))
				break
			}
		}
		if !matched {
			reasons = append(reasons, "未匹配问题要求的事实属性")
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

	if chunk.Kind == "structured_query" {
		reasons = append(reasons, "结构化确定性结果")
	} else if chunk.Kind == "structured_summary" || chunk.Kind == "structured_row" {
		reasons = append(reasons, "结构化数据片段")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "由检索排序策略保留")
	}
	return reasons
}
