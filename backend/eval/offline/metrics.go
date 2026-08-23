package offline

import (
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
)

// CaseResult 单个用例的评估结果
type CaseResult struct {
	CaseID            string
	Question          string
	GroundTruth       GroundTruthCase
	RetrievedChunks   []RetrievedChunkInfo // 检索到的文档信息
	LLMAnswer         string
	RetrievalLatency  time.Duration
	GenerationLatency time.Duration
	DocumentHit       bool
	ChunkHit          bool
	AnswerSnippetHit  bool
	DirectEvidenceHit bool
	HitRank           int     // 第一个命中的排名，-1 表示未命中
	ReciprocalRank    float64 // 1/HitRank，未命中为 0
	Error             string  // 运行时错误信息（若有）
	FailureCategory   string  // 失败根因分类
	FailureReason     string  // 失败根因说明
}

// RetrievedChunkInfo 检索结果摘要（不依赖 service 包，避免循环依赖）
type RetrievedChunkInfo struct {
	KnowledgeBaseID string
	DocumentID      string
	ChunkID         string
	Text            string
	Score           float64
}

// AggregateMetrics 聚合后的评估指标
type AggregateMetrics struct {
	TotalCases            int
	HitRate               float64
	DocumentHitRate       float64
	ChunkHitRate          float64
	AnswerSnippetHitRate  float64
	DirectEvidenceHitRate float64
	MRR                   float64
	LatencyRetrievalP50   time.Duration
	LatencyRetrievalP95   time.Duration
	LatencyGenerationP50  time.Duration
	LatencyGenerationP95  time.Duration
}

type HitClassification struct {
	Hit               bool
	DocumentHit       bool
	ChunkHit          bool
	AnswerSnippetHit  bool
	DirectEvidenceHit bool
	Rank              int
}

const (
	FailureCategoryRecallMiss        = "recall_miss"
	FailureCategoryRankMiss          = "rank_miss"
	FailureCategoryEvidenceGateMiss  = "evidence_gate_miss"
	FailureCategoryCitationMismatch  = "citation_mismatch"
	FailureCategoryTableIntentMiss   = "table_intent_miss"
	FailureCategoryFilenameScopeMiss = "filename_scope_miss"
	FailureCategoryNoAnswerPolicy    = "no_answer_policy_miss"
	FailureCategoryDatasetIssue      = "dataset_issue"
	FailureCategoryRuntimeError      = "runtime_error"
)

// FailureClassification explains why a case did not produce a trustworthy hit.
// It intentionally uses only evaluator-visible evidence so the result remains
// reproducible across real and mock retrieval implementations.
type FailureClassification struct {
	Category string
	Reason   string
}

// IsHit 判断单个用例是否命中（支持 Chunk 精确匹配和无 ChunkID 时的片段匹配）。
// threshold 仅用于没有 source_documents 时的文本片段匹配，范围为 0-1。
func IsHit(result CaseResult, gt GroundTruthCase, threshold float64) (hit bool, rank int) {
	classification := ClassifyHit(result, gt, threshold)
	return classification.Hit, classification.Rank
}

func normalizeHitThreshold(threshold float64) float64 {
	if threshold <= 0 {
		return 0.5
	}
	if threshold > 1 {
		return 1
	}
	return threshold
}

func normalizeEvalText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, value)
}

func snippetMatchScore(text, snippet string) float64 {
	text = normalizeEvalText(text)
	snippet = normalizeEvalText(snippet)
	if text == "" || snippet == "" {
		return 0
	}
	if strings.Contains(text, snippet) {
		return 1
	}

	textRunes := []rune(text)
	snippetRunes := []rune(snippet)
	if len(snippetRunes) == 1 {
		if strings.ContainsRune(text, snippetRunes[0]) {
			return 1
		}
		return 0
	}

	textGrams := make(map[string]struct{}, len(textRunes)-1)
	for i := 0; i < len(textRunes)-1; i++ {
		textGrams[string(textRunes[i:i+2])] = struct{}{}
	}
	seen := make(map[string]struct{}, len(snippetRunes)-1)
	matched := 0
	for i := 0; i < len(snippetRunes)-1; i++ {
		gram := string(snippetRunes[i : i+2])
		if _, exists := seen[gram]; exists {
			continue
		}
		seen[gram] = struct{}{}
		if _, exists := textGrams[gram]; exists {
			matched++
		}
	}
	if len(seen) == 0 {
		return 0
	}
	return float64(matched) / float64(len(seen))
}

func retrievedChunkMatchesSource(chunk RetrievedChunkInfo, source SourceDocument) bool {
	if !retrievedChunkMatchesDocument(chunk, source) {
		return false
	}
	if strings.TrimSpace(source.ChunkID) == "" {
		return true
	}
	return strings.TrimSpace(chunk.ChunkID) != "" &&
		strings.TrimSpace(chunk.ChunkID) == strings.TrimSpace(source.ChunkID)
}

func retrievedChunkMatchesDocument(chunk RetrievedChunkInfo, source SourceDocument) bool {
	if strings.TrimSpace(source.KnowledgeBaseID) != "" &&
		strings.TrimSpace(chunk.KnowledgeBaseID) != "" &&
		strings.TrimSpace(chunk.KnowledgeBaseID) != strings.TrimSpace(source.KnowledgeBaseID) {
		return false
	}
	if strings.TrimSpace(source.DocumentID) == "" ||
		strings.TrimSpace(chunk.DocumentID) != strings.TrimSpace(source.DocumentID) {
		return false
	}
	return true
}

// ClassifyHit 拆分文档、Chunk、答案片段和直接证据四类命中，便于识别引用不支撑答案的情况。
func ClassifyHit(result CaseResult, gt GroundTruthCase, threshold float64) HitClassification {
	threshold = normalizeHitThreshold(threshold)
	classification := HitClassification{Rank: -1}
	hasExactChunkSource := false
	for _, source := range gt.SourceDocuments {
		if strings.TrimSpace(source.ChunkID) != "" {
			hasExactChunkSource = true
			break
		}
	}

	for index, chunk := range result.RetrievedChunks {
		rank := index + 1
		if !classification.DocumentHit {
			for _, source := range gt.SourceDocuments {
				if retrievedChunkMatchesDocument(chunk, source) {
					classification.DocumentHit = true
					break
				}
			}
		}
		if !classification.ChunkHit {
			for _, source := range gt.SourceDocuments {
				if strings.TrimSpace(source.ChunkID) != "" && retrievedChunkMatchesSource(chunk, source) {
					classification.ChunkHit = true
					break
				}
			}
		}
		if !classification.AnswerSnippetHit {
			for _, snippet := range gt.AnswerSnippets {
				if snippetMatchScore(chunk.Text, snippet) >= threshold {
					classification.AnswerSnippetHit = true
					break
				}
			}
		}
		if classification.Rank == -1 {
			if (hasExactChunkSource && classification.ChunkHit) ||
				(!hasExactChunkSource && len(gt.SourceDocuments) > 0 && classification.DocumentHit) ||
				(len(gt.SourceDocuments) == 0 && classification.AnswerSnippetHit) {
				classification.Rank = rank
			}
		}
	}

	classification.DirectEvidenceHit = classification.ChunkHit || classification.AnswerSnippetHit
	if hasExactChunkSource {
		classification.Hit = classification.ChunkHit
	} else if len(gt.SourceDocuments) > 0 {
		classification.Hit = classification.DocumentHit
	} else {
		classification.Hit = classification.AnswerSnippetHit
	}
	return classification
}

// ClassifyFailure assigns a stable, coarse-grained root cause to an evaluation case.
// The evaluator cannot infer every product-level cause from chunks alone, so it
// prefers conservative categories over pretending to know the exact failure stage.
func ClassifyFailure(result CaseResult, gt GroundTruthCase, threshold float64) FailureClassification {
	if strings.TrimSpace(result.Error) != "" && strings.TrimSpace(result.Error) != "未命中" {
		return FailureClassification{
			Category: FailureCategoryRuntimeError,
			Reason:   strings.TrimSpace(result.Error),
		}
	}

	classification := ClassifyHit(result, gt, threshold)
	if isNoAnswerCase(gt) && classification.Hit {
		return FailureClassification{
			Category: FailureCategoryNoAnswerPolicy,
			Reason:   "无答案用例返回了可命中的证据",
		}
	}
	if classification.Hit {
		return FailureClassification{}
	}
	if len(gt.SourceDocuments) == 0 && len(gt.AnswerSnippets) == 0 {
		return FailureClassification{
			Category: FailureCategoryDatasetIssue,
			Reason:   "用例没有 source_documents 或 answer_snippets，无法判断证据是否正确",
		}
	}
	if !classification.DocumentHit {
		return FailureClassification{
			Category: FailureCategoryRecallMiss,
			Reason:   "返回结果没有命中期望文档",
		}
	}
	if classification.AnswerSnippetHit && !classification.ChunkHit {
		return FailureClassification{
			Category: FailureCategoryCitationMismatch,
			Reason:   "结果包含答案片段，但没有命中标注的 Chunk",
		}
	}
	if hasExactChunkSource(gt) && !classification.ChunkHit {
		return FailureClassification{
			Category: FailureCategoryRankMiss,
			Reason:   "命中了目标文档，但正确 Chunk 未被召回或排名不足",
		}
	}
	if !classification.DirectEvidenceHit {
		if looksLikeTableCase(gt) {
			return FailureClassification{
				Category: FailureCategoryTableIntentMiss,
				Reason:   "表格问题命中文档，但返回片段没有覆盖答案证据",
			}
		}
		return FailureClassification{
			Category: FailureCategoryEvidenceGateMiss,
			Reason:   "命中文档范围，但没有足够的直接答案证据",
		}
	}
	return FailureClassification{
		Category: FailureCategoryRankMiss,
		Reason:   "结果存在部分证据，但未达到当前命中判定标准",
	}
}

func hasExactChunkSource(gt GroundTruthCase) bool {
	for _, source := range gt.SourceDocuments {
		if strings.TrimSpace(source.ChunkID) != "" {
			return true
		}
	}
	return false
}

func isNoAnswerCase(gt GroundTruthCase) bool {
	answerType := strings.ToLower(strings.TrimSpace(gt.AnswerType))
	return answerType == "no_answer" || answerType == "unanswerable" || answerType == "unknown"
}

func looksLikeTableCase(gt GroundTruthCase) bool {
	answerType := strings.ToLower(strings.TrimSpace(gt.AnswerType))
	notes := strings.ToLower(strings.TrimSpace(gt.Notes))
	return strings.Contains(answerType, "table") || strings.Contains(notes, "表格") || strings.Contains(notes, "structured")
}

// ComputeHitRate 计算命中率
func ComputeHitRate(results []CaseResult, gts []GroundTruthCase, threshold float64) float64 {
	if len(results) == 0 {
		return 0.0
	}

	groundTruthByID := groundTruthIndex(gts)
	hits := 0
	evaluated := 0
	for i, res := range results {
		gt, ok := groundTruthForResult(res, i, gts, groundTruthByID)
		if !ok {
			continue
		}
		evaluated++
		if hit, _ := IsHit(res, gt, threshold); hit {
			hits++
		}
	}
	if evaluated == 0 {
		return 0
	}
	return float64(hits) / float64(evaluated)
}

// ComputeMRR 计算 MRR
func ComputeMRR(results []CaseResult, gts []GroundTruthCase, threshold float64) float64 {
	if len(results) == 0 {
		return 0.0
	}

	groundTruthByID := groundTruthIndex(gts)
	var sumReciprocalRank float64
	evaluated := 0
	for i, res := range results {
		gt, ok := groundTruthForResult(res, i, gts, groundTruthByID)
		if !ok {
			continue
		}
		evaluated++
		if hit, rank := IsHit(res, gt, threshold); hit && rank > 0 {
			sumReciprocalRank += 1.0 / float64(rank)
		}
	}
	if evaluated == 0 {
		return 0
	}
	return sumReciprocalRank / float64(evaluated)
}

func computeClassificationRates(results []CaseResult, gts []GroundTruthCase, threshold float64) (document, chunk, snippet, direct float64) {
	if len(results) == 0 {
		return 0, 0, 0, 0
	}
	groundTruthByID := groundTruthIndex(gts)
	var documentHits, chunkHits, snippetHits, directHits, evaluated int
	for index, result := range results {
		gt, ok := groundTruthForResult(result, index, gts, groundTruthByID)
		if !ok {
			continue
		}
		evaluated++
		classification := ClassifyHit(result, gt, threshold)
		if classification.DocumentHit {
			documentHits++
		}
		if classification.ChunkHit {
			chunkHits++
		}
		if classification.AnswerSnippetHit {
			snippetHits++
		}
		if classification.DirectEvidenceHit {
			directHits++
		}
	}
	if evaluated == 0 {
		return 0, 0, 0, 0
	}
	return float64(documentHits) / float64(evaluated),
		float64(chunkHits) / float64(evaluated),
		float64(snippetHits) / float64(evaluated),
		float64(directHits) / float64(evaluated)
}

func groundTruthIndex(gts []GroundTruthCase) map[string]GroundTruthCase {
	index := make(map[string]GroundTruthCase, len(gts))
	for _, gt := range gts {
		if id := strings.TrimSpace(gt.ID); id != "" {
			index[id] = gt
		}
	}
	return index
}

func groundTruthForResult(result CaseResult, position int, gts []GroundTruthCase, byID map[string]GroundTruthCase) (GroundTruthCase, bool) {
	if id := strings.TrimSpace(result.CaseID); id != "" {
		gt, ok := byID[id]
		return gt, ok
	}
	if strings.TrimSpace(result.GroundTruth.ID) != "" {
		return result.GroundTruth, true
	}
	if position >= 0 && position < len(gts) {
		return gts[position], true
	}
	return GroundTruthCase{}, false
}

// ComputeLatencyPercentiles 计算时延 P50/P95
func ComputeLatencyPercentiles(durations []time.Duration) (p50, p95 time.Duration) {
	if len(durations) == 0 {
		return 0, 0
	}

	sort.Slice(durations, func(i, j int) bool {
		return durations[i] < durations[j]
	})

	n := len(durations)
	// p50: 中位数，偶数取 n/2（上中位）
	p50Index := n / 2
	if p50Index >= n {
		p50Index = n - 1
	}
	// p95: floor((n-1)*0.95) 使得 10 个元素取 index 8 (80ms)
	p95Index := int(math.Floor(float64(n-1) * 0.95))
	if p95Index >= n {
		p95Index = n - 1
	}

	if p50Index < 0 {
		p50Index = 0
	}
	if p95Index < 0 {
		p95Index = 0
	}

	return durations[p50Index], durations[p95Index]
}

// Aggregate 汇总所有用例结果为聚合指标。
func Aggregate(results []CaseResult, gts []GroundTruthCase, threshold float64) AggregateMetrics {
	totalCases := len(results)
	if totalCases == 0 {
		return AggregateMetrics{}
	}

	var retrievalLatencies []time.Duration
	var generationLatencies []time.Duration
	for _, res := range results {
		retrievalLatencies = append(retrievalLatencies, res.RetrievalLatency)
		generationLatencies = append(generationLatencies, res.GenerationLatency)
	}

	retrievalP50, retrievalP95 := ComputeLatencyPercentiles(retrievalLatencies)
	generationP50, generationP95 := ComputeLatencyPercentiles(generationLatencies)

	hitRate := ComputeHitRate(results, gts, threshold)
	mrr := ComputeMRR(results, gts, threshold)
	documentHitRate, chunkHitRate, answerSnippetHitRate, directEvidenceHitRate := computeClassificationRates(results, gts, threshold)

	return AggregateMetrics{
		TotalCases:            totalCases,
		HitRate:               hitRate,
		DocumentHitRate:       documentHitRate,
		ChunkHitRate:          chunkHitRate,
		AnswerSnippetHitRate:  answerSnippetHitRate,
		DirectEvidenceHitRate: directEvidenceHitRate,
		MRR:                   mrr,
		LatencyRetrievalP50:   retrievalP50,
		LatencyRetrievalP95:   retrievalP95,
		LatencyGenerationP50:  generationP50,
		LatencyGenerationP95:  generationP95,
	}
}
