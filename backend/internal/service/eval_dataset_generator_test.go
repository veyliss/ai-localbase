package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-localbase/internal/model"
)

func TestSelectEvalChunkCandidatesPrefersUsefulChunks(t *testing.T) {
	chunks := []DocumentChunk{
		{ID: "doc-1-chunk-0", Text: "目录", Index: 0},
		{ID: "doc-1-chunk-1", Text: "AI LocalBase 支持知识库管理、文档上传、检索增强问答和聊天记录持久化。", Index: 1},
		{ID: "doc-1-chunk-2", Text: "这是一个普通说明段落，描述项目的背景信息和使用场景。", Index: 2},
	}

	selected := selectEvalChunkCandidates(chunks, 1)
	if len(selected) != 1 {
		t.Fatalf("expected 1 selected chunk, got %d", len(selected))
	}
	if selected[0].ID != "doc-1-chunk-1" {
		t.Fatalf("expected useful capability chunk, got %s", selected[0].ID)
	}
}

func TestSelectEvalChunkCandidatesKeepsStructuredSummaryPriority(t *testing.T) {
	chunks := []DocumentChunk{
		{ID: "doc-1-chunk-0", Text: "第2行：姓名：张三。薪资：24000。第3行：姓名：李四。薪资：18000。", Index: 0, Kind: "structured_row"},
		{ID: "doc-1-summary-0", Text: "统计摘要：文件《工作簿1.csv》共有2条数据记录。\n统计摘要：字段“薪资”为数值列，非空值2个，最小值18000.00，最大值24000.00，平均值21000.00。", Index: 1, Kind: "structured_summary"},
	}

	selected := selectEvalChunkCandidates(chunks, len(chunks))
	if len(selected) != 2 {
		t.Fatalf("expected 2 selected chunks, got %d", len(selected))
	}
	if selected[0].Kind != "structured_summary" {
		t.Fatalf("expected structured summary first, got %s", selected[0].Kind)
	}
}

func TestBuildStructuredSummaryEvalCasesAreGrounded(t *testing.T) {
	document := model.Document{
		ID:              "doc-1",
		KnowledgeBaseID: "kb-1",
		Name:            "工作簿1.csv",
	}
	chunk := DocumentChunk{
		ID:              "doc-1-summary-0",
		KnowledgeBaseID: "kb-1",
		DocumentID:      "doc-1",
		DocumentName:    "工作簿1.csv",
		Text: strings.Join([]string{
			"统计摘要：文件《工作簿1.csv》共有4条数据记录。",
			"统计摘要：字段“薪资”为数值列，非空值4个，最小值7000.00，最大值24000.00，平均值14250.00。",
			"统计摘要：字段“性别”为类别列，共4个非空值，主要分布为：女(2)、男(2)。",
		}, "\n"),
		Index: 0,
		Kind:  "structured_summary",
	}

	cases := buildEvalCasesFromChunk(document, chunk, 10)
	if len(cases) < 5 {
		t.Fatalf("expected structured summary cases, got %d", len(cases))
	}
	for _, item := range cases {
		if !validateEvalCase(item, document.Name, chunk.Text) {
			t.Fatalf("expected grounded eval case, got %#v", item)
		}
		if strings.Contains(item.Question, "主要讲了什么") || strings.Contains(item.Question, "包括哪些要点") {
			t.Fatalf("expected specific question, got %q", item.Question)
		}
	}

	var foundMax bool
	for _, item := range cases {
		if strings.Contains(item.Question, "最大值") && strings.Contains(item.Answer, "24000.00") {
			foundMax = true
			break
		}
	}
	if !foundMax {
		t.Fatalf("expected max value eval case, got %#v", cases)
	}
}

func TestBuildStructuredRowEvalCasesAnswerExactField(t *testing.T) {
	document := model.Document{
		ID:              "doc-1",
		KnowledgeBaseID: "kb-1",
		Name:            "工作簿1.csv",
	}
	chunk := DocumentChunk{
		ID:              "doc-1-chunk-0",
		KnowledgeBaseID: "kb-1",
		DocumentID:      "doc-1",
		DocumentName:    "工作簿1.csv",
		Text:            "第2行：姓名：张三。性别：男。职称：高级职称。教师编号：111222333111。年龄：45。手机号：15911110011。薪资：24000。教龄：20。",
		Index:           0,
		Kind:            "structured_row",
	}

	cases := buildEvalCasesFromChunk(document, chunk, 5)
	if len(cases) == 0 {
		t.Fatal("expected structured row eval cases")
	}
	for _, item := range cases {
		if !validateEvalCase(item, document.Name, chunk.Text) {
			t.Fatalf("expected grounded row eval case, got %#v", item)
		}
	}
	if cases[0].Question != "《工作簿1.csv》第2行的“姓名”是什么？" {
		t.Fatalf("unexpected first row question: %q", cases[0].Question)
	}
	if cases[0].Answer != "张三" {
		t.Fatalf("expected exact field answer, got %q", cases[0].Answer)
	}
}

func TestBuildEvalCasesSkipsUnstructuredPlainText(t *testing.T) {
	document := model.Document{
		ID:              "doc-1",
		KnowledgeBaseID: "kb-1",
		Name:            "随笔.md",
	}
	chunk := DocumentChunk{
		ID:              "doc-1-chunk-0",
		KnowledgeBaseID: "kb-1",
		DocumentID:      "doc-1",
		DocumentName:    "随笔.md",
		Text:            "这是一段没有标题和明确字段的普通说明文字，只提供零散背景，不适合作为自动评估集的可靠来源。",
		Index:           0,
		Kind:            "text",
	}

	cases := buildEvalCasesFromChunk(document, chunk, 5)
	if len(cases) != 0 {
		t.Fatalf("expected no low-confidence cases, got %#v", cases)
	}
}

func TestGenerateEvalDatasetPersistsDataset(t *testing.T) {
	tempDir := t.TempDir()
	documentPath := filepath.Join(tempDir, "teachers.csv")
	content := strings.Join([]string{
		"姓名,性别,薪资",
		"张三,男,24000",
		"李四,女,18000",
		"王五,男,7000",
	}, "\n")
	if err := os.WriteFile(documentPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write csv: %v", err)
	}

	store := NewAppStateStore(filepath.Join(tempDir, "state.json"))
	service := &AppService{
		state: &model.AppState{
			Config: model.AppConfig{},
			KnowledgeBases: map[string]model.KnowledgeBase{
				"kb-1": {
					ID:        "kb-1",
					Name:      "教师信息",
					CreatedAt: "2026-03-12T00:00:00Z",
					Documents: []model.Document{{
						ID:              "doc-1",
						KnowledgeBaseID: "kb-1",
						Name:            "teachers.csv",
						Path:            documentPath,
						Status:          "indexed",
					}},
				},
			},
			EvalDatasets: map[string]model.EvalDataset{},
		},
		store: store,
		rag:   NewRagService(),
	}

	response, err := service.GenerateEvalDataset(model.GenerateEvalDatasetRequest{
		KnowledgeBaseID: "kb-1",
		MaxPerDocument:  3,
	})
	if err != nil {
		t.Fatalf("generate eval dataset: %v", err)
	}
	if response.DatasetID == "" || response.Count == 0 {
		t.Fatalf("expected saved dataset metadata, got %#v", response)
	}

	summaries := service.ListEvalDatasets("kb-1")
	if len(summaries) != 1 || summaries[0].ID != response.DatasetID {
		t.Fatalf("expected one saved dataset summary, got %#v", summaries)
	}

	dataset, err := service.GetEvalDataset(response.DatasetID)
	if err != nil {
		t.Fatalf("get eval dataset: %v", err)
	}
	if dataset.Count != response.Count || len(dataset.Items) != response.Count {
		t.Fatalf("expected saved dataset items, got %#v", dataset)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load persisted state: %v", err)
	}
	if _, ok := loaded.EvalDatasets[response.DatasetID]; !ok {
		t.Fatalf("expected dataset persisted to state, got %#v", loaded.EvalDatasets)
	}

	if err := service.DeleteEvalDataset(response.DatasetID); err != nil {
		t.Fatalf("delete eval dataset: %v", err)
	}
	if len(service.ListEvalDatasets("kb-1")) != 0 {
		t.Fatalf("expected deleted dataset")
	}
}

func TestAddEvalDatasetCandidateCreatesReviewDataset(t *testing.T) {
	tempDir := t.TempDir()
	store := NewAppStateStore(filepath.Join(tempDir, "state.json"))
	service := &AppService{
		state: &model.AppState{
			Config: model.AppConfig{},
			KnowledgeBases: map[string]model.KnowledgeBase{
				"kb-1": {
					ID:        "kb-1",
					Name:      "教师信息",
					CreatedAt: "2026-03-12T00:00:00Z",
					Documents: []model.Document{{
						ID:              "doc-1",
						KnowledgeBaseID: "kb-1",
						Name:            "teachers.csv",
						Status:          "indexed",
					}},
				},
			},
			EvalDatasets: map[string]model.EvalDataset{},
		},
		store: store,
		rag:   NewRagService(),
	}

	req := model.AddEvalDatasetCandidateRequest{
		KnowledgeBaseID: "kb-1",
		DocumentID:      "doc-1",
		Item: model.EvalGroundTruthCase{
			ID:             "debug-low-confidence-kb-1-001",
			Question:       "谁的薪资最高？",
			Answer:         "张三的薪资最高。",
			AnswerSnippets: []string{"张三,男,24000", "张三,男,24000"},
			SourceDocuments: []model.EvalSourceDocument{{
				KnowledgeBaseID: "kb-1",
				DocumentID:      "doc-1",
				ChunkID:         "chunk-1",
			}},
			AnswerType: "retrieval-debug-candidate",
			Difficulty: "hard",
		},
	}

	response, err := service.AddEvalDatasetCandidate(req)
	if err != nil {
		t.Fatalf("add eval candidate: %v", err)
	}
	if !response.Created || response.Dataset.Kind != evalDatasetKindReview {
		t.Fatalf("expected created review dataset, got %#v", response)
	}
	if response.Item.ReviewStatus != evalReviewStatusPending || !response.Item.Disabled {
		t.Fatalf("expected pending disabled candidate, got %#v", response.Item)
	}
	if len(response.Item.AnswerSnippets) != 1 {
		t.Fatalf("expected normalized snippets, got %#v", response.Item.AnswerSnippets)
	}

	updatedReq := req
	updatedReq.Item.Answer = "更新后的答案。"
	second, err := service.AddEvalDatasetCandidate(updatedReq)
	if err != nil {
		t.Fatalf("add duplicate eval candidate: %v", err)
	}
	if second.Dataset.ID != response.Dataset.ID || second.Dataset.Count != 1 || second.Created {
		t.Fatalf("expected duplicate candidate replaced in same dataset, got %#v", second)
	}

	dataset, err := service.GetEvalDataset(response.Dataset.ID)
	if err != nil {
		t.Fatalf("get review dataset: %v", err)
	}
	if dataset.Kind != evalDatasetKindReview || dataset.Count != 1 || dataset.Items[0].Answer != "更新后的答案。" {
		t.Fatalf("unexpected review dataset: %#v", dataset)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load persisted state: %v", err)
	}
	if loaded.EvalDatasets[response.Dataset.ID].Items[0].ReviewStatus != evalReviewStatusPending {
		t.Fatalf("expected persisted review status, got %#v", loaded.EvalDatasets[response.Dataset.ID].Items[0])
	}
}

func TestUpdateAndDeleteEvalDatasetItem(t *testing.T) {
	tempDir := t.TempDir()
	store := NewAppStateStore(filepath.Join(tempDir, "state.json"))
	service := &AppService{
		state: &model.AppState{
			Config: model.AppConfig{},
			KnowledgeBases: map[string]model.KnowledgeBase{
				"kb-1": {
					ID:        "kb-1",
					Name:      "教师信息",
					CreatedAt: "2026-03-12T00:00:00Z",
				},
			},
			EvalDatasets: map[string]model.EvalDataset{
				"eval-1": {
					ID:              "eval-1",
					Name:            "待审核评估样本 - 教师信息",
					Kind:            evalDatasetKindReview,
					KnowledgeBaseID: "kb-1",
					Count:           1,
					DocumentCount:   1,
					CreatedAt:       "2026-03-12T00:00:00Z",
					Items: []model.EvalGroundTruthCase{{
						ID:             "case-1",
						Question:       "谁的薪资最高？",
						Answer:         "候选答案",
						AnswerSnippets: []string{"候选片段"},
						SourceDocuments: []model.EvalSourceDocument{{
							KnowledgeBaseID: "kb-1",
							DocumentID:      "doc-1",
							ChunkID:         "chunk-1",
						}},
						AnswerType:   "retrieval-debug-candidate",
						Difficulty:   "hard",
						ReviewStatus: evalReviewStatusPending,
						Disabled:     true,
					}},
				},
			},
		},
		store: store,
		rag:   NewRagService(),
	}

	updateResp, err := service.UpdateEvalDatasetItem("eval-1", "case-1", model.UpdateEvalDatasetItemRequest{
		Item: model.EvalGroundTruthCase{
			ID:             "ignored-id",
			Question:       "谁的薪资最高？",
			Answer:         "张三的薪资最高。",
			AnswerSnippets: []string{"张三,24000"},
			AnswerType:     "numeric",
			Difficulty:     "medium",
			ReviewStatus:   evalReviewStatusApproved,
			Disabled:       false,
		},
	})
	if err != nil {
		t.Fatalf("update eval dataset item: %v", err)
	}
	if updateResp.Item.ID != "case-1" || updateResp.Item.ReviewStatus != evalReviewStatusApproved || updateResp.Item.Disabled {
		t.Fatalf("unexpected updated item: %#v", updateResp.Item)
	}
	if len(updateResp.Item.SourceDocuments) != 1 {
		t.Fatalf("expected source documents preserved, got %#v", updateResp.Item.SourceDocuments)
	}

	deleteResp, err := service.DeleteEvalDatasetItem("eval-1", "case-1")
	if err != nil {
		t.Fatalf("delete eval dataset item: %v", err)
	}
	if deleteResp.Dataset.Count != 0 {
		t.Fatalf("expected empty dataset after delete, got %#v", deleteResp.Dataset)
	}

	dataset, err := service.GetEvalDataset("eval-1")
	if err != nil {
		t.Fatalf("get eval dataset: %v", err)
	}
	if dataset.Count != 0 || len(dataset.Items) != 0 {
		t.Fatalf("expected deleted item, got %#v", dataset)
	}
}
