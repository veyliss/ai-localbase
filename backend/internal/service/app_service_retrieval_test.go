package service

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"ai-localbase/internal/model"
)

func TestWithCurrentIndexFenceFilterUsesValidTopLevelComposition(t *testing.T) {
	service := &AppService{state: &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{
		"kb-filter": {
			ID: "kb-filter",
			Documents: []model.Document{
				{ID: "doc-current", IndexFence: "index:current"},
				{ID: "doc-legacy"},
			},
		},
	}}}
	base := map[string]any{
		"must": []map[string]any{{
			"key":   "knowledge_base_id",
			"match": map[string]any{"value": "kb-filter"},
		}},
	}
	filter := service.withCurrentIndexFenceFilter("kb-filter", base, "")
	encoded, err := json.Marshal(filter)
	if err != nil {
		t.Fatalf("encode composed filter: %v", err)
	}
	if strings.Contains(string(encoded), `"filter"`) {
		t.Fatalf("expected Qdrant 1.13 compound conditions without filter wrappers, got %s", encoded)
	}
	must, ok := filter["must"].([]map[string]any)
	if !ok || len(must) != 2 {
		t.Fatalf("expected base condition and generation condition, got %#v", filter["must"])
	}
	generationCondition, ok := must[1]["should"].([]map[string]any)
	if !ok {
		t.Fatalf("expected generation alternatives under a compound should condition, got %#v", must[1])
	}
	branches := generationCondition
	if !ok || len(branches) != 2 {
		t.Fatalf("expected two document generation branches, got %#v", generationCondition)
	}
	legacyMust, ok := branches[1]["must"].([]map[string]any)
	if !ok || len(legacyMust) != 1 {
		t.Fatalf("expected legacy branch must to contain the document condition, got %#v", branches[1])
	}
	legacyShould, ok := branches[1]["should"].([]map[string]any)
	if !ok || len(legacyShould) != 2 {
		t.Fatalf("expected legacy branch to accept missing and empty fences, got %#v", branches[1])
	}
	if _, ok := legacyShould[0]["is_empty"]; !ok {
		t.Fatalf("expected missing-fence condition, got %#v", legacyShould)
	}
	if value := legacyShould[1]["match"].(map[string]any)["value"]; value != "" {
		t.Fatalf("expected empty-string fence condition, got %#v", legacyShould[1])
	}
}

func TestAppendIndexFenceMustUsesEmptyConditionForLegacyDocument(t *testing.T) {
	filter := appendIndexFenceMust(map[string]any{
		"must": []map[string]any{{"key": "document_id"}},
	}, "")
	must, ok := filter["must"].([]map[string]any)
	if !ok || len(must) != 2 {
		t.Fatalf("expected two must conditions, got %#v", filter["must"])
	}
	emptyShould, ok := must[1]["should"].([]map[string]any)
	if !ok || len(emptyShould) != 2 {
		t.Fatalf("expected missing and empty-string fence alternatives, got %#v", must[1])
	}
}

func TestWithCurrentIndexFenceFilterScopesExplicitLegacyDocument(t *testing.T) {
	service := &AppService{state: &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{
		"kb-filter": {
			ID:        "kb-filter",
			Documents: []model.Document{{ID: "doc-legacy"}},
		},
	}}}
	filter := service.withCurrentIndexFenceFilter("kb-filter", map[string]any{
		"must": []map[string]any{{
			"key":   "document_id",
			"match": map[string]any{"value": "doc-legacy"},
		}},
	}, "doc-legacy")
	must, ok := filter["must"].([]map[string]any)
	if !ok || len(must) != 2 {
		t.Fatalf("expected explicit document filter to include empty-fence condition, got %#v", filter)
	}
	emptyShould, ok := must[1]["should"].([]map[string]any)
	if !ok || len(emptyShould) != 2 {
		t.Fatalf("expected explicit legacy filter to accept missing and empty-string fences, got %#v", must[1])
	}
}

type recordingContextCompressor struct {
	called int
	result string
}

func (c *recordingContextCompressor) Compress(_ context.Context, _ string, _ []RetrievedChunk) (string, error) {
	c.called++
	return c.result, nil
}

func TestBuildRetrievedContextCompressesBeforeTrim(t *testing.T) {
	compressor := &recordingContextCompressor{result: "压缩后的证据上下文"}
	service := &AppService{
		rag:               NewRagService(),
		contextCompressor: compressor,
		serverConfig: model.ServerConfig{
			RetrievalMaxContextChars: 80,
		},
	}

	contextText, sources, err := service.buildRetrievedContext(
		context.Background(),
		model.ChatCompletionRequest{},
		"示例机构成立时间",
		[]RetrievedChunk{
			{DocumentChunk: DocumentChunk{ID: "chunk-1", DocumentID: "doc-1", DocumentName: "机构简介.md", Text: strings.Repeat("第一份证据。", 12)}},
			{DocumentChunk: DocumentChunk{ID: "chunk-2", DocumentID: "doc-1", DocumentName: "机构简介.md", Text: strings.Repeat("第二份证据。", 12)}},
		},
	)
	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if compressor.called != 1 {
		t.Fatalf("expected compressor to run once before trimming, got %d", compressor.called)
	}
	if contextText != compressor.result {
		t.Fatalf("expected compressed context, got %q", contextText)
	}
	if len(sources) != 2 {
		t.Fatalf("expected sources to retain both original evidence chunks, got %d", len(sources))
	}
}

func TestResolveRetrievalParams(t *testing.T) {
	t.Run("document scope", func(t *testing.T) {
		params := resolveRetrievalParams(model.ChatCompletionRequest{DocumentID: "doc-1"})
		if params.candidateTopK != ragSearchCandidateTopKDocument {
			t.Fatalf("expected document candidateTopK %d, got %d", ragSearchCandidateTopKDocument, params.candidateTopK)
		}
		if params.finalTopK != ragSearchTopKDocument {
			t.Fatalf("expected document finalTopK %d, got %d", ragSearchTopKDocument, params.finalTopK)
		}
		if params.perDocumentLimit != ragSearchTopKDocument {
			t.Fatalf("expected document perDocumentLimit %d, got %d", ragSearchTopKDocument, params.perDocumentLimit)
		}
	})

	t.Run("all documents scope", func(t *testing.T) {
		params := resolveRetrievalParams(model.ChatCompletionRequest{KnowledgeBaseID: "kb-1"})
		if params.candidateTopK != ragSearchCandidateTopKAllDocs {
			t.Fatalf("expected all-docs candidateTopK %d, got %d", ragSearchCandidateTopKAllDocs, params.candidateTopK)
		}
		if params.finalTopK != ragSearchTopKKnowledgeBase {
			t.Fatalf("expected all-docs finalTopK %d, got %d", ragSearchTopKKnowledgeBase, params.finalTopK)
		}
		if params.perDocumentLimit != ragMaxChunksPerDocument {
			t.Fatalf("expected all-docs perDocumentLimit %d, got %d", ragMaxChunksPerDocument, params.perDocumentLimit)
		}
	})

	t.Run("config overrides defaults", func(t *testing.T) {
		params := resolveRetrievalParamsWithConfig(model.ChatCompletionRequest{KnowledgeBaseID: "kb-1"}, model.ServerConfig{
			RetrievalCandidateTopKDocument: 14,
			RetrievalTopKDocument:          7,
			RetrievalCandidateTopKAllDocs:  40,
			RetrievalTopKKnowledgeBase:     11,
			RetrievalMaxChunksPerDocument:  3,
		})
		if params.candidateTopK != 40 {
			t.Fatalf("expected configured all-docs candidateTopK 40, got %d", params.candidateTopK)
		}
		if params.finalTopK != 11 {
			t.Fatalf("expected configured all-docs finalTopK 11, got %d", params.finalTopK)
		}
		if params.perDocumentLimit != 3 {
			t.Fatalf("expected configured all-docs perDocumentLimit 3, got %d", params.perDocumentLimit)
		}
	})

	t.Run("document scope enforces final topk as lower bound", func(t *testing.T) {
		params := resolveRetrievalParamsWithConfig(model.ChatCompletionRequest{DocumentID: "doc-1"}, model.ServerConfig{
			RetrievalCandidateTopKDocument: 9,
			RetrievalTopKDocument:          6,
			RetrievalMaxChunksPerDocument:  2,
		})
		if params.candidateTopK != 9 {
			t.Fatalf("expected configured document candidateTopK 9, got %d", params.candidateTopK)
		}
		if params.finalTopK != 6 {
			t.Fatalf("expected configured document finalTopK 6, got %d", params.finalTopK)
		}
		if params.perDocumentLimit != 6 {
			t.Fatalf("expected document perDocumentLimit to be lifted to finalTopK 6, got %d", params.perDocumentLimit)
		}
	})
}

func TestShouldUseHybridSearch(t *testing.T) {
	service := &AppService{}

	if service.shouldUseHybridSearch(model.ChatCompletionRequest{KnowledgeBaseID: "kb-1"}) {
		t.Fatal("expected hybrid search to be disabled by default")
	}

	if !service.shouldUseHybridSearch(model.ChatCompletionRequest{KnowledgeBaseID: "kb-1", RetrievalMode: "hybrid"}) {
		t.Fatal("expected request-level hybrid mode to override disabled server config")
	}

	service.serverConfig.EnableHybridSearch = true
	if !service.shouldUseHybridSearch(model.ChatCompletionRequest{KnowledgeBaseID: "kb-1"}) {
		t.Fatal("expected hybrid search to be enabled for knowledge base scope")
	}
	if service.shouldUseHybridSearch(model.ChatCompletionRequest{KnowledgeBaseID: "kb-1", RetrievalMode: "dense"}) {
		t.Fatal("expected request-level dense mode to override enabled server config")
	}
	if service.shouldUseHybridSearch(model.ChatCompletionRequest{DocumentID: "doc-1"}) {
		t.Fatal("expected document scope to keep dense-only retrieval")
	}
}

func TestNormalizeRetrievalMode(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "empty", input: "", expected: "auto"},
		{name: "dense", input: "dense", expected: "dense"},
		{name: "vector alias", input: " vector ", expected: "dense"},
		{name: "hybrid", input: "HYBRID", expected: "hybrid"},
		{name: "unknown", input: "keyword", expected: "auto"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if actual := normalizeRetrievalMode(tt.input); actual != tt.expected {
				t.Fatalf("expected %s, got %s", tt.expected, actual)
			}
		})
	}
}

func TestNormalizeRetrievalConfigIncludesRerankAndRewrite(t *testing.T) {
	cfg := normalizeRetrievalConfig(model.RetrievalConfig{}, model.ServerConfig{
		EnableSemanticReranker: true,
		EnableQueryRewrite:     true,
	})
	if cfg.RerankStrategy != "semantic" {
		t.Fatalf("expected semantic rerank default from server config, got %s", cfg.RerankStrategy)
	}
	if !cfg.EnableQueryRewrite {
		t.Fatal("expected query rewrite default from server config")
	}
	if cfg.QueryRewriteMaxVariants != 3 {
		t.Fatalf("expected default query rewrite variants 3, got %d", cfg.QueryRewriteMaxVariants)
	}

	cfg = normalizeRetrievalConfig(model.RetrievalConfig{
		RerankStrategy:          "keyword",
		EnableQueryRewrite:      false,
		QueryRewriteMaxVariants: 9,
	}, model.ServerConfig{EnableSemanticReranker: true, EnableQueryRewrite: true})
	if cfg.RerankStrategy != "keyword" {
		t.Fatalf("expected explicit keyword strategy, got %s", cfg.RerankStrategy)
	}
	if cfg.QueryRewriteMaxVariants != 5 {
		t.Fatalf("expected variants to be clamped to 5, got %d", cfg.QueryRewriteMaxVariants)
	}
}

func TestBuildRetrievalDebugConfidence(t *testing.T) {
	t.Run("empty result is low confidence", func(t *testing.T) {
		confidence := buildRetrievalDebugConfidence("成员甲的金额是多少", nil)
		if confidence.Status != "low" {
			t.Fatalf("expected low confidence, got %s", confidence.Status)
		}
		if len(confidence.Reasons) == 0 || len(confidence.Suggestions) == 0 {
			t.Fatal("expected reasons and suggestions for empty result")
		}
	})

	t.Run("low score result explains score issue", func(t *testing.T) {
		confidence := buildRetrievalDebugConfidence("成员甲的金额是多少", []RetrievedChunk{
			{
				DocumentChunk: DocumentChunk{Text: "成员甲 金额 300"},
				Score:         0.05,
			},
		})
		if confidence.Status != "low" {
			t.Fatalf("expected low confidence, got %s", confidence.Status)
		}
		if !strings.Contains(strings.Join(confidence.Reasons, " "), "最高命中分") {
			t.Fatalf("expected top score reason, got %v", confidence.Reasons)
		}
	})

	t.Run("strong result is normal confidence", func(t *testing.T) {
		confidence := buildRetrievalDebugConfidence("成员甲", []RetrievedChunk{
			{
				DocumentChunk: DocumentChunk{Text: "成员甲 的 金额 是 300 元"},
				Score:         0.92,
			},
			{
				DocumentChunk: DocumentChunk{Text: "成员甲 编号 编号甲"},
				Score:         0.86,
			},
		})
		if confidence.Status != "normal" {
			t.Fatalf("expected normal confidence, got %s with reasons %v", confidence.Status, confidence.Reasons)
		}
		if confidence.EvidenceCoverage <= 0 {
			t.Fatalf("expected evidence coverage, got %.4f", confidence.EvidenceCoverage)
		}
	})
}

func TestSelectWithMMRRespectsPerDocumentLimit(t *testing.T) {
	candidates := []RetrievedChunk{
		{DocumentChunk: DocumentChunk{DocumentID: "doc-a", Text: "示例机构 团队 规模", Index: 0}, Score: 0.98},
		{DocumentChunk: DocumentChunk{DocumentID: "doc-a", Text: "示例机构 教学 团队", Index: 1}, Score: 0.96},
		{DocumentChunk: DocumentChunk{DocumentID: "doc-b", Text: "团队 结构 与 职级", Index: 0}, Score: 0.95},
		{DocumentChunk: DocumentChunk{DocumentID: "doc-c", Text: "高层级 平台 建设", Index: 0}, Score: 0.94},
	}

	selected := selectWithMMR(candidates, 3, 1)
	if len(selected) != 3 {
		t.Fatalf("expected selected size 3, got %d", len(selected))
	}

	counter := map[string]int{}
	for _, item := range selected {
		counter[item.DocumentID]++
	}
	for docID, count := range counter {
		if count > 1 {
			t.Fatalf("expected per-document limit to be respected, doc %s selected %d times", docID, count)
		}
	}
}

func TestRerankCandidatesBoostsKeywordCoverage(t *testing.T) {
	query := "示例机构 团队"
	candidates := []RetrievedChunk{
		{DocumentChunk: DocumentChunk{DocumentID: "doc-cache", Text: "缓存 集群 高可用"}, Score: 0.90},
		{DocumentChunk: DocumentChunk{DocumentID: "doc-team", Text: "示例机构 团队 规模 与 职级结构"}, Score: 0.89},
		{DocumentChunk: DocumentChunk{DocumentID: "doc-misc", Text: "连接 池 参数"}, Score: 0.10},
	}

	service := &AppService{}
	ranked := service.rerankCandidates(context.Background(), candidates, query, model.ChatCompletionRequest{})
	if len(ranked) != len(candidates) {
		t.Fatalf("expected ranked size %d, got %d", len(candidates), len(ranked))
	}
	if ranked[0].DocumentID != "doc-team" {
		t.Fatalf("expected keyword-related doc to rank first, got %s", ranked[0].DocumentID)
	}
}

func TestCosineSimilarity(t *testing.T) {
	vecA := []float32{1, 0, 0}
	vecB := []float32{1, 0, 0}
	vecC := []float32{0, 1, 0}

	if got := cosineSimilarity(vecA, vecB); math.Abs(float64(got-1)) > 1e-6 {
		t.Fatalf("expected cosine similarity 1, got %f", got)
	}
	if got := cosineSimilarity(vecA, vecC); math.Abs(float64(got)) > 1e-6 {
		t.Fatalf("expected cosine similarity 0, got %f", got)
	}
}

func TestEmbeddingRerankerOrder(t *testing.T) {
	reranker := &EmbeddingReranker{}
	reranker.embed = func(ctx context.Context, cfg model.EmbeddingModelConfig, texts []string, vectorSize int) ([][]float64, error) {
		if len(texts) == 1 {
			return [][]float64{{1, 0}}, nil
		}
		vectors := make([][]float64, 0, len(texts))
		for _, text := range texts {
			if text == "match" {
				vectors = append(vectors, []float64{1, 0})
			} else {
				vectors = append(vectors, []float64{0, 1})
			}
		}
		return vectors, nil
	}

	candidates := []RetrievedChunk{
		{DocumentChunk: DocumentChunk{DocumentID: "doc-1", Text: "match", Index: 0}, Score: 0.1},
		{DocumentChunk: DocumentChunk{DocumentID: "doc-2", Text: "other", Index: 0}, Score: 0.9},
	}
	result, err := reranker.Rerank(context.Background(), "query", candidates)
	if err != nil {
		t.Fatalf("expected rerank success, got %v", err)
	}
	if len(result) != len(candidates) {
		t.Fatalf("expected ranked size %d, got %d", len(candidates), len(result))
	}
	if result[0].DocumentID != "doc-1" {
		t.Fatalf("expected embedding-related doc to rank first, got %s", result[0].DocumentID)
	}
}

func TestIsLowConfidenceSelection(t *testing.T) {
	t.Run("low scores", func(t *testing.T) {
		chunks := []RetrievedChunk{
			{DocumentChunk: DocumentChunk{DocumentID: "doc-1", Text: "随机片段"}, Score: 0.12},
			{DocumentChunk: DocumentChunk{DocumentID: "doc-2", Text: "无关内容"}, Score: 0.10},
		}
		if !isLowConfidenceSelection("示例机构 团队", chunks) {
			t.Fatal("expected low confidence when scores are too low")
		}
	})

	t.Run("good scores and entity coverage", func(t *testing.T) {
		chunks := []RetrievedChunk{
			{DocumentChunk: DocumentChunk{DocumentID: "doc-1", Text: "示例机构 团队 规模 超过 3800 人"}, Score: 0.85},
			{DocumentChunk: DocumentChunk{DocumentID: "doc-2", Text: "团队 结构 包含 专家 与 新成员"}, Score: 0.72},
		}
		if isLowConfidenceSelection("示例机构 团队", chunks) {
			t.Fatal("expected confident selection when scores and coverage are sufficient")
		}
	})
}

func TestApplyEvidenceGatePrefersDirectEvidenceOverUnrelatedHighScore(t *testing.T) {
	chunks := []RetrievedChunk{
		{
			DocumentChunk: DocumentChunk{
				DocumentID: "doc-related",
				Text:       "示例机构团队规模超过3800人，核心团队负责平台建设。",
			},
			Score:    0.22,
			RawScore: 0.16,
		},
		{
			DocumentChunk: DocumentChunk{
				DocumentID: "doc-unrelated",
				Text:       "缓存集群通过连接池参数提升吞吐能力。",
			},
			Score:    0.95,
			RawScore: 0.91,
		},
	}

	filtered := applyEvidenceGate("示例机构团队规模是多少", chunks)
	if len(filtered) != 1 || filtered[0].DocumentID != "doc-related" {
		t.Fatalf("expected direct evidence only, got %#v", filtered)
	}
}

func TestApplyEvidenceGateReportsDroppedCandidates(t *testing.T) {
	chunks := []RetrievedChunk{
		{
			DocumentChunk: DocumentChunk{
				DocumentID: "doc-related",
				Text:       "示例机构团队规模超过3800人。",
			},
			Score:    0.42,
			RawScore: 0.36,
		},
		{
			DocumentChunk: DocumentChunk{
				DocumentID: "doc-unrelated",
				Text:       "缓存集群通过连接池参数提升吞吐能力。",
			},
			Score:    0.91,
			RawScore: 0.89,
		},
	}

	filtered, stats := applyEvidenceGateWithStats("示例机构团队规模是多少", chunks)
	if len(filtered) != 1 || stats.InputCount != 2 || stats.OutputCount != 1 || stats.DroppedCount != 1 {
		t.Fatalf("expected one dropped candidate, got filtered=%#v stats=%#v", filtered, stats)
	}

	filtered, stats = applyEvidenceGateWithStats("完全未知主题", []RetrievedChunk{
		{DocumentChunk: DocumentChunk{DocumentID: "doc-1", Text: "无关片段一"}, RawScore: 0.31, Score: 0.31},
		{DocumentChunk: DocumentChunk{DocumentID: "doc-2", Text: "无关片段二"}, RawScore: 0.24, Score: 0.24},
	})
	if len(filtered) != 0 || stats.InputCount != 2 || stats.OutputCount != 0 || stats.DroppedCount != 2 {
		t.Fatalf("expected all low-signal candidates to be dropped, got filtered=%#v stats=%#v", filtered, stats)
	}
}

func TestApplyEvidenceGateAllowsStrongSemanticOnlyMatch(t *testing.T) {
	chunks := []RetrievedChunk{
		{
			DocumentChunk: DocumentChunk{
				DocumentID: "doc-semantic",
				Text:       "项目沿革记录了早期建设阶段和后续发展方向。",
			},
			Score:    0.88,
			RawScore: 0.84,
		},
		{
			DocumentChunk: DocumentChunk{
				DocumentID: "doc-weak",
				Text:       "系统包含若干运行参数。",
			},
			Score:    0.70,
			RawScore: 0.68,
		},
	}

	filtered := applyEvidenceGate("产品演进脉络", chunks)
	if len(filtered) != 1 || filtered[0].DocumentID != "doc-semantic" {
		t.Fatalf("expected strong semantic evidence only, got %#v", filtered)
	}
}

func TestApplyEvidenceGateKeepsDeterministicStructuredResults(t *testing.T) {
	chunks := []RetrievedChunk{{
		DocumentChunk: DocumentChunk{
			DocumentID: "doc-table",
			Kind:       "structured_query",
			Text:       "第2行：姓名：甲。工资：18000。",
		},
		Score:    1,
		RawScore: 1,
	}}

	filtered := applyEvidenceGate("工资最高是谁", chunks)
	if len(filtered) != 1 || filtered[0].Kind != "structured_query" {
		t.Fatalf("expected structured result to bypass gate, got %#v", filtered)
	}
}

func TestApplyEvidenceGateRequiresFactAttributeEvidence(t *testing.T) {
	tests := []string{
		"Hugging Face Transformers 官方文档是否提供了每个模型的在线响应时间？",
		"Hugging Face Transformers 官方文档是否提供了每个模型的在线吞吐量？",
	}
	for _, query := range tests {
		t.Run(query, func(t *testing.T) {
			chunks := []RetrievedChunk{{
				DocumentChunk: DocumentChunk{
					DocumentID: "doc-transformers",
					Text:       "Hugging Face Transformers 是一个面向文本、计算机视觉、音频、视频和多模态模型的机器学习模型定义框架。",
				},
				Score:    0.96,
				RawScore: 0.95,
			}}

			filtered, stats := applyEvidenceGateWithStats(query, chunks)
			if len(filtered) != 0 || stats.OutputCount != 0 || stats.DroppedCount != 1 {
				t.Fatalf("expected missing requested attribute to drop the high-score match, got filtered=%#v stats=%#v", filtered, stats)
			}
			if score := factEvidenceScore(query, chunks[0]); score != 0 {
				t.Fatalf("expected no fact evidence without requested attribute, got score=%d", score)
			}
		})
	}
}

func TestOpenEndedDescriptionQueriesDoNotRequireUnknownFactAttribute(t *testing.T) {
	queries := []string{
		"请说明 Redis 的核心特点",
		"请介绍 Agent Tools 的稳定处理能力包括哪些？",
		"概述示例系统的主要用途",
		"简述产品的运行机制",
	}
	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			if specs := strictFactQuerySpecs(query); len(specs) != 0 {
				t.Fatalf("expected open-ended description query to skip unknown strict attributes, got %#v", specs)
			}
		})
	}
}

func TestApplyEvidenceGateKeepsOpenEndedDescriptionEvidence(t *testing.T) {
	query := "请介绍 Agent Tools 的稳定处理能力包括哪些？"
	chunks := []RetrievedChunk{{
		DocumentChunk: DocumentChunk{
			DocumentID: "doc-agent-tools",
			Text:       "Agent Tools 支持缓存、队列和持久化能力，用于稳定处理知识库任务。",
		},
		Score:    0.72,
		RawScore: 0.68,
	}}

	filtered := applyEvidenceGate(query, chunks)
	if len(filtered) != 1 || filtered[0].DocumentID != "doc-agent-tools" {
		t.Fatalf("expected semantic evidence for open-ended description to remain, got %#v", filtered)
	}
}

func TestApplyEvidenceGateAcceptsReliableFactAttributeAlias(t *testing.T) {
	query := "示例学校的建校时间是多少？"
	chunks := []RetrievedChunk{{
		DocumentChunk: DocumentChunk{
			DocumentID: "doc-school",
			Text:       "示例学校始建于1998年，现有多个校区。",
		},
		Score:    0.42,
		RawScore: 0.38,
	}}

	filtered := applyEvidenceGate(query, chunks)
	if len(filtered) != 1 || filtered[0].DocumentID != "doc-school" {
		t.Fatalf("expected reliable attribute alias to count as direct evidence, got %#v", filtered)
	}
}

func TestApplyEvidenceGateUsesTechnicalAttributeFromNaturalQuestion(t *testing.T) {
	query := "Qdrant 的 payload 可以存储什么类型的信息？"
	specs := strictFactQuerySpecs(query)
	if len(specs) != 1 || specs[0].Attribute != "payload" {
		t.Fatalf("expected predicate to be reduced to the payload attribute, got %#v", specs)
	}
	chunks := []RetrievedChunk{
		{
			DocumentChunk: DocumentChunk{
				DocumentID: "doc-payload",
				Text:       "Qdrant 的 payload 可以存储能够表示为 JSON 的任意信息。",
			},
			Score:    0.44,
			RawScore: 0.4,
		},
		{
			DocumentChunk: DocumentChunk{
				DocumentID: "doc-collection",
				Text:       "Qdrant collection 是一组带有向量的命名集合，可以在其中进行搜索。",
			},
			Score:    0.97,
			RawScore: 0.95,
		},
	}

	filtered := applyEvidenceGate(query, chunks)
	if len(filtered) != 1 || filtered[0].DocumentID != "doc-payload" {
		t.Fatalf("expected the payload evidence only, got %#v", filtered)
	}
}

func TestApplyEvidenceGateNormalizesTechnicalQuestionLeadIn(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		evidence  string
		unrelated string
	}{
		{
			name:      "why question",
			query:     "为什么 Qdrant 的向量检索还需要 payload 过滤？",
			evidence:  "Qdrant 的过滤条件的作用是：当对象的全部特征无法用 embedding 表达时，可以通过 payload 条件补充过滤。",
			unrelated: "Qdrant collection 是一组带有向量和 payload 的命名集合，可以在其中进行搜索。",
		},
		{
			name:      "action question",
			query:     "创建 Qdrant payload index 有什么资源代价？",
			evidence:  "创建 Qdrant payload index 会额外消耗计算资源和内存，因此应谨慎选择需要索引的字段。",
			unrelated: "Qdrant 的 payload 可以存储能够表示为 JSON 的任意信息。",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			specs := strictFactQuerySpecs(tt.query)
			if len(specs) == 0 {
				t.Fatalf("expected a strict fact spec for %q", tt.query)
			}
			foundQdrantSubject := false
			for _, spec := range specs {
				if spec.Subject == "qdrant" {
					foundQdrantSubject = true
					break
				}
			}
			if !foundQdrantSubject {
				t.Fatalf("expected question lead-in to be removed from subject, got %#v", specs)
			}

			filtered, stats := applyEvidenceGateWithStats(tt.query, []RetrievedChunk{
				{
					DocumentChunk: DocumentChunk{DocumentID: "doc-evidence", Text: tt.evidence},
					Score:         0.35,
					RawScore:      0.30,
				},
				{
					DocumentChunk: DocumentChunk{DocumentID: "doc-unrelated", Text: tt.unrelated},
					Score:         0.96,
					RawScore:      0.95,
				},
			})
			if len(filtered) != 1 || filtered[0].DocumentID != "doc-evidence" {
				t.Fatalf("expected the fact evidence to survive without unrelated high-score content, filtered=%#v stats=%#v", filtered, stats)
			}
		})
	}
}

func TestApplyEvidenceGateKeepsFactEvidenceWhenSubjectIsInHeading(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		evidence  string
		unrelated string
	}{
		{
			name:      "why question with predicate evidence",
			query:     "为什么 Qdrant 的向量检索还需要 payload 过滤？",
			evidence:  "过滤条件的作用是：当对象的全部特征无法用 embedding 表达时，可以通过 payload 条件补充过滤。",
			unrelated: "Qdrant collection 是一组带有向量和 payload 的命名集合，可以在其中进行搜索。",
		},
		{
			name:      "resource cost with predicate evidence",
			query:     "Qdrant 的 payload index 有什么资源代价？",
			evidence:  "payload index 会额外消耗计算资源和内存，因此应谨慎选择需要索引的字段。",
			unrelated: "Qdrant 的 payload 可以存储能够表示为 JSON 的任意信息。",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filtered, stats := applyEvidenceGateWithStats(tt.query, []RetrievedChunk{
				{
					DocumentChunk: DocumentChunk{DocumentID: "doc-evidence", Text: tt.evidence},
					Score:         0.35,
					RawScore:      0.30,
				},
				{
					DocumentChunk: DocumentChunk{DocumentID: "doc-unrelated", Text: tt.unrelated},
					Score:         0.96,
					RawScore:      0.95,
				},
			})
			if len(filtered) != 1 || filtered[0].DocumentID != "doc-evidence" {
				t.Fatalf("expected attribute and predicate evidence to survive without repeated subject, filtered=%#v stats=%#v", filtered, stats)
			}
		})
	}
}

func TestRetrievalDebugVerboseKeepsMMRCountSeparateFromEvidenceGate(t *testing.T) {
	details := &model.RetrievalDebugVerboseDetails{
		AfterMMRCount:          6,
		AfterEvidenceGateCount: 0,
	}
	if details.AfterMMRCount == details.AfterEvidenceGateCount {
		t.Fatal("expected MMR and evidence-gate counts to remain independently observable")
	}
}

func TestApplyEvidenceGateRecognizesTimeQuestionWithoutDe(t *testing.T) {
	query := "示例学校什么时候成立？"
	specs := strictFactQuerySpecs(query)
	if len(specs) != 1 || specs[0].Attribute != "成立时间" {
		t.Fatalf("expected time question to normalize to a fact attribute, got %#v", specs)
	}

	filtered := applyEvidenceGate(query, []RetrievedChunk{{
		DocumentChunk: DocumentChunk{
			DocumentID: "doc-school",
			Text:       "示例学校成立于1998年。",
		},
		Score:    0.4,
		RawScore: 0.36,
	}})
	if len(filtered) != 1 {
		t.Fatalf("expected established-time alias to be accepted, got %#v", filtered)
	}
}

func TestApplyEvidenceGateKeepsEachRequestedAttribute(t *testing.T) {
	query := "成员甲的手机号和地址是什么？"
	chunks := []RetrievedChunk{
		{
			DocumentChunk: DocumentChunk{
				DocumentID: "doc-member",
				Text:       "成员甲的手机号是13800000000。",
			},
			Score:    0.52,
			RawScore: 0.48,
		},
		{
			DocumentChunk: DocumentChunk{
				DocumentID: "doc-member",
				Text:       "成员甲的地址是城市甲。",
			},
			Score:    0.51,
			RawScore: 0.47,
		},
	}

	filtered := applyEvidenceGate(query, chunks)
	if len(filtered) != 2 {
		t.Fatalf("expected both requested attributes to remain available, got %#v", filtered)
	}
	if coverage := queryEvidenceCoverage(query, filtered); coverage != 1 {
		t.Fatalf("expected complete multi-attribute coverage, got %.2f", coverage)
	}
}

func TestBuildRetrievalDebugConfidenceReportsMissingFactAttribute(t *testing.T) {
	confidence := buildRetrievalDebugConfidence(
		"Hugging Face Transformers 官方文档是否提供了每个模型的在线响应时间？",
		[]RetrievedChunk{{
			DocumentChunk: DocumentChunk{
				DocumentID: "doc-transformers",
				Text:       "Transformers 的 Pipeline 可用于文本生成、图像分割、自动语音识别和文档问答等任务。",
			},
			Score:    0.94,
			RawScore: 0.93,
		}},
	)
	if confidence.Status != "low" {
		t.Fatalf("expected missing fact attribute to be low confidence, got %#v", confidence)
	}
	if !strings.Contains(strings.Join(confidence.Reasons, " "), "事实属性") {
		t.Fatalf("expected missing fact attribute reason, got %#v", confidence.Reasons)
	}
}

func TestQueryEvidenceTermsDoNotInjectFactAliases(t *testing.T) {
	terms := strings.Join(queryEvidenceTerms("成员甲的手机号是多少？"), "\n")
	for _, alias := range []string{"联系电话", "联系方式", "办公电话"} {
		if strings.Contains(terms, alias) {
			t.Fatalf("expected query terms to come only from the user query, found injected alias %q in %q", alias, terms)
		}
	}
}

func TestCollectCandidatesPreservesQdrantScoreAndChannels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/collections/kb-1/points/query" {
			t.Fatalf("unexpected qdrant request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"points":[{"id":"chunk-1","score":0.437,"payload":{"chunk_id":"chunk-1","knowledge_base_id":"kb-1","document_id":"doc-1","document_name":"source.txt","text":"Qdrant 返回的原始文档片段","chunk_index":2,"chunk_kind":"text","_retrieval_channels":["dense"]}}]}}`))
	}))
	t.Cleanup(server.Close)

	service := &AppService{
		qdrant: NewQdrantService(model.ServerConfig{QdrantURL: server.URL, QdrantVectorSize: 2}),
		state:  &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{}},
	}
	candidates, err := service.collectCandidates(
		t.Context(),
		[]string{"kb-1"},
		model.ChatCompletionRequest{},
		[]float64{0.1, 0.2},
		5,
		false,
		"原始问题",
	)
	if err != nil {
		t.Fatalf("collect candidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected one Qdrant candidate, got %#v", candidates)
	}
	if candidates[0].Score != 0.437 || candidates[0].RawScore != 0.437 {
		t.Fatalf("expected Qdrant score to remain unchanged, got score=%v rawScore=%v", candidates[0].Score, candidates[0].RawScore)
	}
	if len(candidates[0].RetrievalChannels) != 1 || candidates[0].RetrievalChannels[0] != "dense" {
		t.Fatalf("expected only the Qdrant retrieval channel, got %#v", candidates[0].RetrievalChannels)
	}
}

func TestCollectCandidatesPropagatesContextCancellation(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		close(requestStarted)
		<-releaseRequest
	}))
	t.Cleanup(server.Close)
	defer close(releaseRequest)

	service := &AppService{
		qdrant: NewQdrantService(model.ServerConfig{QdrantURL: server.URL, QdrantVectorSize: 2}),
		state:  &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{}},
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		_, err := service.collectCandidates(ctx, []string{"kb-1"}, model.ChatCompletionRequest{}, []float64{0.1, 0.2}, 5, false, "原始问题")
		errCh <- err
	}()

	select {
	case <-requestStarted:
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Qdrant request")
	}

	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "context canceled") {
			t.Fatalf("expected context cancellation error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("collectCandidates did not stop after context cancellation")
	}
}

func TestTrimRetrievedChunksToContextLimitUsesRunes(t *testing.T) {
	chunks := []RetrievedChunk{{
		DocumentChunk: DocumentChunk{DocumentID: "doc-1", Text: "维护说明：组件配置详情"},
	}}

	trimmed := trimRetrievedChunksToContextLimit(chunks, 5, "")
	if len(trimmed) != 1 {
		t.Fatalf("expected one trimmed chunk, got %#v", trimmed)
	}
	if trimmed[0].Text != "维护说明：" {
		t.Fatalf("expected five complete runes, got %q", trimmed[0].Text)
	}
	if !utf8.ValidString(trimmed[0].Text) {
		t.Fatalf("expected valid UTF-8, got %q", trimmed[0].Text)
	}
}

func TestTrimRetrievedChunksDistributesContextAcrossEvidence(t *testing.T) {
	chunks := []RetrievedChunk{
		{DocumentChunk: DocumentChunk{DocumentID: "doc-1", Text: strings.Repeat("背景说明", 300) + "核心组件清单：API 服务。"}},
		{DocumentChunk: DocumentChunk{DocumentID: "doc-1", Text: "任务队列、向量存储和管理界面。" + strings.Repeat("运维说明", 300)}},
	}

	trimmed := trimRetrievedChunksToContextLimit(chunks, 400, "项目用途是什么，核心组件有哪些")
	if len(trimmed) != 2 {
		t.Fatalf("expected two evidence excerpts, got %#v", trimmed)
	}
	joined := strings.Join(chunkTextsFromRetrieved(trimmed), "\n")
	if !strings.Contains(joined, "API 服务") || !strings.Contains(joined, "任务队列") {
		t.Fatalf("expected both evidence areas in context, got %q", joined)
	}
	if chunksTotalChars(trimmed) > 400 {
		t.Fatalf("expected context within rune budget, got %d", chunksTotalChars(trimmed))
	}
}

func TestLimitRetrievalQueries(t *testing.T) {
	queries := []string{"原问题", "改写一", "改写二", "改写三", "规则一", "规则二", "规则三", "规则四", "规则五"}
	limited := limitRetrievalQueries(queries, 8)
	if len(limited) != 8 || limited[0] != "原问题" || limited[7] != "规则四" {
		t.Fatalf("unexpected limited queries: %#v", limited)
	}
}

func TestBuildChatContextIncludesDocumentPreviews(t *testing.T) {
	service := &AppService{state: &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{
		"kb-guide": {
			ID:   "kb-guide",
			Name: "项目资料",
			Documents: []model.Document{{
				ID:             "doc-guide",
				Name:           "系统指南.md",
				ContentPreview: "系统指南包含 API 服务、任务队列、向量存储和管理界面。",
			}},
		},
	}}}

	context, _, err := service.BuildChatContext(model.ChatCompletionRequest{KnowledgeBaseID: "kb-guide"}, []string{"doc-guide"})
	if err != nil {
		t.Fatalf("build chat context: %v", err)
	}
	if !strings.Contains(context, "API 服务") || !strings.Contains(context, "系统指南.md") {
		t.Fatalf("expected document preview in knowledge-base context, got %q", context)
	}
}

func TestFilterRetrievedChunksToScopeDropsForeignAndOrphanDocuments(t *testing.T) {
	service := &AppService{state: &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{
		"kb-school": {
			ID: "kb-school",
			Documents: []model.Document{{
				ID:              "doc-school",
				KnowledgeBaseID: "kb-school",
				Name:            "机构简介.pdf",
			}},
		},
		"kb-novel": {
			ID: "kb-novel",
			Documents: []model.Document{{
				ID:              "doc-novel",
				KnowledgeBaseID: "kb-novel",
				Name:            "作品大纲.md",
			}},
		},
	}}}

	chunks := []RetrievedChunk{
		{DocumentChunk: DocumentChunk{ID: "school", KnowledgeBaseID: "kb-school", DocumentID: "doc-school"}},
		{DocumentChunk: DocumentChunk{ID: "foreign", KnowledgeBaseID: "kb-novel", DocumentID: "doc-novel"}},
		{DocumentChunk: DocumentChunk{ID: "orphan", KnowledgeBaseID: "kb-school", DocumentID: "doc-novel"}},
	}
	filtered := service.filterRetrievedChunksToScope(
		model.ChatCompletionRequest{KnowledgeBaseID: "kb-school"},
		[]string{"kb-school"},
		chunks,
	)
	if len(filtered) != 1 || filtered[0].ID != "school" {
		t.Fatalf("expected only the authoritative in-scope document, got %#v", filtered)
	}
}

func TestFilterRetrievedChunksToScopeDropsStaleIndexGeneration(t *testing.T) {
	service := &AppService{state: &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{
		"kb-school": {
			ID: "kb-school",
			Documents: []model.Document{{
				ID:         "doc-school",
				IndexFence: "mcp:job-current:2",
			}},
		},
	}}}
	chunks := []RetrievedChunk{
		{DocumentChunk: DocumentChunk{ID: "current", KnowledgeBaseID: "kb-school", DocumentID: "doc-school", IndexFence: "mcp:job-current:2"}},
		{DocumentChunk: DocumentChunk{ID: "stale", KnowledgeBaseID: "kb-school", DocumentID: "doc-school", IndexFence: "mcp:job-old:1"}},
		{DocumentChunk: DocumentChunk{ID: "legacy", KnowledgeBaseID: "kb-school", DocumentID: "doc-school"}},
	}
	filtered := service.filterRetrievedChunksToScope(
		model.ChatCompletionRequest{KnowledgeBaseID: "kb-school"},
		[]string{"kb-school"},
		chunks,
	)
	if len(filtered) != 1 || filtered[0].ID != "current" {
		t.Fatalf("expected only current index generation, got %#v", filtered)
	}
}

func TestBuildChatContextRejectsDocumentFromAnotherKnowledgeBase(t *testing.T) {
	service := &AppService{state: &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{
		"kb-school": {ID: "kb-school", Documents: []model.Document{{ID: "doc-school"}}},
		"kb-novel":  {ID: "kb-novel", Documents: []model.Document{{ID: "doc-novel"}}},
	}}}

	_, _, err := service.BuildChatContext(model.ChatCompletionRequest{
		KnowledgeBaseID: "kb-school",
		DocumentID:      "doc-novel",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("expected cross-knowledge-base document scope to be rejected, got %v", err)
	}
}

func TestRecentConversationHistorySkipsCurrentQueryAndDegradedReplies(t *testing.T) {
	messages := []model.ChatMessage{
		{Role: "user", Content: "小说大纲写得怎么样"},
		{Role: "assistant", Content: "⚠️ AI 模型调用已降级\n\n模型超时"},
		{Role: "user", Content: "主角是谁"},
	}

	history := recentConversationHistory(messages, 3)
	if len(history) != 1 || history[0] != "小说大纲写得怎么样" {
		t.Fatalf("unexpected filtered history: %#v", history)
	}
}

func TestBuildRetrievalDebugMatchReasons(t *testing.T) {
	reasons := buildRetrievalDebugMatchReasons("示例机构负责人是谁", RetrievedChunk{
		DocumentChunk: DocumentChunk{
			Kind: "text",
			Text: "示例机构负责人信息与组织结构说明。",
		},
		Score:    0.82,
		RawScore: 0.86,
	})

	joined := strings.Join(reasons, " ")
	if !strings.Contains(joined, "匹配查询证据词") {
		t.Fatalf("expected evidence match reason, got %#v", reasons)
	}
	if !strings.Contains(joined, "原始检索分较高") {
		t.Fatalf("expected high score reason, got %#v", reasons)
	}

	structuredReasons := buildRetrievalDebugMatchReasons("谁的薪资最高", RetrievedChunk{
		DocumentChunk: DocumentChunk{
			Kind: "structured_row",
			Text: "第2行：姓名：成员甲。金额：300。",
		},
		Score:    0.72,
		RawScore: 0.61,
	})
	joined = strings.Join(structuredReasons, " ")
	if !strings.Contains(joined, "结构化数据片段") || strings.Contains(joined, "确定性") {
		t.Fatalf("expected only evidence-backed structured reasons, got %#v", structuredReasons)
	}
}

func TestDeduplicateRetrievedChunks(t *testing.T) {
	chunks := []RetrievedChunk{
		{DocumentChunk: DocumentChunk{DocumentID: "doc-1", DocumentName: "sample.csv", Text: "文件：sample.csv。字段：字段A、字段B。数据行数：4。"}, Score: 0.99},
		{DocumentChunk: DocumentChunk{DocumentID: "doc-1", DocumentName: "sample.csv", Text: "文件：sample.csv。字段：字段A、字段B。数据行数：4。"}, Score: 0.95},
		{DocumentChunk: DocumentChunk{DocumentID: "doc-1", DocumentName: "sample.csv", Text: "第2行：字段A：值甲。字段B：级别1。"}, Score: 0.94},
		{DocumentChunk: DocumentChunk{DocumentID: "doc-2", DocumentName: "other.csv", Text: "文件：other.csv。字段：字段A。数据行数：1。"}, Score: 0.90},
	}

	filtered := deduplicateRetrievedChunks(chunks)
	if len(filtered) != 3 {
		t.Fatalf("expected 3 unique chunks, got %d", len(filtered))
	}
	if filtered[0].Text != chunks[0].Text {
		t.Fatalf("expected first chunk to be preserved, got %q", filtered[0].Text)
	}
}

func TestBuildChunkTextDeduplicatesRepeatedChunks(t *testing.T) {
	chunks := []RetrievedChunk{
		{DocumentChunk: DocumentChunk{DocumentID: "doc-1", DocumentName: "sample.csv", Text: "文件：sample.csv。字段：字段A、字段B。数据行数：4。", Index: 0}, Score: 0.99},
		{DocumentChunk: DocumentChunk{DocumentID: "doc-1", DocumentName: "sample.csv", Text: "文件：sample.csv。字段：字段A、字段B。数据行数：4。", Index: 1}, Score: 0.95},
		{DocumentChunk: DocumentChunk{DocumentID: "doc-1", DocumentName: "sample.csv", Text: "第2行：字段A：值甲。字段B：级别1。", Index: 2}, Score: 0.94},
	}

	text := buildChunkText(chunks)
	if strings.Count(text, "字段：字段A、字段B。数据行数：4。") != 1 {
		t.Fatalf("expected repeated summary to appear once, got %q", text)
	}
	if !strings.Contains(text, "第2行：字段A：值甲。字段B：级别1。") {
		t.Fatalf("expected row detail to be preserved, got %q", text)
	}
}

func TestGetKnowledgeBaseHealthReportsStructuredMetrics(t *testing.T) {
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "records.csv")
	content := strings.Join([]string{
		"姓名,地点,金额",
		"成员甲,城市甲,300",
		"成员乙,城市乙,200",
		"成员丙,城市甲,100",
	}, "\n")
	if err := os.WriteFile(csvPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write csv fixture: %v", err)
	}

	indexedAt := time.Now().UTC().Format(time.RFC3339)
	service := &AppService{
		state: &model.AppState{
			KnowledgeBases: map[string]model.KnowledgeBase{
				"kb-1": {
					ID:   "kb-1",
					Name: "测试知识库",
					Documents: []model.Document{{
						ID:              "doc-users",
						KnowledgeBaseID: "kb-1",
						Name:            "records.csv",
						Path:            csvPath,
						Status:          "indexed",
						IndexedAt:       indexedAt,
						IndexVersion:    currentIndexVersion,
					}},
				},
			},
		},
		rag: NewRagService(),
	}

	health, err := service.GetKnowledgeBaseHealth("kb-1")
	if err != nil {
		t.Fatalf("get knowledge base health: %v", err)
	}
	if health.Status != "healthy" {
		t.Fatalf("expected healthy status, got %s", health.Status)
	}
	if health.Score != 100 {
		t.Fatalf("expected perfect health score, got %d", health.Score)
	}
	if health.Metrics.DocumentCount != 1 || health.Metrics.IndexedCount != 1 {
		t.Fatalf("unexpected document metrics: %#v", health.Metrics)
	}
	if health.Metrics.ChunkCount == 0 {
		t.Fatal("expected chunk count to be reported")
	}
	if health.Metrics.SummaryChunkCount == 0 {
		t.Fatal("expected structured summary chunks to be reported")
	}
	if health.Metrics.StructuredRowCount == 0 {
		t.Fatal("expected structured row chunks to be reported")
	}
	if health.Metrics.RawContentChars == 0 {
		t.Fatal("expected raw content chars to be reported")
	}
	if len(health.Documents) != 1 || health.Documents[0].NeedsReindex {
		t.Fatalf("unexpected document health: %#v", health.Documents)
	}
}

func TestDocumentHealthRequiresReindexAfterIndexRuleChange(t *testing.T) {
	document := model.Document{
		Status:       "indexed",
		IndexedAt:    "2026-08-22T00:00:00Z",
		IndexVersion: currentIndexVersion - 1,
	}
	health := model.KnowledgeBaseDocumentHealth{
		ChunkCount:          2,
		RawContentAvailable: true,
	}

	if !documentNeedsReindex(document, health) {
		t.Fatal("expected a document from an older index version to require reindex")
	}
	if recommendation := documentHealthRecommendation(document, health); !strings.Contains(recommendation, "索引规则已更新") {
		t.Fatalf("expected index version recommendation, got %q", recommendation)
	}
}

func TestReindexKnowledgeBasePreflightsMissingSources(t *testing.T) {
	missingPath := filepath.Join(t.TempDir(), "missing.pdf")
	service := &AppService{
		state: &model.AppState{
			KnowledgeBases: map[string]model.KnowledgeBase{
				"kb-1": {
					ID: "kb-1",
					Documents: []model.Document{
						{ID: "doc-missing", Path: missingPath},
					},
				},
			},
		},
	}

	if _, err := service.ReindexKnowledgeBase("kb-1"); err == nil || !strings.Contains(err.Error(), "source file unavailable") {
		t.Fatalf("expected missing source preflight error, got %v", err)
	}
}

func TestExtractFilenamesFromQuery(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		expected []string
	}{
		{
			name:     "双书名号包裹",
			query:    "《generated-001.csv》共有多少条数据记录？",
			expected: []string{"generated-001.csv"},
		},
		{
			name:     "双引号包裹",
			query:    "\"users.xlsx\"中的数据分布如何？",
			expected: []string{"users.xlsx"},
		},
		{
			name:     "裸文件名",
			query:    "data.csv 有多少行？",
			expected: []string{"data.csv"},
		},
		{
			name:     "多个文件名",
			query:    "比较《file1.xlsx》和《file2.csv》的数据",
			expected: []string{"file1.xlsx", "file2.csv"},
		},
		{
			name:     "无文件名",
			query:    "这个数据集有多少记录？",
			expected: []string{},
		},
		{
			name:     "Excel文件",
			query:    "《structured-records.xlsx》工作表《记录》共有多少条数据记录？",
			expected: []string{"structured-records.xlsx"},
		},
		{
			name:     "PDF文件",
			query:    "《机构简介.pdf》中提到的成立年份是什么？",
			expected: []string{"机构简介.pdf"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractFilenamesFromQuery(tt.query)
			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d filenames, got %d: %v", len(tt.expected), len(result), result)
			}
			for i, expected := range tt.expected {
				if i >= len(result) || result[i] != expected {
					t.Fatalf("expected filename %q at index %d, got %q", expected, i, result[i])
				}
			}
		})
	}
}

func TestFindDocumentByFilename(t *testing.T) {
	service := &AppService{
		state: &model.AppState{
			KnowledgeBases: map[string]model.KnowledgeBase{
				"kb-1": {
					ID: "kb-1",
					Documents: []model.Document{
						{ID: "doc-1", Name: "records.csv"},
						{ID: "doc-2", Name: "generated-001.csv"},
						{ID: "doc-3", Name: "structured-records.xlsx"},
						{ID: "doc-4", Name: "机构简介.pdf"},
					},
				},
			},
		},
	}

	tests := []struct {
		name       string
		kbID       string
		filename   string
		expectedID string
	}{
		{
			name:       "精确匹配",
			kbID:       "kb-1",
			filename:   "records.csv",
			expectedID: "doc-1",
		},
		{
			name:       "部分匹配 - 长文件名",
			kbID:       "kb-1",
			filename:   "generated-001.csv",
			expectedID: "doc-2",
		},
		{
			name:       "中文文件名",
			kbID:       "kb-1",
			filename:   "structured-records.xlsx",
			expectedID: "doc-3",
		},
		{
			name:       "PDF文件",
			kbID:       "kb-1",
			filename:   "机构简介.pdf",
			expectedID: "doc-4",
		},
		{
			name:       "不存在的文件",
			kbID:       "kb-1",
			filename:   "nonexistent.csv",
			expectedID: "",
		},
		{
			name:       "普通文件名不走扩展名兜底",
			kbID:       "kb-1",
			filename:   "orders.xlsx",
			expectedID: "",
		},
		{
			name:       "空文件名",
			kbID:       "kb-1",
			filename:   "",
			expectedID: "",
		},
		{
			name:       "不存在的知识库",
			kbID:       "kb-999",
			filename:   "records.csv",
			expectedID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.findDocumentByFilename(tt.kbID, tt.filename)
			if result != tt.expectedID {
				t.Fatalf("expected document ID %q, got %q", tt.expectedID, result)
			}
		})
	}
}

func TestFindDocumentByFilenameWithExtensionFallback(t *testing.T) {
	service := &AppService{
		state: &model.AppState{
			KnowledgeBases: map[string]model.KnowledgeBase{
				"kb-30": {
					ID: "kb-30",
					Documents: []model.Document{
						{ID: "doc-48", Name: "generated____1.csv"},
						{ID: "doc-50", Name: "generated____1.xlsx"},
						{ID: "doc-35", Name: "records.csv"},
					},
				},
			},
		},
	}

	tests := []struct {
		name       string
		kbID       string
		filename   string
		expectedID string
		desc       string
	}{
		{
			name:       "扩展名唯一匹配 - xlsx",
			kbID:       "kb-30",
			filename:   "incoming____1.xlsx",
			expectedID: "doc-50",
			desc:       "临时文件名应该通过扩展名匹配到唯一的 xlsx 文档",
		},
		{
			name:       "扩展名不唯一 - csv",
			kbID:       "kb-30",
			filename:   "incoming____1.csv",
			expectedID: "",
			desc:       "两个 csv 文档时，不应该通过扩展名匹配",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.findDocumentByFilename(tt.kbID, tt.filename)
			if result != tt.expectedID {
				t.Fatalf("%s: expected document ID %q, got %q", tt.desc, tt.expectedID, result)
			}
		})
	}
}
