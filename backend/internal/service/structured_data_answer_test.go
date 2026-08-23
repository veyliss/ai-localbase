package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-localbase/internal/model"
	"ai-localbase/internal/util"
)

func TestQueryStructuredDataPreview(t *testing.T) {
	service := newStructuredQueryTestService(t)
	result, sources, ok, err := service.QueryStructuredData(model.ChatCompletionRequest{
		DocumentID: "doc-users",
		Messages:   []model.ChatMessage{{Role: "user", Content: "展示当前文档的数据表格"}},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !ok {
		t.Fatal("expected structured data result")
	}
	if len(sources) != 1 || sources[0]["sourceType"] != "structured-data" {
		t.Fatalf("expected structured source metadata, got %#v", sources)
	}
	if result.Intent != "preview" || result.TotalRows != 3 || result.MatchedRows != 3 {
		t.Fatalf("unexpected preview metadata: %#v", result)
	}
	if len(result.Rows) != 3 || result.Rows[0].Values["姓名"] != "成员甲" || result.Rows[0].Values["薪资"] != "300" {
		t.Fatalf("expected structured row data, got %#v", result.Rows)
	}
}

func TestLooksLikeStructuredDataQueryRequiresTableSignal(t *testing.T) {
	if looksLikeStructuredDataQuery("列出主要角色") {
		t.Fatal("expected ordinary document list question not to trigger structured data handling")
	}
	if !looksLikeStructuredDataQuery("列出表格中的所有记录") {
		t.Fatal("expected explicit table record question to trigger structured data handling")
	}
}

func TestQueryStructuredDataFilter(t *testing.T) {
	service := newStructuredQueryTestService(t)
	result, _, ok, err := service.QueryStructuredData(model.ChatCompletionRequest{
		DocumentID: "doc-users",
		Messages:   []model.ChatMessage{{Role: "user", Content: "筛选城市是城市甲的数据"}},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !ok {
		t.Fatal("expected structured data result")
	}
	if result.Intent != "filter" || result.FilterField != "城市" || result.FilterValue != "城市甲" || result.MatchedRows != 2 {
		t.Fatalf("unexpected filter metadata: %#v", result)
	}
	if len(result.Rows) != 2 || result.Rows[0].Values["姓名"] != "成员甲" || result.Rows[1].Values["姓名"] != "成员丙" {
		t.Fatalf("expected city-a rows, got %#v", result.Rows)
	}
	for _, row := range result.Rows {
		if row.Values["城市"] != "城市甲" {
			t.Fatalf("did not expect non-city-a row, got %#v", row)
		}
	}
}

func TestQueryStructuredDataSubjectAttributeFilter(t *testing.T) {
	service := newStructuredQueryTestService(t)
	result, _, ok, err := service.QueryStructuredData(model.ChatCompletionRequest{
		DocumentID: "doc-users",
		Messages:   []model.ChatMessage{{Role: "user", Content: "成员甲的薪资是多少？"}},
	})
	if err != nil || !ok {
		t.Fatalf("expected subject attribute result, ok=%v err=%v", ok, err)
	}
	if result.Intent != "filter" || result.FilterField != "姓名" || result.FilterValue != "成员甲" || result.TargetField != "薪资" || result.MatchedRows != 1 {
		t.Fatalf("unexpected subject attribute metadata: %#v", result)
	}
	if len(result.Rows) != 1 || result.Rows[0].Values["薪资"] != "300" {
		t.Fatalf("expected member-a row with salary, got %#v", result.Rows)
	}
}

func TestQueryStructuredDataMaxAverageAndGroup(t *testing.T) {
	service := newStructuredQueryTestService(t)

	maxResult, _, ok, err := service.QueryStructuredData(model.ChatCompletionRequest{
		DocumentID: "doc-users",
		Messages:   []model.ChatMessage{{Role: "user", Content: "薪资最高的是谁"}},
	})
	if err != nil || !ok {
		t.Fatalf("expected max result, ok=%v err=%v", ok, err)
	}
	if maxResult.Aggregate == nil || maxResult.Aggregate.Operation != "max" || maxResult.Aggregate.Value != 300 || len(maxResult.Rows) != 1 || maxResult.Rows[0].Values["姓名"] != "成员甲" {
		t.Fatalf("unexpected max result: %#v", maxResult)
	}

	avgResult, _, ok, err := service.QueryStructuredData(model.ChatCompletionRequest{
		DocumentID: "doc-users",
		Messages:   []model.ChatMessage{{Role: "user", Content: "平均薪资是多少"}},
	})
	if err != nil || !ok {
		t.Fatalf("expected average result, ok=%v err=%v", ok, err)
	}
	if avgResult.Aggregate == nil || avgResult.Aggregate.Operation != "average" || avgResult.Aggregate.Value != 200 {
		t.Fatalf("unexpected average result: %#v", avgResult)
	}
	if len(avgResult.Rows) != 3 || avgResult.Rows[0].Values["薪资"] != "300" {
		t.Fatalf("expected average evidence rows, got %#v", avgResult.Rows)
	}

	groupResult, _, ok, err := service.QueryStructuredData(model.ChatCompletionRequest{
		DocumentID: "doc-users",
		Messages:   []model.ChatMessage{{Role: "user", Content: "按城市统计分布"}},
	})
	if err != nil || !ok {
		t.Fatalf("expected group result, ok=%v err=%v", ok, err)
	}
	if len(groupResult.Groups) != 2 || groupResult.Groups[0].Value != "城市甲" || groupResult.Groups[0].Count != 2 || groupResult.Groups[1].Value != "城市乙" || groupResult.Groups[1].Count != 1 {
		t.Fatalf("unexpected group result: %#v", groupResult)
	}
	if len(groupResult.Rows) != 3 {
		t.Fatalf("expected group evidence rows, got %#v", groupResult.Rows)
	}
}

func TestStructuredFieldResolverUsesSchemaAliases(t *testing.T) {
	documents := []structuredTableDocument{{
		Tables: []util.StructuredTable{{
			Headers: []string{"学校名称", "成立日期", "更新时间", "工资"},
			Rows: []util.StructuredTableRow{{
				Number: 2,
				Values: []string{"示例机构", "1998", "2024", "180"},
			}},
		}},
	}}

	if field := detectStructuredTargetField("学校的建校时间", documents); field != "成立日期" {
		t.Fatalf("expected schema alias to resolve to 成立日期, got %q matches=%#v", field, structuredFieldMatches("学校的建校时间", documents))
	}
	if field := detectStructuredTargetField("平均工资", documents); field != "工资" {
		t.Fatalf("expected salary alias to resolve to 工资, got %q", field)
	}
}

func TestStructuredFieldResolverRejectsAmbiguousGenericField(t *testing.T) {
	documents := []structuredTableDocument{{
		Tables: []util.StructuredTable{{
			Headers: []string{"学校名称", "成立日期", "更新时间"},
			Rows: []util.StructuredTableRow{{
				Number: 2,
				Values: []string{"示例机构", "1998", "2024"},
			}},
		}},
	}}

	if field := detectStructuredTargetField("时间是多少", documents); field != "" {
		t.Fatalf("expected ambiguous time field to fall back, got %q matches=%#v", field, structuredFieldMatches("时间是多少", documents))
	}
}

func TestStructuredResultChunksCarryStableRowEvidence(t *testing.T) {
	result := StructuredDataQueryResult{
		TotalRows:   1,
		MatchedRows: 1,
		Columns:     []string{"姓名", "工资"},
		Rows: []StructuredDataResultRow{{
			KnowledgeBaseID: "kb-1",
			DocumentID:      "doc-1",
			DocumentName:    "staff.csv",
			RowNumber:       2,
			Values:          map[string]string{"姓名": "成员甲", "工资": "180"},
		}},
	}

	chunks := structuredDataResultChunks(result, nil)
	if len(chunks) != 1 {
		t.Fatalf("expected one structured evidence chunk, got %#v", chunks)
	}
	if chunks[0].EvidenceID == "" || chunks[0].EvidenceID != evidenceIDForChunk(chunks[0].DocumentChunk) {
		t.Fatalf("expected stable structured evidence id, got %#v", chunks[0])
	}
	if chunks[0].TableRow != 2 || len(chunks[0].TableColumns) != 2 {
		t.Fatalf("expected row and table metadata, got %#v", chunks[0])
	}
}

func TestQueryStructuredDataCombinesFilterAndAggregate(t *testing.T) {
	service := newStructuredQueryTestService(t)

	maxResult, _, ok, err := service.QueryStructuredData(model.ChatCompletionRequest{
		DocumentID: "doc-users",
		Messages:   []model.ChatMessage{{Role: "user", Content: "城市甲薪资最高的是谁"}},
	})
	if err != nil || !ok {
		t.Fatalf("expected filtered max result, ok=%v err=%v", ok, err)
	}
	if maxResult.Intent != "max" || maxResult.FilterField != "城市" || maxResult.FilterValue != "城市甲" || maxResult.Aggregate == nil || maxResult.Aggregate.Value != 300 {
		t.Fatalf("unexpected filtered max metadata: %#v", maxResult)
	}
	if len(maxResult.Rows) != 1 || maxResult.Rows[0].Values["姓名"] != "成员甲" {
		t.Fatalf("expected filtered max row, got %#v", maxResult.Rows)
	}

	averageResult, _, ok, err := service.QueryStructuredData(model.ChatCompletionRequest{
		DocumentID: "doc-users",
		Messages:   []model.ChatMessage{{Role: "user", Content: "城市是城市甲薪资平均是多少"}},
	})
	if err != nil || !ok {
		t.Fatalf("expected filtered average result, ok=%v err=%v", ok, err)
	}
	if averageResult.Intent != "average" || averageResult.FilterField != "城市" || averageResult.FilterValue != "城市甲" || averageResult.Aggregate == nil {
		t.Fatalf("unexpected filtered average metadata: %#v", averageResult)
	}
	if averageResult.Aggregate.Value != 200 || averageResult.Aggregate.SampleCount != 2 {
		t.Fatalf("expected filtered average 200 from 2 rows, got %#v", averageResult.Aggregate)
	}
}

func TestQueryStructuredDataFiltersDocumentsByFilename(t *testing.T) {
	service := newStructuredQueryTestService(t)
	dir := filepath.Dir(service.state.KnowledgeBases["kb-1"].Documents[0].Path)
	morePath := filepath.Join(dir, "more-records.csv")
	content := strings.Join([]string{
		"姓名,城市,薪资,年龄",
		"成员丁,城市丙,400,36",
	}, "\n")
	if err := os.WriteFile(morePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write second csv fixture: %v", err)
	}

	kb := service.state.KnowledgeBases["kb-1"]
	kb.Documents = append(kb.Documents, model.Document{
		ID:              "doc-more-users",
		KnowledgeBaseID: "kb-1",
		Name:            "more-records.csv",
		Path:            morePath,
	})
	service.state.KnowledgeBases["kb-1"] = kb

	result, _, ok, err := service.QueryStructuredData(model.ChatCompletionRequest{
		KnowledgeBaseID: "kb-1",
		Messages:        []model.ChatMessage{{Role: "user", Content: "《records.csv》共有多少条数据记录？"}},
	})
	if err != nil || !ok {
		t.Fatalf("expected filename-scoped structured result, ok=%v err=%v", ok, err)
	}
	if result.TotalRows != 3 || result.MatchedRows != 3 {
		t.Fatalf("expected only records.csv rows, got %#v", result)
	}
}

func TestStructuredDocumentsMatchingFilenameDoesNotFallbackForOrdinaryName(t *testing.T) {
	documents := []model.Document{{ID: "doc-generated", Name: "generated-001.csv"}}
	if matches := structuredDocumentsMatchingFilename(documents, "records.csv"); len(matches) != 0 {
		t.Fatalf("expected ordinary filename not to match by extension, got %#v", matches)
	}
	if matches := structuredDocumentsMatchingFilename(documents, "incoming____1.csv"); len(matches) != 1 || matches[0].ID != "doc-generated" {
		t.Fatalf("expected generated filename to use extension fallback, got %#v", matches)
	}
}

func TestQueryStructuredDataAcrossKnowledgeBaseTables(t *testing.T) {
	service := newStructuredQueryTestService(t)
	dir := filepath.Dir(service.state.KnowledgeBases["kb-1"].Documents[0].Path)
	morePath := filepath.Join(dir, "more-records.csv")
	content := strings.Join([]string{
		"姓名,城市,薪资,年龄",
		"成员丁,城市丙,400,36",
	}, "\n")
	if err := os.WriteFile(morePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write second csv fixture: %v", err)
	}

	kb := service.state.KnowledgeBases["kb-1"]
	kb.Documents = append(kb.Documents, model.Document{
		ID:              "doc-more-users",
		KnowledgeBaseID: "kb-1",
		Name:            "more-records.csv",
		Path:            morePath,
	})
	service.state.KnowledgeBases["kb-1"] = kb

	result, _, ok, err := service.QueryStructuredData(model.ChatCompletionRequest{
		KnowledgeBaseID: "kb-1",
		Messages:        []model.ChatMessage{{Role: "user", Content: "谁的工资最高"}},
	})
	if err != nil || !ok {
		t.Fatalf("expected knowledge-base structured result, ok=%v err=%v", ok, err)
	}
	if result.Aggregate == nil || result.Aggregate.Value != 400 || len(result.Rows) != 1 || result.Rows[0].Values["姓名"] != "成员丁" {
		t.Fatalf("expected highest salary across structured documents, got %#v", result)
	}
}

func TestBuildRetrievalContextUsesStructuredEvidence(t *testing.T) {
	service := newStructuredQueryTestService(t)
	service.rag = NewRagService()
	service.queryRewriter = NewLLMQueryRewriter(nil, 3)
	enableQueryRewrite := true
	contextText, sources, err := service.BuildRetrievalContext(model.ChatCompletionRequest{
		DocumentID:         "doc-users",
		EnableQueryRewrite: &enableQueryRewrite,
		Messages:           []model.ChatMessage{{Role: "user", Content: "薪资最高的是谁"}},
	})
	if err != nil {
		t.Fatalf("build retrieval context: %v", err)
	}
	if !strings.Contains(contextText, "字段“薪资”的最大值是300") || !strings.Contains(contextText, "姓名：成员甲") {
		t.Fatalf("expected structured evidence context, got %q", contextText)
	}
	if len(sources) != 1 || sources[0]["chunkKind"] != "structured_query" {
		t.Fatalf("expected structured query source, got %#v", sources)
	}
}

func TestEvaluateRetrieveUsesStructuredDataBeforeVectorSearch(t *testing.T) {
	service := newStructuredQueryTestService(t)
	service.rag = NewRagService()
	chunks, err := service.EvaluateRetrieve(model.ChatCompletionRequest{
		DocumentID: "doc-users",
		Messages:   []model.ChatMessage{{Role: "user", Content: "成员甲的薪资是多少？"}},
	})
	if err != nil {
		t.Fatalf("evaluate retrieve: %v", err)
	}
	if len(chunks) != 1 || chunks[0].Kind != "structured_query" || !strings.Contains(chunks[0].Text, "薪资：300") {
		t.Fatalf("expected deterministic structured chunk, got %#v", chunks)
	}
}

func TestDebugRetrieveUsesStructuredDataBeforeVectorSearch(t *testing.T) {
	service := newStructuredQueryTestService(t)
	service.rag = NewRagService()
	result, err := service.DebugRetrieve(model.RetrievalDebugRequest{
		DocumentID: "doc-users",
		Query:      "成员甲的薪资是多少？",
		TopK:       5,
		Verbose:    true,
	})
	if err != nil {
		t.Fatalf("debug retrieve: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Kind != "structured_query" || result.Items[0].Score != 1 {
		t.Fatalf("expected deterministic structured debug item, got %#v", result.Items)
	}
	if !strings.Contains(result.Items[0].Text, "薪资：300") {
		t.Fatalf("expected structured salary evidence, got %q", result.Items[0].Text)
	}
	if !strings.Contains(strings.Join(result.Items[0].MatchReasons, " "), "结构化确定性结果") {
		t.Fatalf("expected deterministic match reason, got %#v", result.Items[0].MatchReasons)
	}
	var structuredStep *model.RetrievalDebugTraceStep
	for index := range result.Trace {
		if result.Trace[index].Stage == "structured_retrieve" {
			structuredStep = &result.Trace[index]
			break
		}
	}
	if structuredStep == nil || structuredStep.Status != "ok" {
		t.Fatalf("expected successful structured trace, got %#v", result.Trace)
	}
	if result.VerboseDetails == nil || result.VerboseDetails.VectorSearchMs != 0 {
		t.Fatalf("expected structured debug to skip vector search, got %#v", result.VerboseDetails)
	}
}

func TestDebugRetrieveVerboseSkipsVectorWhenQdrantDisabled(t *testing.T) {
	service := newStructuredQueryTestService(t)
	service.rag = NewRagService()
	result, err := service.DebugRetrieve(model.RetrievalDebugRequest{
		DocumentID: "doc-users",
		Query:      "这是一条普通段落问题",
		Verbose:    true,
	})
	if err != nil {
		t.Fatalf("debug retrieve without qdrant: %v", err)
	}
	if result.Count != 0 || result.VerboseDetails == nil {
		t.Fatalf("expected empty diagnostic result without qdrant, got %#v", result)
	}
}

func TestEvaluateRetrieveFallsBackWhenStructuredFileMissing(t *testing.T) {
	service := newStructuredQueryTestService(t)
	service.rag = NewRagService()
	kb := service.state.KnowledgeBases["kb-1"]
	kb.Documents[0].Path = filepath.Join(t.TempDir(), "missing.csv")
	service.state.KnowledgeBases["kb-1"] = kb

	chunks, err := service.EvaluateRetrieve(model.ChatCompletionRequest{
		DocumentID: "doc-users",
		Messages:   []model.ChatMessage{{Role: "user", Content: "成员甲的薪资是多少？"}},
	})
	if err != nil {
		t.Fatalf("expected structured parse error to fall back, got %v", err)
	}
	if len(chunks) != 0 {
		t.Fatalf("expected no vector chunks without qdrant, got %#v", chunks)
	}
}

func TestBuildRetrievalDebugEvalCandidateFromLowConfidence(t *testing.T) {
	candidate := buildRetrievalDebugEvalCandidate(
		model.ChatCompletionRequest{KnowledgeBaseID: "kb-1"},
		"人员薪资最高是谁",
		true,
		[]RetrievedChunk{{
			DocumentChunk: DocumentChunk{
				ID:              "doc-users-source-rows-0",
				KnowledgeBaseID: "kb-1",
				DocumentID:      "doc-users",
				DocumentName:    "records.csv",
				Text:            "第2行：姓名：成员甲。薪资：300。",
			},
			Score: 0.12,
		}},
		"[records.csv#1] 第2行：姓名：成员甲。薪资：300。",
	)
	if candidate == nil {
		t.Fatal("expected eval candidate")
	}
	if candidate.Question != "人员薪资最高是谁" {
		t.Fatalf("unexpected question: %q", candidate.Question)
	}
	if candidate.AnswerType != "retrieval-debug-candidate" || candidate.Difficulty != "hard" {
		t.Fatalf("unexpected eval metadata: %#v", candidate)
	}
	if len(candidate.SourceDocuments) != 1 || candidate.SourceDocuments[0].ChunkID != "doc-users-source-rows-0" {
		t.Fatalf("unexpected sources: %#v", candidate.SourceDocuments)
	}
}

func newStructuredQueryTestService(t *testing.T) *AppService {
	t.Helper()
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "records.csv")
	content := strings.Join([]string{
		"姓名,城市,薪资,年龄",
		"成员甲,城市甲,300,41",
		"成员乙,城市乙,200,32",
		"成员丙,城市甲,100,27",
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
						Name:            "records.csv",
						Path:            csvPath,
					}},
				},
			},
		},
	}
}
