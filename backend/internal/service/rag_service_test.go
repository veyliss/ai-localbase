package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"ai-localbase/internal/model"
	"ai-localbase/internal/util"
)

func TestRagServiceChunkText(t *testing.T) {
	rag := NewRagService()
	input := strings.Repeat("知识库检索能力验证。", 120)

	chunks := rag.ChunkText(input)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}

	for index, chunk := range chunks {
		if strings.TrimSpace(chunk) == "" {
			t.Fatalf("chunk %d should not be empty", index)
		}
	}
}

func TestRagServiceBuildDocumentChunks(t *testing.T) {
	rag := NewRagService()
	document := model.Document{
		ID:              "doc-1",
		KnowledgeBaseID: "kb-1",
		Name:            "demo.md",
	}

	chunks := rag.BuildDocumentChunks(document, strings.Repeat("RAG 文档切分测试。", 100))
	if len(chunks) == 0 {
		t.Fatal("expected non-empty document chunks")
	}

	first := chunks[0]
	if first.DocumentID != document.ID {
		t.Fatalf("expected document id %s, got %s", document.ID, first.DocumentID)
	}
	if first.KnowledgeBaseID != document.KnowledgeBaseID {
		t.Fatalf("expected knowledge base id %s, got %s", document.KnowledgeBaseID, first.KnowledgeBaseID)
	}
}

func TestRagServiceBuildDocumentChunksStructuredSummaryFirst(t *testing.T) {
	rag := NewRagService()
	document := model.Document{
		ID:              "doc-structured",
		KnowledgeBaseID: "kb-1",
		Name:            "sample.csv",
	}
	text := strings.Join([]string{
		"文件：sample.csv。字段：类别、数量、状态。数据行数：4。",
		"统计摘要：文件《sample.csv》共有4条数据记录。",
		"统计摘要：字段“类别”为类别列，共4个非空值，主要分布为：甲类(2)、乙类(1)、丙类(1)。",
		"第2行：类别：甲类。数量：120。状态：启用。",
	}, "\n")

	chunks := rag.BuildDocumentChunks(document, text)
	if len(chunks) == 0 {
		t.Fatal("expected structured chunks")
	}
	if chunks[0].Kind != "structured_summary" {
		t.Fatalf("expected first chunk kind structured_summary, got %s", chunks[0].Kind)
	}
	if !strings.Contains(chunks[0].Text, "统计摘要：") {
		t.Fatalf("expected structured summary chunk first, got %q", chunks[0].Text)
	}
}

func TestRagServiceEmbedTextsFallback(t *testing.T) {
	rag := NewRagService()
	cfg := model.EmbeddingModelConfig{
		Provider: "ollama",
		BaseURL:  "http://127.0.0.1:0",
		Model:    "demo-embedding-model",
	}

	embeddings, err := rag.EmbedTexts(t.Context(), cfg, []string{"示例缓存", "示例检索"}, 8)
	if err != nil {
		t.Fatalf("expected fallback embeddings without error, got %v", err)
	}
	if len(embeddings) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(embeddings))
	}
	for index, vector := range embeddings {
		if len(vector) != 8 {
			t.Fatalf("embedding %d expected dimension 8, got %d", index, len(vector))
		}
	}
}

func TestRagServiceBuildContext(t *testing.T) {
	rag := NewRagService()
	contextText, sources := rag.BuildContext([]RetrievedChunk{
		{
			DocumentChunk: DocumentChunk{
				ID:              "chunk-1",
				KnowledgeBaseID: "kb-1",
				DocumentID:      "doc-1",
				DocumentName:    "demo.md",
				Text:            "这是一个用于回答问题的片段。",
				Index:           0,
			},
			Score: 0.92,
		},
	})

	if !strings.Contains(contextText, "demo.md") {
		t.Fatalf("expected context to contain document name, got %s", contextText)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sources))
	}
	if sources[0]["chunkId"] != "chunk-1" {
		t.Fatalf("expected chunkId chunk-1, got %s", sources[0]["chunkId"])
	}
}

func TestExtractDocumentTextFromMarkdown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "demo.md")
	content := "# 标题\n\n第一段内容。\n第二段内容。\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write markdown file: %v", err)
	}

	text, err := util.ExtractDocumentText(path)
	if err != nil {
		t.Fatalf("extract markdown text: %v", err)
	}

	if !strings.Contains(text, "第一段内容。") {
		t.Fatalf("expected extracted text to contain markdown content, got %q", text)
	}
}

func TestExtractContentPreviewFromMarkdown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "preview.md")
	content := strings.Repeat("用于摘要生成的内容。", 20)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write markdown file: %v", err)
	}

	preview := util.ExtractContentPreview(path)
	if !strings.Contains(preview, "用于摘要生成的内容") {
		t.Fatalf("expected preview to contain file content, got %q", preview)
	}
	if len([]rune(preview)) > 123 {
		t.Fatalf("expected preview to be truncated to a reasonable length, got %d runes", len([]rune(preview)))
	}
}

func TestAppServiceIndexDocumentWithExtractedText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "indexed.md")
	content := strings.Repeat("示例文本抽取后进入索引链路。", 80)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write indexed markdown file: %v", err)
	}

	service := NewAppService(nil, NewAppStateStore(""), nil, model.ServerConfig{})
	knowledgeBases := service.ListKnowledgeBases()
	if len(knowledgeBases) == 0 {
		t.Fatal("expected default knowledge base")
	}

	document := model.Document{
		ID:              "doc-indexed",
		KnowledgeBaseID: knowledgeBases[0].ID,
		Name:            "indexed.md",
		Path:            path,
		Status:          "processing",
	}

	indexed, err := service.IndexDocument(document)
	if err != nil {
		t.Fatalf("index document: %v", err)
	}

	if indexed.Status != "indexed" {
		t.Fatalf("expected indexed status, got %s", indexed.Status)
	}
	if !strings.Contains(indexed.ContentPreview, "示例文本抽取后进入索引链路") {
		t.Fatalf("expected content preview to come from extracted text, got %q", indexed.ContentPreview)
	}
}

func TestBuildSparseVector(t *testing.T) {
	vector := BuildSparseVector("混合 hybrid search 支持 sample device max")
	if len(vector.Indices) == 0 {
		t.Fatal("expected sparse vector indices")
	}
	if len(vector.Indices) != len(vector.Values) {
		t.Fatalf("expected indices and values length match, got %d and %d", len(vector.Indices), len(vector.Values))
	}
	if len(vector.Indices) < 5 {
		t.Fatalf("expected more tokens, got %d", len(vector.Indices))
	}
}

func TestRRFFusion(t *testing.T) {
	dense := []SearchResult{
		{ID: "a", Score: 0.9},
		{ID: "b", Score: 0.8},
		{ID: "c", Score: 0.7},
	}
	sparse := []SearchResult{
		{ID: "b", Score: 0.95},
		{ID: "d", Score: 0.6},
		{ID: "a", Score: 0.55},
	}

	merged := rrfFusion(dense, sparse, 4)
	if len(merged) < 3 {
		t.Fatalf("expected merged results, got %d", len(merged))
	}
	if merged[0].ID != "a" && merged[0].ID != "b" {
		t.Fatalf("expected top1 to be a or b, got %s", merged[0].ID)
	}
	if merged[0].ID == "b" && merged[1].ID != "a" {
		t.Fatalf("expected a to rank near top, got %s", merged[1].ID)
	}
	byID := make(map[string]SearchResult)
	for _, item := range merged {
		byID[item.ID] = item
	}
	channels, ok := byID["a"].Payload[qdrantPayloadRetrievalChannels].([]string)
	if !ok || len(channels) != 2 || channels[0] != "dense" || channels[1] != "sparse" {
		t.Fatalf("expected a to carry dense and sparse channels, got %#v", byID["a"].Payload[qdrantPayloadRetrievalChannels])
	}
	if byID["a"].Payload[qdrantPayloadDenseRank] != 1 || byID["a"].Payload[qdrantPayloadSparseRank] != 3 {
		t.Fatalf("expected a rank metadata, got %#v", byID["a"].Payload)
	}
	sparseOnlyChannels, ok := byID["d"].Payload[qdrantPayloadRetrievalChannels].([]string)
	if !ok || len(sparseOnlyChannels) != 1 || sparseOnlyChannels[0] != "sparse" {
		t.Fatalf("expected d to carry sparse-only channel, got %#v", byID["d"].Payload[qdrantPayloadRetrievalChannels])
	}
}

func TestSearchHybridFallsBackToDenseResults(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/points/search") {
			http.NotFound(w, r)
			return
		}
		call := atomic.AddInt32(&calls, 1)
		var req qdrantSearchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode search request: %v", err)
		}
		if call == 2 {
			http.Error(w, "sparse fallback unavailable", http.StatusInternalServerError)
			return
		}
		resp := qdrantSearchResponse{
			Result: []qdrantScoredPoint{
				{
					ID:    "chunk-dense-1",
					Score: 0.91,
					Payload: map[string]any{
						"chunk_id":          "chunk-dense-1",
						"text":              "dense result one",
						"document_id":       "doc-1",
						"document_name":     "Doc 1",
						"knowledge_base_id": "kb-1",
						"chunk_index":       0,
					},
				},
				{
					ID:    "chunk-dense-2",
					Score: 0.87,
					Payload: map[string]any{
						"chunk_id":          "chunk-dense-2",
						"text":              "dense result two",
						"document_id":       "doc-2",
						"document_name":     "Doc 2",
						"knowledge_base_id": "kb-1",
						"chunk_index":       1,
					},
				},
			},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("encode qdrant response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	rag := NewRagService()
	qdrant := NewQdrantService(model.ServerConfig{
		QdrantURL:        server.URL,
		QdrantVectorSize: 4,
		QdrantDistance:   "cosine",
	})
	rag.SetQdrantService(qdrant)

	results, err := rag.SearchHybrid(t.Context(), qdrant, "kb-1", []float64{0.2, 0.4, 0.1, 0.3}, BuildSparseVector("示例机构 人员"), 2, nil)
	if err != nil {
		t.Fatalf("search hybrid: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected dense fallback results size 2, got %d", len(results))
	}
	if results[0].ID != "chunk-dense-1" {
		t.Fatalf("expected first dense result, got %s", results[0].ID)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected one legacy dense search attempt, got %d", calls)
	}
}

func TestSearchHybridUsesSparseQuery(t *testing.T) {
	var denseQueries int32
	var sparseQueries int32
	var legacySearches int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/points/search") {
			atomic.AddInt32(&legacySearches, 1)
			http.Error(w, "legacy search should not be used", http.StatusBadRequest)
			return
		}
		if !strings.Contains(r.URL.Path, "/points/query") {
			http.NotFound(w, r)
			return
		}
		var req qdrantQueryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode query request: %v", err)
		}
		resultID := "chunk-dense"
		if req.Using == qdrantSparseVectorName {
			atomic.AddInt32(&sparseQueries, 1)
			resultID = "chunk-sparse"
		} else if req.Using == qdrantDenseVectorName {
			atomic.AddInt32(&denseQueries, 1)
		}

		resp := map[string]any{
			"result": map[string]any{
				"points": []qdrantScoredPoint{
					{
						ID:    resultID,
						Score: 0.91,
						Payload: map[string]any{
							"chunk_id":          resultID,
							"text":              resultID + " result",
							"document_id":       "doc-1",
							"document_name":     "Doc 1",
							"knowledge_base_id": "kb-1",
							"chunk_index":       0,
						},
					},
				},
			},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("encode qdrant response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	rag := NewRagService()
	qdrant := NewQdrantService(model.ServerConfig{
		QdrantURL:        server.URL,
		QdrantVectorSize: 4,
		QdrantDistance:   "cosine",
	})

	results, err := rag.SearchHybrid(t.Context(), qdrant, "kb-1", []float64{0.2, 0.4, 0.1, 0.3}, BuildSparseVector("示例机构 人员"), 2, nil)
	if err != nil {
		t.Fatalf("search hybrid: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected dense and sparse results, got %d", len(results))
	}
	if atomic.LoadInt32(&denseQueries) == 0 || atomic.LoadInt32(&sparseQueries) == 0 {
		t.Fatalf("expected dense and sparse query attempts, got dense=%d sparse=%d", denseQueries, sparseQueries)
	}
	if atomic.LoadInt32(&legacySearches) != 0 {
		t.Fatalf("expected no legacy search fallback, got %d", legacySearches)
	}
}

func TestSearchDenseParsesNamedVectorQueryResponse(t *testing.T) {
	var denseQueries int32
	var legacySearches int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/points/search") {
			atomic.AddInt32(&legacySearches, 1)
			http.Error(w, `{"status":{"error":"Wrong input: Collection requires specified vector name in the request, available names: dense, sparse"}}`, http.StatusBadRequest)
			return
		}
		if !strings.Contains(r.URL.Path, "/points/query") {
			http.NotFound(w, r)
			return
		}
		atomic.AddInt32(&denseQueries, 1)
		var req qdrantQueryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode query request: %v", err)
		}
		if req.Using != qdrantDenseVectorName {
			t.Fatalf("expected dense vector name, got %q", req.Using)
		}

		resp := map[string]any{
			"result": map[string]any{
				"points": []qdrantScoredPoint{
					{
						ID:    "chunk-dense",
						Score: 0.93,
						Payload: map[string]any{
							"chunk_id":          "chunk-dense",
							"text":              "dense result",
							"document_id":       "doc-1",
							"document_name":     "Doc 1",
							"knowledge_base_id": "kb-1",
							"chunk_index":       0,
						},
					},
				},
			},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("encode qdrant response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	qdrant := NewQdrantService(model.ServerConfig{
		QdrantURL:        server.URL,
		QdrantVectorSize: 4,
		QdrantDistance:   "cosine",
	})

	results, err := qdrant.Search(t.Context(), "kb-30", []float64{0.2, 0.4, 0.1, 0.3}, 2, nil)
	if err != nil {
		t.Fatalf("search dense: %v", err)
	}
	if len(results) != 1 || results[0].ID != "chunk-dense" {
		t.Fatalf("unexpected dense results: %#v", results)
	}
	if atomic.LoadInt32(&denseQueries) != 1 {
		t.Fatalf("expected one dense query, got %d", denseQueries)
	}
	if atomic.LoadInt32(&legacySearches) != 0 {
		t.Fatalf("expected no legacy search fallback, got %d", legacySearches)
	}
}

func TestMultiQueryDeduplication(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/points/search") {
			call := atomic.AddInt32(&calls, 1)
			var resp qdrantSearchResponse
			if call == 1 {
				resp.Result = []qdrantScoredPoint{
					{
						ID:    "chunk-1",
						Score: 0.9,
						Payload: map[string]any{
							"chunk_id":          "chunk-1",
							"text":              "片段一",
							"document_id":       "doc-1",
							"document_name":     "Doc 1",
							"knowledge_base_id": "kb-1",
							"chunk_index":       0,
						},
					},
					{
						ID:    "chunk-2",
						Score: 0.8,
						Payload: map[string]any{
							"chunk_id":          "chunk-2",
							"text":              "片段二",
							"document_id":       "doc-2",
							"document_name":     "Doc 2",
							"knowledge_base_id": "kb-1",
							"chunk_index":       1,
						},
					},
				}
			} else {
				resp.Result = []qdrantScoredPoint{
					{
						ID:    "chunk-1",
						Score: 0.95,
						Payload: map[string]any{
							"chunk_id":          "chunk-1",
							"text":              "片段一",
							"document_id":       "doc-1",
							"document_name":     "Doc 1",
							"knowledge_base_id": "kb-1",
							"chunk_index":       0,
						},
					},
					{
						ID:    "chunk-3",
						Score: 0.7,
						Payload: map[string]any{
							"chunk_id":          "chunk-3",
							"text":              "片段三",
							"document_id":       "doc-3",
							"document_name":     "Doc 3",
							"knowledge_base_id": "kb-1",
							"chunk_index":       2,
						},
					},
				}
			}
			if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Fatalf("encode qdrant response: %v", err)
			}
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	rag := NewRagService()
	qdrant := NewQdrantService(model.ServerConfig{
		QdrantURL:        server.URL,
		QdrantVectorSize: 4,
		QdrantDistance:   "cosine",
	})
	rag.SetQdrantService(qdrant)

	results, err := rag.MultiQuerySearch(
		t.Context(),
		[]string{"  Foo", "foo", "Bar"},
		"kb-1",
		3,
		0,
		model.EmbeddingModelConfig{Provider: "openai"},
	)
	if err != nil {
		t.Fatalf("multi query search: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 unique chunks, got %d", len(results))
	}
	if results[0].ID != "chunk-1" {
		t.Fatalf("expected top chunk-1, got %s", results[0].ID)
	}
	if results[0].Score != 0.95 {
		t.Fatalf("expected chunk-1 score 0.95, got %v", results[0].Score)
	}
}

func TestLLMQueryRewriterParsing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			resp := openAIChatResponse{
				ID:      "chatcmpl-test",
				Object:  "chat.completion",
				Created: 123,
				Model:   "test-model",
				Choices: []model.ChatCompletionChoice{
					{
						Index: 0,
						Message: model.ChatMessage{
							Role:    "assistant",
							Content: "- 查询一\n• 查询二\n\n* 查询三\n- 查询一",
						},
					},
				},
			}
			if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Fatalf("encode chat response: %v", err)
			}
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	llm := &LLMService{client: server.Client()}
	rewriter := NewLLMQueryRewriter(llm, 3)
	rewriter.SetChatConfigProvider(func() model.ChatModelConfig {
		return model.ChatModelConfig{
			Provider: "openai",
			BaseURL:  server.URL,
			Model:    "test-model",
		}
	})

	result, err := rewriter.Rewrite(t.Context(), "示例问题", []string{"示例上下文1", "示例上下文2"})
	if err != nil {
		t.Fatalf("rewrite query: %v", err)
	}
	if len(result.RewrittenQueries) != 4 {
		t.Fatalf("expected 4 queries, got %d", len(result.RewrittenQueries))
	}
	assertContains := func(target string) {
		for _, item := range result.RewrittenQueries {
			if item == target {
				return
			}
		}
		t.Fatalf("expected queries to contain %s", target)
	}
	assertContains("查询一")
	assertContains("查询二")
	assertContains("查询三")
	assertContains("示例问题")
}
