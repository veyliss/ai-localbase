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
