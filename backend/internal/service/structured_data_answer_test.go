package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-localbase/internal/model"
)

func TestTryBuildStructuredDataAnswerPreview(t *testing.T) {
	service := newStructuredAnswerTestService(t)
	content, sources, ok, err := service.TryBuildStructuredDataAnswer(model.ChatCompletionRequest{
		DocumentID: "doc-users",
		Messages:   []model.ChatMessage{{Role: "user", Content: "展示当前文档的数据表格"}},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !ok {
		t.Fatal("expected structured data answer")
	}
	if len(sources) != 1 || sources[0]["sourceType"] != "structured-data" {
		t.Fatalf("expected structured source metadata, got %#v", sources)
	}
	if !strings.Contains(content, "|姓名|城市|薪资|年龄|") {
		t.Fatalf("expected markdown table, got %q", content)
	}
	if !strings.Contains(content, "|张三|上海|24000|45|") {
		t.Fatalf("expected row data, got %q", content)
	}
}

func TestTryBuildStructuredDataAnswerFilter(t *testing.T) {
	service := newStructuredAnswerTestService(t)
	content, _, ok, err := service.TryBuildStructuredDataAnswer(model.ChatCompletionRequest{
		DocumentID: "doc-users",
		Messages:   []model.ChatMessage{{Role: "user", Content: "筛选城市是上海的数据"}},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !ok {
		t.Fatal("expected structured data answer")
	}
	if !strings.Contains(content, "**总数**：2 条") {
		t.Fatalf("expected two filtered rows, got %q", content)
	}
	if !strings.Contains(content, "|张三|上海|24000|45|") || !strings.Contains(content, "|王五|上海|7000|25|") {
		t.Fatalf("expected shanghai rows, got %q", content)
	}
	if strings.Contains(content, "|李四|北京|18000|30|") {
		t.Fatalf("did not expect beijing row, got %q", content)
	}
}

func TestTryBuildStructuredDataAnswerMaxAverageAndGroup(t *testing.T) {
	service := newStructuredAnswerTestService(t)

	maxContent, _, ok, err := service.TryBuildStructuredDataAnswer(model.ChatCompletionRequest{
		DocumentID: "doc-users",
		Messages:   []model.ChatMessage{{Role: "user", Content: "薪资最高的是谁"}},
	})
	if err != nil || !ok {
		t.Fatalf("expected max answer, ok=%v err=%v", ok, err)
	}
	if !strings.Contains(maxContent, "**数值**：24000") || !strings.Contains(maxContent, "|张三|上海|24000|45|") {
		t.Fatalf("unexpected max answer: %q", maxContent)
	}

	avgContent, _, ok, err := service.TryBuildStructuredDataAnswer(model.ChatCompletionRequest{
		DocumentID: "doc-users",
		Messages:   []model.ChatMessage{{Role: "user", Content: "平均薪资是多少"}},
	})
	if err != nil || !ok {
		t.Fatalf("expected average answer, ok=%v err=%v", ok, err)
	}
	if !strings.Contains(avgContent, "**平均值**：16333.33") {
		t.Fatalf("unexpected average answer: %q", avgContent)
	}

	groupContent, _, ok, err := service.TryBuildStructuredDataAnswer(model.ChatCompletionRequest{
		DocumentID: "doc-users",
		Messages:   []model.ChatMessage{{Role: "user", Content: "按城市统计分布"}},
	})
	if err != nil || !ok {
		t.Fatalf("expected group answer, ok=%v err=%v", ok, err)
	}
	if !strings.Contains(groupContent, "|上海|2|") || !strings.Contains(groupContent, "|北京|1|") {
		t.Fatalf("unexpected group answer: %q", groupContent)
	}
}

func TestTryBuildStructuredDataAnswerAcrossKnowledgeBaseTables(t *testing.T) {
	service := newStructuredAnswerTestService(t)
	dir := filepath.Dir(service.state.KnowledgeBases["kb-1"].Documents[0].Path)
	morePath := filepath.Join(dir, "more_users.csv")
	content := strings.Join([]string{
		"姓名,城市,薪资,年龄",
		"赵六,深圳,32000,36",
	}, "\n")
	if err := os.WriteFile(morePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write second csv fixture: %v", err)
	}

	kb := service.state.KnowledgeBases["kb-1"]
	kb.Documents = append(kb.Documents, model.Document{
		ID:              "doc-more-users",
		KnowledgeBaseID: "kb-1",
		Name:            "more_users.csv",
		Path:            morePath,
	})
	service.state.KnowledgeBases["kb-1"] = kb

	content, _, ok, err := service.TryBuildStructuredDataAnswer(model.ChatCompletionRequest{
		KnowledgeBaseID: "kb-1",
		Messages:        []model.ChatMessage{{Role: "user", Content: "谁的工资最高"}},
	})
	if err != nil || !ok {
		t.Fatalf("expected knowledge-base structured answer, ok=%v err=%v", ok, err)
	}
	if !strings.Contains(content, "**数值**：32000") || !strings.Contains(content, "|赵六|深圳|32000|36|") {
		t.Fatalf("expected highest salary across structured documents, got %q", content)
	}
}

func TestBuildStructuredDeterministicChunks(t *testing.T) {
	service := newStructuredAnswerTestService(t)
	chunks, result, ok, err := service.buildStructuredDeterministicChunks(
		model.ChatCompletionRequest{
			DocumentID: "doc-users",
			Messages:   []model.ChatMessage{{Role: "user", Content: "薪资最高的是谁"}},
		},
		"薪资最高的是谁",
	)
	if err != nil || !ok {
		t.Fatalf("expected deterministic chunks, ok=%v err=%v", ok, err)
	}
	if result.Plan.Intent != structuredIntentMax || result.Plan.TargetField != "薪资" {
		t.Fatalf("unexpected plan: %#v", result.Plan)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected one deterministic chunk, got %d", len(chunks))
	}
	if chunks[0].Kind != "structured_deterministic" {
		t.Fatalf("expected deterministic chunk kind, got %s", chunks[0].Kind)
	}
	if !strings.Contains(chunks[0].Text, "|张三|上海|24000|45|") {
		t.Fatalf("expected source row in deterministic chunk, got %q", chunks[0].Text)
	}
}

func TestBuildRetrievalDebugEvalCandidateFromLowConfidence(t *testing.T) {
	candidate := buildRetrievalDebugEvalCandidate(
		model.ChatCompletionRequest{KnowledgeBaseID: "kb-1"},
		"教师薪资最高是谁",
		true,
		[]RetrievedChunk{{
			DocumentChunk: DocumentChunk{
				ID:              "doc-users-source-rows-0",
				KnowledgeBaseID: "kb-1",
				DocumentID:      "doc-users",
				DocumentName:    "users.csv",
				Text:            "第2行：姓名：张三。薪资：24000。",
			},
			Score: 0.12,
		}},
		"[users.csv#1] 第2行：姓名：张三。薪资：24000。",
	)
	if candidate == nil {
		t.Fatal("expected eval candidate")
	}
	if candidate.Question != "教师薪资最高是谁" {
		t.Fatalf("unexpected question: %q", candidate.Question)
	}
	if candidate.AnswerType != "retrieval-debug-candidate" || candidate.Difficulty != "hard" {
		t.Fatalf("unexpected eval metadata: %#v", candidate)
	}
	if len(candidate.SourceDocuments) != 1 || candidate.SourceDocuments[0].ChunkID != "doc-users-source-rows-0" {
		t.Fatalf("unexpected sources: %#v", candidate.SourceDocuments)
	}
}

func newStructuredAnswerTestService(t *testing.T) *AppService {
	t.Helper()
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "users.csv")
	content := strings.Join([]string{
		"姓名,城市,薪资,年龄",
		"张三,上海,24000,45",
		"李四,北京,18000,30",
		"王五,上海,7000,25",
	}, "\n")
	if err := os.WriteFile(csvPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write csv fixture: %v", err)
	}

	return &AppService{
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
					}},
				},
			},
		},
	}
}
