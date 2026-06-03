package service

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-localbase/internal/model"
)

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
		confidence := buildRetrievalDebugConfidence("张三的薪资是多少", nil, false)
		if confidence.Status != "low" {
			t.Fatalf("expected low confidence, got %s", confidence.Status)
		}
		if len(confidence.Reasons) == 0 || len(confidence.Suggestions) == 0 {
			t.Fatal("expected reasons and suggestions for empty result")
		}
	})

	t.Run("low score result explains score issue", func(t *testing.T) {
		confidence := buildRetrievalDebugConfidence("张三的薪资是多少", []RetrievedChunk{
			{
				DocumentChunk: DocumentChunk{Text: "张三 薪资 24000"},
				Score:         0.05,
			},
		}, false)
		if confidence.Status != "low" {
			t.Fatalf("expected low confidence, got %s", confidence.Status)
		}
		if !strings.Contains(strings.Join(confidence.Reasons, " "), "最高命中分") {
			t.Fatalf("expected top score reason, got %v", confidence.Reasons)
		}
	})

	t.Run("strong result is normal confidence", func(t *testing.T) {
		confidence := buildRetrievalDebugConfidence("张三", []RetrievedChunk{
			{
				DocumentChunk: DocumentChunk{Text: "张三 的 薪资 是 24000 元"},
				Score:         0.92,
			},
			{
				DocumentChunk: DocumentChunk{Text: "张三 教师编号 111222333111"},
				Score:         0.86,
			},
		}, false)
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

func TestFilterRelevantChunksRemovesUnrelatedHits(t *testing.T) {
	chunks := []RetrievedChunk{
		{
			DocumentChunk: DocumentChunk{
				DocumentID:   "doc-unrelated",
				DocumentName: "unrelated.txt",
				Text:         "这是一段关于部署参数、缓存策略和服务端口的文档。",
			},
			Score:    0.94,
			RawScore: 0.61,
		},
		{
			DocumentChunk: DocumentChunk{
				DocumentID:   "doc-related",
				DocumentName: "school.txt",
				Text:         "武汉大学校长信息与学校治理结构说明。",
			},
			Score:    0.82,
			RawScore: 0.58,
		},
	}

	filtered := filterRelevantChunks("武汉大学校长是谁", chunks)
	if len(filtered) != 1 {
		t.Fatalf("expected one relevant chunk, got %#v", filtered)
	}
	if filtered[0].DocumentID != "doc-related" {
		t.Fatalf("expected related document to remain, got %s", filtered[0].DocumentID)
	}
}

func TestFilterRelevantChunksReturnsEmptyWhenNoEvidence(t *testing.T) {
	chunks := []RetrievedChunk{
		{
			DocumentChunk: DocumentChunk{
				DocumentID:   "doc-unrelated",
				DocumentName: "unrelated.txt",
				Text:         "系统部署参数、缓存策略和服务端口。",
			},
			Score:    0.98,
			RawScore: 0.78,
		},
	}

	filtered := filterRelevantChunks("武汉大学校长是谁", chunks)
	if len(filtered) != 0 {
		t.Fatalf("expected no chunks without query evidence, got %#v", filtered)
	}
	if !isLowConfidenceSelection("武汉大学校长是谁", filtered) {
		t.Fatal("expected empty filtered result to be low confidence")
	}
}

func TestBuildRetrievalDebugMatchReasons(t *testing.T) {
	reasons := buildRetrievalDebugMatchReasons("武汉大学校长是谁", RetrievedChunk{
		DocumentChunk: DocumentChunk{
			Kind: "text",
			Text: "武汉大学校长信息与学校治理结构说明。",
		},
		Score:    0.82,
		RawScore: 0.86,
	}, false)

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
			Text: "第2行：姓名：张三。薪资：24000。",
		},
		Score:    0.72,
		RawScore: 0.61,
	}, true)
	joined = strings.Join(structuredReasons, " ")
	if !strings.Contains(joined, "结构化数据片段") || !strings.Contains(joined, "确定性结构化查询补充") {
		t.Fatalf("expected structured deterministic reasons, got %#v", structuredReasons)
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
	csvPath := filepath.Join(dir, "users.csv")
	content := strings.Join([]string{
		"姓名,城市,薪资",
		"张三,上海,24000",
		"李四,北京,18000",
		"王五,上海,7000",
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
						Name:            "users.csv",
						Path:            csvPath,
						Status:          "indexed",
						IndexedAt:       indexedAt,
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

func TestExpandStructuredSourceRowsAddsIndexedFileRows(t *testing.T) {
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "users.csv")
	content := strings.Join([]string{
		"姓名,城市,状态",
		"张三,上海,启用",
		"李四,北京,停用",
		"王五,南京,启用",
	}, "\n")
	if err := os.WriteFile(csvPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write csv fixture: %v", err)
	}

	document := model.Document{
		ID:              "doc-users",
		KnowledgeBaseID: "kb-1",
		Name:            "users.csv",
		Path:            csvPath,
	}
	service := &AppService{
		state: &model.AppState{
			KnowledgeBases: map[string]model.KnowledgeBase{
				"kb-1": {
					ID:        "kb-1",
					Name:      "测试知识库",
					Documents: []model.Document{document},
				},
			},
		},
		rag: NewRagService(),
	}
	chunks := []RetrievedChunk{{
		DocumentChunk: DocumentChunk{
			ID:              "doc-users-summary-0",
			KnowledgeBaseID: "kb-1",
			DocumentID:      "doc-users",
			DocumentName:    "users.csv",
			Text:            "统计摘要：文件《users.csv》共有3条数据记录。",
			Kind:            "structured_summary",
		},
		Score: 0.99,
	}}

	expanded := service.expandStructuredSourceRows(
		model.ChatCompletionRequest{DocumentID: "doc-users"},
		"展示当前文档的数据表格",
		chunks,
	)
	text := buildChunkText(expanded)
	if !strings.Contains(text, "源文件完整行数据") {
		t.Fatalf("expected source row context label, got %q", text)
	}
	if !strings.Contains(text, "第2行：姓名：张三。城市：上海。状态：启用。") {
		t.Fatalf("expected first source row to be included, got %q", text)
	}
	if !strings.Contains(text, "第4行：姓名：王五。城市：南京。状态：启用。") {
		t.Fatalf("expected later source row to be included, got %q", text)
	}
}

func TestShouldExpandStructuredSourceRowsRequiresDetailIntent(t *testing.T) {
	chunks := []RetrievedChunk{{
		DocumentChunk: DocumentChunk{
			DocumentID:   "doc-users",
			DocumentName: "users.csv",
			Text:         "统计摘要：文件《users.csv》共有20000条数据记录。",
			Kind:         "structured_summary",
		},
		Score: 0.99,
	}}

	if shouldExpandStructuredSourceRows(model.ChatCompletionRequest{}, "当前有多少记录", chunks) {
		t.Fatal("expected pure count question to use summary without source row expansion")
	}
	if !shouldExpandStructuredSourceRows(model.ChatCompletionRequest{}, "展示这些数据", chunks) {
		t.Fatal("expected data display question to expand source rows")
	}
}
